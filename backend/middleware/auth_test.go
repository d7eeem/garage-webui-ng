package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// newAuthTestStore opens a throwaway on-disk store (mirrors
// backend/store/store_test.go's newTestStore), installs it as the process
// default for the duration of the test, and restores whatever was installed
// before. It also clears the revalidation cache before and after, so no state
// leaks between test cases that reuse a username.
func newAuthTestStore(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	prev := store.Default()
	store.SetDefault(st)
	t.Cleanup(func() { store.SetDefault(prev) })

	resetCallerCache()
	t.Cleanup(resetCallerCache)

	return st
}

// resetCallerCache clears the middleware's local revalidation cache. Tests
// call it to guarantee a store lookup actually happens rather than being
// served from a stale entry left by an earlier test case.
func resetCallerCache() {
	callerCacheMu.Lock()
	callerCache = map[string]cachedCaller{}
	callerCacheMu.Unlock()
}

// authRequest serves one request through sessMgr.LoadAndSave(AuthMiddleware(next)),
// with the session pre-seeded with the given authenticated/username/role
// values (role is what the OLD code trusted; tests that want to prove the
// store wins set it to something the store row disagrees with). Returns the
// response code and whether next ran.
func authRequest(t *testing.T, sessMgr *scs.SessionManager, authenticated bool, username, sessionRole, method, target string) (code int, reached bool) {
	t.Helper()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticated {
			utils.Session.Set(r, "authenticated", true)
		}
		if username != "" {
			utils.Session.Set(r, "username", username)
		}
		if sessionRole != "" {
			utils.Session.Set(r, "role", sessionRole)
		}
		rec := httptest.NewRecorder()
		AuthMiddleware(next).ServeHTTP(rec, r)
		code = rec.Code
	}))

	req := httptest.NewRequest(method, target, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return code, reached
}

// TestIsViewerAllowed exercises the entire security boundary for the
// read-only viewer role. It must stay fail-closed: any non-GET request that
// isn't POST /auth/logout or POST /auth/change-password is denied, and the one
// GET carve-out (GetKeyInfo?showSecretKey=true) must also be denied.
func TestIsViewerAllowed(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		want   bool
	}{
		{
			name:   "read endpoint via GET is allowed",
			method: http.MethodGet,
			target: "/v2/GetClusterStatus",
			want:   true,
		},
		{
			name:   "GetKeyInfo with showSecretKey=true is denied",
			method: http.MethodGet,
			target: "/v2/GetKeyInfo?showSecretKey=true",
			want:   false,
		},
		{
			name:   "GetKeyInfo without showSecretKey is allowed",
			method: http.MethodGet,
			target: "/v2/GetKeyInfo?id=x",
			want:   true,
		},
		{
			name:   "POST mutation is denied",
			method: http.MethodPost,
			target: "/v2/CreateBucket",
			want:   false,
		},
		{
			name:   "POST logout is allowed",
			method: http.MethodPost,
			target: "/auth/logout",
			want:   true,
		},
		{
			// A viewer must be able to rotate their own credential without
			// asking an administrator. It touches nothing but their own row.
			name:   "POST change-password is allowed",
			method: http.MethodPost,
			target: "/auth/change-password",
			want:   true,
		},
		{
			name:   "change-password by a non-POST method is denied",
			method: http.MethodPut,
			target: "/auth/change-password",
			want:   false,
		},
		{
			name:   "a path merely prefixed with change-password is denied",
			method: http.MethodPost,
			target: "/auth/change-password/other",
			want:   false,
		},
		{
			name:   "DELETE is denied",
			method: http.MethodDelete,
			target: "/browse/b/k",
			want:   false,
		},
		{
			// The privilege leak this check exists to close: /admin/users is a
			// GET, and "every GET is allowed" would otherwise hand a read-only
			// viewer the full roster of accounts, roles and sign-in times.
			name:   "listing users is denied",
			method: http.MethodGet,
			target: "/admin/users",
			want:   false,
		},
		{
			name:   "creating a user is denied",
			method: http.MethodPost,
			target: "/admin/users",
			want:   false,
		},
		{
			name:   "updating a user is denied",
			method: http.MethodPatch,
			target: "/admin/users/1",
			want:   false,
		},
		{
			name:   "deleting a user is denied",
			method: http.MethodDelete,
			target: "/admin/users/1",
			want:   false,
		},
		{
			name:   "resetting a password is denied",
			method: http.MethodPost,
			target: "/admin/users/1/reset-password",
			want:   false,
		},
		{
			// Any future administration endpoint is denied by default, which is
			// the point of matching the whole /admin/ prefix rather than
			// enumerating routes.
			name:   "an unrelated admin endpoint is denied",
			method: http.MethodGet,
			target: "/admin/anything-added-later",
			want:   false,
		},
		{
			name:   "PUT is denied",
			method: http.MethodPut,
			target: "/browse/b/k",
			want:   false,
		},
		{
			// Downloading is a read; the mint step only needs a POST
			// because the selected key list is too large for a URL. This
			// is the important carve-out: it must NOT widen into allowing
			// POST /browse/{bucket}, which serves delete (see the next
			// test case).
			name:   "minting a download token is allowed",
			method: http.MethodPost,
			target: "/browse/download-token",
			want:   true,
		},
		{
			// The important negative case: POST /browse/{bucket} serves
			// BulkDeleteObjects's delete action. If this ever flips to
			// true, a viewer session can delete objects.
			name:   "posting to a bucket (delete) is still denied",
			method: http.MethodPost,
			target: "/browse/some-bucket",
			want:   false,
		},
		{
			name:   "fetching the archive itself is allowed (it's a GET)",
			method: http.MethodGet,
			target: "/browse/b/archive",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.target, nil)
			if got := isViewerAllowed(r); got != tt.want {
				t.Errorf("isViewerAllowed(%s %s) = %v, want %v", tt.method, tt.target, got, tt.want)
			}
		})
	}
}

