package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/sqlbuild"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// PostgresV2 is the n8n operation set.
//
// The values are n8n's, verbatim, because the value string is the contract: an
// imported node's `operation` either lands on a shape that runs or it does not,
// and no amount of matching labels helps if the strings differ.
const (
	PostgresOperationDeleteTable  = "deleteTable"
	PostgresOperationExecuteQuery = "executeQuery"
	PostgresOperationInsert       = "insert"
	PostgresOperationUpsert       = "upsert"
	PostgresOperationSelect       = "select"
	PostgresOperationUpdate       = "update"
)

// PostgresV2ExecutorID binds the operation set's executor.
const PostgresV2ExecutorID = "core.postgres.v2"

// PostgresV2Version is the version the operation set registers at.
//
// Two, not 2.7. The registry resolves the highest version at or below the one a
// document asks for, so every imported node — which keeps n8n's own typeVersion
// of 2.4 or higher — lands here, and the number does not have to chase n8n's.
var PostgresV2Version = workflow.V(2)

// postgresV2Node is the PostgreSQL node at n8n's operation set.
//
// A second version rather than a rewrite of v1. A workflow authored against
// query/execute/transaction keeps running unchanged, because v1 stays
// registered and frozen — it never gains a feature and is never rewritten
// underneath somebody.
func postgresV2Node() node.Definition {
	shownFor := func(operations ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(operations))
		for _, operation := range operations {
			conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
		}
		return conditions
	}
	locator := func(key, label, loader string, dependsOn ...string) node.PropertyDefinition {
		return node.PropertyDefinition{
			Key: key, Label: label, Kind: node.PropertyResourceLocator, Required: true,
			Modes: []node.PropertyMode{
				{
					Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
					LoadOptions: &node.OptionsLoader{
						Source: property.LoaderInternal, Name: loader,
						CredentialType: "postgres", DependsOn: dependsOn,
					},
				},
				{Name: "name", Label: "By Name", Kind: node.PropertyString, Placeholder: "public"},
			},
			VisibleWhen: shownFor(PostgresOperationDeleteTable, PostgresOperationInsert,
				PostgresOperationUpsert, PostgresOperationSelect, PostgresOperationUpdate),
		}
	}

	return node.Definition{
		Type:        PostgresNodeType,
		Version:     PostgresV2Version,
		Credentials: []node.CredentialRequirement{{Type: "postgres", Required: true}},
		DisplayName: "PostgreSQL",
		Description: "Selects, inserts, updates and deletes rows, or runs SQL you write.",
		Category:    "Database",
		Group:       []node.NodeGroup{node.GroupInput},
		Icon:        &node.NodeIcon{Light: "builtin:database"},
		IconColor:   "#0284c7",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true,
				Default: PostgresOperationExecuteQuery,
				Options: []node.PropertyOption{
					{Label: "Delete", Value: PostgresOperationDeleteTable},
					{Label: "Execute Query", Value: PostgresOperationExecuteQuery},
					{Label: "Insert", Value: PostgresOperationInsert},
					{Label: "Insert or Update", Value: PostgresOperationUpsert},
					{Label: "Select", Value: PostgresOperationSelect},
					{Label: "Update", Value: PostgresOperationUpdate},
				},
			},
			{
				Key: "query", Label: "Query", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 5},
				Description: "Consider using query parameters to prevent SQL injection attacks. " +
					"Put the values in Query Parameters and reference them as $1, $2 and so on.",
				VisibleWhen: shownFor(PostgresOperationExecuteQuery),
			},
			{
				Key: "queryParameters", Label: "Query Parameters", Kind: node.PropertyString,
				Description: "JSON array bound to the query's placeholders, in order. Supports expressions.",
				VisibleWhen: shownFor(PostgresOperationExecuteQuery),
			},
			locator("schema", "Schema", loadoptions.SQLSchemasLoader),
			locator("table", "Table", loadoptions.SQLTablesLoader, "schema"),
			{
				Key: "columns", Label: "Columns", Kind: node.PropertyResourceMapper,
				Mapper: &node.ResourceMapperDeclaration{
					Schema: &node.OptionsLoader{
						Source: property.LoaderInternal, Name: loadoptions.SQLMappingColumnsLoader,
						CredentialType: "postgres", DependsOn: []string{"schema", "table"},
					},
					SupportsAutoMap: true,
					ValuesLabel:     "Values to Send",
				},
				VisibleWhen: shownFor(PostgresOperationInsert, PostgresOperationUpsert, PostgresOperationUpdate),
			},
			{
				Key: "deleteCommand", Label: "Command", Kind: node.PropertyOptions, Default: sqlbuild.DeleteRows,
				Options: []node.PropertyOption{
					{Label: "Delete rows that match", Value: sqlbuild.DeleteRows},
					{Label: "Truncate — empty the table and keep it", Value: sqlbuild.DeleteTruncate},
					{Label: "Drop — remove the table itself", Value: sqlbuild.DeleteDrop},
				},
				VisibleWhen: shownFor(PostgresOperationDeleteTable),
			},
			{
				Key: "where", Label: "Conditions", Kind: node.PropertyConditions,
				Description: "Which rows this acts on. Deleting rows needs at least one; " +
					"use the truncate command to empty the whole table.",
				VisibleWhen: shownFor(PostgresOperationSelect, PostgresOperationDeleteTable),
			},
			{
				Key: "combineConditions", Label: "Combine conditions", Kind: node.PropertyOptions, Default: "AND",
				Options: []node.PropertyOption{
					{Label: "All conditions must match", Value: "AND"},
					{Label: "Any condition may match", Value: "OR"},
				},
				VisibleWhen: shownFor(PostgresOperationSelect, PostgresOperationDeleteTable),
			},
			{
				Key: "returnAll", Label: "Return all", Kind: node.PropertyBoolean, Default: false,
				VisibleWhen: shownFor(PostgresOperationSelect),
			},
			{
				Key: "limit", Label: "Limit", Kind: node.PropertyNumber, Default: 50,
				DisplayOptions: node.Visibility{
					Show: []node.Condition{{Key: "operation", Values: []any{PostgresOperationSelect}}},
					Hide: []node.Condition{{Key: "returnAll", Values: []any{true}}},
				},
			},
			{
				Key: "statementTimeoutSeconds", Label: "Statement timeout (seconds)",
				Kind: node.PropertyNumber, Default: 30,
			},
			{Key: "maxRows", Label: "Maximum rows", Kind: node.PropertyNumber, Default: 10000},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     PostgresV2ExecutorID,
		Validate:       validatePostgresV2Configuration,
	}
}

