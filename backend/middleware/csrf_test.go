package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler records whether the protected handler was reached.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

// csrfCookieFrom returns the csrf_token cookie the middleware issued, or nil.
func csrfCookieFrom(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == CSRFCookieName {
			return c
		}
	}
	return nil
}

// TestCSRFIssuesTokenOnSafeRequest: a client that has never talked to the API
// gets its first token from an ordinary read. If this stopped working, no
// browser could ever perform a write.
func TestCSRFIssuesTokenOnSafeRequest(t *testing.T) {
	reached := false
	handler := CSRF(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if !reached {
		t.Error("the wrapped handler did not run for a safe method")
	}

	cookie := csrfCookieFrom(w.Result())
	if cookie == nil {
		t.Fatalf("no %s cookie was set", CSRFCookieName)
	}
	if len(cookie.Value) != 2*csrfTokenBytes {
		t.Errorf("token length = %d hex chars, want %d", len(cookie.Value), 2*csrfTokenBytes)
	}
	// The frontend has to read this cookie to echo it back in the header.
	if cookie.HttpOnly {
		t.Error("HttpOnly = true; the token cookie must be readable from JavaScript")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want %q", cookie.Path, "/")
	}
}

// TestCSRFCookiePathFollowsBasePath: mounted under a prefix, the token cookie
// must be scoped like the session cookie or the browser will not send it back.
func TestCSRFCookiePathFollowsBasePath(t *testing.T) {
	t.Setenv("BASE_PATH", "/garage")

	reached := false
	handler := CSRF(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	cookie := csrfCookieFrom(w.Result())
	if cookie == nil {
		t.Fatalf("no %s cookie was set", CSRFCookieName)
	}
	if cookie.Path != "/garage" {
		t.Errorf("Path = %q, want %q", cookie.Path, "/garage")
	}
}

// TestCSRFCookieSecureOptIn mirrors SESSION_COOKIE_SECURE so the two cookies
// travel under the same rules.
func TestCSRFCookieSecureOptIn(t *testing.T) {
	t.Setenv("SESSION_COOKIE_SECURE", "true")

	reached := false
	handler := CSRF(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	cookie := csrfCookieFrom(w.Result())
	if cookie == nil {
		t.Fatalf("no %s cookie was set", CSRFCookieName)
	}
	if !cookie.Secure {
		t.Error("Secure = false, want true when SESSION_COOKIE_SECURE=true")
	}
}

// TestCSRFDoubleSubmit is the core contract: a write is allowed only when the
// header repeats the cookie exactly.
func TestCSRFDoubleSubmit(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name       string
		method     string
		target     string
		cookie     string // "" ⇒ no cookie
		header     string // "" ⇒ no header
		wantStatus int
		wantReach  bool
	}{
		{"matching cookie and header", http.MethodPost, "/v2/CreateBucket", token, token, http.StatusOK, true},
		{"mismatched header", http.MethodPost, "/v2/CreateBucket", token, "not-the-token", http.StatusForbidden, false},
		{"missing header", http.MethodPost, "/v2/CreateBucket", token, "", http.StatusForbidden, false},
		{"missing cookie", http.MethodPost, "/v2/CreateBucket", "", token, http.StatusForbidden, false},
		// Both empty must NOT compare equal — otherwise a tokenless client
		// could write simply by sending an empty header.
		{"no cookie and no header", http.MethodPost, "/v2/CreateBucket", "", "", http.StatusForbidden, false},
		{"PUT is protected", http.MethodPut, "/browse/b/k", token, "", http.StatusForbidden, false},
		{"PATCH is protected", http.MethodPatch, "/v2/UpdateBucket", token, "", http.StatusForbidden, false},
		{"DELETE is protected", http.MethodDelete, "/browse/b/k", token, "", http.StatusForbidden, false},
		{"PUT with a valid token", http.MethodPut, "/browse/b/k", token, token, http.StatusOK, true},
		{"DELETE with a valid token", http.MethodDelete, "/browse/b/k", token, token, http.StatusOK, true},
		// Safe methods must never be rejected: they are how a client gets its
		// first token.
		{"GET needs no token", http.MethodGet, "/buckets", "", "", http.StatusOK, true},
		{"HEAD needs no token", http.MethodHead, "/buckets", "", "", http.StatusOK, true},
		{"OPTIONS needs no token", http.MethodOptions, "/buckets", "", "", http.StatusOK, true},
		// Logout and password change are ordinary writes — not exempt.
		{"logout is protected", http.MethodPost, "/auth/logout", token, "", http.StatusForbidden, false},
		{"change-password is protected", http.MethodPost, "/auth/change-password", token, "", http.StatusForbidden, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached := false
			handler := CSRF(okHandler(&reached))

			req := httptest.NewRequest(tt.method, tt.target, nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tt.cookie})
			}
			if tt.header != "" {
				req.Header.Set(CSRFHeaderName, tt.header)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if reached != tt.wantReach {
				t.Errorf("handler reached = %v, want %v", reached, tt.wantReach)
			}
		})
	}
}

