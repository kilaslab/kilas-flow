package workflow_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

type catalog map[string]workflow.NodeDefinition

func (c catalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	definition, ok := c[nodeType]
	return definition, ok && definition.Version.Compare(version) == 0
}

func TestDecodeDocumentRejectsUnknownStructuralFields(t *testing.T) {
	_, err := workflow.DecodeDocument(bytes.NewBufferString(`{
        "schemaVersion": 1,
        "id": "wf_019",
        "name": "Draft",
        "nodes": [],
        "connections": [],
        "settings": {},
        "foreign": true
    }`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeDocument() error = %v, want unknown-field error", err)
	}
}

func TestDecodeDocumentRejectsMissingRequiredNestedFields(t *testing.T) {
	_, err := workflow.DecodeDocument(bytes.NewBufferString(`{
        "schemaVersion": 1,
        "id": "wf_019",
        "name": "Draft",
        "nodes": [{
            "id": "set",
            "name": "Set",
            "type": "kilasflow.set",
            "typeVersion": 1
        }],
        "connections": [],
        "settings": {}
    }`))
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("DecodeDocument() error = %v, want missing-position error", err)
	}
}

func TestDecodeDocumentRejectsSchemaInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		document string
		want     string
	}{
		{
			name: "workflow name longer than the schema maximum",
			document: `{
                "schemaVersion": 1,
                "id": "wf_019",
                "name": "` + strings.Repeat("x", 256) + `",
                "nodes": [],
                "connections": [],
                "settings": {}
            }`,
			want: "255",
		},
		{
			name: "null credential reference",
			document: `{
                "schemaVersion": 1,
                "id": "wf_019",
                "name": "Draft",
                "nodes": [{
                    "id": "set",
                    "name": "Set",
                    "type": "kilasflow.set",
                    "typeVersion": 1,
                    "position": {"x": 0, "y": 0},
                    "credentials": {"apiKey": null}
                }],
                "connections": [],
                "settings": {}
            }`,
			want: "credential",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := workflow.DecodeDocument(bytes.NewBufferString(test.document))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeDocument() error = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCanonicalJSONSchemaDeclaresVersionOneDocument(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return this test file")
	}
	schemaPath := filepath.Join(filepath.Dir(testFile), "..", "..", "schemas", "workflow-v1.schema.json")

	contents, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", schemaPath, err)
	}
	var schema struct {
		Schema     string `json:"$schema"`
		ID         string `json:"$id"`
		Properties struct {
			SchemaVersion struct {
				Const int `json:"const"`
			} `json:"schemaVersion"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("schema JSON is invalid: %v", err)
	}
	if schema.Schema == "" || schema.ID == "" {
		t.Fatalf("schema metadata = %#v, want $schema and $id", schema)
	}
	if got, want := schema.Properties.SchemaVersion.Const, workflow.CurrentSchemaVersion; got != want {
		t.Errorf("schemaVersion const = %d, want %d", got, want)
	}
}

func (c catalog) HasType(nodeType string) bool {
	_, ok := c[nodeType]
	return ok
}

func TestCompileRejectsCycleWithStructuredTopologyError(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Cyclic draft",
		Nodes: []workflow.Node{
			{ID: "first", Name: "First", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
			{ID: "second", Name: "Second", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{
				ID: "edge-first-second", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "first", Port: "main"},
				Target: workflow.Endpoint{NodeID: "second", Port: "main"},
			},
			{
				ID: "edge-second-first", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "second", Port: "main"},
				Target: workflow.Endpoint{NodeID: "first", Port: "main"},
			},
		},
		Settings: map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	for _, issue := range validationErrors.Issues {
		if issue.Code == workflow.ErrorInvalidTopology {
			return
		}
	}
	t.Fatalf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
}

// TestCompileDistinguishesUnknownNodeVersion exercises the compiler's error
// branching against a catalog that refuses the version outright. Whether a
// given catalog refuses is the catalog's policy, not the compiler's — the real
// registry resolves downward, which
// TestCompileResolvesAVersionTheRegistryDoesNotHave covers.
func TestCompileDistinguishesUnknownNodeVersion(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Older node version",
		Nodes: []workflow.Node{{
			ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(2),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {Type: "kilasflow.set", Version: workflow.V(1)},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if got, want := validationErrors.Issues[0].Code, workflow.ErrorUnknownVersion; got != want {
		t.Errorf("validation code = %q, want %q", got, want)
	}
}

func TestCompileRejectsEmptyGraphAsInvalidTopology(t *testing.T) {
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Empty draft",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}, catalog{})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if got, want := validationErrors.Issues[0].Code, workflow.ErrorInvalidTopology; got != want {
		t.Errorf("validation code = %q, want %q", got, want)
	}
}

func TestCompileRejectsASecondChatTrigger(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_chat_two",
		Name:          "Two chats",
		Nodes: []workflow.Node{
			{ID: "chat-1", Name: "Chat 1", Type: "kilasflow.chatTrigger", TypeVersion: workflow.V(1)},
			{ID: "chat-2", Name: "Chat 2", Type: "kilasflow.chatTrigger", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, registry)

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidTopology) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
	}
	found := false
	for _, issue := range validationErrors.Issues {
		if issue.NodeID == "chat-2" && strings.Contains(issue.Message, "only one When chat message received") {
			found = true
		}
	}
	if !found {
		t.Errorf("validation issues = %#v, want the second chat trigger named", validationErrors.Issues)
	}

	if _, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_chat_one",
		Name:          "One chat",
		Nodes: []workflow.Node{
			{ID: "chat", Name: "Chat", Type: "kilasflow.chatTrigger", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, registry); err != nil {
		t.Errorf("Compile(one chat trigger) error = %v, want success", err)
	}
}

func TestCompileRejectsNodeDisconnectedFromTheManualTrigger(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Disconnected node",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
			{ID: "orphan", Name: "Orphan", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"status": "orphan"}}},
		},
		Connections: []workflow.Connection{{
			ID: "manual-set", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"assignments"},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidTopology) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
	}
}

func TestCompileRejectsMissingRequiredConfiguration(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Incomplete draft",
		Nodes: []workflow.Node{{
			ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			RequiredParameters: []string{"assignments"},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if got, want := validationErrors.Issues[0].Code, workflow.ErrorRequiredConfig; got != want {
		t.Errorf("validation code = %q, want %q", got, want)
	}
}

func TestCompileRejectsMalformedCoreIFConfiguration(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_022",
		Name:          "Malformed IF",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1), Parameters: map[string]any{"conditions": []any{"not-a-condition"}}},
		},
		Connections: []workflow.Connection{{
			ID: "manual-if", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "if", Port: "main"},
		}},
		Settings: map[string]any{},
	}, registry)

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidConfig) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidConfig)
	}
}

func TestCompileReturnsStructuredConnectionErrors(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Invalid connections",
		Nodes: []workflow.Node{
			{ID: "source", Name: "Source", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
			{ID: "target", Name: "Target", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{
				ID: "missing-port", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "source", Port: "missing"},
				Target: workflow.Endpoint{NodeID: "target", Port: "main"},
			},
			{
				ID: "wrong-kind", Kind: workflow.ConnectionTool,
				Source: workflow.Endpoint{NodeID: "source", Port: "main"},
				Target: workflow.Endpoint{NodeID: "target", Port: "main"},
			},
		},
		Settings: map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	for _, wanted := range []workflow.ErrorCode{workflow.ErrorUnknownPort, workflow.ErrorIncompatiblePort} {
		if !containsValidationCode(validationErrors.Issues, wanted) {
			t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, wanted)
		}
	}
}

func TestCompileRejectsDuplicateConnections(t *testing.T) {
	connection := workflow.Connection{
		Kind:   workflow.ConnectionMain,
		Source: workflow.Endpoint{NodeID: "source", Port: "main"},
		Target: workflow.Endpoint{NodeID: "target", Port: "main"},
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Duplicate connection",
		Nodes: []workflow.Node{
			{ID: "source", Name: "Source", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
			{ID: "target", Name: "Target", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "first", Kind: connection.Kind, Source: connection.Source, Target: connection.Target},
			{ID: "second", Kind: connection.Kind, Source: connection.Source, Target: connection.Target},
		},
		Settings: map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidTopology) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
	}
}

func TestCompileRejectsUnknownNodeType(t *testing.T) {
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Unknown node",
		Nodes: []workflow.Node{{
			ID: "unknown", Name: "Unknown", Type: "kilasflow.unknown", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, catalog{})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if got, want := validationErrors.Issues[0].Code, workflow.ErrorUnknownNode; got != want {
		t.Errorf("validation code = %q, want %q", got, want)
	}
}

func containsValidationCode(issues []workflow.ValidationError, wanted workflow.ErrorCode) bool {
	for _, issue := range issues {
		if issue.Code == wanted {
			return true
		}
	}
	return false
}

func TestCompileBuildsIRForLabeledIFOutputs(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Branch customers",
		Nodes: []workflow.Node{
			{
				ID:          "manual",
				Name:        "Manual Trigger",
				Type:        "kilasflow.manualTrigger",
				TypeVersion: workflow.V(1),
				Position:    workflow.Position{X: 0, Y: 0},
			},
			{
				ID:          "if",
				Name:        "IF",
				Type:        "kilasflow.if",
				TypeVersion: workflow.V(1),
				Position:    workflow.Position{X: 240, Y: 0},
				Parameters:  map[string]any{"conditions": []any{"customer"}},
			},
			{
				ID:          "false-set",
				Name:        "False branch",
				Type:        "kilasflow.set",
				TypeVersion: workflow.V(1),
				Position:    workflow.Position{X: 480, Y: 120},
			},
		},
		Connections: []workflow.Connection{
			{
				ID: "edge-manual-if", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "if", Port: "main"},
			},
			{
				ID: "edge-if-false", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "if", Port: "false"},
				Target: workflow.Endpoint{NodeID: "false-set", Port: "main"},
			},
		},
		Settings: map[string]any{},
	}

	if err := workflow.ValidateDraft(document); err != nil {
		t.Fatalf("ValidateDraft() error = %v", err)
	}

	ir, err := workflow.Compile(document, catalog{
		"kilasflow.manualTrigger": {
			Type: "kilasflow.manualTrigger", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.if": {
			Type: "kilasflow.if", Version: workflow.V(1),
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "true", Kind: workflow.ConnectionMain}, {Name: "false", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"conditions"},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if got, want := len(ir.Nodes), 3; got != want {
		t.Fatalf("compiled nodes = %d, want %d", got, want)
	}
	if got, want := len(ir.Edges), 2; got != want {
		t.Fatalf("compiled edges = %d, want %d", got, want)
	}
	if got, want := ir.Edges[1].SourceOutputIndex, 1; got != want {
		t.Errorf("false output index = %d, want %d", got, want)
	}
}

func TestCompileCopiesCanonicalDataIntoIndependentIR(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Independent IR",
		Nodes: []workflow.Node{{
			ID:          "set",
			Name:        "Set",
			Type:        "kilasflow.set",
			TypeVersion: workflow.V(1),
			Position:    workflow.Position{},
			Parameters: map[string]any{
				"nested": map[string]any{"value": "before"},
			},
			Credentials: map[string]string{"authentication": "cred_019"},
			Settings:    map[string]any{"retry": map[string]any{"max": 1}},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{"execution": map[string]any{"timeout": 30}},
	}

	ir, err := workflow.Compile(document, catalog{
		// A single-node graph still needs an item-producing root; this test is
		// about IR data independence, not topology.
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	document.Nodes[0].Parameters["nested"].(map[string]any)["value"] = "after"
	document.Nodes[0].Credentials["authentication"] = "cred_changed"
	document.Nodes[0].Settings["retry"].(map[string]any)["max"] = 2
	document.Settings["execution"].(map[string]any)["timeout"] = 60

	if got, want := ir.Nodes[0].Parameters["nested"].(map[string]any)["value"], "before"; got != want {
		t.Errorf("IR parameter = %q, want %q", got, want)
	}
	if got, want := ir.Nodes[0].Credentials["authentication"], "cred_019"; got != want {
		t.Errorf("IR credential = %q, want %q", got, want)
	}
	if got, want := ir.Nodes[0].Settings["retry"].(map[string]any)["max"], 1; got != want {
		t.Errorf("IR node setting = %d, want %d", got, want)
	}
	if got, want := ir.Settings["execution"].(map[string]any)["timeout"], 30; got != want {
		t.Errorf("IR workflow setting = %d, want %d", got, want)
	}
}

// TestCompileAcceptsAnAnnotationConnectedToNothing pins the rule a canvas
// annotation depends on: a node declaring no ports in either direction cannot
// be connected to anything, so it is neither a trigger root nor an orphan.
//
// It sits directly against TestCompileRejectsNodeDisconnectedFromTheManualTrigger:
// a node with ports that is not wired up is still an error. Without the
// exemption every annotated workflow would be permanently unactivatable, and
// almost every real n8n workflow is annotated.
func TestCompileAcceptsAnAnnotationConnectedToNothing(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Annotated",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1), Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
			{ID: "note", Name: "Sticky Note", Type: "kilasflow.stickyNote", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "manual-set", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}

	ir, err := workflow.Compile(document, catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"assignments"},
		},
		// No ports at all, in either direction.
		"kilasflow.stickyNote": {Type: "kilasflow.stickyNote", Version: workflow.V(1)},
	})
	if err != nil {
		t.Fatalf("a workflow with an unconnected annotation must compile: %v", err)
	}
	// Exempt from the topology rules, not dropped: the editor would lose it on
	// every save if compilation removed it.
	var kept bool
	for _, node := range ir.Nodes {
		if node.ID == "note" {
			kept = true
		}
	}
	if !kept {
		t.Error("the annotation was dropped from the compiled graph")
	}
}

// TestCompileRejectsAWorkflowThatIsOnlyAnAnnotation proves the exemption does
// not go too far. An annotation supplies no items, so a document containing
// nothing else has no trigger, and saying so is the honest answer.
func TestCompileRejectsAWorkflowThatIsOnlyAnAnnotation(t *testing.T) {
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Only a note",
		Nodes:         []workflow.Node{{ID: "note", Name: "Sticky Note", Type: "kilasflow.stickyNote", TypeVersion: workflow.V(1)}},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}, catalog{
		"kilasflow.stickyNote": {Type: "kilasflow.stickyNote", Version: workflow.V(1)},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidTopology) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
	}
}

// TestDocumentSavedBeforeFractionalVersionsStillLoads is the compatibility
// property. Every workflow persisted before typeVersion became a decimal wrote
// it as a plain integer, and those documents must load, compile and re-encode
// unchanged — the wire format did not change, only the Go type behind it.
func TestDocumentSavedBeforeFractionalVersionsStillLoads(t *testing.T) {
	const persisted = `{
        "schemaVersion": 1,
        "id": "wf_019",
        "name": "Saved before the change",
        "nodes": [
            {"id":"manual","name":"Manual Trigger","type":"kilasflow.manual","typeVersion":1,"position":{"x":0,"y":0}},
            {"id":"set","name":"Set","type":"kilasflow.set","typeVersion":1,"position":{"x":260,"y":0},
             "parameters":{"assignments":{"status":"ready"}}}
        ],
        "connections": [
            {"id":"manual-set","kind":"main",
             "source":{"nodeId":"manual","port":"main"},
             "target":{"nodeId":"set","port":"main"}}
        ],
        "settings": {}
    }`

	document, err := workflow.DecodeDocument(bytes.NewBufferString(persisted))
	if err != nil {
		t.Fatalf("DecodeDocument() error = %v", err)
	}
	if got := document.Nodes[0].TypeVersion.String(); got != "1" {
		t.Errorf("typeVersion = %s, want 1", got)
	}

	// It re-encodes as the same JSON number it arrived as, so a host that
	// round-trips a document through KilasFlow does not see it change.
	encoded, err := json.Marshal(document.Nodes[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"typeVersion":1`)) {
		t.Errorf("re-encoded node = %s, want typeVersion as the bare number 1", encoded)
	}

	if _, err := workflow.Compile(document, catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"assignments"},
		},
	}); err != nil {
		t.Fatalf("a document saved before the change must still compile: %v", err)
	}
}

