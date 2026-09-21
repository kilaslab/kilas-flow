package nodes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// SidecarExecutorID is the binding every community node carried across the
// JavaScript sidecar names. It is re-exported so the catalogue and the engine
// registry agree on one string rather than on two that look alike.
const SidecarExecutorID = sidecarnode.ExecutorID

// sidecarMainPort is the item channel every node reads its work from. The
// sidecar serves exactly one main input, which the conversion enforces.
const sidecarMainPort = "main"

// SidecarRunner runs one node in the calling tenant's sidecar process.
// *sidecar.Pool satisfies it.
type SidecarRunner interface {
	Execute(context.Context, sidecar.Request) (sidecar.Result, error)
}

// SidecarCatalog supplies the definition whose ordered property list and
// visibility rules the executor resolves. *node.Registry satisfies it.
type SidecarCatalog interface {
	Get(nodeType string, version workflow.TypeVersion) (node.Definition, bool)
}

// SidecarSharedSettings returns the settings every node carries (Continue on
// Fail, Retry, Timeout, Always output data).
//
// Exported for the composition root to hand to sidecarnode.Load: a community
// node has to offer the same four switches as a built-in one, and the package
// that defines them is this one.
func SidecarSharedSettings() []node.PropertyDefinition { return sharedSettings() }

// SidecarExecutor runs community nodes in the sidecar process.
//
// The engine does not know a node is remote: it hands over the compiled IR,
// the input items and the runtime request, and this adapter marshals them into
// the protocol. Everything the package receives is third-party input by
// construction, so the adapter holds the line on three things — which items may
// be sent (no binary), which credentials may be resolved (only the declared
// types, only for this tenant), and where the run may reach the network (through
// the host's egress policy and nothing else).
type SidecarExecutor struct {
	runner  SidecarRunner
	index   *sidecarnode.Index
	catalog SidecarCatalog
	policy  safehttp.Policy
	log     *slog.Logger
}

// NewSidecarExecutor builds the adapter. A nil logger discards diagnostics.
func NewSidecarExecutor(runner SidecarRunner, index *sidecarnode.Index, catalog SidecarCatalog, policy safehttp.Policy, log *slog.Logger) *SidecarExecutor {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &SidecarExecutor{runner: runner, index: index, catalog: catalog, policy: policy, log: log}
}

// RegisterSidecarExecutor installs the adapter.
//
// Like RegisterRoutingExecutor it is a separate entry point from
// RegisterExecutors: the nodes it runs are not in this repository, so a
// deployment that never installs a community package never registers it.
func RegisterSidecarExecutor(registry *engine.Registry, runner SidecarRunner, index *sidecarnode.Index, catalog SidecarCatalog, policy safehttp.Policy, log *slog.Logger) error {
	return registry.Register(sidecarnode.ExecutorID, NewSidecarExecutor(runner, index, catalog, policy, log))
}

