package sqlbuild_test

import (
	"context"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlbuild"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden statements from this run")

// golden compares one built statement against its committed file.
//
// A golden rather than an assertion, because what matters is the whole
// statement byte for byte: an assertion that the SQL "contains INSERT INTO"
// passes for a statement with the wrong placeholder numbering, the wrong
// conflict target, or a missing RETURNING.
//
// The vendored n8n checkout does not carry packages/nodes-base/nodes/Postgres,
// so these pin *this* implementation's SQL rather than claiming byte equality
// with n8n's. What they buy is the same thing either way: a change to any
// builder shows up as a diff somebody has to look at.
func golden(t *testing.T, dialect sqlbuild.Dialect, name string, statement sqlnode.Statement, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("build error = %v", err)
	}
	rendered := statement.SQL + "\n"
	if len(statement.Parameters) > 0 {
		encoded, marshalErr := json.Marshal(statement.Parameters)
		if marshalErr != nil {
			t.Fatalf("marshal parameters: %v", marshalErr)
		}
		rendered += "-- parameters: " + string(encoded) + "\n"
	}
	if statement.Returning {
		rendered += "-- returning\n"
	}

	path := filepath.Join("testdata", dialect.Name(), name+".sql")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden directory: %v", err)
		}
	}
	if *updateGolden {
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-golden): %v", err)
	}
	if string(want) != rendered {
		t.Errorf("statement changed.\n got: %s\nwant: %s", rendered, want)
	}
}

var customers = sqlbuild.Target{Schema: "public", Table: "customers"}

// dialects is every spelling the builders support.
//
// The same configuration rendered into both directories side by side is the
// point: a dialect difference is a diff in one of them, and a change to the
// shape they share is a diff in both — which is the distinction a reviewer
// needs and two separate packages would hide.
var dialects = []sqlbuild.Dialect{sqlbuild.Postgres, sqlbuild.MySQL}