// TestDocumentAcceptsAFractionalAndAYYYYMMVersion proves the persisted contract
// carries both forms the ecosystem actually uses.
func TestDocumentAcceptsAFractionalAndAYYYYMMVersion(t *testing.T) {
	const persisted = `{
        "schemaVersion": 1,
        "id": "wf_019",
        "name": "Mixed versions",
        "nodes": [
            {"id":"a","name":"A","type":"kilasflow.manual","typeVersion":4.2,"position":{"x":0,"y":0}},
            {"id":"b","name":"B","type":"kilasflow.waha","typeVersion":202502,"position":{"x":260,"y":0}}
        ],
        "connections": [],
        "settings": {}
    }`

	document, err := workflow.DecodeDocument(bytes.NewBufferString(persisted))
	if err != nil {
		t.Fatalf("DecodeDocument() error = %v", err)
	}
	for index, want := range []string{"4.2", "202502"} {
		if got := document.Nodes[index].TypeVersion.String(); got != want {
			t.Errorf("node %d typeVersion = %s, want %s", index, got, want)
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, want := range []string{`"typeVersion":4.2`, `"typeVersion":202502`} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Errorf("re-encoded document is missing %s: %s", want, encoded)
		}
	}
}

// TestCompileResolvesAVersionTheRegistryDoesNotHave is the production path: an
// imported workflow carries n8n's own typeVersion, which KilasFlow has almost
// never registered, and it must still compile against the newest shape
// available rather than being rejected.
func TestCompileResolvesAVersionTheRegistryDoesNotHave(t *testing.T) {
	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Imported versions",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			// n8n's Set is on 3.4 and KilasFlow registers only version 1.
			{
				ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.MustTypeVersion("3.4"),
				Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}},
			},
		},
		Connections: []workflow.Connection{{
			ID: "manual-set", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "set", Port: "main"},
		}},
		Settings: map[string]any{},
	}

	ir, err := workflow.Compile(document, catalogue)
	if err != nil {
		t.Fatalf("a document asking for an unregistered newer version must resolve downward: %v", err)
	}
	// The IR records the version that actually ran, not the one requested, so a
	// reader of an execution can tell which parameter shape was used.
	for _, current := range ir.Nodes {
		if current.ID != "set" {
			continue
		}
		if got := current.TypeVersion.String(); got != "1" {
			t.Errorf("compiled typeVersion = %s, want the resolved 1 rather than the requested 3.4", got)
		}
	}
}

