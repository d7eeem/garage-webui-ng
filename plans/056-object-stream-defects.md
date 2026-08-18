# Plan 056: Fix the two object-streaming defects — silent corruption and a nil-pointer panic

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**:
> ```
> git diff --stat 947879d..HEAD -- backend/router/browse.go backend/router/browse_test.go
> ls backend/router/browse_test.go
> ```
> Then confirm the "Current state" excerpts match. On a mismatch, STOP.
>
> **Hard prerequisite**: plan 055 must have landed — this plan's tests depend on
> the `newS3Fixture` helper it creates. If `grep -c "newS3Fixture" backend/router/browse_test.go` returns **0**, STOP: plan 055 has not been executed.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: **plan 055** (the S3 handler fixture) — hard gate
- **Category**: bug
- **Planned at**: commit `947879d`, 2026-08-13

## Why this matters

Two defects sit within forty lines of each other on the object-download path,
and both are invisible to the user at the moment they occur.

**1. An error is appended to an already-streamed body.** When `io.Copy` fails
partway through sending an object, the handler calls `utils.ResponseError`. The
status (200), `Content-Type` and `Content-Length` were committed long before, so
the client receives **N valid object bytes followed by an English error string**,
under the object's real content type, with a `Content-Length` that no longer
matches. The browser saves a silently corrupted file: a truncated image with
trailing text, a PDF that will not open, a download that "succeeded". No
client-side check can catch it — the frontend only inspects `res.ok`, which is
`true`.

**2. A nil `LastModified` panics the handler.** `object.LastModified.Format(...)`
dereferences a `*time.Time` with no guard, while the two adjacent optional fields
thirty lines below (`ContentLength`, `ETag`) **are** guarded. The asymmetry shows
this is an oversight rather than a considered invariant. Every image, video and
PDF preview goes through that line; a `GetObject` response without the header
panics the goroutine, and the client gets a reset connection with no status.

Notably, `DownloadArchive` in the same file handles the identical hazard
correctly — it routes per-object failures into a `DOWNLOAD-ERRORS.txt` entry
precisely because the status is already committed. `GetOneObject`, twenty lines
above, never got the same treatment.

## Current state

### `backend/router/browse.go:163-164` — the unguarded dereference

```go
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Header().Set("Last-Modified", object.LastModified.Format(time.RFC1123))
```

### `backend/router/browse.go:195-200` — the guarded fields, for contrast

```go
	if object.ContentLength != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(*object.ContentLength, 10))
	}
	if object.ETag != nil {
		w.Header().Set("Etag", *object.ETag)
	}
```

Match this shape exactly — it is the convention already established in this
function for optional S3 response fields.

### `backend/router/browse.go:203-208` — the error after streaming

```go
	_, err = io.Copy(w, object.Body)

	if err != nil {
		utils.ResponseError(w, err)
		return
	}
```

### What `utils.ResponseError` does — `backend/utils/utils.go:21-24`

It calls `w.WriteHeader(http.StatusInternalServerError)` — a no-op once the
status is committed, logged by `net/http` as "superfluous WriteHeader" — and then
**writes `err.Error()` into the response body**. That write succeeds, which is
what corrupts the file.

### The correct pattern, already in this file — `DownloadArchive`

`browse.go` around line 560 carries a comment stating that from the point the
first byte is written the status is committed, and per-object failures are
therefore collected and reported inside the archive rather than as an HTTP error.
That reasoning applies verbatim here.

### Conventions

- Log with the stdlib `log` package: `log.Printf("download object %q: %v", key, err)`.
- `utils.ResponseError` does **not** stop the handler — always `return` after it.
- Tests: table-driven `testing`, sub-tests via `t.Run`, `t.Setenv` (which forbids
  `t.Parallel()`), fixture from plan 055.

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

## Scope

**In scope:**
- `backend/router/browse.go` — the two sites above, and only those
- `backend/router/browse_test.go` — extend

**Out of scope — do NOT touch:**
- `utils.ResponseError` / `ResponseErrorStatus` themselves. Their behaviour is
  correct for a response that has **not** started; the defect is calling one
  after streaming. Changing the helper would affect ~30 other call sites and is a
  separate piece of work.
- The header policy: `Content-Type`, `Content-Disposition`, the inline-safe
  allowlist, `objectViewCSP`, `X-Frame-Options`, `nosniff`. All settled.
- The `thumb=1` branch (if still present) — a different plan owns it.
- `DownloadArchive` — it already handles this correctly.
- Anything under `src/`.

## Git workflow

- Branch: `advisor/056-object-stream-defects` from your given base.
- Two conventional commits, one per defect, e.g.
  `fix: guard the optional Last-Modified header` and
  `fix: stop writing an error into an already-streamed object body`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Guard the optional `Last-Modified` header

Wrap the set in a nil check, matching the `ContentLength` / `ETag` shape thirty
lines below:

```go
	if object.LastModified != nil {
		w.Header().Set("Last-Modified", object.LastModified.Format(time.RFC1123))
	}
```

Leave the `Cache-Control` line above it alone.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → no output, exit 0.

### Step 2: Stop turning a mid-stream failure into corrupt data

Replace the post-`io.Copy` error handling. The response is already committed, so
there is nothing to report over HTTP; the correct behaviour is to **log and
return**, leaving the transfer truncated:

