package n8n

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
	"github.com/kilaslabs/kilas-flow/internal/sqlbuild"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// n8n marks an expression by prefixing the string with `=`, and uses the same
// `{{ }}` interpolation KilasFlow does. KilasFlow marks one with an explicit
// `{"mode":"expression"}` value instead, so the prefix is translated rather
// than carried.
func expressionValue(template string) map[string]any {
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

// setToKilas reads n8n's Set assignments.
//
// n8n v3 nests them under `assignments.assignments` as typed entries; older
// versions used `values.string` and friends. Both are read, because an
// exported workflow in the wild may be either.
// setToKilas reads n8n's Set node in either of the shapes it has had.
//
// Both are *ordered lists with a declared type per row*, and both are carried
// through as one. The importer used to collapse them into a `map[name]value`,
// which lost the order the author typed and the type of every entry — so a
// boolean returned to n8n as a string and the fields came back alphabetical.
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
				entries = append(entries, assignmentEntry(node, index, identifier, name,
					assignmentTypeOf(fields["type"]), fromN8NValue(fields["value"])))
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
			for index, entry := range list {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				identifier, _ := fields["id"].(string)
				entries = append(entries, assignmentEntry(node, len(entries)+index, identifier, name,
					assignmentTypeOf(kind), fromN8NValue(fields["value"])))
			}
		}
	}

	if keepOnly, ok := node.Parameters["includeOtherFields"].(bool); ok && !keepOnly {
		// KilasFlow's Set always adds to the incoming item.
		issues = append(issues, Unsupported{
			Reason: "n8n's \"keep only set fields\" option has no KilasFlow equivalent; the imported Set adds its fields to every incoming item instead of replacing them",
		})
	}
	if len(entries) == 0 {
		issues = append(issues, Unsupported{
			Reason: "this Set node has no readable assignments; add at least one field before running the workflow",
		})
	}
	return map[string]any{"assignments": map[string]any{"assignments": entries}}, issues
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
	default:
		return string(property.AssignmentString)
	}
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
	return map[string]any{
		"assignments": map[string]any{"assignments": entries},
		"options":     map[string]any{},
	}, nil
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
func legacyCondition(row map[string]any, group string) map[string]any {
	operation, _ := row["operation"].(string)
	if operation == "" {
		operation = "equals"
	}
	return map[string]any{
		"leftValue":  row["value1"],
		"rightValue": row["value2"],
		"operator":   map[string]any{"type": group, "operation": operation},
	}
}

// filterToN8N writes the shared shape back out.
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
	converted := map[string]any{"mode": defaultString(stringParameter(node.Parameters, "mode"), "append")}

	if combineBy := stringParameter(node.Parameters, "combineBy"); combineBy != "" {
		converted["combineBy"] = combineBy
	}
	if count, ok := numberParameter(node.Parameters, "numberInputs"); ok {
		converted["numberInputs"] = count
	}
	if branch, ok := numberParameter(node.Parameters, "chooseBranch"); ok {
		converted["chooseBranch"] = branch
	}
	if joinMode := stringParameter(node.Parameters, "joinMode"); joinMode != "" {
		converted["joinMode"] = joinMode
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
	return converted, issues
}

func mergeToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"mode":    defaultString(stringParameter(node.Parameters, "mode"), "append"),
		"options": map[string]any{},
	}
	for _, key := range []string{"combineBy", "numberInputs", "chooseBranch", "joinMode"} {
		if value, present := node.Parameters[key]; present {
			written[key] = value
		}
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
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if options["fallbackOutput"] != nil {
			// n8n writes "none", "extra", or an output index. An index names a
			// rule's own output, which KilasFlow reaches by wiring rather than
			// by a fallback, so only the two named forms carry.
			if fallback, _ := options["fallbackOutput"].(string); fallback == "extra" {
				built["fallbackOutput"] = "extra"
			} else if fallback != "none" && fallback != "" {
				issues = append(issues, Unsupported{
					Field:  "options.fallbackOutput",
					Reason: fmt.Sprintf("this Switch sent unmatched items to output %v; wire that rule's own output instead", options["fallbackOutput"]),
				})
			}
		}
		if all, _ := options["allMatchingOutputs"].(bool); all {
			built["allMatchingOutputs"] = true
		}
	}
	return built, issues
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

func httpToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{
		"method": strings.ToUpper(defaultString(stringParameter(node.Parameters, "method"), "GET")),
		"url":    fromN8NValue(node.Parameters["url"]),
	}

	if query, ok := namedValues(node.Parameters, "sendQuery", "queryParameters"); ok {
		parameters["sendQuery"] = true
		parameters["queryParameters"] = query
	}
	if headers, ok := namedValues(node.Parameters, "sendHeaders", "headerParameters"); ok {
		parameters["sendHeaders"] = true
		parameters["headers"] = headers
	}

	if send, _ := node.Parameters["sendBody"].(bool); send {
		parameters["sendBody"] = true
		switch contentType := stringParameter(node.Parameters, "contentType"); contentType {
		case "json", "":
			parameters["bodyType"] = "json"
			if body, ok := node.Parameters["jsonBody"]; ok {
				parameters["body"] = fromN8NValue(body)
			} else if fields, ok := namedValues(node.Parameters, "sendBody", "bodyParameters"); ok {
				encoded, _ := json.Marshal(fields)
				parameters["body"] = string(encoded)
			}
		case "form-urlencoded":
			parameters["bodyType"] = "form"
			if fields, ok := namedValues(node.Parameters, "sendBody", "bodyParameters"); ok {
				encoded, _ := json.Marshal(fields)
				parameters["body"] = string(encoded)
			}
		default:
			parameters["bodyType"] = "raw"
			issues = append(issues, Unsupported{
				Reason: fmt.Sprintf("the n8n body content type %q is outside the supported subset; the body was imported as raw text", contentType),
			})
		}
	}

	if authentication := stringParameter(node.Parameters, "authentication"); authentication != "" && authentication != "none" {
		// A credential reference in an exported n8n workflow is an n8n ID and
		// means nothing here, so it is named rather than silently dropped.
		issues = append(issues, Unsupported{
			Reason: "this HTTP Request used an n8n credential. Credentials are not imported; attach a KilasFlow credential before running the workflow.",
		})
	}
	return parameters, issues
}

func httpToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{
		"method":  defaultString(stringParameter(node.Parameters, "method"), "GET"),
		"url":     toN8NValue(node.Parameters["url"]),
		"options": map[string]any{},
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
		case "raw":
			parameters["contentType"] = "raw"
		default:
			parameters["contentType"] = "json"
			parameters["specifyBody"] = "json"
		}
		parameters["jsonBody"] = toN8NValue(node.Parameters["body"])
	}
	if _, set := node.Parameters["neverError"]; set {
		lossy = append(lossy, Lossy{Field: "neverError", Reason: "n8n expresses this as an error-handling setting rather than a parameter; set \"Continue On Fail\" in n8n if it is needed"})
	}
	return parameters, lossy
}

