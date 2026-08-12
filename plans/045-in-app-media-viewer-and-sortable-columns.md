# Plan 045: In-app media viewer + sortable object columns

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- src/pages/buckets/manage/browse/ backend/router/browse.go backend/middleware/headers.go
> ```
> Then confirm every excerpt in "Current state" matches character-for-character.
> On a mismatch, STOP and report.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW (frontend only — no backend change, no new dependency)
- **Depends on**: nothing unmerged. Builds on plan 042 (table column count) and
  plan 043 (object-serving headers), both already on `main`.
- **Category**: direction / UX
- **Planned at**: commit `90a91fd`, 2026-08-10

## Why one plan for two features

They are independent features, but **both edit `src/pages/buckets/manage/browse/object-list.tsx`** — the sort headers touch `Table.Head` and the row
ordering; the viewer touches the row click handler. Two executors on two
branches would conflict on the same file. Do Part A first, then Part B, in one
branch.

---

## Background: what the user asked for

> "media view inside the webui and not a new tab or window if possible, and sortable columns"

Today, clicking an object's name calls `window.open(...)` — every preview leaves
the console. And the object table has no sorting at all: rows appear in whatever
order S3 returns them (lexicographic by key).

---

## Part A — Current state: the viewer

### `src/pages/buckets/manage/browse/object-list.tsx` — the click handler to replace

```tsx
  const onObjectClick = (object: Object) => {
    // object.url arrives percent-encoded from the API; do not re-encode.
    window.open(API_URL + object.url + "?view=1", "_blank");
  };
```

`API_URL` comes from `@/lib/api`. **The comment is load-bearing: never
re-encode `object.url`.** The same `?view=1` URL the new viewer will point its
`<img>`/`<video>` at.

### What the backend will actually serve — `backend/router/browse.go`, `GetOneObject`

```go
	// Always: never let the browser second-guess the type we send.
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if isInlineSafe(stored) {
		w.Header().Set("Content-Type", stored)
	} else {
		// Unknown or scriptable: hand it to the user as a file rather than
		// rendering it on this origin.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDispositionAttachment(keys[len(keys)-1]))
	}

	// Defence in depth: even a type on the allowlist is served with an empty
	// origin and no script execution, so a mislabelled body cannot act.
	w.Header().Set("Content-Security-Policy", "sandbox")
```

and the allowlist (same file), which is **the authority on what can render inline**:

```go
var inlineSafeContentTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/avif": true, "image/bmp": true,
	"image/x-icon": true, "image/vnd.microsoft.icon": true,
	"text/plain": true, "application/pdf": true,
	"video/mp4": true, "video/webm": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/wav": true,
}
```

**Three consequences you must design around — read these twice:**

1. **The backend decides by the object's *stored* content type; the frontend can
   only guess from the file extension.** They will sometimes disagree (a `.png`
   uploaded with `application/octet-stream`, for example). When they do, the
   backend serves `application/octet-stream` and the `<img>` fails to load. The
   viewer **must** handle that with an `onError` fallback, not assume success.
2. **SVG is deliberately absent from the allowlist** (it is XML that can carry
   `<script>`). The viewer must not treat SVG as previewable — the backend will
   serve it as an attachment regardless, so a preview attempt can only fail.
3. **`Content-Security-Policy: sandbox` on that response does not block
   `<img>`/`<video>`/`<audio>`** — per the CSP spec the `sandbox` directive
   applies to documents and workers, and is ignored for subresource loads. It
   *does* apply to the PDF `<iframe>`, which is exactly what we want there.
   If step A5's manual check shows a browser disagreeing, STOP and report —
   do not weaken the backend header.

### The app's own CSP — `backend/middleware/headers.go`

```go
const securityCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
```

Everything the viewer needs is already allowed, because object URLs are
**same-origin** (`/api/browse/...`): `img-src 'self'` covers `<img>`;
`<video>`/`<audio>` fall back to `default-src 'self'`; `<iframe>` falls back to
`default-src 'self'`; `fetch` is covered by `connect-src 'self'`.

**`object-src 'none'` means `<object>` and `<embed>` are blocked.** Use
`<iframe>` for the PDF. Do not use `<embed>`, and **do not modify
`headers.go`** — it is out of scope.

### The dialog pattern this repo uses — `src/lib/disclosure.ts`

```ts
export const createDisclosure = <T = any>() => {
  const store = createStore(() => ({
    data: undefined as T | null,
    isOpen: false,
  }));
  const useDisclosure = () => { … return { ...data, dialogRef } as const; };
  return {
    store,
    use: useDisclosure,
    open: (data?: T | null) => store.setState({ isOpen: true, data }),
    close: () => store.setState({ isOpen: false }),
  };
};
```

Used by `share-dialog.tsx`, which is your exemplar for a dialog in this repo:

```tsx
export const shareDialog = createDisclosure<{ key: string; prefix: string }>();

