package n8n_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestHTMLOptionsMatchTheNodePack(t *testing.T) {
	t.Parallel()

	for _, key := range nodes.HTMLOptionKeys {
		if !n8n.HTMLOptionSupported(key) {
			t.Errorf("the node implements option %q but the importer would refuse it", key)
		}
	}
}

const htmlExtractParameters = `{"operation":"extractHtmlContent","dataPropertyName":"page",` +
	`"extractionValues":{"values":[` +
	`{"key":"title","cssSelector":"h1"},` +
	`{"key":"links","cssSelector":"a.nav","returnValue":"attribute","attribute":"href","returnArray":true},` +
	`{"key":"summary","cssSelector":"p.lead","returnValue":"html","skipSelectors":".ad"}]},` +
	`"options":{"trimValues":true,"cleanUpText":false}}`

func TestHTMLExtractImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.html", 1.2, htmlExtractParameters)
	if imported.Type != n8n.HTMLNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.HTMLNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	output := executeImported(t, imported, nodes.HTMLExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{
		"page": `<h1> Hello </h1><a class="nav" href="/a">A</a><a class="nav" href="/b">B</a><p class="lead">Lead <b>text</b><i class="ad">ad</i></p>`,
	}}}})
	want := map[string]any{"title": "Hello", "links": []any{"/a", "/b"}, "summary": "Lead <b>text</b>"}
	if !reflect.DeepEqual(output[0][0].JSON, want) {
		t.Fatalf("output = %#v, want %#v", output[0][0].JSON, want)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.html" || returned.TypeVersion != 1.2 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.html v1.2", returned.Type, returned.TypeVersion)
	}
	if returned.Parameters["operation"] != "extractHtmlContent" || returned.Parameters["dataPropertyName"] != "page" {
		t.Fatalf("exported parameters = %#v", returned.Parameters)
	}
	rows, _ := returned.Parameters["extractionValues"].(map[string]any)["values"].([]any)
	if len(rows) != 3 || !reflect.DeepEqual(rows[1], map[string]any{
		"key": "links", "cssSelector": "a.nav", "returnValue": "attribute", "attribute": "href", "returnArray": true,
	}) {
		t.Fatalf("exported rows = %#v", rows)
	}
	if !reflect.DeepEqual(returned.Parameters["options"], map[string]any{"trimValues": true, "cleanUpText": false}) {
		t.Fatalf("exported options = %#v", returned.Parameters["options"])
	}
}

func TestHTMLTableImportsAndRuns(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.html", 1.2,
		`{"operation":"convertToHtmlTable","options":{"capitalize":true,"customStyling":true,"tableAttributes":"class=\"report\"","caption":"People"}}`)
	if imported.Type != n8n.HTMLNodeType || len(blockingFor(result, "Legacy")) != 0 {
		t.Fatalf("imported as %q with blocking %#v", imported.Type, blockingFor(result, "Legacy"))
	}
	output := executeImported(t, imported, nodes.HTMLExecutorID, workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "Ada"}}, {JSON: map[string]any{"name": "Grace"}},
	}})
	table, _ := output[0][0].JSON["table"].(string)
	if !strings.HasPrefix(table, `<table class="report"><caption>People</caption>`) || !strings.Contains(table, "<th>Name</th>") ||
		!strings.Contains(table, "<td>Ada</td>") || !strings.Contains(table, "<td>Grace</td>") {
		t.Fatalf("table = %s", table)
	}
}

// A node saved with every default left out generates a template, and an
// expression in the template stays one.
func TestHTMLTemplateImportsWithItsExpression(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.html", 1, `{"html":"=<p>Hi {{ $json.name }}</p>"}`)
	if imported.Type != n8n.HTMLNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.HTMLNodeType)
	}
	output := executeImported(t, imported, nodes.HTMLExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{"name": "Ada"}}}})
	if output[0][0].JSON["html"] != "<p>Hi Ada</p>" {
		t.Fatalf("output = %#v", output[0][0].JSON)
	}
	if returned := exportedLegacyNode(t, result); returned.Parameters["html"] != "=<p>Hi {{ $json.name }}</p>" {
		t.Fatalf("exported html = %#v, want the =-prefixed template", returned.Parameters["html"])
	}
}

func TestHTMLRefusesAnOptionItDoesNotImplement(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.html", 1, `{"operation":"extractHtmlContent","options":{"stripComments":true}}`)
	if imported.Type != n8n.UnsupportedNodeType {
		t.Fatalf("imported as %q, want the unsupported placeholder", imported.Type)
	}
	blocking := blockingFor(result, "Legacy")
	if len(blocking) == 0 || !strings.Contains(blocking[0].Reason, "stripComments") {
		t.Fatalf("blocking = %#v, want a reason naming the option", blocking)
	}
}
