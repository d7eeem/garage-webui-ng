package store

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestStore opens a throwaway database in a temp directory.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesFileAndSchema(t *testing.T) {
	// A nested directory that does not exist yet: Open must create it, which
	// is what makes a fresh container with an empty /data volume work.
	path := filepath.Join(t.TempDir(), "nested", "users.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created at %s: %v", path, err)
	}

	version, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("schema version = %d, want %d", version, len(migrations))
	}

	// The users table must be usable straight away.
	if n, err := s.CountUsers(t.Context()); err != nil {
		t.Errorf("CountUsers on fresh database: %v", err)
	} else if n != 0 {
		t.Errorf("CountUsers on fresh database = %d, want 0", n)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.CreateUser(t.Context(), "admin", "correct-horse-battery", RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-opening must re-run the migrator without re-applying migration 1
	// (which would fail with "table users already exists") and without
	// touching the data.
	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	version, err := second.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("schema version after reopen = %d, want %d", version, len(migrations))
	}

	if n, err := second.CountUsers(t.Context()); err != nil {
		t.Fatalf("CountUsers: %v", err)
	} else if n != 1 {
		t.Errorf("CountUsers after reopen = %d, want 1 (data must survive)", n)
	}
}

func TestDBPathHonoursEnv(t *testing.T) {
	t.Setenv("DB_PATH", "")
	if got := DBPath(); got != DefaultDBPath {
		t.Errorf("DBPath() with DB_PATH unset = %q, want %q", got, DefaultDBPath)
	}

	t.Setenv("DB_PATH", "/data/custom.db")
	if got := DBPath(); got != "/data/custom.db" {
		t.Errorf("DBPath() = %q, want %q", got, "/data/custom.db")
	}
}

func TestSetDefault(t *testing.T) {
	t.Cleanup(func() { SetDefault(nil) })

	SetDefault(nil)
	if Default() != nil {
		t.Fatal("Default() = non-nil after SetDefault(nil)")
	}

	s := newTestStore(t)
	SetDefault(s)
	if Default() != s {
		t.Error("Default() did not return the store passed to SetDefault")
	}
}
