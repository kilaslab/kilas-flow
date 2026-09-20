package nodes

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlbuild"
)

// The database nodes' options collection.
//
// The key names, the defaults and the visibility are n8n's, transcribed from
// its own common.descriptions.ts at 2.34.0. Transcribed rather than read:
// internal/guardrails forbids any build input — a test included — from reading
// the reference checkout, so the anti-drift check compares this declaration
// against testdata/n8n_sql_options.json, a committed record of what the
// reference said and when it was read.
const (
	// SQLOptionCascade drops dependent objects along with a table.
	SQLOptionCascade = "cascade"
	// SQLOptionConnectionTimeout bounds the connect, not the statement.
	SQLOptionConnectionTimeout = "connectionTimeout"
	// SQLOptionDelayClosingIdleConnection has no meaning here; see the refusal.
	SQLOptionDelayClosingIdleConnection = "delayClosingIdleConnection"
	SQLOptionQueryBatching              = "queryBatching"
	// SQLOptionQueryReplacement is n8n's name for the bound values this node
	// carries as a top-level Query Parameters field.
	SQLOptionQueryReplacement = "queryReplacement"
	// SQLOptionTreatSingleQuotesAsText belongs to n8n's own replacement
	// substitution, which this node does not perform.
	SQLOptionTreatSingleQuotesAsText = "treatQueryParametersInSingleQuotesAsText"
	SQLOptionOutputColumns           = "outputColumns"
	SQLOptionLargeNumbersOutput      = "largeNumbersOutput"
	SQLOptionSkipOnConflict          = "skipOnConflict"
	SQLOptionReplaceEmptyStrings     = "replaceEmptyStrings"
)

// Query batching modes, using n8n's value strings.
const (
	// BatchingSingle sends every item's statement as one unit and returns one
	// combined result.
	BatchingSingle = "single"
	// BatchingIndependently runs each item on its own and keeps going past a
	// failure, reporting it in that item's place.
	BatchingIndependently = "independently"
	// BatchingTransaction rolls everything back when any item fails.
	BatchingTransaction = "transaction"
)

// Large-number renderings, using n8n's value strings.
const (
	LargeNumbersAsText    = "text"
	LargeNumbersAsNumbers = "numbers"
)

// postgresOptionsCollection is the Options control the PostgreSQL node carries.
func postgresOptionsCollection() node.PropertyDefinition {
	return sqlOptionsCollection(sqlbuild.Postgres)
}

// mysqlOptionsCollection is the same control, less what MySQL cannot do.
func mysqlOptionsCollection() node.PropertyDefinition {
	return sqlOptionsCollection(sqlbuild.MySQL)
}