// v1Operations are the values version 1 offered.
//
// Named so the v2 validator can recognise a document written for v1 and say so.
var v1Operations = map[string]bool{
	sqlOperationQuery: true, sqlOperationExecute: true, sqlOperationTransaction: true,
}

func validatePostgresV2Configuration(n workflow.Node) error {
	operation := textParameter(n.Parameters, "operation")
	if operation == "" {
		operation = PostgresOperationExecuteQuery
	}

	// The silent flip this version could have caused, made loud. The registry
	// resolves the highest version at or below the one a document asks for, so
	// the moment v2 registered, every stored node carrying n8n's typeVersion of
	// 2.4 or higher stopped resolving to v1 — while still holding parameters
	// written for v1. Without this the first sign would be a run-time error on
	// an unknown operation, with nothing firing at save, at activation, or in
	// the editor.
	if v1Operations[operation] {
		return fmt.Errorf("this node is configured for PostgreSQL version 1, whose operations are query, "+
			"execute and transaction; version 2 uses n8n's set. Either pin the node to typeVersion 1 or "+
			"change the operation to %q and move the SQL into Query", PostgresOperationExecuteQuery)
	}

	switch operation {
	case PostgresOperationExecuteQuery:
		if statementText(n.Parameters, "query") == "" {
			return fmt.Errorf("an execute query needs a query")
		}
	case PostgresOperationDeleteTable, PostgresOperationInsert, PostgresOperationUpsert,
		PostgresOperationSelect, PostgresOperationUpdate:
		if !property.LocatorIsSet(n.Parameters["table"]) {
			return fmt.Errorf("%s needs a table", operation)
		}
	default:
		return fmt.Errorf("operation %q is not supported", operation)
	}
	return nil
}

// SQLOperationExecutor runs the operation set against one dialect.
//
// One executor for both databases rather than two: the operations, the item
// loop, the credential check and the output shape are identical, and only the
// SQL differs — which is exactly what the dialect carries. Two copies would
// have the second one drift on the parts that are not about SQL at all.
type SQLOperationExecutor struct {
	driver         sqlnode.Driver
	credentialType string
	dialect        sqlbuild.Dialect
	guard          sqlnode.Guard
	ceiling        sqlnode.Ceiling
}

