package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/embed"
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
			if allowed, reason := permits(session, r); !allowed {
				deny(w, http.StatusForbidden, reason)
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

// permits decides whether one request is inside a session's authority.
//
// The rule is deliberately about *what the request targets*, not about which
// handler will run: an embed session is confined to one workflow and its
// executions, and everything else is refused by default rather than
// enumerated as forbidden.
func permits(session embed.Session, r *http.Request) (bool, string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")

	switch {
	case path == "/node-types" || strings.HasPrefix(path, "/node-types/"):
		// The editor cannot render without the node catalogue, and the
		// catalogue itself carries no tenant data.
		//
		// The load-options endpoint under this prefix does, so it is not
		// covered by that reasoning: it can reach a customer's service, and an
		// internal loader reads this process's own state. The handler bounds it
		// by the session's WorkflowID rather than only its tenant, because
		// nothing here checks a workflow — see LoadOptions.
		return session.Allows(embed.ScopeRead), "This embed session cannot read."

	case path == "/credentials" && r.Method == http.MethodGet:
		// Credential *names* are needed to render a node's credential picker.
		// Values are never returned by this endpoint.
		return session.Allows(embed.ScopeRead), "This embed session cannot read."

	case path == "/workflows/import":
		// Importing creates a *new* workflow, which is outside any session's
		// single-workflow authority.
		return false, "An embed session cannot import workflows."

	case strings.HasPrefix(path, "/workflows/"):
		rest := strings.TrimPrefix(path, "/workflows/")
		workflowID, action, _ := strings.Cut(rest, "/")
		if workflowID != session.WorkflowID {
			return false, "This embed session is scoped to a different workflow."
		}
		switch {
		case action == "run":
			return session.Allows(embed.ScopeRun), "This embed session cannot run workflows."
		case action == "activate" || action == "deactivate":
			// Activation publishes a webhook endpoint for the whole
			// deployment; that is an owner action, not an embed one.
			return false, "An embed session cannot change activation."
		case r.Method == http.MethodGet:
			return session.Allows(embed.ScopeRead), "This embed session cannot read."
		case r.Method == http.MethodDelete:
			return false, "An embed session cannot delete a workflow."
		default:
			return session.Allows(embed.ScopeWrite), "This embed session is read-only."
		}

	case path == "/executions":
		// A listing must be narrowed to this session's own workflow. Without
		// this, an embedded editor could page through every execution in the
		// tenant, including workflows it was never granted.
		if !session.Allows(embed.ScopeRead) {
			return false, "This embed session cannot read executions."
		}
		if r.URL.Query().Get("workflowId") != session.WorkflowID {
			return false, "An embed session must list executions of its own workflow."
		}
		return true, ""

	case strings.HasPrefix(path, "/executions/"):
		// Which workflow a single execution belongs to is only knowable by
		// loading it, so the ownership check lives in the handler. This gate
		// covers the scope; handlers.RequireEmbedWorkflow covers the identity.
		return session.Allows(embed.ScopeRead), "This embed session cannot read executions."

	case path == "/resume" || strings.HasPrefix(path, "/resume/"):
		// Approval resume is never available to embedded sessions: resuming
		// someone else's approval from inside a host page is the
		// confused-deputy shape the session restriction exists to stop. The
		// resume handler and the service repeat this denial in depth, so a
		// denied call never consumes its token either way.
		return false, "An embed session cannot answer an approval."
	default:
		// Listing every workflow, minting another session, managing schedules
		// or credentials: none of that belongs to an embedded editor.
		return false, "An embed session cannot use this endpoint."
	}
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
