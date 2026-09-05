package n8n_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
	"github.com/kilaslabs/kilas-flow/packs/telegram"
	"github.com/kilaslabs/kilas-flow/packs/waha"
)

// registry is the real node catalogue, so an import is validated by exactly
// the compiler a hand-built workflow goes through.
func registry(t *testing.T) *node.Registry {
	t.Helper()
	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	// The generated packs are part of the catalogue an import resolves
	// against. Registering the real ones rather than a stand-in is the point:
	// every claim about WAHA here is a claim about strings a real template
	// contains, and a fixture written beside the mapping proves only that the
	// two agree with each other.
	executors := engine.NewRegistry()
	routes := routing.NewRegistry()
	triggers := nodepack.NewTriggerRegistry()
	for id, executor := range map[string]engine.Executor{
		routing.ExecutorID:         routing.NewExecutor(safehttp.DefaultPolicy(), routes, catalogue),
		nodepack.TriggerExecutorID: nodepack.NewTriggerExecutor(triggers, safehttp.DefaultPolicy()),
	} {
		if err := executors.Register(id, executor); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	if err := telegram.Register(catalogue, routes, executors, loadoptions.NewResolver(safehttp.DefaultPolicy(), 0)); err != nil {
		t.Fatalf("telegram.Register() error = %v", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: catalogue, Routes: routes, Triggers: triggers,
		Deliveries: webhook.NewRegistry(), Lifecycles: webhook.NewLifecycleRegistry(),
		Executors: executors, Options: loadoptions.NewResolver(safehttp.DefaultPolicy(), 0),
	}); err != nil {
		t.Fatalf("waha.Register() error = %v", err)
	}
	return catalogue
}

func importFixture(t *testing.T, fixture string) n8n.ImportResult {
	t.Helper()
	result, err := n8n.Import([]byte(fixture), registry(t))
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
		if _, err := n8n.Import([]byte(payload), registry(t)); err == nil {
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
	}`), registry(t))
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
	}`), registry(t))
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
	}`), registry(t))
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
	exported, err := n8n.Export(imported.Document, registry(t))
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
		exported, err := n8n.Export(first.Document, registry(t))
		if err != nil {
			t.Fatalf("%s: Export() error = %v", name, err)
		}
		encoded, err := json.Marshal(exported.Document)
		if err != nil {
			t.Fatalf("%s: marshal = %v", name, err)
		}
		second, err := n8n.Import(encoded, registry(t))
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
	exported, err := n8n.Export(imported.Document, registry(t))
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
			{ID: "a", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			// A Code node has no n8n-Go equivalent in the advertised subset.
			{ID: "b", Name: "Code", Type: "kilasflow.code", TypeVersion: workflow.V(1), Parameters: map[string]any{"code": "return items, nil"}},
			{ID: "c", Name: "Query", Type: "kilasflow.sqlite", TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"operation": "query", "statement": "SELECT 1"},
				Credentials: map[string]string{"sqlite": "cred-1"}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain, Source: workflow.Endpoint{NodeID: "a", Port: "main"}, Target: workflow.Endpoint{NodeID: "b", Port: "main"}},
		},
		Settings: map[string]any{},
	}

	exported, err := n8n.Export(document, registry(t))
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
	exported, err := n8n.Export(imported.Document, registry(t))
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
	if placeholder.TypeVersion.Compare(workflow.V(3)) < 0 {
		t.Errorf("placeholder arity = %s, want at least 3 for a three-output node", placeholder.TypeVersion)
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
	exported, err := n8n.Export(imported.Document, registry(t))
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
	exported, err := n8n.Export(imported.Document, registry(t))
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
		definition, found := catalogue.Get(nodes.UnsupportedNodeType, workflow.V(arity))
		if !found {
			t.Fatalf("no placeholder registered at arity %d", arity)
		}
		// Only the item channel is positional. The typed attachment ports are
		// declared alongside it so an imported AI edge has somewhere to land,
		// and n8n identifies those by kind rather than by index.
		mainOutputs := portsOfKind(definition.Outputs, workflow.ConnectionMain)
		mainInputs := portsOfKind(definition.Inputs, workflow.ConnectionMain)
		if len(mainOutputs) != arity || len(mainInputs) != arity {
			t.Errorf("placeholder arity %d has %d main inputs and %d main outputs", arity, len(mainInputs), len(mainOutputs))
		}
		// The port names the adapter derives from an index must be the ones
		// the definition declares, or the compiler rejects the edge.
		for index, port := range mainOutputs {
			if got := n8n.OutputPortNameForTest(nodes.UnsupportedNodeType, index); got != port.Name {
				t.Errorf("arity %d output %d: adapter says %q, definition says %q", arity, index, got, port.Name)
			}
		}
		for index, port := range mainInputs {
			if got := n8n.InputPortNameForTest(nodes.UnsupportedNodeType, index); got != port.Name {
				t.Errorf("arity %d input %d: adapter says %q, definition says %q", arity, index, got, port.Name)
			}
		}
		// Every AI kind must be reachable in both directions, or an imported
		// LangChain edge has nowhere to attach.
		for _, kind := range []workflow.ConnectionKind{
			workflow.ConnectionLanguageModel, workflow.ConnectionMemory, workflow.ConnectionTool,
		} {
			if len(portsOfKind(definition.Inputs, kind)) == 0 {
				t.Errorf("arity %d declares no %s input", arity, kind)
			}
			if len(portsOfKind(definition.Outputs, kind)) == 0 {
				t.Errorf("arity %d declares no %s output", arity, kind)
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
			{ID: "a", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{
				ID: "b", Name: "Send Email", Type: n8n.UnsupportedNodeType, TypeVersion: workflow.V(1),
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

	exported, err := n8n.Export(document, registry(t))
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

// TestImportPreservesTheSourceTypeVersion is the correctness the ticket exists
// for. Every imported node used to collapse to version 1 regardless of what n8n
// said, so a node written against Set 3.4 was configured against whatever
// version 1 happened to be.
func TestImportPreservesTheSourceTypeVersion(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Versions",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
	     "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"a","value":"b","type":"string"}]}}},
	    {"id":"c","name":"Call","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[440,0],
	     "parameters":{"url":"https://example.test/x","method":"GET"}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Edit","type":"main","index":0}]]},
	    "Edit": {"main": [[{"node":"Call","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	for name, want := range map[string]string{"Edit": "3.4", "Call": "4.2"} {
		node := nodeByName(result.Document, name)
		if got := node.TypeVersion.String(); got != want {
			t.Errorf("%s typeVersion = %s, want %s", name, got, want)
		}
	}

	// And the preserved version must still compile: only version 1 of each type
	// is registered today, so the registry has to resolve downward rather than
	// reject a version it does not have.
	document := result.Document
	document.ID = "wf_versions"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("a document carrying n8n's own typeVersion must compile: %v", err)
	}
}

// TestUnsupportedDiagnosticReportsTheExactVersion pins the truncation the
// ticket names: a node on version 4.2 was reported as version 4, which is a
// different node with a different parameter shape.
func TestUnsupportedDiagnosticReportsTheExactVersion(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Fractional unsupported",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Odd","type":"n8n-nodes-base.someUnmappedThing","typeVersion":4.2,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Odd","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	var reported workflow.TypeVersion
	for _, issue := range result.Unsupported {
		if issue.Type == "n8n-nodes-base.someUnmappedThing" {
			reported = issue.TypeVersion
		}
	}
	if got := reported.String(); got != "4.2" {
		t.Errorf("reported typeVersion = %s, want 4.2 — truncating to 4 names a different node", got)
	}
}

// TestImportHandlesAYYYYMMTypeVersion is WAHA's shape. 202502 fits in an int
// but not in a scheme where version 1 is the only version there is.
func TestImportHandlesAYYYYMMTypeVersion(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "WAHA-shaped",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"WAHA","type":"@devlikeapro/n8n-nodes-waha.WAHA","typeVersion":202502,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"WAHA","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	imported := nodeByName(result.Document, "WAHA")
	if imported.Type != n8n.WAHANodeType {
		t.Fatalf("type = %q, want the native WAHA node", imported.Type)
	}
	if got := imported.TypeVersion.String(); got != "202502" {
		t.Fatalf("typeVersion = %s, want the version the workflow was authored at", got)
	}
	// Both versions are registered, and the older one has to keep resolving to
	// itself rather than being pulled forward onto a different event order.
	older := importFixture(t, strings.ReplaceAll(fixture, "202502", "202409"))
	if got := nodeByName(older.Document, "WAHA").TypeVersion.String(); got != "202409" {
		t.Fatalf("typeVersion = %s, want 202409", got)
	}
}

// portsOfKind filters a declared port list to one connection kind.
func portsOfKind(ports []workflow.Port, kind workflow.ConnectionKind) []workflow.Port {
	var matched []workflow.Port
	for _, port := range ports {
		if port.Kind == kind {
			matched = append(matched, port)
		}
	}
	return matched
}

// langchainFixture is an n8n AI workflow: an agent with a chat model, a memory
// and two tools. Every one of those four edges is a typed channel, and every
// one of them used to be dropped — so an imported agent arrived wired to
// nothing and the AI parity work would have had nothing to test against.
//
// Note the casing: n8n writes ai_languageModel camel-cased after the
// underscore, and nothing in this adapter may normalise it.
const langchainFixture = `{
  "name": "Agent with tools",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":1.7,"position":[220,0],"parameters":{}},
    {"id":"c","name":"Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1,"position":[160,200],"parameters":{}},
    {"id":"d","name":"Window Memory","type":"@n8n/n8n-nodes-langchain.memoryBufferWindow","typeVersion":1.3,"position":[280,200],"parameters":{}},
    {"id":"e","name":"Weather","type":"@n8n/n8n-nodes-langchain.toolHttpRequest","typeVersion":1.1,"position":[400,200],"parameters":{}},
    {"id":"f","name":"Search","type":"@n8n/n8n-nodes-langchain.toolHttpRequest","typeVersion":1.1,"position":[520,200],"parameters":{}}
  ],
  "connections": {
    "Manual": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Window Memory": {"ai_memory": [[{"node":"AI Agent","type":"ai_memory","index":0}]]},
    "Weather": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]},
    "Search": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`

// TestImportKeepsAIConnections asserts the exact edge set a LangChain workflow
// must arrive with. n8n keys connections by the source node, and for a typed
// channel the source is the sub-node and the target is the agent — the same
// direction KilasFlow declares, which is why this is a kind mapping rather than
// a rewiring.
func TestImportKeepsAIConnections(t *testing.T) {
	t.Parallel()

	result := importFixture(t, langchainFixture)

	type edge struct {
		source string
		target string
		kind   workflow.ConnectionKind
	}
	nameByID := map[string]string{}
	for _, node := range result.Document.Nodes {
		nameByID[node.ID] = node.Name
	}
	got := map[edge]int{}
	for _, connection := range result.Document.Connections {
		got[edge{nameByID[connection.Source.NodeID], nameByID[connection.Target.NodeID], connection.Kind}]++
	}

	want := []edge{
		{"Manual", "AI Agent", workflow.ConnectionMain},
		{"Chat Model", "AI Agent", workflow.ConnectionLanguageModel},
		{"Window Memory", "AI Agent", workflow.ConnectionMemory},
		{"Weather", "AI Agent", workflow.ConnectionTool},
		{"Search", "AI Agent", workflow.ConnectionTool},
	}
	if len(got) != len(want) {
		t.Fatalf("imported %d distinct edges, want %d: %#v", len(got), len(want), got)
	}
	for _, expected := range want {
		if got[expected] != 1 {
			t.Errorf("edge %s -%s-> %s appeared %d times, want once", expected.source, expected.kind, expected.target, got[expected])
		}
	}

	// Every edge must name ports that exist on both endpoints with the matching
	// kind, or the compiler rejects it before the placeholder's own diagnostic
	// is reached.
	catalogue := registry(t)
	for _, connection := range result.Document.Connections {
		for _, endpoint := range []struct {
			nodeID string
			port   string
			inputs bool
		}{
			{connection.Source.NodeID, connection.Source.Port, false},
			{connection.Target.NodeID, connection.Target.Port, true},
		} {
			var version workflow.TypeVersion
			var nodeType string
			for _, node := range result.Document.Nodes {
				if node.ID == endpoint.nodeID {
					nodeType, version = node.Type, node.TypeVersion
				}
			}
			definition, found := catalogue.Lookup(nodeType, version)
			if !found {
				t.Fatalf("node type %q version %s is not registered", nodeType, version)
			}
			declared := definition.Outputs
			if endpoint.inputs {
				declared = definition.Inputs
			}
			var matched bool
			for _, port := range declared {
				if port.Name == endpoint.port && port.Kind == connection.Kind {
					matched = true
				}
			}
			if !matched {
				t.Errorf("port %q on %s does not exist with kind %s", endpoint.port, nodeType, connection.Kind)
			}
		}
	}
}

// TestAIConnectionsRoundTrip proves an imported AI graph goes back to n8n with
// its wiring intact, under the right channel key and with the sub-node as the
// source.
func TestAIConnectionsRoundTrip(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, langchainFixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	for _, expected := range []struct {
		source  string
		channel string
		target  string
	}{
		{"Chat Model", "ai_languageModel", "AI Agent"},
		{"Window Memory", "ai_memory", "AI Agent"},
		{"Weather", "ai_tool", "AI Agent"},
		{"Search", "ai_tool", "AI Agent"},
	} {
		slots := exported.Document.Connections[expected.source][expected.channel]
		if len(slots) == 0 {
			t.Errorf("%s has no %s connections after export", expected.source, expected.channel)
			continue
		}
		var found bool
		for _, targets := range slots {
			for _, target := range targets {
				if target.Node == expected.target && target.Type == expected.channel {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s -%s-> %s did not survive the round trip", expected.source, expected.channel, expected.target)
		}
	}

	// And the item channel is untouched by the change.
	if len(exported.Document.Connections["Manual"]["main"]) == 0 {
		t.Error("the main connection was lost")
	}
}

// TestUnknownConnectionKindNamesBothEndpoints pins the diagnostic. Saying only
// that a kind was dropped leaves a user with no way to find which two nodes
// stopped being joined.
func TestUnknownConnectionKindNamesBothEndpoints(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Unknown channel",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Vector Store","type":"@n8n/n8n-nodes-langchain.vectorStoreInMemory","typeVersion":1,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {
	    "Vector Store": {"ai_vectorStore": [[{"node":"Manual","type":"ai_vectorStore","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	var reason string
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "ai_vectorStore") {
			reason = issue.Reason
		}
	}
	if reason == "" {
		t.Fatalf("unsupported = %#v, want the dropped channel reported", result.Unsupported)
	}
	for _, want := range []string{"Vector Store", "Manual", "ai_vectorStore"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason = %q, want it to name %q", reason, want)
		}
	}
}

// n8n's documented enums for every value the exporter writes. A round trip that
// produces a document n8n rejects is worse than one that loses a field, because
// nothing reports it until someone tries the import on the other side.
var n8nEnums = map[string]map[string][]string{
	"n8n-nodes-base.webhook": {
		"responseMode": {"onReceived", "lastNode", "responseNode"},
		"httpMethod":   {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"},
	},
	"n8n-nodes-base.respondToWebhook": {
		"respondWith": {"text", "json", "binary", "redirect", "noData"},
	},
	"n8n-nodes-base.set": {
		"mode": {"manual", "raw"},
	},
	"n8n-nodes-base.merge": {
		"mode": {"append", "combine", "chooseBranch"},
	},
	"n8n-nodes-base.postgres": {
		"operation": {"executeQuery", "insert", "update", "upsert", "deleteTable", "select"},
	},
	"n8n-nodes-base.mySql": {
		"operation": {"executeQuery", "insert", "update", "upsert", "deleteTable", "select"},
	},
}

// TestExportWritesOnlyValidN8NEnumValues is the assertion the webhook bug
// escaped.
//
// KilasFlow calls n8n's `onReceived` mode `immediate`, and the exporter passed
// the KilasFlow word straight through — `defaultString` only substitutes when
// the value is empty, and "immediate" is not empty. The result was a document
// n8n refuses to import.
func TestExportWritesOnlyValidN8NEnumValues(t *testing.T) {
	t.Parallel()

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_enums",
		Name:          "Every exported enum",
		Nodes: []workflow.Node{
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "enums", "httpMethod": "POST", "responseMode": "immediate"}},
			{ID: "reply", Name: "Respond", Type: "kilasflow.respondToWebhook", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"responseCode": float64(200), "responseBody": "ok"}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "hook", Port: "main"},
			Target: workflow.Endpoint{NodeID: "reply", Port: "main"},
		}},
		Settings: map[string]any{},
	}

	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		enums, known := n8nEnums[node.Type]
		if !known {
			continue
		}
		for field, allowed := range enums {
			value, present := node.Parameters[field].(string)
			if !present {
				continue
			}
			if !slices.Contains(allowed, value) {
				t.Errorf("node %q exported %s=%q, which is not one of n8n's %v", node.Name, field, value, allowed)
			}
		}
	}
}

// TestWebhookResponseModeRoundTripsExactly pins the inverse property directly:
// whatever n8n wrote must come back out unchanged.
func TestWebhookResponseModeRoundTripsExactly(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"onReceived", "lastNode", "responseNode"} {
		fixture := `{
		  "name": "Mode ` + mode + `",
		  "nodes": [
		    {"id":"a","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
		     "parameters":{"path":"p","httpMethod":"POST","responseMode":"` + mode + `"}}
		  ],
		  "connections": {}
		}`
		imported := importFixture(t, fixture)
		exported, err := n8n.Export(imported.Document, registry(t))
		if err != nil {
			t.Fatalf("Export(%s) error = %v", mode, err)
		}
		if len(exported.Document.Nodes) == 0 {
			t.Fatalf("mode %s: the webhook was not exported", mode)
		}
		if got := exported.Document.Nodes[0].Parameters["responseMode"]; got != mode {
			t.Errorf("responseMode round-tripped %q as %#v", mode, got)
		}
	}
}

// annotatedFixture carries every workflow-level and node-level element the
// importer does not keep. It exists to prove the negative: nothing gets into
// the canonical document, or fails to, without a diagnostic saying so.
const annotatedFixture = `{
  "name": "Maximally annotated",
  "settings": {"executionOrder":"v1","timezone":"Asia/Jakarta","errorWorkflow":"wf_other"},
  "pinData": {"Edit": [{"json":{"pinned":true}}]},
  "meta": {"instanceId":"abc123","templateId":"42"},
  "staticData": {"lastRunToken":"xyz"},
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
     "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"a","value":"b","type":"string"}]}},
     "notes":"runs out of hours",
     "webhookId":"3f2a1b0c-dead-4bee-9f00-0d15ea5eb00b",
     "disabled":true,
     "continueOnFail":true,
     "retryOnFail":true,
     "maxTries":5,
     "waitBetweenTries":2500,
     "alwaysOutputData":true,
     "executeOnce":true,
     "onError":"continueErrorOutput"}
  ],
  "connections": {"Manual": {"main": [[{"node":"Edit","type":"main","index":0}]]}}
}`

// TestImportReportsEveryDroppedElement is the property this ticket exists for.
//
// FEAT-chxkvq shipped on the principle that an unsupported element is named
// rather than silently applied. The node-level *mapping* honoured that; the
// document and node metadata did not — settings, pinData and meta were read
// into the struct and discarded without a word, staticData was not even a
// field, and notes was parsed and never used.
func TestImportReportsEveryDroppedElement(t *testing.T) {
	t.Parallel()

	result := importFixture(t, annotatedFixture)

	reported := map[string]n8n.ImportIssue{}
	for _, issue := range result.Unsupported {
		if issue.Field != "" {
			reported[issue.Field] = issue
		}
	}

	for _, field := range []string{
		// Workflow level.
		"settings", "pinData", "meta", "staticData",
		// Node level.
		"notes", "webhookId", "disabled",
		// The error-handling fields that still have no equivalent. The other
		// four — continueOnFail, retryOnFail, maxTries and waitBetweenTries —
		// are now carried onto the canonical settings, which is asserted by
		// TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours.
		"alwaysOutputData", "executeOnce", "onError",
	} {
		issue, found := reported[field]
		if !found {
			t.Errorf("%q was dropped without a diagnostic", field)
			continue
		}
		// Dropped, not lossy: nothing about it survived, and calling it lossy
		// would imply the setting was applied in some reduced form.
		if issue.Severity != n8n.SeverityDropped {
			t.Errorf("%q reported severity %q, want %q", field, issue.Severity, n8n.SeverityDropped)
		}
		if issue.Reason == "" {
			t.Errorf("%q was reported with no reason", field)
		}
	}

	// A node-level diagnostic names its node; a workflow-level one does not.
	for _, field := range []string{"notes", "onError"} {
		if reported[field].NodeName != "Edit" {
			t.Errorf("%q did not name the node it came from: %#v", field, reported[field])
		}
	}
	for _, field := range []string{"settings", "pinData"} {
		if reported[field].NodeName != "" {
			t.Errorf("%q named a node, but it is a workflow-level element: %#v", field, reported[field])
		}
	}
}

// TestSeverityDistinguishesBlockingFromDropped pins the three-way distinction.
// A user needs to know what stops the workflow running, as against what was
// merely noted.
func TestSeverityDistinguishesBlockingFromDropped(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Mixed severities",
	  "pinData": {"x":[]},
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Odd","type":"n8n-nodes-base.someUnmappedThing","typeVersion":1,"position":[220,0],"parameters":{}},
	    {"id":"c","name":"Cron","type":"n8n-nodes-base.scheduleTrigger","typeVersion":1.2,"position":[440,0],
	     "parameters":{"rule":{"interval":[{"field":"seconds","secondsInterval":30}]}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Odd","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)

	counts := map[n8n.IssueSeverity]int{}
	for _, issue := range result.Unsupported {
		counts[issue.Severity]++
		if issue.Severity == "" {
			t.Errorf("issue reported with no severity: %#v", issue)
		}
	}
	if counts[n8n.SeverityBlocking] == 0 {
		t.Errorf("no blocking issue for a node type with no equivalent: %#v", result.Unsupported)
	}
	if counts[n8n.SeverityDropped] == 0 {
		t.Errorf("no dropped issue for pinData: %#v", result.Unsupported)
	}
	if counts[n8n.SeverityLossy] == 0 {
		t.Errorf("no lossy issue for the schedule interval that had to be reshaped: %#v", result.Unsupported)
	}

	// The blocking one is the node that cannot run, and it names the node.
	for _, issue := range result.Unsupported {
		if issue.Severity != n8n.SeverityBlocking {
			continue
		}
		if issue.NodeName != "Odd" || issue.Type != "n8n-nodes-base.someUnmappedThing" {
			t.Errorf("blocking issue = %#v, want it to name the unmapped node", issue)
		}
	}
}

// TestAnUnsupportedNodeDoesNotReportItsFieldsAsDropped keeps the reporting
// honest in the other direction. An unsupported node keeps the whole source
// node in its capsule and hands it back on export, so claiming its notes were
// dropped would say something was lost that was preserved.
func TestAnUnsupportedNodeDoesNotReportItsFieldsAsDropped(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Unsupported with metadata",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Odd","type":"n8n-nodes-base.someUnmappedThing","typeVersion":1,"position":[220,0],
	     "parameters":{},"notes":"kept in the capsule","retryOnFail":true}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Odd","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Odd" && (issue.Field == "notes" || issue.Field == "retryOnFail") {
			t.Errorf("reported %q as dropped for an unsupported node, but the capsule preserves it: %#v", issue.Field, issue)
		}
	}
}

// TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours is the other half
// of the reporting contract.
//
// The importer deliberately did not read these fields until the runner honoured
// them, because mapping them onto settings nothing read would have turned a
// silent drop into a documented lie. Now that it does, they are carried and
// their "dropped" diagnostics are gone — which is exactly how a later ticket
// retires a diagnostic.
func TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours(t *testing.T) {
	t.Parallel()

	result := importFixture(t, annotatedFixture)
	edit := nodeByName(result.Document, "Edit")

	for key, want := range map[string]any{
		"continueOnFail":   true,
		"retryOnFail":      true,
		"maxTries":         float64(5),
		"waitBetweenTries": float64(2500),
	} {
		if got := edit.Settings[key]; got != want {
			t.Errorf("settings[%q] = %#v, want %#v", key, got, want)
		}
	}

	// And they are no longer reported as dropped.
	for _, issue := range result.Unsupported {
		switch issue.Field {
		case "continueOnFail", "retryOnFail", "maxTries", "waitBetweenTries":
			t.Errorf("%q is carried now, but still reported as dropped: %#v", issue.Field, issue)
		}
	}
}

// TestImportClampsAnOutOfRangeRetryBudget keeps an n8n workflow importable
// while still refusing to let one typo become thousands of calls.
func TestImportClampsAnOutOfRangeRetryBudget(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Huge retry budget",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
	     "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"a","value":"b","type":"string"}]}},
	     "retryOnFail":true,"maxTries":9999,"waitBetweenTries":9999999}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Edit","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	edit := nodeByName(result.Document, "Edit")
	if got := edit.Settings["maxTries"]; got != float64(workflow.MaxRetryAttempts) {
		t.Errorf("maxTries = %#v, want it clamped to %d", got, workflow.MaxRetryAttempts)
	}
	if got := edit.Settings["waitBetweenTries"]; got != float64(workflow.MaxRetryWaitMilliseconds) {
		t.Errorf("waitBetweenTries = %#v, want it clamped to %d", got, workflow.MaxRetryWaitMilliseconds)
	}

	// And the clamped document must still compile, or the clamp achieved
	// nothing.
	document := result.Document
	document.ID = "wf_clamped"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("a clamped retry budget must still compile: %v", err)
	}
}

// The node type strings a WAHA workflow carries are matched byte for byte, and
// the capitalisation is not a typo: one package ships `WAHA` in caps for the
// action node and `wahaTrigger` in camel case for the trigger.
func TestBothWAHAPackageFormsImportOntoTheNativeNodes(t *testing.T) {
	t.Parallel()

	for _, sourceType := range []string{
		"@devlikeapro/n8n-nodes-waha.WAHA",
		"n8n-nodes-waha.WAHA",
	} {
		fixture := fmt.Sprintf(`{"name":"W","nodes":[{"id":"a","name":"W","type":%q,"typeVersion":202502,"position":[0,0],"parameters":{"resource":"Chatting","operation":"Send Text"}}],"connections":{}}`, sourceType)
		imported := nodeByName(importFixture(t, fixture).Document, "W")
		if imported.Type != n8n.WAHANodeType {
			t.Errorf("%s imported as %q, want %q", sourceType, imported.Type, n8n.WAHANodeType)
		}
		// The values an imported node selects with are the package's own
		// strings, carried through untouched.
		if imported.Parameters["resource"] != "Chatting" || imported.Parameters["operation"] != "Send Text" {
			t.Errorf("%s parameters = %#v, want resource and operation unchanged", sourceType, imported.Parameters)
		}
	}
	for _, sourceType := range []string{
		"@devlikeapro/n8n-nodes-waha.wahaTrigger",
		"n8n-nodes-waha.wahaTrigger",
	} {
		fixture := fmt.Sprintf(`{"name":"W","nodes":[{"id":"a","name":"W","type":%q,"typeVersion":202502,"position":[0,0],"parameters":{}}],"connections":{}}`, sourceType)
		imported := nodeByName(importFixture(t, fixture).Document, "W")
		if imported.Type != n8n.WAHATriggerNodeType {
			t.Errorf("%s imported as %q, want %q", sourceType, imported.Type, n8n.WAHATriggerNodeType)
		}
	}
}

// The mapping table and the pack cannot be allowed to disagree about the node
// type, and nothing in the compiler stops them.
func TestTheMappingTargetsAreThePacksOwnTypes(t *testing.T) {
	t.Parallel()

	if n8n.WAHANodeType != waha.NodeType {
		t.Fatalf("the importer maps onto %q but the pack registers %q", n8n.WAHANodeType, waha.NodeType)
	}
	if n8n.WAHATriggerNodeType != waha.TriggerNodeType {
		t.Fatalf("the importer maps onto %q but the pack registers %q", n8n.WAHATriggerNodeType, waha.TriggerNodeType)
	}
	advertised := strings.Join(n8n.SupportedMappings(), "\n")
	for _, pair := range []string{
		"@devlikeapro/n8n-nodes-waha.WAHA ↔ pack.waha",
		"@devlikeapro/n8n-nodes-waha.wahaTrigger ↔ pack.wahaTrigger",
		"n8n-nodes-waha.WAHA ↔ pack.waha",
		"n8n-nodes-waha.wahaTrigger ↔ pack.wahaTrigger",
	} {
		if !strings.Contains(advertised, pair) {
			t.Errorf("SupportedMappings() does not advertise %q; the subset has to be a written-down claim", pair)
		}
	}
}

// A template that wires two events to two branches has to keep both wires. The
// outputs are positional in n8n and named here, and the trigger has 26 of them.
func TestATriggersEventBranchesSurviveARoundTrip(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Two branches",
	  "nodes": [
	    {"id":"a","name":"WAHA Trigger","type":"@devlikeapro/n8n-nodes-waha.wahaTrigger","typeVersion":202502,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"On message","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],"parameters":{}},
	    {"id":"c","name":"On ack","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,200],"parameters":{}}
	  ],
	  "connections": {"WAHA Trigger": {"main": [
	    [],
	    [{"node":"On message","type":"main","index":0}],
	    [],
	    [],
	    [{"node":"On ack","type":"main","index":0}]
	  ]}}
	}`

	result := importFixture(t, fixture)
	byTarget := map[string]string{}
	for _, connection := range result.Document.Connections {
		byTarget[connection.Target.NodeID] = connection.Source.Port
	}
	// Index 1 is `message` and index 4 is `message.ack` in the 202502 table.
	if byTarget["b"] != "message" {
		t.Fatalf("the first branch landed on port %q, want message", byTarget["b"])
	}
	if byTarget["c"] != "message.ack" {
		t.Fatalf("the second branch landed on port %q, want message.ack", byTarget["c"])
	}

	// And back out, onto the same slots it came from.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	slots := exported.Document.Connections["WAHA Trigger"]["main"]
	if len(slots) < 5 {
		t.Fatalf("exported %d output slots, want at least five so index 4 exists", len(slots))
	}
	if len(slots[1]) != 1 || slots[1][0].Node != "On message" {
		t.Fatalf("slot 1 = %#v, want the message branch", slots[1])
	}
	if len(slots[4]) != 1 || slots[4][0].Node != "On ack" {
		t.Fatalf("slot 4 = %#v, want the ack branch", slots[4])
	}
	for _, index := range []int{0, 2, 3} {
		if len(slots[index]) != 0 {
			t.Fatalf("slot %d = %#v, want empty", index, slots[index])
		}
	}
}

