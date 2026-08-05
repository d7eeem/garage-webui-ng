# Plan 015: Presigned share links for private buckets (009)

> **Executor instructions**: Follow step by step. Run every verification command
> and confirm the expected result before moving on. Touch only in-scope files.
> On a STOP condition, stop and report. SKIP updating `plans/README.md` — the
> reviewer maintains it.
>
> **Base reset FIRST** (worktrees come up on stale `origin/main`):
> `git checkout -B advisor/015-presigned-share-links main` then
> `git log --oneline -1` — MUST show `d28926f` or a "Merge branch 'advisor/014"
> commit, NOT `ee420fb`. If wrong, STOP.
>
> **Drift check**: `git diff --stat d28926f..HEAD -- backend/ src/pages/buckets/manage/browse/share-dialog.tsx src/hooks/useConfig.ts src/types/garage.ts`

## Status

- **Priority**: P2 (feature)
- **Effort**: M
- **Risk**: MED — mints credential-bearing URLs; reuses privileged per-bucket S3 client
- **Depends on**: none in code; builds on the validated design `plans/design/009-presigned-share-links.md`
- **Category**: direction / feature
- **Planned at**: commit `d28926f` (main after 014), 2026-08-03

## Why this matters

The current share dialog only builds a **public website URL**, which works only
for buckets with website access enabled (it says so in its own warning banner).
This adds **time-limited presigned S3 links** so an operator can share a single
object from a **private** bucket for a bounded window.

**This was validated in a spike** (see the design doc): Garage honors AWS SigV4
presigned GET URLs, the backend's exact SDK path (`s3.NewPresignClient`) produces
working links, expiry is capped at 7 days by SigV4, and a read-only key can sign
them. **The one product decision is settled: add a new config value
`S3_PUBLIC_ENDPOINT_URL`.** A presigned URL's host is whatever endpoint it was
signed against; the internal `S3_ENDPOINT_URL` (`http://garage:3900` in Compose)
is unreachable to an external recipient, and the host is part of the signature so
it cannot be rewritten. Therefore share links must be signed against a
**publicly reachable** S3 endpoint, and the feature is **hidden in the UI when
that endpoint is not configured.**

## Current state

### `backend/utils/garage.go` — the internal S3 endpoint resolver

```go
func (g *garage) GetS3Endpoint() string {
	endpoint := os.Getenv("S3_ENDPOINT_URL")
	if len(endpoint) > 0 {
		return endpoint
	}
	host := strings.Split(g.Config.RPCPublicAddr, ":")[0]
	port := LastString(strings.Split(g.Config.S3API.APIBindAddr, ":"))
	endpoint = fmt.Sprintf("%s:%s", host, port)
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}
	return endpoint
}
```

`os`, `fmt`, `strings` are imported. `GetAdminKey`/`GetS3Region` are siblings.

### `backend/router/browse.go` — `getS3Client` (refactor to accept an endpoint)

```go
func getS3Client(bucket string) (*s3.Client, error) {
	creds, err := getBucketCredentials(bucket)
	if err != nil {
		return nil, fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)
	}
	// Determine endpoint and whether to disable HTTPS
	endpoint := utils.Garage.GetS3Endpoint()
	disableHTTPS := !strings.HasPrefix(endpoint, "https://")
	awsConfig := aws.Config{
		Credentials: creds,
		Region:      utils.Garage.GetS3Region(),
	}
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = true
		o.EndpointOptions.DisableHTTPS = disableHTTPS
		o.EndpointResolver = s3.EndpointResolverFunc(func(region string, opts s3.EndpointResolverOptions) (aws.Endpoint, error) {
			return aws.Endpoint{URL: endpoint, SigningRegion: utils.Garage.GetS3Region()}, nil
		})
	})
	return client, nil
}
```

### `backend/schema/config.go` — the browser-safe config projection (from plan 001)

```go
type ConfigResponse struct {
	S3API S3APIResponse `json:"s3_api"`
	S3Web S3WebResponse `json:"s3_web"`
}
```

`backend/router/config.go` (`Config.GetAll`) returns
`schema.NewConfigResponse(utils.Garage.Config)`.

### `backend/router/router.go` — routes (append the new one)

