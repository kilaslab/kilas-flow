package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file is the read-only evaluator behind `POST /executions/{id}/eval`.
//
// It exists for one question the rest of the surface cannot answer: what did
// this field hold at this node, in the run that already happened. The
// alternative — re-running with a patched node — answers a different question
// *and* changes the workflow, spending a run's side effects to look at the
// past. Everything the evaluator can read is already readable by the same
// caller through `GET /executions/{id}` and the node-run trace: the stored
// node runs, their inputs and outputs, are the whole of its input.
//
// Two bounds are borrowed from the runtime rather than invented here. The
// grammar is `expression.Evaluate`'s — the evaluator every tenant-authored
// document is already resolved with, so a debugger cannot drift from the
// engine. The budget is the one a node of this revision would be given: the
// workflow's run budget, refined by the node's own timeoutSeconds.

// ErrExpressionDeadline reports an evaluation that outlived the deadline a node
// of this execution is given.
var ErrExpressionDeadline = errors.New("expression evaluation exceeded the deadline a node is given")

// ErrExpressionNodeUnknown reports a node named for evaluation that the
// execution's trace has no row for.
var ErrExpressionNodeUnknown = errors.New("the execution's trace has no such node")

// ExpressionEvaluation is one expression's answer against a stored trace.
type ExpressionEvaluation struct {
	// Value is the result as the JSON a caller receives. Encoding it here is
	// what turns an evaluator value JSON cannot carry into an error the caller
	// can act on, rather than into a marshal failure inside the response
	// writer.
	Value json.RawMessage
	// Type names the JSON shape Value carries: string, number, boolean, array,
	// object or null. It is read off the encoded bytes rather than off the Go
	// value, so what the field says and what the caller received cannot
	// disagree.
	Type string
}

// ExpressionTarget is where in a stored trace an expression is evaluated.
type ExpressionTarget struct {
	// NodeID names the node whose parameters the expression stands in for.
	// Empty evaluates against the trace as a whole.
	NodeID string
	// Run picks one of the node's runs, numbered as `$runIndex` numbers them:
	// from zero, real runs only, so a delivery the runner pruned is not one.
	// Nil is the last run that received items.
	Run *int
	// Item picks one item of that run's input, `$itemIndex`. Nil is the first.
	Item *int
}

// EvaluateExpression evaluates one expression against a finished execution's
// stored trace.
//
// A target with no node evaluates against the trace as a whole: `$json` is
// null and an expression reads the run through `$node`/`$('Name')`, which is
// what a caller with no particular node in mind wants.
//
// A named node is evaluated the way its parameters were: `$json` is an item
// the node received, not one it produced, because "what did this parameter
// see" is the question a debugger is asked. Every root answers from the one
// run and item picked — `$input`, `$runIndex`, `$itemIndex`, and each earlier
// node as it stood when that run started, paired with that item for
// `$('X').item`. A named node must have a trace row — a name nothing ran under
// is refused rather than answered with an empty item, which would look exactly
// like a node that received nothing.
func (service *Service) EvaluateExpression(ctx context.Context, record execution.Record, document workflow.Document, template string, target ExpressionTarget) (ExpressionEvaluation, error) {
	template, target.NodeID = strings.TrimSpace(template), strings.TrimSpace(target.NodeID)
	if template == "" {
		return ExpressionEvaluation{}, errors.New("expression is required")
	}
	if target.NodeID == "" && (target.Run != nil || target.Item != nil) {
		return ExpressionEvaluation{}, errors.New("a run and an item are picked within a node: name a node as well")
	}
	if !strings.Contains(template, "{{") {
		// The grammar is the document grammar. A caller who typed the
		// expression body alone meant that expression, and answering with the
		// literal text would be a debugger that silently echoes its input.
		template = "{{ " + template + " }}"
	}

	roots, err := service.traceExpressionContext(record, document, target)
	if err != nil {
		return ExpressionEvaluation{}, err
	}

	deadline, cancel, err := service.evaluationDeadline(ctx, document, target.NodeID)
	if err != nil {
		return ExpressionEvaluation{}, err
	}
	defer cancel()

	type outcome struct {
		value any
		err   error
	}
	// The evaluator takes no context — it is called from the runner with the
	// run's own deadline already in hand, and it is bounded by the data it
	// walks — so the deadline releases the *request* rather than the walk: the
	// caller is answered at the budget instead of being held open by a
	// pathological expression. The goroutine finishes on its own and is
	// discarded.
	answered := make(chan outcome, 1)
	go func() {
		value, err := expression.Evaluate(template, roots)
		answered <- outcome{value: value, err: err}
	}()

	select {
	case <-deadline.Done():
		if errors.Is(deadline.Err(), context.DeadlineExceeded) {
			return ExpressionEvaluation{}, ErrExpressionDeadline
		}
		// The request itself went away — a disconnected client — which is not
		// this operation's refusal to report.
		return ExpressionEvaluation{}, deadline.Err()

	case result := <-answered:
		if result.err != nil {
			return ExpressionEvaluation{}, result.err
		}
		encoded, err := json.Marshal(result.value)
		if err != nil {
			return ExpressionEvaluation{}, fmt.Errorf("the expression produced a value that cannot be returned: %w", err)
		}
		return ExpressionEvaluation{Value: encoded, Type: jsonKind(encoded)}, nil
	}
}

