package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/d7eeem/garage-webui-ng/utils"
)

// TestIsViewerAllowed exercises the entire security boundary for the
// read-only viewer role. It must stay fail-closed: any non-GET request that
// isn't POST /auth/logout is denied, and the one GET carve-out
// (GetKeyInfo?showSecretKey=true) must also be denied.
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
			name:   "DELETE is denied",
			method: http.MethodDelete,
			target: "/browse/b/k",
			want:   false,
		},
		{
			name:   "PUT is denied",
			method: http.MethodPut,
			target: "/browse/b/k",
			want:   false,
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
}
