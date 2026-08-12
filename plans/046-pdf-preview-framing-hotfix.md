# Plan 046: PDF previews are blank — `X-Frame-Options: DENY` on object responses

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- backend/router/browse.go backend/middleware/headers.go src/pages/buckets/manage/browse/media-viewer.tsx
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P1 — a shipped regression. v3.5.0's release notes advertise PDF
  preview as one of five supported types, and it is broken for every PDF.
- **Effort**: S
- **Risk**: LOW-MED (touches security headers — read §1 before changing any)
- **Depends on**: none. `main` at `9ed7f65` (v3.5.0) already has 043 and 045.
- **Category**: correctness / security
- **Planned at**: commit `9ed7f65`, 2026-08-11

## 1. The diagnosis — measured, not inferred

A user reported blank PDF previews in v3.5.0. The response headers were captured
from a live deployment for a real PDF object:

```
Status Code            200 OK
content-type           application/pdf
content-security-policy sandbox
x-content-type-options nosniff
x-frame-options        DENY
content-length         198952
(no Content-Disposition)
```

What this rules **in** and **out**:

- **The allowlist works.** `content-type: application/pdf` and no
  `Content-Disposition` means `isInlineSafe` returned true and the body is being
  offered for inline rendering, exactly as intended. **Do not touch
  `inlineSafeContentTypes` or `isInlineSafe`.**
- **The body is fine.** `200`, correct length.
- **`x-frame-options: DENY` is the bug.** `DENY` instructs the browser to refuse
  rendering the resource in *any* frame — including a same-origin `<iframe>` on
  our own console. The media viewer renders PDFs in an `<iframe>`, so the frame
  can never paint. Images, video and audio are unaffected because they are not
  frames, which matches the reported symptom exactly (only PDFs blank).

**Where it comes from.** `backend/middleware/headers.go` sets four headers on
every response, including `X-Frame-Options: DENY`. The object handler overrides
`Content-Security-Policy` (replacing the app policy — and with it
`frame-ancestors 'none'`) but **never touches `X-Frame-Options`**:

```
$ grep -c "X-Frame-Options" backend/router/browse.go
0
```

So the object response ends up with the app's anti-clickjacking header, which is
correct for the console's own HTML and wrong for a body we intend to frame
ourselves.

**A second, independent defect**: the PDF branch of the viewer has no error
path, so any failure to render is a permanent blank box rather than a fallback.
Every other branch has one (see §2).

## 2. Current state

### `backend/middleware/headers.go` — the source of the header (DO NOT CHANGE THIS FILE)

```go
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", securityCSP)
```

Its doc comment already anticipates this exact situation:

> Headers are set before next.ServeHTTP runs, so a handler that needs a
> different policy for its own response (e.g. the object-serving endpoint's
> stricter "sandbox" CSP for caller-controlled bodies) can overwrite this
> header with its own Set call — the last write before WriteHeader wins.

**The fix belongs in the handler, overriding for its own response — not in this
middleware.** The console's own HTML must keep `DENY`.

### `backend/router/browse.go` — `GetOneObject`, the view path

```go
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

### `src/pages/buckets/manage/browse/media-viewer.tsx` — the branch with no error path

```tsx
  if (kind === "pdf") {
    return <iframe src={src} title={item.objectKey} className="w-full h-[70vh]" />;
  }
```

Compare the others, which all report failure:

```tsx
      <img … onError={onError} />
      <video … onError={onError} />
      <audio … onError={onError} />
      <TextPreview src={src} onError={onError} />