// evaluationDeadline is the budget a node of this revision would run under: the
// workflow's own run budget, refined by that node's declared timeoutSeconds.
//
// It is the runtime's own resolution, not a convenient number: a debugger that
// allowed itself longer than the node it is asking about would answer questions
// the run could not.
func (service *Service) evaluationDeadline(ctx context.Context, document workflow.Document, nodeID string) (context.Context, context.CancelFunc, error) {
	budget, cancelBudget := service.runBudget(ctx, document)
	if nodeID == "" {
		return budget, cancelBudget, nil
	}
	var settings map[string]any
	for _, node := range document.Nodes {
		if node.ID == nodeID {
			settings = node.Settings
			break
		}
	}
	nodeCtx, cancelNode, _, err := nodeContext(budget, workflow.IRNode{ID: nodeID, Settings: settings})
	if err != nil {
		cancelBudget()
		return nil, nil, fmt.Errorf("node %q of this revision has an invalid timeout: %w", nodeID, err)
	}
	return nodeCtx, func() { cancelNode(); cancelBudget() }, nil
}

// traceExpressionContext rebuilds the approved expression roots from an
// execution's stored trace.
//
// The node runs are the whole data source, redacted exactly as the trace view
// redacts them on the way out, so the evaluator reaches nothing the same caller
// could not already read with `GET /executions/{id}`.
//
// Nodes are keyed by the name the revision gives them, which is the key every
// workflow's own expressions use (`$('HTTP Request')`); a trace row the
// document does not name — an execution whose revision is no longer readable —
// falls back to the id the trace itself shows, rather than disappearing.
//
// With a node named, the trace is read only up to the run picked: the rows
// before it are what every other node had left when that run resolved its
// parameters, which is what `$('X')` read then. Reading the whole trace instead
// answered a loop's node with each upstream node's last iteration, and an
// item's lineage names the iteration it actually came from.
//
// A node nothing ran under is ErrExpressionNodeUnknown. That is separate from
// a node that ran and received nothing, which is an answer, while a name
// nothing ran under is a mistake the caller should be told about.
func (service *Service) traceExpressionContext(record execution.Record, document workflow.Document, target ExpressionTarget) (expression.Context, error) {
	names := make(map[string]string, len(document.Nodes))
	for _, node := range document.Nodes {
		names[node.ID] = node.Name
	}
	key := func(id string) string {
		if name := names[id]; name != "" {
			return name
		}
		return id
	}

	trace := record.NodeRuns
	var picked tracedRun
	var revision revisionGraph
	if target.NodeID != "" {
		var err error
		if picked, err = pickRun(record.NodeRuns, target.NodeID, target.Run); err != nil {
			return expression.Context{}, err
		}
		trace = record.NodeRuns[:picked.position]
		revision = service.revisionGraph(record, document)
	}

	nodes := make(map[string]map[string]any, len(trace))
	items := make(map[string]expression.NodeItem, len(trace))
	// In sequence order, so a node that ran more than once — a loop, a retry —
	// is exposed as it last left things, which is the same row the trace view
	// shows last.
	for _, run := range trace {
		output, ok := expressionOutput(run.Output)
		if !ok {
			continue
		}
		// nodeItemFor is the runtime's own exposure of a completed node: it is
		// what fills `Items`, the item origins `.item` is paired by and the
		// lineage refusal a multi-item node explains itself with. A second
		// builder here would be a second reader of the same semantics.
		entry := nodeItemFor(revision.node(run.NodeID, key(run.NodeID)), output, run.RunIndex)
		if len(entry.Items) == 0 {
			continue
		}
		name := key(run.NodeID)
		items[name] = entry
		nodes[name] = entry.JSON
	}

	roots := expression.Context{
		Nodes:     nodes,
		NodeItems: items,
		Workflow:  service.evaluationWorkflowContext(document),
		Env:       service.environment,
		Execution: expression.ExecutionContext{ID: record.ID, Mode: string(record.Trigger)},
	}
	if target.NodeID == "" {
		return roots, nil
	}

	input := expressionInput(record.NodeRuns[picked.position].Input)
	current, itemIndex, err := pickItem(input, key(target.NodeID), picked.run, target.Item)
	if err != nil {
		return expression.Context{}, err
	}
	roots.JSON = current.JSON
	roots.Input = expressionPorts(input)
	roots.ItemIndex, roots.RunIndex = itemIndex, picked.run
	// The runtime pairs `.item` per item in Request.ExpressionContext, from
	// the origin the item carries; the stored input carries the same one.
	roots.NodeItems = PairNodeItems(items, current, itemIndex)
	roots.NodeBranches = revision.branches(target.NodeID)
	return roots, nil
}

