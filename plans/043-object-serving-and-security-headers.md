# Plan 043: Stop serving object bodies as executable documents on the console's origin

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- backend/router/browse.go backend/utils/image.go \
>   backend/ui/ui_prod.go backend/main.go backend/middleware/ index.html src/lib/consts.ts
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — changes what the browser is allowed to do on every page and how every object body is served. Both are easy to get subtly wrong; the steps are ordered so each is independently verifiable.
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `8e0606d` (v3.4.1), 2026-08-10

## Why this matters

`GetOneObject` copies an object's **stored** `Content-Type` verbatim into the
response and streams the body, with no `X-Content-Type-Options` and no
Content-Security-Policy anywhere in the repo. `object-list.tsx:82` opens objects
with `window.open(API_URL + object.url + "?view=1")` — **same origin as the
admin console**.

So an object whose stored content type is an HTML-ish type renders as a
*document on the console's origin*. Script running there sits inside the trust
boundary of the whole application: it can drive every authenticated API call
with the user's session cookie, and it can read the `csrf_token` cookie, which
is deliberately **not** `HttpOnly` because the frontend must read it for the
double-submit check. The catch-all proxy then attaches the Garage **admin
token** to those calls server-side.

The content type is caller-controlled. `PutObject` takes it from the multipart
part header, and any party with S3 write access to a bucket can set it directly
through Garage's own API. The realistic path is: someone with write access to
any bucket plants an object; an admin later previews it.

This requires an authenticated session to trigger — a read-only viewer is enough
to fire it on themselves, and an admin previewing a planted object is the
escalation. It has shipped in **v3.2.0, v3.3.0, v3.4.0 and v3.4.1**.

Repo-wide greps for `nosniff`, `Content-Security-Policy`, `X-Frame-Options` and
`Referrer-Policy` across `backend/`, `src/`, `index.html`, `Dockerfile` and
`docker-compose.yml` return **zero** matches.

## Current state

### `backend/router/browse.go` — `GetOneObject`, the inline path, verbatim

```go
	if download {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))
	} else if thumbnail {
		body, err := io.ReadAll(object.Body)
		…
		thumb, err := utils.CreateThumbnailImage(body, 64, 64)
		…
		w.Header().Set("Content-Type", "image/png")
		w.Write(thumb)
		return
	}

	w.Header().Set("Cache-Control", "max-age=86400")
	w.Header().Set("Last-Modified", object.LastModified.Format(time.RFC1123))

	if object.ContentType != nil {
		w.Header().Set("Content-Type", *object.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	…
	_, err = io.Copy(w, object.Body)
```

Three request shapes reach this handler, decided by query parameters read near
the top: `?dl=1` (download), `?thumb=1` (thumbnail), `?view=1` (inline preview),
and no-parameter (a `HeadObject` metadata call that returns JSON and never
reaches this code).

`?dl=1` already sets `Content-Disposition: attachment` — **that path is already
safe and must keep working unchanged.**

### `backend/utils/image.go` — the thumbnail lies about its type

```go
	thumb := resize.Thumbnail(width, height, img, resize.NearestNeighbor)
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, thumb, nil); err != nil {
```

It encodes **JPEG**, but the handler declares `image/png`. This is currently
harmless because browsers sniff images — but you are about to add `nosniff`,
which is exactly the header that stops them doing so. **Fixing this is a
prerequisite, not an optional tidy-up**; skip it and every thumbnail breaks.

### `index.html` — an inline `<script>` that blocks a strict CSP

```html
    <script>
      window.__BASE_PATH = "%BASE_PATH%";
    </script>
    <script type="module" src="/src/main.tsx"></script>
```

Confirmed present in `dist/index.html` after a build, so it ships. A CSP with
`script-src 'self'` blocks it, and the SPA then never receives its base path.

### `src/lib/consts.ts` — the consumer, whole file

```ts
// consts.ts

export const BASE_PATH =
  (import.meta.env.PROD ? window.__BASE_PATH : null) ||
  import.meta.env.VITE_BASE_PATH ||
  "";
```

`window.__BASE_PATH` is typed in `src/global.d.ts`.

### `backend/ui/ui_prod.go` — where the placeholder is substituted

The SPA-fallback branch does `strings.ReplaceAll(html, "%BASE_PATH%", basePath)`.
But a **direct** request for `/index.html` takes a different branch:

