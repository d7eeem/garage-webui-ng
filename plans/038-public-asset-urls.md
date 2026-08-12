# Plan 038: Permanent public asset URLs — make the website endpoint a first-class sharing path

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer
> who dispatched you maintains the index.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to
> base on:
> ```
> git diff --stat <BASE> -- src/lib/website.ts src/pages/buckets/manage/browse/ \
>   src/pages/buckets/manage/overview/overview-website-access.tsx backend/router/browse.go
> ```
> Then confirm every excerpt in "Current state" matches the live code. On a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**:
  - **036** (`S3_WEB_PUBLIC_URL` + `config.s3_web.public_url`) — **hard**. This
    plan consumes that config value and does not re-invent it.
  - **037** (`useUpdateBucket` failure reporting, `classifyObjectProbe` /
    `useObjectExists`) — **soft**. 038 reuses 037's presence-probe pattern for
    the "Test" affordance in Phase 5; if 037 has not landed, do Phases 1–4 and
    stop.
- **Category**: direction (feature) + bug
- **Planned at**: advisor stack tip `6bdcb25`, 2026-08-10

---

## 0. Read this before anything else — three findings that reshape the request

The brief asked for investigation before planning. That investigation changed
three of its premises. Do not implement the brief as literally written; implement
this plan.

### 0.1 Garage has exactly ONE anonymous-read mechanism, and it is website access

`backend/schema/bucket.go:11-25` is the complete bucket shape this app receives
from Garage's admin API:

```go
type Bucket struct {
	ID            string        `json:"id"`
	GlobalAliases []string      `json:"globalAliases"`
	LocalAliases  []LocalAlias  `json:"localAliases"`
	WebsiteAccess bool          `json:"websiteAccess"`
	WebsiteConfig WebsiteConfig `json:"websiteConfig"`
	Keys          []KeyElement  `json:"keys"`
	...
}
```

There is **no** ACL field, **no** bucket-policy field, and **no** "public read"
field. Garage does not implement S3 bucket policies or object ACLs; `Keys` are
per-access-key grants (`read`/`write`/`owner`), which are *authenticated* access,
not anonymous. The only way an unauthenticated `GET` succeeds against Garage is
the **`[s3_web]` website endpoint**, gated per bucket by `websiteAccess`.

**Therefore: "Bucket Visibility → Public Read" IS `websiteAccess`.** Do not build
a second toggle, a parallel permission model, or a `publicRead` field. The brief
explicitly forbids inventing a parallel permission system, and here that
constraint and the platform agree. The existing toggle lives at
`src/pages/buckets/manage/overview/overview-website-access.tsx` and already sends
`{ websiteAccess: { enabled, indexDocument, errorDocument } }` to
`POST /v2/UpdateBucket`.

The one honest UI change is **naming**: the section is labelled "Website Access",
which undersells it. It is the anonymous-read switch. Phase 4 relabels it and
adds the consequence warning the brief asked for.

### 0.2 The public base URL already exists — it is `S3_WEB_PUBLIC_URL`

The brief proposes `PUBLIC_OBJECT_BASE_URL` / `GARAGE_PUBLIC_BASE_URL`. Plan 036
already shipped exactly this concept under the name `S3_WEB_PUBLIC_URL`, and it
already models the addressing style Garage actually supports:

- contains `{bucket}` → **vhost style**: `https://{bucket}.web.example.com`
  → `https://assets.web.example.com/dashboard/homepage.svg`

