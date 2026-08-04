package utils

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"
)

type SessionManager struct {
	mgr *scs.SessionManager
}

var Session *SessionManager

const (
	// defaultSessionLifetimeHours is the absolute cap on a session's age,
	// counted from the moment it was created. It is reached even on a session
	// that is in constant use.
	defaultSessionLifetimeHours = 24

	// defaultSessionIdleTimeoutHours logs out a session that has not been
	// touched for a while, which is what most self-hosted admin UIs do.
	defaultSessionIdleTimeoutHours = 2
)

// envHours reads a positive whole number of hours from the environment. Any
// value that is absent, unparseable or <= 0 falls back to the default, with a
// warning for the two cases an operator can actually fix — silently running
// with a different expiry than the one configured would be worse.
func envHours(key string, defaultHours int) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return time.Duration(defaultHours) * time.Hour
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("%s=%q is not a number, using %dh", key, raw, defaultHours)
		return time.Duration(defaultHours) * time.Hour
	}
	if n <= 0 {
		log.Printf("%s=%d must be greater than zero, using %dh", key, n, defaultHours)
		return time.Duration(defaultHours) * time.Hour
	}
	return time.Duration(n) * time.Hour
}

func InitSessionManager() *scs.SessionManager {
	sessMgr := scs.New()
	sessMgr.Lifetime = envHours("SESSION_LIFETIME_HOURS", defaultSessionLifetimeHours)

	// Absolute cap stays at Lifetime; IdleTimeout logs out sessions that have
	// been untouched for a while, which is what most self-hosted UIs do.
	sessMgr.IdleTimeout = envHours("SESSION_IDLE_TIMEOUT_HOURS", defaultSessionIdleTimeoutHours)

	// scs defaults: HttpOnly=true, SameSite=Lax, Secure=false. HttpOnly and
	// SameSite=Lax are what we want and are set explicitly here so a library
	// default change cannot silently weaken them.
	sessMgr.Cookie.HttpOnly = true
	sessMgr.Cookie.SameSite = http.SameSiteLaxMode

	// Secure must be opt-in: browsers reject Secure cookies over plain HTTP,
	// and this UI is commonly served over HTTP on a private network. Operators
	// terminating TLS should set SESSION_COOKIE_SECURE=true.
	sessMgr.Cookie.Secure = GetEnv("SESSION_COOKIE_SECURE", "false") == "true"

	// Scope the cookie to BASE_PATH when the UI is mounted under a prefix.
	if basePath := os.Getenv("BASE_PATH"); basePath != "" {
		sessMgr.Cookie.Path = basePath
	}

	Session = &SessionManager{mgr: sessMgr}
	return sessMgr
}

func (s *SessionManager) Get(r *http.Request, key string) interface{} {
	return s.mgr.Get(r.Context(), key)
}

func (s *SessionManager) Set(r *http.Request, key string, value interface{}) {
	s.mgr.Put(r.Context(), key, value)
}

func (s *SessionManager) Clear(r *http.Request) error {
	return s.mgr.Clear(r.Context())
}

// Renew issues a new session token while preserving the session's data. Call
// this immediately before recording a privilege change (i.e. on login) so that
// a session ID an attacker planted before authentication does not become an
// authenticated one.
func (s *SessionManager) Renew(r *http.Request) error {
	return s.mgr.RenewToken(r.Context())
}
