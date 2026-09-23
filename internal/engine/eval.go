package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// node outputs are the whole of its input.
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

// EvaluateExpression evaluates one expression against a finished execution's
// stored trace.
//
// An empty nodeID evaluates against the trace as a whole: `$json` is null and
// an expression reads the run through `$node`/`$('Name')`, which is what a
// caller with no particular node in mind wants. A named node must have a trace
// row — a name nothing ran under is refused rather than answered with an empty
// item, which would look exactly like a node that produced nothing.
func (service *Service) EvaluateExpression(ctx context.Context, record execution.Record, document workflow.Document, template, nodeID string) (ExpressionEvaluation, error) {
	template, nodeID = strings.TrimSpace(template), strings.TrimSpace(nodeID)
	if template == "" {
		return ExpressionEvaluation{}, errors.New("expression is required")
	}
	if !strings.Contains(template, "{{") {
		// The grammar is the document grammar. A caller who typed the
		// expression body alone meant that expression, and answering with the
		// literal text would be a debugger that silently echoes its input.
		template = "{{ " + template + " }}"
	}

	roots, traced := service.traceExpressionContext(record, document, nodeID)
	if nodeID != "" && !traced {
		return ExpressionEvaluation{}, fmt.Errorf("%w: %s", ErrExpressionNodeUnknown, nodeID)
	}

	deadline, cancel, err := service.evaluationDeadline(ctx, document, nodeID)
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
// The second return value reports whether the named node has a trace row at
// all. It is separate from whether that row carried items, because a node that
// ran and produced nothing is an answer, while a name nothing ran under is a
// mistake the caller should be told about.
func (service *Service) traceExpressionContext(record execution.Record, document workflow.Document, nodeID string) (expression.Context, bool) {
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

	nodes := make(map[string]map[string]any, len(record.NodeRuns))
	items := make(map[string]expression.NodeItem, len(record.NodeRuns))
	inputs := make(map[string]map[string][]map[string]any, len(record.NodeRuns))
	traced := false
	// In sequence order, so a node that ran more than once — a loop, a retry —
	// is exposed as it last left things, which is the same row the trace view
	// shows last.
	for _, run := range record.NodeRuns {
		if run.NodeID == nodeID {
			traced = true
			inputs[run.NodeID] = expressionPorts(run.Input)
		}
		output, ok := expressionOutput(run.Output)
		if !ok {
			continue
		}
		// nodeItemFor is the runtime's own exposure of a completed node: it is
		// what fills `Items`, the item origins `.item` is paired by and the
		// lineage refusal a multi-item node explains itself with. A second
		// builder here would be a second reader of the same semantics.
		entry := nodeItemFor(workflow.IRNode{ID: run.NodeID, Name: key(run.NodeID)}, output, run.RunIndex)
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
	if nodeID != "" {
		if entry, found := items[key(nodeID)]; found {
			roots.JSON = entry.JSON
		}
		roots.Input = inputs[nodeID]
	}
	return roots, traced
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

// expressionPorts decodes one stored node-run input into the item streams
// `$input` reads.
func expressionPorts(payload json.RawMessage) map[string][]map[string]any {
	if len(payload) == 0 {
		return nil
	}
	var input workflow.NodeInput
	if err := json.Unmarshal(execution.Redact(payload), &input); err != nil {
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