// namedValues reads n8n's `{parameters: [{name, value}]}` collections.
func namedValues(parameters map[string]any, flag, key string) (map[string]any, bool) {
	if enabled, _ := parameters[flag].(bool); !enabled {
		return nil, false
	}
	wrapper, ok := parameters[key].(map[string]any)
	if !ok {
		return nil, false
	}
	entries, ok := wrapper["parameters"].([]any)
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
		"path":       strings.Trim(stringParameter(node.Parameters, "path"), "/"),
		"httpMethod": strings.ToUpper(defaultString(stringParameter(node.Parameters, "httpMethod"), "POST")),
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
	default:
		parameters["authentication"] = "none"
		issues = append(issues, Unsupported{
			Reason: fmt.Sprintf("the n8n webhook authentication mode %q is not supported; the imported webhook is unauthenticated and should be secured before activation", authentication),
		})
	}
	return parameters, issues
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
	return map[string]any{
		"path":         stringParameter(node.Parameters, "path"),
		"httpMethod":   defaultString(stringParameter(node.Parameters, "httpMethod"), "POST"),
		"responseMode": responseMode,
		"options":      map[string]any{},
	}, nil
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

	switch respondWith := stringParameter(node.Parameters, "respondWith"); respondWith {
	case "":
		// n8n's own default.
		parameters["respondWith"] = "text"
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
	return parameters, issues
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
	if code, ok := numberParameter(node.Parameters, "responseCode"); ok {
		parameters["options"] = map[string]any{"responseCode": code}
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
	// used. Said once, on every import, rather than discovered at run time.
	issues = append(issues, Unsupported{
		Severity: SeverityBlocking, Field: "workflowId",
		Reason: "the sub-workflow is identified by its n8n workflow ID, which does not exist on this server; " +
			"set this node's Workflow to the KilasFlow workflow that should run",
	})

	if mode := stringParameter(node.Parameters, "mode"); mode == "each" {
		parameters["itemsPerCall"] = "eachItem"
	} else {
		parameters["itemsPerCall"] = "allItems"
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
	return parameters, nil
}

func executeWorkflowTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	parameters := map[string]any{}
	source := stringParameter(node.Parameters, "inputSource")
	switch source {
	case "workflowInputs", "fields":
		parameters["inputSource"] = "fields"
		if inputs, ok := node.Parameters["workflowInputs"].(map[string]any); ok {
			parameters["workflowInputs"] = inputs
		}
	default:
		// n8n's passthrough, and its jsonExample mode, both mean "take what the
		// caller sends". The example itself is documentation for n8n's editor
		// and has no run-time meaning to carry.
		parameters["inputSource"] = "passthrough"
	}
	return parameters, nil
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

	if action, isV1 := node.Parameters["action"].(string); isV1 {
		switch action {
		case "", "format":
			parameters["operation"] = "formatDate"
			parameters["date"] = fromN8NValue(node.Parameters["value"])
			parameters["format"] = luxonFormat(stringParameter(node.Parameters, "toFormat"))
		case "calculate":
			parameters["operation"] = "addToDate"
			parameters["date"] = fromN8NValue(node.Parameters["value"])
			parameters["duration"] = node.Parameters["duration"]
			parameters["unit"] = defaultString(stringParameter(node.Parameters, "timeUnit"), "days")
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
		return withDateOutputField(parameters, options, node), issues
	}

	operation := defaultString(stringParameter(node.Parameters, "operation"), "getCurrentDate")
	switch operation {
	case "getCurrentDate", "addToDate", "subtractFromDate", "formatDate", "roundDate", "extractDate", "getTimeBetweenDates":
		parameters["operation"] = operation
	default:
		issues = append(issues, Unsupported{Field: "operation", Reason: fmt.Sprintf(
			"the n8n Date & Time operation %q has no equivalent; the node was imported as \"get current date\"", operation)})
		parameters["operation"] = "getCurrentDate"
	}

	// `magnitude` is v2's name for the date being worked on; `date` is what
	// extractDate calls the same thing, and `startDate` what the comparison
	// does. One field here, three names there.
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
	if unit := defaultString(stringParameter(node.Parameters, "timeUnit"), stringParameter(node.Parameters, "units")); unit != "" {
		parameters["unit"] = unit
	}
	if part := stringParameter(node.Parameters, "part"); part != "" {
		parameters["part"] = part
	}
	if mode := stringParameter(node.Parameters, "mode"); mode != "" {
		parameters["roundMode"] = mode
	}
	if to := stringParameter(node.Parameters, "to"); to != "" {
		parameters["roundTo"] = to
	}
	if layout := defaultString(stringParameter(node.Parameters, "customFormat"), stringParameter(node.Parameters, "format")); layout != "" {
		parameters["format"] = luxonFormat(layout)
	}
	if zone := stringParameter(options, "timezone"); zone != "" {
		parameters["timezone"] = zone
	}
	if include, present := options["includeInputFields"]; present && include == false {
		// This node always keeps the incoming item and adds a field, which is
		// n8n's own default. Replacing the item is the setting it does not have.
		issues = append(issues, Unsupported{Field: "options.includeInputFields", Reason: "this node always keeps " +
			"the incoming item's fields and adds the result beside them; use a Set node afterwards to keep only the date"})
	}
	return withDateOutputField(parameters, options, node), issues
}

// withDateOutputField carries n8n's output field name, under either of the two
// spellings it has used.
func withDateOutputField(parameters map[string]any, options map[string]any, node Node) map[string]any {
	name := defaultString(stringParameter(node.Parameters, "outputFieldName"), stringParameter(options, "outputFieldName"))
	parameters["outputField"] = defaultString(name, "date")
	return parameters
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

// waitToKilas maps n8n's Wait node, refusing the two durable resume modes.
func waitToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	resume := defaultString(stringParameter(node.Parameters, "resume"), "timeInterval")
	parameters := map[string]any{"resume": resume}

	switch resume {
	case "timeInterval":
		parameters["amount"] = node.Parameters["amount"]
		parameters["unit"] = defaultString(stringParameter(node.Parameters, "unit"), "hours")
	case "specificTime":
		parameters["dateTime"] = fromN8NValue(node.Parameters["dateTime"])
	case "webhook", "form":
		// Imported as the shape it is rather than silently rewritten, so the
		// editor shows what the workflow actually said and the validator
		// refuses to activate it with a message that names what is missing.
		// Rewriting it to a time interval would produce a workflow that
		// activates, runs, and does the wrong thing.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "resume",
			Reason: fmt.Sprintf("resuming on %q needs the execution to be suspended to storage and woken again "+
				"later, which this server does not do yet; split the workflow at this point and start the "+
				"second half from a Webhook trigger", resume),
		})
	default:
		issues = append(issues, Unsupported{Field: "resume", Reason: fmt.Sprintf(
			"the n8n Wait resume mode %q has no equivalent; the node was imported as a time interval", resume)})
		parameters["resume"] = "timeInterval"
	}
	return parameters, issues
}

func waitToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := map[string]any{
		"resume": defaultString(stringParameter(node.Parameters, "resume"), "timeInterval"),
	}
	if value, present := node.Parameters["amount"]; present {
		parameters["amount"] = value
	}
	if unit := stringParameter(node.Parameters, "unit"); unit != "" {
		parameters["unit"] = unit
	}
	if value, present := node.Parameters["dateTime"]; present {
		parameters["dateTime"] = toN8NValue(value)
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
	operation := defaultString(stringParameter(node.Parameters, "operation"), "executeQuery")
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
	operation := defaultString(stringParameter(node.Parameters, "operation"), "executeQuery")
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
func splitInBatchesToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	converted := map[string]any{}
	if size, ok := numberParameter(node.Parameters, "batchSize"); ok {
		converted["batchSize"] = size
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if reset, present := options["reset"]; present && reset != false {
			// n8n's `reset` restarts the loop mid-run from an expression.
			// KilasFlow's loop runs its batches once and stops, which is the
			// bounded shape the compiler allows a cycle for at all.
			issues = append(issues, Unsupported{
				Field:  "options.reset",
				Reason: "n8n's loop reset restarts a running loop; KilasFlow's loop runs its batches once, so this was not carried",
			})
		}
	}
	return converted, issues
}

func splitInBatchesToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{"options": map[string]any{}}
	if size, present := node.Parameters["batchSize"]; present {
		written["batchSize"] = size
	}
	// maxIterations is KilasFlow's own bound and has no n8n equivalent; n8n
	// loops until the items run out.
	if _, present := node.Parameters["maxIterations"]; present {
		return written, []Lossy{{
			Field:  "maxIterations",
			Reason: "KilasFlow's iteration bound has no n8n equivalent; the exported loop runs until its items are exhausted",
		}}
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
	converted := map[string]any{
		"aggregate": defaultString(stringParameter(node.Parameters, "aggregate"), "aggregateIndividualFields"),
	}
	if fields := namedList(node.Parameters["fieldsToAggregate"], "fieldToAggregate"); len(fields) > 0 {
		converted["fieldsToAggregate"] = strings.Join(fields, ",")
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if name, _ := options["destinationFieldName"].(string); name != "" {
			converted["destinationFieldName"] = name
		}
	}
	if name := stringParameter(node.Parameters, "destinationFieldName"); name != "" {
		converted["destinationFieldName"] = name
	}
	return converted, nil
}

func aggregateToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := map[string]any{
		"aggregate": defaultString(stringParameter(node.Parameters, "aggregate"), "aggregateIndividualFields"),
		"options":   map[string]any{},
	}
	values := make([]any, 0, 2)
	for _, field := range strings.Split(stringParameter(node.Parameters, "fieldsToAggregate"), ",") {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			values = append(values, map[string]any{"fieldToAggregate": trimmed})
		}
	}
	written["fieldsToAggregate"] = map[string]any{"values": values}
	if name := stringParameter(node.Parameters, "destinationFieldName"); name != "" {
		written["destinationFieldName"] = name
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
	wrapper, _ := node.Parameters["sortFieldsUI"].(map[string]any)
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
	written["sortFieldsUI"] = map[string]any{"sortField": entries}
	return written, nil
}

