# Plan 048: Sanitise ZIP entry names — the archive can carry `../` out of the extraction directory

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- backend/router/browse.go backend/router/browse_test.go
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P1 (security)
- **Effort**: S
- **Risk**: LOW — pure-function change plus one call site; nothing about
  authentication, tokens or streaming changes.
- **Depends on**: nothing. `main` at `3c7b69b` (v3.5.1).
- **Category**: security / correctness
- **Planned at**: commit `3c7b69b`, 2026-08-11

---

## 1. The finding

`DownloadArchive` writes ZIP entry names straight from S3 object keys, unchecked:

```go
	entryNames := stripCommonKeyPrefix(dl.Keys)
	…
		entryWriter, err := zw.Create(entryNames[key])
```

and `stripCommonKeyPrefix` only trims a shared prefix — it never validates:

```go
func stripCommonKeyPrefix(keys []string) map[string]string {
	entries := make(map[string]string, len(keys))
	prefix := commonDirPrefix(keys)
	for _, k := range keys {
		name := strings.TrimPrefix(k, prefix)
		if name == "" {
			name = path.Base(k)
		}
		entries[k] = name
	}
	return entries
}
```

A repo-wide check confirms nothing else covers it:

```
$ grep -rn "\.\./\|filepath.Clean\|path.Clean\|IsAbs" backend/router/*.go   # excluding tests
(no output)
```

**Why this is exploitable.** S3 object keys are arbitrary UTF-8 strings. `../`,
a leading `/`, and backslashes are all legal in a key. Anyone who can write to
the bucket can create an object named `../../../../.ssh/authorized_keys` — and
that does **not** require an account on this console, because Garage's S3 API is
reachable independently with any key that has write access to the bucket.

The console lists that object like any other. An admin selects it (or uses
select-all) and downloads the ZIP. **We are the producer of a malicious
archive**, and the victim is our own operator: on extraction with any tool that
does not sanitise entry names, the file lands outside the target directory.

This is the classic zip-slip, inverted — we are not extracting an untrusted
archive, we are *building* one from untrusted names and handing it to a trusted
user. Modern GUI extractors and Info-ZIP's `unzip` mostly strip `../`, but many
library extractors do not, and we should not be relying on the victim's choice
of tool for our security.

**A second, smaller path to the same place**: `CreateDownloadToken` takes
`Keys` verbatim from the request JSON with no validation, so a caller can put
`../` in an entry name without any object existing. Lower severity (the token is
bound to the same bucket *and* username, so the caller is attacking their own
machine), but it is fixed by the same change.

**A third, related defect**: nothing prevents two different keys from producing
the **same** entry name. `a/../x.txt` and `x.txt` both sanitise to `x.txt`. A ZIP
with duplicate entries extracts by overwriting — an overwrite primitive inside
the target directory, and simple data loss even with benign keys. The fix must
de-duplicate.

## 2. Current state

### `backend/router/browse.go` — the call site

```go
	entryNames := stripCommonKeyPrefix(dl.Keys)

	// From here on the status is committed — see the function comment.
	zw := zip.NewWriter(w)

	var failures []string
	for _, key := range dl.Keys {
```

and later, inside that loop:

```go
		entryWriter, err := zw.Create(entryNames[key])
		if err != nil {
			obj.Body.Close()
			log.Printf("download archive: cannot create zip entry for %q: %v", key, err)
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
			continue
		}
```

and the existing trailing notes mechanism, which you will reuse:

```go
	if len(failures) > 0 {
		if entryWriter, err := zw.Create("DOWNLOAD-ERRORS.txt"); err == nil {
			body := "The following objects could not be added to this archive:\n\n" +
				strings.Join(failures, "\n") + "\n"
			io.WriteString(entryWriter, body)
		}
	}
```

### `backend/router/browse.go` — the helpers (both stay, unmodified)

`stripCommonKeyPrefix` is quoted in §1. It has four passing tests
(`TestStripCommonKeyPrefix`, `browse_test.go:587`) that **must keep passing
unchanged** — do not edit that test or the function.

```go
func commonDirPrefix(keys []string) string { … }   // leave alone
```

### `backend/router/browse_test.go` — the test exemplars

`TestStripCommonKeyPrefix` (line 587) is the shape for pure-helper tests:
sub-tests with `t.Run`, direct calls, `t.Errorf` with got/want.