> ### ⚠️ CORRECTION — added 2026-08-10, after this plan was first executed
>
> Earlier revisions of this plan, of plan 036, and of the shipped
> `README.md` also described a **path style** for a template with no `{bucket}`
> token — "the bucket becomes the first path segment". **That mode does not
> exist in Garage and has never worked.** It was an advisor invention, not a
> Garage capability, and it is the direct cause of a real operator hitting 404s
> on every public URL the app produced.
>
> Measured against a live Garage `[s3_web]` endpoint (three requests, same
> object, differing only in `Host`):
>
> | Path requested | `Host` header | Result |
> |---|---|---|
> | `/hp/dockhand-white.png` | `assets.web.example.tld` | **200**, `image/png`, 13826 B |
> | `/assets/hp/dockhand-white.png` | `web.example.tld` | **404** |
> | `/hp/dockhand-white.png` | `web.example.tld` | **404** |
>
> Garage's website endpoint resolves the bucket **only** from the `Host` header,
> matched against `[s3_web] root_domain`; nothing consumes a leading path
> segment. **Phase 7 removes the mode and every document that describes it.**

It is delivered to the browser as `config.s3_web.public_url`
(`backend/schema/config.go:44-52`, filled by the handler in
`backend/router/config.go`), and it is deliberately **separate** from
`API_BASE_URL` (admin) and `S3_ENDPOINT_URL` / `S3_PUBLIC_ENDPOINT_URL` (S3 API)
— which is precisely the separation the brief demands.

**Do not add a new environment variable.** Adding `PUBLIC_OBJECT_BASE_URL`
alongside `S3_WEB_PUBLIC_URL` would give operators two variables meaning the same
thing, and the brief forbids parallel abstractions.

### 0.3 The hard requirement — URL encoding — is broken today

This is the one genuine blocker, and it is small.
`src/lib/website.ts:78-86`, verbatim:

```ts
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

The key is interpolated **raw**. A key of `dashboard/my icon (2).svg` yields a URL
with a literal space and parentheses; `notes#1.png` truncates at the `#`;
`تقرير.png` emits raw non-ASCII. The repo already has the correct helper —
`encodeObjectPath` in `src/lib/api.ts:25-29`, which encodes each `/`-delimited
segment with `encodeURIComponent` and leaves the separators literal, which is
exactly the rule the brief specifies. It simply was never applied here.

An independent audit pass flagged the same line. It is real.

---

## 1. Existing relevant architecture

Read these before editing. Every one already exists; your job is to extend, not
replace.

| Concern | Where it lives today |
|---|---|
| **Upload** | `src/pages/buckets/manage/browse/upload-queue.ts` — an `XMLHttpRequest` transport (`fetch` cannot report upload progress) posting `FormData` to `PUT /api/browse/{bucket}/{key}`; a zustand queue with a 3-slot pump, cancel, and per-item state. Rendered by `upload-panel.tsx`. |
| **Upload (server)** | `backend/router/browse.go` `PutObject` — `r.ParseMultipartForm(4 << 20)`, then `client.PutObject` with `ContentType: aws.String(headers.Header.Get("Content-Type"))`. |
| **Object listing** | `src/pages/buckets/manage/browse/object-list.tsx` (rows, selection, `fullKey = currentPrefix + object.objectKey`) + `hooks.ts` `useBrowseObjects` (infinite query). |
| **Object actions** | `object-actions.tsx` — per-row Download / Share / Delete menu. `shareDialog.open({ key, prefix })`. |
| **Sharing UI** | `share-dialog.tsx` — already renders **both** a "Private link (expires)" block (presigned, gated on `config.sharing`) and a "Public link (no expiry)" block (the website URL). Plan 036 added the labels and the mixed-content warning. |
| **Presigned URLs** | `backend/router/browse.go` `ShareObject` (`GET /share/{bucket}/{key...}`) using `s3.NewPresignClient` against `utils.Garage.GetS3PublicEndpoint()`. Enabled only when `S3_PUBLIC_ENDPOINT_URL` is set. |
| **Bucket permissions** | `src/pages/buckets/manage/permissions/` — per-access-key `read`/`write`/`owner` via `AllowBucketKey`/`DenyBucketKey`. **Authenticated access only; unrelated to anonymous reads.** |
| **Website/public settings** | `src/pages/buckets/manage/overview/overview-website-access.tsx` — the `websiteAccess` toggle + index/error document, debounced auto-save. |
| **URL construction** | `src/lib/website.ts` — `isWebsiteHostingConfigured`, `getBucketWebsiteBaseUrl`, `getBucketWebsiteObjectUrl`. 25 tests in `src/lib/website.test.ts`. |
| **Key encoding** | `encodeObjectPath` in `src/lib/api.ts:25-29`; Go counterpart `browseObjectURL` in `backend/router/browse.go`. |
| **Config to browser** | `GET /api/config` → `backend/router/config.go` → `schema.ConfigResponse`. Env-derived fields (`sharing`, `s3_web.public_url`) are set **in the handler**, not in `NewConfigResponse`. |
| **Bucket context** | `useBucketContext()` (`src/pages/buckets/manage/context.ts`) gives `{ bucket, bucketName, refetch }`. `bucket.websiteAccess` is the anonymous-read flag. |

