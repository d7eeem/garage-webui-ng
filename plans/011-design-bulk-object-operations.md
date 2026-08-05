# Plan 011 (design/spike): Bulk object operations in the browser

> **Executor instructions**: This is a **design and spike** plan, not a
> build-everything plan. Your deliverable is a written design document with a
> recommendation, backed by a throwaway prototype of the selection UI — not
> production code, not a merged feature. Follow the steps, answer the open
> questions with evidence, and stop at the boundary marked "do not build past
> here." If anything in the "STOP conditions" section occurs, stop and report.
> When done, update the status row for this plan in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- src/pages/buckets/manage/browse/ backend/router/browse.go`
> If any of these changed since this plan was written, compare the "Current
> state" excerpts against the live code before proceeding.

## Status

- **Priority**: P3
- **Effort**: M (spike: ~1 day; the follow-up build is separately estimated by your output)
- **Risk**: LOW (nothing ships from this plan)
- **Depends on**: `plans/003-paginate-listing-and-delete.md` — **hard dependency**, see "Why this matters."
- **Category**: direction
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

Every object operation in the browser is one row at a time. To delete fifty
files, an operator confirms fifty dialogs. There is no multi-select, no "select
all," no bulk delete or move.

The pieces are mostly there. The backend already does batch deletion — the
recursive folder delete builds a `[]types.ObjectIdentifier` and calls
`DeleteObjects`. The frontend already loops over multiple files for upload. What
is missing is selection state in the table and an endpoint that takes a list of
keys instead of deriving one from a prefix.

**The hard dependency on plan 003 is not bureaucratic.** Plan 003 replaces the
single-page listing with paginated "load more," and it fixes a recursive delete
that silently stopped after 1000 objects. Building "select all" on top of a
paginated list, before that pagination exists and before the truncation bug is
fixed, would reproduce the exact failure this project just got done fixing: a
bulk action that reports success while operating on a fraction of what the user
selected. Do not start this spike until 003 has landed.

That failure mode is also why this is a spike rather than a build plan. The
mechanics are easy; the semantics of "select all" across a lazily-loaded list
are where the danger is.

## Current state

**Note**: these excerpts are from commit `ee420fb`, *before* plan 003. Since 003
is a hard dependency, the code you find will differ — 003 converts the listing to
an infinite query with flattened pages. Read 003's plan file to understand what
changed, then read the actual current code. The excerpts below establish the
starting shape and the pieces you will build on.

### Files

- `src/pages/buckets/manage/browse/object-list.tsx` — the object table.
- `src/pages/buckets/manage/browse/object-actions.tsx` — per-row actions.
- `src/pages/buckets/manage/browse/actions.tsx` — toolbar actions (upload, new folder).
- `src/pages/buckets/manage/browse/hooks.ts` — the query and mutation hooks.
- `backend/router/browse.go` — the delete handler with existing batch machinery.
- `backend/router/router.go` — route registration.

### Excerpt 1 — existing batch delete machinery on the server

`backend/router/browse.go:246-267` (pre-003):

```go
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
```

`DeleteObjects` already takes a list. A bulk endpoint reuses this almost
verbatim — the only change is where the list comes from.

Note `res.Errors`: S3 batch delete reports **per-object** failures, and this code
surfaces only the first one and discards the rest. Partial-failure reporting is
open question 4.

### Excerpt 2 — the per-row delete flow

`src/pages/buckets/manage/browse/object-actions.tsx:19-49`:

```tsx
const ObjectActions = ({ prefix = "", object, end }: Props) => {
  const { bucketName } = useBucketContext();
  const queryClient = useQueryClient();
  const isDirectory = object.objectKey.endsWith("/");

  const deleteObject = useDeleteObject(bucketName, {
    onSuccess: () => {
      toast.success("Object deleted!");
      queryClient.invalidateQueries({ queryKey: ["browse", bucketName] });
    },
    onError: handleError,
  });
  ...
  const onDelete = () => {
    if (
      window.confirm(
        `Are you sure you want to delete this ${
          isDirectory ? "directory and its content" : "object"
        }?`
      )
    ) {
      deleteObject.mutate({
        key: prefix + object.objectKey,
        recursive: isDirectory,
      });
    }
  };
```

Confirmation is `window.confirm`. A bulk delete needs a real dialog — open
question 3.

### Excerpt 3 — the table rows a checkbox column would join

`src/pages/buckets/manage/browse/object-list.tsx:36-41` and `:99-104` (pre-003):

```tsx
      <Table>
        <Table.Head>
          <span>Name</span>
          <span>Size</span>
          <span>Last Modified</span>
        </Table.Head>
```

```tsx
              <tr
                key={object.objectKey}
                className="hover:bg-neutral/60 hover:text-neutral-content group"
              >
```

Three head cells; each body row adds a fourth via `<ObjectActions />`. A
checkbox column changes both. Note there is an existing checkbox primitive at
`src/components/ui/checkbox.tsx` — use it rather than a raw `<input>`.

### Excerpt 4 — the existing multi-item pattern to learn from

`src/pages/buckets/manage/browse/actions.tsx:43-52`:

```tsx
      if (files.length > 20) {
        toast.error("You can only upload up to 20 files at a time");
        return;
      }

      for (const file of files) {
        const key = prefix + file.name;
        putObject.mutate({ key, file });
      }
```

Two things worth noting for the design: there is already a **hard cap of 20**
for a multi-item operation in this codebase (precedent for capping bulk
selection), and the loop fires all mutations concurrently against one shared
mutation object, so progress and error state for individual files are lost. Do
not copy that pattern for bulk delete — open question 4.

### Excerpt 5 — the parallel-mutation pattern that does exist

`src/pages/buckets/manage/hooks.ts:57-81`:

```ts
export const useAllowKey = (
  bucketId?: string | null,
  options?: MutationOptions<...>
) => {
  return useMutation({
    mutationFn: async (payload) => {
      const promises = payload.map(async (key) => {
        return api.post("/v2/AllowBucketKey", {
          body: { bucketId, accessKeyId: key.keyId, permissions: key.permissions },
        });
      });
      const result = await Promise.all(promises);
      return result;
    },
    ...options,
  });
};
```

This one takes an array and awaits all of them in a single mutation — a better
model for bulk operations than the fire-and-forget loop in Excerpt 4. Note it
uses `Promise.all`, which rejects on the first failure and discards the rest;
`Promise.allSettled` is what partial-failure reporting needs.

### Repo conventions to match

- **Go handlers**: methods on empty structs, registered in
  `backend/router/router.go`, ending in `utils.ResponseSuccess` /
  `utils.ResponseError`.
- **Frontend**: TanStack Query v5, hooks in a sibling `hooks.ts`, daisyUI via
  `react-daisyui` for `Table` / `Modal` / `Alert`, the local
  `@/components/ui/button` and `@/components/ui/checkbox` wrappers, icons from
  `lucide-react`, toasts via `sonner`'s `toast`.
- **Dialogs**: two patterns coexist — `useDisclosure` (`src/hooks/useDisclosure.ts`,
  used in `actions.tsx`) and the `createDisclosure` store
  (`src/lib/disclosure.ts`, used by `share-dialog.tsx`). Pick the one that fits
  and say why in the design doc.
- **No new dependencies.**

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Frontend dev | `pnpm run dev:client` | Vite dev server starts |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Frontend tests | `pnpm run test` | all pass |
| Go build | `cd backend && go build ./...` | exit 0 |
| Go run (spike) | `cd backend && go run main.go` | server starts on :3909 |

A running Garage instance with a bucket containing **more than 1000 objects** is
required — the whole point is the interaction between selection and pagination,
which does not manifest below the page size. If you cannot produce that, see
STOP conditions.

## Scope

**In scope** — a throwaway prototype plus a written design:

- `plans/design/011-bulk-object-operations.md` (create — the deliverable)
- Prototype code on a scratch branch, **not merged**: checkbox selection in the
  object table, a selection-count toolbar, and a bulk-delete endpoint.

**Do not build past here:**

- Do not implement move/rename. S3 has no move — it is copy-then-delete, which
  is a materially harder operation (multipart for large objects, partial-failure
  states that leave duplicates). Design it on paper if you like; do not build it.
- Do not implement drag-and-drop, keyboard range-select, or a context menu.
- Do not build a background job system. If the design concludes bulk operations
  need one, that is a finding and a much larger project.

**Out of scope entirely:**

- Pagination itself — plan 003 owns it. Build on it; do not modify it.
- Recursive folder delete — 003 fixes it. Bulk delete of *selected keys* is a
  different operation; the design should say how the two relate.

## Git workflow

- Branch: `advisor/011-spike-bulk-object-operations`
- Commit the prototype freely on that branch; it is not going to be merged.
- The **only** file intended for merge is the design document.
- Do NOT open a PR for the prototype code.

## Steps

### Step 1: Confirm plan 003 has landed and read what it changed

```bash
grep -n "hasNextPage" src/pages/buckets/manage/browse/object-list.tsx
grep -n "IsTruncated" backend/router/browse.go
```

Both must return matches. If either does not, **stop** — plan 003 has not
landed and this spike's central question (selection across pages) is
unanswerable. See STOP conditions.

Then read the current `object-list.tsx` and `hooks.ts` in full. The excerpts in
this file are pre-003; the live code is what you build on.

**Verify**: you can state, in one sentence, how the flattened `objects` array is
produced and what triggers loading the next page.

### Step 2: Prototype selection state

On your scratch branch, add checkbox selection to the object table. Keep it
crude.

Design decisions to make while prototyping (record each):

- Where does selection state live — `object-list.tsx` local state, or lifted to
  `browse-tab.tsx`? Note that navigating into a prefix changes `curPrefix` in
  `browse-tab.tsx`, which should almost certainly clear the selection.
- Are selected keys stored as full keys or prefix-relative? The listing gives
  you `objectKey` with the prefix trimmed and `url` with the full key — pick one
  representation and be consistent, because the delete endpoint needs full keys.
- Do directories (common prefixes) participate? They render as rows too
  (`object-list.tsx` maps `prefixes` before `objects`), and selecting one means
  a recursive delete of unknown size.

Add a toolbar that appears when the selection is non-empty, showing the count
and a Delete button.

**Verify**: you can select rows, the count updates, and navigating into a folder
clears the selection.

### Step 3: Confront the "select all" problem

This is the reason the spike exists. With pagination, "select all" is ambiguous:

- **Select all loaded** — only the rows currently in the DOM. Honest, cheap,
  and possibly surprising ("I clicked select all and it only deleted 1000").
- **Select all matching the prefix** — the user's likely mental model, but the
  client does not know the full key set without paging through everything, and
  the set can change between selection and execution.

Prototype at least one and record what it feels like against a >1000-object
prefix. Then answer:

**Q1 — Which semantics, and how is it labelled?**
If "select all loaded," the label must say so ("Select 1000 loaded"). If "all
matching," the operation has to be server-side by prefix — which is *already*
what recursive folder delete does (plan 003). Consider recommending that the UI
simply not offer "select all matching" and point at folder delete instead. That
would be a good answer.

**Q2 — What is the maximum selectable count, and what happens past it?**
There is precedent: `actions.tsx:43` caps uploads at 20. A bulk delete of 5000
selected keys means 5 `DeleteObjects` batches and a request that may take a
while. Recommend a cap and say what the UI does at the boundary.

### Step 4: Prototype the bulk-delete endpoint

Add a handler taking a list of keys. Sketch:

```go
// POST /browse/{bucket}/bulk-delete
func (b *Browse) BulkDeleteObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")

	var body struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.ResponseError(w, err)
		return
	}
	// ... build []types.ObjectIdentifier, chunk at 1000, call DeleteObjects
}
```

**Route registration is a real problem — do not skip it.** The existing patterns
are `DELETE /browse/{bucket}/{key...}` and `GET /browse/{bucket}`. A path like
`/browse/{bucket}/bulk-delete` collides with the `{key...}` wildcard for an
object literally named `bulk-delete`. Determine how Go's ServeMux resolves that
precedence, and recommend a non-colliding shape — `POST /browse/{bucket}` with
an action field, or a separate `/bulk/delete/{bucket}` prefix, or something
else. Record what you tested.

If plan 003 landed, reuse its `chunkObjectIdentifiers` helper rather than
writing a second chunker.

**Verify**: you can delete 1500 selected objects in one request and all 1500 are
gone.

### Step 5: Answer the remaining open questions

**Q3 — What does confirmation look like?**
`window.confirm` is used for every destructive action today
(`object-actions.tsx:38`, `nodes-list.tsx:104`, `keys/page.tsx:40`). For a bulk
delete of N objects, is a native confirm enough, or does it need a modal listing
what will be deleted? Note that the repo has a `Modal` pattern available. Also
note that recent commit `d1ad6a0 improve layout confirmation messages` shows the
maintainer cares about confirmation wording — match that sensibility.

**Q4 — How are partial failures reported?**
`DeleteObjects` returns a per-object `Errors` array; the current code surfaces
only `res.Errors[0]` and drops the rest (Excerpt 1). For a bulk operation this
matters: "deleted 1,487 of 1,500; 13 failed" is useful, "cannot delete object:
<one error>" is not. Design the response shape and the UI that renders it.
Reaching for `toast.error` with one message is probably not enough.

**Q5 — What happens to the listing after a bulk delete?**
The per-row flow invalidates `["browse", bucketName]`, which after plan 003
resets the infinite query to page one — so a user who loaded five pages loses
their position. Recommend behavior: full reset, optimistic removal of deleted
rows, or something else.

**Q6 — Is bulk delete alone worth shipping, or does it need move/copy to be
useful?**
Be honest. If operators' actual pain is reorganizing objects rather than
deleting them, bulk delete is the easy half of a feature that only pays off with
the hard half. Say so if you believe it.

### Step 6: Write the design document

Create `plans/design/011-bulk-object-operations.md` with these sections:

1. **Verdict** — *recommended, build it* / *recommended, narrower scope* /
   *not worth it*. Lead with it, unhedged.
2. **What was prototyped** — branch name, files touched, how to reproduce.
3. **Evidence** — answers to Q1-Q6 with what you observed, including the
   >1000-object behavior.
4. **Selection model** — where state lives, key representation, what clears it,
   whether directories participate, the "select all" semantics and its label.
5. **API design** — the endpoint shape, the route-collision resolution from
   step 4, request/response including partial failures.
6. **UI design** — checkbox column, selection toolbar, confirmation dialog,
   result reporting.
7. **Effort estimate** — S/M/L split frontend/backend, informed by the
   prototype.
8. **Move/copy** — a paragraph on why it was excluded and what it would take,
   so the next person does not re-derive it.
9. **Open questions you could not resolve.**

**Verify**: `test -f plans/design/011-bulk-object-operations.md` → exit 0, and
the file has all nine sections.

### Step 7: Clean up

```bash
git status --short
```

Expected: `plans/design/011-bulk-object-operations.md` (new) and
`plans/README.md` (modified). The prototype stays on its scratch branch,
unmerged.

## Test plan

No tests. This plan produces a document and a throwaway prototype.

The prototype's "test" is step 4's check: 1500 selected objects, one request,
all 1500 gone, verified by re-listing. If that works the mechanism is proven. If
it silently deletes 1000, you have reproduced the bug plan 003 fixed — which
means either 003 did not land or the bulk path has its own chunking defect.
Either way, report it.

## Done criteria

ALL must hold:

- [ ] `plans/design/011-bulk-object-operations.md` exists with all nine sections
- [ ] The verdict is stated first, unhedged
- [ ] Q1-Q6 each have an answer backed by an observation against a prefix with
      more than 1000 objects
- [ ] The route-collision question from step 4 has a tested answer, not a guess
- [ ] The prototype branch exists and its name is recorded in the design doc
- [ ] `git status --short` on the delivery branch shows only the design doc and
      `plans/README.md`
- [ ] `cd backend && go build ./...` and `pnpm run typecheck` exit 0 on the
      delivery branch (no prototype code leaked in)
- [ ] `plans/README.md` status row for 011 updated

## STOP conditions

Stop and report back (do not improvise) if:

- Step 1's checks fail — plan 003 has not landed. **Do not proceed and do not
  implement 003 yourself.** Report that the dependency is unmet.
- You cannot produce a bucket with more than 1000 objects. Every interesting
  question in this spike lives at that boundary; a design written without
  observing it would be guesswork. Report the blocker. (Generating 1001 tiny
  objects via a script against the S3 endpoint is the intended approach — if
  that is what failed, say why.)
- The bulk delete silently deletes fewer objects than selected. That is the
  exact failure class this project just fixed; report the counts and where the
  truncation happens rather than adding a retry loop.
- You conclude the honest verdict is "not worth it." Write it up and stop.
- You find yourself implementing move, copy, or a background job runner. Past
  the boundary.

## Maintenance notes

- **The "select all" semantics decision is the whole spike.** If the finished
  design doc is skimmed, that is the section to read. Getting it wrong
  reintroduces a silent-truncation bug in a destructive operation.
- **Recursive folder delete and bulk key delete overlap.** After plan 003 the
  former correctly handles unbounded prefixes server-side. A good outcome of
  this spike might be "bulk delete handles explicit selections only, capped;
  point users at folder delete for everything under a prefix" — which is less
  code and fewer failure modes than a general solution.
- **`DeleteObjects` partial failures are real and currently ignored.** Even if
  bulk operations are never built, Q4's finding applies to the existing
  recursive delete path — surfacing only `res.Errors[0]` hides how much did not
  get deleted. Flag it as a standalone finding if the verdict is negative.
