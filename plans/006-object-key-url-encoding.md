# Plan 006: Encode object keys in URLs and download filenames

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/router/browse.go src/pages/buckets/manage/browse/ src/lib/api.ts`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED — touches URL construction on every object operation
- **Depends on**: `plans/002-verification-baseline.md`
- **Category**: bug
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

S3 object keys are nearly arbitrary byte strings. They routinely contain
characters that are structural in a URL: `?`, `#`, `%`, `+`, and spaces. This
codebase builds URLs by string concatenation at five separate points and never
percent-encodes the key at any of them.

Concrete consequences for a file named `report #3 (final).pdf`:

- The listing's `url` field becomes `/browse/mybucket/report #3 (final).pdf`.
  A browser truncates at `#`, so viewing the file opens
  `/browse/mybucket/report ` — a 404 or the wrong object.
- Uploading it issues `PUT /api/browse/mybucket/report #3 (final).pdf`; the
  fragment never reaches the server, so the object is stored under a truncated
  key.
- A key containing `?` is worse: everything after it is parsed as a query
  string, so `?dl=1` silently becomes part of an unrelated parameter and the
  download flag is lost.
- A key containing a literal `%` followed by two hex digits is decoded by the Go
  server into a different byte entirely — a silent wrong-object read.

Separately, the download response builds its `Content-Disposition` header by raw
concatenation. A filename with a space truncates at the space in most browsers
because the value is unquoted; `;` and `"` corrupt the header outright. (Go's
header writer replaces CR and LF with spaces before transmission, so this is a
correctness bug, not header injection.)

None of this is exotic — spaces and `#` in filenames are everyday. Today the UI
appears to work right up until someone uploads a normally-named file.

## Current state

### Files

- `backend/router/browse.go` — builds the `url` field and the `Content-Disposition` header.
- `src/lib/api.ts` — the fetch wrapper that turns a path into a `URL`.
- `src/pages/buckets/manage/browse/hooks.ts` — builds PUT and DELETE paths from raw keys.
- `src/pages/buckets/manage/browse/object-list.tsx` — concatenates `url` for view and thumbnail.
- `src/pages/buckets/manage/browse/object-actions.tsx` — concatenates `url` for download.

### Excerpt 1 — the server-built URL

`backend/router/browse.go:66-78`:

```go
	for _, object := range objects.Contents {
		key := strings.TrimPrefix(*object.Key, prefix)
		if key == "" {
			continue
		}

		result.Objects = append(result.Objects, schema.BrowserObject{
			ObjectKey:    &key,
			LastModified: object.LastModified,
			Size:         object.Size,
			Url:          fmt.Sprintf("/browse/%s/%s", bucket, *object.Key),
		})
	}
```

`*object.Key` is the full, raw key. Note `ObjectKey` is the key with the prefix
*trimmed* (display name), while `Url` uses the *full* key. Both matter.

### Excerpt 2 — the download filename

`backend/router/browse.go:126-129`:

```go
	defer object.Body.Close()
	keys := strings.Split(key, "/")

	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", keys[len(keys)-1]))
```

Unquoted and unencoded.

### Excerpt 3 — how the routes receive the key

`backend/router/router.go:25-28`:

```go
	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)
```

**This is the load-bearing detail of the whole plan.** Go's `net/http` ServeMux
(1.22+) wildcard `{key...}` matches the remainder of the path, and
`r.PathValue("key")` returns the segments **already percent-decoded**. So the
server side needs no decoding change — it needs the client and the generated
`url` to encode correctly, and `PathValue` will undo it.

Verify this yourself before relying on it — see step 1.

### Excerpt 4 — the client's URL construction

`src/lib/api.ts:23-31`:

```ts
  async fetch<T = any>(url: string, options?: Partial<FetchOptions>) {
    const headers: Record<string, string> = {};
    const _url = new URL(API_URL + url, window.location.origin);

    if (options?.params) {
      Object.entries(options.params).forEach(([key, value]) => {
        _url.searchParams.set(key, String(value));
      });
    }
```

`new URL(...)` parses the string — it does **not** encode it. A `?` in `url`
starts a query string; a `#` starts a fragment. By the time `URL` sees it, the
damage is done.

### Excerpt 5 — the three client call sites

`src/pages/buckets/manage/browse/hooks.ts:24-52`:

