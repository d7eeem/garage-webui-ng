# Plan 036: Tell the user why private share links are unavailable, and let the website URL point at a TLS reverse proxy

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**:
> `git diff --stat 796039f..HEAD -- src/lib/website.ts src/lib/website.test.ts src/types/garage.ts src/pages/buckets/manage/browse/share-dialog.tsx backend/schema/config.go backend/router/config.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: bug (UX honesty) + dx (config)
- **Planned at**: commit `796039f`, 2026-08-06

## Why this matters

A maintainer running the app behind TLS at `https://garage.example.xyz` clicked
**Share** on an object and got back
`http://assets.web.example.xyz:3902/hp/dockhand-white.png` — plain HTTP, on
Garage's raw internal web-server port, with no explanation. They reasonably read
that as a bug in share links. It is not: it is the app's *fallback*, and two
separate design decisions conspire to make it look broken.

**First, the private-link half of the dialog is hidden with no message.** Real
presigned links are gated on `config.sharing`, which is true only when the
operator sets `S3_PUBLIC_ENDPOINT_URL`. When it is unset, `share-dialog.tsx`
renders the entire "Private link" block as nothing at all. The user is never
told that private sharing exists, that it is deliberately off, or that it is one
environment variable away. Silence is the worst possible answer here — it reads
as a broken feature rather than an unconfigured one.

**Second, the website URL is derived purely from `garage.toml` and can only ever
be `http://` on the raw bind port.** `getBucketWebsiteBaseUrl` hard-codes the
scheme and reads the port off `[s3_web] bind_addr`. That is correct for a
bare Garage install and wrong for every deployment that fronts the web endpoint
with a reverse proxy — which is exactly the deployment that also serves this UI
over HTTPS, where an `http://` link is mixed content the browser will complain
about. The app cannot infer the operator's public URL, so it needs to be told.

After this plan: the Share dialog always explains its state, and an operator can
set one variable to make website URLs match the address their users actually
visit.

## Current state

### Files

- `src/pages/buckets/manage/browse/share-dialog.tsx` — the Share modal.
- `src/lib/website.ts` — the three pure URL helpers, fully quoted below.
- `src/lib/website.test.ts` — 15 existing unit tests for those helpers. Extend
  it; do not replace it.
- `src/types/garage.ts` — the frontend's `Config` type (18 lines, quoted below).
- `backend/schema/config.go` — `ConfigResponse`, the browser-safe projection of
  `garage.toml`, and `NewConfigResponse`.
- `backend/schema/config_test.go` — has `TestNewConfigResponseKeepsWebFields`
  and a test asserting no secret ever appears in the marshalled response.
- `backend/router/config.go` — the `GET /config` handler (13 lines).
- `backend/utils/garage.go` — `GetS3PublicEndpoint` / `IsSharingEnabled` at
  lines 85–98.
- `README.md` — the environment-variable table (~line 168).

### `src/lib/website.ts` — the whole file, as it exists today

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

Callers of these helpers — **both must keep working**:

- `src/pages/buckets/manage/browse/share-dialog.tsx:35` —
  `getBucketWebsiteObjectUrl(bucketName, objectKey, config)`
- `src/pages/buckets/manage/overview/overview-website-access.tsx:30` —
  `getBucketWebsiteBaseUrl(bucketName, config)`, plus
  `isWebsiteHostingConfigured(config)` at line 120 for its guidance message.
- `src/pages/buckets/components/bucket-card.tsx:32` — renders a `websiteUrl`
  as an `href`.

### `src/pages/buckets/manage/browse/share-dialog.tsx:47-115` — as it exists today

```tsx
  return (
    <Modal ref={dialogRef} open={isOpen} backdrop>
      <Modal.Header className="truncate">Share {data?.key || ""}</Modal.Header>
      <Modal.Body>
        {config?.sharing && (
          <div className="flex flex-col gap-2 mb-4 pb-4 border-b border-base-content/10">
            <p className="label label-text py-0">Private link (expires)</p>
            ... expiry checkboxes, Generate button, result input ...
          </div>
        )}

        {!bucket.websiteAccess && (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1" />
            Sharing is only available for buckets with enabled website access.
          </Alert>
        )}
        {websiteUrl && (
          <div className="relative mt-2">
            <Input value={websiteUrl} className="w-full pr-12" ... readOnly />
            <Button icon={Copy} onClick={() => copyToClipboard(websiteUrl)} ... />
          </div>
        )}
      </Modal.Body>
      <Modal.Actions>
        <Button onClick={() => shareDialog.close()}>Close</Button>
      </Modal.Actions>
    </Modal>
  );
```

