# Plan 040: Move the upload queue into a persistent floating card

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- src/pages/buckets/manage/browse/ src/lib/website.ts \
>   src/components/layouts/main-layout.tsx src/lib/z-layers.ts
> ```
> Then confirm every excerpt in §1 matches the live code. On a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED (touches the root layout and the upload state machine)
- **Depends on**: **the 037/038 stack must be merged first — hard.** See §0.
- **Category**: direction (UX)
- **Planned at**: `main` = `0edc65a` (v3.2.0), advisor stack tip `03ee680`, 2026-08-10

---

## 0. Base — read before anything else

The brief states "the previous work around public URLs/public access has already
been implemented." **On `main` it has not.** Verified:

```
advisor/037-website-hosting-reliability   NOT merged
advisor/038-public-asset-urls             NOT merged
advisor/038-phase7-drop-path-style        NOT merged

git show main:src/lib/website.ts | grep -c getPublicAccess   →   0
```

This matters because the brief also says the card must **reuse the existing
public-link implementation rather than create a second URL-generation path**.
Those two requirements conflict on `main`:

- `main` has only `getBucketWebsiteObjectUrl`, which **does not percent-encode
  the object key** — a key with a space, `#`, `%` or non-ASCII produces a broken
  URL — and still contains a **path-style** mode that returns 404 against a real
  Garage (measured).
- The stack tip has `getPublicAccess` (the three-state
  `public` / `public-no-url` / `private` helper, mutation-tested) and the
  corrected, encoded, vhost-only URL builder.

**Therefore: base this plan on `advisor/038-phase7-drop-path-style`, or on
`main` after that stack has merged.** Building the Copy-URL action on `main`'s
helper would ship a second broken path — exactly what the brief forbids.

**Phase 0 gate.** Before any edit:

```
grep -c "getPublicAccess" src/lib/website.ts
```
→ must be **≥ 1**. If it is `0`, **STOP and report** — you are on the wrong base
and the public-URL half of this plan cannot be done correctly.

*(The brief also says "do not depend on Plan 037." Honoured: nothing here needs
that plan's content. The dependency above is on merged **code**, verified by the
grep gate, not on a planning document.)*

---

## 1. Current upload architecture (the actual code)

| Concern | Location |
|---|---|
| Transport | `src/pages/buckets/manage/browse/upload-queue.ts` — `uploadFile()` using **`XMLHttpRequest`** (`fetch` cannot report upload progress), `PUT /api/browse/{bucket}/{key}` with `FormData`. |
| Queue state | Same file — a **module-level** `createStore` (zustand) plus two module-level `Map`s: `handles` (in-flight `UploadHandle`s) and `pendingFiles` (the `File` objects). |
| Concurrency | `MAX_CONCURRENT_UPLOADS = 3`, driven by a private `pump()`. |
| UI | `src/pages/buckets/manage/browse/upload-panel.tsx`, mounted at **`browse-tab.tsx:72`**. |
| Trigger | `actions.tsx` — a detached `<input type="file" multiple>`; 20-file cap; calls `uploadQueue.enqueue(bucketName, prefix, files)`. |
| Refresh | `upload-panel.tsx:22-26` — `useEffect` on `completedCount` → `queryClient.invalidateQueries({ queryKey: ["browse", bucketName] })`. |

### Four findings that reshape the brief

**(a) A global upload manager already exists. Do not build one.**
The store and both `Map`s are **module-level**, so they outlive any component.
Unmounting the panel does **not** abort anything — the `XMLHttpRequest` keeps
running and the queue keeps pumping. **Uploads already survive navigation
today; only the card disappears**, because it is mounted inside `browse-tab.tsx`.
This plan is therefore a **mount-point move plus a presentation rewrite**, not a
state-management project. The brief's warning about not adding Redux/zustand
"merely because a global manager is needed" is moot — zustand is already a
dependency and already used this way (`src/stores/app-store.ts`,
`src/lib/disclosure.ts` follow the same `createStore` pattern).

**(b) Real byte progress already exists.** `upload-queue.ts:50` —
`xhr.upload.onprogress` sets `loaded`/`size` per item. The brief's §7 ("determine
whether real byte progress currently exists; if not, add it") is already
satisfied. **No transport work is needed, and no faked progress is required.**

**(c) Cancellation is genuinely supported.** `UploadHandle = { abort }` wraps
`xhr.abort()`; `uploadQueue.cancel(id)` calls it and marks the item `canceled`,
guarded so a late `onload` cannot resurrect it. **A `Cancel` button is real, not
decorative** — show it.

**(d) Retry is NOT currently possible, and this is the one transport change
needed.** On the failure path:

```ts
      onError: (message) => {
        cleanup(item.id);            // ← deletes from `handles` AND `pendingFiles`
        patchItem(item.id, …status: "error"…);
        pump();
      },
```

