package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func sheetsRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return registry
}

func TestGoogleSheetsNodeAndTriggerAreRegistered(t *testing.T) {
	t.Parallel()

	registry := sheetsRegistry(t)
	action, ok := registry.Get(nodes.GoogleSheetsNodeType, workflow.V(1))
	if !ok {
		t.Fatal("kilasflow.googleSheets is not registered")
	}
	trigger, ok := registry.Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	if !ok {
		t.Fatal("kilasflow.googleSheetsTrigger is not registered")
	}
	if len(action.Credentials) != 1 || action.Credentials[0].Type != "googleSheetsOAuth2Api" || !action.Credentials[0].Required {
		t.Errorf("action credentials = %#v, want a required googleSheetsOAuth2Api", action.Credentials)
	}
	if len(trigger.Credentials) != 1 || trigger.Credentials[0].Type != "googleSheetsTriggerOAuth2Api" || !trigger.Credentials[0].Required {
		t.Errorf("trigger credentials = %#v, want a required googleSheetsTriggerOAuth2Api", trigger.Credentials)
	}
	if action.Icon == nil || action.Icon.Light != "builtin:sheet" || trigger.Icon == nil || trigger.Icon.Light != "builtin:sheet" {
		t.Errorf("icons = %#v / %#v, want the sheet glyph on both", action.Icon, trigger.Icon)
	}
	if len(trigger.Inputs) != 0 {
		t.Errorf("trigger inputs = %#v, want none", trigger.Inputs)
	}
}

// The operation strings are n8n's own, and read is the default.
func TestGoogleSheetsOperationsAreN8NsStrings(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsNodeType, workflow.V(1))
	var operation node.PropertyDefinition
	var columns node.PropertyDefinition
	var documentID, sheetName node.PropertyDefinition
	for _, parameter := range definition.Parameters {
		switch parameter.Key {
		case "operation":
			operation = parameter
		case "columns":
			columns = parameter
		case "documentId":
			documentID = parameter
		case "sheetName":
			sheetName = parameter
		}
	}
	if operation.Default != "read" {
		t.Errorf("operation default = %#v, want read", operation.Default)
	}
	got := make([]string, 0, len(operation.Options))
	for _, option := range operation.Options {
		got = append(got, option.Value)
	}
	want := []string{"append", "appendOrUpdate", "clear", "delete", "read", "update"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("operations = %v, want %v", got, want)
	}

	if columns.Kind != node.PropertyResourceMapper || columns.Mapper == nil || !columns.Mapper.SupportsAutoMap {
		t.Errorf("columns = %#v, want a resource mapper that supports auto mapping", columns)
	}
	if columns.Mapper != nil && columns.Mapper.Schema.Name != "googleSheets.columns" {
		t.Errorf("columns schema loader = %q", columns.Mapper.Schema.Name)
	}
	modes := func(definition node.PropertyDefinition) string {
		names := make([]string, 0, len(definition.Modes))
		for _, mode := range definition.Modes {
			names = append(names, mode.Name)
		}
		return strings.Join(names, ",")
	}
	if documentID.Kind != node.PropertyResourceLocator || modes(documentID) != "list,url,id" {
		t.Errorf("documentId modes = %q, want list,url,id", modes(documentID))
	}
	if sheetName.Kind != node.PropertyResourceLocator || modes(sheetName) != "list,url,id,name" {
		t.Errorf("sheetName modes = %q, want list,url,id,name", modes(sheetName))
	}
	for _, mode := range documentID.Modes {
		if mode.Name == "list" && (mode.LoadOptions == nil || mode.LoadOptions.Source != property.LoaderInternal || mode.LoadOptions.CredentialType != "googleSheetsOAuth2Api") {
			t.Errorf("documentId list loader = %#v", mode.LoadOptions)
		}
	}
	for _, mode := range sheetName.Modes {
		if mode.Name == "list" && (mode.LoadOptions == nil || mode.LoadOptions.CredentialType != "googleSheetsOAuth2Api" ||
			len(mode.LoadOptions.DependsOn) != 1 || mode.LoadOptions.DependsOn[0] != "documentId") {
			t.Errorf("sheetName list loader = %#v", mode.LoadOptions)
		}
	}
}

func TestGoogleSheetsTriggerEventsAreN8NsStrings(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	for _, parameter := range definition.Parameters {
		if parameter.Key != "event" {
			continue
		}
		got := make([]string, 0, len(parameter.Options))
		for _, option := range parameter.Options {
			got = append(got, option.Value)
		}
		if strings.Join(got, ",") != "rowAdded,rowUpdate,anyUpdate" || parameter.Default != "anyUpdate" {
			t.Errorf("event options = %v default %#v, want rowAdded,rowUpdate,anyUpdate defaulting to anyUpdate", got, parameter.Default)
		}
		return
	}
	t.Fatal("the trigger has no event parameter")
}

