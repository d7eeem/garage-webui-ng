# Plan 053: Delete the thumbnail endpoint — two unbounded memory paths on dead code

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**:
> ```
> git diff --stat 947879d..HEAD -- backend/router/browse.go backend/utils/image.go backend/go.mod src/
> ```
> On a mismatch with the "Current state" excerpts, STOP.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW — removes a route the UI no longer calls
- **Depends on**: none
- **Category**: security
- **Planned at**: commit `947879d`, 2026-08-13

## Why this matters

`GET /browse/{bucket}/{key...}?thumb=1` contains **two independent
memory-exhaustion paths** in a process that holds a cluster-wide Garage admin
token:

1. `io.ReadAll(object.Body)` with no size limit — the whole object is buffered
   per concurrent request.
2. `image.Decode` with no dimension check — allocation is driven by the image
   header's *declared* size, not the file size. A ~1 MB PNG declaring
   30000×30000 decodes to roughly a 3.6 GB RGBA buffer.

The second is the nastier one, and capping the download does not fix it. Both are
reachable by the **read-only viewer role** (it is a GET), and the object bytes are
chosen by anyone with S3 write access to the bucket — not only console users.

**And nothing calls it.** Row thumbnails were deliberately replaced with generic
per-type icons; the frontend no longer sets `thumb=1` anywhere. So the right fix
is deletion, not bounding: bounding maintains dead code, while deleting removes
the exposure, ~130 lines, and a dependency.

Note the contrast in the same file: the upload path is carefully bounded with
`http.MaxBytesReader`. The read path simply never got the same treatment.

## Current state

### `backend/router/browse.go:143-161` — the branch to remove

```go
	} else if thumbnail {
		body, err := io.ReadAll(object.Body)
		if err != nil {
			utils.ResponseError(w, err)
			return
		}

		thumb, err := utils.CreateThumbnailImage(body, 64, 64)
		if err != nil {

			utils.ResponseError(w, err)
			return
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumb)
		return
	}
```

The `thumbnail` variable is parsed near the top of `GetOneObject`:

```go
	thumbnail := queryParams.Get("thumb") == "1"
```

and participates in the guard that decides whether to fetch the object at all:

```go
	if !view && !download && !thumbnail {
		// … HEAD path, returns object metadata as JSON
	}
```

> **That guard is the one place a careless deletion breaks things.** Removing
> `thumbnail` from it changes which requests take the metadata path. Keep the
> guard's behaviour for `view` and `download` exactly as it is.

### `backend/utils/image.go` — the whole file, to be deleted

```go
package utils

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"github.com/nfnt/resize"
)

// CreateThumbnailImage decodes an image (PNG, JPEG or GIF) and returns a
// thumbnail of at most width x height, encoded as JPEG bytes.
func CreateThumbnailImage(buffer []byte, width uint, height uint) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(buffer))
	if err != nil {
		return nil, err
	}

	thumb := resize.Thumbnail(width, height, img, resize.NearestNeighbor)
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, thumb, nil); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
```

`github.com/nfnt/resize` is imported **only** here.

### Proof the UI does not use it

```
$ grep -rn "thumb" src/
src/pages/buckets/manage/hooks.ts:84:   (a comment only — no code sets thumb=1)
```

## Commands you will need

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |
| Frontend gates | `pnpm run typecheck && pnpm run test && pnpm run build` | all exit 0 |

