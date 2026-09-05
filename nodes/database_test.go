package nodes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func databaseNode(t *testing.T, nodeType, credentialType, credentialID string, parameters map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	return workflow.IRNode{
		ID: "db-1", Name: "Database", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Credentials: map[string]string{credentialType: credentialID},
		Definition: definition,
	}
}

func sqliteCredential(path string) *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Workflow database", Type: "sqlite",
		Fields: map[string]string{"path": path},
	}}
}

func TestDatabaseNodesAreRegisteredWithMetadataDrivenConfiguration(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for nodeType, credentialType := range map[string]string{
		nodes.PostgresNodeType: "postgres",
		nodes.MySQLNodeType:    "mysql",
		nodes.SQLiteNodeType:   "sqlite",
	} {
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found {
			t.Fatalf("%s is not registered", nodeType)
		}
		if definition.Category != "Database" {
			t.Errorf("%s category = %q, want Database", nodeType, definition.Category)
		}
		keys := map[string]bool{}
		for _, parameter := range definition.Parameters {
			keys[parameter.Key] = true
		}
		for _, required := range []string{"operation", "statement", "parameters", "timeoutSeconds", "maxRows"} {
			if !keys[required] {
				t.Errorf("%s is missing parameter %q", nodeType, required)
			}
		}
		if got, _ := nodes.DatabaseCredentialType(nodeType); got != credentialType {
			t.Errorf("%s credential type = %q, want %q", nodeType, got, credentialType)
		}
	}
}

func TestDatabaseNodeRequiresACredential(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	// There is deliberately no fallback connection, so a node without a
	// credential has nothing it could legally reach. The requirement is
	// declared on the definition and enforced by the compiler, so it is the
	// compiler that has to be asked — a second check inside Validate would
	// report the same thing twice in two different wordings.
	document := func(credentials map[string]string) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_db", Name: "Query",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{
					ID: "sql", Name: "SQL", Type: nodes.SQLiteNodeType, TypeVersion: workflow.V(1),
					Parameters:  map[string]any{"operation": "query", "statement": "SELECT 1"},
					Credentials: credentials,
				},
			},
			Connections: []workflow.Connection{{
				ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "sql", Port: "main"},
			}},
			Settings: map[string]any{},
		}
	}

	_, err := workflow.Compile(document(nil), registry)
	if err == nil || !strings.Contains(err.Error(), "requires a sqlite credential") {
		t.Fatalf("Compile() = %v, want a credential requirement", err)
	}
	if _, err := workflow.Compile(document(map[string]string{"sqlite": "cred-1"}), registry); err != nil {
		t.Errorf("Compile() with a credential = %v, want accepted", err)
	}
	// An attached-but-empty reference is not attached.
	if _, err := workflow.Compile(document(map[string]string{"sqlite": "  "}), registry); err == nil {
		t.Error("Compile() accepted an empty credential reference")
	}
}

func TestDatabaseNodeQueriesAndMapsRowsToItems(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{})

	setup := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":        "execute",
		"executeStatement": `CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT, tier TEXT)`,
	})
	if _, err := executor.Execute(context.Background(), setup, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("create table error = %v", err)
	}

	insert := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":        "execute",
		"executeStatement": `INSERT INTO customers (name, tier) VALUES (?, ?)`,
		// Bound from the incoming item, never interpolated into the statement.
		"parameters": map[string]any{"mode": "expression", "value": `["{{ $json.name }}","{{ $json.tier }}"]`},
	})
	inserted, err := executor.Execute(context.Background(), insert, workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "Ada", "tier": "gold"}},
		{JSON: map[string]any{"name": "Grace", "tier": "silver"}},
	}}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("insert error = %v", err)
	}
	if len(inserted[0]) != 2 {
		t.Fatalf("insert produced %d items, want one per input item", len(inserted[0]))
	}

	query := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":  "query",
		"statement":  `SELECT name, tier FROM customers WHERE tier = ? ORDER BY name`,
		"parameters": `["gold"]`,
	})
	output, err := executor.Execute(context.Background(), query, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["name"] != "Ada" || output[0][0].JSON["tier"] != "gold" {
		t.Fatalf("query items = %#v, want the one gold customer", output[0])
	}
}

