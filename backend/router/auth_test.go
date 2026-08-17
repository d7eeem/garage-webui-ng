package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"

	"golang.org/x/crypto/bcrypt"
)

// newTestStore opens a throwaway user database and installs it as the
// process-wide store for the duration of the test, mirroring what main.go
// does at startup.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	store.SetDefault(st)
	t.Cleanup(func() {
		store.SetDefault(nil)
		st.Close()
	})
	return st
}

func TestLoginLimiterAllowsUpToLimit(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("client", now) {
		t.Fatal("4th attempt: allow() = true, want false")
	}
}

func TestLoginLimiterWindowExpires(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("client", now) {
		t.Fatal("4th attempt within window: allow() = true, want false")
	}

	later := now.Add(2 * time.Minute)
	if !limiter.allow("client", later) {
		t.Fatal("attempt after window expiry: allow() = false, want true")
	}
}

func TestLoginLimiterIsPerKey(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	now := time.Now()

	for i := 0; i < 3; i++ {
		if !limiter.allow("a", now) {
			t.Fatalf("key a, attempt %d: allow() = false, want true", i+1)
		}
	}

	if limiter.allow("a", now) {
		t.Fatal("key a, 4th attempt: allow() = true, want false")
	}

	if !limiter.allow("b", now) {
		t.Fatal("key b, 1st attempt: allow() = false, want true (should be unaffected by key a)")
	}
}

func TestClientIPStripsPort(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{
			name:       "valid host:port",
			remoteAddr: "192.0.2.1:54321",
			want:       "192.0.2.1",
		},
		{
			name:       "malformed remote addr",
			remoteAddr: "not-a-valid-address",
			want:       "not-a-valid-address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tt.remoteAddr}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoginLimiterSweepsExpiredKeys: once a key's entries have all aged out of
// the window, it must be deleted rather than lingering forever — a
// long-running process seeing many distinct addresses must not accumulate one
// map entry per address permanently.
func TestLoginLimiterSweepsExpiredKeys(t *testing.T) {
	limiter := newLoginLimiter(3, time.Minute)
	t0 := time.Now()

	if !limiter.allow("a", t0) {
		t.Fatal("first attempt for key a: allow() = false, want true")
	}
	if got := len(limiter.attempts); got != 1 {
		t.Fatalf("attempts map has %d keys after one attempt, want 1", got)
	}

	// Well past the window: a's only entry has fully expired. Driving allow
	// for a different key with this later 'now' must trigger the sweep and
	// remove a — no sleeping, the sweep is driven entirely off the explicit
	// 'now' passed to allow.
	later := t0.Add(2 * time.Minute)
	if !limiter.allow("b", later) {
		t.Fatal("first attempt for key b: allow() = false, want true")
	}

	if _, ok := limiter.attempts["a"]; ok {
		t.Error("key a is still present after its entries fully expired and a sweep ran; want it deleted")
	}
	if got := len(limiter.attempts); got != 1 {
		t.Errorf("attempts map has %d keys after sweep, want 1 (only b)", got)
	}
}

// TestClientAddrDefaultUsesRemoteAddr: with TRUSTED_PROXY_HEADER unset (the
// default), clientAddr must behave exactly like clientIP and ignore any
// forwarded header a caller supplies — an untrusted client must not be able to
// pick its own rate-limit bucket just by adding a header.
func TestClientAddrDefaultUsesRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "9.9.9.9")

	if got, want := clientAddr(r), "203.0.113.9"; got != want {
		t.Errorf("clientAddr() = %q, want %q (TRUSTED_PROXY_HEADER unset must ignore forwarded headers)", got, want)
	}
}

// TestClientAddrTrustedProxyHeaderUsesLastHop: X-Forwarded-For is
// append-only and client-writable at the head, so only the final entry was
// added by infrastructure the operator has chosen to trust.
func TestClientAddrTrustedProxyHeaderUsesLastHop(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_HEADER", "X-Forwarded-For")

	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	if got, want := clientAddr(r), "5.6.7.8"; got != want {
		t.Errorf("clientAddr() = %q, want %q (must use the last hop, not the client-supplied first entry)", got, want)
	}
}

