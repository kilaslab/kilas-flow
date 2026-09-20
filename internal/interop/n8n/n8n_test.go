package n8n_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	rows := assignmentRows(t, set)
	if rows["status"]["value"] != "ready" {
		t.Errorf("fixed assignment = %#v, want the plain string", rows["status"]["value"])
	}
	// n8n's `=` prefix becomes KilasFlow's explicit expression marker; carrying
	// the prefix would leave the value looking like a literal.
	expression, ok := rows["customer"]["value"].(map[string]any)
	if !ok || expression["mode"] != "expression" || expression["value"] != "{{ $json.name }}" {
		t.Errorf("expression assignment = %#v, want an explicit expression marker", rows["customer"]["value"])
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
		// An import deliberately carries no credential, so an authenticated
		// trigger arrives visibly unbound and the compiler refuses it — which
		// is the point. Binding it here is what a user does next.
		bindImportedCredentials(&document)
		// The existing compiler is the single validation authority; the adapter
		// deliberately does not add a second, weaker one.
		if _, err := workflow.Compile(document, registry(t)); err != nil {
			t.Errorf("%s fixture did not compile after import: %v", name, err)
		}
	}

	// The whole LangChain cluster compiles too, once its models are bound
	// the way a user binds them after import: an import deliberately carries
	// no credential, so the models arrive visibly unbound. If any converter
	// emits a parameter the native validator refuses, this is where it shows.
	clustered := importFixture(t, langchainFullFixture)
	bound := clustered.Document
	bound.ID = "wf_langchain"
	for index, imported := range bound.Nodes {
		switch imported.Type {
		case "kilasflow.lmChatOpenAi":
			bound.Nodes[index].Credentials = map[string]string{"openAiApi": "cred-local"}
		case "kilasflow.lmChatOpenRouter":
			bound.Nodes[index].Credentials = map[string]string{"openRouterApi": "cred-local"}
		}
	}
	if _, err := workflow.Compile(bound, registry(t)); err != nil {
		t.Errorf("langchain fixture did not compile after import: %v", err)
	}
}

