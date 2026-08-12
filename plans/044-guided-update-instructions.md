# Plan 044: Tell the operator how to update *this* deployment

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. Touch
> only the files listed as in scope. If any STOP condition occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer who
> dispatched you maintains it.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to base on:
> ```
> git diff --stat <BASE> -- backend/router/update.go backend/router/update_test.go \
>   src/pages/settings/about-tab.tsx src/pages/settings/hooks.ts
> ```
> Then confirm every excerpt in "Current state" matches. On a mismatch, STOP.

## Status

- **Priority**: P3
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none at execution time — everything it extends is already on `main`.
- **Category**: dx + direction
- **Planned at**: commit `90a91fd` (v3.4.1 + security headers), 2026-08-10

---

## 0. Why this is guided update and NOT self-update

The brief asked for an "update mechanism". Investigation established that
self-update is **impossible in one deployment mode and a liability in the
other**, so this plan deliberately builds something else. Do not "improve" it
back into self-update.

**Docker: the process cannot write its own binary.** `Dockerfile:74` is
`COPY --from=backend /main /main` with **no** `--chown`, so `/main` is
root-owned; `Dockerfile:84` is `USER nonroot:nonroot` (uid 65532). The runtime
base is `gcr.io/distroless/static-debian12:nonroot` — no shell, no package
manager, no `docker` client. The image is immutable by design and the
orchestrator replaces it. Self-update here is not "hard", it is impossible.

**Binary: possible, but it would be a remote-code-execution path.** This process
holds the Garage **admin token**. Downloading a release asset and executing it
means any compromise of the release channel, or of the check's TLS path, becomes
cluster compromise. Doing it safely needs published checksums and a signing key
— **neither exists**: `.github/workflows/release.yml` attaches the raw binaries
via `softprops/action-gh-release` with no checksum or signature step. Building
self-update before that infrastructure would be backwards.

**So: report the exact command, never run it.** The app knows its own version,
whether an update exists (plan 041 already does this), and can determine whether
it could even write its own binary. That is enough to print the right command.

**The detection question is deliberately narrow.** Do not try to answer "am I in
Docker?" — that is unreliable across Docker, podman, Kubernetes and nerdctl.
Ask the question that actually matters: **"is my own executable writable by the
user I am running as?"** If no, this is a container or a hardened service and
the operator updates it from outside. That single check is honest, portable, and
directly tied to the thing it decides.

---

## 1. Current state

### `backend/router/update.go` — the response shape and handler (already on `main`)

```go
// UpdateCheck is both the cached value and the JSON response shape.
type UpdateCheck struct {
	Enabled         bool   `json:"enabled"`
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	URL             string `json:"url,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
	CheckFailed     bool   `json:"checkFailed,omitempty"`
}
```

```go
func (u *Update) Get(w http.ResponseWriter, r *http.Request) {
	current := AppVersion

	if utils.GetEnv("UPDATE_CHECK_ENABLED", "false") != "true" {
		utils.ResponseSuccess(w, UpdateCheck{Enabled: false, Current: current})
		return
	}

	if cached := utils.Cache.Get(updateCacheKey); cached != nil { … }

	release, err := fetchLatestRelease(r.Context())
	if err != nil { … CheckFailed: true … }

	result := UpdateCheck{
		Enabled:         true,
		Current:         current,
		Latest:          release.TagName,
		URL:             release.HTMLURL,
		UpdateAvailable: isNewer(current, release.TagName),
	}
	utils.Cache.Set(updateCacheKey, result, updateCacheTTL)
	utils.ResponseSuccess(w, result)
}
```

Note `AppVersion` is a package-level `var` in `backend/router/config.go`, set
from `main.go` at startup. `isNewer` and `parseNumericVersion` already exist in
this file — **do not touch them.**

### `src/pages/settings/hooks.ts` — the mirrored type

```ts
export type UpdateCheck = {
  enabled: boolean;
  current: string;
  latest?: string;
  url?: string;
  updateAvailable?: boolean;
  checkFailed?: boolean;
};
```

```ts
export const useUpdateCheck = () =>
  useQuery({
    queryKey: ["update-check"],
    queryFn: () => api.get<UpdateCheck>("/update-check"),
    staleTime: 60 * 60 * 1000,
    retry: false,
  });
