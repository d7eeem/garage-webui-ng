# Plan 042: Fix the object table's column mismatch — the actions column is unreachable

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- src/pages/buckets/manage/browse/object-list.tsx \
>   src/pages/buckets/manage/browse/object-actions.tsx
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug
- **Planned at**: commit `d9fab74` (v3.4.0), 2026-08-10

## Why this matters

A maintainer reported the object table being cut off on the right on a wide
window: the **Last Modified** column truncates mid-word and the per-row actions
column is not visible at all.

That column holds **Download, Share and Delete**. With it off-screen, three
per-object actions are unreachable from the browser tab. This is shipped in
`v3.4.0`.

Three distinct defects combine to produce it. All are in one file.

### Defect 1 — the header declares 4 columns; every row renders 5

`react-daisyui`'s `Table.Head` maps **each child to exactly one `<th>`**.
Confirmed by reading `node_modules/react-daisyui/dist/react-daisyui.esm.js`:

```js
return jsx("thead", { children: jsx("tr", {
  children: children?.map(function (child, i) { return jsx("th", { children: child }, i); })
})});
```

The head passes **4** children (checkbox, `Name`, `Size`, `Last Modified`), so
the table declares 4 columns. Every data row emits **5** cells — the four above
plus `<ObjectActions>`, which renders its own `<td>` (`object-actions.tsx:54`).

An HTML table sizes columns from the widest row, so the browser must invent a
5th column with no header to size it against. That is the primary cause.

### Defect 2 — the `colSpan` values contradict each other

In the same file: `colSpan={4}` for the loading, error and empty rows, but
`colSpan={5}` for the load-more row. One of those is wrong no matter which count
is correct, and the disagreement is direct evidence that the true column count
was never settled.

### Defect 3 — `max-w-[40vw]` is viewport-relative inside a narrower container

The filename span uses `truncate max-w-[40vw]`. `vw` is a fraction of the
**viewport**, but this table lives inside `<div className="container">`
(`src/pages/buckets/manage/page.tsx`), itself inside `<main className="flex-1 overflow-y-auto p-4 md:p-8">`
offset by a persistent sidebar. On a wide screen, 40 % of the viewport can
exceed the width the column actually has, so a long name pushes the table past
its container instead of ellipsizing.

The reported bucket contains
`Balance Sheet_Drivetek Solutions_Fiscal Year_2026-01-01_2026-12-31_2026_2026_Yearly___Report_….pdf`
— exactly that case.

### Why the wrapper's `overflow-x-auto` does not save it

`object-list.tsx` wraps the table in `<div className="overflow-x-auto min-h-[400px]">`,
so in principle the overflow should scroll. But `main-layout.tsx` sets
`contentClassName="flex flex-col overflow-hidden"` on the Drawer and `<main>` is
only `overflow-y-auto`, so horizontal overflow is clipped above the wrapper. Do
**not** attempt to fix this by changing the layout's overflow — that is a
global change with wide blast radius, and the correct fix is to stop the table
overflowing in the first place.

## Current state

### `src/pages/buckets/manage/browse/object-list.tsx` — the head, verbatim

```tsx
    <div className="overflow-x-auto min-h-[400px]">
      <Table>
        <Table.Head>
          <span>
            <Checkbox
              checked={allLoadedSelected}
              onChange={toggleSelectAll}
              aria-label="Select all loaded objects"
            />
          </span>
          <span>Name</span>
          <span>Size</span>
          <span>Last Modified</span>
        </Table.Head>
```

### The `colSpan` sites, verbatim

| Line | Code | Row |
|---|---|---|
| ~104 | `<td colSpan={4}>` | loading |
| ~112 | `<td colSpan={4}>` | error |
| ~120 | `<td className="text-center py-16" colSpan={4}>` | empty |
| ~145 | `<td colSpan={2} />` | prefix row filler — **correct, leave it** |
| ~203 | `<td colSpan={5} className="text-center">` | load more |

### The prefix (folder) row — 5 cells, verbatim

