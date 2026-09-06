package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Purging a tenant removes its executions and their node-run traces while a
// neighbour's rows survive untouched, no cell value survives in any
// tenant-scoped row, and a retried purge converges to zero. The drv- tenant
// prefix is what eachDriver's PostgreSQL cleanup deletes.
func TestPurgeTenantRemovesOnlyThatTenantTrace(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenantA := repository.TenantScope{ID: "drv-purge-a"}
		tenantB := repository.TenantScope{ID: "drv-purge-b"}
		workflows := repository.NewWorkflowStore(db.DB)
		executions := repository.NewExecutionStore(db.DB)

		purgedExec := purgeFixtureExecution(t, ctx, executions, workflows, tenantA, "purge_wf_a", "drv-purge-cell-a")
		keptExec := purgeFixtureExecution(t, ctx, executions, workflows, tenantB, "purge_wf_b", "drv-purge-cell-b")

		purged, err := executions.PurgeTenant(ctx, tenantA)
		if err != nil {
			t.Fatalf("PurgeTenant() error = %v", err)
		}
		if purged.Executions != 1 || purged.NodeRuns != 1 {
			t.Errorf("PurgeTenant() = %+v, want 1 execution and 1 node run", purged)
		}

		if _, err := executions.Get(ctx, tenantA, purgedExec.ID); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("Get(purged) = %v, want ErrNotFound", err)
		}
		if page, err := executions.List(ctx, tenantA, repository.ExecutionFilter{}); err != nil || len(page.Records) != 0 {
			t.Errorf("List(purged) = (%v, %v), want empty", page.Records, err)
		}

		kept, err := executions.Get(ctx, tenantB, keptExec.ID)
		if err != nil {
			t.Fatalf("Get(neighbour) error = %v", err)
		}
		if len(kept.NodeRuns) != 1 || string(kept.NodeRuns[0].Output) != `{"cell":"drv-purge-cell-b"}` {
			t.Errorf("neighbour trace = %v, want its single node run intact", kept.NodeRuns)
		}

		if remaining := countCellRows(t, db, "drv-purge-cell-a"); remaining != 0 {
			t.Errorf("%d node-run rows still hold the purged tenant's cell value", remaining)
		}

		again, err := executions.PurgeTenant(ctx, tenantA)
		if err != nil || again.Executions != 0 || again.NodeRuns != 0 {
			t.Errorf("PurgeTenant(retry) = (%+v, %v), want zero", again, err)
		}
		if _, err := executions.PurgeTenant(ctx, repository.TenantScope{}); err == nil {
			t.Error("PurgeTenant(empty tenant) = nil, want an error")
		}
	})
}

func purgeFixtureExecution(t *testing.T, ctx context.Context, executions *repository.GORMExecutionStore, workflows *repository.GORMWorkflowStore, tenant repository.TenantScope, wfID, cell string) execution.Record {
	t.Helper()
	saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            wfID,
		Name:          "Purge source",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := executions.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	claimed, _, ok, err := executions.ClaimNext(ctx, "purge-worker", time.Now().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
	}
	if _, err := executions.CreateNodeRun(ctx, tenant, execution.NodeRun{
		ExecutionID: queued.ID,
		NodeID:      "manual",
		Attempt:     1,
		Sequence:    1,
		Status:      execution.StatusSucceeded,
		Output:      json.RawMessage(`{"cell":"` + cell + `"}`),
		LeaseOwner:  claimed.LeaseOwner,
	}); err != nil {
		t.Fatalf("CreateNodeRun() error = %v", err)
	}
	return queued
}

// countCellRows counts node-run rows whose stored output still carries a
// purged tenant's cell value. Surviving rows must hold none of it: the trace
// projection records counts and row identifiers, never cells, and the purge
// removes the tenant's rows outright.
func countCellRows(t *testing.T, db *database.DB, cell string) int {
	t.Helper()
	var remaining int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM execution_node_runs WHERE output LIKE ?", "%"+cell+"%",
	).Scan(&remaining).Error; err != nil {
		t.Fatalf("count cell rows: %v", err)
	}
	return int(remaining)
}