```go
		// A direct hit on index.html is the manifest too — never cache it.
		if _path == "index.html" {
			w.Header().Set("Cache-Control", cacheControlIndex)
		} else {
			w.Header().Set("Cache-Control", cacheControlFor(_path))
		}

		fileServer.ServeHTTP(w, r)
```

`fileServer` serves the raw embedded file — **no substitution**. So on that one
path the placeholder reaches the browser literally. Step 4 adds a guard for it.

### What a CSP must tolerate — measured, not assumed

- **No external resources at all.** Greps for `https://`, `fonts.googleapis`, `cdn.` across `index.html` and `src/**` (css and tsx) return nothing. `default-src 'self'` is viable.
- **Inline `style` attributes are required.** `src/components/ui/menu.tsx:232` sets positioning from `@floating-ui` and `src/components/containers/upload-card.tsx:154` sets `zIndex` from `Z_LAYERS`. Floating-ui *cannot* work without inline positioning styles. So `style-src` needs `'unsafe-inline'`. That is an accepted trade: style injection is far less dangerous than script injection, and the script side stays strict.
- **Images**: same-origin plus `data:` (Vite inlines small assets).

### Middleware wiring

`backend/router/router.go` ends with:
```go
	mux.Handle("/", middleware.AuditLog(middleware.CSRF(middleware.AuthMiddleware(router))))
```
and `backend/main.go` builds the outer mux, calls `ui.ServeUI(mux)`, and wraps
everything in `sessionMgr.LoadAndSave(mux)`. That outermost wrap is the one
place that sees **both** API and UI responses.

### Conventions

- **Go handlers**: methods on empty structs, `(w, r)`, ending in `utils.ResponseSuccess` / `utils.ResponseError`. **`utils.ResponseError` does NOT stop the handler — always `return` after it.**
- Middleware lives in `backend/middleware/`, each file one concern, shaped `func Name(next http.Handler) http.Handler`. **Read `backend/middleware/csrf.go` before writing Step 3** — it is the closest exemplar and documents its own security reasoning in comments, which this plan should match.
- Env reads: `utils.GetEnv(name, default)`.
- Go tests: plain `testing`, table-driven; `httptest` for handlers. `backend/middleware/csrf_test.go` is the shape to copy.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems; CI runs it `continue-on-error`). Make new code clean; do not clear the backlog.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Frontend tests | `pnpm run test` | all pass |
| Frontend build | `pnpm run build` | exit 0 |
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

`pnpm` may not be on PATH; it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. Do not substitute `npm`. If `go` is not on PATH, use the pinned
toolchain: `docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(Debian-based — `-race` needs gcc; `git` is unusable inside it).

## Scope

**In scope:**
- `backend/utils/image.go` — Step 1
- `backend/router/browse.go` — Steps 1–2
- `backend/router/browse_test.go` — Step 2 tests
- `backend/middleware/headers.go` (**create**) — Step 3
- `backend/middleware/headers_test.go` (**create**)
- `backend/main.go` — wire the middleware
- `index.html`, `src/lib/consts.ts`, `src/global.d.ts` — Step 4
- `src/lib/consts.test.ts` (**create**) — Step 4 test
- `README.md` — a short security note

**Out of scope — do NOT touch:**
- `middleware/csrf.go`, `middleware/auth.go`, `middleware/audit.go` — the existing security boundaries are correct and independently reviewed. You are **adding** a layer, not editing those.
- The `?dl=1` download path's `Content-Disposition` — already correct.
- `PutObject` / `resolveUploadContentType` — this plan does not change what gets *stored*, only how it is *served*. Sanitising on upload would not protect objects already in a bucket or written directly through Garage's S3 API, which is why serving is the right layer.
- The catch-all proxy, session handling, the S3 client.
- The thumbnail's unbounded `io.ReadAll` (a separate known finding) — you are only fixing its declared content type here. Do not expand into size limits.
- `Cache-Control` on object responses (a separate known finding).
- Any *removal* of the non-`HttpOnly` `csrf_token` cookie — that is the double-submit design and is deliberate.

## Git workflow

- Branch: `advisor/043-object-serving-and-security-headers` from your given base.
- Conventional commits, e.g. `fix(security): serve object bodies non-inline and add response hardening headers`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Make the thumbnail's declared type true (prerequisite)

`utils.CreateThumbnailImage` encodes JPEG; the handler declares `image/png`.
Once Step 3 adds `nosniff`, a false type is no longer harmless.

Pick **one** and be consistent — prefer changing the header, since JPEG is the
better format for photo thumbnails and re-encoding to PNG would grow them:

- In `backend/router/browse.go`'s thumbnail branch, change
  `w.Header().Set("Content-Type", "image/png")` → `"image/jpeg"`.
- Add a doc comment on `CreateThumbnailImage` in `backend/utils/image.go`
  stating it returns **JPEG** bytes, so the next caller does not re-introduce the
  mismatch.

Add `backend/utils/image_test.go` with a table test: a generated 2×2 PNG, JPEG
and GIF each produce output whose first bytes are the **JPEG magic**
(`0xFF 0xD8 0xFF`) and which decodes at ≤64×64; a garbage input returns an error
rather than panicking.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go test -race ./utils/ -run TestCreateThumbnailImage -v
```
→ clean, `PASS`.

