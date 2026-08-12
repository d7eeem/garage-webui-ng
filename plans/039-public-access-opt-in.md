# Plan 039: Make enabling public access a deliberate act, and label private objects as private

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to
> base on:
> ```
> git diff --stat <BASE> -- src/lib/website.ts \
>   src/pages/buckets/manage/overview/overview-website-access.tsx \
>   src/pages/buckets/manage/browse/
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: **037 and 038 (including 038 Phase 7) — hard.** This plan
  builds directly on `getPublicAccess`, `SaveStatus`, and the corrected
  vhost-only URL logic. Base on `advisor/038-phase7-drop-path-style` (or on
  `main` once that stack has merged).
- **Category**: security (UX) + direction
- **Planned at**: advisor stack tip `03ee680`, 2026-08-10

---

## 0. Read this first — most of the request is already satisfied

This plan came from a brief asking to make public access opt-in, on the premise
that plans 037/038 made objects public by default. **They did not.** Before
writing code, understand what already holds, so you neither rebuild it nor
"fix" something that is already correct.

Sixteen acceptance criteria were requested. **Eleven already pass on the base
branch**, verified by reading the code:

| # | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | Buckets private by default | ✅ | `src/pages/buckets/hooks.ts` `useCreateBucket` posts `{ globalAlias }` **only** — it never sends `websiteAccess`. Garage defaults it to `false`. |
| 2 | UI shows real Garage state | ✅ | `useBucket` → `GET /v2/GetBucketInfo` → `bucket.websiteAccess`, read through `useBucketContext()`. There is **no** local `publicEnabled` boolean anywhere. |
| 3 | Can enable from the UI | ✅ | `ToggleField name="websiteAccess"` in `overview-website-access.tsx`. |
| 5 | Can disable from the UI | ✅ | Same toggle. |
| 6 | Disabling needs no object deletion | ✅ | It is a bucket-level setting; objects are untouched. |
| 7 | Disabling revokes immediately | ✅ | Garage-native. Measured: a bucket not served as a website returns **404** at the web endpoint. |
| 8 | Toggle drives real Garage permissions | ✅ | `POST /v2/UpdateBucket` with `{ websiteAccess: { enabled, … } }`. |
| 9 | Public base URL alone ≠ public | ✅ | `getPublicAccess` returns `{state:"private"}` on `websiteAccess !== true` **before** consulting any URL. Mutation-tested in 038. |
| 10 | Public URL shown only when genuinely enabled | ✅ | Same function; same test. |
| 11 | Presigned still works for private objects | ✅ | `ShareObject` untouched by 037/038. |
| 12 | Uploads never enable public access | ✅ | The upload path (`upload-queue.ts` → `PUT /browse/…`) never touches bucket config. |
| 13 | `assets` has no special behaviour | ✅ | `grep -rn '"assets"' src/` finds no name-based logic. Bucket names are data. |
| 16 | Failed updates don't leave misleading UI | ✅ | Plan 037 added `onError: handleError` as a default on `useUpdateBucket` plus a persistent `Not saved` marker (`overview/save-status.tsx`). |

**What is genuinely missing — this plan's entire job:**

| # | Criterion | Gap |
|---|---|---|
| 4 | Enabling requires deliberate confirmation | **The toggle fires immediately** through a 500 ms debounced auto-save. One stray click makes a bucket world-readable with no prompt. This is the real risk in the brief and Step 1 fixes it. |
| — | Private objects are labelled as private | The Share dialog shows the presigned block for a private object but never says the word **Private**. Step 2. |
| — | Upload panel states access | Plan 038 Phase 5 shows *Copy URL* for public buckets but says nothing for private ones. Step 3. |
| 14/15 | Homepage works ON, stops OFF | Needs a **live** revocation check. Step 5 — reviewer's job, not automatable here. |

### Three corrections to the brief

**(a) Options B and C are not available.** The brief asks to evaluate
prefix-level and per-object toggles. Garage implements **neither**.
`backend/schema/bucket.go` is the complete bucket shape the app receives:
`ID`, `GlobalAliases`, `LocalAliases`, `WebsiteAccess`, `WebsiteConfig`, `Keys`,
counters, `Quotas`. There is no ACL field and no bucket-policy field; Garage does
not implement S3 bucket policies, and `Keys` are *authenticated* per-access-key
grants, irrelevant to anonymous reads. **Option A (bucket-level) is forced, not
merely preferred.** The brief's own constraint — "avoid introducing per-object
ACL abstractions unless Garage genuinely supports them" — settles it.

**(b) Revocation returns 404, not 403.** Measured against a live Garage web
endpoint: a `Host` naming a bucket that is not served as a website returns
**404** with a plain `404 Not Found` body, identical to a missing object. That is
the non-existence-leaking behaviour the brief asked to prefer, and it is already
Garage's default. Nothing to build; document it.

**(c) The premise "showing a public URL whenever the bucket is publicly readable
is too risky" is inverted.** `websiteAccess: true` *is* the operator having
opted in — it is the only anonymous-read switch Garage has. Showing the URL then
is correct. The risk actually worth guarding against was a *configured base URL*
implying public, and 038 already closed that. What remains is that flipping the
switch is too **easy**, which is Step 1.

---

## Current state

### `src/pages/buckets/manage/overview/overview-website-access.tsx` — the auto-save wiring, verbatim

```tsx
  const onChange = useDebounce((values: DeepPartial<WebsiteConfigSchema>) => {
    const data = {
      enabled: values.websiteAccess,
      indexDocument: values.websiteAccess
        ? values.websiteConfig?.indexDocument
        : undefined,
      errorDocument: values.websiteAccess
        ? values.websiteConfig?.errorDocument
        : undefined,
    };

    updateMutation.mutate({
      websiteAccess: data,
    });
  });

  useEffect(() => {
    form.reset({ … });
    const { unsubscribe } = form.watch((values) => onChange(values));
    return unsubscribe;
  }, [data]);
