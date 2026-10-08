package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// This file covers BUG-c7s5ss: what a finished run says about its nodes. The
// execution-level error named the failing node by its internal id, and a sticky
// note — documentation on the canvas — was not to be listed as a node the run
// executed.

const failingStepType = "kilasflow.test.failingStep"

func failingStep(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
	return nil, errors.New("upstream said no")
}

func passThrough(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{input["main"]}, nil
}

func annotatedFailingWorkflow() workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Annotated",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "n2", Name: "Call billing", Type: failingStepType, TypeVersion: workflow.V(1)},
			{ID: "note", Name: "Read me", Type: nodes.StickyNoteNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"content": "## Billing"}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "n2", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

func TestAFailedRunNamesTheNodeByItsName(t *testing.T) {
	setup := newCompositionWith(t, map[string]engine.ExecutorFunc{failingStepType: failingStep})
	workflowID := setup.activate(t, setup.tenant, annotatedFailingWorkflow())

	record := setup.runManual(t, workflowID, `{}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %s, want failed", record.Status)
	}
	if !strings.Contains(string(record.Error), `Call billing`) {
		t.Errorf("run error = %q, want the node's name, not only its id", record.Error)
	}
}

func TestAStickyNoteIsNotListedAsARunNode(t *testing.T) {
	// A note has no ports, so nothing feeds it, and the run used to seed it as
	// a root next to the trigger: it ran its no-op executor and was listed as a
	// node that succeeded with no items.
	setup := newCompositionWith(t, map[string]engine.ExecutorFunc{failingStepType: passThrough})
	workflowID := setup.activate(t, setup.tenant, annotatedFailingWorkflow())

	record := setup.runManual(t, workflowID, `{}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("status = %s, error = %s", record.Status, record.Error)
	}
	for _, run := range record.NodeRuns {
		if run.NodeID == "note" {
			t.Errorf("the sticky note is listed as a run node: %#v", run)
		}
	}
}