// NewSQLOperationExecutor builds the operation set's executor for one dialect.
func NewSQLOperationExecutor(driver sqlnode.Driver, credentialType string, dialect sqlbuild.Dialect, guard sqlnode.Guard, ceiling sqlnode.Ceiling) *SQLOperationExecutor {
	return &SQLOperationExecutor{
		driver: driver, credentialType: credentialType, dialect: dialect,
		guard: guard, ceiling: ceiling,
	}
}

// NewPostgresV2Executor builds the PostgreSQL operation set's executor.
func NewPostgresV2Executor(guard sqlnode.Guard, ceiling sqlnode.Ceiling) *SQLOperationExecutor {
	return NewSQLOperationExecutor(sqlnode.DriverPostgres, "postgres", sqlbuild.Postgres, guard, ceiling)
}

// NewMySQLV2Executor builds the MySQL operation set's executor.
func NewMySQLV2Executor(guard sqlnode.Guard, ceiling sqlnode.Ceiling) *SQLOperationExecutor {
	return NewSQLOperationExecutor(sqlnode.DriverMySQL, "mysql", sqlbuild.MySQL, guard, ceiling)
}

// Execute builds each item's statement and runs them as one batch.
//
// Built here rather than inside the per-item loop the executor used to have:
// one statement per item on its own connection is the serial round trip the
// batching work already unwound, and an operation set makes it N times worse
// because now the statement itself is being constructed too.
func (executor *SQLOperationExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	credentialID := strings.TrimSpace(ir.Credentials[executor.credentialType])
	if credentialID == "" {
		return nil, fmt.Errorf("node %q: a %s credential is required", ir.Name, executor.credentialType)
	}
	if request.Credentials == nil {
		return nil, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if resolved.Type != executor.credentialType {
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, not %s",
			ir.Name, resolved.Name, resolved.Type, executor.credentialType)
	}

	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	statements := make([]sqlnode.Statement, 0, len(items))
	var limits sqlnode.Limits
	var clamped map[string]any
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if index == 0 {
			limits, clamped = executor.ceiling.Apply(sqlnode.Limits{
				Timeout: time.Duration(timeoutParameter(parameters, "statementTimeoutSeconds") * float64(time.Second)),
				MaxRows: int(numberValue(parameters["maxRows"])),
			})
		}
		statement, err := buildSQLStatement(executor.dialect, parameters, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		statements = append(statements, statement)
	}

	connection, err := sqlnode.Open(ctx, executor.driver, resolved.Fields, executor.guard)
	if err != nil {
		return nil, fmt.Errorf("node %q: %s connection failed: %w", ir.Name, executor.driver, sqlnode.Sanitize(err))
	}
	defer connection.Close()

	results, err := connection.Transaction(ctx, statements, limits)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, sqlnode.Sanitize(err))
	}
	out := make([]workflow.Item, 0, len(results))
	for _, result := range results {
		for _, row := range result.Rows {
			out = append(out, workflow.Item{JSON: row})
		}
		if len(result.Rows) > 0 {
			continue
		}
		summary := map[string]any{"rowsAffected": float64(result.RowsAffected)}
		// MySQL has no RETURNING, so an insert's generated key comes back on
		// the driver's own OK packet for that statement rather than from the
		// row. Reported only when the driver reported one: a real
		// auto-increment is never zero, and SELECT LAST_INSERT_ID() would give
		// the *previous* statement's id for a table without one — plausible,
		// wrong, and silent.
		if result.LastInsertID != 0 {
			summary["insertId"] = float64(result.LastInsertID)
		}
		out = append(out, workflow.Item{JSON: summary})
	}
	return workflow.NodeOutput{appendClamped(out, clamped)}, nil
}

// BuildSQLStatementForTest builds one statement from resolved parameters.
//
// Exported for tests only. What it exists for is the class of defect a round
// trip cannot see: the importer and the exporter map the condition vocabulary
// through inverse tables, so an inversion between them is symmetric and
// invisible until something asserts the SQL that actually runs.
func BuildSQLStatementForTest(dialect sqlbuild.Dialect, parameters map[string]any) (sqlnode.Statement, error) {
	return buildSQLStatement(dialect, parameters, workflow.Item{JSON: map[string]any{}})
}