### Step 2: Serve object bodies non-inline unless they are provably safe

In `GetOneObject`, for the **view** path (not `?dl=1`, not `?thumb=1`):

Add a package-level allowlist and helper near the other helpers at the bottom of
`browse.go`:

```go
// inlineSafeContentTypes are the only stored content types we will let a
// browser render in place on this application's own origin.
//
// Everything else is served as an attachment, because an object body is
// caller-controlled data: anyone with S3 write access to a bucket chooses its
// content type, and an HTML-ish type rendered here would execute script inside
// the console's origin — able to drive authenticated API calls and read the
// deliberately non-HttpOnly csrf_token cookie. Note SVG is NOT here: it is an
// XML document that can carry <script>.
var inlineSafeContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
	"image/bmp":  true,
	"image/x-icon": true,
	"image/vnd.microsoft.icon": true,
	"text/plain": true,
	"application/pdf": true,
	"video/mp4": true,
	"video/webm": true,
	"audio/mpeg": true,
	"audio/ogg": true,
	"audio/wav": true,
}

// isInlineSafe reports whether a stored content type may be rendered inline.
// The type is matched on its bare form: parameters (charset, boundary) are
// stripped and case is normalised, so "TEXT/PLAIN; charset=utf-8" matches.
func isInlineSafe(contentType string) bool { … }
```

Implement `isInlineSafe` with `mime.ParseMediaType` (stdlib; `mime` is already
imported in this file for `contentDispositionAttachment`). On a parse error,
return **false** — fail closed.

Then in the view path, replace the bare content-type assignment with:

```go
	stored := ""
	if object.ContentType != nil {
		stored = *object.ContentType
	}

	// Always: never let the browser second-guess the type we send.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if isInlineSafe(stored) {
		w.Header().Set("Content-Type", stored)
	} else {
		// Unknown or scriptable: hand it to the user as a file rather than
		// rendering it on this origin.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))
	}

	// Defence in depth: even a type on the allowlist is served with an empty
	// origin and no script execution, so a mislabelled body cannot act.
	w.Header().Set("Content-Security-Policy", "sandbox")
```

Also set `X-Content-Type-Options: nosniff` on the **thumbnail** and **download**
branches. Do not otherwise change them.

**On `Content-Security-Policy: sandbox`** — an empty `sandbox` directive gives
the response a unique opaque origin and blocks script, forms and plugins. It is
the strongest available control on a response you are still willing to render.
Verify PDFs still open for you in Step 6's manual check; if `sandbox` breaks
PDF viewing in a browser you care about, report it rather than silently dropping
the header.

**Tests** in `backend/router/browse_test.go`, table-driven, modelled on
`TestNormalizeListLimit`. Test `isInlineSafe` as a pure function — do **not**
attempt a full handler test; this package tests helpers, and the S3 fixture
needed for a body-serving test is disproportionate here.

