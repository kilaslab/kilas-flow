package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A timer wait resumes on its own, so there is no decision to record and no
// approval page to offer: it reports when it resumes instead. Only an approval
// wait links its approval page. Every mode keeps the machine resume link,
// because a call-resumed wait with a limit is held as a timer and a call to
// that link is what ends it early.
func TestWaitingLinksOfferAnApprovalPageOnlyToApprovalWaits(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	clock := newWaitClock()
	timers := &waitTimerStub{}
	hold := &waitSuspender{}
	var calls int
	var inputs []workflow.NodeInput
	hold.calls = &calls
	hold.requests = &[]engine.Request{}
	waitRegisterFakes(t, executors, hold, &calls, &inputs)
	saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	service := waitTestServiceWithClock(t, store, catalog, executors, "worker-1", clock, timers)

	for _, mode := range []string{engine.WaitModeInterval, engine.WaitModeUntil, engine.WaitModeApproval, engine.WaitModeWebhook} {
		t.Run(mode, func(t *testing.T) {
			hold.mode = mode
			hold.expiresAt = clock.Now().Add(5 * time.Minute)
			queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("QueueManualLatest() error = %v", err)
			}
			if worked, err := service.RunOnce(ctx); err != nil || !worked {
				t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
			}
			wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
			if err != nil {
				t.Fatalf("FindActiveWait() error = %v", err)
			}
			links, ok := service.WaitingLinks(ctx, tenant, queued.ID)
			if !ok {
				t.Fatal("WaitingLinks() found no wait for a waiting execution")
			}
			if !strings.HasSuffix(links.ResumeURL, "/resume/"+wait.ResumeToken) {
				t.Errorf("resume URL = %q, want the wait's resume link", links.ResumeURL)
			}
			switch mode {
			case engine.WaitModeApproval:
				if !strings.HasSuffix(links.ApprovalURL, "/approve/"+wait.ResumeToken) {
					t.Errorf("approval URL = %q, want the approval page", links.ApprovalURL)
				}
				if links.ResumeAt != nil {
					t.Errorf("resume at = %v, want none: an approval's deadline fails it, it does not resume it", links.ResumeAt)
				}
			case engine.WaitModeInterval, engine.WaitModeUntil:
				if links.ApprovalURL != "" {
					t.Errorf("approval URL = %q, want none for a timer wait", links.ApprovalURL)
				}
				if links.ResumeAt == nil || !links.ResumeAt.Equal(wait.ExpiresAt) {
					t.Errorf("resume at = %v, want the timer's deadline %v", links.ResumeAt, wait.ExpiresAt)
				}
			default:
				if links.ApprovalURL != "" || links.ResumeAt != nil {
					t.Errorf("webhook wait links = %+v, want only the resume URL", links)
				}
			}
		})
	}
}

// Stopping a waiting run lands on the node it waited on. The live feed says so
// before the execution's own cancellation, so an open page stops showing the
// node as waiting, and the trace keeps the row for the next reload.
func TestCancellingAWaitingRunMarksItsWaitNodeCancelled(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	hold := &waitSuspender{mode: engine.WaitModeInterval, expiresAfter: 5 * time.Minute}
	var calls int
	var inputs []workflow.NodeInput
	hold.calls = &calls
	hold.requests = &[]engine.Request{}
	waitRegisterFakes(t, executors, hold, &calls, &inputs)
	saved, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, waitTestWorkflow())
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	broker := events.NewBroker(events.BrokerOptions{SubscriberQueue: 64, BufferPerExecution: 64})
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: store, Catalog: catalog, Runner: engine.NewRunner(executors),
		WorkerID: "worker-1", DefaultTimeout: 30 * time.Second, Events: broker,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", worked, err)
	}
	awaitExecutionStatus(t, store, tenant, queued.ID, execution.StatusWaiting)
	subscription := broker.Subscribe(tenant.ID, queued.ID, 0)
	t.Cleanup(subscription.Close)

	cancelled, err := service.Cancel(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.Status != execution.StatusCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}

	nodeCancelled := false
	deadline := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case event, open := <-subscription.Events():
			if !open {
				done = true
				break
			}
			if event.NodeID == "hold" && event.Type == events.NodeFailed && event.Status == execution.StatusCancelled {
				nodeCancelled = true
			}
			if event.Type == events.ExecutionCancelled {
				if !nodeCancelled {
					t.Error("execution.cancelled arrived before the wait node was marked cancelled")
				}
				done = true
			}
		case <-deadline:
			t.Fatal("no execution.cancelled event was published")
		}
	}
	if !nodeCancelled {
		t.Error("no node.failed event marked the wait node cancelled")
	}

	reloaded, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	last := reloaded.NodeRuns[len(reloaded.NodeRuns)-1]
	if last.NodeID != "hold" || last.Status != execution.StatusCancelled {
		t.Errorf("last node run = %s/%s, want hold/cancelled; runs=%s", last.NodeID, last.Status, describeRuns(reloaded))
	}
}
