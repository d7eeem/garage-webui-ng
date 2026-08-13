package router

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestIsPDF pins the media-type match objectViewCSP relies on to decide
// whether an inline body gets the allow-scripts relaxation.
func TestIsPDF(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "pdf", contentType: "application/pdf", want: true},
		{name: "pdf with charset param", contentType: "application/pdf; charset=binary", want: true},
		{name: "uppercase is normalised", contentType: "APPLICATION/PDF", want: true},
		{name: "png is not pdf", contentType: "image/png", want: false},
		{name: "empty string fails closed", contentType: "", want: false},
		{name: "lookalike type is not pdf", contentType: "application/x-pdf", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPDF(tt.contentType)
			if got != tt.want {
				t.Errorf("isPDF(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

// TestObjectViewCSP is the regression guard for plan 046: X-Frame-Options and
// this CSP together decide whether a PDF preview renders. A PDF needs
// allow-scripts (its viewer is itself scripted); everything else must not get
// it. Neither branch may ever grant allow-same-origin — combined with
// allow-scripts that would hand a mislabelled body the console's own origin,
// exactly what the sandbox exists to prevent (see plan 043). The
// allow-same-origin assertion is written as an explicit substring check
// rather than an equality test, so it cannot be quietly loosened by editing
// the expected string.
func TestObjectViewCSP(t *testing.T) {
	pdf := objectViewCSP("application/pdf")
	if !strings.Contains(pdf, "allow-scripts") {
		t.Errorf("objectViewCSP(application/pdf) = %q, want it to contain allow-scripts", pdf)
	}
	if !strings.Contains(pdf, "frame-ancestors 'self'") {
		t.Errorf("objectViewCSP(application/pdf) = %q, want it to contain frame-ancestors 'self'", pdf)
	}
	if strings.Contains(pdf, "allow-same-origin") {
		t.Errorf("objectViewCSP(application/pdf) = %q, must never contain allow-same-origin", pdf)
	}

	png := objectViewCSP("image/png")
	if strings.Contains(png, "allow-scripts") {
		t.Errorf("objectViewCSP(image/png) = %q, must not contain allow-scripts", png)
	}
	if !strings.Contains(png, "frame-ancestors 'self'") {
		t.Errorf("objectViewCSP(image/png) = %q, want it to contain frame-ancestors 'self'", png)
	}
	if strings.Contains(png, "allow-same-origin") {
		t.Errorf("objectViewCSP(image/png) = %q, must never contain allow-same-origin", png)
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

func TestSafeZipEntryName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{"plain relative path is untouched", "a/b.txt", "a/b.txt", false},
		{"leading .. segments are dropped", "../../etc/passwd", "etc/passwd", true},
		{"leading slash is dropped", "/etc/passwd", "etc/passwd", true},
		{"interior .. segments are dropped", "a/../../b.txt", "a/b.txt", true},
		{"bare .. sanitises to empty", "..", "", true},
		{"repeated .. sanitises to empty", "../..", "", true},
		{"backslash traversal is normalized and dropped", "..\\..\\evil.exe", "evil.exe", true},
		{"drive letter is dropped", "C:\\Windows\\x.txt", "Windows/x.txt", true},
		{"UNC prefix is dropped", "\\\\server\\share\\x", "server/share/x", true},
		{"dot segment is dropped", "a/./b.txt", "a/b.txt", true},
		{"empty segment from double slash is dropped", "a//b.txt", "a/b.txt", true},
		{"leading dots that are not exactly .. survive intact", "...hidden.txt", "...hidden.txt", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := safeZipEntryName(tc.input)
			if got != tc.want || changed != tc.changed {
				t.Errorf("safeZipEntryName(%q) = (%q, %v), want (%q, %v)", tc.input, got, changed, tc.want, tc.changed)
			}
		})
	}
}

func TestArchiveEntryNames(t *testing.T) {
	t.Run("no returned name can escape the extraction directory", func(t *testing.T) {
		keys := []string{"../../etc/passwd", "/abs/x", "..\\..\\w.exe", "normal/key.txt"}
		names, _ := archiveEntryNames(keys)
		if len(names) != len(keys) {
			t.Fatalf("archiveEntryNames(...) returned %d names, want %d", len(names), len(keys))
		}
		for k, name := range names {
			if strings.Contains(name, "\\") {
				t.Errorf("name for %q contains a backslash: %q", k, name)
			}
			for _, seg := range strings.Split(name, "/") {
				if seg == "" {
					t.Errorf("name for %q has an empty segment: %q", k, name)
				}
				if seg == ".." {
					t.Errorf("name for %q has a %q segment: %q", k, "..", name)
				}
			}
			if strings.HasPrefix(name, "/") {
				t.Errorf("name for %q begins with '/': %q", k, name)
			}
		}
	})

	t.Run("colliding sanitised names get distinct suffixes", func(t *testing.T) {
		// "../x.txt" sanitises to "x.txt" under the discard-only algorithm
		// (only the exact ".." segment is dropped, nothing is resolved
		// against a preceding segment), so this pair genuinely collides.
		keys := []string{"x.txt", "../x.txt"}
		names, _ := archiveEntryNames(keys)
		if names["x.txt"] == names["../x.txt"] {
			t.Fatalf("archiveEntryNames(...) = %v, want distinct names for colliding keys", names)
		}
		if names["../x.txt"] != "x (2).txt" {
			t.Errorf("archiveEntryNames(...)[%q] = %q, want %q", "../x.txt", names["../x.txt"], "x (2).txt")
		}
	})

	t.Run("every key gets a name and no two keys share one", func(t *testing.T) {
		keys := []string{"a.txt", "b.txt", "../a.txt", "sub/a.txt"}
		names, _ := archiveEntryNames(keys)
		if len(names) != len(keys) {
			t.Fatalf("archiveEntryNames(...) returned %d names, want %d", len(names), len(keys))
		}
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			seen[name] = true
		}
		if len(seen) != len(names) {
			t.Errorf("archiveEntryNames(...) = %v, want all names distinct", names)
		}
	})

	t.Run("renamed lists exactly the keys whose name changed", func(t *testing.T) {
		keys := []string{"p/q/a.txt", "p/q/b.txt", "../evil.txt"}
		_, renamed := archiveEntryNames(keys)
		want := map[string]bool{"../evil.txt": true}
		got := make(map[string]bool, len(renamed))
		for _, k := range renamed {
			got[k] = true
		}
		if len(got) != len(want) || !got["../evil.txt"] {
			t.Errorf("archiveEntryNames(...) renamed = %v, want only %v", renamed, want)
		}
	})

	t.Run("ordinary keys are unaffected and renamed is empty", func(t *testing.T) {
		keys := []string{"p/q/a.txt", "p/q/b.txt"}
		names, renamed := archiveEntryNames(keys)
		if names["p/q/a.txt"] != "a.txt" || names["p/q/b.txt"] != "b.txt" {
			t.Errorf("archiveEntryNames(...) = %v, want the common-prefix trim preserved", names)
		}
		if len(renamed) != 0 {
			t.Errorf("archiveEntryNames(...) renamed = %v, want empty", renamed)
		}
	})

	t.Run("a key that sanitises to nothing gets the unnamed placeholder", func(t *testing.T) {
		names, renamed := archiveEntryNames([]string{"../.."})
		if names["../.."] != "unnamed" {
			t.Errorf("archiveEntryNames(...)[%q] = %q, want %q", "../..", names["../.."], "unnamed")
		}
		if len(renamed) != 1 || renamed[0] != "../.." {
			t.Errorf("archiveEntryNames(...) renamed = %v, want [%q]", renamed, "../..")
		}
	})
}

// ---------------------------------------------------------------------------
// s3Fixture: a fake admin API + fake S3 API wired to the real handlers purely
// through environment variables.
//
// The seam is environmental, not structural: getS3Client (browse.go) resolves
// S3_ENDPOINT_URL via utils.Garage.GetS3Endpoint() at CALL time, not at
// construction, and ShareObject resolves S3_PUBLIC_ENDPOINT_URL the same way.
// t.Setenv on both is therefore enough to point every handler at a local
// httptest server — no interface, no package-level function variable, no
// production code change. This is the same trick TestDownloadArchive's
// success subtest already used; this fixture is that code, generalized so
// every handler in this file can reuse it.
//
// The fake S3 server speaks just enough of the real REST/XML wire protocol
// for aws-sdk-go-v2's restxml deserializers to accept it: XML error bodies
// with a <Code> element, RFC1123 Last-Modified headers, S3-shaped
// ListBucketResult/DeleteResult/ListMultipartUploadsResult documents. Getting
// this wrong doesn't fail loudly with an assertion — it fails as an SDK
// deserialization error, so each piece below is shaped against the actual
// aws-sdk-go-v2 v1.106.4 deserializers.go/serializers.go source rather than
// assumed.
type s3Fixture struct {
	t      *testing.T
	bucket string

	adminServer *httptest.Server
	s3Server    *httptest.Server

	mu             sync.Mutex
	objects        map[string]*fixtureObject
	uploads        []fixtureUpload
	failDeleteKeys map[string]bool

	requestsMu sync.Mutex
	requests   []fixtureRequest

	// Hooks let a single test take full control of one S3 operation without
	// forking the whole fixture. Each returns true if it fully handled the
	// request (the default in-memory behavior below is then skipped).
	onListObjectsV2 func(w http.ResponseWriter, r *http.Request) bool
}

type fixtureObject struct {
	body         []byte
	contentType  string
	lastModified time.Time
	etag         string
}

type fixtureUpload struct {
	key       string
	uploadID  string
	initiated time.Time
}

// fixtureRequest is a recorded incoming request to the fake S3 server, for
// tests that need to assert on what the real handler actually sent (e.g. the
// continuation token or max-keys it forwarded).
type fixtureRequest struct {
	Method string
	Path   string
	Query  url.Values
}

// newS3Fixture starts a fake admin server and a fake S3 server, points the
// real handlers at both via t.Setenv, and registers cleanup. bucket must be
// unique to this test: getBucketCredentials caches per-bucket credentials for
// ~1h with no invalidation (backend/router/browse.go), so a shared bucket
// name would let one test's credentials leak into another's and make results
// order-dependent.
func newS3Fixture(t *testing.T, bucket string) *s3Fixture {
	t.Helper()
	utils.InitCacheManager()

	f := &s3Fixture{
		t:              t,
		bucket:         bucket,
		objects:        map[string]*fixtureObject{},
		failDeleteKeys: map[string]bool{},
	}

	const accessKeyID = "AKIATESTFIXTURE"
	f.adminServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/GetBucketInfo"):
			_ = json.NewEncoder(w).Encode(schema.Bucket{
				Keys: []schema.KeyElement{
					{AccessKeyID: accessKeyID, Permissions: schema.Permissions{Read: true, Write: true}},
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
	t.Cleanup(f.adminServer.Close)

	f.s3Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.s3Server.Close)

	t.Setenv("API_BASE_URL", f.adminServer.URL)
	t.Setenv("S3_ENDPOINT_URL", f.s3Server.URL)

	return f
}

// EnableSharing points S3_PUBLIC_ENDPOINT_URL at a fixed, unreachable host
// distinct from the internal S3 endpoint. ShareObject's presign step signs a
// URL locally without dialing it (see the function comment on ShareObject in
// browse.go), so the endpoint never needs to be reachable — but using a
// different host than the internal one lets a test assert that the PUBLIC
// endpoint, not the internal one, was actually used to sign the link.
func (f *s3Fixture) EnableSharing() {
	f.t.Helper()
	f.t.Setenv("S3_PUBLIC_ENDPOINT_URL", "https://public.example.test")
}

// PutTestObject seeds an object directly into the fake store, bypassing
// PutObject, so read-path tests don't depend on the write path also working.
func (f *s3Fixture) PutTestObject(key, body, contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = &fixtureObject{
		body:         []byte(body),
		contentType:  contentType,
		lastModified: time.Now(),
		etag:         `"etag-` + key + `"`,
	}
}

// SeedUpload registers an in-progress multipart upload for
// ListMultipartUploads/AbortMultipartUpload tests.
func (f *s3Fixture) SeedUpload(key, uploadID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, fixtureUpload{key: key, uploadID: uploadID, initiated: time.Now()})
}

// HasObject reports whether key is still present in the fake store —
// deleted keys are removed by the default DeleteObject/DeleteObjects
// handling below.
func (f *s3Fixture) HasObject(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

// ObjectBody returns the stored body for key, or "" if absent.
func (f *s3Fixture) ObjectBody(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if obj, ok := f.objects[key]; ok {
		return string(obj.body)
	}
	return ""
}

// ObjectContentType returns the stored Content-Type for key ("" if absent) —
// the type the SDK actually sent on the wire, letting a test assert on
// resolveUploadContentType's effect end to end rather than as a pure unit.
func (f *s3Fixture) ObjectContentType(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if obj, ok := f.objects[key]; ok {
		return obj.contentType
	}
	return ""
}

// UploadCount reports how many multipart uploads are still registered.
func (f *s3Fixture) UploadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.uploads)
}

// FailDelete makes the fake report key as a per-object delete failure
// instead of deleting it, for DeleteObject/BulkDeleteObjects partial-failure
// tests.
func (f *s3Fixture) FailDelete(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failDeleteKeys[key] = true
}

// Requests returns every request the fake S3 server has recorded so far, in
// arrival order.
func (f *s3Fixture) Requests() []fixtureRequest {
	f.requestsMu.Lock()
	defer f.requestsMu.Unlock()
	return append([]fixtureRequest(nil), f.requests...)
}

func (f *s3Fixture) recordRequest(r *http.Request) {
	f.requestsMu.Lock()
	defer f.requestsMu.Unlock()
	f.requests = append(f.requests, fixtureRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
	})
}

