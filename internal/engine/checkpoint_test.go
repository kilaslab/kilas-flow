package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A checkpoint an older binary stored has no partial output. It must still
// decode, as the version it always was, and read as having none: the waits
// table holds these until someone answers them, which can be long after an
// upgrade.
func TestACheckpointStoredBeforeThePartialOutputStillDecodes(t *testing.T) {
	old := []byte(`{"version":1,"suspendNode":"hold","suspendAttempt":1,"suspendRun":0,"mode":"approval",` +
		`"triggerNodeId":"","input":{"main":[{"json":{"p":"wait-b"}}]},` +
		`"completed":{"start":[[{"json":{"p":"fail-a"}},{"json":{"p":"wait-b"}}]]},` +
		`"runs":{"start":[[[{"json":{"p":"fail-a"}},{"json":{"p":"wait-b"}}]]]},` +
		`"nodeOutputs":{},"nodeItems":{},"nodeState":{},"output":{},"pending":[]}`)
	checkpoint, err := unmarshalCheckpoint(old)
	if err != nil {
		t.Fatalf("unmarshalCheckpoint(old) error = %v", err)
	}
	if checkpoint.Partial != nil {
		t.Errorf("an old checkpoint decoded with a partial output %#v, want none", checkpoint.Partial)
	}
	if checkpoint.SuspendNode != "hold" || len(checkpoint.Input["main"]) != 1 {
		t.Errorf("the old checkpoint decoded as %#v", checkpoint)
	}
}

// The partial output survives the encoding the waits table stores, items,
// ports and lineage included.
func TestAPartialOutputRoundTripsThroughTheStoredCheckpoint(t *testing.T) {
	partial := &PartialOutput{
		Input: workflow.NodeInput{"main": {
			{JSON: map[string]any{"p": "fail-a"}}, {JSON: map[string]any{"p": "b"}},
			{JSON: map[string]any{"p": "wait-c"}}, {JSON: map[string]any{"p": "d"}},
		}},
		Position: 2,
		Response: json.RawMessage(`{"statusCode":200}`),
		Console:  json.RawMessage(`{"lines":[{"level":"log","text":"b"}]}`),
		Output: workflow.NodeOutput{
			{{JSON: map[string]any{"p": "b"}, Paired: &workflow.PairedItem{SourceNodeID: "start", ItemIndex: 1}}},
			{{JSON: map[string]any{"p": "fail-a", ErrorItemKey: map[string]any{"message": "fail-a broke", "node": "Hold"}},
				Paired: &workflow.PairedItem{SourceNodeID: "start", ItemIndex: 0}}},
		},
		Failed: 1, Error: "fail-a broke",
	}
	raw, err := marshalCheckpoint(Checkpoint{SuspendNode: "hold", Partial: partial})
	if err != nil {
		t.Fatalf("marshalCheckpoint() error = %v", err)
	}
	checkpoint, err := unmarshalCheckpoint(raw)
	if err != nil {
		t.Fatalf("unmarshalCheckpoint() error = %v", err)
	}
	if !reflect.DeepEqual(checkpoint.Partial, partial) {
		t.Errorf("the partial output came back as %#v, want %#v", checkpoint.Partial, partial)
	}
}

// Only a checkpoint carrying a partial run is written as version 2, which a
// binary that predates it refuses instead of resuming a per-item run with one
// item and dropping the rest. Every other checkpoint stays version 1, the
// bytes those binaries read. This binary reads both, and still refuses a
// version it does not know.
func TestOnlyACheckpointCarryingAPartialRunIsVersionTwo(t *testing.T) {
	for _, tc := range []struct {
		partial *PartialOutput
		want    int
	}{{nil, 1}, {&PartialOutput{Output: workflow.NodeOutput{{}}}, 2}} {
		raw, err := marshalCheckpoint(Checkpoint{SuspendNode: "hold", Partial: tc.partial})
		if err != nil {
			t.Fatalf("marshalCheckpoint() error = %v", err)
		}
		var header struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(raw, &header); err != nil || header.Version != tc.want {
			t.Errorf("partial %v: written as version %d (%v), want %d", tc.partial, header.Version, err, tc.want)
		}
		if _, err := unmarshalCheckpoint(raw); err != nil {
			t.Errorf("partial %v: unmarshalCheckpoint() error = %v", tc.partial, err)
		}
	}
	if _, err := unmarshalCheckpoint([]byte(`{"version":3,"suspendNode":"hold"}`)); err == nil {
		t.Error("a version 3 checkpoint decoded, want it refused")
	}
}

func TestAVersionOneCheckpointCarryingAPartialRunIsRefused(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"version":1,"suspendNode":"hold","completed":{},"partial":{"output":[[{"json":{"error":"x"}}]],"failed":1}}`)
	if _, err := unmarshalCheckpoint(raw); err == nil || !strings.Contains(err.Error(), "partial run") {
		t.Fatalf("unmarshalCheckpoint() error = %v, want a refusal naming the partial run", err)
	}
}
