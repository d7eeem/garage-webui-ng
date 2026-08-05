# Plan 033: Move the signed-in username and Settings into the header's right corner

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. The reviewer who dispatched you maintains
> `plans/README.md`; do **not** edit it.
>
> **Drift check (run first)**:
> ```
> git diff --stat f9cd2c6..HEAD -- src/components/layouts/main-layout.tsx src/components/containers/sidebar.tsx src/components/containers/account-button.tsx
> ```
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: dx (UI/UX)
- **Planned at**: commit `f9cd2c6`, 2026-08-05

## Why this matters

Today the account cluster is stranded at the **bottom-left of the sidebar**: a
theme picker, a small "Signed in as …" line, and a Logout button, while
**Settings** sits as a fifth entry in the main nav list above them. Who you are
signed in as is the one piece of state that is true on every page, and it is
currently rendered in the least-looked-at corner of the screen, in 12 px muted
text, below the fold of a scrollable nav.

After this plan, the header's right corner carries a single **account chip** —
a gear icon plus the signed-in username — that navigates to Settings. That is
where every comparable admin console puts identity, it is visible on every route
including mobile, and it collapses two separate affordances ("who am I" and "my
settings") into one target.

**The maintainer decided the scope explicitly** (2026-08-05), and both halves
matter:

1. **Only the username and Settings move.** The theme picker and the Logout
   button **stay** in the sidebar's bottom bar. Do not move them.
2. **Settings leaves the sidebar nav list.** The header chip becomes the *only*
   way to reach `/settings` from the UI. This is why the chip must render
   unconditionally — see the STOP condition about it.

### Why a link and not a dropdown

The chip is a **plain link**, not a menu. With theme and logout deliberately
staying in the sidebar, a dropdown would contain exactly one item, costing a
click to reach its only destination. Do **not** build a `<Menu>` here, and do
not add a chevron or any other menu affordance.

## Current state

### Files

- `src/components/layouts/main-layout.tsx` — the authenticated shell. Its inner
  `Header` component renders the back/hamburger button, the page title, and a
  per-page `actions` slot. **The chip goes here.**
- `src/components/containers/sidebar.tsx` — the nav rail. Holds the `pages`
  array (with the `Settings` entry to remove) and the bottom bar (with the
  "Signed in as …" line to remove).
- `src/components/containers/account-button.tsx` — **does not exist yet**; you
  create it.
- `src/hooks/useAuth.ts` — read-only here; supplies `username`.

### `src/components/layouts/main-layout.tsx:59-93` — the header as it exists today

```tsx
const Header = ({ onSidebarOpen }: HeaderProps) => {
  const page = useContext(PageContext);
  const navigate = useNavigate();

  return (
    <header className="bg-base-100 px-4 md:px-8">
      <div className="container h-16 md:h-20 flex flex-row items-center gap-4">
        {page?.prev ? (
          <Button
            href={page.prev}
            onClick={() => navigate(page.prev!, { replace: true })}
            color="ghost"
            shape="circle"
            className="-mx-2"
          >
            <ArrowLeft />
          </Button>
        ) : (
          <Button
            icon={MenuIcon}
            color="ghost"
            className="md:hidden -mx-2"
            onClick={onSidebarOpen}
          />
        )}

        <h1 className="text-xl flex-1 truncate">
          {page?.title || "Dashboard"}
        </h1>

        {page?.actions}
      </div>
    </header>
  );
};
```

`{page?.actions}` is a **live slot with two real callers** —
`src/pages/buckets/manage/page.tsx:51` and
`src/pages/buckets/manage/browse/browse-tab.tsx:68` both push a node into it.
Your chip must sit **after** it (rightmost) and must not replace or wrap it.

### `src/components/containers/sidebar.tsx:24-30` — the nav array

```tsx
const pages = [
  { icon: LayoutDashboard, title: "Dashboard", path: "/", exact: true },
  { icon: HardDrive, title: "Cluster", path: "/cluster" },
  { icon: ArchiveIcon, title: "Buckets", path: "/buckets" },
  { icon: KeySquare, title: "Keys", path: "/keys" },
  { icon: Settings, title: "Settings", path: "/settings" },
];
```

### `src/components/containers/sidebar.tsx:70-100` — the bottom bar

```tsx
      <div className="py-2 px-4 flex items-center gap-2">
        <Menu
          placement="top-start"
          triggerLabel="Theme"
          triggerClassName={cn("btn btn-ghost", auth.isEnabled && "btn-circle")}
          trigger={
            <>
              <Palette size={18} className={!auth.isEnabled ? "-ml-1" : ""} />
              {!auth.isEnabled ? "Theme" : null}
            </>
          }
          className="max-h-[500px] overflow-y-auto"
        >
          {themes.map((theme) => (
            <MenuItem key={theme} onClick={() => appStore.setTheme(theme)}>
              {ucfirst(theme)}
            </MenuItem>
          ))}
        </Menu>

        {auth.isEnabled ? (
          <div className="flex-1 flex flex-col items-stretch min-w-0">
            {auth.username ? (
              <p className="text-xs text-base-content/60 truncate px-1">
                Signed in as {auth.username}
              </p>
            ) : null}
            <LogoutButton />
          </div>
        ) : null}
      </div>
```

The `<Menu>` block (theme picker) and `<LogoutButton />` **stay exactly as they
are**. Only the `<p>Signed in as …</p>` and the now-pointless `flex-col`
wrapper around it go away.

### `src/hooks/useAuth.ts` — the shape you consume

```ts
export const useAuth = () => {
  const { data, isLoading } = useQuery({
    queryKey: ["auth"],
    queryFn: () => api.get<AuthResponse>("/auth/status"),
    retry: false,
  });
  const role = data?.role;
  return {
    isLoading,
    isEnabled: data?.enabled ?? true,
    isAuthenticated: data?.authenticated ?? false,
    needsSetup: data?.needsSetup ?? false,
    username: data?.username,   // ← `string | undefined`
    role,
    canWrite: role !== "viewer",
  };
};
```

`username` is **optional**. The chip must render a working Settings link even
when it is `undefined` (the query is still in flight on first paint). This is
tested.

### Conventions this repo uses — match them

- **Routing**: `createBrowserRouter` is configured with `basename: BASE_PATH`
  (`src/app/router.tsx:65`). So a link target is written as the plain
  path — `to="/settings"` — and **never** manually prefixed with `BASE_PATH`.
  The sidebar's own nav links do exactly this (`sidebar.tsx:54`).
- **A `Link` styled as a button is established here**, and is what you should
  use. `sidebar.tsx:54-64` renders `<Link to={page.path} className={cn("h-12 flex items-center px-6", …)}>`
  directly rather than going through `src/components/ui/button.tsx`. Follow
  that: a bare `react-router-dom` `Link` with daisyUI `btn` classes. Do **not**
  route this through the `Button` wrapper — it applies `shape="circle"`
  automatically when given an icon and no children, which fights the layout you
  want.
- **Class composition**: always `cn(...)` from `@/lib/utils`. Conditional classes
  are written `isActive && "…"` inside `cn`, as in `sidebar.tsx:56-60`.
- **Imports**: the `@/` alias maps to `src/`. Icons come from `lucide-react`.
- **Component files**: one component per file, `export default` at the bottom.
- **Comments**: this repo comments the *why*, not the *what*, in full sentences.
  See the block comment atop `src/components/ui/menu.tsx:35-47` for the register
  to match. A short block comment on the new component explaining why it is a
  link and not a menu is expected.
- **`noUnusedLocals` is `true`** (`tsconfig.app.json:19`). Removing the last use
  of an import is a **typecheck error**, not a warning. This bites you in Step 3.

## Commands you will need

| Purpose   | Command                                        | Expected on success        |
|-----------|------------------------------------------------|----------------------------|
| Install   | `npx pnpm@9 install --frozen-lockfile`         | exit 0                     |
| Typecheck | `npx pnpm@9 run typecheck`                     | exit 0, no output          |
| Tests     | `npx pnpm@9 run test`                          | all files pass             |
| One test  | `npx pnpm@9 exec vitest run account-button`    | 4 passed                   |
| Build     | `npx pnpm@9 run build`                         | exit 0, writes `dist/`     |

`pnpm` is **not** installed globally on this machine — invoke it via
`npx pnpm@9` exactly as written above. `pnpm run lint` is **expected to be red**
(the repo carries a large pre-existing `@typescript-eslint/no-explicit-any`
backlog and CI runs lint `continue-on-error`). Do not try to clear it. Just make
sure you introduce **no new** lint errors in the files you touch.

## Scope

**In scope** (the only files you may modify):
- `src/components/containers/account-button.tsx` (**create**)
- `src/components/containers/account-button.test.tsx` (**create**)
- `src/components/layouts/main-layout.tsx` (modify — one import, one line in `Header`)
- `src/components/containers/sidebar.tsx` (modify — remove nav entry, remove username line, drop two now-unused imports)

**Out of scope** (do NOT touch, even though they look related):
- `src/components/ui/menu.tsx` — the shared portal-menu primitive. The theme
  picker keeps using it unchanged. You are not building a menu in this plan.
- `src/app/router.tsx` — the `/settings` route is unchanged. Only the way the
  user *reaches* it changes.
- `src/pages/settings/**` — the Settings page itself is untouched.
- `src/hooks/useAuth.ts` — read it, do not change it.
- `src/context/page-context.tsx` and the `actions` slot contract — the chip sits
  beside `page?.actions`, it does not go through it.
- The Logout button and the theme picker in `sidebar.tsx` — they stay. Moving
  them is explicitly **not** what was asked for.
- `README.md`, `docs/**`, and every screenshot under them. The screenshots will
  show the old sidebar after this lands; regenerating them is deliberately
  deferred (see Maintenance notes).
- `plans/README.md` — the dispatching reviewer maintains the index.

## Git workflow

- Branch: `advisor/033-header-account-settings`
- **Step Zero (do this first, before any edit):** the worktree you are in may be
  based on a stale commit. Run:
  ```
  git checkout -B advisor/033-header-account-settings main
  ```
  Then confirm you are on the right base — this repo's `main` already contains
  the portal `Menu` primitive:
  ```
  test -f src/components/ui/menu.tsx && echo SENTINEL_OK
  ```
  If that does not print `SENTINEL_OK`, **STOP and report** — you are on the
  wrong base and every excerpt in this plan will mismatch.
- Commit per step; message style is conventional-commit-ish, lowercase, e.g.
  `feat(ui): add header account chip` / `refactor(sidebar): drop settings nav entry`.
- Do **not** push, tag, or open a PR.

## Steps

### Step 1: Create the account chip component

Create `src/components/containers/account-button.tsx`. Target shape:

```tsx
import { useAuth } from "@/hooks/useAuth";
import { cn } from "@/lib/utils";
import { Settings } from "lucide-react";
import { Link, useLocation } from "react-router-dom";

/**
 * The account chip in the header's right corner: who you are signed in as, and
 * the way into Settings.
 *
 * It is a plain link rather than a dropdown because it has exactly one
 * destination — the theme picker and Logout deliberately stayed in the sidebar,
 * so a menu would cost a click to reach its only item.
 *
 * It must render unconditionally. `Settings` was removed from the sidebar nav
 * when this landed, which makes this chip the only route to that page; hiding
 * it behind any condition strands `/settings` at a URL nobody can click to.
 */
const AccountButton = () => {
  const { username } = useAuth();
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/settings");

  return (
    <Link
      to="/settings"
      className={cn(
        "btn btn-ghost gap-2 px-3 shrink-0 font-normal",
        isActive && "btn-active"
      )}
    >
      <Settings size={18} />
      {/* The visible label is the username, so the destination is only
          discoverable to a screen reader — and it disappears entirely at the
          `sm` breakpoint, leaving an unlabelled icon. This carries the name in
          both cases. */}
      <span className="sr-only">Settings</span>
      {username ? (
        <span className="hidden sm:inline max-w-[12rem] truncate">
          {username}
        </span>
      ) : null}
    </Link>
  );
};

export default AccountButton;
```

Notes on the details, so you do not "improve" them into bugs:

- `hidden sm:inline` on the username: below Tailwind's `sm` (640 px) the chip
  collapses to just the gear, because the mobile header already carries a
  hamburger and a page title and a long username would crowd both out.
- `max-w-[12rem] truncate`: a long username must ellipsize, never wrap or push
  the header wider.
- `shrink-0`: the header row is a flex container whose `<h1>` is `flex-1`. Without
  this the chip is the thing that gets squashed on a narrow viewport.
- `btn-active` for the active state mirrors how the sidebar marks the current
  page. Do not invent a different active treatment.

**Verify**: `npx pnpm@9 run typecheck` → exit 0, no output.

### Step 2: Render the chip in the header

In `src/components/layouts/main-layout.tsx`:

1. Add the import alongside the existing ones at the top:
   ```tsx
   import AccountButton from "../containers/account-button";
   ```
   (`Sidebar` is already imported as `"../containers/sidebar"` on line 4 — match
   that relative style, not the `@/` alias, in this file.)

2. In `Header`'s returned JSX, add `<AccountButton />` **immediately after**
   `{page?.actions}`, as the last child of the `flex flex-row items-center gap-4`
   div:

   ```tsx
        <h1 className="text-xl flex-1 truncate">
          {page?.title || "Dashboard"}
        </h1>

        {page?.actions}

        <AccountButton />
      </div>
   ```

Do not change the `<h1>`, the back/hamburger button, or the header's classes.
The existing `gap-4` already spaces the chip from `page?.actions`.

**Verify**: `npx pnpm@9 run typecheck` → exit 0. Then:
```
grep -n "AccountButton" src/components/layouts/main-layout.tsx
```
→ exactly **2** matches (the import and the JSX usage).

### Step 3: Remove Settings from the sidebar nav and drop the duplicate username

Two edits in `src/components/containers/sidebar.tsx`:

**3a.** Delete the `Settings` entry from the `pages` array, leaving four:

```tsx
const pages = [
  { icon: LayoutDashboard, title: "Dashboard", path: "/", exact: true },
  { icon: HardDrive, title: "Cluster", path: "/cluster" },
  { icon: ArchiveIcon, title: "Buckets", path: "/buckets" },
  { icon: KeySquare, title: "Keys", path: "/keys" },
];
```

**3b.** In the bottom bar, delete the `<p>Signed in as …</p>` line and collapse
the wrapper `<div>` that only existed to stack it above the button. The block
becomes:

```tsx
        {auth.isEnabled ? <LogoutButton /> : null}
```

replacing the whole `{auth.isEnabled ? (<div className="flex-1 flex flex-col …">…</div>) : null}`
expression. `LogoutButton` already carries `className="flex-1"`, so it still
fills the row beside the theme picker.

**3c.** `noUnusedLocals` is on, so you must now remove **two** imports that no
longer have a use:
- `Settings` from the `lucide-react` import list (was only used by the nav entry
  you deleted in 3a).
- Nothing else from `lucide-react` — `Palette` and `LogOut` are still used, and
  `ArchiveIcon` / `HardDrive` / `KeySquare` / `LayoutDashboard` are still used by
  the four remaining nav entries.

`auth` itself is still used (`auth.isEnabled` appears twice on the theme picker
and once on the logout line), so **keep** the `useAuth` import and the
`const auth = useAuth();` line.

**Verify**:
```
npx pnpm@9 run typecheck
```
→ exit 0, no output. (If it reports `'Settings' is declared but its value is
never read`, you skipped 3c.) Then:
```
grep -n "Signed in as\|path: \"/settings\"" src/components/containers/sidebar.tsx
```
→ **no matches**.

### Step 4: Write the tests

Create `src/components/containers/account-button.test.tsx`.

`AccountButton` calls `useAuth`, which calls TanStack Query's `useQuery` — so
rendering it raw would need a `QueryClientProvider`. Mock the hook instead; it
keeps the test about *this* component. It also needs a router, because it renders
a `Link` and reads `useLocation`.

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AccountButton from "./account-button";

// `vi.mock` is hoisted above the imports, so the mutable state it closes over
// has to be hoisted with it.
const mockAuth = vi.hoisted(() => ({
  username: undefined as string | undefined,
}));

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ username: mockAuth.username }),
}));

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <AccountButton />
    </MemoryRouter>
  );

