package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Every password in this file is generated or literal test data — none of it
// is, or has ever been, a real credential.
const testPassword = "correct-horse-battery"

func TestCreateAndGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	created, err := s.CreateUser(ctx, "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == 0 {
		t.Error("created user has ID 0")
	}
	if created.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", created.Role, RoleAdmin)
	}
	if created.Disabled {
		t.Error("new user is disabled, want enabled")
	}
	if created.LastLogin != nil {
		t.Errorf("new user has last_login = %v, want nil", created.LastLogin)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}

	got, err := s.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got == nil {
		t.Fatal("GetUserByUsername returned nil for an existing user")
	}
	if got.ID != created.ID || got.Username != "alice" {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte(testPassword)) != nil {
		t.Error("stored hash does not verify against the password it was created with")
	}

	// Lookup is case-insensitive, matching the COLLATE NOCASE index.
	if u, err := s.GetUserByUsername(ctx, "ALICE"); err != nil || u == nil {
		t.Errorf("GetUserByUsername(%q) = %v, %v; want the alice row", "ALICE", u, err)
	}

	// A missing user is (nil, nil), never sql.ErrNoRows.
	missing, err := s.GetUserByUsername(ctx, "nobody")
	if err != nil {
		t.Errorf("GetUserByUsername(missing) error = %v, want nil", err)
	}
	if missing != nil {
		t.Errorf("GetUserByUsername(missing) = %+v, want nil", missing)
	}

	byID, err := s.GetUserByID(ctx, created.ID)
	if err != nil || byID == nil {
		t.Fatalf("GetUserByID = %v, %v", byID, err)
	}
	if none, err := s.GetUserByID(ctx, 99999); err != nil || none != nil {
		t.Errorf("GetUserByID(missing) = %v, %v; want nil, nil", none, err)
	}
}

// TestUserJSONNeverLeaksHash is the guarantee behind the `json:"-"` tag on
// User.PasswordHash: no API response can ever carry a password hash. If this
// test fails, the tag was removed or renamed — restore it, do not relax the
// assertion.
func TestUserJSONNeverLeaksHash(t *testing.T) {
	s := newTestStore(t)

	u, err := s.CreateUser(t.Context(), "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.PasswordHash == "" {
		t.Fatal("PasswordHash is empty; the test would pass vacuously")
	}

	encoded, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(encoded)

	for _, needle := range []string{"password_hash", "passwordHash", "PasswordHash", u.PasswordHash, "$2a$"} {
		if strings.Contains(got, needle) {
			t.Errorf("marshalled user contains %q: %s", needle, got)
		}
	}
	if !strings.Contains(got, `"username":"alice"`) {
		t.Errorf("marshalled user lost the username: %s", got)
	}
}

func TestCreateUserRejectsDuplicates(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "admin", testPassword, RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := s.CreateUser(ctx, "admin", testPassword, RoleAdmin); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("duplicate username error = %v, want ErrUsernameTaken", err)
	}

	// COLLATE NOCASE: differing case is the same account.
	if _, err := s.CreateUser(ctx, "Admin", testPassword, RoleAdmin); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("case-variant username error = %v, want ErrUsernameTaken", err)
	}
}

func TestCreateUserRejectsInvalidRole(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateUser(t.Context(), "someone", testPassword, "superuser"); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("error = %v, want ErrInvalidRole", err)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		pass    string
		wantErr bool
	}{
		{"9 characters is too short", strings.Repeat("a", 9), true},
		{"10 characters is the minimum", strings.Repeat("a", 10), false},
		{"72 bytes is the bcrypt limit", strings.Repeat("a", 72), false},
		{"73 bytes exceeds bcrypt's limit", strings.Repeat("a", 73), true},
		{"multi-byte runes count as bytes", strings.Repeat("é", 37), true}, // 74 bytes
		{"all whitespace is rejected", strings.Repeat(" ", 12), true},
		{"empty is rejected", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.pass)
			if tt.wantErr {
				if !errors.Is(err, ErrWeakPassword) {
					t.Errorf("ValidatePassword(%d bytes) = %v, want ErrWeakPassword", len(tt.pass), err)
				}
				return
			}
			if err != nil {
				t.Errorf("ValidatePassword(%d bytes) = %v, want nil", len(tt.pass), err)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		wantErr bool
	}{
		{"plain name", "alice", false},
		{"dots, dashes, underscores, at", "a.b-c_d@e", false},
		{"digits", "user123", false},
		{"64 characters is the maximum", strings.Repeat("a", 64), false},
		{"65 characters is too long", strings.Repeat("a", 65), true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"colon is the legacy separator", "a:b", true},
		{"comma is the legacy separator", "a,b", true},
		{"inner space", "a b", true},
		{"slash", "a/b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.user)
			if tt.wantErr && !errors.Is(err, ErrInvalidUsername) {
				t.Errorf("ValidateUsername(%q) = %v, want ErrInvalidUsername", tt.user, err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateUsername(%q) = %v, want nil", tt.user, err)
			}
		})
	}
}