`cleanup` drops the `File` from `pendingFiles`, so after a failure there is
nothing left to resend. Step 3 fixes this narrowly: keep the `File` on the error
path, drop it only on success, cancel, or dismiss.

---

## 2. Current reporting architecture

Everything that reports upload status today:

| Mechanism | Location | Purpose | Action |
|---|---|---|---|
| Queue panel (rows, %, Done, error, Cancel, Clear) | `upload-panel.tsx` | The upload surface | **MERGE** into the floating card |
| `toast.error("You can only upload up to 20 files at a time")` | `actions.tsx:38` | Pre-flight guard, fires before any upload starts | **KEEP** — not upload status; a toast is right for a rejected action |
| `toast.success("Folder created!")` | `actions.tsx:90` | Folder creation | **KEEP** — not an upload |
| `onError: handleError` on `createFolder` | `actions.tsx:95` | Folder creation failure | **KEEP** — not an upload |
| Per-file success toast | — | — | **Already removed** by the plan that introduced the queue. There is no duplicate-reporting problem to clean up. |

**Conclusion: the cleanup the brief anticipates is already done.** The only
upload-status surface is the panel. Do not remove the two folder-creation toasts
or the 20-file guard — they are unrelated to upload reporting, and removing them
would silently drop the only feedback those actions have.

---

## 3. Where the card lives

`src/components/layouts/main-layout.tsx` renders `<Header/>` then
`<main className="flex-1 overflow-y-auto p-4 md:p-8"><Outlet/></main>`.
Route changes swap `<Outlet/>`'s contents; the layout itself persists.

**Mount the card as a sibling of `<main>`, inside the layout.** It then survives
every in-app navigation, which is the brief's critical requirement.

Do **not** mount it in `auth-layout.tsx` (login/setup) — no uploads can exist
there.

**z-index.** `src/lib/z-layers.ts` exists precisely so no call site invents one,
and it has no entry for a persistent floating surface. **Add one:**

```ts
export const Z_LAYERS = {
  dropdown: 1000,
  popover: 1100,
  tooltip: 1200,
  toast: 1300,
  /** Persistent floating upload card. Below toasts so a transient message is
   *  never hidden behind it; above dropdowns so it is not clipped by them. */
  uploadCard: 1250,
} as const;
```

Note the file's own caveat: a native `<dialog>` (daisyUI `Modal`) paints in the
browser's top layer, above every value here. The card will sit *behind* an open
modal. That is correct and expected — do not try to defeat it.

---

## 4. Scope

**In scope:**
- `src/lib/z-layers.ts` — one new entry
- `src/pages/buckets/manage/browse/upload-queue.ts` — retain File on error; add `retry`, `dismiss` wiring, per-bucket completion signal
- `src/pages/buckets/manage/browse/upload-queue.test.ts` — extend
- `src/components/containers/upload-card.tsx` (**create**) — the floating card
- `src/components/containers/upload-card.test.tsx` (**create**)
- `src/components/layouts/main-layout.tsx` — mount the card
- `src/pages/buckets/manage/browse/browse-tab.tsx` — remove the old mount
- `src/pages/buckets/manage/browse/upload-panel.tsx` — **delete**

**Out of scope — do NOT touch:**
- `src/lib/website.ts` / `getPublicAccess` — consume it; never modify it. It is
  mutation-tested.
- `actions.tsx`'s folder-creation toasts and the 20-file guard (see §2).
- `backend/` — nothing server-side changes. Progress, cancel and the transport
  all already work.
- Drag-and-drop, folder upload, chunked/resumable/S3-multipart upload, IndexedDB
  or localStorage queues, service workers. The brief excludes all of these.
- `ShareObject` / presigning.
- Bucket permission semantics.

---

## 5. Steps

### Step 1: Add the `uploadCard` z-layer

Per §3. **Verify**: `pnpm run typecheck` → exit 0.

### Step 2: Give the store what the card needs

In `upload-queue.ts`:

**2a — per-bucket completion.** Replace the single `completedCount: number` with
a signal that names *which* bucket completed, e.g.
`completed: { bucket: string; seq: number } | null`, bumping `seq` each success.

*Why:* the panel today invalidates `["browse", bucketName]` for the **mounted**
bucket, but the queue can hold items from several buckets. Once the card is
global that mismatch is guaranteed rather than incidental — uploads to bucket A
would refresh whichever bucket you happen to be viewing. This is a real bug on
`main`, not a new concern.

**2b — expose `dismiss(id)`** (it exists but nothing calls it) and add
`clearFinished()` if not already exported.

**2c — collapse state.** Add `collapsed: boolean` to the store with a
`toggleCollapsed()` action. It belongs in the store, not component state, so
collapsing survives the card re-rendering. **Collapsing must not touch
`handles`, `pendingFiles`, or any item status.**

