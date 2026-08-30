// Package web embeds the compiled SvelteKit SPA into the kilasflow binary and
// serves it with history-API fallback, so a single binary ships both the API
// and the editor.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// assets holds the built SPA.
//
// The "all:" prefix is required, not cosmetic: without it go:embed skips every
// path whose name begins with "_" or ".", and SvelteKit emits all of its
// JavaScript and CSS into an "_app" directory. A plain "dist" or "dist/*"
// pattern compiles without complaint and produces a binary that serves
// index.html with no assets behind it.
//
// dist/.gitkeep and dist/index.html are committed placeholders so this package
// compiles on a fresh clone, before the frontend has ever been built.
//
//go:embed all:dist
var assets embed.FS

// FS returns the embedded SPA rooted at the dist directory.
func FS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}

// Handler serves the embedded SPA.
//
// Requests for files that exist are served directly; anything else falls back
// to index.html so client-side routes such as /app/workflows/:id survive a
// hard refresh or a deep link.
func Handler() http.Handler {
	sub, err := FS()
	if err != nil {
		// Only reachable if the embedded tree is malformed, which is a build-time
		// property of this package rather than a runtime condition.
		panic("web: cannot open embedded SPA: " + err.Error())
	}

	files := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))

		if name == "." || name == "/" {
			name = "index.html"
		}

		if info, err := fs.Stat(sub, name); err != nil || info.IsDir() {
			serveIndex(w, r, files)
			return
		}

		files.ServeHTTP(w, r)
	})
}

// serveIndex rewrites the request to the SPA entrypoint without mutating the
// caller's request.
func serveIndex(w http.ResponseWriter, r *http.Request, files http.Handler) {
	clone := r.Clone(r.Context())
	clone.URL.Path = "/"
	files.ServeHTTP(w, clone)
}
