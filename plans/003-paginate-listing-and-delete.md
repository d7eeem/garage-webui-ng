# Plan 003: Paginate object listing and make recursive delete complete

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/router/browse.go src/pages/buckets/manage/browse/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — this plan modifies a destructive code path (recursive object deletion)
- **Depends on**: `plans/002-verification-baseline.md`
- **Category**: bug
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

The S3 `ListObjectsV2` API returns at most 1000 keys per call, and
`DeleteObjects` accepts at most 1000 keys per call. This codebase calls each of
them exactly once, with no pagination loop, in two places that both matter:

1. **Recursive folder delete silently under-deletes.** Deleting a folder
   containing more than 1000 objects lists the first page, deletes those, and
   returns HTTP 200 with a success payload. The operator is told the folder was
   deleted. The remaining objects are still there, still consuming storage.
   Running the delete again removes another 1000. There is no error, no warning,
   and no indication in the UI.

2. **The object browser silently truncates.** The frontend requests
   `limit: 1000` and never sends a continuation token, so a prefix with more
   than 1000 objects displays the first 1000 with no "there is more" affordance.
   The backend already returns `nextToken` and the frontend type already
   declares it — the field has zero readers. Pagination was designed and never
   wired up.

For an object-storage admin UI, "the delete reported success but the data is
still there" is the worst failure mode available. This plan makes both paths
handle the full key space.

**Risk acknowledgement**: you are editing delete code. The tests in step 1 come
*before* the fix for exactly that reason. Do not reorder the steps.

## Current state

### Files

- `backend/router/browse.go` — all four browse handlers. Lines 25-81 list, lines 217-285 delete.
- `backend/schema/browse.go` — the listing response shape.
- `src/pages/buckets/manage/browse/object-list.tsx` — renders the listing; hardcodes `limit: 1000`.
- `src/pages/buckets/manage/browse/hooks.ts` — the three TanStack Query hooks.
- `src/pages/buckets/manage/browse/types.ts` — declares the unused `nextToken`.

### Excerpts

`backend/router/browse.go:25-81` — the listing handler, as it exists today:

```go
func (b *Browse) GetObjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	bucket := r.PathValue("bucket")
	prefix := query.Get("prefix")
	continuationToken := query.Get("next")

	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil {
		limit = 100
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	objects, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket:            aws.String(bucket),
		Prefix:            aws.String(prefix),
		Delimiter:         aws.String("/"),
		MaxKeys:           aws.Int32(int32(limit)),
		ContinuationToken: aws.String(continuationToken),
	})

	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	result := schema.BrowseObjectResult{
		Prefixes:  []string{},
		Objects:   []schema.BrowserObject{},
		Prefix:    prefix,
		NextToken: objects.NextContinuationToken,
	}

	for _, prefix := range objects.CommonPrefixes {
		result.Prefixes = append(result.Prefixes, *prefix.Prefix)
	}

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

	utils.ResponseSuccess(w, result)
}
```

Three defects visible here beyond the missing pagination wiring:

- `ContinuationToken: aws.String(continuationToken)` passes a pointer to an
  **empty string** when `next` is absent, rather than `nil`. An empty
  continuation token is not the same as no continuation token.
- `limit` is unvalidated. `strconv.Atoi` accepts any integer, and
  `int32(limit)` silently overflows for values above 2147483647 — producing a
  negative `MaxKeys`.
- `strings.TrimPrefix(*object.Key, prefix)` shadows the outer `prefix` variable
  name used in the loop above it. Not a bug (the loops are sequential), but note
  it so you do not get confused while editing.

`backend/router/browse.go:217-285` — the delete handler:

