// Package web embeds the compiled SvelteKit SPA into the kilasflow binary and
// serves it with history-API fallback, so a single binary ships both the API
// and the editor.
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	// Aliased because this file also imports the standard library's "embed",
	// which is what carries the SPA into the binary. The two are unrelated: the
	// stdlib package is a compiler directive, this one is the iframe session
	// allowlist.
	kembed "github.com/kilaslabs/kilas-flow/internal/embed"
)

// assets holds the built SPA.
//
// The "all:" prefix is required, not cosmetic: without it go:embed skips every
// path whose name begins with "_" or ".", and SvelteKit emits all of its
// JavaScript and CSS into an "_app" directory. A plain "dist" or "dist/*"
// pattern compiles without complaint and produces a binary that serves
// index.html with no assets behind it.
//
// dist/ belongs to the frontend build, and the only file tracked under it is
// .gitkeep. A tracked index.html placeholder used to sit there too, which meant
// `make build-web` rewrote a committed file: the working tree went dirty and
// every build was stamped "-dirty" by `git describe --dirty`. What a fresh
// clone serves instead is placeholderHTML below, so a bare `go build` still
// produces a binary that explains itself.
//
//go:embed all:dist
var assets embed.FS

// placeholderHTML is the page served when the SPA has never been built — a
// fresh clone, or a binary built without `make build-web`. It lives outside
// dist/ precisely because the frontend build owns that directory.
//
//go:embed placeholder/index.html
var placeholderHTML []byte

const (
	// indexName is the SPA document, at the root of dist.
	indexName = "index.html"

	// immutablePrefix is where SvelteKit writes content-addressed assets: the
	// name of every file below it changes when its content changes, so it can
	// be cached for a year. That is worth having: the editor's JavaScript is
	// roughly 770 KB, of which gzip removes about two thirds.
	immutablePrefix = "_app/"

	// gzipMinBytes is the size below which compressing a file costs more CPU
	// than it saves on the wire.
	gzipMinBytes = 1400

	// jsContentType is forced rather than looked up, because the system MIME
	// database disagrees with itself and with browsers across platforms:
	// .js resolves to text/javascript on one machine and application/javascript
	// on the next, and a mismatch is exactly the failure that makes a served
	// module refuse to execute.
	jsContentType = "text/javascript; charset=utf-8"

	// htmlContentType is what the SPA document and the placeholder are served
	// as, in both cases with the charset a browser needs to read them.
	htmlContentType = "text/html; charset=utf-8"

	// embedPrefix is the route family the iframe editor is served from. It is
	// the one route a foreign page is meant to frame, which is why the framing
	// policy below is chosen by prefix rather than sent uniformly.
	embedPrefix = "/embed/"

	// referrerPolicy keeps the workspace's own paths out of the Referer a
	// third-party asset receives, while still sending the origin to the API's
	// own hosts (strict-origin-when-cross-origin).
	referrerPolicy = "strict-origin-when-cross-origin"
)

// HandlerOption customises the SPA handler at construction.
type HandlerOption func(*handlerOptions)

// handlerOptions is the hardening configuration the handler is built with.
//
// It exists because the only knob that cannot be a constant — which hosts may
// frame /embed/* — lives in deployment configuration the web package never
// sees, so the composition root passes it in rather than this package reaching
// for a global.
type handlerOptions struct {
	// frameAncestors are the normalised origins allowed to frame /embed/*,
	// the deployment's embed allowlist.
	frameAncestors []string
}

// WithFrameAncestors lists the host origins allowed to frame the embed route.
//
// The dashboard is never frameable by another site, but /embed/ is framable by
// exactly the hosts the operator approved, and the CSP's frame-ancestors is the
// only directive that can express that list. X-Frame-Options cannot — it takes
// one of three fixed values and predates origin lists — so it is omitted on
// that route rather than sent with a value that would contradict the policy.
//
// Origins are normalised through kembed.NormalizeOrigin, which drops anything
// that is not scheme://host[:port]. Dropping is deliberate: a malformed source
// written into a CSP is at best a source no browser matches, and at worst a
// parse error that discards the whole directive and leaves the page unframed by
// nobody.
func WithFrameAncestors(origins []string) HandlerOption {
	return func(options *handlerOptions) {
		options.frameAncestors = make([]string, 0, len(origins))
		for _, origin := range origins {
			if normalized := kembed.NormalizeOrigin(origin); normalized != "" {
				options.frameAncestors = append(options.frameAncestors, normalized)
			}
		}
	}
}

// FS returns the embedded SPA rooted at the dist directory.
func FS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}

// Handler serves the embedded SPA.
//
// Requests for files that exist are served directly; anything else falls back
// to the SPA document so client-side routes such as /app/workflows/:id survive
// a hard refresh or a deep link.
func Handler(options ...HandlerOption) http.Handler {
	sub, err := FS()
	if err != nil {
		// Only reachable if the embedded tree is malformed, which is a build-time
		// property of this package rather than a runtime condition.
		panic("web: cannot open embedded SPA: " + err.Error())
	}

	return newHandler(sub, placeholderHTML, options...)
}

