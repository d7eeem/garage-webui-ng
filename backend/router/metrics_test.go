package router

import "testing"

// TestParsePromMetrics feeds a small Prometheus text-exposition-format fixture
// through parsePromMetrics and checks the behaviors the handler depends on:
// label sets within a wanted family are summed, unwanted families are
// ignored, a bare `name value` line (no labels) is parsed, comment lines are
// skipped, and every curated key is present in the output even when absent
// from the input (defaulted to 0).
func TestParsePromMetrics(t *testing.T) {
	fixture := `
# HELP api_s3_request_counter Counter of S3 API calls
# TYPE api_s3_request_counter counter
api_s3_request_counter{api="ListObjects"} 10
api_s3_request_counter{api="GetObject"} 25
# HELP api_s3_error_counter Counter of S3 API errors
# TYPE api_s3_error_counter counter
api_s3_error_counter{api="GetObject",code="404"} 3
# HELP some_unwanted_metric A family not in curatedMetrics
# TYPE some_unwanted_metric gauge
some_unwanted_metric{label="x"} 999
block_bytes_read 555
`

	got := parsePromMetrics([]byte(fixture), curatedMetrics)

	if len(got) != len(curatedMetrics) {
		t.Errorf("len(got) = %d, want %d (unwanted family must not leak into the result)", len(got), len(curatedMetrics))
	}

	if _, ok := got["some_unwanted_metric"]; ok {
		t.Error("got contains \"some_unwanted_metric\", want it ignored (not in curatedMetrics)")
	}

	tests := []struct {
		name string
		want float64
	}{
		{"api_s3_request_counter", 35}, // two label sets: 10 + 25
		{"api_s3_error_counter", 3},    // single label set
		{"block_bytes_read", 555},      // bare `name value` line, no labels
		{"block_bytes_written", 0},     // absent from fixture, defaults to 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := got[tt.name]
			if !ok {
				t.Fatalf("got[%q] missing, want present (curated keys must always be present)", tt.name)
			}
			if v != tt.want {
				t.Errorf("got[%q] = %v, want %v", tt.name, v, tt.want)
			}
		})
	}
}

// TestParsePromMetricsEmptyBody confirms an empty (or fully unparsable) body
// still returns every curated key defaulted to 0, rather than an empty map or
// a nil map — the handler always sends a complete, predictable shape.
func TestParsePromMetricsEmptyBody(t *testing.T) {
	got := parsePromMetrics([]byte(""), curatedMetrics)

	if len(got) != len(curatedMetrics) {
		t.Errorf("len(got) = %d, want %d", len(got), len(curatedMetrics))
	}
	for _, name := range curatedMetrics {
		if v, ok := got[name]; !ok || v != 0 {
			t.Errorf("got[%q] = %v, ok=%v; want 0, true", name, v, ok)
		}
	}
}
