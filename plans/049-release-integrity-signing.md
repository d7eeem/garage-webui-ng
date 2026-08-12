# Plan 049: Sign releases — checksums + ed25519 signature

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`.
>
> **NEVER generate, print, paste, or commit a private key.** This plan builds
> the *tooling*; the maintainer generates the keypair themselves (Step 5) and
> puts the private half in a GitHub secret. If you find yourself about to run a
> keygen command and record its output anywhere, STOP.
>
> **Drift check (run first)**:
> ```
> git diff --stat <BASE> -- .github/workflows/release.yml backend/
> ```
> Then confirm the "Current state" excerpt matches. On a mismatch, STOP.

## Status

- **Priority**: P1 — hard prerequisite for plan 050 (in-browser update)
- **Effort**: M
- **Risk**: MED — touches the release pipeline. A mistake here breaks releases,
  though it cannot break a running deployment.
- **Depends on**: nothing.
- **Blocks**: **plan 050 cannot be executed until this ships and a real key is
  configured.**
- **Category**: security / supply chain
- **Planned at**: commit `9858186` (v3.6.0), 2026-08-11

---

## 1. Why this exists

The maintainer asked for an in-browser "update now" button. That feature
downloads an executable from the internet and runs it **in a process holding the
Garage admin token** — so whoever can influence what gets downloaded owns the
cluster, not just the console.

Today nothing stands behind a release artifact:

```
$ grep -icE "sha256|checksum|cosign|sign|attest|provenance" .github/workflows/release.yml
0
```

`release.yml` attaches raw binaries via `softprops/action-gh-release` with no
checksum, no signature, no attestation. An updater built on that would trust
GitHub's account security and TLS alone, with no way to detect a swapped asset.

**This plan makes a release provably ours.** Plan 050 then refuses to install
anything that does not verify.

**Why ed25519 and not cosign/sigstore:** verification has to happen inside this
binary. `crypto/ed25519` and `crypto/sha256` are **Go standard library** — zero
new dependencies, no network calls to Fulcio/Rekor at verify time, and the
public key ships inside the binary being updated. Sigstore is a better fit for
ecosystems with tooling already present; here it would add a large dependency
tree to a `CGO_ENABLED=0` binary for no gain.

## 2. Current state

`.github/workflows/release.yml` — the matrix job, in full for the parts that matter:

```yaml
jobs:
  binaries:
    name: Build release binaries
    runs-on: ubuntu-latest
    strategy:
      matrix:
        goarch: [amd64, arm64]
    steps:
      - uses: actions/checkout@v4
      …
      - name: Build binary (linux/${{ matrix.goarch }})
        working-directory: backend
        run: |
          rm -rf ui/dist && cp -r ../dist ui/dist
          CGO_ENABLED=0 GOOS=linux GOARCH=${{ matrix.goarch }} \
            go build -tags=prod -trimpath -ldflags="-s -w -X main.version=${{ github.ref_name }}" \
            -o "garage-webui-ng-linux-${{ matrix.goarch }}" .

      - name: Attach binary to the release
        uses: softprops/action-gh-release@v2
        with:
          files: backend/garage-webui-ng-linux-${{ matrix.goarch }}
          generate_release_notes: true
```

> **The matrix is the structural problem.** Each arch runs in its own job and
> attaches its own file, so no single job ever sees both binaries. A checksums
> file covering the whole release therefore needs a **third job** that runs after
> both and has both artifacts in hand. Do not try to produce SHA256SUMS inside
> the matrix — you would get two files racing for the same name.

`backend/version.go` is `package main` (`var version = "dev"`, injected via
ldflags). Note that: a `package main` symbol cannot be imported by
`backend/router`, which is why plan 041 had to inject `AppVersion` from
`main.go`. The same constraint applies to the public key.

## Conventions

- Go: stdlib-first, no new dependencies. Errors wrapped with
  `fmt.Errorf("...: %w", err)`. Tests are table-driven `testing`, no framework.
- The Go module lives in `backend/` (module `github.com/d7eeem/garage-webui-ng`).
  A second `package main` under `backend/cmd/<name>/` is fine and is built by
  `go build ./...`.
- CI pins Go to an **exact** patch (`1.25.12`) in four places that must stay in
  lockstep: `.github/workflows/ci.yml` ×2, `release.yml`, and the `Dockerfile`.
  If you add a job that needs Go, use that same version string.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |
| Workflow lint | `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml'))"` | no output |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

