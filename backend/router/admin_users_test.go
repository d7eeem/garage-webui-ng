package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/middleware"
	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"

	"golang.org/x/crypto/bcrypt"
)

// bcryptPrefixes are the markers a stored password hash always starts with.
// Every assertion that a response body is clean greps for these on the RAW
// body rather than on a decoded struct: a decoded struct can only show fields
// the test already knows about, whereas the raw body catches a hash that has
// crept in under any name at all.
var bcryptPrefixes = []string{"$2a$", "$2b$", "$2y$"}

// adminSession is the identity a test request carries. A nil *adminSession
// means an anonymous caller.
type adminSession struct {
	username string
	role     string
}

// adminUsersHandler serves the five administration routes through the scs
// session middleware with the given identity already in the session.
//
// It deliberately leaves out AuthMiddleware and CSRF so that these tests
// exercise requireAdmin — the in-handler backstop — on its own. The outer
// middleware boundary is covered end-to-end by
// TestAdminUsersAuthorizationEndToEnd and by TestIsViewerAllowed.
func adminUsersHandler(sess *adminSession) http.Handler {
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	au := &AdminUsers{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/users", au.List)
	mux.HandleFunc("POST /admin/users", au.Create)
	mux.HandleFunc("PATCH /admin/users/{id}", au.Update)
	mux.HandleFunc("DELETE /admin/users/{id}", au.Delete)
	mux.HandleFunc("POST /admin/users/{id}/reset-password", au.ResetPassword)

	return sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess != nil {
			utils.Session.Set(r, "authenticated", true)
			utils.Session.Set(r, "username", sess.username)
			utils.Session.Set(r, "role", sess.role)
		}
		mux.ServeHTTP(w, r)
	}))
}

// callAdmin issues one request against an admin handler. A nil body sends no
// body at all, which is what a DELETE looks like.
func callAdmin(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// seedUser creates a user and fails the test if it cannot.
func seedUser(t *testing.T, st *store.Store, username, password, role string) *store.User {
	t.Helper()
	u, err := st.CreateUser(t.Context(), username, password, role)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return u
}

// userSnapshot captures every field a lockout guard could change — including
// the password hash, so a stray reset would show up too. Comparing a snapshot
// taken before a refused request with one taken after is how these tests prove
// that a 409 really did leave the database alone.
func userSnapshot(t *testing.T, st *store.Store) map[string]string {
	t.Helper()

	users, err := st.ListUsers(t.Context())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}

	snap := make(map[string]string, len(users))
	for _, u := range users {
		snap[u.Username] = fmt.Sprintf("id=%d role=%s disabled=%v hash=%s",
			u.ID, u.Role, u.Disabled, u.PasswordHash)
	}
	return snap
}

// assertUnchanged is the assertion that matters most in this file: a refused
// privileged write must be a no-op, not a partial one.
func assertUnchanged(t *testing.T, st *store.Store, before map[string]string) {
	t.Helper()
	after := userSnapshot(t, st)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the database changed despite a refused request:\nbefore: %v\nafter:  %v", before, after)
	}
}

// assertNoSecrets fails if a response body carries a password or a hash. Run
// it against every body these handlers produce.
func assertNoSecrets(t *testing.T, body string, passwords ...string) {
	t.Helper()

	for _, prefix := range bcryptPrefixes {
		if strings.Contains(body, prefix) {
			t.Errorf("response body contains a bcrypt hash (%s): %s", prefix, body)
		}
	}
	if strings.Contains(body, "password_hash") || strings.Contains(body, "passwordHash") {
		t.Errorf("response body mentions a password hash field: %s", body)
	}
	for _, p := range passwords {
		if p != "" && strings.Contains(body, p) {
			t.Errorf("response body echoes a submitted password: %s", body)
		}
	}
}

// ---------------------------------------------------------------------------
// Authorization
// ---------------------------------------------------------------------------

