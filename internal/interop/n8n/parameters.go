package n8n

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/scheduler"
	"github.com/kilaslab/kilas-flow/internal/sqlbuild"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// n8nFromAIMarker is the comment n8n writes into an expression when a user
// clicks "let the model define this parameter".
//
// It is a JavaScript comment, so n8n's own evaluator ignores it — but this
// server's parser is not a JavaScript parser, and `{{ /*…*/ $fromAI(…) }}`
// failed with "expression must start with a supported root". Every parameter
// the model was meant to fill in therefore failed on every tool call, which is
// 27 of the 52 $fromAI parameters in the corpus. Stripped here, at the one
// place n8n expressions are read, so every node type that carries one is fixed
// at once.
const n8nFromAIMarker = "/*n8n-auto-generated-fromAI-override*/"

// expressionValue marks a template as an expression.
func expressionValue(template string) map[string]any {
	template = strings.ReplaceAll(template, n8nFromAIMarker, "")
	// The comment left a run of spaces behind; tidying it keeps the stored
	// template readable and keeps an expression that is *only* the comment from
	// arriving as whitespace.
	if strings.Contains(template, n8nFromAIMarker) {
		template = strings.ReplaceAll(template, n8nFromAIMarker, "")
	}
	return map[string]any{"mode": "expression", "value": template}
}

// fromN8NValue converts one n8n parameter value.
//
// A `=`-prefixed string becomes an explicit expression marker; anything else
// is a fixed value. This is exactly the ambiguity KilasFlow's explicit marker
// exists to avoid: in n8n, a fixed string that genuinely starts with `=` is
// unrepresentable.
func fromN8NValue(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	if after, found := strings.CutPrefix(text, "="); found {
		return expressionValue(after)
	}
	return text
}

// toN8NValue converts back, re-adding the `=` prefix for an expression.
func toN8NValue(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if mode, _ := object["mode"].(string); mode != "expression" {
		return value
	}
	template, _ := object["value"].(string)
	return "=" + template
}

func stringParameter(parameters map[string]any, key string) string {
	text, _ := parameters[key].(string)
	return text
}

// textOf reads any scalar as text, for parameters whose JSON shape varies.
func textOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}
func numberParameter(parameters map[string]any, key string) (float64, bool) {
	switch typed := parameters[key].(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	case string:
		value, err := strconv.ParseFloat(typed, 64)
		return value, err == nil
	default:
		return 0, false
	}
}

// --- Set --------------------------------------------------------------------

// setToKilas reads n8n's Set node in every shape it has had.
//
// n8n v3 nests assignments under `assignments.assignments` as typed entries;
// v3.0-3.2 used `fields.values` with `<type>Value` keys; v1/v2 used
// `values.<kind>`. All three are read, because an exported workflow in the
// wild may be any of them.
//
// Both are *ordered lists with a declared type per row*, and both are carried
// through as one. The importer used to collapse them into a `map[name]value`,
// which lost the order the author typed and the type of every entry — so a
// boolean returned to n8n as a string and the fields came back alphabetical.
//
// The include half is version-aware: n8n Set >= 3.3 defaults
// includeOtherFields=false (output only the set fields), so a node that
// carries no key at all still means "none". Older versions default to keeping
// the input (v1/v2 keepOnlySet=false, v3.0-3.2 include=all). The executor
// already supports include/mode/options; the importer never filled them, so
// every such node leaked every input field downstream.
func setToKilas(node Node) (map[string]any, []Unsupported) {
	entries := make([]any, 0, 4)
	issues := make([]Unsupported, 0)

	if wrapper, ok := node.Parameters["assignments"].(map[string]any); ok {
		if rows, ok := wrapper["assignments"].([]any); ok {
			for index, entry := range rows {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				identifier, _ := fields["id"].(string)
				if assignmentTypeIsNull(fields["type"]) {
					issues = append(issues, Unsupported{
						Field: fmt.Sprintf("assignments.assignments[%d].type", index),
						Reason: "this field was assigned n8n's null type, which this server has no type " +
							"for; it is carried as a string assignment holding null, which writes the " +
							"same value",
					})
				}
				entries = append(entries, assignmentEntry(node, index, identifier, name,
					assignmentTypeOf(fields["type"]), fromN8NValue(fields["value"])))
			}
		}
	}

	// Set v3.0-3.2 `fields.values`: the type lives in the *value key* —
	// `stringValue`, `numberValue` — and there is one entry per row.
	if wrapper, ok := node.Parameters["fields"].(map[string]any); ok {
		if rows, ok := wrapper["values"].([]any); ok {
			for _, entry := range rows {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				identifier, _ := fields["id"].(string)
				declared, value := fieldsValueOf(fields)
				entries = append(entries, assignmentEntry(node, len(entries), identifier, name,
					declared, fromN8NValue(value)))
			}
		}
	}

	// Set v2's pre-assignment control: a fixed collection whose type lives in
	// the *key* — `stringValue`, `numberValue` — rather than in a type field.
	if values, ok := node.Parameters["values"].(map[string]any); ok {
		for _, kind := range sortedKeys(values) {
			list, ok := values[kind].([]any)
			if !ok {
				continue
			}
			for _, entry := range list {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				identifier, _ := fields["id"].(string)
				entries = append(entries, assignmentEntry(node, len(entries), identifier, name,
					assignmentTypeOf(kind), fromN8NValue(fields["value"])))
			}
		}
	}

	converted := map[string]any{"assignments": map[string]any{"assignments": entries}}
	issues = append(issues, setIncludeToKilas(node, converted)...)
	issues = append(issues, setModeToKilas(node, converted)...)
	if len(entries) == 0 && textOf(converted["mode"]) != "raw" {
		issues = append(issues, Unsupported{
			Reason: "this Set node has no readable assignments; add at least one field before running the workflow",
		})
	}
	return converted, issues
}

// fieldsValueOf reads one v3.0-3.2 `fields.values` row: the declared type is
// the key whose name ends in `Value`, and its value is the row's value.
func fieldsValueOf(fields map[string]any) (string, any) {
	for _, key := range sortedKeys(fields) {
		if key == "name" || key == "id" {
			continue
		}
		if strings.HasSuffix(key, "Value") {
			return assignmentTypeOf(key), fields[key]
		}
	}
	if value, ok := fields["value"]; ok {
		return assignmentTypeOf(fields["type"]), value
	}
	return string(property.AssignmentString), nil
}

// setIncludeToKilas maps n8n's keep-only/include surface onto the executor's
// include/includeFields/excludeFields.
func setIncludeToKilas(node Node, converted map[string]any) []Unsupported {
	issues := make([]Unsupported, 0)
	version := node.TypeVersion

	if includeOtherFields, present := node.Parameters["includeOtherFields"].(bool); present {
		if !includeOtherFields {
			converted["include"] = "none"
			return issues
		}
		// Explicitly true: input fields survive, narrowed by include when set.
		switch include := stringParameter(node.Parameters, "include"); include {
		case "selected":
			converted["include"] = "selected"
			converted["includeFields"] = includeFieldList(node.Parameters["includeFields"])
		case "except":
			converted["include"] = "except"
			converted["excludeFields"] = includeFieldList(node.Parameters["excludeFields"])
		default:
			converted["include"] = "all"
		}
		return issues
	}

	// No includeOtherFields key. n8n >= 3.3 omits its own default (false), so
	// a modern Set with no key means "only the set fields".
	if version >= 3.3 {
		converted["include"] = "none"
		return issues
	}

	// Older shapes: v3.0-3.2 include defaults to all; v1/v2 carry keepOnlySet.
	if keepOnly, _ := node.Parameters["keepOnlySet"].(bool); keepOnly {
		converted["include"] = "none"
		return issues
	}
	switch include := stringParameter(node.Parameters, "include"); include {
	case "selected":
		converted["include"] = "selected"
		converted["includeFields"] = includeFieldList(node.Parameters["includeFields"])
	case "except":
		converted["include"] = "except"
		converted["excludeFields"] = includeFieldList(node.Parameters["excludeFields"])
	default:
		converted["include"] = "all"
	}
	return issues
}

// includeFieldList reads n8n's include/exclude field list, which is either a
// comma-separated string or a fixed collection of names.
func includeFieldList(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		names := namedList(typed, "field")
		if len(names) == 0 {
			names = namedList(typed, "fieldName")
		}
		return strings.Join(names, ",")
	default:
		return ""
	}
}

// setModeToKilas maps n8n's raw JSON mode and options onto the executor's
// mode/jsonOutput/options.
func setModeToKilas(node Node, converted map[string]any) []Unsupported {
	issues := make([]Unsupported, 0)
	if mode := stringParameter(node.Parameters, "mode"); mode == "raw" {
		converted["mode"] = "raw"
		if body, ok := node.Parameters["jsonOutput"]; ok {
			converted["jsonOutput"] = fromN8NValue(body)
		}
	} else {
		converted["mode"] = "manual"
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		carried := map[string]any{}
		for _, key := range []string{"dotNotation", "ignoreConversionErrors", "includeBinary", "stripBinary"} {
			if value, present := options[key]; present && value != nil {
				carried[key] = value
			}
		}
		if len(carried) > 0 {
			converted["options"] = carried
		}
		for key := range options {
			switch key {
			case "dotNotation", "ignoreConversionErrors", "includeBinary", "stripBinary":
			default:
				issues = append(issues, Unsupported{
					Field:  "options." + key,
					Reason: fmt.Sprintf("the Set option %q has no KilasFlow equivalent and was not carried", key),
				})
			}
		}
	}
	return issues
}

// assignmentEntry builds one row.
//
// n8n's own row id is carried when it has one, so a workflow that goes back is
// the same document rather than a new one. Only a row that never had an id gets
// a minted one.
func assignmentEntry(node Node, index int, identifier, name, declared string, value any) map[string]any {
	if strings.TrimSpace(identifier) == "" {
		base := node.ID
		if base == "" {
			base = node.Name
		}
		identifier = fmt.Sprintf("%s-%d", base, index)
	}
	return map[string]any{"id": identifier, "name": name, "type": declared, "value": value}
}

// assignmentTypeOf reads n8n's declared type in either of the two spellings.
//
// Set v3 writes the bare name — `string`, `number` — while v2 named its value
// *field* `stringValue`, `numberValue` and so on. Both mean the same thing, and
// a type outside the five KilasFlow has a control for degrades to a string
// rather than being carried into a document nothing can render.
func assignmentTypeOf(declared any) string {
	name, _ := declared.(string)
	name = strings.TrimSuffix(strings.TrimSpace(name), "Value")
	switch property.AssignmentType(name) {
	case property.AssignmentString, property.AssignmentNumber, property.AssignmentBoolean,
		property.AssignmentArray, property.AssignmentObject:
		return name
	case "null":
		// n8n's null assignment type. This server has no such type, and a
		// string row holding nil *is* null once the executor stops turning nil
		// into "" — so the row is carried as a string assignment whose value is
		// null, which is the same field value n8n writes. Named as lossy rather
		// than done quietly: the editor shows the type as `string`, and a user
		// comparing the two nodes should know why.
		return string(property.AssignmentString)
	default:
		return string(property.AssignmentString)
	}
}

// assignmentTypeIsNull reports n8n's null assignment type, which this server
// carries as a string row holding null.
func assignmentTypeIsNull(declared any) bool {
	name, _ := declared.(string)
	return strings.TrimSpace(name) == "null"
}

// setToN8N writes the rows back in the order and with the types they carry.
func setToN8N(node workflow.Node) (map[string]any, []Lossy) {
	wrapper, _ := node.Parameters["assignments"].(map[string]any)
	rows, ordered := wrapper["assignments"].([]any)
	if !ordered {
		// A document saved before assignments had order or types. Its order is
		// a map's, so it is written alphabetically — the only stable answer —
		// and every row goes back as a string, which is what it was stored as.
		names := sortedKeys(wrapper)
		entries := make([]any, 0, len(names))
		for index, name := range names {
			entries = append(entries, map[string]any{
				"id":    fmt.Sprintf("%s-%d", node.ID, index),
				"name":  name,
				"type":  string(property.AssignmentString),
				"value": toN8NValue(wrapper[name]),
			})
		}
		return map[string]any{
			"assignments": map[string]any{"assignments": entries},
			"options":     map[string]any{},
		}, nil
	}

	entries := make([]any, 0, len(rows))
	for index, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			continue
		}
		identifier, _ := fields["id"].(string)
		if identifier == "" {
			identifier = fmt.Sprintf("%s-%d", node.ID, index)
		}
		declared, _ := fields["type"].(string)
		if declared == "" {
			declared = string(property.AssignmentString)
		}
		entries = append(entries, map[string]any{
			"id":    identifier,
			"name":  fields["name"],
			"type":  declared,
			"value": toN8NValue(fields["value"]),
		})
	}
	written := map[string]any{
		"assignments": map[string]any{"assignments": entries},
		"options":     setOptionsToN8N(node.Parameters),
	}
	setIncludeToN8N(node.Parameters, written)
	setModeToN8N(node.Parameters, written)
	return written, nil
}

// setIncludeToN8N writes the include half back: includeOtherFields plus the
// include selector, so a node imported at >= 3.3 returns to the shape n8n
// reads as keep-only.
func setIncludeToN8N(parameters, written map[string]any) {
	switch textOf(parameters["include"]) {
	case "none":
		written["includeOtherFields"] = false
	case "selected":
		written["includeOtherFields"] = true
		written["include"] = "selected"
		if fields := textOf(parameters["includeFields"]); fields != "" {
			written["includeFields"] = fields
		}
	case "except":
		written["includeOtherFields"] = true
		written["include"] = "except"
		if fields := textOf(parameters["excludeFields"]); fields != "" {
			written["excludeFields"] = fields
		}
	default:
		written["includeOtherFields"] = true
	}
}

// setModeToN8N writes the mode half back: raw carries its JSON body.
func setModeToN8N(parameters, written map[string]any) {
	if textOf(parameters["mode"]) == "raw" {
		written["mode"] = "raw"
		if body, ok := parameters["jsonOutput"]; ok {
			written["jsonOutput"] = toN8NValue(body)
		}
	}
}

// setOptionsToN8N carries the options the executor honours.
func setOptionsToN8N(parameters map[string]any) map[string]any {
	options := map[string]any{}
	if stored, ok := parameters["options"].(map[string]any); ok {
		for _, key := range []string{"dotNotation", "ignoreConversionErrors", "includeBinary", "stripBinary"} {
			if value, present := stored[key]; present && value != nil {
				options[key] = value
			}
		}
	}
	return options
}

// --- IF ---------------------------------------------------------------------

// n8n operator names differ from KilasFlow's, and its v2 conditions carry a
// richer operator object. Only the four KilasFlow supports are mapped.
var ifOperators = map[string]string{
	"equals":     "equals",
	"equal":      "equals",
	"notEquals":  "notEquals",
	"notEqual":   "notEquals",
	"exists":     "exists",
	"notExists":  "notExists",
	"isNotEmpty": "exists",
	"isEmpty":    "notExists",
	"is":         "equals",
}

// leftValuePattern pulls a field path out of n8n's `={{ $json.field }}` form.
var leftValuePattern = regexp.MustCompile(`\{\{\s*\$json\.([A-Za-z0-9_.]+)\s*\}\}`)

// filterToKilas translates n8n's filter parameter into the shared shape.
//
// The shape is carried rather than reduced. KilasFlow's IF used to accept one
// condition over four operators, so the importer picked the first and reported
// the rest as lost — which meant an "A and B" workflow imported as "A" and ran
// happily on half its own logic. One evaluator with n8n's own shape makes the
// translation a rename rather than a decision.
func filterToKilas(value any) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	declared, ok := value.(map[string]any)
	if !ok {
		return nil, append(issues, Unsupported{Reason: "this node has no readable conditions; set one before running the workflow"})
	}

	entries, _ := declared["conditions"].([]any)
	if len(entries) == 0 {
		// n8n v1 grouped conditions by value type instead of listing them.
		for _, group := range []string{"string", "number", "boolean", "dateTime"} {
			list, grouped := declared[group].([]any)
			if !grouped {
				continue
			}
			for _, entry := range list {
				row, _ := entry.(map[string]any)
				if row == nil {
					continue
				}
				entries = append(entries, legacyCondition(row, group))
			}
		}
	}
	if len(entries) == 0 {
		return nil, append(issues, Unsupported{Reason: "this node has no readable conditions; set one before running the workflow"})
	}

	converted := make([]any, 0, len(entries))
	for index, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		operator, ok := row["operator"].(map[string]any)
		if !ok {
			issues = append(issues, Unsupported{
				Field:  fmt.Sprintf("conditions[%d].operator", index),
				Reason: "this condition names no operator type and was left out",
			})
			continue
		}
		left := row["leftValue"]
		if left == nil {
			left = row["value1"]
		}
		right := row["rightValue"]
		if right == nil {
			right = row["value2"]
		}
		converted = append(converted, map[string]any{
			"id":        row["id"],
			"leftValue": fromN8NValue(left),
			"operator": map[string]any{
				"type":        operator["type"],
				"operation":   operator["operation"],
				"singleValue": operator["singleValue"] == true,
			},
			"rightValue": fromN8NValue(right),
		})
	}
	if len(converted) == 0 {
		return nil, append(issues, Unsupported{Reason: "none of this node's conditions could be read"})
	}

	filter := map[string]any{"conditions": converted}
	if combinator, ok := declared["combinator"].(string); ok && combinator != "" {
		filter["combinator"] = combinator
	}
	// n8n's own default is case-sensitive; carrying the options rather than
	// assuming keeps a case-insensitive filter case-insensitive.
	options := map[string]any{"caseSensitive": true}
	if declaredOptions, ok := declared["options"].(map[string]any); ok {
		if sensitive, present := declaredOptions["caseSensitive"].(bool); present {
			options["caseSensitive"] = sensitive
		}
		if validation, present := declaredOptions["typeValidation"].(string); present && validation != "" {
			options["typeValidation"] = validation
		}
	}
	filter["options"] = options
	return filter, issues
}

// legacyCondition rewrites n8n v1's type-grouped condition as a v2 one.
//
// v1 had no operator object: the *group* was the type and `operation` sat
// beside the values. Reading it here means the evaluator sees one shape.
// The operation names are translated too: v1 `larger` is v2 `largerEquals`'s
// neighbour `bigger`, v1 `equal` is `equals`, and so on. An untranslated name
// would import cleanly and then fail the run with "not supported".
func legacyCondition(row map[string]any, group string) map[string]any {
	operation, _ := row["operation"].(string)
	if operation == "" {
		operation = legacyDefaultOperation(group)
	} else if mapped, ok := legacyOperations[operation]; ok {
		operation = mapped
	}
	return map[string]any{
		"leftValue":  row["value1"],
		"rightValue": row["value2"],
		"operator":   map[string]any{"type": group, "operation": operation},
	}
}

// legacyOperations translates n8n v1 operation names to their v2 equivalents.
// The v2 vocabulary is the evaluator's: equals/notEquals/contains and friends
// for strings, larger/smaller/largerEqual/smallerEqual for numbers, empty and
// notEmpty for presence, after/before for dates. Only the four number names
// actually change spelling; the rest already read the same in both
// generations, and are listed so that a name v1 has and this server does not
// is visible here rather than at run time.
var legacyOperations = map[string]string{
	"equal":        "equals",
	"notEqual":     "notEquals",
	"larger":       "larger",
	"largerEqual":  "largerEqual",
	"smaller":      "smaller",
	"smallerEqual": "smallerEqual",
	"contains":     "contains",
	"notContains":  "notContains",
	"startsWith":   "startsWith",
	"endsWith":     "endsWith",
	"regex":        "regex",
	"isEmpty":      "empty",
	"isNotEmpty":   "notEmpty",
	"after":        "after",
	"before":       "before",
}

// legacyDefaultOperation is n8n v1's per-type default when the operation is
// omitted: numbers default to `smaller`, everything else to `equals`.
func legacyDefaultOperation(group string) string {
	if group == "number" {
		return "smaller"
	}
	return "equals"
}
func filterToN8N(value any) map[string]any {
	declared, ok := value.(map[string]any)
	if !ok {
		return map[string]any{"conditions": []any{}}
	}
	entries, _ := declared["conditions"].([]any)
	converted := make([]any, 0, len(entries))
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		converted = append(converted, map[string]any{
			"id":         row["id"],
			"leftValue":  toN8NValue(row["leftValue"]),
			"operator":   row["operator"],
			"rightValue": toN8NValue(row["rightValue"]),
		})
	}
	written := map[string]any{"conditions": converted}
	if combinator, ok := declared["combinator"].(string); ok && combinator != "" {
		written["combinator"] = combinator
	}
	if options, ok := declared["options"].(map[string]any); ok {
		written["options"] = options
	}
	return written
}

func ifToKilas(node Node) (map[string]any, []Unsupported) {
	filter, issues := filterToKilas(node.Parameters["conditions"])
	if filter == nil {
		return map[string]any{}, issues
	}
	return map[string]any{"conditions": filter}, issues
}

func ifToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written, lossy := conditionsToN8N(node, "the IF node")
	return map[string]any{"conditions": written, "options": map[string]any{}}, lossy
}

// conditionsToN8N writes a node's conditions parameter back out in either of
// the shapes it may be stored in.
//
// The flat shape is what IF stored when it accepted exactly one condition, and
// a workflow saved then still has to export. Its `field` is a path rather than
// a resolved value, so it becomes the expression n8n would have written.
func conditionsToN8N(node workflow.Node, described string) (map[string]any, []Lossy) {
	switch typed := node.Parameters["conditions"].(type) {
	case map[string]any:
		written := filterToN8N(typed)
		written["options"] = mergeOptions(written["options"], map[string]any{"caseSensitive": true, "version": 2})
		return written, nil
	case []any:
		if len(typed) == 0 {
			return map[string]any{}, []Lossy{{Field: "conditions", Reason: described + " had no condition to export"}}
		}
		entries := make([]any, 0, len(typed))
		for index, entry := range typed {
			condition, _ := entry.(map[string]any)
			field, _ := condition["field"].(string)
			operation, _ := condition["operator"].(string)
			entries = append(entries, map[string]any{
				"id":         fmt.Sprintf("%s-%d", node.ID, index),
				"leftValue":  "={{ $json." + field + " }}",
				"rightValue": toN8NValue(condition["value"]),
				"operator":   map[string]any{"type": "string", "operation": operation},
			})
		}
		return map[string]any{
			"options":    map[string]any{"caseSensitive": true, "version": 2},
			"conditions": entries,
			"combinator": "and",
		}, nil
	default:
		return map[string]any{}, []Lossy{{Field: "conditions", Reason: described + " had no condition to export"}}
	}
}

func mergeOptions(existing any, defaults map[string]any) map[string]any {
	options, _ := existing.(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	for key, value := range defaults {
		if _, present := options[key]; !present {
			options[key] = value
		}
	}
	return options
}

// --- Merge ------------------------------------------------------------------

// mergeToKilas carries n8n's Merge configuration through.
//
// It used to rewrite every mode to `append` and report an issue whose own words
// were "changes what this node does" — accurate, and useless to somebody who
// imported a `combine`-mode Merge. Every mode now has an implementation, so the
// values travel as themselves.
func mergeToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	mode := defaultString(stringParameter(node.Parameters, "mode"), "append")
	converted := map[string]any{}

	// n8n v2 names the same family differently: `combinationMode` with
	// mergeByFields/mergeByPosition/multiplex. v3 spells it `mode: combine`
	// plus `combineBy`. Both land on the executor's mode vocabulary.
	if combination := stringParameter(node.Parameters, "combinationMode"); combination != "" {
		switch combination {
		case "mergeByFields":
			mode, converted["combineBy"] = "combine", "combineByFields"
		case "mergeByPosition":
			mode, converted["combineBy"] = "combine", "combineByPosition"
		case "multiplex":
			mode, converted["combineBy"] = "combine", "combineAll"
		default:
			issues = append(issues, Unsupported{
				Field:  "combinationMode",
				Reason: fmt.Sprintf("the n8n Merge combination mode %q has no equivalent; the node was imported as append", combination),
			})
			mode = "append"
		}
	}
	if combineBy := stringParameter(node.Parameters, "combineBy"); combineBy != "" {
		converted["combineBy"] = combineBy
		if mode == "append" {
			mode = "combine"
		}
	}
	converted["mode"] = mode

	if count, ok := numberParameter(node.Parameters, "numberInputs"); ok {
		converted["numberInputs"] = count
	}
	// The branch to keep: v3 `useDataOfInput`/`output` names it by input,
	// v2 `output` does too, and `chooseBranch` is the executor's own key.
	if branch, ok := numberParameter(node.Parameters, "chooseBranch"); ok {
		converted["chooseBranch"] = branch
	} else if branch, ok := numberParameter(node.Parameters, "useDataOfInput"); ok {
		converted["chooseBranch"] = branch
	} else if output := stringParameter(node.Parameters, "output"); output != "" {
		if resolved := mergeOutputBranch(output); resolved > 0 {
			converted["chooseBranch"] = float64(resolved)
		}
	}
	if joinMode := stringParameter(node.Parameters, "joinMode"); joinMode != "" {
		converted["joinMode"] = joinMode
	}
	if from := stringParameter(node.Parameters, "outputDataFrom"); from != "" {
		converted["outputDataFrom"] = from
	}

	// n8n's fields-to-match is a fixed collection of `{field1, field2}` pairs.
	// KilasFlow matches on one name across every input, which is the same thing
	// whenever the fields are named alike — and a pair naming two *different*
	// fields is reported rather than silently matched on the first.
	if fields, ok := node.Parameters["fieldsToMatchString"].(string); ok && strings.TrimSpace(fields) != "" {
		converted["fieldsToMatch"] = fields
	} else if collection, ok := node.Parameters["mergeByFields"].(map[string]any); ok {
		names := make([]string, 0, 2)
		values, _ := collection["values"].([]any)
		for _, entry := range values {
			pair, _ := entry.(map[string]any)
			first, _ := pair["field1"].(string)
			second, _ := pair["field2"].(string)
			if first == "" {
				continue
			}
			names = append(names, first)
			if second != "" && second != first {
				issues = append(issues, Unsupported{
					Field:  "mergeByFields",
					Reason: fmt.Sprintf("this Merge matched %q against %q; KilasFlow matches one field name across every input, so %q was used", first, second, first),
				})
			}
		}
		if len(names) > 0 {
			converted["fieldsToMatch"] = strings.Join(names, ",")
		}
	}

	// n8n keeps the Merge's output choice under `options`, under a name that
	// moved between versions: v3 spells it `mergeMode`, v2 `joinMode`, and v2
	// also carries the booleans `includeUnpaired`, `keepNonMatches` and
	// `enrichInput2`. All of them mean one thing — which items survive the join
	// — and the executor implements exactly that set as `joinMode`, so they are
	// mapped onto it.
	//
	// They used to be parked in an `options` bag nothing read. That was worse
	// than dropping them: the keys were marked consumed, so the import report
	// stayed clean, and the unpaired items simply disappeared from the output.
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		mode, unmapped := mergeJoinMode(options)
		if mode != "" {
			converted["joinMode"] = mode
		}
		if unmapped != "" {
			issues = append(issues, Unsupported{
				Field: unmapped,
				Reason: fmt.Sprintf("the Merge output type %q is not one this server implements "+
					"(keepMatches, keepEverything, enrichInput1, enrichInput2, keepNonMatches); "+
					"the node keeps matches only, so items that do not match are not carried",
					textOf(options[strings.TrimPrefix(unmapped, "options.")])),
			})
		}
		for _, key := range []string{"clashHandling", "multipleMatches"} {
			if _, present := options[key]; present {
				issues = append(issues, Unsupported{
					Field: "options." + key,
					Reason: fmt.Sprintf("the Merge option %q decides which of several matches is kept; this server "+
						"combines every matching pair and keeps each field of each item, so the option was not carried", key),
				})
			}
		}
		for key := range options {
			switch key {
			case "includeUnpaired", "keepNonMatches", "enrichInput2", "clashHandling", "mergeMode", "multipleMatches", "joinMode":
			default:
				issues = append(issues, Unsupported{
					Field:  "options." + key,
					Reason: fmt.Sprintf("the Merge option %q has no KilasFlow equivalent and was not carried", key),
				})
			}
		}
	}
	if mode == "combineBySql" || stringParameter(node.Parameters, "combineBy") == "combineBySql" ||
		mode == "passThrough" || mode == "wait" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "mode",
			Reason: fmt.Sprintf("the n8n Merge mode %q has no KilasFlow equivalent; replace it before running the workflow", stringParameter(node.Parameters, "mode")),
		})
	}
	return converted, issues
}

