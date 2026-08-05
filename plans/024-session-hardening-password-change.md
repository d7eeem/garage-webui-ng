# Plan 024: CSRF protection, session expiry, and self-service password change

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/024-session-hardening main` then
> `git log --oneline -1`.
> SENTINEL (**022 and 023 must be merged**):
> `test -f backend/router/setup.go && test -f backend/store/users.go && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (touches the auth middleware chain; a wrong CSRF check breaks every write)
- **Depends on**: **022**, **023**. Blocks 026.
- **Category**: security / feature
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

Users can now be created in-app (022/023) but there is still **no way to change a
password without an administrator**, and the session layer lacks two things a
modern self-hosted service is expected to have: an **idle timeout** and explicit
**CSRF protection**.

**Honest threat assessment (read before implementing):** the app is *already*
substantially CSRF-resistant — session cookies are `SameSite=Lax` (browsers do not
attach them to cross-site POSTs) and the API consumes `application/json`, which a
cross-origin HTML form cannot produce without a CORS preflight that the server
never grants. The token added here is **defence in depth**, not a fix for a live
hole. Implement it cleanly, but do not weaken anything else to accommodate it.

## Current state

### `backend/utils/session.go`

```go
func InitSessionManager() *scs.SessionManager {
	sessMgr := scs.New()
	sessMgr.Lifetime = 24 * time.Hour
	sessMgr.Cookie.HttpOnly = true
	sessMgr.Cookie.SameSite = http.SameSiteLaxMode
	sessMgr.Cookie.Secure = GetEnv("SESSION_COOKIE_SECURE", "false") == "true"
	if basePath := os.Getenv("BASE_PATH"); basePath != "" { sessMgr.Cookie.Path = basePath }
	Session = &SessionManager{mgr: sessMgr}
	return sessMgr
}
```
`SessionManager` exposes `Get/Set/Clear/Renew(r, ...)`. There is **no**
`IdleTimeout` and no CSRF machinery.

### `backend/router/router.go` — middleware chain

```go
	mux.Handle("/", middleware.AuditLog(middleware.AuthMiddleware(router)))
```
`main.go` wraps the whole mux in `sessionMgr.LoadAndSave(mux)`, so anything inside
this chain has the session in context.

### `backend/middleware/auth.go` — the viewer boundary (must be edited carefully)

```go
func isViewerAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet {
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") && r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true
	}
	return r.Method == http.MethodPost && r.URL.Path == "/auth/logout"
}
```
A **read-only viewer must still be able to change their own password**, so this
plan adds exactly one more allowed non-GET path (Step 4). Do not loosen it further.

### `src/lib/api.ts` — the single fetch wrapper (where the CSRF header goes)

```ts
export const API_URL = BASE_PATH + "/api";
// ...
      credentials: "include",
// ...
    if (res.status === 401 && !url.startsWith("/auth")) { /* redirect to login */ }
```
All API traffic goes through this module — add the header in **one** place.

### Tabs component (for the Settings page)

`src/components/containers/tab-view` (`TabView`, `Tab`) is already used by
`src/pages/buckets/manage/page.tsx`:
```tsx
const tabs: Tab[] = [{ name: "overview", title: "Overview", icon: ChartLine, Component: OverviewTab }];
<TabView tabs={tabs} className="bg-base-100 h-14 px-1.5" />
```

### Sidebar nav array — `src/components/containers/sidebar.tsx`

```tsx
const pages = [
  { icon: LayoutDashboard, title: "Dashboard", path: "/", exact: true },
  { icon: HardDrive, title: "Cluster", path: "/cluster" },
  { icon: ArchiveIcon, title: "Buckets", path: "/buckets" },
  { icon: KeySquare, title: "Keys", path: "/keys" },
];
```

## Commands

Same gates as plans 022/023 (backend `build/vet/gofmt/test -race`; frontend
`typecheck`, `test`, `build`).

## Scope

**In scope**:
- `backend/utils/session.go` (idle timeout)
- `backend/middleware/csrf.go` + `backend/middleware/csrf_test.go` (create)
- `backend/middleware/auth.go` (allow password-change for viewers) + its test
- `backend/router/router.go` (wrap with CSRF; register the route)
- `backend/router/auth.go` (`ChangePassword` handler) + `auth_test.go`
- `src/lib/api.ts` (send the CSRF header)
- `src/pages/settings/page.tsx`, `src/pages/settings/account-tab.tsx`,
  `src/pages/settings/schema.ts`, `src/pages/settings/hooks.ts` (create)
- `src/app/router.tsx`, `src/components/containers/sidebar.tsx`

