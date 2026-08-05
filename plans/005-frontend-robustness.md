# Plan 005: Fix the alias-less bucket crash, broken search, and the auth status contract

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md` — unless a reviewer dispatched you and told you they
> maintain the index.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- src/pages/buckets/page.tsx src/pages/keys/page.tsx src/pages/buckets/manage/page.tsx src/app/app.tsx src/hooks/useAuth.ts backend/router/auth.go`
> If any in-scope file changed since this plan was written, compare the
> "Current state" excerpts against the live code before proceeding; on a
> mismatch, treat it as a STOP condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: `plans/002-verification-baseline.md`
- **Category**: bug
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

Three unrelated frontend defects, grouped because they share a test setup:

1. **A bucket with no alias crashes the entire Buckets page.** The list is
   sorted with `a.aliases[0].localeCompare(b.aliases[0])`. A bucket's `aliases`
   array is built from its global and local aliases, and Garage permits a bucket
   with neither — a bucket created by ID, or one whose aliases were all removed.
   For such a bucket `aliases[0]` is `undefined`, `.localeCompare` throws a
   `TypeError` during render, and because the app has no error boundary the
   whole page goes blank. There is no recovery short of removing the bucket via
   another tool.

2. **"Case-insensitive" search is not.** Both the Buckets and Keys pages
   lowercase the search query and then compare it against the *un*-lowercased
   field. Searching for a bucket named `Backups` fails for every possible
   input: `Backups` fails because the query was lowercased, `backups` fails
   because the name was not. The Cluster page's node search does it correctly,
   so the codebase contains both the bug and its own fix.

3. **`/auth/status` always reports `authenticated: true`.** The handler
   initializes `isAuthenticated := true` and never assigns `false`. In practice
   this is masked — the route sits behind the auth middleware, so an
   unauthenticated caller gets a 401 before reaching it — but the consequence is
   real: a logged-out client can never read the `enabled` flag, so
   `useAuth().isEnabled` is permanently `undefined`. Any future login-page logic
   that wants to know "is auth even turned on here?" has no way to ask.

## Current state

### Files

- `src/pages/buckets/page.tsx` — bucket list; contains the crash and one search bug.
- `src/pages/keys/page.tsx` — key list; contains the other search bug.
- `src/pages/buckets/manage/page.tsx` — bucket detail; degrades badly for alias-less buckets.
- `src/app/app.tsx` — app root; has no error boundary.
- `backend/router/auth.go` — the `/auth/status` handler.
- `src/hooks/useAuth.ts` — its client.
- `src/pages/cluster/components/nodes-list.tsx` — **the exemplar**: correct search, do not modify.

### Excerpt 1 — the crash

`src/pages/buckets/page.tsx:12-35`:

```tsx
  const items = useMemo(() => {
    let buckets =
      data?.map((bucket) => {
        return {
          ...bucket,
          aliases: [
            ...(bucket.globalAliases || []),
            ...(bucket.localAliases?.map((l) => l.alias) || []),
          ],
        };
      }) || [];

    if (search?.length > 0) {
      const q = search.toLowerCase();
      buckets = buckets.filter(
        (bucket) =>
          bucket.id.includes(q) ||
          bucket.aliases.find((alias) => alias.includes(q))
      );
    }

    buckets = buckets.sort((a, b) => a.aliases[0].localeCompare(b.aliases[0]));

    return buckets;
  }, [data, search]);
```

Line 33 is the crash. Lines 25-30 are search bug #1: `q` is lowercased,
`bucket.id` and `alias` are not.

### Excerpt 2 — search bug #2

`src/pages/keys/page.tsx:45-52`:

```tsx
  const items = useMemo(() => {
    if (!search?.length) {
      return data;
    }

    const q = search.toLowerCase();
    return data?.filter((item) => item.id.includes(q) || item.name.includes(q));
  }, [data, search]);
```

Same defect. Additionally `item.name` is typed as `string` but comes from the
Garage API, which can return a key with no name — worth defending against.