// TestAdminUsersRequireAdminRole pins the in-handler half of the guard: even
// if a routing mistake let a non-admin request through the middleware, every
// handler refuses it on its own — and refuses it *before* touching the store.
func TestAdminUsersRequireAdminRole(t *testing.T) {
	callers := []struct {
		name string
		sess *adminSession
	}{
		{"viewer session", &adminSession{username: "bob", role: store.RoleViewer}},
		{"session with no role at all", &adminSession{username: "bob", role: ""}},
		{"anonymous", nil},
	}

	requests := []struct {
		name   string
		method string
		target string
		body   any
	}{
		{"list", http.MethodGet, "/admin/users", nil},
		{"create", http.MethodPost, "/admin/users", map[string]string{"username": "mallory", "password": "mallory-s3cret-pass", "role": "admin"}},
		{"update", http.MethodPatch, "/admin/users/1", map[string]any{"role": "viewer"}},
		{"delete", http.MethodDelete, "/admin/users/1", nil},
		{"reset password", http.MethodPost, "/admin/users/1/reset-password", map[string]string{"newPassword": "brand-new-password"}},
	}

	for _, caller := range callers {
		for _, req := range requests {
			t.Run(caller.name+"/"+req.name, func(t *testing.T) {
				st := newTestStore(t)
				seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
				before := userSnapshot(t, st)

				w := callAdmin(t, adminUsersHandler(caller.sess), req.method, req.target, req.body)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 (body: %s)", w.Code, w.Body.String())
				}
				assertUnchanged(t, st, before)
			})
		}
	}
}

