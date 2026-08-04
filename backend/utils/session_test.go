package utils

import (
	"net/http"
	"testing"
	"time"
)

func TestSessionCookieDefaults(t *testing.T) {
	sessMgr := InitSessionManager()

	if sessMgr.Cookie.HttpOnly != true {
		t.Errorf("Cookie.HttpOnly = %v, want true", sessMgr.Cookie.HttpOnly)
	}
	if sessMgr.Cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie.SameSite = %v, want %v", sessMgr.Cookie.SameSite, http.SameSiteLaxMode)
	}
	if sessMgr.Cookie.Secure != false {
		t.Errorf("Cookie.Secure = %v, want false", sessMgr.Cookie.Secure)
	}
	if sessMgr.Cookie.Path != "/" {
		t.Errorf("Cookie.Path = %q, want %q", sessMgr.Cookie.Path, "/")
	}
	if sessMgr.Lifetime != 24*time.Hour {
		t.Errorf("Lifetime = %v, want %v", sessMgr.Lifetime, 24*time.Hour)
	}
	if sessMgr.IdleTimeout != 2*time.Hour {
		t.Errorf("IdleTimeout = %v, want %v", sessMgr.IdleTimeout, 2*time.Hour)
	}
}

// TestSessionExpiryEnvOverrides: an operator can shorten (or lengthen) either
// window without rebuilding.
func TestSessionExpiryEnvOverrides(t *testing.T) {
	t.Setenv("SESSION_LIFETIME_HOURS", "8")
	t.Setenv("SESSION_IDLE_TIMEOUT_HOURS", "1")

	sessMgr := InitSessionManager()

	if sessMgr.Lifetime != 8*time.Hour {
		t.Errorf("Lifetime = %v, want %v", sessMgr.Lifetime, 8*time.Hour)
	}
	if sessMgr.IdleTimeout != time.Hour {
		t.Errorf("IdleTimeout = %v, want %v", sessMgr.IdleTimeout, time.Hour)
	}
}

// TestSessionExpiryIgnoresInvalidValues: a typo must not silently disable
// expiry (IdleTimeout = 0 means "never idle out" in scs), so anything
// unparseable or non-positive falls back to the compiled-in default.
func TestSessionExpiryIgnoresInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"not a number", "two"},
		{"zero would disable the timeout entirely", "0"},
		{"negative", "-1"},
		{"a duration string is not accepted", "2h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SESSION_LIFETIME_HOURS", tt.value)
			t.Setenv("SESSION_IDLE_TIMEOUT_HOURS", tt.value)

			sessMgr := InitSessionManager()

			if sessMgr.Lifetime != 24*time.Hour {
				t.Errorf("Lifetime = %v, want the %v default", sessMgr.Lifetime, 24*time.Hour)
			}
			if sessMgr.IdleTimeout != 2*time.Hour {
				t.Errorf("IdleTimeout = %v, want the %v default", sessMgr.IdleTimeout, 2*time.Hour)
			}
		})
	}
}

func TestSessionCookieSecureOptIn(t *testing.T) {
	t.Setenv("SESSION_COOKIE_SECURE", "true")

	sessMgr := InitSessionManager()

	if sessMgr.Cookie.Secure != true {
		t.Errorf("Cookie.Secure = %v, want true", sessMgr.Cookie.Secure)
	}
}

func TestSessionCookieSecureIgnoresOtherValues(t *testing.T) {
	t.Setenv("SESSION_COOKIE_SECURE", "1")

	sessMgr := InitSessionManager()

	if sessMgr.Cookie.Secure != false {
		t.Errorf("Cookie.Secure = %v, want false (only the exact string \"true\" should enable it)", sessMgr.Cookie.Secure)
	}
}

func TestSessionCookiePathFollowsBasePath(t *testing.T) {
	t.Setenv("BASE_PATH", "/garage")

	sessMgr := InitSessionManager()

	if sessMgr.Cookie.Path != "/garage" {
		t.Errorf("Cookie.Path = %q, want %q", sessMgr.Cookie.Path, "/garage")
	}
}