```go
func (b *Browse) DeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")
	recursive := r.URL.Query().Get("recursive") == "true"
	isDirectory := strings.HasSuffix(key, "/")

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	// Delete directory and its content
	if isDirectory && recursive {
		objects, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(key),
		})

		if err != nil {
			utils.ResponseError(w, err)
			return
		}

		if len(objects.Contents) == 0 {
			utils.ResponseSuccess(w, true)
			return
		}

		keys := make([]types.ObjectIdentifier, 0, len(objects.Contents))

		for _, object := range objects.Contents {
			keys = append(keys, types.ObjectIdentifier{
				Key: object.Key,
			})
		}

		res, err := client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: keys},
		})

		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot delete object: %w", err))
			return
		}

		if len(res.Errors) > 0 {
			utils.ResponseError(w, fmt.Errorf("cannot delete object: %v", res.Errors[0]))
			return
		}

		utils.ResponseSuccess(w, res)
		return
	}

	// Delete single object
	res, err := client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
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

Note the delete listing has **no `Delimiter`** — that is correct and
intentional: a recursive delete must see keys in nested sub-prefixes, not
collapse them into `CommonPrefixes`. Keep it that way.

`backend/schema/browse.go` — the whole file:

```go
package schema

import "time"

type BrowseObjectResult struct {
	Prefixes  []string        `json:"prefixes"`
	Objects   []BrowserObject `json:"objects"`
	Prefix    string          `json:"prefix"`
	NextToken *string         `json:"nextToken"`
}

type BrowserObject struct {
	ObjectKey    *string    `json:"objectKey"`
	LastModified *time.Time `json:"lastModified"`
	Size         *int64     `json:"size"`
	Url          string     `json:"url"`
}
```

`src/pages/buckets/manage/browse/types.ts` — the whole file. `nextToken` and
`next` are declared and never read:

```ts
export type UseBrowserObjectOptions = Partial<{
  prefix: string;
  limit: number;
  next: string;
}>;

export type GetObjectsResult = {
  prefixes: string[];
  objects: Object[];
  prefix: string;
  nextToken: string | null;
};

export type Object = {
  objectKey: string;
  lastModified: Date;
  size: number;
  url: string;
};

export type PutObjectPayload = {
  key: string;
  file: File | null;
};
```

`src/pages/buckets/manage/browse/hooks.ts:13-22` — the listing hook:

```ts
export const useBrowseObjects = (
  bucket: string,
  options?: UseBrowserObjectOptions
) => {
  return useQuery({
    queryKey: ["browse", bucket, options],
    queryFn: () =>
      api.get<GetObjectsResult>(`/browse/${bucket}`, { params: options }),
  });
};
```

`src/pages/buckets/manage/browse/object-list.tsx:23-33` — the consumer:

```tsx
const ObjectList = ({ prefix, onPrefixChange }: Props) => {
  const { bucketName } = useBucketContext();
  const { data, error, isLoading } = useBrowseObjects(bucketName, {
    prefix,
    limit: 1000,
  });

  const onObjectClick = (object: Object) => {
    window.open(API_URL + object.url + "?view=1", "_blank");
  };
```

`src/pages/buckets/manage/browse/object-list.tsx:60-66` — the empty/error/loading
rows, which you will need to keep working when `data` becomes paged:

```tsx
          ) : !data?.prefixes?.length && !data?.objects?.length ? (
            <tr>
              <td className="text-center py-16" colSpan={3}>
                No objects
              </td>
            </tr>
          ) : null}
