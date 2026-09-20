package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// scopedSetType is the node type the scoped pack registers. Every version of a
// type shares one scope, so the tests name the type and never a version.
const scopedSetType = "pack.scoped.set"

// registerScopedSet adds one pack node that only tenant-a may see, reusing
// Set's real definition and executor so a run of it executes the same code a
// built-in does rather than a stub.
func registerScopedSet(t *testing.T, catalog *node.Registry) {
	t.Helper()
	definition, found := catalog.Get("kilasflow.set", workflow.V(1))
	if !found {
		t.Fatal("the Set node is not registered")
	}
	definition.Type = scopedSetType
	definition.VisibleTo = []string{"tenant-a"}
	if err := catalog.RegisterFrom(node.SourcePack, definition); err != nil {
		t.Fatalf("RegisterFrom(%q) error = %v", scopedSetType, err)
	}
}

// scopedWorkflow is a manual workflow whose one step is nodeType.
func scopedWorkflow(name, nodeType string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: name,
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Stamp", Type: nodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"stamp": "scoped"}}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// runOnce drives one claim synchronously and returns the settled record.
func runOnce(t *testing.T, setup composition, tenant repository.TenantScope, executionID string) execution.Record {
	t.Helper()
	ctx := context.Background()
	if worked, err := setup.service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the queued execution claimed", worked, err)
	}
	record, err := setup.executions.Get(ctx, tenant, executionID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", executionID, err)
	}
	return record
}

// issueCode returns the compile diagnostic raised against one node, failing
// the test when the error is not a validation error naming that node.
func issueCode(t *testing.T, err error, nodeID string) workflow.ErrorCode {
	t.Helper()
	var validation *workflow.ValidationErrors
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v (%T), want *workflow.ValidationErrors", err, err)
	}
	for _, issue := range validation.Issues {
		if issue.NodeID == nodeID {
			return issue.Code
		}
	}
	t.Fatalf("issues = %#v, none is about node %q", validation.Issues, nodeID)
	return ""
}

// The same document has to compile for the tenant the pack was given and be
// refused for everyone else — at both gates that compile a document a user
// asked for: activation and the manual run that follows it.
func TestAScopedNodeRunsForItsTenantAndIsRefusedForAnother(t *testing.T) {
	setup := newComposition(t)
	registerScopedSet(t, setup.catalog)
	ctx := context.Background()

	allowed := repository.TenantScope{ID: "tenant-a"}
	view := workflow.CatalogFor(setup.catalog, allowed.ID)
	stored, err := setup.workflows.SaveDraft(ctx, allowed, scopedWorkflow("Scoped", scopedSetType))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := setup.workflows.Activate(ctx, allowed, stored.ID, view); err != nil {
		t.Fatalf("Activate() error = %v, want the scoped node to compile for tenant-a", err)
	}
	queued, err := setup.executions.QueueManualLatest(ctx, allowed, stored.ID, view, "", json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	record := runOnce(t, setup, allowed, queued.ID)
	if record.Status != execution.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded (error %s)", record.Status, record.Error)
	}

	refused := repository.TenantScope{ID: "tenant-b"}
	refusedView := workflow.CatalogFor(setup.catalog, refused.ID)
	// Drafts are never compiled, so a tenant can hold and edit a document that
	// names a node it may not run. The gates are activation and the run.
	copied, err := setup.workflows.SaveDraft(ctx, refused, scopedWorkflow("Copied", scopedSetType))
	if err != nil {
		t.Fatalf("SaveDraft(copied) error = %v, want a draft to accept an invisible type", err)
	}
	if _, err := setup.workflows.Activate(ctx, refused, copied.ID, refusedView); issueCode(t, err, "set") != workflow.ErrorNodeNotAvailable {
		t.Fatalf("Activate() error = %v, want node.not_available", err)
	}
	if _, err := setup.executions.QueueManualLatest(ctx, refused, copied.ID, refusedView, "", json.RawMessage(`{}`)); issueCode(t, err, "set") != workflow.ErrorNodeNotAvailable {
		t.Fatalf("QueueManualLatest() error = %v, want node.not_available", err)
	}

	// A type that is not registered at all keeps its own, different
	// diagnostic: a typo is the author's to fix, a scope is not.
	misspelt, err := setup.workflows.SaveDraft(ctx, refused, scopedWorkflow("Misspelt", scopedSetType+"t"))
	if err != nil {
		t.Fatalf("SaveDraft(misspelt) error = %v", err)
	}
	if _, err := setup.workflows.Activate(ctx, refused, misspelt.ID, refusedView); issueCode(t, err, "set") != workflow.ErrorUnknownNode {
		t.Fatalf("Activate(misspelt) error = %v, want node.unknown_type", err)
	}
}