// Execute runs one community node for the calling tenant.
func (executor *SidecarExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	ref, found := executor.index.Get(ir.Type, ir.TypeVersion)
	if !found {
		return nil, fmt.Errorf("node %q: %s version %s is not a community node this deployment loaded", ir.Name, ir.Type, ir.TypeVersion)
	}
	// Refused before anything is sent: without a tenant there is no process the
	// run's decrypted credentials are allowed to enter, and "no tenant" is a
	// composition mistake, not a workflow the user wrote.
	tenant := request.Execution.TenantID
	if strings.TrimSpace(tenant) == "" {
		return nil, fmt.Errorf("node %q: a community node needs an execution tenant, and this run has none", ir.Name)
	}
	definition, found := executor.catalog.Get(ir.Type, ir.TypeVersion)
	if !found {
		return nil, fmt.Errorf("node %q: %s version %s is not in the node catalogue", ir.Name, ir.Type, ir.TypeVersion)
	}

	items := input[sidecarMainPort]
	if len(items) == 0 {
		// A community node behind a trigger that produced no items still runs
		// once, the same way a routed node does: a node that sends a message
		// must not be skipped because its trigger carried no payload.
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	marshalled := make([]sidecar.Item, 0, len(items))
	for index, item := range items {
		if len(item.Binary) > 0 {
			// Attachments are refused rather than dropped: a package that gets
			// an item without the file it was told about would send something
			// the user did not write.
			return nil, fmt.Errorf("node %q: item %d carries a binary attachment, which the JavaScript sidecar does not support", ir.Name, index)
		}
		json := item.JSON
		if json == nil {
			json = map[string]any{}
		}
		marshalled = append(marshalled, sidecar.Item{JSON: json})
	}

	resolved, credentialsByType, err := resolveSidecarCredentials(ctx, request, ir, ref)
	if err != nil {
		return nil, err
	}
	scrub := newScrubber(resolved)

	paramsByItem, err := sidecarParams(definition, ref, ir, input, request, items)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	var params map[string]any
	if len(paramsByItem) > 0 {
		params = paramsByItem[0]
	}
	result, err := executor.runner.Execute(ctx, sidecar.Request{
		Tenant:       tenant,
		Node:         ref.Name,
		NodeVersion:  ref.NodeVersion,
		Params:       params,
		ParamsByItem: paramsByItem,
		Items:        marshalled,
		Credentials:  credentialsByType,
		Context: &sidecar.RunContext{
			NodeName:    ir.Name,
			NodeType:    ir.Type,
			TypeVersion: ir.TypeVersion.Float(),
			ExecutionID: request.Execution.ID,
			WorkflowID:  request.Execution.WorkflowID,
			Mode:        request.Execution.Mode,
			Timezone:    request.Workflow.Timezone,
		},
		Host: newSidecarEgress(executor.policy, resolved, scrub, executor.log, tenant, ir.Name),
	})
	if err != nil {
		// A context that ended is the caller's answer, not the child's: the
		// engine classifies it as a timeout or a cancellation of its own.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// The child's own words are rebuilt rather than wrapped verbatim: a
		// secret the package echoed into its message must not survive into the
		// trace. The error stays a *CallError, so a caller can still read its
		// code and unwrap the cause.
		var callErr *sidecar.CallError
		if errors.As(err, &callErr) {
			scrubbed := *callErr
			scrubbed.Detail = scrub.scrub(callErr.Detail)
			scrubbed.ChildMessage = scrub.scrub(callErr.ChildMessage)
			return nil, fmt.Errorf("node %q: %w", ir.Name, &scrubbed)
		}
		return nil, fmt.Errorf("node %q: %s", ir.Name, scrub.scrub(err.Error()))
	}
	return sidecarOutputs(ir, result)
}

// sidecarParams resolves the parameters one item at a time.
//
// The order matters and is the whole reason this is a function rather than a
// loop in Execute: defaults are filled first (a declared default may itself be
// an expression, which n8n writes as `={{ $json.x }}`), then everything is
// resolved against the item's expression context, and only then are the fields
// the node does not currently show dropped. Resolving before the visibility
// filter is what stops a stale value from a resource the node was switched away
// from reaching the package.
func sidecarParams(definition node.Definition, ref sidecarnode.NodeRef, ir workflow.IRNode, input workflow.NodeInput, request engine.Request, items []workflow.Item) ([]map[string]any, error) {
	version := ir.TypeVersion.String()
	stored := ir.Parameters
	params := make([]map[string]any, 0, len(items))
	for index, item := range items {
		merged := make(map[string]any, len(stored)+len(ref.HiddenDefaults))
		for key, value := range stored {
			merged[key] = value
		}
		// A hidden property is not in the editor and so not in the document,
		// but the package can still read it: its declared default has to reach
		// the run.
		for key, value := range ref.HiddenDefaults {
			if _, present := merged[key]; !present {
				merged[key] = value
			}
		}
		filled := property.WithDefaults(definition.Parameters, merged)
		resolved, err := expression.Resolve(filled, request.ExpressionContext(item, input, index))
		if err != nil {
			return nil, err
		}
		for _, declared := range definition.Parameters {
			// A node keeps the parameters of every resource and operation it
			// has ever been set to. Without this filter a stale value from the
			// branch the user switched away from would reach the package and
			// be sent somewhere.
			if !property.VisibleProperty(declared, resolved, version) {
				delete(resolved, declared.Key)
			}
		}
		params = append(params, resolved)
	}
	return params, nil
}

// resolveSidecarCredentials resolves exactly the credential types the node
// declares.
//
// Only the declared types, because the compiler does not restrict the keys a
// document may carry: a node that never asked for a credential would otherwise
// be handed whatever the workflow author attached to it, and third-party code
// holding a secret it never asked for is a leak with no upside.
func resolveSidecarCredentials(ctx context.Context, request engine.Request, ir workflow.IRNode, ref sidecarnode.NodeRef) ([]engine.Credential, map[string]map[string]string, error) {
	declared := map[string]bool{}
	for _, nodeType := range ref.CredentialTypes {
		declared[nodeType] = true
	}

	types := make([]string, 0, len(ir.Credentials))
	for nodeType, credentialID := range ir.Credentials {
		if strings.TrimSpace(credentialID) == "" {
			// A blank ID is a document that names the slot and nothing else,
			// the same shape ResolveNodeCredential skips.
			continue
		}
		if !declared[nodeType] {
			return nil, nil, fmt.Errorf("node %q: the workflow attaches a %q credential, which this node does not declare", ir.Name, nodeType)
		}
		types = append(types, nodeType)
	}
	if len(types) == 0 {
		return nil, nil, nil
	}
	if request.Credentials == nil {
		return nil, nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	// Sorted so a package with two credential types sees them in a stable
	// order, and so the scrub list is deterministic.
	sort.Strings(types)

	resolved := make([]engine.Credential, 0, len(types))
	byType := make(map[string]map[string]string, len(types))
	for _, nodeType := range types {
		credential, err := request.Credentials.ResolveCredential(ctx, ir.Credentials[nodeType])
		if err != nil {
			return nil, nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if credential.Type != nodeType {
			return nil, nil, fmt.Errorf("node %q: credential %q is a %s credential, not %s", ir.Name, credential.Name, credential.Type, nodeType)
		}
		resolved = append(resolved, credential)
		byType[nodeType] = credential.Fields
	}
	return resolved, byType, nil
}

// sidecarOutputs maps the child's answer onto the streams the engine expects.
//
// The error port is the runner's, not the executor's: a node that continues on
// an error branch has one port the package knows nothing about, and the engine
// pads it. Returning the full declared arity there fails the run with "returned
// N output streams, want N-1", so the count is derived rather than assumed.
func sidecarOutputs(ir workflow.IRNode, result sidecar.Result) (workflow.NodeOutput, error) {
	outputs := result.Outputs
	if len(outputs) == 0 {
		outputs = [][]sidecar.Item{result.Items}
	}
	want := sidecarExpectedPorts(ir)
	if len(outputs) > want {
		for position, extra := range outputs[want:] {
			if len(extra) > 0 {
				return nil, fmt.Errorf("node %q: the community node returned items on output %d, and it declares %d outputs", ir.Name, want+position+1, want)
			}
		}
		outputs = outputs[:want]
	}

	built := make(workflow.NodeOutput, want)
	for position := range built {
		built[position] = []workflow.Item{}
	}
	for position, stream := range outputs {
		items := make([]workflow.Item, 0, len(stream))
		for itemIndex, item := range stream {
			if len(item.Binary) > 0 {
				return nil, fmt.Errorf("node %q: output item %d of output %d carries a binary attachment, which the JavaScript sidecar does not support", ir.Name, itemIndex, position+1)
			}
			json := item.JSON
			if json == nil {
				json = map[string]any{}
			}
			// Paired is left nil on purpose: the runner infers lineage by
			// position when the item count matches, and a package that
			// reorders or filters must not have its own pairing overwritten.
			items = append(items, workflow.Item{JSON: json})
		}
		built[position] = items
	}
	return built, nil
}

// sidecarExpectedPorts is how many streams the engine expects from this node,
// which is the declared arity minus the error port the engine owns.
//
// It mirrors internal/engine's own rule (errorPortIndex/expectedPorts): the
// port exists only under continueErrorOutput, and only when the definition's
// last port is named "error". Conversion refuses a package that declares that
// name itself, so the two rules cannot disagree.
func sidecarExpectedPorts(ir workflow.IRNode) int {
	ports := len(ir.Definition.Outputs)
	if onErrorSetting(ir.Settings) != "continueErrorOutput" {
		return ports
	}
	if ports > 0 && ir.Definition.Outputs[ports-1].Name == "error" {
		return ports - 1
	}
	return ports
}

// onErrorSetting reads the node's error mode the way the engine does: n8n 1.x
// writes onError, and an imported 0.x document still writes continueOnFail.
func onErrorSetting(settings map[string]any) string {
	if value, ok := settings["onError"].(string); ok {
		return value
	}
	if continueOnFail, _ := settings["continueOnFail"].(bool); continueOnFail {
		return "continueRegularOutput"
	}
	return "stopWorkflow"
}

// scrubber redacts credential values from anything that leaves the host.
//
// A child's own error message, and the message this sidecar sends it back for a
// refused host call, are both places a secret can surface: a package that puts
// its API key in a URL has it echoed by every transport error, and a diagnostic
// is not a reason to move a secret into a log. Values shorter than four
// characters are skipped, because redacting "a" would redact everything.
type scrubber struct{ secrets []string }

func newScrubber(resolved []engine.Credential) scrubber {
	seen := map[string]bool{}
	secrets := make([]string, 0, len(resolved))
	for _, credential := range resolved {
		for _, value := range credential.Fields {
			if len(value) < 4 || seen[value] {
				continue
			}
			seen[value] = true
			secrets = append(secrets, value)
		}
	}
	// Longest first, so a secret that contains another is redacted whole.
	sort.Slice(secrets, func(left, right int) bool { return len(secrets[left]) > len(secrets[right]) })
	return scrubber{secrets: secrets}
}

func (scrubber scrubber) scrub(text string) string {
	for _, secret := range scrubber.secrets {
		text = strings.ReplaceAll(text, secret, "[credential withheld]")
	}
	return text
}
