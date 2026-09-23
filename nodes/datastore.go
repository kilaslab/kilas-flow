package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The datastore node's server-owned bindings.
const (
	DatastoreNodeType   = "kilasflow.datastore"
	DatastoreExecutorID = "core.datastore"
)

// DatastoreVersion is the version the datastore operation set registers at.
var DatastoreVersion = workflow.V(1)

// Datastore resources, using n8n's names so the cascade reads the same.
const (
	datastoreResourceRow   = "row"
	datastoreResourceTable = "table"
)

// Datastore row operations: the seven Row Actions of design-refs shot 26,
// plus Increment, which n8n has no equivalent for and which exists because a
// counter that must not lose writes cannot be built from Get and Update.
const (
	DatastoreOperationInsert      = "insert"
	DatastoreOperationGet         = "get"
	DatastoreOperationUpdate      = "update"
	DatastoreOperationUpsert      = "upsert"
	DatastoreOperationIncrement   = "increment"
	DatastoreOperationDelete      = "delete"
	DatastoreOperationIfExists    = "ifExists"
	DatastoreOperationIfNotExists = "ifNotExists"
)

// Datastore table operations: the five Table Actions of shot 26. The delete
// carries its own value because it shares one option list with the row
// delete, and the update operation is surfaced as Rename, never as a generic
// update.
const (
	DatastoreOperationCreateTable = "create"
	DatastoreOperationListTables  = "list"
	DatastoreOperationRenameTable = "rename"
	DatastoreOperationDeleteTable = "deleteTable"
	DatastoreOperationClearTable  = "clear"
)

func datastoreShownFor(operations ...string) []node.VisibilityCondition {
	conditions := make([]node.VisibilityCondition, 0, len(operations))
	for _, operation := range operations {
		conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
	}
	return conditions
}

