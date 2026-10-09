package nodes_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// sheetsTemplateRun is the hand-authored 19200-shaped workflow, imported,
// bound to a credential, compiled, and ready to run against an in-memory
// spreadsheet that holds the placeholder document the fixture names.
type sheetsTemplateRun struct {
	fake     *fakeSheets
	document workflow.Document
	ir       workflow.IR
	executor *engine.Registry
}

func newSheetsTemplateRun(t *testing.T) *sheetsTemplateRun {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "internal", "interop", "n8n", "testdata", "n8n_sheets_19200.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	imported, err := n8n.Import(raw, catalog)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	for _, issue := range imported.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Fatalf("the fixture has a blocking issue on %q: %s", issue.NodeName, issue.Reason)
		}
	}
	document := imported.Document
	document.ID = "wf_sheets_run"
	for index := range document.Nodes {
		if document.Nodes[index].Type == nodes.GoogleSheetsNodeType {
			document.Nodes[index].Credentials = map[string]string{nodes.GoogleSheetsCredentialType: "cred"}
		}
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	fake := newFakeSheets(t, "1PLACEHOLDERdocumentId")
	fake.addTab(0, "Orders", 50,
		sheetsRow("order", "status"),
		sheetsRow("A-100", "pending"),
		sheetsRow("A-101", "done"),
		sheetsRow("A-102", "pending"),
		sheetsRow("A-103", "pending"),
	)
	fake.addTab(7, "Log", 50, sheetsRow("event", "order_row"))
	fake.addTab(9, "Totals", 50, sheetsRow("key", "value"), sheetsRow("last_row", 0.0))

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, sheetsPolicy(t, fake.endpoint()), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	return &sheetsTemplateRun{fake: fake, document: document, ir: ir, executor: executors}
}

// run starts the workflow with a pending-status item, as the read's filter
// expects, and answers each node's trace row by the node's name.
func (template *sheetsTemplateRun) run(t *testing.T) map[string]engine.NodeRun {
	t.Helper()
	result, err := engine.NewRunner(template.executor).Run(context.Background(), template.ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"status": "pending"}},
		Credentials: googleCreds{
			typ: nodes.GoogleSheetsCredentialType, host: httptestHost(template.fake.server.URL),
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v\ncalls:\n%s", err, template.fake.callLog())
	}
	byName := map[string]engine.NodeRun{}
	for _, run := range result.NodeRuns {
		for _, candidate := range template.document.Nodes {
			if candidate.ID == run.NodeID {
				byName[candidate.Name] = run
			}
		}
	}
	return byName
}

// The Sheets steps of an n8n template, imported and run end to end: the import
// maps them, the compiler accepts them with a credential bound, and the runner
// drives the native node against an in-memory spreadsheet. The assertions are
// on what the spreadsheet holds afterwards.
func TestAnImportedSheetsWorkflowRunsAgainstTheStub(t *testing.T) {
	template := newSheetsTemplateRun(t)
	byName := template.run(t)

	for name, run := range byName {
		if run.Error != nil {
			t.Errorf("node %s failed: %v", name, run.Error)
		}
		if run.Skipped {
			t.Errorf("node %s was skipped", name)
		}
	}
	// The read found the three pending rows, the Limit kept two, and each step
	// handed the next what it wrote.
	if got := len(byName["Get pending rows"].Output[0]); got != 3 {
		t.Errorf("the read produced %d rows, want the 3 pending ones", got)
	}
	if got := len(byName["Limit"].Output[0]); got != 2 {
		t.Errorf("the limit kept %d rows, want 2", got)
	}
	if got := len(byName["Mark as done"].Output[0]); got != 2 {
		t.Errorf("the update produced %d items, want the mapped values of both rows", got)
	}

	want := map[string][][]any{
		// Rows 2 and 4 were the two the Limit kept; row 5 is still pending.
		"Orders": {
			sheetsRow("order", "status"),
			sheetsRow("A-100", "done"),
			sheetsRow("A-101", "done"),
			sheetsRow("A-102", "done"),
			sheetsRow("A-103", "pending"),
		},
		"Log": {
			sheetsRow("event", "order_row"),
			sheetsRow("marked done", 2.0),
			sheetsRow("marked done", 4.0),
		},
		// Both items name the same key, so the one row is updated over the sheet
		// as it was, and ends holding the last value.
		"Totals": {sheetsRow("key", "value"), sheetsRow("last_row", 4.0)},
	}
	for title, grid := range want {
		if got := template.fake.snapshot(title); !equalGrid(got, grid) {
			t.Errorf("%s holds\n got %v\nwant %v\ncalls:\n%s", title, got, grid, template.fake.callLog())
		}
	}

	// The update has Continue (using error output) set, which used to split it
	// into a call per item. As one batch it reads the sheet once and writes once.
	if got := template.fake.callsMatching(`GET .*/values/'Orders'$`); len(got) != 2 {
		t.Errorf("reads of Orders = %d, want 2 (the read node and the update node)\n%s", len(got), template.fake.callLog())
	}
	if got := template.fake.callsMatching(`POST .*/values:batchUpdate$`); len(got) != 2 {
		t.Errorf("value batches = %d, want 2 (the update, then the totals)\n%s", len(got), template.fake.callLog())
	}
}

// A failure of the update, under Continue (using error output), is one error
// item on the node's error output, as n8n's node gives, and the rest of the
// workflow is not run on rows that were never written.
func TestASheetsFailureUnderTheErrorOutputIsOneErrorItem(t *testing.T) {
	template := newSheetsTemplateRun(t)
	template.fake.failWith = 400
	template.fake.failOnly = regexp.MustCompile(`^POST .*/values:batchUpdate$`)

	byName := template.run(t)
	update := byName["Mark as done"]
	if len(update.Output) < 2 || len(update.Output[0]) != 0 || len(update.Output[1]) != 1 {
		t.Fatalf("the update's outputs = %#v, want nothing on main and one error item on the error output", update.Output)
	}
	if _, ok := update.Output[1][0].JSON[engine.ErrorItemKey]; !ok {
		t.Errorf("the error item = %#v, want an %s object", update.Output[1][0].JSON, engine.ErrorItemKey)
	}
	if got := template.fake.snapshot("Orders"); !equalGrid(got[1:2], [][]any{sheetsRow("A-100", "pending")}) {
		t.Errorf("Orders = %v, want the failed update to have changed nothing", got)
	}
	if got := template.fake.callsMatching(`GET .*/values/'Orders'$`); len(got) != 2 {
		t.Errorf("reads of Orders = %d, want 2: the failed batch is not retried item by item\n%s", len(got), template.fake.callLog())
	}
}

func equalGrid(got, want [][]any) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if len(got[index]) != len(want[index]) {
			return false
		}
		for column := range got[index] {
			if got[index][column] != want[index][column] {
				return false
			}
		}
	}
	return true
}