func TestEachOperationBuildsTheStatementItPromises(t *testing.T) {
	values := map[string]any{"email": "ada@example.test", "tier": "gold", "id": float64(7)}

	for _, dialect := range dialects {
		t.Run(dialect.Name(), func(t *testing.T) {
			t.Run("select", func(t *testing.T) {
				statement, err := sqlbuild.Select(dialect, customers, nil, []sqlbuild.Comparison{
					{Column: "tier", Operator: "equals", Value: "gold"},
					{Column: "email", Operator: "like", Value: "%@example.test"},
				}, "AND", []sqlbuild.Order{{Column: "email"}}, 50)
				golden(t, dialect, "select", statement, err)
			})

			t.Run("select all", func(t *testing.T) {
				statement, err := sqlbuild.Select(dialect, customers, []string{"id", "email"}, nil, "", nil, 0)
				golden(t, dialect, "select_all", statement, err)
			})

			t.Run("select with a null test", func(t *testing.T) {
				// A null test binds nothing, so it must not consume a
				// placeholder — the numbering bug a "contains" assertion
				// misses, and the one that differs least visibly between a
				// numbered dialect and an unnumbered one.
				statement, err := sqlbuild.Select(dialect, customers, nil, []sqlbuild.Comparison{
					{Column: "tier", Operator: "isNull"},
					{Column: "email", Operator: "equals", Value: "ada@example.test"},
				}, "OR", nil, 0)
				golden(t, dialect, "select_null", statement, err)
			})

			t.Run("select case-insensitively", func(t *testing.T) {
				// PostgreSQL has ILIKE; MySQL does not, and folds instead.
				statement, err := sqlbuild.Select(dialect, customers, nil, []sqlbuild.Comparison{
					{Column: "email", Operator: "ilike", Value: "ADA@%"},
				}, "AND", nil, 0)
				golden(t, dialect, "select_ilike", statement, err)
			})

			t.Run("insert", func(t *testing.T) {
				statement, err := sqlbuild.Insert(dialect, customers, values, false)
				golden(t, dialect, "insert", statement, err)
			})

			t.Run("update", func(t *testing.T) {
				statement, err := sqlbuild.Update(dialect, customers, values, []string{"id"})
				golden(t, dialect, "update", statement, err)
			})

			t.Run("upsert", func(t *testing.T) {
				statement, err := sqlbuild.Upsert(dialect, customers, values, []string{"id"})
				golden(t, dialect, "upsert", statement, err)
			})

			t.Run("upsert with nothing to change", func(t *testing.T) {
				statement, err := sqlbuild.Upsert(dialect, customers, map[string]any{"id": float64(7)}, []string{"id"})
				golden(t, dialect, "upsert_nothing", statement, err)
			})

			t.Run("delete rows", func(t *testing.T) {
				statement, err := sqlbuild.Delete(dialect, customers, sqlbuild.DeleteRows, []sqlbuild.Comparison{
					{Column: "tier", Operator: "equals", Value: "bronze"},
				}, "AND", sqlbuild.Removal{})
				golden(t, dialect, "delete_rows", statement, err)
			})

			t.Run("truncate", func(t *testing.T) {
				statement, err := sqlbuild.Delete(dialect, customers, sqlbuild.DeleteTruncate, nil, "", sqlbuild.Removal{})
				golden(t, dialect, "delete_truncate", statement, err)
			})

			t.Run("drop", func(t *testing.T) {
				statement, err := sqlbuild.Delete(dialect, customers, sqlbuild.DeleteDrop, nil, "", sqlbuild.Removal{})
				golden(t, dialect, "delete_drop", statement, err)
			})

			t.Run("drop cascading", func(t *testing.T) {
				// The MySQL golden is deliberately the same statement as the
				// plain drop. MySQL parses CASCADE and documents that it does
				// nothing, so emitting it would put a promise in the SQL that
				// the server does not keep.
				statement, err := sqlbuild.Delete(dialect, customers, sqlbuild.DeleteDrop, nil, "",
					sqlbuild.Removal{Cascade: true})
				golden(t, dialect, "delete_drop_cascade", statement, err)
			})

			t.Run("truncate restarting sequences", func(t *testing.T) {
				// The MySQL golden is the same statement as the plain
				// truncate, and for the opposite reason to the cascade case:
				// MySQL always resets AUTO_INCREMENT and has no keyword for
				// asking, so the option is in effect there rather than absent.
				statement, err := sqlbuild.Delete(dialect, customers, sqlbuild.DeleteTruncate, nil, "",
					sqlbuild.Removal{RestartSequences: true})
				golden(t, dialect, "delete_truncate_restart", statement, err)
			})

			t.Run("select in order", func(t *testing.T) {
				statement, err := sqlbuild.Select(dialect, customers, nil, nil, "", []sqlbuild.Order{
					{Column: "tier"},
					{Column: "id", Descending: true},
				}, 0)
				golden(t, dialect, "select_ordered", statement, err)
			})

			t.Run("insert skipping conflicts", func(t *testing.T) {
				statement, err := sqlbuild.Insert(dialect, customers, values, true)
				golden(t, dialect, "insert_skip_conflict", statement, err)
			})
		})
	}
}