func datastoreNode() node.Definition {
	return node.Definition{
		Type:        DatastoreNodeType,
		Version:     DatastoreVersion,
		DisplayName: "Data table",
		Description: "Stores rows in a KilasFlow data table: insert, read, update, upsert, increment and delete without holding a database credential. A filtered update, delete or clear writes in one statement, atomic per row on both drivers: concurrent writers never interleave inside a row, the last writer wins, and nothing takes a row lock. Upsert matched on the id column is one statement (created at exactly that id, never inserted twice); upsert matched on any other column is read-then-write in no single transaction, so two concurrent upserts against the same filter may both insert. A counter or flag that must not lose writes uses Increment, which adds to a number column in one statement and returns the new value, never Get followed by Update.",
		Category:    "Datastore",
		Group:       []node.NodeGroup{node.GroupInput},
		Icon:        &node.NodeIcon{Light: "builtin:table"},
		IconColor:   "#0e7490",
		Subtitle:    "{{ $parameter.resource }}/{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Required: true, Default: datastoreResourceRow,
				Options: []node.PropertyOption{
					{Label: "Row", Value: datastoreResourceRow},
					{Label: "Table", Value: datastoreResourceTable},
				},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: DatastoreOperationInsert,
				Options: []node.PropertyOption{
					{Label: "Row: Insert", Value: DatastoreOperationInsert},
					{Label: "Row: Get", Value: DatastoreOperationGet},
					{Label: "Row: Update", Value: DatastoreOperationUpdate},
					{Label: "Row: Upsert", Value: DatastoreOperationUpsert},
					{Label: "Row: Increment", Value: DatastoreOperationIncrement},
					{Label: "Row: Delete", Value: DatastoreOperationDelete},
					{Label: "Row: If Exists", Value: DatastoreOperationIfExists},
					{Label: "Row: If Not Exists", Value: DatastoreOperationIfNotExists},
					{Label: "Table: Create", Value: DatastoreOperationCreateTable},
					{Label: "Table: List", Value: DatastoreOperationListTables},
					{Label: "Table: Rename", Value: DatastoreOperationRenameTable},
					{Label: "Table: Delete", Value: DatastoreOperationDeleteTable},
					{Label: "Table: Clear", Value: DatastoreOperationClearTable},
				},
			},
			{
				Key: "dataTableId", Label: "Data table", Kind: node.PropertyResourceLocator, Required: true,
				Description: "Which data table this acts on.",
				Modes: []node.PropertyMode{
					{
						Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
						LoadOptions: &node.OptionsLoader{
							Source: property.LoaderInternal, Name: loadoptions.DatastoreListLoader,
						},
					},
					{Name: "name", Label: "By Name", Kind: node.PropertyString, Placeholder: "Metrics"},
					{Name: "id", Label: "By ID", Kind: node.PropertyString, Placeholder: "datastore_…"},
				},
			},
			{
				Key: "name", Label: "Name", Kind: node.PropertyString,
				Description: "The table's name.",
				VisibleWhen: datastoreShownFor(DatastoreOperationCreateTable, DatastoreOperationRenameTable),
			},
			{
				Key: "columns", Label: "Columns", Kind: node.PropertyResourceMapper,
				Mapper: &node.ResourceMapperDeclaration{
					Schema: &node.OptionsLoader{
						Source: property.LoaderInternal, Name: loadoptions.DatastoreMappingColumnsLoader,
						DependsOn: []string{loadoptions.DatastoreDependency},
					},
					SupportsAutoMap: true,
					ValuesLabel:     "Values to Send",
				},
				Description: "What goes into each column. Automatic maps incoming fields by name; manual sets each column.",
				VisibleWhen: datastoreShownFor(DatastoreOperationInsert, DatastoreOperationUpdate, DatastoreOperationUpsert),
			},
			{
				Key: "counterColumn", Label: "Column", Kind: node.PropertyString, Required: true,
				Description: "The number column to add to. A column name, never an expression.",
				VisibleWhen: datastoreShownFor(DatastoreOperationIncrement),
			},
			{
				Key: "amount", Label: "Amount", Kind: node.PropertyNumber, Default: 1,
				Description: "Added in one statement; negative subtracts; an empty cell counts as zero.",
				VisibleWhen: datastoreShownFor(DatastoreOperationIncrement),
			},
			{
				Key: "match", Label: "Must Match", Kind: node.PropertyOptions, Default: "any",
				Description: "Whether any or all of the conditions must hold for a row to match.",
				Options: []node.PropertyOption{
					{Label: "Any Condition", Value: "any"},
					{Label: "All Conditions", Value: "all"},
				},
				VisibleWhen: datastoreShownFor(DatastoreOperationGet, DatastoreOperationUpdate,
					DatastoreOperationUpsert, DatastoreOperationIncrement, DatastoreOperationDelete,
					DatastoreOperationIfExists, DatastoreOperationIfNotExists),
			},
			{
				Key: "filters", Label: "Conditions", Kind: node.PropertyFixedCollection,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add condition"},
				Description: "Which rows this acts on. Column is the column, condition defaults to equals, value supports expressions.",
				Groups: []node.PropertyGroup{{
					Key: "conditions", Label: "Condition",
					Fields: []node.PropertyDefinition{
						{Key: "keyName", Label: "Column", Kind: node.PropertyString, Required: true, Description: "A column name, never an expression."},
						{Key: "condition", Label: "Condition", Kind: node.PropertyOptions, Default: string(datastore.CondEq),
							Options: []node.PropertyOption{
								{Label: "Equals", Value: string(datastore.CondEq)},
								{Label: "Not equals", Value: string(datastore.CondNeq)},
								{Label: "Contains (case-sensitive)", Value: string(datastore.CondLike)},
								{Label: "Contains (case-insensitive)", Value: string(datastore.CondILike)},
								{Label: "Greater than", Value: string(datastore.CondGt)},
								{Label: "Greater than or equal", Value: string(datastore.CondGte)},
								{Label: "Less than", Value: string(datastore.CondLt)},
								{Label: "Less than or equal", Value: string(datastore.CondLte)},
								{Label: "Is empty", Value: string(datastore.CondIsEmpty)},
								{Label: "Is not empty", Value: string(datastore.CondIsNotEmpty)},
							}},
						{Key: "keyValue", Label: "Value", Kind: node.PropertyString, Description: "Supports expressions."},
					},
				}},
				VisibleWhen: datastoreShownFor(DatastoreOperationGet, DatastoreOperationUpdate,
					DatastoreOperationUpsert, DatastoreOperationIncrement, DatastoreOperationDelete,
					DatastoreOperationIfExists, DatastoreOperationIfNotExists),
			},
			{
				Key: "returnAll", Label: "Return All", Kind: node.PropertyBoolean, Default: false,
				VisibleWhen: datastoreShownFor(DatastoreOperationGet),
			},
			{
				Key: "limitPerInputRow", Label: "Limit Per Input Row", Kind: node.PropertyNumber, Default: 50,
				Description: "How many rows one input item reads at most.",
				DisplayOptions: node.Visibility{
					Show: []node.Condition{{Key: "operation", Values: []any{DatastoreOperationGet}}},
					Hide: []node.Condition{{Key: "returnAll", Values: []any{true}}},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     DatastoreExecutorID,
		Validate:       validateDatastoreConfiguration,
		PortsFor:       datastorePortsFor,
	}
}

// datastorePortsFor gives the branch operations their second output. Every
// other operation keeps one, so the canvas never shows a fork an insert
// cannot take.
//
// The two ports are named the way the IF node names its branches — `true` for
// the outcome the operation tests for, `false` for the one it does not —
// rather than after the operation's own vocabulary: a port's identity has to
// hold while the label follows the configuration, and `rowFound` would be the
// *first* port for If Exists and the *second* for If Not Exists. Two ports
// sharing one name is what made the second unreachable: the compiler resolves
// a connection's port by name and takes the first match, so every wire landed
// on index 0.
func datastorePortsFor(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	switch textValue(parameters["operation"], DatastoreOperationInsert) {
	case DatastoreOperationIfExists:
		return mainInput(), []workflow.Port{
			{Name: "true", DisplayName: "Row found", Kind: workflow.ConnectionMain},
			{Name: "false", DisplayName: "No row", Kind: workflow.ConnectionMain},
		}
	case DatastoreOperationIfNotExists:
		return mainInput(), []workflow.Port{
			{Name: "true", DisplayName: "No row", Kind: workflow.ConnectionMain},
			{Name: "false", DisplayName: "Row found", Kind: workflow.ConnectionMain},
		}
	default:
		return mainInput(), mainOutput()
	}
}

// datastoreRowOperations are the operations that act on rows through the
// table the locator names.
var datastoreRowOperations = map[string]bool{
	DatastoreOperationInsert: true, DatastoreOperationGet: true,
	DatastoreOperationUpdate: true, DatastoreOperationUpsert: true,
	DatastoreOperationIncrement: true, DatastoreOperationDelete: true,
	DatastoreOperationIfExists: true, DatastoreOperationIfNotExists: true,
}

// datastoreTableOperations are the operations that act on tables.
var datastoreTableOperations = map[string]bool{
	DatastoreOperationCreateTable: true, DatastoreOperationListTables: true,
	DatastoreOperationRenameTable: true, DatastoreOperationDeleteTable: true,
	DatastoreOperationClearTable: true,
}

// datastoreFilterOperations are the row operations that read the conditions
// panel.
var datastoreFilterOperations = map[string]bool{
	DatastoreOperationGet: true, DatastoreOperationUpdate: true,
	DatastoreOperationUpsert: true, DatastoreOperationIncrement: true,
	DatastoreOperationDelete: true, DatastoreOperationIfExists: true,
	DatastoreOperationIfNotExists: true,
}

// datastoreMatchingOperations need at least one condition: without one the
// operation addresses the whole table, which the management API refuses and
// the node must refuse first. Get without conditions reads the table, which
// is what Return All and the per-row limit are for.
var datastoreMatchingOperations = map[string]bool{
	DatastoreOperationUpdate: true, DatastoreOperationUpsert: true,
	DatastoreOperationIncrement: true, DatastoreOperationDelete: true,
	DatastoreOperationIfExists: true, DatastoreOperationIfNotExists: true,
}

func validateDatastoreConfiguration(n workflow.Node) error {
	resource := textParameter(n.Parameters, "resource")
	if resource == "" {
		resource = datastoreResourceRow
	}
	operation := textParameter(n.Parameters, "operation")
	if operation == "" {
		operation = DatastoreOperationInsert
	}
	switch resource {
	case datastoreResourceRow:
		if !datastoreRowOperations[operation] {
			return fmt.Errorf("operation %q does not act on rows", operation)
		}
	case datastoreResourceTable:
		if !datastoreTableOperations[operation] {
			return fmt.Errorf("operation %q does not act on tables", operation)
		}
	default:
		return fmt.Errorf("resource %q is not supported", resource)
	}
	// Column names are literal-only: the executor resolves the whole
	// parameter tree, so a marker in a column slot would hand an identifier
	// to whoever controls the incoming item. Refused at save, and again at
	// run against the unresolved parameters, which is the only place the
	// marker is still visible.
	if err := refuseDatastoreColumnExpressions(n.Parameters); err != nil {
		return err
	}
	if operation != DatastoreOperationCreateTable && operation != DatastoreOperationListTables {
		if !property.LocatorIsSet(n.Parameters["dataTableId"]) {
			return fmt.Errorf("%s needs a data table", operation)
		}
	}
	switch operation {
	case DatastoreOperationCreateTable, DatastoreOperationRenameTable:
		if strings.TrimSpace(textParameter(n.Parameters, "name")) == "" {
			return fmt.Errorf("%s needs a name", operation)
		}
	case DatastoreOperationIncrement:
		// The store refuses a non-number column at run time; a missing
		// column name is refused here, where the operator can still see
		// which parameter is unset.
		if strings.TrimSpace(textParameter(n.Parameters, "counterColumn")) == "" {
			return fmt.Errorf("%s needs a column to add to", operation)
		}
	}
	if datastoreMatchingOperations[operation] {
		conditions, err := datastoreFilterRows(n.Parameters["filters"])
		if err != nil {
			return err
		}
		if len(conditions) == 0 {
			return fmt.Errorf("%s needs at least one condition; a filterless %s addresses the whole table", operation, operation)
		}
	}
	return nil
}

// refuseDatastoreColumnExpressions refuses expression markers in column-name
// slots: every filter condition's keyName, the increment's counterColumn, a
// mapper value that is itself a marker (its keys would then come from the
// incoming item), and matching column entries. Values stay
// expression-capable — that is what binding is for — and the engine
// re-checks every resolved name against the catalogue before it is quoted,
// so a literal that names no column still fails.
func refuseDatastoreColumnExpressions(parameters map[string]any) error {
	filters, present := parameters["filters"]
	if present && filters != nil && expression.IsExpression(filters) {
		return fmt.Errorf("filters is built from an expression, and column names cannot be: " +
			"an expression here resolves its column names from the incoming item, so a value " +
			"arriving from a webhook would choose which columns this reads or writes")
	}
	if _, err := datastoreFilterRows(filters); err != nil {
		return err
	}
	if counter, present := parameters["counterColumn"]; present && counter != nil && expression.IsExpression(counter) {
		return fmt.Errorf("counterColumn is built from an expression, and a column name cannot be one: " +
			"an expression here resolves the column name from the incoming item, so a value " +
			"arriving from a webhook would choose which column this writes")
	}
	columns, present := parameters["columns"]
	if present && columns != nil && expression.IsExpression(columns) {
		return fmt.Errorf("columns is built from an expression, and column names cannot be: " +
			"map each column explicitly and keep expressions in the values")
	}
	if mapping, ok := property.ReadMapping(columns); ok {
		for _, name := range mapping.MatchingColumns {
			if expression.IsExpression(name) {
				return fmt.Errorf("columns.matchingColumns holds an expression, and a column name cannot be one")
			}
		}
	}
	return nil
}

// datastoreFilterRows reads the stored conditions panel: filters.conditions
// is the list, each row carrying the node's keyName/condition/keyValue
// triple. An expression-valued panel is left for run time, where the backstop
// refuses it before anything resolves; anything else shaped wrong is refused
// here rather than misread there.
func datastoreFilterRows(value any) ([]datastore.NodeFilterCondition, error) {
	if value == nil {
		return nil, nil
	}
	if expression.IsExpression(value) {
		return nil, nil
	}
	collection, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("filters must be a conditions panel")
	}
	raw, present := collection["conditions"]
	if !present || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("filters.conditions must be a list of condition rows")
	}
	conditions := make([]datastore.NodeFilterCondition, 0, len(list))
	for index, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("filters.conditions[%d] must be a condition row", index)
		}
		if expression.IsExpression(fields["keyName"]) {
			return nil, fmt.Errorf("filters.conditions[%d].keyName is built from an expression, and a column name cannot be: "+
				"resolve the column to a literal name and keep the expression in the value", index)
		}
		keyName, _ := fields["keyName"].(string)
		condition := datastore.CondEq
		if rawCondition, _ := fields["condition"].(string); rawCondition != "" {
			condition = datastore.Condition(rawCondition)
		}
		conditions = append(conditions, datastore.NodeFilterCondition{
			KeyName: keyName, Condition: condition, KeyValue: fields["keyValue"],
		})
	}
	return conditions, nil
}

