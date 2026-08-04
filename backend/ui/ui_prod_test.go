//go:build prod
// +build prod

package ui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newUIServer starts a test server around ServeUI, skipping the test when the
// frontend has not been built into backend/ui/dist (a build artefact, not
// committed).
func newUIServer(t *testing.T) *httptest.Server {
	t.Helper()

	if _, err := fs.Stat(embeddedFs, "dist/index.html"); err != nil {
		t.Skip("frontend not built: run `pnpm run build && cp -r dist backend/ui/dist`")
	}

	mux := http.NewServeMux()
	ServeUI(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()

	res, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { res.Body.Close() })

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", path, err)
	}
	return res, string(body)
}

// firstAsset returns the name of a real file under dist/assets.
func firstAsset(t *testing.T, suffix string) string {
	t.Helper()

	entries, err := fs.ReadDir(embeddedFs, "dist/assets")
	if err != nil {
		t.Fatalf("read dist/assets: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			return e.Name()
		}
	}
	t.Fatalf("no %s file in dist/assets", suffix)
	return ""
}

// The reported bug: a hashed chunk that no longer exists was answered with
// index.html and status 200, so the browser parsed HTML as a JS module.
func TestMissingBuildAssetIs404NotHTML(t *testing.T) {
	srv := newUIServer(t)

	for _, path := range []string{
		"/assets/definitely-missing-xyz.js",
		"/assets/page-DOESNOTEXIST.js",
		"/assets/gone-abc123.css",
		"/missing-at-root.js",
		"/nope.woff2",
	} {
		res, body := get(t, srv, path)

		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, res.StatusCode)
		}
		if strings.Contains(strings.ToLower(body), "<!doctype") {
			t.Errorf("GET %s: body is an HTML document, want a plain 404", path)
		}
	}
}

// The history fallback must keep working for navigation routes, or every deep
// link breaks.
func TestUnknownRouteServesIndexHTML(t *testing.T) {
	srv := newUIServer(t)

	for _, path := range []string{
		"/buckets/some-deep-link",
		"/buckets/abc123/browse",
		"/cluster",
		"/keys",
		"/setup",
	} {
		res, body := get(t, srv, path)

		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, res.StatusCode)
		}
		if !strings.Contains(strings.ToLower(body), "<!doctype html") {
			t.Errorf("GET %s: body is not an HTML document", path)
		}
		if got := res.Header.Get("Cache-Control"); got != cacheControlIndex {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, cacheControlIndex)
		}
	}
}

func TestIndexIsNoCache(t *testing.T) {
	srv := newUIServer(t)

	for _, path := range []string{"/", "/index.html"} {
		res, body := get(t, srv, path)

		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, res.StatusCode)
		}
		if !strings.Contains(strings.ToLower(body), "<!doctype html") {
			t.Errorf("GET %s: body is not an HTML document", path)
		}
		if got := res.Header.Get("Cache-Control"); got != cacheControlIndex {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, cacheControlIndex)
		}
	}
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	srv := newUIServer(t)

	for _, suffix := range []string{".js", ".css"} {
		name := firstAsset(t, suffix)
		path := "/assets/" + name

		res, _ := get(t, srv, path)

		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", path, res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != cacheControlImmutable {
			t.Errorf("GET %s: Cache-Control = %q, want %q", path, got, cacheControlImmutable)
		}
	}
}

// Root-level files are real but not content-hashed, so they get a short TTL
// rather than `immutable`.
func TestUnhashedStaticFileIsShortLived(t *testing.T) {
	srv := newUIServer(t)

	res, _ := get(t, srv, "/favicon.ico")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /favicon.ico: status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != cacheControlStatic {
		t.Errorf("GET /favicon.ico: Cache-Control = %q, want %q", got, cacheControlStatic)
	}
}

func TestIsBuildAsset(t *testing.T) {
	assets := []string{
		"assets/index-abc123.js",
		"assets/index-abc123.css",
		"assets/logo-abc.svg",
		"main.js",
		"styles.css",
		"bundle.js.map",
		"site.webmanifest",
		"favicon.ico",
		"font.woff2",
	}
	for _, p := range assets {
		if !isBuildAsset(p) {
			t.Errorf("isBuildAsset(%q) = false, want true", p)
		}
	}

	routes := []string{
		"",
		"buckets",
		"buckets/some-deep-link",
		"cluster",
		"settings/account",
	}
	for _, p := range routes {
		if isBuildAsset(p) {
			t.Errorf("isBuildAsset(%q) = true, want false", p)
		}
	}
}