The frontend is untouched.

## Scope

**In scope:**
- `backend/cmd/relsign/main.go` (new) — keygen / sign / verify tool
- `backend/cmd/relsign/main_test.go` (new)
- `backend/release_key.go` (new) — the embedded public key + its accessor
- `backend/release_key_test.go` (new)
- `.github/workflows/release.yml` — add the signing job
- `README.md` — a "Verifying a release" section

**Out of scope — do NOT touch:**
- `backend/version.go`, the ldflags wiring, the tag-vs-`package.json` guard.
- `backend/router/` — **nothing in this plan consumes the key yet.** Plan 050
  does that. If you find yourself adding an update endpoint, STOP: wrong plan.
- `Dockerfile`, `ci.yml`, `docker-publish.yml`.
- Anything under `src/`.
- Any new dependency, in Go or in the workflow (no cosign action, no gpg).

## Git workflow

- Branch: `advisor/049-release-integrity-signing` from your given base.
- Conventional commits, e.g. `feat(release): publish signed checksums`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: The `relsign` tool

Create `backend/cmd/relsign/main.go` — one `package main`, stdlib only
(`crypto/ed25519`, `crypto/rand`, `crypto/sha256`, `encoding/hex`, `flag`,
`os`, `bufio`). Three subcommands:

```
relsign keygen
    Generates an ed25519 keypair. Writes the PRIVATE key (hex) to stdout and
    the PUBLIC key (hex) to stderr, each on one line, clearly labelled.

relsign sign -key-env RELEASE_SIGNING_KEY -in SHA256SUMS -out SHA256SUMS.sig
    Reads the hex private key from the named ENVIRONMENT VARIABLE (never a
    flag — a flag value lands in the process table and CI logs), signs the
    file's exact bytes, writes the hex signature.

relsign verify -pub <hex> -in SHA256SUMS -sig SHA256SUMS.sig
    Exit 0 if the signature is valid for that public key, non-zero otherwise.
```

Requirements:

- **`sign` reads the key only from the environment.** Reject a key passed any
  other way. Do not log the key, its length, or any prefix of it.
- The signed payload is the **raw bytes of the file**, not a re-serialisation —
  so verification cannot disagree about formatting.
- `verify` must return a non-zero exit code on *any* failure (bad hex, wrong
  length, bad signature), and print a short reason to stderr.
- Reject a public or private key whose decoded length is not
  `ed25519.PublicKeySize` / `ed25519.PrivateKeySize`, with a clear message.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → clean.

### Step 2: The embedded public key

Create `backend/release_key.go`:

```go
package main

// releasePublicKey is the hex-encoded ed25519 public key that release
// artifacts are signed with. The matching private key lives only in the
// repository's RELEASE_SIGNING_KEY GitHub secret and must never appear here or
// anywhere else in this tree.
//
// EMPTY MEANS UNCONFIGURED, AND UNCONFIGURED MUST FAIL CLOSED: any feature that
// installs a downloaded artifact has to refuse to run when this is empty,
// rather than falling back to "no verification". See plan 050.
//
// To configure: run `go run ./cmd/relsign keygen`, put the private key in the
// GitHub secret, and paste the public key here.
var releasePublicKey = ""

// ReleasePublicKey returns the configured release-signing public key, or "" if
// this build has none.
func ReleasePublicKey() string { return releasePublicKey }
```

**Leave it empty.** You do not have a keypair and must not generate one — the
maintainer does that in Step 5. An empty value is the correct, safe committed
state.

