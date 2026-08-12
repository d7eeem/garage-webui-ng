# Plan 041: Let the app report its own version, and optionally tell you when a newer one exists

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- backend/main.go backend/router/ backend/Makefile \
>   Dockerfile .github/workflows/ src/pages/settings/
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: dx + direction
- **Planned at**: commit `bb57345`, 2026-08-10

## Why this matters

**The application does not know what version it is.** Verified across the whole
repo:

- No version constant, variable, or `-X` ldflag anywhere in `backend/`.
- No `define` in `vite.config.ts`; nothing in `src/` reads a version.
- `package.json`'s `"version"` is consumed by **nothing at runtime** — it exists
  only as metadata on a `"private": true` package that is never published.

Three concrete costs:

1. **An operator cannot tell what they are running.** The cluster page already
   shows *Garage's* version (`src/pages/cluster/page.tsx:33`,
   `value={node?.garageVersion}`) — so the UI happily reports the version of the
   thing it talks to, and nothing about itself. Support questions start with
   "which version?" and the answer requires shell access.
2. **Version drift went unnoticed for days.** `v3.2.0` was tagged on a tree whose
   `package.json` still read `3.1.0`, because the bump commit only touched
   `plans/README.md`. Nothing in the product surfaced the contradiction. A
   displayed version turns that class of mistake into something you see.
3. **No upgrade signal at all.** This is a self-hosted app distributed by GitHub
   release and `ghcr.io` tag. Operators find out a new version exists by
   remembering to look.

After this plan: the footer of Settings → About shows the running version, and —
**only if the operator opts in** — whether a newer release exists.

## Current state

### Build wiring — `-ldflags` is already used in both release paths

`Dockerfile:51`:
```
    go build -tags=prod -trimpath -ldflags="-s -w" -o /main .
```

`.github/workflows/release.yml:49-51`:
```yaml
          CGO_ENABLED=0 GOOS=linux GOARCH=${{ matrix.goarch }} \
            go build -tags=prod -trimpath -ldflags="-s -w" \
            -o "garage-webui-ng-linux-${{ matrix.goarch }}" .
```

`backend/Makefile` (the whole file — note it has **no** ldflags):
```make

build:
	CGO_ENABLED=0 go build -o main -tags="prod" .

run:
	go run .
```

Both release paths already pass `-ldflags`, so adding `-X` is a one-token change
in each. The Makefile is the local/dev path and will report `dev`.

### `backend/main.go` — flag handling exists

```go
func main() {
	healthCheck := flag.Bool("health", false, "…")
	resetPassword := flag.String("reset-password", "", "…")
	createAdmin := flag.String("create-admin", "", "…")
	listUsers := flag.Bool("list-users", false, "…")

	flag.Parse()
```

There is a precedent for cheap CLI branches that run and exit — `-health` is the
container healthcheck. A `-version` flag belongs beside them.

### `backend/router/config.go` — the whole handler

```go
func (c *Config) GetAll(w http.ResponseWriter, r *http.Request) {
	resp := schema.NewConfigResponse(utils.Garage.Config)
	resp.Sharing = utils.Garage.IsSharingEnabled()
	resp.S3Web.PublicURL = utils.Garage.GetWebPublicURL()
	utils.ResponseSuccess(w, resp)
}
```

Env-derived, non-`garage.toml` fields are set **in the handler**, never in
`schema.NewConfigResponse` (which stays a pure projection of the parsed TOML).
Follow that.

### `src/pages/settings/page.tsx` — the tab registry

```tsx
const tabs: Tab[] = [
  { …, title: "Account", …, Component: AccountTab },
  { …, title: "Users", icon: UsersIcon, Component: UsersTab },
];
```

Adding a third entry is the whole UI wiring. `TabView` handles the rest.

### Conventions to match

- **Go handlers**: methods on empty structs, `(w, r)`, ending in
  `utils.ResponseSuccess(w, data)` / `utils.ResponseError(w, err)`.
  **`utils.ResponseError` does NOT stop the handler — always `return` after it.**
