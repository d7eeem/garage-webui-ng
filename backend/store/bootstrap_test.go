package store

import (
	"context"
	"fmt"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// hashFor produces a bcrypt hash the way an operator's AUTH_USER_PASS would
// have. Cost is MinCost to keep the tests fast; nothing here is a real
// credential.
func hashFor(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword: %v", err)
	}
	return string(h)
}

func TestParseUserPass(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{"single entry", "u:h", map[string]string{"u": "h"}},
		{"two entries", "a:h1,b:h2", map[string]string{"a": "h1", "b": "h2"}},
		{"whitespace around entries trimmed", " a:h1 , b:h2 ", map[string]string{"a": "h1", "b": "h2"}},
		{"empty raw yields no entries", "", map[string]string{}},
		{
			name: "malformed entries skipped: no colon, empty user, empty hash, blank",
			raw:  "noColon,:h,u:,,a:h1",
			want: map[string]string{"a": "h1"},
		},
		{"hash containing $ kept intact", "u:$2y$10$abc", map[string]string{"u": "$2y$10$abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseUserPass(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseUserPass(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("ParseUserPass(%q)[%q] = %q, ok=%v; want %q", tt.raw, k, gotV, ok, wantV)
				}
			}
		})
	}
}

// TestImportLegacyUsersStoresHashVerbatim is the core of the migration
// contract: AUTH_USER_PASS already holds bcrypt hashes, so re-hashing them
// would lock every existing operator out of their own deployment.
func TestImportLegacyUsersStoresHashVerbatim(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	const password = "the-original-password"
	hash := hashFor(t, password)

	n, err := ImportLegacyUsers(ctx, s, "admin:"+hash, "")
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d users, want 1", n)
	}

	u, err := s.GetUserByUsername(ctx, "admin")
	if err != nil || u == nil {
		t.Fatalf("GetUserByUsername(admin) = %v, %v", u, err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, RoleAdmin)
	}
	if u.PasswordHash != hash {
		t.Errorf("hash was not stored verbatim")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		t.Error("the original password no longer validates against the imported hash")
	}
}

func TestImportLegacyUsersRoles(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	adminHash := hashFor(t, "admin-password-1")
	viewerHash := hashFor(t, "viewer-password-2")

	n, err := ImportLegacyUsers(ctx, s,
		fmt.Sprintf("admin:%s", adminHash),
		fmt.Sprintf("viewer:%s", viewerHash))
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d users, want 2", n)
	}

	for name, wantRole := range map[string]string{"admin": RoleAdmin, "viewer": RoleViewer} {
		u, err := s.GetUserByUsername(ctx, name)
		if err != nil || u == nil {
			t.Fatalf("GetUserByUsername(%s) = %v, %v", name, u, err)
		}
		if u.Role != wantRole {
			t.Errorf("%s: role = %q, want %q", name, u.Role, wantRole)
		}
	}
}

// TestImportLegacyUsersAdminWinsOnConflict: the same name in both variables
// must resolve to one row, and to the more capable role, rather than failing
// the whole import on a UNIQUE violation.
func TestImportLegacyUsersAdminWinsOnConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	adminHash := hashFor(t, "admin-password-1")
	viewerHash := hashFor(t, "viewer-password-2")

	n, err := ImportLegacyUsers(ctx, s, "dave:"+adminHash, "Dave:"+viewerHash)
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d users, want 1", n)
	}

	u, err := s.GetUserByUsername(ctx, "dave")
	if err != nil || u == nil {
		t.Fatalf("GetUserByUsername(dave) = %v, %v", u, err)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q (admin entries win)", u.Role, RoleAdmin)
	}
}