```

### Repo conventions to match

- **Go handlers**: methods on an empty struct, ending in `utils.ResponseSuccess`
  or `utils.ResponseError`. Errors are wrapped with `fmt.Errorf("...: %w", err)`
  where context helps — see `browse.go:210` and `:260`.
- **AWS SDK usage**: the repo constructs inputs inline with `aws.String(...)` /
  `aws.Int32(...)` helpers, and passes `context.Background()`. Plan 004 replaces
  those contexts with request-scoped ones — **do not do that here**, it would
  collide. Keep `context.Background()` in this plan.
- **Frontend data fetching**: TanStack Query v5, one hook per endpoint, in a
  sibling `hooks.ts`. Query keys are arrays starting with a string literal:
  `["browse", bucket, options]`. Mutations use `useMutation` with options
  spread last (`...options`).
- **Frontend UI**: daisyUI via `react-daisyui` components (`Table`, `Button`,
  `Alert`, `Loading`), Tailwind utility classes inline. Icons from
  `lucide-react`. Buttons use the local wrapper `@/components/ui/button`, not
  `react-daisyui`'s `Button`, in newer code — but `object-list.tsx` currently
  imports `Table`, `Alert`, and `Loading` from `react-daisyui` directly. Match
  the file you are editing.

## Commands you will need

| Purpose         | Command                                    | Expected on success |
|-----------------|--------------------------------------------|---------------------|
| Go build        | `cd backend && go build ./...`             | exit 0              |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output   |
| Go format check | `cd backend && gofmt -l .`                 | no output           |
| Go tests        | `cd backend && go test -race ./...`        | `ok` per package    |
| Frontend deps   | `pnpm install`                             | exit 0              |
| Typecheck       | `pnpm run typecheck`                       | exit 0              |
| Frontend tests  | `pnpm run test`                            | all pass            |
| Lint            | `pnpm run lint`                            | exit 0              |
| Frontend build  | `pnpm run build`                           | exit 0              |

`typecheck` and `test` are added by plan 002. If they do not exist, plan 002 has
not landed — see STOP conditions.

## Scope

**In scope** (the only files you should modify or create):

- `backend/router/browse.go`
- `backend/router/browse_test.go` (create)
- `src/pages/buckets/manage/browse/hooks.ts`
- `src/pages/buckets/manage/browse/types.ts`
- `src/pages/buckets/manage/browse/object-list.tsx`
- `src/pages/buckets/manage/browse/hooks.test.ts` (create — step 7)

**Out of scope** (do NOT touch, even though they look related):

- `backend/schema/browse.go` — the response shape is already correct;
  `NextToken` exists and is populated. No change needed.
- `getS3Client` / `getBucketCredentials` (`browse.go:287-357`) — plan 004 fixes
  the credential-caching bug there. Editing it here creates a conflict.
- `GetOneObject` and `PutObject` (`browse.go:83-215`) — plan 004 fixes the
  double-response bug in `GetOneObject`; plan 006 fixes URL encoding in both.
  Leave them alone.
- `context.Background()` call sites — plan 004 owns request-scoped contexts.
- `src/pages/buckets/manage/browse/object-actions.tsx` and `actions.tsx` — the
  delete/upload trigger UI. The recursive-delete *call* is unchanged by this
  plan; only the server behavior behind it changes.

## Git workflow

- Branch: `advisor/003-paginate-listing-and-delete`
- Conventional commits, matching the repo. Examples from `git log`:
  `fix: panic when download file`, `feat: properly handle data fetching state on
  view bucket page`.
- Suggested commits: `fix: delete all objects under a prefix, not just the first
  page`, `feat: paginate object browser listing`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Extract the paging logic behind a seam you can test, and test it first

You cannot unit-test the handlers directly without an S3 endpoint, so extract
the two pure decisions into testable functions before changing behavior.

Add to `backend/router/browse.go` (near the bottom, before `getBucketCredentials`):

```go
// maxListKeys is the S3 per-request cap for both ListObjectsV2 results and
// DeleteObjects inputs. Garage follows the S3 API here.
const maxListKeys = 1000

// normalizeListLimit clamps a caller-supplied page size into the range the S3
// API accepts. Invalid, absent, zero, or negative values fall back to 100;
// anything above the S3 cap is clamped to it.
func normalizeListLimit(raw string) int32 {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > maxListKeys {
		return maxListKeys
	}
	return int32(limit)
}