```tsx
              <td />
              <td className="cursor-pointer" role="button" onClick={() => onPrefixChange?.(prefix)}>
                …
              </td>
              <td colSpan={2} />
              <ObjectActions object={{ objectKey: prefix, url: "" }} />
```

`<td />` + `<td>` + `<td colSpan={2}>` + the actions `<td>` = **5 columns**.
This row is already correct; it is the head that is short.

### The object row's name cell, verbatim

```tsx
                <td
                  className="cursor-pointer"
                  role="button"
                  onClick={() => onObjectClick(object)}
                >
                  <span className="flex items-center font-normal w-full">
                    <FilePreview ext={ext?.substring(1)} object={object} />
                    <span className="truncate max-w-[40vw]">{filename}</span>
                    {ext && <span className="text-base-content/60">{ext}</span>}
                    {isPublic && (
                      <span className="badge badge-ghost badge-sm ml-2 shrink-0">
                        Public
                      </span>
                    )}
                  </span>
                </td>
```

### `src/pages/buckets/manage/browse/object-actions.tsx:54` — the 5th cell

```tsx
    <td className="!p-0 w-auto">
```

### Conventions

- daisyUI + Tailwind; `react-daisyui` primitives with thin wrappers in `src/components/ui/`.
- The `@/` alias maps to `src/`.
- Component tests use `@testing-library/react`. **`src/components/containers/account-button.test.tsx` is the `vi.hoisted` + `vi.mock` exemplar** — read it before writing tests.
- **jsdom has no layout engine.** `getBoundingClientRect` returns zeros and CSS is not applied, so you **cannot** test "is the column visible" or "does it overflow". `src/components/ui/menu.test.tsx` documents this and stubs around it. See the Test plan for what *is* testable.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems; CI runs it `continue-on-error`). Make new code clean; do not clear the backlog.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| One file | `pnpm exec vitest run object-list` | all pass |
| Build | `pnpm run build` | exit 0 |

`pnpm` may not be on PATH; it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. Do not substitute `npm` (lockfile is `pnpm-lock.yaml`). No Go changes.

## Scope

**In scope:**
- `src/pages/buckets/manage/browse/object-list.tsx`
- `src/pages/buckets/manage/browse/object-list.test.tsx` (**create**)

**Out of scope — do NOT touch:**
- `src/components/layouts/main-layout.tsx` — do **not** change the Drawer's
  `overflow-hidden` or `<main>`'s `overflow-y-auto`. Making the page scroll
  horizontally would hide this bug rather than fix it, and it affects every
  route.
- `src/pages/buckets/manage/page.tsx` and its `.container` — a global width
  change to fix one table is the wrong lever.
- `src/pages/buckets/manage/browse/object-actions.tsx` — its `<td>` is correct.
  The head is what is missing a cell.
- `tailwind.config.js` — no new breakpoints, no container customisation.
- The selection logic, `fullKey` composition, `getPublicAccess` usage, the
  `Public` badge, infinite scroll, or anything else in this file that is not a
  column count or a width class.
- Converting the table to a grid/flex layout. That is a rewrite; this is a
  three-line correctness fix.

## Git workflow

- Branch: `advisor/042-object-table-column-mismatch` from your given base.
- Conventional commits, e.g. `fix(ui): give the object table's actions column a header`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Give the actions column a header cell

In `Table.Head`, add a **fifth** child after `Last Modified`:

```tsx
          <span>Last Modified</span>
          {/* The actions column. `Table.Head` renders one <th> per child, so
              this must exist or the table declares 4 columns while every row
              renders 5 — the browser then has no header to size the actions
              column against, and it is pushed out of the container.
              Visually empty, but named for screen readers. */}
          <span className="sr-only">Actions</span>
```

`sr-only` keeps the header visually blank (matching the design — the other
action columns in this app have no visible label) while giving assistive
technology a name for the column.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 2: Make every `colSpan` agree with the real count