### Excerpt 3 — the correct pattern to copy

`src/pages/cluster/components/nodes-list.tsx:65-80`. **Do not modify this file** —
it is here as the exemplar:

```tsx
  const items = useMemo(() => {
    return nodes
      .filter((item) => {
        if (filter.search) {
          const q = filter.search.toLowerCase();
          return (
            item.hostname.toLowerCase().includes(q) ||
            item.id.includes(q) ||
            item.addr.includes(q) ||
            item.role?.zone?.includes(q) ||
            item.role?.tags?.find((tag) => tag.toLowerCase().includes(q))
          );
        }

        return true;
      })
```

Note it lowercases both sides for `hostname` and `tags`. (`id` and `addr` are
hex/numeric so it does not bother — that is a defensible choice, though
lowercasing uniformly is simpler and is what this plan does.)

### Excerpt 4 — the degraded detail page

`src/pages/buckets/manage/page.tsx:39-51`:

```tsx
const ManageBucketPage = () => {
  const { id } = useParams();
  const { data, error, isLoading } = useBucket(id);

  const name = data?.globalAliases[0];

  return (
    <>
      <Page
        title={name || "Manage Bucket"}
        prev="/buckets"
        actions={data ? <MenuButton /> : undefined}
      />
```

and `:65-73`:

```tsx
      {data && (
        <div className="container">
          <BucketContext.Provider
            value={{ bucket: data, refetch, bucketName: name || "" }}
          >
            <TabView tabs={tabs} className="bg-base-100 h-14 px-1.5" />
          </BucketContext.Provider>
        </div>
      )}
```

For an alias-less bucket, `bucketName` becomes `""`. The Browse tab then issues
requests to `/api/browse/` with an empty bucket name — which fails confusingly
rather than saying "this bucket has no alias." Note the backend's browse
handlers look buckets up **by global alias**, not by ID
(`backend/router/browse.go:295` uses `GetBucketInfo?globalAlias=`), so browsing
an alias-less bucket is genuinely impossible, not just awkward. The UI should
say so.

### Excerpt 5 — no error boundary

`src/app/app.tsx`, the whole file:

```tsx
import { PageContextProvider } from "@/context/page-context";
import Router from "./router";
import { Toaster } from "sonner";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import ThemeProvider from "@/components/containers/theme-provider";
import "./styles.css";

const App = () => {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <PageContextProvider>
      <QueryClientProvider client={queryClient}>
        <Router />
      </QueryClientProvider>
      <Toaster richColors />
      <ThemeProvider />
    </PageContextProvider>
  );
};

export default App;
```

### Excerpt 6 — the auth status handler

`backend/router/auth.go:47-64`:

```go
func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	isAuthenticated := true
	authSession := utils.Session.Get(r, "authenticated")
	enabled := false

	if utils.GetEnv("AUTH_USER_PASS", "") != "" {
		enabled = true
	}

	if authSession != nil && authSession.(bool) {
		isAuthenticated = true
	}

	utils.ResponseSuccess(w, map[string]bool{
		"enabled":       enabled,
		"authenticated": isAuthenticated,
	})
}
```

And its registration, `backend/router/router.go:11-16` — note `GetStatus` is on
`router`, which is wrapped by the auth middleware at line 33, while `Login` is
on the unwrapped `mux`:

```go
	auth := &Auth{}
	mux.HandleFunc("POST /auth/login", auth.Login)

	router := http.NewServeMux()
	router.HandleFunc("POST /auth/logout", auth.Logout)
	router.HandleFunc("GET /auth/status", auth.GetStatus)
```

`src/hooks/useAuth.ts`, the whole file:

