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

func (c catalog) Lookup(nodeType string, version int) (workflow.NodeDefinition, bool) {
	definition, ok := c[nodeType]
	return definition, ok && definition.Version == version
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
			{ID: "first", Name: "First", Type: "kilasflow.set", TypeVersion: 1},
			{ID: "second", Name: "Second", Type: "kilasflow.set", TypeVersion: 1},
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
			Type: "kilasflow.set", Version: 1,
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

func TestCompileDistinguishesUnknownNodeVersion(t *testing.T) {
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Older node version",
		Nodes: []workflow.Node{{
			ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: 2,
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {Type: "kilasflow.set", Version: 1},
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
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"status": "ready"}}},
			{ID: "orphan", Name: "Orphan", Type: "kilasflow.set", TypeVersion: 1, Parameters: map[string]any{"assignments": map[string]any{"status": "orphan"}}},
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
			Type: "kilasflow.manual", Version: 1,
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: 1,
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
			ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: 1,
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: 1,
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
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: 1},
			{ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: 1, Parameters: map[string]any{"conditions": []any{"not-a-condition"}}},
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
			{ID: "source", Name: "Source", Type: "kilasflow.set", TypeVersion: 1},
			{ID: "target", Name: "Target", Type: "kilasflow.set", TypeVersion: 1},
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
			Type: "kilasflow.set", Version: 1,
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
			{ID: "source", Name: "Source", Type: "kilasflow.set", TypeVersion: 1},
			{ID: "target", Name: "Target", Type: "kilasflow.set", TypeVersion: 1},
		},
		Connections: []workflow.Connection{
			{ID: "first", Kind: connection.Kind, Source: connection.Source, Target: connection.Target},
			{ID: "second", Kind: connection.Kind, Source: connection.Source, Target: connection.Target},
		},
		Settings: map[string]any{},
	}

	_, err := workflow.Compile(document, catalog{
		"kilasflow.set": {
			Type: "kilasflow.set", Version: 1,
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
			ID: "unknown", Name: "Unknown", Type: "kilasflow.unknown", TypeVersion: 1,
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
				TypeVersion: 1,
				Position:    workflow.Position{X: 0, Y: 0},
			},
			{
				ID:          "if",
				Name:        "IF",
				Type:        "kilasflow.if",
				TypeVersion: 1,
				Position:    workflow.Position{X: 240, Y: 0},
				Parameters:  map[string]any{"conditions": []any{"customer"}},
			},
			{
				ID:          "false-set",
				Name:        "False branch",
				Type:        "kilasflow.set",
				TypeVersion: 1,
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
			Type: "kilasflow.manualTrigger", Version: 1,
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.if": {
			Type: "kilasflow.if", Version: 1,
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "true", Kind: workflow.ConnectionMain}, {Name: "false", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"conditions"},
		},
		"kilasflow.set": {
			Type: "kilasflow.set", Version: 1,
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
			TypeVersion: 1,
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
		"kilasflow.set": {Type: "kilasflow.set", Version: 1},
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
