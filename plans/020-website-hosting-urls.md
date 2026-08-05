# Plan 020: Correct & discoverable static website hosting (D5)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it). This is a **frontend-only** plan —
> do not touch any Go file.
>
> **Base reset FIRST**: `git checkout -B advisor/020-website-hosting-urls main`
> then `git log --oneline -1` — MUST show `00dd4e3` (a "docs: add CLAUDE.md…"
> commit) or newer, NOT `ee420fb`. Then confirm the base carries the prior
> roadmap work with this SENTINEL:
> `test -f backend/middleware/audit.go && grep -q "getBucketCredentials" backend/router/browse.go && echo BASE_OK`
> It MUST print `BASE_OK`. If it does not, STOP and report.

## Status

- **Priority**: P3 (feature polish / correctness)
- **Effort**: M
- **Risk**: LOW (frontend only; no API or data-shape changes)
- **Depends on**: nothing new. Builds on already-merged website config + object
  sharing (share-dialog). No backend changes — `/config` already exposes
  `s3_web` and `/buckets` already returns `websiteAccess` per bucket.
- **Category**: direction / feature (finding D5)
- **Planned at**: commit `00dd4e3`, 2026-08-03

## Why this matters

Garage can serve a bucket as a static website, and this UI already lets you
**configure** it (toggle + index/error documents, persisted via
`POST /v2/UpdateBucket`). What's broken is every place that shows the resulting
**public URL** — and that logic is duplicated and wrong in two components:

- A bare hostname `http://<bucket>` is emitted as a "website URL"
  (`overview-website-access.tsx` line 99, `share-dialog.tsx` line 50). A bare
  bucket name is not a resolvable host in a browser — that link never works.
- When Garage has no `[s3_web]` block, `root_domain` is empty/undefined, so
  `` `${bucketName}${rootDomain}` `` yields duplicates, or literally
  `mybucketundefined:80` while `/config` is still loading (`share-dialog.tsx`
  lines 37-44 build this with no guard).
- Scheme and port are hardcoded/naive (`http://` always, `:${port}` always).
- Nothing tells an operator **why** their site won't serve when Garage has no
  web endpoint configured.

And the feature is **undiscoverable**: whether a bucket is a website is buried
inside one section of one tab; the bucket list shows no indication, even though
it already has the data.

**This plan** (scope confirmed with the maintainer: "1 & 2"):

1. Extract **one shared, correct** website-URL helper and use it in both
   consumers; show a clear "configure `[s3_web]`" guidance state when web
   serving isn't set up.
2. Make website status **discoverable** with a badge on the bucket list card.

## Current state (read these before editing)

### `src/types/garage.ts` — config shape the helper consumes

```ts
export type Config = {
  // ...
  s3_web?: S3Web;
};
export type S3Web = {
  bind_addr: string;
  root_domain: string;
};
```

`useConfig()` (`src/hooks/useConfig.ts`) is a cached TanStack query on `/config`
returning `Config`. Multiple components may call it freely — it's one shared
`["config"]` query.

### `src/pages/buckets/manage/overview/overview-website-access.tsx` — buggy URLs (consumer #1)

Lines 24-25 derive the port/domain by hand; lines 97-126 render the URLs,
including an always-shown bare-hostname link:

```tsx
  const websitePort = config?.s3_web?.bind_addr?.split(":").pop() || "80";
  const rootDomain = config?.s3_web?.root_domain;
  // ...
          <div className="mt-4 alert flex flex-row flex-wrap text-sm gap-x-2 gap-y-1">
            <a
              href={`http://${bucketName}`}   {/* <-- bare host, never resolves */}
              className="inline-flex items-center flex-row gap-2 font-medium hover:link"
              target="_blank"
            >
              <LinkIcon size={14} />
              {bucketName}
            </a>
            {rootDomain ? (
              <>
                <a href={`http://${bucketName}${rootDomain}`} ... >{bucketName + rootDomain}</a>
                <a href={`http://${bucketName}${rootDomain}:${websitePort}`} ... >
                  {bucketName + rootDomain + ":" + websitePort}
                </a>
              </>
            ) : null}
          </div>
