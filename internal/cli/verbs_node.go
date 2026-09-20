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
			Run:       runNodeDescribe,
			Human:     humanResourceResource,
		},
		{
			Path:      "node options",
			Operation: "load-node-property-options",
			Summary:   "load a property's selectable values from the service that owns them (`--property`, `--version`, `--mode`, `--credential`)",
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
}

// registerNodeOptionsFlags attaches the load-options parameters.
func registerNodeOptionsFlags(fs *flag.FlagSet) any {
	flags := &nodeOptionsFlags{}
	fs.StringVar(&flags.property, "property", "", "the property whose options to load (required)")
	fs.StringVar(&flags.version, "version", "", "node type version; defaults to the registered one")
	fs.StringVar(&flags.mode, "mode", "", "for a resource locator, the mode whose list to load")
	fs.StringVar(&flags.credentialID, "credential", "", "credential id to resolve against, from your own tenant")

	return flags
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
// The node as configured in the editor is what the loader reads, but the three
// parameters that change which list comes back are the ones the verb exposes:
// a partially configured node is a document, and a caller that has one sends it
// through `kilasflow api load-node-property-options`.
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

	body, err := json.Marshal(struct {
		Property     string `json:"property"`
		Version      string `json:"version,omitempty"`
		Mode         string `json:"mode,omitempty"`
		CredentialID string `json:"credentialId,omitempty"`
	}{
		Property:     flags.property,
		Version:      strings.TrimSpace(flags.version),
		Mode:         strings.TrimSpace(flags.mode),
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
