package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// apiVerbPath is the escape hatch's own path, spelled once so the MCP adapter
// can recognise it without repeating the literal.
const apiVerbPath = "api"

// apiVerbs is the generic escape hatch: one verb that reaches every operation
// the running server serves by its operation id.
//
// It exists so the surface is complete on day one and so an operation nobody
// wrote a verb for is reachable without waiting for a release. It is a naming
// bypass, never a consent or an authority bypass: an operation id that a
// guarded verb wraps asks runAPI for the same `--yes` and the same
// tenant-wide key that verb would (BUG-r1m83f) before anything is sent, and
// every other operation carries exactly the configured credential, with a
// refusal from the server carried through unchanged.
func apiVerbs() []Verb {
	return []Verb{
		{
			Path:    apiVerbPath,
			Summary: "call any operation the running server serves, by operation id (`--list` to enumerate)",
			Args:    []Arg{optionalArg("operation id")},
			Flags:   registerAPIFlags,
			Run:     runAPI,
			Human:   humanAPIResult,
		},
	}
}

// guardedByOperation indexes the guarded verbs by the operation id each one
// wraps, so the escape hatch can ask for the same confirmation and the same
// authority a named verb would, even though it reaches the operation by id
// rather than by name.
func guardedByOperation() map[string]Verb {
	byOperation := make(map[string]Verb)
	for _, verb := range registry() {
		if verb.Guarded && verb.Operation != "" {
			byOperation[verb.Operation] = verb
		}
	}

	return byOperation
}

// apiGuardVerb adapts a guarded verb's metadata to the escape hatch: the
// refusal has to name the operation id the caller typed — what `api --list`
// would show — rather than the verb's own argument shape, which the escape
// hatch does not have. Naming the verb it wraps ("activate-workflow is
// `workflow activate`") is what tells the caller there is a shorter,
// better-typed way to ask for the same thing.
func apiGuardVerb(id string, wrapped Verb) Verb {
	return Verb{
		Path:    id,
		Guarded: true,
		Refusal: "is `" + wrapped.Path + "`",
	}
}

// apiFlags are the escape hatch's own flags: where the parameters go, and what
// to do with the body that comes back.
type apiFlags struct {
	list     bool
	paths    *pairFlags
	query    *pairFlags
	header   *pairFlags
	body     string
	bodyFile string
	out      string
}

// registerAPIFlags attaches the verb's flags and returns them for Run.
func registerAPIFlags(fs *flag.FlagSet) any {
	flags := &apiFlags{
		paths:  newPairFlags(),
		query:  newPairFlags(),
		header: newPairFlags(),
	}

	fs.BoolVar(&flags.list, "list", false, "print every operation id the running server serves, sorted")
	fs.Var(flags.paths, "path", "fill one path placeholder, as name=value (repeatable)")
	fs.Var(flags.query, "query", "add one query parameter, as name=value (repeatable)")
	fs.Var(flags.header, "header", "add one request header, as name=value (repeatable)")
	fs.StringVar(&flags.body, "body", "", "request body: JSON, @<file>, or - to read stdin")
	fs.StringVar(&flags.bodyFile, "body-file", "", "read the request body from a file")
	fs.StringVar(&flags.out, "out", "", "write the response body to a file; - writes it to stdout raw")

	return flags
}

// apiList is the payload of `api --list`.
type apiList struct {
	Operations []string `json:"operations"`
	Count      int      `json:"count"`
}

// apiRaw is a response body the CLI cannot decode as JSON, carried as bytes so
// the envelope always holds valid JSON.
type apiRaw struct {
	ContentType string `json:"contentType"`
	Raw         []byte `json:"raw"`
}

// apiWritten is the payload of `--out <path>`.
type apiWritten struct {
	Path        string `json:"path"`
	Bytes       int    `json:"bytes"`
	ContentType string `json:"contentType,omitempty"`
}

// runAPI lists the served operations, or calls one of them.
func runAPI(ctx *Context, args []string) error {
	flags, ok := ctx.VerbFlags.(*apiFlags)
	if !ok {
		return usageError("the api verb was registered without its flags")
	}

	// The argument shape is checked before the document is read: a missing or
	// doubled operation id is a usage error that needs no server, and it should
	// not cost a request.
	if flags.list {
		if len(args) > 0 {
			return usageError("--list enumerates every operation; it takes no operation id (got %q)", args[0])
		}
	} else {
		if len(args) == 0 {
			return usageError("no operation id: run `kilasflow api --list` to see what this server serves")
		}
		if len(args) > 1 {
			return usageError("one operation id at a time; got %q and %q", args[0], args[1])
		}
	}

	// An operation id a guarded verb wraps is guarded whichever name reached
	// it: ask for the same confirmation and the same authority that verb
	// would, before the document is read, so a refusal here — like a refusal
	// from a named verb — sends nothing at all. `--list` never resolves an id
	// and stays unguarded.
	if !flags.list {
		if wrapped, guarded := guardedByOperation()[args[0]]; guarded {
			guard := apiGuardVerb(args[0], wrapped)
			if err := requireConfirmation(guard, ctx.Flags.Yes, ctx.Env); err != nil {
				return err
			}
			if err := requireAuthority(ctx, guard); err != nil {
				return err
			}
		}
	}

	operations, err := ctx.Client.Operations(ctx.Ctx)
	if err != nil {
		return err
	}

	if flags.list {
		ids := sortedOperations(operations)
		ctx.Data = apiList{Operations: ids, Count: len(ids)}
		// One id per line for --quiet, so a shell pipeline can iterate them:
		// the envelope is the primary output, and for a listing that is the list.
		ctx.Primary = strings.Join(ids, "\n")

		return nil
	}

	id := args[0]
	method, target, err := resolveOperation(operations, id, flags.paths.first(), flags.query.asQuery())
	if err != nil {
		return err
	}
	// The envelope reports the operation that was actually called, so a log line
	// from `api get-workflow` traces back to the API call and not to the verb.
	ctx.Operation = id

	body, err := flags.requestBody(ctx.Env.Stdin)
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, method, target, nil, flags.header.header(), body)
	if err != nil {
		return err
	}

	if flags.out != "" {
		return writeAPIResponse(ctx, flags.out, resp)
	}

	// A JSON body travels unmodified: the API's own field names are what a
	// caller reads, and re-encoding them would invent a second vocabulary.
	if json.Valid(resp.Body) {
		ctx.Data = json.RawMessage(resp.Body)
		ctx.Primary = fieldValue(resp.Body, "id")

		return nil
	}

	ctx.Data = apiRaw{ContentType: resp.ContentType, Raw: resp.Body}

	return nil
}