```

`<iframe>` does not fire `onError` for a response the browser declines to
render, so **adding `onError` to it does not work** — do not try. See Step 3.

## Conventions

- **Go handlers**: methods on empty structs, `(w, r)`, ending in
  `utils.ResponseSuccess` / `utils.ResponseError`. **`utils.ResponseError` does
  NOT stop the handler — always `return` after it.**
- Go tests: table-driven `testing`, `httptest`, `t.Setenv`. See
  `backend/middleware/headers_test.go` and `backend/router/browse_test.go`.
- Frontend: `react-daisyui` + local wrappers in `src/components/ui/`; icons from
  `lucide-react`; `@/` aliases `src/`.
- **`pnpm run lint` is expected to be red** (~55 pre-existing). New code clean.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| Build | `pnpm run build` | exit 0 |

`pnpm` is at `/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` —
prepend that directory. Do not substitute `npm`. If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(Debian-based; `git` is unusable inside it).

## Scope

**In scope:**
- `backend/router/browse.go` — override the framing headers on the view path
- `backend/router/browse_test.go` — extend
- `src/pages/buckets/manage/browse/media-viewer.tsx` — PDF escape hatch
- `src/pages/buckets/manage/browse/media-viewer.test.tsx` — extend

**Out of scope — do NOT touch:**
- **`backend/middleware/headers.go`.** The global `DENY` is correct for the
  console's own HTML and is what stops this admin UI being framed by another
  site. Narrowing it globally would reintroduce the clickjacking exposure 043
  closed. The override is per-response, in the handler.
- `inlineSafeContentTypes` / `isInlineSafe` — proven working by the captured
  headers. Adding `image/svg+xml` or `text/html` is a STOP condition.
- The `?dl=1` download path and the `?thumb=1` thumbnail path. Neither is ever
  framed, so neither needs the override. Leave their headers alone.
- `src/pages/buckets/manage/browse/object-list.tsx`, `sorting.ts`, and every
  other 045 file.
- Any new dependency.

## Git workflow

- Branch: `advisor/046-pdf-preview-framing-hotfix` from your given base.
- Conventional commit, e.g. `fix: allow same-origin framing of previewable object bodies`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Let the console — and only the console — frame an object body

In `GetOneObject`, on the path that currently sets
`Content-Security-Policy: sandbox`, replace the framing headers **for this
response only**:

```go
	// This body is rendered inside the console's own media viewer (an
	// <iframe> for PDFs), so it must be framable by us. The global
	// X-Frame-Options: DENY from middleware.SecurityHeaders is correct for the
	// console's own HTML but forbids framing by *anyone*, same-origin
	// included — which is what left PDF previews blank. Narrow it to
	// SAMEORIGIN here, and express the same rule for modern browsers with
	// frame-ancestors 'self'. Another site still cannot frame this.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
```

and extend the CSP so the sandbox is kept *and* same-origin framing is allowed:

```go
	w.Header().Set("Content-Security-Policy", csp)
```

where `csp` comes from a new pure helper:

```go
// objectViewCSP builds the Content-Security-Policy for an inline object body.
//
// `sandbox` gives the body an opaque origin with no script execution, so a
// mislabelled body cannot act on the console's origin — that is the property
// worth protecting and it is preserved in every branch below.
//
// PDFs additionally need `allow-scripts`: browsers render PDFs with a viewer
// that is itself scripted, and a fully sandboxed frame blanks it. This is a
// deliberate, narrow relaxation — `allow-same-origin` is NOT granted, so the
// document keeps its opaque origin and still cannot read the console's cookies
// (including the deliberately non-HttpOnly csrf_token), touch the parent DOM,
// or make credentialed same-origin requests. Scripts confined to an opaque
// origin cannot reach anything that matters.
//
// frame-ancestors 'self' lets the console's own viewer frame the body while
// still refusing every other site.
func objectViewCSP(contentType string) string {
	if isPDF(contentType) {
		return "sandbox allow-scripts; frame-ancestors 'self'"
	}
	return "sandbox; frame-ancestors 'self'"
}
```

Add a small `isPDF(contentType string) bool` that parses the media type the same
way `isInlineSafe` does (`mime.ParseMediaType`, lowercased, ignoring
parameters), so `application/pdf; charset=binary` still matches.

> **Do not grant `allow-same-origin`.** Together with `allow-scripts` it
> effectively removes the sandbox for same-origin content and hands a
> mislabelled body the console's origin — the exact outcome 043 exists to
> prevent. If you find PDFs still do not render without it, that is a STOP
> condition, not a licence to add it.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./...
```
→ no output, exit 0.

### Step 2: Tests (Go)

Extend `backend/router/browse_test.go`, table-driven:

1. **`TestObjectViewCSP`** — a PDF type yields a policy containing
   `allow-scripts`; a non-PDF inline type (e.g. `image/png`) yields one that
   does **not**; **both** contain `frame-ancestors 'self'`; and **neither ever
   contains `allow-same-origin`**. That last assertion is the security
   regression guard — write it as an explicit `strings.Contains(... ,
   "allow-same-origin") == false` check, not an equality test that someone can
   update without thinking.
2. **`TestIsPDF`** — `application/pdf` → true; `application/pdf; charset=binary`
   → true; `APPLICATION/PDF` → true; `image/png`, `""`, `application/x-pdf` →
   false.

If serving a full object through `GetOneObject` in a test requires a live S3
client and there is no existing fixture for it, **do not build one** — test the
pure helpers and say so in NOTES. Check whether `browse_test.go` already has a
pattern for this before deciding.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestObjectViewCSP|TestIsPDF"` → `PASS`.

### Step 3: Give the PDF frame an escape hatch

`<iframe>` does **not** fire `onError` when a browser declines to render a
response, so the viewer cannot detect this class of failure. Rather than a
detection heuristic that will be wrong, always render a small affordance beneath
the PDF frame:

- A muted line under the iframe: `Not displaying? ` followed by two `Button`s
  (`color="ghost"`, `size="sm"`): **Open in new tab** and **Download**.
