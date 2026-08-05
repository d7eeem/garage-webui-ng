# Design 009: Presigned share links for private buckets

> Spike output. Deliverable is this recommendation, grounded in tests against a
> local Garage v2.0.0 instance (Docker), plus reasoning over the existing code.
> Prototype was a direct SigV4 verification, not a merged branch. Written
> 2026-07-30 against `integration-check` (all 8 fix plans merged).
>
> **Update 2026-07-31**: the two items §9 left open were closed by a follow-up Go
> prototype using the backend's exact SDK path (`s3.NewPresignClient`), run against
> a fresh local Garage with both a read+write and a read-only key. Results folded
> into §2, §5 (Q2/Q4), and §9 below. Verdict unchanged.

## 1. Verdict

**Recommended with one hard prerequisite.** Garage honors AWS SigV4 presigned
GET URLs — verified end to end — so private-bucket sharing is mechanically
sound and roughly one endpoint away from the existing per-bucket S3 client. The
prerequisite: the S3 endpoint used to *sign* the URL must be reachable by the
person receiving the link. In the project's own documented Compose setup it is
not (it's the container-internal `http://garage:3900`), and the signing host
cannot be rewritten after the fact because it is part of the signature. So this
feature cannot ship without a new, deployment-specific configuration value for a
publicly reachable S3 endpoint — and a UI that is honest when that value is
absent.

If the maintainer is willing to introduce that config, build it. If not, it is
not viable as a general feature and should stay a documented non-goal.

## 2. What was verified (evidence, not prediction)

Against a local single-node Garage v2.0.0 in Docker, a `test-bucket` with one
object, and an RWO key:

- **Q1 — Garage serves SigV4 presigned GET.** A presigned URL fetched with **no
  credentials** returned `200` and the correct object body. The *same path with
  the query signature stripped* returned `403` with
  `Garage does not support anonymous access`. So it is the signature that
  authorizes the read, exactly as the feature needs — not a public bucket.
- **Q2 — expiry is enforced.** A URL minted with a 10-minute window
  (`X-Amz-Expires=600`) served at first, then returned `400 InvalidRequest`
  once the window elapsed. Garage rejects expired presigned URLs.
- **Q3 — the host is signed.** Every presigned URL carries
  `X-Amz-SignedHeaders=host`; the request host is covered by the signature. A
  link is therefore bound to the exact scheme+host+port used at signing time and
  cannot be transparently proxied or rewritten to a different public address.

The initial verification used the MinIO client (`docker run minio/mc … share
download`). **A follow-up (2026-07-31) then ran the backend's exact code path** —
a Go program replicating `getS3Client` (path-style, custom endpoint resolver,
static creds, region `garage`) with `aws-sdk-go-v2/service/s3 v1.59.0`, calling
`s3.NewPresignClient(...).PresignGetObject(...)`:

- **Go-SDK GET presign → HTTP 200**, returned the private object body. The exact
  backend approach works; no longer inferred from `mc`.
- **Expiry boundary is precise**: 15 min / 1 h / 24 h / **7 days all served 200**;
  **8 days → HTTP 400**. The SigV4 7-day maximum is enforced by Garage (§5 Q2).
- The SDK percent-encodes the key inside the URL (`report #3 (final).pdf` →
  `report%20%233%20%28final%29.pdf`), so presigned URLs must **not** be passed
  through plan 006's `encodeObjectPath`/`browseObjectURL` — that double-encodes.

## 3. How it would be built

The backend already has everything but the endpoint:

- `getS3Client(bucket)` (`backend/router/browse.go:328`) builds an authenticated,
  per-bucket `*s3.Client` from credentials fetched via the admin API and cached
  for an hour. **After plan 004, its failure path is clean** (it no longer caches
  empty credentials), which matters here — a presign endpoint would reuse it.
- `github.com/aws/aws-sdk-go-v2/service/s3` (already in `go.mod`) provides
  `s3.NewPresignClient(client).PresignGetObject(ctx, input, s3.WithPresignExpires(d))`.

Proposed endpoint (a sibling of the existing browse routes in
`backend/router/router.go`):

```
POST /api/browse/{bucket}/{key...}?share=1   ->  { "url": "<presigned>", "expiresAt": "<rfc3339>" }
```

Handler sketch (mirrors the existing handlers' shape):

```go
client, err := getS3Client(bucket)          // reuse — do not re-implement
presign := s3.NewPresignClient(client)
req, err := presign.PresignGetObject(r.Context(), &s3.GetObjectInput{
    Bucket: aws.String(bucket), Key: aws.String(key),
}, s3.WithPresignExpires(ttl))
utils.ResponseSuccess(w, map[string]string{"url": req.URL})
```

**The one change beyond this**: the presign must sign against a *public* S3
endpoint (see §4), not `GetS3Endpoint()`'s internal value.

## 4. The crux — endpoint reachability (Q3)

`getS3Client` signs against `utils.Garage.GetS3Endpoint()`
(`backend/router/browse.go:335`), which returns `S3_ENDPOINT_URL` or a value
derived from `rpc_public_addr` (`backend/utils/garage.go:60-75`). In the
README's Compose example that is `http://garage:3900` — a name that only
resolves *inside the Docker network*. A link signed against it is useless to an
external recipient, and because the host is signed (§2, Q3) the server cannot
hand out a link with a different public host without re-signing against that
host.

Consequences for the design:

- A new config value is required, e.g. `S3_PUBLIC_ENDPOINT_URL`, used **only**
  for presigning. When unset, the feature must be disabled (or clearly marked
  unavailable in the UI) rather than minting links that 404 for the recipient.
- The signer builds a second S3 client (or overrides the endpoint resolver) using
  the public endpoint for `PresignGetObject`, while normal browse traffic keeps
  using the internal endpoint. The AWS SDK signs whatever endpoint the client is
  configured with, so this is a per-call client option, not a protocol problem.
- The UI cannot reliably detect from the browser whether an endpoint is
  externally reachable, so "is this deployable" is an operator assertion (they
  set `S3_PUBLIC_ENDPOINT_URL`), not something the app can verify. The honest UI
  is: no public endpoint configured → no presigned-share option shown.

This is the finding that turns "one endpoint away" into "one endpoint plus a
config contract." It should be surfaced prominently to whoever decides to build.

## 5. Remaining questions, answered

- **Q2 expiry policy** — offer a small fixed set in the dialog (15 min / 1 h /
  24 h / 7 d). SigV4 caps validity at 7 days; do not offer longer. Recommend a
  conservative default (1 h). Server should clamp to ≤ 7 d regardless of client.
- **Q4 key selection** — `getBucketCredentials` selects a key with **read+write+
  owner** (`browse.go:307`). A presigned GET is read-only regardless of the
  key's powers, so the *link* cannot write. Two real consequences: (a) the link's
  validity dies if that key is rotated or loses access; (b) anyone who can hit
  the share endpoint is leveraging a privileged key. **Now verified (2026-07-31):
  Garage supports read-only keys (`bucket allow --read`), and a read-only key
  presigns a fully working GET (HTTP 200).** So signing with least privilege is
  viable — prefer a read-only key where one exists. Not a blocker either way (the
  emitted capability is read-only regardless), but it is cheap defense-in-depth
  against the signing key's secret leaking through another channel.
- **Q5 dialog UX** — the current share dialog
  (`src/pages/buckets/manage/browse/share-dialog.tsx`) builds a bare website URL
  and warns it only works with website access enabled. Recommend: keep the
  website URL for public buckets, and add a "Create share link" action that calls
  the presign endpoint and shows the returned URL + its expiry. Lead with the
  presigned option for private buckets. The existing `<Input>` + copy button
  handles a long opaque URL acceptably. Generation is now async and can fail — route
  errors to the existing `handleError`/toast.
- **Q6 issuance logging** — nothing in the app logs user actions today. A share
  link is the first action where "who handed this out, and when" plausibly
  matters. Recommend: log issuance server-side (bucket, key prefix, expiry, no
  full URL) if/when any request logging exists; do **not** build a logging
  subsystem just for this. Note it as a deferred concern, not a blocker.

## 6. Configuration impact

- **New, required for the feature**: `S3_PUBLIC_ENDPOINT_URL` (or similar).
  Optional overall — when unset, the share-link feature is simply unavailable and
  the rest of the app is unaffected. This is the one and only new deployment knob.
- No change to existing env vars. No breaking change to current deployments.

## 7. Security notes

- A presigned link is a **bearer capability**: anyone with the URL can read the
  object until it expires. There is **no revocation** short of rotating the
  signing key (which breaks all outstanding links for that bucket). State this in
  the UI next to the expiry.
- Keep expiries short by default; the 7-day SigV4 ceiling is the max, not a
  recommendation.
- Signing with an RWO key means link lifetime is coupled to a privileged key's
  lifecycle — see Q4.
- The endpoint must require an authenticated UI session (it sits behind the
  existing `AuthMiddleware`), so only logged-in operators can mint links.

## 8. Effort estimate for the real build

- Backend endpoint + public-endpoint client + config plumbing: **S–M** (~half a
  day). The reuse of `getS3Client` and the SDK's `PresignGetObject` keeps it small;
  the public-endpoint client is the only genuinely new code.
- Frontend dialog changes: **S** (a mutation hook + a button + expiry select).
- Docs (new env var, the "links need a public S3 endpoint" caveat): **S**.
- Total: **M**, gated on the maintainer accepting the new config contract.

## 9. Open questions

- ~~**Read-only key minting** (Q4)~~ — **RESOLVED 2026-07-31.** Garage creates
  read-only keys and they presign a working GET (HTTP 200). The presign endpoint
  can prefer a read-only key; `getBucketCredentials` would need to select one when
  available. Still optional (link is read-only regardless), now proven feasible.
- ~~**Go-SDK parity**~~ — **RESOLVED 2026-07-31.** A Go prototype using
  `s3.NewPresignClient` against local Garage returned HTTP 200; the exact backend
  code path is confirmed, not inferred.
- **The one decision that remains**: will the maintainer accept the new
  `S3_PUBLIC_ENDPOINT_URL` config contract (§4/§6)? This gates the whole feature
  and is a product call, not a technical unknown.
- **Expiry-vs-key-rotation UX**: if links silently die on key rotation, should the
  UI warn? Depends on how often keys rotate in practice.

## Reproduction

Local Garage used for this spike (Docker, `127.0.0.1` only):

```bash
# admin token in scratchpad/garage-local/admin_token, key in .../key.txt
docker run --rm --network container:garage-local --entrypoint sh minio/mc -c \
  "mc alias set g http://127.0.0.1:3900 <KEY> <SECRET> && \
   mc share download --expire 2h g/test-bucket/secret.txt"
# then fetch the printed Share: URL with no auth -> 200; strip the query -> 403
```
