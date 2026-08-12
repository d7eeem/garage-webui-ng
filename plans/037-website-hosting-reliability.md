# Plan 037: Make bucket settings report whether they saved, and tell the user when a website's index document is missing

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. Do **not** edit `plans/README.md`; the reviewer
> who dispatched you maintains the index.
>
> **Drift check (run first)**, where `<BASE>` is the branch you were told to
> base on (see "Git workflow"):
> ```
> git diff --stat 796039f..<BASE> -- \
>   src/pages/buckets/manage/hooks.ts \
>   src/pages/buckets/manage/overview/overview-website-access.tsx \
>   src/pages/buckets/manage/overview/overview-quota.tsx
> ```
> Expected: **empty**. Then confirm the Go excerpt in "Current state" still
> matches `backend/router/browse.go`. On any mismatch, treat it as a STOP
> condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none. (Written alongside plans 031/033/034/035/036, but
  **base-agnostic**: none of them touch any region this plan edits, verified by
  the drift check above returning empty against the tip of that stack.)
- **Category**: bug + dx
- **Planned at**: commit `796039f`, 2026-08-06 — and verified byte-identical at
  the advisor stack tip `6bdcb25`, so either base works.

## Why this matters

Turn on website hosting for a bucket today and the UI tells you **nothing**
about whether it worked, at two separate levels.

**Level one: the save may have failed and you will never know.**
`overview-website-access.tsx` and `overview-quota.tsx` both auto-save on change
through `useUpdateBucket(...)`, called with **no** `onError`, backed by a hook
that supplies **no default**. When `POST /v2/UpdateBucket` returns 403, 500, or
anything else, the mutation rejects, the rejection is swallowed, and the form
keeps showing the value you typed. The user believes the setting is live. It is
not. There is also no "saving…"/"saved" affordance at all, so even a *successful*
save is invisible — the input is debounced 500 ms and then simply nothing
happens.

**Level two: the site can be configured perfectly and still 404.** Garage serves
`indexDocument` (default `index.html`) at the bucket's web root. If that object
is not in the bucket, every visitor gets an error and the console gives no hint —
it happily shows a green link to a URL that cannot work.

The app already has the machinery to check: `GET /api/browse/{bucket}/{key}` with
no `view`/`dl`/`thumb` parameter performs a `HeadObject`. But it is unusable for
this today, because **that branch reports a missing object as 500**, while the
sibling GET branch twenty lines below correctly maps it to 404. Without a real
404 you cannot distinguish "the index document isn't there" from "Garage is
down", and showing "missing!" on a transient backend error would be worse than
showing nothing.

After this plan: every bucket-settings save reports success or failure, and the
Website Access section warns inline when the configured index or error document
is not actually present in the bucket.

Target shape of the section (the layout is illustrative — match the repo's
existing markup, not this sketch):

```
Website Access                    [Info]
  Enabled                     [ ON  ]  ✓ Saved

  Index Document      Error Document
  [index.html      ]  [error/400.html ]
  ⚠ Not found in     ⚠ Not found in
    this bucket        this bucket

  🔗 https://assets.web.example.com   [Copy]
```

## Current state

### Files

- `src/pages/buckets/manage/hooks.ts` — the bucket mutation hooks.
  `useUpdateBucket` is at lines 42–51.
- `src/pages/buckets/manage/overview/overview-website-access.tsx` — the Website
  Access section (140 lines). Auto-save wiring at lines 32–49.
- `src/pages/buckets/manage/overview/overview-quota.tsx` — the Quotas section
  (82 lines). Identical auto-save bug at lines 21–34.
- `backend/router/browse.go` — `GetOneObject`; the HEAD branch is at lines
  99–113, the GET branch's correct 404 mapping at lines 112–126.
- `backend/router/browse_test.go` — existing tests. They cover **pure helper
  functions only**; there is no S3 mock for handler paths in the general case.

### `src/pages/buckets/manage/hooks.ts:42-51` — exactly as it exists today

