# Plan 017: Multi-user local accounts (D3)

> **Executor instructions**: Follow step by step. Run every verification command
> and confirm the expected result before moving on. Touch only in-scope files.
> On a STOP condition, stop and report. SKIP updating `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/017-multi-user-accounts main`
> then `git log --oneline -1` — MUST show `bcedef0` or a "Merge branch 'advisor/015"
> commit (or newer main), NOT `ee420fb`. If wrong, STOP.

## Status

- **Priority**: P2 (feature — gates plan 018 / 010 scoped roles)
- **Effort**: M
- **Risk**: MED — authentication code
- **Depends on**: none. **Gates 010** (roles attach to the identities this adds).
- **Category**: direction / feature
- **Planned at**: commit `bcedef0` (main after 015), 2026-08-03

## Why this matters

Auth today is a **single** shared credential: `AUTH_USER_PASS=username:bcrypt_hash`.
A team can't have per-person logins, individual rotation, or offboarding, and
nothing records **who** is acting. This extends `AUTH_USER_PASS` to **multiple**
comma-separated `username:hash` entries, authenticates against any of them, and
stores the logged-in **username** in the session — which is the identity that the
later roles (010) and audit log (D4) will attach to.

**Decision (settled): multi-user local accounts**, not OIDC. Extend the existing
env var; a single `username:hash` stays valid (backward compatible).

## Current state

### `backend/router/auth.go` — single-credential parse

```go
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
	userPass := strings.Split(utils.GetEnv("AUTH_USER_PASS", ""), ":")
	if len(userPass) < 2 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}
	if strings.TrimSpace(body.Username) != userPass[0] || bcrypt.CompareHashAndPassword([]byte(userPass[1]), []byte(body.Password)) != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}
	if err := utils.Session.Renew(r); err != nil {
		utils.ResponseErrorStatus(w, errors.New("cannot start session"), 500)
		return
	}
	utils.Session.Set(r, "authenticated", true)
	utils.ResponseSuccess(w, map[string]bool{"authenticated": true})
}
```

`GetStatus` returns `map[string]bool{"enabled":..., "authenticated":...}` and
reads the session with the comma-ok form:

```go
func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != ""
	isAuthenticated := !enabled
	if authSession, ok := utils.Session.Get(r, "authenticated").(bool); ok && authSession {
		isAuthenticated = true
	}
	utils.ResponseSuccess(w, map[string]bool{"enabled": enabled, "authenticated": isAuthenticated})
}
```

`utils.Session.Set/Get/Renew` and the rate limiter already exist. `bcrypt`,
`strings`, `errors` imported.

**bcrypt hashes contain `$ . /` and alphanumerics but never `,` or `:`** — so
splitting the env on `,` (between entries) then on the **first** `:` (username vs
hash) is unambiguous.

### `src/hooks/useAuth.ts` — the client auth state

```ts
type AuthResponse = { enabled: boolean; authenticated: boolean; };
export const useAuth = () => {
  const { data, isLoading } = useQuery({ queryKey: ["auth"], queryFn: () => api.get<AuthResponse>("/auth/status"), retry: false });
  return { isLoading, isEnabled: data?.enabled ?? false, isAuthenticated: data?.authenticated ?? false };
};
```

### `src/components/containers/sidebar.tsx` — already has a `LogoutButton`

The sidebar imports `useAuth` and renders `<LogoutButton />` when `auth.isEnabled`
(around line 87). The identity string goes next to it.

### `backend/router/auth_test.go` — exists (from plans 005/007)

Has `TestLoginLimiter*`, `TestClientIPStripsPort`, `TestGetStatusAuthDisabled`,
`TestGetStatusAuthEnabledNoSession`. **Extend, do not overwrite.** The GetStatus
tests serve the handler through `sessMgr.LoadAndSave(http.HandlerFunc(...))` —
follow that pattern for any new session-touching test (calling the handler
directly panics: `scs: no session data in context`).

## Commands

`pnpm` not installed → `npx pnpm@9 <cmd>`.

| Purpose | Command | Expected |
|---|---|---|
| Go build/vet/fmt | `cd backend && go build ./... && go vet ./... && gofmt -l .` | exit 0, no output |
| Go tests | `cd backend && go test -race ./...` | `ok` |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Frontend test | `npx pnpm@9 run test` | all pass |
| Build | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/router/auth.go` (`parseUserPass` helper; multi-user `Login`; `username` in session + `GetStatus`)
- `backend/router/auth_test.go` (extend — parse + multi-user login + status username)
- `README.md` (multi-user format)
- `backend/.env.example` (multi-user example)
- `src/hooks/useAuth.ts` (expose `username`)
- `src/components/containers/sidebar.tsx` ("Logged in as {username}" beside logout)

**Out of scope**: roles/permissions (that's 010), OIDC/SSO, a user-management UI,
audit logging (D4), the middleware (`middleware/auth.go` — its authenticated-bool
gate is unchanged; multi-user still boils down to one authenticated session).

## Steps

### Step 1: `parseUserPass` helper

In `auth.go`, add:

```go
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
```

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 2: Multi-user `Login`

Replace the `userPass := strings.Split(...)` block and the credential check with:

```go
	users := parseUserPass(utils.GetEnv("AUTH_USER_PASS", ""))
	if len(users) == 0 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	username := strings.TrimSpace(body.Username)
	hash, ok := users[username]
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}
```

Then, after `Session.Renew`, store the username alongside the authenticated flag:

```go
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", username)
	utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": username})
