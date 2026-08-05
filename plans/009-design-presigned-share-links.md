# Plan 009 (design/spike): Presigned share links for private buckets

> **Executor instructions**: This is a **design and spike** plan, not a
> build-everything plan. Your deliverable is a working throwaway prototype plus
> a written design document with a recommendation — not production code, not a
> merged feature. Follow the steps, answer the open questions with evidence
> from the prototype, and stop at the boundary marked "do not build past here."
> If anything in the "STOP conditions" section occurs, stop and report. When
> done, update the status row for this plan in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat ee420fb..HEAD -- src/pages/buckets/manage/browse/share-dialog.tsx backend/router/browse.go`
> If either file changed since this plan was written, compare the "Current
> state" excerpts against the live code before proceeding.

## Status

- **Priority**: P3
- **Effort**: M (spike: ~1 day; the follow-up build is separately estimated by your output)
- **Risk**: LOW (nothing ships from this plan)
- **Depends on**: none. Overlaps `plans/006-object-key-url-encoding.md` — read the note in Scope.
- **Category**: direction
- **Planned at**: commit `ee420fb`, 2026-07-24

## Why this matters

The share dialog builds a plain public website URL and pastes it into a text
box. It works only when the bucket has website access enabled — and the dialog
knows it, because it renders a warning banner saying exactly that. So the
feature's own UI documents its limitation.

Meanwhile the backend already constructs an authenticated, per-bucket S3 client
using credentials it fetches from the Garage admin API. The AWS SDK v2 that
client comes from ships `s3.NewPresignClient`, which turns that same client into
time-limited signed URLs. The capability is roughly one endpoint away from code
that already exists.

The product question this answers: **can a Garage operator share a single object
with someone, for a bounded time, without making the whole bucket public?**
Today the answer is no. That is a meaningful gap for the use case this UI serves
— an operator handing a file to a colleague.

The spike exists because the interesting parts are not the SDK call. They are:
how long links should live, whether issuing them should be recorded, what
happens when the underlying access key is rotated or its permission revoked, and
whether the share dialog should offer both kinds of link or replace one with the
other.

## Current state

### Files

- `src/pages/buckets/manage/browse/share-dialog.tsx` — the current share UI (79 lines).
- `backend/router/browse.go` — has `getS3Client`, the piece you will reuse.
- `backend/router/router.go` — where a new route would register.
- `src/pages/buckets/manage/browse/object-actions.tsx` — opens the dialog.

### Excerpt 1 — the current share dialog

`src/pages/buckets/manage/browse/share-dialog.tsx:14-47`:

```tsx
const ShareDialog = () => {
  const { isOpen, data, dialogRef } = shareDialog.use();
  const { bucket, bucketName } = useBucketContext();
  const { data: config } = useConfig();
  const [domain, setDomain] = useState(bucketName);

  const websitePort = config?.s3_web?.bind_addr?.split(":").pop() || "80";
  const rootDomain = config?.s3_web?.root_domain;

  const domains = useMemo(
    () => [
      bucketName,
      bucketName + rootDomain,
      bucketName + rootDomain + `:${websitePort}`,
    ],
    [bucketName, config?.s3_web]
  );

  useEffect(() => {
    setDomain(bucketName);
  }, [domains]);

  const url = "http://" + domain + "/" + data?.prefix + data?.key;

  return (
    <Modal ref={dialogRef} open={isOpen} backdrop>
      <Modal.Header className="truncate">Share {data?.key || ""}</Modal.Header>
      <Modal.Body>
        {!bucket.websiteAccess && (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1" />
            Sharing is only available for buckets with enabled website access.
          </Alert>
        )}
```

Line 36 is the URL construction — hardcoded `http://`, no encoding, no
expiry, no signature. Lines 42-47 are the self-documented limitation.

Note this dialog reads `config?.s3_web`, which comes from `GET /api/config`.
**Plan 001 narrows that endpoint's response** — `s3_web.bind_addr` and
`s3_web.root_domain` both survive, so this dialog keeps working. Confirm that
before assuming otherwise.

### Excerpt 2 — the S3 client you will reuse

`backend/router/browse.go:328-357`:

```go
func getS3Client(bucket string) (*s3.Client, error) {
	creds, err := getBucketCredentials(bucket)
	if err != nil {
		return nil, fmt.Errorf("cannot get credentials for bucket %s: %w", bucket, err)
	}

	// Determine endpoint and whether to disable HTTPS
	endpoint := utils.Garage.GetS3Endpoint()
	disableHTTPS := !strings.HasPrefix(endpoint, "https://")

	// AWS config without BaseEndpoint
	awsConfig := aws.Config{
		Credentials: creds,
		Region:      utils.Garage.GetS3Region(),
	}

	// Build S3 client with custom endpoint resolver for proper signing
	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		o.UsePathStyle = true
		o.EndpointOptions.DisableHTTPS = disableHTTPS
		o.EndpointResolver = s3.EndpointResolverFunc(func(region string, opts s3.EndpointResolverOptions) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:           endpoint,
				SigningRegion: utils.Garage.GetS3Region(),
			}, nil
		})
	})

	return client, nil
}
```

The credentials come from `getBucketCredentials` (lines 287-326), which asks the
Garage admin API for a key with read+write on the bucket and caches it for an
hour. **This is the fact that makes the design interesting**: a presigned URL
generated here is signed with a key that has *write* access, and its validity is
tied to that key's continued existence.

### Excerpt 3 — the S3 endpoint the signature is bound to

`backend/utils/garage.go:60-75`:

```go
func (g *garage) GetS3Endpoint() string {
	endpoint := os.Getenv("S3_ENDPOINT_URL")
	if len(endpoint) > 0 {
		return endpoint
	}

	host := strings.Split(g.Config.RPCPublicAddr, ":")[0]
	port := LastString(strings.Split(g.Config.S3API.APIBindAddr, ":"))

	endpoint = fmt.Sprintf("%s:%s", host, port)
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = fmt.Sprintf("http://%s", endpoint)
	}

	return endpoint
}
```

Critical for this design: in the documented Docker Compose setup,
`S3_ENDPOINT_URL` is `http://garage:3900` — a **container-internal** hostname.
A presigned URL built against that endpoint is useless outside the Docker
network, and the signature covers the host, so it cannot be rewritten
client-side. This is open question 3 and it may be the deciding constraint.

### Repo conventions to match

- **Go handlers**: methods on empty structs, `utils.ResponseSuccess` /
  `utils.ResponseError`, registered in `backend/router/router.go`.
- **Frontend data**: TanStack Query v5, hooks in a sibling `hooks.ts`,
  mutations via `useMutation`.
- **No new dependencies** for the prototype. `s3.NewPresignClient` is part of
  the already-vendored `github.com/aws/aws-sdk-go-v2/service/s3`.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Go build | `cd backend && go build ./...` | exit 0 |
| Go vet | `cd backend && go vet ./...` | exit 0, no output |
| Go run (spike) | `cd backend && go run main.go` | server starts on :3909 |
| Frontend dev | `pnpm run dev:client` | Vite dev server starts |
| Typecheck | `pnpm run typecheck` | exit 0 |

A running Garage instance is **required** for this spike — you cannot evaluate
presigned URLs without one. `docker-compose.yml` in the repo root brings one up
(apply `plans/008-docker-compose-and-docs-fixes.md`'s port fix first, or note
the discrepancy). If you cannot get a Garage instance running, see STOP
conditions.

## Scope

**In scope** — a throwaway prototype branch plus a written design:

- `plans/design/009-presigned-share-links.md` (create — the deliverable)
- Prototype code on a scratch branch, **not merged**: a `GET /browse/{bucket}/{key...}?share=1`
  handler variant and a modified share dialog, enough to answer the open
  questions.

**Do not build past here:**

- Do not merge the prototype.
- Do not add a link-revocation store, an audit log, or a share-management page.
  If the design concludes those are needed, that is a finding for the design
  doc — and a much larger project.
- Do not change `getBucketCredentials`' caching or permission model. If the
  design needs a read-only key, say so in the doc; do not implement it.

**Out of scope entirely:**

- `plans/006-object-key-url-encoding.md` overlaps this file's neighborhood. If
  006 has landed, `object.url` is percent-encoded and the share dialog's URL
  construction may look different from Excerpt 1. **Presigned URLs are encoded
  by the AWS SDK itself** — do not apply 006's `encodeObjectPath` to them, and
  say so explicitly in your design doc so a future implementer does not
  double-encode.
- Public/anonymous bucket policies. Garage's website access is a different
  mechanism and this plan does not replace it — see open question 5.

## Git workflow

- Branch: `advisor/009-spike-presigned-share-links`
- Commit the prototype freely on that branch; it is not going to be merged.
- The **only** file intended for merge is the design document.
- Do NOT open a PR for the prototype code.

## Steps

### Step 1: Stand up Garage and confirm the current behavior

Bring up a Garage instance, create a bucket **without** website access, put an
object in it, and open the share dialog in the UI.

Record: what URL the dialog produces, and what happens when you open it.
Expected: a URL that returns an error or nothing, plus the warning banner. This
is your baseline — the design doc needs it.

**Verify**: you have a screenshot or transcript of the failing share.

### Step 2: Prototype the presign endpoint

On your scratch branch, add a minimal handler. Sketch:

```go
func (b *Browse) ShareObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	client, err := getS3Client(bucket)
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	presign := s3.NewPresignClient(client)
	req, err := presign.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		utils.ResponseError(w, err)
		return
	}

	utils.ResponseSuccess(w, map[string]string{"url": req.URL})
}
```

Register it and hit it directly with `curl`. Then open the returned URL in a
browser and in `curl`.

**Verify**: you have observed, first-hand, whether the presigned URL fetches the
object. Record the exact URL shape (host, query parameters, expiry encoding).

### Step 3: Answer the open questions with evidence

Each of these needs an answer grounded in what you observed, not what you
expect. Write the evidence down as you go.

**Q1 — Does Garage honor AWS SigV4 presigned GET URLs at all?**
Garage aims for S3 compatibility but presigned URL support is the kind of thing
that varies. Test it. If the answer is no, the whole feature is blocked and the
design doc should say so in one paragraph and stop. Record the Garage version
you tested (`docker-compose.yml` pins `dxflrs/garage:v2.0.0`).

**Q2 — What expiry should links have, and who chooses?**
Options: a fixed server-side constant; a small set of choices in the dialog
(15 min / 1 hour / 24 hours / 7 days); operator-configurable via env var. Note
that SigV4 caps presigned URL validity at 7 days — confirm that cap applies
here. Recommend one and say why.

**Q3 — Does the signed endpoint resolve for the person receiving the link?**
This is the most likely blocker. See Excerpt 3: in the documented Compose setup
`S3_ENDPOINT_URL` is `http://garage:3900`, an internal hostname, and the
signature covers the host so it cannot be rewritten. Determine:
  - what URL the presign actually produces in that setup,
  - whether a separate "public S3 endpoint" configuration value is needed,
  - whether the UI must warn when the configured endpoint is not externally
    reachable, and whether it can even detect that.

An honest answer here may be "this feature requires a new required config value,"
which is a real cost the maintainer should weigh.

**Q4 — What are the consequences of signing with a read+write key?**
`getBucketCredentials` selects a key with **both** read and write
(`browse.go:307-309`). A presigned GET is read-only regardless of the key's
powers, so the link itself cannot write. But: the link's validity dies with the
key, and anyone who can trigger link generation is using a privileged key.
Determine whether Garage can mint a read-only key the UI could prefer, and what
that would cost. Do not implement it.

**Q5 — Does this replace the website-access URL or sit beside it?**
The two have different properties: website URLs are permanent, human-readable,
and require a public bucket; presigned URLs are temporary, opaque, and work on
private buckets. Recommend whether the dialog offers both (with the current
warning intact for the website option) or leads with presigned. Sketch the
dialog layout either way.

**Q6 — Should link issuance be recorded?**
Nothing in this codebase logs user actions today. Issuing a share link is the
first action where "who gave this out, and when" plausibly matters. Recommend
yes/no, and if yes, note that there is no logging infrastructure to hang it on —
that is a prerequisite project, not a line of code.

### Step 4: Prototype the dialog change

Modify `share-dialog.tsx` on the scratch branch to call the new endpoint and
display the returned URL. Keep it crude — the point is to see the interaction,
not to ship it.

Pay attention to and record:

- The URL is long and opaque. Does the existing `<Input>` + copy button
  (`share-dialog.tsx:58-70`) handle it acceptably?
- Generation is now async and can fail. Where does the error go — the existing
  `Alert`, a `toast` via `handleError`, or inline?
- The dialog currently computes its URL synchronously from props. Making it a
  mutation changes when it fires: on open, or on a button press? Recommend one.

**Verify**: you can open the dialog, get a working link, and paste it into a
private-window browser tab that has no session cookie. **That last check is the
whole point** — it proves the link works for a recipient who is not logged in.

### Step 5: Write the design document

Create `plans/design/009-presigned-share-links.md` with these sections:

1. **Verdict** — one of: *recommended, build it*; *recommended with
   prerequisites* (list them); *not viable* (say why). Lead with this.
2. **What was prototyped** — branch name, files touched, how to reproduce.
3. **Evidence** — the answer to each of Q1-Q6 with what you observed.
4. **Proposed API** — the endpoint shape, request/response, error cases.
5. **Proposed UI** — the dialog, with the both-or-replace decision made.
6. **Configuration impact** — any new env vars, and whether they are required
   or optional. Flag clearly if the feature cannot work without new required
   config.
7. **Security notes** — expiry policy, key selection, what a leaked link grants
   and for how long, whether revocation is possible (it is not, without a
   store — say so plainly).
8. **Effort estimate for the real build** — S/M/L with a breakdown, informed by
   what the prototype actually took.
9. **Open questions you could not resolve** — with what it would take to
   resolve them.

Be willing to write "not viable" or "not worth it." A spike that concludes
against building is a successful spike.

**Verify**: `test -f plans/design/009-presigned-share-links.md` → exit 0, and
the file has all nine sections.

### Step 6: Clean up

Confirm the working tree contains only the design document:

```bash
git status --short
```

Expected: `plans/design/009-presigned-share-links.md` (new) and
`plans/README.md` (modified). The prototype stays on its scratch branch,
unmerged. If prototype changes leaked into the branch you are delivering,
revert them.

## Test plan

No tests. This plan produces a document and a throwaway prototype.

The prototype's "test" is step 4's final check: a working link opened in a
browser with no session. If that works, the mechanism is proven; if it does not,
you have found the blocker, which is equally valuable.

## Done criteria

ALL must hold:

- [ ] `plans/design/009-presigned-share-links.md` exists and contains all nine
      sections from step 5
- [ ] The verdict is stated in the first section, unhedged
- [ ] Q1-Q6 each have an answer backed by an observation, not a prediction
- [ ] The prototype branch exists and its name is recorded in the design doc
- [ ] `git status --short` on the delivery branch shows only the design doc and
      `plans/README.md`
- [ ] `cd backend && go build ./...` exits 0 on the delivery branch (i.e. no
      prototype code leaked in)
- [ ] `plans/README.md` status row for 009 updated

## STOP conditions

Stop and report back (do not improvise) if:

- You cannot get a Garage instance running. This spike cannot be done from code
  reading alone — every open question needs observation. Report the blocker
  rather than writing a speculative design document.
- Q1's answer is no: Garage does not honor presigned GET URLs. Write a
  one-paragraph "not viable" design doc with the evidence and stop. Do not
  attempt a workaround (proxying the fetch through this server with a
  server-side token is a *different* feature with a different security model —
  propose it as a follow-up finding if you think it is worth it, but do not
  build it here).
- Q3's answer requires a new **required** configuration value. That is a
  breaking change for existing deployments. Document it prominently as a
  prerequisite and let the maintainer decide; do not design around it silently.
- You find yourself building link revocation, a share-management page, or an
  audit store. Those are past the "do not build past here" boundary. Note them
  as findings.

## Maintenance notes

- **This plan produces a decision, not a feature.** The design doc is the
  artifact. If the verdict is "build it," the follow-up implementation gets its
  own numbered plan, written against the design.
- **The endpoint-reachability problem (Q3) is the crux.** If you read only one
  section of the finished design doc, read that one — it determines whether this
  is a small feature or one that changes how the app is configured.
- **Do not let the prototype linger on a branch pretending to be a feature.**
  Either it becomes a plan or the branch gets deleted. Record which in the
  design doc.
