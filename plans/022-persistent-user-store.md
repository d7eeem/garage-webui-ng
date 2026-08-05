# Plan 022: Persistent user store (SQLite) + AUTH_USER_PASS migration + auth rewire

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it).
>
> **Base reset FIRST**: `git checkout -B advisor/022-persistent-user-store main`
> then `git log --oneline -1` — MUST show `b6101af` or newer, NOT `ee420fb`.
> SENTINEL: `grep -q "garage-webui-ng" backend/go.mod && test -f backend/middleware/audit.go && echo BASE_OK`
> MUST print `BASE_OK`. If not, STOP.

## Status

- **Priority**: P1 (architecture)
- **Effort**: L
- **Risk**: **HIGH** — makes a stateless service stateful and makes auth mandatory
- **Depends on**: nothing. **Blocks 023, 024, 025, 026.**
- **Category**: architecture / security
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

Today authentication lives in **environment variables** (`AUTH_USER_PASS`,
`AUTH_VIEWER_USER_PASS`): every user change means editing Docker/compose and
restarting. Users are *application data*, not deployment configuration. This plan
moves them into a persistent **SQLite** database so the app can manage its own
users, matching how Gitea / Grafana / MinIO Console behave.

**Two decisions were made by the maintainer and are binding for this plan set:**

1. **Authentication becomes mandatory.** The current "no `AUTH_USER_PASS` ⇒ the
   whole API is open" mode is **removed**. This is a **BREAKING CHANGE** (see
   *Breaking changes* below) and must be documented loudly (plan 026).
2. **The database is authoritative** once populated; `AUTH_USER_PASS` is imported
   exactly once and ignored thereafter.

### The two hard constraints (already verified — do not re-litigate)

- **SQLite driver MUST be `modernc.org/sqlite` (pure Go).** The release build is
  `CGO_ENABLED=0` (`backend/Makefile`, `Dockerfile`) on a `distroless/static`
  base. `github.com/mattn/go-sqlite3` requires CGO and **will break the build**.
  `modernc.org/sqlite` v1.55.0 was verified to compile under `CGO_ENABLED=0` and
  execute SQL. If you find yourself adding `mattn/go-sqlite3`, STOP.
- **The runtime image runs as non-root (uid/gid 65532).** A named volume mounted
  at `/data` is root-owned unless the image already contains `/data` owned by
  65532. The Dockerfile must create it with that ownership (Step 7), or the app
  cannot write its database.

## Current state (read these before editing)

### `backend/router/auth.go` — env-based login (to be rewired)

```go
func parseUserPass(raw string) map[string]string { /* "user:hash,user2:hash2" → map */ }

func (c *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !loginAttempts.allow(clientIP(r), time.Now()) { /* 429 */ }
	var body struct{ Username, Password string }
	// ...
	admins := parseUserPass(utils.GetEnv("AUTH_USER_PASS", ""))
	viewers := parseUserPass(utils.GetEnv("AUTH_VIEWER_USER_PASS", ""))
	if len(admins) == 0 && len(viewers) == 0 { /* 500 "AUTH_USER_PASS not set" */ }
	username := strings.TrimSpace(body.Username)
	role := ""
	if h, ok := admins[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "admin"
	} else if h, ok := viewers[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "viewer"
	}
	if role == "" { /* 401 */ }
	if err := utils.Session.Renew(r); err != nil { /* 500 */ }
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", username)
	utils.Session.Set(r, "role", role)
	utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": username, "role": role})
}

func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != "" || utils.GetEnv("AUTH_VIEWER_USER_PASS", "") != ""
	isAuthenticated := !enabled          // ← open-mode pass-through, being REMOVED
	// ... reads "authenticated"/"username"/"role" from the session
	utils.ResponseSuccess(w, map[string]any{"enabled": ..., "authenticated": ..., "username": ..., "role": ...})
}
```

`loginLimiter` (10 attempts / minute / IP), `clientIP`, `Logout` stay as they are.

### `backend/middleware/auth.go` — the single authorization chokepoint

