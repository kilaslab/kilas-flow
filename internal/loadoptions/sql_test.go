package loadoptions_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// liveDatabases are the two servers introspection is measured against, each
// gated on its own DSN. SQLite is absent on purpose: it publishes no
// information schema at all, which the loaders say plainly rather than
// answering with an empty list.
var liveDatabases = map[string]struct {
	env            string
	credentialType string
	// schema is what the fixture is created in: a real schema on PostgreSQL,
	// the database itself on MySQL, which has no schemas separate from them.
	schema string
	// enumType and arrayType are the two shapes PostgreSQL's information_schema
	// lies about. MySQL spells its enum out and has no arrays.
	enumColumn  string
	arrayColumn string
}{
	"postgres": {env: "KILASFLOW_TEST_POSTGRES_DSN", credentialType: "postgres", schema: "kilas_introspect",
		enumColumn: "tier", arrayColumn: "labels"},
	"mysql": {env: "KILASFLOW_TEST_MYSQL_DSN", credentialType: "mysql", schema: "kilasflow",
		enumColumn: "tier"},
}

// liveCredential turns an integration DSN into credential fields.
func liveCredential(t *testing.T, env, credentialType string) *loadoptions.ResolvedCredential {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		t.Skipf("set %s to run this half of the introspection coverage", env)
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
	return &loadoptions.ResolvedCredential{
		Record: credentials.Record{ID: "cred-db", Name: "Integration", Type: credentialType},
		Fields: map[string]string{
			"host": parsed.Hostname(), "port": parsed.Port(),
			"database": strings.TrimPrefix(parsed.Path, "/"),
			"user":     parsed.User.Username(), "password": password, "sslMode": sslMode,
		},
	}
}

