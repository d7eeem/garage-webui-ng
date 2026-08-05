# Plan 002: Establish a verification baseline (tests, typecheck, CI)

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- package.json tsconfig.app.json vite.config.ts backend/`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none
- **Category**: tests
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

This repository has zero automated tests — no Go tests, no frontend tests — no
CI workflow, and no standalone typecheck command. The only quality gate is
`pnpm run build`, which happens to run `tsc -b` as a side effect of building.

That means every change to this codebase ships blind. The code handles
destructive operations (recursive object deletion, cluster layout changes,
bucket deletion) against real object storage. Several other plans in this
directory modify exactly those paths. Without a regression net, a subtle
mistake in a delete path is discovered by a user losing data.

This plan is the prerequisite for the riskier plans (003, 004, 006). It does not
fix any bug. It creates: a runnable Go test suite, a runnable frontend test
suite with two real tests, a `typecheck` script, and a CI workflow that runs all
of it on push and pull request.

Scope discipline matters here: **do not fix bugs you notice while writing
tests.** If a test you write fails against current behavior, that is a finding —
report it, mark the test skipped with a comment pointing at the plan that fixes
it, and move on. Other plans own those fixes.

## Current state

### Files

- `package.json` — scripts and dependencies. No `test`, no `typecheck`.
- `tsconfig.app.json` — the app TS project. Strict mode on.
- `vite.config.ts` — Vite config. No `test` section.
- `backend/` — Go module `khairul169/garage-webui`, Go 1.23. No `_test.go` files.
- `.github/` — **does not exist**. You will create it.

### Excerpts

`package.json:1-13` — current scripts:

```json
{
  "name": "garage-webui",
  "private": true,
  "version": "1.1.0",
  "type": "module",
  "scripts": {
    "dev:client": "vite",
    "build": "tsc -b && vite build",
    "lint": "eslint .",
    "preview": "vite preview",
    "dev:server": "cd backend && air",
    "dev": "concurrently \"npm run dev:client\" \"npm run dev:server\""
  },
```

`vite.config.ts` — the whole file:

```ts
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react-swc";
import path from "path";

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  process.env = { ...process.env, ...loadEnv(mode, process.cwd()) };

  return {
    plugins: [react()],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "src"),
      },
    },
    server: {
      proxy: {
        "/api": {
          target: process.env.VITE_API_URL,
          changeOrigin: true,
        },
      },
    },
  };
});
```

Note the `@` → `src` alias. Tests must resolve it; because tests will run
through Vite's config, they inherit it automatically.

`backend/Makefile` — the whole file:

```make

build:
	CGO_ENABLED=0 go build -o main -tags="prod" main.go

run:
	go run main.go
```

Note `-tags="prod"`. The `prod` build tag selects `backend/ui/ui_prod.go`, which
contains `//go:embed dist`. **That directory does not exist in the repo** — it is
produced by the frontend build and copied in by the Dockerfile. Therefore
`go build -tags=prod` fails on a clean checkout, but `go build ./...` and
`go test ./...` (no tags) succeed, because the non-prod `backend/ui/ui.go` is
selected instead. CI must not use the `prod` tag for the test job.

`backend/ui/ui.go` — the whole file, confirming the non-prod stub:

```go
//go:build !prod
// +build !prod

package ui

import "net/http"

func ServeUI(mux *http.ServeMux) {}
```

`Dockerfile:14-23` — confirms the Go version and build sequence CI should match:

```dockerfile
FROM golang:1.23 AS backend
WORKDIR /app

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
COPY --from=frontend /app/dist ./ui/dist
RUN make
```

`backend/go.mod:1-5`:

```
module khairul169/garage-webui

go 1.23.0

toolchain go1.24.0
```

`tsconfig.app.json` compiler options that affect tests — `strict`,
`noUnusedLocals`, `noUnusedParameters`, `"include": ["src"]`:

```json
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,

    "baseUrl": ".",
    "paths": {
      "@/*": ["src/*"],
    }
  },
  "include": ["src"]
```

Because `include` is `["src"]`, test files placed under `src/` are typechecked.
That is what we want.

### Repo conventions to match

- **Package manager is pnpm.** `pnpm-lock.yaml` is committed; the Dockerfile
  uses corepack + `pnpm install --frozen-lockfile`. Use `pnpm add -D`, never
  `npm install`.
- **Frontend module style**: ESM (`"type": "module"`), TypeScript, `.tsx` for
  components. Imports use the `@/` alias for anything under `src/`
  (e.g. `import api from "@/lib/api"`). Match that in tests.