```

and the control:

```tsx
      <ToggleField
        form={form}
        name="websiteAccess"
        label="Enabled"
        disabled={!canWrite}
      />
```

**This is the crux.** `form.watch` fires on *every* field change including
`websiteAccess`, and `onChange` writes to Garage 500 ms later. There is no
interception point today.

### The confirm-modal pattern to copy

`src/pages/buckets/manage/browse/bulk-actions.tsx` — `useDisclosure()` +
`react-daisyui` `Modal` with `Modal.Header` / `Modal.Body` / `Modal.Actions`,
a Cancel button and a coloured confirm button. Read it before writing Step 1.

### `getPublicAccess` — already correct, do not change

```ts
export function getPublicAccess(
  websiteAccess: boolean | undefined | null,
  bucketName: string,
  objectKey: string,
  config?: Config
): PublicAccess {
  if (websiteAccess !== true) return { state: "private" };
  const url = getBucketWebsiteObjectUrl(bucketName, objectKey, config);
  if (url == null) return { state: "public-no-url" };
  return { state: "public", url };
}
```

### Conventions

- Forms: react-hook-form + zod; `ToggleField`/`InputField` from `src/components/ui/`.
- Modals: `useDisclosure()` from `@/hooks/useDisclosure` + `Modal` from `react-daisyui`.
- Toasts: `handleError` from `@/lib/utils`; `sonner`.
- Icons: `lucide-react`. Class composition: `cn` from `@/lib/utils`.
- Tests: Vitest; pure helpers get `*.test.ts` beside them. Component tests use
  `@testing-library/react` — see `src/components/containers/account-button.test.tsx`
  for the `vi.hoisted` + `vi.mock` pattern.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems, CI runs
  it `continue-on-error`). Make new code clean; do not clear the backlog.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| One file | `pnpm exec vitest run website-access` | all pass |
| Build | `pnpm run build` | exit 0 |

`pnpm` may not be on PATH; it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. Do not substitute `npm` (the lockfile is `pnpm-lock.yaml`).

## Scope

**In scope:**
- `src/pages/buckets/manage/overview/overview-website-access.tsx`
- `src/pages/buckets/manage/overview/public-access-confirm.tsx` (create)
- `src/pages/buckets/manage/overview/website-access.test.tsx` (create)
- `src/pages/buckets/manage/browse/share-dialog.tsx`
- `src/pages/buckets/manage/browse/upload-panel.tsx`
- `README.md` (one short subsection — Step 4)

**Out of scope — do NOT touch:**
- `src/lib/website.ts` and `getPublicAccess` — already correct and
  mutation-tested. Consuming it is fine; changing it is not.
- `src/pages/buckets/hooks.ts` / `useCreateBucket` — already private-by-default.
  **Do not add a `websiteAccess` field to bucket creation**; sending `false`
  explicitly is redundant and risks a Garage API mismatch.
- `src/pages/buckets/manage/hooks.ts`, `save-status.tsx` — 037's work.
- Any backend file. This plan is frontend-only; Garage already enforces
  everything.
- `overview-quota.tsx`, `ShareObject`, presigning.
- Any per-object or prefix-level visibility concept. See §0(a).
- `plans/README.md`.

## Git workflow

- Branch: `advisor/039-public-access-opt-in`, created from the base you were given.
- Conventional commits, e.g. `feat(ui): confirm before enabling public bucket access`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Require confirmation before enabling — the whole point of this plan

**The problem.** `form.watch` → `onChange` → `updateMutation.mutate` fires for
every field, `websiteAccess` included, 500 ms after a click. Flipping a bucket to
world-readable currently takes one stray click and no prompt.

**The design.** Turning it **ON** must be interceptable; turning it **OFF** must
stay immediate (the brief is explicit that disabling should be easy, and a
confirmation on the safe direction trains people to click through).

Take `websiteAccess` out of the debounced auto-save path and drive it explicitly:

1. In the `form.watch` handler, **skip** the mutation when only `websiteAccess`
   changed — react-hook-form's watch callback receives `(values, { name })`, so
   gate on `name === "websiteAccess"`. Index/error-document edits keep their
   existing debounced behaviour untouched.
2. Replace the bare `<ToggleField>` with a controlled toggle whose `onChange`:
   - **false → true**: do **not** change form state yet. Open the confirm modal.
   - **true → false**: set the value and call `updateMutation.mutate({ websiteAccess: { enabled: false } })` straight away.
3. On confirm in the modal: set the form value to `true`, call
   `updateMutation.mutate({ websiteAccess: { enabled: true, indexDocument, errorDocument } })`
   using the current form values, and close.
4. On cancel: close, change nothing. The toggle must still read OFF.

**Create `public-access-confirm.tsx`** — a small presentational modal, modelled
on `bulk-actions.tsx`'s confirm:

- Header: `Enable public access?`
- Body, naming the bucket: *"Every object in **{bucketName}** becomes readable by anyone who can reach the Garage website endpoint, with no sign-in. Uploads and deletions still require credentials. You can turn this off again at any time."*
- Actions: `Cancel` (ghost) and `Enable public access` (`color="warning"`).

Props: `{ bucketName: string; isOpen: boolean; onCancel: () => void; onConfirm: () => void }`.
No data fetching, no mutation inside it — that keeps it trivially testable.

**Do not** add a confirmation to the disable path, and **do not** convert the
document-name inputs away from auto-save.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 2: Say "Private" when it is private

In `share-dialog.tsx`, the dialog already branches on `getPublicAccess`. Add an
explicit access line above the link sections so the state is stated, not implied:

- `state === "public"` → `Access: Public` (with the existing public URL block).
- `state === "public-no-url"` → `Access: Public` plus the existing configuration
  hint.
- `state === "private"` → `Access: Private`, and where the public URL would be,
  the text *"No public URL — public access is off for this bucket."*

Keep the presigned block exactly as it is in every state. Use muted text
(`text-xs text-base-content/60`) for the label; a coloured badge is not needed
and the brief explicitly warns against alarm fatigue.

**Verify**: `pnpm run typecheck && pnpm run build` → exit 0.

### Step 3: State access on completed uploads

In `upload-panel.tsx`, plan 038 Phase 5 renders `[Copy URL]` on a `done` row when
`getPublicAccess` is `public`. Add the private case: on a `done` row whose bucket
is **not** public, render a muted `Private` label instead of the button.

**Carry forward 038's constraint**: the panel can hold items from several
buckets, so build from `item.bucket`, never the mounted `bucketName`. 038 gates
the button to the mounted bucket for exactly that reason — keep that gate; a row
from another bucket gets neither button nor label.

**Verify**: `pnpm run typecheck && pnpm run build` → exit 0.

### Step 4: Document the model

Add a short subsection to `README.md` near the public-asset recipe:

- Public read is **off** for every new bucket; nothing enables it implicitly —
  not the bucket's name, not a configured `S3_WEB_PUBLIC_URL`.
- It maps to exactly one Garage setting, `websiteAccess`, which is bucket-level.
  Garage has no per-object or per-prefix anonymous-read control.
- Turning it off takes effect immediately and Garage then answers anonymous
  requests with **404**, indistinguishable from a missing object.
- Presigned links are the mechanism for sharing from a private bucket and are
  unaffected.

Keep it to a short paragraph plus a bullet list. Do not restructure the README.

**Verify**: `grep -n "websiteAccess\|Public read" README.md` → matches in the new
subsection.

### Step 5: Tests

Create `src/pages/buckets/manage/overview/website-access.test.tsx`. Model the
mocking on `src/components/containers/account-button.test.tsx` (`vi.hoisted` +
`vi.mock`). Mock `useBucketContext`, `useAuth`, `useConfig` and `useUpdateBucket`
so no network or `QueryClientProvider` is needed.

Cases — the first three are the reason this plan exists:

1. **Enabling opens the confirm modal and does NOT call the mutation.** Click the
   toggle from OFF; assert the modal is shown and `mutate` was **not** called.
2. **Confirming calls the mutation with `enabled: true`.**
3. **Cancelling calls nothing and leaves the toggle OFF.**
4. **Disabling calls the mutation with `enabled: false` and shows no modal** —
   the safe direction stays one click.
5. **Editing the index document still auto-saves** — the debounced path is
   untouched (advance timers with `vi.useFakeTimers()`; the debounce is 500 ms).

Then, in the existing `src/lib/website.test.ts`, confirm — do not duplicate —
that a case already asserts `websiteAccess: false` + a configured `public_url`
yields `{ state: "private" }`. **038 added it. If it is missing, add it; if it is
present, leave it alone and say so in your report.**

**Verify**: `pnpm run test` → all pass, including the 5 new cases.

### Step 6: Prove the tests can fail

Temporarily change Step 1's toggle handler so `false → true` mutates directly
without opening the modal. Run `pnpm exec vitest run website-access` and confirm
**cases 1 and 3 fail**. Revert, confirm all pass. Report both numbers.

### Step 7: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
```
All exit 0.

