# Upgrading

Version-specific migration notes. Newest first — when a future release breaks
something, a new section is appended above the previous one rather than
rewriting history.

---

## Upgrading to the persistent user store

This release moves user accounts out of environment variables and into a SQLite
database that the app owns. **It is a breaking change for every existing
deployment.** Budget ten minutes and read the three breaking changes before you
pull the new image.

Full reference: [`authentication.md`](authentication.md).

### Breaking changes

1. **Authentication is mandatory.** The open (no-auth) mode is gone. A
   deployment with no accounts serves nothing but the first-run `/setup` wizard
   until an administrator is created. There is no opt-out.
2. **A persistent volume is required.** Accounts live in a SQLite file at
   `DB_PATH` (`/data/garage-webui-ng.db` in the image). Without a volume mounted
   at `/data`, every account you create is lost the next time the container is
   recreated.
3. **`AUTH_USER_PASS` and `AUTH_VIEWER_USER_PASS` are import-only.** They are
   read exactly once, at the first start against an **empty** database, and are
   then ignored forever. Editing them afterwards does nothing — the database is
   authoritative. Manage users in **Settings → Users** instead.

Nothing else changes: your `garage.toml`, `API_BASE_URL`, `S3_ENDPOINT_URL`,
`BASE_PATH` and every other setting behave exactly as before, and no data in
Garage itself is touched.

### Before you start

Note the credentials currently in `AUTH_USER_PASS` — you will sign in with the
same username and password after the upgrade. The hashes migrate verbatim, so
your existing passwords keep working.

---

### Path A — you already use `AUTH_USER_PASS`

Your accounts are imported automatically. Keep the variables set for this one
start, then remove them.

**1. Add the volume.** In `docker-compose.yml`:

```yaml
services:
  webui:
    image: ghcr.io/d7eeem/garage-webui-ng:latest
    volumes:
      - ./garage.toml:/etc/garage.toml:ro
      # The user database. Without this, every container recreation wipes
      # the accounts and the setup wizard starts over.
      - webui_data:/data

volumes:
  webui_data:
```

Or, for `docker run`, add `-v garage_webui_data:/data`.

**2. Pull and start the new image**, with `AUTH_USER_PASS` still set:

```bash
docker compose pull webui
docker compose up -d webui
```

**3. Confirm the import** in the logs:

```bash
docker compose logs webui | grep -E 'User database|imported'
```

You are looking for both of these:

```
User database: /data/garage-webui-ng.db
Initial administrator imported from AUTH_USER_PASS (1 user(s)).
```

The count includes any `AUTH_VIEWER_USER_PASS` accounts, which are imported with
the `viewer` role. Entries whose password is not a bcrypt hash (`$2a$`, `$2b$`
or `$2y$`) are skipped with a log line naming the user.

If you see the `User database:` line but no `imported` line, the database was
not empty — the import had already happened on an earlier start, which is fine
and expected on any restart after the first.

**4. Log in** with your existing username and password, and check
**Settings → Users**: your accounts should be listed with their roles.

**5. Remove the variables.** Delete `AUTH_USER_PASS` and
`AUTH_VIEWER_USER_PASS` from your compose file / unit / `.env` and recreate the
container. From here on they are dead weight.

> ### ⚠️ The one trap: keeping the variables *and* running without a volume
>
> If you leave `AUTH_USER_PASS` set **and** never mount `/data`, then every
> container recreation starts from an empty database, silently re-imports the
> legacy accounts, and **discards every user you created in the UI** — along
> with every password change, role change and disable.
>
> Because you can still log in with the env-var credentials afterwards, this
> looks like it is working. It is the failure mode that hides a missing volume.
> Mount the volume, then remove the variables, and both halves of the trap are
> gone.

---

### Path B — you ran with no authentication

There was nothing to import, so the new instance starts empty.

1. **Add the volume** exactly as in step 1 of Path A.
2. Pull and start the new image.
3. Open the UI. Every route redirects to **`/setup`**. The startup log says the
   same thing:
   ```
   No users configured — open http://0.0.0.0:3909/setup to create the first administrator.
   ```
4. Create the administrator: username (1–64 chars, `A–Z a–z 0–9 . _ @ -`) and a
   password of at least 10 characters. Finishing the wizard signs you in.
