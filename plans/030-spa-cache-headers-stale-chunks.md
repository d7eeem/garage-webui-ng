# Plan 030: Fix stale-chunk breakage after upgrade (cache headers, asset 404s, preload recovery)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it).
>
> **Base reset FIRST**: `git checkout -B advisor/030-spa-cache-headers main` then
> `git log --oneline -1`.
> SENTINEL (**plan 028 must already be merged** — it touches the frontend too):
> `test -f src/components/ui/menu.tsx && test -f backend/ui/ui_prod.go && echo BASE_OK`
> MUST print `BASE_OK`. If `menu.tsx` is missing, plan 028 has not landed — STOP.

## Status

- **Priority**: P1 (reproduced on a live deployment; breaks the UI for every open
  tab on every upgrade)
- **Effort**: S–M
- **Risk**: MED (touches the static-file path that serves the whole UI; a mistake
  makes the app unreachable rather than merely stale)
- **Depends on**: **028** (both edit frontend files; 028 first avoids a conflict).
- **Category**: bug / operations
- **Planned at**: commit `4f9d4db`, 2026-08-04

## Why this matters

Reported from a live instance immediately after upgrading the binary:

```
Unexpected Application Error!
error loading dynamically imported module: https://<host>/assets/page-Bb-knZwX.js
```

Every route in `src/app/router.tsx` is a lazy chunk (`lazy(() => import(…))`, 8 of
them), so the built `index.html` points at content-hashed files like
`page-Bb-knZwX.js`. When the binary is upgraded, the embedded `dist/` contains
**new hashes and the old files are gone**. Any browser still holding the previous
`index.html` requests a chunk that no longer exists.

**That alone would be a recoverable 404. What makes it a hard error is the
server's fallback.** In `backend/ui/ui_prod.go`:

```go
		_path := path.Clean(r.URL.Path)[1:]

		// Rewrite non-existing paths to index.html
		if _, err := fs.Stat(distFs, _path); err != nil {
			index, _ := fs.ReadFile(distFs, "index.html")
			…
			w.Header().Add("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(html))
			return
		}
```

A request for a **missing `/assets/*.js` returns `index.html` as `text/html` with
status 200**. The browser asked for a JavaScript module and received an HTML
document, which is exactly the error above. The SPA history fallback is correct
for *navigation* routes and wrong for hashed assets.

Three defects, all fixed here:

1. **Missing assets fall through to `index.html` with 200** instead of 404.
2. **No `Cache-Control` anywhere** — neither on the HTML fallback nor on assets.
   Caching is left to browser heuristics and to whatever reverse proxy fronts the
   deployment, so a stale `index.html` can persist indefinitely.
3. **No client-side recovery.** Vite emits a `vite:preloadError` event for
   precisely this case; nothing listens, so the user lands on the error boundary
   with no way forward but a manual hard-reload.

## Current state

### `backend/ui/ui_prod.go` — the whole file is ~60 lines; read it before editing

Relevant shape (build-tagged `prod`; the non-prod `ui.go` is a no-op stub):

```go
//go:embed dist
var embeddedFs embed.FS

func ServeUI(mux *http.ServeMux) {
	distFs, _ := fs.Sub(embeddedFs, "dist")
	fileServer := http.FileServer(http.FS(distFs))
	basePath := os.Getenv("BASE_PATH")

	mux.Handle(basePath+"/", http.StripPrefix(basePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_path := path.Clean(r.URL.Path)[1:]

		// Rewrite non-existing paths to index.html      ← defect 1 lives here
		if _, err := fs.Stat(distFs, _path); err != nil { … }

		// Add prefix to each /assets strings in js
		if len(basePath) > 0 && strings.HasSuffix(_path, ".js") { … }

		fileServer.ServeHTTP(w, r)
	})))
}
```

Note the existing `BASE_PATH` rewriting branch — a `.js` file **is** read and
rewritten when `BASE_PATH` is set. Preserve that behaviour exactly; only add
headers to it.

### `src/app/app.tsx` — where a global listener belongs

```tsx
const App = () => {
  const [queryClient] = useState(() => new QueryClient());
  return (
    <PageContextProvider>
      <QueryClientProvider client={queryClient}>
        <ErrorBoundary>
          <Router />
        </ErrorBoundary>
      </QueryClientProvider>
      <Toaster richColors />
      <ThemeProvider />
    </PageContextProvider>
  );
};
```
Entry point is `src/main.tsx` (referenced by `index.html:23`).

### `src/app/router.tsx` — the 8 lazy chunks

```tsx
const LoginPage = lazy(() => import("@/pages/auth/login"));
const SetupPage = lazy(() => import("@/pages/setup/page"));
const ClusterPage = lazy(() => import("@/pages/cluster/page"));
const HomePage = lazy(() => import("@/pages/home/page"));
const BucketsPage = lazy(() => import("@/pages/buckets/page"));
const ManageBucketPage = lazy(() => import("@/pages/buckets/manage/page"));
const KeysPage = lazy(() => import("@/pages/keys/page"));
const SettingsPage = lazy(() => import("@/pages/settings/page"));
```