// mergeJoinMode reads the output choice n8n's Merge keeps under `options`, and
// names the key it could not map.
//
// Three spellings mean the same thing and moved between versions: v3's
// `mergeMode`, v2's `joinMode`, and v2's booleans — `includeUnpaired` keeps
// everything, `keepNonMatches` keeps only what did not match, `enrichInput2`
// keeps input 2 whole. The executor implements exactly that set, so they are
// mapped onto its `joinMode` rather than parked in a bag nothing reads.
//
// A value this server does not implement is named rather than mapped onto the
// nearest guess: a join that silently drops items is the failure this mapping
// exists to prevent.
func mergeJoinMode(options map[string]any) (mode string, unmapped string) {
	for _, key := range []string{"mergeMode", "joinMode"} {
		value, ok := options[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if joinModeIsImplemented(value) {
			return value, ""
		}
		return "", "options." + key
	}
	switch {
	case optionFlag(options, "includeUnpaired"):
		return "keepEverything", ""
	case optionFlag(options, "keepNonMatches"):
		return "keepNonMatches", ""
	case optionFlag(options, "enrichInput2"):
		return "enrichInput2", ""
	}
	return "", ""
}

// joinModeIsImplemented reports whether the merge executor implements this
// output choice. It mirrors the node's own option list.
func joinModeIsImplemented(mode string) bool {
	switch mode {
	case "keepMatches", "keepEverything", "enrichInput1", "enrichInput2", "keepNonMatches":
		return true
	}
	return false
}

// mergeOutputBranch reads n8n's v2/v3 output selector: `input1`/`input2` or a
// bare number. Zero means unrecognized rather than a valid branch.
func mergeOutputBranch(output string) int {
	trimmed := strings.TrimSpace(output)
	if strings.HasPrefix(strings.ToLower(trimmed), "input") {
		if number, err := strconv.Atoi(strings.TrimSpace(trimmed[5:])); err == nil {
			return number
		}
		return 0
	}
	if number, err := strconv.Atoi(trimmed); err == nil {
		return number
	}
	return 0
}

func mergeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"mode": defaultString(stringParameter(node.Parameters, "mode"), "append"),
	}
	for _, key := range []string{"combineBy", "numberInputs", "chooseBranch", "joinMode", "outputDataFrom"} {
		if value, present := node.Parameters[key]; present {
			written[key] = value
		}
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		written["options"] = options
	} else {
		written["options"] = map[string]any{}
	}
	if fields := stringParameter(node.Parameters, "fieldsToMatch"); fields != "" {
		values := make([]any, 0, 2)
		for _, name := range strings.Split(fields, ",") {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				values = append(values, map[string]any{"field1": trimmed, "field2": trimmed})
			}
		}
		written["mergeByFields"] = map[string]any{"values": values}
	}
	return written, nil
}

// --- Flow control -----------------------------------------------------------

func filterNodeToKilas(node Node) (map[string]any, []Unsupported) {
	filter, issues := filterToKilas(node.Parameters["conditions"])
	if filter == nil {
		return map[string]any{}, issues
	}
	return map[string]any{"conditions": filter}, issues
}

func filterNodeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written, lossy := conditionsToN8N(node, "the Filter node")
	return map[string]any{"conditions": written, "options": map[string]any{}}, lossy
}

// switchToKilas carries n8n's rules, in order.
//
// The order is the contract: a Switch's outputs are positional, and a rule that
// moved would move every wire below it.
func switchToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	if mode := stringParameter(node.Parameters, "mode"); mode != "" && mode != "rules" {
		// n8n's `expression` mode routes by evaluating an output index, which
		// has no rules to import at all.
		return map[string]any{}, append(issues, Unsupported{
			Field:  "mode",
			Reason: fmt.Sprintf("this Switch routes by %q rather than by rules, which KilasFlow does not support; rebuild it with routing rules", mode),
		})
	}

	rules, _ := node.Parameters["rules"].(map[string]any)
	values, _ := rules["values"].([]any)
	converted := make([]any, 0, len(values))
	if len(values) == 0 {
		// n8n's Switch v1 and v2 keep their rules under `rules.rules`, one row
		// per output, holding `value1`/`value2`, a `dataType` and an
		// `operation`. Reading only the v3 shape imported those as a node with
		// no rules and one output — so every branch from output 1 onwards was
		// held back and the workflow failed with "switch rules must be a list".
		legacy, legacyIssues := legacySwitchRules(rules)
		issues = append(issues, legacyIssues...)
		converted = legacy
	}
	for index, entry := range values {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		filter, ruleIssues := filterToKilas(rule["conditions"])
		for _, issue := range ruleIssues {
			issue.Field = fmt.Sprintf("rules[%d].%s", index, issue.Field)
			issues = append(issues, issue)
		}
		if filter == nil {
			continue
		}
		built := map[string]any{"conditions": filter}
		if name, _ := rule["outputKey"].(string); name != "" {
			built["outputKey"] = name
		} else if name, _ := rule["renameOutput"].(string); name != "" {
			built["outputKey"] = name
		}
		converted = append(converted, built)
	}
	if len(converted) == 0 {
		return map[string]any{}, append(issues, Unsupported{
			Reason: "this Switch has no readable rules; add one before running the workflow",
		})
	}

	built := map[string]any{"rules": converted}
	fallback, fallbackField := switchFallback(node)
	if fallback != nil {
		switch value := fallback.(type) {
		case string:
			if value == "extra" {
				built["fallbackOutput"] = "extra"
			} else if value != "none" && value != "" {
				// A numeric string is the same case as a number: it names an
				// existing output rather than the extra one.
				index, err := strconv.Atoi(strings.TrimSpace(value))
				if err == nil {
					issues = append(issues, switchNumericFallback(fallbackField, index))
					break
				}
				issues = append(issues, Unsupported{
					Field:  fallbackField,
					Reason: fmt.Sprintf("this Switch sent unmatched items to output %v; wire that rule's own output instead", value),
				})
			}
		case float64:
			issues = append(issues, switchNumericFallback(fallbackField, int(value)))
		case int:
			issues = append(issues, switchNumericFallback(fallbackField, value))
		default:
			issues = append(issues, Unsupported{
				Field:  fallbackField,
				Reason: fmt.Sprintf("this Switch sent unmatched items to output %v; wire that rule's own output instead", value),
			})
		}
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if all, _ := options["allMatchingOutputs"].(bool); all {
			built["allMatchingOutputs"] = true
		}
	}
	return built, issues
}

// switchNumericFallback reports an n8n fallback that names an existing output.
//
// A numeric fallback means "send unmatched items to output N", which is one of
// the rules' own branches. This server's Switch routes unmatched items to a new
// "extra" output or not at all, so the index has no equivalent — and writing it
// into `fallbackOutput` was worse than dropping it: the executor reads only
// "none" and "extra", so the value was inert, the items were dropped, and the
// import report said nothing at all. The index is named here instead.
func switchNumericFallback(field string, index int) Unsupported {
	return Unsupported{
		Field: field,
		Reason: fmt.Sprintf("this Switch sent unmatched items to output %d, one of the rules' own branches; "+
			"this server routes unmatched items to a new output or not at all, so wire that branch from the rule itself "+
			"or expect unmatched items to be dropped", index),
	}
}

// switchFallback reads the fallback output, which the versions keep in
// different places: v3 under `options.fallbackOutput`, v1/v2 as a top-level
// parameter. Reading only the first lost the branch unmatched items were
// supposed to take, so a legacy Switch silently dropped them.
func switchFallback(node Node) (any, string) {
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if value := options["fallbackOutput"]; value != nil {
			return value, "options.fallbackOutput"
		}
	}
	if value := node.Parameters["fallbackOutput"]; value != nil {
		return value, "fallbackOutput"
	}
	return nil, ""
}

// legacySwitchRules translates n8n's v1/v2 Switch rules.
//
// One row per output, each carrying the left-hand value, the comparison and the
// right-hand value — the same shape a v1 If condition has, so the row is
// translated by the same code (legacyCondition), including the operation-name
// vocabulary that changed between the two generations. A row's output index is
// its position, which is how the Switch routes.
func legacySwitchRules(rules map[string]any) ([]any, []Unsupported) {
	rows, _ := rules["rules"].([]any)
	converted := make([]any, 0, len(rows))
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if _, present := row["value1"]; !present {
			continue
		}
		dataType, _ := row["dataType"].(string)
		if dataType == "" {
			dataType = "string"
		}
		condition := legacyCondition(row, dataType)
		// Converted, so an expression in a legacy rule stays an expression
		// rather than arriving as the literal text `={{ … }}`.
		condition["leftValue"] = fromN8NValue(condition["leftValue"])
		condition["rightValue"] = fromN8NValue(condition["rightValue"])
		built := map[string]any{
			"conditions": map[string]any{
				"conditions": []any{condition},
				"options":    map[string]any{"caseSensitive": true},
			},
		}
		if name, _ := row["renameOutput"].(string); name != "" {
			built["outputKey"] = name
		}
		converted = append(converted, built)
	}
	if len(converted) == 0 {
		return nil, []Unsupported{{
			Severity: SeverityBlocking, Field: "rules",
			Reason: "this Switch's rules could not be read in either the current or the legacy shape; " +
				"rebuild them before running the workflow",
		}}
	}
	return converted, nil
}

func switchToN8N(node workflow.Node) (map[string]any, []Lossy) {
	rules, _ := node.Parameters["rules"].([]any)
	values := make([]any, 0, len(rules))
	for _, entry := range rules {
		rule, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		written := map[string]any{"conditions": filterToN8N(rule["conditions"])}
		if name, _ := rule["outputKey"].(string); name != "" {
			written["outputKey"] = name
			written["renameOutput"] = true
		}
		values = append(values, written)
	}

	options := map[string]any{}
	if node.Parameters["fallbackOutput"] == "extra" {
		options["fallbackOutput"] = "extra"
	}
	if node.Parameters["allMatchingOutputs"] == true {
		options["allMatchingOutputs"] = true
	}
	return map[string]any{
		"mode":    "rules",
		"rules":   map[string]any{"values": values},
		"options": options,
	}, nil
}

func limitToKilas(node Node) (map[string]any, []Unsupported) {
	converted := map[string]any{}
	if maximum, ok := numberParameter(node.Parameters, "maxItems"); ok {
		converted["maxItems"] = maximum
	}
	if keep := stringParameter(node.Parameters, "keep"); keep != "" {
		converted["keep"] = keep
	}
	return converted, nil
}

func limitToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{}
	for _, key := range []string{"maxItems", "keep"} {
		if value, present := node.Parameters[key]; present {
			written[key] = value
		}
	}
	return written, nil
}

// --- HTTP Request -----------------------------------------------------------

// fromN8NTree converts every value inside a nested parameter tree.
//
// fromN8NValue only reads the top level, which is right for a flat parameter
// and wrong for an option group: `options.responseHeaders.entries[0].value`
// is an expression two levels down, and leaving it unmarked exported it as the
// literal text `={{ … }}`.
func fromN8NTree(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted[key] = fromN8NTree(nested)
		}
		return converted
	case []any:
		converted := make([]any, 0, len(typed))
		for _, entry := range typed {
			converted = append(converted, fromN8NTree(entry))
		}
		return converted
	default:
		return fromN8NValue(value)
	}
}

// optionAt reads an option that n8n nests under a group in newer versions and
// wrote flat in older ones.
//
// Both spellings exist in the wild, and reading only the nested one silently
// ignored every option on a node imported from an older n8n.
func optionAt(options map[string]any, path ...string) (any, bool) {
	current := any(options)
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// optionNumber reads a numeric option wherever its version put it.
func optionNumber(options map[string]any, path ...string) (float64, bool) {
	value, present := optionAt(options, path...)
	if !present {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// optionFlag reads a boolean option wherever its version put it.
func optionFlag(options map[string]any, path ...string) bool {
	value, present := optionAt(options, path...)
	if !present {
		return false
	}
	flag, _ := value.(bool)
	return flag
}

// httpCollection reads a name/value collection under either of the two names
// n8n gives it: the HTTP Request node's, and the HTTP Request tool's.
func httpCollection(parameters map[string]any, flag string, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if values, ok := namedValues(parameters, flag, key); ok {
			return values, true
		}
	}
	return nil, false
}

// reportUnconsumedOptions names every option key nothing read.
//
// The importer promises never to drop a source parameter in silence, and an
// options collection is where the drops hid: the translator read six keys out
// of twenty and said nothing about the rest. `consumed` holds the keys the
// translator read, spelled as the path the choice was made at.
func reportUnconsumedOptions(options map[string]any, consumed map[string]bool, issues []Unsupported) []Unsupported {
	for _, key := range sortedKeys(options) {
		if consumed[key] || options[key] == nil {
			continue
		}
		if nested, isObject := options[key].(map[string]any); isObject {
			// An option group: walked one level deeper, because the outer
			// group being consumed is exactly how a setting two levels down
			// disappears without a word.
			for _, inner := range sortedKeys(nested) {
				child, nestsFurther := nested[inner].(map[string]any)
				if nestsFurther {
					for _, leaf := range sortedKeys(child) {
						if consumed[key+"."+inner+"."+leaf] {
							continue
						}
						issues = append(issues, Unsupported{
							Severity: SeverityDropped, Field: "options." + key + "." + inner + "." + leaf,
							Reason: fmt.Sprintf("the n8n option %q has no KilasFlow equivalent and was not carried", leaf),
						})
					}
					continue
				}
				if consumed[key+"."+inner] {
					continue
				}
				issues = append(issues, Unsupported{
					Severity: SeverityDropped, Field: "options." + key + "." + inner,
					Reason: fmt.Sprintf("the n8n option %q has no KilasFlow equivalent and was not carried", inner),
				})
			}
			continue
		}
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "options." + key,
			Reason: fmt.Sprintf("the n8n option %q has no KilasFlow equivalent and was not carried", key),
		})
	}
	return issues
}

// jsonParameters reads n8n's JSON spelling of a parameter collection.
//
// `specifyQuery: json` and `specifyHeaders: json` hold the whole collection as
// JSON text. It was not read at all, so those nodes imported with no query and
// no headers — a request that looks configured and calls a different URL.
func jsonParameters(parameters map[string]any, key string, field string, issues []Unsupported) (map[string]any, []Unsupported) {
	raw, present := parameters[key]
	if !present || raw == nil {
		return nil, issues
	}
	text, isText := raw.(string)
	if !isText || strings.TrimSpace(text) == "" {
		return nil, issues
	}
	if strings.HasPrefix(text, "=") {
		// The whole collection is one expression, which a key/value map cannot
		// hold. Named rather than dropped: the request would otherwise go out
		// with none of the parameters it was written to send.
		return nil, append(issues, Unsupported{
			Severity: SeverityBlocking, Field: field,
			Reason: "this node builds its parameters from an expression that returns the whole JSON " +
				"object, which this server cannot evaluate into a parameter list; write the fields out " +
				"one by one",
		})
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, append(issues, Unsupported{
			Severity: SeverityBlocking, Field: field,
			Reason: fmt.Sprintf("this node's JSON parameters could not be read (%v); fix the JSON before running the workflow", err),
		})
	}
	converted := make(map[string]any, len(parsed))
	for name, value := range parsed {
		converted[name] = fromN8NTree(value)
	}
	return converted, issues
}

// httpBodyFields carries n8n's key/value body parameters.
//
// Kept as a map of name → value with expression markers intact rather than
// marshalled into a JSON string. Marshalling turned `={{ $json.id }}` into the
// literal text of the marker, so a body field that was an expression arrived as
// a JSON object describing an expression.
func httpBodyFields(node Node, parameters map[string]any, issues []Unsupported) []Unsupported {
	fields, ok := namedValues(node.Parameters, "sendBody", "bodyParameters")
	if !ok || len(fields) == 0 {
		return issues
	}
	parameters["bodyFields"] = fields
	return issues
}

func httpToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{
		"method": strings.ToUpper(defaultString(stringParameter(node.Parameters, "method"), "GET")),
		"url":    fromN8NValue(node.Parameters["url"]),
	}

	if stringParameter(node.Parameters, "specifyQuery") == "json" {
		query, queryIssues := jsonParameters(node.Parameters, "jsonQuery", "jsonQuery", issues)
		issues = queryIssues
		if len(query) > 0 {
			parameters["sendQuery"] = true
			parameters["queryParameters"] = query
		}
	} else if query, ok := httpCollection(node.Parameters, "sendQuery", "queryParameters", "parametersQuery"); ok {
		parameters["sendQuery"] = true
		parameters["queryParameters"] = query
	}

	if stringParameter(node.Parameters, "specifyHeaders") == "json" {
		headers, headerIssues := jsonParameters(node.Parameters, "jsonHeaders", "jsonHeaders", issues)
		issues = headerIssues
		if len(headers) > 0 {
			parameters["sendHeaders"] = true
			parameters["headers"] = headers
		}
	} else if headers, ok := httpCollection(node.Parameters, "sendHeaders", "headerParameters", "parametersHeaders"); ok {
		parameters["sendHeaders"] = true
		parameters["headers"] = headers
	}

	if send, _ := node.Parameters["sendBody"].(bool); send {
		parameters["sendBody"] = true
		contentType := stringParameter(node.Parameters, "contentType")
		specifyBody := stringParameter(node.Parameters, "specifyBody")
		switch {
		case strings.Contains(contentType, "multipart"):
			// A multipart body is built from binary parts this server has no
			// way to attach, so the choice is refusing or sending something
			// else. Sending something else is how a form upload becomes an
			// empty POST that the far end accepts.
			parameters["bodyType"] = "form"
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "contentType",
				Reason: "this node sends a multipart form body, which this server does not build; " +
					"send the fields as a form-urlencoded or JSON body instead",
			})
		case contentType == "binaryData":
			parameters["bodyType"] = "raw"
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "contentType",
				Reason: "this node sends a binary body from the item's binary data, which this server " +
					"does not attach to a request; reference the file from an expression instead",
			})
		case contentType == "raw":
			parameters["bodyType"] = "raw"
			if rawContentType := stringParameter(node.Parameters, "rawContentType"); rawContentType != "" {
				parameters["rawContentType"] = rawContentType
			}
			if body, present := node.Parameters["body"]; present {
				parameters["body"] = fromN8NValue(body)
			}
		case contentType == "form-urlencoded":
			parameters["bodyType"] = "form"
			issues = httpBodyFields(node, parameters, issues)
		default:
			parameters["bodyType"] = "json"
			if specifyBody == "json" {
				if body, present := node.Parameters["jsonBody"]; present {
					parameters["body"] = fromN8NValue(body)
				}
			} else {
				carried := httpBodyFields(node, parameters, issues)
				issues = carried
				if _, present := parameters["bodyFields"]; !present {
					// No key/value body was stored either, so the node has an
					// empty body: `jsonBody` may still be there in every shape.
					if body, present := node.Parameters["jsonBody"]; present {
						parameters["body"] = fromN8NValue(body)
					}
				}
			}
		}

		if stringParameter(node.Parameters, "inputDataFieldName") != "" && contentType == "binaryData" {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "inputDataFieldName",
				Reason: "the name of the binary field this node sent is meaningless without a binary body and was not carried",
			})
		}
	}

	issues = append(issues, httpOptionsToKilas(node, parameters)...)

	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" && authentication != "none" {
		// A credential reference in an exported n8n workflow is an n8n ID and
		// means nothing here, so it is named rather than silently dropped.
		//
		// The mode itself is carried, because the mode is what decides which
		// KilasFlow credential type has to be attached. It used to be dropped
		// along with the reference, so a node that used generic header auth
		// imported looking unauthenticated.
		parameters["authentication"] = authentication
		if generic := stringParameter(node.Parameters, "genericAuthType"); generic != "" {
			parameters["genericAuthType"] = generic
		}
		issues = append(issues, Unsupported{
			Reason: "this HTTP Request used an n8n credential. Credentials are not imported; attach a KilasFlow credential before running the workflow.",
		})
	}
	return parameters, issues
}

// httpOptionsToKilas carries the request options this server implements and
// names the ones it does not.
func httpOptionsToKilas(node Node, parameters map[string]any) []Unsupported {
	options, ok := node.Parameters["options"].(map[string]any)
	if !ok || len(options) == 0 {
		// A node with no options at all still has a redirect policy, and the
		// two sides default it differently — so the default is written even
		// when there is nothing else to read.
		applyRedirectDefault(node, parameters)
		return nil
	}
	issues := make([]Unsupported, 0)
	consumed := map[string]bool{}

	// Timeout. n8n renamed this option at 4.2 and changed its unit with the
	// name: `timeout` is milliseconds, `requestTimeout` is seconds. Copying
	// either one into a seconds parameter made a 5000 ms timeout 5000 seconds
	// — clamped to the deployment's ceiling, so the node silently waited far
	// longer than its author asked for.
	if timeout, present := optionNumber(options, "timeout"); present {
		consumed["timeout"] = true
		if timeout > 0 {
			parameters["requestTimeoutSeconds"] = timeout / 1000
		}
	} else if timeout, present := optionNumber(options, "requestTimeout"); present {
		consumed["requestTimeout"] = true
		if timeout > 0 {
			parameters["requestTimeoutSeconds"] = timeout
		}
	}

	for _, path := range [][]string{{"response", "response", "neverError"}, {"response", "neverError"}, {"neverError"}} {
		if optionFlag(options, path...) {
			consumed[strings.Join(path, ".")] = true
			parameters["neverError"] = true
			break
		}
	}
	for _, path := range [][]string{{"response", "response", "responseFormat"}, {"response", "responseFormat"}, {"responseFormat"}} {
		if value, present := optionAt(options, path...); present {
			consumed[strings.Join(path, ".")] = true
			if format := textOf(value); format != "" {
				switch format {
				case "autodetect", "json", "text", "file":
					parameters["responseFormat"] = format
				default:
					issues = append(issues, Unsupported{
						Severity: SeverityBlocking, Field: "options.response.responseFormat",
						Reason: fmt.Sprintf("the n8n response format %q is not one this server reads "+
							"(autodetect, json, text, file); the response would be decoded the wrong way", format),
					})
				}
			}
			break
		}
	}
	for _, path := range [][]string{{"response", "response", "fullResponse"}, {"response", "fullResponse"}, {"fullResponse"}} {
		if optionFlag(options, path...) {
			consumed[strings.Join(path, ".")] = true
			parameters["fullResponse"] = true
			break
		}
	}
	for _, path := range [][]string{{"response", "response", "outputPropertyName"}, {"response", "outputPropertyName"}, {"outputPropertyName"}} {
		if value, present := optionAt(options, path...); present {
			consumed[strings.Join(path, ".")] = true
			if name := textOf(value); name != "" {
				parameters["outputPropertyName"] = name
			}
			break
		}
	}
	for _, path := range [][]string{{"redirect", "redirect", "followRedirects"}, {"redirect", "followRedirects"}, {"followRedirects"}} {
		if value, present := optionAt(options, path...); present {
			consumed[strings.Join(path, ".")] = true
			if flag, isFlag := value.(bool); isFlag {
				// Both values are carried. Writing only `false` meant a node
				// whose author turned redirects *on* imported as "do not
				// follow", which is the opposite of what it said — and the
				// import report stayed clean.
				parameters["followRedirects"] = flag
			}
			break
		}
	}
	for _, path := range [][]string{{"redirect", "redirect", "maxRedirects"}, {"redirect", "maxRedirects"}, {"maxRedirects"}} {
		if redirects, present := optionNumber(options, path...); present {
			consumed[strings.Join(path, ".")] = true
			if redirects > 0 {
				parameters["maxRedirects"] = redirects
			}
			break
		}
	}
	// n8n's HTTP Request follows redirects by default from v4 on, and that
	// default is what its own description declares. This server's node defaults
	// the other way, so a v4 node that never opened the redirect option — which
	// is nearly every exported one — imported as "do not follow" and stopped at
	// the first 3xx with a body its author never expected.
	applyRedirectDefault(node, parameters)

	if optionFlag(options, "allowUnauthorizedCerts") {
		consumed["allowUnauthorizedCerts"] = true
		// Refused rather than ignored, and refused loudly: a node that skips
		// TLS verification to reach a self-signed endpoint fails every call
		// here, and quietly verifying the certificate instead is not a
		// behaviour anybody asked for.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "options.allowUnauthorizedCerts",
			Reason: "this node accepts unauthorized TLS certificates, which this server never does; " +
				"install the endpoint's certificate or disable this option before running the workflow",
		})
	}
	if _, present := optionAt(options, "pagination"); present {
		consumed["pagination"] = true
		issues = append(issues, Unsupported{
			Field: "options.pagination",
			Reason: "n8n's request pagination is not implemented here; the node sends one request and " +
				"returns its response, so add a loop node for the remaining pages",
		})
	}
	if _, present := optionAt(options, "batching"); present {
		consumed["batching"] = true
		issues = append(issues, Unsupported{
			Field: "options.batching",
			Reason: "n8n splits a batched request into several; this server sends one request with " +
				"every item, so split the items with a loop node instead",
		})
	}

	return reportUnconsumedOptions(options, consumed, issues)
}

func httpToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{
		"method":  defaultString(stringParameter(node.Parameters, "method"), "GET"),
		"url":     toN8NValue(node.Parameters["url"]),
		"options": httpOptionsToN8N(node.Parameters),
	}

	if enabled, _ := node.Parameters["sendQuery"].(bool); enabled {
		parameters["sendQuery"] = true
		parameters["queryParameters"] = n8nNamedValues(node.Parameters["queryParameters"])
	}
	if enabled, _ := node.Parameters["sendHeaders"].(bool); enabled {
		parameters["sendHeaders"] = true
		parameters["headerParameters"] = n8nNamedValues(node.Parameters["headers"])
	}
	if enabled, _ := node.Parameters["sendBody"].(bool); enabled {
		parameters["sendBody"] = true
		switch stringParameter(node.Parameters, "bodyType") {
		case "form":
			parameters["contentType"] = "form-urlencoded"
			if fields, ok := node.Parameters["bodyFields"].(map[string]any); ok && len(fields) > 0 {
				parameters["bodyParameters"] = n8nNamedValues(fields)
			}
			if body, present := node.Parameters["body"]; present {
				parameters["body"] = toN8NValue(body)
			}
		case "raw":
			parameters["contentType"] = "raw"
			if contentType := stringParameter(node.Parameters, "rawContentType"); contentType != "" {
				parameters["rawContentType"] = contentType
			}
			parameters["body"] = toN8NValue(node.Parameters["body"])
		default:
			parameters["contentType"] = "json"
			if fields, ok := node.Parameters["bodyFields"].(map[string]any); ok && len(fields) > 0 {
				parameters["specifyBody"] = "keypair"
				parameters["bodyParameters"] = n8nNamedValues(fields)
			} else {
				parameters["specifyBody"] = "json"
				parameters["jsonBody"] = toN8NValue(node.Parameters["body"])
			}
		}
	}
	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" {
		parameters["authentication"] = authentication
		if generic := stringParameter(node.Parameters, "genericAuthType"); generic != "" {
			parameters["genericAuthType"] = generic
		}
	}
	if _, set := node.Parameters["neverError"]; set {
		lossy = append(lossy, Lossy{Field: "neverError", Reason: "n8n expresses this as an error-handling setting rather than a parameter; set \"Continue On Fail\" in n8n if it is needed"})
	}
	return parameters, lossy
}

// applyRedirectDefault writes the redirect policy an n8n HTTP Request node
// implies when its options say nothing about it.
//
// n8n's own node description defaults `followRedirects` to true from
// typeVersion 4 on, and n8n omits a parameter that holds its default, so the
// overwhelming majority of exported v4+ nodes carry no redirect option at all.
// KilasFlow's node defaults it to false, so without this the two sides disagree
// about every one of those nodes and the imported request stops at the first
// 3xx. An explicit option always wins.
func applyRedirectDefault(node Node, parameters map[string]any) {
	if _, set := parameters["followRedirects"]; set {
		return
	}
	if node.TypeVersion >= 4 {
		parameters["followRedirects"] = true
	}
}

// httpOptionsToN8N is the inverse of httpOptionsToKilas, and the same
// principle applies: an option this server read goes back where n8n keeps it,
// and an option it never had is not invented.
func httpOptionsToN8N(source map[string]any) map[string]any {
	options := map[string]any{}
	response := map[string]any{}
	if seconds, ok := source["requestTimeoutSeconds"].(float64); ok && seconds > 0 {
		// Milliseconds, which is the unit of the `timeout` spelling this export
		// writes: n8n reads it as milliseconds and changed both the name and
		// the unit at 4.2 (`requestTimeout`, seconds).
		options["timeout"] = seconds * 1000
	}
	if flag, _ := source["neverError"].(bool); flag {
		response["neverError"] = true
	}
	if format := stringParameter(source, "responseFormat"); format != "" && format != "autodetect" {
		response["responseFormat"] = format
	}
	if name := stringParameter(source, "outputPropertyName"); name != "" {
		response["outputPropertyName"] = name
	}
	if flag, set := source["fullResponse"].(bool); set && flag {
		response["fullResponse"] = true
	}
	if len(response) > 0 {
		options["response"] = map[string]any{"response": response}
	}
	redirect := map[string]any{}
	// Both values go out. Writing only `false` lost the flag in the other
	// direction: a KilasFlow node that follows redirects exported with no
	// option at all, so an n8n instance read its own default instead of the
	// node's.
	if flag, set := source["followRedirects"].(bool); set {
		redirect["followRedirects"] = flag
	}
	if redirects, ok := source["maxRedirects"].(float64); ok && redirects > 0 {
		redirect["maxRedirects"] = redirects
	}
	if len(redirect) > 0 {
		options["redirect"] = map[string]any{"redirect": redirect}
	}
	return options
}

// namedValues reads n8n's `{parameters: [{name, value}]}` collections.
//
// The HTTP Request *tool* keeps the same list under different names and a
// different wrapper key (`parametersQuery.values`, not
// `queryParameters.parameters`), and it has no sendQuery flag at all — the
// presence of the collection is the flag. Reading only the node's spelling
// imported every tool request with no query parameters and no headers.
func namedValues(parameters map[string]any, flag, key string) (map[string]any, bool) {
	wrapper, ok := parameters[key].(map[string]any)
	if !ok {
		return nil, false
	}
	if enabled, present := parameters[flag].(bool); present && !enabled {
		return nil, false
	}
	entries, ok := wrapper["parameters"].([]any)
	if !ok {
		entries, ok = wrapper["values"].([]any)
	}
	if !ok {
		return nil, false
	}
	values := map[string]any{}
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := fields["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		values[name] = fromN8NValue(fields["value"])
	}
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

func n8nNamedValues(value any) map[string]any {
	values, _ := value.(map[string]any)
	entries := make([]any, 0, len(values))
	for _, name := range sortedKeys(values) {
		entries = append(entries, map[string]any{"name": name, "value": toN8NValue(values[name])})
	}
	return map[string]any{"parameters": entries}
}

// --- Webhook and Respond ----------------------------------------------------

func webhookToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{
		"path": strings.Trim(stringParameter(node.Parameters, "path"), "/"),
	}

	// n8n's Webhook defaults to GET, and an export omits a parameter still
	// holding its default — so a method-less webhook is a GET webhook. Reading
	// the absence as POST minted a POST-only endpoint, and every caller the
	// workflow was written for got a 404.
	switch method := node.Parameters["httpMethod"].(type) {
	case []any:
		// n8n 2.1 allows several methods on one path.
		methods := make([]any, 0, len(method))
		for _, entry := range method {
			if name := strings.ToUpper(strings.TrimSpace(textOf(entry))); name != "" {
				methods = append(methods, name)
			}
		}
		if len(methods) > 0 {
			parameters["httpMethods"] = methods
		} else {
			parameters["httpMethod"] = "GET"
		}
	case string:
		if trimmed := strings.TrimSpace(method); trimmed == "" {
			parameters["httpMethod"] = "GET"
		} else {
			parameters["httpMethod"] = strings.ToUpper(trimmed)
		}
	default:
		parameters["httpMethod"] = "GET"
	}

	// n8n keeps the response configuration in two places: two keys at the top
	// level and the rest under options. Both are read, and the options travel
	// as they are — the runtime reads the keys n8n wrote, so renaming them here
	// would break the one thing that has to line up.
	if value, present := node.Parameters["responseData"]; present {
		parameters["responseData"] = textOf(value)
	}
	if value, present := node.Parameters["responseCode"]; present {
		parameters["responseCode"] = fromN8NTree(value)
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		parameters["options"] = fromN8NTree(options)
	}

	switch responseMode := stringParameter(node.Parameters, "responseMode"); responseMode {
	case "responseNode":
		parameters["responseMode"] = "responseNode"
	case "lastNode":
		parameters["responseMode"] = "lastNode"
	default:
		parameters["responseMode"] = "immediate"
	}

	switch authentication := stringParameter(node.Parameters, "authentication"); authentication {
	case "", "none":
		parameters["authentication"] = "none"
	case "basicAuth":
		parameters["authentication"] = "basicAuth"
		issues = append(issues, Unsupported{
			Reason: "this Webhook used n8n basic auth. Attach a KilasFlow httpBasicAuth credential before activating the workflow.",
		})
	case "headerAuth":
		parameters["authentication"] = "headerAuth"
		issues = append(issues, Unsupported{
			Reason: "this Webhook used n8n header auth. Attach a KilasFlow httpHeaderAuth credential before activating the workflow.",
		})
	case "jwtAuth":
		// Kept rather than rewritten to none. The mode is what decides which
		// credential has to be attached, and an imported endpoint that says
		// "unauthenticated" is an endpoint somebody will leave open.
		parameters["authentication"] = "jwtAuth"
		issues = append(issues, Unsupported{
			Reason: "this Webhook used n8n JWT auth. Attach a KilasFlow jwtAuth credential (Key Type Passphrase or PEM Key, with the matching algorithm) before activating the workflow.",
		})
	default:
		parameters["authentication"] = "none"
		issues = append(issues, Unsupported{
			Reason: fmt.Sprintf("the n8n webhook authentication mode %q is not supported; the imported webhook is unauthenticated and should be secured before activation", authentication),
		})
	}
	return parameters, issues
}

// webhookMinimumVersion is the oldest n8n Webhook typeVersion that can express
// this node's method selection.
//
// `multipleMethods` and the array-valued `httpMethod` arrived in 2.1, so a node
// answering several methods exported at the 2.0 pin claimed a version whose
// node has no such parameter — n8n would read the array as a single method or
// refuse the file, and the selection was lost either way.
func webhookMinimumVersion(node workflow.Node) float64 {
	if methods, ok := node.Parameters["httpMethods"].([]any); ok && len(methods) > 1 {
		return 2.1
	}
	return 0
}

// webhookToN8N is the exact inverse of webhookToKilas.
//
// The response mode has to be translated rather than passed through. n8n's
// enum is onReceived / lastNode / responseNode; KilasFlow calls the first of
// those `immediate`, and writing that word into an exported workflow produced a
// document n8n rejects. `defaultString` hid it, because it only substitutes
// when the value is empty and `immediate` is not empty.
func webhookToN8N(node workflow.Node) (map[string]any, []ExportIssue) {
	responseMode := "onReceived"
	switch stringParameter(node.Parameters, "responseMode") {
	case "responseNode":
		responseMode = "responseNode"
	case "lastNode":
		responseMode = "lastNode"
	case "immediate", "":
		responseMode = "onReceived"
	}
	written := map[string]any{
		"path":         stringParameter(node.Parameters, "path"),
		"responseMode": responseMode,
		"options":      map[string]any{},
	}
	if methods, ok := node.Parameters["httpMethods"].([]any); ok && len(methods) > 0 {
		// n8n keeps the list in the same `httpMethod` parameter the single
		// form uses and gates it behind `multipleMethods`. Writing the array
		// without the flag produced a node n8n reads as a single method — the
		// selection was not representable in the file at all.
		written["httpMethod"] = methods
		written["multipleMethods"] = true
	} else {
		// Written explicitly rather than left to n8n's default: an imported
		// GET webhook exported without the key is a GET webhook in n8n too,
		// but saying so costs nothing and makes the round trip readable.
		written["httpMethod"] = defaultString(strings.ToUpper(stringParameter(node.Parameters, "httpMethod")), "GET")
	}
	if value, present := node.Parameters["responseData"]; present {
		written["responseData"] = textOf(value)
	}
	if value, present := node.Parameters["responseCode"]; present {
		written["responseCode"] = toN8NTree(value)
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		written["options"] = toN8NTree(options)
	}
	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" && authentication != "none" {
		written["authentication"] = authentication
	}
	return written, nil
}

// toN8NTree is the export inverse of fromN8NTree.
func toN8NTree(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted[key] = toN8NTree(nested)
		}
		return converted
	case []any:
		converted := make([]any, 0, len(typed))
		for _, entry := range typed {
			converted = append(converted, toN8NTree(entry))
		}
		return converted
	default:
		return toN8NValue(value)
	}
}

// formTriggerToKilas maps n8n's Form Trigger onto this server's.
//
// The two nodes ask for the same thing in the same shape — a path, a title, a
// description, an auth mode and a list of fields — so the mapping is mostly a
// copy. The one structural difference is the field options: n8n keeps each
// choice in its own `{option}` row, and this server reads a list of strings.
func formTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{
		"path": strings.Trim(stringParameter(node.Parameters, "path"), "/"),
	}
	for _, key := range []string{"formTitle", "formDescription"} {
		if value := stringParameter(node.Parameters, key); value != "" {
			parameters[key] = fromN8NValue(value)
		}
	}
	switch responseMode := stringParameter(node.Parameters, "responseMode"); responseMode {
	case "lastNode", "responseNode":
		parameters["responseMode"] = responseMode
	default:
		// n8n's onReceived, and its absence, both mean "answer as soon as the
		// form is submitted", which this server calls immediate.
		parameters["responseMode"] = "immediate"
	}
	switch authentication := stringParameter(node.Parameters, "authentication"); authentication {
	case "basicAuth", "headerAuth":
		parameters["authentication"] = authentication
		issues = append(issues, Unsupported{
			Reason: fmt.Sprintf("this form used n8n %s auth. Attach the matching KilasFlow credential "+
				"before activating the workflow.", authentication),
		})
	default:
		parameters["authentication"] = "none"
	}

	if wrapper, ok := node.Parameters["formFields"].(map[string]any); ok {
		rows, _ := wrapper["values"].([]any)
		fields := make([]any, 0, len(rows))
		for index, entry := range rows {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			field := map[string]any{}
			for _, key := range []string{"fieldLabel", "fieldType", "placeholder"} {
				if value := stringParameter(row, key); value != "" {
					field[key] = fromN8NValue(value)
				}
			}
			if required, present := row["requiredField"]; present && required != nil {
				field["requiredField"] = required
			}
			if options, ok := row["fieldOptions"].(map[string]any); ok {
				if values, ok := options["values"].([]any); ok {
					choices := make([]any, 0, len(values))
					for _, choice := range values {
						option, _ := choice.(map[string]any)
						if option == nil {
							continue
						}
						if value, present := option["option"]; present && value != nil {
							choices = append(choices, fromN8NValue(value))
						}
					}
					if len(choices) > 0 {
						field["fieldOptions"] = map[string]any{"values": choices}
					}
				}
			}
			if field["fieldType"] == "file" || field["fieldType"] == "fileUpload" {
				// The node accepts an upload, but the field's own options are
				// the form's business rather than the importer's.
				field["fieldType"] = "file"
			}
			if len(field) == 0 {
				issues = append(issues, Unsupported{
					Severity: SeverityDropped, Field: fmt.Sprintf("formFields.values[%d]", index),
					Reason: "this form field could not be read and was dropped; add it again before activating",
				})
				continue
			}
			fields = append(fields, field)
		}
		if len(fields) > 0 {
			parameters["formFields"] = map[string]any{"values": fields}
		}
	}

	// n8n's test mode serves the form on a different URL and does not produce a
	// production webhook; this server has one form endpoint per path.
	if mode := stringParameter(node.Parameters, "formMode"); mode == "test" {
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "formMode",
			Reason: "this trigger was in n8n's test mode, which serves the form at a test URL; this " +
				"server has one form endpoint, so the form answers on its production path",
		})
	}
	// The native form trigger has the same option, so it is carried rather than
	// reported: an issue here would tell the user something was lost that was
	// not.
	if value, present := node.Parameters["appendAttribution"]; present && value != nil {
		parameters["appendAttribution"] = fromN8NValue(value)
	}
	return parameters, issues
}

// formTriggerToN8N writes the trigger back in n8n's shape.
func formTriggerToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"path": stringParameter(node.Parameters, "path"),
	}
	for _, key := range []string{"formTitle", "formDescription"} {
		if value, present := node.Parameters[key]; present && value != nil {
			parameters[key] = toN8NValue(value)
		}
	}
	responseMode := "onReceived"
	switch stringParameter(node.Parameters, "responseMode") {
	case "lastNode":
		responseMode = "lastNode"
	case "responseNode":
		responseMode = "responseNode"
	}
	parameters["responseMode"] = responseMode
	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" && authentication != "none" {
		parameters["authentication"] = authentication
	}
	if attribution, present := node.Parameters["appendAttribution"]; present && attribution != nil {
		parameters["appendAttribution"] = toN8NValue(attribution)
	}
	if wrapper, ok := node.Parameters["formFields"].(map[string]any); ok {
		rows, _ := wrapper["values"].([]any)
		values := make([]any, 0, len(rows))
		for _, entry := range rows {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			written := map[string]any{}
			for _, key := range []string{"fieldLabel", "fieldType", "placeholder"} {
				if value, present := row[key]; present {
					written[key] = toN8NValue(value)
				}
			}
			if required, present := row["requiredField"]; present {
				written["requiredField"] = required
			}
			if options, ok := row["fieldOptions"].(map[string]any); ok {
				if choices, ok := options["values"].([]any); ok {
					entries := make([]any, 0, len(choices))
					for _, choice := range choices {
						entries = append(entries, map[string]any{"option": toN8NValue(choice)})
					}
					written["fieldOptions"] = map[string]any{"values": entries}
				}
			}
			values = append(values, written)
		}
		if len(values) > 0 {
			parameters["formFields"] = map[string]any{"values": values}
		}
	}
	return parameters, nil
}

// errorTriggerToKilas maps n8n's Error Trigger onto this server's.
//
// The node is where an error workflow begins, and it takes nothing: the
// payload is the failed execution's, which the engine supplies. n8n's
// `workflowId` on the trigger is the id of the workflow it belongs to, which
// this server knows without being told.
func errorTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	if id := locatorName(node.Parameters["workflowId"]); id != "" {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "workflowId",
			Reason: "n8n's error trigger names the workflow it belongs to; this server reads that from " +
				"the workflow itself, so the value was not carried",
		})
	}
	return map[string]any{}, issues
}

func errorTriggerToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return map[string]any{}, nil
}

// stopAndErrorToKilas maps n8n's Stop and Error onto this server's.
//
// n8n keeps the error in one of three shapes behind `errorObject`: a plain
// message, an object of message and description, or JSON the author typed.
// This server's node takes a message and, optionally, JSON text — so an object
// is serialised into that text and a plain message stays a message. The
// alternative, mapping the object mode onto the message alone, would throw away
// the description the author wrote.
func stopAndErrorToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	message, _ := node.Parameters["errorMessage"]
	if message == nil || message == "" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "errorMessage",
			Reason: "this node stops the workflow with no message, so nothing would say why; give it a " +
				"message before activating",
		})
	} else {
		parameters["errorMessage"] = fromN8NValue(message)
	}

	switch mode := stringParameter(node.Parameters, "errorObject"); mode {
	case "", "message":
		// The default: the message is the whole error.
	case "object":
		described := map[string]any{}
		if value := node.Parameters["errorMessage"]; value != nil && value != "" {
			described["errorMessage"] = fromN8NValue(value)
		}
		if value := node.Parameters["errorDescription"]; value != nil && value != "" {
			described["errorDescription"] = fromN8NValue(value)
		}
		if len(described) > 0 {
			if encoded, err := json.Marshal(described); err == nil {
				parameters["errorObject"] = string(encoded)
			}
		}
	case "json":
		value := node.Parameters["errorObjectJson"]
		if value == nil {
			value = node.Parameters["errorObject"]
		}
		if encoded := jsonText(value); encoded != "" {
			parameters["errorObject"] = encoded
		} else {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "errorObject",
				Reason: "this node throws a JSON error object and its JSON could not be read; fix the JSON " +
					"before activating",
			})
		}
	default:
		issues = append(issues, Unsupported{
			Field:  "errorObject",
			Reason: fmt.Sprintf("the n8n error shape %q has no equivalent; the node throws its message only", mode),
		})
	}
	return parameters, issues
}

// jsonText renders an n8n value as JSON text, or "" when it cannot be.
//
// An expression marker is not JSON and is left to resolve at run time: the
// parameter it was written for is a string, and the evaluator produces the text
// the author's expression returns.
func jsonText(value any) string {
	if value == nil {
		return ""
	}
	if marker, ok := value.(map[string]any); ok {
		if mode, _ := marker["mode"].(string); mode == "expression" {
			return ""
		}
	}
	if text, ok := value.(string); ok {
		if strings.HasPrefix(text, "=") {
			return ""
		}
		if !json.Valid([]byte(text)) {
			return ""
		}
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stopAndErrorToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"errorObject":  "message",
		"errorMessage": toN8NValue(node.Parameters["errorMessage"]),
	}
	if text := stringParameter(node.Parameters, "errorObject"); text != "" {
		parameters["errorObject"] = "json"
		parameters["errorObjectJson"] = text
	}
	return parameters, nil
}

func respondToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}
	// n8n keeps the status code under options on this node and at the top level
	// on some versions, so both are read.
	options, _ := node.Parameters["options"].(map[string]any)
	if code, ok := numberParameter(node.Parameters, "responseCode"); ok {
		parameters["responseCode"] = code
	} else if code, ok := numberParameter(options, "responseCode"); ok {
		parameters["responseCode"] = code
	}
	if body, ok := node.Parameters["responseBody"]; ok {
		parameters["responseBody"] = fromN8NValue(body)
	}
	if url, ok := node.Parameters["redirectURL"]; ok {
		parameters["redirectURL"] = fromN8NValue(url)
	}
	if headers := responseHeaderEntries(options); len(headers) > 0 {
		parameters["responseHeaders"] = headers
	}
	if key := stringParameter(options, "responseKey"); key != "" {
		parameters["responseKey"] = key
	}

	switch respondWith := stringParameter(node.Parameters, "respondWith"); respondWith {
	case "":
		// n8n's own default is the first incoming item, and an export omits a
		// parameter holding its default. Reading the absence as `text` answered
		// every webhook-backed API with an empty body.
		parameters["respondWith"] = "firstIncomingItem"
	case "text", "json", "allIncomingItems", "firstIncomingItem", "noData", "redirect":
		parameters["respondWith"] = respondWith
	case "binary":
		// Blocking rather than rewritten: a response silently downgraded from a
		// file to a JSON body is a broken integration that returns 200.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "respondWith",
			Reason: "responding with a binary file needs the response streamed from the binary store, " +
				"which this server does not do yet; respond with JSON carrying a link to the file instead",
		})
		parameters["respondWith"] = respondWith
	case "jwt":
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "respondWith",
			Reason: "responding with a signed JWT needs a signing credential type this server does not have; " +
				"build the token in a Code node and respond with text",
		})
		parameters["respondWith"] = respondWith
	default:
		issues = append(issues, Unsupported{Field: "respondWith", Reason: fmt.Sprintf(
			"the n8n Respond to Webhook mode %q has no equivalent; the node was imported as a text response", respondWith)})
		parameters["respondWith"] = "text"
	}
	// The options this node did not read are named rather than forgotten;
	// responseCode, responseHeaders and responseKey are the ones it does.
	if len(options) > 0 {
		consumed := map[string]bool{"responseCode": true, "responseHeaders": true, "responseKey": true}
		issues = reportUnconsumedOptions(options, consumed, issues)
	}
	return parameters, issues
}

// responseHeaderEntries reads n8n's `{entries: [{name, value}]}` header list.
func responseHeaderEntries(options map[string]any) map[string]any {
	collection, ok := options["responseHeaders"].(map[string]any)
	if !ok {
		return nil
	}
	entries, _ := collection["entries"].([]any)
	headers := make(map[string]any, len(entries))
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := fields["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		headers[name] = fromN8NValue(fields["value"])
	}
	return headers
}

// respondToN8N writes the response mode the node actually holds.
//
// It used to write `respondWith: "text"` unconditionally, which is a value
// invented by the exporter rather than derived from the document: a node
// configured to answer with JSON came back as text. Import already accepts both
// text and json, so the inverse must distinguish them.
func respondToN8N(node workflow.Node) (map[string]any, []ExportIssue) {
	respondWith := stringParameter(node.Parameters, "respondWith")
	if respondWith == "" {
		// A node saved before respondWith existed says which it meant by the
		// shape of its body: a string is text and anything else is JSON.
		respondWith = "text"
		if body, ok := node.Parameters["responseBody"]; ok {
			if _, isText := body.(string); !isText {
				respondWith = "json"
			}
		}
	}
	parameters := map[string]any{"respondWith": respondWith, "options": map[string]any{}}
	options := map[string]any{}
	if code, ok := numberParameter(node.Parameters, "responseCode"); ok {
		options["responseCode"] = code
	}
	if headers, ok := node.Parameters["responseHeaders"].(map[string]any); ok && len(headers) > 0 {
		entries := make([]any, 0, len(headers))
		for _, name := range sortedKeys(headers) {
			entries = append(entries, map[string]any{"name": name, "value": toN8NValue(headers[name])})
		}
		options["responseHeaders"] = map[string]any{"entries": entries}
	}
	if key := stringParameter(node.Parameters, "responseKey"); key != "" {
		options["responseKey"] = key
	}
	if len(options) > 0 {
		parameters["options"] = options
	}
	if body, ok := node.Parameters["responseBody"]; ok {
		parameters["responseBody"] = toN8NValue(body)
	}
	if url, ok := node.Parameters["redirectURL"]; ok {
		parameters["redirectURL"] = toN8NValue(url)
	}
	return parameters, nil
}