// sqlOptionsCollection is the Options control both database nodes carry.
//
// Taking the dialect rather than a flag because the one member that is not
// shared is not shared for a reason the dialect already knows: MySQL parses
// CASCADE on DROP TABLE and documents that it does nothing, so offering the
// choice there would take an instruction and not carry it out.
func sqlOptionsCollection(dialect sqlbuild.Dialect) node.PropertyDefinition {
	shownFor := func(operations ...any) node.Visibility {
		return node.Visibility{Show: []node.Condition{{Key: "operation", Values: operations}}}
	}
	fields := make([]node.PropertyDefinition, 0, 10)
	if dialect.DropsCascade() {
		fields = append(fields, node.PropertyDefinition{
			Key: SQLOptionCascade, Label: "Cascade", Kind: node.PropertyBoolean, Default: false,
			Description: "Drop everything that depends on the table — views, sequences — along with it.",
			DisplayOptions: node.Visibility{
				Show: []node.Condition{{Key: "operation", Values: []any{PostgresOperationDeleteTable}}},
				Hide: []node.Condition{{Key: "deleteCommand", Values: []any{sqlbuild.DeleteRows}}},
			},
		})
	}
	fields = append(fields,
		node.PropertyDefinition{
			Key: SQLOptionConnectionTimeout, Label: "Connection Timeout", Kind: node.PropertyNumber, Default: 30,
			Description: "Seconds to wait for the connection itself. This is not the statement timeout, " +
				"which is its own field: a database that answers slowly and one that does not answer at all " +
				"are different problems.",
		},
		node.PropertyDefinition{
			Key: SQLOptionDelayClosingIdleConnection, Label: "Delay Closing Idle Connection",
			Kind: node.PropertyNumber, Default: 0,
			Description: "Not applied. This server opens a connection per node run and closes it when the run " +
				"ends, so there is no idle connection to delay closing — a pool outliving a run would keep " +
				"using a credential that may since have been revoked. The field exists so an imported " +
				"workflow keeps what it held.",
		},
		node.PropertyDefinition{
			Key: SQLOptionQueryBatching, Label: "Query Batching", Kind: node.PropertyOptions,
			Default: BatchingSingle,
			Options: []node.PropertyOption{
				{Label: "Single query", Value: BatchingSingle},
				{Label: "Independently", Value: BatchingIndependently},
				{Label: "Transaction", Value: BatchingTransaction},
			},
			Description: "How every input item's statement reaches the database. Single sends them together " +
				"and returns one result. Independently runs each on its own and carries on past a failure, " +
				"putting the error in that item's place — this is an item-level answer and it wins over the " +
				"node's Continue on fail setting, which decides what happens once the whole node has failed. " +
				"Transaction rolls every item's work back when any one of them fails.",
		},
		node.PropertyDefinition{
			Key: SQLOptionQueryReplacement, Label: "Query Parameters (n8n)", Kind: node.PropertyString,
			Default: "",
			Description: "Not applied here. n8n stores bound values in this option as a comma-separated " +
				"string; this node binds a JSON array in its own Query Parameters field, which is a " +
				"different shape rather than a different name, and the importer is what translates one " +
				"into the other.",
			DisplayOptions: shownFor(PostgresOperationExecuteQuery),
		},
		node.PropertyDefinition{
			Key: SQLOptionTreatSingleQuotesAsText, Label: "Treat Query Parameters in Single Quotes as Text",
			Kind: node.PropertyBoolean, Default: false,
			Description: "Not applied. It governs n8n's own textual substitution of query replacements, which " +
				"this node does not do — every value is bound, so a quote inside one is data.",
			DisplayOptions: shownFor(PostgresOperationExecuteQuery),
		},
		node.PropertyDefinition{
			Key: SQLOptionOutputColumns, Label: "Output Columns", Kind: node.PropertyMultiOptions,
			Description: "Which columns come back. Empty returns them all.",
			DisplayOptions: shownFor(PostgresOperationSelect, PostgresOperationInsert,
				PostgresOperationUpdate, PostgresOperationUpsert),
		},
		node.PropertyDefinition{
			Key: SQLOptionLargeNumbersOutput, Label: "Output Large-Format Numbers As",
			Kind: node.PropertyOptions, Default: LargeNumbersAsText,
			Options: []node.PropertyOption{
				{Label: "Text", Value: LargeNumbersAsText},
				{Label: "Numbers", Value: LargeNumbersAsNumbers},
			},
			Description: "A bigint or a numeric column holds more digits than a JSON number carries exactly. " +
				"Text keeps every digit; Numbers is easier to do arithmetic on and starts losing the low " +
				"digits somewhere past sixteen of them.",
		},
		node.PropertyDefinition{
			Key: SQLOptionSkipOnConflict, Label: "Skip on Conflict", Kind: node.PropertyBoolean, Default: false,
			Description:    "Pass over a row that violates a unique constraint instead of failing the node.",
			DisplayOptions: shownFor(PostgresOperationInsert),
		},
		node.PropertyDefinition{
			Key: SQLOptionReplaceEmptyStrings, Label: "Replace Empty Strings with NULL",
			Kind: node.PropertyBoolean, Default: false,
			Description: "Useful when the data came from a spreadsheet, where a blank cell arrives as an " +
				"empty string rather than as nothing at all.",
			DisplayOptions: shownFor(PostgresOperationInsert, PostgresOperationUpdate,
				PostgresOperationUpsert, PostgresOperationExecuteQuery),
		},
	)
	return node.PropertyDefinition{
		Key: "options", Label: "Options", Kind: node.PropertyCollection,
		Description: "Settings that change how the statement is sent and how its results come back.",
		Fields:      fields,
	}
}

// sqlOptions is the collection, read.
type sqlOptions struct {
	Cascade             bool
	ConnectionTimeout   float64
	QueryBatching       string
	OutputColumns       []string
	LargeNumbersOutput  string
	SkipOnConflict      bool
	ReplaceEmptyStrings bool
}

