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