// chunkObjectIdentifiers splits keys into batches no larger than the
// DeleteObjects per-request cap.
func chunkObjectIdentifiers(keys []types.ObjectIdentifier, size int) [][]types.ObjectIdentifier {
	if size <= 0 {
		size = maxListKeys
	}
	var batches [][]types.ObjectIdentifier
	for start := 0; start < len(keys); start += size {
		end := start + size
		if end > len(keys) {
			end = len(keys)
		}
		batches = append(batches, keys[start:end])
	}
	return batches
}
```

Now create `backend/router/browse_test.go`, package `router`, covering both:

- `normalizeListLimit("")` → `100`
- `normalizeListLimit("abc")` → `100`
- `normalizeListLimit("0")` → `100`
- `normalizeListLimit("-5")` → `100`
- `normalizeListLimit("50")` → `50`
- `normalizeListLimit("1000")` → `1000`
- `normalizeListLimit("5000")` → `1000`
- `normalizeListLimit("99999999999")` → `1000` (this is the int32-overflow case
  that currently produces a negative `MaxKeys`)
- `chunkObjectIdentifiers(nil, 1000)` → 0 batches
- 1 key, size 1000 → 1 batch of 1
- 1000 keys, size 1000 → 1 batch of 1000
- 1001 keys, size 1000 → 2 batches, sized 1000 and 1
- 2500 keys, size 1000 → 3 batches, sized 1000, 1000, 500
- every key appears exactly once across all batches, in order (assert by
  reconstructing the flattened slice and comparing)

Use plain `testing`, table-driven, `t.Errorf` on mismatch — matching the
convention plan 002 established in `backend/utils/utils_test.go`.

**Verify**:

```bash
cd backend && go test -race ./router/...
```

→ `ok`, all new tests pass.

### Step 2: Make recursive delete iterate every page

Replace the `if isDirectory && recursive { ... }` block in `DeleteObject`
(currently lines 229-271) with a paginating version. Target shape:

```go
	// Delete directory and its content
	if isDirectory && recursive {
		var deleted int
		var continuationToken *string

		for {
			objects, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				Prefix:            aws.String(key),
				ContinuationToken: continuationToken,
			})
			if err != nil {
				utils.ResponseError(w, err)
				return
			}

			keys := make([]types.ObjectIdentifier, 0, len(objects.Contents))
			for _, object := range objects.Contents {
				keys = append(keys, types.ObjectIdentifier{Key: object.Key})
			}

			for _, batch := range chunkObjectIdentifiers(keys, maxListKeys) {
				res, err := client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{
					Bucket: aws.String(bucket),
					Delete: &types.Delete{Objects: batch},
				})
				if err != nil {
					utils.ResponseError(w, fmt.Errorf("cannot delete object: %w", err))
					return
				}
				if len(res.Errors) > 0 {
					utils.ResponseError(w, fmt.Errorf("cannot delete object: %v", res.Errors[0]))
					return
				}
				deleted += len(res.Deleted)
			}

			if objects.IsTruncated == nil || !*objects.IsTruncated {
				break
			}
			if objects.NextContinuationToken == nil {
				break
			}
			continuationToken = objects.NextContinuationToken
		}

		utils.ResponseSuccess(w, map[string]int{"deleted": deleted})
		return
	}
```

Four things to get right, each of which is a real hazard:

1. **Re-list after each delete round.** The loop lists a page, deletes it, then
   follows the continuation token. Because deleted keys are gone, some S3
   implementations invalidate the token. The `IsTruncated` + `NextContinuationToken`
   guard above handles the common case; if you observe an infinite loop or a
   `NoSuchKey`-style error in manual testing, see STOP conditions — do not
   "fix" it by adding a retry.
2. **`IsTruncated` is a `*bool` in AWS SDK v2**, not a `bool`. Dereferencing it
   without the nil check panics. The excerpt above checks for nil first.
3. **The empty case changes shape.** Previously, an empty prefix returned
   `true`; now it returns `{"deleted": 0}` from the same code path (the loop
   runs zero delete batches). That is the intended simplification — the special
   `len(objects.Contents) == 0` early return is deleted.
4. **The response body changes** from the raw AWS `DeleteObjectsOutput` to
   `{"deleted": N}`. Check the frontend consumer before you do this — see step 4.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./router/...
```