func sheetsNode(parameters map[string]any) workflow.Node {
	if _, present := parameters["documentId"]; !present {
		parameters["documentId"] = map[string]any{"__rl": true, "mode": "id", "value": "doc-1"}
	}
	if _, present := parameters["sheetName"]; !present {
		parameters["sheetName"] = map[string]any{"__rl": true, "mode": "name", "value": "Sheet1"}
	}
	return workflow.Node{
		ID: "sheets", Name: "Sheets", Type: nodes.GoogleSheetsNodeType, TypeVersion: workflow.V(1),
		Parameters:  parameters,
		Credentials: map[string]string{"googleSheetsOAuth2Api": "cred"},
	}
}

func TestGoogleSheetsValidateRefusesWhatItDoesNotImplement(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsNodeType, workflow.V(1))
	match := func(columns ...string) map[string]any {
		matching := make([]any, 0, len(columns))
		for _, column := range columns {
			matching = append(matching, column)
		}
		return map[string]any{"mappingMode": "defineBelow", "value": map[string]any{"id": "1"}, "matchingColumns": matching}
	}
	for _, test := range []struct {
		name       string
		parameters map[string]any
		want       string // empty means valid
	}{
		{"read by default", map[string]any{}, ""},
		{"append", map[string]any{"operation": "append", "columns": map[string]any{"mappingMode": "autoMapInputData", "value": map[string]any{}}}, ""},
		{"update on one column", map[string]any{"operation": "update", "columns": match("id")}, ""},
		{"appendOrUpdate on one column", map[string]any{"operation": "appendOrUpdate", "columns": match("id")}, ""},
		{"service account", map[string]any{"authentication": "serviceAccount"}, "OAuth2"},
		{"spreadsheet resource", map[string]any{"resource": "spreadsheet"}, "resource"},
		{"create", map[string]any{"operation": "create"}, "create"},
		{"remove", map[string]any{"operation": "remove"}, "remove"},
		{"update without a matching column", map[string]any{"operation": "update", "columns": match()}, "matching column"},
		{"update with no mapper at all", map[string]any{"operation": "update"}, "matching column"},
		{"appendOrUpdate on two columns", map[string]any{"operation": "appendOrUpdate", "columns": match("id", "email")}, "one matching column"},
		{"no document", map[string]any{"documentId": map[string]any{"__rl": true, "mode": "list", "value": ""}}, "documentId"},
		{"no sheet", map[string]any{"sheetName": map[string]any{"__rl": true, "mode": "list", "value": ""}}, "sheetName"},
		{"a document from an expression", map[string]any{"documentId": map[string]any{"__rl": true, "mode": "id",
			"value": map[string]any{"mode": "expression", "value": "{{ $json.doc }}"}}}, ""},
		{"a sheet gid of zero", map[string]any{"sheetName": map[string]any{"__rl": true, "mode": "list", "value": float64(0)}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := definition.Validate(sheetsNode(test.parameters))
			switch {
			case test.want == "" && err != nil:
				t.Fatalf("Validate() = %v, want it valid", err)
			case test.want != "" && err == nil:
				t.Fatalf("Validate() = nil, want an error mentioning %q", test.want)
			case test.want != "" && !strings.Contains(err.Error(), test.want):
				t.Fatalf("Validate() = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestGoogleSheetsTriggerValidateRefusesWhatItDoesNotImplement(t *testing.T) {
	t.Parallel()

	definition, _ := sheetsRegistry(t).Get(nodes.GoogleSheetsTriggerNodeType, workflow.V(1))
	build := func(parameters map[string]any) workflow.Node {
		parameters["documentId"] = map[string]any{"__rl": true, "mode": "id", "value": "doc-1"}
		parameters["sheetName"] = map[string]any{"__rl": true, "mode": "name", "value": "Sheet1"}
		return workflow.Node{
			ID: "trigger", Name: "Trigger", Type: nodes.GoogleSheetsTriggerNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Credentials: map[string]string{"googleSheetsTriggerOAuth2Api": "cred"},
		}
	}
	if err := definition.Validate(build(map[string]any{})); err != nil {
		t.Errorf("Validate() default = %v", err)
	}
	if err := definition.Validate(build(map[string]any{"authentication": "serviceAccount"})); err == nil || !strings.Contains(err.Error(), "OAuth2") {
		t.Errorf("Validate(serviceAccount) = %v, want a refusal", err)
	}
	if err := definition.Validate(build(map[string]any{"event": "rowDeleted"})); err == nil || !strings.Contains(err.Error(), "event") {
		t.Errorf("Validate(unknown event) = %v, want a refusal", err)
	}
}

func TestGoogleSheetsExecutorsAreRegistered(t *testing.T) {
	t.Parallel()

	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	for _, id := range []string{nodes.GoogleSheetsExecutorID, nodes.GoogleSheetsTriggerExecutorID} {
		if _, found := registry.Lookup(id); !found {
			t.Errorf("executor %q is not registered", id)
		}
	}
}
