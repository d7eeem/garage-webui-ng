# Plan 027: Clear `govulncheck` — pin a patched Go toolchain and bump aws-sdk-go-v2

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it).
>
> **Base reset FIRST**: `git checkout -B advisor/027-clear-govulncheck main` then
> `git log --oneline -1` — MUST show `6a6c5c0` or newer, NOT `ee420fb`.
> SENTINEL: `test -f backend/store/users.go && grep -q "modernc.org/sqlite" backend/go.mod && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P1 (security — CI's `security` job is currently RED on `main`)
- **Effort**: M
- **Risk**: **MED-HIGH** — the S3 SDK jump is ~38 minor versions and the object
  browser is the app's most-used feature. The Go bump itself is low risk.
- **Depends on**: nothing. Independent of the 022–026 auth set (all merged).
- **Category**: dependencies / security
- **Planned at**: commit `6a6c5c0`, 2026-08-04

## Why this matters

The `security` job in `.github/workflows/ci.yml` runs `govulncheck` **blocking**,
and it fails on `main` today. Release **v3.0.0** shipped with it red.

The advisor ran `govulncheck` locally to split the findings precisely. **Do not
re-derive this; it is measured, not assumed:**

- Under CI's toolchain (`go-version: "1.25"` → an older 1.25.x) there were
  **7 findings**, six of them Go **standard library** issues
  (`crypto/tls`, `crypto/x509`, `net`, `net/textproto`, `net/http/httputil`),
  each marked *Fixed in* a `go1.25.10`–`go1.25.12` patch.
- Re-run locally under **Go 1.26.5**, all six stdlib findings disappear and the
  output is exactly:

  ```
  Vulnerability #1: GO-2026-5764
    Fixed in: github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.8
    Fixed in: github.com/aws/aws-sdk-go-v2/service/s3@v1.97.3
  Your code is affected by 1 vulnerability from 1 module.
  ```

So there are exactly **two independent fixes**, and they should land as two
separate commits so a regression can be bisected to the right one:

1. **Pin a patched Go toolchain** everywhere the build resolves a Go version →
   removes six stdlib findings.
2. **Bump `aws-sdk-go-v2`** (`service/s3` ≥ `v1.97.3`, `aws/protocol/eventstream`
   ≥ `v1.7.8`) → removes `GO-2026-5764`.

## Current state

### Go version is pinned in three places, inconsistently

| File | Line | Current |
|---|---|---|
| `backend/go.mod` | 3 | `go 1.25.0` |
| `.github/workflows/ci.yml` | 53 (backend job) | `go-version: "1.25"` |
| `.github/workflows/ci.yml` | 72 (security job) | `go-version: "1.25"` |
| `Dockerfile` | 27 | `FROM golang:1.25-alpine AS backend` |
| `.github/workflows/release.yml` | (setup-go step) | `go-version: "1.25"` |

`"1.25"` is a *floating minor* — `actions/setup-go` resolves it to whatever
1.25.x it has cached, which is how a vulnerable patch got used. The `go` directive
in `go.mod` is a **minimum**, not a toolchain selector, and must stay ≤ the pinned
toolchain.

### AWS SDK pins (`backend/go.mod`)

```
	github.com/aws/aws-sdk-go-v2 v1.30.4
	github.com/aws/aws-sdk-go-v2/credentials v1.17.28
	github.com/aws/aws-sdk-go-v2/service/s3 v1.59.0
	github.com/aws/smithy-go v1.20.4
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.4 // indirect
	... (7 more aws indirect pins)