// handle dispatches an incoming request from the real aws-sdk-go-v2 client to
// the matching fake operation, based on the same method+path-style+query
// shape the SDK's restxml serializers actually produce.
func (f *s3Fixture) handle(w http.ResponseWriter, r *http.Request) {
	f.recordRequest(r)

	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	key := ""
	if len(parts) > 1 {
		key = parts[1]
	}
	q := r.URL.Query()

	switch {
	// ListObjectsV2's fixed opPath is "/?list-type=2" (serializers.go:6190).
	case r.Method == http.MethodGet && key == "" && q.Get("list-type") == "2":
		if f.onListObjectsV2 != nil && f.onListObjectsV2(w, r) {
			return
		}
		f.defaultListObjectsV2(w, q)

	// ListMultipartUploads' fixed opPath is "/?uploads" (serializers.go:5888).
	case r.Method == http.MethodGet && key == "" && q.Has("uploads"):
		f.defaultListMultipartUploads(w)

	// DeleteObjects' fixed opPath is "/?delete" (serializers.go:2477).
	case r.Method == http.MethodPost && key == "" && q.Has("delete"):
		f.defaultDeleteObjects(w, r)

	case r.Method == http.MethodPut && key != "":
		f.defaultPutObject(w, r, key)

	case r.Method == http.MethodHead && key != "":
		f.defaultHeadObject(w, key)

	case r.Method == http.MethodGet && key != "":
		f.defaultGetObject(w, key)

	case r.Method == http.MethodDelete && key != "" && q.Has("uploadId"):
		f.defaultAbortMultipartUpload(key, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodDelete && key != "":
		f.defaultDeleteObject(w, key)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// defaultListObjectsV2 answers GET /{bucket}/?list-type=2. Query params it
// understands (awsRestxml_serializeOpHttpBindingsListObjectsV2Input,
// serializers.go:6219): prefix, delimiter, max-keys, continuation-token.
// Pagination is a simplified but real cursor: the continuation token is just
// the last key emitted, which is fine because the SDK never interprets the
// token itself — it only ever echoes back whatever NextContinuationToken this
// fixture handed it on the previous page.
func (f *s3Fixture) defaultListObjectsV2(w http.ResponseWriter, q url.Values) {
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	maxKeys, _ := strconv.Atoi(q.Get("max-keys"))
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	continuationToken := q.Get("continuation-token")

	f.mu.Lock()
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	f.mu.Unlock()
	sort.Strings(keys)

	start := 0
	if continuationToken != "" {
		for i, k := range keys {
			if k == continuationToken {
				start = i + 1
				break
			}
		}
	}

	var contents, commonPrefixes []string
	seenPrefix := map[string]bool{}
	end := start
	for end < len(keys) && len(contents)+len(commonPrefixes) < maxKeys {
		k := keys[end]
		rel := strings.TrimPrefix(k, prefix)
		if delimiter != "" {
			if idx := strings.Index(rel, delimiter); idx >= 0 {
				cp := prefix + rel[:idx+len(delimiter)]
				if !seenPrefix[cp] {
					seenPrefix[cp] = true
					commonPrefixes = append(commonPrefixes, cp)
				}
				end++
				continue
			}
		}
		contents = append(contents, k)
		end++
	}

	truncated := end < len(keys)
	nextToken := ""
	if truncated {
		nextToken = keys[end-1]
	}

	f.writeListObjectsV2(w, contents, commonPrefixes, truncated, nextToken)
}

// writeListObjectsV2 writes a ListBucketResult document shaped for
// awsRestxml_deserializeOpDocumentListObjectsV2Output (deserializers.go:10899):
// Contents/Key/LastModified/Size/ETag, CommonPrefixes/Prefix, IsTruncated,
// NextContinuationToken. Kept separate from defaultListObjectsV2 so a test's
// onListObjectsV2 hook can serve an exact page sequence (e.g. a forced
// two-page split) without reimplementing the XML shape.
func (f *s3Fixture) writeListObjectsV2(w http.ResponseWriter, contents, commonPrefixes []string, truncated bool, nextToken string) {
	f.mu.Lock()
	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	sb.WriteString(fmt.Sprintf("<IsTruncated>%t</IsTruncated>", truncated))
	if nextToken != "" {
		sb.WriteString("<NextContinuationToken>" + xmlEscape(nextToken) + "</NextContinuationToken>")
	}
	for _, k := range contents {
		obj := f.objects[k]
		sb.WriteString("<Contents><Key>" + xmlEscape(k) + "</Key>")
		lm := time.Unix(0, 0)
		size := 0
		etag := ""
		if obj != nil {
			if !obj.lastModified.IsZero() {
				lm = obj.lastModified
			}
			size = len(obj.body)
			etag = obj.etag
		}
		sb.WriteString("<LastModified>" + lm.UTC().Format("2006-01-02T15:04:05.000Z") + "</LastModified>")
		sb.WriteString(fmt.Sprintf("<Size>%d</Size>", size))
		if etag != "" {
			sb.WriteString("<ETag>" + xmlEscape(etag) + "</ETag>")
		}
		sb.WriteString("</Contents>")
	}
	for _, p := range commonPrefixes {
		sb.WriteString("<CommonPrefixes><Prefix>" + xmlEscape(p) + "</Prefix></CommonPrefixes>")
	}
	sb.WriteString(`</ListBucketResult>`)
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, sb.String())
}

// defaultGetObject answers GET /{bucket}/{key}. A missing key returns the XML
// shape awsRestxml_deserializeOpErrorGetObject (deserializers.go:6601)
// matches on: a <Code>NoSuchKey</Code> element, which the SDK turns into
// *types.NoSuchKey — this is what makes isNotFoundErr's GetObject branch
// reachable at all from a fake server.
func (f *s3Fixture) defaultGetObject(w http.ResponseWriter, key string) {
	f.mu.Lock()
	obj, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		f.writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.", key)
		return
	}
	if obj.contentType != "" {
		w.Header().Set("Content-Type", obj.contentType)
	}
	if !obj.lastModified.IsZero() {
		w.Header().Set("Last-Modified", obj.lastModified.UTC().Format(http.TimeFormat))
	}
	if obj.etag != "" {
		w.Header().Set("ETag", obj.etag)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.body)
}

// defaultHeadObject answers HEAD /{bucket}/{key}. A HEAD response has no
// body, so a missing key is a bare 404: GetErrorResponseComponents
// (service/internal/s3shared/xml_utils.go) derives the error code from the
// status text itself ("Not Found" -> "NotFound") whenever the body carries
// neither a Code nor a Message — exactly what makes isNotFoundErr's
// HeadObject branch (case "NotFound") reachable.
func (f *s3Fixture) defaultHeadObject(w http.ResponseWriter, key string) {
	f.mu.Lock()
	obj, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if obj.contentType != "" {
		w.Header().Set("Content-Type", obj.contentType)
	}
	if !obj.lastModified.IsZero() {
		w.Header().Set("Last-Modified", obj.lastModified.UTC().Format(http.TimeFormat))
	}
	if obj.etag != "" {
		w.Header().Set("ETag", obj.etag)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
	w.WriteHeader(http.StatusOK)
}

// defaultPutObject answers PUT /{bucket}/{key}, storing whatever Content-Type
// the SDK actually sent — what lets a test assert on
// resolveUploadContentType's effect end to end, not just as a unit.
func (f *s3Fixture) defaultPutObject(w http.ResponseWriter, r *http.Request, key string) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.objects[key] = &fixtureObject{
		body:         body,
		contentType:  r.Header.Get("Content-Type"),
		lastModified: time.Now(),
		etag:         `"put-etag"`,
	}
	f.mu.Unlock()
	w.Header().Set("ETag", `"put-etag"`)
	w.WriteHeader(http.StatusOK)
}

// defaultDeleteObject answers DELETE /{bucket}/{key} (single-object delete).
func (f *s3Fixture) defaultDeleteObject(w http.ResponseWriter, key string) {
	f.mu.Lock()
	delete(f.objects, key)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// deleteObjectsXML is just enough of DeleteObjectsInput's request body
// (<Delete><Object><Key>...</Key></Object>...</Delete>) to recover which
// keys were asked for.
type deleteObjectsXML struct {
	XMLName xml.Name `xml:"Delete"`
	Objects []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}

// defaultDeleteObjects answers POST /{bucket}/?delete. Any key registered via
// FailDelete comes back as a per-object <Error> instead of <Deleted>, shaped
// for awsRestxml_deserializeOpDocumentDeleteObjectsOutput
// (deserializers.go:2952) — this is how the BulkDeleteObjects/DeleteObject
// partial-failure tests are driven.
func (f *s3Fixture) defaultDeleteObjects(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var parsed deleteObjectsXML
	_ = xml.Unmarshal(body, &parsed)

	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString("<DeleteResult>")
	f.mu.Lock()
	for _, o := range parsed.Objects {
		if f.failDeleteKeys[o.Key] {
			sb.WriteString("<Error><Key>" + xmlEscape(o.Key) + "</Key><Code>AccessDenied</Code><Message>test-induced failure</Message></Error>")
			continue
		}
		delete(f.objects, o.Key)
		sb.WriteString("<Deleted><Key>" + xmlEscape(o.Key) + "</Key></Deleted>")
	}
	f.mu.Unlock()
	sb.WriteString("</DeleteResult>")

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, sb.String())
}

// defaultListMultipartUploads answers GET /{bucket}/?uploads, shaped for
// awsRestxml_deserializeOpDocumentListMultipartUploadsOutput
// (deserializers.go:10037): one <Upload> per seeded upload.
func (f *s3Fixture) defaultListMultipartUploads(w http.ResponseWriter) {
	f.mu.Lock()
	uploads := append([]fixtureUpload(nil), f.uploads...)
	f.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString("<ListMultipartUploadsResult>")
	for _, u := range uploads {
		sb.WriteString("<Upload><Key>" + xmlEscape(u.key) + "</Key><UploadId>" + xmlEscape(u.uploadID) + "</UploadId><Initiated>" + u.initiated.UTC().Format("2006-01-02T15:04:05.000Z") + "</Initiated></Upload>")
	}
	sb.WriteString("</ListMultipartUploadsResult>")

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, sb.String())
}