```ts
export const useUpdateBucket = (id?: string | null) => {
  return useMutation({
    mutationFn: (values: any) => {
      return api.post<any>("/v2/UpdateBucket", {
        params: { id },
        body: values,
      });
    },
  });
};
```

No `options` parameter, no `onError`. This is the root of level one.

### `src/pages/buckets/manage/overview/overview-website-access.tsx:29-49` — exactly as it exists today

```tsx
  const websiteUrl = getBucketWebsiteBaseUrl(bucketName, config);

  const updateMutation = useUpdateBucket(data?.id);

  const onChange = useDebounce((values: DeepPartial<WebsiteConfigSchema>) => {
    const data = {
      enabled: values.websiteAccess,
      indexDocument: values.websiteAccess
        ? values.websiteConfig?.indexDocument
        : undefined,
      errorDocument: values.websiteAccess
        ? values.websiteConfig?.errorDocument
        : undefined,
    };

    updateMutation.mutate({
      websiteAccess: data,
    });
  });
```

and its form reset / watch wiring:

```tsx
  useEffect(() => {
    form.reset({
      websiteAccess: data?.websiteAccess,
      websiteConfig: {
        indexDocument: data?.websiteConfig?.indexDocument || "index.html",
        errorDocument: data?.websiteConfig?.errorDocument || "error/400.html",
      },
    });

    const { unsubscribe } = form.watch((values) => onChange(values));
    return unsubscribe;
  }, [data]);
```

The section renders `<ToggleField form={form} name="websiteAccess" label="Enabled" disabled={!canWrite} />`,
then — when enabled — two `<InputField>`s (`websiteConfig.indexDocument`,
`websiteConfig.errorDocument`) in a `grid grid-cols-1 md:grid-cols-2 gap-4`,
then either the URL block or a guidance `<Alert>`.

### `src/pages/buckets/manage/overview/overview-quota.tsx:21-34` — the same bug

```tsx
  const updateMutation = useUpdateBucket(data?.id);

  const onChange = useDebounce((values: DeepPartial<QuotaSchema>) => {
    const { enabled } = values;
    const maxObjects = Number(values.maxObjects);
    const maxSize = Math.round(Number(values.maxSize) * 1024 ** 3);

    const data = {
      maxObjects: enabled && maxObjects > 0 ? maxObjects : null,
      maxSize: enabled && maxSize > 0 ? maxSize : null,
    };

    updateMutation.mutate({ quotas: data });
  });
```

### `backend/router/browse.go:99-126` — the asymmetry to fix

```go
	if !view && !download && !thumbnail {
		object, err := client.HeadObject(r.Context(), &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			utils.ResponseError(w, err)          // ← always 500, even for "missing"
			return
		}
		utils.ResponseSuccess(w, object)
		return
	}

	object, err := client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		var ae smithy.APIError
		if errors.As(err, &ae) && ae.ErrorCode() == "NoSuchKey" {   // ← correct
			utils.ResponseErrorStatus(w, err, http.StatusNotFound)
			return
		}

		utils.ResponseError(w, err)
		return
	}
```

**Critical SDK detail — do not skip this.** A HEAD request has no response body,
so S3 cannot return a `NoSuchKey` error document. `aws-sdk-go-v2` therefore
surfaces a missing object on `HeadObject` as **`NotFound`** (error code
`"NotFound"`, concrete type `*types.NotFound`), *not* `NoSuchKey`. Matching only
on `NoSuchKey` — copying the GET branch verbatim — will silently not work. Match
both codes.

`smithy` and `errors` are already imported in this file (used by the GET
branch). `net/http` is already imported.

### The frontend contract for the existence check

`GET /api/browse/{bucket}/{key}` with **no** `view`/`dl`/`thumb` query parameter
hits the HEAD branch above. Two things about it:

- **The bucket is addressed by its global alias, not its ID.** Every
  `/browse/...` route resolves per-bucket S3 credentials via
  `GetBucketInfo?globalAlias=<name>`. Use `bucketName` from `useBucketContext()`,
  which is already that alias. Do **not** pass `data.id`.