// DatastoreStore is the slice of the row store the node may use: the tenant
// arrives from the execution, never from the document.
type DatastoreStore interface {
	ListDatastores(ctx context.Context, tenantID string) ([]datastore.Datastore, error)
	GetDatastore(ctx context.Context, tenantID, id string) (*datastore.Datastore, error)
	Create(ctx context.Context, tenantID, name string, in []datastore.ColumnInput) (*datastore.Datastore, error)
	RenameDatastore(ctx context.Context, tenantID, id, name string) error
	Drop(ctx context.Context, tenantID, id string) error
	Clear(ctx context.Context, tenantID, dsID string) (int64, error)
	Insert(ctx context.Context, tenantID, dsID string, values map[string]any) (datastore.Row, error)
	Get(ctx context.Context, tenantID, dsID string, id int64) (datastore.Row, error)
	List(ctx context.Context, tenantID, dsID string, q datastore.RowQuery) (datastore.RowPage, error)
	Update(ctx context.Context, tenantID, dsID string, filter *datastore.Filter, values map[string]any, dryRun bool) (*datastore.UpdateResult, error)
	Delete(ctx context.Context, tenantID, dsID string, filter *datastore.Filter, dryRun bool) (*datastore.DeleteResult, error)
	Upsert(ctx context.Context, tenantID, dsID string, filter *datastore.Filter, values map[string]any, dryRun bool) (*datastore.UpsertResult, error)
}

// DatastoreExecutor runs the data-table operation set against the row store.
type DatastoreExecutor struct {
	store DatastoreStore
}

// NewDatastoreExecutor builds the data-table executor. A nil store refuses
// every run with the install message rather than dereferencing.
func NewDatastoreExecutor(store DatastoreStore) *DatastoreExecutor {
	return &DatastoreExecutor{store: store}
}

// Execute resolves parameters per item and runs the configured operation once
// per incoming item, so a failure on the seventh of ten rows has an item to
// blame and the continue-on-fail setting has something to mean.
func (executor *DatastoreExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if executor.store == nil {
		return nil, fmt.Errorf("node %q: datastore storage is not available on this server", ir.Name)
	}
	// The backstop for the compile-time refusal, against the unresolved
	// parameters — the only place the marker is still visible. A document can
	// reach an executor without passing validation: an import, a direct
	// repository write, a workflow saved before the rule existed.
	if err := refuseDatastoreColumnExpressions(ir.Parameters); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	tenant := request.Execution.TenantID
	if strings.TrimSpace(tenant) == "" {
		return nil, fmt.Errorf("node %q: this run carries no tenant", ir.Name)
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	continueOnFail := datastoreContinueOnFail(ir.Settings)
	out := make([]workflow.Item, 0, len(items))
	second := make([]workflow.Item, 0)
	branched := false
	failed := func(index int, cause error) workflow.Item {
		return workflow.Item{JSON: map[string]any{
			engine.ErrorItemKey: map[string]any{
				"message": cause.Error(),
				"node":    ir.Name,
				"item":    float64(index + 1),
			},
		}, Paired: &workflow.PairedItem{SourceNodeID: ir.ID, SourcePort: "main", ItemIndex: index}}
	}
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			if !continueOnFail {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			out = append(out, failed(index, err))
			continue
		}
		produced, other, isBranch, err := executor.runOne(ctx, ir, tenant, parameters, item, index)
		if err != nil {
			if !continueOnFail {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			out = append(out, failed(index, err))
			continue
		}
		if isBranch {
			branched = true
			out = append(out, produced...)
			second = append(second, other...)
			continue
		}
		out = append(out, produced...)
	}
	if branched {
		return workflow.NodeOutput{out, second}, nil
	}
	return workflow.NodeOutput{out}, nil
}

// datastoreContinueOnFail reads the node's own setting, tolerating the string
// forms a hand-written document or an import can produce.
func datastoreContinueOnFail(settings map[string]any) bool {
	value, found := settings["continueOnFail"]
	if !found || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	case float64:
		return typed != 0
	default:
		return false
	}
}

// runOne runs the operation for one item. Branch operations return their two
// ports; every other operation returns its items on the first.
func (executor *DatastoreExecutor) runOne(ctx context.Context, ir workflow.IRNode, tenant string, parameters map[string]any, item workflow.Item, index int) ([]workflow.Item, []workflow.Item, bool, error) {
	resource := textValue(parameters["resource"], datastoreResourceRow)
	operation := textValue(parameters["operation"], DatastoreOperationInsert)
	lineage := &workflow.PairedItem{SourceNodeID: ir.ID, SourcePort: "main", ItemIndex: index}
	emit := func(rows []datastore.Row) []workflow.Item {
		produced := make([]workflow.Item, 0, len(rows))
		for _, row := range rows {
			produced = append(produced, workflow.Item{JSON: map[string]any(row), Paired: lineage})
		}
		return produced
	}
	switch resource {
	case datastoreResourceTable:
		produced, err := executor.runTable(ctx, tenant, operation, parameters, lineage)
		return produced, nil, false, err
	case datastoreResourceRow:
		if operation == DatastoreOperationIfExists || operation == DatastoreOperationIfNotExists {
			return executor.runBranch(ctx, tenant, operation, parameters, item, lineage, emit)
		}
		produced, err := executor.runRow(ctx, tenant, operation, parameters, item, emit)
		return produced, nil, false, err
	default:
		return nil, nil, false, fmt.Errorf("resource %q is not supported", resource)
	}
}

