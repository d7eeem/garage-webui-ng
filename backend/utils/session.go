package utils

import (
	"net/http"
	"os"
	"time"

	"github.com/alexedwards/scs/v2"
)

type SessionManager struct {
	mgr *scs.SessionManager
}

var Session *SessionManager

func InitSessionManager() *scs.SessionManager {
	sessMgr := scs.New()
	sessMgr.Lifetime = 24 * time.Hour

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