- **It needs a bucket key with read+write permission.** A bucket with no such
  key returns an error from `getS3Client` — a **500**, not a 404. This is why
  the UI must treat "not 404" as **unknown** and render nothing, rather than
  assuming missing. `browse-tab.tsx` guards its whole tab on
  `bucket.keys.find((k) => k.permissions.read && k.permissions.write)` for the
  same reason.

`src/lib/api.ts` throws `APIError` with a `.status` field, so a caller can test
`(err as APIError).status === 404`.

### Repo conventions to match

- **Frontend data hooks**: one hook per endpoint, in the page's `hooks.ts`.
  Query keys are arrays. Mutation hooks spread `...options` **last** so a caller
  can override a default. `useAbortMultipart` and `useAddAlias` in the same file
  are the exemplars.
- **Error toasts**: `handleError` from `@/lib/utils` (`toast.error(err.message ||
  "Unknown error")`). Toasts come from `sonner`.
- **Forms**: react-hook-form + zod, fields via `InputField` / `ToggleField` from
  `src/components/ui/`. `FormControl` supplies the label and layout — read
  `src/components/ui/form-control.tsx` before adding anything under a field.
- **Guidance markup**: the existing `<Alert status="warning" icon={<CircleXIcon />} className="mt-4 items-start text-sm">`
  block at the bottom of `overview-website-access.tsx` is the tone and markup to
  match, including `<code>` around config keys.
- Icons from `lucide-react`; class composition via `cn` from `@/lib/utils`.
- The `@/` import alias maps to `src/`.
- **Go handlers**: methods on empty structs; `utils.ResponseError` does **not**
  stop the handler — always `return` after it.
- **`pnpm run lint` is expected to be red** (~55 pre-existing problems, CI runs
  it `continue-on-error`). Make *new* code lint-clean; do not clear the backlog.

## Commands you will need

| Purpose        | Command                                    | Expected on success |
|----------------|--------------------------------------------|---------------------|
| Install        | `pnpm install`                              | exit 0              |
| Typecheck      | `pnpm run typecheck`                        | exit 0              |
| Frontend tests | `pnpm run test`                             | all pass            |
| One test file  | `pnpm exec vitest run object-exists`        | all pass            |
| Frontend build | `pnpm run build`                            | exit 0              |
| Go gates       | `cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...` | no gofmt/vet output, all `ok` |

If `pnpm` is not on PATH it is at
`/home/t1nk33r/.local/share/mise/installs/node/26.3.1/bin/pnpm` — prepend that
directory. Do **not** substitute `npm` (the lockfile is `pnpm-lock.yaml`).
If `go` is not on PATH, run Go commands in the repo's pinned toolchain:
`docker run --rm -v "$PWD":/w -w /w/backend -e GOFLAGS=-buildvcs=false golang:1.25.12 sh -c '<cmd>'`
(use `golang:1.25.12`, Debian-based — `-race` needs gcc, which `-alpine` lacks;
`git` is not usable inside the container).

## Scope

**In scope**:

- `backend/router/browse.go` — the HEAD 404 mapping only.
- `backend/router/browse_test.go` — one new test.
- `src/pages/buckets/manage/hooks.ts` — `useUpdateBucket` gains options + a
  default `onError`; one new query hook.
- `src/pages/buckets/manage/overview/overview-website-access.tsx`
- `src/pages/buckets/manage/overview/overview-quota.tsx`
- `src/pages/buckets/manage/object-exists.test.ts` (create)

**Out of scope** (do NOT touch, even though they look related):

- The GET branch of `GetOneObject` and its existing `NoSuchKey` mapping — it is
  already correct. Only the HEAD branch changes.
- `PutObject`, `BulkDeleteObjects`, `DownloadArchive`, `CreateDownloadToken`,
  `maxUploadBytes`, `drainRequestBody` in `browse.go`, and everything under
  `src/pages/buckets/manage/browse/` — these belong to sibling plans. Your
  `browse.go` diff must contain no hunk inside any of them.
