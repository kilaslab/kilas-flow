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
		Type:        nodeType,
		Version:     workflow.V(1),
		DisplayName: displayName,
		Description: "Runs SQL against a " + displayName + " database you configure with a credential.",
		Category:    "Database",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertySelect, Required: true, Default: sqlOperationQuery,
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
				Description: "JSON array of statements, run in order inside one transaction.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: sqlOperationTransaction}},
			},
			{
				Key: "parameters", Label: "Parameters", Kind: node.PropertyString,
				Description: "JSON array bound to the statement's placeholders, in order. Supports expressions.",
			},
			{Key: "timeoutSeconds", Label: "Statement timeout (seconds)", Kind: node.PropertyNumber, Default: 30},
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
		default:
			return fmt.Errorf("operation %q is not supported", operation)
		}

		// A database node without a credential has nothing to connect to, and
		// there is deliberately no fallback connection it could use instead.
		id, found := n.Credentials[credentialType]
		if !found || strings.TrimSpace(id) == "" {
			return fmt.Errorf("a %s credential is required", credentialType)
		}
		return nil
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
}

// NewDatabaseExecutor builds one database executor.
//
// The guard carries KilasFlow's own database paths so a SQLite credential can
// be refused before a connection is ever opened.
func NewDatabaseExecutor(driver sqlnode.Driver, credentialType string, guard sqlnode.Guard) *DatabaseExecutor {
	return &DatabaseExecutor{driver: driver, credentialType: credentialType, guard: guard}
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
		return nil, fmt.Errorf("node %q: %s connection failed: %w", ir.Name, executor.driver, sanitize(err))
	}
	// Closed on every path, including a failed statement, so a node error never
	// leaks a connection.
	defer connection.Close()

	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		produced, err := executor.runOne(ctx, ir, connection, parameters)
		if err != nil {
			return nil, err
		}
		out = append(out, produced...)
	}
	return workflow.NodeOutput{out}, nil
}

func (executor *DatabaseExecutor) runOne(ctx context.Context, ir workflow.IRNode, connection *sqlnode.Connection, parameters map[string]any) ([]workflow.Item, error) {
	limits := sqlnode.Limits{
		Timeout: time.Duration(numberValue(parameters["timeoutSeconds"]) * float64(time.Second)),
		MaxRows: int(numberValue(parameters["maxRows"])),
	}
	bound, err := boundParameters(parameters["parameters"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	switch operation := textValue(parameters["operation"], sqlOperationQuery); operation {
	case sqlOperationQuery:
		result, err := connection.Query(ctx, textValue(parameters["statement"], ""), bound, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sanitize(err))
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
		return items, nil

	case sqlOperationExecute:
		result, err := connection.Execute(ctx, textValue(parameters["executeStatement"], ""), bound, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sanitize(err))
		}
		return []workflow.Item{{JSON: map[string]any{"rowsAffected": float64(result.RowsAffected)}}}, nil

	case sqlOperationTransaction:
		statements, err := transactionStatements(parameters["statements"])
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		results, err := connection.Transaction(ctx, statements, limits)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, sanitize(err))
		}
		affected := int64(0)
		for _, result := range results {
			affected += result.RowsAffected
		}
		return []workflow.Item{{JSON: map[string]any{
			"rowsAffected": float64(affected),
			"statements":   float64(len(results)),
			"committed":    true,
		}}}, nil

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
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("statements must be a JSON array of {sql, parameters}")
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("a transaction needs at least one statement")
	}
	statements := make([]sqlnode.Statement, 0, len(decoded))
	for _, entry := range decoded {
		if strings.TrimSpace(entry.SQL) == "" {
			return nil, fmt.Errorf("every transaction statement needs sql")
		}
		statements = append(statements, sqlnode.Statement{SQL: entry.SQL, Parameters: entry.Parameters})
	}
	return statements, nil
}

// sanitize strips credential material a driver may have embedded in its error.
//
// Postgres and MySQL both echo the DSN on a connection failure, and that DSN
// carries the password the credential store just decrypted.
func sanitize(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, scheme := range []string{"postgres://", "postgresql://", "mysql://"} {
		message = redactURLCredentials(message, scheme)
	}
	// MySQL DSNs are user:password@tcp(...), which carries no scheme.
	if at := strings.Index(message, "@tcp("); at >= 0 {
		if start := strings.LastIndexAny(message[:at], " \t\"'"); start >= 0 {
			message = message[:start+1] + "[redacted]" + message[at:]
		} else {
			message = "[redacted]" + message[at:]
		}
	}
	return fmt.Errorf("%s", message)
}

func redactURLCredentials(message, scheme string) string {
	for {
		start := strings.Index(message, scheme)
		if start < 0 {
			return message
		}
		rest := message[start+len(scheme):]
		at := strings.Index(rest, "@")
		if at < 0 {
			return message
		}
		if space := strings.IndexAny(rest[:at], " \t"); space >= 0 {
			return message
		}
		message = message[:start+len(scheme)] + "[redacted]" + rest[at:]
	}
}