```

### `src/pages/settings/about-tab.tsx` — the render (whole file is short; read it)

It renders the version row, then three conditional blocks:
`update?.updateAvailable && update.latest` (the "Update available" line with a
release link), `update && !update.enabled` (the disabled hint), and
`update?.checkFailed`. You are adding a fourth block and changing nothing else.

### Conventions

- **Go handlers**: methods on empty structs, `(w, r)`, ending in `utils.ResponseSuccess` / `utils.ResponseError`. **`utils.ResponseError` does NOT stop the handler — always `return` after it.**
- Env reads: `utils.GetEnv(name, default)`.
- Go tests: plain `testing`, table-driven. `TestIsNewer` in `backend/router/update_test.go` is the exact shape to copy.
- Frontend: `copyToClipboard` from `@/lib/utils` is the established copy helper (used by the share dialog and upload card). Icons from `lucide-react`.
- Component tests: `@testing-library/react` with `vi.hoisted` + `vi.mock`; `src/pages/settings/about-tab.test.tsx` **already exists** — extend it, do not create a second file.
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
directory. Do not substitute `npm`. If `go` is not on PATH:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(Debian-based — `-race` needs gcc; `git` is unusable inside it).

## Scope

**In scope:**
- `backend/router/update.go` — deployment detection + two new response fields
- `backend/router/update_test.go` — extend
- `src/pages/settings/hooks.ts` — mirror the two fields
- `src/pages/settings/about-tab.tsx` — render the command
- `src/pages/settings/about-tab.test.tsx` — extend
- `README.md` — a short "Updating" note

**Out of scope — do NOT touch:**
- **Anything that downloads, replaces, restarts, or executes.** No `os.Exec`, no writing to the executable, no `syscall.Exec`, no pulling images. Re-read §0.
- `isNewer`, `parseNumericVersion`, `fetchLatestRelease`, the cache TTL, the timeout, `UPDATE_CHECK_ENABLED` semantics — all correct and tested.
- `backend/version.go`, `AppVersion`, the ldflags wiring, the CI version guard.
- `backend/middleware/` — including the security headers just added.
- Any new environment variable. The detection is automatic; an operator override is **not** wanted (it would be a footgun that prints the wrong command).
- Adding a dependency of any kind.

## Git workflow

- Branch: `advisor/044-guided-update-instructions` from your given base.
- Conventional commits, e.g. `feat: show the update command for the running deployment`.
- Do NOT push, open a PR, or merge.

---

## Steps

### Step 1: Detect whether this deployment is self-updatable

Add to `backend/router/update.go`, near the other helpers:

```go
// DeploymentKind describes how this instance was deployed, to the only degree
// of precision that matters: whether the operator updates it in place or from
// outside.
type DeploymentKind string

const (
	// deploymentBinary: the running executable is writable by this process's
	// user, so the operator replaces the file and restarts the service.
	deploymentBinary DeploymentKind = "binary"
	// deploymentManaged: the executable is not writable by us — a container
	// image, or a service whose binary is root-owned or on a read-only mount.
	// The operator updates it from outside.
	deploymentManaged DeploymentKind = "managed"
	// deploymentUnknown: we could not determine our own path.
	deploymentUnknown DeploymentKind = "unknown"
)

// detectDeployment reports how this instance should be updated.
//
// It deliberately does NOT try to answer "am I in Docker?" — that is
// unreliable across Docker, podman, Kubernetes and nerdctl, and it is the wrong
// question. The question that decides the advice is whether we could replace
// our own executable at all, so that is what we test: can the current user
// write the file we are running from?
//
// Fails to "managed" on any doubt: telling a container operator to overwrite a
// binary is worse advice than telling a binary operator to check their setup.
func detectDeployment() DeploymentKind { … }
```

Implement with `os.Executable()`, then `os.OpenFile(path, os.O_WRONLY, 0)` —
**open for write and immediately close; never truncate, never write a byte.**

- `os.Executable()` error → `deploymentUnknown`
- open succeeds → close it, return `deploymentBinary`
- open fails with a permission error → `deploymentManaged`
- open fails any other way → `deploymentManaged` (fail safe)

**Do not** use `unix.Access` or a mode-bits check — the effective answer depends
on ownership, mount flags (`ro`), and security modules, and only an actual open
accounts for all three.

Extract the classification into a pure, testable helper so the test does not
need a real read-only file:

```go
// classifyOpenResult turns the outcome of the write-probe into a kind.
func classifyOpenResult(execErr, openErr error) DeploymentKind { … }
```

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./...
```
→ no output, exit 0.