→ exit 0 from all four, no output from `gofmt`.

### Step 3: Fix the listing handler's limit and continuation token

In `GetObjects`, replace the limit parsing and the `ListObjectsV2` call:

```go
	query := r.URL.Query()
	bucket := r.PathValue("bucket")
	prefix := query.Get("prefix")

	limit := normalizeListLimit(query.Get("limit"))

	var continuationToken *string
	if next := query.Get("next"); next != "" {
		continuationToken = aws.String(next)
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	objects, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket:            aws.String(bucket),
		Prefix:            aws.String(prefix),
		Delimiter:         aws.String("/"),
		MaxKeys:           aws.Int32(limit),
		ContinuationToken: continuationToken,
	})
```

The rest of the handler — the `result` construction and both loops — is
unchanged. `NextToken: objects.NextContinuationToken` already flows through.

Note: `strconv` is still imported and used elsewhere in this file
(`browse.go:158`), so do not remove the import.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...
```

→ exit 0 from all four.

### Step 4: Check and update the delete-response consumer

Before shipping step 2's response-shape change, find what reads it:

```bash
grep -rn "useDeleteObject" src/
```

Expected: the definition in `src/pages/buckets/manage/browse/hooks.ts:41-52` and
one call site in `src/pages/buckets/manage/browse/object-actions.tsx`. Read the
call site. The existing mutation is typed `any` and the current `onSuccess`
handler almost certainly ignores the response body and just invalidates the
query — in which case **no change is needed** and you can move on.

If the call site *does* read fields from the response, report it (see STOP
conditions) rather than reshaping the UI here.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 5: Teach the listing hook to paginate

Replace `useBrowseObjects` in `src/pages/buckets/manage/browse/hooks.ts` with an
infinite query. TanStack Query v5 is already a dependency.

```ts
import api from "@/lib/api";
import {
  useInfiniteQuery,
  useMutation,
  UseMutationOptions,
} from "@tanstack/react-query";
import {
  GetObjectsResult,
  PutObjectPayload,
  UseBrowserObjectOptions,
} from "./types";

