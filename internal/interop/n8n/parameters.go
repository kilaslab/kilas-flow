package n8n

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
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
func setToKilas(node Node) (map[string]any, []Unsupported) {
	assignments := map[string]any{}
	issues := make([]Unsupported, 0)

	if wrapper, ok := node.Parameters["assignments"].(map[string]any); ok {
		if entries, ok := wrapper["assignments"].([]any); ok {
			for _, entry := range entries {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				if strings.TrimSpace(name) == "" {
					continue
				}
				assignments[name] = fromN8NValue(fields["value"])
			}
		}
	}

	if values, ok := node.Parameters["values"].(map[string]any); ok {
		for kind, entries := range values {
			list, ok := entries.([]any)
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
				assignments[name] = fromN8NValue(fields["value"])
				_ = kind
			}
		}
	}

	if keepOnly, ok := node.Parameters["includeOtherFields"].(bool); ok && !keepOnly {
		// KilasFlow's Set always adds to the incoming item.
		issues = append(issues, Unsupported{
			Reason: "n8n's \"keep only set fields\" option has no KilasFlow equivalent; the imported Set adds its fields to every incoming item instead of replacing them",
		})
	}
	if len(assignments) == 0 {
		issues = append(issues, Unsupported{
			Reason: "this Set node has no readable assignments; add at least one field before running the workflow",
		})
	}
	return map[string]any{"assignments": assignments}, issues
}

func setToN8N(node workflow.Node) (map[string]any, []Lossy) {
	assignments, _ := node.Parameters["assignments"].(map[string]any)
	entries := make([]any, 0, len(assignments))
	names := sortedKeys(assignments)
	for index, name := range names {
		entries = append(entries, map[string]any{
			"id":    fmt.Sprintf("%s-%d", node.ID, index),
			"name":  name,
			"type":  "string",
			"value": toN8NValue(assignments[name]),
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

func ifToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)

	conditions, ok := node.Parameters["conditions"].(map[string]any)
	if !ok {
		return map[string]any{}, append(issues, Unsupported{
			Reason: "this IF node has no readable conditions; set one before running the workflow",
		})
	}

	entries, _ := conditions["conditions"].([]any)
	if len(entries) == 0 {
		// n8n v1 grouped conditions by value type instead.
		for _, group := range []string{"string", "number", "boolean", "dateTime"} {
			if list, ok := conditions[group].([]any); ok {
				entries = append(entries, list...)
			}
		}
	}
	if len(entries) == 0 {
		return map[string]any{}, append(issues, Unsupported{
			Reason: "this IF node has no readable conditions; set one before running the workflow",
		})
	}
	if len(entries) > 1 {
		// Importing only the first would silently change what the workflow
		// does, so the extras are named.
		issues = append(issues, Unsupported{
			Reason: fmt.Sprintf("KilasFlow's IF supports one condition; this node had %d, and only the first was imported. Add the rest with additional IF nodes.", len(entries)),
		})
	}

	first, _ := entries[0].(map[string]any)
	field, operator, value, err := ifConditionFrom(first)
	if err != nil {
		return map[string]any{}, append(issues, Unsupported{Reason: err.Error()})
	}

	condition := map[string]any{"field": field, "operator": operator}
	if operator == "equals" || operator == "notEquals" {
		condition["value"] = value
	}
	return map[string]any{"conditions": []any{condition}}, issues
}

func ifConditionFrom(entry map[string]any) (field, operator string, value any, err error) {
	if entry == nil {
		return "", "", nil, fmt.Errorf("this IF node's condition could not be read")
	}

	left, _ := entry["leftValue"].(string)
	if left == "" {
		left, _ = entry["value1"].(string)
	}
	field = fieldPathFrom(left)
	if field == "" {
		return "", "", nil, fmt.Errorf("this IF node compares %q, which is not a plain $json field path that KilasFlow can evaluate", left)
	}

	rawOperator := ""
	if object, ok := entry["operator"].(map[string]any); ok {
		rawOperator, _ = object["operation"].(string)
	}
	if rawOperator == "" {
		rawOperator, _ = entry["operation"].(string)
	}
	mapped, supported := ifOperators[rawOperator]
	if !supported {
		return "", "", nil, fmt.Errorf("KilasFlow's IF does not support the n8n operator %q; supported operators are equals, notEquals, exists, and notExists", rawOperator)
	}

	value = entry["rightValue"]
	if value == nil {
		value = entry["value2"]
	}
	return field, mapped, fromN8NValue(value), nil
}

// fieldPathFrom accepts `={{ $json.a.b }}`, `{{ $json.a }}`, or a bare path.
func fieldPathFrom(left string) string {
	left = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(left), "="))
	if match := leftValuePattern.FindStringSubmatch(left); len(match) == 2 {
		return match[1]
	}
	if left == "" || strings.ContainsAny(left, "{}$ ") {
		return ""
	}
	return left
}

func ifToN8N(node workflow.Node) (map[string]any, []Lossy) {
	conditions, _ := node.Parameters["conditions"].([]any)
	if len(conditions) == 0 {
		return map[string]any{}, []Lossy{{Field: "conditions", Reason: "the IF node had no condition to export"}}
	}
	condition, _ := conditions[0].(map[string]any)
	field, _ := condition["field"].(string)
	operator, _ := condition["operator"].(string)

	entry := map[string]any{
		"id":         node.ID + "-0",
		"leftValue":  "={{ $json." + field + " }}",
		"rightValue": toN8NValue(condition["value"]),
		"operator":   map[string]any{"type": "string", "operation": operator},
	}
	return map[string]any{
		"conditions": map[string]any{
			"options":    map[string]any{"caseSensitive": true, "version": 2},
			"conditions": []any{entry},
			"combinator": "and",
		},
		"options": map[string]any{},
	}, nil
}

