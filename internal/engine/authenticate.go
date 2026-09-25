package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Authenticate resolves the node's credential and applies it to an outbound
// request.
//
// Ownership, type and domain scope are all checked before the secret touches
// the request, so a workflow cannot point a credential at an arbitrary host.
//
// It lives here rather than beside one node because there are now two callers —
// the hand-written HTTP node and the declarative routing interpreter — and a
// second copy of this check would be a second place for the host-scope test to
// be forgotten. Neither caller is allowed its own version.
func (request Request) Authenticate(ctx context.Context, ir workflow.IRNode, httpRequest *http.Request) error {
	resolved, _, found, err := request.ResolveNodeCredential(ctx, ir)
	if err != nil || !found {
		return err
	}
	return applyResolved(ir, resolved, httpRequest)
}

// AuthenticateAs resolves the credential of one named type that the node
// attached and applies it to an outbound request.
//
// It is the seam a node pack authenticates through. A pack names the
// credential *type* it wants and the host does the rest: the pack never sees a
// secret, cannot name a credential the node did not attach, and cannot skip the
// domain scope, because all three are decided here.
//
// Unlike Authenticate, naming a type the node did not attach is an error rather
// than a silent no-op: the caller asked for a credential, and sending the
// request anonymously instead would be the opposite of what it asked for.
func (request Request) AuthenticateAs(ctx context.Context, ir workflow.IRNode, credentialType string, httpRequest *http.Request) error {
	resolved, found, err := request.ResolveAttachedCredential(ctx, ir, credentialType)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("node %q: no %s credential is attached to it", ir.Name, credentialType)
	}
	return applyResolved(ir, resolved, httpRequest)
}

// ResolveAttachedCredential resolves the credential of one named type that the
// node attached. It reports found=false when the node attached none of that
// type, and does not consult the resolver in that case.
//
// The lookup is by type rather than by position because a node may attach
// several credentials — an HTTP request that signs with one and reports to
// another — and a caller that names one must get that one.
func (request Request) ResolveAttachedCredential(ctx context.Context, ir workflow.IRNode, credentialType string) (Credential, bool, error) {
	if strings.TrimSpace(credentialType) == "" {
		return Credential{}, false, nil
	}
	credentialID := strings.TrimSpace(ir.Credentials[credentialType])
	if credentialID == "" {
		return Credential{}, false, nil
	}
	return request.resolveCredential(ctx, ir, credentialType, credentialID)
}

// applyResolved is the one place a resolved credential is bound to a request.
//
// Ownership, type and domain scope are all checked before the secret touches
// the request, so a workflow cannot point a credential at an arbitrary host.
// It is a free function rather than a method because it needs nothing from the
// request: what it needs is the credential and the URL.
func applyResolved(ir workflow.IRNode, resolved Credential, httpRequest *http.Request) error {
	if err := resolved.ScopeRequest(httpRequest); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if err := credentials.Apply(httpRequest, resolved.Type, resolved.Fields); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return nil
}

// ScopeRequest checks that this credential may be sent to the request's host
// and binds the credential's redirect scope to the request, before anything
// secret is placed on it.
//
// The same bound has to survive a redirect. The host check covers the URL the
// caller names; without the scope on the request context, a 30x from it
// carries the credential's header or query secret to a host AllowedDomains
// never named, because Go strips only Authorization and Cookie on a cross-host
// hop — and a credential naming no domains would follow a redirect to any host
// at all. The redirect check reads the scope off the initial request's
// context, so it is attached here, beside the check that produced it.
//
// Exported so a caller that places a credential on a request by some other
// means — a trigger lifecycle whose template writes the secret into the URL,
// the Telegram registration calls — goes through the same two checks the
// engine applies to a node's request rather than a copy that drifts.
//
// The request is mutated in place because its context is what the HTTP client
// carries into the redirect chain; returning a new request would change a
// signature two callers already use.
func (credential Credential) ScopeRequest(httpRequest *http.Request) error {
	if !credential.AllowsHost(httpRequest.URL.Host) {
		return fmt.Errorf("credential %q is not allowed for host %q", credential.Name, httpRequest.URL.Hostname())
	}
	*httpRequest = *httpRequest.WithContext(safehttp.WithCredentialScope(httpRequest.Context(), credential.RedirectScope()))
	return nil
}

// RedirectScope is the bound this credential places on a redirect chain; see
// credentials.Record.RedirectScope.
func (credential Credential) RedirectScope() safehttp.CredentialScope {
	return credentials.Record{AllowedDomains: credential.AllowedDomains}.RedirectScope()
}

