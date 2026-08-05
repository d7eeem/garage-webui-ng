# Plan 018: Scoped read-only operator role (010)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/018-scoped-operator-role main`
> then `git log --oneline -1` — MUST show `b5d5ee4` or a "Merge branch 'advisor/017"
> commit (or newer main with plan 017), NOT `ee420fb`. If wrong, STOP.

## Status

- **Priority**: P2 (feature)
- **Effort**: M (server-light, client-heavy)
- **Risk**: MED — authorization boundary; a bug in the predicate is a full escalation
- **Depends on**: 017 (multi-user identity — merged into `main`). Roles attach to it.
- **Category**: direction / feature / security
- **Planned at**: commit `b5d5ee4` (main after 017), 2026-08-03

## Why this matters

Auth is currently all-or-nothing: any login is full admin through the proxy. This
adds a **`viewer` (read-only)** role so the UI can be handed to someone who should
only *look*, not change buckets/keys/layout. The spike verified the enforcement is
cheap: the web UI calls every read via `GET` and every mutation via `POST/PUT/DELETE`,
Garage honors those verbs, and there's a single middleware chokepoint — so a
fail-closed allowlist (~15 lines) is the whole server side.

**Scope caveat, state it in code and docs:** this is a **guardrail against mistakes
by a semi-trusted operator, not an isolation boundary.** The server still holds the
full admin token and proxies with it; the middleware is the only thing between a
viewer session and the admin API. It does not scope by bucket — a viewer sees
everything, just can't change it.

## Current state

### `backend/router/auth.go` (post-017) — multi-user login sets `username`

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
	if err := utils.Session.Renew(r); err != nil { /* 500 */ }
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", username)
	utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": username})
