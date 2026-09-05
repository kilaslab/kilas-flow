package node_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
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
	if len(definitions) == 0 {
		t.Fatal("List() returned nothing after registering the built-ins")
	}
	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.Type)
	}

	// The property that matters is the ordering contract, not the current
	// membership: asserting a literal list would force an unrelated edit into
	// every commit that adds a node.
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("List() types = %#v, want them sorted", got)
	}
	// The registry is keyed by {type, version} and List sorts by both, so one
	// type may legitimately appear at several versions — the import
	// placeholder is registered once per port arity. What must never repeat is
	// a type at the same version.
	type key struct {
		nodeType string
		version  workflow.TypeVersion
	}
	seenKey := make(map[key]bool, len(definitions))
	seen := make(map[string]bool, len(got))
	for _, definition := range definitions {
		current := key{definition.Type, definition.Version}
		if seenKey[current] {
			t.Errorf("List() returned %q version %s twice", definition.Type, definition.Version)
		}
		seenKey[current] = true
		seen[definition.Type] = true
	}
	for _, expected := range []string{"kilasflow.manual", "kilasflow.set"} {
		if !seen[expected] {
			t.Errorf("List() is missing the core node %q", expected)
		}
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

	manual, found := registry.Get("kilasflow.manual", workflow.V(1))
	if !found {
		t.Fatal("manual trigger was not registered")
	}
	if len(manual.Inputs) != 0 || !hasPort(manual.Outputs, "main", workflow.ConnectionMain) {
		t.Errorf("manual ports = %#v -> %#v, want no input and main output", manual.Inputs, manual.Outputs)
	}
	if manual.ExecutorID == "" {
		t.Error("manual trigger executor binding is empty")
	}

	set, found := registry.Get("kilasflow.set", workflow.V(1))
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

	ifNode, found := registry.Get("kilasflow.if", workflow.V(1))
	if !found {
		t.Fatal("if node was not registered")
	}
	if !hasPort(ifNode.Outputs, "true", workflow.ConnectionMain) || !hasPort(ifNode.Outputs, "false", workflow.ConnectionMain) {
		t.Errorf("if outputs = %#v, want labelled true/false outputs", ifNode.Outputs)
	}
	if !hasRequiredProperty(ifNode.Parameters, "conditions", node.PropertyConditions) {
		t.Errorf("if parameters = %#v, want required conditions control", ifNode.Parameters)
	}

	merge, found := registry.Get("kilasflow.merge", workflow.V(1))
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
		Version:     workflow.V(1),
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
	loaded, found := registry.Get("kilasflow.test", workflow.V(1))
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
		Type: "kilasflow.source", Version: workflow.V(1), DisplayName: "Source", Category: "Test", ExecutorID: "source",
		Outputs: outputs,
	}); err != nil {
		t.Fatalf("register source = %v", err)
	}
	nodes := []workflow.Node{{ID: "source", Name: "source", Type: "kilasflow.source", TypeVersion: workflow.V(1)}}
	connections := make([]workflow.Connection, 0, len(kinds))
	for _, kind := range kinds {
		targetType := "kilasflow.target." + string(kind)
		if err := registry.Register(node.Definition{
			Type: targetType, Version: workflow.V(1), DisplayName: targetType, Category: "Test", ExecutorID: targetType,
			Inputs: []workflow.Port{{Name: "in", Kind: kind}},
		}); err != nil {
			t.Fatalf("register target %q = %v", kind, err)
		}
		targetID := "target-" + string(kind)
		nodes = append(nodes,
			workflow.Node{ID: targetID, Name: targetID, Type: targetType, TypeVersion: workflow.V(1)},
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

// TestRegistryResolvesDownwardNeverUpward pins the version dispatch rule.
//
// The direction is the whole point. Resolving upward would silently run a
// workflow written for version 2 against version 3's parameter shape, which is
// a behaviour change disguised as a lookup. Resolving downward can only give a
// workflow the shape it was written for or an older one.
func TestRegistryResolvesDownwardNeverUpward(t *testing.T) {
	registry := node.NewRegistry()
	for _, version := range []string{"1", "2", "3.4"} {
		if err := registry.Register(node.Definition{
			Type: "test.versioned", Version: workflow.MustTypeVersion(version),
			DisplayName: "Versioned", Category: "Test", ExecutorID: "test.exec",
		}); err != nil {
			t.Fatalf("Register(%s) error = %v", version, err)
		}
	}

	for _, testCase := range []struct {
		requested string
		want      string
		found     bool
		why       string
	}{
		{"3.4", "3.4", true, "an exact match resolves to itself"},
		{"2", "2", true, "an exact match resolves to itself"},
		{"3", "2", true, "3 is not registered, so the highest below it wins"},
		{"4.2", "3.4", true, "a newer workflow runs against the newest shape available"},
		{"202502", "3.4", true, "a YYYYMM request still resolves downward"},
		{"1", "1", true, "the oldest registered version is reachable"},
	} {
		got, found := registry.Resolve("test.versioned", workflow.MustTypeVersion(testCase.requested))
		if found != testCase.found {
			t.Errorf("Resolve(%s) found = %t, want %t", testCase.requested, found, testCase.found)
			continue
		}
		if got.Version.String() != testCase.want {
			t.Errorf("Resolve(%s) = %s, want %s (%s)", testCase.requested, got.Version, testCase.want, testCase.why)
		}
	}

	// Nothing is asked for: the newest registered version is current.
	current, found := registry.Resolve("test.versioned", workflow.TypeVersion{})
	if !found || current.Version.String() != "3.4" {
		t.Errorf("Resolve(unset) = %s (found %t), want the newest registered version", current.Version, found)
	}

	// Every registered version is newer than the request: say so rather than
	// guess. This installation cannot run this workflow.
	if _, found := registry.Resolve("test.versioned", workflow.MustTypeVersion("0.5")); found {
		t.Error("Resolve(0.5) found a definition; every registered version is newer, so it must fail")
	}
}

// TestRegistryHoldsTwoVersionsOfOneTypeAtOnce is the premise of the node-pack
// work: Set v2 and Set v3.4 take different parameters, and a document must be
// able to select between them.
func TestRegistryHoldsTwoVersionsOfOneTypeAtOnce(t *testing.T) {
	registry := node.NewRegistry()
	for _, version := range []string{"202409", "202502"} {
		if err := registry.Register(node.Definition{
			Type: "waha.action", Version: workflow.MustTypeVersion(version),
			DisplayName: "WAHA", Category: "Test", ExecutorID: "waha.exec",
			Parameters: []node.PropertyDefinition{
				{Key: version, Label: "Shape " + version, Kind: node.PropertyString},
			},
		}); err != nil {
			t.Fatalf("Register(%s) error = %v", version, err)
		}
	}

	for _, version := range []string{"202409", "202502"} {
		definition, found := registry.Get("waha.action", workflow.MustTypeVersion(version))
		if !found {
			t.Fatalf("Get(waha.action, %s) not found", version)
		}
		if definition.Version.String() != version {
			t.Errorf("Get(%s) = %s", version, definition.Version)
		}
		// A YYYYMM version must select its own parameter shape, not the other's.
		if len(definition.Parameters) != 1 || definition.Parameters[0].Key != version {
			t.Errorf("version %s resolved to the wrong parameter shape: %#v", version, definition.Parameters)
		}
	}
}
