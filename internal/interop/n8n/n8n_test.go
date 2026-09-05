package n8n_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// registry is the real node catalogue, so an import is validated by exactly
// the compiler a hand-built workflow goes through.
func registry(t *testing.T) *node.Registry {
	t.Helper()
	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return catalogue
}

func importFixture(t *testing.T, fixture string) n8n.ImportResult {
	t.Helper()
	result, err := n8n.Import([]byte(fixture))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	return result
}

func nodeByName(document workflow.Document, name string) workflow.Node {
	for _, candidate := range document.Nodes {
		if candidate.Name == name {
			return candidate
		}
	}
	return workflow.Node{}
}

// --- Fixtures ---------------------------------------------------------------

const linearFixture = `{
  "name": "Linear",
  "nodes": [
    {"id": "a", "name": "When clicking Test", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
    {"id": "b", "name": "Edit Fields", "type": "n8n-nodes-base.set", "typeVersion": 3.4, "position": [220, 0],
     "parameters": {"assignments": {"assignments": [
       {"id": "1", "name": "status", "type": "string", "value": "ready"},
       {"id": "2", "name": "customer", "type": "string", "value": "={{ $json.name }}"}
     ]}}}
  ],
  "connections": {
    "When clicking Test": {"main": [[{"node": "Edit Fields", "type": "main", "index": 0}]]}
  }
}`

const branchingFixture = `{
  "name": "Branching",
  "nodes": [
    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
    {"id": "b", "name": "If", "type": "n8n-nodes-base.if", "typeVersion": 2, "position": [220, 0],
     "parameters": {"conditions": {"conditions": [
       {"id": "1", "leftValue": "={{ $json.tier }}", "rightValue": "gold",
        "operator": {"type": "string", "operation": "equals"}}
     ]}}},
    {"id": "c", "name": "Gold", "type": "n8n-nodes-base.set", "typeVersion": 3.4, "position": [440, -80],
     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "priority", "value": "high"}]}}},
    {"id": "d", "name": "Standard", "type": "n8n-nodes-base.set", "typeVersion": 3.4, "position": [440, 80],
     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "priority", "value": "normal"}]}}}
  ],
  "connections": {
    "Manual": {"main": [[{"node": "If", "type": "main", "index": 0}]]},
    "If": {"main": [
      [{"node": "Gold", "type": "main", "index": 0}],
      [{"node": "Standard", "type": "main", "index": 0}]
    ]}
  }
}`

const webhookFixture = `{
  "name": "Webhook and HTTP",
  "nodes": [
    {"id": "a", "name": "Webhook", "type": "n8n-nodes-base.webhook", "typeVersion": 2, "position": [0, 0],
     "parameters": {"path": "orders", "httpMethod": "POST", "responseMode": "responseNode", "authentication": "basicAuth"}},
    {"id": "b", "name": "HTTP Request", "type": "n8n-nodes-base.httpRequest", "typeVersion": 4.2, "position": [220, 0],
     "parameters": {"method": "GET", "url": "=https://api.test/orders/{{ $json.body.id }}",
       "sendHeaders": true,
       "headerParameters": {"parameters": [{"name": "Accept", "value": "application/json"}]}}},
    {"id": "c", "name": "Respond to Webhook", "type": "n8n-nodes-base.respondToWebhook", "typeVersion": 1.1, "position": [440, 0],
     "parameters": {"respondWith": "json", "responseBody": "={{ $json.body }}"}}
  ],
  "connections": {
    "Webhook": {"main": [[{"node": "HTTP Request", "type": "main", "index": 0}]]},
    "HTTP Request": {"main": [[{"node": "Respond to Webhook", "type": "main", "index": 0}]]}
  }
}`

const sqlFixture = `{
  "name": "SQL",
  "nodes": [
    {"id": "a", "name": "Schedule Trigger", "type": "n8n-nodes-base.scheduleTrigger", "typeVersion": 1.2, "position": [0, 0],
     "parameters": {"rule": {"interval": [{"field": "cronExpression", "expression": "0 9 * * 1-5"}]}}},
    {"id": "b", "name": "Postgres", "type": "n8n-nodes-base.postgres", "typeVersion": 2.4, "position": [220, 0],
     "parameters": {"operation": "executeQuery", "query": "SELECT id, name FROM customers WHERE tier = $1"},
     "credentials": {"postgres": {"id": "5", "name": "Prod DB"}}}
  ],
  "connections": {
    "Schedule Trigger": {"main": [[{"node": "Postgres", "type": "main", "index": 0}]]}
  }
}`