// runTable runs the five table operations. Tables carry no per-item data, so
// the item only lends its lineage.
func (executor *DatastoreExecutor) runTable(ctx context.Context, tenant, operation string, parameters map[string]any, lineage *workflow.PairedItem) ([]workflow.Item, error) {
	one := func(json map[string]any) []workflow.Item {
		return []workflow.Item{{JSON: json, Paired: lineage}}
	}
	switch operation {
	case DatastoreOperationListTables:
		definitions, err := executor.store.ListDatastores(ctx, tenant)
		if err != nil {
			return nil, err
		}
		produced := make([]workflow.Item, 0, len(definitions))
		for _, definition := range definitions {
			produced = append(produced, workflow.Item{JSON: datastoreTableJSON(definition), Paired: lineage})
		}
		return produced, nil
	case DatastoreOperationCreateTable:
		name := strings.TrimSpace(textValue(parameters["name"], ""))
		if name == "" {
			return nil, fmt.Errorf("create needs a name")
		}
		definition, err := executor.store.Create(ctx, tenant, name, nil)
		if err != nil {
			return nil, err
		}
		return one(datastoreTableJSON(*definition)), nil
	case DatastoreOperationRenameTable:
		id, err := executor.datastoreID(ctx, tenant, parameters)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(textValue(parameters["name"], ""))
		if name == "" {
			return nil, fmt.Errorf("rename needs a name")
		}
		if err := executor.store.RenameDatastore(ctx, tenant, id, name); err != nil {
			return nil, err
		}
		definition, err := executor.store.GetDatastore(ctx, tenant, id)
		if err != nil {
			return nil, err
		}
		return one(datastoreTableJSON(*definition)), nil
	case DatastoreOperationDeleteTable:
		id, err := executor.datastoreID(ctx, tenant, parameters)
		if err != nil {
			return nil, err
		}
		if _, err := executor.store.GetDatastore(ctx, tenant, id); err != nil {
			return nil, err
		}
		if err := executor.store.Drop(ctx, tenant, id); err != nil {
			return nil, err
		}
		return one(map[string]any{"id": id}), nil
	case DatastoreOperationClearTable:
		id, err := executor.datastoreID(ctx, tenant, parameters)
		if err != nil {
			return nil, err
		}
		deleted, err := executor.store.Clear(ctx, tenant, id)
		if err != nil {
			return nil, err
		}
		return one(map[string]any{"id": id, "deleted": float64(deleted)}), nil
	default:
		return nil, fmt.Errorf("operation %q does not act on tables", operation)
	}
}

// runRow runs the row operations that produce one port: insert, get, update,
// upsert and delete.
func (executor *DatastoreExecutor) runRow(ctx context.Context, tenant, operation string, parameters map[string]any, item workflow.Item, emit func([]datastore.Row) []workflow.Item) ([]workflow.Item, error) {
	id, err := executor.datastoreID(ctx, tenant, parameters)
	if err != nil {
		return nil, err
	}
	switch operation {
	case DatastoreOperationInsert:
		values, err := executor.datastoreValues(ctx, tenant, id, operation, parameters, item)
		if err != nil {
			return nil, err
		}
		row, err := executor.store.Insert(ctx, tenant, id, values)
		if err != nil {
			return nil, err
		}
		return emit([]datastore.Row{row}), nil
	case DatastoreOperationGet:
		filter, err := datastoreItemFilter(parameters)
		if err != nil {
			return nil, err
		}
		query := datastore.RowQuery{Filter: filter}
		if isTrue(parameters["returnAll"]) {
			query.ReturnAll = true
		} else {
			query.Limit = int(numberValue(parameters["limitPerInputRow"]))
			if query.Limit <= 0 {
				query.Limit = 50
			}
		}
		page, err := executor.store.List(ctx, tenant, id, query)
		if err != nil {
			return nil, err
		}
		return emit(page.Rows), nil
	case DatastoreOperationUpdate:
		filter, err := datastoreItemFilter(parameters)
		if err != nil {
			return nil, err
		}
		if err := refuseEmptyNodeFilter(filter); err != nil {
			return nil, err
		}
		values, err := executor.datastoreValues(ctx, tenant, id, operation, parameters, item)
		if err != nil {
			return nil, err
		}
		result, err := executor.store.Update(ctx, tenant, id, filter, values, false)
		if err != nil {
			return nil, err
		}
		return emit(result.Rows), nil
	case DatastoreOperationUpsert:
		filter, err := datastoreItemFilter(parameters)
		if err != nil {
			return nil, err
		}
		if err := refuseEmptyNodeFilter(filter); err != nil {
			return nil, err
		}
		values, err := executor.datastoreValues(ctx, tenant, id, operation, parameters, item)
		if err != nil {
			return nil, err
		}
		result, err := executor.store.Upsert(ctx, tenant, id, filter, values, false)
		if err != nil {
			return nil, err
		}
		return emit(result.Rows), nil
	case DatastoreOperationIncrement:
		filter, err := datastoreItemFilter(parameters)
		if err != nil {
			return nil, err
		}
		if err := refuseEmptyNodeFilter(filter); err != nil {
			return nil, err
		}
		column := strings.TrimSpace(textValue(parameters["counterColumn"], ""))
		if column == "" {
			return nil, fmt.Errorf("increment needs a column to add to")
		}
		amount, err := datastoreIncrementAmount(parameters)
		if err != nil {
			return nil, err
		}
		incrementer, ok := executor.store.(DatastoreIncrementer)
		if !ok {
			return nil, fmt.Errorf("increment is not available on this store")
		}
		result, err := incrementer.Increment(ctx, tenant, id, filter, column, amount)
		if err != nil {
			return nil, err
		}
		return emit(result.Rows), nil
	case DatastoreOperationDelete:
		filter, err := datastoreItemFilter(parameters)
		if err != nil {
			return nil, err
		}
		if err := refuseEmptyNodeFilter(filter); err != nil {
			return nil, err
		}
		result, err := executor.store.Delete(ctx, tenant, id, filter, false)
		if err != nil {
			return nil, err
		}
		return emit(result.Rows), nil
	default:
		return nil, fmt.Errorf("operation %q does not act on rows", operation)
	}
}

// runBranch runs If Exists and If Not Exists. The two ports are the two
// outcomes of the operation's own test: the first carries what the branch
// produced, the second the incoming item when the test did not hold, so
// downstream always has an item whose lineage names the input that was tested.
// If Exists produces the rows that matched; If Not Exists has no rows to
// produce, so the item it tested passes on the first port when the table holds
// no match — the fork is otherwise a first port that can never carry anything.
func (executor *DatastoreExecutor) runBranch(ctx context.Context, tenant, operation string, parameters map[string]any, item workflow.Item, lineage *workflow.PairedItem, emit func([]datastore.Row) []workflow.Item) ([]workflow.Item, []workflow.Item, bool, error) {
	id, err := executor.datastoreID(ctx, tenant, parameters)
	if err != nil {
		return nil, nil, false, err
	}
	filter, err := datastoreItemFilter(parameters)
	if err != nil {
		return nil, nil, false, err
	}
	if err := refuseEmptyNodeFilter(filter); err != nil {
		return nil, nil, false, err
	}
	page, err := executor.store.List(ctx, tenant, id, datastore.RowQuery{Filter: filter, Limit: 1})
	if err != nil {
		return nil, nil, false, err
	}
	// The item as it arrived, carrying this item's lineage: it is what leaves
	// on the second port when the operation's test does not hold.
	passed := []workflow.Item{{JSON: map[string]any(item.JSON), Paired: lineage}}
	if len(page.Rows) > 0 {
		if operation == DatastoreOperationIfExists {
			return emit(page.Rows), []workflow.Item{}, true, nil
		}
		return []workflow.Item{}, passed, true, nil
	}
	if operation == DatastoreOperationIfNotExists {
		return passed, []workflow.Item{}, true, nil
	}
	return []workflow.Item{}, passed, true, nil
}

