# Plan 054: Make the login limiter usable behind a reverse proxy

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**:
> ```
> git diff --stat 947879d..HEAD -- backend/router/auth.go backend/router/setup.go .env.example README.md
> ```
> On a mismatch with the "Current state" excerpts, STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — a wrong trusted-proxy default would let clients choose their own rate-limit bucket
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `947879d`, 2026-08-13

## Why this matters

The login limiter keys on `r.RemoteAddr`. **In the deployment this project
documents — behind a reverse proxy — every request arrives from the proxy's
address**, so all users share a single 10-attempts-per-minute budget. Ten failed
logins from one person locks out *everyone*, and a trivial loop keeps the console
permanently unauthenticatable. A rate limiter that converts one attacker into a
denial of administration for the whole organisation is worse than none, because
operators believe they are protected.

Two further defects compound it:

- The budget is **shared** with `ChangePassword` and `Setup.Create`, so login
  attempts also exhaust the allowance for changing a password.
- The `attempts` map **never deletes keys**, even when a key's window empties. On
  a directly-exposed instance that is unbounded growth driven by source-address
  diversity.

The existing comment correctly refuses to blindly trust `X-Forwarded-For` — that
reasoning is sound and this plan keeps it. The fix is to make the trust
**explicit and opt-in**, so an operator who knows their topology can configure
it, and everyone else keeps today's behaviour.

## Current state

### `backend/router/auth.go:42-62` — the limiter

```go
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

Note `l.attempts[key] = recent` — the key is rewritten, never deleted, so an
entry persists for the process lifetime once created.

### `backend/router/auth.go:96-102` — the key

```go
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

### The three call sites sharing one budget

- `backend/router/auth.go:105` — `Login`
- `backend/router/auth.go:219` — `ChangePassword`
- `backend/router/setup.go:59` — `Setup.Create`

### Conventions

- Environment variables are read with `utils.GetEnv(name, default)`.
- Go tests are table-driven `testing`; session-touching handlers must be served
  through `sessMgr.LoadAndSave(...)` — see `backend/router/auth_test.go:350-360`.
- `.env.example` documents every operator-facing variable; the README carries an
  env table.

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

## Scope

**In scope:**
- `backend/router/auth.go` — the limiter, the key derivation, the buckets
- `backend/router/auth_test.go` — extend
- `backend/router/setup.go` — only the limiter call, if the bucket changes
- `.env.example` and `README.md` — document the new variable

**Out of scope — do NOT touch:**
- The credential comparison, the dummy-hash timing equaliser, or
  `errInvalidCredentials`. The single-message-for-every-failure property is
  deliberate anti-enumeration behaviour — do not make failures distinguishable.
- Session creation, `Renew`, or anything in `backend/utils/session.go`.
- The `/setup` bootstrap semantics (the 409-forever behaviour).
- `backend/middleware/` — this is not a middleware change.

## Git workflow

- Branch: `advisor/054-login-limiter-proxy-aware` from your given base.
- Conventional commit, e.g. `fix(security): make the login limiter proxy-aware`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Stop the map growing without bound

In `allow`, when the filtered `recent` slice is empty **and** the attempt is
being recorded, there is nothing to clean; but when a key's window has fully
expired and no new attempt is being added, the entry should go. Concretely:

- After filtering, if `len(recent) == 0` and the call is about to record a new
  attempt, that is fine — the key stays with one entry.
- Add a periodic full sweep: track a `lastSweep time.Time` on the limiter, and
  when `now.Sub(l.lastSweep) > l.window`, range the map and `delete` every key
  whose entries are all older than the cutoff.

The sweep runs under the existing mutex — it is O(keys) at most once per window,
which is cheap for a login path.

**Verify**: `cd backend && go build ./... && go test -race ./router/ -run TestLoginLimiter` → `ok`.

### Step 2: Separate the buckets

One shared limiter for login, password change and setup means a login attacker
also blocks password changes. Give each its own:

- `loginAttempts` — used by `Login` only
- `passwordChangeAttempts` — used by `ChangePassword`
- `setupAttempts` — used by `Setup.Create`

Keep the same limit and window for each (10/minute) unless a different value is
clearly justified; the point is isolation, not retuning.

Additionally, key `ChangePassword` on the **username** rather than the IP. That
handler runs on an authenticated session, so the username is known and is the
resource actually being brute-forced (the current-password check).

**Verify**: `cd backend && go build ./... && go vet ./...` → clean.

### Step 3: Opt-in trusted-proxy support

Add a new environment variable, read via `utils.GetEnv`:

```
TRUSTED_PROXY_HEADER   (unset by default)
```

Behaviour:

- **Unset (the default): behave exactly as today** — key on `RemoteAddr`'s host.
  Existing deployments must not change behaviour by upgrading.