```ts
import api from "@/lib/api";
import { useQuery } from "@tanstack/react-query";

type AuthResponse = {
  enabled: boolean;
  authenticated: boolean;
};

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

### Repo conventions to match

- **Components** are function components with a `type Props = {...}` above them,
  default-exported at the bottom. See `src/pages/buckets/components/bucket-card.tsx`.
- **Derived lists** go through `useMemo` with an explicit dependency array. See
  the three `items` memos quoted above.
- **Styling** is Tailwind utility classes inline plus daisyUI component classes.
  Empty/error states use `<Alert status="error" icon={<CircleXIcon />}>` from
  `react-daisyui` — see `src/pages/buckets/manage/page.tsx:59-63` and
  `src/pages/buckets/manage/browse/object-list.tsx:55-57`.
- **Icons** come from `lucide-react`.
- **Go handlers** end in `utils.ResponseSuccess` / `utils.ResponseError`.
- **Tests** (added by plan 002): Vitest with globals enabled — `describe`, `it`,
  `expect`, `vi` are available without imports. Testing Library React is
  installed. jsdom is the environment. Model on `src/lib/utils.test.ts`.

## Commands you will need

| Purpose         | Command                                    | Expected on success |
|-----------------|--------------------------------------------|---------------------|
| Frontend deps   | `pnpm install`                             | exit 0              |
| Typecheck       | `pnpm run typecheck`                       | exit 0              |
| Frontend tests  | `pnpm run test`                            | all pass            |
| Lint            | `pnpm run lint`                            | exit 0              |
| Frontend build  | `pnpm run build`                           | exit 0              |
| Go build        | `cd backend && go build ./...`             | exit 0              |
| Go vet          | `cd backend && go vet ./...`               | exit 0, no output   |
| Go tests        | `cd backend && go test -race ./...`        | `ok` per package    |

`typecheck` and `test` are added by plan 002. If they do not exist, see STOP
conditions.

## Scope

**In scope** (the only files you should modify or create):

- `src/pages/buckets/page.tsx`
- `src/pages/buckets/page.test.tsx` (create)
- `src/pages/keys/page.tsx`
- `src/pages/buckets/manage/page.tsx`
- `src/app/app.tsx`
- `src/components/containers/error-boundary.tsx` (create)
- `src/hooks/useAuth.ts`
- `backend/router/auth.go`
- `backend/router/auth_test.go` (create)

**Out of scope** (do NOT touch, even though they look related):

- `src/pages/cluster/components/nodes-list.tsx` — its search is already correct.
  It is the exemplar, not a target.
- `src/pages/buckets/components/bucket-card.tsx` — already defensive
  (`data.aliases?.join(", ")` at line 18 handles the empty case fine).
- `src/pages/buckets/hooks.ts` and `src/pages/keys/hooks.ts` — data fetching is
  correct; the bugs are in the components' derived state.
- `backend/middleware/auth.go` — enforcement is correct and unchanged.
  `GetStatus` staying behind the middleware is deliberate; step 6 does not move
  it.
- `backend/router/router.go` — route registration is unchanged.
- `src/components/layouts/main-layout.tsx` — its redirect at line 26 is correct
  given `useAuth`'s current shape and stays correct after step 7.

## Git workflow

- Branch: `advisor/005-frontend-robustness`
- Conventional commits. Examples from `git log`: `fix: err cluster page for
  garage v0.9.x`, `fix: make s3_region configurable`.
- Suggested commits: `fix: handle buckets with no alias`, `fix: make bucket and
  key search case-insensitive`, `feat: add error boundary`, `fix: report actual
  authentication state from /auth/status`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Fix the sort crash and the search in the Buckets page

In `src/pages/buckets/page.tsx`, replace the `items` memo body (lines 12-35)
with:

```tsx
  const items = useMemo(() => {
    let buckets =
      data?.map((bucket) => {
        return {
          ...bucket,
          aliases: [
            ...(bucket.globalAliases || []),
            ...(bucket.localAliases?.map((l) => l.alias) || []),
          ],
        };
      }) || [];

    if (search?.length > 0) {
      const q = search.toLowerCase();
      buckets = buckets.filter(
        (bucket) =>
          bucket.id.toLowerCase().includes(q) ||
          bucket.aliases.some((alias) => alias.toLowerCase().includes(q))
      );
    }

    buckets = buckets.sort((a, b) =>
      (a.aliases[0] ?? "").localeCompare(b.aliases[0] ?? "")
    );

    return buckets;
  }, [data, search]);
