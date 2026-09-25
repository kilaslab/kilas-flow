// Package safehttp builds outbound HTTP clients that refuse to reach internal
// infrastructure.
//
// A workflow's HTTP node sends requests to URLs authored by a tenant of a host
// SaaS. Without a policy, that URL is a request forgery primitive pointed at
// the cloud metadata service, the internal database, or a neighbouring
// service on the same network.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrBlocked reports a target the policy refuses to contact.
var ErrBlocked = errors.New("request target is not allowed")

// CredentialScope is the domain bound a credential places on one outbound
// request. Carried on the request context from Authenticate to CheckRedirect
// so a redirect outside the scope stops the chain instead of carrying the
// secret to a host it was never allowed to reach.
type CredentialScope struct {
	// AllowsHost reports whether the credential may be sent to a host,
	// using the same wildcard rule Authenticate applied to the first URL.
	AllowsHost func(host string) bool
	// Unbounded reports that the credential names no domains, so AllowsHost
	// admits every host. A redirect is then held to the hostname the first
	// request went to instead: an empty domain list is the author saying
	// "wherever this node sends it", not "wherever a server redirects it",
	// and Go forwards every header but Authorization and Cookie across a
	// host change — X-Api-Key and a custom template's headers included.
	Unbounded bool
}

type credentialScopeKey struct{}

// WithCredentialScope attaches a credential's domain bound to a request
// context. A scope on the context means a credential is on the request, so
// the redirect check refuses a scheme downgrade whatever the scope says. A nil
// AllowsHost skips only the domain check, which is never read as a refusal.
// A caller with no credential attaches no scope and behaves exactly as before.
func WithCredentialScope(ctx context.Context, scope CredentialScope) context.Context {
	return context.WithValue(ctx, credentialScopeKey{}, scope)
}

// CredentialScopeFrom returns the domain bound carried by ctx, if any.
func CredentialScopeFrom(ctx context.Context) (CredentialScope, bool) {
	scope, ok := ctx.Value(credentialScopeKey{}).(CredentialScope)
	return scope, ok
}

// Policy bounds what an outbound workflow request may do.
type Policy struct {
	// AllowPrivateNetworks disables the private-address guard. It exists for
	// self-hosted installs whose workflows legitimately call services on the
	// same network, and is off by default.
	AllowPrivateNetworks bool
	// AllowedHosts, when non-empty, is the only set of hosts that may be
	// contacted at all. Entries may be exact or `*.`-prefixed.
	AllowedHosts []string
	// AllowedPrivateEndpoints names the exact `host:port` endpoints that may
	// resolve to a private address while the guard stays on for everything
	// else. It exists for a service an operator deliberately runs beside the
	// instance — a local model server, a stub a test suite talks to — where
	// the only other lever, AllowPrivateNetworks, would open every internal
	// address to every outbound request in the installation.
	//
	// An entry is one host and one port, matched exactly: `127.0.0.1:11434`,
	// `[::1]:11434`, `ollama.internal:11434`. The port is part of the grant
	// rather than optional, so an allowance for a model server is not also an
	// allowance for the SSH daemon or the database on the same box. A URL that
	// names no port is dialled on 80 or 443, so an entry has to say which.
	// A `*.` prefix is not honoured here even though AllowedHosts honours one,
	// because `*.internal:11434` is a licence to sweep a network — which is
	// the thing this field exists to avoid — and an entry that cannot be
	// parsed matches nothing, so a typo costs the allowance rather than
	// widening it. The host is compared as the URL wrote it, never as it
	// resolves, so an entry for `localhost:11434` does not admit
	// `127.0.0.1:11434`: name the form the workflow actually uses.
	//
	// What it does not protect against, stated plainly rather than left to be
	// discovered:
	//
	//   - It trusts DNS for an entry that names a hostname. Whoever answers
	//     for `ollama.internal` chooses which private address the grant
	//     reaches on that port. An entry naming an IP literal has no such
	//     exposure, so prefer one.
	//   - It says nothing about the service listening there. Whatever that
	//     endpoint can be made to do with a request body a workflow controls
	//     is reachable by any workflow that can address it.
	//   - It grants nothing at pre-flight. CheckURL is a separate gate that
	//     callers apply before a request is built, so a non-empty AllowedHosts
	//     has to name the host as well, and a redirect away from the endpoint
	//     is dialled — and refused — on its own address.
	AllowedPrivateEndpoints []string
	// MaxRedirects bounds redirect chains; each hop is re-checked.
	MaxRedirects int
	// MaxResponseBytes bounds how much of a response is read into memory.
	MaxResponseBytes int64
	// Timeout bounds the whole request.
	Timeout time.Duration
}

// DefaultPolicy is the conservative policy used when nothing is configured.
func DefaultPolicy() Policy {
	return Policy{
		MaxRedirects:     5,
		MaxResponseBytes: 8 << 20,
		Timeout:          30 * time.Second,
	}
}