Handler-level helpers already exist — `withDownloadSession` (line 624),
`mintDownloadToken` (line 638), `requestArchive` (line 661). Read them before
writing any handler test.

> **`DownloadArchive` cannot be tested end-to-end here.** Past the token checks
> it calls `getS3Client(bucket)`, which needs a live Garage cluster. The existing
> archive tests only cover paths that return *before* that call (missing token,
> expired token, wrong bucket/user). **Do not build an S3 fixture** — test the
> pure naming function exhaustively instead, and say so in NOTES.

## Conventions

- **Go handlers**: methods on empty structs, `(w, r)`, ending in
  `utils.ResponseSuccess` / `utils.ResponseError`. **`utils.ResponseError` does
  NOT stop the handler — always `return` after it.**
- Wrap errors with `fmt.Errorf("...: %w", err)`; log via stdlib `log`.
- Go tests: plain `testing`, table-driven or `t.Run` sub-tests. No new test deps.
- **`pnpm run lint` is expected to be red** (~55 pre-existing). Not relevant here
  — this plan is backend-only.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(Debian-based — `-race` needs gcc, which that image has; `git` is unusable inside it.)

The frontend is untouched by this plan; you do not need `pnpm`.

## Scope

**In scope:**
- `backend/router/browse.go` — new naming function + the one call site
- `backend/router/browse_test.go` — extend

**Out of scope — do NOT touch:**
- `stripCommonKeyPrefix`, `commonDirPrefix`, `commonStringPrefix` and their
  existing tests. The new function *calls* `stripCommonKeyPrefix`; it does not
  replace or modify it.
- `CreateDownloadToken` — do **not** add rejection there. See the STOP
  conditions: a `../` key can be a legitimate object that the operator is
  entitled to download, just not under a dangerous name. Sanitising always
  succeeds; rejecting would deny access to a real object.
- The token minting, TTL, single-use invalidation, or the bucket/username check.
- The streaming structure of `DownloadArchive`, its `failures` handling, its
  context-cancellation check, or the committed-status comment.
- `GetOneObject`, `PutObject`, the security headers, anything frontend.
- Any new dependency.

## Git workflow

- Branch: `advisor/048-archive-entry-name-traversal` from your given base.
- Conventional commit, e.g. `fix(security): sanitise zip entry names in the object archive`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: The sanitiser

Add to `backend/router/browse.go`, near `stripCommonKeyPrefix`:

```go
// safeZipEntryName converts a proposed archive entry name into one that can
// only ever extract *inside* the extraction directory.
//
// Object keys are arbitrary UTF-8 and are attacker-controlled: anyone with S3
// write access to the bucket — not necessarily a user of this console — can
// store an object literally named "../../../.ssh/authorized_keys". Writing that
// verbatim into a zip makes THIS SERVICE the producer of a zip-slip archive,
// with our own operator as the victim when they extract it. Some extractors
// strip such names; we do not get to depend on which tool the operator uses.
//
// The rules, in order:
//   - backslashes become forward slashes (Windows extractors treat "\" as a
//     separator, so "..\..\x" is the same attack in a different costume)
//   - a leading drive letter ("C:") or UNC prefix is dropped
//   - the path is split on "/" and every "", "." and ".." segment is discarded,
//     which also makes the result relative
//   - segments are rejoined with "/"
//
// Returns the safe name and whether anything was changed. An input that
// sanitises away to nothing returns ("", true); the caller substitutes a
// placeholder — see archiveEntryNames.
func safeZipEntryName(name string) (string, bool) { … }
```

**Implementation notes that matter:**

- **Do not use `path.Clean` as the mechanism.** `path.Clean("../a")` returns
  `"../a"` — it does not remove leading `..`, so it does not solve this problem.
  Split on `/` and filter segments explicitly.
- Drop the drive letter by checking whether the **first** segment ends in `:`
  (e.g. `C:`); do not try to be clever about Windows path grammar.
- Discarding `..` segments (rather than resolving them) is deliberate: resolving
  `a/../../b` could still escape depending on order, whereas discarding cannot.
  `a/../../b` therefore becomes `a/b` — safe, and it keeps the file discoverable
  rather than silently dropping it.
- The comparison for "changed" is simply `result != name`.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → no output, exit 0.

### Step 2: Compose naming, sanitising and de-duplication

Add, also near `stripCommonKeyPrefix`:

