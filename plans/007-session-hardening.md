# Plan 007: Harden session handling on the login path

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/utils/session.go backend/router/auth.go backend/main.go README.md`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none (but `plans/002-verification-baseline.md` should land first if you want the tests to run in CI)
- **Category**: security
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

The session layer is left entirely at library defaults. Three gaps follow from
that, in descending order of how much they matter:

1. **No session token rotation on login.** `alexedwards/scs` provides
   `RenewToken` precisely so an application can issue a fresh session ID when a
   user's privilege level changes. This code writes `authenticated = true` into
   whatever session the browser already presented. An attacker who can plant a
   session cookie in a victim's browser before login — via a subdomain, a shared
   machine, or an XSS on a sibling origin — retains a valid, now-authenticated
   session ID after the victim signs in. This is textbook session fixation and
   the library documents the one-line fix.

2. **The session cookie is never marked `Secure`.** scs defaults `Secure` to
   false, so the cookie is transmitted over plain HTTP. The README explicitly
   recommends running this behind a TLS-terminating reverse proxy — which is
   exactly the deployment where `Secure` should be on and currently is not.

3. **No rate limiting on `POST /auth/login`.** The password is checked with
   bcrypt, whose cost factor slows an attacker considerably, and the route is a
   single-user admin login rather than a large credential surface. But the
   endpoint is unauthenticated by definition and there is nothing to stop an
   unlimited request rate.

**What is already fine, and should not be "fixed":** scs defaults `HttpOnly` to
true and `SameSite` to `Lax`. `SameSite=Lax` blocks the cookie on cross-site
POST requests, which covers the CSRF exposure on every state-changing route in
this application. Do not add CSRF tokens as part of this plan — that is a real
project with real complexity, and the marginal gain over `SameSite=Lax` here is
small.

## Current state

### Files

- `backend/utils/session.go` — the session manager wrapper (33 lines).
- `backend/router/auth.go` — login/logout/status handlers (64 lines).
- `backend/main.go` — where the manager is constructed and installed.
- `README.md` — documents environment variables; a new one needs adding.

### Excerpt 1 — the session manager

`backend/utils/session.go`, the whole file:

```go
package utils

import (
	"net/http"
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
```

Only `Lifetime` is configured. Everything else — the cookie attributes, the
store — is scs's default. The default store is in-memory, which is correct for a
single-process binary; do not change it.

### Excerpt 2 — the login handler

`backend/router/auth.go:15-45`:

```go
func (c *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}

	userPass := strings.Split(utils.GetEnv("AUTH_USER_PASS", ""), ":")
	if len(userPass) < 2 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	if strings.TrimSpace(body.Username) != userPass[0] || bcrypt.CompareHashAndPassword([]byte(userPass[1]), []byte(body.Password)) != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}

	utils.Session.Set(r, "authenticated", true)
	utils.ResponseSuccess(w, map[string]bool{
		"authenticated": true,
	})
}

