# Plan 047: Replace row thumbnails with generic per-type icons

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- src/pages/buckets/manage/browse/object-list.tsx
> ```
> Then confirm the "Current state" excerpt matches. On a mismatch, STOP.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW (frontend only, deletes a network path rather than adding one)
- **Depends on**: nothing unmerged. `main` at `4b3d529` has 045 and 046.
- **Category**: UX / performance
- **Planned at**: commit `4b3d529`, 2026-08-11

## Why

The maintainer's instruction, verbatim: *"its not icons its thumbnails i did not
ask for thumbnails just make it generic thumbnail for media like video will have
a clapperboard, image will have a film strip, pdf will have a document and so
on."*

So real image thumbnails go away entirely and every row gets a **generic icon
chosen by file type**. That is a deletion, and it removes three defects with it:

1. **Full files are downloaded to draw a 20-pixel icon.** The current code only
   requests a real thumbnail (`?thumb=1`) for `jpg/jpeg/png/gif`. Every other
   image type falls through to `?view=1`, which streams the **entire original**
   and scales it in CSS. The backend cannot do better —
   `backend/utils/image.go` registers only `image/gif`, `image/jpeg` and
   `image/png` decoders — so `webp`, `avif`, `bmp` and `ico` each cost their
   full size per row. A folder of 10 MB `.webp` files downloads 10 MB per row.
2. **`.svg` row icons are broken as of v3.5.0.** An SVG takes the image branch,
   gets `?view=1`, and plan 043 now serves SVG as `application/octet-stream`
   with an attachment disposition — so the `<img>` fails and renders a broken
   glyph.
3. **Every non-image type collapses to one generic document icon**, so nothing
   distinguishes a video from a spreadsheet in the list.

There is also an unexplained report of two `.pdf` rows rendering a collapsed
`<img>`-like mark while a third `.pdf` rendered normally. **Do not investigate
it.** This plan deletes the `<img>` path entirely, so the symptom cannot survive
regardless of its cause.

## Current state

`src/pages/buckets/manage/browse/object-list.tsx`, bottom of the file — the
whole component being replaced:

```tsx
type FilePreviewProps = {
  ext?: string | null;
  object: Object;
};

const FilePreview = ({ ext, object }: FilePreviewProps) => {
  const type = mime.getType(ext || "")?.split("/")[0];
  let Icon = FileIcon;

  if (
    ["zip", "rar", "7z", "iso", "tar", "gz", "bz2", "xz"].includes(ext || "")
  ) {
    Icon = FileArchive;
  }

  if (type === "image") {
    const thumbnailSupport = ["jpg", "jpeg", "png", "gif"].includes(ext || "");
    // object.url arrives percent-encoded from the API; do not re-encode.
    return (
      <img
        src={API_URL + object.url + (thumbnailSupport ? "?thumb=1" : "?view=1")}
        alt={object.objectKey}
        className="size-5 object-cover overflow-hidden mr-2"
      />
    );
  }

  if (type === "text") {
    Icon = FileType;
  }

  return (
    <Icon
      size={20}
      className="text-base-content/60 group-hover:text-neutral-content/80 mr-2"
    />
  );
};
```

Its call site, inside the object row:

```tsx
                  <span className="flex items-center font-normal w-full min-w-0">
                    <FilePreview ext={ext?.substring(1)} object={object} />
                    <span className="truncate min-w-0">{filename}</span>
```

and the `ext` it is given, computed just above:

```tsx
            const extIdx = object.objectKey.lastIndexOf(".");
            const filename =
              extIdx >= 0
                ? object.objectKey.substring(0, extIdx)
                : object.objectKey;
            const ext = extIdx >= 0 ? object.objectKey.substring(extIdx) : null;
