# Plan 001: Stop serving Garage cluster secrets to the browser

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/router/config.go backend/schema/config.go backend/main.go src/types/garage.ts`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

`GET /api/config` serializes the entire parsed `garage.toml` and returns it to
the browser. That struct includes the cluster's RPC secret and the admin API
token. Those two values together are full control of the Garage cluster: the
RPC secret lets a process join the cluster as a peer, and the admin token
authorizes every admin API call.

The web UI does not need either one. Only two components read the config, and
between them they use exactly two fields: `s3_web.bind_addr` and
`s3_web.root_domain`.

Today those secrets land in the browser's JS heap, in the TanStack Query cache,
in devtools' network tab, in any HAR file a user exports for a bug report, and
in the access logs of any reverse proxy that logs response bodies. And because
authentication is opt-in (`AUTH_USER_PASS` unset by default), on a default
deployment the endpoint is reachable by anyone who can reach port 3909 —
no credentials required.

After this plan, the endpoint returns only the fields the UI actually consumes,
and the server logs a warning at startup when it is running without
authentication.

## Current state

### Files

- `backend/router/config.go` — the `GET /config` handler. Returns the raw config struct.
- `backend/schema/config.go` — the TOML/JSON struct. Carries the secret fields.
- `src/types/garage.ts` — the frontend mirror of that struct. Declares the secret fields.
- `backend/main.go` — startup. Where the no-auth warning goes.
- `src/hooks/useConfig.ts` — the single query hook for this endpoint (no change needed, listed for context).

### Excerpts

`backend/router/config.go` — the whole file:

```go
package router

import (
	"khairul169/garage-webui/utils"
	"net/http"
)

type Config struct{}

func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	config := utils.Garage.Config
	utils.ResponseSuccess(w, config)
}
```

`backend/schema/config.go:3-16` — note the three secret-bearing fields:

```go
type Config struct {
	RPCBindAddr   string `json:"rpc_bind_addr" toml:"rpc_bind_addr"`
	RPCPublicAddr string `json:"rpc_public_addr" toml:"rpc_public_addr"`
	RPCSecret     string `json:"rpc_secret" toml:"rpc_secret"`
	Admin         Admin  `json:"admin" toml:"admin"`
	S3API         S3API  `json:"s3_api" toml:"s3_api"`
	S3Web         S3Web  `json:"s3_web" toml:"s3_web"`
}

type Admin struct {
	AdminToken   string `json:"admin_token" toml:"admin_token"`
	APIBindAddr  string `json:"api_bind_addr" toml:"api_bind_addr"`
	MetricsToken string `json:"metrics_token" toml:"metrics_token"`
}
```

`src/types/garage.ts:1-20` — the frontend type, which also declares them:

```ts
export type Config = {
  metadata_dir: string;
  data_dir: string;
  db_engine: string;
  metadata_auto_snapshot_interval: string;
  replication_factor: number;
  compression_level: number;
  rpc_bind_addr: string;
  rpc_public_addr: string;
  rpc_secret: string;
  s3_api?: S3API;
  s3_web?: S3Web;
  admin?: Admin;
};