func TestTheMySQLDialectNeverWritesAPostgresShape(t *testing.T) {
	t.Parallel()

	// Three invariants a golden file states but does not enforce, asserted on
	// the SQL text rather than on a query result. A live single-database
	// fixture cannot see the qualifier one at all: `a`.`b` on MySQL names
	// database a's table b, and against a fixture where a *is* the database it
	// would run and pass.
	values := map[string]any{"email": "ada@example.test", "id": float64(7)}
	read, err := sqlbuild.Select(sqlbuild.MySQL, customers, nil, []sqlbuild.Comparison{
		{Column: "tier", Operator: "equals", Value: "gold"},
		{Column: "email", Operator: "like", Value: "a%"},
	}, "AND", nil, 10)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	writes := []sqlnode.Statement{}
	for _, build := range []func() (sqlnode.Statement, error){
		func() (sqlnode.Statement, error) { return sqlbuild.Insert(sqlbuild.MySQL, customers, values, false) },
		func() (sqlnode.Statement, error) {
			return sqlbuild.Update(sqlbuild.MySQL, customers, values, []string{"id"})
		},
		func() (sqlnode.Statement, error) {
			return sqlbuild.Upsert(sqlbuild.MySQL, customers, values, []string{"id"})
		},
	} {
		statement, err := build()
		if err != nil {
			t.Fatalf("build error = %v", err)
		}
		writes = append(writes, statement)
	}

	for _, statement := range append([]sqlnode.Statement{read}, writes...) {
		if strings.Contains(statement.SQL, "$1") {
			t.Errorf("SQL = %q, want ? placeholders", statement.SQL)
		}
		if strings.Contains(statement.SQL, "`public`.`customers`") {
			t.Errorf("SQL = %q, want an unqualified table: `a`.`b` on MySQL names database a's table b", statement.SQL)
		}
		if strings.Contains(statement.SQL, "RETURNING") {
			t.Errorf("SQL = %q, and MySQL has no RETURNING", statement.SQL)
		}
		if got, want := strings.Count(statement.SQL, "?"), len(statement.Parameters); got != want {
			t.Errorf("SQL = %q has %d placeholders for %d parameters", statement.SQL, got, want)
		}
	}

	// A select yields rows in either dialect — Returning means "this statement
	// has rows to read", not "this statement carries a RETURNING clause". A
	// write is where the two dialects part company.
	if !read.Returning {
		t.Errorf("a MySQL select is not marked as returning rows")
	}
	for _, statement := range writes {
		if statement.Returning {
			t.Errorf("SQL = %q is marked as returning rows, and MySQL has no RETURNING", statement.SQL)
		}
	}
}

func TestABuilderRefusesWhatWouldBeWorseThanFailing(t *testing.T) {
	t.Parallel()

	values := map[string]any{"email": "ada@example.test", "id": float64(7)}

	// A delete with no condition is a truncate, and a user who meant that has a
	// mode for it — while a user who forgot a condition has just emptied a
	// table.
	if _, err := sqlbuild.Delete(sqlbuild.Postgres, customers, sqlbuild.DeleteRows, nil, "AND", sqlbuild.Removal{}); err == nil {
		t.Error("a delete with no condition was built")
	}
	if _, err := sqlbuild.Update(sqlbuild.Postgres, customers, values, nil); err == nil {
		t.Error("an update with nothing to match on was built")
	}
	if _, err := sqlbuild.Upsert(sqlbuild.Postgres, customers, values, nil); err == nil {
		t.Error("an upsert with no conflict target was built")
	}
	// Every column is a matching column, so there is nothing left to set. An
	// empty SET list is a syntax error, not an update.
	if _, err := sqlbuild.Update(sqlbuild.Postgres, customers, map[string]any{"id": float64(7)}, []string{"id"}); err == nil {
		t.Error("an update with nothing to set was built")
	}
	if _, err := sqlbuild.Insert(sqlbuild.Postgres, customers, map[string]any{}, false); err == nil {
		t.Error("an insert with no columns was built")
	}
	if _, err := sqlbuild.Select(sqlbuild.Postgres, sqlbuild.Target{}, nil, nil, "", nil, 0); err == nil {
		t.Error("a select with no table was built")
	}
	if _, err := sqlbuild.Select(sqlbuild.Postgres, customers, nil, []sqlbuild.Comparison{
		{Column: "tier", Operator: "; DROP TABLE customers --", Value: "x"},
	}, "", nil, 0); err == nil {
		t.Error("an unknown comparison was built into the statement")
	}
}