// unappliedSQLOptions are the keys this server keeps and does not act on.
//
// Named rather than silently ignored, and named here rather than in three
// places: the enumeration test walks the declared collection and fails on any
// key that is neither read by readSQLOptions nor listed here, so an option
// added to the form without an implementation cannot ship quietly.
func unappliedSQLOptions() map[string]string {
	return map[string]string{
		SQLOptionDelayClosingIdleConnection: "this server opens a connection per node run and closes it " +
			"when the run ends, so there is no idle connection to delay closing",
		SQLOptionQueryReplacement: "n8n stores bound values in this option as a comma-separated string; " +
			"this node binds a JSON array in its own Query Parameters field, and the importer translates " +
			"one into the other rather than keeping both",
		SQLOptionTreatSingleQuotesAsText: "it governs n8n's own textual substitution, which this node " +
			"does not do — every value is bound",
	}
}

// readSQLOptions decodes the collection, supplying every default itself.
//
// The defaults are filled here rather than relied on from the platform: the
// registry's default-filling walks the top level only, so a collection's nested
// declared defaults never reach a stored document. An option the user never
// opened therefore arrives absent, and absent has to mean the declared default
// rather than the zero value — which for `largeNumbersOutput` is the difference
// between keeping every digit and silently choosing a float.
func readSQLOptions(value any) sqlOptions {
	options := sqlOptions{
		ConnectionTimeout:  30,
		QueryBatching:      BatchingSingle,
		LargeNumbersOutput: LargeNumbersAsText,
	}
	stored, ok := value.(map[string]any)
	if !ok {
		return options
	}
	if flag, present := stored[SQLOptionCascade].(bool); present {
		options.Cascade = flag
	}
	// Zero keeps the declared default rather than meaning "no timeout": it
	// reaches context.WithTimeout, where a zero duration is a deadline already
	// in the past, so honouring it literally would fail every run instantly.
	if seconds := numberValue(stored[SQLOptionConnectionTimeout]); seconds > 0 {
		options.ConnectionTimeout = seconds
	}
	switch batching := textValue(stored[SQLOptionQueryBatching], ""); batching {
	case BatchingSingle, BatchingIndependently, BatchingTransaction:
		options.QueryBatching = batching
	}
	switch rendering := textValue(stored[SQLOptionLargeNumbersOutput], ""); rendering {
	case LargeNumbersAsText, LargeNumbersAsNumbers:
		options.LargeNumbersOutput = rendering
	}
	if flag, present := stored[SQLOptionSkipOnConflict].(bool); present {
		options.SkipOnConflict = flag
	}
	if flag, present := stored[SQLOptionReplaceEmptyStrings].(bool); present {
		options.ReplaceEmptyStrings = flag
	}
	if columns, present := stored[SQLOptionOutputColumns].([]any); present {
		for _, column := range columns {
			if name := textValue(column, ""); name != "" {
				options.OutputColumns = append(options.OutputColumns, name)
			}
		}
	}
	return options
}

// validateSQLOptions refuses a collection this server cannot honour.
//
// The editor writes a collection as text until it has a control for one, so a
// string here is the ordinary shape of a half-built form rather than an attack
// — and reading it as an empty collection would silently reset every option the
// user had set.
func validateSQLOptions(value any) error {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return fmt.Errorf("the options are stored as text rather than as settings; clear the field and set them again")
	default:
		return fmt.Errorf("the options must be a collection of settings")
	}

	stored, _ := value.(map[string]any)
	// Checked against the widest declaration rather than this node's, so that
	// importing a PostgreSQL workflow into the MySQL node reports the one
	// option MySQL cannot honour where it is honoured — in the node's own
	// diagnostics — rather than here as an unknown key.
	declared := map[string]bool{}
	for _, field := range sqlOptionsCollection(sqlbuild.Postgres).Fields {
		declared[field.Key] = true
	}
	unknown := make([]string, 0, 2)
	for key := range stored {
		if !declared[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		// Named rather than dropped. An option this server does not know is
		// either a newer n8n's or a typo, and both are worth seeing.
		return fmt.Errorf("these options are not settings this server has: %s", strings.Join(unknown, ", "))
	}
	if batching := textValue(stored[SQLOptionQueryBatching], BatchingSingle); batching != "" {
		switch batching {
		case BatchingSingle, BatchingIndependently, BatchingTransaction:
		default:
			return fmt.Errorf("query batching %q is not supported", batching)
		}
	}
	return nil
}