// bindImportedCredentials attaches a local credential to every node the
// compiler requires one for, which is the step a user takes after an import.
func bindImportedCredentials(document *workflow.Document) {
	for index, imported := range document.Nodes {
		switch imported.Type {
		case "kilasflow.webhook":
			switch imported.Parameters["authentication"] {
			case "basicAuth":
				document.Nodes[index].Credentials = map[string]string{"httpBasicAuth": "cred-local"}
			case "headerAuth":
				document.Nodes[index].Credentials = map[string]string{"httpHeaderAuth": "cred-local"}
			case "jwtAuth":
				document.Nodes[index].Credentials = map[string]string{"jwtAuth": "cred-local"}
			}
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

	// The condition travels as n8n wrote it: the left value stays an
	// expression, the operator keeps its type, and nothing is reduced to a
	// field path the evaluator would have to guess at.
	filter := nodeByName(result.Document, "If").Parameters["conditions"].(map[string]any)
	condition := filter["conditions"].([]any)[0].(map[string]any)
	left, _ := condition["leftValue"].(map[string]any)
	if left["mode"] != "expression" || left["value"] != "{{ $json.tier }}" {
		t.Fatalf("left value = %#v, want the expression carried", condition["leftValue"])
	}
	operator, _ := condition["operator"].(map[string]any)
	if operator["type"] != "string" || operator["operation"] != "equals" {
		t.Fatalf("operator = %#v, want the type and operation carried", condition["operator"])
	}
	if condition["rightValue"] != "gold" {
		t.Fatalf("right value = %#v, want it carried", condition["rightValue"])
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
	// The rule shape is identical on both sides, so a cron interval is carried
	// as the interval it is rather than flattened into a single string.
	rule, _ := schedule.Parameters["rule"].(map[string]any)
	intervals, _ := rule["interval"].([]any)
	if len(intervals) != 1 {
		t.Fatalf("rule = %#v, want the one interval carried", schedule.Parameters["rule"])
	}
	if first, _ := intervals[0].(map[string]any); first["expression"] != "0 9 * * 1-5" {
		t.Errorf("interval = %#v, want the n8n cron expression carried", intervals[0])
	}

	postgres := nodeByName(result.Document, "Postgres")
	// n8n's own operation value, carried across as itself. It used to be
	// flattened to "query" whatever it was, so an imported insert arrived as an
	// empty query.
	if postgres.Type != "kilasflow.postgres" || postgres.Parameters["operation"] != "executeQuery" {
		t.Fatalf("postgres node = %#v, want a mapped execute query", postgres)
	}
	if !strings.Contains(postgres.Parameters["query"].(string), "FROM customers") {
		t.Errorf("query = %#v, want the SQL carried", postgres.Parameters["query"])
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

// A regex condition used to be reported as unsupported and dropped. It is
// carried now, because the evaluator implements it — Go's RE2 makes a pattern
// from a document safe to run in a way a backtracking engine does not.
func TestImportCarriesARegexCondition(t *testing.T) {
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
	if hasReason(result.Unsupported, "regex") {
		t.Fatalf("unsupported = %#v, want a regex condition carried rather than dropped", result.Unsupported)
	}
	filter := nodeByName(result.Document, "If").Parameters["conditions"].(map[string]any)
	condition := filter["conditions"].([]any)[0].(map[string]any)
	operator, _ := condition["operator"].(map[string]any)
	if operator["operation"] != "regex" {
		t.Fatalf("operator = %#v, want the regex operation carried", condition["operator"])
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
	// SQLite has no first-class n8n node either.
	if !hasLossyReason(exported.Lossy, "kilasflow.sqlite") {
		t.Errorf("lossy = %#v, want the SQLite node named", exported.Lossy)
	}
	// Named and kept, not named and dropped. This test used to assert the
	// omission, and the omission was the defect: every edge touching an
	// omitted node went with it, so this three-node workflow came out as one
	// node and n8n showed a workflow that looked complete and did almost
	// nothing. Emitted under its own type, n8n does not recognise it and
	// refuses to run — which is the same information, where the user is.
	if len(exported.Document.Nodes) != len(document.Nodes) {
		t.Fatalf("exported %d nodes, want all %d", len(exported.Document.Nodes), len(document.Nodes))
	}
	byName := map[string]n8n.Node{}
	for _, exportedNode := range exported.Document.Nodes {
		byName[exportedNode.Name] = exportedNode
	}
	if byName["Code"].Type != "kilasflow.code" {
		t.Errorf("Code exported as %q, want its own type so n8n can say it does not know it", byName["Code"].Type)
	}
	if byName["Query"].Type != "kilasflow.sqlite" {
		t.Errorf("Query exported as %q, want its own type", byName["Query"].Type)
	}
	// And the edge survives, which is the whole point of keeping the node.
	targets, ok := exported.Document.Connections["Manual"]
	if !ok || len(targets["main"]) == 0 || len(targets["main"][0]) == 0 {
		t.Fatalf("connections = %#v, want the edge into the unsupported node kept", exported.Document.Connections)
	}
	if targets["main"][0][0].Node != "Code" {
		t.Errorf("the edge goes to %q, want Code", targets["main"][0][0].Node)
	}
	if hasLossyReason(exported.Lossy, "orphan") {
		t.Errorf("lossy = %#v, want no orphaned-connection report now that nothing is omitted", exported.Lossy)
	}
}

func TestSupportedMappingsAreAdvertisedExplicitly(t *testing.T) {
	t.Parallel()

	// The exact list, not a length and a handful of substrings. This is the
	// interoperability claim the product makes; a test that only checked six
	// of the pairs would let a seventh be removed, or a new one be added and
	// then quietly stop working, without anything going red.
	want := []string{
		"@aldinokemal2104/n8n-nodes-gowa.gowa ↔ kilasflow.httpRequest",
		"@devlikeapro/n8n-nodes-waha.WAHA ↔ pack.waha",
		"@devlikeapro/n8n-nodes-waha.wahaTrigger ↔ pack.wahaTrigger",
		"@n8n/n8n-nodes-langchain.agent ↔ kilasflow.agent",
		"@n8n/n8n-nodes-langchain.chainLlm ↔ kilasflow.chainLlm",
		"@n8n/n8n-nodes-langchain.lmChatOpenAi ↔ kilasflow.lmChatOpenAi",
		"@n8n/n8n-nodes-langchain.lmChatOpenRouter ↔ kilasflow.lmChatOpenRouter",
		"@n8n/n8n-nodes-langchain.memoryBufferWindow ↔ kilasflow.memoryBuffer",
		"@n8n/n8n-nodes-langchain.outputParserStructured ↔ kilasflow.outputParser",
		"@n8n/n8n-nodes-langchain.toolHttpRequest ↔ kilasflow.httpTool",
		"@n8n/n8n-nodes-langchain.toolWorkflow ↔ kilasflow.workflowTool",
		"n8n-nodes-base.aggregate ↔ kilasflow.aggregate",
		"n8n-nodes-base.code ↔ kilasflow.foreignCode",
		"n8n-nodes-base.dataTable ↔ kilasflow.datastore",
		"n8n-nodes-base.dataTableTool ↔ kilasflow.datastoreTool",
		"n8n-nodes-base.dateTime ↔ kilasflow.dateTime",
		"n8n-nodes-base.executeWorkflow ↔ kilasflow.executeWorkflow",
		"n8n-nodes-base.executeWorkflowTrigger ↔ kilasflow.executeWorkflowTrigger",
		"n8n-nodes-base.filter ↔ kilasflow.filter",
		"n8n-nodes-base.httpRequest ↔ kilasflow.httpRequest",
		"n8n-nodes-base.if ↔ kilasflow.if",
		"n8n-nodes-base.limit ↔ kilasflow.limit",
		"n8n-nodes-base.manualTrigger ↔ kilasflow.manual",
		"n8n-nodes-base.merge ↔ kilasflow.merge",
		"n8n-nodes-base.mySql ↔ kilasflow.mysql",
		"n8n-nodes-base.noOp ↔ kilasflow.noOp",
		"n8n-nodes-base.postgres ↔ kilasflow.postgres",
		"n8n-nodes-base.removeDuplicates ↔ kilasflow.removeDuplicates",
		"n8n-nodes-base.respondToWebhook ↔ kilasflow.respondToWebhook",
		"n8n-nodes-base.scheduleTrigger ↔ kilasflow.schedule",
		"n8n-nodes-base.set ↔ kilasflow.set",
		"n8n-nodes-base.sort ↔ kilasflow.sort",
		"n8n-nodes-base.splitInBatches ↔ kilasflow.loop",
		"n8n-nodes-base.splitOut ↔ kilasflow.splitOut",
		"n8n-nodes-base.stickyNote ↔ kilasflow.stickyNote",
		"n8n-nodes-base.summarize ↔ kilasflow.summarize",
		"n8n-nodes-base.switch ↔ kilasflow.switch",
		"n8n-nodes-base.telegram ↔ pack.telegram",
		"n8n-nodes-base.telegramTrigger ↔ kilasflow.telegramTrigger",
		"n8n-nodes-base.wait ↔ kilasflow.wait",
		"n8n-nodes-base.webhook ↔ kilasflow.webhook",
		"n8n-nodes-gowa.gowa ↔ kilasflow.httpRequest",
		"n8n-nodes-waha.WAHA ↔ pack.waha",
		"n8n-nodes-waha.wahaTrigger ↔ pack.wahaTrigger",
	}
	got := n8n.SupportedMappings()
	if len(got) != len(want) {
		t.Fatalf("mappings advertise %d pairs, want %d:\ngot  %s\nwant %s",
			len(got), len(want), strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("mapping %d = %q, want %q", index, got[index], want[index])
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
// switchFixture is a three-output node KilasFlow does *not* support, used to
// exercise the placeholder's arity handling.
//
// It was a Switch until Switch became a real node. The type matters only in
// that nothing maps it: what is under test is that a three-output placeholder
// keeps its three branches distinct, not anything about routing.
const switchFixture = `{
  "name": "Three ways",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"Route","type":"n8n-nodes-base.compareDatasets","typeVersion":2,"position":[220,0],"parameters":{"rules":{}}},
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
	if !strings.Contains(err.Error(), "n8n-nodes-base.compareDatasets") {
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
		"notes", "webhookId",
		// The error-handling fields that still have no equivalent. The rest —
		// continueOnFail, retryOnFail, maxTries, waitBetweenTries,
		// alwaysOutputData, executeOnce and onError="continueRegularOutput" —
		// are carried onto the canonical settings, which is asserted by
		// TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours.
		"onError",
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

	// A disabled node is not a dropped element: it is a node whose side effects
	// would fire, so the diagnostic blocks activation rather than noting a
	// setting that was left behind.
	if issue, found := reported["disabled"]; !found {
		t.Error("a disabled node was not reported")
	} else if issue.Severity != n8n.SeverityBlocking {
		t.Errorf("disabled reported severity %q, want %q", issue.Severity, n8n.SeverityBlocking)
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
	     "parameters":{"rule":{"interval":[{"field":"weeks","weeksInterval":2,"triggerAtDay":[1],"triggerAtHour":9}]}}}
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
	// Every fortnight has no cron equivalent, so it is imported as every week
	// and the difference is stated. An interval cron *can* express — every 30
	// seconds, every Monday — is carried exactly and says nothing, which is why
	// this fixture uses the one that cannot be.
	if counts[n8n.SeverityLossy] == 0 {
		t.Errorf("no lossy issue for the schedule interval with no cron equivalent: %#v", result.Unsupported)
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

// assignmentRows reads an imported Set node's ordered rows by name.
func assignmentRows(t *testing.T, node workflow.Node) map[string]map[string]any {
	t.Helper()
	wrapper, _ := node.Parameters["assignments"].(map[string]any)
	list, ok := wrapper["assignments"].([]any)
	if !ok {
		t.Fatalf("assignments = %#v, want the ordered list", node.Parameters["assignments"])
	}
	rows := make(map[string]map[string]any, len(list))
	for _, entry := range list {
		fields, _ := entry.(map[string]any)
		name, _ := fields["name"].(string)
		rows[name] = fields
	}
	return rows
}

// Order and type are what a map cannot hold, and they are what the round trip
// has to give back.
//
// The importer used to collapse assignments to `map[name]value`: two rows
// writing the same field became one, the order the author typed was lost the
// first time the document was saved, and the exporter hardcoded `"string"` so a
// boolean came home as text.
func TestSetAssignmentsKeepTheirOrderAndTypeThroughARoundTrip(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Typed",
	  "nodes": [{"id":"a","name":"Edit Fields","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[0,0],
	    "parameters":{"assignments":{"assignments":[
	      {"id":"r1","name":"zebra","type":"string","value":"last alphabetically, first in order"},
	      {"id":"r2","name":"count","type":"number","value":7},
	      {"id":"r3","name":"active","type":"boolean","value":true},
	      {"id":"r4","name":"tags","type":"array","value":["a","b"]},
	      {"id":"r5","name":"meta","type":"object","value":{"k":"v"}},
	      {"id":"r6","name":"zebra","type":"string","value":"the later row wins"}
	    ]}}}],
	  "connections": {}
	}`

	imported := importFixture(t, fixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	wrapper, _ := exported.Document.Nodes[0].Parameters["assignments"].(map[string]any)
	rows, _ := wrapper["assignments"].([]any)
	if len(rows) != 6 {
		t.Fatalf("exported %d rows, want all six — including both writes of the same field", len(rows))
	}

	type row struct{ name, declared string }
	got := make([]row, 0, len(rows))
	for _, entry := range rows {
		fields, _ := entry.(map[string]any)
		name, _ := fields["name"].(string)
		declared, _ := fields["type"].(string)
		got = append(got, row{name, declared})
	}
	want := []row{
		{"zebra", "string"}, {"count", "number"}, {"active", "boolean"},
		{"tags", "array"}, {"meta", "object"}, {"zebra", "string"},
	}
	for index, expected := range want {
		if got[index] != expected {
			t.Errorf("row %d = %+v, want %+v", index, got[index], expected)
		}
	}
	// The ids n8n minted come back as themselves, so a re-import in n8n is the
	// same document rather than a new one.
	first, _ := rows[0].(map[string]any)
	if first["id"] != "r1" {
		t.Errorf("first row id = %#v, want n8n's own", first["id"])
	}
}

// A document saved before assignments had order or types still imports, still
// exports and still means the same thing.
func TestASetNodeSavedWithTheOldFlatShapeStillRoundTrips(t *testing.T) {
	t.Parallel()

	legacy := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_legacy", Name: "Legacy",
		Nodes: []workflow.Node{{
			ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"assignments": map[string]any{"status": "ready", "count": float64(2)}},
		}},
		Settings: map[string]any{},
	}
	exported, err := n8n.Export(legacy, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	wrapper, _ := exported.Document.Nodes[0].Parameters["assignments"].(map[string]any)
	rows, _ := wrapper["assignments"].([]any)
	if len(rows) != 2 {
		t.Fatalf("exported %d rows from the flat shape, want two", len(rows))
	}
	// Alphabetical, which is the only stable order a map can offer.
	names := make([]string, 0, 2)
	for _, entry := range rows {
		fields, _ := entry.(map[string]any)
		name, _ := fields["name"].(string)
		names = append(names, name)
	}
	if strings.Join(names, ",") != "count,status" {
		t.Fatalf("names = %v, want the only stable order a map can give", names)
	}
}

// Every one of these used to become the unsupported placeholder, so a single
// Switch made an entire imported workflow unactivatable.
func TestTheFlowControlFamilyImportsAndExports(t *testing.T) {
	t.Parallel()

	for sourceType, want := range map[string]string{
		"n8n-nodes-base.switch":         n8n.SwitchNodeType,
		"n8n-nodes-base.filter":         n8n.FilterNodeType,
		"n8n-nodes-base.limit":          n8n.LimitNodeType,
		"n8n-nodes-base.noOp":           n8n.NoOpNodeType,
		"n8n-nodes-base.splitInBatches": n8n.LoopNodeType,
	} {
		advertised := strings.Join(n8n.SupportedMappings(), "\n")
		if !strings.Contains(advertised, sourceType+" ↔ "+want) {
			t.Errorf("SupportedMappings() does not advertise %s ↔ %s", sourceType, want)
		}
	}

	const fixture = `{
	  "name": "Flow",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Route","type":"n8n-nodes-base.switch","typeVersion":3.2,"position":[220,0],
	     "parameters":{"mode":"rules","rules":{"values":[
	       {"conditions":{"combinator":"and","options":{"caseSensitive":true},"conditions":[
	         {"leftValue":"={{ $json.tier }}","rightValue":"gold","operator":{"type":"string","operation":"equals"}}]},
	        "outputKey":"VIP","renameOutput":true},
	       {"conditions":{"combinator":"and","options":{"caseSensitive":true},"conditions":[
	         {"leftValue":"={{ $json.tier }}","rightValue":"silver","operator":{"type":"string","operation":"equals"}}]}}
	     ]},"options":{"fallbackOutput":"extra"}}},
	    {"id":"c","name":"Nothing","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[440,-120],"parameters":{}},
	    {"id":"d","name":"Few","type":"n8n-nodes-base.limit","typeVersion":1,"position":[440,0],
	     "parameters":{"maxItems":5,"keep":"lastItems"}},
	    {"id":"e","name":"Rest","type":"n8n-nodes-base.filter","typeVersion":2.2,"position":[440,120],
	     "parameters":{"conditions":{"combinator":"or","options":{"caseSensitive":false},"conditions":[
	       {"leftValue":"={{ $json.n }}","rightValue":3,"operator":{"type":"number","operation":"gt"}}]}}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Route","type":"main","index":0}]]},
	    "Route": {"main": [
	      [{"node":"Nothing","type":"main","index":0}],
	      [{"node":"Few","type":"main","index":0}],
	      [{"node":"Rest","type":"main","index":0}]
	    ]}
	  }
	}`

	result := importFixture(t, fixture)
	for name, want := range map[string]string{
		"Route": n8n.SwitchNodeType, "Nothing": n8n.NoOpNodeType,
		"Few": n8n.LimitNodeType, "Rest": n8n.FilterNodeType,
	} {
		if got := nodeByName(result.Document, name).Type; got != want {
			t.Errorf("%s imported as %q, want %q", name, got, want)
		}
	}

	// The three branches keep their own ports. n8n identifies an output
	// positionally and a rule that moved would move every wire below it, so the
	// port name is the index and the label is what carries the rename.
	ports := map[string]string{}
	for _, connection := range result.Document.Connections {
		if connection.Source.NodeID == "b" {
			ports[connection.Target.NodeID] = connection.Source.Port
		}
	}
	if ports["c"] != "0" || ports["d"] != "1" || ports["e"] != "2" {
		t.Fatalf("switch branches = %#v, want three distinct ports including the fallback", ports)
	}

	// Compiling proves the ports the rules produced are the ports the
	// connections were resolved against.
	document := result.Document
	document.ID = "wf_flow"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// And out again, onto the same slots.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	slots := exported.Document.Connections["Route"]["main"]
	if len(slots) != 3 {
		t.Fatalf("exported %d output slots, want three", len(slots))
	}
	for index, wanted := range []string{"Nothing", "Few", "Rest"} {
		if len(slots[index]) != 1 || slots[index][0].Node != wanted {
			t.Fatalf("slot %d = %#v, want %q", index, slots[index], wanted)
		}
	}
	var routed n8n.Node
	for _, exportedNode := range exported.Document.Nodes {
		if exportedNode.Name == "Route" {
			routed = exportedNode
		}
	}
	if routed.Type != "n8n-nodes-base.switch" {
		t.Fatalf("exported type = %q", routed.Type)
	}
	rules, _ := routed.Parameters["rules"].(map[string]any)
	values, _ := rules["values"].([]any)
	if len(values) != 2 {
		t.Fatalf("exported %d rules, want both", len(values))
	}
	first, _ := values[0].(map[string]any)
	if first["outputKey"] != "VIP" {
		t.Errorf("exported first rule = %#v, want the renamed output carried", first)
	}
}

// n8n's Merge used to have every mode rewritten to `append`, with an issue
// whose own words were "changes what this node does".
func TestMergeModesSurviveImport(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Join",
	  "nodes": [{"id":"a","name":"Join","type":"n8n-nodes-base.merge","typeVersion":3,"position":[0,0],
	    "parameters":{"mode":"combine","combineBy":"combineByFields","numberInputs":3,
	      "mergeByFields":{"values":[{"field1":"id","field2":"id"}]},"joinMode":"keepEverything"}}],
	  "connections": {}
	}`

	result := importFixture(t, fixture)
	merged := nodeByName(result.Document, "Join")
	for key, want := range map[string]any{
		"mode": "combine", "combineBy": "combineByFields",
		"numberInputs": float64(3), "fieldsToMatch": "id", "joinMode": "keepEverything",
	} {
		if merged.Parameters[key] != want {
			t.Errorf("%s = %#v, want %#v", key, merged.Parameters[key], want)
		}
	}
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "only appends") {
			t.Error("the importer still claims Merge only appends")
		}
	}
}

// Every data-shaping node used to import as the unsupported placeholder, so a
// single Sort blocked activation of the whole workflow.
func TestTheDataShapingFamilyImportsAndExports(t *testing.T) {
	t.Parallel()

	advertised := strings.Join(n8n.SupportedMappings(), "\n")
	for sourceType, want := range map[string]string{
		"n8n-nodes-base.aggregate":        n8n.AggregateNodeType,
		"n8n-nodes-base.splitOut":         n8n.SplitOutNodeType,
		"n8n-nodes-base.sort":             n8n.SortNodeType,
		"n8n-nodes-base.summarize":        n8n.SummarizeNodeType,
		"n8n-nodes-base.removeDuplicates": n8n.RemoveDuplicatesNodeType,
	} {
		if !strings.Contains(advertised, sourceType+" ↔ "+want) {
			t.Errorf("SupportedMappings() does not advertise %s ↔ %s", sourceType, want)
		}
	}

	const fixture = `{
	  "name": "Shape",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Split","type":"n8n-nodes-base.splitOut","typeVersion":1,"position":[220,0],
	     "parameters":{"fieldToSplitOut":"lines","include":"allOtherFields","options":{"destinationFieldName":"line"}}},
	    {"id":"c","name":"Order","type":"n8n-nodes-base.sort","typeVersion":1,"position":[440,0],
	     "parameters":{"type":"simple","sortFieldsUI":{"sortField":[
	       {"fieldName":"total","order":"descending"},{"fieldName":"sku","order":"ascending"}]}}},
	    {"id":"d","name":"Gather","type":"n8n-nodes-base.aggregate","typeVersion":1,"position":[660,0],
	     "parameters":{"aggregate":"aggregateIndividualFields",
	       "fieldsToAggregate":{"values":[{"fieldToAggregate":"sku"},{"fieldToAggregate":"total"}]}}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Split","type":"main","index":0}]]},
	    "Split": {"main": [[{"node":"Order","type":"main","index":0}]]},
	    "Order": {"main": [[{"node":"Gather","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	for name, want := range map[string]string{
		"Split": n8n.SplitOutNodeType, "Order": n8n.SortNodeType, "Gather": n8n.AggregateNodeType,
	} {
		if got := nodeByName(result.Document, name).Type; got != want {
			t.Errorf("%s imported as %q, want %q", name, got, want)
		}
	}
	// n8n's fixed collections become the comma-separated lists this product's
	// controls use, with the descending suffix carried.
	if got := nodeByName(result.Document, "Order").Parameters["sortFieldsUI"]; got != "total:desc,sku" {
		t.Errorf("sort keys = %#v, want the fields and their directions", got)
	}
	if got := nodeByName(result.Document, "Gather").Parameters["fieldsToAggregate"]; got != "sku,total" {
		t.Errorf("aggregate fields = %#v, want both", got)
	}
	if got := nodeByName(result.Document, "Split").Parameters["destinationFieldName"]; got != "line" {
		t.Errorf("destination = %#v, want the option lifted out", got)
	}

	document := result.Document
	document.ID = "wf_shape"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, exportedNode := range exported.Document.Nodes {
		if exportedNode.Name != "Order" {
			continue
		}
		wrapper, _ := exportedNode.Parameters["sortFieldsUi"].(map[string]any)
		if wrapper == nil {
			wrapper, _ = exportedNode.Parameters["sortFieldsUI"].(map[string]any)
		}
		fields, _ := wrapper["sortField"].([]any)
		first, _ := fields[0].(map[string]any)
		if first["fieldName"] != "total" || first["order"] != "descending" {
			t.Fatalf("exported first sort field = %#v, want the direction back", first)
		}
	}
}

// n8n's third Sort mode is a JavaScript comparator. Approximating it would sort
// by something the author did not write, so it is named instead.
func TestAJavaScriptSortComparatorIsRefusedRatherThanApproximated(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{"name":"S","nodes":[{"id":"a","name":"Order","type":"n8n-nodes-base.sort","typeVersion":1,"position":[0,0],"parameters":{"type":"code","code":"return 0"}}],"connections":{}}`)
	if !hasReason(result.Unsupported, "JavaScript") {
		t.Fatalf("unsupported = %#v, want the comparator named", result.Unsupported)
	}
}

// Removing items seen in previous executions needs durable per-workflow state.
func TestRemoveDuplicatesAcrossExecutionsIsNamedOnImport(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{"name":"D","nodes":[{"id":"a","name":"Dedupe","type":"n8n-nodes-base.removeDuplicates","typeVersion":2,"position":[0,0],"parameters":{"operation":"removeItemsSeenInPreviousExecutions"}}],"connections":{}}`)
	if !hasReason(result.Unsupported, "previous executions") {
		t.Fatalf("unsupported = %#v, want the missing capability named", result.Unsupported)
	}
	// And it imports as the local operation rather than as something that
	// cannot run at all.
	if got := nodeByName(result.Document, "Dedupe").Parameters["operation"]; got != "removeDuplicateInputItems" {
		t.Errorf("operation = %#v, want the local form", got)
	}
}

func TestTheTimeFamilyImportsAndExports(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Time family",
	  "nodes": [
	    {"id":"a","name":"Every weekday and evening","type":"n8n-nodes-base.scheduleTrigger","typeVersion":1.2,"position":[0,0],
	     "parameters":{"rule":{"interval":[
	       {"field":"days","daysInterval":1,"triggerAtHour":9,"triggerAtMinute":0},
	       {"field":"cronExpression","expression":"0 17 * * 1-5"}]}}},
	    {"id":"b","name":"Thirty days ago","type":"n8n-nodes-base.dateTime","typeVersion":2,"position":[220,0],
	     "parameters":{"operation":"subtractFromDate","magnitude":"={{ $json.created }}","duration":30,
	                   "timeUnit":"days","outputFieldName":"since","options":{"timezone":"Asia/Jakarta"}}},
	    {"id":"c","name":"Breathe","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[440,0],
	     "parameters":{"resume":"timeInterval","amount":5,"unit":"minutes"}}
	  ],
	  "connections": {"Every weekday and evening": {"main": [[{"node":"Thirty days ago","type":"main","index":0}]]},
	                  "Thirty days ago": {"main": [[{"node":"Breathe","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("a blocking issue for a family that is now mapped: %#v", issue)
		}
	}

	// Both intervals survive. The old importer returned on the first one
	// carrying a cron expression, so this workflow would have arrived running
	// only in the evening.
	schedule := nodeByName(result.Document, "Every weekday and evening")
	rule, _ := schedule.Parameters["rule"].(map[string]any)
	intervals, _ := rule["interval"].([]any)
	if len(intervals) != 2 {
		t.Fatalf("intervals = %#v, want both carried", rule["interval"])
	}

	date := nodeByName(result.Document, "Thirty days ago")
	if date.Type != n8n.DateTimeNodeType {
		t.Fatalf("date node = %q, want it mapped rather than a placeholder", date.Type)
	}
	if date.Parameters["operation"] != "subtractFromDate" || date.Parameters["outputField"] != "since" {
		t.Errorf("date parameters = %#v, want the operation and output field carried", date.Parameters)
	}
	if date.Parameters["unit"] != "days" || date.Parameters["timezone"] != "Asia/Jakarta" {
		t.Errorf("date parameters = %#v, want the unit and zone carried", date.Parameters)
	}

	wait := nodeByName(result.Document, "Breathe")
	if wait.Type != n8n.WaitNodeType || wait.Parameters["unit"] != "minutes" {
		t.Errorf("wait node = %#v, want a mapped five-minute wait", wait)
	}

	// And back out again.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		switch node.Name {
		case "Every weekday and evening":
			if node.Type != "n8n-nodes-base.scheduleTrigger" {
				t.Errorf("exported schedule type = %q", node.Type)
			}
			exportedRule, _ := node.Parameters["rule"].(map[string]any)
			if entries, _ := exportedRule["interval"].([]any); len(entries) != 2 {
				t.Errorf("exported intervals = %#v, want both", exportedRule["interval"])
			}
		case "Thirty days ago":
			if node.Type != "n8n-nodes-base.dateTime" || node.Parameters["timeUnit"] != "days" {
				t.Errorf("exported date node = %#v", node.Parameters)
			}
		case "Breathe":
			if node.Type != "n8n-nodes-base.wait" || node.Parameters["unit"] != "minutes" {
				t.Errorf("exported wait node = %#v", node.Parameters)
			}
		}
	}
}

func TestAWaitThatNeedsDurableSuspensionIsBlockingRatherThanRewritten(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Approval",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Wait for approval","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
	     "parameters":{"resume":"webhook"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Wait for approval","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)

	// The mode is carried as itself: the execution parks in storage and wakes
	// on the resume URL, which is what n8n's mode means. It used to be a
	// blocking refusal, and a blocking refusal of a feature that now works is
	// a workflow that cannot be activated for no reason.
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Fatalf("blocking issue for a wait this server now supports: %+v", issue)
		}
	}
	wait := nodeByName(result.Document, "Wait for approval")
	if wait.Parameters["resume"] != "webhook" {
		t.Errorf("resume = %#v, want the workflow's own answer kept rather than rewritten", wait.Parameters["resume"])
	}
}

// TestAnImportedFormWaitIsCarriedAsTheApprovalPage pins the one durable mode
// that does not translate exactly: n8n resumes on a form the workflow defines,
// and this server resumes on its approve/deny page. The wait works; the page
// is different, and that is a lossy note rather than a refusal.
func TestAnImportedFormWaitIsCarriedAsTheApprovalPage(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Form wait",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Wait for form","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
	     "parameters":{"resume":"form"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Wait for form","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	wait := nodeByName(result.Document, "Wait for form")
	if wait.Parameters["resume"] != "form" {
		t.Errorf("resume = %#v, want it carried", wait.Parameters["resume"])
	}
	noted := false
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityLossy && strings.Contains(issue.Reason, "approval page") {
			noted = true
		}
		if issue.Severity == n8n.SeverityBlocking {
			t.Fatalf("blocking issue for a form wait: %+v", issue)
		}
	}
	if !noted {
		t.Fatalf("unsupported = %#v, want a lossy note about the approval page", result.Unsupported)
	}
}

func TestTheCompositionFamilyImportsAndExports(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Composed",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Enrich each","type":"n8n-nodes-base.executeWorkflow","typeVersion":1.2,"position":[220,0],
	     "parameters":{"workflowId":{"__rl":true,"mode":"list","value":"aBcD1234","cachedResultName":"Enrichment"},
	                   "mode":"each","options":{"waitForSubWorkflow":false}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Enrich each","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	call := nodeByName(result.Document, "Enrich each")
	if call.Type != n8n.ExecuteWorkflowNodeType {
		t.Fatalf("call node = %q, want it mapped rather than a placeholder", call.Type)
	}
	if call.Parameters["itemsPerCall"] != "eachItem" || call.Parameters["mode"] != "fireAndForget" {
		t.Errorf("call parameters = %#v, want the per-item and fire-and-forget choices carried", call.Parameters)
	}
	// The imported ID is n8n's and will not resolve here, so the import says so
	// once rather than letting the run discover it.
	blocking := false
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && strings.Contains(issue.Reason, "n8n workflow ID") {
			blocking = true
		}
	}
	if !blocking {
		t.Errorf("unsupported = %#v, want the unresolvable workflow ID named", result.Unsupported)
	}

	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Enrich each" {
			continue
		}
		if node.Type != "n8n-nodes-base.executeWorkflow" || node.Parameters["mode"] != "each" {
			t.Errorf("exported call = %#v", node.Parameters)
		}
		locator, _ := node.Parameters["workflowId"].(map[string]any)
		if locator["mode"] != "id" {
			t.Errorf("exported locator = %#v, want an ID rather than a name from another instance", locator)
		}
	}
}

func TestASubWorkflowTriggerImportsAsARootThatCompiles(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Callable",
	  "nodes": [
	    {"id":"a","name":"When Executed by Another Workflow","type":"n8n-nodes-base.executeWorkflowTrigger","typeVersion":1.1,"position":[0,0],
	     "parameters":{"inputSource":"workflowInputs",
	                   "workflowInputs":{"values":[{"name":"customerId","type":"string"}]}}},
	    {"id":"b","name":"Set","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
	     "parameters":{"assignments":{"assignments":[{"id":"1","name":"ok","value":"yes"}]}}}
	  ],
	  "connections": {"When Executed by Another Workflow": {"main": [[{"node":"Set","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	trigger := nodeByName(result.Document, "When Executed by Another Workflow")
	if trigger.Type != n8n.ExecuteWorkflowTriggerType {
		t.Fatalf("trigger = %q, want it mapped", trigger.Type)
	}
	if trigger.Parameters["inputSource"] != "fields" {
		t.Errorf("inputSource = %#v, want the declared field list kept", trigger.Parameters["inputSource"])
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Errorf("a blocking issue for a workflow that is now fully mapped: %#v", issue)
		}
	}
	// The compiler admits it as a root: a node with no inputs that produces
	// items is a trigger, so nothing had to learn this type's name.
	document := result.Document
	document.ID = "wf_callable"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want a workflow that starts from its sub-workflow trigger", err)
	}
}

func TestARespondNodeCarriesItsWholeRespondWithSet(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		respondWith string
		blocking    bool
	}{
		"text":              {respondWith: "text"},
		"json":              {respondWith: "json"},
		"allIncomingItems":  {respondWith: "allIncomingItems"},
		"firstIncomingItem": {respondWith: "firstIncomingItem"},
		"noData":            {respondWith: "noData"},
		"redirect":          {respondWith: "redirect"},
		// Refused rather than downgraded: a response silently turned from a
		// file into a JSON body is a broken integration that returns 200.
		"binary": {respondWith: "binary", blocking: true},
		"jwt":    {respondWith: "jwt", blocking: true},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := `{
			  "name": "Responder",
			  "nodes": [
			    {"id":"a","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
			     "parameters":{"path":"reply","httpMethod":"POST","responseMode":"responseNode"}},
			    {"id":"b","name":"Respond","type":"n8n-nodes-base.respondToWebhook","typeVersion":1.1,"position":[220,0],
			     "parameters":{"respondWith":"` + testCase.respondWith + `","redirectURL":"https://example.test",
			                   "options":{"responseCode":201}}}
			  ],
			  "connections": {"Webhook": {"main": [[{"node":"Respond","type":"main","index":0}]]}}
			}`
			result := importFixture(t, fixture)
			respond := nodeByName(result.Document, "Respond")
			if respond.Parameters["respondWith"] != testCase.respondWith {
				t.Errorf("respondWith = %#v, want %q carried", respond.Parameters["respondWith"], testCase.respondWith)
			}
			if code, _ := respond.Parameters["responseCode"].(float64); code != 201 {
				t.Errorf("responseCode = %#v, want the one under options carried", respond.Parameters["responseCode"])
			}
			blocking := false
			for _, issue := range result.Unsupported {
				if issue.Severity == n8n.SeverityBlocking {
					blocking = true
				}
			}
			if blocking != testCase.blocking {
				t.Errorf("blocking = %v, want %v (%#v)", blocking, testCase.blocking, result.Unsupported)
			}
		})
	}
}

func TestAnImportedCodeNodeIsAFirstClassRefusalRatherThanThePlaceholder(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Scripted",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Filter in JS","type":"n8n-nodes-base.code","typeVersion":2,"position":[220,0],
	     "parameters":{"mode":"runOnceForAllItems","jsCode":"return items.filter(i => i.json.ok);"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Filter in JS","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	code := nodeByName(result.Document, "Filter in JS")
	// Not the generic unsupported placeholder: an unsupported node type is
	// something this product has not built, and a Code node is something it
	// deliberately has not. The two need different answers.
	if code.Type != n8n.ForeignCodeNodeType {
		t.Fatalf("code node = %q, want the dedicated placeholder", code.Type)
	}
	if code.Parameters["jsCode"] != "return items.filter(i => i.json.ok);" {
		t.Errorf("jsCode = %#v, want the original source kept", code.Parameters["jsCode"])
	}
	if code.Parameters["mode"] != "runOnceForAllItems" || code.Parameters["language"] != "javaScript" {
		t.Errorf("parameters = %#v, want the mode and language kept", code.Parameters)
	}

	// One blocking issue, naming the node and the replacement.
	named := false
	for _, issue := range result.Unsupported {
		if issue.Severity != n8n.SeverityBlocking {
			continue
		}
		if issue.NodeName == "Filter in JS" && strings.Contains(issue.Reason, "Filter node") {
			named = true
		}
	}
	if !named {
		t.Errorf("unsupported = %#v, want the Code node named with its replacement", result.Unsupported)
	}

	// A round trip must not cost a user their source.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Filter in JS" {
			continue
		}
		if node.Type != "n8n-nodes-base.code" {
			t.Errorf("exported type = %q, want the n8n Code node", node.Type)
		}
		if node.Parameters["jsCode"] != "return items.filter(i => i.json.ok);" {
			t.Errorf("exported jsCode = %#v, want the source returned unchanged", node.Parameters["jsCode"])
		}
	}
}

func TestEveryJavaScriptEscapeHatchRefusesInTheSameWords(t *testing.T) {
	t.Parallel()

	// One mechanism, one wording, one severity. Three wordings for one
	// situation is how a user concludes the three are different problems.
	const fixture = `{
	  "name": "Two hatches",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Code","type":"n8n-nodes-base.code","typeVersion":2,"position":[220,0],
	     "parameters":{"jsCode":"return items;"}},
	    {"id":"c","name":"Sort","type":"n8n-nodes-base.sort","typeVersion":1,"position":[440,0],
	     "parameters":{"type":"code","code":"return a.json.n - b.json.n;"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Code","type":"main","index":0}]]}},
	  "pinData": {}
	}`

	result := importFixture(t, fixture)
	refusals := 0
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && strings.Contains(issue.Reason, "which this server does not run") {
			refusals++
		}
	}
	if refusals != 2 {
		t.Fatalf("unsupported = %#v, want both escape hatches refused in the same words", result.Unsupported)
	}
}

func TestEveryPostgresOperationImportsOntoItsOwnShape(t *testing.T) {
	t.Parallel()

	// Every operation but executeQuery used to return {"operation":"query"}
	// with no statement at all, so an imported insert arrived as an empty query
	// — a node that activated, ran, and did nothing.
	for _, operation := range []string{"deleteTable", "executeQuery", "insert", "upsert", "select", "update"} {
		t.Run(operation, func(t *testing.T) {
			fixture := `{
			  "name": "Postgres ` + operation + `",
			  "nodes": [
			    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
			    {"id":"b","name":"Postgres","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[220,0],
			     "parameters":{"operation":"` + operation + `",
			                   "query":"SELECT 1",
			                   "schema":{"__rl":true,"mode":"list","value":"public"},
			                   "table":{"__rl":true,"mode":"list","value":"customers"},
			                   "where":{"values":[{"column":"tier","condition":"equal","value":"gold"}]},
			                   "columns":{"mappingMode":"defineBelow","value":{"email":"ada@example.test"},
			                              "matchingColumns":["id"],
			                              "schema":[{"id":"email","displayName":"email","type":"string"}]}}}
			  ],
			  "connections": {"Manual": {"main": [[{"node":"Postgres","type":"main","index":0}]]}}
			}`
			result := importFixture(t, fixture)
			postgres := nodeByName(result.Document, "Postgres")

			// The operation value carries across as itself.
			if postgres.Parameters["operation"] != operation {
				t.Fatalf("operation = %#v, want %q", postgres.Parameters["operation"], operation)
			}
			if operation == "executeQuery" {
				if postgres.Parameters["query"] != "SELECT 1" {
					t.Errorf("query = %#v, want the SQL carried", postgres.Parameters["query"])
				}
				return
			}
			// A table locator, not a flattened string: the shape is the same on
			// both sides so it carries rather than being translated.
			table, ok := postgres.Parameters["table"].(map[string]any)
			if !ok || table["value"] != "customers" {
				t.Fatalf("table = %#v, want the locator carried", postgres.Parameters["table"])
			}
			if table["__rl"] != true {
				t.Errorf("table = %#v, want the locator sentinel", table)
			}
			switch operation {
			case "insert", "upsert", "update":
				columns, ok := postgres.Parameters["columns"].(map[string]any)
				if !ok || columns["mappingMode"] != "defineBelow" {
					t.Errorf("columns = %#v, want the mapper carried whole", postgres.Parameters["columns"])
				}
			case "select", "deleteTable":
				rows, ok := postgres.Parameters["where"].([]any)
				if !ok || len(rows) != 1 {
					t.Fatalf("where = %#v, want the one condition carried", postgres.Parameters["where"])
				}
				row, _ := rows[0].(map[string]any)
				if row["field"] != "tier" || row["operator"] != "equals" {
					t.Errorf("condition = %#v, want the column and operator mapped", row)
				}
			}
		})
	}
}

func TestAnImportedPostgresNodeRoundTripsToN8N(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Round trip",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Postgres","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[220,0],
	     "parameters":{"operation":"select",
	                   "schema":{"__rl":true,"mode":"list","value":"public"},
	                   "table":{"__rl":true,"mode":"list","value":"customers"},
	                   "where":{"values":[{"column":"tier","condition":"equal","value":"gold"}]},
	                   "returnAll":true}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Postgres","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Postgres" {
			continue
		}
		if node.Type != "n8n-nodes-base.postgres" || node.Parameters["operation"] != "select" {
			t.Fatalf("exported = %#v, want the operation carried back", node.Parameters)
		}
		table, _ := node.Parameters["table"].(map[string]any)
		if table["value"] != "customers" || table["__rl"] != true {
			t.Errorf("exported table = %#v, want the locator", node.Parameters["table"])
		}
		where, _ := node.Parameters["where"].(map[string]any)
		values, _ := where["values"].([]any)
		if len(values) != 1 {
			t.Fatalf("exported where = %#v, want the condition carried back", node.Parameters["where"])
		}
		row, _ := values[0].(map[string]any)
		// n8n's own vocabulary on the way out, this node's on the way in.
		if row["column"] != "tier" || row["condition"] != "equal" {
			t.Errorf("exported condition = %#v, want n8n's spelling", row)
		}
		if node.Parameters["returnAll"] != true {
			t.Errorf("exported returnAll = %#v, want it carried", node.Parameters["returnAll"])
		}
	}
}

func TestAnImportedWorkflowKeepsItsTimezone(t *testing.T) {
	t.Parallel()

	// A scheduled workflow whose zone was dropped runs at the wrong hour every
	// day, with nothing anywhere saying why. It was reported as uncarried until
	// the Schedule Trigger learned to read it, and the diagnostic outlived the
	// gap it described.
	const fixture = `{
	  "name": "Jakarta nightly",
	  "settings": {"timezone": "Asia/Jakarta", "executionOrder": "v1"},
	  "nodes": [
	    {"id":"a","name":"Schedule","type":"n8n-nodes-base.scheduleTrigger","typeVersion":1.2,"position":[0,0],
	     "parameters":{"rule":{"interval":[{"field":"days","triggerAtHour":9}]}}}
	  ],
	  "connections": {}
	}`
	result := importFixture(t, fixture)
	if result.Document.Settings["timezone"] != "Asia/Jakarta" {
		t.Fatalf("settings = %#v, want the timezone carried", result.Document.Settings)
	}
	// The other settings still are not carried, and still say so.
	if !hasReason(result.Unsupported, "execution order") {
		t.Errorf("unsupported = %#v, want the uncarried settings still named", result.Unsupported)
	}

	// A zone this server cannot resolve is named rather than carried: falling
	// back to UTC silently is the same wrong hour by another route.
	const bad = `{
	  "name": "Typo",
	  "settings": {"timezone": "Asia/Jakata"},
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}}
	  ],
	  "connections": {}
	}`
	typo := importFixture(t, bad)
	if _, present := typo.Document.Settings["timezone"]; present {
		t.Errorf("settings = %#v, want an unresolvable zone left out", typo.Document.Settings)
	}
	if !hasReason(typo.Unsupported, "not a zone this server knows") {
		t.Errorf("unsupported = %#v, want the unknown zone named", typo.Unsupported)
	}
}

func TestN8NsNullConditionsImportOntoTheRightSideOfTheVocabulary(t *testing.T) {
	t.Parallel()

	// `exists` means the value is present, which is what internal/conditions
	// evaluates and what n8n's isNotEmpty already maps to. So IS NULL is
	// notExists and IS NOT NULL is exists — and getting that backwards makes an
	// imported delete remove the complement of the rows it was meant to.
	for condition, want := range map[string]string{
		"IS NULL":     "notExists",
		"IS NOT NULL": "exists",
	} {
		t.Run(condition, func(t *testing.T) {
			fixture := `{
			  "name": "Nulls",
			  "nodes": [
			    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
			    {"id":"b","name":"Postgres","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[220,0],
			     "parameters":{"operation":"select",
			                   "table":{"__rl":true,"mode":"list","value":"customers"},
			                   "where":{"values":[{"column":"deleted_at","condition":"` + condition + `"}]}}}
			  ],
			  "connections": {"Manual": {"main": [[{"node":"Postgres","type":"main","index":0}]]}}
			}`
			result := importFixture(t, fixture)
			postgres := nodeByName(result.Document, "Postgres")
			rows, _ := postgres.Parameters["where"].([]any)
			if len(rows) != 1 {
				t.Fatalf("where = %#v, want the one condition", postgres.Parameters["where"])
			}
			row, _ := rows[0].(map[string]any)
			if row["operator"] != want {
				t.Errorf("%s imported as %#v, want %q", condition, row["operator"], want)
			}
		})
	}
}

func TestEveryMySQLOperationImportsOntoItsOwnShapeWithoutASchema(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"deleteTable", "executeQuery", "insert", "upsert", "select", "update"} {
		t.Run(operation, func(t *testing.T) {
			fixture := `{
			  "name": "MySQL ` + operation + `",
			  "nodes": [
			    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
			    {"id":"b","name":"MySQL","type":"n8n-nodes-base.mySql","typeVersion":2.4,"position":[220,0],
			     "parameters":{"operation":"` + operation + `",
			                   "query":"SELECT 1",
			                   "table":{"__rl":true,"mode":"list","value":"customers"},
			                   "where":{"values":[{"column":"tier","condition":"equal","value":"gold"}]}}}
			  ],
			  "connections": {"Manual": {"main": [[{"node":"MySQL","type":"main","index":0}]]}}
			}`
			result := importFixture(t, fixture)
			mysql := nodeByName(result.Document, "MySQL")

			// The operation used to be flattened to "query" with the SQL
			// dropped entirely — an insert arrived as an empty query.
			if mysql.Parameters["operation"] != operation {
				t.Fatalf("operation = %#v, want %q", mysql.Parameters["operation"], operation)
			}
			// Never a schema. MySQL has none separate from a database, and
			// PostgreSQL's default of "public" would address a database
			// literally called public.
			if _, present := mysql.Parameters["schema"]; present {
				t.Errorf("parameters = %#v, want no schema on a MySQL node", mysql.Parameters)
			}
			if operation != "executeQuery" {
				table, ok := mysql.Parameters["table"].(map[string]any)
				if !ok || table["value"] != "customers" {
					t.Errorf("table = %#v, want the locator carried", mysql.Parameters["table"])
				}
			}
		})
	}

	// A node that names no operation gets n8n's MySQL default, which is insert
	// where its PostgreSQL node's is executeQuery.
	const bare = `{
	  "name": "Bare",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"MySQL","type":"n8n-nodes-base.mySql","typeVersion":2.4,"position":[220,0],
	     "parameters":{"table":{"__rl":true,"mode":"list","value":"customers"}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"MySQL","type":"main","index":0}]]}}
	}`
	if got := nodeByName(importFixture(t, bare).Document, "MySQL").Parameters["operation"]; got != "insert" {
		t.Errorf("default operation = %#v, want n8n's insert", got)
	}
}

// clusterFixture is the same agent cluster as langchainFixture, with the
// parameters a real exported n8n workflow carries. langchainFixture proves the
// wiring survives; this one proves the nodes arrive configured, which is what
// separates a graph that can be looked at from one that can be activated.
const clusterFixture = `{
  "name": "Support agent",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":3.1,"position":[220,0],"parameters":{
      "promptType":"define","text":"=Answer this: {{ $json.question }}",
      "options":{"systemMessage":"You are a support agent.","maxIterations":12}
    }},
    {"id":"c","name":"OpenAI Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[160,200],"parameters":{
      "model":{"__rl":true,"mode":"list","value":"gpt-4.1-mini"},
      "options":{"temperature":0.2,"maxTokens":2048}
    }},
    {"id":"d","name":"Window Memory","type":"@n8n/n8n-nodes-langchain.memoryBufferWindow","typeVersion":1.3,"position":[280,200],"parameters":{
      "sessionIdType":"customKey","sessionKey":"=chat-{{ $json.chatId }}","contextWindowLength":25
    }},
    {"id":"e","name":"Get Weather","type":"@n8n/n8n-nodes-langchain.toolHttpRequest","typeVersion":1.1,"position":[400,200],"parameters":{
      "toolDescription":"Weather by city","method":"GET","url":"https://api.test/weather"
    }}
  ],
  "connections": {
    "Manual": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Window Memory": {"ai_memory": [[{"node":"AI Agent","type":"ai_memory","index":0}]]},
    "Get Weather": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`

// TestAnImportedAgentClusterCompilesInsteadOfArrivingAsPlaceholders is the gap
// this ticket closed. The AI node types had no mapping entry at all, so every
// one of them imported as the unsupported placeholder: the wiring was right,
// the picture looked right, and the workflow could never be activated because a
// placeholder deliberately fails compilation.
func TestAnImportedAgentClusterCompilesInsteadOfArrivingAsPlaceholders(t *testing.T) {
	t.Parallel()

	result := importFixture(t, clusterFixture)

	for name, wanted := range map[string]string{
		"AI Agent":          "kilasflow.agent",
		"OpenAI Chat Model": "kilasflow.lmChatOpenAi",
		"Window Memory":     "kilasflow.memoryBuffer",
		"Get Weather":       "kilasflow.httpTool",
	} {
		if got := nodeByName(result.Document, name).Type; got != wanted {
			t.Errorf("%s imported as %q, want %q", name, got, wanted)
		}
	}

	document := result.Document
	// An import carries no workflow identity; the store assigns one on save.
	document.ID = "wf_imported"
	// It also deliberately carries no credential: an n8n credential id names a
	// row in the instance it came from, so the model arrives visibly unbound
	// and the user attaches a local one. Doing that here is exactly what a user
	// does before activating, and it is the last step between an imported
	// cluster and a running one.
	for index, imported := range document.Nodes {
		if imported.Type == "kilasflow.lmChatOpenAi" {
			document.Nodes[index].Credentials = map[string]string{"openAiApi": "cred-local"}
		}
	}

	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want an imported cluster to activate", err)
	}
}

// TestAnImportedClusterIsTheSameGraphAsOneBuiltNatively is the direction trap
// stated as an assertion. n8n keys a connection by its source node, and for a
// typed channel the source is the sub-node and the target is the root agent —
// the opposite of how the canvas reads, where the agent appears to own its
// model. An importer written from the picture would reverse every typed edge
// and produce a graph that still compiles, because both endpoints exist, and
// never delivers a descriptor at run time.
func TestAnImportedClusterIsTheSameGraphAsOneBuiltNatively(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, clusterFixture).Document

	type edge struct {
		sourceType string
		targetType string
		targetPort string
		kind       workflow.ConnectionKind
	}
	typeByID := map[string]string{}
	for _, node := range imported.Nodes {
		typeByID[node.ID] = node.Type
	}
	got := map[edge]int{}
	for _, connection := range imported.Connections {
		got[edge{
			typeByID[connection.Source.NodeID], typeByID[connection.Target.NodeID],
			connection.Target.Port, connection.Kind,
		}]++
	}

	// Each sub-node is the source and the agent is the target, on the agent's
	// own named slot.
	want := []edge{
		{"kilasflow.manual", "kilasflow.agent", "main", workflow.ConnectionMain},
		{"kilasflow.lmChatOpenAi", "kilasflow.agent", "model", workflow.ConnectionLanguageModel},
		{"kilasflow.memoryBuffer", "kilasflow.agent", "memory", workflow.ConnectionMemory},
		{"kilasflow.httpTool", "kilasflow.agent", "tools", workflow.ConnectionTool},
	}
	if len(got) != len(want) {
		t.Fatalf("imported %d distinct edges, want %d: %#v", len(got), len(want), got)
	}
	for _, expected := range want {
		if got[expected] != 1 {
			t.Errorf("edge %s -%s-> %s.%s appeared %d times, want once",
				expected.sourceType, expected.kind, expected.targetType, expected.targetPort, got[expected])
		}
	}
}

