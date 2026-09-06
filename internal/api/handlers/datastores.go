package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/datastore"
)

// Datastores is the REST surface over the row store: tables, columns and
// rows. Every operation resolves its tenant through the injected resolver
// and passes the scope's ID into the engine, whose lookup clauses the
// catalogue read on tenant and id together — so a request naming another
// tenant's datastore reads as unknown and answers 404, never as a row
// belonging to someone else.
type Datastores struct {
	store   *datastore.Engine
	tenants TenantResolver
}

// NewDatastores constructs the datastore handler.
func NewDatastores(store *datastore.Engine, tenants TenantResolver) *Datastores {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Datastores{store: store, tenants: tenants}
}

// DatastoreColumnResource is one user column of a datastore.
type DatastoreColumnResource struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// DatastoreResource is one datastore with its live columns in definition
// order.
type DatastoreResource struct {
	ID      string                    `json:"id"`
	Name    string                    `json:"name"`
	Columns []DatastoreColumnResource `json:"columns"`
}

type datastorePathInput struct {
	ID string `path:"id" minLength:"1" doc:"Datastore identifier"`
}

type datastoreColumnInput struct {
	Name string `json:"name" minLength:"1" doc:"Column name"`
	Type string `json:"type" minLength:"1" doc:"Column type: string, number, boolean or date"`
}

type createDatastoreInput struct {
	Body struct {
		Name    string                 `json:"name" minLength:"1" doc:"Display name"`
		Columns []datastoreColumnInput `json:"columns,omitempty" doc:"User columns; empty means system columns only"`
	}
}

type datastoreOutput struct {
	Body DatastoreResource
}

type createdDatastoreOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     DatastoreResource
}

type datastoreListOutput struct {
	Body struct {
		Items []DatastoreResource `json:"items"`
	}
}

type renameDatastoreInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Name string `json:"name" minLength:"1" doc:"New display name"`
	}
}

type deletedDatastoreOutput struct {
	Status int `status:"204"`
}

type addDatastoreColumnInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body datastoreColumnInput
}

type renameDatastoreColumnInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Name string `path:"name" minLength:"1" doc:"Column name"`
	Body struct {
		Name string `json:"name" minLength:"1" doc:"New column name"`
	}
}

type datastoreColumnOutput struct {
	Body DatastoreColumnResource
}

type clearedDatastoreOutput struct {
	Body struct {
		Deleted int64 `json:"deleted"`
	}
}

type insertRowInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Values map[string]any `json:"values" doc:"User column values keyed by column name"`
	}
}

type createdRowOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     datastore.Row
}

type listRowsInput struct {
	ID         string   `path:"id" minLength:"1" doc:"Datastore identifier"`
	Limit      int      `query:"limit" doc:"Page size; clamped to the store maximum"`
	Cursor     string   `query:"cursor" doc:"Opaque cursor from a previous page"`
	Match      string   `query:"match" doc:"Any Condition matches any, All Conditions matches all; default Any Condition"`
	Columns    []string `query:"columnName" doc:"Filter column, repeated; zipped with condition and value by position"`
	Conditions []string `query:"condition" doc:"Filter operator, repeated"`
	Values     []string `query:"value" doc:"Filter value as JSON, repeated; unquoted text stays a string"`
}

type rowListOutput struct {
	Body struct {
		Items      []datastore.Row `json:"items"`
		NextCursor string          `json:"nextCursor,omitempty"`
	}
}

type rowPathInput struct {
	ID    string `path:"id" minLength:"1" doc:"Datastore identifier"`
	RowID int64  `path:"rowId" doc:"Row identifier"`
}

type rowOutput struct {
	Body datastore.Row
}

type updateRowsInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Filter datastore.Filter `json:"filter" doc:"Rows to update; an empty filter is refused"`
		Values map[string]any   `json:"values" doc:"Columns to set"`
	}
}

type updateRowsOutput struct {
	Body struct {
		Matched int64           `json:"matched"`
		Rows    []datastore.Row `json:"rows"`
	}
}

type deleteRowsInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Filter datastore.Filter `json:"filter" doc:"Rows to delete; an empty filter is refused and removes nothing"`
	}
}

