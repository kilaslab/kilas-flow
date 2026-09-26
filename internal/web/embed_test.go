package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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

// The embedded placeholder is what a clone serves before the frontend has ever
// been built. It shares dist/ with the build output, which is why it is a file
// outside that directory: a tracked index.html inside dist/ was overwritten by
// `make build-web`, dirtying the tree on every build.
func TestPlaceholderLivesOutsideTheBuildDirectory(t *testing.T) {
	if len(placeholderHTML) == 0 {
		t.Fatal("the embedded placeholder is empty")
	}
	if !bytes.Contains(placeholderHTML, []byte("<!doctype html>")) {
		t.Error("the placeholder is not an HTML document")
	}
	if !bytes.Contains(placeholderHTML, []byte("make build-web")) {
		t.Error("the placeholder does not name the command that builds the editor")
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
	handler := newHandler(spaTree(), placeholderHTML)

	paths := []string{
		"/app/workflows/wf_123",
		"/embed/wf_123",
		"/settings/credentials",
		"/deeply/nested/unknown/route",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 via SPA fallback", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "spa-document") {
			t.Errorf("GET %s did not serve the SPA document", path)
		}
	}
}

// A clone with no SPA built is the one state a fresh checkout is in, and it is
// the state the editor must not answer with a blank page.
func TestHandlerServesPlaceholderWhenTheSPAWasNeverBuilt(t *testing.T) {
	handler := newHandler(fstest.MapFS{".gitkeep": {Data: []byte{}}}, placeholderHTML)

	for _, path := range []string{"/", "/app/workflows/wf_123", "/embed/wf_123"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != htmlContentType {
			t.Errorf("GET %s Content-Type = %q, want %q", path, got, htmlContentType)
		}
		if !strings.Contains(rec.Body.String(), "make build-web") {
			t.Errorf("GET %s did not serve the placeholder", path)
		}
	}
}

// A missing hashed asset used to be answered with the SPA document and a 200,
// which a browser reports as a MIME error instead of fetching the new chunk.
func TestHandlerAnswersMissingAssetsWithNotFound(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	paths := []string{
		"/_app/immutable/entry/start.gone.js",
		"/_app/immutable/chunks/data.json",
		"/vendor/missing-bundle.js",
		"/robots.txt",
		"/icons/logo.svg",
	}

	for _, path := range paths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s was served the SPA document", path)
		}
	}
}

