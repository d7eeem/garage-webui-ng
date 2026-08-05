# Plan 019: Structured audit log of mutations (D4)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/019-audit-log main` then
> `git log --oneline -1` — MUST show `075a885` or a "Merge branch 'advisor/016"
> commit (or newer main), NOT `ee420fb`. If wrong, STOP.

## Status

- **Priority**: P3 (feature)
- **Effort**: S
- **Risk**: LOW (adds logging; changes no response behavior)
- **Depends on**: 017 (session `username`, merged). Complements 018 (roles).
- **Category**: direction / feature
- **Planned at**: commit `075a885` (main after 016), 2026-08-03

## Why this matters

Nothing records **who did what**. As multi-user (017) and roles (018) land, the
absence is acute: no trail of who created/deleted a bucket, removed an object,
issued a share link, or changed the cluster layout. **Decision (settled): a
structured stdout log of mutating requests** — no database (the backend is
stateless), no new dependency. Operators pipe stdout wherever they collect logs; a
queryable in-app view is explicitly a *later, larger* project, not this one.

This rides the single middleware chokepoint: one `AuditLog` middleware logs every
**state-changing** request (POST/PUT/DELETE) with the session username, path, and
result status — including **denied** ones (a viewer's 403 write is exactly what an
audit wants to see).

## Current state

### `backend/router/router.go` — the wrap point (line ~42)

```go
	router.HandleFunc("/", ProxyHandler)

	mux.Handle("/", middleware.AuthMiddleware(router))
	return mux
```

All mutating traffic — the explicit `POST/PUT/DELETE /browse`, `/multipart`, the
bulk `POST /browse/{bucket}`, and every proxied `POST /v2/...` write — passes
through this one handler. Wrap it with `AuditLog`.

### `backend/main.go` — session is loaded outermost

```go
	if err := http.ListenAndServe(addr, sessionMgr.LoadAndSave(mux)); err != nil {
```

`LoadAndSave` wraps the whole mux, so by the time a request reaches the router
wrap point the scs session is in the context — `utils.Session.Get(r, "username")`
works there (returns nil safely if unset; comma-ok form, no panic).

### Logging convention

Stdlib `log` (`log.Println`/`log.Printf`), used in `main.go`. No logging library.

### `utils.Session.Get` — reads the session (username set at login by plan 017)

`utils.Session.Get(r, "username")` returns the logged-in user (persisted in the
session), or nil when not logged in / auth disabled.

## Commands

`pnpm` not installed → `npx pnpm@9 <cmd>` (only needed for the final frontend build sanity — this plan changes no frontend).

| Purpose | Command | Expected |
|---|---|---|
| Go build/vet/fmt | `cd backend && go build ./... && go vet ./... && gofmt -l .` | exit 0, no output |
| Go tests | `cd backend && go test -race ./...` | `ok` |
| Build (sanity) | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/middleware/audit.go` (create — the `AuditLog` middleware)
- `backend/middleware/audit_test.go` (create — test it logs mutations, skips reads)
- `backend/router/router.go` (wrap the router with `AuditLog`)
- `README.md` (a short "Audit log" note)

**Out of scope**: a database / queryable store / in-app audit UI (deferred), logging
GET reads (noise), touching `middleware/auth.go` (that's 018's file — do NOT edit
it), any handler.

## Steps

### Step 1: The `AuditLog` middleware

Create `backend/middleware/audit.go`:

```go
package middleware

import (
	"encoding/json"
	"khairul169/garage-webui/utils"
	"log"
	"net/http"
	"time"
)