5. Add any further accounts in **Settings → Users**. Use the `viewer` role for
   read-only access.

The wizard closes permanently once that first account exists —
`POST /api/setup` answers `409` from then on.

There is no way to keep the old open mode. If the UI was previously reachable
only because it sat on a trusted network, that network isolation is still worth
keeping; it is now defence in depth rather than the only control.

---

### Verifying the migration

```bash
# 1. The database is where you think it is, on the volume.
docker compose logs webui | grep 'User database:'
#    → User database: /data/garage-webui-ng.db

# 2. The instance is set up (needsSetup must be false).
curl -s http://localhost:3909/api/auth/status
#    → {"authenticated":false,"enabled":true,"needsSetup":false,"role":"","username":""}

# 3. The volume really is a volume.
docker inspect -f '{{ range .Mounts }}{{ .Destination }} {{ end }}' garage-webui-ng
#    → /etc/garage.toml /data
```

Then, in the UI: **Settings → Users** lists every account with its role, status,
last login and creation date. That table is the authoritative view — if an
account you expected is missing, it was never imported.

Last check, and the one that actually proves persistence:

```bash
docker compose up -d --force-recreate webui
```

Log back in. Your users are still there. If they are not, the volume is not
mounted where you think it is.

---

### Rollback

The change is forward-only in one direction: the new release never writes to the
environment variables, and the old release never reads the database.

1. Pin the previous image tag (`ghcr.io/d7eeem/garage-webui-ng:<old-tag>`) and
   recreate the container.
2. The old release reads `AUTH_USER_PASS` live, so **keep those variables set
   until you are confident in the upgrade** — that is the only reason to delay
   step 5 of Path A.
3. Accounts created in the UI after the upgrade do **not** exist for the old
   release. They stay in the database file, untouched, and reappear if you roll
   forward again; the volume is safe to leave mounted either way.

---

### Reference compose snippet

```yaml
name: garage-webui-ng

services:
  webui:
    image: ghcr.io/d7eeem/garage-webui-ng:latest
    container_name: garage-webui-ng
    restart: unless-stopped
    volumes:
      - ./garage.toml:/etc/garage.toml:ro
      - webui_data:/data          # ← required: the user database
    ports:
      - "3909:3909"
    environment:
      API_BASE_URL: http://garage:3903
      S3_ENDPOINT_URL: http://garage:3900
      # Set this whenever TLS is terminated in front of the UI.
      SESSION_COOKIE_SECURE: "true"
      # AUTH_USER_PASS / AUTH_VIEWER_USER_PASS: import-only. Set them for the
      # first start of an upgraded deployment, then delete them.
    healthcheck:
      test: ["CMD", "/main", "-health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s

volumes:
  webui_data:
```

Bind-mounting a host directory instead of a named volume works too, but the
container runs as uid/gid **65532**, so chown the directory first:

```bash
mkdir -p ./webui-data && sudo chown 65532:65532 ./webui-data
```

Otherwise startup fails fast with `cannot open user database`.

---

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `cannot open user database … permission denied` and the process exits | `/data` is a bind mount not writable by uid 65532 | `chown 65532:65532` the host directory, or use a named volume |
| The setup wizard appears even though `AUTH_USER_PASS` is set | The hash was skipped — it is not `$2a$`/`$2b$`/`$2y$`, or the entry is malformed | Check the logs for `Skipping legacy user`; fix the value, or just create the account in the wizard |
| Users vanish after every deploy | No volume at `/data` | Mount one (see above) |
| Changing `AUTH_USER_PASS` has no effect | Working as designed — it is import-only | Change the password in **Settings → Account**, or reset it from **Settings → Users** |
| Signed out after every restart | Working as designed — session storage is in memory | Log back in; accounts are unaffected |
| `403 invalid or missing CSRF token` from a script | The double-submit token is required on all writes except login and setup | Do a `GET` first, keep the `csrf_token` cookie, and echo it in `X-CSRF-Token` |
| Locked out of every admin account | No admin credential remains | Reset the database — [`authentication.md` §9](authentication.md#9-lockout--recovery). **This deletes all users.** |
