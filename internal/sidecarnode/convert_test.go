package sidecarnode_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// greetPackage is the fixture package's node as the runner describes it, cut
// down to the shapes the converter has to map. It is written out here rather
// than read from the fixture directory so these tests are pure Go: the Node
// process that produces this JSON is stage 2's contract and is covered by
// sidecar/runner_test.go.
func greetPackage() sidecarnode.PackageInfo {
	return sidecarnode.PackageInfo{
		Name:    "kf-fixture-nodes",
		Version: "1.4.0",
		Credentials: []sidecarnode.PackageCredential{{
			File:        "dist/credentials/FixtureApi.credentials.js",
			Name:        "fixtureApi",
			DisplayName: "Fixture API",
			Properties: []map[string]any{
				{"displayName": "API Key", "name": "apiKey", "type": "string", "typeOptions": map[string]any{"password": true}, "default": "", "required": true},
				{"displayName": "Base URL", "name": "baseUrl", "type": "string", "default": "https://example.invalid", "required": true},
			},
		}},
		Nodes: []sidecarnode.PackageNode{{
			File:    "dist/nodes/Greet/Greet.node.js",
			Name:    "fixtureGreet",
			Version: json.RawMessage("1"),
			Execute: true,
			Description: map[string]any{
				"displayName": "Fixture Greet",
				"name":        "fixtureGreet",
				"icon":        "file:Greet.svg",
				"group":       []any{"transform"},
				"version":     float64(1),
				"description": "Greets every input item using the fixture credential",
				"defaults":    map[string]any{"name": "Fixture Greet"},
				"inputs":      []any{"main"},
				"outputs":     []any{"main", "main"},
				"credentials": []any{map[string]any{"name": "fixtureApi", "required": true}},
				"properties": []any{
					map[string]any{"displayName": "Name", "name": "name", "type": "string", "default": "={{ $json.name }}", "required": true, "description": "The name to greet"},
					map[string]any{"displayName": "Mode", "name": "mode", "type": "options", "options": []any{
						map[string]any{"name": "Plain", "value": "plain"},
						map[string]any{"name": "Shout", "value": "shout"},
					}, "default": "plain"},
					map[string]any{"displayName": "Provider", "name": "providerId", "type": "options",
						"typeOptions": map[string]any{"loadOptionsMethod": "getProviders"}, "default": "", "required": true},
					map[string]any{"displayName": "Loud", "name": "loud", "type": "boolean", "default": false, "noDataExpression": true},
					map[string]any{"displayName": "Extras", "name": "extras", "type": "collection", "default": map[string]any{}, "options": []any{
						map[string]any{"displayName": "Suffix", "name": "suffix", "type": "string", "default": ""},
						map[string]any{"displayName": "Repeat", "name": "repeat", "type": "number", "default": float64(1)},
					}},
					map[string]any{"displayName": "Headers", "name": "headers", "type": "fixedCollection", "default": map[string]any{},
						"typeOptions": map[string]any{"multipleValues": true}, "options": []any{
							map[string]any{"displayName": "Header", "name": "values", "values": []any{
								map[string]any{"displayName": "Key", "name": "key", "type": "string", "default": ""},
								map[string]any{"displayName": "Value", "name": "value", "type": "string", "default": ""},
							}},
						}},
					map[string]any{"displayName": "Attachment Filename", "name": "attachmentFilename", "type": "string", "default": "",
						"displayOptions": map[string]any{"show": map[string]any{"mode": []any{"plain"}}}},
					map[string]any{"displayName": "Attachment Note", "name": "attachmentNote", "type": "string", "default": "",
						"displayOptions": map[string]any{"hide": map[string]any{"mode": []any{"shout"}}}},
					map[string]any{"displayName": "Hidden Value", "name": "hiddenValue", "type": "hidden", "default": "kept-out-of-the-editor"},
				},
			},
		}},
	}
}

