package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Guards the "all:" prefix on the go:embed directive.
//
// SvelteKit emits every asset into an "_app" directory, and go:embed skips
// underscore- and dot-prefixed paths unless the pattern says "all:". Dropping
// the prefix still compiles and still serves index.html, so the only visible
// symptom is an unstyled, non-interactive page in production.
//
// dist/.gitkeep is a dot-prefixed file, so it is embedded only when the
// directive is correct. If this test fails, check internal/web/embed.go.
func TestEmbedIncludesUnderscoreAndDotPaths(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Fatalf("FS: %v", err)
	}

	if _, err := fs.Stat(sub, ".gitkeep"); err != nil {
		t.Fatal(
			"dot-prefixed files are missing from the embedded FS, which means the " +
				"go:embed directive lost its \"all:\" prefix. A real build would ship " +
				"index.html with no _app assets behind it.")
	}
}

func TestHandlerServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("did not serve the SPA document")
	}
}

func TestHandlerFallsBackForClientRoutes(t *testing.T) {
	paths := []string{
		"/app/workflows/wf_123",
		"/embed/wf_123",
		"/settings/credentials",
		"/deeply/nested/unknown/route",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 via SPA fallback", path, rec.Code)
		}
	}
}

// The fallback must not let a request escape the embedded tree.
func TestHandlerRejectsTraversal(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../../go.mod", nil))

	if body := rec.Body.String(); strings.Contains(body, "module github.com") {
		t.Fatal("path traversal escaped the embedded filesystem")
	}
}
