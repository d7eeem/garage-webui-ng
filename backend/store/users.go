package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	sqlite "modernc.org/sqlite"
)

// User is an application user. PasswordHash is never serialised: the
// `json:"-"` tag is the single guarantee that a hash cannot leak through any
// API response, so it must stay on this field.
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLogin    *time.Time `json:"lastLogin"`
}

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

const (
	// MinPasswordLength is the shortest password the app will accept.
	MinPasswordLength = 10

	// MaxPasswordBytes is bcrypt's hard input limit. bcrypt silently ignores
	// everything past 72 bytes, so a longer password would give a false sense
	// of strength (and two passwords sharing a 72-byte prefix would be
	// interchangeable). Reject it explicitly instead.
	MaxPasswordBytes = 72

	// MaxUsernameLength bounds the login name.
	MaxUsernameLength = 64

	// bcryptCost is the work factor for new hashes.
	bcryptCost = 10
)

// usernamePattern deliberately excludes ':' and ',' — the separators of the
// legacy AUTH_USER_PASS format — plus whitespace, so every username the app
// creates round-trips through that format and through log lines unambiguously.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

var (
	ErrUsernameTaken   = errors.New("username is already taken")
	ErrUserNotFound    = errors.New("user not found")
	ErrInvalidRole     = errors.New("invalid role")
	ErrWeakPassword    = errors.New("password is not acceptable")
	ErrInvalidUsername = errors.New("invalid username")

	// ErrSetupAlreadyDone is returned when the instance already has at least
	// one user. It is the guard that makes the unauthenticated /setup endpoint
	// safe: once it can be returned, the wizard is permanently closed.
	ErrSetupAlreadyDone = errors.New("setup has already been completed")
)

// ValidateUsername enforces the login-name charset and length.
func ValidateUsername(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("%w: must not be empty", ErrInvalidUsername)
	}
	if len(s) > MaxUsernameLength {
		return fmt.Errorf("%w: must be at most %d characters", ErrInvalidUsername, MaxUsernameLength)
	}
	if !usernamePattern.MatchString(s) {
		return fmt.Errorf("%w: only letters, digits and . _ @ - are allowed", ErrInvalidUsername)
	}
	return nil
}

// ValidatePassword enforces the password policy. It returns ErrWeakPassword
// wrapped with a reason suitable for showing to the user.
func ValidatePassword(s string) error {
	if len(s) < MinPasswordLength {
		return fmt.Errorf("%w: must be at least %d characters", ErrWeakPassword, MinPasswordLength)
	}
	if len(s) > MaxPasswordBytes {
		return fmt.Errorf("%w: must be at most %d bytes (bcrypt ignores anything beyond that)", ErrWeakPassword, MaxPasswordBytes)
	}
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%w: must not be entirely whitespace", ErrWeakPassword)
	}
	return nil
}

// ValidateRole rejects anything that is not one of the two known roles.
func ValidateRole(role string) error {
	switch role {
	case RoleAdmin, RoleViewer:
		return nil
	}
	return fmt.Errorf("%w: %q (want %q or %q)", ErrInvalidRole, role, RoleAdmin, RoleViewer)
}

const userColumns = `id, username, password_hash, role, disabled, created_at, updated_at, last_login`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var (
		u         User
		disabled  int64
		createdAt sql.NullTime
		updatedAt sql.NullTime
		lastLogin sql.NullTime
	)
	if err := row.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role,
		&disabled, &createdAt, &updatedAt, &lastLogin,
	); err != nil {
		return nil, err
	}

	u.Disabled = disabled != 0
	u.CreatedAt = createdAt.Time
	u.UpdatedAt = updatedAt.Time
	if lastLogin.Valid {
		t := lastLogin.Time
		u.LastLogin = &t
	}
	return &u, nil
}

// CountUsers returns the total number of accounts, enabled or not.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("cannot count users: %w", err)
	}
	return n, nil
}

// CountEnabledAdmins returns how many admins can still log in. Callers use it
// to refuse the last operation that would lock everyone out of the app.
func (s *Store) CountEnabledAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ? AND disabled = 0`, RoleAdmin,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("cannot count enabled admins: %w", err)
	}
	return n, nil
}

// GetUserByUsername looks a user up by login name (case-insensitively, per
// the COLLATE NOCASE index). A missing user is (nil, nil), not an error:
// "no such user" is an ordinary outcome for a login attempt, and callers must
// not have to distinguish sql.ErrNoRows from a real database failure.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ?`, strings.TrimSpace(username))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot load user %q: %w", username, err)
	}
	return u, nil
}

// GetUserByID looks a user up by primary key. A missing user is (nil, nil).
func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot load user %d: %w", id, err)
	}
	return u, nil
}

// ListUsers returns every account ordered by username.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("cannot list users: %w", err)
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("cannot scan user: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot list users: %w", err)
	}
	return users, nil
}

// CreateUser validates the inputs, hashes the password with bcrypt and
// inserts the account.
func (s *Store) CreateUser(ctx context.Context, username, plaintextPassword, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(plaintextPassword); err != nil {
		return nil, err
	}
	if err := ValidateRole(role); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("cannot hash password: %w", err)
	}

	return s.insertUser(ctx, username, string(hash), role)
}