**Verify**: `pnpm run typecheck` → exit 0.

### Step 3: Make retry possible

The narrow fix from §1(d). In the `onError` callback, do **not** call the full
`cleanup`. Split it:

```ts
// Drop the transport handle but KEEP the File: a failed item can be retried,
// and the File is the only thing we cannot reconstruct.
const releaseHandle = (id: string) => { handles.delete(id); };
const releaseFile   = (id: string) => { pendingFiles.delete(id); };
```

- success → `releaseHandle` + `releaseFile`
- cancel → `releaseHandle` + `releaseFile`
- **error → `releaseHandle` only**
- `dismiss(id)` / `clearFinished()` → `releaseFile` for the removed items

Then add:

```ts
/** Puts a failed item back in the queue, reusing its original File, bucket and
 *  key. Does NOT create a new row — the brief requires one row per file. */
const retry = (id: string) => { … set status "queued", clear error, pump() … };
```

`retry` must no-op if the item is not in `error`, or if its `File` is missing
(defensive — should not happen once the above holds).

**Verify**: `pnpm exec vitest run upload-queue` → existing tests still pass.

### Step 4: Build the card

Create `src/components/containers/upload-card.tsx`.

**Placement**: `fixed bottom-4 right-4 w-[min(92vw,26rem)]` on desktop;
`inset-x-4 bottom-4 w-auto` below `sm`. `z-index` from
`Z_LAYERS.uploadCard`. Renders `null` when `items.length === 0`.

**Header** (always visible, is the collapse control):
- expanded: `Uploads` + `{done} / {total}`
- collapsed: `Uploading {active} of {total}` + aggregate `%`
- **Aggregate % only when every active item has a non-zero `size`.** Otherwise
  show counts only — the brief forbids misleading aggregates.
- a chevron toggling collapse, and an `X` that calls `clearFinished()`

**Rows** (expanded only), one per item, in a
`max-h-[45vh] overflow-y-auto` list so 100 files cannot exceed the viewport:

| status | shows |
|---|---|
| `queued` | `Waiting` |
| `uploading` | `<progress>` + `{pct}%` + `[Cancel]` |
| `done` | ✓ `Uploaded` + `[Copy URL]` **only when public** (§6) |
| `error` | ✕ + the message (truncated, full text in `title`) + `[Retry]` |
| `canceled` | `Canceled` |

Show the destination (`item.bucket` + prefix) **only when the queue holds more
than one distinct bucket** — the brief warns against cluttering every row.

**Completed items are never auto-removed.** No timer-based dismissal at all;
the user clears them. This trivially satisfies "failures must prevent auto
dismissal" and keeps the component free of timers.

**Accessibility:**
- collapse button: `aria-expanded`, label `Collapse upload list` / `Expand upload list`
- `<progress>` carries `aria-label={`Upload progress for ${item.name}`}`
- an `aria-live="polite"` region announcing **transitions only** — e.g.
  `"3 of 5 uploads complete"` and `"homepage.svg failed"` — never percentages
- `[Cancel]` / `[Retry]` get `aria-label`s naming the file
- status is conveyed by icon **and** text, never colour alone

**Motion:** a short entrance/expand transition, wrapped in
`motion-safe:` Tailwind variants so `prefers-reduced-motion` is respected.

**Verify**: `pnpm run typecheck && pnpm run build` → exit 0.

### Step 5: Wire public URLs — reuse, never re-derive

The card must call **`getPublicAccess`** from `@/lib/website` (the helper the
Phase 0 gate checked for). Do not build a URL any other way; do not call
`getBucketWebsiteObjectUrl` directly.

`getPublicAccess(websiteAccess, bucketName, objectKey, config)` needs the
bucket's `websiteAccess` flag. The card is global, so it cannot use
`useBucketContext()` — that only exists inside a bucket route.

**Resolve it from the buckets list, which is already cached**: `useBuckets()`
(`src/pages/buckets/hooks.ts`) drives the bucket list page, so the data is
usually in the TanStack cache already. Look up `item.bucket` there. If the
bucket is not found in the cache, treat access as **unknown and show no Copy
button** — never guess public.

`[Copy URL]` therefore appears only when `getPublicAccess(...)` returns
`{ state: "public" }`. A private bucket shows no button, satisfying the brief.
Copy via `copyToClipboard` from `@/lib/utils`.

**Do not change any permission semantics.** The card only reads.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 6: Move the mount, delete the panel

- `main-layout.tsx`: import and render `<UploadCard />` as a sibling of `<main>`.
- `browse-tab.tsx`: remove the `<UploadPanel …/>` element and its import.
- Delete `src/pages/buckets/manage/browse/upload-panel.tsx`.
- Move the `invalidateQueries` effect into the card, keyed on the **new
  per-bucket signal** from Step 2a: invalidate `["browse", completed.bucket]`.

