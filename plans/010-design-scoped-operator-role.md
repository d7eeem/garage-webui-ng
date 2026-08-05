# Plan 010 (design/spike): A read-only operator role

> **Executor instructions**: This is a **design and spike** plan, not a
> build-everything plan. Your deliverable is a written design document with a
> recommendation, backed by a throwaway prototype of the enforcement layer —
> not production code, not a merged feature. Follow the steps, answer the open
> questions with evidence, and stop at the boundary marked "do not build past
> here." If anything in the "STOP conditions" section occurs, stop and report.
> When done, update the status row for this plan in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- backend/middleware/auth.go backend/router/ src/hooks/useAuth.ts`
> If any of these changed since this plan was written, compare the "Current
> state" excerpts against the live code before proceeding.

## Status

- **Priority**: P3
- **Effort**: M (spike: ~1 day; the follow-up build is separately estimated by your output)
- **Risk**: LOW (nothing ships from this plan)
- **Depends on**: `plans/007-session-hardening.md` should land first — it touches the same session code and this design assumes token renewal exists.
- **Category**: direction
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

Authentication in this application is a single boolean. `AUTH_USER_PASS` defines
one credential; a session either has `authenticated = true` or it does not. And
what that one credential grants is *everything*: the API router ends in a
catch-all that reverse-proxies any unmatched request to the Garage admin API
with the cluster's admin bearer token attached. Log in, and you can delete every
bucket and rewrite the cluster layout.

There is no way to give someone access to browse buckets without also giving
them the ability to destroy the cluster.

The architecture makes a scoped role unusually cheap to add on the server side:
enforcement has exactly one chokepoint, the auth middleware, and the session
layer already exists to carry a claim. The expensive part is the frontend — the
UI calls Garage admin endpoints directly and renders every destructive control
unconditionally, so a role that the server enforces but the UI ignores produces
a screen full of buttons that return 403.

That asymmetry — cheap server, expensive client — is exactly what a spike should
measure before anyone commits to it.

## Current state

### Files

- `backend/middleware/auth.go` — the single enforcement chokepoint (27 lines).
- `backend/router/router.go` — route registration, including the catch-all proxy (35 lines).
- `backend/router/proxy.go` — the admin API passthrough (28 lines).
- `backend/router/auth.go` — login, logout, status.
- `backend/utils/session.go` — the session wrapper.
- `src/hooks/useAuth.ts` — the client's view of auth state.
- `src/pages/cluster/hooks.ts` — an example of the frontend calling admin endpoints directly.

### Excerpt 1 — the chokepoint

`backend/middleware/auth.go`, the whole file:

```go
package middleware

import (
	"errors"
	"khairul169/garage-webui/utils"
	"net/http"
)

