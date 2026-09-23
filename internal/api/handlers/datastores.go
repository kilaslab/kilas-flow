package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
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
	// idempotency makes a retried row write replay instead of writing twice.
	// Optional: a nil service refuses a request that carries an
	// Idempotency-Key rather than writing unprotected.
	idempotency *idempotency.Service
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

// listDatastoresInput is one page request for the catalogue. The bounds match
// the engine's own clamp, so an out-of-range value is refused at the edge with
// a schema error rather than clamped behind the caller's back.
type listDatastoresInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"500" doc:"Maximum datastores to return (default 100)"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's X-Next-Cursor header"`
}

type datastoreListOutput struct {
	// NextCursor is empty on the last page. It rides in a header so the body
	// keeps the object shape the dashboard already reads.
	NextCursor string `header:"X-Next-Cursor" doc:"Cursor for the next page; empty when there is none"`
	Body       struct {
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
	ID string `path:"id" minLength:"1" doc:"Datastore identifier"`
	// IdempotencyKey is the caller's retry key. Absent and empty are the same
	// thing to the framework, and both mean no idempotency.
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"255" pattern:"^[!-~]+$" doc:"1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency."`
	Body           struct {
		Values map[string]any `json:"values" doc:"User column values keyed by column name"`
	}
}

type createdRowOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	// Replayed is empty on a first response, and huma omits an empty header.
	Replayed string `header:"Idempotent-Replayed" doc:"true when this response replays an earlier request with the same Idempotency-Key"`
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
		// IfUpdatedAt makes the write conditional. It is the stamp a
		// previous read returned, so it is optional: without it the write
		// is unconditional and the last writer wins.
		IfUpdatedAt *time.Time `json:"ifUpdatedAt,omitempty" doc:"A row's updatedAt exactly as a previous read returned it; when present the filter must match exactly one row and the write lands only if that row is unchanged"`
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
		Filter      datastore.Filter `json:"filter" doc:"Rows to delete; an empty filter is refused and removes nothing"`
		IfUpdatedAt *time.Time       `json:"ifUpdatedAt,omitempty" doc:"A row's updatedAt exactly as a previous read returned it; when present the filter must match exactly one row and the delete lands only if that row is unchanged"`
	}
}

type deleteRowsOutput struct {
	Body struct {
		Deleted int64           `json:"deleted"`
		Rows    []datastore.Row `json:"rows"`
	}
}

type upsertRowInput struct {
	ID string `path:"id" minLength:"1" doc:"Datastore identifier"`
	// IdempotencyKey is the caller's retry key. Absent and empty are the same
	// thing to the framework, and both mean no idempotency.
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"255" pattern:"^[!-~]+$" doc:"1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency."`
	Body           struct {
		Filter datastore.Filter `json:"filter" doc:"Rows to match; on no match one row is inserted"`
		Values map[string]any   `json:"values" doc:"Columns to set or to insert with"`
	}
}

type upsertRowOutput struct {
	// Replayed is empty on a first response, and huma omits an empty header.
	Replayed string `header:"Idempotent-Replayed" doc:"true when this response replays an earlier request with the same Idempotency-Key"`
	Body     struct {
		Inserted bool            `json:"inserted"`
		Matched  int64           `json:"matched"`
		Rows     []datastore.Row `json:"rows"`
	}
}

// incrementRowsInput is the atomic counter write. Amount is a pointer so an
// explicit zero stays a zero: huma's `default` tag would rewrite any zero
// value to the default, silently turning "add nothing" into "add one".
type incrementRowsInput struct {
	ID   string `path:"id" minLength:"1" doc:"Datastore identifier"`
	Body struct {
		Filter datastore.Filter `json:"filter" doc:"Rows to increment; an empty filter is refused"`
		Column string           `json:"column" minLength:"1" doc:"The number column to add to"`
		Amount *float64         `json:"amount,omitempty" doc:"Added to the column in one statement; negative subtracts; absent means 1"`
	}
}

type incrementRowsOutput struct {
	Body struct {
		Matched int64           `json:"matched"`
		Rows    []datastore.Row `json:"rows"`
	}
}

// Register wires the three datastore path families plus the CSV transfer
// pair, which lives in datastores_csv.go and is mounted here so every
// datastore operation registers in one place.
func (handler *Datastores) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-datastores", Method: http.MethodGet, Path: "/datastores",
		Summary: "List datastores", Description: "Returns one page of data tables, in name order. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.", Tags: []string{"Datastores"},
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
		Summary: "Insert a row", Description: "Writes one row and reads it back. Send Idempotency-Key to make a retry safe: " + idempotencyKeyDoc, Tags: []string{"Datastore rows"},
	}, handler.InsertRow)
	huma.Register(api, huma.Operation{
		OperationID: "get-datastore-row", Method: http.MethodGet, Path: "/datastores/{id}/rows/{rowId}",
		Summary: "Get a row", Description: "Returns one row by id.", Tags: []string{"Datastore rows"},
	}, handler.GetRow)
	huma.Register(api, huma.Operation{
		OperationID: "update-datastore-rows", Method: http.MethodPut, Path: "/datastores/{id}/rows",
		Summary: "Update rows", Description: "Sets columns on every row matching the filter. One statement, atomic per row on both drivers: concurrent writers never interleave inside a row and the last writer wins; no row lock is taken. Pass ifUpdatedAt — a row's updatedAt exactly as a previous read returned it — to make the write conditional: the filter must then match exactly one row and the write lands only if the row is unchanged. A stale stamp answers 409 with the row's current updatedAt in errors[0].value, so the caller retries against the new stamp without a second read.", Tags: []string{"Datastore rows"},
	}, handler.UpdateRows)
	huma.Register(api, huma.Operation{
		OperationID: "delete-datastore-rows", Method: http.MethodDelete, Path: "/datastores/{id}/rows",
		Summary: "Delete rows", Description: "Removes every row matching the filter. An empty filter is refused and removes nothing. One statement, atomic per row on both drivers: the last writer wins and no row lock is taken. Pass ifUpdatedAt — a row's updatedAt exactly as a previous read returned it — to make the delete conditional: the filter must then match exactly one row and the delete lands only if the row is unchanged. A stale stamp answers 409 with the row's current updatedAt in errors[0].value, so the caller retries against the new stamp without a second read.", Tags: []string{"Datastore rows"},
	}, handler.DeleteRows)
	huma.Register(api, huma.Operation{
		OperationID: "upsert-datastore-row", Method: http.MethodPost, Path: "/datastores/{id}/rows/upsert",
		Summary: "Upsert rows", Description: "Updates every row matching the filter, or inserts one row when nothing matches. When the filter is exactly one condition, id equals a value between 1 and 9007199254740991, this is a single INSERT ... ON CONFLICT statement on both drivers: it never inserts the same id twice and a missing id is created at exactly that id. Matched on any other column it is read-then-write in no single transaction, so two concurrent upserts against the same filter may both insert. A counter or flag that must not lose writes uses increment. Send Idempotency-Key to make a retry safe: " + idempotencyKeyDoc, Tags: []string{"Datastore rows"},
	}, handler.UpsertRow)
	huma.Register(api, huma.Operation{
		OperationID: "increment-datastore-rows", Method: http.MethodPost, Path: "/datastores/{id}/rows/increment",
		Summary: "Increment rows", Description: "Adds amount (default 1, may be negative) to a number column on every matching row in one statement, atomic per row on both drivers, and returns each row as that statement left it. A NULL cell counts as zero. Concurrent increments never lose a write.", Tags: []string{"Datastore rows"},
	}, handler.IncrementRows)
	handler.registerDatastoreTransfer(api)
}