- `src/lib/website.ts`, `src/types/garage.ts`, `share-dialog.tsx`,
  `bucket-card.tsx`, `overview-website-access.tsx`'s URL block and guidance
  alert — the public-URL logic is a different plan's territory. You add warnings
  under the two document inputs; you do not touch how the URL is computed or
  rendered.
- `src/hooks/useDebounce.ts` — the 500 ms auto-save debounce stays.
- **Do not convert the auto-save forms to explicit Save buttons.** That is a
  larger UX change nobody asked for; this plan makes the existing auto-save
  *honest*, not different.
- Creating the missing index document for the user, or any write triggered by
  the check. The warning is advisory only.
- Any server-side fetch of the public website URL (a reachability probe). That
  would add an outbound-request surface to the backend and was explicitly
  deferred.
- `plans/README.md`.

## Git workflow

- Branch: `advisor/037-website-hosting-reliability`, created from the base your
  dispatcher names (`main`, or the tip of the advisor stack):
  `git checkout -B advisor/037-website-hosting-reliability <BASE>`
- Conventional-commit messages, matching `git log`, e.g.
  `fix: report bucket-setting save failures and missing website documents`.
- Do NOT push, open a PR, or merge.

## Steps

### Step 1: Map a missing object to 404 on the HEAD path

In `backend/router/browse.go`, replace the HEAD branch's error handling so a
genuinely-absent object returns 404 while everything else stays a 500.

Add a small helper next to the other package-level helpers at the bottom of the
file (after `normalizeListLimit`):

```go
// isNotFoundErr reports whether an S3 error means "this object does not exist".
//
// The two codes are NOT interchangeable. GetObject returns NoSuchKey with an
// XML error document; HEAD has no response body, so aws-sdk-go-v2 synthesizes
// NotFound instead. A caller that matches only one of them silently misses the
// other — which is why the HEAD branch used to answer 500 for a missing object.
func isNotFoundErr(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "NotFound", "NoSuchKey":
		return true
	}
	return false
}
```

Then in the HEAD branch:

```go
		if err != nil {
			if isNotFoundErr(err) {
				utils.ResponseErrorStatus(w, err, http.StatusNotFound)
				return
			}
			utils.ResponseError(w, err)
			return
		}
```

**Leave the GET branch exactly as it is.** It already works, it is covered by
existing behaviour, and rewriting it to use the new helper would widen this
plan's blast radius into the download path for no benefit. (If you find that
irritating, note it in your report — do not act on it.)

Add a table-driven test `TestIsNotFoundErr` to `backend/router/browse_test.go`,
modelled on `TestNormalizeListLimit` in the same file. Cover: a `*types.NotFound`
(the HEAD case), a `*types.NoSuchKey` (the GET case), an unrelated
`smithy.APIError` code such as `"AccessDenied"` → false, a plain
`errors.New("boom")` → false, and `nil` → false.

`github.com/aws/aws-sdk-go-v2/service/s3/types` is already imported in
`browse_test.go`.

**Verify**:
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./router/ -run TestIsNotFoundErr -v
```
→ no gofmt/vet output, build 0, `PASS` with 5 subtests.

### Step 2: Give `useUpdateBucket` options and a failure default

In `src/pages/buckets/manage/hooks.ts`, replace `useUpdateBucket` with:

```ts
export const useUpdateBucket = (
  id?: string | null,
  options?: UseMutationOptions<any, Error, any>
) => {
  return useMutation({
    mutationFn: (values: any) => {
      return api.post<any>("/v2/UpdateBucket", {
        params: { id },
        body: values,
      });
    },
    // These forms auto-save on change with no Save button, so a rejected
    // request has nothing to report it. Without this default the mutation
    // rejects silently and the form keeps showing a value the server never
    // accepted. `...options` is spread last so a caller can still override.
    onError: handleError,
    ...options,
  });
};
```

`UseMutationOptions` is already imported from `@tanstack/react-query` in this
file. Add `import { handleError } from "@/lib/utils";`.

**Verify**: `pnpm run typecheck` → exit 0.

### Step 3: Add the object-existence hook

Still in `src/pages/buckets/manage/hooks.ts`, add an exported **pure** classifier
plus the query hook. The classifier is exported separately so it can be
unit-tested without React or a network:

```ts
/** What we can say about a configured document after probing for it. */
export type ObjectPresence = "present" | "missing" | "unknown";

