package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Executor IDs for the database family.
const (
	PostgresExecutorID = "core.postgres"
	MySQLExecutorID    = "core.mysql"
	SQLiteExecutorID   = "core.sqlite"
)

// Node types for the database family.
const (
	PostgresNodeType = "kilasflow.postgres"
	MySQLNodeType    = "kilasflow.mysql"
	SQLiteNodeType   = "kilasflow.sqlite"
)

// Operations a database node can perform.
const (
	sqlOperationQuery       = "query"
	sqlOperationExecute     = "execute"
	sqlOperationTransaction = "transaction"
)

func databaseNode(nodeType, executorID, displayName, credentialType string) node.Definition {
	return node.Definition{
		Type: nodeType,
		// Each database node accepts exactly its own driver's credential, and
		// requires it: there is no shared or fallback connection to fall back
		// to, which validateDatabaseConfiguration already enforces.
		Credentials: []node.CredentialRequirement{{Type: credentialType, Required: true}},
		Version:     workflow.V(1),
		DisplayName: displayName,
		Description: "Runs SQL against a " + displayName + " database you configure with a credential.",
		Category:    "Database",
		Group:       []node.NodeGroup{node.GroupInput},
		Icon:        &node.NodeIcon{Light: "builtin:database"},
		IconColor:   "#0284c7",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: sqlOperationQuery,
				Options: []node.PropertyOption{
					{Label: "Query (returns rows)", Value: sqlOperationQuery},
					{Label: "Execute (returns rows affected)", Value: sqlOperationExecute},
					{Label: "Transaction (several statements, all or nothing)", Value: sqlOperationTransaction},
				},
			},
			{
				Key: "statement", Label: "SQL", Kind: node.PropertyString, Required: true,
				Description: "One statement. Use placeholders and the Parameters field; never build SQL from an expression.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: sqlOperationQuery}},
			},
			{
				Key: "executeStatement", Label: "SQL", Kind: node.PropertyString,
				Description: "One statement. Use placeholders and the Parameters field.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: sqlOperationExecute}},
			},
			{
				Key: "statements", Label: "Statements", Kind: node.PropertyString,
				Description: "JSON array of {sql, parameters, returning}, run in order inside one transaction. " +
					"Set returning on a statement whose rows you want back, such as INSERT … RETURNING id.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: sqlOperationTransaction}},
			},
			{
				Key: "parameters", Label: "Parameters", Kind: node.PropertyString,
				Description: "JSON array bound to the statement's placeholders, in order. Supports expressions. " +
					"A transaction binds parameters per statement instead, inside Statements.",
			},
			{
				Key: "statementTimeoutSeconds", Label: "Statement timeout (seconds)", Kind: node.PropertyNumber, Default: 30,
				Description: "Bounds one statement. An execute over several input items runs them in one " +
					"transaction, and this bounds each of them rather than the batch.",
			},
			{Key: "maxRows", Label: "Maximum rows", Kind: node.PropertyNumber, Default: 10000},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     executorID,
		Validate:       validateDatabaseConfiguration(credentialType),
	}
}

func postgresNode() node.Definition {
	return databaseNode(PostgresNodeType, PostgresExecutorID, "PostgreSQL", "postgres")
}

func mysqlNode() node.Definition {
	return databaseNode(MySQLNodeType, MySQLExecutorID, "MySQL", "mysql")
}

func sqliteNode() node.Definition {
	return databaseNode(SQLiteNodeType, SQLiteExecutorID, "SQLite", "sqlite")
}

// DatabaseCredentialType maps a database node type to the credential type it
// accepts, so the editor and the executor agree without duplicating the list.
func DatabaseCredentialType(nodeType string) (string, bool) {
	switch nodeType {
	case PostgresNodeType:
		return "postgres", true
	case MySQLNodeType:
		return "mysql", true
	case SQLiteNodeType:
		return "sqlite", true
	default:
		return "", false
	}
}

func validateDatabaseConfiguration(credentialType string) workflow.ConfigValidator {
	return func(n workflow.Node) error {
		operation := textParameter(n.Parameters, "operation")
		if operation == "" {
			operation = sqlOperationQuery
		}
		switch operation {
		case sqlOperationQuery:
			if statementText(n.Parameters, "statement") == "" {
				return fmt.Errorf("a query needs a statement")
			}
		case sqlOperationExecute:
			if statementText(n.Parameters, "executeStatement") == "" {
				return fmt.Errorf("an execute needs a statement")
			}
		case sqlOperationTransaction:
			if statementText(n.Parameters, "statements") == "" {
				return fmt.Errorf("a transaction needs statements")
			}
			// Refused rather than ignored. A transaction binds parameters per
			// statement, so a list set here has no statement to belong to —
			// and it was already being decoded, which meant a malformed one
			// failed a transaction that would otherwise never have read it.
			//
			// The field cannot simply be hidden for this operation:
			// VisibilityCondition is single-key equality AND-ed together, so
			// "shown unless transaction" is not expressible, and forking it
			// into queryParameters and executeParameters would rewrite every
			// stored document for a cosmetic gain.
			if parametersConfigured(n.Parameters["parameters"]) {
				return fmt.Errorf("a transaction binds parameters per statement: put them in each statement's %q field inside Statements, not in the node's Parameters field", "parameters")
			}
		default:
			return fmt.Errorf("operation %q is not supported", operation)
		}
		// The credential is not checked here. The definition declares it
		// required and the compiler enforces that for every node, so a second
		// check would report the same thing twice with two different wordings.
		return nil
	}
}

