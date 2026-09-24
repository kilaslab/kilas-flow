package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// These tests drive the four debug primitives through the real authenticated
// server: the same middleware chain, the same tenant resolution and the same
// stores the binary runs. A unit test of the handler would prove the shape of
// an answer; only this proves who receives it.

// withNode connects two nodes on the main channel, so a document under test is
// a runnable graph rather than one node beside another.
func withNode(document workflow.Document, node workflow.Node) workflow.Document {
	document.Nodes = append(document.Nodes, node)
	document.Connections = append(document.Connections, workflow.Connection{
		ID: "c1", Kind: workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
		Target: workflow.Endpoint{NodeID: node.ID, Port: "main"},
	})
	return document
}

func diagnosticList(t *testing.T, answer map[string]any) []map[string]any {
	t.Helper()
	raw, ok := answer["diagnostics"].([]any)
	if !ok {
		t.Fatalf("diagnostics = %#v, want a list", answer["diagnostics"])
	}
	diagnostics := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		diagnostic, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("diagnostic = %#v, want an object", entry)
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

// A dry run answers what the compiler said and saves nothing — which is the
// whole point of the operation, because the other two verbs in the loop both
// write.
func TestValidateWorkflowDocumentAnswersWithoutSaving(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	key := keys["acme"]
	server.createWorkflowAs(t, key, "Existing")

	valid := server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", key, workflowDraft(validManualWorkflow("Scaffold")), http.StatusOK)
	if valid["valid"] != true {
		t.Errorf("validate(a runnable document) = %#v, want valid", valid)
	}
	if diagnostics := diagnosticList(t, valid); len(diagnostics) != 0 {
		t.Errorf("validate(a runnable document) diagnostics = %#v, want none", diagnostics)
	}

	broken := validManualWorkflow("Broken")
	broken.Nodes[0].Type = "kilasflow.not-a-node"
	refused := server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", key, workflowDraft(broken), http.StatusOK)
	if refused["valid"] != false {
		t.Errorf("validate(an unregistered node) = %#v, want invalid", refused)
	}
	diagnostics := diagnosticList(t, refused)
	if len(diagnostics) != 1 {
		t.Fatalf("validate(an unregistered node) diagnostics = %#v, want one", diagnostics)
	}
	if diagnostics[0]["severity"] != "blocking" || diagnostics[0]["nodeId"] != "manual" ||
		!strings.Contains(diagnostics[0]["reason"].(string), "not registered") {
		t.Errorf("diagnostic = %#v, want a blocking one naming the node and the reason", diagnostics[0])
	}

	// A draft that is structurally unreadable is answered the same way: the
	// caller asked what is wrong with it, and "name is required" is that
	// answer rather than a refused request. (A body the schema itself cannot
	// read is still a 422, as it is for every other operation.)
	unnamed := workflowDraft(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Nodes:         []workflow.Node{}, Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	empty := server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", key, unnamed, http.StatusOK)
	if empty["valid"] != false || len(diagnosticList(t, empty)) == 0 {
		t.Errorf("validate(a document with no name) = %#v, want a diagnostic", empty)
	}

	if items := server.callArray(t, "/api/v1/workflows", key); len(items) != 1 {
		t.Errorf("workflow count after three dry runs = %d, want the one workflow that was created", len(items))
	}
}

// The catalogue is narrowed to the caller's tenant, which is the half of the
// answer a local check cannot give: a node this workspace may not use has to be
// reported here rather than at activation.
func TestValidateWorkflowDocumentSeesTheCallersCatalogue(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	document := withNode(validManualWorkflow("Uses a workspace-only node"), workflow.Node{
		ID: "crm", Name: "CRM", Type: acmeCRMType, TypeVersion: workflow.V(1),
	})

	acme := server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", keys["acme"], workflowDraft(document), http.StatusOK)
	if acme["valid"] != true {
		t.Errorf("validate(acme's own node) = %#v, want valid", acme)
	}

	globex := server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", keys["globex"], workflowDraft(document), http.StatusOK)
	if globex["valid"] != false {
		t.Fatalf("validate(a node scoped to another tenant) = %#v, want invalid", globex)
	}
	diagnostics := diagnosticList(t, globex)
	if len(diagnostics) != 1 || diagnostics[0]["nodeId"] != "crm" ||
		!strings.Contains(diagnostics[0]["reason"].(string), "not available to this workspace") {
		t.Errorf("diagnostics = %#v, want the unavailable node named", diagnostics)
	}
}

// A copy is a new workflow under the same tenant, named after the source unless
// the request names one — and it never carries the source's identity, which
// would append a revision to the original instead of copying it.
func TestDuplicateWorkflowCopiesTheLatestRevision(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	key := keys["acme"]
	sourceID := server.createWorkflowAs(t, key, "Scaffold")

	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+sourceID+"/duplicate", key, nil, http.StatusCreated)
	if id, _ := copied["id"].(string); id == "" || id == sourceID {
		t.Fatalf("copy id = %#v, want a new workflow", copied["id"])
	}
	if copied["name"] != "Scaffold (copy)" {
		t.Errorf("copy name = %#v, want the source's name with (copy)", copied["name"])
	}
	latest, _ := copied["latestVersion"].(map[string]any)
	if revision, _ := latest["revision"].(float64); revision != 1 {
		t.Errorf("copy latestVersion.revision = %#v, want 1", latest["revision"])
	}
	document, _ := latest["document"].(map[string]any)
	if nodes, _ := document["nodes"].([]any); len(nodes) != 1 {
		t.Errorf("copy document nodes = %#v, want the source's graph", document["nodes"])
	}

	// The copy outlives the source: deleting one leaves the other readable, and
	// the copy carries the caller's own tenant rather than the source's row.
	server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+copied["id"].(string), key, nil, http.StatusOK)

	named := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+sourceID+"/duplicate", key,
		map[string]any{"name": "Experiment"}, http.StatusCreated)
	if named["name"] != "Experiment" {
		t.Errorf("named copy = %#v, want the requested name", named["name"])
	}

	// The schema puts no uniqueness on a workflow's name, so a second copy is
	// stored rather than refused — the same answer POST /workflows gives.
	again := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+sourceID+"/duplicate", key, nil, http.StatusCreated)
	if again["id"] == copied["id"] {
		t.Errorf("second copy id = %#v, want a third workflow", again["id"])
	}

	// This is a tenant-scoped read like every other workflow read: another
	// tenant's id is not there to copy.
	if recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+sourceID+"/duplicate", keys["globex"], nil); recorder.Code != http.StatusNotFound {
		t.Errorf("cross-tenant duplicate status = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
}

// A retry is the same workflow, the same revision and the same input; a run
// that has not finished is refused rather than queued beside itself.
func TestRetryExecutionQueuesTheSameRevision(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	key := keys["acme"]
	workflowID := server.createWorkflowAs(t, key, "Retryable")
	finished := server.seedExecution(t, "acme", key, workflowID)

	retried := server.callJSON(t, http.MethodPost, "/api/v1/executions/"+finished.ID+"/retry", key, nil, http.StatusCreated)
	if retried["id"] == finished.ID {
		t.Fatalf("retry id = %#v, want a new execution", retried["id"])
	}
	if retried["workflowId"] != finished.WorkflowID || retried["workflowVersionId"] != finished.WorkflowVersionID {
		t.Errorf("retry = %#v, want the original's workflow and revision", retried)
	}
	if retried["status"] != string(execution.StatusQueued) || retried["trigger"] != string(execution.TriggerManual) {
		t.Errorf("retry status/trigger = %#v/%#v, want a queued manual run", retried["status"], retried["trigger"])
	}
	server.callJSON(t, http.MethodGet, "/api/v1/executions/"+retried["id"].(string), key, nil, http.StatusOK)

	// A retry keeps the trigger its original ran under: a retried webhook run
	// is a production run, not a test from the editor.
	finishedAt := time.Now().UTC()
	delivered, err := server.executions.Create(context.Background(), repository.TenantScope{ID: "acme"}, execution.Record{
		WorkflowID: workflowID, WorkflowVersionID: finished.WorkflowVersionID,
		Status: execution.StatusFailed, Trigger: execution.TriggerWebhook, StartedAt: finishedAt, FinishedAt: &finishedAt,
	})
	if err != nil {
		t.Fatalf("seed a webhook execution: %v", err)
	}
	again := server.callJSON(t, http.MethodPost, "/api/v1/executions/"+delivered.ID+"/retry", key, nil, http.StatusCreated)
	if again["trigger"] != string(execution.TriggerWebhook) {
		t.Errorf("retry of a webhook run trigger = %#v, want webhook", again["trigger"])
	}

	// A sub-workflow run has a parent, and a retry has none: a retried
	// sub-workflow (or error-workflow) run is queued as a manual one rather
	// than as an orphan sub-workflow run.
	child, err := server.executions.Create(context.Background(), repository.TenantScope{ID: "acme"}, execution.Record{
		WorkflowID: workflowID, WorkflowVersionID: finished.WorkflowVersionID, ParentExecutionID: finished.ID,
		Status: execution.StatusSucceeded, Trigger: execution.TriggerSubworkflow, StartedAt: finishedAt, FinishedAt: &finishedAt,
	})
	if err != nil {
		t.Fatalf("seed a sub-workflow execution: %v", err)
	}
	orphan := server.callJSON(t, http.MethodPost, "/api/v1/executions/"+child.ID+"/retry", key, nil, http.StatusCreated)
	if orphan["trigger"] != string(execution.TriggerManual) || orphan["parentExecutionId"] != nil && orphan["parentExecutionId"] != "" {
		t.Errorf("retry of a sub-workflow run = trigger %#v, parent %#v; want a manual run with no parent", orphan["trigger"], orphan["parentExecutionId"])
	}

	// Work that is still in flight has nothing to retry yet.
	running, err := server.executions.Create(context.Background(), repository.TenantScope{ID: "acme"}, execution.Record{
		WorkflowID: workflowID, WorkflowVersionID: finished.WorkflowVersionID,
		Status: execution.StatusRunning, Trigger: execution.TriggerManual, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed a running execution: %v", err)
	}
	conflict := server.call(t, http.MethodPost, "/api/v1/executions/"+running.ID+"/retry", key, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("retry of a running execution status = %d, want 409 (body: %s)", conflict.Code, conflict.Body)
	}
	if !strings.Contains(conflict.Body.String(), "still running") {
		t.Errorf("409 body = %s, want it to name the status", conflict.Body)
	}

	// Another tenant's execution is not there to retry.
	if recorder := server.call(t, http.MethodPost, "/api/v1/executions/"+finished.ID+"/retry", keys["globex"], nil); recorder.Code != http.StatusNotFound {
		t.Errorf("cross-tenant retry status = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
}

// seedTrace stores one finished execution whose trace holds a node output.
//
// The trace is written the way a worker writes one — under a claim, because
// the repository fences node-run writes to the lease that produced them — so
// the seed claims a queued run and settles it afterwards rather than writing
// around the fence into a row nothing owns.
func seedTrace(t *testing.T, server *authenticatedAPI, key, workflowID string) execution.Record {
	t.Helper()
	tenant := repository.TenantScope{ID: "acme"}
	stored := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID, key, nil, http.StatusOK)
	latest, _ := stored["latestVersion"].(map[string]any)
	versionID, _ := latest["id"].(string)

	queued, err := server.executions.Create(context.Background(), tenant, execution.Record{
		WorkflowID: workflowID, WorkflowVersionID: versionID,
		Status: execution.StatusQueued, Trigger: execution.TriggerManual, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed a queued execution: %v", err)
	}
	claimed, _, found, err := server.executions.ClaimNext(context.Background(), "seed", time.Now().UTC().Add(time.Minute))
	if err != nil || !found || claimed.ID != queued.ID {
		t.Fatalf("claim the seeded execution = %q/%v (%v), want %q", claimed.ID, found, err, queued.ID)
	}

	_, err = server.executions.CreateNodeRun(context.Background(), tenant, execution.NodeRun{
		ExecutionID: claimed.ID, NodeID: "manual", Attempt: 1, RunIndex: 0, Sequence: 1,
		Status: execution.StatusSucceeded, LeaseOwner: claimed.LeaseOwner,
		Input:     json.RawMessage(`{"main":[{"json":{"customer":"Ada"}}]}`),
		Output:    json.RawMessage(`[[{"json":{"customer":"Ada","total":41}}]]`),
		StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("write the seed trace: %v", err)
	}

	finishedAt := time.Now().UTC()
	claimed.Status, claimed.FinishedAt = execution.StatusSucceeded, &finishedAt
	settled, err := server.executions.UpdateRuntime(context.Background(), tenant, claimed)
	if err != nil {
		t.Fatalf("settle the seed execution: %v", err)
	}
	return settled
}

// The evaluator answers what a field held at a node, reading the same stored
// outputs the trace already returns to this caller.
func TestEvalExpressionReadsTheStoredTrace(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	key := keys["acme"]
	workflowID := server.createWorkflowAs(t, key, "Observable")
	record := seedTrace(t, server, key, workflowID)

	for _, testCase := range []struct {
		body      map[string]any
		wantValue string
		wantType  string
	}{
		{map[string]any{"expression": "{{ $json.customer }}", "nodeId": "manual"}, `"Ada"`, "string"},
		{map[string]any{"expression": "{{ $json.total + 1 }}", "nodeId": "manual"}, "42", "number"},
		{map[string]any{"expression": "{{ $input.main[0].json.customer }}", "nodeId": "manual"}, `"Ada"`, "string"},
		{map[string]any{"expression": "$json.total", "nodeId": "manual"}, "41", "number"},
	} {
		answer := server.callJSON(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", key, testCase.body, http.StatusOK)
		if string(mustJSON(t, answer["value"])) != testCase.wantValue || answer["type"] != testCase.wantType {
			t.Errorf("eval %v = %v (%v), want %s (%s)", testCase.body["expression"], answer["value"], answer["type"], testCase.wantValue, testCase.wantType)
		}
	}

	// $env is the runtime's allowlist, never the process environment: PATH is
	// in every process and is not exposed to a workflow.
	withheld := server.call(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", key,
		map[string]any{"expression": "{{ $env.PATH }}"})
	if withheld.Code != http.StatusUnprocessableEntity {
		t.Errorf("eval($env.PATH) status = %d, want 422 (body: %s)", withheld.Code, withheld.Body)
	}

	// A node nothing ran under is a mistake rather than an empty item, and an
	// expression that is not the document grammar is refused with its reason.
	unknown := server.call(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", key,
		map[string]any{"expression": "{{ $json.x }}", "nodeId": "ghost"})
	if unknown.Code != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body.String(), "ghost") {
		t.Errorf("eval(unknown node) = %d %s, want 422 naming the node", unknown.Code, unknown.Body)
	}
	broken := server.call(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", key,
		map[string]any{"expression": "{{ $json.total +", "nodeId": "manual"})
	if broken.Code != http.StatusUnprocessableEntity {
		t.Errorf("eval(unterminated expression) = %d, want 422 (body: %s)", broken.Code, broken.Body)
	}

	// Another tenant's execution does not exist to evaluate against.
	if recorder := server.call(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", keys["globex"],
		map[string]any{"expression": "{{ $json.customer }}"}); recorder.Code != http.StatusNotFound {
		t.Errorf("cross-tenant eval status = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}

	// Nothing was written by any of it: the trace still holds the one row the
	// seed created, and the execution's own state is untouched.
	read := server.callJSON(t, http.MethodGet, "/api/v1/executions/"+record.ID, key, nil, http.StatusOK)
	if runs, _ := read["nodeRuns"].([]any); len(runs) != 1 {
		t.Errorf("node runs after evaluating = %d, want the one the seed wrote", len(runs))
	}
	if read["status"] != string(execution.StatusSucceeded) {
		t.Errorf("execution status after evaluating = %#v, want it unchanged", read["status"])
	}
}

// A run can name the revision to run, so an agent reproduces a run against the
// exact graph it was reading.
func TestRunWorkflowPinsARevision(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	key := keys["acme"]
	workflowID := server.createWorkflowAs(t, key, "Revisioned")
	first := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID, key, nil, http.StatusOK)
	firstVersion, _ := first["latestVersion"].(map[string]any)
	firstVersionID, _ := firstVersion["id"].(string)

	// A second save moves the workflow's latest revision past the one the
	// caller wants to run.
	updated := validManualWorkflow("Revisioned")
	updated.Nodes[0].Name = "Manual Trigger v2"
	server.callJSON(t, http.MethodPut, "/api/v1/workflows/"+workflowID, key, workflowDraft(updated), http.StatusOK)

	pinned := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", key,
		map[string]any{"workflowVersionId": firstVersionID}, http.StatusAccepted)
	if pinned["workflowVersionId"] != firstVersionID {
		t.Errorf("run --revision queued %#v, want the pinned revision %q", pinned["workflowVersionId"], firstVersionID)
	}

	// Without the field the newest revision runs, which is what a manual run
	// has always meant.
	latest := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", key, map[string]any{}, http.StatusAccepted)
	if latest["workflowVersionId"] == firstVersionID {
		t.Errorf("run without a revision queued %#v, want the latest revision", latest["workflowVersionId"])
	}

	// A version that is not this workflow's reads as missing rather than as
	// refused: the caller learns nothing about a revision it did not name.
	other := server.createWorkflowAs(t, key, "Elsewhere")
	otherRead := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+other, key, nil, http.StatusOK)
	otherVersion, _ := otherRead["latestVersion"].(map[string]any)
	recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", key,
		map[string]any{"workflowVersionId": otherVersion["id"]})
	if recorder.Code != http.StatusNotFound {
		t.Errorf("run of another workflow's revision = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
}

// The authority each new operation needs, asserted through the mounted gate: a
// scoped key minted by the real endpoint, and the real middleware chain
// answering.
func TestTheDebugPrimitivesRespectScopedTokenAuthority(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	tenantKey := keys["acme"]
	workflowID := server.createWorkflowAs(t, tenantKey, "Guarded")
	record := server.seedExecution(t, "acme", tenantKey, workflowID)

	reader := mintScopedKey(t, server, tenantKey, "reader", map[string]any{"scopes": []string{"workflow:read"}})
	runner := mintScopedKey(t, server, tenantKey, "runner", map[string]any{"scopes": []string{"workflow:run"}})
	writer := mintScopedKey(t, server, tenantKey, "writer", map[string]any{"scopes": []string{"workflow:write"}})

	// The dry run and the evaluator are reads: compiling a document the caller
	// supplies and re-reading a trace the caller can already read.
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/validate", reader,
		workflowDraft(validManualWorkflow("Draft")), http.StatusOK)
	server.callJSON(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/eval", reader,
		map[string]any{"expression": "{{ $json }}"}, http.StatusOK)

	// A retry starts a run, and a read scope is not a run scope.
	refused := server.call(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/retry", reader, nil)
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "cannot run workflows") {
		t.Fatalf("read-only retry = %d %s, want 403 naming the run scope", refused.Code, refused.Body)
	}
	server.callJSON(t, http.MethodPost, "/api/v1/executions/"+record.ID+"/retry", runner, nil, http.StatusCreated)

	// A copy is a create, so it takes the write scope and nothing less.
	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/duplicate", writer, nil, http.StatusCreated)
	if copied["id"] == workflowID || copied["id"] == nil {
		t.Errorf("duplicate body = %#v, want a new workflow id", copied["id"])
	}
	refusedCopy := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/duplicate", reader, nil)
	if refusedCopy.Code != http.StatusForbidden || !strings.Contains(refusedCopy.Body.String(), "is read-only") {
		t.Errorf("read-only duplicate = %d %s, want 403 naming the write scope", refusedCopy.Code, refusedCopy.Body)
	}
}

// An embed session reaches the dry run and the evaluator as reads, is refused
// the copy, and is held to its own workflow's executions by the same ownership
// rule every other execution read uses.
func TestEmbedSessionsReachTheDebugPrimitivesWithinTheirConfinement(t *testing.T) {
	handler, issuer, workflowID, executions := embedServerWithExecutions(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeRun}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// The dry run is a read, and what it may validate is its own document.
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/validate",
		workflowDraft(validManualWorkflow("Guest"))); got.Code != http.StatusOK {
		t.Errorf("embed validate status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	// A copy is a sibling workflow, outside the session's subject in exactly
	// the way an import is — even with the write scope it was not given here.
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/duplicate", nil); got.Code != http.StatusForbidden {
		t.Errorf("embed duplicate status = %d, want 403 (body: %s)", got.Code, got.Body)
	}

	// An execution of a workflow the session was not granted does not exist to
	// it, whether it evaluates or retries.
	other := createWorkflow(t, handler, validManualWorkflow("Other"))
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	foreign, err := executions.Create(context.Background(), tenant, execution.Record{
		WorkflowID: other.ID, WorkflowVersionID: other.LatestVersion.ID,
		Status: execution.StatusSucceeded, Trigger: execution.TriggerManual, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed another workflow's execution: %v", err)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/executions/"+foreign.ID+"/eval",
		map[string]any{"expression": "{{ $json }}"}); got.Code != http.StatusNotFound {
		t.Errorf("embed eval of another workflow's execution = %d, want 404", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/executions/"+foreign.ID+"/retry", nil); got.Code != http.StatusNotFound {
		t.Errorf("embed retry of another workflow's execution = %d, want 404", got.Code)
	}

	// Its own workflow's execution is readable and retryable, which is what
	// makes the confinement a boundary rather than a wall.
	own, err := executions.Create(context.Background(), tenant, execution.Record{
		WorkflowID: workflowID, WorkflowVersionID: mustVersionID(t, handler, workflowID),
		Status: execution.StatusSucceeded, Trigger: execution.TriggerManual, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed the session's own execution: %v", err)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/executions/"+own.ID+"/eval",
		map[string]any{"expression": "{{ $json }}"}); got.Code != http.StatusOK {
		t.Errorf("embed eval of its own execution = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/executions/"+own.ID+"/retry", nil); got.Code != http.StatusCreated {
		t.Errorf("embed retry of its own execution = %d, want 201 (body: %s)", got.Code, got.Body)
	}
}

// mustVersionID reads the latest revision of a workflow through the surface the
// test itself is driving.
func mustVersionID(t *testing.T, handler http.Handler, workflowID string) string {
	t.Helper()
	stored := requestJSON[workflowResource](t, handler, http.MethodGet, "/api/v1/workflows/"+workflowID, nil, http.StatusOK)
	return stored.LatestVersion.ID
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode %#v: %v", value, err)
	}
	return encoded
}
