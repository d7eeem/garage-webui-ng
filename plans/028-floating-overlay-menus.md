# Plan 028: Portal-based floating menus (fix dropdown clipping)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it). This is a **frontend-only** plan —
> do not touch any Go file.
>
> **Base reset FIRST**: `git checkout -B advisor/028-floating-overlay-menus main`
> then `git log --oneline -1` — MUST show `4f9d4db` or newer, NOT `ee420fb`.
> SENTINEL: `test -f src/pages/settings/users-tab.tsx && grep -q "react-daisyui" package.json && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P2 (real UX defect, shipped and visible)
- **Effort**: M
- **Risk**: MED (touches every row-action menu in the app; a mistake makes menus unreachable rather than merely clipped)
- **Depends on**: nothing.
- **Category**: bug / UI architecture
- **Planned at**: commit `4f9d4db`, 2026-08-04

## Why this matters

`react-daisyui`'s `Dropdown` is a **pure-CSS component with no portal** — the menu
renders as a sibling `<ul>` inside the trigger's DOM position. Every one of this
app's action menus therefore lives inside a scroll container and is clipped by it.

**The codebase already contains three separate workarounds for this one root
cause**, which is the clearest evidence the approach is wrong:

1. `src/pages/buckets/manage/browse/object-list.tsx:188` — flips the last rows'
   menus upward by hand:
   ```tsx
   end={idx >= objects.length - 2 && objects.length > 5}
   ```
2. `src/pages/cluster/components/nodes-list.tsx:274-276` — the same hack again,
   written differently:
   ```tsx
   vertical={idx > 2 && idx >= items.length - 2 ? "top" : "bottom"}
   ```
3. `src/app/styles.css:27-29` plus `src/pages/settings/users-tab.tsx:290`
   (`className="z-10 w-56"`) — z-index patches applied where menus were seen to
   render underneath things.

None of these fix clipping, because **no z-index or placement value escapes an
`overflow` ancestor**. Only a portal does.

### The five clipped call sites (all confirmed)

| Call site | Enclosing overflow container |
|---|---|
| `src/pages/buckets/manage/browse/object-actions.tsx:61` | `object-list.tsx:83` — `overflow-x-auto min-h-[400px]` |
| `src/pages/settings/users-tab.tsx:285` | `users-tab.tsx:242` — `w-full overflow-x-auto` |
| `src/pages/cluster/components/nodes-list.tsx:272` | `nodes-list.tsx:189` — `overflow-x-auto **overflow-y-hidden**` |
| `src/components/containers/sidebar.tsx:70` (theme picker) | `sidebar.tsx:36` — `overflow-hidden` on the `<aside>` |
| `src/pages/buckets/manage/components/menu-button.tsx:35` | page header; migrate for consistency |

`nodes-list` is the worst case: `overflow-y-hidden` guarantees vertical clipping
no matter which direction the menu opens. On top of all of these,
`src/components/layouts/main-layout.tsx:46` makes `<main>` `overflow-y-auto`, so
menus near the bottom of any page clip against the viewport too.

### What is NOT affected (do not "fix" these)

- **Modals** — `react-daisyui`'s `Modal` renders a native `<dialog>`
  (`Modal.d.ts` exposes `dialogRef: React.RefObject<HTMLDialogElement>`). Native
  dialogs render in the browser's **top layer**, above all normal content
  regardless of z-index, and are never clipped. Leave every dialog alone.
- **Toasts** — `sonner`'s `<Toaster />` (`src/app/app.tsx:20`) already portals.

## Current state (read before editing)

### The canonical call site — `src/pages/buckets/manage/browse/object-actions.tsx:59-84`

```tsx
      <div className="hidden md:flex flex-row items-center">
        <Dropdown end vertical={end ? "top" : "bottom"}>
          <Dropdown.Toggle button={false}>
            <Button icon={EllipsisVertical} color="ghost" shape="circle" />
          </Dropdown.Toggle>

          <Dropdown.Menu className="gap-y-1">
            <Dropdown.Item
              onClick={() => shareDialog.open({ key: object.objectKey, prefix })}
            >
              <Share2 size={16} /> Share
            </Dropdown.Item>
            {canWrite && (
              <Dropdown.Item onClick={onDelete} className="bg-error/10 text-error">
                <Trash size={16} /> Delete
              </Dropdown.Item>
            )}
          </Dropdown.Menu>
        </Dropdown>
      </div>