```go
// archiveEntryNames maps each object key to the name it will carry inside the
// archive: the existing common-prefix trim, then safeZipEntryName, then
// de-duplication.
//
// De-duplication is a security requirement, not tidiness. Two distinct keys can
// sanitise to the same name ("a/../x.txt" and "x.txt" both become "x.txt"), and
// a zip holding two entries with one name extracts by overwriting — an
// overwrite primitive inside the target directory, and plain data loss even
// for benign keys.
//
// Iterates `keys` in order so the result is deterministic; the returned
// `renamed` slice lists every key whose name had to change, for the archive's
// notes file.
func archiveEntryNames(keys []string) (names map[string]string, renamed []string) { … }
```

Behaviour:

1. `base := stripCommonKeyPrefix(keys)` — reuse it, do not reimplement.
2. For each key **in `keys` order**: sanitise `base[key]`.
3. If the sanitised name is empty, use `"unnamed"`.
4. If the name is already taken by an earlier key, append a counter **before the
   extension**: `x.txt` → `x (2).txt`, then `x (3).txt`. For a name with no
   extension, append at the end: `data` → `data (2)`. Use `path.Ext` to split.
   Keep incrementing until unused — a collision on the *suffixed* name is
   possible if a real object is literally called `x (2).txt`.
5. Record the key in `renamed` when the final name differs from `base[key]`.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./router/ -run "TestSafeZipEntryName|TestArchiveEntryNames"` → clean.

### Step 3: Use it, and tell the operator

In `DownloadArchive`, replace:

```go
	entryNames := stripCommonKeyPrefix(dl.Keys)
```

with:

```go
	entryNames, renamedEntries := archiveEntryNames(dl.Keys)
```

Leave the loop body's `zw.Create(entryNames[key])` exactly as it is.

Then extend the existing trailing-notes block so a rename is visible rather than
silent. Keep the current `DOWNLOAD-ERRORS.txt` behaviour untouched and add a
**separate** entry when `len(renamedEntries) > 0`:

- File name: `RENAMED-ENTRIES.txt`
- Body: a short explanation followed by the affected keys, e.g.
  ```
  These object keys contained path segments that are unsafe inside an archive
  (for example "..", a leading "/", or a drive letter), or collided with another
  entry. They were renamed so that extracting this archive cannot write outside
  the directory you extract it into. The objects themselves are unchanged.

  <one key per line>
  ```
- Same best-effort style as the errors file: `if entryWriter, err := zw.Create(...); err == nil { … }`, ignoring write errors, because the HTTP status is already committed.

**Do not** fail the request, change the status code, or skip a renamed object.
The status is committed before the first entry is written — see the comment
already in the function — so late failure is not available, and dropping the
object would lose data the operator asked for.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` → all `ok`.

### Step 4: Tests

Extend `backend/router/browse_test.go`. Do **not** modify `TestStripCommonKeyPrefix`.

**`TestSafeZipEntryName`** — sub-tests, each asserting both returned values:

| Input | Expected name | changed |
|---|---|---|
| `a/b.txt` | `a/b.txt` | false |
| `../../etc/passwd` | `etc/passwd` | true |
| `/etc/passwd` | `etc/passwd` | true |
| `a/../../b.txt` | `a/b.txt` | true |
| `..` | `""` | true |
| `../..` | `""` | true |
| `..\..\evil.exe` | `evil.exe` | true |
| `C:\Windows\x.txt` | `Windows/x.txt` | true |
| `\\server\share\x` | `server/share/x` | true |
| `a/./b.txt` | `a/b.txt` | true |
| `a//b.txt` | `a/b.txt` | true |
| `...hidden.txt` | `...hidden.txt` | false |

> The last row matters: only a segment that is **exactly** `..` is dangerous. A
> file legitimately named `...hidden.txt`, or `..bashrc`, must survive intact. A
> naive `strings.Contains(name, "..")` check would corrupt them — do not write
> one.

**`TestArchiveEntryNames`** — sub-tests:

