package middleware

import (
	"context"
	"net"
	"strings"
)

// clientIPKey is the context key the requesting address travels under.
type clientIPKey struct{}

// WithClientIP records the requesting address on a request context.
//
// It exists because huma hands a handler nothing but a context.Context, and the
// sign-in throttle has to count attempts per client. The address is narrowed to
// its host here, so a caller comparing two requests does not have to remember
// that RemoteAddr carries an ephemeral port that changes per connection.
//
// The address is taken from the connection, never from a header: nothing in
// this codebase trusts X-Forwarded-For, and a deployment behind a proxy that
// does not set RemoteAddr correctly gets throttling keyed on the proxy's
// address — which fails closed (everyone behind it shares one bucket) rather
// than open (an attacker rotating a header escapes every bucket).
func WithClientIP(ctx context.Context, remoteAddr string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, hostOf(remoteAddr))
}

// ClientIPFrom returns the address recorded by WithClientIP.
//
// An empty result means no address was recorded — a request that never passed
// through the gate that records one. Callers that throttle must treat it as a
// single shared key rather than as a reason to skip throttling.
func ClientIPFrom(ctx context.Context) string {
	address, _ := ctx.Value(clientIPKey{}).(string)
	return address
}

// hostOf strips the port from a RemoteAddr.
//
// RemoteAddr is host:port for TCP and often bare for a unix socket, so a form
// that does not split is returned trimmed rather than discarded: an address
// that cannot be parsed is still a better throttle key than nothing.
func hostOf(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	host, _, err := net.SplitHostPort(trimmed)
	if err != nil {
		return trimmed
	}
	return host
}
