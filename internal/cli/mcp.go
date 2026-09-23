// The MCP adapter: `kilasflow mcp serve` publishes this binary's command tree
// as Model Context Protocol tools over stdio.
//
// Design §6 is the contract, and it is short: tools map to CLI verbs, a tool's
// description comes from the verb's own summary and from the skills bundle, and
// a guarded verb becomes a tool that needs `confirm: true` where the CLI needs
// `--yes`. Everything below follows from the command tree being the only
// definition of what this binary can do:
//
//   - one tool per verb, named after the verb's path, generated once at startup;
//   - a tool's properties are the verb's own flags — read back out of the
//     FlagSet the verb's Flags function registered, so a flag added to a verb
//     appears as a property with no second list to update — and the positional
//     arguments the verb declares (Verb.Args);
//   - a tool call is turned back into argv and dispatched through run(), so it
//     resolves to the same Verb the CLI dispatches, with the same guard, the
//     same configuration chain and the same envelope;
//   - `confirm: true` adds `--yes` and nothing else does: without it the CLI's
//     own guard refuses with `confirmation_required`, which stays the single
//     implementation of that refusal;
//   - a tool's answer is the CLI's own JSON envelope, verbatim, so an agent
//     reads one output contract whether it drove the CLI or the protocol.
//
// It is a verb of this binary rather than a second executable, so the shipped
// image carries one command (design §4.1 and §6).
//
// Deliberately not here: streamable HTTP (§6 defers it to after stdio), the
// `initialize`-less modern protocol revisions (see internal/mcp), and the
// global flags — --url, --token, --config and the rest are how the server was
// started, not something a tool call gets to change.
package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/mcp"
	"github.com/kilaslab/kilas-flow/internal/skills"
)

// mcpServeVerb is the verb's path, spelled once: it is both a registered verb
// and a name the adapter must not publish as a tool.
const mcpServeVerb = "mcp serve"

// mcpServerModes are the verbs that are not tools: the two ways this process
// runs as a server rather than as a client of one. Publishing them would offer a
// caller the server's own boot path, and `mcp serve` inside a tool call would
// nest a protocol loop inside the protocol.
var mcpServerModes = map[string]bool{ServeVerb: true, mcpServeVerb: true}

// mcpInstructions is what the client shows the model beside the tool list. It
// says the three things a model cannot read off a schema: where the credential
// comes from, what a result is, and what `confirm` means.
const mcpInstructions = `Every tool is one verb of the kilasflow command line: its arguments are that verb's own ` +
	`flags and positional arguments, and a call runs the same command the CLI would. A result is the CLI's own ` +
	`JSON envelope, {"ok":true,"data":…} or {"ok":false,"error":{"code":…,"message":…}}, with isError set when the ` +
	`command failed. The endpoint and credential are the environment this server was started with, not tool ` +
	`arguments. A tool whose description says Guarded publishes, destroys or stores something a user has to decide: ` +
	`pass confirm: true only on an explicit instruction from the user. Read the skill a tool's description names ` +
	`before using it.`

// mcpVerbs is the adapter's own verb.
func mcpVerbs() []Verb {
	return []Verb{
		{
			Path:    mcpServeVerb,
			Summary: "serve the Model Context Protocol on stdin and stdout, one tool per verb",
			Run:     runMCPServe,
		},
	}
}

// runMCPServe answers MCP requests on the process's own stdin and stdout.
//
// The protocol owns stdout from here: the frames are the output, so the CLI
// writes no envelope over them and reports a failure on stderr instead, which is
// what ctx.Streamed asks for.
func runMCPServe(ctx *Context, args []string) error {
	if err := refusePositional(args, mcpServeVerb); err != nil {
		return err
	}

	handler, err := newMCPHandler(ctx)
	if err != nil {
		return &ExitError{Code: ExitFailure, ErrCode: "mcp_catalogue", Message: mcpServeVerb + ": " + err.Error()}
	}

	ctx.Streamed = true

	if err := mcp.NewServer(handler.info(), handler).Serve(stdin(ctx.Env), stdout(ctx.Env)); err != nil {
		return &ExitError{Code: ExitFailure, ErrCode: "mcp_transport", Message: mcpServeVerb + ": " + err.Error()}
	}

	return nil
}