const unsupportedFixture = `{
  "name": "Has unsupported",
  "nodes": [
    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
    {"id": "b", "name": "Send Email", "type": "n8n-nodes-base.emailSend", "typeVersion": 2.1, "position": [220, 0],
     "parameters": {"toEmail": "ops@example.test", "subject": "Report"}}
  ],
  "connections": {
    "Manual": {"main": [[{"node": "Send Email", "type": "main", "index": 0}]]}
  }
}`

// --- Import -----------------------------------------------------------------

func TestImportLinearWorkflow(t *testing.T) {
	t.Parallel()

	result := importFixture(t, linearFixture)
	if len(result.Unsupported) != 0 {
		t.Fatalf("unsupported = %#v, want none for a fully supported workflow", result.Unsupported)
	}
	if result.Document.Name != "Linear" || len(result.Document.Nodes) != 2 || len(result.Document.Connections) != 1 {
		t.Fatalf("document = %#v, want two nodes and one connection", result.Document)
	}

	set := nodeByName(result.Document, "Edit Fields")
	assignments, _ := set.Parameters["assignments"].(map[string]any)
	if assignments["status"] != "ready" {
		t.Errorf("fixed assignment = %#v, want the plain string", assignments["status"])
	}
	// n8n's `=` prefix becomes KilasFlow's explicit expression marker; carrying
	// the prefix would leave the value looking like a literal.
	expression, ok := assignments["customer"].(map[string]any)
	if !ok || expression["mode"] != "expression" || expression["value"] != "{{ $json.name }}" {
		t.Errorf("expression assignment = %#v, want an explicit expression marker", assignments["customer"])
	}

	// Position is preserved so the imported canvas looks like the original.
	trigger := nodeByName(result.Document, "When clicking Test")
	if trigger.Position.X != 0 || nodeByName(result.Document, "Edit Fields").Position.X != 220 {
		t.Errorf("positions = %#v, want them carried across", result.Document.Nodes)
	}
}

func TestImportedWorkflowCompiles(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{
		"linear":    linearFixture,
		"branching": branchingFixture,
		"webhook":   webhookFixture,
	} {
		result := importFixture(t, fixture)
		document := result.Document
		document.ID = "wf_imported"
		// The existing compiler is the single validation authority; the adapter
		// deliberately does not add a second, weaker one.
		if _, err := workflow.Compile(document, registry(t)); err != nil {
			t.Errorf("%s fixture did not compile after import: %v", name, err)
		}
	}
}

func TestImportBranchingMapsOutputIndexesToNamedPorts(t *testing.T) {
	t.Parallel()

	result := importFixture(t, branchingFixture)
	byTarget := map[string]string{}
	for _, connection := range result.Document.Connections {
		byTarget[connection.Target.NodeID] = connection.Source.Port
	}
	// n8n identifies IF's branches positionally; KilasFlow names them, so
	// index 0 must become `true` rather than the generic `main` that other
	// nodes use.
	if byTarget["c"] != "true" {
		t.Errorf("gold branch port = %q, want true", byTarget["c"])
	}
	if byTarget["d"] != "false" {
		t.Errorf("standard branch port = %q, want false", byTarget["d"])
	}

	condition := nodeByName(result.Document, "If").Parameters["conditions"].([]any)[0].(map[string]any)
	if condition["field"] != "tier" || condition["operator"] != "equals" || condition["value"] != "gold" {
		t.Fatalf("condition = %#v, want the field path extracted from the n8n left value", condition)
	}
}

