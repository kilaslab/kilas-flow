package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// waitErrorOutputWorkflow wires start → hold → done, with hold set to continue
// using its error output and that output wired to caught.
func waitErrorOutputWorkflow() workflow.Document {
	link := func(id, source, port, target string) workflow.Connection {
		return workflow.Connection{
			ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port},
			Target: workflow.Endpoint{NodeID: target, Port: "main"},
		}
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_wait_error_output", Name: "Wait with an error output",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1),
				Settings: map[string]any{"onError": workflow.OnErrorContinueBranch}},
			{ID: "done", Name: "Done", Type: "test.done", TypeVersion: workflow.V(1)},
			{ID: "caught", Name: "Caught", Type: "test.caught", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			link("start-hold", "start", "main", "hold"),
			link("hold-done", "hold", "main", "done"),
			link("hold-caught", "hold", "error", "caught"),
		},
		Settings: map[string]any{},
	}
}

// waitFailer is a wait that fails before it ever suspends, as a Wait whose
// parameters do not resolve does.
type waitFailer struct{}

func (waitFailer) Execute(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
	return nil, errors.New("the wait could not read its parameters")
}

// TestAWaitWithAnErrorOutputResumes covers a Wait set to continue using its
// error output. The compiler gives such a node a second output, and the stored
// resume output used to be padded to it — two streams — while the runner
// expected the one the wait itself returns, so every resume failed with
// "resume output for node "hold" has 2 streams, want 1".
//
// Resumed by a decision or by its deadline, the wait now continues on its
// regular output with its error output empty, and a wait that fails before it
// suspends still routes to the error output.
func TestAWaitWithAnErrorOutputResumes(t *testing.T) {
	type outcome struct {
		branch, pruned string
		// hold is the wait's own row: succeeded once resumed, failed when the
		// failure it tolerated is what sent its item down the error output.
		hold, status execution.Status
	}
	regular := outcome{branch: "done", pruned: "caught", hold: execution.StatusSucceeded, status: execution.StatusSucceeded}
	for _, test := range []struct {
		name string
		// hold is the wait's executor; settle resumes it once suspended, and
		// is nil for a wait that never suspends.
		hold   func(clock *waitClock) engine.Executor
		settle func(t *testing.T, ctx context.Context, worker *engine.Service, store *repository.GORMExecutionStore, tenant repository.TenantScope, executionID string, clock *waitClock)
		want   outcome
	}{
		{
			name: "resumed by a decision",
			hold: func(*waitClock) engine.Executor {
				return &waitSuspender{calls: new(int), requests: &[]engine.Request{}, mode: engine.WaitModeApproval}
			},
			settle: func(t *testing.T, ctx context.Context, worker *engine.Service, store *repository.GORMExecutionStore, tenant repository.TenantScope, executionID string, _ *waitClock) {
				wait, err := store.FindActiveWait(ctx, tenant, executionID)
				if err != nil {
					t.Fatalf("FindActiveWait() error = %v", err)
				}
				if _, _, err := worker.ResumeApproval(ctx, tenant.ID, wait.ResumeToken, engine.ApprovalDecision{
					Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC(),
				}, false); err != nil {
					t.Fatalf("ResumeApproval() error = %v", err)
				}
			},
			want: regular,
		},
		{
			name: "resumed by its deadline",
			hold: func(clock *waitClock) engine.Executor {
				return &waitSuspender{calls: new(int), requests: &[]engine.Request{},
					mode: engine.WaitModeInterval, expiresAfter: time.Minute, now: clock.Now}
			},
			settle: func(t *testing.T, ctx context.Context, worker *engine.Service, _ *repository.GORMExecutionStore, _ repository.TenantScope, _ string, clock *waitClock) {
				clock.Advance(time.Minute)
				if settled, err := worker.SweepWaits(ctx); err != nil || settled != 1 {
					t.Fatalf("SweepWaits() = (%d, %v), want the one due wait settled", settled, err)
				}
			},
			want: regular,
		},
		{
			name: "failed before it suspended",
			hold: func(*waitClock) engine.Executor { return waitFailer{} },
			want: outcome{branch: "caught", pruned: "done", hold: execution.StatusFailed, status: execution.StatusSucceeded},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
			if err := catalog.Register(waitTestDefinition("test.caught", "test.passthrough")); err != nil {
				t.Fatalf("Register(caught) error = %v", err)
			}
			clock := newWaitClock()
			timers := &waitTimerStub{}
			for id, executor := range map[string]engine.Executor{
				"test.hold":        test.hold(clock),
				"test.passthrough": &waitPassthrough{calls: new(int), inputs: &[]workflow.NodeInput{}},
			} {
				if err := executors.Register(id, executor); err != nil {
					t.Fatalf("Register(%q) error = %v", id, err)
				}
			}
			saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, waitErrorOutputWorkflow())
			if err != nil {
				t.Fatalf("SaveDraft() error = %v", err)
			}
			queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"order":7}`))
			if err != nil {
				t.Fatalf("QueueManualLatest() error = %v", err)
			}
			worker := waitTestServiceWithClock(t, store, catalog, executors, "worker-0", clock, timers)
			if worked, err := worker.RunOnce(ctx); err != nil || !worked {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
			}
			if test.settle != nil {
				waiting, err := store.Get(ctx, tenant, queued.ID)
				if err != nil {
					t.Fatalf("Get(waiting) error = %v", err)
				}
				if waiting.Status != execution.StatusWaiting {
					t.Fatalf("status = %q, want waiting at the wait (error %s)", waiting.Status, waiting.Error)
				}
				test.settle(t, ctx, worker, store, tenant, queued.ID, clock)
				// A fresh worker, so the resume continues from storage alone.
				worker = waitTestServiceWithClock(t, store, catalog, executors, "worker-1", clock, timers)
				if worked, err := worker.RunOnce(ctx); err != nil || !worked {
					t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
				}
			}

			finished, err := store.Get(ctx, tenant, queued.ID)
			if err != nil {
				t.Fatalf("Get(finished) error = %v", err)
			}
			if finished.Status != test.want.status {
				t.Fatalf("status = %q, want %q (error %s; runs %s)", finished.Status, test.want.status, finished.Error, describeRuns(finished))
			}
			statuses := map[string]execution.Status{}
			for _, run := range finished.NodeRuns {
				statuses[run.NodeID] = run.Status
			}
			if statuses["hold"] != test.want.hold {
				t.Errorf("the wait's run is %q, want %q (runs %s)", statuses["hold"], test.want.hold, describeRuns(finished))
			}
			if statuses[test.want.branch] != execution.StatusSucceeded {
				t.Errorf("%s is %q, want it run on the wait's output (runs %s)", test.want.branch, statuses[test.want.branch], describeRuns(finished))
			}
			if status, ran := statuses[test.want.pruned]; ran && status != execution.StatusSkipped {
				t.Errorf("%s is %q, want its branch untaken (runs %s)", test.want.pruned, status, describeRuns(finished))
			}
		})
	}
}
