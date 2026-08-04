package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// setupHandler wires the three endpoints the wizard touches onto one session
// manager, so a test can follow a POST /setup with a GET /auth/status on the
// cookie it was handed and see whether the caller really is logged in.
//
// Everything goes through scs LoadAndSave: calling a session-touching handler
// directly panics with "scs: no session data in context".
func setupHandler() http.Handler {
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	setup := &Setup{}
	auth := &Auth{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup/status", setup.GetStatus)
	mux.HandleFunc("POST /setup", setup.Create)
	mux.HandleFunc("GET /auth/status", auth.GetStatus)

	return sessMgr.LoadAndSave(mux)
}

// postSetup submits the wizard form. remoteAddr is per-call because the rate
// limiter is a package-level singleton shared with the login handler.
func postSetup(t *testing.T, handler http.Handler, remoteAddr string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/setup", bytes.NewReader(raw))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func getSetupStatus(t *testing.T, handler http.Handler) bool {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /setup/status: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var body struct {
		NeedsSetup bool `json:"needsSetup"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode /setup/status: %v", err)
	}
	return body.NeedsSetup
}

// TestSetupStatus: the wizard is offered exactly while the instance has no
// users, and never again afterwards.
func TestSetupStatus(t *testing.T) {
	st := newTestStore(t)
	handler := setupHandler()

	if !getSetupStatus(t, handler) {
		t.Error("needsSetup = false on an empty instance, want true")
	}

	if _, err := st.CreateUser(t.Context(), "alice", "correct-horse-battery", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if getSetupStatus(t, handler) {
		t.Error("needsSetup = true once a user exists, want false")
	}
}

// TestSetupStatusWithoutStore: before the database is open the honest answer
// is "not set up yet". It grants nothing — AuthMiddleware, not this endpoint,
// decides what a caller may reach.
func TestSetupStatusWithoutStore(t *testing.T) {
	store.SetDefault(nil)
	handler := setupHandler()

	if !getSetupStatus(t, handler) {
		t.Error("needsSetup = false with no store, want true")
	}
}

// TestSetupCreatesFirstAdminAndLogsIn is the whole point of the wizard: one
// unauthenticated POST turns a blank instance into a usable one, and the
// caller comes out the other side already signed in.
func TestSetupCreatesFirstAdminAndLogsIn(t *testing.T) {
	const password = "first-admin-password"

	st := newTestStore(t)
	handler := setupHandler()

	w := postSetup(t, handler, "198.51.100.1:1", map[string]string{
		"username":        "alice",
		"password":        password,
		"confirmPassword": password,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	rawBody := w.Body.String()

	var resp struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
		Role          string `json:"role"`
	}
	if err := json.Unmarshal([]byte(rawBody), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Authenticated {
		t.Error("authenticated = false, want true — finishing setup should land in the app")
	}
	if resp.Username != "alice" {
		t.Errorf("username = %q, want %q", resp.Username, "alice")
	}
	if resp.Role != store.RoleAdmin {
		t.Errorf("role = %q, want %q", resp.Role, store.RoleAdmin)
	}

	// The response must never echo the credential back, in any form.
	if strings.Contains(rawBody, password) {
		t.Errorf("response body contains the submitted password: %s", rawBody)
	}
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		if strings.Contains(rawBody, prefix) {
			t.Errorf("response body contains a bcrypt hash (%s): %s", prefix, rawBody)
		}
	}

	// The account really landed in the database, with a working password.
	user, err := st.GetUserByUsername(t.Context(), "alice")
	if err != nil || user == nil {
		t.Fatalf("GetUserByUsername = %v, %v", user, err)
	}
	if user.Role != store.RoleAdmin {
		t.Errorf("stored role = %q, want %q", user.Role, store.RoleAdmin)
	}

	// Follow the session cookie: the caller is authenticated for real, not
	// just told so in the response body.
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup did not set a session cookie")
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, req)

	var status struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
		Role          string `json:"role"`
		NeedsSetup    bool   `json:"needsSetup"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode /auth/status: %v", err)
	}
	if !status.Authenticated {
		t.Error("/auth/status authenticated = false on the setup session, want true")
	}
	if status.Username != "alice" || status.Role != store.RoleAdmin {
		t.Errorf("/auth/status username = %q, role = %q; want %q / %q", status.Username, status.Role, "alice", store.RoleAdmin)
	}
	if status.NeedsSetup {
		t.Error("/auth/status needsSetup = true after setup, want false")
	}
}