func TestImportWebhookAndHTTP(t *testing.T) {
	t.Parallel()

	result := importFixture(t, webhookFixture)

	webhook := nodeByName(result.Document, "Webhook")
	if webhook.Parameters["path"] != "orders" || webhook.Parameters["responseMode"] != "responseNode" {
		t.Fatalf("webhook = %#v, want the path and response mode carried", webhook.Parameters)
	}
	if webhook.Parameters["authentication"] != "basicAuth" {
		t.Errorf("authentication = %#v, want basicAuth carried", webhook.Parameters["authentication"])
	}

	request := nodeByName(result.Document, "HTTP Request")
	url, ok := request.Parameters["url"].(map[string]any)
	if !ok || url["value"] != "https://api.test/orders/{{ $json.body.id }}" {
		t.Fatalf("url = %#v, want the n8n expression translated", request.Parameters["url"])
	}
	headers, _ := request.Parameters["headers"].(map[string]any)
	if headers["Accept"] != "application/json" {
		t.Errorf("headers = %#v, want the named-value collection flattened", request.Parameters["headers"])
	}

	// A credential is an n8n identifier and means nothing here, so the import
	// must say so rather than leave a dangling reference.
	if !hasReason(result.Unsupported, "credential") {
		t.Errorf("unsupported = %#v, want the webhook credential named", result.Unsupported)
	}
}

func TestImportSQL(t *testing.T) {
	t.Parallel()

	result := importFixture(t, sqlFixture)

	schedule := nodeByName(result.Document, "Schedule Trigger")
	if schedule.Parameters["cron"] != "0 9 * * 1-5" {
		t.Errorf("cron = %#v, want the n8n cron expression carried", schedule.Parameters["cron"])
	}

	postgres := nodeByName(result.Document, "Postgres")
	if postgres.Type != "kilasflow.postgres" || postgres.Parameters["operation"] != "query" {
		t.Fatalf("postgres node = %#v, want a mapped query node", postgres)
	}
	if !strings.Contains(postgres.Parameters["statement"].(string), "FROM customers") {
		t.Errorf("statement = %#v, want the SQL carried", postgres.Parameters["statement"])
	}
	if !hasReason(result.Unsupported, "credential") {
		t.Errorf("unsupported = %#v, want the database credential named", result.Unsupported)
	}
}

func TestImportKeepsAnUnsupportedNodeVisibleAndUnrunnable(t *testing.T) {
	t.Parallel()

	result := importFixture(t, unsupportedFixture)

	// Visible: the node is in the document with its original identity.
	placeholder := nodeByName(result.Document, "Send Email")
	if placeholder.Type != n8n.UnsupportedNodeType {
		t.Fatalf("node type = %q, want the unsupported placeholder", placeholder.Type)
	}
	if placeholder.Parameters["originalType"] != "n8n-nodes-base.emailSend" {
		t.Errorf("originalType = %#v, want the n8n type preserved", placeholder.Parameters["originalType"])
	}
	// The capsule is a structured object, not JSON escaped inside JSON, and it
	// holds the whole source node rather than a summary of it.
	original, ok := placeholder.Parameters["original"].(map[string]any)
	if !ok {
		t.Fatalf("original = %#v, want a structured object", placeholder.Parameters["original"])
	}
	parameters, ok := original["parameters"].(map[string]any)
	if !ok || parameters["toEmail"] != "ops@example.test" {
		t.Errorf("original parameters = %#v, want the source parameters preserved", original["parameters"])
	}
	if original["type"] != "n8n-nodes-base.emailSend" {
		t.Errorf("original type = %#v, want the source type preserved", original["type"])
	}

	// Actionable: the message names the exact unsupported element.
	if !hasReason(result.Unsupported, "n8n-nodes-base.emailSend") {
		t.Fatalf("unsupported = %#v, want the node type named", result.Unsupported)
	}

	// Cannot silently execute as a different node: it fails compilation, so
	// the workflow can be edited but never activated or run.
	document := result.Document
	document.ID = "wf_imported"
	err := func() error {
		_, err := workflow.Compile(document, registry(t))
		return err
	}()
	if err == nil {
		t.Fatal("a workflow containing an unsupported node compiled")
	}
	if !strings.Contains(err.Error(), "emailSend") {
		t.Errorf("compile error = %v, want it to name the unsupported node", err)
	}
}

func TestImportRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"empty":       ``,
		"not json":    `not json at all`,
		"no nodes":    `{"name":"x","nodes":[],"connections":{}}`,
		"nodes wrong": `{"name":"x","nodes":"lots","connections":{}}`,
	} {
		if _, err := n8n.Import([]byte(payload)); err == nil {
			t.Errorf("%s input was accepted", name)
		}
	}
}

