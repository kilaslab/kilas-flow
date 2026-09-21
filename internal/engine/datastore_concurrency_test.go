package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// This file is the execution-level proof for FEAT-1axhdn's criterion 2: the
// row store's atomicity put through the shipped worker pool at the shipped
// default width, rather than through the store's own API. The store tests
// show that one statement cannot lose a write; this shows that ten
// executions of a real workflow — claimed from the durable queue by the
// workers engine.Service.Start spawns, compiled and run by the same runner
// the server uses — leave the counter at exactly the number of runs.
//
// It runs on SQLite (file-backed, so the workers share nothing but the
// database) and on PostgreSQL when KILASFLOW_TEST_POSTGRES_DSN names a live
// server, the same gate the rest of the repository's PostgreSQL halves use.

// datastoreExecutionDriver is one dialect the execution-level proof runs
// against. SQLite always runs; PostgreSQL joins when a server is named.
type datastoreExecutionDriver struct {
	name   string
	driver string
	dsn    string
}

func datastoreExecutionDrivers() []datastoreExecutionDriver {
	drivers := []datastoreExecutionDriver{{name: "sqlite", driver: "sqlite"}}
	if dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN"); dsn != "" {
		drivers = append(drivers, datastoreExecutionDriver{name: "postgres", driver: "postgres", dsn: dsn})
	}
	return drivers
}

// openDatastoreExecutionDatabase opens the handle these tests drive the
// engine through, migrates it, and returns it.
//
// The SQLite pool opt-in exists for criterion 8, which asks what a widened
// pool does to the concurrency suites. Widening is expected to expose
// repository-layer failures — ClaimNext's read-then-write transaction on a
// multi-connection SQLite pool does not survive workers competing for the
// same file — and CI never sets the variable, so the default stays the
// production pin of one connection.
func openDatastoreExecutionDatabase(t *testing.T, driver, dsn string) *database.DB {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if driver == "sqlite" && dsn == "" {
		dsn = filepath.Join(t.TempDir(), "kilasflow-engine.db")
	}
	db, err := database.Open(context.Background(), config.Database{Driver: driver, DSN: dsn}, quiet)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", driver, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if driver == "sqlite" {
		if raw := os.Getenv("KILASFLOW_TEST_SQLITE_POOL"); raw != "" {
			width, convErr := strconv.Atoi(raw)
			if convErr != nil || width <= 0 {
				t.Fatalf("KILASFLOW_TEST_SQLITE_POOL=%q is not a positive integer", raw)
			}
			widenDatastoreExecutionPool(t, db, width)
		}
	}
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate(%s) error = %v", driver, err)
	}
	return db
}

// widenDatastoreExecutionPool raises the handle's pool. It is the test-only
// counterpart of database.Open's SetMaxOpenConns(1) for SQLite: production
// pinning is untouched, and nothing in the engine package may depend on the
// single connection that hides races today.
func widenDatastoreExecutionPool(t *testing.T, db *database.DB, width int) {
	t.Helper()
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("access underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(width)
	sqlDB.SetMaxIdleConns(width)
}

// datastoreExecutionTenant mints a tenant id unique to this run. The
// PostgreSQL server is shared between runs, and FEAT-fpqvwx's purge is a
// different ticket's work: this test cleans up after itself instead.
func datastoreExecutionTenant(name string) repository.TenantScope {
	return repository.TenantScope{ID: fmt.Sprintf("drv-engine-%s-%d", name, time.Now().UnixNano())}
}

// cleanDatastoreExecutionTenant removes everything this run wrote. The
// datastore's physical table is dropped by the engine (it carries a
// generated name), so only the tenant-scoped rows are removed here.
func cleanDatastoreExecutionTenant(db *database.DB, tenant repository.TenantScope) {
	for _, table := range []string{"execution_node_runs", "execution_waits", "executions", "workflow_versions", "workflows"} {
		_ = db.Exec("DELETE FROM "+table+" WHERE tenant_id = ?", tenant.ID).Error
	}
}

// datastoreIncrementDocument is the workflow under test: a manual trigger
// into one Data table node that adds one to the row's counter, then a Set
// node that carries the value the increment returned on the item stream.
//
// The Set node is the observation point, not decoration. The engine's trace
// path replaces a Data table node's durable output with
// datastore.ProjectTrace's envelope — row identifiers and a count, never
// cell contents — so the counter the increment returned is only observable
// downstream, where it travels as an ordinary item field. Reading it here
// proves what each run actually wrote, not just where the total ended.
//
// The filter addresses the row by id, which is the address an increment
// takes.
func datastoreIncrementDocument(dsID string, rowID int64) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_ds_increment",
		Name:          "Datastore counter",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{
				ID: "ds", Name: "Data table", Type: nodes.DatastoreNodeType, TypeVersion: nodes.DatastoreVersion,
				Parameters: map[string]any{
					"resource":    "row",
					"operation":   nodes.DatastoreOperationIncrement,
					"dataTableId": property.WriteLocator(property.Locator{Mode: "id", Value: dsID}),
					"match":       "all",
					"filters": map[string]any{"conditions": []any{map[string]any{
						"keyName": "id", "condition": "eq", "keyValue": strconv.FormatInt(rowID, 10),
					}}},
					"counterColumn": "n",
					"amount":        1,
				},
			},
			{
				ID: "observed", Name: "Observed counter", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{
					"mode": "manual",
					"assignments": map[string]any{
						"counter": map[string]any{"mode": "expression", "value": "{{ $json.n }}"},
					},
				},
			},
		},
		Connections: []workflow.Connection{
			{
				ID: "manual-ds", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "ds", Port: "main"},
			},
			{
				ID: "ds-observed", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "ds", Port: "main"},
				Target: workflow.Endpoint{NodeID: "observed", Port: "main"},
			},
		},
		Settings: map[string]any{},
	}
}

