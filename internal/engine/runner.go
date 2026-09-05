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

// Request is runtime input supplied by the trigger that starts a graph.
type Request struct {
	Input workflow.Item
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

func (registry *Registry) lookup(id string) (Executor, bool) {
	if registry == nil {
		return nil, false
	}
	executor, found := registry.executors[id]
	return executor, found
}

// NodeRun is the deterministic in-memory result of one node invocation.
// Durable storage is injected later at the service boundary.
type NodeRun struct {
	NodeID    string
	Input     workflow.NodeInput
	Output    workflow.NodeOutput
	Error     error
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
	nodes := make(map[string]workflow.IRNode, len(ir.Nodes))
	for _, node := range ir.Nodes {
		nodes[node.ID] = node
	}
	incoming := make(map[string][]workflow.IREdge, len(ir.Nodes))
	outgoing := make(map[string]int, len(ir.Nodes))
	for _, edge := range ir.Edges {
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

	completed := make(map[string]workflow.NodeOutput, len(ir.Nodes))
	result := Result{NodeRuns: make([]NodeRun, 0, len(ir.Nodes)), Output: make(map[string]workflow.NodeOutput)}
	for len(completed) < len(ir.Nodes) {
		ready := make([]string, 0, len(ir.Nodes)-len(completed))
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
		executor, found := runner.executors.lookup(node.Definition.ExecutorID)
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
		result.NodeRuns = append(result.NodeRuns, NodeRun{NodeID: nodeID, Input: cloneInput(input), Output: output})
		if outgoing[nodeID] == 0 {
			result.Output[nodeID] = cloneOutput(output)
		}
	}
	return result, nil
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

func cloneRequest(request Request) Request { return Request{Input: cloneItem(request.Input)} }

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
