# Plan 032: Menu `hide()` middleware and intrinsic width sizing

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`. **Frontend-only** — do not touch any Go file.
>
> **Base reset FIRST**: `git checkout -B advisor/032-menu-sizing main` then
> `git log --oneline -1` — MUST show `b48f1ae` or newer, NOT `ee420fb`.
> SENTINEL: `grep -q "FloatingPortal" src/components/ui/menu.tsx && echo BASE_OK`
> MUST print `BASE_OK`. If `menu.tsx` has no `FloatingPortal`, plan 028 is not
> merged — STOP.

## Status

- **Priority**: P3 (polish on an already-working portal menu)
- **Effort**: S
- **Risk**: LOW (one shared component + two call-site class removals)
- **Depends on**: **028** (merged — the portal `Menu` this refines)
- **Category**: UI polish
- **Planned at**: commit `b48f1ae`, 2026-08-05

## Why this matters

Plan 028 already moved every action menu into a `FloatingPortal` with
`autoUpdate` / `flip` / `shift` / `offset` / `size`, so **clipping is solved**.
Two refinements from the follow-up request remain genuinely unimplemented:

1. **`hide()` is not in the middleware chain.** `autoUpdate` keeps the menu glued
   to its trigger, but when that trigger scrolls out of its scroll container the
   menu keeps floating — now detached over unrelated content, because the portal
   put it outside the clipping ancestor that used to hide it. `hide()` is
   Floating UI's answer: it reports when the reference is clipped/off-screen so
   the surface can hide itself.
2. **Fixed widths defeat the sizing rules.** Two call sites still hard-code a
   width, so long labels truncate or wrap oddly regardless of content:
   - `src/pages/settings/users-tab.tsx:262` → `className="w-56"`
   - `src/pages/cluster/components/nodes-list.tsx:276` → `className="min-w-40 gap-y-1"`

   Desired: **min width = trigger width; grow to fit content; cap at the viewport
   minus a safe margin.**

Everything else in the request — portal rendering, flip, shift, offset, size,
scroll/resize anchoring, Esc, outside click, keyboard navigation, ARIA, focus
preservation, the z-index layer scale — is already merged and verified. Do not
rebuild it.

## Current state (read before editing)

### `src/components/ui/menu.tsx` — the merged middleware chain (~line 110-135)

```tsx
  const { refs, floatingStyles, context } = useFloating({
    open,
    onOpenChange: setOpen,
    placement,
    whileElementsMounted: autoUpdate,
    middleware: [
      offset(offsetPx),
      flip({ padding: 8 }),
      shift({ padding: 8 }),
      size({
        padding: 8,
        apply({ availableHeight, rects, elements }) {
          if (matchTriggerWidth) {
            // …sets width to the reference width…
          }
          // …clamps max-height to availableHeight, never raising a class cap…
        },
      }),
    ],
  });
```
There is **no `hide()`**, and `size()`'s `apply` only sets width when
`matchTriggerWidth` is true.

The surface is rendered inside:
```tsx
        <FloatingPortal root={portalRoot ?? undefined}>
          <FloatingFocusManager context={context} modal={false}>
            {/* floating surface with floatingStyles + Z_LAYERS.dropdown */}
```

`MenuProps` currently exposes `placement`, `offsetPx`, `matchTriggerWidth`,
`className`, `triggerClassName`, `triggerLabel`, `portalRoot`.

### The two call sites with fixed widths

`src/pages/settings/users-tab.tsx` (~line 259-265):
```tsx
                    <Menu
                      placement="bottom-end"
                      triggerLabel={`Actions for ${user.username}`}
                      className="w-56"
                      trigger={<EllipsisVertical size={20} />}
                    >
```

`src/pages/cluster/components/nodes-list.tsx` (~line 274-278):
```tsx
                  <Menu
                    placement="bottom-end"
                    className="min-w-40 gap-y-1"
                    trigger={<EllipsisVertical />}
                  >
