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
	// Empty means every root, which is what a manual run of a workflow means
	// and what preserves today's behaviour for a single-root graph.
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

// preparedGraph is the scheduling shape of one compiled run: the live nodes,
// their edges partitioned both ways, and the loop back edges ordinary
// scheduling must ignore.
type preparedGraph struct {
	nodes    map[string]workflow.IRNode
	incoming map[string][]workflow.IREdge
	outgoing map[string]int
	loops    map[string]*loopGraph
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

	// A loop's back edge is the one thing in the graph that points backwards,
	// and it has to be excluded from ordinary scheduling: a loop node whose
	// body has not run yet would otherwise be waiting on its own output.
	loops := findLoops(ir, nodes)
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
	return preparedGraph{nodes: nodes, incoming: incoming, outgoing: outgoing, loops: loops}, nil
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
	nodes, incoming, outgoing, loops := graph.nodes, graph.incoming, graph.outgoing, graph.loops

	// Outputs are kept per run index rather than one per node: a node inside a
	// loop or a fan-out produces several distinct runs, and an expression that
	// reaches back to it has to be able to name which one. The last run is what
	// scheduling reads, so a single-run graph behaves exactly as before.
	runs := make(map[string][]workflow.NodeOutput, len(nodes))
	completed := make(map[string]workflow.NodeOutput, len(nodes))
	// `$node["Name"]` reads the first item a completed node produced. Building
	// it here keeps the lookup ordered by actual execution, so a node can never
	// observe one that has not run yet.
	// `$workflow` reads the compiled graph's identity: the caller supplies what
	// only it knows (whether the workflow is active), and the runner fills in
	// the rest rather than leaving `$workflow.name` empty in every production
	// run.
	request.Workflow = workflowContextFor(ir, request.Workflow)
	if request.NodeOutputs == nil {
		request.NodeOutputs = make(map[string]map[string]any, len(nodes))
	}
	if request.NodeItems == nil {
		request.NodeItems = make(map[string]expression.NodeItem, len(nodes))
	}
	if request.NodeState == nil {
		request.NodeState = make(map[string]map[string]any, len(nodes))
	}
	// Every node gets its own entry up front, because the executor mutates it in
	// place: a node that replaces its entry would write to a copy the runner
	// never sees.
	for nodeID := range nodes {
		if _, exists := request.NodeState[nodeID]; !exists {
			request.NodeState[nodeID] = map[string]any{}
		}
	}
	result := Result{NodeRuns: make([]NodeRun, 0, len(nodes)), Output: make(map[string]workflow.NodeOutput)}
	suspended, err := runner.runLoop(ctx, nodes, incoming, outgoing, loops, &request, completed, runs, &result)
	if err != nil {
		return result, err
	}
	if suspended != nil {
		return result, suspended
	}
	return result, nil
}