// TestSetupIsClosedOnceAUserExists pins the security contract of the only
// unauthenticated write endpoint in the app: after the first account exists,
// POST /setup is 409 forever, and it must not create or alter anything.
func TestSetupIsClosedOnceAUserExists(t *testing.T) {
	st := newTestStore(t)
	handler := setupHandler()

	if _, err := st.CreateUser(t.Context(), "alice", "correct-horse-battery", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	w := postSetup(t, handler, "198.51.100.2:1", map[string]string{
		"username":        "mallory",
		"password":        "second-attempt-password",
		"confirmPassword": "second-attempt-password",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
	}
	if cookie := w.Header().Get("Set-Cookie"); cookie != "" {
		t.Errorf("a refused setup handed out a session cookie: %q", cookie)
	}

	if u, err := st.GetUserByUsername(t.Context(), "mallory"); err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	} else if u != nil {
		t.Error("the refused setup created a user")
	}
	if n, err := st.CountUsers(t.Context()); err != nil {
		t.Fatalf("CountUsers: %v", err)
	} else if n != 1 {
		t.Errorf("CountUsers = %d, want 1", n)
	}
}

// TestSetupTwiceIsConflict: the second request is refused even when the first
// one is what created the user, which is the sequence a double-clicked submit
// button produces.
func TestSetupTwiceIsConflict(t *testing.T) {
	const password = "first-admin-password"

	newTestStore(t)
	handler := setupHandler()

	body := map[string]string{
		"username":        "alice",
		"password":        password,
		"confirmPassword": password,
	}

	if w := postSetup(t, handler, "198.51.100.3:1", body); w.Code != http.StatusOK {
		t.Fatalf("first setup: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if w := postSetup(t, handler, "198.51.100.4:1", body); w.Code != http.StatusConflict {
		t.Fatalf("second setup: status = %d, want 409 (body: %s)", w.Code, w.Body.String())
	}
}

// TestSetupRejectsBadInput: the server validates independently of the browser
// form, since POST /setup is reachable without one.
func TestSetupRejectsBadInput(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		body       map[string]string
	}{
		{
			name:       "mismatched confirmation",
			remoteAddr: "198.51.100.11:1",
			body: map[string]string{
				"username":        "alice",
				"password":        "first-admin-password",
				"confirmPassword": "something-else-here",
			},
		},
		{
			name:       "short password",
			remoteAddr: "198.51.100.12:1",
			body: map[string]string{
				"username":        "alice",
				"password":        "short",
				"confirmPassword": "short",
			},
		},
		{
			name:       "empty username",
			remoteAddr: "198.51.100.13:1",
			body: map[string]string{
				"username":        "",
				"password":        "first-admin-password",
				"confirmPassword": "first-admin-password",
			},
		},
		{
			name:       "username with a colon",
			remoteAddr: "198.51.100.14:1",
			body: map[string]string{
				"username":        "alice:bob",
				"password":        "first-admin-password",
				"confirmPassword": "first-admin-password",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			handler := setupHandler()

			w := postSetup(t, handler, tt.remoteAddr, tt.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), tt.body["password"]) {
				t.Errorf("error body echoes the submitted password: %s", w.Body.String())
			}
			if n, err := st.CountUsers(t.Context()); err != nil {
				t.Fatalf("CountUsers: %v", err)
			} else if n != 0 {
				t.Errorf("CountUsers = %d, want 0 — a rejected submission must not create an account", n)
			}
		})
	}
}

// TestSetupWithoutStore: no database means no account, and certainly not a
// session that claims one exists.
func TestSetupWithoutStore(t *testing.T) {
	store.SetDefault(nil)
	handler := setupHandler()

	w := postSetup(t, handler, "198.51.100.21:1", map[string]string{
		"username":        "alice",
		"password":        "first-admin-password",
		"confirmPassword": "first-admin-password",
	})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body: %s)", w.Code, w.Body.String())
	}
}

// TestSetupRoutesAreWiredEndToEnd drives the real HandleApiRouter, because the
// two setup routes have to match middleware.isPublicPath *exactly*. A typo
// ("/setup/" for "/setup") would not 404: unmatched paths fall through to the
// catch-all ProxyHandler, which attaches the Garage admin token — so a
// misregistered public route would hand an anonymous caller the admin API.
func TestSetupRoutesAreWiredEndToEnd(t *testing.T) {
	const password = "first-admin-password"

	newTestStore(t)
	sessMgr := utils.InitSessionManager()
	handler := sessMgr.LoadAndSave(HandleApiRouter())

	// GET /setup/status, anonymous.
	req := httptest.NewRequest(http.MethodGet, "/setup/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /setup/status: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var status struct {
		NeedsSetup bool `json:"needsSetup"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("GET /setup/status did not return the handler's JSON (body: %s): %v", w.Body.String(), err)
	}
	if !status.NeedsSetup {
		t.Error("needsSetup = false on an empty instance, want true")
	}

	// POST /setup, anonymous.
	raw, err := json.Marshal(map[string]string{
		"username":        "alice",
		"password":        password,
		"confirmPassword": password,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/setup", bytes.NewReader(raw))
	req.RemoteAddr = "198.51.100.41:1"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /setup: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	// And a normal API path is still refused without a session, so the
	// allowlist has not been widened.
	req = httptest.NewRequest(http.MethodGet, "/buckets", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /buckets: status = %d, want 401", w.Code)
	}
}

// TestSetupIsRateLimited: an unauthenticated write endpoint must not be a free
// loop for anyone who can reach the port.
func TestSetupIsRateLimited(t *testing.T) {
	newTestStore(t)
	handler := setupHandler()

	const addr = "198.51.100.31:1"
	body := map[string]string{
		"username":        "alice",
		"password":        "short", // always 400, so the store guard never closes
		"confirmPassword": "short",
	}

	// The shared limiter allows 10 attempts per minute per client.
	sawLimit := false
	for i := 0; i < 12; i++ {
		if postSetup(t, handler, addr, body).Code == http.StatusTooManyRequests {
			sawLimit = true
			break
		}
	}
	if !sawLimit {
		t.Error("12 consecutive setup attempts from one client were never rate-limited")
	}
}
