package nodes_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/sqlbuild"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// liveV2 is one operation-set node under test against a real server.
//
// A live gate rather than the SQLite harness the older database tests use: the
// operation set builds dialect-specific SQL, and SQLite is neither dialect.
// Running these against SQLite would prove that a statement nobody ships
// behaves, which is worse than not running them.
var liveV2 = map[string]struct {
	nodeType   string
	credential string
	env        string
	dialect    sqlbuild.Dialect
	driver     sqlnode.Driver
	// serial is the column type for a self-assigning key.
	serial string
	// text is the widest string type both servers spell differently.
	text string
}{
	"postgres": {
		nodeType: nodes.PostgresNodeType, credential: "postgres", env: "KILASFLOW_TEST_POSTGRES_DSN",
		dialect: sqlbuild.Postgres, driver: sqlnode.DriverPostgres, serial: "SERIAL PRIMARY KEY", text: "TEXT",
	},
	"mysql": {
		nodeType: nodes.MySQLNodeType, credential: "mysql", env: "KILASFLOW_TEST_MYSQL_DSN",
		dialect: sqlbuild.MySQL, driver: sqlnode.DriverMySQL, serial: "INT AUTO_INCREMENT PRIMARY KEY", text: "VARCHAR(64)",
	},
}

// v2Node builds an operation-set node, which is version 2 rather than 1.
func v2Node(t *testing.T, nodeType, credentialType string, parameters map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodeType, workflow.V(2))
	if !found {
		t.Fatalf("node type %q version 2 is not registered", nodeType)
	}
	return workflow.IRNode{
		ID: "db-2", Name: "Database", Type: nodeType, TypeVersion: workflow.V(2),
		Parameters: parameters, Credentials: map[string]string{credentialType: "cred-db"},
		Definition: definition,
	}
}

// The three batching modes differ in exactly what they promise about a failure
// partway through, and each promise is read back from the table.
//
// Read back rather than inferred from the returned error, because the returned
// error is the same sentence in two of the three modes and the difference —
// whether the rows before the failure are still there — is the whole point.
func TestALiveServerHonoursEachBatchingMode(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			run := func(t *testing.T, parameters map[string]any, items []workflow.Item) (workflow.NodeOutput, error) {
				t.Helper()
				ir := v2Node(t, live.nodeType, live.credential, parameters)
				return executor.Execute(context.Background(), ir,
					workflow.NodeInput{"main": items}, engine.Request{Credentials: resolver})
			}
			query := func(t *testing.T, statement string) workflow.NodeOutput {
				t.Helper()
				output, err := run(t, map[string]any{
					"operation": "executeQuery", "query": statement,
				}, []workflow.Item{{JSON: map[string]any{}}})
				if err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
				return output
			}

			for _, mode := range []string{"transaction", "single", "independently"} {
				t.Run(mode, func(t *testing.T) {
					table := "kf_batch_" + mode
					query(t, "DROP TABLE IF EXISTS "+table)
					query(t, "CREATE TABLE "+table+" (id INT PRIMARY KEY, tier "+live.text+")")
					t.Cleanup(func() { query(t, "DROP TABLE IF EXISTS "+table) })

					// The second item repeats the first item's key, so the
					// database refuses it. Two items rather than three because
					// the question is only what happens to the work before the
					// failure.
					insert := map[string]any{
						"operation":       "executeQuery",
						"query":           "INSERT INTO " + table + " (id, tier) VALUES (" + bindOne(live.dialect) + ", 'gold')",
						"queryParameters": map[string]any{"mode": "expression", "value": `[{{ $json.id }}]`},
						"options":         map[string]any{"queryBatching": mode},
					}
					items := []workflow.Item{
						{JSON: map[string]any{"id": float64(1)}},
						{JSON: map[string]any{"id": float64(1)}},
						{JSON: map[string]any{"id": float64(2)}},
					}
					output, err := run(t, insert, items)

					switch mode {
					case "independently":
						if err != nil {
							t.Fatalf("independently returned an error rather than reporting the item: %v", err)
						}
						if len(output[0]) != 3 {
							t.Fatalf("independently produced %d items, want one per input item", len(output[0]))
						}
						descriptor, isError := output[0][1].JSON[engine.ErrorItemKey].(map[string]any)
						if !isError {
							t.Fatalf("item 2 = %#v, want the failure in the failing item's place", output[0][1].JSON)
						}
						if descriptor["item"] != float64(2) {
							t.Errorf("the failure names item %v, want 2", descriptor["item"])
						}
						if rows := countIn(t, query, table); rows != 2 {
							t.Errorf("%s holds %d rows, want the two that did not conflict", table, rows)
						}
					case "single":
						if err == nil {
							t.Fatal("single returned no error for a failing item")
						}
						// No transaction, so the first item is committed and
						// staying committed is the promise the mode makes.
						if rows := countIn(t, query, table); rows != 1 {
							t.Errorf("%s holds %d rows, want the one committed before the failure", table, rows)
						}
					case "transaction":
						if err == nil {
							t.Fatal("transaction returned no error for a failing item")
						}
						if rows := countIn(t, query, table); rows != 0 {
							t.Errorf("%s holds %d rows after a rollback, want none", table, rows)
						}
					}
				})
			}
		})
	}
}