// TestImportLegacyUsersIsIdempotent is the guarantee that makes the database
// authoritative: once it holds any user, the environment variables are never
// read again, so a stale AUTH_USER_PASS cannot revert a changed password or
// resurrect a deleted account on the next restart.
func TestImportLegacyUsersIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	legacyHash := hashFor(t, "legacy-password-1")
	if n, err := ImportLegacyUsers(ctx, s, "admin:"+legacyHash, ""); err != nil || n != 1 {
		t.Fatalf("first import = %d, %v; want 1, nil", n, err)
	}

	u, err := s.GetUserByUsername(ctx, "admin")
	if err != nil || u == nil {
		t.Fatalf("GetUserByUsername: %v, %v", u, err)
	}

	// The operator changes their password inside the app...
	const newPassword = "chosen-in-the-app"
	if err := s.SetPassword(ctx, u.ID, newPassword); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	// ...and restarts with AUTH_USER_PASS still set to the old hash.
	n, err := ImportLegacyUsers(ctx, s, "admin:"+legacyHash, "")
	if err != nil {
		t.Fatalf("second ImportLegacyUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("second import returned %d, want 0", n)
	}

	after, err := s.GetUserByUsername(ctx, "admin")
	if err != nil || after == nil {
		t.Fatalf("GetUserByUsername: %v, %v", after, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte(newPassword)) != nil {
		t.Error("the password reverted to the AUTH_USER_PASS hash; the import is not idempotent")
	}
	if count, err := s.CountUsers(ctx); err != nil || count != 1 {
		t.Errorf("CountUsers = %d, %v; want 1, nil", count, err)
	}
}

// TestImportLegacyUsersSkipsNonEmptyDatabase covers a deployment that already
// created its users through the app: a newly-set AUTH_USER_PASS must not
// inject an extra account.
func TestImportLegacyUsersSkipsNonEmptyDatabase(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if _, err := s.CreateUser(ctx, "existing", "correct-horse-battery", RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	n, err := ImportLegacyUsers(ctx, s, "intruder:"+hashFor(t, "intruder-password"), "")
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d users into a populated database, want 0", n)
	}
	if u, err := s.GetUserByUsername(ctx, "intruder"); err != nil || u != nil {
		t.Errorf("GetUserByUsername(intruder) = %v, %v; want nil, nil", u, err)
	}
}

func TestImportLegacyUsersSkipsMalformedEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	good := hashFor(t, "a-good-password")
	raw := "nocolon," + // no separator
		"empty:," + // empty hash
		":orphan," + // empty username
		"plaintext:not-a-bcrypt-hash," + // wrong algorithm
		"md5user:$1$abcdefgh$xyz," + // a different hash family
		"good:" + good

	n, err := ImportLegacyUsers(ctx, s, raw, "")
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d users, want 1 (only the bcrypt entry)", n)
	}

	for _, name := range []string{"nocolon", "empty", "orphan", "plaintext", "md5user"} {
		if u, err := s.GetUserByUsername(ctx, name); err != nil || u != nil {
			t.Errorf("GetUserByUsername(%s) = %v, %v; want nil, nil", name, u, err)
		}
	}
	if u, err := s.GetUserByUsername(ctx, "good"); err != nil || u == nil {
		t.Errorf("GetUserByUsername(good) = %v, %v; want the imported row", u, err)
	}
}

func TestImportLegacyUsersAcceptsAllBcryptPrefixes(t *testing.T) {
	s := newTestStore(t)

	// x/crypto/bcrypt only emits $2a$, so $2b$/$2y$ (produced by htpasswd and
	// by PHP) are spelled out here as literal prefixes over a real digest.
	base := hashFor(t, "a-good-password")
	raw := fmt.Sprintf("a2a:%s,a2b:$2b$%s,a2y:$2y$%s", base, base[4:], base[4:])

	n, err := ImportLegacyUsers(t.Context(), s, raw, "")
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 3 {
		t.Errorf("imported %d users, want 3 ($2a$, $2b$ and $2y$ must all be accepted)", n)
	}
}

func TestImportLegacyUsersNoEnv(t *testing.T) {
	s := newTestStore(t)

	n, err := ImportLegacyUsers(t.Context(), s, "", "")
	if err != nil {
		t.Fatalf("ImportLegacyUsers: %v", err)
	}
	if n != 0 {
		t.Errorf("imported %d users with no environment set, want 0", n)
	}
}

func TestImportLegacyUsersNilStore(t *testing.T) {
	if _, err := ImportLegacyUsers(context.Background(), nil, "admin:$2a$10$x", ""); err == nil {
		t.Error("ImportLegacyUsers(nil store) = nil error, want an error")
	}
}