```go
func AuthMiddleware(next http.Handler) http.Handler {
	authData := utils.GetEnv("AUTH_USER_PASS", "") + utils.GetEnv("AUTH_VIEWER_USER_PASS", "")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := utils.Session.Get(r, "authenticated")
		if authData == "" {            // ← open-mode bypass, being REMOVED
			next.ServeHTTP(w, r); return
		}
		if auth == nil || !auth.(bool) { /* 401 */ }
		if role, _ := utils.Session.Get(r, "role").(string); role == "viewer" && !isViewerAllowed(r) { /* 403 */ }
		next.ServeHTTP(w, r)
	})
}
```

`isViewerAllowed` is the read-only-role boundary — **keep it exactly as is**.

### `backend/utils/session.go` — scs session (in-memory store)

`utils.Session.Get/Set/Clear/Renew(r, ...)`. `InitSessionManager()` sets
`Lifetime = 24h`, `HttpOnly`, `SameSite=Lax`, `Secure` from `SESSION_COOKIE_SECURE`.
**Store is scs's default in-memory store** — sessions do not survive a restart.
That is existing behaviour; persisting sessions is out of scope here.

### `backend/utils/utils.go` — response + env helpers (conventions)

```go
func GetEnv(key, defaultValue string) string
func ResponseError(w http.ResponseWriter, err error)                       // 500
func ResponseErrorStatus(w http.ResponseWriter, err error, status int)
func ResponseSuccess(w http.ResponseWriter, data interface{})
```
Handlers are methods on empty structs, take `(w, r)`, and **`return` after
`ResponseError*`** (it does not stop the handler). Wrap errors with
`fmt.Errorf("...: %w", err)`.

### `backend/main.go` — startup order (where bootstrap hooks in)

```go
godotenv.Load()
utils.InitCacheManager()
sessionMgr := utils.InitSessionManager()
if err := utils.Garage.LoadConfig(); err != nil { log.Println("Cannot load garage config!", err) }
if utils.GetEnv("AUTH_USER_PASS", "") == "" { log.Println("WARNING: ... accessible without authentication ...") }
// ... mux, ui.ServeUI(mux), ListenAndServe with graceful shutdown
```

### `.gitignore` already ignores `data/`

So a default DB path under `./data/` will not be committed.

### Go test convention (`backend/router/auth_test.go`)

Session-touching handlers must be served through the scs middleware or
`Session.Get` panics:

```go
sessMgr := utils.InitSessionManager()
handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).GetStatus))
req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
w := httptest.NewRecorder()
handler.ServeHTTP(w, req)
```
Use `httptest` + `t.Setenv`. Tests live beside the code they cover.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Build/vet/fmt | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)"` | exit 0 |
| Tests | `cd backend && go test -race ./...` | `ok` |
| **CGO-free build (critical)** | `cd backend && CGO_ENABLED=0 go build ./...` | exit 0 |
| Frontend | `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/go.mod`, `backend/go.sum` (add `modernc.org/sqlite`)
- `backend/store/store.go`, `backend/store/users.go`, `backend/store/bootstrap.go` (create)
- `backend/store/store_test.go`, `backend/store/users_test.go`, `backend/store/bootstrap_test.go` (create)
- `backend/router/auth.go` (login/status against the store)
- `backend/router/auth_test.go` (update)
- `backend/middleware/auth.go` (remove the open-mode bypass)
- `backend/middleware/auth_test.go` (update)
- `backend/main.go` (open DB, run bootstrap, fail fast)
- `Dockerfile` (`/data` owned by 65532, `ENV DB_PATH`, `VOLUME`)
- `docker-compose.yml` (named volume for the webui DB)
- `.env.example` (document `DB_PATH`; mark `AUTH_USER_PASS` as import-only)
- `src/hooks/useAuth.ts` (surface `needsSetup`, drop the "auth off ⇒ admin" branch)

**Out of scope** — do NOT touch:
- `isViewerAllowed` in `backend/middleware/auth.go` (the role boundary is correct).
- The `/setup` wizard endpoints and UI — **that is plan 023**. This plan only
  reports `needsSetup` in `/auth/status`.
- Admin user-management endpoints/UI (plan 025), CSRF/password-change (plan 024),
  README/CLAUDE.md/docs rewrite (plan 026).