- **Go style**: stdlib-only where possible. The existing dependencies are all
  runtime concerns (AWS SDK, TOML, bcrypt, sessions) — there is no assertion
  library and you should not add one. Use plain `testing` with `t.Errorf` /
  `t.Fatalf`.
- **Go layout**: packages are `router`, `utils`, `schema`, `middleware`, `ui`.
  Tests go beside the code they test, in the same package.

## Commands you will need

| Purpose         | Command                                    | Expected on success        |
|-----------------|--------------------------------------------|----------------------------|
| Frontend deps   | `pnpm install`                             | exit 0                     |
| Add a dev dep   | `pnpm add -D <pkg>`                        | exit 0, lockfile updated   |
| Frontend build  | `pnpm run build`                           | exit 0                     |
| Lint            | `pnpm run lint`                            | exit 0                     |
| Typecheck (new) | `pnpm run typecheck`                       | exit 0, no errors          |
| Frontend tests  | `pnpm run test`                            | all pass                   |
| Go build        | `cd backend && go build ./...`             | exit 0                     |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output          |
| Go tests        | `cd backend && go test ./...`              | `ok` per package           |
| Go format check | `cd backend && gofmt -l .`                 | no output                  |

If `go` or `pnpm` is not installed in your environment, see STOP conditions.

## Scope

**In scope** (the only files you should modify or create):

- `package.json` (modify — scripts and devDependencies)
- `pnpm-lock.yaml` (modify — regenerated by `pnpm add`)
- `vite.config.ts` (modify — add the `test` section)
- `src/test/setup.ts` (create)
- `src/lib/utils.test.ts` (create)
- `src/lib/api.test.ts` (create)
- `backend/utils/utils_test.go` (create)
- `backend/utils/cache_test.go` (create)
- `.github/workflows/ci.yml` (create)
- `README.md` (modify — the Testing subsection added in step 9)
- `.gitignore` (modify — only if step 1 produces untracked `*.tsbuildinfo` files)

**Out of scope** (do NOT touch, even though they look related):

- **Any production source file.** Not `src/lib/utils.ts`, not
  `backend/utils/cache.go`, none of it. This plan adds tests around existing
  behavior; it changes no behavior. If a test reveals a bug, see "Why this
  matters" — report it, skip the test, move on.
- `eslint.config.js` — the lint setup works. Adding test-file overrides is a
  separate concern and risks breaking the existing lint run.
- `tsconfig.app.json` — `include: ["src"]` already covers test files under
  `src/`. Do not add a separate test tsconfig.
- `backend/Makefile` — the `prod`-tagged build target is correct for releases.
  CI calls `go test` directly instead.
- `Dockerfile` — release build, unrelated.

## Git workflow

- Branch: `advisor/002-verification-baseline`
- The repo uses conventional commits. Recent examples from `git log`:
  `feat: add authentication`, `fix: browser for bucket handle https connection`,
  `chore: bump version to 1.1.0`.
- Suggested commits: `chore: add typecheck script`, `test: add go and vitest
  suites`, `ci: add build and test workflow`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add a standalone typecheck script

In `package.json`, add one entry to `scripts`:

```json
    "typecheck": "tsc -b",
```

Place it after `"build"`. Do not modify any other script.

Note: `tsc -b` (build mode, using the project references in `tsconfig.json`) is
exactly what `build` already runs, so this checks both `tsconfig.app.json` and
`tsconfig.node.json`. **Do not add `--noEmit`** — both referenced tsconfigs
already set `"noEmit": true` in their own `compilerOptions`, so nothing is
emitted, and older TypeScript versions reject `--build` combined with
`--noEmit` outright.

`tsc -b` writes `.tsbuildinfo` files. Check whether they land in the repo:

```bash
git status --short
```

If an untracked `*.tsbuildinfo` appears, add a line for it to `.gitignore` —
that counts as in scope for this plan.

**Verify**:

```bash
pnpm install && pnpm run typecheck
```

→ exit 0, no errors. If it reports pre-existing type errors, see STOP
conditions.

### Step 2: Add Vitest and its dependencies

```bash
pnpm add -D vitest@^2 jsdom @testing-library/react @testing-library/jest-dom @testing-library/user-event
```

Rationale for each: `vitest` is the natural test runner for a Vite project (it
reuses `vite.config.ts`, so the `@/` alias and the SWC React plugin work with no
extra configuration); `jsdom` provides the DOM environment; the Testing Library
packages are for the component tests that later plans will add.

**Verify**:

```bash
pnpm exec vitest --version
```

