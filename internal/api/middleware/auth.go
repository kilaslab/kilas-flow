package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

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

// UserLookup re-reads the account a browser session names.
//
// It is the second identity question the request path asks, and it exists only
// so a session can be revalidated: the session carries the address, and the
// answer carries the two stored facts that must invalidate it — the password
// hash and whether the account is disabled. repository.AuthRepository already
// satisfies it, so a deployment wires the same store as both fields.
//
// It is a field of its own rather than a reuse of Keys because the two are
// genuinely different questions with different costs, and a deployment that
// answers one without the other should have to say so.
type UserLookup interface {
	FindUserForLogin(ctx context.Context, email string) (repository.User, error)
}

// DefaultSessionRevalidationTTL is how long one account read is trusted.
//
// It is the revocation window: a password change or a disabled account takes
// effect within this long, because that is the longest a session can be served
// from a cached answer. Five seconds is short enough that an operator who
// disables an account sees it take effect while they are still watching, and
// long enough that a dashboard polling several endpoints does not turn each
// refresh into a query. Shortening it trades database reads for a faster
// revocation; nothing else changes.
const DefaultSessionRevalidationTTL = 5 * time.Second

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
	// Users re-reads the account behind a browser session, so a session stops
	// working when its user is disabled or its password changes.
	//
	// Nil refuses every session. That is deliberate and it is a breaking
	// wiring change: a process that cannot look the account up cannot tell
	// whether the cookie it is holding belongs to somebody who has since been
	// disabled or had their password changed, and admitting the session anyway
	// is the hole this closes. Such an installation keeps authenticating API
	// keys and refuses every browser session, so it is wired in the same
	// change that introduces this field; the middleware logs the mistake once
	// rather than leaving an operator to guess why sign-in does not stick.
	Users UserLookup
	// SessionRevalidationTTL is how long one lookup is trusted. Zero uses
	// DefaultSessionRevalidationTTL.
	SessionRevalidationTTL time.Duration
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
// A browser session is revalidated against the account it names, so disabling
// an account or changing its password takes effect within AuthOptions's cache
// lifetime rather than at the token's expiry. That lookup is the one thing here
// that touches storage on the request path; without it a stateless token is a
// grant nothing can withdraw.
//
// What it still does not do: it neither rate-limits nor locks an account out
// after repeated failures — that belongs to the login handler, which is the
// only place that knows the address being tried — and it does not bind a
// session to a device, so a stolen cookie works from anywhere until it expires
// or its account changes.
func Authenticate(opts AuthOptions) func(http.Handler) http.Handler {
	prefix := opts.APIPrefix
	cookieName := opts.CookieName
	if cookieName == "" {
		cookieName = auth.SessionCookieName
	}
	accounts := newSessionCache(opts.Users, opts.SessionRevalidationTTL)

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

			principal, ok := opts.resolve(r, operation, cookieName, accounts)
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
func (opts AuthOptions) resolve(r *http.Request, operation, cookieName string, accounts *sessionCache) (auth.Principal, bool) {
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

	// An API key is revalidated by definition: the store looks the row up on
	// every request and answers only for a key that is neither unknown nor
	// revoked, so revoking one takes effect on the next request rather than
	// when something expires. Nothing on this path mints, extends or widens a
	// session — a key stays a key, and the principal below carries no user.
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
			// Includes a token minted before sessions carried a user version:
			// it cannot be revalidated, so it is refused rather than trusted.
			return auth.Principal{}, false
		}
		// A signature proves the token is ours, not that the account behind it
		// is still the one that signed in. Re-reading the account is what makes
		// a disable or a password change take effect on a stateless token, and
		// every disagreement between the two is a refusal: the address that
		// signed in must still resolve to the same account, in the same tenant,
		// with the same credential state.
		account, ok := accounts.current(r.Context(), session)
		if !ok || account.disabled ||
			account.userID != session.UserID ||
			account.tenantID != session.TenantID ||
			account.version != session.UserVersion {
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