- The audit middleware, proxy, browse/S3 code, `plans/`.

## Steps

### Step 1 — Add the pure-Go SQLite driver

```
cd backend && go get modernc.org/sqlite@v1.55.0 && go mod tidy
```
**Verify**: `grep "modernc.org/sqlite" backend/go.mod` matches, and
`cd backend && CGO_ENABLED=0 go build ./...` exits 0.

### Step 2 — `backend/store/store.go`: connection + schema migrations

Create package `store`. Requirements:

- `import _ "modernc.org/sqlite"`; open with `sql.Open("sqlite", dsn)` (driver
  name is **`sqlite`**, not `sqlite3`).
- `func DBPath() string` → `utils.GetEnv("DB_PATH", "./data/garage-webui-ng.db")`.
- `func Open(path string) (*Store, error)`:
  - `os.MkdirAll(filepath.Dir(path), 0o750)` first; wrap failures with context
    (`fmt.Errorf("cannot create data directory %q: %w", dir, err)`).
  - DSN should enable foreign keys and WAL, e.g.
    `file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)`.
  - `db.SetMaxOpenConns(1)` — SQLite + a single writer avoids `database is locked`
    under concurrent handlers. Document why in a comment.
  - Ping the DB, then run `migrate()`.
- `migrate()` — a tiny explicit migrator, not a library:
  - `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`.
  - Ordered `[]string` of migration statements; apply any whose index+1 > current
    version, inside a transaction, recording the version.
  - **Migration 1**:
    ```sql
    CREATE TABLE users (
      id            INTEGER PRIMARY KEY AUTOINCREMENT,
      username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
      password_hash TEXT    NOT NULL,
      role          TEXT    NOT NULL DEFAULT 'admin',
      disabled      INTEGER NOT NULL DEFAULT 0,
      created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
      last_login    TIMESTAMP
    );
    ```
  - `COLLATE NOCASE` makes usernames case-insensitive-unique (so `Admin` and
    `admin` cannot both exist). Document that in a comment.
- `func (s *Store) Close() error`.

**Verify**: `cd backend && go build ./... && go vet ./...` exit 0.

### Step 3 — `backend/store/users.go`: model + CRUD

```go
// User is an application user. PasswordHash is never serialised: the `json:"-"`
// tag is the single guarantee that a hash cannot leak through any API response.
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLogin    *time.Time `json:"lastLogin"`
}

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)
```

Functions (all take `context.Context` as the first arg):
- `CountUsers(ctx) (int, error)`
- `GetUserByUsername(ctx, username string) (*User, error)` — returns
  `(nil, nil)` when not found (do **not** return `sql.ErrNoRows` to callers).
- `GetUserByID(ctx, id int64) (*User, error)`
- `ListUsers(ctx) ([]User, error)` — ordered by `username`.
- `CreateUser(ctx, username, plaintextPassword, role string) (*User, error)` —
  validates (Step 4), hashes with `bcrypt.GenerateFromPassword(pw, 10)`, inserts.
  On UNIQUE violation return a friendly `ErrUsernameTaken`.
- `SetPassword(ctx, id int64, plaintextPassword string) error` — validates + hashes, bumps `updated_at`.
- `SetDisabled(ctx, id int64, disabled bool) error`
- `SetRole(ctx, id int64, role string) error` — role must be `admin` or `viewer`.
- `Rename(ctx, id int64, username string) error`
- `DeleteUser(ctx, id int64) error`
- `TouchLastLogin(ctx, id int64) error`
- `CountEnabledAdmins(ctx) (int, error)` — used by plan 025's lockout guards; add it now.

Exported sentinel errors: `ErrUsernameTaken`, `ErrUserNotFound`, `ErrInvalidRole`,
`ErrWeakPassword`, `ErrInvalidUsername`.

### Step 4 — Validation rules (in `users.go`)

- `ValidateUsername(s string) error` — trimmed, 1–64 chars, must match
  `^[A-Za-z0-9._@-]+$` (no spaces/colons/commas — keeps parity with the legacy
  `user:hash` format and avoids surprises).