// TestCompileStillRefusesAVersionOlderThanAnythingRegistered is the other half.
// Resolving downward must not become "resolve to anything": when every
// registered version is newer than the request, this installation cannot run
// that workflow and says so.
func TestCompileStillRefusesAVersionOlderThanAnythingRegistered(t *testing.T) {
	catalogue := node.NewRegistry()
	if err := catalogue.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "test.newonly", Version: workflow.V(5),
		DisplayName: "New only", Category: "Test", ExecutorID: "test.exec",
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Too old",
		Nodes:         []workflow.Node{{ID: "a", Name: "A", Type: "test.newonly", TypeVersion: workflow.V(2)}},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}, catalogue)

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if got, want := validationErrors.Issues[0].Code, workflow.ErrorUnknownVersion; got != want {
		t.Errorf("validation code = %q, want %q — the type exists, the version does not", got, want)
	}
	if !strings.Contains(validationErrors.Issues[0].Message, "2") {
		t.Errorf("message = %q, want it to name the requested version", validationErrors.Issues[0].Message)
	}
}

// TestCompileAcceptsSeveralTriggerRoots is the rule this ticket removes. A
// webhook for live traffic beside a schedule for a nightly catch-up is the
// standard shape, and over half the import corpus failed on it before node
// types were even considered.
func TestCompileAcceptsSeveralTriggerRoots(t *testing.T) {
	triggers := catalog{
		"kilasflow.webhook": {
			Type: "kilasflow.webhook", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.schedule": {
			Type: "kilasflow.schedule", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Two triggers",
		Nodes: []workflow.Node{
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1)},
			{ID: "cron", Name: "Schedule", Type: "kilasflow.schedule", TypeVersion: workflow.V(1)},
			{ID: "shared", Name: "Shared", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "hook-shared", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "hook", Port: "main"},
				Target: workflow.Endpoint{NodeID: "shared", Port: "main"}},
			{ID: "cron-shared", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "cron", Port: "main"},
				Target: workflow.Endpoint{NodeID: "shared", Port: "main"}},
		},
		Settings: map[string]any{},
	}

	if _, err := workflow.Compile(document, triggers); err != nil {
		t.Fatalf("a workflow with a webhook and a schedule must compile: %v", err)
	}
}