// TestAnImportedClusterArrivesConfigured checks the parameters rather than the
// wiring. A cluster whose nodes all arrive empty compiles no better than a
// placeholder: the agent's prompt, the model's name and the memory's session are
// all required, and each of them lives under a different key on n8n's side.
func TestAnImportedClusterArrivesConfigured(t *testing.T) {
	t.Parallel()

	document := importFixture(t, clusterFixture).Document

	agent := nodeByName(document, "AI Agent")
	// n8n's `text` is the prompt, and its `=` prefix is an expression marker
	// that has to become this server's explicit one.
	prompt, _ := agent.Parameters["prompt"].(map[string]any)
	if prompt["mode"] != "expression" || prompt["value"] != "Answer this: {{ $json.question }}" {
		t.Errorf("agent prompt = %#v, want the n8n expression translated", agent.Parameters["prompt"])
	}
	// The system message and the iteration bound live inside n8n's `options`
	// collection, not at the top level where this server keeps them.
	if agent.Parameters["systemMessage"] != "You are a support agent." {
		t.Errorf("agent systemMessage = %#v, want it read out of n8n's options collection", agent.Parameters["systemMessage"])
	}
	if agent.Parameters["maxIterations"] != float64(12) {
		t.Errorf("agent maxIterations = %#v, want 12", agent.Parameters["maxIterations"])
	}

	model := nodeByName(document, "OpenAI Chat Model")
	locator, _ := model.Parameters["model"].(map[string]any)
	if locator["value"] != "gpt-4.1-mini" {
		t.Errorf("model = %#v, want the locator's model name carried", model.Parameters["model"])
	}
	options, _ := model.Parameters["options"].(map[string]any)
	if options["temperature"] != 0.2 || options["maxTokens"] != float64(2048) {
		t.Errorf("model options = %#v, want temperature and maxTokens carried", options)
	}

	memory := nodeByName(document, "Window Memory")
	if memory.Parameters["sessionIdType"] != "customKey" {
		t.Errorf("memory sessionIdType = %#v, want n8n's mode carried", memory.Parameters["sessionIdType"])
	}
	session, _ := memory.Parameters["sessionKey"].(map[string]any)
	if session["value"] != "chat-{{ $json.chatId }}" {
		t.Errorf("memory sessionKey = %#v, want n8n's sessionKey", memory.Parameters["sessionKey"])
	}
	// n8n counts interactions — one human turn and one AI turn — and this
	// server counts messages, so the window is doubled rather than copied.
	if memory.Parameters["maxMessages"] != float64(50) {
		t.Errorf("memory maxMessages = %#v, want twice n8n's contextWindowLength", memory.Parameters["maxMessages"])
	}

	tool := nodeByName(document, "Get Weather")
	// n8n has no tool-name parameter: the name a model calls is derived from the
	// node's canvas name, so inventing one here would break any system prompt
	// that already named the tool.
	if tool.Parameters["toolName"] != "Get_Weather" {
		t.Errorf("tool name = %#v, want it derived from the node name the way n8n does", tool.Parameters["toolName"])
	}
	if tool.Parameters["url"] != "https://api.test/weather" {
		t.Errorf("tool url = %#v, want the request carried", tool.Parameters["url"])
	}
}