// stdin is the reader the protocol reads, with the same discard fallback stdout
// has, so a caller that only wants the exit code supplies neither.
func stdin(env Env) io.Reader {
	if env.Stdin == nil {
		return strings.NewReader("")
	}

	return env.Stdin
}

// mcpHandler answers the protocol with the command tree.
type mcpHandler struct {
	// catalogue is the command tree as tools, generated once: a tools/list is
	// the same list every time, and a call resolves through the same index.
	catalogue *mcpCatalogue
	// forward is what every tool call inherits from this server's own command
	// line: the JSON output mode, so a result is always an envelope, and the
	// deadline the operator gave this server.
	forward []string
	// getenv resolves the configuration chain for a tool call. It answers the
	// two variables the server itself resolved, so the operator configures an
	// endpoint and a credential once, at launch, and a tool call cannot change
	// either — nor put a token in its arguments, where a client would keep it.
	getenv func(string) string
	// log is where the adapter's own diagnostics go: never stdout, which is
	// the protocol's. It is nil unless the operator asked for --verbose.
	log io.Writer
	// version is the binary version a tool call reports, so `version` answers
	// with the same number the CLI would.
	version string
}

// newMCPHandler generates the tool list and the dispatch index for one run of
// `mcp serve`.
func newMCPHandler(ctx *Context) (*mcpHandler, error) {
	catalogue, err := buildMCPCatalogue(registry())
	if err != nil {
		return nil, err
	}

	handler := &mcpHandler{
		catalogue: catalogue,
		forward:   []string{"--json", "--timeout", ctx.Flags.Timeout.String()},
		getenv:    serverGetenv(ctx),
		version:   ctx.Env.Version,
	}
	if ctx.Flags.Verbose {
		handler.log = stderr(ctx.Env)
	}

	return handler, nil
}

// info is what the server reports about itself in the initialize answer.
func (h *mcpHandler) info() mcp.Info {
	return mcp.Info{
		Name:         "kilasflow",
		Title:        "KilasFlow",
		Version:      h.version,
		Instructions: mcpInstructions,
	}
}

// Tools is the tool list tools/list publishes.
func (h *mcpHandler) Tools() []mcp.Tool { return h.catalogue.descriptors }

// serverGetenv pins every tool call to the configuration this server resolved
// at launch.
//
// The chain a verb walks is --url/--token, then KILASFLOW_URL/KILASFLOW_TOKEN,
// then the configuration file; answering those two variables with the values
// this process already resolved is what makes `kilasflow mcp serve --url …` and
// a KILASFLOW_URL in the environment mean the same thing to a tool call as they
// do to the command that started the server. A token travels in a closure
// rather than in each call's argv, which is where the CLI's own documentation
// asks it not to be.
func serverGetenv(ctx *Context) func(string) string {
	base, token := "", ""
	if ctx.Client != nil {
		base, token = ctx.Client.BaseURL, ctx.Client.Token
	}

	return func(name string) string {
		switch name {
		case envURLVar:
			return base
		case envTokenVar:
			return token
		default:
			return getenv(ctx.Env, name)
		}
	}
}

// Call runs one tool: the arguments become the arguments of the verb the tool
// names, and the invocation goes through run(), the same entry point the
// process's own command line uses.
func (h *mcpHandler) Call(name string, arguments map[string]any) (mcp.Result, error) {
	tool, known := h.catalogue.byName[name]
	if !known {
		return mcp.Result{}, mcp.ErrUnknownTool
	}

	argv, err := tool.argv(arguments, h.forward)
	if err != nil {
		return mcp.Result{}, err
	}

	var out, errOut bytes.Buffer

	env := Env{
		Args: argv,
		// A verb that reads standard input would consume the protocol's: the
		// reader refuses instead of swallowing the next request.
		Stdin:   mcpStdin{},
		Stdout:  &out,
		Stderr:  &errOut,
		Getenv:  h.getenv,
		TTY:     false,
		Version: h.version,
	}

	code, _ := run(registry(), env)

	if h.log != nil {
		fmt.Fprintf(h.log, "kilasflow mcp: tools/call %s exit %d\n", name, code)
		if errOut.Len() > 0 {
			_, _ = h.log.Write(errOut.Bytes())
		}
	}

	// A verb writes its envelope to stdout, and a verb that streams writes its
	// payload there instead: both are what the caller should read. A verb that
	// failed before it wrote anything (a usage error is written to stderr in
	// the non-JSON modes) is reported from stderr rather than as an empty
	// result.
	text := out.String()
	if text == "" {
		text = errOut.String()
	}

	return mcp.Text(text, code != ExitOK), nil
}