// defaultAbortMultipartUpload removes a single seeded upload; the caller
// (handle) writes the 204 response — AbortMultipartUploadOutput carries no
// body.
func (f *s3Fixture) defaultAbortMultipartUpload(key, uploadID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.uploads[:0]
	for _, u := range f.uploads {
		if u.key == key && u.uploadID == uploadID {
			continue
		}
		kept = append(kept, u)
	}
	f.uploads = kept
}

// writeS3Error writes an XML error body shaped for
// GetUnwrappedErrorResponseComponents (service/internal/s3shared/xml_utils.go):
// a <Code> element as a direct child of the root, which is all the SDK's
// error deserializers actually look for regardless of the root element's own
// name.
func (f *s3Fixture) writeS3Error(w http.ResponseWriter, status int, code, message, key string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header+"<Error><Code>"+xmlEscape(code)+"</Code><Message>"+xmlEscape(message)+"</Message><Key>"+xmlEscape(key)+"</Key></Error>")
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
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
		const bucket = "archive-success-bucket"

		f := newS3Fixture(t, bucket)
		f.PutTestObject("p/q/a.txt", "content-a", "application/octet-stream")
		f.PutTestObject("p/q/b.txt", "content-b", "application/octet-stream")

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

// ---------------------------------------------------------------------------
// Handler-level tests for the S3 data plane (plan 055). None of these
// handlers consult utils.Session, so unlike the download-token/archive tests
// above, they don't need withDownloadSession — a plain httptest.Request with
// SetPathValue is enough.