// tracedRun is one run of a node in a stored trace: the row that decided it,
// and its number as `$runIndex` counts.
type tracedRun struct {
	position int
	run      int
}

// pickRun finds the run of a node an evaluation stands in.
//
// A node's runs are its rows less two kinds that are not runs. A row the
// runner recorded for a delivery that carried nothing (status skipped) never
// resolved a parameter, and `$runIndex` does not count it; and an attempt a
// retry replaced is part of the run its last attempt decided. The trace row's
// own runIndex is not the number, because it counts those pruned deliveries
// too.
//
// With no run asked for, the last run that received items is picked, so a
// pruned row after a loop's iterations decides nothing; a node that never
// received an item is read at its last run, or its last row, as the empty
// item its parameters saw.
func pickRun(trace []execution.NodeRun, nodeID string, asked *int) (tracedRun, error) {
	var rows, runs []int
	for position, row := range trace {
		if row.NodeID != nodeID {
			continue
		}
		if len(rows) > 0 && len(runs) > 0 && runs[len(runs)-1] == rows[len(rows)-1] && attemptNumber(row) > attemptNumber(trace[rows[len(rows)-1]]) {
			// A retry of the run before it: the later attempt decides that run.
			runs = runs[:len(runs)-1]
		}
		rows = append(rows, position)
		if row.Status != execution.StatusSkipped {
			runs = append(runs, position)
		}
	}
	if len(rows) == 0 {
		return tracedRun{}, fmt.Errorf("%w: %s", ErrExpressionNodeUnknown, nodeID)
	}

	if asked != nil {
		if *asked < 0 || *asked >= len(runs) {
			return tracedRun{}, fmt.Errorf("node %s ran %s, so there is no run %d", nodeID, counted(len(runs), "time"), *asked)
		}
		return tracedRun{position: runs[*asked], run: *asked}, nil
	}
	for run := len(runs) - 1; run >= 0; run-- {
		if len(inputItems(expressionInput(trace[runs[run]].Input))) > 0 {
			return tracedRun{position: runs[run], run: run}, nil
		}
	}
	if len(runs) > 0 {
		return tracedRun{position: runs[len(runs)-1], run: len(runs) - 1}, nil
	}
	return tracedRun{position: rows[len(rows)-1]}, nil
}

// pickItem finds the input item an evaluation stands in. A run that received
// nothing is read as the empty item an executor resolves its parameters with
// then, at index 0.
func pickItem(input workflow.NodeInput, name string, run int, asked *int) (workflow.Item, int, error) {
	received := inputItems(input)
	index := 0
	if asked != nil {
		index = *asked
	}
	if len(received) == 0 && index == 0 {
		return workflow.Item{JSON: map[string]any{}}, 0, nil
	}
	if index < 0 || index >= len(received) {
		return workflow.Item{}, 0, fmt.Errorf("run %d of node %q received %s, so there is no item %d", run, name, counted(len(received), "item"), index)
	}
	item := received[index]
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return item, index, nil
}

