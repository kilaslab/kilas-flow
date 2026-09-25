package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
		for _, required := range []string{"operation", "statement", "parameters", "statementTimeoutSeconds", "maxRows"} {
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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())

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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())

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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", sqlnode.Guard{InternalPaths: []string{internal}, SQLite: sqlnode.SQLiteFiles{Unconfined: true}}, sqlnode.DefaultCeiling())
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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverPostgres, "postgres", sqlnode.Guard{}, sqlnode.DefaultCeiling())
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
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())

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

// unconfinedSQLite reads a SQLite credential's path as written, so the tests
// about statements can keep their files in a temp directory. Confinement has
// its own tests, here and in internal/sqlnode.
func unconfinedSQLite() sqlnode.Guard {
	return sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Unconfined: true}}
}

// The executor narrows the process guard to the run's tenant, so a relative
// SQLite path lands in that tenant's directory and a second tenant naming the
// same path reaches a different file.
func TestADatabaseNodeOpensSQLiteInsideTheRunsTenantDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite",
		sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}, sqlnode.DefaultCeiling())
	resolver := sqliteCredential("orders.db")
	create := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "execute", "executeStatement": `CREATE TABLE orders (id INTEGER)`,
	})

	for _, tenant := range []string{"acme", "globex"} {
		request := engine.Request{Credentials: resolver, Execution: engine.ExecutionContext{TenantID: tenant}}
		if _, err := executor.Execute(context.Background(), create, workflow.NodeInput{}, request); err != nil {
			t.Fatalf("%s: create table error = %v", tenant, err)
		}
		if _, err := os.Stat(filepath.Join(root, tenant, "orders.db")); err != nil {
			t.Fatalf("%s's database is not in its own directory: %v", tenant, err)
		}
	}

	escape := sqliteCredential("../acme/orders.db")
	request := engine.Request{Credentials: escape, Execution: engine.ExecutionContext{TenantID: "globex"}}
	if _, err := executor.Execute(context.Background(), create, workflow.NodeInput{}, request); !errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("a path into another tenant's directory = %v, want ErrForbiddenTarget", err)
	}
}

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

// createTable runs one DDL statement through the node, so a test's setup uses
// the same path as the behaviour it is about to check.
func createTable(t *testing.T, executor *nodes.DatabaseExecutor, resolver *stubCredentials, statement string) {
	t.Helper()
	setup := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "execute", "executeStatement": statement,
	})
	if _, err := executor.Execute(context.Background(), setup, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("create table error = %v", err)
	}
}