// TestAdminUsersAuthorizationEndToEnd drives the real HandleApiRouter, so the
// routes are judged by the middleware stack they actually run behind. Two
// things must hold: an anonymous caller gets 401, and a signed-in viewer gets
// 403 — the latter being the privilege leak this plan closes, since
// GET /admin/users would otherwise be waved through as "just a read".
func TestAdminUsersAuthorizationEndToEnd(t *testing.T) {
	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)

	// serve wraps the whole API router. The identity, if any, is put into the
	// session between LoadAndSave and the router, so AuthMiddleware sees it.
	serve := func(sess *adminSession, method, target string, body any, csrf *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		sessMgr := utils.InitSessionManager()
		api := HandleApiRouter()
		handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sess != nil {
				utils.Session.Set(r, "authenticated", true)
				utils.Session.Set(r, "username", sess.username)
				utils.Session.Set(r, "role", sess.role)
			}
			api.ServeHTTP(w, r)
		}))

		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			reader = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, target, reader)
		req.Header.Set("Content-Type", "application/json")
		if csrf != nil {
			req.AddCookie(csrf)
			req.Header.Set(middleware.CSRFHeaderName, csrf.Value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// A write has to carry the double-submit CSRF pair, otherwise it would be
	// rejected at that layer and never reach the question under test. A safe
	// request is how a real client obtains its first token.
	primer := serve(nil, http.MethodGet, "/auth/status", nil, nil)
	var csrf *http.Cookie
	for _, c := range primer.Result().Cookies() {
		if c.Name == middleware.CSRFCookieName {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("the CSRF middleware issued no token on a safe request")
	}

	tests := []struct {
		name       string
		sess       *adminSession
		method     string
		target     string
		body       any
		wantStatus int
	}{
		{"anonymous list", nil, http.MethodGet, "/admin/users", nil, http.StatusUnauthorized},
		{"anonymous create", nil, http.MethodPost, "/admin/users", map[string]string{"username": "mallory", "password": "mallory-s3cret-pass", "role": "admin"}, http.StatusUnauthorized},
		{"anonymous delete", nil, http.MethodDelete, "/admin/users/1", nil, http.StatusUnauthorized},

		{"viewer list", &adminSession{"bob", store.RoleViewer}, http.MethodGet, "/admin/users", nil, http.StatusForbidden},
		{"viewer create", &adminSession{"bob", store.RoleViewer}, http.MethodPost, "/admin/users", map[string]string{"username": "mallory", "password": "mallory-s3cret-pass", "role": "admin"}, http.StatusForbidden},
		{"viewer update", &adminSession{"bob", store.RoleViewer}, http.MethodPatch, "/admin/users/1", map[string]any{"role": "viewer"}, http.StatusForbidden},
		{"viewer delete", &adminSession{"bob", store.RoleViewer}, http.MethodDelete, "/admin/users/1", nil, http.StatusForbidden},
		{"viewer reset password", &adminSession{"bob", store.RoleViewer}, http.MethodPost, "/admin/users/1/reset-password", map[string]string{"newPassword": "brand-new-password"}, http.StatusForbidden},

		{"admin list", &adminSession{"alice", store.RoleAdmin}, http.MethodGet, "/admin/users", nil, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := userSnapshot(t, st)
			w := serve(tt.sess, tt.method, tt.target, tt.body, csrf)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				assertUnchanged(t, st, before)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// TestAdminUsersListDoesNotLeakHashes: the roster is useful to an admin, but
// User.PasswordHash carries `json:"-"` precisely so that no hash can ride along
// with it. The assertion is on the raw body, which is what keeps that tag from
// being quietly dropped in a refactor.
func TestAdminUsersListDoesNotLeakHashes(t *testing.T) {
	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}), http.MethodGet, "/admin/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	raw := w.Body.String()
	assertNoSecrets(t, raw, "alice-s3cret-password", "bob-s3cret-password")

	var users []store.User
	if err := json.Unmarshal([]byte(raw), &users); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, raw)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2 (body: %s)", len(users), raw)
	}

	byName := map[string]store.User{}
	for _, u := range users {
		byName[u.Username] = u
		if u.PasswordHash != "" {
			t.Errorf("user %q came back with a password hash", u.Username)
		}
	}
	if byName["alice"].Role != store.RoleAdmin {
		t.Errorf("alice role = %q, want %q", byName["alice"].Role, store.RoleAdmin)
	}
	if byName["bob"].Role != store.RoleViewer {
		t.Errorf("bob role = %q, want %q", byName["bob"].Role, store.RoleViewer)
	}
	// lastLogin is nullable and the UI renders "—" for it; it must survive as
	// JSON null rather than becoming a zero timestamp.
	if byName["bob"].LastLogin != nil {
		t.Errorf("bob lastLogin = %v, want nil for an account that never signed in", byName["bob"].LastLogin)
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestAdminUsersCreate(t *testing.T) {
	const newPassword = "carol-s3cret-password"

	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	handler := adminUsersHandler(&adminSession{"alice", store.RoleAdmin})

	w := callAdmin(t, handler, http.MethodPost, "/admin/users", map[string]string{
		"username": "carol",
		"password": newPassword,
		"role":     store.RoleViewer,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	assertNoSecrets(t, w.Body.String(), newPassword)

	var created store.User
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Username != "carol" || created.Role != store.RoleViewer {
		t.Errorf("created = %q/%q, want carol/%s", created.Username, created.Role, store.RoleViewer)
	}

	// The account really exists and the password really works.
	stored, err := st.GetUserByUsername(t.Context(), "carol")
	if err != nil || stored == nil {
		t.Fatalf("GetUserByUsername(carol) = %v, %v", stored, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(newPassword)); err != nil {
		t.Errorf("the stored password does not verify: %v", err)
	}
}

// TestAdminUsersCreateRejectsBadInput: the server validates independently of
// the browser form, and a rejected submission creates nothing.
func TestAdminUsersCreateRejectsBadInput(t *testing.T) {
	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
	}{
		{
			name:       "duplicate username",
			body:       map[string]string{"username": "alice", "password": "another-s3cret-pass", "role": store.RoleAdmin},
			wantStatus: http.StatusConflict,
		},
		{
			name: "duplicate username differing only in case",
			// usernames are COLLATE NOCASE, so this is the same account.
			body:       map[string]string{"username": "ALICE", "password": "another-s3cret-pass", "role": store.RoleAdmin},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "weak password",
			body:       map[string]string{"username": "carol", "password": "short", "role": store.RoleViewer},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown role",
			body:       map[string]string{"username": "carol", "password": "carol-s3cret-password", "role": "superuser"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing role",
			body:       map[string]string{"username": "carol", "password": "carol-s3cret-password"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid username",
			body:       map[string]string{"username": "carol:bob", "password": "carol-s3cret-password", "role": store.RoleViewer},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty username",
			body:       map[string]string{"username": "", "password": "carol-s3cret-password", "role": store.RoleViewer},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
			before := userSnapshot(t, st)

			w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
				http.MethodPost, "/admin/users", tt.body)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			assertNoSecrets(t, w.Body.String(), tt.body["password"])
			assertUnchanged(t, st, before)
		})
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// TestAdminUsersUpdate covers the three editable fields. Each runs against a
// second administrator, so no lockout guard is in play — those have their own
// tests below.
func TestAdminUsersUpdate(t *testing.T) {
	t.Run("rename", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", bob.ID),
			map[string]any{"username": "robert"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		assertNoSecrets(t, w.Body.String())

		var updated store.User
		if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if updated.Username != "robert" {
			t.Errorf("username = %q, want %q", updated.Username, "robert")
		}
		// The role and status were absent from the body, so they must be
		// untouched — this is what the pointer fields buy.
		if updated.Role != store.RoleViewer || updated.Disabled {
			t.Errorf("a rename changed role/disabled: role = %q, disabled = %v", updated.Role, updated.Disabled)
		}
	})

	t.Run("promote to admin", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", bob.ID),
			map[string]any{"role": store.RoleAdmin})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}

		stored, err := st.GetUserByID(t.Context(), bob.ID)
		if err != nil || stored == nil {
			t.Fatalf("GetUserByID: %v, %v", stored, err)
		}
		if stored.Role != store.RoleAdmin {
			t.Errorf("stored role = %q, want %q", stored.Role, store.RoleAdmin)
		}
	})

	t.Run("demote a second admin", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", bob.ID),
			map[string]any{"role": store.RoleViewer})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}

		stored, err := st.GetUserByID(t.Context(), bob.ID)
		if err != nil || stored == nil {
			t.Fatalf("GetUserByID: %v, %v", stored, err)
		}
		if stored.Role != store.RoleViewer {
			t.Errorf("stored role = %q, want %q", stored.Role, store.RoleViewer)
		}
	})

	t.Run("disable and re-enable", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)
		handler := adminUsersHandler(&adminSession{"alice", store.RoleAdmin})
		target := fmt.Sprintf("/admin/users/%d", bob.ID)

		if w := callAdmin(t, handler, http.MethodPatch, target, map[string]any{"disabled": true}); w.Code != http.StatusOK {
			t.Fatalf("disable: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		if stored, _ := st.GetUserByID(t.Context(), bob.ID); stored == nil || !stored.Disabled {
			t.Fatalf("bob is not disabled: %+v", stored)
		}

		// Re-enabling is never guarded: it only ever adds an administrator.
		if w := callAdmin(t, handler, http.MethodPatch, target, map[string]any{"disabled": false}); w.Code != http.StatusOK {
			t.Fatalf("enable: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		if stored, _ := st.GetUserByID(t.Context(), bob.ID); stored == nil || stored.Disabled {
			t.Fatalf("bob is still disabled: %+v", stored)
		}
	})

	t.Run("rename to a taken username is a conflict", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)
		before := userSnapshot(t, st)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", bob.ID),
			map[string]any{"username": "alice"})
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
		}
		assertUnchanged(t, st, before)
	})

	t.Run("unknown role is rejected before anything is written", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)
		before := userSnapshot(t, st)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", bob.ID),
			map[string]any{"username": "robert", "role": "superuser"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
		}
		// The rename in the same body must not have been applied either.
		assertUnchanged(t, st, before)
	})

	t.Run("an admin may rename themselves", func(t *testing.T) {
		st := newTestStore(t)
		alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", alice.ID),
			map[string]any{"username": "alicia"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — a rename takes nobody's administration away (body: %s)", w.Code, w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Lockout guards — the heart of this file
// ---------------------------------------------------------------------------

// TestAdminUsersCannotDeleteSelf: an administrator deleting their own account
// is always refused, even when other admins remain. There is no undo, and the
// caller would be signed in to an account that no longer exists.
func TestAdminUsersCannotDeleteSelf(t *testing.T) {
	st := newTestStore(t)
	alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin) // a second admin exists
	before := userSnapshot(t, st)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
		http.MethodDelete, fmt.Sprintf("/admin/users/%d", alice.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "your own account") {
		t.Errorf("message does not explain the refusal: %s", w.Body.String())
	}
	assertUnchanged(t, st, before)
}

// TestAdminUsersCannotDeleteSelfCaseInsensitively: usernames are stored
// COLLATE NOCASE, so "Alice" and "alice" are one row. The self-check has to
// match that, otherwise a session spelled differently from the stored name
// would slip past the guard.
func TestAdminUsersCannotDeleteSelfCaseInsensitively(t *testing.T) {
	st := newTestStore(t)
	alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)
	before := userSnapshot(t, st)

	w := callAdmin(t, adminUsersHandler(&adminSession{"ALICE", store.RoleAdmin}),
		http.MethodDelete, fmt.Sprintf("/admin/users/%d", alice.ID), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
	}
	assertUnchanged(t, st, before)
}

// TestAdminUsersCannotDemoteOrDisableSelf: a one-click accidental lockout is
// too easy otherwise. Refused even with other admins present — an admin who
// genuinely wants to step down asks a colleague to do it.
func TestAdminUsersCannotDemoteOrDisableSelf(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"self-demote", map[string]any{"role": store.RoleViewer}},
		{"self-disable", map[string]any{"disabled": true}},
		{"self-demote while renaming", map[string]any{"username": "alicia", "role": store.RoleViewer}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
			seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin) // not the last admin
			before := userSnapshot(t, st)

			w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
				http.MethodPatch, fmt.Sprintf("/admin/users/%d", alice.ID), tt.body)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "your own account") {
				t.Errorf("message does not explain the refusal: %s", w.Body.String())
			}
			assertUnchanged(t, st, before)
		})
	}
}

