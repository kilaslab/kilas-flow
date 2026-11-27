package n8n_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestXMLOptionsMatchTheNodePack(t *testing.T) {
	t.Parallel()

	for _, key := range nodes.XMLOptionKeys {
		if !n8n.XMLOptionSupported(key) {
			t.Errorf("the node implements option %q but the importer would refuse it", key)
		}
	}
}

func TestXMLToJSONImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.xml", 1,
		`{"mode":"xmlToJson","dataPropertyName":"xml","options":{"explicitArray":true,"attrkey":"@","async":true}}`)
	if imported.Type != n8n.XMLNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.XMLNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
	// The async flag changes no output, so it is not carried.
	wantOptions := map[string]any{"explicitArray": true, "attrkey": "@"}
	if !reflect.DeepEqual(imported.Parameters["options"], wantOptions) {
		t.Fatalf("options = %#v, want %#v", imported.Parameters["options"], wantOptions)
	}

	output := executeImported(t, imported, nodes.XMLExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{
		"xml": `<order><line>pen</line></order>`,
	}}}})
	want := map[string]any{"order": map[string]any{"line": []any{"pen"}}}
	if !reflect.DeepEqual(output[0][0].JSON, want) {
		t.Fatalf("output = %#v, want %#v", output[0][0].JSON, want)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.xml" || returned.TypeVersion != 1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.xml v1", returned.Type, returned.TypeVersion)
	}
	if returned.Parameters["mode"] != "xmlToJson" || returned.Parameters["dataPropertyName"] != "xml" ||
		!reflect.DeepEqual(returned.Parameters["options"], wantOptions) {
		t.Fatalf("exported parameters = %#v", returned.Parameters)
	}
}

func TestJSONToXMLImportsAndRunsWithRenderOptions(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.xml", 1,
		`{"mode":"jsonToxml","dataPropertyName":"body","options":{"headless":true,"rootName":"items","renderOpts":{"pretty":false}}}`)
	if imported.Type != n8n.XMLNodeType || len(blockingFor(result, "Legacy")) != 0 {
		t.Fatalf("imported as %q with blocking %#v", imported.Type, blockingFor(result, "Legacy"))
	}
	output := executeImported(t, imported, nodes.XMLExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{"a": "1", "b": "2"}}}})
	if got := output[0][0].JSON["body"]; got != "<items><a>1</a><b>2</b></items>" {
		t.Fatalf("body = %#v, want a compact document under the root name", got)
	}
	returned := exportedLegacyNode(t, result)
	options, _ := returned.Parameters["options"].(map[string]any)
	if !reflect.DeepEqual(options["renderOpts"], map[string]any{"pretty": false}) || options["rootName"] != "items" {
		t.Fatalf("exported options = %#v, want the render options kept", options)
	}
}

// A node saved with every default left out is an XML-to-JSON conversion of the
// data field.
func TestXMLImportsAnAllDefaultsNodeAsXMLToJSON(t *testing.T) {
	t.Parallel()

	imported, _ := importNode(t, "n8n-nodes-base.xml", 1, `{}`)
	output := executeImported(t, imported, nodes.XMLExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{"data": "<a x=\"1\"/>"}}}})
	if want := map[string]any{"a": map[string]any{"x": "1"}}; !reflect.DeepEqual(output[0][0].JSON, want) {
		t.Fatalf("output = %#v, want %#v", output[0][0].JSON, want)
	}
}

func TestXMLRefusesAnOptionItDoesNotImplement(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.xml", 1, `{"mode":"xmlToJson","options":{"explicitChildren":true}}`)
	if imported.Type != n8n.UnsupportedNodeType {
		t.Fatalf("imported as %q, want the unsupported placeholder", imported.Type)
	}
	blocking := blockingFor(result, "Legacy")
	if len(blocking) == 0 || !strings.Contains(blocking[0].Reason, "explicitChildren") {
		t.Fatalf("blocking = %#v, want a reason naming the option", blocking)
	}
}