```

## Conventions

- Pure, exported, unit-testable helpers in their own module — follow
  `src/pages/buckets/manage/browse/sorting.ts` (created by plan 045) and
  `compareByFirstAlias` in `src/pages/buckets/page.tsx`.
- Components are arrow functions with `type Props = {...}` above and
  `export default Name` at the bottom.
- Icons from `lucide-react`; `@/` aliases `src/`.
- Tests: `@testing-library/react` + `vitest`, `vi.hoisted` for mutable mock
  state. `object-list.test.tsx` is the exemplar.
- **`pnpm run lint` is expected to be red** (~55 pre-existing). New code clean.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| One file | `pnpm exec vitest run <pattern>` | — |
| Build | `pnpm run build` | exit 0 |

`pnpm` is at `/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` —
prepend that directory. Do not substitute `npm`.

## Scope

**In scope:**
- `src/pages/buckets/manage/browse/file-icons.ts` (new)
- `src/pages/buckets/manage/browse/file-icons.test.ts` (new)
- `src/pages/buckets/manage/browse/object-list.tsx` (modify — `FilePreview` only)
- `src/pages/buckets/manage/browse/object-list.test.tsx` (extend)

**Out of scope — do NOT touch:**
- **Any Go file.** This plan removes the UI's only caller of `?thumb=1`, but the
  endpoint and `backend/utils/image.go` stay exactly as they are. Deleting a
  shipped endpoint is a separate decision, not a side effect of an icon change.
- `media-viewer.tsx` and `classifyMedia`. That decides what the **viewer** can
  render and mirrors a backend security allowlist. This plan decides what
  **icon** a row shows. They are different questions with different
  consequences, and the icon map is allowed to cover types the viewer refuses
  (an `.svg` gets an image icon and still downloads when clicked). **Do not
  merge the two lists.**
- `sorting.ts`, the sort headers, the partial-sort hint, the row click handler,
  the selection checkboxes, `ObjectActions`.
- The `Table.Head` block. It must keep **exactly five children** — see the
  comment in it; three tests guard this.
- Any new dependency.

## Git workflow

- Branch: `advisor/047-generic-file-type-icons` from your given base.
- Conventional commit, e.g. `feat: show a generic per-type icon for each object`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: The icon map

Create `src/pages/buckets/manage/browse/file-icons.ts`.

```ts
import {
  Clapperboard,
  FileArchive,
  FileAudio,
  FileCode,
  FileIcon,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileType,
  Presentation,
  type LucideIcon,
} from "lucide-react";

/**
 * Pick a generic icon for an object, from its file extension.
 *
 * Deliberately NOT a thumbnail: the previous implementation fetched the object
 * itself to draw a 20px row icon, which cost the full file for every image the
 * backend could not thumbnail (it decodes only gif/jpeg/png), and broke
 * outright for .svg once object bodies started being served as attachments.
 *
 * This is also NOT classifyMedia() in media-viewer.tsx. That mirrors a backend
 * security allowlist and decides what may be RENDERED; this only decides which
 * glyph to draw, so it may cover types the viewer refuses. Keep them separate.
 */
export function iconForObjectKey(objectKey: string): LucideIcon { … }
```

Extension → icon, matched **case-insensitively** on the substring after the last
`.` (no extension, or a trailing `.`, → the default):

| Icon | Extensions |
|---|---|
| `FileImage` | `png jpg jpeg gif webp avif bmp ico svg tif tiff heic` |
| `Clapperboard` | `mp4 webm mkv mov avi m4v mpg mpeg wmv` |
| `FileAudio` | `mp3 wav ogg oga m4a flac aac opus` |
| `FileText` | `pdf` |
| `FileSpreadsheet` | `xlsx xls csv tsv ods` |
| `Presentation` | `ppt pptx odp` |
| `FileArchive` | `zip rar 7z iso tar gz bz2 xz` |
| `FileCode` | `js jsx ts tsx json go py rb sh bash yml yaml toml html css scss sql` |
| `FileType` | `txt md markdown rst log doc docx odt rtf` |
| `FileIcon` | everything else (the default) |

Implement as a single `Record<string, LucideIcon>` lookup built from those
groups — **not** a chain of `if` statements, and **not** `mime.getType`.
Extension-driven is the point: `mime/lite` has known gaps (it does not know
`.ico`, documented in `backend/router/browse.go`'s `resolveUploadContentType`)
and going through MIME is what produced the current `type.split("/")[0]`
brittleness.

Do not import `mime/lite` in this file at all.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 2: Use it, and delete the `<img>` path

In `object-list.tsx`, replace the whole `FilePreview` component with:

```tsx
type FilePreviewProps = {
  objectKey: string;
};

