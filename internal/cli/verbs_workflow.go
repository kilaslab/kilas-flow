package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// workflowVerbs are the workflow verbs this phase owns: the read surface,
// `create`, and the three guarded lifecycle verbs.
//
// The paths are the stable ones documented in the API contract, not a lookup in
// the served document: `workflow get` is one request, and an agent that has to
// read `/api/openapi.json` first to learn a path would be paying for the escape
// hatch's flexibility on every call. `kilasflow api <operation-id>` remains the
// verb that resolves against the running server.
//
// activate, deactivate and delete are the design's guarded verbs: each refuses
// without --yes, and each refuses a scoped agent token outright (see
// requireAuthority), because publishing an endpoint and destroying a workflow
// are the tenant's own decisions rather than a scoped key's.
//
// Deliberately absent: update, restore, import, duplicate and validate. They
// have no verb yet, and `kilasflow api <operation-id>` reaches them today.
func workflowVerbs() []Verb {
	return []Verb{
		{
			Path:      "workflow list",
			Operation: "list-workflows",
			Summary:   "list workflows, newest first (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runWorkflowList,
			Human:     humanResourceList("id", "name", "active", "updatedAt"),
		},
		{
			Path:      "workflow get",
			Operation: "get-workflow",
			Summary:   "read one workflow by id",
			Run:       runWorkflowGet,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow create",
			Operation: "create-workflow",
			Summary:   "create a workflow from a canonical document (`--file <path>|-`)",
			Flags:     registerCreateFlags,
			Run:       runWorkflowCreate,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow versions",
			Operation: "list-workflow-versions",
			Summary:   "list a workflow's revisions (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runWorkflowVersions,
			Human:     humanResourceList("id", "revision", "draft", "published", "createdAt"),
		},
		{
			Path:      "workflow get-version",
			Operation: "get-workflow-version",
			Summary:   "read one revision of a workflow, with its document",
			Run:       runWorkflowGetVersion,
			Human:     humanResourceResource,
		},
		{
			Path:      "workflow publish-events",
			Operation: "list-workflow-publish-events",
			Summary:   "read a workflow's publish audit trail",
			Run:       runWorkflowPublishEvents,
			Human:     humanResourceList("createdAt", "action", "versionId", "actor"),
		},
		{
			Path:      "workflow export",
			Operation: "export-workflow",
			Summary:   "export a workflow as n8n JSON, with what could not be carried",
			Flags:     registerExportFlags,
			Run:       runWorkflowExport,
			Human:     humanResourceResource,
		},
		{
			Path:      "workflow diagnostics",
			Operation: "workflow-diagnostics",
			Summary:   "read the import report stored with a revision",
			Flags:     registerDiagnosticsFlags,
			Run:       runWorkflowDiagnostics,
			Human:     humanResourceResource,
		},
		{
			Path:      "workflow activate",
			Operation: "activate-workflow",
			Summary:   "activate a workflow's latest revision, publishing its endpoint (guarded)",
			Guarded:   true,
			Refusal:   "publishes a public endpoint",
			Run:       runWorkflowActivate,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow deactivate",
			Operation: "deactivate-workflow",
			Summary:   "take a workflow's published endpoint offline (guarded)",
			Guarded:   true,
			Refusal:   "takes a public endpoint offline",
			Run:       runWorkflowDeactivate,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow delete",
			Operation: "delete-workflow",
			Summary:   "delete a workflow, retaining its audit trail (guarded)",
			Guarded:   true,
			Refusal:   "deletes a workflow",
			Run:       runWorkflowDelete,
			Human:     humanDeletion,
		},
	}
}

// pageFlags are the two parameters every paged listing takes.
type pageFlags struct {
	limit  int
	cursor string
}

// registerPageFlags attaches --limit and --cursor.
func registerPageFlags(fs *flag.FlagSet) any {
	flags := &pageFlags{}
	fs.IntVar(&flags.limit, "limit", 0, "maximum items to return (server default when 0)")
	fs.StringVar(&flags.cursor, "cursor", "", "opaque cursor from a previous page")

	return flags
}

// query renders the page parameters, omitting what the caller did not set.
func (f *pageFlags) query() url.Values {
	query := url.Values{}
	if f.limit > 0 {
		query.Set("limit", strconv.Itoa(f.limit))
	}
	if strings.TrimSpace(f.cursor) != "" {
		query.Set("cursor", f.cursor)
	}

	return query
}

// createFlags carry the document to create.
type createFlags struct {
	file string
}

// registerCreateFlags attaches --file.
func registerCreateFlags(fs *flag.FlagSet) any {
	flags := &createFlags{}
	fs.StringVar(&flags.file, "file", "", "workflow document to send: a path, or - for stdin")

	return flags
}

