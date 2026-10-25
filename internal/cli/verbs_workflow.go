package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// workflowVerbs are the workflow verbs this phase owns: the read surface,
// `create`, the n8n import/export pair, and the three guarded lifecycle verbs.
//
// The paths are the stable ones documented in the API contract, not a lookup in
// the served document: `workflow get` is one request, and an agent that has to
// read `/api/openapi.json` first to learn a path would be paying for the escape
// hatch's flexibility on every call. `kilasflow api <operation-id>` remains the
// verb that resolves against the running server.
//
// activate, deactivate, delete and import are the design's guarded verbs: each
// refuses without --yes, and each refuses a scoped agent token outright (see
// requireAuthority), because publishing an endpoint, destroying a workflow and
// creating one from an outside document are the tenant's own decisions rather
// than a scoped key's.
//
// `validate` and `duplicate` are ordinary verbs rather than guarded ones: one
// answers a question about a document the caller already holds and saves
// nothing, and the other copies a workflow the caller can already read.
//
// Deliberately absent: update and restore. They have no verb yet, and
// `kilasflow api <operation-id>` reaches them today.
func workflowVerbs() []Verb {
	return []Verb{
		{
			Path:      "workflow list",
			Operation: "list-workflows",
			Summary:   "list workflows, newest first (`--limit`, `--cursor`, `--tag`, `--name`, `--active`)",
			Flags:     registerWorkflowListFlags,
			Run:       runWorkflowList,
			Human:     humanResourceList("id", "name", "active", "updatedAt"),
		},
		{
			Path:      "workflow get",
			Operation: "get-workflow",
			Summary:   "read one workflow by id",
			Args:      []Arg{arg("workflow id")},
			Run:       runWorkflowGet,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow create",
			Operation: "create-workflow",
			Summary:   "create a workflow from a canonical document (`--file <path>|-`)",
			Flags:     registerCreateFlags("workflow"),
			Run:       runWorkflowCreate,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow import",
			Operation: "import-workflow",
			Summary:   "import n8n workflow JSON as a draft (`--file <path>|-`, or `--dir <directory>`) (guarded)",
			Flags:     registerImportFlags,
			Guarded:   true,
			Refusal:   "creates a workflow from an imported n8n document",
			Run:       runWorkflowImport,
			Human:     humanWorkflowImport,
		},
		{
			Path:      "workflow validate",
			Operation: "validate-workflow-document",
			Summary:   "check a draft document the way the API would, saving nothing (`--file <path>|-`)",
			Flags:     registerValidateFlags,
			Run:       runWorkflowValidate,
			Human:     humanWorkflowValidation,
		},
		{
			Path:      "workflow duplicate",
			Operation: "duplicate-workflow",
			Summary:   "copy a workflow (`--name <name>` to rename the copy)",
			Args:      []Arg{arg("workflow id")},
			Flags:     registerDuplicateFlags,
			Run:       runWorkflowDuplicate,
			Human:     humanWorkflowResource,
		},
		{
			Path:      "workflow versions",
			Operation: "list-workflow-versions",
			Summary:   "list a workflow's revisions (`--limit`, `--cursor`)",
			Args:      []Arg{arg("workflow id")},
			Flags:     registerPageFlags,
			Run:       runWorkflowVersions,
			Human:     humanResourceList("id", "revision", "draft", "published", "createdAt"),
		},
		{
			Path:      "workflow get-version",
			Operation: "get-workflow-version",
			Summary:   "read one revision of a workflow, with its document",
			Args:      []Arg{arg("workflow id"), arg("revision id")},
			Run:       runWorkflowGetVersion,
			Human:     humanResourceResource,
		},
		{
			Path:      "workflow publish-events",
			Operation: "list-workflow-publish-events",
			Summary:   "read a workflow's publish audit trail",
			Args:      []Arg{arg("workflow id")},
			Run:       runWorkflowPublishEvents,
			Human:     humanResourceList("createdAt", "action", "versionId", "actor"),
		},
		{
			Path:      "workflow export",
			Operation: "export-workflow",
			Summary: "export a workflow as n8n JSON (`--out <file>` writes the bare n8n document; " +
				"`--all --out-dir <dir>` writes every workflow to one)",
			Args:  []Arg{optionalArg("workflow id")},
			Flags: registerExportFlags,
			Run:   runWorkflowExport,
			Human: humanWorkflowExport,
		},
		{
			Path:      "workflow diagnostics",
			Operation: "workflow-diagnostics",
			Summary:   "read the import report stored with a revision",
			Args:      []Arg{arg("workflow id")},
			Flags:     registerDiagnosticsFlags,
			Run:       runWorkflowDiagnostics,
			Human:     humanResourceResource,
		},
		{
			Path:      "workflow activate",
			Operation: "activate-workflow",
			Summary:   "activate a workflow's latest revision, publishing its endpoint; `--all` activates every inactive workflow (guarded)",
			Args:      []Arg{optionalArg("workflow id")},
			Flags:     registerLifecycleFlags,
			Guarded:   true,
			Refusal:   "publishes a public endpoint",
			Run:       runWorkflowActivate,
			Human:     humanWorkflowLifecycle,
		},
		{
			Path:      "workflow deactivate",
			Operation: "deactivate-workflow",
			Summary:   "take a workflow's published endpoint offline; `--all` deactivates every active workflow (guarded)",
			Args:      []Arg{optionalArg("workflow id")},
			Flags:     registerLifecycleFlags,
			Guarded:   true,
			Refusal:   "takes a public endpoint offline",
			Run:       runWorkflowDeactivate,
			Human:     humanWorkflowLifecycle,
		},
		{
			Path:      "workflow delete",
			Operation: "delete-workflow",
			Summary:   "delete a workflow, retaining its audit trail (guarded)",
			Args:      []Arg{arg("workflow id")},
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

// workflowListFlags are the page parameters plus the listing's filters.
type workflowListFlags struct {
	pageFlags
	tag string
	// name and active filter the listing here rather than by the server: the
	// listing operation takes no such parameters, and the CLI pages until the
	// filters are satisfied (FEAT-sc3qrq).
	name   string
	active string
}

// registerWorkflowListFlags attaches --limit, --cursor, --tag, --name and
// --active.
func registerWorkflowListFlags(fs *flag.FlagSet) any {
	flags := &workflowListFlags{}
	fs.IntVar(&flags.limit, "limit", 0, "maximum items to return (server default when 0)")
	fs.StringVar(&flags.cursor, "cursor", "", "opaque cursor from a previous page")
	fs.StringVar(&flags.tag, "tag", "", "only workflows carrying this tag (case is ignored)")
	fs.StringVar(&flags.name, "name", "", "only workflows whose name contains this text (case is ignored)")
	fs.StringVar(&flags.active, "active", "", "only workflows in this state: true or false")

	return flags
}

// query renders the page parameters and the tag filter, omitting what the
// caller did not set. --name and --active are deliberately absent: they are
// applied here, page by page, rather than sent as parameters the server does
// not read.
func (f *workflowListFlags) query() url.Values {
	query := f.pageFlags.query()
	if tag := strings.TrimSpace(f.tag); tag != "" {
		query.Set("tag", tag)
	}

	return query
}

// createFlags carry the document to create.
type createFlags struct {
	file string
}

// registerCreateFlags attaches --file, described as a document of the named
// kind. The flag is shared by every verb that sends a document, so the noun is
// the verb's: with one fixed noun, `credential create -h` described a workflow
// document.
func registerCreateFlags(noun string) func(fs *flag.FlagSet) any {
	return func(fs *flag.FlagSet) any {
		flags := &createFlags{}
		fs.StringVar(&flags.file, "file", "", noun+" document to send: a path, or - for stdin")

		return flags
	}
}

// validateFlags carry the draft document to validate.
type validateFlags struct {
	file string
}

// registerValidateFlags attaches --file.
func registerValidateFlags(fs *flag.FlagSet) any {
	flags := &validateFlags{}
	fs.StringVar(&flags.file, "file", "", "workflow document to validate: a path, or - for stdin")

	return flags
}

// duplicateFlags carry the name the copy should take.
type duplicateFlags struct {
	name string
}

// registerDuplicateFlags attaches --name.
func registerDuplicateFlags(fs *flag.FlagSet) any {
	flags := &duplicateFlags{}
	fs.StringVar(&flags.name, "name", "", "name for the copy; omit to let the server derive one")

	return flags
}

// importFlags carry where the n8n documents come from: one file, or a
// directory of them.
type importFlags struct {
	file string
	dir  string
}

// registerImportFlags attaches --file and --dir.
func registerImportFlags(fs *flag.FlagSet) any {
	flags := &importFlags{}
	fs.StringVar(&flags.file, "file", "", "n8n workflow JSON to import: a path, or - for stdin")
	fs.StringVar(&flags.dir, "dir", "", "import every .json file in this directory, in name order")

	return flags
}

// exportFlags carry the export's query and where the bare document goes.
type exportFlags struct {
	format string
	out    string
	outDir string
	all    bool
}

// registerExportFlags attaches --format, --out, --out-dir and --all.
func registerExportFlags(fs *flag.FlagSet) any {
	flags := &exportFlags{}
	fs.StringVar(&flags.format, "format", "", "export format; n8n is the only one the server supports")
	fs.StringVar(&flags.out, "out", "", "write the bare n8n document to this file; - writes it to stdout, with the lossy report on stderr")
	fs.StringVar(&flags.outDir, "out-dir", "", "with --all: write one bare n8n document per workflow into this directory")
	fs.BoolVar(&flags.all, "all", false, "export every workflow (requires --out-dir)")

	return flags
}

// lifecycleFlags carry the bulk form of the two lifecycle verbs.
type lifecycleFlags struct {
	all bool
}

// registerLifecycleFlags attaches --all.
func registerLifecycleFlags(fs *flag.FlagSet) any {
	flags := &lifecycleFlags{}
	fs.BoolVar(&flags.all, "all", false, "apply to every workflow: activate the inactive ones, deactivate the active ones")

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

// runWorkflowList reads one page of workflows, or — when --name or --active
// was given — pages through the whole listing keeping only the matches.
//
// The filters are applied here because the listing operation has no such
// parameters: the CLI walks the pages (following each page's cursor) and keeps
// the workflows whose name contains --name and whose active flag is --active,
// both compared without regard to case. --limit stops the walk after that many
// matches; without it the walk reads every page the server has.
func runWorkflowList(ctx *Context, args []string) error {
	if err := refusePositional(args, "workflow list"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*workflowListFlags)
	if !ok {
		return usageError("the workflow list verb was registered without its flags")
	}

	name := strings.TrimSpace(flags.name)
	active, err := parseActiveFlag(flags.active)
	if err != nil {
		return err
	}
	if name == "" && !active.set {
		list, err := ctx.listPage("/workflows", flags.query())
		if err != nil {
			return err
		}

		return listPageResult(ctx, list)
	}

	matched := []json.RawMessage{}
	err = ctx.walkPages("/workflows", flags.query(), func(item json.RawMessage) bool {
		if name != "" && !strings.Contains(strings.ToLower(fieldText(item, "name")), strings.ToLower(name)) {
			return true
		}
		if active.set && fieldActive(item) != active.value {
			return true
		}
		matched = append(matched, item)

		return flags.limit <= 0 || len(matched) < flags.limit
	})
	if err != nil {
		return err
	}

	return listPageResult(ctx, resourceList{Items: matched, Count: len(matched)})
}

// activeFilter is --active as a tri-state: unset means no filter, and
// true/false are the only spellings.
type activeFilter struct {
	set   bool
	value bool
}

// parseActiveFlag reads --active, refusing anything a caller might mean but
// did not say: "1", "yes" and the empty string a shell left behind are usage
// errors rather than guesses.
func parseActiveFlag(raw string) (activeFilter, error) {
	switch value := strings.TrimSpace(raw); value {
	case "":
		return activeFilter{}, nil
	case "true":
		return activeFilter{set: true, value: true}, nil
	case "false":
		return activeFilter{set: true, value: false}, nil
	default:
		return activeFilter{}, usageError("--active wants true or false; got %q", value)
	}
}

// fieldText reads one string field out of a raw JSON item.
func fieldText(item json.RawMessage, field string) string {
	text, _ := fieldOf(item, field).(string)

	return text
}

// fieldActive reads an item's active flag, answering false for an item that
// carries none: a listing the server filtered to inactive workflows is the
// only shape where the field can be missing, and false is what it means there.
func fieldActive(item json.RawMessage) bool {
	active, _ := fieldOf(item, "active").(bool)

	return active
}

// walkPages reads a listing page by page, following each page's cursor, and
// hands every item to visit until the pages end or visit says stop.
func (ctx *Context) walkPages(path string, query url.Values, visit func(item json.RawMessage) bool) error {
	for {
		list, err := ctx.listPage(path, query)
		if err != nil {
			return err
		}
		for _, item := range list.Items {
			if !visit(item) {
				return nil
			}
		}
		if list.NextCursor == "" {
			return nil
		}
		query = url.Values{"cursor": []string{list.NextCursor}}
	}
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

	document, err := readWorkflowDocument(flags.file, ctx.Env.Stdin)
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

// runWorkflowImport imports n8n workflow JSON through the import operation:
// one document via --file (or - for stdin), or every .json in a directory via
// --dir, n8n's `--separate` form.
//
// Each file is sent wrapped in the envelope the import operation reads —
// {format, workflow} — so the n8n export goes in exactly as it came out of
// n8n, which is the wrapper the audit found people building by hand. The
// first file the server refuses stops the run with the file's name in the
// message: a migration that silently skipped half its files is worse than one
// that stopped and said which file it stopped on.
func runWorkflowImport(ctx *Context, args []string) error {
	if err := refusePositional(args, "workflow import"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*importFlags)
	if !ok {
		return usageError("the workflow import verb was registered without its flags")
	}
	file, dir := strings.TrimSpace(flags.file), strings.TrimSpace(flags.dir)
	switch {
	case file != "" && dir != "":
		return usageError("--file and --dir are mutually exclusive; pass one or the other")
	case file == "" && dir == "":
		return usageError("no document: pass --file <path> (or - for stdin), or --dir <directory>")
	}

	files := []string{file}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return usageError("could not read the directory %s: %v", dir, err)
		}
		files = files[:0]
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			files = append(files, filepath.Join(dir, entry.Name()))
		}
		sort.Strings(files)
		if len(files) == 0 {
			return usageError("the directory %s holds no .json workflow to import", dir)
		}
	}

	imported := make([]importedDocument, 0, len(files))
	for _, path := range files {
		one, err := importDocument(ctx, path)
		if err != nil {
			return annotatePathError(path, err)
		}
		imported = append(imported, one)
	}

	if len(files) == 1 {
		// One document answers with the import's own response: the saved
		// workflow, everything the adapter reported, and the webhook
		// addresses — the same payload `api import-workflow` returns.
		ctx.Data = imported[0].response
		ctx.Primary = imported[0].ID

		return nil
	}
	ids := make([]string, 0, len(imported))
	for _, one := range imported {
		ids = append(ids, one.ID)
	}
	ctx.Data = importBatch{Imported: imported, Count: len(imported)}
	ctx.Primary = strings.Join(ids, "\n")

	return nil
}

// importedDocument is one file's outcome.
type importedDocument struct {
	// File is the path the document was read from, "." for stdin.
	File string `json:"file,omitempty"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Unsupported counts what the adapter reported for this document —
	// carried differently, dropped, or blocking — and the report itself
	// travels in the single-file response.
	Unsupported int `json:"unsupported"`
	Webhooks    int `json:"webhooks"`
	// response is the import operation's whole answer, kept for the
	// single-file case so its envelope is the API's own payload.
	response json.RawMessage
}

// importBatch is the payload of a directory import.
type importBatch struct {
	Imported []importedDocument `json:"imported"`
	Count    int                `json:"count"`
}

// importDocument reads one n8n document and imports it.
func importDocument(ctx *Context, path string) (importedDocument, error) {
	document, err := readDocument(path, ctx.Env.Stdin)
	if err != nil {
		return importedDocument{}, err
	}
	// The wrapper the audit's step 1 demanded: {format, workflow} around the
	// bare n8n document, so the file an n8n export wrote needs no editing
	// before it can be imported here.
	body, err := json.Marshal(struct {
		Format   string          `json:"format"`
		Workflow json.RawMessage `json:"workflow"`
	}{Format: "n8n", Workflow: document})
	if err != nil {
		return importedDocument{}, usageError("could not render the import request: %v", err)
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath("/workflows/import"), nil, nil, body)
	if err != nil {
		return importedDocument{}, err
	}

	var resource struct {
		Workflow struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workflow"`
		Unsupported []json.RawMessage `json:"unsupported"`
		Webhooks    []json.RawMessage `json:"webhooks"`
	}
	if err := json.Unmarshal(resp.Body, &resource); err != nil {
		return importedDocument{}, &ExitError{
			Code: ExitFailure, ErrCode: "unexpected_response",
			Message: "the import is not the response this CLI expects: " + err.Error(),
		}
	}
	one := importedDocument{
		File: displayPath(path), ID: resource.Workflow.ID, Name: resource.Workflow.Name,
		Unsupported: len(resource.Unsupported), Webhooks: len(resource.Webhooks),
	}
	if raw, ok := jsonOrText(resp.Body).(json.RawMessage); ok {
		one.response = raw
	} else {
		encoded, _ := json.Marshal(string(resp.Body))
		one.response = encoded
	}
	if one.ID == "" {
		one.ID = locationID(resp.Header.Get("Location"))
	}

	return one, nil
}

// displayPath is the name a report prints for a document's source: "." for
// stdin, the path as given for a file.
func displayPath(path string) string {
	if strings.TrimSpace(path) == "-" {
		return "-"
	}

	return path
}

// annotatePathError names the file a batch stopped on, keeping the exit code
// and error code the failure already carries: a caller branching on exit 3 or
// exit 4 must not see exit 1 because a file name was prepended.
func annotatePathError(path string, err error) error {
	failure, ok := err.(*ExitError)
	if !ok {
		return fmt.Errorf("%s: %w", path, err)
	}
	annotated := *failure
	annotated.Message = path + ": " + failure.Message

	return &annotated
}

// The document is sent unchanged, the way `workflow create` sends one, and is
// checked for being JSON before it is sent for the same reason: the API would
// answer a malformed body with 422, which the exit contract maps to "fix the
// invocation" anyway.
//
// A document the API reports as invalid is an answer, not a failure: the
// operation is 200 with valid:false and the diagnostics, so the verb exits 0
// and --quiet prints the verdict, which is what a pipeline branches on.
// runWorkflowValidate checks a draft document without saving it.
// runWorkflowValidate checks a draft document without saving it.
//
// The document is sent unchanged, the way `workflow create` sends one, and is
// checked for being JSON before it is sent for the same reason: the API would
// answer a malformed body with 422, which the exit contract maps to "fix the
// invocation" anyway.
func runWorkflowValidate(ctx *Context, args []string) error {
	if err := refusePositional(args, "workflow validate"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*validateFlags)
	if !ok {
		return usageError("the workflow validate verb was registered without its flags")
	}
	if strings.TrimSpace(flags.file) == "" {
		return usageError("no document: pass --file <path>, or --file - to read it from stdin")
	}

	document, err := readWorkflowDocument(flags.file, ctx.Env.Stdin)
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath("/workflows/validate"), nil, nil, document)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The verdict is what a pipeline branches on, so it is what --quiet prints:
	// the document is already on the command line, and the diagnostics are in
	// the payload.
	ctx.Primary = strconv.FormatBool(workflowValidity(resp.Body))

	return nil
}