// TestAnExportedClusterPutsTheSubNodeBackOnTheSourceSide is the other half of
// the direction rule. Export has to key each typed edge by the sub-node, or the
// file opens in n8n with an agent attached to nothing.
func TestAnExportedClusterPutsTheSubNodeBackOnTheSourceSide(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, clusterFixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	for _, expected := range []struct{ source, channel, target string }{
		{"OpenAI Chat Model", "ai_languageModel", "AI Agent"},
		{"Window Memory", "ai_memory", "AI Agent"},
		{"Get Weather", "ai_tool", "AI Agent"},
	} {
		var found bool
		for _, targets := range exported.Document.Connections[expected.source][expected.channel] {
			for _, target := range targets {
				if target.Node == expected.target && target.Type == expected.channel {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s -%s-> %s is not keyed by the sub-node after export: %#v",
				expected.source, expected.channel, expected.target, exported.Document.Connections)
		}
	}
	// The agent must not have grown outgoing typed channels of its own, which
	// is what a reversed export would produce.
	for channel := range exported.Document.Connections["AI Agent"] {
		if channel != "main" {
			t.Errorf("the agent exported an outgoing %q channel; the sub-node owns that edge", channel)
		}
	}

	// The node types have to go back out as n8n's own, or the file names types
	// n8n cannot resolve.
	byName := map[string]string{}
	for _, node := range exported.Document.Nodes {
		byName[node.Name] = node.Type
	}
	for name, wanted := range map[string]string{
		"AI Agent":          "@n8n/n8n-nodes-langchain.agent",
		"OpenAI Chat Model": "@n8n/n8n-nodes-langchain.lmChatOpenAi",
		"Window Memory":     "@n8n/n8n-nodes-langchain.memoryBufferWindow",
		"Get Weather":       "@n8n/n8n-nodes-langchain.toolHttpRequest",
	} {
		if byName[name] != wanted {
			t.Errorf("%s exported as %q, want %q", name, byName[name], wanted)
		}
	}
}

// TestAnAgentClusterSurvivesTheRoundTripUnchanged is the property the two
// directions owe each other. Import then export then import again has to land
// on the same configuration, or a workflow degrades a little every time it
// crosses the boundary.
func TestAnAgentClusterSurvivesTheRoundTripUnchanged(t *testing.T) {
	t.Parallel()

	first := importFixture(t, clusterFixture).Document
	exported, err := n8n.Export(first, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	second := importFixture(t, string(encoded)).Document

	for _, name := range []string{"AI Agent", "OpenAI Chat Model", "Window Memory", "Get Weather"} {
		before, after := nodeByName(first, name), nodeByName(second, name)
		if before.Type != after.Type {
			t.Errorf("%s changed type across the round trip: %q then %q", name, before.Type, after.Type)
		}
		for _, key := range []string{"prompt", "systemMessage", "maxIterations", "model", "sessionIdType", "sessionKey", "sessionId", "maxMessages", "toolName", "url"} {
			if _, present := before.Parameters[key]; !present {
				continue
			}
			if !reflect.DeepEqual(before.Parameters[key], after.Parameters[key]) {
				t.Errorf("%s.%s = %#v after the round trip, want %#v", name, key, after.Parameters[key], before.Parameters[key])
			}
		}
	}
}

// TestTheTwoChatModelProvidersKeepTheirOwnModelShape is the difference that
// makes one shared translator wrong. n8n's OpenAI node stores its model as a
// resource locator from typeVersion 1.2, and its OpenRouter node stores a bare
// string at every version it publishes — so an export that wrote one shape for
// both would produce a file n8n's own editor could not read.
func TestTheTwoChatModelProvidersKeepTheirOwnModelShape(t *testing.T) {
	t.Parallel()

	fixture := `{
	  "name": "Two providers",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"c","name":"OpenAI","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[0,200],"parameters":{
	      "model":{"__rl":true,"mode":"id","value":"gpt-4.1"}}},
	    {"id":"d","name":"Router","type":"@n8n/n8n-nodes-langchain.lmChatOpenRouter","typeVersion":1,"position":[0,300],"parameters":{
	      "model":"anthropic/claude-3.5-sonnet"}}
	  ],
	  "connections": {}
	}`

	document := importFixture(t, fixture).Document
	// Both arrive as this server's resource locator, whichever shape they came
	// in as, because that is the one shape its own node declares.
	for name, wanted := range map[string]string{"OpenAI": "gpt-4.1", "Router": "anthropic/claude-3.5-sonnet"} {
		locator, _ := nodeByName(document, name).Parameters["model"].(map[string]any)
		if locator["value"] != wanted {
			t.Errorf("%s model = %#v, want %q", name, nodeByName(document, name).Parameters["model"], wanted)
		}
	}

	exported, err := n8n.Export(document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	byName := map[string]n8n.Node{}
	for _, node := range exported.Document.Nodes {
		byName[node.Name] = node
	}
	if locator, ok := byName["OpenAI"].Parameters["model"].(map[string]any); !ok || locator["value"] != "gpt-4.1" {
		t.Errorf("OpenAI model exported as %#v, want a resource locator", byName["OpenAI"].Parameters["model"])
	}
	if name, ok := byName["Router"].Parameters["model"].(string); !ok || name != "anthropic/claude-3.5-sonnet" {
		t.Errorf("OpenRouter model exported as %#v, want a bare string", byName["Router"].Parameters["model"])
	}
}

// TestTheClusterMappingMatchesWhatTheReferenceWasRecordedAsSaying is the
// anti-drift check. The n8n type strings and the versions written on an export
// are interchange facts, and this compares the mapping table against the
// committed transcription rather than against the reference checkout, which no
// build input may read.
func TestTheClusterMappingMatchesWhatTheReferenceWasRecordedAsSaying(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("testdata", "n8n_cluster_nodes.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var recorded struct {
		TypePrefix string `json:"_typePrefix"`
		Nodes      []struct {
			N8NName           string    `json:"n8nName"`
			PublishedVersions []float64 `json:"publishedVersions"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(recorded.Nodes) == 0 || recorded.TypePrefix == "" {
		t.Fatal("the transcription is empty; it is the only record of what the reference said")
	}

	advertised := map[string]bool{}
	for _, pair := range n8n.SupportedMappings() {
		advertised[strings.SplitN(pair, " ", 2)[0]] = true
	}
	for _, entry := range recorded.Nodes {
		nodeType := recorded.TypePrefix + entry.N8NName
		if !advertised[nodeType] {
			t.Errorf("the reference records %s, which the mapping table does not advertise", nodeType)
		}
		if len(entry.PublishedVersions) == 0 {
			t.Errorf("%s has no recorded published versions, so an export cannot know what n8n accepts", nodeType)
		}
	}
}

// TestImportRefusesANonToolsAgent is the pre-1.82 selector trap. A workflow
// carrying parameters.agent naming anything but the Tools Agent must arrive
// as a placeholder that names the node and the agent type — never as a Tools
// Agent that would silently run a workflow the author never wrote.
func TestImportRefusesANonToolsAgent(t *testing.T) {
	t.Parallel()

	fixture := func(agent string) string {
		parameters := `"promptType":"define","text":"hi"`
		if agent != "" {
			parameters += fmt.Sprintf(`,"agent":%q`, agent)
		}
		return `{
	  "name": "Old agent",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Old Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":1.7,"position":[220,0],"parameters":{` +
			parameters + `}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Old Agent","type":"main","index":0}]]}}
	}`
	}

	refused := importFixture(t, fixture("conversationalAgent"))
	node := nodeByName(refused.Document, "Old Agent")
	if node.Type != n8n.UnsupportedNodeType {
		t.Fatalf("a conversationalAgent imported as %q, want the unsupported placeholder", node.Type)
	}
	var named bool
	for _, issue := range refused.Unsupported {
		if issue.Severity == n8n.SeverityBlocking &&
			issue.NodeName == "Old Agent" &&
			strings.Contains(issue.Reason, "conversationalAgent") {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic names the node and the agent type: %#v", refused.Unsupported)
	}

	// The Tools Agent by name, and the post-1.82 graph with no selector at
	// all, both import as the agent.
	for name, agent := range map[string]string{"named tools agent": "toolsAgent", "no selector": ""} {
		imported := importFixture(t, fixture(agent))
		if got := nodeByName(imported.Document, "Old Agent").Type; got != "kilasflow.agent" {
			t.Errorf("%s imported as %q, want kilasflow.agent", name, got)
		}
	}
}

// TestImportReportsAgentBatchingAsDropped pins the batching contract: the
// options are never accepted into the document and silently ignored — they
// arrive as a dropped diagnostic and items run in order.
func TestImportReportsAgentBatchingAsDropped(t *testing.T) {
	t.Parallel()

	// n8n nests batching inside the options collection, not beside it.
	batched := `{
	  "name": "Batched agent",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":3.1,"position":[220,0],"parameters":{
	      "promptType":"define","text":"hi",
	      "options":{"batching":{"batchSize":5,"delayBetweenBatches":100}}
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"AI Agent","type":"main","index":0}]]}}
	}`

	result := importFixture(t, batched)
	node := nodeByName(result.Document, "AI Agent")
	if node.Type != "kilasflow.agent" {
		t.Fatalf("agent imported as %q, want kilasflow.agent", node.Type)
	}
	if _, present := node.Parameters["batching"]; present {
		t.Errorf("batching was accepted into the document: %#v", node.Parameters)
	}
	var reported bool
	for _, issue := range result.Unsupported {
		if issue.Field == "options.batching" && issue.Severity == n8n.SeverityDropped && issue.NodeName == "AI Agent" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("options.batching was not reported as dropped: %#v", result.Unsupported)
	}
}

// TestImportCarriesTheChainNode proves the Basic LLM Chain crosses with its
// prompt surface intact, and that the slots this server does not have arrive
// as diagnostics rather than silence.
func TestImportCarriesTheChainNode(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Summariser",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Summarise","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.9,"position":[220,0],"parameters":{
	      "promptType":"define","text":"Summarise:",
	      "messages":{"messageValues":[
	        {"type":"system","message":"You are a summariser."},
	        {"type":"human","message":"=Be brief about {{ $json.topic }}."}
	      ]}
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Summarise","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	node := nodeByName(result.Document, "Summarise")
	if node.Type != "kilasflow.chainLlm" {
		t.Fatalf("chain imported as %q, want kilasflow.chainLlm", node.Type)
	}
	if node.Parameters["promptType"] != "define" || node.Parameters["text"] != "Summarise:" {
		t.Errorf("chain prompt = %#v, want promptType and text carried", node.Parameters)
	}
	collection, _ := node.Parameters["messages"].(map[string]any)
	rows, _ := collection["messageValues"].([]any)
	if len(rows) != 2 {
		t.Fatalf("message rows = %#v, want both rows carried", node.Parameters["messages"])
	}
	// The `=` prefix is n8n's expression marker and has to become this
	// server's explicit one, row by row.
	second, _ := rows[1].(map[string]any)
	expression, _ := second["message"].(map[string]any)
	if expression["mode"] != "expression" || expression["value"] != "Be brief about {{ $json.topic }}." {
		t.Errorf("row 2 message = %#v, want the n8n expression translated", second["message"])
	}

	unsupported := `{
	  "name": "Fancy chain",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Summarise","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.9,"position":[220,0],"parameters":{
	      "promptType":"auto","hasOutputParser":true,"needsFallback":true,
	      "batching":{"batchSize":5}
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Summarise","type":"main","index":0}]]}}
	}`
	blocked := importFixture(t, unsupported)
	fields := map[string]n8n.IssueSeverity{}
	for _, issue := range blocked.Unsupported {
		if issue.NodeName == "Summarise" && issue.Field != "" {
			fields[issue.Field] = issue.Severity
		}
	}
	for field, severity := range map[string]n8n.IssueSeverity{
		"hasOutputParser": n8n.SeverityBlocking,
		"needsFallback":   n8n.SeverityBlocking,
		"batching":        n8n.SeverityDropped,
	} {
		if fields[field] != severity {
			t.Errorf("%s reported as %q, want %q (all issues: %#v)", field, fields[field], severity, blocked.Unsupported)
		}
	}
}

// TestImportCarriesMemoryModes proves the session modes cross under their own
// names, so the scoping rule travels with the key.
func TestImportCarriesMemoryModes(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Scoped memory",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Window Memory","type":"@n8n/n8n-nodes-langchain.memoryBufferWindow","typeVersion":1.3,"position":[220,0],"parameters":{
	      "sessionIdType":"fromInput","sessionKey":"={{ $json.sessionId }}"
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Window Memory","type":"main","index":0}]]}}
	}`

	node := nodeByName(importFixture(t, fixture).Document, "Window Memory")
	if node.Parameters["sessionIdType"] != "fromInput" {
		t.Errorf("sessionIdType = %#v, want fromInput carried", node.Parameters["sessionIdType"])
	}
	key, _ := node.Parameters["sessionKey"].(map[string]any)
	if key["mode"] != "expression" || key["value"] != "{{ $json.sessionId }}" {
		t.Errorf("sessionKey = %#v, want the n8n expression translated", node.Parameters["sessionKey"])
	}
}

// langchainFullFixture is the whole cluster in one workflow: an agent and a
// chain, each with its own chat model, a memory and two tools on the agent —
// one HTTP, one workflow — and a structured parser wired to the agent. The
// chain names a parser it does not wire, so one hasOutputParser flag resolves
// through its edge and the other stays a diagnostic.
const langchainFullFixture = `{
  "name": "Assistant and summarisers",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":3.1,"position":[220,0],"parameters":{
      "promptType":"define","text":"Help with order {{ $json.orderId }}.",
      "options":{"systemMessage":"You are order support.","maxIterations":10},
      "hasOutputParser":true
    }},
    {"id":"c","name":"Summarise","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.9,"position":[220,320],"parameters":{
      "promptType":"define","text":"Summarise:",
      "messages":{"messageValues":[
        {"type":"system","message":"You are a summariser."},
        {"type":"human","message":"=Be brief about {{ $json.topic }}."}
      ]},
      "hasOutputParser":true
    }},
    {"id":"d","name":"Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[160,200],"parameters":{
      "model":{"mode":"list","value":"gpt-5-mini"}
    }},
    {"id":"e","name":"Chain Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenRouter","typeVersion":1,"position":[160,420],"parameters":{
      "model":"openai/gpt-4.1-mini"
    }},
    {"id":"f","name":"Window Memory","type":"@n8n/n8n-nodes-langchain.memoryBufferWindow","typeVersion":1.3,"position":[280,200],"parameters":{
      "sessionIdType":"customKey","sessionKey":"wa-123"
    }},
    {"id":"g","name":"Weather","type":"@n8n/n8n-nodes-langchain.toolHttpRequest","typeVersion":1.1,"position":[400,200],"parameters":{
      "method":"GET","url":"https://api.example.test/weather","toolDescription":"Reads the weather."
    }},
    {"id":"h","name":"Lookup","type":"@n8n/n8n-nodes-langchain.toolWorkflow","typeVersion":2.1,"position":[520,200],"parameters":{
      "name":"lookup_order","description":"Looks an order up.",
      "source":"database","workflowId":{"value":"order-workflow"},
      "workflowInputs":{"mappingMode":"defineBelow","value":{"mapping":{"orderId":"={{ $json.orderId }}"}}}
    }},
    {"id":"i","name":"Answer Parser","type":"@n8n/n8n-nodes-langchain.outputParserStructured","typeVersion":1.3,"position":[280,80],"parameters":{
      "schemaType":"fromJson","jsonSchemaExample":"{\"state\": \"California\"}",
      "autoFix":true,"customizeRetryPrompt":true,"prompt":"Fix it: {error}"
    }}
  ],
  "connections": {
    "Manual": {"main": [[{"node":"AI Agent","type":"main","index":0},{"node":"Summarise","type":"main","index":0}]]},
    "Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Chain Model": {"ai_languageModel": [[{"node":"Summarise","type":"ai_languageModel","index":0}]]},
    "Window Memory": {"ai_memory": [[{"node":"AI Agent","type":"ai_memory","index":0}]]},
    "Weather": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]},
    "Lookup": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]},
    "Answer Parser": {"ai_outputParser": [[{"node":"AI Agent","type":"ai_outputParser","index":0}]]}
  }
}`

// TestImportMapsTheWholeLangChainCluster proves every node in a representative
// AI workflow lands on a native type with its parameters carried, and that the
// one parser flag answered by an edge stops being a diagnostic while the one
// without an edge stays blocking.
func TestImportMapsTheWholeLangChainCluster(t *testing.T) {
	t.Parallel()

	result := importFixture(t, langchainFullFixture)

	for name, want := range map[string]string{
		"AI Agent": "kilasflow.agent", "Summarise": "kilasflow.chainLlm",
		"Chat Model": "kilasflow.lmChatOpenAi", "Chain Model": "kilasflow.lmChatOpenRouter",
		"Window Memory": "kilasflow.memoryBuffer", "Weather": "kilasflow.httpTool",
		"Lookup": "kilasflow.workflowTool", "Answer Parser": "kilasflow.outputParser",
	} {
		if got := nodeByName(result.Document, name).Type; got != want {
			t.Errorf("%s imported as %q, want %q", name, got, want)
		}
	}

	// The workflow tool keeps its explicit name, its reference and its
	// inputs, with the expression translated.
	lookup := nodeByName(result.Document, "Lookup")
	if lookup.Parameters["toolName"] != "lookup_order" {
		t.Errorf("toolName = %#v, want the explicit name carried", lookup.Parameters["toolName"])
	}
	if lookup.Parameters["workflowId"] != "order-workflow" {
		t.Errorf("workflowId = %#v, want the referenced workflow carried", lookup.Parameters["workflowId"])
	}
	inputs, _ := lookup.Parameters["workflowInputs"].(map[string]any)
	entry, _ := inputs["orderId"].(map[string]any)
	if entry["mode"] != "expression" || entry["value"] != "{{ $json.orderId }}" {
		t.Errorf("workflowInputs.orderId = %#v, want the n8n expression translated", inputs["orderId"])
	}

	// The parser translates its mode and turns auto-fix into a retry count,
	// while the retry prompt it cannot carry is named.
	parser := nodeByName(result.Document, "Answer Parser")
	if parser.Parameters["schemaType"] != "exampleJson" {
		t.Errorf("schemaType = %#v, want fromJson translated to exampleJson", parser.Parameters["schemaType"])
	}
	if parser.Parameters["exampleJson"] != `{"state": "California"}` {
		t.Errorf("exampleJson = %#v, want the example carried", parser.Parameters["exampleJson"])
	}
	if parser.Parameters["maxRetries"] != float64(2) {
		t.Errorf("maxRetries = %#v, want autoFix carried as 2 retries", parser.Parameters["maxRetries"])
	}

	flags := map[string]n8n.IssueSeverity{}
	for _, issue := range result.Unsupported {
		if issue.Field == "hasOutputParser" || issue.Field == "customizeRetryPrompt" || issue.Field == "prompt" {
			flags[issue.NodeName+"/"+issue.Field] = issue.Severity
		}
	}
	// The agent's flag resolved through its parser edge; the chain's parser
	// never arrived, so its flag stays blocking.
	if _, present := flags["AI Agent/hasOutputParser"]; present {
		t.Errorf("AI Agent/hasOutputParser still reported: %#v", result.Unsupported)
	}
	for field, severity := range map[string]n8n.IssueSeverity{
		"Summarise/hasOutputParser":          n8n.SeverityBlocking,
		"Answer Parser/customizeRetryPrompt": n8n.SeverityDropped,
		"Answer Parser/prompt":               n8n.SeverityDropped,
	} {
		if flags[field] != severity {
			t.Errorf("%s reported as %q, want %q (all issues: %#v)", field, flags[field], severity, result.Unsupported)
		}
	}

	// The typed edges land with the sub-node as the source, including the
	// parser channel the cluster had no test for.
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
	for _, expected := range []edge{
		{"Chat Model", "AI Agent", workflow.ConnectionLanguageModel},
		{"Chain Model", "Summarise", workflow.ConnectionLanguageModel},
		{"Window Memory", "AI Agent", workflow.ConnectionMemory},
		{"Weather", "AI Agent", workflow.ConnectionTool},
		{"Lookup", "AI Agent", workflow.ConnectionTool},
		{"Answer Parser", "AI Agent", workflow.ConnectionOutputParser},
	} {
		if got[expected] != 1 {
			t.Errorf("edge %s -%s-> %s appeared %d times, want once", expected.source, expected.kind, expected.target, got[expected])
		}
	}
}

// TestLangChainClusterRoundTrip proves the two new nodes go back to n8n at
// the versions n8n accepts, with the sub-node still the connection source.
func TestLangChainClusterRoundTrip(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, langchainFullFixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	byName := map[string]n8n.Node{}
	for _, node := range exported.Document.Nodes {
		byName[node.Name] = node
	}
	if byName["Lookup"].Type != "@n8n/n8n-nodes-langchain.toolWorkflow" || byName["Lookup"].TypeVersion != 2.2 {
		t.Errorf("Lookup exported as %s@%v, want toolWorkflow@2.2", byName["Lookup"].Type, byName["Lookup"].TypeVersion)
	}
	if byName["Answer Parser"].Type != "@n8n/n8n-nodes-langchain.outputParserStructured" || byName["Answer Parser"].TypeVersion != 1.3 {
		t.Errorf("Answer Parser exported as %s@%v, want outputParserStructured@1.3",
			byName["Answer Parser"].Type, byName["Answer Parser"].TypeVersion)
	}
	tool := byName["Lookup"].Parameters
	if tool["name"] != "lookup_order" || tool["source"] != "database" {
		t.Errorf("Lookup parameters = %#v, want name and database source carried", tool)
	}
	if locator, ok := tool["workflowId"].(map[string]any); !ok || locator["value"] != "order-workflow" {
		t.Errorf("Lookup workflowId = %#v, want the referenced workflow carried", tool["workflowId"])
	}
	structured := byName["Answer Parser"].Parameters
	if structured["schemaType"] != "fromJson" || structured["jsonSchemaExample"] != `{"state": "California"}` {
		t.Errorf("Answer Parser parameters = %#v, want the example mode restored", structured)
	}
	if structured["autoFix"] != true {
		t.Errorf("Answer Parser autoFix = %#v, want maxRetries 2 carried back as true", structured["autoFix"])
	}
	// A custom retry prompt has nowhere to go on export, so it stays
	// dropped rather than coming back invented.
	if _, present := structured["prompt"]; present {
		t.Errorf("Answer Parser prompt = %#v, want no invented retry prompt", structured["prompt"])
	}
	for _, expected := range []struct {
		source  string
		channel string
		target  string
	}{
		{"Lookup", "ai_tool", "AI Agent"},
		{"Answer Parser", "ai_outputParser", "AI Agent"},
		{"Chain Model", "ai_languageModel", "Summarise"},
	} {
		var found bool
		for _, targets := range exported.Document.Connections[expected.source][expected.channel] {
			for _, target := range targets {
				if target.Node == expected.target && target.Type == expected.channel {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s has no %s connection to %s after export", expected.source, expected.channel, expected.target)
		}
	}
}

// TestImportRefusesAnInlineWorkflowTool is the source=parameter trap. Inline
// workflow JSON has no native equivalent, so the node maps but arrives with
// a blocking diagnostic naming the field — never as a database tool calling
// nothing.
func TestImportRefusesAnInlineWorkflowTool(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Inline tool",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Inline","type":"@n8n/n8n-nodes-langchain.toolWorkflow","typeVersion":2.1,"position":[220,0],"parameters":{
	      "name":"inline_tool","description":"Inline.","source":"parameter","workflowJson":"{}"
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Inline","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	if got := nodeByName(result.Document, "Inline").Type; got != "kilasflow.workflowTool" {
		t.Fatalf("tool imported as %q, want kilasflow.workflowTool", got)
	}
	var named bool
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Inline" && issue.Field == "workflowJson" && issue.Severity == n8n.SeverityBlocking {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic names the inline workflow JSON: %#v", result.Unsupported)
	}
}

// TestImportReadsAPreVersionParser proves a parser from before the mode
// selector still maps: its schema lives under `jsonSchema` and becomes this
// server's jsonSchema mode with auto-fix off.
func TestImportReadsAPreVersionParser(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Old parser",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Old Parser","type":"@n8n/n8n-nodes-langchain.outputParserStructured","typeVersion":1.1,"position":[220,0],"parameters":{
	      "jsonSchema":"{\"type\": \"object\"}"
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Old Parser","type":"main","index":0}]]}}
	}`

	node := nodeByName(importFixture(t, fixture).Document, "Old Parser")
	if node.Type != "kilasflow.outputParser" {
		t.Fatalf("parser imported as %q, want kilasflow.outputParser", node.Type)
	}
	if node.Parameters["maxRetries"] != float64(0) {
		t.Errorf("maxRetries = %#v, want auto-fix off carried as 0", node.Parameters["maxRetries"])
	}
}