**Reverse proxy is already assumed.** `S3_WEB_PUBLIC_URL` exists precisely
because a proxy fronts Garage's web endpoint; `docker-compose.yml` ships a
Traefik example. Nothing in this plan puts the WebUI in the data path — the
browser fetches `Browser → Garage web endpoint` directly.

---

## 2. Garage public access model (documented, not speculated)

| Question from the brief | Answer, from this repo's code |
|---|---|
| Public bucket reads / anonymous GET | Only via the `[s3_web]` website endpoint, per-bucket gate `websiteAccess`. |
| Website access | `bucket.websiteAccess` + `websiteConfig.{indexDocument,errorDocument}`, set through `POST /v2/UpdateBucket`. |
| Bucket aliases | `globalAliases[]`. **The website hostname is the global alias.** A bucket with no global alias cannot be browsed *or* served as a website. |
| Bucket permissions | Per-access-key `read`/`write`/`owner`. Authenticated only — irrelevant to anonymous reads. |
| S3 endpoint vs website endpoint | Different services on different ports. The S3 API always requires SigV4; the website endpoint never does. **Public asset URLs must come from the website endpoint.** |
| Custom domain mapping | Deployment concern. Operator points a hostname at the web endpoint and declares it via `S3_WEB_PUBLIC_URL`. Not the app's job. |
| Path vs virtual-host style | **Virtual-host only.** The bucket comes from the `Host` header, matched against `[s3_web] root_domain`. Path style does not exist — verified against a live Garage: same object, `Host: assets.web.…` → 200, `Host: web.…` with `/assets/…` → 404. `S3_WEB_PUBLIC_URL` therefore **requires** the `{bucket}` token. |
| TLS wildcards do not nest (deployment note) | A cert for `*.example.tld` does **not** cover `assets.web.example.tld` — TLS wildcards match exactly one label. A nested website root domain needs its own `*.web.example.tld` cert, and if public DNS points at a private address, only a DNS-01 challenge can issue it. Not the app's job, but it is where operators get stuck; the README recipe must say so. |
| **Directory listing** | Garage's website endpoint serves `indexDocument` for a prefix and `errorDocument` otherwise. **It does not produce an anonymous index of bucket contents.** A known object URL works; the bucket root does not enumerate. Document this; build nothing. |
| Anonymous writes | Not possible via the website endpoint — it is read-only. Nothing to disable. |

---

## 3. Proposed UX

**Object in a bucket with `websiteAccess: true`, `public_url` configured** —
share dialog leads with the permanent URL:

```
Share  homepage.svg

  Public URL  ·  permanent, no sign-in
  [ https://assets.web.example.local/dashboard/homepage.svg ] [Copy] [Open]

  ▸ Advanced — temporary private link
```

**Object in a bucket with `websiteAccess: true`, `public_url` NOT configured:**

```
  Public read is enabled on this bucket, but no public URL is configured.
  Set S3_WEB_PUBLIC_URL to the address your users reach the website
  endpoint at (for example https://{bucket}.web.example.com).
```