```

The component already imports `LinkIcon`, `Info` from `lucide-react`, `Button`,
and reads `config` via `useConfig()`; `canWrite` via `useAuth()`;
`bucket`/`bucketName` via `useBucketContext()`. The docs link button (lines
62-70) points at `https://garagehq.deuxfleurs.fr/documentation/cookbook/exposing-websites`
— reuse that URL for the guidance state.

### `src/pages/buckets/manage/browse/share-dialog.tsx` — buggy URLs (consumer #2)

Lines 26, 34-50 build the public URL with the same broken logic and an
unguarded `undefined` concatenation; lines 115-137 render a domain-picker + URL:

```tsx
  const [domain, setDomain] = useState(bucketName);
  // ...
  const websitePort = config?.s3_web?.bind_addr?.split(":").pop() || "80";
  const rootDomain = config?.s3_web?.root_domain;
  const domains = useMemo(
    () => [
      bucketName,
      bucketName + rootDomain,                    // "<name>undefined" when unset
      bucketName + rootDomain + `:${websitePort}`,
    ],
    [bucketName, config?.s3_web]
  );
  // ...
  const url = "http://" + domain + "/" + data?.prefix + data?.key;
```

The **private presigned-link** section (lines 67-107, gated on `config?.sharing`)
is from a different feature — **do not touch it**. Only the public-website-URL
part (the `domain`/`domains`/`url` state and the JSX at lines 115-137) is in
scope. `objectKey` is already computed at line 51:
`const objectKey = (data?.prefix ?? "") + (data?.key ?? "");`.

### `src/pages/buckets/components/bucket-card.tsx` — discoverability target

Renders one card per bucket. `data` is `Bucket & { aliases: string[] }`, so
`data.websiteAccess` (boolean) and `data.globalAliases` are available. It does
**not** currently call `useConfig`. Title block is lines 13-20.

### `src/pages/buckets/manage/page.tsx` — Alert exemplar for the guidance state

Lines 68-80 show the repo's convention for an inline warning (copy this shape
for the "web endpoint not configured" guidance):

```tsx
<Alert status="warning" icon={<CircleXIcon />} className="mb-4 items-start text-sm">
  <span>This bucket has no global alias. …</span>
</Alert>
```

`Alert` and the icons come from `react-daisyui` / `lucide-react` respectively.

### Garage website-URL scheme (the correct format)

Garage serves `http://<bucket-global-alias><root_domain>[:<web-port>]/`. By
Garage convention `root_domain` in `[s3_web]` **starts with a dot** (e.g.
`.web.example.com`), so `<bucket><root_domain>` = `mybucket.web.example.com`.
Web serving is plain HTTP on the web bind port; HTTPS requires an external
reverse proxy (there is no config signal for it), so we always emit `http://`.

## Commands

`pnpm` is not installed → use `npx pnpm@9 <cmd>`. In a fresh worktree run
`npx pnpm@9 install` once first (node_modules is gitignored/absent); it will not
modify `pnpm-lock.yaml`.

| Purpose | Command | Expected |
|---|---|---|
| Typecheck | `npx pnpm@9 run typecheck` | exit 0 |
| Unit tests | `npx pnpm@9 exec vitest run src/lib/website.test.ts` | pass |
| Full test suite | `npx pnpm@9 run test` | pass (no regressions) |
| Build | `npx pnpm@9 run build` | exit 0 |
| Lint (new files only) | `npx pnpm@9 exec eslint src/lib/website.ts src/pages/buckets/components/bucket-card.tsx` | 0 errors in these files |

Note: `npx pnpm@9 run lint` over the whole repo is **expected to be red** (large
pre-existing backlog). Only ensure the files you create/edit add no new errors.

## Scope

