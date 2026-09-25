package nodes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// hopByHopHeaders are the headers a proxy must not forward.
//
// The child is not a proxy, but the rule is the same one and for the same
// reason: these headers describe *this* connection. Forwarding them invites a
// hop-by-hop header a package made up to change how the host's own client
// behaves, and `Connection: keep-alive` on a request whose connection is the
// host's is a lie the transport then acts on.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// sidecarEgress serves the child's outbound HTTP calls.
//
// A community package never opens a socket: the runner refuses every direct
// route before a connection exists, and the only way out is a host call, which
// lands here. That makes this the single place the deployment's egress policy
// is applied to third-party code, which is the point — the boundary is the same
// safehttp client the native HTTP node uses, not a second one written for
// community nodes.
type sidecarEgress struct {
	policy safehttp.Policy
	client *http.Client
	held   []engine.Credential
	scrub  scrubber
	log    *slog.Logger
	tenant string
	node   string
}

// newSidecarEgress builds the handler for one run.
//
// The client is built once per run rather than per request: safehttp.NewClient
// installs a dial-time address guard and a redirect check, and building it per
// call would rebuild both for every request the package makes.
func newSidecarEgress(policy safehttp.Policy, held []engine.Credential, scrub scrubber, log *slog.Logger, tenant, node string) *sidecarEgress {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &sidecarEgress{
		policy: policy, client: safehttp.NewClient(policy), held: held, scrub: scrub,
		log: log, tenant: tenant, node: node,
	}
}

// HTTP issues one outbound request on the package's behalf.
func (egress *sidecarEgress) HTTP(ctx context.Context, call sidecar.HTTPRequest) (sidecar.HTTPResponse, error) {
	target, err := url.Parse(strings.TrimSpace(call.URL))
	if err != nil || target.Host == "" {
		return sidecar.HTTPResponse{}, egress.refuse(fmt.Errorf("the URL %q is not a valid absolute URL", call.URL))
	}
	// Checked before the dial as well as at it: the dial-time guard runs after
	// DNS, so a forbidden but resolvable host would already have been resolved
	// and, without this, contacted before it was refused.
	if err := egress.policy.CheckURL(target); err != nil {
		return sidecar.HTTPResponse{}, egress.refuse(err)
	}
	// Every credential the run holds has to allow the host. The host cannot
	// know which secret the package is about to put in which header, so the
	// only sound rule is the intersection: a run holding a credential scoped to
	// one API may not reach anywhere that credential is not scoped for.
	//
	// An unscoped credential allows every host, so this only ever bites where a
	// deployment deliberately scoped one.
	for _, credential := range egress.held {
		if !credential.AllowsHost(target.Host) {
			return sidecar.HTTPResponse{}, egress.refuse(fmt.Errorf("credential %q is not allowed for host %q", credential.Name, target.Host))
		}
	}

	// The same conjunction on the context, so a redirect outside the scope
	// stops the chain instead of following it with the secret in hand. A
	// held credential that names no domains holds the whole chain to the
	// first request's host, since the host cannot know whether that is the
	// secret the package put in a header. A run holding no credential
	// attaches no scope and redirects exactly as an unauthenticated request.
	scoped := ctx
	if len(egress.held) > 0 {
		unbounded := false
		for _, credential := range egress.held {
			// The effective scope decides, so a type default counts as named.
			unbounded = unbounded || credential.RedirectScope().Unbounded
		}
		scoped = safehttp.WithCredentialScope(ctx, safehttp.CredentialScope{AllowsHost: func(host string) bool {
			for _, credential := range egress.held {
				if !credential.AllowsHost(host) {
					return false
				}
			}
			return true
		}, Unbounded: unbounded})
	}

	// The package's own timeout may only shorten the deployment's, never
	// lengthen it.
	timeout := egress.policy.Timeout
	if call.Timeout > 0 && (timeout <= 0 || call.Timeout < timeout) {
		timeout = call.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		scoped, cancel = context.WithTimeout(scoped, timeout)
		defer cancel()
	}

	method := strings.ToUpper(strings.TrimSpace(call.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body *bytes.Reader
	if len(call.Body) > 0 {
		body = bytes.NewReader(call.Body)
	}
	var request *http.Request
	if body == nil {
		request, err = http.NewRequestWithContext(scoped, method, target.String(), nil)
	} else {
		request, err = http.NewRequestWithContext(scoped, method, target.String(), body)
	}
	if err != nil {
		return sidecar.HTTPResponse{}, egress.refuse(fmt.Errorf("the request could not be built: %w", err))
	}
	for key, value := range call.Headers {
		if hopByHopHeaders[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		request.Header.Set(key, value)
	}
	if body != nil && request.Header.Get("Content-Type") == "" {
		// The runner serialises a non-string body as JSON, so the host says so
		// when the package did not.
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := egress.client.Do(request)
	if err != nil {
		return sidecar.HTTPResponse{}, egress.refuse(safehttp.RedactError(err))
	}
	defer response.Body.Close()

	contents, truncated, err := egress.policy.ReadBody(response.Body)
	if err != nil {
		return sidecar.HTTPResponse{}, egress.refuse(fmt.Errorf("the response body could not be read: %w", err))
	}
	if truncated {
		return sidecar.HTTPResponse{}, &hostCallError{
			code:    "response-too-large",
			message: fmt.Sprintf("the response from %q is larger than this deployment allows", target.Host),
		}
	}

	headers := make(map[string]string, len(response.Header))
	for key, values := range response.Header {
		// One string per header, lower-cased: the child's helper hands these to
		// the package as a plain object, and a package reading `headers['content-type']`
		// must not miss because the host spelled it differently.
		headers[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	return sidecar.HTTPResponse{
		Status:     response.StatusCode,
		StatusText: http.StatusText(response.StatusCode),
		Headers:    headers,
		Body:       contents,
	}, nil
}

// refuse logs a refused call and returns it redacted.
//
// Every refusal is a Warn with the tenant and the node: an operator who scoped
// a credential or forbade a private target needs to see the attempt, and the
// package's own error is the only other place it appears.
func (egress *sidecarEgress) refuse(err error) error {
	message := egress.scrub.scrub(err.Error())
	egress.log.Warn("community node host call refused", "tenant", egress.tenant, "node", egress.node, "reason", message)
	return fmt.Errorf("%s", message)
}

// hostCallError is a host call refused for a reason the child has a name for.
//
// The sidecar protocol carries a code beside the message, and the child's
// helper raises an error carrying it. Without one, every refusal reads the same
// to a package that wants to tell "too large" from "not allowed".
type hostCallError struct {
	code    string
	message string
}

func (err *hostCallError) Error() string { return err.message }

// HostCallCode is the code the sidecar pool puts on the http.error frame.
func (err *hostCallError) HostCallCode() (string, string) { return err.code, err.message }

// The handler is what the protocol asks for; asserted here so a signature drift
// is a compile error rather than a run-time denial of every host call.
var _ sidecar.HostHandler = (*sidecarEgress)(nil)