// datastoreID resolves the locator to a catalogue id. From-list and By-ID
// already carry it; By-Name resolves through the tenant's own list, so one
// tenant's name can never address another tenant's table, and through
// datastore.ResolveByName, so a name two tables share is refused rather than
// taken to mean whichever the list returned first.
func (executor *DatastoreExecutor) datastoreID(ctx context.Context, tenant string, parameters map[string]any) (string, error) {
	locator, ok := property.ReadLocator(parameters["dataTableId"])
	if !ok || strings.TrimSpace(fmt.Sprint(locator.Value)) == "" {
		return "", fmt.Errorf("this operation needs a data table")
	}
	if !strings.EqualFold(locator.Mode, "name") {
		return fmt.Sprint(locator.Value), nil
	}
	definitions, err := executor.store.ListDatastores(ctx, tenant)
	if err != nil {
		return "", err
	}
	return datastore.ResolveByName(definitions, fmt.Sprint(locator.Value))
}

// datastoreValues builds one row's write from the mapper and the incoming
// item. Automatic mapping takes the intersection of the item's keys and the
// live schema — never the schema alone, which would blank untouched columns
// with explicit nulls — and manual mapping takes the mapped values as set.
func (executor *DatastoreExecutor) datastoreValues(ctx context.Context, tenant, id, operation string, parameters map[string]any, item workflow.Item) (map[string]any, error) {
	schema, err := executor.datastoreSchema(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	mapping, ok := property.ReadMapping(parameters["columns"])
	if !ok {
		mapping = property.Mapping{Mode: property.MappingAuto}
	}
	// Matching columns are not required: the filter identifies the rows
	// this writes, so the mapper only supplies values. Requiring them
	// would force every update to name a match column twice.
	declaration := node.ResourceMapperDeclaration{SupportsAutoMap: true}
	if err := property.ValidateMapping(declaration, schema, mapping, operation); err != nil {
		return nil, err
	}
	if mapping.Mode == "" || mapping.Mode == property.MappingAuto {
		values, _ := property.MappedColumns(schema, mapping, item.JSON)
		return values, nil
	}
	values := make(map[string]any, len(mapping.Values))
	for column, value := range mapping.Values {
		values[column] = value
	}
	return values, nil
}

// datastoreSchema reads the live column list the mapper validates and maps
// against. System columns ride along read-only, so they can be matched on
// and never written — the store refuses them on write regardless.
func (executor *DatastoreExecutor) datastoreSchema(ctx context.Context, tenant, id string) ([]property.MapperField, error) {
	definition, err := executor.store.GetDatastore(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	fields := []property.MapperField{
		{ID: "id", DisplayName: "id (number)", Type: "number", ReadOnly: true, CanBeUsedToMatch: true},
		{ID: "createdAt", DisplayName: "createdAt (date)", Type: "dateTime", ReadOnly: true, CanBeUsedToMatch: true},
		{ID: "updatedAt", DisplayName: "updatedAt (date)", Type: "dateTime", ReadOnly: true, CanBeUsedToMatch: true},
	}
	for _, column := range definition.Columns {
		fields = append(fields, property.MapperField{
			ID: column.Name, DisplayName: column.Name + " (" + string(column.Type) + ")",
			Type: datastoreMapperType(string(column.Type)), CanBeUsedToMatch: true,
		})
	}
	return fields, nil
}

// datastoreMapperType renders a datastore wire type in the mapper's own
// vocabulary. The loader package carries the same rule for the editor; this
// one serves the executor, which cannot import the editor's seam.
func datastoreMapperType(wire string) string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "date", "datetime":
		return "dateTime"
	default:
		return "string"
	}
}

// datastoreItemFilter compiles one item's conditions panel into the service
// envelope through the keyName-to-columnName mapping.
func datastoreItemFilter(parameters map[string]any) (*datastore.Filter, error) {
	conditions, err := datastoreFilterRows(parameters["filters"])
	if err != nil {
		return nil, err
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	match := textValue(parameters["match"], "any")
	return datastore.NodeConditionsToFilter(match, conditions)
}

// refuseEmptyNodeFilter refuses a filterless write the way the management
// API does: an empty filter matches nothing by refusal, never everything.
func refuseEmptyNodeFilter(filter *datastore.Filter) error {
	if filter == nil || len(filter.Conditions) == 0 {
		return fmt.Errorf("this operation needs at least one condition; a filterless write addresses the whole table")
	}
	return nil
}

// datastoreTableJSON renders a datastore as an item.
func datastoreTableJSON(definition datastore.Datastore) map[string]any {
	columns := make([]any, 0, len(definition.Columns))
	for _, column := range definition.Columns {
		columns = append(columns, map[string]any{"name": column.Name, "type": string(column.Type)})
	}
	return map[string]any{"id": definition.ID, "name": definition.Name, "columns": columns}
}

// isTrue tolerates the forms a boolean parameter can arrive in.
func isTrue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	case float64:
		return typed != 0
	default:
		return false
	}
}

// The datastore tool's server-owned bindings: the data table as an agent
// tool, beside the step node's own bindings above.
const (
	DatastoreToolNodeType   = "kilasflow.datastoreTool"
	DatastoreToolExecutorID = "core.datastoreTool"
)

// Bounds for one datastore tool call. The row cap keeps a whole table out of
// the model's context; the byte cap keeps wide rows from doing the same.
// Past either the call fails naming the cap, so the model can narrow the
// filter or lower the limit and try again.
const (
	datastoreToolDefaultLimit = 50
	datastoreToolMaxLimit     = 200
	datastoreToolMaxBytes     = 256 * 1024
)

// datastoreToolLegacyInsertHint is the rule an editor-built tool from before
// writes existed depends on, said where the editor shows it.
const datastoreToolLegacyInsertHint = "An Insert with no column values reads rows instead; set Operation to Get to make that explicit, or map columns to write."

// datastoreToolOperations are the row operations a tool performs: the read,
// and the four writes a model drives through $fromAI. Increment and the two
// branches stay on the step node — a counter a model bumps by guessing is a
// lost update, and a tool has one output, never a fork — and a tool never
// manages tables.
var datastoreToolOperations = map[string]bool{
	DatastoreOperationGet: true, DatastoreOperationInsert: true,
	DatastoreOperationUpdate: true, DatastoreOperationUpsert: true,
	DatastoreOperationDelete: true,
}

// datastoreToolNode exposes the data table as an agent tool, derived from
// the ordinary node rather than re-declared: same parameters, same locator,
// differing only in ports, tool naming, picker filing, and the operations a
// tool performs.
func datastoreToolNode() node.Definition {
	definition := toolVariantOf(datastoreNode(), DatastoreToolNodeType, "Data table Tool",
		"Reads and writes rows in a KilasFlow data table for an AI Agent. "+datastoreToolLegacyInsertHint, DatastoreToolExecutorID)
	// The branch operations' second output belongs to the step node: a tool
	// emits one descriptor on one port, never a fork.
	definition.PortsFor = nil
	definition.Parameters = datastoreToolParameters(definition.Parameters)
	definition.Validate = validateDatastoreToolConfiguration
	return definition
}