### Step 8: Live check — reviewer's job, not yours

You have no Garage instance. Do **not** claim these passed; list them in NOTES:

1. New bucket → public read is OFF, Share dialog says `Access: Private`, no public URL.
2. Toggle ON → confirm modal appears; **Cancel** leaves it OFF and Garage unchanged.
3. Confirm → `Saved`, public URL appears, anonymous `GET` returns **200**.
4. Paste into Homepage/gethomepage → icon renders.
5. Toggle OFF (no modal, immediate) → anonymous `GET` returns **404**; Homepage stops rendering it.
6. Throughout, presigned links from a private bucket still work.

Step 5 is the revocation criterion (#14/#15) and is the single most important
manual check in this plan.

---

## Done criteria

- [ ] `pnpm run typecheck`, `pnpm run test`, `pnpm run build` all exit 0
- [ ] 5 new cases in `website-access.test.tsx`, all passing
- [ ] Step 6's mutation check failed cases 1 and 3, and was reverted
- [ ] `git diff <BASE>..HEAD -- src/lib/website.ts` is **empty** (`getPublicAccess` untouched)
- [ ] `git diff <BASE>..HEAD -- backend/ src/pages/buckets/hooks.ts src/pages/buckets/manage/hooks.ts` is **empty**
- [ ] `grep -rn "publicEnabled\|isPublic\s*=" src/` → no app-owned visibility boolean was introduced
- [ ] `grep -rn '"assets"' src/` → no bucket-name-based logic
- [ ] `git diff --stat <BASE>..HEAD` lists only the in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You find yourself adding a `publicRead`/`isPublic` field, per-object or
  per-prefix visibility, or any app-side permission store. Garage has none of
  these; re-read §0(a).
- You are about to add `websiteAccess` to `useCreateBucket`. Buckets are already
  private by default; sending it explicitly is redundant.
- You are about to change `getPublicAccess`. It is correct and mutation-tested.
- Adding the confirm breaks the index/error-document auto-save. Those two fields
  must keep saving on debounce — if you cannot separate them, report rather than
  converting the whole section to a Save button.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **`websiteAccess` is the only anonymous-read switch Garage has.** Anything
  showing a public URL must gate on it *first*. If Garage ever gains bucket
  policies, revisit §0(a) — until then, per-object visibility is not
  implementable and must not be faked.
- **The confirm guards one direction on purpose.** Confirming a disable would
  train users to click through the dialog, weakening the enable guard. If a
  future change adds a second confirmation here, that trade-off is what it costs.
- **`websiteAccess` is now the one field in this form that does not auto-save.**
  Anyone adding a field must decide which path it belongs on; the `name ===
  "websiteAccess"` guard in the watch handler is the seam.
- **Revocation is Garage's, not the app's.** Turning the toggle off changes one
  bucket setting; Garage stops serving immediately and answers 404. The app
  never proxies object bytes, so there is no cache of ours to invalidate.