// newHandler is Handler with its tree and its placeholder injected, so the
// tests can drive both states — a built SPA and a fresh clone — without a build.
func newHandler(sub fs.FS, placeholder []byte, options ...HandlerOption) http.Handler {
	settings := handlerOptions{}
	for _, option := range options {
		option(&settings)
	}
	security := newSecurityHeaders(settings)

	spa, _ := loadAsset(sub, indexName)
	fallback := &asset{
		body:        placeholder,
		contentType: htmlContentType,
		etag:        etagFor(placeholder),
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		security.apply(w.Header(), isEmbedPath(r.URL.Path))

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := requestName(r.URL.Path)

		entry, err := loadAsset(sub, name)
		switch {
		case err == nil:
			entry.serve(w, r, cacheControlFor(name))

		// The document itself is absent, so this binary carries no SPA at all.
		case name == indexName:
			fallback.serve(w, r, cacheControlFor(indexName))

		// A missing asset is answered as one. Serving the SPA document here — a
		// 200 text/html for a .js request — is how a stale tab after an upgrade
		// turns into a MIME error instead of a reload it can recover from.
		case isAssetPath(name):
			http.NotFound(w, r)

		case spa != nil:
			spa.serve(w, r, cacheControlFor(indexName))

		default:
			fallback.serve(w, r, cacheControlFor(indexName))
		}
	})
}

// securityHeaders is the hardening every response from this handler carries.
//
// One value is built per handler rather than per request: the policy is a
// function of configuration that cannot change while the process runs, and
// rebuilding the same string on every asset request would spend an allocation
// on the hot path for nothing.
type securityHeaders struct {
	// dashboardPolicy and embedPolicy are the frame-ancestors directives the
	// two route families are served with.
	dashboardPolicy string
	embedPolicy     string
}

// newSecurityHeaders builds the policies for one handler configuration.
func newSecurityHeaders(settings handlerOptions) securityHeaders {
	// 'self' is in both lists. On the dashboard it is the whole policy: the
	// operator's own pages may frame it, and framing it from anywhere else is
	// what turns a destructive button into a clickjacking target. On the embed
	// route it lets the dashboard preview the iframe the host page will show,
	// which is a same-origin frame and needs no allowlist entry.
	embedPolicy := frameAncestorsSelf
	if len(settings.frameAncestors) > 0 {
		embedPolicy += " " + strings.Join(settings.frameAncestors, " ")
	}
	return securityHeaders{dashboardPolicy: frameAncestorsSelf, embedPolicy: embedPolicy}
}

// frameAncestorsSelf is the source list that permits only the page's own
// origin to frame it.
const frameAncestorsSelf = "frame-ancestors 'self'"

// apply writes the hardening headers a response is served with.
//
// Which framing policy applies depends on the route, and the legacy
// X-Frame-Options header is sent only where it agrees with the CSP: it cannot
// express an origin list, so on the embed route it would either be ignored by a
// browser that honours frame-ancestors or — worse — be honoured by one that
// does not and refuse the frames the allowlist permits.
func (headers securityHeaders) apply(header http.Header, embed bool) {
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", referrerPolicy)

	if embed {
		header.Set("Content-Security-Policy", headers.embedPolicy)
		return
	}
	header.Set("Content-Security-Policy", headers.dashboardPolicy)
	header.Set("X-Frame-Options", "SAMEORIGIN")
}

// isEmbedPath reports whether a request path is the iframe editor route.
//
// A prefix test rather than a router match because this handler is the SPA's
// catch-all: every client route arrives here as a path, and only the embed
// family is framable by another origin.
func isEmbedPath(urlPath string) bool {
	return urlPath == strings.TrimSuffix(embedPrefix, "/") || strings.HasPrefix(urlPath, embedPrefix)
}

// requestName maps a request path to a name inside the embedded tree.
//
// The result cannot escape that tree: path.Clean resolves every "." and ".."
// component before the leading separator is dropped, so a request for
// /../../go.mod becomes the bare name "go.mod".
func requestName(urlPath string) string {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || name == "." {
		return indexName
	}
	return name
}

// isAssetPath reports whether a name that is not in the tree is asset-like, and
// must therefore 404 rather than receive the SPA document. SvelteKit's own
// assets and vendored bundles are here for their prefixes, everything else for
// its extension: a client-side route never ends in one.
func isAssetPath(name string) bool {
	return strings.HasPrefix(name, immutablePrefix) ||
		strings.HasPrefix(name, "vendor/") ||
		path.Ext(name) != ""
}