func (c *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	utils.Session.Clear(r)
	utils.ResponseSuccess(w, true)
}
```

Line 36 writes into the existing session. No `RenewToken` call anywhere.

Note also: `strings.Split(..., ":")` with `len(userPass) < 2` means a bcrypt
hash containing `:` would be truncated. Bcrypt's alphabet is
`./A-Za-z0-9` plus `$`, so `:` cannot appear — this is safe as written. Do not
"fix" it.

### Excerpt 3 — where the manager is installed

`backend/main.go:15-19` and `:46`:

```go
func main() {
	// Initialize app
	godotenv.Load()
	utils.InitCacheManager()
	sessionMgr := utils.InitSessionManager()
```

```go
	if err := http.ListenAndServe(addr, sessionMgr.LoadAndSave(mux)); err != nil {
```

`InitSessionManager` returns the raw `*scs.SessionManager` so `main` can install
its `LoadAndSave` middleware. That is why the constructor returns the concrete
type — keep that.

### Excerpt 4 — the documented env vars

`README.md:141-150`:

```markdown
### Environment Variables

Configurable envs:

- `CONFIG_PATH`: Path to the Garage `config.toml` file. Defaults to `/etc/garage.toml`.
- `BASE_PATH`: Base path or prefix for Web UI.
- `API_BASE_URL`: Garage admin API endpoint URL.
- `API_ADMIN_KEY`: Admin API key.
- `S3_REGION`: S3 Region.
- `S3_ENDPOINT_URL`: S3 Endpoint url.
```

### Repo conventions to match

- **Env var reads** go through `utils.GetEnv(key, defaultValue)` — see
  `backend/utils/utils.go:9-15`. Values are strings; there is no existing
  boolean-env helper, so write one or compare directly.
- **Handlers** end in `utils.ResponseSuccess` / `utils.ResponseErrorStatus`.
- **No new dependencies.** A rate limiter can be ~30 lines of stdlib
  (`sync.Mutex` + a map of timestamps). `golang.org/x/time/rate` is not in
  `go.mod` and this plan does not add it.
- **Tests**: stdlib `testing`, table-driven, `t.Setenv` for env isolation —
  the pattern from `backend/utils/utils_test.go` (plan 002).

## Commands you will need

| Purpose         | Command                                    | Expected on success |
|-----------------|--------------------------------------------|---------------------|
| Go build        | `cd backend && go build ./...`             | exit 0              |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output   |
| Go format check | `cd backend && gofmt -l .`                 | no output           |
| Go tests        | `cd backend && go test -race ./...`        | `ok` per package    |
| Frontend build  | `pnpm run build`                           | exit 0              |

## Scope

**In scope** (the only files you should modify or create):

- `backend/utils/session.go`
- `backend/utils/session_test.go` (create)
- `backend/router/auth.go`
- `backend/router/auth_test.go` (create, or extend if plan 005 created it)
- `backend/main.go`
- `README.md`
- `backend/.env.example`

**Out of scope** (do NOT touch, even though they look related):

- `backend/middleware/auth.go` — enforcement logic is correct.
- CSRF tokens — `SameSite=Lax` already covers the exposure. Adding tokens means
  a server-side token store, a client-side header, and changes to every mutation
  in `src/`. Not this plan.
- The in-memory session store — correct for a single binary. Swapping in Redis
  or SQLite is a deployment-topology decision, not a hardening fix.
- Changing `AUTH_USER_PASS` into a multi-user store — that is plan 010's
  territory.
- `src/` — no frontend change is needed. The login flow already posts to
  `/auth/login` and relies on the cookie.

## Git workflow

- Branch: `advisor/007-session-hardening`
- Conventional commits. Example from `git log`: `feat: add authentication`.
- Suggested commits: `fix: renew session token on login`, `feat: add
  SESSION_COOKIE_SECURE env var`, `feat: rate limit login attempts`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Renew the session token on login

This is the highest-value change in the plan and the smallest.

Add a method to `backend/utils/session.go`:

```go
// Renew issues a new session token while preserving the session's data. Call
// this immediately before recording a privilege change (i.e. on login) so that
// a session ID an attacker planted before authentication does not become an
// authenticated one.
func (s *SessionManager) Renew(r *http.Request) error {
	return s.mgr.RenewToken(r.Context())
}
```

Then in `backend/router/auth.go`, call it before setting the flag:

```go
	if err := utils.Session.Renew(r); err != nil {
		utils.ResponseErrorStatus(w, errors.New("cannot start session"), 500)
		return
	}

	utils.Session.Set(r, "authenticated", true)
	utils.ResponseSuccess(w, map[string]bool{
		"authenticated": true,
	})
```

Note the error is deliberately generic in the response — `err.Error()` from the
session layer is internal detail. Log it if you want visibility, but do not
return it.

Also renew on **logout**, so the post-logout session ID differs from the
authenticated one:

```go
func (c *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	utils.Session.Clear(r)
	_ = utils.Session.Renew(r)
	utils.ResponseSuccess(w, true)
}
```

`Clear` already wipes the data; renewing additionally invalidates the old token.
The error is intentionally ignored here — logout should not fail for the user if
token rotation hiccups, and the data is already gone.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

### Step 2: Make the cookie's `Secure` attribute configurable

Do **not** hardcode `Secure: true`. The README documents plain-HTTP access
(`http://your-ip:3909`) as the normal way to reach this UI, and browsers refuse
to store `Secure` cookies over HTTP — turning it on unconditionally would lock
every existing plain-HTTP user out of their own admin panel with no error
message that explains why.

Make it opt-in, defaulting to off, in `backend/utils/session.go`:

```go
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
```

Add `"os"` to the imports (`net/http` and `time` are already there).

On the `Cookie.Path` change: scs defaults to `/`. Setting it to `BASE_PATH` when
one is configured means the cookie is not sent to unrelated apps sharing the
same host — a real improvement when this UI is mounted at `/garage` behind a
shared reverse proxy.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

Add `backend/utils/session_test.go` (package `utils`):

- `TestSessionCookieDefaults` — call `InitSessionManager()` with no env set,
  assert on the returned `*scs.SessionManager`: `Cookie.HttpOnly == true`,
  `Cookie.SameSite == http.SameSiteLaxMode`, `Cookie.Secure == false`,
  `Cookie.Path == "/"`, `Lifetime == 24*time.Hour`.
- `TestSessionCookieSecureOptIn` — `t.Setenv("SESSION_COOKIE_SECURE", "true")`,
  assert `Cookie.Secure == true`.
- `TestSessionCookieSecureIgnoresOtherValues` — `t.Setenv("SESSION_COOKIE_SECURE", "1")`,
  assert `Cookie.Secure == false`. This pins the exact-match behavior so nobody
  is surprised by `1` or `yes` silently not working. If you would rather accept
  those, use `strconv.ParseBool` and change the test accordingly — but pick one
  and test it.
- `TestSessionCookiePathFollowsBasePath` — `t.Setenv("BASE_PATH", "/garage")`,
  assert `Cookie.Path == "/garage"`.

**Verify**: `cd backend && go test -race ./utils/...` → `ok`, 4 new tests pass.

### Step 3: Rate-limit login attempts

Add a small stdlib-only limiter. Create it inside `backend/router/auth.go`
(it has exactly one consumer, so a separate package would be overkill):

```go
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
```

Add `"sync"`, `"time"`, and `"net"` to the imports.

Then guard the handler. Insert at the very top of `Login`, before decoding the
body:

```go
func (c *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !loginAttempts.allow(clientIP(r), time.Now()) {
		utils.ResponseErrorStatus(w, errors.New("too many login attempts, try again later"), http.StatusTooManyRequests)
		return
	}
```

and add the IP helper:

```go
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
```

**Read that comment and understand it before proceeding.** Behind a reverse
proxy every request shares one `RemoteAddr`, so the limit becomes global rather
than per-client. That is the *safe* failure direction (it throttles harder, not
softer), and 10 attempts per minute is generous enough that a legitimate
operator will not hit it. Do not "improve" this by trusting
`X-Forwarded-For` — that is a spoofable rate-limit bypass unless the trusted
proxy set is configured, which is out of scope here.

Note there is no cleanup goroutine: entries for an IP are pruned on that IP's
next attempt. An attacker cycling through many source IPs can grow the map. For
an admin panel on a private network this is an acceptable trade; noted in
Maintenance notes as a known bound.

Add tests to `backend/router/auth_test.go`:

- `TestLoginLimiterAllowsUpToLimit` — a limiter with limit 3 and a 1-minute
  window allows 3 calls for the same key and denies the 4th.
- `TestLoginLimiterWindowExpires` — after 3 denials, calling with
  `now.Add(2*time.Minute)` is allowed again. Pass `now` explicitly (that is why
  `allow` takes a time parameter) so the test needs no sleeping.
- `TestLoginLimiterIsPerKey` — key `"a"` hitting its limit does not affect
  key `"b"`.
- `TestClientIPStripsPort` — `clientIP` on a request with
  `RemoteAddr = "192.0.2.1:54321"` returns `"192.0.2.1"`; on a malformed
  `RemoteAddr` it returns the raw value.

**Verify**: `cd backend && go test -race ./router/...` → `ok`, 4 new tests pass.

### Step 4: Document the new environment variable

In `README.md`, add to the `### Environment Variables` list (after
`S3_ENDPOINT_URL`):

```markdown
- `SESSION_COOKIE_SECURE`: Set to `true` to mark the session cookie as `Secure`. Enable this when serving the UI over HTTPS. Defaults to `false`, because browsers reject `Secure` cookies sent over plain HTTP.
```

And in the `### Authentication` section, after the existing `docker-compose.yml`
snippet (around line 171), add:

```markdown
Login attempts are rate-limited to 10 per minute per client address.

If you serve the UI over HTTPS — for example behind a reverse proxy that
terminates TLS — also set `SESSION_COOKIE_SECURE: "true"` so the session cookie
is never sent over an unencrypted connection.
```

Then add the variable to `backend/.env.example`:

```
SESSION_COOKIE_SECURE="false"
```

**Verify**: `grep -n "SESSION_COOKIE_SECURE" README.md backend/.env.example` →
matches in both files.

### Step 5: Full verification

```bash
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
```

→ exit 0.

```bash
pnpm run build
```

→ exit 0.

### Step 6: Manual verification

Start the server with authentication enabled and observe the cookie rotation.
You need a bcrypt hash; generate one with `htpasswd -nbBC 10 admin secret` (the
README documents this) or reuse the placeholder in `backend/.env.example`.

1. Load the login page and note the `session` cookie value in devtools.
2. Log in successfully.
3. Note the cookie value again. **It must be different.** If it is identical,
   `RenewToken` is not taking effect — see STOP conditions.
4. Log out. The cookie value changes again and the app redirects to login.
5. Submit 11 failed logins in a minute. The 11th returns HTTP 429.

Report which of the five you verified.

## Test plan

New tests:

| File | Tests | Covers |
|---|---|---|
| `backend/utils/session_test.go` | 4 | cookie attribute configuration (step 2) |
| `backend/router/auth_test.go` | 4 | the rate limiter and IP extraction (step 3) |

Structural pattern: stdlib `testing`, `t.Setenv` for env isolation — match
`backend/utils/utils_test.go` from plan 002.

**Not covered by unit tests, and honestly so**: `RenewToken` itself (step 1).
Asserting that the emitted `Set-Cookie` value changes requires driving the
handler through scs's `LoadAndSave` middleware with a cookie jar — doable, but
it tests the library more than the code. The one-line call is visually
verifiable and step 6.3 is the real check. Note this in your report.

**Verification**: `cd backend && go test -race ./...` → `ok`.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0, including all 8 new tests
- [ ] `cd backend && grep -n "RenewToken" utils/session.go` returns a match
- [ ] `cd backend && grep -n "Session.Renew(r)" router/auth.go` returns 2 matches (login and logout)
- [ ] `cd backend && grep -n "SESSION_COOKIE_SECURE" utils/session.go` returns a match
- [ ] `cd backend && grep -n "StatusTooManyRequests" router/auth.go` returns a match
- [ ] `grep -n "SESSION_COOKIE_SECURE" README.md` returns a match
- [ ] `pnpm run build` exits 0
- [ ] `git status` shows only the in-scope files (plus `plans/README.md`) modified or created
- [ ] `plans/README.md` status row for 007 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- `go` is not installed in your environment. Report this — do not install a
  toolchain, and do not skip verification gates.
- The session cookie value does **not** change after login in step 6.3. That
  means `RenewToken` is not reaching the request's session — most likely because
  the handler runs outside `LoadAndSave`'s scope. Report the observed cookie
  values before and after; do not work around it by clearing the cookie manually.
- `scs.SessionManager.Cookie` does not have the fields this plan sets (the
  library's API changed). Report the actual struct shape rather than guessing at
  equivalents.
- Enabling `SESSION_COOKIE_SECURE=true` over plain HTTP does something other
  than "login appears to succeed but the user is immediately bounced back to the
  login page." That is the expected symptom of a rejected `Secure` cookie and it
  is why the default is off — confirm the failure mode matches before
  documenting it.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **`SESSION_COOKIE_SECURE` defaults to `false` on purpose.** Flipping the
  default would break every plain-HTTP deployment with a symptom (silent
  redirect back to login) that is very hard for a user to diagnose. If the
  project ever decides to flip it, that is a major-version change and needs a
  loud migration note.
- **The rate limiter's map is unbounded.** Entries are pruned lazily on the next
  attempt from the same IP, so an attacker rotating source addresses grows it
  without bound. For an admin panel this is an accepted trade. If this ever
  faces the open internet, replace it with a bounded LRU or a periodic sweep —
  and at that point `golang.org/x/time/rate` plus a bounded cache is worth the
  dependency.
- **The limiter counts all attempts, not just failures.** A successful login
  consumes budget too. That is intentional (simpler, and no benefit to
  distinguishing), but it means a script legitimately re-authenticating in a
  loop will trip it.
- **`X-Forwarded-For` is deliberately not trusted.** If someone later adds
  trusted-proxy configuration, the limiter key is the place to use it — and the
  test `TestClientIPStripsPort` is where to extend coverage.
- **Reviewer should scrutinize**: that `Renew` is called *before*
  `Session.Set(r, "authenticated", true)` and not after (after would rotate away
  the flag you just set), and that the `Secure` default is `false`.
- **Deliberately deferred**: CSRF tokens. `SameSite=Lax`, now set explicitly
  rather than inherited, blocks cross-site cookie transmission on the POST/PUT/
  DELETE routes that matter. Tokens would add a server-side store and touch
  every mutation in the frontend for a marginal gain. Revisit if the app ever
  needs `SameSite=None` (e.g. embedding in a cross-origin iframe), because that
  removes the protection this relies on.
