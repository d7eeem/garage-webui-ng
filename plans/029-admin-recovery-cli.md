# Plan 029: Offline admin-recovery CLI (`-reset-password`, `-create-admin`, `-list-users`)

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md` (the advisor maintains it). This is a **backend + docs** plan —
> do not touch any `.tsx`/`.ts` file.
>
> **Base reset FIRST**: `git checkout -B advisor/029-admin-recovery-cli main` then
> `git log --oneline -1` — MUST show `4f9d4db` or newer, NOT `ee420fb`.
> SENTINEL: `test -f backend/store/users.go && grep -q "runHealthCheck" backend/main.go && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P2 (operational recovery — closes a real self-hosting gap)
- **Effort**: S–M
- **Risk**: MED (writes credentials directly to the database; a mistake here is a
  privilege-escalation or lockout bug, not a cosmetic one)
- **Depends on**: nothing. Independent of plan 028 (frontend).
- **Category**: DX / operations
- **Planned at**: commit `4f9d4db`, 2026-08-04

## Why this matters

Right now, forgetting the administrator password means **destroying every
account**. `docs/authentication.md` §9 says so plainly:

> Losing every administrator credential is unrecoverable from inside the app …
> The only recovery path is to reset the database … **This deletes every account**

The only CLI flag that exists is `-health` (`backend/main.go:29`). Comparable
self-hosted tools all ship a recovery command — `gitea admin user
change-password`, `grafana-cli admin reset-admin-password`. This one doesn't, so
a forgotten password costs the operator every user, role and login record.

This plan adds a **local, offline** recovery CLI to the same binary.

### Security properties this must have (non-negotiable)

1. **Never an HTTP endpoint.** Password reset over the network with no
   authentication is a backdoor. These are process-local flags operating directly
   on the database file, usable only by someone who already has shell access to
   the host and read/write on `DB_PATH` — i.e. someone who could edit the DB by
   hand anyway.
2. **Never accept a password as a command-line argument.** Arguments leak into
   shell history, `ps`, and process accounting. Read it from the terminal with
   echo disabled.
3. **Never print or log a password or hash**, on success or failure.
4. **Reuse the app's own validation** (`store.ValidatePassword`) so the CLI and
   the UI can never disagree about what a valid password is.

## Current state (read before editing)

### `backend/main.go:23-34` — the flag pattern to follow

```go
func main() {
	godotenv.Load()

	// -health runs a lightweight self-probe used by the container HEALTHCHECK:
	// it issues a local HTTP request and exits 0 (healthy) or 1 (unhealthy), so
	// the runtime image needs no shell or curl.
	healthCheck := flag.Bool("health", false, "run a local health probe and exit (0 = healthy)")
	flag.Parse()
	if *healthCheck {
		os.Exit(runHealthCheck())
	}

	// Initialize app
	utils.InitCacheManager()
	…
```

`runHealthCheck() int` at the bottom of the same file is the exemplar: a
self-contained function that returns a process exit code, called before any
server setup. **Follow that shape** — parse flags, dispatch, `os.Exit`, and never
fall through into starting the HTTP server.

### `backend/store` — everything needed already exists

```go
func DBPath() string
func Open(path string) (*Store, error)          // runs migrations; safe on an existing DB
func (s *Store) Close() error
func (s *Store) ListUsers(ctx) ([]User, error)
func (s *Store) GetUserByUsername(ctx, username string) (*User, error)   // (nil, nil) when absent
func (s *Store) CreateUser(ctx, username, plaintextPassword, role string) (*User, error)
func (s *Store) SetPassword(ctx, id int64, plaintextPassword string) error
func (s *Store) CountEnabledAdmins(ctx) (int, error)
func ValidateUsername(s string) error
func ValidatePassword(s string) error
const RoleAdmin = "admin"; const RoleViewer = "viewer"
var ErrUsernameTaken, ErrWeakPassword, ErrInvalidUsername, ErrNoStore
```

`User.PasswordHash` carries `json:"-"`; `SetPassword`/`CreateUser` hash with
bcrypt cost 10 internally. **Do not hash anything in the CLI** — call the store.

### `backend/go.mod` — the TTY dependency is already in the graph

`golang.org/x/term v0.29.0` resolves today (transitively, via `golang.org/x/crypto`).
Promoting it to a direct requirement adds **no new dependency tree**.

### SQLite concurrency

`store.Open` uses WAL with `busy_timeout(5000)` and `SetMaxOpenConns(1)`. WAL
permits a second process to write while the server runs, so the CLI does **not**
require stopping the service. See *Maintenance notes* for the one caveat.

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Add direct dep | `cd backend && go get golang.org/x/term && go mod tidy` | `x/term` moves to the direct require block |
| Build/vet/fmt | `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)"` | exit 0 |
| CGO-free build | `cd backend && CGO_ENABLED=0 go build ./...` | exit 0 (**must stay true**) |
| Tests | `cd backend && go test -race ./...` | `ok` (~2 min; real bcrypt) |
| Vulnerability scan | `cd backend && "$(go env GOPATH)/bin/govulncheck" ./...` | `No vulnerabilities found.` |