// --- Data Table ---------------------------------------------------------------

// TestDataTableOperationsImportOntoDatastoreOperations proves every one of
// n8n's twelve Data Table operations lands on this server's operation of the
// same meaning: deleteRows is delete, the row-exists pair become the two
// branches, and the table update is Rename, never a generic update.
func TestDataTableOperationsImportOntoDatastoreOperations(t *testing.T) {
	t.Parallel()

	rows := []struct {
		n8nOperation string
		resource     string
		want         string
	}{
		{"insert", "row", "insert"},
		{"get", "row", "get"},
		{"update", "row", "update"},
		{"upsert", "row", "upsert"},
		{"deleteRows", "row", "delete"},
		{"rowExists", "row", "ifExists"},
		{"rowNotExists", "row", "ifNotExists"},
		{"create", "table", "create"},
		{"list", "table", "list"},
		{"clear", "table", "clear"},
		{"update", "table", "rename"},
		{"delete", "table", "deleteTable"},
	}
	for _, row := range rows {
		fixture := fmt.Sprintf(`{
		  "name": "Data Table ops",
		  "nodes": [
		    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
		    {"id":"b","name":"Table","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
		      "resource": %q, "operation": %q,
		      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
		      "name": "T2"
		    }}
		  ],
		  "connections": {"Manual": {"main": [[{"node":"Table","type":"main","index":0}]]}}
		}`, row.resource, row.n8nOperation)
		result := importFixture(t, fixture)
		node := nodeByName(result.Document, "Table")
		if node.Type != "kilasflow.datastore" {
			t.Fatalf("%s/%s imported as %q, want kilasflow.datastore", row.resource, row.n8nOperation, node.Type)
		}
		if got := stringParameter(node.Parameters, "resource"); got != row.resource {
			t.Errorf("%s/%s resource = %q, want %q", row.resource, row.n8nOperation, got, row.resource)
		}
		if got := stringParameter(node.Parameters, "operation"); got != row.want {
			t.Errorf("%s/%s operation = %q, want %q", row.resource, row.n8nOperation, got, row.want)
		}
	}
}