*(No URL is emitted. The derived `http://<bucket><root_domain>:3902` fallback
from `garage.toml` is still shown when it exists — it is a real, working URL on
a bare install — but it carries 036's mixed-content warning when the console is
HTTPS.)*

**Object in a bucket with `websiteAccess: false`** — private, presigned only:

```
Share  secrets.json

  This object is private. Its bucket does not allow anonymous reads.

  Private link (expires)   [15 min] [1 hour] [24 hours] [7 days]
  [Generate link]
```

**Object list** — one quiet badge in the name cell, bucket-wide (not per object,
because the permission is bucket-wide):

```
  dashboard/  
  homepage.svg   Public
  truenas.png    Public
```

**After upload**, in the panel row that already says `Done`:

```
  homepage.svg   ✓ Done   [Copy URL]
```

---

## 4. Public URL construction

One function, extended — `getBucketWebsiteObjectUrl` in `src/lib/website.ts`.

```
publicUrl(bucket, key) = getBucketWebsiteBaseUrl(bucket, config) + "/" + encodeObjectPath(stripLeadingSlashes(key))
```

`getBucketWebsiteBaseUrl` is unchanged (036 already resolves override → derived).
The only change is applying `encodeObjectPath`.

**Encoding rules** (these are `encodeObjectPath`'s existing semantics — do not
write a new encoder):

- Split the key on `/`; `encodeURIComponent` each segment; rejoin with literal `/`.
- `/` separators stay literal → the folder hierarchy survives into the URL.
- Space → `%20`; `#` → `%23`; `%` → `%25`; `(` `)` → `%28` `%29`; non-ASCII → UTF-8 percent-encoding (Arabic, CJK, emoji).
- The extension is never touched: `dashboard/homepage.svg` ends `homepage.svg`.

**Worked examples** (these become test cases):

| bucket | key | `public_url` | result |
|---|---|---|---|
| `assets` | `dashboard/homepage.svg` | `https://{bucket}.web.ex.local` | `https://assets.web.ex.local/dashboard/homepage.svg` |
| `assets` | `dashboard/homepage.svg` | `https://web.ex.local` *(no token)* | **`null`** — rejected, see Phase 7. Emitting `https://web.ex.local/assets/…` here is the bug that shipped. |
| `assets` | `icons/my icon (2).png` | `https://{bucket}.web.ex.local` | `https://assets.web.ex.local/icons/my%20icon%20(2).png` — note `encodeURIComponent` leaves `(` `)` literal, which is valid in a path |
| `assets` | `docs/تقرير.png` | `https://{bucket}.web.ex.local` | `https://assets.web.ex.local/docs/%D8%AA%D9%82%D8%B1%D9%8A%D8%B1.png` |
| `assets` | `notes#1.png` | `https://{bucket}.web.ex.local` | `https://assets.web.ex.local/notes%231.png` |

**Never** build a public URL by presigning and stripping the query — the plan
does not go near `ShareObject`.

---

## 5. Configuration changes

**None.** `S3_WEB_PUBLIC_URL` (036) is the public base URL; it is already
documented in `README.md`'s env table and surfaced as `config.s3_web.public_url`.

Two documentation gaps to close in Phase 6, both real:

1. `S3_WEB_PUBLIC_URL` is **absent from `.env.example`, `backend/.env.example`,
   and the `docker-compose.yml` `environment:` block** — so a Compose operator
   cannot set it, which makes this entire feature unreachable on the primary
   documented deployment path.
2. The README should carry a short "hosting public assets" recipe (create
   bucket → global alias → enable public read → upload → copy URL), plus the
   Caddy/Traefik note that the hostname must route to Garage's **web** port,
   not the S3 API port.

Config stays env-only, matching the project's existing philosophy. No settings
table, no settings UI.

---

## 6. Bucket visibility integration