// exportFlags carry the one query the export operation accepts.
type exportFlags struct {
	format string
}

// registerExportFlags attaches --format.
func registerExportFlags(fs *flag.FlagSet) any {
	flags := &exportFlags{}
	fs.StringVar(&flags.format, "format", "", "export format; n8n is the only one the server supports")

	return flags
}

// diagnosticsFlags name the revision to read.
type diagnosticsFlags struct {
	versionID string
}

// registerDiagnosticsFlags attaches --version-id.
func registerDiagnosticsFlags(fs *flag.FlagSet) any {
	flags := &diagnosticsFlags{}
	fs.StringVar(&flags.versionID, "version-id", "", "revision to read; defaults to the newest")

	return flags
}

// resourceList is one page of a listing: the items as the server sent them,
// how many there are, and the cursor for the next page.
//
// The items are kept as raw JSON so the envelope carries the API's own field
// names and values; the count is what an agent branches on, and the cursor is
// what it pages with.
type resourceList struct {
	Items      []json.RawMessage `json:"items"`
	Count      int               `json:"count"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// runWorkflowList reads one page of workflows.
func runWorkflowList(ctx *Context, args []string) error {
	if err := refusePositional(args, "workflow list"); err != nil {
		return err
	}

	list, err := ctx.listPage("/workflows", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runWorkflowGet reads one workflow.
func runWorkflowGet(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodGet, "/workflows/"+url.PathEscape(id), nil, id)
}

// runWorkflowCreate sends a canonical document unchanged.
//
// The document is checked for being JSON before it is sent: the API would
// answer a malformed body with 422, which the exit contract maps to "fix the
// invocation" anyway — so the CLI says so before spending a request.
func runWorkflowCreate(ctx *Context, args []string) error {
	if err := refusePositional(args, "workflow create"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*createFlags)
	if !ok {
		return usageError("the workflow create verb was registered without its flags")
	}
	if strings.TrimSpace(flags.file) == "" {
		return usageError("no document: pass --file <path>, or --file - to read it from stdin")
	}

	document, err := readDocument(flags.file, ctx.Env.Stdin)
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath("/workflows"), nil, nil, document)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The identifier a caller pipes onward comes from the Location header the
	// API sets, so --quiet prints the same id the API named rather than a
	// field the CLI happened to find in the body.
	ctx.Primary = locationID(resp.Header.Get("Location"))
	if ctx.Primary == "" {
		ctx.Primary = fieldValue(resp.Body, "id")
	}

	return nil
}

// runWorkflowVersions reads one page of a workflow's revisions.
func runWorkflowVersions(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	list, err := ctx.listPage("/workflows/"+url.PathEscape(id)+"/versions", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runWorkflowGetVersion reads one revision, document included.
func runWorkflowGetVersion(ctx *Context, args []string) error {
	id, versionID, err := requireTwoIDs(args, "workflow id", "revision id")
	if err != nil {
		return err
	}

	path := "/workflows/" + url.PathEscape(id) + "/versions/" + url.PathEscape(versionID)

	return ctx.readResource(http.MethodGet, path, nil, versionID)
}

// runWorkflowPublishEvents reads the audit trail. The operation is not paged.
func runWorkflowPublishEvents(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	path := "/workflows/" + url.PathEscape(id) + "/publish-events"

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)

	return nil
}

// runWorkflowExport converts a workflow into n8n JSON.
func runWorkflowExport(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*exportFlags)
	if !ok {
		return usageError("the workflow export verb was registered without its flags")
	}

	query := url.Values{}
	if format := strings.TrimSpace(flags.format); format != "" {
		query.Set("format", format)
	}

	return ctx.readResource(http.MethodGet, "/workflows/"+url.PathEscape(id)+"/export", query, id)
}

// runWorkflowDiagnostics reads the import report stored with a revision.
func runWorkflowDiagnostics(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*diagnosticsFlags)
	if !ok {
		return usageError("the workflow diagnostics verb was registered without its flags")
	}

	query := url.Values{}
	if versionID := strings.TrimSpace(flags.versionID); versionID != "" {
		query.Set("versionId", versionID)
	}

	return ctx.readResource(http.MethodGet, "/workflows/"+url.PathEscape(id)+"/diagnostics", query, id)
}

// runWorkflowActivate compiles the workflow's latest revision and pins it as
// the active one, which is what publishes its endpoint.
//
// No body: the operation reads the workflow and its newest revision from the
// server, and a document sent here would be ignored.
func runWorkflowActivate(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodPost, "/workflows/"+url.PathEscape(id)+"/activate", nil, id)
}

// runWorkflowDeactivate takes the workflow's endpoint offline. It keeps the
// active revision on record, which is what makes activating again cheap.
func runWorkflowDeactivate(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodPost, "/workflows/"+url.PathEscape(id)+"/deactivate", nil, id)
}

// runWorkflowDelete soft-deletes a workflow. The API keeps the audit evidence,
// so "deleted" here means "no longer reachable", not "the rows are gone".
func runWorkflowDelete(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	return ctx.deleteResource("/workflows/"+url.PathEscape(id), id)
}

// readResource performs one read whose response is the payload, and records the
// identifier a --quiet caller would print.
func (ctx *Context) readResource(method, path string, query url.Values, primary string) error {
	resp, err := ctx.Client.Do(ctx.Ctx, method, apiPath(path), query, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	ctx.Primary = primary

	return nil
}

// listPage reads one paged listing and normalises it.
//
// The API is not uniform: workflows answer with a bare array and a cursor in a
// header, while revisions and executions answer with an object carrying both.
// A verb should not have to know which, and a caller should not have to care.
func (ctx *Context) listPage(path string, query url.Values) (resourceList, error) {
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath(path), query, nil, nil)
	if err != nil {
		return resourceList{}, err
	}

	items, cursor, err := decodePage(resp.Body)
	if err != nil {
		return resourceList{}, &ExitError{
			Code:    ExitFailure,
			ErrCode: "unexpected_response",
			Message: "the listing at " + path + " is not a page: " + err.Error(),
		}
	}

	if cursor == "" {
		cursor = resp.Header.Get("X-Next-Cursor")
	}
	if items == nil {
		items = []json.RawMessage{}
	}

	return resourceList{Items: items, Count: len(items), NextCursor: cursor}, nil
}

// listPageResult stores a page and the identifiers a --quiet caller reads.
func listPageResult(ctx *Context, list resourceList) error {
	return listPageResultField(ctx, list, "id")
}

// listPageResultField stores a page and one field of each item, which is the
// identifier a --quiet caller reads: an id for most resources, and the type for
// the node catalogue, whose definitions have no id.
func listPageResultField(ctx *Context, list resourceList, field string) error {
	ctx.Data = list
	ctx.Primary = strings.Join(itemFieldValues(list.Items, field), "\n")

	return nil
}

// itemFieldValues reads one field of each item, skipping an item that does not
// carry it.
func itemFieldValues(items []json.RawMessage, field string) []string {
	values := make([]string, 0, len(items))
	for _, item := range items {
		if value := fieldValue(item, field); value != "" {
			values = append(values, value)
		}
	}

	return values
}

// decodePage reads either shape of listing.
func decodePage(body []byte) ([]json.RawMessage, string, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, "", nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, "", err
		}

		return items, "", nil
	}

	var page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", err
	}

	return page.Items, page.NextCursor, nil
}

// readDocument reads a request body from a file, or from stdin for "-".
func readDocument(path string, stdin io.Reader) ([]byte, error) {
	var (
		raw []byte
		err error
	)

	if strings.TrimSpace(path) == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, usageError("could not read the document from %s: %v", path, err)
	}

	if !json.Valid(raw) {
		return nil, usageError("the document read from %s is not valid JSON", path)
	}

	return raw, nil
}

// locationID is the last path segment of a Location header, which is the id the
// API says it created.
func locationID(location string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(location), "/")
	if trimmed == "" {
		return ""
	}

	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		return trimmed[index+1:]
	}

	return trimmed
}

// pageQuery reads the page flags a listing verb was registered with.
func pageQuery(ctx *Context) url.Values {
	flags, ok := ctx.VerbFlags.(*pageFlags)
	if !ok {
		return nil
	}

	return flags.query()
}

// writeResource performs one write whose body the caller built and whose
// response carries the resource it wrote, and records the identifier a --quiet
// caller would print.
func (ctx *Context) writeResource(method, path string, body []byte, primary string) error {
	resp, err := ctx.Client.Do(ctx.Ctx, method, apiPath(path), nil, nil, body)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	ctx.Primary = primary

	return nil
}

// deleteResource performs one DELETE and records what it removed.
//
// A delete answers 204 with no body, so there is no payload to carry: the
// identifier is echoed back because it is what a --quiet caller reads, and the
// status is there because the server's own "it is gone" is the only evidence a
// deletion leaves in the envelope.
func (ctx *Context) deleteResource(path, primary string) error {
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodDelete, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = deletionResult{ID: primary, Status: resp.Status}
	ctx.Primary = primary

	return nil
}

// deletionResult is what a verb reports after a DELETE that answered 204.
type deletionResult struct {
	ID     string `json:"id"`
	Status int    `json:"status"`
}

// humanDeletion prints what was removed.
func humanDeletion(w io.Writer, data any) {
	result, ok := data.(deletionResult)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"deleted", result.ID},
		{"status", strconv.Itoa(result.Status)},
	})
}

// requireOneID returns the single identifier a verb needs.
//
// An empty identifier is refused rather than sent: `/workflows/` is not a
// workflow, and the SPA catch-all answers it with 200 HTML, which no exit code
// can distinguish from an answer.
func requireOneID(args []string, what string) (string, error) {
	if len(args) == 0 {
		return "", usageError("no %s: pass one as an argument", what)
	}
	if len(args) > 1 {
		return "", usageError("one %s at a time; got %q and %q", what, args[0], args[1])
	}
	if strings.TrimSpace(args[0]) == "" {
		return "", usageError("the %s must not be empty", what)
	}

	return args[0], nil
}

// requireTwoIDs returns the two identifiers a verb needs, in order.
func requireTwoIDs(args []string, first, second string) (string, string, error) {
	if len(args) == 0 {
		return "", "", usageError("no %s: pass one as an argument", first)
	}
	if len(args) == 1 {
		return "", "", usageError("no %s: pass it after the %s", second, first)
	}
	if len(args) > 2 {
		return "", "", usageError("two identifiers at a time; got %q, %q and %q", args[0], args[1], args[2])
	}
	if strings.TrimSpace(args[0]) == "" {
		return "", "", usageError("the %s must not be empty", first)
	}
	if strings.TrimSpace(args[1]) == "" {
		return "", "", usageError("the %s must not be empty", second)
	}

	return args[0], args[1], nil
}

// requireThreeIDs returns the three names a verb needs, in order. It is the
// same rule requireTwoIDs applies, one argument further: renaming a column
// names the datastore, the column and what it becomes, and every one of them is
// refused if it is missing rather than sent empty.
func requireThreeIDs(args []string, first, second, third string) (string, string, string, error) {
	if len(args) < 3 {
		missing := third
		if len(args) == 0 {
			missing = first
		} else if len(args) == 1 {
			missing = second
		}

		return "", "", "", usageError("no %s: pass the %s, the %s and the %s as arguments", missing, first, second, third)
	}
	if len(args) > 3 {
		return "", "", "", usageError("three names at a time; got %q, %q, %q and %q", args[0], args[1], args[2], args[3])
	}
	for index, what := range []string{first, second, third} {
		if strings.TrimSpace(args[index]) == "" {
			return "", "", "", usageError("the %s must not be empty", what)
		}
	}

	return args[0], args[1], args[2], nil
}

// humanResourceList renders a listing as a table of the fields named, falling
// back to the item's JSON when it carries none of them.
func humanResourceList(fields ...string) func(io.Writer, any) {
	return func(w io.Writer, data any) {
		list, ok := data.(resourceList)
		if !ok {
			printJSONValue(w, data)

			return
		}

		rows := make([][]string, 0, len(list.Items))
		for _, item := range list.Items {
			row := make([]string, 0, len(fields))
			for _, field := range fields {
				row = append(row, fmt.Sprint(fieldOf(item, field)))
			}
			rows = append(rows, row)
		}

		printTable(w, fields, rows)
		if list.NextCursor != "" {
			fmt.Fprintf(w, "nextCursor: %s\n", list.NextCursor)
		}
	}
}

// humanWorkflowResource prints a workflow resource as the lines a person needs
// to choose the next command.
func humanWorkflowResource(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var resource struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Active        bool   `json:"active"`
		LatestVersion struct {
			ID       string `json:"id"`
			Revision int    `json:"revision"`
		} `json:"latestVersion"`
		UpdatedAt string `json:"updatedAt"`
	}
	if err := json.Unmarshal(raw, &resource); err != nil {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"id", resource.ID},
		{"name", resource.Name},
		{"active", strconv.FormatBool(resource.Active)},
		{"latest", resource.LatestVersion.ID + " (revision " + strconv.Itoa(resource.LatestVersion.Revision) + ")"},
		{"updated", resource.UpdatedAt},
	})
}

// humanResourceResource prints a resource this phase has no shape for.
func humanResourceResource(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printJSONValue(w, raw)
}

// fieldOf reads one field out of a raw JSON item for display.
func fieldOf(item json.RawMessage, name string) any {
	var fields map[string]any
	if err := json.Unmarshal(item, &fields); err != nil {
		return nil
	}

	return fields[name]
}
