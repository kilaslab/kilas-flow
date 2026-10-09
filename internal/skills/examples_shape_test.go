package skills

import (
	"strings"
	"testing"
)

// fence builds a fenced block the way a reference file writes one.
func fence(info, body string) string {
	return "```" + info + "\n" + body + "\n```\n"
}

const goodDocument = `{
  "schemaVersion": 1,
  "name": "demo",
  "nodes": [
    {"id": "a", "name": "A", "type": "kilasflow.manual", "typeVersion": 1, "position": {"x": 0, "y": 0}},
    {"id": "b", "name": "B", "type": "kilasflow.set", "typeVersion": 1, "position": {"x": 1, "y": 2}}
  ],
  "connections": [
    {"id": "c1", "kind": "main", "source": {"nodeId": "a", "port": "main"}, "target": {"nodeId": "b", "port": "main"}}
  ],
  "settings": {}
}`

func skillWith(body string, references map[string]string) Skill {
	loaded := Skill{Name: "kilasflow-fixture", Path: "skills/kilasflow-fixture/SKILL.md", Body: body, ReferenceText: map[string]string{}}
	for name, text := range references {
		loaded.References = append(loaded.References, name)
		loaded.ReferenceText[name] = text
	}

	return loaded
}

func TestExamplesReadsWorkflowAndRequestFences(t *testing.T) {
	loaded := skillWith("prose\n\n"+fence("json kilasflow-workflow", goodDocument)+"\nmore prose\n", map[string]string{
		"references/EXAMPLE.md": "# Example\n\n" + fence("json kilasflow-request create-credential", `{"name": "n", "type": "t", "fields": {}}`) +
			"\n" + fence("json", `{"not": "an example"}`) + "\n" + fence("bash", "kilasflow context"),
	})

	found, err := Examples(loaded)
	if err != nil {
		t.Fatalf("Examples() error = %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("Examples() = %d examples, want 2: %+v", len(found), found)
	}

	workflow, request := found[0], found[1]
	if workflow.Kind != ExampleWorkflow || workflow.Path != "skills/kilasflow-fixture/SKILL.md" {
		t.Errorf("first example = %+v, want a workflow in the SKILL.md", workflow)
	}
	if request.Kind != ExampleRequest || request.Operation != "create-credential" || request.Path != "skills/kilasflow-fixture/references/EXAMPLE.md" {
		t.Errorf("second example = %+v, want a create-credential request in references/EXAMPLE.md", request)
	}
	if !strings.Contains(workflow.JSON, `"schemaVersion": 1`) {
		t.Errorf("the workflow example lost its body: %q", workflow.JSON)
	}
}

func TestExamplesRefusesAMalformedInfoString(t *testing.T) {
	for name, info := range map[string]string{
		"request without an operation": "json kilasflow-request",
		"unknown example kind":         "json kilasflow-sketch",
		"workflow with an operation":   "json kilasflow-workflow create-workflow",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Examples(skillWith(fence(info, "{}"), nil))
			if err == nil {
				t.Fatalf("Examples() accepted the info string %q", info)
			}
		})
	}
}

func TestExamplesRefusesAnUnterminatedFence(t *testing.T) {
	if _, err := Examples(skillWith("```json kilasflow-workflow\n{}\n", nil)); err == nil {
		t.Fatal("Examples() accepted a fence that never closes")
	}
}

func TestCheckExampleShape(t *testing.T) {
	good := Example{Kind: ExampleWorkflow, JSON: goodDocument}
	if err := CheckExampleShape(good); err != nil {
		t.Fatalf("CheckExampleShape(good) = %v", err)
	}

	cases := map[string]string{
		"not json":                `{"schemaVersion": 1`,
		"a missing top-level key": strings.Replace(goodDocument, `"settings": {}`, `"extra": {}`, 1),
		"a server-owned id":       strings.Replace(goodDocument, `"name": "demo",`, `"name": "demo", "id": "wf_1",`, 1),
		"one node": strings.Replace(goodDocument, `,
    {"id": "b", "name": "B", "type": "kilasflow.set", "typeVersion": 1, "position": {"x": 1, "y": 2}}`, "", 1),
		"no connection":              strings.Replace(goodDocument, `{"id": "c1", "kind": "main", "source": {"nodeId": "a", "port": "main"}, "target": {"nodeId": "b", "port": "main"}}`, "", 1),
		"a node without typeVersion": strings.Replace(goodDocument, `"typeVersion": 1, "position": {"x": 1, "y": 2}`, `"position": {"x": 1, "y": 2}`, 1),
		"a position as an array":     strings.Replace(goodDocument, `"position": {"x": 1, "y": 2}`, `"position": [1, 2]`, 1),
		"a position without y":       strings.Replace(goodDocument, `"position": {"x": 1, "y": 2}`, `"position": {"x": 1}`, 1),
		"a connection without kind":  strings.Replace(goodDocument, `"kind": "main", `, "", 1),
		"a source without a port":    strings.Replace(goodDocument, `"source": {"nodeId": "a", "port": "main"}`, `"source": {"nodeId": "a"}`, 1),
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if err := CheckExampleShape(Example{Kind: ExampleWorkflow, JSON: document}); err == nil {
				t.Fatalf("CheckExampleShape accepted %s", name)
			}
		})
	}
}

func TestCheckExampleShapeOfARequest(t *testing.T) {
	if err := CheckExampleShape(Example{Kind: ExampleRequest, Operation: "create-credential", JSON: `{"name": "n"}`}); err != nil {
		t.Fatalf("CheckExampleShape(request) = %v", err)
	}
	if err := CheckExampleShape(Example{Kind: ExampleRequest, Operation: "create-credential", JSON: `[1]`}); err == nil {
		t.Error("CheckExampleShape accepted a request body that is not an object")
	}
	if err := CheckExampleShape(Example{Kind: ExampleRequest, JSON: `{}`}); err == nil {
		t.Error("CheckExampleShape accepted a request with no operation id")
	}
}
