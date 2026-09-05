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
	"strings"
	"time"
)

// ErrBlocked reports a target the policy refuses to contact.
var ErrBlocked = errors.New("request target is not allowed")

// Policy bounds what an outbound workflow request may do.
type Policy struct {
	// AllowPrivateNetworks disables the private-address guard. It exists for
	// self-hosted installs whose workflows legitimately call services on the
	// same network, and is off by default.
	AllowPrivateNetworks bool
	// AllowedHosts, when non-empty, is the only set of hosts that may be
	// contacted at all. Entries may be exact or `*.`-prefixed.
	AllowedHosts []string
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
func NewClient(policy Policy) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
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
				if err := policy.CheckAddress(ip); err != nil {
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
			return policy.CheckURL(request.URL)
		},
	}
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
