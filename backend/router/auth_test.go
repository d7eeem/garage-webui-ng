package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"khairul169/garage-webui/utils"

	"golang.org/x/crypto/bcrypt"
)

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

func TestGetStatusAuthDisabled(t *testing.T) {
	t.Setenv("AUTH_USER_PASS", "")
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).GetStatus))

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var body struct {
		Enabled       bool `json:"enabled"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled != false || body.Authenticated != true {
		t.Errorf("got enabled=%v authenticated=%v; want false,true", body.Enabled, body.Authenticated)
	}
}

func TestGetStatusAuthEnabledNoSession(t *testing.T) {
	t.Setenv("AUTH_USER_PASS", "u:hash")
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).GetStatus))

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var body struct {
		Enabled       bool `json:"enabled"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled != true || body.Authenticated != false {
		t.Errorf("got enabled=%v authenticated=%v; want true,false", body.Enabled, body.Authenticated)
	}
}

func TestParseUserPass(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "single entry",
			raw:  "u:h",
			want: map[string]string{"u": "h"},
		},
		{
			name: "two entries",
			raw:  "a:h1,b:h2",
			want: map[string]string{"a": "h1", "b": "h2"},
		},
		{
			name: "whitespace around entries trimmed",
			raw:  " a:h1 , b:h2 ",
			want: map[string]string{"a": "h1", "b": "h2"},
		},
		{
			name: "empty raw yields no entries",
			raw:  "",
			want: map[string]string{},
		},
		{
			name: "malformed entries skipped: no colon, empty user, empty hash, blank",
			raw:  "noColon,:h,u:,,a:h1",
			want: map[string]string{"a": "h1"},
		},
		{
			name: "hash containing $ kept intact",
			raw:  "u:$2y$10$abc",
			want: map[string]string{"u": "$2y$10$abc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseUserPass(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseUserPass(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("parseUserPass(%q)[%q] = %q, ok=%v; want %q", tt.raw, k, gotV, ok, wantV)
				}
			}
		})
	}
}

// TestLoginMultiUser drives the real Login handler (through the scs
// LoadAndSave middleware, per the GetStatus test pattern above — calling the
// handler directly panics with "scs: no session data in context") against an
// AUTH_USER_PASS containing two users with freshly generated bcrypt hashes.
// Credentials are generated at test time; nothing here is a real credential.
func TestLoginMultiUser(t *testing.T) {
	aliceHash, err := bcrypt.GenerateFromPassword([]byte("alice-s3cret-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword(alice): %v", err)
	}
	bobHash, err := bcrypt.GenerateFromPassword([]byte("bob-s3cret-2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword(bob): %v", err)
	}

	t.Setenv("AUTH_USER_PASS", fmt.Sprintf("alice:%s,bob:%s", aliceHash, bobHash))
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	tests := []struct {
		name       string
		remoteAddr string // distinct per case so the shared login rate limiter doesn't interfere
		username   string
		password   string
		wantStatus int
	}{
		{"alice valid password", "203.0.113.1:1", "alice", "alice-s3cret-1", http.StatusOK},
		{"bob valid password", "203.0.113.2:1", "bob", "bob-s3cret-2", http.StatusOK},
		{"alice wrong password", "203.0.113.3:1", "alice", "not-the-password", http.StatusUnauthorized},
		{"bob wrong password", "203.0.113.4:1", "bob", "not-the-password", http.StatusUnauthorized},
		{"unknown user", "203.0.113.5:1", "carol", "whatever", http.StatusUnauthorized},
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
				return
			}

			var resp struct {
				Authenticated bool   `json:"authenticated"`
				Username      string `json:"username"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !resp.Authenticated {
				t.Error("authenticated = false, want true")
			}
			if resp.Username != tt.username {
				t.Errorf("username = %q, want %q", resp.Username, tt.username)
			}
		})
	}
}

// TestLoginStampsRole drives the real Login handler against an
// AUTH_USER_PASS (admin) and an AUTH_VIEWER_USER_PASS (viewer), each with a
// freshly generated bcrypt hash, and asserts the session/response role
// matches which map the username was found in. Credentials are generated at
// test time; nothing here is a real credential.
func TestLoginStampsRole(t *testing.T) {
	adminHash, err := bcrypt.GenerateFromPassword([]byte("admin-s3cret-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword(admin): %v", err)
	}
	viewerHash, err := bcrypt.GenerateFromPassword([]byte("viewer-s3cret-2"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt.GenerateFromPassword(viewer): %v", err)
	}

	t.Setenv("AUTH_USER_PASS", fmt.Sprintf("admin:%s", adminHash))
	t.Setenv("AUTH_VIEWER_USER_PASS", fmt.Sprintf("viewer:%s", viewerHash))
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).Login))

	tests := []struct {
		name       string
		remoteAddr string // distinct per case so the shared login rate limiter doesn't interfere
		username   string
		password   string
		wantStatus int
		wantRole   string
	}{
		{"admin valid password", "203.0.113.11:1", "admin", "admin-s3cret-1", http.StatusOK, "admin"},
		{"viewer valid password", "203.0.113.12:1", "viewer", "viewer-s3cret-2", http.StatusOK, "viewer"},
		{"admin wrong password", "203.0.113.13:1", "admin", "not-the-password", http.StatusUnauthorized, ""},
		{"viewer wrong password", "203.0.113.14:1", "viewer", "not-the-password", http.StatusUnauthorized, ""},
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
			if resp.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", resp.Role, tt.wantRole)
			}
		})
	}
}