→ prints a version number, exit 0.

### Step 3: Wire Vitest into the Vite config

`vite.config.ts` is a function-form config. Add a `test` key to the returned
object, as a sibling of `plugins` / `resolve` / `server`:

```ts
    test: {
      environment: "jsdom",
      globals: true,
      setupFiles: ["./src/test/setup.ts"],
      include: ["src/**/*.{test,spec}.{ts,tsx}"],
    },
```

The file needs a Vitest type reference so `test` typechecks. Add this as the
**first line** of `vite.config.ts`:

```ts
/// <reference types="vitest/config" />
```

Then create `src/test/setup.ts`:

```ts
import "@testing-library/jest-dom/vitest";
```

Add the test script to `package.json`, after `"typecheck"`:

```json
    "test": "vitest run",
    "test:watch": "vitest",
```

**Verify**:

```bash
pnpm run test
```

→ exits 0 with "No test files found" (you have not written any yet). If it
errors on config resolution instead, fix that before continuing.

Then:

```bash
pnpm run typecheck
```

→ exit 0. `vite.config.ts` is covered by `tsconfig.node.json`, so a bad `test`
key would surface here.

### Step 4: Write frontend tests for `src/lib/utils.ts`

Create `src/lib/utils.test.ts`. Test the pure functions in `src/lib/utils.ts`.
Here is the current source of the two worth testing:

```ts
export const ucfirst = (text?: string | null) => {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : null;
};

export const readableBytes = (bytes?: number | null, divider = 1024) => {
  if (bytes == null || Number.isNaN(bytes)) return "n/a";

  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.max(0, Math.floor(Math.log(bytes) / Math.log(divider)));

  return `${(bytes / Math.pow(divider, i)).toFixed(1)} ${sizes[i]}`;
};
```

Write these cases, asserting **current** behavior:

- `ucfirst("hello")` → `"Hello"`
- `ucfirst("")` → `null`
- `ucfirst(undefined)` → `null`
- `ucfirst(null)` → `null`
- `readableBytes(null)` → `"n/a"`
- `readableBytes(undefined)` → `"n/a"`
- `readableBytes(NaN)` → `"n/a"`
- `readableBytes(0)` → `"0.0 B"`
- `readableBytes(1024)` → `"1.0 KB"`
- `readableBytes(1536)` → `"1.5 KB"`
- `readableBytes(1000, 1000)` → `"1.0 KB"` (the `divider = 1000` path used for
  node capacities in `src/pages/cluster/components/nodes-list.tsx:237`)

Then add **one** case documenting a known limitation, written as a
`it.skip(...)` with a comment:

```ts
  // Known gap: `sizes` stops at "TB", so anything >= 1 PB indexes out of
  // bounds and renders "undefined". Not fixed here — this plan adds tests
  // only. Un-skip when the sizes array is extended.
  it.skip("formats petabyte-scale values", () => {
    expect(readableBytes(1024 ** 5)).toBe("1.0 PB");
  });
```

Run each expected value against the real implementation before committing — if
one of the non-skipped assertions above does not match actual behavior, trust
the implementation, correct the test, and note the discrepancy in your report.

**Verify**: `pnpm run test` → 11 passing, 1 skipped.

### Step 5: Write a frontend test for the API client's error handling

Create `src/lib/api.test.ts`. The subject is `src/lib/api.ts`. Its current
response handling, which is what you are pinning down:

```ts
    const isJson = res.headers
      .get("Content-Type")
      ?.includes("application/json");
    const data = isJson ? await res.json() : await res.text();

    if (res.status === 401 && !url.startsWith("/auth")) {
      window.location.href = utils.url("/auth/login");
      throw new APIError("unauthorized", res.status);
    }

    if (!res.ok) {
      const message = isJson
        ? data?.message
        : typeof data === "string"
        ? data
        : res.statusText;
      throw new APIError(message, res.status);
    }

    return data as unknown as T;
```

Stub `globalThis.fetch` with `vi.fn()` in a `beforeEach`, and restore it in an
`afterEach` via `vi.restoreAllMocks()`. Cases:

- **JSON success**: fetch resolves 200 with `Content-Type: application/json` and
  body `{"ok":true}` → `api.get("/x")` resolves to `{ ok: true }`.
- **Plain-text error**: fetch resolves 500 with no JSON content type and body
  `"boom"` → `api.get("/x")` rejects with an `APIError` whose `message` is
  `"boom"` and `status` is `500`. This is the shape the Go backend actually
  produces: `backend/utils/utils.go:21-24` writes `err.Error()` as a bare body
  with no `Content-Type` header.
