# Plan 055: Handler-level tests for the S3 data plane, using the fixture that already exists

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**:
> ```
> git diff --stat 947879d..HEAD -- backend/router/browse.go backend/router/browse_test.go backend/router/buckets_test.go
> ```
> On a mismatch with the "Current state" excerpts, STOP.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW — **test-only; no production code changes at all**
- **Depends on**: none
- **Blocks**: plan 056 (the two streaming defects) — those fixes are unverifiable without this
- **Category**: tests
- **Planned at**: commit `947879d`, 2026-08-13

## Why this matters

Eight handlers in `backend/router/browse.go` measure **0.0% coverage**:
`GetObjects`, `GetOneObject`, `PutObject`, `DeleteObject`, `BulkDeleteObjects`,
`ListMultipartUploads`, `AbortMultipartUpload`, `ShareObject`. These are the
operations the product exists to perform, in the largest and highest-churn file
in the repo. Everything currently tested in `browse_test.go` is a *pure helper* —
`normalizeListLimit`, `isInlineSafe`, `safeZipEntryName` and friends — plus the
archive path.

The consequence is not abstract: the known defects in this file (a nil-pointer
dereference on the preview path, and an error written into an already-streamed
response body) **cannot be given a regression test** until a handler can be
driven against a fake S3. This plan is the prerequisite for fixing them.

**You do not need to change any production code to do this.** A working fixture
already exists in this repo and is used exactly once.

## Current state

### The fixture that already works — `backend/router/browse_test.go:921-975`

`TestDownloadArchive`'s success case stands up two `httptest` servers and points
the real code at them purely with environment variables:

```go
		// Mock admin API: getBucketCredentials calls GetBucketInfo, then
		// GetKeyInfo for the first read+write key it finds.
		adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasPrefix(r.URL.Path, "/v2/GetBucketInfo"):
				_ = json.NewEncoder(w).Encode(schema.Bucket{
					Keys: []schema.KeyElement{
						{
							AccessKeyID: accessKeyID,
							Permissions: schema.Permissions{Read: true, Write: true},
						},
					},
				})
			case strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo"):
				_ = json.NewEncoder(w).Encode(schema.KeyElement{
					AccessKeyID:     accessKeyID,
					SecretAccessKey: "test-secret",
				})
			…
		}))
		defer adminServer.Close()

		// Mock S3 API (path-style) …
		s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
			…
		}))
		defer s3Server.Close()

		t.Setenv("API_BASE_URL", adminServer.URL)
```

(the next lines also `t.Setenv("S3_ENDPOINT_URL", s3Server.URL)`).

**Why this works with no production change**: `getS3Client` resolves its endpoint
at call time —

```go
func getS3Client(bucket string) (*s3.Client, error) {
	return getS3ClientForEndpoint(bucket, utils.Garage.GetS3Endpoint())
}
```

— and `GetS3Endpoint()` reads `S3_ENDPOINT_URL` from the environment on every
call. So `t.Setenv` *is* the seam. Nothing needs to be refactored.

> **Do not introduce an interface or a package-level `var newS3Client`.**
> `backend/router/browse.go:875` does `presign := s3.NewPresignClient(client)`,
> which requires the concrete `*s3.Client`; an interface seam would break
> `ShareObject`. The environment-based seam has none of that problem and is
> already proven in this file.

### The S3 SDK calls your fake must answer

Counted from `browse.go`: `ListObjectsV2` ×2, `GetObject` ×2, `DeleteObjects` ×2,
`ListMultipartUploads` ×2, `PutObject`, `HeadObject`, `DeleteObject`, and
`presign.PresignGetObject` (which signs locally and issues **no** request).

### The other helpers already in the file

- `withDownloadSession(t, username, fn)` — `browse_test.go:624` — seeds an
  authenticated session inside the scs middleware. **Session-touching handlers
  must go through this**, or `utils.Session.Get` panics.
- `mintDownloadToken`, `requestArchive` — `browse_test.go:638`, `:661`.

### Conventions

