package middleware

import (
	"errors"
	"github.com/d7eeem/garage-webui-ng/utils"
	"net/http"
	"strings"
)

// isViewerAllowed is the entire security boundary for the read-only viewer
// role: fail-closed by construction. Nothing under /admin/ is reachable at all;
// otherwise every GET is allowed except the one carve-out that reveals a secret
// (GetKeyInfo?showSecretKey=true), and every non-GET is denied except the two
// that only affect the caller's own account. Any new write served via GET, or
// any new secret-revealing GET, must be added here explicitly — do not loosen
// this to "allow all GET".
//
// This is the outer half of a two-layer guard on the administration API: every
// /admin/* handler also calls requireAdmin (backend/router/admin_users.go), so
// a routing mistake alone cannot expose those endpoints. Keep both.
func isViewerAllowed(r *http.Request) bool {
	// User administration is admin-only whatever the method, so the check sits
	// above the per-method rules. Reads are included deliberately: the roster of
	// accounts, their roles and their sign-in times are not viewer-visible
	// information, and "every GET is allowed" below would otherwise hand a
	// viewer the whole user list.
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return false
	}

	if r.Method == http.MethodGet {
		// one carve-out: never let a viewer reveal a secret access key
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") &&
			r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true
	}
	// A read-only viewer may still end their session and change their OWN
	// password. Nothing else that mutates state is permitted.
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" || r.URL.Path == "/auth/change-password"
	}
	return false
}

// isPublicPath reports whether a request may be served without an
// authenticated session. This is a security boundary: authentication is
// mandatory, so everything not listed here requires a session, and the list
// must stay minimal and exact-match — never a prefix.
//
// POST /auth/login is registered on the outer mux (router.HandleApiRouter)
// and never reaches this middleware, so it does not appear here.
func isPublicPath(r *http.Request) bool {
	switch {
	// The UI needs to know whether it is logged in before it can render a
	// login form. The handler exposes nothing beyond the caller's own
	// session state and whether a first account still has to be created.
	case r.Method == http.MethodGet && r.URL.Path == "/auth/status":
		return true

	// First-run setup: a brand-new deployment has no users, so nobody could
	// possibly authenticate in order to create the first one. The handlers
	// themselves must refuse to run once any user exists.
	case r.Method == http.MethodGet && r.URL.Path == "/setup/status":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/setup":
		return true
	}
	return false
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Comma-ok, not a bare type assertion: a session value of an
		// unexpected type must fail closed, not panic.
		if authenticated, _ := utils.Session.Get(r, "authenticated").(bool); !authenticated {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		if role, _ := utils.Session.Get(r, "role").(string); role == "viewer" && !isViewerAllowed(r) {
			utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
