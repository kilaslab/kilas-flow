package node_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
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
		Group:       []node.NodeGroup{node.GroupTransform},
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
		Group: []node.NodeGroup{node.GroupTransform},
		Type:  "kilasflow.source", Version: workflow.V(1), DisplayName: "Source", Category: "Test", ExecutorID: "source",
		Outputs: outputs,
	}); err != nil {
		t.Fatalf("register source = %v", err)
	}
	nodes := []workflow.Node{{ID: "source", Name: "source", Type: "kilasflow.source", TypeVersion: workflow.V(1)}}
	connections := make([]workflow.Connection, 0, len(kinds))
	for _, kind := range kinds {
		targetType := "kilasflow.target." + string(kind)
		if err := registry.Register(node.Definition{
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  targetType, Version: workflow.V(1), DisplayName: targetType, Category: "Test", ExecutorID: targetType,
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
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "test.versioned", Version: workflow.MustTypeVersion(version),
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
			Group: []node.NodeGroup{node.GroupTransform},
			Type:  "waha.action", Version: workflow.MustTypeVersion(version),
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

// TestRegistryDeepCopiesPresentationFields is why cloneDefinition exists.
//
// The registry hands out copies, so a new slice or map that skips the clone
// silently aliases the registry's own storage — visible only once a caller
// mutates what it was given, which is exactly the sort of bug that surfaces in
// production and not in a test.
func TestRegistryDeepCopiesPresentationFields(t *testing.T) {
	registry := node.NewRegistry()
	definition := node.Definition{
		Type: "test.presented", Version: workflow.V(1),
		DisplayName: "Presented", Category: "Test", ExecutorID: "test.exec",
		Group:     []node.NodeGroup{node.GroupTransform},
		Icon:      &node.NodeIcon{Light: "builtin:box", Dark: "builtin:box-dark"},
		IconColor: "#000000",
		Codex: &node.NodeCodex{
			Categories:    []string{"Test"},
			Subcategories: map[string][]string{"Test": {"Sub"}},
			Aliases:       []string{"presented"},
		},
	}
	if err := registry.Register(definition); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	first, found := registry.Get("test.presented", workflow.V(1))
	if !found {
		t.Fatal("the definition was not registered")
	}
	// Mutate every reference type the caller was handed.
	first.Group[0] = node.GroupTrigger
	first.Icon.Light = "tampered"
	first.Codex.Categories[0] = "tampered"
	first.Codex.Aliases[0] = "tampered"
	first.Codex.Subcategories["Test"][0] = "tampered"

	second, _ := registry.Get("test.presented", workflow.V(1))
	if second.Group[0] != node.GroupTransform {
		t.Error("group was aliased with the registry's storage")
	}
	if second.Icon.Light != "builtin:box" {
		t.Error("icon was aliased with the registry's storage")
	}
	if second.Codex.Categories[0] != "Test" {
		t.Error("codex categories were aliased with the registry's storage")
	}
	if second.Codex.Aliases[0] != "presented" {
		t.Error("codex aliases were aliased with the registry's storage")
	}
	if second.Codex.Subcategories["Test"][0] != "Sub" {
		t.Error("codex subcategories were aliased with the registry's storage")
	}
}

// TestRegistryRefusesAnUnknownGroupOrABadSubtitle keeps both closed sets shut.
func TestRegistryRefusesAnUnknownGroupOrABadSubtitle(t *testing.T) {
	base := func() node.Definition {
		return node.Definition{
			Type: "test.bad", Version: workflow.V(1),
			DisplayName: "Bad", Category: "Test", ExecutorID: "test.exec",
			Group: []node.NodeGroup{node.GroupTransform},
		}
	}

	for name, mutate := range map[string]func(*node.Definition){
		"no group":          func(d *node.Definition) { d.Group = nil },
		"unknown group":     func(d *node.Definition) { d.Group = []node.NodeGroup{"database"} },
		"foreign root":      func(d *node.Definition) { d.Subtitle = "{{ $json.name }}" },
		"unclosed template": func(d *node.Definition) { d.Subtitle = "{{ $parameter.name" },
		"empty parameter":   func(d *node.Definition) { d.Subtitle = "{{ $parameter. }}" },
	} {
		definition := base()
		mutate(&definition)
		if err := node.NewRegistry().Register(definition); err == nil {
			t.Errorf("%s: Register() accepted an invalid definition", name)
		}
	}

	// And the valid subtitle form registers.
	definition := base()
	definition.Subtitle = "{{ $parameter.method }} {{ $parameter.path }}"
	if err := node.NewRegistry().Register(definition); err != nil {
		t.Errorf("a valid subtitle was refused: %v", err)
	}
}

// TestRequiredParametersUnderstandsAPerElementDefault is the semantic that
// silently corrupts an imported node when it is wrong.
//
// Under multipleValues, n8n's default describes **one element**, not the
// collection. A default normally satisfies a requirement — the node has a
// usable value without the user typing one — but an element default says
// nothing about whether the list has any elements, so a required list is still
// unsatisfied until one is added.
func TestRequiredParametersUnderstandsAPerElementDefault(t *testing.T) {
	registry := node.NewRegistry()
	if err := registry.Register(node.Definition{
		Type: "test.repeated", Version: workflow.V(1),
		DisplayName: "Repeated", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{
			// A plain default satisfies a requirement.
			{Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: "append"},
			// A per-element default does not.
			{
				Key: "headers", Label: "Headers", Kind: node.PropertyCollection, Required: true,
				Default:     map[string]any{},
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add header"},
				Fields: []node.PropertyDefinition{
					{Key: "name", Label: "Name", Kind: node.PropertyString},
					{Key: "value", Label: "Value", Kind: node.PropertyString},
				},
			},
			// A notice holds no value and can never be required.
			{Key: "hint", Label: "Hint", Kind: node.PropertyNotice, Required: true},
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	definition, found := registry.Lookup("test.repeated", workflow.V(1))
	if !found {
		t.Fatal("the definition was not registered")
	}
	required := map[string]bool{}
	for _, key := range definition.RequiredParameters {
		required[key] = true
	}

	if required["mode"] {
		t.Error("a property with a plain default was reported as unsatisfied")
	}
	if !required["headers"] {
		t.Error("a required list with a per-element default was treated as satisfied; the element default says nothing about whether the list has elements")
	}
	if required["hint"] {
		t.Error("a notice was reported as required; it holds no value and must never be stored")
	}
}

// TestValidatePropertiesRecursesAndScopesKeysPerLevel covers the nesting rule
// the flat validator could not express.
func TestValidatePropertiesRecursesAndScopesKeysPerLevel(t *testing.T) {
	base := func(parameters []node.PropertyDefinition) node.Definition {
		return node.Definition{
			Type: "test.nested", Version: workflow.V(1),
			DisplayName: "Nested", Category: "Test", ExecutorID: "test.exec",
			Group:      []node.NodeGroup{node.GroupTransform},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Parameters: parameters,
		}
	}

	t.Run("an inner key may repeat an outer one", func(t *testing.T) {
		// They live in different objects and never meet, so a shared seen map
		// across the recursion would refuse a perfectly ordinary shape.
		err := node.NewRegistry().Register(base([]node.PropertyDefinition{
			{Key: "value", Label: "Value", Kind: node.PropertyString},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "value", Label: "Value", Kind: node.PropertyString},
				},
			},
		}))
		if err != nil {
			t.Errorf("an inner key repeating an outer one was refused: %v", err)
		}
	})

	t.Run("a duplicate within one level is refused", func(t *testing.T) {
		err := node.NewRegistry().Register(base([]node.PropertyDefinition{
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "value", Label: "Value", Kind: node.PropertyString},
					{Key: "value", Label: "Value again", Kind: node.PropertyString},
				},
			},
		}))
		if err == nil {
			t.Error("a duplicate key inside a collection was accepted")
		}
	})

	t.Run("an invalid kind at depth is refused", func(t *testing.T) {
		err := node.NewRegistry().Register(base([]node.PropertyDefinition{
			{
				Key: "groups", Label: "Groups", Kind: node.PropertyFixedCollection,
				Groups: []node.PropertyGroup{{
					Key: "header", Label: "Header",
					Fields: []node.PropertyDefinition{
						{Key: "name", Label: "Name", Kind: "resourceLocator"},
					},
				}},
			},
		}))
		if err == nil {
			t.Error("an unknown kind nested two levels down was accepted")
		}
	})

	t.Run("an unnamed group is refused", func(t *testing.T) {
		err := node.NewRegistry().Register(base([]node.PropertyDefinition{
			{
				Key: "groups", Label: "Groups", Kind: node.PropertyFixedCollection,
				Groups: []node.PropertyGroup{{Key: "", Label: ""}},
			},
		}))
		if err == nil {
			t.Error("a fixedCollection group with no key was accepted")
		}
	})
}

