package n8n_test

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestSpreadsheetFileWritingMapsOntoConvertToFile(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.spreadsheetFile", 2,
		`{"operation":"toFile","fileFormat":"csv","binaryPropertyName":"sheet","options":{"fileName":"out.csv","headerRow":true,"compression":true}}`)
	if imported.Type != n8n.ConvertToFileNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.ConvertToFileNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
	wantOptions := map[string]any{"fileName": "out.csv", "headerRow": true}
	if imported.Parameters["operation"] != "csv" || imported.Parameters["binaryPropertyName"] != "sheet" ||
		!reflect.DeepEqual(imported.Parameters["options"], wantOptions) {
		t.Fatalf("parameters = %#v", imported.Parameters)
	}

	store := binaryStoreForImport(t)
	output := executeImportedWithBinaries(t, imported, nodes.ConvertToFileExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{"a": "1", "b": "2"}}}}, store)
	file := output[0][0].Binary["sheet"]
	body, _, err := store.Get(file.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	contents, _ := io.ReadAll(body)
	_ = body.Close()
	if file.FileName != "out.csv" || string(contents) != "a,b\n1,2\n" {
		t.Fatalf("file = %#v, contents %q", file, contents)
	}

	// It goes back out as the native node it became.
	if returned := exportedLegacyNode(t, result); returned.Type != "n8n-nodes-base.convertToFile" {
		t.Fatalf("exported as %s, want n8n-nodes-base.convertToFile", returned.Type)
	}
}

func TestSpreadsheetFileReadingMapsOntoExtractFromFile(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.spreadsheetFile", 2,
		`{"operation":"fromFile","fileFormat":"csv","binaryPropertyName":"upload","options":{"headerRow":true}}`)
	if imported.Type != n8n.ExtractFromFileNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.ExtractFromFileNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	store := binaryStoreForImport(t)
	reference, err := store.Put("people.csv", "text/csv", bytes.NewReader([]byte("name,city\nAda,Jakarta\nGrace,NYC\n")))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	output := executeImportedWithBinaries(t, imported, nodes.ExtractFromFileExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"upload": reference}}}}, store)
	if len(output[0]) != 2 || output[0][0].JSON["name"] != "Ada" || output[0][1].JSON["city"] != "NYC" {
		t.Fatalf("output = %#v, want a row per item", output[0])
	}
}

func TestSpreadsheetFileReadingXLSXSaysCellsComeBackAsText(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.spreadsheetFile", 2, `{"operation":"fromFile","fileFormat":"xlsx"}`)
	if imported.Type != n8n.ExtractFromFileNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.ExtractFromFileNodeType)
	}
	said := false
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Legacy" && issue.Severity == n8n.SeverityLossy && strings.Contains(issue.Reason, "text") {
			said = true
		}
	}
	if !said {
		t.Fatalf("issues = %#v, want a lossy note that cells are read as text", result.Unsupported)
	}
}

func TestSpreadsheetFileRefusesWhatTheNativeNodesDoNot(t *testing.T) {
	t.Parallel()

	for name, parameters := range map[string]string{
		"autodetect":    `{}`,
		"ods":           `{"operation":"fromFile","fileFormat":"ods"}`,
		"xls":           `{"operation":"toFile"}`,
		"rtf":           `{"operation":"toFile","fileFormat":"rtf"}`,
		"range":         `{"operation":"fromFile","fileFormat":"csv","options":{"range":"A1:C5"}}`,
		"readAsString":  `{"operation":"fromFile","fileFormat":"xlsx","options":{"readAsString":true}}`,
		"rawData":       `{"operation":"toFile","fileFormat":"csv","options":{"rawData":true}}`,
		"sideEffectOps": `{"operation":"merge"}`,
	} {
		imported, result := importNode(t, "n8n-nodes-base.spreadsheetFile", 2, parameters)
		if imported.Type != n8n.UnsupportedNodeType {
			t.Errorf("%s: imported as %q, want the unsupported placeholder", name, imported.Type)
			continue
		}
		blocking := blockingFor(result, "Legacy")
		needle := name
		switch name {
		case "sideEffectOps":
			needle = "merge"
		case "autodetect":
			needle = "Extract From File (kilasflow.extractFromFile)"
		}
		if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, needle) {
			t.Errorf("%s: blocking = %#v, want a reason naming %q", name, blocking, needle)
		}
	}
}
