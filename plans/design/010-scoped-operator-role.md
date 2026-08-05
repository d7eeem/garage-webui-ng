# Design 010: A read-only / scoped operator role

> Spike output. Deliverable is this recommendation. Grounded in a full inventory
> of the frontend's API calls and verb-behavior tests against a local Garage
> v2.0.0 (Docker). Written 2026-07-30 against `integration-check`.

## 1. Verdict

**Recommended, and cheaper on the server than expected — but only worth building
with the client half included.** The spike's central worry (Garage v2 uses POST
for read-only calls, so a role can't be enforced by HTTP verb) turned out to be
**false for this UI**: the web UI calls every read via `GET` and every mutation
via `POST`/`PUT`/`DELETE`, and Garage honors those verbs. That makes a read-only
role enforceable with a ~10-line allowlist at the single middleware chokepoint.

The real cost is the frontend: ~8 screens render write controls unconditionally,
and a role the server enforces but the UI ignores produces buttons that 403. So
this is a small-server / medium-client feature, and the client work is part of
the feature, not optional polish. Build it if "hand this UI to someone who should
only look" is a real need; the server side is genuinely easy.

**Important scope caveat (Q6):** this defends against *mistakes by a semi-trusted
operator*, not against a *malicious* one — the server still holds the full admin
token and still proxies with it. See §8.

## 2. API inventory

Every endpoint the frontend calls (from `git grep "api\.(get|post|put|delete)"`
over `src/`), classified. **R** = read, **W** = write/mutation.

| Endpoint | Verb | R/W | Notes |
|---|---|---|---|
| `/auth/status` | GET | R | auth state |
| `/auth/login` | POST | — | auth op — must be allowed for any role |
| `/auth/logout` | POST | — | auth op — must be allowed for any role |
| `/config` | GET | R | post-001: no secrets |
| `/buckets` | GET | R | webui aggregate |
| `/browse/{bucket}` | GET | R | list objects |
| `/browse/{bucket}/{key...}` | GET | R | get/view/download object |
| `/browse/{bucket}/{key...}` | PUT | **W** | upload |
| `/browse/{bucket}/{key...}` | DELETE | **W** | delete object |
| `/v2/GetClusterHealth` | GET | R | |
| `/v2/GetClusterStatus` | GET | R | |
| `/v2/GetClusterLayout` | GET | R | |
| `/v2/GetNodeInfo` | GET | R | |
| `/v2/GetBucketInfo` | GET | R | |
| `/v2/ListKeys` | GET | R | |
| `/v2/GetKeyInfo?showSecretKey=true` | GET | R* | **reads, but discloses a secret access key** — see below |
| `/v2/CreateBucket` | POST | **W** | |
| `/v2/UpdateBucket` | POST | **W** | quota / website config |
| `/v2/DeleteBucket` | POST | **W** | destructive |
| `/v2/AddBucketAlias` | POST | **W** | |
| `/v2/RemoveBucketAlias` | POST | **W** | |
| `/v2/AllowBucketKey` | POST | **W** | grants permission |
| `/v2/DenyBucketKey` | POST | **W** | |
| `/v2/CreateKey` | POST | **W** | |
| `/v2/ImportKey` | POST | **W** | |
| `/v2/DeleteKey` | POST | **W** | destructive |
| `/v2/ConnectClusterNodes` | POST | **W** | |
| `/v2/UpdateClusterLayout` | POST | **W** | assign/unassign (2 call sites) |
| `/v2/RevertClusterLayout` | POST | **W** | |
| `/v2/ApplyClusterLayout` | POST | **W** | destructive — rewrites layout |

**28 distinct endpoints. Read = GET, write = POST/PUT/DELETE, with exactly two
exceptions to encode by hand:** `/auth/login` and `/auth/logout` are POST but
must be allowed for a viewer, and `GetKeyInfo?showSecretKey=true` is a GET that
returns a secret.

## 3. Evidence

- **Verb split holds (the key finding).** Against local Garage v2.0.0, all read
  endpoints returned `200` via `GET`
  (`GetClusterHealth/Status/Layout`, `ListKeys`, `GetBucketInfo`), and
  `CreateBucket` via `GET` returned `400` — Garage requires the mutating verb. So
  the proxy faithfully carries the frontend's verb, and verb == capability for
  this UI. A verb-based rule needs no per-endpoint allowlist to be *safe*
  (it fails closed: an unknown POST is denied to a viewer).
- **Single chokepoint.** All non-login routes pass through
  `middleware.AuthMiddleware` (`backend/router/auth.go` / `middleware/auth.go`),
  wrapped once in `router.go`. One function is the whole enforcement surface.
- **Session already carries data.** After plan 007, `utils.Session` is set up
  with `Renew`, and `/auth/status` (fixed in 005) reports real state — so adding
  a `role` claim rides existing machinery.
- **The `showSecretKey` read is real.** `src/pages/keys/page.tsx:27` calls
  `GET /v2/GetKeyInfo?showSecretKey=true` to reveal a secret access key in the
  UI. This is a GET, so a naive "GET = allowed" rule would let a viewer read
  every bucket's secret keys. Must be special-cased.

