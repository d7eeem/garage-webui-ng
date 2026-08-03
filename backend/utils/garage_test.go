package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Garage is a package-level singleton, so these tests mutate shared state.
// Run in the order written; do not add t.Parallel().

func TestLoadConfigReturnsErrorOnMalformedToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garage.toml")

	if err := os.WriteFile(path, []byte("this is not = = valid toml"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	t.Setenv("CONFIG_PATH", path)

	// Before this fix, a parse error called log.Fatal and the test process
	// would exit; after it, the error comes back to the caller.
	if err := Garage.LoadConfig(); err == nil {
		t.Errorf("LoadConfig() = nil error, want non-nil for malformed TOML")
	}
}

func TestLoadConfigReturnsErrorOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.toml")

	t.Setenv("CONFIG_PATH", path)

	if err := Garage.LoadConfig(); err == nil {
		t.Errorf("LoadConfig() = nil error, want non-nil for missing file")
	}
}

func TestLoadConfigParsesValidToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garage.toml")

	toml := "[s3_api]\nroot_domain = \"s3.example.com\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	t.Setenv("CONFIG_PATH", path)

	if err := Garage.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() = %v, want nil error", err)
	}

	if got := Garage.Config.S3API.RootDomain; got != "s3.example.com" {
		t.Errorf("Garage.Config.S3API.RootDomain = %q, want %q", got, "s3.example.com")
	}
}

func TestFetchReturnsStatusCodeForNonJsonErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>bad gateway</html>"))
	}))
	defer server.Close()

	t.Setenv("API_BASE_URL", server.URL)

	_, err := Garage.Fetch("/anything", &FetchOptions{})
	if err == nil {
		t.Fatalf("Fetch() = nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("Fetch() error = %q, want it to contain %q", err.Error(), "502")
	}
}

func TestFetchReturnsApiMessageForJsonErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"bucket not found"}`))
	}))
	defer server.Close()

	t.Setenv("API_BASE_URL", server.URL)

	_, err := Garage.Fetch("/anything", &FetchOptions{})
	if err == nil {
		t.Fatalf("Fetch() = nil error, want non-nil")
	}
	if err.Error() != "bucket not found" {
		t.Errorf("Fetch() error = %q, want %q", err.Error(), "bucket not found")
	}
}

func TestFetchSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	t.Setenv("API_BASE_URL", server.URL)

	body, err := Garage.Fetch("/anything", &FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}
	if ok, present := data["ok"]; !present || ok != true {
		t.Errorf("response body = %v, want map containing ok=true", data)
	}
}

func TestGetS3PublicEndpointFallback(t *testing.T) {
	t.Setenv("S3_ENDPOINT_URL", "http://internal:3900")
	os.Unsetenv("S3_PUBLIC_ENDPOINT_URL")

	if got, want := Garage.GetS3PublicEndpoint(), Garage.GetS3Endpoint(); got != want {
		t.Errorf("GetS3PublicEndpoint() = %q, want %q (fallback to GetS3Endpoint())", got, want)
	}

	t.Setenv("S3_PUBLIC_ENDPOINT_URL", "https://public.example.com")
	if got, want := Garage.GetS3PublicEndpoint(), "https://public.example.com"; got != want {
		t.Errorf("GetS3PublicEndpoint() = %q, want %q", got, want)
	}
}

func TestIsSharingEnabled(t *testing.T) {
	os.Unsetenv("S3_PUBLIC_ENDPOINT_URL")
	if Garage.IsSharingEnabled() {
		t.Errorf("IsSharingEnabled() = true, want false when S3_PUBLIC_ENDPOINT_URL is unset")
	}

	t.Setenv("S3_PUBLIC_ENDPOINT_URL", "https://public.example.com")
	if !Garage.IsSharingEnabled() {
		t.Errorf("IsSharingEnabled() = false, want true when S3_PUBLIC_ENDPOINT_URL is set")
	}
}
