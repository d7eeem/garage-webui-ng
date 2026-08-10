package main

import "testing"

func TestVersion(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "default", value: "dev", want: "dev"},
		{name: "injected value returned verbatim", value: "v3.3.0", want: "v3.3.0"},
		{name: "empty falls back to dev", value: "", want: "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := version
			version = tt.value
			t.Cleanup(func() { version = original })

			if got := Version(); got != tt.want {
				t.Errorf("Version() = %q, want %q", got, tt.want)
			}
		})
	}
}