// mcpStdin refuses a verb that tries to read the process's standard input.
//
// On `mcp serve` that stream is the protocol's: a verb reading it would swallow
// the next request and answer with nothing. The failure names the way out, so an
// agent that asked for `--file -` learns to pass a path.
type mcpStdin struct{}

// Read implements io.Reader by refusing.
func (mcpStdin) Read([]byte) (int, error) {
	return 0, errors.New("standard input is the MCP transport here: pass a file path instead of -")
}

// mcpCatalogue is the command tree as tools: the descriptors tools/list
// publishes and the index a tools/call resolves through.
type mcpCatalogue struct {
	descriptors []mcp.Tool
	byName      map[string]*mcpTool
}

// mcpTool is one verb as a tool: what the protocol publishes, and what turning
// a call back into argv needs.
type mcpTool struct {
	name string
	verb Verb
	// args are the verb's positional arguments, in the order the verb takes
	// them.
	args []mcpArg
	// flags are the verb's own flags, in name order.
	flags []mcpFlag
	// skills are the bundle's skills that teach this verb, in name order.
	skills []string
	// schema is the input schema tools/list publishes.
	schema map[string]any
}

// mcpArg is one positional argument as a schema property.
type mcpArg struct {
	property string
	name     string
	optional bool
}

// mcpFlag is one flag as a schema property.
type mcpFlag struct {
	property string
	name     string
	usage    string
	defValue string
	kind     mcpFlagKind
}

// mcpFlagKind is how a flag's value maps onto a JSON Schema type.
type mcpFlagKind int

const (
	mcpFlagString mcpFlagKind = iota
	mcpFlagBool
	mcpFlagInteger
	mcpFlagNumber
	mcpFlagDuration
	// mcpFlagList is a repeatable flag this adapter cannot type — a flag.Value
	// that is not a flag.Getter, which today is the escape hatch's name=value
	// flags and `exec list --status`. It is published as an array of strings,
	// one command-line occurrence per element, which is exactly what a
	// repeatable flag takes.
	mcpFlagList
)

// buildMCPCatalogue renders the command tree as tools.
//
// It refuses rather than guesses: a verb whose flag this adapter cannot describe
// as a JSON Schema property, or whose argument has no name a schema can use,
// ends the generation with the verb and the flag named. A tool that quietly
// dropped a flag would be a capability the CLI has and the protocol cannot
// reach, which is the drift this adapter exists to avoid.
func buildMCPCatalogue(verbs []Verb) (*mcpCatalogue, error) {
	globals := globalFlagNames()
	index, err := mcpSkillIndex()
	if err != nil {
		return nil, err
	}
	catalogue := &mcpCatalogue{descriptors: make([]mcp.Tool, 0, len(verbs)), byName: make(map[string]*mcpTool, len(verbs))}

	for _, verb := range verbs {
		if mcpServerModes[verb.Path] {
			continue
		}

		tool, err := mcpToolFor(verb, globals, index[verb.Path])
		if err != nil {
			return nil, err
		}

		catalogue.descriptors = append(catalogue.descriptors, tool.descriptor())
		catalogue.byName[tool.name] = tool
	}

	return catalogue, nil
}

