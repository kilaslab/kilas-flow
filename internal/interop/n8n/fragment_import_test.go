package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// clipboardFixture is what n8n puts on the clipboard when three nodes and a
// Code node are copied: nodes, the name-keyed connections, and the instance
// metadata n8n adds to every copy. It is BUG-txafja's reproduction.
const clipboardFixture = `{
  "nodes": [
    {"id":"m","name":"When clicking 'Execute workflow'","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"s","name":"Edit Fields","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
     "parameters":{"assignments":{"assignments":[{"id":"a1","name":"city","value":"Oslo","type":"string"}]},"options":{}}},
    {"id":"h","name":"HTTP Request","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[440,0],
     "parameters":{"url":"=https://example.com/{{ $json.city }}","options":{}}},
    {"id":"c","name":"Code","type":"n8n-nodes-base.code","typeVersion":2,"position":[660,0],
     "parameters":{"jsCode":"return items.map((item) => ({ json: { city: item.json.city } }));"}}
  ],
  "connections": {
    "When clicking 'Execute workflow'": {"main": [[{"node":"Edit Fields","type":"main","index":0}]]},
    "Edit Fields": {"main": [[{"node":"HTTP Request","type":"main","index":0}]]},
    "HTTP Request": {"main": [[{"node":"Code","type":"main","index":0}]]}
  },
  "pinData": {"Edit Fields": [{"json": {"city": "Oslo"}}]},
  "meta": {"instanceId": "0123456789abcdef"}
}`

func fragmentNode(t *testing.T, result n8n.ImportResult, id string) workflow.Node {
	t.Helper()
	for _, candidate := range result.Document.Nodes {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("the fragment has no node %q", id)
	return workflow.Node{}
}

func TestAPastedFragmentConvertsAsAnImportWould(t *testing.T) {
	t.Parallel()

	catalog := registry(t)
	pasted, err := n8n.ImportFragment([]byte(clipboardFixture), catalog)
	if err != nil {
		t.Fatalf("ImportFragment() error = %v", err)
	}
	imported, err := n8n.Import([]byte(clipboardFixture), catalog)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	// The same nodes, parameters and connections as Import: one translator.
	if len(pasted.Document.Nodes) != len(imported.Document.Nodes) || len(pasted.Document.Connections) != len(imported.Document.Connections) {
		t.Fatalf("fragment = %d nodes / %d connections, import = %d / %d",
			len(pasted.Document.Nodes), len(pasted.Document.Connections), len(imported.Document.Nodes), len(imported.Document.Connections))
	}
	for index := range imported.Document.Nodes {
		if pasted.Document.Nodes[index].Type != imported.Document.Nodes[index].Type {
			t.Errorf("node %d: fragment type %q, import type %q", index, pasted.Document.Nodes[index].Type, imported.Document.Nodes[index].Type)
		}
	}

	// The three things a client-side paste got wrong.
	if got := fragmentNode(t, pasted, "m").Type; got != "kilasflow.manual" {
		t.Errorf("Manual Trigger pasted as %q, want kilasflow.manual", got)
	}
	code := fragmentNode(t, pasted, "c")
	if code.Type != "kilasflow.jsCode" {
		t.Errorf("JavaScript Code node pasted as %q, want kilasflow.jsCode", code.Type)
	}
	if source, _ := code.Parameters["jsCode"].(string); !strings.Contains(source, "item.json.city") {
		t.Errorf("JavaScript source = %q, want it carried", source)
	}
	url, ok := fragmentNode(t, pasted, "h").Parameters["url"].(map[string]any)
	if !ok || url["mode"] != "expression" || !strings.Contains(url["value"].(string), "$json.city") {
		t.Errorf("url = %#v, want the '=' value as an expression", fragmentNode(t, pasted, "h").Parameters["url"])
	}
	if len(pasted.Document.Connections) != 3 {
		t.Errorf("connections = %d, want 3", len(pasted.Document.Connections))
	}
}

func TestAPastedFragmentReportsOnlyWhatThePasteLost(t *testing.T) {
	t.Parallel()

	catalog := registry(t)
	pasted, err := n8n.ImportFragment([]byte(clipboardFixture), catalog)
	if err != nil {
		t.Fatalf("ImportFragment() error = %v", err)
	}
	imported, err := n8n.Import([]byte(clipboardFixture), catalog)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	fields := func(issues []n8n.ImportIssue) map[string]bool {
		seen := map[string]bool{}
		for _, issue := range issues {
			seen[issue.Field] = true
		}
		return seen
	}

	// n8n's instance metadata rides on every copy; the workflow the paste
	// lands in keeps its own, so a whole import reports it and a paste does not.
	if !fields(imported.Unsupported)["meta"] {
		t.Fatal("the fixture's metadata is not reported by Import; the test no longer proves anything")
	}
	if fields(pasted.Unsupported)["meta"] {
		t.Error("a paste reports n8n's clipboard metadata as dropped")
	}
	// Pinned data is about the pasted nodes: they will run for real.
	if !fields(pasted.Unsupported)["pinData"] {
		t.Error("a paste no longer reports the pinned data it dropped")
	}
}

func TestAPastedFragmentRefusesWhatImportRefuses(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]string{
		"not JSON":        `nodes: [`,
		"no nodes":        `{"nodes": [], "connections": {}}`,
		"duplicate names": `{"nodes":[{"name":"A","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[0,0]},{"name":"A","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[0,0]}],"connections":{}}`,
	} {
		if _, err := n8n.ImportFragment([]byte(payload), registry(t)); err == nil {
			t.Errorf("%s: ImportFragment() error = nil, want the import's refusal", name)
		}
	}
}
