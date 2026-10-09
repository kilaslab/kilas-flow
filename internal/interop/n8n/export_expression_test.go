package n8n_test

import (
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// webhookWithNestedExpressions is a Webhook whose response code and a header
// value deep inside `options` are both expressions.
const webhookWithNestedExpressions = `{
  "name": "Webhook expressions",
  "nodes": [
    {"id":"w","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
     "parameters":{"path":"orders","httpMethod":"POST","responseMode":"lastNode",
       "responseCode":"={{ $json.code }}",
       "options":{"responseHeaders":{"entries":[{"name":"X-Trace","value":"={{ $json.trace }}"},{"name":"X-Fixed","value":"static"}]}}}}
  ],
  "connections": {}
}`

// TestAnExpressionNestedInAnExportedParameterGoesBackAsAnEqualsString is
// BUG-8xz9t1: the tree walker used on export recursed into the expression
// marker `{mode: expression, value}` as if it were an ordinary object, so a
// marker anywhere below the top level of a parameter was written back as that
// object, which n8n reads as a literal and never evaluates.
func TestAnExpressionNestedInAnExportedParameterGoesBackAsAnEqualsString(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, webhookWithNestedExpressions)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	webhook := exportedNode(t, exported.Document, "Webhook")

	if got := webhook.Parameters["responseCode"]; got != "={{ $json.code }}" {
		t.Errorf("responseCode = %#v, want the =-prefixed string", got)
	}
	options, _ := webhook.Parameters["options"].(map[string]any)
	headers, _ := options["responseHeaders"].(map[string]any)
	entries, _ := headers["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("responseHeaders.entries = %#v, want both headers", headers["entries"])
	}
	first, _ := entries[0].(map[string]any)
	if first["value"] != "={{ $json.trace }}" {
		t.Errorf("nested header value = %#v, want the =-prefixed string, not a marker object", first["value"])
	}
	second, _ := entries[1].(map[string]any)
	if second["value"] != "static" || second["name"] != "X-Fixed" {
		t.Errorf("fixed header = %#v, want it unchanged", second)
	}
}

// TestAWebhookWithNestedExpressionsImportsTheSameAfterAnExport is the round
// trip the fix is for: import, export, import again, and the KilasFlow
// parameters are identical.
func TestAWebhookWithNestedExpressionsImportsTheSameAfterAnExport(t *testing.T) {
	t.Parallel()

	first := importFixture(t, webhookWithNestedExpressions)
	exported, err := n8n.Export(first.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("marshal export = %v", err)
	}
	second := importFixture(t, string(encoded))

	before, _ := json.Marshal(nodeByName(first.Document, "Webhook").Parameters)
	after, _ := json.Marshal(nodeByName(second.Document, "Webhook").Parameters)
	if string(before) != string(after) {
		t.Errorf("parameters after a round trip\n got %s\nwant %s", after, before)
	}
}