```ts
export const usePutObject = (
  bucket: string,
  options?: UseMutationOptions<any, Error, PutObjectPayload>
) => {
  return useMutation({
    mutationFn: async (body) => {
      const formData = new FormData();
      if (body.file) {
        formData.append("file", body.file);
      }

      return api.put(`/browse/${bucket}/${body.key}`, { body: formData });
    },
    ...options,
  });
};

export const useDeleteObject = (
  bucket: string,
  options?: UseMutationOptions<any, Error, { key: string; recursive?: boolean }>
) => {
  return useMutation({
    mutationFn: (data) =>
      api.delete(`/browse/${bucket}/${data.key}`, {
        params: { recursive: data.recursive },
      }),
    ...options,
  });
};
```

`src/pages/buckets/manage/browse/object-list.tsx:30-32` and `:156-162`:

```tsx
  const onObjectClick = (object: Object) => {
    window.open(API_URL + object.url + "?view=1", "_blank");
  };
```

```tsx
      <img
        src={API_URL + object.url + (thumbnailSupport ? "?thumb=1" : "?view=1")}
        alt={object.objectKey}
        className="size-5 object-cover overflow-hidden mr-2"
      />
```

`src/pages/buckets/manage/browse/object-actions.tsx:32-34`:

```tsx
  const onDownload = () => {
    window.open(API_URL + object.url + "?dl=1", "_blank");
  };
```

### Repo conventions to match

- **Go**: stdlib first. `net/url` and `mime` are both stdlib — no new
  dependency is needed or permitted for this plan.
- **Frontend**: shared helpers live in `src/lib/`. `src/lib/utils.ts` holds
  generic helpers (`cn`, `readableBytes`, `url`); `src/lib/api.ts` holds the
  fetch client. A key-encoding helper belongs in one of those, not duplicated
  across components.
- **Tests** (added by plan 002): Vitest with globals, and stdlib Go `testing`.
  Model on `src/lib/utils.test.ts` and `backend/utils/utils_test.go`.

## Commands you will need

| Purpose         | Command                                    | Expected on success |
|-----------------|--------------------------------------------|---------------------|
| Go build        | `cd backend && go build ./...`             | exit 0              |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output   |
| Go format check | `cd backend && gofmt -l .`                 | no output           |
| Go tests        | `cd backend && go test -race ./...`        | `ok` per package    |
| Typecheck       | `pnpm run typecheck`                       | exit 0              |
| Frontend tests  | `pnpm run test`                            | all pass            |
| Lint            | `pnpm run lint`                            | exit 0              |
| Frontend build  | `pnpm run build`                           | exit 0              |

## Scope

**In scope** (the only files you should modify or create):

- `backend/router/browse.go`
- `backend/router/browse_test.go` (create, or extend if plan 003 created it)
- `src/lib/api.ts`
- `src/lib/api.test.ts` (extend — plan 002 creates it)
- `src/pages/buckets/manage/browse/hooks.ts`
- `src/pages/buckets/manage/browse/object-list.tsx`
- `src/pages/buckets/manage/browse/object-actions.tsx`

**Out of scope** (do NOT touch, even though they look related):

- `backend/router/router.go` — the route patterns are correct. `{key...}` with
  automatic decoding is exactly what this plan relies on.
- `backend/schema/browse.go` — the `Url` field's *type* is fine; only its
  *content* changes.
- `src/pages/buckets/manage/browse/share-dialog.tsx` — it builds a public
  website URL (`"http://" + domain + "/" + prefix + key`), which is a different
  URL space with different rules, and plan 009 redesigns it entirely. Leave it.
- `src/pages/buckets/manage/browse/actions.tsx` — the upload trigger. It passes
  `prefix + file.name` to `usePutObject`, which is correct; the encoding belongs
  in the hook, not the caller.
- Pagination and delete-loop logic — plan 003 owns those.
- `context.Background()` call sites — plan 004 owns those.

## Git workflow

- Branch: `advisor/006-object-key-url-encoding`
- Conventional commits. Example from `git log`: `fix: panic when download file`.
- Suggested commits: `fix: percent-encode object keys in browse urls`,
  `fix: encode download filename per rfc 5987`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Confirm the ServeMux decoding assumption before changing anything

This plan's entire design rests on `r.PathValue("key")` returning a decoded key.
Verify it rather than trusting the excerpt.

Write a throwaway Go test in `backend/router/browse_test.go` (package `router`):

```go
func TestPathValueDecodesWildcard(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("GET /browse/{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("key")
	})

	req := httptest.NewRequest("GET", "/browse/b/dir/report%20%233.pdf", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if want := "dir/report #3.pdf"; got != want {
		t.Errorf("PathValue(key) = %q, want %q", got, want)
	}
}
```