### Step 2: Return the deployment kind and the command

Extend the response struct — **append fields, do not reorder or rename**
existing ones (the frontend and the cached value both depend on the shape):

```go
	// Deployment is how this instance should be updated: "binary", "managed"
	// or "unknown". See detectDeployment.
	Deployment string `json:"deployment,omitempty"`
	// UpdateCommand is the shell command an operator runs to update THIS
	// deployment. Informational only — this service never executes it.
	UpdateCommand string `json:"updateCommand,omitempty"`
```

Add a pure function:

```go
// updateCommandFor returns the shell command that updates a deployment of the
// given kind. Informational only: nothing in this service ever runs it.
func updateCommandFor(kind DeploymentKind) string { … }
```

Returning:

- `deploymentBinary` → a **single line**, the only case where we know the
  command is right:
  ```
  sudo systemctl stop garage-webui && sudo install -m 0755 ./garage-webui-ng /usr/local/bin/garage-webui-ng && sudo systemctl start garage-webui
  ```
- `deploymentManaged` → **`""`** (empty)
- `deploymentUnknown` → **`""`** (empty)

> **AMENDED 2026-08-11 — do not restore the Docker command here.**
> An earlier draft returned `docker compose pull && docker compose up -d` for
> `managed`. That is wrong, and it is wrong for the most common *correct*
> deployment. `managed` only means "this process cannot write its own
> executable", which is equally true of a container **and** of a properly
> deployed systemd service whose binary is root-owned and whose unit runs as a
> non-root user — more so under `ProtectSystem=strict`, which makes the
> filesystem read-only to the process. Emitting a `docker compose` line there
> tells a systemd operator to run a command that does not apply to their host.
>
> The perverse consequence of the old mapping: the `binary` branch only fires
> when the service can overwrite its own executable, i.e. mostly when running
> as root — so the **least safe** configuration got the **most useful** advice,
> and every hardened one got advice that was simply false.
>
> We cannot tell a container from a hardened service without exactly the
> runtime sniffing §0 rejects, so **we do not guess**. `managed` gets prose in
> the UI that is true either way (Step 4), not a copyable command.

Keep the `binary` string **single-line** — the UI renders it in a
copy-to-clipboard block, and a multi-line string with a `\` continuation copies
badly. Do not prefix it with a `# download the release binary first` comment
line; that would break single-line copying. The UI supplies that context in
prose.

**Populate the fields in all three response paths** of `Get` — the disabled
path, the check-failed path, and the success path. An operator who has updates
switched off still benefits from knowing the command. Do **not** populate them
inside the cached value: call `detectDeployment()` per request and set the
fields after reading the cache, so a cached `UpdateCheck` never freezes a stale
deployment kind. This matters if someone changes the binary's permissions or
redeploys without restarting.

