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

func TestMySQLV2RegistersTheSameSixOperationValues(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.MySQLNodeType, nodes.MySQLV2Version)
	if !found {
		t.Fatalf("%s version %s is not registered", nodes.MySQLNodeType, nodes.MySQLV2Version)
	}

	var operation *node.PropertyDefinition
	keys := map[string]bool{}
	for index, parameter := range definition.Parameters {
		keys[parameter.Key] = true
		if parameter.Key == "operation" {
			operation = &definition.Parameters[index]
		}
	}
	if operation == nil {
		t.Fatal("version 2 declares no operation parameter")
	}
	got := make([]string, 0, len(operation.Options))
	for _, option := range operation.Options {
		got = append(got, option.Value)
	}
	// Verified against n8n 2.34.0's own MySql/v2/actions/database/
	// Database.resource.ts, which lists the same six values as its PostgreSQL
	// node — so the constants are shared rather than declared twice.
	want := []string{"deleteTable", "executeQuery", "insert", "upsert", "select", "update"}
	if len(got) != len(want) {
		t.Fatalf("operations = %#v, want exactly %#v", got, want)
	}
	for index, value := range want {
		if got[index] != value {
			t.Errorf("operation %d = %q, want %q", index, got[index], value)
		}
	}
	// n8n's MySQL node defaults to insert where its PostgreSQL node defaults to
	// executeQuery.
	if operation.Default != "insert" {
		t.Errorf("default = %#v, want n8n's insert", operation.Default)
	}

	// No schema, deliberately: MySQL has none separate from a database, so
	// `a`.`b` names database a's table b and a schema field would either
	// address the wrong database or be ignored.
	if keys["schema"] {
		t.Error("the MySQL node declares a schema, which MySQL does not have")
	}
	if !keys["table"] {
		t.Error("the MySQL node declares no table")
	}

	// The same collision check the PostgreSQL node carries, since registration
	// enforces it for every node.
	settings := map[string]bool{}
	for _, setting := range definition.SharedSettings {
		settings[setting.Key] = true
	}
	for key := range keys {
		if settings[key] {
			t.Errorf("version 2 declares %q as both a parameter and a shared setting", key)
		}
	}
}

func TestMySQLVersionOneStaysRegisteredAndResolves(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for name, testCase := range map[string]struct {
		asked workflow.TypeVersion
		want  workflow.TypeVersion
	}{
		"a document pinned at 1":        {asked: workflow.V(1), want: workflow.V(1)},
		"a version between rounds down": {asked: mustVersion(t, "1.9"), want: workflow.V(1)},
		"a document at 2":               {asked: workflow.V(2), want: nodes.MySQLV2Version},
		"an imported node at 2.4":       {asked: mustVersion(t, "2.4"), want: nodes.MySQLV2Version},
		"an imported node at 2.5":       {asked: mustVersion(t, "2.5"), want: nodes.MySQLV2Version},
	} {
		t.Run(name, func(t *testing.T) {
			resolved, found := registry.Resolve(nodes.MySQLNodeType, testCase.asked)
			if !found {
				t.Fatalf("Resolve(%s) found nothing", testCase.asked)
			}
			if resolved.Version != testCase.want {
				t.Errorf("Resolve(%s) = version %s, want %s", testCase.asked, resolved.Version, testCase.want)
			}
		})
	}

	// SQLite has no second version and keeps the shared factory: it has no
	// information schema to build pickers from and no n8n counterpart to match.
	if _, found := registry.Get(nodes.SQLiteNodeType, workflow.V(2)); found {
		t.Error("SQLite gained a version 2 it has no operation set for")
	}
	if _, found := registry.Get(nodes.SQLiteNodeType, workflow.V(1)); !found {
		t.Error("SQLite lost its version 1")
	}
}

func TestAVersionOneShapedMySQLDocumentIsRefusedLoudly(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.MySQLNodeType, nodes.MySQLV2Version)

	// Worse here than for PostgreSQL: every already-imported MySQL node is
	// sitting in storage as {"operation": "query"} with no statement at all,
	// because the old importer flattened every operation but executeQuery and
	// dropped the SQL with it.
	for _, operation := range []string{"query", "execute", "transaction"} {
		err := definition.Validate(workflow.Node{
			ID: "sql", Name: "SQL", Type: nodes.MySQLNodeType, TypeVersion: mustVersion(t, "2.4"),
			Parameters: map[string]any{"operation": operation, "statement": "SELECT 1"},
		})
		if err == nil {
			t.Fatalf("version 2 accepted the version 1 operation %q", operation)
		}
		if !strings.Contains(err.Error(), "typeVersion 1") || !strings.Contains(err.Error(), "executeQuery") {
			t.Errorf("error for %q = %v, want both the pin and the replacement named", operation, err)
		}
	}

	if err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.MySQLNodeType, TypeVersion: nodes.MySQLV2Version,
		Parameters: map[string]any{
			"operation": "insert",
			"table":     property.WriteLocator(property.Locator{Mode: "name", Value: "customers"}),
		},
	}); err != nil {
		t.Errorf("Validate() on a version 2 insert = %v, want accepted", err)
	}
}

func TestTheMySQLNodeBuildsMySQLShapedStatements(t *testing.T) {
	t.Parallel()

	// The node and the dialect have to agree, and only an end-to-end build
	// shows it: a node wired to the wrong dialect still registers, still
	// validates, and produces PostgreSQL SQL that a MySQL server rejects on the
	// first run.
	statement, err := nodes.BuildSQLStatementForTest(sqlbuild.MySQL, map[string]any{
		"operation": "select",
		"table":     property.WriteLocator(property.Locator{Mode: "name", Value: "customers"}),
		"where": []any{map[string]any{
			"field": "tier", "operator": "equals", "value": "gold",
		}},
		"limit": float64(10),
	})
	if err != nil {
		t.Fatalf("build error = %v", err)
	}
	if !strings.Contains(statement.SQL, "`customers`") {
		t.Errorf("SQL = %q, want a backtick-quoted table", statement.SQL)
	}
	if strings.Contains(statement.SQL, "$1") || !strings.Contains(statement.SQL, "?") {
		t.Errorf("SQL = %q, want ? placeholders", statement.SQL)
	}
}