type deleteRowsOutput struct {
	Body struct {
		Deleted int64           `json:"deleted"`
		Rows    []datastore.Row `json:"rows"`
	}
}

type upsertRowInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Filter datastore.Filter `json:"filter" doc:"Rows to match; on no match one row is inserted"`
		Values map[string]any   `json:"values" doc:"Columns to set or to insert with"`
	}
}

type upsertRowOutput struct {
	Body struct {
		Inserted bool            `json:"inserted"`
		Matched  int64           `json:"matched"`
		Rows     []datastore.Row `json:"rows"`
	}
}

// Register wires the three datastore path families plus the CSV transfer
// pair, which lives in datastores_csv.go and is mounted here so every
// datastore operation registers in one place.
func (handler *Datastores) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-datastores", Method: http.MethodGet, Path: "/datastores",
		Summary: "List datastores", Description: "Returns every data table in the workspace.", Tags: []string{"Datastores"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "create-datastore", Method: http.MethodPost, Path: "/datastores", DefaultStatus: http.StatusCreated,
		Summary: "Create a datastore", Description: "Creates a data table; columns are added afterwards.", Tags: []string{"Datastores"},
	}, handler.Create)
	huma.Register(api, huma.Operation{
		OperationID: "get-datastore", Method: http.MethodGet, Path: "/datastores/{id}",
		Summary: "Get a datastore", Description: "Returns one data table with its columns.", Tags: []string{"Datastores"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "rename-datastore", Method: http.MethodPut, Path: "/datastores/{id}",
		Summary: "Rename a datastore", Description: "Renames a data table; columns change through the column endpoints.", Tags: []string{"Datastores"},
	}, handler.Rename)
	huma.Register(api, huma.Operation{
		OperationID: "delete-datastore", Method: http.MethodDelete, Path: "/datastores/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a datastore", Description: "Removes a data table and every row it holds.", Tags: []string{"Datastores"},
	}, handler.Delete)
	huma.Register(api, huma.Operation{
		OperationID: "clear-datastore", Method: http.MethodPost, Path: "/datastores/{id}/clear",
		Summary: "Clear a datastore", Description: "Removes every row and keeps the schema.", Tags: []string{"Datastores"},
	}, handler.Clear)
	huma.Register(api, huma.Operation{
		OperationID: "add-datastore-column", Method: http.MethodPost, Path: "/datastores/{id}/columns",
		Summary: "Add a column", Description: "Appends one user column to a data table.", Tags: []string{"Datastore columns"},
	}, handler.AddColumn)
	huma.Register(api, huma.Operation{
		OperationID: "rename-datastore-column", Method: http.MethodPut, Path: "/datastores/{id}/columns/{name}",
		Summary: "Rename a column", Description: "Renames one user column of a data table.", Tags: []string{"Datastore columns"},
	}, handler.RenameColumn)
	huma.Register(api, huma.Operation{
		OperationID: "delete-datastore-column", Method: http.MethodDelete, Path: "/datastores/{id}/columns/{name}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a column", Description: "Removes one user column from a data table.", Tags: []string{"Datastore columns"},
	}, handler.DropColumn)
	huma.Register(api, huma.Operation{
		OperationID: "list-datastore-rows", Method: http.MethodGet, Path: "/datastores/{id}/rows",
		Summary: "List rows", Description: "Returns one page of rows in id order with the cursor for the next.", Tags: []string{"Datastore rows"},
	}, handler.ListRows)
	huma.Register(api, huma.Operation{
		OperationID: "insert-datastore-row", Method: http.MethodPost, Path: "/datastores/{id}/rows", DefaultStatus: http.StatusCreated,
		Summary: "Insert a row", Description: "Writes one row and reads it back.", Tags: []string{"Datastore rows"},
	}, handler.InsertRow)
	huma.Register(api, huma.Operation{
		OperationID: "get-datastore-row", Method: http.MethodGet, Path: "/datastores/{id}/rows/{rowId}",
		Summary: "Get a row", Description: "Returns one row by id.", Tags: []string{"Datastore rows"},
	}, handler.GetRow)
	huma.Register(api, huma.Operation{
		OperationID: "update-datastore-rows", Method: http.MethodPut, Path: "/datastores/{id}/rows",
		Summary: "Update rows", Description: "Sets columns on every row matching the filter. One statement, atomic per row on both drivers: concurrent writers never interleave inside a row and the last writer wins; no row lock is taken.", Tags: []string{"Datastore rows"},
	}, handler.UpdateRows)
	huma.Register(api, huma.Operation{
		OperationID: "delete-datastore-rows", Method: http.MethodDelete, Path: "/datastores/{id}/rows",
		Summary: "Delete rows", Description: "Removes every row matching the filter. An empty filter is refused and removes nothing. One statement, atomic per row on both drivers: the last writer wins and no row lock is taken.", Tags: []string{"Datastore rows"},
	}, handler.DeleteRows)
	huma.Register(api, huma.Operation{
		OperationID: "upsert-datastore-row", Method: http.MethodPost, Path: "/datastores/{id}/rows/upsert",
		Summary: "Upsert rows", Description: "Updates every row matching the filter, or inserts one row when nothing matches. Read-then-write in no single transaction: two concurrent upserts against the same filter may both insert, so a counter that must not lose writes uses increment instead.", Tags: []string{"Datastore rows"},
	}, handler.UpsertRow)
	handler.registerDatastoreTransfer(api)
}