**Keep this test** — it is a load-bearing assumption and deserves permanent
coverage.

**Verify**: `cd backend && go test -race ./router/...` → `ok`, this test passes.

If it fails — if `PathValue` returns the raw encoded string — **stop**. The
whole approach inverts (the server would need to decode explicitly) and that is
a different plan. See STOP conditions.

### Step 2: Percent-encode the generated `url` on the server

In `backend/router/browse.go`, add a helper near the other package-level
functions at the bottom of the file:

```go
// browseObjectURL builds the API path for an object, percent-encoding both the
// bucket and the key so that keys containing '?', '#', '%', '+', or spaces
// survive the round trip. Each path segment is escaped individually; the '/'
// separators between segments stay literal so the {key...} wildcard still
// matches them.
func browseObjectURL(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return "/browse/" + url.PathEscape(bucket) + "/" + strings.Join(segments, "/")
}
```

Add `"net/url"` to the import block. `strings` is already imported.

Why segment-by-segment rather than escaping the whole key: `url.PathEscape`
encodes `/` as `%2F`, which would collapse the object hierarchy into a single
segment and break the `{key...}` match. Escaping each segment preserves the
separators while encoding everything else.

Then use it at line 76:

```go
			Url:          browseObjectURL(bucket, *object.Key),
```

Add tests to `backend/router/browse_test.go`:

- `browseObjectURL("b", "file.txt")` → `/browse/b/file.txt`
- `browseObjectURL("b", "dir/file.txt")` → `/browse/b/dir/file.txt` (slash preserved)
- `browseObjectURL("b", "report #3.pdf")` → `/browse/b/report%20%233.pdf`
- `browseObjectURL("b", "a?b.txt")` → `/browse/b/a%3Fb.txt`
- `browseObjectURL("b", "100%.txt")` → `/browse/b/100%25.txt`
- `browseObjectURL("b", "a+b.txt")` → check the actual output of
  `url.PathEscape("a+b.txt")` and assert that. `PathEscape` leaves `+` literal
  in a path (it is only special in query strings), so the expected value is
  `/browse/b/a+b.txt`. **Confirm against the real function** rather than
  trusting this note.
- Round-trip: for each case, feed the result through the step-1 mux and assert
  `PathValue("key")` equals the original key. This is the assertion that
  actually proves the fix.

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./router/...` → exit 0 from all four.

### Step 3: Build the download filename with `mime.FormatMediaType`

In `backend/router/browse.go`, replace the `Content-Disposition` construction:

```go
	if download {
		filename := keys[len(keys)-1]
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
			"filename": filename,
		}))
```

Add `"mime"` to the import block.

`mime.FormatMediaType` quotes and escapes the parameter value correctly, and for
non-ASCII values it emits the RFC 2231 `filename*=utf-8''...` form on its own.
It returns an **empty string** when the value is not valid UTF-8 — which S3 keys
can be, since keys are byte strings. Add a fallback for that case:

```go
	if download {
		filename := keys[len(keys)-1]
		disposition := mime.FormatMediaType("attachment", map[string]string{
			"filename": filename,
		})
		if disposition == "" {
			// FormatMediaType rejects values that are not valid UTF-8. Fall
			// back to a percent-encoded RFC 5987 parameter.
			disposition = "attachment; filename*=UTF-8''" + url.PathEscape(filename)
		}
		w.Header().Set("Content-Disposition", disposition)
```

`url` is already imported from step 2.

Extract this into a testable helper instead of inlining it — add near
`browseObjectURL`:

```go
// contentDispositionAttachment builds a Content-Disposition header value that
// survives filenames containing spaces, quotes, semicolons, or non-ASCII
// characters.
func contentDispositionAttachment(filename string) string {
	if disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": filename,
	}); disposition != "" {
		return disposition
	}
	return "attachment; filename*=UTF-8''" + url.PathEscape(filename)
}
```

and call it: `w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))`

Tests to add:

- `contentDispositionAttachment("file.txt")` → contains `attachment` and
  `file.txt`
- `contentDispositionAttachment("my report.pdf")` → the filename is quoted, so
  the result contains `"my report.pdf"` including the quote characters
- `contentDispositionAttachment(`a"b.txt`)` → the quote is escaped, and the
  result parses cleanly back through `mime.ParseMediaType` with the `filename`
  parameter equal to the input
- `contentDispositionAttachment("résumé.pdf")` → returns a non-empty value
  starting with `attachment`. Do not assert the exact form: Go emits the
  RFC 2231 `filename*=utf-8''` variant here, but pinning that string makes the
  test brittle for no gain.
- `contentDispositionAttachment(string([]byte{0xff, 0xfe}))` → exercises the
  fallback branch (invalid UTF-8). Assert non-empty and that it starts with
  `attachment`. This is the only input that reaches the fallback, so without
  this case that branch is dead in the test suite.

Prefer asserting via `mime.ParseMediaType` round-trip over asserting exact
strings — it tests the property that matters and is not brittle to formatting.

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./router/...` → exit 0 from all four.