func AuthMiddleware(next http.Handler) http.Handler {
	authData := utils.GetEnv("AUTH_USER_PASS", "")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := utils.Session.Get(r, "authenticated")

		if authData == "" {
			next.ServeHTTP(w, r)
			return
		}

		if auth == nil || !auth.(bool) {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
```

One function, one decision. This is where a role check goes.

### Excerpt 2 — everything behind it

`backend/router/router.go`, the whole file:

```go
func HandleApiRouter() *http.ServeMux {
	mux := http.NewServeMux()

	auth := &Auth{}
	mux.HandleFunc("POST /auth/login", auth.Login)

	router := http.NewServeMux()
	router.HandleFunc("POST /auth/logout", auth.Logout)
	router.HandleFunc("GET /auth/status", auth.GetStatus)

	config := &Config{}
	router.HandleFunc("GET /config", config.GetAll)

	buckets := &Buckets{}
	router.HandleFunc("GET /buckets", buckets.GetAll)

	browse := &Browse{}
	router.HandleFunc("GET /browse/{bucket}", browse.GetObjects)
	router.HandleFunc("GET /browse/{bucket}/{key...}", browse.GetOneObject)
	router.HandleFunc("PUT /browse/{bucket}/{key...}", browse.PutObject)
	router.HandleFunc("DELETE /browse/{bucket}/{key...}", browse.DeleteObject)

	mux.Handle("/", middleware.AuthMiddleware(router))

	// Proxy request to garage api endpoint
	router.HandleFunc("/", ProxyHandler)
	return mux
}
```

Note the shape: nine explicit routes, then `router.HandleFunc("/", ProxyHandler)`
catches everything else. The explicit routes are enumerable and easy to classify
by verb. **The catch-all is the hard part** — it forwards arbitrary paths, and
Garage's admin API v2 uses `POST` for read-only operations
(`GetClusterStatus`, `GetBucketInfo`, `GetKeyInfo`), so HTTP verb alone cannot
distinguish read from write.

### Excerpt 3 — the proxy

`backend/router/proxy.go`, the whole file:

```go
func ProxyHandler(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(utils.Garage.GetAdminEndpoint())
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/api")
			r.Out.Header.Set("Authorization", fmt.Sprintf("Bearer %s", utils.Garage.GetAdminKey()))
		},
	}

	proxy.ServeHTTP(w, r)
}
```

Every proxied request carries the cluster admin token. That is by design — this
is an admin UI — and the design is *why* a role has to be enforced before the
proxy, not inside Garage.

### Excerpt 4 — what the frontend calls directly

`src/pages/cluster/hooks.ts:26-88`, abbreviated:

```ts
export const useClusterStatus = () => {
  return useQuery({
    queryKey: ["status"],
    queryFn: () => api.get<GetStatusResult>("/v2/GetClusterStatus"),
  });
};
...
export const useAssignNode = (options?: Partial<UseMutationOptions>) => {
  return useMutation<any, Error, AssignNodeBody>({
    mutationFn: (data) =>
      api.post("/v2/UpdateClusterLayout", {
        body: { parameters: null, roles: [data] },
      }),
    ...(options as any),
  });
};
```

These `/v2/...` paths hit the catch-all proxy. Enumerate all of them — that
enumeration is step 2 of this spike.

### Excerpt 5 — the client's auth state

`src/hooks/useAuth.ts`, the whole file:

```ts
export const useAuth = () => {
  const { data, isLoading } = useQuery({
    queryKey: ["auth"],
    queryFn: () => api.get<AuthResponse>("/auth/status"),
    retry: false,
  });
  return {
    isLoading,
    isEnabled: data?.enabled,
    isAuthenticated: data?.authenticated,
  };
};
```

A role would ride along in this response. `plans/005-frontend-robustness.md`
rewrites `GetStatus` and this hook — read that plan's step 6 and 7 before
designing the response shape, so your proposal builds on it rather than
conflicting.

### Excerpt 6 — how credentials are configured today

`backend/router/auth.go:25-34`:

```go
	userPass := strings.Split(utils.GetEnv("AUTH_USER_PASS", ""), ":")
	if len(userPass) < 2 {
		utils.ResponseErrorStatus(w, errors.New("AUTH_USER_PASS not set"), 500)
		return
	}

	if strings.TrimSpace(body.Username) != userPass[0] || bcrypt.CompareHashAndPassword([]byte(userPass[1]), []byte(body.Password)) != nil {
```

One user, one env var, `username:bcrypt_hash`. Any multi-role design has to
answer how a second credential is configured — that is open question 1.

### Repo conventions to match

- **Middleware** takes and returns `http.Handler`; see the excerpt above.
- **Handlers** are methods on empty structs ending in `utils.ResponseSuccess` /
  `utils.ResponseErrorStatus`.
- **Session values** go through the `utils.Session` wrapper, not scs directly.
- **No new dependencies.**

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Go build | `cd backend && go build ./...` | exit 0 |
| Go vet | `cd backend && go vet ./...` | exit 0, no output |
| Go run (spike) | `cd backend && go run main.go` | server starts on :3909 |
| Enumerate frontend API calls | `grep -rn "api\.\(get\|post\|put\|delete\)" src/` | list of call sites |
| Typecheck | `pnpm run typecheck` | exit 0 |

A running Garage instance is **strongly recommended** but not strictly required —
the enforcement prototype can be evaluated against 403 responses without a real
cluster behind it. Say in your report which you had.

## Scope

**In scope** — a throwaway prototype plus a written design:

- `plans/design/010-scoped-operator-role.md` (create — the deliverable)
- A **complete inventory** of every API call the frontend makes, classified
  read vs. write. This is the most valuable artifact of the spike and it goes
  in the design doc.
- Prototype code on a scratch branch, **not merged**: a role claim in the
  session and an allowlist check in `AuthMiddleware`.

**Do not build past here:**

- Do not implement a user store, a user-management UI, or per-bucket
  permissions. If the design concludes those are needed, that is a finding.
- Do not modify any frontend component to hide controls. Prototype at most
  *one* screen to measure the cost, and say how many more there are.
- Do not change `AUTH_USER_PASS`'s format in a way that breaks existing
  deployments without flagging it as breaking.

**Out of scope entirely:**

- OIDC / SSO / LDAP. If the maintainer wants real identity, that is a different
  project and this spike should say so rather than half-designing it.
- Garage-side permissions. Garage's own key permissions are per-bucket S3
  access, not admin-API scoping; they do not solve this.

## Git workflow

- Branch: `advisor/010-spike-scoped-operator-role`
- Commit the prototype freely on that branch; it is not going to be merged.
- The **only** file intended for merge is the design document.
- Do NOT open a PR for the prototype code.

## Steps

### Step 1: Inventory every API call the frontend makes

This is the foundation. Run:

```bash
grep -rn "api\.get\|api\.post\|api\.put\|api\.delete" src/
```

For each hit, record in a table: the file, the path, the HTTP verb, and whether
the operation **reads** or **mutates**. Known clusters to cover, from recon:

- `src/hooks/useConfig.ts`, `src/hooks/useAuth.ts`
- `src/pages/home/hooks.ts`
- `src/pages/cluster/hooks.ts` — the largest group, includes layout mutations
- `src/pages/buckets/hooks.ts`, `src/pages/buckets/manage/hooks.ts`
- `src/pages/keys/hooks.ts`, `src/pages/keys/page.tsx` (an inline `api.get` at
  line 27 — do not miss call sites outside `hooks.ts` files)
- `src/pages/buckets/manage/browse/hooks.ts`
- `src/pages/auth/hooks.ts`

**The classification is the point.** Watch for:

- Garage admin v2 uses `POST` for read-only calls (`GetClusterStatus`,
  `GetBucketInfo`, `GetKeyInfo`, `GetNodeInfo`). Verb-based rules are wrong.
- `GET /v2/GetKeyInfo?showSecretKey=true` (`src/pages/keys/page.tsx:27`) is a
  *read* that discloses a secret access key. Should a read-only role see it?
  That is a design decision, not a mechanical one — flag it.
- `GET /api/config` returns cluster configuration. After
  `plans/001-stop-serving-cluster-secrets.md` it no longer contains secrets, but
  confirm that against the current code.

**Verify**: your table has a row for every grep hit, and each row has a
read/write verdict. Count them and put the count in the design doc.

### Step 2: Prototype enforcement in the middleware

On your scratch branch, add a role to the session at login and check it in the
middleware. Sketch:

```go
// in AuthMiddleware, after the existing authenticated check
role, _ := utils.Session.Get(r, "role").(string)
if role == "viewer" && !isReadOnlyRequest(r) {
	utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
	return
}
```

The interesting function is `isReadOnlyRequest`. Because of the verb problem
(step 1), it cannot be `r.Method == "GET"`. It has to be an allowlist of paths.
Prototype it as an explicit set derived from your step-1 table, and note how
long the list is and how it would be maintained.

Then measure the failure mode: log in as the viewer role and click through the
UI. Record every place a button appears that returns 403.

**Verify**: you have a list of every UI control that breaks under the viewer
role. Count them.

### Step 3: Answer the open questions with evidence

**Q1 — How is a second credential configured?**
Options: a second env var (`AUTH_VIEWER_USER_PASS`); extending `AUTH_USER_PASS`
to a list with a role suffix (`user:hash:admin,viewer:hash:viewer`); a config
file. Each has a migration story for existing single-user deployments.
Recommend one and state explicitly whether it is backward compatible.

**Q2 — Allowlist or denylist?**
An allowlist fails closed: a new admin endpoint is denied to viewers until
someone adds it. A denylist fails open: a new destructive endpoint is permitted
until someone remembers. Recommend the allowlist and say what it costs in
maintenance (every new Garage admin endpoint the UI adopts needs a
classification). If you disagree, argue it.

**Q3 — How does the frontend learn its role, and what does it do with it?**
The role rides in `/auth/status` alongside `enabled`/`authenticated` (see
Excerpt 5, and read `plans/005-frontend-robustness.md` steps 6-7 first). The
harder half: how many components need a conditional? Use your step-2 count. If
it is a handful, this is cheap. If it is dozens, recommend a shared
`<RequireRole>` wrapper or a `useCan()` hook and estimate that instead.

**Q4 — What happens to the object browser under a viewer role?**
Browsing is `GET /browse/...` (read) but the same tab has upload, delete, and
create-folder controls. Note also `browse-tab.tsx:41` already gates the whole
tab on the bucket having a read+write key — so there is precedent in this
codebase for hiding a surface based on capability. Recommend whether viewers get
a read-only browser or no browser.

**Q5 — Is "viewer" the right and only second role?**
Plausible alternatives: viewer / operator (buckets and keys, no cluster layout) /
admin. Each additional role multiplies the classification work in step 1.
Recommend the smallest set that solves a real problem, and name the problem.

**Q6 — What is the actual attack this defends against?**
Be honest. A viewer role protects against *mistakes* by a semi-trusted colleague
far better than against a *malicious* one — and it does nothing about the fact
that the server still holds the admin token. If the real requirement is
defending against a hostile user, say that this design does not deliver it and
what would.

### Step 4: Write the design document

Create `plans/design/010-scoped-operator-role.md` with these sections:

1. **Verdict** — *recommended, build it* / *recommended with prerequisites* /
   *not worth the cost*. Lead with it, unhedged.
2. **API inventory** — the full table from step 1. This is the doc's most
   reusable artifact; include it even if the verdict is negative.
3. **Evidence** — answers to Q1-Q6 with what you observed.
4. **Proposed model** — the role set, how credentials are configured, the
   migration path for existing deployments.
5. **Server design** — the middleware change, the allowlist's shape and where it
   lives, how a new endpoint gets classified.
6. **Client design** — how the role reaches the UI, the count of components
   needing changes, and the pattern (per-component conditional vs. shared hook).
7. **Effort estimate** — S/M/L split into server and client, informed by your
   measured counts, not by intuition.
8. **What this does not protect against** — from Q6. Be direct.
9. **Open questions you could not resolve.**

**Verify**: `test -f plans/design/010-scoped-operator-role.md` → exit 0, and the
file has all nine sections.

### Step 5: Clean up

```bash
git status --short
```

Expected: `plans/design/010-scoped-operator-role.md` (new) and
`plans/README.md` (modified). The prototype stays on its scratch branch,
unmerged.

## Test plan

No tests. This plan produces a document and a throwaway prototype.

The prototype's "test" is step 2's click-through: how many controls break under
the viewer role. That number is the design's central input and the reason this
spike exists rather than someone just writing the middleware check.

## Done criteria

ALL must hold:

- [ ] `plans/design/010-scoped-operator-role.md` exists with all nine sections
- [ ] The verdict is stated first, unhedged
- [ ] The API inventory table covers every hit from
      `grep -rn "api\.get\|api\.post\|api\.put\|api\.delete" src/`, with a
      read/write verdict per row
- [ ] Q1-Q6 each have an answer backed by an observation or a measured count
- [ ] The client-side effort estimate cites the number of broken controls
      measured in step 2, not a guess
- [ ] The prototype branch exists and its name is recorded in the design doc
- [ ] `git status --short` on the delivery branch shows only the design doc and
      `plans/README.md`
- [ ] `cd backend && go build ./...` exits 0 on the delivery branch
- [ ] `plans/README.md` status row for 010 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The step-1 inventory turns out to be substantially larger than expected (say,
  more than 40 distinct endpoints). That changes the verdict's arithmetic —
  report the count and your read on whether an allowlist is maintainable at that
  size before continuing to prototype.
- Q1's best answer requires a breaking change to `AUTH_USER_PASS`'s format.
  Document it prominently as a prerequisite; do not design a silent migration.
- You conclude mid-spike that the honest verdict is "not worth the cost."
  **Write that up and stop.** Do not keep prototyping to justify the plan's
  existence. A negative verdict with a good inventory table is a successful
  spike.
- You find yourself building a user store, a user-management screen, or
  per-bucket permissions. Past the boundary.
- `plans/005-frontend-robustness.md` has not landed and `/auth/status` still has
  the `isAuthenticated := true` bug. Design against 005's corrected shape
  anyway, and note the dependency — do not fix it here.

## Maintenance notes

- **The API inventory outlives the verdict.** Even if nobody builds roles, a
  read/write classification of every endpoint the UI touches is useful
  documentation. Make it good.
- **Server-side enforcement without client-side hiding is worse than nothing**
  for usability — a UI full of buttons that 403 is a bug report generator. If
  the verdict is "build it," the client work is not optional polish; it is part
  of the feature. Make that explicit in the effort estimate.
- **This design does not remove the admin token from the server.** The server
  still holds full cluster authority and still proxies with it. A role is a
  guardrail for humans, not an isolation boundary. Q6 exists so the finished doc
  says that out loud.
