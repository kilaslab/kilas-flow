package n8n_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// sheetsTemplateEnv names a file holding a real n8n template, fetched by the
// person running the check into a directory outside the repository. The
// template is never committed: the licence boundary keeps third-party templates
// out of the tree, so this check reads one only when asked to.
const sheetsTemplateEnv = "KILASFLOW_TEST_SHEETS_TEMPLATE"

// TestARealSheetsTemplateImportsWithNoBlockingIssue is the manual check behind
// "template 19200 imports with no blocking issue". Point the variable at the
// workflow JSON (the `workflow.workflow` object of api.n8n.io's answer for
// template 19200) to run it; without the variable it skips.
func TestARealSheetsTemplateImportsWithNoBlockingIssue(t *testing.T) {
	path := os.Getenv(sheetsTemplateEnv)
	if path == "" {
		t.Skipf("%s is not set: fetch a real template into a scratch directory and point it here to run this check", sheetsTemplateEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	result, err := n8n.Import(raw, registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	counts := map[string]int{}
	for _, node := range result.Document.Nodes {
		counts[node.Type]++
	}
	t.Logf("imported %d nodes: %v", len(result.Document.Nodes), counts)
	for _, issue := range result.Unsupported {
		t.Logf("%-8s %-40s %-18s %s", issue.Severity, issue.NodeName, issue.Field, issue.Reason)
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("blocking issue on %q (%s): %s", issue.NodeName, issue.Field, issue.Reason)
		}
	}
	if counts[n8n.UnsupportedNodeType] != 0 {
		t.Errorf("%d nodes imported as the unsupported placeholder", counts[n8n.UnsupportedNodeType])
	}

	// Compiled with every credential the document asks for bound to a
	// stand-in, the same check a run makes.
	document := result.Document
	document.ID = "wf_real_template"
	catalog := registry(t)
	for index := range document.Nodes {
		definition, found := catalog.Lookup(document.Nodes[index].Type, document.Nodes[index].TypeVersion)
		if !found {
			continue
		}
		bound := map[string]string{}
		for _, credentialType := range definition.RequiredCredentials {
			bound[credentialType] = "stand-in"
		}
		if len(bound) > 0 {
			document.Nodes[index].Credentials = bound
		}
	}
	if _, err := workflow.Compile(document, catalog); err != nil {
		encoded, _ := json.Marshal(err.Error())
		t.Errorf("Compile() error = %s", encoded)
	}
}