1. **The security property, stated directly**: for a set including
   `../../etc/passwd`, `/abs/x`, `..\..\w.exe` and a normal key, **no** returned
   name begins with `/`, contains a `..` segment, or contains `\`. Assert this
   by splitting each name on `/` and checking segments — not with a substring
   match, so `...hidden.txt` does not trip it.
2. **Collisions get distinct names**: keys `x.txt` and `a/../x.txt` yield two
   different entry names, and the second is `x (2).txt`.
3. **All keys are present**: the returned map has one entry per input key, and
   no two keys share a name (build a `map[string]bool` of names and compare
   sizes).
4. **`renamed` lists exactly the keys whose name changed**, and is empty for a
   set of ordinary keys.
5. **Ordinary keys keep the existing behaviour**: `p/q/a.txt` + `p/q/b.txt`
   still yield `a.txt` and `b.txt`, proving the common-prefix trim still runs.
6. **A key that sanitises to nothing** (`../..`) gets the `unnamed` placeholder
   rather than an empty entry name — an empty name in a zip is malformed.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestSafeZipEntryName|TestArchiveEntryNames|TestStripCommonKeyPrefix"` → all `PASS`, including the four pre-existing `TestStripCommonKeyPrefix` sub-tests.

### Step 5: Prove the tests can fail

Run each mutation, confirm the named test fails, then revert:

1. Make `safeZipEntryName` return its input unchanged → `TestSafeZipEntryName`
   **and** `TestArchiveEntryNames`'s security sub-test **must both fail**.
2. Drop the de-duplication (let a later key overwrite an earlier name) →
   `TestArchiveEntryNames`'s collision sub-test **must fail**.
3. Filter segments with `strings.Contains(seg, "..")` instead of `seg == ".."` →
   the `...hidden.txt` row **must fail**. This pins the over-eager-filter
   mistake.

Report all three, then confirm `git status --porcelain` is clean before committing.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

### Step 7: Manual check — reviewer's job

You cannot reach a Garage cluster. Do **not** claim this passed; list it in NOTES:

1. Store an object with a key containing `../` (via the S3 API directly, since
   the UI will not create one), select it, download the archive, and confirm the
   entry name inside the zip is sanitised — e.g. `unzip -l` shows no `..`.
2. Confirm `RENAMED-ENTRIES.txt` appears in that archive and names the key.
3. Confirm an ordinary multi-file download is byte-for-byte unaffected.

## Done criteria

- [ ] Step 6 exits 0, all packages `ok`
- [ ] Step 5's three mutations each failed the named test, and were reverted
- [ ] `grep -n "zw.Create(entryNames\[key\])" backend/router/browse.go` → still exactly one match (the loop body was not restructured)
- [ ] `grep -c "stripCommonKeyPrefix" backend/router/browse.go` → **2** (its definition, and the call inside `archiveEntryNames`)
- [ ] `git diff <BASE>..HEAD -- backend/router/browse_test.go | grep -c "^-"` — the only removed lines are context/none inside `TestStripCommonKeyPrefix`; that test is unmodified
- [ ] `git diff --stat <BASE>..HEAD` lists exactly `backend/router/browse.go` and `backend/router/browse_test.go`
- [ ] `git diff <BASE>..HEAD -- src/ .github/ backend/middleware/` is **empty**

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You are about to reject keys in `CreateDownloadToken`. A `../` key may be a
  real object the operator is entitled to download; sanitising serves them,
  rejecting denies them. If you believe rejection is needed, report instead.
- You are about to change the streaming structure of `DownloadArchive`, or make
  it return an error status after `zip.NewWriter(w)` — the status is already
  committed at that point.
- You are about to modify `stripCommonKeyPrefix` or its tests.
- You are about to filter segments with a substring match on `..` rather than an
  exact `== ".."` comparison.
- You are about to build an S3 fixture or a live-cluster test to exercise
  `DownloadArchive` end-to-end. Test the pure functions; note the gap.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The threat is that we are the archive *producer*.** The usual zip-slip
  advice ("validate when extracting") does not apply — our output is consumed by
  someone else's extractor, so the entry names must be safe when they leave us.
  Any future code path that writes a zip from object keys needs the same
  treatment; `archiveEntryNames` is the one place to route it through.
- **Only an exact `..` segment is dangerous.** `..bashrc` and `...hidden.txt`
  are ordinary filenames and a substring check corrupts them. There is a test row
  for this precisely because it is the tempting wrong fix.
- **De-duplication is part of the security property**, not cosmetics: duplicate
  entry names extract by overwriting.
- **Sanitising never fails, and that is why it is safe here.** `DownloadArchive`
  commits its HTTP status before the first entry, so anything that could fail
  late would have no way to report. Renames are surfaced in
  `RENAMED-ENTRIES.txt` instead.
- `CreateDownloadToken` still accepts arbitrary keys by design; the safety
  boundary is at archive construction, one place, rather than spread across
  every producer of a key list.
