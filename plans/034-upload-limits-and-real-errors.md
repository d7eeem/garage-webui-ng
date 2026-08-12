# Plan 034: Make a failed upload return a real HTTP error instead of a dropped connection

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**:
> `git diff --stat 796039f..HEAD -- backend/router/browse.go backend/router/browse_test.go README.md`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `796039f`, 2026-08-06

## Why this matters

A maintainer reported that uploading a file through the object browser fails
with a bare **"network error"** toast. That string is not produced anywhere in
this codebase — it is Firefox's `TypeError: NetworkError when attempting to
fetch resource`, which the frontend's `handleError` toasts verbatim. The browser
reports it whenever the connection is torn down while the request body is still
being sent, which is exactly what happens today: `PutObject` (or a middleware,
or a reverse proxy in front of the app) writes an error response and returns
while the browser is still uploading megabytes, Go's HTTP server gives up on
draining the unread body, closes the connection, and the real status code and
message — the thing that would tell the operator *why* — never reaches the user.

There is also no upper bound on an upload at all. `r.FormFile` buffers the
entire body (32 MiB in RAM, the remainder in temp files on disk) before a single
byte reaches Garage, so a handful of concurrent large uploads is an unbounded
memory and disk commitment on a container whose only writable volume is `/data`.

After this plan: an over-sized upload gets a real **413** with a message naming
the limit, an S3-side failure gets its real **500** body, and the handler drains
the request body before answering so the browser can actually read that response
instead of seeing a reset socket. Plan 035 then displays those messages in the
upload panel.

## Current state

### Files

- `backend/router/browse.go` — the S3 gateway handlers. `PutObject` is at
  lines 175–217; the helper functions this file's tests exercise live at the
  bottom (lines ~523 onward).
- `backend/router/browse_test.go` — existing tests for this file. They test
  **pure helper functions only** — there is no S3 mock and no httptest server
  for the upload path. Match that: the new logic goes into a pure helper that
  is unit-tested, and the handler calls it.
- `README.md` — the environment-variable table (the row for
  `S3_PUBLIC_ENDPOINT_URL` is around line 168). A new env var must be
  documented there.

### `backend/router/browse.go:175-217` — exactly as it exists today

```go
func (b *Browse) PutObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	isDirectory := strings.HasSuffix(key, "/")

	file, headers, err := r.FormFile("file")
	if err != nil && !isDirectory {
		utils.ResponseError(w, err)
		return
	}

	if file != nil {
		defer file.Close()
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	var contentType string = ""
	var size int64 = 0

	if file != nil {
		contentType = headers.Header.Get("Content-Type")
		size = headers.Size
	}

	result, err := client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          file,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})

	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot put object: %w", err))
		return
	}

	utils.ResponseSuccess(w, result)
}
```

Note `isDirectory`: creating a folder is a `PUT` with a key ending in `/` and
**no** file part. That path must keep working — `r.FormFile` returns an error
there and the handler deliberately continues with a zero-byte body.

### Response helpers — `backend/utils/utils.go:21-35`

```go
func ResponseError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(err.Error()))
}

func ResponseErrorStatus(w http.ResponseWriter, err error, status int) {
	w.WriteHeader(status)
	w.Write([]byte(err.Error()))
}

func ResponseSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(data)
}
```

Error bodies are **plain text**, not JSON. This is a known, documented
inconsistency in this repo (see `plans/README.md`, "Documented mismatches") —
do not change it here. The frontend's `api.ts` already handles it: a
non-`application/json` response is read as text and that text becomes the
thrown `APIError.message`.

### Repo conventions to match

- Handlers are methods on empty structs, take `(w, r)`, and end in
  `utils.ResponseSuccess(w, data)` or `utils.ResponseError(w, err)`.
- **`utils.ResponseError` does NOT stop the handler — always `return` after
  it.** Every existing error branch in `browse.go` does.
- Wrap errors with `fmt.Errorf("...: %w", err)`.
- Env-var reading uses `utils.GetEnv(name, default)` (see
  `backend/middleware/csrf.go:141` for a live call:
  `utils.GetEnv("SESSION_COOKIE_SECURE", "false") == "true"`).
- Tests are plain `testing` + table-driven subtests. See
  `TestNormalizeListLimit` in `backend/router/browse_test.go:36-60` for the
  exact structural pattern to copy.

### Facts already verified — do NOT "fix" these

These were checked while writing this plan. Acting on them would be wasted
effort or a regression:

1. **Temp files from `ParseMultipartForm` are already cleaned up.** Go's
   `net/http` server calls `req.MultipartForm.RemoveAll()` in `finishRequest()`
   when the request completes. Do not add a `defer r.MultipartForm.RemoveAll()`.
2. **Streaming the upload straight into `PutObject` is out of scope.** SigV4
   needs either a seekable body or a known length to sign; a
   `multipart.Part` is neither, so the AWS SDK would buffer it anyway. Real
   streaming means switching to multipart *S3* uploads, which is deferred item
   **D2b** in `plans/README.md` and is explicitly NOT this plan.
3. **`gcr.io/distroless/static-debian12` does include `/tmp`**, so the
   spill-to-disk path is not broken in the container image.

## Commands you will need

Run backend commands from `backend/`.

| Purpose   | Command                                            | Expected on success |
|-----------|----------------------------------------------------|---------------------|
| Build     | `cd backend && go build ./...`                      | exit 0, no output   |
| Tests     | `cd backend && go test -race ./...`                 | all packages `ok`   |
| One test  | `cd backend && go test -race ./router/ -run TestUploadLimit -v` | PASS    |
| Vet       | `cd backend && go vet ./...`                        | exit 0, no output   |
| Format    | `cd backend && gofmt -l .`                          | **no output**       |

## Scope

**In scope** (the only files you may modify):

- `backend/router/browse.go`
- `backend/router/browse_test.go`
- `README.md` (one new row in the environment-variable table only)

**Out of scope** (do NOT touch, even though they look related):

- `backend/utils/utils.go` — the plain-text error-body convention is
  deliberate and load-bearing for `src/lib/api.ts`. Changing it breaks every
  error message in the frontend.
- `backend/middleware/csrf.go`, `backend/middleware/auth.go` — they also write
  responses without draining, but they reject in a few hundred bytes, long
  before a body is streaming. Changing a security boundary is not worth it
  here.
- Any frontend file. The UI half is plan 035.
- `backend/router/browse.go`'s multipart-*S3* handlers
  (`ListMultipartUploads`, `AbortMultipartUpload`) — unrelated to browser
  uploads despite the name.
- Any change to the `PUT /browse/{bucket}/{key...}` route shape, or to the
  folder-creation behaviour (a `PUT` with a trailing-slash key and no file).

## Git workflow

- Branch: `advisor/034-upload-limits-and-real-errors`
  (create it from `main`: `git checkout -B advisor/034-upload-limits-and-real-errors main`)
- Conventional-commit messages, matching `git log`. Examples from this repo:
  `fix: hide menus when the trigger scrolls away and size them to content`,
  `chore: bump version to 3.1.0`.
- Do NOT push, open a PR, or merge.

## Steps

### Step 1: Add the upload-size limit helper and its tests

In `backend/router/browse.go`, near the other package-level helpers at the
bottom of the file (after `normalizeListLimit`), add:

```go
// defaultMaxUploadBytes caps a single browser upload. The whole body is
// buffered by ParseMultipartForm before any of it reaches Garage, so this is a
// real memory/disk commitment per concurrent upload, not a policy knob. 512 MiB
// is generous for a browser form post; anything larger belongs in a multipart
// S3 upload (deferred — see plans/README.md, D2b).
const defaultMaxUploadBytes int64 = 512 << 20