// List returns one page of the tenant's datastores, in name order.
func (handler *Datastores) List(ctx context.Context, input *listDatastoresInput) (*datastoreListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	page, err := handler.store.ListDatastoresPage(ctx, handler.tenants.Resolve(ctx).ID, datastore.DatastoreQuery{
		Limit: input.Limit, Cursor: input.Cursor,
	})
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	items := make([]DatastoreResource, 0, len(page.Datastores))
	for _, definition := range page.Datastores {
		items = append(items, datastoreResource(definition))
	}
	out := &datastoreListOutput{NextCursor: page.NextCursor}
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
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
	}
	definition, err := handler.store.GetDatastore(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
	}
	if err := handler.store.Drop(ctx, tenant, input.ID); err != nil {
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
	}
	return &datastoreColumnOutput{Body: DatastoreColumnResource{Name: input.Body.Name, Type: input.Body.Type}}, nil
}

// RenameColumn renames one user column.
func (handler *Datastores) RenameColumn(ctx context.Context, input *renameDatastoreColumnInput) (*datastoreColumnOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.store.RenameColumn(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.Name, input.Body.Name); err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &datastoreColumnOutput{Body: DatastoreColumnResource{Name: input.Body.Name}}, nil
}

// DropColumn removes one user column.
func (handler *Datastores) DropColumn(ctx context.Context, input *datastoreColumnPathInput) (*deletedDatastoreOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.store.DropColumn(ctx, handler.tenants.Resolve(ctx).ID, input.ID, input.Name); err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &deletedDatastoreOutput{Status: http.StatusNoContent}, nil
}