// --- Merge ------------------------------------------------------------------

func mergeToKilas(node Node) (map[string]any, []Unsupported) {
	mode := stringParameter(node.Parameters, "mode")
	if mode != "" && mode != "append" {
		return map[string]any{"mode": "append"}, []Unsupported{{
			Reason: fmt.Sprintf("KilasFlow's Merge only appends; the n8n mode %q was replaced with append, which changes what this node does", mode),
		}}
	}
	return map[string]any{"mode": "append"}, nil
}

func mergeToN8N(workflow.Node) (map[string]any, []Lossy) {
	return map[string]any{"mode": "append", "options": map[string]any{}}, nil
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
	parameters := map[string]any{}
	if code, ok := numberParameter(node.Parameters, "responseCode"); ok {
		parameters["responseCode"] = code
	}
	if body, ok := node.Parameters["responseBody"]; ok {
		parameters["responseBody"] = fromN8NValue(body)
	}
	if respondWith := stringParameter(node.Parameters, "respondWith"); respondWith != "" && respondWith != "text" && respondWith != "json" {
		return parameters, []Unsupported{{
			Reason: fmt.Sprintf("the n8n Respond to Webhook mode %q is outside the supported subset; only text and json bodies are imported", respondWith),
		}}
	}
	return parameters, nil
}

// respondToN8N writes the response mode the node actually holds.
//
// It used to write `respondWith: "text"` unconditionally, which is a value
// invented by the exporter rather than derived from the document: a node
// configured to answer with JSON came back as text. Import already accepts both
// text and json, so the inverse must distinguish them.
func respondToN8N(node workflow.Node) (map[string]any, []ExportIssue) {
	respondWith := "text"
	if body, ok := node.Parameters["responseBody"]; ok {
		if _, isText := body.(string); !isText {
			respondWith = "json"
		}
	}
	parameters := map[string]any{"respondWith": respondWith, "options": map[string]any{}}
	if code, ok := numberParameter(node.Parameters, "responseCode"); ok {
		parameters["options"] = map[string]any{"responseCode": code}
	}
	if body, ok := node.Parameters["responseBody"]; ok {
		parameters["responseBody"] = toN8NValue(body)
	}
	return parameters, nil
}

// --- Schedule ---------------------------------------------------------------

func scheduleToKilas(node Node) (map[string]any, []Unsupported) {
	rule, _ := node.Parameters["rule"].(map[string]any)
	intervals, _ := rule["interval"].([]any)
	for _, interval := range intervals {
		fields, ok := interval.(map[string]any)
		if !ok {
			continue
		}
		if expression, ok := fields["expression"].(string); ok && strings.TrimSpace(expression) != "" {
			return map[string]any{"cron": strings.TrimSpace(expression)}, nil
		}
	}
	// n8n's visual interval builder has no single cron equivalent, so an
	// hourly default is written and named rather than guessed at silently.
	return map[string]any{"cron": "0 * * * *"}, []Unsupported{{
		Reason: "this Schedule Trigger used n8n's interval builder rather than a cron expression; it was imported as hourly (0 * * * *). Set the cron expression you need.",
	}}
}

func scheduleToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return map[string]any{
		"rule": map[string]any{
			"interval": []any{map[string]any{
				"field":      "cronExpression",
				"expression": defaultString(stringParameter(node.Parameters, "cron"), "0 * * * *"),
			}},
		},
	}, nil
}

// --- SQL --------------------------------------------------------------------

func sqlToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	operation := stringParameter(node.Parameters, "operation")
	if operation != "" && operation != "executeQuery" {
		return map[string]any{"operation": "query"}, append(issues, Unsupported{
			Reason: fmt.Sprintf("KilasFlow's database nodes run SQL directly; the n8n operation %q has no equivalent and the node was imported as an empty query", operation),
		})
	}

	statement := node.Parameters["query"]
	parameters := map[string]any{
		"operation": "query",
		"statement": fromN8NValue(statement),
	}
	if options, ok := node.Parameters["options"].(map[string]any); ok {
		if values, ok := options["queryReplacement"]; ok {
			// n8n passes replacements as a comma-joined string; KilasFlow binds
			// a JSON array, so the shape is named rather than mangled.
			issues = append(issues, Unsupported{
				Reason: fmt.Sprintf("n8n query replacements (%v) were not imported; set the Parameters field to a JSON array to bind them", values),
			})
		}
	}
	issues = append(issues, Unsupported{
		Reason: "database credentials are not imported; attach a KilasFlow credential before running this node",
	})
	return parameters, issues
}

func sqlToN8N(node workflow.Node) (map[string]any, []Lossy) {
	lossy := make([]Lossy, 0)
	operation := stringParameter(node.Parameters, "operation")
	statement := node.Parameters["statement"]
	switch operation {
	case "", "query":
	case "execute":
		statement = node.Parameters["executeStatement"]
	default:
		lossy = append(lossy, Lossy{
			Field:  "operation",
			Reason: fmt.Sprintf("the KilasFlow operation %q has no n8n equivalent; the export uses executeQuery", operation),
		})
	}
	if _, bound := node.Parameters["parameters"]; bound {
		lossy = append(lossy, Lossy{
			Field:  "parameters",
			Reason: "bound query parameters were not exported; n8n expresses them as query replacements",
		})
	}
	return map[string]any{
		"operation": "executeQuery",
		"query":     toN8NValue(statement),
		"options":   map[string]any{},
	}, lossy
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