func convert(t *testing.T, pkg sidecarnode.PackageInfo) ([]sidecarnode.Converted, []sidecarnode.Exclusion) {
	t.Helper()
	converted, _, excluded, err := sidecarnode.Convert(pkg)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(excluded) != 0 {
		t.Fatalf("Convert() excluded %v, want none", excluded)
	}
	return converted, excluded
}

func propertyNamed(t *testing.T, definition node.Definition, key string) node.PropertyDefinition {
	t.Helper()
	for _, declared := range definition.Parameters {
		if declared.Key == key {
			return declared
		}
	}
	t.Fatalf("%s has no parameter %q: %+v", definition.Type, key, definition.Parameters)
	return node.PropertyDefinition{}
}

// TestConvertMapsDescriptionToDefinition is the conversion contract: every
// property shape the fixture carries arrives as the control this registry
// models, with the group, ports, category and binding a sidecar node needs.
func TestConvertMapsDescriptionToDefinition(t *testing.T) {
	converted, _ := convert(t, greetPackage())
	if len(converted) != 1 {
		t.Fatalf("Convert() returned %d definitions, want 1", len(converted))
	}
	definition := converted[0].Definition

	if got, want := definition.Type, "sidecar.kf-fixture-nodes.fixtureGreet"; got != want {
		t.Errorf("type = %q, want %q", got, want)
	}
	if got, want := definition.Version, workflow.V(1); got.Compare(want) != 0 {
		t.Errorf("version = %s, want %s", got, want)
	}
	if got, want := definition.DisplayName, "Fixture Greet"; got != want {
		t.Errorf("display name = %q, want %q", got, want)
	}
	if got, want := definition.Description, "Greets every input item using the fixture credential"; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if got, want := definition.Category, sidecarnode.Category; got != want {
		t.Errorf("category = %q, want %q", got, want)
	}
	if got, want := definition.Group, []node.NodeGroup{node.GroupTransform}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("group = %v, want %v", got, want)
	}
	if got, want := definition.ExecutorID, sidecarnode.ExecutorID; got != want {
		t.Errorf("executor id = %q, want %q", got, want)
	}
	if len(definition.Inputs) != 1 || definition.Inputs[0].Name != "main" || definition.Inputs[0].Kind != workflow.ConnectionMain {
		t.Errorf("inputs = %+v, want one main input", definition.Inputs)
	}
	// The package declares ['main','main'] and no outputNames: port names have
	// to be unique in the catalogue, so the second one is synthesised.
	if len(definition.Outputs) != 2 || definition.Outputs[0].Name != "main" || definition.Outputs[1].Name != "main2" {
		t.Errorf("outputs = %+v, want main and main2", definition.Outputs)
	}
	if len(definition.Credentials) != 1 || definition.Credentials[0].Type != "fixtureApi" || !definition.Credentials[0].Required {
		t.Errorf("credentials = %+v, want a required fixtureApi", definition.Credentials)
	}

	name := propertyNamed(t, definition, "name")
	if name.Kind != property.KindString || !name.Required || name.Label != "Name" {
		t.Errorf("name = %+v, want a required string labelled Name", name)
	}
	// n8n marks an expression default with a leading '='; this registry spells
	// the same rule as a marker object, and the value keeps the braces.
	if !expression.IsExpression(name.Default) {
		t.Errorf("name default = %#v, want the expression marker", name.Default)
	}
	if marker, _ := name.Default.(map[string]any); marker != nil && marker["value"] != "{{ $json.name }}" {
		t.Errorf("name default value = %#v, want the braces kept", name.Default)
	}

	mode := propertyNamed(t, definition, "mode")
	if mode.Kind != property.KindOptions || len(mode.Options) != 2 || mode.Options[0] != (node.PropertyOption{Label: "Plain", Value: "plain"}) {
		t.Errorf("mode = %+v, want two static options", mode)
	}
	if mode.Default != "plain" {
		t.Errorf("mode default = %#v, want plain", mode.Default)
	}

	provider := propertyNamed(t, definition, "providerId")
	if provider.Kind != property.KindString {
		t.Errorf("providerId kind = %q, want a string: a dynamic list cannot be served", provider.Kind)
	}
	if !strings.Contains(provider.Description, "getProviders") {
		t.Errorf("providerId description = %q, want it to name the loader it cannot reach", provider.Description)
	}
	if provider.TypeOptions != nil {
		t.Errorf("providerId typeOptions = %+v, want them dropped with the list", provider.TypeOptions)
	}

	loud := propertyNamed(t, definition, "loud")
	if loud.Kind != property.KindBoolean || loud.Default != false {
		t.Errorf("loud = %+v, want a boolean defaulting to false", loud)
	}

	extras := propertyNamed(t, definition, "extras")
	if extras.Kind != property.KindCollection || len(extras.Fields) != 2 {
		t.Fatalf("extras = %+v, want a collection of two fields", extras)
	}
	if extras.Fields[1].Key != "repeat" || extras.Fields[1].Kind != property.KindNumber {
		t.Errorf("extras.repeat = %+v, want a number", extras.Fields[1])
	}

	headers := propertyNamed(t, definition, "headers")
	if headers.Kind != property.KindFixedCollection || len(headers.Groups) != 1 {
		t.Fatalf("headers = %+v, want a fixed collection with one group", headers)
	}
	if headers.Groups[0].Key != "values" || len(headers.Groups[0].Fields) != 2 {
		t.Errorf("headers group = %+v, want values with two fields", headers.Groups[0])
	}
	if headers.TypeOptions == nil || !headers.TypeOptions.MultipleValues {
		t.Errorf("headers typeOptions = %+v, want multipleValues kept", headers.TypeOptions)
	}

	shown := propertyNamed(t, definition, "attachmentFilename")
	if len(shown.DisplayOptions.Show) != 1 || shown.DisplayOptions.Show[0].Key != "mode" {
		t.Errorf("attachmentFilename visibility = %+v, want a show rule on mode", shown.DisplayOptions)
	}
	hidden := propertyNamed(t, definition, "attachmentNote")
	if len(hidden.DisplayOptions.Hide) != 1 || hidden.DisplayOptions.Hide[0].Key != "mode" {
		t.Errorf("attachmentNote visibility = %+v, want a hide rule on mode", hidden.DisplayOptions)
	}

	// A hidden property has no control, so it is not a parameter; its default
	// still has to reach the package, which is what the index carries.
	for _, declared := range definition.Parameters {
		if declared.Key == "hiddenValue" {
			t.Errorf("hiddenValue was emitted as a parameter")
		}
	}
	if got, want := converted[0].Ref.HiddenDefaults["hiddenValue"], "kept-out-of-the-editor"; got != want {
		t.Errorf("hidden default = %#v, want %#v", got, want)
	}
	if got, want := converted[0].Ref.Name, "fixtureGreet"; got != want {
		t.Errorf("dispatch name = %q, want %q", got, want)
	}
	if got, want := converted[0].Ref.NodeVersion, float64(1); got != want {
		t.Errorf("dispatch version = %v, want %v", got, want)
	}
}