// InsertRow writes one row and reads it back.
//
// A caller that sends Idempotency-Key gets the first insert's row and location
// back instead of a second row. The recorded form is the row, and above the
// recorded-outcome cap it is just the id, which is all a replay needs to answer
// with the same location.
func (handler *Datastores) InsertRow(ctx context.Context, input *insertRowInput) (*createdRowOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	values := input.Body.Values
	if values == nil {
		// An absent values map and an empty one are the same request, and must
		// hash the same or a retry would be refused as a conflict.
		values = map[string]any{}
	}
	tenantID := handler.tenants.Resolve(ctx).ID
	insert := func(ctx context.Context) (idempotency.Response, error) {
		row, err := handler.store.Insert(ctx, tenantID, input.ID, values)
		if err != nil {
			return idempotency.Response{}, err
		}
		return idempotency.Response{
			Status:  http.StatusCreated,
			Body:    row,
			Compact: datastore.Row{"id": row["id"]},
		}, nil
	}

	if input.IdempotencyKey == "" {
		response, err := insert(ctx)
		if err != nil {
			return nil, handler.problem(ctx, err)
		}
		row := response.Body.(datastore.Row)
		return &createdRowOutput{Status: response.Status, Location: rowLocation(input.ID, row), Body: row}, nil
	}
	if handler.idempotency == nil {
		return nil, idempotencyUnavailable()
	}
	result, err := handler.idempotency.Do(ctx, tenantID, input.IdempotencyKey, idempotency.Request{
		Operation: "insert-datastore-row",
		Target:    input.ID,
		Body: struct {
			Values map[string]any `json:"values"`
		}{Values: values},
	}, insert)
	if err != nil {
		if problem, ok := idempotencyProblem(ctx, err); ok {
			return nil, problem
		}
		return nil, handler.problem(ctx, err)
	}
	var row datastore.Row
	if err := result.Outcome.Decode(&row); err != nil {
		return nil, serverProblem(ctx, "the recorded row outcome could not be read", err)
	}
	return &createdRowOutput{
		Status:   result.Outcome.Status,
		Replayed: replayedHeader(result.Replayed),
		Location: rowLocation(input.ID, row),
		Body:     row,
	}, nil
}

// rowLocation is the row's own address, rebuilt from the decoded row because a
// replay reads its outcome back as JSON, where an id is a float64.
func rowLocation(datastoreID string, row datastore.Row) string {
	return "/api/v1/datastores/" + datastoreID + "/rows/" + strconv.FormatInt(rowIDOf(row), 10)
}