- `ValidatePassword(s string) error` —
  - at least **10** characters (define `MinPasswordLength = 10`),
  - **at most 72 bytes** — bcrypt silently truncates beyond 72 bytes, so a longer
    password would give a false sense of strength; reject it explicitly,
  - not entirely whitespace.
  Return `ErrWeakPassword` wrapped with a human-readable reason.

**Verify**: `cd backend && go build ./...` exit 0.

### Step 5 — `backend/store/bootstrap.go`: one-time AUTH_USER_PASS import

```go
// ImportLegacyUsers seeds the database from AUTH_USER_PASS / AUTH_VIEWER_USER_PASS
// exactly once. It is a no-op when any user already exists, which makes it
// idempotent and makes the database authoritative from then on: the environment
// variables are never consulted again.
func ImportLegacyUsers(ctx context.Context, s *Store, adminsRaw, viewersRaw string) (int, error)
```

Behaviour:
- If `CountUsers > 0` → return `(0, nil)` immediately (**idempotency guarantee**).
- Parse both env vars with the **existing** comma/first-colon format. Move
  `parseUserPass` from `backend/router/auth.go` into `store` (exported as
  `ParseUserPass`) and have the router use the store's copy — do not duplicate it.
- Insert each entry **with the hash as-is** (they are already bcrypt hashes — do
  NOT re-hash), role `admin` / `viewer` respectively. Admins win on conflict.
- Skip malformed entries and entries whose hash does not look like bcrypt
  (`$2a$`/`$2b$`/`$2y$` prefix); `log.Printf` a warning naming the username only —
  **never log the hash**.
- Return the number imported. `main.go` logs, when > 0:
  `log.Printf("Initial administrator imported from AUTH_USER_PASS (%d user(s)).", n)`

### Step 6 — Wire it up: `main.go`, `auth.go`, `middleware/auth.go`

**`backend/main.go`** — after `utils.InitSessionManager()`:
```go
st, err := store.Open(store.DBPath())
if err != nil {
	log.Fatalf("cannot open user database at %s: %v", store.DBPath(), err)   // fail fast
}
defer st.Close()
log.Printf("User database: %s", store.DBPath())
n, err := store.ImportLegacyUsers(context.Background(), st, utils.GetEnv("AUTH_USER_PASS", ""), utils.GetEnv("AUTH_VIEWER_USER_PASS", ""))
if err != nil { log.Printf("legacy user import failed: %v", err) }
if n > 0 { log.Printf("Initial administrator imported from AUTH_USER_PASS (%d user(s)).", n) }
```
- **Delete** the old `AUTH_USER_PASS is not set` warning block; replace with: if
  `CountUsers == 0`, log `"No users configured — open <url>/setup to create the first administrator."`
- Expose the store to handlers. Simplest approach that matches the existing
  empty-struct handler style: a package-level accessor in `store`
  (`store.Default()` set once from `main.go` via `store.SetDefault(st)`), mirroring
  how `utils.Session` and `utils.Garage` are already package-level singletons.
  **Do not** rewrite every handler to take a dependency — match the repo.

**`backend/router/auth.go`**:
- `Login`: keep the rate limiter and `Session.Renew`. Replace the env lookup with
  `store.Default().GetUserByUsername(ctx, username)`.
  - If the user is missing **or** disabled → run a **dummy bcrypt compare**
    against a fixed hash before returning 401, so response timing does not reveal
    whether a username exists. Comment this.
  - On success: `TouchLastLogin`, set session `authenticated`/`username`/`role`
    (use the **stored** username casing, not what the client typed).
  - Remove the `AUTH_USER_PASS not set` 500 branch.
- `GetStatus`: replace `enabled` semantics. New response:
  ```json
  { "enabled": true, "authenticated": false, "username": "", "role": "", "needsSetup": true }
  ```
  - `enabled` is now **always `true`** (auth is mandatory) — keep the field for
    frontend compatibility.
  - `needsSetup` = `CountUsers() == 0`.
  - `authenticated` comes **only** from the session (delete `isAuthenticated := !enabled`).

**`backend/middleware/auth.go`**:
- Delete the `authData` variable and the `if authData == "" { next... }` bypass.
  Every request must now have an authenticated session.