// datastoreToolParameters narrows the inherited resource and operation to
// what a tool performs, and defaults the operation to get.
//
// The default is get, not the step node's insert: a tool that names no
// operation has always read, and honouring insert there would turn every such
// tool into a writer on its next run. Each narrowed property is a fresh value
// with its own option slice, so nothing here can reach back into the step
// node's definition.
func datastoreToolParameters(inherited []node.PropertyDefinition) []node.PropertyDefinition {
	parameters := make([]node.PropertyDefinition, 0, len(inherited)+1)
	for _, declared := range inherited {
		switch declared.Key {
		case "resource":
			declared.Options = []node.PropertyOption{{Label: "Row", Value: datastoreResourceRow}}
		case "operation":
			options := make([]node.PropertyOption, 0, len(datastoreToolOperations))
			for _, option := range declared.Options {
				if datastoreToolOperations[option.Value] {
					options = append(options, option)
				}
			}
			declared.Options = options
			declared.Default = DatastoreOperationGet
			declared.Description = "What the tool does with its table. Get reads rows; the writes take their values from $fromAI. " +
				datastoreToolLegacyInsertHint
			parameters = append(parameters, declared, node.PropertyDefinition{
				Key: "legacyInsertNotice", Label: "An Insert with nothing to write reads", Kind: node.PropertyNotice,
				Description: datastoreToolLegacyInsertHint,
				VisibleWhen: datastoreShownFor(DatastoreOperationInsert),
			})
			continue
		}
		parameters = append(parameters, declared)
	}
	return parameters
}

// DatastoreToolOperation is the operation a datastore tool with these
// parameters performs, which is not always the one it stores.
//
// Legacy compatibility, and the one place it is decided: an Insert that
// declares nothing to write reads, exactly as the tool did before it wrote
// anything. Every tool dropped on the canvas until writes existed was stored
// with the step node's default operation, insert, and was used as a read —
// its author could not have meant an insert, because the tool never inserted,
// and an insert that writes nothing is never what anyone means. Turning those
// into writers would insert an empty row on every call and take away the read
// the agent was built around. Update and upsert get no such reading: nobody
// reached either by default, so one that writes nothing is refused instead.
//
// Exported for the n8n adapter, which must know which imported tools this
// rule would turn into reads.
func DatastoreToolOperation(parameters map[string]any) string {
	operation := strings.TrimSpace(textValue(parameters["operation"], DatastoreOperationGet))
	if operation == DatastoreOperationInsert && !datastoreToolWritesValues(parameters) {
		return DatastoreOperationGet
	}
	return operation
}

// datastoreToolWritesValues reports whether a tool declares something to
// write: a column value mapped by hand, or a $fromAI call in the mapping for
// the model to fill. A mapped column left empty declares nothing, the way the
// mapper's own required-column check reads it.
func datastoreToolWritesValues(parameters map[string]any) bool {
	if mapping, ok := property.ReadMapping(parameters["columns"]); ok && mapping.Mode == property.MappingManual {
		for _, value := range mapping.Values {
			if value != nil && value != "" {
				return true
			}
		}
	}
	calls, err := ai.ExtractFromAI(map[string]any{"columns": parameters["columns"]})
	return err == nil && len(calls) > 0
}

// validateDatastoreToolConfiguration checks the tool's own binding and then
// the operation it performs, by the step node's own rules.
func validateDatastoreToolConfiguration(n workflow.Node) error {
	if err := validateToolNameAndDescription(n); err != nil {
		return err
	}
	locator, ok := property.ReadLocator(n.Parameters["dataTableId"])
	if !ok || strings.TrimSpace(fmt.Sprint(locator.Value)) == "" {
		return fmt.Errorf("a datastore tool needs a data table")
	}
	// The table is bound when the tool is built, with no item to resolve
	// against, so an expression here has nothing to resolve against. Refused
	// at save, and again at run against the unresolved parameters.
	if expression.IsExpression(locator.Value) {
		return fmt.Errorf("a datastore tool needs a fixed data table, not an expression")
	}
	if err := checkDatastoreToolOperation(n.Parameters); err != nil {
		return err
	}
	return validateToolFromAI(n)
}

// checkDatastoreToolOperation holds a tool to what it performs. It runs at
// save and again when the tool is built, because a document can reach the
// executor without passing validation: an import, a direct repository write,
// a workflow saved before the rule existed.
func checkDatastoreToolOperation(parameters map[string]any) error {
	if resource := textValue(parameters["resource"], datastoreResourceRow); resource != datastoreResourceRow {
		return fmt.Errorf("a datastore tool acts on rows; resource %q is not one it supports", resource)
	}
	stored := strings.TrimSpace(textValue(parameters["operation"], DatastoreOperationGet))
	if !datastoreToolOperations[stored] {
		return fmt.Errorf("a datastore tool gets, inserts, updates, upserts or deletes rows; operation %q is not one of them", stored)
	}
	operation := DatastoreToolOperation(parameters)
	if (operation == DatastoreOperationUpdate || operation == DatastoreOperationUpsert) && !datastoreToolWritesValues(parameters) {
		return fmt.Errorf("a datastore tool that runs %s needs the values it writes: map at least one column, "+
			"with $fromAI for what the model supplies; to read rows, set the operation to Get", operation)
	}
	if err := refuseDatastoreToolFromAIColumns(parameters); err != nil {
		return err
	}
	// The step node's own rules for the operation the tool performs — a
	// matching write needs a condition, column names are literal — so the
	// tool and the step can never disagree about what is a valid write. The
	// copy carries the resolved operation: the step node's missing-operation
	// default is insert, which is exactly what a tool must not inherit.
	resolved := make(map[string]any, len(parameters)+2)
	for key, value := range parameters {
		resolved[key] = value
	}
	resolved["resource"] = datastoreResourceRow
	resolved["operation"] = operation
	return validateDatastoreConfiguration(workflow.Node{Parameters: resolved})
}

// refuseDatastoreToolFromAIColumns refuses a $fromAI call where a column name
// goes: a condition's keyName, or a matching column. Column names are
// literal-only on the step node, and the check that enforces it sees only
// expression markers — a bare $fromAI in a plain string is not one, yet the
// tool would substitute the model's argument into it, letting the model choose
// which column a delete filters on.
func refuseDatastoreToolFromAIColumns(parameters map[string]any) error {
	names := func(value string) bool {
		return strings.Contains(strings.ToLower(value), "$fromai")
	}
	conditions, err := datastoreFilterRows(parameters["filters"])
	if err != nil {
		return err
	}
	for index, condition := range conditions {
		if names(condition.KeyName) {
			return fmt.Errorf("filters.conditions[%d].keyName calls $fromAI, and a column name is never the model's to choose", index)
		}
	}
	if mapping, ok := property.ReadMapping(parameters["columns"]); ok {
		for _, column := range mapping.MatchingColumns {
			if names(column) {
				return fmt.Errorf("columns.matchingColumns calls $fromAI, and a column name is never the model's to choose")
			}
		}
	}
	return nil
}

// DatastoreToolExecutor emits the descriptor that exposes one data table to
// an AI Agent. The descriptor carries the table id, its frozen column list,
// the operation the tool performs and the node's parameters — never rows,
// and no secret, since the store needs no credential. That item is persisted
// in the execution record and streamed to the live feed, which is why the
// table travels as its identifier and never as its contents.
type DatastoreToolExecutor struct {
	store DatastoreStore
}

// NewDatastoreToolExecutor builds the data-table tool executor. A nil store
// refuses every run with the install message rather than dereferencing.
func NewDatastoreToolExecutor(store DatastoreStore) *DatastoreToolExecutor {
	return &DatastoreToolExecutor{store: store}
}