// openLive connects the way a loader does, so a fixture is created through the
// same path it is later read through.
func openLive(t *testing.T, credential *loadoptions.ResolvedCredential) *sqlnode.Connection {
	t.Helper()
	driver, _ := sqlnode.DriverForCredential(credential.Record.Type)
	connection, err := sqlnode.Open(context.Background(), driver, credential.Fields, sqlnode.Guard{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func run(t *testing.T, connection *sqlnode.Connection, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := connection.Execute(context.Background(), statement, nil, sqlnode.DefaultLimits()); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func TestTheFiveLoadersReadALiveDatabase(t *testing.T) {
	for name, live := range liveDatabases {
		t.Run(name, func(t *testing.T) {
			credential := liveCredential(t, live.env, live.credentialType)
			connection := openLive(t, credential)

			// The fixture carries the two shapes PostgreSQL's information_schema
			// misreports: an enum, which it calls USER-DEFINED, and an array,
			// which it calls ARRAY. A mapper keyed on data_type would type both
			// as unknown, pass every test written over text and integer columns,
			// and fail only when a real insert reached a real column.
			switch live.credentialType {
			case "postgres":
				run(t, connection,
					`DROP SCHEMA IF EXISTS kilas_introspect CASCADE`,
					`CREATE SCHEMA kilas_introspect`,
					`CREATE TYPE kilas_introspect.tier AS ENUM ('gold','silver')`,
					`CREATE TABLE kilas_introspect.customers (
						id SERIAL PRIMARY KEY,
						email TEXT NOT NULL,
						tier kilas_introspect.tier,
						labels TEXT[],
						note TEXT DEFAULT '',
						created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
					`CREATE VIEW kilas_introspect.recent AS SELECT id FROM kilas_introspect.customers`)
				t.Cleanup(func() { run(t, connection, `DROP SCHEMA IF EXISTS kilas_introspect CASCADE`) })
			case "mysql":
				run(t, connection,
					`DROP TABLE IF EXISTS customers_introspect`,
					`CREATE TABLE customers_introspect (
						id INT AUTO_INCREMENT PRIMARY KEY,
						email VARCHAR(255) NOT NULL,
						tier ENUM('gold','silver'),
						note TEXT,
						created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
				t.Cleanup(func() { run(t, connection, `DROP TABLE IF EXISTS customers_introspect`) })
			}
			table := "customers"
			if live.credentialType == "mysql" {
				table = "customers_introspect"
			}

			resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Nanosecond)
			if err := loadoptions.RegisterSQL(resolver, sqlnode.Guard{}); err != nil {
				t.Fatalf("RegisterSQL() error = %v", err)
			}
			load := func(t *testing.T, loader string, dependencies map[string]string) loadoptions.Result {
				t.Helper()
				result, err := resolver.Load(context.Background(),
					property.OptionsLoader{Source: property.LoaderInternal, Name: loader, CredentialType: live.credentialType},
					loadoptions.Scope{TenantID: "tenant-a", Dependencies: dependencies},
					credential.Record.ID, fixedCredential{credential})
				if err != nil {
					t.Fatalf("%s: %v", loader, err)
				}
				return result
			}

			// 1. Schemas.
			schemas := load(t, loadoptions.SQLSchemasLoader, nil)
			if !hasValue(schemas.Options, live.schema) {
				t.Fatalf("schemas = %#v, want %q", schemas.Options, live.schema)
			}
			for _, option := range schemas.Options {
				if option.Value == "information_schema" || option.Value == "pg_catalog" {
					t.Errorf("schemas include the catalogue itself: %#v", option)
				}
			}

			// 2. Tables, including the view, labelled rather than hidden.
			tables := load(t, loadoptions.SQLTablesLoader, map[string]string{"schema": live.schema})
			if !hasValue(tables.Options, table) {
				t.Fatalf("tables = %#v, want %q", tables.Options, table)
			}
			if live.credentialType == "postgres" {
				found := false
				for _, option := range tables.Options {
					if option.Value == "recent" && strings.Contains(option.Label, "view") {
						found = true
					}
				}
				if !found {
					t.Errorf("tables = %#v, want the view listed and labelled", tables.Options)
				}
			}

			dependencies := map[string]string{"schema": live.schema, "table": table}

			// 3. Columns, with their types shown inline.
			columns := load(t, loadoptions.SQLColumnsLoader, dependencies)
			for _, want := range []string{"id", "email", live.enumColumn} {
				if !hasValue(columns.Options, want) {
					t.Errorf("columns = %#v, want %q", columns.Options, want)
				}
			}

			// 4. Matching columns: the primary key and nothing else.
			matching := load(t, loadoptions.SQLMatchingColumnsLoader, dependencies)
			if len(matching.Options) != 1 || matching.Options[0].Value != "id" {
				t.Errorf("matching columns = %#v, want the primary key only", matching.Options)
			}

			// 5. Mapping columns, typed.
			schema, err := resolver.LoadSchema(context.Background(),
				property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLMappingColumnsLoader, CredentialType: live.credentialType},
				loadoptions.Scope{TenantID: "tenant-a", Dependencies: dependencies},
				credential.Record.ID, fixedCredential{credential})
			if err != nil {
				t.Fatalf("LoadSchema() error = %v", err)
			}
			fields := map[string]property.MapperField{}
			for _, field := range schema.Fields {
				fields[field.ID] = field
			}
			if !fields["id"].ReadOnly || !fields["id"].CanBeUsedToMatch || !fields["id"].DefaultMatch {
				t.Errorf("id = %#v, want a read-only default match", fields["id"])
			}
			if !fields["email"].Required || fields["email"].Type != "string" {
				t.Errorf("email = %#v, want a required string", fields["email"])
			}
			// Not null, but with a default the database supplies, so the row is
			// accepted without it.
			if fields["created_at"].Required {
				t.Errorf("created_at = %#v, want it optional because the database defaults it", fields["created_at"])
			}
			// The enum reports its own name, not the placeholder. This is the
			// assertion the whole fixture exists for.
			if strings.EqualFold(fields[live.enumColumn].Type, "USER-DEFINED") {
				t.Errorf("%s = %#v, want its real type rather than the placeholder", live.enumColumn, fields[live.enumColumn])
			}
			if live.arrayColumn != "" {
				if fields[live.arrayColumn].Type != "array" {
					t.Errorf("%s = %#v, want it typed as an array rather than ARRAY the placeholder",
						live.arrayColumn, fields[live.arrayColumn])
				}
			}
		})
	}
}

func TestAnEmptyCatalogueSaysWhyRatherThanReadingAsAnEmptyDatabase(t *testing.T) {
	for name, live := range liveDatabases {
		t.Run(name, func(t *testing.T) {
			credential := liveCredential(t, live.env, live.credentialType)
			resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Nanosecond)
			if err := loadoptions.RegisterSQL(resolver, sqlnode.Guard{}); err != nil {
				t.Fatalf("RegisterSQL() error = %v", err)
			}

			// information_schema is privilege filtered, so a schema nobody can
			// see returns nothing — indistinguishable from an empty database
			// unless the answer says so.
			result, err := resolver.Load(context.Background(),
				property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLTablesLoader, CredentialType: live.credentialType},
				loadoptions.Scope{TenantID: "tenant-a", Dependencies: map[string]string{"schema": "nothing_here"}},
				credential.Record.ID, fixedCredential{credential})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(result.Options) != 0 {
				t.Fatalf("options = %#v, want none", result.Options)
			}
			if !strings.Contains(result.Reason, "privileges") {
				t.Errorf("reason = %q, want it to distinguish a narrow grant from an empty database", result.Reason)
			}
		})
	}
}

func TestIntrospectionRefusesWhatItCannotAnswerRatherThanStalling(t *testing.T) {
	t.Parallel()

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Nanosecond)
	if err := loadoptions.RegisterSQL(resolver, sqlnode.Guard{}); err != nil {
		t.Fatalf("RegisterSQL() error = %v", err)
	}
	sqlite := &loadoptions.ResolvedCredential{
		Record: credentials.Record{ID: "cred-db", Name: "Local file", Type: "sqlite"},
		Fields: map[string]string{"path": t.TempDir() + "/workflow.db"},
	}

	// SQLite publishes no information schema. Said plainly rather than
	// answering with an empty list, which would read as "this database has no
	// tables".
	_, err := resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLSchemasLoader, CredentialType: "sqlite"},
		loadoptions.Scope{TenantID: "tenant-a"}, sqlite.Record.ID, fixedCredential{sqlite})
	if err == nil || !strings.Contains(err.Error(), "information schema") {
		t.Fatalf("error = %v, want SQLite's own limitation named", err)
	}

	// A loader that needs a credential and is given none says which type is
	// missing, rather than attempting a connection with nothing to connect
	// with and failing as "could not reach the database".
	_, err = resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLSchemasLoader, CredentialType: "postgres"},
		loadoptions.Scope{TenantID: "tenant-a"}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "postgres credential") {
		t.Fatalf("error = %v, want the missing credential type named", err)
	}

	// And a credential of the wrong type is refused before a connection.
	_, err = resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLSchemasLoader, CredentialType: "postgres"},
		loadoptions.Scope{TenantID: "tenant-a"}, sqlite.Record.ID, fixedCredential{sqlite})
	if err == nil || !strings.Contains(err.Error(), "sqlite credential") {
		t.Fatalf("error = %v, want the type mismatch named", err)
	}
}