- Table-driven `testing`, sub-tests via `t.Run`, `t.Errorf` with got/want.
- `t.Setenv` forbids `t.Parallel()` — these tests must stay sequential.
- `utils.InitCacheManager()` is called at the top of tests that touch the
  per-bucket credential cache; without it the cache is nil. Note the credential
  cache has a ~1h TTL, so **each test must use a distinct bucket name** or it
  will silently reuse another test's cached credentials.

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |
| Coverage | `cd backend && go test ./router/ -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out \| grep browse.go` | the eight handlers above 0.0% |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

**Write the coverage profile to `/tmp`, not into the repo.**

## Scope

**In scope:**
- `backend/router/browse_test.go` — extract the fixture, add handler tests
- `backend/router/buckets_test.go` — replace the re-implementation test

**Out of scope — do NOT touch:**
- **Any non-test file.** This plan changes zero production code. If you believe a
  handler must change to be testable, that is a STOP condition — the
  environment-based seam already works.
- The existing pure-helper tests and `TestDownloadArchive` — leave them passing
  and unmodified beyond the mechanical extraction in Step 1.
- `backend/router/browse.go` — including the defects you will notice while
  writing these tests. **Do not fix them here.** Plan 056 fixes them, and it
  depends on this plan landing first so the fixes have a regression net.

## Git workflow

- Branch: `advisor/055-s3-handler-test-fixture` from your given base.
- Conventional commit, e.g. `test: cover the S3 data-plane handlers`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Extract the fixture, changing no behaviour

Lift the two `httptest` servers out of `TestDownloadArchive` into a reusable
helper in `browse_test.go`:

```go
// s3Fixture wires the real handlers to fake admin and S3 servers. The seam is
// environmental: getS3Client resolves S3_ENDPOINT_URL at call time, so no
// production code needs a test hook.
type s3Fixture struct {
	bucket   string
	objects  map[string]string  // key -> body
	// hooks the tests can set to control responses:
	getObject func(w http.ResponseWriter, r *http.Request) bool // return true if handled
	…
}

func newS3Fixture(t *testing.T, bucket string) *s3Fixture
```

Requirements:

- The fixture registers `t.Cleanup` to close both servers — do not rely on
  `defer` at the call site.
- It calls `utils.InitCacheManager()` and `t.Setenv` for both endpoints.
- It takes the bucket name so each test can pass a **unique** one (the
  credential cache is keyed by bucket with a ~1h TTL).
- It exposes a way for a test to override the S3 response for a specific request
  — a function field is enough. Later tests need to return a nil
  `Last-Modified`, an error mid-body, and an oversized upload response.

Then rewrite `TestDownloadArchive`'s success case to use it. **That test must
still pass, unchanged in what it asserts.**

**Verify**: `cd backend && go test -race ./router/ -run TestDownloadArchive -v` → all sub-tests `PASS`.

### Step 2: Cover the read handlers

Add table tests using the fixture:

**`GetObjects`**
1. Lists objects under a prefix, with the common prefix stripped as the handler does.
2. A continuation token round-trips: the response's `nextToken` is what the fake saw as the continuation parameter on the second call.
3. The limit is clamped by `normalizeListLimit` — pass an out-of-range limit and assert the value the fake receives.

**`GetOneObject`**
4. With **no** `view`/`dl` parameter, returns object metadata as JSON (the HEAD path), not a body.
5. `?view=1` on an inline-safe type (e.g. `image/png`) returns the body with that `Content-Type` and **no** `Content-Disposition`.
6. `?view=1` on a type **not** on the allowlist (e.g. `text/html`) returns `application/octet-stream` **with** `Content-Disposition: attachment`.
7. `?dl=1` always sets `Content-Disposition: attachment`.
8. A missing object returns **404**, not 500.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestGetObjects|TestGetOneObject"` → all `PASS`.

### Step 3: Cover the write handlers

**`PutObject`**
9. A normal multipart upload succeeds and the fake receives the expected key and body.
10. An upload larger than `MAX_UPLOAD_SIZE_MB` returns **413** and the response body is readable by the client (this is the behaviour `drainRequestBody` exists to guarantee). Use `t.Setenv` to set a small limit.
11. Content-type resolution: a part with an empty or generic type gets a type resolved from the key's extension.

