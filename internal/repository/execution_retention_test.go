package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/binary"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// retentionFixture is one workflow and its execution store, ready to run
// against whichever driver eachDriver handed over.
type retentionFixture struct {
	executions *repository.GORMExecutionStore
	tenant     repository.TenantScope
	workflowID string
}

// newRetentionFixture saves a one-node workflow the executions can pin to.
//
// The workflow ID is supplied rather than generated so a driver run against the
// shared PostgreSQL server can be told apart from the rows another test left,
// and so eachDriver's tenant-scoped cleanup reaches it.
func newRetentionFixture(t *testing.T, db *database.DB, workflowID string) retentionFixture {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "drv-retention"}
	workflows := repository.NewWorkflowStore(db.DB)
	saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID, Name: "Retention",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	return retentionFixture{
		executions: repository.NewExecutionStore(db.DB),
		tenant:     tenant,
		workflowID: saved.ID,
	}
}

// queue puts one manual run on the queue and returns it.
func (fixture retentionFixture) queue(t *testing.T) execution.Record {
	t.Helper()
	queued, err := fixture.executions.QueueManualLatest(
		context.Background(), fixture.tenant, fixture.workflowID, driverCatalog(), nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	return queued
}

// finish claims the oldest queued run, writes it a node run, and lands it on a
// terminal status — the shape of every row a prune is meant to remove.
func (fixture retentionFixture) finish(t *testing.T, worker string) execution.Record {
	t.Helper()
	ctx := context.Background()
	claimed, _, ok, err := fixture.executions.ClaimNext(ctx, worker, time.Now().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
	}
	started := time.Now().UTC()
	if _, err := fixture.executions.CreateNodeRun(ctx, fixture.tenant, execution.NodeRun{
		ExecutionID: claimed.ID, NodeID: "manual", Attempt: 1, Sequence: 1,
		Status: execution.StatusSucceeded, StartedAt: started, LeaseOwner: claimed.LeaseOwner,
		Output: json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("CreateNodeRun() error = %v", err)
	}
	finished := claimed
	finished.Status = execution.StatusSucceeded
	completed := time.Now().UTC()
	finished.FinishedAt = &completed
	finished.Output = json.RawMessage(`{"ok":true}`)
	stored, err := fixture.executions.UpdateRuntime(ctx, fixture.tenant, finished)
	if err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}
	return stored
}

// gone reports whether the execution has been deleted.
func (fixture retentionFixture) gone(t *testing.T, executionID string) bool {
	t.Helper()
	_, err := fixture.executions.Get(context.Background(), fixture.tenant, executionID)
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "not found") {
		return true
	}
	t.Fatalf("Get(%s) error = %v", executionID, err)
	return false
}

// nodeRunCount counts the trace rows still attached to an execution.
func nodeRunCount(t *testing.T, db *database.DB, executionID string) int64 {
	t.Helper()
	var count int64
	if err := db.Table("execution_node_runs").
		Where("execution_id = ?", executionID).Count(&count).Error; err != nil {
		t.Fatalf("count node runs: %v", err)
	}
	return count
}

// A finished execution and its node runs go once they are older than the bound.
//
// Both halves matter. execution_node_runs' foreign key to executions is
// declared ON DELETE RESTRICT, so a prune that deleted the execution first
// would fail the whole statement rather than cascading — and one that deleted
// only the execution row could not exist at all.
func TestRetentionRemovesAFinishedExecutionWithItsNodeRuns(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		fixture := newRetentionFixture(t, db, "drv_retain")
		fixture.queue(t)
		finished := fixture.finish(t, "drv-worker")
		if got := nodeRunCount(t, db, finished.ID); got != 1 {
			t.Fatalf("node runs before the prune = %d, want 1", got)
		}

		// A nanosecond of retention puts the cutoff in the immediate past, so
		// anything already finished is expired without the test having to
		// forge a timestamp behind the store's back.
		pruned, err := fixture.executions.PruneExpired(
			context.Background(), repository.ExecutionRetention{MaxAge: time.Nanosecond}, nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 1 {
			t.Errorf("PruneExpired() = %d, want 1", pruned)
		}
		if !fixture.gone(t, finished.ID) {
			t.Error("the expired execution is still stored")
		}
		if got := nodeRunCount(t, db, finished.ID); got != 0 {
			t.Errorf("node runs after the prune = %d, want 0 — the trace outlived its execution", got)
		}
	})
}

