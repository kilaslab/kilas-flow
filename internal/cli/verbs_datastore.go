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

// datastoreVerbs are the read-only datastore verbs, including the CSV export.
//
// `datastore export` is one of the two documented exceptions to "always one
// envelope": its body is RFC 4180 CSV, and a CSV wrapped in JSON is a CSV the
// caller has to parse twice. It is also the reason the export takes `--out` —
// with a path the verb writes the bytes and reports where they went, and
// without one it streams them, which is what `> rows.csv` wants.
//
// Deliberately absent: every row write (insert, update, upsert, delete), create,
// rename, delete, columns and clear. The writes are phase 2's; the rest have no
// verb yet. `kilasflow api insert-datastore-row` and friends reach them today.
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
			Run:       runDatastoreGet,
			Human:     humanDatastoreResource,
		},
		{
			Path:      "datastore rows",
			Operation: "list-datastore-rows",
			Summary:   "read one page of a datastore's rows (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runDatastoreRows,
			Human:     humanResourceList("id", "createdAt", "updatedAt"),
		},
		{
			Path:      "datastore export",
			Operation: "export-datastore-rows",
			Summary:   "export a datastore's rows as CSV (`--out <path>` writes a file; the default streams to stdout)",
			Flags:     registerDatastoreExportFlags,
			Run:       runDatastoreExport,
			Human:     humanDatastoreExport,
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