func TestSetPassword(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	oldHash := u.PasswordHash

	const newPassword = "a-completely-different-one"
	if err := s.SetPassword(ctx, u.ID, newPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	updated, err := s.GetUserByID(ctx, u.ID)
	if err != nil || updated == nil {
		t.Fatalf("GetUserByID: %v, %v", updated, err)
	}
	if updated.PasswordHash == oldHash {
		t.Error("password hash unchanged after SetPassword")
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(newPassword)) != nil {
		t.Error("new password does not verify against the stored hash")
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(testPassword)) == nil {
		t.Error("old password still verifies after SetPassword")
	}

	// Validation runs before hashing.
	if err := s.SetPassword(ctx, u.ID, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("SetPassword(short) = %v, want ErrWeakPassword", err)
	}

	if err := s.SetPassword(ctx, 99999, testPassword); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("SetPassword(missing id) = %v, want ErrUserNotFound", err)
	}
}

func TestCountEnabledAdmins(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	admin1, err := s.CreateUser(ctx, "admin1", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(admin1): %v", err)
	}
	admin2, err := s.CreateUser(ctx, "admin2", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(admin2): %v", err)
	}
	if _, err := s.CreateUser(ctx, "viewer1", testPassword, RoleViewer); err != nil {
		t.Fatalf("CreateUser(viewer1): %v", err)
	}

	assertAdmins := func(want int) {
		t.Helper()
		got, err := s.CountEnabledAdmins(ctx)
		if err != nil {
			t.Fatalf("CountEnabledAdmins: %v", err)
		}
		if got != want {
			t.Errorf("CountEnabledAdmins = %d, want %d", got, want)
		}
	}

	assertAdmins(2) // the viewer must not count

	if err := s.SetDisabled(ctx, admin1.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	assertAdmins(1)

	if err := s.SetDisabled(ctx, admin1.ID, false); err != nil {
		t.Fatalf("SetDisabled(false): %v", err)
	}
	assertAdmins(2)

	if err := s.SetRole(ctx, admin1.ID, RoleViewer); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	assertAdmins(1)

	if err := s.DeleteUser(ctx, admin2.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	assertAdmins(0)

	if err := s.DeleteUser(ctx, admin2.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("DeleteUser(already deleted) = %v, want ErrUserNotFound", err)
	}
}

func TestSetDisabledAndRole(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: %v, %v", got, err)
	}
	if !got.Disabled {
		t.Error("Disabled = false after SetDisabled(true)")
	}

	if err := s.SetRole(ctx, u.ID, "root"); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("SetRole(root) = %v, want ErrInvalidRole", err)
	}
	if err := s.SetRole(ctx, u.ID, RoleViewer); err != nil {
		t.Fatalf("SetRole(viewer): %v", err)
	}
	got, err = s.GetUserByID(ctx, u.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: %v, %v", got, err)
	}
	if got.Role != RoleViewer {
		t.Errorf("role = %q, want %q", got.Role, RoleViewer)
	}
}