// mcpToolFor renders one verb as a tool.
func mcpToolFor(verb Verb, globals map[string]bool, skillNames []string) (*mcpTool, error) {
	tool := &mcpTool{name: mcpToolName(verb.Path), verb: verb, skills: skillNames}

	properties := make(map[string]any, len(verb.Args)+8)
	required := make([]string, 0, len(verb.Args))

	for _, declared := range verb.Args {
		property := mcpProperty(declared.Name)
		if property == "" {
			return nil, fmt.Errorf("verb %q has an argument named %q, which no schema property can carry", verb.Path, declared.Name)
		}

		tool.args = append(tool.args, mcpArg{property: property, name: declared.Name, optional: declared.Optional})
		properties[property] = map[string]any{
			"type":        "string",
			"description": "the verb's " + declared.Name + ", as `kilasflow " + verb.Path + "` takes it",
		}
		if !declared.Optional {
			required = append(required, property)
		}
	}

	flags, err := mcpFlagsFor(verb, globals)
	if err != nil {
		return nil, err
	}
	tool.flags = flags

	for _, flag := range flags {
		properties[flag.property] = flag.schema()
	}

	if verb.Guarded {
		// Not required: the CLI's own guard is what refuses a call without
		// consent, and it has to be reachable for that refusal to be the one
		// an agent sees.
		properties[mcpConfirmProperty] = map[string]any{
			"type": "boolean",
			"description": "confirms what the verb does — " + verb.Refusal + ". The same consent `--yes` carries: " +
				"pass it only on an explicit instruction from the user.",
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	tool.schema = schema

	return tool, nil
}

// mcpConfirmProperty is the property a guarded tool carries.
const mcpConfirmProperty = "confirm"

// mcpFlagsFor reads a verb's own flags back out of the FlagSet its Flags
// function registers.
//
// Registering them is the only way to see them: a flag's type, its default and
// its usage sentence live in the FlagSet, and asking it is what keeps a flag
// added to a verb from needing a second description here. The global flags are
// dropped — they are the server's own configuration — and the verb's own are
// returned in name order, so the schema is stable between runs.
func mcpFlagsFor(verb Verb, globals map[string]bool) ([]mcpFlag, error) {
	set := flag.NewFlagSet(verb.Path, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	registerGlobalFlags(set)
	if verb.Flags != nil {
		verb.Flags(set)
	}

	flags := make([]mcpFlag, 0, 8)
	var err error

	set.VisitAll(func(registered *flag.Flag) {
		if err != nil || globals[registered.Name] {
			return
		}

		property := mcpProperty(registered.Name)
		if property == "" {
			err = fmt.Errorf("verb %q has a flag named %q, which no schema property can carry", verb.Path, registered.Name)

			return
		}

		kind, known := mcpKindOf(registered.Value)
		if !known {
			err = fmt.Errorf("verb %q has a flag --%s this adapter cannot describe as a JSON Schema property", verb.Path, registered.Name)

			return
		}

		flags = append(flags, mcpFlag{
			property: property,
			name:     registered.Name,
			usage:    registered.Usage,
			defValue: registered.DefValue,
			kind:     kind,
		})
	})
	if err != nil {
		return nil, err
	}

	return flags, nil
}

// globalFlagNames returns the names registerGlobalFlags installs, asked of the
// function itself so the set cannot drift from it.
func globalFlagNames() map[string]bool {
	set := flag.NewFlagSet("globals", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	registerGlobalFlags(set)

	names := make(map[string]bool, 8)
	set.VisitAll(func(registered *flag.Flag) { names[registered.Name] = true })

	return names
}

// mcpKindOf classifies a flag's value by what it holds, which is the only
// description the flag package gives.
//
// A flag.Value that is not a flag.Getter holds something this adapter cannot
// ask about, and the only thing it can say about itself is how it is written on
// a command line: it becomes a list of strings.
func mcpKindOf(value flag.Value) (mcpFlagKind, bool) {
	getter, ok := value.(flag.Getter)
	if !ok {
		return mcpFlagList, true
	}

	switch getter.Get().(type) {
	case bool:
		return mcpFlagBool, true
	case string:
		return mcpFlagString, true
	case int, int64, uint, uint64:
		return mcpFlagInteger, true
	case float64:
		return mcpFlagNumber, true
	case time.Duration:
		return mcpFlagDuration, true
	default:
		return 0, false
	}
}

// schema is the flag as a JSON Schema property.
func (f mcpFlag) schema() map[string]any {
	property := map[string]any{"description": f.usage}

	switch f.kind {
	case mcpFlagBool:
		property["type"] = "boolean"
	case mcpFlagInteger:
		property["type"] = "integer"
	case mcpFlagNumber:
		property["type"] = "number"
	case mcpFlagDuration:
		// The flag takes what time.ParseDuration takes, so the schema says
		// string rather than inventing a unit the CLI does not accept.
		property["type"] = "string"
		property["description"] = f.usage + " (a Go duration, such as 30s or 5m)"
	case mcpFlagList:
		property["type"] = "array"
		property["items"] = map[string]any{"type": "string"}
	case mcpFlagString:
		property["type"] = "string"
	}

	if value, ok := mcpDefault(f); ok {
		property["default"] = value
	}

	return property
}

// mcpDefault is a flag's default as the schema's own type, when saying it adds
// anything.
func mcpDefault(f mcpFlag) (any, bool) {
	if f.defValue == "" {
		return nil, false
	}

	switch f.kind {
	case mcpFlagBool:
		value, err := strconv.ParseBool(f.defValue)

		return value, err == nil
	case mcpFlagInteger:
		value, err := strconv.Atoi(f.defValue)

		return value, err == nil
	case mcpFlagNumber:
		value, err := strconv.ParseFloat(f.defValue, 64)

		return value, err == nil
	case mcpFlagDuration, mcpFlagString:
		return f.defValue, true
	default:
		return nil, false
	}
}

// descriptor is the tool as tools/list publishes it.
func (t *mcpTool) descriptor() mcp.Tool {
	description := []string{sentence(t.verb.Summary)}
	if t.verb.Guarded {
		description = append(description, "Guarded: "+sentence(t.verb.Refusal))
	}
	if len(t.skills) == 1 {
		description = append(description, "KilasFlow skill: "+t.skills[0]+".")
	} else if len(t.skills) > 1 {
		description = append(description, "KilasFlow skills: "+strings.Join(t.skills, ", ")+".")
	}

	return mcp.Tool{
		Name:        t.name,
		Description: strings.Join(description, " "),
		InputSchema: t.schema,
	}
}

// sentence ends a summary or a refusal the way a description reads: the tree's
// own sentences carry no full stop, and a description that ran two of them
// together would be one sentence to whoever reads it.
func sentence(text string) string {
	if text == "" {
		return ""
	}
	if strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") || strings.HasSuffix(text, "?") {
		return text
	}

	return text + "."
}

// argv turns one tool call into the arguments the CLI would have been given.
//
// Every property is consumed exactly once and an unknown one is refused: the
// schema says additionalProperties is false, and a server that silently ignored
// an argument would answer a question nobody asked. A missing required argument
// is refused here rather than sent as an empty string, because the verb would
// read the empty string as a real identifier.
func (t *mcpTool) argv(arguments map[string]any, forward []string) ([]string, error) {
	known := make(map[string]bool, len(t.args)+len(t.flags)+1)
	argv := strings.Fields(t.verb.Path)

	for _, argument := range t.args {
		known[argument.property] = true

		value, present := arguments[argument.property]
		if !present {
			if argument.optional {
				continue
			}

			return nil, mcp.BadParamsf("%s takes its %s: pass %s", t.verb.Path, argument.name, argument.property)
		}

		text, ok := value.(string)
		if !ok {
			return nil, mcp.BadParamsf("%s must be a string", argument.property)
		}

		argv = append(argv, text)
	}

	for _, flag := range t.flags {
		known[flag.property] = true

		value, present := arguments[flag.property]
		if !present {
			continue
		}

		rendered, err := flag.argv(value)
		if err != nil {
			return nil, err
		}

		argv = append(argv, rendered...)
	}

	// confirm is accepted on every tool, the way --yes is: an unguarded verb
	// ignores it, and refusing it here would make this adapter stricter than
	// the command line it adapts.
	known[mcpConfirmProperty] = true

	if value, present := arguments[mcpConfirmProperty]; present {
		confirmed, ok := value.(bool)
		if !ok {
			return nil, mcp.BadParamsf("%s must be a boolean", mcpConfirmProperty)
		}
		if confirmed {
			argv = append(argv, "--yes")
		}
	}

	for property := range arguments {
		if !known[property] {
			return nil, mcp.BadParamsf("%s has no argument %q", t.verb.Path, property)
		}
	}

	return append(argv, forward...), nil
}

// argv is one flag and the value the caller gave it, spelled the way the
// command line spells them.
//
// A flag the caller set is always passed, even when it holds the flag's own
// default: the caller's word is what the command line should carry, and a flag
// whose default is not the zero value would otherwise be unsettable.
func (f mcpFlag) argv(value any) ([]string, error) {
	switch f.kind {
	case mcpFlagBool:
		held, ok := value.(bool)
		if !ok {
			return nil, mcp.BadParamsf("--%s takes true or false", f.name)
		}

		// Go's flag package never takes the next token as a boolean's value —
		// "--wait true" leaves "true" as a stray positional and the flag at its
		// default — so a boolean must be spelled as one "--name=value" token.
		return []string{"--" + f.name + "=" + strconv.FormatBool(held)}, nil
	case mcpFlagInteger:
		held, ok := mcpWholeNumber(value)
		if !ok {
			return nil, mcp.BadParamsf("--%s takes a whole number", f.name)
		}

		return []string{"--" + f.name, strconv.FormatInt(held, 10)}, nil
	case mcpFlagNumber:
		held, ok := value.(float64)
		if !ok {
			return nil, mcp.BadParamsf("--%s takes a number", f.name)
		}

		return []string{"--" + f.name, strconv.FormatFloat(held, 'g', -1, 64)}, nil
	case mcpFlagList:
		held, ok := value.([]any)
		if !ok {
			return nil, mcp.BadParamsf("--%s takes a list of strings", f.name)
		}

		argv := make([]string, 0, len(held)*2)
		for _, element := range held {
			text, ok := element.(string)
			if !ok {
				return nil, mcp.BadParamsf("--%s takes a list of strings", f.name)
			}

			argv = append(argv, "--"+f.name, text)
		}

		return argv, nil
	default:
		held, ok := value.(string)
		if !ok {
			return nil, mcp.BadParamsf("--%s takes a string", f.name)
		}

		return []string{"--" + f.name, held}, nil
	}
}

// mcpWholeNumber accepts a JSON number that is whole, which is what an integer
// property is: encoding/json decodes every number as a float64, so an integer
// property has to be read back out of one.
func mcpWholeNumber(value any) (int64, bool) {
	switch held := value.(type) {
	case float64:
		whole := int64(held)
		if float64(whole) != held {
			return 0, false
		}

		return whole, true
	case int64:
		return held, true
	case int:
		return int64(held), true
	case uint64:
		if held > math.MaxInt64 {
			return 0, false
		}

		return int64(held), true
	case uint:
		if uint64(held) > math.MaxInt64 {
			return 0, false
		}

		return int64(held), true
	default:
		return 0, false
	}
}

// mcpToolName is a verb's path as a tool name: the words joined with an
// underscore.
//
// The specification's tool-name rule allows letters, digits, underscore, hyphen
// and dot, and no spaces; a verb's own words already carry the hyphens
// (`get-version`), so the separator is the underscore and nothing needs
// escaping. Names are unique because verb paths are, which the registry's own
// test asserts.
func mcpToolName(path string) string { return strings.ReplaceAll(path, " ", "_") }

// mcpProperty is a name as a JSON Schema property: lower case, with everything
// that is not a letter or a digit collapsed into one underscore.
func mcpProperty(name string) string {
	var property []rune

	pending := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if pending && len(property) > 0 {
				property = append(property, '_')
			}
			pending = false
			property = append(property, r)
		default:
			pending = true
		}
	}

	return string(property)
}

// mcpSkillIndex maps a verb path to the skills whose declarations name it.
//
// The bundle is the single source of truth for what an agent should read before
// driving a verb, so the tool description points at it instead of repeating it:
// a skill renamed or a declaration changed shows up in the tool list without
// anything here being edited. A bundle that cannot be read is an error rather
// than an empty index — the bundle is embedded in this binary and gated by
// tests, so a failure here is a broken build, not a caller's mistake.
func mcpSkillIndex() (map[string][]string, error) {
	index := map[string][]string{}

	bundle, err := skills.LoadBundle()
	if err != nil {
		return nil, fmt.Errorf("read the skills bundle: %w", err)
	}

	for _, skill := range bundle {
		for _, command := range skill.Commands {
			path := strings.TrimPrefix(command, "kilasflow ")
			if path == command || path == "" {
				continue
			}

			index[path] = append(index[path], skill.Name)
		}
	}

	return index, nil
}
