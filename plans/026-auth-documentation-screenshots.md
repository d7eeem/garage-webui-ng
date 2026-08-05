# Plan 026: Authentication documentation, migration guide, and screenshots

> **Executor instructions**: Follow step by step. Run every verification command.
> Touch only in-scope files. On a STOP condition, stop and report. SKIP updating
> `plans/README.md`.
>
> **Base reset FIRST**: `git checkout -B advisor/026-auth-documentation main` then
> `git log --oneline -1`.
> SENTINEL (**022–025 must be merged**):
> `test -f backend/router/admin_users.go && test -f src/pages/settings/users-tab.tsx && echo BASE_OK`
> MUST print `BASE_OK`, else STOP.

## Status

- **Priority**: P2 (but **required** — the change is breaking and undocumented breakage is the worst kind)
- **Effort**: M
- **Risk**: LOW (docs only; no runtime code)
- **Depends on**: **022, 023, 024, 025**.
- **Category**: docs
- **Planned at**: commit `b6101af`, 2026-08-03

## Why this matters

Plans 022–025 changed the product's security model: authentication is now
**mandatory**, users live in a **SQLite database on a persistent volume**, and
`AUTH_USER_PASS` is a **one-time import** rather than the live source of truth.
Every existing document still describes the old world — including CLAUDE.md's
headline claim that *"the backend holds no state of its own"*, which is now false.

This plan makes the documentation true again and gives existing operators a
migration path they can follow without surprises.

## Current state — every place that still describes the old model

| File | What is now wrong |
|---|---|
| `README.md` L37 | Feature bullet: "optional session auth with bcrypt-hashed credentials" — auth is no longer optional. |
| `README.md` L154 | Env table row: `AUTH_USER_PASS` … "Enables auth." — it is now import-only. |
| `README.md` L158 | Note: "if neither … is set, the UI **and** the admin-token-injecting proxy are open" — open mode is gone. |
| `README.md` L188-196 | Security section: "Optional authentication", and the "open (no-auth) mode proxies the Garage admin token" warning. |
| `README.md` L217-219 | FAQ: "How do I generate a bcrypt hash for `AUTH_USER_PASS`?" + the `htpasswd` pipeline — no longer the primary flow. |
| `README.md` (Screenshots) | No setup wizard / Settings screenshots. |
| `CLAUDE.md` L7 | "The backend holds no state of its own — it is a gateway…" — **false**, it now owns a user DB. |
| `CLAUDE.md` L44 | "**Auth is optional and session-based.** Set `AUTH_USER_PASS=…` to enable it … When unset, the entire API … is open". |
| `CLAUDE.md` (Architecture/Commands) | No mention of `backend/store/`, migrations, `DB_PATH`, or the `modernc.org/sqlite` CGO constraint. |
| `.env.example` | Partially updated by plan 022 — verify and finish. |
| `docs/` | Only `screenshots/`. No authentication or upgrade guide exists. |

## Commands

| Purpose | Command | Expected |
|---|---|---|
| Frontend build (screenshots) | `npx pnpm@9 run build` | exit 0 |
| Docs link sanity | `grep -rn "docs/screenshots/" README.md` | every referenced file exists |

## Scope

**In scope**:
- `README.md`
- `CLAUDE.md`
- `docs/authentication.md` (create)
- `docs/UPGRADING.md` (create)
- `.env.example` (finish/verify)
- `backend/.env.example` — **carry-over from plan 022**: this second, backend-local
  example file still advertises the old env-var auth model and now contradicts
  reality. Bring it in line with the root `.env.example` (auth vars are legacy /
  import-only; add `DB_PATH`).
- `CLAUDE.md` — also correct the stale **"Go 1.23+"** floor: plan 022 raised the
  module's `go` directive to **1.25.0** (`modernc.org/sqlite` requires it) and the
  Docker builder to `golang:1.25-alpine`.
- `docs/screenshots/*.png` (add 3 new; keep existing)
- `plans/021-password-hash-generator.md` — **status note only** (mark superseded;
  do not delete the file)

**Out of scope**: any Go or TypeScript source change, `docker-compose.yml` /
`Dockerfile` (already done in 022), re-capturing unrelated screenshots.

## Steps

### Step 1 — `docs/authentication.md` (the new canonical reference)

Create it with these sections — this is the deliverable that carries the
architecture/schema/API detail out of the README:

1. **Overview** — users are application data stored in SQLite; authentication is
   mandatory; no environment editing after the first start.
2. **First run** — the `/setup` wizard: what it asks, that it auto-logs-in, and
   that it closes permanently once a user exists.
3. **Database & persistence** — `DB_PATH` (default `/data/garage-webui-ng.db` in
   the image, `./data/garage-webui-ng.db` for local runs); **a persistent volume
   is required**; the container runs as uid 65532 and the image ships `/data`
   owned by it.
