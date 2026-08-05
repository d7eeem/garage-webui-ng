# Plan 013: Surface and abort orphaned multipart uploads (D2a)

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If a STOP condition occurs, stop and report.
> When done, update this plan's row in `plans/README.md` — unless a reviewer
> dispatched you and said they maintain the index.
>
> **Drift check (run first)**: `git diff --stat c5543b7..HEAD -- backend/router/browse.go backend/router/router.go src/pages/buckets/manage/overview/`
> If any in-scope file changed since this plan was written, compare the "Current
> state" excerpts to the live code before proceeding; on a mismatch, STOP.

## Status

- **Priority**: P2 (feature — first of the roadmap direction items)
- **Effort**: S–M
- **Risk**: LOW–MED (a destructive action — aborting an in-progress upload — behind a confirmation)
- **Depends on**: none
- **Category**: direction / feature
- **Planned at**: commit `c5543b7`, 2026-08-03

## Why this matters

An interrupted S3 multipart upload leaves an **unfinished upload** on the server
that keeps consuming storage until explicitly aborted. Garage reports these per
bucket, and the web UI already receives the counts — the `Bucket` model carries
`unfinishedMultipartUploads`, `unfinishedMultipartUploadParts`, and
`unfinishedMultipartUploadBytes` (`src/pages/buckets/types.ts:15-17`) — but **the
UI never displays them and offers no way to clean them up**. There is no
`ListMultipartUploads` / `AbortMultipartUpload` code anywhere in the backend.

This plan surfaces the count on the bucket Overview tab and adds a backend
endpoint + UI action to list and abort orphaned uploads. It is the smallest,
self-contained slice of the broader "robust uploads" direction (the full
browser-side multipart upload is a separate, larger plan — do not build that
here).

## Current state

### Files

- `backend/router/browse.go` — all object handlers + `getS3Client(bucket)` (the per-bucket authenticated S3 client). New multipart handlers go here.
- `backend/router/router.go` — route registration.
- `src/pages/buckets/manage/overview/overview-tab.tsx` — the Overview "Usage" card. Where the count + action surface.
- `src/pages/buckets/manage/hooks.ts` — existing bucket Query/mutation hooks (the pattern to follow).
- `src/pages/buckets/manage/context.ts` — `useBucketContext()` gives `{ bucket, bucketName, refetch }`.

### Route registration — `backend/router/router.go` (whole relevant block)

```go
	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)

	// Proxy request to garage api endpoint
	router.HandleFunc("/", ProxyHandler)
```

**Route-collision note (important):** do NOT hang the new routes under
`/browse/{bucket}/...` — that path already has a `{key...}` wildcard, so
`/browse/{bucket}/multipart` would be interpreted as an object named
`multipart`. Use a **separate `/multipart/` prefix**. Go 1.22+ `ServeMux` matches
the most specific pattern, so `GET /multipart/{bucket}` wins over the catch-all
`/`. Register the new routes before (or alongside) the existing ones; ordering
does not matter for correctness, only specificity does.

### Handler conventions — `backend/router/browse.go`

Handlers are methods on `type Browse struct{}`, take `(w http.ResponseWriter, r
*http.Request)`, read path values via `r.PathValue("bucket")`, build a client
with `getS3Client(bucket)`, use `r.Context()` for AWS calls, and end in
`utils.ResponseSuccess(w, data)` or `utils.ResponseError(w, err)` — **always
`return` after `ResponseError`**. Existing exemplar (`DeleteObject`, the
single-object branch):

```go
func (b *Browse) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	// ...
	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	res, err := client.DeleteObject(r.Context(), &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot delete object: %w", err))
		return
	}
	utils.ResponseSuccess(w, res)
}
```

`s3`, `aws`, `fmt`, and the `types` package are already imported in `browse.go`.
The AWS SDK provides `client.ListMultipartUploads(ctx, *s3.ListMultipartUploadsInput)`
and `client.AbortMultipartUpload(ctx, *s3.AbortMultipartUploadInput)` (needs
`Bucket`, `Key`, `UploadId`).

### Overview UI — `src/pages/buckets/manage/overview/overview-tab.tsx` (the Usage card)

```tsx
      <Card className="card-body order-1 md:order-2">
        <Card.Title>Usage</Card.Title>

        <div className="grid grid-cols-2 gap-4 mt-4">
          <div className="flex flex-row gap-3">
            <ChartPie className="mt-1" size={20} />
            <div className="flex-1">
              <p className="text-sm flex items-center gap-1">Storage</p>
              <p className="text-2xl font-medium">{readableBytes(data?.bytes)}</p>
            </div>
          </div>
          <div className="flex flex-row gap-3">
            <ChartScatter className="mt-1" size={20} />
            <div className="flex-1">
              <p className="text-sm flex items-center gap-1">Objects</p>
              <p className="text-2xl font-medium">{data?.objects}</p>
            </div>
          </div>
        </div>
      </Card>
```

`useBucketContext()` provides `bucket` (has `.unfinishedMultipartUploads`,
`.unfinishedMultipartUploadBytes`, `.globalAliases`), `bucketName`, and
`refetch`.