func summarizeToKilas(node Node) (map[string]any, []Unsupported) {
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
	return converted, nil
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
		parameters["systemPrompt"] = fromN8NValue(system)
	}
	if iterations, ok := numberParameter(options, "maxIterations"); ok {
		parameters["maxIterations"] = iterations
	}

	// Both of these are sub-node slots this server does not have. Reported
	// rather than ignored: an agent that silently stops parsing its output, or
	// silently loses its fallback model, still answers — it just answers
	// differently from the workflow that was imported.
	if parser, _ := node.Parameters["hasOutputParser"].(bool); parser {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "hasOutputParser",
			Reason: "this agent parsed its answer through a connected output parser sub-node, which this server does not have yet. " +
				"The agent was imported without it and returns the model's text unparsed.",
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
	if system := node.Parameters["systemPrompt"]; system != nil && system != "" {
		options["systemMessage"] = toN8NValue(system)
	}
	if iterations, ok := numberParameter(node.Parameters, "maxIterations"); ok {
		options["maxIterations"] = iterations
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

// n8nChatModelOptionKeys are the sampling and transport options both servers
// carry under the same names. They are n8n's names on this side too — see the
// option key constants in nodes/ai.go — so the collection maps across as a
// whole rather than key by key.
var n8nChatModelOptionKeys = []string{
	"frequencyPenalty", "maxTokens", "maxRetries",
	"presencePenalty", "temperature", "timeout", "topP",
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

// memoryToKilas maps n8n's window memory onto this server's Simple Memory.
func memoryToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := map[string]any{}

	sessionKey := node.Parameters["sessionKey"]
	if stringParameter(node.Parameters, "sessionIdType") == "customKey" {
		parameters["sessionId"] = fromN8NValue(sessionKey)
	} else if sessionKey == nil || sessionKey == "" {
		// "fromInput", and every n8n version below 1.2, which had no selector
		// at all. The default is materialised for the same reason the agent's
		// prompt is: sessionId is required here, and a field the author never
		// edited was never stored.
		parameters["sessionId"] = expressionValue("{{ $json.sessionId }}")
	} else {
		parameters["sessionId"] = fromN8NValue(sessionKey)
	}

	if length, ok := numberParameter(node.Parameters, "contextWindowLength"); ok {
		parameters["maxMessages"] = length
	}
	return parameters, issues
}

func memoryToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	parameters := map[string]any{
		// "customKey" rather than "fromInput": the session is written on this
		// node, and fromInput would have n8n read it from a connected chat
		// trigger's payload instead — silently loading a different conversation.
		"sessionIdType": "customKey",
		"sessionKey":    toN8NValue(node.Parameters["sessionId"]),
	}
	if length, ok := numberParameter(node.Parameters, "maxMessages"); ok {
		parameters["contextWindowLength"] = length
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
	parameters["toolName"] = toolNameFrom(node.Name)

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
	if name := stringParameter(node.Parameters, "toolName"); name != "" && name != toolNameFrom(node.Name) {
		lossy = append(lossy, Lossy{
			Severity: SeverityLossy, Field: "toolName",
			Reason: fmt.Sprintf(
				"the model called this tool %q. n8n derives a tool's name from the node's own name, "+
					"so after export it is called %q; rename the node to keep the old name.", name, toolNameFrom(node.Name)),
		})
	}
	return parameters, lossy
}

// toolNameFrom derives the name a model calls from a node's canvas name, the
// way n8n does. The node's own validator accepts letters, digits and
// underscores, so anything else becomes an underscore.
func toolNameFrom(name string) string {
	var built strings.Builder
	for _, letter := range name {
		switch {
		case letter >= 'a' && letter <= 'z', letter >= 'A' && letter <= 'Z',
			letter >= '0' && letter <= '9', letter == '_':
			built.WriteRune(letter)
		default:
			built.WriteRune('_')
		}
	}
	if built.Len() == 0 {
		return "http_request"
	}
	return built.String()
}
