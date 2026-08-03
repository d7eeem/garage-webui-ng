package router

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