func TestEveryColumnIsAMatchingColumnBecomesDoNothing(t *testing.T) {
	t.Parallel()

	// DO UPDATE SET with an empty list is a syntax error; DO NOTHING is the
	// honest statement for "this row already exists and there is nothing to
	// change about it".
	statement, err := sqlbuild.Upsert(sqlbuild.Postgres, customers, map[string]any{"id": float64(7)}, []string{"id"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if !strings.Contains(statement.SQL, "DO NOTHING") {
		t.Errorf("SQL = %q, want DO NOTHING", statement.SQL)
	}
}

func TestIdentifierQuotingRefusesWhatEachServerWouldMangle(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		parts    []string
		postgres string
		mysql    string
	}{
		"a plain name":     {parts: []string{"customers"}, postgres: `"customers"`, mysql: "`customers`"},
		"a qualified name": {parts: []string{"public", "customers"}, postgres: `"public"."customers"`, mysql: "`public`.`customers`"},
		// The quote character is doubled, not stripped: the identifier a user
		// wrote is the identifier that gets addressed. Each dialect doubles its
		// own mark and leaves the other one alone.
		"a name carrying the other mark": {
			parts: []string{"we`ird"}, postgres: "\"we`ird\"", mysql: "`we``ird`",
		},
		"a name carrying its own mark": {
			parts: []string{`we"ird`}, postgres: `"we""ird"`, mysql: "`we\"ird`",
		},
		"a name that looks like SQL": {
			parts:    []string{`customers"; DROP TABLE users; --`},
			postgres: `"customers""; DROP TABLE users; --"`,
			mysql:    "`customers\"; DROP TABLE users; --`",
		},
		"a name with a dot": {parts: []string{"a.b"}, postgres: `"a.b"`, mysql: "`a.b`"},
		"a unicode name":    {parts: []string{"名前"}, postgres: `"名前"`, mysql: "`名前`"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := sqlbuild.Postgres.Identifier(testCase.parts...)
			if err != nil {
				t.Fatalf("Postgres.Identifier(%q) error = %v", testCase.parts, err)
			}
			if got != testCase.postgres {
				t.Errorf("Postgres.Identifier(%q) = %s, want %s", testCase.parts, got, testCase.postgres)
			}
			got, err = sqlbuild.MySQL.Identifier(testCase.parts...)
			if err != nil {
				t.Fatalf("MySQL.Identifier(%q) error = %v", testCase.parts, err)
			}
			if got != testCase.mysql {
				t.Errorf("MySQL.Identifier(%q) = %s, want %s", testCase.parts, got, testCase.mysql)
			}
		})
	}

	for name, parts := range map[string][]string{
		"nothing at all": {},
		"an empty part":  {""},
		"only spaces":    {"   "},
		// pgx strips a NUL instead of refusing it, which would silently rename
		// the thing being addressed.
		"a NUL byte": {"a\x00b"},
	} {
		t.Run("both refuse "+name, func(t *testing.T) {
			for _, dialect := range dialects {
				if _, err := dialect.Identifier(parts...); err == nil {
					t.Errorf("%s.Identifier(%q) was accepted", dialect.Name(), parts)
				}
			}
		})
	}

	// The two limits differ in kind, not only in number. PostgreSQL counts
	// bytes and truncates silently, so its refusal prevents two names colliding
	// into one; MySQL counts characters and rejects an over-long name itself,
	// so its refusal only buys a better message.
	twentyTwoRunes := strings.Repeat("名", 22) // 66 bytes, 22 characters
	if _, err := sqlbuild.Postgres.Identifier(twentyTwoRunes); err == nil {
		t.Error("PostgreSQL accepted a 66-byte name it would truncate")
	}
	if _, err := sqlbuild.MySQL.Identifier(twentyTwoRunes); err != nil {
		t.Errorf("MySQL refused a 22-character name it allows: %v", err)
	}
	if _, err := sqlbuild.Postgres.Identifier(strings.Repeat("a", sqlbuild.MaxIdentifierBytes)); err != nil {
		t.Errorf("a name exactly at PostgreSQL's limit was refused: %v", err)
	}
	if _, err := sqlbuild.MySQL.Identifier(strings.Repeat("a", sqlbuild.MaxIdentifierRunes+1)); err == nil {
		t.Error("MySQL accepted a 65-character name")
	}
	// MySQL refuses a trailing space in a table name even when it is quoted, so
	// a name that reached the server would fail at run time rather than here.
	if _, err := sqlbuild.MySQL.Identifier("trailing "); err == nil {
		t.Error("MySQL accepted a name ending in a space")
	}
}

