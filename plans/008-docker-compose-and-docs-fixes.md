# Plan 008: Fix the Compose port mapping and the auth documentation

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- docker-compose.yml README.md Dockerfile`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: docs
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

Two documentation defects that cost real users real time, plus one small
robustness fix while you are in the file:

1. **`docker-compose.yml` maps host port 3902 to container port 3903.** Port
   3903 is Garage's **admin API** — the privileged endpoint that creates
   buckets, mints access keys, and rewrites the cluster layout. So anyone
   copying this compose file publishes the admin API on two host ports instead
   of one, and never publishes `s3_web` (3902) at all, which is what the mapping
   was obviously meant to expose. Bucket website access silently does not work.
   The README's equivalent block has the correct mapping, so the two files
   contradict each other and the wrong one is the copy-pasteable artifact.

2. **The README's `AUTH_USER_PASS` example cannot work as written.** Docker
   Compose performs variable interpolation on values in `environment:` blocks.
   A bcrypt hash always contains `$` characters (`$2y$10$...`), and Compose
   expands `$2y` and `$10` as variables — which are unset, so they become empty
   strings. The user pastes the documented value, gets a mangled hash, and every
   login fails with "invalid username or password" and no clue why. The fix is
   to escape each `$` as `$$`, and the README says nothing about it.

3. **The Dockerfile's healthcheck hardcodes port 3909.** The binary honors a
   `PORT` environment variable (`backend/main.go:41`), so a container started
   with `PORT=8080` reports permanently unhealthy and gets restarted forever by
   any orchestrator watching health status.

## Current state

### Files

- `docker-compose.yml` — the copy-pasteable deployment example (25 lines).
- `README.md` — installation and configuration docs.
- `Dockerfile` — release image build (26 lines).

### Excerpt 1 — the port typo

`docker-compose.yml`, the whole file:

```yaml
services:
  garage:
    image: dxflrs/garage:v2.0.0
    container_name: garage
    volumes:
      - ./garage.toml:/etc/garage.toml
      - ./meta:/var/lib/garage/meta
      - ./data:/var/lib/garage/data
    restart: unless-stopped
    ports:
      - 3900:3900
      - 3901:3901
      - 3902:3903
      - 3903:3903

  webui:
    image: khairul169/garage-webui:latest
    container_name: garage-webui
    restart: unless-stopped
    volumes:
      - ./garage.toml:/etc/garage.toml:ro
    ports:
      - 3909:3909
    environment:
      API_BASE_URL: "http://garage:3903"
      S3_ENDPOINT_URL: "http://garage:3900"
```

Line 11 is `3902:3903`. Every other line follows the `N:N` pattern.

### Excerpt 2 — the README's correct version of the same block

`README.md:31-58`, which proves the intent:

```yaml
services:
  garage:
    image: dxflrs/garage:v2.0.0
    container_name: garage
    volumes:
      - ./garage.toml:/etc/garage.toml
      - ./meta:/var/lib/garage/meta
      - ./data:/var/lib/garage/data
    restart: unless-stopped
    ports:
      - 3900:3900
      - 3901:3901
      - 3902:3902
      - 3903:3903
```

`3902:3902`. The two files disagree; the README is right.

For reference, the port meanings, from the README's own example config
(`README.md:110-137`):

| Port | Role | Config key |
|---|---|---|
| 3900 | S3 API | `[s3_api] api_bind_addr` |
| 3901 | RPC (inter-node) | `rpc_bind_addr` |
| 3902 | Web (bucket website hosting) | `[s3_web] bind_addr` |
| 3903 | **Admin API** | `[admin] api_bind_addr` |

### Excerpt 3 — the auth documentation

`README.md:152-171`:

```markdown
### Authentication

Enable authentication by setting the `AUTH_USER_PASS` environment variable in the format `username:password_hash`, where `password_hash` is a bcrypt hash of the password.

Generate the username and password hash using the following command:

```bash
htpasswd -nbBC 10 "YOUR_USERNAME" "YOUR_PASSWORD"
```

> If command 'htpasswd' is not found, install `apache2-utils` using your package manager.

Then update your `docker-compose.yml`:

```yml
webui:
  ....
  environment:
    AUTH_USER_PASS: "username:$2y$10$DSTi9o..."
```
```

The final snippet is the problem: pasted verbatim into a Compose file, `$2y` and
`$10` are interpolated away.

Note `backend/.env.example` has the same hash but that file is read by
`godotenv` (`backend/main.go:17`), **not** by Compose, so `$` is literal there
and it is correct as-is. Do not change it.

For reference, how the value is consumed — `backend/router/auth.go:25-34`:

```go
	userPass := strings.Split(utils.GetEnv("AUTH_USER_PASS", ""), ":")
	if len(userPass) < 2 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	if strings.TrimSpace(body.Username) != userPass[0] || bcrypt.CompareHashAndPassword([]byte(userPass[1]), []byte(body.Password)) != nil {
		utils.ResponseErrorStatus(w, errors.New("invalid username or password"), 401)
		return
	}
```

A mangled hash reaches `bcrypt.CompareHashAndPassword`, which returns an error
for a malformed hash — indistinguishable, from the user's side, from a wrong
password.

### Excerpt 4 — the healthcheck

`Dockerfile:18-26`:

```dockerfile
FROM scratch

COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=ghcr.io/tarampampam/curl:8.6.0 /bin/curl /bin/curl
COPY --from=backend /app/main /bin/main

HEALTHCHECK --interval=5m --timeout=2s --retries=3 --start-period=15s CMD [ \
    "curl", "--fail", "http://127.0.0.1:3909" \
]

ENTRYPOINT [ "main" ]
```

And the port it should follow, `backend/main.go:40-44`:

```go
	host := utils.GetEnv("HOST", "0.0.0.0")
	port := utils.GetEnv("PORT", "3909")

	addr := fmt.Sprintf("%s:%s", host, port)
	log.Printf("Starting server on http://%s", addr)
```

Note `COPY --from=alpine` refers to an *image* (`alpine:latest`), not a build
stage — there is no `FROM alpine AS alpine` in this file. That is valid Docker
syntax and it works; it is only an unpinned base. Mentioned so you do not
"fix" it.

### Repo conventions to match

- **README structure**: `##` for top-level sections, `###` for subsections,
  fenced code blocks tagged `sh`, `yml`, `toml`, or `bash`. Shell examples are
  prefixed with `$`.
- **Compose file style**: two-space indent, `N:N` port strings unquoted,
  `environment:` as a mapping (not a list).

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Validate Compose | `docker compose -f docker-compose.yml config` | exit 0, prints the resolved config |
| Frontend build | `pnpm run build` | exit 0 |
| Go build | `cd backend && go build ./...` | exit 0 |

`docker compose config` requires Docker. If it is unavailable, the
`grep`-based checks in Done criteria are the fallback — see STOP conditions.

## Scope

**In scope** (the only files you should modify):

- `docker-compose.yml`
- `README.md`
- `Dockerfile`

**Out of scope** (do NOT touch, even though they look related):

- `backend/.env.example` — read by `godotenv`, not Compose. `$` is literal
  there; the existing hash is correct. Changing it would break it.
- `backend/main.go` — the `PORT` handling is correct; the healthcheck is what
  needs to follow it.
- `misc/build-docker.sh` and `misc/build-binaries.sh` — release tooling,
  unrelated to these defects.
- `COPY --from=alpine` in the Dockerfile — valid, works, out of scope. Pinning
  it is a supply-chain improvement worth doing separately, not here.
- Any Go or TypeScript source. This plan changes no code behavior except the
  healthcheck command.

## Git workflow

- Branch: `advisor/008-docker-compose-and-docs-fixes`
- Conventional commits. Examples from `git log`: `docs: update readme`,
  `ccfa2cd docs: update readme & docker-compose.yml`.
- Suggested commits: `fix: correct s3_web port mapping in docker-compose`,
  `docs: explain $ escaping for AUTH_USER_PASS in compose`,
  `fix: make docker healthcheck follow PORT`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Fix the port mapping

In `docker-compose.yml`, change line 11 from `- 3902:3903` to `- 3902:3902`.

That is the entire change to that line. Do not renumber anything else.

**Verify**:

```bash
grep -n "3902" docker-compose.yml
```

→ exactly one match, reading `      - 3902:3902`.