```
The component's props include `end?: boolean` purely to drive that manual flip.

### `src/pages/settings/users-tab.tsx` — has a local `ActionItem` wrapper (lines ~57-81)

It exists because `Dropdown.Item` renders an anchor with no `disabled` support;
it applies daisyUI's `disabled` class, guards the handler, and shows a reason via
`title`. **Preserve that behaviour** when migrating — the new `MenuItem` must
support `disabled` + a reason tooltip natively.

### `src/app/styles.css:27-29` — the global z-index patch to delete

```css
  .dropdown-content {
    @apply z-10;
  }
```

### UI component conventions — `src/components/ui/`

Thin local wrappers over `react-daisyui`, default-exported, with named
sub-exports where useful (see `src/components/ui/input.tsx`, `toggle.tsx`,
`button.tsx`). The new primitive belongs here and must follow that shape.
`cn()` from `@/lib/utils` is the className merge helper.

### Test conventions

Vitest + jsdom, with `@testing-library/react`, `@testing-library/user-event` and
`@testing-library/jest-dom` already installed. Component tests exist at
`src/pages/buckets/page.test.tsx` — follow its import/render style.

## Commands

`pnpm` is not installed → use `npx pnpm@9 <cmd>`; run `npx pnpm@9 install` once
first in a fresh worktree.

| Purpose | Command | Expected |
|---|---|---|
| Add dep | `npx pnpm@9 add @floating-ui/react` | package.json + lockfile updated |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Tests | `npx pnpm@9 run test` | pass |
| Build | `npx pnpm@9 run build` | exit 0 |
| Lint new files | `npx pnpm@9 exec eslint src/components/ui/menu.tsx src/lib/z-layers.ts` | 0 errors |

## Scope

**In scope**:
- `package.json` / `pnpm-lock.yaml` (add `@floating-ui/react`)
- `src/lib/z-layers.ts` (create — the layer scale)
- `src/components/ui/menu.tsx` (create — the portal menu primitive)
- `src/components/ui/menu.test.tsx` (create)
- `src/app/styles.css` (remove the `.dropdown-content` z-index patch)
- `tailwind.config.js` (z-index tokens)
- The five call sites: `object-actions.tsx`, `object-list.tsx` (drop the `end`
  prop), `users-tab.tsx`, `nodes-list.tsx`, `sidebar.tsx`, `menu-button.tsx`

**Out of scope** — do NOT touch:
- Any `Modal` / dialog (native `<dialog>`, already top-layer).
- `sonner` / `<Toaster />`.
- Any Go file, the auth/store packages, `plans/`.
- **Do not build Combobox, Command Palette, Color Picker, Date Picker,
  Autocomplete, or generic "floating panels"** — this app has no callers for
  them. Building unused primitives is explicitly rejected scope.
- Do not restyle the menus. Visual output should be near-identical; only the
  positioning mechanism changes.

## Steps

### Step 1 — Dependency

```
npx pnpm@9 add @floating-ui/react
```
**Verify**: `grep '"@floating-ui/react"' package.json` matches; `npx pnpm@9 run typecheck` exits 0.

### Step 2 — `src/lib/z-layers.ts`: one place for stacking order

```ts
/**
 * Central stacking order for overlay surfaces. Two rules:
 *
 * 1. Every floating surface takes its z-index from here — never an ad-hoc
 *    `z-10` at a call site. That is what produced the scattered patches this
 *    file replaces.
 * 2. Native <dialog> (daisyUI Modal) is NOT in this scale. Dialogs render in
 *    the browser's top layer, which paints above every z-index here no matter
 *    how large. A menu that must appear inside a dialog has to be portalled
 *    into that dialog — see `portalRoot` in components/ui/menu.tsx.
 */
export const Z_LAYERS = {
  dropdown: 1000,
  popover: 1100,
  tooltip: 1200,
  toast: 1300,
} as const;

export type ZLayer = keyof typeof Z_LAYERS;
```

Mirror these as Tailwind tokens in `tailwind.config.js` so classes stay usable:
```js
  theme: {
    extend: {
      zIndex: { dropdown: "1000", popover: "1100", tooltip: "1200", toast: "1300" },
    },
  },