// TestConvertExcludesUnsupportedShapesWithANamedReason is the fail-closed rule:
// a shape this build cannot marshal excludes its node with a reason an operator
// can act on, and never half-registers it.
func TestConvertExcludesUnsupportedShapesWithANamedReason(t *testing.T) {
	base := func(mutate func(*sidecarnode.PackageInfo)) sidecarnode.PackageInfo {
		pkg := greetPackage()
		mutate(&pkg)
		return pkg
	}
	withProperty := func(declared map[string]any) func(*sidecarnode.PackageInfo) {
		return func(pkg *sidecarnode.PackageInfo) {
			description := pkg.Nodes[0].Description
			description["properties"] = append([]any{declared}, description["properties"].([]any)...)
		}
	}

	cases := []struct {
		name     string
		pkg      sidecarnode.PackageInfo
		contains string
	}{
		{
			name: "trigger group",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Description["group"] = []any{"trigger"}
			}),
			contains: "trigger",
		},
		{
			name: "webhook method",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Unsupported = []string{"webhook"}
			}),
			contains: "webhook",
		},
		{
			name: "no execute",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Execute = false
			}),
			contains: "execute()",
		},
		{
			name: "non main input",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Description["inputs"] = []any{"ai_tool"}
			}),
			contains: "ai_tool",
		},
		{
			name: "two main inputs",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Description["inputs"] = []any{"main", "main"}
			}),
			contains: "main",
		},
		{
			name: "input declared as an object",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Description["inputs"] = []any{map[string]any{"type": "main"}}
			}),
			contains: "not a named connection type",
		},
		{
			name:     "resource locator",
			pkg:      base(withProperty(map[string]any{"displayName": "Document", "name": "documentId", "type": "resourceLocator", "default": map[string]any{}})),
			contains: "resourceLocator",
		},
		{
			name:     "numeric option value",
			pkg:      base(withProperty(map[string]any{"displayName": "Precision", "name": "precision", "type": "options", "options": []any{map[string]any{"name": "Low", "value": float64(1)}}, "default": float64(1)})),
			contains: "not a string",
		},
		{
			name: "unknown credential type",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Description["credentials"] = []any{map[string]any{"name": "fixtureUnknownCredential", "required": true}}
			}),
			contains: "fixtureUnknownCredential",
		},
		{
			name: "property with no name",
			pkg:  base(withProperty(map[string]any{"displayName": "Nameless", "type": "string", "default": ""})),
		},
		{
			name: "no version",
			pkg: base(func(pkg *sidecarnode.PackageInfo) {
				pkg.Nodes[0].Version = json.RawMessage(`null`)
			}),
			contains: "version",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			converted, _, excluded, err := sidecarnode.Convert(testCase.pkg)
			if err != nil {
				t.Fatalf("Convert() error = %v", err)
			}
			if len(converted) != 0 {
				t.Fatalf("Convert() registered %d definitions for a shape it cannot serve", len(converted))
			}
			if len(excluded) != 1 {
				t.Fatalf("exclusions = %+v, want exactly one", excluded)
			}
			if excluded[0].Reason == "" {
				t.Errorf("exclusion %+v carries no reason", excluded[0])
			}
			if testCase.contains != "" && !strings.Contains(excluded[0].Reason, testCase.contains) {
				t.Errorf("reason = %q, want it to name %q", excluded[0].Reason, testCase.contains)
			}
		})
	}
}

