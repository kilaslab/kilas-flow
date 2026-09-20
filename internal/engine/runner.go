// Package engine executes compiled KilasFlow workflow graphs.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// WorkflowCall is one Execute Workflow node's request.
type WorkflowCall struct {
	// WorkflowID is the workflow to run. It is always resolved inside the
	// calling execution's tenant, so an ID from another tenant does not exist.
	WorkflowID string
	// Items are what the sub-workflow's trigger emits.
	Items []workflow.Item
	// Wait is whether the caller needs the sub-workflow's output.
	//
	// Fire and forget still runs the child to completion — it simply does not
	// hand the items back. It is not "start it in the background": a child runs
	// inline in the caller's goroutine, and a detached one would need a worker
	// slot the parent is already holding.
	Wait bool
}

// WorkflowCallResult is what a sub-workflow handed back.
type WorkflowCallResult struct {
	ExecutionID string
	Items       []workflow.Item
}

// WorkflowInvoker runs another workflow of the same tenant.
type WorkflowInvoker interface {
	InvokeWorkflow(ctx context.Context, parent ExecutionContext, call WorkflowCall) (WorkflowCallResult, error)
}

// ExecutionContext is the identity a node may read: the `$execution`
// expression root exposes ID, Mode and the resume links, while tenant and
// workflow are available to executors that need to scope storage, such as
// agent memory.
type ExecutionContext struct {
	ID         string
	Mode       string
	TenantID   string
	WorkflowID string
	// ResumeURL is the per-run machine resume link ($execution.resumeUrl). It
	// is minted before the graph runs so a workflow can send it before
	// suspending; a run that never suspends discards its token and the link
	// answers 404. Empty when the service composed no links.
	ResumeURL string
	// ApprovalURL is the human page for the same token
	// ($execution.approvalUrl). Empty alongside ResumeURL.
	ApprovalURL string
	// ParentID is the execution that called this one, for a sub-workflow run.
	ParentID string
	// Stack is the workflow IDs already on the call chain, outermost first,
	// including this one.
	//
	// A stack beside a depth counter rather than instead of one. A counter alone
	// lets A→B→A→B run all the way to the limit and spend the whole budget
	// before failing; the stack refuses the second A immediately and can name
	// the cycle in the error, which is the difference between a message someone
	// can act on and a message that says a number was exceeded. Chains of
	// distinct workflows that never repeat are still capped by
	// MaxWorkflowCallDepth.
	Stack []string
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
	Env map[string]string
	// Binaries stores and reads item payloads.
	//
	// Threaded here alongside the credential resolver and for the same reason:
	// an executor must not reach into storage on its own, so the runtime stays
	// the single place tenant scoping is enforced.
	Binaries    BinaryStore
	Credentials CredentialResolver
	// Workflows runs another workflow of the same tenant.
	//
	// Threaded here beside the credential resolver and for the same reason: an
	// executor must never reach into storage itself, so the runtime stays the
	// single place tenant scoping and the call stack are enforced. Nil leaves
	// an Execute Workflow node failing with a clear message rather than
	// silently doing nothing.
	Workflows WorkflowInvoker
	// Events publishes nested progress from inside a node.
	Events NodeEventSink
	// NodeRunSink is called with every trace row the moment it is appended,
	// with its index in completion order, so a service can persist progress
	// while the execution is still running. Nil is a no-op.
	NodeRunSink func(index int, run NodeRun)
	// NodeStartSink is called with a node's ID just before its executor runs,
	// so a service can announce a node that has started — the half of live
	// progress a completed-row sink cannot report. Nil is a no-op.
	NodeStartSink func(nodeID string)
	// NodeOutputs maps a completed node's display name to its first output
	// item, backing the `$node` expression root. The runner fills it as the
	// graph progresses, so a node only ever sees nodes that ran before it.
	NodeOutputs map[string]map[string]any
	// NodeItems is the same nodes with their `json` wrapper and their whole
	// item list, backing `$('Name')` and `$node["Name"].json.…`. It is filled
	// alongside NodeOutputs rather than replacing it, so an expression written
	// either way resolves.
	NodeItems map[string]expression.NodeItem
	// Workflow identifies the workflow, backing `$workflow`.
	Workflow expression.WorkflowContext
	// NodeState is per-node memory the runner owns for the whole execution,
	// keyed by node ID.
	//
	// A node that needs to remember something between its own invocations — a
	// loop's cursor is the one node that does — reads and writes its entry
	// here. Keeping it in the runner rather than on the items the node emits
	// is what makes it survive a body node that replaces an item's fields
	// entirely, which is exactly how n8n's Split In Batches keeps its cursor in
	// node context. An executor may mutate its own node's map in place; the
	// runner passes the same map to every invocation of that node and persists
	// it with the wait checkpoint.
	NodeState map[string]map[string]any
	// TriggerNodeID names the trigger this execution starts from.
	//
	// A workflow may declare several — a webhook beside a nightly schedule is
	// the standard shape — and only one of them fires on any given run. Without
	// this, every root's executor would run seeded with the same item, so a
	// schedule trigger would fire on a webhook delivery.
	//
	// Empty means every root, which is the default a manual run keeps and what
	// preserves today's behaviour for a single-root graph. A manual run that
	// names a trigger carries the user's choice here instead, so a workflow
	// that declares several runs the one they picked rather than all of them.
	TriggerNodeID string
}

