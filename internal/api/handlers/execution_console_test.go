package handlers

import (
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/execution"
)

// An execution's node runs expose what each node printed under `console`, so
// the execution detail page can show it, and leave the field out entirely for
// a node that printed nothing rather than claiming an empty console.
func TestTheNodeRunResourceExposesWhatTheNodePrinted(t *testing.T) {
	console := json.RawMessage(`{"lines":[{"level":"warn","text":"  careful\nnow","at":"2026-09-23T10:00:00Z"}],"truncated":true}`)
	resource := executionResource(execution.Record{
		ID: "exec-console", WorkflowID: "wf-1", WorkflowVersionID: "wfv-1",
		Status: execution.StatusSucceeded, Trigger: execution.TriggerManual,
		NodeRuns: []execution.NodeRun{
			{NodeID: "code", Attempt: 1, Sequence: 1, Status: execution.StatusSucceeded, Console: console},
			{NodeID: "set", Attempt: 1, Sequence: 2, Status: execution.StatusSucceeded},
		},
	})
	encoded, err := json.Marshal(resource)
	if err != nil {
		t.Fatalf("marshal the execution resource: %v", err)
	}
	var body struct {
		NodeRuns []map[string]json.RawMessage `json:"nodeRuns"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	if len(body.NodeRuns) != 2 {
		t.Fatalf("node runs = %d, want 2", len(body.NodeRuns))
	}

	printed, ok := body.NodeRuns[0]["console"]
	if !ok {
		t.Fatalf("code run = %s, want a console field", encoded)
	}
	var detail struct {
		Lines []struct {
			Level string `json:"level"`
			Text  string `json:"text"`
			At    string `json:"at"`
		} `json:"lines"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(printed, &detail); err != nil {
		t.Fatalf("console %s is not a console detail: %v", printed, err)
	}
	if len(detail.Lines) != 1 || detail.Lines[0].Level != "warn" || detail.Lines[0].Text != "  careful\nnow" || detail.Lines[0].At == "" {
		t.Errorf("console lines = %+v, want the one warn line with its whitespace and time", detail.Lines)
	}
	if !detail.Truncated {
		t.Error("console.truncated = false, want true")
	}

	if silent, ok := body.NodeRuns[1]["console"]; ok {
		t.Errorf("set run console = %s, want the field absent: it printed nothing", silent)
	}
}