func TestDatabaseNodeRunsATransactionAtomically(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{})

	setup := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "execute", "executeStatement": `CREATE TABLE ledger (amount INTEGER)`,
	})
	if _, err := executor.Execute(context.Background(), setup, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("create table error = %v", err)
	}

	committed := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "transaction",
		"statements": `[{"sql":"INSERT INTO ledger (amount) VALUES (?)","parameters":[10]},
		                {"sql":"INSERT INTO ledger (amount) VALUES (?)","parameters":[20]}]`,
	})
	output, err := executor.Execute(context.Background(), committed, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("transaction error = %v", err)
	}
	if output[0][0].JSON["committed"] != true || output[0][0].JSON["rowsAffected"] != float64(2) {
		t.Fatalf("transaction item = %#v, want two committed rows", output[0][0].JSON)
	}

	failing := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "transaction",
		"statements": `[{"sql":"INSERT INTO ledger (amount) VALUES (?)","parameters":[30]},
		                {"sql":"INSERT INTO missing (amount) VALUES (?)","parameters":[40]}]`,
	})
	if _, err := executor.Execute(context.Background(), failing, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err == nil {
		t.Fatal("a failing transaction reported success")
	}

	check := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT COUNT(*) AS total FROM ledger`,
	})
	after, err := executor.Execute(context.Background(), check, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("count error = %v", err)
	}
	if after[0][0].JSON["total"] != int64(2) {
		t.Fatalf("rows after rollback = %#v, want the committed 2", after[0][0].JSON["total"])
	}
}

func TestDatabaseNodeCannotOpenTheInternalDatabase(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	internal := filepath.Join(directory, "kilasflow.db")
	if err := os.WriteFile(internal, []byte("internal"), 0o600); err != nil {
		t.Fatalf("write internal database: %v", err)
	}

	resolver := sqliteCredential(internal)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{InternalPaths: []string{internal}})
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT * FROM credentials`,
	})

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil {
		t.Fatal("a workflow opened KilasFlow's own database")
	}
	if !strings.Contains(err.Error(), "KilasFlow's own database") {
		t.Errorf("error = %v, want the guard's explanation", err)
	}
}

func TestDatabaseNodeRejectsACredentialOfTheWrongType(t *testing.T) {
	t.Parallel()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Wrong", Type: "postgres",
		Fields: map[string]string{"host": "localhost"},
	}}
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{})
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT 1`,
	})

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil || !strings.Contains(err.Error(), "not sqlite") {
		t.Fatalf("error = %v, want a credential type rejection", err)
	}
}

func TestDatabaseNodeReportsAFailingStatementWithoutLeakingTheCredential(t *testing.T) {
	t.Parallel()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Remote", Type: "postgres",
		Fields: map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
	}}
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverPostgres, "postgres", sqlnode.Guard{})
	ir := databaseNode(t, nodes.PostgresNodeType, "postgres", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT 1`,
	})

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil {
		t.Fatal("a connection to a closed port reported success")
	}
	// The driver echoes its DSN on failure, and that DSN carries the password
	// the credential store just decrypted.
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the node error leaked the password: %v", err)
	}
}

func TestDatabaseNodeSurfacesTruncationRatherThanHidingIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{})

	setup := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "execute", "executeStatement": `CREATE TABLE numbers (n INTEGER)`,
	})
	if _, err := executor.Execute(context.Background(), setup, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("create table error = %v", err)
	}
	for index := range 5 {
		insert := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
			"operation": "execute", "executeStatement": `INSERT INTO numbers (n) VALUES (?)`,
			"parameters": mustJSON(t, []any{index}),
		})
		if _, err := executor.Execute(context.Background(), insert, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
			t.Fatalf("insert error = %v", err)
		}
	}

	query := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT n FROM numbers`, "maxRows": float64(2),
	})
	output, err := executor.Execute(context.Background(), query, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	// A partial read that looked complete would quietly corrupt downstream work.
	last := output[0][len(output[0])-1]
	if last.JSON["$truncated"] != true {
		t.Fatalf("last item = %#v, want a truncation marker", last.JSON)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal = %v", err)
	}
	return string(encoded)
}

// sqlGuard is the empty guard used by tests that do not exercise the
// internal-database protection.
func sqlGuard() sqlnode.Guard { return sqlnode.Guard{} }

// runExecutor looks up a registered executor and runs it, so a test exercises
// the same binding the engine would.
func runExecutor(t *testing.T, registry *engine.Registry, executorID string, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	t.Helper()
	executor, found := registry.Lookup(executorID)
	if !found {
		t.Fatalf("executor %q is not registered", executorID)
	}
	return executor.Execute(context.Background(), ir, input, request)
}