```

### Step 3 — `src/components/ui/menu.tsx`: the primitive

Build it with `@floating-ui/react`, following that library's documented
dropdown-menu recipe. Required behaviour:

- **Portal**: wrap the floating element in `<FloatingPortal root={portalRoot}>`
  (default root = `document.body`). This is the entire point — it escapes every
  `overflow` ancestor.
- **Positioning**: `useFloating({ placement, whileElementsMounted: autoUpdate, middleware: [offset(…), flip(), shift({ padding: 8 }), size(…)] })`.
  `autoUpdate` gives scroll/resize/anchor tracking for free — it is why the manual
  `end`/`vertical` hacks can be deleted.
- **Interactions**: `useClick`, `useDismiss` (Esc + outside click),
  `useRole({ role: "menu" })`, `useListNavigation` (arrow keys, Home/End),
  `useTypeahead` — composed through `useInteractions`.
- **Focus**: wrap content in `<FloatingFocusManager context={…} modal={false}>`
  so focus returns to the trigger on close. `modal={false}` is deliberate: a menu
  must not trap focus like a dialog.
- **ARIA**: the trigger gets `aria-haspopup="menu"`, `aria-expanded`; the surface
  `role="menu"`; items `role="menuitem"`. `useRole`/`useInteractions` supply most
  of this — do not hand-roll conflicting attributes.
- **z-index**: `Z_LAYERS.dropdown` via inline style or the `z-dropdown` class.
- **Animation**: a short fade/scale (~120 ms) — `useTransitionStyles` from
  `@floating-ui/react` is the least-code option. Keep it subtle.

Public API (keep it small — these are the only shapes the call sites need):

```tsx
type MenuProps = {
  trigger: React.ReactNode;            // rendered inside the reference button
  children: React.ReactNode;           // MenuItem list
  placement?: Placement;               // default "bottom-end"
  offsetPx?: number;                   // default 4
  matchTriggerWidth?: boolean;         // default false; uses size() when true
  className?: string;                  // extra classes on the surface
  portalRoot?: HTMLElement | null;     // escape hatch — see Maintenance notes
};

type MenuItemProps = {
  onClick?: () => void;
  disabled?: boolean;
  disabledReason?: string;             // shown via title= when disabled
  icon?: LucideIcon;
  className?: string;
  children: React.ReactNode;
};
```

Style the surface with the existing daisyUI classes (`menu bg-base-100 rounded-box
shadow p-2`) so it looks the same as today.

**Verify**: `npx pnpm@9 run typecheck` exits 0.

### Step 4 — Migrate `object-actions.tsx` (and drop the flip hack)

Replace the `Dropdown` block with `<Menu trigger={…} placement="bottom-end">` +
`MenuItem`s. Then:
- **Delete the `end?: boolean` prop** from `Props`.
- In `src/pages/buckets/manage/browse/object-list.tsx:188`, **delete**
  `end={idx >= objects.length - 2 && objects.length > 5}`.

**Verify**: `grep -n "end=" src/pages/buckets/manage/browse/object-list.tsx` → nothing;
`grep -c "vertical=" src/pages/buckets/manage/browse/object-actions.tsx` → `0`.

### Step 5 — Migrate `nodes-list.tsx` (drop the second flip hack)

Replace the `Dropdown` and **delete** the
`vertical={idx > 2 && idx >= items.length - 2 ? "top" : "bottom"}` expression.

**Verify**: `grep -c "vertical=" src/pages/cluster/components/nodes-list.tsx` → `0`.

### Step 6 — Migrate `users-tab.tsx`

Replace the `Dropdown` block. Fold the local `ActionItem` wrapper into
`MenuItem`'s native `disabled` + `disabledReason` — then **delete `ActionItem`**.
Remove the `z-10` from the old `Dropdown.Menu className`. Keep every existing
guard (the disabled reasons for self-delete / last-admin) exactly as they read now.

**Verify**: `grep -c "ActionItem" src/pages/settings/users-tab.tsx` → `0`;
`grep -c "z-10" src/pages/settings/users-tab.tsx` → `0`.

### Step 7 — Migrate `sidebar.tsx` (theme picker) and `menu-button.tsx`

- `sidebar.tsx`: the theme list is long — pass `className="max-h-[500px] overflow-y-auto"`
  onto the surface and use `placement="top-start"`. This one currently clips
  against the `<aside>`'s `overflow-hidden`, so verify it opens fully.
- `menu-button.tsx`: straight swap, `placement="bottom-end"`.

**Verify**: `grep -rn "react-daisyui" src --include="*.tsx" | grep -c Dropdown` → `0`
(no `Dropdown` import remains anywhere).

### Step 8 — Remove the global z-index patch

Delete from `src/app/styles.css`:
```css
  .dropdown-content {
    @apply z-10;
  }
