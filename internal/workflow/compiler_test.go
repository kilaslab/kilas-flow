package workflow_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// stubCatalog is a catalogue of exactly the definitions a test declares.
//
// The compiler's own tests live here rather than in the node package so that a
// compiler rule can be stated without a real node happening to exercise it —
// and so that changing a real node cannot quietly change what a compiler test
// proves.
type stubCatalog struct {
	definitions map[string]workflow.NodeDefinition
}

func (catalog stubCatalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, found := catalog.definitions[nodeType]
	if !found {
		return workflow.NodeDefinition{}, false
	}
	definition.Version = version
	return definition, true
}

func (catalog stubCatalog) HasType(nodeType string) bool {
	_, found := catalog.definitions[nodeType]
	return found
}

// A node whose whole request is built from a credential must not compile
// without one.
//
// Before this, such a node activated and failed at its first outbound call with
// an error about a URL — because the base URL template resolved to nothing —
// which named a symptom rather than the cause. The database nodes had the check
// hand-written in their own Validate; a generated pack has no Validate and
// cannot have one, which is the point of a pack being data.
func TestCompileRequiresADeclaredCredential(t *testing.T) {
	t.Parallel()

	catalog := stubCatalog{definitions: map[string]workflow.NodeDefinition{
		"test.trigger": {
			Type: "test.trigger", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"test.needsCredential": {
			Type: "test.needsCredential", Version: workflow.V(1),
			Inputs:              []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			RequiredCredentials: []string{"someApi"},
		},
		"test.optionalCredential": {
			Type: "test.optionalCredential", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}}

	document := func(nodeType string, credentials map[string]string) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_credential", Name: "Needs a credential",
			Nodes: []workflow.Node{
				{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
				{ID: "node", Name: "Node", Type: nodeType, TypeVersion: workflow.V(1), Credentials: credentials},
			},
			Connections: []workflow.Connection{{
				ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
				Target: workflow.Endpoint{NodeID: "node", Port: "main"},
			}},
			Settings: map[string]any{},
		}
	}

	_, err := workflow.Compile(document("test.needsCredential", nil), catalog)
	if err == nil || !strings.Contains(err.Error(), "requires a someApi credential") {
		t.Fatalf("Compile() = %v, want the credential named", err)
	}
	// An attached-but-empty reference is not attached: an imported node arrives
	// with its foreign reference dropped, and treating the empty string as a
	// credential would let exactly that node through.
	if _, err := workflow.Compile(document("test.needsCredential", map[string]string{"someApi": "   "}), catalog); err == nil {
		t.Error("Compile() accepted an empty credential reference")
	}
	if _, err := workflow.Compile(document("test.needsCredential", map[string]string{"someApi": "cred-1"}), catalog); err != nil {
		t.Errorf("Compile() with the credential = %v, want accepted", err)
	}
	// A node whose credential is optional still compiles with none.
	if _, err := workflow.Compile(document("test.optionalCredential", nil), catalog); err != nil {
		t.Errorf("Compile() of a node with no required credential = %v, want accepted", err)
	}
}

// Two tools claiming one model-facing name must fail compilation naming both
// nodes, not fail mid-run on a workflow the compiler already accepted.
//
// The types here are generic on purpose: the rule reads only the tool
// channel, the canvas names and the optional override, so a stub agent
// proves it without a real node happening to exercise it.
func TestCompileRefusesDuplicateToolNames(t *testing.T) {
	t.Parallel()

	catalog := stubCatalog{definitions: map[string]workflow.NodeDefinition{
		"test.trigger": {
			Type: "test.trigger", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"test.agent": {
			Type: "test.agent", Version: workflow.V(1),
			Inputs: []workflow.Port{
				{Name: "main", Kind: workflow.ConnectionMain},
				{Name: "tools", Kind: workflow.ConnectionTool},
			},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"test.tool": {
			Type: "test.tool", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}},
		},
	}}

	document := func(tools []workflow.Node) workflow.Document {
		nodes := []workflow.Node{
			{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
			{ID: "agent", Name: "Agent", Type: "test.agent", TypeVersion: workflow.V(1)},
		}
		connections := []workflow.Connection{{
			ID: "c0", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
			Target: workflow.Endpoint{NodeID: "agent", Port: "main"},
		}}
		for index, tool := range tools {
			nodes = append(nodes, tool)
			connections = append(connections, workflow.Connection{
				ID:     fmt.Sprintf("c%d", index+1),
				Kind:   workflow.ConnectionTool,
				Source: workflow.Endpoint{NodeID: tool.ID, Port: "tool"},
				Target: workflow.Endpoint{NodeID: "agent", Port: "tools"},
			})
		}
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_tools", Name: "Tool names",
			Nodes:       nodes,
			Connections: connections,
			Settings:    map[string]any{},
		}
	}
	tool := func(id, name string, parameters map[string]any) workflow.Node {
		return workflow.Node{
			ID: id, Name: name, Type: "test.tool", TypeVersion: workflow.V(1),
			Parameters: parameters,
		}
	}

	// Two tools under one canvas name claim one tool name.
	_, err := workflow.Compile(document([]workflow.Node{
		tool("tool-a", "Weather", nil),
		tool("tool-b", "Weather", nil),
	}), catalog)
	if err == nil {
		t.Fatal("Compile() accepted two tools under one name")
	}
	for _, want := range []string{"Weather", "Weather"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err.Error(), want)
		}
	}

	// Distinct canvas names that normalise alike collide too.
	if _, err := workflow.Compile(document([]workflow.Node{
		tool("tool-a", "Get Weather", nil),
		tool("tool-b", "Get_Weather", nil),
	}), catalog); err == nil {
		t.Error("Compile() accepted two tools that normalise to one name")
	}

	// An explicit override collides with a derived name all the same.
	if _, err := workflow.Compile(document([]workflow.Node{
		tool("tool-a", "Weather", map[string]any{"toolName": "Radar"}),
		tool("tool-b", "Radar", nil),
	}), catalog); err == nil {
		t.Error("Compile() accepted an override colliding with a derived name")
	}

	// Distinct names compile, and the same name on two agents is fine: each
	// model context resolves its own tools.
	if _, err := workflow.Compile(document([]workflow.Node{
		tool("tool-a", "Weather", nil),
		tool("tool-b", "Radar", nil),
	}), catalog); err != nil {
		t.Errorf("Compile() of distinct tools = %v, want accepted", err)
	}
}

func TestNormalizeToolNameMatchesTheRuntimeRule(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"Get Weather":   "Get_Weather",
		"Get_Weather":   "Get_Weather",
		"Cuaca Jakarta": "Cuaca_Jakarta",
		"":              "http_request",
	} {
		if got := workflow.NormalizeToolName(name); got != want {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", name, got, want)
		}
	}
}
