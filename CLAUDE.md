# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**Garage WebUI-NG** — admin web UI for [Garage](https://garagehq.deuxfleurs.fr/) (self-hosted, S3-compatible distributed object storage). A **Go backend + React/TypeScript frontend**, shipped as a single binary (the Go binary embeds the built frontend via `//go:embed`) or a Docker image. The backend is a gateway to a running Garage cluster **plus** a small local SQLite store for its own users and authentication (`backend/store/`, at `DB_PATH`). Nothing else is persisted locally: every bucket, key and object shown comes live from Garage.

- **Go module path:** `github.com/d7eeem/garage-webui-ng` (imports look like `github.com/d7eeem/garage-webui-ng/utils`). npm package: `garage-webui-ng`. Docker image: `ghcr.io/d7eeem/garage-webui-ng`.
- A next-generation fork of [garage-webui](https://github.com/khairul169/garage-webui) (© 2024 Khairul Hidayat, MIT). Keep the upstream attribution intact.

## Commands

Frontend lives at the repo root (package manager: **pnpm**); backend under `backend/` (**Go 1.25+** — the `go` directive in `backend/go.mod` is `1.25.0`, raised by `modernc.org/sqlite`; CI and the Docker builder pin an **exact** patch,
currently `1.25.12` / `golang:1.25.12-alpine`, in four places that must stay in
lockstep — `.github/workflows/ci.yml` ×2, `.github/workflows/release.yml`, and
the `Dockerfile`. A floating minor is what let a vulnerable stdlib patch into a
release build, and `govulncheck` in CI is blocking, so renewing the pin is a
deliberate chore rather than something to loosen).

Frontend:
- `pnpm install`
- `pnpm run dev` — frontend (Vite) + backend (air) concurrently; or `pnpm run dev:client` / `pnpm run dev:server` separately
- `pnpm run build` — `tsc -b && vite build` → `dist/`
- `pnpm run typecheck` — `tsc -b` (both tsconfigs are `noEmit`)
- `pnpm run test` — Vitest (jsdom). Single file/pattern: `pnpm exec vitest run <pattern>`; watch: `pnpm run test:watch`
- `pnpm run lint` — ESLint. **Expected to be red**: `main` carries a large pre-existing backlog (~55 problems, mostly `@typescript-eslint/no-explicit-any`), and CI runs lint `continue-on-error`. Make *new* code lint clean; do not try to clear the backlog as a side task.

Backend (`cd backend`):
- `go build ./...` — dev build (uses the non-`prod` UI stub; frontend not embedded)
- `go test -race ./...` — tests. Single: `go test -race ./utils/... -run TestName`
- `go vet ./...` · `gofmt -l .`
- `make` — **release build** (`CGO_ENABLED=0 go build -o main -tags=prod main.go`). Requires `backend/ui/dist/` to exist (the built frontend, copied in). A clean checkout cannot `make` until you run the frontend build and copy `dist` → `backend/ui/dist`.

`docker build .` runs the full pipeline (frontend build → Go build with embedded UI → `scratch` image). CI is `.github/workflows/ci.yml`.

## Architecture (the parts that span multiple files)

**Dev-vs-release UI split.** `backend/ui/ui_prod.go` (`//go:build prod`) does `//go:embed dist`, serves the SPA, and rewrites `%BASE_PATH%`. `backend/ui/ui.go` (non-prod) is a no-op stub, so `go build ./...` compiles without the frontend present. Only `make` / `-tags=prod` embeds the UI. The Dockerfile builds the frontend first and copies `dist` into `backend/ui/dist` before the tagged Go build.

**The backend is a thin gateway to two Garage APIs:**
- **Admin API** (cluster / bucket / key management). `backend/utils/garage.go` — `Garage.Fetch(path, opts)` is the admin client and injects the admin bearer token. `backend/router/router.go` registers a few explicit routes, then a **catch-all `ProxyHandler`** (`backend/router/proxy.go`) that reverse-proxies *any unmatched* `/api/*` request to the Garage admin endpoint with the admin token attached. This is why the frontend calls Garage admin endpoints (`/v2/GetClusterStatus`, `/v2/GetBucketInfo`, …) directly — they fall through to the proxy. Garage's admin API is **v2** (`/v2/...`).
- **S3 API** (object browsing). `backend/router/browse.go` — `getS3Client(bucket)` builds an AWS SDK v2 S3 client using per-bucket credentials fetched from the admin API (`getBucketCredentials`, cached ~1h in `backend/utils/cache.go`). List / get / put / delete objects go through this client, not the proxy.

**Config resolution** (`backend/utils/garage.go`): the app reuses Garage's own `garage.toml` (`CONFIG_PATH`, default `/etc/garage.toml`) for `rpc_public_addr`, `admin_token`, S3 settings, etc., so it works alongside a Garage install with no separate config. Env vars override: `API_BASE_URL`, `API_ADMIN_KEY`, `S3_ENDPOINT_URL`, `S3_REGION`. `GetAdminEndpoint()` / `GetS3Endpoint()` / `GetAdminKey()` encapsulate that precedence.

**Buckets are addressed by global alias, not ID, in the browse/S3 path** — `getBucketCredentials` calls `GetBucketInfo?globalAlias=<name>`. A bucket with no global alias cannot be browsed; code and UI must handle that case.

**`backend/store/` — the user database, the only state this service owns.** SQLite via **`modernc.org/sqlite`**, which is **required, not a preference**: the release build is `CGO_ENABLED=0` onto a distroless/static base, and the cgo-based `mattn/go-sqlite3` cannot link there. (Note the registered driver name is `sqlite`, not `sqlite3`.) `store.Default()` is the process-wide singleton, installed by `main.go` alongside `utils.Session` / `utils.Garage`; handlers reach it that way and **must handle `nil`** (startup not finished ⇒ `store.ErrNoStore` ⇒ 500). `migrations` in `store.go` is **append-only** — each entry's index+1 is its recorded `schema_migrations` version, so editing a shipped entry means it silently never runs on existing installs. The pool is capped at `SetMaxOpenConns(1)`, which is what makes `CreateFirstAdmin`'s count-then-insert atomic; raising it without switching to `BEGIN IMMEDIATE` reintroduces that race. Location is `DB_PATH` (default `./data/garage-webui-ng.db`; `/data/garage-webui-ng.db` in the image, where `/data` is a declared volume owned by uid 65532 so the non-root process can write it). Startup is fail-fast: an unopenable database is `log.Fatalf`, not a degraded server.

**Auth is mandatory and session-based, backed by the user table.** `backend/middleware/auth.go` gates everything except an exact-match `isPublicPath` allowlist (`GET /auth/status`, `GET /setup/status`, `POST /setup`); `POST /auth/login` is registered on the *outer* mux in `router.go` and so bypasses the middleware chain entirely. A brand-new deployment has zero users and bootstraps through `POST /setup` (`backend/router/setup.go`), which auto-logs-in and then refuses with **409** forever — the emptiness check lives inside `store.CreateFirstAdmin`'s transaction. `AUTH_USER_PASS` / `AUTH_VIEWER_USER_PASS` are **import-once** (`store.ImportLegacyUsers`, only while the table is empty, hashes stored verbatim) and never consulted again. Roles are `admin` / `viewer`; the viewer boundary is the allowlist in `isViewerAllowed`. `/admin/*` (`backend/router/admin_users.go`) is guarded **twice, independently** — the middleware denies `/admin/` to a viewer *and* every handler calls `requireAdmin`; keep both. `middleware.CSRF` requires a double-submit `X-CSRF-Token` on every write, exempting only `POST /auth/login` and `POST /setup`. Sessions are `alexedwards/scs` (`backend/utils/session.go`) with `IdleTimeout` + `Lifetime` (`SESSION_IDLE_TIMEOUT_HOURS` / `SESSION_LIFETIME_HOURS`), in-memory storage, and `Renew` on every privilege change. Login is rate-limited 10/min/IP, a bucket shared with `POST /setup` and the current-password check in `ChangePassword`. Operator-facing reference: [`docs/authentication.md`](docs/authentication.md).

**Frontend** (`src/`): React 18 + TS + Vite. Data via **TanStack Query**; the `api` client (`src/lib/api.ts`) wraps `fetch` with `credentials: "include"`, attaches the `X-CSRF-Token` header read from the (deliberately non-`HttpOnly`) `csrf_token` cookie on *every* request so no caller can forget it on a write, and redirects to `/auth/login` on 401. Forms use react-hook-form + zod; UI is **daisyUI** (`react-daisyui`) + Tailwind with thin local wrappers in `src/components/ui/`. Routing is `react-router-dom` `createBrowserRouter` (`src/app/router.tsx`); `/setup` sits **outside** both layouts because each of them redirects there while `needsSetup` is true, so nesting it would loop. Pages live under `src/pages/{home,cluster,buckets,keys,settings,setup}`, each typically with a sibling `hooks.ts` (Query hooks) and `schema.ts`/`types.ts`. `BASE_PATH` (`src/lib/consts.ts`) supports mounting the UI under a path prefix, wired through both `ui_prod.go` and Vite.

## Conventions

- **Go handlers** are methods on empty structs (`type Buckets struct{}`), take `(w, r)`, and end in `utils.ResponseSuccess(w, data)` or `utils.ResponseError(w, err)`. `utils.ResponseError` does **not** stop the handler — always `return` after it. Wrap errors with `fmt.Errorf("...: %w", err)`; log via stdlib `log`. DTO/schema structs live in `backend/schema/` with both `json:` and `toml:` tags.
- **Frontend data hooks**: one hook per endpoint in the page's `hooks.ts`; query keys are arrays (`["browse", bucket, opts]`); mutation hooks spread `...options` last. The `@/` import alias maps to `src/`.
- **Go handler tests**: the `utils.Session` singleton needs the scs `LoadAndSave` middleware in the request context, or `Session.Get` panics. Test session-touching handlers by serving through `sessMgr.LoadAndSave(http.HandlerFunc(...))` rather than calling the handler directly. Use `httptest` + `t.Setenv`.
- **Never let a password or hash leave the process.** `store.User.PasswordHash` carries `json:"-"` — that tag is the single guarantee, and the store/router tests assert on **raw response bodies** that no bcrypt prefix ever appears. Handlers that accept a password replace the JSON decoder's error with a flat `"invalid request body"`, because the decoder can quote the body it failed on. Nothing logs a submitted credential.
- **Docs are part of an auth endpoint's definition of done.** Adding or changing an `/auth/*` or `/admin/*` route means updating the API table in [`docs/authentication.md`](docs/authentication.md); the README links there instead of duplicating it.

## `plans/` directory (untracked)

Holds an `/improve` advisor pass: numbered implementation plans (shipped in release 1.2.0), design/spike docs under `plans/design/`, and `plans/README.md` — an index of what shipped, findings considered-and-rejected, a dependency audit, and known gaps (including the lint backlog above). Useful background on *why* the code is shaped as it is; not part of the built artifact.