// stringParameter reads a stored string parameter the way the exporter does.
func stringParameter(parameters map[string]any, key string) string {
	text, _ := parameters[key].(string)
	return text
}

// TestDataTableRefusesAnUnknownOperationByName proves an operation outside
// the twelve maps to nothing silently: it arrives as the resource default
// with a blocking diagnostic naming the operation.
func TestDataTableRefusesAnUnknownOperationByName(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Unknown op",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Table","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "vacuum",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true}
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Table","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	node := nodeByName(result.Document, "Table")
	if got := stringParameter(node.Parameters, "operation"); got != "insert" {
		t.Fatalf("operation = %q, want the row default insert", got)
	}
	var named bool
	for _, issue := range result.Unsupported {
		if issue.Field == "operation" && issue.Severity == n8n.SeverityBlocking &&
			strings.Contains(issue.Reason, `"vacuum"`) {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic names the unknown operation: %#v", result.Unsupported)
	}
}

// TestDataTableLocatorModesAreCarriedWithTheirCachedNames proves all three
// resource locator modes — From list, By Name and By ID — cross with the
// cached name travelling as display data, and that every carried reference
// reports the n8n id it came from as blocking: the id names a row in n8n's
// catalogue and has no local counterpart.
func TestDataTableLocatorModesAreCarriedWithTheirCachedNames(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"list", "name", "id"} {
		fixture := fmt.Sprintf(`{
		  "name": "Locator modes",
		  "nodes": [
		    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
		    {"id":"b","name":"Table","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
		      "resource": "row", "operation": "get",
		      "dataTableId": {"mode":%q,"value":"dt_metrics_01","cachedResultName":"Metrics","__rl":true}
		    }}
		  ],
		  "connections": {"Manual": {"main": [[{"node":"Table","type":"main","index":0}]]}}
		}`, mode)
		result := importFixture(t, fixture)
		node := nodeByName(result.Document, "Table")
		locator, ok := node.Parameters["dataTableId"].(map[string]any)
		if !ok {
			t.Fatalf("mode %s: dataTableId was not carried: %#v", mode, node.Parameters)
		}
		if locator["mode"] != mode {
			t.Errorf("mode %s: locator mode = %#v, want %q", mode, locator["mode"], mode)
		}
		if locator["value"] != "dt_metrics_01" {
			t.Errorf("mode %s: locator value = %#v, want the n8n table id", mode, locator["value"])
		}
		if locator["cachedResultName"] != "Metrics" {
			t.Errorf("mode %s: cached name = %#v, want Metrics", mode, locator["cachedResultName"])
		}
		var named bool
		for _, issue := range result.Unsupported {
			if issue.Field == "dataTableId" && issue.Severity == n8n.SeverityBlocking &&
				strings.Contains(issue.Reason, "dt_metrics_01") &&
				strings.Contains(issue.Reason, "Metrics") {
				named = true
			}
		}
		if !named {
			t.Errorf("mode %s: no blocking diagnostic names the n8n id and cached name: %#v", mode, result.Unsupported)
		}
	}
}