## Scope

**In scope**:
- `backend/go.mod`, `backend/go.sum`
- `backend/cli.go` (create — the three commands + TTY prompt)
- `backend/cli_test.go` (create)
- `backend/main.go` (register the flags; dispatch before server startup)
- `docs/authentication.md` (§9 recovery — lead with the CLI)
- `docs/UPGRADING.md` (the "Locked out of every admin account" troubleshooting row)
- `README.md` (a short entry wherever operational commands belong)

**Out of scope** — do NOT touch:
- Any HTTP handler, route, or middleware. **There must be no new endpoint.**
- `backend/store/users.go` — the store API is sufficient; if you believe it is
  not, STOP and report rather than widening it.
- Any frontend file, `plans/`, the auth/session code.
- Interactive "menus", colour output, or a TUI. Three flags, plain text.

## Steps

### Step 1 — Promote `golang.org/x/term` to a direct dependency

```
cd backend && go get golang.org/x/term && go mod tidy
```
**Verify**: `grep -A6 "^require (" backend/go.mod | grep "golang.org/x/term"` matches;
`cd backend && CGO_ENABLED=0 go build ./...` exits 0.

### Step 2 — `backend/cli.go`

Package `main`. One exported-to-`main.go` entry point per command, each returning
a process exit code like `runHealthCheck` does.

**Password prompt** — the security-critical helper:

```go
// promptPassword reads a password from the terminal with echo disabled, then
// asks for it twice so a typo cannot silently become the new credential.
//
// A password is never taken from a command-line argument: arguments are visible
// in shell history, `ps` output and process accounting. When stdin is not a
// terminal (a pipe, for scripted recovery) a single line is read instead, and
// no confirmation is possible.
func promptPassword(prompt string) (string, error)
```
- TTY: `term.IsTerminal(int(os.Stdin.Fd()))` → `term.ReadPassword(int(os.Stdin.Fd()))`
  twice, compare, error `"passwords do not match"` on mismatch. Print a newline
  after each read (ReadPassword swallows the Enter).
- Non-TTY: `bufio.NewReader(os.Stdin).ReadString('\n')`, trimmed of the trailing
  newline **only** (a password may legitimately contain spaces).
- Never echo, never log, never include the value in an error.

**Command 1 — `runResetPassword(username string) int`**
1. Open the store at `store.DBPath()`; on failure print `cannot open user database at <path>: <err>` to stderr → exit 1.
2. `GetUserByUsername`; `nil` → stderr `no such user: <name>` → exit 1.
3. `promptPassword("New password: ")`.
4. `SetPassword(ctx, user.ID, pw)`; surface `ErrWeakPassword`'s message verbatim → exit 1.
5. Print `Password updated for "<username>" (role: <role>).` → exit 0.

**Command 2 — `runCreateAdmin(username string) int`**
1. Open the store. `ValidateUsername` first — reject early with the store's message.
2. `promptPassword("Password: ")`.
3. `CreateUser(ctx, username, pw, store.RoleAdmin)`; `ErrUsernameTaken` → stderr
   `user "<name>" already exists — use -reset-password instead` → exit 1.
4. Print `Created administrator "<username>".` → exit 0.

> Unlike `POST /setup`, this deliberately works **even when users already exist** —
> that is the entire point of a recovery tool. It is safe because it requires
> local write access to the database file.

**Command 3 — `runListUsers() int`**
Print an aligned table: `USERNAME  ROLE  STATUS  LAST LOGIN  CREATED`
(`text/tabwriter`). `STATUS` is `active`/`disabled`; a nil `LastLogin` prints `-`.
**Never print `PasswordHash`.** Exit 0 (exit 1 only on a store error).

### Step 3 — Register the flags in `main.go`

Immediately after the existing `-health` block, before any app initialisation:

```go
	resetPassword := flag.String("reset-password", "", "set a new password for `username` (prompts; local database access required)")
	createAdmin := flag.String("create-admin", "", "create a new administrator called `username` (prompts)")
	listUsers := flag.Bool("list-users", false, "list accounts in the user database and exit")
```
Dispatch after `flag.Parse()`:
```go
	switch {
	case *listUsers:
		os.Exit(runListUsers())
	case *resetPassword != "":
		os.Exit(runResetPassword(*resetPassword))
	case *createAdmin != "":
		os.Exit(runCreateAdmin(*createAdmin))
	}
```
Keep `-health` dispatching first (it is the container healthcheck and must stay
the cheapest path).

**Verify**: `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)"` exit 0.

### Step 4 — Tests (`backend/cli_test.go`)

The store calls are already covered by `backend/store/users_test.go`; test the CLI
layer's own logic against a **temp database** (`t.TempDir()` + `t.Setenv("DB_PATH", …)`):

- `runListUsers` on a seeded store exits 0, and its captured stdout contains the
  usernames and roles but **no `$2a$`/`$2b$` substring** (assert on the raw output).
- `runResetPassword("nope")` on a store without that user exits **1**.
- `runResetPassword(<existing>)` with a piped password on stdin exits 0, and the
  new password then verifies against the stored hash via
  `bcrypt.CompareHashAndPassword` while the old one does not.
