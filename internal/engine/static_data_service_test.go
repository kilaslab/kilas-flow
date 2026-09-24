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
// workflow static data, with one active workflow built from Code nodes and,
// where it asks for one, a node that waits for an approval.
type staticSetup struct {
	composition
	static     *repository.GORMStaticDataStore
	workflowID string
	versionID  string
}

// holdType is a node that parks its run until an approval resumes it, and
// blockType one that runs until its run is cancelled.
const (
	holdType  = "test.staticHold"
	blockType = "test.staticBlock"
)

// blocked is closed when a blockType node starts running.
var blocked = make(chan struct{}, 1)

func newStaticSetup(t *testing.T, steps ...workflow.Node) staticSetup {
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
	catalog := testCatalog(t, stepType(holdType, "Hold"), stepType(blockType, "Block"))
	executors := withExecutors(t, map[string]engine.ExecutorFunc{
		holdType: func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, &engine.SuspendError{Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(time.Hour)}
		},
		blockType: func(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			blocked <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	executionStore := repository.NewExecutionStore(db.DB)
	static := repository.NewStaticDataStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog, StaticData: static,
		Runner: engine.NewRunner(executors), WorkerID: "test-worker",
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
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Keeps static data",
		Nodes:    append([]workflow.Node{{ID: "start", Name: "Start", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}}, steps...),
		Settings: map[string]any{},
	}
	for index := range steps {
		document.Connections = append(document.Connections, workflow.Connection{
			ID: document.Nodes[index].ID + "-" + steps[index].ID, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: document.Nodes[index].ID, Port: "main"},
			Target: workflow.Endpoint{NodeID: steps[index].ID, Port: "main"},
		})
	}
	setup.workflowID = setup.activate(t, setup.tenant, document)
	stored, err := setup.workflows.Get(ctx, setup.tenant, setup.workflowID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	setup.versionID = stored.ActiveVersion.ID
	return setup
}

func codeStep(id, name string, lines ...string) workflow.Node {
	return workflow.Node{ID: id, Name: name, Type: nodes.JSCodeNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"jsCode": strings.Join(lines, "\n")}}
}

// counting is a workflow whose first Code node counts its runs in the static
// data, and whose second fails when the input asks it to, after the first
// has changed the data.
func counting(t *testing.T) staticSetup {
	return newStaticSetup(t,
		codeStep("count", "Count",
			"const global = $getWorkflowStaticData('global')",
			"global.runs = (global.runs || 0) + 1",
			"$getWorkflowStaticData('node').lastMode = $execution.mode",
			"return [{ json: { runs: global.runs, fail: $input.first().json.fail } }]"),
		codeStep("check", "Check",
			"if ($input.first().json.fail) throw new Error('asked to fail')",
			"return $input.all()"),
	)
}

// queue queues one execution with the given trigger and input.
func (setup staticSetup) queue(t *testing.T, trigger execution.Trigger, input string) execution.Record {
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
	return queued
}

// work runs the queued execution and returns it as it settled.
func (setup staticSetup) work(t *testing.T, queued execution.Record) execution.Record {
	t.Helper()
	ctx := context.Background()
	if worked, err := setup.service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v)", worked, err)
	}
	record, err := setup.executions.Get(ctx, setup.tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return record
}

// run queues and runs one execution, returning the count the Count node saw
// (0 when the run did not succeed) and how it ended.
func (setup staticSetup) run(t *testing.T, trigger execution.Trigger, input string) (float64, execution.Status) {
	t.Helper()
	record := setup.work(t, setup.queue(t, trigger, input))
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

// Static data outlives every production run that changed it, as in n8n:
// one that succeeds and one that fails after a node changed it. A manual run
// from the editor reads it and changes it for itself, and leaves what is
// stored as it was.
func TestStaticDataIsSavedAfterEveryProductionRunButNotAManualOne(t *testing.T) {
	setup := counting(t)
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
	if got := setup.stored(t); got != `{"global":{"runs":2},"node:Count":{"lastMode":"schedule"}}` {
		t.Fatalf("stored after a manual run = %s, want it unchanged", got)
	}
	if _, status := setup.run(t, execution.TriggerWebhook, `{"fail":true}`); status != execution.StatusFailed {
		t.Fatalf("the failing run ended %s", status)
	}
	if got := setup.stored(t); got != `{"global":{"runs":3},"node:Count":{"lastMode":"webhook"}}` {
		t.Fatalf("stored after a failed webhook run = %s, want what it changed before it failed", got)
	}
	if runs, _ := setup.run(t, execution.TriggerWebhook, `{}`); runs != 4 {
		t.Fatalf("the next production run saw runs = %v, want 4", runs)
	}
}

// A run cancelled while it runs keeps nothing, even what a node changed
// before the cancellation.
func TestACancelledRunKeepsNoStaticData(t *testing.T) {
	setup := newStaticSetup(t,
		codeStep("count", "Count", "$getWorkflowStaticData('global').runs = 1", "return $input.all()"),
		workflow.Node{ID: "block", Name: "Block", Type: blockType, TypeVersion: workflow.V(1)},
	)
	queued := setup.queue(t, execution.TriggerWebhook, `{}`)
	go func() {
		<-blocked
		_, _ = setup.service.Cancel(context.Background(), setup.tenant, queued.ID)
	}()
	if record := setup.work(t, queued); record.Status != execution.StatusCancelled {
		t.Fatalf("the run ended %s, want cancelled", record.Status)
	}
	if got := setup.stored(t); got != "" {
		t.Fatalf("stored after a cancelled run = %s, want nothing", got)
	}
}

// The half of a run before a Wait keeps what it changed, and the half that
// resumes reads it: the resumed run loads the stored data afresh, not an
// empty object.
func TestStaticDataChangedBeforeAWaitIsSavedAndSeenAfterIt(t *testing.T) {
	setup := newStaticSetup(t,
		codeStep("before", "Before", "$getWorkflowStaticData('global').before = true", "return $input.all()"),
		workflow.Node{ID: "hold", Name: "Hold", Type: holdType, TypeVersion: workflow.V(1)},
		codeStep("after", "After",
			"const global = $getWorkflowStaticData('global')",
			"const sawBefore = global.before === true",
			"global.after = true",
			"return [{ json: { sawBefore } }]"),
	)
	ctx := context.Background()
	queued := setup.queue(t, execution.TriggerWebhook, `{}`)
	if record := setup.work(t, queued); record.Status != execution.StatusWaiting {
		t.Fatalf("the run ended %s, want waiting", record.Status)
	}
	if got := setup.stored(t); got != `{"global":{"before":true}}` {
		t.Fatalf("stored while waiting = %s, want what the first half changed", got)
	}
	wait, err := setup.executions.FindActiveWait(ctx, setup.tenant, queued.ID)
	if err != nil {
		t.Fatalf("FindActiveWait() error = %v", err)
	}
	if _, _, err := setup.service.ResumeApproval(ctx, setup.tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC()}, false); err != nil {
		t.Fatalf("ResumeApproval() error = %v", err)
	}
	record := setup.work(t, queued)
	if record.Status != execution.StatusSucceeded || !strings.Contains(string(record.Output), `"sawBefore":true`) {
		t.Fatalf("the resumed run ended %s with %s, want it to see the first half's data", record.Status, record.Output)
	}
	if got := setup.stored(t); got != `{"global":{"before":true,"after":true}}` {
		t.Fatalf("stored after the resumed run = %s", got)
	}
}