### Frontend conventions

- Data hooks: TanStack Query v5, one hook per endpoint, query keys are arrays.
  Mutations spread `...options` last. See `src/pages/buckets/manage/hooks.ts`
  (`useUpdateBucket`, `useRemoveBucket`) and `src/pages/buckets/manage/browse/hooks.ts`
  (`useDeleteObject`) for exact shape. The `@/` alias maps to `src/`.
- The `api` client (`src/lib/api.ts`): `api.get`, `api.delete(url, { params })`.
- UI: daisyUI via `react-daisyui`; local button wrapper `@/components/ui/button`;
  icons from `lucide-react`; toasts via `sonner`'s `toast`; destructive actions
  confirm with `window.confirm(...)` (see `object-actions.tsx`, `nodes-list.tsx`).

## Commands you will need

`pnpm` is not installed; use `npx pnpm@9 <cmd>` (run `npx pnpm@9 install` first —
fresh worktree).

| Purpose | Command | Expected |
|---|---|---|
| Go build | `cd backend && go build ./...` | exit 0 |
| Go vet | `cd backend && go vet ./...` | exit 0, no output |
| Go fmt | `cd backend && gofmt -l .` | no output |
| Go tests | `cd backend && go test -race ./...` | `ok` per package |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Frontend test | `npx pnpm@9 run test` | all pass |
| Lint | `npx pnpm@9 run lint` | exit 1, pre-existing backlog — confirm none of YOUR files appear |
| Frontend build | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/router/browse.go` (add two handlers + a small helper)
- `backend/router/router.go` (register two routes)
- `backend/router/browse_test.go` (extend — it exists)
- `src/pages/buckets/manage/hooks.ts` (add two hooks)
- `src/pages/buckets/manage/overview/overview-tab.tsx` (surface count + action)
- `src/pages/buckets/manage/overview/multipart-uploads.tsx` (create — the abort UI)

**Out of scope**:
- Browser-side multipart *uploading* (the big "D2b" plan — not this one). Do not
  touch `PutObject` or the upload flow in `browse/actions.tsx`.
- The `{key...}` browse routes and their handlers.
- `getS3Client` / `getBucketCredentials`.

## Git workflow

- Branch: `advisor/013-abort-multipart-uploads`
- Conventional commits (e.g. `feat: list and abort orphaned multipart uploads`).
- Do NOT push or open a PR unless instructed.

## Steps

### Step 1: Backend — list + abort handlers

In `backend/router/browse.go`, add two methods on `Browse`:

```go
// GET /multipart/{bucket} — list unfinished multipart uploads for a bucket.
func (b *Browse) ListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}
	out, err := client.ListMultipartUploads(r.Context(), &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot list multipart uploads: %w", err))
		return
	}
	type upload struct {
		Key       string     `json:"key"`
		UploadID  string     `json:"uploadId"`
		Initiated *time.Time `json:"initiated"`
	}
	uploads := make([]upload, 0, len(out.Uploads))
	for _, u := range out.Uploads {
		uploads = append(uploads, upload{
			Key:       aws.ToString(u.Key),
			UploadID:  aws.ToString(u.UploadId),
			Initiated: u.Initiated,
		})
	}
	utils.ResponseSuccess(w, map[string]any{"uploads": uploads})
}

// DELETE /multipart/{bucket}?key=<key>&uploadId=<id>  — abort one
// DELETE /multipart/{bucket}?all=true                 — abort all
func (b *Browse) AbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	q := r.URL.Query()
	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	abort := func(key, uploadID string) error {
		_, err := client.AbortMultipartUpload(r.Context(), &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		return err
	}

	if q.Get("all") == "true" {
		out, err := client.ListMultipartUploads(r.Context(), &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot list multipart uploads: %w", err))
			return
		}
		aborted := 0
		for _, u := range out.Uploads {
			if err := abort(aws.ToString(u.Key), aws.ToString(u.UploadId)); err != nil {
				utils.ResponseError(w, fmt.Errorf("cannot abort upload: %w", err))
				return
			}
			aborted++
		}
		utils.ResponseSuccess(w, map[string]int{"aborted": aborted})
		return
	}

	key, uploadID := q.Get("key"), q.Get("uploadId")
	if key == "" || uploadID == "" {
		utils.ResponseErrorStatus(w, fmt.Errorf("key and uploadId are required (or all=true)"), http.StatusBadRequest)
		return
	}
	if err := abort(key, uploadID); err != nil {
		utils.ResponseError(w, fmt.Errorf("cannot abort upload: %w", err))
		return
	}
	utils.ResponseSuccess(w, map[string]int{"aborted": 1})
}
```

Add `"time"` to the imports if not present (check the import block; `s3`, `aws`,
`fmt`, `net/http`, `utils` are already there).

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → exit 0, no output.

### Step 2: Register the routes

In `backend/router/router.go`, add after the existing `browse.*` routes and
before the `router.HandleFunc("/", ProxyHandler)` line:

```go
	router.HandleFunc("GET /multipart/{bucket}", browse.ListMultipartUploads)
	router.HandleFunc("DELETE /multipart/{bucket}", browse.AbortMultipartUpload)
