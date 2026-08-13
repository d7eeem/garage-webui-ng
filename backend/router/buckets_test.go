package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/d7eeem/garage-webui-ng/schema"
)

// TestBucketInfoConcurrencyIsBounded drives the real Buckets.GetAll handler
// (backend/router/buckets.go) against a fake admin API, rather than
// reimplementing the bounded-semaphore pattern in the test body — that
// reimplementation never called GetAll at all, so it measured 0.0% coverage
// on the handler it was meant to guard.
//
// The fake's GetBucketInfo endpoint blocks every request until exactly
// maxBucketInfoConcurrency of them are in flight simultaneously, then
// releases them all at once via a closed channel. This proves the semaphore
// in GetAll actually bounds concurrency at the HTTP transport level —
// deterministically, with no artificial delay: the gate itself forces the overlap
// instead of hoping goroutines race each other inside a sleep window. If
// GetAll's semaphore were removed or its capacity constant misread, this
// test would hang until the overall `go test` timeout rather than pass by
// accident.
func TestBucketInfoConcurrencyIsBounded(t *testing.T) {
	const totalBuckets = 20
	// A bucket whose GetBucketInfo fails. Chosen outside the first
	// maxBucketInfoConcurrency arrivals so it doesn't change how the release
	// gate below is reached.
	const failIndex = totalBuckets - 1

	ids := make([]string, totalBuckets)
	aliases := make([][]string, totalBuckets)
	for i := range ids {
		ids[i] = fmt.Sprintf("bucket-%02d", i)
		aliases[i] = []string{fmt.Sprintf("alias-%02d", i)}
	}

	var inFlight int32
	var peak int32
	release := make(chan struct{})
	var releaseOnce sync.Once

	adminServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/ListBuckets"):
			res := make([]schema.GetBucketsRes, totalBuckets)
			for i := range res {
				res[i] = schema.GetBucketsRes{ID: ids[i], GlobalAliases: aliases[i]}
			}
			_ = json.NewEncoder(w).Encode(res)

		case strings.HasPrefix(r.URL.Path, "/v2/GetBucketInfo"):
			n := atomic.AddInt32(&inFlight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			if n == maxBucketInfoConcurrency {
				releaseOnce.Do(func() { close(release) })
			}
			<-release
			atomic.AddInt32(&inFlight, -1)

			id := r.URL.Query().Get("id")
			if id == ids[failIndex] {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"induced failure"}`))
				return
			}

			idx, _ := strconv.Atoi(strings.TrimPrefix(id, "bucket-"))
			_ = json.NewEncoder(w).Encode(schema.Bucket{
				ID:            id,
				GlobalAliases: aliases[idx],
				Created:       "created-" + id,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer adminServer.Close()

	t.Setenv("API_BASE_URL", adminServer.URL)

	req := httptest.NewRequest(http.MethodGet, "/buckets", nil)
	rec := httptest.NewRecorder()
	(&Buckets{}).GetAll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var got []schema.Bucket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("cannot decode response: %v", err)
	}

	if len(got) != totalBuckets {
		t.Fatalf("got %d buckets, want %d", len(got), totalBuckets)
	}

	for i, b := range got {
		if b.ID != ids[i] {
			t.Errorf("response[%d].ID = %q, want %q (results must stay in list order)", i, b.ID, ids[i])
		}
	}

	fb := got[failIndex]
	if fb.ID != ids[failIndex] {
		t.Errorf("fallback bucket ID = %q, want %q", fb.ID, ids[failIndex])
	}
	if len(fb.GlobalAliases) != 1 || fb.GlobalAliases[0] != aliases[failIndex][0] {
		t.Errorf("fallback bucket GlobalAliases = %v, want %v", fb.GlobalAliases, aliases[failIndex])
	}
	if fb.Created != "" {
		t.Errorf("fallback bucket Created = %q, want empty (proves this came from the ListBuckets fallback, not a real GetBucketInfo response)", fb.Created)
	}

	if got := atomic.LoadInt32(&peak); got > maxBucketInfoConcurrency {
		t.Errorf("peak concurrency = %d, want <= %d", got, maxBucketInfoConcurrency)
	}
	if got := atomic.LoadInt32(&peak); got != maxBucketInfoConcurrency {
		t.Errorf("peak concurrency = %d, want exactly %d (the release gate should force it)", got, maxBucketInfoConcurrency)
	}
}
