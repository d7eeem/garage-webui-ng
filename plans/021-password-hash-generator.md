# Plan 021: In-app client-side bcrypt password hash generator

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it). This is a **frontend-only** plan —
> do not touch any Go file.
>
> **Base reset FIRST**: `git checkout -B advisor/021-password-hash-generator main`
> then `git log --oneline -1` — MUST show `b6101af` or newer, NOT `ee420fb`.
> Then confirm the base with this SENTINEL:
> `test -f src/lib/website.ts && grep -q "garage-webui-ng" package.json && echo BASE_OK`
> It MUST print `BASE_OK`. If not, STOP and report.

## Status

> **SUPERSEDED by plans 022–026.** Users are now created in the app (first-run
> wizard + Settings → Users), so no one needs to generate a bcrypt hash by hand.
> Kept for history; do not execute.

- **Priority**: P3 (feature / DX)
- **Effort**: M
- **Risk**: LOW (frontend only; adds one pure-JS dependency; no API, auth, or data changes)
- **Depends on**: nothing.
- **Category**: feature / documentation
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

Enabling authentication requires a bcrypt hash in `AUTH_USER_PASS`. Today the only
documented way to produce one is a server-side shell pipeline:

```bash
htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n' | sed 's/^$2y/$2a/'
```

That needs Apache's `htpasswd` (or Docker) installed and a terminal — a poor
first-run experience, and it pushes users toward pasting passwords into random
online "bcrypt generator" sites. **This plan adds a built-in, fully offline,
browser-based bcrypt hash generator** so the default flow never needs a terminal
or an external website. All hashing runs locally via `bcryptjs` (pure JS, bundled
into the app); the password is never stored, logged, transmitted, or persisted.

**Compatibility note (important):** the backend verifies hashes with Go's
`golang.org/x/crypto/bcrypt`, which accepts `$2a$`, `$2b$`, and `$2y$` prefixes.
`bcryptjs` emits `$2a$`/`$2b$` by default, so **no `$2y$ → $2a$` conversion is
needed** (unlike the `htpasswd` pipeline).

## Current state (read before editing)

### `src/app/router.tsx` — routes (lazy, under `MainLayout`)

```tsx
const KeysPage = lazy(() => import("@/pages/keys/page"));
// ...
    {
      path: "/",
      Component: MainLayout,
      children: [
        { index: true, Component: HomePage },
        { path: "cluster", Component: ClusterPage },
        { path: "buckets", children: [ /* ... */ ] },
        { path: "keys", Component: KeysPage },
      ],
    },
```

Add a lazy `PasswordHashPage` and a `{ path: "password-hash", Component: PasswordHashPage }` child under `MainLayout`.

### `src/components/containers/sidebar.tsx` — nav (`pages` array, top of file)

```tsx
import { ArchiveIcon, HardDrive, KeySquare, LayoutDashboard, LogOut, Palette } from "lucide-react";
// ...
const pages = [
  { icon: LayoutDashboard, title: "Dashboard", path: "/", exact: true },
  { icon: HardDrive, title: "Cluster", path: "/cluster" },
  { icon: ArchiveIcon, title: "Buckets", path: "/buckets" },
  { icon: KeySquare, title: "Keys", path: "/keys" },
];
```

Add one entry, e.g. `{ icon: KeyRound, title: "Hash Generator", path: "/password-hash" }` (import `KeyRound` from `lucide-react`).

### UI primitives (local wrappers over react-daisyui)

- `src/components/ui/input.tsx` → `import Input from "@/components/ui/input"` (plain `<Input />`, forwards to daisyUI Input).
- `src/components/ui/button.tsx` → `import Button from "@/components/ui/button"`.
- `src/components/ui/toggle.tsx` → `import Toggle from "@/components/ui/toggle"` (has a `label` prop).
- Page header: `import Page from "@/context/page-context"` then `<Page title="Hash Generator" />` (see `src/pages/keys/page.tsx:62`).

Use **plain React `useState`** here (not react-hook-form) — it's a one-field tool.

### Clipboard + notifications (already available)

- `import { copyToClipboard } from "@/lib/utils"` — copies text and shows a `toast.success("Copied to clipboard")`.
- `import { toast } from "sonner"` — `toast.success(...)`, `toast.error(...)`. The app already renders the `<Toaster />`.

### `package.json` — `bcryptjs` is NOT a dependency yet

Add it (Step 1). Recent `bcryptjs` (v3.x) ships its own TypeScript types, so no
`@types/bcryptjs` is needed; if `tsc` complains about missing types, add
`@types/bcryptjs` as a devDependency instead.

