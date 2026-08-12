# Plan 050: In-browser update — download, verify, stage

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`.
>
> **Drift check (run first)**:
> ```
> git diff --stat <BASE> -- backend/router/update.go backend/release_key.go src/pages/settings/
> ls backend/cmd/relsign/main.go backend/release_key.go
> ```
> Both files must exist. If they do not, plan 049 has not landed — **STOP
> immediately**; this plan is unbuildable without it.

## Status

- **Priority**: P2
- **Effort**: L
- **Risk**: **HIGH** — this is the only code path in the product that writes an
  executable to disk from a network source. Treat every step as security code.
- **Depends on**: **plan 049 (release signing) — hard gate.** Verification is
  the entire safety story; without a configured signing key this feature must
  refuse to run.
- **Category**: feature / security-sensitive
- **Planned at**: commit `9858186` (v3.6.0), 2026-08-11

---

## 0. What this builds, and the one thing it deliberately does not

The maintainer asked for an in-browser update. This plan delivers:

**Download → verify signature → verify checksum → stage the new binary**, driven
by an admin-only button in Settings → About, with the outcome reported in the UI.

It deliberately **does not restart the service by default**. Applying a staged
binary needs the process to restart, and a process cannot reliably restart
itself: if it exits and the service manager is not configured to bring it back,
**the admin console is gone and the only way in is SSH** — exactly when the
operator would most want the console. So:

- Default (`restart: false`): the binary is swapped on disk and the response
  says a restart is required. The running process keeps serving the old version
  until the operator restarts it. **Nothing can lock them out.**
- Opt-in (`restart: true`): after a successful swap, the process triggers its
  existing graceful shutdown. The UI must obtain a **separate, explicit
  confirmation** for this and state that the service manager must be configured
  to restart the service.

Do not change that default, and do not remove the staged path. If the maintainer
later wants one-click-including-restart as the default, that is a follow-up
decision once staging has proven itself in the field.

## 1. The security model — read before writing code

This process holds the **Garage admin token**. Code it downloads and later
executes has cluster-wide authority. The controls, in order of importance:

1. **Signature verification is mandatory and fails closed.** If
   `ReleasePublicKey()` is `""`, the endpoint returns an error and downloads
   nothing. There is no "unverified but proceed" path, no override flag, no
   env var to disable it.
2. **The signature is checked before any hash in `SHA256SUMS` is trusted.**
   `SHA256SUMS` is attacker-controlled until its signature verifies. Verify the
   ed25519 signature over the file's raw bytes *first*; only then parse it and
   use a hash from it.
3. **The downloaded binary's SHA-256 must match the entry for our exact asset
   name.** Match on the full name (`garage-webui-ng-linux-<GOARCH>`), not a
   prefix or a substring.
4. **Admin-only.** `/update/*` is a write endpoint: it goes through the existing
   CSRF middleware, and the handler calls the same `requireAdmin` check
   `backend/router/admin_users.go` uses. A viewer must get 403.
5. **Bounded.** Cap the download with `io.LimitReader` (60 MB is ~4× the current
   binary), set an HTTP client timeout, and refuse to run two updates at once.
6. **Nothing is executed by this plan.** The staged file is written and renamed,
   never run. The only thing that ever executes it is the service manager, after
   a restart the operator controls.

## 2. Current state

### `backend/release_key.go` (from plan 049)

```go
// ReleasePublicKey returns the configured release-signing public key, or "" if
// this build has none.
func ReleasePublicKey() string { return releasePublicKey }
```

**It is `package main`.** A `package main` symbol cannot be imported by
`backend/router` — this repo has hit that exact wall before. Follow the
established fix: a package-level `var` in `backend/router/config.go` assigned
from `main.go` at startup, the same way `AppVersion` is (set at `main.go:70`,
before `HandleApiRouter()` and `ListenAndServe`).

### `backend/router/update.go` (from plans 041 + 044)

Already present and **not to be modified beyond adding fields**:

```go
type UpdateCheck struct {
	Enabled         bool   `json:"enabled"`
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	URL             string `json:"url,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
	CheckFailed     bool   `json:"checkFailed,omitempty"`
	Deployment      string `json:"deployment,omitempty"`
	UpdateCommand   string `json:"updateCommand,omitempty"`
}
```

and the deployment probe, whose result gates this feature:

```go
const (
	deploymentBinary  DeploymentKind = "binary"
	deploymentManaged DeploymentKind = "managed"
	deploymentUnknown DeploymentKind = "unknown"
)
func detectDeployment() DeploymentKind { … }   // O_WRONLY probe, fails safe to managed
```

`fetchLatestRelease`, `isNewer`, `releasesURL` and the 6-hour cache also live in
this file. Reuse `fetchLatestRelease`; do not duplicate it.

### `backend/main.go` — the shutdown path that `restart: true` reuses

```go
	stop := make(chan os.Signal, 1)
	…
	if err := srv.Shutdown(ctx); err != nil {
```

A graceful-shutdown path already exists on a signal channel. `restart: true`
must trigger **that**, by sending the process its own SIGTERM — not `os.Exit`,
which would drop in-flight requests including the one that asked for the update.

## Conventions

- **Go handlers**: methods on empty structs, `(w, r)`, ending in
  `utils.ResponseSuccess(w, data)` / `utils.ResponseError(w, err)`.
  **`utils.ResponseError` does NOT stop the handler — always `return` after it.**
- Routes are registered in `backend/router/router.go`; the CSRF middleware
  already covers non-GET requests, and `middleware/auth.go` denies `/admin/` to
  viewers. This endpoint is **not** under `/admin/`, so its admin check is the
  handler's own `requireAdmin` — read how `admin_users.go` does it and match.
- Frontend: TanStack Query hooks in the page's `hooks.ts`, one per endpoint;
  mutation hooks spread `...options` last. UI from `react-daisyui` with local
  wrappers in `src/components/ui/`.
- Tests: Go table-driven `testing` + `httptest`; **session-touching handlers
  must be served through `sessMgr.LoadAndSave(...)`** or `utils.Session.Get`
  panics. Frontend uses `@testing-library/react` + `vi.hoisted`/`vi.mock`.
- **`pnpm run lint` is expected to be red** (~55 pre-existing). New code clean.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Tests | `pnpm run test` | all pass |
| Build | `pnpm run build` | exit 0 |

`pnpm` is at `/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm`.
If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`

## Scope

**In scope:**
- `backend/router/selfupdate.go` (new) — download, verify, stage
- `backend/router/selfupdate_test.go` (new)
- `backend/router/config.go` — one injected `var` for the public key
- `backend/router/router.go` — register the route
- `backend/main.go` — assign the key var (one line, beside `AppVersion`)
- `backend/router/update.go` — **only** to add `CanSelfUpdate bool` to `UpdateCheck`
- `src/pages/settings/hooks.ts`, `about-tab.tsx`, `about-tab.test.tsx`
- `README.md` — document the feature and its requirements

**Out of scope — do NOT touch:**
- `detectDeployment`, `classifyOpenResult`, `updateCommandFor`, `isNewer`,
  `parseNumericVersion`, the update-check cache/TTL, `UPDATE_CHECK_ENABLED`
  semantics, or the managed-deployment prose. All settled by plans 041/044.
- `backend/release_key.go` and `backend/cmd/relsign/` — plan 049 owns them.
  **Do not put a key value in either.**
- `backend/middleware/` — CSRF and auth already do their jobs.
- `backend/router/browse.go`, and anything to do with objects or buckets.
- Any new dependency. Verification is `crypto/ed25519` + `crypto/sha256`, both
  stdlib.

## Git workflow

- Branch: `advisor/050-in-browser-update` from your given base.
- Conventional commits, e.g. `feat: download and stage a signed update`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Make the public key reachable from `router`

In `backend/router/config.go`, beside the existing `AppVersion`:

```go
// ReleasePublicKey is the hex ed25519 public key releases are signed with,
// injected from main at startup because release_key.go is package main and Go
// forbids importing it. Empty means this build cannot verify a release, and
// every self-update path must refuse to run — see selfupdate.go.
var ReleasePublicKey string
```

In `backend/main.go`, next to `router.AppVersion = Version()`:

```go
	router.ReleasePublicKey = ReleasePublicKey()
```

Confirm by reading `main.go` that this runs **before** `HandleApiRouter()` and
`ListenAndServe`, exactly as the `AppVersion` assignment does.

**Verify**: `cd backend && go build ./...` → exit 0.

### Step 2: Verification helpers (pure, testable)

Create `backend/router/selfupdate.go`. Start with the pure functions, because
these are what the tests can actually pin:

```go
// verifyChecksumsSignature reports whether sig (hex ed25519) is a valid
// signature over the exact bytes of checksums, under pubHex.
//
// Order is the security property: SHA256SUMS is attacker-controlled until this
// returns nil, so no hash inside it may be used before that. An empty pubHex is
// a hard failure — this build cannot verify anything, and "cannot verify" must
// never degrade into "install anyway".
func verifyChecksumsSignature(pubHex string, checksums, sigHex []byte) error

// checksumFor extracts the expected SHA-256 for exactly `name` from a verified
// SHA256SUMS body (`<64-hex>  <name>` per line). Matching is on the full name;
// a prefix or substring match would let "…-amd64-evil" satisfy "…-amd64".
func checksumFor(checksums []byte, name string) (string, error)

// assetName is the release asset this build should install.
func assetName() string   // "garage-webui-ng-linux-" + runtime.GOARCH
```

Requirements:

- `verifyChecksumsSignature` returns a **non-nil error** for: empty `pubHex`,
  non-hex key or signature, wrong key length, wrong signature length, and a
  signature that simply does not verify. Never return `nil` on a parse failure.
- `checksumFor` tolerates the two `sha256sum` output spacings (`"  "` and
  `" *"`), ignores blank lines, and errors when the name is absent.
- `checksumFor` must **reject a duplicate entry** for the same name — two lines
  claiming different hashes for one file is a tampered manifest, not a
  tie-break.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./...` → clean.

### Step 3: The staging routine

Still in `selfupdate.go`. A single-flight guard first:

```go
// updateInFlight serialises update attempts: two concurrent downloads racing to
// rename onto the same path is how a half-written binary gets installed.
var updateInFlight atomic.Bool
```

Then the handler `func (u *Update) Apply(w http.ResponseWriter, r *http.Request)`,
in this exact order — each check before any work it guards:

1. `requireAdmin` (match `admin_users.go`); on failure respond 403 and **return**.
2. If `ReleasePublicKey == ""` → 400, "this build has no release signing key
   configured; in-browser update is unavailable". Nothing is downloaded.
3. If `detectDeployment() != deploymentBinary` → 400, explaining the binary is
   not writable by this process and the deployment is updated from outside.
4. `updateInFlight.CompareAndSwap(false, true)`; if it fails → 409, "an update
   is already in progress". `defer updateInFlight.Store(false)`.
5. Decode the body: `{"restart": bool}`. A malformed body is `"invalid request
   body"` — do not echo the body back.
6. `fetchLatestRelease(r.Context())` (reuse it). If it is not newer than
   `AppVersion` → 400, "already up to date".
7. Download three assets from that release with a **dedicated `http.Client`**
   (30s timeout, `io.LimitReader` at 60 MB for the binary and 1 MB for the two
   text files): the binary named `assetName()`, `SHA256SUMS`, `SHA256SUMS.sig`.
   A missing asset is an error naming which one.
8. `verifyChecksumsSignature(...)` → on error, **abort and delete anything
   written**. This is the gate; nothing below runs if it fails.
9. `checksumFor(...)`, then `sha256.Sum256` over the downloaded bytes; mismatch
   → abort and delete.
10. Only now touch the filesystem:
    - `exe, err := os.Executable()`
    - write to `exe + ".new"` **in the same directory** (a different filesystem
      makes the rename non-atomic), mode `0755`, via `os.CreateTemp` in that dir
      then rename, so a partial write is never left at `.new`
    - copy the current binary to `exe + ".bak"` (best-effort; log and continue
      if it fails — do not abort an otherwise-verified update over a backup)
    - `os.Rename(exe+".new", exe)` — atomic on the same filesystem
11. Respond with what actually happened: the new version, whether a restart is
    required, and the `.bak` path.
12. If `restart` was true **and** everything above succeeded, send the process
    SIGTERM (`syscall.Kill(os.Getpid(), syscall.SIGTERM)` or
    `p, _ := os.FindProcess(os.Getpid()); p.Signal(syscall.SIGTERM)`) **after
    the response is flushed** — respond first, then signal, so the browser gets
    an answer. Never `os.Exit`.

> **Delete partial downloads on every failure path.** A leftover `<exe>.new` is
> confusing at best and a stale-binary hazard at worst.

Register in `router.go` as `POST /update/apply` on the authenticated router,
beside the existing `/update-check`.

**Verify**: `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` → all `ok`.

### Step 4: Advertise the capability

Add **one** field to `UpdateCheck` in `update.go` — append, do not reorder:

```go
	// CanSelfUpdate is true when this build could install an update itself:
	// a signing key is configured AND the executable is writable. Advisory
	// only — Apply re-checks both, since the answer can change between calls.
	CanSelfUpdate bool `json:"canSelfUpdate,omitempty"`
```

Set it per request, alongside `Deployment`/`UpdateCommand`, in **all** response
paths, and **after** any cache read — the existing code already establishes that
pattern with a comment; follow it exactly. Change nothing else in that file.

**Verify**: `cd backend && go test -race ./router/ -run TestDeploymentFields -v` → `PASS`.

### Step 5: Tests (Go)

`backend/router/selfupdate_test.go`. Generate an ephemeral keypair in-test with
`ed25519.GenerateKey` — never a committed key.

**`TestVerifyChecksumsSignature`**:
1. Valid signature → `nil`.
2. **Empty public key → error.** The fail-closed guard; it is the single most
   important test in this plan.
3. Tampered checksums body → error.
4. Tampered signature → error.
5. Signature from a different key → error.
6. Non-hex key, wrong-length key, non-hex signature → error, not panic.

**`TestChecksumFor`**:
7. Finds the hash for an exact name.
8. **Does not match on a prefix**: a manifest containing only
   `garage-webui-ng-linux-amd64-evil` must **error** when asked for
   `garage-webui-ng-linux-amd64`.
9. Handles both `"  "` and `" *"` separators; ignores blank lines.
10. Missing name → error.
11. **Duplicate entries for one name → error.**

**`TestApplyRefuses`** — handler-level, served through
`sessMgr.LoadAndSave(...)` (see `browse_test.go`'s `withDownloadSession` for the
pattern):
12. Non-admin → 403.
13. Admin + `ReleasePublicKey == ""` → 4xx, and the body mentions the missing
    signing key. Restore the var with `t.Cleanup`.

> Tests 12 and 13 must assert **no outbound request was made** — point
> `releasesURL` at an `httptest` server that fails the test if hit, and restore
> it with `t.Cleanup`. "Refuses" means refuses before touching the network, not
> after.

A full happy path needs a fake release server plus a writable fake executable.
Build it **only if** you can do so without `os.Executable` trickery; otherwise
skip it and say so in NOTES. Do not fake `os.Executable` by rewriting the
binary under test.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestVerifyChecksums|TestChecksumFor|TestApplyRefuses"` → all `PASS`.

### Step 6: The UI

`src/pages/settings/hooks.ts` — add `canSelfUpdate?: boolean` to the
`UpdateCheck` type, and a mutation hook:

```ts
export const useApplyUpdate = (options?: UseMutationOptions<ApplyUpdateResult, Error, { restart: boolean }>) => …
```

posting to `/update/apply`. The shared `api` client already attaches the CSRF
header.

`src/pages/settings/about-tab.tsx` — when `update?.canSelfUpdate &&
update?.updateAvailable`, render an **Update now** button beside the existing
"Update available" line. Behaviour:

- First click opens a confirmation (`window.confirm` is acceptable and matches
  `object-actions.tsx`'s delete flow) naming the version being installed and
  stating that **a restart is required to apply it**.
- Default request is `{restart: false}`.
- While pending: disable the button, show `Updating…`.
- On success: a persistent message — not a toast — saying the update is staged
  and the service must be restarted, including the version.
- On failure: show the server's error text via the existing `handleError`.
- A **separate** checkbox, unchecked by default, labelled something like
  "Restart the service automatically after installing", with a muted warning
  that the service manager must be configured to restart it or the console will
  stay down. Only that checkbox sends `{restart: true}`.

Leave the 044 blocks (`updateCommand`, managed prose, disabled hint,
`checkFailed`) exactly as they are — this adds a path, it does not replace one.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step 7: Tests (frontend)

Extend `src/pages/settings/about-tab.test.tsx`:
14. No **Update now** button when `canSelfUpdate` is false or absent.
15. Button appears when `canSelfUpdate && updateAvailable`.
16. Clicking it (with `window.confirm` stubbed true) calls the mutation with
    `{restart: false}`.
17. With the restart checkbox ticked, it sends `{restart: true}`.
18. Declining the confirm calls nothing.

**Verify**: `pnpm exec vitest run about-tab` → all pass, existing cases included.

### Step 8: Document it

`README.md` — an **In-browser update** subsection stating: it is admin-only;
it requires a build with a configured release signing key (plan 049) and a
writable executable; it verifies an ed25519 signature over `SHA256SUMS` and then
the binary's SHA-256 **before** replacing anything; the previous binary is kept
at `<path>.bak`; and **a restart is required**, which the app does not perform
unless explicitly asked. Say plainly that Docker deployments cannot use this and
should pull a new image.

**Verify**: `grep -n "In-browser update" README.md` → matches.

### Step 9: Prove the tests can fail

1. Make `verifyChecksumsSignature` return `nil` when `pubHex == ""` → test 2
   **must fail**. *The fail-closed guard; if this mutation does not fail a test,
   the plan is not done.*
2. Make `checksumFor` use `strings.HasPrefix` → test 8 **must fail**.
3. Remove the `ReleasePublicKey == ""` check from `Apply` → test 13 **must
   fail**.
4. Default `restart` to true → frontend test 16 **must fail**.

Report all four; confirm `git status --porcelain` is clean before committing.

### Step 10: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

### Step 11: Manual checks — reviewer's job

You cannot exercise this. Do **not** claim any of it passed; list it in NOTES:

1. A build with **no** key configured shows no Update button, and `POST
   /update/apply` refuses without any outbound request.
2. With a real signed release: the button stages the binary, `<exe>.bak` exists,
   and the service runs the new version **after** a manual restart.
3. **Tamper test — the one that matters**: serve a release whose binary does not
   match `SHA256SUMS`, and separately one whose `SHA256SUMS.sig` is invalid.
   Both must refuse, leave the running binary untouched, and leave no
   `<exe>.new` behind.
4. A viewer account gets 403.
5. A Docker deployment shows no button (its executable is not writable).

## Done criteria

- [ ] Step 10 passes; all Go packages `ok`
- [ ] Step 9's four mutations each failed the named test, and were reverted
- [ ] `grep -rnE "os/exec|syscall.Exec|exec.Command" backend/router/selfupdate.go` → **no matches** — this plan never executes what it downloads
- [ ] `grep -n "os.Exit" backend/router/selfupdate.go` → **no matches** (restart is SIGTERM into the existing graceful shutdown)
- [ ] `grep -c "ReleasePublicKey" backend/router/selfupdate.go` → **≥1**, and the empty-key check precedes any network call (read the function; confirm by eye)
- [ ] `git diff <BASE>..HEAD -- backend/release_key.go backend/cmd/ backend/middleware/ backend/router/browse.go` is **empty**
- [ ] `git diff <BASE>..HEAD -- backend/router/update.go` touches **only** the `CanSelfUpdate` field and its assignments
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- `backend/release_key.go` or `backend/cmd/relsign/` do not exist — plan 049 has
  not landed. This plan is unbuildable; stop.
- You are about to add a way to skip verification: a flag, an env var, a
  "verification unavailable, continuing" branch, or treating an empty key as
  permissive.
- You are about to execute the downloaded binary — including "just to check
  `-version`".
- You are about to make `restart: true` the default, or remove the staged path.
- You are about to use `os.Exit` rather than the existing graceful-shutdown
  signal path.
- You are about to trust any hash from `SHA256SUMS` before its signature
  verifies.
- You are about to write the new binary anywhere other than the directory
  holding the current executable (the rename must be atomic).
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **Fail closed is the whole design.** An empty `ReleasePublicKey` must always
  mean "refuse", never "skip verification". Every future change here should be
  read against that sentence.
- **Verify the signature before parsing SHA256SUMS.** The manifest is untrusted
  input until the ed25519 check passes; using a hash from it earlier would make
  the signature decorative.
- **Full-name matching, not prefix.** `…-amd64-evil` must not satisfy a request
  for `…-amd64`. There is a test for it because it is an easy, invisible slip.
- **The staged default is a safety property, not caution.** A process that exits
  without a supervisor to restart it removes the operator's only web access.
- **`<exe>.bak` is the rollback, and it is manual.** Automatic rollback is
  impossible from inside a process that has already exited; the README says so.
- **Key rotation breaks old builds' ability to verify new releases** (plan 049).
  When that happens, operators on old versions must update manually — the
  correct behaviour, and another reason this fails closed.