```

### The entire S3 surface lives in ONE file: `backend/router/browse.go`

Everything the upgrade could break:

```
config.LoadDefaultConfig / credentials.NewStaticCredentialsProvider
s3.NewFromConfig  ·  s3.NewPresignClient  ·  PresignGetObject
client.HeadObject      (s3.HeadObjectInput)
client.GetObject       (s3.GetObjectInput)
client.PutObject       (s3.PutObjectInput)
client.DeleteObject    (s3.DeleteObjectInput)
client.DeleteObjects   (s3.DeleteObjectsInput)
client.ListMultipartUploads   (s3.ListMultipartUploadsInput)
client.AbortMultipartUpload   (s3.AbortMultipartUploadInput)
```
Plus paginated listing. `backend/router/browse_test.go` is the only other file
importing the SDK.

**Garage is S3-*compatible*, not S3.** Newer SDK versions have changed request
defaults in ways third-party servers notice — most notably **integrity/checksum
behaviour on `PutObject`** (`when_supported` vs `when_required`). This is the
single most likely breakage, and it will NOT show up in `go build` or in unit
tests — only against a real Garage. Live verification is mandatory (see below).

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Build/vet/fmt | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)"` | exit 0 |
| CGO-free build | `cd backend && CGO_ENABLED=0 go build ./...` | exit 0 (**must stay true**) |
| Tests | `cd backend && go test -race ./...` | `ok` (note: ~2 min, real bcrypt) |
| Vulnerability scan | `cd backend && go install golang.org/x/vuln/cmd/govulncheck@latest && "$(go env GOPATH)/bin/govulncheck" ./...` | `No vulnerabilities found.` |
| Frontend | `npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build` | exit 0 |

## Scope

**In scope**:
- `backend/go.mod`, `backend/go.sum`
- `.github/workflows/ci.yml` (two `go-version` pins)
- `.github/workflows/release.yml` (one `go-version` pin)
- `Dockerfile` (builder image tag)
- `CLAUDE.md` (the "Go 1.25+" line, if the floor changes)
- `backend/router/browse.go` — **only if** the SDK bump forces a compile fix

**Out of scope**: any behavioural change to the object browser, the auth/store
packages, the frontend, `plans/`. Do not "modernise" SDK call sites that still
compile. Do not touch `modernc.org/sqlite`.

## Steps

### Step 1 — Commit 1: pin a patched Go toolchain

Pick the pin: use the **latest patch of the Go line you standardise on**. Both of
these satisfy the advisories (all six stdlib fixes land by `go1.25.12`):

- Conservative: `1.25.12` (or later 1.25.x) — smallest change.
- Forward: `1.26.x` — what the advisor verified locally as clean (Go 1.26.5).

Prefer whichever is available to `actions/setup-go` **as an exact patch**. Set the
SAME version in all four places:

- `.github/workflows/ci.yml` — both `go-version:` values (backend + security jobs)
- `.github/workflows/release.yml` — the `go-version:` value
- `Dockerfile` — `FROM golang:<version>-alpine AS backend`

Use an **exact patch** (`"1.25.12"`, `golang:1.25.12-alpine`), not a floating
minor — a floating minor is what let a vulnerable patch in. Update the Dockerfile
comment above the `FROM` to say the pin is exact and why.

Leave `go 1.25.0` in `backend/go.mod` **unless** you chose a 1.26 toolchain and
want to raise the floor; the directive is a minimum and must never exceed the
pinned toolchain.

**Verify**:
```
cd backend && go build ./... && go vet ./... && CGO_ENABLED=0 go build ./...
grep -rn "go-version" .github/workflows/*.yml     # all identical, exact patch
grep -n "FROM golang:" Dockerfile                  # same exact patch
```
Commit: `ci: pin an exact patched Go toolchain (closes stdlib advisories)`

### Step 2 — Commit 2: bump aws-sdk-go-v2

```
cd backend
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go get github.com/aws/aws-sdk-go-v2@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go mod tidy
```
Then confirm the two advisory floors are met:
```
grep -E "service/s3 v|aws/protocol/eventstream v" go.mod   # s3 >= v1.97.3, eventstream >= v1.7.8
```

Fix any compile breaks **minimally** — the goal is to keep behaviour identical.
If a call site needs a semantic change (not just a renamed type), that is a STOP
condition: report it rather than guessing at Garage compatibility.

**Verify**: `go build ./... && go vet ./... && CGO_ENABLED=0 go build ./... && go test -race ./...`
Commit: `deps: bump aws-sdk-go-v2 (closes GO-2026-5764)`

### Step 3 — Prove the scan is clean