Three problems visible in that block:

1. `config?.sharing` false ⇒ the whole private-link section vanishes silently.
2. The public website URL is shown with **no label**, so the user cannot tell
   what kind of link they just copied — public-forever vs. expiring.
3. The `!bucket.websiteAccess` alert says "Sharing is only available for buckets
   with enabled website access", which became **false** when presigned links
   shipped (plan 015): presigned sharing works on a bucket with no website
   access at all. The copy was never updated.

### `src/types/garage.ts` — the whole file

```ts
export type Config = {
  s3_api?: S3API;
  s3_web?: S3Web;
  sharing?: boolean;
};

export type S3API = {
  s3_region: string;
  root_domain: string;
};

export type S3Web = {
  bind_addr: string;
  root_domain: string;
  index: string;
};
```

### `backend/schema/config.go` — the browser-safe projection

```go
// ConfigResponse is the subset of the Garage configuration that is safe to
// return to the browser. Secret-bearing fields (rpc_secret, admin_token,
// metrics_token) are deliberately absent — the UI never needs them.
type ConfigResponse struct {
	S3API   S3APIResponse `json:"s3_api"`
	S3Web   S3WebResponse `json:"s3_web"`
	Sharing bool          `json:"sharing"`
}

type S3WebResponse struct {
	BindAddr   string `json:"bind_addr"`
	RootDomain string `json:"root_domain"`
	Index      string `json:"index"`
}

func NewConfigResponse(c Config) ConfigResponse {
	return ConfigResponse{
		S3API: S3APIResponse{
			RootDomain: c.S3API.RootDomain,
			S3Region:   c.S3API.S3Region,
		},
		S3Web: S3WebResponse{
			BindAddr:   c.S3Web.BindAddr,
			RootDomain: c.S3Web.RootDomain,
			Index:      c.S3Web.Index,
		},
	}
}
```

`NewConfigResponse` takes only the parsed `garage.toml`. The env-var-derived
`Sharing` flag is set by the **handler**, `backend/router/config.go`:

```go
func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	utils.ResponseSuccess(w, resp)
}
```

The new env-derived field follows the same shape: set it in the handler, not in
`NewConfigResponse`. That keeps `NewConfigResponse` a pure projection and keeps
its existing tests meaningful.

### `backend/utils/garage.go:85-98` — the existing env-override pattern to copy

```go
// GetS3PublicEndpoint returns the endpoint used to SIGN share links — it must be
// reachable by link recipients. Falls back to the internal S3 endpoint.
func (g *garage) GetS3PublicEndpoint() string {
	if ep := os.Getenv("S3_PUBLIC_ENDPOINT_URL"); ep != "" {
		return ep
	}
	return g.GetS3Endpoint()
}

// IsSharingEnabled reports whether a public S3 endpoint is explicitly configured.
// Presigned share links are only offered when it is (an internal-only endpoint
// produces links unreachable to external recipients).
func (g *garage) IsSharingEnabled() bool {
	return os.Getenv("S3_PUBLIC_ENDPOINT_URL") != ""
}
```

### Repo conventions to match

- Go: handlers are methods on empty structs ending in `utils.ResponseSuccess` /
  `utils.ResponseError`; **`utils.ResponseError` does not stop the handler —
  always `return` after it**. DTO structs live in `backend/schema/` with both
  `json:` and `toml:` tags where they map to `garage.toml`; env-only fields need
  a `json:` tag only.
- Go tests: plain `testing`, table-driven where it fits. See
  `backend/schema/config_test.go`.
- Frontend: pure helpers in `src/lib/*.ts` with a sibling `*.test.ts` of plain
  `describe`/`it` Vitest cases. `src/lib/website.test.ts` is the exemplar and
  already has a `mkConfig` factory to reuse.
- daisyUI + Tailwind; `Alert` from `react-daisyui`; icons from `lucide-react`.
  The existing guidance alert in
  `src/pages/buckets/manage/overview/overview-website-access.tsx:120-131` is the
  tone and markup to match — including using `<code>` for config keys.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems, CI runs
  it `continue-on-error`). Make *new* code lint-clean; do not clear the backlog.
- Docs: the README env-var table is the single place these variables are
  documented for operators.

## Commands you will need

