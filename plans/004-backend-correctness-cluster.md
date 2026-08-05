# Plan 004: Fix backend correctness and resilience defects

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/router/browse.go backend/router/buckets.go backend/utils/garage.go backend/main.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED
- **Depends on**: `plans/002-verification-baseline.md`
- **Category**: bug
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

Five independent backend defects, grouped because they all live in the same
three files and share a verification story:

1. **`GetOneObject` writes two HTTP responses** when `HeadObject` fails — a
   missing `return`. The client gets a 500 body immediately followed by `null`,
   and Go logs a "superfluous response.WriteHeader" warning. The user sees a
   nonsense error message.

2. **A malformed `garage.toml` kills the process.** `LoadConfig` calls
   `log.Fatal` on a TOML parse error even though it returns an `error` and the
   caller in `main.go` deliberately handles that error by logging a warning and
   continuing — because every setting can also come from environment variables.
   One stray character in the config file turns a degraded-but-running server
   into a crash loop.

3. **Empty S3 credentials get cached for an hour.** If a bucket has no key with
   both read and write permission, the credential lookup loop never assigns
   anything, and a provider holding empty strings is cached under the bucket's
   name with a one-hour TTL. Every browse, upload, and delete for that bucket
   then fails with an opaque S3 signature error — and granting the missing
   permission in Garage does not help until the hour expires.

4. **No timeouts on any outbound call.** Every request to the Garage admin API
   uses a fresh `&http.Client{}` with a zero timeout, and every S3 call uses
   `context.Background()`. A Garage node that accepts connections but never
   responds pins handler goroutines indefinitely. Client disconnects cancel
   nothing.

5. **Unbounded goroutine fan-out in `GET /buckets`.** One goroutine and one
   admin-API HTTP request per bucket, with no concurrency limit. On a cluster
   with thousands of buckets this is a self-inflicted burst against the admin
   API, and combined with (4) none of it is cancellable.

None of these is exotic. All five are the kind of defect that turns a
recoverable incident into an unrecoverable one.

## Current state

### Files

- `backend/router/browse.go` — object handlers and the credential cache (357 lines).
- `backend/router/buckets.go` — the bucket list handler (54 lines).
- `backend/utils/garage.go` — config loading and the admin-API HTTP client (177 lines).
- `backend/main.go` — startup (49 lines).

### Excerpt 1 — the double response

`backend/router/browse.go:97-107`:

```go
	if !view && !download && !thumbnail {
		object, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			utils.ResponseError(w, err)
		}
		utils.ResponseSuccess(w, object)
		return
	}
```

The `if err != nil` block has no `return`. Execution falls through to
`ResponseSuccess(w, object)` with `object == nil`.

For context, `backend/utils/utils.go:21-35` — both response helpers write
headers and a body unconditionally:

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

### Excerpt 2 — `log.Fatal` in a function that returns an error

`backend/utils/garage.go:24-41`:

```go
func (g *garage) LoadConfig() error {
	path := GetEnv("CONFIG_PATH", "/etc/garage.toml")
	data, err := os.ReadFile(path)

	if err != nil {
		return err
	}

	var cfg schema.Config
	err = toml.Unmarshal(data, &cfg)
	if err != nil {
		log.Fatal(err)
	}

	g.Config = cfg

	return nil
}
```

And the caller, `backend/main.go:21-23`, which clearly intends to survive a
load failure:

```go
	if err := utils.Garage.LoadConfig(); err != nil {
		log.Println("Cannot load garage config!", err)
	}
```

That the file-read error path returns while the parse error path calls
`log.Fatal` is the tell: the inconsistency is the bug.

### Excerpt 3 — the credential cache

`backend/router/browse.go:287-326`:

```go
func getBucketCredentials(bucket string) (aws.CredentialsProvider, error) {
	cacheKey := fmt.Sprintf("key:%s", bucket)
	cacheData := utils.Cache.Get(cacheKey)

	if cacheData != nil {
		return cacheData.(aws.CredentialsProvider), nil
	}

	body, err := utils.Garage.Fetch("/v2/GetBucketInfo?globalAlias="+bucket, &utils.FetchOptions{})
	if err != nil {
		return nil, err
	}

	var bucketData schema.Bucket
	if err := json.Unmarshal(body, &bucketData); err != nil {
		return nil, err
	}

	var key schema.KeyElement

	for _, k := range bucketData.Keys {
		if !k.Permissions.Read || !k.Permissions.Write {
			continue
		}

		body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetKeyInfo?id=%s&showSecretKey=true", k.AccessKeyID), &utils.FetchOptions{})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &key); err != nil {
			return nil, err
		}
		break
	}

	credential := credentials.NewStaticCredentialsProvider(key.AccessKeyID, key.SecretAccessKey, "")
	utils.Cache.Set(cacheKey, credential, time.Hour)

	return credential, nil
}
```

