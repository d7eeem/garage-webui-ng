package router

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/d7eeem/garage-webui-ng/utils"
)

// GET /update-check — reports whether a newer release exists.
//
// Disabled by default. This is the only outbound request this service makes to
// anything other than the configured Garage cluster, so it is opt-in
// (UPDATE_CHECK_ENABLED=true) and the URL is a package-level var only so tests
// can point it at an httptest server — never built from user input, and never
// proxied on a caller's behalf.
var releasesURL = "https://api.github.com/repos/d7eeem/garage-webui-ng/releases/latest"

// updateCacheKey namespaces this handler's entry in utils.Cache.
const updateCacheKey = "update-check"

// updateCacheTTL bounds how often this service calls GitHub. GitHub's
// unauthenticated rate limit is 60 requests/hour/IP; 6h keeps this service
// nowhere near that even with many browser tabs open, since every tab hits
// this cache, not GitHub.
const updateCacheTTL = 6 * time.Hour

// updateRequestTimeout bounds a single call to GitHub so a slow or hanging
// upstream can never stall this handler.
const updateRequestTimeout = 5 * time.Second

// UpdateCheck is both the cached value and the JSON response shape.
type UpdateCheck struct {
	Enabled         bool   `json:"enabled"`
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	URL             string `json:"url,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
	CheckFailed     bool   `json:"checkFailed,omitempty"`
}

type Update struct{}

// githubRelease is the subset of GitHub's "latest release" response this
// handler cares about.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func (u *Update) Get(w http.ResponseWriter, r *http.Request) {
	current := AppVersion

	if utils.GetEnv("UPDATE_CHECK_ENABLED", "false") != "true" {
		utils.ResponseSuccess(w, UpdateCheck{Enabled: false, Current: current})
		return
	}

	if cached := utils.Cache.Get(updateCacheKey); cached != nil {
		if result, ok := cached.(UpdateCheck); ok {
			utils.ResponseSuccess(w, result)
			return
		}
	}

	release, err := fetchLatestRelease(r.Context())
	if err != nil {
		log.Printf("update check: %v", err)
		result := UpdateCheck{Enabled: true, Current: current, CheckFailed: true}
		// Deliberately not cached: a transient GitHub outage should not force
		// every viewer to see "check failed" for a further 6 hours once GitHub
		// recovers.
		utils.ResponseSuccess(w, result)
		return
	}

	result := UpdateCheck{
		Enabled:         true,
		Current:         current,
		Latest:          release.TagName,
		URL:             release.HTMLURL,
		UpdateAvailable: isNewer(current, release.TagName),
	}
	utils.Cache.Set(updateCacheKey, result, updateCacheTTL)
	utils.ResponseSuccess(w, result)
}

// fetchLatestRelease calls releasesURL and decodes the fields this handler
// needs. Never returns GitHub's raw response body to the caller — only a
// wrapped error for the server log.
func fetchLatestRelease(ctx context.Context) (githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, updateRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, &statusError{status: resp.StatusCode}
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

// statusError reports an unexpected HTTP status without carrying the response
// body, so a caller logging it can never leak upstream content.
type statusError struct{ status int }

func (e *statusError) Error() string {
	return "unexpected status " + strconv.Itoa(e.status)
}

// isNewer reports whether latest is a numerically greater dotted version than
// current. Deliberately conservative: anything that is not purely
// numeric-dotted (after trimming a leading "v") — "dev", pre-release suffixes,
// empty strings — reports false. A missed notification is a non-event; a
// false "update available" erodes trust. Kept dependency-free on purpose: no
// semver library, just a left-to-right numeric component comparison (a plain
// string compare would wrongly rank "3.9.0" below "3.10.0").
func isNewer(current, latest string) bool {
	currentParts, ok := parseNumericVersion(current)
	if !ok {
		return false
	}
	latestParts, ok := parseNumericVersion(latest)
	if !ok {
		return false
	}

	n := len(currentParts)
	if len(latestParts) > n {
		n = len(latestParts)
	}
	for i := 0; i < n; i++ {
		var c, l int
		if i < len(currentParts) {
			c = currentParts[i]
		}
		if i < len(latestParts) {
			l = latestParts[i]
		}
		if l != c {
			return l > c
		}
	}
	return false
}

// parseNumericVersion trims a leading "v" and splits on ".", requiring every
// component to be purely numeric. Returns ok=false for anything else (e.g.
// "dev", "3.3.0-rc1", "").
func parseNumericVersion(v string) ([]int, bool) {
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, false
	}
	segments := strings.Split(v, ".")
	parts := make([]int, len(segments))
	for i, s := range segments {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, false
		}
		parts[i] = n
	}
	return parts, true
}
