# Plan 014: Bulk delete of selected objects (011)

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If a STOP condition occurs, stop and report.
> SKIP updating `plans/README.md` — your reviewer maintains it.
>
> **Drift check (run first, after the base reset below)**:
> `git diff --stat 15d0882..HEAD -- backend/router/browse.go src/pages/buckets/manage/browse/`
> If any in-scope file changed since this plan was written, compare the "Current
> state" excerpts to live code; on a mismatch, STOP.

## Status

- **Priority**: P2 (feature)
- **Effort**: M (backend-light, frontend-heavy)
- **Risk**: MED — a destructive multi-object operation
- **Depends on**: 003 (pagination + `chunkObjectIdentifiers`, shipped in 1.2.0) and 013 (merged into `main`; no code overlap but shares `browse.go`)
- **Category**: direction / feature
- **Planned at**: commit `15d0882` (main after 013), 2026-08-03

## Why this matters

Every object operation in the browser is one row at a time. This adds
**bulk delete of an explicit, visible selection**: checkboxes in the object
table, a selection toolbar, a confirm modal, and a `POST /browse/{bucket}`
endpoint that deletes a list of keys and reports per-key results. It reuses
003's `chunkObjectIdentifiers` (S3 1000-key batch cap) so the server chunks
automatically.

**Two hard design decisions (from the spike, do not relitigate):**
1. **Explicit selection only.** Select **all loaded** rows (what's fetched), never
   "all matching the prefix" — a client that walks pages to select everything
   reintroduces the silent-truncation failure 003 fixed. For "delete the whole
   folder," users keep the existing recursive folder delete (a folder row's
   trash action). **Folders are excluded from multi-select.**
2. **Report the full per-key error set.** This plan also fixes an existing gap:
   the recursive delete currently surfaces only `res.Errors[0]` and drops the
   rest. Both the bulk endpoint and the existing recursive delete must return the
   complete errors list.

## Current state

### Backend — `backend/router/browse.go`

The recursive-delete inner loop (inside `DeleteObject`, the `if isDirectory &&
recursive` block) — note the **`res.Errors[0]`** that drops all but the first
error:

```go
			for _, batch := range chunkObjectIdentifiers(keys, maxListKeys) {
				res, err := client.DeleteObjects(r.Context(), &s3.DeleteObjectsInput{
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
```

Reusable helpers already in the file:
- `const maxListKeys = 1000` (line ~381)
- `func chunkObjectIdentifiers(keys []types.ObjectIdentifier, size int) [][]types.ObjectIdentifier` (line ~399) — batches at the S3 cap.
- `getS3Client(bucket)` — the per-bucket authenticated client.

Imports already present: `s3`, `aws`, `types` (`.../service/s3/types`), `fmt`,
`encoding/json`, `net/http`, `utils`.

### Route registration — `backend/router/router.go`

```go
	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)
	router.HandleFunc("GET /multipart/{bucket}", browse.ListMultipartUploads)
	router.HandleFunc("DELETE /multipart/{bucket}", browse.AbortMultipartUpload)
	// Proxy...
	router.HandleFunc("/", ProxyHandler)
```

`POST /browse/{bucket}` is **method-distinct** from `GET /browse/{bucket}` (the
listing) — the spike verified this registers cleanly against the `{key...}`
routes with no collision. Do NOT invent a `/browse/{bucket}/bulk-delete` literal.

### Frontend — the selection lives in `browse-tab.tsx`

`browse-tab.tsx` owns `curPrefix` and has an effect that runs on `curPrefix`
change. Selection state goes here so **navigating prefixes clears it**:

```tsx
const BrowseTab = () => {
  const { bucket } = useBucketContext();
  const [searchParams, setSearchParams] = useSearchParams();
  const [prefixHistory, setPrefixHistory] = useState<string[]>(getInitialPrefixes(searchParams));
  const [curPrefix, setCurPrefix] = useState(prefixHistory.length - 1);

  useEffect(() => {
    const prefix = prefixHistory[curPrefix] || "";
    const newParams = new URLSearchParams(searchParams);
    newParams.set("prefix", prefix);
    setSearchParams(newParams);
  }, [curPrefix]);
  // ...
  // renders <ObjectListNavigator .../> and <ObjectList prefix={...} onPrefixChange={gotoPrefix} />
```