If the loop never `break`s — no key has both `Read` and `Write` — `key` is the
zero `schema.KeyElement`, so `key.AccessKeyID` and `key.SecretAccessKey` are
both `""`. That empty provider is cached at line 323 for a full hour.

### Excerpt 4 — the admin API client

`backend/utils/garage.go:103-149`, the relevant portion:

```go
func (g *garage) Fetch(url string, options *FetchOptions) ([]byte, error) {
	var reqBody io.Reader
	reqUrl := fmt.Sprintf("%s%s", g.GetAdminEndpoint(), url)
	method := http.MethodGet
	...
	req, err := http.NewRequest(method, reqUrl, reqBody)
	if err != nil {
		return nil, err
	}
	...
	// Add auth token
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", g.GetAdminKey()))

	if options.Headers != nil {
		for k, v := range options.Headers {
			req.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	res, err := client.Do(req)
```

A new zero-value `http.Client` per call: no timeout, and no connection reuse
because each client gets its own transport.

Also note `backend/utils/garage.go:151-169`, the error path:

```go
	if res.StatusCode != 200 {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}

		var data map[string]interface{}

		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}

		message := fmt.Sprintf("unexpected status code: %d", res.StatusCode)
		if data["message"] != nil {
			message = fmt.Sprintf("%v", data["message"])
		}

		return nil, errors.New(message)
	}
```

If the error response is not JSON — an HTML 502 from a reverse proxy, say — the
`json.Unmarshal` failure is returned *instead of* the status code, so the
operator sees a JSON syntax error rather than "502".

### Excerpt 5 — the goroutine fan-out

`backend/router/buckets.go:13-54`, the whole handler:

```go
func (b *Buckets) GetAll(w http.ResponseWriter, r *http.Request) {
	body, err := utils.Garage.Fetch("/v2/ListBuckets", &utils.FetchOptions{})
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	var buckets []schema.GetBucketsRes
	if err := json.Unmarshal(body, &buckets); err != nil {
		utils.ResponseError(w, err)
		return
	}

	ch := make(chan schema.Bucket, len(buckets))

	for _, bucket := range buckets {
		go func() {
			body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetBucketInfo?id=%s", bucket.ID), &utils.FetchOptions{})

			if err != nil {
				ch <- schema.Bucket{ID: bucket.ID, GlobalAliases: bucket.GlobalAliases}
				return
			}

			var data schema.Bucket
			if err := json.Unmarshal(body, &data); err != nil {
				ch <- schema.Bucket{ID: bucket.ID, GlobalAliases: bucket.GlobalAliases}
				return
			}

			data.LocalAliases = bucket.LocalAliases
			ch <- data
		}()
	}

	res := make([]schema.Bucket, 0, len(buckets))
	for i := 0; i < len(buckets); i++ {
		res = append(res, <-ch)
	}

	utils.ResponseSuccess(w, res)
}
```

**Important — this is NOT the classic loop-variable capture bug.** `backend/go.mod`
declares `go 1.23.0`, and Go 1.22+ gives each iteration its own `bucket`
variable. The closure is correct. Do not "fix" it by adding a parameter; that
would be churn. The actual problem is that the fan-out is unbounded and
uncancellable, and that results arrive in nondeterministic order.

`backend/go.mod:1-5`, confirming the language version:

```
module khairul169/garage-webui

go 1.23.0

toolchain go1.24.0
```

### Repo conventions to match

- **Handlers** are methods on empty structs, ending in `utils.ResponseSuccess`
  or `utils.ResponseError`. See `backend/router/buckets.go`.
- **Error wrapping** uses `fmt.Errorf("context: %w", err)` — see
  `backend/router/browse.go:210`, `:260`, `:331`.
- **Logging** is stdlib `log`, `log.Println` / `log.Printf`. No logging library.
- **No new dependencies.** Everything this plan needs is in the stdlib or
  already in `go.mod`. `golang.org/x/sync/errgroup` would be idiomatic for
  step 6 but is **not** currently a dependency — use a buffered-channel
  semaphore instead, shown in the step.
