package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
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
	if !resolved.AllowsHost(httpRequest.URL.Host) {
		return fmt.Errorf("node %q: credential %q is not allowed for host %q", ir.Name, resolved.Name, httpRequest.URL.Hostname())
	}
	// The same bound has to survive a redirect. The check above covers the URL
	// the node names; without the scope on the request context, a 30x from it
	// carries the credential's header or query secret to a host AllowedDomains
	// never named, because Go strips only Authorization and Cookie on a
	// cross-host hop. The redirect check reads the scope off the initial
	// request's context, so it is attached here — beside the check that
	// produced it — rather than in each caller, which is the whole reason this
	// function exists instead of a copy per node.
	//
	// The request is mutated in place because its context is what the HTTP
	// client carries into the redirect chain; returning a new request would
	// change a signature two callers already use.
	*httpRequest = *httpRequest.WithContext(safehttp.WithCredentialScope(
		httpRequest.Context(),
		safehttp.CredentialScope{AllowsHost: resolved.AllowsHost},
	))
	if err := credentials.Apply(httpRequest, resolved.Type, resolved.Fields); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
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
	if request.Credentials == nil {
		return Credential{}, "", false, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return Credential{}, "", false, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if credentialType != "" && resolved.Type != credentialType {
		return Credential{}, "", false, fmt.Errorf("node %q: credential %q is a %s credential, not %s", ir.Name, resolved.Name, resolved.Type, credentialType)
	}
	return resolved, credentialType, true, nil
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
		NodeItems: PairNodeItems(request.NodeItems, item, index),
		Workflow:  request.Workflow,
		Env:       request.Env,
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
		// The clock reads the workflow's own settings.timezone, which the
		// importer preserves. Without it `$today` was midnight UTC, which is
		// the wrong calendar day for most of the world for part of every day.
		Timezone: request.Workflow.Timezone,
	}
}