// parametersConfigured reports whether the node-level parameters field carries
// anything.
//
// It accepts every shape boundParameters does, and treats an expression marker
// as configured: the editor stores this field as a string, but a document
// posted to the API can hold a real JSON array, and an expression that resolves
// to one is still parameters the transaction has nowhere to put.
func parametersConfigured(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	default:
		return true
	}
}

// statementText reads a statement parameter, treating an expression marker as
// present-but-unknown. A statement built from an expression is still bound
// rather than interpolated at run time.
func statementText(parameters map[string]any, key string) string {
	if expression.IsExpression(parameters[key]) {
		return "expression"
	}
	value, _ := parameters[key].(string)
	return strings.TrimSpace(value)
}

// DatabaseExecutor runs SQL against a user-configured database.
type DatabaseExecutor struct {
	driver         sqlnode.Driver
	credentialType string
	guard          sqlnode.Guard
	ceiling        sqlnode.Ceiling
}

// NewDatabaseExecutor builds one database executor.
//
// The guard carries KilasFlow's own database paths so a SQLite credential can
// be refused before a connection is ever opened. The ceiling bounds what the
// document may ask for, which the document itself cannot raise.
func NewDatabaseExecutor(driver sqlnode.Driver, credentialType string, guard sqlnode.Guard, ceiling sqlnode.Ceiling) *DatabaseExecutor {
	return &DatabaseExecutor{driver: driver, credentialType: credentialType, guard: guard, ceiling: ceiling}
}

// Execute resolves the credential, opens a connection, and runs the configured
// operation once per incoming item.
func (executor *DatabaseExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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
		return nil, fmt.Errorf("node %q: credential %q is a %s credential, not %s", ir.Name, resolved.Name, resolved.Type, executor.credentialType)
	}

	connection, err := sqlnode.Open(ctx, executor.driver, resolved.Fields, executor.guard)
	if err != nil {
		// The error deliberately does not echo the DSN, which would carry the
		// password the credential store just decrypted.
		return nil, fmt.Errorf("node %q: %s connection failed: %w", ir.Name, executor.driver, sqlnode.Sanitize(err))
	}
	// Closed on every path, including a failed statement, so a node error never
	// leaks a connection.
	defer connection.Close()

	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	// Resolved up front, because whether the run can be batched is a question
	// about every item's operation and cannot be answered one item at a time.
	// Parameters are still resolved per item: an expression reading $json has
	// to see the item it belongs to.
	perItem := make([]map[string]any, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		perItem = append(perItem, parameters)
	}

	if everyItemExecutes(perItem) {
		out, err := executor.runExecuteBatch(ctx, ir, connection, perItem)
		if err != nil {
			return nil, err
		}
		return workflow.NodeOutput{out}, nil
	}

	out := make([]workflow.Item, 0, len(items))
	for _, parameters := range perItem {
		produced, err := executor.runOne(ctx, ir, connection, parameters)
		if err != nil {
			return nil, err
		}
		out = append(out, produced...)
	}
	return workflow.NodeOutput{out}, nil
}

// everyItemExecutes reports whether the whole run is one execute operation.
//
// The operation is a dropdown, so in practice it is the same for every item —
// but it is resolved like any other parameter and an expression could vary it.
// A mixed run falls back to the per-item loop rather than batching part of it:
// a batch that covers some of the items is neither the old behaviour nor
// atomic, and nothing in the output would say which items were in it.
func everyItemExecutes(resolved []map[string]any) bool {
	if len(resolved) == 0 {
		return false
	}
	for _, parameters := range resolved {
		if textValue(parameters["operation"], sqlOperationQuery) != sqlOperationExecute {
			return false
		}
	}
	return true
}

// runExecuteBatch runs an execute over every input item as one transaction.
//
// The output shape is unchanged — one `rowsAffected` item per input item, in
// order — so a downstream node sees exactly the stream it saw before. What
// changes is underneath: one prepared statement instead of one parse per item,
// and all-or-nothing instead of a half-applied write nobody is told about.
//
// The limits come from the first item. A batch is one transaction and one
// prepared statement, so it has one row bound and one deadline per statement;
// a maximum that varied per item inside a shared transaction would be a
// fiction. The first item is the honest choice because it is the one whose
// values an author reading the node would expect to apply.
func (executor *DatabaseExecutor) runExecuteBatch(ctx context.Context, ir workflow.IRNode, connection *sqlnode.Connection, resolved []map[string]any) ([]workflow.Item, error) {
	limits, clamped := executor.limitsFor(resolved[0])

	statements := make([]sqlnode.Statement, 0, len(resolved))
	for index, parameters := range resolved {
		bound, err := boundParameters(parameters["parameters"])
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		statements = append(statements, sqlnode.Statement{
			SQL:        textValue(parameters["executeStatement"], ""),
			Parameters: bound,
		})
	}

	results, err := connection.ExecuteBatch(ctx, statements, limits)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, sqlnode.Sanitize(err))
	}
	out := make([]workflow.Item, 0, len(results)+1)
	for _, result := range results {
		out = append(out, workflow.Item{JSON: map[string]any{"rowsAffected": float64(result.RowsAffected)}})
	}
	return appendClamped(out, clamped), nil
}