The table has **5** columns. Change the three `colSpan={4}` occurrences
(loading, error, empty) to `colSpan={5}`.

**Leave `<td colSpan={2} />` in the prefix row alone** — that one is a filler
spanning the Size and Last Modified columns, and it is already correct.
**Leave the load-more `colSpan={5}`** — already correct.

After this edit:

```
grep -n "colSpan" src/pages/buckets/manage/browse/object-list.tsx
```
→ exactly **five** matches: four `colSpan={5}` and one `colSpan={2}`.

**Verify**: the grep above, plus `pnpm run typecheck` → exit 0.

### Step 3: Make the filename constraint container-relative

Replace the viewport-relative cap with the flex idiom that actually truncates
inside a table cell. Two changes in the name cell:

1. On the wrapping span, add `min-w-0` — without it a flex child refuses to
   shrink below its content's intrinsic width, which is what defeats `truncate`.
2. On the filename span, drop `max-w-[40vw]` and keep `truncate`, adding
   `min-w-0` so it can shrink.

```tsx
                  <span className="flex items-center font-normal w-full min-w-0">
                    <FilePreview ext={ext?.substring(1)} object={object} />
                    <span className="truncate min-w-0">{filename}</span>
                    {ext && (
                      <span className="text-base-content/60 shrink-0">{ext}</span>
                    )}
                    {isPublic && (
                      <span className="badge badge-ghost badge-sm ml-2 shrink-0">
                        Public
                      </span>
                    )}
                  </span>
```

Note `shrink-0` added to the extension span so the file extension stays readable
when the stem truncates — truncating `report-2026-final.pdf` to
`report-2026-fin…` is more useful than losing the `.pdf`.

Also add `max-w-0 w-full` to the **name `<td>`** so the cell yields width to the
fixed-width columns rather than the reverse:

```tsx
                <td
                  className="cursor-pointer max-w-0 w-full"
                  role="button"
                  onClick={() => onObjectClick(object)}
                >
```

`max-w-0` on a table cell is the standard trick that makes `truncate` work in a
table: it tells the layout algorithm this cell may be squeezed, so the `Size`
and `Last Modified` columns (both `whitespace-nowrap`) keep their natural width
and the name absorbs the remainder.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step 4: Tests

Create `src/pages/buckets/manage/browse/object-list.test.tsx`.

**Read this before writing:** jsdom has **no layout engine** — it applies no CSS
and returns zeros from `getBoundingClientRect`. You therefore **cannot** assert
"the actions column is visible", "the table does not overflow", or anything
about pixels. Do not try, and do not stub layout to fake it.

What *is* testable, and is exactly the defect: **the header cell count must
equal the body cell count**. That is a DOM-structure property, and it is what
was actually wrong.

Mock the hooks so no network or `QueryClientProvider` is needed — model the
mocking on `src/components/containers/account-button.test.tsx` (`vi.hoisted` +
`vi.mock`). You will need to mock at least `useBrowseObjects`, `useAuth`,
`useConfig` and `useBucketContext`; check the component's imports and mock
whatever it actually calls. Wrap in `MemoryRouter` if any child renders a `Link`.

Cases:

1. **The header declares as many columns as a data row renders.** Render with one
   object; count `thead th` and compare with the *effective* cell count of the
   object's `tr` — summing `colSpan` (default 1) across its `td`s. Assert equal.
   *This is the regression guard for Defect 1.*
2. **The prefix (folder) row has the same effective width.** Render with one
   prefix; same summation; assert it equals the `th` count. Guards the
   `colSpan={2}` filler.
3. **The empty-state row spans the full table.** Render with no objects and no
   prefixes; assert the empty row's single `td` has `colSpan` equal to the `th`
   count. *Regression guard for Defect 2.* Do the same for the load-more row if
   it can be rendered without excessive setup; skip it if not, and say so.
4. **The actions column header exists and is named.** Assert a `th` with
   accessible text `Actions` is present. Guards someone "tidying away" the
   `sr-only` span.

Write a small helper for the summation so cases 1–3 share it:

