package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// tenantVariables is a VariableSource answering a fixed map per tenant, or an
// error for every tenant.
type tenantVariables struct {
	byTenant map[string]map[string]any
	err      error
}

func (source tenantVariables) VariablesFor(_ context.Context, tenant repository.TenantScope) (map[string]any, error) {
	if source.err != nil {
		return nil, source.err
	}
	return source.byTenant[tenant.ID], nil
}

// variablesRun runs a manual → step workflow whose step evaluates
// `{{ $vars.API_BASE }}` and emits it, under the given variable source.
func variablesRun(t *testing.T, source engine.VariableSource, tenant repository.TenantScope) execution.Record {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "variables.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	readVars := func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		item := workflow.Item{JSON: map[string]any{}}
		if items := input["main"]; len(items) > 0 {
			item = items[0]
		}
		value, err := expression.Evaluate("{{ $vars.API_BASE }}", request.ExpressionContext(item, input, 0))
		if err != nil {
			return nil, err
		}
		return workflow.NodeOutput{{{JSON: map[string]any{"base": value}}}}, nil
	}
	catalog := testCatalog(t, stepType("test.readVars", "Read Vars"))
	executions := repository.NewExecutionStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: catalog,
		Runner:         engine.NewRunner(withExecutors(t, map[string]engine.ExecutorFunc{"test.readVars": readVars})),
		WorkerID:       "test-worker",
		DefaultTimeout: 30 * time.Second,
		Variables:      source,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	workflows := repository.NewWorkflowStore(db.DB)
	stored, err := workflows.SaveDraft(ctx, tenant, scopedWorkflow("Reads vars", "test.readVars"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := executions.QueueManualLatest(ctx, tenant, stored.ID, catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the queued execution claimed", worked, err)
	}
	record, err := executions.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return record
}

// `$vars` reads the running tenant's variables, and only that tenant's.
func TestAnExecutionReadsItsTenantsVariables(t *testing.T) {
	source := tenantVariables{byTenant: map[string]map[string]any{
		"tenant-a": {"API_BASE": "https://a.example"},
		"tenant-b": {"API_BASE": "https://b.example"},
	}}
	record := variablesRun(t, source, repository.TenantScope{ID: "tenant-a"})
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (error %s)", record.Status, record.Error)
	}
	output := string(record.Output)
	if !strings.Contains(output, "https://a.example") || strings.Contains(output, "https://b.example") {
		t.Fatalf("output = %s, want tenant-a's API_BASE only", output)
	}
}

// A variable store that cannot be read fails the run instead of running it
// with an empty `$vars`.
func TestAnUnreadableVariableStoreFailsTheRun(t *testing.T) {
	record := variablesRun(t, tenantVariables{err: errors.New("database is gone")}, repository.TenantScope{ID: "tenant-a"})
	if record.Status == execution.StatusSucceeded {
		t.Fatalf("status = %q, want the run to fail when variables cannot be read", record.Status)
	}
	if !strings.Contains(string(record.Error), "variables") {
		t.Fatalf("error = %q, want it to name the variables read", record.Error)
	}
}
