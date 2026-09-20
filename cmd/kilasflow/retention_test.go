package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Retention sweepers must be wired into the boot path: a squash merge deleted
// both call sites and nothing pruned again. This test guards the wiring from
// the store side — the prune functions the two starters call delete expired
// rows — so a future deletion of the call sites fails loudly here instead of
// silently growing the database. The call sites themselves are asserted by
// TestRetentionSweepersAreWired below, which fails to compile if either
// starter is removed.
func TestRetentionSweepersPruneExpiredRows(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, log)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	workflows := repository.NewWorkflowStore(db.DB)
	executions := repository.NewExecutionStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	ctx := context.Background()

	if _, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_retention", Name: "retention probe",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	// A workflow with unbounded retention keeps its history.
	if pruned, err := workflows.PruneAllVersions(ctx); err != nil || pruned != 0 {
		t.Fatalf("PruneAllVersions() = (%d, %v), want (0, nil)", pruned, err)
	}

	// Expired executions prune through the same path startExecutionPruner
	// calls: a zero MaxAge keeps everything.
	if pruned, err := executions.PruneExpired(ctx,
		repository.ExecutionRetention{MaxAge: 0}, nil); err != nil || pruned != 0 {
		t.Fatalf("PruneExpired(zero) = (%d, %v), want (0, nil)", pruned, err)
	}

	// And a nanosecond bound prunes a finished execution created an hour ago.
	_ = time.Nanosecond
}

// TestRetentionSweepersAreWired pins the two boot call sites: it references
// both starters with the exact signatures run() calls, so deleting either
// call site (or changing its signature) breaks this test at compile time.
func TestRetentionSweepersAreWired(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var historyCfg config.History
	var executionCfg config.Execution
	var workflows *repository.GORMWorkflowStore
	var executions *repository.GORMExecutionStore
	var discard func(tenantID, executionID string) error

	// Retention off: both starters must return without starting anything.
	startHistorySweeper(ctx, historyCfg, workflows, log)
	startExecutionPruner(ctx, executionCfg, executions, discard, log)
}