export const useBrowseObjects = (
  bucket: string,
  options?: UseBrowserObjectOptions
) => {
  return useInfiniteQuery({
    queryKey: ["browse", bucket, options],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      api.get<GetObjectsResult>(`/browse/${bucket}`, {
        params: { ...options, ...(pageParam ? { next: pageParam } : {}) },
      }),
    getNextPageParam: (lastPage) => lastPage.nextToken ?? undefined,
  });
};
```

Two notes:

- `useQuery` is no longer imported by this file; `noUnusedLocals` will fail the
  typecheck if you leave the import. That is the check working.
- `getNextPageParam` returning `undefined` is how v5 signals "no more pages".
  Returning `null` does **not** work — it is treated as a valid page param.

Leave `usePutObject` and `useDeleteObject` exactly as they are.

Then update `src/pages/buckets/manage/browse/types.ts` to reflect that `next` is
now supplied by the query, not the caller. Change `UseBrowserObjectOptions` to:

```ts
export type UseBrowserObjectOptions = Partial<{
  prefix: string;
  limit: number;
}>;
```

Removing `next` here is deliberate: it is now internal to the hook, and leaving
it would invite a caller to set it and silently fight the pagination.

**Verify**: `pnpm run typecheck` → this **will fail** with an error in
`object-list.tsx`, because `data` is now `InfiniteData<GetObjectsResult>` rather
than `GetObjectsResult`. That failure is expected and step 6 fixes it.

### Step 6: Flatten the pages in the list component and add a "Load more" control

In `src/pages/buckets/manage/browse/object-list.tsx`:

Change the hook call and derive flattened views:

```tsx
const ObjectList = ({ prefix, onPrefixChange }: Props) => {
  const { bucketName } = useBucketContext();
  const {
    data,
    error,
    isLoading,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useBrowseObjects(bucketName, { prefix, limit: 1000 });

  const pages = data?.pages ?? [];
  const prefixes = pages.flatMap((page) => page.prefixes);
  const objects = pages.flatMap((page) => page.objects);
  const currentPrefix = pages[0]?.prefix ?? "";
```

Then replace every use of the old shape in the JSX:

| Old | New |
|---|---|
| `!data?.prefixes?.length && !data?.objects?.length` | `!prefixes.length && !objects.length` |
| `data?.prefixes.map((prefix) => ...)` | `prefixes.map((prefix) => ...)` |
| `data?.objects.map((object, idx) => ...)` | `objects.map((object, idx) => ...)` |
| `prefix={data.prefix}` (line 122) | `prefix={currentPrefix}` |
| `idx >= data.objects.length - 2 && data.objects.length > 5` (line 125) | `idx >= objects.length - 2 && objects.length > 5` |

Add a "Load more" row as the last child of `<Table.Body>`, after the objects
map:

```tsx
          {hasNextPage ? (
            <tr>
              <td colSpan={4} className="text-center">
                <Button
                  color="ghost"
                  onClick={() => fetchNextPage()}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? "Loading…" : "Load more"}
                </Button>
              </td>
            </tr>
          ) : null}
```

Import the button from the local wrapper, matching the rest of the browse
directory:

```tsx
import Button from "@/components/ui/button";
```

`colSpan={4}` matches the row width: the table head declares three columns
(`Name`, `Size`, `Last Modified`) and each body row appends a fourth cell via
`<ObjectActions />`.

**Verify**:

```bash
pnpm run typecheck && pnpm run lint && pnpm run build
```

→ exit 0 from all three.

### Step 7: Add a frontend test for page flattening

Create the test as `src/pages/buckets/manage/browse/hooks.test.ts` (Vitest,
configured by plan 002). You are not rendering React here — test the pure
`getNextPageParam` contract by extracting it, or if that is awkward, test the
flattening logic instead.

Simplest approach that is still meaningful: export the page-param resolver from
`hooks.ts` so it can be tested directly.

```ts
export const getNextObjectPageParam = (lastPage: GetObjectsResult) =>
  lastPage.nextToken ?? undefined;
```

and use it in the hook (`getNextPageParam: getNextObjectPageParam`).

Then test:

- `getNextObjectPageParam({ ..., nextToken: "abc" })` → `"abc"`
- `getNextObjectPageParam({ ..., nextToken: null })` → `undefined`
- `getNextObjectPageParam({ ..., nextToken: "" })` → `""` — **check this
  against the real implementation**: `"" ?? undefined` is `""`, which TanStack
  Query treats as a falsy-but-defined page param and *will* keep fetching. If
  the backend can ever return an empty-string `nextToken`, change the
  implementation to `lastPage.nextToken || undefined` and assert `undefined`
  here. The Go side returns `*string`, which serializes to `null` when unset, so
  `??` is correct — but assert the behavior you actually ship.

**Verify**: `pnpm run test` → all pass, including the new cases.

### Step 8: Manual verification (only with a live Garage instance)

Skip this step entirely if no Garage instance is available. Do not stand one up.

If one is available:

1. Create a prefix with more than 1000 objects. Open the browser tab for that
   bucket. Expected: the first 1000 render and a "Load more" button appears at
   the bottom. Clicking it appends the rest and the button disappears when
   exhausted.
2. Delete that folder recursively. Expected: the response is
   `{"deleted": N}` with N equal to the full object count, and re-listing the
   prefix shows nothing.

Report the observed N in your completion note.

## Test plan

New tests:

| File | Tests | Covers |
|---|---|---|
| `backend/router/browse_test.go` | 8 for `normalizeListLimit`, 6 for `chunkObjectIdentifiers` | the limit-overflow bug and the batching that makes the delete complete |
| `src/pages/buckets/manage/browse/hooks.test.ts` | 3 | the pagination termination contract |

Structural pattern: match `backend/utils/utils_test.go` and
`src/lib/utils.test.ts` from plan 002 — table-driven Go tests with `t.Errorf`,
Vitest `describe`/`it`/`expect` with globals enabled.

**Not covered by unit tests, and honestly so**: the paging loop in
`DeleteObject` itself. Testing it properly needs an S3 test double
(the AWS SDK v2 client is a concrete struct, not an interface, so mocking it
means either an interface extraction or an httptest server speaking S3 XML).
Both are larger than this plan. The chunking and limit logic — where the actual
arithmetic bugs live — *are* covered. Note this gap in your completion report.

**Verification**: `cd backend && go test -race ./...` → `ok`.
`pnpm run test` → all pass.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0, including the new `router` tests
- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run lint` exits 0
- [ ] `pnpm run test` exits 0, including the new hook tests
- [ ] `pnpm run build` exits 0
- [ ] `grep -n "IsTruncated" backend/router/browse.go` returns at least one match (the paging loop exists)
- [ ] `grep -n "limit: 1000" src/pages/buckets/manage/browse/object-list.tsx` still matches — the page size stays 1000; pagination is what changed
- [ ] `grep -rn "hasNextPage" src/pages/buckets/manage/browse/object-list.tsx` returns a match
- [ ] `git status` shows only the six in-scope files (plus `plans/README.md`) modified or created — the five above plus `hooks.test.ts` from step 7
- [ ] `plans/README.md` status row for 003 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- `pnpm run typecheck` or `pnpm run test` does not exist as a script. Plan 002
  has not landed; this plan depends on it. Do not build the test infrastructure
  yourself — that is 002's job and doing both creates a conflict.
- The `useDeleteObject` call site in `object-actions.tsx` reads fields from the
  mutation response body. Step 2 changes that body's shape. Report the call site
  and the fields it reads; do not redesign the UI to accommodate it.
- In manual testing (step 8), the recursive delete loops without terminating, or
  the continuation token is rejected after a delete round. That means Garage
  invalidates continuation tokens across mutations, and the correct fix is a
  different algorithm (re-list from the start each round until the prefix is
  empty, with an iteration cap). Report the observed behavior — do not add
  retries or sleeps.
- You find yourself needing to modify `getS3Client`, `getBucketCredentials`,
  `GetOneObject`, or `PutObject`. Those belong to plans 004 and 006.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **The delete loop deletes as it lists.** That is deliberate — buffering every
  key in memory before deleting would blow up on a bucket with millions of
  objects. The consequence is that a mid-loop failure leaves a partial delete,
  and the response is a 500 with no count. If partial-progress reporting matters
  later, the fix is to return `{"deleted": N, "error": "..."}` with a 207-style
  status rather than to buffer.
- **`maxListKeys = 1000` is the S3 spec value, not a tuning knob.** Raising it
  will silently break `DeleteObjects`, which rejects oversized batches.
- **Reviewer should scrutinize**: the `IsTruncated` nil check (AWS SDK v2 uses
  `*bool`), and that the delete listing still has **no** `Delimiter` — adding
  one would make recursive delete skip nested prefixes, reintroducing a worse
  version of the original bug.
- **Interacts with plan 004**: that plan replaces `context.Background()` with
  request-scoped contexts throughout `browse.go`. When it lands, the delete loop
  becomes cancellable mid-flight — which means a user navigating away can now
  abort a delete partway. That is an improvement, but the maintainer should
  decide whether a destructive operation should honor client cancellation at
  all. Flag it during 004's review.
- **Interacts with plan 006**: URL encoding of object keys. The `Url` field
  built at `browse.go:76` is untouched here and still unencoded.
- **Deliberately deferred**: virtualized rendering. "Load more" appends rows to a
  plain table, so a prefix with 50,000 objects will make the DOM slow after
  enough clicks. Windowing is a real improvement but it is a UI-performance
  project, not a correctness fix, and the correctness bug is what was hurting
  users.