// Execute binds one table and emits its tool descriptor.
func (executor *DatastoreToolExecutor) Execute(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if executor.store == nil {
		return nil, fmt.Errorf("node %q: datastore storage is not available on this server", ir.Name)
	}
	tenant := strings.TrimSpace(request.Execution.TenantID)
	if tenant == "" {
		return nil, fmt.Errorf("node %q: this run carries no tenant", ir.Name)
	}
	// The backstop for the compile-time refusal, against the unresolved
	// parameters: a document can reach an executor without passing
	// validation, through an import or a direct repository write.
	locator, ok := property.ReadLocator(ir.Parameters["dataTableId"])
	if !ok || strings.TrimSpace(fmt.Sprint(locator.Value)) == "" {
		return nil, fmt.Errorf("node %q: a datastore tool needs a data table", ir.Name)
	}
	if expression.IsExpression(locator.Value) {
		return nil, fmt.Errorf("node %q: a datastore tool needs a fixed data table, not an expression", ir.Name)
	}
	if err := checkDatastoreToolOperation(ir.Parameters); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if _, err := ai.ExtractFromAI(ir.Parameters); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	id, err := datastoreToolID(ctx, executor.store, tenant, locator)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	definition, err := executor.store.GetDatastore(ctx, tenant, id)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	columns := make([]any, 0, len(definition.Columns))
	for _, column := range definition.Columns {
		columns = append(columns, map[string]any{"name": column.Name, "type": string(column.Type)})
	}
	// The parameters travel as the workflow tool's do: a write substitutes
	// the model's $fromAI arguments into them and runs the step executor.
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":          toolKindDatastore,
		"name":          toolNameFor(ir),
		"description":   textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":      ir.Name,
		"datastoreId":   definition.ID,
		"datastoreName": definition.Name,
		"columns":       columns,
		"operation":     DatastoreToolOperation(ir.Parameters),
		"parameters":    parameters,
	}}}}}, nil
}

// datastoreToolID resolves the locator to a catalogue id. By-Name resolves
// through the tenant's own list, so one tenant's name can never address
// another tenant's table, and through datastore.ResolveByName, so a name two
// tables share is refused; By-ID is checked against the tenant by the store
// itself on the next call.
func datastoreToolID(ctx context.Context, store DatastoreStore, tenant string, locator property.Locator) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(locator.Mode)), "name") {
		return fmt.Sprint(locator.Value), nil
	}
	definitions, err := store.ListDatastores(ctx, tenant)
	if err != nil {
		return "", err
	}
	return datastore.ResolveByName(definitions, fmt.Sprint(locator.Value))
}

// datastoreStoreOf binds the executor, keeping a typed-nil engine from
// becoming a non-nil store interface that panics on first use.
func datastoreStoreOf(engine *datastore.Engine) DatastoreStore {
	var store DatastoreStore
	if engine != nil {
		store = engine
	}
	return store
}

// datastoreToolExecutorOf binds the tool executor the way datastoreExecutorOf
// binds the step node's.
func datastoreToolExecutorOf(settings executorSettings) engine.Executor {
	return NewDatastoreToolExecutor(datastoreStoreOf(settings.datastoreEngine))
}

// datastoreToolColumn is one frozen column the tool's schema enumerates.
type datastoreToolColumn struct {
	Name string
	Type string
}

// datastoreToolColumns reads the frozen column list back out of a descriptor.
// Anything shaped wrong is skipped rather than misread; a descriptor left
// with no columns is refused by the caller, not repaired here.
func datastoreToolColumns(value any) []datastoreToolColumn {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	columns := make([]datastoreToolColumn, 0, len(list))
	for _, entry := range list {
		record, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := record["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		columnType, _ := record["type"].(string)
		columns = append(columns, datastoreToolColumn{Name: name, Type: columnType})
	}
	return columns
}

// datastoreTool reads or writes rows of its bound table for the model. The
// table is fixed at construction: the schema carries no datastore identifier,
// and Invoke takes none, so an argument naming another table changes nothing.
type datastoreTool struct {
	name          string
	description   string
	nodeName      string
	agentNode     string
	tenant        string
	datastoreID   string
	datastoreName string
	columns       []datastoreToolColumn
	store         DatastoreStore
	// operation is what the tool performs: get, or one of the writes.
	operation string
	// parameters is the node's own configuration, the template a write
	// substitutes the model's $fromAI arguments into. Never written to.
	parameters map[string]any
	request    engine.Request
}

// writes reports whether the tool performs a write rather than the read.
func (tool *datastoreTool) writes() bool {
	return tool.operation != DatastoreOperationGet
}

// datastoreToolOperators is the fixed vocabulary a filter condition may use,
// the same set the step node's conditions panel offers.
var datastoreToolOperators = []string{"eq", "neq", "like", "ilike", "gt", "gte", "lt", "lte", "isEmpty", "isNotEmpty"}

// Definition describes the tool to the model.
//
// Unlike httpRequestTool's open input object — one untyped property the model
// fills wholesale — the read schema is closed: every structural choice is an
// enum over the bound table's own columns and the fixed operator vocabulary,
// and only the compared values are free. The schema is a hint a provider may
// ignore, so Invoke re-checks every column and operator before any statement
// is built.
func (tool *datastoreTool) Definition() ai.ToolDefinition {
	names := tool.columnNames()
	if tool.writes() {
		return tool.writeDefinition(names)
	}
	description := tool.description
	if tool.datastoreName != "" {
		description += " Queries the " + strconv.Quote(tool.datastoreName) + " data table (columns: " + strings.Join(names, ", ") + ")."
	}
	return ai.ToolDefinition{
		Name:        tool.name,
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"match": map[string]any{
					"type":        "string",
					"enum":        []string{"any", "all"},
					"description": "Whether any or all of the conditions must hold for a row to match.",
				},
				"conditions": map[string]any{
					"type":        "array",
					"description": "Which rows to return. Omit for the whole table, up to the limit.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"columnName": map[string]any{
								"type":        "string",
								"enum":        names,
								"description": "The column to compare, one of this table's columns.",
							},
							"condition": map[string]any{
								"type":        "string",
								"enum":        datastoreToolOperators,
								"description": "How to compare the column to the value.",
							},
							"value": map[string]any{
								"description": "The value to compare against. Omit for isEmpty and isNotEmpty.",
							},
						},
						"required":             []string{"columnName", "condition"},
						"additionalProperties": false,
					},
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     float64(1),
					"maximum":     float64(datastoreToolMaxLimit),
					"description": "How many rows to return at most. Defaults to 50.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// writeDefinition describes a write. The schema is derived rather than
// hand-written, as httpRequestTool's is: every $fromAI call in the parameters
// contributes a typed property carrying its description, and nothing else is
// offered. It is closed like the read schema — a write whose parameters call
// $fromAI nowhere takes no arguments at all — and Invoke enforces the same
// closure, since a provider may ignore the schema.
func (tool *datastoreTool) writeDefinition(names []string) ai.ToolDefinition {
	parameters := map[string]any{"type": "object", "properties": map[string]any{}}
	if calls, err := ai.ExtractFromAI(tool.parameters); err == nil && len(calls) > 0 {
		parameters = ai.FromAISchema(calls)
	}
	parameters["additionalProperties"] = false
	description := tool.description
	if tool.datastoreName != "" {
		table := strconv.Quote(tool.datastoreName) + " data table (columns: " + strings.Join(names, ", ") + ")"
		switch tool.operation {
		case DatastoreOperationInsert:
			description += " Inserts one row into the " + table + "."
		case DatastoreOperationUpdate:
			description += " Updates the matching rows of the " + table + "."
		case DatastoreOperationUpsert:
			description += " Updates the matching rows of the " + table + ", or inserts one when none match."
		case DatastoreOperationDelete:
			description += " Deletes the matching rows from the " + table + "."
		}
	}
	return ai.ToolDefinition{Name: tool.name, Description: description, Parameters: parameters}
}

// datastoreToolArgs is one tool call's arguments. Unknown fields — including
// a datastore identifier, which the schema never declares — decode to
// nothing: the call always reads the bound table.
type datastoreToolArgs struct {
	Match      string                  `json:"match"`
	Conditions []datastoreToolCallCond `json:"conditions"`
	Limit      int                     `json:"limit"`
}

// datastoreToolCallCond is one filter row in a tool call's arguments.
type datastoreToolCallCond struct {
	ColumnName string `json:"columnName"`
	Condition  string `json:"condition"`
	Value      any    `json:"value"`
}

// Invoke runs the tool's operation against the bound table: a write goes to
// invokeWrite, and the read below runs one filtered read and returns the rows
// as data. A row whose contents read as an instruction is returned as data
// like any other value: it widens neither the schema nor what the next call
// accepts, because neither is ever derived from row contents.
func (tool *datastoreTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if tool.writes() {
		return tool.invokeWrite(ctx, arguments)
	}
	var args datastoreToolArgs
	if len(bytes.TrimSpace(arguments)) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return "", fmt.Errorf("node %q: tool %q: invalid arguments: %w", tool.agentNode, tool.name, err)
		}
	}
	known := make(map[string]bool, len(tool.columns))
	for _, column := range tool.columns {
		known[column.Name] = true
	}
	conditions := make([]datastore.NodeFilterCondition, 0, len(args.Conditions))
	for _, cond := range args.Conditions {
		if !known[cond.ColumnName] {
			return "", fmt.Errorf("node %q: tool %q: unknown column %q (the table has columns: %s)", tool.agentNode, tool.name, cond.ColumnName, strings.Join(tool.columnNames(), ", "))
		}
		if !datastoreToolOperatorOK(cond.Condition) {
			return "", fmt.Errorf("node %q: tool %q: unknown condition %q (supported: %s)", tool.agentNode, tool.name, cond.Condition, strings.Join(datastoreToolOperators, ", "))
		}
		conditions = append(conditions, datastore.NodeFilterCondition{
			KeyName:   cond.ColumnName,
			Condition: datastore.Condition(cond.Condition),
			KeyValue:  cond.Value,
		})
	}
	var filter *datastore.Filter
	if len(conditions) > 0 {
		match := args.Match
		if strings.TrimSpace(match) == "" {
			match = "any"
		}
		var err error
		filter, err = datastore.NodeConditionsToFilter(match, conditions)
		if err != nil {
			return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = datastoreToolDefaultLimit
	}
	if limit > datastoreToolMaxLimit {
		limit = datastoreToolMaxLimit
	}
	page, err := tool.store.List(ctx, tool.tenant, tool.datastoreID, datastore.RowQuery{Filter: filter, Limit: limit})
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	rows := page.Rows
	if rows == nil {
		rows = []datastore.Row{}
	}
	encoded, err := json.Marshal(map[string]any{"rows": rows, "truncated": page.NextCursor != ""})
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: encode tool result: %w", tool.agentNode, tool.name, err)
	}
	if len(encoded) > datastoreToolMaxBytes {
		return "", fmt.Errorf("node %q: tool %q: the result is %d bytes, past the %d-byte cap: narrow the filter or lower the limit", tool.agentNode, tool.name, len(encoded), datastoreToolMaxBytes)
	}
	return string(encoded), nil
}

