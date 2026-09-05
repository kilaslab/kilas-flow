// Package auth gives a request an owner.
//
// Two paths lead to the same identity: a tenant-scoped API key for machine
// callers, and a signed browser session for the dashboard. Both resolve to a
// Principal, and the tenant on that principal is what every repository call is
// scoped by, so a caller can only ever reach its own rows.
//
// What this package does NOT do: it carries no roles, no permissions and no
// group membership, so every principal of a tenant has that tenant's full
// authority. It also does not rate-limit, so it is no defence against an
// attacker who has a valid key and simply uses it too much. Narrower authority
// inside a tenant is the embed session's job (internal/embed), and only for the
// one workflow that session names.
package auth

import (
	"context"
	"errors"
)

// ErrUnauthenticated reports a credential that must not be honoured.
//
// Expired, revoked, forged and malformed all collapse into this one error on
// purpose: a caller that can tell "no such key" from "wrong secret" apart has
// been handed an oracle for enumerating keys.
var ErrUnauthenticated = errors.New("the credential is not valid")

// Kind names which path authenticated a request.
type Kind string

const (
	// KindAPIKey is a machine caller: a host backend, the SDK, or CI.
	KindAPIKey Kind = "api_key"
	// KindSession is a person's browser, holding a signed session cookie.
	KindSession Kind = "session"
)

// Principal is the authenticated caller behind one request.
//
// TenantID is the only field the repository layer ever sees. The rest exists
// for auditing and for the dashboard's own "who am I" call.
type Principal struct {
	TenantID string
	// UserID is empty for a machine caller: an API key belongs to a tenant,
	// not to the person who happened to create it, so revoking a departing
	// employee's account must not silently stop the host's integration.
	UserID string
	// KeyID is empty for a browser session.
	KeyID string
	Kind  Kind
	// Label is the key's name or the user's email, for log lines only. It is
	// never used to make an authorization decision.
	Label string
}

type contextKey struct{}

// WithPrincipal returns a context carrying the authenticated caller.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

// PrincipalFrom returns the authenticated caller a request was admitted under.
//
// The second result is false for a request that reached a handler without
// authentication, which is what an install with auth disabled looks like.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, found := ctx.Value(contextKey{}).(Principal)
	if !found || principal.TenantID == "" {
		return Principal{}, false
	}
	return principal, true
}
