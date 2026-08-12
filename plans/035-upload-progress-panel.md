# Plan 035: Show upload progress in a queue panel, with per-file cancel and real error messages

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**:
> `git diff --stat 796039f..HEAD -- src/lib/api.ts src/pages/buckets/manage/browse/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED
- **Depends on**: `plans/034-upload-limits-and-real-errors.md` — **soft**. This
  plan works without 034, but 034 is what makes the server send a real 413
  instead of dropping the connection, so the error messages this panel renders
  are far more useful once it has landed. Land 034 first if both are queued.
- **Category**: bug
- **Planned at**: commit `796039f`, 2026-08-06

## Why this matters

Uploading through the object browser gives the user **nothing** — no progress,
no per-file state, no way to cancel. You pick files, the UI sits still for
however long the transfer takes, and then a toast says either "File uploaded!"
or the raw text of whatever went wrong. On a large file over a slow link that is
indistinguishable from a hung app, and the maintainer's actual report was a bare
"network error" with no indication of which file, how far it got, or why.

The root cause of the missing progress is structural, not cosmetic: the upload
goes through `fetch`, and **`fetch` cannot report upload progress**. There is no
`onprogress` for a request body in the Fetch API (`ReadableStream` request
bodies are not supported for this in all target browsers and would not give
byte counts for a `FormData` part anyway). `XMLHttpRequest` is the only web API
that exposes `upload.onprogress`. So this plan swaps the transport for the
upload path only — everything else keeps using `api.ts`'s `fetch` wrapper.

Because we are rewriting that call site anyway, this is also where the swallowed
error gets fixed: `XMLHttpRequest` exposes `xhr.status` and `xhr.responseText`
on `load`, so a 413 or a 500 from Garage reaches the user as its real message
instead of Firefox's generic `NetworkError when attempting to fetch resource`.

After this plan the browse tab shows:

```
┌─ Uploading 3 of 5 ───────────── ✕ ─┐
│ dockhand-white.png  ████████░░  78% │
│ hero.jpg            ██░░░░░░░░  21% │
│ notes.txt           ✓ done          │
│ big.iso             ✗ 413 too large │
└─────────────────────────────────────┘
```

## Current state

### Files

- `src/pages/buckets/manage/browse/actions.tsx` — the Upload and Create Folder
  buttons. `onUploadFile` (lines 34–58) opens a file picker and fires one
  mutation per file.
- `src/pages/buckets/manage/browse/hooks.ts` — TanStack Query hooks for the
  browse tab. `usePutObject` (lines 32–49) is the upload mutation.
- `src/pages/buckets/manage/browse/browse-tab.tsx` — composes the tab; this is
  where the panel gets mounted.
- `src/pages/buckets/manage/browse/types.ts` — shared types for this folder.
- `src/lib/api.ts` — the `fetch` wrapper. Its cookie-reading and URL-building
  logic must be reused, not duplicated.

### `src/pages/buckets/manage/browse/hooks.ts:32-49` — as it exists today

```ts
export const usePutObject = (
  bucket: string,
  options?: UseMutationOptions<any, Error, PutObjectPayload>
) => {
  return useMutation({
    mutationFn: async (body) => {
      const formData = new FormData();
      if (body.file) {
        formData.append("file", body.file);
      }

      return api.put(`/browse/${bucket}/${encodeObjectPath(body.key)}`, {
        body: formData,
      });
    },
    ...options,
  });
};
```

`usePutObject` has **two** callers, and they are not the same kind of thing:

1. `actions.tsx:26` — the real file upload (`{ key, file }`).
2. `actions.tsx:99` — **folder creation** (`createFolder.mutate({ key: \`${prefix}${values.name}/\`, file: null })`),
   a zero-byte PUT with `file: null`.

Folder creation must keep using the existing `fetch`-based mutation. It has no
progress to show, it lives inside a modal with its own `disabled` state, and
routing it through an upload queue would put "create folder" rows in a panel
titled "Uploading". **Leave `usePutObject` exactly as it is** and add the new
XHR path alongside it.

### `src/pages/buckets/manage/browse/actions.tsx:34-58` — as it exists today

```tsx
  const onUploadFile = () => {
    const input = document.createElement("input");
    input.type = "file";
    input.multiple = true;

    input.onchange = (e) => {
      const files = (e.target as HTMLInputElement).files;
      if (!files?.length) {
        return;
      }

      if (files.length > 20) {
        toast.error("You can only upload up to 20 files at a time");
        return;
      }

      for (const file of files) {
        const key = prefix + file.name;
        putObject.mutate({ key, file });
      }
    };

    input.click();
    input.remove();
  };
```

Note the `for` loop: **all** selected files start at once, up to 20 concurrent
uploads. That is a second reason large uploads misbehave and it is fixed here by
the queue's concurrency limit.

### `src/lib/api.ts` — the parts you must reuse

```ts
export const API_URL = BASE_PATH + "/api";

export const encodeObjectPath = (key: string) =>
  key
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");

const readCookie = (name: string) =>
  document.cookie
    .split("; ")
    .find((c) => c.startsWith(name + "="))
    ?.split("=")[1];

const CSRF_COOKIE_NAME = "csrf_token";
const CSRF_HEADER_NAME = "X-CSRF-Token";

export class APIError extends Error {
  status!: number;
  constructor(message: string, status: number = 400) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}
```

and, inside `api.fetch`:

```ts
    const headers: Record<string, string> = {
      [CSRF_HEADER_NAME]: readCookie(CSRF_COOKIE_NAME) ?? "",
    };
    const _url = new URL(API_URL + url, window.location.origin);
    ...
    const res = await fetch(_url, {
      ...options,
      credentials: "include",
      headers: { ...headers, ...(options?.headers || {}) },
    });
```

Three things the XHR path must replicate, or the upload will fail in production:

1. **`credentials: "include"`** → `xhr.withCredentials = true`.
2. **The `X-CSRF-Token` header.** Every write needs it — `middleware.CSRF`
   exempts only `POST /auth/login` and `POST /setup`. Without it the PUT is
   rejected with 403.
3. **The `BASE_PATH` prefix** via `API_URL`. The app supports being mounted
   under a path prefix; a hard-coded `/api/...` breaks those deployments.
4. **Do NOT set `Content-Type` yourself.** The browser must set it so it can
   include the multipart boundary. `api.ts` skips it for `FormData` for exactly
   this reason (`!(options.body instanceof FormData)` at line 79).

### Server error-body shape

Error responses from this backend are **plain text**, not JSON
(`backend/utils/utils.go:21-29` writes `err.Error()` directly). So
`xhr.responseText` on a non-2xx *is* the message. Success responses are JSON.

### Repo conventions to match

- **State stores use `zustand`'s `createStore` + a plain object of actions**,
  not the `create` hook API. Two exemplars, both short — read them:
  - `src/lib/disclosure.ts` (`createDisclosure`, `useStore(store)`)
  - `src/stores/app-store.ts` (`createStore(...)` then
    `const appStore = { ...store, setTheme: ... }`)
- Query keys are arrays: `["browse", bucketName]`. Invalidate with
  `queryClient.invalidateQueries({ queryKey: ["browse", bucketName] })`.
- UI primitives are thin daisyUI wrappers in `src/components/ui/`. Available:
  `button.tsx`, `checkbox.tsx`, `chips.tsx`, `code.tsx`, `form-control.tsx`,
  `goto-top-btn.tsx`, `input.tsx`, `menu.tsx`, `select.tsx`, `toggle.tsx`.
  There is **no** progress primitive — use daisyUI's `progress` CSS classes
  directly (`<progress className="progress progress-primary w-full" value={n}
  max={100} />`), which is what `react-daisyui` renders anyway.
- Icons come from `lucide-react`.
- Toasts come from `sonner` (`import { toast } from "sonner"`).
- Z-index values come from `src/lib/z-layers.ts` — `Z_LAYERS.dropdown` = 1000,
  `popover` = 1100, `tooltip` = 1200, `toast` = 1300. If the panel floats, use
  a value from that map; do not invent a new magic number.
- The `@/` import alias maps to `src/`.
- Writes are gated on `useAuth().canWrite` (`src/hooks/useAuth.ts`). `actions.tsx`
  already returns `null` for a viewer.
- **`pnpm run lint` is expected to be red** — `main` carries ~55 pre-existing
  problems, mostly `@typescript-eslint/no-explicit-any`, and CI runs lint
  `continue-on-error`. Make *new* code lint-clean; do not clear the backlog.

### jsdom constraint for tests

`jsdom` has no layout engine and **no `XMLHttpRequest` upload progress**. Tests
must stub `XMLHttpRequest` rather than rely on the real one. See
`src/components/ui/menu.test.tsx` for the established pattern of stubbing
browser APIs jsdom lacks (it stubs `Element.prototype.getBoundingClientRect`
and `clientWidth`/`clientHeight`).

## Commands you will need

Run from the repo root.

| Purpose   | Command                                        | Expected on success |
|-----------|------------------------------------------------|---------------------|
| Install   | `pnpm install`                                  | exit 0              |
| Typecheck | `pnpm run typecheck`                            | exit 0, no errors   |
| Tests     | `pnpm run test`                                 | all pass            |
| One file  | `pnpm exec vitest run upload-queue`             | all pass            |
| Lint      | `pnpm run lint`                                 | see note above      |
| Build     | `pnpm run build`                                | exit 0, writes `dist/` |

If `pnpm` is not on your `PATH`, activate the pinned version rather than
substituting `npm` — the lockfile is `pnpm-lock.yaml` and `package.json` pins
`"packageManager": "pnpm@9.15.9"`:
`corepack enable && corepack prepare pnpm@9.15.9 --activate`.

## Scope

**In scope**:

- `src/lib/api.ts` — export two small helpers only (see Step 1). No behaviour
  change to `api.fetch`.
- `src/pages/buckets/manage/browse/upload-queue.ts` (create) — the store + the
  XHR transport.
- `src/pages/buckets/manage/browse/upload-queue.test.ts` (create)
- `src/pages/buckets/manage/browse/upload-panel.tsx` (create) — the UI.
- `src/pages/buckets/manage/browse/actions.tsx` — `onUploadFile` enqueues
  instead of mutating.
- `src/pages/buckets/manage/browse/browse-tab.tsx` — mount the panel.
- `src/pages/buckets/manage/browse/types.ts` — add the queue item type.

**Out of scope** (do NOT touch, even though they look related):

- `usePutObject` in `src/pages/buckets/manage/browse/hooks.ts` — still used by
  folder creation. Leave the function, its signature, and its `fetch` transport
  untouched.
- The `CreateFolderAction` component in `actions.tsx` — folders are not
  uploads and must not appear in the queue.
- `api.fetch`, `api.get/post/put/delete` — no behaviour change. Every other
  call site depends on them.
- `src/pages/buckets/manage/browse/bulk-actions.tsx`,
  `object-list.tsx`, `object-actions.tsx`, `share-dialog.tsx` — untouched.
- Any backend file. The server half is plan 034.
- Drag-and-drop upload, folder upload (`webkitdirectory`), resumable/chunked
  upload, and S3 multipart upload. Multipart is deferred item **D2b** in
  `plans/README.md`. Adding any of them here makes the diff unreviewable.

## Git workflow

- Branch: `advisor/035-upload-progress-panel`
  (create it from `main`: `git checkout -B advisor/035-upload-progress-panel main`)
- Conventional-commit messages, matching `git log`. Examples from this repo:
  `fix: hide menus when the trigger scrolls away and size them to content`,
  `docs: track the plans/ backlog and ignore .claude/`.
- Do NOT push, open a PR, or merge.

## Steps

### Step 1: Export the two `api.ts` primitives the XHR transport needs

The XHR path needs the same URL and CSRF header as `api.fetch`, and there must
be exactly one definition of each. In `src/lib/api.ts`:

- Change `const readCookie` to keep it private, but add two **exported**
  helpers below the `CSRF_HEADER_NAME` constant:

```ts
/**
 * Absolute URL for an API path, honouring BASE_PATH — the same resolution
 * `api.fetch` performs. Exported for the upload transport, which has to use
 * XMLHttpRequest (fetch cannot report upload progress) and therefore cannot go
 * through `api.fetch`.
 */
export const apiUrl = (path: string) =>
  new URL(API_URL + path, window.location.origin).toString();

/**
 * The CSRF header pair every write must carry. `middleware.CSRF` exempts only
 * `POST /auth/login` and `POST /setup`; anything else without this is 403.
 */
export const csrfHeader = (): Record<string, string> => ({
  [CSRF_HEADER_NAME]: readCookie(CSRF_COOKIE_NAME) ?? "",
});
```

- Then use them inside `api.fetch` so the two paths cannot drift:
  replace the `headers` initialiser with `const headers: Record<string, string> = { ...csrfHeader() };`
  and leave the `_url` construction as-is (it needs the `URL` object for
  `searchParams`, so do not force it through `apiUrl`).

Nothing else in `api.ts` changes.

**Verify**: `pnpm run typecheck` → exit 0. Then
`pnpm run test` → all existing tests still pass.

### Step 2: Add the queue item type

In `src/pages/buckets/manage/browse/types.ts`, append:

```ts
export type UploadStatus = "queued" | "uploading" | "done" | "error" | "canceled";

export type UploadItem = {
  /** Stable identity for React keys and cancel lookups. */
  id: string;
  /** Full object key, i.e. prefix + file name. */
  key: string;
  /** Display name — the file name without the prefix. */
  name: string;
  bucket: string;
  size: number;
  loaded: number;
  status: UploadStatus;
  /** Populated only when status === "error". Server text, or a diagnostic. */
  error?: string;
};
```

**Verify**: `pnpm run typecheck` → exit 0.

### Step 3: Write the upload store and XHR transport

Create `src/pages/buckets/manage/browse/upload-queue.ts`.

Follow `src/stores/app-store.ts`'s shape: a module-level `createStore(...)`,
then an exported object spreading the store and adding actions.

Required pieces:

```ts
import { createStore, useStore } from "zustand";
import { apiUrl, csrfHeader, encodeObjectPath } from "@/lib/api";
import { UploadItem, UploadStatus } from "./types";

/** How many uploads run at once. */
export const MAX_CONCURRENT_UPLOADS = 3;
```

**`uploadFile` — the exported, testable transport.** Keep it a free function
with injected callbacks so a test can drive it with a fake `XMLHttpRequest`:

```ts
export type UploadHandle = { abort: () => void };

export type UploadFileArgs = {
  bucket: string;
  key: string;
  file: File;
  onProgress: (loaded: number, total: number) => void;
  onSuccess: () => void;
  onError: (message: string) => void;
  /** Injectable for tests; defaults to the global. */
  createXhr?: () => XMLHttpRequest;
};

export const uploadFile = ({ ... }: UploadFileArgs): UploadHandle => { ... };
```

Its body must:

1. Build `const url = apiUrl(\`/browse/${bucket}/${encodeObjectPath(key)}\`)`.
2. `const form = new FormData(); form.append("file", file);`
3. `xhr.open("PUT", url, true); xhr.withCredentials = true;`
4. Set every header from `csrfHeader()` via `xhr.setRequestHeader`.
   **Never set `Content-Type`** — the browser adds the multipart boundary.
5. `xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress(e.loaded, e.total); }`
6. `xhr.onload = () => { ... }` — branch on `xhr.status`:
   - `>= 200 && < 300` → `onProgress(file.size, file.size)` then `onSuccess()`.
     (The final `progress` event does not always fire at exactly 100%.)
   - otherwise → `onError(...)` with **the server's text**, falling back to the
     status when the body is empty:
     ```ts
     const body = (xhr.responseText || "").trim();
     onError(body || `Upload failed with status ${xhr.status}`);
     ```
7. `xhr.onerror = () => onError(...)` — this is the case that currently
   surfaces as a bare "network error". Produce something diagnosable instead of
   the browser's generic string:
   ```ts
   xhr.onerror = () =>
     onError(
       "The connection dropped before the server replied. The file may exceed a " +
         "size limit on the server or a reverse proxy in front of it."
     );
   ```
8. `xhr.ontimeout` → `onError("Upload timed out.")`. Do **not** set
   `xhr.timeout`; leaving it at 0 (no timeout) is correct for large uploads.
   The handler is there only for completeness.
9. `xhr.onabort = () => {}` — cancellation is driven by the store, which sets
   the status itself; the handler must not overwrite it with an error.
10. `xhr.send(form)` and `return { abort: () => xhr.abort() }`.

**The store.** State: `{ items: UploadItem[] }`. Keep the in-flight
`UploadHandle`s in a module-level `Map<string, UploadHandle>` **outside** the
store — they are not serializable state and must not trigger re-renders.

Actions to export:

- `enqueue(bucket: string, prefix: string, files: File[])` — appends one
  `UploadItem` per file with `status: "queued"`, `loaded: 0`,
  `key: prefix + file.name`, `name: file.name`, then calls the pump.
- `cancel(id: string)` — aborts the handle if present, sets that item's status
  to `"canceled"`, then pumps.
- `dismiss(id: string)` — removes one finished item.
- `clearFinished()` — removes every item whose status is `done`, `error`, or
  `canceled`.
- `useUploadQueue()` — `useStore(store)`, mirroring `useDisclosure` in
  `src/lib/disclosure.ts`.

**The pump** (private): while the number of `uploading` items is below
`MAX_CONCURRENT_UPLOADS`, take the first `queued` item, flip it to
`"uploading"`, and start `uploadFile` with callbacks that patch that item by
`id` and — on any terminal outcome — call the pump again.

Two correctness requirements, both easy to get wrong:

- **Patch by `id`, never by array index.** Items can be dismissed while others
  are in flight.
- **A completion callback that arrives after the item was canceled must not
  resurrect it.** Guard every callback with a status check: if the item is
  missing or already `"canceled"`, return without patching.

**Notifying the object list.** The store cannot call `useQueryClient()`. Instead
expose a subscribable signal the panel component can watch:

```ts
/** Bumped once per successful upload so the browse list can refetch. */
completedCount: number;
```

Increment it in the success path; the panel then runs

```ts
useEffect(() => {
  if (completedCount > 0) {
    queryClient.invalidateQueries({ queryKey: ["browse", bucketName] });
  }
}, [completedCount]);
```

**Verify**: `pnpm run typecheck` → exit 0.

### Step 4: Test the store and transport

Create `src/pages/buckets/manage/browse/upload-queue.test.ts`. Model the
file structure on `src/pages/buckets/manage/browse/hooks.test.ts` (plain
`describe`/`it` from `vitest`, no React) plus the API-stubbing approach in
`src/components/ui/menu.test.tsx`.

Write a fake XHR so no network is involved:

```ts
class FakeXhr {
  status = 0;
  responseText = "";
  upload = { onprogress: null as ((e: any) => void) | null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  headers: Record<string, string> = {};
  withCredentials = false;
  aborted = false;
  method = "";
  url = "";
  body: any = null;

  open(method: string, url: string) { this.method = method; this.url = url; }
  setRequestHeader(k: string, v: string) { this.headers[k] = v; }
  send(body: any) { this.body = body; }
  abort() { this.aborted = true; this.onabort?.(); }
}
```

Cases to cover — name each one after the behaviour, not the function:

`uploadFile`:
1. **sends a PUT to the BASE_PATH-aware, percent-encoded URL** — a key of
   `hp/my file #1.png` must produce `.../browse/<bucket>/hp/my%20file%20%231.png`.
2. **attaches the `X-CSRF-Token` header** — set `document.cookie =
   "csrf_token=abc"` and assert `headers["X-CSRF-Token"] === "abc"`.
3. **sets `withCredentials`**.
4. **never sets `Content-Type`** — assert the key is absent from `headers`.
   This is the regression guard for the multipart boundary.
5. **reports progress** — fire `upload.onprogress({lengthComputable: true,
   loaded: 50, total: 100})` and assert `onProgress(50, 100)`.
6. **surfaces the server's error text on a non-2xx** — set `status = 413`,
   `responseText = "upload is too large: ..."`, fire `onload`, assert
   `onError` received that exact string. **This is the bug this plan fixes.**
7. **falls back to the status when the error body is empty** — `status = 502`,
   `responseText = ""` → message contains `502`.
8. **turns a transport failure into a diagnosable message** — fire `onerror`,
   assert the message mentions a size limit or reverse proxy, and that it is
   **not** the empty string.

Store:
9. **`enqueue` starts at most `MAX_CONCURRENT_UPLOADS` at once** — enqueue 5
   files, assert exactly 3 have status `"uploading"` and 2 are `"queued"`.
10. **completing one starts the next** — resolve one, assert the queued count
    drops to 1.
11. **`cancel` on an in-flight item aborts it and marks it `"canceled"`**, and
    a late `onload` for that item does **not** flip it to `"done"`. (Regression
    guard for the resurrection bug called out in Step 3.)
12. **a failed upload does not stall the queue** — the pump must run on error
    too.

Reset the store between tests (`beforeEach`) so cases do not leak state.

**Verify**: `pnpm exec vitest run upload-queue` → all pass, 12 tests.

### Step 5: Build the panel component

Create `src/pages/buckets/manage/browse/upload-panel.tsx`.

Behaviour:

- Renders `null` when `items.length === 0`.
- Header: `Uploading {activeCount} of {items.length}` while any item is
  `queued`/`uploading`; once everything is terminal, `Uploads finished` plus a
  **Clear** action wired to `clearFinished()`. A single `X` button in the header
  also calls `clearFinished()`.
- One row per item: name (truncated, `title` attribute carries the full key),
  then one of:
  - `queued` → the text `Queued`
  - `uploading` → `<progress className="progress progress-primary w-full" value={pct} max={100} />`
    plus `{pct}%`, where `pct = size > 0 ? Math.round((loaded / size) * 100) : 0`,
    and a cancel button (`X` from `lucide-react`) calling `cancel(id)`
  - `done` → a `Check` icon and `Done`
  - `error` → an `AlertCircle` icon and the message, `text-error`, with the full
    message in a `title` attribute so a long server string is readable
  - `canceled` → `Canceled`
- Placement: render it inside the `Card` in `browse-tab.tsx`, directly under
  `ObjectListNavigator` and above `BulkActions`. It is **in-flow, not floating**
  — that keeps it out of the `Z_LAYERS` question entirely and means it cannot
  cover the object table.
- Styling: match `bulk-actions.tsx`'s toolbar, which is the closest sibling —
  `className="flex flex-col gap-2 mx-2 mb-2 px-3 py-2 bg-base-200 rounded-lg"`.
- The `useEffect` on `completedCount` that invalidates
  `["browse", bucketName]` lives here (see Step 3).

Accessibility: the cancel button needs an `aria-label` (e.g.
`Cancel upload of ${name}`), matching how `object-list.tsx` labels its
checkboxes (`aria-label="Select all loaded objects"`).

**Verify**: `pnpm run typecheck` → exit 0; `pnpm run build` → exit 0.

### Step 6: Switch `onUploadFile` to the queue and mount the panel

In `src/pages/buckets/manage/browse/actions.tsx`:

- Delete the `putObject` mutation (lines 26–32) and its now-unused imports
  (`usePutObject` stays imported — `CreateFolderAction` still uses it).
- Rewrite `onUploadFile`'s `onchange` body to call
  `uploadQueue.enqueue(bucketName, prefix, Array.from(files))` instead of the
  `for` loop.
- **Keep** the 20-file guard and its `toast.error` exactly as-is.
- The `toast.success("File uploaded!")` and `queryClient.invalidateQueries`
  move into the panel/store — remove them from here. If `queryClient` becomes
  unused in `Actions`, remove the `useQueryClient()` call from that component
  (it is still needed in `CreateFolderAction`).
- Per-file success no longer needs a toast — the panel shows it. Do **not**
  add 20 toasts.

In `src/pages/buckets/manage/browse/browse-tab.tsx`, add
`<UploadPanel bucketName={bucketName} />` immediately after
`<ObjectListNavigator ... />` and before the `{selected.size > 0 && ...}` block.

**Verify**:

```
pnpm run typecheck && pnpm run test && pnpm run build
```
→ all exit 0. Then:
```
grep -n "putObject" src/pages/buckets/manage/browse/actions.tsx
```
→ no matches (only `createFolder` remains).

### Step 7: Live check

Run the app against a real Garage instance (`pnpm run dev`) and confirm, in a
browser:

1. Selecting 5 files shows the panel with 3 uploading and 2 queued; the queued
   ones start as the others finish.
2. A percentage visibly advances for a file large enough to take a second or
   two.
3. Cancelling an in-flight upload marks it `Canceled` and does not later flip
   to `Done`.
4. The object list refreshes as uploads complete.
5. Creating a folder still works and does **not** appear in the panel.
6. Force an error and confirm the real message appears: set
   `MAX_UPLOAD_SIZE_MB=1` (needs plan 034) or point at a bucket you lack write
   access to, and check the row shows the server's text rather than
   "NetworkError when attempting to fetch resource".

If you cannot reach a live Garage instance, say so explicitly in your report
rather than claiming these passed.

## Test plan

- New file `src/pages/buckets/manage/browse/upload-queue.test.ts`, 12 tests, as
  enumerated in Step 4.
- Structural pattern: `src/pages/buckets/manage/browse/hooks.test.ts` for the
  plain `describe`/`it` shape; `src/components/ui/menu.test.tsx` for stubbing a
  browser API jsdom does not provide.
- The most important three, because they are the reported bug:
  **case 6** (server error text survives), **case 8** (transport failure is
  diagnosable), **case 11** (cancel is not resurrected).
- No component test for `upload-panel.tsx` is required — its logic is a
  rendering of store state that cases 9–12 already pin. Add one only if it
  falls out cheaply.
- Verification: `pnpm run test` → all pass, including 12 new tests, and the
  pre-existing suite unchanged.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run test` exits 0, with 12 new tests in `upload-queue.test.ts`
- [ ] `pnpm run build` exits 0
- [ ] `pnpm exec eslint src/pages/buckets/manage/browse/upload-queue.ts src/pages/buckets/manage/browse/upload-panel.tsx src/lib/api.ts`
      → **0 errors** (new code is lint-clean; the repo-wide backlog is not your
      problem)
- [ ] `grep -n "putObject" src/pages/buckets/manage/browse/actions.tsx` → no
      matches
- [ ] `grep -n "usePutObject" src/pages/buckets/manage/browse/hooks.ts` →
      still present and unmodified (`git diff main..HEAD -- src/pages/buckets/manage/browse/hooks.ts`
      is **empty**)
- [ ] `grep -n "Content-Type" src/pages/buckets/manage/browse/upload-queue.ts`
      → no matches
- [ ] `git diff --stat main..HEAD` lists only the in-scope files
- [ ] `plans/README.md` status row updated

## STOP conditions

Stop and report back (do not improvise) if:

- `usePutObject` or `onUploadFile` does not match the excerpts in "Current
  state" — the file has drifted.
- You conclude the fix requires changing `api.fetch`'s behaviour, or any file
  outside the in-scope list.
- Folder creation breaks at any point. It shares `usePutObject` with the old
  upload path and is the single most likely collateral damage in this plan.
- A verification command fails twice after a reasonable fix attempt.
- You find yourself implementing chunked, resumable, or S3-multipart upload, or
  drag-and-drop. All are explicitly out of scope; multipart is deferred item
  D2b.
- jsdom's `XMLHttpRequest` turns out to be needed for real (it is not — inject
  `createXhr`). If you are reaching for a network mock library, stop: no new
  dependency should be needed for this plan.

## Maintenance notes

For whoever owns this next:

- **The upload path is now the one place in the frontend that does not go
  through `src/lib/api.ts`.** That is deliberate and unavoidable — `fetch` has
  no upload-progress event. The two shared primitives (`apiUrl`, `csrfHeader`)
  exist so the CSRF and BASE_PATH behaviour cannot drift apart; if `api.fetch`
  ever gains another cross-cutting concern (a new header, an auth retry), it
  must be added to `csrfHeader`/`apiUrl` or mirrored in `upload-queue.ts`. A
  reviewer should check this specifically.
- `MAX_CONCURRENT_UPLOADS = 3` is a guess, not a measurement. If large uploads
  still stall, that is the first knob to try.
- The queue is per-page-session: navigating away from the bucket loses it, and
  a page reload cancels in-flight uploads with no warning. A `beforeunload`
  guard was deliberately left out to keep this diff reviewable — it is the
  natural follow-up.
- If D2b (browser-side S3 multipart upload) is built, `uploadFile` is the seam
  to replace: keep the `UploadFileArgs` callback shape and the store, swap the
  transport underneath, and the panel needs no changes.
- The panel deliberately does not float. If someone later makes it a floating
  overlay, it must take a value from `src/lib/z-layers.ts` — the repo already
  paid for one round of ad-hoc z-index fixes (see plans 028 and 032).
