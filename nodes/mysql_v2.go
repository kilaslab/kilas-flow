package nodes

import (
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/sqlbuild"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// MySQLV2ExecutorID binds the MySQL operation set's executor.
const MySQLV2ExecutorID = "core.mysql.v2"

// MySQLV2Version is the version the operation set registers at.
var MySQLV2Version = workflow.V(2)

// mysqlV2Node is the MySQL node at n8n's operation set.
//
// The same six operation values as PostgreSQL — verified against n8n 2.34.0's
// own Database.resource.ts, which lists deleteTable, executeQuery, insert,
// upsert, select and update — so the constants are shared rather than declared
// twice. What differs is the default operation, which is `insert` there and
// `executeQuery` on the PostgreSQL node, and the absence of a schema.
//
// **There is no schema locator, deliberately.** MySQL has no schemas separate
// from databases, so `a`.`b` names database a's table b: a schema field would
// either address the wrong database or be ignored, and n8n's own MySQL node
// declares only a table for exactly that reason.
func mysqlV2Node() node.Definition {
	shownFor := func(operations ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(operations))
		for _, operation := range operations {
			conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
		}
		return conditions
	}
	return node.Definition{
		Type:        MySQLNodeType,
		Version:     MySQLV2Version,
		Credentials: []node.CredentialRequirement{{Type: "mysql", Required: true}},
		DisplayName: "MySQL",
		Description: "Selects, inserts, updates and deletes rows, or runs SQL you write. Works with MariaDB too.",
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
				// n8n's MySQL node defaults to insert where its PostgreSQL node
				// defaults to executeQuery. Matched rather than harmonised: an
				// imported node that names no operation gets the one n8n would
				// have given it.
				Default: PostgresOperationInsert,
				Options: []node.PropertyOption{
					{Label: "Delete", Value: PostgresOperationDeleteTable},
					{Label: "Execute SQL", Value: PostgresOperationExecuteQuery},
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
					"Put the values in Query Parameters and reference them as ? in order.",
				VisibleWhen: shownFor(PostgresOperationExecuteQuery),
			},
			{
				Key: "queryParameters", Label: "Query Parameters", Kind: node.PropertyString,
				Description: "JSON array bound to the query's placeholders, in order. Supports expressions.",
				VisibleWhen: shownFor(PostgresOperationExecuteQuery),
			},
			{
				Key: "table", Label: "Table", Kind: node.PropertyResourceLocator, Required: true,
				Description: "The table to work on, in the database the credential names.",
				Modes: []node.PropertyMode{
					{
						Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
						LoadOptions: &node.OptionsLoader{
							Source: property.LoaderInternal, Name: loadoptions.SQLTablesLoader,
							CredentialType: "mysql",
						},
					},
					{Name: "name", Label: "By Name", Kind: node.PropertyString, Placeholder: "table_name"},
				},
				VisibleWhen: shownFor(PostgresOperationDeleteTable, PostgresOperationInsert,
					PostgresOperationUpsert, PostgresOperationSelect, PostgresOperationUpdate),
			},
			{
				Key: "columns", Label: "Columns", Kind: node.PropertyResourceMapper,
				Mapper: &node.ResourceMapperDeclaration{
					Schema: &node.OptionsLoader{
						Source: property.LoaderInternal, Name: loadoptions.SQLMappingColumnsLoader,
						CredentialType: "mysql", DependsOn: []string{"table"},
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
				Description: "Truncate and drop are DDL, which MySQL commits implicitly: inside a run over " +
					"several items they commit whatever ran before them and cannot be rolled back.",
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
		ExecutorID:     MySQLV2ExecutorID,
		Validate:       validateMySQLV2Configuration,
	}
}

func validateMySQLV2Configuration(n workflow.Node) error {
	operation := textParameter(n.Parameters, "operation")
	if operation == "" {
		operation = PostgresOperationInsert
	}
	// The same silent flip the PostgreSQL node guards against, and worse here:
	// every already-imported MySQL node is sitting in storage as
	// {"operation": "query"} with no statement at all, because the old importer
	// flattened every operation but executeQuery and dropped the SQL with it.
	if v1Operations[operation] {
		return fmt.Errorf("this node is configured for MySQL version 1, whose operations are query, "+
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
