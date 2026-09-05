package sqlnode

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Introspection is how the editor asks a customer's database what it holds.
//
// It lives here, as methods on Connection, rather than in a package of its own
// opening its own handles. A separate package would duplicate DSN construction,
// the SQLite path guard and the password redaction every driver error goes
// through — and three copies of a redaction rule is how a password eventually
// reaches a log.
//
// Every query reads `information_schema`, not `pg_catalog`. pg_catalog is
// richer, and it is PostgreSQL-only: using it would make the MySQL dialect a
// second implementation rather than a second set of column names.
//
// What `information_schema` is *not* is authoritative. It is privilege
// filtered: a role with no privilege on a table simply sees no row for it, so
// an empty list is indistinguishable from an empty database. Every list here
// therefore reports emptiness as something the caller must explain rather than
// as a fact about the database.

// IntrospectionTimeout bounds one catalogue read.
//
// Far below DefaultLimits' thirty seconds, because this runs while somebody is
// typing. A picker that takes half a minute to populate has already been given
// up on, and the connection it holds is against someone else's production
// database.
const IntrospectionTimeout = 5 * time.Second

// Table is one table or view the caller can see.
type Table struct {
	Name string
	// Kind is "table" or "view", lowercased from the catalogue's own spelling.
	Kind string
}

// Column is one column, with everything a mapper needs to type it.
type Column struct {
	Name string
	// DataType is the column's real type.
	//
	// PostgreSQL's information_schema reports `USER-DEFINED` for an enum or a
	// domain and `ARRAY` for any array, with the real name only in `udt_name`.
	// A mapper keyed on data_type would type every enum column as unknown,
	// pass every test written over text and integer columns, and fail only when
	// a real insert reached a real column.
	DataType string
	Nullable bool
	Default  string
	// PrimaryKey marks a column that identifies a row, which is what a mapper
	// offers as a matching column.
	PrimaryKey bool
	// Generated marks a column the database fills in — an identity, a computed
	// column — which may be matched on and never written.
	Generated bool
}

// Schemas lists the schemas this credential's role can see.
//
// On MySQL a schema is a database, which is the same question asked in that
// dialect's vocabulary rather than a different one.
func (connection *Connection) Schemas(ctx context.Context) ([]string, error) {
	statement := `SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('pg_catalog', 'information_schema')
		  AND schema_name NOT LIKE 'pg\_toast%' AND schema_name NOT LIKE 'pg\_temp%'
		ORDER BY schema_name`
	if connection.driver == DriverMySQL {
		statement = `SELECT schema_name FROM information_schema.schemata
			WHERE schema_name NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
			ORDER BY schema_name`
	}
	rows, err := connection.introspect(ctx, statement)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, rowText(row, "schema_name"))
	}
	return names, nil
}

// Tables lists the tables and views in one schema.
func (connection *Connection) Tables(ctx context.Context, schema string) ([]Table, error) {
	if strings.TrimSpace(schema) == "" {
		return nil, fmt.Errorf("a table list needs a schema")
	}
	statement := `SELECT table_name, table_type FROM information_schema.tables
		WHERE table_schema = ` + connection.placeholder(1) + ` ORDER BY table_name`
	rows, err := connection.introspect(ctx, statement, schema)
	if err != nil {
		return nil, err
	}
	tables := make([]Table, 0, len(rows))
	for _, row := range rows {
		kind := "table"
		if strings.Contains(strings.ToUpper(rowText(row, "table_type")), "VIEW") {
			kind = "view"
		}
		tables = append(tables, Table{Name: rowText(row, "table_name"), Kind: kind})
	}
	return tables, nil
}