describe("AccountButton", () => {
  beforeEach(() => {
    mockAuth.username = "abdulrahman";
  });

  it("links to /settings and shows the signed-in username", () => {
    renderAt("/");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute(
      "href",
      "/settings"
    );
    expect(screen.getByText("abdulrahman")).toBeInTheDocument();
  });

  it("still renders a working Settings link before the username loads", () => {
    // `useAuth().username` is `string | undefined` — undefined on first paint,
    // while /auth/status is in flight. Settings must stay reachable: this chip
    // is the only route to it.
    mockAuth.username = undefined;
    renderAt("/");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute(
      "href",
      "/settings"
    );
  });

  it("marks itself active on the settings route", () => {
    renderAt("/settings");

    expect(screen.getByRole("link", { name: /settings/i })).toHaveClass(
      "btn-active"
    );
  });

  it("is not active on an unrelated route", () => {
    renderAt("/buckets");

    expect(screen.getByRole("link", { name: /settings/i })).not.toHaveClass(
      "btn-active"
    );
  });
});
```

Two things to know before you debug a surprise:

- **jsdom does not apply Tailwind.** The `hidden sm:inline` class has no effect
  there, so the username span *is* in the accessibility tree during tests and the
  link's accessible name is `"Settings abdulrahman"`. That is why every query
  above uses the regex `/settings/i` rather than an exact string. Do not "fix"
  this by removing `sr-only` or by asserting an exact name.
- `@testing-library/jest-dom` matchers (`toHaveAttribute`, `toHaveClass`,
  `toBeInTheDocument`) are already registered globally by
  `src/test/setup.ts` — do not import them.

**Verify**:
```
npx pnpm@9 exec vitest run account-button
```
→ `Test Files 1 passed`, `Tests 4 passed`.

### Step 5: Prove the tests can fail

A test that cannot fail is not a test. Temporarily change the `to="/settings"`
in `account-button.tsx` to `to="/setting"`, re-run
`npx pnpm@9 exec vitest run account-button`, and confirm **2 tests fail** on the
`href` assertion. Then **revert that edit** and confirm all 4 pass again.

Report both numbers in your final summary. If the suite stays green with the
broken path, the tests are not wired to the component — STOP and report.

### Step 6: Full gates

```
npx pnpm@9 run typecheck
npx pnpm@9 run test
npx pnpm@9 run build
```

All three exit 0. The full test run must show the pre-existing suites still
passing — in particular `src/components/ui/menu.test.tsx` (7 tests), which is
untouched by this plan. If a menu test breaks, you changed something out of
scope — STOP and report.

## Test plan

- **New file**: `src/components/containers/account-button.test.tsx`, 4 tests —
  the happy path (link target + visible username), the `username === undefined`
  first-paint case, active on `/settings`, and not-active elsewhere.
- **Structural pattern**: model the mocking and provider setup on the file's own
  shape above. The nearest existing component test is
  `src/components/ui/menu.test.tsx` (`@testing-library/react` + `userEvent` +
  `describe`/`it` from vitest) — match its import style and its habit of
  explaining non-obvious jsdom behavior in a comment. It is *not* a template for
  the mocking, since `Menu` has no hooks to mock.
- **No test is needed for the sidebar removals** — they are deletions with no
  behavior of their own, and the grep in Step 3 is the check.
- **Verification**: `npx pnpm@9 run test` → every suite passes, including the 4
  new tests.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `npx pnpm@9 run typecheck` exits 0 with no output
- [ ] `npx pnpm@9 run test` exits 0; `account-button.test.tsx` contributes 4 passing tests
- [ ] `npx pnpm@9 run build` exits 0
- [ ] Step 5's mutation test failed 2 tests, and the edit was reverted
- [ ] `grep -rn "Signed in as" src/` → **no matches**
- [ ] `grep -n "path: \"/settings\"" src/components/containers/sidebar.tsx` → **no matches**
- [ ] `grep -c "MenuItem\|<Menu" src/components/containers/account-button.tsx` → **0** (the chip is a link, not a menu)
- [ ] `grep -n "LogoutButton\|Palette" src/components/containers/sidebar.tsx` → still matches; the theme picker and Logout survive
- [ ] `git diff --name-only main` lists **exactly** these four paths and nothing else:
      `src/components/containers/account-button.test.tsx`,
      `src/components/containers/account-button.tsx`,
      `src/components/containers/sidebar.tsx`,
      `src/components/layouts/main-layout.tsx`

## STOP conditions

Stop and report back (do not improvise) if:

- The sentinel in "Git workflow" does not print `SENTINEL_OK`, or the code at the
  locations in "Current state" does not match the excerpts — the branch is based
  on the wrong commit.
- **Removing `Settings` from the sidebar leaves `/settings` unreachable.** After
  Step 3, the header chip is the only link to that page. If for any reason the
  chip cannot render on some route, do not proceed with the nav removal — report
  instead. A settings page nobody can click to is worse than a fifth nav item.
- `page?.actions` and the new chip visually collide, or you find yourself wanting
  to change the `actions` contract in `src/context/page-context.tsx` to make them
  coexist. The two are independent siblings in a `gap-4` flex row; if that is not
  enough, report rather than redesigning the slot.
- You conclude the chip should be a dropdown after all (e.g. because it seems
  bare with one destination). That was decided against — report the reasoning,
  do not build it.
- Any menu test in `src/components/ui/menu.test.tsx` fails.
- A step's verification fails twice after a reasonable fix attempt.

## Maintenance notes

For whoever owns this next:

- **`/settings` now has exactly one entry point in the UI.** Any future change
  that conditionally hides `AccountButton` — a role check, a "hide on mobile", a
  loading guard — silently strands the Settings page. The
  `username === undefined` test exists specifically to pin this; if you find
  yourself deleting it, put the nav entry back first.
- **Screenshots are now stale.** `README.md` and `docs/` embed UI screenshots
  showing the five-item sidebar with `Settings` in it and the "Signed in as …"
  line at the bottom. Regenerating them was deliberately left out of this plan
  (it is a separate, larger chore covered by plan 026's tooling). The prose is
  unaffected: `docs/authentication.md:465`, `README.md:62/181/190`, and
  `docs/UPGRADING.md` all say "**Settings → Users**", which stays accurate — the
  Settings page and its tabs did not change, only the route you take to it.
- **What a reviewer should scrutinize**: that the theme picker and Logout are
  byte-for-byte unchanged in the sidebar diff (the temptation to "finish the job"
  and move them up too is the most likely scope creep here); that the chip is
  after `page?.actions` and not instead of it; and that no `BASE_PATH` prefixing
  crept into the `to=` prop.
- **Deferred on purpose**: no avatar, no initials bubble, no role badge on the
  chip. The maintainer scoped this to username + Settings; a role indicator
  (`admin` / `viewer`) in the header is a reasonable follow-up but was not asked
  for.