```go
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	// ... PUT, DELETE, POST bulk, multipart ...
	router.HandleFunc("/", ProxyHandler)
```

Use a **distinct `/share/` prefix** (like `/multipart/`) — do NOT branch inside
`GetOneObject` and do NOT add a literal under `/browse/{bucket}/...`.

### `src/pages/buckets/manage/browse/share-dialog.tsx` — the dialog

Opened via `shareDialog.open({ key, prefix })` from `object-actions.tsx`. The full
object key is `data.prefix + data.key`. It reads `useConfig()`, builds a website
URL, and shows a copy input. (Whole file is short — see it in the repo.)

### `src/types/garage.ts` — `Config` type

```ts
export type Config = {
  s3_api?: S3API;
  s3_web?: S3Web;
};
```

### Conventions

- Backend: handlers on `Browse{}`, `getS3Client`, `r.Context()`,
  `utils.ResponseSuccess`/`ResponseError` (always `return` after error). New env
  reads via `utils.GetEnv`/`os.Getenv`. `s3.NewPresignClient` +
  `PresignGetObject(ctx, in, s3.WithPresignExpires(d))` come from the already-vendored
  `service/s3`.
- Frontend: TanStack Query hooks in `browse/hooks.ts`; `api.get`; daisyUI + local
  `@/components/ui/*`; `sonner` toast; `handleError`.

## Commands

`pnpm` not installed → `npx pnpm@9 <cmd>` (run `npx pnpm@9 install` first).

| Purpose | Command | Expected |
|---|---|---|
| Go build/vet/fmt | `cd backend && go build ./... && go vet ./... && gofmt -l .` | exit 0, no output |
| Go tests | `cd backend && go test -race ./...` | `ok` |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Frontend test | `npx pnpm@9 run test` | all pass |
| Build | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/utils/garage.go` (add `GetS3PublicEndpoint` + `IsSharingEnabled`)
- `backend/utils/garage_test.go` (extend — test the fallback + flag)
- `backend/router/browse.go` (refactor `getS3Client` to take an endpoint; add `ShareObject`)
- `backend/router/router.go` (register `GET /share/{bucket}/{key...}`)
- `backend/schema/config.go` (add `Sharing bool` to `ConfigResponse`)
- `backend/router/config.go` (set `Sharing` from `IsSharingEnabled`)
- `src/types/garage.ts` (add `sharing?: boolean`)
- `src/pages/buckets/manage/browse/hooks.ts` (add `useShareLink`)
- `src/pages/buckets/manage/browse/share-dialog.tsx` (private-link section)
- `README.md` (document `S3_PUBLIC_ENDPOINT_URL` + the sharing caveat)

**Out of scope**: the website-URL half of the dialog (keep it), presigned PUT/upload,
per-link revocation, read-only-key selection (`getBucketCredentials` unchanged —
the link is GET-only regardless), audit logging (that's D4/a later plan).

## Steps

### Step 1: Config — public endpoint + sharing flag

In `garage.go`, add beside `GetS3Endpoint`:

```go
// GetS3PublicEndpoint returns the endpoint used to SIGN share links — it must be
// reachable by link recipients. Falls back to the internal S3 endpoint.
func (g *garage) GetS3PublicEndpoint() string {
	if ep := os.Getenv("S3_PUBLIC_ENDPOINT_URL"); ep != "" {
		return ep
	}
	return g.GetS3Endpoint()
}

// IsSharingEnabled reports whether a public S3 endpoint is explicitly configured.
// Presigned share links are only offered when it is (an internal-only endpoint
// produces links unreachable to external recipients).
func (g *garage) IsSharingEnabled() bool {
	return os.Getenv("S3_PUBLIC_ENDPOINT_URL") != ""
}
```

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 2: Refactor `getS3Client` to accept an endpoint

Change `getS3Client` to delegate to a new `getS3ClientForEndpoint(bucket, endpoint string)`:

```go
func getS3Client(bucket string) (*s3.Client, error) {
	return getS3ClientForEndpoint(bucket, utils.Garage.GetS3Endpoint())
}

