// Package mail speaks the two protocols an email node needs — SMTP for
// sending, IMAP4rev1 for reading — over dials the deployment's outbound
// policy guards, with no dependency beyond the standard library.
//
// The guarded dial is the point of the package's shape: a mail server is a
// TCP host a credential names, not a URL an HTTP client checks, so the same
// resolve-then-check-then-dial discipline internal/sqlnode applies to a
// database host is applied here. Without it, an email node would be a
// credential-shaped hole into the internal network: the host field of an SMTP
// credential is workflow-editable, and the policy that refuses a workflow
// calling 10.0.0.1 over HTTP has to refuse it dialling 10.0.0.1 over SMTP
// too.
//
// The IMAP client is deliberately small: login, select, UID search, UID fetch
// of one message, flag store and logout — the operations a polling trigger
// with a UID watermark needs, and nothing that could run arbitrary commands.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// Default SMTP and IMAP ports, applied when the credential names none. They
// are the implicit-TLS ports because that is the shape almost every provider
// documents; a STARTTLS submission server names 587 itself.
const (
	defaultSMTPPort = "465"
	defaultIMAPPort = "993"
	dialTimeout     = 10 * time.Second
)

// guardedDial resolves host, refuses every address the policy forbids, and
// dials a checked address rather than the hostname, so a name that changes
// its answer between the two checks cannot slip a forbidden address through.
// It is the same discipline safehttp's dialer applies to an HTTP request and
// internal/sqlnode applies to a database host.
func guardedDial(ctx context.Context, network, host, port string, policy safehttp.Policy) (net.Conn, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	addresses, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("resolve mail host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("mail host %q resolved to no addresses", host)
	}
	for _, address := range addresses {
		if err := policy.CheckEndpointAddress(host, port, address.IP); err != nil {
			return nil, fmt.Errorf("mail host %q resolved to %s: %w", host, address.IP, err)
		}
	}
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	var firstErr error
	for _, address := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// tlsConfig builds the client config for one mail host. InsecureCerts exists
// for a self-signed server the operator runs beside the instance, the same
// option n8n's email nodes expose, and is honoured on both the implicit and
// the STARTTLS handshake.
func tlsConfig(host string, insecureCerts bool) *tls.Config {
	return &tls.Config{ServerName: host, InsecureSkipVerify: insecureCerts}
}

// mailTarget reads and checks a credential's host and port before anything
// dials. The allowedHost check, when the credential carries a scope, is the
// same one every outbound request applies: a mail credential scoped to one
// relay cannot be pointed at another host by editing a node.
func mailTarget(host, port, fallbackPort string, allowedHost func(string) bool) (string, string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", "", fmt.Errorf("the mail credential names no host")
	}
	port = strings.TrimSpace(port)
	if port == "" {
		port = fallbackPort
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return "", "", fmt.Errorf("mail port %q is not a port number between 1 and 65535", port)
	}
	if allowedHost != nil && !allowedHost(host) {
		return "", "", fmt.Errorf("this credential is not allowed to reach the mail host %q", host)
	}
	return host, port, nil
}
