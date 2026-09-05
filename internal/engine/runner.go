// Package engine executes compiled KilasFlow workflow graphs.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ExecutionContext is the identity a node may read: the `$execution`
// expression root exposes ID and Mode, while tenant and workflow are available
// to executors that need to scope storage, such as agent memory.
type ExecutionContext struct {
	ID         string
	Mode       string
	TenantID   string
	WorkflowID string
}

// NodeEvent is a nested occurrence an executor publishes while it runs, such
// as an agent's model turn or tool call.
//
// It exists so a long-running node can report progress through the one
// standardized execution event channel instead of inventing its own.
type NodeEvent struct {
	NodeID string
	Name   string
	Detail json.RawMessage
}

// NodeEventSink receives nested node events. It must never block the run: an
// implementation that cannot keep up drops rather than stalls, because event
// delivery is never allowed to gate execution.
type NodeEventSink func(NodeEvent)

// Emit is nil-safe so an executor need not guard every publish.
func (sink NodeEventSink) Emit(event NodeEvent) {
	if sink == nil {
		return
	}
	sink(event)
}

// CredentialResolver hands an executor the decrypted fields of a credential
// the node references.
//
// Executors never reach into storage themselves: keeping resolution behind
// this interface is what lets the runtime enforce tenant ownership, type, and
// domain scope in one place before a secret is ever handed out.
type CredentialResolver interface {
	ResolveCredential(ctx context.Context, credentialID string) (Credential, error)
}

// Credential is one resolved secret plus the scope that governs its use.
type Credential struct {
	ID             string
	Name           string
	Type           string
	Fields         map[string]string
	AllowedDomains []string
}

// Request is runtime input supplied by the trigger that starts a graph, plus
// the ambient context an executor needs to resolve expressions and
// authenticate.
type Request struct {
	Input     workflow.Item
	Execution ExecutionContext
	// Env is the allowlisted environment exposed as `$env`. The runtime decides
	// what enters it; nothing reads os.Environ during execution.
	Env         map[string]string
	Credentials CredentialResolver
	// Events publishes nested progress from inside a node.
	Events NodeEventSink
	// NodeOutputs maps a completed node's display name to its first output
	// item, backing the `$node` expression root. The runner fills it as the
	// graph progresses, so a node only ever sees nodes that ran before it.
	NodeOutputs map[string]map[string]any
	// TriggerNodeID names the trigger this execution starts from.
	//
	// A workflow may declare several — a webhook beside a nightly schedule is
	// the standard shape — and only one of them fires on any given run. Without
	// this, every root's executor would run seeded with the same item, so a
	// schedule trigger would fire on a webhook delivery.
	//
	// Empty means every root, which is what a manual run of a workflow means
	// and what preserves today's behaviour for a single-root graph.
	TriggerNodeID string
}

// Executor runs one registered node using its compiled configuration.
type Executor interface {
	Execute(context.Context, workflow.IRNode, workflow.NodeInput, Request) (workflow.NodeOutput, error)
}

// ExecutorFunc adapts a function to Executor.
type ExecutorFunc func(context.Context, workflow.IRNode, workflow.NodeInput, Request) (workflow.NodeOutput, error)

func (fn ExecutorFunc) Execute(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request Request) (workflow.NodeOutput, error) {
	return fn(ctx, node, input, request)
}

// Registry maps opaque, server-owned executor IDs to implementations.
type Registry struct {
	executors map[string]Executor
}

// NewRegistry creates an empty immutable-at-runtime executor registry.
func NewRegistry() *Registry {
	return &Registry{executors: make(map[string]Executor)}
}

// Register binds an executor ID during application composition.
func (registry *Registry) Register(id string, executor Executor) error {
	if registry == nil || id == "" || executor == nil {
		return fmt.Errorf("engine executor ID and implementation are required")
	}
	if _, found := registry.executors[id]; found {
		return fmt.Errorf("engine executor %q is already registered", id)
	}
	registry.executors[id] = executor
	return nil
}

// Lookup returns a registered executor. It is exported so a test can run the
// exact binding the engine would, rather than constructing an executor a
// different way and asserting on something the engine never uses.
func (registry *Registry) Lookup(id string) (Executor, bool) {
	if registry == nil {
		return nil, false
	}
	executor, found := registry.executors[id]
	return executor, found
}

// NodeRun is the deterministic in-memory result of one node invocation.
// Durable storage is injected later at the service boundary.
type NodeRun struct {
	NodeID string
	Input  workflow.NodeInput
	Output workflow.NodeOutput
	Error  error
	// Skipped marks a node the runner did not invoke because no incoming item
	// channel delivered anything — the untaken arm of a branch.
	//
	// It is recorded rather than omitted. A node that simply vanished from the
	// trace would make an execution look as though the branch never existed,
	// and the difference between "did not run" and "ran and produced nothing"
	// is exactly what someone reading a branch needs to see.
	Skipped   bool
	ErrorCode string
}

