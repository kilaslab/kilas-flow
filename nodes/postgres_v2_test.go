package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/sqlbuild"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestPostgresV2RegistersN8NsOperationValuesVerbatim(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.PostgresNodeType, nodes.PostgresV2Version)
	if !found {
		t.Fatalf("%s version %s is not registered", nodes.PostgresNodeType, nodes.PostgresV2Version)
	}

	var operation *node.PropertyDefinition
	for index, parameter := range definition.Parameters {
		if parameter.Key == "operation" {
			operation = &definition.Parameters[index]
		}
	}
	if operation == nil {
		t.Fatal("version 2 declares no operation parameter")
	}
	// The value strings are the contract, not the labels: an imported node's
	// operation either lands on a shape that runs or it does not, and matching
	// labels help nobody if the strings differ.
	got := make([]string, 0, len(operation.Options))
	for _, option := range operation.Options {
		got = append(got, option.Value)
	}
	want := []string{"deleteTable", "executeQuery", "insert", "upsert", "select", "update"}
	if len(got) != len(want) {
		t.Fatalf("operations = %#v, want exactly %#v", got, want)
	}
	for index, value := range want {
		if got[index] != value {
			t.Errorf("operation %d = %q, want %q", index, got[index], value)
		}
	}
}

func TestVersionOneStaysRegisteredAndUnaltered(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	// Frozen, not deprecated and not rewritten: a workflow authored against
	// query/execute/transaction keeps running exactly as it did.
	v1, found := registry.Get(nodes.PostgresNodeType, workflow.V(1))
	if !found {
		t.Fatal("version 1 is no longer registered")
	}
	operations := map[string]bool{}
	for _, parameter := range v1.Parameters {
		if parameter.Key != "operation" {
			continue
		}
		for _, option := range parameter.Options {
			operations[option.Value] = true
		}
	}
	for _, want := range []string{"query", "execute", "transaction"} {
		if !operations[want] {
			t.Errorf("version 1 lost the %q operation", want)
		}
	}

	// Resolve picks the highest version at or below what a document asks for,
	// so the two versions must land where their documents expect.
	for name, testCase := range map[string]struct {
		asked workflow.TypeVersion
		want  workflow.TypeVersion
	}{
		"a document pinned at 1":                {asked: workflow.V(1), want: workflow.V(1)},
		"a document at 2":                       {asked: workflow.V(2), want: nodes.PostgresV2Version},
		"an imported node at n8n's 2.4":         {asked: mustVersion(t, "2.4"), want: nodes.PostgresV2Version},
		"an imported node at n8n's 2.7":         {asked: mustVersion(t, "2.7"), want: nodes.PostgresV2Version},
		"a version between the two rounds down": {asked: mustVersion(t, "1.9"), want: workflow.V(1)},
	} {
		t.Run(name, func(t *testing.T) {
			resolved, found := registry.Resolve(nodes.PostgresNodeType, testCase.asked)
			if !found {
				t.Fatalf("Resolve(%s) found nothing", testCase.asked)
			}
			if resolved.Version != testCase.want {
				t.Errorf("Resolve(%s) = version %s, want %s", testCase.asked, resolved.Version, testCase.want)
			}
		})
	}
}

