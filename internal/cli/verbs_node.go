package cli

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// nodeVerbs are the read-only views of the server's node catalogue.
//
// Two of them drive one operation: `node list` prints the catalogue and
// `node describe` picks one entry out of it. That is the reverse of design
// §4.2's one-verb-per-operation rule, and it is deliberate — the catalogue
// endpoint is the only source of a node's parameter shape, so a second
// operation would have to be invented before a second verb could be written,
// and `node describe` exists precisely because reading a 90-entry catalogue to
// answer "what does httpRequest take?" is the wrong shape for an agent's
// context. The rule is kept where it can be: every verb still names the
// operation it calls, and the tree test asserts the two share theirs.
//
// Deliberately absent: `node icons`, `node grammar` and `node schema-lookup`.
// The icon and grammar operations are browser needs, and loading a resource
// mapper's columns is a sibling of `node options` that no caller has needed
// yet; `kilasflow api get-node-icon`, `api get-expression-grammar` and
// `api load-node-property-schema` reach all three today.
func nodeVerbs() []Verb {
	return []Verb{
		{
			Path:      "node list",
			Operation: "list-node-types",
			Summary:   "list the node catalogue the server will run",
			Run:       runNodeList,
			Human:     humanResourceList("type", "version", "displayName", "category"),
		},
		{
			Path:      "node describe",
			Operation: "list-node-types",
			Summary:   "print one node type's definition from the catalogue",
			Args:      []Arg{arg("node type")},
			Run:       runNodeDescribe,
			Human:     humanResourceResource,
		},
		{
			Path:      "node options",
			Operation: "load-node-property-options",
			Summary:   "load a property's selectable values from the service that owns them (`--property`, `--version`, `--mode`, `--credential`, `--param`, `--parameters`)",
			Args:      []Arg{arg("node type")},
			Flags:     registerNodeOptionsFlags,
			Run:       runNodeOptions,
			Human:     humanResourceResource,
		},
	}
}

// nodeOptionsFlags name the property to resolve and the node it belongs to.
type nodeOptionsFlags struct {
	property     string
	version      string
	mode         string
	credentialID string
	// params are the repeatable --param name=value pairs; parameters is the
	// --parameters object. Both end up in the request's parameters member,
	// which is what the loader's dependsOn keys are read from.
	params     *pairFlags
	parameters string
}

// registerNodeOptionsFlags attaches the load-options parameters.
func registerNodeOptionsFlags(fs *flag.FlagSet) any {
	flags := &nodeOptionsFlags{params: newPairFlags()}
	fs.StringVar(&flags.property, "property", "", "the property whose options to load (required)")
	fs.StringVar(&flags.version, "version", "", "node type version; defaults to the registered one")
	fs.StringVar(&flags.mode, "mode", "", "for a resource locator, the mode whose list to load")
	fs.StringVar(&flags.credentialID, "credential", "", "credential id to resolve against, from your own tenant")
	fs.Var(flags.params, "param", "set one node parameter the property's options depend on, as name=value; the value is sent as a string (repeatable)")
	fs.StringVar(&flags.parameters, "parameters", "", "the node's parameters as a JSON object, @<file>, or - to read stdin; a --param of the same name overrides it")

	return flags
}

// requestParameters resolves --parameters and --param into the parameters
// member of the request, or nil when the caller named none.
//
// The values stay raw JSON so a number or a resource locator in --parameters
// reaches the server exactly as it was written; a --param value is always a
// string, which is what almost every dependency is (a base URL, a schema name).
func (f *nodeOptionsFlags) requestParameters(ctx *Context) (map[string]json.RawMessage, error) {
	parameters := map[string]json.RawMessage{}

	if source := strings.TrimSpace(f.parameters); source != "" {
		var raw []byte
		switch {
		case source == "-" || strings.HasPrefix(source, "@"):
			document, err := readDocument(strings.TrimPrefix(source, "@"), ctx.Env.Stdin)
			if err != nil {
				return nil, err
			}
			raw = document
		default:
			if !json.Valid([]byte(source)) {
				return nil, usageError("--parameters is not valid JSON: %s", source)
			}
			raw = []byte(source)
		}

		// A parameters member is a map by definition; null, an array or a
		// scalar is a mistake the server would otherwise answer with a decode
		// error that does not name the flag.
		if trimmed := strings.TrimSpace(string(raw)); !strings.HasPrefix(trimmed, "{") {
			return nil, usageError("--parameters must be a JSON object of parameter name to value")
		}
		if err := json.Unmarshal(raw, &parameters); err != nil {
			return nil, usageError("--parameters is not a JSON object: %v", err)
		}
	}

	for _, name := range f.params.keys {
		values := f.params.values[name]
		if len(values) > 1 {
			return nil, usageError("--param %s was given %d times: pass each parameter once", name, len(values))
		}
		encoded, err := json.Marshal(values[0])
		if err != nil {
			return nil, usageError("could not encode --param %s: %v", name, err)
		}
		parameters[name] = encoded
	}

	if len(parameters) == 0 {
		return nil, nil
	}

	return parameters, nil
}

// runNodeList reads the catalogue. The operation takes no parameters, so the
// verb takes none either.
func runNodeList(ctx *Context, args []string) error {
	if err := refusePositional(args, "node list"); err != nil {
		return err
	}

	list, err := ctx.listPage("/node-types", nil)
	if err != nil {
		return err
	}

	// The type is what identifies a definition (there is no id), so it is what
	// --quiet prints and what `node describe` takes.
	return listPageResultField(ctx, list, "type")
}