- When set to a header name (e.g. `X-Forwarded-For`), take the client address
  from that header. For `X-Forwarded-For` specifically, use the **last** entry,
  not the first — the first is client-supplied and freely spoofed, whereas the
  last hop was appended by the proxy you are choosing to trust.
- If the header is set but absent or unparseable on a given request, fall back to
  `RemoteAddr` rather than to a shared empty key.

Write a comment at that function explaining the threat model in one or two lines:
trusting a forwarded header lets a client pick its own bucket unless a proxy is
genuinely in front, which is exactly why this is opt-in and off by default.

Also key the login bucket on **`(clientAddr, username)`** rather than the address
alone, so one abusive client cannot exhaust the budget for a different account
even when the address is shared.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → no output, exit 0.

### Step 4: Tests

Extend `backend/router/auth_test.go`:

1. **The core regression**: with `TRUSTED_PROXY_HEADER` unset and two different
   usernames arriving from the **same** `RemoteAddr`, exhausting the budget for
   user A must **not** lock out user B. This is the finding; without this test
   the plan is not done.
2. `TRUSTED_PROXY_HEADER=X-Forwarded-For`: two requests with different
   last-hop addresses in the header get **separate** buckets.
3. With the header configured but **absent** from the request, the limiter falls
   back to `RemoteAddr` and still limits (does not become a single shared empty
   key).
4. **Spoofing resistance**: with `X-Forwarded-For: 1.2.3.4, 5.6.7.8`, the key is
   derived from `5.6.7.8` (the last hop), not `1.2.3.4`.
5. **The map is swept**: after the window elapses, keys whose attempts have all
   expired are gone. Assert on the map length through a small test-only accessor
   or by exporting a `len()` helper on the limiter — do not sleep; drive `allow`
   with an explicit `now` (it already takes one).
6. **Buckets are independent**: exhausting `Login` does not refuse
   `ChangePassword`.

Use `t.Setenv` for the variable. Note `t.Setenv` forbids `t.Parallel()`.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestLoginLimiter|TestClientKey"` → all `PASS`.

### Step 5: Document it

- `.env.example`: add a commented `TRUSTED_PROXY_HEADER` entry next to the other
  network-facing settings, stating that it must only be set when a proxy you
  control is in front, and that setting it wrongly lets clients choose their own
  rate-limit bucket.
- `README.md`: add the variable to the env table with the same one-line warning.

**Verify**: `grep -n "TRUSTED_PROXY_HEADER" .env.example README.md` → matches in both.

### Step 6: Commit, then prove the tests can fail

**Commit first.** Back up `backend/router/auth.go` outside the repo, then one
mutation at a time, restoring immediately after each:

1. Key the login bucket on the address only (drop the username) → test 1 **must fail**.
2. Take the **first** `X-Forwarded-For` entry instead of the last → test 4 **must fail**.
3. Remove the sweep → test 5 **must fail**.

Report all three; confirm `git status --porcelain` is clean.

### Step 7: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

## Done criteria

- [ ] Step 7 passes; all packages `ok`
- [ ] Step 6's three mutations each failed the named test, and were reverted
- [ ] `grep -c "loginAttempts" backend/router/` shows the login limiter is no longer shared by `ChangePassword` and `Setup.Create`
- [ ] `grep -n "TRUSTED_PROXY_HEADER" backend/router/auth.go .env.example README.md` → present in all three
- [ ] Default behaviour is unchanged: with the variable unset, a test asserts the key still derives from `RemoteAddr`
- [ ] `git diff 947879d..HEAD -- backend/middleware/ backend/utils/session.go src/` is **empty**
- [ ] `git diff --stat 947879d..HEAD` lists only in-scope files

## STOP conditions

- You are about to make `X-Forwarded-For` trusted **by default**. It must be
  opt-in; the existing comment in this file explains why, and that reasoning
  stands.
- You are about to use the **first** `X-Forwarded-For` entry.
- You are about to change what a failed login returns, or make failure reasons
  distinguishable to the caller.
- You conclude the limiter should move into middleware — out of scope here.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **Default-off is the security property.** A trusted-proxy setting that defaults
  on turns a rate limiter into a client-controlled bucket selector. If someone
  later proposes auto-detecting the proxy, the answer is no — detection is
  spoofable by exactly the traffic the limiter is meant to constrain.
- **Last hop, not first.** `X-Forwarded-For` is append-only and client-writable
  at the head; only the final entry was added by infrastructure you control.
- The per-username dimension matters more than the per-IP one behind a proxy.
  Keep both: IP alone fails behind a proxy, username alone fails against
  credential-stuffing across many accounts.
- Deferred deliberately: no lockout escalation or persistent ban list. This
  process keeps almost no state by design, and a memory-resident limiter resets
  on restart — acceptable for the threat this addresses.
