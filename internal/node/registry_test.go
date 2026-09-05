package node_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func TestRegistryListsBuiltinsInStableOrder(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	definitions := registry.List()
	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.Type)
	}
	want := []string{
		"kilasflow.httpRequest",
		"kilasflow.if",
		"kilasflow.manual",
		"kilasflow.merge",
		"kilasflow.set",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() types = %#v, want %#v", got, want)
	}

	first, err := json.Marshal(definitions)
	if err != nil {
		t.Fatalf("marshal first catalogue = %v", err)
	}
	second, err := json.Marshal(registry.List())
	if err != nil {
		t.Fatalf("marshal second catalogue = %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("catalogue serialization is not stable:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestRegistryExposesCorePortAndPropertyMetadata(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	manual, found := registry.Get("kilasflow.manual", 1)
	if !found {
		t.Fatal("manual trigger was not registered")
	}
	if len(manual.Inputs) != 0 || !hasPort(manual.Outputs, "main", workflow.ConnectionMain) {
		t.Errorf("manual ports = %#v -> %#v, want no input and main output", manual.Inputs, manual.Outputs)
	}
	if manual.ExecutorID == "" {
		t.Error("manual trigger executor binding is empty")
	}

	set, found := registry.Get("kilasflow.set", 1)
	if !found {
		t.Fatal("set node was not registered")
	}
	if !hasPort(set.Inputs, "main", workflow.ConnectionMain) || !hasPort(set.Outputs, "main", workflow.ConnectionMain) {
		t.Errorf("set ports = %#v -> %#v, want main input/output", set.Inputs, set.Outputs)
	}
	if !hasRequiredProperty(set.Parameters, "assignments", node.PropertyKeyValue) {
		t.Errorf("set parameters = %#v, want required assignments key-value control", set.Parameters)
	}
	if len(set.SharedSettings) == 0 {
		t.Error("set shared settings are empty")
	}

	ifNode, found := registry.Get("kilasflow.if", 1)
	if !found {
		t.Fatal("if node was not registered")
	}
	if !hasPort(ifNode.Outputs, "true", workflow.ConnectionMain) || !hasPort(ifNode.Outputs, "false", workflow.ConnectionMain) {
		t.Errorf("if outputs = %#v, want labelled true/false outputs", ifNode.Outputs)
	}
	if !hasRequiredProperty(ifNode.Parameters, "conditions", node.PropertyConditions) {
		t.Errorf("if parameters = %#v, want required conditions control", ifNode.Parameters)
	}

	merge, found := registry.Get("kilasflow.merge", 1)
	if !found {
		t.Fatal("merge node was not registered")
	}
	if !hasPort(merge.Inputs, "input1", workflow.ConnectionMain) || !hasPort(merge.Inputs, "input2", workflow.ConnectionMain) || !hasPort(merge.Outputs, "main", workflow.ConnectionMain) {
		t.Errorf("merge ports = %#v -> %#v, want input1/input2 and main", merge.Inputs, merge.Outputs)
	}
}

func TestRegistryRejectsDuplicateDefinitionsAndDefendsCopies(t *testing.T) {
	registry := node.NewRegistry()
	definition := node.Definition{
		Type:        "kilasflow.test",
		Version:     1,
		DisplayName: "Test",
		Category:    "Core",
		Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		ExecutorID:  "test",
	}
	if err := registry.Register(definition); err != nil {
		t.Fatalf("Register() first error = %v", err)
	}
	if err := registry.Register(definition); err == nil {
		t.Fatal("Register() duplicate error = nil, want an error")
	}

	listed := registry.List()
	listed[0].Outputs[0].Name = "changed"
	loaded, found := registry.Get("kilasflow.test", 1)
	if !found {
		t.Fatal("registered definition disappeared")
	}
	if got, want := loaded.Outputs[0].Name, "main"; got != want {
		t.Errorf("registry leaked mutable output slice = %q, want %q", got, want)
	}
}

func TestRegistryValidatesEverySupportedConnectionKind(t *testing.T) {
	registry := node.NewRegistry()
	kinds := []workflow.ConnectionKind{
		workflow.ConnectionMain,
		workflow.ConnectionLanguageModel,
		workflow.ConnectionMemory,
		workflow.ConnectionTool,
	}
	outputs := make([]workflow.Port, 0, len(kinds))
	for _, kind := range kinds {
		outputs = append(outputs, workflow.Port{Name: "out-" + string(kind), Kind: kind})
	}
	if err := registry.Register(node.Definition{
		Type: "kilasflow.source", Version: 1, DisplayName: "Source", Category: "Test", ExecutorID: "source",
		Outputs: outputs,
	}); err != nil {
		t.Fatalf("register source = %v", err)
	}
	nodes := []workflow.Node{{ID: "source", Name: "source", Type: "kilasflow.source", TypeVersion: 1}}
	connections := make([]workflow.Connection, 0, len(kinds))
	for _, kind := range kinds {
		targetType := "kilasflow.target." + string(kind)
		if err := registry.Register(node.Definition{
			Type: targetType, Version: 1, DisplayName: targetType, Category: "Test", ExecutorID: targetType,
			Inputs: []workflow.Port{{Name: "in", Kind: kind}},
		}); err != nil {
			t.Fatalf("register target %q = %v", kind, err)
		}
		targetID := "target-" + string(kind)
		nodes = append(nodes,
			workflow.Node{ID: targetID, Name: targetID, Type: targetType, TypeVersion: 1},
		)
		connections = append(connections, workflow.Connection{
			ID: "edge-" + string(kind), Kind: kind,
			Source: workflow.Endpoint{NodeID: "source", Port: "out-" + string(kind)}, Target: workflow.Endpoint{NodeID: targetID, Port: "in"},
		})
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_019", Name: "All connection kinds",
		Nodes: nodes, Connections: connections, Settings: map[string]any{},
	}
	if _, err := workflow.Compile(document, registry); err != nil {
		t.Fatalf("Compile() matching connection kinds error = %v", err)
	}

	document.Connections[0].Kind = workflow.ConnectionTool
	_, err := workflow.Compile(document, registry)
	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("Compile() incompatible connection error = %v, want ValidationErrors", err)
	}
	if !containsCode(validationErrors.Issues, workflow.ErrorIncompatiblePort) {
		t.Errorf("validation issues = %#v, want %q", validationErrors.Issues, workflow.ErrorIncompatiblePort)
	}
}

func hasPort(ports []workflow.Port, name string, kind workflow.ConnectionKind) bool {
	for _, port := range ports {
		if port.Name == name && port.Kind == kind {
			return true
		}
	}
	return false
}

func hasRequiredProperty(properties []node.PropertyDefinition, key string, kind node.PropertyKind) bool {
	for _, property := range properties {
		if property.Key == key && property.Kind == kind && property.Required {
			return true
		}
	}
	return false
}

func containsCode(issues []workflow.ValidationError, want workflow.ErrorCode) bool {
	for _, issue := range issues {
		if issue.Code == want {
			return true
		}
	}
	return false
}
