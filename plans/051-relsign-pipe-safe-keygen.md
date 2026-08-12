# Plan 051: Make `relsign keygen` safe to pipe, and key decoding whitespace-tolerant

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report. **Commit your completed work BEFORE starting the mutation step**, and
> back each file up outside the repo before applying any mutation. Do **not**
> edit `plans/README.md`.
>
> **You must never generate, print, store or commit a real private key.**
> Keypairs created *inside a Go test* via `ed25519.GenerateKey` are expected and
> fine — they are ephemeral. Running the keygen command yourself and recording
> its output is not.

## Status

- **Priority**: P1 — the release pipeline is currently broken; `v3.7.0` failed
  to publish because of this.
- **Effort**: S
- **Risk**: LOW
- **Depends on**: plan 049 (merged — `backend/cmd/relsign/` exists on `main`).
- **Planned at**: commit `56737fa`, 2026-08-12

---

## 1. What went wrong

Plan 049 specified that `keygen` write the private key to stdout **"clearly
labelled"**. The maintainer's documented setup step pipes that stdout straight
into `gh secret set RELEASE_SIGNING_KEY`, so the secret received the label text
too. The first real release then failed:

```
relsign: decode private key from $RELEASE_SIGNING_KEY: invalid hex encoding: encoding/hex: invalid byte: U+0050 'P'
```

`U+0050` is the `P` of `PRIVATE KEY (…)`. A human-readable label and a
pipe-safe stdout are incompatible requirements, and 049 asked for both.

**The rule this establishes: stdout is for the machine, stderr is for the
human.** A tool whose output is designed to be piped must put *only* the payload
on stdout.

There is a second, independent hardening worth doing at the same time: even with
bare hex, a trailing newline or stray whitespace can reach the decoder — secret
stores and shell pipelines add them freely. Decoding should tolerate surrounding
whitespace rather than fail on an invisible character.

## 2. Current state

`backend/cmd/relsign/main.go`:

```go
	fmt.Fprintln(stdout, "PRIVATE KEY (hex, secret — store as the RELEASE_SIGNING_KEY GitHub secret, never commit):", hex.EncodeToString(priv))
	fmt.Fprintln(stderr, "PUBLIC KEY (hex, safe to share — paste into backend/release_key.go):", hex.EncodeToString(pub))
```

and the two decoders (read them in full before editing — they already validate
length, and that must not regress):

```go
func decodePrivateKey(s string) (ed25519.PrivateKey, error) { … }
func decodePublicKey(s string) (ed25519.PublicKey, error) { … }
```

`README.md` documents the setup step; it currently tells the maintainer to pipe
`keygen`'s stdout into the secret, which is the correct *instruction* — the tool
is what must change to match it.

## Conventions

- Go, stdlib only, no new dependencies. Errors wrapped with
  `fmt.Errorf("...: %w", err)`. Table-driven `testing`.
- **Never log a key, its length, or any prefix of it.** The existing error paths
  are careful about this; keep them that way.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

## Scope

**In scope:**
- `backend/cmd/relsign/main.go`
- `backend/cmd/relsign/main_test.go`
- `README.md` — only if its wording contradicts the new behaviour

**Out of scope — do NOT touch:**
- `backend/release_key.go` and its test. **Do not change the committed public
  key value**, even though it is about to be rotated — that is a maintainer
  action, not yours.
- `.github/workflows/release.yml`. The workflow is correct; the tool was wrong.
- `backend/router/`, `backend/version.go`, anything under `src/`.
- The `sign` / `verify` subcommand semantics beyond the whitespace tolerance
  described below. In particular **`sign` must still read the key only from the
  environment** — no flag, ever.

## Git workflow

- Branch: `advisor/051-relsign-pipe-safe-keygen` from your given base.
- Conventional commit, e.g. `fix(relsign): emit only the private key on stdout`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: stdout carries only the key

Change `runKeygen` so that:

- **stdout receives exactly one line: the bare hex private key.** No label, no
  prefix, no surrounding punctuation.
- **stderr receives everything a human needs**: a labelled public key line, and
  a labelled note that stdout carried the private key and should be piped
  straight into a secret store rather than displayed.

Add a comment at that point saying *why*, in one or two lines: stdout is piped
into `gh secret set`, so anything but the payload corrupts the secret — this
already broke a release once.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → clean.

### Step 2: tolerate surrounding whitespace when decoding

