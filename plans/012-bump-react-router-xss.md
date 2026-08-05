# Plan 012: Patch the react-router open-redirect XSS advisory

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. If any
> STOP condition occurs, stop and report. When done, update the status row for
> this plan in `plans/README.md` — unless a reviewer dispatched you and told you
> they maintain the index.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none (independent of plans 001–011; touches only dependency manifests)
- **Category**: migration / security
- **Planned at**: commit `b1ab4ab` (the `integration-check` tip with all 8 fix plans), 2026-07-30

## Why this matters

`pnpm audit` reports a HIGH advisory in `@remix-run/router` — "React Router
vulnerable to XSS via Open Redirects" (GHSA affecting `<=1.23.1`, patched in
`>=1.23.2`). This package is the routing engine under `react-router-dom`, so it
**ships in the browser bundle** — unlike the ~20 other audit findings, which are
all build-toolchain transitive deps that never reach a user's browser. The
installed version is `1.19.0`, pulled by `react-router-dom ^6.26.0`.

The fix is a minor version bump within react-router v6 — low risk, no API
migration (v6 → v6). After this, the advisory clears.

## Current state

`package.json:25` declares:

```json
    "react-router-dom": "^6.26.0",
```

and `pnpm why @remix-run/router` resolves it to `1.19.0` (vulnerable).

The app uses `createBrowserRouter` with static route objects
(`src/app/router.tsx`) — no data loaders or redirects with user-controlled URLs
were found, so exploitability is likely low, but the advisory is real and the
fix is cheap.

## Commands you will need

`pnpm` is not installed; use `npx pnpm@9 <cmd>`. `go` is not needed (this plan is
frontend-only).

| Purpose | Command | Expected |
|---|---|---|
| Install | `npx pnpm@9 install` | exit 0 |
| Check resolved router | `npx pnpm@9 why @remix-run/router` | shows `>=1.23.2` after the fix |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Tests | `npx pnpm@9 run test` | all pass (24 passed, 1 skipped on the integrated base) |
| Build | `npx pnpm@9 run build` | exit 0 |
| Re-audit | `npx pnpm@9 audit --prod` | no `@remix-run/router` advisory |

## Scope

**In scope**: `package.json`, `pnpm-lock.yaml` only.

**Out of scope**: any source file. This is a dependency bump; no code change
should be needed (v6 → v6 is API-compatible). If the build or tests fail in a way
that would require editing `src/`, that is a STOP condition — report it, do not
"fix" source to accommodate a version bump.

## Steps

### Step 0 — base your worktree on the integrated branch (FIRST)

Your worktree starts on pristine `ee420fb`, but this plan is stamped against the
integrated tree (which has plan 002's `package.json` / lockfile changes). Run:

```
git checkout -B advisor/012-bump-react-router integration-check
git log --oneline -1     # MUST show a merge commit around b1ab4ab, NOT ee420fb
```

If that fails or shows `ee420fb`, STOP and report. Do all work on this branch.

### Step 1 — bump react-router-dom

In `package.json`, change the `react-router-dom` dependency to `^6.30.1`:

```json
    "react-router-dom": "^6.30.1",
```

Then update the lockfile:

```
npx pnpm@9 install
```

### Step 2 — verify the transitive router is patched

```
npx pnpm@9 why @remix-run/router
```

The resolved version **must be `>=1.23.2`**. If `react-router-dom@^6.30.1` does
not pull a patched router, add a pnpm override instead — in `package.json`'s
existing `"pnpm"` block, add:

```json
  "pnpm": {
    "overrides": {
      "@remix-run/router": ">=1.23.2"
    },
    "onlyBuiltDependencies": [ ... keep existing ... ]
  }
```

then re-run `npx pnpm@9 install` and re-check. Forcing `>=1.23.2` is safe — it is
a patch release over `1.23.1`. Report in NOTES which approach worked.

### Step 3 — verify nothing broke

```
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
```

All must pass. The test total on the integrated base is **24 passed, 1 skipped**
— it must not drop. `pnpm run lint` remains red from the pre-existing backlog;
ignore it (this plan touches no lintable source).

### Step 4 — confirm the advisory cleared

```
npx pnpm@9 audit --prod
```

The `@remix-run/router` / "XSS via Open Redirects" advisory must no longer
appear. (Other build-toolchain advisories may remain — those are out of scope.)

## Done criteria

- [ ] `npx pnpm@9 why @remix-run/router` shows `>=1.23.2`
- [ ] `npx pnpm@9 run typecheck` exits 0
- [ ] `npx pnpm@9 run test` — 24 passed, 1 skipped (unchanged)
- [ ] `npx pnpm@9 run build` exits 0
- [ ] `npx pnpm@9 audit --prod` no longer lists `@remix-run/router`
- [ ] `git diff --name-only integration-check..HEAD` shows only `package.json` and `pnpm-lock.yaml`
- [ ] committed on `advisor/012-bump-react-router`; `plans/README.md` untouched (reviewer maintains it)

## STOP conditions

- Step 0's `git log` shows `ee420fb` (wrong base).
- The build or tests fail in a way that needs a `src/` change — react-router v6.30
  is API-compatible with 6.26, so this would indicate something unexpected;
  report it rather than editing source.
- `pnpm why` still shows `<1.23.2` even after the override — report the resolution
  tree.

## Maintenance notes

- This only patches the one runtime-reachable advisory. The build-toolchain
  advisories (`rollup`, `vite`, `postcss`, `glob`, etc.) are tracked in
  `plans/README.md` as low-signal; clear them with a routine `pnpm update` when
  convenient, separately.
- If a future change adopts react-router data routers (loaders/actions with
  redirects), revisit the open-redirect surface — that's the usage pattern the
  advisory concerns.