// An item whose own parameters never build is still that item's failure under
// independently, not the run's.
//
// The mode is read from the unresolved parameters for exactly this: taking it
// from the first resolved item would make it unknowable in the one case where
// the first item is the one that does not resolve.
func TestUnderIndependentlyAnItemThatNeverBuildsIsStillJustThatItem(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			// The first item's query parameters are not a JSON array, so that
			// item never reaches the database at all. The second item's are.
			ir := v2Node(t, live.nodeType, live.credential, map[string]any{
				"operation": "executeQuery",
				// Cast where the dialect has one: PostgreSQL cannot infer a
				// bound parameter's type in a bare select list, and that would
				// fail the item that is supposed to succeed.
				"query":           "SELECT " + bindOne(live.dialect) + castTo(live.dialect) + " AS answer",
				"queryParameters": map[string]any{"mode": "expression", "value": `{{ $json.bound }}`},
				"options":         map[string]any{"queryBatching": "independently"},
			})
			output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {
				{JSON: map[string]any{"bound": "not an array"}},
				{JSON: map[string]any{"bound": `["ok"]`}},
			}}, engine.Request{Credentials: resolver})
			if err != nil {
				t.Fatalf("the run failed rather than the item: %v", err)
			}
			if len(output[0]) != 2 {
				t.Fatalf("produced %d items, want one per input item", len(output[0]))
			}
			descriptor, isError := output[0][0].JSON[engine.ErrorItemKey].(map[string]any)
			if !isError {
				t.Fatalf("item 1 = %#v, want the build failure in its place", output[0][0].JSON)
			}
			if descriptor["item"] != float64(1) {
				t.Errorf("the failure names item %v, want 1", descriptor["item"])
			}
			if _, stillFailed := output[0][1].JSON[engine.ErrorItemKey]; stillFailed {
				t.Errorf("item 2 = %#v, want the item that did build to have run", output[0][1].JSON)
			}
		})
	}
}

// The same promise, one stage earlier: an item whose expressions do not even
// resolve.
//
// A separate test from the build-failure one because the two fail at different
// stages and only one of them leaves the run's own settings unread. Resolving
// is what produces the parameters the connect timeout and the row shaping are
// read from, so an unresolvable first item used to leave those at zero — and a
// zero connect timeout is a deadline already past, which ended the whole run at
// the connection instead of at the item.
func TestUnderIndependentlyAnItemThatNeverResolvesIsStillJustThatItem(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			// The first item's JSON has no `bound` object to reach into, so
			// its expression does not resolve. The second item's does.
			ir := v2Node(t, live.nodeType, live.credential, map[string]any{
				"operation": "executeQuery",
				"query": map[string]any{
					"mode":  "expression",
					"value": `SELECT '{{ $json.bound.inner }}'` + castTo(live.dialect) + ` AS answer`,
				},
				"options": map[string]any{"queryBatching": "independently"},
			})
			output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {
				{JSON: map[string]any{"bound": "not an object"}},
				{JSON: map[string]any{"bound": map[string]any{"inner": "ok"}}},
			}}, engine.Request{Credentials: resolver})
			if err != nil {
				t.Fatalf("the run failed rather than the item: %v", err)
			}
			if len(output[0]) != 2 {
				t.Fatalf("produced %d items, want one per input item", len(output[0]))
			}
			if _, isError := output[0][0].JSON[engine.ErrorItemKey]; !isError {
				t.Fatalf("item 1 = %#v, want the resolve failure in its place", output[0][0].JSON)
			}
			if answer := output[0][1].JSON["answer"]; answer != "ok" {
				t.Errorf("item 2 answer = %#v, want the item that did resolve to have run", answer)
			}
		})
	}
}