A single exported helper, so no component re-derives the rule:

```ts
export type PublicAccess =
  | { state: "public"; url: string }        // anonymous read on + a URL we can build
  | { state: "public-no-url" }              // anonymous read on, no public base URL configured
  | { state: "private" };                   // anonymous read off
```

Decision order — **anonymous read is checked first, always**:

1. `bucket.websiteAccess !== true` → `private`. Nothing else is consulted.
2. `getBucketWebsiteObjectUrl(...)` returns `null` → `public-no-url`.
3. otherwise → `public` with the URL.

This is what stops the misleading-label failure the brief calls out: a configured
`public_url` alone can never produce a "Public" badge, because step 1 gates it.

---

## 7. Frontend changes

| File | Change |
|---|---|
| `src/lib/website.ts` | Apply `encodeObjectPath`; add `getPublicAccess`. |
| `src/lib/website.test.ts` | Extend (25 existing tests must keep passing). |
| `src/pages/buckets/manage/browse/share-dialog.tsx` | Reorder to public-first; collapse presigned under "Advanced" when public; add Open + Copy-Markdown/HTML. |
| `src/pages/buckets/manage/browse/object-list.tsx` | One `Public` badge in the header row area. |
| `src/pages/buckets/manage/browse/upload-panel.tsx` | `[Copy URL]` on `done` rows when the bucket is public. |
| `src/pages/buckets/manage/browse/upload-queue.ts` | Set the `File`'s MIME fallback (Phase 3). |
| `src/pages/buckets/manage/overview/overview-website-access.tsx` | Relabel to "Public read (website hosting)"; add the consequence warning. |

## 8. Backend changes

**One, and only if Phase 3's frontend fix proves insufficient** — see Phase 3.
Everything else is frontend + existing Garage APIs, as the brief requires.

`Content-Disposition` needs **no** change: `PutObject` does not set it, so objects
render inline by default. The `?dl=1` download path sets it per-request, which is
correct and out of scope. **Do not "fix" this.**

---

## Phases

Each phase is independently shippable and independently revertable.

### Phase 1 — Encode the object key (the blocker)

- **Objective**: public URLs work for keys with spaces, `#`, `%`, parentheses, and non-ASCII.
- **Files**: `src/lib/website.ts`, `src/lib/website.test.ts`.
- **Change**: import `encodeObjectPath` from `@/lib/api`; apply it in
  `getBucketWebsiteObjectUrl` after stripping leading slashes. Nothing else.
- **Tests**: the five worked examples in §4, plus: empty key → base URL with no
  trailing slash change; a key that is already percent-encoded is encoded again
  (documented, correct — `%` is a literal byte in an S3 key); `/` separators
  survive.
- **Verify**: `pnpm exec vitest run website` → 25 existing + new all pass.
- **Risk**: LOW. **Rollback**: revert one commit; URLs return to today's broken-but-familiar form.

### Phase 2 — `getPublicAccess` + honest labelling

- **Objective**: one source of truth for "is this public", used everywhere.
- **Files**: `src/lib/website.ts` (+ test), `share-dialog.tsx`, `object-list.tsx`.
- **Change**: add `getPublicAccess` per §6. Share dialog leads with the public
  URL when `state === "public"`, shows the configuration hint when
  `"public-no-url"`, and shows only the presigned block when `"private"`. Add
  `[Open]` (an `<a target="_blank" rel="noreferrer">`). Add the `Public` badge to
  the object list.
- **Tests**: the three states, each asserted against `bucket.websiteAccess` ×
  `public_url` presence — **including the trap case**: `websiteAccess: false` +
  `public_url` set → `private`, no URL.
- **Verify**: `pnpm run typecheck && pnpm run test && pnpm run build`.
- **Risk**: LOW. **Rollback**: revert; 036's dialog returns.

### Phase 3 — Correct Content-Type on upload

