// Package store owns the only persistent state this service has: the user
// database. Everything else here is a stateless gateway to a Garage cluster,
// so this package is deliberately small — a SQLite file, a hand-rolled
// migrator, and CRUD over one table.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/d7eeem/garage-webui-ng/utils"

	// Pure-Go SQLite driver. This MUST stay modernc.org/sqlite: the release
	// build is CGO_ENABLED=0 (backend/Makefile, Dockerfile) onto a
	// distroless/static base, and the cgo-based github.com/mattn/go-sqlite3
	// cannot link there. Note the registered driver name is "sqlite", not
	// "sqlite3".
	_ "modernc.org/sqlite"
)

// DefaultDBPath is where the user database lives unless DB_PATH says
// otherwise. In the container image DB_PATH points at /data, which is the
// declared volume.
const DefaultDBPath = "./data/garage-webui-ng.db"

// ErrNoStore signals that no store has been installed yet. Reaching it means
// a request arrived before startup finished, or a test forgot to call
// SetDefault.
var ErrNoStore = errors.New("user database is not available")

// Store is a handle on the user database.
type Store struct {
	db *sql.DB
}

var (
	defaultMu    sync.RWMutex
	defaultStore *Store
)

// SetDefault installs the process-wide store. Handlers in this repo are
// methods on empty structs and reach their dependencies through package-level
// singletons (utils.Session, utils.Garage); this mirrors that rather than
// threading a *Store through every constructor.
func SetDefault(s *Store) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultStore = s
}

// Default returns the process-wide store, or nil when none has been
// installed. Callers must handle nil: it means the process has not finished
// starting up (or a test has not wired one in).
func Default() *Store {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultStore
}

// DBPath returns the configured database file location. It must sit on
// persistent storage: losing this file loses every user account.
func DBPath() string {
	return utils.GetEnv("DB_PATH", DefaultDBPath)
}

// Open opens (creating it if needed) the SQLite database at path and brings
// its schema up to date.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("cannot create data directory %q: %w", dir, err)
	}

	// busy_timeout   — wait instead of failing instantly if a lock is held.
	// journal_mode   — WAL, so a reader never blocks the writer.
	// foreign_keys   — off by default in SQLite; on so future FKs are enforced.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open user database %q: %w", path, err)
	}

	// SQLite permits exactly one writer at a time. This database holds a
	// handful of rows and is touched on login and on user administration, so
	// funnelling every statement through a single connection costs nothing
	// and removes "database is locked" as a failure mode entirely. Do not
	// raise this without adding SQLITE_BUSY retry handling.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot reach user database %q: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot migrate user database %q: %w", path, err)
	}

	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// migrations is APPEND-ONLY. Each entry is one schema version and its
// index+1 is the number recorded in schema_migrations. Never edit, reorder or
// remove an entry that has shipped — the version counter is the only record
// of what a live database already has, so an edited entry would silently
// never be applied to existing installs.
var migrations = []string{
	// 1 — users.
	//
	// username is COLLATE NOCASE so the UNIQUE index is case-insensitive:
	// "Admin" and "admin" are the same account. That matches what operators
	// expect from a login name and stops look-alike duplicate accounts.
	`CREATE TABLE users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
		password_hash TEXT    NOT NULL,
		role          TEXT    NOT NULL DEFAULT 'admin',
		disabled      INTEGER NOT NULL DEFAULT 0,
		created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_login    TIMESTAMP
	)`,
}

// migrate applies every migration newer than the recorded schema version,
// each in its own transaction together with the version bump, so a failure
// can never leave a half-applied version behind.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("cannot create schema_migrations table: %w", err)
	}

	current, err := s.SchemaVersion()
	if err != nil {
		return err
	}

	for i, stmt := range migrations {
		version := i + 1
		if version <= current {
			continue
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("migration %d: cannot begin transaction: %w", version, err)
		}
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: cannot record version: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: cannot commit: %w", version, err)
		}
	}

	return nil
}

// SchemaVersion reports the highest migration applied to this database (0 for
// a brand-new file).
func (s *Store) SchemaVersion() (int, error) {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("cannot read schema version: %w", err)
	}
	return version, nil
}