// runNodeDescribe prints one definition out of the catalogue.
//
// A type registered at several versions resolves the way the server resolves a
// document that names no version — the highest registered one — so the answer
// describes the node the server would actually run.
func runNodeDescribe(ctx *Context, args []string) error {
	nodeType, err := requireOneID(args, "node type")
	if err != nil {
		return err
	}

	list, err := ctx.listPage("/node-types", nil)
	if err != nil {
		return err
	}

	var (
		best    json.RawMessage
		bestVer float64
		found   bool
	)

	for _, item := range list.Items {
		if fieldValue(item, "type") != nodeType {
			continue
		}
		version := nodeVersion(item)
		if found && version <= bestVer {
			continue
		}
		best, bestVer, found = item, version, true
	}

	if !found {
		return &ExitError{
			Code:    ExitNotFound,
			ErrCode: "not_found",
			Message: "no node type " + strconv.Quote(nodeType) + " in the catalogue" + closeTypeHint(list.Items, nodeType),
		}
	}

	ctx.Data = best
	ctx.Primary = nodeType

	return nil
}

// runNodeOptions resolves a property's selectable values.
//
// A property whose list depends on other parameters (a chat model's models need
// the base URL they are listed from) cannot load without them, so the node as
// far as it is configured goes in with --param and --parameters, and the
// credential the loader signs with with --credential. What the loader reads is
// the request's `parameters`; `workflowId`, which bounds an embed session's
// lookup, is the one member left to `kilasflow api load-node-property-options`.
func runNodeOptions(ctx *Context, args []string) error {
	nodeType, err := requireOneID(args, "node type")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*nodeOptionsFlags)
	if !ok {
		return usageError("the node options verb was registered without its flags")
	}
	if strings.TrimSpace(flags.property) == "" {
		return usageError("no property: pass --property <key>")
	}

	parameters, err := flags.requestParameters(ctx)
	if err != nil {
		return err
	}

	body, err := json.Marshal(struct {
		Property     string                     `json:"property"`
		Version      string                     `json:"version,omitempty"`
		Mode         string                     `json:"mode,omitempty"`
		Parameters   map[string]json.RawMessage `json:"parameters,omitempty"`
		CredentialID string                     `json:"credentialId,omitempty"`
	}{
		Property:     flags.property,
		Version:      strings.TrimSpace(flags.version),
		Mode:         strings.TrimSpace(flags.mode),
		Parameters:   parameters,
		CredentialID: strings.TrimSpace(flags.credentialID),
	})
	if err != nil {
		return &ExitError{Code: ExitFailure, ErrCode: "error", Message: "could not encode the request body: " + err.Error()}
	}

	path := "/node-types/" + url.PathEscape(nodeType) + "/load-options"

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath(path), nil, nil, body)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)

	return nil
}

// nodeVersion reads a definition's version as the number the wire format
// carries. A definition without one compares as zero, which is lower than any
// real version, so it can never win a tie against one that has a version.
func nodeVersion(item json.RawMessage) float64 {
	var definition struct {
		Version float64 `json:"version"`
	}
	if err := json.Unmarshal(item, &definition); err != nil {
		return 0
	}

	return definition.Version
}

// closeTypeHint names the catalogue entries a mistyped type probably meant.
//
// A node type is a name a caller has to remember, and the catalogue answers a
// wrong one with nothing at all, so the refusal carries the nearest names rather
// than a bare "not found" the caller cannot act on. Nothing is suggested when
// nothing is close: a wrong guess is worse than no guess.
func closeTypeHint(items []json.RawMessage, want string) string {
	candidates := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		nodeType := fieldValue(item, "type")
		if nodeType == "" || seen[nodeType] {
			continue
		}
		seen[nodeType] = true
		candidates = append(candidates, nodeType)
	}

	matches := closeTypes(candidates, want)
	if len(matches) == 0 {
		return ""
	}

	return "; did you mean " + strings.Join(matches, ", ") + "?"
}

// closeTypes returns up to five catalogue types within two edits of want.
//
// Two covers the typos a name actually suffers — a transposition, a dropped
// letter, a doubled one — without suggesting neighbours that share only a
// namespace, which is what a bound scaled to the name's length would do.
func closeTypes(candidates []string, want string) []string {
	type scored struct {
		name     string
		distance int
	}

	const limit = 2

	matches := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		distance := editDistance(strings.ToLower(candidate), strings.ToLower(want))
		if distance > limit {
			continue
		}
		matches = append(matches, scored{name: candidate, distance: distance})
	}

	sort.Slice(matches, func(left, right int) bool {
		if matches[left].distance != matches[right].distance {
			return matches[left].distance < matches[right].distance
		}

		return matches[left].name < matches[right].name
	})

	if len(matches) > 5 {
		matches = matches[:5]
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match.name)
	}

	return names
}

// editDistance is the Levenshtein distance between two type names. Node types
// are short and ASCII, so the quadratic table costs nothing next to reading the
// catalogue. It is the same shape as the configuration loader's suggestion
// helper, which is the one other place the repository guesses at a typo.
func editDistance(left, right string) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, min(current[j-1]+1, previous[j-1]+cost))
		}
		previous, current = current, previous
	}

	return previous[len(right)]
}