- Env reads: `utils.GetEnv(name, default)` (see `backend/middleware/csrf.go`).
- DTOs live in `backend/schema/` with `json:` tags.
- **Frontend hooks**: one per endpoint in the page's `hooks.ts`; array query keys.
- Component tests: `@testing-library/react`; `src/components/containers/account-button.test.tsx` is the `vi.hoisted` + `vi.mock` exemplar.
- Go tests: plain `testing`, table-driven. `backend/router/browse_test.go`'s `TestNormalizeListLimit` is the shape.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems; CI runs it `continue-on-error`). Make new code clean; do not clear the backlog.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Install | `pnpm install` | exit 0 |
| Typecheck | `pnpm run typecheck` | exit 0 |
| Frontend tests | `pnpm run test` | all pass |
| Frontend build | `pnpm run build` | exit 0 |
| Go gates | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

`pnpm` may not be on PATH; it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. Do not substitute `npm` (lockfile is `pnpm-lock.yaml`). If `go` is not
on PATH, use the repo's pinned toolchain:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(Debian-based — `-race` needs gcc; `git` is unusable inside the container.)

## Scope

**In scope:**
- `backend/version.go` (**create**) — the version var and its accessor
- `backend/version_test.go` (**create**)
- `backend/main.go` — a `-version` flag
- `backend/router/config.go` — surface the version
- `backend/schema/config.go` — one new field
- `backend/schema/config_test.go` — extend
- `backend/router/update.go` (**create**) — the opt-in release check
- `backend/router/update_test.go` (**create**)
- `backend/router/router.go` — one route
- `backend/Makefile`, `Dockerfile`, `.github/workflows/release.yml`, `.github/workflows/docker-publish.yml` — pass the version
- `.github/workflows/ci.yml` — one guard step (Step 6)
- `src/types/garage.ts` — mirror the config field
- `src/pages/settings/about-tab.tsx` (**create**)
- `src/pages/settings/about-tab.test.tsx` (**create**)
- `src/pages/settings/hooks.ts` — one hook
- `src/pages/settings/page.tsx` — one tab entry
- `README.md` — one env-var row + a short note

**Out of scope — do NOT touch:**
- Any **self-updating** behaviour: downloading, replacing the binary, restarting,
  pulling images. This plan **reports**; it never acts. Self-update on a
  single-binary service is a different, much riskier project.
- `schema.NewConfigResponse` — keep it a pure `garage.toml` projection.
- Auth, session, CSRF, the S3/browse path, `getPublicAccess`, the upload card.
- Any *browser-side* call to GitHub — see Step 4 for why.
- Bumping `package.json` yourself. Step 6 adds a guard; it does not set versions.

## Git workflow

- Branch: `advisor/041-version-and-update-check` from your given base.
- Conventional commits, e.g. `feat: report the running version and check for updates`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Give the binary a version

Create `backend/version.go`:

```go
package main

// version is the release identity of this binary, injected at build time with
//
//	-ldflags "-X main.version=v3.3.0"
//
// It defaults to "dev" so a plain `go build` / `make build` is always honest
// about being an untagged local build rather than claiming a release number.
//
// The GIT TAG is the source of truth, not package.json. The tag is what
// produces the GitHub release and the ghcr.io semver image tags, and it is what
// an operator quotes in a bug report. package.json is metadata on a
// "private": true package that is never published; it must FOLLOW the tag, and
// Step 6 adds a CI guard that fails a release when the two disagree.
var version = "dev"

// Version returns the running build's version, never empty.
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}
```

In `backend/main.go`, add a flag beside the existing ones and handle it right
after `flag.Parse()`, before any other branch — it must not need a database or
a Garage connection:

```go
	showVersion := flag.Bool("version", false, "print the version and exit")
	…
	flag.Parse()

	if *showVersion {
		fmt.Println(Version())
		return
	}
```

`fmt` is already imported in `main.go`.

