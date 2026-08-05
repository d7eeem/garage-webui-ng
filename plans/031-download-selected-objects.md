# Plan 031: Download selected objects as a streamed ZIP

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it).
>
> **Base reset FIRST**: `git checkout -B advisor/031-download-selected main` then
> `git log --oneline -1` — MUST show `f9cd2c6` or newer, NOT `ee420fb`.
> SENTINEL: `grep -q "BulkDeleteObjects" backend/router/browse.go && test -f src/pages/buckets/manage/browse/bulk-actions.tsx && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P3 (feature — the obvious companion to bulk delete)
- **Effort**: M
- **Risk**: MED (streams a response the server cannot un-send; touches the viewer
  authorization boundary)
- **Depends on**: nothing hard, but **merge after 030** to keep frontend merges clean.
- **Category**: feature
- **Planned at**: commit `bcbf86c`, 2026-08-04

## Why this matters

The object browser can already **select many objects and delete them** — the
selection toolbar in `bulk-actions.tsx` offers exactly one action, *Delete
selected*. There is no way to get several objects out. Today the only download is
one file at a time via the per-row menu
(`object-actions.tsx:33` → `window.open(API_URL + object.url + "?dl=1")`).

This adds **Download selected**, producing a single ZIP.

### The three constraints that shape the design (resolved from the code — do not re-litigate)

1. **A native browser download cannot send a CSRF header.** `middleware.CSRF`
   requires `X-CSRF-Token` on every `POST`/`PUT`/`PATCH`/`DELETE`, and a plain
   navigation or form submit cannot set headers. So the archive itself must be
   fetched with a **GET**.
2. **The key list is too big for a URL.** Selections are capped at
   `maxListKeys = 1000` (`browse.go:477`) and keys can be long, so
   `GET …?keys=a&keys=b&…` would blow past practical URL limits.
3. **`fetch()` + `blob()` buffers the whole archive in the tab's memory.** For a
   bucket browser that is a tab-killer on a large selection.

Therefore: **two steps.** A `POST` (with the CSRF header, via the normal `api`
client) mints a short-lived token; the browser then navigates to a `GET` that
streams the ZIP straight to disk. Constant memory, native download, CSRF intact.

### Only objects are selectable

`object-list.tsx` renders prefixes (folders) at line ~123 **without** checkboxes;
the checkbox is only on object rows (line ~163). So this feature is
**objects-only** — there is no recursive folder download, and you must not add one.

## Current state (read before editing)

### `backend/router/browse.go` — the existing handler style to imitate (do NOT edit this one)

```go
func (b *Browse) BulkDeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var body struct {
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil { … }
	if body.Action != "delete" {
		utils.ResponseErrorStatus(w, fmt.Errorf("unsupported action %q", body.Action), http.StatusBadRequest)
		return
	}
	if len(body.Keys) == 0 { … }
	if len(body.Keys) > maxListKeys { … 400 … }

	client, err := getS3Client(bucket)
	…
```
`const maxListKeys = 1000` is at `browse.go:477`. `getS3Client(bucket)` returns a
configured S3 client. Handlers are methods on `type Browse struct{}` and must
`return` after `utils.ResponseError*`.

### `backend/utils/cache.go` — the TTL store for the token

```go
func InitCacheManager()
func (c *CacheManager) Set(key string, value interface{}, ttl time.Duration)
func (c *CacheManager) Get(key string) interface{}
```
`utils.Cache` is the process-wide instance (installed in `main.go`). Use it; do
not add a new store.

### `backend/router/router.go` — route registration

```go
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)
	router.HandleFunc("POST /browse/{bucket}", browse.BulkDeleteObjects)
```
**Route-collision hazard:** `GET /browse/{bucket}/{key...}` is a wildcard that
would swallow `GET /browse/{bucket}/archive`. Go 1.22's ServeMux prefers the more
specific pattern, but do **not** rely on that silently — the plan's test
(Step 6) asserts the archive route actually wins.

### `backend/middleware/auth.go` — the viewer boundary you must edit carefully

```go
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" || r.URL.Path == "/auth/change-password"
	}
	return false