// TestIsPublicPath pins the unauthenticated allowlist. Authentication is
// mandatory, so anything that is not on this list must require a session —
// adding an entry here widens the attack surface of the whole API.
func TestIsPublicPath(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		want   bool
	}{
		{"auth status", http.MethodGet, "/auth/status", true},
		{"setup status", http.MethodGet, "/setup/status", true},
		{"setup submit", http.MethodPost, "/setup", true},
		{"logout still needs a session", http.MethodPost, "/auth/logout", false},
		{"buckets", http.MethodGet, "/buckets", false},
		{"proxied admin API", http.MethodGet, "/v2/GetClusterStatus", false},
		{"auth status with the wrong method", http.MethodPost, "/auth/status", false},
		{"setup with the wrong method", http.MethodGet, "/setup", false},
		{"prefix must not match", http.MethodGet, "/auth/status/../buckets", false},
		{"setup subpath must not match", http.MethodPost, "/setup/anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.target, nil)
			if got := isPublicPath(r); got != tt.want {
				t.Errorf("isPublicPath(%s %s) = %v, want %v", tt.method, tt.target, got, tt.want)
			}
		})
	}
}

// TestAuthMiddlewareRequiresSession is the behaviour change from plan 022:
// there is no longer an "auth not configured ⇒ let everyone through" mode, so
// an anonymous request to a normal API path is rejected. AUTH_USER_PASS is
// set to empty here precisely because that used to be the open-mode trigger.
func TestAuthMiddlewareRequiresSession(t *testing.T) {
	t.Setenv("AUTH_USER_PASS", "")
	t.Setenv("AUTH_VIEWER_USER_PASS", "")

	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := sessMgr.LoadAndSave(AuthMiddleware(next))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if reached {
		t.Error("the wrapped handler ran for an unauthenticated request")
	}
}

// TestAuthMiddlewareAllowsPublicPath: /auth/status must stay reachable
// without a session, otherwise the UI can never discover that it needs to log
// in (or run the setup wizard).
func TestAuthMiddlewareAllowsPublicPath(t *testing.T) {
	sessMgr := utils.InitSessionManager()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := sessMgr.LoadAndSave(AuthMiddleware(next))

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !reached {
		t.Error("the wrapped handler did not run for a public path")
	}
}

// TestAuthMiddlewareAllowsSetupPaths: a brand-new deployment has no users, so
// nobody could authenticate in order to create the first one — the wizard's
// two endpoints must reach their handlers without a session. Everything else
// still needs one; the handlers behind /setup carry their own guard against
// running once an account exists.
func TestAuthMiddlewareAllowsSetupPaths(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantReach  bool
	}{
		{"setup status", http.MethodGet, "/setup/status", http.StatusOK, true},
		{"setup submit", http.MethodPost, "/setup", http.StatusOK, true},
		{"an ordinary API path still needs a session", http.MethodGet, "/buckets", http.StatusUnauthorized, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessMgr := utils.InitSessionManager()

			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			handler := sessMgr.LoadAndSave(AuthMiddleware(next))

			req := httptest.NewRequest(tt.method, tt.target, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("%s %s: status = %d, want %d", tt.method, tt.target, w.Code, tt.wantStatus)
			}
			if reached != tt.wantReach {
				t.Errorf("%s %s: handler reached = %v, want %v", tt.method, tt.target, reached, tt.wantReach)
			}
		})
	}
}