```
**Verify**: `grep -c "dropdown-content" src/app/styles.css` → `0`.

### Step 9 — Tests

Create `src/components/ui/menu.test.tsx` (Vitest + Testing Library). Cover:

- **Portals out of an overflow parent** — the decisive test. Render the menu
  inside `<div style={{ overflow: "hidden" }} data-testid="clip">`, open it, and
  assert the surface is **not** a descendant of that div:
  ```tsx
  expect(screen.getByTestId("clip")).not.toContainElement(screen.getByRole("menu"));
  ```
- Opens on trigger click; `role="menu"` present; trigger has `aria-expanded="true"`.
- **Escape** closes and focus returns to the trigger.
- **Outside click** closes.
- Arrow-Down moves focus to the first item (keyboard nav).
- `MenuItem` `onClick` fires and the menu closes.
- A `disabled` item does **not** fire `onClick` and exposes its `disabledReason`.

**Verify**: `npx pnpm@9 exec vitest run src/components/ui/menu.test.tsx` → all pass.

### Step 10 — Full gate sweep

```
npx pnpm@9 run typecheck
npx pnpm@9 run test
npx pnpm@9 run build
```
Commit on `advisor/028-floating-overlay-menus`:
`fix: render action menus in a portal so they are never clipped`

## Test plan

- The unit tests above are the contract; the **portal-escapes-overflow** assertion
  is the one that would have caught the original bug.
- **Reviewer live verification** (advisor's job) — the manual matrix, run against
  a real instance in a browser:

  | Check | Where |
  |---|---|
  | Menu not clipped horizontally | Buckets → Browse (wide table, scrolled right) |
  | Menu not clipped vertically | Cluster → nodes table (`overflow-y-hidden`) |
  | Last-row menu opens fully | Browse + Users, bottom row of a long list |
  | Stays attached while scrolling | Scroll `<main>` with a menu open |
  | Works inside a card | Settings → Users |
  | Theme picker opens fully | Sidebar (inside `overflow-hidden`) |
  | Esc / outside click close it | Any menu |
  | Keyboard nav works | Tab to trigger, Enter, arrows |
  | Mobile viewport | 390 px wide — menu stays on-screen (`shift()`) |

## Done criteria

- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` all exit 0
- [ ] `grep -rn "from \"react-daisyui\"" src --include="*.tsx" | xargs -I{} echo {} | grep -c Dropdown` → `0`
- [ ] `grep -rn "vertical=" src/pages/cluster/components/nodes-list.tsx src/pages/buckets/manage/browse/object-actions.tsx` → nothing
- [ ] `grep -c "dropdown-content" src/app/styles.css` → `0`
- [ ] `grep -rn "z-10" src/pages/settings/users-tab.tsx` → nothing
- [ ] `grep -c "ActionItem" src/pages/settings/users-tab.tsx` → `0`
- [ ] The portal test in `menu.test.tsx` passes
- [ ] `git diff --name-only 4f9d4db..HEAD` shows only in-scope files; **zero `.go` files**

## STOP conditions

- Base reset shows `ee420fb` or SENTINEL missing.
- A migrated menu becomes **unreachable** (renders but cannot be opened/clicked) —
  report rather than papering over with z-index.
- You conclude a `Modal`/`<dialog>` must change to make menus work — STOP. Dialogs
  are top-layer and out of scope; if a menu is ever needed *inside* one, that is
  what `portalRoot` is for.
- You find yourself building Combobox / Command Palette / Date Picker / Color
  Picker / Autocomplete — explicitly out of scope; STOP.
- `@floating-ui/react` fails to bundle in `vite build` — report; do not hand-roll
  positioning.

## Maintenance notes

- **`autoUpdate` is what deletes the hacks.** It re-measures on scroll, resize and
  layout shift, so `flip()`/`shift()` keep the menu on-screen without any
  index-based "is this one of the last rows?" logic. If anyone re-introduces a
  manual `vertical`/`end` calculation, that is a regression.
- **The `<dialog>` top-layer trap.** A native `<dialog>` paints above *every*
  z-index. A menu portalled to `document.body` while a modal is open will be
  hidden behind that modal. If a menu is ever needed inside a dialog, pass
  `portalRoot={dialogElement}` so it portals into the dialog's own subtree. No
  current call site needs this — but the first one that does will fail confusingly
  without it, which is why the escape hatch exists.
- **One z-index source.** `src/lib/z-layers.ts` (mirrored as Tailwind tokens) is
  the only place stacking order is decided. Ad-hoc `z-*` on a floating surface is
  how the previous patches accumulated.
- **`useDismiss` handles nesting**: nested menus close inside-out via
  `FloatingTree`. Not needed today (no nested menus exist); if one is added, wrap
  the tree rather than hand-managing open state.
