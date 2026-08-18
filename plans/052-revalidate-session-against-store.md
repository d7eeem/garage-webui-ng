# Plan 052: Revalidate every authenticated request against the user store

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**:
> ```
> git diff --stat 947879d..HEAD -- backend/middleware/auth.go backend/store/users.go backend/router/auth.go docs/authentication.md
> ```
> If any in-scope file changed, compare the "Current state" excerpts against the
> live code before proceeding; on a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — puts a database read on every authenticated request
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `947879d`, 2026-08-13

## Why this matters

Authorization is a snapshot taken at login and never re-checked. `AuthMiddleware`
reads `authenticated` and `role` from the session cookie and nothing ever
consults the user table again, so **disabling, demoting or deleting an account
has no effect on that person's open session** — they keep whatever privileges
they had for up to the session lifetime (24h absolute, 2h idle).

For a demoted admin that means continued access to `/admin/users` (where they can
create a new admin or reset anyone's password) and to every write through the
catch-all proxy, which attaches a **cluster-wide Garage admin token**.

Worse, this is the documented incident response. `docs/authentication.md:346`
tells operators to *"disable the account if those must die immediately"*. That
instruction does not work. An operator following it believes they have contained
a compromise and has not. Either the doc or the code is wrong; this plan makes
the code match the doc.

## Current state

### `backend/middleware/auth.go` — the whole of `AuthMiddleware`

```go
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Comma-ok, not a bare type assertion: a session value of an
		// unexpected type must fail closed, not panic.
		if authenticated, _ := utils.Session.Get(r, "authenticated").(bool); !authenticated {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		if role, _ := utils.Session.Get(r, "role").(string); role == "viewer" && !isViewerAllowed(r) {
			utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
```

### What the session carries

`backend/router/auth.go:157-159` and `backend/router/setup.go:113-115` both set
exactly three values:

```go
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", user.Username)
	utils.Session.Set(r, "role", user.Role)
```

So `username` is already present on every authenticated session — this plan does
not need a new session field.

### The lookup you will use — `backend/store/users.go:167`

```go
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ?`, strings.TrimSpace(username))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	…
}
```

> **Critical**: a missing user is `(nil, nil)` — **not** an error. A `nil` user
> with a `nil` error must be treated as "deny", or a deleted account keeps
> access. This is the single most likely way to get this plan wrong.

`backend/store/users.go:19-28`:

```go
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	…
}
```

### The exemplar to copy — `backend/router/auth.go:203-213`

`ChangePassword` is the **only** handler that already does this, and it is the
pattern to match: load the user by the session's username, and refuse when the
user is missing or disabled. Read those lines before writing Step 1.

### Conventions

- The store singleton is `store.Default()` and **may be nil** while startup is
  incomplete; handlers treat that as `store.ErrNoStore` ⇒ 500. Match that.
- Middleware responds with `utils.ResponseErrorStatus(w, errors.New("..."), http.Status...)` and **returns**.
- Go tests are table-driven `testing` + `httptest`. Session-touching code must be
  exercised **through** `sessMgr.LoadAndSave(...)` or `utils.Session.Get` panics —
  see `backend/router/admin_users_test.go:50-60` for the established shape.

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

The frontend is untouched.

## Scope

**In scope:**
- `backend/middleware/auth.go` — the revalidation
- `backend/middleware/auth_test.go` — extend
- `docs/authentication.md` — correct the section that describes revocation
- `backend/router/admin_users_test.go` — **one additive line only** (see the
  amendment below): seed `bob` as a viewer in
  `TestAdminUsersAuthorizationEndToEnd`, mirroring the existing `alice` seed.

> **Amendment (2026-08-17, after first execution attempt).** The original scope
> locked out all of `backend/router/`, but `TestAdminUsersAuthorizationEndToEnd`
> drives the real router through `AuthMiddleware` with a viewer session for
> username `bob` **without a `bob` row in the store**. Under this plan's own
> Step 1.3 a phantom-username session is correctly 401, so five subtests
> (`viewer_*`) fail expecting 403. The test's intent is "a signed-in **viewer**
> gets 403", so the fix is to make bob a real viewer:
> `seedUser(t, st, "bob", "bob-s3cret-password", store.RoleViewer)` immediately
> after the `alice` seed near line 198. Do not touch
> `TestAdminUsersRequireAdminRole` (it calls handlers directly, no middleware)
> and do not change any expected status code.

**Out of scope — do NOT touch:**
- `isViewerAllowed` and `isPublicPath` — the allowlists themselves are settled;
  you are changing **where the role comes from**, not what each role may do.
- `backend/router/auth.go`, `setup.go`, `admin_users.go` — the session values
  they set are already correct and sufficient.
- `backend/utils/session.go` — timeouts and storage stay as they are.
- `backend/store/` — no schema or query changes. `GetUserByUsername` already
  does what you need.
- Any new environment variable.

## Git workflow

- Branch: `advisor/052-revalidate-session-against-store` from your given base.
- Conventional commit, e.g. `fix(security): revalidate session role and status per request`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Resolve the caller from the store on every authenticated request

In `backend/middleware/auth.go`, after the `authenticated` check and **before**
the viewer gate, add a resolution step:

1. Read `username` from the session (comma-ok, like the existing reads).
   An empty username ⇒ 401.
2. `st := store.Default()`; if `st == nil` ⇒ **500**, not 401 — startup is
   incomplete, and calling that "unauthorized" would be misleading.
3. `user, err := st.GetUserByUsername(r.Context(), username)`
   - `err != nil` ⇒ **500** (fail closed; do not fall back to the session copy)
   - `user == nil` ⇒ **401** (deleted account)
   - `user.Disabled` ⇒ **401** (disabled account)
4. Use `user.Role` — **not** the session's `role` — for the viewer gate.

Write a comment at that block explaining *why* it exists: the session is a
snapshot, and disable/demote/delete must take effect on the next request.

> **Do not** write the freshly-read role back into the session. It looks tidy and
> it is a trap: scs writes a `Set` back to the session store, so every request
> would mutate session state and the value you are trying not to trust becomes
> load-bearing again. Read it, use it, discard it.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → no output, exit 0.

### Step 2: Keep the per-request cost off the single-connection pool

The SQLite pool is deliberately capped at one connection, so an uncached lookup
serialises every authenticated request behind one query.

Add a small in-memory cache **inside `backend/middleware/auth.go`**:

- `map[string]cachedUser` guarded by a `sync.RWMutex`, where `cachedUser` holds
  the role, the disabled flag, and an expiry timestamp.
- TTL: **5 seconds**, as a named constant with a comment stating the trade
  explicitly — revocation takes effect within 5s rather than instantly, which is
  still four orders of magnitude better than 24 hours.
- On a cache miss or expiry, do the store lookup and populate.
- **Cache negative results too** (user missing or disabled), or a deleted account
  becomes a database query per request — the exact hot path an attacker controls.
- Evict expired entries opportunistically when the map exceeds a small bound
  (e.g. 1024 entries) so it cannot grow without limit.

> **Do not** use `utils.Cache` for this. That cache is global, has no sweeper,
> and is already flagged elsewhere for unbounded growth; a bounded local map
> keeps this middleware's behaviour self-contained and testable.

**Verify**: `cd backend && go build ./... && go test -race ./middleware/` → `ok`.

### Step 3: Tests

Extend `backend/middleware/auth_test.go`. Serve through `sessMgr.LoadAndSave(...)`
and drive a real `store` backed by a temp-file database (see
`backend/store/store_test.go` for how a test store is opened).

1. **A disabled user is rejected on the next request.** Authenticate, then set
   `disabled = 1` in the store, then issue a request → **401**. This is the
   regression guard for the whole plan.
2. **A demoted admin loses admin access.** Session says `role: admin`; the row
   says `viewer`. A request to a path a viewer may not reach → **403**.
   Assert the *store* wins over the session.
3. **A deleted user is rejected** — row removed, `GetUserByUsername` returns
   `(nil, nil)` → **401**, not a 500 and not a pass-through.
4. **A promoted viewer gains access** — session says `viewer`, row says `admin`;
   a write path → allowed. Proves the role is read fresh in both directions.
5. **`store.Default() == nil` ⇒ 500**, not 401.
6. **Public paths still bypass everything** — no store lookup happens for
   `GET /auth/status` (assert by pointing at a nil store and expecting success).
7. **The cache expires**: with the TTL elapsed, a status change is observed.
   Inject time rather than sleeping — make the TTL comparison take a `now`
   parameter, or expose the clock as a package var the test can override.

**Verify**: `cd backend && go test -race ./middleware/ -v -run TestAuth` → all `PASS`.

### Step 4: Correct the documentation

`docs/authentication.md` currently states, around line 346, that disabling an
account is how to kill existing sessions. After this plan that becomes true, but
with a bounded delay. Update that passage to say revocation takes effect **within
the cache TTL (5 seconds)**, not instantly, and that it applies to disable,
delete and role change alike.

Check the surrounding section for any other sentence claiming sessions are
unaffected by account changes, and correct those too.

**Verify**: `grep -n "disable the account\|within 5 seconds\|takes effect" docs/authentication.md` → the passage reflects the new behaviour.

### Step 5: Commit, then prove the tests can fail

**Commit your work first.** Then back up `backend/middleware/auth.go` outside the
repo (`cp … /tmp/`), and apply one mutation at a time, restoring immediately
after each:

1. Use the session's `role` instead of `user.Role` → test 2 **must fail**.
2. Treat `user == nil` as "allow" (skip the nil check) → test 3 **must fail**.
3. Ignore `user.Disabled` → test 1 **must fail**.

Report all three, then confirm `git status --porcelain` is clean.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

## Done criteria

- [ ] Step 6 passes; all packages `ok`
- [ ] Step 5's three mutations each failed the named test, and were reverted
- [ ] `grep -n 'Session.Get(r, "role")' backend/middleware/auth.go` → **no matches** (the role no longer comes from the session)
- [ ] `grep -c "utils.Cache" backend/middleware/auth.go` → **0** (the local cache is used, not the global one)
- [ ] `git diff 947879d..HEAD -- backend/router/ ':!backend/router/admin_users_test.go' -- backend/store/ backend/utils/session.go src/` is **empty**
- [ ] `git diff 947879d..HEAD -- backend/router/admin_users_test.go` shows **exactly one added line** (the `bob` seed) and zero removed lines
- [ ] `git diff --stat 947879d..HEAD` lists only the four in-scope files

## STOP conditions

- The `AuthMiddleware` excerpt does not match — the branch drifted.
- You are about to write the resolved role back into the session.
- You are about to change `isViewerAllowed` or `isPublicPath`.
- You are about to make a store error fail **open** (pass the request through).
- You conclude the lookup must happen in every handler rather than the
  middleware — it must not; the middleware is the single chokepoint.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The 5-second TTL is the whole trade.** It exists because the SQLite pool is
  capped at one connection; without a cache every authenticated request
  serialises behind one query. If that cap ever changes, revisit whether the
  cache is still needed.
- **Negative results must stay cached.** Otherwise a deleted account's requests
  become an uncached query each — an attacker-controlled hot path.
- **`GetUserByUsername` returns `(nil, nil)` for "not found".** Any future
  caller in this file must treat that as deny, not as "no error, carry on".
- A reviewer should check exactly one thing first: that the viewer gate reads
  `user.Role` and not the session copy. Everything else in this plan is in
  service of that line.
