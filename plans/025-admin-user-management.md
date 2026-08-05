# Plan 025: Admin user management (API + Settings → Users)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/025-admin-user-management main`
> then `git log --oneline -1`.
> SENTINEL (**022, 023, 024 must be merged**):
> `test -f backend/middleware/csrf.go && test -f src/pages/settings/page.tsx && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (privileged endpoints; a mistake here means privilege escalation or admin lockout)
- **Depends on**: **022**, **023**, **024**. Blocks 026.
- **Category**: feature / security
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

Users live in SQLite (022) and can bootstrap themselves (023) and change their own
password (024) — but an administrator still cannot **add, disable, rename, or
remove** anyone without touching the database directly. This plan completes the
"never edit Docker again" goal with an Administration UI.

**Two hard safety properties** this plan must guarantee:

1. **No privilege leak** — only `role == "admin"` may reach any `/admin/*`
   endpoint. Note that `isViewerAllowed` currently permits *every* GET, so a
   viewer could otherwise **list users**. That must be closed explicitly.
2. **No lockout** — it must be impossible to delete, disable, or demote the
   **last enabled administrator**, or for an admin to delete themselves. Without
   these guards the instance becomes permanently unadministrable (the `/setup`
   wizard cannot re-open — see plan 023).

## Current state

### `backend/middleware/auth.go` (after plan 024)

```go
func isViewerAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet {
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") && r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true            // ← every GET is allowed: /admin/users would leak
	}
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" || r.URL.Path == "/auth/change-password"
	}
	return false
}
```

### `backend/store/users.go` (from plan 022)

Already provides: `ListUsers`, `GetUserByID`, `GetUserByUsername`, `CreateUser`,
`SetPassword`, `SetDisabled`, `SetRole`, `Rename`, `DeleteUser`,
`CountEnabledAdmins`, `RoleAdmin`/`RoleViewer`, and the sentinel errors
(`ErrUsernameTaken`, `ErrUserNotFound`, `ErrInvalidRole`, `ErrWeakPassword`,
`ErrInvalidUsername`). `User.PasswordHash` carries `json:"-"`.

### Route registration — `backend/router/router.go`

Go 1.22 `ServeMux` patterns with path values are already used:
```go
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
```
Read a path value with `r.PathValue("id")`.

### Settings page (from plan 024) — add a tab

`src/pages/settings/page.tsx` builds a `TabView` from a `tabs: Tab[]` array with a
single `account` entry. Append a `users` tab; do not restructure the page.

### Handler conventions

Methods on empty structs, `(w, r)`, `utils.ResponseSuccess/ResponseError*`,
**always `return` after an error response**, wrap with `fmt.Errorf("...: %w", err)`.

## Commands

Same gates as the previous plans.

## Scope

**In scope**:
- `backend/middleware/auth.go` (deny `/admin/` to viewers) + `auth_test.go`
- `backend/router/admin_users.go` + `backend/router/admin_users_test.go` (create)
- `backend/router/router.go` (register 5 routes)
- `backend/store/users.go` (only if a guard helper is genuinely missing) + tests
- `src/pages/settings/users-tab.tsx`, `src/pages/settings/users-hooks.ts`,
  `src/pages/settings/users-schema.ts` (create)
- `src/pages/settings/page.tsx` (append the tab)

**Out of scope**: RBAC beyond the existing `admin`/`viewer` roles, MFA, OAuth/LDAP
(future extension points only), docs/screenshots (plan 026), the login/setup/
password-change flows, `isViewerAllowed`'s existing GET carve-out for `GetKeyInfo`.

## Steps

### Step 1 — Close the viewer read leak

In `isViewerAllowed`, **before** the "every GET is allowed" return:
```go
		// Administration endpoints are admin-only, including reads: the user
		// list is not viewer-visible information.
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			return false
		}
```
**Verify**: `grep -n "/admin/" backend/middleware/auth.go` → present.

### Step 2 — `requireAdmin` guard (defence in depth)

In `backend/router/admin_users.go`:
```go
type AdminUsers struct{}

// requireAdmin reports whether the session belongs to an administrator and
// writes a 403 when it does not. Every /admin/* handler must call it first —
// the middleware check in isViewerAllowed is the outer boundary; this is the
// second, so a routing mistake cannot expose these endpoints.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if role, _ := utils.Session.Get(r, "role").(string); role == store.RoleAdmin {
		return true
	}
	utils.ResponseErrorStatus(w, errors.New("forbidden: administrator role required"), http.StatusForbidden)
	return false
}
```

### Step 3 — The five endpoints

All start with `if !requireAdmin(w, r) { return }`. All are registered on the
**inner** `router` (so they inherit auth + CSRF + audit).

| Route | Behaviour |
|---|---|
| `GET /admin/users` | `store.Default().ListUsers(ctx)` → `utils.ResponseSuccess(w, users)`. **Never** include hashes (guaranteed by `json:"-"`). |
| `POST /admin/users` | Body `{username, password, role}`. Validate role ∈ {admin, viewer}. `CreateUser`. `ErrUsernameTaken` → **409**; `ErrWeakPassword`/`ErrInvalidUsername`/`ErrInvalidRole` → **400**. Returns the created user. |
| `PATCH /admin/users/{id}` | Body with optional `username`, `role`, `disabled` (use pointers: `*string`, `*string`, `*bool`, so "absent" ≠ "empty"). Apply only the provided fields via `Rename`/`SetRole`/`SetDisabled`. Enforce the guards in Step 4. Returns the updated user. |
| `DELETE /admin/users/{id}` | Enforce guards, then `DeleteUser`. Returns `true`. |
| `POST /admin/users/{id}/reset-password` | Body `{newPassword}`. `SetPassword` (validation surfaces `ErrWeakPassword` → 400). Returns `true`. **Never** returns or logs the password. |

Parse the id with `strconv.ParseInt(r.PathValue("id"), 10, 64)`; bad id → 400.
Unknown id → **404** (`ErrUserNotFound`).

### Step 4 — Lockout guards (the critical part)

Implement one helper used by both `PATCH` and `DELETE`:

```go
// ensureNotLastAdmin rejects a change that would remove the final enabled
// administrator, which would leave the instance permanently unadministrable
// (the /setup wizard only opens when there are zero users).
```

Rules — return **409 Conflict** with a clear message when violated:
1. **Self-deletion**: an admin may not `DELETE` their own account
   ("you cannot delete your own account"). Compare the session username against
   the target user.
2. **Last enabled admin**: if the target is an enabled admin and
   `CountEnabledAdmins(ctx) <= 1`, reject any of: `DELETE`, `disabled: true`,
   `role: "viewer"` ("cannot remove the last administrator").
3. **Self-demotion / self-disable**: an admin may not set their own
   `role: "viewer"` or `disabled: true` (prevents an accidental one-click
   lockout even when other admins exist). Message: "you cannot demote or disable
   your own account".

Evaluate guards **before** mutating, and read `CountEnabledAdmins` inside the same
request (a small TOCTOU window is acceptable here; note it in a comment).

### Step 5 — Register routes

```go
	adminUsers := &AdminUsers{}
	router.HandleFunc("GET /admin/users", adminUsers.List)
	router.HandleFunc("POST /admin/users", adminUsers.Create)
	router.HandleFunc("PATCH /admin/users/{id}", adminUsers.Update)
	router.HandleFunc("DELETE /admin/users/{id}", adminUsers.Delete)
	router.HandleFunc("POST /admin/users/{id}/reset-password", adminUsers.ResetPassword)
```
**Verify**: `grep -c "/admin/users" backend/router/router.go` = 5; `go build ./...` exit 0.

### Step 6 — Frontend: Users tab

`src/pages/settings/users-hooks.ts` — one hook per endpoint, query key
`["admin-users"]`; mutations spread `...options` last and invalidate
`["admin-users"]` on success (match `src/pages/buckets/manage/hooks.ts`):
`useUsers()`, `useCreateUser()`, `useUpdateUser()`, `useDeleteUser()`,
`useResetPassword()`.

`src/pages/settings/users-schema.ts` — zod schemas: `createUserSchema`
(username, password min 10, role enum), `resetPasswordSchema` (newPassword min 10).

`src/pages/settings/users-tab.tsx`:
- A table of users: **Username**, **Role** (badge), **Status** (Active / Disabled),
  **Last login** (use `dayjs` as elsewhere; `—` when null), **Created**, and a
  row action menu.
- Row actions: **Reset password** (dialog), **Change role** (admin ⇄ viewer),
  **Disable / Enable**, **Delete** (confirm dialog).
- A **Create user** button opening a dialog (username, password, role) — follow
  `src/pages/keys/components/create-key-dialog.tsx` for dialog structure and
  `createDisclosure` usage.
- Disable the destructive actions client-side for the current user and when the
  server would reject (e.g. only one enabled admin) — but treat the **server as
  authoritative**: surface its 409 message via `toast.error`.
- Gate the whole tab on `useAuth().role === "admin"`; render a short
  "Administrator access required" note otherwise.

`src/pages/settings/page.tsx` — append
`{ name: "users", title: "Users", icon: UsersIcon, Component: UsersTab }` to `tabs`.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` exit 0.

### Step 7 — Tests

`backend/middleware/auth_test.go` (add): viewer + `GET /admin/users` → **denied**
by `isViewerAllowed` (this is the leak fix); admin unaffected.

`backend/router/admin_users_test.go` — serve through `sessMgr.LoadAndSave(...)`
with a seeded temp store, and a helper that puts `role`/`username` in the session:
- **Authorization**: viewer session → 403 on all five endpoints; no session → 401
  (via middleware); admin → 200.
- **List**: response JSON contains usernames and **does not contain**
  `password_hash` or any `$2a$`/`$2b$` substring (assert on the raw body).
- **Create**: 200 + user persisted; duplicate → 409; weak password → 400; bad role → 400.
- **Update**: rename works; role change works; `disabled: true` works.
- **Guards** (each asserts the DB is unchanged afterwards):
  - delete self → 409
  - delete the only enabled admin → 409
  - disable the only enabled admin → 409
  - demote the only enabled admin to viewer → 409
  - self-demote / self-disable → 409
  - with **two** admins, deleting the *other* admin → 200
- **Reset password**: 200 and the new password verifies; body contains neither the
  password nor a hash; weak password → 400.
- **404**: unknown id on PATCH/DELETE/reset.

**Verify**: `cd backend && go test -race ./...` all `ok`.

### Step 8 — Gate sweep + commit

Backend + frontend gates. Commit on `advisor/025-admin-user-management`:
`feat: admin user management API and Settings UI`

## Test plan

- The guard tests above are the heart of this plan — an untested lockout guard is
  the single most likely way to brick an instance.
- **Reviewer live verification**: as admin, create a viewer; log in as that viewer
  and confirm the Users tab is inaccessible and `GET /api/admin/users` returns
  403; back as admin, reset the viewer's password and log in with it; try to
  delete the last admin and confirm a clear 409 in the UI; confirm no response
  body anywhere contains a bcrypt hash.

## Done criteria

- [ ] Backend + frontend gates exit 0
- [ ] `grep -c "/admin/users" backend/router/router.go` → `5`
- [ ] `grep -n "requireAdmin" backend/router/admin_users.go` → present and called by all five handlers
- [ ] `grep -n "/admin/" backend/middleware/auth.go` → present (viewer denial)
- [ ] `grep -rn "CountEnabledAdmins" backend/router/admin_users.go` → present (lockout guard)
- [ ] `git diff --name-only <024-merge-sha>..HEAD` shows only in-scope files

## STOP conditions

- SENTINEL fails (024 not merged).
- A lockout guard cannot be implemented without racing — report; do not ship
  without the last-admin guard.
- You need to return a password or hash to satisfy the UI — STOP; redesign the UI.
- You find yourself adding RBAC roles, MFA, or OIDC — out of scope (extension
  points are documented in plan 026, not built).

## Maintenance notes

- **Two independent authorization layers** protect `/admin/*`: the
  `isViewerAllowed` denial in the middleware and `requireAdmin` in every handler.
  Keep both — the middleware is the boundary, the handler check is the backstop.
- **The lockout guards are load-bearing.** `/setup` only reopens at zero users, so
  losing the last admin is unrecoverable from the UI. Any refactor must keep the
  `CountEnabledAdmins <= 1` checks and their tests.
- `User.PasswordHash` is `json:"-"`; the raw-body assertions in the tests are what
  keep it that way. Do not add a DTO that re-exposes it.
- The `role` column is a plain string with two values today. RBAC would extend it
  (roles/permissions tables) — the store API is already role-agnostic enough that
  handlers, not the schema, would carry most of the change.
