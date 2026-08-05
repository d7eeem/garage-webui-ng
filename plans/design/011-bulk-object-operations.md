# Design 011: Bulk object operations in the browser

> Spike output. Deliverable is this recommendation. Grounded in a 1200-object
> local Garage bucket (Docker) and a tested Go ServeMux route probe. Written
> 2026-07-30 against `integration-check` (includes plan 003's pagination, which
> this depends on).

## 1. Verdict

**Recommended, narrowed to bulk *delete of an explicit selection*, capped.**
The machinery mostly exists: plan 003 already made recursive prefix-delete
paginate correctly, its `chunkObjectIdentifiers` helper batches at the S3
1000-key cap, and a clean bulk endpoint route is available (tested, §5). What's
missing is selection state in the table and a keys-list endpoint.

**Do not build a client-side "select everything under this prefix" that walks
pages** — that reintroduces exactly the silent-truncation failure class plan 003
just fixed. For "delete this whole folder," point users at the existing recursive
folder delete (server-side, already correct). Bulk operations should act on an
explicit, bounded selection the user can see.

**Move/rename is a separate, larger feature** (S3 has no move; it's copy-then-
delete with partial-failure states) — excluded here, sketched in §8.

## 2. What was prototyped / measured

- Seeded **1200 objects** under `test-bucket/many/` on local Garage — confirms
  the >1000 regime where pagination and the delete-cap actually bite is real and
  reachable.
- **Route probe** (Go program, real `net/http.ServeMux` with the project's actual
  route patterns from `router.go`): tested where a bulk endpoint can live without
  colliding with the existing `{key...}` wildcard routes. Result in §5.
- Did **not** stand up the full webui against Garage to click through selection —
  the selection-semantics decision (§4) is a design choice, not something a
  prototype resolves; the empirical risks (pagination interaction, route
  collision, batch cap) are what needed measuring, and they were.

## 3. Evidence / answers

- **Q1 (select-all semantics) — the crux.** With 003's "Load more" pagination,
  the table only holds what's been fetched. "Select all" is ambiguous: *all
  loaded* (honest, cheap) vs *all matching the prefix* (the user's mental model,
  but the client doesn't know the full set without walking every page, and it can
  change mid-operation). **Recommendation: offer only "select all loaded," label
  it with the count ("Select 1000 loaded"), and do not offer select-all-matching.
  For whole-prefix deletion, surface the existing recursive folder delete.** This
  is the single most important decision in the spike and the one that keeps a
  destructive feature safe.
- **Q2 (max selectable).** There is precedent: `actions.tsx:43` already caps
  multi-file *upload* at 20. A bulk delete of a visible selection is safe to allow
  larger — recommend a cap of **1000** (one `DeleteObjects` batch; above that,
  the server chunks via 003's helper anyway). Past the cap, disable further
  selection with a note. Never silently drop.
- **Q3 (confirmation).** `window.confirm` is used for every destructive action
  today, and commit `d1ad6a0` shows the maintainer tunes confirmation wording. For
  N objects, a native confirm stating the count is the floor; a small modal
  listing the first few keys + "and N more" is better. Recommend a modal for bulk
  (the count makes a bare confirm too easy to misread).
- **Q4 (partial failures) — a real gap even today.** S3 `DeleteObjects` returns a
  per-object `Errors` array; the current recursive delete
  (`browse.go`, post-003) surfaces only `res.Errors[0]` and drops the rest. A bulk
  UI must report "deleted N of M; K failed" from the full `Errors` set. **This is
  worth fixing in the existing recursive delete regardless of whether bulk ships.**
- **Q5 (listing after delete).** The per-row delete invalidates
  `["browse", bucketName]`, which after 003 resets the infinite query to page one
  — a user who loaded five pages loses position. For bulk, recommend optimistic
  removal of the deleted rows from the current pages, then a background refetch,
  rather than a full reset.
- **Q6 (is delete-alone worth it?).** Yes — it's the common operator pain (clear
  out a prefix of junk) and it reuses existing server machinery. Move/rename is
  the harder half and can follow if demanded.

## 4. Selection model

- **State**: a `Set<string>` of full object keys, held in `browse-tab.tsx` (the
  level that owns `curPrefix`), so navigating into a prefix **clears** the
  selection (changing `curPrefix` resets it). Store full keys (not prefix-relative)
  — the delete endpoint needs them.
- **Directories (common prefixes)**: a folder row represents an unbounded subtree.
  **Exclude folders from multi-select**; deleting a folder stays the existing
  single recursive-delete action. Mixing "selected folder" into a bulk key list
  hides an unbounded operation behind a checkbox — the thing to avoid.