`object-list.tsx` (post-003) flattens infinite-query pages into `objects` and
`prefixes`, renders a `<Table>` with head `Name / Size / Last Modified`, maps
`prefixes` (folders) then `objects` (files), and has a "Load more" row gated on
`hasNextPage`. Each object row ends in `<ObjectActions .../>` (a `<td>`), and
`object.objectKey` is the **prefix-relative** display name — the **full** key is
`prefix + object.objectKey` (that is what the delete endpoint needs; see how
`object-actions.tsx` builds `key: prefix + object.objectKey`).

### Conventions

- Backend handlers: methods on `Browse{}`, `getS3Client`, `r.Context()`,
  `utils.ResponseSuccess`/`ResponseError` (always `return` after error), decode
  JSON with `json.NewDecoder(r.Body).Decode(&body)` (see `Auth.Login`).
- Frontend: TanStack Query v5; hooks in `browse/hooks.ts` (see `useDeleteObject`);
  daisyUI + local `@/components/ui/button`, `@/components/ui/checkbox`; `sonner`
  `toast`; `useDisclosure` (`@/hooks/useDisclosure`) for local dialog state
  (see `browse/actions.tsx`'s `CreateFolderAction`); `handleError` from
  `@/lib/utils`; the `@/` alias maps to `src/`.
- Query invalidation: `queryClient.invalidateQueries({ queryKey: ["browse", bucketName] })`.

## Commands you will need

`pnpm` not installed → `npx pnpm@9 <cmd>` (run `npx pnpm@9 install` first).

| Purpose | Command | Expected |
|---|---|---|
| Go build/vet/fmt | `cd backend && go build ./... && go vet ./... && gofmt -l .` | exit 0, no output |
| Go tests | `cd backend && go test -race ./...` | `ok` per package |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Frontend test | `npx pnpm@9 run test` | all pass |
| Lint | `npx pnpm@9 run lint` | exit 1 (pre-existing backlog); confirm none of YOUR files appear |
| Build | `npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/router/browse.go` (new `BulkDeleteObjects` handler + fix the existing recursive-delete error reporting)
- `backend/router/router.go` (register `POST /browse/{bucket}`)
- `backend/router/browse_test.go` (extend — test the error-aggregation helper)
- `src/pages/buckets/manage/browse/hooks.ts` (add `useBulkDelete`)
- `src/pages/buckets/manage/browse/types.ts` (bulk-delete request/response types)
- `src/pages/buckets/manage/browse/browse-tab.tsx` (selection state, cleared on prefix change)
- `src/pages/buckets/manage/browse/object-list.tsx` (checkbox column + select-all-loaded header)
- `src/pages/buckets/manage/browse/bulk-actions.tsx` (create — the selection toolbar + confirm modal + result panel)

**Out of scope**:
- Move/rename/copy (a separate, larger feature).
- "Select all matching prefix" / any cross-page selection walking. **Do not build it.**
- Folder (common-prefix) multi-select — folders keep the single recursive-delete action.
- Multipart routes/handlers from 013.

## Git workflow

- **Base reset first** (worktrees come up on stale `origin/main`): run
  `git checkout -B advisor/014-bulk-object-delete main` and confirm
  `git log --oneline -1` shows `15d0882` (or newer main with 013). If it shows
  `ee420fb`, STOP.
- Conventional commits (e.g. `feat: bulk-delete selected objects`).
- Do NOT push.

## Steps

### Step 1: Backend — extract an error-formatting helper and fix the existing delete

Add a small pure helper near `chunkObjectIdentifiers` in `browse.go`:

```go
// deleteErrorsToList converts S3 per-object delete errors into a flat,
// JSON-friendly slice. Reports ALL failures, not just the first.
func deleteErrorsToList(errs []types.Error) []map[string]string {
	out := make([]map[string]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, map[string]string{
			"key":     aws.ToString(e.Key),
			"message": aws.ToString(e.Message),
		})
	}
	return out
}
```

Then in the existing recursive-delete loop, replace the
`if len(res.Errors) > 0 { ... res.Errors[0] ... return }` block so it
**accumulates** errors across batches instead of aborting on the first, and
returns them in the success payload. Target shape for that block:

```go
				deleted += len(res.Deleted)
				failed = append(failed, deleteErrorsToList(res.Errors)...)
```

Declare `failed := []map[string]string{}` (NOT `var failed []map[string]string`)
alongside `var deleted int` at the top of the recursive branch — it MUST be a
non-nil slice so that with no failures it marshals to JSON `[]`, not `null` (a
nil slice serializes to `null`, which would crash a frontend consumer doing
`errors.length`). Change the final success line from
`map[string]int{"deleted": deleted}` to:

```go
		utils.ResponseSuccess(w, map[string]any{"deleted": deleted, "errors": failed})
```

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l .` → clean.

### Step 2: Backend — the bulk-delete handler

Add to `browse.go`:

```go
// POST /browse/{bucket}  body: {"action":"delete","keys":["a","b/c",...]}
func (b *Browse) BulkDeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var body struct {
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}
	if body.Action != "delete" {
		utils.ResponseErrorStatus(w, fmt.Errorf("unsupported action %q", body.Action), http.StatusBadRequest)
		return
	}
	if len(body.Keys) == 0 {
		utils.ResponseSuccess(w, map[string]any{"deleted": 0, "errors": []any{}})
		return
	}
	if len(body.Keys) > maxListKeys {
		utils.ResponseErrorStatus(w, fmt.Errorf("too many keys: %d (max %d)", len(body.Keys), maxListKeys), http.StatusBadRequest)
		return
	}

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	ids := make([]types.ObjectIdentifier, 0, len(body.Keys))
	for _, k := range body.Keys {
		ids = append(ids, types.ObjectIdentifier{Key: aws.String(k)})
	}

	deleted := 0
	failed := []map[string]string{} // non-nil: marshals to [] not null on the happy path
	for _, batch := range chunkObjectIdentifiers(ids, maxListKeys) {
		res, err := client.DeleteObjects(r.Context(), &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: batch},
		})
		if err != nil {
			utils.ResponseError(w, fmt.Errorf("cannot delete objects: %w", err))
			return
		}
		deleted += len(res.Deleted)
		failed = append(failed, deleteErrorsToList(res.Errors)...)
	}
	utils.ResponseSuccess(w, map[string]any{"deleted": deleted, "errors": failed})
}
```

The `maxListKeys` cap means at most one batch here, but the loop keeps it uniform
with the recursive path and safe if the cap is later raised.

**Verify**: `cd backend && go build ./... && go vet ./... && gofmt -l . && go test -race ./...` → clean.

### Step 3: Register the route

In `router.go`, add after the existing browse routes (before the multipart lines
is fine):

```go
	router.HandleFunc("POST /browse/{bucket}", browse.BulkDeleteObjects)