// maxUploadBytes returns the configured single-upload ceiling in bytes.
//
// MAX_UPLOAD_SIZE_MB is read as whole megabytes because that is the unit an
// operator matches against their reverse proxy (nginx client_max_body_size,
// Caddy request_body max_size). A missing, unparseable, zero or negative value
// falls back to the default rather than disabling the limit: an accidental
// typo must not turn the cap off.
func maxUploadBytes() int64 {
	raw := utils.GetEnv("MAX_UPLOAD_SIZE_MB", "")
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 {
		return defaultMaxUploadBytes
	}
	return mb << 20
}
```

`strconv` is already imported in this file (it is used by `normalizeListLimit`
and `GetOneObject`). `utils` is already imported. Add no new imports for this
step.

Now add the tests to `backend/router/browse_test.go`, modelled structurally on
`TestNormalizeListLimit` (table of `{name, raw, want}` with `t.Run` subtests).
Use `t.Setenv("MAX_UPLOAD_SIZE_MB", tt.raw)` to set the variable, and for the
"unset" case use `t.Setenv("MAX_UPLOAD_SIZE_MB", "")`.

```go
func TestMaxUploadBytes(t *testing.T) {
	const mib = int64(1) << 20
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "unset falls back to the default", raw: "", want: defaultMaxUploadBytes},
		{name: "non-numeric falls back", raw: "abc", want: defaultMaxUploadBytes},
		{name: "zero falls back rather than disabling the cap", raw: "0", want: defaultMaxUploadBytes},
		{name: "negative falls back", raw: "-10", want: defaultMaxUploadBytes},
		{name: "megabytes are converted to bytes", raw: "100", want: 100 * mib},
		{name: "one megabyte", raw: "1", want: mib},
	}
	// ... t.Run subtests calling maxUploadBytes()
}
```

**Verify**: `cd backend && go test -race ./router/ -run TestMaxUploadBytes -v`
→ `PASS`, 6 subtests, all `--- PASS`.

### Step 2: Reject an over-sized upload up front, with a real 413

At the very top of `PutObject` — **before** `r.FormFile` is called, so nothing
is buffered — check the declared body size. A browser sending a `File` inside
`FormData` always sets `Content-Length`, so `r.ContentLength` is reliable here;
`-1` means unknown and must not be treated as over-limit.

Insert immediately after the `isDirectory` line:

```go
	// Reject before ParseMultipartForm buffers anything. Answering while the
	// browser is still streaming is the only way it can read the status at all
	// — once the handler returns without consuming the body, the server closes
	// the connection and the browser reports an opaque network error instead.
	limit := maxUploadBytes()
	if r.ContentLength > limit {
		utils.ResponseErrorStatus(w, fmt.Errorf(
			"upload is too large: %d bytes exceeds the %d MB limit (raise MAX_UPLOAD_SIZE_MB, and any body-size limit on your reverse proxy)",
			r.ContentLength, limit>>20,
		), http.StatusRequestEntityTooLarge)
		return
	}

	// Belt to the Content-Length suspenders: a client that lies about or omits
	// its length is still cut off at the ceiling, and MaxBytesReader makes the
	// overflow surface as a read error from FormFile rather than as unbounded
	// buffering.
	r.Body = http.MaxBytesReader(w, r.Body, limit)
```

`net/http` and `fmt` are already imported in `browse.go`.

**Verify**: `cd backend && go build ./... && go vet ./...` → both exit 0 with no
output.

### Step 3: Drain the request body before every error return

Add a helper next to `maxUploadBytes`:

```go
// drainRequestBody consumes and discards whatever is left of the request body.
//
// Go's HTTP server only auto-drains a small unread remainder before deciding to
// close the connection. When a handler answers a large upload with an error and
// returns, the socket is reset while the browser is still writing, and the
// browser surfaces "NetworkError"/"Failed to fetch" instead of the status and
// message the handler actually sent. Draining first lets the response through.
// The read is already bounded by the MaxBytesReader installed in PutObject.
func drainRequestBody(r *http.Request) {
	if r.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
}
```

`io` is already imported in `browse.go` (used by `GetOneObject`).

Then, in `PutObject` **only**, call `drainRequestBody(r)` immediately before
each `utils.ResponseError(...)`/`utils.ResponseErrorStatus(...)` that can fire
*after* the body has started arriving. That is:

- the `getS3Client` failure branch,
- the `client.PutObject` failure branch,
- the `r.FormFile` failure branch (`err != nil && !isDirectory`).

Do **not** drain in the Step 2 size-check branch: that path deliberately answers
without reading, because draining a 4 GB body just to reject it defeats the
point of rejecting it. Add a one-line comment there saying so.

Also lower the in-memory threshold so large uploads spill to disk instead of
sitting in RAM. Replace the bare `r.FormFile("file")` call with an explicit
parse first:

```go
	// Parse explicitly with a small memory budget. r.FormFile would otherwise
	// call ParseMultipartForm(32 MiB), holding up to 32 MiB per concurrent
	// upload in RAM; 4 MiB is enough for the form's non-file fields and pushes
	// the payload to the temp directory, which the runtime image has.
	if err := r.ParseMultipartForm(4 << 20); err != nil && !isDirectory {
		drainRequestBody(r)
		utils.ResponseError(w, fmt.Errorf("cannot read upload: %w", err))
		return
	}

	file, headers, err := r.FormFile("file")
	if err != nil && !isDirectory {
		drainRequestBody(r)
		utils.ResponseError(w, fmt.Errorf("cannot read uploaded file: %w", err))
		return
	}
```

**Folder creation must still work.** A `PUT` with a trailing-slash key sends no
multipart body at all, so `ParseMultipartForm` returns
`http.ErrNotMultipart`/`ErrMissingBoundary` and `FormFile` returns
`http.ErrMissingFile`; both are swallowed by the `&& !isDirectory` guard, and
`file` stays `nil` so the existing zero-byte `PutObject` runs unchanged. Do not
restructure that guard.

**Verify**:

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```
→ `gofmt -l .` prints nothing, `go vet` prints nothing, build exits 0, every
package reports `ok`.

### Step 4: Document `MAX_UPLOAD_SIZE_MB`