- **Allowlist the unauthenticated endpoints** so login/status/setup still work:
  `GET /auth/status`, and (for plan 023) `GET /setup/status` + `POST /setup`.
  `POST /auth/login` is registered on the outer mux and does not pass through this
  middleware — leave that as is.
  Implement as a small `isPublicPath(r) bool` helper with a comment that it is a
  security boundary: **only** these may be reached unauthenticated.
- Keep the viewer/`isViewerAllowed` check untouched.

**Verify**: `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)"` exit 0.

### Step 7 — Container: writable `/data` for the non-root user

In `Dockerfile`, **backend build stage**, create the directory that will become the
volume mount point so its ownership is baked into the image:
```dockerfile
RUN mkdir -p /data
```
In the **runtime stage**, before `USER nonroot:nonroot`:
```dockerfile
COPY --from=backend --chown=65532:65532 /data /data
ENV DB_PATH=/data/garage-webui-ng.db
VOLUME ["/data"]
```
> Docker seeds a fresh named volume from the image directory, so the volume
> inherits uid/gid 65532 and the non-root process can write. Without this the app
> fails fast with "cannot open user database".

In `docker-compose.yml`, add to the `webui` service:
```yaml
    volumes:
      - ./garage.toml:/etc/garage.toml:ro
      - webui_data:/data
```
and a `webui_data:` entry under the top-level `volumes:` block.

**Verify**: `docker compose config >/dev/null` exits 0. Then a real end-to-end check:
```
docker build -t gwui-ng:022 .
docker run -d --name gwui022 -p 3912:3909 -v gwui022data:/data gwui-ng:022
sleep 3 && docker logs gwui022        # expect "User database: /data/..." and the /setup hint, NO fatal
docker rm -f gwui022 && docker volume rm gwui022data
```
Expect no `cannot open user database` error. If it appears, the ownership trick
is wrong — STOP and report.

### Step 8 — `.env.example`

Rewrite the auth block: `AUTH_USER_PASS` / `AUTH_VIEWER_USER_PASS` are now
**legacy, import-only** (read once on first start into the database, ignored
afterwards); new deployments use the `/setup` wizard. Add `DB_PATH` with its
default and a note that it must live on a **persistent volume**.

### Step 9 — Frontend: `src/hooks/useAuth.ts`

- Add `needsSetup: boolean` to `AuthResponse` and to the returned object.
- **Delete** the open-mode assumption: `canWrite` becomes `role !== "viewer"`
  (auth is always on now). Keep `isEnabled` reading `data?.enabled ?? true`.

Do **not** add the `/setup` route or redirect here — that is plan 023.

**Verify**: `npx pnpm@9 run typecheck` exits 0.

### Step 10 — Tests

`backend/store/store_test.go`:
- `Open` on a `t.TempDir()` path creates the file and applies migrations;
  calling `Open` twice is safe (migrations idempotent, version stays 1).

`backend/store/users_test.go`:
- Create → `GetUserByUsername` round-trip; `PasswordHash` is populated but
  **`json.Marshal(user)` must not contain `password_hash` or the hash value**
  (assert on the marshalled bytes — this is the "never returned by APIs" guarantee).
- Duplicate username (and different casing, e.g. `Admin` vs `admin`) → `ErrUsernameTaken`.
- `ValidatePassword`: 9 chars → error; 10 chars → ok; 73 bytes → error.
- `ValidateUsername`: rejects `a:b`, `a,b`, empty, and 65+ chars.
- `SetPassword` changes the hash and the new password verifies with bcrypt.
- `CountEnabledAdmins` reflects disabling/deleting.

`backend/store/bootstrap_test.go` (the migration contract):
- Empty DB + `AUTH_USER_PASS="admin:$2a$..."` → 1 user, role `admin`,
  **hash stored verbatim** (not re-hashed — verify the original password still
  validates against the stored hash).
- Admin + viewer vars → both imported with correct roles.
- **Idempotency**: running `ImportLegacyUsers` again returns `0` and does not
  modify the existing row (change the user's password first, re-run, assert the
  password did not revert).
- Non-empty DB + env set → returns `0` (DB authoritative).
- Malformed entries (`nocolon`, empty hash, non-bcrypt hash) are skipped.