### Step 4: Add a key-encoding helper to the frontend API client

In `src/lib/api.ts`, export a helper above the `api` object:

```ts
/**
 * Percent-encodes an object key for use in a `/browse/...` path.
 *
 * Each path segment is encoded individually so that `/` separators — which
 * delimit S3 "directories" — stay literal, while `?`, `#`, `%`, spaces, and
 * other structural characters inside a segment are escaped.
 */
export const encodeObjectPath = (key: string) =>
  key
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");
```

`encodeURIComponent` is the correct JS counterpart to Go's `url.PathEscape` for
this purpose. It encodes `#`, `?`, `%`, and space, and leaves `-_.!~*'()`
literal — all of which are legal in a path segment.

Add tests to `src/lib/api.test.ts` (created by plan 002):

- `encodeObjectPath("file.txt")` → `"file.txt"`
- `encodeObjectPath("dir/file.txt")` → `"dir/file.txt"`
- `encodeObjectPath("report #3.pdf")` → `"report%20%233.pdf"`
- `encodeObjectPath("a?b.txt")` → `"a%3Fb.txt"`
- `encodeObjectPath("100%.txt")` → `"100%25.txt"`
- `encodeObjectPath("")` → `""`
- Cross-check against the Go side: `encodeURIComponent` and `url.PathEscape`
  differ on a few characters (notably `!`, `'`, `(`, `)`, and `*`). That is
  **fine** — both produce a string the other side decodes to the same key, which
  is the only property that matters. Do not try to make the two byte-identical.
  Add a comment saying so, so a future reader does not "fix" it.

**Verify**: `pnpm run test` → all pass.

### Step 5: Use the helper in the mutation hooks

In `src/pages/buckets/manage/browse/hooks.ts`, encode the key in both mutations:

```ts
import api, { encodeObjectPath } from "@/lib/api";
```

then:

```ts
      return api.put(`/browse/${bucket}/${encodeObjectPath(body.key)}`, {
        body: formData,
      });
```

and:

```ts
      api.delete(`/browse/${bucket}/${encodeObjectPath(data.key)}`, {
        params: { recursive: data.recursive },
      }),
```

The `bucket` name itself does not need encoding — Garage bucket aliases are
DNS-compatible names. Leave it plain; note that in a comment if you like.

**Verify**: `pnpm run typecheck && pnpm run lint` → exit 0.

### Step 6: Stop double-encoding in the read paths

The `url` field returned by the server is now **already encoded** (step 2). The
three components that concatenate it must therefore *not* encode it again.

Read each of the three call sites and confirm they use `object.url` verbatim:

- `src/pages/buckets/manage/browse/object-list.tsx:31` — `window.open(API_URL + object.url + "?view=1", "_blank")`
- `src/pages/buckets/manage/browse/object-list.tsx:158` — the `<img src=...>`
- `src/pages/buckets/manage/browse/object-actions.tsx:33` — `window.open(API_URL + object.url + "?dl=1", "_blank")`

**No code change is required at any of the three** — they already pass
`object.url` through untouched, and it is now correctly encoded. Add a short
comment at each site so nobody "helpfully" adds encoding later:

```tsx
  // object.url arrives percent-encoded from the API; do not re-encode.
```

This step exists to make you *check*, not to make you change. If any of the
three transforms `object.url` (e.g. with `encodeURI`), that is a double-encode
and must be removed.

**Verify**: `pnpm run typecheck && pnpm run lint && pnpm run build` → exit 0.

### Step 7: Confirm `src/lib/api.ts` does not mangle the encoded path

`api.fetch` does `new URL(API_URL + url, window.location.origin)`. With `url`
now percent-encoded, `new URL` will parse it correctly — `%23` is not a
fragment delimiter, `%3F` is not a query delimiter.

Add a test to `src/lib/api.test.ts` proving the whole chain:

- Stub `fetch`, call
  `api.get("/browse/b/" + encodeObjectPath("a?b#c.txt"), { params: { view: 1 } })`,
  and assert the URL passed to `fetch` has `pathname` equal to
  `/api/browse/b/a%3Fb%23c.txt` and `search` equal to `?view=1`.

