package n8n_test

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestConvertToFileOptionsMatchTheNodePack(t *testing.T) {
	t.Parallel()

	for _, key := range nodes.ConvertToFileOptionKeys {
		if !n8n.ConvertToFileOptionSupported(key) {
			t.Errorf("the node implements option %q but the importer would refuse it", key)
		}
	}
}

func TestConvertToFileImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.convertToFile", 1.1,
		`{"operation":"csv","binaryPropertyName":"report","options":{"fileName":"people.csv","delimiter":";","headerRow":true,"compression":true}}`)
	if imported.Type != n8n.ConvertToFileNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.ConvertToFileNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
	wantOptions := map[string]any{"fileName": "people.csv", "delimiter": ";", "headerRow": true}
	if !reflect.DeepEqual(imported.Parameters["options"], wantOptions) {
		t.Fatalf("options = %#v, want %#v (the packing option is not carried)", imported.Parameters["options"], wantOptions)
	}

	store := binaryStoreForImport(t)
	output := executeImportedWithBinaries(t, imported, nodes.ConvertToFileExecutorID, workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "Ada", "city": "Jakarta"}}, {JSON: map[string]any{"name": "Grace", "city": "NYC"}},
	}}, store)
	file := output[0][0].Binary["report"]
	if file.FileName != "people.csv" {
		t.Fatalf("output binary = %#v, want the file under the chosen field and name", output[0][0].Binary)
	}
	body, _, err := store.Get(file.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	contents, _ := io.ReadAll(body)
	_ = body.Close()
	if string(contents) != "city;name\nJakarta;Ada\nNYC;Grace\n" {
		t.Fatalf("csv = %q", contents)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.convertToFile" || returned.TypeVersion != 1.1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.convertToFile v1.1", returned.Type, returned.TypeVersion)
	}
	if returned.Parameters["operation"] != "csv" || returned.Parameters["binaryPropertyName"] != "report" ||
		!reflect.DeepEqual(returned.Parameters["options"], wantOptions) {
		t.Fatalf("exported parameters = %#v", returned.Parameters)
	}
}

// A node saved with every default left out writes a CSV into the data field.
func TestConvertToFileImportsAnAllDefaultsNodeAsCSV(t *testing.T) {
	t.Parallel()

	imported, _ := importNode(t, "n8n-nodes-base.convertToFile", 1, `{}`)
	store := binaryStoreForImport(t)
	output := executeImportedWithBinaries(t, imported, nodes.ConvertToFileExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{"a": "1"}}}}, store)
	if file := output[0][0].Binary["data"]; file.FileName != "file.csv" || file.MediaType != "text/csv" {
		t.Fatalf("output binary = %#v, want file.csv in data", output[0][0].Binary)
	}
}

func TestConvertToFileImportsTheJSONAndTextOperations(t *testing.T) {
	t.Parallel()

	store := binaryStoreForImport(t)
	imported, _ := importNode(t, "n8n-nodes-base.convertToFile", 1.1, `{"operation":"toJson","mode":"each","options":{"format":true}}`)
	output := executeImportedWithBinaries(t, imported, nodes.ConvertToFileExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{"id": float64(1)}}, {JSON: map[string]any{"id": float64(2)}}}}, store)
	if len(output[0]) != 2 || output[0][1].Binary["data"].FileName != "file.json" {
		t.Fatalf("output = %#v, want a file per item", output[0])
	}

	imported, _ = importNode(t, "n8n-nodes-base.convertToFile", 1.1, `{"operation":"toText","sourceProperty":"={{ 'note' }}"}`)
	value, _ := imported.Parameters["sourceProperty"].(map[string]any)
	if value["mode"] != "expression" {
		t.Fatalf("sourceProperty = %#v, want an expression marker", imported.Parameters["sourceProperty"])
	}
}

func TestConvertToFileRefusesWhatItDoesNotWrite(t *testing.T) {
	t.Parallel()

	for name, parameters := range map[string]string{
		"xls":        `{"operation":"xls"}`,
		"ods":        `{"operation":"ods"}`,
		"rtf":        `{"operation":"rtf"}`,
		"html":       `{"operation":"html"}`,
		"iCal":       `{"operation":"iCal"}`,
		"dataIsBase": `{"operation":"toBinary","options":{"dataIsBase64":true}}`,
		"latin1":     `{"operation":"csv","options":{"encoding":"latin1"}}`,
	} {
		imported, result := importNode(t, "n8n-nodes-base.convertToFile", 1.1, parameters)
		if imported.Type != n8n.UnsupportedNodeType {
			t.Errorf("%s: imported as %q, want the unsupported placeholder", name, imported.Type)
			continue
		}
		blocking := blockingFor(result, "Legacy")
		if len(blocking) == 0 || !strings.Contains(blocking[0].Reason, strings.TrimSuffix(name, "Base")) {
			t.Errorf("%s: blocking = %#v, want a reason naming it", name, blocking)
		}
	}
	// UTF-8 under any spelling, and an unsupported option switched off, change nothing.
	imported, _ := importNode(t, "n8n-nodes-base.convertToFile", 1.1, `{"operation":"csv","options":{"encoding":"utf-8","dataIsBase64":false}}`)
	if imported.Type != n8n.ConvertToFileNodeType {
		t.Fatalf("imported as %q, want the node", imported.Type)
	}
}