// An execution that has not been finished for long enough stays.
func TestRetentionKeepsAnExecutionInsideTheAgeBound(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		fixture := newRetentionFixture(t, db, "drv_retain_young")
		fixture.queue(t)
		finished := fixture.finish(t, "drv-worker")

		pruned, err := fixture.executions.PruneExpired(
			context.Background(), repository.ExecutionRetention{MaxAge: time.Hour}, nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 0 {
			t.Errorf("PruneExpired() = %d, want 0", pruned)
		}
		if fixture.gone(t, finished.ID) {
			t.Error("an execution that finished a moment ago was deleted by an hour of retention")
		}
	})
}

// Retention off keeps everything, which is what an upgrade must get.
func TestRetentionOffDeletesNothing(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		fixture := newRetentionFixture(t, db, "drv_retain_off")
		fixture.queue(t)
		finished := fixture.finish(t, "drv-worker")

		pruned, err := fixture.executions.PruneExpired(
			context.Background(), repository.ExecutionRetention{}, nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 0 {
			t.Errorf("PruneExpired() = %d, want 0 — the zero policy deleted history nobody asked it to", pruned)
		}
		if fixture.gone(t, finished.ID) {
			t.Error("the zero retention policy deleted a finished execution")
		}
	})
}

// A queued, running or cancelling execution survives however old it is.
//
// The age bound is read from finished_at, which none of these three has, and
// the status is checked again in the DELETE itself. A prune that took a queued
// run would silently drop work nobody cancelled; one that took a running run
// would delete the row its own worker is about to write back to.
func TestRetentionNeverRemovesAQueuedRunningOrCancellingExecution(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		fixture := newRetentionFixture(t, db, "drv_retain_active")

		queued := fixture.queue(t)

		fixture.queue(t)
		running, _, ok, err := fixture.executions.ClaimNext(ctx, "drv-worker-run", time.Now().Add(time.Hour))
		if err != nil || !ok {
			t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
		}

		fixture.queue(t)
		claimed, _, ok, err := fixture.executions.ClaimNext(ctx, "drv-worker-cancel", time.Now().Add(time.Hour))
		if err != nil || !ok {
			t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
		}
		cancelling, err := fixture.executions.Cancel(ctx, fixture.tenant, claimed.ID)
		if err != nil {
			t.Fatalf("Cancel() error = %v", err)
		}
		if cancelling.Status != execution.StatusCancelling {
			t.Fatalf("status after Cancel() = %q, want cancelling", cancelling.Status)
		}

		pruned, err := fixture.executions.PruneExpired(
			ctx, repository.ExecutionRetention{MaxAge: time.Nanosecond}, nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 0 {
			t.Errorf("PruneExpired() = %d, want 0 — it took work out of the queue", pruned)
		}
		for name, id := range map[string]string{
			"queued": queued.ID, "running": running.ID, "cancelling": cancelling.ID,
		} {
			if fixture.gone(t, id) {
				t.Errorf("the %s execution was pruned", name)
			}
		}
	})
}