### `README.md` — the section to replace (FAQ, ~line 217)

```md
**How do I generate a bcrypt hash for `AUTH_USER_PASS`?**
​```bash
htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n' | sed 's/^$2y/$2a/'
​```
```

## Commands

`pnpm` is not installed → use `npx pnpm@9 <cmd>`. In a fresh worktree run
`npx pnpm@9 install` once first.

| Purpose | Command | Expected |
|---|---|---|
| Add dep | `npx pnpm@9 add bcryptjs` | updates package.json + lockfile |
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Unit test | `npx pnpm@9 exec vitest run src/lib/bcrypt.test.ts` | pass |
| Full test | `npx pnpm@9 run test` | pass |
| Build | `npx pnpm@9 run build` | exit 0 |
| Lint (new files) | `npx pnpm@9 exec eslint src/lib/bcrypt.ts src/pages/password-hash/page.tsx` | 0 errors |

## Scope

**In scope** (frontend only):
- `package.json` / `pnpm-lock.yaml` (add `bcryptjs`)
- `src/lib/bcrypt.ts` (create — the pure hashing helper)
- `src/lib/bcrypt.test.ts` (create — unit tests)
- `src/pages/password-hash/page.tsx` (create — the generator page)
- `src/app/router.tsx` (add the route)
- `src/components/containers/sidebar.tsx` (add the nav entry)
- `README.md` (make the in-app generator the primary flow; keep `htpasswd` as an alternative)