func countRows(t *testing.T, executor *nodes.DatabaseExecutor, resolver *stubCredentials, table string) int64 {
	t.Helper()
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT COUNT(*) AS total FROM ` + table,
	})
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("count error = %v", err)
	}
	total, ok := output[0][0].JSON["total"].(int64)
	if !ok {
		t.Fatalf("count = %#v, want an integer", output[0][0].JSON["total"])
	}
	return total
}

func TestABulkExecuteIsOneAtomicBatchAndKeepsItsPerItemOutput(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
	createTable(t, executor, resolver, `CREATE TABLE readings (id INTEGER PRIMARY KEY, value INTEGER)`)

	insert := func() workflow.IRNode {
		return databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
			"operation":        "execute",
			"executeStatement": `INSERT INTO readings (id, value) VALUES (?, ?)`,
			"parameters":       map[string]any{"mode": "expression", "value": `[{{ $json.id }},{{ $json.value }}]`},
		})
	}

	items := make([]workflow.Item, 0, 500)
	for index := range 500 {
		items = append(items, workflow.Item{JSON: map[string]any{"id": float64(index + 1), "value": float64(index)}})
	}

	output, err := executor.Execute(context.Background(), insert(), workflow.NodeInput{"main": items}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("bulk insert error = %v", err)
	}
	// The point of the batch is that it is invisible downstream: one
	// rowsAffected item per input item, in order, exactly as before.
	if len(output[0]) != 500 {
		t.Fatalf("bulk insert produced %d items, want one per input item", len(output[0]))
	}
	for index, item := range output[0] {
		if item.JSON["rowsAffected"] != float64(1) {
			t.Fatalf("item %d = %#v, want rowsAffected 1", index, item.JSON)
		}
	}
	if total := countRows(t, executor, resolver, "readings"); total != 500 {
		t.Fatalf("rows after the batch = %d, want 500", total)
	}

	// A middle item now collides with a primary key the batch itself inserted.
	// Before, the first half was applied and stayed applied.
	clash := make([]workflow.Item, 0, 500)
	for index := range 500 {
		id := float64(1000 + index)
		if index == 250 {
			id = 1
		}
		clash = append(clash, workflow.Item{JSON: map[string]any{"id": id, "value": float64(index)}})
	}
	_, err = executor.Execute(context.Background(), insert(), workflow.NodeInput{"main": clash}, engine.Request{Credentials: resolver})
	if err == nil {
		t.Fatal("a batch with a failing item reported success")
	}
	if !strings.Contains(err.Error(), "item 251") {
		t.Errorf("error = %v, want the failing item's number", err)
	}
	if total := countRows(t, executor, resolver, "readings"); total != 500 {
		t.Fatalf("rows after the rolled-back batch = %d, want the original 500", total)
	}
}

func TestABatchOfOneStatementRunsAsOneTransaction(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
	createTable(t, executor, resolver, `CREATE TABLE animals (name TEXT)`)

	// This test used to vary the statement text per item through an
	// expression, which is now refused — see the test below. What it exists to
	// prove is the batching: one prepared handle, one transaction, one output
	// item per input item. The values vary per item, which is what values are
	// for; the statement does not, which is what makes it safe.
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":        "execute",
		"executeStatement": `INSERT INTO animals (name) VALUES (?)`,
		"parameters":       map[string]any{"mode": "expression", "value": `["{{ $json.name }}"]`},
	})
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "ada"}},
		{JSON: map[string]any{"name": "quartz"}},
		{JSON: map[string]any{"name": "grace"}},
	}}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("batch error = %v", err)
	}
	if len(output[0]) != 3 {
		t.Fatalf("batch produced %d items, want one per input item", len(output[0]))
	}
	if total := countRows(t, executor, resolver, "animals"); total != 3 {
		t.Errorf("animals = %d, want 3", total)
	}
}

// SQL text may not be built from an expression, and the rule is enforced at
// save and again before anything runs.
//
// The panel has always said so. Until this was written it was advice: the
// executor resolved every parameter including the statement and handed the
// result to the driver as text, so a value arriving from a webhook body became
// SQL. The exploit was run before the fix — a body of `nobody' OR '1'='1`
// returned every row of a table instead of none — on both the version 1 node
// and the version 2 operation set.
func TestSQLBuiltFromAnExpressionIsRefusedAtSaveAndAtRun(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	marker := map[string]any{"mode": "expression", "value": `SELECT * FROM t WHERE name = '{{ $json.body.name }}'`}

	t.Run("refused when the workflow is saved", func(t *testing.T) {
		for _, row := range []struct {
			nodeType string
			version  workflow.TypeVersion
			key      string
			extra    map[string]any
		}{
			{nodes.SQLiteNodeType, workflow.V(1), "statement", map[string]any{"operation": "query"}},
			{nodes.SQLiteNodeType, workflow.V(1), "executeStatement", map[string]any{"operation": "execute"}},
			{nodes.SQLiteNodeType, workflow.V(1), "statements", map[string]any{"operation": "transaction"}},
			{nodes.PostgresNodeType, nodes.PostgresV2Version, "query", map[string]any{"operation": "executeQuery"}},
			{nodes.MySQLNodeType, nodes.MySQLV2Version, "query", map[string]any{"operation": "executeQuery"}},
		} {
			t.Run(row.nodeType+"/"+row.key, func(t *testing.T) {
				definition, found := registry.Get(row.nodeType, row.version)
				if !found {
					t.Fatalf("%s version %s is not registered", row.nodeType, row.version)
				}
				parameters := map[string]any{}
				for key, value := range row.extra {
					parameters[key] = value
				}
				parameters[row.key] = marker
				err := definition.Validate(workflow.Node{Parameters: parameters})
				if err == nil {
					t.Fatalf("a statement built from an expression was accepted on %q", row.key)
				}
				if !strings.Contains(err.Error(), row.key) {
					t.Errorf("error = %v, want it to name the parameter %q", err, row.key)
				}
			})
		}
	})

	t.Run("refused again before anything runs", func(t *testing.T) {
		// Validate runs at save, and a document can reach an executor without
		// passing through it. The executor is where the injection would
		// actually happen, so it refuses too.
		path := filepath.Join(t.TempDir(), "workflow.db")
		resolver := sqliteCredential(path)
		executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
		createTable(t, executor, resolver, `CREATE TABLE t (name TEXT)`)

		ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
			"operation": "query", "statement": marker,
		})
		_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {
			{JSON: map[string]any{"body": map[string]any{"name": "nobody' OR '1'='1"}}},
		}}, engine.Request{Credentials: resolver})
		if err == nil {
			t.Fatal("the executor ran a statement built from an expression")
		}
		if !strings.Contains(err.Error(), "statement") {
			t.Errorf("error = %v, want it to name the parameter", err)
		}
	})
}

func TestATransactionStatementDeclaredReturningHandsItsRowsOn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), sqlnode.DefaultCeiling())
	createTable(t, executor, resolver, `CREATE TABLE orders (id INTEGER PRIMARY KEY, total INTEGER)`)

	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation": "transaction",
		"statements": `[{"sql":"INSERT INTO orders (total) VALUES (?) RETURNING id","parameters":[99],"returning":true},
		                {"sql":"UPDATE orders SET total = total + 1","parameters":[]}]`,
	})
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("transaction error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("transaction produced %#v, want the returned row and the summary", output[0])
	}
	if output[0][0].JSON["id"] != int64(1) {
		t.Errorf("returned row = %#v, want the generated id", output[0][0].JSON)
	}
	summary := output[0][1].JSON
	if summary["committed"] != true {
		t.Errorf("summary = %#v, want a committed transaction", summary)
	}
	// The trap: routing every statement through Query would leave the update's
	// count unreachable, so the summary would say two statements committed and
	// zero rows affected.
	if summary["rowsAffected"] != float64(2) {
		t.Errorf("summary rowsAffected = %#v, want the returned row plus the updated one", summary["rowsAffected"])
	}
}

func TestATransactionRefusesParametersItCouldOnlyIgnore(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Get(nodes.SQLiteNodeType, workflow.V(1))

	err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.SQLiteNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation":  "transaction",
			"statements": `[{"sql":"SELECT 1"}]`,
			"parameters": `["orphaned"]`,
		},
	})
	if err == nil {
		t.Fatal("a transaction accepted node-level parameters it would have ignored")
	}
	if !strings.Contains(err.Error(), "per statement") {
		t.Errorf("error = %v, want the per-statement field named", err)
	}

	// The editor stores this field as a string, but a document posted to the
	// API can hold a real JSON array, and a check that only looked at strings
	// would let exactly that shape through.
	err = definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.SQLiteNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation":  "transaction",
			"statements": `[{"sql":"SELECT 1"}]`,
			"parameters": []any{"orphaned"},
		},
	})
	if err == nil {
		t.Error("a transaction accepted an array-shaped parameters field")
	}

	// The same field on a query is how a query is meant to be written.
	if err := definition.Validate(workflow.Node{
		ID: "sql", Name: "SQL", Type: nodes.SQLiteNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "query", "statement": "SELECT 1", "parameters": `["gold"]`},
	}); err != nil {
		t.Errorf("Validate() on a query with parameters = %v, want accepted", err)
	}
}

func TestLimitsArrivingFromAnExpressionAreClampedAndSaidSo(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	// A deployment that allows two rows and one second.
	ceiling := sqlnode.Ceiling{MaxRows: 2, MaxTimeout: time.Second}
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), ceiling)
	createTable(t, executor, resolver, `CREATE TABLE numbers (n INTEGER)`)

	seed := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":        "execute",
		"executeStatement": `INSERT INTO numbers (n) VALUES (?)`,
		"parameters":       map[string]any{"mode": "expression", "value": `[{{ $json.n }}]`},
	})
	seedItems := make([]workflow.Item, 0, 5)
	for index := range 5 {
		seedItems = append(seedItems, workflow.Item{JSON: map[string]any{"n": float64(index)}})
	}
	if _, err := executor.Execute(context.Background(), seed, workflow.NodeInput{"main": seedItems}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("seed error = %v", err)
	}

	// Both limits arrive from the item, which is how a webhook body reaches
	// them: a whole-expression marker returns the looked-up value's own type,
	// so `maxRows` becomes whatever the caller sent.
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":               "query",
		"statement":               `SELECT n FROM numbers ORDER BY n`,
		"maxRows":                 map[string]any{"mode": "expression", "value": `{{ $json.maxRows }}`},
		"statementTimeoutSeconds": map[string]any{"mode": "expression", "value": `{{ $json.timeout }}`},
	})
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {
		{JSON: map[string]any{"maxRows": float64(500_000_000), "timeout": float64(3600)}},
	}}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("query error = %v", err)
	}

	rows := 0
	var clamped map[string]any
	truncated := false
	for _, item := range output[0] {
		switch {
		case item.JSON["$clamped"] == true:
			clamped = item.JSON
		case item.JSON["$truncated"] == true:
			truncated = true
		default:
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("read %d rows, want the ceiling's 2", rows)
	}
	if !truncated {
		t.Error("a truncated read did not say so")
	}
	if clamped == nil {
		t.Fatal("the run did not report that its limits were lowered")
	}
	if clamped["maxRows"] != float64(2) || clamped["timeoutSeconds"] != float64(1) {
		t.Errorf("clamp notice = %#v, want both limits at the ceiling", clamped)
	}
}

func TestATimeoutSavedUnderTheOldKeyStillApplies(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workflow.db")
	resolver := sqliteCredential(path)
	ceiling := sqlnode.Ceiling{MaxRows: 10, MaxTimeout: time.Hour}
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", unconfinedSQLite(), ceiling)
	createTable(t, executor, resolver, `CREATE TABLE numbers (n INTEGER)`)

	// A workflow saved before the rename carries the parameter under the name
	// it then collided with. It has to keep meaning what its author meant.
	ir := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":      "query",
		"statement":      `SELECT n FROM numbers`,
		"timeoutSeconds": float64(45),
	})
	if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("legacy timeout error = %v", err)
	}

	// A timeout no database could meet fails the statement, which is how the
	// value is observed at all: a clean run proves only that nothing crashed.
	tiny := databaseNode(t, nodes.SQLiteNodeType, "sqlite", "cred-db", map[string]any{
		"operation":      "query",
		"statement":      `SELECT n FROM numbers`,
		"timeoutSeconds": 1e-9,
	})
	if _, err := executor.Execute(context.Background(), tiny, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err == nil {
		t.Fatal("a nanosecond statement timeout stored under the old key was ignored")
	}
}

// liveDrivers are the two servers the batching and returning-rows changes can
// behave differently on, each gated on its own DSN.
//
// SQLite proves the logic; a real server is where it can actually be wrong.
// RETURNING is PostgreSQL's own idiom rather than a SQLite convenience, a
// prepared statement carries a server-side plan, MySQL treats DDL as an
// implicit commit inside a transaction, and the two number their placeholders
// differently — so the statements are built per driver rather than shared.
var liveDrivers = map[string]struct {
	driver     sqlnode.Driver
	nodeType   string
	credential string
	env        string
	// bind renders the nth placeholder, counting from one.
	bind func(int) string
	// returning is empty where the server has no RETURNING clause.
	returning bool
}{
	"postgres": {
		driver: sqlnode.DriverPostgres, nodeType: nodes.PostgresNodeType, credential: "postgres",
		env: "KILASFLOW_TEST_POSTGRES_DSN", bind: func(n int) string { return "$" + strconv.Itoa(n) },
		returning: true,
	},
	"mysql": {
		driver: sqlnode.DriverMySQL, nodeType: nodes.MySQLNodeType, credential: "mysql",
		env: "KILASFLOW_TEST_MYSQL_DSN", bind: func(int) string { return "?" },
		// MySQL has no RETURNING, so a transaction there is checked for the
		// half that applies: it still commits and still counts its rows.
		returning: false,
	},
}

// liveCredential turns an integration DSN into the credential fields a database
// node actually takes.
//
// A node never sees a DSN — it is assembled from credential fields, which is
// what keeps a workflow from naming a connection string of its own. The gate
// hands over a DSN because that is what the rest of the repository's
// integration coverage uses, so it is taken apart here.
func liveCredential(t *testing.T, env, credentialType string) *stubCredentials {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run the %s half of this coverage", env, credentialType)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", env, err)
	}
	password, _ := parsed.User.Password()
	sslMode := parsed.Query().Get("sslmode")
	if sslMode == "" {
		sslMode = "disable"
	}
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Integration", Type: credentialType,
		Fields: map[string]string{
			"host": parsed.Hostname(), "port": parsed.Port(),
			"database": strings.TrimPrefix(parsed.Path, "/"),
			"user":     parsed.User.Username(), "password": password,
			"sslMode": sslMode,
		},
	}}
}

func TestALiveServerBatchesAtomicallyAndReturnsRows(t *testing.T) {
	for name, live := range liveDrivers {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			// The guard admits exactly the endpoint under test, as written:
			// the default-deny policy refuses loopback test databases.
			endpoint := resolver.credential.Fields["host"] + ":" + resolver.credential.Fields["port"]
			guard := sqlnode.Guard{Policy: safehttp.Policy{AllowedPrivateEndpoints: []string{endpoint}}}
			executor := nodes.NewDatabaseExecutor(live.driver, live.credential, guard, sqlnode.DefaultCeiling())

			run := func(t *testing.T, parameters map[string]any, items []workflow.Item) (workflow.NodeOutput, error) {
				t.Helper()
				ir := databaseNode(t, live.nodeType, live.credential, "cred-db", parameters)
				return executor.Execute(context.Background(), ir, workflow.NodeInput{"main": items}, engine.Request{Credentials: resolver})
			}
			ddl := func(t *testing.T, statement string) {
				t.Helper()
				// DDL goes through the same execute path, which now prepares
				// inside a transaction — the case MySQL treats as an implicit
				// commit, so it is worth running rather than reasoning about.
				if _, err := run(t, map[string]any{"operation": "execute", "executeStatement": statement}, nil); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}

			ddl(t, `DROP TABLE IF EXISTS batch_readings`)
			ddl(t, `CREATE TABLE batch_readings (id INTEGER PRIMARY KEY, value INTEGER)`)
			t.Cleanup(func() { ddl(t, `DROP TABLE IF EXISTS batch_readings`) })

			insert := map[string]any{
				"operation":        "execute",
				"executeStatement": `INSERT INTO batch_readings (id, value) VALUES (` + live.bind(1) + `, ` + live.bind(2) + `)`,
				"parameters":       map[string]any{"mode": "expression", "value": `[{{ $json.id }},{{ $json.value }}]`},
			}
			items := make([]workflow.Item, 0, 500)
			for index := range 500 {
				items = append(items, workflow.Item{JSON: map[string]any{"id": float64(index + 1), "value": float64(index)}})
			}

			output, err := run(t, insert, items)
			if err != nil {
				t.Fatalf("bulk insert error = %v", err)
			}
			if len(output[0]) != 500 {
				t.Fatalf("bulk insert produced %d items, want one per input item", len(output[0]))
			}

			count := func(t *testing.T) int64 {
				t.Helper()
				got, err := run(t, map[string]any{"operation": "query", "statement": `SELECT COUNT(*) AS total FROM batch_readings`}, nil)
				if err != nil {
					t.Fatalf("count error = %v", err)
				}
				total, ok := got[0][0].JSON["total"].(int64)
				if !ok {
					t.Fatalf("count = %#v, want an integer", got[0][0].JSON["total"])
				}
				return total
			}
			if total := count(t); total != 500 {
				t.Fatalf("rows after the batch = %d, want 500", total)
			}

			clash := make([]workflow.Item, 0, 500)
			for index := range 500 {
				id := float64(1000 + index)
				if index == 250 {
					id = 1
				}
				clash = append(clash, workflow.Item{JSON: map[string]any{"id": id, "value": float64(index)}})
			}
			if _, err := run(t, insert, clash); err == nil {
				t.Fatal("a batch with a duplicate key reported success")
			}
			if total := count(t); total != 500 {
				t.Fatalf("rows after the rolled-back batch = %d, want the original 500", total)
			}

			statements := `[{"sql":"INSERT INTO batch_readings (id, value) VALUES (` + live.bind(1) + `, ` + live.bind(2) + `)","parameters":[9001,7]},
			                {"sql":"UPDATE batch_readings SET value = value + 1 WHERE id = ` + live.bind(1) + `","parameters":[9001]}]`
			wantItems := 1
			if live.returning {
				statements = `[{"sql":"INSERT INTO batch_readings (id, value) VALUES (` + live.bind(1) + `, ` + live.bind(2) + `) RETURNING id","parameters":[9001,7],"returning":true},
				               {"sql":"UPDATE batch_readings SET value = value + 1 WHERE id = ` + live.bind(1) + `","parameters":[9001]}]`
				wantItems = 2
			}
			returned, err := run(t, map[string]any{"operation": "transaction", "statements": statements}, nil)
			if err != nil {
				t.Fatalf("transaction error = %v", err)
			}
			if len(returned[0]) != wantItems {
				t.Fatalf("transaction produced %#v, want %d items", returned[0], wantItems)
			}
			if live.returning && returned[0][0].JSON["id"] != int64(9001) {
				t.Errorf("returned row = %#v, want the inserted id", returned[0][0].JSON)
			}
			// The trap the returning route has to avoid: routing every
			// statement through Query leaves the update's count unreachable, so
			// the summary would say committed and zero rows affected.
			if summary := returned[0][wantItems-1].JSON; summary["committed"] != true || summary["rowsAffected"] != float64(2) {
				t.Errorf("summary = %#v, want two committed rows", summary)
			}
		})
	}
}

// A credential scoped to one host must not open another, on either database
// executor generation. The refusal names the credential and the host and
// carries ErrForbiddenTarget, never the DSN.
func TestDatabaseExecutorsEnforceTheCredentialDomainScope(t *testing.T) {
	t.Parallel()

	networkFields := map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	}
	v1 := func(driver sqlnode.Driver, credentialType string) *nodes.DatabaseExecutor {
		return nodes.NewDatabaseExecutor(driver, credentialType, sqlnode.Guard{}, sqlnode.DefaultCeiling())
	}
	for _, setup := range []struct {
		name           string
		nodeType       string
		credentialType string
		driver         sqlnode.Driver
	}{
		{"postgres v1", nodes.PostgresNodeType, "postgres", sqlnode.DriverPostgres},
		{"mysql v1", nodes.MySQLNodeType, "mysql", sqlnode.DriverMySQL},
	} {
		t.Run(setup.name, func(t *testing.T) {
			t.Parallel()

			resolver := &stubCredentials{credential: engine.Credential{
				ID: "cred-db", Name: "Partner", Type: setup.credentialType,
				Fields:         networkFields,
				AllowedDomains: []string{"db.partner.test"},
			}}
			executor := v1(setup.driver, setup.credentialType)
			ir := databaseNode(t, setup.nodeType, setup.credentialType, "cred-db", map[string]any{
				"operation": "query", "statement": `SELECT 1`,
			})
			_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
			assertScopeRefusal(t, err, "127.0.0.1")

			// Scoped to the target: the pre-flight passes and the refusal
			// underneath is the process policy's, which names the resolved
			// address rather than the credential scope.
			resolver.credential.AllowedDomains = []string{"127.0.0.1"}
			_, err = executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
			if err == nil {
				t.Fatal("a loopback connection was opened under the default-deny policy")
			}
			if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
				t.Fatalf("error = %v, want ErrForbiddenTarget", err)
			}
			if strings.Contains(err.Error(), "not allowed for host") {
				t.Errorf("error = %v, the credential scope passed but the node reported a scope refusal", err)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the refusal leaked the password: %v", err)
			}
		})
	}
}

func assertScopeRefusal(t *testing.T, err error, host string) {
	t.Helper()
	if err == nil {
		t.Fatal("an out-of-scope credential opened a connection")
	}
	if !errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("error = %v, want ErrForbiddenTarget", err)
	}
	if !strings.Contains(err.Error(), `not allowed for host "`+host+`"`) {
		t.Errorf("error = %v, want the host named", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal leaked the password: %v", err)
	}
}

// The version 2 operation sets resolve the same credential, so they carry the
// same pre-flight gate. The refusal lands before any statement is built.
func TestV2DatabaseExecutorsEnforceTheCredentialDomainScope(t *testing.T) {
	t.Parallel()

	for _, setup := range []struct {
		name           string
		nodeType       string
		credentialType string
		executor       *nodes.SQLOperationExecutor
	}{
		{"postgres v2", nodes.PostgresNodeType, "postgres", nodes.NewPostgresV2Executor(sqlnode.Guard{}, sqlnode.DefaultCeiling())},
		{"mysql v2", nodes.MySQLNodeType, "mysql", nodes.NewMySQLV2Executor(sqlnode.Guard{}, sqlnode.DefaultCeiling())},
	} {
		t.Run(setup.name, func(t *testing.T) {
			t.Parallel()

			resolver := &stubCredentials{credential: engine.Credential{
				ID: "cred-db", Name: "Partner", Type: setup.credentialType,
				Fields: map[string]string{
					"host": "127.0.0.1", "port": "1", "database": "app",
					"user": "ada", "password": "hunter2", "sslMode": "disable",
				},
				AllowedDomains: []string{"db.partner.test"},
			}}
			ir := databaseNode(t, setup.nodeType, setup.credentialType, "cred-db", map[string]any{})
			_, err := setup.executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
			assertScopeRefusal(t, err, "127.0.0.1")
		})
	}
}

// A MySQL dial failure echoes its DSN — password included — and the node's
// error must not. The policy permits the dial here, so the driver is the one
// that fails and its echo is what Sanitize has to strip.
func TestDatabaseNodeRedactsAMySQLPasswordOnConnectionFailure(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Remote", Type: "mysql",
		Fields: map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2",
		},
	}}
	executor := nodes.NewDatabaseExecutor(sqlnode.DriverMySQL, "mysql",
		sqlnode.Guard{Policy: policy}, sqlnode.DefaultCeiling())
	ir := databaseNode(t, nodes.MySQLNodeType, "mysql", "cred-db", map[string]any{
		"operation": "query", "statement": `SELECT 1`,
	})

	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil {
		t.Fatal("a connection to a closed port reported success")
	}
	if errors.Is(err, sqlnode.ErrForbiddenTarget) {
		t.Fatalf("error = %v, want a genuine dial failure rather than a policy refusal", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the node error leaked the password: %v", err)
	}
}
