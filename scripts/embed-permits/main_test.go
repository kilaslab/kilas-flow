package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

func buildTable(t *testing.T) table {
	t.Helper()
	built, err := build()
	if err != nil {
		t.Fatalf("build() error = %v", err)
	}

	return built
}

// The table is walked from the live document, so an operation added later is in
// it without anyone remembering to add it, and every operation answers for
// every scope: a gap here would read as "Deny" on the page for the wrong reason.
func TestEveryOperationOfTheDocumentIsEvaluatedForEveryScope(t *testing.T) {
	built := buildTable(t)

	if len(built.Operations) < 80 {
		t.Fatalf("table holds %d operations, want the whole document (80 or more)", len(built.Operations))
	}
	want := 0
	for _, subject := range built.Subjects {
		want += len(subject.Scopes)
	}
	if want != 7 {
		t.Fatalf("subjects carry %d scopes, want the 7 embed scopes", want)
	}
	for id, operation := range built.Operations {
		if len(operation.Verdicts) != want {
			t.Errorf("%s has %d verdicts, want one per scope (%d)", id, len(operation.Verdicts), want)
		}
		for scope, verdict := range operation.Verdicts {
			if verdict.Allow && (verdict.Status != 0 || verdict.Detail != "") {
				t.Errorf("%s with %s is allowed yet carries a refusal %d %q", id, scope, verdict.Status, verdict.Detail)
			}
			if !verdict.Allow && (verdict.Status == 0 || verdict.Detail == "") {
				t.Errorf("%s with %s is refused without a status and a sentence", id, scope)
			}
		}
	}
}

// The verdicts below are the ones the hand-written generator got wrong or could
// not state: datastore rows are allowed for a datastore session, the publish
// and activate scopes stand alone, and the sentences are the server's own.
func TestTheTableStatesTheServersVerdicts(t *testing.T) {
	built := buildTable(t)

	for _, test := range []struct {
		operation string
		scope     string
		allow     bool
		detail    string
	}{
		{"insert-datastore-row", "datastore:write", true, ""},
		{"insert-datastore-row", "datastore:read", false, "This embed session is read-only."},
		{"list-datastore-rows", "datastore:read", true, ""},
		{"list-datastore-rows", "datastore:write", true, ""},
		{"export-datastore-rows", "datastore:read", true, ""},
		{"import-datastore-rows", "datastore:write", true, ""},
		{"clear-datastore", "datastore:write", false, "This embed session cannot manage datastores."},
		{"list-datastores", "datastore:read", false, "This embed session cannot manage datastores."},
		{"get-workflow", "workflow:read", true, ""},
		{"get-workflow", "workflow:run", true, ""},
		{"get-workflow", "datastore:read", false, "This embed session cannot read."},
		{"update-workflow", "workflow:read", false, "This embed session is read-only."},
		{"update-workflow", "workflow:write", true, ""},
		{"run-workflow", "workflow:write", false, "This embed session cannot run workflows."},
		{"run-workflow", "workflow:run", true, ""},
		{"activate-workflow", "workflow:write", false, "This embed session cannot change activation."},
		{"activate-workflow", "workflow:activate", true, ""},
		{"publish-workflow-version", "workflow:write", false, "This embed session cannot publish a revision without the workflow:publish scope."},
		{"publish-workflow-version", "workflow:publish", true, ""},
		{"delete-workflow", "workflow:write", false, "This embed session cannot delete a workflow."},
		{"list-workflows", "workflow:write", false, "This embed session cannot list workflows."},
		{"list-executions", "workflow:read", true, ""},
		{"list-variables", "workflow:write", false, "This embed session cannot manage variables."},
		{"get-me", "workflow:read", false, "This embed session cannot use this endpoint."},
	} {
		operation, found := built.Operations[test.operation]
		if !found {
			t.Errorf("operation %q is not in the table", test.operation)
			continue
		}
		verdict := operation.Verdicts[test.scope]
		if verdict.Allow != test.allow || verdict.Detail != test.detail {
			t.Errorf("%s with %s = allow %t %q, want allow %t %q",
				test.operation, test.scope, verdict.Allow, verdict.Detail, test.allow, test.detail)
		}
	}
}

// requires names the one scope that unlocks an operation when the others only
// imply it, so a page can say "Allow with workflow:read" rather than listing
// five scopes; it is empty where no scope opens the operation.
func TestRequiresNamesTheWeakestScopeThatOpensAnOperation(t *testing.T) {
	built := buildTable(t)

	for operation, want := range map[string]string{
		"get-workflow":             "workflow:read",
		"update-workflow":          "workflow:write",
		"run-workflow":             "workflow:run",
		"activate-workflow":        "workflow:activate",
		"publish-workflow-version": "workflow:publish",
		"get-datastore":            "datastore:read",
		"upsert-datastore-row":     "datastore:write",
		"list-workflows":           "",
		"create-embed-session":     "",
	} {
		if got := built.Operations[operation].Requires; got != want {
			t.Errorf("%s requires %q, want %q", operation, got, want)
		}
	}
}

// A request for another workflow is refused by the gate itself, so the table
// can say which operations are bound to the session's own workflow; a datastore
// is bound in the handler (404), which the gate cannot see, so it is not claimed.
func TestOwnWorkflowOnlyIsDerivedFromTheGateNotAssumed(t *testing.T) {
	built := buildTable(t)

	for operation, want := range map[string]bool{
		"get-workflow":               true,
		"run-workflow":               true,
		"list-executions":            true,
		"list-node-types":            false,
		"validate-workflow-document": false,
		"get-datastore":              false,
	} {
		if got := built.Operations[operation].OwnWorkflowOnly; got != want {
			t.Errorf("%s ownWorkflowOnly = %t, want %t", operation, got, want)
		}
	}
}

func TestRenderIsStable(t *testing.T) {
	first, err := render(buildTable(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := render(buildTable(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of the same code differ: the committed file could never match")
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("the rendered file does not end in a newline")
	}
}

// The committed file is what the reference generator reads, so it must be the
// file this command renders now. `make generate-embed-permits-check` is the
// same comparison for CI; running it here too means a change to the gate cannot
// pass `go test` and fail only in the drift job.
func TestCommittedTableMatchesTheGate(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(root, tablePath))
	if err != nil {
		t.Fatalf("read the committed table: %v (run: make generate-embed-permits)", err)
	}
	rendered, err := render(buildTable(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, rendered) {
		t.Fatalf("%s is stale: run make generate-embed-permits", tablePath)
	}
}
