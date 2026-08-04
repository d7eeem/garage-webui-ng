# Authentication

The canonical reference for how Garage WebUI-NG authenticates and authorises
people. The README links here rather than duplicating any of it.

Upgrading an existing deployment? Read [`UPGRADING.md`](UPGRADING.md) first.

---

## 1. Overview

- **Users are application data, not configuration.** They live in a small
  SQLite database owned by the app, and are managed entirely from the UI.
- **Authentication is mandatory.** There is no open (no-auth) mode. Every API
  route requires a session except the three listed under
  [Public endpoints](#61-public-endpoints).
- **Nothing needs to be edited after the first start.** Creating, renaming,
  re-roling, disabling or deleting an account never involves an environment
  variable, a config file or a restart.
- Passwords are hashed with **bcrypt (cost 10)**. **No endpoint ever returns a
  password or a password hash** — see [§6.5](#65-no-endpoint-returns-a-password).

The database is the only state this service owns. Everything else it shows you
is read live from your Garage cluster.

---

## 2. First run — the setup wizard

A deployment with **zero** users is *un-set-up*. In that state:

- `GET /api/auth/status` and `GET /api/setup/status` both report
  `needsSetup: true`;
- the SPA redirects every route — including `/auth/login` — to **`/setup`**;
- the server logs, at startup:
  `No users configured — open http://<host>:<port>/setup to create the first administrator.`

The wizard asks for exactly three things: **username**, **password**, and
**confirm password**. It creates one account, always with the **`admin`** role.

Finishing the wizard **signs you straight in** (the server issues a fresh
session token and stores the identity), so you land in the app rather than on a
login form.

The wizard then closes **permanently**: `POST /api/setup` refuses with **409
Conflict** (`setup has already been completed`) as soon as any user exists, and
the check happens inside the same database transaction as the insert, so two
concurrent requests cannot both succeed. Deleting every user is not a supported
way to reopen it — see [Lockout & recovery](#9-lockout--recovery).

### Validation rules

| Field | Rule |
|---|---|
| Username | 1–64 characters, `A–Z a–z 0–9 . _ @ -` only. Case-**insensitive** uniqueness: `Admin` and `admin` are the same account. |
| Password | At least **10** characters, at most **72 bytes** (bcrypt ignores anything beyond that, so a longer password is rejected rather than silently truncated), and not entirely whitespace. |

The browser validates the same rules for a faster round trip; the server
validates independently and is authoritative.

---

## 3. Database & persistence

| | |
|---|---|
| Env var | `DB_PATH` |
| Default in the Docker image | `/data/garage-webui-ng.db` |
| Default elsewhere (binary, `go run`) | `./data/garage-webui-ng.db`, relative to the working directory |
| Driver | `modernc.org/sqlite` — pure Go, no cgo |
| Journal mode | WAL (so `-wal` and `-shm` sidecar files sit next to the database) |

**A persistent volume is required.** Losing the file loses every account and
drops the instance back to the setup wizard. The container declares
`VOLUME ["/data"]` and the Compose stack mounts a named volume `webui_data`
there for exactly this reason.

The runtime image is distroless `nonroot`: the process runs as **uid/gid
65532**, and the image ships a `/data` directory already owned by that uid.
Docker seeds a fresh named volume from the image directory, so the volume
inherits the ownership and the non-root process can create its database. If you
bind-mount a host directory instead, **chown it to `65532:65532` yourself** —
otherwise startup fails with `cannot open user database`.

Startup is fail-fast: if the database cannot be opened or migrated, the process
exits rather than serving a UI that could only return errors. On a successful
start it logs the path it used:

```
User database: /data/garage-webui-ng.db
```

---

## 4. Schema

One table plus a version counter. Migrations are **append-only**: each entry is
a schema version, its index+1 is the number recorded in `schema_migrations`, and
a shipped entry is never edited, reordered or removed — the counter is the only
record of what a live database already has, so an edited entry would silently
never be applied to existing installs. Each migration runs in its own
transaction together with its version bump, so a failure cannot leave a
half-applied version behind.

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);

-- migration 1
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL DEFAULT 'admin',
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login    TIMESTAMP
);
```

Notes:

- `COLLATE NOCASE` on `username` makes the UNIQUE index case-insensitive, which
  is what operators expect from a login name and blocks look-alike duplicates.
- `updated_at` tracks **administrative** changes to the account. A successful
  sign-in stamps `last_login` only, deliberately leaving `updated_at` alone.
- `disabled` accounts keep their row (and their history); they simply cannot
  sign in.
- The connection pool is capped at **one** connection. SQLite permits a single
  writer, this database holds a handful of rows, and funnelling every statement
  through one connection removes “database is locked” as a failure mode.

---

## 5. Roles

Two roles: **`admin`** and **`viewer`**.

### `admin`

Full access: everything a viewer can do, plus every write, plus user
administration under `/api/admin/*`.

### `viewer` — read-only, fail-closed

The role is defined by an allowlist, not a denylist:

- **Everything under `/api/admin/` is denied outright**, whatever the method.
  Reads included: the roster of accounts, their roles and their sign-in times
  are not viewer-visible information.
- **Every other `GET` is allowed**, with one carve-out: `GET /api/v2/GetKeyInfo`
  with `showSecretKey=true` is denied, so a viewer can never reveal a secret
  access key.
- **The only writes permitted are `POST /api/auth/logout` and
  `POST /api/auth/change-password`** — both act solely on the caller's own
  session and own account.
- Every other method (`POST`, `PUT`, `PATCH`, `DELETE`, …) is denied with
  **403** `forbidden: read-only session`.

A denied request is still recorded by the audit log, with its `403` status.

The administration API is guarded **twice, independently**: the middleware
refuses `/admin/` for a viewer, and every `/admin/*` handler additionally calls
`requireAdmin` before doing anything. A routing mistake alone cannot expose
those endpoints. Keep both.

---

## 6. API reference

All paths are relative to the server root and are served under `/api`
(prefixed by `BASE_PATH` when that is set). Successful responses are JSON;
**error responses are a plain-text message body** with the status code below.

### 6.1 Public endpoints

Exactly three routes are reachable without a session. The list is
exact-match — never a prefix — and is a security boundary.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/auth/status` | Session state + whether setup is still needed. |
| `GET` | `/api/setup/status` | Whether setup is still needed. |
| `POST` | `/api/setup` | Create the first administrator (only while there are none). |

`POST /api/auth/login` is registered ahead of the middleware chain and so is
also reachable without a session, by construction.

### 6.2 Setup

| | |
|---|---|
| **`GET /api/setup/status`** | |
| Auth | none |
| Request | — |
| Success | `200` · `{"needsSetup": true}` |
| Errors | `500` — the user count could not be read. While startup is still in progress the answer is `{"needsSetup": true}`; the wizard's own guard, not this answer, decides whether an account can actually be created. |

| | |
|---|---|
| **`POST /api/setup`** | |
| Auth | none — but only succeeds while the users table is empty |
| CSRF | exempt |
| Request | `{"username": "...", "password": "...", "confirmPassword": "..."}` |
| Success | `200` · `{"authenticated": true, "username": "...", "role": "admin"}` — and the caller is signed in |
| `400` | `invalid request body`, `passwords do not match`, or a validation message (`password is not acceptable: …`, `invalid username: …`, `"x" is already taken`) |
| `409` | `setup has already been completed` — the instance already has a user; the wizard is closed for good |
| `429` | `too many setup attempts, try again later` |
| `500` | store unavailable, or the account could not be created |

### 6.3 Session

| | |
|---|---|
| **`POST /api/auth/login`** | |
| Auth | none |
| CSRF | exempt |
| Request | `{"username": "...", "password": "..."}` |
| Success | `200` · `{"authenticated": true, "username": "...", "role": "admin"\|"viewer"}` |
| `401` | `invalid username or password` — returned identically for an unknown user, a disabled account and a wrong password, and a disabled/unknown account still pays for a bcrypt comparison so the timing matches |
| `429` | `too many login attempts, try again later` |
| `500` | invalid JSON body, store unavailable, or the session could not be started |

The response reports the **stored** spelling of the username, not what was
typed — logins are case-insensitive and the session and audit log should show
one canonical form. Login renews the session token before recording the
identity, so a session ID planted before authentication cannot become an
authenticated one.

| | |
|---|---|
| **`POST /api/auth/logout`** | |
| Auth | any authenticated session (including `viewer`) |
| CSRF | **required** |
| Request | — |
| Success | `200` · `true` |
| Errors | `401` unauthenticated · `403` missing/invalid CSRF token |

| | |
|---|---|
| **`GET /api/auth/status`** | |
| Auth | none |
| Success | `200` · `{"enabled": true, "authenticated": bool, "username": "...", "role": "...", "needsSetup": bool}` |
| Errors | `500` — the user count could not be read |

`enabled` is a constant `true` (authentication is mandatory); the field is kept
because the frontend still reads it. `username` and `role` are empty strings on
an unauthenticated session. This endpoint reveals nothing beyond the caller's
own session state and whether a first account is still needed.

| | |
|---|---|
| **`POST /api/auth/change-password`** | |
| Auth | any authenticated session (including `viewer`) |
| CSRF | **required** |
| Request | `{"currentPassword": "...", "newPassword": "...", "confirmPassword": "..."}` |
| Success | `200` · `true` |
| `400` | `invalid request body`, `passwords do not match`, `the new password must differ from the current one`, or `password is not acceptable: …` |
| `401` | no session, the session names an account that no longer exists or has been disabled, or `current password is incorrect` |
| `403` | missing/invalid CSRF token |
| `429` | `too many attempts, try again later` |
| `500` | store unavailable, or the password could not be stored |

The body carries **no user id**: the endpoint always acts on whoever the session
names, so it can never target another account. Verifying the current password
is a password guess like any other, so it shares the login rate limiter — the
check runs *before* the comparison, otherwise a stolen session would be an
unthrottled oracle for the password it could not otherwise read. A successful
change renews the session token while keeping the caller signed in.

### 6.4 User administration

Every route below requires an **`admin`** session and a valid CSRF token on
writes. A viewer receives `403 forbidden: read-only session` from the
middleware; anything else that reaches a handler without the admin role gets
`403 forbidden: administrator role required`.

A **user object** is:

```json
{
  "id": 1,
  "username": "alice",
  "role": "admin",
  "disabled": false,
  "createdAt": "2026-08-04T09:15:00Z",
  "updatedAt": "2026-08-04T09:15:00Z",
  "lastLogin": "2026-08-04T10:02:11Z"
}
```

`lastLogin` is `null` for an account that has never signed in. There is no
password field of any kind.

| | |
|---|---|
| **`GET /api/admin/users`** | |
| Request | — |
| Success | `200` · array of user objects, ordered by username |
| Errors | `401` · `403` · `500` |

| | |
|---|---|
| **`POST /api/admin/users`** | |
| CSRF | required |
| Request | `{"username": "...", "password": "...", "role": "admin"\|"viewer"}` |
| Success | `200` · the created user object |
| `400` | `invalid request body`, `invalid role: …`, `invalid username: …`, `password is not acceptable: …` |
| `409` | `"x" is already taken` |
| Errors | `401` · `403` · `500` |

| | |
|---|---|
| **`PATCH /api/admin/users/{id}`** | |
| CSRF | required |
| Request | any subset of `{"username": "...", "role": "...", "disabled": bool}`. Fields are optional pointers, so *absent* differs from *empty*: `{}` is a no-op, `{"username": ""}` is a rejected rename. |
| Success | `200` · the updated user object |
| `400` | `invalid user id`, `invalid request body`, `invalid role: …`, `invalid username: …` |
| `404` | `user not found` |
| `409` | `"x" is already taken`, or a lockout guard (see [§7](#7-lockout-guards)) |
| Errors | `401` · `403` · `500` |

| | |
|---|---|
| **`DELETE /api/admin/users/{id}`** | |
| CSRF | required |
| Request | — |
| Success | `200` · `true` |
| `400` | `invalid user id` |
| `404` | `user not found` |
| `409` | a lockout guard (see [§7](#7-lockout-guards)) |
| Errors | `401` · `403` · `500` |

| | |
|---|---|
| **`POST /api/admin/users/{id}/reset-password`** | |
| CSRF | required |
| Request | `{"newPassword": "..."}` |
| Success | `200` · `true` |
| `400` | `invalid user id`, `invalid request body`, `password is not acceptable: …` |
| `404` | `user not found` |
| Errors | `401` · `403` · `500` |

An administrator resetting someone else's password does **not** need the old
one — that is the escape hatch for a locked-out colleague. It deliberately does
not invalidate the target's existing sessions; disable the account if those must
die immediately.

Renames and password resets never remove administrative access, so the lockout
guards never block them.

### 6.5 No endpoint returns a password

`store.User.PasswordHash` carries `json:"-"`, which is the single guarantee that
a hash cannot leak through any response, and the tests assert on raw response
bodies that no bcrypt prefix ever appears. No handler logs a submitted password
either — JSON decoder errors are replaced with a flat `invalid request body`
precisely because the decoder can quote the request body, which is where the
password is.

---

## 7. Lockout guards

Three rules stop the instance from becoming unadministrable. Each returns
**409 Conflict** with the message shown, and the write does not happen. A
failure to *evaluate* a rule fails closed as a `500` — an unreadable admin count
must never be read as “there are plenty of admins”.

| Rule | Message |
|---|---|
| An admin may not delete their own account. | `you cannot delete your own account` |
| An admin may not demote or disable their own account — even when other admins exist, because a one-click accidental lockout is too easy. | `you cannot demote or disable your own account` |
| Nobody may take administration away from the last **enabled** admin (delete, disable, or demote to viewer). | `cannot remove the last administrator: this instance would become unadministrable` |

Identity comes from the session, never from the request body, and is compared
case-insensitively to match the `COLLATE NOCASE` username index.

The Users tab greys out actions these rules would refuse, but that is a
courtesy, not a control — the server answers 409 regardless and the UI shows the
message verbatim.

---

## 8. Sessions & CSRF

### Session cookie

Sessions are server-side ([`alexedwards/scs`](https://github.com/alexedwards/scs));
the cookie carries only an opaque token.

| Property | Value |
|---|---|
| `HttpOnly` | `true` — set explicitly, so a library default change cannot weaken it |
| `SameSite` | `Lax` — likewise explicit |
| `Secure` | `SESSION_COOKIE_SECURE=true` (default `false`) |
| `Path` | `/`, or `BASE_PATH` when the UI is mounted under a prefix |

`Secure` is opt-in because browsers reject `Secure` cookies over plain HTTP and
this UI is commonly served over HTTP on a private network. **Set
`SESSION_COOKIE_SECURE=true` whenever you terminate TLS in front of it.**

| Env var | Default | Meaning |
|---|---|---|
| `SESSION_LIFETIME_HOURS` | `24` | Absolute cap on a session's age, counted from creation. Reached even by a session in constant use. |
| `SESSION_IDLE_TIMEOUT_HOURS` | `2` | Signs out a session that has not been touched for this long. |

Both take a positive whole number of hours. A value that is unparseable or
`<= 0` falls back to the default and logs a warning rather than silently running
with a different expiry.

Session data is held **in memory**, so restarting the process signs everyone
out. Accounts are unaffected — those are in the database.

The token is renewed on login, on completing setup, and after a password change,
so a session ID planted before a privilege change never becomes a privileged one.

### CSRF double-submit token

| | |
|---|---|
| Cookie | `csrf_token` — 32 random bytes, hex-encoded. Deliberately **not** `HttpOnly`: the frontend has to read it. Mirrors the session cookie's `Path`, `SameSite=Lax` and `Secure`. |
| Header | `X-CSRF-Token` — must equal the cookie on every state-changing request |

Any request without the cookie is issued one, so a fresh client picks up its
token on the first `GET`. `GET`/`HEAD`/`OPTIONS` are never rejected.
`POST`/`PUT`/`PATCH`/`DELETE` are rejected with **403** `invalid or missing CSRF
token` unless the pair matches (compared in constant time).

**Only two endpoints are exempt**, as exact method+path matches:
`POST /api/auth/login` and `POST /api/setup` — the only writes a caller can make
before it holds any session or token at all. Both are rate-limited and both are
guarded by a credential or by the "no users exist yet" check. Every other write,
including logout and password change, requires the token.

Honest framing: the session cookie is already `SameSite=Lax` and the API
consumes `application/json`, so a cross-site form post cannot reach a write
endpoint with credentials attached anyway. The token is defence in depth on top
of those two — do not relax `SameSite` or add permissive CORS to accommodate it.

### Login rate limiting

**10 attempts per minute per client IP**, a fixed window. Exceeding it returns
**429**. The bucket is shared by `POST /api/auth/login`, `POST /api/setup`, and
the current-password check inside `POST /api/auth/change-password`.

The key is the peer address (`RemoteAddr`). `X-Forwarded-For` is deliberately
**not** trusted: without knowing the proxy topology, honouring it would let a
client choose its own rate-limit bucket. Behind a reverse proxy every attempt
therefore shares one bucket — size your fronting proxy's own rate limiting
accordingly.

---

## 9. Lockout & recovery

Losing every administrator credential cannot be fixed *from inside the app*: the
setup wizard only reopens when there are **zero** users, and the lockout guards
exist precisely to stop you reaching a state with no working admin.

Work down this list — only step 3 destroys anything.

### Step 1 — another admin still works

Use **Settings → Users → Reset password** on the locked-out account. Nothing
else in this section applies.

### Step 2 — you have shell access: the recovery CLI

The same binary that serves the UI carries three offline recovery flags. They
read and write the SQLite database at `DB_PATH` directly, **lose no data**, and
work even when every password is forgotten.

| Flag | Effect |
|---|---|
| `-list-users` | Print every account: username, role, status, last login, created. Never prints a password or a hash. |
| `-reset-password <username>` | Prompt for a new password and store it on that account. |
| `-create-admin <username>` | Prompt for a password and create a new **admin**. Works even when accounts already exist — unlike `POST /setup`. |

```bash
# systemd / bare binary — run as the service user so the new file ownership
# and the database's own permissions stay correct.
sudo -u garage-webui DB_PATH=/var/lib/garage-webui-ng/garage-webui-ng.db \
  /usr/local/bin/garage-webui-ng -list-users

sudo -u garage-webui DB_PATH=/var/lib/garage-webui-ng/garage-webui-ng.db \
  /usr/local/bin/garage-webui-ng -reset-password admin

# Docker — -it is required, otherwise there is no terminal to prompt on.
docker run -it --rm -v garage-webui-ng_webui_data:/data \
  --entrypoint /main ghcr.io/d7eeem/garage-webui-ng:latest -reset-password admin

# ...or against the running container.
docker compose exec webui /main -list-users
```

Output looks like this — note that no hash appears anywhere:

```
USERNAME  ROLE    STATUS  LAST LOGIN            CREATED
admin     admin   active  2026-08-04 09:12:44Z  2026-06-01 10:02:11Z
reader    viewer  active  -                     2026-06-01 10:04:02Z
```

What these commands are, and are not:

- **They are not HTTP endpoints, and never will be.** An unauthenticated
  password reset reachable over the network is a backdoor. These are process
  flags that require local read/write access to `DB_PATH` — anyone who can run
  them could already edit the database file by hand, so they grant no new
  capability.
- **The password is never a command-line argument.** It is prompted for with
  terminal echo disabled and asked twice, so it never reaches your shell
  history, `ps` output or process accounting. For scripted recovery a single
  line piped on stdin is accepted instead (`printf '%s\n' "$NEW" | … -reset-password admin`),
  which skips the confirmation prompt — mind your history settings if you do
  that.
- **They enforce the same password policy as the UI** (§2), because they call
  the same validation code.
- **Stopping the service is not required.** WAL mode plus a busy timeout makes
  the concurrent write safe, and credentials are read per-login rather than
  cached, so a reset takes effect on the next sign-in. Stopping it anyway is
  the conservative choice and costs nothing.
- **Existing sessions are not signed out.** Sessions live in memory (§8); a
  password change — from the CLI or the UI — does not revoke one already
  established.
- **They run schema migrations**, like the server does. Pointing them at an old
  backup copy upgrades that copy.

### Step 3 — last resort: reset the database

Only if you have no shell access to the host at all.

1. **Stop** the container / process.
2. Remove the database from the volume — the file at `DB_PATH` **and its WAL
   sidecars**:
   ```bash
   docker compose stop webui
   docker run --rm -v garage-webui-ng_webui_data:/data busybox \
     sh -c 'rm -f /data/garage-webui-ng.db /data/garage-webui-ng.db-wal /data/garage-webui-ng.db-shm'
   docker compose start webui
   ```
   (Adjust the volume name to match your stack — `docker volume ls` shows it.)
3. Reopen the UI. `needsSetup` is `true` again and the wizard asks for a new
   administrator.

> **This deletes every account**, including viewers, along with their creation
> and last-login history. Nothing else is affected: buckets, keys and objects
> live in Garage, not here.

---

## 10. Legacy import (`AUTH_USER_PASS`)

Pre-database releases read accounts from `AUTH_USER_PASS` and
`AUTH_VIEWER_USER_PASS` on every request. They are now an **import-only** path,
kept so an existing deployment survives the upgrade:

- They are read **exactly once**, at startup, and **only when the users table is
  empty**. After that the database is authoritative and the variables are never
  consulted again — editing them has no effect.
- Format is unchanged: comma-separated `username:bcrypt-hash` entries; within an
  entry the **first** `:` splits the two (bcrypt hashes contain neither `:` nor
  `,`). Malformed entries are skipped.
- Only `$2a$`, `$2b$` and `$2y$` hashes are accepted; anything else is skipped
  with a log line naming the user (never the hash).
- Hashes are stored **verbatim** — re-hashing them would lock every existing
  operator out.
- Admins are imported before viewers, so a name present in both variables lands
  as an admin and its viewer entry is dropped.
- Legacy usernames are **not** checked against the current username charset, so
  an existing login keeps working even if the app would no longer hand it out.
- On a successful import the server logs:
  `Initial administrator imported from AUTH_USER_PASS (N user(s)).`

Once you have logged in, **remove the variables** from your compose file or unit
— see [`UPGRADING.md`](UPGRADING.md), which explains the one trap worth knowing
about (keeping them *and* running without a volume).

---

## 11. Not implemented (future extension points)

None of the following exists today. They are recorded so the shape of the schema
and the API is understood as deliberate, not accidental.

- **Fine-grained RBAC.** The `role` column holds one of two strings. A richer
  model would add `roles` and `permissions` tables and a join, leaving `users`
  otherwise intact; the two current values would become seeded rows.
- **MFA / TOTP.** Would add a per-user secret (and recovery codes) in a new
  migration, plus a second step between password verification and
  `Session.Renew` in the login handler.
- **OAuth / OIDC and LDAP.** Would map an external identity onto a local user
  row — the local account stays the authorisation subject, so roles, the audit
  log and the lockout guards keep working unchanged. `password_hash` would
  become optional for externally-authenticated accounts.
- **Session revocation across restarts.** Session storage is in-memory today;
  persisting it would also make “sign out everywhere” implementable.

---

## See also

- [`UPGRADING.md`](UPGRADING.md) — migrating an existing deployment
- [`../README.md`](../README.md) — install, configuration, security summary
- [`../CLAUDE.md`](../CLAUDE.md) — architecture reference