// FuzzIdentifier is the defence for the one place user input reaches statement
// text.
//
// The property is absolute: whatever comes back is either an error or a string
// that starts and ends with a double quote and whose only unescaped quotes are
// those two. A value that escaped the quoting would be arbitrary SQL in a
// statement about to run against a customer's database.
func FuzzIdentifier(f *testing.F) {
	for _, seed := range []string{
		"customers", "public", `we"ird`, `"; DROP TABLE users; --`, "a\nb", "名前",
		"a\x00b", strings.Repeat("a", 64), "", "  ", `""`, `\`, "%s", "$1",
		// MySQL's own mark, doubled and undoubled, and the two shapes its
		// extra rules refuse.
		"`", "a`b", "``", "trailing ", strings.Repeat("名", 22),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, part string) {
		// Both dialects in one target, which keeps the committed corpus live:
		// splitting it in two would orphan every entry, and the property being
		// checked is the same either way.
		for _, dialect := range dialects {
			quoted, err := dialect.Identifier(part)
			if err != nil {
				continue
			}
			mark := byte('"')
			if dialect.Name() == "mysql" {
				mark = '`'
			}
			if len(quoted) < 2 || quoted[0] != mark || quoted[len(quoted)-1] != mark {
				t.Fatalf("%s.Identifier(%q) = %q, which is not a quoted identifier", dialect.Name(), part, quoted)
			}
			// Every mark inside the body must be doubled. Counting them is the
			// whole check: an odd run anywhere means the identifier ended early
			// and whatever follows is statement text.
			body := quoted[1 : len(quoted)-1]
			for index := 0; index < len(body); index++ {
				if body[index] != mark {
					continue
				}
				if index+1 >= len(body) || body[index+1] != mark {
					t.Fatalf("%s.Identifier(%q) = %q, which closes early at byte %d",
						dialect.Name(), part, quoted, index+1)
				}
				index++
			}
			if strings.ContainsRune(quoted, 0) {
				t.Fatalf("%s.Identifier(%q) = %q, which carries a NUL byte", dialect.Name(), part, quoted)
			}
		}
	})
}

// TestAQuotedIdentifierRoundTripsAgainstALiveServer is the only assertion that
// can settle whether the quoting is right.
//
// Every unit test above compares one string to another string this repository
// also wrote. What actually matters is that PostgreSQL reads back the name the
// user typed — so the name is created through the builder's own quoting and
// then read out of information_schema and compared byte for byte.
func TestAQuotedIdentifierRoundTripsAgainstALiveServer(t *testing.T) {
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the live identifier coverage")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("KILASFLOW_TEST_POSTGRES_DSN is not a URL: %v", err)
	}
	password, _ := parsed.User.Password()
	// The guard admits exactly the endpoint under test, as written: the
	// default-deny policy refuses loopback test databases.
	endpoint := parsed.Hostname() + ":" + parsed.Port()
	guard := sqlnode.Guard{Policy: safehttp.Policy{AllowedPrivateEndpoints: []string{endpoint}}}
	connection, err := sqlnode.Open(context.Background(), sqlnode.DriverPostgres, map[string]string{
		"host": parsed.Hostname(), "port": parsed.Port(),
		"database": strings.TrimPrefix(parsed.Path, "/"),
		"user":     parsed.User.Username(), "password": password, "sslMode": "disable",
	}, guard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	// Names chosen to break naive quoting: an embedded double quote, a dot that
	// looks like a qualification, a space, a keyword, and one exactly at the
	// length limit.
	for _, name := range []string{
		`we"ird`,
		`has.a.dot`,
		`has a space`,
		`select`,
		`Mixed Case`,
		`"; DROP TABLE users; --`,
		strings.Repeat("z", sqlbuild.MaxIdentifierBytes),
	} {
		t.Run(name, func(t *testing.T) {
			quoted, err := sqlbuild.Postgres.Identifier(name)
			if err != nil {
				t.Fatalf("Identifier(%q) error = %v", name, err)
			}
			create := "CREATE TABLE " + quoted + " (" + quoted + " INTEGER)"
			if _, err := connection.Execute(context.Background(), create, nil, sqlnode.DefaultLimits()); err != nil {
				t.Fatalf("%s: %v", create, err)
			}
			t.Cleanup(func() {
				_, _ = connection.Execute(context.Background(), "DROP TABLE IF EXISTS "+quoted, nil, sqlnode.DefaultLimits())
			})

			// Read back through the catalogue, which reports the name as the
			// server actually stored it.
			result, err := connection.Query(context.Background(),
				`SELECT table_name, column_name FROM information_schema.columns WHERE table_name = $1`,
				[]any{name}, sqlnode.DefaultLimits())
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if len(result.Rows) != 1 {
				t.Fatalf("catalogue rows = %d, want the one table this created", len(result.Rows))
			}
			if got := result.Rows[0]["table_name"]; got != name {
				t.Errorf("table name = %q, want %q byte for byte", got, name)
			}
			if got := result.Rows[0]["column_name"]; got != name {
				t.Errorf("column name = %q, want %q byte for byte", got, name)
			}
		})
	}
}