func TestImportRefusesDuplicateNodeNames(t *testing.T) {
	t.Parallel()

	// n8n keys connections by name, so duplicates make the graph ambiguous.
	// Importing something that routes wrongly would be worse than refusing.
	_, err := n8n.Import([]byte(`{
	  "name": "Ambiguous",
	  "nodes": [
	    {"id":"a","name":"Same","type":"n8n-nodes-base.manualTrigger","typeVersion":1},
	    {"id":"b","name":"Same","type":"n8n-nodes-base.set","typeVersion":3.4}
	  ],
	  "connections": {}
	}`))
	if err == nil || !strings.Contains(err.Error(), "named") {
		t.Fatalf("Import() = %v, want a duplicate-name rejection", err)
	}
}

func TestImportReportsAConnectionToAMissingNode(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Dangling",
	  "nodes": [{"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1}],
	  "connections": {"Manual": {"main": [[{"node":"Gone","type":"main","index":0}]]}}
	}`))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(result.Document.Connections) != 0 {
		t.Fatalf("connections = %#v, want the dangling edge dropped", result.Document.Connections)
	}
	if !hasReason(result.Unsupported, "Gone") {
		t.Errorf("unsupported = %#v, want the missing target named", result.Unsupported)
	}
}

func TestImportReportsAnUnsupportedIFOperator(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Regex",
	  "nodes": [{"id":"b","name":"If","type":"n8n-nodes-base.if","typeVersion":2,
	    "parameters":{"conditions":{"conditions":[
	      {"leftValue":"={{ $json.email }}","rightValue":".*@example",
	       "operator":{"type":"string","operation":"regex"}}]}}}],
	  "connections": {}
	}`))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if !hasReason(result.Unsupported, "regex") {
		t.Fatalf("unsupported = %#v, want the exact operator named", result.Unsupported)
	}
}

// --- Export -----------------------------------------------------------------

func TestExportProducesN8NShape(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, linearFixture)
	exported, err := n8n.Export(imported.Document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if exported.Document.Name != "Linear" || len(exported.Document.Nodes) != 2 {
		t.Fatalf("export = %#v, want both nodes", exported.Document)
	}
	// n8n keys connections by node name, not ID.
	targets := exported.Document.Connections["When clicking Test"]["main"]
	if len(targets) != 1 || len(targets[0]) != 1 || targets[0][0].Node != "Edit Fields" {
		t.Fatalf("connections = %#v, want a name-keyed edge", exported.Document.Connections)
	}

	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("marshal export = %v", err)
	}
	// The `=` prefix must come back, or n8n would treat the expression as a
	// literal string.
	if !strings.Contains(string(encoded), `"={{ $json.name }}"`) {
		t.Errorf("export = %s, want the n8n expression prefix restored", encoded)
	}
}

func TestRoundTripPreservesSupportedGraphSemantics(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{
		"linear":    linearFixture,
		"branching": branchingFixture,
		"webhook":   webhookFixture,
	} {
		first := importFixture(t, fixture)
		exported, err := n8n.Export(first.Document)
		if err != nil {
			t.Fatalf("%s: Export() error = %v", name, err)
		}
		encoded, err := json.Marshal(exported.Document)
		if err != nil {
			t.Fatalf("%s: marshal = %v", name, err)
		}
		second, err := n8n.Import(encoded)
		if err != nil {
			t.Fatalf("%s: re-import error = %v", name, err)
		}

		if len(second.Document.Nodes) != len(first.Document.Nodes) {
			t.Errorf("%s: nodes %d → %d, want the count preserved", name, len(first.Document.Nodes), len(second.Document.Nodes))
		}
		if len(second.Document.Connections) != len(first.Document.Connections) {
			t.Errorf("%s: connections %d → %d, want the graph preserved", name, len(first.Document.Connections), len(second.Document.Connections))
		}
		// Node types and the edges between them are the semantics that must
		// survive; IDs and cosmetic fields are explicitly not.
		if typesOf(first.Document) != typesOf(second.Document) {
			t.Errorf("%s: types %q → %q", name, typesOf(first.Document), typesOf(second.Document))
		}
		if edgesOf(first.Document) != edgesOf(second.Document) {
			t.Errorf("%s: edges %q → %q", name, edgesOf(first.Document), edgesOf(second.Document))
		}
	}
}