Add `backend/release_key_test.go` asserting:
1. `ReleasePublicKey()` returns whatever `releasePublicKey` holds (a trivial
   accessor test that will catch someone wiring it to the wrong variable).
2. **If** it is non-empty, it decodes as hex to exactly `ed25519.PublicKeySize`
   bytes. Skip with `t.Skip` when empty, so the test is meaningful the moment a
   real key lands and passes harmlessly until then.

> A test that a *committed* key is absent would be wrong — the whole point is
> that a real public key eventually lives here. It is public; it is not a
> secret. Only the private half is.

**Verify**: `cd backend && go test -race ./... -run TestReleasePublicKey -v` → `PASS` (with the length case skipped).

### Step 3: Tests for the tool

`backend/cmd/relsign/main_test.go`, table-driven where it fits:

1. **Round trip**: generate a keypair in-test (`ed25519.GenerateKey`), sign a
   temp file's bytes, verify → valid.
2. **Tampered payload fails**: sign, then flip one byte of the file, verify →
   invalid.
3. **Tampered signature fails**: sign, flip one byte of the signature hex,
   verify → invalid.
4. **Wrong key fails**: sign with key A, verify with key B's public half →
   invalid.
5. **Malformed inputs are rejected** with an error, not a panic: non-hex key,
   correct-hex-but-wrong-length key, empty signature file.

Use `t.TempDir()` for files and `t.Setenv` for the key env var. Keys generated
inside a test are ephemeral and fine; **do not write any generated private key
to a file that is not under `t.TempDir()`**.

**Verify**: `cd backend && go test -race ./cmd/relsign/ -v` → all `PASS`.

### Step 4: The release workflow

Restructure `.github/workflows/release.yml`:

**(a) The `binaries` matrix job** stops attaching to the release. Instead it
uploads its binary as a workflow artifact:

```yaml
      - uses: actions/upload-artifact@v4
        with:
          name: binary-${{ matrix.goarch }}
          path: backend/garage-webui-ng-linux-${{ matrix.goarch }}
          if-no-files-found: error
          retention-days: 1
```

Keep every existing step in that job unchanged — the pnpm setup, the
tag-vs-`package.json` guard, the pinned Go version, the build flags.

**(b) A new `release` job**, `needs: binaries`:

```yaml
  release:
    name: Checksum, sign and publish
    needs: binaries
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.25.12"
          cache-dependency-path: backend/go.sum
      - uses: actions/download-artifact@v4
        with:
          path: dist-artifacts
          merge-multiple: true
      - name: Generate SHA256SUMS
        run: |
          cd dist-artifacts
          sha256sum garage-webui-ng-linux-* > SHA256SUMS
          cat SHA256SUMS
      - name: Sign SHA256SUMS
        env:
          RELEASE_SIGNING_KEY: ${{ secrets.RELEASE_SIGNING_KEY }}
        run: |
          if [ -z "$RELEASE_SIGNING_KEY" ]; then
            echo "::error::RELEASE_SIGNING_KEY is not set — releases must be signed"
            exit 1
          fi
          cd backend
          go run ./cmd/relsign sign \
            -key-env RELEASE_SIGNING_KEY \
            -in ../dist-artifacts/SHA256SUMS \
            -out ../dist-artifacts/SHA256SUMS.sig
      - name: Attach everything to the release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            dist-artifacts/garage-webui-ng-linux-*
            dist-artifacts/SHA256SUMS
            dist-artifacts/SHA256SUMS.sig
          generate_release_notes: true
```

Three things that matter:

- **The job fails loudly when the secret is missing.** An unsigned release is
  worse than a failed one, because plan 050 would then have nothing to check
  and an operator would assume the release is fine.
- `sha256sum` output lists **bare file names** (because of the `cd`), so the
  file the updater downloads matches the name in SHA256SUMS exactly. Do not
  produce paths.
- `generate_release_notes: true` stays only so the release is created; the
  maintainer replaces the body afterwards.