// BinaryStore is the slice of the payload store an executor may use.
//
// Scoped to this execution by the runtime before an executor sees it, so a node
// cannot read another tenant's payload even by holding its reference.
type BinaryStore interface {
	Put(name, mediaType string, body io.Reader) (workflow.BinaryRef, error)
	Get(id string) (io.ReadCloser, workflow.BinaryRef, error)
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
// Registered lists every bound executor ID, in a stable order.
//
// It exists so a test can assert the reverse of the binding invariant: an
// executor nobody points at is dead code left behind by a rename.
func (registry *Registry) Registered() []string {
	if registry == nil {
		return nil
	}
	ids := make([]string, 0, len(registry.executors))
	for id := range registry.executors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

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
	// Attempt is 1 for a first try and increments per retry. Every attempt is
	// its own row: the persistence layer has carried an Attempt column with a
	// unique index on (execution, node, attempt) since V1, and the service
	// hardcoded 1 into it.
	Attempt int
	// RunIndex is the Nth time this node ran in this execution, counting from
	// zero. It is deliberately separate from Attempt: attempt means "this is
	// retry N of the same run", run index means "this is the Nth time the node
	// ran", and conflating them makes a retry inside a loop unrepresentable.
	RunIndex int
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

// preparedGraph is the scheduling shape of one compiled run: the live nodes and
// their edges partitioned both ways. Both lists are sorted once, here, because
// the scheduler reads them in the order n8n would.
type preparedGraph struct {
	nodes    map[string]workflow.IRNode
	incoming map[string][]workflow.IREdge
	outgoing map[string][]workflow.IREdge
}

// prepareGraph builds the scheduling shape Run and Resume share from a
// compiled graph: only the trigger's own subgraph executes, so a node fed by
// two triggers waits only on the one that fired.
func prepareGraph(ir workflow.IR, triggerNodeID string) (preparedGraph, error) {
	if len(ir.Nodes) == 0 {
		return preparedGraph{}, fmt.Errorf("compiled workflow graph is empty")
	}
	// Only the part of the graph belonging to this run's trigger executes. The
	// rest is not skipped node by node — it is absent, so a node fed by both
	// triggers waits only on the one that fired rather than deadlocking on the
	// one that did not.
	active, err := activeNodes(ir, triggerNodeID)
	if err != nil {
		return preparedGraph{}, err
	}
	nodes := make(map[string]workflow.IRNode, len(active))
	for _, node := range ir.Nodes {
		if _, live := active[node.ID]; live {
			nodes[node.ID] = node
		}
	}
	if triggerNodeID != "" {
		if trigger, found := nodes[triggerNodeID]; found && trigger.Disabled {
			return preparedGraph{}, fmt.Errorf("execution names trigger node %q, which is disabled", triggerNodeID)
		}
	}
	incoming := make(map[string][]workflow.IREdge, len(nodes))
	outgoing := make(map[string][]workflow.IREdge, len(nodes))
	for _, edge := range ir.Edges {
		if _, sourceLive := active[edge.Source.NodeID]; !sourceLive {
			continue
		}
		if _, targetLive := active[edge.Target.NodeID]; !targetLive {
			continue
		}
		incoming[edge.Target.NodeID] = append(incoming[edge.Target.NodeID], edge)
		outgoing[edge.Source.NodeID] = append(outgoing[edge.Source.NodeID], edge)
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
	// Outgoing edges are ordered the way n8n schedules them: by output index,
	// then by where the target sits on the canvas (top to bottom, left to
	// right), and by node ID only as a tie-breaker. Imported workflows keep
	// n8n's random UUIDs, so an ID-ordered run visits parallel branches in an
	// order nobody drew — and a whole branch does not finish before the next
	// one starts.
	for nodeID := range outgoing {
		sort.Slice(outgoing[nodeID], func(left, right int) bool {
			a, b := outgoing[nodeID][left], outgoing[nodeID][right]
			if a.SourceOutputIndex != b.SourceOutputIndex {
				return a.SourceOutputIndex < b.SourceOutputIndex
			}
			first, second := nodes[a.Target.NodeID].Position, nodes[b.Target.NodeID].Position
			if first.Y != second.Y {
				return first.Y < second.Y
			}
			if first.X != second.X {
				return first.X < second.X
			}
			return a.Target.NodeID < b.Target.NodeID
		})
	}
	return preparedGraph{nodes: nodes, incoming: incoming, outgoing: outgoing}, nil
}

// Run executes every node of one compiled graph exactly once.
func (runner *Runner) Run(ctx context.Context, ir workflow.IR, request Request) (Result, error) {
	if runner == nil || runner.executors == nil {
		return Result{}, fmt.Errorf("engine executor registry is required")
	}
	graph, err := prepareGraph(ir, request.TriggerNodeID)
	if err != nil {
		return Result{}, err
	}
	// `$workflow` reads the compiled graph's identity: the caller supplies what
	// only it knows (whether the workflow is active), and the runner fills in
	// the rest rather than leaving `$workflow.name` empty in every production
	// run.
	request.Workflow = workflowContextFor(ir, request.Workflow)
	state := newRunState(graph, &request)
	suspended, err := runner.runLoop(ctx, graph, &request, state)
	if err != nil {
		return *state.result, err
	}
	if suspended != nil {
		return *state.result, suspended
	}
	return *state.result, nil
}

// pendingInvocation is one invocation the scheduler owes: a node, and the items
// a single upstream run delivered to it.
type pendingInvocation struct {
	nodeID string
	input  workflow.NodeInput
	// skipped marks a delivery that carried nothing: the node is not run, and
	// the trace records that a branch reached it and produced no items. It is
	// recorded when the delivery happens rather than at the end of the run, so
	// a node inside a loop keeps one pruned row per iteration.
	skipped bool
}

// runState is the live scheduling state of one pass: what has run, what the run
// still owes, and the trace being built.
type runState struct {
	completed map[string]workflow.NodeOutput
	runs      map[string][]workflow.NodeOutput
	result    *Result
	// pending is the execution stack, newest first. The branch the run is
	// already on sits on top, which is what makes the order depth-first.
	pending []pendingInvocation
}

// newRunState allocates the live state of one pass and seeds the execution
// stack with every node nothing feeds.
func newRunState(graph preparedGraph, request *Request) *runState {
	state := emptyRunState(graph)
	ensureRequestMaps(request, graph)
	state.seed(graph)
	return state
}

// emptyRunState allocates the live state of one pass without scheduling
// anything, which is what a resumed run needs: its work comes from the
// checkpoint.
func emptyRunState(graph preparedGraph) *runState {
	return &runState{
		completed: make(map[string]workflow.NodeOutput, len(graph.nodes)),
		runs:      make(map[string][]workflow.NodeOutput, len(graph.nodes)),
		result:    &Result{NodeRuns: make([]NodeRun, 0, len(graph.nodes)), Output: make(map[string]workflow.NodeOutput)},
	}
}

// ensureRequestMaps gives the run the expression views and the per-node state
// every executor expects to find.
func ensureRequestMaps(request *Request, graph preparedGraph) {
	// `$node["Name"]` reads the first item a completed node produced. Building
	// it as the graph progresses keeps the lookup ordered by actual execution,
	// so a node can never observe one that has not run yet.
	if request.NodeOutputs == nil {
		request.NodeOutputs = make(map[string]map[string]any, len(graph.nodes))
	}
	if request.NodeItems == nil {
		request.NodeItems = make(map[string]expression.NodeItem, len(graph.nodes))
	}
	if request.NodeState == nil {
		request.NodeState = make(map[string]map[string]any, len(graph.nodes))
	}
	// Every node gets its own state entry up front, because an executor mutates
	// it in place: a node that replaces its entry would write to a copy the
	// runner never sees.
	for nodeID := range graph.nodes {
		if _, exists := request.NodeState[nodeID]; !exists {
			request.NodeState[nodeID] = map[string]any{}
		}
	}
}

// seed schedules the nodes a run starts from: the ones nothing feeds, in canvas
// order so the topmost of them runs first.
//
// A disabled trigger is not among them. A workflow routinely carries a trigger
// its author switched off, and starting from it would run the very thing they
// turned off.
func (state *runState) seed(graph preparedGraph) {
	roots := make([]string, 0, 4)
	for nodeID, node := range graph.nodes {
		if len(graph.incoming[nodeID]) > 0 || node.Disabled {
			continue
		}
		roots = append(roots, nodeID)
	}
	sort.Slice(roots, func(left, right int) bool {
		first, second := graph.nodes[roots[left]].Position, graph.nodes[roots[right]].Position
		if first.Y != second.Y {
			return first.Y < second.Y
		}
		if first.X != second.X {
			return first.X < second.X
		}
		return roots[left] < roots[right]
	})
	for index := len(roots) - 1; index >= 0; index-- {
		state.pending = append(state.pending, pendingInvocation{nodeID: roots[index], input: workflow.NodeInput{}})
	}
}

// next returns the invocation to run next.
//
// The stack is what gives the run n8n's v1 order: a node pushes the branches it
// feeds, and the topmost of those runs to its end before the next one starts.
// The fallback below covers the nodes a stack cannot carry — a Merge, whose
// several item inputs have to be collected from every upstream run before it
// can run at all — and it only ever picks a node that has never run, so a
// branch the stack is holding is never overtaken.
func (state *runState) next(graph preparedGraph) (pendingInvocation, bool, error) {
	for index := len(state.pending) - 1; index >= 0; index-- {
		candidate := state.pending[index]
		if !state.configReady(graph, candidate.nodeID) {
			continue
		}
		// The invocation carries the items one branch delivered. Everything
		// else the node reads — its chat model, its memory, its tools — arrives
		// on typed ports from nodes that have already run, exactly as the
		// fallback below builds it.
		if err := state.mergeConfigInputs(graph, &candidate); err != nil {
			return pendingInvocation{}, false, err
		}
		state.pending = append(state.pending[:index], state.pending[index+1:]...)
		return candidate, true, nil
	}
	ready := make([]string, 0, len(graph.nodes))
	for nodeID := range graph.nodes {
		if _, done := state.completed[nodeID]; done {
			continue
		}
		if graph.nodes[nodeID].Disabled {
			continue
		}
		if !dependenciesComplete(graph.incoming[nodeID], state.completed) {
			continue
		}
		// A node nothing delivered items to is not run: that is what keeps the
		// untaken arm of an IF from firing. Its inputs are read from what its
		// upstream runs produced, and an empty stream is not work.
		if !delivered(graph, nodeID, state.completed) {
			continue
		}
		ready = append(ready, nodeID)
	}
	if len(ready) == 0 {
		return pendingInvocation{}, false, nil
	}
	sort.Strings(ready)
	nodeID := ready[0]
	input, err := nodeInput(graph.incoming[nodeID], state.completed)
	if err != nil {
		return pendingInvocation{}, false, fmt.Errorf("build node %q input: %w", nodeID, err)
	}
	return pendingInvocation{nodeID: nodeID, input: input}, true, nil
}

// delivered reports whether anything reached a node's item inputs.
//
// A node with no item input at all is not gated: a trigger and a sub-node are
// started by the graph, not by items. A node that declares one is started only
// when a branch delivered to it, which is the same rule the stack applies when
// it pushes a branch — the difference is that this one is read from the
// completed map, so it also covers the convergence nodes the stack leaves
// alone.
func delivered(graph preparedGraph, nodeID string, completed map[string]workflow.NodeOutput) bool {
	ports := itemInputPorts(graph.nodes[nodeID])
	if ports == 0 {
		return true
	}
	for _, edge := range graph.incoming[nodeID] {
		if edge.Kind != workflow.ConnectionMain {
			continue
		}
		output := completed[edge.Source.NodeID]
		if edge.SourceOutputIndex >= 0 && edge.SourceOutputIndex < len(output) && len(output[edge.SourceOutputIndex]) > 0 {
			return true
		}
	}
	// A node whose item port has no incoming edge at all cannot be delivered
	// to, and the compiler already refuses that graph; running it keeps the
	// executors' empty-item substitution reachable rather than changing what a
	// hand-written document does.
	for _, edge := range graph.incoming[nodeID] {
		if edge.Kind == workflow.ConnectionMain {
			return false
		}
	}
	return true
}

// mergeConfigInputs fills a scheduled invocation's typed inputs from the nodes
// that have already run.
//
// An attachment edge carries configuration rather than items, so it is never
// part of what a branch delivers: an agent started by its trigger still has to
// find its model on the model port, and a scheduled invocation that carried
// only the trigger's items would leave every attachment empty.
func (state *runState) mergeConfigInputs(graph preparedGraph, invocation *pendingInvocation) error {
	for _, edge := range graph.incoming[invocation.nodeID] {
		if edge.Kind == workflow.ConnectionMain {
			continue
		}
		output := state.completed[edge.Source.NodeID]
		if edge.SourceOutputIndex < 0 || edge.SourceOutputIndex >= len(output) {
			return fmt.Errorf("source node %q did not return output port %q", edge.Source.NodeID, edge.Source.Port)
		}
		invocation.input[edge.Target.Port] = append(invocation.input[edge.Target.Port], cloneItems(output[edge.SourceOutputIndex])...)
	}
	return nil
}

// configReady reports whether a scheduled invocation may run: every typed
// attachment edge into the node must have delivered, because a chat model, a
// memory and a tool supply configuration rather than items, and an agent
// started before its model has run would resolve nothing.
//
// Item edges deliberately do not gate this. The invocation already carries the
// items one branch delivered, which is what makes a node fed by two branches
// run once for each of them, as n8n does.
func (state *runState) configReady(graph preparedGraph, nodeID string) bool {
	for _, edge := range graph.incoming[nodeID] {
		if edge.Kind == workflow.ConnectionMain {
			continue
		}
		if _, done := state.completed[edge.Source.NodeID]; !done {
			return false
		}
	}
	return true
}

// complete records one finished invocation: its trace row, the expression view
// of the node, and the branches it feeds.
func (state *runState) complete(graph preparedGraph, node workflow.IRNode, input workflow.NodeInput, output workflow.NodeOutput, run NodeRun, request *Request) {
	state.completed[node.ID] = output
	state.runs[node.ID] = append(state.runs[node.ID], cloneOutput(output))
	run.RunIndex = len(state.runs[node.ID]) - 1
	run.Output = output
	if first, ok := firstItem(output); ok {
		request.NodeOutputs[node.Name] = first
	}
	request.NodeItems[node.Name] = nodeItemFor(node, output)
	state.record(run, request)
	if len(graph.outgoing[node.ID]) == 0 {
		state.result.Output[node.ID] = cloneOutput(output)
	}
	state.push(graph, node, output)
}

// record appends one trace row and hands it to the run's progress sink, which
// is how a running execution is visible before it finishes.
func (state *runState) record(run NodeRun, request *Request) {
	state.result.NodeRuns = append(state.result.NodeRuns, run)
	if request.NodeRunSink == nil {
		return
	}
	request.NodeRunSink(len(state.result.NodeRuns)-1, run)
}

// push schedules the branches this run feeds.
//
// One invocation per target, carrying the items this run delivered on the ports
// it delivered them on. That is what makes a node fed by two branches run once
// per branch instead of once with both streams concatenated, and a port that
// carried nothing delivers nothing, which is how the untaken arm of an IF
// prunes itself without a second traversal.
//
// Children are pushed in reverse, so the stack pops them in canvas order and
// each branch runs to its end before the next one starts.
func (state *runState) push(graph preparedGraph, node workflow.IRNode, output workflow.NodeOutput) {
	// Whether this run of a loop entry dispatched a batch. A node below the
	// loop's `done` port is not reached while it has: an empty `done` on a
	// dispatch is not a branch that produced nothing, it is a branch that has
	// not run yet, and recording it would put a pruned row in the trace for
	// every iteration.
	dispatched := node.Definition.LoopEntry && portHasItems(node, output, loopPortName)

	byTarget := make(map[string]*pendingInvocation, 2)
	ordered := make([]*pendingInvocation, 0, 2)
	for _, edge := range graph.outgoing[node.ID] {
		if edge.Kind != workflow.ConnectionMain {
			continue
		}
		if dispatched && edge.Source.Port == donePortName {
			continue
		}
		target := graph.nodes[edge.Target.NodeID]
		// A node with several item inputs is a convergence point: it needs
		// every branch's items at once, so it is left to the fallback rather
		// than started with one side of its input.
		if itemInputPorts(target) > 1 {
			continue
		}
		items := []workflow.Item{}
		if edge.SourceOutputIndex >= 0 && edge.SourceOutputIndex < len(output) {
			items = output[edge.SourceOutputIndex]
		}
		// Nothing on this port is nothing to deliver — unless the target asked
		// for an item regardless, which is what Always Output Data means.
		//
		// A loop entry is deliberately not exempt. It is invoked by the data its
		// body returns, and an empty return means the body produced nothing to
		// iterate: starting the next batch on it would both mis-order a nested
		// loop — the inner loop's `done` is empty on every iteration it has not
		// finished — and hand the outer loop work it never received.
		empty := len(items) == 0 && !settingBool(target.Settings, "alwaysOutputData")
		entry, found := byTarget[edge.Target.NodeID]
		if !found {
			entry = &pendingInvocation{nodeID: edge.Target.NodeID, input: workflow.NodeInput{}, skipped: empty}
			byTarget[edge.Target.NodeID] = entry
			ordered = append(ordered, entry)
		}
		if len(items) > 0 {
			entry.input[edge.Target.Port] = append(entry.input[edge.Target.Port], cloneItems(items)...)
			entry.skipped = false
		}
	}
	for index := len(ordered) - 1; index >= 0; index-- {
		state.pending = append(state.pending, *ordered[index])
	}
}

// recordSkips writes a row for every node this run never reached.
//
// A node is reached by a branch delivering items to it, so one that nothing
// delivered to did not run. Recording it as skipped is what keeps the
// difference between "did not run" and "ran and produced nothing" visible in
// the trace.
func (state *runState) recordSkips(graph preparedGraph, request *Request) {
	skipped := make([]string, 0, len(graph.nodes))
	for nodeID := range graph.nodes {
		if _, done := state.completed[nodeID]; done {
			continue
		}
		skipped = append(skipped, nodeID)
	}
	sort.Strings(skipped)
	for _, nodeID := range skipped {
		node := graph.nodes[nodeID]
		empty := make(workflow.NodeOutput, len(node.Definition.Outputs))
		for index := range empty {
			empty[index] = []workflow.Item{}
		}
		state.completed[nodeID] = empty
		state.runs[nodeID] = append(state.runs[nodeID], cloneOutput(empty))
		state.record(NodeRun{NodeID: nodeID, Skipped: true, Output: empty}, request)
		if len(graph.outgoing[nodeID]) == 0 {
			state.result.Output[nodeID] = cloneOutput(empty)
		}
	}
}

// snapshotCheckpoint copies the live run state at a suspension. The copies are
// cheap insurance: the in-memory run ends here, but aliasing its maps into
// durable storage would corrupt the checkpoint the moment any future change
// touches them before the marshal.
func snapshotCheckpoint(nodeID, mode string, input workflow.NodeInput, attempt int, state *runState, request *Request) Checkpoint {
	checkpoint := Checkpoint{
		SuspendNode: nodeID, SuspendAttempt: attempt, Mode: mode,
		TriggerNodeID: request.TriggerNodeID,
		Input:         cloneInput(input),
		Completed:     make(map[string]workflow.NodeOutput, len(state.completed)),
		Runs:          make(map[string][]workflow.NodeOutput, len(state.runs)),
		NodeOutputs:   make(map[string]map[string]any, len(request.NodeOutputs)),
		NodeItems:     make(map[string]expression.NodeItem, len(request.NodeItems)),
		NodeState:     cloneNodeState(request.NodeState),
		Output:        make(map[string]workflow.NodeOutput, len(state.result.Output)),
		Pending:       make([]PendingNode, 0, len(state.pending)),
	}
	for id, output := range state.completed {
		checkpoint.Completed[id] = cloneOutput(output)
	}
	for id, outputs := range state.runs {
		restored := make([]workflow.NodeOutput, 0, len(outputs))
		for _, output := range outputs {
			restored = append(restored, cloneOutput(output))
		}
		checkpoint.Runs[id] = restored
	}
	for name, fields := range request.NodeOutputs {
		restored := make(map[string]any, len(fields))
		for key, value := range fields {
			restored[key] = value
		}
		checkpoint.NodeOutputs[name] = restored
	}
	for name, item := range request.NodeItems {
		checkpoint.NodeItems[name] = item
	}
	for id, output := range state.result.Output {
		checkpoint.Output[id] = cloneOutput(output)
	}
	// The stack is the work the run still owes. Without it a resumed run would
	// lose every branch that was waiting behind the one that suspended, and the
	// branch that continues would look like the whole graph.
	for _, invocation := range state.pending {
		checkpoint.Pending = append(checkpoint.Pending, PendingNode{
			NodeID: invocation.nodeID, Input: cloneInput(invocation.input),
		})
	}
	return checkpoint
}

// Resume continues a run suspended at checkpoint.SuspendNode, completing that
// node with resumeOutput and then running the same pass Run would have. Nodes
// completed before suspension never execute again: their outputs arrive in
// the checkpoint, not from a second run.
func (runner *Runner) Resume(ctx context.Context, ir workflow.IR, request Request, checkpoint Checkpoint, resumeOutput workflow.NodeOutput) (Result, error) {
	if runner == nil || runner.executors == nil {
		return Result{}, fmt.Errorf("engine executor registry is required")
	}
	if checkpoint.TriggerNodeID != request.TriggerNodeID {
		return Result{}, fmt.Errorf("wait checkpoint is for a different trigger")
	}
	graph, err := prepareGraph(ir, request.TriggerNodeID)
	if err != nil {
		return Result{}, err
	}
	node, found := graph.nodes[checkpoint.SuspendNode]
	if !found {
		return Result{}, fmt.Errorf("suspended node %q is not in the compiled graph", checkpoint.SuspendNode)
	}
	if _, done := checkpoint.Completed[checkpoint.SuspendNode]; done {
		return Result{}, fmt.Errorf("suspended node %q already completed", checkpoint.SuspendNode)
	}
	if got, want := len(resumeOutput), expectedPorts(node); got != want {
		return Result{}, fmt.Errorf("resume output for node %q has %d streams, want %d", checkpoint.SuspendNode, got, want)
	}
	request.Workflow = workflowContextFor(ir, request.Workflow)
	// Not seeded: a resumed run continues the stack the checkpoint recorded,
	// and pushing the graph's roots again would run the trigger a second time.
	state := &runState{
		completed: make(map[string]workflow.NodeOutput, len(graph.nodes)),
		runs:      make(map[string][]workflow.NodeOutput, len(graph.nodes)),
		result:    &Result{NodeRuns: make([]NodeRun, 0, len(graph.nodes)), Output: make(map[string]workflow.NodeOutput)},
	}
	ensureRequestMaps(&request, graph)
	for id, output := range checkpoint.Completed {
		state.completed[id] = output
	}
	for id, outputs := range checkpoint.Runs {
		state.runs[id] = outputs
	}
	for name, fields := range checkpoint.NodeOutputs {
		request.NodeOutputs[name] = fields
	}
	for name, item := range checkpoint.NodeItems {
		request.NodeItems[name] = item
	}
	// Loop state comes back with the run: a loop that suspended inside its body
	// must resume on the batch it was on, not start again from the first one.
	for id, fields := range checkpoint.NodeState {
		request.NodeState[id] = cloneStateFields(fields)
	}
	for id, output := range checkpoint.Output {
		state.result.Output[id] = output
	}
	// The work the suspended run had not reached, restored below the branch the
	// suspended node is about to continue.
	for _, invocation := range checkpoint.Pending {
		state.pending = append(state.pending, pendingInvocation{
			nodeID: invocation.NodeID, input: cloneInput(invocation.Input),
		})
	}
	// The suspending node completes here, mirroring a normal completion:
	// provenance, expression state, the trace row, and the branch it feeds all
	// behave as if the node had just run.
	output := withErrorPort(node, cloneOutput(resumeOutput))
	stampProvenance(node, graph.incoming[checkpoint.SuspendNode], checkpoint.Input, output, len(state.runs[checkpoint.SuspendNode]))
	attempt := checkpoint.SuspendAttempt
	if attempt < 1 {
		attempt = 1
	}
	state.complete(graph, node, checkpoint.Input, output, NodeRun{
		NodeID: node.ID, Input: cloneInput(checkpoint.Input), Attempt: attempt,
	}, &request)
	suspended, err := runner.runLoop(ctx, graph, &request, state)
	if err != nil {
		return *state.result, err
	}
	if suspended != nil {
		return *state.result, suspended
	}
	return *state.result, nil
}

// runLoop schedules every node the graph still owes. It is the single pass
// Run and Resume share: Run starts it with the graph's roots, Resume starts it
// from a checkpoint. A suspension returns the signal with the checkpoint
// attached, alongside the runs produced so far; any other error fails the run.
func (runner *Runner) runLoop(ctx context.Context, graph preparedGraph, request *Request, state *runState) (*SuspendError, error) {
	for {
		// Cancellation floor. A holder in another process learns about
		// Cancel by polling the row and interrupting this context; the
		// check here is what turns that interrupt into a stop between
		// nodes. An executor that ignores its context still finishes its
		// current node, but the next node never starts: without this, a
		// cancelled run would execute every remaining node and only be
		// relabelled cancelled when the terminal write raced the request.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		invocation, found, err := state.next(graph)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		suspended, err := runner.runNode(ctx, graph, request, state, invocation)
		if err != nil {
			return nil, err
		}
		if suspended != nil {
			return suspended, nil
		}
	}
	state.recordSkips(graph, request)
	return nil, nil
}

// runNode executes one scheduled invocation.
func (runner *Runner) runNode(ctx context.Context, graph preparedGraph, request *Request, state *runState, invocation pendingInvocation) (*SuspendError, error) {
	node := graph.nodes[invocation.nodeID]
	// A delivery that carried nothing reaches the node without running it: the
	// untaken arm of a branch is recorded, and pruned the same way further
	// down, rather than vanishing from the trace.
	if invocation.skipped {
		state.skip(graph, invocation, request)
		return nil, nil
	}
	// A disabled node is never invoked. It passes its first main input through,
	// which is what n8n does: switching a node off in the middle of a chain
	// leaves the chain working, with that node's own edit missing.
	if node.Disabled {
		state.complete(graph, node, invocation.input, passThrough(node, invocation.input), NodeRun{
			NodeID: node.ID, Input: cloneInput(invocation.input),
		}, request)
		return nil, nil
	}
	if _, found := runner.executors.Lookup(node.Definition.ExecutorID); !found {
		return nil, fmt.Errorf("node %q executor %q is not registered", node.ID, node.Definition.ExecutorID)
	}
	if request.NodeStartSink != nil {
		request.NodeStartSink(node.ID)
	}
	policy := retryPolicy(node.Settings)
	input := invocation.input
	if settingBool(node.Settings, "executeOnce") {
		// Execute Once means the node sees the first item, not the batch.
		input = firstItemOnly(input)
	}

	// A node that tolerates failures is resolved one item at a time, because
	// n8n resolves continueOnFail inside the item loop: one failing item must
	// not discard the items that already succeeded or skip the ones after it.
	if perItemTolerance(node, policy) && len(input[mainPortName]) > 0 {
		return runner.runPerItem(ctx, graph, request, state, node, input, policy)
	}

	output, cause, code, attempt, suspended, err := runner.invoke(ctx, node, input, request, state, policy)
	if err != nil {
		return nil, err
	}
	if suspended != nil {
		return suspended, nil
	}
	if cause != nil {
		if policy.onError == errorStop {
			state.record(NodeRun{NodeID: node.ID, Input: cloneInput(input), Error: cause, Attempt: attempt, ErrorCode: code}, request)
			return nil, fmt.Errorf("execute node %q: %w", node.ID, cause)
		}
		// Tolerated: the node emits error items rather than aborting, and the
		// run carries on. The items are not the input passed through — a
		// downstream node has to be able to tell a tolerated failure from a
		// success.
		output = toleratedOutput(node, input, cause, policy.onError)
		state.complete(graph, node, input, output, NodeRun{
			NodeID: node.ID, Input: cloneInput(input), Error: cause, Attempt: attempt, ErrorCode: code,
		}, request)
		return nil, nil
	}

	if got, want := len(output), expectedPorts(node); got != want {
		return nil, fmt.Errorf("node %q returned %d output streams, want %d", node.ID, got, want)
	}
	output = withErrorPort(node, cloneOutput(output))
	if settingBool(node.Settings, "alwaysOutputData") {
		output = withEmptyItem(output)
	}
	// Provenance the executor did not set is inferred by position, but only
	// when that inference is actually sound: exactly one incoming item port
	// that delivered, and a matching item count. An executor that reorders or
	// filters must set its own, which is why IF and Merge do.
	stampProvenance(node, graph.incoming[node.ID], input, output, len(state.runs[node.ID]))
	state.complete(graph, node, input, output, NodeRun{
		NodeID: node.ID, Input: cloneInput(input), Attempt: attempt,
	}, request)
	return nil, nil
}

// invoke runs one node invocation with its retry budget.
//
// It returns the output on success, or the last cause with the code that
// classifies it. A suspension is neither: it ends the run with no retry and no
// failure row, and the caller hands the signal back to the service.
func (runner *Runner) invoke(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request *Request, state *runState, policy retry) (workflow.NodeOutput, error, string, int, *SuspendError, error) {
	executor, _ := runner.executors.Lookup(node.Definition.ExecutorID)
	var (
		output  workflow.NodeOutput
		cause   error
		code    string
		attempt int
	)
	for attempt = 1; attempt <= policy.attempts; attempt++ {
		// The per-node context is rebuilt each time round, because its cancel
		// must fire per attempt rather than once for all of them — a shared
		// deadline would make the second attempt inherit the first one's
		// remaining time.
		nodeCtx, cancel, timeout, err := nodeContext(ctx, node)
		if err != nil {
			state.record(NodeRun{NodeID: node.ID, Input: cloneInput(input), Error: err, Attempt: attempt, ErrorCode: "config.invalid"}, request)
			return nil, nil, "", attempt, nil, err
		}
		output, err = executor.Execute(nodeCtx, node, cloneInput(input), cloneRequest(*request))
		cancel()
		var suspended *SuspendError
		if err != nil && errors.As(err, &suspended) {
			raw, cerr := marshalCheckpoint(snapshotCheckpoint(node.ID, suspended.Mode, input, attempt, state, request))
			if cerr != nil {
				return nil, nil, "", attempt, nil, cerr
			}
			suspended.NodeID = node.ID
			suspended.Checkpoint = raw
			return nil, nil, "", attempt, suspended, nil
		}
		if err == nil {
			return output, nil, "", attempt, nil, nil
		}
		cause, code = err, "node.failed"
		if timeout > 0 && errors.Is(nodeCtx.Err(), context.DeadlineExceeded) {
			code = "node.timeout"
		} else if timeout == 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "execution.timeout"
		}
		// A failed attempt is a row of its own, so a reader can see that a node
		// succeeded on its third try rather than only that it succeeded.
		if attempt < policy.attempts {
			state.record(NodeRun{NodeID: node.ID, Input: cloneInput(input), Error: err, Attempt: attempt, ErrorCode: code}, request)
			if !sleepBetweenAttempts(ctx, policy.wait) {
				// The execution was cancelled while waiting; stop here rather
				// than burning the remaining attempts.
				state.record(NodeRun{NodeID: node.ID, Input: cloneInput(input), Error: ctx.Err(), Attempt: attempt + 1, ErrorCode: "execution.cancelled"}, request)
				return nil, nil, "", attempt, nil, fmt.Errorf("execute node %q: %w", node.ID, ctx.Err())
			}
		}
	}
	return output, cause, code, attempt - 1, nil, nil
}

// runPerItem runs a node once per input item, so a tolerated failure costs the
// failing item and nothing else.
func (runner *Runner) runPerItem(ctx context.Context, graph preparedGraph, request *Request, state *runState, node workflow.IRNode, input workflow.NodeInput, policy retry) (*SuspendError, error) {
	items := input[mainPortName]
	assembled := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range assembled {
		assembled[index] = []workflow.Item{}
	}
	errorPort := errorPortIndex(node)
	var firstCause error
	failed := 0
	for position, item := range items {
		output, cause, _, _, suspended, err := runner.invoke(ctx, node, singleItemInput(input, item), request, state, policy)
		if err != nil {
			return nil, err
		}
		if suspended != nil {
			// The items this node has not reached yet are scheduled, so a
			// resumed run finishes them instead of dropping them.
			if remaining := items[position+1:]; len(remaining) > 0 {
				state.pending = append(state.pending, pendingInvocation{
					nodeID: node.ID, input: singleItemInput(input, remaining...),
				})
			}
			return suspended, nil
		}
		if cause != nil {
			failed++
			if firstCause == nil {
				firstCause = cause
			}
			failedItem := errorItem(node, item, cause, policy.onError == errorBranch)
			if errorPort >= 0 && policy.onError == errorBranch {
				assembled[errorPort] = append(assembled[errorPort], failedItem)
			} else {
				assembled[0] = append(assembled[0], failedItem)
			}
			continue
		}
		mergeOutput(assembled, output)
	}
	run := NodeRun{NodeID: node.ID, Input: cloneInput(input), Error: firstCause}
	if firstCause != nil {
		run.ErrorCode = "node.partial"
		if failed == len(items) {
			run.ErrorCode = "node.failed"
		}
	}
	stampProvenance(node, graph.incoming[node.ID], input, assembled, len(state.runs[node.ID]))
	state.complete(graph, node, input, assembled, run, request)
	return nil, nil
}

// skip records a node a branch reached with no items, and passes the same
// emptiness on: the cascade downstream falls out of this one rule instead of
// needing a second traversal.
func (state *runState) skip(graph preparedGraph, invocation pendingInvocation, request *Request) {
	node := graph.nodes[invocation.nodeID]
	empty := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range empty {
		empty[index] = []workflow.Item{}
	}
	state.completed[node.ID] = empty
	state.runs[node.ID] = append(state.runs[node.ID], cloneOutput(empty))
	state.record(NodeRun{NodeID: node.ID, Input: cloneInput(invocation.input), Skipped: true, Output: empty}, request)
	if len(graph.outgoing[node.ID]) == 0 {
		state.result.Output[node.ID] = cloneOutput(empty)
	}
	// Deliberately not pushed on: emptiness travels no further than the node it
	// reached. Everything below an unreached node is recorded as skipped at the
	// end of the run, and pushing emptiness around a loop's back edge would
	// never stop.
}

// passThrough is what a disabled node emits: its first main input, unchanged.
func passThrough(node workflow.IRNode, input workflow.NodeInput) workflow.NodeOutput {
	output := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range output {
		output[index] = []workflow.Item{}
	}
	if len(output) == 0 {
		return output
	}
	// The first declared item input is the one n8n forwards. A disabled node
	// with no item input has nothing to pass and simply does not run.
	for _, port := range node.Definition.Inputs {
		if port.Kind != workflow.ConnectionMain {
			continue
		}
		output[0] = cloneItems(input[port.Name])
		break
	}
	return output
}

// singleItemInput is one invocation's input for a single item (or a run of
// them): the item ports other than the main one are configuration and stay as
// they are.
func singleItemInput(input workflow.NodeInput, items ...workflow.Item) workflow.NodeInput {
	single := make(workflow.NodeInput, len(input))
	for port, portItems := range input {
		if port == mainPortName {
			continue
		}
		single[port] = portItems
	}
	single[mainPortName] = cloneItems(items)
	return single
}

// mergeOutput appends one invocation's streams onto the assembled result, port
// by port: a node that routes an item to its second port, such as an IF, has to
// have both ports collected across the items it was given.
func mergeOutput(assembled workflow.NodeOutput, output workflow.NodeOutput) {
	for index, port := range output {
		if index >= len(assembled) {
			break
		}
		assembled[index] = append(assembled[index], port...)
	}
}

// withEmptyItem is Always Output Data: a node that produced nothing still hands
// downstream an item, so the branch does not silently stop.
func withEmptyItem(output workflow.NodeOutput) workflow.NodeOutput {
	if len(output) == 0 {
		return output
	}
	for _, port := range output {
		if len(port) > 0 {
			return output
		}
	}
	output[0] = []workflow.Item{{JSON: map[string]any{}}}
	return output
}

// firstItemOnly is Execute Once: the node sees the first item of its main input
// rather than the batch.
func firstItemOnly(input workflow.NodeInput) workflow.NodeInput {
	items := input[mainPortName]
	if len(items) <= 1 {
		return input
	}
	truncated := make(workflow.NodeInput, len(input))
	for port, portItems := range input {
		truncated[port] = portItems
	}
	truncated[mainPortName] = items[:1]
	return truncated
}

// The two ports a loop node declares, named as the node declares them.
const (
	donePortName = "done"
	loopPortName = "loop"
)

// portHasItems reports whether one of a node's declared output ports carried
// items in this run.
func portHasItems(node workflow.IRNode, output workflow.NodeOutput, port string) bool {
	for index, declared := range node.Definition.Outputs {
		if declared.Name != port || index >= len(output) {
			continue
		}
		return len(output[index]) > 0
	}
	return false
}

// errorPortName is the extra output a node that continues on a separate error
// branch declares. The compiler appends it from the node's own setting, so a
// workflow with a wired error branch compiles and runs.
const errorPortName = "error"

// errorPortIndex is where that port sits, or -1 when the node has none.
func errorPortIndex(node workflow.IRNode) int {
	if onErrorMode(node.Settings) != errorBranch {
		return -1
	}
	last := len(node.Definition.Outputs) - 1
	if last >= 0 && node.Definition.Outputs[last].Name == errorPortName {
		return last
	}
	return -1
}

// expectedPorts is how many streams the executor returns. The error port is the
// runner's, not the executor's: an executor knows nothing about error routing.
func expectedPorts(node workflow.IRNode) int {
	if index := errorPortIndex(node); index >= 0 {
		return index
	}
	return len(node.Definition.Outputs)
}

// withErrorPort pads a run's output to the declared arity, leaving the error
// port empty when nothing failed.
func withErrorPort(node workflow.IRNode, output workflow.NodeOutput) workflow.NodeOutput {
	index := errorPortIndex(node)
	if index < 0 || index < len(output) {
		return output
	}
	padded := make(workflow.NodeOutput, index+1)
	copy(padded, output)
	for position := len(output); position <= index; position++ {
		padded[position] = []workflow.Item{}
	}
	return padded
}

// errorItem is one tolerated failure in n8n's shape: the error itself on the
// main output, and the input item plus the error on the error output, so an
// imported check such as `{{ $json.error }}` matches and the item keeps its
// paired-item lineage.
func errorItem(node workflow.IRNode, item workflow.Item, cause error, withInput bool) workflow.Item {
	fields := make(map[string]any, len(item.JSON)+1)
	if withInput {
		for key, value := range item.JSON {
			fields[key] = cloneValue(value)
		}
	}
	fields[ErrorItemKey] = map[string]any{
		"message": cause.Error(),
		"node":    node.Name,
	}
	return workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired}
}

// toleratedOutput is a node-level tolerated failure: one error item per input
// item, so downstream item counts and paired-item lineage survive, and a single
// item when the node had no input to pair against.
func toleratedOutput(node workflow.IRNode, input workflow.NodeInput, cause error, mode errorMode) workflow.NodeOutput {
	output := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range output {
		output[index] = []workflow.Item{}
	}
	items := make([]workflow.Item, 0, len(input[mainPortName]))
	for _, item := range input[mainPortName] {
		items = append(items, errorItem(node, item, cause, mode == errorBranch))
	}
	if len(items) == 0 {
		items = []workflow.Item{errorItem(node, workflow.Item{JSON: map[string]any{}}, cause, mode == errorBranch)}
	}
	if index := errorPortIndex(node); index >= 0 && mode == errorBranch {
		output[index] = items
		return output
	}
	if len(output) > 0 {
		output[0] = items
	}
	return output
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

// settingNumber coerces a node setting to a number.
//
// Settings arrive from JSON, so an integer is a float64 and a json.Number is
// possible depending on how the document was decoded. One helper rather than a
// switch per setting: three copies of this had already started to appear.
func settingNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// settingBool coerces a node setting to a boolean, tolerating the string forms
// a hand-written document or an import can produce.
func settingBool(settings map[string]any, key string) bool {
	value, found := settings[key]
	if !found || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true"
	default:
		return false
	}
}

func timeoutSeconds(value any) (float64, error) {
	seconds, ok := settingNumber(value)
	if !ok {
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

// cloneRequest is what each executor is handed.
//
// Every field an expression can read has to be here. `NodeItems`, `Workflow`
// and `TriggerNodeID` were not, which meant `$('Name')` — the form every
// imported n8n workflow uses to read an earlier node — resolved to "that node
// has not produced output in this run" no matter what had run. The evaluator
// was right and the data never reached it.
func cloneRequest(request Request) Request {
	cloned := Request{
		Input:         cloneItem(request.Input),
		Execution:     request.Execution,
		TriggerNodeID: request.TriggerNodeID,
		Workflow:      request.Workflow,
		Binaries:      request.Binaries,
		Credentials:   request.Credentials,
		Events:        request.Events,
		NodeRunSink:   request.NodeRunSink,
		NodeStartSink: request.NodeStartSink,
		Workflows:     request.Workflows,
		Env:           make(map[string]string, len(request.Env)),
		NodeOutputs:   make(map[string]map[string]any, len(request.NodeOutputs)),
		NodeItems:     make(map[string]expression.NodeItem, len(request.NodeItems)),
		NodeState:     make(map[string]map[string]any, len(request.NodeState)),
	}
	for key, value := range request.Env {
		cloned.Env[key] = value
	}
	for name, item := range request.NodeOutputs {
		cloned.NodeOutputs[name] = cloneMap(item)
	}
	// A shallow copy of the map: the runner replaces whole entries as the graph
	// progresses rather than mutating one in place, so an executor holding this
	// map cannot see a half-written node.
	for name, item := range request.NodeItems {
		cloned.NodeItems[name] = item
	}
	// The outer map is copied so one node cannot swap another's state entry out
	// from under the runner, but the inner maps are shared deliberately: a
	// node's own state is meant to be mutated across its invocations, and a
	// copy here would throw away every write a loop makes.
	for id, state := range request.NodeState {
		cloned.NodeState[id] = state
	}
	return cloned
}

// cloneNodeState copies every node's state at a checkpoint boundary. The inner
// maps are copied here, unlike cloneRequest, because the checkpoint outlives
// the invocation that wrote it.
func cloneNodeState(source map[string]map[string]any) map[string]map[string]any {
	cloned := make(map[string]map[string]any, len(source))
	for id, fields := range source {
		cloned[id] = cloneStateFields(fields)
	}
	return cloned
}

func cloneStateFields(fields map[string]any) map[string]any {
	if fields == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		if items, ok := value.([]workflow.Item); ok {
			// What a loop holds between batches, in memory.
			cloned[key] = cloneItems(items)
			continue
		}
		cloned[key] = cloneValue(value)
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

// cloneItem is the choke point every item passes through, which is why
// provenance is copied here rather than at each call site: missing it here is
// the kind of failure that stays invisible until one specific graph shape hits
// it.
func cloneItem(item workflow.Item) workflow.Item {
	cloned := workflow.Item{JSON: cloneMap(item.JSON)}
	if item.Binary != nil {
		cloned.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, value := range item.Binary {
			cloned.Binary[key] = value
		}
	}
	if item.Paired != nil {
		paired := *item.Paired
		cloned.Paired = &paired
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

// errorMode is how a node handles a failure it could not retry away.
type errorMode int

const (
	// errorStop fails the execution. It is the default, and what n8n's
	// stopWorkflow means.
	errorStop errorMode = iota
	// errorRegular passes an error item on the node's own main output and keeps
	// the run going. It is what legacy continueOnFail and n8n's
	// continueRegularOutput both mean.
	errorRegular
	// errorBranch routes failed items to the node's own error output, so a
	// workflow can handle them without stopping.
	errorBranch
)

// onErrorMode reads the node's error handling.
//
// n8n 1.x writes onError; the 0.x UI wrote continueOnFail, and imported
// documents still carry it. They are the same setting under two names, and the
// importer maps the old one onto the new, so both are read here.
func onErrorMode(settings map[string]any) errorMode {
	if value, ok := settings["onError"].(string); ok {
		switch value {
		case "continueRegularOutput":
			return errorRegular
		case "continueErrorOutput":
			return errorBranch
		case "stopWorkflow":
			return errorStop
		}
	}
	if settingBool(settings, "continueOnFail") {
		return errorRegular
	}
	return errorStop
}

// retry is how a node handles its own failure.
type retry struct {
	// attempts is the total number of tries, so 1 means no retry at all.
	attempts int
	// wait is the delay between attempts.
	wait time.Duration
	// onError is what happens when every attempt failed.
	onError errorMode
}

// defaultRetryWait is used when retryOnFail is on and no delay was given.
//
// It is non-zero deliberately: without it, ticking Retry on Fail against a
// rate-limited API sends every attempt inside a millisecond, turning one
// failing request into a burst against an upstream that is already struggling.
const defaultRetryWait = time.Second

// retryPolicy reads the settings every node declares.
//
// The compiler has already refused an out-of-range value, so this clamps rather
// than errors: a document that reached the runner has valid settings, and a
// second error path here would only be reachable if that stopped being true.
func retryPolicy(settings map[string]any) retry {
	policy := retry{attempts: 1, wait: 0, onError: onErrorMode(settings)}
	if !settingBool(settings, "retryOnFail") {
		return policy
	}
	policy.attempts = 3
	if value, found := settings["maxTries"]; found && value != nil {
		if tries, ok := settingNumber(value); ok && tries >= 1 {
			policy.attempts = int(tries)
			if policy.attempts > workflow.MaxRetryAttempts {
				policy.attempts = workflow.MaxRetryAttempts
			}
		}
	}
	policy.wait = defaultRetryWait
	if value, found := settings["waitBetweenTries"]; found && value != nil {
		if milliseconds, ok := settingNumber(value); ok && milliseconds >= 0 {
			if milliseconds > workflow.MaxRetryWaitMilliseconds {
				milliseconds = workflow.MaxRetryWaitMilliseconds
			}
			policy.wait = time.Duration(milliseconds) * time.Millisecond
		}
	}
	return policy
}

// sleepBetweenAttempts waits, and reports false when the execution was
// cancelled instead — a retry loop must not outlive the run it belongs to.
func sleepBetweenAttempts(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// ErrorItemKey is the field a tolerated failure writes on each item it emits.
//
// It is n8n's own name for it. Imported checks read `{{ $json.error }}`, so a
// descriptor under any other key matches nothing and the branch silently takes
// the wrong arm.
const ErrorItemKey = "error"

// mainPortName is the item channel every executor reads its work from. The
// document spells it, the compiler resolves ports against it, and the runner
// has to know which of a node's ports carries items when it hands an executor
// one item at a time.
const mainPortName = "main"

// itemInputPorts counts the item channels a node declares.
func itemInputPorts(node workflow.IRNode) int {
	ports := 0
	for _, port := range node.Definition.Inputs {
		if port.Kind == workflow.ConnectionMain {
			ports++
		}
	}
	return ports
}

// perItemTolerance reports whether this node's tolerated failures are resolved
// one item at a time.
//
// n8n resolves continueOnFail inside the node's item loop, so one failing item
// must not discard the items that already succeeded or skip the ones after it.
// Resolving it per item is only sound where an item is the unit of work: a
// single item input, no batching setting, and not a loop entry, whose state
// machine dispatches batches rather than items.
func perItemTolerance(node workflow.IRNode, policy retry) bool {
	if policy.onError == errorStop {
		return false
	}
	if node.Definition.LoopEntry || settingBool(node.Settings, "executeOnce") {
		return false
	}
	return itemInputPorts(node) == 1
}

// stampProvenance fills in the lineage an executor did not set.
//
// Inference by position is a convenience, never the contract. It applies only
// when a node has exactly one incoming item port and produced exactly as many
// items as it consumed, which is the shape every one-to-one node has — Set,
// HTTP, a database query, a model call. A node that reorders, filters or
// aggregates must set its own provenance, because guessing there would produce
// a confident answer that happens to be wrong, which is the worst failure mode
// an imported workflow can have.
func stampProvenance(node workflow.IRNode, edges []workflow.IREdge, input workflow.NodeInput, output workflow.NodeOutput, runIndex int) {
	// Only the ports this invocation actually read count. A node fed by two
	// branches runs once per branch, each time carrying one branch's items, so
	// counting the edges instead would mark every item of both runs as lost.
	var sources []workflow.IREdge
	for _, edge := range edges {
		if edge.Kind != workflow.ConnectionMain {
			continue
		}
		if items, delivered := input[edge.Target.Port]; !delivered || len(items) == 0 {
			continue
		}
		sources = append(sources, edge)
	}

	for portIndex := range output {
		for itemIndex := range output[portIndex] {
			if output[portIndex][itemIndex].Paired != nil {
				continue // The executor knew better.
			}
			// A trigger has no input to descend from, so its items originate
			// here rather than having lost anything.
			if len(sources) == 0 {
				output[portIndex][itemIndex].Paired = &workflow.PairedItem{
					SourceNodeID: node.ID, RunIndex: runIndex, ItemIndex: itemIndex,
				}
				continue
			}
			source := sources[0]
			incomingItems := input[source.Target.Port]
			if len(sources) != 1 || len(incomingItems) != len(output[portIndex]) {
				// Several inputs, or a changed item count: the correspondence
				// is genuinely unknown and saying so is the honest answer.
				output[portIndex][itemIndex].Paired = &workflow.PairedItem{
					SourceNodeID: node.ID, RunIndex: runIndex, ItemIndex: itemIndex, Lost: true,
				}
				continue
			}
			// One in, one out, same count: the item at position N descends from
			// the item at position N, and it inherits the origin that item
			// already carried rather than pointing at this node.
			if origin := incomingItems[itemIndex].Paired; origin != nil && !origin.Lost {
				inherited := *origin
				output[portIndex][itemIndex].Paired = &inherited
				continue
			}
			output[portIndex][itemIndex].Paired = &workflow.PairedItem{
				SourceNodeID: source.Source.NodeID, SourcePort: source.Source.Port,
				RunIndex: runIndex, ItemIndex: itemIndex,
			}
		}
	}
}

// nodeItemFor exposes one completed node to expressions.
//
// It publishes the node's whole run — items plus, per item, the canonical name
// of the origin that item descends from — and deliberately does not pick `.item`
// here. Which item is "the" paired one depends on the item being processed,
// which this function has never seen; PairNodeItems makes that choice per
// evaluation.
func nodeItemFor(node workflow.IRNode, output workflow.NodeOutput) expression.NodeItem {
	item := expression.NodeItem{
		// The node's own configuration, which `$('X').params` reads.
		Parameters: cloneMap(node.Parameters),
	}
	for _, port := range output {
		for _, entry := range port {
			item.Items = append(item.Items, entry.JSON)
			item.ItemOrigins = append(item.ItemOrigins, originKeyOf(entry.Paired))
		}
	}
	if len(item.Items) > 0 {
		item.JSON = item.Items[0]
		// A single item is unambiguous on its own, and every caller that
		// evaluates an expression without a per-item context — a webhook
		// response, an agent's tool parameters — still reads `.item` from here.
		// PairNodeItems overrides this the moment a current item is known.
		if len(item.Items) == 1 {
			item.Paired = item.Items[0]
			return item
		}
		item.LineageReason = fmt.Sprintf("node %q produced %d items; use .all(), .first() or .last() to choose one", node.Name, len(item.Items))
		return item
	}
	item.LineageReason = fmt.Sprintf("node %q produced no items", node.Name)
	return item
}

// PairNodeItems resolves `.item` for the item being evaluated.
//
// n8n's `$('X').item` is the item of X that the current item descends from,
// found by following paired-item lineage. Every item the runner produces is
// stamped with the origin it descends from, so the correspondence is an
// equality of two origin names: the current item's, and each of X's items'.
//
// Four answers, in order, because they are increasingly weaker:
//
//  1. A unique item of X descends from the same origin as this one. That is the
//     answer, and it covers a one-to-one chain where each item carries its own
//     origin (A -> Set -> B over three items).
//  2. A node that produced exactly one item is unambiguous whatever the
//     lineage says: every single-item reference has always resolved to it.
//  3. The origin is shared by several of X's items — a fan-out, as after Split
//     Out — and the current item sits at position N of its own run, so X's item
//     at position N is the one it descends from. This is the positional
//     correspondence n8n relies on for a same-order chain.
//  4. Otherwise the correspondence is genuinely unknown and saying so beats
//     guessing: a confident wrong item is the failure mode this exists to
//     prevent.
//
// The map is rebuilt rather than mutated because `Request.NodeItems` is the
// runner's live view of the run and must not carry one item's pairing into the
// next item's evaluation.
func PairNodeItems(items map[string]expression.NodeItem, current workflow.Item, itemIndex int) map[string]expression.NodeItem {
	if len(items) == 0 {
		return items
	}
	key := originKeyOf(current.Paired)
	paired := make(map[string]expression.NodeItem, len(items))
	for name, item := range items {
		paired[name] = pairNodeItem(name, item, key, current.Paired, itemIndex)
	}
	return paired
}

func pairNodeItem(name string, item expression.NodeItem, key string, origin *workflow.PairedItem, itemIndex int) expression.NodeItem {
	item.Paired, item.LineageReason = nil, ""
	if len(item.Items) == 0 {
		item.LineageReason = fmt.Sprintf("node %q produced no items", name)
		return item
	}
	if len(item.Items) == 1 {
		item.Paired = item.Items[0]
		return item
	}
	if key == "" {
		// The item being processed records no origin of its own: an executor
		// built it from scratch, or its correspondence was already lost.
		if origin != nil && origin.Lost {
			item.LineageReason = "the item being processed lost its lineage upstream, so there is no single item to pair with"
			return item
		}
		item.LineageReason = "the item being processed did not record where it came from"
		return item
	}

	match, matches := -1, 0
	unknown := 0
	for index, candidate := range item.ItemOrigins {
		if candidate == "" {
			unknown++
			continue
		}
		if candidate != key {
			continue
		}
		matches++
		if match < 0 {
			match = index
		}
	}
	switch {
	case matches == 1:
		item.Paired = item.Items[match]
		return item
	case matches > 1, matches == 0 && unknown == 0:
		// A fan-out gave several of X's items the same origin, or the positions
		// of the run are the only correspondence left. Position is the answer
		// n8n uses for a same-order chain; outside the run it is not an answer
		// at all.
		if itemIndex >= 0 && itemIndex < len(item.Items) {
			item.Paired = item.Items[itemIndex]
			return item
		}
		if matches > 1 {
			item.LineageReason = fmt.Sprintf("node %q produced several items paired with this one; use .all(), .first() or .last() to choose one", name)
			return item
		}
		item.LineageReason = fmt.Sprintf("no item of node %q descends from the item being processed; use .all(), .first() or .last()", name)
		return item
	default:
		item.LineageReason = fmt.Sprintf("node %q changed the item correspondence, so there is no single item to pair with", name)
		return item
	}
}

// originKeyOf names the origin an item descends from, in the exact form the
// expression package compares. An item with no usable origin — one an executor
// built from scratch, or one whose correspondence was lost — has no name, and
// never matches another item.
func originKeyOf(origin *workflow.PairedItem) string {
	if origin == nil || origin.Lost {
		return ""
	}
	return expression.OriginKey(origin.SourceNodeID, origin.SourcePort, origin.RunIndex, origin.ItemIndex)
}

// workflowContextFor exposes the compiled workflow to `$workflow`.
//
// The caller may have supplied identity the engine cannot know — whether the
// workflow is active, for one — so anything already set is left alone and only
// the fields the compiled graph carries are filled in.
func workflowContextFor(ir workflow.IR, provided expression.WorkflowContext) expression.WorkflowContext {
	if provided.ID == "" {
		provided.ID = ir.WorkflowID
	}
	if provided.Name == "" {
		provided.Name = ir.Name
	}
	if provided.Timezone == "" {
		if timezone, ok := ir.Settings["timezone"].(string); ok {
			provided.Timezone = timezone
		}
	}
	return provided
}