func TestRename(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	alice, err := s.CreateUser(ctx, "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if _, err := s.CreateUser(ctx, "bob", testPassword, RoleAdmin); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}

	if err := s.Rename(ctx, alice.ID, "alice2"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if u, err := s.GetUserByUsername(ctx, "alice2"); err != nil || u == nil {
		t.Fatalf("GetUserByUsername(alice2) = %v, %v", u, err)
	}

	if err := s.Rename(ctx, alice.ID, "BOB"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("Rename to an existing name (different case) = %v, want ErrUsernameTaken", err)
	}
	if err := s.Rename(ctx, alice.ID, "no:colons"); !errors.Is(err, ErrInvalidUsername) {
		t.Errorf("Rename to an invalid name = %v, want ErrInvalidUsername", err)
	}
}

func TestTouchLastLogin(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	u, err := s.CreateUser(ctx, "alice", testPassword, RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.LastLogin != nil {
		t.Fatalf("new user has last_login = %v, want nil", u.LastLogin)
	}

	if err := s.TouchLastLogin(ctx, u.ID); err != nil {
		t.Fatalf("TouchLastLogin: %v", err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUserByID: %v, %v", got, err)
	}
	if got.LastLogin == nil {
		t.Fatal("last_login still nil after TouchLastLogin")
	}
	if got.LastLogin.IsZero() {
		t.Error("last_login is the zero time")
	}

	if err := s.TouchLastLogin(ctx, 99999); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("TouchLastLogin(missing id) = %v, want ErrUserNotFound", err)
	}
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if users, err := s.ListUsers(ctx); err != nil {
		t.Fatalf("ListUsers on empty store: %v", err)
	} else if len(users) != 0 {
		t.Errorf("ListUsers on empty store returned %d rows", len(users))
	}

	for _, name := range []string{"carol", "alice", "bob"} {
		if _, err := s.CreateUser(ctx, name, testPassword, RoleAdmin); err != nil {
			t.Fatalf("CreateUser(%s): %v", name, err)
		}
	}

	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	want := []string{"alice", "bob", "carol"}
	if len(users) != len(want) {
		t.Fatalf("ListUsers returned %d rows, want %d", len(users), len(want))
	}
	for i, name := range want {
		if users[i].Username != name {
			t.Errorf("users[%d].Username = %q, want %q (must be ordered by username)", i, users[i].Username, name)
		}
	}
}

// TestCreateFirstAdmin covers the happy path: an empty instance gets exactly
// one administrator, and the password it was given actually works.
func TestCreateFirstAdmin(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	u, err := CreateFirstAdmin(ctx, s, "alice", testPassword)
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if u.ID == 0 {
		t.Error("first administrator has ID 0")
	}
	if u.Username != "alice" {
		t.Errorf("username = %q, want %q", u.Username, "alice")
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q — the first account must be able to administer the instance", u.Role, RoleAdmin)
	}
	if u.Disabled {
		t.Error("first administrator is disabled, want enabled")
	}
	if u.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(testPassword)); err != nil {
		t.Errorf("stored hash does not verify against the given password: %v", err)
	}

	n, err := s.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

// TestCreateFirstAdminIsOneShot is the security contract behind the
// unauthenticated POST /setup: once any user exists the wizard is closed, and
// a second call must neither create a row nor overwrite the existing one.
func TestCreateFirstAdminIsOneShot(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if _, err := CreateFirstAdmin(ctx, s, "alice", testPassword); err != nil {
		t.Fatalf("first CreateFirstAdmin: %v", err)
	}

	_, err := CreateFirstAdmin(ctx, s, "mallory", "second-attempt-password")
	if !errors.Is(err, ErrSetupAlreadyDone) {
		t.Fatalf("second CreateFirstAdmin error = %v, want ErrSetupAlreadyDone", err)
	}

	n, err := s.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1 — the rejected call must not have inserted anything", n)
	}
	if u, err := s.GetUserByUsername(ctx, "mallory"); err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	} else if u != nil {
		t.Error("the rejected setup attempt created a user")
	}
}

// TestCreateFirstAdminAfterCreateUser: the guard counts users, not "users
// created by the wizard", so an instance seeded any other way (the legacy
// AUTH_USER_PASS import, for one) is already set up.
func TestCreateFirstAdminAfterCreateUser(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "viewer", testPassword, RoleViewer); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := CreateFirstAdmin(ctx, s, "alice", testPassword); !errors.Is(err, ErrSetupAlreadyDone) {
		t.Fatalf("error = %v, want ErrSetupAlreadyDone", err)
	}
}

// TestCreateFirstAdminValidates: bad input is rejected before anything is
// written, so a failed wizard submission leaves the instance still setup-able.
func TestCreateFirstAdminValidates(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{"short password", "alice", "short", ErrWeakPassword},
		{"empty password", "alice", "", ErrWeakPassword},
		{"whitespace password", "alice", "           ", ErrWeakPassword},
		{"empty username", "", testPassword, ErrInvalidUsername},
		{"username with a colon", "alice:bob", testPassword, ErrInvalidUsername},
		{"username with a space", "alice bob", testPassword, ErrInvalidUsername},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := t.Context()

			if _, err := CreateFirstAdmin(ctx, s, tt.username, tt.password); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}

			n, err := s.CountUsers(ctx)
			if err != nil {
				t.Fatalf("CountUsers: %v", err)
			}
			if n != 0 {
				t.Errorf("CountUsers = %d, want 0 — a rejected submission must not create anything", n)
			}
		})
	}
}

// TestCreateFirstAdminIsAtomic is the race a count-then-insert would lose:
// several callers bootstrap the same empty instance at once and exactly one
// may win. Without the transaction, two could both read a count of 0.
func TestCreateFirstAdminIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	const racers = 8

	var (
		start   sync.WaitGroup
		done    sync.WaitGroup
		mu      sync.Mutex
		created []string
		other   []error
	)
	start.Add(1)
	done.Add(racers)

	for i := range racers {
		go func(i int) {
			defer done.Done()
			start.Wait()

			u, err := CreateFirstAdmin(ctx, s, fmt.Sprintf("admin%d", i), testPassword)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created = append(created, u.Username)
			case errors.Is(err, ErrSetupAlreadyDone):
				// the expected outcome for every loser
			default:
				other = append(other, err)
			}
		}(i)
	}

	start.Done()
	done.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors from concurrent setup: %v", other)
	}
	if len(created) != 1 {
		t.Fatalf("%d concurrent calls succeeded (%v), want exactly 1", len(created), created)
	}

	n, err := s.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

// TestCreateFirstAdminWithoutStore: a request that arrives before the database
// is open must report a failure rather than panic.
func TestCreateFirstAdminWithoutStore(t *testing.T) {
	if _, err := CreateFirstAdmin(t.Context(), nil, "alice", testPassword); !errors.Is(err, ErrNoStore) {
		t.Fatalf("error = %v, want ErrNoStore", err)
	}
}