// TestCompileStillRejectsANodeReachableFromNoTrigger proves the relaxation did
// not become "anything goes": reachability is now from *any* root, not none.
func TestCompileStillRejectsANodeReachableFromNoTrigger(t *testing.T) {
	catalogue := catalog{
		"kilasflow.webhook": {
			Type: "kilasflow.webhook", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.schedule": {
			Type: "kilasflow.schedule", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Stranded node",
		Nodes: []workflow.Node{
			{ID: "hook", Name: "Webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1)},
			{ID: "cron", Name: "Schedule", Type: "kilasflow.schedule", TypeVersion: workflow.V(1)},
			{ID: "stranded", Name: "Stranded", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, catalogue)

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidTopology) {
		t.Errorf("issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidTopology)
	}
	var named bool
	for _, issue := range validationErrors.Issues {
		if issue.NodeID == "stranded" && strings.Contains(issue.Message, "disconnected") {
			named = true
		}
	}
	if !named {
		t.Errorf("issues = %#v, want the stranded node named as disconnected", validationErrors.Issues)
	}
}

// TestCompileStillRejectsAGraphWithNoTrigger keeps the other bound.
func TestCompileStillRejectsAGraphWithNoTrigger(t *testing.T) {
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "No trigger",
		Nodes: []workflow.Node{
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	var said bool
	for _, issue := range validationErrors.Issues {
		if strings.Contains(issue.Message, "at least one trigger root") {
			said = true
		}
	}
	if !said {
		t.Errorf("issues = %#v, want the missing trigger root named", validationErrors.Issues)
	}
}

// TestCompileRejectsAnOutOfRangeRetryBudget puts settings validation where the
// editor sees it.
//
// Settings are static, unlike parameters that may hold expressions, so there is
// no reason to discover a bad one part-way through a run. The cap is what stops
// a typo turning one failing node into thousands of calls against an upstream
// that is already failing.
func TestCompileRejectsAnOutOfRangeRetryBudget(t *testing.T) {
	catalogue := catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	for name, settings := range map[string]map[string]any{
		"too many attempts": {"retryOnFail": true, "maxTries": float64(9999)},
		"zero attempts":     {"retryOnFail": true, "maxTries": float64(0)},
		"non-numeric":       {"retryOnFail": true, "maxTries": "three"},
		"negative wait":     {"retryOnFail": true, "waitBetweenTries": float64(-1)},
		"absurd wait":       {"retryOnFail": true, "waitBetweenTries": float64(60 * 60 * 1000)},
		"negative timeout":  {"timeoutSeconds": float64(-5)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := workflow.Compile(workflow.Document{
				SchemaVersion: workflow.CurrentSchemaVersion,
				ID:            "wf_019",
				Name:          "Bad settings",
				Nodes: []workflow.Node{{
					ID: "manual", Name: "Manual", Type: "kilasflow.manual",
					TypeVersion: workflow.V(1), Settings: settings,
				}},
				Connections: []workflow.Connection{},
				Settings:    map[string]any{},
			}, catalogue)

			var validationErrors *workflow.ValidationErrors
			if !errors.As(err, &validationErrors) {
				t.Fatalf("Compile() error = %v, want ValidationErrors", err)
			}
			if !containsValidationCode(validationErrors.Issues, workflow.ErrorInvalidConfig) {
				t.Errorf("issues = %#v, want %q", validationErrors.Issues, workflow.ErrorInvalidConfig)
			}
		})
	}
}

// TestCompileAcceptsAValidRetryBudget keeps the bound from being so tight that
// an ordinary configuration is refused.
func TestCompileAcceptsAValidRetryBudget(t *testing.T) {
	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Sensible settings",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
			Settings: map[string]any{
				"continueOnFail": true, "retryOnFail": true,
				"maxTries": float64(3), "waitBetweenTries": float64(1000), "timeoutSeconds": float64(30),
			},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}, catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	})
	if err != nil {
		t.Fatalf("an ordinary retry configuration must compile: %v", err)
	}
}

// TestCompileAcceptsABackEdgeOntoALoopButNothingElse is the narrow widening
// this allows.
//
// n8n's Split In Batches is a cycle by construction, so refusing every cycle
// made every workflow built on it unrepresentable. What is allowed is a
// *designated* loop with a finite bound — an arbitrary back edge between two
// ordinary nodes stays rejected exactly as it was.
func TestCompileAcceptsABackEdgeOntoALoopButNothingElse(t *testing.T) {
	catalogue := catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.loop": {
			Type: "kilasflow.loop", Version: workflow.V(1), LoopEntry: true,
			Inputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{
				{Name: "done", Kind: workflow.ConnectionMain},
				{Name: "loop", Kind: workflow.ConnectionMain},
			},
		},
		"kilasflow.step": {
			Type: "kilasflow.step", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	t.Run("a back edge onto a loop is accepted", func(t *testing.T) {
		_, err := workflow.Compile(workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_019", Name: "Batched",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "loop", Name: "Loop", Type: "kilasflow.loop", TypeVersion: workflow.V(1)},
				{ID: "body", Name: "Body", Type: "kilasflow.step", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{
				{ID: "c1", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
					Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
				{ID: "c2", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "loop", Port: "loop"},
					Target: workflow.Endpoint{NodeID: "body", Port: "main"}},
				{ID: "c3", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "body", Port: "main"},
					Target: workflow.Endpoint{NodeID: "loop", Port: "main"}},
			},
			Settings: map[string]any{},
		}, catalogue)
		if err != nil {
			t.Fatalf("a loop's back edge must compile: %v", err)
		}
	})

	t.Run("a cycle between ordinary nodes is still rejected", func(t *testing.T) {
		_, err := workflow.Compile(workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_019", Name: "Plain cycle",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "a", Name: "A", Type: "kilasflow.step", TypeVersion: workflow.V(1)},
				{ID: "b", Name: "B", Type: "kilasflow.step", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{
				{ID: "c1", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
					Target: workflow.Endpoint{NodeID: "a", Port: "main"}},
				{ID: "c2", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "a", Port: "main"},
					Target: workflow.Endpoint{NodeID: "b", Port: "main"}},
				{ID: "c3", Kind: workflow.ConnectionMain,
					Source: workflow.Endpoint{NodeID: "b", Port: "main"},
					Target: workflow.Endpoint{NodeID: "a", Port: "main"}},
			},
			Settings: map[string]any{},
		}, catalogue)

		var validationErrors *workflow.ValidationErrors
		if !errors.As(err, &validationErrors) {
			t.Fatalf("Compile() error = %v, want ValidationErrors", err)
		}
		var said bool
		for _, issue := range validationErrors.Issues {
			if strings.Contains(issue.Message, "cycle") {
				said = true
			}
		}
		if !said {
			t.Errorf("issues = %#v, want the cycle named", validationErrors.Issues)
		}
	})
}