func TestRoundTripKeepsAnUnsupportedNodeAsItsOriginalN8NNode(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, unsupportedFixture)
	exported, err := n8n.Export(imported.Document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	var placeholder n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Send Email" {
			placeholder = candidate
		}
	}
	// The node came from n8n and belongs there; exporting it as a KilasFlow
	// placeholder would make the round trip lossy for no reason.
	if placeholder.Type != "n8n-nodes-base.emailSend" {
		t.Fatalf("exported type = %q, want the original n8n type", placeholder.Type)
	}
	if placeholder.Parameters["toEmail"] != "ops@example.test" {
		t.Errorf("exported parameters = %#v, want the originals restored", placeholder.Parameters)
	}
	if !hasLossyReason(exported.Lossy, "unsupported") {
		t.Errorf("lossy = %#v, want the round trip declared", exported.Lossy)
	}
}

func TestExportNamesWhatItCannotCarry(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Native only",
		Nodes: []workflow.Node{
			{ID: "a", Name: "Manual", Type: "kilasflow.manual", TypeVersion: 1},
			// A Code node has no n8n-Go equivalent in the advertised subset.
			{ID: "b", Name: "Code", Type: "kilasflow.code", TypeVersion: 1, Parameters: map[string]any{"code": "return items, nil"}},
			{ID: "c", Name: "Query", Type: "kilasflow.sqlite", TypeVersion: 1,
				Parameters:  map[string]any{"operation": "query", "statement": "SELECT 1"},
				Credentials: map[string]string{"sqlite": "cred-1"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "a", Port: "main"}, Target: workflow.Endpoint{NodeID: "b", Port: "main"}},
		},
		Settings: map[string]any{},
	}

	exported, err := n8n.Export(document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if !hasLossyReason(exported.Lossy, "kilasflow.code") {
		t.Errorf("lossy = %#v, want the Code node named", exported.Lossy)
	}
	// SQLite has no first-class n8n node, and a connection to an omitted node
	// must be dropped with it rather than left dangling.
	if !hasLossyReason(exported.Lossy, "kilasflow.sqlite") {
		t.Errorf("lossy = %#v, want the SQLite node named", exported.Lossy)
	}
	if !hasLossyReason(exported.Lossy, "connection") {
		t.Errorf("lossy = %#v, want the orphaned connection named", exported.Lossy)
	}
	for _, exportedNode := range exported.Document.Nodes {
		if exportedNode.Name == "Code" || exportedNode.Name == "Query" {
			t.Errorf("an unsupported node was exported anyway: %#v", exportedNode)
		}
	}
}

func TestSupportedMappingsAreAdvertisedExplicitly(t *testing.T) {
	t.Parallel()

	mappings := n8n.SupportedMappings()
	if len(mappings) < 8 {
		t.Fatalf("mappings = %#v, want the advertised subset", mappings)
	}
	joined := strings.Join(mappings, "\n")
	for _, expected := range []string{
		"n8n-nodes-base.manualTrigger", "n8n-nodes-base.set", "n8n-nodes-base.if",
		"n8n-nodes-base.httpRequest", "n8n-nodes-base.webhook", "n8n-nodes-base.postgres",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("mappings do not advertise %q", expected)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func hasReason(issues []n8n.Unsupported, needle string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Reason, needle) || strings.Contains(issue.Type, needle) {
			return true
		}
	}
	return false
}

func hasLossyReason(issues []n8n.Lossy, needle string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Reason, needle) || strings.Contains(issue.Field, needle) {
			return true
		}
	}
	return false
}

func typesOf(document workflow.Document) string {
	names := make([]string, 0, len(document.Nodes))
	for _, candidate := range document.Nodes {
		names = append(names, candidate.Name+":"+candidate.Type)
	}
	sortStrings(names)
	return strings.Join(names, ",")
}