In both `decodePrivateKey` and `decodePublicKey`, `strings.TrimSpace` the input
before hex-decoding. Keep every existing validation — hex correctness and the
exact `ed25519.PrivateKeySize` / `ed25519.PublicKeySize` length check — unchanged
and applied *after* trimming.

Why: a secret store or a shell pipeline can append a newline, and failing on an
invisible trailing character produces an error that looks identical to a genuinely
wrong key. Trimming removes a whole class of unreproducible support problem.

**Do not** strip anything other than leading/trailing whitespace. Interior
whitespace must still be an error — that is a malformed key, not a formatting
artefact.

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 3: Tests

Extend `backend/cmd/relsign/main_test.go`:

1. **`keygen` writes only hex to stdout** — run it with `bytes.Buffer`s for
   stdout/stderr, then assert the stdout content, **after trimming the trailing
   newline, matches `^[0-9a-f]{128}$`** and nothing else. This is the regression
   guard for the bug that broke the release; assert on the whole string, not a
   substring.
2. **The stdout value round-trips**: feed exactly what stdout produced (trimmed)
   into `decodePrivateKey` → no error. This proves the pipe-to-secret path works
   end to end without a human in the middle.
3. **stderr carries the public key** and does **not** contain the private key
   hex. Compare against the private hex explicitly — a copy-paste slip that
   printed both to stderr would otherwise pass unnoticed.
4. **Whitespace tolerance**: `decodePrivateKey` and `decodePublicKey` accept a
   value with a trailing `\n`, a leading space, and both together.
5. **Interior whitespace is still rejected** — e.g. a valid hex string with a
   space inserted in the middle → error.
6. The existing length and non-hex rejection tests must still pass unchanged.

Generate keys with `ed25519.GenerateKey` inside the test; never write one to a
path outside `t.TempDir()`.

**Verify**: `cd backend && go test -race ./cmd/relsign/ -v` → all `PASS`, including the pre-existing cases.

### Step 4: README

If `README.md`'s setup instructions describe the old labelled-stdout behaviour,
update them to state plainly that **`keygen` prints only the private key on
stdout, so it can be piped directly into a secret store**, and that the public
key appears on stderr. If the existing wording is already compatible, leave it
alone and say so in NOTES.

**Verify**: `grep -n "keygen" README.md` → read the surrounding lines and confirm they match the new behaviour.

### Step 5: Commit, then prove the tests can fail

**Commit your work first.** Then back up `main.go` outside the repo
(`cp backend/cmd/relsign/main.go /tmp/`), and apply one mutation at a time,
restoring immediately after each:

1. Restore the label on the stdout line → test 1 **must fail**.
2. Print the private key to stderr as well → test 3 **must fail**.
3. Remove the `TrimSpace` from `decodePrivateKey` → test 4 **must fail**.

Report all three, then confirm `git status --porcelain` is clean.

### Step 6: Full gates

```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

## Done criteria

- [ ] Step 6 passes; all Go packages `ok`
- [ ] Step 5's three mutations each failed the named test, and were reverted
- [ ] `grep -n "PRIVATE KEY" backend/cmd/relsign/main.go` → any match is on a **stderr** write or in a comment, never on the stdout write
- [ ] `grep -rnE "[0-9a-f]{64,}" backend/cmd/relsign/main.go` → **no matches** (no key material)
- [ ] `git diff <BASE>..HEAD -- backend/release_key.go .github/ backend/router/ src/` is **empty**
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- You are about to change the committed public key in `backend/release_key.go`.
- You are about to add a way for `sign` to take the key from a flag.
- You are about to strip interior whitespace, or otherwise "repair" a malformed
  key rather than rejecting it.
- You are about to touch `.github/workflows/release.yml` — the workflow is
  correct.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **stdout is for the machine; stderr is for the human.** Any future subcommand
  whose output is meant to be piped follows the same rule. A label on stdout
  broke a release once, and the failure surfaced far away from its cause — in a
  hex decoder, minutes into a CI run.
- **`TrimSpace` on decode is defence against invisible characters**, not
  leniency. Interior whitespace still fails, because that is a corrupt key
  rather than a formatting artefact.
- The keypair configured before this fix must be **rotated**: its secret holds
  label text, not a key. Rotation is a maintainer action — regenerate, update the
  secret, replace the public key in `release_key.go`, re-tag.