// TestConnectionKindsMatchN8NByteForByte pins the exact spelling of all
// thirteen.
//
// These strings appear verbatim in imported workflow JSON, so the casing is
// load-bearing: it is lowerCamel after the ai_ prefix, and a normalising or
// snake-casing transform anywhere in the import path silently drops every edge
// on that channel. A literal list is the point here — deriving it from the
// constants would assert nothing.
func TestConnectionKindsMatchN8NByteForByte(t *testing.T) {
	want := []string{
		"main",
		"ai_agent", "ai_chain", "ai_document", "ai_embedding",
		"ai_languageModel", "ai_memory", "ai_outputParser",
		"ai_retriever", "ai_reranker", "ai_textSplitter",
		"ai_tool", "ai_vectorStore",
	}

	got := make([]string, 0, len(workflow.ConnectionKinds()))
	for _, kind := range workflow.ConnectionKinds() {
		got = append(got, string(kind))
	}
	if len(got) != len(want) {
		t.Fatalf("ConnectionKinds() has %d values, want n8n's %d: %v", len(got), len(want), got)
	}
	for index, expected := range want {
		if got[index] != expected {
			t.Errorf("kind %d = %q, want %q", index, got[index], expected)
		}
		if !workflow.KnownConnectionKind(workflow.ConnectionKind(expected)) {
			t.Errorf("%q is not accepted by KnownConnectionKind", expected)
		}
	}

	// A snake-cased or lower-cased spelling must be refused, because accepting
	// one would let a normalising transform pass unnoticed.
	for _, wrong := range []string{"ai_language_model", "ai_languagemodel", "AI_LanguageModel", "ai_vectorstore"} {
		if workflow.KnownConnectionKind(workflow.ConnectionKind(wrong)) {
			t.Errorf("%q was accepted; the casing is load-bearing", wrong)
		}
	}
}

