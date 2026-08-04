package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/store"

	"golang.org/x/crypto/bcrypt"
)

// bcryptPrefixes are the hash markers that must never reach any CLI output.
var bcryptPrefixes = []string{"$2a$", "$2b$", "$2y$"}

// cliTempDB points DB_PATH at a fresh database inside the test's temp dir and
// returns its path.
func cliTempDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.db")
	t.Setenv("DB_PATH", path)
	return path
}

// cliOpenStore opens the temp database directly, for seeding and assertions.
// The pool holds a single connection, so callers must close it before running
// a command that opens the same file.
func cliOpenStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("cannot open test database: %v", err)
	}
	return st
}

// cliSeedUser creates an account in the temp database and closes the store
// again so the command under test can open it.
func cliSeedUser(t *testing.T, path, username, password, role string) {
	t.Helper()
	st := cliOpenStore(t, path)
	defer st.Close()
	if _, err := st.CreateUser(context.Background(), username, password, role); err != nil {
		t.Fatalf("cannot seed user %q: %v", username, err)
	}
}

// cliPasswordHash reads back the stored hash so a test can verify what the
// command actually wrote.
func cliPasswordHash(t *testing.T, path, username string) string {
	t.Helper()
	st := cliOpenStore(t, path)
	defer st.Close()
	u, err := st.GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("cannot load user %q: %v", username, err)
	}
	if u == nil {
		t.Fatalf("user %q does not exist", username)
	}
	return u.PasswordHash
}

// cliUser returns the stored account, or nil when it does not exist.
func cliUser(t *testing.T, path, username string) *store.User {
	t.Helper()
	st := cliOpenStore(t, path)
	defer st.Close()
	u, err := st.GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("cannot load user %q: %v", username, err)
	}
	return u
}

// cliRun executes one command with stdin fed from a pipe (so promptPassword
// takes the non-TTY branch) and stdout+stderr captured. It returns the exit
// code and everything the command printed.
func cliRun(t *testing.T, stdin string, fn func() int) (int, string) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("cannot create stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("cannot create stdout pipe: %v", err)
	}

	origIn, origOut, origErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, outW

	collected := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, outR)
		collected <- buf.String()
	}()

	go func() {
		defer inW.Close()
		_, _ = io.WriteString(inW, stdin)
	}()

	code := fn()

	os.Stdin, os.Stdout, os.Stderr = origIn, origOut, origErr
	outW.Close()
	out := <-collected
	outR.Close()
	inR.Close()

	return code, out
}

// assertNoSecrets fails when a captured output leaks a password or a hash.
func assertNoSecrets(t *testing.T, out string, passwords ...string) {
	t.Helper()
	for _, p := range bcryptPrefixes {
		if strings.Contains(out, p) {
			t.Errorf("output contains a bcrypt hash prefix %q:\n%s", p, out)
		}
	}
	for _, p := range passwords {
		if p != "" && strings.Contains(out, p) {
			t.Errorf("output contains a plaintext password:\n%s", out)
		}
	}
}