// Result contains node data in execution order and outputs of graph leaves.
type Result struct {
	NodeRuns []NodeRun
	Output   map[string]workflow.NodeOutput
}

// Runner schedules a compiled DAG. Nodes inside one execution run in stable
// topological order, eliminating race-dependent item concatenation while the
// service can still run independent executions concurrently.
type Runner struct {
	executors *Registry
}

func NewRunner(executors *Registry) *Runner {
	return &Runner{executors: executors}
}

// Run executes every node of one compiled graph exactly once.
func (runner *Runner) Run(ctx context.Context, ir workflow.IR, request Request) (Result, error) {
	if runner == nil || runner.executors == nil {
		return Result{}, fmt.Errorf("engine executor registry is required")
	}
	if len(ir.Nodes) == 0 {
		return Result{}, fmt.Errorf("compiled workflow graph is empty")
	}
	// Only the part of the graph belonging to this run's trigger executes. The
	// rest is not skipped node by node — it is absent, so a node fed by both
	// triggers waits only on the one that fired rather than deadlocking on the
	// one that did not.
	active, err := activeNodes(ir, request.TriggerNodeID)
	if err != nil {
		return Result{}, err
	}
	nodes := make(map[string]workflow.IRNode, len(active))
	for _, node := range ir.Nodes {
		if _, live := active[node.ID]; live {
			nodes[node.ID] = node
		}
	}
	incoming := make(map[string][]workflow.IREdge, len(nodes))
	outgoing := make(map[string]int, len(nodes))
	for _, edge := range ir.Edges {
		if _, sourceLive := active[edge.Source.NodeID]; !sourceLive {
			continue
		}
		if _, targetLive := active[edge.Target.NodeID]; !targetLive {
			continue
		}
		incoming[edge.Target.NodeID] = append(incoming[edge.Target.NodeID], edge)
		outgoing[edge.Source.NodeID]++
	}
	for nodeID := range incoming {
		sort.Slice(incoming[nodeID], func(left, right int) bool {
			a, b := incoming[nodeID][left], incoming[nodeID][right]
			if a.Target.Port != b.Target.Port {
				return a.Target.Port < b.Target.Port
			}
			if a.Source.NodeID != b.Source.NodeID {
				return a.Source.NodeID < b.Source.NodeID
			}
			if a.SourceOutputIndex != b.SourceOutputIndex {
				return a.SourceOutputIndex < b.SourceOutputIndex
			}
			return a.ID < b.ID
		})
	}

	completed := make(map[string]workflow.NodeOutput, len(nodes))
	// `$node["Name"]` reads the first item a completed node produced. Building
	// it here keeps the lookup ordered by actual execution, so a node can never
	// observe one that has not run yet.
	if request.NodeOutputs == nil {
		request.NodeOutputs = make(map[string]map[string]any, len(nodes))
	}
	result := Result{NodeRuns: make([]NodeRun, 0, len(nodes)), Output: make(map[string]workflow.NodeOutput)}
	for len(completed) < len(nodes) {
		ready := make([]string, 0, len(nodes)-len(completed))
		for nodeID := range nodes {
			if _, done := completed[nodeID]; done || !dependenciesComplete(incoming[nodeID], completed) {
				continue
			}
			ready = append(ready, nodeID)
		}
		if len(ready) == 0 {
			return Result{}, fmt.Errorf("compiled workflow graph has no schedulable node")
		}
		sort.Strings(ready)
		nodeID := ready[0]
		node := nodes[nodeID]
		input, err := nodeInput(incoming[nodeID], completed)
		if err != nil {
			return Result{}, fmt.Errorf("build node %q input: %w", nodeID, err)
		}

		// A node whose item channel delivered nothing is not run at all.
		//
		// Scheduling used to ask only whether every upstream node had
		// *completed*, so the untaken arm of an IF still fired one HTTP
		// request, one model call, one SQL statement and one webhook response —
		// because every executor reads an empty `main` port as "run once
		// against an empty $json". That is a cost and security problem as much
		// as a correctness one.
		//
		// Skipping is expressed as an all-empty output written straight into
		// `completed`, so the cascade downstream falls out of this same rule
		// instead of needing a second traversal.
		if !isLive(node, incoming[nodeID], completed) {
			skipped := make(workflow.NodeOutput, len(node.Definition.Outputs))
			for index := range skipped {
				skipped[index] = []workflow.Item{}
			}
			completed[nodeID] = skipped
			// Deliberately not registered in NodeOutputs: `$node["Name"]` must
			// keep reporting "not set" rather than an empty object, which would
			// read as a successful lookup of a node that never ran.
			result.NodeRuns = append(result.NodeRuns, NodeRun{
				NodeID: nodeID, Input: cloneInput(input), Output: skipped, Skipped: true,
			})
			if outgoing[nodeID] == 0 {
				result.Output[nodeID] = cloneOutput(skipped)
			}
			continue
		}

		executor, found := runner.executors.Lookup(node.Definition.ExecutorID)
		if !found {
			return Result{}, fmt.Errorf("node %q executor %q is not registered", nodeID, node.Definition.ExecutorID)
		}
		nodeCtx, cancel, timeout, err := nodeContext(ctx, node)
		if err != nil {
			result.NodeRuns = append(result.NodeRuns, NodeRun{NodeID: nodeID, Input: cloneInput(input), Error: err, ErrorCode: "config.invalid"})
			return result, err
		}
		output, err := executor.Execute(nodeCtx, node, cloneInput(input), cloneRequest(request))
		cancel()
		if err != nil {
			code := "node.failed"
			if timeout > 0 && errors.Is(nodeCtx.Err(), context.DeadlineExceeded) {
				code = "node.timeout"
			} else if timeout == 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "execution.timeout"
			}
			result.NodeRuns = append(result.NodeRuns, NodeRun{NodeID: nodeID, Input: cloneInput(input), Error: err, ErrorCode: code})
			return result, fmt.Errorf("execute node %q: %w", nodeID, err)
		}
		if got, want := len(output), len(node.Definition.Outputs); got != want {
			return Result{}, fmt.Errorf("node %q returned %d output streams, want %d", nodeID, got, want)
		}
		output = cloneOutput(output)
		completed[nodeID] = output
		if first, ok := firstItem(output); ok {
			request.NodeOutputs[node.Name] = first
		}
		result.NodeRuns = append(result.NodeRuns, NodeRun{NodeID: nodeID, Input: cloneInput(input), Output: output})
		if outgoing[nodeID] == 0 {
			result.Output[nodeID] = cloneOutput(output)
		}
	}
	return result, nil
}

