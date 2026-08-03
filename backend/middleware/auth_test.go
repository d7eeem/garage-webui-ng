package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsViewerAllowed exercises the entire security boundary for the
// read-only viewer role. It must stay fail-closed: any non-GET request that
// isn't POST /auth/logout is denied, and the one GET carve-out
// (GetKeyInfo?showSecretKey=true) must also be denied.
func TestIsViewerAllowed(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		want   bool
	}{
		{
			name:   "read endpoint via GET is allowed",
			method: http.MethodGet,
			target: "/v2/GetClusterStatus",
			want:   true,
		},
		{
			name:   "GetKeyInfo with showSecretKey=true is denied",
			method: http.MethodGet,
			target: "/v2/GetKeyInfo?showSecretKey=true",
			want:   false,
		},
		{
			name:   "GetKeyInfo without showSecretKey is allowed",
			method: http.MethodGet,
			target: "/v2/GetKeyInfo?id=x",
			want:   true,
		},
		{
			name:   "POST mutation is denied",
			method: http.MethodPost,
			target: "/v2/CreateBucket",
			want:   false,
		},
		{
			name:   "POST logout is allowed",
			method: http.MethodPost,
			target: "/auth/logout",
			want:   true,
		},
		{
			name:   "DELETE is denied",
			method: http.MethodDelete,
			target: "/browse/b/k",
			want:   false,
		},
		{
			name:   "PUT is denied",
			method: http.MethodPut,
			target: "/browse/b/k",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.target, nil)
			if got := isViewerAllowed(r); got != tt.want {
				t.Errorf("isViewerAllowed(%s %s) = %v, want %v", tt.method, tt.target, got, tt.want)
			}
		})
	}
}
