package n8n_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// codeFixture is a workflow with one n8n Code node whose parameters are given
// as raw JSON, so a test can say exactly which keys the source carried.
func codeFixture(typeVersion int, parameters string) string {
	return `{
	  "name": "Scripted",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Code","type":"n8n-nodes-base.code","typeVersion":` + string(rune('0'+typeVersion)) + `,"position":[220,0],"parameters":` + parameters + `}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Code","type":"main","index":0}]]}}
	}`
}

func blockingFor(result n8n.ImportResult, name string) []n8n.ImportIssue {
	var blocking []n8n.ImportIssue
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.NodeName == name {
			blocking = append(blocking, issue)
		}
	}
	return blocking
}

// A JavaScript Code node now runs, so it imports as the runnable node with
// nothing blocking it, and goes back out as n8n wrote it: the source byte for
// byte, CRLF line ends, tabs, trailing space and non-ASCII included, and never
// read as an expression.
func TestAJavaScriptCodeNodeImportsRunnableAndRoundTripsByteForByte(t *testing.T) {
	t.Parallel()

	source := "  // {{ $json.x }} = not an expression here  \r\nconst greeting = 'héllo 😀';\n\treturn items.map(item => ({ json: { ...item.json, greeting } }));\n"
	encoded, _ := json.Marshal(source)
	for _, mode := range []string{"runOnceForAllItems", "runOnceForEachItem"} {
		body := source
		if mode == "runOnceForEachItem" {
			body = "return { json: { ...$json, each: true } };"
			encoded, _ = json.Marshal(body)
		}
		result := importFixture(t, codeFixture(2, `{"language":"javaScript","mode":"`+mode+`","jsCode":`+string(encoded)+`}`))
		code := nodeByName(result.Document, "Code")
		if code.Type != n8n.JSCodeNodeType {
			t.Fatalf("%s: code node = %q, want the runnable Code (JavaScript) node", mode, code.Type)
		}
		if blocking := blockingFor(result, "Code"); len(blocking) != 0 {
			t.Fatalf("%s: blocking issues %#v, want none", mode, blocking)
		}
		if code.Parameters["jsCode"] != body || code.Parameters["mode"] != mode {
			t.Fatalf("%s: parameters = %#v, want the source and mode copied exactly", mode, code.Parameters)
		}
		exported, err := n8n.Export(result.Document, registry(t))
		if err != nil {
			t.Fatalf("%s: Export() error = %v", mode, err)
		}
		for _, node := range exported.Document.Nodes {
			if node.Name != "Code" {
				continue
			}
			if node.Type != "n8n-nodes-base.code" || node.Parameters["jsCode"] != body || node.Parameters["language"] != "javaScript" {
				t.Errorf("%s: exported %s %#v, want the n8n Code node with its source unchanged", mode, node.Type, node.Parameters)
			}
		}
	}
}

// n8n's first Code node version has no language parameter; its code is
// JavaScript.
func TestACodeNodeWithoutALanguageIsJavaScript(t *testing.T) {
	t.Parallel()

	result := importFixture(t, codeFixture(1, `{"jsCode":"return items;"}`))
	if code := nodeByName(result.Document, "Code"); code.Type != n8n.JSCodeNodeType {
		t.Fatalf("code node = %q, want the runnable Code (JavaScript) node", code.Type)
	}
	if blocking := blockingFor(result, "Code"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
}

// A construct the engine cannot run faithfully is refused at import in the
// words the node's own validation uses, so the import report and the editor
// say the same thing.
func TestAnUnsupportedConstructRefusesAtImportInTheSameWords(t *testing.T) {
	t.Parallel()

	const source = "const fs = require('fs');\nreturn items;"
	encoded, _ := json.Marshal(source)
	result := importFixture(t, codeFixture(2, `{"jsCode":`+string(encoded)+`}`))
	blocking := blockingFor(result, "Code")
	if len(blocking) != 1 {
		t.Fatalf("blocking issues %#v, want exactly one", blocking)
	}
	definition, ok := registry(t).Resolve(nodes.JSCodeNodeType, workflow.V(1))
	if !ok {
		t.Fatal("the Code (JavaScript) node is not registered")
	}
	validation := definition.Validate(workflow.Node{Parameters: map[string]any{"jsCode": source}})
	if validation == nil || blocking[0].Reason != validation.Error() {
		t.Fatalf("import says %q, validation says %v; want the same sentence", blocking[0].Reason, validation)
	}
	if !strings.HasPrefix(validation.Error(), `this node's code requires the module "fs" (line 1), which this server does not run.`) {
		t.Fatalf("validation = %q", validation)
	}
}
