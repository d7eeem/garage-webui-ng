package router

import (
	"encoding/json"
	"errors"
	"khairul169/garage-webui/utils"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Auth struct{}

// loginLimiter throttles login attempts per client IP. It is deliberately
// simple: a fixed window with a small allowance, enough to make online
// password guessing impractical without adding a dependency or a background
// sweeper goroutine.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	limit    int
	window   time.Duration
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

// allow records an attempt for key and reports whether it is within the limit.
func (l *loginLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	recent := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= l.limit {
		l.attempts[key] = recent
		return false
	}

	l.attempts[key] = append(recent, now)
	return true
}

var loginAttempts = newLoginLimiter(10, time.Minute)

// parseUserPass parses AUTH_USER_PASS into username→bcrypt-hash. Entries are
// comma-separated; within an entry, the FIRST ':' splits username from hash
// (bcrypt hashes never contain ',' or ':'). A single "user:hash" yields one entry.
func parseUserPass(raw string) map[string]string {
	users := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		i := strings.Index(entry, ":")
		if i <= 0 || i == len(entry)-1 {
			continue // malformed: no ':', empty user, or empty hash
		}
		users[strings.TrimSpace(entry[:i])] = entry[i+1:]
	}
	return users
}

// clientIP extracts a rate-limiting key from the request. RemoteAddr is used
// directly: this service is typically behind a reverse proxy, and trusting
// X-Forwarded-For without knowing the proxy topology would let a client choose
// its own rate-limit bucket.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (c *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !loginAttempts.allow(clientIP(r), time.Now()) {
		utils.ResponseErrorStatus(w, errors.New("too many login attempts, try again later"), http.StatusTooManyRequests)
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}

	admins := parseUserPass(utils.GetEnv("AUTH_USER_PASS", ""))
	viewers := parseUserPass(utils.GetEnv("AUTH_VIEWER_USER_PASS", ""))
	if len(admins) == 0 && len(viewers) == 0 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	username := strings.TrimSpace(body.Username)
	role := ""
	if h, ok := admins[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "admin"
	} else if h, ok := viewers[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "viewer"
	}
	if role == "" {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}

	if err := utils.Session.Renew(r); err != nil {
		utils.ResponseErrorStatus(w, errors.New("cannot start session"), 500)
		return
	}

	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", username)
	utils.Session.Set(r, "role", role)
	utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": username, "role": role})
}

func (c *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	utils.Session.Clear(r)
	_ = utils.Session.Renew(r)
	utils.ResponseSuccess(w, true)
}

func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != ""

	// When authentication is disabled every request is implicitly authorized,
	// which is what the middleware does too (middleware/auth.go).
	isAuthenticated := !enabled

	if authSession, ok := utils.Session.Get(r, "authenticated").(bool); ok && authSession {
		isAuthenticated = true
	}

	username := ""
	if u, ok := utils.Session.Get(r, "username").(string); ok {
		username = u
	}

	role := ""
	if rr, ok := utils.Session.Get(r, "role").(string); ok {
		role = rr
	}

	utils.ResponseSuccess(w, map[string]any{
		"enabled":       enabled,
		"authenticated": isAuthenticated,
		"username":      username,
		"role":          role,
	})
}
