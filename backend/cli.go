package main

// Offline account-recovery commands.
//
// These are process flags, never HTTP endpoints. A password reset reachable
// over the network without authentication is a backdoor; these commands
// instead operate directly on the SQLite file at DB_PATH, so running them
// requires local read/write access to a file the operator could already edit
// by hand. Never "improve" this by exposing it over HTTP.
//
// A password is likewise never accepted as a command-line argument: argv is
// visible in shell history, `ps` output and process accounting. It is read
// from the terminal with echo disabled, or from a pipe for scripted recovery.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/d7eeem/garage-webui-ng/store"

	"golang.org/x/term"
)

// openCLIStore opens the user database at the same location the server uses,
// so the CLI and the running service can never disagree about which file holds
// the accounts. On failure it reports the path and returns a nil store; the
// caller must return the accompanying exit code without touching the store.
//
// Note that store.Open runs migrations, so pointing this at an older database
// (a backup copy, say) upgrades its schema.
func openCLIStore() (*store.Store, int) {
	path := store.DBPath()
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open user database at %s: %v\n", path, err)
		return nil, 1
	}
	return st, 0
}

// promptPassword reads a password from the terminal with echo disabled, then
// asks for it a second time so a typo cannot silently become the new
// credential.
//
// A password is never taken from a command-line argument: arguments are
// visible in shell history, `ps` output and process accounting. When stdin is
// not a terminal (a pipe, for scripted recovery) a single line is read
// instead, and no confirmation is possible.
//
// The value is never echoed, never logged, and never included in an error.
func promptPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("cannot read password from stdin: %w", err)
		}
		// Strip the line terminator and nothing else: a password may
		// legitimately contain leading or trailing spaces.
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			return "", errors.New("no password on stdin")
		}
		return line, nil
	}

	// Prompts go to stderr so that piping stdout somewhere never captures them.
	first, err := readHidden(fd, prompt)
	if err != nil {
		return "", err
	}
	second, err := readHidden(fd, "Confirm password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}

// readHidden writes a prompt and reads one line with terminal echo disabled.
// ReadPassword consumes the Enter key without echoing it, so the newline that
// ends the prompt line has to be written by hand.
func readHidden(fd int, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("cannot read password: %w", err)
	}
	return string(b), nil
}

// runResetPassword sets a new password for an existing account and returns a
// process exit code. Hashing is the store's job — the CLI never hashes.
func runResetPassword(username string) int {
	st, code := openCLIStore()
	if st == nil {
		return code
	}
	defer st.Close()

	ctx := context.Background()
	user, err := st.GetUserByUsername(ctx, username)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if user == nil {
		fmt.Fprintf(os.Stderr, "no such user: %s\n", username)
		return 1
	}

	password, err := promptPassword("New password: ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// SetPassword validates against the same policy the UI enforces, so the
	// two can never disagree about what a valid password is. Its error names
	// the reason, never the value.
	if err := st.SetPassword(ctx, user.ID, password); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("Password updated for %q (role: %s).\n", user.Username, user.Role)
	return 0
}

// runCreateAdmin creates a new administrator and returns a process exit code.
//
// Unlike POST /setup this deliberately works even when accounts already exist:
// that is the entire point of a recovery tool. It is safe because it requires
// local write access to the database file.
func runCreateAdmin(username string) int {
	// Validate the name before opening anything, so a typo does not create a
	// data directory or migrate a database.
	if err := store.ValidateUsername(username); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	st, code := openCLIStore()
	if st == nil {
		return code
	}
	defer st.Close()

	password, err := promptPassword("Password: ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	user, err := st.CreateUser(context.Background(), username, password, store.RoleAdmin)
	if err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			fmt.Fprintf(os.Stderr, "user %q already exists - use -reset-password instead\n", username)
			return 1
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("Created administrator %q.\n", user.Username)
	return 0
}

// runListUsers prints the accounts in the user database and returns a process
// exit code. Only non-secret columns are ever printed: User.PasswordHash must
// not appear in this output on any code path.
func runListUsers() int {
	st, code := openCLIStore()
	if st == nil {
		return code
	}
	defer st.Close()

	users, err := st.ListUsers(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(users) == 0 {
		fmt.Println("No accounts in the user database.")
		return 0
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tROLE\tSTATUS\tLAST LOGIN\tCREATED")
	for _, u := range users {
		status := "active"
		if u.Disabled {
			status = "disabled"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			u.Username, u.Role, status, formatTime(u.LastLogin), formatTime(&u.CreatedAt))
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// formatTime renders an optional timestamp for the table, printing "-" for a
// value that was never set.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05Z")
}