**In scope** (frontend only):
- `src/lib/website.ts` (create — the shared helper)
- `src/lib/website.test.ts` (create — unit tests)
- `src/pages/buckets/manage/overview/overview-website-access.tsx` (use helper + guidance)
- `src/pages/buckets/manage/browse/share-dialog.tsx` (use helper for the public URL)
- `src/pages/buckets/components/bucket-card.tsx` (website badge)

**Out of scope** — do NOT touch:
- Any Go / backend file (`/config` already exposes `s3_web`; `/buckets` already
  returns `websiteAccess`). If you think a backend change is needed, STOP and report.
- The **private presigned-link** section of `share-dialog.tsx` (lines 67-107,
  the `config?.sharing` block) — that's feature 009, leave it exactly as is.
- The website **config** form (toggle, index/error document inputs) in
  `overview-website-access.tsx` — only replace the URL-display part.
- `plans/`, `CLAUDE.md`, backend tests.
- Adding HTTPS detection, custom-domain support, or a bucket policy / anonymous-
  read toggle — out of scope (Garage's website access already governs public
  serving). If tempted, STOP.

## Steps

### Step 1 — The shared helper

Create `src/lib/website.ts`:

```ts
import type { Config } from "@/types/garage";

/**
 * Garage serves a bucket as a static website at
 *   http://<bucket-global-alias><root_domain>[:<web-port>]/
 * where <root_domain> comes from the garage.toml [s3_web] block and, by Garage
 * convention, begins with a dot (e.g. ".web.example.com"). Website serving is
 * plain HTTP on the web bind port; HTTPS needs an external reverse proxy, so we
 * always emit http:// here.
 */

/** True when Garage has a web endpoint configured (an [s3_web] root_domain). */
export function isWebsiteHostingConfigured(config?: Config): boolean {
  return !!config?.s3_web?.root_domain?.trim();
}

/**
 * Public base URL at which `bucketName` is served as a website, or null when the
 * bucket name is empty or Garage has no [s3_web] root_domain configured (no
 * working public URL exists then — callers should show guidance instead).
 */
export function getBucketWebsiteBaseUrl(
  bucketName: string,
  config?: Config
): string | null {
  const name = bucketName?.trim();
  const rawRoot = config?.s3_web?.root_domain?.trim();
  if (!name || !rawRoot) return null;

  // Garage convention: root_domain starts with a dot. Normalise so an operator
  // who omitted it still gets a valid host.
  const root = rawRoot.startsWith(".") ? rawRoot : `.${rawRoot}`;

  // Web bind_addr looks like "[::]:3902" or "0.0.0.0:3902"; take the trailing port.
  const port = config?.s3_web?.bind_addr?.split(":").pop()?.trim();
  const portSuffix = port && port !== "80" && port !== "443" ? `:${port}` : "";

  return `http://${name}${root}${portSuffix}`;
}

