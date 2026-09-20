package engine

import (
	"encoding/json"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// checkpointVersion is the only checkpoint encoding this binary reads and
// writes. Bumped when the shape below changes; a resumed run carrying any
// other version fails loudly rather than continuing on state it cannot read.
const checkpointVersion = 1

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
	SuspendAttempt int    `json:"suspendAttempt"`
	Mode           string `json:"mode"`
	TriggerNodeID  string `json:"triggerNodeId"`
	// Input is what the suspending node was about to run against. Stored so
	// the resumed run records the same input and timer resumes can pass it
	// through unchanged.
	Input       workflow.NodeInput               `json:"input"`
	Completed   map[string]workflow.NodeOutput   `json:"completed"`
	Runs        map[string][]workflow.NodeOutput `json:"runs"`
	NodeOutputs map[string]map[string]any        `json:"nodeOutputs"`
	NodeItems   map[string]expression.NodeItem   `json:"nodeItems"`
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
}

// PendingNode is one scheduled invocation carried across a suspension: the node
// to run, and the items the branch had delivered to it.
type PendingNode struct {
	NodeID string             `json:"nodeId"`
	Input  workflow.NodeInput `json:"input"`
}

func marshalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	checkpoint.Version = checkpointVersion
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
	if checkpoint.Version != checkpointVersion {
		return Checkpoint{}, fmt.Errorf("wait checkpoint version %d is not supported", checkpoint.Version)
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
