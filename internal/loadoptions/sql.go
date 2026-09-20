package loadoptions

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// The five loaders a database node's pickers are built from.
//
// Named as data rather than as functions, so a node declares which one it wants
// and this package decides what that means. A loader a node could name by
// function would be a function the request could eventually name.
const (
	SQLSchemasLoader = "sql.schemas"
	SQLTablesLoader  = "sql.tables"
	// SQLColumnsLoader lists every column, for a picker that names one.
	SQLColumnsLoader = "sql.columns"
	// SQLMatchingColumnsLoader lists only the columns that identify a row.
	SQLMatchingColumnsLoader = "sql.matchingColumns"
	// SQLMappingColumnsLoader is the schema loader behind a resource mapper.
	SQLMappingColumnsLoader = "sql.mappingColumns"
)

// The parameter keys these loaders read as dependencies.
const (
	SQLSchemaDependency = "schema"
	SQLTableDependency  = "table"
)

// RegisterSQL installs the database introspection loaders.
//
// The guard is the same one the executors receive, so a SQLite credential
// naming KilasFlow's own database is refused here too. Introspection is the
// first read this product performs against a customer's database *outside* a
// run, on behalf of whoever holds the editor rather than of a workflow, so
// every defence a run gets has to be present here rather than assumed.
func RegisterSQL(resolver *Resolver, guard sqlnode.Guard) error {
	if resolver == nil {
		return fmt.Errorf("an options resolver is required")
	}
	for name, loader := range map[string]InternalLoader{
		SQLSchemasLoader:         sqlSchemas(guard),
		SQLTablesLoader:          sqlTables(guard),
		SQLColumnsLoader:         sqlColumns(guard, false),
		SQLMatchingColumnsLoader: sqlColumns(guard, true),
	} {
		if err := resolver.RegisterInternal(name, loader); err != nil {
			return err
		}
	}
	return resolver.RegisterSchema(SQLMappingColumnsLoader, sqlMappingColumns(guard))
}

// schemaFor decides which schema a loader is asking about.
//
// A MySQL node declares no schema field, because MySQL has none separate from
// databases — so the credential's own database is the schema, and asking the
// user to choose it again would be asking them to repeat the credential. A
// PostgreSQL node does declare one, and until it is chosen there is nothing to
// list.
//
// The error is a reason rather than a failure: "choose a schema first" is a
// state the form is in, not a fault.
func schemaFor(scope Scope) (string, error) {
	if schema := strings.TrimSpace(scope.Dependencies[SQLSchemaDependency]); schema != "" {
		return schema, nil
	}
	if scope.Credential != nil && scope.Credential.Record.Type == "mysql" {
		if database := strings.TrimSpace(scope.Credential.Fields["database"]); database != "" {
			return database, nil
		}
		return "", fmt.Errorf("this credential names no database, so there are no tables to list")
	}
	return "", fmt.Errorf("choose a schema first")
}

// openFor builds a connection from the credential the caller resolved.
func openFor(ctx context.Context, scope Scope, guard sqlnode.Guard) (*sqlnode.Connection, error) {
	if scope.Credential == nil {
		return nil, fmt.Errorf("this field needs a database credential before it can be filled in")
	}
	driver, known := sqlnode.DriverForCredential(scope.Credential.Record.Type)
	if !known {
		return nil, fmt.Errorf("%q is not a database credential", scope.Credential.Record.Name)
	}
	connection, err := sqlnode.Open(ctx, driver, scope.Credential.Fields, guard)
	if err != nil {
		// The DSN carries the password the credential store just decrypted, and
		// both drivers echo it on a connection failure.
		return nil, sqlnode.Sanitize(err)
	}
	return connection, nil
}

func sqlSchemas(guard sqlnode.Guard) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		connection, err := openFor(ctx, scope, guard)
		if err != nil {
			return Result{}, err
		}
		defer connection.Close()

		names, err := connection.Schemas(ctx)
		if err != nil {
			return Result{}, err
		}
		options := make([]Option, 0, len(names))
		for _, name := range names {
			options = append(options, Option{Label: name, Value: name})
		}
		return Result{Options: options, Reason: privilegeNotice(len(options), "schemas")}, nil
	}
}

func sqlTables(guard sqlnode.Guard) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		schema, err := schemaFor(scope)
		if err != nil {
			return Result{Options: []Option{}, Reason: err.Error()}, nil
		}
		connection, err := openFor(ctx, scope, guard)
		if err != nil {
			return Result{}, err
		}
		defer connection.Close()

		tables, err := connection.Tables(ctx, schema)
		if err != nil {
			return Result{}, err
		}
		options := make([]Option, 0, len(tables))
		for _, table := range tables {
			label := table.Name
			if table.Kind == "view" {
				// Said in the label rather than filtered out: a view is a
				// perfectly good thing to select from, and hiding it would
				// leave the author hunting for something they know exists.
				label += " (view)"
			}
			options = append(options, Option{Label: label, Value: table.Name})
		}
		return Result{Options: options, Reason: privilegeNotice(len(options), "tables")}, nil
	}
}