**`DeleteObject`**
12. A single-object delete calls the fake once with that key.
13. A recursive delete over a listing that spans **two pages** deletes every key from both pages — this exercises the pagination loop, which is invisible to a single-page test.

**`BulkDeleteObjects`**
14. A partial failure is reported: the response names the failed key and still reports the successes.

**`ShareObject`**
15. Returns a presigned URL when sharing is configured, and the expiry is clamped to the 7-day ceiling when a larger value is requested. (`PresignGetObject` signs locally — the fake S3 server is not contacted.)
16. Returns a clear error rather than a panic when sharing is **not** configured.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestPutObject|TestDeleteObject|TestBulkDelete|TestShareObject"` → all `PASS`.

### Step 4: Replace the `buckets_test.go` re-implementation

`backend/router/buckets_test.go`'s `TestBucketInfoConcurrencyIsBounded` re-declares
the semaphore loop **in the test body** and never enters `Buckets.GetAll`, which
measures 0.0%. It also contains the only `time.Sleep` in the backend suite.

Replace it with a test that drives the real handler against a fake admin server
which counts in-flight `GetBucketInfo` requests. Assert:

- peak concurrency ≤ `maxBucketInfoConcurrency`
- one entry in the response per bucket, **in list order**
- a bucket whose `GetBucketInfo` fails falls back to its ID and global aliases
  rather than being dropped

Use an atomic counter and a channel rather than `time.Sleep`.

**Verify**:
```
cd backend && go test -race ./router/ -run TestBucket -v
grep -c "time.Sleep" router/buckets_test.go
```
→ `PASS`; the grep returns **0**.

### Step 5: Confirm the coverage actually moved

```
cd backend && go test ./router/ -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | grep -E "browse.go|buckets.go"
```

Every handler named in Steps 2–4 must be **above 0.0%**. Record the before/after
numbers in your report. This is the plan's headline outcome — if a handler is
still at 0.0%, its tests are not reaching it.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

## Test plan

All new tests live in `backend/router/browse_test.go` and
`backend/router/buckets_test.go`, modelled structurally on the existing
`TestDownloadArchive` success case (fixture + `withDownloadSession` +
direct handler invocation). No new dependencies, no test framework.

## Done criteria

- [ ] Step 6 passes; all packages `ok`
- [ ] `git diff --stat 947879d..HEAD` lists **only** `browse_test.go` and `buckets_test.go` — zero production files
- [ ] `go tool cover -func` shows all eight named `browse.go` handlers **> 0.0%**, and `buckets.go:GetAll` **> 0.0%**
- [ ] `grep -c "time.Sleep" backend/router/buckets_test.go` → **0**
- [ ] `TestDownloadArchive` still passes with its original assertions
- [ ] `grep -rn "newS3Client\|s3API interface" backend/router/*.go` → **no matches** (no seam was introduced)

## STOP conditions

- You are about to modify **any** file that is not a `_test.go` file.
- You are about to introduce an interface or a package-level function variable to
  make the handlers testable — the environment seam already works; see the note
  in "Current state".
- You are about to fix a bug you noticed in `browse.go`. Note it in your report;
  plan 056 owns those fixes and depends on this landing first.
- Two tests share a bucket name and one of them passes only because of a cached
  credential from the other — give every test a unique bucket.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The seam is `t.Setenv` on `API_BASE_URL` and `S3_ENDPOINT_URL`**, because
  both endpoints are resolved at call time rather than at construction. Anything
  that caches the endpoint at startup would break every test here — worth
  flagging in review if it is ever proposed.
- **`s3.NewPresignClient` needs the concrete `*s3.Client`**, which is why an
  interface-based seam is the wrong shape for this package.
- **The credential cache is keyed by bucket with a ~1h TTL and no invalidation**,
  so unique bucket names per test are not stylistic — a shared name makes tests
  order-dependent.
- These tests are the safety net for plan 056. If they are ever weakened, the two
  streaming defects that plan fixes lose their only regression guard.
