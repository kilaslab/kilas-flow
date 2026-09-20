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

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// composition is everything a sub-workflow test needs: a migrated database, the
// two stores, and a service wired the way the composition root wires it.
type composition struct {
	executions *repository.GORMExecutionStore
	workflows  *repository.GORMWorkflowStore
	catalog    *node.Registry
	service    *engine.Service
	tenant     repository.TenantScope
}

func newComposition(t *testing.T) composition {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "composition.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executionStore := repository.NewExecutionStore(db.DB)
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: executionStore, Catalog: catalog,
		Runner: engine.NewRunner(executors), WorkerID: "test-worker",
		DefaultTimeout:         30 * time.Second,
		SubworkflowTriggerType: nodes.ExecuteWorkflowTriggerType,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return composition{
		executions: executionStore, workflows: repository.NewWorkflowStore(db.DB),
		catalog: catalog, service: service, tenant: repository.TenantScope{ID: "tenant-a"},
	}
}

// activate saves and activates one workflow, returning its ID.
func (setup composition) activate(t *testing.T, tenant repository.TenantScope, document workflow.Document) string {
	t.Helper()
	stored, err := setup.workflows.SaveDraft(context.Background(), tenant, document)
	if err != nil {
		t.Fatalf("SaveDraft(%q) error = %v", document.Name, err)
	}
	if _, err := setup.workflows.Activate(context.Background(), tenant, stored.ID, setup.catalog); err != nil {
		t.Fatalf("Activate(%q) error = %v", document.Name, err)
	}
	return stored.ID
}