**Out of scope** — do NOT touch:
- Any Go / backend file, the auth middleware, or `AUTH_USER_PASS` parsing.
- The login flow. (The page lives under `MainLayout`; that's sufficient — see Maintenance.)
- Persisting anything (no localStorage, no zustand, no query cache for the password/hash).

## Steps

### Step 1 — Add the dependency

```
npx pnpm@9 add bcryptjs
```
**Verify**: `grep '"bcryptjs"' package.json` returns a line; `npx pnpm@9 run typecheck` still exits 0 (add `@types/bcryptjs` as a devDependency only if typecheck reports missing types).

### Step 2 — The pure hashing helper

Create `src/lib/bcrypt.ts`:

```ts
import bcrypt from "bcryptjs";

/** Cost factors offered in the UI. 10 matches the historical htpasswd default. */
export const BCRYPT_COSTS = [8, 10, 12] as const;
export const DEFAULT_BCRYPT_COST = 10;

/**
 * Generate a bcrypt hash entirely in-process (no network). The output uses the
 * `$2a$`/`$2b$` prefix, which Go's golang.org/x/crypto/bcrypt accepts directly —
 * so it drops straight into AUTH_USER_PASS with no conversion.
 */
export function generateBcryptHash(password: string, cost = DEFAULT_BCRYPT_COST): string {
  const salt = bcrypt.genSaltSync(cost);
  return bcrypt.hashSync(password, salt);
}

/** Verify a password against a hash (used by tests; handy for round-trip checks). */
export function verifyBcryptHash(password: string, hash: string): boolean {
  return bcrypt.compareSync(password, hash);
}
```

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 3 — Unit tests

Create `src/lib/bcrypt.test.ts` (follow `src/lib/website.test.ts` for style —
`import { describe, it, expect } from "vitest";`). Cover:

- `generateBcryptHash("hunter2")` returns a string starting with `$2a$` or `$2b$`
  and containing the default cost segment `$10$`.
- The hash **round-trips**: `verifyBcryptHash("hunter2", generateBcryptHash("hunter2"))` is `true`, and a wrong password is `false`.
- A non-default cost is honoured: `generateBcryptHash("x", 12)` contains `$12$`.
- Two calls with the same password produce **different** hashes (random salt).

**Verify**: `npx pnpm@9 exec vitest run src/lib/bcrypt.test.ts` → all pass.

### Step 4 — The generator page

Create `src/pages/password-hash/page.tsx` — a default-exported component using
plain `useState`. Requirements (all from the feature spec):

- `<Page title="Hash Generator" />` header.
- **Password input** with a **show/hide toggle** (an eye button that flips
  `type` between `password`/`text`; use `Eye`/`EyeOff` from `lucide-react`).
- **Cost factor selector** — a `<select>` (or daisyUI select) over `BCRYPT_COSTS`,
  default `DEFAULT_BCRYPT_COST`; note "higher = slower & stronger".
- **"Generate Hash" button** — on click: if password is empty, `toast.error("Enter a password first")` and return; otherwise compute `generateBcryptHash(password, cost)`, store it in state, and `toast.success("Hash generated")`. Wrap the call so the button shows a brief pending state (cost 12 can take ~250 ms); e.g. set a `generating` flag, compute in a `setTimeout(…, 0)` or `await`ed microtask, then clear it.
- **Read-only output field** showing the hash (empty until generated).
- **One-click "Copy" button** — `copyToClipboard(hash)` (it toasts on success); disabled when there's no hash.
- **AUTH_USER_PASS hint** — below the output, show the ready-to-paste form, e.g.
  `admin:<hash>`. Make three things explicit so users know "what next":
  1. It goes into the **`AUTH_USER_PASS` environment variable** as `username:hash`
     — e.g. Docker `-e AUTH_USER_PASS='admin:<hash>'`, a Compose `environment:`
     entry, or a `.env` file next to the backend.
  2. It is **not** a `garage.toml` setting (that file is Garage's own config).
  3. The backend reads it **at startup**, so **restart** the app after setting it.
  The generator does not apply the hash itself — the app is stateless and takes
  auth from the environment; this tool only produces the value to place there.
- **`$`-escaping warning (required)** — a bcrypt hash contains `$` characters. In
  **docker-compose** `environment:` values (and some shells/`.env` uses) `$` is
  interpreted as variable interpolation and will mangle the hash. Show a short
  warning: "Using Docker Compose? Double every `$` to `$$` (or single-quote the
  value) so it isn't treated as a variable." Optionally offer a second read-only
  field with the compose-safe (`$$`) form + its own copy button.
- **72-byte note** — bcrypt only hashes the first **72 bytes** of the password;
  note this near the input so users aren't surprised that longer passwords are
  truncated.
- **Security messaging** — a visible note/alert: "All hashing happens locally in
  your browser. Your password is never stored, logged, or sent anywhere."
- **Render safely (required)** — display the password and hash **only** as escaped
  text or as an input `value`. Do **not** use `dangerouslySetInnerHTML`,
  `innerHTML`, or place either value in an `href`/`src`/URL. React escapes
  `{value}` and `value={…}` by default — rely on that; introduce no unsafe sink.
- **Never persist** — keep everything in component state; do not write to
  localStorage/store; do not `console.log` the password or hash.
- Style with the existing daisyUI classes so it matches the app (cards, `label`,
  `input`, `btn`); keep it responsive (single column on mobile). Match the look of
  an existing simple page (e.g. `src/pages/keys/page.tsx`).

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 5 — Route

In `src/app/router.tsx`, add the lazy import and the child route under `MainLayout`:

```tsx
const PasswordHashPage = lazy(() => import("@/pages/password-hash/page"));
// ...inside the "/" (MainLayout) children:
{ path: "password-hash", Component: PasswordHashPage },
```

**Verify**: `npx pnpm@9 run build` → exit 0; a `password-hash` chunk is emitted.

### Step 6 — Navigation entry

In `src/components/containers/sidebar.tsx`, import `KeyRound` from `lucide-react`
and append to `pages`:

```tsx
{ icon: KeyRound, title: "Hash Generator", path: "/password-hash" },
```

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 7 — README

Replace the FAQ answer (and reinforce in the Security/Configuration sections) so
the **in-app generator is primary** and `htpasswd` is a labelled fallback:

```md
**How do I generate a bcrypt hash for `AUTH_USER_PASS`?**

Use the built-in **Hash Generator** (sidebar → *Hash Generator*) — it runs fully
offline in your browser, no terminal or external site required:

1. Open **Hash Generator**.
2. Enter your password.
3. Click **Generate Hash**.
4. Copy the generated bcrypt hash.
5. Set the **`AUTH_USER_PASS` environment variable** to `username:hash` (Docker
   `-e`, a Compose `environment:` entry, or a `.env` file) — this is an env var,
   **not** a `garage.toml` setting.
6. **Restart** the app so it picks up the new value.

> In Docker Compose, double each `$` in the hash to `$$` (or single-quote the
> value) so it isn't treated as variable interpolation.

<details><summary>Alternative (CLI, for advanced users)</summary>

​```bash
htpasswd -bnBC 10 "" 'your-password' | tr -d ':\n' | sed 's/^$2y/$2a/'
​```
</details>
```

Also update the **Security** section's authentication bullet and the
`AUTH_USER_PASS` note in **Configuration** to mention the built-in generator.

**Verify**: `grep -ci "Hash Generator" README.md` ≥ 2.

### Step 8 — Screenshot (documentation)

The new page appears in the UI, so a screenshot should be added to
`docs/screenshots/` (e.g. `password-hash-light.png`) and referenced in the README
screenshot gallery. **This is captured at review time** against a running build
(the advisor/reviewer runs the app and captures it, consistent with the existing
screenshot set) — the executor does not need to produce the PNG, but should add
the README gallery reference with the agreed filename `password-hash-light.png`.

### Step 9 — Full gate sweep

```
npx pnpm@9 run typecheck
npx pnpm@9 exec vitest run src/lib/bcrypt.test.ts
npx pnpm@9 run test
npx pnpm@9 run build
```
All exit 0 / pass. Commit on branch `advisor/021-password-hash-generator`:
`feat: in-app offline bcrypt password hash generator`

## Test plan

- **Unit (`bcrypt.test.ts`)** is the core: prefix, cost segment, salt randomness,
  and password round-trip (`verifyBcryptHash`) — this proves the output is a valid
  bcrypt hash of the input at the chosen cost.
- **Type/build**: page + route + nav must typecheck and build in dev and prod.
- **Reviewer live verification** (advisor's job): run the app, open **Hash
  Generator**, generate a hash for a known password, and confirm the **backend
  accepts it** — set `AUTH_USER_PASS=testuser:<generated-hash>`, restart the
  backend, and log in with that password (should succeed; a wrong password should
  401). This closes the loop that a browser-generated hash is Go-bcrypt valid.

## Done criteria

- [ ] `bcryptjs` is in `package.json`; `pnpm-lock.yaml` updated.
- [ ] `npx pnpm@9 run typecheck` exits 0.
- [ ] `npx pnpm@9 exec vitest run src/lib/bcrypt.test.ts` all pass.
- [ ] `npx pnpm@9 run test` passes (no regressions); `npx pnpm@9 run build` exits 0.
- [ ] `grep -rn "bcryptjs" src/lib/bcrypt.ts` and the `password-hash` route both present.
- [ ] `grep -ci "Hash Generator" README.md` ≥ 2 and the `htpasswd` block is under an "Alternative" `<details>`.
- [ ] `git diff --name-only b6101af..HEAD` shows only the in-scope files; zero `.go` files.
- [ ] `grep -rn "dangerouslySetInnerHTML\|innerHTML" src/pages/password-hash/` returns nothing (no unsafe render sink); the `$$`-escaping warning is present in the page.

## STOP conditions

- Base reset shows `ee420fb` or the SENTINEL doesn't print `BASE_OK`.
- A current-state excerpt doesn't match the live file (report the drift).
- `bcryptjs` pulls native/binary build steps or fails to bundle in `vite build`
  (it should be pure JS) — STOP and report rather than swapping libraries.
- You find yourself adding any network call, server endpoint, or persistence for
  the password/hash — that violates the security requirement; STOP.

## Security considerations (threat model)

- **No injection into the app/backend.** Hashing is client-side `bcryptjs`; there
  is no SQL, shell, `eval`, template, or server sink in the path, and the backend
  only ever runs `bcrypt.CompareHashAndPassword` on the stored hash. The password
  never reaches the server.
- **XSS is prevented by correct rendering, not by luck.** React escapes text and
  input `value`s. The *only* way to introduce XSS is an unsafe sink — hence the
  hard rule above: no `dangerouslySetInnerHTML`/`innerHTML`/URL sinks for the
  password or hash. A reviewer should grep the new page for those and reject any.
- **Downstream config interpolation is the real footgun.** The `$` in bcrypt
  hashes is interpreted by docker-compose / shell variable expansion, silently
  corrupting `AUTH_USER_PASS`. The UI must warn and offer the `$$`-escaped form
  (see Step 4). This is a correctness/security issue (a corrupted hash can fail
  open to "auth misconfigured"), not cosmetic.
- **bcrypt 72-byte truncation** is a property of the algorithm, surfaced as a note
  so users don't assume a very long passphrase is fully used.
- **No persistence / no logging** of the password or hash — enforced in code
  (component state only) and checked at review.

## Maintenance notes

- **Pure client-side, by design** — hashing is `bcryptjs` in the browser bundle;
  there is no server involvement, so it works identically in `pnpm run dev` and in
  the embedded prod binary. Never route the password through the backend.
- **Go compatibility** — `bcryptjs` `$2a$`/`$2b$` output is accepted by
  `golang.org/x/crypto/bcrypt`; no `$2y$` rewrite is required. If the backend ever
  switches hashing schemes, revisit `src/lib/bcrypt.ts`.
- **Auth chicken-and-egg** — the page sits under `MainLayout`, reachable when auth
  is unset (first-run setup, app is open) or when an admin is logged in (adding a
  user). If you later want it reachable from the login screen too, add a link on
  `src/pages/auth/login.tsx`; not required here.
- **No persistence** — keep the password/hash in component state only. Do not add
  it to the zustand `appdata` store or localStorage.