/** Public URL of a single object served via the bucket's website, or null. */
export function getBucketWebsiteObjectUrl(
  bucketName: string,
  objectKey: string,
  config?: Config
): string | null {
  const base = getBucketWebsiteBaseUrl(bucketName, config);
  if (base == null) return null;
  const key = (objectKey ?? "").replace(/^\/+/, "");
  return `${base}/${key}`;
}
```

**Verify**: `npx pnpm@9 run typecheck` → exit 0.

### Step 2 — Unit tests for the helper

Create `src/lib/website.test.ts` (Vitest; the repo uses jsdom + vitest). Follow
the style of the sibling **`src/lib/utils.test.ts`** — same directory, same
pure-function shape (`import { describe, it, expect } from "vitest";`). Cover:

- `isWebsiteHostingConfigured`: `undefined` → false; `{ s3_web: { root_domain: "" }}`
  → false; whitespace-only root_domain → false; `".web.ex.com"` → true.
- `getBucketWebsiteBaseUrl`:
  - no config / empty root_domain → `null`
  - empty `bucketName` → `null`
  - root `".web.ex.com"`, bind `"[::]:3902"` → `"http://mybucket.web.ex.com:3902"`
  - root **without** leading dot `"web.ex.com"` → normalised
    `"http://mybucket.web.ex.com:3902"`
  - port `80` (bind `"[::]:80"`) → no port suffix → `"http://mybucket.web.ex.com"`
  - no `bind_addr` → no port suffix
- `getBucketWebsiteObjectUrl`:
  - unconfigured → `null`
  - key `"index.html"` → `<base>/index.html`
  - key `"/leading"` → leading slash stripped → `<base>/leading`

**Verify**: `npx pnpm@9 exec vitest run src/lib/website.test.ts` → all pass.

### Step 3 — Fix consumer #1: `overview-website-access.tsx`

- Delete the local `websitePort` and `rootDomain` derivations (lines 24-25).
- Import at top: `import { getBucketWebsiteBaseUrl, isWebsiteHostingConfigured } from "@/lib/website";`
  and add `Alert` to the existing `react-daisyui` imports if not present (the file
  may not import it yet — check).
- Inside the `{isEnabled && ( … )}` block, replace the URL `<div className="mt-4 alert …">`
  (lines 97-126) with:
  - `const websiteUrl = getBucketWebsiteBaseUrl(bucketName, config);` (compute near
    the other hooks, not inside JSX).
  - When `websiteUrl` is non-null: render a single clickable link (keep the
    `LinkIcon` + `hover:link` styling already used) to `websiteUrl`, `target="_blank"`,
    showing `websiteUrl` as its text. Optionally a copy button using the existing
    `copyToClipboard` from `@/lib/utils` (already used elsewhere) — nice to have.
  - When `websiteUrl` is null (enabled but Garage has no `[s3_web]`): render a
    warning `Alert` (copy the shape from `page.tsx` lines 68-80) reading roughly:
    "Garage has no web endpoint configured, so this bucket has no public website
    URL. Add an `[s3_web]` block with `bind_addr` and `root_domain` to
    `garage.toml`." Keep the existing docs-link Button (lines 62-70) as the "learn
    more" affordance.
- Do **not** change the toggle or the index/error document inputs.

**Verify**: `npx pnpm@9 run typecheck` → exit 0; `grep -c "http://\${bucketName}" src/pages/buckets/manage/overview/overview-website-access.tsx` → `0` (the bare-host link is gone).

### Step 4 — Fix consumer #2: `share-dialog.tsx` (public URL only)

- Remove the `domain`/`setDomain` state (line 26), the `websitePort`/`rootDomain`
  (lines 34-35), the `domains` `useMemo` (lines 37-44), the `useEffect` that resets
  `domain` (lines 46-48), and the `url` (line 50).
- Import `getBucketWebsiteObjectUrl` from `@/lib/website`.
- Compute `const websiteUrl = getBucketWebsiteObjectUrl(bucketName, objectKey, config);`
  (after `objectKey` at line 51).
- Replace the domain-picker + URL JSX (lines 115-137) with: when `websiteUrl` is
  non-null, a single read-only `Input` showing `websiteUrl` + the existing copy
  `Button` (keep the `Copy` icon pattern). When null, render nothing there.
- Keep the `{!bucket.websiteAccess && (<Alert>…</Alert>)}` notice (lines 109-114)
  as-is — it already explains the public URL needs website access.
- **Do not touch** the private presigned-link block (lines 67-107).

**Verify**: `npx pnpm@9 run typecheck` → exit 0; `grep -c "bucketName + rootDomain" src/pages/buckets/manage/browse/share-dialog.tsx` → `0`.

### Step 5 — Discoverability: website badge on the bucket card

In `src/pages/buckets/components/bucket-card.tsx`:
- Import `Globe` from `lucide-react`, `useConfig` from `@/hooks/useConfig`, and
  `getBucketWebsiteBaseUrl` from `@/lib/website`.
- `const { data: config } = useConfig();` inside the component.
- When `data.websiteAccess` is true, render a small badge near the title (line
  13-20 block): a `Globe` icon (size 16) + the text `Website`, using a subdued
  daisyUI style (e.g. `className="badge badge-ghost gap-1"` or a small inline
  flex row — match the card's existing muted `text-sm` look).
- If `getBucketWebsiteBaseUrl(data.globalAliases?.[0] ?? "", config)` is non-null,
  wrap the badge in an `<a href={url} target="_blank" rel="noreferrer">` so it
  links to the live site; otherwise render it as plain (non-link) text. Use
  `data.globalAliases?.[0]`, **not** `data.aliases[0]` (which may be the
  `"(no alias)"` placeholder).
- Do not change the Usage/Objects/Manage/Browse layout.

**Verify**: `npx pnpm@9 run typecheck` → exit 0; `npx pnpm@9 exec eslint src/pages/buckets/components/bucket-card.tsx` → no new errors.

### Step 6 — Full gate sweep

```
npx pnpm@9 run typecheck
npx pnpm@9 exec vitest run src/lib/website.test.ts
npx pnpm@9 run test
npx pnpm@9 run build
```
All exit 0 / pass. Then commit on branch `advisor/020-website-hosting-urls`:
`feat: correct and discoverable static website URLs`

## Test plan

- **Unit (authoritative here — the helper is pure logic)**: `website.test.ts`
  covers configured/unconfigured, leading-dot normalisation, port 80 omission,
  object-key slash handling. This is where correctness is proven.
- **Type/build**: the three component edits must typecheck and build.
- **Reviewer live verification** (advisor's job, not the executor's): run the UI
  against a Garage whose `garage.toml` has **no** `[s3_web]` block and confirm the
  overview shows the **guidance Alert** (not a broken/`undefined` URL) and the
  share dialog shows **no** public URL; then against a Garage **with** `[s3_web]`
  confirm the overview + share dialog show the correct
  `http://<bucket><root_domain>[:port]` URL and the bucket card shows the Website
  badge linking to it.

