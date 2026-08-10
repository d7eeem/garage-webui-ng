package middleware

import "net/http"

// securityCSP is the default Content-Security-Policy applied to every
// response.
//
// default-src 'self' with no external hosts anywhere in the app (no CDN
// scripts, no external fonts/images — verified by grep across index.html and
// src/**). script-src 'self' has no inline or eval allowance: nothing in this
// application needs either, and losing that guarantee is the whole point of
// this header. style-src allows 'unsafe-inline' because @floating-ui
// (src/components/ui/menu.tsx, src/components/containers/upload-card.tsx)
// positions elements via inline style attributes and cannot work without it —
// style injection is a cosmetic risk, script injection is a session-takeover
// risk, so the two are not treated the same. img-src/font-src allow data: for
// Vite's inlined small assets. frame-ancestors 'none' is what actually stops
// this console being framed by another site; X-Frame-Options is sent too for
// older browsers that do not understand CSP.
const securityCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// SecurityHeaders sets response hardening headers on every response, API and
// SPA alike.
//
// The CSP is deliberately strict on script-src and permissive on style-src:
// @floating-ui positions menus with inline style attributes and cannot work
// without them, whereas nothing in this application needs inline script. Style
// injection is a cosmetic risk; script injection is a session-takeover risk.
//
// frame-ancestors 'none' is what stops this console being framed by another
// site — it supersedes X-Frame-Options, which is sent too for older browsers.
//
// Headers are set before next.ServeHTTP runs, so a handler that needs a
// different policy for its own response (e.g. the object-serving endpoint's
// stricter "sandbox" CSP for caller-controlled bodies) can overwrite this
// header with its own Set call — the last write before WriteHeader wins.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", securityCSP)

		next.ServeHTTP(w, r)
	})
}