func edgesOf(document workflow.Document) string {
	nameByID := map[string]string{}
	for _, candidate := range document.Nodes {
		nameByID[candidate.ID] = candidate.Name
	}
	edges := make([]string, 0, len(document.Connections))
	for _, connection := range document.Connections {
		edges = append(edges, nameByID[connection.Source.NodeID]+"."+connection.Source.Port+
			"->"+nameByID[connection.Target.NodeID]+"."+connection.Target.Port)
	}
	sortStrings(edges)
	return strings.Join(edges, ",")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// TestMirroredNodeTypesMatchTheNodePack pins the constants the adapter mirrors
// from the node pack. They are duplicated rather than imported so the adapter
// does not depend on the pack, and a silent drift between them would send
// imports to a node type nothing registers.
func TestMirroredNodeTypesMatchTheNodePack(t *testing.T) {
	t.Parallel()

	for name, pair := range map[string][2]string{
		"unsupported": {n8n.UnsupportedNodeType, nodes.UnsupportedNodeType},
		"sticky note": {n8n.StickyNoteNodeType, nodes.StickyNoteNodeType},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s node type: adapter has %q, node pack has %q", name, pair[0], pair[1])
		}
	}
}

// stickyFixture is a workflow whose only unmapped node is an annotation. Before
// the sticky note existed this could not be activated at all: the placeholder
// declared main ports it never used, so it tripped the missing-input and
// disconnected-from-trigger checks as well as its own always-fails validator.
const stickyFixture = `{
  "name": "Annotated",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
     "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"stage","value":"done","type":"string"}]}}},
    {"id":"c","name":"Sticky Note","type":"n8n-nodes-base.stickyNote","typeVersion":1,"position":[0,-160],
     "parameters":{"content":"## Why this exists","height":300,"width":420,"color":6}}
  ],
  "connections": {"Manual": {"main": [[{"node":"Edit","type":"main","index":0}]]}}
}`

// switchFixture is an unmapped node wired on three separate outputs. Its
// branches must stay distinguishable through an import and an export.
const switchFixture = `{
  "name": "Three ways",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"Route","type":"n8n-nodes-base.switch","typeVersion":3,"position":[220,0],"parameters":{"rules":{}}},
    {"id":"c","name":"First","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[440,-120],"parameters":{}},
    {"id":"d","name":"Second","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[440,0],"parameters":{}},
    {"id":"e","name":"Third","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[440,120],"parameters":{}}
  ],
  "connections": {
    "Manual": {"main": [[{"node":"Route","type":"main","index":0}]]},
    "Route": {"main": [
      [{"node":"First","type":"main","index":0}],
      [{"node":"Second","type":"main","index":0}],
      [{"node":"Third","type":"main","index":0}]
    ]}
  }
}`

// capsuleFixture carries every field the placeholder must preserve.
const capsuleFixture = `{
  "name": "Full node",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"Send Email","type":"n8n-nodes-base.emailSend","typeVersion":2.1,"position":[220,0],
     "parameters":{"toEmail":"ops@example.test"},
     "credentials":{"smtp":{"id":"7","name":"Company SMTP"}},
     "disabled":true,
     "notes":"Only fires out of hours",
     "webhookId":"3f2a1b0c-dead-4bee-9f00-0d15ea5eb00b",
     "continueOnFail":true,
     "retryOnFail":true,
     "maxTries":5,
     "waitBetweenTries":2500,
     "alwaysOutputData":true,
     "executeOnce":true,
     "onError":"continueErrorOutput"}
  ],
  "connections": {"Manual": {"main": [[{"node":"Send Email","type":"main","index":0}]]}}
}`

// TestStickyNoteImportsAndActivates is the property that unblocks every real
// imported workflow: Sticky Note is the most deployed node in n8n, and while it
// became an always-failing placeholder no annotated workflow could be activated.
func TestStickyNoteImportsAndActivates(t *testing.T) {
	t.Parallel()

	result := importFixture(t, stickyFixture)
	note := nodeByName(result.Document, "Sticky Note")
	if note.Type != n8n.StickyNoteNodeType {
		t.Fatalf("sticky note type = %q, want %q", note.Type, n8n.StickyNoteNodeType)
	}
	if note.Parameters["content"] != "## Why this exists" {
		t.Errorf("content = %#v, want the annotation preserved", note.Parameters["content"])
	}
	for key, want := range map[string]float64{"height": 300, "width": 420, "color": 6} {
		if got, _ := note.Parameters[key].(float64); got != want {
			t.Errorf("%s = %#v, want %v", key, note.Parameters[key], want)
		}
	}
	// An annotation is not an unsupported element; reporting it as one would
	// train users to ignore the list.
	if hasReason(result.Unsupported, "stickyNote") {
		t.Errorf("unsupported = %#v, want the sticky note not reported as unsupported", result.Unsupported)
	}

	document := result.Document
	document.ID = "wf_sticky"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("an annotated workflow must compile: %v", err)
	}
}