```

**Verify**: `cd backend && go build ./...` → exit 0. Confirm it coexists with
`GET /browse/{bucket}`: `grep -n "POST /browse/{bucket}\|GET /browse/{bucket}\"" backend/router/router.go`.

### Step 4: Go test for the error aggregation

Extend `backend/router/browse_test.go` with `TestDeleteErrorsToList`: pass a
`[]types.Error{{Key: aws.String("a"), Message: aws.String("denied")}, {Key: aws.String("b"), Message: aws.String("gone")}}` and assert the result has 2 entries with the right key/message (proves ALL errors are reported, the Q4 fix). Match the table-test style already in the file. `import` `types` and `aws` if not present.

**Verify**: `cd backend && go test -race ./router/...` → `ok`, new test passes.

### Step 5: Frontend types + hook

In `browse/types.ts` add:

```ts
export type BulkDeleteResult = {
  deleted: number;
  errors: { key: string; message: string }[];
};
```

In `browse/hooks.ts` add (mirroring `useDeleteObject`):

```ts
export const useBulkDelete = (
  bucket: string,
  options?: UseMutationOptions<BulkDeleteResult, Error, string[]>
) => {
  return useMutation({
    mutationFn: (keys) =>
      api.post<BulkDeleteResult>(`/browse/${bucket}`, {
        body: { action: "delete", keys },
      }),
    ...options,
  });
};
```

Ensure `UseMutationOptions` and `BulkDeleteResult` are imported.

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 6: Selection state in `browse-tab.tsx`

Add a `Set<string>` of full keys, cleared whenever `curPrefix` changes:

```tsx
  const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => { setSelected(new Set()); }, [curPrefix]);
```

Pass `selected`, `setSelected`, and the current prefix down to `<ObjectList>` and
render a new `<BulkActions>` toolbar (step 8) when `selected.size > 0`. Thread the
props; do not lift anything else.

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 7: Checkbox column in `object-list.tsx`

- Add a header cell before `Name`: a "select all loaded" checkbox (checked when
  every loaded object key is in `selected`; toggles all loaded object keys).
  Use `@/components/ui/checkbox`.
- Add a leading cell to each **object** row (not folder rows) with a checkbox
  bound to whether `prefix + object.objectKey` is in `selected`; toggling it
  adds/removes that full key. Folder (`prefixes.map`) rows get an **empty**
  leading cell — folders are not selectable.
- Bump the `colSpan` on the empty/loading/error rows and the "Load more" row by 1
  to account for the new column.

Selection is controlled from `browse-tab.tsx` (props `selected` / `setSelected` /
`prefix`). Keep the existing per-row `<ObjectActions>` unchanged.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run build` → exit 0.