4. **Schema** — reproduce the `users` table DDL and the `schema_migrations`
   versioning approach (append-only migrations).
5. **Roles** — `admin` vs `viewer`; exactly what a viewer may do (all reads except
   `GetKeyInfo?showSecretKey=true`; the only writes are logout and
   own-password-change); admin-only `/admin/*`.
6. **API reference** — a table of every auth/admin endpoint:
   `GET /api/setup/status`, `POST /api/setup`, `POST /api/auth/login`,
   `POST /api/auth/logout`, `GET /api/auth/status`,
   `POST /api/auth/change-password`, `GET /api/admin/users`,
   `POST /api/admin/users`, `PATCH /api/admin/users/{id}`,
   `DELETE /api/admin/users/{id}`, `POST /api/admin/users/{id}/reset-password`.
   For each: method, path, auth requirement, request fields, success shape, and
   the notable error codes (400/401/403/404/409/429). State plainly that **no
   endpoint ever returns a password or a password hash**.
7. **Sessions & CSRF** — HttpOnly + `SameSite=Lax` cookies, `SESSION_COOKIE_SECURE`,
   `SESSION_LIFETIME_HOURS` / `SESSION_IDLE_TIMEOUT_HOURS`, the `X-CSRF-Token`
   double-submit token, and login rate limiting (10/min/IP).
8. **Lockout & recovery** — the last-admin guards; and the honest recovery path if
   all admin credentials are lost (stop the container, remove the database file
   from the volume, restart → the `/setup` wizard reopens; **this deletes all
   users**).
9. **Future extension points** — RBAC (roles/permissions tables), MFA (TOTP
   secret per user), OAuth/OIDC and LDAP (external identity → local user
   mapping). Label clearly as *not implemented*.

### Step 2 — `docs/UPGRADING.md` (migration guide)

Aimed at someone upgrading an existing deployment. Must contain:

- **Breaking changes**, stated up front:
  1. Authentication is mandatory; open (no-auth) deployments must now complete the
     `/setup` wizard.
  2. A **persistent volume** is required (`/data`); without one, users are lost
     whenever the container is recreated.
  3. `AUTH_USER_PASS` / `AUTH_VIEWER_USER_PASS` are now **import-only**.
- **Path A — you already use `AUTH_USER_PASS`**: add the volume, start the new
  image, look for the log line
  `Initial administrator imported from AUTH_USER_PASS (N user(s)).`, log in with
  the same credentials, then **remove the env vars** from your compose file
  (they are ignored from now on). Warn explicitly: if you *keep* the env vars and
  have **no** volume, every container recreation silently re-imports and discards
  users you created in the UI — which masks the missing volume.
- **Path B — you ran with no authentication**: after upgrading, the UI redirects
  to `/setup`; create the administrator. There is no opt-out.
- **Verifying the migration**: check the startup log for the DB path, confirm
  `GET /api/auth/status` returns `needsSetup: false`, and confirm the user list in
  Settings → Users.
- **Rollback**: pin the previous image tag; the old release still reads
  `AUTH_USER_PASS` (keep it set until you are confident).
- A ready-to-paste compose snippet showing the `webui_data:/data` volume.

### Step 3 — `README.md`

- **Features bullet** (L37): replace "optional session auth…" with in-app user
  management: first-run setup wizard, admin/viewer roles, self-service password
  change, CSRF-protected sessions.
- **Configuration table**: `AUTH_USER_PASS` / `AUTH_VIEWER_USER_PASS` → mark
  **legacy, imported once on first start, then ignored**; add `DB_PATH`,
  `SESSION_LIFETIME_HOURS`, `SESSION_IDLE_TIMEOUT_HOURS`. Delete the "if neither …
  is set … open" note (L158) and replace it with a pointer to
  [`docs/authentication.md`](docs/authentication.md).
- **Installation / Docker / Compose**: every `docker run` and compose example must
  now mount a persistent volume (`-v garage_webui_data:/data`). Add a line after
  the run command: "On first start, open the UI and complete the setup wizard."
- **Security section**: replace "Optional authentication" and the open-mode
  warning with: mandatory auth, bcrypt (cost 10), HttpOnly/SameSite cookies,
  CSRF token, rate-limited login, idle+absolute session expiry, admin/viewer
  roles, audit log, and "no endpoint returns password hashes".
- **FAQ**: replace the `htpasswd` answer with: "You don't need one — create users
  in **Settings → Users**, or complete the first-run wizard." Keep a short
  *Alternative (legacy import)* note that `AUTH_USER_PASS` still seeds the first
  admin on a brand-new instance.
- **Roadmap**: tick the new capability; add RBAC / MFA / OIDC / LDAP as future items.
- **Screenshots gallery**: add the three new images from Step 5.
- Grep afterwards: **no** sentence may imply an operator must edit
  `AUTH_USER_PASS`, `.env`, compose, or restart to manage users.