const ShareDialog = () => {
  const { isOpen, data, dialogRef } = shareDialog.use();
  …
  return (
    <Modal ref={dialogRef} open={isOpen} backdrop>
      <Modal.Header className="truncate">Share {data?.key || ""}</Modal.Header>
      <Modal.Body> … </Modal.Body>
      <Modal.Actions>
        <Button onClick={() => shareDialog.close()}>Close</Button>
      </Modal.Actions>
    </Modal>
  );
};
```

and is mounted in `browse-tab.tsx` inside the `<Card>`, after `<ObjectList />`:

```tsx
        <ShareDialog />
      </Card>
```

**Do not invent a portal or a custom overlay.** `Modal` is a native `<dialog>`,
which renders in the browser's top layer — it needs no z-index. See
`src/lib/z-layers.ts`, which says so explicitly; **do not add an entry to it.**

---

## Part B — Current state: the table

### `object-list.tsx` — the header (note the comment; it is a landmine)

```tsx
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
          {/* The actions column. `Table.Head` renders one <th> per child, so
              this must exist or the table declares 4 columns while every row
              renders 5 — the browser then has no header to size the actions
              column against, and it is pushed out of the container.
              Visually empty, but named for screen readers. */}
          <span className="sr-only">Actions</span>
        </Table.Head>
```

> **`Table.Head` renders one `<th>` per child.** The header must keep **exactly
> five children**. Put the sort button *inside* the existing `<span>` — adding a
> sibling silently adds a column and re-breaks the bug plan 042 fixed. Three
> tests in `object-list.test.tsx` already guard this; they must keep passing.

### `object-list.tsx` — the rows to sort

```tsx
  const pages = data?.pages ?? [];
  const prefixes = pages.flatMap((page) => page.prefixes);
  const objects = pages.flatMap((page) => page.objects);
  const currentPrefix = pages[0]?.prefix ?? "";