### Step 8: `bulk-actions.tsx` — toolbar, confirm modal, result panel

Create `src/pages/buckets/manage/browse/bulk-actions.tsx`:

- A toolbar (rendered by `browse-tab.tsx` when `selected.size > 0`) showing the
  count and a "Delete selected" button.
- A confirmation **modal** (`react-daisyui` `Modal` + `useDisclosure`, matching
  `browse/actions.tsx`'s `CreateFolderAction`) listing the first ~5 keys and
  "…and N more", with a destructive confirm button.
- On confirm: `useBulkDelete(bucketName).mutate([...selected])`. On success:
  if `errors.length === 0`, `toast.success("Deleted N objects")`; else render a
  small result panel ("Deleted X of Y — Z failed" + the failed keys). Then clear
  the selection, `queryClient.invalidateQueries({ queryKey: ["browse", bucketName] })`.
- Errors via `handleError`.

**Verify**: `npx pnpm@9 run typecheck && npx pnpm@9 run lint && npx pnpm@9 run build` → typecheck & build exit 0; lint red only on pre-existing (confirm none of your new files appear: `npx pnpm@9 run lint 2>&1 | grep -E "bulk-actions|object-list"` → nothing, or only pre-existing `any` consistent with the file's existing hooks).

### Step 9: Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
All exit 0.

## Test plan

- **Go**: `TestDeleteErrorsToList` (step 4) covers the error-aggregation fix — the
  one piece of pure logic. The handlers themselves are SDK plumbing (no brittle
  mock), matching how existing browse handlers are treated.
- **Frontend**: if you extract a pure selection predicate (e.g. "are all loaded
  keys selected"), add a Vitest case beside `src/lib/utils.test.ts`. A full
  component test is not required; typecheck + build gate the wiring.
- **Live verification is the reviewer's job**: they have a Garage instance with
  1000+ seeded objects — select a subset, delete, confirm the count/errors panel,
  and that a forced per-key failure surfaces ALL failures, not one.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `npx pnpm@9 run typecheck` exits 0
- [ ] `npx pnpm@9 run test` exits 0 (unchanged count, plus any new pure test)
- [ ] `npx pnpm@9 run build` exits 0
- [ ] `grep -n "BulkDeleteObjects" backend/router/browse.go` and `grep -n "POST /browse/{bucket}" backend/router/router.go` both match
- [ ] `grep -n "res.Errors\[0\]" backend/router/browse.go` returns **no matches** (the Q4 fix removed it)
- [ ] `git diff --name-only 15d0882..HEAD` shows only the 8 in-scope files (plus `plans/README.md`)
- [ ] `plans/README.md` row for 014 updated

## STOP conditions

- Base reset shows `ee420fb` (worktree on stale upstream — the reset didn't take).
- Current-state excerpts don't match live code (drift — 013 or something else moved `browse.go` unexpectedly).
- `POST /browse/{bucket}` conflicts with `GET /browse/{bucket}` at `ServeMux` registration (it should not — method-distinct; report the panic if it does).
- You find yourself building select-all-across-pages, folder multi-select, or move/copy. All out of scope — stop and report.
- `npx pnpm@9 run typecheck`/`test` scripts absent (wrong base).

## Maintenance notes

- **The `res.Errors[0]` fix applies to BOTH paths** — the new bulk endpoint and
  the pre-existing recursive folder delete. A reviewer should confirm the
  recursive delete now returns `{deleted, errors}` and that any frontend consumer
  of the recursive delete still works (it ignored the body before — check
  `object-actions.tsx`'s delete `onSuccess`).
- **Selection clears on prefix navigation by design** (spike decision). If users
  later want cross-prefix accumulation, that's a deliberate change, not a bug.
- **The 1000-key cap** matches one `DeleteObjects` batch; the chunk loop makes
  raising it safe server-side, but the UI cap in the toolbar must move in lockstep.
- **Folders are intentionally not multi-selectable** — deleting a folder is an
  unbounded subtree and stays the explicit recursive-delete action, so a checkbox
  never hides an unbounded delete.