// runLoop schedules every node the graph still owes. It is the single pass
// Run and Resume share: Run starts it empty, Resume starts it from a
// checkpoint. A suspension returns the signal with the checkpoint attached,
// alongside the runs produced so far; any other error fails the run.
func (runner *Runner) runLoop(ctx context.Context, nodes map[string]workflow.IRNode, incoming map[string][]workflow.IREdge, outgoing map[string]int, loops map[string]*loopGraph, request *Request, completed map[string]workflow.NodeOutput, runs map[string][]workflow.NodeOutput, result *Result) (*SuspendError, error) {
	for len(completed) < len(nodes) {
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
		ready := make([]string, 0, len(nodes)-len(completed))
		for nodeID := range nodes {
			if _, done := completed[nodeID]; done || !dependenciesComplete(schedulingEdges(incoming[nodeID], loops), completed) {
				continue
			}
			if awaitingLoop(incoming[nodeID], loops) {
				continue
			}
			ready = append(ready, nodeID)
		}
		if len(ready) == 0 {
			return nil, fmt.Errorf("compiled workflow graph has no schedulable node")
		}
		sort.Strings(ready)
		nodeID := ready[0]
		node := nodes[nodeID]
		input, err := nodeInput(iterationEdges(nodeID, incoming[nodeID], loops), completed)
		if err != nil {
			return nil, fmt.Errorf("build node %q input: %w", nodeID, err)
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
			runs[nodeID] = append(runs[nodeID], cloneOutput(skipped))
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
			return nil, fmt.Errorf("node %q executor %q is not registered", nodeID, node.Definition.ExecutorID)
		}

		// The attempt loop. The per-node context is rebuilt each time round,
		// because its cancel must fire per attempt rather than once for all of
		// them — a shared deadline would make the second attempt inherit the
		// first one's remaining time.
		policy := retryPolicy(node.Settings)
		var (
			output      workflow.NodeOutput
			lastErr     error
			lastCode    string
			usedAttempt int
		)
		for attempt := 1; attempt <= policy.attempts; attempt++ {
			usedAttempt = attempt
			nodeCtx, cancel, timeout, err := nodeContext(ctx, node)
			if err != nil {
				result.NodeRuns = append(result.NodeRuns, NodeRun{
					NodeID: nodeID, Input: cloneInput(input), Error: err, Attempt: attempt, ErrorCode: "config.invalid",
				})
				return nil, err
			}
			output, err = executor.Execute(nodeCtx, node, cloneInput(input), cloneRequest(*request))
			cancel()
			var suspended *SuspendError
			if err != nil && errors.As(err, &suspended) {
				// Suspension is neither success nor failure: the run stops
				// here with no retry and no failure row, and the service
				// persists the continuation durably.
				raw, cerr := marshalCheckpoint(snapshotCheckpoint(nodeID, suspended.Mode, input, attempt, completed, runs, request, result))
				if cerr != nil {
					return nil, cerr
				}
				suspended.NodeID = nodeID
				suspended.Checkpoint = raw
				return suspended, nil
			}
			if err == nil {
				// Earlier attempts were already recorded as they failed; this
				// one is recorded below with its successful output.
				lastErr, lastCode = nil, ""
				break
			}
			lastErr = err
			lastCode = "node.failed"
			if timeout > 0 && errors.Is(nodeCtx.Err(), context.DeadlineExceeded) {
				lastCode = "node.timeout"
			} else if timeout == 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				lastCode = "execution.timeout"
			}
			// A failed attempt is a row of its own, so a reader can see that a
			// node succeeded on its third try rather than only that it
			// succeeded.
			if attempt < policy.attempts {
				result.NodeRuns = append(result.NodeRuns, NodeRun{
					NodeID: nodeID, Input: cloneInput(input), Error: err, Attempt: attempt, ErrorCode: lastCode,
				})
				if !sleepBetweenAttempts(ctx, policy.wait) {
					// The execution was cancelled while waiting; stop here
					// rather than burning the remaining attempts.
					result.NodeRuns = append(result.NodeRuns, NodeRun{
						NodeID: nodeID, Input: cloneInput(input), Error: ctx.Err(), Attempt: attempt + 1, ErrorCode: "execution.cancelled",
					})
					return nil, fmt.Errorf("execute node %q: %w", nodeID, ctx.Err())
				}
				continue
			}
		}

		if lastErr != nil {
			if !policy.continueOnFail {
				result.NodeRuns = append(result.NodeRuns, NodeRun{
					NodeID: nodeID, Input: cloneInput(input), Error: lastErr, Attempt: usedAttempt, ErrorCode: lastCode,
				})
				return nil, fmt.Errorf("execute node %q: %w", nodeID, lastErr)
			}
			// Tolerated: the node emits error items rather than aborting, and
			// the run carries on. The items are *not* the input passed through
			// — a downstream node has to be able to tell a tolerated failure
			// from a success.
			output = errorOutput(node, input, lastErr)
			result.NodeRuns = append(result.NodeRuns, NodeRun{
				NodeID: nodeID, Input: cloneInput(input), Output: cloneOutput(output),
				Error: lastErr, Attempt: usedAttempt, ErrorCode: lastCode,
			})
			completed[nodeID] = cloneOutput(output)
			runs[nodeID] = append(runs[nodeID], cloneOutput(output))
			if first, ok := firstItem(output); ok {
				request.NodeOutputs[node.Name] = first
			}
			if outgoing[nodeID] == 0 {
				result.Output[nodeID] = cloneOutput(output)
			}
			continue
		}

		if got, want := len(output), len(node.Definition.Outputs); got != want {
			return nil, fmt.Errorf("node %q returned %d output streams, want %d", nodeID, got, want)
		}
		output = cloneOutput(output)
		// Provenance the executor did not set is inferred by position, but only
		// when that inference is actually sound: exactly one incoming item port
		// and a matching item count. An executor that reorders or filters must
		// set its own, which is why IF and Merge do.
		stampProvenance(node, incoming[nodeID], input, output, len(runs[nodeID]))
		completed[nodeID] = output
		runs[nodeID] = append(runs[nodeID], cloneOutput(output))
		if first, ok := firstItem(output); ok {
			request.NodeOutputs[node.Name] = first
		}
		request.NodeItems[node.Name] = nodeItemFor(node, output)
		result.NodeRuns = append(result.NodeRuns, NodeRun{
			NodeID: nodeID, Input: cloneInput(input), Output: output,
			Attempt: usedAttempt, RunIndex: len(runs[nodeID]) - 1,
		})
		// A loop that still has work reopens its body so the next batch can run,
		// and reopens its entry when that body comes back round. Every
		// iteration keeps its own trace rows, because each run advanced the
		// node's run index above.
		reopenLoops(nodeID, node, output, loops, completed)
		closeIteration(nodeID, incoming, loops, completed)
		if outgoing[nodeID] == 0 {
			result.Output[nodeID] = cloneOutput(output)
		}
	}
	return nil, nil
}