// largeNumbersOutput changes the Go type of the value in the item, not its
// spelling.
//
// Asserted on the type because the trap the ticket names is real: normalize
// already turns every []byte a driver returns into a string, so a test that
// compared rendered values would pass on both settings while one of them did
// nothing at all.
func TestALiveServerRendersLargeNumbersAsTheOptionAsks(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			run := func(t *testing.T, parameters map[string]any) workflow.NodeOutput {
				t.Helper()
				ir := v2Node(t, live.nodeType, live.credential, parameters)
				output, err := executor.Execute(context.Background(), ir,
					workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
				if err != nil {
					t.Fatalf("execute: %v", err)
				}
				return output
			}
			ddl := func(statement string) {
				run(t, map[string]any{"operation": "executeQuery", "query": statement})
			}

			ddl("DROP TABLE IF EXISTS kf_numbers")
			ddl("CREATE TABLE kf_numbers (id INT PRIMARY KEY, big BIGINT, exact DECIMAL(30,2))")
			t.Cleanup(func() { ddl("DROP TABLE IF EXISTS kf_numbers") })
			// Past 2^53, where a float64 stops holding every digit — which is
			// the entire reason the option exists.
			ddl("INSERT INTO kf_numbers (id, big, exact) VALUES (1, 9007199254740993, 12345678901234567890.12)")

			for _, rendering := range []string{"text", "numbers"} {
				t.Run(rendering, func(t *testing.T) {
					output := run(t, map[string]any{
						"operation": "select", "table": "kf_numbers", "schema": defaultSchema(live.dialect),
						"returnAll": true,
						"options":   map[string]any{"largeNumbersOutput": rendering},
					})
					if len(output[0]) != 1 {
						t.Fatalf("select returned %d items, want one", len(output[0]))
					}
					row := output[0][0].JSON
					for _, column := range []string{"big", "exact"} {
						switch rendering {
						case "text":
							text, isText := row[column].(string)
							if !isText {
								t.Errorf("%s = %#v (%T), want a string", column, row[column], row[column])
								continue
							}
							if strings.ContainsAny(text, "eE+") {
								t.Errorf("%s = %q, want plain digits rather than an exponent", column, text)
							}
						case "numbers":
							if _, isNumber := row[column].(float64); !isNumber {
								t.Errorf("%s = %#v (%T), want a number", column, row[column], row[column])
							}
						}
					}
					if rendering == "text" && row["big"] != "9007199254740993" {
						t.Errorf("big = %#v, want every digit kept", row["big"])
					}
				})
			}
		})
	}
}

// replaceEmptyStrings writes a NULL where an empty string was, and leaves a
// non-empty one alone.
func TestALiveServerReplacesOnlyTheEmptyStrings(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			run := func(t *testing.T, parameters map[string]any, items []workflow.Item) workflow.NodeOutput {
				t.Helper()
				ir := v2Node(t, live.nodeType, live.credential, parameters)
				output, err := executor.Execute(context.Background(), ir,
					workflow.NodeInput{"main": items}, engine.Request{Credentials: resolver})
				if err != nil {
					t.Fatalf("execute: %v", err)
				}
				return output
			}
			ddl := func(statement string) {
				run(t, map[string]any{"operation": "executeQuery", "query": statement},
					[]workflow.Item{{JSON: map[string]any{}}})
			}

			ddl("DROP TABLE IF EXISTS kf_blanks")
			ddl("CREATE TABLE kf_blanks (id INT PRIMARY KEY, note " + live.text + ")")
			t.Cleanup(func() { ddl("DROP TABLE IF EXISTS kf_blanks") })

			for _, replacing := range []bool{false, true} {
				name := "kept"
				if replacing {
					name = "replaced"
				}
				t.Run(name, func(t *testing.T) {
					ddl("DELETE FROM kf_blanks WHERE id > 0")
					run(t, map[string]any{
						"operation": "executeQuery",
						"query": "INSERT INTO kf_blanks (id, note) VALUES (" +
							bindOne(live.dialect) + ", " + bindTwo(live.dialect) + ")",
						"queryParameters": map[string]any{"mode": "expression", "value": `[{{ $json.id }}, "{{ $json.note }}"]`},
						"options":         map[string]any{"replaceEmptyStrings": replacing},
					}, []workflow.Item{
						{JSON: map[string]any{"id": float64(1), "note": ""}},
						{JSON: map[string]any{"id": float64(2), "note": "kept"}},
					})

					output := run(t, map[string]any{
						"operation": "executeQuery",
						"query":     "SELECT id, note FROM kf_blanks ORDER BY id",
					}, []workflow.Item{{JSON: map[string]any{}}})
					if len(output[0]) != 2 {
						t.Fatalf("read back %d rows, want two", len(output[0]))
					}
					blank := output[0][0].JSON["note"]
					if replacing && blank != nil {
						t.Errorf("the empty note came back as %#v, want a JSON null", blank)
					}
					if !replacing && blank != "" {
						t.Errorf("the empty note came back as %#v, want the empty string it was", blank)
					}
					// The half that a one-sided test would miss: the option
					// must not reach a string that had something in it.
					if kept := output[0][1].JSON["note"]; kept != "kept" {
						t.Errorf("the non-empty note came back as %#v, want %q", kept, "kept")
					}
				})
			}
		})
	}
}