### Step 4 — `CLAUDE.md`

- **"What this is"**: correct the stateless claim. New wording must say the
  backend is a gateway to Garage **plus** a small local SQLite store for its own
  users/authentication — nothing else is persisted.
- **Architecture**: add a `backend/store/` paragraph — pure-Go
  `modernc.org/sqlite` (**required**: the release build is `CGO_ENABLED=0` on
  distroless; `mattn/go-sqlite3` would break it), append-only migrations,
  `store.Default()` singleton alongside `utils.Session` / `utils.Garage`,
  `DB_PATH`, and `/data` ownership (uid 65532).
- **Auth section (L44)**: rewrite — mandatory session auth backed by the user
  table, `/setup` bootstrap, `AUTH_USER_PASS` import-once, roles, CSRF middleware,
  and the two-layer `/admin/*` authorization.
- **Conventions**: note that `User.PasswordHash` is `json:"-"` and that store
  tests assert hashes never appear in marshalled output.

### Step 5 — Screenshots

Rebuild the frontend, run the app against a live Garage, and capture **three**
new 2×-DPR PNGs into `docs/screenshots/` matching the existing set's style
(1440×900 viewport, light theme, realistic data):

- `setup-wizard.png` — the `/setup` page on a fresh instance (empty DB).
- `settings-users.png` — Settings → Users with 2–3 users (e.g. an `admin` and a
  `viewer`), showing roles/status/last-login.
- `settings-account.png` — Settings → Account (change-password form).

Use throwaway credentials; **no real secrets in any screenshot**, and never show a
password field's contents. Reference all three from the README gallery.

### Step 6 — Mark plan 021 superseded

`plans/021-password-hash-generator.md` planned an in-app bcrypt generator whose
only purpose was producing `AUTH_USER_PASS` values. With users managed in the UI,
it is no longer needed. Add a short note directly under its `## Status` heading:

```md
> **SUPERSEDED by plans 022–026.** Users are now created in the app (first-run
> wizard + Settings → Users), so no one needs to generate a bcrypt hash by hand.
> Kept for history; do not execute.
```
Change nothing else in that file.

### Step 7 — Consistency sweep

```
grep -rniE "AUTH_USER_PASS" README.md CLAUDE.md docs/ .env.example
```
Every remaining hit must be in a **legacy/import/migration** context. Then:
```
grep -rn "htpasswd" README.md docs/          # only inside a legacy/alternative note
grep -rn "no state of its own" CLAUDE.md     # must return nothing
grep -rn "docs/screenshots/" README.md       # every referenced PNG must exist
```
**Verify**: run each; confirm the expected results above.

### Step 8 — Commit

`npx pnpm@9 run build` still exits 0 (no source changed, but confirm).
Commit on `advisor/026-auth-documentation`:
`docs: rewrite authentication docs, add migration guide and screenshots`

## Test plan

Documentation has no unit tests; the greps in Step 7 are the machine-checkable
part. **Reviewer verification**: read `docs/UPGRADING.md` as if operating an
existing deployment and confirm both paths are followable end-to-end against the
real build; confirm every screenshot shows the current UI; confirm no document
tells an operator to edit an env var to manage users.

## Done criteria

- [ ] `docs/authentication.md` and `docs/UPGRADING.md` exist and cover all sections above
- [ ] `grep -rn "no state of its own" CLAUDE.md` → nothing
- [ ] `grep -rn "the entire API — including the admin-token-injecting proxy — is open" CLAUDE.md README.md` → nothing
- [ ] Every `AUTH_USER_PASS` mention in README/CLAUDE.md/docs is framed as legacy/import-only
- [ ] `docs/screenshots/setup-wizard.png`, `settings-users.png`, `settings-account.png` exist and are referenced by README
- [ ] `plans/021-password-hash-generator.md` carries the SUPERSEDED note
- [ ] `npx pnpm@9 run build` exits 0
- [ ] `git diff --name-only <025-merge-sha>..HEAD` shows only in-scope files (no `.go`, no `.tsx`)

## STOP conditions

- SENTINEL fails (022–025 not all merged).
- A documented behaviour does not match the built app (e.g. an endpoint's error
  code differs) — **report the mismatch instead of documenting the aspiration**.
- You cannot capture a screenshot because the feature is missing — STOP; that
  means an earlier plan is incomplete.

## Maintenance notes

- `docs/authentication.md` is the canonical auth reference; the README should link
  to it rather than duplicating the API table, so the two cannot drift.
- The API table must be updated whenever an `/admin/*` or `/auth/*` endpoint
  changes — treat it as part of the endpoint's definition of done.
- `docs/UPGRADING.md` is versioned advice: when the next breaking change lands,
  append a new section rather than rewriting history.
- The "keeps env vars + no volume ⇒ silent re-import" trap is the subtlest failure
  mode of this whole migration. Keep that warning prominent.