// TestAuthMiddlewareViewerForbidden: an authenticated viewer session still
// gets 403 on a write, and 200 on a read. The role now comes from the store
// row, not the session, so the fixture creates a real viewer account.
func TestAuthMiddlewareViewerForbidden(t *testing.T) {
	sessMgr := utils.InitSessionManager()
	st := newAuthTestStore(t)
	if _, err := st.CreateUser(t.Context(), "a-viewer", "correct-horse-battery", store.RoleViewer); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Seed an authenticated viewer session inside the scs middleware, then
	// call the guarded handler in the same request context.
	serve := func(method, target string) int {
		code, _ := authRequest(t, sessMgr, true, "a-viewer", "viewer", method, target)
		return code
	}

	if got := serve(http.MethodPost, "/v2/CreateBucket"); got != http.StatusForbidden {
		t.Errorf("viewer POST /v2/CreateBucket = %d, want 403", got)
	}
	if got := serve(http.MethodGet, "/buckets"); got != http.StatusOK {
		t.Errorf("viewer GET /buckets = %d, want 200", got)
	}
	if got := serve(http.MethodPost, "/auth/change-password"); got != http.StatusOK {
		t.Errorf("viewer POST /auth/change-password = %d, want 200", got)
	}
}

// TestAuthMiddlewareAdminAPIIsAdminOnly is the middleware half of the two-layer
// guard on user administration: a viewer session is stopped here, before the
// handler runs at all, while an administrator passes straight through. The
// other half is requireAdmin inside every /admin/* handler. Each role is
// backed by a real store row, since that is what the middleware now consults.
func TestAuthMiddlewareAdminAPIIsAdminOnly(t *testing.T) {
	st := newAuthTestStore(t)
	if _, err := st.CreateUser(t.Context(), "an-admin", "correct-horse-battery", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser(admin): %v", err)
	}
	if _, err := st.CreateUser(t.Context(), "a-viewer", "correct-horse-battery", store.RoleViewer); err != nil {
		t.Fatalf("CreateUser(viewer): %v", err)
	}

	serve := func(username, method, target string) (int, bool) {
		sessMgr := utils.InitSessionManager()
		return authRequest(t, sessMgr, true, username, "", method, target)
	}

	if code, reached := serve("a-viewer", http.MethodGet, "/admin/users"); code != http.StatusForbidden || reached {
		t.Errorf("viewer GET /admin/users = %d (handler reached = %v), want 403 and not reached", code, reached)
	}
	if code, reached := serve("an-admin", http.MethodGet, "/admin/users"); code != http.StatusOK || !reached {
		t.Errorf("admin GET /admin/users = %d (handler reached = %v), want 200 and reached", code, reached)
	}
	if code, reached := serve("an-admin", http.MethodDelete, "/admin/users/1"); code != http.StatusOK || !reached {
		t.Errorf("admin DELETE /admin/users/1 = %d (handler reached = %v), want 200 and reached", code, reached)
	}
}