// rowIDOf reads a row's id in any of the shapes it arrives in: an int64 from
// the engine, a float64 from JSON decoding, or a json.Number when a decoder
// was configured to keep the text.
func rowIDOf(row datastore.Row) int64 {
	switch id := row["id"].(type) {
	case int64:
		return id
	case float64:
		return int64(id)
	case json.Number:
		value, err := id.Int64()
		if err == nil {
			return value
		}
	}
	return 0
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
		return nil, handler.problem(ctx, err)
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
		return nil, handler.problem(ctx, err)
	}
	return &rowOutput{Body: row}, nil
}

// UpdateRows sets columns on every row matching the filter. An ifUpdatedAt
// stamp turns the write into compare-and-swap on that row's updatedAt: the
// stamp came from a read, so a row that moved under the caller is refused
// with a 409 rather than overwritten.
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
	tenant := handler.tenants.Resolve(ctx).ID
	var result *datastore.UpdateResult
	var err error
	if input.Body.IfUpdatedAt != nil {
		result, err = handler.store.UpdateWithPrecondition(ctx, tenant, input.ID, &input.Body.Filter, input.Body.Values, *input.Body.IfUpdatedAt)
	} else {
		result, err = handler.store.Update(ctx, tenant, input.ID, &input.Body.Filter, input.Body.Values, false)
	}
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	out := &updateRowsOutput{}
	out.Body.Matched = result.Matched
	out.Body.Rows = result.Rows
	if out.Body.Rows == nil {
		out.Body.Rows = []datastore.Row{}
	}
	return out, nil
}

// DeleteRows removes every row matching the filter. An ifUpdatedAt stamp
// makes it conditional in exactly the shape UpdateRows documents.
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
	tenant := handler.tenants.Resolve(ctx).ID
	var result *datastore.DeleteResult
	var err error
	if input.Body.IfUpdatedAt != nil {
		result, err = handler.store.DeleteWithPrecondition(ctx, tenant, input.ID, &input.Body.Filter, *input.Body.IfUpdatedAt)
	} else {
		result, err = handler.store.Delete(ctx, tenant, input.ID, &input.Body.Filter, false)
	}
	if err != nil {
		return nil, handler.problem(ctx, err)
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
//
// A caller that sends Idempotency-Key gets the first call's outcome back
// instead of a second upsert. That is observable: a keyless repeat of an
// inserting upsert reports the update branch, while the replay still reports
// the insert it recorded.
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
	values := input.Body.Values
	if values == nil {
		values = map[string]any{}
	}
	tenantID := handler.tenants.Resolve(ctx).ID

	out := &upsertRowOutput{}
	upsert := func(ctx context.Context) (idempotency.Response, error) {
		result, err := handler.store.Upsert(ctx, tenantID, input.ID, &input.Body.Filter, values, false)
		if err != nil {
			return idempotency.Response{}, err
		}
		matched := int64(len(result.Rows))
		if result.Inserted {
			matched = 0
		}
		out.Body.Inserted = result.Inserted
		out.Body.Matched = matched
		out.Body.Rows = result.Rows
		if out.Body.Rows == nil {
			out.Body.Rows = []datastore.Row{}
		}
		// Above the recorded-outcome cap the counts and an empty row list are
		// still a faithful replay: what the caller can act on is that the
		// upsert inserted rather than updated.
		compact := map[string]any{"inserted": result.Inserted, "matched": matched, "rows": []datastore.Row{}}
		return idempotency.Response{Status: http.StatusOK, Body: out.Body, Compact: compact}, nil
	}

	if input.IdempotencyKey == "" {
		if _, err := upsert(ctx); err != nil {
			return nil, handler.problem(ctx, err)
		}
		return out, nil
	}
	if handler.idempotency == nil {
		return nil, idempotencyUnavailable()
	}
	result, err := handler.idempotency.Do(ctx, tenantID, input.IdempotencyKey, idempotency.Request{
		Operation: "upsert-datastore-row",
		Target:    input.ID,
		Body: struct {
			Filter datastore.Filter `json:"filter"`
			Values map[string]any   `json:"values"`
		}{Filter: input.Body.Filter, Values: values},
	}, upsert)
	if err != nil {
		if problem, ok := idempotencyProblem(ctx, err); ok {
			return nil, problem
		}
		return nil, handler.problem(ctx, err)
	}
	if err := json.Unmarshal(result.Outcome.Body, &out.Body); err != nil {
		return nil, serverProblem(ctx, "the recorded upsert outcome could not be read", err)
	}
	out.Replayed = replayedHeader(result.Replayed)
	return out, nil
}

