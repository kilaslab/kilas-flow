package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	return newCompositionWith(t, nil)
}

// newCompositionWith is newComposition with test steps beside the product's
// nodes: each one a node type with one item input and one item output, run by
// the executor given for it.
func newCompositionWith(t *testing.T, steps map[string]engine.ExecutorFunc) composition {
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
	definitions := make([]node.Definition, 0, len(steps))
	for typeID := range steps {
		definitions = append(definitions, stepType(typeID, typeID))
	}
	catalog := testCatalog(t, definitions...)
	executors := withExecutors(t, steps)
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

// keptOnly is an IF condition passing the items marked keep=yes. A child that
// ends in it returns its items in another order than it made them: the kept
// ones from the true port first, then the rest from the false port.
var keptOnly = []any{map[string]any{"field": "keep", "operator": "equals", "value": "yes"}}

// lineageComposition is a composition with the steps the sub-workflow lineage
// tests are built from. Fan makes x0, x1 and x2 from each item, the first one
// not kept, and copies the item's p onto all three. Parent fan makes p0, p1 and
// p2. Copy rebuilds each item and leaves its lineage to the runner. Probe
// tallies what probe reads for each item it is sent, against label of the item.
func lineageComposition(t *testing.T, probe string, label func(workflow.Item) string, tally *pairingTally) composition {
	t.Helper()
	return newCompositionWith(t, map[string]engine.ExecutorFunc{
		"test.fanOut": func(_ context.Context, _ workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
			var out []workflow.Item
			for _, item := range input["main"] {
				for index, keep := range []string{"no", "yes", "yes"} {
					out = append(out, workflow.Item{JSON: map[string]any{"u": fmt.Sprintf("x%d", index), "keep": keep, "p": item.JSON["p"]}})
				}
			}
			return workflow.NodeOutput{out}, nil
		},
		"test.parentFan": func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return workflow.NodeOutput{{{JSON: map[string]any{"p": "p0"}}, {JSON: map[string]any{"p": "p1"}}, {JSON: map[string]any{"p": "p2"}}}}, nil
		},
		"test.copy":  copyWithoutLineage,
		"test.probe": tallyProbe(probe, label, tally),
	})
}

// TestDollarItemAfterASubWorkflowReadsTheCallNotAChildNodeOfTheSameID covers a
// child node that shares its ID with the node calling it.
//
// Node IDs are unique within a workflow, not across workflows: Duplicate keeps
// every one, the n8n importer falls back to `n8n-<index>`, and a document
// written through the API or the CLI uses readable ones. The child's Fan made
// three items, so the child's runner stamped them lost under "call" with each
// one's index, and its IF then handed them back in another order. Returned with
// those stamps, the caller took them for its own Execute Workflow node's, and
// `$('Call').item` read the call's output by the child's indices: x1 read x2,
// x2 read x0 and x0 read x1. A child's lineage names the nodes of the run that
// wrote it, so it ends at the call, and the caller's runner stamps the call's
// own count change: each item then pairs with itself.
func TestDollarItemAfterASubWorkflowReadsTheCallNotAChildNodeOfTheSameID(t *testing.T) {
	var tally pairingTally
	setup := lineageComposition(t, "{{ $('Call').item.json.u }}",
		func(item workflow.Item) string { return fmt.Sprint(item.JSON["u"]) }, &tally)
	child := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Child",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Called", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": keptOnly}},
		},
		Connections: []workflow.Connection{mainEdge("c1", "start", "main", "call"), mainEdge("c2", "call", "main", "if")},
		Settings:    map[string]any{},
	})
	parent := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Parent",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Call", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"workflowId": child}},
			{ID: "after", Name: "After", Type: "test.copy", TypeVersion: workflow.V(1)},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "call"),
			mainEdge("c2", "call", "main", "after"),
			mainEdge("c3", "after", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	record := setup.runManual(t, parent, `{}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}
	if len(tally.wrong) > 0 {
		t.Errorf("$('Call').item read the child's lineage: %s", strings.Join(tally.wrong, "; "))
	}
	if got, want := strings.Join(tally.paired, ","), "x1,x2,x0"; got != want || tally.refused != 0 {
		t.Errorf("$('Call').item paired [%s] and refused %d, want [%s] and none refused: the call made each of them", got, tally.refused, want)
	}
}

// TestDollarItemRefusesAChildsLineageUnderAParentNodesID covers a child that
// hands back exact pointers rather than lost stamps.
//
// The child's Copy turned Fan's lost stamps into pointers at Fan's items, and
// the caller has a count-changing node of its own under the same ID. Returned
// with those pointers, every call's items named "fan", so `$('Parent fan').item`
// read the caller's Parent fan by the child's indices, whichever item had made
// the call: six of the nine read another call's item. The Execute Workflow node
// made nine items from three, and which call made which is not recorded on
// them, so the only right answer is a refusal.
func TestDollarItemRefusesAChildsLineageUnderAParentNodesID(t *testing.T) {
	var tally pairingTally
	setup := lineageComposition(t, "{{ $('Parent fan').item.json.p }}",
		func(item workflow.Item) string { return fmt.Sprint(item.JSON["p"]) }, &tally)
	child := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Child",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Called", Type: nodes.ExecuteWorkflowTriggerType, TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Fan", Type: "test.fanOut", TypeVersion: workflow.V(1)},
			{ID: "copy", Name: "Copy", Type: "test.copy", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": keptOnly}},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "start", "main", "fan"),
			mainEdge("c2", "fan", "main", "copy"),
			mainEdge("c3", "copy", "main", "if"),
		},
		Settings: map[string]any{},
	})
	parent := setup.activate(t, setup.tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Parent",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "fan", Name: "Parent fan", Type: "test.parentFan", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Call", Type: nodes.ExecuteWorkflowNodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"workflowId": child, "itemsPerCall": "eachItem"}},
			{ID: "probe", Name: "Probe", Type: "test.probe", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			mainEdge("c1", "manual", "main", "fan"),
			mainEdge("c2", "fan", "main", "call"),
			mainEdge("c3", "call", "main", "probe"),
		},
		Settings: map[string]any{},
	})

	record := setup.runManual(t, parent, `{}`)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("parent status = %s, error = %s", record.Status, record.Error)
	}
	if len(tally.wrong) > 0 {
		t.Errorf("$('Parent fan').item read another call's item: %s", strings.Join(tally.wrong, "; "))
	}
	if len(tally.paired) != 0 || tally.refused != 9 {
		t.Errorf("$('Parent fan').item paired %v and refused %d, want all 9 refused", tally.paired, tally.refused)
	}
}