```bash
diff <(sed -n '/^  garage:/,/^$/p' docker-compose.yml | grep -E '^\s+- [0-9]+:[0-9]+') <(sed -n '/^  garage:/,/^  webui:/p' README.md | grep -E '^\s+- [0-9]+:[0-9]+')
```

→ no output. The Compose file's garage ports now match the README's.

If Docker is available:

```bash
docker compose -f docker-compose.yml config
```

→ exit 0, and the `garage` service lists published ports 3900, 3901, 3902, 3903
each mapped to the identical container port.

### Step 2: Document the `$` escaping requirement

In `README.md`, replace the final snippet of the `### Authentication` section
(currently lines 164-171) with:

````markdown
Then update your `docker-compose.yml`. **Escape every `$` in the hash by
doubling it (`$$`)** — Docker Compose treats a single `$` as the start of a
variable reference and will silently strip parts of the hash, after which every
login attempt fails with "invalid username or password":

```yml
webui:
  ....
  environment:
    # htpasswd output: username:$2y$10$DSTi9o...
    # In compose, every $ becomes $$:
    AUTH_USER_PASS: "username:$$2y$$10$$DSTi9o..."
```

If you pass the variable through an `.env` file or `env_file:` instead, use the
hash **exactly as `htpasswd` printed it** — no doubling. The escaping rule
applies only to values written inline in a Compose `environment:` block.
````

The `.env` caveat is important and correct: `godotenv` (used at
`backend/main.go:17`) and Compose's `env_file:` both take the value literally.
Getting this backwards is the most likely way for a user to be confused twice.

**Verify**:

```bash
grep -n 'AUTH_USER_PASS: "username:\$\$2y' README.md
```

→ one match.

```bash
grep -c 'AUTH_USER_PASS' README.md
```

→ at least 3 (the prose mention, the comment, and the example).

### Step 3: Make the healthcheck follow `PORT`

In `Dockerfile`, replace the `HEALTHCHECK` block:

```dockerfile
HEALTHCHECK --interval=5m --timeout=2s --retries=3 --start-period=15s \
    CMD curl --fail "http://127.0.0.1:${PORT:-3909}${BASE_PATH}/" || exit 1
```

Two changes and one caveat:

- **Shell form instead of exec form.** `${PORT:-3909}` is shell substitution; it
  is not expanded in the JSON exec form the current file uses. The shell form is
  required for the variable to work.
- **`${BASE_PATH}` is included** because when `BASE_PATH` is set, the root path
  `/` returns a 301 redirect (`backend/main.go:36-38`), and `curl --fail`
  treats 3xx as success only without `--location` — it does not follow, but it
  also does not fail on 3xx. Including the base path checks the real UI instead.
- **Caveat**: the final image is `FROM scratch`, which has **no shell**. Shell
  form requires `/bin/sh`. Verify this before committing — see the verification
  below. If there is no shell, fall back to exec form with the port left
  hardcoded and instead document the limitation:

  ```dockerfile
  # NOTE: the scratch base image has no shell, so PORT cannot be interpolated
  # here. Containers started with a non-default PORT must override HEALTHCHECK.
  HEALTHCHECK --interval=5m --timeout=2s --retries=3 --start-period=15s CMD [ \
      "curl", "--fail", "http://127.0.0.1:3909" \
  ]
  ```

**Verify**, if Docker is available:

```bash
docker build -t garage-webui-healthcheck-test . && docker run --rm --entrypoint /bin/sh garage-webui-healthcheck-test -c 'echo shell-present'
```

- If it prints `shell-present`, the shell form works — keep it.
- If it errors with "no such file or directory", the image has no shell — revert
  to the exec form with the explanatory comment shown above.

Building the image requires network access for the base images. If Docker is
unavailable, **take the documented fallback** (exec form plus comment): it is
correct either way, whereas the shell form is correct only if a shell exists.
Note in your report which branch you took and why.

### Step 4: Cross-check the README's Docker CLI example

`README.md:24` documents the single-container run:

```sh
$ docker run -p 3909:3909 -v ./garage.toml:/etc/garage.toml:ro --restart unless-stopped --name garage-webui khairul169/garage-webui:latest
```