// IncrementRows adds to a number column on every row matching the filter, in
// one statement. It is the write a counter or a flag uses: the value it
// returns is the one its own statement produced, so a concurrent writer's
// later value never comes back instead and no increment is lost.
func (handler *Datastores) IncrementRows(ctx context.Context, input *incrementRowsInput) (*incrementRowsOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	if err := refuseEmptyFilter(&input.Body.Filter); err != nil {
		return nil, err
	}
	amount := 1.0
	if input.Body.Amount != nil {
		amount = *input.Body.Amount
	}
	result, err := handler.store.Increment(ctx, handler.tenants.Resolve(ctx).ID, input.ID,
		&input.Body.Filter, input.Body.Column, amount)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	out := &incrementRowsOutput{}
	out.Body.Matched = result.Matched
	out.Body.Rows = result.Rows
	if out.Body.Rows == nil {
		out.Body.Rows = []datastore.Row{}
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

func (handler *Datastores) problem(ctx context.Context, err error) error {
	// A taken name conflicts with the table that holds it, and the detail names
	// that table: the create and rename dialogs show it as written, and for a
	// clash of case alone the holder's name is not the one that was typed. It
	// is matched before IsUnknown, which reads the error's text: this error's
	// text carries the name the tenant chose, and a name that says "unknown
	// datastore" is still a name another table holds, not a missing table.
	var taken *datastore.NameTakenError
	if errors.As(err, &taken) {
		return huma.Error409Conflict(fmt.Sprintf("A data table named “%s” already exists", taken.Name))
	}
	if datastore.IsUnknown(err) {
		return huma.Error404NotFound("datastore not found")
	}
	// A refused precondition is a conflict, not a caller mistake: the row
	// moved after the caller read it. The current stamp rides in the detail
	// body's errors[0].value so the caller retries without a second read —
	// this branch sits ahead of the general mappings below so none of them
	// can claim the error.
	var conflict *datastore.PreconditionError
	if errors.As(err, &conflict) {
		now := conflict.Current.UTC().Format(time.RFC3339Nano)
		return &huma.ErrorModel{
			Status: http.StatusConflict,
			Title:  "Conflict",
			Detail: fmt.Sprintf("this row changed since it was read (updatedAt is now %s): re-read or retry against the new stamp", now),
			Errors: []*huma.ErrorDetail{{
				Message:  "updatedAt no longer matches ifUpdatedAt",
				Location: "body.ifUpdatedAt",
				Value:    now,
			}},
		}
	}
	if errors.Is(err, datastore.ErrRowNotFound) {
		return huma.Error404NotFound("row not found")
	}
	if errors.Is(err, datastore.ErrInvalidRowCursor) {
		return huma.Error400BadRequest("row cursor is invalid")
	}
	if errors.Is(err, datastore.ErrInvalidDatastoreCursor) {
		return huma.Error400BadRequest("datastore cursor is invalid")
	}
	// What the engine refuses about the request — a column it cannot resolve, a
	// name it will not take, a filter it cannot build — is the caller's to fix
	// and its message names what to change. A failure underneath the engine is
	// not: answering it 422 with the driver's own text presented a server fault
	// as a caller mistake and disclosed the table and column names it named.
	if internalFailure(err) {
		return serverProblem(ctx, "datastore operation failed", err)
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

// WithIdempotency attaches the request idempotency service. Without it a row
// write that carries an Idempotency-Key is refused rather than written
// unprotected.
func (handler *Datastores) WithIdempotency(service *idempotency.Service) *Datastores {
	handler.idempotency = service
	return handler
}