// TestTypeIDIsURLSafeAndNeverInTheBuiltinNamespace covers the identifier a
// scoped npm package would otherwise break: the type is a URL path segment
// (/node-types/{type}/icon) and may never claim the built-in namespace.
func TestTypeIDIsURLSafeAndNeverInTheBuiltinNamespace(t *testing.T) {
	pkg := greetPackage()
	pkg.Name = "@scope/some pkg"
	converted, _ := convert(t, pkg)
	if len(converted) != 1 {
		t.Fatalf("Convert() returned %d definitions, want 1", len(converted))
	}
	nodeType := converted[0].Definition.Type
	if got, want := nodeType, "sidecar.scope-some-pkg.fixtureGreet"; got != want {
		t.Errorf("type = %q, want %q", got, want)
	}
	if strings.HasPrefix(nodeType, node.BuiltinPrefix) {
		t.Errorf("type %q claims the built-in namespace", nodeType)
	}
	for _, character := range nodeType {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '.', character == '_', character == '-':
		default:
			t.Errorf("type %q contains %q, which is not safe in a URL path", nodeType, character)
		}
	}

	// A node file whose description name is not an identifier must not be able
	// to produce a type the icon route cannot address either.
	pkg = greetPackage()
	pkg.Nodes[0].Description["name"] = "fixture greet/one"
	converted, _ = convert(t, pkg)
	if got, want := converted[0].Definition.Type, "sidecar.kf-fixture-nodes.fixture-greet-one"; got != want {
		t.Errorf("type = %q, want %q", got, want)
	}
}