```

### Conventions

`cn()` from `@/lib/utils` merges classes. `Z_LAYERS` lives in `@/lib/z-layers`.
Tests use Vitest + Testing Library; `src/components/ui/menu.test.tsx` is the
pattern to extend (7 tests today, including the portal-escapes-overflow one).

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Menu tests | `npx pnpm@9 exec vitest run src/components/ui/menu.test.tsx` | pass |
| Full tests | `npx pnpm@9 run test` | pass (no regressions) |
| Build | `npx pnpm@9 run build` | exit 0 |

> If `node_modules` is missing `@floating-ui/react`, run `npx pnpm@9 install`
> first — it is already in `package.json`.

## Scope

**In scope**:
- `src/components/ui/menu.tsx`
- `src/components/ui/menu.test.tsx`
- `src/pages/settings/users-tab.tsx` (remove `w-56`)
- `src/pages/cluster/components/nodes-list.tsx` (remove `min-w-40`)

**Out of scope** — do NOT touch:
- The portal, `flip`, `shift`, `offset`, `autoUpdate`, focus, ARIA, keyboard or
  z-index code — **all already correct**; this plan only adds `hide()` and width.
- Any `Modal`/`<dialog>` (native top layer, never clipped) or `sonner` toasts.
- **Do not build** a Tooltip, Combobox, Command Palette, Context Menu, Date/Color
  Picker, nested-menu `FloatingTree`, scroll-locking, or a virtualized-table
  integration. None have callers; that was rejected scope in 028 and stays
  rejected. If you think one is needed, STOP and report.
- Any Go file, `plans/`.

## Steps

### Step 1 — Add `hide()` to the middleware chain

Import `hide` from `@floating-ui/react` and append it **last** in `middleware`
(it must run after positioning so it sees final coordinates):

```tsx
      // Last on purpose: hide() reports on the *final* position. When the
      // trigger scrolls out of its container the portal surface would otherwise
      // keep floating over unrelated content — the portal removed the clipping
      // ancestor that used to hide it, so we hide it explicitly.
      hide({ padding: 8 }),
```

Apply it to the surface style, merged with `floatingStyles`:

```tsx
  const { middlewareData } = ...;               // from useFloating
  const hidden = middlewareData.hide?.referenceHidden;
  // on the floating element:
  style={{ ...floatingStyles, zIndex: Z_LAYERS.dropdown,
           visibility: hidden ? "hidden" : "visible" }}
```

Use `visibility`, **not** `display: none` — the element must keep its box so
`autoUpdate` and focus management continue to work while it is out of view.

**Verify**: `npx pnpm@9 run typecheck` exits 0; `grep -c "hide(" src/components/ui/menu.tsx` ≥ 1.

### Step 2 — Intrinsic width in `size()`

Replace the width half of `size()`'s `apply` so it implements the three rules.
Keep the existing max-height clamping exactly as it is.

```tsx
        apply({ availableWidth, availableHeight, rects, elements }) {
          // Width rules:
          //  • at least as wide as the trigger (never narrower than what was clicked)
          //  • otherwise intrinsic — the surface grows to fit its longest item
          //  • never wider than the viewport minus the same padding used above
          // `matchTriggerWidth` remains an opt-in exact match for callers that
          // want a select-style surface.
          Object.assign(elements.floating.style, {
            minWidth: matchTriggerWidth
              ? `${rects.reference.width}px`
              : `${Math.min(rects.reference.width, availableWidth)}px`,
            width: matchTriggerWidth ? `${rects.reference.width}px` : "max-content",
            maxWidth: `${availableWidth}px`,
          });
          // …existing max-height clamp, unchanged…
        },