// List returns every datastore in the tenant.
func (handler *Datastores) List(ctx context.Context, _ *struct{}) (*datastoreListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	definitions, err := handler.store.ListDatastores(ctx, handler.tenants.Resolve(ctx).ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	items := make([]DatastoreResource, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, datastoreResource(definition))
	}
	out := &datastoreListOutput{}
	out.Body.Items = items
	return out, nil
}

// Create stores a datastore and computes nothing else: columns arrive empty
// and are added through the column endpoints, matching the create dialog
// that takes a name only.
func (handler *Datastores) Create(ctx context.Context, input *createDatastoreInput) (*createdDatastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	columns := make([]datastore.ColumnInput, 0, len(input.Body.Columns))
	for _, column := range input.Body.Columns {
		columns = append(columns, datastore.ColumnInput{Name: column.Name, Type: column.Type})
	}
	definition, err := handler.store.Create(ctx, handler.tenants.Resolve(ctx).ID, input.Body.Name, columns)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &createdDatastoreOutput{
		Status:   http.StatusCreated,
		Location: "/api/v1/datastores/" + definition.ID,
		Body:     datastoreResource(*definition),
	}, nil
}

// Get returns one datastore in the tenant.
func (handler *Datastores) Get(ctx context.Context, input *datastorePathInput) (*datastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	definition, err := handler.store.GetDatastore(ctx, handler.tenants.Resolve(ctx).ID, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &datastoreOutput{Body: datastoreResource(*definition)}, nil
}

// Rename changes a datastore's display name. It is deliberately not an update:
// the table-level operation renames and nothing else.
func (handler *Datastores) Rename(ctx context.Context, input *renameDatastoreInput) (*datastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	tenant := handler.tenants.Resolve(ctx).ID
	if err := handler.store.RenameDatastore(ctx, tenant, input.ID, input.Body.Name); err != nil {
		return nil, handler.problem(err)
	}
	definition, err := handler.store.GetDatastore(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &datastoreOutput{Body: datastoreResource(*definition)}, nil
}

// Delete removes a datastore after proving the tenant owns it. The engine's
// drop converges unknown ids to a no-op, so reading first is what turns
// another tenant's id into a 404 instead of a silent 204.
func (handler *Datastores) Delete(ctx context.Context, input *datastorePathInput) (*deletedDatastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	tenant := handler.tenants.Resolve(ctx).ID
	if _, err := handler.store.GetDatastore(ctx, tenant, input.ID); err != nil {
		return nil, handler.problem(err)
	}
	if err := handler.store.Drop(ctx, tenant, input.ID); err != nil {
		return nil, handler.problem(err)
	}
	return &deletedDatastoreOutput{Status: http.StatusNoContent}, nil
}

// Clear removes every row and keeps the schema.
func (handler *Datastores) Clear(ctx context.Context, input *datastorePathInput) (*clearedDatastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	deleted, err := handler.store.Clear(ctx, handler.tenants.Resolve(ctx).ID, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	out := &clearedDatastoreOutput{}
	out.Body.Deleted = deleted
	return out, nil
}

// AddColumn appends one user column.
func (handler *Datastores) AddColumn(ctx context.Context, input *addDatastoreColumnInput) (*datastoreColumnOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	tenant := handler.tenants.Resolve(ctx).ID
	if err := handler.store.AddColumn(ctx, tenant, input.ID, datastore.ColumnInput{Name: input.Body.Name, Type: input.Body.Type}); err != nil {
		return nil, handler.problem(err)
	}
	return &datastoreColumnOutput{Body: DatastoreColumnResource{Name: input.Body.Name, Type: input.Body.Type}}, nil
}

// RenameColumn renames one user column.
func (handler *Datastores) RenameColumn(ctx context.Context, input *renameDatastoreColumnInput) (*datastoreColumnOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.store.RenameColumn(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.Name, input.Body.Name); err != nil {
		return nil, handler.problem(err)
	}
	return &datastoreColumnOutput{Body: DatastoreColumnResource{Name: input.Body.Name}}, nil
}

// DropColumn removes one user column.
func (handler *Datastores) DropColumn(ctx context.Context, input *datastoreColumnPathInput) (*deletedDatastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.store.DropColumn(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.Name); err != nil {
		return nil, handler.problem(err)
	}
	return &deletedDatastoreOutput{Status: http.StatusNoContent}, nil
}

// InsertRow writes one row and reads it back.
func (handler *Datastores) InsertRow(ctx context.Context, input *insertRowInput) (*createdRowOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	row, err := handler.store.Insert(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.Body.Values)
	if err != nil {
		return nil, handler.problem(err)
	}
	id, _ := row["id"].(int64)
	return &createdRowOutput{
		Status:   http.StatusCreated,
		Location: "/api/v1/datastores/" + input.ID + "/rows/" + strconv.FormatInt(id, 10),
		Body:     row,
	}, nil
}

// ListRows returns one page of rows in id order.
func (handler *Datastores) ListRows(ctx context.Context, input *listRowsInput) (*rowListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	filter, err := queryFilter(input)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	page, err := handler.store.List(ctx, handler.tenants.Resolve(ctx).ID, input.ID, datastore.RowQuery{
		Filter: filter, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	out := &rowListOutput{}
	out.Body.Items = page.Rows
	out.Body.NextCursor = page.NextCursor
	if out.Body.Items == nil {
		out.Body.Items = []datastore.Row{}
	}
	return out, nil
}

// GetRow returns one row by id.
func (handler *Datastores) GetRow(ctx context.Context, input *rowPathInput) (*rowOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	row, err := handler.store.Get(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.RowID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &rowOutput{Body: row}, nil
}

// UpdateRows sets columns on every row matching the filter.
func (handler *Datastores) UpdateRows(ctx context.Context, input *updateRowsInput) (*updateRowsOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	if err := refuseEmptyFilter(&input.Body.Filter); err != nil {
		return nil, err
	}
	result, err := handler.store.Update(ctx, handler.tenants.Resolve(ctx).ID, input.ID, &input.Body.Filter, input.Body.Values, false)
	if err != nil {
		return nil, handler.problem(err)
	}
	out := &updateRowsOutput{}
	out.Body.Matched = result.Matched
	out.Body.Rows = result.Rows
	if out.Body.Rows == nil {
		out.Body.Rows = []datastore.Row{}
	}
	return out, nil
}

// DeleteRows removes every row matching the filter.
func (handler *Datastores) DeleteRows(ctx context.Context, input *deleteRowsInput) (*deleteRowsOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	if err := refuseEmptyFilter(&input.Body.Filter); err != nil {
		return nil, err
	}
	result, err := handler.store.Delete(ctx, handler.tenants.Resolve(ctx).ID, input.ID, &input.Body.Filter, false)
	if err != nil {
		return nil, handler.problem(err)
	}
	out := &deleteRowsOutput{}
	out.Body.Deleted = int64(len(result.Rows))
	out.Body.Rows = result.Rows
	if out.Body.Rows == nil {
		out.Body.Rows = []datastore.Row{}
	}
	return out, nil
}

// UpsertRow updates every row matching the filter, or inserts one row when
// nothing matches.
func (handler *Datastores) UpsertRow(ctx context.Context, input *upsertRowInput) (*upsertRowOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	if err := refuseEmptyFilter(&input.Body.Filter); err != nil {
		return nil, err
	}
	result, err := handler.store.Upsert(ctx, handler.tenants.Resolve(ctx).ID, input.ID, &input.Body.Filter, input.Body.Values, false)
	if err != nil {
		return nil, handler.problem(err)
	}
	out := &upsertRowOutput{}
	out.Body.Inserted = result.Inserted
	out.Body.Rows = result.Rows
	if out.Body.Rows == nil {
		out.Body.Rows = []datastore.Row{}
	} else {
		out.Body.Matched = int64(len(result.Rows))
		if result.Inserted {
			out.Body.Matched = 0
		}
	}
	return out, nil
}

// refuseEmptyFilter closes the trap the ticket names: Go decodes an absent
// filters key and an explicit [] to the same nil slice, so guarding on the
// compiled predicate's emptiness is the only check that refuses both {} and
// {"type":"and","filters":[]} instead of compiling them to no predicate and
// deleting or rewriting the whole table with a 200 that reads as success.
func refuseEmptyFilter(filter *datastore.Filter) error {
	if filter == nil || len(filter.Conditions) == 0 {
		return huma.Error422UnprocessableEntity("a row filter with at least one condition is required; an empty filter matches nothing by refusal, never everything")
	}
	return nil
}

// queryFilter compiles the repeated flat triples into the service envelope.
// The three lists zip by position; ragged lists are a 422 rather than a
// silent truncation, and the match mode defaults to n8n's Any Condition.
func queryFilter(input *listRowsInput) (*datastore.Filter, error) {
	if len(input.Columns) != len(input.Conditions) || len(input.Columns) != len(input.Values) {
		return nil, errors.New("filter triples must line up: columnName, condition and value repeat together")
	}
	if len(input.Columns) == 0 {
		return nil, nil
	}
	match := input.Match
	if match == "" {
		match = "any"
	}
	filterType, err := datastore.NodeMatchToFilterType(match)
	if err != nil {
		return nil, err
	}
	filter := &datastore.Filter{Type: filterType}
	for index := range input.Columns {
		filter.Conditions = append(filter.Conditions, datastore.FilterCondition{
			Column:    input.Columns[index],
			Condition: datastore.Condition(input.Conditions[index]),
			Value:     queryValue(input.Values[index]),
		})
	}
	return filter, nil
}

// queryValue reads one filter value back into its JSON type. Query strings
// arrive untyped, so a value that parses as JSON takes the parsed type —
// 3 becomes a number, true a boolean — and anything else stays the string
// the caller wrote, which is what a LIKE pattern always is.
func queryValue(raw string) any {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return raw
	}
	return decoded
}

func (handler *Datastores) problem(err error) error {
	if datastore.IsUnknown(err) {
		return huma.Error404NotFound("datastore not found")
	}
	if errors.Is(err, datastore.ErrRowNotFound) {
		return huma.Error404NotFound("row not found")
	}
	if errors.Is(err, datastore.ErrInvalidRowCursor) {
		return huma.Error400BadRequest("row cursor is invalid")
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// ownsDatastore confines an embed session to its own datastore, in the shape
// of ownsExecution: which datastore a row belongs to is addressed in the
// path, but whether this session may name it is a handler question, because
// the middleware answers in 403 and a session bound to one datastore must
// read any other as unknown rather than as forbidden.
//
// A request with no embed session is the internal dashboard and is unaffected.
func (handler *Datastores) ownsDatastore(ctx context.Context, datastoreID string) error {
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		return nil
	}
	if session.DatastoreID != "" && session.DatastoreID == datastoreID {
		return nil
	}
	// The same document an unknown id produces: an embed session has no
	// business learning that another datastore exists.
	return huma.Error404NotFound("datastore not found")
}

func datastoreResource(definition datastore.Datastore) DatastoreResource {
	columns := make([]DatastoreColumnResource, 0, len(definition.Columns))
	for _, column := range definition.Columns {
		columns = append(columns, DatastoreColumnResource{Name: column.Name, Type: string(column.Type)})
	}
	return DatastoreResource{ID: definition.ID, Name: definition.Name, Columns: columns}
}

type datastoreColumnPathInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Name string `path:"name" minLength:"1" doc:"Column name"`
}
