package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/d7eeem/garage-webui-ng/store"
	"github.com/d7eeem/garage-webui-ng/utils"
)

// isViewerAllowed is the entire security boundary for the read-only viewer
// role: fail-closed by construction. Nothing under /admin/ is reachable at all;
// otherwise every GET is allowed except the one carve-out that reveals a secret
// (GetKeyInfo?showSecretKey=true), and every non-GET is denied except the two
// that only affect the caller's own account. Any new write served via GET, or
// any new secret-revealing GET, must be added here explicitly — do not loosen
// this to "allow all GET".
//
// This is the outer half of a two-layer guard on the administration API: every
// /admin/* handler also calls requireAdmin (backend/router/admin_users.go), so
// a routing mistake alone cannot expose those endpoints. Keep both.
func isViewerAllowed(r *http.Request) bool {
	// User administration is admin-only whatever the method, so the check sits
	// above the per-method rules. Reads are included deliberately: the roster of
	// accounts, their roles and their sign-in times are not viewer-visible
	// information, and "every GET is allowed" below would otherwise hand a
	// viewer the whole user list.
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return false
	}

	if r.Method == http.MethodGet {
		// one carve-out: never let a viewer reveal a secret access key
		if strings.HasPrefix(r.URL.Path, "/v2/GetKeyInfo") &&
			r.URL.Query().Get("showSecretKey") == "true" {
			return false
		}
		return true
	}
	// A read-only viewer may still end their session and change their OWN
	// password. Nothing else that mutates state is permitted.
	//
	// Downloading is a read. The archive endpoint is a GET (already allowed);
	// the token that authorises it is minted with a POST purely because the key
	// list is too large for a URL. It mutates nothing, so a read-only viewer
	// may call it. Exact match only — this must never become a prefix.
	if r.Method == http.MethodPost {
		return r.URL.Path == "/auth/logout" ||
			r.URL.Path == "/auth/change-password" ||
			r.URL.Path == "/browse/download-token"
	}
	return false
}

// isPublicPath reports whether a request may be served without an
// authenticated session. This is a security boundary: authentication is
// mandatory, so everything not listed here requires a session, and the list
// must stay minimal and exact-match — never a prefix.
//
// POST /auth/login is registered on the outer mux (router.HandleApiRouter)
// and never reaches this middleware, so it does not appear here.
func isPublicPath(r *http.Request) bool {
	switch {
	// The UI needs to know whether it is logged in before it can render a
	// login form. The handler exposes nothing beyond the caller's own
	// session state and whether a first account still has to be created.
	case r.Method == http.MethodGet && r.URL.Path == "/auth/status":
		return true

	// First-run setup: a brand-new deployment has no users, so nobody could
	// possibly authenticate in order to create the first one. The handlers
	// themselves must refuse to run once any user exists.
	case r.Method == http.MethodGet && r.URL.Path == "/setup/status":
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/setup":
		return true
	}
	return false
}

// callerCacheTTL bounds how stale a revalidated role/disabled status may be.
// The user database sits behind a pool capped at one connection
// (backend/store/store.go), so an uncached lookup on every authenticated
// request would serialise the whole API behind one query; this cache is the
// trade that avoids that. Five seconds means a disabled, demoted or deleted
// account loses its access within 5s instead of up to the 24h session
// lifetime — four orders of magnitude better, not instant.
const callerCacheTTL = 5 * time.Second

// callerCacheEvictAt is the map size at which resolveCaller opportunistically
// sweeps expired entries, so the cache cannot grow without bound.
const callerCacheEvictAt = 1024

// cachedCaller is what the local cache remembers about one username between
// store lookups. found=false covers both "no such user" and "username not
// yet looked up" callers that turned out to be missing — it, like disabled,
// must be treated as deny.
type cachedCaller struct {
	role     string
	disabled bool
	found    bool
	expires  time.Time
}

var (
	callerCacheMu sync.RWMutex
	callerCache   = map[string]cachedCaller{}

	// nowFunc is the clock resolveCaller uses to judge cache expiry. Tests
	// override it to exercise TTL expiry without sleeping.
	nowFunc = time.Now
)

// resolveCaller answers "what does the store currently say about username,"
// using the short-lived cache above to keep that off the single-connection
// SQLite pool on every request.
//
// ok reports whether the caller may proceed at all: false covers both "no
// such user" (store.GetUserByUsername's (nil, nil) result) and "disabled",
// which callers MUST treat identically to a missing account. err is non-nil
// only for an infrastructure failure (no store installed, or the query
// itself failed) and must fail the request closed, not fall back to ok=true.
func resolveCaller(ctx context.Context, username string) (role string, ok bool, err error) {
	now := nowFunc()

	callerCacheMu.RLock()
	entry, hit := callerCache[username]
	callerCacheMu.RUnlock()
	if hit && now.Before(entry.expires) {
		if !entry.found || entry.disabled {
			return "", false, nil
		}
		return entry.role, true, nil
	}

	st := store.Default()
	if st == nil {
		// Startup has not finished (or a test forgot to install one). This is
		// not the caller's fault, so it is not cached and not reported as
		// "unauthorized" — the handler below turns it into a 500.
		return "", false, store.ErrNoStore
	}

	user, err := st.GetUserByUsername(ctx, username)
	if err != nil {
		return "", false, err
	}

	fresh := cachedCaller{expires: now.Add(callerCacheTTL)}
	if user != nil {
		// A missing user (nil, nil from GetUserByUsername) leaves fresh.found
		// false, which is deliberate: a deleted account must be denied, and
		// caching that negative result is what keeps a deleted account's
		// requests from becoming an uncached query apiece.
		fresh.found = true
		fresh.role = user.Role
		fresh.disabled = user.Disabled
	}

	callerCacheMu.Lock()
	if len(callerCache) >= callerCacheEvictAt {
		for k, v := range callerCache {
			if now.After(v.expires) {
				delete(callerCache, k)
			}
		}
	}
	callerCache[username] = fresh
	callerCacheMu.Unlock()

	if !fresh.found || fresh.disabled {
		return "", false, nil
	}
	return fresh.role, true, nil
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Comma-ok, not a bare type assertion: a session value of an
		// unexpected type must fail closed, not panic.
		if authenticated, _ := utils.Session.Get(r, "authenticated").(bool); !authenticated {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		// The session is a snapshot taken at login and never updated again, so
		// on its own it cannot reflect an account being disabled, demoted or
		// deleted after the session was issued. This block is what makes that
		// take effect: it re-resolves the caller's role and status from the
		// user store (through the short-lived cache above) on every request,
		// and the viewer gate below reads THAT role, never the session's
		// stored copy. Do not write the resolved role back into the session —
		// scs persists any Set, which would make this revalidation as stale
		// as the thing it exists to replace.
		username, _ := utils.Session.Get(r, "username").(string)
		if username == "" {
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		role, ok, err := resolveCaller(r.Context(), username)
		if err != nil {
			utils.ResponseErrorStatus(w, err, http.StatusInternalServerError)
			return
		}
		if !ok {
			// Missing or disabled: identical to "never logged in" from the
			// caller's point of view.
			utils.ResponseErrorStatus(w, errors.New("unauthorized"), http.StatusUnauthorized)
			return
		}

		if role == "viewer" && !isViewerAllowed(r) {
			utils.ResponseErrorStatus(w, errors.New("forbidden: read-only session"), http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