// writeAPIResponse puts the response body where --out asked for it.
//
// `-` streams the bytes to stdout and suppresses the envelope: the bytes are
// the output, and wrapping them would make the verb useless in a pipeline. Any
// other path is written as-is and reported in the envelope.
func writeAPIResponse(ctx *Context, out string, resp *Response) error {
	if out == "-" {
		if _, err := stdout(ctx.Env).Write(resp.Body); err != nil {
			return outputWriteError("could not write the response to stdout: %v", err)
		}
		ctx.Streamed = true

		return nil
	}

	if err := os.WriteFile(out, resp.Body, 0o600); err != nil {
		return outputWriteError("could not write %s: %v", out, err)
	}

	ctx.Data = apiWritten{Path: out, Bytes: len(resp.Body), ContentType: resp.ContentType}

	return nil
}

// requestBody resolves --body / --body-file into the bytes to send.
//
// An inline body is documented as JSON and is checked here, so a typo is a
// usage error before a request is made rather than a 422 after it. A file or
// stdin is passed through untouched, because an operation such as the CSV
// import takes bytes that are not JSON at all.
func (f *apiFlags) requestBody(stdin io.Reader) ([]byte, error) {
	switch {
	case f.body != "" && f.bodyFile != "":
		return nil, usageError("--body and --body-file are mutually exclusive")
	case f.bodyFile != "":
		return readBodyFile(f.bodyFile)
	case f.body == "":
		return nil, nil
	case f.body == "-":
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return nil, usageError("could not read the request body from stdin: %v", err)
		}

		return raw, nil
	case strings.HasPrefix(f.body, "@"):
		return readBodyFile(strings.TrimPrefix(f.body, "@"))
	default:
		if !json.Valid([]byte(f.body)) {
			return nil, usageError("--body is not valid JSON: %s", f.body)
		}

		return []byte(f.body), nil
	}
}

// readBodyFile reads a request body from disk.
func readBodyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, usageError("could not read the request body from %s: %v", path, err)
	}

	return raw, nil
}

// humanAPIResult prints one response for a person: a listing as one id per
// line, a JSON body as it arrived, and anything else as the envelope payload
// (a non-JSON body is reported with its media type and its bytes in base64).
func humanAPIResult(w io.Writer, data any) {
	switch typed := data.(type) {
	case apiList:
		for _, id := range typed.Operations {
			fmt.Fprintln(w, id)
		}
	case json.RawMessage:
		fmt.Fprintln(w, string(typed))
	default:
		printJSONValue(w, data)
	}
}

// pairFlags collects a repeatable name=value flag, keeping each name's values
// in the order they were given.
type pairFlags struct {
	values map[string][]string
	keys   []string
}

// newPairFlags returns an empty collector.
func newPairFlags() *pairFlags {
	return &pairFlags{values: map[string][]string{}}
}

// String renders the flag as it would be written on a command line.
func (p *pairFlags) String() string {
	parts := make([]string, 0, len(p.keys))
	for _, key := range p.keys {
		for _, value := range p.values[key] {
			parts = append(parts, key+"="+value)
		}
	}

	return strings.Join(parts, ",")
}

// Set records one name=value pair. flag reports the error and exits 2 with the
// usage line, so a malformed pair never reaches the server or a template.
func (p *pairFlags) Set(raw string) error {
	name, value, found := strings.Cut(raw, "=")
	name = strings.TrimSpace(name)
	if !found || name == "" {
		return fmt.Errorf("want name=value, got %q", raw)
	}

	if _, seen := p.values[name]; !seen {
		p.keys = append(p.keys, name)
	}
	p.values[name] = append(p.values[name], value)

	return nil
}

// first is the first value of every name, which is what a path placeholder
// takes. A second --path id=... is a mistake the caller would rather see
// silently pinned to the first value than have fail: no path placeholder takes
// two values, so there is nothing to choose between them.
func (p *pairFlags) first() map[string]string {
	out := make(map[string]string, len(p.keys))
	for _, key := range p.keys {
		out[key] = p.values[key][0]
	}

	return out
}

// asQuery renders the collector as query parameters.
func (p *pairFlags) asQuery() url.Values {
	query := make(url.Values, len(p.keys))
	for _, key := range p.keys {
		query[key] = append([]string(nil), p.values[key]...)
	}

	return query
}

// header renders the collector as request headers.
func (p *pairFlags) header() http.Header {
	header := make(http.Header, len(p.keys))
	for _, key := range p.keys {
		for _, value := range p.values[key] {
			header.Add(key, value)
		}
	}

	return header
}
