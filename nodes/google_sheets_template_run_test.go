package nodes_test

import (
	"context"
	"os"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// TestTheRealTemplateRunsItsFirstSheetsStepAgainstTheStub is the manual check
// behind "template 19200 runs its Sheets step against a stub". Point
// KILASFLOW_TEST_SHEETS_TEMPLATE at the template's workflow JSON, fetched into a
// scratch directory outside the repository; without it the test skips. Only the
// first step is asserted: the template's later steps call a CRM over HTTP and
// name a node that is not in the file, so they cannot finish against a stub.
func TestTheRealTemplateRunsItsFirstSheetsStepAgainstTheStub(t *testing.T) {
	path := os.Getenv("KILASFLOW_TEST_SHEETS_TEMPLATE")
	if path == "" {
		t.Skip("KILASFLOW_TEST_SHEETS_TEMPLATE is not set: point it at a real template's workflow JSON to run this check")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	imported, err := n8n.Import(raw, catalog)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	document := imported.Document
	document.ID = "wf_real_template"
	for index := range document.Nodes {
		definition, found := catalog.Lookup(document.Nodes[index].Type, document.Nodes[index].TypeVersion)
		if !found || len(definition.RequiredCredentials) == 0 {
			continue
		}
		bound := map[string]string{}
		for _, credentialType := range definition.RequiredCredentials {
			bound[credentialType] = "cred"
		}
		document.Nodes[index].Credentials = bound
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// The first step reads the accounts tab by gid, keeping the rows whose
	// DONE STATUS is NOT DONE.
	fake := newFakeSheets(t, "YOUR_GOOGLE_SHEET_ID_2")
	fake.addTab(712044815, "Accounts Tab", 20,
		sheetsRow("Id", "Name", "DONE STATUS"),
		sheetsRow("001A", "Acme", "NOT DONE"),
		sheetsRow("001B", "Globex", "DONE"),
		sheetsRow("001C", "Initech", "NOT DONE"),
	)
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, sheetsPolicy(t, fake.endpoint()), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	// Later steps fail by design here, so the run's own error is not the check.
	result, _ := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{}},
		Credentials: googleCreds{
			typ: nodes.GoogleSheetsCredentialType, host: httptestHost(fake.server.URL),
		},
	})
	var read *engine.NodeRun
	for index, run := range result.NodeRuns {
		for _, candidate := range document.Nodes {
			if candidate.ID == run.NodeID && candidate.Name == "Sheets - Read Salesforce Accounts" {
				read = &result.NodeRuns[index]
			}
		}
	}
	if read == nil {
		t.Fatalf("the first Sheets step has no trace row\ncalls:\n%s", fake.callLog())
	}
	if read.Error != nil {
		t.Fatalf("the first Sheets step failed: %v\ncalls:\n%s", read.Error, fake.callLog())
	}
	if len(read.Output) != 1 || len(read.Output[0]) != 2 || read.Output[0][0].JSON["Name"] != "Acme" || read.Output[0][1].JSON["row_number"] != 4.0 {
		t.Fatalf("the first Sheets step produced %#v, want the two NOT DONE rows with their sheet row numbers", read.Output)
	}
	t.Logf("requests made:\n%s", fake.callLog())
}