// limitsFor reads a run's limits from its parameters and clamps them.
func (executor *DatabaseExecutor) limitsFor(parameters map[string]any) (sqlnode.Limits, map[string]any) {
	return executor.ceiling.Apply(sqlnode.Limits{
		Timeout: time.Duration(timeoutParameter(parameters, "statementTimeoutSeconds") * float64(time.Second)),
		MaxRows: int(numberValue(parameters["maxRows"])),
	})
}

// appendClamped marks an output that ran under a lowered limit.
//
// Marked rather than silent, for the same reason the query path appends
// `$truncated`: a run that quietly read fewer rows than it was asked for is
// indistinguishable from one that read everything.
func appendClamped(items []workflow.Item, clamped map[string]any) []workflow.Item {
	if len(clamped) == 0 {
		return items
	}
	notice := map[string]any{"$clamped": true}
	for key, value := range clamped {
		notice[key] = value
	}
	return append(items, workflow.Item{JSON: notice})
}

func (executor *DatabaseExecutor) runOne(ctx context.Context, ir workflow.IRNode, connection *sqlnode.Connection, parameters map[string]any) ([]workflow.Item, error) {
	limits, clamped := executor.limitsFor(parameters)
	bound, err := boundParameters(parameters["parameters"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	switch operation := textValue(parameters["operation"], sqlOperationQuery); operation {
	case sqlOperationQuery:
		result, err := connection.Query(ctx, textValue(parameters["statement"], ""), bound, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sqlnode.Sanitize(err))
		}
		items := make([]workflow.Item, 0, len(result.Rows))
		for _, row := range result.Rows {
			items = append(items, workflow.Item{JSON: row})
		}
		if result.Truncated {
			// Surfacing truncation as an item keeps a partial read from looking
			// like a complete one.
			items = append(items, workflow.Item{JSON: map[string]any{"$truncated": true, "maxRows": float64(limits.MaxRows)}})
		}
		return appendClamped(items, clamped), nil

	case sqlOperationExecute:
		result, err := connection.Execute(ctx, textValue(parameters["executeStatement"], ""), bound, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sqlnode.Sanitize(err))
		}
		return appendClamped([]workflow.Item{{JSON: map[string]any{"rowsAffected": float64(result.RowsAffected)}}}, clamped), nil

	case sqlOperationTransaction:
		statements, err := transactionStatements(parameters["statements"])
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		results, err := connection.Transaction(ctx, statements, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sqlnode.Sanitize(err))
		}
		affected := int64(0)
		items := make([]workflow.Item, 0, len(results)+1)
		truncated := false
		for _, result := range results {
			affected += result.RowsAffected
			// Rows come first and the summary last, so a statement declared as
			// returning hands its rows to the next node in statement order.
			for _, row := range result.Rows {
				items = append(items, workflow.Item{JSON: row})
			}
			truncated = truncated || result.Truncated
		}
		if truncated {
			items = append(items, workflow.Item{JSON: map[string]any{"$truncated": true, "maxRows": float64(limits.MaxRows)}})
		}
		items = append(items, workflow.Item{JSON: map[string]any{
			"rowsAffected": float64(affected),
			"statements":   float64(len(results)),
			"committed":    true,
		}})
		return appendClamped(items, clamped), nil

	default:
		return nil, fmt.Errorf("node %q: operation %q is not supported", ir.Name, operation)
	}
}

// boundParameters decodes the JSON array bound to a statement's placeholders.
func boundParameters(value any) ([]any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []any:
		return typed, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		var decoded []any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return nil, fmt.Errorf("parameters must be a JSON array")
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("parameters must be a JSON array")
	}
}

// transactionStatements decodes the statement list run inside one transaction.
func transactionStatements(value any) ([]sqlnode.Statement, error) {
	raw, ok := value.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("statements must be a JSON array")
	}
	var decoded []struct {
		SQL        string `json:"sql"`
		Parameters []any  `json:"parameters"`
		// Returning is declared per statement rather than inferred from the
		// SQL, so `INSERT … RETURNING id` gives up its id and a plain INSERT
		// still reports rows affected.
		Returning bool `json:"returning"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("statements must be a JSON array of {sql, parameters, returning}")
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("a transaction needs at least one statement")
	}
	statements := make([]sqlnode.Statement, 0, len(decoded))
	for _, entry := range decoded {
		if strings.TrimSpace(entry.SQL) == "" {
			return nil, fmt.Errorf("every transaction statement needs sql")
		}
		statements = append(statements, sqlnode.Statement{SQL: entry.SQL, Parameters: entry.Parameters, Returning: entry.Returning})
	}
	return statements, nil
}