// An unknown /api path used to fall through to the SPA and answer 200 text/html,
// which a script or an agent takes for success and then fails parsing. Every
// method gets the problem document: a POST to a path nothing serves is the same
// mistake, and the SPA's "405, allow GET" would misdirect it.
func TestHandlerAnswersUnknownAPIPathsWithAProblem(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/nonexistent"},
		{http.MethodGet, "/api/v2/foo"},
		{http.MethodGet, "/api/v1/schedules/sched_x"},
		{http.MethodGet, "/api"},
		{http.MethodGet, "/api/"},
		{http.MethodGet, "//api/v1/nonexistent"},
		{http.MethodHead, "/api/v1/nonexistent"},
		{http.MethodPost, "/api/v1/nonexistent"},
		{http.MethodDelete, "/api/v2/foo"},
	}

	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(testCase.method, testCase.path, nil))

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := rec.Header().Get("Allow"); got != "" {
				t.Errorf("Allow = %q, want none: no method is allowed on a path nothing serves", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want the hardening headers kept", got)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got == "" {
				t.Error("no Content-Security-Policy on the problem response")
			}
			if testCase.method == http.MethodHead {
				return
			}

			var problem struct {
				Title  string `json:"title"`
				Status int    `json:"status"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("the body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if problem.Title != "Not Found" || problem.Status != http.StatusNotFound || problem.Detail == "" {
				t.Errorf("problem = %+v, want title Not Found, status 404 and a detail", problem)
			}
		})
	}
}

// The API's prefix is claimed as a path segment, not as a string prefix: a
// client route or an asset that merely starts with the letters keeps its old
// answer, as does everything the API rule sits in front of.
func TestHandlerKeepsTheSPAAnswersOutsideTheAPIPrefix(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	for _, path := range []string{"/workflows", "/apiary", "/apis/v1", "/app/workflows/wf_1", "/embed/wf_1"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 via SPA fallback", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != htmlContentType {
			t.Errorf("GET %s Content-Type = %q, want %q", path, got, htmlContentType)
		}
		if !strings.Contains(rec.Body.String(), "spa-document") {
			t.Errorf("GET %s did not serve the SPA document", path)
		}
	}

	// A missing asset is still a plain 404, not a problem document: it is a
	// browser's request, and the api rule must not reach it.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/gone.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing .js = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); strings.Contains(got, "problem+json") {
		t.Errorf("missing .js Content-Type = %q, want the plain 404", got)
	}
}

// Hashed assets are the reason the SPA loads quickly on a second visit, and
// the document is the reason an upgraded server is not served from a stale
// cache. The two rules are opposites, so they are asserted together.
func TestHandlerCachePolicySeparatesHashedAssetsFromTheDocument(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	cases := map[string]string{
		"/":                                  "no-cache",
		"/_app/immutable/entry/start.abc.js": "public, max-age=31536000, immutable",
		"/_app/immutable/assets/app.abc.css": "public, max-age=31536000, immutable",
		"/vendor/scalar.js":                  "public, max-age=3600",
	}

	for path, want := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, got, want)
		}
	}
}

// The editor's JavaScript is ~770 KB raw. A client that offers gzip must get
// gzip, and the bytes must survive the round trip.
func TestHandlerCompressesWhenTheClientAsks(t *testing.T) {
	body := bytes.Repeat([]byte("const averylongidentifier = 1;\n"), 200)
	tree := fstest.MapFS{
		"index.html":                        {Data: []byte("spa-document")},
		"_app/immutable/entry/start.abc.js": {Data: body},
	}
	handler := newHandler(tree, placeholderHTML)

	request := httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/start.abc.js", nil)
	request.Header.Set("Accept-Encoding", "br, gzip;q=0.8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Content-Type"); got != jsContentType {
		t.Errorf("Content-Type = %q, want %q", got, jsContentType)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to name Accept-Encoding", rec.Header().Get("Vary"))
	}

	reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("response is not gzip: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the compressed body: %v", err)
	}
	if !bytes.Equal(decoded, body) {
		t.Fatal("the decoded body differs from the file")
	}
}

func TestHandlerDoesNotCompressWhenNotAsked(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 8000)
	handler := newHandler(fstest.MapFS{
		"index.html":                        {Data: []byte("spa-document")},
		"_app/immutable/entry/start.abc.js": {Data: body},
	}, placeholderHTML)

	for _, encoding := range []string{"", "br", "gzip;q=0", "identity"} {
		request := httptest.NewRequest(http.MethodGet, "/_app/immutable/entry/start.abc.js", nil)
		if encoding != "" {
			request.Header.Set("Accept-Encoding", encoding)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Accept-Encoding %q: Content-Encoding = %q, want none", encoding, got)
		}
		if int64(len(rec.Body.Bytes())) != int64(len(body)) {
			t.Errorf("Accept-Encoding %q: served %d bytes, want %d", encoding, rec.Body.Len(), len(body))
		}
	}
}

// Revalidation is what makes the editor reload cheap after an upgrade: the
// document is re-fetched as a 304 rather than downloaded again.
func TestHandlerRevalidatesWithETag(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("the response carries no ETag")
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("a 304 carried %d bytes of body", second.Body.Len())
	}
	if second.Header().Get("ETag") != etag {
		t.Errorf("304 ETag = %q, want %q", second.Header().Get("ETag"), etag)
	}

	// And a changed file must not validate against the old tag.
	other := newHandler(fstest.MapFS{"index.html": {Data: []byte("spa-document-v2")}}, placeholderHTML)
	stale := httptest.NewRecorder()
	otherRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	otherRequest.Header.Set("If-None-Match", etag)
	other.ServeHTTP(stale, otherRequest)

	if stale.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a changed document", stale.Code)
	}
}

func TestHandlerAnswersOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
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

// spaTree is a distilled build tree: the document, one hashed script, one
// hashed stylesheet and one vendored bundle — enough to exercise every branch
// the handler takes.
func spaTree() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                        {Data: []byte("spa-document")},
		"_app/immutable/entry/start.abc.js": {Data: []byte("export const start = 1;")},
		"_app/immutable/assets/app.abc.css": {Data: []byte("body { color: red; }")},
		"vendor/scalar.js":                  {Data: []byte("scalar")},
		"favicon.svg":                       {Data: []byte("<svg/>")},
	}
}

// The dashboard is one origin's own page: anything else that frames it is
// framing a session-bearing UI it must not see.
func TestHandlerRefusesToBeFramedOffOrigin(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML, WithFrameAncestors([]string{"https://host.example"}))

	for _, path := range []string{"/", "/app/workflows/wf_1", "/settings/credentials"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
			t.Errorf("GET %s Content-Security-Policy = %q, want frame-ancestors 'self'", path, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
			t.Errorf("GET %s X-Frame-Options = %q, want SAMEORIGIN", path, got)
		}
	}
}

// /embed/ exists to be framed, but only by the hosts the deployment approved.
// The legacy header is omitted rather than sent: it cannot name a list, so any
// value would contradict the policy a browser actually enforces.
func TestHandlerFramesTheEmbedRouteOnlyForAllowlistedOrigins(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML,
		WithFrameAncestors([]string{"https://host.example", "HTTPS://Partner.Example/"}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed/wf_1", nil))

	want := "frame-ancestors 'self' https://host.example https://partner.example"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("Content-Security-Policy = %q, want %q", got, want)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q, want the header omitted on the embed route", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); strings.Contains(got, "evil.example") {
		t.Errorf("a disallowed origin reached the policy: %q", got)
	}
}

// Nothing is configured on an instance that never embeds, and the embed route
// must still not be framable by a stranger.
func TestHandlerWithoutConfiguredOriginsFramesNothing(t *testing.T) {
	for _, handler := range []http.Handler{
		newHandler(spaTree(), placeholderHTML),
		newHandler(spaTree(), placeholderHTML, WithFrameAncestors(nil)),
		Handler(),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed/wf_1", nil))

		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
			t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'self'", got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "" {
			t.Errorf("X-Frame-Options = %q, want the header omitted on the embed route", got)
		}
	}
}

// A configuration entry that is not an origin is dropped rather than written
// into the policy, where it would be a source no browser matches or a parse
// error that discards the directive for the real hosts too.
func TestHandlerDropsUnusableFrameAncestorEntries(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML,
		WithFrameAncestors([]string{"", "   ", "not an origin", "ftp://host.example", "javascript:alert(1)", "https://host.example"}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/embed/wf_1", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self' https://host.example" {
		t.Errorf("Content-Security-Policy = %q, want only the usable origin", got)
	}
}

// nosniff and the referrer policy belong to every response, not only to the
// document: a sniffed asset is how an uploaded or served file becomes script,
// and the referrer is what leaks a workspace path to a third-party asset host.
func TestHandlerHardensEveryResponse(t *testing.T) {
	handler := newHandler(spaTree(), placeholderHTML)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"document", http.MethodGet, "/"},
		{"hashed asset", http.MethodGet, "/_app/immutable/entry/start.abc.js"},
		{"vendored bundle", http.MethodGet, "/vendor/scalar.js"},
		{"placeholder fallback", http.MethodGet, "/deeply/nested/unknown/route"},
		{"missing asset", http.MethodGet, "/_app/immutable/entry/gone.js"},
		{"method refusal", http.MethodPost, "/"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(testCase.method, testCase.path, nil))

			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
				t.Errorf("Referrer-Policy = %q", got)
			}
			if rec.Header().Get("Content-Security-Policy") == "" {
				t.Error("no frame-ancestors policy on a response from the web handler")
			}
		})
	}
}
