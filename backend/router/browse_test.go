package router

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/d7eeem/garage-webui-ng/schema"
	"github.com/d7eeem/garage-webui-ng/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// TestPathValueDecodesWildcard confirms the load-bearing assumption behind
// this file's URL-encoding helpers: Go's net/http ServeMux (1.22+) decodes
// the {key...} wildcard before handlers see it via r.PathValue. If this ever
// stops being true, browseObjectURL and encodeObjectPath must be redesigned
// to decode explicitly instead of relying on ServeMux to do it.
func TestPathValueDecodesWildcard(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("GET /browse/{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("key")
	})

	req := httptest.NewRequest("GET", "/browse/b/dir/report%20%233.pdf", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if want := "dir/report #3.pdf"; got != want {
		t.Errorf("PathValue(key) = %q, want %q", got, want)
	}
}

func TestNormalizeListLimit(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int32
	}{
		{name: "empty", raw: "", want: 100},
		{name: "non-numeric", raw: "abc", want: 100},
		{name: "zero", raw: "0", want: 100},
		{name: "negative", raw: "-5", want: 100},
		{name: "within range", raw: "50", want: 50},
		{name: "exactly at cap", raw: "1000", want: 1000},
		{name: "above cap", raw: "5000", want: 1000},
		{name: "int32 overflow", raw: "99999999999", want: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeListLimit(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeListLimit(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// TestIsInlineSafe pins the allowlist that decides whether a stored content
// type may be rendered inline on the console's own origin. This is a security
// boundary (see the comment on inlineSafeContentTypes in browse.go): anyone
// with S3 write access to a bucket chooses an object's content type, so
// HTML-ish types must always come back false, and a parse failure must fail
// closed rather than open.
func TestIsInlineSafe(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "png", contentType: "image/png", want: true},
		{name: "plain text with charset param", contentType: "text/plain; charset=utf-8", want: true},
		{name: "uppercase is normalised", contentType: "TEXT/PLAIN", want: true},
		{name: "html is never inline-safe", contentType: "text/html", want: false},
		{name: "svg is never inline-safe", contentType: "image/svg+xml", want: false},
		{name: "xhtml is never inline-safe", contentType: "application/xhtml+xml", want: false},
		{name: "javascript is never inline-safe", contentType: "application/javascript", want: false},
		{name: "empty string fails closed", contentType: "", want: false},
		{name: "malformed type fails closed", contentType: "not/a/valid/type", want: false},
		{name: "generic octet-stream is not on the allowlist", contentType: "application/octet-stream", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInlineSafe(tt.contentType)
			if got != tt.want {
				t.Errorf("isInlineSafe(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

// fakeAPIError is a minimal smithy.APIError implementation for exercising
// isNotFoundErr against an error code the SDK's concrete s3/types package
// does not model (e.g. "AccessDenied" has no matching struct there).
type fakeAPIError struct{ code string }

func (e fakeAPIError) Error() string                 { return e.code }
func (e fakeAPIError) ErrorCode() string             { return e.code }
func (e fakeAPIError) ErrorMessage() string          { return "" }
func (e fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestIsNotFoundErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "HeadObject miss (NotFound)", err: &types.NotFound{}, want: true},
		{name: "GetObject miss (NoSuchKey)", err: &types.NoSuchKey{}, want: true},
		{name: "unrelated API error code", err: fakeAPIError{code: "AccessDenied"}, want: false},
		{name: "plain non-API error", err: errors.New("boom"), want: false},
		{name: "nil error", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundErr(tt.err)
			if got != tt.want {
				t.Errorf("isNotFoundErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestMaxUploadBytes(t *testing.T) {
	const mib = int64(1) << 20
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "unset falls back to the default", raw: "", want: defaultMaxUploadBytes},
		{name: "non-numeric falls back", raw: "abc", want: defaultMaxUploadBytes},
		{name: "zero falls back rather than disabling the cap", raw: "0", want: defaultMaxUploadBytes},
		{name: "negative falls back", raw: "-10", want: defaultMaxUploadBytes},
		{name: "megabytes are converted to bytes", raw: "100", want: 100 * mib},
		{name: "one megabyte", raw: "1", want: mib},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MAX_UPLOAD_SIZE_MB", tt.raw)
			got := maxUploadBytes()
			if got != tt.want {
				t.Errorf("maxUploadBytes() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResolveUploadContentType exercises the extension-based fallback added
// because a browser leaves a multipart part's Content-Type empty (serialized
// as "" or the generic "application/octet-stream") whenever File.type is
// empty — which happens for any extension the OS's local mime database does
// not know. The frontend's `mime/lite` has the same gap for .ico specifically
// (verified separately), which is why this is resolved server-side instead.
func TestResolveUploadContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		key         string
		want        string
	}{
		{name: "empty content-type resolves svg", contentType: "", key: "dashboard/homepage.svg", want: "image/svg+xml"},
		{name: "generic octet-stream resolves webp", contentType: "application/octet-stream", key: "photo.webp", want: "image/webp"},
		{name: "empty content-type resolves avif", contentType: "", key: "photo.avif", want: "image/avif"},
		{name: "empty content-type resolves ico", contentType: "", key: "favicon.ico", want: "image/vnd.microsoft.icon"},
		{name: "empty content-type resolves png", contentType: "", key: "logo.png", want: "image/png"},
		{name: "unresolvable extension keeps the incoming generic type", contentType: "application/octet-stream", key: "data.unknownext", want: "application/octet-stream"},
		{name: "unresolvable extension keeps an empty incoming type as empty", contentType: "", key: "data.unknownext", want: ""},
		{name: "non-empty, non-generic content-type is preserved unchanged", contentType: "text/x-custom", key: "file.svg", want: "text/x-custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUploadContentType(tt.contentType, tt.key)
			if got != tt.want {
				t.Errorf("resolveUploadContentType(%q, %q) = %q, want %q", tt.contentType, tt.key, got, tt.want)
			}
		})
	}
}

// makeObjectIdentifiers builds n distinct ObjectIdentifier values, keyed
// "key-0", "key-1", ... "key-{n-1}", so ordering can be asserted.
func makeObjectIdentifiers(n int) []types.ObjectIdentifier {
	keys := make([]types.ObjectIdentifier, 0, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("key-%d", i)
		keys = append(keys, types.ObjectIdentifier{Key: &k})
	}
	return keys
}

func flattenBatches(batches [][]types.ObjectIdentifier) []types.ObjectIdentifier {
	var flat []types.ObjectIdentifier
	for _, batch := range batches {
		flat = append(flat, batch...)
	}
	return flat
}

func TestChunkObjectIdentifiers(t *testing.T) {
	t.Run("nil input produces no batches", func(t *testing.T) {
		batches := chunkObjectIdentifiers(nil, 1000)
		if len(batches) != 0 {
			t.Errorf("chunkObjectIdentifiers(nil, 1000) = %d batches, want 0", len(batches))
		}
	})

	t.Run("single key fits in one batch", func(t *testing.T) {
		keys := makeObjectIdentifiers(1)
		batches := chunkObjectIdentifiers(keys, 1000)
		if len(batches) != 1 {
			t.Fatalf("got %d batches, want 1", len(batches))
		}
		if len(batches[0]) != 1 {
			t.Errorf("batch 0 size = %d, want 1", len(batches[0]))
		}
	})

	t.Run("exactly one cap's worth fits in one batch", func(t *testing.T) {
		keys := makeObjectIdentifiers(1000)
		batches := chunkObjectIdentifiers(keys, 1000)
		if len(batches) != 1 {
			t.Fatalf("got %d batches, want 1", len(batches))
		}
		if len(batches[0]) != 1000 {
			t.Errorf("batch 0 size = %d, want 1000", len(batches[0]))
		}
	})

	t.Run("one over the cap splits into two batches", func(t *testing.T) {
		keys := makeObjectIdentifiers(1001)
		batches := chunkObjectIdentifiers(keys, 1000)
		if len(batches) != 2 {
			t.Fatalf("got %d batches, want 2", len(batches))
		}
		if len(batches[0]) != 1000 {
			t.Errorf("batch 0 size = %d, want 1000", len(batches[0]))
		}
		if len(batches[1]) != 1 {
			t.Errorf("batch 1 size = %d, want 1", len(batches[1]))
		}
	})

	t.Run("two and a half cap's worth splits into three batches", func(t *testing.T) {
		keys := makeObjectIdentifiers(2500)
		batches := chunkObjectIdentifiers(keys, 1000)
		if len(batches) != 3 {
			t.Fatalf("got %d batches, want 3", len(batches))
		}
		wantSizes := []int{1000, 1000, 500}
		for i, want := range wantSizes {
			if len(batches[i]) != want {
				t.Errorf("batch %d size = %d, want %d", i, len(batches[i]), want)
			}
		}
	})

	t.Run("every key appears exactly once, in order", func(t *testing.T) {
		keys := makeObjectIdentifiers(2500)
		batches := chunkObjectIdentifiers(keys, 1000)
		flat := flattenBatches(batches)

		if len(flat) != len(keys) {
			t.Fatalf("flattened length = %d, want %d", len(flat), len(keys))
		}
		for i := range keys {
			if *flat[i].Key != *keys[i].Key {
				t.Errorf("flattened[%d] = %q, want %q", i, *flat[i].Key, *keys[i].Key)
			}
		}
	})
}

// TestDeleteErrorsToList proves the Q4 fix: every per-object delete error is
// reported, not just the first (the bug this plan removed was truncating the
// list to res.Errors[0]).
func TestDeleteErrorsToList(t *testing.T) {
	t.Run("nil input produces an empty, non-nil slice", func(t *testing.T) {
		got := deleteErrorsToList(nil)
		if got == nil {
			t.Fatal("deleteErrorsToList(nil) = nil, want non-nil empty slice")
		}
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})

	t.Run("reports ALL errors, not just the first", func(t *testing.T) {
		errs := []types.Error{
			{Key: aws.String("a"), Message: aws.String("denied")},
			{Key: aws.String("b"), Message: aws.String("gone")},
		}
		got := deleteErrorsToList(errs)

		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0]["key"] != "a" || got[0]["message"] != "denied" {
			t.Errorf("got[0] = %v, want key=a message=denied", got[0])
		}
		if got[1]["key"] != "b" || got[1]["message"] != "gone" {
			t.Errorf("got[1] = %v, want key=b message=gone", got[1])
		}
	})
}

// TestDeleteResponseErrorsSerializesAsEmptyArray guards a regression found
// in live testing: both DeleteObject's recursive branch and
// BulkDeleteObjects accumulate failures into a `failed` slice that starts
// empty and is only ever grown via `append(failed, deleteErrorsToList(...)...)`.
// A nil []map[string]string marshals to JSON `null`; only a non-nil empty
// slice marshals to `[]`. The frontend (bulk-actions.tsx) calls
// data.errors.map(...) / data.errors.length unconditionally, so a `null`
// response crashes the success handler on every all-succeeded delete — the
// most common case. This test pins the fix (`failed := []map[string]string{}`)
// by exercising the exact same construction the handlers use — declare,
// then append zero results from deleteErrorsToList — and asserting the
// marshaled bytes, not just the in-memory value.
func TestDeleteResponseErrorsSerializesAsEmptyArray(t *testing.T) {
	t.Run("no failures across any batch marshals errors as [], not null", func(t *testing.T) {
		failed := []map[string]string{}
		failed = append(failed, deleteErrorsToList(nil)...)
		failed = append(failed, deleteErrorsToList([]types.Error{})...)

		body, err := json.Marshal(map[string]any{"deleted": 3, "errors": failed})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		got := string(body)
		if !strings.Contains(got, `"errors":[]`) {
			t.Errorf("marshaled body = %s, want it to contain %q", got, `"errors":[]`)
		}
		if strings.Contains(got, `"errors":null`) {
			t.Errorf("marshaled body = %s, must not contain %q", got, `"errors":null`)
		}
	})

	t.Run("regression check: the old nil-declaration pattern would have produced null", func(t *testing.T) {
		// This documents *why* the fix matters by exercising the buggy
		// pattern the review caught (var failed []map[string]string, never
		// reassigned when there's nothing to append) — kept as a negative
		// control, not as code to reintroduce.
		var failed []map[string]string
		failed = append(failed, deleteErrorsToList(nil)...)

		body, err := json.Marshal(map[string]any{"deleted": 3, "errors": failed})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		got := string(body)
		if !strings.Contains(got, `"errors":null`) {
			t.Fatalf("expected the nil-slice pattern to still marshal to null (sanity check on Go's own behavior); got %s — if this ever changes, the bug this test guards against may no longer be possible", got)
		}
	})
}

// browseMuxKey routes an already-encoded /browse/{bucket}/{key...} path
// through a mux matching the real route pattern (see router.go) and returns
// the decoded key, mirroring the setup in TestPathValueDecodesWildcard. Used
// by TestBrowseObjectURL to prove the encode/decode round trip.
func browseMuxKey(t *testing.T, path string) string {
	t.Helper()
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("GET /browse/{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("key")
	})

	req := httptest.NewRequest("GET", path, nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestBrowseObjectURL(t *testing.T) {
	tests := []struct {
		name   string
		bucket string
		key    string
		want   string
	}{
		{name: "simple file", bucket: "b", key: "file.txt", want: "/browse/b/file.txt"},
		{name: "nested path keeps the slash separator literal", bucket: "b", key: "dir/file.txt", want: "/browse/b/dir/file.txt"},
		{name: "space and hash", bucket: "b", key: "report #3.pdf", want: "/browse/b/report%20%233.pdf"},
		{name: "question mark", bucket: "b", key: "a?b.txt", want: "/browse/b/a%3Fb.txt"},
		{name: "literal percent", bucket: "b", key: "100%.txt", want: "/browse/b/100%25.txt"},
		// url.PathEscape leaves '+' literal in a path segment (it is only
		// special in query strings), confirmed against the real function
		// rather than assumed.
		{name: "plus sign stays literal in a path segment", bucket: "b", key: "a+b.txt", want: "/browse/b/a+b.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := browseObjectURL(tt.bucket, tt.key)
			if got != tt.want {
				t.Errorf("browseObjectURL(%q, %q) = %q, want %q", tt.bucket, tt.key, got, tt.want)
			}

			// Round trip: the encoded URL, run through the real route
			// pattern, must decode back to the original key. This is the
			// assertion that actually proves the fix — it doesn't matter
			// exactly which bytes browseObjectURL produces as long as the
			// server's own mux recovers the original key from them.
			decoded := browseMuxKey(t, tt.want)
			if decoded != tt.key {
				t.Errorf("round trip: PathValue(key) for %q = %q, want %q", tt.want, decoded, tt.key)
			}
		})
	}
}

func TestContentDispositionAttachment(t *testing.T) {
	t.Run("simple filename", func(t *testing.T) {
		got := contentDispositionAttachment("file.txt")
		if !strings.Contains(got, "attachment") || !strings.Contains(got, "file.txt") {
			t.Errorf("contentDispositionAttachment(%q) = %q, want it to contain %q and %q", "file.txt", got, "attachment", "file.txt")
		}
	})

	t.Run("space forces a quoted filename", func(t *testing.T) {
		got := contentDispositionAttachment("my report.pdf")
		if !strings.Contains(got, `"my report.pdf"`) {
			t.Errorf(`contentDispositionAttachment("my report.pdf") = %q, want it to contain %q`, got, `"my report.pdf"`)
		}
	})

	t.Run("embedded quote round-trips through ParseMediaType", func(t *testing.T) {
		in := `a"b.txt`
		got := contentDispositionAttachment(in)

		_, params, err := mime.ParseMediaType(got)
		if err != nil {
			t.Fatalf("contentDispositionAttachment(%q) = %q, which failed to parse: %v", in, got, err)
		}
		if params["filename"] != in {
			t.Errorf("contentDispositionAttachment(%q) = %q, parsed filename = %q, want %q", in, got, params["filename"], in)
		}
	})

	t.Run("non-ASCII filename produces a parseable non-empty header", func(t *testing.T) {
		in := "résumé.pdf"
		got := contentDispositionAttachment(in)

		if got == "" || !strings.HasPrefix(got, "attachment") {
			t.Errorf("contentDispositionAttachment(%q) = %q, want a non-empty value starting with %q", in, got, "attachment")
		}

		// Go emits the RFC 2231 filename*=utf-8'' form here. The exact
		// encoding is intentionally not pinned (see plan 006's notes on
		// brittleness), but it must still parse and round-trip to the
		// original filename.
		_, params, err := mime.ParseMediaType(got)
		if err != nil {
			t.Fatalf("contentDispositionAttachment(%q) = %q, which failed to parse: %v", in, got, err)
		}
		if params["filename"] != in {
			t.Errorf("contentDispositionAttachment(%q) parsed filename = %q, want %q", in, params["filename"], in)
		}
	})

	t.Run("invalid UTF-8 still produces a non-empty attachment header", func(t *testing.T) {
		// This is the input contentDispositionAttachment's fallback branch is
		// written to guard against: mime.FormatMediaType is documented to
		// return "" on a standard violation, and a non-UTF-8 filename was the
		// motivating case. Empirically, on the Go stdlib version this repo
		// builds with (verified by reading mime/mediatype.go), FormatMediaType
		// never returns "" for a fixed, valid ("attachment", "filename")
		// pair — it percent-encodes arbitrary byte values via RFC 2231
		// instead — so this input exercises FormatMediaType's own byte-wise
		// encoding, not the `disposition == ""` branch inside
		// contentDispositionAttachment. The manual fallback is kept as cheap
		// defensive coverage in case that stdlib behavior ever changes; this
		// test pins the externally observable contract (non-empty, starts
		// with "attachment") either way.
		in := string([]byte{0xff, 0xfe})
		got := contentDispositionAttachment(in)

		if got == "" || !strings.HasPrefix(got, "attachment") {
			t.Errorf("contentDispositionAttachment(%q) = %q, want a non-empty value starting with %q", in, got, "attachment")
		}
	})
}

// TestArchiveRouteWinsOverWildcard guards the route-collision hazard called
// out in plan 031: GET /browse/{bucket}/{key...} is a wildcard that would
// otherwise swallow GET /browse/{bucket}/archive. Go 1.22's ServeMux prefers
// the more specific pattern regardless of registration order, but this test
// pins that behavior against the real patterns rather than relying on it
// silently.
func TestArchiveRouteWinsOverWildcard(t *testing.T) {
	mux := http.NewServeMux()
	var hitArchive, hitWildcard bool
	mux.HandleFunc("GET /browse/{bucket}/archive", func(w http.ResponseWriter, r *http.Request) {
		hitArchive = true
	})
	mux.HandleFunc("GET /browse/{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		hitWildcard = true
	})

	req := httptest.NewRequest(http.MethodGet, "/browse/b/archive", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if !hitArchive || hitWildcard {
		t.Errorf("GET /browse/b/archive: hitArchive=%v hitWildcard=%v, want the archive route to win", hitArchive, hitWildcard)
	}
}

// TestStripCommonKeyPrefix pins the entry-naming contract for the archive:
// a common leading directory shared by every selected key is stripped so the
// zip doesn't repeat it in every entry name, but keys that diverge earlier
// keep their distinguishing path so entries never collide.
func TestStripCommonKeyPrefix(t *testing.T) {
	t.Run("shared directory is stripped", func(t *testing.T) {
		got := stripCommonKeyPrefix([]string{"p/q/a.txt", "p/q/b.txt"})
		want := map[string]string{"p/q/a.txt": "a.txt", "p/q/b.txt": "b.txt"}
		if len(got) != len(want) || got["p/q/a.txt"] != "a.txt" || got["p/q/b.txt"] != "b.txt" {
			t.Errorf("stripCommonKeyPrefix(...) = %v, want %v", got, want)
		}
	})

	t.Run("keys with no shared directory are left alone", func(t *testing.T) {
		got := stripCommonKeyPrefix([]string{"a/x.txt", "b/y.txt"})
		if got["a/x.txt"] != "a/x.txt" || got["b/y.txt"] != "b/y.txt" {
			t.Errorf("stripCommonKeyPrefix(...) = %v, want keys unchanged", got)
		}
	})

	t.Run("a single key is reduced to its base name", func(t *testing.T) {
		got := stripCommonKeyPrefix([]string{"p/q/a.txt"})
		if got["p/q/a.txt"] != "a.txt" {
			t.Errorf("stripCommonKeyPrefix(...) = %v, want a.txt", got)
		}
	})

	t.Run("partial shared prefix only strips the common directory", func(t *testing.T) {
		got := stripCommonKeyPrefix([]string{"p/q/a.txt", "p/r/b.txt"})
		if got["p/q/a.txt"] != "q/a.txt" || got["p/r/b.txt"] != "r/b.txt" {
			t.Errorf("stripCommonKeyPrefix(...) = %v, want the deeper directories preserved", got)
		}
	})
}

// withDownloadSession seeds an authenticated session (username only — this
// package's handlers under test don't consult "authenticated"/"role") inside
// the scs middleware, then runs fn with a request sharing that context, so fn
// can call Browse's handler methods directly and see the same
// utils.Session.Get("username") the handler reads. Mirrors the pattern in
// TestAuthMiddlewareAdminAPIIsAdminOnly (backend/middleware/auth_test.go).
func withDownloadSession(t *testing.T, username string, fn func(r *http.Request)) {
	t.Helper()
	sessMgr := utils.InitSessionManager()
	handler := sessMgr.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		utils.Session.Set(r, "username", username)
		fn(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
}

// mintDownloadToken calls CreateDownloadToken directly (sharing r's session
// context) and returns the minted token, failing the test if minting itself
// didn't succeed.
func mintDownloadToken(t *testing.T, r *http.Request, bucket string, keys []string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"bucket": bucket, "keys": keys})
	if err != nil {
		t.Fatalf("cannot marshal mint request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/browse/download-token", bytes.NewReader(body)).WithContext(r.Context())
	rec := httptest.NewRecorder()
	(&Browse{}).CreateDownloadToken(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mintDownloadToken: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("mintDownloadToken: cannot decode response %q: %v", rec.Body.String(), err)
	}
	return out.Token
}

// requestArchive calls DownloadArchive directly (sharing r's session
// context) for the given bucket/token and returns the recorder.
func requestArchive(r *http.Request, bucket, token string) *httptest.ResponseRecorder {
	target := "/browse/" + bucket + "/archive"
	if token != "" {
		target += "?token=" + token
	}
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(r.Context())
	req.SetPathValue("bucket", bucket)
	rec := httptest.NewRecorder()
	(&Browse{}).DownloadArchive(rec, req)
	return rec
}

func TestCreateDownloadToken(t *testing.T) {
	t.Run("no keys is rejected", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			body, _ := json.Marshal(map[string]any{"bucket": "b", "keys": []string{}})
			req := httptest.NewRequest(http.MethodPost, "/browse/download-token", bytes.NewReader(body)).WithContext(r.Context())
			rec := httptest.NewRecorder()
			(&Browse{}).CreateDownloadToken(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	})

	t.Run("missing bucket is rejected", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			body, _ := json.Marshal(map[string]any{"bucket": "", "keys": []string{"a"}})
			req := httptest.NewRequest(http.MethodPost, "/browse/download-token", bytes.NewReader(body)).WithContext(r.Context())
			rec := httptest.NewRecorder()
			(&Browse{}).CreateDownloadToken(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	})

	t.Run("too many keys is rejected", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			keys := make([]string, maxListKeys+1)
			for i := range keys {
				keys[i] = fmt.Sprintf("key-%d", i)
			}
			body, _ := json.Marshal(map[string]any{"bucket": "b", "keys": keys})
			req := httptest.NewRequest(http.MethodPost, "/browse/download-token", bytes.NewReader(body)).WithContext(r.Context())
			rec := httptest.NewRecorder()
			(&Browse{}).CreateDownloadToken(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	})

	t.Run("valid request mints a non-empty token", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			token := mintDownloadToken(t, r, "b", []string{"a.txt"})
			if token == "" {
				t.Error("token is empty, want non-empty")
			}
		})
	})
}

// TestDownloadArchive covers the security-critical contract of the archive
// endpoint: a token only authorises the bucket and user it was minted for,
// and can only ever be used once. Only the final subtest needs a real (mock)
// S3 backend — everything else is rejected before the handler ever calls
// getS3Client.
func TestDownloadArchive(t *testing.T) {
	t.Run("missing token is rejected", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			rec := requestArchive(r, "b", "")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	})

	t.Run("unknown token is not found", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			rec := requestArchive(r, "b", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd")
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	})

	t.Run("token minted for a different bucket is forbidden", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			token := mintDownloadToken(t, r, "bucket-a", []string{"x.txt"})
			rec := requestArchive(r, "bucket-b", token)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	})

	t.Run("token minted by a different user is forbidden", func(t *testing.T) {
		utils.InitCacheManager()

		var token string
		withDownloadSession(t, "alice", func(r *http.Request) {
			token = mintDownloadToken(t, r, "bucket-a", []string{"x.txt"})
		})

		withDownloadSession(t, "mallory", func(r *http.Request) {
			rec := requestArchive(r, "bucket-a", token)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	})

	t.Run("a used token cannot be replayed", func(t *testing.T) {
		utils.InitCacheManager()
		withDownloadSession(t, "alice", func(r *http.Request) {
			token := mintDownloadToken(t, r, "bucket-a", []string{"x.txt"})

			// The first use is deliberately a bucket mismatch, so this test
			// needs no S3 fixture — the point being pinned is that the token
			// is consumed on the first successful cache lookup, before the
			// bucket/user check even runs.
			first := requestArchive(r, "bucket-mismatch", token)
			if first.Code != http.StatusForbidden {
				t.Fatalf("first use: status = %d, want 403", first.Code)
			}

			second := requestArchive(r, "bucket-mismatch", token)
			if second.Code != http.StatusNotFound {
				t.Errorf("replay: status = %d, want 404 (token must be single-use)", second.Code)
			}
		})
	})

	t.Run("success streams a zip with common-prefix-stripped entries and the right headers", func(t *testing.T) {
		utils.InitCacheManager()

		const bucket = "archive-success-bucket"
		const accessKeyID = "AKIATESTARCHIVE"

		// Mock admin API: getBucketCredentials calls GetBucketInfo, then
		// GetKeyInfo for the first read+write key it finds.
		adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasPrefix(r.URL.Path, "/v2/GetBucketInfo"):
				_ = json.NewEncoder(w).Encode(schema.Bucket{
					Keys: []schema.KeyElement{
						{
							AccessKeyID: accessKeyID,
							Permissions: schema.Permissions{Read: true, Write: true},
						},
					},
				})
			case strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo"):
				_ = json.NewEncoder(w).Encode(schema.KeyElement{
					AccessKeyID:     accessKeyID,
					SecretAccessKey: "test-secret",
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer adminServer.Close()

		// Mock S3 API (path-style): serves fixed content for the two keys the
		// test selects, keyed on everything after the leading /{bucket}/.
		objects := map[string]string{
			"p/q/a.txt": "content-a",
			"p/q/b.txt": "content-b",
		}
		s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
			if len(parts) != 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			content, ok := objects[parts[1]]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(content))
		}))
		defer s3Server.Close()

		t.Setenv("API_BASE_URL", adminServer.URL)
		t.Setenv("S3_ENDPOINT_URL", s3Server.URL)

		withDownloadSession(t, "alice", func(r *http.Request) {
			token := mintDownloadToken(t, r, bucket, []string{"p/q/a.txt", "p/q/b.txt"})
			rec := requestArchive(r, bucket, token)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/zip")
			}
			cd := rec.Header().Get("Content-Disposition")
			if !strings.Contains(cd, "attachment") {
				t.Errorf("Content-Disposition = %q, want it to contain %q", cd, "attachment")
			}
			if !strings.Contains(cd, bucket) {
				t.Errorf("Content-Disposition = %q, want it to contain the bucket name %q", cd, bucket)
			}

			zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			if err != nil {
				t.Fatalf("cannot read response body as zip: %v", err)
			}

			names := make(map[string]bool, len(zr.File))
			for _, f := range zr.File {
				names[f.Name] = true
			}
			if !names["a.txt"] || !names["b.txt"] {
				t.Errorf("zip entries = %v, want exactly a.txt and b.txt (common prefix p/q/ stripped)", names)
			}
			if names["p/q/a.txt"] || names["p/q/b.txt"] {
				t.Errorf("zip entries = %v, want the common prefix stripped, not the raw keys", names)
			}
		})
	})
}