- **Objective**: `homepage.svg` is stored as `image/svg+xml`, so a browser renders it instead of downloading it.
- **Current state**: `upload-queue.ts:39-40` does `form.append("file", file)`.
  The browser sets that part's `Content-Type` from `File.type`, which the OS
  infers from the extension — **and which is the empty string for any extension
  the OS does not know**. `PutObject` then stores
  `headers.Header.Get("Content-Type")`, i.e. `""`. Garage serves an empty or
  `application/octet-stream` type and the SVG does not render.
- **Change (frontend, preferred)**: when `file.type` is empty, look the type up
  from the filename and append with an explicit type:
  `form.append("file", new File([file], file.name, { type: resolved }))` — or
  `form.append("file", file, file.name)` after constructing a `Blob` with the
  resolved type. Resolve via `mime` (`mime/lite`), **already a dependency**
  (`object-list.tsx` imports it). No new dependency.
- **STOP and report** if `mime/lite` does not resolve `svg`, `webp`, `avif`, or
  `ico` — then the correct fix is the backend fallback below, not a new package.
- **Change (backend, only if the above is insufficient)**: in `PutObject`, when
  the part's content type is empty or `application/octet-stream`, fall back to
  `mime.TypeByExtension(path.Ext(key))` (Go stdlib, already imported in
  `browse.go`). Keep an explicit client-supplied type when present.
- **Tests**: a pure exported `resolveUploadContentType(fileName, fileType)` unit-tested over
  `.svg → image/svg+xml`, `.png`, `.jpg`, `.jpeg`, `.webp`, `.gif`, `.ico`, `.css`,
  `.js`, an unknown extension → `application/octet-stream`, and **a non-empty
  `file.type` is preserved unchanged**.
- **Risk**: LOW — worst case an object gets a more specific type than before.
- **Rollback**: revert; uploads return to browser-supplied types.

### Phase 4 — Rename the toggle and state the consequence

- **Objective**: the anonymous-read switch says what it does.
- **Files**: `overview-website-access.tsx`.
- **Change**: title → `Public read (website hosting)`. Under the enabled toggle:
  *"Anyone who can reach the Garage website endpoint can retrieve objects in this
  bucket without signing in. Uploads and deletions still require credentials."*
  Keep the existing Garage docs link. **Do not** change the payload sent to
  `UpdateBucket`, and **do not** default it on.
- **Tests**: none required (copy change). Existing gates must stay green.
- **Risk**: LOW. **Rollback**: revert.

### Phase 5 — Copy URL after upload  *(requires 037)*

- **Objective**: upload → copy → paste into Homepage, without leaving the panel.
- **Files**: `upload-panel.tsx`, possibly `types.ts`.
- **Change**: on a `done` row, when `getPublicAccess(...)` is `public`, render
  `[Copy URL]` using `copyToClipboard` from `@/lib/utils`. The URL is built from
  `item.key` (already the full prefix + name) and `item.bucket`.
- **Note**: the audit found the upload panel renders items from **all** buckets
  regardless of which bucket page is mounted. Build the URL from `item.bucket`,
  never from the mounted `bucketName`, or a cross-bucket row copies a wrong URL.
- **Tests**: extend `upload-queue.test.ts` for the URL built from a `done` item.
- **Risk**: LOW. **Rollback**: revert.

### Phase 6 — Make the feature reachable and documented

- **Objective**: a Compose operator can actually turn this on.
- **Files**: `.env.example`, `backend/.env.example`, `docker-compose.yml`, `README.md`.
- **Change**: add `S3_WEB_PUBLIC_URL` (and `MAX_UPLOAD_SIZE_MB`, missing for the
  same reason) to both `.env.example` files and the Compose `environment:` block;
  add the README recipe and the proxy note from §5.
- **Verify**: `grep -n "S3_WEB_PUBLIC_URL" .env.example backend/.env.example docker-compose.yml README.md` → a hit in each.
- **Risk**: LOW. **Rollback**: revert.