// invokeWrite runs one write through the step node's own executor, so the
// tool inherits the mapper, the condition rules and the whole-table refusal
// rather than reimplementing any of them.
//
// Three things are fixed before the model's arguments reach it. Only the keys
// the parameters declare through $fromAI cross — the schema is closed, and a
// provider that ignores it cannot widen what an automatic mapping writes. The
// substitution goes into a copy, never into the tool's own template, for the
// reason httpRequestTool's does: a template overwritten by the first call
// repeats that call forever. And the table is pinned to the one the
// descriptor resolved, by id, so the table the schema describes is the table
// written — a rename between build and call cannot move it, and no argument
// can.
//
// A failure returns as a tool error, never as a result the model could read
// as success.
func (tool *datastoreTool) invokeWrite(ctx context.Context, arguments json.RawMessage) (string, error) {
	supplied := map[string]any{}
	if len(bytes.TrimSpace(arguments)) > 0 {
		if err := json.Unmarshal(arguments, &supplied); err != nil {
			return "", fmt.Errorf("node %q: tool %q: invalid arguments: %w", tool.agentNode, tool.name, err)
		}
	}
	calls, err := ai.ExtractFromAI(tool.parameters)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	declared := make(map[string]any, len(calls))
	for _, call := range calls {
		if value, present := supplied[call.Key]; present {
			declared[call.Key] = value
		}
	}
	parameters, err := ai.SubstituteFromAI(tool.parameters, declared)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	parameters["resource"] = datastoreResourceRow
	parameters["operation"] = tool.operation
	parameters["dataTableId"] = property.WriteLocator(property.Locator{Mode: "id", Value: tool.datastoreID})
	node := workflow.IRNode{
		ID: tool.agentNode + ":" + tool.name, Name: tool.nodeName,
		Type: DatastoreNodeType, TypeVersion: DatastoreVersion, Parameters: parameters,
		Definition: workflow.NodeDefinition{
			Type: DatastoreNodeType, Version: DatastoreVersion,
			Outputs: mainOutput(), ExecutorID: DatastoreExecutorID,
		},
	}
	output, err := NewDatastoreExecutor(tool.store).Execute(ctx, node, workflow.NodeInput{"main": {{JSON: declared}}}, tool.request)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: %w", tool.agentNode, tool.name, err)
	}
	rows := make([]any, 0)
	if len(output) > 0 {
		for _, item := range output[0] {
			rows = append(rows, item.JSON)
		}
	}
	result := map[string]any{"operation": tool.operation, "affected": len(rows), "rows": rows, "truncated": false}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("node %q: tool %q: encode tool result: %w", tool.agentNode, tool.name, err)
	}
	if len(encoded) > datastoreToolMaxBytes {
		// The write has committed, so the cap cannot be a failure the way it
		// is for a read: that would tell the model its write did not happen.
		// The count stays and the rows go.
		result["rows"] = []any{}
		result["truncated"] = true
		if encoded, err = json.Marshal(result); err != nil {
			return "", fmt.Errorf("node %q: tool %q: encode tool result: %w", tool.agentNode, tool.name, err)
		}
	}
	return string(encoded), nil
}

// columnNames lists the frozen columns for refusal messages, so a refused
// call tells the model what it may name instead.
func (tool *datastoreTool) columnNames() []string {
	names := make([]string, 0, len(tool.columns))
	for _, column := range tool.columns {
		names = append(names, column.Name)
	}
	return names
}

// datastoreToolOperatorOK reports whether the operator belongs to the fixed
// vocabulary. A switch rather than a set: an unrecognised operator is an
// error naming the supported set, never a dropped predicate.
func datastoreToolOperatorOK(operator string) bool {
	switch datastore.Condition(operator) {
	case datastore.CondEq, datastore.CondNeq, datastore.CondLike, datastore.CondILike,
		datastore.CondGt, datastore.CondGte, datastore.CondLt, datastore.CondLte,
		datastore.CondIsEmpty, datastore.CondIsNotEmpty:
		return true
	default:
		return false
	}
}