// --- Sub-workflows ----------------------------------------------------------

func executeWorkflowToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	// n8n's workflowId is a resource locator, and so is this node's, so the
	// shape carries across rather than being flattened to a string. Only the
	// value is portable — a cached name or a URL means nothing on a server that
	// has never seen that n8n instance, so neither is kept.
	//
	// It always arrives in By ID mode, whatever mode it left n8n in. A list
	// selection there holds an ID from *that* instance, and this server's list
	// will never contain it — so a locator imported in list mode would render
	// as an empty picker with an invisible value behind it, which reads as "no
	// workflow chosen" rather than "the wrong workflow is chosen".
	switch locator := node.Parameters["workflowId"].(type) {
	case map[string]any:
		if mode, _ := locator["mode"].(string); mode != "" && mode != "id" && mode != "list" {
			issues = append(issues, Unsupported{Field: "workflowId", Reason: fmt.Sprintf(
				"the workflow was selected by %q, which names it on the n8n instance it came from; "+
					"set the KilasFlow workflow this should call", mode)})
		}
		parameters["workflowId"] = property.WriteLocator(property.Locator{
			Mode: "id", Value: fromN8NValue(locator["value"]),
		})
	case nil:
	default:
		parameters["workflowId"] = property.WriteLocator(property.Locator{
			Mode: "id", Value: fromN8NValue(locator),
		})
	}
	// The imported ID is n8n's, so it will not resolve here whatever mode it
	// used — unless it names the workflow it sits in, which this server's
	// evaluator resolves at run time exactly as n8n does.
	if n8nSelfWorkflowReference(node.Parameters["workflowId"]) {
		parameters["workflowId"] = property.WriteLocator(property.Locator{
			Mode: "id", Value: expressionValue(n8nSelfWorkflowTemplate),
		})
	} else {
		// Said once, on every import, rather than discovered at run time.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "workflowId",
			Reason: "the sub-workflow is identified by its n8n workflow ID, which does not exist on this server; " +
				"set this node's Workflow to the KilasFlow workflow that should run",
		})
	}

	if mode := stringParameter(node.Parameters, "mode"); mode == "each" {
		parameters["itemsPerCall"] = "eachItem"
	} else {
		parameters["itemsPerCall"] = "allItems"
	}
	// n8n's "Define using fields below" mapper — what the sub-workflow is told,
	// per item, in the caller's context. It was never read, so every modern
	// sub-workflow call sent its raw items and a sub-workflow that routes on a
	// mapped field suddenly had nothing to route on.
	if fields, _, reported := workflowInputsToKilas(node.Parameters["workflowInputs"]); !reported && len(fields) > 0 {
		parameters["inputFields"] = fields
	} else if reported {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "workflowInputs",
			Reason: "this call mapped its workflow inputs in a shape this importer does not read, so the " +
				"sub-workflow receives the incoming items unchanged: define the fields on the call again",
		})
	}
	options, _ := node.Parameters["options"].(map[string]any)
	// n8n spells "do not wait" as waitForSubWorkflow: false.
	if wait, present := options["waitForSubWorkflow"]; present && wait == false {
		parameters["mode"] = "fireAndForget"
	} else {
		parameters["mode"] = "each"
	}
	return parameters, issues
}

func executeWorkflowToN8N(node workflow.Node) (map[string]any, []Lossy) {
	locator, _ := property.ReadLocator(node.Parameters["workflowId"])
	parameters := map[string]any{
		"workflowId": map[string]any{
			property.LocatorSentinel: true,
			"mode":                   defaultString(locator.Mode, "id"),
			"value":                  toN8NValue(locator.Value),
		},
		"options": map[string]any{},
	}
	if stringParameter(node.Parameters, "itemsPerCall") == "eachItem" {
		parameters["mode"] = "each"
	}
	if stringParameter(node.Parameters, "mode") == "fireAndForget" {
		parameters["options"] = map[string]any{"waitForSubWorkflow": false}
	}
	if fields, ok := node.Parameters["inputFields"].(map[string]any); ok && len(fields) > 0 {
		value := make(map[string]any, len(fields))
		for _, key := range sortedKeys(fields) {
			value[key] = toN8NValue(fields[key])
		}
		parameters["workflowInputs"] = map[string]any{"mappingMode": "defineBelow", "value": value}
	}
	return parameters, nil
}

func executeWorkflowTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	parameters := map[string]any{}
	source := stringParameter(node.Parameters, "inputSource")
	inputs, declared := node.Parameters["workflowInputs"].(map[string]any)
	switch source {
	case "workflowInputs", "fields":
		parameters["inputSource"] = "fields"
		if declared {
			parameters["workflowInputs"] = inputs
		}
	case "":
		// n8n 1.1 made workflowInputs the default, and an export omits a
		// parameter still holding its default — so a trigger that declares the
		// fields it expects carries no inputSource at all. Reading only an
		// explicit value imported every one of those as passthrough with its
		// declarations discarded. A trigger with nothing declared is left as
		// passthrough: an empty declaration and no declaration are the same
		// tool, and claiming `fields` for it would describe a contract nobody
		// wrote.
		if declared && declaredFieldCount(inputs) > 0 {
			parameters["inputSource"] = "fields"
			parameters["workflowInputs"] = inputs
		} else {
			parameters["inputSource"] = "passthrough"
		}
	default:
		// n8n's passthrough, and its jsonExample mode, both mean "take what the
		// caller sends". The example itself is documentation for n8n's editor
		// and has no run-time meaning to carry.
		parameters["inputSource"] = "passthrough"
	}
	return parameters, nil
}

// declaredFieldCount counts the entries of an n8n `{values: [{name, type}]}`
// declaration.
func declaredFieldCount(inputs map[string]any) int {
	values, _ := inputs["values"].([]any)
	return len(values)
}

func executeWorkflowTriggerToN8N(node workflow.Node) (map[string]any, []Lossy) {
	if stringParameter(node.Parameters, "inputSource") != "fields" {
		return map[string]any{"inputSource": "passthrough"}, nil
	}
	parameters := map[string]any{"inputSource": "workflowInputs"}
	if inputs, ok := node.Parameters["workflowInputs"].(map[string]any); ok {
		parameters["workflowInputs"] = inputs
	}
	return parameters, nil
}

// --- Schedule ---------------------------------------------------------------

// scheduleToKilas carries a Schedule Trigger's whole rule across.
//
// The rule shape is identical on both sides, so the import is a copy rather
// than a translation — which is the point of having adopted n8n's shape for the
// node rather than inventing one. What the import still owes the user is a
// statement of every place the two schedulers disagree, and that is what the
// diagnostics below are.
//
// Before this, the importer returned on the first interval carrying a cron
// expression and silently dropped every other one, so a rule saying "09:00 and
// 17:00" arrived as one of the two, and a rule built entirely in n8n's visual
// builder arrived as hourly.
func scheduleToKilas(node Node) (map[string]any, []Unsupported) {
	rule, _ := node.Parameters["rule"].(map[string]any)
	entries, _ := rule["interval"].([]any)
	intervals := scheduler.ReadRule(rule)
	if len(intervals) == 0 {
		// A trigger with no rule at all is n8n's default, which is every day.
		return map[string]any{"rule": map[string]any{"interval": []any{
			map[string]any{"field": scheduler.FieldDays, "daysInterval": float64(1),
				"triggerAtHour": float64(0), "triggerAtMinute": float64(0)},
		}}}, nil
	}

	issues := make([]Unsupported, 0)
	kept := make([]any, 0, len(intervals))
	for index, interval := range intervals {
		if err := interval.Validate(); err != nil {
			issues = append(issues, Unsupported{
				Field:  "rule.interval",
				Reason: fmt.Sprintf("trigger rule %d (%s) is out of range and was dropped: %v", index+1, interval.Field, err),
			})
			continue
		}
		if note := interval.Anchoring(); note != "" {
			// Named rather than absorbed: the schedule still runs at the right
			// time on the right kind of day, and only which of them is chosen
			// differs. That is a difference the author can accept once they
			// are told about it, and cannot if they are not.
			issues = append(issues, Unsupported{
				Field:  "rule.interval",
				Reason: fmt.Sprintf("trigger rule %d: %s", index+1, note),
			})
		}
		if index < len(entries) {
			if fields, ok := entries[index].(map[string]any); ok {
				kept = append(kept, fields)
				continue
			}
		}
		kept = append(kept, intervalToMap(interval))
	}
	if len(kept) == 0 {
		return map[string]any{"rule": map[string]any{"interval": []any{}}}, issues
	}
	return map[string]any{"rule": map[string]any{"interval": kept}}, issues
}

// intervalToMap renders a decoded interval back into the stored shape, for the
// case where the incoming entry was not a map this importer could keep.
func intervalToMap(interval scheduler.Interval) map[string]any {
	fields := map[string]any{"field": interval.Field}
	for key, value := range map[string]int{
		"secondsInterval": interval.SecondsInterval, "minutesInterval": interval.MinutesInterval,
		"hoursInterval": interval.HoursInterval, "daysInterval": interval.DaysInterval,
		"weeksInterval": interval.WeeksInterval, "monthsInterval": interval.MonthsInterval,
	} {
		if value > 0 {
			fields[key] = float64(value)
		}
	}
	switch interval.Field {
	case scheduler.FieldCronExpression:
		fields["expression"] = interval.Expression
	case scheduler.FieldMonths:
		fields["triggerAtDayOfMonth"] = float64(interval.TriggerAtDayOfMonth)
	case scheduler.FieldWeeks:
		days := make([]any, 0, len(interval.TriggerAtDay))
		for _, day := range interval.TriggerAtDay {
			days = append(days, float64(day))
		}
		fields["triggerAtDay"] = days
	}
	if interval.Field != scheduler.FieldCronExpression {
		fields["triggerAtHour"] = float64(interval.TriggerAtHour)
		fields["triggerAtMinute"] = float64(interval.TriggerAtMinute)
	}
	return fields
}

func scheduleToN8N(node workflow.Node) (map[string]any, []Lossy) {
	if rule, ok := node.Parameters["rule"].(map[string]any); ok {
		if entries, ok := rule["interval"].([]any); ok && len(entries) > 0 {
			return map[string]any{"rule": map[string]any{"interval": entries}}, nil
		}
	}
	// A node saved before Trigger Rules carries one cron string. Exporting it
	// as a custom interval is exact: that is what a custom interval is.
	return map[string]any{
		"rule": map[string]any{
			"interval": []any{map[string]any{
				"field":      scheduler.FieldCronExpression,
				"expression": defaultString(stringParameter(node.Parameters, "cron"), "0 * * * *"),
			}},
		},
	}, nil
}

// --- Date & Time and Wait ---------------------------------------------------
//
// The vendored reference checkout carries only four node sources — HttpRequest,
// If, Schedule and Set — so these two are mapped from n8n's published parameter
// surface rather than from its code. That is stated here because it changes how
// the mapping is written: every key it does not recognise produces a named
// diagnostic instead of being assumed away, so a workflow that used something
// this pair got wrong says so on import rather than running differently.

// dateTimeToKilas maps n8n's Date & Time node, which has two quite different
// generations under one type.
//
// v1 has an `action` of format-or-calculate; v2 replaced it with the operation
// set this node implements. They are told apart structurally — by which key is
// present — rather than by typeVersion, because a hand-written or
// partially-migrated document can carry either.
func dateTimeToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}
	options, _ := node.Parameters["options"].(map[string]any)
	operation := defaultString(stringParameter(node.Parameters, "operation"), "getCurrentDate")
	// v1 is detected by typeVersion, not by the `action` key: a v1 node at
	// the default action stores no `action` at all, and keying on it imports
	// a "format MMMM DD YYYY" node as "get current date".
	if action, isV1 := node.Parameters["action"].(string); isV1 || node.TypeVersion < 2 {
		switch action {
		case "", "format":
			parameters["operation"] = "formatDate"
			parameters["date"] = fromN8NValue(node.Parameters["value"])
			parameters["format"] = luxonFormat(momentToLuxon(stringParameter(node.Parameters, "toFormat")))
		case "calculate":
			parameters["operation"] = "addToDate"
			parameters["date"] = fromN8NValue(node.Parameters["value"])
			parameters["duration"] = node.Parameters["duration"]
			parameters["unit"] = dateUnit(defaultString(stringParameter(node.Parameters, "timeUnit"), "days"))
			if stringParameter(node.Parameters, "operation") == "subtract" {
				parameters["operation"] = "subtractFromDate"
			}
		default:
			issues = append(issues, Unsupported{Field: "action", Reason: fmt.Sprintf(
				"the n8n Date & Time action %q has no equivalent; the node was imported as a format operation", action)})
			parameters["operation"] = "formatDate"
			parameters["date"] = fromN8NValue(node.Parameters["value"])
		}
		if from := stringParameter(node.Parameters, "fromFormat"); from != "" {
			issues = append(issues, Unsupported{Field: "fromFormat", Reason: fmt.Sprintf(
				"the input format %q was not carried; this node reads ISO 8601, Unix timestamps and the "+
					"common human forms without being told which one to expect", from)})
		}
		// v1 writes its result to `data`, not `date`.
		name := defaultString(stringParameter(node.Parameters, "dataPropertyName"), stringParameter(options, "outputFieldName"))
		parameters["outputField"] = defaultString(name, "data")
		return parameters, issues
	}
	// `magnitude` is v2's name for the date being worked on; `date` is what
	// extractDate calls the same thing, and `startDate` what the comparison
	// does. One field here, three names there.
	switch operation {
	case "getCurrentDate", "addToDate", "subtractFromDate", "formatDate", "roundDate", "extractDate", "getTimeBetweenDates":
		parameters["operation"] = operation
	default:
		issues = append(issues, Unsupported{Field: "operation", Reason: fmt.Sprintf(
			"the n8n Date & Time operation %q has no equivalent; the node was imported as \"get current date\"", operation)})
		operation = "getCurrentDate"
		parameters["operation"] = operation
	}
	for _, key := range []string{"magnitude", "date", "startDate"} {
		if value, present := node.Parameters[key]; present && value != nil {
			parameters["date"] = fromN8NValue(value)
			break
		}
	}
	if value, present := node.Parameters["endDate"]; present {
		parameters["endDate"] = fromN8NValue(value)
	}
	if value, present := node.Parameters["duration"]; present {
		parameters["duration"] = value
	}
	// Singular units (`day`, `month`) are n8n's own vocabulary beside the
	// plural; both land on the executor's plural units.
	if unit := defaultString(stringParameter(node.Parameters, "timeUnit"), stringParameter(node.Parameters, "units")); unit != "" {
		parameters["unit"] = dateUnit(unit)
	}
	if part := stringParameter(node.Parameters, "part"); part != "" {
		parameters["part"] = part
	}
	if mode := stringParameter(node.Parameters, "mode"); mode != "" {
		parameters["roundMode"] = mode
	}
	// Rounding down snaps to the start of `toNearest`; n8n's singular `day`
	// is this node's plural `days`.
	if to := stringParameter(node.Parameters, "to"); to != "" {
		parameters["roundTo"] = dateUnit(to)
	} else if to := stringParameter(node.Parameters, "toNearest"); to != "" {
		parameters["roundTo"] = dateUnit(to)
	}
	if layout := defaultString(stringParameter(node.Parameters, "customFormat"), stringParameter(node.Parameters, "format")); layout != "" {
		parameters["format"] = luxonFormat(layout)
	}
	if zone := stringParameter(options, "timezone"); zone != "" {
		parameters["timezone"] = zone
	}
	// n8n's default keeps only the new field; this node keeps the whole item
	// unless told otherwise at run time. Carried rather than defaulted, so
	// the editor shows what the import meant.
	if include, present := options["includeInputFields"]; present && include == true {
		parameters["includeInputFields"] = true
	}
	return withDateOutputField(parameters, options, node, defaultString(stringParameter(node.Parameters, "operation"), "getCurrentDate")), issues
}

// dateUnit normalises n8n's singular units onto the executor's plural ones.
func dateUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "day":
		return "days"
	case "month":
		return "months"
	case "year":
		return "years"
	case "hour":
		return "hours"
	case "minute":
		return "minutes"
	case "second":
		return "seconds"
	case "week":
		return "weeks"
	default:
		return unit
	}
}

// withDateOutputField carries n8n's output field name, under either of the two
// spellings it has used. Absent, each operation keeps n8n's own default:
// formattedDate for a format, newDate for arithmetic, timeDifference for a
// comparison — because a downstream `{{ $json.formattedDate }}` that resolves
// empty is a silent break.
func withDateOutputField(parameters map[string]any, options map[string]any, node Node, operation string) map[string]any {
	name := defaultString(stringParameter(node.Parameters, "outputFieldName"), stringParameter(options, "outputFieldName"))
	if name == "" {
		name = dateDefaultOutputField(operation)
	}
	parameters["outputField"] = defaultString(name, "date")
	return parameters
}

// dateDefaultOutputField is n8n's per-operation default output field.
func dateDefaultOutputField(operation string) string {
	switch operation {
	case "formatDate":
		return "formattedDate"
	case "addToDate", "subtractFromDate":
		return "newDate"
	case "getTimeBetweenDates":
		return "timeDifference"
	case "extractDate":
		return "date"
	case "roundDate":
		return "date"
	default:
		return "date"
	}
}

// luxonFormat carries a format string across unchanged.
//
// It is a named function rather than a bare assignment because the tokens are
// the one place these two products are already speaking the same language, and
// a future need to translate should have somewhere obvious to go rather than
// being scattered over the call sites.
func luxonFormat(layout string) string {
	return layout
}

// momentTokens maps an n8n v1 moment token to the Luxon token this node's
// `format` speaks.
//
// Longest first, so a four-letter token is never read as two two-letter ones,
// and the list is read in one pass — see momentToLuxon for why that matters.
//
// The offset rows are the ones worth stating: moment's `Z` is `+07:00` and its
// `ZZ` is `+0700`, while Luxon's `Z` is the narrow `+7`, `ZZ` is `+07:00` and
// `ZZZ` is `+0700`. So moment's `Z` lands on Luxon's `ZZ` and moment's `ZZ` on
// Luxon's `ZZZ` — one row further along, not on the token of the same name.
//
// `X` (Unix seconds) is Luxon's `X` as well, and `x` (Unix milliseconds) is not
// listed because Luxon spells it the same way.
var momentTokens = []struct{ from, to string }{
	{"YYYY", "yyyy"}, {"MMMM", "MMMM"}, {"DDDD", "ooo"}, {"dddd", "cccc"},
	{"MMM", "MMM"}, {"DDD", "o"}, {"ddd", "ccc"},
	{"YY", "yy"}, {"MM", "MM"}, {"DD", "dd"}, {"dd", "cc"},
	{"HH", "HH"}, {"hh", "hh"}, {"mm", "mm"}, {"ss", "ss"}, {"ZZ", "ZZZ"},
	{"M", "M"}, {"D", "d"}, {"d", "c"}, {"H", "H"}, {"h", "h"},
	{"m", "m"}, {"s", "s"}, {"A", "a"}, {"a", "a"}, {"Z", "ZZ"}, {"X", "X"},
}

// momentToLuxon translates n8n v1's moment tokens to the Luxon tokens this
// node's `format` speaks. Only the tokens templates actually use are mapped;
// anything else crosses unchanged and renders literally on both sides.
//
// One pass over the layout, longest token first, and a replacement is never
// rescanned. It used to run the tokens as a sequence of ReplaceAll passes, and
// the output of one pass stayed matchable by the next: `DD` became `dd` and
// then `cc` — the ISO weekday — and `ZZ` became `ZZZZ`. A format was therefore
// silently translated into a different one, and `YYYY-MM-DD` rendered the
// weekday where the day of the month belongs: a wrong date rather than an
// error, which is the worst thing an import can produce.
func momentToLuxon(layout string) string {
	var translated strings.Builder
	translated.Grow(len(layout))
	for index := 0; index < len(layout); {
		matched := false
		for _, token := range momentTokens {
			if strings.HasPrefix(layout[index:], token.from) {
				translated.WriteString(token.to)
				index += len(token.from)
				matched = true
				break
			}
		}
		if !matched {
			translated.WriteByte(layout[index])
			index++
		}
	}
	return translated.String()
}

func dateTimeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"operation":       defaultString(stringParameter(node.Parameters, "operation"), "getCurrentDate"),
		"outputFieldName": defaultString(stringParameter(node.Parameters, "outputField"), "date"),
	}
	if value, present := node.Parameters["date"]; present {
		parameters["magnitude"] = toN8NValue(value)
	}
	if value, present := node.Parameters["endDate"]; present {
		parameters["endDate"] = toN8NValue(value)
	}
	if value, present := node.Parameters["duration"]; present {
		parameters["duration"] = value
	}
	if unit := stringParameter(node.Parameters, "unit"); unit != "" {
		parameters["timeUnit"] = unit
	}
	if part := stringParameter(node.Parameters, "part"); part != "" {
		parameters["part"] = part
	}
	if mode := stringParameter(node.Parameters, "roundMode"); mode != "" {
		parameters["mode"] = mode
	}
	if to := stringParameter(node.Parameters, "roundTo"); to != "" {
		parameters["to"] = to
	}
	if layout := stringParameter(node.Parameters, "format"); layout != "" {
		parameters["customFormat"] = layout
	}
	if zone := stringParameter(node.Parameters, "timezone"); zone != "" {
		parameters["options"] = map[string]any{"timezone": zone}
	}
	return parameters, nil
}

// waitDefaults are n8n's own Wait defaults for a node version.
//
// n8n omits a parameter that still holds its default, so an export usually says
// nothing about the amount or the unit — and the two versions disagree about
// both. v1 waits one hour; v1.1 changed the default to five seconds, which is
// what every "pause for a moment" node in the corpus relies on. Reading either
// through one hard-coded pair turned "wait three seconds" into three hours and
// an omitted amount into no pause at all.
func waitDefaults(version float64) (float64, string) {
	if version >= 1.1 {
		return 5, "seconds"
	}
	return 1, "hours"
}

// waitUnits are the units n8n's Wait accepts, and therefore the ones this
// server must know.
var waitUnits = map[string]bool{"seconds": true, "minutes": true, "hours": true, "days": true}

// readableAsNumber reports whether a converted n8n value is something the
// executor can read as a number: a number, an expression, or numeric text.
func readableAsNumber(value any) bool {
	switch typed := value.(type) {
	case float64, int, int64, json.Number:
		return true
	case string:
		_, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return err == nil
	case map[string]any:
		// An expression marker. Whether it resolves to a number is only
		// knowable per item, at run time.
		mode, _ := typed["mode"].(string)
		return mode == "expression"
	default:
		return false
	}
}

// waitToKilas maps n8n's Wait node, refusing the two durable resume modes.
func waitToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	resume := defaultString(stringParameter(node.Parameters, "resume"), "timeInterval")
	parameters := map[string]any{"resume": resume}

	switch resume {
	case "timeInterval":
		defaultAmount, defaultUnit := waitDefaults(node.TypeVersion)
		// Converted, not copied: an amount written as `={{ $json.w }}` is a
		// string, and a string that reached the executor's number reader was
		// read as zero — a rate-limit pause that silently did not happen.
		amount := fromN8NValue(node.Parameters["amount"])
		if amount == nil {
			amount = defaultAmount
		} else if !readableAsNumber(amount) {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "amount",
				Reason: fmt.Sprintf("this Wait's amount %q is neither a number nor an expression this "+
					"server can read, so the pause would be zero; set a fixed amount or an expression",
					textOf(node.Parameters["amount"])),
			})
		}
		parameters["amount"] = amount

		unit := fromN8NValue(node.Parameters["unit"])
		if unit == nil {
			unit = defaultUnit
		} else if text, isText := unit.(string); isText && !waitUnits[strings.TrimSpace(text)] {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "unit",
				Reason: fmt.Sprintf("the n8n wait unit %q is not one this server knows (seconds, minutes, "+
					"hours, days); the pause would be wrong, so set the unit explicitly", text),
			})
		}
		parameters["unit"] = unit
	case "specificTime":
		parameters["dateTime"] = fromN8NValue(node.Parameters["dateTime"])
	case "webhook":
		// Carried as itself: the execution parks in storage and wakes on the
		// resume URL, which is what n8n's mode means.
	case "form":
		// Also carried, but not as an arbitrary form: this server's form mode
		// is the approval page, so an n8n form with fields of its own renders
		// as approve/deny. Named rather than refused — the wait works, the
		// page is different, and a user needs to know which.
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "resume",
			Reason: "n8n's form wait resumes on a form the workflow defines; this server resumes on its " +
				"approval page, where the run is approved or denied. Any fields the n8n form collected " +
				"are not part of that page.",
		})
	default:
		issues = append(issues, Unsupported{Field: "resume", Reason: fmt.Sprintf(
			"the n8n Wait resume mode %q has no equivalent; the node was imported as a time interval", resume)})
		parameters["resume"] = "timeInterval"
	}

	// n8n's own bound on how long the wait may last. Carried under the names
	// the executor reads, and only when the source actually set it: n8n omits
	// a parameter holding its default, and the default is "no limit".
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		carried := map[string]any{}
		if limit, _ := options["limitWaitTime"].(bool); limit {
			carried["limitWaitTime"] = true
			if kind := stringParameter(options, "limitType"); kind != "" {
				carried["limitType"] = kind
			}
			if amount, ok := numberParameter(options, "limitAmount"); ok {
				carried["limitAmount"] = amount
			}
			if unit := stringParameter(options, "limitUnit"); unit != "" {
				carried["limitUnit"] = unit
			}
			if at, present := options["limitAt"]; present && at != nil {
				carried["limitAt"] = fromN8NValue(at)
			}
		}
		for key := range options {
			switch key {
			case "limitWaitTime", "limitType", "limitAmount", "limitUnit", "limitAt":
			default:
				issues = append(issues, Unsupported{
					Severity: SeverityDropped, Field: "options." + key,
					Reason: fmt.Sprintf("the n8n Wait option %q has no KilasFlow equivalent and was not carried", key),
				})
			}
		}
		if len(carried) > 0 {
			for key, value := range carried {
				parameters[key] = value
			}
		}
	}
	return parameters, issues
}

func waitToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"resume": defaultString(stringParameter(node.Parameters, "resume"), "timeInterval"),
	}
	if value, present := node.Parameters["amount"]; present {
		// Converted, not copied: the amount may be an expression marker, and
		// n8n reads a fixed value there — writing the marker object back
		// exported a node whose amount was a JSON object.
		parameters["amount"] = toN8NValue(value)
	}
	if value, present := node.Parameters["unit"]; present {
		parameters["unit"] = toN8NValue(value)
	}
	if value, present := node.Parameters["dateTime"]; present {
		parameters["dateTime"] = toN8NValue(value)
	}
	// The wait's own limit goes back where n8n keeps it. Written only when the
	// document has it, because n8n's default is "no limit" and writing the
	// default back would be inventing a setting.
	if limit, _ := node.Parameters["limitWaitTime"].(bool); limit {
		options := map[string]any{"limitWaitTime": true}
		for _, key := range []string{"limitType", "limitAmount", "limitUnit"} {
			if value, present := node.Parameters[key]; present {
				options[key] = value
			}
		}
		if value, present := node.Parameters["limitAt"]; present {
			options["limitAt"] = toN8NValue(value)
		}
		parameters["options"] = options
	}
	return parameters, nil
}

// --- Code -------------------------------------------------------------------

// unsupportedScript is the one refusal every JavaScript escape hatch produces.
//
// One function, so a Code node, a Sort comparator and whatever comes next all
// say the same thing in the same words and carry the same severity. Three
// wordings for one situation is how a user concludes the three are different
// problems.
//
// Blocking, always. A script this server cannot run is not a detail that was
// lost in translation; it is work the workflow was relying on that will not
// happen, and a workflow that activates without it produces plausible output
// with a hole in it.
func unsupportedScript(field, language, alternative string) Unsupported {
	return Unsupported{
		Severity: SeverityBlocking, Field: field,
		Reason: fmt.Sprintf("this node's code is written in %s, which this server does not run. %s",
			language, alternative),
	}
}

// codeToKilas keeps an imported Code node's source rather than discarding it.
//
// The alternative — translating JavaScript to Go — is a compiler project with
// no correct stopping point, and the failure mode is the worst one available:
// a body that translates into Go which compiles and computes something else.
// So the node is refused, and refused *well*: the source is kept and visible,
// the language is kept, and the diagnostic names the native node that most
// likely replaces it.
func codeToKilas(node Node) (map[string]any, []Unsupported) {
	language := stringParameter(node.Parameters, "language")
	if language == "" {
		language = "javaScript"
	}
	source := stringParameter(node.Parameters, "jsCode")
	if source == "" {
		source = stringParameter(node.Parameters, "pythonCode")
	}
	suggestion := nodes.SuggestReplacement(source)

	parameters := map[string]any{
		"language":    language,
		"mode":        defaultString(stringParameter(node.Parameters, "mode"), "runOnceForAllItems"),
		"replacement": suggestion,
	}
	if value, present := node.Parameters["jsCode"]; present {
		parameters["jsCode"] = fromN8NValue(value)
	}
	if value, present := node.Parameters["pythonCode"]; present {
		parameters["pythonCode"] = fromN8NValue(value)
	}
	return parameters, []Unsupported{unsupportedScript("jsCode", codeLanguageName(language), suggestion)}
}

func codeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"language": defaultString(stringParameter(node.Parameters, "language"), "javaScript"),
		"mode":     defaultString(stringParameter(node.Parameters, "mode"), "runOnceForAllItems"),
	}
	// The source goes back exactly as it arrived, which is the point of having
	// kept it: a round trip through this server must not cost a user their code.
	for _, key := range []string{"jsCode", "pythonCode"} {
		if value, present := node.Parameters[key]; present {
			parameters[key] = toN8NValue(value)
		}
	}
	return parameters, nil
}

func codeLanguageName(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python", "pythonNative", "pythonnative":
		return "Python"
	case "javascript", "":
		return "JavaScript"
	default:
		return language
	}
}

// --- SQL --------------------------------------------------------------------

// postgresToKilas maps n8n's whole PostgreSQL operation set.
//
// Every operation but executeQuery used to return `{"operation": "query"}` with
// no statement at all, so an imported insert arrived as an empty query — a node
// that activated, ran, and did nothing. The six values are n8n's own, and this
// node's are the same six, so the operation carries across as itself.
// mysqlToKilas maps n8n's MySQL node, which has the same six operations and no
// schema.
//
// A thin wrapper rather than a copy: the two nodes differ in the schema field
// and the default operation, and copying two hundred lines to express that is
// how the second copy stops matching the first.
func mysqlToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, issues := postgresToKilas(node)
	// MySQL has no schema separate from a database, so "public" — which the
	// PostgreSQL default supplies — would address a database literally called
	// public if it were carried across.
	delete(parameters, "schema")
	if parameters["operation"] == "executeQuery" && node.Parameters["operation"] == nil {
		// n8n's MySQL node defaults to insert where its PostgreSQL node
		// defaults to executeQuery, so a node that names no operation gets the
		// one n8n would have given it.
		parameters["operation"] = "insert"
	}
	return parameters, issues
}

func mysqlToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters, lossy := postgresToN8N(node)
	delete(parameters, "schema")
	return parameters, lossy
}

func postgresToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	// n8n's own default is insert, and an export omits a parameter still
	// holding its default — so a node whose author never opened the dropdown
	// carries no `operation` at all. Reading that as executeQuery produced a
	// node with no query, a discarded column mapping and a blocking "an execute
	// query needs a query" at activation, for a node that was written to insert
	// rows. The MySQL translator already patches the same default.
	operation := defaultString(stringParameter(node.Parameters, "operation"), "insert")
	parameters := map[string]any{"operation": operation}

	switch operation {
	case "executeQuery":
		parameters["query"] = fromN8NValue(node.Parameters["query"])
	case "deleteTable", "insert", "upsert", "select", "update":
		parameters["schema"] = locatorFromN8N(node.Parameters["schema"], "public")
		parameters["table"] = locatorFromN8N(node.Parameters["table"], "")
		if operation == "deleteTable" {
			// Written whether or not n8n stored it, and this is the whole
			// point. n8n's own default is "truncate" and this node's is
			// "delete"; a node whose author never opened the dropdown carries
			// no key at all, so letting the absence cross the boundary means
			// each side reads its own default and an "empty this table"
			// becomes a row delete. Never let a default cross.
			parameters["deleteCommand"] = defaultString(
				stringParameter(node.Parameters, "deleteCommand"), sqlbuild.DeleteTruncate)
			if restart, present := node.Parameters["restartSequences"]; present {
				parameters["restartSequences"] = restart
			}
		}
		if columns, ok := node.Parameters["columns"].(map[string]any); ok {
			// The mapper's stored shape is n8n's, so it carries across whole —
			// including the schema copy, which is what makes the export of an
			// imported node lossless.
			parameters["columns"] = columns
		}
		if where, ok := node.Parameters["where"].(map[string]any); ok {
			converted, whereIssues := postgresWhereToKilas(where)
			parameters["where"] = converted
			issues = append(issues, whereIssues...)
		}
		if combine := stringParameter(node.Parameters, "combineConditions"); combine != "" {
			parameters["combineConditions"] = strings.ToUpper(combine)
		}
		if sorting, ok := node.Parameters["sort"].(map[string]any); ok {
			// The stored shape is n8n's, so it carries across whole. Dropping
			// it was silent and changed the answer: an imported
			// "ORDER BY created_at DESC LIMIT 50" became an arbitrary fifty
			// rows, which looks like data rather than like a defect.
			parameters["sort"] = sorting
		}
		if returnAll, present := node.Parameters["returnAll"]; present {
			parameters["returnAll"] = returnAll
		}
		if limit, ok := numberParameter(node.Parameters, "limit"); ok {
			parameters["limit"] = limit
		}
	default:
		issues = append(issues, Unsupported{Field: "operation", Reason: fmt.Sprintf(
			"the n8n PostgreSQL operation %q has no equivalent; the node was imported as an execute query", operation)})
		parameters["operation"] = "executeQuery"
	}

	if options, ok := node.Parameters["options"].(map[string]any); ok {
		carried, bound, optionIssues := sqlOptionsToKilas(options)
		if len(carried) > 0 {
			parameters["options"] = carried
		}
		if bound != nil {
			// Translated rather than kept in both places. n8n binds from
			// options.queryReplacement and this node binds from
			// queryParameters, so a stored copy of the first would go stale
			// the moment somebody edited the second — and the export derives
			// it back, so nothing is lost by moving it.
			parameters["queryParameters"] = bound
		}
		issues = append(issues, optionIssues...)
	}
	return parameters, issues
}

// sqlOptionsToKilas carries the options collection across.
//
// Filtered to the set the node declares rather than passed through: the node's
// own validator refuses a key this server does not know, so passing a newer
// n8n's option straight through would turn an import into a workflow that
// cannot be saved. What is dropped is named.
func sqlOptionsToKilas(options map[string]any) (map[string]any, any, []Unsupported) {
	declared := map[string]bool{}
	for _, key := range nodes.DeclaredSQLOptions() {
		declared[key] = true
	}

	issues := make([]Unsupported, 0)
	carried := make(map[string]any, len(options))
	unknown := make([]string, 0)
	for key, value := range options {
		switch {
		case key == "queryReplacement":
			// Handled below, and deliberately not carried: keeping it here as
			// well would leave two copies of one thing to disagree.
		case declared[key]:
			carried[key] = value
		default:
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		issues = append(issues, Unsupported{Field: "options", Severity: SeverityDropped, Reason: fmt.Sprintf(
			"these n8n options have no equivalent and were dropped: %s", strings.Join(unknown, ", "))})
	}

	bound, replacementIssues := queryReplacementToKilas(options["queryReplacement"])
	issues = append(issues, replacementIssues...)
	return carried, bound, issues
}

// queryReplacementToKilas turns n8n's replacement list into bound values.
//
// n8n stores them as one comma-separated string and splits on the comma at run
// time, which means a value containing a comma is two values over there and
// there is nothing in the stored document that could say otherwise. So a split
// that changes the count is reported rather than guessed at: binding the wrong
// number of values would run a different query, silently.
func queryReplacementToKilas(value any) (any, []Unsupported) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case []any:
		// Already a list, which newer n8n versions accept. Nothing to split.
		return typed, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		// Split, filter, then trim — n8n's own stringToArray, in that order.
		// The order is observable: it drops an empty entry from "a,,b" but
		// keeps a whitespace-only one from "a, ,b", which trims to the empty
		// string and binds as one. Reproduced rather than tidied, because the
		// point is to bind what n8n would have bound.
		parts := strings.Split(typed, ",")
		bound := make([]any, 0, len(parts))
		for _, part := range parts {
			if part == "" {
				continue
			}
			bound = append(bound, strings.TrimSpace(part))
		}
		if len(bound) == 0 {
			return nil, nil
		}
		return bound, []Unsupported{{Field: "options.queryReplacement", Severity: SeverityLossy, Reason: fmt.Sprintf(
			"n8n's query replacements were split on the comma into %d bound values, which is what n8n "+
				"itself does with them — its format has no escape, so a value containing a comma was "+
				"already two values there and is two here", len(bound))}}
	default:
		return nil, []Unsupported{{Field: "options.queryReplacement", Severity: SeverityDropped, Reason: fmt.Sprintf(
			"n8n query replacements of type %T were not imported; set Query Parameters to a JSON array", value)}}
	}
}

// locatorFromN8N carries a resource locator across, or builds one from a bare
// string — which is the shape older n8n versions stored.
func locatorFromN8N(value any, fallback string) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		mode, _ := typed["mode"].(string)
		if mode != "name" && mode != "list" {
			mode = "name"
		}
		return property.WriteLocator(property.Locator{Mode: mode, Value: fromN8NValue(typed["value"])})
	case nil:
		if fallback == "" {
			return nil
		}
		return property.WriteLocator(property.Locator{Mode: "name", Value: fallback})
	default:
		return property.WriteLocator(property.Locator{Mode: "name", Value: fromN8NValue(typed)})
	}
}

// postgresWhereToKilas maps n8n's WHERE builder rows onto the condition rows
// this node's control writes.
func postgresWhereToKilas(where map[string]any) ([]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	entries, _ := where["values"].([]any)
	rows := make([]any, 0, len(entries))
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		// An absent condition is n8n's own default, which is equality. Only a
		// condition that is present and unrecognised is worth a diagnostic:
		// reporting the default as lossy fills the list with rows where nothing
		// was lost, and a diagnostic list nobody reads is the same as none.
		condition := stringParameter(row, "condition")
		operator, mapped := postgresConditionOperators[condition]
		if !mapped {
			if condition != "" {
				issues = append(issues, Unsupported{Field: "where", Reason: fmt.Sprintf(
					"the n8n condition %q has no equivalent; that row was imported as an equality", condition)})
			}
			operator = "equals"
		}
		rows = append(rows, map[string]any{
			"field":    fromN8NValue(row["column"]),
			"operator": operator,
			"value":    fromN8NValue(row["value"]),
		})
	}
	return rows, issues
}

// postgresConditionOperators is n8n's WHERE vocabulary mapped onto this one.
//
// The null pair is the one worth reading twice. In this product's shared
// condition vocabulary `exists` means the value is present — see
// internal/conditions, and the isNotEmpty mapping above — so n8n's `IS NULL` is
// `notExists` and its `IS NOT NULL` is `exists`. Written the other way round,
// an imported `WHERE col IS NULL` builds `WHERE col IS NOT NULL` and a delete
// removes the exact complement of the rows it was meant to.
//
// A round-trip test cannot catch that: n8nConditionName is the literal inverse
// of this map, so an inversion here is undone symmetrically on the way out and
// the exported workflow matches the imported one. The test that catches it
// asserts the SQL the builder emits.
var postgresConditionOperators = map[string]string{
	"equal": "equals", "!=": "notEquals", "LIKE": "like", "ILIKE": "ilike",
	">": "gt", ">=": "gte", "<": "lt", "<=": "lte",
	"IS NULL": "notExists", "IS NOT NULL": "exists",
}

// postgresToN8N writes the operation set back out.
//
// The values are the same on both sides, so this is a copy rather than a
// translation — which is what makes an imported node round-trip unchanged.
func postgresToN8N(node workflow.Node) (map[string]any, []Lossy) {
	// Written explicitly, and defaulted the same way the import does: the two
	// sides default this key differently, so an absent key means "insert" in
	// n8n and something else here. Never let a default cross.
	operation := defaultString(stringParameter(node.Parameters, "operation"), "insert")
	options, lossy := sqlOptionsToN8N(node.Parameters)
	parameters := map[string]any{"operation": operation, "options": options}
	if operation == "executeQuery" {
		parameters["query"] = toN8NValue(node.Parameters["query"])
		return parameters, lossy
	}
	for _, key := range []string{"schema", "table"} {
		locator, ok := property.ReadLocator(node.Parameters[key])
		if !ok {
			continue
		}
		parameters[key] = map[string]any{
			property.LocatorSentinel: true,
			"mode":                   defaultString(locator.Mode, "name"),
			"value":                  toN8NValue(locator.Value),
		}
	}
	for _, key := range []string{"columns", "returnAll", "limit", "combineConditions", "sort", "restartSequences"} {
		if value, present := node.Parameters[key]; present {
			parameters[key] = value
		}
	}
	if operation == "deleteTable" {
		// Always written, for the reason the importer always writes it: the
		// two systems default this key differently, so an absent key means
		// "empty the whole table" over there and "delete matching rows" here.
		// Exporting the absence would hand n8n a TRUNCATE nobody asked for.
		parameters["deleteCommand"] = defaultString(
			stringParameter(node.Parameters, "deleteCommand"), sqlbuild.DeleteRows)
	}
	if rows, ok := node.Parameters["where"].([]any); ok {
		values := make([]any, 0, len(rows))
		for _, entry := range rows {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			values = append(values, map[string]any{
				"column":    toN8NValue(row["field"]),
				"condition": n8nConditionName(stringParameter(row, "operator")),
				"value":     toN8NValue(row["value"]),
			})
		}
		parameters["where"] = map[string]any{"values": values}
	}
	return parameters, lossy
}

// sqlOptionsToN8N writes the options collection back.
//
// The stored collection carries across whole, and queryReplacement is derived
// from the bound Query Parameters rather than stored alongside them. Derived,
// because n8n binds from the option and this node binds from the field: a
// stored copy would go stale the moment somebody edited the field here, and
// n8n would then run the query with the older values without saying so.
func sqlOptionsToN8N(parameters map[string]any) (map[string]any, []Lossy) {
	options := map[string]any{}
	if stored, ok := parameters["options"].(map[string]any); ok {
		for key, value := range stored {
			options[key] = value
		}
	}
	replacement, lossy := queryReplacementToN8N(parameters["queryParameters"])
	if replacement != "" {
		options["queryReplacement"] = replacement
	}
	return options, lossy
}

// queryReplacementToN8N joins bound values the way n8n stores them.
//
// A value containing a comma cannot survive this, because n8n's own format has
// no way to escape one — it splits on every comma at run time. Such a value is
// left out rather than joined into a string that would silently become two
// values on arrival; the export names it as lossy.
func queryReplacementToN8N(value any) (string, []Lossy) {
	var bound []any
	switch typed := value.(type) {
	case []any:
		bound = typed
	case string:
		// The editor stores this field as the JSON text somebody typed, so a
		// string here is the ordinary case rather than the exception.
		if strings.TrimSpace(typed) == "" {
			return "", nil
		}
		if err := json.Unmarshal([]byte(typed), &bound); err != nil {
			return "", []Lossy{{Field: "queryParameters", Severity: SeverityDropped, Reason: "the query " +
				"parameters are not a JSON array, so they could not be written as n8n query replacements"}}
		}
	default:
		return "", nil
	}
	parts := make([]string, 0, len(bound))
	for _, entry := range bound {
		text := fmt.Sprintf("%v", entry)
		if strings.Contains(text, ",") {
			// n8n splits its replacement string on every comma and has no
			// escape, so a value containing one would arrive as two. Left out
			// entirely rather than joined into something that would run a
			// different query without saying so.
			return "", []Lossy{{Field: "queryParameters", Severity: SeverityDropped, Reason: "a bound value " +
				"contains a comma, which n8n's query replacements cannot carry — they are split on every " +
				"comma and have no escape — so the replacements were left out and the query will run unbound"}}
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, ","), nil
}

// n8nConditionName is the inverse of postgresConditionOperators.
func n8nConditionName(operator string) string {
	for name, mapped := range postgresConditionOperators {
		if mapped == operator {
			return name
		}
	}
	return "equal"
}

// --- helpers ----------------------------------------------------------------

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// stickyToKilas carries an annotation across unchanged.
//
// Every field is geometry or presentation, so there is nothing to translate and
// nothing that can be unsupported: a note that arrives with a colour KilasFlow
// draws differently is still the same note. Absent fields are left absent so
// the node definition's defaults apply, rather than being pinned to n8n's.
func stickyToKilas(node Node) (map[string]any, []Unsupported) {
	parameters := map[string]any{}
	if content := stringParameter(node.Parameters, "content"); content != "" {
		parameters["content"] = content
	}
	for _, key := range []string{"width", "height", "color"} {
		if value, ok := numberParameter(node.Parameters, key); ok {
			parameters[key] = value
		}
	}
	return parameters, nil
}

// stickyToN8N is the exact inverse, so an annotation round-trips byte for byte
// in the fields n8n reads.
func stickyToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{}
	if content := stringParameter(node.Parameters, "content"); content != "" {
		parameters["content"] = content
	}
	for _, key := range []string{"width", "height", "color"} {
		if value, ok := numberParameter(node.Parameters, key); ok {
			parameters[key] = value
		}
	}
	return parameters, nil
}

// packToKilas carries a generated pack's parameters through unchanged.
//
// A pack node's parameter *names* are the ones the package it mirrors chose —
// that is the whole point of generating from the same document — so there is
// nothing to rename. What does change is the expression dialect: n8n marks an
// expression with a leading `=` on a plain string, KilasFlow with an explicit
// marker, and a template that omitted a parameter to rely on its default has
// nothing here at all and picks the pack's own default up at run time.
func packToKilas(node Node) (map[string]any, []Unsupported) {
	if len(node.Parameters) == 0 {
		return nil, nil
	}
	converted := make(map[string]any, len(node.Parameters))
	for key, value := range node.Parameters {
		converted[key] = fromN8NValue(value)
	}
	return converted, nil
}

// packToN8N carries them back out, restoring the `=` prefix.
func packToN8N(node workflow.Node) (map[string]any, []Lossy) {
	if len(node.Parameters) == 0 {
		return nil, nil
	}
	converted := make(map[string]any, len(node.Parameters))
	for key, value := range node.Parameters {
		converted[key] = toN8NValue(value)
	}
	return converted, nil
}

// telegramToN8N writes the pack's parameters back into n8n's shape.
//
// The Additional Fields go back under the collection n8n reads, in its own
// snake_case names; a pack key with no n8n equivalent is named rather than
// dropped quietly, because the alternative is a round trip that looks clean and
// sends different messages.
func telegramToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters, _ := packToN8N(node)
	if parameters == nil {
		parameters = map[string]any{}
	}
	additional := map[string]any{}
	for n8nKey, packKey := range telegramAdditionalFieldKeys {
		if value, present := node.Parameters[packKey]; present && value != nil && value != "" {
			additional[n8nKey] = toN8NValue(value)
			delete(parameters, packKey)
		}
	}
	if len(additional) > 0 {
		parameters["additionalFields"] = additional
	}
	return parameters, nil
}

// telegramOperations mirrors the operations packs/telegram declares, resource
// by resource.
//
// Mirrored rather than imported for the same reason the node-type constants
// are: the adapter must not depend on the node pack. TestTelegramOperationsMatch
// ThePack keeps the two in step, so a pack that gains an operation and a
// mapping that forgets it cannot drift apart silently.
var telegramOperations = map[string][]string{
	"message": {"sendMessage", "sendPhoto", "sendDocument", "sendAnimation", "sendAudio", "sendVideo",
		"sendSticker", "sendMediaGroup", "sendLocation", "sendChatAction", "editMessageText",
		"deleteMessage", "pinChatMessage", "unpinChatMessage"},
	"chat":     {"get", "administrators", "member", "leave", "setTitle", "setDescription"},
	"callback": {"answerQuery", "answerInlineQuery"},
	"file":     {"get"},
}

// telegramAdditionalFieldKeys maps n8n's Additional Fields members onto the
// pack's parameter names.
//
// n8n stores per-send options under `additionalFields` with the Bot API's own
// snake_case names, and the pack reads them at the top level in camel case.
// Copying the collection through verbatim meant HTML formatting was silently
// dropped and messages arrived with raw tags.
var telegramAdditionalFieldKeys = map[string]string{
	"parse_mode":           "parseMode",
	"caption":              "caption",
	"disable_notification": "disableNotification",
	"reply_to_message_id":  "replyToMessageId",
}

// telegramToKilas maps n8n's Telegram node onto the pack.
//
// Two things the verbatim copy got wrong, and both of them changed what the
// workflow did: an operation the pack does not have activated and then failed
// at run time, and every Additional Field was dropped because the two nodes
// keep them in different places.
func telegramToKilas(node Node) (map[string]any, []Unsupported) {
	converted, _ := packToKilas(node)
	issues := make([]Unsupported, 0)
	if converted == nil {
		converted = map[string]any{}
	}

	resource := stringParameter(node.Parameters, "resource")
	operation := stringParameter(node.Parameters, "operation")
	if resource == "" {
		resource = "message"
	}
	if operation != "" && !telegramOperationKnown(resource, operation) {
		// Blocking, and it blocks *before* the node can activate: n8n's
		// sendAndWait and its rich-message operations have no pack request, so
		// the node would activate and then fail on its first run.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "operation",
			Reason: fmt.Sprintf("the n8n Telegram operation %q on resource %q has no equivalent in this "+
				"server's Telegram node; replace the node or rebuild the call as an HTTP Request before "+
				"activating", operation, resource),
		})
	}

	if additional, ok := node.Parameters["additionalFields"].(map[string]any); ok {
		for _, key := range sortedKeys(additional) {
			if value := additional[key]; value != nil && value != "" {
				if mapped, known := telegramAdditionalFieldKeys[key]; known {
					converted[mapped] = fromN8NValue(value)
					continue
				}
			}
			if additional[key] == nil {
				continue
			}
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "additionalFields." + key,
				Reason: fmt.Sprintf("the n8n Telegram option %q has no equivalent in this server's Telegram "+
					"node and was not carried", key),
			})
		}
	}

	// The binary attachment: n8n keeps the property name at the top level and
	// the pack reads it under the same name, but only on the operations that
	// upload, so an empty one is left absent.
	if propertyName := stringParameter(node.Parameters, "binaryPropertyName"); propertyName != "" {
		converted["binaryPropertyName"] = propertyName
	}
	if _, present := node.Parameters["binaryData"]; present {
		converted["binaryData"] = node.Parameters["binaryData"]
	}

	// The reply markup: n8n selects a keyboard *type* and keeps each type's
	// rows in its own collection, while the pack takes the Bot API's JSON.
	if markup, _ := node.Parameters["replyMarkup"].(string); markup != "" {
		switch markup {
		case "inlineKeyboard":
			if keyboard := inlineKeyboardJSON(node.Parameters["inlineKeyboard"]); keyboard != nil {
				converted["replyMarkup"] = keyboard
			}
		case "keyboard":
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "replyMarkup",
				Reason: "this node sent a custom reply keyboard, which this server's Telegram node does not " +
					"build; send the keyboard as the Bot API's JSON in Reply Markup instead",
			})
		}
	}
	if _, present := node.Parameters["appendAttribution"]; present {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "appendAttribution",
			Reason: "n8n appends an attribution line to the message; this server's Telegram node does not, " +
				"and the option was not carried",
		})
	}
	return converted, issues
}

// TelegramOperationKnown reports whether the pack declares a resource/operation
// pair. Exported so the mirror above can be pinned against the pack itself.
func TelegramOperationKnown(resource, operation string) bool {
	return telegramOperationKnown(resource, operation)
}

// telegramOperationKnown reports whether the pack declares a resource/operation
// pair.
func telegramOperationKnown(resource, operation string) bool {
	for _, candidate := range telegramOperations[strings.ToLower(resource)] {
		if candidate == operation {
			return true
		}
	}
	return false
}