```

Three changes: `.toLowerCase()` on both compared values, `.some()` instead of
`.find()` (same semantics, clearer intent and a real boolean), and `?? ""` on
both sides of the sort comparison.

`?? ""` sorts alias-less buckets first, which is a reasonable default — they are
the ones needing attention.

**Verify**: `pnpm run typecheck && pnpm run lint` → exit 0 from both.

### Step 2: Surface alias-less buckets in the card

An alias-less bucket now renders with an empty title, which looks like a
rendering bug rather than a data condition. In `src/pages/buckets/page.tsx`,
where `BucketCard` is rendered (around line 54), nothing changes — instead fix
the card itself is **out of scope**, so handle it in the list by giving the card
a non-empty alias list.

In the `data?.map(...)` block, after building `aliases`, leave it as-is — do
**not** inject a placeholder string into the data, because `aliases` is also
what search matches against and a placeholder would become searchable.

Instead, in `src/pages/buckets/page.tsx`, pass an explicit display name:

```tsx
          {items?.map((bucket) => (
            <BucketCard
              key={bucket.id}
              data={{
                ...bucket,
                aliases: bucket.aliases.length
                  ? bucket.aliases
                  : ["(no alias)"],
              }}
            />
          ))}
```

This keeps `bucket-card.tsx` untouched (it is out of scope) while making the
condition visible to the operator.

**Verify**: `pnpm run typecheck && pnpm run lint && pnpm run build` → exit 0.

### Step 3: Fix the search in the Keys page

In `src/pages/keys/page.tsx`, replace the `items` memo (lines 45-52):

```tsx
  const items = useMemo(() => {
    if (!search?.length) {
      return data;
    }

    const q = search.toLowerCase();
    return data?.filter(
      (item) =>
        item.id.toLowerCase().includes(q) ||
        (item.name ?? "").toLowerCase().includes(q)
    );
  }, [data, search]);
```

The `?? ""` on `name` guards against a key with no name, which the Garage API
permits.

**Verify**: `pnpm run typecheck && pnpm run lint` → exit 0.

### Step 4: Tell the user when a bucket cannot be browsed

In `src/pages/buckets/manage/page.tsx`, the bucket detail page, make the
alias-less case explicit rather than silently broken.

Change line 43 to derive both a name and a flag:

```tsx
  const name = data?.globalAliases?.[0];
  const hasAlias = !!name;
```

Then inside the `{data && (...)}` block, render a warning above the tabs when
there is no alias:

```tsx
      {data && (
        <div className="container">
          {!hasAlias && (
            <Alert
              status="warning"
              icon={<CircleXIcon />}
              className="mb-4 items-start text-sm"
            >
              <span>
                This bucket has no global alias. Object browsing is unavailable
                until one is added — the storage API addresses buckets by alias.
                Add an alias in the Overview tab.
              </span>
            </Alert>
          )}
          <BucketContext.Provider
            value={{ bucket: data, refetch, bucketName: name || "" }}
          >
            <TabView tabs={tabs} className="bg-base-100 h-14 px-1.5" />
          </BucketContext.Provider>
        </div>
      )}
```

`Alert` and `CircleXIcon` are already imported in this file (lines 6 and 16).

Note the optional chain added to `globalAliases?.[0]` — currently
`data?.globalAliases[0]` would throw if the API ever omitted the array. Cheap
insurance.

**Verify**: `pnpm run typecheck && pnpm run lint && pnpm run build` → exit 0.

### Step 5: Add an error boundary so one bad row cannot blank the app

Create `src/components/containers/error-boundary.tsx`:

```tsx
import { Component, ErrorInfo, ReactNode } from "react";
import { Alert } from "react-daisyui";
import { CircleXIcon } from "lucide-react";

type Props = {
  children: ReactNode;
};

type State = {
  error: Error | null;
};