// insertUser writes a row with passwordHash exactly as given. Producing that
// hash is the caller's job: CreateUser hashes a plaintext password, while
// ImportLegacyUsers passes through a hash that AUTH_USER_PASS already carried.
func (s *Store) insertUser(ctx context.Context, username, passwordHash, role string) (*User, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`,
		username, passwordHash, role)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%q: %w", username, ErrUsernameTaken)
		}
		return nil, fmt.Errorf("cannot create user %q: %w", username, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("cannot read id of new user %q: %w", username, err)
	}

	u, err := s.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("user %q disappeared right after insert: %w", username, ErrUserNotFound)
	}
	return u, nil
}

// CreateFirstAdmin creates the initial administrator, but only while the users
// table is empty. It backs POST /setup, the one endpoint in this app that
// writes without an authenticated session, so the emptiness check and the
// insert must be indivisible: two concurrent setup requests may not both
// succeed, and no request may succeed after the first user exists.
//
// Atomicity comes from running the count and the insert on one *sql.Tx over a
// pool limited to a single connection (Open sets SetMaxOpenConns(1)). The
// second caller cannot even begin its transaction until the first has
// committed and released that connection, so it always sees the new row and
// gets ErrSetupAlreadyDone. Raising SetMaxOpenConns without switching this
// transaction to BEGIN IMMEDIATE would reintroduce the race.
//
// It is a function rather than a method purely to keep the callers' intent
// obvious at the call site: store.CreateFirstAdmin reads as a one-time
// bootstrap, not as ordinary user administration.
func CreateFirstAdmin(ctx context.Context, s *Store, username, password string) (*User, error) {
	if s == nil {
		return nil, ErrNoStore
	}

	// Validate before touching the database: a rejected password must not cost
	// a transaction, and the caller needs the specific validation error.
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}

	// Hash outside the transaction. bcrypt at cost 10 takes ~100ms and the
	// store has exactly one connection, so hashing inside would block every
	// other query for that whole time.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("cannot hash password: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot begin setup transaction: %w", err)
	}
	// No-op once Commit has run; the guard against leaving the sole connection
	// held open on every early return below.
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, fmt.Errorf("cannot count users: %w", err)
	}
	if n > 0 {
		return nil, ErrSetupAlreadyDone
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, disabled) VALUES (?, ?, ?, 0)`,
		username, string(hash), RoleAdmin)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%q: %w", username, ErrUsernameTaken)
		}
		return nil, fmt.Errorf("cannot create first administrator %q: %w", username, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("cannot read id of first administrator %q: %w", username, err)
	}

	// Read the row back on the transaction, not on s.db: the pool has a single
	// connection and this transaction is holding it, so a query through the
	// store would deadlock until the context expired.
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("cannot read back first administrator %q: %w", username, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cannot commit first administrator %q: %w", username, err)
	}
	return u, nil
}

// SetPassword validates and stores a new password for the given user.
func (s *Store) SetPassword(ctx context.Context, id int64, plaintextPassword string) error {
	if err := ValidatePassword(plaintextPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), bcryptCost)
	if err != nil {
		return fmt.Errorf("cannot hash password: %w", err)
	}
	return s.exec(ctx, "set password",
		`UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(hash), id)
}

// SetDisabled enables or disables an account. A disabled account keeps its
// row (and its audit history) but can no longer log in.
func (s *Store) SetDisabled(ctx context.Context, id int64, disabled bool) error {
	flag := 0
	if disabled {
		flag = 1
	}
	return s.exec(ctx, "set disabled",
		`UPDATE users SET disabled = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		flag, id)
}

// SetRole changes an account's role.
func (s *Store) SetRole(ctx context.Context, id int64, role string) error {
	if err := ValidateRole(role); err != nil {
		return err
	}
	return s.exec(ctx, "set role",
		`UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		role, id)
}

// Rename changes an account's login name.
func (s *Store) Rename(ctx context.Context, id int64, username string) error {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return err
	}
	err := s.exec(ctx, "rename",
		`UPDATE users SET username = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		username, id)
	if err != nil && isUniqueViolation(err) {
		return fmt.Errorf("%q: %w", username, ErrUsernameTaken)
	}
	return err
}

// DeleteUser removes an account permanently.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.exec(ctx, "delete user", `DELETE FROM users WHERE id = ?`, id)
}

// TouchLastLogin records a successful login. It deliberately leaves
// updated_at alone: that column tracks administrative changes to the account,
// not sign-in activity.
func (s *Store) TouchLastLogin(ctx context.Context, id int64) error {
	return s.exec(ctx, "record last login",
		`UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = ?`, id)
}

// exec runs a single-row statement and turns "matched nothing" into
// ErrUserNotFound so callers get one consistent signal.
func (s *Store) exec(ctx context.Context, what, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("cannot %s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cannot %s: %w", what, err)
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SQLite extended result codes for the two constraint failures a duplicate
// username can produce.
const (
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

// isUniqueViolation reports whether err is SQLite refusing a duplicate key.
// The driver surfaces it as *sqlite.Error carrying an extended result code;
// the message check is a fallback in case a future driver version wraps the
// error differently.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		switch serr.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
