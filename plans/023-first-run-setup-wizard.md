# Plan 023: First-run setup wizard (`/setup`)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/023-setup-wizard main` then
> `git log --oneline -1`.
> SENTINEL (**plan 022 must already be merged**):
> `test -f backend/store/users.go && grep -q "needsSetup" backend/router/auth.go && echo BASE_OK`
> MUST print `BASE_OK`. If it does not, plan 022 has not landed — STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED (creates the first administrator — the endpoint must be unreachable once any user exists)
- **Depends on**: **022** (user store + `needsSetup`). Blocks 026 (docs/screenshots).
- **Category**: feature / security
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

After plan 022 authentication is mandatory and users live in SQLite. A brand-new
deployment therefore starts with **zero users and no way to log in**. This plan
adds the first-run wizard that creates the initial administrator entirely in the
browser — no terminal, no `htpasswd`, no environment variables, no restart.

**The security property that matters:** `POST /setup` creates an unauthenticated
administrator, so it must be **impossible to call once any user exists**. That
check has to be atomic (see Step 2) — a naive "count then insert" is a race that
would let two concurrent requests both create admins.

## Current state (after plan 022)

### `backend/middleware/auth.go` — public-path allowlist (already present)

Plan 022 added an `isPublicPath(r)` allowlist that permits unauthenticated access
to `GET /auth/status`, `GET /setup/status` and `POST /setup`. **The routes for the
latter two do not exist yet — this plan adds them.** Do not widen the allowlist.

### `backend/router/router.go` — route registration

```go
	auth := &Auth{}
	mux.HandleFunc("POST /auth/login", auth.Login)      // outer mux: skips AuthMiddleware

	router := http.NewServeMux()
	router.HandleFunc("POST /auth/logout", auth.Logout)
	router.HandleFunc("GET /auth/status", auth.GetStatus)
	// ... other routes ...
	router.HandleFunc("/", ProxyHandler)
	mux.Handle("/", middleware.AuditLog(middleware.AuthMiddleware(router)))
```
Register the setup routes on the **inner `router`** (so they pass through
`AuditLog` and the allowlist) — *not* on the outer mux.

### `backend/store` (from plan 022)

`store.Default()`, `CountUsers(ctx)`, `CreateUser(ctx, username, password, role)`,
`ValidateUsername`, `ValidatePassword`, `RoleAdmin`, `ErrUsernameTaken`,
`ErrWeakPassword`, `ErrInvalidUsername`.

### `backend/router/auth.go` — rate limiter to reuse

`loginAttempts` (`newLoginLimiter(10, time.Minute)`) and `clientIP(r)` already
exist in this package. Reuse them; do not add a second limiter type.

### Frontend routing — `src/app/router.tsx`

```tsx
const LoginPage = lazy(() => import("@/pages/auth/login"));
// ...
    { path: "/auth", Component: AuthLayout, children: [ { path: "login", Component: LoginPage } ] },
    { path: "/", Component: MainLayout, children: [ /* app pages */ ] },
```

### Frontend gating — `src/components/layouts/main-layout.tsx` / `auth-layout.tsx`

```tsx
const MainLayout = () => {
  const auth = useAuth();
  if (auth.isLoading) return null;
  if (!auth.isAuthenticated) return <Navigate to="/auth/login" />;
  // ...
};

const AuthLayout = () => {
  const auth = useAuth();
  if (auth.isLoading) return null;
  if (auth.isAuthenticated) return <Navigate to="/" replace />;
  return (<div className="min-h-svh flex items-center justify-center"><Outlet /></div>);
};
```
`useAuth()` already returns `needsSetup` (plan 022).

### Login form exemplar — `src/pages/auth/login.tsx` + `schema.ts` + `hooks.ts`