## Done criteria

- [ ] `npx pnpm@9 run typecheck` exits 0
- [ ] `npx pnpm@9 exec vitest run src/lib/website.test.ts` all pass
- [ ] `npx pnpm@9 run test` passes (no regressions)
- [ ] `npx pnpm@9 run build` exits 0
- [ ] `grep -rn "http://\${bucketName}\`" src/` returns nothing (bare-host link removed)
- [ ] `grep -rn "bucketName + rootDomain" src/` returns nothing (buggy concat removed)
- [ ] `git diff --name-only 00dd4e3..HEAD` shows exactly the 5 in-scope files
- [ ] No Go files changed: `git diff --name-only 00dd4e3..HEAD | grep -c "\.go$"` → `0`

## STOP conditions

- Base reset shows `ee420fb` or the SENTINEL doesn't print `BASE_OK`.
- A current-state excerpt doesn't match the live file (report the drift; don't
  guess).
- You conclude a backend change is required (it isn't — `/config` exposes
  `s3_web`, `/buckets` returns `websiteAccess`). STOP and report what's missing.
- The private presigned-link block in `share-dialog.tsx` would need changing to
  make this work — it shouldn't; STOP.

## Maintenance notes

- **One source of truth**: after this, every website URL comes from
  `src/lib/website.ts`. Any future website surface (e.g. a dedicated hosting page)
  must use these helpers, never re-derive the URL.
- **HTTPS**: intentionally always `http://` — Garage's web endpoint is plain HTTP
  and there is no config signal for TLS termination. If a `web_scheme`/TLS field is
  ever added to `/config`, thread it through `getBucketWebsiteBaseUrl` only.
- **root_domain leading dot**: the helper normalises a missing leading dot, but
  Garage's documented convention includes it. If Garage changes the convention,
  the normalisation is the single place to adjust.
- **Badge data**: the bucket list already carries `websiteAccess` (from
  `GetBucketInfo` enrichment in `backend/router/buckets.go`); the badge needs no
  new endpoint. If that enrichment is ever removed for performance, the badge goes
  dark — keep them together.