**Out of scope**: admin user CRUD + the Users tab (plan 025 — it will add a tab to
the Settings page created here), docs/screenshots (026), the login/setup flows,
`isViewerAllowed`'s GET rules, remember-me (explicitly deferred).

## Steps

### Step 1 — Session idle timeout

In `InitSessionManager`, after `Lifetime`:
```go
	// Absolute cap stays at Lifetime; IdleTimeout logs out sessions that have
	// been untouched for a while, which is what most self-hosted UIs do.
	sessMgr.IdleTimeout = 2 * time.Hour
```
Make both configurable via env with the existing helper, defaulting to today's
values: `SESSION_LIFETIME_HOURS` (default `24`), `SESSION_IDLE_TIMEOUT_HOURS`
(default `2`). Parse with `strconv.Atoi`; ignore/clamp invalid or `<= 0` values to
the default and `log.Printf` a warning.

**Verify**: `cd backend && go build ./... && go test -race ./utils/...` exit 0.

### Step 2 — `backend/middleware/csrf.go`

Double-submit-cookie CSRF:

```go
const CSRFCookieName = "csrf_token"
const CSRFHeaderName = "X-CSRF-Token"
```

- `func CSRF(next http.Handler) http.Handler`:
  - **Ensure a token exists**: if the request has no `csrf_token` cookie, generate
    32 random bytes (`crypto/rand`), hex-encode, and `http.SetCookie` with
    `HttpOnly: false` (the browser JS must read it), `SameSite=Lax`,
    `Path` = `BASE_PATH` or `/`, `Secure` mirroring `SESSION_COOKIE_SECURE`.
  - **Enforce on state-changing methods only** (`POST`, `PUT`, `PATCH`, `DELETE`):
    compare the `X-CSRF-Token` header with the cookie value using
    `crypto/subtle.ConstantTimeCompare`. Mismatch/absent → **403**
    `"invalid or missing CSRF token"` via `utils.ResponseErrorStatus`.
  - **Exempt** `POST /auth/login` and `POST /setup`: they are pre-session
    endpoints; a browser that has never loaded the app has no token yet. Both are
    already rate-limited. Put the exemption in one clearly-commented helper.
- Keep it dependency-free (stdlib only).

### Step 3 — Wrap the chain

`backend/router/router.go`:
```go
	mux.Handle("/", middleware.AuditLog(middleware.CSRF(middleware.AuthMiddleware(router))))
```
Order matters: `AuditLog` outermost (records the final status, incl. 403s), then
CSRF, then auth. Note the login route lives on the outer mux and is exempt anyway.

**Verify**: `grep -c "CSRF" backend/router/router.go` = 1; `go build ./...` exit 0.

### Step 4 — Allow viewers to change their own password

In `backend/middleware/auth.go`, extend the final line of `isViewerAllowed`:
```go
	// A read-only viewer may still end their session and change their OWN
	// password. Nothing else that mutates state is permitted.
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" || r.URL.Path == "/auth/change-password"
	}
	return false
```
Do not add anything else here.

### Step 5 — `ChangePassword` handler

In `backend/router/auth.go`:

```go
// POST /auth/change-password — a user changing their own password.
func (c *Auth) ChangePassword(w http.ResponseWriter, r *http.Request)
```
1. Read `username` from the session; if empty → 401.
2. Decode `{currentPassword, newPassword, confirmPassword}`.
3. `newPassword != confirmPassword` → 400 `"passwords do not match"`.
4. Load the user via `store.Default().GetUserByUsername`; if nil or disabled → 401.
5. `bcrypt.CompareHashAndPassword(user.PasswordHash, currentPassword)` != nil →
   **401** `"current password is incorrect"`. Rate-limit this with the existing
   `loginAttempts.allow(clientIP(r), time.Now())` guard **before** the compare.
6. Reject when `newPassword == currentPassword` → 400.
7. `store.Default().SetPassword(ctx, user.ID, newPassword)` — surfaces
   `ErrWeakPassword` as **400**.
8. **`utils.Session.Renew(r)`** after a successful change (session-fixation
   hygiene), keeping the session data.
9. `utils.ResponseSuccess(w, true)`.

Register on the inner router: `router.HandleFunc("POST /auth/change-password", auth.ChangePassword)`.
**Never log any password.**

### Step 6 — Frontend: send the CSRF header

In `src/lib/api.ts`, add a tiny reader and attach the header to every request
(harmless on GETs):
```ts
const readCookie = (name: string) =>
  document.cookie.split("; ").find((c) => c.startsWith(name + "="))?.split("=")[1];
```
Merge `{ "X-CSRF-Token": readCookie("csrf_token") ?? "" }` into the existing
headers. Keep `credentials: "include"`. Do not change the 401 redirect behaviour.