// TestStickyNoteRoundTrips proves the annotation survives an export, so a
// workflow edited here and taken back to n8n keeps its documentation.
func TestStickyNoteRoundTrips(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, stickyFixture)
	exported, err := n8n.Export(imported.Document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var note n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Sticky Note" {
			note = candidate
		}
	}
	if note.Type != "n8n-nodes-base.stickyNote" {
		t.Fatalf("exported type = %q, want the n8n sticky note", note.Type)
	}
	if note.Parameters["content"] != "## Why this exists" {
		t.Errorf("exported content = %#v, want the annotation preserved", note.Parameters["content"])
	}
}

// TestPlaceholderKeepsItsBranchesDistinct is the export bug the ticket names: a
// three-output node whose every branch was silently rewired onto n8n output 0.
func TestPlaceholderKeepsItsBranchesDistinct(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, switchFixture)
	placeholder := nodeByName(imported.Document, "Route")
	if placeholder.Type != n8n.UnsupportedNodeType {
		t.Fatalf("Route type = %q, want the placeholder", placeholder.Type)
	}
	// Three used outputs need a placeholder that declares at least three.
	if placeholder.TypeVersion < 3 {
		t.Errorf("placeholder arity = %d, want at least 3 for a three-output node", placeholder.TypeVersion)
	}

	// Import must record the three branches as distinct ports.
	ports := map[string]string{}
	for _, connection := range imported.Document.Connections {
		if connection.Source.NodeID == placeholder.ID {
			ports[connection.Target.NodeID] = connection.Source.Port
		}
	}
	if len(ports) != 3 {
		t.Fatalf("placeholder outgoing ports = %#v, want three", ports)
	}
	distinct := map[string]bool{}
	for _, port := range ports {
		distinct[port] = true
	}
	if len(distinct) != 3 {
		t.Errorf("placeholder ports = %#v, want three distinct ports", ports)
	}

	// And export must put them back on three different n8n output slots.
	exported, err := n8n.Export(imported.Document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	slots := exported.Document.Connections["Route"]["main"]
	if len(slots) != 3 {
		t.Fatalf("exported output slots = %d, want 3", len(slots))
	}
	for index, targets := range slots {
		if len(targets) != 1 {
			t.Errorf("output slot %d has %d targets, want exactly 1", index, len(targets))
		}
	}
	seen := map[string]bool{}
	for _, targets := range slots {
		for _, target := range targets {
			seen[target.Node] = true
		}
	}
	for _, want := range []string{"First", "Second", "Third"} {
		if !seen[want] {
			t.Errorf("exported connections lost the branch to %q", want)
		}
	}
}

// TestPlaceholderCapsuleRoundTripsEveryField is the lossless half of the
// ticket: a node imported as unsupported and exported back must return whole.
func TestPlaceholderCapsuleRoundTripsEveryField(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, capsuleFixture)
	exported, err := n8n.Export(imported.Document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var returned n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Send Email" {
			returned = candidate
		}
	}
	if returned.Type != "n8n-nodes-base.emailSend" {
		t.Fatalf("exported type = %q, want the original n8n type", returned.Type)
	}
	if returned.TypeVersion != 2.1 {
		t.Errorf("exported typeVersion = %v, want 2.1", returned.TypeVersion)
	}
	if returned.Parameters["toEmail"] != "ops@example.test" {
		t.Errorf("exported parameters = %#v, want them preserved", returned.Parameters)
	}
	if len(returned.Credentials) == 0 {
		t.Error("exported credentials were lost; a placeholder must return its bindings")
	}
	if !returned.Disabled {
		t.Error("exported disabled flag was lost")
	}
	if returned.Notes != "Only fires out of hours" {
		t.Errorf("exported notes = %q, want them preserved", returned.Notes)
	}
	if returned.WebhookID != "3f2a1b0c-dead-4bee-9f00-0d15ea5eb00b" {
		t.Errorf("exported webhookId = %q, want it preserved", returned.WebhookID)
	}
	for name, check := range map[string]bool{
		"continueOnFail":   returned.ContinueOnFail,
		"retryOnFail":      returned.RetryOnFail,
		"alwaysOutputData": returned.AlwaysOutputData,
		"executeOnce":      returned.ExecuteOnce,
	} {
		if !check {
			t.Errorf("exported %s was lost", name)
		}
	}
	if returned.MaxTries != 5 {
		t.Errorf("exported maxTries = %v, want 5", returned.MaxTries)
	}
	if returned.WaitBetweenTries != 2500 {
		t.Errorf("exported waitBetweenTries = %v, want 2500", returned.WaitBetweenTries)
	}
	if returned.OnError != "continueErrorOutput" {
		t.Errorf("exported onError = %q, want it preserved", returned.OnError)
	}
}