- **"Select all"**: selects only the currently-loaded object rows, labeled with
  the count. No cross-page walking.
- Use the existing `src/components/ui/checkbox.tsx` primitive, not a raw input.

## 5. API design (route collision resolved)

Tested against a real `http.ServeMux` with the project's patterns
(`GET /browse/{bucket}`, `GET|PUT|DELETE /browse/{bucket}/{key...}`):

| Candidate | Result |
|---|---|
| **`POST /browse/{bucket}`** (keys in JSON body) | **Registers clean; no collision.** `POST /browse/mybucket` → the new handler; GET/PUT/DELETE on objects (incl. one named `bulk-delete`) still route to `{key...}`. **Recommended.** |
| `POST /browse/{bucket}/bulk-delete` (literal) | Also clean today, but reserves the object name `bulk-delete`; if a future `POST .../{key...}` is added, an object named `bulk-delete` is shadowed by the endpoint. |

Recommended shape:

```
POST /api/browse/{bucket}        body: { "action": "delete", "keys": ["a","b/c",...] }
  -> { "deleted": N, "errors": [ {"key":"...","message":"..."} ] }   // HTTP 200; 207-style semantics in the body
```

- Reuse **003's `chunkObjectIdentifiers`** to batch at 1000; do not re-implement.
- Return the **full** per-key error set (fixes Q4), not just the first.
- `POST /browse/{bucket}` is method-distinct from `GET /browse/{bucket}` (the
  listing), so the two coexist cleanly.

## 6. UI design

- **Checkbox column** as the first table cell in `object-list.tsx`; a header
  checkbox = "select all loaded."
- **Selection toolbar** appears when the selection is non-empty: shows the count
  and a Delete button (and later Move, if built). Precedent for a conditional
  action bar exists in `nodes-list.tsx` (the staged-changes Apply/Revert bar).
- **Confirmation modal** (not `window.confirm`) listing the first few keys +
  "and N more," matching the maintainer's attention to confirmation wording.
- **Result reporting**: a toast for the happy path; if `errors` is non-empty, a
  small result panel ("deleted 1,487 of 1,500 — 13 failed") rather than a single
  `toast.error`.
- Dialog machinery: the repo has two patterns (`useDisclosure` and the
  `createDisclosure` store). Use `useDisclosure` here — it's local UI state, no
  cross-component sharing needed.

## 7. Effort estimate

- **Backend**: `POST /browse/{bucket}` bulk-delete handler reusing
  `chunkObjectIdentifiers`, with full error reporting + a Go test → **S–M**.
  Fixing the existing recursive delete to report all errors (Q4) is a small
  add-on, worth doing in the same change.
- **Frontend**: selection state + checkbox column + toolbar + confirm modal +
  result panel → **M** (the bulk of the work).
- **Docs**: negligible.
- Total: **M**, backend-light / frontend-heavy.

## 8. Move / copy (excluded — why, and what it would take)

S3 has no move. "Rename"/"move" = server-side copy (`CopyObject`) then delete,
per object. Complications that make it its own project: large objects need
multipart copy; a mid-batch failure leaves **duplicates** (copied but not
deleted), a worse partial-failure story than delete's "some remain"; and the UI
needs a destination picker. If operators' real pain is reorganizing rather than
deleting, move is the feature they want — but it should be scoped and estimated
separately, not bolted onto bulk delete.

## 9. Open questions

- **Confirm the bulk-delete of >1000 selected keys end to end** against the 1200-
  object bucket once the endpoint exists (the seeded data is ready). The chunking
  is 003's, already tested at the unit level; an integration pass would close it.
- **Should "select all loaded" also offer "…and delete the rest of the prefix"**
  as an explicit, server-side action (i.e. hand off to recursive folder delete)?
  That's the safe way to satisfy the "delete everything" mental model without
  client-side page-walking — worth a UX decision.
- **Cross-page selection persistence**: the recommendation clears selection on
  navigation. If users want to accumulate a selection across "Load more" clicks
  within the same prefix, that's supported (same prefix, no reset); across
  *prefix* changes it is not. Confirm that matches expectations.

## Reproduction

```bash
# 1200 objects already seeded under test-bucket/many/ on local Garage:
docker run --rm --network container:garage-local --entrypoint sh minio/mc -c \
  "mc alias set g http://127.0.0.1:3900 <KEY> <SECRET> && mc ls --recursive g/test-bucket/many/ | wc -l"  # 1200
# route probe: scratchpad/routetest/main.go  (go run .)
```