// TestMySQLIdentifiersAndUpsertsAgainstALiveServer is what settles the two
// decisions this dialect makes that no offline test can.
//
// Both MySQL and MariaDB, because they differ on exactly those decisions:
// MariaDB has INSERT … RETURNING (rejected here, because using it would split
// the dialect on a runtime version probe) and does not have the MySQL 8.0.19
// row-alias upsert form (which is why VALUES() was chosen).
func TestMySQLIdentifiersAndUpsertsAgainstALiveServer(t *testing.T) {
	for name, env := range map[string]string{
		"mysql":   "KILASFLOW_TEST_MYSQL_DSN",
		"mariadb": "KILASFLOW_TEST_MARIADB_DSN",
	} {
		t.Run(name, func(t *testing.T) {
			connection := openLive(t, env, sqlnode.DriverMySQL)

			// Names chosen to break naive quoting, plus one at the character
			// limit that is over the byte limit — the case that proves the two
			// dialects count differently rather than sharing a number.
			for _, identifier := range []string{
				"we`ird", "has.a.dot", "has a space", "select", strings.Repeat("名", 22),
			} {
				t.Run(identifier, func(t *testing.T) {
					quoted, err := sqlbuild.MySQL.Identifier(identifier)
					if err != nil {
						t.Fatalf("Identifier(%q) error = %v", identifier, err)
					}
					create := "CREATE TABLE " + quoted + " (" + quoted + " INT)"
					if _, err := connection.Execute(context.Background(), create, nil, sqlnode.DefaultLimits()); err != nil {
						t.Fatalf("%s: %v", create, err)
					}
					t.Cleanup(func() {
						_, _ = connection.Execute(context.Background(), "DROP TABLE IF EXISTS "+quoted, nil, sqlnode.DefaultLimits())
					})
					// Aliased, because MySQL 8 labels an unaliased
					// information_schema column in upper case and MariaDB does
					// not — a difference that has nothing to do with what is
					// being tested.
					result, err := connection.Query(context.Background(),
						`SELECT column_name AS name FROM information_schema.columns WHERE table_name = ?`,
						[]any{identifier}, sqlnode.DefaultLimits())
					if err != nil {
						t.Fatalf("read back: %v", err)
					}
					if len(result.Rows) != 1 {
						t.Fatalf("catalogue rows = %d, want the one column this created", len(result.Rows))
					}
					// The column half is unconditional: MySQL preserves column
					// case regardless of lower_case_table_names, which only
					// folds table names.
					if got := result.Rows[0]["name"]; got != identifier {
						t.Errorf("column name = %q, want %q byte for byte", got, identifier)
					}
				})
			}

			// The upsert form. VALUES() rather than the MySQL 8.0.19 row alias,
			// which MariaDB does not have at all.
			run := func(statement sqlnode.Statement) sqlnode.Result {
				t.Helper()
				result, err := connection.Execute(context.Background(), statement.SQL, statement.Parameters, sqlnode.DefaultLimits())
				if err != nil {
					t.Fatalf("%s: %v", statement.SQL, err)
				}
				return result
			}
			ddl := func(statement string) {
				t.Helper()
				if _, err := connection.Execute(context.Background(), statement, nil, sqlnode.DefaultLimits()); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}
			ddl(`DROP TABLE IF EXISTS kilas_upsert`)
			ddl(`CREATE TABLE kilas_upsert (id INT PRIMARY KEY, tier VARCHAR(32))`)
			t.Cleanup(func() { ddl(`DROP TABLE IF EXISTS kilas_upsert`) })

			target := sqlbuild.Target{Table: "kilas_upsert"}
			insert, err := sqlbuild.Upsert(sqlbuild.MySQL, target,
				map[string]any{"id": 1, "tier": "gold"}, []string{"id"})
			if err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			// 1 for an insert, 2 for an update, 0 when the row existed and
			// nothing changed. Passing that through as a row count would be
			// misleading, which is why the node names it.
			if got := run(insert).RowsAffected; got != 1 {
				t.Errorf("first upsert affected %d, want 1 for an insert", got)
			}
			changed, _ := sqlbuild.Upsert(sqlbuild.MySQL, target,
				map[string]any{"id": 1, "tier": "silver"}, []string{"id"})
			if got := run(changed).RowsAffected; got != 2 {
				t.Errorf("changing upsert affected %d, want 2 for an update", got)
			}
			if got := run(changed).RowsAffected; got != 0 {
				t.Errorf("unchanged upsert affected %d, want 0", got)
			}

			// The generated key comes from the driver's own answer for that
			// statement, so it is zero rather than stale where none was made.
			ddl(`DROP TABLE IF EXISTS kilas_auto`)
			ddl(`CREATE TABLE kilas_auto (id INT AUTO_INCREMENT PRIMARY KEY, tier VARCHAR(32))`)
			t.Cleanup(func() { ddl(`DROP TABLE IF EXISTS kilas_auto`) })
			auto, _ := sqlbuild.Insert(sqlbuild.MySQL, sqlbuild.Target{Table: "kilas_auto"}, map[string]any{"tier": "gold"}, false)
			if got := run(auto).LastInsertID; got == 0 {
				t.Error("an auto-increment insert reported no generated key")
			}
			// Immediately afterwards, into a table that generates none. A
			// SELECT LAST_INSERT_ID() here would report the previous id.
			plain, _ := sqlbuild.Insert(sqlbuild.MySQL, target, map[string]any{"id": 99, "tier": "bronze"}, false)
			if got := run(plain).LastInsertID; got != 0 {
				t.Errorf("insert id = %d, want 0 rather than the previous statement's key", got)
			}
		})
	}
}

// openLive connects to an integration server named by an environment variable.
func openLive(t *testing.T, env string, driver sqlnode.Driver) *sqlnode.Connection {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this half of the coverage", env)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s is not a URL: %v", env, err)
	}
	password, _ := parsed.User.Password()
	endpoint := parsed.Hostname() + ":" + parsed.Port()
	guard := sqlnode.Guard{Policy: safehttp.Policy{AllowedPrivateEndpoints: []string{endpoint}}}
	connection, err := sqlnode.Open(context.Background(), driver, map[string]string{
		"host": parsed.Hostname(), "port": parsed.Port(),
		"database": strings.TrimPrefix(parsed.Path, "/"),
		"user":     parsed.User.Username(), "password": password, "sslMode": "disable",
	}, guard)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}