| Purpose         | Command                                          | Expected on success |
|-----------------|--------------------------------------------------|---------------------|
| Typecheck       | `pnpm run typecheck`                              | exit 0              |
| Frontend tests  | `pnpm run test`                                   | all pass            |
| One test file   | `pnpm exec vitest run website`                    | all pass            |
| Frontend build  | `pnpm run build`                                  | exit 0              |
| Go build        | `cd backend && go build ./...`                    | exit 0              |
| Go tests        | `cd backend && go test -race ./...`               | all packages `ok`   |
| Go vet + fmt    | `cd backend && go vet ./... && gofmt -l .`        | no output           |

If `pnpm` is not on your `PATH`, activate the pinned version rather than
substituting `npm` — the lockfile is `pnpm-lock.yaml` and `package.json` pins
`"packageManager": "pnpm@9.15.9"`:
`corepack enable && corepack prepare pnpm@9.15.9 --activate`.

## Scope

**In scope**:

- `backend/utils/garage.go` — one new accessor.
- `backend/schema/config.go` — one new field on `S3WebResponse`.
- `backend/schema/config_test.go` — extend.
- `backend/router/config.go` — populate the new field.
- `src/types/garage.ts` — mirror the new field.
- `src/lib/website.ts` — honour the override.
- `src/lib/website.test.ts` — extend.
- `src/pages/buckets/manage/browse/share-dialog.tsx` — the honest-state UI.
- `README.md` — one new row in the env-var table, and correct the
  `S3_PUBLIC_ENDPOINT_URL` row if needed.

**Out of scope** (do NOT touch, even though they look related):

- `backend/router/browse.go`'s `ShareObject` handler and the presigning logic.
  Presigned links work correctly (live-verified in plan 015). This plan changes
  only what the UI *says* when they are unavailable.
- `S3_PUBLIC_ENDPOINT_URL`'s semantics, `IsSharingEnabled`, or
  `GetS3PublicEndpoint`. Do not make sharing default-on: an internal-only
  endpoint produces links recipients cannot reach, which is why it is opt-in.
- `src/pages/buckets/manage/overview/overview-website-access.tsx` and
  `src/pages/buckets/components/bucket-card.tsx` — they call the helpers you are
  changing and must keep working, but they need **no edits**. Their behaviour
  improves for free once `getBucketWebsiteBaseUrl` honours the override.
- Any auth, session, or CSRF file.
- Making the app probe or validate the operator's public URL. It cannot know
  whether a reverse proxy exists; the whole point is that it must be told.

## Git workflow

- Branch: `advisor/036-share-dialog-truth-and-public-website-url`
  (create it from `main`: `git checkout -B advisor/036-share-dialog-truth-and-public-website-url main`)
- Conventional-commit messages, matching `git log`. Examples from this repo:
  `fix: hide menus when the trigger scrolls away and size them to content`,
  `docs: track the plans/ backlog and ignore .claude/`.
- Do NOT push, open a PR, or merge.

## Steps

### Step 1: Add the `S3_WEB_PUBLIC_URL` accessor on the backend

The contract, decided and not open for reinterpretation:

> `S3_WEB_PUBLIC_URL` is the public base URL at which buckets are served as
> static websites. If it contains the literal token `{bucket}`, that token is
> replaced with the bucket's global alias (vhost style, matching Garage's own
> `root_domain` addressing). If it does not, the bucket name is appended as the
> first path segment (path style, for proxies that route that way).
>
> Examples:
> - `https://{bucket}.web.example.com` → `https://assets.web.example.com/hp/x.png`
> - `https://web.example.com`          → `https://web.example.com/assets/hp/x.png`
>
> Unset ⇒ current behaviour (derive `http://<bucket><root_domain>:<port>` from
> `garage.toml`) is unchanged.

One variable rather than separate scheme/host/port knobs, because scheme, host,
port and addressing style all have to agree and an operator who knows their
proxy's URL can just paste it.

In `backend/utils/garage.go`, next to `GetS3PublicEndpoint`, add:

```go
// GetWebPublicURL returns the operator-declared public base URL for static
// website hosting, or "" when unset.
//
// The app cannot derive this: garage.toml describes Garage's own web listener
// (plain HTTP on bind_addr's port), not whatever reverse proxy fronts it. A
// deployment serving this UI over HTTPS almost certainly serves its buckets
// over HTTPS too, and an http:// link from an https:// page is mixed content.
//
// Contains "{bucket}" ⇒ vhost style, the token is substituted. Otherwise ⇒
// path style, the bucket name becomes the first path segment. The frontend
// (src/lib/website.ts) performs the substitution; this only transports the
// value.
func (g *garage) GetWebPublicURL() string {
	return strings.TrimRight(os.Getenv("S3_WEB_PUBLIC_URL"), "/")
}
```