// TestClientAddrTrustedProxyHeaderDifferentAddresses: two requests arriving
// through the same proxy (same RemoteAddr) but with different last-hop
// addresses in the trusted header must get separate rate-limit keys.
func TestClientAddrTrustedProxyHeaderDifferentAddresses(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_HEADER", "X-Forwarded-For")

	r1 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r1.RemoteAddr = "10.0.0.1:1"
	r1.Header.Set("X-Forwarded-For", "1.1.1.1")

	r2 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r2.RemoteAddr = "10.0.0.1:1" // same proxy address for both requests
	r2.Header.Set("X-Forwarded-For", "2.2.2.2")

	a1, a2 := clientAddr(r1), clientAddr(r2)
	if a1 == a2 {
		t.Fatalf("clientAddr() gave the same key for two different last-hop addresses: %q", a1)
	}
	if a1 != "1.1.1.1" || a2 != "2.2.2.2" {
		t.Errorf("clientAddr() = %q, %q; want 1.1.1.1, 2.2.2.2", a1, a2)
	}
}

// TestClientAddrTrustedProxyHeaderFallsBackWhenAbsent: a header that is
// configured but missing on a given request must fall back to RemoteAddr, not
// collapse every such request onto one shared empty key.
func TestClientAddrTrustedProxyHeaderFallsBackWhenAbsent(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_HEADER", "X-Forwarded-For")

	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "203.0.113.50:1"
	// The header is deliberately not set on this request.

	if got, want := clientAddr(r), "203.0.113.50"; got != want {
		t.Errorf("clientAddr() = %q, want %q (must fall back to RemoteAddr when the configured header is absent)", got, want)
	}
}

// TestLoginLimiterIsolatesUsernamesAtSameAddress is the core regression this
// plan fixes: behind a reverse proxy, every request arrives from the same
// address, so keying the login limiter on the address alone would let one
// attacker's failed attempts against account A lock out account B too, even
// though B never made a single bad attempt.
func TestLoginLimiterIsolatesUsernamesAtSameAddress(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()

	if _, err := st.CreateUser(ctx, "alice", "alice-s3cret-password", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if _, err := st.CreateUser(ctx, "bob", "bob-s3cret-password", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}

	sessMgr := utils.InitSessionManager()
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	const sameAddr = "198.51.100.77:1"

	login := func(username, password string) int {
		reqBody, err := json.Marshal(map[string]string{"username": username, "password": password})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(reqBody))
		req.RemoteAddr = sameAddr
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	// Exhaust the budget for alice with wrong passwords, all arriving from the
	// same RemoteAddr — simulating every request arriving from one reverse
	// proxy.
	for i := 0; i < 10; i++ {
		if code := login("alice", "wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d against alice: status = %d, want 401", i+1, code)
		}
	}
	if code := login("alice", "wrong-password"); code != http.StatusTooManyRequests {
		t.Fatalf("alice's budget: status = %d, want 429 (limiter should now refuse alice)", code)
	}

	// bob, from the same address, must be unaffected by alice's exhausted
	// budget.
	if code := login("bob", "bob-s3cret-password"); code != http.StatusOK {
		t.Fatalf("bob from the same address as the exhausted alice: status = %d, want 200 (bob must not be locked out by alice's attempts)", code)
	}
}