react-hook-form + `zodResolver`, `<InputField form={...} name=... title=... />`
inside a daisyUI `<Card>`, mutation via `api.post("/auth/login", { body })`,
`onSuccess` → `queryClient.invalidateQueries({ queryKey: ["auth"] })`,
`onError` → `toast.error(...)`. **Match this structure exactly.**

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Backend gates | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` | exit 0 |
| Frontend gates | `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/router/setup.go` (create), `backend/router/setup_test.go` (create)
- `backend/router/router.go` (register two routes)
- `backend/store/users.go` (add `CreateFirstAdmin` — atomic; see Step 2)
- `backend/store/users_test.go` (test it)
- `src/pages/setup/page.tsx`, `src/pages/setup/schema.ts`, `src/pages/setup/hooks.ts` (create)
- `src/app/router.tsx` (add `/setup`)
- `src/components/layouts/main-layout.tsx`, `src/components/layouts/auth-layout.tsx` (redirect when `needsSetup`)

- `.github/workflows/ci.yml` (Go version bump — see Step 0)

**Out of scope**: admin user CRUD (plan 025), password change/CSRF (plan 024),
README/docs/screenshots (plan 026), widening `isPublicPath`, the login flow itself.

## Step 0 — Carry-over fix from plan 022 (do this first)

Plan 022 added `modernc.org/sqlite`, whose own `go.mod` declares `go 1.25.0`.
That raised this module's `go` directive from `1.23.0` to **`1.25.0`** (the
Dockerfile builder was bumped to `golang:1.25-alpine` in 022). But
`.github/workflows/ci.yml` still pins `go-version: "1.24"` in **two** jobs
(`backend` and `security`).

`actions/setup-go` leaves `GOTOOLCHAIN` at its default, so CI currently still
passes by *downloading* Go 1.25 on every run — correct, but slower and a latent
break the moment toolchain auto-download is disabled.

Change both occurrences to `go-version: "1.25"`.

**Verify**: `grep -c '"1.24"' .github/workflows/ci.yml` → `0`;
`grep -c '"1.25"' .github/workflows/ci.yml` → `2`.

## Steps

### Step 1 — `store.CreateFirstAdmin` (atomic guard)

In `backend/store/users.go` add:

```go
// ErrSetupAlreadyDone is returned when the instance already has at least one
// user. It is the guard that makes the unauthenticated /setup endpoint safe.
var ErrSetupAlreadyDone = errors.New("setup has already been completed")

// CreateFirstAdmin creates the initial administrator, but only while the users
// table is empty. The count and the insert run inside one IMMEDIATE transaction
// so two concurrent setup requests cannot both succeed.
func CreateFirstAdmin(ctx context.Context, s *Store, username, password string) (*User, error)
```

Implementation notes:
- Validate username/password **before** opening the transaction.
- `BEGIN IMMEDIATE` (with `modernc.org/sqlite` use `s.db.BeginTx` and issue the
  count + insert on that `*sql.Tx`; the store's `SetMaxOpenConns(1)` plus the
  transaction makes this serialised).
- Inside the tx: `SELECT COUNT(*) FROM users`; if `> 0` → return `ErrSetupAlreadyDone`.
- Insert with `role = RoleAdmin`, `disabled = 0`, hash via the same bcrypt path as
  `CreateUser` (cost 10). Commit, then return the created user.

### Step 2 — `backend/router/setup.go`

```go
type Setup struct{}
```

**`GET /setup/status`** → `utils.ResponseSuccess(w, map[string]any{"needsSetup": n == 0})`
where `n, err := store.Default().CountUsers(r.Context())`. On error →
`utils.ResponseError(w, fmt.Errorf("cannot read users: %w", err))` then `return`.

**`POST /setup`**:
1. Rate-limit with the existing limiter: `if !loginAttempts.allow(clientIP(r), time.Now()) { 429; return }`.
2. Decode `{username, password, confirmPassword string}`.
3. If `password != confirmPassword` → 400 `"passwords do not match"`.
4. `store.CreateFirstAdmin(...)`:
   - `ErrSetupAlreadyDone` → **409 Conflict**, message
     `"setup has already been completed"`.
   - `ErrWeakPassword` / `ErrInvalidUsername` / `ErrUsernameTaken` → **400** with
     the error's message.
   - other → 500.
5. On success **log in the new administrator** so the wizard flows straight into
   the app: `utils.Session.Renew(r)`, then set `authenticated`/`username`/`role`
   exactly as `Auth.Login` does.
6. `utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": u.Username, "role": u.Role})`.

**Never log the password or the hash.**

### Step 3 — Register the routes

In `backend/router/router.go`, on the **inner** `router`:
```go
	setup := &Setup{}
	router.HandleFunc("GET /setup/status", setup.GetStatus)
	router.HandleFunc("POST /setup", setup.Create)
```
**Verify**: `grep -c "setup" backend/router/router.go` ≥ 2; `go build ./...` exit 0.

### Step 4 — Frontend: schema + hook

`src/pages/setup/schema.ts`:
```ts
import { z } from "zod";

export const setupSchema = z
  .object({
    username: z.string().min(1, "Username is required"),
    password: z.string().min(10, "Password must be at least 10 characters"),
    confirmPassword: z.string().min(1, "Please confirm your password"),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });
```

`src/pages/setup/hooks.ts` — mirror `src/pages/auth/hooks.ts`:
`useSetup()` → `api.post("/setup", { body })`, `onSuccess` invalidates
`["auth"]`, `onError` → `toast.error(...)`.

### Step 5 — Frontend: the wizard page