```

`parseUserPass(raw string) map[string]string` already exists (comma-separated
`user:hash`). `GetStatus` returns `{enabled, authenticated, username}`.

### `backend/middleware/auth.go` — the single chokepoint (whole file)

```go
func AuthMiddleware(next http.Handler) http.Handler {
	authData := utils.GetEnv("AUTH_USER_PASS", "")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := utils.Session.Get(r, "authenticated")
		if authData == "" {
			next.ServeHTTP(w, r)
			return
		}
		if auth == nil || !auth.(bool) {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

`r.URL.Path` here is **post-`/api`-strip** (router.go does `StripPrefix(apiPrefix, ...)`),
so paths are `/v2/CreateBucket`, `/browse/...`, `/auth/logout`, `/multipart/...`,
`/share/...`, `/metrics`, etc.

### `src/hooks/useAuth.ts` (post-017)

```ts
type AuthResponse = { enabled: boolean; authenticated: boolean; username?: string; };
export const useAuth = () => {
  const { data, isLoading } = useQuery({ queryKey: ["auth"], queryFn: () => api.get<AuthResponse>("/auth/status"), retry: false });
  return { isLoading, isEnabled: data?.enabled ?? false, isAuthenticated: data?.authenticated ?? false, username: data?.username };
};
```

### Write controls in the UI (what the client must gate)

Verified call sites (the spike's inventory). Each is a mutation the viewer must not see:
- Buckets list: `src/pages/buckets/components/create-bucket-dialog.tsx` (create)
- Bucket manage menu: `src/pages/buckets/manage/components/menu-button.tsx` (delete bucket)
- Overview: `src/pages/buckets/manage/overview/overview-website-access.tsx`, `overview-quota.tsx`, `overview-aliases.tsx` (update / add-remove alias)
- Permissions: `src/pages/buckets/manage/permissions/allow-key-dialog.tsx`, `permissions-tab.tsx` (allow/deny)
- Browse: `src/pages/buckets/manage/browse/actions.tsx` (upload, create folder), `object-actions.tsx` (delete/rename), `bulk-actions.tsx` (bulk delete)
- Keys: `src/pages/keys/page.tsx` (the **View secret** button + delete), `src/pages/keys/components/create-key-dialog.tsx` (create)
- Cluster: `src/pages/cluster/components/nodes-list.tsx` (assign/unassign, apply/revert), `connect-node-dialog.tsx`, `assign-node-dialog.tsx`

### Conventions

- Backend: `utils.ResponseErrorStatus(w, err, code)`; session via `utils.Session`.
- Frontend: `useAuth()` derived flags; daisyUI; the codebase already does
  capability-hiding (`browse-tab.tsx:41` hides the browse surface when no r+w key).

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
- `backend/router/auth.go` (Login checks admin+viewer maps → session `role`; GetStatus returns `role`)
- `backend/middleware/auth.go` (`isViewerAllowed` predicate + role enforcement)
- `backend/middleware/auth_test.go` (create — test `isViewerAllowed`)
- `backend/router/auth_test.go` (extend — role stamped on login)
- `README.md` + `backend/.env.example` (`AUTH_VIEWER_USER_PASS`)
- `src/hooks/useAuth.ts` (expose `role` + `canWrite`)
- The write-control files listed above (gate each control on `canWrite`)

**Out of scope**: per-bucket/tenant scoping, more than two roles, OIDC, removing the
server's admin token, a user-management UI. Do not change read behavior for admins.

## Steps

### Step 1: Login stamps a role

In `auth.go` `Login`, after decoding the body and the rate-limit check, replace the
single-map lookup with an admin-then-viewer check:

```go
	admins := parseUserPass(utils.GetEnv("AUTH_USER_PASS", ""))
	viewers := parseUserPass(utils.GetEnv("AUTH_VIEWER_USER_PASS", ""))
	if len(admins) == 0 && len(viewers) == 0 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	username := strings.TrimSpace(body.Username)
	role := ""
	if h, ok := admins[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "admin"
	} else if h, ok := viewers[username]; ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(body.Password)) == nil {
		role = "viewer"
	}
	if role == "" {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}
	// ... Renew ...
	utils.Session.Set(r, "authenticated", true)
	utils.Session.Set(r, "username", username)
	utils.Session.Set(r, "role", role)
	utils.ResponseSuccess(w, map[string]any{"authenticated": true, "username": username, "role": role})
```

In `GetStatus`, (a) fix `enabled` to match the middleware (Step 2 ORs both auth
vars), and (b) read and return `role`. **The `enabled` fix is required** — without
it, a viewer-only deployment reports `enabled:false`, which makes the client treat
a logged-out viewer as authenticated + writable:

```go
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != "" ||
		utils.GetEnv("AUTH_VIEWER_USER_PASS", "") != ""
	// ... existing isAuthenticated / username logic ...
	role := ""
	if rr, ok := utils.Session.Get(r, "role").(string); ok {
		role = rr
	}
	// return map includes "enabled", "authenticated", "username", "role": role
```

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 2: Middleware enforcement

In `middleware/auth.go`, add the predicate and the check. Note this file's `authData`
gate: treat auth as enabled if EITHER var is set:

```go
func isViewerAllowed(r *http.Request) bool {
	if r.Method == http.MethodGet {
		// one carve-out: never let a viewer reveal a secret access key
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") &&
			r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true
	}
	// the only non-GET a viewer may call is logout
	return r.Method == http.MethodPost && r.URL.Path == "/auth/logout"
}
```

and inside the handler, after the `auth == nil || !auth.(bool)` 401 check:

```go
		if role, _ := utils.Session.Get(r, "role").(string); role == "viewer" && !isViewerAllowed(r) {
			utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
			return
		}
```

Change the `authData` line to `authData := utils.GetEnv("AUTH_USER_PASS", "") + utils.GetEnv("AUTH_VIEWER_USER_PASS", "")` so a viewers-only config still enables auth. Add `"strings"` and `"net/http"` imports if missing (`net/http` is already there).

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 3: Backend tests

- `backend/middleware/auth_test.go` (create): `TestIsViewerAllowed` — table test: `GET /v2/GetClusterStatus` → true; `GET /v2/GetKeyInfo?showSecretKey=true` → false; `GET /v2/GetKeyInfo?id=x` (no showSecretKey) → true; `POST /v2/CreateBucket` → false; `POST /auth/logout` → true; `DELETE /browse/b/k` → false; `PUT /browse/b/k` → false. Build the requests with `httptest.NewRequest`.
- `backend/router/auth_test.go` (extend): `TestLoginStampsRole` — set `AUTH_USER_PASS` (an admin) and `AUTH_VIEWER_USER_PASS` (a viewer) via `t.Setenv` with generated bcrypt hashes; drive `Login` through `sessMgr.LoadAndSave(...)`; assert the admin login response has `role:"admin"` and the viewer `role:"viewer"`, and a wrong password → 401.

**Verify**: `cd backend && go test -race ./middleware/... ./router/...` → `ok`; existing auth/limiter/GetStatus tests still pass.

### Step 4: Frontend — expose `canWrite`

In `useAuth.ts`, add `role` to `AuthResponse` and derive a write flag. **When auth
is disabled (`isEnabled` false), everyone is effectively admin** (matches the
middleware pass-through):

```ts
  const role = data?.role;
  const isViewer = role === "viewer";
  return {
    isLoading,
    isEnabled: data?.enabled ?? false,
    isAuthenticated: data?.authenticated ?? false,
    username: data?.username,
    role,
    canWrite: !(data?.enabled) || role !== "viewer", // auth off ⇒ writable; else only non-viewers
  };
```

Add `role?: string;` to `AuthResponse`.

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 5: Gate the write controls

For each control file listed in "Current state", read `const { canWrite } = useAuth();`
and gate the mutating control: either **not render** it (`{canWrite && <Button .../>}`)
or **disable** it (`disabled={!canWrite}`), preferring not-render for standalone
action buttons/dialogs and disable for controls embedded in a form. Specifically:

- `create-bucket-dialog.tsx`: hide the "Create Bucket" trigger.
- `menu-button.tsx`: hide the Delete (and any mutating menu items).
- `overview-website-access.tsx` / `overview-quota.tsx`: disable the form inputs +
  submit for viewers (these auto-save; disabling inputs is cleanest).
- `overview-aliases.tsx`: hide add/remove alias controls.
- `permissions-tab.tsx` / `allow-key-dialog.tsx`: hide allow/deny key controls.
- `browse/actions.tsx`: hide Upload + Create Folder.
- `browse/object-actions.tsx`: hide Delete (keep Download/Share — reads).
- `browse/bulk-actions.tsx`: the selection toolbar's Delete — hide, or don't offer
  selection to viewers (simplest: hide the bulk Delete button).
- `keys/page.tsx`: hide the **View secret** button (it calls `showSecretKey`, which
  the server now 403s for viewers) and the delete-key button.
- `keys/components/create-key-dialog.tsx`: hide the create trigger.
- `cluster/components/nodes-list.tsx`: hide assign/unassign menu items and the
  Apply/Revert buttons; `connect-node-dialog.tsx` / `assign-node-dialog.tsx`: hide
  their triggers.

Keep all **read** UI (browsing, viewing, listing, status) fully visible.

**Verify after each cluster of edits**: `npx pnpm@9 run typecheck`. After all:
`npx pnpm@9 run typecheck && npx pnpm@9 run lint && npx pnpm@9 run build` → typecheck
& build exit 0; lint red only on pre-existing (no NEW errors from your files).

### Step 6: Docs

`README.md` — add an `AUTH_VIEWER_USER_PASS` entry (same `username:hash` /
comma-separated multi-user format as `AUTH_USER_PASS`) and a short "Roles"
paragraph: admins = full; viewers = read-only; **note the guardrail-not-isolation
caveat**. `backend/.env.example` — add a commented `AUTH_VIEWER_USER_PASS` example.

**Verify**: `grep -c AUTH_VIEWER_USER_PASS README.md backend/.env.example` ≥ 1 each.

### Step 7: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0.

## Test plan

- **Go**: `TestIsViewerAllowed` (the enforcement predicate — the security-critical
  logic, fail-closed) and `TestLoginStampsRole`. These are the load-bearing tests.
- **Frontend**: typecheck + build gate the control-gating wiring.
- **Live verification is the reviewer's job**: run with an admin and a viewer
  account; as viewer, confirm `/auth/status` returns `role:"viewer"`, write
  endpoints (`POST /v2/CreateBucket`, `DELETE /browse/...`,
  `GET /v2/GetKeyInfo?showSecretKey=true`) return **403**, reads return 200, and
  the UI hides write controls; as admin, everything works.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` all exit 0
- [ ] `grep -n "isViewerAllowed" backend/middleware/auth.go` and `grep -n 'Session.Set(r, "role"' backend/router/auth.go` match
- [ ] `grep -rn "canWrite" src/hooks/useAuth.ts` matches; the gated control files reference `useAuth`/`canWrite`
- [ ] `grep -c AUTH_VIEWER_USER_PASS README.md` ≥ 1
- [ ] `git diff --name-only b5d5ee4..HEAD` shows only in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 018 updated

## STOP conditions

- Base reset shows `ee420fb`.
- Current-state excerpts don't match live code.
- `isViewerAllowed` can't be made fail-closed for some route (e.g. a write served
  via GET turns up) — report it; do NOT loosen the predicate to "allow all GET"
  without the `showSecretKey` carve-out.
- The frontend gating balloons far past the listed controls (a control you can't
  find, or a shared component used in both read and write contexts) — report it
  rather than gating something that breaks reads.

## Maintenance notes

- **`isViewerAllowed` is the entire security boundary — review it like one.** It is
  fail-closed (unknown non-GET → denied). Any new *write* served via GET, or a new
  secret-revealing GET, must be added as a carve-out. A bug here is a full
  escalation for a viewer session.
- **This is a human guardrail, not isolation** — the server still proxies with the
  admin token. Documented in the README caveat; keep that caveat.
- **Client gating vs server enforcement**: the server is authoritative (viewers get
  403 regardless of UI). The frontend gating is UX so viewers don't see buttons
  that 403. If a control is missed, it degrades to a 403 toast — annoying, not
  unsafe. A reviewer should still confirm the high-impact destructive controls
  (delete bucket/key/object, apply layout) are gated.
- **Two roles only.** More granular or per-bucket roles are a much larger feature;
  the env-var model is a stopgap before real user management / OIDC.