```
A read-only **viewer must be able to download** (downloading is a read). The
token-mint step is a `POST`, so without a carve-out viewers get 403. Step 3 adds
exactly one exact-match path. Nothing else.

### `src/pages/buckets/manage/browse/bulk-actions.tsx` — the toolbar

Receives `{ bucketName, selected: Set<string>, setSelected }`, gates destructive
UI on `useAuth().canWrite`, uses `useDisclosure()` for the confirm modal, and
`toast` from `sonner` for feedback. Hooks live in `./hooks.ts`, one per endpoint,
query keys are arrays, mutations spread `...options` last.

### `src/lib/api.ts`

`API_URL` is `BASE_PATH + "/api"`; `api.post/get` attach the CSRF header and
`credentials: "include"` automatically.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Backend gates | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` | exit 0 |
| CGO-free | `cd backend && CGO_ENABLED=0 go build ./...` | exit 0 |
| Frontend | `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/router/browse.go` (mint action + archive handler)
- `backend/router/browse_test.go` (tests)
- `backend/router/router.go` (one route)
- `backend/middleware/auth.go` + `auth_test.go` (one viewer carve-out)
- `src/pages/buckets/manage/browse/hooks.ts` (one hook)
- `src/pages/buckets/manage/browse/bulk-actions.tsx` (the button)

**Out of scope** — do NOT touch:
- Recursive folder/prefix download (folders are not selectable).
- The delete path, `object-actions.tsx`, the single-object `?dl=1` route.
- `store/`, auth handlers, the CSRF middleware itself.
- Any client-side zipping library. The archive is built server-side.
- Resumable/parallel download machinery.

## Steps

### Step 1 — Mint a download token: `POST /browse/download-token`

Add a **new handler** `func (b *Browse) CreateDownloadToken(w http.ResponseWriter, r *http.Request)`
in `backend/router/browse.go`, on its **own route**. Do **not** extend
`BulkDeleteObjects`'s `action` dispatch and do not add an `action: "download"` —
`BulkDeleteObjects` must come out of this plan byte-identical. The separate route
is a **security requirement**, not a style preference: see the boxed warning in
Step 3 for why folding the mint into `POST /browse/{bucket}` would hand viewers
the delete endpoint.

Request body:

```go
var body struct {
	Bucket string   `json:"bucket"`
	Keys   []string `json:"keys"`
}
```

- Decode failure → 400; `body.Bucket == ""` → 400.
- Reject `len(Keys) == 0` → 400; `len(Keys) > maxListKeys` → 400. Reuse the
  existing message shape from `browse.go:317-318`:
  `fmt.Errorf("too many keys: %d (max %d)", len(body.Keys), maxListKeys)`.
- Generate 32 random bytes (`crypto/rand`), hex-encode → `token`.
- Store in `utils.Cache` under a namespaced key (e.g. `"dlzip:"+token`) with a
  **60-second TTL**, holding a struct of `{Bucket string; Keys []string; Username string}`.
  Take `Username` from `utils.Session.Get(r, "username")`.
- Respond `utils.ResponseSuccess(w, map[string]any{"token": token})`.

`BulkDeleteObjects` and its `action: "delete"` dispatch are **not touched by this
step** — `git diff backend/router/browse.go` must show only additions around the
new handler, with no hunk inside `BulkDeleteObjects`.

### Step 2 — Stream the archive (`GET /browse/{bucket}/archive`)

New handler `func (b *Browse) DownloadArchive(w http.ResponseWriter, r *http.Request)`:

1. `token := r.URL.Query().Get("token")`; empty → 400.
2. Look up in `utils.Cache`. Missing/expired → **404** `"download link expired"`.
   **Single-use:** overwrite the entry with an already-expired TTL (or store a
   `used` flag) immediately after a successful lookup, so a leaked URL cannot be
   replayed.