// CheckURL validates a target before a request is built. It rejects
// non-HTTP(S) schemes, missing hosts, and hosts outside an explicit allowlist.
//
// This is a first pass only: the address a hostname resolves to is checked
// again at dial time, which is what actually stops DNS rebinding.
func (policy Policy) CheckURL(target *url.URL) error {
	if target == nil {
		return fmt.Errorf("%w: no URL", ErrBlocked)
	}
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme %q is not supported", ErrBlocked, target.Scheme)
	}
	host := target.Hostname()
	if host == "" {
		return fmt.Errorf("%w: URL has no host", ErrBlocked)
	}
	if len(policy.AllowedHosts) > 0 && !hostAllowed(host, policy.AllowedHosts) {
		return fmt.Errorf("%w: host %q is not in the allowed list", ErrBlocked, host)
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if suffix, wildcard := strings.CutPrefix(entry, "*."); wildcard {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

// CheckAddress reports whether one resolved IP may be contacted.
//
// It is told nothing about the endpoint the address was resolved for, so it
// cannot honour AllowedPrivateEndpoints. The dialer calls CheckEndpointAddress
// instead, which can; a caller holding only an address keeps using this and
// gets the stricter answer.
func (policy Policy) CheckAddress(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: address could not be parsed", ErrBlocked)
	}
	if policy.AllowPrivateNetworks {
		return nil
	}
	if reason := blockedReason(ip); reason != "" {
		return fmt.Errorf("%w: %s address %s", ErrBlocked, reason, ip)
	}
	return nil
}

// CheckEndpointAddress reports whether one resolved IP may be contacted while
// dialling a named endpoint.
//
// This is CheckAddress plus the single exemption AllowedPrivateEndpoints
// describes. It lives beside the dialer because only the dialer knows both
// halves — which endpoint was asked for, and what it resolved to — and the
// exemption is worthless, or dangerous, without both.
func (policy Policy) CheckEndpointAddress(host, port string, ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: address could not be parsed", ErrBlocked)
	}
	if policy.allowsPrivateEndpoint(host, port) {
		return nil
	}
	return policy.CheckAddress(ip)
}

// allowsPrivateEndpoint reports whether this exact host and port were named.
func (policy Policy) allowsPrivateEndpoint(host, port string) bool {
	if len(policy.AllowedPrivateEndpoints) == 0 {
		return false
	}
	dialedHost, dialedPort, err := parsePrivateEndpoint(net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	for _, entry := range policy.AllowedPrivateEndpoints {
		entryHost, entryPort, err := parsePrivateEndpoint(entry)
		if err != nil {
			// An entry nobody can parse grants nothing. A misconfigured
			// allowance has to narrow the policy, never widen it.
			continue
		}
		if entryHost == dialedHost && entryPort == dialedPort {
			return true
		}
	}
	return false
}

// CheckPrivateEndpoint reports why an AllowedPrivateEndpoints entry cannot be
// honoured.
//
// The matcher ignores an entry it cannot parse, which fails closed but does so
// in silence — and an operator whose allowance silently grants nothing is an
// operator on their way to turning the whole guard off out of frustration. A
// configuration loader calls this to refuse the typo at startup instead.
func CheckPrivateEndpoint(entry string) error {
	_, _, err := parsePrivateEndpoint(entry)
	return err
}

// parsePrivateEndpoint splits an entry into the form the matcher compares.
//
// Ports are compared as numbers and IP literals in their canonical text, so
// `[0:0:0:0:0:0:0:1]:11434` and `[::1]:11434` are one grant rather than two
// spellings of which only one happens to match what the dialer was handed.
func parsePrivateEndpoint(entry string) (string, int, error) {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return "", 0, errors.New("an empty entry names no endpoint")
	}
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "", 0, fmt.Errorf("%q must name a host and a port, as in 127.0.0.1:11434", entry)
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", 0, fmt.Errorf("%q names a port but no host", entry)
	}
	if strings.Contains(host, "*") {
		return "", 0, fmt.Errorf("%q uses a wildcard, and this list names one endpoint at a time", entry)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%q names port %q, which is not a port number", entry, portText)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), port, nil
	}
	// Anything that is not an IP literal has to look like a hostname. Without
	// this, `http://127.0.0.1:11434` splits into the host `http://127.0.0.1`
	// and a valid port, so an operator who pasted a URL would be told their
	// entry is fine by a list that can never match it.
	for _, character := range host {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '-', character == '.', character == '_':
		default:
			return "", 0, fmt.Errorf("%q names host %q, which is not a hostname or an IP address", entry, host)
		}
	}
	return host, port, nil
}

