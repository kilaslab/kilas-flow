package middleware

import (
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// CORS preflight and response vocabulary. The methods and headers are fixed
// rather than derived from the route table: this middleware is mounted once,
// before the router, so it has no operation to ask, and the API's surface is
// small enough that listing it costs nothing.
const (
	// corsMethods is the method list a browser is told it may use.
	corsMethods = "GET, POST, PUT, DELETE, OPTIONS"

	// corsDefaultHeaders is what a preflight is answered with when the browser
	// did not name its own headers. Authorization and Content-Type are the two
	// every credentialed JSON call sends; X-KilasFlow-Embed is the embed
	// session header, and Last-Event-ID is resent by EventSource when a stream
	// reconnects.
	corsDefaultHeaders = "Authorization, Content-Type, X-KilasFlow-Embed, Last-Event-ID"

	// corsExposedHeaders are the response headers a cross-origin script may
	// read. Both are deliberately not sensitive: the request ID correlates a
	// failure with a log line, and the next cursor is the pagination position.
	corsExposedHeaders = "X-Request-ID, X-Next-Cursor"

	// corsMaxAge is how long a browser may cache the preflight result. Ten
	// minutes keeps a busy embed page from re-preflighting every request while
	// still picking up a changed allowlist within a coffee break.
	corsMaxAge = "600"
)

// CORS answers the browser's cross-origin checks for an allowlisted origin.
//
// Why it exists at all: a host page embeds the editor in an iframe and opens an
// EventSource against the API, and the browser refuses both without these
// headers — silently, as far as the server is concerned, because the request
// itself arrives and is served. For a single-use stream ticket that is worse
// than a refusal: the ticket is spent on a response the browser throws away, so
// the host's next attempt fails too. The preflight has the same shape — an
// OPTIONS request carries no credential by definition, so the auth gate used to
// answer it with a 401 the browser reports as a CORS failure and never
// retries.
//
// What it deliberately does not do:
//
//   - It never sends Access-Control-Allow-Credentials. Enabling that would let
//     a host page's script read responses made with the operator's session
//     cookie, turning any allowlisted integration into a cross-origin read of
//     the whole workspace. The design is bearer and ticket based, so nothing
//     here needs cookies.
//   - It never reflects an origin outside the allowlist and never answers "*".
//     Reflecting an arbitrary origin with credentials disabled is still an
//     information leak for public endpoints, and it makes the allowlist a
//     comment rather than a control.
//   - It does not send the headers on a request with no Origin. Same-origin
//     fetches and server-to-server callers get no CORS headers, which is
//     correct: there is no browser policy to satisfy.
//
// The middleware is a pure passthrough for everything that is not an allowlisted
// cross-origin request, including an unknown route, so mounting it first costs
// one map lookup per request and changes nothing else.
//
// prefix is the surface this layer is justified by, and it is required rather
// than optional: the same mux carries the public webhook surface and the SPA,
// and neither is a cross-origin API. Mounted on the whole mux the layer also
// decorated those responses, widening a control whose only reason to exist is
// the event stream a host page opens against the API.
func CORS(allowedOrigins []string, prefix string) func(http.Handler) http.Handler {
	// Normalise once, at mount time rather than per request: the allowlist is
	// static for the life of the process, and re-parsing a URL for every
	// request would put that work on the hot path for no benefit.
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if normalized := embed.NormalizeOrigin(origin); normalized != "" {
			allowed[normalized] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if prefix != "" && !strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if normalized := embed.NormalizeOrigin(origin); normalized == "" || !allowed[normalized] {
				next.ServeHTTP(w, r)
				return
			}

			header := w.Header()
			// The header value is the origin as the browser sent it, not its
			// normalised form: the browser compares the two byte for byte, and
			// answering a lowercased origin is a mismatch it reports as a CORS
			// failure.
			header.Set("Access-Control-Allow-Origin", origin)
			// The response varies by Origin even when the origins allowed are
			// the same, because a disallowed one gets no headers at all. Without
			// this an intermediary caches the headerless answer and serves it to
			// the origin that is allowed.
			header.Set("Vary", "Origin")
			header.Set("Access-Control-Allow-Methods", corsMethods)
			header.Set("Access-Control-Allow-Headers", requestedHeaders(r))
			header.Set("Access-Control-Expose-Headers", corsExposedHeaders)
			header.Set("Access-Control-Max-Age", corsMaxAge)

			// A preflight is a question about permission, never a request for
			// the resource: it stops here with no body so the auth gate never
			// sees it, and so a stream ticket is not spent by one.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requestedHeaders echoes the header list a preflight asks about, falling back
// to the default when the browser named none.
//
// Echoing rather than answering with a fixed superset: the browser refuses a
// response whose allowed list omits a header the request named, and it compares
// the names case-insensitively but expects the ones it asked for. The value is
// only ever a header-name list — it reaches no parser and no header of the
// response but this one — and it is echoed only for an allowlisted origin.
func requestedHeaders(r *http.Request) string {
	requested := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers"))
	if requested == "" {
		return corsDefaultHeaders
	}
	return requested
}
