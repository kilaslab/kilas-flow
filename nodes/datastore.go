package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/datastore"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
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

// Datastore row operations: the seven Row Actions of design-refs shot 26.
const (
	DatastoreOperationInsert      = "insert"
	DatastoreOperationGet         = "get"
	DatastoreOperationUpdate      = "update"
	DatastoreOperationUpsert      = "upsert"
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
		Description: "Stores rows in a KilasFlow data table: insert, read, update, upsert and delete without holding a database credential.",
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
				Key: "match", Label: "Must Match", Kind: node.PropertyOptions, Default: "any",
				Description: "Whether any or all of the conditions must hold for a row to match.",
				Options: []node.PropertyOption{
					{Label: "Any Condition", Value: "any"},
					{Label: "All Conditions", Value: "all"},
				},
				VisibleWhen: datastoreShownFor(DatastoreOperationGet, DatastoreOperationUpdate,
					DatastoreOperationUpsert, DatastoreOperationDelete,
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
					DatastoreOperationUpsert, DatastoreOperationDelete,
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
func datastorePortsFor(parameters map[string]any, _ workflow.TypeVersion) ([]workflow.Port, []workflow.Port) {
	switch textValue(parameters["operation"], DatastoreOperationInsert) {
	case DatastoreOperationIfExists, DatastoreOperationIfNotExists:
		return mainInput(), []workflow.Port{
			{Name: "main", Kind: workflow.ConnectionMain},
			{Name: "main", Kind: workflow.ConnectionMain},
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
	DatastoreOperationDelete: true, DatastoreOperationIfExists: true,
	DatastoreOperationIfNotExists: true,
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
	DatastoreOperationUpsert: true, DatastoreOperationDelete: true,
	DatastoreOperationIfExists: true, DatastoreOperationIfNotExists: true,
}

// datastoreMatchingOperations need at least one condition: without one the
// operation addresses the whole table, which the management API refuses and
// the node must refuse first. Get without conditions reads the table, which
// is what Return All and the per-row limit are for.
var datastoreMatchingOperations = map[string]bool{
	DatastoreOperationUpdate: true, DatastoreOperationUpsert: true,
	DatastoreOperationDelete: true, DatastoreOperationIfExists: true,
	DatastoreOperationIfNotExists: true,
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
// slots: every filter condition's keyName, a mapper value that is itself a
// marker (its keys would then come from the incoming item), and matching
// column entries. Values stay expression-capable — that is what binding is
// for — and the engine re-checks every resolved name against the catalogue
// before it is quoted, so a literal that names no column still fails.
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

// runBranch runs If Exists and If Not Exists. A match emits the rows on the
// first port for If Exists and on the second for If Not Exists; a miss emits
// the incoming item on the other port, so downstream always has an item
// whose lineage names the input that was tested.
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
	miss := []workflow.Item{{JSON: map[string]any(item.JSON), Paired: lineage}}
	if len(page.Rows) > 0 {
		if operation == DatastoreOperationIfExists {
			return emit(page.Rows), []workflow.Item{}, true, nil
		}
		return []workflow.Item{}, miss, true, nil
	}
	if operation == DatastoreOperationIfNotExists {
		return emit([]datastore.Row{}), miss, true, nil
	}
	return []workflow.Item{}, miss, true, nil
}

// datastoreID resolves the locator to a catalogue id. From-list and By-ID
// already carry it; By-Name resolves through the tenant's own list, so one
// tenant's name can never address another tenant's table.
func (executor *DatastoreExecutor) datastoreID(ctx context.Context, tenant string, parameters map[string]any) (string, error) {
	locator, ok := property.ReadLocator(parameters["dataTableId"])
	if !ok || strings.TrimSpace(fmt.Sprint(locator.Value)) == "" {
		return "", fmt.Errorf("this operation needs a data table")
	}
	if !strings.EqualFold(locator.Mode, "name") {
		return fmt.Sprint(locator.Value), nil
	}
	want := fmt.Sprint(locator.Value)
	definitions, err := executor.store.ListDatastores(ctx, tenant)
	if err != nil {
		return "", err
	}
	for _, definition := range definitions {
		if strings.EqualFold(definition.Name, want) {
			return definition.ID, nil
		}
	}
	return "", fmt.Errorf("datastore: unknown datastore %q", want)
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
