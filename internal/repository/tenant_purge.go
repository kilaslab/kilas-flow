package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// TenantPurgeResult names what purging one tenant removed: the execution
// rows, the node-run rows that traced them, the waits that suspended them and
// the idempotency keys that recorded them. It is evidence for a deletion
// request, which is honoured only when the caller can see that it was.
type TenantPurgeResult struct {
	Executions int
	NodeRuns   int
	Waits      int
	// IdempotencyKeys counts the request-idempotency keys the tenant owned.
	IdempotencyKeys int
}

// PurgeTenant deletes every execution, node run and wait one tenant owns, and
// the idempotency keys it holds, in one transaction. It is the repository half
// of a tenant deletion: the datastore engine's PurgeTenant owns the catalogue
// and the physical tables, and this owns the trace, because a datastore row
// written once is copied into a node-run row that outlives the datastore.
//
// The waits go first: execution_waits.execution_id is ON DELETE RESTRICT, so a
// leftover wait keeps its execution — and every node run below it — alive.
//
// The idempotency keys travel with the trace: a key's recorded outcome names
// the execution a run queued or the datastore row a write returned, so it is
// the tenant's data and outlives neither.
//
// Node runs and waits go first, in that order. Both foreign keys into
// executions are declared ON DELETE RESTRICT, so deleting an execution that
// still has a trace or a suspended run fails the whole statement rather than
// cascading — and a tenant whose approval has been waiting for days is the
// ordinary case, not an edge one.
//
// An empty tenant purges to zero rather than failing, so a retried deletion
// converges. An empty tenant id is refused outright: it matches nothing, and
// a purge that matches nothing by accident is one typo away from a purge
// that matches everything by accident.
func (store *GORMExecutionStore) PurgeTenant(ctx context.Context, tenant TenantScope) (TenantPurgeResult, error) {
	if err := tenant.validate(); err != nil {
		return TenantPurgeResult{}, err
	}

	var result TenantPurgeResult
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		nodeRuns := tx.Where("tenant_id = ?", tenant.ID).Delete(&executionNodeRunModel{})
		if nodeRuns.Error != nil {
			return fmt.Errorf("purge tenant node runs: %w", nodeRuns.Error)
		}
		waits := tx.Where("tenant_id = ?", tenant.ID).Delete(&executionWaitModel{})
		if waits.Error != nil {
			return fmt.Errorf("purge tenant waits: %w", waits.Error)
		}
		executions := tx.Where("tenant_id = ?", tenant.ID).Delete(&executionModel{})
		if executions.Error != nil {
			return fmt.Errorf("purge tenant executions: %w", executions.Error)
		}
		// Keep this when the purge orchestrator lands: the schema-driven
		// completeness test requires every tenant_id table be covered. No
		// foreign key ties the keys to executions, so the order is free.
		keys, err := purgeIdempotencyKeys(tx, tenant)
		if err != nil {
			return err
		}
		result = TenantPurgeResult{
			Executions:      int(executions.RowsAffected),
			NodeRuns:        int(nodeRuns.RowsAffected),
			Waits:           int(waits.RowsAffected),
			IdempotencyKeys: int(keys),
		}
		return nil
	})
	if err != nil {
		return TenantPurgeResult{}, err
	}
	return result, nil
}