3. Verify the token's `Bucket` equals `r.PathValue("bucket")` **and** its
   `Username` equals the current session's username → else **403**. A token
   minted for one user/bucket must not work for another.
4. Set headers **before** writing any bytes:
   ```
   Content-Type: application/zip
   Content-Disposition: attachment; filename="<bucket>-objects.zip"
   Cache-Control: no-store
   ```
   Use the same safe-filename helper the single-object download already uses
   (`contentDispositionAttachment`) if it fits; otherwise sanitise the bucket name.
5. `zw := zip.NewWriter(w)` (`archive/zip`). For each key: `client.GetObject`,
   create the entry, `io.Copy`, close the object body. **Stream — never buffer a
   whole object in memory.**
6. **Entry names:** strip the longest common directory prefix across the selected
   keys, so selecting three files inside `assets/css/` yields `main.css`, not
   `assets/css/main.css`. This is collision-free by construction (keys that
   differ only below the common prefix keep their distinguishing path).
7. **Mid-stream failures.** Once the first byte is written the status is already
   200 and cannot be changed. Do **not** abort into a truncated archive: collect
   failures and, at the end, write a final `DOWNLOAD-ERRORS.txt` entry listing the
   keys that failed and why. Log server-side with `log.Printf`. Comment this
   clearly — it is the one place the handler cannot report an HTTP error.
8. `zw.Close()` at the end (its error is best-effort — the client may have
   disconnected).

Respect `r.Context()` on every S3 call so a cancelled download stops work.

### Step 3 — Let viewers mint a token

In `backend/middleware/auth.go`, extend the POST branch of `isViewerAllowed`:

```go
	// Downloading is a read. The archive endpoint is a GET (already allowed);
	// the token that authorises it is minted with a POST purely because the key
	// list is too large for a URL. It mutates nothing, so a read-only viewer
	// may call it. Exact match only — this must never become a prefix.
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" ||
			r.URL.Path == "/auth/change-password" ||
			r.URL.Path == "/browse/download-token"
	}
```

> **This changes the shape of the mint route.** Because `POST /browse/{bucket}`
> also serves *delete*, allowing that path wholesale would hand viewers the
> delete endpoint. **Therefore mint on a separate, non-mutating route:**
> register `POST /browse/download-token` (bucket supplied in the JSON body) and
> leave `POST /browse/{bucket}` for delete only. Step 1 is already written this
> way — this box explains *why*.
> If you find yourself allowing `POST /browse/{bucket}` for viewers, **STOP** —
> that is a privilege escalation.

### Step 4 — Routes

```go
	router.HandleFunc("POST /browse/download-token", browse.CreateDownloadToken)
	router.HandleFunc("GET /browse/{bucket}/archive", browse.DownloadArchive)
```
Register the archive route **before** the `{key...}` wildcard for clarity.

### Step 5 — Frontend

`hooks.ts` — one hook, mirroring `useBulkDelete`:
```ts
export const useDownloadToken = (bucketName: string, options?: …) =>
  useMutation({
    mutationFn: (keys: string[]) =>
      api.post<{ token: string }>("/browse/download-token", { body: { bucket: bucketName, keys } }),
    ...options,
  });
```

`bulk-actions.tsx` — add a **Download selected** button beside *Delete selected*:
- Visible to **everyone** (not gated on `canWrite` — viewers may download).
- On click: call the hook, then `window.location.href = API_URL + "/browse/" + encodeURIComponent(bucketName) + "/archive?token=" + token`.
  (A plain navigation, so the browser streams it to disk. Do **not** use
  `fetch`+`blob`.)
- Show a `toast.error` via the existing `handleError` on failure.
- Keep the selection intact after download (unlike delete, nothing was removed).

### Step 6 — Tests

`backend/router/browse_test.go`:
- **Route wins over the wildcard:** a request to `/browse/b/archive` reaches
  `DownloadArchive`, not `GetOneObject` (this is the collision hazard).