- **Credentials are sent**: assert the second argument passed to `fetch`
  includes `credentials: "include"`. Session auth depends on this.
- **Query params**: `api.get("/x", { params: { a: 1 } })` results in a fetch URL
  whose search string is `?a=1`.

Do **not** test the 401 branch — it assigns `window.location.href`, which jsdom
handles inconsistently across versions and would make the suite flaky. Note the
omission in a comment.

**Verify**: `pnpm run test` → 15 passing, 1 skipped.

### Step 6: Write Go tests for `backend/utils`

Create `backend/utils/utils_test.go`, package `utils`. Current source under
test (`backend/utils/utils.go:9-19`):

```go
func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}
	return value
}

func LastString(str []string) string {
	return str[len(str)-1]
}
```

Cases:

- `GetEnv` returns the default when the variable is unset. Use `t.Setenv` for
  isolation — it restores automatically.
- `GetEnv` returns the value when set.
- `GetEnv` returns the **default** when the variable is set to the empty string
  (this is current behavior — `len(value) == 0`).
- `LastString([]string{"a", "b", "c"})` → `"c"`.
- `LastString([]string{"only"})` → `"only"`.

Then document the known crash, skipped:

```go
func TestLastStringEmptySlice(t *testing.T) {
	t.Skip("LastString panics on an empty slice; not fixed in this plan")
	// LastString([]string{}) indexes str[-1] and panics.
}
```

Also add `backend/utils/cache_test.go`, package `utils`, covering
`backend/utils/cache.go`:

```go
func (c *CacheManager) Set(key string, value interface{}, ttl time.Duration) {
	c.cache.Store(key, CacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	})
}

func (c *CacheManager) Get(key string) interface{} {
	entry, ok := c.cache.Load(key)
	if !ok {
		return nil
	}

	cacheEntry := entry.(CacheEntry)
	if cacheEntry.expiresAt.Before(time.Now()) {
		c.cache.Delete(key)
		return nil
	}

	return cacheEntry.value
}
```

Note: `Cache` is a package-level singleton initialized by `InitCacheManager()`.
Call `InitCacheManager()` at the start of each test to get a clean map.

Cases:

- Set then Get returns the stored value.
- Get on a missing key returns `nil`.
- A key set with a negative TTL (already expired) returns `nil` on Get, and a
  second Get still returns `nil` (confirms the delete-on-read path).
- Concurrent `Set` and `Get` from 50 goroutines each do not panic — run this one
  under `go test -race`.

**Verify**:

```bash
cd backend && go test ./utils/... && go test -race ./utils/...
```

→ `ok` both times.

### Step 7: Confirm the whole Go module still builds and tests clean

```bash
cd backend && go build ./... && go vet ./... && gofmt -l . && go test ./...
```

Expected: exit 0 from all four; `gofmt -l` prints nothing; `go test` prints `ok`
for `utils` (and `schema` if plan 001 has already landed) and
`no test files` for the rest. `no test files` is not a failure.

### Step 8: Add the CI workflow

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  frontend:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
        with:
          version: 9
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      # Non-blocking: `main` carries a pre-existing lint backlog (55 problems as
      # of ee420fb — mostly @typescript-eslint/no-explicit-any, plus some
      # react-hooks/exhaustive-deps). Lint still runs so violations stay visible
      # in the job log, but it does not gate merges until that backlog is
      # cleared. Remove `continue-on-error` once `pnpm run lint` exits 0.
      - name: Lint (non-blocking — see comment)
        run: pnpm run lint
        continue-on-error: true
      - run: pnpm run typecheck
      - run: pnpm run test
      - run: pnpm run build

  backend:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: backend
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
          cache-dependency-path: backend/go.sum
      - run: go build ./...
      - run: go vet ./...
      - name: Check formatting
        run: test -z "$(gofmt -l .)"
      - run: go test -race ./...
```

Two things to be careful about, both already handled above:

1. The backend job runs `go build ./...` **without** `-tags=prod`. With the
   `prod` tag, `backend/ui/ui_prod.go`'s `//go:embed dist` fails because
   `backend/ui/dist` is not in the repo — it is produced by the frontend build
   and copied in by the Dockerfile at release time.
2. Node 20 and Go 1.23 match the Dockerfile's `node:20-slim` and `golang:1.23`.

**Verify** locally (you cannot run GitHub Actions from here) by running each
workflow command by hand:

```bash
pnpm install --frozen-lockfile && pnpm run lint && pnpm run typecheck && pnpm run test && pnpm run build
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...
```