// TestCompileEnforcesPortCardinality is what makes the AI Agent's slots real.
// Before, the compiler would accept three language models on one agent.
func TestCompileEnforcesPortCardinality(t *testing.T) {
	catalogue := catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"test.model": {
			Type: "test.model", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "model", Kind: workflow.ConnectionLanguageModel}},
		},
		"test.agent": {
			Type: "test.agent", Version: workflow.V(1),
			Inputs: []workflow.Port{
				{Name: "main", Kind: workflow.ConnectionMain},
				{Name: "model", DisplayName: "Chat Model", Kind: workflow.ConnectionLanguageModel,
					Required: true, MaxConnections: 1},
			},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	build := func(models int) workflow.Document {
		document := workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_019", Name: "Agent",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "agent", Name: "Agent", Type: "test.agent", TypeVersion: workflow.V(1)},
			},
			Connections: []workflow.Connection{{
				ID: "c0", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "agent", Port: "main"},
			}},
			Settings: map[string]any{},
		}
		for index := range models {
			id := fmt.Sprintf("model%d", index)
			document.Nodes = append(document.Nodes, workflow.Node{
				ID: id, Name: id, Type: "test.model", TypeVersion: workflow.V(1),
			})
			document.Connections = append(document.Connections, workflow.Connection{
				ID: "cm" + id, Kind: workflow.ConnectionLanguageModel,
				Source: workflow.Endpoint{NodeID: id, Port: "model"},
				Target: workflow.Endpoint{NodeID: "agent", Port: "model"},
			})
		}
		return document
	}

	t.Run("one model compiles", func(t *testing.T) {
		if _, err := workflow.Compile(build(1), catalogue); err != nil {
			t.Fatalf("an agent with exactly one model must compile: %v", err)
		}
	})

	t.Run("three models are refused", func(t *testing.T) {
		_, err := workflow.Compile(build(3), catalogue)
		var validationErrors *workflow.ValidationErrors
		if !errors.As(err, &validationErrors) {
			t.Fatalf("Compile() error = %v, want ValidationErrors", err)
		}
		if !containsValidationCode(validationErrors.Issues, workflow.ErrorPortFull) {
			t.Errorf("issues = %#v, want %q", validationErrors.Issues, workflow.ErrorPortFull)
		}
		// The message names the port by its display name, which is what the
		// user sees in the editor.
		var named bool
		for _, issue := range validationErrors.Issues {
			if strings.Contains(issue.Message, "Chat Model") {
				named = true
			}
		}
		if !named {
			t.Errorf("issues = %#v, want the port named by its display name", validationErrors.Issues)
		}
	})

	t.Run("no model is refused", func(t *testing.T) {
		_, err := workflow.Compile(build(0), catalogue)
		var validationErrors *workflow.ValidationErrors
		if !errors.As(err, &validationErrors) {
			t.Fatalf("Compile() error = %v, want ValidationErrors", err)
		}
		if !containsValidationCode(validationErrors.Issues, workflow.ErrorPortRequired) {
			t.Errorf("issues = %#v, want %q — an agent with no model cannot do anything",
				validationErrors.Issues, workflow.ErrorPortRequired)
		}
	})
}

