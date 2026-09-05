package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// KeyAuthenticator resolves a presented API key to the row that owns it.
//
// Narrower than repository.AuthRepository on purpose: this is the only identity
// question the request path asks, so it is the only one a test double has to
// answer.
type KeyAuthenticator interface {
	AuthenticateAPIKey(ctx context.Context, token string) (repository.APIKey, error)
}

// AuthOptions configures the authentication gate.
type AuthOptions struct {
	// Enabled turns the gate on. A disabled gate admits everything and stores
	// no principal, which is the behaviour every installation had before
	// identity existed.
	Enabled bool
	// Keys resolves API keys. Nil refuses every key rather than admitting one.
	Keys KeyAuthenticator
	// Sessions verifies browser sessions and spends stream tickets. Nil refuses
	// both.
	Sessions *auth.Issuer
	// CookieName is where the browser session lives. Empty falls back to the
	// secure __Host- name.
	CookieName string
	// APIPrefix is the only prefix this gate covers.
	APIPrefix string
}

// publicOperations are the API paths served without a credential.
//
// Health and readiness are here because an orchestrator probes them before any
// account exists, and login because presenting the password is the whole point
// of it. Logout is public because clearing a cookie needs no proof of anything,
// and refusing it would strand a browser holding a session the server no longer
// recognises.
//
// The OpenAPI document and the docs page are not listed because they are served
// outside the API prefix and this gate never sees them.
var publicOperations = map[string]bool{
	"/health":      true,
	"/ready":       true,
	"/auth/login":  true,
	"/auth/logout": true,
}

// Authenticate refuses an API request that carries no identity.
//
// It is scoped to the API prefix rather than mounted over the whole router. The
// router is flat — the webhook prefix and the SPA's static assets hang off the
// same mux — so a gate that answered every path would demand a credential for
// an inbound webhook delivery, which by definition has none, and for the login
// page's own JavaScript.
//
// What it does not do: it neither rate-limits nor locks an account out after
// repeated failures, so it is no defence against an attacker working through
// passwords. It also does not bind a session to a device or an address, so a
// stolen cookie works from anywhere until it expires.
func Authenticate(opts AuthOptions) func(http.Handler) http.Handler {
	prefix := opts.APIPrefix
	cookieName := opts.CookieName
	if cookieName == "" {
		cookieName = auth.SessionCookieName
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !opts.Enabled || prefix == "" || !strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			operation := strings.TrimPrefix(r.URL.Path, prefix)
			if publicOperations[operation] {
				next.ServeHTTP(w, r)
				return
			}
			// An embed token is a credential in its own right, and a narrower
			// one. EmbedAuth downstream verifies it and confines the request to
			// its single workflow, so demanding an API key as well here would
			// only mean the request ended up with the wider authority of the
			// two.
			if embedToken(r) != "" {
				next.ServeHTTP(w, r)
				return
			}

			principal, ok := opts.resolve(r, operation, cookieName)
			if !ok {
				// The refusal names nothing: no tenant, no workflow, no user,
				// and no hint about which of the several ways to authenticate
				// came closest.
				deny(w, http.StatusUnauthorized, "This request needs an API key or a signed-in session.")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
}

// resolve tries each credential the request could be carrying.
func (opts AuthOptions) resolve(r *http.Request, operation, cookieName string) (auth.Principal, bool) {
	// The stream ticket is tried first for the one operation that accepts it,
	// because a browser opening an EventSource cannot send a header and a
	// cross-origin one will not send the cookie either. Spending the ticket
	// here rather than in the handler means a replay never reaches a handler.
	if executionID, isStream := streamOperation(operation); isStream && opts.Sessions != nil {
		if ticket := strings.TrimSpace(r.URL.Query().Get("ticket")); ticket != "" {
			redeemed, err := opts.Sessions.RedeemTicket(ticket, executionID)
			if err != nil {
				return auth.Principal{}, false
			}
			return auth.Principal{TenantID: redeemed.TenantID, Kind: auth.KindSession, Label: "stream ticket"}, true
		}
	}

	if token, found := bearerToken(r); found && auth.LooksLikeKey(token) {
		if opts.Keys == nil {
			return auth.Principal{}, false
		}
		key, err := opts.Keys.AuthenticateAPIKey(r.Context(), token)
		if err != nil {
			return auth.Principal{}, false
		}
		return auth.Principal{
			TenantID: key.TenantID, KeyID: key.ID, Kind: auth.KindAPIKey, Label: key.Label,
		}, true
	}

	if cookie, err := r.Cookie(cookieName); err == nil && opts.Sessions != nil {
		session, err := opts.Sessions.VerifySession(cookie.Value)
		if err != nil {
			return auth.Principal{}, false
		}
		return auth.Principal{
			TenantID: session.TenantID, UserID: session.UserID,
			Kind: auth.KindSession, Label: session.Email,
		}, true
	}

	return auth.Principal{}, false
}

// streamOperation reports whether a path is one execution's event stream, and
// which execution it names.
func streamOperation(operation string) (string, bool) {
	rest, found := strings.CutPrefix(operation, "/executions/")
	if !found {
		return "", false
	}
	executionID, action, found := strings.Cut(rest, "/")
	if !found || action != "events" || executionID == "" {
		return "", false
	}
	return executionID, true
}

func bearerToken(r *http.Request) (string, bool) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	after, found := strings.CutPrefix(authorization, "Bearer ")
	if !found {
		return "", false
	}
	return strings.TrimSpace(after), true
}