This is the test that would have caught the original bug end to end.

**Verify**: `pnpm run test` → all pass.

### Step 8: Manual verification (only with a live Garage instance)

Skip entirely if no Garage instance is available.

Upload a file named `report #3 (v2) 100%.txt` through the UI, then:

1. It appears in the listing with its full name.
2. Clicking it opens the correct object (not a 404).
3. The download button downloads it with the correct filename, spaces intact.
4. Deleting it removes that object and no other.

Report which of the four you verified.

## Test plan

New tests:

| File | Tests | Covers |
|---|---|---|
| `backend/router/browse_test.go` | 1 mux-decoding, 6 `browseObjectURL` (+ round-trips), 4 `contentDispositionAttachment` | steps 1-3 |
| `src/lib/api.test.ts` (extend) | 6 `encodeObjectPath`, 1 end-to-end URL | steps 4 and 7 |

Structural pattern: table-driven Go tests with `t.Errorf` (match
`backend/utils/utils_test.go`); Vitest `describe`/`it`/`expect` with globals
(match `src/lib/utils.test.ts`).

The round-trip assertions in step 2 are the important ones — they test the
property ("encode then decode yields the original key") rather than a specific
encoding, which keeps them meaningful if the escaping helper is ever swapped.

**Verification**: `cd backend && go test -race ./...` → `ok`.
`pnpm run test` → all pass.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0, including all new `router` tests
- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run lint` exits 0
- [ ] `pnpm run test` exits 0, including the new `api.test.ts` cases
- [ ] `pnpm run build` exits 0
- [ ] `grep -n 'fmt.Sprintf("/browse/%s/%s"' backend/router/browse.go` returns no matches
- [ ] `grep -n 'filename=%s' backend/router/browse.go` returns no matches
- [ ] `grep -n "encodeObjectPath" src/pages/buckets/manage/browse/hooks.ts` returns 2 matches
- [ ] `git status` shows only the in-scope files (plus `plans/README.md`) modified or created
- [ ] `plans/README.md` status row for 006 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- **Step 1's test fails** — `r.PathValue("key")` does not return a decoded
  value. Every other step assumes it does. Report the actual value returned;
  the correct redesign (explicit server-side decoding) is a different plan.
- `pnpm run typecheck` or `pnpm run test` does not exist — plan 002 has not
  landed.
- Manual testing (step 8) shows a key round-tripping to a *different* key. That
  suggests a double-encode or a double-decode somewhere in the chain; report the
  original key, the URL observed in devtools, and the key the server stored.
  Do not add a compensating `decodeURIComponent` — that hides the bug.
- You need to modify `src/pages/buckets/manage/browse/share-dialog.tsx`. It
  builds a different kind of URL and belongs to plan 009.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **The encoding boundary is now explicit and one-directional**: the server
  emits encoded `url` values, the client encodes keys it constructs itself
  (upload, delete), and nothing decodes. Any new code that builds a
  `/browse/...` path must use `encodeObjectPath` (frontend) or
  `browseObjectURL` (backend). A new call site that concatenates a raw key
  reintroduces the bug silently.
- **Go and JS encoders are intentionally not byte-identical.**
  `url.PathEscape` and `encodeURIComponent` differ on `!'()*`. Both round-trip
  correctly, which is the property the tests assert. Do not "align" them.
- **`browseObjectURL` escapes per segment on purpose.** Escaping the whole key
  would turn `/` into `%2F` and break the `{key...}` route match. There is a
  test for the slash case; keep it.
- **Reviewer should scrutinize**: that no component re-encodes `object.url`
  (double-encoding is the most likely regression here, and it fails silently
  with a 404 rather than an error), and that the `Content-Disposition` fallback
  branch has a test that actually reaches it — `mime.FormatMediaType` handles
  non-ASCII itself and only returns `""` on invalid UTF-8, so it is easy to
  write a fallback nothing ever exercises.
- **Deliberately deferred**: keys containing a literal newline or control
  character. S3 permits them; `url.PathEscape` encodes them correctly, so they
  should work, but they are untested here and rare enough not to justify the
  fixture work. If a user reports one, add a case to the round-trip table.
- **Interacts with plan 009** (presigned share links): that plan builds a
  third kind of URL for the same objects. It will need its own encoding
  decision — presigned URLs are generated by the AWS SDK, which handles
  encoding itself, so it should *not* reuse these helpers.
