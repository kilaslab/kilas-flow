package engine_test

import (
	"context"
	"encoding/json"
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
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// staticSetup is a migrated database, the stores and a service that keeps
// workflow static data, with one active workflow whose Code node counts its
// runs in the static data.
type staticSetup struct {
	composition
	static     *repository.GORMStaticDataStore
	workflowID string
	versionID  string
}

func newStaticSetup(t *testing.T) staticSetup {
	t.Helper()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(ctx, config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "static.db")}, logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, logger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := testCatalog(t)
	executionStore := repository.NewExecutionStore(db.DB)
	static := repository.NewStaticDataStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog, StaticData: static,
		Runner: engine.NewRunner(withExecutors(t, nil)), WorkerID: "test-worker",
		DefaultTimeout: 30 * time.Second, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	setup := staticSetup{
		composition: composition{
			executions: executionStore, workflows: repository.NewWorkflowStore(db.DB),
			catalog: catalog, service: service, tenant: repository.TenantScope{ID: "tenant-a"},
		},
		static: static,
	}
	setup.workflowID = setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Counts its runs",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "code", Name: "Count", Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1), Parameters: map[string]any{
				"jsCode": strings.Join([]string{
					"const global = $getWorkflowStaticData('global')",
					"global.runs = (global.runs || 0) + 1",
					"$getWorkflowStaticData('node').lastMode = $execution.mode",
					"if ($input.first().json.fail) throw new Error('asked to fail')",
					"return [{ json: { runs: global.runs } }]",
				}, "\n"),
			}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "start", Port: "main"},
			Target: workflow.Endpoint{NodeID: "code", Port: "main"},
		}},
		Settings: map[string]any{},
	})
	stored, err := setup.workflows.Get(ctx, setup.tenant, setup.workflowID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	setup.versionID = stored.ActiveVersion.ID
	return setup
}

// run queues one execution with the given trigger and input, runs it, and
// returns the count the Code node saw (0 when it failed) and its status.
func (setup staticSetup) run(t *testing.T, trigger execution.Trigger, input string) (float64, execution.Status) {
	t.Helper()
	ctx := context.Background()
	var queued execution.Record
	var err error
	if trigger == execution.TriggerManual {
		queued, err = setup.executions.QueueManualLatest(ctx, setup.tenant, setup.workflowID, setup.catalog, "", json.RawMessage(input))
	} else {
		queued, err = setup.executions.QueueTriggered(ctx, setup.tenant, setup.workflowID, setup.versionID, trigger, "", json.RawMessage(input))
	}
	if err != nil {
		t.Fatalf("queue a %s run: %v", trigger, err)
	}
	if worked, err := setup.service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v)", worked, err)
	}
	record, err := setup.executions.Get(ctx, setup.tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var output map[string][][]workflow.Item
	_ = json.Unmarshal(record.Output, &output)
	for _, ports := range output {
		if len(ports) > 0 && len(ports[0]) > 0 {
			runs, _ := ports[0][0].JSON["runs"].(float64)
			return runs, record.Status
		}
	}
	return 0, record.Status
}

func (setup staticSetup) stored(t *testing.T) string {
	t.Helper()
	document, err := setup.static.LoadStaticData(context.Background(), setup.tenant, setup.workflowID)
	if err != nil {
		t.Fatalf("LoadStaticData() error = %v", err)
	}
	return string(document)
}

// Static data outlives a production run, and only a production run: a
// manual run and a failed one read it and change it for themselves, and
// leave what is stored as it was, as n8n documents for test runs.
func TestStaticDataIsSavedOnlyAfterASuccessfulProductionRun(t *testing.T) {
	setup := newStaticSetup(t)
	for want := 1.0; want <= 2; want++ {
		if runs, status := setup.run(t, execution.TriggerSchedule, `{}`); runs != want || status != execution.StatusSucceeded {
			t.Fatalf("schedule run %v saw runs = %v (%s)", want, runs, status)
		}
	}
	if got := setup.stored(t); got != `{"global":{"runs":2},"node:Count":{"lastMode":"schedule"}}` {
		t.Fatalf("stored after two scheduled runs = %s", got)
	}

	if runs, status := setup.run(t, execution.TriggerManual, `{}`); runs != 3 || status != execution.StatusSucceeded {
		t.Fatalf("the manual run saw runs = %v (%s), want 3", runs, status)
	}
	if _, status := setup.run(t, execution.TriggerWebhook, `{"fail":true}`); status != execution.StatusFailed {
		t.Fatalf("the failing run ended %s", status)
	}
	if got := setup.stored(t); got != `{"global":{"runs":2},"node:Count":{"lastMode":"schedule"}}` {
		t.Fatalf("stored after a manual and a failed run = %s, want it unchanged", got)
	}
	if runs, _ := setup.run(t, execution.TriggerWebhook, `{}`); runs != 3 {
		t.Fatalf("the next production run saw runs = %v, want 3", runs)
	}
	if got := setup.stored(t); got != `{"global":{"runs":3},"node:Count":{"lastMode":"webhook"}}` {
		t.Fatalf("stored after the webhook run = %s", got)
	}
}
