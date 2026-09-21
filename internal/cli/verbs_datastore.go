package cli

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// datastoreVerbs are the datastore verbs: the read surface with the CSV export,
// and the guarded schema verbs.
//
// `datastore export` is one of the two documented exceptions to "always one
// envelope": its body is RFC 4180 CSV, and a CSV wrapped in JSON is a CSV the
// caller has to parse twice. It is also the reason the export takes `--out` —
// with a path the verb writes the bytes and reports where they went, and
// without one it streams them, which is what `> rows.csv` wants.
//
// create, rename, delete, clear and the three column operations are guarded:
// they are the schema half of the surface, and a data table is what every
// workflow in the tenant reads and writes. `datastore create` takes the name as
// its argument rather than a document, because the API's own description of the
// operation is that columns are added afterwards — through `datastore columns
// add`, which is where a column's type is named.
//
// Deliberately absent: every row write (insert, update, upsert, delete,
// increment, and the CSV import). They are row data rather than the tenant's
// schema, so they are phase 2's unguarded writes and have no verb yet;
// `kilasflow api insert-datastore-row` and friends reach them today.
func datastoreVerbs() []Verb {
	return []Verb{
		{
			Path:      "datastore list",
			Operation: "list-datastores",
			Summary:   "list datastores (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runDatastoreList,
			Human:     humanResourceList("id", "name"),
		},
		{
			Path:      "datastore get",
			Operation: "get-datastore",
			Summary:   "read one datastore and its columns",
			Args:      []Arg{arg("datastore id")},
			Run:       runDatastoreGet,
			Human:     humanDatastoreResource,
		},
		{
			Path:      "datastore rows",
			Operation: "list-datastore-rows",
			Summary:   "read one page of a datastore's rows (`--limit`, `--cursor`)",
			Args:      []Arg{arg("datastore id")},
			Flags:     registerPageFlags,
			Run:       runDatastoreRows,
			Human:     humanResourceList("id", "createdAt", "updatedAt"),
		},
		{
			Path:      "datastore export",
			Operation: "export-datastore-rows",
			Summary:   "export a datastore's rows as CSV (`--out <path>` writes a file; the default streams to stdout)",
			Args:      []Arg{arg("datastore id")},
			Flags:     registerDatastoreExportFlags,
			Run:       runDatastoreExport,
			Human:     humanDatastoreExport,
		},
		{
			Path:      "datastore create",
			Operation: "create-datastore",
			Summary:   "create a data table; columns are added afterwards (guarded)",
			Args:      []Arg{arg("datastore name")},
			Guarded:   true,
			Refusal:   "creates a new data table",
			Run:       runDatastoreCreate,
			Human:     humanDatastoreResource,
		},
		{
			Path:      "datastore rename",
			Operation: "rename-datastore",
			Summary:   "rename a data table (`<datastoreId> <newName>`, guarded)",
			Args:      []Arg{arg("datastore id"), arg("new name")},
			Guarded:   true,
			Refusal:   "renames a data table",
			Run:       runDatastoreRename,
			Human:     humanDatastoreResource,
		},
		{
			Path:      "datastore delete",
			Operation: "delete-datastore",
			Summary:   "delete a data table and every row it holds (guarded)",
			Args:      []Arg{arg("datastore id")},
			Guarded:   true,
			Refusal:   "destroys a data table and every row it holds",
			Run:       runDatastoreDelete,
			Human:     humanDeletion,
		},
		{
			Path:      "datastore clear",
			Operation: "clear-datastore",
			Summary:   "delete every row of a data table, keeping its schema (guarded)",
			Args:      []Arg{arg("datastore id")},
			Guarded:   true,
			Refusal:   "deletes every row of a data table",
			Run:       runDatastoreClear,
			Human:     humanDatastoreClear,
		},
		{
			Path:      "datastore columns add",
			Operation: "add-datastore-column",
			Summary:   "append one column to a data table (`<datastoreId> <name> --type <type>`, guarded)",
			Args:      []Arg{arg("datastore id"), arg("column name")},
			Guarded:   true,
			Refusal:   "changes a data table's schema",
			Flags:     registerDatastoreColumnFlags,
			Run:       runDatastoreColumnAdd,
			Human:     humanDatastoreColumn,
		},
		{
			Path:      "datastore columns rename",
			Operation: "rename-datastore-column",
			Summary:   "rename one column of a data table (`<datastoreId> <name> <newName>`, guarded)",
			Args:      []Arg{arg("datastore id"), arg("column name"), arg("new column name")},
			Guarded:   true,
			Refusal:   "changes a data table's schema",
			Run:       runDatastoreColumnRename,
			Human:     humanDatastoreColumn,
		},
		{
			Path:      "datastore columns drop",
			Operation: "delete-datastore-column",
			Summary:   "drop one column of a data table and the values in it (guarded)",
			Args:      []Arg{arg("datastore id"), arg("column name")},
			Guarded:   true,
			Refusal:   "changes a data table's schema, dropping a column and the values in it",
			Run:       runDatastoreColumnDrop,
			Human:     humanDeletion,
		},
	}
}

