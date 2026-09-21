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
func EmbedAuth(verifier EmbedVerifier) func(http.Handler) http.Handler {
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
			if origin := r.Header.Get("Origin"); origin != "" && !session.MatchesOrigin(origin) {
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