- "Open in new tab" → `window.open(src, "_blank")` (the `?view=1` URL).
- "Download" → `window.open(API_URL + item.url + "?dl=1", "_blank")`, matching
  `object-actions.tsx`'s existing `onDownload`.
- Wrap the iframe and the line in a fragment or a `<div className="flex flex-col gap-2">`.

This costs two buttons and removes the failure mode where a user faces a blank
rectangle with no way forward. **Do not add `onError` to the iframe** (it will
not fire) and **do not add a load-timeout heuristic** (it produces false
positives on slow, large PDFs).

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step 4: Tests (frontend)

Extend `src/pages/buckets/manage/browse/media-viewer.test.tsx` — do not create a
new file. It already mocks what is needed; follow its existing cases.

3. Renders an `<iframe>` for a `.pdf` item whose `src` ends in `?view=1`.
4. Renders both the **Open in new tab** and **Download** buttons alongside the
   PDF frame.
5. The Download button opens the `?dl=1` URL — assert on a mocked
   `window.open`, checking the argument contains `?dl=1`.

> jsdom has no layout engine and does not render PDFs. Assert on the DOM only —
> which elements exist and what their attributes are. Never assert that the PDF
> displayed.

**Verify**: `pnpm exec vitest run media-viewer` → all pass, including the seven
pre-existing cases.

### Step 5: Prove the tests can fail

Run each mutation, confirm the named test fails, then revert:

1. Return `"sandbox"` unconditionally from `objectViewCSP` (dropping
   `frame-ancestors`) → `TestObjectViewCSP` **must fail**.
2. Add `allow-same-origin` to the PDF branch → `TestObjectViewCSP` **must
   fail**. This is the important one — it is the guard against the one change
   that would quietly undo 043.
3. Remove the Download button from the PDF branch → frontend case 4 or 5 **must
   fail**.

Report all three, then confirm `git status --porcelain` is clean before
committing.

### Step 6: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

### Step 7: Manual checks — reviewer's job

You have no browser. Do **not** claim these passed; list them in NOTES:

1. A PDF renders inside the viewer in **Chrome** and **Firefox**.
2. The response now carries `x-frame-options: SAMEORIGIN` and
   `content-security-policy: sandbox allow-scripts; frame-ancestors 'self'`.
3. A **non-PDF** inline type (a `.png`) still carries plain `sandbox` with
   `frame-ancestors 'self'` and **no** `allow-scripts`.
4. The console's own pages still carry `X-Frame-Options: DENY` — the global
   header is unchanged for HTML.
5. `.svg` and `.zip` still download rather than render.
6. **If a PDF still blanks after this change**, capture the headers again and
   report. Do not add `allow-same-origin`.

## Done criteria

- [ ] All gates in Step 6 exit 0
- [ ] Step 5's three mutations each failed the named test, and were reverted
- [ ] `git diff <BASE>..HEAD -- backend/middleware/headers.go` is **empty**
- [ ] `grep -c "allow-same-origin" backend/` → **0**
- [ ] `grep -n "X-Frame-Options" backend/router/browse.go` → exactly one match, `SAMEORIGIN`
- [ ] `grep -n "inlineSafeContentTypes" backend/router/browse.go` — the map is unchanged (`git diff` shows no hunk inside it)
- [ ] `grep -n "onError" src/pages/buckets/manage/browse/media-viewer.tsx` — still **no** `onError` on the `<iframe>`
- [ ] `git diff --stat <BASE>..HEAD` lists only the 4 in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You are about to add `allow-same-origin`, or to relax
  `middleware/headers.go`'s global `DENY`, or to add a type to
  `inlineSafeContentTypes`. All three undo 043; none is an executor decision.
- You are about to add `onError` to the `<iframe>` or a load-timeout heuristic.
- You conclude the `sandbox` directive itself must be dropped entirely.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **Two headers govern framing and they are set in different places.** The
  middleware sets `X-Frame-Options` globally; the handler overrides both it and
  the CSP for object bodies. Changing either alone produces exactly this bug —
  a policy that looks right in one file and is contradicted by the other. This
  is what the v3.5.0 regression was.
- **`allow-scripts` without `allow-same-origin` is the whole trick.** The
  document keeps an opaque origin, so its scripts cannot read the console's
  cookies, touch the parent DOM, or make credentialed same-origin requests.
  Granting both together is equivalent to no sandbox at all for same-origin
  content. If a future change needs script access to the parent, the answer is
  not to add `allow-same-origin`.
- **The PDF affordance is deliberate, not a placeholder.** `<iframe>` cannot
  report a render failure, so the buttons are the only escape from a blank
  frame. Do not replace them with a detection heuristic.
- The captured headers that diagnosed this are in §1 — if PDF previews break
  again, capture the same three (`content-type`, `content-disposition`,
  `x-frame-options`) before theorising.
