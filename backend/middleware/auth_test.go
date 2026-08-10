package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/d7eeem/garage-webui-ng/utils"
)

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
// gets 403 on a write, and 200 on a read.
func TestAuthMiddlewareViewerForbidden(t *testing.T) {
	sessMgr := utils.InitSessionManager()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Seed an authenticated viewer session inside the scs middleware, then
	// call the guarded handler in the same request context.
	serve := func(method, target string) int {
		var code int
		handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			utils.Session.Set(r, "authenticated", true)
			utils.Session.Set(r, "role", "viewer")
			rec := httptest.NewRecorder()
			AuthMiddleware(next).ServeHTTP(rec, r)
			code = rec.Code
		}))
		req := httptest.NewRequest(method, target, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
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
// other half is requireAdmin inside every /admin/* handler.
func TestAuthMiddlewareAdminAPIIsAdminOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// serve seeds an authenticated session with the given role inside the scs
	// middleware, then calls the guarded handler in that same request context.
	serve := func(role, method, target string) (int, bool) {
		var (
			code    int
			reached bool
		)
		sessMgr := utils.InitSessionManager()
		handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			utils.Session.Set(r, "authenticated", true)
			utils.Session.Set(r, "role", role)
			rec := httptest.NewRecorder()
			AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				next.ServeHTTP(w, r)
			})).ServeHTTP(rec, r)
			code = rec.Code
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
		return code, reached
	}

	if code, reached := serve("viewer", http.MethodGet, "/admin/users"); code != http.StatusForbidden || reached {
		t.Errorf("viewer GET /admin/users = %d (handler reached = %v), want 403 and not reached", code, reached)
	}
	if code, reached := serve("admin", http.MethodGet, "/admin/users"); code != http.StatusOK || !reached {
		t.Errorf("admin GET /admin/users = %d (handler reached = %v), want 200 and reached", code, reached)
	}
	if code, reached := serve("admin", http.MethodDelete, "/admin/users/1"); code != http.StatusOK || !reached {
		t.Errorf("admin DELETE /admin/users/1 = %d (handler reached = %v), want 200 and reached", code, reached)
	}
}
