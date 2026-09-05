package workflow_test

import (
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
