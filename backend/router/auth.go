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

// loginLimiter throttles attempts per key. It is deliberately simple: a fixed
// window with a small allowance, enough to make online password guessing
// impractical without adding a dependency or a background sweeper goroutine —
// the map is instead swept opportunistically inside allow (see sweep) so a
// long-running process does not accumulate one entry per distinct key forever.
type loginLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	limit     int
	window    time.Duration
	lastSweep time.Time
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

	allowed := len(recent) < l.limit
	if allowed {
		recent = append(recent, now)
	}
	l.attempts[key] = recent

	l.sweep(now)

	return allowed
}

// sweep deletes every key whose attempts have all aged out of the window, so
// the map does not grow without bound over the life of the process. It runs
// under the same lock as allow, at most once per window, which keeps the cost
// to O(keys) rarely rather than on every call.
func (l *loginLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) <= l.window {
		return
	}
	l.lastSweep = now

	cutoff := now.Add(-l.window)
	for key, times := range l.attempts {
		stale := true
		for _, t := range times {
			if t.After(cutoff) {
				stale = false
				break
			}
		}
		if stale {
			delete(l.attempts, key)
		}
	}
}

// Separate buckets so a login attacker cannot also exhaust the allowance for
// changing a password or for the first-run setup wizard — each endpoint gets
// its own budget.
var (
	loginAttempts          = newLoginLimiter(10, time.Minute)
	passwordChangeAttempts = newLoginLimiter(10, time.Minute)
	setupAttempts          = newLoginLimiter(10, time.Minute)
)

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

// clientAddr resolves the address used to key a rate limiter. By default it is
// identical to clientIP: RemoteAddr's host, which is the proxy's own address
// when this service sits behind one — the safe default, because trusting a
// forwarded header lets any client pick its own rate-limit bucket unless a
// proxy that actually overwrites that header genuinely sits in front of this
// process. That is exactly why honouring one is opt-in, off by default, and
// configured by naming the exact header to trust rather than guessed.
//
// When TRUSTED_PROXY_HEADER names a header, its value is used instead. Header
// values may be a comma-separated chain (X-Forwarded-For's format): the last
// entry is the one appended by the trusted proxy itself, while every earlier
// entry — including the first — was written by whoever made the request and is
// freely spoofed. If the header is configured but absent or empty on a given
// request, this falls back to clientIP rather than to a shared empty key.
func clientAddr(r *http.Request) string {
	headerName := utils.GetEnv("TRUSTED_PROXY_HEADER", "")
	if headerName == "" {
		return clientIP(r)
	}

	value := r.Header.Get(headerName)
	if value == "" {
		return clientIP(r)
	}

	parts := strings.Split(value, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return clientIP(r)
	}
	return last
}

// loginKey combines the client address with the username being attempted, so
// one abusive client cannot exhaust the login budget for a different account
// that happens to share the same address (common behind NAT or a proxy).
func loginKey(addr, username string) string {
	return addr + "|" + username
}

func (c *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decodeErr := json.NewDecoder(r.Body).Decode(&body)

	// Key on (client address, username): an address alone is shared by every
	// user behind a NAT or a reverse proxy, so exhausting the budget for one
	// account must not lock out a different one from the same address.
	key := loginKey(clientAddr(r), strings.ToLower(strings.TrimSpace(body.Username)))
	if !loginAttempts.allow(key, time.Now()) {
		utils.ResponseErrorStatus(w, errors.New("too many login attempts, try again later"), http.StatusTooManyRequests)
		return
	}

	if decodeErr != nil {
		utils.ResponseError(w, decodeErr)
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
	// has its own budget, keyed on the username rather than the address: this
	// handler runs on an authenticated session, so the account being probed is
	// already known, and it — not the caller's (possibly shared, possibly
	// proxied) address — is the resource actually under attack. The check has
	// to come *before* the comparison, otherwise a stolen session becomes an
	// unthrottled oracle for the password it could not otherwise read.
	if !passwordChangeAttempts.allow(username, time.Now()) {
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
