package utils

import (
	"sync"
	"testing"
	"time"
)

func TestCacheSetThenGet(t *testing.T) {
	InitCacheManager()

	Cache.Set("key", "value", time.Minute)

	got := Cache.Get("key")
	if got != "value" {
		t.Errorf("Cache.Get() = %v, want %v", got, "value")
	}
}

func TestCacheGetMissingKey(t *testing.T) {
	InitCacheManager()

	got := Cache.Get("missing")
	if got != nil {
		t.Errorf("Cache.Get() = %v, want nil", got)
	}
}

func TestCacheGetExpiredEntry(t *testing.T) {
	InitCacheManager()

	Cache.Set("key", "value", -time.Minute)

	if got := Cache.Get("key"); got != nil {
		t.Errorf("Cache.Get() = %v, want nil (expired)", got)
	}

	// Confirms the delete-on-read path: a second Get still returns nil.
	if got := Cache.Get("key"); got != nil {
		t.Errorf("Cache.Get() second call = %v, want nil (expired)", got)
	}
}

func TestCacheConcurrentSetAndGet(t *testing.T) {
	InitCacheManager()

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func(i int) {
			defer wg.Done()
			Cache.Set("concurrent-key", i, time.Minute)
		}(i)

		go func() {
			defer wg.Done()
			Cache.Get("concurrent-key")
		}()
	}

	wg.Wait()
}