// TestDataTableWithoutATableIsBlockingAndKeepsTheCachedName proves a locator
// with no value still imports — the workflow can be edited — while reporting
// what was missing, including the cached name the picker last showed.
func TestDataTableWithoutATableIsBlockingAndKeepsTheCachedName(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Empty locator",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Table","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "insert",
	      "dataTableId": {"mode":"list","value":"","cachedResultName":"Metrics","__rl":true}
	    }}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Table","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	node := nodeByName(result.Document, "Table")
	if node.Type != "kilasflow.datastore" {
		t.Fatalf("node imported as %q, want kilasflow.datastore", node.Type)
	}
	var named bool
	for _, issue := range result.Unsupported {
		if issue.Field == "dataTableId" && issue.Severity == n8n.SeverityBlocking &&
			strings.Contains(issue.Reason, "Metrics") {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic names the missing table and cached name: %#v", result.Unsupported)
	}
}

// TestDataTableMappingModesCrossVerbatim proves both mapping column modes —
// Map Each Column Manually and Map Automatically, with their help texts
// living on the node definition — arrive under n8n's own mode names, with
// manual values converting as expressions and the schema copy riding along.
func TestDataTableMappingModesCrossVerbatim(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Mapping modes",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Manual Map","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "insert",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "columns": {
	        "mappingMode": "defineBelow",
	        "value": {"metric": "cpu", "count": "={{ $json.count }}"},
	        "matchingColumns": ["metric"],
	        "schema": [{"id":"metric","displayName":"metric","type":"string"}]
	      }
	    }},
	    {"id":"c","name":"Auto Map","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[440,0],"parameters":{
	      "resource": "row", "operation": "insert",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "columns": {"mappingMode": "autoMapInputData", "value": {}}
	    }}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Manual Map","type":"main","index":0}]]},
	    "Manual Map": {"main": [[{"node":"Auto Map","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	manual, ok := nodeByName(result.Document, "Manual Map").Parameters["columns"].(map[string]any)
	if !ok {
		t.Fatalf("manual columns were not carried: %#v", result.Document.Nodes)
	}
	if manual["mappingMode"] != "defineBelow" {
		t.Errorf("manual mappingMode = %#v, want defineBelow", manual["mappingMode"])
	}
	values, _ := manual["value"].(map[string]any)
	if marker, ok := values["count"].(map[string]any); !ok || marker["mode"] != "expression" {
		t.Errorf("manual count = %#v, want the expression marker", values["count"])
	}
	matching, _ := manual["matchingColumns"].([]any)
	if len(matching) != 1 || matching[0] != "metric" {
		t.Errorf("matchingColumns = %#v, want [metric]", matching)
	}
	if values["metric"] != "cpu" {
		t.Errorf("manual metric = %#v, want the fixed string carried as itself", values["metric"])
	}
	auto, ok := nodeByName(result.Document, "Auto Map").Parameters["columns"].(map[string]any)
	if !ok || auto["mappingMode"] != "autoMapInputData" {
		t.Errorf("auto columns = %#v, want mappingMode autoMapInputData", nodeByName(result.Document, "Auto Map").Parameters["columns"])
	}
}

// TestDataTableFiltersUseTheNodePaths proves the Conditions panel crosses
// under the node's own keyName/condition/keyValue paths — not the service
// layer's columnName/condition/value — with the default eq applied silently
// and an expression-valued column slot refused by name.
func TestDataTableFiltersUseTheNodePaths(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Filters",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Get","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "get",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "match": "all",
	      "filters": {"conditions": [
	        {"keyName": "metric", "condition": "gte", "keyValue": "={{ $json.floor }}"},
	        {"keyName": "count", "keyValue": 10},
	        {"keyName": "stale", "condition": "regexp", "keyValue": "x"}
	      ]},
	      "returnAll": false,
	      "limitPerInputRow": 20
	    }},
	    {"id":"c","name":"Bad Column","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[440,0],"parameters":{
	      "resource": "row", "operation": "get",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "filters": {"conditions": [{"keyName": "={{ $json.column }}", "condition": "eq", "keyValue": "x"}]}
	    }}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Get","type":"main","index":0}]]},
	    "Get": {"main": [[{"node":"Bad Column","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	node := nodeByName(result.Document, "Get")
	filters, ok := node.Parameters["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters were not carried: %#v", node.Parameters)
	}
	conditions, _ := filters["conditions"].([]any)
	if len(conditions) != 3 {
		t.Fatalf("conditions = %#v, want three rows", filters["conditions"])
	}
	first, _ := conditions[0].(map[string]any)
	if first["keyName"] != "metric" || first["condition"] != "gte" {
		t.Errorf("first row = %#v, want keyName metric with gte", first)
	}
	if marker, ok := first["keyValue"].(map[string]any); !ok || marker["mode"] != "expression" {
		t.Errorf("first keyValue = %#v, want the expression marker", first["keyValue"])
	}
	second, _ := conditions[1].(map[string]any)
	if second["condition"] != "eq" {
		t.Errorf("absent condition = %#v, want the n8n default eq carried silently", second["condition"])
	}
	third, _ := conditions[2].(map[string]any)
	if third["condition"] != "eq" {
		t.Errorf("unknown condition = %#v, want the equality fallback", third["condition"])
	}
	if !hasReason(result.Unsupported, `"regexp"`) {
		t.Errorf("no diagnostic names the unmapped condition: %#v", result.Unsupported)
	}
	if stringParameter(node.Parameters, "match") != "all" {
		t.Errorf("match = %#v, want all", node.Parameters["match"])
	}
	if limit, _ := node.Parameters["limitPerInputRow"].(float64); limit != 20 {
		t.Errorf("limitPerInputRow = %#v, want 20", node.Parameters["limitPerInputRow"])
	}
	var named bool
	for _, issue := range result.Unsupported {
		if issue.Field == "filters" && issue.Severity == n8n.SeverityBlocking &&
			strings.Contains(issue.Reason, "column") {
			named = true
		}
	}
	if !named {
		t.Errorf("no blocking diagnostic names the expression-valued column slot: %#v", result.Unsupported)
	}
}

// TestDataTableToolImportsBesideAnAgent proves n8n-nodes-base.dataTableTool
// imports onto the datastore tool with its ai_tool edge landing on the
// agent's tool port — the same direction the HTTP tool beside it uses.
func TestDataTableToolImportsBesideAnAgent(t *testing.T) {
	t.Parallel()
	if _, found := registry(t).Lookup("kilasflow.datastoreTool", workflow.V(1)); !found {
		t.Skip("kilasflow.datastoreTool is not registered yet; its registration is owned by the datastore-tool ticket")
	}
	const fixture = `{
	  "name": "Table tool",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":3.1,"position":[220,0],"parameters":{}},
	    {"id":"c","name":"Lookup Table","type":"n8n-nodes-base.dataTableTool","typeVersion":1,"position":[220,180],"parameters":{
	      "resource": "row", "operation": "get",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "toolDescription": "Looks up metrics."
	    }}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Agent","type":"main","index":0}]]},
	    "Lookup Table": {"ai_tool": [[{"node":"Agent","type":"ai_tool","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	tool := nodeByName(result.Document, "Lookup Table")
	if tool.Type != "kilasflow.datastoreTool" {
		t.Fatalf("tool imported as %q, want kilasflow.datastoreTool", tool.Type)
	}
	if stringParameter(tool.Parameters, "toolDescription") != "Looks up metrics." {
		t.Errorf("toolDescription = %#v, want the carried description", tool.Parameters["toolDescription"])
	}
	var edged bool
	for _, connection := range result.Document.Connections {
		if connection.Kind != workflow.ConnectionTool {
			continue
		}
		if nodeByName(result.Document, "Agent").ID == connection.Target.NodeID &&
			tool.ID == connection.Source.NodeID {
			edged = true
		}
	}
	if !edged {
		t.Errorf("no ai_tool edge runs from the table tool to the agent: %#v", result.Document.Connections)
	}
	if !hasReason(result.Unsupported, "dt_1") {
		t.Errorf("no diagnostic names the tool's n8n table id: %#v", result.Unsupported)
	}
}

// TestDataTableRoundTripPreservesOperationsAndConnections proves an imported
// Data Table workflow goes back to n8n with its operations under n8n's names
// — rename included — and with every connection that touched it intact. The
// count is the assertion that matters: a dropped middle node used to export
// as two disconnected halves.
func TestDataTableRoundTripPreservesOperationsAndConnections(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Round trip",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Insert","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "insert",
	      "dataTableId": {"mode":"list","value":"dt_1","cachedResultName":"Metrics","__rl":true},
	      "columns": {"mappingMode": "defineBelow", "value": {"metric": "={{ $json.metric }}"}}
	    }},
	    {"id":"c","name":"Rename","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[440,0],"parameters":{
	      "resource": "table", "operation": "update",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"Metrics","__rl":true},
	      "name": "Archive"
	    }}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Insert","type":"main","index":0}]]},
	    "Insert": {"main": [[{"node":"Rename","type":"main","index":0}]]}
	  }
	}`

	imported := importFixture(t, fixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(exported.Document.Nodes) != 3 {
		t.Fatalf("exported %d nodes, want 3", len(exported.Document.Nodes))
	}
	edges := 0
	for _, kinds := range exported.Document.Connections {
		for _, slots := range kinds {
			for _, targets := range slots {
				edges += len(targets)
			}
		}
	}
	if edges != 2 {
		t.Errorf("exported %d connections, want 2: %#v", edges, exported.Document.Connections)
	}
	byName := map[string]map[string]any{}
	for _, node := range exported.Document.Nodes {
		byName[node.Name] = node.Parameters
	}
	if byName["Insert"]["operation"] != "insert" || byName["Insert"]["resource"] != "row" {
		t.Errorf("Insert exported as %#v, want row/insert", byName["Insert"])
	}
	if byName["Rename"]["operation"] != "update" || byName["Rename"]["resource"] != "table" {
		t.Errorf("Rename exported as %#v, want table/update back under n8n's name", byName["Rename"])
	}
	locator, _ := byName["Insert"]["dataTableId"].(map[string]any)
	if locator["mode"] != "list" || locator["value"] != "dt_1" || locator["cachedResultName"] != "Metrics" {
		t.Errorf("locator exported as %#v, want the carried mode, id and cached name", locator)
	}
	columns, _ := byName["Insert"]["columns"].(map[string]any)
	if columns["mappingMode"] != "defineBelow" {
		t.Errorf("columns exported as %#v, want the manual mode back", columns)
	}
	if !hasLossyReason(exported.Lossy, "dataTableId") {
		t.Errorf("no export diagnostic names the datastore identifier as KilasFlow-local: %#v", exported.Lossy)
	}
	var local bool
	for _, issue := range exported.Lossy {
		if issue.Field == "dataTableId" && strings.Contains(issue.Reason, "KilasFlow identifiers") {
			local = true
		}
	}
	if !local {
		t.Errorf("no export diagnostic reports the identifier in credential-reference terms: %#v", exported.Lossy)
	}
}

// TestImportedDataTableWorkflowCompiles proves a representative Data Table
// workflow survives the compiler the way a hand-built one must: the adapter
// emits no parameter the native validator refuses.
func TestImportedDataTableWorkflowCompiles(t *testing.T) {
	t.Parallel()
	const fixture = `{
	  "name": "Compiles",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Insert","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[220,0],"parameters":{
	      "resource": "row", "operation": "insert",
	      "dataTableId": {"mode":"id","value":"dt_1","cachedResultName":"T","__rl":true},
	      "columns": {"mappingMode": "defineBelow", "value": {"metric": "={{ $json.metric }}"}}
	    }},
	    {"id":"c","name":"Read","type":"n8n-nodes-base.dataTable","typeVersion":1,"position":[440,0],"parameters":{
	      "resource": "row", "operation": "get",
	      "dataTableId": {"mode":"name","value":"Metrics","__rl":true},
	      "filters": {"conditions": [{"keyName": "metric", "condition": "eq", "keyValue": "cpu"}]}
	    }}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Insert","type":"main","index":0}]]},
	    "Insert": {"main": [[{"node":"Read","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	document := result.Document
	document.ID = "wf_datatable"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Errorf("datatable fixture did not compile after import: %v", err)
	}
}

// TestDatastoreTypeMatchesTheNodePack pins the canonical type the adapter
// mirrors from the node pack. It is duplicated rather than imported so the
// adapter does not depend on the pack, and a silent drift between them would
// send imports to a node type nothing registers.
func TestDatastoreTypeMatchesTheNodePack(t *testing.T) {
	t.Parallel()
	if n8n.DatastoreNodeType != nodes.DatastoreNodeType {
		t.Errorf("adapter mirrors %q, node pack registers %q", n8n.DatastoreNodeType, nodes.DatastoreNodeType)
	}
}