### Phase 7 — Remove path style everywhere it exists  *(added 2026-08-10, post-execution)*

**Why this phase exists.** Phases 1–6 shipped on branch
`advisor/038-public-asset-urls` and were approved. Then a real deployment proved
that the path-style fallback this plan inherited from plan 036 does not work
(see the CORRECTION box in §0.2 for the three measured requests). Worse, Phase 6
**propagated the wrong claim into two more files**. Do this before the branch
merges — otherwise a second release ships documentation that sends operators
down a path that always 404s. It already cost one operator a working setup.

**Files** (all already in this plan's scope):
`src/lib/website.ts`, `src/lib/website.test.ts`, `README.md`, `.env.example`,
`backend/.env.example`.

**7a — reject a template with no `{bucket}`.** In `src/lib/website.ts`,
`applyPublicUrlTemplate` currently ends:

```ts
  if (template.includes("{bucket}")) {
    return template.split("{bucket}").join(bucketName);
  }
  return `${template}/${bucketName}`;      // ← the invented path style
```

Replace that last line with `return null;`, and document why:

```ts
  // No {bucket} token means we cannot address the bucket at all. Garage's
  // website endpoint resolves the bucket from the Host header only; a leading
  // path segment is consumed by nothing and always 404s. Returning null makes
  // getPublicAccess report "public-no-url", which tells the operator to fix
  // their configuration instead of handing them a broken link.
  return null;
```

`getPublicAccess`'s `"public-no-url"` branch already renders the
"set `S3_WEB_PUBLIC_URL`" guidance, so a token-less template now produces that
message instead of a dead URL. **No change to `getPublicAccess` is needed.**

**7b — correct the three documents.** Each currently claims path style:
`README.md` (the `S3_WEB_PUBLIC_URL` env-table row, and the public-asset
recipe), `.env.example`, and `backend/.env.example`.

State instead that the `{bucket}` token is **required**, because Garage's
website endpoint is virtual-host only. In the README recipe add the two
deployment facts that actually block operators, both learned the hard way:

1. The public hostname must route to Garage's **web** port (default 3902)
   preserving the `Host` header — not the S3 API port.
2. **TLS wildcards do not nest.** A `*.example.tld` certificate does not cover
   `assets.web.example.tld`; a nested root domain needs its own
   `*.web.example.tld` certificate. If public DNS for the zone resolves to a
   private address, HTTP-01 validation cannot reach it and **DNS-01** is the
   only option.

**7c — tests.** In `src/lib/website.test.ts`:

- Change any existing case asserting the path-style output so it expects `null`
  from `getBucketWebsiteObjectUrl` / `"public-no-url"` from `getPublicAccess`.
  **At least one such case exists from Phase 1** — locate it rather than
  assuming: `grep -n "web.ex.local/assets" src/lib/website.test.ts`.
- Add: template with no `{bucket}` and `websiteAccess: true` →
  `{ state: "public-no-url" }`, with a comment naming the reason (Garage is
  vhost-only) so nobody "restores" the fallback later.

**Verify**:
```
pnpm exec vitest run website
grep -rn "first path segment\|path style\|path-style" README.md .env.example backend/.env.example src/lib/website.ts
```
→ tests pass; the grep returns **no matches**, except optionally a line that
explicitly says path style is *not* supported.

**Risk**: LOW — the only behaviour change is that a misconfigured template
yields guidance instead of a URL that 404s.
**Rollback**: revert the commit; the broken mode returns.

### Deliberately NOT in this plan

- **A "Test Public Endpoint" button.** Investigated and deferred with a reason:
  a browser cross-origin `fetch` to the public URL returns an **opaque** response
  whose status is unreadable, so it cannot report 200 vs 403; and a server-side
  HEAD adds an outbound-fetch endpoint to the backend — a real SSRF surface,
  already deferred once in plan 037. `[Open]` from Phase 2 lets the operator
  verify with their own eyes at zero risk. Revisit only with an allowlist bound
  to the configured endpoint.
- **Per-object visibility.** Garage has no object ACLs; visibility is bucket-wide.
- **Anonymous directory listing.** Garage does not do it; do not synthesise it.
- **A second public-URL env var.** See §0.2.
- **Any change to `ShareObject`/presigning.** Presigned links keep working
  unchanged — that is a regression test, not a change.

---

## Done criteria

- [ ] `pnpm run typecheck`, `pnpm run test`, `pnpm run build` all exit 0
- [ ] `src/lib/website.test.ts`: the 25 pre-existing tests still pass, plus new encoding and visibility cases
- [ ] `grep -n "encodeObjectPath" src/lib/website.ts` → present
- [ ] `git diff <BASE> -- backend/router/browse.go` is **empty** unless Phase 3's backend fallback was needed and justified in the report
- [ ] No new environment variable: `grep -rn "PUBLIC_OBJECT_BASE_URL\|GARAGE_PUBLIC_BASE_URL" .` → no matches
- [ ] A bucket with `websiteAccess: false` never renders a public URL, even with `public_url` configured (asserted by test)
- [ ] Presigned generation for a private object is unchanged and still works

**Phase 7 (added 2026-08-10) — all must also hold:**

- [ ] `grep -rn "first path segment\|path style\|path-style" README.md .env.example backend/.env.example src/lib/website.ts`
      → no matches asserting path style is supported
- [ ] `grep -n 'return `${template}/${bucketName}`' src/lib/website.ts` → **no match**
      (the invented fallback is gone)
- [ ] A test asserts a `public_url` with no `{bucket}` token yields
      `{ state: "public-no-url" }`, not a URL
- [ ] `pnpm exec vitest run website` passes with that case present, and **fails**
      if `return null` is reverted to the old `${template}/${bucketName}` —
      mutation-check this, do not assume

## Manual acceptance — the Homepage case

1. Create bucket `assets` with global alias `assets`; enable **Public read**.
2. Set `S3_WEB_PUBLIC_URL=https://{bucket}.web.example.local`; restart; point the hostname at Garage's **web** port.
3. Upload `dashboard/homepage.svg` and `dashboard/truenas.png`.
4. Panel shows `[Copy URL]`; copied value is `https://assets.web.example.local/dashboard/homepage.svg`.
5. In a **logged-out** private window that URL returns 200, `Content-Type: image/svg+xml`, renders, has no query string, and does not redirect to a login page.
6. `icon: https://assets.web.example.local/dashboard/homepage.svg` in Homepage renders. Repeat for the `.png`.
7. Upload `docs/my icon (2).png` and a file with an Arabic name; both URLs open.
8. On a bucket with public read **off**, the dialog offers only the presigned link, and generating one still works.

## STOP conditions

- Any "Current state" excerpt does not match — the code has drifted.
- You find yourself adding a `publicRead` field, an ACL concept, or a second public-URL env var. Re-read §0.
- You conclude the public URL should be produced by presigning and stripping the query. It must not be.
- `mime/lite` cannot resolve `svg`/`webp`/`avif`/`ico` — report, then use the Go stdlib fallback.
- You are about to route object bytes through the WebUI. The browser must fetch Garage directly.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **`websiteAccess` is the anonymous-read boundary.** Anything that reads it to
  decide what to show must check it *first*; a configured `public_url` must never
  imply public.
- **`S3_WEB_PUBLIC_URL` is cosmetic; `S3_PUBLIC_ENDPOINT_URL` is cryptographic.**
  The first only formats a string. The second is the host presigned URLs are
  *signed against* — changing it invalidates signatures. Never swap them.
- **Public URLs outlive this app.** They are served by Garage; the WebUI going
  down does not break them. That is the design goal — keep the WebUI out of the
  data path.