```

### The data shape — and where its TypeScript type lies to you

`src/pages/buckets/manage/browse/types.ts`:

```ts
export type Object = {
  objectKey: string;
  lastModified: Date;
  size: number;
  url: string;
};
```

The Go source of truth, `backend/schema/browse.go`:

```go
type BrowserObject struct {
	ObjectKey    *string    `json:"objectKey"`
	LastModified *time.Time `json:"lastModified"`
	Size         *int64     `json:"size"`
	Url          string     `json:"url"`
}
```

**Two traps, both of which will bite a naive comparator:**

1. **`lastModified` is a `string` at runtime, not a `Date`.** It arrives as JSON
   and nothing parses it. `dayjs(object.lastModified)` works on a string, which
   is why nobody has noticed. **`a.lastModified.getTime()` will throw.** Use
   `new Date(x as unknown as string).getTime()`.
2. **`size` and `lastModified` are pointers in Go, so both can be `null`** in
   the JSON despite the TS type. Every comparator must be null-safe, and
   `new Date(null).getTime()` is `0` while `new Date(undefined).getTime()` is
   `NaN` — and **`NaN` in a comparator silently corrupts the sort**. Normalise
   missing values to a sentinel before comparing; never let `NaN` reach the
   return value.

Do **not** "fix" `types.ts` to match — that is a wider change than this plan,
and it would cascade into `dayjs()` and `readableBytes()` call sites. Handle it
defensively in the comparators and leave a comment saying why.

### The repo's comparator exemplar — `src/pages/buckets/page.tsx`

```ts
export const compareByFirstAlias = (
  a: { aliases: string[] },
  b: { aliases: string[] }
) => (a.aliases[0] ?? "").localeCompare(b.aliases[0] ?? "");
```

Exported from the page module, pure, unit-testable. Follow that shape.

---

## Conventions

- Components are arrow functions with a `type Props = {...}` above and
  `export default Name` at the bottom.
- UI from `react-daisyui` (`Modal`, `Table`, `Alert`, `Loading`) with local
  wrappers in `src/components/ui/` (`Button`, `Checkbox`). Icons from
  `lucide-react`. Class names via Tailwind + daisyUI; `cn()` from `@/lib/utils`
  when conditional.
- `@/` aliases `src/`.
- Tests: `@testing-library/react` + `vitest`, `vi.hoisted` for mutable mock
  state, `vi.mock` for hook modules. `object-list.test.tsx` is the exemplar and
  is reproduced in Step B4.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems, CI runs
  it `continue-on-error`). Make new code clean; do not clear the backlog.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| One file | `pnpm exec vitest run <pattern>` | — |
| Build | `pnpm run build` | exit 0 |

`pnpm` may not be on PATH; it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. **Do not substitute `npm`.**

## Scope

**In scope:**
- `src/pages/buckets/manage/browse/media-viewer.tsx` (new)
- `src/pages/buckets/manage/browse/media-viewer.test.tsx` (new)
- `src/pages/buckets/manage/browse/sorting.ts` (new)
- `src/pages/buckets/manage/browse/sorting.test.ts` (new)
- `src/pages/buckets/manage/browse/object-list.tsx` (modify)
- `src/pages/buckets/manage/browse/object-list.test.tsx` (extend)
- `src/pages/buckets/manage/browse/browse-tab.tsx` (mount the viewer — one line)

**Out of scope — do NOT touch:**
- **Any Go file.** No backend change is needed. In particular do not touch
  `browse.go`'s `inlineSafeContentTypes` / `isInlineSafe`, and do not relax
  `middleware/headers.go`. If you believe a backend change is required, STOP.
- `types.ts` — see the trap note above.
- `src/lib/z-layers.ts` — native `<dialog>` is not on that scale.
- `share-dialog.tsx`, `object-actions.tsx`, `bulk-actions.tsx`, `upload-queue.ts`,
  `actions.tsx`, `hooks.ts`.
- The `FilePreview` thumbnail component at the bottom of `object-list.tsx` —
  the 20px row icon. It is unrelated to the full-size viewer. Leave it alone.
- Any new dependency. `mime/lite` (already a dep, already imported in
  `object-list.tsx`) covers type detection.

## Git workflow

- Branch: `advisor/045-media-viewer-and-sorting` from your given base.
- Conventional commits; at least two (`feat: preview media inside the console`,
  `feat: sort the object table by name, size and date`).
- Do NOT push, open a PR, or merge.

---

# Part A — In-app media viewer

### Step A1: Classify what can be previewed

Create `src/pages/buckets/manage/browse/media-viewer.tsx`. At the top, a pure
exported classifier:

```tsx
/** How the viewer should render an object, or null if it cannot preview it. */
export type MediaKind = "image" | "video" | "audio" | "pdf" | "text";

/**
 * Decide how to preview an object, from its file extension.
 *
 * This MIRRORS the backend allowlist in backend/router/browse.go
 * (`inlineSafeContentTypes`) — the backend is the authority and decides from
 * the object's *stored* content type, which we cannot see from the listing.
 * When the two disagree the backend serves application/octet-stream and the
 * media element fails to load, which is why every renderer below has an
 * onError fallback. Keep this list a subset of the Go one.
 *
 * SVG is deliberately absent: it is XML that can carry <script>, the backend
 * refuses to serve it inline, and a preview attempt could only fail.
 */
