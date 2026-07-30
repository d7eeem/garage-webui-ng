package utils

import (
	"os"
	"path/filepath"
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