// sqlColumns lists a table's columns, optionally only the ones that identify a
// row.
func sqlColumns(guard sqlnode.Guard, matchingOnly bool) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		schema, err := schemaFor(scope)
		if err != nil {
			return Result{Options: []Option{}, Reason: err.Error()}, nil
		}
		table := scope.Dependencies[SQLTableDependency]
		if strings.TrimSpace(table) == "" {
			return Result{Options: []Option{}, Reason: "choose a table first"}, nil
		}
		connection, err := openFor(ctx, scope, guard)
		if err != nil {
			return Result{}, err
		}
		defer connection.Close()

		columns, err := connection.Columns(ctx, schema, table)
		if err != nil {
			return Result{}, err
		}
		options := make([]Option, 0, len(columns))
		for _, column := range columns {
			if matchingOnly && !column.PrimaryKey {
				continue
			}
			options = append(options, Option{Label: columnLabel(column), Value: column.Name})
		}
		if matchingOnly && len(options) == 0 && len(columns) > 0 {
			// The table exists and has no primary key. That is a real answer —
			// and a different one from a table nobody can see.
			return Result{Options: options, Reason: "this table has no primary key, so a matching column has to be chosen by hand"}, nil
		}
		return Result{Options: options, Reason: privilegeNotice(len(options), "columns")}, nil
	}
}

// sqlMappingColumns is the schema behind a resource mapper.
func sqlMappingColumns(guard sqlnode.Guard) SchemaLoader {
	return func(ctx context.Context, scope Scope) (property.MapperSchema, error) {
		schema, err := schemaFor(scope)
		if err != nil {
			return property.MapperSchema{Reason: err.Error()}, nil
		}
		table := scope.Dependencies[SQLTableDependency]
		if strings.TrimSpace(table) == "" {
			return property.MapperSchema{Reason: "choose a table first"}, nil
		}
		connection, err := openFor(ctx, scope, guard)
		if err != nil {
			return property.MapperSchema{}, err
		}
		defer connection.Close()

		columns, err := connection.Columns(ctx, schema, table)
		if err != nil {
			return property.MapperSchema{}, err
		}
		fields := make([]property.MapperField, 0, len(columns))
		for _, column := range columns {
			fields = append(fields, property.MapperField{
				ID: column.Name, DisplayName: column.Name,
				Type: mapperType(column.DataType),
				// A column is required when the database will refuse the row
				// without it: not nullable, no default, and not something the
				// database fills in itself.
				Required:         !column.Nullable && column.Default == "" && !column.Generated,
				CanBeUsedToMatch: column.PrimaryKey,
				DefaultMatch:     column.PrimaryKey,
				ReadOnly:         column.Generated,
			})
		}
		return property.MapperSchema{Fields: fields, Reason: privilegeNotice(len(fields), "columns")}, nil
	}
}

// columnLabel shows a column's type inline, the way n8n's own column picker
// does — `id (integer)` tells the author what a value has to look like without
// a second lookup.
func columnLabel(column sqlnode.Column) string {
	label := column.Name
	if column.DataType != "" {
		label += " (" + column.DataType + ")"
	}
	if column.PrimaryKey {
		label += " — key"
	}
	return label
}

// mapperType maps a database type onto the closed set the mapper's controls
// know.
//
// An unrecognised type is returned **as written** rather than mapped to string.
// The editor renders it as text with a warning either way, and keeping the real
// name is what lets the warning say which type it was — a mapper that silently
// called every enum a string would look right and be wrong.
func mapperType(dataType string) string {
	normalized := strings.ToLower(strings.TrimSpace(dataType))
	// An array is spelled `_int4` on PostgreSQL, whose leading underscore is
	// the only marker there is.
	if strings.HasPrefix(normalized, "_") {
		return "array"
	}
	for prefix, kind := range map[string]string{
		"int": "number", "serial": "number", "numeric": "number", "decimal": "number",
		"float": "number", "double": "number", "real": "number", "money": "number",
		"bool": "boolean",
		"date": "dateTime", "time": "dateTime",
		"json": "object",
		"char": "string", "text": "string", "varchar": "string", "uuid": "string",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return kind
		}
	}
	if strings.HasPrefix(normalized, "enum") || strings.HasPrefix(normalized, "set") {
		// An enum's values are in the type on MySQL and behind a catalogue
		// lookup on PostgreSQL. Typed as a string until a ticket needs the
		// values, which is a smaller lie than calling it unknown.
		return "string"
	}
	return dataType
}

// privilegeNotice explains an empty list.
//
// information_schema is privilege filtered, so a role with no privilege on a
// table simply sees no row for it. Without this, a narrow grant is
// indistinguishable from an empty database, and the author's next hour goes to
// the wrong question.
func privilegeNotice(count int, what string) string {
	if count > 0 {
		return ""
	}
	return fmt.Sprintf("this connection succeeded and returned no %s; the role may have no privileges on them", what)
}