// TestAdminUsersCannotRemoveTheLastAdmin is the guard that keeps an instance
// administrable. Losing the last enabled admin is unrecoverable from the UI:
// the /setup wizard only reopens when there are *zero* users, so the remaining
// viewers could never promote anyone.
//
// The caller here is a *disabled* administrator, which is the realistic way to
// reach this state without tripping the self-modification rules first: bob
// signed in as an admin, alice disabled him, and his session still says admin
// because sessions are not revalidated against the database on every request.
// alice is now the only enabled administrator, and nothing bob does may take
// that away.
func TestAdminUsersCannotRemoveTheLastAdmin(t *testing.T) {
	tests := []struct {
		name   string
		method string
		body   map[string]any
	}{
		{"delete the last admin", http.MethodDelete, nil},
		{"disable the last admin", http.MethodPatch, map[string]any{"disabled": true}},
		{"demote the last admin to viewer", http.MethodPatch, map[string]any{"role": store.RoleViewer}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
			bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)
			if err := st.SetDisabled(t.Context(), bob.ID, true); err != nil {
				t.Fatalf("SetDisabled(bob): %v", err)
			}

			if n, err := st.CountEnabledAdmins(t.Context()); err != nil || n != 1 {
				t.Fatalf("CountEnabledAdmins = %d, %v; want 1 — the fixture is wrong", n, err)
			}
			before := userSnapshot(t, st)

			var body any
			if tt.body != nil {
				body = tt.body
			}
			w := callAdmin(t, adminUsersHandler(&adminSession{"bob", store.RoleAdmin}),
				tt.method, fmt.Sprintf("/admin/users/%d", alice.ID), body)
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "last administrator") {
				t.Errorf("message does not explain the refusal: %s", w.Body.String())
			}
			assertUnchanged(t, st, before)

			// And the instance is still administrable, which is the whole point.
			if n, err := st.CountEnabledAdmins(t.Context()); err != nil || n != 1 {
				t.Errorf("CountEnabledAdmins = %d, %v; want 1", n, err)
			}
		})
	}
}

