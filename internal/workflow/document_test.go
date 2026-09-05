package workflow_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
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
		Type: "test.newonly", Version: workflow.V(5),
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