// subworkflow is a callable workflow that stamps a field on every item.
func subworkflow(name, stamp string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: name,
		Nodes: []workflow.Node{
			{ID: "start", Name: "Called", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Stamp", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"stamp": stamp}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "start", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// caller is a manual workflow whose one step calls another workflow.
func caller(name, targetID string, parameters map[string]any) workflow.Document {
	call := map[string]any{"workflowId": targetID}
	for key, value := range parameters {
		call[key] = value
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: name,
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Call", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1), Parameters: call},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "call", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// runManual queues and runs one workflow, returning its finished record.
func (setup composition) runManual(t *testing.T, workflowID string, input string) execution.Record {
	t.Helper()
	ctx := context.Background()
	queued, err := setup.executions.QueueManualLatest(ctx, setup.tenant, workflowID, setup.catalog, "", json.RawMessage(input))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := setup.service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the queued execution claimed", worked, err)
	}
	record, err := setup.executions.Get(ctx, setup.tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	return record
}

func TestASubWorkflowRunsAndReturnsItsItems(t *testing.T) {
	setup := newComposition(t)
	child := setup.activate(t, setup.tenant, subworkflow("Child", "stamped"))
	parent := setup.activate(t, setup.tenant, caller("Parent", child, nil))

	record := setup.runManual(t, parent, `{"customer":"Ada"}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}

	// The call's own output is the sub-workflow's items, so the branch
	// continues with what the child produced.
	var outputs map[string][][]workflow.Item
	if err := json.Unmarshal(record.Output, &outputs); err != nil {
		t.Fatalf("decode parent output: %v", err)
	}
	items := outputs["call"]
	if len(items) != 1 || len(items[0]) != 1 || items[0][0].JSON["stamp"] != "stamped" {
		t.Fatalf("parent output = %#v, want the sub-workflow's stamped item", outputs)
	}

	// And the child has a record of its own, with the parent on it.
	page, err := setup.executions.List(context.Background(), setup.tenant, repository.ExecutionFilter{WorkflowID: child})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("child executions = %d, want one", len(page.Records))
	}
	if page.Records[0].ParentExecutionID != record.ID {
		t.Errorf("child parent = %q, want the calling execution %q", page.Records[0].ParentExecutionID, record.ID)
	}
	if page.Records[0].Trigger != execution.TriggerSubworkflow {
		t.Errorf("child trigger = %q, want %q", page.Records[0].Trigger, execution.TriggerSubworkflow)
	}
	if page.Records[0].Status != execution.StatusSucceeded {
		t.Errorf("child status = %q, want succeeded", page.Records[0].Status)
	}

	// The chain is walkable from the child, which is what an execution view
	// needs to show where a run came from.
	ancestors, err := setup.executions.Ancestry(context.Background(), setup.tenant, page.Records[0].ID)
	if err != nil {
		t.Fatalf("Ancestry() error = %v", err)
	}
	if len(ancestors) != 1 || ancestors[0].ID != record.ID {
		t.Errorf("ancestry = %#v, want the parent execution", ancestors)
	}
}

func TestFireAndForgetRunsTheChildAndPassesItsInputThrough(t *testing.T) {
	setup := newComposition(t)
	child := setup.activate(t, setup.tenant, subworkflow("Child", "stamped"))
	parent := setup.activate(t, setup.tenant, caller("Parent", child, map[string]any{"mode": "fireAndForget"}))

	record := setup.runManual(t, parent, `{"customer":"Ada"}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}
	var outputs map[string][][]workflow.Item
	_ = json.Unmarshal(record.Output, &outputs)
	items := outputs["call"]
	// The incoming items pass through, so the branch continues with what it
	// had rather than with nothing.
	if len(items) != 1 || len(items[0]) != 1 || items[0][0].JSON["stamp"] != nil {
		t.Fatalf("parent output = %#v, want the incoming item unchanged", outputs)
	}
	if items[0][0].JSON["customer"] != "Ada" {
		t.Errorf("parent output = %#v, want the incoming item", items[0][0].JSON)
	}

	// It still ran to completion. "Fire and forget" is about the output, not
	// about whether the work happened.
	page, _ := setup.executions.List(context.Background(), setup.tenant, repository.ExecutionFilter{WorkflowID: child})
	if len(page.Records) != 1 || page.Records[0].Status != execution.StatusSucceeded {
		t.Fatalf("child executions = %#v, want one succeeded run", page.Records)
	}
}

func TestASelfCallingWorkflowIsRefusedRatherThanSurvived(t *testing.T) {
	setup := newComposition(t)

	// A workflow that calls itself. Without the call stack this recurses until
	// something else stops it — and with an inline runtime, "something else" is
	// the goroutine stack or the worker pool, neither of which produces an
	// error anyone can read.
	stored, err := setup.workflows.SaveDraft(context.Background(), setup.tenant,
		caller("Ouroboros", "placeholder", nil))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	document := caller("Ouroboros", stored.ID, nil)
	document.ID = stored.ID
	if _, err := setup.workflows.SaveDraft(context.Background(), setup.tenant, document); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := setup.workflows.Activate(context.Background(), setup.tenant, stored.ID, setup.catalog); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	record := setup.runManual(t, stored.ID, `{}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %s, want a failure rather than a hang", record.Status)
	}
	// The cycle is named, so the author can see which call to remove.
	if !strings.Contains(string(record.Error), "would repeat workflow") {
		t.Errorf("error = %s, want the cycle named", record.Error)
	}
	// No child ran at all. The stack already contains this workflow when the
	// call is made, so the very first recursion is refused — nothing was
	// started and then abandoned, and the worker pool was never at risk.
	page, _ := setup.executions.List(context.Background(), setup.tenant, repository.ExecutionFilter{WorkflowID: stored.ID})
	if len(page.Records) != 1 {
		t.Errorf("executions = %d, want only the run the user started", len(page.Records))
	}
}

func TestAMutualRecursionIsRefusedAtTheSecondVisit(t *testing.T) {
	setup := newComposition(t)

	// A → B → A. A depth counter alone would let this run all the way to the
	// limit and spend the whole budget before failing; the stack refuses the
	// second A immediately.
	first, err := setup.workflows.SaveDraft(context.Background(), setup.tenant, caller("A", "placeholder", nil))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	second := setup.activate(t, setup.tenant, caller("B", first.ID, nil))
	document := caller("A", second, nil)
	document.ID = first.ID
	if _, err := setup.workflows.SaveDraft(context.Background(), setup.tenant, document); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := setup.workflows.Activate(context.Background(), setup.tenant, first.ID, setup.catalog); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	record := setup.runManual(t, first.ID, `{}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %s, want a failure", record.Status)
	}
	if !strings.Contains(string(record.Error), "would repeat workflow") {
		t.Errorf("error = %s, want the cycle named", record.Error)
	}
}

func TestASubWorkflowCannotReachAnotherTenant(t *testing.T) {
	setup := newComposition(t)
	other := repository.TenantScope{ID: "tenant-b"}

	// The second tenant's workflow exists and is active. Naming its ID from the
	// first tenant must not reach it: the tenant clause is the boundary, so the
	// ID simply does not exist over here.
	foreign := setup.activate(t, other, subworkflow("Someone else's", "leaked"))
	parent := setup.activate(t, setup.tenant, caller("Parent", foreign, nil))

	record := setup.runManual(t, parent, `{}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %s, want a refusal", record.Status)
	}
	if strings.Contains(string(record.Error), "leaked") {
		t.Fatalf("the other tenant's workflow ran: %s", record.Error)
	}
	page, _ := setup.executions.List(context.Background(), other, repository.ExecutionFilter{WorkflowID: foreign})
	if len(page.Records) != 0 {
		t.Errorf("the other tenant's workflow ran %d times", len(page.Records))
	}
}

func TestAnInactiveSubWorkflowIsRefusedWithAReason(t *testing.T) {
	setup := newComposition(t)
	// A run is pinned to an immutable revision, and the active one is the only
	// revision this server treats as the one that runs.
	draft, err := setup.workflows.SaveDraft(context.Background(), setup.tenant, subworkflow("Never activated", "x"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	parent := setup.activate(t, setup.tenant, caller("Parent", draft.ID, nil))

	record := setup.runManual(t, parent, `{}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %s, want a refusal", record.Status)
	}
	if !strings.Contains(string(record.Error), "active workflow") {
		t.Errorf("error = %s, want the activation requirement named", record.Error)
	}
}
