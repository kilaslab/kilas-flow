package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// QueueDepth counts the executions waiting for a worker, the number the
// metrics gauge reads at scrape time. Only queued rows count: a running
// execution is not waiting, and a finished one never was.
func TestQueueDepthCountsOnlyQueuedExecutions(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-queue-depth"}
		workflows := repository.NewWorkflowStore(db.DB)
		executions := repository.NewExecutionStore(db.DB)

		depth, err := executions.QueueDepth(ctx)
		if err != nil {
			t.Fatalf("QueueDepth() error = %v", err)
		}
		if depth != 0 {
			t.Fatalf("QueueDepth() on an empty queue = %d, want 0", depth)
		}

		saved, err := workflows.SaveDraft(ctx, tenant, hostEventDocument("wf_queue_depth", "message.received"))
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := executions.QueueManualLatest(ctx, tenant, saved.ID, hostEventCatalog(), "", nil); err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		if depth, err = executions.QueueDepth(ctx); err != nil || depth != 1 {
			t.Fatalf("QueueDepth() = (%d, %v), want the one queued execution", depth, err)
		}

		// Claiming the execution takes it off the queue: the depth reads the
		// state a worker sees, not the state an API just wrote.
		if _, _, claimed, err := executions.ClaimNext(ctx, "queue-depth-worker", time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
			t.Fatalf("ClaimNext() = (%v, %v), want the claim", claimed, err)
		}
		if depth, err = executions.QueueDepth(ctx); err != nil || depth != 0 {
			t.Fatalf("QueueDepth(after claim) = (%d, %v), want 0", depth, err)
		}
	})
}