export function classifyMedia(objectKey: string): MediaKind | null { … }
```

Implement with `mime.getType()` from `mime/lite` (import exactly as
`object-list.tsx` does: `import mime from "mime/lite";`), then:

- `image/svg+xml` → **`null`** (check this *before* the `image/` prefix test)
- `image/*` → `"image"` — but only for the extensions the backend allows:
  `png, jpg, jpeg, gif, webp, avif, bmp, ico`
- `video/mp4`, `video/webm` → `"video"`
- `audio/mpeg`, `audio/ogg`, `audio/wav` → `"audio"`
- `application/pdf` → `"pdf"`
- `text/plain` → `"text"`
- anything else, or no extension, or `mime.getType` returns `null` → `null`

> `mime/lite` does not know `.ico` (documented in `browse.go`'s
> `resolveUploadContentType` comment). Special-case the extension directly.
> Also note `mime/lite` is **ESM-only** — `require()` will fail; the test file
> must `import` it.

**Verify**: `pnpm run typecheck` → exit 0.

### Step A2: The viewer component

In the same file:

```tsx
export const mediaViewer = createDisclosure<{
  /** Objects in the current view that classifyMedia can render, in display order. */
  items: { objectKey: string; url: string }[];
  /** Index into `items` of the object to show first. */
  index: number;
}>();
```

The component:

- `const { isOpen, data, dialogRef } = mediaViewer.use();`
- Local `useState` for the current index, seeded from `data.index`. Re-seed with
  a `useEffect` on `[data]` so reopening on a different object works.
- Wrap in `<Modal ref={dialogRef} open={isOpen} backdrop className="max-w-4xl w-11/12">`.
- `<Modal.Header className="truncate">` — the current object's key.
- Body renders by `classifyMedia(current.objectKey)`:
  - `image` → `<img src={src} alt={key} className="max-h-[70vh] mx-auto object-contain" onError={…} />`
  - `video` → `<video src={src} controls className="max-h-[70vh] w-full" onError={…} />`
  - `audio` → `<audio src={src} controls className="w-full" onError={…} />`
  - `pdf` → `<iframe src={src} title={key} className="w-full h-[70vh]" />`
  - `text` → see Step A3
- `src` is `` `${API_URL}${item.url}?view=1` `` — **`item.url` arrives
  percent-encoded from the API; do not re-encode it.** Carry that comment over.
- **Error state**: `useState` an `failed` flag reset whenever the index changes;
  `onError` sets it. When set, render instead: an `<Alert>` saying the preview
  is unavailable and the file can still be downloaded, plus a `Button` that does
  `window.open(API_URL + item.url + "?dl=1", "_blank")` — matching
  `object-actions.tsx`'s existing `onDownload`.
- **Prev/next**: `ChevronLeft` / `ChevronRight` `Button`s in `Modal.Actions`,
  disabled at the ends, with `aria-label="Previous"` / `"Next"`, plus a
  `n of m` label. Skip them entirely when `items.length === 1`.
- `Modal.Actions` also gets a `Close` button calling `mediaViewer.close()`.

**Do not** add a keydown listener for arrow keys — the native `<dialog>` already
handles Escape, and a document-level listener here would leak across dialogs.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step A3: Text preview, safely

For `kind === "text"`, fetch the body rather than embedding it:

- `useEffect` on the current src; `fetch(src, { credentials: "include" })`.
- **Cap the read at 256 KB** — read `res.text()` then `slice(0, 256 * 1024)`,
  and when truncated append a visible `… (truncated)` note. An object listing
  can contain a multi-GB `.txt`; do not put it in a DOM node.
- Render as `<pre className="whitespace-pre-wrap break-all text-xs max-h-[70vh] overflow-auto">{text}</pre>`.
- **Render the text as a React child, never via `dangerouslySetInnerHTML`.**
  React escapes children; that escaping is the only thing standing between a
  hostile `.txt` and script execution on this origin. If you find yourself
  typing `dangerouslySetInnerHTML`, STOP.
- Abort the fetch on unmount / index change with an `AbortController`, and
  ignore `AbortError` in the catch so a fast next-click does not flash an error.
- On fetch failure, reuse the same "preview unavailable" fallback from A2.

**Verify**: `grep -rn "dangerouslySetInnerHTML" src/pages/buckets/manage/browse/` → **no matches**.

### Step A4: Wire it into the list, keeping the old path as a fallback

In `object-list.tsx`, replace `onObjectClick`:

- Build the previewable set from the objects **in current display order**
  (i.e. after Part B's sort, once that exists — so do this wiring but revisit
  the ordering at the end of Part B):
  ```tsx
  const previewable = sortedObjects.filter((o) => classifyMedia(o.objectKey) !== null);
  ```
- On click: if `classifyMedia(object.objectKey) !== null`, call
  `mediaViewer.open({ items: previewable, index: previewable.findIndex(…) })`.
- **Otherwise keep today's behaviour exactly**:
  `window.open(API_URL + object.url + "?view=1", "_blank")`. A `.zip` or a
  `.docx` must still do what it does today — the backend serves it as an
  attachment, so this is the download path. Do not remove it.

Mount the component in `browse-tab.tsx` as a sibling of `<ShareDialog />`:

```tsx
        <ShareDialog />
        <MediaViewer />
      </Card>