// statusRecorder captures the response status for the audit line.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AuditLog emits a structured (JSON) stdout line for every state-changing
// request (POST/PUT/DELETE/PATCH), including denied ones. Reads (GET/HEAD/OPTIONS)
// are not logged. The backend is stateless — this is a stdout trail, not a store.
func AuditLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		user, _ := utils.Session.Get(r, "username").(string)
		if user == "" {
			user = "-"
		}
		entry, _ := json.Marshal(map[string]any{
			"audit":  true,
			"ts":     time.Now().UTC().Format(time.RFC3339),
			"user":   user,
			"method": r.Method,
			"path":   r.URL.Path,
			"status": rec.status,
		})
		log.Println(string(entry))
	})
}
```

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 2: Wrap the router

In `router.go`, change the wrap line to compose `AuditLog` outside `AuthMiddleware`
(so it records the final status, including a 401/403):

```go
	mux.Handle("/", middleware.AuditLog(middleware.AuthMiddleware(router)))
```

**Verify**: `cd backend && go build ./...` → exit 0; `grep -c "AuditLog" backend/router/router.go` = 1.

### Step 3: Test

Create `backend/middleware/audit_test.go`, `package middleware`. Capture log output
with `log.SetOutput(&buf)` (restore with `defer log.SetOutput(os.Stderr)`).
`TestAuditLog`:
- A `POST` through `AuditLog(dummyHandler)` (dummy writes 200) → the buffer contains
  a line with `"audit":true`, `"method":"POST"`, `"path":"/x"`, `"status":200`, and
  `"user":"-"` (no session in the bare test).
- A `GET` → the buffer is **empty** (reads are not logged).
- A `DELETE` where the dummy handler writes 403 → logged with `"status":403` (proves
  denied mutations are captured and the status recorder works).

Use `httptest.NewRequest` / `httptest.NewRecorder`. No scs session needed (username
defaults to `-`; `utils.Session.Get` returns nil safely).

**Verify**: `cd backend && go test -race ./middleware/...` → `ok`, `TestAuditLog` passes.

### Step 4: Docs

`README.md` — add a short note (near the Authentication/Env section): the server
emits a structured JSON audit line to **stdout** for every state-changing request
(user, method, path, status), including denied ones; collect it via your log
pipeline. No configuration required.

**Verify**: `grep -ci "audit" README.md` ≥ 1.

### Step 5: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run build
```
All exit 0 (this plan changes no frontend, but confirm the build is unaffected).

## Test plan

- **Go**: `TestAuditLog` covers the middleware — mutation logged (with status),
  read skipped, denied-write captured. That is the whole behavior.
- **Live verification is the reviewer's job**: run the backend, perform a mutation
  (e.g. delete an object) as a logged-in user, and confirm a JSON audit line with
  the right `user`/`method`/`path`/`status` appears on stdout; confirm a GET
  produces none.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run build` exit 0
- [ ] `grep -n "func AuditLog" backend/middleware/audit.go` and `grep -c "AuditLog" backend/router/router.go` (=1) match
- [ ] `TestAuditLog` passes
- [ ] `git diff --name-only 075a885..HEAD` shows only the 4 in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 019 updated

## STOP conditions

- Base reset shows `ee420fb`.
- Current-state excerpts don't match live code (e.g. the `router.go` wrap line
  differs — 018 was told NOT to touch `router.go`, so if it changed, report it).
- `utils.Session.Get` panics inside `AuditLog` in a live run (it should not — it's
  within `LoadAndSave`'s scope; report the trace if it does).
- You find yourself adding a database or an audit-query endpoint — out of scope.

## Maintenance notes

- **Stdout only, by design** — no store, no query UI. That's the settled decision
  for a stateless backend; a queryable audit is a separate, larger project.
- **Logs mutations, not reads** — GET is skipped to avoid drowning the signal. If a
  specific read ever needs auditing (e.g. `showSecretKey`), add it explicitly.
- **The login route (`POST /auth/login`) is on the outer mux, not under this
  wrap**, so login attempts are not in the audit stream (they're rate-limited
  elsewhere). Logout is under the wrap and is logged. If login auditing is wanted
  later, wrap the login route too.
- **Composes with 018**: a viewer's denied write logs as `status:403`, which is a
  useful signal. Keep `AuditLog` outside `AuthMiddleware` so the final status is
  what's recorded.