Create `backend/version_test.go` with a table-driven `TestVersion` covering: the
default (`"dev"`), an injected value returned verbatim, and an empty string
falling back to `"dev"`. Set the package var directly in the test and restore it
with `t.Cleanup`.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./... -run TestVersion -v
```
→ no gofmt/vet output, `PASS` with 3 subtests.

### Step 2: Inject it in every build path

Add `-X main.version=…` to each. **The value comes from the git tag**, with a
sensible fallback:

- `Dockerfile` — add `ARG VERSION=dev` before the build stage's `RUN`, and
  extend the existing ldflags to `-ldflags="-s -w -X main.version=${VERSION}"`.
- `.github/workflows/docker-publish.yml` — pass it through to the build:
  add `build-args: VERSION=${{ github.ref_name }}` to the build-push step.
- `.github/workflows/release.yml` — extend the existing ldflags to
  `-ldflags="-s -w -X main.version=${{ github.ref_name }}"`.
- `backend/Makefile` — the local path. Use git when available, `dev` otherwise:

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -o main -tags="prod" -ldflags="-X main.version=$(VERSION)" .
```

**Do not** add a 5th hand-maintained version constant anywhere. The repo already
carries a documented four-place lockstep chore for the Go toolchain pin; do not
create a second one.

**Verify**:
```
cd backend && go build -ldflags="-X main.version=v9.9.9" -o /tmp/gwng-vtest . && /tmp/gwng-vtest -version
```
→ prints exactly `v9.9.9`. Then `go build -o /tmp/gwng-vtest2 . && /tmp/gwng-vtest2 -version` → prints `dev`.

### Step 3: Surface the version to the browser

`backend/schema/config.go` — add one field to `ConfigResponse`:

```go
	// Version is the running build's release identity, injected at build time.
	// Not from garage.toml, so NewConfigResponse leaves it empty — the handler
	// fills it, exactly as it fills Sharing.
	Version string `json:"version"`
```

`backend/router/config.go`:

```go
	resp.Version = Version()
```

Extend `backend/schema/config_test.go` with a case asserting
`NewConfigResponse` leaves `Version` empty (it is not a `garage.toml` field),
modelled on the existing `TestNewConfigResponseKeepsWebFields`. **The existing
test that scans the marshalled body for `rpc_secret` / `admin_token` /
`metrics_token` must still pass unmodified** — confirm it ran.

Mirror it in `src/types/garage.ts`:

```ts
export type Config = {
  s3_api?: S3API;
  s3_web?: S3Web;
  sharing?: boolean;
  /** Running build's version, e.g. "v3.3.0" or "dev". */
  version?: string;
};
```

Optional (`?`) because older servers will not send it.

**Verify**: `cd backend && go test -race ./schema/...` → `ok`; `pnpm run typecheck` → exit 0.

### Step 4: The update check — opt-in, server-side, cached

**Three decisions, each with a reason. Do not "simplify" past them.**

1. **Opt-in, default OFF.** A self-hosted service must not make outbound calls
   nobody asked for. Gate on `UPDATE_CHECK_ENABLED` (default `"false"`), read
   with `utils.GetEnv`, exactly like `SESSION_COOKIE_SECURE`.
2. **Server-side, not browser-side.** If the browser called GitHub directly,
   every user's IP would be exposed to GitHub and the operator could not disable
   it centrally. The backend calls; the browser asks the backend.
3. **Cached, with a hard timeout.** GitHub's unauthenticated limit is 60
   requests/hour/IP. Cache the result in `utils.Cache` for **6 hours** and give
   the outbound request a **5-second** `context.WithTimeout`. A failed or
   disabled check must degrade to "unknown", never to an error banner.

Create `backend/router/update.go`:

```go
// GET /update-check — reports whether a newer release exists.
//
// Disabled by default. This is the only outbound request this service makes to
// anything other than the configured Garage cluster, so it is opt-in
// (UPDATE_CHECK_ENABLED=true) and the URL is a compile-time constant — never
// built from user input, and never proxied on a caller's behalf.
const releasesURL = "https://api.github.com/repos/d7eeem/garage-webui-ng/releases/latest"
```

Handler shape:

- `UPDATE_CHECK_ENABLED != "true"` → `utils.ResponseSuccess` with
  `{"enabled": false, "current": Version()}`. **Status 200, not an error** — a
  disabled feature is not a failure.
- Cache hit → return it.
- Otherwise `GET` the constant URL with a 5 s timeout, decode only
  `{"tag_name": string, "html_url": string}`, cache for 6 h, return
  `{"enabled": true, "current": …, "latest": …, "url": …, "updateAvailable": bool}`.
- On any network/decode error: log with `log.Printf` and return
  `{"enabled": true, "current": …, "checkFailed": true}` at **200**. The UI shows
  the running version and nothing else. Never surface GitHub's response body.

**Version comparison — keep it dumb and correct.** Do **not** add a semver
dependency. Normalise by trimming a leading `v` from both sides and compare the
dot-separated numeric components left to right; if either side is not purely
numeric-dotted (e.g. `dev`, `v3.3.0-rc1`), report `updateAvailable: false`.
A wrong "up to date" is harmless; a wrong "update available" is noise.

Put that comparison in an exported-for-test pure function
`isNewer(current, latest string) bool` and table-test it in
`backend/router/update_test.go`: equal → false; `3.3.0` vs `3.4.0` → true;
`3.4.0` vs `3.3.0` → false; `v` prefixes on either/both; `3.3` vs `3.3.0` → false;
`dev` vs anything → false; `3.3.0-rc1` vs `3.4.0` → false; empty → false;
`3.9.0` vs `3.10.0` → **true** (the classic string-compare trap — this case is
the reason the function is numeric).

Register in `backend/router/router.go` on the **inner** router (so it inherits
auth — an unauthenticated caller must not be able to make this service emit
outbound requests):

```go
	router.HandleFunc("GET /update-check", (&Update{}).Get)
```

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./router/ -run "TestIsNewer" -v
```
→ clean, `PASS`, all subtests.

### Step 5: The About tab

`src/pages/settings/hooks.ts` — one hook:

```ts
export const useUpdateCheck = () =>
  useQuery({
    queryKey: ["update-check"],
    queryFn: () => api.get<UpdateCheck>("/update-check"),
    staleTime: 60 * 60 * 1000, // the server caches for 6h; don't re-ask per mount
    retry: false,
  });
```

Create `src/pages/settings/about-tab.tsx`:

- **Garage WebUI-NG** — `config?.version ?? "unknown"`.
- **Garage** — reuse the cluster status the app already fetches
  (`node?.garageVersion`), or omit it if that requires a new query; it is a
  nice-to-have, not the point of this tab. Do not add a query solely for it.
- When the check is enabled and a newer release exists: a restrained line —
  `Update available: {latest}` with an `<a target="_blank" rel="noreferrer">` to
  the release URL. **No modal, no banner, no red.** The repo's own guidance is
  clarity over alarm.
- When disabled: `Update checks are off. Set UPDATE_CHECK_ENABLED=true to enable them.` in muted text.
- When `checkFailed`: `Could not reach GitHub to check for updates.` in muted text.

Register it in `src/pages/settings/page.tsx` as a third tab, `title: "About"`,
with an `InfoIcon` from `lucide-react`.

Create `about-tab.test.tsx`, mocking `useConfig` and `useUpdateCheck`:

1. Renders the version from config.
2. Renders `unknown` when config has no version (older server).
3. Shows the update line when `updateAvailable: true`, with a link to the URL.
4. Shows **no** update line when `updateAvailable: false`.
5. Shows the disabled hint when `enabled: false`.

**Verify**: `pnpm run typecheck && pnpm run test && pnpm run build` → all exit 0.

### Step 6: Stop the version drift that motivated this

Add one step to `.github/workflows/ci.yml` that runs **only on tag builds** — or,
if that is awkward in the existing job layout, add it to `release.yml` before the
build — asserting `package.json`'s version matches the tag:

```yaml
      - name: Verify package.json matches the tag
        if: startsWith(github.ref, 'refs/tags/v')
        run: |
          TAG="${GITHUB_REF_NAME#v}"
          PKG=$(node -p "require('./package.json').version")
          if [ "$TAG" != "$PKG" ]; then
            echo "::error::tag v$TAG but package.json says $PKG"
            exit 1
          fi