> **Where this is easy to get wrong**: the cache stores an `UpdateCheck`. If you
> set `Deployment` before caching, the cached copy carries it and the "set after
> reading the cache" rule is silently pointless. Set the two new fields on the
> value you are about to write to the response, in every branch, *after* any
> cache read.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./router/ -v -run "TestUpdateCommandFor|TestClassifyOpenResult"
```
→ clean, `PASS`.

### Step 3: Tests (Go)

Extend `backend/router/update_test.go`, table-driven, modelled on the existing
`TestIsNewer`:

1. **`TestClassifyOpenResult`** — `execErr` non-nil → `unknown`; both nil →
   `binary`; `openErr = fs.ErrPermission` → `managed`; `openErr` = some other
   error → `managed`. *(Use `errors.New` for the generic case.)*
2. **`TestUpdateCommandFor`** — `binary` → contains `systemctl` and `install`;
   **`managed` → exactly `""`**; `unknown` → `""`; and **every returned string
   contains no newline**, since the UI copies it as one line. Assert the
   `managed` case as an explicit equality against `""` **and** add a guard that
   it does not contain `docker` — that is the regression guard for the amended
   behaviour, and it must fail loudly if anyone reintroduces a guessed command.
3. **`TestDeploymentFieldsAreSetWhenDisabled`** — with `UPDATE_CHECK_ENABLED`
   unset, serve `Get` through `httptest` and assert the JSON still carries a
   non-empty `deployment`. This is the regression guard for the "populate in all
   three paths" requirement. Use `t.Setenv`.

> The existing tests in this file stand up an `httptest` server and point
> `releasesURL` at it — reuse that pattern for any handler test, and restore
> `releasesURL` with `t.Cleanup`.

**Verify**: `cd backend && go test -race ./router/ -v -run "TestClassify|TestUpdateCommand|TestDeploymentFields"` → all `PASS`.

### Step 4: Surface it in the About tab

`src/pages/settings/hooks.ts` — append to the `UpdateCheck` type, matching the
JSON names exactly:

```ts
  deployment?: "binary" | "managed" | "unknown";
  updateCommand?: string;