func TestAVersionOneShapedDocumentAtVersionTwoIsRefusedLoudly(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.PostgresNodeType, nodes.PostgresV2Version)

	// The silent flip. The moment version 2 registered, every stored node
	// carrying n8n's typeVersion of 2.4 or higher stopped resolving to version
	// 1 while still holding parameters written for version 1. Without this the
	// first sign would be a run-time error on an unknown operation, with
	// nothing firing at save, at activation, or in the editor.
	for _, operation := range []string{"query", "execute", "transaction"} {
		err := definition.Validate(workflow.Node{
			ID: "sql", Name: "SQL", Type: nodes.PostgresNodeType, TypeVersion: mustVersion(t, "2.4"),
			Parameters: map[string]any{"operation": operation, "statement": "SELECT 1"},
		})
		if err == nil {
			t.Fatalf("version 2 accepted the version 1 operation %q", operation)
		}
		// The message has to say what to do, or it is only a refusal.
		if !strings.Contains(err.Error(), "typeVersion 1") || !strings.Contains(err.Error(), "executeQuery") {
			t.Errorf("error for %q = %v, want both the pin and the replacement named", operation, err)
		}
	}

	// The v2 shape is accepted.
	if err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.PostgresNodeType, TypeVersion: nodes.PostgresV2Version,
		Parameters: map[string]any{"operation": "executeQuery", "query": "SELECT 1"},
	}); err != nil {
		t.Errorf("Validate() on a version 2 query = %v, want accepted", err)
	}
	if err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.PostgresNodeType, TypeVersion: nodes.PostgresV2Version,
		Parameters: map[string]any{
			"operation": "insert",
			"table":     property.WriteLocator(property.Locator{Mode: "name", Value: "customers"}),
		},
	}); err != nil {
		t.Errorf("Validate() on a version 2 insert = %v, want accepted", err)
	}
	// An operation needing a table and given none is refused, naming it.
	err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.PostgresNodeType, TypeVersion: nodes.PostgresV2Version,
		Parameters: map[string]any{"operation": "insert"},
	})
	if err == nil || !strings.Contains(err.Error(), "table") {
		t.Errorf("error = %v, want the missing table named", err)
	}
}

func TestVersionTwoDeclaresNoKeyItsSharedSettingsAlsoDeclare(t *testing.T) {
	t.Parallel()

	// Registration enforces this for every node, so a definition that collided
	// would not register at all — which is what this asserts by registering.
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.PostgresNodeType, nodes.PostgresV2Version)
	settings := map[string]bool{}
	for _, setting := range definition.SharedSettings {
		settings[setting.Key] = true
	}
	for _, parameter := range definition.Parameters {
		if settings[parameter.Key] {
			t.Errorf("version 2 declares %q as both a parameter and a shared setting", parameter.Key)
		}
	}
}

func mustVersion(t *testing.T, text string) workflow.TypeVersion {
	t.Helper()
	version, err := workflow.ParseTypeVersion(text)
	if err != nil {
		t.Fatalf("ParseTypeVersion(%q) error = %v", text, err)
	}
	return version
}

func TestAnImportedNullConditionBuildsTheNullTestItMeans(t *testing.T) {
	t.Parallel()

	// The assertion has to be on the emitted SQL. n8n spells this condition
	// "IS NULL"; the importer maps it into the shared vocabulary and the
	// builder maps it back out, and the exporter's map is the literal inverse
	// of the importer's — so an inversion in the middle round-trips perfectly
	// while a delete removes the exact complement of the rows it was meant to.
	for name, testCase := range map[string]struct {
		operator string
		wantSQL  string
		wrongSQL string
	}{
		"a row that must be null":     {operator: "notExists", wantSQL: "IS NULL", wrongSQL: "IS NOT NULL"},
		"a row that must not be null": {operator: "exists", wantSQL: "IS NOT NULL", wrongSQL: "IS NULL"},
	} {
		t.Run(name, func(t *testing.T) {
			statement, err := nodes.BuildSQLStatementForTest(sqlbuild.Postgres, map[string]any{
				"operation": "select",
				"table":     property.WriteLocator(property.Locator{Mode: "name", Value: "customers"}),
				"where": []any{map[string]any{
					"field": "deleted_at", "operator": testCase.operator,
				}},
			})
			if err != nil {
				t.Fatalf("build error = %v", err)
			}
			if !strings.Contains(statement.SQL, `"deleted_at" `+testCase.wantSQL) {
				t.Errorf("SQL = %q, want %q", statement.SQL, testCase.wantSQL)
			}
			// Belt and braces: IS NULL is a prefix of IS NOT NULL nowhere, but
			// the inverse test is what the earlier defect would have failed.
			if testCase.wantSQL == "IS NULL" && strings.Contains(statement.SQL, testCase.wrongSQL) {
				t.Errorf("SQL = %q, want no %q", statement.SQL, testCase.wrongSQL)
			}
			// A null test binds nothing.
			if len(statement.Parameters) != 0 {
				t.Errorf("parameters = %#v, want a null test to bind nothing", statement.Parameters)
			}
		})
	}
}
