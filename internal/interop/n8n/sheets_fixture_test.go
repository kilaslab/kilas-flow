package n8n_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// sheets19200Fixture is a hand-authored workflow with the Sheets steps of n8n
// template 19200: a read with no operation key, an update that matches on
// row_number, an append and an appendOrUpdate, with the settings that template
// puts on them. It is not an n8n.io export and carries none of its bytes.
func sheets19200Fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "n8n_sheets_19200.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "hand-authored KilasFlow fixture") {
		t.Fatal("the fixture must say it is hand-authored")
	}
	return raw
}

// Template 19200's Sheets steps import onto the native node with nothing
// blocking: the template carries no credential references, and every
// parameter it uses has a home.
func TestTheSheets19200ShapeImportsWithNoBlockingIssue(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import(sheets19200Fixture(t), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("blocking issue on %q (%s): %s", issue.NodeName, issue.Field, issue.Reason)
		}
	}
	sheets := 0
	for _, node := range result.Document.Nodes {
		if node.Type == n8n.UnsupportedNodeType {
			t.Errorf("node %q imported as the unsupported placeholder", node.Name)
		}
		if node.Type == n8n.GoogleSheetsNodeType {
			sheets++
		}
	}
	if sheets != 4 {
		t.Errorf("Google Sheets nodes = %d, want 4", sheets)
	}

	read := nodeByName(result.Document, "Get pending rows")
	if read.Parameters["operation"] != "read" {
		t.Errorf("read operation = %#v, want n8n's default made explicit", read.Parameters["operation"])
	}
	if read.Settings["alwaysOutputData"] != true {
		t.Errorf("read settings = %#v, want Always Output Data carried", read.Settings)
	}
	update := nodeByName(result.Document, "Mark as done")
	if update.Settings["onError"] != "continueErrorOutput" {
		t.Errorf("update settings = %#v, want the error output carried", update.Settings)
	}
	for name, want := range map[string]string{"Mark as done": "update", "Log result": "append", "Upsert totals": "appendOrUpdate"} {
		if got := nodeByName(result.Document, name).Parameters["operation"]; got != want {
			t.Errorf("%s operation = %#v, want %q", name, got, want)
		}
	}
}

// Once a credential is bound, the imported graph passes the compiler: this is
// the same check a run makes, so an import with no blocking issue is also one
// that can start.
func TestTheSheets19200ShapeCompilesOnceACredentialIsBound(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import(sheets19200Fixture(t), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	document := result.Document
	document.ID = "wf_sheets"
	if _, err := workflow.Compile(document, registry(t)); err == nil || !strings.Contains(err.Error(), "googleSheetsOAuth2Api") {
		t.Fatalf("Compile() = %v, want it to ask for the Sheets credential", err)
	}
	for index := range document.Nodes {
		if document.Nodes[index].Type == n8n.GoogleSheetsNodeType {
			document.Nodes[index].Credentials = map[string]string{"googleSheetsOAuth2Api": "cred"}
		}
	}
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}