// runWorkflowDuplicate copies a workflow.
//
// It is not guarded: the copy is a new workflow in the caller's own tenant, so
// it publishes nothing and destroys nothing, and a key that may create a
// workflow may copy one.
func runWorkflowDuplicate(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*duplicateFlags)
	if !ok {
		return usageError("the workflow duplicate verb was registered without its flags")
	}

	// A copy with no name of its own is "let the server derive one", and an
	// absent body is how the run verb says the same kind of thing: nothing to
	// say is not the same as an empty thing to say.
	var body []byte
	if name := strings.TrimSpace(flags.name); name != "" {
		encoded, err := json.Marshal(struct {
			Name string `json:"name"`
		}{Name: name})
		if err != nil {
			return usageError("could not render the duplicate request: %v", err)
		}
		body = encoded
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost,
		apiPath("/workflows/"+url.PathEscape(id)+"/duplicate"), nil, nil, body)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The copy's own id is what a --quiet caller pipes onward: it comes from
	// the Location header the API set for the 201, like `workflow create`.
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
//
// The default is the whole export response — the document, the lossy report
// and the supported mappings — as the envelope's payload. --out writes the
// bare n8n document instead, which is the file n8n's own "Import from File"
// reads: the wrapper around it is this server's, and n8n refuses it. --all
// with --out-dir writes every workflow to one bare document each, the backup
// and migration form.
func runWorkflowExport(ctx *Context, args []string) error {
	flags, ok := ctx.VerbFlags.(*exportFlags)
	if !ok {
		return usageError("the workflow export verb was registered without its flags")
	}
	out, outDir := strings.TrimSpace(flags.out), strings.TrimSpace(flags.outDir)

	if flags.all {
		if err := refusePositional(args, "workflow export --all"); err != nil {
			return err
		}
		if outDir == "" {
			return usageError("--all exports every workflow: pass --out-dir <directory> to say where")
		}
		if out != "" {
			return usageError("--out writes one workflow and --all writes every one: pass --out-dir instead of --out")
		}

		return exportEveryWorkflow(ctx, outDir)
	}

	if outDir != "" {
		return usageError("--out-dir is for --all; a single workflow wants --out <file>")
	}

	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	query := url.Values{}
	if format := strings.TrimSpace(flags.format); format != "" {
		query.Set("format", format)
	}
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet,
		apiPath("/workflows/"+url.PathEscape(id)+"/export"), query, nil, nil)
	if err != nil {
		return err
	}

	if out == "" {
		ctx.Data = jsonOrText(resp.Body)
		ctx.Primary = id

		return nil
	}

	var resource struct {
		Workflow json.RawMessage `json:"workflow"`
		Lossy    []exportNote    `json:"lossy"`
	}
	if err := json.Unmarshal(resp.Body, &resource); err != nil {
		return &ExitError{
			Code: ExitFailure, ErrCode: "unexpected_response",
			Message: "the export of " + id + " is not the response this CLI expects: " + err.Error(),
		}
	}
	if len(resource.Workflow) == 0 {
		return &ExitError{
			Code: ExitFailure, ErrCode: "unexpected_response",
			Message: "the export of " + id + " carried no workflow document",
		}
	}
	if resource.Lossy == nil {
		resource.Lossy = []exportNote{}
	}
	if out == "-" {
		// The bytes are the payload, so the lossy report goes to stderr: a
		// note interleaved into the document stream would corrupt the file
		// being piped.
		for _, note := range resource.Lossy {
			fmt.Fprintf(stderr(ctx.Env), "lossy: %s\n", note.line())
		}
		if _, err := stdout(ctx.Env).Write(resource.Workflow); err != nil {
			return outputWriteError("could not write the export to stdout: %v", err)
		}
		ctx.Streamed = true

		return nil
	}
	if err := os.WriteFile(out, resource.Workflow, 0o600); err != nil {
		return outputWriteError("could not write %s: %v", out, err)
	}
	ctx.Data = exportWritten{Path: out, Bytes: len(resource.Workflow), Format: "n8n", Lossy: resource.Lossy}
	ctx.Primary = out

	return nil
}