// inlineKeyboardJSON turns n8n's inline keyboard collection into the Bot API's
// JSON.
//
// n8n's shape is a list of rows, each holding a list of buttons, each with a
// text and its own additional fields; the Bot API wants an array of arrays of
// button objects. The two are the same information in different shapes, and
// the pack's Reply Markup takes the Bot API's.
func inlineKeyboardJSON(value any) map[string]any {
	collection, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	rows, _ := collection["rows"].([]any)
	keyboard := make([][]any, 0, len(rows))
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		buttons, _ := row["row"].(map[string]any)
		list, _ := buttons["buttons"].([]any)
		converted := make([]any, 0, len(list))
		for _, item := range list {
			button, _ := item.(map[string]any)
			built := map[string]any{}
			if text, present := button["text"]; present {
				built["text"] = fromN8NValue(text)
			}
			if extra, ok := button["additionalFields"].(map[string]any); ok {
				for key, extraValue := range extra {
					if extraValue != nil {
						built[key] = fromN8NValue(extraValue)
					}
				}
			}
			if len(built) > 0 {
				converted = append(converted, built)
			}
		}
		if len(converted) > 0 {
			keyboard = append(keyboard, converted)
		}
	}
	if len(keyboard) == 0 {
		return nil
	}
	return map[string]any{"inline_keyboard": keyboard}
}

// telegramTriggerToKilas carries the trigger's parameters and names the ones
// KilasFlow does not have.
//
// n8n's Telegram trigger stores its Additional Fields under the same key this
// one does, so the shape carries straight through. What does not carry is
// reported rather than dropped: a parameter that silently disappears leaves a
// workflow that looks identical to the one it came from and behaves differently.
func telegramTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	converted, _ := packToKilas(node)
	var issues []Unsupported

	additional, _ := node.Parameters["additionalFields"].(map[string]any)
	for key, reason := range map[string]string{
		"restrictToChatIds": "KilasFlow names this Restrict to Chat IDs and reads it from `additionalFields.chatIds`",
		"restrictToUserIds": "KilasFlow names this Restrict to User IDs and reads it from `additionalFields.userIds`",
	} {
		if _, present := additional[key]; present {
			issues = append(issues, Unsupported{Field: "additionalFields." + key, Reason: reason})
		}
	}
	return converted, issues
}

// splitInBatchesToKilas maps n8n's Loop Over Items onto the bounded loop.
//
// The versions do not agree about what the node's outputs are. v3 split one
// batch output into `done` (0) and `loop` (1); v1/v2 have a single output that
// emits each batch, and the loop ends when an If node reads
// `$node["…"].context["noItemsLeft"]`. The port itself is remapped where the
// ports are resolved (see legacyOutputPort), and the node is flagged here
// because the *exit* is a graph the user has to rewire, not a parameter that
// can be translated.
func splitInBatchesToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	converted := map[string]any{}
	if size, ok := numberParameter(node.Parameters, "batchSize"); ok {
		converted["batchSize"] = size
	}
	// n8n's loop has no iteration bound: it runs until its items are exhausted.
	// This server's fails rather than truncating, so the bound is written
	// explicitly at the instance-wide ceiling — an imported loop that would
	// legitimately run a few hundred batches must not inherit a lower default
	// and die halfway, which reads as a defect in the data rather than a
	// limit nobody chose.
	converted["maxIterations"] = float64(workflow.MaxLoopIterations)
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if reset, present := options["reset"]; present && reset != nil {
			// Carried, so a round trip returns the node as it was authored,
			// and still reported: n8n's `reset` restarts a running loop
			// mid-flight from an expression, and KilasFlow's loop runs its
			// batches once and stops. Keeping the value is honest; pretending
			// it does something would not be.
			converted["reset"] = fromN8NValue(reset)
			issues = append(issues, Unsupported{
				Field: "options.reset",
				Reason: "n8n's loop reset restarts a running loop from an expression; the value is carried so a " +
					"round trip returns the node as it was authored, but KilasFlow's loop runs its batches once " +
					"and does not restart on it",
			})
		}
	}
	if node.TypeVersion < 3 {
		// Blocking rather than lossy: the batch body keeps running, but the
		// branch the loop used to leave on never fires — a truncated workflow
		// that reports success is worse than one that refuses to start.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "typeVersion",
			Reason: fmt.Sprintf("this cycle is an n8n Split In Batches v%g, whose single output emits each "+
				"batch and whose loop ends on an If node reading $node[…].context[\"noItemsLeft\"]. "+
				"KilasFlow's loop ends by itself: the batch output was wired to `loop`, so reconnect this "+
				"node's `done` output to whatever the If's true branch reached, and delete the noItemsLeft "+
				"test — the items collected by the body leave on `done` once the batches run out",
				node.TypeVersion),
		})
	}
	return converted, issues
}

func splitInBatchesToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{"options": map[string]any{}}
	if size, present := node.Parameters["batchSize"]; present {
		written["batchSize"] = size
	}
	if reset, present := node.Parameters["reset"]; present {
		written["options"] = map[string]any{"reset": toN8NValue(reset)}
	}
	// maxIterations is KilasFlow's own bound and has no n8n equivalent; n8n
	// loops until the items run out. The ceiling this importer writes itself
	// carries no information n8n lacks, so it goes quietly; a bound somebody
	// lowered is named, because n8n will run past it.
	if max, present := node.Parameters["maxIterations"]; present {
		if numberSetting(max) != float64(workflow.MaxLoopIterations) {
			return written, []Lossy{{
				Field:  "maxIterations",
				Reason: "KilasFlow's iteration bound has no n8n equivalent; the exported loop runs until its items are exhausted",
			}}
		}
	}
	return written, nil
}

// --- Data shaping -------------------------------------------------------------

// namedList reads n8n's `{values: [...]}` fixed collection into names.
func namedList(value any, key string) []string {
	wrapper, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	entries, _ := wrapper["values"].([]any)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		row, _ := entry.(map[string]any)
		if name, _ := row[key].(string); strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

func aggregateToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	converted := map[string]any{
		"aggregate": defaultString(stringParameter(node.Parameters, "aggregate"), "aggregateIndividualFields"),
	}
	// n8n's key is `fieldToAggregate` (singular), not `values`: a fixed
	// collection read under the wrong key imports empty and then fails
	// activation with "needs at least one field name" — with no issue
	// saying why.
	if wrapper, ok := node.Parameters["fieldsToAggregate"].(map[string]any); ok {
		entries, _ := wrapper["fieldToAggregate"].([]any)
		if len(entries) == 0 {
			entries, _ = wrapper["values"].([]any)
		}
		fields := make([]string, 0, len(entries))
		outputs := make([]string, 0, len(entries))
		for _, entry := range entries {
			row, _ := entry.(map[string]any)
			if row == nil {
				continue
			}
			name, _ := row["fieldToAggregate"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			fields = append(fields, strings.TrimSpace(name))
			// A renamed field is stored as `source>output` beside the plain
			// list, so the executor can name its output without a second
			// parameter shape.
			renamed := strings.TrimSpace(stringParameter(row, "outputFieldName"))
			if renamed == "" {
				renamed = strings.TrimSpace(stringParameter(row, "renameField"))
			}
			if renamed != "" {
				outputs = append(outputs, strings.TrimSpace(name)+">"+renamed)
			}
		}
		if len(fields) == 0 && len(wrapper) > 0 {
			issues = append(issues, Unsupported{
				Field:  "fieldsToAggregate",
				Reason: "this Aggregate lists fields in a shape the importer could not read; add the field names before running the workflow",
			})
		}
		if len(fields) > 0 {
			converted["fieldsToAggregate"] = strings.Join(fields, ",")
		}
		if len(outputs) > 0 {
			converted["outputFieldNames"] = strings.Join(outputs, ",")
		}
	}
	if include := stringParameter(node.Parameters, "include"); include == "specifiedFields" {
		converted["include"] = "specifiedFields"
		if fields := namedList(node.Parameters["fieldsToInclude"], "field"); len(fields) > 0 {
			converted["fieldsToInclude"] = strings.Join(fields, ",")
		} else if fields := stringParameter(node.Parameters, "fieldsToInclude"); fields != "" {
			converted["fieldsToInclude"] = fields
		}
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		for _, key := range []string{"mergeLists", "keepMissing", "disableDotNotation"} {
			if value, present := options[key]; present && value != nil {
				if carried, ok := converted["options"].(map[string]any); ok {
					carried[key] = value
				} else {
					converted["options"] = map[string]any{key: value}
				}
			}
		}
	}
	if name := stringParameter(node.Parameters, "destinationFieldName"); name != "" {
		converted["destinationFieldName"] = name
	}
	return converted, issues
}

func aggregateToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"aggregate": defaultString(stringParameter(node.Parameters, "aggregate"), "aggregateIndividualFields"),
		"options":   map[string]any{},
	}
	values := make([]any, 0, 2)
	renames := map[string]string{}
	for _, pair := range strings.Split(stringParameter(node.Parameters, "outputFieldNames"), ",") {
		if source, output, found := strings.Cut(pair, ">"); found {
			renames[strings.TrimSpace(source)] = strings.TrimSpace(output)
		}
	}
	for _, field := range strings.Split(stringParameter(node.Parameters, "fieldsToAggregate"), ",") {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			row := map[string]any{"fieldToAggregate": trimmed}
			if renamed, ok := renames[trimmed]; ok && renamed != "" {
				row["outputFieldName"] = renamed
			}
			values = append(values, row)
		}
	}
	// n8n's key is `fieldToAggregate`: writing `values` produces a node n8n
	// opens with "No fields specified".
	written["fieldsToAggregate"] = map[string]any{"fieldToAggregate": values}
	if name := stringParameter(node.Parameters, "destinationFieldName"); name != "" {
		written["destinationFieldName"] = name
	}
	if include := stringParameter(node.Parameters, "include"); include != "" {
		written["include"] = include
		if fields := stringParameter(node.Parameters, "fieldsToInclude"); fields != "" {
			written["fieldsToInclude"] = fields
		}
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		written["options"] = options
	}
	return written, nil
}

func splitOutToKilas(node Node) (map[string]any, []Unsupported) {
	converted := map[string]any{
		"fieldToSplitOut": stringParameter(node.Parameters, "fieldToSplitOut"),
		"include":         defaultString(stringParameter(node.Parameters, "include"), "noOtherFields"),
	}
	if fields := stringParameter(node.Parameters, "fieldsToInclude"); fields != "" {
		converted["fieldsToInclude"] = fields
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if name, _ := options["destinationFieldName"].(string); name != "" {
			converted["destinationFieldName"] = name
		}
	}
	return converted, nil
}

func splitOutToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"fieldToSplitOut": stringParameter(node.Parameters, "fieldToSplitOut"),
		"include":         defaultString(stringParameter(node.Parameters, "include"), "noOtherFields"),
		"options":         map[string]any{},
	}
	if fields := stringParameter(node.Parameters, "fieldsToInclude"); fields != "" {
		written["fieldsToInclude"] = fields
	}
	if name := stringParameter(node.Parameters, "destinationFieldName"); name != "" {
		written["options"] = map[string]any{"destinationFieldName": name}
	}
	return written, nil
}

// sortToKilas carries a field sort and refuses a JavaScript comparator.
//
// n8n's third mode is a JS comparator, which this product has no runtime for.
// Approximating it would sort by something the author did not write, so it is
// named instead — through unsupportedScript, which is the one refusal every
// JavaScript escape hatch in this importer produces.
func sortToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	mode := defaultString(stringParameter(node.Parameters, "type"), "simple")
	if mode == "code" {
		return map[string]any{"type": "simple"}, append(issues, unsupportedScript("type", "JavaScript",
			"Set the fields to sort by on this node before running the workflow."))
	}

	converted := map[string]any{"type": mode}
	keys := make([]string, 0, 2)
	// n8n's key is `sortFieldsUi` with a lower-case i. Reading only the
	// camel-cased `sortFieldsUI` imports an empty sort that then fails
	// activation — with no issue saying why.
	wrapper, _ := node.Parameters["sortFieldsUi"].(map[string]any)
	if wrapper == nil {
		wrapper, _ = node.Parameters["sortFieldsUI"].(map[string]any)
	}
	entries, _ := wrapper["sortField"].([]any)
	for _, entry := range entries {
		row, _ := entry.(map[string]any)
		name, _ := row["fieldName"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		if order, _ := row["order"].(string); order == "descending" {
			name += ":desc"
		}
		keys = append(keys, name)
	}
	if len(keys) > 0 {
		converted["sortFieldsUI"] = strings.Join(keys, ",")
	} else if wrapper != nil {
		issues = append(issues, Unsupported{
			Field:  "sortFieldsUi",
			Reason: "this Sort lists fields in a shape the importer could not read; add the field names before running the workflow",
		})
	}
	return converted, issues
}

func sortToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"type":    defaultString(stringParameter(node.Parameters, "type"), "simple"),
		"options": map[string]any{},
	}
	entries := make([]any, 0, 2)
	for _, key := range strings.Split(stringParameter(node.Parameters, "sortFieldsUI"), ",") {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		name, order := trimmed, "ascending"
		if field, suffix, found := strings.Cut(trimmed, ":"); found {
			name = strings.TrimSpace(field)
			if strings.EqualFold(strings.TrimSpace(suffix), "desc") {
				order = "descending"
			}
		}
		entries = append(entries, map[string]any{"fieldName": name, "order": order})
	}
	// n8n's key is `sortFieldsUi` with a lower-case i: writing `sortFieldsUI`
	// produces a node n8n opens with "No sorting specified".
	written["sortFieldsUi"] = map[string]any{"sortField": entries}
	return written, nil
}

func summarizeToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	converted := map[string]any{}
	if wrapper, ok := node.Parameters["fieldsToSummarize"].(map[string]any); ok {
		entries, _ := wrapper["values"].([]any)
		columns := make([]any, 0, len(entries))
		for _, entry := range entries {
			row, _ := entry.(map[string]any)
			if row == nil {
				continue
			}
			field, _ := row["field"].(string)
			if field == "" {
				continue
			}
			columns = append(columns, map[string]any{
				"aggregation": defaultString(stringText(row["aggregation"]), "count"),
				"field":       field,
			})
		}
		converted["fieldsToSummarize"] = columns
	}
	if fields := namedList(node.Parameters["fieldsToSplitBy"], "fieldName"); len(fields) > 0 {
		converted["fieldsToSplitBy"] = strings.Join(fields, ",")
	} else if fields := stringParameter(node.Parameters, "fieldsToSplitBy"); fields != "" {
		converted["fieldsToSplitBy"] = fields
	}
	// The concatenate separator: n8n's default is a bare comma, and the
	// author may set a custom one. Dropped, every concatenation joins with
	// ", " instead.
	if separator := stringParameter(node.Parameters, "separateBy"); separator != "" {
		converted["separator"] = separator
	} else if separator := stringParameter(node.Parameters, "customSeparator"); separator != "" {
		converted["separator"] = separator
	}
	if outputFormat := stringParameter(node.Parameters, "outputFormat"); outputFormat != "" {
		converted["outputFormat"] = outputFormat
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		for key, value := range options {
			if _, carried := converted[key]; !carried && value != nil {
				if carriedOptions, ok := converted["options"].(map[string]any); ok {
					carriedOptions[key] = value
				} else {
					converted["options"] = map[string]any{key: value}
				}
			}
		}
		if separateBy, _ := options["separateBy"].(string); separateBy != "" {
			converted["separator"] = separateBy
		}
		if custom, _ := options["customSeparator"].(string); custom != "" {
			converted["separator"] = custom
		}
	}
	return converted, issues
}

func summarizeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	columns, _ := node.Parameters["fieldsToSummarize"].([]any)
	values := make([]any, 0, len(columns))
	for _, entry := range columns {
		row, _ := entry.(map[string]any)
		if row == nil {
			continue
		}
		values = append(values, map[string]any{"aggregation": row["aggregation"], "field": row["field"]})
	}
	written := map[string]any{
		"fieldsToSummarize": map[string]any{"values": values},
		"options":           map[string]any{},
	}
	if fields := stringParameter(node.Parameters, "fieldsToSplitBy"); fields != "" {
		written["fieldsToSplitBy"] = fields
	}
	if separator := stringParameter(node.Parameters, "separator"); separator != "" {
		written["options"] = map[string]any{"separateBy": separator}
	}
	return written, nil
}

// removeDuplicatesToKilas carries the local operation and names the durable one.
func removeDuplicatesToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	operation := defaultString(stringParameter(node.Parameters, "operation"), "removeDuplicateInputItems")
	if operation != "removeDuplicateInputItems" {
		return map[string]any{"operation": "removeDuplicateInputItems"}, append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "operation",
			Reason: "this node removed items seen in previous executions, which needs durable per-workflow state KilasFlow does not have yet; it was imported as removing duplicates within one run",
		})
	}

	converted := map[string]any{"operation": operation}
	if compare := stringParameter(node.Parameters, "compare"); compare != "" {
		converted["compare"] = compare
	}
	for _, key := range []string{"fieldsToExclude", "fieldsToCompare"} {
		if fields := stringParameter(node.Parameters, key); fields != "" {
			converted[key] = fields
		}
	}
	return converted, issues
}

func removeDuplicatesToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"operation": defaultString(stringParameter(node.Parameters, "operation"), "removeDuplicateInputItems"),
		"options":   map[string]any{},
	}
	for _, key := range []string{"compare", "fieldsToExclude", "fieldsToCompare"} {
		if value := stringParameter(node.Parameters, key); value != "" {
			written[key] = value
		}
	}
	return written, nil
}

func stringText(value any) string {
	text, _ := value.(string)
	return text
}

// --- The LangChain cluster --------------------------------------------------
//
// n8n models an AI agent as a cluster: a root node with sub-nodes attached on
// typed channels. The direction is worth naming, because the picture and the
// JSON disagree. In a stored n8n workflow the *sub-node* is the source key of
// the connection and the root agent is the target — the chat model connects to
// the agent, not the other way round — which is the same direction
// workflow.Connection uses. So these are type and parameter mappings only, with
// no rewiring; a translator written from the drawing rather than from the JSON
// would reverse them and produce a graph that compiles and never delivers a
// descriptor to the agent at all.
//
// Every parameter name, option value string and default below is n8n's own,
// transcribed into testdata/n8n_cluster_nodes.json with the file it was read
// from and the date. The reference checkout is never a build input.

// refuseNonToolsAgent names why a pre-1.82 agent selector value other than
// the Tools Agent must not import as this server's AI Agent.
//
// n8n removed the selector in 1.82 and only the Tools Agent remains, so an
// absent key is a Tools Agent by construction. Anything else naming another
// agent would silently run as one if carried across.
func refuseNonToolsAgent(node Node) string {
	agent := stringParameter(node.Parameters, "agent")
	switch agent {
	case "", "toolsAgent":
		return ""
	default:
		return fmt.Sprintf("this node uses the %q agent, which KilasFlow does not implement. Only the Tools Agent imports: the node was kept as a placeholder and cannot run until it is replaced or rebuilt as a Tools Agent.", agent)
	}
}

// agentToKilas maps n8n's Tools Agent onto this server's AI Agent.
func agentToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	// n8n splits the user turn in two: `promptType` names where it comes from
	// and `text` carries it. `text` holds the value in every mode — under
	// "auto" it holds the chatInput expression n8n defaults it to — so the
	// prompt is `text` whenever there is one.
	if text := node.Parameters["text"]; text != nil && text != "" {
		parameters["prompt"] = fromN8NValue(text)
	} else {
		// A field left at its default is not stored, so n8n's default has to be
		// materialised here. An empty prompt is a required parameter this
		// server refuses to compile, which would turn "the author never touched
		// this field" into an import that cannot be activated.
		parameters["prompt"] = expressionValue("{{ $json.chatInput }}")
	}
	if promptType := stringParameter(node.Parameters, "promptType"); promptType == "guardrails" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "promptType",
			Reason: "this agent took its prompt from a connected Guardrails node, which this server has no equivalent for. " +
				"The expression was carried across so the prompt is visible, but it will not resolve until the prompt is rewritten.",
		})
	}

	options, _ := node.Parameters["options"].(map[string]any)
	if system := options["systemMessage"]; system != nil && system != "" {
		parameters["systemMessage"] = fromN8NValue(system)
	}
	if iterations, ok := numberParameter(options, "maxIterations"); ok {
		parameters["maxIterations"] = iterations
	}
	// The toggles this server's agent carries under the same names.
	for _, key := range []string{"returnIntermediateSteps", "passthroughBinaryImages", "enableStreaming"} {
		if value, present := options[key]; present && value != nil {
			parameters[key] = value
		}
	}
	// Batching is accepted into neither document: this server runs items in
	// order, so a batch size n8n ran in parallel would silently become
	// sequential. Reported as dropped rather than carried.
	if batching, present := options["batching"]; present && batching != nil {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "options.batching",
			Reason: "this agent processed items in parallel batches, which this server does not implement. " +
				"The batching options were dropped and items run in order.",
		})
	}

	// Both of these are sub-node slots this server does not have. Reported
	// rather than ignored: an agent that silently stops parsing its output, or
	// silently loses its fallback model, still answers — it just answers
	// differently from the workflow that was imported.
	if parser, _ := node.Parameters["hasOutputParser"].(bool); parser {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "hasOutputParser",
			Reason: "this agent parsed its answer through a connected output parser sub-node, which imports as its own output-parser node. " +
				"The agent was imported without it and returns the model's text unparsed until the parser is wired back up.",
		})
	}
	if fallback, _ := node.Parameters["needsFallback"].(bool); fallback {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "needsFallback",
			Reason: "this agent had a fallback chat model for when the first one fails, and this server's agent takes exactly one model. " +
				"Only the primary model was carried.",
		})
	}
	return parameters, issues
}

// agentToN8N writes the agent back.
func agentToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	options := map[string]any{}
	// systemMessage first, with the legacy alias behind it for documents
	// imported while the importer still wrote systemPrompt.
	system := node.Parameters["systemMessage"]
	if system == nil || system == "" {
		system = node.Parameters["systemPrompt"]
	}
	if system != nil && system != "" {
		options["systemMessage"] = toN8NValue(system)
	}
	if iterations, ok := numberParameter(node.Parameters, "maxIterations"); ok {
		options["maxIterations"] = iterations
	}
	for _, key := range []string{"returnIntermediateSteps", "passthroughBinaryImages", "enableStreaming"} {
		if value, present := node.Parameters[key]; present && value != nil {
			options[key] = value
		}
	}
	return map[string]any{
		// "define" rather than "auto": the prompt is written on this node, and
		// telling n8n to take it from a connected chat trigger instead would
		// discard the text being exported.
		"promptType": "define",
		"text":       toN8NValue(node.Parameters["prompt"]),
		"options":    options,
	}, lossy
}

// chainToKilas maps n8n's Basic LLM Chain onto this server's. The prompt
// surface crosses almost verbatim — promptType, text and the messageValues
// rows share names and shapes on both sides — so the translator mostly
// reports what has no equivalent.
func chainToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	if promptType := stringParameter(node.Parameters, "promptType"); promptType != "" {
		parameters["promptType"] = promptType
	}
	if text := node.Parameters["text"]; text != nil && text != "" {
		parameters["text"] = fromN8NValue(text)
	}
	if carried, rowIssues := chainMessagesToKilas(node.Parameters["messages"]); carried != nil || len(rowIssues) > 0 {
		if carried != nil {
			parameters["messages"] = carried
		}
		issues = append(issues, rowIssues...)
	}
	// n8n's chain versions below 1.4 keep the user prompt in a single `prompt`
	// field, and the importer read only `text` — so an imported chain lost its
	// question and ran on its system message alone.
	if _, present := node.Parameters["text"]; !present {
		if prompt, ok := node.Parameters["prompt"]; ok && prompt != nil && prompt != "" {
			parameters["promptType"] = "define"
			parameters["text"] = fromN8NValue(prompt)
		}
	}
	// A top-level batching collection n8n runs in parallel from version 1.7.
	// Dropped, with the same reasoning as the agent's: items run in order.
	if batching, present := node.Parameters["batching"]; present && batching != nil {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "batching",
			Reason: "this chain processed items in parallel batches, which this server does not implement. " +
				"The batching options were dropped and items run in order.",
		})
	}
	if parser, _ := node.Parameters["hasOutputParser"].(bool); parser {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "hasOutputParser",
			Reason: "this chain parsed its answer through a connected output parser sub-node, which imports as its own output-parser node. " +
				"The chain was imported without it and returns the model's text unparsed until the parser is wired back up.",
		})
	}
	if fallback, _ := node.Parameters["needsFallback"].(bool); fallback {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "needsFallback",
			Reason: "this chain had a fallback chat model for when the first one fails, and this server's chain takes exactly one model. " +
				"Only the primary model was carried.",
		})
	}
	return parameters, issues
}

// chainToN8N writes the chain back.
func chainToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{}
	if promptType := stringParameter(node.Parameters, "promptType"); promptType != "" {
		parameters["promptType"] = promptType
	}
	if text := node.Parameters["text"]; text != nil && text != "" {
		parameters["text"] = toN8NValue(text)
	}
	if messages := chainMessagesToN8N(node.Parameters["messages"]); messages != nil {
		parameters["messages"] = messages
	}
	// n8n's auto mode always reads chatInput; a renamed field cannot be
	// expressed there.
	if field := stringParameter(node.Parameters, "inputField"); field != "" && field != "chatInput" {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "inputField",
			Reason: fmt.Sprintf(
				"this chain read its prompt from field %q. n8n's automatic prompt always reads chatInput, "+
					"so the exported chain reads chatInput instead.", field),
		})
	}
	return parameters, lossy
}

