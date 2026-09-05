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
func golden(t *testing.T, name string, statement sqlnode.Statement, err error) {
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

	path := filepath.Join("testdata", name+".sql")
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

func TestEachOperationBuildsTheStatementItPromises(t *testing.T) {
	values := map[string]any{"email": "ada@example.test", "tier": "gold", "id": float64(7)}

	t.Run("select", func(t *testing.T) {
		statement, err := sqlbuild.Select(customers, nil, []sqlbuild.Comparison{
			{Column: "tier", Operator: "equals", Value: "gold"},
			{Column: "email", Operator: "like", Value: "%@example.test"},
		}, "AND", []sqlbuild.Order{{Column: "email"}}, 50)
		golden(t, "select", statement, err)
	})

	t.Run("select all", func(t *testing.T) {
		statement, err := sqlbuild.Select(customers, []string{"id", "email"}, nil, "", nil, 0)
		golden(t, "select_all", statement, err)
	})

	t.Run("select with a null test", func(t *testing.T) {
		// A null test binds nothing, so it must not consume a placeholder —
		// which is exactly the numbering bug a "contains" assertion misses.
		statement, err := sqlbuild.Select(customers, nil, []sqlbuild.Comparison{
			{Column: "tier", Operator: "isNull"},
			{Column: "email", Operator: "equals", Value: "ada@example.test"},
		}, "OR", nil, 0)
		golden(t, "select_null", statement, err)
	})

	t.Run("insert", func(t *testing.T) {
		statement, err := sqlbuild.Insert(customers, values)
		golden(t, "insert", statement, err)
	})

	t.Run("update", func(t *testing.T) {
		statement, err := sqlbuild.Update(customers, values, []string{"id"})
		golden(t, "update", statement, err)
	})

	t.Run("upsert", func(t *testing.T) {
		statement, err := sqlbuild.Upsert(customers, values, []string{"id"})
		golden(t, "upsert", statement, err)
	})

	t.Run("delete rows", func(t *testing.T) {
		statement, err := sqlbuild.Delete(customers, sqlbuild.DeleteRows, []sqlbuild.Comparison{
			{Column: "tier", Operator: "equals", Value: "bronze"},
		}, "AND")
		golden(t, "delete_rows", statement, err)
	})

	t.Run("truncate", func(t *testing.T) {
		statement, err := sqlbuild.Delete(customers, sqlbuild.DeleteTruncate, nil, "")
		golden(t, "delete_truncate", statement, err)
	})

	t.Run("drop", func(t *testing.T) {
		statement, err := sqlbuild.Delete(customers, sqlbuild.DeleteDrop, nil, "")
		golden(t, "delete_drop", statement, err)
	})
}

func TestABuilderRefusesWhatWouldBeWorseThanFailing(t *testing.T) {
	t.Parallel()

	values := map[string]any{"email": "ada@example.test", "id": float64(7)}

	// A delete with no condition is a truncate, and a user who meant that has a
	// mode for it — while a user who forgot a condition has just emptied a
	// table.
	if _, err := sqlbuild.Delete(customers, sqlbuild.DeleteRows, nil, "AND"); err == nil {
		t.Error("a delete with no condition was built")
	}
	if _, err := sqlbuild.Update(customers, values, nil); err == nil {
		t.Error("an update with nothing to match on was built")
	}
	if _, err := sqlbuild.Upsert(customers, values, nil); err == nil {
		t.Error("an upsert with no conflict target was built")
	}
	// Every column is a matching column, so there is nothing left to set. An
	// empty SET list is a syntax error, not an update.
	if _, err := sqlbuild.Update(customers, map[string]any{"id": float64(7)}, []string{"id"}); err == nil {
		t.Error("an update with nothing to set was built")
	}
	if _, err := sqlbuild.Insert(customers, map[string]any{}); err == nil {
		t.Error("an insert with no columns was built")
	}
	if _, err := sqlbuild.Select(sqlbuild.Target{}, nil, nil, "", nil, 0); err == nil {
		t.Error("a select with no table was built")
	}
	if _, err := sqlbuild.Select(customers, nil, []sqlbuild.Comparison{
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
	statement, err := sqlbuild.Upsert(customers, map[string]any{"id": float64(7)}, []string{"id"})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if !strings.Contains(statement.SQL, "DO NOTHING") {
		t.Errorf("SQL = %q, want DO NOTHING", statement.SQL)
	}
}

func TestIdentifierQuotingRefusesWhatPostgresWouldMangle(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		parts []string
		want  string
	}{
		"a plain name":     {parts: []string{"customers"}, want: `"customers"`},
		"a qualified name": {parts: []string{"public", "customers"}, want: `"public"."customers"`},
		// The quote is doubled, not stripped: the identifier a user wrote is
		// the identifier that gets addressed.
		"a name with a quote in it": {parts: []string{`we"ird`}, want: `"we""ird"`},
		"a name that looks like SQL": {
			parts: []string{`customers"; DROP TABLE users; --`},
			want:  `"customers""; DROP TABLE users; --"`,
		},
		"a name with a dot":     {parts: []string{"a.b"}, want: `"a.b"`},
		"a unicode name":        {parts: []string{"名前"}, want: `"名前"`},
		"a name with a newline": {parts: []string{"a\nb"}, want: "\"a\nb\""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := sqlbuild.Identifier(testCase.parts...)
			if err != nil {
				t.Fatalf("Identifier(%q) error = %v", testCase.parts, err)
			}
			if got != testCase.want {
				t.Errorf("Identifier(%q) = %s, want %s", testCase.parts, got, testCase.want)
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
		// PostgreSQL truncates at 63 bytes silently, so two names that long can
		// become one — a statement that runs and writes the wrong column.
		"too long": {strings.Repeat("a", sqlbuild.MaxIdentifierBytes+1)},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, err := sqlbuild.Identifier(parts...); err == nil {
				t.Errorf("Identifier(%q) was accepted", parts)
			}
		})
	}

	// Exactly at the limit is fine; one byte over is not.
	if _, err := sqlbuild.Identifier(strings.Repeat("a", sqlbuild.MaxIdentifierBytes)); err != nil {
		t.Errorf("a name exactly at the limit was refused: %v", err)
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
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, part string) {
		quoted, err := sqlbuild.Identifier(part)
		if err != nil {
			return
		}
		if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
			t.Fatalf("Identifier(%q) = %q, which is not a quoted identifier", part, quoted)
		}
		// Every quote inside the body must be doubled. Counting them is the
		// whole check: an odd run of quotes anywhere means the identifier ended
		// early and whatever follows is statement text.
		body := quoted[1 : len(quoted)-1]
		for index := 0; index < len(body); index++ {
			if body[index] != '"' {
				continue
			}
			if index+1 >= len(body) || body[index+1] != '"' {
				t.Fatalf("Identifier(%q) = %q, which closes early at byte %d", part, quoted, index+1)
			}
			index++
		}
		if strings.ContainsRune(quoted, 0) {
			t.Fatalf("Identifier(%q) = %q, which carries a NUL byte", part, quoted)
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
	connection, err := sqlnode.Open(context.Background(), sqlnode.DriverPostgres, map[string]string{
		"host": parsed.Hostname(), "port": parsed.Port(),
		"database": strings.TrimPrefix(parsed.Path, "/"),
		"user":     parsed.User.Username(), "password": password, "sslMode": "disable",
	}, sqlnode.Guard{})
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
			quoted, err := sqlbuild.Identifier(name)
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