// TestLoginAndPasswordChangeBucketsAreIndependent: exhausting the Login
// limiter must not also refuse ChangePassword — they are separate budgets, so
// a login attacker cannot lock a legitimate signed-in user out of changing
// their password.
func TestLoginAndPasswordChangeBucketsAreIndependent(t *testing.T) {
	st := newTestStore(t)
	const password = "alice-current-password"
	if _, err := st.CreateUser(t.Context(), "alice", password, store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sessMgr := utils.InitSessionManager()
	loginHandler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	const addr = "198.51.100.88:1"
	login := func() int {
		reqBody, err := json.Marshal(map[string]string{"username": "alice", "password": "wrong-password"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(reqBody))
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		loginHandler.ServeHTTP(w, req)
		return w.Code
	}

	for i := 0; i < 10; i++ {
		if code := login(); code != http.StatusUnauthorized {
			t.Fatalf("login attempt %d: status = %d, want 401", i+1, code)
		}
	}
	if code := login(); code != http.StatusTooManyRequests {
		t.Fatalf("login budget: status = %d, want 429", code)
	}

	// ChangePassword, for the same account and address, must still work: it is
	// a separate budget (passwordChangeAttempts), not the exhausted
	// loginAttempts bucket.
	rec := changePassword(t, "alice", addr, map[string]string{
		"currentPassword": password,
		"newPassword":     "alice-brand-new-password",
		"confirmPassword": "alice-brand-new-password",
	})
	if rec.Code == http.StatusTooManyRequests {
		t.Fatalf("ChangePassword was refused after Login's budget was exhausted: status = %d, want != 429", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ChangePassword status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

type statusResponse struct {
	Enabled       bool   `json:"enabled"`
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username"`
	Role          string `json:"role"`
	NeedsSetup    bool   `json:"needsSetup"`
}

func getStatus(t *testing.T) statusResponse {
	t.Helper()
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).GetStatus))

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /auth/status: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var body statusResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// TestGetStatusNeedsSetup: a fresh deployment has no users, so the UI must be
// told to run the setup wizard rather than show a login form nobody can pass.
func TestGetStatusNeedsSetup(t *testing.T) {
	newTestStore(t)

	body := getStatus(t)
	if !body.Enabled {
		t.Error("enabled = false, want true (authentication is mandatory)")
	}
	if body.Authenticated {
		t.Error("authenticated = true, want false (no session)")
	}
	if !body.NeedsSetup {
		t.Error("needsSetup = false, want true (no users exist)")
	}
}

// TestGetStatusWithUsersNoSession: once a user exists the deployment is set
// up, but an anonymous caller is still unauthenticated. The old open-mode
// behaviour (no AUTH_USER_PASS ⇒ authenticated:true) is gone.
func TestGetStatusWithUsersNoSession(t *testing.T) {
	// Deliberately set: the environment must no longer influence the answer.
	t.Setenv("AUTH_USER_PASS", "")
	t.Setenv("AUTH_VIEWER_USER_PASS", "")

	st := newTestStore(t)
	if _, err := st.CreateUser(t.Context(), "admin", "correct-horse-battery", store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	body := getStatus(t)
	if !body.Enabled {
		t.Error("enabled = false, want true (authentication is mandatory)")
	}
	if body.Authenticated {
		t.Error("authenticated = true, want false (no session)")
	}
	if body.NeedsSetup {
		t.Error("needsSetup = true, want false (a user exists)")
	}
	if body.Username != "" || body.Role != "" {
		t.Errorf("username = %q, role = %q; want both empty for an anonymous caller", body.Username, body.Role)
	}
}

// TestLoginAgainstStore drives the real Login handler (through the scs
// LoadAndSave middleware — calling the handler directly panics with "scs: no
// session data in context") against a temporary user database. Every
// credential here is generated at test time.
func TestLoginAgainstStore(t *testing.T) {
	const (
		adminPassword    = "alice-s3cret-password"
		viewerPassword   = "bob-s3cret-password"
		disabledPassword = "carol-s3cret-password"
	)

	st := newTestStore(t)
	ctx := t.Context()

	if _, err := st.CreateUser(ctx, "alice", adminPassword, store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if _, err := st.CreateUser(ctx, "bob", viewerPassword, store.RoleViewer); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	carol, err := st.CreateUser(ctx, "carol", disabledPassword, store.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	if err := st.SetDisabled(ctx, carol.ID, true); err != nil {
		t.Fatalf("SetDisabled(carol): %v", err)
	}

	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	tests := []struct {
		name       string
		remoteAddr string // distinct per case so the shared rate limiter doesn't interfere
		username   string
		password   string
		wantStatus int
		wantUser   string
		wantRole   string
	}{
		{"admin valid password", "203.0.113.1:1", "alice", adminPassword, http.StatusOK, "alice", store.RoleAdmin},
		{"viewer valid password", "203.0.113.2:1", "bob", viewerPassword, http.StatusOK, "bob", store.RoleViewer},
		{"username casing is canonicalised", "203.0.113.3:1", "ALICE", adminPassword, http.StatusOK, "alice", store.RoleAdmin},
		{"admin wrong password", "203.0.113.4:1", "alice", "not-the-password", http.StatusUnauthorized, "", ""},
		{"viewer wrong password", "203.0.113.5:1", "bob", "not-the-password", http.StatusUnauthorized, "", ""},
		{"unknown user", "203.0.113.6:1", "dave", "whatever-password", http.StatusUnauthorized, "", ""},
		{"disabled user with the right password", "203.0.113.7:1", "carol", disabledPassword, http.StatusUnauthorized, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody, err := json.Marshal(map[string]string{
				"username": tt.username,
				"password": tt.password,
			})
			if err != nil {
				t.Fatalf("marshal request body: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(reqBody))
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantStatus != http.StatusOK {
				// A failed login must not hand out a session cookie.
				if cookie := w.Header().Get("Set-Cookie"); cookie != "" {
					t.Errorf("failed login set a cookie: %q", cookie)
				}
				return
			}

			var resp struct {
				Authenticated bool   `json:"authenticated"`
				Username      string `json:"username"`
				Role          string `json:"role"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !resp.Authenticated {
				t.Error("authenticated = false, want true")
			}
			if resp.Username != tt.wantUser {
				t.Errorf("username = %q, want %q", resp.Username, tt.wantUser)
			}
			if resp.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", resp.Role, tt.wantRole)
			}
		})
	}
}

// TestLoginStampsLastLogin: a successful login records when it happened; a
// failed one must not.
func TestLoginStampsLastLogin(t *testing.T) {
	const password = "alice-s3cret-password"

	st := newTestStore(t)
	ctx := t.Context()

	alice, err := st.CreateUser(ctx, "alice", password, store.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if alice.LastLogin != nil {
		t.Fatalf("last_login = %v before any login, want nil", alice.LastLogin)
	}

	sessMgr := utils.InitSessionManager()
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	login := func(remoteAddr, password string) int {
		reqBody, err := json.Marshal(map[string]string{"username": "alice", "password": password})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(reqBody))
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}

	if code := login("203.0.113.21:1", "wrong-password-here"); code != http.StatusUnauthorized {
		t.Fatalf("failed login status = %d, want 401", code)
	}
	after, err := st.GetUserByID(ctx, alice.ID)
	if err != nil || after == nil {
		t.Fatalf("GetUserByID: %v, %v", after, err)
	}
	if after.LastLogin != nil {
		t.Errorf("last_login = %v after a failed login, want nil", after.LastLogin)
	}

	if code := login("203.0.113.22:1", password); code != http.StatusOK {
		t.Fatalf("successful login status = %d, want 200", code)
	}
	after, err = st.GetUserByID(ctx, alice.ID)
	if err != nil || after == nil {
		t.Fatalf("GetUserByID: %v, %v", after, err)
	}
	if after.LastLogin == nil {
		t.Error("last_login is nil after a successful login")
	}
}

// changePassword drives the real ChangePassword handler with a session that
// names sessionUser (empty ⇒ anonymous). The handler is served inside the scs
// LoadAndSave middleware because it reads the session; calling it directly
// panics with "scs: no session data in context".
//
// remoteAddr is distinct per call site: ChangePassword shares the process-wide
// login limiter, so cases that reuse an address would throttle each other.
func changePassword(t *testing.T, sessionUser, remoteAddr string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session

	rec := httptest.NewRecorder()
	handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sessionUser != "" {
			utils.Session.Set(r, "authenticated", true)
			utils.Session.Set(r, "username", sessionUser)
		}
		(&Auth{}).ChangePassword(rec, r)
	}))

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewReader(raw))
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	return rec
}

// passwordVerifies reports whether plaintext matches the hash currently stored
// for the named user. It reads the database, not the response, so it catches a
// handler that reports success without actually writing.
func passwordVerifies(t *testing.T, st *store.Store, username, plaintext string) bool {
	t.Helper()
	u, err := st.GetUserByUsername(t.Context(), username)
	if err != nil || u == nil {
		t.Fatalf("GetUserByUsername(%q) = %v, %v", username, u, err)
	}
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plaintext)) == nil
}

// TestChangePasswordSuccess: the happy path must actually replace the stored
// hash — the new password verifies afterwards and the old one no longer does.
func TestChangePasswordSuccess(t *testing.T) {
	const (
		oldPassword = "alice-old-password"
		newPassword = "alice-new-password"
	)

	st := newTestStore(t)
	if _, err := st.CreateUser(t.Context(), "alice", oldPassword, store.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rec := changePassword(t, "alice", "198.51.100.1:1", map[string]string{
		"currentPassword": oldPassword,
		"newPassword":     newPassword,
		"confirmPassword": newPassword,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !passwordVerifies(t, st, "alice", newPassword) {
		t.Error("the new password does not verify against the stored hash")
	}
	if passwordVerifies(t, st, "alice", oldPassword) {
		t.Error("the old password still verifies against the stored hash")
	}
}

// TestChangePasswordWorksForAViewer: the read-only role owns its own
// credential too (middleware.isViewerAllowed lets this one write through).
func TestChangePasswordWorksForAViewer(t *testing.T) {
	const (
		oldPassword = "bob-old-password"
		newPassword = "bob-new-password"
	)

	st := newTestStore(t)
	if _, err := st.CreateUser(t.Context(), "bob", oldPassword, store.RoleViewer); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rec := changePassword(t, "bob", "198.51.100.2:1", map[string]string{
		"currentPassword": oldPassword,
		"newPassword":     newPassword,
		"confirmPassword": newPassword,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !passwordVerifies(t, st, "bob", newPassword) {
		t.Error("the new password does not verify against the stored hash")
	}
}

// TestChangePasswordRejections covers every way the request can be refused.
// In all of them the stored hash must be untouched.
func TestChangePasswordRejections(t *testing.T) {
	const currentPassword = "alice-current-password"

	tests := []struct {
		name        string
		sessionUser string
		remoteAddr  string
		body        map[string]string
		wantStatus  int
	}{
		{
			name:        "no session",
			sessionUser: "",
			remoteAddr:  "198.51.100.11:1",
			body: map[string]string{
				"currentPassword": currentPassword,
				"newPassword":     "a-brand-new-password",
				"confirmPassword": "a-brand-new-password",
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:        "wrong current password",
			sessionUser: "alice",
			remoteAddr:  "198.51.100.12:1",
			body: map[string]string{
				"currentPassword": "not-the-current-password",
				"newPassword":     "a-brand-new-password",
				"confirmPassword": "a-brand-new-password",
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:        "confirmation does not match",
			sessionUser: "alice",
			remoteAddr:  "198.51.100.13:1",
			body: map[string]string{
				"currentPassword": currentPassword,
				"newPassword":     "a-brand-new-password",
				"confirmPassword": "a-different-password",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "new password is too short",
			sessionUser: "alice",
			remoteAddr:  "198.51.100.14:1",
			body: map[string]string{
				"currentPassword": currentPassword,
				"newPassword":     "short",
				"confirmPassword": "short",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "new password equals the current one",
			sessionUser: "alice",
			remoteAddr:  "198.51.100.15:1",
			body: map[string]string{
				"currentPassword": currentPassword,
				"newPassword":     currentPassword,
				"confirmPassword": currentPassword,
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "session names a user that no longer exists",
			sessionUser: "ghost",
			remoteAddr:  "198.51.100.16:1",
			body: map[string]string{
				"currentPassword": currentPassword,
				"newPassword":     "a-brand-new-password",
				"confirmPassword": "a-brand-new-password",
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newTestStore(t)
			if _, err := st.CreateUser(t.Context(), "alice", currentPassword, store.RoleAdmin); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}

			rec := changePassword(t, tt.sessionUser, tt.remoteAddr, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !passwordVerifies(t, st, "alice", currentPassword) {
				t.Error("the stored hash changed on a rejected request")
			}
		})
	}
}

// TestChangePasswordRejectsADisabledAccount: a session that outlived the
// account being disabled must not be able to reset its credential back into
// usefulness.
func TestChangePasswordRejectsADisabledAccount(t *testing.T) {
	const currentPassword = "carol-current-password"

	st := newTestStore(t)
	carol, err := st.CreateUser(t.Context(), "carol", currentPassword, store.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.SetDisabled(t.Context(), carol.ID, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}

	rec := changePassword(t, "carol", "198.51.100.21:1", map[string]string{
		"currentPassword": currentPassword,
		"newPassword":     "a-brand-new-password",
		"confirmPassword": "a-brand-new-password",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	if !passwordVerifies(t, st, "carol", currentPassword) {
		t.Error("the stored hash changed for a disabled account")
	}
}

// TestChangePasswordWithoutStore: no database means no way to verify or write
// a credential, which must be a server error rather than a panic.
func TestChangePasswordWithoutStore(t *testing.T) {
	store.SetDefault(nil)

	rec := changePassword(t, "alice", "198.51.100.31:1", map[string]string{
		"currentPassword": "whatever-password",
		"newPassword":     "a-brand-new-password",
		"confirmPassword": "a-brand-new-password",
	})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestLoginWithoutStore: if the process somehow serves a login before the
// database is open, it must fail with a server error rather than panic or,
// worse, let the caller in.
func TestLoginWithoutStore(t *testing.T) {
	store.SetDefault(nil)

	sessMgr := utils.InitSessionManager()
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	reqBody, err := json.Marshal(map[string]string{"username": "alice", "password": "whatever-password"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(reqBody))
	req.RemoteAddr = "203.0.113.31:1"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
