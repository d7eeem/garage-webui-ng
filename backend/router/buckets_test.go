package router

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBucketInfoConcurrencyIsBounded exercises the same bounded-semaphore
// pattern GetAll uses to limit concurrent GetBucketInfo calls: a buffered
// channel of capacity maxBucketInfoConcurrency is filled before each
// goroutine is spawned, and drained when it finishes.
//
// This does not exercise the HTTP handler itself — GetAll calls the
// package-level utils.Garage singleton, which would need a live admin API or
// an httptest fixture wired through it to test end to end. This test instead
// proves the concurrency-limiting pattern: peak concurrent goroutines never
// exceeds the bound, and every task still completes.
func TestBucketInfoConcurrencyIsBounded(t *testing.T) {
	const tasks = 100

	sem := make(chan struct{}, maxBucketInfoConcurrency)
	var wg sync.WaitGroup
	var current int64
	var peak int64
	var completed int64

	for i := 0; i < tasks; i++ {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			n := atomic.AddInt64(&current, 1)
			for {
				p := atomic.LoadInt64(&peak)
				if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
					break
				}
			}

			// Hold the slot briefly so concurrent goroutines actually
			// overlap; without this, tasks may complete so fast that the
			// bound is never really exercised.
			time.Sleep(time.Millisecond)

			atomic.AddInt64(&current, -1)
			atomic.AddInt64(&completed, 1)
		}()
	}

	wg.Wait()

	if completed != tasks {
		t.Errorf("completed tasks = %d, want %d", completed, tasks)
	}
	if peak > maxBucketInfoConcurrency {
		t.Errorf("peak concurrency = %d, want <= %d", peak, maxBucketInfoConcurrency)
	}
	if peak == 0 {
		t.Error("peak concurrency = 0, want > 0 (test never observed any goroutine running)")
	}
}