// exportNote is one thing an export could not carry, as the API reports it.
type exportNote struct {
	Severity string `json:"severity,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
	Field    string `json:"field,omitempty"`
	Reason   string `json:"reason"`
}

// line renders the note the way a terminal reads it.
func (note exportNote) line() string {
	line := note.Reason
	if note.NodeName != "" {
		line = note.NodeName + ": " + line
	} else if note.Field != "" {
		line = note.Field + ": " + line
	}

	return line
}

// exportWritten is the payload of `workflow export --out`: where the bare n8n
// document went, how big it is, and what the conversion could not carry.
type exportWritten struct {
	Path   string       `json:"path"`
	Bytes  int          `json:"bytes"`
	Format string       `json:"format"`
	Lossy  []exportNote `json:"lossy"`
}

// exportedFile is one workflow's outcome in a --all export.
type exportedFile struct {
	ID    string `json:"id"`
	File  string `json:"file"`
	Lossy int    `json:"lossy"`
}

// exportBatch is the payload of `workflow export --all --out-dir`.
type exportBatch struct {
	Exported []exportedFile `json:"exported"`
	Count    int            `json:"count"`
}

// exportEveryWorkflow writes one bare n8n document per workflow into a
// directory, following the listing's pages to the end.
//
// Files are named by workflow id: names repeat across tenants and ids do not,
// and a migration that overwrote one workflow with another's document would
// be a quiet disaster.
func exportEveryWorkflow(ctx *Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return outputWriteError("could not create %s: %v", dir, err)
	}

	ids := make([]string, 0)
	if err := ctx.walkPages("/workflows", url.Values{}, func(item json.RawMessage) bool {
		if id := fieldText(item, "id"); id != "" {
			ids = append(ids, id)
		}

		return true
	}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return usageError("the server holds no workflow to export")
	}

	exported := make([]exportedFile, 0, len(ids))
	for _, id := range ids {
		resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet,
			apiPath("/workflows/"+url.PathEscape(id)+"/export"), nil, nil, nil)
		if err != nil {
			return annotatePathError(id, err)
		}
		var resource struct {
			Workflow json.RawMessage `json:"workflow"`
			Lossy    []exportNote    `json:"lossy"`
		}
		if err := json.Unmarshal(resp.Body, &resource); err != nil {
			return annotatePathError(id, &ExitError{
				Code: ExitFailure, ErrCode: "unexpected_response",
				Message: "the export is not the response this CLI expects: " + err.Error(),
			})
		}
		if len(resource.Workflow) == 0 {
			return annotatePathError(id, &ExitError{
				Code: ExitFailure, ErrCode: "unexpected_response",
				Message: "the export carried no workflow document",
			})
		}
		path := filepath.Join(dir, id+".json")
		if err := os.WriteFile(path, resource.Workflow, 0o600); err != nil {
			return outputWriteError("could not write %s: %v", path, err)
		}
		exported = append(exported, exportedFile{ID: id, File: path, Lossy: len(resource.Lossy)})
	}

	files := make([]string, 0, len(exported))
	for _, one := range exported {
		files = append(files, one.File)
	}
	ctx.Data = exportBatch{Exported: exported, Count: len(exported)}
	ctx.Primary = strings.Join(files, "\n")

	return nil
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
// server, and a document sent here would be ignored. With --all the same
// operation runs for every workflow that is not active, one POST each.
func runWorkflowActivate(ctx *Context, args []string) error {
	return runWorkflowLifecycle(ctx, args, "activate", false)
}

// runWorkflowDeactivate takes the workflow's endpoint offline. It keeps the
// active revision on record, which is what makes activating again cheap. With
// --all it runs for every workflow that is active.
func runWorkflowDeactivate(ctx *Context, args []string) error {
	return runWorkflowLifecycle(ctx, args, "deactivate", true)
}

// runWorkflowLifecycle is the shared body of the two lifecycle verbs: one
// workflow by id, or — with --all — every workflow whose active flag is
// `currently`, in listing order. A bulk run stops at the first refusal and
// names the workflow it stopped on, so a migration reports exactly where it
// ended rather than skipping onward.
func runWorkflowLifecycle(ctx *Context, args []string, action string, currently bool) error {
	flags, ok := ctx.VerbFlags.(*lifecycleFlags)
	if !ok {
		return usageError("the workflow %s verb was registered without its flags", action)
	}
	if !flags.all {
		id, err := requireOneID(args, "workflow id")
		if err != nil {
			return err
		}

		return ctx.readResource(http.MethodPost, "/workflows/"+url.PathEscape(id)+"/"+action, nil, id)
	}
	if err := refusePositional(args, "workflow "+action+" --all"); err != nil {
		return err
	}

	ids := make([]string, 0)
	if err := ctx.walkPages("/workflows", url.Values{}, func(item json.RawMessage) bool {
		if fieldActive(item) == currently {
			if id := fieldText(item, "id"); id != "" {
				ids = append(ids, id)
			}
		}

		return true
	}); err != nil {
		return err
	}

	changed := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := ctx.Client.Do(ctx.Ctx, http.MethodPost,
			apiPath("/workflows/"+url.PathEscape(id)+"/"+action), nil, nil, nil); err != nil {
			return annotatePathError(id, err)
		}
		changed = append(changed, id)
	}
	ctx.Data = workflowBatch{Action: action, Changed: changed, Count: len(changed)}
	ctx.Primary = strings.Join(changed, "\n")

	return nil
}

// workflowBatch is the payload of a bulk lifecycle change.
type workflowBatch struct {
	Action  string   `json:"action"`
	Changed []string `json:"changed"`
	Count   int      `json:"count"`
}

// humanWorkflowLifecycle prints one workflow's resource, or the bulk form's
// count and the ids it changed.
func humanWorkflowLifecycle(w io.Writer, data any) {
	if batch, ok := data.(workflowBatch); ok {
		pairs := [][2]string{
			{"action", batch.Action},
			{"changed", strconv.Itoa(batch.Count)},
		}
		printKV(w, pairs)
		for _, id := range batch.Changed {
			fmt.Fprintln(w, id)
		}

		return
	}
	humanWorkflowResource(w, data)
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

// readWorkflowDocument reads a workflow document for a verb that sends one,
// without its top-level id.
//
// The document `workflow get` prints carries the workflow's id, and the write
// schema refuses it as server-owned: the workflow a request acts on is the one
// its path names. get → edit → validate is the loop this CLI is built for, so
// the id is dropped here rather than left for every caller to know to strip
// (BUG-6gkd12). A document without one is sent byte for byte as it was read.
func readWorkflowDocument(path string, stdin io.Reader) ([]byte, error) {
	raw, err := readDocument(path, stdin)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return raw, nil
	}
	if _, carriesID := members["id"]; !carriesID {
		return raw, nil
	}
	delete(members, "id")
	return json.Marshal(members)
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
// workflow, and the server answers it with a 404 that reads as a workflow that
// does not exist.
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

// workflowValidity reads the validate operation's verdict.
func workflowValidity(body []byte) bool {
	var verdict struct {
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal(body, &verdict); err != nil {
		return false
	}

	return verdict.Valid
}

// humanWorkflowValidation prints the verdict and then every diagnostic, because
// the reason to run the verb by hand is to read them.
func humanWorkflowValidation(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	type diagnosticLine struct {
		Severity string `json:"severity"`
		NodeID   string `json:"nodeId"`
		Field    string `json:"field"`
		Reason   string `json:"reason"`
	}
	var report struct {
		Valid       bool             `json:"valid"`
		Diagnostics []diagnosticLine `json:"diagnostics"`
		// Warnings carry no severity of their own: they never make the
		// document invalid, so they are printed after the diagnostics as
		// "warning".
		Warnings []diagnosticLine `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"valid", strconv.FormatBool(report.Valid)},
		{"diagnostics", strconv.Itoa(len(report.Diagnostics))},
	})
	for _, warning := range report.Warnings {
		warning.Severity = "warning"
		report.Diagnostics = append(report.Diagnostics, warning)
	}
	for _, diagnostic := range report.Diagnostics {
		// The diagnostic is the import path's own resource, so the reason is
		// under `reason` and the text is carried, never reworded here.
		line := diagnostic.Severity + ": " + diagnostic.Reason
		switch {
		case diagnostic.NodeID != "":
			line = diagnostic.NodeID + ": " + line
		case diagnostic.Field != "":
			line = diagnostic.Field + ": " + line
		}
		fmt.Fprintln(w, line)
	}
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