func blockedReason(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsUnspecified():
		return "unspecified"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// 169.254.0.0/16 also carries the cloud metadata service, which is the
		// single most valuable SSRF target on a hosted install.
		return "link-local"
	case ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return "multicast"
	case ip.IsPrivate():
		return "private"
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 carrier-grade NAT and 192.0.0.0/24 IETF protocol
		// assignments are neither private nor public in Go's classification.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return "shared-address-space"
		}
		if v4[0] == 192 && v4[1] == 0 && v4[2] == 0 {
			return "reserved"
		}
		if v4[0] >= 240 {
			return "reserved"
		}
		return ""
	}
	// IPv4-mapped addresses were normalized by To4 above; what remains here is
	// genuine IPv6. fc00::/7 is unique-local, 64:ff9b::/96 is NAT64.
	if len(ip) == net.IPv6len {
		if ip[0]&0xfe == 0xfc {
			return "unique-local"
		}
		if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b {
			return "NAT64"
		}
	}
	return ""
}

// NewClient builds an HTTP client that enforces the policy on every
// connection, including each redirect hop.
//
// Tenant-authored egress never uses a proxy: ProxyFromEnvironment would hand
// the dial to a proxy whose address alone is validated, while the real target
// (metadata IP, RFC1918, loopback) is never resolved or checked — voiding
// the private-address guard on every deployment that sets HTTP(S)_PROXY. An
// operator that needs egress through a proxy terminates it outside this
// client, where the proxy enforces its own policy.
func NewClient(policy Policy) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	transport := &http.Transport{
		Proxy: nil,
		// The address is checked here, after DNS resolution and immediately
		// before the socket is opened, so a hostname that resolves to a public
		// address on the first lookup and an internal one on the second cannot
		// slip past a URL-only check.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", ErrBlocked, address)
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				if err := policy.CheckEndpointAddress(host, port, ip); err != nil {
					lastErr = err
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err != nil {
					lastErr = err
					continue
				}
				return conn, nil
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("%w: %s did not resolve", ErrBlocked, host)
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}

	maxRedirects := policy.MaxRedirects
	if maxRedirects < 0 {
		maxRedirects = 0
	}
	return &http.Client{
		Transport: transport,
		Timeout:   policy.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			// A redirect can point anywhere, so the destination gets the same
			// scheme and allowlist check the original URL did.
			if err := policy.CheckURL(request.URL); err != nil {
				return err
			}
			// And the credential's own domain scope must survive the hop: Go
			// strips only Authorization/Cookie on cross-host redirects, so a
			// scoped header/query secret would otherwise follow a 30x to a
			// host its AllowedDomains never named. A hop outside the scope
			// stops the chain with the last in-scope response rather than
			// leaking the secret or failing the whole call. The full
			// host:port is passed (not just the hostname) so a scope may be
			// port-aware where the deployment needs it; AllowsHost
			// implementations that match hostnames ignore the port half.
			// A credential that names no domains is held to the first
			// request's host, and no credential follows a step down to
			// plain http; see allowsHop.
			if scope, ok := CredentialScopeFrom(request.Context()); ok && !scope.allowsHop(request.URL, via) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// allowsHop reports whether a redirect may carry the credential on to next.
//
// Three rules, each stopping the chain with the last in-scope response rather
// than failing the call, as the domain rule always has:
//
//   - the credential's own domains, when it names any;
//   - the first request's hostname, when it names none — Unbounded is an
//     author sending the secret where the node points, not wherever a server
//     answers with a Location header. The port is not compared, as the domain
//     rule does not compare it, so a service moving to another port of the
//     same host keeps working;
//   - no step down from https to http, since the secret would cross the
//     network in the clear on the next hop.
func (scope CredentialScope) allowsHop(next *url.URL, via []*http.Request) bool {
	if scope.AllowsHost != nil && !scope.AllowsHost(next.Host) {
		return false
	}
	if len(via) == 0 {
		return true
	}
	if scope.Unbounded && !sameHostname(via[0].URL, next) {
		return false
	}
	previous := via[len(via)-1].URL
	if strings.EqualFold(previous.Scheme, "https") && !strings.EqualFold(next.Scheme, "https") {
		return false
	}
	return true
}

// sameHostname compares two URLs' hostnames the way the domain rule does:
// case-insensitive, a trailing dot ignored, the port left out.
func sameHostname(first, next *url.URL) bool {
	normalise := func(target *url.URL) string {
		return strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	}
	return normalise(first) == normalise(next)
}

// ReadBody reads at most MaxResponseBytes and reports when the limit was hit,
// so a workflow cannot be used to pull an unbounded response into memory.
func (policy Policy) ReadBody(body io.Reader) ([]byte, bool, error) {
	limit := policy.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultPolicy().MaxResponseBytes
	}
	contents, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(contents)) > limit {
		return contents[:limit], true, nil
	}
	return contents, false, nil
}