// buildSQLStatement turns one item's resolved parameters into SQL.
func buildSQLStatement(dialect sqlbuild.Dialect, parameters map[string]any, item workflow.Item) (sqlnode.Statement, error) {
	operation := textValue(parameters["operation"], PostgresOperationExecuteQuery)
	if operation == PostgresOperationExecuteQuery {
		bound, err := boundParameters(parameters["queryParameters"])
		if err != nil {
			return sqlnode.Statement{}, err
		}
		query := textValue(parameters["query"], "")
		if strings.TrimSpace(query) == "" {
			return sqlnode.Statement{}, fmt.Errorf("an execute query needs a query")
		}
		// Returning, because a query the user wrote is usually a read and a
		// read that reported only "rows affected" would be useless. A write
		// through this operation returns its own zero rows, which the executor
		// turns back into a rowsAffected item.
		return sqlnode.Statement{SQL: query, Parameters: bound, Returning: true}, nil
	}

	target := sqlbuild.Target{
		Schema: locatorText(parameters["schema"]),
		Table:  locatorText(parameters["table"]),
	}
	combine := textValue(parameters["combineConditions"], "AND")

	switch operation {
	case PostgresOperationSelect:
		limit := int(numberValue(parameters["limit"]))
		if truthy(parameters["returnAll"]) {
			limit = 0
		}
		where, err := postgresConditions(parameters["where"])
		if err != nil {
			return sqlnode.Statement{}, err
		}
		return sqlbuild.Select(dialect, target, nil, where, combine, nil, limit)

	case PostgresOperationDeleteTable:
		mode := textValue(parameters["deleteCommand"], sqlbuild.DeleteRows)
		where, err := postgresConditions(parameters["where"])
		if err != nil {
			return sqlnode.Statement{}, err
		}
		return sqlbuild.Delete(dialect, target, mode, where, combine)

	case PostgresOperationInsert, PostgresOperationUpdate, PostgresOperationUpsert:
		mapping, ok := property.ReadMapping(parameters["columns"])
		if !ok {
			return sqlnode.Statement{}, fmt.Errorf("%s needs its columns mapped", operation)
		}
		// The live schema is not available here — this runs inside an
		// execution, and re-reading the catalogue per item would be a
		// round trip per row. The stored copy is what the mapping was built
		// against, and the database itself is the authority that refuses a
		// column that no longer exists.
		values, _ := property.MappedColumns(mapping.Schema, mapping, item.JSON)
		if len(values) == 0 {
			return sqlnode.Statement{}, fmt.Errorf("%s has no columns to write", operation)
		}
		switch operation {
		case PostgresOperationInsert:
			return sqlbuild.Insert(dialect, target, values)
		case PostgresOperationUpdate:
			return sqlbuild.Update(dialect, target, values, mapping.MatchingColumns)
		default:
			return sqlbuild.Upsert(dialect, target, values, mapping.MatchingColumns)
		}

	default:
		return sqlnode.Statement{}, fmt.Errorf("operation %q is not supported", operation)
	}
}

// postgresConditions reads the WHERE builder's rows.
func postgresConditions(value any) ([]sqlbuild.Comparison, error) {
	rows, ok := value.([]any)
	if !ok {
		return nil, nil
	}
	comparisons := make([]sqlbuild.Comparison, 0, len(rows))
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		column := textValue(row["field"], "")
		if strings.TrimSpace(column) == "" {
			continue
		}
		comparisons = append(comparisons, sqlbuild.Comparison{
			Column:   column,
			Operator: postgresOperator(textValue(row["operator"], "equals")),
			Value:    row["value"],
		})
	}
	return comparisons, nil
}

// postgresOperator maps the shared condition vocabulary onto the builder's.
func postgresOperator(name string) string {
	switch name {
	case "notEquals":
		return "notEquals"
	case "exists":
		return "isNotNull"
	case "notExists":
		return "isNull"
	default:
		return name
	}
}

// locatorText reads a resource locator's value as text.
func locatorText(value any) string {
	locator, ok := property.ReadLocator(value)
	if !ok {
		return ""
	}
	return textValue(locator.Value, "")
}

func truthy(value any) bool {
	flag, _ := value.(bool)
	return flag
}