func TestIntrospectionHoldsADeadlineWellBelowAStatementTimeout(t *testing.T) {
	t.Parallel()

	// The picker runs while somebody is typing; the statement default is thirty
	// seconds, which is a stalled editor rather than a slow one.
	if sqlnode.IntrospectionTimeout >= sqlnode.DefaultLimits().Timeout {
		t.Fatalf("introspection timeout %s is not below the statement default %s",
			sqlnode.IntrospectionTimeout, sqlnode.DefaultLimits().Timeout)
	}

	// A target that accepts a connection and never answers yields within it.
	credential := &loadoptions.ResolvedCredential{
		Record: credentials.Record{ID: "cred-db", Name: "Silent", Type: "postgres"},
		Fields: map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Nanosecond)
	if err := loadoptions.RegisterSQL(resolver, sqlnode.Guard{}); err != nil {
		t.Fatalf("RegisterSQL() error = %v", err)
	}
	started := time.Now()
	if _, err := resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.SQLSchemasLoader, CredentialType: "postgres"},
		loadoptions.Scope{TenantID: "tenant-a"}, credential.Record.ID, fixedCredential{credential}); err == nil {
		t.Fatal("a refused port answered with a schema list")
	}
	if elapsed := time.Since(started); elapsed > sqlnode.DefaultLimits().Timeout {
		t.Errorf("the loader took %s, which is not a bounded failure", elapsed)
	}
}

// fixedCredential resolves exactly one credential, which is what a
// tenant-scoped store does for anything outside the tenant.
type fixedCredential struct {
	credential *loadoptions.ResolvedCredential
}

func (fixed fixedCredential) Resolve(_ context.Context, id string) (credentials.Record, map[string]string, error) {
	if fixed.credential == nil || id != fixed.credential.Record.ID {
		return credentials.Record{}, nil, os.ErrNotExist
	}
	return fixed.credential.Record, fixed.credential.Fields, nil
}

func hasValue(options []loadoptions.Option, want string) bool {
	for _, option := range options {
		if option.Value == want {
			return true
		}
	}
	return false
}
