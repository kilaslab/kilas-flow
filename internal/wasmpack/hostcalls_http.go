package wasmpack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tetratelabs/wazero/api"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// allowedMethods are the request methods a pack may use.
//
// A method outside this list is refused rather than passed through, so a pack
// cannot use the host as a general-purpose client for verbs the deployment
// never intended to send (TRACE, CONNECT and whatever a server invents).
var allowedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
	http.MethodHead:   true,
}

// transportHeaders are the headers that belong to the transport rather than to
// the request, and that a pack may not set.
//
// They are refused because each one is a way to make the host's own client do
// something the pack was never granted: Content-Length and Transfer-Encoding
// decide how the bytes are framed, Connection and Upgrade reach past HTTP,
// Host decides which virtual host answers while the policy checks the URL, and
// Proxy-* would name a proxy the policy never vetted.
var transportHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
	"upgrade":           true,
	"te":                true,
	"trailer":           true,
}

// refusedHeader reports whether a pack may not set one header.
func refusedHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return transportHeaders[lower] || strings.HasPrefix(lower, "proxy-")
}

// httpRequest is the host side of sdk.HTTP.
func (b *binding) httpRequest(ctx context.Context, module api.Module, stack []uint64) int32 {
	if !b.calls.Enter(ctx, module) {
		return 0
	}
	b.run.clear()

	metadata, code := readGuest(module, api.DecodeU32(stack[0]), api.DecodeU32(stack[1]), maxMetadata)
	if code != 0 {
		return b.fail(code, "the request description could not be read")
	}
	body, code := readGuest(module, api.DecodeU32(stack[2]), api.DecodeU32(stack[3]), maxRequestBody)
	if code != 0 {
		return b.fail(code, "the request body could not be read")
	}

	var request sdk.HTTPRequest
	if err := decodeStrict(metadata, &request); err != nil {
		return b.fail(sdk.ErrInvalid, "the request description is not valid: "+err.Error())
	}
	if len(request.URL) > maxURL {
		return b.fail(sdk.ErrTooLarge, fmt.Sprintf("the request URL is longer than %d bytes", maxURL))
	}
	if len(request.Headers) > maxHeaders {
		return b.fail(sdk.ErrTooLarge, fmt.Sprintf("the request sets more than %d headers", maxHeaders))
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !allowedMethods[method] {
		return b.fail(sdk.ErrInvalid, fmt.Sprintf("the %s method is not one a pack may use", method))
	}
	for name := range request.Headers {
		if refusedHeader(name) {
			return b.fail(sdk.ErrDenied, fmt.Sprintf("the %s header belongs to the transport and cannot be set by a pack", name))
		}
	}

	target, err := url.Parse(request.URL)
	if err != nil {
		return b.fail(sdk.ErrInvalid, "the request URL is not a URL")
	}
	// The same gate the HTTP node passes through, applied before the request is
	// built: scheme, host, and the deployment's allowlist.
	if err := b.host.policy.CheckURL(target); err != nil {
		return b.fail(sdk.ErrBlocked, err.Error())
	}

	// The credential is checked against the manifest before anything resolves
	// it: a pack that did not declare a credential type cannot cause it to be
	// looked up at all, which is what makes "a credential a pack did not
	// declare is not resolvable" true by construction rather than by review.
	credentialType := strings.TrimSpace(request.Credential)
	if credentialType != "" {
		if !b.caps.GrantedCredential(credentialType) {
			return b.fail(sdk.ErrDenied, fmt.Sprintf("the pack did not declare the %s credential", credentialType))
		}
		if !httpCapableCredential(credentialType) {
			return b.fail(sdk.ErrDenied, fmt.Sprintf("%s is not a credential type that can authenticate an HTTP request", credentialType))
		}
	}

	requestCtx, cancel := b.requestContext(ctx, request.TimeoutMS)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(requestCtx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return b.fail(sdk.ErrInvalid, "the request could not be built")
	}
	for name, value := range request.Headers {
		httpRequest.Header.Set(name, value)
	}
	if credentialType != "" {
		// The engine's own seam: it resolves the credential the node attached
		// of that type, refuses one the node does not carry, checks the domain
		// scope before a byte is sent, attaches the scope for the redirect
		// chain and applies the secret. There is deliberately no second copy of
		// any of that here.
		//
		// Every way it can fail is a refusal — the credential is not attached
		// to the node, its AllowedDomains do not cover the host, or its type
		// cannot sign a request — which is what `denied` means to a pack, so
		// the pack is told the host's own words rather than a code it would
		// retry. A storage failure inside the resolver reads as a denial too:
		// the alternative was resolving the credential a second time just to
		// tell the two apart, and one resolution per request is the property
		// worth keeping.
		if err := b.inv.Request.AuthenticateAs(requestCtx, b.inv.IR, credentialType, httpRequest); err != nil {
			return b.fail(sdk.ErrDenied, err.Error())
		}
	}

	client := b.host.stopping
	if request.FollowRedirects {
		client = b.host.following
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return b.requestFailed(ctx, module, err, request.URL)
	}
	defer response.Body.Close()

	if len(response.Header) > maxHeaders {
		return b.fail(sdk.ErrTooLarge, fmt.Sprintf("the response has more than %d headers", maxHeaders))
	}
	responseBody, truncated, err := b.host.policy.ReadBody(response.Body)
	if err != nil {
		return b.requestFailed(ctx, module, err, request.URL)
	}
	head, err := json.Marshal(sdk.HTTPResponse{
		Status:     response.StatusCode,
		Headers:    response.Header,
		BodyLength: len(responseBody),
		Truncated:  truncated,
	})
	if err != nil {
		return b.fail(sdk.ErrFailed, "the response head could not be described")
	}
	return b.succeed(head, responseBody)
}

// requestContext bounds one request by the smallest of the pack's own
// per-request timeout, the deployment's policy timeout and the run's wall
// clock.
//
// The last one is what keeps a pack from parking the whole run in a host call:
// the sandbox's own deadline stops the guest, but a guest blocked inside a host
// function is only released when that function returns, so the request itself
// has to be bounded by the same clock.
func (b *binding) requestContext(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	now := time.Now()
	deadline := b.run.budget(b.limits.Timeout)
	if timeoutMS > 0 {
		if candidate := now.Add(time.Duration(timeoutMS) * time.Millisecond); candidate.Before(deadline) {
			deadline = candidate
		}
	}
	if b.host.policy.Timeout > 0 {
		if candidate := now.Add(b.host.policy.Timeout); candidate.Before(deadline) {
			deadline = candidate
		}
	}
	return context.WithDeadline(ctx, deadline)
}

// requestFailed maps a failed request onto what the pack is told.
func (b *binding) requestFailed(ctx context.Context, module api.Module, err error, target string) int32 {
	// Every branch below may quote the error, and the request carried the
	// host-applied credential — a query secret, a token in the path, or
	// userinfo — so the URL is cut to its scheme and host before anything
	// reads it. A pack that read the secret back out of an error message would
	// have learned what the host never meant to hand it.
	err = safehttp.RedactError(err)
	// A request that ran out of the run's wall clock ended the run, and the
	// honest report is the run's time limit rather than a failed call the pack
	// would retry.
	if b.run.expired(b.limits.Timeout) {
		return b.deadlineEnded(ctx, module)
	}
	switch {
	case errors.Is(err, safehttp.ErrBlocked):
		return b.fail(sdk.ErrBlocked, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return b.fail(sdk.ErrFailed, "the request to "+displayURL(target)+" timed out")
	case errors.Is(err, context.Canceled):
		return b.fail(sdk.ErrFailed, "the request to "+displayURL(target)+" was cancelled")
	default:
		return b.fail(sdk.ErrFailed, err.Error())
	}
}

// displayURL renders a target for a message, without its userinfo.
func displayURL(target string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return "the target"
	}
	parsed.User = nil
	return parsed.String()
}
