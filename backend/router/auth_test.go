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