// TestPlaceholderStillRefusesToCompile guards the contract this ticket extends
// without weakening: the placeholder is still unrunnable and still says why.
func TestPlaceholderStillRefusesToCompile(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, switchFixture)
	document := imported.Document
	document.ID = "wf_switch"
	_, err := workflow.Compile(document, registry(t))
	if err == nil {
		t.Fatal("a workflow containing an unsupported placeholder must not compile")
	}
	if !strings.Contains(err.Error(), "n8n-nodes-base.switch") {
		t.Errorf("compile error = %v, want it to name the original n8n type", err)
	}
}

// TestPlaceholderArityFamilyMatchesTheNodePack pins the arity family the
// adapter mirrors. If the node pack registered a different set, imports would
// select a version nothing has registered.
func TestPlaceholderArityFamilyMatchesTheNodePack(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	for _, arity := range nodes.UnsupportedArities {
		definition, found := catalogue.Get(nodes.UnsupportedNodeType, arity)
		if !found {
			t.Fatalf("no placeholder registered at arity %d", arity)
		}
		if len(definition.Outputs) != arity || len(definition.Inputs) != arity {
			t.Errorf("placeholder arity %d has %d inputs and %d outputs", arity, len(definition.Inputs), len(definition.Outputs))
		}
		// The port names the adapter derives from an index must be the ones
		// the definition declares, or the compiler rejects the edge.
		for index, port := range definition.Outputs {
			if got := n8n.OutputPortNameForTest(nodes.UnsupportedNodeType, index); got != port.Name {
				t.Errorf("arity %d output %d: adapter says %q, definition says %q", arity, index, got, port.Name)
			}
		}
		for index, port := range definition.Inputs {
			if got := n8n.InputPortNameForTest(nodes.UnsupportedNodeType, index); got != port.Name {
				t.Errorf("arity %d input %d: adapter says %q, definition says %q", arity, index, got, port.Name)
			}
		}
	}
}

// TestExportReadsTheLegacyStringCapsule proves workflows imported before the
// capsule became structured still export whole.
//
// They are already persisted with `original` as a JSON string, and reading only
// the new shape would silently return them stripped of their parameters — a
// regression nobody would notice until a customer re-exported an old import.
func TestExportReadsTheLegacyStringCapsule(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_legacy",
		Name:          "Legacy import",
		Nodes: []workflow.Node{
			{ID: "a", Name: "Manual", Type: "kilasflow.manual", TypeVersion: 1},
			{
				ID: "b", Name: "Send Email", Type: n8n.UnsupportedNodeType, TypeVersion: 1,
				Parameters: map[string]any{
					"originalType":        "n8n-nodes-base.emailSend",
					"originalTypeVersion": 2.1,
					// Exactly the shape the previous importer wrote.
					"original": `{"type":"n8n-nodes-base.emailSend","typeVersion":2.1,"parameters":{"toEmail":"ops@example.test"}}`,
				},
			},
		},
		Connections: []workflow.Connection{{
			ID: "a-b", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "a", Port: "main"},
			Target: workflow.Endpoint{NodeID: "b", Port: "main"},
		}},
		Settings: map[string]any{},
	}

	exported, err := n8n.Export(document)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Send Email" {
			continue
		}
		if node.Type != "n8n-nodes-base.emailSend" {
			t.Errorf("exported type = %q, want the original n8n type", node.Type)
		}
		if node.Parameters["toEmail"] != "ops@example.test" {
			t.Errorf("exported parameters = %#v, want the legacy capsule read", node.Parameters)
		}
		return
	}
	t.Fatal("the placeholder was not exported at all")
}