**Verify**:
```
pnpm run typecheck && pnpm run test && pnpm run build
grep -rn "upload-panel" src/     # → no matches
```

### Step 7: Tests

Extend `upload-queue.test.ts` (the existing `FakeXhr` harness is already there):

1. **A failed item keeps its `File`** — fail an upload, assert `retry(id)` moves
   it back to `queued` and starts a new transport. *This is the Step 3
   regression guard.*
2. **`retry` does not create a second row** — item count is unchanged.
3. **`retry` no-ops on a non-error item.**
4. **`clearFinished` removes only terminal rows**, leaving active ones.
5. **The completion signal names the right bucket** — enqueue to two buckets,
   complete one, assert the signal carries that bucket.
6. **`toggleCollapsed` changes only `collapsed`** — assert item statuses,
   `handles` and `pendingFiles` are untouched. *(The brief's "collapsing must not
   affect transfers".)*

Create `upload-card.test.tsx`, modelled on
`src/components/containers/account-button.test.tsx` (`vi.hoisted` + `vi.mock`):

7. **Renders nothing when the queue is empty.**
8. **One card, many rows** — 3 items → one container, three rows.
9. **`[Copy URL]` appears for a public bucket and not for a private one** — mock
   `getPublicAccess` to return each state.
10. **`[Retry]` calls `uploadQueue.retry` with the item's id.**

**Verify**: `pnpm run test` → all pass, 10 new cases.

### Step 8: Prove the tests can fail

Temporarily restore the full `cleanup(item.id)` on the error path. Run
`pnpm exec vitest run upload-queue` → **case 1 must fail**. Revert; confirm all
pass. Report both numbers.

### Step 9: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
```
All exit 0.

### Step 10: Manual checks — reviewer's job

You have no Garage instance. Do **not** claim these passed; list them in NOTES:

1. Upload 5 files → one card, bottom-right, 3 uploading / 2 waiting, real percentages.
2. **Navigate to Cluster mid-upload → the card stays, uploads continue, progress keeps moving.** *(The headline requirement.)*
3. Collapse mid-upload → transfers continue; expand → progress intact.
4. Start a second batch while the first runs → same card, same queue.
5. Fail one (stop Garage) → row stays with the error; `[Retry]` resends the same row.
6. Cancel an in-flight upload → stops, marks `Canceled`, does not later flip to Done.
7. `Clear completed` → rows vanish; objects still present in the bucket listing.
8. Public bucket → `[Copy URL]` gives a working URL; private bucket → no button.
9. Object listing refreshes for the **uploaded** bucket, not merely the viewed one.
10. Mobile width → card spans with margins, still expandable.

---

## Done criteria

- [ ] `pnpm run typecheck`, `pnpm run test`, `pnpm run build` all exit 0
- [ ] 10 new tests, all passing; Step 8's mutation failed case 1 and was reverted
- [ ] `grep -rn "upload-panel" src/` → no matches
- [ ] `grep -rn "getBucketWebsiteObjectUrl" src/components/` → no matches (the card uses `getPublicAccess` only)
- [ ] `grep -n "uploadCard" src/lib/z-layers.ts` → present; `grep -rn "z-\[.*\]\|z-50" src/components/containers/upload-card.tsx` → no ad-hoc z-index
- [ ] `git diff <BASE>..HEAD -- backend/ src/lib/website.ts` is **empty**
- [ ] `grep -n "setTimeout" src/components/containers/upload-card.tsx` → no auto-dismiss timer
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- **Phase 0's grep returns `0`** — wrong base; the public-URL half cannot be done correctly.
- Any §1 excerpt does not match the live code.
- You find yourself adding a state-management library. zustand is already a dependency and the store already exists.
- You find yourself faking progress with a timer, or computing an aggregate % when some item has `size === 0`.
- You are about to modify `getPublicAccess` or anything in `backend/`.
- Removing the panel breaks the object-list refresh — the effect must move to the card, not disappear.
- You are about to add IndexedDB/localStorage/service-worker persistence. Out of scope.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The queue was already global; only its UI was local.** If a future change
  moves the card back inside a route, uploads will keep running while their
  reporting vanishes — the bug this plan fixes.
- **`pendingFiles` now outlives a failure by design.** It is released on success,
  cancel, or dismissal. A retained `File` pins its bytes in memory, so anything
  that stops calling `releaseFile` becomes a leak — that is the thing to check in
  review.
- **The card reads bucket visibility from the buckets-list cache.** If that query
  is ever removed or re-keyed, the Copy-URL button silently stops appearing.
  It fails closed (no button) rather than open, which is the right direction.
- **`uploadCard` sits below `toast` deliberately** so a transient message is
  never hidden behind a persistent card, and native `<dialog>`s still paint
  above both.