```
`width: "max-content"` is what makes the surface grow to its longest item;
`maxWidth: availableWidth` is the viewport cap that stops it overflowing;
`minWidth` enforces the trigger-width floor.

Ensure the surface's base classes do **not** re-impose a width. If a
`whitespace-nowrap` is present it will prevent wrapping at the cap — allow
wrapping instead (the spec asks to avoid unnecessary truncation), e.g. keep text
wrapping enabled and do not add `truncate` to menu items.

**Verify**: `npx pnpm@9 run typecheck` exits 0.

### Step 3 — Remove the fixed widths at the two call sites

- `src/pages/settings/users-tab.tsx:262` — delete `className="w-56"` (drop the
  prop entirely if nothing else is in it).
- `src/pages/cluster/components/nodes-list.tsx:276` — change
  `className="min-w-40 gap-y-1"` to `className="gap-y-1"` (keep the gap).

**Verify**:
```
grep -c "w-56" src/pages/settings/users-tab.tsx            # → 0
grep -c "min-w-40" src/pages/cluster/components/nodes-list.tsx   # → 0
```

### Step 4 — Tests

Extend `src/components/ui/menu.test.tsx` (keep the existing 7 passing):

- **`hide()` wiring**: with the menu open, the surface carries a `visibility`
  style (`visible` in the normal case). Asserting the *hidden* case in jsdom is
  unreliable — jsdom has no real layout — so assert the style property is
  managed rather than trying to fake a scrolled-away reference. Say so in a
  comment rather than writing a test that cannot fail.
- **Intrinsic width**: a menu whose longest item is much wider than the trigger
  gets `width: max-content` and a `maxWidth` set (jsdom reports the inline
  style even without layout).
- **`matchTriggerWidth`** still sets an explicit pixel `width` when true.
- Existing behaviour unchanged: portal escape, Esc, outside click, ArrowDown,
  item click, disabled item.

**Verify**: `npx pnpm@9 exec vitest run src/components/ui/menu.test.tsx` → all pass.

### Step 5 — Gate sweep

```
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```
Commit on `advisor/032-menu-sizing`:
`fix: hide menus when the trigger scrolls away and size them to content`

## Test plan

- Unit tests above cover the inline-style contract (jsdom cannot verify layout).
- **Reviewer live verification** (advisor's job), in a real browser:
  1. **Users tab** — open a row menu; it is as wide as its longest item
     ("Reset password"), not a fixed 224 px, and no label truncates.
  2. **Cluster nodes** — same, with the longer labels there.
  3. **`hide()`**: open a row menu in the object browser, then scroll the table
     so the trigger leaves the visible area — the menu must disappear rather than
     float over unrelated content.
  4. Very long username → the menu grows but never exceeds the viewport, and the
     text wraps instead of being cut.
  5. Narrow viewport (390 px) → menu stays fully on-screen.
  6. Re-confirm nothing regressed: menu still escapes the table's
     `overflow-x-auto`, still closes on Esc/outside click, keyboard nav works.

## Done criteria

- [ ] `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` exit 0
- [ ] `grep -c "hide(" src/components/ui/menu.tsx` ≥ 1 and `hide` is **last** in the middleware array
- [ ] `grep -c "referenceHidden" src/components/ui/menu.tsx` ≥ 1
- [ ] `grep -c "max-content" src/components/ui/menu.tsx` ≥ 1
- [ ] `grep -c "w-56" src/pages/settings/users-tab.tsx` → `0`
- [ ] `grep -c "min-w-40" src/pages/cluster/components/nodes-list.tsx` → `0`
- [ ] `git diff --name-only b48f1ae..HEAD` shows only the 4 in-scope files; zero `.go`

## STOP conditions

- `hide()` makes menus disappear during **normal** use (not just when the trigger
  scrolls away) — the middleware order or the `visibility` binding is wrong; STOP.
- Intrinsic width causes a menu to overflow the viewport on a narrow screen —
  `maxWidth`/`availableWidth` is not wired correctly; STOP.
- You are tempted to build a Tooltip / nested menus / scroll-lock / virtualized
  table support — explicitly out of scope; STOP.
- You need to change `overflow` on any ancestor — that is the CSS hack the whole
  design rejects; STOP.

## Maintenance notes

- **`hide()` must stay last** in the middleware array; it reads the final
  computed position. Re-ordering silently breaks it.
- **`visibility`, not `display`** — the surface must keep its box so `autoUpdate`
  and focus management keep working while hidden.
- **Widths belong in `size()`, not at call sites.** The two fixed widths removed
  here are exactly what this plan exists to prevent; a new `w-*` on a `Menu`
  surface is a regression.
- `matchTriggerWidth` stays for select-style callers that want an exact match;
  the default is now intrinsic sizing.
