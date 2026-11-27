package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// --- Rename Keys ---------------------------------------------------------------
//
// n8n's Rename Keys and this server's share one parameter shape — a fixed
// collection `keys` whose `key` rows hold `currentKey` and `newKey` — so the
// translators copy rows rather than reshape them. The one thing n8n's node
// does that this one does not is its regular-expression replacement list, and
// an import that dropped it would run a workflow the author never wrote, so it
// is named as blocking.

// isBlankText reports whether a parameter is an empty or whitespace-only
// string. A value that is not a string — an expression marker — is not blank:
// it names something, even if only at run time.
func isBlankText(value any) bool {
	text, isText := value.(string)
	return value == nil || (isText && strings.TrimSpace(text) == "")
}

func renameKeysToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	rows := make([]any, 0)
	if wrapper, ok := node.Parameters["keys"].(map[string]any); ok {
		entries, _ := wrapper["key"].([]any)
		for _, entry := range entries {
			row, isRow := entry.(map[string]any)
			if !isRow {
				continue
			}
			// The editor's untouched node carries one empty row; it renames
			// nothing, and carrying it would only add a row that fails
			// validation for the author to delete.
			if isBlankText(row["currentKey"]) && isBlankText(row["newKey"]) {
				continue
			}
			rows = append(rows, map[string]any{
				"currentKey": fromN8NValue(row["currentKey"]),
				"newKey":     fromN8NValue(row["newKey"]),
			})
		}
	}
	if options, ok := node.Parameters["additionalOptions"].(map[string]any); ok && len(options) > 0 {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "additionalOptions",
			Reason: "this Rename Keys node also renames keys by regular expression, which KilasFlow's Rename Keys does not do: " +
				"the exact renames were imported and the expression replacements were not, so the keys they matched keep their names",
		})
	}
	return map[string]any{"keys": map[string]any{"key": rows}}, issues
}

func renameKeysToN8N(node workflow.Node) (map[string]any, []Lossy) {
	rows := make([]any, 0)
	if wrapper, ok := node.Parameters["keys"].(map[string]any); ok {
		entries, _ := wrapper["key"].([]any)
		for _, entry := range entries {
			row, isRow := entry.(map[string]any)
			if !isRow {
				continue
			}
			rows = append(rows, map[string]any{
				"currentKey": toN8NValue(row["currentKey"]),
				"newKey":     toN8NValue(row["newKey"]),
			})
		}
	}
	return map[string]any{"keys": map[string]any{"key": rows}}, nil
}

// --- Compare Datasets ----------------------------------------------------------
//
// The n8n node's four outputs are numbered In A only, Same, Different, In B
// only, and the native node declares its ports in that order, so the importer's
// positional resolution through the catalogue wires each branch to the output
// of the same name. The options it does not implement — fuzzy comparison, all
// matches rather than the first, and a mix of versions per field — change which
// output an item lands on or what it holds, so a node that sets one arrives as
// the placeholder with the reason, never as a node that quietly ignores it.

// refuseUnsupportedCompareDatasetsOption names the option a Compare Datasets
// node sets that the native node does not implement.
func refuseUnsupportedCompareDatasetsOption(node Node) string {
	const tail = "; it was imported as an unsupported placeholder so the workflow does not run with a different comparison than the one it was written for"
	if stringParameter(node.Parameters, "resolve") == "mix" {
		return "this Compare Datasets node resolves differences with a mix of versions per field, which KilasFlow does not do " +
			"(it keeps the whole item from Input A or Input B, or reports both versions)" + tail
	}
	options, _ := node.Parameters["options"].(map[string]any)
	if fuzzy, _ := options["fuzzyCompare"].(bool); fuzzy {
		return "this Compare Datasets node compares fuzzily (so 1 and \"1\" are equal), which KilasFlow does not do: its comparison is exact" + tail
	}
	if matches := textOf(options["multipleMatches"]); matches != "" && matches != "first" {
		return "this Compare Datasets node keeps every match when an item has several, which KilasFlow does not do: " +
			"it pairs items one to one, in order" + tail
	}
	return ""
}

func compareDatasetsToKilas(node Node) (map[string]any, []Unsupported) {
	converted := map[string]any{}
	rows := make([]any, 0)
	if wrapper, ok := node.Parameters["mergeByFields"].(map[string]any); ok {
		entries, _ := wrapper["values"].([]any)
		for _, entry := range entries {
			row, isRow := entry.(map[string]any)
			if !isRow {
				continue
			}
			if isBlankText(row["field1"]) && isBlankText(row["field2"]) {
				continue
			}
			rows = append(rows, map[string]any{
				"field1": fromN8NValue(row["field1"]),
				"field2": fromN8NValue(row["field2"]),
			})
		}
	}
	converted["mergeByFields"] = map[string]any{"values": rows}
	if resolve := stringParameter(node.Parameters, "resolve"); resolve != "" {
		converted["resolve"] = resolve
	}

	options := map[string]any{}
	if source, ok := node.Parameters["options"].(map[string]any); ok {
		if skip := strings.TrimSpace(stringParameter(source, "skipFields")); skip != "" {
			options["skipFields"] = skip
		}
		if disabled, _ := source["disableDotNotation"].(bool); disabled {
			options["disableDotNotation"] = true
		}
	}
	if len(options) > 0 {
		converted["options"] = options
	}
	return converted, nil
}

func compareDatasetsToN8N(node workflow.Node) (map[string]any, []Lossy) {
	rows := make([]any, 0)
	if wrapper, ok := node.Parameters["mergeByFields"].(map[string]any); ok {
		entries, _ := wrapper["values"].([]any)
		for _, entry := range entries {
			row, isRow := entry.(map[string]any)
			if !isRow {
				continue
			}
			rows = append(rows, map[string]any{
				"field1": toN8NValue(row["field1"]),
				"field2": toN8NValue(row["field2"]),
			})
		}
	}
	written := map[string]any{
		"mergeByFields": map[string]any{"values": rows},
		"resolve":       defaultString(stringParameter(node.Parameters, "resolve"), "includeBoth"),
		"options":       map[string]any{},
	}
	if source, ok := node.Parameters["options"].(map[string]any); ok {
		options := written["options"].(map[string]any)
		for _, key := range []string{"skipFields", "disableDotNotation"} {
			if value, present := source[key]; present {
				options[key] = value
			}
		}
	}
	return written, nil
}