- **Tests** are stdlib `testing`, table-driven, `t.Errorf` — the pattern plan
  002 establishes in `backend/utils/utils_test.go`.

## Commands you will need

| Purpose         | Command                                    | Expected on success |
|-----------------|--------------------------------------------|---------------------|
| Go build        | `cd backend && go build ./...`             | exit 0              |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output   |
| Go format check | `cd backend && gofmt -l .`                 | no output           |
| Go tests        | `cd backend && go test -race ./...`        | `ok` per package    |
| Frontend build  | `pnpm run build`                           | exit 0              |

Plan 002 adds the CI that runs these. If `go` is not installed in your
environment, see STOP conditions.

## Scope

**In scope** (the only files you should modify or create):

- `backend/router/browse.go`
- `backend/router/buckets.go`
- `backend/utils/garage.go`
- `backend/utils/garage_test.go` (create)
- `backend/router/buckets_test.go` (create)

**Out of scope** (do NOT touch, even though they look related):

- `backend/main.go` — no change needed. Its error handling at lines 21-23 is
  already correct; step 2 makes `LoadConfig` honor it. (Plan 001 adds a startup
  warning to this file — leave that alone.)
- `backend/utils/utils.go` — the response helpers. They are simplistic but
  changing their signatures ripples through every handler. Plan 004 fixes the
  *caller* that misuses them, not the helpers.
- `backend/router/browse.go` lines 25-81 (`GetObjects`) and 217-285
  (`DeleteObject`) — **plan 003 rewrites both with pagination.** If 003 has
  already landed, those functions will not match this plan's excerpts, which is
  fine — this plan does not touch them. Step 5 does touch their
  `context.Background()` calls; read that step's note carefully.
- `backend/utils/cache.go` — the cache implementation is correct. Step 3 fixes
  what gets *put into* it.
- `backend/router/proxy.go` — the reverse proxy has its own timeout story
  (`httputil.ReverseProxy` uses `http.DefaultTransport`, which does have
  timeouts). Out of scope.
- Anything under `src/` — this is a backend-only plan.

## Git workflow

- Branch: `advisor/004-backend-correctness-cluster`
- Conventional commits. Examples from `git log`: `fix: panic when download
  file`, `fix: remove unused config struct key, fix local aliases parsing error`.
- Suggested commits, one per step: `fix: return after HeadObject error`,
  `fix: do not exit on malformed garage.toml`, `fix: do not cache empty bucket
  credentials`, `fix: add timeouts to garage admin api calls`, `perf: bound
  concurrency when listing buckets`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Fix the double response in `GetOneObject`

In `backend/router/browse.go`, add the missing `return`:

```go
	if !view && !download && !thumbnail {
		object, err := client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			utils.ResponseError(w, err)
			return
		}
		utils.ResponseSuccess(w, object)
		return
	}
```

That is the entire change: one line.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

Then confirm no other handler in the file has the same shape:

```bash
cd backend && grep -n -A2 "utils.ResponseError(w, err)" router/browse.go | grep -B1 -v "return"
```

Read the output and confirm every `ResponseError` call is followed by `return`.
This is a judgment read, not an automated gate — do it carefully, there are
about ten call sites.

### Step 2: Make `LoadConfig` return its parse error instead of exiting

In `backend/utils/garage.go`, replace the `log.Fatal(err)` at line 35:

```go
	var cfg schema.Config
	err = toml.Unmarshal(data, &cfg)
	if err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}
```

`fmt` is already imported in this file. Check whether `log` is still used
elsewhere in `garage.go` after this edit:

```bash
cd backend && grep -n "log\." utils/garage.go
```

If there are no remaining uses, remove the `"log"` import — an unused import is
a compile error in Go, so the build will tell you either way.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

Add a test in `backend/utils/garage_test.go` (package `utils`):

- `TestLoadConfigReturnsErrorOnMalformedToml` — write a file containing invalid
  TOML (e.g. `"this is not = = valid toml"`) into `t.TempDir()`, set
  `CONFIG_PATH` to it with `t.Setenv`, call `Garage.LoadConfig()`, and assert a
  non-nil error is returned. Before this fix the test process would exit; after
  it, the error comes back. This is the regression test that matters.
- `TestLoadConfigReturnsErrorOnMissingFile` — point `CONFIG_PATH` at a
  nonexistent path, assert a non-nil error.