// TestCredentialTypeMarksEveryFieldSecret is the credential policy: a community
// credential is encrypted whole and this build does not run the package's own
// authenticate block, so every field is withheld.
func TestCredentialTypeMarksEveryFieldSecret(t *testing.T) {
	_, types, _, err := sidecarnode.Convert(greetPackage())
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("credential types = %+v, want one", types)
	}
	credentialType := types[0]
	if got, want := credentialType.ID, "fixtureApi"; got != want {
		t.Errorf("credential id = %q, want %q", got, want)
	}
	if got, want := credentialType.DisplayName, "Fixture API"; got != want {
		t.Errorf("credential display name = %q, want %q", got, want)
	}
	if credentialType.Authenticate != nil {
		t.Errorf("credential authenticate = %+v, want none in v1", credentialType.Authenticate)
	}
	if len(credentialType.Secrets) != len(credentialType.Properties) || len(credentialType.Properties) != 2 {
		t.Fatalf("secrets = %v over %d properties, want every field", credentialType.Secrets, len(credentialType.Properties))
	}
	for _, field := range credentialType.Properties {
		found := false
		for _, secret := range credentialType.Secrets {
			if secret == field.Key {
				found = true
			}
		}
		if !found {
			t.Errorf("credential field %q is not marked secret", field.Key)
		}
	}
}

// TestConvertExcludesAParameterThatCollidesWithASharedSetting covers the one
// collision the registry refuses outright: a community parameter named after a
// setting every node already carries.
func TestConvertExcludesAParameterThatCollidesWithASharedSetting(t *testing.T) {
	pkg := greetPackage()
	description := pkg.Nodes[0].Description
	description["properties"] = append([]any{
		map[string]any{"displayName": "Timeout", "name": "timeoutSeconds", "type": "number", "default": float64(0)},
	}, description["properties"].([]any)...)

	converted, _, excluded, err := sidecarnode.Convert(pkg)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(converted) != 0 {
		t.Fatalf("Convert() registered %d definitions for a node whose parameter collides with a shared setting", len(converted))
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0].Reason, "timeoutSeconds") {
		t.Fatalf("exclusions = %+v, want one naming timeoutSeconds", excluded)
	}
}

// TestConvertRefusesAnOutputNamedError covers the reserved port: the engine
// sizes the executor's answer from the last port's name, so a package output of
// the same name would make that sizing wrong.
func TestConvertRefusesAnOutputNamedError(t *testing.T) {
	pkg := greetPackage()
	description := pkg.Nodes[0].Description
	description["outputNames"] = []any{"success", "error"}

	converted, _, excluded, err := sidecarnode.Convert(pkg)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(converted) != 0 {
		t.Fatalf("Convert() registered %d definitions for a node with an output named error", len(converted))
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0].Reason, "reserved") {
		t.Fatalf("exclusions = %+v, want one naming the reserved port", excluded)
	}

	// A package that names its outputs distinctly keeps them.
	pkg = greetPackage()
	pkg.Nodes[0].Description["outputNames"] = []any{"success", "failed"}
	converted, _ = convert(t, pkg)
	if got := converted[0].Definition.Outputs[1].Name; got != "failed" {
		t.Errorf("second output = %q, want the package's own name", got)
	}
}