// datastoreExportFlags say where the CSV goes.
type datastoreExportFlags struct {
	out string
}

// registerDatastoreExportFlags attaches --out. The default is stdout, because
// that is what an export is for.
func registerDatastoreExportFlags(fs *flag.FlagSet) any {
	flags := &datastoreExportFlags{}
	fs.StringVar(&flags.out, "out", "-", "write the CSV to a file; - (the default) streams it to stdout")

	return flags
}

// runDatastoreList reads one page of datastores.
func runDatastoreList(ctx *Context, args []string) error {
	if err := refusePositional(args, "datastore list"); err != nil {
		return err
	}

	list, err := ctx.listPage("/datastores", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runDatastoreGet reads one datastore.
func runDatastoreGet(ctx *Context, args []string) error {
	id, err := requireOneID(args, "datastore id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodGet, "/datastores/"+url.PathEscape(id), nil, id)
}

// runDatastoreRows reads one page of a datastore's rows.
func runDatastoreRows(ctx *Context, args []string) error {
	id, err := requireOneID(args, "datastore id")
	if err != nil {
		return err
	}

	list, err := ctx.listPage("/datastores/"+url.PathEscape(id)+"/rows", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runDatastoreExport streams the CSV, or writes it where --out asked.
//
// The path is the tree's `/datastores/{id}/rows/export`, not the shorter
// `/datastores/{id}/export` the ticket cited: the shorter one is answered by the
// SPA catch-all with 200 HTML, which no exit code can distinguish from an
// export.
func runDatastoreExport(ctx *Context, args []string) error {
	id, err := requireOneID(args, "datastore id")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*datastoreExportFlags)
	if !ok {
		return usageError("the datastore export verb was registered without its flags")
	}

	path := "/datastores/" + url.PathEscape(id) + "/rows/export"

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	if flags.out == "-" {
		if _, err := stdout(ctx.Env).Write(resp.Body); err != nil {
			return outputWriteError("could not write the export to stdout: %v", err)
		}
		// The bytes are the output; Run writes no envelope over them.
		ctx.Streamed = true

		return nil
	}

	// 0600: an export is the tenant's own data, and a file the operator has to
	// remember to protect is a file that leaks.
	if err := os.WriteFile(flags.out, resp.Body, 0o600); err != nil {
		return outputWriteError("could not write %s: %v", flags.out, err)
	}

	ctx.Data = apiWritten{Path: flags.out, Bytes: len(resp.Body), ContentType: resp.ContentType}
	ctx.Primary = flags.out

	return nil
}

// humanDatastoreResource prints a datastore as the lines a person needs before
// reading its rows: what it is called and what columns it has.
func humanDatastoreResource(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var resource struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Columns []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(raw, &resource); err != nil {
		printJSONValue(w, data)

		return
	}

	columns := make([]string, 0, len(resource.Columns))
	for _, column := range resource.Columns {
		columns = append(columns, column.Name+" ("+column.Type+")")
	}

	printKV(w, [][2]string{
		{"id", resource.ID},
		{"name", resource.Name},
		{"columns", strings.Join(columns, ", ")},
	})
}

// datastoreColumnFlags carry the type of the column being added.
type datastoreColumnFlags struct {
	columnType string
}

// registerDatastoreColumnFlags attaches --type. It has no default: the API's
// column types are a closed set the server validates, and a CLI that guessed one
// would be choosing the type of a column the caller is about to fill.
func registerDatastoreColumnFlags(fs *flag.FlagSet) any {
	flags := &datastoreColumnFlags{}
	fs.StringVar(&flags.columnType, "type", "", "column type: string, number, boolean or date")

	return flags
}

// datastoreNameBody is the one-field body `datastore create` and
// `datastore rename` send.
type datastoreNameBody struct {
	Name string `json:"name"`
}

// datastoreColumnBody is one column as the column operations send it and as the
// API answers `add` and `rename` with.
type datastoreColumnBody struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// runDatastoreCreate creates an empty data table.
//
// The name is an argument rather than a document: it is the whole body, and
// columns are added afterwards with `datastore columns add`.
func runDatastoreCreate(ctx *Context, args []string) error {
	name, err := requireOneID(args, "datastore name")
	if err != nil {
		return err
	}

	body, err := json.Marshal(datastoreNameBody{Name: name})
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath("/datastores"), nil, nil, body)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	ctx.Primary = locationID(resp.Header.Get("Location"))
	if ctx.Primary == "" {
		ctx.Primary = fieldValue(resp.Body, "id")
	}

	return nil
}

// runDatastoreRename renames a data table, which is the one field of it that
// changes: rows and columns are addressed through the id.
func runDatastoreRename(ctx *Context, args []string) error {
	id, name, err := requireTwoIDs(args, "datastore id", "new name")
	if err != nil {
		return err
	}

	body, err := json.Marshal(datastoreNameBody{Name: name})
	if err != nil {
		return err
	}

	return ctx.writeResource(http.MethodPut, "/datastores/"+url.PathEscape(id), body, id)
}

// runDatastoreDelete removes a data table and every row it holds.
func runDatastoreDelete(ctx *Context, args []string) error {
	id, err := requireOneID(args, "datastore id")
	if err != nil {
		return err
	}

	return ctx.deleteResource("/datastores/"+url.PathEscape(id), id)
}

// runDatastoreClear removes every row and keeps the schema.
//
// The API answers with how many rows went, which is what the verb reports: a
// clear that removed nothing and one that removed a million are the same
// success, and the count is the only thing that tells them apart.
func runDatastoreClear(ctx *Context, args []string) error {
	id, err := requireOneID(args, "datastore id")
	if err != nil {
		return err
	}

	path := "/datastores/" + url.PathEscape(id) + "/clear"

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	ctx.Primary = id

	return nil
}

// runDatastoreColumnAdd appends one column to a data table.
func runDatastoreColumnAdd(ctx *Context, args []string) error {
	id, name, err := requireTwoIDs(args, "datastore id", "column name")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*datastoreColumnFlags)
	if !ok {
		return usageError("the datastore columns add verb was registered without its flags")
	}
	if strings.TrimSpace(flags.columnType) == "" {
		return usageError("no column type: pass --type string|number|boolean|date")
	}

	body, err := json.Marshal(datastoreColumnBody{Name: name, Type: flags.columnType})
	if err != nil {
		return err
	}

	return ctx.writeResource(http.MethodPost, "/datastores/"+url.PathEscape(id)+"/columns", body, name)
}

// runDatastoreColumnRename renames one column, keeping the values in it.
func runDatastoreColumnRename(ctx *Context, args []string) error {
	id, name, newName, err := requireThreeIDs(args, "datastore id", "column name", "new column name")
	if err != nil {
		return err
	}

	body, err := json.Marshal(datastoreColumnBody{Name: newName})
	if err != nil {
		return err
	}

	path := "/datastores/" + url.PathEscape(id) + "/columns/" + url.PathEscape(name)

	return ctx.writeResource(http.MethodPut, path, body, newName)
}

// runDatastoreColumnDrop drops one column and the values in it.
func runDatastoreColumnDrop(ctx *Context, args []string) error {
	id, name, err := requireTwoIDs(args, "datastore id", "column name")
	if err != nil {
		return err
	}

	path := "/datastores/" + url.PathEscape(id) + "/columns/" + url.PathEscape(name)

	return ctx.deleteResource(path, name)
}

// humanDatastoreExport reports where the file went and how big it is.
func humanDatastoreExport(w io.Writer, data any) {
	written, ok := data.(apiWritten)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"path", written.Path},
		{"bytes", strconv.Itoa(written.Bytes)},
	})
}

// humanDatastoreClear reports how much a clear removed.
func humanDatastoreClear(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var cleared struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(raw, &cleared); err != nil {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{{"rowsDeleted", strconv.FormatInt(cleared.Deleted, 10)}})
}

// humanDatastoreColumn prints a column as the pair a person needs to see before
// the next schema change.
func humanDatastoreColumn(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var column datastoreColumnBody
	if err := json.Unmarshal(raw, &column); err != nil {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{{"column", column.Name}, {"type", column.Type}})
}
