package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/kilaslabs/kilas-flow/internal/execution"
)

// ExecutionRetention bounds how much execution history an installation keeps.
//
// It is one global policy rather than one per tenant because tenancy on the
// main API does not exist yet — every request resolves to the default tenant —
// so a per-tenant policy would have no authority to attach itself to. When
// tenants gain a real identity this becomes a per-tenant lookup, and the
// pruner's shape does not have to change for it.
type ExecutionRetention struct {
	// MaxAge deletes an execution that has been finished for longer than this.
	// Zero keeps every execution and is the default, so an upgrade never
	// silently deletes a customer's run history.
	MaxAge time.Duration
	// BatchSize bounds one delete transaction. Zero takes
	// DefaultExecutionPruneBatch.
	BatchSize int
}

// prunes reports whether this policy would delete anything.
func (policy ExecutionRetention) prunes() bool { return policy.MaxAge > 0 }

// DefaultExecutionPruneBatch is how many executions one prune transaction
// removes.
//
// Small enough that the transaction holds its row locks briefly and, on SQLite,
// gives the single writer back promptly; large enough that clearing a backlog
// of millions does not cost a round trip per row.
const DefaultExecutionPruneBatch = 200

// activeExecutionStatuses are the statuses a prune must never touch. An
// execution in one of them is queued, on a worker, or being cancelled, and
// deleting it would take work out of the queue that nobody asked to cancel.
var activeExecutionStatuses = []string{
	string(execution.StatusQueued),
	string(execution.StatusRunning),
	string(execution.StatusCancelling),
}

// PruneExpired deletes executions that finished before the policy's age bound,
// with their node runs and their stored binary payloads, and reports how many
// went.
//
// discard removes one execution's stored payloads — engine.Service's
// DiscardBinaries in the server, nil where there is no binary storage. It is a
// callback rather than a dependency because the engine imports this package,
// and it is called before the rows go rather than after: the execution row is
// the only record of what that execution wrote to disk, so a failure between
// the two must leave a row the next sweep retries, not a directory nothing
// points at any more.
//
// Selection is by finished_at and never by started_at. A queued or running
// execution has no finished_at at all, and an execution that started three
// weeks ago and is still going is exactly the row a prune must leave alone.
func (store *GORMExecutionStore) PruneExpired(
	ctx context.Context,
	policy ExecutionRetention,
	discard func(tenantID, executionID string) error,
) (int, error) {
	if !policy.prunes() {
		return 0, nil
	}
	batch := policy.BatchSize
	if batch <= 0 {
		batch = DefaultExecutionPruneBatch
	}
	// Fixed once for the whole sweep rather than recomputed per batch, so a
	// long sweep cannot chase a moving cutoff and never terminate.
	cutoff := time.Now().UTC().Add(-policy.MaxAge)

	total := 0
	for {
		// Between batches rather than inside one: a shutdown stops the sweep at
		// a transaction boundary instead of aborting one half-done.
		if err := ctx.Err(); err != nil {
			return total, err
		}

		candidates, err := store.expiredExecutions(ctx, cutoff, batch)
		if err != nil {
			return total, err
		}
		if len(candidates) == 0 {
			return total, nil
		}

		if discard != nil {
			for _, candidate := range candidates {
				if err := discard(candidate.TenantID, candidate.ID); err != nil {
					return total, fmt.Errorf("discard the payloads of execution %s: %w", candidate.ID, err)
				}
			}
		}

		removed, err := store.deleteExecutions(ctx, cutoff, candidates)
		if err != nil {
			return total, err
		}
		total += removed

		// A batch that removed nothing means another process got there first,
		// and looping would re-read the same rows for ever. The next sweep
		// picks up whatever it left.
		if removed == 0 || len(candidates) < batch {
			return total, nil
		}
	}
}

// expiredExecution is one candidate for deletion, carrying the tenant that owns
// its payloads.
type expiredExecution struct {
	ID       string
	TenantID string
}

// expiredExecutions reads one batch of prunable executions, oldest first.
//
// It runs outside a transaction on purpose: the read is the long part of a
// sweep over a large history, and holding it open would keep a write
// transaction — on SQLite, the whole database — locked for its duration.
func (store *GORMExecutionStore) expiredExecutions(ctx context.Context, cutoff time.Time, batch int) ([]expiredExecution, error) {
	var candidates []expiredExecution
	err := store.db.WithContext(ctx).Model(&executionModel{}).
		Select("id", "tenant_id").
		Where("finished_at IS NOT NULL AND finished_at < ? AND status NOT IN ?", cutoff, activeExecutionStatuses).
		Order("finished_at ASC, id ASC").
		Limit(batch).
		Find(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("read the executions to prune: %w", err)
	}
	return candidates, nil
}

// deleteExecutions removes one batch and its node runs in a single transaction.
func (store *GORMExecutionStore) deleteExecutions(ctx context.Context, cutoff time.Time, candidates []expiredExecution) (int, error) {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}

	var removed int64
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Node runs first. execution_node_runs' foreign key to executions is
		// declared ON DELETE RESTRICT, so deleting an execution that still has
		// a trace fails the whole statement rather than cascading.
		if err := tx.Where("execution_id IN ?", ids).Delete(&executionNodeRunModel{}).Error; err != nil {
			return fmt.Errorf("prune execution node runs: %w", err)
		}
		// The age and status predicates are repeated rather than trusting the
		// ids the read returned. Another KilasFlow process may be sweeping the
		// same database and a row may already be gone; more to the point,
		// re-stating them is what makes this statement, read on its own, unable
		// to delete a run that is queued, on a worker, or being cancelled.
		result := tx.Where(
			"id IN ? AND finished_at IS NOT NULL AND finished_at < ? AND status NOT IN ?",
			ids, cutoff, activeExecutionStatuses,
		).Delete(&executionModel{})
		if result.Error != nil {
			return fmt.Errorf("prune executions: %w", result.Error)
		}
		removed = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(removed), nil
}