// CheckType reports a credential that is not of the type its caller declared.
//
// It is the half that stops a caller from being pointed at a credential of
// another kind — a database credential where an HTTP one was expected, a
// header key where a bot token was — and every lookup path applies it, the
// engine's own and a trigger lifecycle's alike.
func (credential Credential) CheckType(want string) error {
	if want != "" && credential.Type != want {
		return fmt.Errorf("credential %q is a %s credential, not %s", credential.Name, credential.Type, want)
	}
	return nil
}

// AllowsHost reports whether this credential may be sent to a host.
//
// Exported so a caller that authenticates something other than an
// *http.Request — a chat model, which signs its own call inside the provider
// adapter — checks the domain scope through the same rule Authenticate uses
// instead of reimplementing the wildcard matching a second time.
func (credential Credential) AllowsHost(host string) bool {
	return credentials.Record{AllowedDomains: credential.AllowedDomains}.AllowsHost(host)
}

// ResolveNodeCredential resolves the one credential a node names and checks it
// is of the declared type. It reports found=false when the node names none.
//
// Separate from Authenticate because the routing interpreter needs the resolved
// record *before* it has a request to authenticate: a routing template may read
// `$credentials.baseUrl`, which decides what URL is built at all.
func (request Request) ResolveNodeCredential(ctx context.Context, ir workflow.IRNode) (Credential, string, bool, error) {
	credentialID, credentialType := "", ""
	for typeID, id := range ir.Credentials {
		if strings.TrimSpace(id) == "" {
			continue
		}
		credentialType, credentialID = typeID, id
		break
	}
	if credentialID == "" {
		return Credential{}, "", false, nil
	}
	resolved, found, err := request.resolveCredential(ctx, ir, credentialType, credentialID)
	if err != nil || !found {
		return Credential{}, "", false, err
	}
	return resolved, credentialType, true, nil
}

// resolveCredential resolves one credential by ID and checks it is of the type
// the node declared for it.
//
// The type check is the half that stops a node from being pointed at a
// credential of another kind — a database credential where an HTTP one was
// expected — and it lives here so every lookup path applies it.
func (request Request) resolveCredential(ctx context.Context, ir workflow.IRNode, credentialType, credentialID string) (Credential, bool, error) {
	if request.Credentials == nil {
		return Credential{}, false, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return Credential{}, false, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if err := resolved.CheckType(credentialType); err != nil {
		return Credential{}, false, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return resolved, true, nil
}

// ExpressionContext assembles the approved roots for one item.
//
// Shared by every executor that resolves parameters, so a `{{ }}` in an HTTP
// URL, one in a webhook response body and one in a node pack's routing template
// all see exactly the same data. The routing interpreter adds its own three
// roots on top of this; it does not build a different context.
func (request Request) ExpressionContext(item workflow.Item, input workflow.NodeInput, index int) expression.Context {
	inputItems := make(map[string][]map[string]any, len(input))
	for port, portItems := range input {
		converted := make([]map[string]any, len(portItems))
		for position, portItem := range portItems {
			converted[position] = portItem.JSON
		}
		inputItems[port] = converted
	}
	return expression.Context{
		JSON:  item.JSON,
		Input: inputItems,
		Nodes: request.NodeOutputs,
		// Pairing is done here rather than left to each node, because this is
		// the one place every executor's expression context is built: an
		// executor that forgot it would read `$('X').item` and the legacy
		// `$node["X"].json` from the wrong item, silently.
		NodeItems:    PairNodeItems(request.NodeItems, item, index),
		NodeBranches: request.NodeBranches,
		Workflow:     request.Workflow,
		Env:          request.Env,
		Execution: expression.ExecutionContext{
			ID:   request.Execution.ID,
			Mode: request.Execution.Mode,
			// The resume links are minted before the graph runs so a workflow
			// can send one out before it suspends. Dropping them here made
			// `$execution.resumeUrl` and `$execution.approvalUrl` always empty,
			// which left the Wait node's webhook and form modes unusable — the
			// author had no way to compose the link to send.
			ResumeURL:   request.Execution.ResumeURL,
			ApprovalURL: request.Execution.ApprovalURL,
		},
		ItemIndex: index,
		RunIndex:  request.RunIndex,
		// The clock reads the workflow's own settings.timezone, which the
		// importer preserves. Without it `$today` was midnight UTC, which is
		// the wrong calendar day for most of the world for part of every day.
		Timezone: request.Workflow.Timezone,
	}
}