// The worker is the authoritative gate: a record that reached the queue
// without passing a scoped compile — a bypass, or work queued before the
// operator narrowed the scope — must fail at claim rather than execute.
func TestTheWorkerRefusesAScopedNodeEvenIfItWasQueuedPastTheAPI(t *testing.T) {
	setup := newComposition(t)
	registerScopedSet(t, setup.catalog)
	ctx := context.Background()

	refused := repository.TenantScope{ID: "tenant-b"}
	stored, err := setup.workflows.SaveDraft(ctx, refused, scopedWorkflow("Bypassed", scopedSetType))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// Queued against the raw registry on purpose.
	if _, err := setup.workflows.Activate(ctx, refused, stored.ID, setup.catalog); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	queued, err := setup.executions.QueueManualLatest(ctx, refused, stored.ID, setup.catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	record := runOnce(t, setup, refused, queued.ID)
	if record.Status != execution.StatusFailed {
		t.Fatalf("status = %q, want failed", record.Status)
	}
	if !strings.Contains(string(record.Error), "not available to this workspace") {
		t.Errorf("error = %s, want it to say the node is not available", record.Error)
	}
	if len(record.NodeRuns) != 0 {
		t.Fatalf("node runs = %d, want nothing executed", len(record.NodeRuns))
	}
}

// A webhook and a schedule are queued by the trigger surface, never by a
// request holding a document, so the worker's compile is the only gate they
// pass through.
func TestTriggeredRunsOfAScopedAwayNodeFail(t *testing.T) {
	setup := newComposition(t)
	registerScopedSet(t, setup.catalog)
	ctx := context.Background()

	refused := repository.TenantScope{ID: "tenant-b"}
	stored, err := setup.workflows.SaveDraft(ctx, refused, scopedWorkflow("Triggered", scopedSetType))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// Activated before the operator narrowed the scope: the active revision
	// still holds the type.
	activated, err := setup.workflows.Activate(ctx, refused, stored.ID, setup.catalog)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activated.ActiveVersion == nil {
		t.Fatal("activation pinned no version")
	}
	versionID := activated.ActiveVersion.ID

	scheduled, err := setup.service.QueueScheduled(ctx, refused.ID, stored.ID, versionID, "manual", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueScheduled() error = %v", err)
	}
	webhooked, err := setup.service.QueueWebhook(ctx, repository.WebhookBinding{
		TenantID: refused.ID, WorkflowID: stored.ID, WorkflowVersionID: versionID, NodeID: "manual",
	}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueWebhook() error = %v", err)
	}

	for _, queued := range []execution.Record{scheduled, webhooked} {
		record := runOnce(t, setup, refused, queued.ID)
		if record.Status != execution.StatusFailed {
			t.Fatalf("%s status = %q, want failed", record.Trigger, record.Status)
		}
		if !strings.Contains(string(record.Error), "not available to this workspace") {
			t.Errorf("%s error = %s, want it to say the node is not available", record.Trigger, record.Error)
		}
		if len(record.NodeRuns) != 0 {
			t.Fatalf("%s node runs = %d, want nothing executed", record.Trigger, len(record.NodeRuns))
		}
	}
}

// A sub-workflow runs in the caller's tenant, so copying a parent that calls a
// workflow holding a scoped node must not let the child's document run: the
// child is compiled under the tenant that called it.
func TestASubworkflowCannotSmuggleAScopedNode(t *testing.T) {
	setup := newComposition(t)
	registerScopedSet(t, setup.catalog)
	setup.tenant = repository.TenantScope{ID: "tenant-b"}
	ctx := context.Background()

	// The child holds the scoped node and was activated against the raw
	// catalogue, as it would have been before the pack was scoped.
	child, err := setup.workflows.SaveDraft(ctx, setup.tenant, scopedWorkflow("Child", scopedSetType))
	if err != nil {
		t.Fatalf("SaveDraft(child) error = %v", err)
	}
	if _, err := setup.workflows.Activate(ctx, setup.tenant, child.ID, setup.catalog); err != nil {
		t.Fatalf("Activate(child) error = %v", err)
	}
	parent := setup.activate(t, setup.tenant, caller("Parent", child.ID, nil))

	record := setup.runManual(t, parent, `{"customer":"Ada"}`)
	if record.Status != execution.StatusFailed {
		t.Fatalf("parent status = %q, want failed", record.Status)
	}
	// Named with the workflow, because "node X failed" inside a sub-workflow
	// reads as a failure of the caller otherwise.
	for _, want := range []string{child.ID, "not available to this workspace"} {
		if !strings.Contains(string(record.Error), want) {
			t.Errorf("parent error = %s, want it to name %q", record.Error, want)
		}
	}

	page, err := setup.executions.List(ctx, setup.tenant, repository.ExecutionFilter{WorkflowID: child.ID})
	if err != nil {
		t.Fatalf("List(child executions) error = %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("child executions = %d, want one", len(page.Records))
	}
	if page.Records[0].Status != execution.StatusFailed {
		t.Errorf("child status = %q, want failed", page.Records[0].Status)
	}
	if len(page.Records[0].NodeRuns) != 0 {
		t.Errorf("child node runs = %d, want nothing executed", len(page.Records[0].NodeRuns))
	}
}

// waitTestWorkflowReaching is waitTestWorkflow with the final node's type
// chosen by the test, so the node can be scoped away between the suspension
// and the resume.
func waitTestWorkflowReaching(lastType string) workflow.Document {
	link := func(id, source, target string) workflow.Connection {
		return workflow.Connection{
			ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: "main"},
			Target: workflow.Endpoint{NodeID: target, Port: "main"},
		}
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_resume_scoped", Name: "Resume under a narrowed scope",
		Nodes: []workflow.Node{
			{ID: "start", Name: "Start", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "mark", Name: "Mark", Type: "test.mark", TypeVersion: workflow.V(1)},
			{ID: "hold", Name: "Hold", Type: "test.hold", TypeVersion: workflow.V(1)},
			{ID: "done", Name: "Done", Type: lastType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			link("start-mark", "start", "mark"),
			link("mark-hold", "mark", "hold"),
			link("hold-done", "hold", "done"),
		},
		Settings: map[string]any{},
	}
}

// A run waiting on approval is re-queued and recompiled by the worker that
// picks it up, so narrowing a scope while a run waits must stop it rather than
// let it continue on the strength of a compile that happened before the
// change.
func TestAResumedRunIsRecompiledUnderItsTenantsScope(t *testing.T) {
	ctx, db, store, catalog, executors, tenant := waitTestSetup(t)
	_ = db
	hold := &waitSuspender{mode: engine.WaitModeApproval, expiresAt: time.Now().Add(time.Hour)}
	var calls int
	var requests []engine.Request
	hold.calls = &calls
	hold.requests = &requests
	var passCalls int
	var passInputs []workflow.NodeInput
	waitRegisterFakes(t, executors, hold, &passCalls, &passInputs)

	// Unscoped at first: the run that suspends compiles it like any other node.
	if err := catalog.RegisterFrom(node.SourcePack, waitTestDefinition("pack.test.done", "test.passthrough")); err != nil {
		t.Fatalf("RegisterFrom(pack.test.done) error = %v", err)
	}

	workflowStore := repository.NewWorkflowStore(db.DB)
	saved, err := workflowStore.SaveDraft(ctx, tenant, waitTestWorkflowReaching("pack.test.done"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, catalog, "", json.RawMessage(`{"customer":"Ada"}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}

	service := waitTestService(t, store, catalog, executors, "worker-1")
	if worked, err := service.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(suspend) = (%v, %v), want (true, nil)", worked, err)
	}
	suspended, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(suspended) error = %v", err)
	}
	if suspended.Status != execution.StatusWaiting {
		t.Fatalf("status = %q, want waiting", suspended.Status)
	}
	completedBeforeScope := passCalls

	// No worker goroutine is running: these tests drive RunOnce synchronously.
	if err := catalog.ScopeTo("pack.test.done", []string{"someone-else"}); err != nil {
		t.Fatalf("ScopeTo() error = %v", err)
	}

	wait, err := store.FindActiveWait(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("FindActiveWait() error = %v", err)
	}
	if _, _, err := service.ResumeApproval(ctx, tenant.ID, wait.ResumeToken,
		engine.ApprovalDecision{Approved: true, DecidedBy: "tester", RespondedAt: time.Now().UTC()}, false); err != nil {
		t.Fatalf("ResumeApproval() error = %v", err)
	}

	restarted := waitTestService(t, store, catalog, executors, "worker-2")
	if worked, err := restarted.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("RunOnce(resume) = (%v, %v), want (true, nil)", worked, err)
	}
	finished, err := store.Get(ctx, tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get(finished) error = %v", err)
	}
	if finished.Status != execution.StatusFailed {
		t.Fatalf("status = %q, want failed (error %s)", finished.Status, finished.Error)
	}
	if !strings.Contains(string(finished.Error), "not available to this workspace") {
		t.Errorf("error = %s, want it to say the node is not available", finished.Error)
	}
	// The pre-suspension rows stay; what must not happen is 'done' executing.
	if passCalls != completedBeforeScope {
		t.Fatalf("passthrough calls = %d, want the %d from before the suspension", passCalls, completedBeforeScope)
	}
	for _, run := range finished.NodeRuns {
		if run.NodeID == "done" {
			t.Fatalf("the scoped node executed after being scoped away: %#v", run)
		}
	}
}