func getS3ClientForEndpoint(bucket, endpoint string) (*s3.Client, error) {
	creds, err := getBucketCredentials(bucket)
	if err != nil {
		return nil, fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)
	}
	disableHTTPS := !strings.HasPrefix(endpoint, "https://")
	awsConfig := aws.Config{Credentials: creds, Region: utils.Garage.GetS3Region()}
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = true
		o.EndpointOptions.DisableHTTPS = disableHTTPS
		o.EndpointResolver = s3.EndpointResolverFunc(func(region string, opts s3.EndpointResolverOptions) (aws.Endpoint, error) {
			return aws.Endpoint{URL: endpoint, SigningRegion: utils.Garage.GetS3Region()}, nil
		})
	})
	return client, nil
}
```

All existing callers of `getS3Client` are unchanged. **Verify**:
`cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...` → clean.

### Step 3: The share handler

Add to `browse.go`:

```go
// GET /share/{bucket}/{key...}?expires=<seconds> — presigned GET link.
func (b *Browse) ShareObject(w http.ResponseWriter, r *http.Request) {
	if !utils.Garage.IsSharingEnabled() {
		utils.ResponseErrorStatus(w, fmt.Errorf("sharing is not enabled (set S3_PUBLIC_ENDPOINT_URL)"), http.StatusNotImplemented)
		return
	}
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	const def, max = 3600, 604800 // 1h default, 7d cap (SigV4 ceiling)
	expires := def
	if v, err := strconv.Atoi(r.URL.Query().Get("expires")); err == nil && v > 0 {
		expires = v
	}
	if expires > max {
		expires = max
	}

	client, err := getS3ClientForEndpoint(bucket, utils.Garage.GetS3PublicEndpoint())
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	presign := s3.NewPresignClient(client)
	req, err := presign.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(time.Duration(expires)*time.Second))
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot presign object: %w", err))
		return
	}
	utils.ResponseSuccess(w, map[string]any{
		"url":            req.URL,
		"expiresSeconds": expires,
	})
}
```

`strconv` and `time` are already imported in `browse.go`. **Do NOT** run the
presigned `req.URL` through any key-encoding helper — the SDK already
percent-encodes the key (verified in the spike; double-encoding would break it).

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 4: Register the route

In `router.go`, add (before the catch-all):

```go
	router.HandleFunc("GET /share/{bucket}/{key...}", browse.ShareObject)
```

**Verify**: `cd backend && go build ./...` → exit 0; `grep -c "share" backend/router/router.go` ≥ 1.

### Step 5: Advertise sharing in `/config`

In `schema/config.go`, add to `ConfigResponse`:

```go
	Sharing bool `json:"sharing"`
```

Leave `NewConfigResponse` unchanged (it stays a pure projection of the parsed
TOML; `Sharing` is env-derived). In `backend/router/config.go`, set it in the
handler:

```go
func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	utils.ResponseSuccess(w, resp)
}
```

**Verify**: `cd backend && go build ./... && go test -race ./schema/...` → the
existing `TestNewConfigResponseOmitsSecrets` still passes (Sharing is a bool,
not a secret; it defaults false in `NewConfigResponse`).

### Step 6: Go tests

Extend `backend/utils/garage_test.go`:
- `TestGetS3PublicEndpointFallback` — with `S3_PUBLIC_ENDPOINT_URL` unset (and `S3_ENDPOINT_URL` set via `t.Setenv`), `GetS3PublicEndpoint()` equals `GetS3Endpoint()`; with it set, it returns that value.
- `TestIsSharingEnabled` — false when unset, true when set (`t.Setenv`).

**Verify**: `cd backend && go test -race ./utils/...` → `ok`.

### Step 7: Frontend — config type + hook

`src/types/garage.ts`: add `sharing?: boolean;` to `Config`.

`src/pages/buckets/manage/browse/hooks.ts`: add

```ts
export const useShareLink = (bucket: string) => {
  return useMutation({
    mutationFn: (v: { key: string; expires: number }) =>
      api.get<{ url: string; expiresSeconds: number }>(
        `/share/${bucket}/${v.key}`,
        { params: { expires: v.expires } }
      ),
  });
};
```

(`api.get` with `{ params }` is the existing pattern; the key is passed raw — the
server's `{key...}` decodes it, matching how the browse GET works. If plan 006's
`encodeObjectPath` is used elsewhere for constructed paths, use it here too for
the key segment: `encodeObjectPath(v.key)`.)

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 8: Dialog — private-link section

In `share-dialog.tsx`, when `config?.sharing` is true, render a new section
**above** the website-URL block:

- A label "Private link (expires)".
- An expiry `<select>` (or buttons): 15 min / 1 hour / 24 hours / 7 days → values
  900 / 3600 / 86400 / 604800.
- A "Generate link" button that calls
  `useShareLink(bucketName).mutate({ key: (data.prefix ?? "") + (data.key ?? ""), expires })`.
- On success, put `result.url` in a copy-input (reuse the existing `<Input>` +
  copy `<Button>` pattern already in the file).
- On error, `handleError`.
- Keep the existing website-URL section and its `!bucket.websiteAccess` warning
  unchanged.

When `config?.sharing` is falsy, show nothing new (the dialog is unchanged from
today) — do not render the private-link section at all.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run lint && npx pnpm@9 run build` →
typecheck & build exit 0; lint red only on the pre-existing backlog (confirm
`share-dialog`/`hooks` add no NEW error beyond the file's existing `any` pattern).

### Step 9: Docs

In `README.md` env-var list, add:

```markdown
- `S3_PUBLIC_ENDPOINT_URL`: Publicly-reachable S3 API endpoint used to sign
  object **share links**. Required for the "private link" share option to appear;
  when unset, sharing falls back to website URLs only. Must be reachable by
  whoever receives a link (the internal `S3_ENDPOINT_URL` — e.g. `http://garage:3900`
  in Docker — is not).