func newGetObjectsRequest(bucket string, query url.Values) *http.Request {
	target := "/browse/" + bucket
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("bucket", bucket)
	return req
}

func TestGetObjects(t *testing.T) {
	t.Run("lists objects under a prefix with the common prefix stripped", func(t *testing.T) {
		f := newS3Fixture(t, "getobjects-prefix-bucket")
		f.PutTestObject("photos/img1.jpg", "one", "image/jpeg")
		f.PutTestObject("photos/img2.jpg", "two", "image/jpeg")
		f.PutTestObject("photos/sub/img3.jpg", "three", "image/jpeg")

		req := newGetObjectsRequest(f.bucket, url.Values{"prefix": {"photos/"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetObjects(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var got schema.BrowseObjectResult
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}

		gotKeys := map[string]bool{}
		for _, o := range got.Objects {
			if o.ObjectKey != nil {
				gotKeys[*o.ObjectKey] = true
			}
		}
		if !gotKeys["img1.jpg"] || !gotKeys["img2.jpg"] {
			t.Errorf("Objects = %v, want img1.jpg and img2.jpg with the prefix stripped", gotKeys)
		}
		if gotKeys["photos/img1.jpg"] {
			t.Errorf("Objects contains the raw key %q, want the prefix stripped", "photos/img1.jpg")
		}

		found := false
		for _, p := range got.Prefixes {
			if p == "photos/sub/" {
				found = true
			}
		}
		if !found {
			t.Errorf("Prefixes = %v, want it to contain %q", got.Prefixes, "photos/sub/")
		}
	})

	t.Run("continuation token round-trips through the fake's continuation-token query param", func(t *testing.T) {
		f := newS3Fixture(t, "getobjects-continuation-bucket")
		f.PutTestObject("a.txt", "a", "text/plain")
		f.PutTestObject("b.txt", "b", "text/plain")
		f.PutTestObject("c.txt", "c", "text/plain")

		req := newGetObjectsRequest(f.bucket, url.Values{"limit": {"2"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetObjects(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("first call: status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var first schema.BrowseObjectResult
		if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
			t.Fatalf("cannot decode first response: %v", err)
		}
		if first.NextToken == nil || *first.NextToken == "" {
			t.Fatalf("first response NextToken = %v, want non-empty (2 of 3 objects should truncate)", first.NextToken)
		}

		req2 := newGetObjectsRequest(f.bucket, url.Values{"limit": {"2"}, "next": {*first.NextToken}})
		rec2 := httptest.NewRecorder()
		(&Browse{}).GetObjects(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("second call: status = %d, want 200, body=%s", rec2.Code, rec2.Body.String())
		}

		var listCalls []fixtureRequest
		for _, r := range f.Requests() {
			if r.Query.Get("list-type") == "2" {
				listCalls = append(listCalls, r)
			}
		}
		if len(listCalls) != 2 {
			t.Fatalf("fake saw %d ListObjectsV2 calls, want 2", len(listCalls))
		}
		if got := listCalls[1].Query.Get("continuation-token"); got != *first.NextToken {
			t.Errorf("second call's continuation-token = %q, want %q (the first response's NextToken)", got, *first.NextToken)
		}
	})

	t.Run("an out-of-range limit is clamped before reaching the fake", func(t *testing.T) {
		f := newS3Fixture(t, "getobjects-limit-bucket")
		f.PutTestObject("a.txt", "a", "text/plain")

		req := newGetObjectsRequest(f.bucket, url.Values{"limit": {"5000"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetObjects(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}

		reqs := f.Requests()
		if len(reqs) != 1 {
			t.Fatalf("fake saw %d requests, want 1", len(reqs))
		}
		if got := reqs[0].Query.Get("max-keys"); got != "1000" {
			t.Errorf("max-keys received by the fake = %q, want %q (normalizeListLimit's cap)", got, "1000")
		}
	})
}

func newGetOneObjectRequest(bucket, key string, query url.Values) *http.Request {
	target := "/browse/" + bucket + "/" + key
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("bucket", bucket)
	req.SetPathValue("key", key)
	return req
}

func TestGetOneObject(t *testing.T) {
	t.Run("no view/dl/thumb returns metadata as JSON, not a body", func(t *testing.T) {
		f := newS3Fixture(t, "getone-head-bucket")
		f.PutTestObject("doc.txt", "hello", "text/plain")

		req := newGetOneObjectRequest(f.bucket, "doc.txt", nil)
		rec := httptest.NewRecorder()
		(&Browse{}).GetOneObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if !strings.Contains(rec.Body.String(), `"ETag"`) {
			t.Errorf("body = %s, want it to contain object metadata (ETag)", rec.Body.String())
		}
	})

	t.Run("view=1 on an inline-safe type returns the body with no Content-Disposition", func(t *testing.T) {
		f := newS3Fixture(t, "getone-view-safe-bucket")
		f.PutTestObject("photo.png", "pngbytes", "image/png")

		req := newGetOneObjectRequest(f.bucket, "photo.png", url.Values{"view": {"1"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetOneObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); cd != "" {
			t.Errorf("Content-Disposition = %q, want empty for an inline-safe type", cd)
		}
		if rec.Body.String() != "pngbytes" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "pngbytes")
		}
	})

	t.Run("view=1 on a type not on the allowlist is downgraded to octet-stream with an attachment header", func(t *testing.T) {
		f := newS3Fixture(t, "getone-view-unsafe-bucket")
		f.PutTestObject("page.html", "<script>evil()</script>", "text/html")

		req := newGetOneObjectRequest(f.bucket, "page.html", url.Values{"view": {"1"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetOneObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("Content-Type = %q, want application/octet-stream", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("Content-Disposition = %q, want it to contain attachment", cd)
		}
	})

	t.Run("dl=1 always sets an attachment Content-Disposition", func(t *testing.T) {
		f := newS3Fixture(t, "getone-dl-bucket")
		f.PutTestObject("photo.png", "pngbytes", "image/png")

		req := newGetOneObjectRequest(f.bucket, "photo.png", url.Values{"dl": {"1"}})
		rec := httptest.NewRecorder()
		(&Browse{}).GetOneObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("Content-Disposition = %q, want it to contain attachment even for an inline-safe type", cd)
		}
	})

	t.Run("a missing object is a 404, not a 500", func(t *testing.T) {
		f := newS3Fixture(t, "getone-missing-bucket")

		req := newGetOneObjectRequest(f.bucket, "nope.txt", nil)
		rec := httptest.NewRecorder()
		(&Browse{}).GetOneObject(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
		}
	})
}

func newPutObjectRequest(t *testing.T, bucket, key string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "upload.bin")
	if err != nil {
		t.Fatalf("cannot create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("cannot write form file content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("cannot close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/browse/"+bucket+"/"+key, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.ContentLength = int64(buf.Len())
	req.SetPathValue("bucket", bucket)
	req.SetPathValue("key", key)
	return req
}

func TestPutObject(t *testing.T) {
	t.Run("a normal upload succeeds and the fake receives the expected key and body", func(t *testing.T) {
		f := newS3Fixture(t, "putobject-normal-bucket")

		req := newPutObjectRequest(t, f.bucket, "uploads/report.txt", []byte("hello world"))
		rec := httptest.NewRecorder()
		(&Browse{}).PutObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if got := f.ObjectBody("uploads/report.txt"); got != "hello world" {
			t.Errorf("fake stored body = %q, want %q", got, "hello world")
		}
	})

	t.Run("an upload larger than MAX_UPLOAD_SIZE_MB is rejected with 413 before touching S3", func(t *testing.T) {
		f := newS3Fixture(t, "putobject-toolarge-bucket")
		t.Setenv("MAX_UPLOAD_SIZE_MB", "1")

		req := newPutObjectRequest(t, f.bucket, "big.bin", []byte("small body, but ContentLength lies"))
		req.ContentLength = 2 << 20 // 2 MiB, over the 1 MiB cap set above

		rec := httptest.NewRecorder()
		(&Browse{}).PutObject(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413, body=%s", rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			t.Error("response body is empty, want a readable error message")
		}
		if len(f.Requests()) != 0 {
			t.Errorf("fake S3 saw %d requests, want 0 (the 413 must be answered before any S3 call)", len(f.Requests()))
		}
	})

	t.Run("an empty or generic content-type is resolved from the key's extension", func(t *testing.T) {
		f := newS3Fixture(t, "putobject-contenttype-bucket")

		req := newPutObjectRequest(t, f.bucket, "assets/logo.svg", []byte("<svg/>"))
		rec := httptest.NewRecorder()
		(&Browse{}).PutObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if len(f.Requests()) != 1 {
			t.Fatalf("fake saw %d requests, want 1", len(f.Requests()))
		}
		// The fake's default PutObject handler stores whatever Content-Type
		// header the SDK actually sent, so this proves the resolution happened
		// end to end, not just in the unit-level TestResolveUploadContentType.
		if ct := f.ObjectContentType("assets/logo.svg"); ct != "image/svg+xml" {
			t.Errorf("stored Content-Type = %q, want %q", ct, "image/svg+xml")
		}
	})
}

func newDeleteObjectRequest(bucket, key string, recursive bool) *http.Request {
	target := "/browse/" + bucket + "/" + key
	if recursive {
		target += "?recursive=true"
	}
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	req.SetPathValue("bucket", bucket)
	req.SetPathValue("key", key)
	return req
}

func TestDeleteObject(t *testing.T) {
	t.Run("a single-object delete calls the fake once with that key", func(t *testing.T) {
		f := newS3Fixture(t, "deleteobject-single-bucket")
		f.PutTestObject("solo.txt", "x", "text/plain")

		req := newDeleteObjectRequest(f.bucket, "solo.txt", false)
		rec := httptest.NewRecorder()
		(&Browse{}).DeleteObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if f.HasObject("solo.txt") {
			t.Error("solo.txt is still present in the fake, want it deleted")
		}

		var deleteCalls int
		for _, r := range f.Requests() {
			if r.Method == http.MethodDelete {
				deleteCalls++
			}
		}
		if deleteCalls != 1 {
			t.Errorf("fake saw %d DELETE calls, want 1", deleteCalls)
		}
	})

	t.Run("a recursive delete spanning two ListObjectsV2 pages deletes every key from both pages", func(t *testing.T) {
		f := newS3Fixture(t, "deleteobject-recursive-bucket")
		f.PutTestObject("dir/a.txt", "a", "text/plain")
		f.PutTestObject("dir/b.txt", "b", "text/plain")

		var listCalls int32
		f.onListObjectsV2 = func(w http.ResponseWriter, r *http.Request) bool {
			n := atomic.AddInt32(&listCalls, 1)
			if n == 1 {
				f.writeListObjectsV2(w, []string{"dir/a.txt"}, nil, true, "page-2")
			} else {
				f.writeListObjectsV2(w, []string{"dir/b.txt"}, nil, false, "")
			}
			return true
		}

		req := newDeleteObjectRequest(f.bucket, "dir/", true)
		rec := httptest.NewRecorder()
		(&Browse{}).DeleteObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if got := atomic.LoadInt32(&listCalls); got != 2 {
			t.Fatalf("fake saw %d ListObjectsV2 calls, want 2 (the pagination loop must follow NextContinuationToken)", got)
		}
		if f.HasObject("dir/a.txt") || f.HasObject("dir/b.txt") {
			t.Errorf("recursive delete left objects behind: dir/a.txt present=%v dir/b.txt present=%v", f.HasObject("dir/a.txt"), f.HasObject("dir/b.txt"))
		}

		var got struct {
			Deleted int `json:"deleted"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}
		if got.Deleted != 2 {
			t.Errorf("deleted = %d, want 2", got.Deleted)
		}
	})
}

func TestBulkDeleteObjects(t *testing.T) {
	t.Run("a partial failure names the failed key and still reports the successes", func(t *testing.T) {
		f := newS3Fixture(t, "bulkdelete-partial-bucket")
		f.PutTestObject("ok.txt", "x", "text/plain")
		f.PutTestObject("bad.txt", "y", "text/plain")
		f.FailDelete("bad.txt")

		body, _ := json.Marshal(map[string]any{"action": "delete", "keys": []string{"ok.txt", "bad.txt"}})
		req := httptest.NewRequest(http.MethodPost, "/browse/"+f.bucket, bytes.NewReader(body))
		req.SetPathValue("bucket", f.bucket)
		rec := httptest.NewRecorder()
		(&Browse{}).BulkDeleteObjects(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}

		var got struct {
			Deleted int                 `json:"deleted"`
			Errors  []map[string]string `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}
		if got.Deleted != 1 {
			t.Errorf("deleted = %d, want 1", got.Deleted)
		}
		if len(got.Errors) != 1 || got.Errors[0]["key"] != "bad.txt" {
			t.Errorf("errors = %v, want exactly one entry naming bad.txt", got.Errors)
		}
		if f.HasObject("ok.txt") {
			t.Error("ok.txt is still present, want it deleted")
		}
		if !f.HasObject("bad.txt") {
			t.Error("bad.txt was deleted despite the induced failure, want it to remain")
		}
	})
}

func TestListMultipartUploads(t *testing.T) {
	f := newS3Fixture(t, "listmultipart-bucket")
	f.SeedUpload("big/video.mp4", "upload-1")
	f.SeedUpload("big/other.mp4", "upload-2")

	req := httptest.NewRequest(http.MethodGet, "/multipart/"+f.bucket, nil)
	req.SetPathValue("bucket", f.bucket)
	rec := httptest.NewRecorder()
	(&Browse{}).ListMultipartUploads(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got struct {
		Uploads []struct {
			Key      string `json:"key"`
			UploadID string `json:"uploadId"`
		} `json:"uploads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}
	if len(got.Uploads) != 2 {
		t.Fatalf("uploads = %v, want 2 entries", got.Uploads)
	}
	seen := map[string]bool{}
	for _, u := range got.Uploads {
		seen[u.Key+"|"+u.UploadID] = true
	}
	if !seen["big/video.mp4|upload-1"] || !seen["big/other.mp4|upload-2"] {
		t.Errorf("uploads = %v, want both seeded uploads present", got.Uploads)
	}
}

func TestAbortMultipartUpload(t *testing.T) {
	t.Run("aborts a single upload", func(t *testing.T) {
		f := newS3Fixture(t, "abortmultipart-single-bucket")
		f.SeedUpload("big/video.mp4", "upload-1")

		req := httptest.NewRequest(http.MethodDelete, "/multipart/"+f.bucket+"?key=big%2Fvideo.mp4&uploadId=upload-1", nil)
		req.SetPathValue("bucket", f.bucket)
		rec := httptest.NewRecorder()
		(&Browse{}).AbortMultipartUpload(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if f.UploadCount() != 0 {
			t.Errorf("upload count = %d, want 0", f.UploadCount())
		}
	})

	t.Run("aborts every upload when all=true", func(t *testing.T) {
		f := newS3Fixture(t, "abortmultipart-all-bucket")
		f.SeedUpload("a.mp4", "upload-1")
		f.SeedUpload("b.mp4", "upload-2")

		req := httptest.NewRequest(http.MethodDelete, "/multipart/"+f.bucket+"?all=true", nil)
		req.SetPathValue("bucket", f.bucket)
		rec := httptest.NewRecorder()
		(&Browse{}).AbortMultipartUpload(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		if f.UploadCount() != 0 {
			t.Errorf("upload count = %d, want 0", f.UploadCount())
		}

		var got struct {
			Aborted int `json:"aborted"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}
		if got.Aborted != 2 {
			t.Errorf("aborted = %d, want 2", got.Aborted)
		}
	})
}

func newShareObjectRequest(bucket, key string, query url.Values) *http.Request {
	target := "/share/" + bucket + "/" + key
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("bucket", bucket)
	req.SetPathValue("key", key)
	return req
}

func TestShareObject(t *testing.T) {
	t.Run("returns a presigned URL from the public endpoint, with the default expiry", func(t *testing.T) {
		f := newS3Fixture(t, "share-default-bucket")
		f.EnableSharing()

		req := newShareObjectRequest(f.bucket, "photo.png", nil)
		rec := httptest.NewRecorder()
		(&Browse{}).ShareObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var got struct {
			URL            string `json:"url"`
			ExpiresSeconds int    `json:"expiresSeconds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}
		if got.ExpiresSeconds != 3600 {
			t.Errorf("expiresSeconds = %d, want 3600 (the default)", got.ExpiresSeconds)
		}
		if !strings.Contains(got.URL, "public.example.test") {
			t.Errorf("url = %q, want it signed against the public endpoint (public.example.test), not the internal S3 endpoint", got.URL)
		}
		// PresignGetObject signs locally and never dials the endpoint, so no
		// request should have reached the fake S3 server.
		if len(f.Requests()) != 0 {
			t.Errorf("fake S3 saw %d requests, want 0 (presigning must be local)", len(f.Requests()))
		}
	})

	t.Run("an expiry above the 7-day ceiling is clamped", func(t *testing.T) {
		f := newS3Fixture(t, "share-clamp-bucket")
		f.EnableSharing()

		req := newShareObjectRequest(f.bucket, "photo.png", url.Values{"expires": {"999999999"}})
		rec := httptest.NewRecorder()
		(&Browse{}).ShareObject(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var got struct {
			ExpiresSeconds int `json:"expiresSeconds"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("cannot decode response: %v", err)
		}
		if got.ExpiresSeconds != 604800 {
			t.Errorf("expiresSeconds = %d, want 604800 (the 7-day SigV4 ceiling)", got.ExpiresSeconds)
		}
	})

	t.Run("returns a clear error, not a panic, when sharing is not configured", func(t *testing.T) {
		f := newS3Fixture(t, "share-disabled-bucket")
		t.Setenv("S3_PUBLIC_ENDPOINT_URL", "")

		req := newShareObjectRequest(f.bucket, "photo.png", nil)
		rec := httptest.NewRecorder()
		(&Browse{}).ShareObject(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Errorf("status = %d, want 501, body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "sharing is not enabled") {
			t.Errorf("body = %q, want it to mention sharing is not enabled", rec.Body.String())
		}
	})
}