```

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 3: `GetStatus` returns the username

Change `GetStatus` to include the session username (empty string when not logged
in or auth disabled):

```go
func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != ""
	isAuthenticated := !enabled
	username := ""
	if authSession, ok := utils.Session.Get(r, "authenticated").(bool); ok && authSession {
		isAuthenticated = true
	}
	if u, ok := utils.Session.Get(r, "username").(string); ok {
		username = u
	}
	utils.ResponseSuccess(w, map[string]any{
		"enabled":       enabled,
		"authenticated": isAuthenticated,
		"username":      username,
	})
}
```

**Verify**: `cd backend && go build ./... && go test -race ./router/...` → the
existing GetStatus tests still pass (they assert on `enabled`/`authenticated`;
decode into a struct or `map[string]any` — if a test decoded into
`map[string]bool`, update it to tolerate the string field: decode the two bools
you assert on individually, e.g. into a struct with `Enabled`/`Authenticated`
fields, ignoring `username`). Do NOT weaken what they assert.

### Step 4: Tests

Extend `auth_test.go`:
- `TestParseUserPass` — single `"u:h"` → one entry; `"a:h1,b:h2"` → two;
  whitespace trimmed; malformed entries (`"noColon"`, `":h"`, `"u:"`, empty)
  skipped; a hash containing `$` (e.g. `"u:$2y$10$abc"`) kept intact.
- `TestLoginMultiUser` — set `AUTH_USER_PASS` via `t.Setenv` to two entries with
  **real bcrypt hashes you generate in the test** (`bcrypt.GenerateFromPassword`)
  for two distinct passwords; drive `Login` through `sessMgr.LoadAndSave(...)`
  (like the GetStatus tests) with a JSON body for each user; assert each valid
  pair → 200, a wrong password → 401, an unknown user → 401. Never hardcode a
  real credential; generate hashes at test time.

**Verify**: `cd backend && go test -race ./router/...` → `ok`, new tests pass;
`grep -c "^func Test" backend/router/auth_test.go` increased (007/005 tests intact).

### Step 5: Frontend — expose username

`src/hooks/useAuth.ts`: add `username?: string` to `AuthResponse` and return
`username: data?.username`.

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 6: Sidebar identity

In `sidebar.tsx`, where `<LogoutButton />` renders (guarded by `auth.isEnabled`),
show the logged-in user when present, e.g. a small muted line "Signed in as
{auth.username}" above/beside the logout button. Keep it minimal and consistent
with the sidebar's styling. Only render the username line when `auth.username` is
truthy.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run lint && npx pnpm@9 run build` →
typecheck & build exit 0; lint red only on pre-existing (confirm `sidebar`/`useAuth`
add no NEW errors).

### Step 7: Docs

`README.md` Authentication section — document that `AUTH_USER_PASS` accepts
multiple comma-separated `username:hash` entries for multiple users, e.g.
`AUTH_USER_PASS="alice:$2y$10$...,bob:$2y$10$..."`, and that a single entry still
works. Keep the existing `$$`-escaping-in-compose note.

`backend/.env.example` — show a two-user example (the existing placeholder hash,
duplicated with a second username, is fine — it's a documented placeholder, not a
live credential).

**Verify**: `grep -c "," README.md` (sanity) and manual read; `npx pnpm@9 run build` unaffected.

### Step 8: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0.

## Test plan

- **Go**: `TestParseUserPass` (parsing edge cases) and `TestLoginMultiUser`
  (real bcrypt, two users, through `LoadAndSave`) are the core coverage. Existing
  auth tests must stay green.
- **Frontend**: typecheck + build gate the wiring.
- **Live verification is the reviewer's job**: run with `AUTH_USER_PASS` set to
  two users, log in as each, confirm `/auth/status` returns the right `username`,
  and confirm a wrong password / unknown user is rejected.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` all exit 0
- [ ] `grep -n "parseUserPass" backend/router/auth.go` matches; `grep -c "^func Test" backend/router/auth_test.go` ≥ 6
- [ ] `grep -n 'Session.Set(r, "username"' backend/router/auth.go` matches
- [ ] Existing `TestGetStatusAuthDisabled`/`TestGetStatusAuthEnabledNoSession` still pass
- [ ] `git diff --name-only bcedef0..HEAD` shows only the 6 in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 017 updated

## STOP conditions

- Base reset shows `ee420fb`.
- Current-state excerpts don't match live code.
- Changing `GetStatus`'s return type breaks an existing test in a way that needs
  changing what it ASSERTS (not just how it decodes) — report it; the bools must
  still mean the same thing.
- You find yourself building roles/permissions or a user-management UI — out of
  scope (that's 010); stop and report.

## Maintenance notes

- **This establishes identity; 010 (roles) builds on it.** The session now carries
  `username` — 010 will add a role claim keyed on it, and D4 (audit log) will
  attribute actions to it.
- **Timing/enumeration**: `Login` returns generic errors and the rate limiter
  caps attempts, but it does not do a constant-time compare on unknown usernames
  (it short-circuits). That matches the prior behavior and is acceptable behind
  the rate limiter; a constant-time dummy-compare is a minor deferred hardening.
- **Format**: comma-separated because bcrypt hashes never contain `,` or `:`. If a
  future auth backend needs richer config (roles per user), that likely graduates
  to a config file — but keep the env format working for backward compatibility.
- **Reviewer**: confirm a single-entry `AUTH_USER_PASS` still logs in (backward
  compat) and that the username never leaks a hash to the browser (GetStatus
  returns only the username string).