`src/pages/setup/page.tsx`, default export, same `<Card>` + `InputField`
structure as `login.tsx`:
- Title "Welcome" / subtitle "Create the administrator account for this instance."
- Fields: **Username**, **Password** (`type="password"`), **Confirm password**
  (`type="password"`).
- Submit button "Create administrator", `loading={setup.isPending}`.
- A short note: "This is a one-time setup. You can add more users later from
  Settings." Do **not** mention `AUTH_USER_PASS` or `htpasswd`.

### Step 6 — Route + gating

`src/app/router.tsx`: `const SetupPage = lazy(() => import("@/pages/setup/page"));`
and a **top-level** route (not under `MainLayout`):
```tsx
    { path: "/setup", Component: SetupLayout, children: [{ index: true, Component: SetupPage }] },
```
Reuse `AuthLayout`'s centred shell rather than inventing a new one — simplest is
to add the `/setup` route as a child of the existing `/auth`-style layout by
extracting the centring wrapper, **or** render the page standalone with the same
`min-h-svh flex items-center justify-center` wrapper. Pick one and keep it simple.

Gating (both layouts):
- `MainLayout`: `if (auth.needsSetup) return <Navigate to="/setup" replace />;`
  **before** the `!isAuthenticated` check.
- `AuthLayout`: same redirect before its authenticated check (so `/auth/login`
  bounces to `/setup` on a fresh instance).
- On the setup page itself: if `!auth.needsSetup`, `<Navigate to="/" replace />`
  so a completed instance cannot re-open the wizard.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` exit 0.

### Step 7 — Tests

`backend/store/users_test.go` (add):
- `CreateFirstAdmin` on an empty store → user with role `admin`, password verifies.
- Second call → `ErrSetupAlreadyDone`, and `CountUsers` stays 1.
- Weak password / invalid username → validation errors, `CountUsers` stays 0.

`backend/router/setup_test.go` (serve through `sessMgr.LoadAndSave(...)`):
- `GET /setup/status` with 0 users → `{"needsSetup":true}`; with 1 user → `false`.
- `POST /setup` on empty instance → 200, response contains the username,
  **no password/hash anywhere in the body**, and the session is authenticated
  (a following `GET /auth/status` on the same cookie jar reports `authenticated:true`).
- `POST /setup` when a user exists → **409**.
- Mismatched confirmPassword → 400; short password → 400.

`backend/middleware/auth_test.go` (add): unauthenticated `GET /setup/status` and
`POST /setup` are **not** 401 (allowlisted), while `GET /buckets` still is.

**Verify**: `cd backend && go test -race ./...` all `ok`.

### Step 8 — Full gate sweep + commit

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
Commit on `advisor/023-setup-wizard`: `feat: first-run setup wizard`

## Test plan

- Unit/handler tests above are the contract (especially the **409 after setup**
  and the **atomic** first-admin guard).
- **Reviewer live verification**: start with an empty DB and no env vars → the UI
  redirects to `/setup`; create an admin → land in the dashboard already logged
  in; log out and back in with those credentials; reload `/setup` → redirected
  away; `curl -X POST /api/setup` again → **409**.

## Done criteria

- [ ] Backend gates exit 0; `go test -race ./...` all `ok`
- [ ] Frontend `typecheck` + `test` + `build` exit 0
- [ ] `grep -n "ErrSetupAlreadyDone" backend/store/users.go backend/router/setup.go` → present in both
- [ ] `grep -rn "409\|StatusConflict" backend/router/setup.go` → present
- [ ] `grep -rn "htpasswd\|AUTH_USER_PASS" src/pages/setup/` → **nothing**
- [ ] `git diff --name-only <022-merge-sha>..HEAD` shows only in-scope files

## STOP conditions

- SENTINEL fails (plan 022 not merged).
- You need to widen `isPublicPath` beyond the three allowlisted endpoints — STOP.
- `CreateFirstAdmin` cannot be made atomic with the current store — report rather
  than shipping a count-then-insert race.
- You find yourself building admin user CRUD or a settings page — that is plan 025.

## Maintenance notes

- **`/setup` is the only unauthenticated write endpoint in the app.** Its entire
  safety rests on `ErrSetupAlreadyDone` inside a transaction. Any refactor of the
  store must preserve that atomicity, and the 409 test must keep passing.
- Auto-login after setup is deliberate (Gitea/Grafana behaviour). It reuses the
  same session code path as `Auth.Login`, including `Session.Renew` — keep them
  in sync if either changes.
- The wizard is reachable only while `CountUsers == 0`; there is intentionally no
  way to re-open it. Recovery from a lost admin password is an operator task
  (delete the DB, or a future CLI flag) — document that in plan 026, not here.