**Verify**:
```
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"
grep -c "upload-artifact\|download-artifact\|relsign sign" .github/workflows/release.yml
```
→ no YAML error; the grep finds all three.

> You cannot run the workflow. Do not claim it passed — the first real tag is
> the test, and Step 6 tells the maintainer how to check.

### Step 5: Document the one-time key setup (maintainer action, not yours)

Add a **Verifying a release** section to `README.md` covering:

1. **One-time setup for the maintainer** — run `go run ./cmd/relsign keygen`
   from `backend/`, store the printed private key as the repository secret
   `RELEASE_SIGNING_KEY`, paste the public key into `backend/release_key.go`,
   and commit **only** the public half. State explicitly that the private key
   must never be committed, pasted into an issue, or logged.
2. **How a user verifies a download**, using only standard tools plus this repo:
   ```
   sha256sum -c SHA256SUMS --ignore-missing
   go run ./cmd/relsign verify -pub <public key> -in SHA256SUMS -sig SHA256SUMS.sig
   ```
3. A note that **losing the private key means rotating it**: generate a new
   pair, update the secret and `release_key.go`, and older binaries will not
   verify newer releases — which is precisely why plan 050 must fail closed
   rather than fall back.

**Verify**: `grep -n "Verifying a release\|RELEASE_SIGNING_KEY" README.md` → matches.

### Step 6: Prove the tests can fail

Run each mutation, confirm the named test fails, then revert:

1. Make `verify` return success without checking the signature → round-trip test
   still passes but the **tampered-payload** and **wrong-key** tests **must
   fail**.
2. Make `sign` accept the key from a flag as well as the env var → no test
   covers this by default, so **add one first**: assert that a key supplied via
   a flag is rejected. Then the mutation must fail it.
3. Make `ReleasePublicKey()` return a hardcoded string instead of the variable →
   the accessor test **must fail**.

Report all three, then confirm `git status --porcelain` is clean.

### Step 7: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"
```

## Done criteria

- [ ] Step 7 passes; all Go packages `ok`
- [ ] Step 6's three mutations each failed the named test, and were reverted
- [ ] `grep -rn "PRIVATE KEY\|releasePrivateKey" backend/ .github/` → **no matches** outside `cmd/relsign`'s own flag/env handling and doc comments — **no private key material anywhere**
- [ ] `grep -n "releasePublicKey = " backend/release_key.go` → the value is the **empty string**
- [ ] `git diff <BASE>..HEAD -- backend/version.go backend/router/ src/ Dockerfile .github/workflows/ci.yml` is **empty**
- [ ] `grep -c "go-version: \"1.25.12\"" .github/workflows/release.yml` → **2** (the existing matrix job and the new release job)
- [ ] `git diff --stat <BASE>..HEAD` lists only the in-scope files

## STOP conditions

- You are about to generate a real keypair and commit, print, or store any part
  of the private key. The maintainer does this; you build the tool.
- You are about to put a non-empty value in `releasePublicKey`.
- You are about to add an update endpoint, or otherwise consume the key in
  `backend/router/`. That is plan 050, and it must not land before this does.
- You are about to add a dependency (cosign action, gpg, a signing library).
- You are about to make the signing step optional / non-fatal when the secret is
  missing.
- You are about to produce SHA256SUMS inside the matrix job.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **Empty key = fail closed.** Everything downstream must refuse to install when
  `ReleasePublicKey()` is `""`. A "verify if configured, otherwise proceed"
  fallback would make the whole exercise decorative.
- **The signature covers SHA256SUMS, not each binary.** That is the standard
  shape and it means one signing operation per release, but it also means the
  updater must verify the signature *first* and only then trust any hash in that
  file. Order matters; plan 050 pins it.
- **Rotating the key invalidates old binaries' ability to verify new releases.**
  An operator on an old build will see verification fail and must update
  manually. Document any rotation in the release notes.
- The matrix/`needs:` split exists because no matrix job sees both binaries.
  Adding a third architecture means adding it to the matrix only — the release
  job picks up whatever artifacts exist.