- Mint: 0 keys → 400; > `maxListKeys` → 400; valid → 200 with a non-empty token.
- Archive: missing token → 400; unknown/expired token → 404; token minted for
  bucket `a` used against bucket `b` → 403; token minted by user `x` used by
  user `y` → 403; **replay of a used token → 404**.
- Response headers on success include `Content-Type: application/zip` and a
  `Content-Disposition: attachment` filename.
- Common-prefix stripping: keys `p/q/a.txt`, `p/q/b.txt` → entries `a.txt`, `b.txt`.

`backend/middleware/auth_test.go`:
- viewer + `POST /browse/download-token` → **allowed**
- viewer + `POST /browse/some-bucket` (delete) → still **denied** ← the important one
- viewer + `GET /browse/b/archive` → allowed

**Verify**: `cd backend && go test -race ./...` → all `ok`.

### Step 7 — Gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && CGO_ENABLED=0 go build ./... && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
Commit on `advisor/031-download-selected`: `feat: download selected objects as a ZIP`

## Test plan

- Handler tests above are the contract; the **viewer-can-mint-but-not-delete**
  pair and the **token replay → 404** case are the security-critical ones.
- **Reviewer live verification** (advisor's job), against a real Garage:
  1. Select 2–3 objects → *Download selected* → a `.zip` lands, opens, and its
     entries match the selection with the common prefix stripped.
  2. Byte-compare one extracted entry against a direct single-object download.
  3. Select an object inside a nested prefix → entry name is sensible.
  4. Reusing the same archive URL a second time → **404**.
  5. Log in as a **viewer**: *Download selected* works; *Delete selected* is still
     unavailable and `POST /browse/<bucket>` still returns 403.
  6. A token minted against bucket A, used on bucket B → 403.

## Done criteria

- [ ] Backend gates + `CGO_ENABLED=0` build + `go test -race ./...` all exit 0
- [ ] Frontend `typecheck` / `test` / `build` exit 0
- [ ] `grep -n "archive/zip" backend/router/browse.go` → present
- [ ] `grep -n "download-token" backend/middleware/auth.go` → present (exact match, not a prefix)
- [ ] `grep -c "POST /browse/{bucket}\"" backend/router/router.go` → still `1` (delete route unchanged)
- [ ] `grep -rn "blob()" src/pages/buckets/manage/browse/` → **nothing** (no in-memory buffering)
- [ ] `git diff --name-only main` shows **only** in-scope files. (Compare against
      `main`, **not** against the `Planned at` SHA — three unrelated merges have
      landed on `main` since this plan was stamped, so a `bcbf86c..HEAD` diff would
      list their files too and this criterion would fail through no fault of yours.)
- [ ] `git diff main -- backend/router/browse.go` contains **no hunk inside
      `BulkDeleteObjects`** — the delete handler is untouched

## STOP conditions

- You allow `POST /browse/{bucket}` for viewers — that hands them **delete**;
  privilege escalation. STOP.
- The archive route is shadowed by `GET /browse/{bucket}/{key...}` and you cannot
  make the specific route win — report rather than renaming the wildcard.
- You need a client-side zip library, or `fetch`+`blob` — STOP; that defeats the
  streaming design.
- Streaming requires buffering an entire object in memory to work — STOP.

## Maintenance notes

- **The status code is committed at the first byte.** Anything that can fail must
  be checked *before* `zip.NewWriter` starts writing; failures after that can only
  be reported inside the archive (`DOWNLOAD-ERRORS.txt`). Keep that comment.
- **The token is a bearer credential** — short TTL, single-use, and bound to both
  bucket and username. Loosening any of those three makes an archive URL
  shareable, which the presigned-share feature already covers deliberately and
  this one must not.
- **`POST /browse/download-token` is deliberately a separate route** from
  `POST /browse/{bucket}` so the viewer carve-out cannot leak the delete action.
  Never merge them.
- Folders are not selectable, so there is no recursive download. If prefixes ever
  become selectable, this handler needs a listing step and a new size guard.
