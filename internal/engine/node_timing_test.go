package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// This file covers BUG-dstsg9: every node run was written with its start and
// finish both set to the moment the row was rendered, so a 30 s agent read
// "0 ms" in the inspector and the CLI, and a slow node could not be found. The
// node.started event carried the real start; the row threw it away.
//
// Time here is the service's clock, which the test owns: the slow step moves it
// by a known amount while it runs, so the assertion is on that amount exactly
// rather than on how long the machine happened to take.

// slowStepType is a test step that takes thirty seconds of the test's clock.
const slowStepType = "kilasflow.test.slowStep"

const slowStepTakes = 30 * time.Second

// slowStep is the executor for slowStepType: it passes its items through after
// moving the clock on by slowStepTakes, as a slow model call or HTTP request
// would.
func slowStep(clock *waitClock) engine.ExecutorFunc {
	return func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
		clock.Advance(slowStepTakes)
		return workflow.NodeOutput{input["main"]}, nil
	}
}

// timedComposition is a composition whose service reads the test's clock, with
// the slow step registered beside the product's nodes.
func timedComposition(t *testing.T) (composition, *waitClock) {
	t.Helper()
	// On a whole second, so a start read back from the store compares equal
	// whatever precision the store keeps.
	clock := &waitClock{now: time.Now().UTC().Truncate(time.Second)}
	setup := newCompositionWith(t, map[string]engine.ExecutorFunc{slowStepType: slowStep(clock)})
	setup.service.SetClockForTest(clock.Now)
	return setup, clock
}

// nodeRunOf is the one row a node wrote in a trace.
func nodeRunOf(t *testing.T, record execution.Record, nodeID string) execution.NodeRun {
	t.Helper()
	var found []execution.NodeRun
	for _, run := range record.NodeRuns {
		if run.NodeID == nodeID {
			found = append(found, run)
		}
	}
	if len(found) != 1 {
		t.Fatalf("node %q wrote %d rows, want 1: %#v", nodeID, len(found), record.NodeRuns)
	}
	return found[0]
}

// durationOf is how long a stored row says its node ran.
func durationOf(t *testing.T, run execution.NodeRun) time.Duration {
	t.Helper()
	if run.FinishedAt == nil {
		t.Fatalf("node %q has no finish time", run.NodeID)
	}
	return run.FinishedAt.Sub(run.StartedAt)
}

func TestANodeRunRecordsWhenItStartedAndWhenItFinished(t *testing.T) {
	setup, clock := timedComposition(t)
	began := clock.Now()
	workflowID := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Slow step",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "slow", Name: "Slow", Type: slowStepType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "slow", Port: "main"},
		}},
		Settings: map[string]any{},
	})

	record := setup.runManual(t, workflowID, `{}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("status = %s, error = %s", record.Status, record.Error)
	}

	slow := nodeRunOf(t, record, "slow")
	if got := durationOf(t, slow); got != slowStepTakes {
		t.Errorf("slow node ran for %v by its row (started %v, finished %v), want %v: the row was stamped when it was written, not when the node started",
			got, slow.StartedAt, slow.FinishedAt, slowStepTakes)
	}
	if !slow.StartedAt.Equal(began) {
		t.Errorf("slow node started at %v, want %v: the moment its executor was called", slow.StartedAt, began)
	}
	// The trigger takes no time on this clock, and it finished before the step
	// it feeds started: the rows read as the timeline the run actually was.
	manual := nodeRunOf(t, record, "manual")
	if got := durationOf(t, manual); got != 0 {
		t.Errorf("manual trigger ran for %v by its row, want 0 on a clock nothing moved", got)
	}
	if manual.FinishedAt.After(slow.StartedAt) {
		t.Errorf("manual trigger finished at %v, after the node it feeds started at %v", manual.FinishedAt, slow.StartedAt)
	}
}

// A sub-workflow's trace is written by a path of its own, once the call
// returns, and it stamped its rows the same way. The child's slow step reads
// its own thirty seconds, and the parent's call reads at least the same: the
// child ran inside it.
func TestASubWorkflowTraceRecordsEachNodesOwnDuration(t *testing.T) {
	setup, _ := timedComposition(t)
	child := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Slow child",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Called", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1)},
			{ID: "slow", Name: "Slow", Type: slowStepType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "start", Port: "main"},
			Target: workflow.Endpoint{NodeID: "slow", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	parent := setup.activate(t, setup.tenant, caller("Parent", child, nil))

	record := setup.runManual(t, parent, `{}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}
	if got := durationOf(t, nodeRunOf(t, record, "call")); got < slowStepTakes {
		t.Errorf("the parent's call ran for %v by its row, want at least the %v its child took", got, slowStepTakes)
	}

	page, err := setup.executions.List(context.Background(), setup.tenant, repository.ExecutionFilter{WorkflowID: child})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("child executions = %d, want one", len(page.Records))
	}
	childRecord, err := setup.executions.Get(context.Background(), setup.tenant, page.Records[0].ID)
	if err != nil {
		t.Fatalf("Get(child) error = %v", err)
	}
	if got := durationOf(t, nodeRunOf(t, childRecord, "slow")); got != slowStepTakes {
		t.Errorf("the child's slow node ran for %v by its row, want %v", got, slowStepTakes)
	}
}

// Each failed attempt is a row of its own and is timed from its own start, so
// a reader sees what each try took. The row that answers is timed from the
// node's start, retries included, which is what n8n reports as the node's
// execution time.
func TestARetriedNodeTimesEachAttemptAndTheWholeNode(t *testing.T) {
	clock := &waitClock{now: time.Now().UTC().Truncate(time.Second)}
	const attemptTakes = 5 * time.Second
	calls := 0
	registry := engine.NewRegistry()
	for id, executor := range map[string]engine.ExecutorFunc{
		"test.trigger": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"seed": 1}}}}, nil
		},
		"test.flaky": func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			calls++
			clock.Advance(attemptTakes)
			if calls == 1 {
				return nil, fmt.Errorf("first try fails")
			}
			return workflow.NodeOutput{input["main"]}, nil
		},
		"test.after": func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{input["main"]}, nil
		},
	} {
		if err := registry.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	ir := retryIR(t, map[string]any{"retryOnFail": true, "maxTries": float64(2), "waitBetweenTries": float64(0)})

	result, err := engine.NewRunner(registry).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}}, Now: clock.Now,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	took := map[int]time.Duration{}
	for _, run := range result.NodeRuns {
		if run.NodeID == "flaky" {
			took[run.Attempt] = run.FinishedAt.Sub(run.StartedAt)
		}
	}
	if got, want := took[1], attemptTakes; got != want {
		t.Errorf("the failed first attempt ran for %v by its row, want %v", got, want)
	}
	if got, want := took[2], 2*attemptTakes; got != want {
		t.Errorf("the answering attempt ran for %v by its row, want %v: the node's whole time, retries included", got, want)
	}
}