/**
 * Classifies the outcome of a HEAD probe.
 *
 * Only a 404 proves absence. Every other failure — most importantly the 500 a
 * bucket with no read+write key produces — means we could not tell, and the UI
 * must stay silent rather than accuse the user of a missing file.
 */
export const classifyObjectProbe = (
  isSuccess: boolean,
  error: unknown
): ObjectPresence => {
  if (isSuccess) return "present";
  if (error && (error as APIError).status === 404) return "missing";
  return "unknown";
};

/**
 * Probes whether `key` exists in `bucketName`. A GET to /browse/{bucket}/{key}
 * with no view/dl/thumb parameter performs a HeadObject server-side.
 *
 * `bucketName` is the bucket's GLOBAL ALIAS, not its id — every /browse route
 * resolves credentials via GetBucketInfo?globalAlias=.
 */
export const useObjectExists = (bucketName: string, key?: string | null) => {
  const query = useQuery({
    queryKey: ["object-exists", bucketName, key],
    queryFn: () => api.get(`/browse/${bucketName}/${encodeObjectPath(key!)}`),
    enabled: !!bucketName && !!key,
    // A 404 is the answer, not a transient failure worth retrying.
    retry: false,
  });

  return {
    presence: query.isLoading
      ? ("unknown" as ObjectPresence)
      : classifyObjectProbe(query.isSuccess, query.error),
    isLoading: query.isLoading,
  };
};
```

Import `api, { APIError, encodeObjectPath }` from `@/lib/api` (the file already
imports `api` as the default export — extend that import rather than adding a
second one).

**Verify**: `pnpm run typecheck` → exit 0.

### Step 4: Test the classifier

Create `src/pages/buckets/manage/object-exists.test.ts`. Model it on
`src/pages/buckets/manage/browse/hooks.test.ts` — plain `describe`/`it` from
`vitest`, no React, testing an exported pure function.

Cases:
1. success → `"present"`.
2. an `APIError` with `status === 404` → `"missing"`.
3. an `APIError` with `status === 500` → `"unknown"` — **this is the
   no-read+write-key case and the most important test in this plan**; a
   regression here makes the UI accuse users of missing files that are present.
4. an `APIError` with `status === 403` → `"unknown"`.
5. a plain `new Error("boom")` (no `status`) → `"unknown"`.
6. `isSuccess === false` with `error === null` → `"unknown"`.

**Verify**: `pnpm exec vitest run object-exists` → 6 passed.

### Step 5: Surface save state and the document warnings

In `src/pages/buckets/manage/overview/overview-website-access.tsx`:

**5a — save state.** Derive a small status node from the existing
`updateMutation` and render it beside the `Website Access` heading (the
`flex flex-row gap-2` div that already holds the label and the `Info` button):

- `updateMutation.isPending` → `Saving…`, muted (`text-xs text-base-content/60`).
- `updateMutation.isSuccess` → `✓ Saved` using the `Check` icon from
  `lucide-react`, `text-xs text-success`.
- `updateMutation.isError` → `Not saved` with `text-xs text-error`. The toast
  from Step 2's default `onError` carries the reason; this is the persistent
  marker that the form is out of sync with the server.
- otherwise → nothing.

Do **not** auto-clear the saved state on a timer. TanStack Query resets it on
the next mutation, which is the behaviour you want.

**5b — document warnings.** Read the **persisted** values from the bucket, not
the live form values:

```tsx
const indexDoc = data?.websiteConfig?.indexDocument;
const errorDoc = data?.websiteConfig?.errorDocument;
const indexPresence = useObjectExists(bucketName, data?.websiteAccess ? indexDoc : null);
const errorPresence = useObjectExists(bucketName, data?.websiteAccess ? errorDoc : null);
```

Probing the persisted value rather than the form value is deliberate and
load-bearing: the form value changes on **every keystroke**, which would fire a
request per character. `data` comes from `useBucketContext()` and only changes
when a save round-trips. Do not wire these to `useWatch`.

Render a warning **directly under** the matching `InputField`, only when that
field's presence is `"missing"`:

```tsx
<p className="text-xs text-warning mt-1">
  Not found in this bucket — visitors will get an error until you upload it.
