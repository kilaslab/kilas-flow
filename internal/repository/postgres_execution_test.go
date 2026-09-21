package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// eachDriver runs a test against SQLite and, when one is configured, against a
// live PostgreSQL.
//
// It exists because there was no repository test on PostgreSQL at all, and the
// absence had a cost: an UPDATE that SQLite's type affinity accepted and
// PostgreSQL rejected at parse time shipped, and left every execution on that
// tier running for ever. A test that only ever sees one driver cannot see a
// difference between two.
func eachDriver(t *testing.T, run func(t *testing.T, db *database.DB)) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("sqlite", func(t *testing.T) {
		db, err := database.Open(context.Background(), config.Database{
			Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
		}, quiet)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := database.Migrate(db, quiet); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		run(t, db)
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
		}
		db, err := database.Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, quiet)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := database.Migrate(db, quiet); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}
		// The server is shared between runs, so the rows this test writes are
		// removed rather than the tables dropped — another test may be using
		// them.
		//
		// By tenant rather than by identifier prefix, and naming the table an
		// execution's node runs are actually in. The first spelling of this
		// deleted from `node_runs`, which is not a table, and matched
		// executions on `id LIKE 'drv_%'`, which no execution has — a queued
		// run is given a generated `exec_` ID. Both errors are discarded, so
		// every row every run wrote stayed behind, the workflow delete then
		// failed on the executions foreign key, and the leftovers were
		// invisible until a test tried to count what was in the table.
		t.Cleanup(func() {
			db.Exec(`DELETE FROM idempotency_keys WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM webhook_deliveries WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM webhook_routes WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM webhook_bindings WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM schedules WHERE tenant_id LIKE 'drv-%'`)
			// Waits before executions: execution_waits.execution_id is
			// ON DELETE RESTRICT, so a leftover wait would silently keep its
			// execution and every node run below it alive for the next run.
			db.Exec(`DELETE FROM execution_waits WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM execution_node_runs WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM executions WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM workflow_publish_events WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM workflow_versions WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM workflows WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM secret_bindings WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM credentials WHERE tenant_id LIKE 'drv-%'`)
			// Identity last, and keys and users before the tenant they
			// reference: both are ON DELETE RESTRICT.
			db.Exec(`DELETE FROM api_keys WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM users WHERE tenant_id LIKE 'drv-%'`)
			db.Exec(`DELETE FROM tenants WHERE id LIKE 'drv-%'`)
		})
		run(t, db)
	})
}

// driverCatalog is the smallest catalogue that lets a manual run be queued.
func driverCatalog() activationCatalog {
	return activationCatalog{"kilasflow.manual": {
		Type: "kilasflow.manual", Version: workflow.V(1),
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}}
}

// An execution reaches a terminal status on every driver this server supports.
//
// The assertion is deliberately about what is *stored*, not what UpdateRuntime
// returns. The defect this pins wrote nothing at all — the whole UPDATE was
// rejected during parse analysis because a CASE of untyped placeholders types
// as text and the output column is bytea — so status, finished_at and the
// lease columns were all left as they were, and the row was reclaimed and
// re-run once a minute for ever.
func TestAnExecutionReachesATerminalStatusOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tenant"}
		workflows := repository.NewWorkflowStore(db.DB)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "drv_wf", Name: "Driver parity",
			Nodes: []workflow.Node{{
				ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
			}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}

		executions := repository.NewExecutionStore(db.DB)
		queued, err := executions.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), "", nil)
		if err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		claimed, _, ok, err := executions.ClaimNext(ctx, "drv-worker", time.Now().Add(time.Minute))
		if err != nil || !ok {
			t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
		}
		if claimed.ID != queued.ID {
			t.Fatalf("claimed %q, want %q", claimed.ID, queued.ID)
		}

		finished := claimed
		finished.Status = execution.StatusSucceeded
		completed := time.Now().UTC().Truncate(time.Second)
		finished.FinishedAt = &completed
		finished.Output = json.RawMessage(`{"greet":"hello"}`)
		if _, err := executions.UpdateRuntime(ctx, tenant, finished); err != nil {
			t.Fatalf("UpdateRuntime() error = %v", err)
		}

		// Read back through a fresh query rather than trusting the return
		// value: the defect was in what reached the table.
		stored, err := executions.Get(ctx, tenant, queued.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if stored.Status != execution.StatusSucceeded {
			t.Errorf("status = %q, want succeeded — the terminal write did not reach the table", stored.Status)
		}
		if stored.FinishedAt == nil {
			t.Error("finishedAt is nil, so the row still looks like it is running")
		}
		if stored.LeaseOwner != "" {
			t.Errorf("leaseOwner = %q, want it released — a held lease is reclaimed and the workflow runs again",
				stored.LeaseOwner)
		}
		if stored.Output == nil {
			t.Error("output is nil, so the run's result was lost")
		}
	})
}

// The cancellation race the two updates replace a CASE for still works.
//
// An execution asked to cancel while it was running must land on cancelled
// with the cancellation error, whatever outcome the worker reports — that is
// what the original CASE expressed, and replacing it must not lose it.
func TestACancellingExecutionLandsOnCancelledOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-tenant"}
		workflows := repository.NewWorkflowStore(db.DB)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "drv_wf2", Name: "Cancellation parity",
			Nodes: []workflow.Node{{
				ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
			}},
			Connections: []workflow.Connection{}, Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		executions := repository.NewExecutionStore(db.DB)
		if _, err := executions.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), "", nil); err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		claimed, _, ok, err := executions.ClaimNext(ctx, "drv-worker", time.Now().Add(time.Minute))
		if err != nil || !ok {
			t.Fatalf("ClaimNext() = (%v, %v), want a claim", ok, err)
		}
		if _, err := executions.Cancel(ctx, tenant, claimed.ID); err != nil {
			t.Fatalf("Cancel() error = %v", err)
		}

		// The worker finishes and reports success, unaware of the cancellation.
		finished := claimed
		finished.Status = execution.StatusSucceeded
		completed := time.Now().UTC().Truncate(time.Second)
		finished.FinishedAt = &completed
		finished.Output = json.RawMessage(`{"greet":"hello"}`)
		if _, err := executions.UpdateRuntime(ctx, tenant, finished); err != nil {
			t.Fatalf("UpdateRuntime() error = %v", err)
		}

		stored, err := executions.Get(ctx, tenant, claimed.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if stored.Status != execution.StatusCancelled {
			t.Errorf("status = %q, want cancelled — the cancellation lost the race it must win", stored.Status)
		}
		if stored.FinishedAt == nil {
			t.Error("finishedAt is nil on a cancelled execution")
		}
	})
}