export type Admin = {
  api_bind_addr: string;
  admin_token: string;
  metrics_token: string;
};
```

### What the UI actually reads

Exactly two call sites, both reading only `s3_web`:

`src/pages/buckets/manage/browse/share-dialog.tsx:20-21`:

```ts
const websitePort = config?.s3_web?.bind_addr?.split(":").pop() || "80";
const rootDomain = config?.s3_web?.root_domain;
```

`src/pages/buckets/manage/overview/overview-website-access.tsx:22-23`:

```ts
const websitePort = config?.s3_web?.bind_addr?.split(":").pop() || "80";
const rootDomain = config?.s3_web?.root_domain;
```

Confirm this yourself before you start:

```bash
grep -rn "useConfig()" src/
```

Expected: exactly the two files above (plus the definition in
`src/hooks/useConfig.ts`). If a third consumer exists, see STOP conditions.

`backend/main.go:15-25` — where the startup warning goes:

```go
func main() {
	// Initialize app
	godotenv.Load()
	utils.InitCacheManager()
	sessionMgr := utils.InitSessionManager()

	if err := utils.Garage.LoadConfig(); err != nil {
		log.Println("Cannot load garage config!", err)
	}

	basePath := os.Getenv("BASE_PATH")
```

`backend/middleware/auth.go:9-18` — why the warning matters (this is the
pass-through when `AUTH_USER_PASS` is empty). Read only; do not modify:

```go
func AuthMiddleware(next http.Handler) http.Handler {
	authData := utils.GetEnv("AUTH_USER_PASS", "")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := utils.Session.Get(r, "authenticated")

		if authData == "" {
			next.ServeHTTP(w, r)
			return
		}
```

### Repo conventions to match

- **Go schema structs** live in `backend/schema/`, one file per domain, with
  both `json:` and `toml:` tags. See `backend/schema/bucket.go` for the
  exemplar — plain structs, no methods, JSON tags in `camelCase` for API
  responses and `snake_case` for TOML-derived config.
- **Handlers** are methods on an empty struct (`type Config struct{}`), take
  `(w http.ResponseWriter, r *http.Request)`, and end in a call to
  `utils.ResponseSuccess` or `utils.ResponseError`. See
  `backend/router/buckets.go` for the exemplar.
- **Logging** uses the stdlib `log` package with `log.Println` / `log.Printf`.
  See `backend/main.go:22` and `:44`. Do not introduce a logging library.
- **Frontend types** live in `src/types/garage.ts` as exported `type` aliases
  (not interfaces), fields in `snake_case` for config, `camelCase` for API
  models.

## Commands you will need

This repo has no test suite yet and no `typecheck` script (plan 002 adds both).
Use these:

| Purpose         | Command                                         | Expected on success |
|-----------------|-------------------------------------------------|---------------------|
| Go build        | `cd backend && go build ./...`                  | exit 0, no output   |
| Go vet          | `cd backend && go vet ./...`                    | exit 0, no output   |
| Go format check | `cd backend && gofmt -l .`                      | no output           |
| Frontend deps   | `pnpm install`                                  | exit 0              |
| Frontend build  | `pnpm run build`                                | exit 0              |
| Lint            | `pnpm run lint`                                 | exit 0              |

If `go` or `pnpm` is not installed in your environment, see STOP conditions.

## Scope

**In scope** (the only files you should modify):

- `backend/schema/config.go`
- `backend/router/config.go`
- `backend/main.go`
- `src/types/garage.ts`

**Out of scope** (do NOT touch, even though they look related):

- `backend/utils/garage.go` — `GetAdminKey()` at line 88 legitimately reads
  `g.Config.Admin.AdminToken` server-side to authenticate outbound calls to
  Garage. The token must keep flowing there. Only the *HTTP response* changes.
- `backend/router/proxy.go` — injects the admin token into proxied requests.
  Correct and required.
- `backend/middleware/auth.go` — enforcement logic is unchanged by this plan.
  Making auth mandatory is a breaking change for existing deployments and is
  deliberately not in scope; this plan only adds a warning.
- `src/hooks/useConfig.ts` — the hook is fine as-is.
- The two `useConfig()` consumers — they read `s3_web` fields that survive.

## Git workflow

- Branch: `advisor/001-stop-serving-cluster-secrets`
- The repo uses conventional commits. Recent examples from `git log`:
  `fix: update API endpoint for cluster status query`, `feat: add authentication`,
  `chore: bump version to 1.1.0`.
- Suggested commit: `fix: do not expose rpc_secret and admin_token via /api/config`
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add a response-only config type that omits the secrets

In `backend/schema/config.go`, **keep the existing `Config`, `Admin`, `S3API`,
and `S3Web` structs exactly as they are** — they are the TOML parsing target and
the server needs `Admin.AdminToken` internally.

Append a new response type to the same file:

```go
// ConfigResponse is the subset of the Garage configuration that is safe to
// return to the browser. Secret-bearing fields (rpc_secret, admin_token,
// metrics_token) are deliberately absent — the UI never needs them.
type ConfigResponse struct {
	S3API S3APIResponse `json:"s3_api"`
	S3Web S3WebResponse `json:"s3_web"`
}

type S3APIResponse struct {
	RootDomain string `json:"root_domain"`
	S3Region   string `json:"s3_region"`
}

type S3WebResponse struct {
	BindAddr   string `json:"bind_addr"`
	RootDomain string `json:"root_domain"`
	Index      string `json:"index"`
}

// NewConfigResponse projects a parsed Config onto the browser-safe subset.
func NewConfigResponse(c Config) ConfigResponse {
	return ConfigResponse{
		S3API: S3APIResponse{
			RootDomain: c.S3API.RootDomain,
			S3Region:   c.S3API.S3Region,
		},
		S3Web: S3WebResponse{
			BindAddr:   c.S3Web.BindAddr,
			RootDomain: c.S3Web.RootDomain,
			Index:      c.S3Web.Index,
		},
	}
}
```

Note: `S3API` fields are included even though no component reads them today —
they are non-secret and the frontend type already declares them, so keeping
them avoids a second breaking change later. `api_bind_addr` is intentionally
dropped from both: it exposes internal bind addresses and nothing reads it.

**Verify**: `cd backend && go build ./...` → exit 0, no output.

### Step 2: Return the projection from the handler

Rewrite `backend/router/config.go` to:

```go
package router

import (
	"khairul169/garage-webui/schema"
	"khairul169/garage-webui/utils"
	"net/http"
)

type Config struct{}

func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	utils.ResponseSuccess(w, schema.NewConfigResponse(utils.Garage.Config))
}
```

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output from any of the three.

### Step 3: Prove no secret field can reach the response

Run:

```bash
cd backend && grep -n "RPCSecret\|AdminToken\|MetricsToken" router/ schema/ utils/ -r
```

Expected matches, and **only** these:

- `schema/config.go` — the three struct field declarations (still present; they
  are the TOML parse target).
- `utils/garage.go:93` — `return g.Config.Admin.AdminToken` inside
  `GetAdminKey()`. This is a server-side read for outbound auth. Correct.

If any file under `router/` matches, the projection is incomplete — fix it
before continuing.

### Step 4: Drop the secret fields from the frontend type

In `src/types/garage.ts`, replace the `Config` and `Admin` type declarations so
they mirror the new response. Delete the `Admin` type entirely (nothing else
imports it — verify below).

Target shape:

```ts
export type Config = {
  s3_api?: S3API;
  s3_web?: S3Web;
};

export type S3API = {
  s3_region: string;
  root_domain: string;
};

export type S3Web = {
  bind_addr: string;
  root_domain: string;
  index: string;
};
```

Note `api_bind_addr` is removed from `S3API` and `S3Web` to match step 1.

Before deleting `Admin`, confirm nothing imports it:

```bash
grep -rn "Admin" src/ --include='*.ts' --include='*.tsx'
```

Expected after your edit: no matches (or only unrelated words like
"admin" in a comment/string). If a component imports the `Admin` type, see STOP
conditions.

**Verify**:

```bash
pnpm install && pnpm run build
```

→ exit 0. `tsconfig.app.json` sets `strict`, `noUnusedLocals`, and
`noUnusedParameters`, so an orphaned import or unused local will fail the build.
That is the signal you want.

### Step 5: Warn at startup when authentication is disabled

In `backend/main.go`, immediately after the `utils.Garage.LoadConfig()` block
(currently lines 21-23), add:

```go
	if utils.GetEnv("AUTH_USER_PASS", "") == "" {
		log.Println("WARNING: AUTH_USER_PASS is not set — the web UI and the " +
			"Garage admin API proxy are accessible without authentication. " +
			"Set AUTH_USER_PASS or restrict network access to this port.")
	}
```

`utils` and `log` are already imported in this file; do not add imports.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

### Step 6: Manual smoke check (only if you have a running Garage instance)

If — and only if — a Garage instance is available to you, start the server and
confirm the response shape:

```bash
curl -s localhost:3909/api/config
```

Expected: a JSON object containing only `s3_api` and `s3_web` keys. It must not
contain `rpc_secret`, `admin_token`, `metrics_token`, `rpc_bind_addr`, or
`rpc_public_addr`.

If no Garage instance is available, skip this step — the grep in step 3 and the
compile gates are the binding checks. Do not stand up a Garage cluster just for
this.

## Test plan

This repo has no test infrastructure at the time this plan was written; plan
002 establishes it. Do **not** add a test framework here — that is 002's job and
doing it in both places creates a merge conflict.

Add one table-driven Go test that needs no framework beyond the stdlib, in a new
file `backend/schema/config_test.go`:

- `TestNewConfigResponseOmitsSecrets` — build a `schema.Config` with non-empty
  `RPCSecret`, `Admin.AdminToken`, and `Admin.MetricsToken` (use obvious dummy
  strings such as `"dummy-rpc-secret"`; never a real credential), pass it
  through `NewConfigResponse`, marshal the result with `encoding/json`, and
  assert the resulting bytes contain none of the three dummy values and none of
  the strings `rpc_secret`, `admin_token`, `metrics_token`.
- `TestNewConfigResponseKeepsWebFields` — assert `s3_web.bind_addr`,
  `s3_web.root_domain`, and `s3_api.root_domain` survive the projection.

Structural pattern: standard Go `testing` package, `func TestX(t *testing.T)`,
failures via `t.Errorf`. There is no existing test in this repo to model on, so
follow stdlib convention.

**Verification**: `cd backend && go test ./schema/...` → `ok`, 2 tests pass.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && gofmt -l .` produces no output
- [ ] `cd backend && go test ./schema/...` passes, including the 2 new tests
- [ ] `cd backend && grep -rn "RPCSecret\|AdminToken\|MetricsToken" router/` returns no matches
- [ ] `grep -rn "rpc_secret\|admin_token\|metrics_token" src/` returns no matches
- [ ] `pnpm run build` exits 0
- [ ] `pnpm run lint` exits 0
- [ ] `git status` shows only these modified/created files: `backend/schema/config.go`, `backend/schema/config_test.go`, `backend/router/config.go`, `backend/main.go`, `src/types/garage.ts`, `plans/README.md`
- [ ] `plans/README.md` status row for 001 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above
  (the codebase has drifted since this plan was written).
- `grep -rn "useConfig()" src/` finds a consumer beyond `share-dialog.tsx` and
  `overview-website-access.tsx`. A third consumer may read a field this plan
  removes — report which file and which field rather than guessing whether to
  keep it.
- Any component imports the `Admin` type from `src/types/garage.ts`. Report the
  file; do not keep the secret fields to satisfy it.
- `go` or `pnpm` is not installed in your environment. Report this — do not
  install a toolchain, and do not skip the verification gates.
- `pnpm run build` fails for a reason unrelated to your edits (e.g. a
  pre-existing type error elsewhere). Report the error rather than fixing
  unrelated code.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **`schema.Config` and `schema.ConfigResponse` must be kept in sync by hand.**
  If a future change adds a field to `garage.toml` that the UI needs, it has to
  be added to *both* the parse struct and the projection. That is the intended
  friction: the default is "not exposed."
- **Never widen `ConfigResponse` by embedding `Config`.** Embedding would
  silently reintroduce every field, including the secrets, and no test here
  would catch it except `TestNewConfigResponseOmitsSecrets` — which is exactly
  why that test asserts on marshalled bytes rather than on struct fields.
- **Reviewer should scrutinize**: that `NewConfigResponse` copies fields
  explicitly rather than via reflection or embedding, and that the frontend
  `Config` type has no optional field that would let a stale secret read
  typecheck.
- **Operational follow-up, not code**: any deployment that exposed
  `/api/config` to an untrusted network before this change should treat the
  cluster's `rpc_secret` and `admin_token` as disclosed and rotate them. Rotating
  `rpc_secret` requires a coordinated restart of all Garage nodes — see the
  Garage documentation. This plan cannot do that for you; flag it to the
  operator in your completion report.
- **Deliberately deferred**: making `AUTH_USER_PASS` mandatory. That breaks
  every existing deployment that relies on network isolation instead. Step 5
  adds a warning only. If the maintainer later wants auth on by default, that is
  a major-version change with a migration note.