// chainMessagesToKilas carries a chain's messageValues rows, converting the
// text of each row the way any other expression-valued parameter converts.
// Image rows have no text equivalent here: they are dropped and named
// rather than carried as empty rows the chain would refuse to compile.
func chainMessagesToKilas(value any) (any, []Unsupported) {
	collection, ok := value.(map[string]any)
	if !ok || collection == nil {
		return nil, nil
	}
	raw, present := collection["messageValues"]
	if !present {
		return nil, nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	issues := make([]Unsupported, 0)
	carried := make([]any, 0, len(rows))
	for index, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if messageType := stringParameter(fields, "messageType"); messageType != "" && messageType != "text" {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: fmt.Sprintf("messages.messageValues[%d]", index),
				Reason: "this prompt row carried an image, which this server's chain does not implement. " +
					"The row was dropped and the chain runs on its text rows.",
			})
			continue
		}
		role, _ := fields["type"].(string)
		if strings.TrimSpace(role) == "" {
			// n8n omits the type when it holds its default, which is the
			// system prompt.
			role = "SystemMessagePromptTemplate"
		}
		mapped, known := kilasChainRoles[role]
		if !known {
			issues = append(issues, Unsupported{
				Field: fmt.Sprintf("messages.messageValues[%d].type", index),
				Reason: fmt.Sprintf("the prompt row's role %q is not one this server knows (system, human, "+
					"ai); the row was imported as a system message", role),
			})
			mapped = "system"
		}
		carried = append(carried, map[string]any{
			"type":    mapped,
			"message": fromN8NValue(fields["message"]),
		})
	}
	return map[string]any{"messageValues": carried}, issues
}

// kilasChainRoles translates n8n's prompt-template class names to the role
// names this server's chain accepts.
//
// The two vocabularies are the same idea spelled differently: n8n stores the
// LangChain class name, this server stores the role. Copying the class name
// through verbatim imported a chain that could not activate — "message 1 has
// type HumanMessagePromptTemplate, want system, human, or ai" — which is a
// failure that names a shape rather than the mapping nobody wrote.
var kilasChainRoles = map[string]string{
	"SystemMessagePromptTemplate": "system",
	"HumanMessagePromptTemplate":  "human",
	"AIMessagePromptTemplate":     "ai",
}

// n8nChainRoles is the inverse, for export.
var n8nChainRoles = map[string]string{
	"system": "SystemMessagePromptTemplate",
	"human":  "HumanMessagePromptTemplate",
	"ai":     "AIMessagePromptTemplate",
}

// chainMessagesToN8N writes the rows back, re-adding n8n's expression prefix.
func chainMessagesToN8N(value any) any {
	collection, ok := value.(map[string]any)
	if !ok || collection == nil {
		return nil
	}
	raw, present := collection["messageValues"]
	if !present {
		return nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return nil
	}
	carried := make([]any, 0, len(rows))
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			continue
		}
		role, _ := fields["type"].(string)
		if mapped, known := n8nChainRoles[role]; known {
			role = mapped
		}
		carried = append(carried, map[string]any{
			"type":    role,
			"message": toN8NValue(fields["message"]),
		})
	}
	return map[string]any{"messageValues": carried}
}

// n8nChatModelOptionKeys are the sampling and transport options both servers
// carry under the same names. They are n8n's names on this side too — see the
// option key constants in nodes/ai.go — so the collection maps across as a
// whole rather than key by key.
var n8nChatModelOptionKeys = []string{
	"frequencyPenalty", "maxTokens", "maxRetries",
	"presencePenalty", "temperature", "timeout", "topP",
}

// openAICompatibleModelToKilas maps a provider's chat-model node onto this
// server's OpenAI-compatible model.
//
// The providers below publish an OpenAI-compatible endpoint, so the node is a
// model name, a base URL and the same sampling options — and the mapping costs
// one table entry rather than a provider implementation. The base URL is
// written explicitly because n8n keeps it in the credential, which never
// imports: a deployment pointed at a gateway, or at a local Ollama, has to be
// told where to point here, and the diagnostic says so.
func openAICompatibleModelToKilas(node Node, defaultBaseURL string, modelField string) (map[string]any, []Unsupported) {
	parameters, issues := chatModelToKilas(node, "")
	if modelField != "" {
		if value, present := node.Parameters[modelField]; present && value != nil && value != "" {
			parameters["model"] = fromN8NValue(value)
		}
	}
	parameters["baseUrl"] = defaultBaseURL
	return parameters, append(issues, Unsupported{
		Severity: SeverityLossy, Field: "credentials",
		Reason: fmt.Sprintf("this model's endpoint came from an n8n credential, which does not import; "+
			"the base URL was set to %s — point it at your deployment before running the workflow",
			defaultBaseURL),
	})
}

// ollamaModelToKilas maps n8n's Ollama chat model, whose endpoint is the
// local server's OpenAI-compatible path rather than a vendor API.
func ollamaModelToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, issues := openAICompatibleModelToKilas(node, "http://localhost:11434/v1", "model")
	// n8n's Ollama node names the token budget `numPredict`; the shared option
	// vocabulary names it `maxTokens`.
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if predict, ok := numberParameter(options, "numPredict"); ok {
			carried, _ := parameters["options"].(map[string]any)
			if carried == nil {
				carried = map[string]any{}
			}
			carried["maxTokens"] = predict
			parameters["options"] = carried
		}
	}
	return parameters, issues
}

// toolCalculatorToKilas maps n8n's Calculator tool.
//
// It has no parameters the model does not supply: the expression arrives in the
// tool call, so only the model-facing description crosses.
func toolCalculatorToKilas(node Node) (map[string]any, []Unsupported) {
	parameters := map[string]any{
		"toolName": nodes.NormalizeToolName(node.Name),
	}
	if description := node.Parameters["description"]; description != nil && description != "" {
		parameters["toolDescription"] = fromN8NValue(description)
	} else {
		parameters["toolDescription"] = "Evaluates an arithmetic expression."
	}
	return parameters, nil
}

func toolCalculatorToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return map[string]any{
		"description": toN8NValue(node.Parameters["toolDescription"]),
	}, nil
}

// mcpClientToolToKilas maps n8n's MCP Client tool onto this server's.
//
// n8n names the endpoint `endpointUrl` at the versions that speak streamable
// HTTP and `sseEndpoint` at the ones that speak SSE. Both carry: this server's
// client speaks the streamable transport, so an SSE-only endpoint is named as a
// lossy note rather than silently requested with the wrong protocol.
func mcpClientToolToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{
		"toolName": nodes.NormalizeToolName(node.Name),
	}
	if description := node.Parameters["description"]; description != nil && description != "" {
		parameters["toolDescription"] = fromN8NValue(description)
	} else {
		parameters["toolDescription"] = "Calls the MCP server " + node.Name + "."
	}
	endpoint := stringParameter(node.Parameters, "endpointUrl")
	if endpoint == "" {
		endpoint = stringParameter(node.Parameters, "sseEndpoint")
		if endpoint != "" {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "sseEndpoint",
				Reason: "this tool pointed at an MCP server's SSE endpoint; this server's client speaks " +
					"the streamable HTTP transport, so point the URL at the server's /mcp endpoint",
			})
		}
	}
	if endpoint == "" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "endpointUrl",
			Reason: "this MCP tool named no server endpoint, so it imports with nothing to call; " +
				"set the Server URL before activating.",
		})
	} else {
		parameters["serverUrl"] = fromN8NValue(endpoint)
	}

	switch include := stringParameter(node.Parameters, "toolsToInclude"); include {
	case "selected":
		names := stringList(node.Parameters["includeTools"])
		if len(names) == 0 {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "includeTools",
				Reason: "this tool was set to expose only the tools it lists, and the list is empty; " +
					"name the tools to expose before activating.",
			})
		}
		parameters["tools"] = strings.Join(names, ",")
	case "allExcept":
		names := stringList(node.Parameters["excludeTools"])
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "excludeTools",
			Reason: fmt.Sprintf("this tool exposed every server tool except %s; this server's MCP tool "+
				"names the tools to expose rather than the ones to hide, so every tool is exposed — "+
				"list the ones you want in Tools to expose", strings.Join(names, ", ")),
		})
	}
	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" && authentication != "none" {
		issues = append(issues, Unsupported{
			Reason: "this MCP tool authenticated with an n8n credential. Credentials are not imported; " +
				"attach a KilasFlow HTTP credential before running the workflow.",
		})
	}
	return parameters, issues
}

func mcpClientToolToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"description":    toN8NValue(node.Parameters["toolDescription"]),
		"endpointUrl":    toN8NValue(node.Parameters["serverUrl"]),
		"toolsToInclude": "all",
	}
	if names := stringParameter(node.Parameters, "tools"); names != "" {
		parameters["toolsToInclude"] = "selected"
		parameters["includeTools"] = splitListAny(names)
	}
	return parameters, nil
}

// stringList reads n8n's array-of-strings parameter, which a hand-written
// document may hold as a comma-separated string.
func stringList(value any) []string {
	switch typed := value.(type) {
	case []any:
		names := make([]string, 0, len(typed))
		for _, entry := range typed {
			if name := strings.TrimSpace(textOf(entry)); name != "" {
				names = append(names, name)
			}
		}
		return names
	case string:
		return splitList(typed)
	default:
		return nil
	}
}

// splitList splits a comma-separated list, dropping empty entries.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// splitListAny is splitList for a parameter the exporter writes as JSON.
func splitListAny(value string) []any {
	names := splitList(value)
	entries := make([]any, 0, len(names))
	for _, name := range names {
		entries = append(entries, name)
	}
	return entries
}

func openAIModelToKilas(node Node) (map[string]any, []Unsupported) {
	return chatModelToKilas(node, "gpt-5-mini")
}

func openRouterModelToKilas(node Node) (map[string]any, []Unsupported) {
	return chatModelToKilas(node, "openai/gpt-4.1-mini")
}

func chatModelToKilas(node Node, defaultModel string) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{"model": modelLocator(node.Parameters["model"], defaultModel)}

	options, _ := node.Parameters["options"].(map[string]any)
	carried := map[string]any{}
	for _, key := range n8nChatModelOptionKeys {
		if value, ok := numberParameter(options, key); ok {
			carried[key] = value
		}
	}
	if len(carried) > 0 {
		parameters["options"] = carried
	}
	// n8n carried the address inside the options collection on its OpenAI node
	// below typeVersion 1.1. Here it is top level, because the model-list loader
	// reads parameters and cannot see a collection member.
	if base := stringParameter(options, "baseURL"); base != "" {
		parameters["baseUrl"] = base
	}
	if format := stringParameter(options, "responseFormat"); format != "" && format != "text" {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "options.responseFormat",
			Reason: fmt.Sprintf(
				"this model was asked for %s output. This server has no JSON-mode plumbing through its model request yet, "+
					"so the option was dropped and the model answers in text; ask for the format in the prompt instead.", format),
		})
	}
	return parameters, issues
}

// modelLocator normalises n8n's two model shapes into the resource locator this
// server stores.
//
// n8n's OpenAI node writes a locator from typeVersion 1.2 upwards and a bare
// string below it, and its OpenRouter node writes a bare string at every
// published version, so both shapes appear in real documents and both have to
// be read.
func modelLocator(value any, fallback string) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		mode, _ := typed["mode"].(string)
		if mode == "" {
			mode = "id"
		}
		if name, _ := typed["value"].(string); name != "" {
			return map[string]any{property.LocatorSentinel: true, "mode": mode, "value": name}
		}
	case string:
		if typed != "" {
			// "id" rather than "list": a name read out of a document is a name
			// somebody typed, and claiming it was chosen from a catalogue this
			// server has not fetched would show a selection it cannot verify.
			return map[string]any{property.LocatorSentinel: true, "mode": "id", "value": typed}
		}
	}
	return map[string]any{property.LocatorSentinel: true, "mode": "list", "value": fallback}
}

// locatorName reads a model name back out of whichever shape it is stored in.
func locatorName(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		name, _ := typed["value"].(string)
		return name
	case string:
		return typed
	}
	return ""
}

func openAIModelToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return chatModelToN8N(node, true, "https://api.openai.com/v1")
}

func openRouterModelToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return chatModelToN8N(node, false, "https://openrouter.ai/api/v1")
}

func chatModelToN8N(node workflow.Node, asLocator bool, defaultBaseURL string) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	name := locatorName(node.Parameters["model"])
	parameters := map[string]any{}
	if asLocator {
		mode := "id"
		if stored, ok := node.Parameters["model"].(map[string]any); ok {
			if declared, _ := stored["mode"].(string); declared != "" {
				mode = declared
			}
		}
		parameters["model"] = map[string]any{property.LocatorSentinel: true, "mode": mode, "value": name}
	} else {
		// n8n's OpenRouter node stores a bare string at every published
		// version, so writing a locator here would produce a document its own
		// editor could not read.
		parameters["model"] = name
	}

	options := map[string]any{}
	if stored, ok := node.Parameters["options"].(map[string]any); ok {
		for _, key := range n8nChatModelOptionKeys {
			if value, ok := numberParameter(stored, key); ok {
				options[key] = value
			}
		}
	}
	parameters["options"] = options

	// n8n hides the base URL on these nodes at the versions exported here, so a
	// deployment pointed at a gateway goes back out pointed at the vendor.
	// Named rather than dropped quietly: the exported workflow would otherwise
	// send its traffic somewhere the author did not choose.
	if base := stringParameter(node.Parameters, "baseUrl"); base != "" && base != defaultBaseURL {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "baseUrl",
			Reason: fmt.Sprintf(
				"this model called %s. n8n's chat model nodes have no visible base URL at the exported version, "+
					"so the export addresses %s instead.", base, defaultBaseURL),
		})
	}
	return parameters, lossy
}

// defaultContextWindowLength is n8n's own default for a Buffer Window Memory.
//
// n8n omits a parameter still holding its default, so a memory node authored
// without touching the field carries no key at all — and the absence has to be
// read as this, not as "remember nothing".
const defaultContextWindowLength = 5

// memoryToKilas maps n8n's Buffer Window Memory onto this server's memory.
func memoryToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	// The modes cross under their own names, so the scoping rule travels
	// with the key: a fromInput key stays node-scoped here exactly as it
	// was there, instead of collapsing into one shared conversation under
	// the legacy sessionId.
	if stringParameter(node.Parameters, "sessionIdType") == "customKey" {
		parameters["sessionIdType"] = "customKey"
		parameters["sessionKey"] = fromN8NValue(node.Parameters["sessionKey"])
	} else if key := node.Parameters["sessionKey"]; key != nil && key != "" {
		parameters["sessionIdType"] = "fromInput"
		parameters["sessionKey"] = fromN8NValue(key)
	} else {
		// "fromInput", and every n8n version below 1.2, which had no selector
		// at all. The default is materialised for the same reason the agent's
		// prompt is: sessionKey is required here, and a field the author never
		// edited was never stored.
		parameters["sessionIdType"] = "fromInput"
		parameters["sessionKey"] = expressionValue("{{ $json.sessionId }}")
	}

	// n8n counts *interactions* — one human turn and one AI turn — where this
	// server counts messages, so the window is doubled rather than copied. A
	// contextWindowLength of 10 means ten exchanges, and importing it as ten
	// messages silently halved every conversation an imported agent could
	// remember. The default is materialised for the same reason the session key
	// is: n8n omits a parameter still holding its default, and this node's own
	// default is a different number of turns.
	length, ok := numberParameter(node.Parameters, "contextWindowLength")
	if !ok {
		length = defaultContextWindowLength
	}
	parameters["maxMessages"] = length * 2
	return parameters, issues
}

func memoryToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	mode := stringParameter(node.Parameters, "sessionIdType")
	key := node.Parameters["sessionKey"]
	if mode != "fromInput" && mode != "customKey" {
		// A legacy sessionId is written on this node. "customKey" rather
		// than "fromInput": the session is written on this node, and
		// fromInput would have n8n read it from a connected chat trigger's
		// payload instead — silently loading a different conversation.
		mode = "customKey"
		key = node.Parameters["sessionId"]
	}
	parameters := map[string]any{
		"sessionIdType": mode,
		"sessionKey":    toN8NValue(key),
	}
	// Halved, the exact inverse of the import: this server counts messages and
	// n8n counts interactions.
	if length, ok := numberParameter(node.Parameters, "maxMessages"); ok {
		parameters["contextWindowLength"] = length / 2
	}
	return parameters, lossy
}

// httpToolToKilas maps n8n's HTTP Request Tool onto this server's, reusing the
// HTTP Request translation the two nodes share.
func httpToolToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, issues := httpToKilas(node)

	// n8n has no tool-name parameter at all: the name a model calls is derived
	// from the node's own canvas name. Deriving it the same way is what keeps an
	// imported system prompt that named the tool still naming the same tool.
	parameters["toolName"] = nodes.NormalizeToolName(node.Name)

	description := node.Parameters["toolDescription"]
	if description == nil || description == "" {
		// Required here, and a model given no description cannot know when to
		// call the tool. Filling the gap visibly beats failing compilation with
		// nothing for the author to look at.
		parameters["toolDescription"] = "Calls " + node.Name + "."
	} else {
		parameters["toolDescription"] = fromN8NValue(description)
	}

	// n8n lets the model fill {placeholder} segments of the URL, declared in a
	// placeholderDefinitions collection. This server's tool takes its arguments
	// as an expression over the model's JSON instead, so a placeholder URL would
	// be requested literally.
	if placeholders, ok := node.Parameters["placeholderDefinitions"].(map[string]any); ok && len(placeholders) > 0 {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "placeholderDefinitions",
			Reason: "this tool let the model fill {placeholder} segments of its request. " +
				"This server's tool reads the model's arguments through an expression such as {{ $json.city }}, " +
				"so rewrite the URL that way before activating.",
		})
	}
	return parameters, issues
}

func httpToolToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters, lossy := httpToN8N(node)
	// n8n's tool node has no options collection of its own.
	delete(parameters, "options")
	parameters["toolDescription"] = toN8NValue(node.Parameters["toolDescription"])

	// The name the model calls is the node's canvas name in n8n and a parameter
	// here, so a tool renamed away from its node name cannot be expressed.
	if name := stringParameter(node.Parameters, "toolName"); name != "" && name != nodes.NormalizeToolName(node.Name) {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "toolName",
			Reason: fmt.Sprintf(
				"the model called this tool %q. n8n derives a tool's name from the node's own name, "+
					"so after export it is called %q; rename the node to keep the old name.", name, nodes.NormalizeToolName(node.Name)),
		})
	}
	return parameters, lossy
}

// --- Workflow Tool and Structured Output Parser --------------------------------
//
// The last two LangChain mappings, after the agent, the chain, the chat
// models, the memory and the HTTP tool. The workflow tool wraps another
// workflow as a callable tool; the structured parser forces model output
// into a JSON shape. Both are sub-nodes: the tool emits ai_tool, the parser
// emits ai_outputParser, and neither takes a main-channel input.

// workflowToolToKilas maps n8n's Call n8n Workflow Tool onto this server's
// workflow tool. The name and description cross directly; the workflow
// reference and its inputs cross when they name a stored workflow.
func workflowToolToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	// Unlike the HTTP tool this node names itself: `name` is an explicit
	// parameter, so an author-set name is honoured and only a blank one is
	// derived from the canvas name the way the HTTP tool always is.
	if name := node.Parameters["name"]; name != nil && name != "" {
		parameters["toolName"] = fromN8NValue(name)
	} else {
		parameters["toolName"] = nodes.NormalizeToolName(node.Name)
	}
	if description := node.Parameters["description"]; description != nil && description != "" {
		parameters["toolDescription"] = fromN8NValue(description)
	} else {
		// Required here, and a model given no description cannot know when
		// to call the tool. Same defaulting as the HTTP tool.
		parameters["toolDescription"] = "Calls " + node.Name + "."
	}

	// n8n's default when the author never touched the selector.
	source := stringParameter(node.Parameters, "source")
	if source == "" {
		source = "database"
	}
	switch source {
	case "database":
		if n8nSelfWorkflowReference(node.Parameters["workflowId"]) {
			// `{{ $workflow.id }}` means "call this workflow", which n8n
			// resolves at run time — a recursive agent is the usual reason.
			// It is carried as the same expression, which this server's
			// evaluator resolves to the running workflow's own ID, so the
			// self-reference keeps working instead of blocking activation.
			parameters["workflowId"] = expressionValue(n8nSelfWorkflowTemplate)
			break
		}
		// Stored as a locator object ({value}) at every published version,
		// and as a bare string in older or hand-written documents. Either
		// way only the identifier crosses: the referenced workflow lives in
		// n8n's database, not in this one.
		if id := locatorName(node.Parameters["workflowId"]); id != "" {
			parameters["workflowId"] = fromN8NValue(id)
		} else {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "workflowId",
				Reason: "this tool named no workflow to call, so it imports with nothing to execute. " +
					"Point it at a workflow before activating.",
			})
		}
		if carried, present, reported := workflowInputsToKilas(node.Parameters["workflowInputs"]); reported {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "workflowInputs",
				Reason: "this tool declared its workflow inputs in a shape this importer does not read. " +
					"The inputs were dropped: declare them on the tool again before activating.",
			})
		} else if present && carried != nil {
			parameters["workflowInputs"] = carried
		}
	case "parameter":
		// An inline workflow document has no native equivalent: this
		// server's tool calls a stored workflow by reference.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "workflowJson",
			Reason: "this tool carried its workflow as inline JSON, which this server's workflow tool does not implement. " +
				"Save the JSON as a workflow and point the tool at it before activating.",
		})
	default:
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "source",
			Reason: fmt.Sprintf("this tool took its workflow from %q, which this server does not implement. "+
				"Only a stored workflow imports: reconfigure the source before activating.", source),
		})
	}

	// Version-1 declarations the resource mapper replaced. Reported rather
	// than carried: silently running a tool whose inputs changed shape is
	// the failure this whole importer exists to prevent.
	if fields, present := node.Parameters["fields"]; present && fields != nil {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "fields",
			Reason: "this tool declared its workflow inputs in the version-1 fields collection, which has no equivalent here. " +
				"The declarations were dropped: declare the inputs on the tool again before activating.",
		})
	}
	if propertyName := stringParameter(node.Parameters, "responsePropertyName"); propertyName != "" {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "responsePropertyName",
			Reason: "this tool read its answer from a named response property, which this server's tool does not implement. " +
				"The tool returns the whole result instead.",
		})
	}
	return parameters, issues
}

// workflowInputsToKilas carries a workflow tool's resourceMapper value: the
// defined-below entries become plain expression-valued inputs. Anything else
// present is reported so the caller can name it, never carried half-read.
func workflowInputsToKilas(value any) (map[string]any, bool, bool) {
	collection, ok := value.(map[string]any)
	if !ok || collection == nil {
		return nil, value != nil, value != nil
	}
	inner, _ := collection["value"].(map[string]any)
	entries, _ := inner["mapping"].(map[string]any)
	if len(entries) == 0 {
		// The modern resource mapper holds the fields directly under `value`;
		// the `mapping` wrapper is an older shape. Reading only the wrapper
		// imported every current n8n mapper as empty.
		entries = inner
	}
	if len(entries) == 0 {
		// Absent, or a mapper the author never filled in. Optional either
		// way; an empty mapper and no mapper are the same tool.
		empty := len(collection) > 0
		return nil, empty, false
	}
	carried := make(map[string]any, len(entries))
	for _, key := range sortedKeys(entries) {
		carried[key] = fromN8NValue(entries[key])
	}
	return carried, true, false
}

// workflowToolToN8N writes the workflow tool back.
// n8nSelfWorkflowTemplate is how n8n writes "the workflow this node is in".
const n8nSelfWorkflowTemplate = "{{ $workflow.id }}"

// n8nSelfWorkflowReference reports whether a workflow reference points at the
// workflow it sits in rather than at another one.
//
// n8n writes it as `={{ $workflow.id }}` inside a resource locator, and a
// hand-written document may carry the bare expression. Only an exact match
// counts: anything else is an identifier from another instance, which this
// server cannot resolve and must not pretend to.
func n8nSelfWorkflowReference(value any) bool {
	text := locatorName(value)
	if text == "" {
		text = textOf(value)
	}
	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "="))
	text = strings.TrimSpace(strings.TrimPrefix(text, "{{"))
	text = strings.TrimSpace(strings.TrimSuffix(text, "}}"))
	return text == "$workflow.id"
}

func workflowToolToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{
		"name":        toN8NValue(node.Parameters["toolName"]),
		"description": toN8NValue(node.Parameters["toolDescription"]),
		"source":      "database",
		"workflowId":  map[string]any{"value": workflowReferenceToN8N(node.Parameters["workflowId"])},
	}
	if inputs, ok := node.Parameters["workflowInputs"].(map[string]any); ok && len(inputs) > 0 {
		// The editor re-resolves the mapper schema from the workflow on
		// open, so only the entries travel; the wrapper is rebuilt there.
		value := make(map[string]any, len(inputs))
		for _, key := range sortedKeys(inputs) {
			value[key] = toN8NValue(inputs[key])
		}
		parameters["workflowInputs"] = map[string]any{"mappingMode": "defineBelow", "value": value}
	}
	return parameters, lossy
}

// workflowReferenceToN8N renders a stored workflow reference back into the
// `={{ }}` form n8n's locator holds, or the plain identifier it usually is.
func workflowReferenceToN8N(value any) string {
	if stored, ok := value.(map[string]any); ok {
		if mode, _ := stored["mode"].(string); mode == "expression" {
			if template, _ := stored["value"].(string); template != "" {
				return "=" + template
			}
			return ""
		}
		return locatorName(value)
	}
	if name, ok := value.(string); ok {
		return name
	}
	return ""
}

// outputParserToKilas maps n8n's Structured Output Parser onto this server's
// output parser. The mode translates rather than crossing: n8n says
// fromJson/manual, this server says exampleJson/jsonSchema.
// n8nStructuredParserExample is n8n's built-in default example for the
// Structured Output Parser.
//
// Its field default is this JSON text, and n8n omits a parameter still holding
// its default — so a parser whose author never filled the example in carries
// nothing, and the parser asks the model for this shape. Applying the same
// example here is what keeps such a node working; leaving it empty refused
// activation instead.
const n8nStructuredParserExample = `{"example":"value"}`

func outputParserToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	example := node.Parameters["jsonSchemaExample"]
	manual, hasManual := node.Parameters["inputSchema"]
	if !hasManual {
		// Below typeVersion 1.2 there is no mode selector and the schema
		// lives under `jsonSchema` instead.
		manual, hasManual = node.Parameters["jsonSchema"]
	}
	switch mode := stringParameter(node.Parameters, "schemaType"); mode {
	case "manual":
		parameters["schemaType"] = "jsonSchema"
		if hasManual && manual != nil && manual != "" {
			parameters["jsonSchema"] = fromN8NValue(manual)
		}
	case "fromJson":
		parameters["schemaType"] = "exampleJson"
		if example != nil && example != "" {
			parameters["exampleJson"] = fromN8NValue(example)
		} else {
			// n8n fills its built-in example in when the author never touched
			// the field, and an export omits a parameter holding its default —
			// so the absence is n8n's example, not "no example". Importing it
			// as empty failed activation with "exampleJson is required when the
			// schema type is example JSON" for a node that worked in n8n.
			parameters["exampleJson"] = n8nStructuredParserExample
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "jsonSchemaExample",
				Reason: "this parser used n8n's built-in example, which was not stored in the export; " +
					"n8n's own default example was applied, so edit it to the shape this parser should return",
			})
		}
	case "":
		// No selector: either a pre-1.2 document, or a mode the author
		// never touched. Whichever key is populated decides.
		if hasManual && manual != nil && manual != "" {
			parameters["schemaType"] = "jsonSchema"
			parameters["jsonSchema"] = fromN8NValue(manual)
		} else {
			parameters["schemaType"] = "exampleJson"
			if example != nil && example != "" {
				parameters["exampleJson"] = fromN8NValue(example)
			} else {
				parameters["exampleJson"] = n8nStructuredParserExample
			}
		}
	default:
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "schemaType",
			Reason: fmt.Sprintf("this parser specified its schema %q, which this server does not implement. "+
				"Only a JSON example or a JSON schema imports: choose one before activating.", mode),
		})
		parameters["schemaType"] = "exampleJson"
	}

	// n8n retries through a model call; here that is a retry count. Absent
	// means the author never touched it, which is n8n's own default of off.
	// A float64, like every number this importer emits: the stored document
	// is JSON, where there is no integer type.
	if fixed, _ := node.Parameters["autoFix"].(bool); fixed {
		parameters["maxRetries"] = float64(2)
	} else {
		parameters["maxRetries"] = float64(0)
	}
	// The retry prompt only fires when auto-fix does, so both are reported
	// together with it rather than as two more mysteries.
	if _, custom := node.Parameters["customizeRetryPrompt"].(bool); custom {
		if fixed, _ := node.Parameters["autoFix"].(bool); fixed {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "customizeRetryPrompt",
				Reason: "this parser customised its retry prompt, which this server's parser does not implement. " +
					"The default retry prompt applies instead.",
			})
		}
	}
	if prompt := node.Parameters["prompt"]; prompt != nil && prompt != "" {
		if fixed, _ := node.Parameters["autoFix"].(bool); fixed {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "prompt",
				Reason: "this parser carried a custom retry prompt, which this server's parser does not implement. " +
					"The prompt was dropped and the default retry prompt applies instead.",
			})
		}
	}
	return parameters, issues
}

// outputParserToN8N writes the parser back, restoring n8n's mode names.
func outputParserToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{}
	switch stringParameter(node.Parameters, "schemaType") {
	case "jsonSchema":
		parameters["schemaType"] = "manual"
		if schema := node.Parameters["jsonSchema"]; schema != nil && schema != "" {
			parameters["inputSchema"] = toN8NValue(schema)
		}
	default:
		// exampleJson, and anything a hand-built document holds instead:
		// n8n's default mode is the example, so that is what an unknown
		// mode degrades to rather than a version n8n cannot open.
		parameters["schemaType"] = "fromJson"
		if example := node.Parameters["exampleJson"]; example != nil && example != "" {
			parameters["jsonSchemaExample"] = toN8NValue(example)
		}
	}
	// This server counts retries; n8n toggles a model call. Any nonzero
	// count is auto-fix on.
	if retries, ok := numberParameter(node.Parameters, "maxRetries"); ok {
		parameters["autoFix"] = retries != 0
	} else {
		parameters["autoFix"] = true
	}
	return parameters, lossy
}

// --- Data Table ---------------------------------------------------------------
//
// The Data Table node (n8n-nodes-base.dataTable) reads and writes n8n's
// project-scoped data tables, and n8n-nodes-base.dataTableTool exposes the
// same surface as an agent tool. Both map onto this server's datastore node
// family, whose own parameter names deliberately mirror n8n's.
//
// Three translations below are load-bearing rather than cosmetic. The node's
// filter rows are keyName/condition/keyValue, which differ from the
// service-layer columnName/condition/value the same workflow would meet over
// the API — the importer carries the node names verbatim and never assumes
// one shape. The table-level update operation is surfaced as Rename, never
// as a generic update. And a carried table reference is always somebody
// else's catalogue id, so it arrives with a blocking diagnostic rather than
// a silent bind.
//
// Every parameter name, option value and default below is n8n's own,
// transcribed from the reference checkout's data-table.types.ts (row and
// table operation unions, filter shape, system columns at 2.34.0) and the
// design-refs captures of the node's own surface (shots 26-32, n8n 2.33.7).
// The node's parameter descriptions are absent from the sparse checkout, so
// anything structural the captures do not evidence produces a named
// diagnostic instead of being assumed away.

// dataTableRowOperations maps n8n's row operation names onto this server's.
// n8n says deleteRows where this server says delete, and rowExists and
// rowNotExists where this server branches as ifExists and ifNotExists.
var dataTableRowOperations = map[string]string{
	"insert": "insert", "get": "get", "update": "update", "upsert": "upsert",
	"deleteRows": "delete", "rowExists": "ifExists", "rowNotExists": "ifNotExists",
}

// dataTableTableOperations maps n8n's table operation names onto this
// server's. The update operation is surfaced as Rename, never as a generic
// update, on both sides.
var dataTableTableOperations = map[string]string{
	"create": "create", "list": "list", "clear": "clear",
	"update": "rename", "delete": "deleteTable",
}

// datastoreRowOperationsToN8N is the inverse of dataTableRowOperations.
var datastoreRowOperationsToN8N = map[string]string{
	"insert": "insert", "get": "get", "update": "update", "upsert": "upsert",
	"delete": "deleteRows", "ifExists": "rowExists", "ifNotExists": "rowNotExists",
}

// datastoreTableOperationsToN8N is the inverse of dataTableTableOperations.
var datastoreTableOperationsToN8N = map[string]string{
	"create": "create", "list": "list", "clear": "clear",
	"rename": "update", "deleteTable": "delete",
}

// dataTableConditionOperations are the filter conditions both servers spell
// the same way. The node's Column select offers the system columns id,
// createdAt and updatedAt alongside the user columns, and the operator set
// is n8n's own filter union — eq, neq, like, ilike, gt, gte, lt, lte.
var dataTableConditionOperations = map[string]bool{
	"eq": true, "neq": true, "like": true, "ilike": true,
	"gt": true, "gte": true, "lt": true, "lte": true,
}

// dataTableToKilas maps n8n's Data Table node onto this server's datastore
// node. The resource, operation, locator, mapper and filter panel all cross;
// the table reference always arrives with a blocking diagnostic, because an
// n8n table id names a row in somebody else's catalogue.
func dataTableToKilas(node Node) (map[string]any, []Unsupported) {
	return dataTableParams(node, false)
}

// dataTableParams carries a Data Table node's parameters, with tool carrying
// the toolDescription key the tool variant owns.
func dataTableParams(node Node, tool bool) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	resource := stringParameter(node.Parameters, "resource")
	if resource == "" {
		resource = "row"
	}
	if resource != "row" && resource != "table" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "resource",
			Reason: fmt.Sprintf("this node read resource %q, which this server does not implement. "+
				"It was imported as a row node: set the resource before activating.", resource),
		})
		resource = "row"
	}
	parameters["resource"] = resource

	operation := stringParameter(node.Parameters, "operation")
	mapped := ""
	if resource == "row" {
		mapped = dataTableRowOperations[operation]
	} else {
		mapped = dataTableTableOperations[operation]
	}
	if mapped == "" {
		fallback := "insert"
		if resource == "table" {
			fallback = "create"
		}
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "operation",
			Reason: fmt.Sprintf("this node ran operation %q, which this server does not implement. "+
				"It was imported as %q: set the operation before activating.", operation, fallback),
		})
		mapped = fallback
	}
	parameters["operation"] = mapped

	// Only create and list run without a table. Every other operation names
	// one, and the reference is always carried with a blocking diagnostic:
	// the id is n8n-local, no table is ever created as a side effect of an
	// import, and the cached name is a display string n8n never guarantees
	// is current — so matching it against a same-named local table would be
	// a write to the wrong table that succeeds.
	if mapped != "create" && mapped != "list" {
		locator, tableIssue := dataTableLocatorToKilas(node.Parameters["dataTableId"])
		if locator != nil {
			parameters["dataTableId"] = locator
		}
		if tableIssue != nil {
			issues = append(issues, *tableIssue)
		}
	} else if raw, ok := node.Parameters["dataTableId"]; ok && raw != nil {
		carried, tableIssue := dataTableLocatorToKilas(raw)
		if carried != nil {
			parameters["dataTableId"] = carried
		}
		if tableIssue != nil {
			issues = append(issues, *tableIssue)
		}
	} else {
		// Create and list run without a table, but the parameter key itself
		// is required: the compiler refuses a node that omits it outright.
		// An empty locator is what the editor stores before a table is
		// picked, so that is what an n8n node without a table slot becomes.
		parameters["dataTableId"] = property.WriteLocator(property.Locator{Mode: "list"})
	}

	if name := node.Parameters["name"]; name != nil && name != "" {
		parameters["name"] = fromN8NValue(name)
	}
	if columns, present := node.Parameters["columns"]; present && columns != nil {
		carried, columnIssues := dataTableColumnsToKilas(columns)
		if carried != nil {
			parameters["columns"] = carried
		}
		issues = append(issues, columnIssues...)
	}
	if filters, present := node.Parameters["filters"]; present && filters != nil {
		carried, filterIssues := dataTableFiltersToKilas(filters)
		if carried != nil {
			parameters["filters"] = carried
		}
		issues = append(issues, filterIssues...)
	}
	// Must Match: Any Condition or All Conditions. Absent is n8n's own
	// default, which is Any — the same default this server applies — so only
	// a present and unrecognised value is worth a diagnostic.
	switch match := stringParameter(node.Parameters, "match"); match {
	case "", "any", "all":
		if match != "" {
			parameters["match"] = match
		}
	default:
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "match",
			Reason: fmt.Sprintf("this node matched rows with %q, which this server does not implement. "+
				"It was imported matching any condition instead.", match),
		})
	}
	if value, present := node.Parameters["returnAll"]; present {
		if fixed, ok := value.(bool); ok {
			parameters["returnAll"] = fixed
		} else {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "returnAll",
				Reason: "this node carried Return All in a shape this server does not read. " +
					"It was dropped: set Return All again before activating.",
			})
		}
	}
	if value, present := node.Parameters["limitPerInputRow"]; present && value != nil {
		if limit, ok := numberParameter(node.Parameters, "limitPerInputRow"); ok {
			parameters["limitPerInputRow"] = limit
		} else {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "limitPerInputRow",
				Reason: "this node carried its per-row limit in a shape this server does not read. " +
					"It was dropped and the default limit applies instead.",
			})
		}
	}
	if options, present := node.Parameters["options"]; present && options != nil {
		if collection, ok := options.(map[string]any); !ok || len(collection) > 0 {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "options",
				Reason: "this node carried an Options collection, which this server's datastore node does not implement. " +
					"The options were dropped.",
			})
		}
	}
	// Anything else present is a parameter this importer does not read.
	// Reported rather than dropped silently: a parameter that disappears
	// leaves a workflow that looks identical to the one it came from and
	// behaves differently.
	known := map[string]bool{
		"resource": true, "operation": true, "dataTableId": true, "columns": true,
		"filters": true, "match": true, "returnAll": true, "limitPerInputRow": true,
		"name": true, "options": true,
	}
	if tool {
		known["toolDescription"] = true
	}
	for _, key := range sortedKeys(node.Parameters) {
		if !known[key] {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: key,
				Reason: fmt.Sprintf("this node carried %q, which this server's datastore node does not implement. "+
					"It was dropped: check the node before activating.", key),
			})
		}
	}
	return parameters, issues
}

// dataTableLocatorToKilas carries n8n's data-table resource locator across.
// All three modes — From list, By Name and By ID — are kept, with the cached
// name travelling as display data only. The second return is always non-nil
// when a locator was needed: a carried reference reports the n8n id it came
// from, and an empty one reports what was missing.
func dataTableLocatorToKilas(value any) (map[string]any, *Unsupported) {
	raw, ok := value.(map[string]any)
	if !ok {
		// A bare string is the shape older documents stored: a table id with
		// no mode and no cached name.
		if text, ok := value.(string); ok && text != "" {
			return property.WriteLocator(property.Locator{Mode: "id", Value: fromN8NValue(text)}),
				&Unsupported{
					Severity: SeverityBlocking, Field: "dataTableId",
					Reason: fmt.Sprintf("this node addressed the n8n data table %q, which names a table in n8n's catalogue "+
						"and has no local counterpart. No table was created: rebind this node to a KilasFlow data table before activating.", text),
				}
		}
		return nil, &Unsupported{
			Severity: SeverityBlocking, Field: "dataTableId",
			Reason: "this node named no data table, so it imports with nothing to act on. " +
				"Point it at a data table before activating.",
		}
	}
	mode, _ := raw["mode"].(string)
	if mode != "list" && mode != "name" && mode != "id" {
		mode = "list"
	}
	cached, _ := raw["cachedResultName"].(string)
	locator := property.Locator{Mode: mode, Value: fromN8NValue(raw["value"]), CachedResultName: cached}
	if !property.LocatorIsSet(property.WriteLocator(locator)) {
		return property.WriteLocator(property.Locator{Mode: mode, CachedResultName: cached}), &Unsupported{
			Severity: SeverityBlocking, Field: "dataTableId",
			Reason: fmt.Sprintf("this node named no data table, so it imports with nothing to act on. "+
				"Point it at a data table before activating%s.", quotedCachedName(cached)),
		}
	}
	described := locatorDescription(raw["value"], cached)
	return property.WriteLocator(locator), &Unsupported{
		Severity: SeverityBlocking, Field: "dataTableId",
		Reason: fmt.Sprintf("this node addressed the n8n data table %s, which names a table in n8n's catalogue "+
			"and has no local counterpart. No table was created: rebind this node to a KilasFlow data table before activating.", described),
	}
}

// quotedCachedName renders the cached name a locator carried, or nothing when
// it carried none.
func quotedCachedName(cached string) string {
	if cached == "" {
		return ""
	}
	return fmt.Sprintf(" (it last showed %q)", cached)
}

// locatorDescription names the n8n table id and the cached name together, so
// the diagnostic identifies the table both systems would recognise.
func locatorDescription(value any, cached string) string {
	identifier, _ := value.(string)
	if identifier == "" {
		identifier = "an unrecorded id"
	} else {
		identifier = fmt.Sprintf("%q", identifier)
	}
	if cached == "" {
		return identifier
	}
	return fmt.Sprintf("%s (last shown as %q)", identifier, cached)
}

// dataTableColumnsToKilas carries the mapping-column panel across. The stored
// shape already uses n8n's names — mappingMode of defineBelow or
// autoMapInputData, with value, matchingColumns and a schema copy — so the
// panel is carried rather than translated, and each mapped value converts
// the way any other expression-valued parameter does.
func dataTableColumnsToKilas(value any) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	collection, ok := value.(map[string]any)
	if !ok {
		if text, ok := value.(string); ok {
			if carried := fromN8NValue(text); carried != text {
				return carried.(map[string]any), []Unsupported{{
					Severity: SeverityBlocking, Field: "columns",
					Reason: "this node built its column mapping from an expression, and column names cannot be expressions. " +
						"Map each column explicitly and keep expressions in the values.",
				}}
			}
		}
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "columns",
			Reason: "this node carried its column mapping in a shape this importer does not read. " +
				"Map the columns again before activating.",
		})
		return nil, issues
	}
	// n8n's own modes, verbatim: Map Each Column Manually sets the value for
	// each column, while Map Automatically looks for incoming data matching
	// the data table's columns. An absent mode is n8n's default, which is
	// manual; only a present and unrecognised one is worth a diagnostic.
	mode, _ := collection["mappingMode"].(string)
	if mode == "" {
		mode = property.MappingManual
	}
	if mode != property.MappingManual && mode != property.MappingAuto {
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "columns",
			Reason: fmt.Sprintf("this node mapped its columns with mode %q, which this server does not implement. "+
				"It was imported mapping each column manually instead.", mode),
		})
		mode = property.MappingManual
	}
	values := map[string]any{}
	if raw, ok := collection["value"].(map[string]any); ok {
		for _, key := range sortedKeys(raw) {
			values[key] = fromN8NValue(raw[key])
		}
	} else if collection["value"] != nil {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "columns",
			Reason: "this node carried its mapped column values in a shape this importer does not read. " +
				"The values were dropped: map the columns again before activating.",
		})
	}
	matching := make([]string, 0)
	if raw, ok := collection["matchingColumns"].([]any); ok {
		for _, entry := range raw {
			if name, ok := entry.(string); ok {
				matching = append(matching, name)
			} else {
				issues = append(issues, Unsupported{
					Severity: SeverityDropped, Field: "columns",
					Reason: "this node matched rows on a column this importer does not read. " +
						"The entry was dropped: check the matching columns before activating.",
				})
			}
		}
	}
	mapping := property.Mapping{Mode: mode, Values: values, MatchingColumns: matching}
	if raw, ok := collection["schema"].([]any); ok {
		for _, entry := range raw {
			if row, ok := entry.(map[string]any); ok {
				mapping.Schema = append(mapping.Schema, property.MapperField{
					ID:               fieldString(row["id"]),
					DisplayName:      fieldString(row["displayName"]),
					Type:             fieldString(row["type"]),
					Required:         fieldFlag(row["required"]),
					CanBeUsedToMatch: fieldFlag(row["canBeUsedToMatch"]),
					DefaultMatch:     fieldFlag(row["defaultMatch"]),
					ReadOnly:         fieldFlag(row["readOnly"]),
				})
			}
		}
	}
	return property.WriteMapping(mapping), issues
}

// fieldString reads a mapper schema field's text.
func fieldString(value any) string {
	text, _ := value.(string)
	return text
}

// fieldFlag reads a mapper schema field's flag.
func fieldFlag(value any) bool {
	fixed, _ := value.(bool)
	return fixed
}

// dataTableFiltersToKilas carries the Conditions panel across. The row paths
// are the node's own — filters.conditions[i].keyName, .condition defaulting
// to eq, and .keyValue — and they differ from the service layer's
// columnName/condition/value, so the rows cross under the node names rather
// than being reshaped. Only the value converts as an expression; the column
// slot is literal-only, and an expression there is refused by name.
func dataTableFiltersToKilas(value any) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	if text, ok := value.(string); ok {
		if carried := fromN8NValue(text); carried != text {
			return carried.(map[string]any), []Unsupported{{
				Severity: SeverityBlocking, Field: "filters",
				Reason: "this node built its conditions from an expression, and column names cannot be expressions. " +
					"Add the conditions explicitly and keep expressions in the values.",
			}}
		}
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "filters",
			Reason: "this node carried its conditions in a shape this importer does not read. " +
				"The conditions were dropped: add them again before activating.",
		})
		return nil, issues
	}
	collection, ok := value.(map[string]any)
	if !ok {
		issues = append(issues, Unsupported{
			Severity: SeverityDropped, Field: "filters",
			Reason: "this node carried its conditions in a shape this importer does not read. " +
				"The conditions were dropped: add them again before activating.",
		})
		return nil, issues
	}
	entries, _ := collection["conditions"].([]any)
	rows := make([]any, 0, len(entries))
	for index, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "filters",
				Reason: fmt.Sprintf("this node carried its condition row %d in a shape this importer does not read. "+
					"The row was dropped: check the conditions before activating.", index),
			})
			continue
		}
		carried := map[string]any{}
		// The column slot is literal-only. An expression here would hand the
		// choice of column to the incoming item, so it is named rather than
		// converted — and the marker is kept, so the compiler refuses the
		// node even if the diagnostic is never read.
		if text, ok := row["keyName"].(string); ok && strings.HasPrefix(text, "=") {
			carried["keyName"] = fromN8NValue(row["keyName"])
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "filters",
				Reason: fmt.Sprintf("this node's condition row %d chose its column from an expression, and a column name cannot be one. "+
					"Resolve the column to a literal name and keep the expression in the value.", index),
			})
		} else {
			carried["keyName"] = row["keyName"]
		}
		// An absent condition is n8n's own default, which is equality. Only
		// a condition that is present and unrecognised is worth a
		// diagnostic: reporting the default as lossy fills the list with
		// rows where nothing was lost.
		condition := stringParameter(row, "condition")
		if condition == "" {
			condition = "eq"
		}
		if !dataTableConditionOperations[condition] {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "filters",
				Reason: fmt.Sprintf("this node's condition row %d used condition %q, which this server does not implement. "+
					"The row was imported as an equality instead.", index, condition),
			})
			condition = "eq"
		}
		carried["condition"] = condition
		carried["keyValue"] = fromN8NValue(row["keyValue"])
		rows = append(rows, carried)
	}
	return map[string]any{"conditions": rows}, issues
}

// dataTableToolToKilas maps n8n's Data Table Tool onto this server's
// datastore tool, reusing the Data Table translation the two nodes share.
func dataTableToolToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, issues := dataTableParams(node, true)

	// n8n has no tool-name parameter at all: the name a model calls is derived
	// from the node's own canvas name. Deriving it the same way is what keeps
	// an imported system prompt that named the tool still naming the same
	// tool.
	parameters["toolName"] = nodes.NormalizeToolName(node.Name)

	description := node.Parameters["toolDescription"]
	if description == nil || description == "" {
		// Required here, and a model given no description cannot know when to
		// call the tool. Filling the gap visibly beats failing compilation
		// with nothing for the author to look at.
		parameters["toolDescription"] = "Calls " + node.Name + "."
	} else {
		parameters["toolDescription"] = fromN8NValue(description)
	}
	return parameters, issues
}

// dataTableToN8N writes a datastore node back out as n8n's Data Table node.
// Operations translate back to n8n's names — delete to deleteRows, the
// branches to rowExists and rowNotExists, rename to update and deleteTable
// to delete — and the filter rows go back under the node's own
// keyName/condition/keyValue paths.
func dataTableToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return dataTableToN8NParams(node, false)
}

// dataTableToN8NParams writes either datastore flavour back, with the tool
// carrying the description n8n's tool node owns.
func dataTableToN8NParams(node workflow.Node, tool bool) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{}

	resource := defaultString(stringParameter(node.Parameters, "resource"), "row")
	if resource != "row" && resource != "table" {
		resource = "row"
	}
	parameters["resource"] = resource
	operation := stringParameter(node.Parameters, "operation")
	if resource == "row" {
		operation = defaultString(datastoreRowOperationsToN8N[operation], "insert")
	} else {
		operation = defaultString(datastoreTableOperationsToN8N[operation], "create")
	}
	parameters["operation"] = operation

	if locator, ok := property.ReadLocator(node.Parameters["dataTableId"]); ok {
		exported := map[string]any{
			property.LocatorSentinel: true,
			"mode":                   defaultString(locator.Mode, "list"),
			"value":                  toN8NValue(locator.Value),
		}
		if locator.CachedResultName != "" {
			exported["cachedResultName"] = locator.CachedResultName
		}
		parameters["dataTableId"] = exported
		// A KilasFlow table id names a row in this server's catalogue and
		// means nothing in an n8n instance, so it is named as lost rather
		// than emitted as a reference that resolves nowhere — in the same
		// terms the exporter names a credential reference.
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "dataTableId",
			Reason: "data table references are KilasFlow identifiers and were not exported; " +
				"reattach the data table in n8n before running the workflow",
		})
	}
	if name := node.Parameters["name"]; name != nil && name != "" {
		parameters["name"] = toN8NValue(name)
	}
	if mapping, ok := property.ReadMapping(node.Parameters["columns"]); ok {
		values := make(map[string]any, len(mapping.Values))
		for _, key := range sortedKeys(mapping.Values) {
			values[key] = toN8NValue(mapping.Values[key])
		}
		exported := property.WriteMapping(property.Mapping{
			Mode: mapping.Mode, Values: values,
			MatchingColumns: mapping.MatchingColumns, Schema: mapping.Schema,
		})
		parameters["columns"] = exported
	} else if columns, present := node.Parameters["columns"]; present && columns != nil {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "columns",
			Reason: "this node carried its column mapping in a shape n8n does not read. " +
				"It was not exported; map the columns again in n8n",
		})
	}
	if filters, ok := node.Parameters["filters"].(map[string]any); ok {
		entries, _ := filters["conditions"].([]any)
		rows := make([]any, 0, len(entries))
		for _, entry := range entries {
			row, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			rows = append(rows, map[string]any{
				"keyName":   toN8NValue(row["keyName"]),
				"condition": defaultString(stringParameter(row, "condition"), "eq"),
				"keyValue":  toN8NValue(row["keyValue"]),
			})
		}
		parameters["filters"] = map[string]any{"conditions": rows}
	} else if filters, present := node.Parameters["filters"]; present && filters != nil {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "filters",
			Reason: "this node carried its conditions in a shape n8n does not read. " +
				"It was not exported; add the conditions again in n8n",
		})
	}
	for _, key := range []string{"match", "returnAll", "limitPerInputRow"} {
		if value, present := node.Parameters[key]; present {
			parameters[key] = value
		}
	}
	if tool {
		parameters["toolDescription"] = toN8NValue(node.Parameters["toolDescription"])
		// The name the model calls is the node's canvas name in n8n and a
		// parameter here, so a tool renamed away from its node name cannot
		// be expressed.
		if name := stringParameter(node.Parameters, "toolName"); name != "" && name != nodes.NormalizeToolName(node.Name) {
			lossy = append(lossy, Lossy{
				Severity: SeverityLossy, Field: "toolName",
				Reason: fmt.Sprintf(
					"the model called this tool %q. n8n derives a tool's name from the node's own name, "+
						"so after export it is called %q; rename the node to keep the old name.", name, nodes.NormalizeToolName(node.Name)),
			})
		}
	}
	return parameters, lossy
}

// dataTableToolToN8N writes the datastore tool back as n8n's Data Table Tool.
func dataTableToolToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return dataTableToN8NParams(node, true)
}