// React only supports error boundaries as class components; there is no hook
// equivalent. This is the one class component in the codebase.
class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Unhandled render error:", error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="p-4 md:p-8">
          <Alert status="error" icon={<CircleXIcon />} className="items-start">
            <div>
              <p className="font-medium">Something went wrong.</p>
              <p className="text-sm opacity-80">{this.state.error.message}</p>
            </div>
          </Alert>
        </div>
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
```

Then wrap the router in `src/app/app.tsx`:

```tsx
import { PageContextProvider } from "@/context/page-context";
import Router from "./router";
import { Toaster } from "sonner";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import ThemeProvider from "@/components/containers/theme-provider";
import ErrorBoundary from "@/components/containers/error-boundary";
import "./styles.css";

const App = () => {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <PageContextProvider>
      <QueryClientProvider client={queryClient}>
        <ErrorBoundary>
          <Router />
        </ErrorBoundary>
      </QueryClientProvider>
      <Toaster richColors />
      <ThemeProvider />
    </PageContextProvider>
  );
};

export default App;
```

Note `eslint.config.js` enables `react-refresh/only-export-components` as a
warning. A default-exported class component satisfies it. If lint complains
anyway, report the exact rule rather than disabling it inline.

**Verify**: `pnpm run typecheck && pnpm run lint && pnpm run build` → exit 0.

### Step 6: Make `/auth/status` report the real state

In `backend/router/auth.go`, rewrite `GetStatus`:

```go
func (c *Auth) GetStatus(w http.ResponseWriter, r *http.Request) {
	enabled := utils.GetEnv("AUTH_USER_PASS", "") != ""

	// When authentication is disabled every request is implicitly authorized,
	// which is what the middleware does too (middleware/auth.go).
	isAuthenticated := !enabled

	if authSession, ok := utils.Session.Get(r, "authenticated").(bool); ok && authSession {
		isAuthenticated = true
	}

	utils.ResponseSuccess(w, map[string]bool{
		"enabled":       enabled,
		"authenticated": isAuthenticated,
	})
}
```

Two improvements over the original beyond the logic fix: the type assertion is
now the comma-ok form, so a session value that is somehow not a `bool` returns
false instead of panicking; and `enabled` is derived directly rather than through
an if-statement.

Behavior after this change:

| `AUTH_USER_PASS` | session | response |
|---|---|---|
| unset | any | `{enabled: false, authenticated: true}` |
| set | valid | `{enabled: true, authenticated: true}` |
| set | absent | 401 from the middleware — handler not reached |

The third row is why this is a correctness fix rather than a security fix: the
middleware already blocks that case.

**Verify**:

```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

→ exit 0, no output.

Add `backend/router/auth_test.go` (package `router`):

- `TestGetStatusAuthDisabled` — `t.Setenv("AUTH_USER_PASS", "")`, assert
  `enabled == false` and `authenticated == true`.
- `TestGetStatusAuthEnabledNoSession` — `t.Setenv("AUTH_USER_PASS", "u:hash")`,
  assert `enabled == true` and `authenticated == false`.

**Critical — do NOT call `GetStatus` directly.** `scs.SessionManager.Get`
(reached via `utils.Session.Get`) *panics* — `scs: no session data in context` —
when the request context has not been through the scs `LoadAndSave` middleware.
A bare `httptest.NewRequest` has not, so a direct call crashes the whole
`router` test binary. Invoke the handler *through* the middleware instead.
`utils.InitSessionManager()` returns the `*scs.SessionManager` you need:

```go
func TestGetStatusAuthDisabled(t *testing.T) {
	t.Setenv("AUTH_USER_PASS", "")
	sessMgr := utils.InitSessionManager() // also sets the package-global utils.Session
	handler := sessMgr.LoadAndSave(http.HandlerFunc((&Auth{}).GetStatus))

	req := httptest.NewRequest(http.MethodGet, "/auth/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var body struct {
		Enabled       bool `json:"enabled"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled != false || body.Authenticated != true {
		t.Errorf("got enabled=%v authenticated=%v; want false,true", body.Enabled, body.Authenticated)
	}
}
```

Through `LoadAndSave` the context is populated, so `Session.Get` returns nil
(no `"authenticated"` key) rather than panicking, and the comma-ok assertion in
`GetStatus` leaves `isAuthenticated` at `!enabled`. Model the second test the
same way with the enabled env and the inverted assertion.

**Verify**: `cd backend && go test -race ./router/...` → `ok`, and
`grep -c "^func Test" backend/router/auth_test.go` → 6 (007's 4 + these 2).

### Step 7: Make the auth hook's return type honest

In `src/hooks/useAuth.ts`, the two flags are currently `boolean | undefined`,
which forces every consumer into three-state logic. Now that the endpoint
reports real values, narrow them:

```ts
import api from "@/lib/api";
import { useQuery } from "@tanstack/react-query";

type AuthResponse = {
  enabled: boolean;
  authenticated: boolean;
};

export const useAuth = () => {
  const { data, isLoading } = useQuery({
    queryKey: ["auth"],
    queryFn: () => api.get<AuthResponse>("/auth/status"),
    retry: false,
  });

  return {
    isLoading,
    isEnabled: data?.enabled ?? false,
    isAuthenticated: data?.authenticated ?? false,
  };
};
```

Defaulting to `false` is the safe direction: when the status call fails (which
includes the 401 case), the app treats the user as unauthenticated and
`main-layout.tsx:26` redirects to login. That is the current behavior too —
`undefined` is falsy — so this is a type-level clarification, not a behavior
change. Confirm that by reading `src/components/layouts/main-layout.tsx:22-28`:

```tsx
  if (auth.isLoading) {
    return null;
  }

  if (!auth.isAuthenticated) {
    return <Navigate to="/auth/login" />;
  }
```

**Verify**: `pnpm run typecheck && pnpm run build` → exit 0.

### Step 8: Add a regression test for the crash

Create `src/pages/buckets/page.test.tsx`. The goal is a test that fails on the
old code and passes on the new.

Rendering the whole page needs a `QueryClientProvider` and a router, which is a
lot of scaffolding for one assertion. Prefer testing the sort comparator
directly: extract it from the memo into a named export in
`src/pages/buckets/page.tsx`:

```tsx
export const compareByFirstAlias = (
  a: { aliases: string[] },
  b: { aliases: string[] }
) => (a.aliases[0] ?? "").localeCompare(b.aliases[0] ?? "");
```

and use it in the memo: `buckets = buckets.sort(compareByFirstAlias);`

Note: `eslint.config.js` has `react-refresh/only-export-components` set to
`warn` with `allowConstantExport: true`. Exporting a non-component arrow
function from a component module may produce a warning. If `pnpm run lint`
fails (rather than warns) because of it, move `compareByFirstAlias` into a new
file `src/pages/buckets/utils.ts` and import it — do not disable the rule
inline.

Then the test:

- sorts `[{aliases:["b"]},{aliases:["a"]}]` → `a` first
- does not throw for `[{aliases:[]},{aliases:["a"]}]`, and puts the alias-less
  entry first
- does not throw when both entries have empty `aliases`

Also test the search predicate. Extract it the same way if it makes the test
clean, or assert on the filter behavior through a small helper. Cases:

- query `"BACK"` matches a bucket aliased `backups`
- query `"back"` matches a bucket aliased `Backups`
- query `"zzz"` matches nothing

**Verify**: `pnpm run test` → all pass, including the new file.

Sanity-check that the test actually catches the bug: temporarily revert
`compareByFirstAlias` to `a.aliases[0].localeCompare(b.aliases[0])` and confirm
the empty-alias test fails with a `TypeError`. Then restore the fix. Do not
commit the temporary revert.

## Test plan

New tests:

| File | Tests | Covers |
|---|---|---|
| `src/pages/buckets/page.test.tsx` | 3 sort + 3 search | the crash and search bug #1 |
| `backend/router/auth_test.go` | 2 | the auth status contract |

Structural pattern: match `src/lib/utils.test.ts` (Vitest, globals enabled) and
`backend/utils/utils_test.go` (stdlib `testing`, `t.Setenv`) from plan 002.

**Not covered, and honestly so**: the error boundary (step 5) and the Keys page
search (step 3). The boundary needs a component render test with a deliberately
throwing child, which is doable with Testing Library but adds a `console.error`
suppression dance for one low-risk component. The Keys search is the same
one-line fix as the Buckets search, which *is* tested. Write them if the effort
is small; report the omission if you skip them.

**Verification**: `pnpm run test` → all pass.
`cd backend && go test -race ./...` → `ok`.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run lint` exits 0
- [ ] `pnpm run test` exits 0, including the new bucket page tests
- [ ] `pnpm run build` exits 0
- [ ] `cd backend && go build ./...` exits 0
- [ ] `cd backend && go vet ./...` exits 0 with no output
- [ ] `cd backend && test -z "$(gofmt -l .)"` exits 0
- [ ] `cd backend && go test -race ./...` exits 0, including the new auth tests
- [ ] `grep -n "aliases\[0\]\.localeCompare" src/pages/buckets/page.tsx` returns no matches
- [ ] `grep -cn "toLowerCase" src/pages/keys/page.tsx` returns at least `3`
- [ ] `grep -n "isAuthenticated := true" backend/router/auth.go` returns no matches
- [ ] `grep -rn "ErrorBoundary" src/app/app.tsx` returns a match
- [ ] `git status` shows only the in-scope files (plus `plans/README.md`) modified or created
- [ ] `plans/README.md` status row for 005 updated

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the excerpts above.
- `pnpm run typecheck` or `pnpm run test` does not exist as a script — plan 002
  has not landed. Do not build the test infrastructure yourself.
