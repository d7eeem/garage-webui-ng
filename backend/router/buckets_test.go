package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/d7eeem/garage-webui-ng/schema"
)

// settleWindow is a deliberate pause between "we've seen the expected number
// of concurrent arrivals" and "snapshot peak concurrency", giving any
// EXTRA (bound-violating) arrivals a chance to land before the assertion
// fires. Do not remove this: with an over-wide semaphore, those extra
// arrivals are still in flight (racing the scheduler, not gated by
// anything) and only reliably show up if we wait for them. A longer window
// only makes the test a stricter, more reliable guard against a widened
// bound — it never makes the correct-bound case flaky, because once
// maxBucketInfoConcurrency holders are all blocked on <-release, the real
// semaphore in GetAll guarantees nothing else can start until one of them
// finishes, which cannot happen until this test closes release itself.
const settleWindow = 50 * time.Millisecond

// TestBucketInfoConcurrencyIsBounded drives the real Buckets.GetAll handler
// (backend/router/buckets.go) against a fake admin API, rather than
// reimplementing the bounded-semaphore pattern in the test body — that
// reimplementation never called GetAll at all, so it measured 0.0% coverage
// on the handler it was meant to guard.
//
// The fake's GetBucketInfo endpoint blocks every request unconditionally on
// <-release — it never decides for itself when enough requests have
// arrived. Instead this test waits (via a channel, not a sleep loop) until
// exactly maxBucketInfoConcurrency arrivals have been observed, pauses for
// settleWindow so any bound-violating extra arrivals have time to land, and
// only then asserts peak concurrency and releases every blocked handler at
// once. This ordering is what makes the test a real guard rather than a
// race: gating release on "the Nth arrival showed up" (an earlier version
// of this test did that) makes an over-wide semaphore invisible whenever
// the first N handlers happen to finish before goroutines N+1.. arrive — an
// over-wide bound would then never get caught. If GetAll's semaphore were
// removed or its capacity constant misread, this test now fails the peak
// assertion instead of just hoping the timing lines up — and if the real
// bound is somehow never reached at all, it fails via the arrival timeout
// below rather than hanging the suite.
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
	// arrived gets one send per GetBucketInfo arrival, buffered to
	// totalBuckets so a handler's send never blocks regardless of how many
	// requests actually land concurrently (including in the bound-violated
	// case this test exists to catch).
	arrived := make(chan struct{}, totalBuckets)

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
			arrived <- struct{}{}
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

	// GetAll blocks until every one of its goroutines returns, which
	// requires this test to close release first — so it has to run in its
	// own goroutine while the test observes arrivals and snapshots peak.
	done := make(chan struct{})
	go func() {
		(&Buckets{}).GetAll(rec, req)
		close(done)
	}()

	arrivalTimeout := time.NewTimer(5 * time.Second)
	defer arrivalTimeout.Stop()
	for i := 0; i < maxBucketInfoConcurrency; i++ {
		select {
		case <-arrived:
		case <-arrivalTimeout.C:
			t.Fatalf("timed out waiting for %d concurrent GetBucketInfo arrivals (only saw %d) — GetAll may not be bounding concurrency at all", maxBucketInfoConcurrency, i)
		}
	}

	// See the comment on settleWindow: give any bound-violating extra
	// arrivals a chance to land before we snapshot peak.
	settle := time.NewTimer(settleWindow)
	defer settle.Stop()
	<-settle.C

	if got := atomic.LoadInt32(&peak); got != maxBucketInfoConcurrency {
		t.Errorf("peak concurrency = %d, want exactly %d (GetAll's semaphore should make more impossible)", got, maxBucketInfoConcurrency)
	}

	close(release)

	doneTimeout := time.NewTimer(5 * time.Second)
	defer doneTimeout.Stop()
	select {
	case <-done:
	case <-doneTimeout.C:
		t.Fatal("GetAll did not return after release was closed")
	}

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
}