// TestCompileEnforcesAPortsNodeTypeFilter proves the filter is a compile-time
// rule, so an imported document cannot bypass what the editor would refuse.
func TestCompileEnforcesAPortsNodeTypeFilter(t *testing.T) {
	catalogue := catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"test.wrongTool": {
			Type: "test.wrongTool", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "tool", Kind: workflow.ConnectionTool}},
		},
		"test.picky": {
			Type: "test.picky", Version: workflow.V(1),
			Inputs: []workflow.Port{
				{Name: "main", Kind: workflow.ConnectionMain},
				{Name: "tools", Kind: workflow.ConnectionTool, AllowedNodeTypes: []string{"test.rightTool"}},
			},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}

	_, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019", Name: "Filtered",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "picky", Name: "Picky", Type: "test.picky", TypeVersion: workflow.V(1)},
			{ID: "tool", Name: "Tool", Type: "test.wrongTool", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "picky", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionTool,
				Source: workflow.Endpoint{NodeID: "tool", Port: "tool"},
				Target: workflow.Endpoint{NodeID: "picky", Port: "tools"}},
		},
		Settings: map[string]any{},
	}, catalogue)

	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() error = %v, want ValidationErrors", err)
	}
	if !containsValidationCode(validationErrors.Issues, workflow.ErrorPortNotAllowed) {
		t.Errorf("issues = %#v, want %q", validationErrors.Issues, workflow.ErrorPortNotAllowed)
	}
}

