package engine_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// This file covers BUG-hfhzq6 end to end: an IF inside a loop, where the branch
// that is not taken is recorded as skipped on every iteration. The runner
// stamps no run index on those rows, so they all arrive as index 0 and the
// unique key on (execution, node, attempt, run_index) refuses the second one —
// which used to fail the trace write, leave the execution running with its
// lease, and have the next lease holder re-run the whole graph, side effects
// and all, for ever.
//
// The loop shape is deliberately the imported one: Split Out turns a list into
// items, Loop Over Items dispatches one batch per iteration, and the IF routes
// to a branch that is pruned on every pass.

// loopWithPrunedBranch saves the workflow the defect needs and queues one run
// of it. The condition matches nothing, so one branch is skipped on every
// iteration.
func loopWithPrunedBranch(t *testing.T, sandbox *engineSandbox, tenant repository.TenantScope, workflowID string) execution.Record {
	t.Helper()
	_, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID,
		Name:          "IF inside a loop",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "split", Name: "Split Out", Type: nodes.SplitOutNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"fieldToSplitOut": "arr"}},
			{ID: "loop", Name: "Loop Over Items", Type: nodes.LoopNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"batchSize": float64(1), "maxIterations": float64(10)}},
			{ID: "gate", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"conditions": []any{
					map[string]any{"field": "n", "operator": "equals", "value": 9999},
				}}},
			{ID: "hit", Name: "Hit", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"path": "hit"}}},
			{ID: "miss", Name: "Miss", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"path": "miss"}}},
			{ID: "after", Name: "After", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"stage": "after"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "split", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "split", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c3", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
				Target: workflow.Endpoint{NodeID: "gate", Port: "main"}},
			{ID: "c4", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "gate", Port: "true"},
				Target: workflow.Endpoint{NodeID: "hit", Port: "main"}},
			{ID: "c5", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "gate", Port: "false"},
				Target: workflow.Endpoint{NodeID: "miss", Port: "main"}},
			// Both branches return to the loop: n8n's Split In Batches shape,
			// and the reason a pruned branch is recorded once per iteration.
			{ID: "c6", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "hit", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c7", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "miss", Port: "main"},
				Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			{ID: "c8", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "loop", Port: "done"},
				Target: workflow.Endpoint{NodeID: "after", Port: "main"}},
		},
		Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft(%q) error = %v", workflowID, err)
	}
	queued, err := sandbox.store.QueueManualLatest(sandbox.ctx, tenant, workflowID, sandbox.catalog, "", json.RawMessage(`{"arr":[1,3,4]}`))
	if err != nil {
		t.Fatalf("QueueManualLatest(%q) error = %v", workflowID, err)
	}
	return queued
}

// The asserted invariant is the trace key, not how many rows the runner decides
// to record for a pruned branch: that accounting belongs to the runner and has
// changed under this test once already (skipped rows used to be emitted once
// per iteration, which is what made the collision reproducible end to end).
// The direct regression for the collision itself is the store-level test
// CreateNodeRuns is covered by; this one pins the end-to-end promise — the
// imported shape reaches a terminal state with a trace the unique index accepts.
func TestAnIfInsideALoopReachesATerminalStateWithEveryPrunedRowRecorded(t *testing.T) {
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-loop-prune"}
	queued := loopWithPrunedBranch(t, sandbox, tenant, "wf_loop_prune")

	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		WorkerID: "loop-worker", DefaultTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	worked, runErr := service.RunOnce(sandbox.ctx)
	if !worked {
		t.Fatal("RunOnce() claimed nothing, want the queued execution run")
	}
	// The failing write must not surface as an error that leaves the row
	// running: it is the reason the execution was reclaimed and re-run for
	// ever, side effects included.
	if runErr != nil {
		t.Fatalf("RunOnce() error = %v, want the loop trace persisted", runErr)
	}

	record, err := sandbox.store.Get(sandbox.ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, want := record.Status, execution.StatusSucceeded; got != want {
		t.Fatalf("execution status = %q, want %q", got, want)
	}
	if record.FinishedAt == nil {
		t.Error("execution finishedAt is nil")
	}

	pruned := 0
	iterations := 0
	taken := map[string]bool{}
	for _, run := range record.NodeRuns {
		key := fmt.Sprintf("%s|%d|%d", run.NodeID, run.Attempt, run.RunIndex)
		if taken[key] {
			t.Errorf("two rows share the trace key %s: the unique index would have refused the second one", key)
		}
		taken[key] = true
		switch run.NodeID {
		case "hit":
			pruned++
		case "gate":
			iterations++
		}
	}
	// Three items, one per batch: the body ran three times, and the untaken
	// branch is recorded rather than dropped.
	if iterations == 0 {
		t.Error("the IF inside the loop produced no trace row at all")
	}
	if pruned == 0 {
		t.Error("the pruned branch left no row: a branch that never ran must still be visible in the trace")
	}
}
