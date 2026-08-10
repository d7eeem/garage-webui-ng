package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSecurityHeadersSetOnEveryResponse pins the four headers this middleware
// is responsible for. Losing any of them silently reopens the finding this
// middleware exists to close (see plans/043).
func TestSecurityHeadersSetOnEveryResponse(t *testing.T) {
	reached := false
	handler := SecurityHeaders(okHandler(&reached))

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !reached {
		t.Fatal("the wrapped handler did not run")
	}

	res := w.Result()

	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := res.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want %q", got, "no-referrer")
	}

	csp := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want it to contain %q", csp, "frame-ancestors 'none'")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q, want it to contain %q", csp, "script-src 'self'")
	}
}

// TestSecurityHeadersHandlerCSPWins is the load-bearing contract for object
// serving: browse.go's GetOneObject sets its own, much stricter
// Content-Security-Policy: sandbox on caller-controlled bodies. Middleware
// runs before the handler, so the handler's Set call must be able to replace
// this middleware's default policy rather than being overwritten by it.
func TestSecurityHeadersHandlerCSPWins(t *testing.T) {
	const objectPolicy = "sandbox"

	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirrors what GetOneObject does for a served object body.
		w.Header().Set("Content-Security-Policy", objectPolicy)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/browse/bucket/key", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Result().Header.Get("Content-Security-Policy"); got != objectPolicy {
		t.Errorf("Content-Security-Policy = %q, want the handler's own %q to win", got, objectPolicy)
	}
}
