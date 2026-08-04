//go:build prod
// +build prod

package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

//go:embed dist
var embeddedFs embed.FS

// Cache-Control values. index.html is the manifest naming every content-hashed
// chunk, so it must always be revalidated; the hashed chunks themselves can be
// cached forever because Vite changes the filename whenever the content does.
const (
	cacheControlIndex     = "no-cache"
	cacheControlImmutable = "public, max-age=31536000, immutable"
	cacheControlStatic    = "public, max-age=3600"
)

// isBuildAsset reports whether p names a build artefact rather than an SPA
// navigation route.
//
// Requests for build artefacts must never fall through to index.html. A hashed
// chunk that no longer exists (an upgraded binary, a browser holding the old
// index.html) has to fail as a 404: returning HTML with status 200 makes the
// browser try to parse a document as a JavaScript module, which surfaces as
// "error loading dynamically imported module" and dead-ends the app.
func isBuildAsset(p string) bool {
	if strings.HasPrefix(p, "assets/") {
		return true
	}
	switch path.Ext(p) {
	case ".js", ".mjs", ".css", ".map", ".json", ".woff", ".woff2", ".ttf", ".svg", ".png", ".ico", ".webmanifest":
		return true
	}
	return false
}

// cacheControlFor picks the caching policy for a path that exists in dist.
func cacheControlFor(p string) string {
	if strings.HasPrefix(p, "assets/") {
		return cacheControlImmutable
	}
	return cacheControlStatic
}

func ServeUI(mux *http.ServeMux) {
	distFs, _ := fs.Sub(embeddedFs, "dist")
	fileServer := http.FileServer(http.FS(distFs))
	basePath := os.Getenv("BASE_PATH")

	mux.Handle(basePath+"/", http.StripPrefix(basePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_path := path.Clean(r.URL.Path)[1:]

		// Rewrite non-existing paths to index.html
		if _, err := fs.Stat(distFs, _path); err != nil {
			// ...but only navigation routes. A missing build artefact is a 404,
			// never an HTML document with status 200.
			if isBuildAsset(_path) {
				http.NotFound(w, r)
				return
			}

			index, _ := fs.ReadFile(distFs, "index.html")
			html := string(index)

			// Set base path for the UI
			html = strings.ReplaceAll(html, "%BASE_PATH%", basePath)
			html = addBasePath(html, basePath)

			w.Header().Add("Content-Type", "text/html")
			w.Header().Set("Cache-Control", cacheControlIndex)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(html))
			return
		}

		// Add prefix to each /assets strings in js
		if len(basePath) > 0 && strings.HasSuffix(_path, ".js") {
			data, _ := fs.ReadFile(distFs, _path)
			html := string(data)
			html = strings.ReplaceAll(html, "assets/", basePath[1:]+"/assets/")

			w.Header().Add("Content-Type", "text/javascript")
			w.Header().Set("Cache-Control", cacheControlFor(_path))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(html))
			return
		}

		// A direct hit on index.html is the manifest too — never cache it.
		if _path == "index.html" {
			w.Header().Set("Cache-Control", cacheControlIndex)
		} else {
			w.Header().Set("Cache-Control", cacheControlFor(_path))
		}

		fileServer.ServeHTTP(w, r)
	})))
}

func addBasePath(html string, basePath string) string {
	re := regexp.MustCompile(`(href|src)=["'](/[^"'>]+)["']`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		return re.ReplaceAllString(match, `$1="`+basePath+`$2"`)
	})
}
