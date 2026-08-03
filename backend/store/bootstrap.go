package store

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
)

// bcryptPrefixes are the hash identifiers golang.org/x/crypto/bcrypt accepts.
// Anything else in AUTH_USER_PASS is a typo or a hash from a different
// algorithm and would only ever produce failed logins.
var bcryptPrefixes = []string{"$2a$", "$2b$", "$2y$"}

// ParseUserPass parses the legacy AUTH_USER_PASS format into
// username→bcrypt-hash. Entries are comma-separated; within an entry the
// FIRST ':' splits username from hash (bcrypt hashes contain neither ',' nor
// ':'). A single "user:hash" yields one entry; malformed entries are dropped.
//
// It lives in this package, not in the router, because the environment
// variables are now read exactly once — at import time — and never consulted
// again.
func ParseUserPass(raw string) map[string]string {
	users := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		i := strings.Index(entry, ":")
		if i <= 0 || i == len(entry)-1 {
			continue // malformed: no ':', empty user, or empty hash
		}
		users[strings.TrimSpace(entry[:i])] = entry[i+1:]
	}
	return users
}

// ImportLegacyUsers seeds the database from AUTH_USER_PASS /
// AUTH_VIEWER_USER_PASS exactly once. It is a no-op when any user already
// exists, which makes it idempotent and makes the database authoritative from
// then on: the environment variables are never consulted again, so changing
// them after the first start has no effect.
//
// Hashes are stored verbatim — they are already bcrypt hashes, and re-hashing
// them would lock every existing operator out.
//
// It returns how many accounts were imported.
func ImportLegacyUsers(ctx context.Context, s *Store, adminsRaw, viewersRaw string) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("cannot import legacy users: %w", ErrNoStore)
	}

	existing, err := s.CountUsers(ctx)
	if err != nil {
		return 0, err
	}
	// The single check that makes this import one-shot. Do not remove it: it
	// is what stops a stale AUTH_USER_PASS from resurrecting a deleted
	// account or reverting a changed password on every restart.
	if existing > 0 {
		return 0, nil
	}

	type candidate struct {
		username string
		hash     string
		role     string
	}

	var pending []candidate
	// Usernames are case-insensitive in the database, so dedupe the same way
	// here — otherwise "Admin" as a viewer and "admin" as an admin would
	// collide at INSERT time instead of being resolved by the rule below.
	claimed := map[string]bool{}

	// Admins are collected first so that a username appearing in both
	// variables is imported as an admin and its viewer entry is dropped.
	for _, src := range []struct {
		raw  string
		role string
	}{
		{adminsRaw, RoleAdmin},
		{viewersRaw, RoleViewer},
	} {
		parsed := ParseUserPass(src.raw)

		// Map iteration order is random; sort so the import is deterministic
		// and its log output reproducible.
		names := make([]string, 0, len(parsed))
		for name := range parsed {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			hash := parsed[name]
			if !looksLikeBcrypt(hash) {
				// Never log the hash itself, only the name it belongs to.
				log.Printf("Skipping legacy user %q: password is not a bcrypt hash.", name)
				continue
			}
			key := strings.ToLower(name)
			if claimed[key] {
				log.Printf("Skipping duplicate legacy viewer %q: already imported as an administrator.", name)
				continue
			}
			claimed[key] = true
			pending = append(pending, candidate{username: name, hash: hash, role: src.role})
		}
	}

	imported := 0
	for _, c := range pending {
		// Deliberately NOT validated against ValidateUsername: an existing
		// deployment's login name must keep working across the upgrade, even
		// if it uses characters the app would no longer hand out.
		if _, err := s.insertUser(ctx, c.username, c.hash, c.role); err != nil {
			log.Printf("Skipping legacy user %q: %v", c.username, err)
			continue
		}
		imported++
	}

	return imported, nil
}

func looksLikeBcrypt(hash string) bool {
	for _, p := range bcryptPrefixes {
		if strings.HasPrefix(hash, p) {
			return true
		}
	}
	return false
}