```
cd backend && go install golang.org/x/vuln/cmd/govulncheck@latest
"$(go env GOPATH)/bin/govulncheck" ./...
```
**Expected: `No vulnerabilities found.`** If anything remains, report the exact
IDs — do not suppress, and do not add `continue-on-error` to the security job.

### Step 4 — Live verification against a real Garage (REQUIRED)

Unit tests will not catch an S3 wire-compatibility regression. Start a Garage node
(e.g. `dxflrs/garage:v2.0.0` with the S3 API on `:3900` and admin on `:3903`),
run the backend against it, and exercise **every** SDK call path through the API:

| Path | Endpoint |
|---|---|
| list objects (incl. a prefix/folder) | `GET /api/browse/{bucket}` |
| download | `GET /api/browse/{bucket}/{key}` |
| **upload** ← most likely to break (checksum defaults) | `PUT /api/browse/{bucket}/{key}` |
| delete one | `DELETE /api/browse/{bucket}/{key}` |
| bulk delete | `POST /api/browse/{bucket}` |
| multipart list / abort | `GET`/`DELETE /api/multipart/{bucket}` |
| presigned share link | `GET /api/share/{bucket}/{key}?expires=3600` — then fetch the returned URL **unauthenticated** and confirm 200 |

Every one must behave as before. **Upload and presign are the two to watch**: if
`PutObject` starts sending a checksum/trailer Garage rejects, uploads fail with a
signature or `501`-style error even though everything compiles.

If any path breaks, STOP and report the exact request/response — do not work
around it by disabling checksums without saying so prominently.

### Step 5 — Docs + full sweep

If the Go floor changed, update `CLAUDE.md`'s "Go 1.25+" line (and the Dockerfile
note) so the documented toolchain matches reality.

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && CGO_ENABLED=0 go build ./... && go test -race ./...
npx pnpm@9 run typecheck && npx pnpm@9 run test && npx pnpm@9 run build
docker build -t gwui-ng:027 .        # the builder image tag changed — prove it still builds
```

## Test plan

- Existing `backend/router/browse_test.go` must keep passing unmodified. If it
  needs edits to compile, that is a signal the SDK's types changed — report it.
- **The live matrix in Step 4 is the real test plan.** An SDK upgrade that
  compiles and passes unit tests can still break every upload.
- `govulncheck` returning `No vulnerabilities found.` is the acceptance criterion.

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `cd backend && CGO_ENABLED=0 go build ./...` exits 0 (distroless build preserved)
- [ ] `govulncheck ./...` prints **`No vulnerabilities found.`**
- [ ] `grep -E "service/s3 v1\.(9[7-9]|[0-9]{3})" backend/go.mod` matches (s3 ≥ v1.97.3)
- [ ] All `go-version:` values across both workflows are the **same exact patch**; `grep -c '"1.25"' .github/workflows/*.yml` → 0 (no floating minor)
- [ ] `docker build .` succeeds
- [ ] Every row of the Step 4 live matrix verified, including a presigned link fetched unauthenticated
- [ ] Two separate commits (toolchain, then SDK)

## STOP conditions

- The SDK bump requires a **semantic** change at a call site (not a rename) —
  report it; Garage compatibility is not something to guess at.
- Any Step 4 path regresses, especially upload or presign.
- `CGO_ENABLED=0 go build ./...` stops working — something pulled in cgo; STOP.
- `govulncheck` still reports findings after both commits — report the IDs; never
  silence the scanner or make the security job non-blocking.

## Maintenance notes

- **Pin exact Go patches, not floating minors.** A floating `"1.25"` is exactly
  how a vulnerable stdlib got into a release build. Renewing the pin is a small,
  regular chore — better than a silent regression.
- **`govulncheck` is intentionally blocking.** If it goes red, fix the dependency;
  do not add `continue-on-error` (that is what made the earlier Trivy step
  worthless before it was removed).
- **Garage is S3-compatible, not S3.** Every future `aws-sdk-go-v2` bump needs the
  Step 4 live matrix, because wire-level default changes are invisible to the
  compiler and to unit tests.
- The SDK pins had drifted ~38 minor versions before this bump. Consider a
  scheduled dependency review so the next jump is smaller.