// TestConvertDryRunExcludesInvalidVisibility is what the trial registration is
// for: a rule the catalogue refuses excludes that node with the registry's own
// message, instead of failing the boot or, worse, registering a node whose
// fields can never be shown.
func TestConvertDryRunExcludesInvalidVisibility(t *testing.T) {
	pkg := greetPackage()
	description := pkg.Nodes[0].Description
	description["properties"] = append([]any{
		map[string]any{"displayName": "Feature", "name": "featureFlag", "type": "string", "default": "",
			"displayOptions": map[string]any{"show": map[string]any{"@feature": []any{"nope"}}}},
	}, description["properties"].([]any)...)

	converted, _, excluded, err := sidecarnode.Convert(pkg)
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(converted) != 0 {
		t.Fatalf("Convert() registered %d definitions with an unsupported visibility key", len(converted))
	}
	if len(excluded) != 1 {
		t.Fatalf("exclusions = %+v, want one", excluded)
	}
	// The reason is the registry's own, so the exclusion can never disagree
	// with the rule that produced it.
	if !strings.Contains(excluded[0].Reason, "@feature") {
		t.Errorf("reason = %q, want the registry's message about @feature", excluded[0].Reason)
	}
}

// TestConvertKeepsOneVersionedNameAtSeveralVersions covers the shape the
// owner's own package ships: one node name at two versions in two files.
func TestConvertKeepsOneVersionedNameAtSeveralVersions(t *testing.T) {
	pkg := sidecarnode.PackageInfo{Name: "kf-fixture-versions", Version: "0.2.0"}
	for _, declared := range []struct {
		file    string
		version float64
	}{{"dist/nodes/Foo.node.js", 1}, {"dist/nodes/v2/FooV2.node.js", 2}} {
		pkg.Nodes = append(pkg.Nodes, sidecarnode.PackageNode{
			File: declared.file, Name: "fixtureVersioned",
			Version: json.RawMessage(fmt.Sprintf("%d", int(declared.version))),
			Execute: true,
			Description: map[string]any{
				"displayName": "Fixture Versioned Node", "name": "fixtureVersioned",
				"group": []any{"transform"}, "version": declared.version,
				"defaults": map[string]any{"name": "Fixture Versioned Node"},
				"inputs":   []any{"main"}, "outputs": []any{"main"},
				"properties": []any{map[string]any{"displayName": "Text", "name": "text", "type": "string", "default": ""}},
			},
		})
	}

	converted, _ := convert(t, pkg)
	if len(converted) != 2 {
		t.Fatalf("Convert() returned %d definitions, want one per version", len(converted))
	}
	seen := map[float64]bool{}
	for _, one := range converted {
		if one.Definition.Type != "sidecar.kf-fixture-versions.fixtureVersioned" {
			t.Errorf("type = %q, want both versions under one type", one.Definition.Type)
		}
		seen[one.Ref.NodeVersion] = true
	}
	if !seen[1] || !seen[2] {
		t.Errorf("dispatch versions = %v, want 1 and 2", seen)
	}

	// A file that declares an array of versions is listed once per version by
	// the runner, and both entries carry the whole array: the conversion
	// registers each version once.
	pkg.Nodes = nil
	pkg.Nodes = append(pkg.Nodes, sidecarnode.PackageNode{
		File: "dist/nodes/Foo.node.js", Name: "fixtureVersioned",
		Version: json.RawMessage(`[1, 2]`), Execute: true,
		Description: map[string]any{
			"displayName": "Fixture Versioned Node", "name": "fixtureVersioned",
			"group": []any{"transform"}, "version": []any{float64(1), float64(2)},
			"defaults": map[string]any{"name": "Fixture Versioned Node"},
			"inputs":   []any{"main"}, "outputs": []any{"main"},
			"properties": []any{map[string]any{"displayName": "Text", "name": "text", "type": "string", "default": ""}},
		},
	})
	pkg.Nodes = append(pkg.Nodes, pkg.Nodes[0])
	converted, _ = convert(t, pkg)
	if len(converted) != 2 {
		t.Fatalf("Convert() returned %d definitions for a two-version file, want 2", len(converted))
	}
}