// TestTenConcurrentExecutionsIncrementOneDatastoreRow is criterion 2 as the
// ticket words it: ten executions, one row, one counter, run at
// execution.max_concurrent of 10 — the shipped default from config.Default,
// asserted rather than assumed — and the row ends at exactly ten with the
// ten runs' own outputs being exactly the values 1..10.
func TestTenConcurrentExecutionsIncrementOneDatastoreRow(t *testing.T) {
	runDatastoreIncrementExecutions(t, 10)
}

// TestHundredExecutionsIncrementOneDatastoreRowSoak is the same proof over a
// hundred executions: ten workers, a hundred claims, every write landed and
// every returned post-image distinct.
func TestHundredExecutionsIncrementOneDatastoreRowSoak(t *testing.T) {
	runDatastoreIncrementExecutions(t, 100)
}

func runDatastoreIncrementExecutions(t *testing.T, executions int) {
	for _, drv := range datastoreExecutionDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			db := openDatastoreExecutionDatabase(t, drv.driver, drv.dsn)
			store, err := datastore.NewEngine(db, "")
			if err != nil {
				t.Fatalf("NewEngine() error = %v", err)
			}
			tenant := datastoreExecutionTenant(drv.name)
			t.Cleanup(func() { cleanDatastoreExecutionTenant(db, tenant) })

			definition, err := store.Create(ctx, tenant.ID, "counters", []datastore.ColumnInput{{Name: "n", Type: string(datastore.ColumnNumber)}})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			t.Cleanup(func() { _ = store.Drop(context.Background(), tenant.ID, definition.ID) })
			row, err := store.Insert(ctx, tenant.ID, definition.ID, map[string]any{"n": 0.0})
			if err != nil {
				t.Fatalf("Insert() error = %v", err)
			}
			rowID, ok := row["id"].(int64)
			if !ok {
				t.Fatalf("inserted row id = %#v, want an int64", row["id"])
			}

			catalog := node.NewRegistry()
			if err := nodes.RegisterAll(catalog); err != nil {
				t.Fatalf("RegisterAll() error = %v", err)
			}
			executors := engine.NewRegistry()
			if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil,
				nodes.WithDatastoreEngine(store)); err != nil {
				t.Fatalf("RegisterExecutors() error = %v", err)
			}

			stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, datastoreIncrementDocument(definition.ID, rowID))
			if err != nil {
				t.Fatalf("SaveDraft() error = %v", err)
			}
			executionStore := repository.NewExecutionStore(db.DB)
			ids := make([]string, 0, executions)
			for range executions {
				queued, err := executionStore.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", json.RawMessage(`{"tick":1}`))
				if err != nil {
					t.Fatalf("QueueManualLatest() error = %v", err)
				}
				ids = append(ids, queued.ID)
			}

			service, err := engine.NewService(engine.ServiceDeps{
				Executions:     executionStore,
				Catalog:        catalog,
				Runner:         engine.NewRunner(executors),
				WorkerID:       "drv-engine-worker",
				DefaultTimeout: 30 * time.Second,
			})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			// The criterion names execution.max_concurrent of 10, so the
			// width is read from the shipped default and asserted here: a
			// test that passed at a width of one would prove nothing.
			maxConcurrent := config.Default().Execution.MaxConcurrent
			if maxConcurrent != 10 {
				t.Fatalf("config.Default().Execution.MaxConcurrent = %d, want the 10 the criterion names", maxConcurrent)
			}

			runCtx, cancel := context.WithCancel(context.Background())
			if err := service.Start(runCtx, maxConcurrent); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			waitForDatastoreExecutions(t, ctx, cancel, executionStore, tenant, ids)
			cancel()
			drainCtx, stopDrain := context.WithTimeout(context.Background(), 30*time.Second)
			defer stopDrain()
			if err := service.Drain(drainCtx); err != nil {
				t.Fatalf("Drain() error = %v", err)
			}

			// The counter is what the workflow is for: ten runs, ten
			// increments, ten — not nine, not eleven.
			final, err := store.Get(ctx, tenant.ID, definition.ID, rowID)
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got := final["n"]; got != float64(executions) {
				t.Errorf("counter = %v, want exactly %d: a write was lost or counted twice", got, executions)
			}

			// And the runs' own returned post-images are the values 1..N,
			// each exactly once: no two workers read the same value, so the
			// returned counter is the whole history rather than the end
			// state alone.
			seen := map[int]int{}
			for _, id := range ids {
				record, err := executionStore.Get(ctx, tenant, id)
				if err != nil {
					t.Fatalf("Get(%s) error = %v", id, err)
				}
				for _, run := range record.NodeRuns {
					if run.NodeID != "observed" {
						continue
					}
					seen[datastoreCounterOutput(t, id, run.Output)]++
				}
			}
			if len(seen) != executions {
				t.Errorf("distinct returned counter values = %d (%s), want %d", len(seen), datastoreCounterSummary(seen), executions)
			}
			for value := 1; value <= executions; value++ {
				if seen[value] != 1 {
					t.Errorf("returned counter value %d appeared %d times (%s), want exactly once", value, seen[value], datastoreCounterSummary(seen))
				}
			}
		})
	}
}