Confirm this is still accurate against the current `Dockerfile` (image name,
port, config mount path). The default `CONFIG_PATH` is `/etc/garage.toml`
(`backend/utils/garage.go:25`), so the mount target is correct, and the default
`PORT` is `3909` (`backend/main.go:41`), so the publish is correct.

**No change expected.** This step is a read-and-confirm. If something does not
match, report it rather than editing — a wrong image name or tag is a release
concern, not a docs typo.

### Step 5: Full verification

```bash
pnpm run build && cd backend && go build ./...
```

→ exit 0 from both. Neither should be affected, but confirm you did not
accidentally edit a source file.

```bash
git diff --name-only
```

→ exactly `docker-compose.yml`, `README.md`, `Dockerfile` (plus
`plans/README.md` once you update the status row).

## Test plan

There is nothing here to unit-test — these are configuration and documentation
artifacts. The verification is structural, and the Done criteria below are the
complete gate.

The one behavioral check worth doing, if Docker and a Garage instance are
available: bring up the compose stack, confirm `curl -sI localhost:3902`
reaches the Garage **web** endpoint (not the admin API), and confirm
`curl -sI localhost:3903` reaches the admin API. Before this fix both ports hit
the admin API. Report whether you were able to run this.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `grep -c "3902:3903" docker-compose.yml` returns `0`
- [ ] `grep -c "3902:3902" docker-compose.yml` returns `1`
- [ ] `grep -n 'AUTH_USER_PASS: "username:\$\$2y' README.md` returns a match
- [ ] `grep -n "env_file" README.md` returns a match (the caveat about not doubling `$` there)
- [ ] `grep -n "PORT" Dockerfile` returns a match — either the interpolated healthcheck or the explanatory comment from the fallback
- [ ] `docker compose -f docker-compose.yml config` exits 0 (skip only if Docker is unavailable; say so in your report)
- [ ] `pnpm run build` exits 0
- [ ] `cd backend && go build ./...` exits 0
- [ ] `git diff --name-only` lists only `docker-compose.yml`, `README.md`, `Dockerfile`, `plans/README.md`
- [ ] `plans/README.md` status row for 008 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- `docker-compose.yml` already reads `3902:3902` — someone fixed it. Verify the
  README block still matches, do the remaining steps, and note it.
- Docker is unavailable, so `docker compose config` and the image build cannot
  run. This is **not** a blocker: do steps 1, 2, 4, 5 normally, take the
  documented fallback in step 3, and report clearly which verifications you
  could not perform. Do not install Docker.
- The README's Docker CLI example (step 4) does not match the current image
  name, tag, or published port. Report the mismatch — release metadata is not
  yours to change unilaterally.
- Escaping the `$` in a real Compose file still produces a failing login. That
  would mean the mangling has a second cause (for example the value also passing
  through a shell) and the docs fix is incomplete. Report the exact value that
  reached the container, with the hash characters after the first eight replaced
  by `...` — do not paste a working credential into your report.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **`docker-compose.yml` and the README's compose block are duplicated by
  hand.** They already drifted once — that is this plan. If a third copy appears
  (a `docker-compose.prod.yml`, say), consider making the README include the
  file by reference rather than restating it. Note that `.gitignore` excludes
  `docker-compose.*.yml`, so variant files are deliberately untracked.
- **The `$$` escaping rule is Compose-specific and counterintuitive.** Expect it
  to be asked about again. The prose added in step 2 covers both directions
  (inline vs `env_file`) precisely because getting it backwards is the common
  second mistake.
- **Reviewer should scrutinize**: that the README's `$$` example is inside a
  fenced block that does not itself mangle the dollars when rendered on GitHub —
  view the rendered README, not just the diff.
- **Deliberately deferred**: pinning `COPY --from=alpine` to a digest. Real
  supply-chain hygiene, unrelated to these two user-facing defects, and it
  belongs with a broader look at the release pipeline (`misc/build-docker.sh`).
- **Deliberately deferred**: adding the `webui` service's `AUTH_USER_PASS` to
  `docker-compose.yml` itself. The file is a getting-started example and
  shipping a placeholder credential in it invites someone to keep the
  placeholder. The README's Authentication section is the right home for it.