`backend/router/auth_test.go` (update):
- Login against a temp store: correct password → 200 + session; wrong password →
  401; unknown user → 401; **disabled user → 401**.
- `GetStatus` with zero users → `needsSetup: true`, `authenticated: false`.
- `GetStatus` with users but no session → `needsSetup: false`, `authenticated: false`.
- Delete/replace tests that asserted the old open-mode `enabled:false` semantics.

`backend/middleware/auth_test.go` (update):
- With **no** session, a normal API path (e.g. `GET /buckets`) → **401**
  (previously 200 in open mode — this is the behaviour change).
- `GET /auth/status` → allowed unauthenticated.
- Existing viewer 403 tests still pass.

**Verify**: `cd backend && go test -race ./...` → all `ok`.

### Step 11 — Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && CGO_ENABLED=0 go build ./... && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
docker compose config >/dev/null
```
Commit on `advisor/022-persistent-user-store`:
`feat: persistent SQLite user store with AUTH_USER_PASS migration`

## Test plan

Unit tests above are the contract. **Reviewer live verification** (advisor's job):
1. Fresh DB + `AUTH_USER_PASS=admin:<hash>` → start → log shows
   "Initial administrator imported from AUTH_USER_PASS (1 user(s))."; login works.
2. Restart with the env var **removed** → the imported admin still logs in (DB authoritative).
3. Restart with the env var **changed** → the DB row is unchanged (no re-import).
4. Fresh DB, **no** env var → `/api/auth/status` reports `needsSetup: true`, and an
   unauthenticated `GET /api/buckets` returns **401** (open mode is gone).
5. Container run with a named volume → DB persists across `docker rm` + re-run.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `cd backend && CGO_ENABLED=0 go build ./...` exits 0 (**no CGO dependency**)
- [ ] `grep -c "mattn/go-sqlite3" backend/go.mod` → `0`
- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` exit 0
- [ ] `grep -n "authData" backend/middleware/auth.go` → nothing (open-mode bypass removed)
- [ ] `grep -rn "password_hash" backend/store/users.go` shows the field carries `json:"-"`
- [ ] `docker compose config` exits 0 and `webui_data` volume is present
- [ ] `git diff --name-only b6101af..HEAD` shows only in-scope files

## STOP conditions

- Base reset shows `ee420fb` or SENTINEL missing.
- `CGO_ENABLED=0 go build ./...` fails after adding the driver → wrong driver; STOP.
- The container cannot write `/data` as uid 65532 → STOP and report (do not
  "fix" it by running as root).
- A current-state excerpt does not match the live file → report the drift.
- You are tempted to add setup-wizard endpoints, admin CRUD endpoints, or rewrite
  the README → out of scope (plans 023/025/026); STOP.

## Breaking changes (must be carried into plan 026's docs)

1. **Authentication is now mandatory.** Deployments that ran with no
   `AUTH_USER_PASS` (open mode, relying on network isolation) will now require a
   one-time `/setup` wizard. Decided by the maintainer; no opt-out.
2. **A persistent volume is now required.** Without one, the user database is
   lost whenever the container is recreated. (Legacy deployments that still set
   `AUTH_USER_PASS` self-heal via re-import, which masks the problem — say so
   explicitly in the docs.)
3. `/api/auth/status` gains `needsSetup`; `enabled` is now always `true`.

## Maintenance notes

- **`SetMaxOpenConns(1)`** is deliberate: SQLite allows one writer, and the app's
  load is trivial. Do not "optimise" it away without adding retry-on-locked.
- **Migrations are append-only.** Add a new statement to the end of the slice and
  never edit an applied one; the version counter is the only source of truth.
- **`ImportLegacyUsers` must stay guarded by `CountUsers > 0`.** That single check
  is what makes the DB authoritative and the import idempotent.
- **Never log or serialise a password hash.** `json:"-"` on `PasswordHash` plus
  the marshalling test is the guarantee; keep both.
- Sessions remain in-memory (scs default), so a restart logs everyone out. If that
  becomes a problem, add a persistent scs store — note that `sqlite3store`
  requires CGO, so a `modernc`-compatible store would be needed.