Cases: `image/png` → true; `text/plain; charset=utf-8` → true (parameters
stripped); `TEXT/PLAIN` → true (case); **`text/html` → false**;
**`image/svg+xml` → false** (the one people get wrong — SVG carries script);
`application/xhtml+xml` → false; `application/javascript` → false;
`""` → false; `not/a/valid/type` → false; `application/octet-stream` → false.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./router/ -run TestIsInlineSafe -v
```
→ clean, `PASS`, all subtests.

### Step 3: Add a response-hardening middleware

Create `backend/middleware/headers.go`. Read `backend/middleware/csrf.go` first
and match its commenting register — it explains *why* each control exists, which
is what makes these files maintainable.

```go
// SecurityHeaders sets response hardening headers on every response, API and
// SPA alike.
//
// The CSP is deliberately strict on script-src and permissive on style-src:
// @floating-ui positions menus with inline style attributes and cannot work
// without them, whereas nothing in this application needs inline script. Style
// injection is a cosmetic risk; script injection is a session-takeover risk.
//
// frame-ancestors 'none' is what stops this console being framed by another
// site — it supersedes X-Frame-Options, which is sent too for older browsers.
func SecurityHeaders(next http.Handler) http.Handler { … }
```

Set:

| Header | Value |
|---|---|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `no-referrer` |
| `Content-Security-Policy` | see below |

CSP value:

```
default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'
```

**Two rules the implementation must honour:**

1. **Do not overwrite a `Content-Security-Policy` a handler already set.** Step 2
   sets `sandbox` on object bodies, and that must win. Check
   `w.Header().Get("Content-Security-Policy") == ""` before setting. Since
   middleware runs *before* the handler, the practical way is to set the
   app-wide CSP here and have Step 2's handler **overwrite** it — which it does,
   because `Set` replaces. Confirm by test rather than by reasoning: assert an
   object-body response carries `sandbox` and not the app policy.
2. **`Referrer-Policy: no-referrer` matters concretely here**, not just as
   hygiene: `bulk-actions.tsx` puts the ZIP download token in a query string on
   a top-level navigation, so without this the token would leak in the
   `Referer` of any subsequent cross-origin request.

Wire it in `backend/main.go` as the **outermost** wrap, so it covers both the
API mux and the embedded SPA:

```go
	Handler: middleware.SecurityHeaders(sessionMgr.LoadAndSave(mux)),
```

Create `backend/middleware/headers_test.go` following `csrf_test.go`: serve a
trivial handler through `SecurityHeaders` and assert each of the four headers is
present with the expected value; assert the CSP contains `frame-ancestors 'none'`
and `script-src 'self'`; and assert a handler that sets its own CSP wins.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./middleware/ -v
```
→ clean, all `PASS`.

### Step 4: Remove the inline script so `script-src 'self'` is real

Without this, Step 3's CSP blocks `window.__BASE_PATH` and the SPA loses its
base path on any deployment using `BASE_PATH`.

**4a — `index.html`**: replace the inline `<script>` block with a meta tag:

```html
    <meta name="app-base-path" content="%BASE_PATH%" />
```

Keep the `<script type="module" src="/src/main.tsx">` line — that is an external
script and `script-src 'self'` allows it.

**4b — `src/lib/consts.ts`**: read the meta tag instead of the global, and guard
the un-substituted placeholder:

```ts
/**
 * Base path the UI is mounted under, injected by the Go server into a meta tag
 * (backend/ui/ui_prod.go rewrites "%BASE_PATH%").
 *
 * A meta tag rather than an inline <script> so the Content-Security-Policy can
 * use a strict `script-src 'self'` with no inline allowance.
 *
 * The placeholder guard is load-bearing: a DIRECT request for /index.html is
 * served straight from the embedded filesystem without substitution, so the
 * literal "%BASE_PATH%" can reach the browser. Treating it as empty keeps the
 * app working on that path instead of prefixing every URL with a placeholder.
 */
const readBasePath = (): string => {
  const raw = document
    .querySelector('meta[name="app-base-path"]')
    ?.getAttribute("content")
    ?.trim();
  if (!raw || raw === "%BASE_PATH%") return "";
  return raw;
};

export const BASE_PATH =
  (import.meta.env.PROD ? readBasePath() : "") ||
  import.meta.env.VITE_BASE_PATH ||
  "";
```

**4c — `src/global.d.ts`**: remove the now-unused `__BASE_PATH?: string;`
declaration. `noUnusedLocals` does not cover ambient declarations, so this will
not error — remove it anyway so nothing reintroduces the global.

**4d — tests.** Create `src/lib/consts.test.ts` exercising `readBasePath`'s three
cases. Export it for test purposes. Cases: a real path returns it; the literal
`%BASE_PATH%` returns `""`; a missing meta tag returns `""`. Set up the DOM with
`document.head.innerHTML = …` in each case.

**STOP and report** if `ui_prod.go`'s `addBasePath` regex — which rewrites
`(href|src)="/..."` attributes — also mangles your new meta tag's `content`
attribute. It should not (it matches only `href`/`src`), but verify by reading
it rather than assuming. Do **not** edit `ui_prod.go`; report instead.

**Verify**: `pnpm run typecheck && pnpm run test && pnpm run build` → all exit 0.
Then confirm the built output no longer carries an inline script:
```
grep -c "window.__BASE_PATH" dist/index.html
```
→ `0`.

### Step 5: Document it