- `utils.Session.Get` still panics in step 6's tests *even after* invoking the
  handler through `sessMgr.LoadAndSave` as step 6 now prescribes. That would mean
  the scs API changed and the middleware no longer populates the context — report
  the panic trace; do not fall back to a direct call or skip the test silently.
  (A *direct* call panicking is expected and is why step 6 uses `LoadAndSave` —
  that is no longer a STOP condition, it is the documented approach.)
- `pnpm run lint` *fails* — not warns — because of
  `react-refresh/only-export-components` after step 8's extraction. Move the
  function to its own module as described; if that also fails, report the rule
  output rather than adding an eslint-disable comment.
- The Buckets page still crashes for some input after step 1. That would mean
  the crash has a second cause this plan did not find — report the stack trace.

## Maintenance notes

For the human/agent who owns this code after the change lands:

- **The error boundary is a safety net, not a fix.** It catches render errors
  app-wide and shows a message instead of a blank page, but a caught error still
  means a broken screen. It should stay empty of "expected" errors — if it
  starts firing routinely, something upstream needs fixing.
- **`?? ""` in the sort comparator sorts alias-less buckets first.** If the
  maintainer would rather sort them last, flip to `a.aliases[0] ?? "￿"`.
  Mentioned because it is the kind of thing a reviewer will have an opinion on.
- **Browse genuinely cannot work without a global alias.** The backend resolves
  buckets via `GetBucketInfo?globalAlias=` (`backend/router/browse.go:295`).
  Step 4's warning is the honest UI for that. If someone later wants browsing by
  bucket ID, the backend lookup has to change first — that is a real feature,
  not a UI tweak.
- **Reviewer should scrutinize**: that `toLowerCase()` was applied to *both*
  sides of every comparison (the original bug was applying it to one side, which
  looks correct at a glance), and that step 6's truth table matches the
  middleware's actual behavior.
- **Deliberately deferred**: a shared `matchesSearch` helper across the three
  list pages. Three call sites with slightly different fields is not yet
  duplication worth abstracting, and a premature helper would make each page's
  search behavior harder to read. Revisit at the fourth list page.