### Go conventions

Handlers use `utils.ResponseError*`, but `ui_prod.go` is a plain static handler
and should stay one — use `http.Error` / `w.WriteHeader` directly there, matching
the file's existing style. `backend/ui/ui.go` (non-prod) must keep compiling.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Backend gates | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` | exit 0 |
| **Prod build (this file is behind a build tag!)** | `cd backend && CGO_ENABLED=0 go build -tags=prod ./...` | exit 0 — requires `backend/ui/dist` to exist |
| Frontend | `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` | exit 0 |

> `ui_prod.go` only compiles under `-tags=prod`, and that needs the built frontend
> copied in: `npx pnpm@9 run build && rm -rf backend/ui/dist && cp -r dist backend/ui/dist`.
> **A plain `go build ./...` will not compile your changes** — you must run the
> tagged build or you are testing nothing.

## Scope

**In scope**:
- `backend/ui/ui_prod.go`
- `backend/ui/ui_prod_test.go` (create — needs the `prod` tag)
- `src/app/app.tsx` (or a small `src/lib/preload-recovery.ts` it imports)
- `docs/UPGRADING.md` (one troubleshooting row)

**Out of scope** — do NOT touch:
- `backend/ui/ui.go` (the non-prod stub) beyond keeping it compiling.
- The router, the error boundary's rendering, any page component.
- Vite's build config / chunk naming. Content hashing already works; do not
  disable it, and do not add `manualChunks`.
- Anything in `backend/router/`, `store/`, `middleware/`.

## Steps

### Step 1 — Serve missing assets as 404, not HTML

In `ui_prod.go`, before the index fallback, classify the request. The SPA history
fallback must apply **only to navigation requests**:

```go
// Requests for build artefacts must never fall through to index.html. A hashed
// chunk that no longer exists (an upgraded binary, a browser holding the old
// index.html) has to fail as a 404: returning HTML with status 200 makes the
// browser try to parse a document as a JavaScript module, which surfaces as
// "error loading dynamically imported module" and dead-ends the app.
func isBuildAsset(p string) bool {
	if strings.HasPrefix(p, "assets/") {
		return true
	}
	switch path.Ext(p) {
	case ".js", ".mjs", ".css", ".map", ".json", ".woff", ".woff2", ".ttf", ".svg", ".png", ".ico", ".webmanifest":
		return true
	}
	return false
}
```

In the missing-path branch: if `isBuildAsset(_path)` → `http.NotFound(w, r)` and
return. Otherwise serve `index.html` as today.

**Verify**: after the tagged build, a request for `/assets/does-not-exist.js`
returns **404**, and `/buckets/anything` still returns the SPA HTML with 200.

### Step 2 — Cache headers

Add a small helper and apply it on every write path in this handler:

- **`index.html`** (the fallback, and a direct request for `/` or `/index.html`):
  ```
  Cache-Control: no-cache
  ```
  `no-cache` means "revalidate before reuse" — not "don't store". This is the
  manifest that names the hashed chunks, so it must never be served stale. Do not
  use `no-store` (it defeats revalidation and costs a full re-download each load).
- **Content-hashed assets under `assets/`**:
  ```
  Cache-Control: public, max-age=31536000, immutable
  ```
  Safe forever: Vite changes the filename whenever the content changes.
- **Everything else** (`favicon.ico`, `site.webmanifest`, files at the root that
  are *not* content-hashed): `Cache-Control: public, max-age=3600`.

Apply the header **before** `w.WriteHeader(...)` in every branch, including the
existing `BASE_PATH` `.js`-rewriting branch — a rewritten chunk is still a hashed
asset and should still be immutable.

> Keep `Content-Type` handling as it is. The existing code sets it explicitly on
> the two hand-written branches and lets `http.FileServer` sniff for the rest.

**Verify**: `curl -sI …/assets/<real-hashed-file>.js | grep -i cache-control`
→ `public, max-age=31536000, immutable`; `curl -sI …/ | grep -i cache-control`
→ `no-cache`.

### Step 3 — Client-side recovery from a stale chunk

Vite dispatches a `vite:preloadError` event on `window` when a dynamic import
fails. Add a listener (in `src/app/app.tsx`, or a tiny module it imports) that
reloads **once**:

```ts
/**
 * After an upgrade, a tab still running the previous build asks for chunk
 * filenames that no longer exist. Vite raises `vite:preloadError`; reloading
 * fetches the new index.html and its new chunk names.
 *
 * Guarded by a sessionStorage flag so a genuinely broken deployment cannot put
 * the browser into a reload loop — the second failure falls through to the
 * error boundary, which is the honest outcome.
 */
const RELOAD_FLAG = "gwui:chunk-reload";

window.addEventListener("vite:preloadError", (event) => {
  if (sessionStorage.getItem(RELOAD_FLAG)) return;   // already tried once
  sessionStorage.setItem(RELOAD_FLAG, "1");
  event.preventDefault();       // stop the unhandled rejection
  window.location.reload();
});
```
Clear the flag on a successful load (e.g. at module top level, after the app
mounts) so a later upgrade can recover again.