// humanWorkflowImport prints what an import saved and everything the adapter
// reported, because the reason to run the verb by hand is to read the
// refusals before opening the draft.
func humanWorkflowImport(w io.Writer, data any) {
	if batch, ok := data.(importBatch); ok {
		rows := make([][]string, 0, len(batch.Imported))
		for _, one := range batch.Imported {
			rows = append(rows, []string{one.File, one.ID, one.Name, strconv.Itoa(one.Unsupported)})
		}
		printTable(w, []string{"file", "id", "name", "unsupported"}, rows)

		return
	}

	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}
	var imported struct {
		Workflow struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workflow"`
		Unsupported []struct {
			Severity string `json:"severity"`
			NodeName string `json:"nodeName"`
			Field    string `json:"field"`
			Reason   string `json:"reason"`
		} `json:"unsupported"`
		Webhooks []struct {
			URL string `json:"url"`
		} `json:"webhooks"`
	}
	if err := json.Unmarshal(raw, &imported); err != nil {
		printJSONValue(w, raw)

		return
	}
	printKV(w, [][2]string{
		{"imported", imported.Workflow.ID},
		{"name", imported.Workflow.Name},
		{"unsupported", strconv.Itoa(len(imported.Unsupported))},
	})
	for _, issue := range imported.Unsupported {
		line := issue.Severity + ": " + issue.Reason
		switch {
		case issue.NodeName != "":
			line = issue.NodeName + ": " + line
		case issue.Field != "":
			line = issue.Field + ": " + line
		}
		fmt.Fprintln(w, line)
	}
	for _, webhook := range imported.Webhooks {
		fmt.Fprintln(w, "webhook: "+webhook.URL)
	}
}

// humanWorkflowExport prints where a bare document went and what the
// conversion could not carry, or — for --all — one line per workflow written.
func humanWorkflowExport(w io.Writer, data any) {
	switch typed := data.(type) {
	case exportWritten:
		printKV(w, [][2]string{
			{"written", typed.Path},
			{"bytes", strconv.Itoa(typed.Bytes)},
			{"format", typed.Format},
			{"lossy", strconv.Itoa(len(typed.Lossy))},
		})
		for _, note := range typed.Lossy {
			fmt.Fprintln(w, "lossy: "+note.line())
		}
	case exportBatch:
		rows := make([][]string, 0, len(typed.Exported))
		for _, one := range typed.Exported {
			rows = append(rows, []string{one.ID, one.File, strconv.Itoa(one.Lossy)})
		}
		printTable(w, []string{"id", "file", "lossy"}, rows)
	default:
		printJSONValue(w, data)
	}
}

// fieldOf reads one field out of a raw JSON item for display.
func fieldOf(item json.RawMessage, name string) any {
	var fields map[string]any
	if err := json.Unmarshal(item, &fields); err != nil {
		return nil
	}

	return fields[name]
}