`pnpm` is at `/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend to PATH.
If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

## Scope

**In scope:**
- `backend/router/browse.go` — remove the `thumbnail` branch and its query parse
- `backend/router/browse_test.go` — remove or adjust any test referencing `thumb`
- `backend/utils/image.go` — delete
- `backend/utils/image_test.go` — delete
- `backend/go.mod` / `backend/go.sum` — drop `nfnt/resize` via `go mod tidy`
- `README.md` — only if it documents the `thumb` parameter

**Out of scope — do NOT touch:**
- The `view` and `download` branches, the inline-safe content-type allowlist,
  `isInlineSafe`, `objectViewCSP`, the frame headers, or anything else about how
  object bodies are served. This plan removes one branch; it changes nothing
  about the other two.
- The HEAD/metadata path's behaviour for `view` and `download`.
- `src/` — the frontend already does not call this. If you find code that does,
  that is a STOP condition, not something to edit.
- Any other dependency in `go.mod`.

## Git workflow

- Branch: `advisor/053-delete-thumbnail-endpoint` from your given base.
- Conventional commit, e.g. `fix(security): remove the unbounded thumbnail endpoint`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Confirm the endpoint is genuinely unused before deleting anything

```
grep -rn "thumb" src/
grep -rn "thumb=1\|thumbnail" README.md docs/
grep -rn "CreateThumbnailImage" backend/
```

Expected: `src/` yields **only** a comment (no code constructing `thumb=1`);
`CreateThumbnailImage` appears only in `browse.go`, `utils/image.go` and
`utils/image_test.go`.

> **If any frontend code actually sets `thumb=1`, STOP and report.** The premise
> of this plan is that the route is dead; if it is not, the correct fix is
> bounding rather than deletion and that is a different plan.

### Step 2: Remove the branch

In `backend/router/browse.go`:

- Delete the entire `} else if thumbnail { … }` block.
- Delete the `thumbnail := queryParams.Get("thumb") == "1"` line.
- Update the `if !view && !download && !thumbnail` guard to `if !view && !download`.
- Remove the `io` import **only if** nothing else in the file still uses it
  (`io.Copy` is used further down, so it almost certainly stays — check, do not
  assume).

Leave the `download` branch and everything below the guard untouched.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./...
grep -c "thumb" router/browse.go
```
→ no gofmt/vet output, exit 0; the grep returns **0**.

### Step 3: Delete the image helper and its dependency

- `git rm backend/utils/image.go backend/utils/image_test.go`
- `cd backend && go mod tidy`

**Verify**:
```
cd backend && go build ./... && grep -c "nfnt/resize" go.mod
```
→ exit 0; the grep returns **0**.

> If `go mod tidy` removes anything **other** than `nfnt/resize`, STOP and report
> what it touched. A tidy that prunes unrelated modules means the module graph
> was already inconsistent and that is worth a human look.

### Step 4: Adjust tests that referenced the removed path

Search `backend/router/browse_test.go` for `thumb`. If a test exercises the
thumbnail branch, delete that test — do not rewrite it to assert a 404, because
the route still exists for `view`/`download` and a `?thumb=1` request now simply
falls through to the ordinary view path.

Add one small test to `browse_test.go` pinning the new behaviour: a request with
**no** `view`, `download` or `dl` parameter still takes the metadata path (the
guard change is the only behavioural risk in this plan). Model it on the existing
table tests in that file.

**Verify**: `cd backend && go test -race ./...` → all packages `ok`.

### Step 5: Documentation

If `README.md` or anything under `docs/` documents a `thumb` query parameter,
remove it. If nothing does, say so in NOTES and change nothing.

**Verify**: `grep -rn "thumb" README.md docs/` → no matches.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
pnpm run typecheck && pnpm run test && pnpm run build
```

The frontend gates should be unaffected — run them to prove it.

## Done criteria

- [ ] Step 6 passes; all Go packages `ok`; frontend typecheck/test/build exit 0
- [ ] `grep -rn "thumb" backend/ src/ README.md docs/` → no matches in code (a historical comment in `src/pages/buckets/manage/hooks.ts` may remain; note it if so)
- [ ] `ls backend/utils/image.go backend/utils/image_test.go` → both absent
- [ ] `grep -c "nfnt/resize" backend/go.mod backend/go.sum` → **0** in both
- [ ] `grep -n "if !view && !download" backend/router/browse.go` → exactly one match, with no `thumbnail` term
- [ ] `git diff 947879d..HEAD -- src/ backend/middleware/` is **empty**
- [ ] `git diff --stat 947879d..HEAD` lists only in-scope files

## STOP conditions

- Frontend code is found that actually sets `thumb=1` — the deletion premise is
  wrong; report instead.
- `go mod tidy` removes a module other than `nfnt/resize`.
- You are about to change the `view` or `download` branch, or any header policy.
- You are about to keep `CreateThumbnailImage` "just in case" — either it goes or
  this plan is not done.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **If thumbnails are ever wanted again**, they must be generated with both
  bounds this code lacked: an `io.LimitReader` on the download *and* an
  `image.DecodeConfig` pixel-count check before decoding. Capping only the byte
  size leaves the decode bomb fully intact — that was the whole point of removing
  this rather than patching it.
- Server-side thumbnailing for an object store is arguably the wrong shape
  anyway; if the need returns, generating on upload and storing the result beside
  the object avoids decoding attacker-controlled bytes on demand.
- A reviewer should confirm one thing: the `if !view && !download` guard still
  routes metadata requests the same way it did before.