**The loop guard is not optional** — without it, a deployment whose assets are
genuinely missing would reload forever.

**Verify**: `npx pnpm@9 run typecheck` exits 0.

### Step 4 — Tests

Create `backend/ui/ui_prod_test.go` with the **`//go:build prod`** tag (mirroring
`ui_prod.go`; otherwise it never runs). Serve through a `httptest` server built on
`ServeUI` and assert:

- `GET /assets/definitely-missing-xyz.js` → **404**, and the body is **not** HTML
  (`!strings.Contains(body, "<!doctype")`).
- `GET /some/spa/route` → **200** with HTML (the fallback still works).
- A real file under `assets/` → 200 with
  `Cache-Control: public, max-age=31536000, immutable`.
- `GET /` → 200 with `Cache-Control: no-cache`.

> These tests need `backend/ui/dist` present. That is a build artefact, not
> committed — the test must **skip cleanly** when it is absent:
> `if _, err := fs.Stat(embeddedFs, "dist/index.html"); err != nil { t.Skip("frontend not built") }`.
> Say in your report whether the tests actually ran or skipped.

**Verify**: `cd backend && go test -tags=prod -race ./ui/...` → `ok` (or an
explicit skip if `dist` is absent — build it first so they really run).

### Step 5 — Docs

`docs/UPGRADING.md` — add a troubleshooting row:

| Symptom | Cause | Fix |
|---|---|---|
| `error loading dynamically imported module` right after an upgrade | A tab (or a caching proxy) is holding the previous `index.html`, which names chunk files the new build no longer contains | Hard-reload (`Cmd/Ctrl+Shift+R`). The app now reloads itself once automatically; if a proxy caches `index.html`, configure it to honour `Cache-Control: no-cache` |

### Step 6 — Full gate sweep

```
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
rm -rf backend/ui/dist && cp -r dist backend/ui/dist
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
cd backend && CGO_ENABLED=0 go build -tags=prod ./... && go test -tags=prod -race ./ui/...
```
Commit on `advisor/030-spa-cache-headers`:
`fix: 404 missing build assets and set SPA cache headers`

## Test plan

- The `prod`-tagged handler tests are the contract; the **404-not-HTML** assertion
  is the one that reproduces the reported bug.
- **Reviewer live verification** (advisor's job) — reproduce the original failure
  and prove it is fixed:
  1. Build and run the prod binary; load the UI; note a chunk name from
     DevTools → Network.
  2. `curl -sI http://…/assets/<that-chunk>` → `immutable`; `curl -sI http://…/`
     → `no-cache`.
  3. `curl -s -o /dev/null -w '%{http_code}' http://…/assets/page-DOESNOTEXIST.js`
     → **404** (before this plan: 200 + HTML).
  4. With a tab open, rebuild the frontend with a change (so hashes rotate),
     restart the binary, and navigate in the still-open tab: it should reload
     itself once and land on the working app, not the error boundary.

## Done criteria

- [ ] Frontend `typecheck` / `test` / `build` exit 0
- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` exit 0
- [ ] `cd backend && CGO_ENABLED=0 go build -tags=prod ./...` exits 0
- [ ] `go test -tags=prod -race ./ui/...` passes (report whether it ran or skipped)
- [ ] `grep -n "isBuildAsset" backend/ui/ui_prod.go` → present
- [ ] `grep -c "immutable" backend/ui/ui_prod.go` → ≥ 1
- [ ] `grep -n "vite:preloadError" src/` → present, **with** a sessionStorage loop guard
- [ ] `git diff --name-only <028-merge-sha>..HEAD` shows only in-scope files

## STOP conditions

- SENTINEL fails (028 not merged).
- The SPA stops serving on unknown routes (deep links like `/buckets/<id>` must
  still return `index.html`) — that would break navigation entirely; STOP.
- You need to disable Vite content hashing or add `manualChunks` — out of scope;
  hashing is what makes `immutable` safe.
- The preload listener causes a reload loop in testing — the guard is wrong; STOP
  rather than shipping it.

## Maintenance notes

- **The fallback rule in one line:** unknown *route* → `index.html`; unknown
  *asset* → 404. Any future change to `ServeUI` must preserve that split, or this
  bug returns in exactly the same shape.
- **`no-cache` on `index.html` is load-bearing.** It is the manifest naming every
  hashed chunk. If a reverse proxy in front (Caddy/nginx/Traefik) overrides it
  with its own caching, upgrades break again — that is a deployment-side
  responsibility worth mentioning to operators.
- **`immutable` is only safe because Vite content-hashes.** If chunk names ever
  become stable across builds, that header becomes a footgun.
- The reload guard uses `sessionStorage`, so it resets per tab-session. That is
  deliberate: a persistent flag would stop recovery working on the *next* upgrade.
