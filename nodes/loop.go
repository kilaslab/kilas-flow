package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// LoopExecutorID is the server-owned binding for the batch loop.
const LoopExecutorID = "core.loop"

// LoopNodeType dispatches its input in batches, one batch per iteration.
const LoopNodeType = "kilasflow.loop"

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
//
// It lives in the runner's per-node state for this execution, not on the items
// the loop dispatches: a body node that builds its output from scratch — an
// HTTP call, a Code node, an aggregate — would drop anything riding on an item
// and the loop would restart on its own output, forever.
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
// Every later call is the body wiring back in, carrying the items the body
// produced. Which of the two it is comes from the runner's node state, never
// from anything on the items: the same input that restarts a lost loop is
// indistinguishable from a body's return if the state rides on the items.
func executeLoop(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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

	fields := request.NodeState[ir.ID]
	if fields == nil {
		return nil, fmt.Errorf("node %q: loop state is not available in this runtime", ir.Name)
	}
	incoming := input["main"]
	state, returning := decodeLoopState(fields)
	if !returning {
		// First entry: the upstream items are the work.
		state = loopState{Pending: cloneItems(incoming)}
	} else {
		// The body came back: everything it produced is this iteration's
		// result.
		state.Collected = append(state.Collected, cloneItems(incoming)...)
	}

	if len(state.Pending) == 0 {
		// Nothing left: the accumulated items leave on `done` and the body is
		// given an empty stream, which the runner prunes. The state is cleared
		// in place so a later dispatch of this node starts a new loop rather
		// than appending to a finished one; the map is the runner's, so the
		// clear is visible to it.
		resetLoopState(fields)
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
	encodeLoopState(fields, state)
	return workflow.NodeOutput{[]workflow.Item{}, cloneItems(batch)}, nil
}

// decodeLoopState reads the state the runner kept for this node. It reports
// false when nothing has been dispatched yet, which is the only thing that
// makes an invocation a first entry.
func decodeLoopState(fields map[string]any) (loopState, bool) {
	raw, present := fields["iteration"]
	if !present {
		return loopState{}, false
	}
	state := loopState{}
	if iteration, ok := raw.(float64); ok {
		state.Iteration = int(iteration)
	}
	if cursor, ok := fields["cursor"].(float64); ok {
		state.Cursor = int(cursor)
	}
	state.Pending = stateItems(fields["pending"])
	state.Collected = stateItems(fields["collected"])
	return state, true
}

// encodeLoopState writes the state back into the runner's map.
//
// The items are stored as workflow.Item values, so binary references and
// paired-item lineage survive a checkpoint round trip — the alternative, a
// JSON-ish projection, is what loses them.
func encodeLoopState(fields map[string]any, state loopState) {
	fields["iteration"] = float64(state.Iteration)
	fields["cursor"] = float64(state.Cursor)
	fields["pending"] = state.Pending
	fields["collected"] = state.Collected
}

// resetLoopState empties the entry in place. Replacing the map outright would
// write to the copy the executor was handed rather than to the runner's own
// entry, and the finished loop would come back on the next dispatch.
func resetLoopState(fields map[string]any) {
	for key := range fields {
		delete(fields, key)
	}
}

// stateItems reads an item list out of node state.
//
// In memory the value is the []workflow.Item that was stored; after a
// checkpoint round trip the same value arrives as decoded JSON, so both shapes
// have to be readable.
func stateItems(raw any) []workflow.Item {
	switch typed := raw.(type) {
	case []workflow.Item:
		return cloneItems(typed)
	case []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var items []workflow.Item
		if err := json.Unmarshal(encoded, &items); err != nil {
			return nil
		}
		return items
	default:
		return nil
	}
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