// cacheControlFor is the freshness a name is served with.
func cacheControlFor(name string) string {
	switch {
	case name == indexName:
		// The document names the hashed assets, so it is the one file that must
		// be revalidated: a tab holding a cached copy after an upgrade would
		// reference chunks the new binary no longer has. Revalidating costs a
		// 304 body of a few bytes.
		return "no-cache"

	case strings.HasPrefix(name, immutablePrefix):
		return "public, max-age=31536000, immutable"

	default:
		// favicon.svg, vendor/scalar.js and anything else without a content
		// hash in its name: worth caching, not worth a year of it.
		return "public, max-age=3600"
	}
}

// asset is one servable file. Everything derived from the bytes — its entity
// tag and its compressed body — is computed at most once, because the embedded
// tree is immutable for the life of the process.
type asset struct {
	body        []byte
	contentType string
	etag        string

	gzipOnce sync.Once
	gzipped  []byte
}

// loadAsset reads one name out of the embedded tree. A missing name or a
// directory is an error, and errors are deliberately not cached: the name comes
// from the request, so a cache that remembered every miss would be a memory
// leak with a reachable key.
func loadAsset(sub fs.FS, name string) (*asset, error) {
	info, err := fs.Stat(sub, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fs.ErrNotExist
	}

	body, err := fs.ReadFile(sub, name)
	if err != nil {
		return nil, err
	}

	return &asset{
		body:        body,
		contentType: contentTypeFor(name, body),
		etag:        etagFor(body),
	}, nil
}

// contentTypeFor resolves a name the way a browser caching a module needs it
// resolved: the two extensions SvelteKit relies on are pinned, and everything
// else falls back to the system MIME database and then to sniffing.
func contentTypeFor(name string, body []byte) string {
	ext := path.Ext(name)
	if ext == ".js" || ext == ".mjs" {
		return jsContentType
	}
	if ext == ".html" {
		return htmlContentType
	}
	if byExtension := mime.TypeByExtension(ext); byExtension != "" {
		return byExtension
	}
	return http.DetectContentType(body)
}

// serve writes the asset, negotiating compression and answering a conditional
// request without reading the file again.
func (a *asset) serve(w http.ResponseWriter, r *http.Request, cacheControl string) {
	header := w.Header()
	header.Set("Content-Type", a.contentType)
	header.Set("Cache-Control", cacheControl)

	// Only an asset that could be sent compressed varies by Accept-Encoding.
	negotiable := len(a.body) >= gzipMinBytes && compressibleType(a.contentType)
	if negotiable {
		addVary(header, "Accept-Encoding")
	}

	body, etag := a.body, a.etag
	if negotiable && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		if compressed := a.gzip(); compressed != nil {
			body = compressed
			// The tag names the representation rather than the file, because a
			// validated cache entry and a compressed body have to agree.
			etag += "-gzip"
			header.Set("Content-Encoding", "gzip")
		}
	}
	header.Set("ETag", etag)

	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		header.Del("Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}

	header.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(body); err != nil {
		// The client hung up — a page being reloaded, most often. There is
		// nothing to recover and nothing to report.
		return
	}
}

// gzip compresses the body once, keeping the result because the embedded tree
// never changes. A nil return means the compression failed and the caller
// serves the file as it is.
func (a *asset) gzip() []byte {
	a.gzipOnce.Do(func() {
		var buf bytes.Buffer
		writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return
		}
		if _, err := writer.Write(a.body); err != nil {
			return
		}
		if err := writer.Close(); err != nil {
			return
		}
		a.gzipped = buf.Bytes()
	})
	return a.gzipped
}

// etagFor is the entity tag of one representation: long enough that a stale
// entry is never validated against changed bytes, short enough to send on every
// request.
func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// matchesETag reports whether an If-None-Match header names this tag. A weak
// validator from the client matches a strong one from the server, which is the
// comparison RFC 9110 asks for.
func matchesETag(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// acceptsGzip reads the quality values out of Accept-Encoding. Absent header,
// or an explicit q=0, means no: a client that did not ask does not get it.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		encoding := strings.TrimSpace(fields[0])
		if !strings.EqualFold(encoding, "gzip") && encoding != "*" {
			continue
		}
		for _, parameter := range fields[1:] {
			name, value, ok := strings.Cut(parameter, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			if quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && quality == 0 {
				return false
			}
		}
		return true
	}
	return false
}

// compressibleType reports whether compressing a media type is worth trying.
// Images, fonts and archives are already compressed, and running them through
// gzip spends CPU to make them slightly larger.
func compressibleType(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")
	base = strings.ToLower(strings.TrimSpace(base))
	if strings.HasPrefix(base, "text/") {
		return true
	}
	switch base {
	case "application/json", "application/javascript", "application/xml",
		"application/manifest+json", "image/svg+xml", "application/xhtml+xml":
		return true
	}
	return strings.HasSuffix(base, "+json") || strings.HasSuffix(base, "+xml")
}

// addVary appends a field to Vary unless it is already there, so a CORS
// middleware that set the header earlier is added to rather than replaced.
func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, field := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(field), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