`strings` and `os` are already imported in this file.

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 2: Carry it to the browser in `GET /config`

In `backend/schema/config.go`, add one field to `S3WebResponse`:

```go
type S3WebResponse struct {
	BindAddr   string `json:"bind_addr"`
	RootDomain string `json:"root_domain"`
	Index      string `json:"index"`
	// PublicURL is env-derived (S3_WEB_PUBLIC_URL), not read from garage.toml,
	// so NewConfigResponse leaves it empty — the handler in
	// backend/router/config.go fills it, the same way it fills Sharing.
	PublicURL string `json:"public_url"`
}
```

Do **not** touch `NewConfigResponse`. In `backend/router/config.go`:

```go
func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	resp.S3Web.PublicURL = utils.Garage.GetWebPublicURL()
	utils.ResponseSuccess(w, resp)
}
```

Extend `backend/schema/config_test.go` with a test asserting
`NewConfigResponse` leaves `PublicURL` empty (it is not a `garage.toml` field),
modelled on the existing `TestNewConfigResponseKeepsWebFields`.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```
→ no gofmt/vet output, build exits 0, all packages `ok`.

Confirm no secret leaked into the response shape — the existing test in
`config_test.go` that scans the marshalled body for `rpc_secret`,
`admin_token`, `metrics_token` must still pass. It does not need changing; just
confirm it ran.

### Step 3: Mirror the field in the frontend type

In `src/types/garage.ts`:

```ts
export type S3Web = {
  bind_addr: string;
  root_domain: string;
  index: string;
  /**
   * Operator-declared public base URL (S3_WEB_PUBLIC_URL). Empty when unset.
   * May contain the literal token "{bucket}" for vhost-style addressing.
   */
  public_url?: string;
};
```

Optional (`?`) because `src/lib/website.test.ts` builds `Config` objects from a
`Partial<S3Web>` cast and older servers will not send the field.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 4: Honour the override in `src/lib/website.ts`

Rewrite `getBucketWebsiteBaseUrl` so the override wins, and update the file's
top-of-file doc comment — it currently asserts "we always emit http:// here",
which stops being true.

```ts
/**
 * Applies the operator-declared public base URL (S3_WEB_PUBLIC_URL, delivered
 * as config.s3_web.public_url) to a bucket name.
 *
 * Vhost style when the template contains "{bucket}", path style otherwise.
 * Returns null when no override is configured.
 */
function applyPublicUrlTemplate(
  bucketName: string,
  config?: Config
): string | null {
  const template = config?.s3_web?.public_url?.trim().replace(/\/+$/, "");
  if (!template) return null;
  if (template.includes("{bucket}")) {
    return template.replaceAll("{bucket}", bucketName);
  }
  return `${template}/${bucketName}`;
}
```

Then, in `getBucketWebsiteBaseUrl`, after the `if (!name)` guard and **before**
the `root_domain` requirement:

```ts
  const override = applyPublicUrlTemplate(name, config);
  if (override) return override;
```

Two consequences to get right:

- **The override alone is sufficient.** With `S3_WEB_PUBLIC_URL` set, a URL must
  be produced even if `[s3_web] root_domain` is absent from `garage.toml` — a
  proxy-fronted deployment may not set `root_domain` at all. So the override
  check must come before the `rawRoot` guard, not after.
- **`isWebsiteHostingConfigured` must also return true when the override is
  set**, or `overview-website-access.tsx` will show its "Garage has no web
  endpoint configured" warning next to a perfectly good URL. Update it:

```ts
export function isWebsiteHostingConfigured(config?: Config): boolean {
  return (
    !!config?.s3_web?.public_url?.trim() ||
    !!config?.s3_web?.root_domain?.trim()
  );
}
```

`getBucketWebsiteObjectUrl` needs no change — it composes on top of the base.

`String.prototype.replaceAll` requires an ES2021 lib; if `pnpm run typecheck`
rejects it, use `template.split("{bucket}").join(bucketName)` instead rather
than editing `tsconfig`.

**Verify**: `pnpm exec vitest run website` → the 15 existing tests still pass.

### Step 5: Extend `src/lib/website.test.ts`

Reuse the existing `mkConfig` factory. Add these cases:

`applyPublicUrlTemplate` via `getBucketWebsiteBaseUrl`:
1. vhost template — `public_url: "https://{bucket}.web.ex.com"`, bucket
   `assets` → `https://assets.web.ex.com`.