```

**Verify**: `cd backend && go build ./...` → exit 0. Then confirm the catch-all
still comes last and the new routes are distinct from `/browse/`:
`grep -n "multipart\|ProxyHandler" backend/router/router.go`.

### Step 3: Frontend hooks

In `src/pages/buckets/manage/hooks.ts`, add (matching the file's existing hook
shape):

```ts
export const useMultipartUploads = (bucketName: string) => {
  return useQuery({
    queryKey: ["multipart", bucketName],
    queryFn: () =>
      api.get<{ uploads: { key: string; uploadId: string; initiated: string }[] }>(
        `/multipart/${bucketName}`
      ),
    enabled: !!bucketName,
  });
};

export const useAbortMultipart = (
  bucketName: string,
  options?: MutationOptions<any, Error, { key: string; uploadId: string } | { all: true }>
) => {
  return useMutation({
    mutationFn: (v) =>
      api.delete(`/multipart/${bucketName}`, {
        params: "all" in v ? { all: true } : { key: v.key, uploadId: v.uploadId },
      }),
    ...options,
  });
};
```

Ensure `useQuery`, `useMutation`, `MutationOptions` are imported from
`@tanstack/react-query` (the file already imports some of these — extend the
import, don't duplicate).

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 4: Overview UI — count + abort action

Create `src/pages/buckets/manage/overview/multipart-uploads.tsx`: a small section
that renders **only when `bucket.unfinishedMultipartUploads > 0`**, shows the
count and `readableBytes(bucket.unfinishedMultipartUploadBytes)`, and offers an
"Abort all" button (guarded by `window.confirm`) wired to
`useAbortMultipart(bucketName).mutate({ all: true })`. On success:
`toast.success`, then `refetch()` from `useBucketContext()` so the count updates.
Route errors through `handleError` (from `@/lib/utils`). Model the component's
structure on `overview-quota.tsx` / `overview-website-access.tsx` (same folder).

Then render it in `overview-tab.tsx` inside the Usage card, after the
Storage/Objects grid:

```tsx
        <MultipartUploadsSection />
```

(import it at the top). It self-hides when there are no orphans, so the common
case shows nothing new.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` → exit 0.

### Step 5: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
```
then
```
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0 (lint stays red on the pre-existing backlog — confirm none of your
files appear: `npx pnpm@9 run lint 2>&1 | grep -E "multipart|overview-tab"` →
nothing).

## Test plan

- **Go**: the handlers are thin AWS-SDK plumbing (like the existing browse
  handlers, which have no direct unit tests). Do **not** invent a brittle mock.
  Add one small table test to `backend/router/browse_test.go` only if you extract
  a pure helper; otherwise note in your report that the handlers are covered by
  build/vet + the reviewer's live check, matching how `DeleteObject` etc. are
  treated. Do not weaken existing tests.
- **Frontend**: no component test is required for this self-hiding section; the
  typecheck + build gates cover wiring. If you extract any pure predicate, add a
  Vitest case beside `src/lib/utils.test.ts`'s style.
- **Live verification is the reviewer's job** (they have a Garage instance):
  create a partial multipart upload, confirm it appears in the count and that
  "Abort all" clears it.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck` exits 0
- [ ] `npx pnpm@9 run test` exits 0 (unchanged count)
- [ ] `npx pnpm@9 run build` exits 0
- [ ] `grep -n "ListMultipartUploads\|AbortMultipartUpload" backend/router/browse.go` → both present
- [ ] `grep -c "multipart" backend/router/router.go` → 2
- [ ] `git diff --name-only c5543b7..HEAD` shows only the 6 in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 013 updated

## STOP conditions

- Current-state excerpts don't match live code (drift).
- Registering `GET /multipart/{bucket}` shadows or is shadowed by an existing
  route (test with `grep` and a build; if Go complains about a conflicting
  pattern, report it).
- `npx pnpm@9 run typecheck`/`test` scripts are absent (means the base is wrong —
  they exist as of `c5543b7`).
- The AWS SDK version in `go.mod` lacks `ListMultipartUploads`/`AbortMultipartUpload`
  on the `s3.Client` (it should — `service/s3 v1.59.0`). If the build can't find
  them, report the SDK version rather than working around it.

## Maintenance notes

- **This is the read+abort half of "robust uploads."** The browser-side
  multipart *upload* (chunking, part URLs, progress) is a separate, larger plan
  (D2b). When that lands, the "abort" action here will also serve as cleanup for
  *this app's own* interrupted uploads, not just other clients'.
- **"Abort all" is destructive** if a legitimate multipart upload is in flight
  from another client. It is behind a `window.confirm`; a reviewer should confirm
  the confirmation copy is explicit ("aborts ALL in-progress multipart uploads
  for this bucket").
- The new `/multipart/` route prefix is deliberately separate from `/browse/` to
  avoid the `{key...}` wildcard collision — keep future multipart routes under it.
