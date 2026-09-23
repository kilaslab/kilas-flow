package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

type embedContextKey struct{}

// EmbedSessionFrom returns the verified session a request is scoped to.
func EmbedSessionFrom(ctx context.Context) (embed.Session, bool) {
	session, found := ctx.Value(embedContextKey{}).(embed.Session)
	return session, found
}

// EmbedVerifier verifies a token. It is an interface so the middleware can be
// mounted on a deployment that has no embed key configured.
type EmbedVerifier interface {
	Verify(token string) (embed.Session, error)
}

// EmbedAuth enforces an embed token when one is present.
//
// A request without a token passes through untouched: the internal dashboard
// is unaffected. A request *with* one is confined to that session's workflow
// and scopes, so a token that leaked out of an iframe is useless for anything
// but the one workflow it was minted for.
//
// publicURL is server.public_url. It says where browsers load the editor
// iframe from, which is the origin the iframe's own requests carry.
func EmbedAuth(verifier EmbedVerifier, publicURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := embedToken(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			if verifier == nil {
				deny(w, http.StatusServiceUnavailable, "Embed sessions are not configured on this instance.")
				return
			}

			session, err := verifier.Verify(token)
			if err != nil {
				// Expired, forged, and malformed all answer the same way: a
				// caller learns only that the token is unusable.
				deny(w, http.StatusUnauthorized, "The embed session is not valid.")
				return
			}
			// The origin is re-checked on every request, not only when the
			// session was minted: a token copied into another page must stop
			// working there.
			if !originPermitted(r, session, publicURL) {
				deny(w, http.StatusForbidden, "This embed session is not allowed from that origin.")
				return
			}
			if status, detail := permits(sessionSubject{session: session}, r); status != 0 {
				deny(w, status, detail)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), embedContextKey{}, session)))
		})
	}
}

// originPermitted says whether a request may spend this session from where it
// was sent.
//
// A request with no Origin passes, because a browser omits it on a same-origin
// GET and the editor's reads are exactly that. The host page the session was
// minted for passes by its own origin.
//
// The editor iframe is the third caller, and the awkward one. It is served by
// this instance, so its saves and runs carry this instance's origin and not the
// host's. That origin cannot pass on its own: any page on the embed allowlist
// may frame the editor, and one of them holding another host's token would
// then spend it freely. So the frame also sends X-KilasFlow-Embed-Parent, the
// origin of the page it completed the handshake with, read from the
// browser-verified event.origin of the session message. Any page can send the
// header, but only a document on this instance's origin can send it alongside
// this instance's Origin, and the header must name the session's host.
//
// Sec-Fetch-Site, when the browser sends it, must say same-origin: a request
// claiming the editor's origin from any other context is not the editor's.
// Browsers send that header only to secure origins, so it is checked when
// present and never required.
func originPermitted(r *http.Request, session embed.Session, publicURL string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || session.MatchesOrigin(origin) {
		return true
	}
	self := embed.SelfOrigin(r, publicURL)
	if self == "" || embed.NormalizeOrigin(origin) != self {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	return session.MatchesOrigin(r.Header.Get("X-KilasFlow-Embed-Parent"))
}

func embedToken(r *http.Request) string {
	if header := strings.TrimSpace(r.Header.Get("X-KilasFlow-Embed")); header != "" {
		return header
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if after, found := strings.CutPrefix(authorization, "Bearer "); found {
		// Only a KilasFlow embed token is claimed here; anything else is left
		// for a future auth scheme rather than swallowed.
		if strings.HasPrefix(strings.TrimSpace(after), "kfe1.") {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

func deny(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}