2. path template — `public_url: "https://web.ex.com"`, bucket `assets` →
   `https://web.ex.com/assets`.
3. trailing slash is stripped — `"https://web.ex.com/"` → `https://web.ex.com/assets`
   (not `//assets`).
4. **the override wins over `root_domain`** — both set, expect the override.
5. **the override works with no `root_domain` at all** — only `public_url` set,
   expect a URL, not `null`. (This is the proxy-fronted deployment.)
6. empty/whitespace `public_url` falls back to the derived URL.
7. no bucket name still returns `null` even with an override set.

`getBucketWebsiteObjectUrl`:
8. override + object key → `https://assets.web.ex.com/hp/dockhand-white.png`
   (the maintainer's actual case, but over HTTPS).

`isWebsiteHostingConfigured`:
9. true when only `public_url` is set.
10. still false when neither is set.

**Verify**: `pnpm exec vitest run website` → all pass, 10 new tests on top of
the existing 15.

### Step 6: Make the Share dialog state-explicit

Edit `src/pages/buckets/manage/browse/share-dialog.tsx`. Four changes:

**(a) Never render the private-link section as nothing.** Replace
`{config?.sharing && (...)}` with a conditional that has an else branch. When
`config?.sharing` is false, render an `Alert` in the same slot:

```tsx
<Alert className="mb-4 items-start text-sm">
  <FileWarningIcon className="mt-1 shrink-0" />
  <span>
    Expiring private links are not enabled. Set{" "}
    <code>S3_PUBLIC_ENDPOINT_URL</code> to the S3 API address your link
    recipients can reach (for example{" "}
    <code>https://s3.example.com</code>) and restart the app.
  </span>
</Alert>
```

Match the markup style of the existing guidance alert at
`src/pages/buckets/manage/overview/overview-website-access.tsx:120-131` —
`<code>` around config keys, `status`/`icon` props on `Alert`.

**(b) Label both link kinds.** The public URL input currently has no label at
all. Give it one — `Public link (no expiry)` — as a sibling `<p className="label
label-text py-0">`, matching the existing `Private link (expires)` label at line
53. A user copying a link must be able to tell which one they got.

**(c) Fix the false statement.** The alert at line 93 reads "Sharing is only
available for buckets with enabled website access." That has been wrong since
presigned links shipped. Reword to describe only what website access controls,
and show it **only when it is the relevant limitation** — i.e. when
`!bucket.websiteAccess`:

```
This bucket has no website access, so it has no public URL. Private links
above still work.
```

Drop "Private links above still work." when `config?.sharing` is false, so the
dialog never points at a section that is not there.

**(d) Warn on mixed content.** When the resolved `websiteUrl` starts with
`http://` and the app itself is served over HTTPS, add a short note under the
public-link input:

```tsx
{websiteUrl?.startsWith("http://") &&
  window.location.protocol === "https:" && (
    <p className="text-xs text-warning mt-1">
      This link is plain HTTP while the console is served over HTTPS. Set{" "}
      <code>S3_WEB_PUBLIC_URL</code> to your website endpoint's public HTTPS
      address.
    </p>
  )}
```

This is the exact condition that produced the maintainer's report.

**Verify**: `pnpm run typecheck && pnpm run test && pnpm run build` → all exit 0.

### Step 7: Document `S3_WEB_PUBLIC_URL`

In `README.md`, add a row to the env-var table next to `S3_PUBLIC_ENDPOINT_URL`
(~line 168), matching the existing column layout and tone:

```
| `S3_WEB_PUBLIC_URL` | *(unset)* | Public base URL for **static website hosting**, overriding the `http://<bucket><root_domain>:<port>` address derived from `garage.toml`. Use `{bucket}` for vhost-style routing (`https://{bucket}.web.example.com`); without it the bucket becomes the first path segment (`https://web.example.com` → `https://web.example.com/mybucket`). Set this whenever a reverse proxy fronts Garage's web endpoint. |
```

While you are in that table, confirm the `S3_PUBLIC_ENDPOINT_URL` row makes
clear it is what **enables** private share links (it already says so — leave it
alone if it does).

**Verify**: `grep -n "S3_WEB_PUBLIC_URL" README.md` → exactly one match, inside
the env-var table.

### Step 8: Live check

Against a real Garage instance:

1. **Unset both variables** — Share dialog shows the `S3_PUBLIC_ENDPOINT_URL`
   guidance alert (not an empty gap) and the derived
   `http://<bucket>.<root_domain>:3902/<key>` labelled `Public link (no expiry)`.
2. **Set `S3_WEB_PUBLIC_URL=https://{bucket}.web.example.com`** and restart —
   the same dialog and the bucket overview both show
   `https://assets.web.example.com/...`; the mixed-content warning is gone.
3. **Set `S3_WEB_PUBLIC_URL=https://web.example.com`** (no token) — URLs become
   `https://web.example.com/assets/...`.
4. **Set `S3_PUBLIC_ENDPOINT_URL`** — the private-link section reappears and
   `Generate link` still returns a working presigned URL.
5. A bucket with website access **off** shows the corrected message and no
   public link.

If you cannot reach a live Garage instance, say so explicitly in your report
rather than claiming these passed.

## Test plan

- **Frontend**: 10 new cases in `src/lib/website.test.ts`, listed in Step 5.
  Structural pattern: the file's own existing `describe`/`it` blocks and its
  `mkConfig` factory. The load-bearing ones are **case 4** (override beats
  `root_domain`) and **case 5** (override works with no `root_domain`) — those
  two are the whole point of the feature.
- **Backend**: one new case in `backend/schema/config_test.go` asserting
  `NewConfigResponse` leaves `PublicURL` empty, modelled on
  `TestNewConfigResponseKeepsWebFields`. The existing secret-leak test must
  still pass unmodified.
- No component test for `share-dialog.tsx` is required — it renders config
  state that the helper tests already pin, and the repo has no existing test for
  that component to model on.
- Verification: `pnpm run test` and `cd backend && go test -race ./...` → all
  pass.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run test` exits 0, with 10 new tests in `src/lib/website.test.ts`
- [ ] `pnpm run build` exits 0
- [ ] `cd backend && go build ./... && go vet ./... && go test -race ./...` —
      exit 0, no vet output, all packages `ok`
- [ ] `cd backend && gofmt -l .` produces no output
- [ ] `grep -n "S3_WEB_PUBLIC_URL" README.md` → exactly one match
- [ ] `grep -rn "S3_WEB_PUBLIC_URL" backend/` → exactly one match, in
      `backend/utils/garage.go`
- [ ] `grep -n "public_url" src/types/garage.ts backend/schema/config.go` → one
      match in each
- [ ] `git diff main..HEAD -- src/pages/buckets/manage/overview/ src/pages/buckets/components/`
      is **empty** — those callers must need no edits
- [ ] `git diff main..HEAD -- backend/router/browse.go` is **empty** — the
      presigning logic is untouched
- [ ] `git diff --stat main..HEAD` lists only the in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `src/lib/website.ts` or `share-dialog.tsx` does not match the excerpts in
  "Current state" — the files have drifted.
- Any of the 15 existing tests in `src/lib/website.test.ts` fails. They pin the
  derive-from-`garage.toml` behaviour, which this plan must **preserve** as the
  fallback, not replace. A failure there means the override check is in the
  wrong place.
- `overview-website-access.tsx` or `bucket-card.tsx` needs an edit to keep
  working. It should not; if it does, the helper signature changed in a way this
  plan did not intend.
- You conclude sharing should be enabled by default, or that
  `S3_PUBLIC_ENDPOINT_URL` should fall back to something. It must not — an
  internal-only endpoint mints links that recipients cannot open, which is worse
  than an honest "not configured" message.
- A verification command fails twice after a reasonable fix attempt.

## Maintenance notes

For whoever owns this next:

- **There are now two independent "public address" variables and they are not
  interchangeable.** `S3_PUBLIC_ENDPOINT_URL` is the **S3 API** endpoint and is
  used to *sign* presigned links — changing it changes the signature host, so it
  must match what recipients actually resolve. `S3_WEB_PUBLIC_URL` is the
  **static website** endpoint and is cosmetic — it only formats a URL, nothing
  is signed with it. A reviewer should confirm the new one is never passed
  anywhere near the presigner in `backend/router/browse.go`.
- `S3_WEB_PUBLIC_URL` is deliberately not validated. If an operator sets
  garbage, they get garbage links; the app has no way to check reachability and
  a startup probe would make the server fail on an unrelated outage.
- The `{bucket}` token convention is now user-facing config. If a third
  addressing style is ever needed, extend the token vocabulary rather than
  adding a second variable.
- The Share dialog now has four possible states (sharing on/off ×
  website access on/off). Any future change there should be checked against all
  four — the bug this plan fixes existed precisely because one of them rendered
  as blank space.