// TestAuthMiddlewareRevalidatesAgainstStore is the regression suite for plan
// 052: authorization must be re-checked against the user store on every
// request, not trusted from the session snapshot taken at login.
func TestAuthMiddlewareRevalidatesAgainstStore(t *testing.T) {
	t.Run("a disabled user is rejected on the next request", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()
		st := newAuthTestStore(t)
		u, err := st.CreateUser(t.Context(), "will-be-disabled", "correct-horse-battery", store.RoleAdmin)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		if code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets"); code != http.StatusOK || !reached {
			t.Fatalf("before disabling: status = %d, reached = %v, want 200 and reached", code, reached)
		}

		if err := st.SetDisabled(t.Context(), u.ID, true); err != nil {
			t.Fatalf("SetDisabled: %v", err)
		}
		resetCallerCache() // force the next request to re-query the store

		code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets")
		if code != http.StatusUnauthorized {
			t.Errorf("after disabling: status = %d, want 401", code)
		}
		if reached {
			t.Error("the wrapped handler ran for a disabled account")
		}
	})

	t.Run("a demoted admin loses admin access even though the session still says admin", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()
		st := newAuthTestStore(t)
		u, err := st.CreateUser(t.Context(), "will-be-demoted", "correct-horse-battery", store.RoleViewer)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		// Session claims admin; the store row says viewer. The store must win.
		code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodPost, "/v2/CreateBucket")
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 (store role, not session role, must govern)", code)
		}
		if reached {
			t.Error("the wrapped handler ran for a viewer-level write")
		}
	})

	t.Run("a deleted user is rejected, not passed through and not a 500", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()
		st := newAuthTestStore(t)
		u, err := st.CreateUser(t.Context(), "will-be-deleted", "correct-horse-battery", store.RoleAdmin)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if err := st.DeleteUser(t.Context(), u.ID); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}

		code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets")
		if code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
		if reached {
			t.Error("the wrapped handler ran for a deleted account")
		}
	})

	t.Run("a promoted viewer gains access even though the session still says viewer", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()
		st := newAuthTestStore(t)
		u, err := st.CreateUser(t.Context(), "will-be-promoted", "correct-horse-battery", store.RoleAdmin)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		// Session claims viewer; the store row says admin. A write must be
		// allowed, proving the role is read fresh in both directions.
		code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleViewer, http.MethodPost, "/v2/CreateBucket")
		if code != http.StatusOK || !reached {
			t.Errorf("status = %d, reached = %v, want 200 and reached", code, reached)
		}
	})

	t.Run("store.Default() == nil is a 500, not a 401", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()

		prev := store.Default()
		store.SetDefault(nil)
		t.Cleanup(func() { store.SetDefault(prev) })
		resetCallerCache()
		t.Cleanup(resetCallerCache)

		code, reached := authRequest(t, sessMgr, true, "anyone", store.RoleAdmin, http.MethodGet, "/buckets")
		if code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", code)
		}
		if reached {
			t.Error("the wrapped handler ran with no store installed")
		}
	})

	t.Run("public paths still bypass the store lookup entirely", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()

		// Point at a nil store: if the middleware performed a lookup at all for
		// a public path, this would 500 instead of 200.
		prev := store.Default()
		store.SetDefault(nil)
		t.Cleanup(func() { store.SetDefault(prev) })
		resetCallerCache()
		t.Cleanup(resetCallerCache)

		code, reached := authRequest(t, sessMgr, false, "", "", http.MethodGet, "/auth/status")
		if code != http.StatusOK || !reached {
			t.Errorf("GET /auth/status with nil store = %d (reached = %v), want 200 and reached", code, reached)
		}
	})

	t.Run("the cache expires so a status change is observed without a new login", func(t *testing.T) {
		sessMgr := utils.InitSessionManager()
		st := newAuthTestStore(t)
		u, err := st.CreateUser(t.Context(), "cache-expiry", "correct-horse-battery", store.RoleAdmin)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		base := time.Now()
		prevNow := nowFunc
		nowFunc = func() time.Time { return base }
		t.Cleanup(func() { nowFunc = prevNow })

		if code, _ := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets"); code != http.StatusOK {
			t.Fatalf("initial request: status = %d, want 200", code)
		}

		if err := st.SetDisabled(t.Context(), u.ID, true); err != nil {
			t.Fatalf("SetDisabled: %v", err)
		}

		// Still within the TTL: the cached (enabled) entry must still be
		// served, without re-querying the store.
		nowFunc = func() time.Time { return base.Add(callerCacheTTL - time.Millisecond) }
		if code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets"); code != http.StatusOK || !reached {
			t.Fatalf("within TTL: status = %d, reached = %v, want 200 and reached (stale cache should still apply)", code, reached)
		}

		// Past the TTL: the cache entry has expired, so the disabled status
		// must now be observed.
		nowFunc = func() time.Time { return base.Add(callerCacheTTL + time.Millisecond) }
		code, reached := authRequest(t, sessMgr, true, u.Username, store.RoleAdmin, http.MethodGet, "/buckets")
		if code != http.StatusUnauthorized {
			t.Errorf("after TTL: status = %d, want 401", code)
		}
		if reached {
			t.Error("the wrapped handler ran after the disabled status should have been observed")
		}
	})
}