// TestAdminUsersLastAdminGuardAllowsHarmlessChanges: the guard must not be so
// broad that it blocks changes which leave administration intact — renaming
// the last admin, resetting their password, re-enabling them, or removing
// someone who was never an enabled admin.
func TestAdminUsersLastAdminGuardAllowsHarmlessChanges(t *testing.T) {
	t.Run("rename the last admin", func(t *testing.T) {
		st := newTestStore(t)
		alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)
		if err := st.SetDisabled(t.Context(), bob.ID, true); err != nil {
			t.Fatalf("SetDisabled(bob): %v", err)
		}

		w := callAdmin(t, adminUsersHandler(&adminSession{"bob", store.RoleAdmin}),
			http.MethodPatch, fmt.Sprintf("/admin/users/%d", alice.ID),
			map[string]any{"username": "alicia"})
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("delete a viewer while one admin remains", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)

		w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
			http.MethodDelete, fmt.Sprintf("/admin/users/%d", bob.ID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("delete a disabled admin while one enabled admin remains", func(t *testing.T) {
		st := newTestStore(t)
		seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
		bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)
		carol := seedUser(t, st, "carol", "carol-s3cret-password", store.RoleAdmin)
		if err := st.SetDisabled(t.Context(), carol.ID, true); err != nil {
			t.Fatalf("SetDisabled(carol): %v", err)
		}
		if err := st.SetDisabled(t.Context(), bob.ID, true); err != nil {
			t.Fatalf("SetDisabled(bob): %v", err)
		}

		// carol cannot administer anything, so removing her costs nothing.
		w := callAdmin(t, adminUsersHandler(&adminSession{"bob", store.RoleAdmin}),
			http.MethodDelete, fmt.Sprintf("/admin/users/%d", carol.ID), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
	})
}

// TestAdminUsersDeleteAnotherAdminSucceeds is the positive case the guards must
// not swallow: with two enabled administrators, one may remove the other.
func TestAdminUsersDeleteAnotherAdminSucceeds(t *testing.T) {
	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	bob := seedUser(t, st, "bob", "bob-s3cret-password", store.RoleAdmin)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
		http.MethodDelete, fmt.Sprintf("/admin/users/%d", bob.ID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	if stored, err := st.GetUserByID(t.Context(), bob.ID); err != nil {
		t.Fatalf("GetUserByID: %v", err)
	} else if stored != nil {
		t.Error("bob is still in the database after a successful delete")
	}
	if n, err := st.CountEnabledAdmins(t.Context()); err != nil || n != 1 {
		t.Errorf("CountEnabledAdmins = %d, %v; want 1", n, err)
	}
}

// ---------------------------------------------------------------------------
// Reset password
// ---------------------------------------------------------------------------

// TestAdminUsersResetPassword: the administrator's escape hatch for a
// locked-out colleague. It must set a working password and say nothing about
// it in the response.
func TestAdminUsersResetPassword(t *testing.T) {
	const (
		oldPassword = "bob-s3cret-password"
		newPassword = "bob-brand-new-password"
	)

	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	bob := seedUser(t, st, "bob", oldPassword, store.RoleViewer)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
		http.MethodPost, fmt.Sprintf("/admin/users/%d/reset-password", bob.ID),
		map[string]string{"newPassword": newPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	assertNoSecrets(t, w.Body.String(), newPassword, oldPassword)

	stored, err := st.GetUserByID(t.Context(), bob.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetUserByID: %v, %v", stored, err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(newPassword)); err != nil {
		t.Errorf("the new password does not verify: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(oldPassword)); err == nil {
		t.Error("the old password still works after a reset")
	}
}

// TestAdminUsersResetPasswordRejectsWeak: the policy is enforced on the server,
// and a rejected reset leaves the existing password in place.
func TestAdminUsersResetPasswordRejectsWeak(t *testing.T) {
	const oldPassword = "bob-s3cret-password"

	st := newTestStore(t)
	seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
	bob := seedUser(t, st, "bob", oldPassword, store.RoleViewer)
	before := userSnapshot(t, st)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
		http.MethodPost, fmt.Sprintf("/admin/users/%d/reset-password", bob.ID),
		map[string]string{"newPassword": "short"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	assertNoSecrets(t, w.Body.String(), "short")
	assertUnchanged(t, st, before)
}

// TestAdminUsersMayResetTheirOwnPassword: resetting a password takes nobody's
// administration away, so the self-modification guards must not block it.
func TestAdminUsersMayResetTheirOwnPassword(t *testing.T) {
	st := newTestStore(t)
	alice := seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}),
		http.MethodPost, fmt.Sprintf("/admin/users/%d/reset-password", alice.ID),
		map[string]string{"newPassword": "alice-brand-new-password"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Ids
// ---------------------------------------------------------------------------

// TestAdminUsersUnknownID: an id that names nobody is a 404 on every endpoint
// that takes one, and a malformed one is a 400 — neither may fall through to
// something that mutates.
func TestAdminUsersUnknownID(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
	}{
		{"update unknown", http.MethodPatch, "/admin/users/424242", map[string]any{"role": store.RoleViewer}, http.StatusNotFound},
		{"delete unknown", http.MethodDelete, "/admin/users/424242", nil, http.StatusNotFound},
		{"reset unknown", http.MethodPost, "/admin/users/424242/reset-password", map[string]string{"newPassword": "brand-new-password"}, http.StatusNotFound},
		{"update malformed", http.MethodPatch, "/admin/users/not-a-number", map[string]any{"role": store.RoleViewer}, http.StatusBadRequest},
		{"delete malformed", http.MethodDelete, "/admin/users/not-a-number", nil, http.StatusBadRequest},
		{"reset malformed", http.MethodPost, "/admin/users/not-a-number/reset-password", map[string]string{"newPassword": "brand-new-password"}, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			seedUser(t, st, "alice", "alice-s3cret-password", store.RoleAdmin)
			before := userSnapshot(t, st)

			w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}), tt.method, tt.path, tt.body)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			assertUnchanged(t, st, before)
		})
	}
}

// TestAdminUsersWithoutStore: a request that arrives before the database is
// open must fail loudly rather than appear to succeed.
func TestAdminUsersWithoutStore(t *testing.T) {
	store.SetDefault(nil)

	w := callAdmin(t, adminUsersHandler(&adminSession{"alice", store.RoleAdmin}), http.MethodGet, "/admin/users", nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body: %s)", w.Code, w.Body.String())
	}
}
