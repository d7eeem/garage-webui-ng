package middleware

import (
	"errors"
	"khairul169/garage-webui/utils"
	"net/http"
	"strings"
)

// isViewerAllowed is the entire security boundary for the read-only viewer
// role: fail-closed by construction. Every GET is allowed except the one
// carve-out that reveals a secret (GetKeyInfo?showSecretKey=true); every
// non-GET is denied except logout. Any new write served via GET, or any new
// secret-revealing GET, must be added here explicitly — do not loosen this
// to "allow all GET".
func isViewerAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet {
		// one carve-out: never let a viewer reveal a secret access key
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") &&
			r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true
	}
	// the only non-GET a viewer may call is logout
	return r.Method == http.MethodPost && r.URL.Path == "/auth/logout"
}

func AuthMiddleware(next http.Handler) http.Handler {
	authData := utils.GetEnv("AUTH_USER_PASS", "") + utils.GetEnv("AUTH_VIEWER_USER_PASS", "")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := utils.Session.Get(r, "authenticated")

		if authData == "" {
			next.ServeHTTP(w, r)
			return
		}

		if auth == nil || !auth.(bool) {
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