```

**Verify**: `pnpm run typecheck && pnpm run test` → exit 0, all pass (the three
existing `object-list.test.tsx` cases must still pass — if the column-count test
broke, you changed the header; revert that).

### Step A5: Manual checks — reviewer's job

You have no browser. Do **not** claim these passed; list them under NOTES:

1. A `.png`, a `.mp4`, an `.mp3`, a `.pdf` and a `.txt` each preview in the
   modal without leaving the page.
2. A `.svg` and a `.zip` still open via the old path and are downloaded, not
   rendered inline.
3. A `.png` that was uploaded with a wrong/absent content type shows the
   "preview unavailable" fallback and its Download button works.
4. Prev/next steps through only the previewable objects, in the order the table
   currently displays them (including after a re-sort).
5. Escape and the backdrop close the modal; no console CSP violations appear.
6. **PDF specifically**: confirm the `sandbox` CSP on the object response does
   not blank the iframe in Chrome and Firefox. If it does, report it — the fix
   is a plan decision, not a header relaxation.

---

# Part B — Sortable columns

### Step B1: Pure comparators

Create `src/pages/buckets/manage/browse/sorting.ts`:

```ts
export type SortColumn = "name" | "size" | "lastModified";
export type SortDirection = "asc" | "desc";
export type SortState = { column: SortColumn; direction: SortDirection };

/** The server's own order: S3 ListObjectsV2 returns keys lexicographically. */
export const DEFAULT_SORT: SortState = { column: "name", direction: "asc" };
```

Then `export function sortObjects<T extends { objectKey: string; size: number; lastModified: Date }>(objects: T[], sort: SortState): T[]`.

Requirements, each of which has a test in B3:

- **Pure**: return a new array (`[...objects].sort(...)`), never sort in place.
  The input is `pages.flatMap(...)`, and mutating it corrupts render order.
- **Null-safe.** `size` and `lastModified` are pointers in Go and arrive as
  `null`. Normalise missing to `0` **before** comparing.
- **`lastModified` is a string at runtime**, despite its `Date` type — parse
  with `new Date(v as unknown as string).getTime()`, and map a `NaN` result to
  `0`. A `NaN` returned from a comparator silently corrupts the sort order.
  Leave a comment saying why the cast is there.
- **Name sorting uses `localeCompare`**, matching `compareByFirstAlias` in
  `src/pages/buckets/page.tsx`.
- **Ties break by `objectKey` ascending**, so equal sizes/dates render in a
  stable, predictable order.

Also export a helper for folder rows:

```ts
/**
 * Folders sort among themselves by name and always render above files,
 * regardless of the active column — a folder has neither a size nor a date, so
 * interleaving them with files would put blank rows in the middle of a sort.
 */