## 4. Proposed model

- **Two roles: `admin` (default, today's behavior) and `viewer` (read-only).**
  Do not over-build a permission matrix; two roles solve the stated need. More
  granular roles multiply the client work in §6 without a named requirement.
- **Config**: add a second optional credential,
  `AUTH_VIEWER_USER_PASS` (same `username:bcrypt` format as `AUTH_USER_PASS`).
  Backward compatible — unset means "no viewer account," and existing
  single-admin deployments are unchanged. `Login` checks both and stamps the
  session `role` accordingly. (Alternative — a role suffix on a single list-valued
  `AUTH_USER_PASS` — is more compact but a breaking format change; prefer the new
  var.)

## 5. Server design

In `AuthMiddleware`, after the existing authenticated check, add:

```go
role, _ := utils.Session.Get(r, "role").(string)
if role == "viewer" && !isViewerAllowed(r) {
    utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
    return
}
```

`isViewerAllowed` is a small, **fail-closed** predicate:

```go
func isViewerAllowed(r *http.Request) bool {
    if r.Method == http.MethodGet { // all reads are GET (verified)
        // one carve-out: never let a viewer reveal secret keys
        if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") &&
           r.URL.Query().Get("showSecretKey") == "true" {
            return false
        }
        return true
    }
    // the only non-GET a viewer may call is logout
    return r.Method == http.MethodPost && r.URL.Path == "/auth/logout"
}
```

That is the entire server enforcement. It fails closed: any future write endpoint
is denied to viewers until someone deliberately allows it. Note the path here is
post-`StripPrefix` (the `/api` prefix is already stripped in `router.go`).

`Login` sets `role = "admin"` or `"viewer"` based on which credential matched.
`/auth/status` should also return the role so the client can adapt (§6).

## 6. Client design

This is where the cost is. A viewer whose writes 403 but whose buttons still
render is a bug-report generator, so the UI must hide/disable write controls.
The controls, by screen (≈8 sites):

- **Buckets list**: `CreateBucketDialog`.
- **Bucket manage**: update (quota/website), add/remove alias, allow/deny key,
  delete bucket (`MenuButton`).
- **Browse tab**: upload, create folder, delete object.
- **Keys**: create key, delete key, and the **"View" secret** button (maps to the
  `showSecretKey` carve-out).
- **Cluster**: connect node, assign/unassign, apply/revert layout.

Recommended pattern: expose `role` via the existing `useAuth()` hook (it already
returns `isEnabled`/`isAuthenticated` after plan 005) and add a `useCan()` /
`isViewer` derived flag, then gate the write controls. A shared
`<RequireWrite>` wrapper or a `can.write` boolean beats scattering conditionals.
Precedent exists: `browse-tab.tsx:41` already hides the whole browse surface when
the bucket lacks a read+write key — the codebase does capability-based hiding.

Count for the estimate: ~8 screens, ~15 individual controls.

## 7. Effort estimate

- **Server**: `AUTH_VIEWER_USER_PASS` parsing + role on session + the
  `isViewerAllowed` predicate + 2-3 Go tests → **S** (a few hours).
- **Client**: `role` through `useAuth`, a `useCan` helper, gate ~15 controls
  across ~8 screens, plus hiding the cluster page's write affordances → **M**
  (~1 day, most of it mechanical).
- **Docs**: one env var + a short "roles" section → **S**.
- Total: **M**, server-light / client-heavy.

## 8. What this does NOT protect against

State this plainly in any build:

- The server still holds the full Garage **admin token** and still proxies with
  it. A viewer role is a **guardrail for humans**, not an isolation boundary.
- It defends well against *accidental* damage by a semi-trusted colleague (the
  intended use). It does **not** defend against a malicious viewer who bypasses
  the UI, because the only thing standing between a session and the admin API is
  this middleware — which is exactly the thing being trusted. A determined
  attacker with a viewer session cannot escalate through the *intended* API
  (writes are 403), but any bug in the predicate is a full escalation.
- It does not scope by bucket/tenant — a viewer sees *everything*, just can't
  change it. Per-bucket scoping is a much larger feature and out of scope.

## 9. Open questions

- **Should a viewer see the object browser at all?** Browsing is GET (allowed),
  but a viewer could still read every object's contents. If that's too much,
  `isViewerAllowed` can also gate `/browse/`. Product decision.
- **`showSecretKey` for admins over plain HTTP** is a separate pre-existing
  exposure (secret keys travel to the browser); not this spike's problem, but
  noted while here.
- **Role source beyond env vars**: if the project ever wants real user
  management (many operators, audit of who did what), this env-var model is a
  stopgap, and OIDC/SSO is the actual answer — a much larger project this spike
  explicitly does not design.

## Reproduction

```bash
# verb behavior against local Garage (admin token in scratchpad/garage-local/admin_token)
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3903/v2/GetClusterStatus   # 200 via GET
curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:3903/v2/CreateBucket        # 400 — requires POST
```