- `TestLoadConfigParsesValidToml` — write a minimal valid `garage.toml` with an
  `[s3_api]` section containing `root_domain`, assert no error and that
  `Garage.Config.S3API.RootDomain` matches.

Note: `Garage` is a package-level singleton, so these tests mutate shared state.
Run them in the order written and do not add `t.Parallel()`.

**Verify**: `cd backend && go test -race ./utils/...` → `ok`, 3 new tests pass.

### Step 3: Stop caching empty credentials

In `backend/router/browse.go`, replace the tail of `getBucketCredentials` so a
missing key is an error rather than a cached empty provider. Target shape:

```go
	var key schema.KeyElement
	var found bool

	for _, k := range bucketData.Keys {
		if !k.Permissions.Read || !k.Permissions.Write {
			continue
		}

		body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetKeyInfo?id=%s&showSecretKey=true", k.AccessKeyID), &utils.FetchOptions{})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &key); err != nil {
			return nil, err
		}
		found = true
		break
	}

	if !found || key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return nil, fmt.Errorf(
			"no access key with read and write permission is assigned to bucket %q; "+
				"grant a key read+write access to this bucket in the Permissions tab",
			bucket,
		)
	}

	credential := credentials.NewStaticCredentialsProvider(key.AccessKeyID, key.SecretAccessKey, "")
	utils.Cache.Set(cacheKey, credential, time.Hour)

	return credential, nil
}
```

Two properties this gives you:

- Nothing is cached on the failure path, so granting the permission in Garage
  takes effect on the next request instead of up to an hour later.
- The error message is actionable and names the fix, replacing an opaque S3
  signature failure.

**Do not** shorten the cache TTL as an alternative. The TTL is fine for the
success case; caching a failure was the bug.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...
```

→ exit 0 from all four.

Manual check of the error text reaching the client: `getS3Client` at
`browse.go:328-332` already wraps this with
`fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)`, so the
user-visible message will be that prefix plus your text. Read it once and
confirm it is not doubled-up nonsense.

### Step 4: Give the admin API client a timeout and reuse it

In `backend/utils/garage.go`, replace the per-call client with a package-level
one. Add near the top of the file, after the `var Garage = &garage{}`
declaration at line 22:

```go
// adminHTTPClient is shared across all admin API calls so connections are
// reused. The timeout bounds a Garage node that accepts connections but never
// responds; without it a stalled node pins a handler goroutine forever.
var adminHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}
```

Add `"time"` to the import block.

Then in `Fetch`, replace:

```go
	client := &http.Client{}
	res, err := client.Do(req)
```

with:

```go
	res, err := adminHTTPClient.Do(req)
```

While you are in this function, also fix the non-JSON error-response path
(lines 151-169). Replace the `json.Unmarshal` error return so the status code
survives:

```go
	if res.StatusCode != 200 {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("unexpected status code: %d (cannot read body: %w)", res.StatusCode, err)
		}

		message := fmt.Sprintf("unexpected status code: %d", res.StatusCode)

		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err == nil && data["message"] != nil {
			message = fmt.Sprintf("%v", data["message"])
		}

		return nil, errors.New(message)
	}
```

Now a non-JSON error body yields `unexpected status code: 502` instead of a JSON
syntax error.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...
```

→ exit 0 from all four.

Add to `backend/utils/garage_test.go`:

- `TestFetchReturnsStatusCodeForNonJsonErrorBody` — stand up an
  `httptest.NewServer` that responds 502 with `Content-Type: text/html` and body
  `<html>bad gateway</html>`, set `API_BASE_URL` to `server.URL` via `t.Setenv`
  (this is what `GetAdminEndpoint()` reads first, per `garage.go:44-47`), call
  `Garage.Fetch("/anything", &FetchOptions{})`, and assert the returned error
  message contains `502`.
- `TestFetchReturnsApiMessageForJsonErrorBody` — same, but respond 400 with
  `{"message":"bucket not found"}` and assert the error message is
  `bucket not found`.
- `TestFetchSucceeds` — respond 200 with `{"ok":true}`, assert no error and that
  the returned bytes unmarshal to a map containing `ok`.

`httptest` is stdlib — no new dependency.

**Verify**: `cd backend && go test -race ./utils/...` → `ok`, 6 tests total in
this file.

### Step 5: Make S3 calls use the request context