const FilePreview = ({ objectKey }: FilePreviewProps) => {
  const Icon = iconForObjectKey(objectKey);
  return (
    <Icon
      size={20}
      className="text-base-content/60 group-hover:text-neutral-content/80 mr-2 shrink-0"
    />
  );
};
```

Note `shrink-0` is added: the old `<img>` had a fixed size, a lucide icon in a
flex row can otherwise be squeezed by a long filename.

Update the call site to `<FilePreview objectKey={object.objectKey} />` — it no
longer needs `ext` or the whole `object`.

Then **remove every import that is now unused**: `mime` from `mime/lite`, and
whichever of `FileArchive` / `FileIcon` / `FileType` are no longer referenced in
this file. Leave `API_URL` alone — the row click handler and `ObjectActions`
still use it.

> `ext` and `filename` are still computed above and still used to render the
> name in two spans (`{filename}` and `{ext}`). **Do not delete them** — only
> `FilePreview` stops consuming `ext`.

**Verify**:
```
pnpm run typecheck && pnpm run build
grep -c "img" src/pages/buckets/manage/browse/object-list.tsx
```
→ typecheck and build exit 0; the grep → **0**.

### Step 3: Tests

`src/pages/buckets/manage/browse/file-icons.test.ts` (new, pure):

1. Each of the ten icon groups gets one representative extension asserted to the
   right icon (e.g. `iconForObjectKey("a.mp4")` → `Clapperboard`). Compare
   against the imported icon component by identity, not by name string.
2. **Case-insensitive**: `"REPORT.PDF"` → `FileText`, `"clip.MP4"` →
   `Clapperboard`.
3. **No extension** (`"README"`), **trailing dot** (`"weird."`), and an
   **unknown extension** (`"a.qqq"`) all → the default `FileIcon`.
4. **A dotted path is handled by the last segment only**: `"a.b.tar.gz"` → the
   `gz` icon (`FileArchive`), and `"my.folder/file.png"` → `FileImage`.
5. **`.svg` gets `FileImage`** — and add a comment on this case noting the icon
   map intentionally covers a type the media viewer refuses to render.

`src/pages/buckets/manage/browse/object-list.test.tsx` (extend — do **not**
create a second file):

6. **No `<img>` is rendered for an image object.** Add an object with a `.png`
   key and assert `container.querySelector("img")` is `null`. This is the
   regression guard for the whole plan — if it is missing, the plan is not done.
7. The existing column-count invariant still holds (the three pre-existing cases
   must pass unchanged — do not edit them).

**Verify**: `pnpm run test` → all pass.

### Step 4: Prove the tests can fail

Run each mutation, confirm the named test fails, then revert:

1. Make `iconForObjectKey` return `FileIcon` unconditionally → several cases in
   file-icons.test.ts **must fail**.
2. Lowercase-match only (drop the case normalisation) → test 2 **must fail**.
3. Restore the `<img>` branch for images in `FilePreview` → test 6 **must
   fail**.

Report all three, then confirm `git status --porcelain` is clean before
committing.

### Step 5: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
```

All three exit 0.

### Step 6: Manual check — reviewer's job

You have no browser. Do **not** claim this passed; list it in NOTES:

1. Every row shows a type-appropriate icon: clapperboard for `.mp4`, image glyph
   for `.jpg`/`.png`/`.svg`, document for `.pdf`, spreadsheet for `.xlsx`,
   archive for `.zip`.
2. **No network request is made per row on folder load** — the Network tab
   should show no `?thumb=1` or `?view=1` request while merely listing. This is
   the performance win; confirm it is real.
3. Long filenames still truncate without pushing the icon out of the row.

## Done criteria

- [ ] All gates in Step 5 exit 0
- [ ] Step 4's three mutations each failed the named test, and were reverted
- [ ] `grep -c "img" src/pages/buckets/manage/browse/object-list.tsx` → **0**
- [ ] `grep -c "thumb=1" src/` → **0**
- [ ] `grep -c "mime/lite" src/pages/buckets/manage/browse/object-list.tsx` → **0**
- [ ] `grep -c "mime" src/pages/buckets/manage/browse/file-icons.ts` → **0**
- [ ] `git diff --stat <BASE>..HEAD -- backend/` is **empty**
- [ ] `git diff --stat <BASE>..HEAD -- src/pages/buckets/manage/browse/media-viewer.tsx src/pages/buckets/manage/browse/sorting.ts` is **empty**
- [ ] `git diff --stat <BASE>..HEAD` lists only the 4 in-scope files

## STOP conditions

- The "Current state" excerpt does not match — the branch drifted.
- You are about to change `classifyMedia` or `media-viewer.tsx` to share a list
  with the icon map. They answer different questions; one is a security
  boundary.
- You are about to delete the backend `?thumb=1` endpoint or
  `backend/utils/image.go`.
- You are about to change `Table.Head`'s child count.
- You are about to use `mime.getType` for the icon decision.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The icon map and `classifyMedia` are deliberately separate lists.**
  `classifyMedia` mirrors the backend's `inlineSafeContentTypes` and gates what
  gets rendered on the console's origin — a security boundary. The icon map only
  picks a glyph. Merging them would let a cosmetic edit widen a security
  allowlist, which is precisely the mistake to design against. Both files carry
  a comment saying so.
- **`.svg` shows an image icon but still downloads when clicked.** That is
  correct and intentional, not an inconsistency to "fix".
- **The `?thumb=1` endpoint now has no caller in the UI.** It still works and is
  still tested. If it is ever removed, `backend/utils/image.go` and its resize
  dependency go with it — check for external callers first, since it is a public
  route on an authenticated API.
- Extension-driven, not MIME-driven, on purpose: `mime/lite` does not know
  `.ico` and has gaps for `.avif`/`.heic`, and the previous
  `mime.getType(ext)?.split("/")[0]` shape is what made the old component
  fragile.