// largeNumberTypes are the column types whose values outrun a JSON number.
//
// Named types rather than a width test on the value, because the question is
// what the column can hold and not what this row happens to hold: a bigint
// column returning 7 today returns 9007199254740993 tomorrow, and a rendering
// that changed shape when it did would be worse than either rendering.
//
// PostgreSQL and MySQL spellings both, since one map serves both dialects and
// the names do not collide.
var largeNumberTypes = map[string]bool{
	"INT8": true, "BIGINT": true, "BIGSERIAL": true, "SERIAL8": true,
	"NUMERIC": true, "DECIMAL": true, "MONEY": true,
	"UNSIGNED BIGINT": true,
}

// shapeRow applies the options that change a returned row.
//
// Both of them are output-side: the projection decides which columns survive
// and the rendering decides how a large number is spelled. Neither touches a
// value the database did not return, so a row that came back empty stays empty.
func shapeRow(row map[string]any, columnTypes map[string]string, options sqlOptions) map[string]any {
	if len(options.OutputColumns) > 0 {
		kept := make(map[string]any, len(options.OutputColumns))
		for _, column := range options.OutputColumns {
			if value, present := row[column]; present {
				kept[column] = value
			}
		}
		row = kept
	}
	for column, value := range row {
		if !largeNumberTypes[columnTypes[column]] {
			continue
		}
		row[column] = renderLargeNumber(value, options.LargeNumbersOutput)
	}
	return row
}

// renderLargeNumber spells one large-format number the way the option asked.
//
// Both directions are real conversions, because the driver's own answer is
// already split: PostgreSQL hands back bigint as an int64 and numeric as a
// string, so neither setting can be satisfied by leaving the value alone. A
// value that is neither is returned untouched — an option about number
// rendering must not reach a NULL or a type the map is wrong about.
func renderLargeNumber(value any, rendering string) any {
	switch rendering {
	case LargeNumbersAsNumbers:
		switch typed := value.(type) {
		case string:
			number, err := strconv.ParseFloat(typed, 64)
			if err != nil {
				// Not a number this rendering can produce — a numeric NaN, or
				// a type the map claimed wrongly. Keeping the text is the only
				// answer that loses nothing.
				return typed
			}
			return number
		case int64:
			return float64(typed)
		}
		return value
	default:
		switch typed := value.(type) {
		case int64:
			return strconv.FormatInt(typed, 10)
		case float64:
			// Already through a float, so the digits past the sixteenth are
			// already gone; formatting without an exponent at least keeps this
			// from arriving downstream as 1e+18.
			return strconv.FormatFloat(typed, 'f', -1, 64)
		}
		return value
	}
}

// DeclaredSQLOptions names every option the database nodes accept.
//
// Exported for the n8n importer, which has to filter an incoming collection to
// this set: validateSQLOptions refuses a node carrying a key this server does
// not know, so passing a newer n8n's option straight through would turn an
// import into a workflow that cannot be saved. The importer reports what it
// dropped instead.
func DeclaredSQLOptions() []string {
	fields := sqlOptionsCollection(sqlbuild.Postgres).Fields
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Key)
	}
	sort.Strings(names)
	return names
}

// UnappliedSQLOptionsForTest is the refusal list, for the enumeration test.
func UnappliedSQLOptionsForTest() map[string]string { return unappliedSQLOptions() }

// SQLOptionIsDeclaredForTest reports whether the collection declares a key.
func SQLOptionIsDeclaredForTest(key string) bool {
	for _, field := range sqlOptionsCollection(sqlbuild.Postgres).Fields {
		if field.Key == key {
			return true
		}
	}
	return false
}

// SQLOptionIsAppliedForTest reports whether readSQLOptions reads a key.
//
// A probe rather than a second list. Asking "is this key in the applied set?"
// against a hand-kept set answers only whether somebody remembered to add it;
// setting the option to something other than its default and watching whether
// the decoded options change answers whether the reader actually looks at it,
// which is the thing the enumeration test wants to know.
//
// It proves the key is read, not that the read value reaches the database —
// that is what the behavioural tests are for. The two together are what make
// the declaration honest.
func SQLOptionIsAppliedForTest(key string) bool {
	var field node.PropertyDefinition
	for _, declared := range sqlOptionsCollection(sqlbuild.Postgres).Fields {
		if declared.Key == key {
			field = declared
		}
	}
	probe := probeValueFor(field)
	if probe == nil {
		return false
	}
	return !reflect.DeepEqual(readSQLOptions(map[string]any{key: probe}), readSQLOptions(nil))
}