// TestCSRFExemptions pins the exemption list. It is a security boundary: only
// POST /auth/login and POST /setup may pass without a token, and only as exact
// matches. A failure here means the list has been widened — check why.
func TestCSRFExemptions(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantExempt bool
	}{
		{"login", http.MethodPost, "/auth/login", true},
		{"setup", http.MethodPost, "/setup", true},
		{"login with the wrong method", http.MethodPut, "/auth/login", false},
		{"setup with the wrong method", http.MethodDelete, "/setup", false},
		{"a path merely starting with /setup", http.MethodPost, "/setup/anything", false},
		{"a path merely starting with /auth", http.MethodPost, "/auth/logout", false},
		{"change password", http.MethodPost, "/auth/change-password", false},
		{"an ordinary write", http.MethodPost, "/v2/CreateBucket", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.target, nil)
			if got := isCSRFExempt(r); got != tt.wantExempt {
				t.Errorf("isCSRFExempt(%s %s) = %v, want %v", tt.method, tt.target, got, tt.wantExempt)
			}
		})
	}
}

// TestCSRFExemptEndpointsServeWithoutToken is the behavioural half of the
// exemption test: a brand-new browser with no cookie at all must still be able
// to log in and to run the first-run wizard.
func TestCSRFExemptEndpointsServeWithoutToken(t *testing.T) {
	for _, target := range []string{"/auth/login", "/setup"} {
		t.Run(target, func(t *testing.T) {
			reached := false
			handler := CSRF(okHandler(&reached))

			req := httptest.NewRequest(http.MethodPost, target, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("POST %s without a token: status = %d, want 200", target, w.Code)
			}
			if !reached {
				t.Errorf("POST %s without a token did not reach the handler", target)
			}
			// It should still seed a token for the writes that follow.
			if csrfCookieFrom(w.Result()) == nil {
				t.Errorf("POST %s did not set a %s cookie", target, CSRFCookieName)
			}
		})
	}
}

// TestCSRFTokensAreUnique guards against a constant or reused token.
func TestCSRFTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		reached := false
		handler := CSRF(okHandler(&reached))
		req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		cookie := csrfCookieFrom(w.Result())
		if cookie == nil {
			t.Fatal("no token cookie was set")
		}
		if seen[cookie.Value] {
			t.Fatalf("token %q was issued twice", cookie.Value)
		}
		seen[cookie.Value] = true
	}
}

// TestCSRFDoesNotReissueAnExistingToken: a client that already holds a token
// must keep it, or every response would rotate it out from under an in-flight
// request.
func TestCSRFDoesNotReissueAnExistingToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	reached := false
	handler := CSRF(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if c := csrfCookieFrom(w.Result()); c != nil {
		t.Errorf("re-issued a token (%q) to a client that already had one", c.Value)
	}
}