func TestCLIListUsersPrintsNoHashes(t *testing.T) {
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", "correct-horse-battery", store.RoleAdmin)
	cliSeedUser(t, path, "bob", "correct-horse-battery", store.RoleViewer)

	code, out := cliRun(t, "", runListUsers)
	if code != 0 {
		t.Fatalf("runListUsers exit = %d, want 0\n%s", code, out)
	}

	for _, want := range []string{"alice", "bob", store.RoleAdmin, store.RoleViewer, "active", "USERNAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	assertNoSecrets(t, out, "correct-horse-battery")
}

func TestCLIListUsersOnEmptyDatabase(t *testing.T) {
	cliTempDB(t)

	code, out := cliRun(t, "", runListUsers)
	if code != 0 {
		t.Fatalf("runListUsers exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "No accounts") {
		t.Errorf("output does not report an empty database:\n%s", out)
	}
}

func TestCLIResetPasswordUnknownUserFails(t *testing.T) {
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", "correct-horse-battery", store.RoleAdmin)

	code, out := cliRun(t, "another-good-password\n", func() int { return runResetPassword("nope") })
	if code != 1 {
		t.Fatalf("runResetPassword exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "no such user") {
		t.Errorf("output does not explain the failure:\n%s", out)
	}
	assertNoSecrets(t, out, "another-good-password")
}

func TestCLIResetPasswordReplacesTheCredential(t *testing.T) {
	const (
		oldPassword = "correct-horse-battery"
		newPassword = "another-good-password"
	)
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", oldPassword, store.RoleAdmin)

	code, out := cliRun(t, newPassword+"\n", func() int { return runResetPassword("alice") })
	if code != 0 {
		t.Fatalf("runResetPassword exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("output does not confirm the account:\n%s", out)
	}
	assertNoSecrets(t, out, oldPassword, newPassword)

	hash := cliPasswordHash(t, path, "alice")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(newPassword)); err != nil {
		t.Errorf("new password does not verify against the stored hash: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)); err == nil {
		t.Error("old password still verifies against the stored hash")
	}
}

func TestCLIResetPasswordRejectsWeakPassword(t *testing.T) {
	const oldPassword = "correct-horse-battery"
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", oldPassword, store.RoleAdmin)
	before := cliPasswordHash(t, path, "alice")

	code, out := cliRun(t, "short\n", func() int { return runResetPassword("alice") })
	if code != 1 {
		t.Fatalf("runResetPassword exit = %d, want 1\n%s", code, out)
	}
	assertNoSecrets(t, out, "short")

	after := cliPasswordHash(t, path, "alice")
	if after != before {
		t.Error("a rejected password changed the stored hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(after), []byte(oldPassword)); err != nil {
		t.Errorf("original password no longer verifies: %v", err)
	}
}

func TestCLIResetPasswordRejectsEmptyStdin(t *testing.T) {
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", "correct-horse-battery", store.RoleAdmin)
	before := cliPasswordHash(t, path, "alice")

	code, out := cliRun(t, "", func() int { return runResetPassword("alice") })
	if code != 1 {
		t.Fatalf("runResetPassword exit = %d, want 1\n%s", code, out)
	}
	if after := cliPasswordHash(t, path, "alice"); after != before {
		t.Error("an empty password changed the stored hash")
	}
}

func TestCLICreateAdminCreatesAnAdministrator(t *testing.T) {
	const password = "recovery-password-1"
	path := cliTempDB(t)

	code, out := cliRun(t, password+"\n", func() int { return runCreateAdmin("recovery") })
	if code != 0 {
		t.Fatalf("runCreateAdmin exit = %d, want 0\n%s", code, out)
	}
	assertNoSecrets(t, out, password)

	u := cliUser(t, path, "recovery")
	if u == nil {
		t.Fatal("account was not created")
	}
	if u.Role != store.RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, store.RoleAdmin)
	}
	if u.Disabled {
		t.Error("new administrator is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		t.Errorf("password does not verify against the stored hash: %v", err)
	}
}

func TestCLICreateAdminWorksWhenUsersAlreadyExist(t *testing.T) {
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", "correct-horse-battery", store.RoleAdmin)

	code, out := cliRun(t, "recovery-password-1\n", func() int { return runCreateAdmin("recovery") })
	if code != 0 {
		t.Fatalf("runCreateAdmin exit = %d, want 0\n%s", code, out)
	}
	if u := cliUser(t, path, "recovery"); u == nil {
		t.Fatal("recovery account was not created alongside the existing admin")
	}
}

func TestCLICreateAdminRejectsDuplicateUsername(t *testing.T) {
	path := cliTempDB(t)
	cliSeedUser(t, path, "alice", "correct-horse-battery", store.RoleAdmin)
	before := cliPasswordHash(t, path, "alice")

	code, out := cliRun(t, "recovery-password-1\n", func() int { return runCreateAdmin("alice") })
	if code != 1 {
		t.Fatalf("runCreateAdmin exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("output does not explain the failure:\n%s", out)
	}
	assertNoSecrets(t, out, "recovery-password-1")

	if after := cliPasswordHash(t, path, "alice"); after != before {
		t.Error("a rejected duplicate overwrote the existing account")
	}

	st := cliOpenStore(t, path)
	defer st.Close()
	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("cannot list users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("user count = %d, want 1 (no duplicate created)", len(users))
	}
}

func TestCLICreateAdminRejectsInvalidUsername(t *testing.T) {
	cliTempDB(t)

	code, out := cliRun(t, "recovery-password-1\n", func() int { return runCreateAdmin("bad name!") })
	if code != 1 {
		t.Fatalf("runCreateAdmin exit = %d, want 1\n%s", code, out)
	}
	assertNoSecrets(t, out, "recovery-password-1")
}