// probeValueFor picks a value for a field that differs from its default.
func probeValueFor(field node.PropertyDefinition) any {
	switch field.Kind {
	case node.PropertyBoolean:
		return true
	case node.PropertyNumber:
		// Not 30 and not 0, so it differs from both declared numeric default.
		return float64(45)
	case node.PropertyString:
		return "probe"
	case node.PropertyMultiOptions:
		return []any{"probe"}
	case node.PropertyOptions:
		for _, option := range field.Options {
			if option.Value != field.Default {
				return option.Value
			}
		}
	}
	return nil
}

// sqlRestartSequencesProperty is n8n's own top-level truncate modifier.
//
// Top-level rather than inside the options collection because that is where
// n8n puts it, and the shape of a stored document is the contract. Declared
// only where the dialect offers a choice: MySQL's TRUNCATE always resets
// AUTO_INCREMENT, so a switch there would be a control with one position.
func sqlRestartSequencesProperty(dialect sqlbuild.Dialect) (node.PropertyDefinition, bool) {
	if !dialect.ChoosesSequenceRestart() {
		return node.PropertyDefinition{}, false
	}
	return node.PropertyDefinition{
		Key: "restartSequences", Label: "Restart sequences", Kind: node.PropertyBoolean, Default: false,
		Description: "Reset the table's identity columns to where they started, so the next inserted row " +
			"takes the first number again rather than carrying on from the highest one used.",
		DisplayOptions: node.Visibility{Show: []node.Condition{
			{Key: "operation", Values: []any{PostgresOperationDeleteTable}},
			{Key: "deleteCommand", Values: []any{sqlbuild.DeleteTruncate}},
		}},
	}, true
}

// mustProperty unwraps a property a dialect is known to declare.
//
// Used only where the caller has already committed to the dialect, so a false
// here is a programming error rather than a configuration one.
func mustProperty(property node.PropertyDefinition, declared bool) node.PropertyDefinition {
	if !declared {
		panic("the dialect does not declare this property")
	}
	return property
}

// shownForOperations is the visibility shorthand both database nodes use.
func shownForOperations(operations ...string) []node.VisibilityCondition {
	conditions := make([]node.VisibilityCondition, 0, len(operations))
	for _, operation := range operations {
		conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
	}
	return conditions
}

// sqlSortCollection is the ORDER BY builder, in n8n's stored shape.
//
// A fixed collection named `sort` holding a `values` group of `column` and
// `direction`, because that is what n8n stores and the point of this node is
// that an imported workflow keeps working. The direction values are n8n's
// "ASC" and "DESC" verbatim.
func sqlSortCollection() node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "sort", Label: "Sort", Kind: node.PropertyFixedCollection,
		TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add sort rule"},
		Description: "The order rows come back in. Several rules may be added, and they apply in the " +
			"order they are listed — without one, the database is free to return the rows in any " +
			"order at all, which is what makes an unsorted Limit return an arbitrary subset.",
		VisibleWhen: shownForOperations(PostgresOperationSelect),
		Groups: []node.PropertyGroup{{
			Key: "values", Label: "Sort Rule",
			Fields: []node.PropertyDefinition{
				{Key: "column", Label: "Column", Kind: node.PropertyOptions,
					Description: "Choose from the list, or name one with an expression."},
				{Key: "direction", Label: "Direction", Kind: node.PropertyOptions, Default: SortAscending,
					Options: []node.PropertyOption{
						{Label: "Ascending", Value: SortAscending},
						{Label: "Descending", Value: SortDescending},
					}},
			},
		}},
	}
}

// Sort directions, using n8n's value strings.
const (
	SortAscending  = "ASC"
	SortDescending = "DESC"
)

// readSQLSort reads the sort collection into the builder's own shape.
//
// A rule with no column is dropped rather than refused: n8n's fixed collection
// adds an empty row the moment the button is pressed, so an unfinished rule is
// the ordinary state of a form somebody is still filling in, and failing the
// node for it would make the control unusable.
func readSQLSort(value any) []sqlbuild.Order {
	stored, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	rows, ok := stored["values"].([]any)
	if !ok {
		return nil
	}
	order := make([]sqlbuild.Order, 0, len(rows))
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		column := strings.TrimSpace(textValue(row["column"], ""))
		if column == "" {
			continue
		}
		order = append(order, sqlbuild.Order{
			Column:     column,
			Descending: strings.EqualFold(textValue(row["direction"], SortAscending), SortDescending),
		})
	}
	if len(order) == 0 {
		return nil
	}
	return order
}