```

This is the guard that would have caught `v3.2.0` being tagged on a tree reading
`3.1.0`. It only fails a **release**; ordinary pushes are unaffected.

Document `UPDATE_CHECK_ENABLED` in `README.md`'s env-var table, matching the
existing row style, and note that it is off by default and makes one outbound
request to GitHub every 6 hours when on.

**Verify**:
```
grep -n "UPDATE_CHECK_ENABLED" README.md .env.example
```
→ a hit in each. (Add it to `.env.example` too — the repo has a history of
documented-but-unsettable variables.)

### Step 7: Prove the tests can fail

1. Change `isNewer` to compare with `>` on strings instead of numerically. Run
   `go test ./router/ -run TestIsNewer` → the `3.9.0` vs `3.10.0` case **must
   fail**. Revert.
2. Change `Version()` to return `version` without the empty fallback. Run
   `go test ./... -run TestVersion` → the empty case **must fail**. Revert.

Report both, and confirm `git status --porcelain` is clean before committing.

### Step 8: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```
All exit 0; no gofmt/vet output; all Go packages `ok`.

### Step 9: Manual checks — reviewer's job

State plainly in NOTES that these were not run:

1. `make build && ./main -version` → a git-derived version, not `dev`.
2. Plain `go build . && ./main -version` → `dev`.
3. Settings → About shows the running version.
4. With `UPDATE_CHECK_ENABLED` unset → the disabled hint; **verify with
   `tcpdump`/proxy logs that no outbound request to github.com is made**.
5. With it `true` → the check runs once, and a second page load does **not**
   produce a second outbound request (cache works).

## Done criteria

- [ ] All gates in Step 8 exit 0
- [ ] Step 7's two mutations each failed the named case, and were reverted
- [ ] `go build -ldflags="-X main.version=v9.9.9" && ./main -version` → `v9.9.9`; plain build → `dev`
- [ ] `grep -rn "main.version" Dockerfile backend/Makefile .github/workflows/` → hits in **all four** build paths
- [ ] `grep -rn "UPDATE_CHECK_ENABLED" backend/ README.md .env.example` → hits in each
- [ ] `grep -rn "api.github.com" src/` → **no matches** (the browser never calls GitHub)
- [ ] `grep -rn "semver\|Masterminds" backend/go.mod` → no new dependency
- [ ] `git diff <BASE>..HEAD -- backend/schema/config.go | grep -c NewConfigResponse` → `0` (the pure projection is untouched)
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You find yourself implementing **self-update**: downloading a binary, replacing
  the running one, restarting, or pulling an image. This plan reports only.
- You are about to add a semver library. The comparison is a dozen lines and a
  table test; a dependency is not warranted.
- You are about to make the update check default-on, unauthenticated, or
  browser-side. All three are deliberate no's — re-read Step 4.
- You are about to add a 5th hand-maintained version constant. The git tag is the
  source of truth.
- The update check's failure path surfaces GitHub's response body or an error
  status to the UI. It must degrade quietly at 200.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The git tag is the version's source of truth.** `package.json` follows it,
  and Step 6's CI guard enforces that on release builds only. If you ever need
  the version in a third place, derive it — do not copy it.
- **`UPDATE_CHECK_ENABLED` is the only outbound-request switch in this service.**
  Everything else talks solely to the configured Garage cluster. If a second
  outbound feature is ever added, it needs its own opt-in, not a rename of this
  one to something general.
- **`isNewer` is deliberately conservative**: anything it cannot parse numerically
  reports "no update". A missed notification is a non-event; a false one erodes
  trust in the indicator. Keep that bias if you extend it to pre-releases.
- **The About tab is the natural home for anything build-identifying** — commit
  SHA, build date, Go version — if that is ever wanted. It needs no new plumbing
  beyond another ldflag.
