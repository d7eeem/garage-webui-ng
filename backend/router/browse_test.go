package router

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

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