// firstItem returns the first item of a node's first non-empty output port,
// which is what `$node["Name"]` exposes.
func firstItem(output workflow.NodeOutput) (map[string]any, bool) {
	for _, items := range output {
		if len(items) > 0 {
			return cloneMap(items[0].JSON), true
		}
	}
	return nil, false
}

func nodeContext(parent context.Context, node workflow.IRNode) (context.Context, context.CancelFunc, time.Duration, error) {
	value, found := node.Settings["timeoutSeconds"]
	if !found || value == nil {
		return parent, func() {}, 0, nil
	}
	seconds, err := timeoutSeconds(value)
	if err != nil {
		return parent, func() {}, 0, err
	}
	if seconds == 0 {
		return parent, func() {}, 0, nil
	}
	timeout := time.Duration(seconds * float64(time.Second))
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, timeout, nil
}

func timeoutSeconds(value any) (float64, error) {
	var seconds float64
	switch typed := value.(type) {
	case float64:
		seconds = typed
	case float32:
		seconds = float64(typed)
	case int:
		seconds = float64(typed)
	case int64:
		seconds = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("timeoutSeconds must be numeric")
		}
		seconds = parsed
	default:
		return 0, fmt.Errorf("timeoutSeconds must be numeric")
	}
	if seconds < 0 {
		return 0, fmt.Errorf("timeoutSeconds must not be negative")
	}
	return seconds, nil
}

// dependenciesComplete reports that every upstream node has reached a terminal
// state — run or skipped. It decides *when* a node may be considered, not
// whether it runs.
func dependenciesComplete(edges []workflow.IREdge, completed map[string]workflow.NodeOutput) bool {
	for _, edge := range edges {
		if _, found := completed[edge.Source.NodeID]; !found {
			return false
		}
	}
	return true
}

// isLive reports whether a node should actually be invoked.
//
// A node with no declared `main` input is a trigger or a sub-node and always
// runs. Otherwise it runs only when some incoming item edge delivered at least
// one item on the port it was wired to.
//
// Typed attachment edges never gate this. A chat model, a memory and a tool
// supply configuration rather than items, and an agent with no memory is a
// perfectly valid agent — gating on them would skip every agent in the product.
func isLive(node workflow.IRNode, edges []workflow.IREdge, completed map[string]workflow.NodeOutput) bool {
	var wantsItems bool
	for _, port := range node.Definition.Inputs {
		if port.Kind == workflow.ConnectionMain {
			wantsItems = true
			break
		}
	}
	if !wantsItems {
		return true
	}
	var itemEdges int
	for _, edge := range edges {
		if edge.Kind != workflow.ConnectionMain {
			continue
		}
		itemEdges++
		output := completed[edge.Source.NodeID]
		if edge.SourceOutputIndex >= 0 && edge.SourceOutputIndex < len(output) && len(output[edge.SourceOutputIndex]) > 0 {
			return true
		}
	}
	// A node that declares a main input but has none connected is not pruned:
	// the compiler already refuses that graph, and the executors' empty-item
	// substitution stays reachable for a node genuinely wired to nothing.
	return itemEdges == 0
}