// waitForDatastoreExecutions polls every queued execution until all of them
// are succeeded, printing each one's status when the deadline passes.
func waitForDatastoreExecutions(t *testing.T, ctx context.Context, cancel context.CancelFunc, store *repository.GORMExecutionStore, tenant repository.TenantScope, ids []string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		report := make([]string, 0, len(ids))
		terminal := make([]string, 0)
		pending := 0
		for _, id := range ids {
			record, err := store.Get(ctx, tenant, id)
			if err != nil {
				cancel()
				t.Fatalf("Get(%s) error = %v", id, err)
			}
			report = append(report, fmt.Sprintf("%s=%s", id, record.Status))
			switch record.Status {
			case execution.StatusSucceeded:
			case execution.StatusFailed, execution.StatusCancelled:
				terminal = append(terminal, fmt.Sprintf("%s=%s", id, record.Status))
			default:
				pending++
			}
		}
		if len(terminal) > 0 {
			cancel()
			t.Fatalf("executions did not succeed: %s (every status: %s)", strings.Join(terminal, " "), strings.Join(report, " "))
		}
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("executions still pending after %s: %s", time.Minute, strings.Join(report, " "))
		}
		select {
		case <-ctx.Done():
			cancel()
			t.Fatal("test context cancelled while waiting for executions")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// datastoreCounterOutput reads one run's returned counter from the Set node
// that observed it. The run's output is the node output shape — one list of
// items per port — and the Set node copies the increment's own post-image
// field onto the item it passes on.
func datastoreCounterOutput(t *testing.T, executionID string, output json.RawMessage) int {
	t.Helper()
	var ports [][]workflow.Item
	if err := json.Unmarshal(output, &ports); err != nil {
		t.Fatalf("decode %s observed output: %v (%s)", executionID, err, output)
	}
	if len(ports) != 1 || len(ports[0]) != 1 {
		t.Fatalf("decode %s observed output = %s, want one item on one port", executionID, output)
	}
	value, ok := ports[0][0].JSON["counter"].(float64)
	if !ok {
		t.Fatalf("decode %s observed output = %s, want the counter the increment returned", executionID, output)
	}
	return int(value)
}

// datastoreCounterSummary renders the returned counter values in order, so a
// failure names every value and how often it came back.
func datastoreCounterSummary(seen map[int]int) string {
	values := make([]string, 0, len(seen))
	for value, count := range seen {
		values = append(values, fmt.Sprintf("%d x%d", value, count))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