// inputItems is the stream a node's parameters are resolved once per item of:
// its main input, which is what every executor iterates. A node whose inputs
// are named otherwise is read at the first of them, by name, that carried
// items.
func inputItems(input workflow.NodeInput) []workflow.Item {
	if items, found := input["main"]; found {
		return items
	}
	ports := make([]string, 0, len(input))
	for port := range input {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		if len(input[port]) > 0 {
			return input[port]
		}
	}
	return nil
}

// attemptNumber is a row's attempt, reading a row recorded before attempts
// were tracked as the first, as attemptOf does.
func attemptNumber(run execution.NodeRun) int {
	if run.Attempt < 1 {
		return 1
	}
	return run.Attempt
}

// counted is "1 item" or "3 items".
func counted(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// revisionGraph is the execution's revision as the runner compiled it, for the
// two things an evaluation at a node reads from it: each node's output port
// names, which an item's origin names its port by, and which output of each
// earlier node the node is connected to. A revision that no longer compiles (a
// node type since removed) has neither: its nodes' items are paired by origin
// alone, and are read at their first output.
type revisionGraph struct {
	graph    preparedGraph
	compiled bool
}

func (service *Service) revisionGraph(record execution.Record, document workflow.Document) revisionGraph {
	if service.catalog == nil {
		return revisionGraph{}
	}
	ir, err := service.compile(record, document)
	if err != nil {
		return revisionGraph{}
	}
	graph, err := prepareGraph(ir, "")
	if err != nil {
		return revisionGraph{}
	}
	return revisionGraph{graph: graph, compiled: true}
}

// node is the node nodeItemFor exposes. Only its definition is taken from
// the revision: the parameters stay out, as they always have, because the
// evaluator reads the trace and the trace does not carry them.
func (revision revisionGraph) node(id, name string) workflow.IRNode {
	node := workflow.IRNode{ID: id, Name: name}
	if compiled, found := revision.graph.nodes[id]; found {
		node.Definition = compiled.Definition
	}
	return node
}

// branches is which output of each earlier node the named node is connected
// to, the one `$('X').all()` read in the run, worked out as the runner works
// it out.
func (revision revisionGraph) branches(nodeID string) map[string]int {
	if !revision.compiled {
		return nil
	}
	return connectedOutputs(revision.graph, nodeID)
}

// evaluationWorkflowContext is `$workflow` for an evaluation, including the
// clock's zone.
//
// The zone is the one the runner resolves at run time — the document's own
// settings.timezone, or the instance default when it names none — because a
// debugger answering `$today` in UTC for a workflow that runs in Europe/Berlin
// would report a different calendar day for part of every day.
func (service *Service) evaluationWorkflowContext(document workflow.Document) expression.WorkflowContext {
	context := service.workflowContext(document)
	if context.Timezone != "" {
		return context
	}
	if declared, ok := document.Settings[workflowTimezoneSetting].(string); ok {
		context.Timezone = strings.TrimSpace(declared)
	}
	return context
}

// expressionOutput decodes one stored node-run output.
//
// Redacted on the way in for the same reason executionResource redacts on the
// way out: a row written before that boundary existed is redacted when it is
// read, and a debugger reading it unredacted would be a new way to see a
// credential's shape.
func expressionOutput(payload json.RawMessage) (workflow.NodeOutput, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	var output workflow.NodeOutput
	if err := json.Unmarshal(execution.Redact(payload), &output); err != nil {
		return nil, false
	}
	return output, true
}

// expressionInput decodes one stored node-run input, redacted as
// expressionOutput is. The items keep the origins they carried, which is what
// pairs `$('X').item` with the item being read.
func expressionInput(payload json.RawMessage) workflow.NodeInput {
	if len(payload) == 0 {
		return nil
	}
	var input workflow.NodeInput
	if err := json.Unmarshal(execution.Redact(payload), &input); err != nil {
		return nil
	}
	return input
}

// expressionPorts is a node's input as the item streams `$input` reads.
func expressionPorts(input workflow.NodeInput) map[string][]map[string]any {
	if input == nil {
		return nil
	}
	ports := make(map[string][]map[string]any, len(input))
	for port, items := range input {
		converted := make([]map[string]any, len(items))
		for position, item := range items {
			converted[position] = item.JSON
		}
		ports[port] = converted
	}
	return ports
}

// jsonKind names the JSON shape an encoded value carries.
//
// A type name derived from the Go value would be a second opinion about the
// same bytes; this one cannot disagree with what the caller received.
func jsonKind(encoded []byte) string {
	trimmed := strings.TrimSpace(string(encoded))
	if trimmed == "" {
		return "null"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
