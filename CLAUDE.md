# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**Garage WebUI-NG** — admin web UI for [Garage](https://garagehq.deuxfleurs.fr/) (self-hosted, S3-compatible distributed object storage). A **Go backend + React/TypeScript frontend**, shipped as a single binary (the Go binary embeds the built frontend via `//go:embed`) or a Docker image. The backend holds no state of its own — it is a gateway to a running Garage cluster.

- **Go module path:** `github.com/d7eeem/garage-webui-ng` (imports look like `github.com/d7eeem/garage-webui-ng/utils`). npm package: `garage-webui-ng`. Docker image: `ghcr.io/d7eeem/garage-webui-ng`.
- A next-generation fork of [garage-webui](https://github.com/khairul169/garage-webui) (© 2024 Khairul Hidayat, MIT). Keep the upstream attribution intact.

## Commands

Frontend lives at the repo root (package manager: **pnpm**); backend under `backend/` (**Go 1.23+**).

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

**Auth is optional and session-based.** Set `AUTH_USER_PASS=username:<bcrypt-hash>` to enable it (`backend/middleware/auth.go`; sessions via `alexedwards/scs` in `backend/utils/session.go`). When unset, the entire API — including the admin-token-injecting proxy — is open, so deployments rely on network isolation. Login renews the session token and is rate-limited (`backend/router/auth.go`).

**Frontend** (`src/`): React 18 + TS + Vite. Data via **TanStack Query**; the `api` client (`src/lib/api.ts`) wraps `fetch` with `credentials: "include"` and redirects to `/auth/login` on 401. Forms use react-hook-form + zod; UI is **daisyUI** (`react-daisyui`) + Tailwind with thin local wrappers in `src/components/ui/`. Routing is `react-router-dom` `createBrowserRouter` (`src/app/router.tsx`). Pages live under `src/pages/{home,cluster,buckets,keys}`, each typically with a sibling `hooks.ts` (Query hooks) and `schema.ts`/`types.ts`. `BASE_PATH` (`src/lib/consts.ts`) supports mounting the UI under a path prefix, wired through both `ui_prod.go` and Vite.

## Conventions

- **Go handlers** are methods on empty structs (`type Buckets struct{}`), take `(w, r)`, and end in `utils.ResponseSuccess(w, data)` or `utils.ResponseError(w, err)`. `utils.ResponseError` does **not** stop the handler — always `return` after it. Wrap errors with `fmt.Errorf("...: %w", err)`; log via stdlib `log`. DTO/schema structs live in `backend/schema/` with both `json:` and `toml:` tags.
- **Frontend data hooks**: one hook per endpoint in the page's `hooks.ts`; query keys are arrays (`["browse", bucket, opts]`); mutation hooks spread `...options` last. The `@/` import alias maps to `src/`.
- **Go handler tests**: the `utils.Session` singleton needs the scs `LoadAndSave` middleware in the request context, or `Session.Get` panics. Test session-touching handlers by serving through `sessMgr.LoadAndSave(http.HandlerFunc(...))` rather than calling the handler directly. Use `httptest` + `t.Setenv`.

## `plans/` directory (untracked)

Holds an `/improve` advisor pass: numbered implementation plans (shipped in release 1.2.0), design/spike docs under `plans/design/`, and `plans/README.md` — an index of what shipped, findings considered-and-rejected, a dependency audit, and known gaps (including the lint backlog above). Useful background on *why* the code is shaped as it is; not part of the built artifact.
