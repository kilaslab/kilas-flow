package engine

import (
	"encoding/json"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// checkpointVersion is the newest checkpoint encoding this binary reads and
// writes. Bumped when the shape below changes; a resumed run carrying a
// version it does not know fails loudly rather than continuing on state it
// cannot read.
//
// Version 2 is a checkpoint carrying Partial. A binary that predates it would
// read the field as absent and resume a per-item run with the resumed item
// alone, silently dropping every other item; the bump makes it refuse the
// resume instead. A checkpoint without Partial is still written as version 1,
// the same bytes older binaries write and read.
const (
	checkpointVersion               = 2
	checkpointVersionWithoutPartial = 1
)

// Checkpoint is the exact run state a suspended execution continues from. It
// carries the completed outputs — including values whose keys look sensitive
// — so it is stored verbatim in the waits table, never through the redacting
// payload() path, and never served on any read surface.
type Checkpoint struct {
	Version int `json:"version"`
	// SuspendNode is the node that asked to suspend. The resumed run
	// completes it first, then continues the same pass: no completed node
	// runs twice.
	SuspendNode string `json:"suspendNode"`
	// SuspendAttempt is the attempt the suspension interrupted, so the
	// resumed run's trace row keeps the attempt sequence without colliding
	// with failures recorded before the suspend.
	SuspendAttempt int `json:"suspendAttempt"`
	// SuspendRun is the run the suspension interrupted: how many runs of
	// SuspendNode had completed when it suspended. A node inside a loop, or
	// one resolved item by item, has completed runs from the batches and items
	// before this one, so a completed run of the node is not a reason to
	// refuse the resume; only a completed run at this index is. Checkpoints
	// written before the field existed read 0, which is exact for the first
	// suspension of a node and refuses every later one, as they always did.
	SuspendRun    int    `json:"suspendRun"`
	Mode          string `json:"mode"`
	TriggerNodeID string `json:"triggerNodeId"`
	// Input is what the suspending node was about to run against. Stored so
	// the resumed run records the same input and timer resumes can pass it
	// through unchanged.
	Input     workflow.NodeInput               `json:"input"`
	Completed map[string]workflow.NodeOutput   `json:"completed"`
	Runs      map[string][]workflow.NodeOutput `json:"runs"`
	// Executions counts each node's executed runs, for `$runIndex`. Nil in a
	// checkpoint written before it existed.
	Executions  map[string]int                 `json:"executions,omitempty"`
	NodeOutputs map[string]map[string]any      `json:"nodeOutputs"`
	NodeItems   map[string]expression.NodeItem `json:"nodeItems"`
	// NodeState is the per-execution memory a node keeps between its own
	// invocations, keyed by node ID. A loop's cursor belongs here rather than
	// on the items it dispatches: a body node that replaces an item's fields
	// (an HTTP call, an aggregate) would otherwise destroy the loop's own
	// state and restart it.
	NodeState map[string]map[string]any `json:"nodeState"`
	// Output holds the terminal outputs reached before suspension, so the
	// resumed run's final output merges them instead of dropping branches
	// that finished early.
	Output map[string]workflow.NodeOutput `json:"output"`
	// Pending is the work the suspended run had not reached: the branches it
	// was holding behind the one that suspended. Without it a resumed run
	// would lose every waiting branch and look as though the branch that
	// continues were the whole graph.
	Pending []PendingNode `json:"pending"`
	// Partial is the run a node resolved one item at a time was in the middle
	// of when one of its items suspended: the whole invocation's input, the
	// position that waited, and what the items before it produced. The resume
	// puts the resumed item in its place, resolves the items after it, and
	// completes the node once — one run, however many of its items waited.
	//
	// Absent for a node that runs its items as one invocation, for a per-item
	// node given a single item, and in every checkpoint written before the
	// field existed. Those older checkpoints scheduled the items after the
	// wait as a pending invocation of their own and kept nothing of the items
	// before it; they still resume that way.
	Partial *PartialOutput `json:"partial,omitempty"`
}

// PartialOutput is a per-item run caught at the item that suspended.
type PartialOutput struct {
	// Input is the invocation the run was resolving; its main port holds
	// every item, before and after Position, the one that waited.
	Input    workflow.NodeInput `json:"input"`
	Position int                `json:"position"`
	// Output is every port's items from the items before Position, lineage
	// already stamped, in the node's declared arity (the error port included
	// when it has one).
	Output workflow.NodeOutput `json:"output"`
	// Failed counts the tolerated failures among them, and Error is the first
	// one's message: the row written when the node completes is the whole
	// run's, and has to say it partly failed just as the row of a run that
	// never waited does.
	Failed int    `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
	// Response and Console are what the row would have captured from those
	// items: the first answer the node produced, and what it printed.
	Response json.RawMessage `json:"response,omitempty"`
	Console  json.RawMessage `json:"console,omitempty"`
}

// PendingNode is one scheduled invocation carried across a suspension: the node
// to run, and the items the branch had delivered to it.
type PendingNode struct {
	NodeID string             `json:"nodeId"`
	Input  workflow.NodeInput `json:"input"`
}

func marshalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	checkpoint.Version = checkpointVersionWithoutPartial
	if checkpoint.Partial != nil {
		checkpoint.Version = checkpointVersion
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, fmt.Errorf("encode wait checkpoint: %w", err)
	}
	return raw, nil
}

func unmarshalCheckpoint(raw []byte) (Checkpoint, error) {
	var checkpoint Checkpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode wait checkpoint: %w", err)
	}
	if checkpoint.Version != checkpointVersion && checkpoint.Version != checkpointVersionWithoutPartial {
		return Checkpoint{}, fmt.Errorf("wait checkpoint version %d is not supported", checkpoint.Version)
	}
	// A partial run is only ever written as version 2, with its input and its
	// waiting position. A version-1 checkpoint carrying one has a shape this
	// binary cannot resume from — resuming it would run the node over an empty
	// input and let the pending remainder complete it a second time — so it is
	// refused rather than read as far as it goes.
	if checkpoint.Version == checkpointVersionWithoutPartial && checkpoint.Partial != nil {
		return Checkpoint{}, fmt.Errorf("wait checkpoint version %d carries a partial run, which only version %d may", checkpoint.Version, checkpointVersion)
	}
	if checkpoint.SuspendNode == "" {
		return Checkpoint{}, fmt.Errorf("wait checkpoint names no suspending node")
	}
	if checkpoint.Completed == nil {
		checkpoint.Completed = map[string]workflow.NodeOutput{}
	}
	if checkpoint.Runs == nil {
		checkpoint.Runs = map[string][]workflow.NodeOutput{}
	}
	if checkpoint.NodeOutputs == nil {
		checkpoint.NodeOutputs = map[string]map[string]any{}
	}
	if checkpoint.NodeItems == nil {
		checkpoint.NodeItems = map[string]expression.NodeItem{}
	}
	if checkpoint.NodeState == nil {
		checkpoint.NodeState = map[string]map[string]any{}
	}
	if checkpoint.Pending == nil {
		checkpoint.Pending = []PendingNode{}
	}
	if checkpoint.Output == nil {
		checkpoint.Output = map[string]workflow.NodeOutput{}
	}
	return checkpoint, nil
}