export function sortPrefixes(prefixes: string[], direction: SortDirection): string[]
```

`sortPrefixes` honours only the direction, and only when the active column is
`name` — otherwise it returns name-ascending order. Keep that rule in the doc
comment.

**Verify**: `pnpm run typecheck` → exit 0.

### Step B2: Sortable headers

In `object-list.tsx`:

- `const [sort, setSort] = useState<SortState>(DEFAULT_SORT);`
- Apply with `useMemo`:
  ```tsx
  const sortedObjects = useMemo(() => sortObjects(objects, sort), [objects, sort]);
  const sortedPrefixes = useMemo(() => sortPrefixes(prefixes, …), [prefixes, sort]);
  ```
  and render from the sorted arrays. **`allLoadedKeys` must be derived from the
  sorted array too** — or rather, it must stay correct: it is a set membership
  check, so order does not matter, but do not accidentally leave it pointing at
  a stale variable.
- Clicking a header toggles direction if it is the active column, otherwise
  switches to that column with `asc`. Two states only — no third "unsorted"
  state; the default *is* name-ascending, which equals server order.
- Replace the three header `<span>`s (`Name`, `Size`, `Last Modified`) with
  **the same `<span>` containing a `<button>`**:
  ```tsx
  <span>
    <button
      type="button"
      className="inline-flex items-center gap-1 hover:text-base-content"
      onClick={() => toggleSort("name")}
      aria-label="Sort by name"
    >
      Name
      {sort.column === "name" ? (
        sort.direction === "asc" ? <ChevronUp size={14} /> : <ChevronDown size={14} />
      ) : null}
    </button>
  </span>
  ```
  > **The header must still have exactly five children.** The button goes
  > *inside* the existing span. Do not add a sixth child, and do not remove the
  > `sr-only` Actions span.
- Set `aria-sort` on the header cell. `Table.Head` owns the `<th>`, so you
  cannot pass props to it directly — put `aria-sort` on the inner `<span>`
  instead and note in a comment that the `<th>` is generated by `Table.Head`.
  **If `aria-sort` on the span does not satisfy the accessibility test you
  write, do not restructure `Table.Head` to get it onto the `<th>` — that is the
  exact change plan 042 had to repair. Drop the `aria-sort` assertion and note
  it in NOTES instead.**

**Verify**: `pnpm exec vitest run object-list` → all pass, including the three
pre-existing column-count cases.

### Step B3: Say the truth about pagination

The list is an infinite query (`useBrowseObjects`, `limit: 1000`, "Load more").
S3 has **no server-side sort** — `ListObjectsV2` returns keys lexicographically
and that is all. So a sort applies **only to the objects already loaded**.

When `hasNextPage` is true **and** the sort is not `DEFAULT_SORT`, render a
one-line hint above the table:

> Sorted across the objects loaded so far — load more to sort the rest.

Muted styling (`text-xs text-base-content/60`). This is not optional polish: a
user who sorts by size descending on a partially-loaded bucket will otherwise
believe they are looking at the largest object in the bucket, and be wrong.

**Do not** try to fix this by fetching all pages on sort — a bucket can hold
millions of keys, and that would hang the browser. If you think you have a way
to sort server-side, STOP and report rather than building it.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step B4: Tests

`src/pages/buckets/manage/browse/sorting.test.ts` (new, pure — no rendering):

1. Sorts by name ascending and descending.
2. Sorts by size ascending and descending.
3. Sorts by `lastModified` **given string inputs** — the runtime shape. Build
   fixtures with `lastModified: "2026-01-02T00:00:00Z" as unknown as Date`.
4. **Tolerates `null` size and `null` lastModified** without throwing, and
   places them consistently. This is the regression guard for the Go-pointer
   trap; if it is missing the plan is not done.
5. **Does not mutate its input** — assert the original array's order is
   unchanged after sorting.
6. Ties on size break by `objectKey` ascending.
7. `sortPrefixes` returns name order and honours direction only for `name`.

`src/pages/buckets/manage/browse/object-list.test.tsx` (extend — do **not**
create a second file). The existing mock scaffolding is reproduced here so you
do not have to reverse-engineer it:

```tsx
const mockBrowse = vi.hoisted(() => ({
  data: undefined as { pages: GetObjectsResult[] } | undefined,
  error: null as Error | null,
  isLoading: false,
  hasNextPage: false,
}));