→ both lines exit 0.

Note `--frozen-lockfile` will fail if step 2 did not update `pnpm-lock.yaml`.
That is a real check — the lockfile must be committed.

### Step 9: Update the README development section

In `README.md`, in the `## Development` section after the existing `### Running`
subsection (around line 203), add:

```markdown
### Testing

Run the frontend test suite, typecheck, and lint:

```sh
$ pnpm run test
$ pnpm run typecheck
$ pnpm run lint
```

Run the backend test suite:

```sh
$ cd backend && go test ./...
```

All of the above run in CI on every pull request.
```

Do not change any other part of the README.

**Verify**: `grep -n "pnpm run test" README.md` → at least one match.

## Test plan

The tests *are* the deliverable of this plan. Summary of what must exist when
you are done:

| File | Tests | Notes |
|---|---|---|
| `src/lib/utils.test.ts` | 11 passing, 1 skipped | pure formatting functions |
| `src/lib/api.test.ts` | 4 passing | `fetch` stubbed via `vi.fn()` |
| `backend/utils/utils_test.go` | 5 passing, 1 skipped | `t.Setenv` for isolation |
| `backend/utils/cache_test.go` | 4 passing | one is race-tested |

There is no existing test in this repo to model on. Follow the framework
defaults: Vitest with `describe`/`it`/`expect` (globals are enabled in step 3,
so no imports are needed for those), and Go's stdlib `testing`.

**Verification**: `pnpm run test` → 15 passed, 1 skipped.
`cd backend && go test -race ./...` → `ok` for `utils`.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `pnpm install --frozen-lockfile` exits 0 (proves the lockfile is committed)
- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run lint` **runs**. It is expected to exit 1 — `main` carries a
      pre-existing backlog of 55 problems (confirmed at `ee420fb`). What must
      hold is that none of the reported files are files this plan created or
      modified: `npx pnpm@9 run lint 2>&1 | grep -E "test\.(ts|tsx)$"` returns
      nothing. That is why the CI step above is `continue-on-error`.
- [ ] `pnpm run test` exits 0 with 15 passed and 1 skipped
- [ ] `pnpm run build` exits 0
- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0
- [ ] `test -f .github/workflows/ci.yml` exits 0
- [ ] `git diff --name-only ee420fb..HEAD -- src/lib/utils.ts src/lib/api.ts backend/utils/utils.go backend/utils/cache.go` returns **nothing** — no production file was modified
- [ ] `plans/README.md` status row for 002 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- `pnpm run typecheck` (step 1) reports type errors on the untouched codebase.
  That is a pre-existing problem this plan does not own — report the errors
  verbatim and stop, rather than fixing source files.
- `go` or `pnpm` is not installed in your environment. Report this — do not
  install a toolchain, and do not skip verification gates.
- A test you write fails against current behavior and you are tempted to change
  the source to make it pass. **Do not.** Skip the test with a comment naming
  the behavior, and report it. Production code is out of scope for this plan.
- `pnpm add -D vitest@^2` resolves to a version incompatible with the installed
  Vite 5 (Vitest 2.x targets Vite 5; Vitest 3.x expects Vite 6). If the install
  or the first `vitest` run reports a peer-dependency conflict, report the exact
  message rather than upgrading Vite — a Vite major bump is a separate migration
  with its own blast radius.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **This is the foundation the other plans build on.** Plans 003, 004, and 006
  all say "depends on 002" because they modify delete paths, error paths, and
  URL construction that need regression coverage. If this plan is reverted,
  those become high-risk again.
- **CI runs `go build ./...`, not `make`.** If someone later adds embedded
  assets that only exist under the `prod` tag, the CI job will not catch a break
  in `ui_prod.go`. Building the release image is what covers that. Consider
  adding a `docker build` smoke job if `ui_prod.go` starts changing often — it
  has been stable.
- **Reviewer should scrutinize**: that no file under `src/` or `backend/`
  outside the test files was modified. The `git diff --name-only` check in Done
  criteria is the mechanical version of that; read the diff anyway.
- **Deliberately deferred**: component tests with Testing Library. The packages
  are installed and the jsdom environment is configured, so later plans can add
  them with no setup cost, but writing them for existing components is a large
  job with low marginal value compared to shipping the bug fixes. Plan 005 adds
  the first real component test as part of fixing a render crash.
- **Deliberately deferred**: end-to-end tests against a live Garage instance.
  Valuable, but it needs a container fixture and a seeded cluster; that is its
  own plan.