Add a short subsection to `README.md`: the console sends `nosniff`, a strict
CSP, `frame-ancestors 'none'` and `no-referrer`; object bodies are served as
attachments unless their stored type is on a small inline-safe allowlist, and
always under `Content-Security-Policy: sandbox`. Note that SVG is deliberately
**not** inline-safe. Keep it to a paragraph — do not restructure the README.

**Verify**: `grep -n "Content-Security-Policy\|nosniff" README.md` → matches.

### Step 6: Prove the tests can fail

1. Add `"text/html": true` to `inlineSafeContentTypes`. Run
   `go test ./router/ -run TestIsInlineSafe` → the `text/html` case **must fail**. Revert.
2. Remove `frame-ancestors 'none'` from the CSP string. Run
   `go test ./middleware/` → the CSP assertion **must fail**. Revert.
3. Change `readBasePath`'s placeholder guard to accept `%BASE_PATH%`. Run
   `pnpm exec vitest run consts` → that case **must fail**. Revert.

Report all three, and confirm `git status --porcelain` is clean before committing.

### Step 7: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

### Step 8: Manual checks — reviewer's job

You have no browser. Do **not** claim these passed; list them in NOTES. These
matter more than usual because a CSP breaks things silently in ways tests do not
model:

1. **The app still works at all** — log in, browse a bucket, open a menu
   (floating-ui inline styles), open the upload card. Check the browser console
   for **zero** CSP violation reports.
2. An image object still previews inline via `?view=1`.
3. A `.html` object now **downloads** instead of rendering.
4. An `.svg` object now downloads rather than rendering — deliberate.
5. Thumbnails still appear in the object list (Step 1's type fix).
6. A PDF still opens — this is the one `sandbox` is most likely to affect.
7. `curl -sI` an object and confirm `X-Content-Type-Options: nosniff` plus
   `Content-Security-Policy: sandbox`; `curl -sI` the SPA root and confirm the
   app-wide CSP with `frame-ancestors 'none'`.
8. If `BASE_PATH` is set on your deployment, confirm the UI still loads — Step 4
   changed how that value reaches the browser.

## Done criteria

- [ ] All gates in Step 7 exit 0
- [ ] Step 6's three mutations each failed the named case, and were reverted
- [ ] `grep -c "window.__BASE_PATH" dist/index.html` → `0` after a build
- [ ] `grep -rn "image/svg" backend/router/browse.go` → no match inside `inlineSafeContentTypes`
- [ ] `grep -n "SecurityHeaders" backend/main.go` → present, and it is the **outermost** wrap around `sessionMgr.LoadAndSave`
- [ ] `git diff <BASE>..HEAD -- backend/middleware/csrf.go backend/middleware/auth.go backend/middleware/audit.go` is **empty**
- [ ] `git diff <BASE>..HEAD -- backend/ui/ui_prod.go` is **empty**
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- `ui_prod.go`'s `addBasePath` regex mangles the new meta tag. Report; do not edit `ui_prod.go`.
- You conclude the fix belongs on the **upload** path instead. It does not: objects can be written straight through Garage's S3 API, bypassing this app entirely, and objects already in buckets would stay dangerous.
- You are about to make `csrf_token` `HttpOnly`. That breaks the double-submit pattern the frontend depends on.
- You are about to add `'unsafe-inline'` or `'unsafe-eval'` to **script-src** to make something work. Report what needed it instead — that is the one directive whose strictness this whole plan rests on.
- Adding `nosniff` breaks thumbnails. That means Step 1 was skipped or wrong; go back rather than dropping the header.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The allowlist is the security boundary; keep it small and deny-by-default.**
  Anything not listed is downloaded, which is the safe failure. Adding a type
  means arguing it cannot execute script — `image/svg+xml` is the standing
  example of one that looks safe and is not.
- **`style-src 'unsafe-inline'` is a knowing trade**, forced by `@floating-ui`'s
  inline positioning. If the menu primitive is ever replaced with one using CSS
  classes, tighten it.
- **The base path now travels in a meta tag.** Anything that reintroduces an
  inline `<script>` in `index.html` silently breaks under the CSP — the symptom
  is a blank page and a console violation, not a build error.
- **`Referrer-Policy: no-referrer` protects the ZIP download token**, which
  travels in a query string on a top-level navigation. Loosening it re-exposes
  that token in `Referer`.
- **Two adjacent known findings are deliberately out of scope** and still open:
  the thumbnail path's unbounded `io.ReadAll`, and `Cache-Control: max-age=86400`
  on private object bodies with no `private` directive.