</p>
```

`"unknown"` and `"present"` render nothing. There is no green "present" tick on
the fields; absence of a warning is the signal, which keeps the section quiet in
the normal case.

Because `InputField` renders through `FormControl`, you cannot pass the warning
as a child of `InputField`. Wrap each field and its warning in a `<div>` inside
the existing `grid grid-cols-1 md:grid-cols-2 gap-4`, so the grid still lays out
two columns.

**5c** — leave the URL block, the guidance `Alert`, the `ToggleField`, and the
`onChange`/`useEffect` wiring untouched.

**Verify**: `pnpm run typecheck && pnpm run build` → both exit 0.

### Step 6: Give the Quotas section the same save state

In `src/pages/buckets/manage/overview/overview-quota.tsx`, add the same
save-state node beside the `Quotas` title. `useUpdateBucket` already toasts on
failure after Step 2, so this is only the visible marker.

`ToggleField` renders its own `title`, so add the status next to it rather than
restructuring — a small `flex flex-row items-center gap-2` wrapper is fine. Do
not change the quota calculation, the schema, or the debounce.

**Consider extracting** the status node into a tiny shared component if writing
it twice is awkward — but if you do, put it in
`src/pages/buckets/manage/overview/save-status.tsx` (a new in-scope file) and
say so in your report. Duplicating ~8 lines is also acceptable; do not
over-engineer.

**Verify**: `pnpm run typecheck && pnpm run test && pnpm run build` → all exit 0.

### Step 7: Prove the new tests can fail

A test that cannot fail is not a test.

1. In `hooks.ts`, temporarily change `classifyObjectProbe`'s 404 check to
   `=== 400`. Run `pnpm exec vitest run object-exists` → the `"missing"` case
   must fail. **Revert.**
2. In `browse.go`, temporarily drop `"NotFound"` from `isNotFoundErr`'s switch.
   Run `go test -race ./router/ -run TestIsNotFoundErr` → the HEAD case must
   fail. **Revert.**

Report both failure counts, and confirm both files are back to their committed
state (`git status --porcelain` clean before you commit).

### Step 8: Full gates

```
pnpm run typecheck && pnpm run test && pnpm run build
cd backend && gofmt -l . && go vet ./... && go build ./... && go test -race ./...
```

All exit 0; no gofmt/vet output; all Go packages `ok`.

### Step 9: Live check

This requires a real Garage instance and a browser. You almost certainly have
neither. Do **not** claim it passed — state plainly in NOTES that it was not
performed, and list what a reviewer should check:

1. Website access on, bucket **without** `index.html` → warning under Index
   Document; upload `index.html` → warning clears on refetch.
2. Website access on a bucket **with no read+write key** → **no** warning
   (presence is `unknown`, not `missing`).
3. Revoke write permission / stop Garage, change a quota → error toast **and**
   the `Not saved` marker.

## Test plan

- **New**: `src/pages/buckets/manage/object-exists.test.ts`, 6 cases (Step 4).
  Pattern: `src/pages/buckets/manage/browse/hooks.test.ts`.
  The load-bearing one is **case 3** (500 → `"unknown"`).
- **New**: `TestIsNotFoundErr` in `backend/router/browse_test.go`, 5 subtests
  (Step 1). Pattern: `TestNormalizeListLimit` in the same file.
- **No component test** for either overview section: the repo has no existing
  test for those components to model on, and both new behaviours are thin
  renderings of state the two pure functions above already pin. Adding one is
  optional, not required.
- **No handler test** for the HEAD 404 path: `browse_test.go` covers pure
  helpers only because there is no S3 mock in that package. `isNotFoundErr` is
  extracted precisely so the logic is testable without one.
- Verification: `pnpm run test` and `cd backend && go test -race ./...` → all
  pass.

## Done criteria

Machine-checkable. ALL must hold. `<BASE>` is the branch you based on.

- [ ] `pnpm run typecheck` exits 0
- [ ] `pnpm run test` exits 0, with 6 new tests in `object-exists.test.ts`
- [ ] `pnpm run build` exits 0
- [ ] `cd backend && gofmt -l .` → no output; `go vet ./...` → no output;
      `go build ./...` → exit 0; `go test -race ./...` → all packages `ok`,
      including `TestIsNotFoundErr` (5 subtests)
- [ ] Step 7's two mutation checks each failed at least one test, and both
      files were reverted
- [ ] `git diff <BASE>..HEAD --stat` lists only the in-scope files
- [ ] `git diff <BASE>..HEAD -- backend/router/browse.go` contains **no hunk**
      inside `PutObject`, `BulkDeleteObjects`, `DownloadArchive`,
      `CreateDownloadToken`, `maxUploadBytes`, or `drainRequestBody`
- [ ] `git diff <BASE>..HEAD -- src/lib/website.ts src/types/garage.ts src/pages/buckets/manage/browse/`
      is **empty**
- [ ] `useUpdateBucket`'s `...options` is spread **after** `onError` (so a
      caller can override the default) — confirm by reading the function

## STOP conditions

Stop and report back (do not improvise) if:

- Any excerpt in "Current state" does not match the live code.
- `isNotFoundErr` cannot be made to match a real `HeadObject` miss because the
  SDK reports something other than `NotFound`/`NoSuchKey`. Report the actual
  error code rather than guessing at a broader match — **do not** fall back to
  treating every error as 404. That would make the UI claim files are missing
  whenever Garage is unreachable, which is worse than the current bug.
- Adding the default `onError` to `useUpdateBucket` starts producing toasts on
  page load or on a normal successful edit. That would mean the auto-save is
  firing spurious requests, which is a separate bug — report it.
- The existence probe fires on every keystroke. It must be wired to the
  persisted `data.websiteConfig`, never to `useWatch`/form state.
- You conclude the forms should get explicit Save buttons, or that the app
  should create the missing index document. Both are out of scope.
- A step's verification fails twice after a reasonable fix attempt.

## Maintenance notes

- **`useUpdateBucket` now has a default `onError` and `...options` spread last.**
  Any future caller that passes its own `onError` silently replaces the toast —
  that is intended, but such a caller must then report the failure itself. A
  reviewer should check this on every new call site.
- **`isNotFoundErr` matches two codes for one reason** (HEAD has no response
  body, so the SDK synthesizes `NotFound`). If a future refactor unifies the GET
  and HEAD branches on this helper, keep both codes — dropping `NoSuchKey` would
  break the download path's 404.
- **The probe costs one request per configured document per bucket view**, and
  it is a `HeadObject` against Garage. If bucket pages ever gain many such
  checks, batch them rather than adding more individual queries.
- **`"unknown"` is load-bearing, not laziness.** The check runs through the
  `/browse` path, which needs a read+write bucket key; buckets without one can
  never be probed. Any future change that collapses `unknown` into `missing`
  will show false warnings on exactly those buckets.
- **Deferred on purpose**: a live reachability probe of the public URL (needs a
  server-side outbound fetch — a real SSRF surface, and it must be constrained
  to the configured web endpoint), a dedicated Website tab, and per-bucket
  custom-domain/CNAME guidance. The maintainer scoped this plan to reliability
  and document verification on 2026-08-06.
