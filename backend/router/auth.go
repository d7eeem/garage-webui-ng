package router

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"

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

// errInvalidCredentials is the single message returned for every failed
// login, whatever the actual reason: it must not tell an attacker whether the
// username exists, is disabled, or simply had the wrong password.
var errInvalidCredentials = errors.New("invalid username or password")

// dummyPasswordHash is compared against when a login names an account that
// does not exist or is disabled, so that those cases cost the same wall-clock
// time as a wrong password. Without it, response latency alone would let an
// attacker enumerate valid usernames. It is derived from random bytes at
// startup and is not a credential: nothing can ever match it.
var dummyPasswordHash = newDummyPasswordHash()

func newDummyPasswordHash() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// Only the CPU cost of the comparison matters here, so a fixed
		// fallback is fine — this value is never accepted as a password.
		secret = []byte("garage-webui-ng-timing-equaliser")
	}
	hash, err := bcrypt.GenerateFromPassword(secret, bcrypt.DefaultCost)
	if err != nil {
		return nil
	}
	return hash
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

	st := store.Default()
	if st == nil {
		utils.ResponseErrorStatus(w, store.ErrNoStore, http.StatusInternalServerError)
		return
	}

	user, err := st.GetUserByUsername(r.Context(), strings.TrimSpace(body.Username))
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot look up user: %w", err))
		return
	}

	// An unknown or disabled account still pays for a bcrypt comparison, so
	// that its response time is indistinguishable from a wrong password.
	if user == nil || user.Disabled {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(body.Password))
		utils.ResponseErrorStatus(w, errInvalidCredentials, http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)); err != nil {
		utils.ResponseErrorStatus(w, errInvalidCredentials, http.StatusUnauthorized)
		return
	}

	if err := utils.Session.Renew(r); err != nil {
		utils.ResponseErrorStatus(w, errors.New("cannot start session"), 500)
		return
	}

	// Best-effort: a failure to stamp last_login must not fail the login.
	if err := st.TouchLastLogin(r.Context(), user.ID); err != nil {
		log.Printf("cannot record last login for user %d: %v", user.ID, err)
	}

	// Record the stored spelling, not what the client typed: usernames are
	// case-insensitive, so the session and the audit log should show one
	// canonical form.
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", user.Username)
	utils.Session.Set(r, "role", user.Role)
	utils.ResponseSuccess(w, map[string]any{
		"authenticated": true,
		"username":      user.Username,
		"role":          user.Role,
	})
}

// ChangePassword lets a signed-in user replace their own password. It is the
// only write a read-only viewer may make (middleware.isViewerAllowed), and it
// only ever touches the account named by the caller's session — the request
// body carries no user id, so there is nothing here to point at somebody else.
//
// Nothing in this handler may log a password or a hash.
func (c *Auth) ChangePassword(w http.ResponseWriter, r *http.Request) {
	username, _ := utils.Session.Get(r, "username").(string)
	if username == "" {
		utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
		return
	}

	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// The decoder error is not echoed back: it can quote the request body,
		// which is exactly where the passwords are.
		utils.ResponseErrorStatus(w, errors.New("invalid request body"), http.StatusBadRequest)
		return
	}

	if body.NewPassword != body.ConfirmPassword {
		utils.ResponseErrorStatus(w, errors.New("passwords do not match"), http.StatusBadRequest)
		return
	}

	st := store.Default()
	if st == nil {
		utils.ResponseErrorStatus(w, store.ErrNoStore, http.StatusInternalServerError)
		return
	}

	user, err := st.GetUserByUsername(r.Context(), username)
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot look up user: %w", err))
		return
	}
	if user == nil || user.Disabled {
		// The session names an account that no longer exists or has been
		// disabled since it signed in. Treat it as unauthenticated.
		utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
		return
	}

	// Verifying the current password is a password guess like any other, so it
	// shares the login limiter. The check has to come *before* the comparison,
	// otherwise a stolen session becomes an unthrottled oracle for the
	// password it could not otherwise read.
	if !loginAttempts.allow(clientIP(r), time.Now()) {
		utils.ResponseErrorStatus(w, errors.New("too many attempts, try again later"), http.StatusTooManyRequests)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.CurrentPassword)); err != nil {
		utils.ResponseErrorStatus(w, errors.New("current password is incorrect"), http.StatusUnauthorized)
		return
	}

	if body.NewPassword == body.CurrentPassword {
		utils.ResponseErrorStatus(w, errors.New("the new password must differ from the current one"), http.StatusBadRequest)
		return
	}

	if err := st.SetPassword(r.Context(), user.ID, body.NewPassword); err != nil {
		if errors.Is(err, store.ErrWeakPassword) {
			utils.ResponseErrorStatus(w, err, http.StatusBadRequest)
			return
		}
		utils.ResponseError(w, fmt.Errorf("cannot change password: %w", err))
		return
	}

	// Session-fixation hygiene: a credential change is a privilege change, so
	// issue a fresh session token while keeping the session's data (the caller
	// stays signed in).
	if err := utils.Session.Renew(r); err != nil {
		log.Printf("cannot renew session for user %d after a password change: %v", user.ID, err)
	}

	log.Printf("user %q changed their password", user.Username)

	utils.ResponseSuccess(w, true)
}

func (c *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	utils.Session.Clear(r)
	_ = utils.Session.Renew(r)
	utils.ResponseSuccess(w, true)
}

// GetStatus is the one endpoint the UI may call before authenticating (see
// middleware.isPublicPath), so it must never leak more than: is anyone logged
// in on this session, and does this deployment still need its first account?
func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	// Authentication is mandatory since users moved into the database, so
	// "enabled" is a constant. The field stays in the payload because the
	// frontend still reads it.
	const enabled = true

	// A missing store means startup has not finished; report the deployment
	// as un-set-up rather than pretending it is ready. This never grants
	// access — the middleware is the authority on that.
	needsSetup := true
	if st := store.Default(); st != nil {
		count, err := st.CountUsers(r.Context())
		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot count users: %w", err))
			return
		}
		needsSetup = count == 0
	}

	// Authentication comes only from the session now.
	isAuthenticated := false
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
		"needsSetup":    needsSetup,
	})
}
