package nodes

import (
	"context"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// LoopExecutorID is the server-owned binding for the batch loop.
const LoopExecutorID = "core.loop"

// LoopNodeType dispatches its input in batches, one batch per iteration.
const LoopNodeType = "kilasflow.loop"

// LoopStateKey is where a loop node keeps its remaining work between
// iterations.
//
// The state travels on the item rather than in the runner. That keeps the
// runner free of node-specific knowledge — it schedules a graph, it does not
// know what a loop is — and it means the state lands in the execution record
// for free, so an operator can see which batch a run was on when it failed.
const LoopStateKey = "$loop"

// DefaultLoopMaxIterations bounds a loop that never converges.
const DefaultLoopMaxIterations = 100

// loopNode dispatches its input a batch at a time.
//
// The `loop` output runs the body; the body's last node wires back into this
// node's input, which is the back edge the compiler allows because this
// definition declares LoopEntry. When the batches run out, the accumulated
// items leave on `done` and the nodes below it run exactly once.
func loopNode() node.Definition {
	return node.Definition{
		Type:        LoopNodeType,
		Version:     workflow.V(1),
		DisplayName: "Loop Over Items",
		Description: "Splits incoming items into batches and runs the connected body once per batch.",
		Category:    "Flow",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:repeat"},
		IconColor:   "#f59e0b",
		LoopEntry:   true,
		Inputs:      mainInput(),
		Outputs: []workflow.Port{
			// `done` first, so it is output index 0 and a workflow that only
			// wires the finished branch behaves like an ordinary node.
			{Name: "done", Kind: workflow.ConnectionMain},
			{Name: "loop", Kind: workflow.ConnectionMain},
		},
		Parameters: []node.PropertyDefinition{
			{Key: "batchSize", Label: "Batch Size", Kind: node.PropertyNumber, Default: 1,
				Description: "How many items each iteration receives."},
			{Key: "maxIterations", Label: "Maximum Iterations", Kind: node.PropertyNumber, Default: DefaultLoopMaxIterations,
				Description: "Fails the execution rather than looping forever. A truncated loop that reported success would be worse."},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     LoopExecutorID,
		Validate:       validateLoopConfiguration,
	}
}

func validateLoopConfiguration(n workflow.Node) error {
	if size, ok := numericParameter(n.Parameters, "batchSize"); ok && size < 1 {
		return fmt.Errorf("batchSize must be at least 1")
	}
	if max, ok := numericParameter(n.Parameters, "maxIterations"); ok {
		if max < 1 {
			return fmt.Errorf("maxIterations must be at least 1")
		}
		if max > workflow.MaxLoopIterations {
			return fmt.Errorf("maxIterations must not exceed %d", workflow.MaxLoopIterations)
		}
	}
	return nil
}

// loopState is what a loop node remembers between iterations.
type loopState struct {
	// Cursor is how many items have already been dispatched.
	Cursor int
	// Iteration is how many batches have been dispatched, counting from 1.
	Iteration int
	// Pending is the work not yet dispatched.
	Pending []workflow.Item
	// Collected is every item the body has returned so far, which is what
	// leaves on `done`.
	Collected []workflow.Item
}

// executeLoop dispatches one batch per invocation.
//
// The first call sees the upstream items and takes the whole set as its work.
// Every later call is the body wiring back in, carrying the state it was handed
// and the items the body produced.
func executeLoop(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batchSize := 1
	if size, ok := numericParameter(ir.Parameters, "batchSize"); ok && size >= 1 {
		batchSize = int(size)
	}
	maxIterations := DefaultLoopMaxIterations
	if max, ok := numericParameter(ir.Parameters, "maxIterations"); ok && max >= 1 {
		maxIterations = int(max)
	}

	incoming := input["main"]
	state, returning := readLoopState(incoming)
	if !returning {
		// First entry: the upstream items are the work.
		state = loopState{Pending: cloneItems(incoming)}
	} else {
		// The body came back. Everything it produced, minus the state marker,
		// is this iteration's result.
		state.Collected = append(state.Collected, strippedItems(incoming)...)
	}

	if len(state.Pending) == 0 {
		// Nothing left: the accumulated items leave on `done` and the body is
		// given an empty stream, which the runner prunes.
		return workflow.NodeOutput{state.Collected, []workflow.Item{}}, nil
	}

	state.Iteration++
	if state.Iteration > maxIterations {
		// Failing beats emitting `done`: a truncated loop that reported success
		// would hand downstream nodes a partial result they cannot tell from a
		// complete one.
		return nil, fmt.Errorf("loop node %q exceeded its maximum of %d iterations", ir.Name, maxIterations)
	}

	size := batchSize
	if size > len(state.Pending) {
		size = len(state.Pending)
	}
	batch := state.Pending[:size]
	state.Pending = state.Pending[size:]
	state.Cursor += size

	dispatched := make([]workflow.Item, 0, len(batch))
	for _, item := range batch {
		dispatched = append(dispatched, cloneItem(item))
	}
	// The state rides on the first item of the batch, so whatever the body does
	// to the others it comes back intact.
	if len(dispatched) > 0 {
		dispatched[0].JSON[LoopStateKey] = encodeLoopState(state)
	}
	return workflow.NodeOutput{[]workflow.Item{}, dispatched}, nil
}

func readLoopState(items []workflow.Item) (loopState, bool) {
	for _, item := range items {
		raw, present := item.JSON[LoopStateKey]
		if !present {
			continue
		}
		if state, ok := decodeLoopState(raw); ok {
			return state, true
		}
	}
	return loopState{}, false
}

// strippedItems removes the loop's own bookkeeping so it never reaches a
// downstream node's `$json`.
func strippedItems(items []workflow.Item) []workflow.Item {
	stripped := make([]workflow.Item, 0, len(items))
	for _, item := range items {
		copied := cloneItem(item)
		delete(copied.JSON, LoopStateKey)
		stripped = append(stripped, copied)
	}
	return stripped
}

func encodeLoopState(state loopState) map[string]any {
	return map[string]any{
		"cursor":    float64(state.Cursor),
		"iteration": float64(state.Iteration),
		"pending":   itemsToAny(state.Pending),
		"collected": itemsToAny(state.Collected),
	}
}

func decodeLoopState(raw any) (loopState, bool) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return loopState{}, false
	}
	state := loopState{}
	if cursor, ok := fields["cursor"].(float64); ok {
		state.Cursor = int(cursor)
	}
	if iteration, ok := fields["iteration"].(float64); ok {
		state.Iteration = int(iteration)
	}
	state.Pending = anyToItems(fields["pending"])
	state.Collected = anyToItems(fields["collected"])
	return state, true
}

func itemsToAny(items []workflow.Item) []any {
	encoded := make([]any, 0, len(items))
	for _, item := range items {
		encoded = append(encoded, cloneMap(item.JSON))
	}
	return encoded
}

func anyToItems(raw any) []workflow.Item {
	entries, ok := raw.([]any)
	if !ok {
		return nil
	}
	items := make([]workflow.Item, 0, len(entries))
	for _, entry := range entries {
		if fields, ok := entry.(map[string]any); ok {
			items = append(items, workflow.Item{JSON: cloneMap(fields)})
		}
	}
	return items
}

// numericParameter coerces a node parameter to a number. Parameters arrive from
// JSON, so an integer is a float64 and a hand-written document may hold either.
func numericParameter(parameters map[string]any, key string) (float64, bool) {
	switch typed := parameters[key].(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