In `README.md`, add a row to the environment-variable table that already
contains `S3_PUBLIC_ENDPOINT_URL` (around line 168). Match the existing column
layout and tone exactly:

```
| `MAX_UPLOAD_SIZE_MB` | `512` | Largest single file the object browser accepts, in MB. A larger upload is refused with **413** before it is buffered. Must not exceed the body-size limit of any reverse proxy in front of the app (nginx `client_max_body_size`, Caddy `request_body max_size`). |
```

**Verify**: `grep -n "MAX_UPLOAD_SIZE_MB" README.md` → exactly one match, inside
the env-var table.

### Step 5: Full gate run

**Verify**: from the repo root:

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

→ no gofmt output, no vet output, build exits 0, all packages `ok`.

Then confirm nothing outside scope moved:

```
git status --porcelain
```
→ exactly three modified paths: `backend/router/browse.go`,
`backend/router/browse_test.go`, `README.md` (plus `plans/README.md` if you are
the one updating the index).

## Test plan

New tests, all in `backend/router/browse_test.go`, modelled on
`TestNormalizeListLimit` (`backend/router/browse_test.go:36-60`):

1. `TestMaxUploadBytes` — table-driven, 6 cases: unset, non-numeric, `"0"`,
   negative, `"100"` → `100 << 20`, `"1"` → `1 << 20`. Uses `t.Setenv`.

Do **not** attempt an end-to-end upload test. There is no S3 mock in this
package and building one is a much larger change than this plan; every existing
test in `browse_test.go` covers a pure helper for exactly that reason. The
handler wiring is covered by the manual check below.

**Manual verification (do this, and paste the output in your report):**

With a real Garage instance and the app running, upload something over the
limit and confirm the browser receives a status, not a reset:

```
# Expect: HTTP/1.1 413 ... and a body naming the limit — NOT a curl (52)/(56) error
curl -i -X PUT \
  -H "X-CSRF-Token: $TOKEN" -b "csrf_token=$TOKEN;session=$SESSION" \
  -F "file=@/path/to/a/file/larger/than/MAX_UPLOAD_SIZE_MB" \
  "http://localhost:3909/api/browse/<bucket>/big.bin"
```

If you cannot reach a live Garage instance, say so explicitly in your report
rather than claiming the manual check passed.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && gofmt -l .` produces no output
- [ ] `cd backend && go test -race ./...` — all packages `ok`, including the new
      `TestMaxUploadBytes` (6 subtests)
- [ ] `grep -c "drainRequestBody(r)" backend/router/browse.go` → `4` — the four
      call sites inside `PutObject` (`ParseMultipartForm`, `FormFile`,
      `getS3Client`, `client.PutObject`). The helper's own definition does not
      match this pattern, and the Step 2 size-check branch deliberately has no
      call.
- [ ] `grep -n "MaxBytesReader" backend/router/browse.go` → exactly one match,
      inside `PutObject`
- [ ] `grep -n "MAX_UPLOAD_SIZE_MB" README.md` → exactly one match
- [ ] `git diff --stat main..HEAD` lists only the in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- The `PutObject` body in `backend/router/browse.go` does not match the excerpt
  in "Current state" — the file has drifted and the insertion points may be
  wrong.
- Adding `MaxBytesReader` breaks folder creation (a `PUT` to a key ending in
  `/` with no body must still return 200). If any existing test or a live check
  regresses here, stop: the `isDirectory` guard interaction is the one subtle
  part of this plan.
- You conclude the fix requires touching `backend/utils/utils.go`, the CSRF or
  auth middleware, or any frontend file.
- A verification command fails twice after a reasonable fix attempt.
- You find yourself implementing streaming or S3 multipart uploads. That is
  deferred item D2b, not this plan.

## Maintenance notes

For whoever owns this next:

- **This plan does not eliminate every opaque network error.** A reverse proxy
  that enforces its own body limit still cuts the connection before the request
  ever reaches Go, and the browser will still report a transport failure. That
  is why the 413 message names both `MAX_UPLOAD_SIZE_MB` *and* the proxy: the
  operator has two places to look. Plan 035's client-side error message covers
  the residual case by telling the user what to check.
- If D2b (browser-side S3 multipart upload) is ever built, `maxUploadBytes` and
  the `MaxBytesReader` become per-*part* concerns, not per-file — revisit both,
  and revisit the `README.md` wording, which currently says "single file".
- A reviewer should check three things: that every `drainRequestBody` call sits
  *before* its `ResponseError` and is followed by `return`; that the Step 2
  size check is genuinely before `ParseMultipartForm`; and that the
  `!isDirectory` guards were not restructured.