// A non-terminal execution that carries a finished_at is still not pruned.
//
// The three cases above are excluded by the age predicate alone, because a run
// that is queued, on a worker or being cancelled has no finished_at to compare.
// This is the row that separates the age predicate from the status guard:
// Create takes a whole record, so a caller can store a running execution with a
// finished timestamp, and on that row the finished_at test says "expired" and
// only the status test says "leave it alone".
func TestRetentionRefusesANonTerminalExecutionThatCarriesAFinishedAt(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		fixture := newRetentionFixture(t, db, "drv_retain_mixed")

		// A version to pin to, obtained the way every execution gets one.
		queued := fixture.queue(t)

		long := time.Now().UTC().Add(-30 * 24 * time.Hour)
		running, err := fixture.executions.Create(ctx, fixture.tenant, execution.Record{
			WorkflowID:        fixture.workflowID,
			WorkflowVersionID: queued.WorkflowVersionID,
			Status:            execution.StatusRunning,
			Trigger:           execution.TriggerManual,
			StartedAt:         long,
			FinishedAt:        &long,
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}

		pruned, err := fixture.executions.PruneExpired(
			ctx, repository.ExecutionRetention{MaxAge: time.Hour}, nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 0 {
			t.Errorf("PruneExpired() = %d, want 0 — a running execution was deleted on age alone", pruned)
		}
		if fixture.gone(t, running.ID) {
			t.Error("the running execution was pruned, so its worker will write back to a row that is gone")
		}
	})
}

// A backlog larger than one batch is cleared, a batch at a time.
//
// The batch bound is what keeps a prune from holding one transaction — and on
// SQLite the whole database — open across a history of millions. It only earns
// that if the sweep still finishes, so the loop is what this pins.
func TestRetentionClearsABacklogInBoundedBatches(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		fixture := newRetentionFixture(t, db, "drv_retain_batch")
		const backlog = 5
		finished := make([]string, 0, backlog)
		for range backlog {
			fixture.queue(t)
			finished = append(finished, fixture.finish(t, "drv-worker").ID)
		}

		pruned, err := fixture.executions.PruneExpired(
			context.Background(),
			repository.ExecutionRetention{MaxAge: time.Nanosecond, BatchSize: 2},
			nil)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != backlog {
			t.Errorf("PruneExpired() = %d, want %d — the sweep stopped after its first batch", pruned, backlog)
		}
		for _, id := range finished {
			if !fixture.gone(t, id) {
				t.Errorf("execution %s survived the sweep", id)
			}
		}
	})
}

// Pruning an execution removes the payloads it wrote to disk.
//
// Through engine.Service.DiscardBinaries rather than a directory the pruner
// works out for itself: the store keys payloads by tenant and execution, and a
// pruner that deleted only the rows would leave the disk growing with nothing
// left to say what is on it.
func TestRetentionDiscardsTheStoredBinaryPayloads(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		fixture := newRetentionFixture(t, db, "drv_retain_binary")
		fixture.queue(t)
		finished := fixture.finish(t, "drv-worker")

		root := t.TempDir()
		payloads, err := binary.NewFileStore(root, 1<<20)
		if err != nil {
			t.Fatalf("NewFileStore() error = %v", err)
		}
		scope := binary.Scope{TenantID: fixture.tenant.ID, ExecutionID: finished.ID}
		if _, err := payloads.Put(scope, "report.txt", "text/plain", strings.NewReader("attachment")); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if countFiles(t, root) == 0 {
			t.Fatal("the payload was not written, so its removal proves nothing")
		}

		runtime, err := engine.NewService(engine.ServiceDeps{
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
			Executions:     fixture.executions,
			Binaries:       payloads,
			Catalog:        driverCatalog(),
			Runner:         engine.NewRunner(engine.NewRegistry()),
			WorkerID:       "drv-pruner",
			DefaultTimeout: time.Minute,
		})
		if err != nil {
			t.Fatalf("NewService() error = %v", err)
		}

		pruned, err := fixture.executions.PruneExpired(
			ctx, repository.ExecutionRetention{MaxAge: time.Nanosecond}, runtime.DiscardBinaries)
		if err != nil {
			t.Fatalf("PruneExpired() error = %v", err)
		}
		if pruned != 1 {
			t.Fatalf("PruneExpired() = %d, want 1", pruned)
		}
		if got := countFiles(t, root); got != 0 {
			t.Errorf("%d payload files survived the prune — the rows went and the disk did not", got)
		}
	})
}

// countFiles counts the regular files under a directory tree.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return count
}