vi.mock("./hooks", () => ({
  useBrowseObjects: () => ({ … }),
  // object-actions.tsx also imports from "./hooks" (same module id) — it must
  // be mocked here too or the mock factory below is incomplete.
  useDeleteObject: () => ({ mutate: vi.fn() }),
}));
```

Add cases:

8. Clicking the "Size" header reorders the rendered rows (assert on the order of
   rendered object names, via `screen.getAllByRole("row")`).
9. Clicking the same header twice reverses that order.
10. The header still declares exactly as many columns as a data row renders,
    **after** sorting — i.e. re-assert the plan-042 invariant in the sorted
    state, not just the initial one.
11. The partial-sort hint appears when `hasNextPage` is true and a non-default
    sort is active, and is **absent** when `hasNextPage` is false.

`src/pages/buckets/manage/browse/media-viewer.test.tsx` (new):

12. `classifyMedia` returns the right kind for `.png`, `.mp4`, `.mp3`, `.pdf`,
    `.txt`; **`null` for `.svg`**, `.zip`, `.docx`, and a name with no extension.
    (`.svg` is the one that matters — it is a security boundary, not a
    preference.)
13. Renders an `<img>` for an image item when open.
14. Shows the download fallback after the media element fires `onError`
    (`fireEvent.error(screen.getByRole("img"))`).
15. Prev/next are absent for a single item and present for two.

> jsdom has **no layout engine** — every rect is 0×0. These tests must assert on
> the DOM (which element rendered, in what order), never on measured geometry.
> Also stub nothing about `@floating-ui` — the viewer uses `Modal`, not a menu.
> jsdom does not implement media playback; do not assert that video plays.

**Verify**: `pnpm run test` → all pass.

### Step B5: Prove the tests can fail

Run each mutation, confirm the named test fails, then revert:

1. Make `sortObjects` sort in place (`objects.sort(...)`) → test 5 **must fail**.
2. Drop the null-guard on `size` → test 4 **must fail**.
3. Return `NaN` from the date comparator (remove the `NaN`→`0` mapping) → test 3
   or 4 **must fail**.
4. Make `classifyMedia` return `"image"` for `.svg` → test 12 **must fail**.
5. Add a sixth child to `Table.Head` → test 10 (and the pre-existing
   column-count test) **must fail**.

Report all five. Then confirm `git status --porcelain` shows no leftover
mutations before committing.

### Step B6: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
```

All three exit 0.

---

## Done criteria

- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run test` — all pass, including the three pre-existing `object-list.test.tsx` cases
- [ ] `pnpm run build` exits 0
- [ ] Step B5's five mutations each failed the named test, and were reverted
- [ ] `git diff --stat <BASE>..HEAD -- backend/` is **empty** (no backend change)
- [ ] `grep -rn "dangerouslySetInnerHTML" src/` → no matches
- [ ] `grep -c "<span" src/pages/buckets/manage/browse/object-list.tsx` — the
      `Table.Head` block still has exactly 5 direct children (read it and
      confirm by eye; the grep alone will not tell you)
- [ ] `grep -rn "z-layers\|Z_LAYERS" src/pages/buckets/manage/browse/media-viewer.tsx` → no matches
- [ ] `grep -n "svg" src/pages/buckets/manage/browse/media-viewer.tsx` → present, in the exclusion
- [ ] `git diff --stat <BASE>..HEAD` lists only the 7 in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You conclude a backend change is needed. It is not: every element the viewer
  uses is same-origin and already permitted by both CSPs. If a browser disagrees
  in the manual checks, report it — **relaxing `securityCSP` or
  `inlineSafeContentTypes` is a plan decision, not an executor decision.**
- You are about to add SVG to the previewable set, or use
  `dangerouslySetInnerHTML`, `<embed>`, or `<object>`.
- You are about to fetch all pages to make sorting global.
- You are about to change `Table.Head`'s child count, or restructure it to get
  `aria-sort` onto the `<th>`.
- You are about to edit `types.ts` to make `lastModified` a string. Correct in
  the abstract, out of scope here — it cascades into every `dayjs()` call site.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **`classifyMedia` is a mirror, not a source of truth.** The backend's
  `inlineSafeContentTypes` in `backend/router/browse.go` decides what actually
  renders. If someone adds a type there, add it here too — and if someone adds
  one *here* only, the result is a preview that always falls back. Both lists
  carry a comment pointing at the other; keep them.
- **SVG stays excluded on both sides.** It is XML that can carry `<script>`, and
  it would execute on the console's own origin. This is the single most
  likely "helpful" regression in this area.
- **The sort is client-side over loaded pages, and always will be** unless
  Garage grows a server-side sort — S3's `ListObjectsV2` has none. The hint in
  Step B3 is the honesty mechanism; do not delete it as clutter.
- **`Table.Head` renders one `<th>` per child** — the trap plan 042 fixed. Any
  future column work in this file starts by re-reading that comment.
- **`types.ts` still declares `lastModified: Date` and non-null `size` when the
  API sends strings and nulls.** The comparators work around it locally. A
  future plan should fix the type properly and update `dayjs()`/`readableBytes()`
  call sites together; until then, expect this trap in any new code that reads
  those fields.
- The viewer's 256 KB text cap is arbitrary but deliberate — raise it only with
  virtualised rendering, not by removing the cap.