// TestRegistryDeepCopiesNestedProperties is the aliasing bug one level deeper
// than the last one: a caller mutating an inner collection field would change
// what every other caller reads.
func TestRegistryDeepCopiesNestedProperties(t *testing.T) {
	registry := node.NewRegistry()
	precision := 2
	if err := registry.Register(node.Definition{
		Type: "test.deep", Version: workflow.V(1),
		DisplayName: "Deep", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{{
			Key: "outer", Label: "Outer", Kind: node.PropertyFixedCollection,
			TypeOptions: &node.TypeOptions{NumberPrecision: &precision, MultipleValues: true},
			Fields: []node.PropertyDefinition{
				{Key: "inner", Label: "Inner", Kind: node.PropertyString},
			},
			Groups: []node.PropertyGroup{{
				Key: "group", Label: "Group",
				Fields: []node.PropertyDefinition{{Key: "deep", Label: "Deep", Kind: node.PropertyString}},
			}},
		}},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	first, _ := registry.Get("test.deep", workflow.V(1))
	first.Parameters[0].Fields[0].Label = "tampered"
	first.Parameters[0].Groups[0].Fields[0].Label = "tampered"
	*first.Parameters[0].TypeOptions.NumberPrecision = 9

	second, _ := registry.Get("test.deep", workflow.V(1))
	if second.Parameters[0].Fields[0].Label != "Inner" {
		t.Error("a nested collection field was aliased with the registry's storage")
	}
	if second.Parameters[0].Groups[0].Fields[0].Label != "Deep" {
		t.Error("a nested group field was aliased with the registry's storage")
	}
	if *second.Parameters[0].TypeOptions.NumberPrecision != 2 {
		t.Error("a type-options pointer was aliased with the registry's storage")
	}
}

// TestKnownPropertyKindsIsExactlyTheDocumentedSet keeps the allowlist closed.
func TestKnownPropertyKindsIsExactlyTheDocumentedSet(t *testing.T) {
	want := []string{
		"string", "number", "boolean",
		"options", "multiOptions",
		"collection", "fixedCollection",
		"notice", "json", "dateTime",
		"keyValue", "conditions",
	}
	got := make([]string, 0, len(node.KnownPropertyKinds()))
	for _, kind := range node.KnownPropertyKinds() {
		got = append(got, string(kind))
	}
	if len(got) != len(want) {
		t.Fatalf("KnownPropertyKinds() = %v, want %v", got, want)
	}
	for index, expected := range want {
		if got[index] != expected {
			t.Errorf("kind %d = %q, want %q", index, got[index], expected)
		}
	}
	// `select` is gone rather than kept as a synonym: two names for one control
	// would mean every generated pack has to remember which this server speaks.
	for _, gone := range []string{"select", "resourceLocator", "filter"} {
		for _, kind := range got {
			if kind == gone {
				t.Errorf("%q is still an accepted kind", gone)
			}
		}
	}
}

// TestSourceIsSetByTheRegistrationPathNotTheDefinition is what makes the tag
// trustworthy. A pack able to declare itself built-in would claim the built-in
// namespace and win every precedence contest.
func TestSourceIsSetByTheRegistrationPathNotTheDefinition(t *testing.T) {
	registry := node.NewRegistry()

	// A definition that lies about where it came from.
	if err := registry.RegisterFrom(node.SourcePack, node.Definition{
		Type: "pack.honest", Version: workflow.V(1),
		DisplayName: "Honest", Category: "Test", ExecutorID: "pack.exec",
		Group:  []node.NodeGroup{node.GroupTransform},
		Source: node.SourceBuiltin,
	}); err != nil {
		t.Fatalf("RegisterFrom() error = %v", err)
	}
	stored, found := registry.Get("pack.honest", workflow.V(1))
	if !found {
		t.Fatal("the definition was not registered")
	}
	if stored.Source != node.SourcePack {
		t.Errorf("source = %q, want %q — the claim in the definition must be ignored", stored.Source, node.SourcePack)
	}

	// And a pack cannot register *as* a built-in through the pack entry point.
	if err := registry.RegisterFrom(node.SourceBuiltin, node.Definition{
		Type: "pack.other", Version: workflow.V(1),
		DisplayName: "Other", Category: "Test", ExecutorID: "pack.exec",
		Group: []node.NodeGroup{node.GroupTransform},
	}); err == nil {
		t.Error("a pack registered itself as built-in")
	}
}

// TestThePackNamespaceIsEnforced keeps a pack from shadowing — or being
// mistaken for — a node this project ships, which is a supply-chain problem
// rather than a naming one.
func TestThePackNamespaceIsEnforced(t *testing.T) {
	registry := node.NewRegistry()
	err := registry.RegisterFrom(node.SourcePack, node.Definition{
		Type: "kilasflow.set", Version: workflow.V(2),
		DisplayName: "Impostor", Category: "Test", ExecutorID: "pack.exec",
		Group: []node.NodeGroup{node.GroupTransform},
	})
	if err == nil {
		t.Fatal("a pack claimed the built-in namespace")
	}
	if !strings.Contains(err.Error(), "kilasflow.set") {
		t.Errorf("error = %v, want the offending type named", err)
	}

	// The same type outside the namespace is fine.
	if err := registry.RegisterFrom(node.SourcePack, node.Definition{
		Type: "waha.set", Version: workflow.V(2),
		DisplayName: "Fine", Category: "Test", ExecutorID: "pack.exec",
		Group: []node.NodeGroup{node.GroupTransform},
	}); err != nil {
		t.Errorf("a pack in its own namespace was refused: %v", err)
	}
}

// TestABuiltinAlwaysWinsAndTheLoserIsNamed pins precedence.
//
// "Last wins" would make the catalogue depend on load order, so it changes when
// a directory listing does. Refusing keeps it deterministic, and naming both
// sources is what tells whoever hits it which two things collided.
func TestABuiltinAlwaysWinsAndTheLoserIsNamed(t *testing.T) {
	builtin := node.Definition{
		Type: "kilasflow.thing", Version: workflow.V(1),
		DisplayName: "Built in", Category: "Test", ExecutorID: "core.exec",
		Group: []node.NodeGroup{node.GroupTransform},
	}
	pack := node.Definition{
		Type: "vendor.thing", Version: workflow.V(1),
		DisplayName: "From a pack", Category: "Test", ExecutorID: "pack.exec",
		Group: []node.NodeGroup{node.GroupTransform},
	}

	t.Run("a pack cannot displace a built-in", func(t *testing.T) {
		registry := node.NewRegistry()
		if err := registry.Register(builtin); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		// Renamed into the pack's own namespace so the namespace rule is not
		// what refuses it — precedence is.
		impostor := pack
		impostor.Type = builtin.Type
		err := registry.RegisterFrom(node.SourcePack, impostor)
		if err == nil {
			t.Fatal("a pack displaced a built-in")
		}
		// The built-in is untouched.
		stored, _ := registry.Get(builtin.Type, workflow.V(1))
		if stored.DisplayName != "Built in" {
			t.Errorf("the built-in was replaced: %#v", stored)
		}
	})

	t.Run("two packs colliding are refused, naming both", func(t *testing.T) {
		registry := node.NewRegistry()
		if err := registry.RegisterFrom(node.SourcePack, pack); err != nil {
			t.Fatalf("RegisterFrom() error = %v", err)
		}
		err := registry.RegisterFrom(node.SourceSidecar, pack)
		if err == nil {
			t.Fatal("a second registration of the same type and version was accepted")
		}
		for _, want := range []string{string(node.SourcePack), string(node.SourceSidecar), pack.Type} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to name %q", err, want)
			}
		}
	})
}

// TestEveryBuiltinIsTaggedAsSuch covers the whole shipped catalogue.
func TestEveryBuiltinIsTaggedAsSuch(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, definition := range registry.List() {
		if definition.Source != node.SourceBuiltin {
			t.Errorf("%s is tagged %q, want %q", definition.Type, definition.Source, node.SourceBuiltin)
		}
		if !strings.HasPrefix(definition.Type, node.BuiltinPrefix) {
			t.Errorf("%s is a built-in outside the %q namespace", definition.Type, node.BuiltinPrefix)
		}
	}
}