```

`src/pages/settings/about-tab.tsx` — add **two mutually exclusive** blocks,
directly after the existing "Update available" block. Which one renders is
decided by `deployment`, and the wording is where the amendment lives.

**(a) When `update?.updateCommand` is a non-empty string** (i.e. `binary`):

- A muted label: `To update this deployment:`
- The command in a `<code>` block that wraps rather than overflows —
  `block whitespace-pre-wrap break-all rounded bg-base-200 p-2 text-xs`
- A copy button using `copyToClipboard` from `@/lib/utils` and the `Copy` icon
  from `lucide-react`, with `aria-label="Copy update command"`
- Above the code block, one muted sentence for the context the single-line
  command omits: `Download the release binary first, then:`

**(b) When `update?.deployment === "managed"`** — render **prose only, no code
block and no copy button**:

> This deployment is updated from outside the app — replace the container image
> or the binary and restart the service.

That sentence is true of a container *and* of a hardened systemd unit, which is
the whole point of the amendment: `managed` means only "this process cannot
write its own executable", and we refuse to guess which of the two it is. **Do
not** add a `docker compose` example here, not even as an illustration — an
operator will copy it.

When `deployment` is `"unknown"`, render neither block.

**Show it whenever a command exists**, not only when an update is available —
an operator checking how to update should not have to wait for a release. Do
**not** add a confirm step, a "run" button, or anything that suggests the app
will act.

Leave the three existing conditional blocks exactly as they are.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step 5: Tests (frontend)

Extend the **existing** `src/pages/settings/about-tab.test.tsx` — do not create
a new file. It already mocks `useConfig` and `useUpdateCheck`; add cases:

1. Renders the command when `updateCommand` is present.
2. Renders **no** command block when `updateCommand` is absent or `""`.
3. The copy button calls `copyToClipboard` with the exact command string. Mock
   `@/lib/utils`, preserving its other exports with `importOriginal` so the
   component's other helpers keep working.

**Verify**: `pnpm exec vitest run about-tab` → all pass, including the
pre-existing cases.

### Step 6: Document it

Add a short **Updating** subsection to `README.md` near the installation
section: the About tab shows the running version, whether a newer release
exists (when `UPDATE_CHECK_ENABLED=true`), and the exact command for the
detected deployment. State plainly that **the app never updates itself** — it
holds a Garage admin token, and downloading and executing code in that process
is a risk the project deliberately does not take.

**Verify**: `grep -n "never updates itself\|Updating" README.md` → matches.

### Step 7: Prove the tests can fail

1. Make `updateCommandFor` return a `docker compose pull …` string for
   `managed` → `go test ./router/ -run TestUpdateCommandFor` → **must fail**
   (both the equality and the no-`docker` guard). Revert. This is the guard on
   the amendment.
2. Make `classifyOpenResult` return `binary` on a permission error →
   `go test ./router/ -run TestClassifyOpenResult` → **must fail**. Revert.
   *(This is the fail-safe direction; it must be pinned.)*
3. Stop populating the fields on the disabled path →
   `go test ./router/ -run TestDeploymentFields` → **must fail**. Revert.

Report all three, and confirm `git status --porcelain` is clean before committing.

### Step 8: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

### Step 9: Manual checks — reviewer's job

You have neither a browser nor a container here. Do **not** claim these passed;
list them in NOTES:

1. Run the binary locally from a writable path → About shows the **binary**
   command (`systemctl` / `install`).
2. Run the Docker image → About shows the **managed prose**, no code block and
   no copy button. This is the detection actually being exercised: the image
   runs as uid 65532 against a root-owned `/main`.
2b. **The case that motivated the amendment**: run the binary under a unit with
   `ProtectSystem=strict`, or simply as a non-root user against a root-owned
   binary. It must classify as **managed** and show the prose — never a
   `docker compose` line. This is the maintainer's own deployment shape.
3. The copy button puts exactly the displayed string on the clipboard, on one
   line.
4. With `UPDATE_CHECK_ENABLED` unset, the command still appears alongside the
   "checks are off" hint.

## Done criteria

- [ ] All gates in Step 8 exit 0
- [ ] Step 7's three mutations each failed the named case, and were reverted
- [ ] `grep -rnE "os/exec|syscall.Exec|os.Create|io.Copy" backend/router/update.go` → **no matches** (nothing downloads, writes or executes)
- [ ] `grep -c "O_WRONLY" backend/router/update.go` → `1`, and no `O_TRUNC` / `O_CREATE` anywhere in the file
- [ ] `git diff <BASE>..HEAD -- backend/version.go backend/middleware/ .github/` is **empty**
- [ ] `grep -n "isNewer\|parseNumericVersion" backend/router/update.go` still present and unmodified (`git diff` shows no hunk inside either)
- [ ] `git diff --stat <BASE>..HEAD` lists only in-scope files

## STOP conditions

- Any "Current state" excerpt does not match — the branch drifted.
- You find yourself writing code that downloads a release asset, writes to the
  executable, calls `os/exec`, or restarts the process. Re-read §0; that is a
  different project and it needs release signing that does not exist yet.
- You are about to add an environment variable to override the detection. Not
  wanted — an override that is wrong prints a command that breaks someone's
  deployment.
- The write-probe truncates or modifies the executable. It must open with
  `O_WRONLY` **only**, and close immediately. If you cannot do that safely on
  this platform, report instead.
- `detectDeployment` returns `binary` inside the container image. That inverts
  the fail-safe and would tell a container operator to overwrite a file they
  cannot write; investigate rather than adjusting the test.
- A verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **The detection answers "can I write my own binary", not "am I in Docker".**
  That is deliberate: it is portable across container runtimes and it is the
  precondition that actually matters. If someone later "improves" it into
  runtime sniffing, they will get Kubernetes and podman wrong.
- **It fails safe to `managed`.** Advising a container operator to overwrite a
  binary is worse than advising a binary operator to double-check — keep that
  bias if you extend it.
- **The two new fields are set per request, never cached**, so a redeploy or a
  permissions change is reflected without waiting out the 6-hour update cache.
- **If self-update is ever revisited**, the prerequisite is release integrity,
  not UI: published checksums and a signing key wired into
  `.github/workflows/release.yml`, which currently attaches raw binaries with
  neither. Until that exists, this process holding the Garage admin token must
  not execute anything it downloaded.
- **`managed` deliberately carries no command.** It means only "cannot write my
  own executable", which covers containers *and* correctly-hardened systemd
  services. Distinguishing them needs the runtime sniffing §0 rejects, so the
  UI says something true of both instead. Reintroducing a `docker compose`
  default would mis-advise every non-root binary install — the common, correct
  case.
- **The binary command names `garage-webui` as the unit** and
  `/usr/local/bin/garage-webui-ng` as the path — the conventional install from
  the README. An operator who deployed elsewhere will need to adapt it; that is
  acceptable for a copyable hint and better than a wrong override knob.