func nodeInput(edges []workflow.IREdge, completed map[string]workflow.NodeOutput) (workflow.NodeInput, error) {
	input := make(workflow.NodeInput)
	for _, edge := range edges {
		output := completed[edge.Source.NodeID]
		if edge.SourceOutputIndex < 0 || edge.SourceOutputIndex >= len(output) {
			return nil, fmt.Errorf("source node %q did not return output port %q", edge.Source.NodeID, edge.Source.Port)
		}
		input[edge.Target.Port] = append(input[edge.Target.Port], cloneItems(output[edge.SourceOutputIndex])...)
	}
	return input, nil
}

func cloneRequest(request Request) Request {
	cloned := Request{
		Input:       cloneItem(request.Input),
		Execution:   request.Execution,
		Credentials: request.Credentials,
		Events:      request.Events,
		Env:         make(map[string]string, len(request.Env)),
		NodeOutputs: make(map[string]map[string]any, len(request.NodeOutputs)),
	}
	for key, value := range request.Env {
		cloned.Env[key] = value
	}
	for name, item := range request.NodeOutputs {
		cloned.NodeOutputs[name] = cloneMap(item)
	}
	return cloned
}

func cloneInput(input workflow.NodeInput) workflow.NodeInput {
	cloned := make(workflow.NodeInput, len(input))
	for port, items := range input {
		cloned[port] = cloneItems(items)
	}
	return cloned
}

func cloneOutput(output workflow.NodeOutput) workflow.NodeOutput {
	cloned := make(workflow.NodeOutput, len(output))
	for index, items := range output {
		cloned[index] = cloneItems(items)
	}
	return cloned
}

func cloneItems(items []workflow.Item) []workflow.Item {
	cloned := make([]workflow.Item, len(items))
	for index, item := range items {
		cloned[index] = cloneItem(item)
	}
	return cloned
}

func cloneItem(item workflow.Item) workflow.Item {
	cloned := workflow.Item{JSON: cloneMap(item.JSON)}
	if item.Binary != nil {
		cloned.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, value := range item.Binary {
			cloned.Binary[key] = value
		}
	}
	return cloned
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}

// activeNodes is the part of a compiled graph that one execution runs.
//
// A workflow may declare several trigger roots and only one of them fires on
// any given run, so starting from a named trigger means walking forward over
// the item channel from that node alone. Attachment providers — a chat model, a
// memory, a tool — are then walked *backwards* from whatever was reached, since
// they are upstream of the node they configure rather than downstream of a
// trigger, and would otherwise be excluded from every run.
//
// An empty trigger runs everything, which is what a manual run of a workflow
// means and what keeps a single-root graph behaving exactly as before.
func activeNodes(ir workflow.IR, triggerNodeID string) (map[string]struct{}, error) {
	active := make(map[string]struct{}, len(ir.Nodes))
	if triggerNodeID == "" {
		for _, node := range ir.Nodes {
			active[node.ID] = struct{}{}
		}
		return active, nil
	}

	var known bool
	for _, node := range ir.Nodes {
		if node.ID == triggerNodeID {
			known = true
		}
	}
	if !known {
		return nil, fmt.Errorf("execution names trigger node %q, which is not in this workflow", triggerNodeID)
	}

	active[triggerNodeID] = struct{}{}
	queue := []string{triggerNodeID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range ir.Edges {
			if edge.Kind != workflow.ConnectionMain || edge.Source.NodeID != current {
				continue
			}
			if _, seen := active[edge.Target.NodeID]; seen {
				continue
			}
			active[edge.Target.NodeID] = struct{}{}
			queue = append(queue, edge.Target.NodeID)
		}
	}

	// Repeat until nothing changes, so a provider attached to another provider
	// is reached too.
	for {
		grew := false
		for _, edge := range ir.Edges {
			if edge.Kind == workflow.ConnectionMain {
				continue
			}
			if _, targetLive := active[edge.Target.NodeID]; !targetLive {
				continue
			}
			if _, sourceLive := active[edge.Source.NodeID]; sourceLive {
				continue
			}
			active[edge.Source.NodeID] = struct{}{}
			grew = true
		}
		if !grew {
			break
		}
	}
	return active, nil
}
