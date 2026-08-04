package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"os"

	"github.com/d7eeem/garage-webui-ng/utils"
)

const (
	// CSRFCookieName holds the token the browser echoes back. It is
	// deliberately NOT HttpOnly: the frontend's fetch wrapper has to read it in
	// order to send the matching header.
	CSRFCookieName = "csrf_token"

	// CSRFHeaderName is where the copy of the token must appear on a
	// state-changing request. A cross-origin page cannot set a custom header
	// without a CORS preflight this server never grants, which is what makes
	// the double-submit pair meaningful.
	CSRFHeaderName = "X-CSRF-Token"

	// csrfTokenBytes is the entropy behind each token, hex-encoded into the
	// cookie.
	csrfTokenBytes = 32
)

var errCSRF = errors.New("invalid or missing CSRF token")

// isCSRFExempt reports whether a request may proceed without a CSRF token.
//
// THIS LIST IS A SECURITY BOUNDARY. Only these two endpoints may appear here,
// and only as exact method+path matches — never as a prefix:
//
//   - POST /auth/login and POST /setup are the only writes a caller can make
//     before it has any session at all, so a browser that has never loaded the
//     app cannot yet hold a token. Both are rate-limited (loginAttempts in
//     backend/router/auth.go) and both are guarded by a credential or by the
//     "no users exist yet" check, so neither is a useful CSRF target.
//
// Every other write — including logout and password change — requires the
// token. Adding an entry here removes CSRF protection from that endpoint.
func isCSRFExempt(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	return r.URL.Path == "/auth/login" || r.URL.Path == "/setup"
}

// isStateChanging reports whether the method needs a token. GET/HEAD/OPTIONS
// are safe by definition and are also how a fresh client obtains its first
// token, so they must never be rejected.
func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// CSRF implements double-submit-cookie protection.
//
// Honest framing: the session cookie is already SameSite=Lax and the API
// consumes application/json, so a cross-site form post cannot reach a write
// endpoint with credentials attached. This token is defence in depth on top of
// those two, not a substitute for either — do not relax SameSite or add
// permissive CORS to accommodate it.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The token that arrived with the request. Missing cookie ⇒ empty, and
		// an empty value can never satisfy the comparison below.
		cookieToken := ""
		if c, err := r.Cookie(CSRFCookieName); err == nil {
			cookieToken = c.Value
		}

		// Hand out a token to any client that does not have one yet. This runs
		// before next.ServeHTTP so the header is still writable, and before the
		// check below so that the *incoming* value — not the fresh one — is
		// what a state-changing request is judged on.
		if cookieToken == "" {
			token, err := newCSRFToken()
			if err != nil {
				utils.ResponseError(w, err)
				return
			}
			http.SetCookie(w, csrfCookie(token))
		}

		if isStateChanging(r.Method) && !isCSRFExempt(r) {
			headerToken := r.Header.Get(CSRFHeaderName)
			if cookieToken == "" || headerToken == "" ||
				subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
				utils.ResponseErrorStatus(w, errCSRF, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func newCSRFToken() (string, error) {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// Fail closed: serving a predictable token would be worse than serving
		// an error.
		return "", errors.New("cannot generate a CSRF token")
	}
	return hex.EncodeToString(buf), nil
}

// csrfCookie mirrors the session cookie's scope and Secure setting so the two
// travel together, but stays readable from JavaScript — that is the whole
// point of the double-submit pattern.
func csrfCookie(token string) *http.Cookie {
	path := "/"
	if basePath := os.Getenv("BASE_PATH"); basePath != "" {
		path = basePath
	}
	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     path,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   utils.GetEnv("SESSION_COOKIE_SECURE", "false") == "true",
	}
}