// snapshotCheckpoint copies the live run state at a suspension. The copies
// are cheap insurance: the in-memory run ends here, but aliasing its maps
// into durable storage would corrupt the checkpoint the moment any future
// change touches them before the marshal.
func snapshotCheckpoint(nodeID, mode string, input workflow.NodeInput, attempt int, completed map[string]workflow.NodeOutput, runs map[string][]workflow.NodeOutput, request *Request, result *Result) Checkpoint {
	checkpoint := Checkpoint{
		SuspendNode: nodeID, SuspendAttempt: attempt, Mode: mode,
		TriggerNodeID: request.TriggerNodeID,
		Input:         cloneInput(input),
		Completed:     make(map[string]workflow.NodeOutput, len(completed)),
		Runs:          make(map[string][]workflow.NodeOutput, len(runs)),
		NodeOutputs:   make(map[string]map[string]any, len(request.NodeOutputs)),
		NodeItems:     make(map[string]expression.NodeItem, len(request.NodeItems)),
		NodeState:     cloneNodeState(request.NodeState),
		Output:        make(map[string]workflow.NodeOutput, len(result.Output)),
	}
	for id, output := range completed {
		checkpoint.Completed[id] = cloneOutput(output)
	}
	for id, outputs := range runs {
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
	for id, output := range result.Output {
		checkpoint.Output[id] = cloneOutput(output)
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
	if got, want := len(resumeOutput), len(node.Definition.Outputs); got != want {
		return Result{}, fmt.Errorf("resume output for node %q has %d streams, want %d", checkpoint.SuspendNode, got, want)
	}
	request.Workflow = workflowContextFor(ir, request.Workflow)
	if request.NodeOutputs == nil {
		request.NodeOutputs = make(map[string]map[string]any, len(checkpoint.NodeOutputs))
	}
	if request.NodeItems == nil {
		request.NodeItems = make(map[string]expression.NodeItem, len(checkpoint.NodeItems))
	}
	for name, fields := range checkpoint.NodeOutputs {
		request.NodeOutputs[name] = fields
	}
	for name, item := range checkpoint.NodeItems {
		request.NodeItems[name] = item
	}
	// Loop state comes back with the run: a loop that suspended inside its body
	// must resume on the batch it was on, not start again from the first one.
	if request.NodeState == nil {
		request.NodeState = make(map[string]map[string]any, len(checkpoint.NodeState))
	}
	for id, state := range checkpoint.NodeState {
		request.NodeState[id] = cloneStateFields(state)
	}
	for nodeID := range graph.nodes {
		if _, exists := request.NodeState[nodeID]; !exists {
			request.NodeState[nodeID] = map[string]any{}
		}
	}
	completed := checkpoint.Completed
	runs := checkpoint.Runs
	result := Result{
		NodeRuns: make([]NodeRun, 0, len(graph.nodes)),
		Output:   make(map[string]workflow.NodeOutput, len(checkpoint.Output)),
	}
	for id, output := range checkpoint.Output {
		result.Output[id] = output
	}
	// The suspending node completes here, mirroring a normal completion:
	// provenance, expression state, the trace row, and loop bookkeeping all
	// behave as if the node had just run.
	nodeID := checkpoint.SuspendNode
	output := cloneOutput(resumeOutput)
	stampProvenance(node, graph.incoming[nodeID], checkpoint.Input, output, len(runs[nodeID]))
	completed[nodeID] = output
	runs[nodeID] = append(runs[nodeID], cloneOutput(output))
	if first, ok := firstItem(output); ok {
		request.NodeOutputs[node.Name] = first
	}
	request.NodeItems[node.Name] = nodeItemFor(node, output)
	attempt := checkpoint.SuspendAttempt
	if attempt < 1 {
		attempt = 1
	}
	result.NodeRuns = append(result.NodeRuns, NodeRun{
		NodeID: nodeID, Input: cloneInput(checkpoint.Input), Output: output,
		Attempt: attempt, RunIndex: len(runs[nodeID]) - 1,
	})
	reopenLoops(nodeID, node, output, graph.loops, completed)
	closeIteration(nodeID, graph.incoming, graph.loops, completed)
	if graph.outgoing[nodeID] == 0 {
		result.Output[nodeID] = cloneOutput(output)
	}
	suspended, err := runner.runLoop(ctx, graph.nodes, graph.incoming, graph.outgoing, graph.loops, &request, completed, runs, &result)
	if err != nil {
		return result, err
	}
	if suspended != nil {
		return result, suspended
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

// retry is how a node handles its own failure.
type retry struct {
	// attempts is the total number of tries, so 1 means no retry at all.
	attempts int
	// wait is the delay between attempts.
	wait time.Duration
	// continueOnFail tolerates a final failure instead of aborting the run.
	continueOnFail bool
}

// defaultRetryWait is used when retryOnFail is on and no delay was given.
//
// It is non-zero deliberately: without it, ticking Retry on Fail against a
// rate-limited API sends every attempt inside a millisecond, turning one
// failing request into a burst against an upstream that is already struggling.
const defaultRetryWait = time.Second

// retryPolicy reads the three settings every node declares.
//
// The compiler has already refused an out-of-range value, so this clamps rather
// than errors: a document that reached the runner has valid settings, and a
// second error path here would only be reachable if that stopped being true.
func retryPolicy(settings map[string]any) retry {
	policy := retry{attempts: 1, wait: 0, continueOnFail: settingBool(settings, "continueOnFail")}
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
const ErrorItemKey = "$error"

// errorOutput is what a node emits when its failure was tolerated.
//
// One error item per input item, so downstream item counts and paired-item
// lineage survive a tolerated failure; a single item when the node had no input
// to pair against. The input is deliberately not passed through unchanged —
// a downstream node has to be able to tell a tolerated failure from a success,
// and identical items would make that impossible.
func errorOutput(node workflow.IRNode, input workflow.NodeInput, cause error) workflow.NodeOutput {
	descriptor := map[string]any{
		"message": cause.Error(),
		"node":    node.Name,
	}
	var items []workflow.Item
	for _, port := range input {
		for range port {
			items = append(items, workflow.Item{JSON: map[string]any{ErrorItemKey: cloneMap(descriptor)}})
		}
	}
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{ErrorItemKey: cloneMap(descriptor)}}}
	}

	output := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range output {
		output[index] = []workflow.Item{}
	}
	if len(output) > 0 {
		output[0] = items
	}
	return output
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
	var itemPorts int
	var source workflow.IREdge
	for _, edge := range edges {
		if edge.Kind != workflow.ConnectionMain {
			continue
		}
		itemPorts++
		source = edge
	}

	for portIndex := range output {
		for itemIndex := range output[portIndex] {
			if output[portIndex][itemIndex].Paired != nil {
				continue // The executor knew better.
			}
			// A trigger has no input to descend from, so its items originate
			// here rather than having lost anything.
			if itemPorts == 0 {
				output[portIndex][itemIndex].Paired = &workflow.PairedItem{
					SourceNodeID: node.ID, RunIndex: runIndex, ItemIndex: itemIndex,
				}
				continue
			}
			incomingItems := input[source.Target.Port]
			if itemPorts != 1 || len(incomingItems) != len(output[portIndex]) {
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

// loopGraph is what the runner needs to know about one bounded loop.
type loopGraph struct {
	// entryID is the loop node itself.
	entryID string
	// backEdges are the edges pointing from the body back into the entry.
	backEdges map[string]bool
	// body is every node between the entry's `loop` output and the back edge.
	body map[string]bool
	// started marks a loop that has dispatched at least one batch, so its
	// entry now reads from the back edge rather than from upstream.
	started bool
	// finished marks a loop that has emitted on `done`. Until it has, the
	// nodes below `done` must not be scheduled: they would be handed an empty
	// stream mid-loop, be pruned as an untaken branch, and never run again
	// once the final batch actually produced something.
	finished bool
}

// findLoops locates every bounded loop in a compiled graph.
//
// A loop is a node whose definition declares LoopEntry together with the edges
// that close back onto it and the nodes between. The compiler has already
// refused any other cycle, so anything found here is a loop somebody declared.
func findLoops(ir workflow.IR, active map[string]workflow.IRNode) map[string]*loopGraph {
	loops := map[string]*loopGraph{}
	for _, node := range ir.Nodes {
		if !node.Definition.LoopEntry {
			continue
		}
		if _, live := active[node.ID]; !live {
			continue
		}
		loops[node.ID] = &loopGraph{
			entryID: node.ID, backEdges: map[string]bool{}, body: map[string]bool{},
		}
	}
	if len(loops) == 0 {
		return loops
	}

	// The body of a loop is whatever its `loop` output reaches before coming
	// back. Walking forward from the entry and stopping at the entry is enough:
	// the compiler guarantees no other cycle exists to wander into.
	forward := map[string][]workflow.IREdge{}
	for _, edge := range ir.Edges {
		forward[edge.Source.NodeID] = append(forward[edge.Source.NodeID], edge)
	}
	for entryID, loop := range loops {
		queue := []string{entryID}
		seen := map[string]bool{entryID: true}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, edge := range forward[current] {
				if edge.Kind != workflow.ConnectionMain {
					continue
				}
				if edge.Target.NodeID == entryID {
					loop.backEdges[edge.ID] = true
					continue
				}
				if seen[edge.Target.NodeID] {
					continue
				}
				seen[edge.Target.NodeID] = true
				loop.body[edge.Target.NodeID] = true
				queue = append(queue, edge.Target.NodeID)
			}
		}
	}
	return loops
}

// schedulingEdges hides a loop's back edge until the loop has actually started.
//
// Without this a loop node waits forever on its own body, which has not run
// because the loop node has not dispatched a batch.
func schedulingEdges(edges []workflow.IREdge, loops map[string]*loopGraph) []workflow.IREdge {
	if len(loops) == 0 {
		return edges
	}
	filtered := make([]workflow.IREdge, 0, len(edges))
	for _, edge := range edges {
		if loop, found := loops[edge.Target.NodeID]; found && loop.backEdges[edge.ID] && !loop.started {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered
}

// iterationEdges decides what a loop node reads on re-entry.
//
// On the first dispatch it takes its work from upstream. On every later one it
// reads only the back edge, because the upstream items are still sitting in the
// completed map and feeding them in again would restart the loop with every
// iteration.
func iterationEdges(nodeID string, edges []workflow.IREdge, loops map[string]*loopGraph) []workflow.IREdge {
	loop, found := loops[nodeID]
	if !found {
		return edges
	}
	filtered := make([]workflow.IREdge, 0, len(edges))
	for _, edge := range edges {
		// Exactly one side of the loop feeds any given dispatch: upstream on
		// the first, the body on every one after. Taking both would restart the
		// loop on every iteration, and taking the back edge before the body has
		// run would read an output that does not exist.
		if loop.backEdges[edge.ID] == loop.started {
			filtered = append(filtered, edge)
		}
	}
	return filtered
}

// reopenLoops clears the completion state of a loop's body once a batch has
// been dispatched, so the next iteration can run.
//
// It is driven by the loop node's own output rather than by a counter the
// runner keeps: the node says whether it has more work by emitting on `loop`,
// which keeps the iteration state where it belongs and out of the scheduler.
func reopenLoops(nodeID string, node workflow.IRNode, output workflow.NodeOutput, loops map[string]*loopGraph, completed map[string]workflow.NodeOutput) {
	loop, found := loops[nodeID]
	if !found {
		return
	}
	dispatched := false
	for index, port := range node.Definition.Outputs {
		if port.Name == "loop" && index < len(output) && len(output[index]) > 0 {
			dispatched = true
		}
	}
	if !dispatched {
		// The loop is finished. Its body keeps whatever state it ended with,
		// and the nodes below `done` become schedulable and run once.
		loop.finished = true
		return
	}
	loop.started = true
	// Only the body is reopened, and only the body. Reopening the entry here
	// too would deadlock: the entry would then be waiting on a back edge whose
	// source has just been reopened and cannot run until the entry does.
	//
	// The entry is reopened instead when the back edge's source completes,
	// which is the moment the next batch actually has somewhere to come from.
	for bodyID := range loop.body {
		delete(completed, bodyID)
	}
}

// closeIteration reopens a loop's entry once its body has come back round.
//
// This is the other half of reopenLoops, and the order matters: the body is
// reopened when a batch is dispatched, and the entry when the body returns.
// Doing both at once leaves neither able to run.
func closeIteration(nodeID string, edges map[string][]workflow.IREdge, loops map[string]*loopGraph, completed map[string]workflow.NodeOutput) {
	for _, loop := range loops {
		if !loop.started {
			continue
		}
		for _, edge := range edges[loop.entryID] {
			if loop.backEdges[edge.ID] && edge.Source.NodeID == nodeID {
				delete(completed, loop.entryID)
			}
		}
	}
}

// awaitingLoop reports a node fed by a loop's `done` port while that loop is
// still iterating.
//
// Such a node must not be scheduled yet. Its input is empty until the final
// batch, so scheduling it mid-loop would prune it as an untaken branch — and
// once pruned it is complete, so it would never run when `done` finally carried
// the accumulated items.
func awaitingLoop(edges []workflow.IREdge, loops map[string]*loopGraph) bool {
	for _, edge := range edges {
		loop, found := loops[edge.Source.NodeID]
		if !found || !loop.started || loop.finished {
			continue
		}
		if edge.Source.Port == "done" {
			return true
		}
	}
	return false
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