```tsx
const effectiveCells = (row: HTMLTableRowElement) =>
  Array.from(row.querySelectorAll("td")).reduce(
    (n, td) => n + (Number(td.getAttribute("colspan")) || 1),
    0
  );
```

**Verify**: `pnpm exec vitest run object-list` → all pass.

### Step 5: Prove the tests can fail

A test that cannot fail is not a test.

1. Temporarily delete the `<span className="sr-only">Actions</span>` from
   `Table.Head`. Run `pnpm exec vitest run object-list` → **cases 1, 2 and 4
   must fail**. Revert.
2. Temporarily change one `colSpan={5}` back to `colSpan={4}`. Run again →
   **case 3 must fail**. Revert.

Report both failure counts, and confirm `git status --porcelain` is clean before
committing.

### Step 6: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
```
All exit 0.

### Step 7: Manual check — reviewer's job

You have no browser. Do **not** claim these passed; list them in NOTES:

1. A bucket with a very long object name, on a **wide** window with the sidebar
   visible: the name ellipsizes, `Size` and `Last Modified` are fully readable,
   and the Download / ⋮ actions are visible at the right edge.
2. The same at a narrow width (sidebar collapsed) — still no horizontal clipping.
3. A folder (prefix) row still lays out correctly and its ⋮ menu opens.
4. The empty state, the error state and `Load more` each still span the table.

## Done criteria

- [ ] `pnpm run typecheck`, `pnpm run test`, `pnpm run build` all exit 0
- [ ] 4 new tests in `object-list.test.tsx`, all passing
- [ ] Step 5's two mutations failed the named cases, and were reverted
- [ ] `grep -c "colSpan={5}" src/pages/buckets/manage/browse/object-list.tsx` → `4`
- [ ] `grep -c "colSpan={4}" src/pages/buckets/manage/browse/object-list.tsx` → `0`
- [ ] `grep -c "colSpan={2}" src/pages/buckets/manage/browse/object-list.tsx` → `1`
- [ ] `grep -c "max-w-\[40vw\]" src/pages/buckets/manage/browse/object-list.tsx` → `0`
- [ ] `git diff <BASE>..HEAD --stat` lists **only** `object-list.tsx` and the new test file
- [ ] `git diff <BASE>..HEAD -- src/components/layouts/ src/pages/buckets/manage/page.tsx src/pages/buckets/manage/browse/object-actions.tsx tailwind.config.js` is **empty**

## STOP conditions

- Any "Current state" excerpt does not match — the file has drifted.
- You are about to change `main-layout.tsx`'s overflow, the `.container`, or
  `tailwind.config.js`. Those hide the symptom instead of fixing the cause and
  affect every route.
- You are about to rewrite the table as a grid or flex layout. Out of scope.
- You find yourself stubbing `getBoundingClientRect` or otherwise faking layout
  to test visibility. jsdom cannot do it; test the column counts instead.
- Adding the 5th `<th>` visibly shifts or breaks the header row in a way you
  cannot resolve with `sr-only` — report rather than adding a visible label
  nobody asked for.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **`Table.Head` renders one `<th>` per child.** Any future column added to a row
  needs a matching child here, and every `colSpan` needs bumping. The tests in
  Step 4 turn that from a silent layout bug into a failing assertion — which is
  the real value of this plan, more than the three-line fix.
- **`max-w-0` on the name cell is load-bearing, not decorative.** It is what lets
  `truncate` work inside a table; removing it brings the overflow straight back.
  Same for `min-w-0` on the flex spans.
- **The layout's `overflow-hidden` is still there.** It is not the bug's cause,
  but it does mean any *future* table that exceeds its container will clip rather
  than scroll, with no scrollbar to hint at it. If a second table hits this,
  reconsider the wrapper — not the layout.
- **jsdom will never catch the visual half of this.** The column-count tests are
  a proxy for the real defect. A genuine regression in width handling can only be
  caught by eye or by a browser-based test, which this repo does not have.