```go
	if _, err := io.Copy(w, object.Body); err != nil {
		// The status, Content-Type and Content-Length are already committed —
		// see the same reasoning in DownloadArchive. Writing an error here
		// would append text to a partial object body and hand the user a
		// silently corrupted file. A truncated response that disagrees with
		// Content-Length is a detectable transport error; a corrupt file is not.
		log.Printf("stream object %q from bucket %q: %v", key, bucket, err)
		return
	}
```

Adjust the variable names to match the surrounding code (the handler already has
`bucket` and `key` in scope; confirm before using them).

> **Why truncation is the right outcome**: the client set out to receive
> `Content-Length` bytes and receives fewer, so the transfer fails visibly at the
> HTTP layer. That is strictly better than a complete-looking file with error
> text glued to the end.

**Verify**:
```
cd backend && go build ./...
sed -n '200,215p' router/browse.go
```
→ exit 0; no `utils.ResponseError` call remains after the `io.Copy`.

### Step 3: Audit the rest of the file for the same shape

Search for any other `utils.ResponseError` (or `ResponseErrorStatus`) that could
execute **after** bytes have been written to `w`:

```
grep -n "ResponseError" backend/router/browse.go
```

For each hit, determine whether a write to `w` can precede it on that path. The
thumbnail branch (if present) writes only after a successful decode, so its error
returns are safe. `DownloadArchive` is already correct.

**Report what you found**, including the sites you judged safe and why. If you
find a second genuine post-write error site, fix it the same way and say so — but
do not restructure any handler to do it.

**Verify**: `cd backend && go vet ./... && go build ./...` → clean.

### Step 4: Tests

Extend `backend/router/browse_test.go` using `newS3Fixture` from plan 055.

**For the nil `LastModified` defect:**
1. The fake S3 returns a `GET` response with **no** `Last-Modified` header.
   `GetOneObject` with `?view=1` must return **200** with the body intact and
   simply omit the `Last-Modified` response header. Before the fix this panics —
   that is the regression guard.
2. With `Last-Modified` present, the header is set and formatted RFC1123.

**For the error-after-streaming defect:**
3. The fake S3 returns a `Content-Length` larger than the body it actually
   writes, then closes the connection mid-body (or the fixture's response hook
   returns an error partway). Assert the recorded response body **does not
   contain** the error text — specifically, assert it contains no substring from
   the S3 error message and that the body is a prefix of the intended content.
   This is the corruption guard.
4. Assert the status is still **200** (it was committed before the failure) — the
   point of the fix is not to change the status but to stop appending text.

> `httptest.ResponseRecorder` does not simulate a real connection reset, so
> exercise the failure via the fixture's response hook (an `io.Reader` that
> returns an error after N bytes) rather than trying to kill a socket. If the
> plan-055 fixture does not expose such a hook, add one **to the test file** —
> that is in scope here.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestGetOneObject"` → all `PASS`, including the two new cases.

### Step 5: Commit, then prove the tests can fail

**Commit both fixes first.** Back up `backend/router/browse.go` outside the repo,
then one mutation at a time, restoring immediately after each:

1. Remove the nil guard from `Last-Modified` → test 1 **must fail** (a panic
   counts as a failure; confirm the test reports it rather than taking the whole
   run down).
2. Restore `utils.ResponseError(w, err)` after the `io.Copy` → test 3 **must
   fail** on the "body contains error text" assertion.

Report both; confirm `git status --porcelain` is clean.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

## Done criteria

- [ ] Step 6 passes; all packages `ok`
- [ ] Step 5's two mutations each failed the named test, and were reverted
- [ ] `grep -n "object.LastModified" backend/router/browse.go` → the only dereference is inside a `!= nil` guard
- [ ] No `utils.ResponseError` appears between the `io.Copy(w, object.Body)` call and the end of `GetOneObject`
- [ ] `git diff 947879d..HEAD -- backend/utils/utils.go src/ backend/middleware/` is **empty**
- [ ] `git diff --stat 947879d..HEAD` lists only `browse.go` and `browse_test.go`

## STOP conditions

- `grep -c "newS3Fixture" backend/router/browse_test.go` returns 0 — plan 055 has
  not landed and these tests cannot be written.
- You are about to change `utils.ResponseError` itself.
- You are about to change the status code, headers, or content-type decisions on
  the object path.
- You conclude the fix requires buffering the whole object before writing so an
  error can still be reported. It does not — that would reintroduce the unbounded
  memory pattern this codebase has been removing.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **Once a response body has started, the only honest failure is truncation.**
  Any future error path added below the `io.Copy` in this handler must log and
  return, never write. `DownloadArchive` documents the same rule for the archive
  path; these two are now consistent.
- **Optional S3 response fields are pointers.** `ContentLength`, `ETag` and
  `LastModified` are all `*T` and all three are now guarded. Anything added
  alongside them needs the same treatment — the schema models them as nullable
  because the upstream genuinely omits them.
- A reviewer should check exactly two lines: that `Last-Modified` sits inside a
  nil check, and that nothing writes to `w` after `io.Copy` fails.
- Deliberately not addressed here: the ~30 other bare `utils.ResponseError(w, err)`
  sites that leak internal error text to clients. That is a separate concern about
  *what* errors say, not *when* it is safe to send them.