// outputColumns keeps only the named columns, and skipOnConflict passes over a
// duplicate rather than failing the node.
func TestALiveServerAppliesTheRemainingRowOptions(t *testing.T) {
	for name, live := range liveV2 {
		t.Run(name, func(t *testing.T) {
			resolver := liveCredential(t, live.env, live.credential)
			executor := newV2Executor(t, live.credential)

			run := func(t *testing.T, parameters map[string]any) (workflow.NodeOutput, error) {
				t.Helper()
				ir := v2Node(t, live.nodeType, live.credential, parameters)
				return executor.Execute(context.Background(), ir,
					workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
			}
			ddl := func(statement string) {
				if _, err := run(t, map[string]any{"operation": "executeQuery", "query": statement}); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}

			ddl("DROP TABLE IF EXISTS kf_rowopts")
			ddl("CREATE TABLE kf_rowopts (id INT PRIMARY KEY, tier " + live.text + ", note " + live.text + ")")
			t.Cleanup(func() { ddl("DROP TABLE IF EXISTS kf_rowopts") })
			ddl("INSERT INTO kf_rowopts (id, tier, note) VALUES (1, 'gold', 'first')")

			t.Run("outputColumns", func(t *testing.T) {
				output, err := run(t, map[string]any{
					"operation": "select", "table": "kf_rowopts", "schema": defaultSchema(live.dialect),
					"returnAll": true,
					"options":   map[string]any{"outputColumns": []any{"tier"}},
				})
				if err != nil {
					t.Fatalf("select: %v", err)
				}
				row := output[0][0].JSON
				if len(row) != 1 || row["tier"] != "gold" {
					t.Fatalf("row = %#v, want only the tier column", row)
				}
			})

			t.Run("cascade", func(t *testing.T) {
				if !live.dialect.DropsCascade() {
					t.Skip("this dialect parses CASCADE and ignores it, so the node does not offer it")
				}
				ddl("CREATE TABLE IF NOT EXISTS kf_cascade (id INT PRIMARY KEY)")
				ddl("CREATE VIEW kf_cascade_view AS SELECT id FROM kf_cascade")
				t.Cleanup(func() {
					ddl("DROP VIEW IF EXISTS kf_cascade_view")
					ddl("DROP TABLE IF EXISTS kf_cascade")
				})

				drop := map[string]any{
					"operation": "deleteTable", "table": "kf_cascade", "schema": defaultSchema(live.dialect),
					"deleteCommand": sqlbuild.DeleteDrop,
				}
				// Without it the server refuses, which is what makes the
				// cascading run below mean something.
				if _, err := run(t, drop); err == nil {
					t.Fatal("a table with a dependent view dropped without cascade; the rest of this test proves nothing")
				}
				drop["options"] = map[string]any{"cascade": true}
				if _, err := run(t, drop); err != nil {
					t.Fatalf("cascade did not take the dependent view with it: %v", err)
				}
			})

			t.Run("skipOnConflict", func(t *testing.T) {
				// Through executeQuery's sibling path would prove nothing:
				// skipOnConflict rewrites the insert the builder emits, so the
				// insert operation is the only place it can be seen.
				conflicting := map[string]any{
					"operation": "insert", "table": "kf_rowopts", "schema": defaultSchema(live.dialect),
					"columns": map[string]any{
						"mappingMode": "defineBelow",
						"value":       map[string]any{"id": float64(1), "tier": "silver"},
						"schema": []any{
							map[string]any{"id": "id", "displayName": "id", "type": "number", "canBeUsedToMatch": true},
							map[string]any{"id": "tier", "displayName": "tier", "type": "string"},
						},
					},
				}
				if _, err := run(t, conflicting); err == nil {
					t.Fatal("a duplicate key inserted without complaint; the rest of this test proves nothing")
				}
				conflicting["options"] = map[string]any{"skipOnConflict": true}
				if _, err := run(t, conflicting); err != nil {
					t.Fatalf("skipOnConflict still failed on the duplicate: %v", err)
				}
				output, err := run(t, map[string]any{
					"operation": "executeQuery", "query": "SELECT tier FROM kf_rowopts WHERE id = 1",
				})
				if err != nil {
					t.Fatalf("read back: %v", err)
				}
				if tier := output[0][0].JSON["tier"]; tier != "gold" {
					t.Errorf("tier = %#v, want the row skipped rather than overwritten", tier)
				}
			})
		})
	}
}

func newV2Executor(t *testing.T, credentialType string) *nodes.SQLOperationExecutor {
	t.Helper()
	if credentialType == "postgres" {
		return nodes.NewPostgresV2Executor(sqlnode.Guard{}, sqlnode.DefaultCeiling())
	}
	return nodes.NewMySQLV2Executor(sqlnode.Guard{}, sqlnode.DefaultCeiling())
}

func bindOne(dialect sqlbuild.Dialect) string {
	if dialect.Name() == "postgres" {
		return "$1"
	}
	return "?"
}

// castTo names a bound parameter's type, where the dialect needs it named.
func castTo(dialect sqlbuild.Dialect) string {
	if dialect.Name() == "postgres" {
		return "::text"
	}
	return ""
}

func bindTwo(dialect sqlbuild.Dialect) string {
	if dialect.Name() == "postgres" {
		return "$2"
	}
	return "?"
}

// defaultSchema is the schema locator a select needs, where the dialect has one.
func defaultSchema(dialect sqlbuild.Dialect) string {
	if dialect.Name() == "postgres" {
		return "public"
	}
	return ""
}

// countIn reads a table's row count through the node itself.
func countIn(t *testing.T, query func(*testing.T, string) workflow.NodeOutput, table string) int64 {
	t.Helper()
	output := query(t, "SELECT COUNT(*) AS total FROM "+table)
	if len(output[0]) != 1 {
		t.Fatalf("count returned %d items, want one", len(output[0]))
	}
	switch total := output[0][0].JSON["total"].(type) {
	case int64:
		return total
	case float64:
		return int64(total)
	case string:
		// A count comes back as a bigint, which the default rendering spells
		// as text — the option under test in this very file.
		var parsed int64
		for _, digit := range total {
			parsed = parsed*10 + int64(digit-'0')
		}
		return parsed
	default:
		t.Fatalf("count = %#v (%T), want a number", output[0][0].JSON["total"], output[0][0].JSON["total"])
		return 0
	}
}

// connectionTimeout bounds reaching the server, not the statement.
//
// Pointed at 192.0.2.1 — RFC 5737 reserves it as unroutable, so nothing on any
// developer's network answers — with a one-second bound and an assertion that
// the failure arrives long before the driver's own default would have. It is
// an upper bound rather than an equality: a host whose network stack refuses
// the address outright fails immediately and the deadline never fires, which
// still satisfies what the option promises.
func TestAConnectionTimeoutBoundsReachingTheServer(t *testing.T) {
	t.Parallel()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-db", Name: "Unreachable", Type: "postgres",
		Fields: map[string]string{
			"host": "192.0.2.1", "port": "5432", "database": "kilasflow",
			"user": "kilas", "password": "hunter2", "sslMode": "disable",
		},
	}}
	executor := nodes.NewPostgresV2Executor(sqlnode.Guard{}, sqlnode.DefaultCeiling())
	ir := v2Node(t, nodes.PostgresNodeType, "postgres", map[string]any{
		"operation": "executeQuery", "query": "SELECT 1",
		"options": map[string]any{"connectionTimeout": float64(1)},
	})

	started := time.Now()
	_, err := executor.Execute(context.Background(), ir,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{Credentials: resolver})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("connecting to an unroutable address succeeded")
	}
	if elapsed > 10*time.Second {
		t.Errorf("the connect took %s with a one-second bound, so the option is not reaching Open", elapsed)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the connection error carries the password: %v", err)
	}
}