```

**Verify**: `grep -c S3_PUBLIC_ENDPOINT_URL README.md` ≥ 1.

### Step 10: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0.

## Test plan

- **Go**: `garage_test.go` covers the endpoint fallback + flag (the pure logic).
  The share handler is SDK plumbing (no brittle mock) — matches existing browse
  handlers. `TestNewConfigResponseOmitsSecrets` must still pass.
- **Frontend**: typecheck + build gate the wiring; no component test required.
- **Live verification is the reviewer's job**: run the backend with
  `S3_PUBLIC_ENDPOINT_URL` set against Garage, `GET /api/share/{bucket}/{key}`,
  confirm a 200 with a `url`, fetch that url with **no auth** → the object; and
  confirm that with the var **unset**, `/config` reports `sharing:false` and the
  endpoint 501s.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` all exit 0
- [ ] `grep -n "ShareObject" backend/router/browse.go` and `grep -n "/share/{bucket}" backend/router/router.go` match
- [ ] `grep -n "GetS3PublicEndpoint\|IsSharingEnabled" backend/utils/garage.go` → both present
- [ ] `grep -n "S3_PUBLIC_ENDPOINT_URL" README.md` matches
- [ ] `git diff --name-only d28926f..HEAD` shows only the 10 in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 015 updated

## STOP conditions

- Base reset shows `ee420fb`.
- Current-state excerpts don't match live code.
- `getS3Client`'s refactor changes behavior for existing callers (they must build
  the identical internal-endpoint client — verify the browse/list/delete paths
  still work in the reviewer's live check).
- The AWS SDK lacks `s3.NewPresignClient`/`WithPresignExpires` (it has them —
  `service/s3 v1.59.0`; report the version if not).
- You find yourself editing `getBucketCredentials` (read-only-key selection is a
  deferred hardening, not this plan) or adding audit logging.

## Maintenance notes

- **`S3_PUBLIC_ENDPOINT_URL` is the deployment contract.** Share links are
  unreachable to recipients unless it points at a publicly-resolvable S3 endpoint;
  the UI self-hides when it's unset, which is the honest behavior. Document
  prominently.
- **Links are bearer capabilities, un-revocable** except by expiry (≤7d) or
  rotating the signing key (kills all links for that bucket). A reviewer should
  confirm the default expiry is conservative (1h) and the 7d cap is enforced.
- **The signing key is the bucket's read+write key** (`getBucketCredentials`).
  The presigned URL is GET-only regardless, so the link can't write; preferring a
  read-only key is a deferred hardening (design doc Q4), tracked separately.
- **Do not re-encode the presigned URL** — the SDK encodes the key; plan 006's
  helpers must not touch `req.URL`.