- `runCreateAdmin(<new>)` with a piped password exits 0 and creates a user with
  role `admin`; running it again for the same name exits **1** and does not
  create a duplicate.
- A piped password shorter than the minimum exits **1** and leaves the stored
  hash unchanged.

To pipe stdin in a test, swap `os.Stdin` for a `*os.File` from `os.Pipe()` and
restore it with `t.Cleanup`. Capture stdout/stderr the same way.

**Verify**: `cd backend && go test -race -count=1 . -run 'CLI|ResetPassword|CreateAdmin|ListUsers' -v` → all pass.

### Step 5 — Documentation

**`docs/authentication.md` §9 "Lockout & recovery"** — currently states the only
path is deleting the database. That becomes false; rewrite it so the CLI is the
**primary** route and the destructive reset is the last resort:

```
1. Another admin works → Settings → Users → Reset password.
2. You have shell access → the recovery CLI (no data loss):

     # systemd / bare binary
     sudo -u <service-user> /usr/local/bin/garage-webui-ng -list-users
     sudo -u <service-user> /usr/local/bin/garage-webui-ng -reset-password admin

     # Docker (needs -it for the password prompt)
     docker run -it --rm -v <volume>:/data --entrypoint /main \
       ghcr.io/d7eeem/garage-webui-ng:latest -reset-password admin

3. Last resort → delete the database (destroys every account).
```
State plainly that the CLI requires local write access to `DB_PATH`, that it is
**not** reachable over HTTP, and that the password is prompted for, never passed
as an argument. Keep the existing destructive procedure as step 3.

**`docs/UPGRADING.md`** — the troubleshooting row "Locked out of every admin
account" currently points at the destructive reset; point it at the CLI first.

**`README.md`** — add the three commands wherever operational commands belong.

**Verify**: `grep -c "reset-password" docs/authentication.md docs/UPGRADING.md README.md` → each ≥ 1.

### Step 6 — Full gate sweep

```
cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && CGO_ENABLED=0 go build ./... && go test -race ./...
cd backend && "$(go env GOPATH)/bin/govulncheck" ./...
```
Commit on `advisor/029-admin-recovery-cli`: `feat: offline admin-recovery CLI`

## Test plan

- Unit tests (Step 4) are the contract, especially "no hash in `-list-users`
  output" and "wrong/short password leaves the stored hash untouched".
- **Reviewer live verification** (advisor's job), against a real binary and DB:
  1. `-list-users` prints the seeded accounts and **no** bcrypt prefix.
  2. `-reset-password <user>` with the service **running**: the new password logs
     in over HTTP; the old one returns 401.
  3. `-create-admin recovery` then log in as that account and confirm it reaches
     `/admin/users` (i.e. it really is an admin).
  4. `-reset-password nosuchuser` exits 1 with a clear message.
  5. The password never appears in shell history, `ps`, or the process output.
  6. `-health` still works (the container healthcheck must not regress).

## Done criteria

- [ ] `cd backend && go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test -race ./...` all exit 0
- [ ] `cd backend && CGO_ENABLED=0 go build ./...` exits 0
- [ ] `govulncheck ./...` → `No vulnerabilities found.`
- [ ] `grep -rn "reset-password" backend/router/ backend/middleware/` → **nothing** (no HTTP surface added)
- [ ] `grep -n "term.ReadPassword" backend/cli.go` → present (echo-disabled input)
- [ ] `grep -rn "flag.String(\"reset-password\"" backend/main.go` → present
- [ ] Docs updated in all three files
- [ ] `git diff --name-only 4f9d4db..HEAD` shows only in-scope files; **zero `.tsx`/`.ts`**

## STOP conditions

- You find yourself adding an HTTP route, handler, or middleware for password
  reset — that is a backdoor; STOP.
- You need to accept the password as a command-line argument to make something
  work — STOP; it must come from stdin/TTY.
- `store` needs a new exported function to complete this — report rather than
  widening the store API.
- `CGO_ENABLED=0 go build ./...` stops working after adding `x/term` — STOP.

## Maintenance notes

- **Local-only by construction.** The recovery commands are process flags, not
  endpoints. Anyone who can run them can already read and write the SQLite file
  directly, so the CLI grants no capability that filesystem access did not. Never
  "improve" this by exposing it over HTTP.
- **No password in `argv`, ever.** `term.ReadPassword` (TTY) or piped stdin only.
  A `-password` flag would leak into shell history, `ps` and audit logs.
- **Existing sessions are not revoked.** Sessions live in scs's in-memory store,
  so a password change via CLI (or UI) does not sign out sessions already
  established. That is pre-existing behaviour, not introduced here — but if
  session revocation is ever added, this command should call it.
- **Runs while the service is up.** WAL plus `busy_timeout(5000)` makes the
  concurrent write safe, and the server reads credentials per-login rather than
  caching them, so a reset takes effect on the next sign-in. Stopping the service
  is still the most conservative choice and costs nothing.
- **`store.Open` runs migrations.** Pointing the CLI at an older database will
  migrate it, which is correct but worth knowing before running it against a
  backup copy.