// Export reproduces the original type string, capitalisation included, and the
// version the node is actually at.
func TestExportReproducesTheWAHATypeStringAndVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"202409", "202502"} {
		fixture := fmt.Sprintf(`{"name":"W","nodes":[{"id":"a","name":"W","type":"@devlikeapro/n8n-nodes-waha.WAHA","typeVersion":%s,"position":[0,0],"parameters":{}}],"connections":{}}`, version)
		imported := importFixture(t, fixture)
		exported, err := n8n.Export(imported.Document, registry(t))
		if err != nil {
			t.Fatalf("Export() error = %v", err)
		}
		if len(exported.Document.Nodes) != 1 {
			t.Fatalf("exported %d nodes, want one", len(exported.Document.Nodes))
		}
		node := exported.Document.Nodes[0]
		if node.Type != "@devlikeapro/n8n-nodes-waha.WAHA" {
			t.Fatalf("exported type = %q, want the scoped package's own capitalisation", node.Type)
		}
		if fmt.Sprintf("%.0f", node.TypeVersion) != version {
			t.Fatalf("exported typeVersion = %v, want %s", node.TypeVersion, version)
		}
	}
}

// An n8n credential id names a row in somebody else's database. Dropping it is
// right; dropping it silently leaves a node that looks configured and fails at
// run time.
func TestAnImportedCredentialReferenceIsNamedAndNotCarried(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "W",
	  "nodes": [{"id":"a","name":"W","type":"@devlikeapro/n8n-nodes-waha.WAHA","typeVersion":202502,"position":[0,0],
	    "parameters":{},"credentials":{"wahaApi":{"id":"7","name":"Production WAHA"}}}],
	  "connections": {}
	}`

	result := importFixture(t, fixture)
	imported := nodeByName(result.Document, "W")
	if len(imported.Credentials) != 0 {
		t.Fatalf("credentials = %#v, want the foreign reference not carried", imported.Credentials)
	}
	var reported string
	for _, issue := range result.Unsupported {
		if issue.Field == "credentials" {
			reported = issue.Reason
		}
	}
	if reported == "" {
		t.Fatal("the dropped credential was not reported; the node looks configured and is not")
	}
	for _, want := range []string{"wahaApi", "Production WAHA"} {
		if !strings.Contains(reported, want) {
			t.Errorf("report = %q, want it to name %q", reported, want)
		}
	}
	// The foreign identifier is never repeated back, so nothing downstream can
	// mistake it for one of ours.
	if strings.Contains(reported, `"7"`) {
		t.Errorf("report = %q, want no foreign credential id", reported)
	}
}

// A version this installation does not have imports at that version and says
// so, rather than resolving down onto a node with a different event order.
func TestAnUnknownVersionIsReportedRatherThanSilentlyResolved(t *testing.T) {
	t.Parallel()

	const fixture = `{"name":"W","nodes":[{"id":"a","name":"W","type":"@devlikeapro/n8n-nodes-waha.WAHA","typeVersion":209912,"position":[0,0],"parameters":{}}],"connections":{}}`

	result := importFixture(t, fixture)
	if got := nodeByName(result.Document, "W").TypeVersion.String(); got != "209912" {
		t.Fatalf("typeVersion = %s, want the version the workflow named", got)
	}
	var reported string
	for _, issue := range result.Unsupported {
		if issue.Field == "typeVersion" {
			reported = issue.Reason
		}
	}
	if reported == "" {
		t.Fatal("an unknown version imported silently")
	}
	if !strings.Contains(reported, "209912") {
		t.Errorf("report = %q, want it to name the version", reported)
	}
}

// Both Telegram types import onto native nodes, and the advertised subset says
// so — an interoperability claim that is not written down is not a claim.
func TestBothTelegramTypesImportOntoNativeNodes(t *testing.T) {
	t.Parallel()

	for sourceType, want := range map[string]string{
		"n8n-nodes-base.telegram":        n8n.TelegramNodeType,
		"n8n-nodes-base.telegramTrigger": n8n.TelegramTriggerNodeType,
	} {
		fixture := fmt.Sprintf(`{"name":"T","nodes":[{"id":"a","name":"T","type":%q,"typeVersion":1.2,"position":[0,0],"parameters":{"resource":"message","operation":"sendMessage","chatId":"=%s","text":"hi"}}],"connections":{}}`,
			sourceType, "{{ $json.message.chat.id }}")
		imported := nodeByName(importFixture(t, fixture).Document, "T")
		if imported.Type != want {
			t.Errorf("%s imported as %q, want %q", sourceType, imported.Type, want)
		}
		// n8n marks an expression with a leading `=`; KilasFlow with an
		// explicit marker. A chat id read from the trigger item is the single
		// most common parameter in a real Telegram workflow.
		marker, _ := imported.Parameters["chatId"].(map[string]any)
		if marker["mode"] != "expression" || marker["value"] != "{{ $json.message.chat.id }}" {
			t.Errorf("%s chatId = %#v, want the expression translated", sourceType, imported.Parameters["chatId"])
		}
		if imported.Parameters["operation"] != "sendMessage" {
			t.Errorf("%s operation = %#v, want it carried unchanged", sourceType, imported.Parameters["operation"])
		}
	}

	advertised := strings.Join(n8n.SupportedMappings(), "\n")
	for _, pair := range []string{
		"n8n-nodes-base.telegram ↔ pack.telegram",
		"n8n-nodes-base.telegramTrigger ↔ kilasflow.telegramTrigger",
	} {
		if !strings.Contains(advertised, pair) {
			t.Errorf("SupportedMappings() does not advertise %q", pair)
		}
	}
}

// Export puts the original type strings back, so a workflow that came from n8n
// can go home.
func TestExportReproducesTheTelegramTypeStrings(t *testing.T) {
	t.Parallel()

	const fixture = `{"name":"T","nodes":[
	  {"id":"a","name":"Trigger","type":"n8n-nodes-base.telegramTrigger","typeVersion":1.2,"position":[0,0],"parameters":{}},
	  {"id":"b","name":"Reply","type":"n8n-nodes-base.telegram","typeVersion":1.2,"position":[220,0],"parameters":{"resource":"message","operation":"sendMessage"}}
	],"connections":{"Trigger":{"main":[[{"node":"Reply","type":"main","index":0}]]}}}`

	imported := importFixture(t, fixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	types := map[string]string{}
	for _, node := range exported.Document.Nodes {
		types[node.Name] = node.Type
	}
	if types["Trigger"] != "n8n-nodes-base.telegramTrigger" || types["Reply"] != "n8n-nodes-base.telegram" {
		t.Fatalf("exported types = %#v, want the originals", types)
	}
	// And the wire survives, which needs the trigger's single main output to
	// map back onto slot zero.
	slots := exported.Document.Connections["Trigger"]["main"]
	if len(slots) != 1 || len(slots[0]) != 1 || slots[0][0].Node != "Reply" {
		t.Fatalf("connections = %#v, want the wire preserved", exported.Document.Connections)
	}
}

// A parameter n8n supports that KilasFlow does not carry is named rather than
// dropped: a workflow that looks identical and behaves differently is the worst
// outcome an importer has.
func TestATriggerParameterKilasFlowDoesNotCarryIsNamed(t *testing.T) {
	t.Parallel()

	const fixture = `{"name":"T","nodes":[{"id":"a","name":"T","type":"n8n-nodes-base.telegramTrigger","typeVersion":1.2,"position":[0,0],
	  "parameters":{"updates":["message"],"additionalFields":{"restrictToChatIds":"42","download":true}}}],"connections":{}}`

	result := importFixture(t, fixture)
	var reported string
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Field, "restrictToChatIds") {
			reported = issue.Reason
		}
	}
	if reported == "" {
		t.Fatal("a parameter KilasFlow reads under a different key was dropped in silence")
	}
	if !strings.Contains(reported, "chatIds") {
		t.Errorf("report = %q, want it to name where KilasFlow reads it", reported)
	}
	// What *is* carried stays carried.
	imported := nodeByName(result.Document, "T")
	additional, _ := imported.Parameters["additionalFields"].(map[string]any)
	if additional["download"] != true {
		t.Errorf("additionalFields = %#v, want the supported fields kept", additional)
	}
}