// Columns lists one table's columns, typed.
func (connection *Connection) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	if strings.TrimSpace(schema) == "" || strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("a column list needs a schema and a table")
	}

	statement := `SELECT column_name,
			CASE WHEN data_type IN ('USER-DEFINED', 'ARRAY') THEN udt_name ELSE data_type END AS data_type,
			is_nullable,
			COALESCE(column_default, '') AS column_default,
			CASE WHEN COALESCE(is_identity, 'NO') = 'YES'
			      OR COALESCE(is_generated, 'NEVER') <> 'NEVER'
			      -- SERIAL is neither an identity nor generated: it is an
			      -- integer with a nextval default, and information_schema has
			      -- no other word for it. Missing this would offer the
			      -- commonest primary key in PostgreSQL as a column to fill in.
			      OR COALESCE(column_default, '') LIKE 'nextval(%'
			     THEN 'YES' ELSE 'NO' END AS gen_flag
		FROM information_schema.columns
		WHERE table_schema = ` + connection.placeholder(1) + ` AND table_name = ` + connection.placeholder(2) + `
		ORDER BY ordinal_position`
	if connection.driver == DriverMySQL {
		// MySQL has no udt_name, no is_identity and no is_generated. What it
		// has is `extra`, which carries auto_increment and GENERATED, and
		// `column_type`, which spells an enum out in full where data_type only
		// says "enum".
		statement = `SELECT column_name,
				CASE WHEN data_type IN ('enum', 'set') THEN column_type ELSE data_type END AS data_type,
				is_nullable,
				COALESCE(column_default, '') AS column_default,
				-- Aliased away from the word "generated", which MySQL reserves.
				CASE WHEN extra LIKE '%auto_increment%' OR extra LIKE '%GENERATED%' THEN 'YES' ELSE 'NO' END AS gen_flag
			FROM information_schema.columns
			WHERE table_schema = ? AND table_name = ?
			ORDER BY ordinal_position`
	}
	rows, err := connection.introspect(ctx, statement, schema, table)
	if err != nil {
		return nil, err
	}
	keys, err := connection.primaryKey(ctx, schema, table)
	if err != nil {
		return nil, err
	}

	columns := make([]Column, 0, len(rows))
	for _, row := range rows {
		name := rowText(row, "column_name")
		columns = append(columns, Column{
			Name:       name,
			DataType:   rowText(row, "data_type"),
			Nullable:   strings.EqualFold(rowText(row, "is_nullable"), "YES"),
			Default:    rowText(row, "column_default"),
			PrimaryKey: keys[name],
			Generated:  strings.EqualFold(rowText(row, "gen_flag"), "YES"),
		})
	}
	return columns, nil
}

// primaryKey reads which columns identify a row.
func (connection *Connection) primaryKey(ctx context.Context, schema, table string) (map[string]bool, error) {
	statement := `SELECT kcu.column_name FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name = kcu.constraint_name
		 AND tc.table_schema = kcu.table_schema
		 AND tc.table_name = kcu.table_name
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND tc.table_schema = ` + connection.placeholder(1) + `
		  AND tc.table_name = ` + connection.placeholder(2)
	rows, err := connection.introspect(ctx, statement, schema, table)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(rows))
	for _, row := range rows {
		keys[rowText(row, "column_name")] = true
	}
	return keys, nil
}

// introspect runs one catalogue query under the introspection deadline.
func (connection *Connection) introspect(ctx context.Context, statement string, parameters ...any) ([]map[string]any, error) {
	if connection == nil || connection.db == nil {
		return nil, fmt.Errorf("database connection is not open")
	}
	if connection.driver == DriverSQLite {
		// SQLite has no information_schema at all. Said plainly rather than
		// returning an empty list, which would read as "this database has no
		// tables".
		return nil, fmt.Errorf("SQLite does not publish an information schema, so its tables cannot be listed here")
	}
	result, err := connection.Query(ctx, statement, parameters, Limits{
		Timeout: IntrospectionTimeout,
		// Generous, and still a bound: a schema with more columns than this is
		// not a picker anybody can use.
		MaxRows: 5_000,
	})
	if err != nil {
		return nil, Sanitize(err)
	}
	return result.Rows, nil
}

// placeholder renders the nth bound parameter in this driver's dialect.
func (connection *Connection) placeholder(position int) string {
	if connection.driver == DriverPostgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

// rowText reads a column from an introspection row.
//
// The catalogue's own column names come back lowercased on PostgreSQL and in
// whatever case the server uses on MySQL, so both are tried rather than
// assuming one.
func rowText(row map[string]any, column string) string {
	for _, key := range []string{column, strings.ToUpper(column)} {
		if value, present := row[key]; present {
			if text, ok := value.(string); ok {
				return text
			}
			if value != nil {
				return fmt.Sprint(value)
			}
		}
	}
	return ""
}