In `backend/router/browse.go`, replace `context.Background()` with `r.Context()`
in every handler where a `*http.Request` named `r` is in scope. As of the plan's
base commit those are at lines 42, 98, 109, 201, 231, 254, and 274.

**Coordination note**: if plan 003 has already landed, lines 231/254/274 have
been restructured into a paging loop — apply the same substitution inside that
loop. Find every remaining call site mechanically:

```bash
cd backend && grep -n "context.Background()" router/browse.go
```

Substitute each one, then re-run the grep. Expected final result: **no matches
in `router/browse.go`**.

Leave `getBucketCredentials` and `getS3Client` alone — they take no request and
call `utils.Garage.Fetch`, which is bounded by step 4's client timeout.

The `"context"` import may become unused. The compiler will tell you; remove it
if so.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...
```

→ exit 0 from all four, and `grep -c "context.Background()" router/browse.go`
returns 0.

### Step 6: Bound the concurrency in `GET /buckets`

Rewrite the fan-out in `backend/router/buckets.go` with a fixed worker limit and
deterministic result ordering. Target shape:

```go
// maxBucketInfoConcurrency bounds how many GetBucketInfo calls run at once.
// Without a bound, a cluster with thousands of buckets fires one admin API
// request per bucket simultaneously.
const maxBucketInfoConcurrency = 8

func (b *Buckets) GetAll(w http.ResponseWriter, r *http.Request) {
	body, err := utils.Garage.Fetch("/v2/ListBuckets", &utils.FetchOptions{})
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	var buckets []schema.GetBucketsRes
	if err := json.Unmarshal(body, &buckets); err != nil {
		utils.ResponseError(w, err)
		return
	}

	res := make([]schema.Bucket, len(buckets))
	sem := make(chan struct{}, maxBucketInfoConcurrency)
	var wg sync.WaitGroup

	for i, bucket := range buckets {
		wg.Add(1)
		sem <- struct{}{}

		go func(i int, bucket schema.GetBucketsRes) {
			defer wg.Done()
			defer func() { <-sem }()

			fallback := schema.Bucket{ID: bucket.ID, GlobalAliases: bucket.GlobalAliases}

			body, err := utils.Garage.Fetch(fmt.Sprintf("/v2/GetBucketInfo?id=%s", bucket.ID), &utils.FetchOptions{})
			if err != nil {
				res[i] = fallback
				return
			}

			var data schema.Bucket
			if err := json.Unmarshal(body, &data); err != nil {
				res[i] = fallback
				return
			}

			data.LocalAliases = bucket.LocalAliases
			res[i] = data
		}(i, bucket)
	}

	wg.Wait()
	utils.ResponseSuccess(w, res)
}
```

Add `"sync"` to the imports.

Three deliberate choices:

- **Writing to `res[i]` from goroutines is safe** — each goroutine owns a
  distinct index and `wg.Wait()` happens-before the read. `go test -race` will
  confirm. This also makes the response order match `ListBuckets` order, which
  the channel version did not.
- **Parameters are passed explicitly** (`func(i int, bucket schema.GetBucketsRes)`)
  even though Go 1.22+ per-iteration scoping makes it unnecessary. Here it is
  not a bug fix — it is documentation that these are per-goroutine values.
- **`sem <- struct{}{}` is outside the goroutine**, so the loop itself blocks
  once 8 are in flight. Putting it inside would spawn all N goroutines
  immediately and defeat the point.

Add `backend/router/buckets_test.go` (package `router`) with a test that does
not need a Garage instance:

- `TestBucketInfoConcurrencyIsBounded` — a pure test of the semaphore pattern:
  run 100 tasks through a `chan struct{}` of capacity 8 while tracking peak
  concurrency with `sync/atomic`, assert the peak never exceeds 8 and that all
  100 complete. This tests the pattern you just wrote, not the handler.

If that feels too indirect to be worth writing, say so in your report and skip
it — an honest "not meaningfully testable without an S3/admin fixture" is better
than a test that asserts nothing. The `-race` run on the handler is the real
gate.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...
```

→ exit 0 from all four.

### Step 7: Full-repo verification

```bash
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
```

→ exit 0.

```bash
pnpm run build
```

→ exit 0. (Nothing frontend changed, but confirm you did not break the build.)

## Test plan

New tests:

| File | Tests | Covers |
|---|---|---|
| `backend/utils/garage_test.go` | 3 for `LoadConfig`, 3 for `Fetch` | step 2 and step 4; `httptest` server, no live Garage needed |
| `backend/router/buckets_test.go` | 1 | the bounded-concurrency pattern from step 6 |

Structural pattern: match `backend/utils/utils_test.go` and
`backend/utils/cache_test.go` from plan 002 — stdlib `testing`, table-driven
where there are multiple cases, `t.Setenv` for environment isolation,
`t.TempDir()` for files.

**Not covered, and honestly so:**

- Step 1 (the missing `return`) — reaching it requires a failing `HeadObject`
  against a real S3 endpoint. The fix is one line and visually obvious; the
  compile gate plus a read of the diff is the verification. Note this in your
  report.
- Step 3 (credential caching) — `getBucketCredentials` calls the package-level
  `utils.Garage` singleton and constructs a concrete AWS provider. Testing it
  needs either dependency injection into `browse.go` or an `httptest` server
  standing in for the admin API. The latter is feasible (set `API_BASE_URL` and
  serve canned `GetBucketInfo` JSON) — **write it if you can do so in under
  ~40 lines**; skip and report if it balloons.
- Step 5 (request contexts) — behavioral change under client disconnect; needs
  an integration harness.

**Verification**: `cd backend && go test -race ./...` → `ok` for `utils`,
`router`, `schema`.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0, including all new tests
- [ ] `cd backend && grep -c "context.Background()" router/browse.go` returns `0`
- [ ] `cd backend && grep -c "log.Fatal" utils/garage.go` returns `0`
- [ ] `cd backend && grep -n "http.Client{}" utils/garage.go` returns no matches
- [ ] `cd backend && grep -n "Timeout:" utils/garage.go` returns a match
- [ ] `cd backend && grep -n "maxBucketInfoConcurrency" router/buckets.go` returns a match
- [ ] `pnpm run build` exits 0
- [ ] `git status` shows only the five in-scope files (plus `plans/README.md`) modified or created
- [ ] `plans/README.md` status row for 004 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above,
  **except** for `GetObjects` / `DeleteObject` in `browse.go` if plan 003 has
  already landed — that divergence is expected and step 5 tells you how to
  handle it.
- `go` is not installed in your environment. Report this — do not install a
  toolchain, and do not skip verification gates.
- `go test -race` reports a data race in the rewritten `buckets.go`. That means
  the indexed-write pattern is not what you implemented; report the race trace
  rather than adding a mutex on top.
- Step 3's error path turns out to be reachable on a *healthy* bucket in your
  testing — i.e. buckets that work today start returning "no access key with
  read and write permission". That would mean Garage reports permissions in a
  shape this code misreads, which is a different and larger bug. Report it.
- You find yourself wanting to change `utils.ResponseError` / `ResponseSuccess`
  signatures, or to touch `backend/main.go`. Both are out of scope.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **`utils.ResponseError` cannot detect a second write.** Step 1 fixes the one
  known instance of a double response, but the helper will happily let it happen
  again. If this class of bug recurs, the durable fix is to make the helpers
  return a sentinel the caller must return, or to switch handlers to a
  `func(w, r) error` signature with a wrapper — a worthwhile refactor, and
  deliberately not attempted here because it touches every handler.
- **The 30-second admin timeout is a guess, not a measurement.** It is generous
  for a healthy cluster and short enough to fail fast. If large-cluster
  operations (`ApplyClusterLayout` on a big layout) start timing out, raise it
  or make it configurable via an env var — do not remove it.
- **`maxBucketInfoConcurrency = 8` is likewise a starting point.** It bounds
  admin-API load; tune against a real cluster if bucket-list latency becomes a
  complaint.
- **Request contexts now cancel S3 operations.** A user who navigates away
  mid-delete will abort the delete. See plan 003's maintenance note — whether a
  destructive operation should honor client cancellation is a product decision
  the maintainer should make explicitly. Right now it does.
- **Reviewer should scrutinize**: the `res[i] = ...` indexed writes in
  `buckets.go` (correct, but only because indices are disjoint and `wg.Wait()`
  synchronizes), and that step 3 does not cache on the failure path.
- **Deliberately deferred**: `errgroup` for the fan-out. It is the idiomatic
  choice and would let one failed `GetBucketInfo` cancel the rest — but the
  current behavior deliberately degrades to a partial bucket record instead of
  failing the whole list, which is better UX. Adding `golang.org/x/sync` for a
  worse behavior is not a trade worth making.