// TestCompileDoesNotRequireAHiddenParameter is the defect that blocks every
// declarative node pack.
//
// Visibility used to exist only on the client, so the compiler demanded every
// required parameter regardless of whether the node's configuration showed it.
// A node shaped like a real n8n node — where chatId is required only when
// resource is message — was therefore unactivatable in every other
// configuration.
func TestCompileDoesNotRequireAHiddenParameter(t *testing.T) {
	catalogue := node.NewRegistry()
	if err := catalogue.Register(node.Definition{
		Type: "test.telegram", Version: workflow.V(1),
		DisplayName: "Telegram", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTrigger},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{
			{Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Required: true},
			{
				Key: "chatId", Label: "Chat ID", Kind: node.PropertyString, Required: true,
				DisplayOptions: property.Visibility{
					Show: []property.Condition{{Key: "resource", Values: []any{"message"}}},
				},
			},
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	build := func(parameters map[string]any) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_019", Name: "Telegram",
			Nodes: []workflow.Node{{
				ID: "tg", Name: "Telegram", Type: "test.telegram",
				TypeVersion: workflow.V(1), Parameters: parameters,
			}},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		}
	}

	t.Run("a configuration that hides the field activates", func(t *testing.T) {
		if _, err := workflow.Compile(build(map[string]any{"resource": "chat"}), catalogue); err != nil {
			t.Fatalf("a node whose configuration hides chatId must compile: %v", err)
		}
	})

	t.Run("a configuration that shows it still demands it", func(t *testing.T) {
		_, err := workflow.Compile(build(map[string]any{"resource": "message"}), catalogue)
		var validationErrors *workflow.ValidationErrors
		if !errors.As(err, &validationErrors) {
			t.Fatalf("Compile() error = %v, want ValidationErrors", err)
		}
		var named bool
		for _, issue := range validationErrors.Issues {
			if strings.Contains(issue.Message, "chatId") {
				named = true
			}
		}
		if !named {
			t.Errorf("issues = %#v, want chatId demanded when it is shown", validationErrors.Issues)
		}
	})

	t.Run("an expression in the controlling value demands it", func(t *testing.T) {
		// Nothing can know at compile time what the expression resolves to, and
		// the show rule shows the field — so the field is required.
		_, err := workflow.Compile(build(map[string]any{
			"resource": map[string]any{"mode": "expression", "value": "{{ $json.kind }}"},
		}), catalogue)
		if err == nil {
			t.Error("an expression-controlled field was treated as hidden")
		}
	})
}