### Step 7 — Frontend: Settings page (Account tab)

- `src/pages/settings/schema.ts` — `changePasswordSchema` with `currentPassword`
  (min 1), `newPassword` (min 10), `confirmPassword`, `.refine` for equality
  (`path: ["confirmPassword"]`).
- `src/pages/settings/hooks.ts` — `useChangePassword()` → `api.post("/auth/change-password", { body })`,
  `onSuccess`: `toast.success("Password changed")` + `form.reset()`;
  `onError`: `toast.error(err.message)`.
- `src/pages/settings/account-tab.tsx` — a `<Card>` with the three
  `InputField`s (`type="password"`) and a Save button; shows the current username
  from `useAuth()`.
- `src/pages/settings/page.tsx` — `<Page title="Settings" />` plus a `TabView`
  with a single tab `{ name: "account", title: "Account", icon: UserIcon, Component: AccountTab }`.
  **Structure it so plan 025 can add a "Users" tab by appending one entry.**
- `src/app/router.tsx`: lazy `SettingsPage`, route `{ path: "settings", Component: SettingsPage }`
  under `MainLayout`.
- `src/components/containers/sidebar.tsx`: append
  `{ icon: Settings, title: "Settings", path: "/settings" }` (import `Settings`
  from `lucide-react`).

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` exit 0.

### Step 8 — Tests

`backend/middleware/csrf_test.go`:
- GET with no cookie → passes through **and** a `csrf_token` cookie is set.
- POST with matching cookie+header → passes.
- POST with mismatched header → **403**; POST with missing header → **403**.
- `POST /auth/login` and `POST /setup` with no token → **not** 403 (exempt).

`backend/middleware/auth_test.go` (add): viewer + `POST /auth/change-password` →
allowed by `isViewerAllowed`; viewer + `POST /auth/logout` → allowed; viewer +
`POST /v2/CreateBucket` → still denied.

`backend/router/auth_test.go` (add), served through `sessMgr.LoadAndSave`:
- correct current password → 200 and the **new** password verifies against the
  stored hash while the old one no longer does;
- wrong current password → 401 and the hash is unchanged;
- mismatched confirm → 400; short new password → 400; same-as-current → 400;
- no session → 401.

`backend/utils/session_test.go` (add): `IdleTimeout` is set and env overrides are
honoured (e.g. `SESSION_IDLE_TIMEOUT_HOURS=1`), invalid values fall back to default.

**Verify**: `cd backend && go test -race ./...` all `ok`.

### Step 9 — Gate sweep + commit

Backend + frontend gates. Commit on `advisor/024-session-hardening`:
`feat: CSRF protection, session idle timeout, self-service password change`

## Test plan

- Handler/middleware tests above are the contract.
- **Reviewer live verification**: log in; confirm a `csrf_token` cookie exists and
  that a normal UI write (e.g. create a bucket) still succeeds; replay the same
  write with `curl` **without** the header → 403; change the password in
  Settings → log out → old password fails, new password works; confirm a viewer
  account can change its own password but still cannot create a bucket.

## Done criteria

- [ ] Backend + frontend gates exit 0
- [ ] `grep -n "ConstantTimeCompare" backend/middleware/csrf.go` → present
- [ ] `grep -n "change-password" backend/middleware/auth.go` → present (viewer allowlist)
- [ ] `grep -n "X-CSRF-Token" src/lib/api.ts` → present
- [ ] `grep -rn "IdleTimeout" backend/utils/session.go` → present
- [ ] `git diff --name-only <023-merge-sha>..HEAD` shows only in-scope files

## STOP conditions

- The CSRF middleware breaks existing UI writes in the live check and the fix
  would require exempting broad path prefixes — STOP and report (the design is
  wrong, don't paper over it).
- You need to disable `SameSite=Lax` or add permissive CORS to make CSRF work —
  STOP; that would be a net security regression.
- You find yourself building admin user CRUD — that is plan 025.

## Maintenance notes

- **The CSRF exemption list is a security boundary.** Only `POST /auth/login` and
  `POST /setup` may be exempt — both are pre-session and rate-limited. Never add
  a prefix-based exemption.
- `SameSite=Lax` + JSON content-type remain the primary CSRF defences; the token
  is defence in depth. Keep all three.
- `isViewerAllowed` now permits exactly two non-GET paths (logout,
  change-password). Every future write endpoint is denied to viewers by default —
  that fail-closed shape is intentional.
- Session data lives in memory (scs default), so tightening `IdleTimeout` only
  affects live sessions; a restart still logs everyone out.
