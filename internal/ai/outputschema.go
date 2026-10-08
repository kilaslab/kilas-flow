package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// FormatFinalJSONResponse is the synthetic tool a structured output parser
// offers the model. The name is part of the contract with the model, not an
// implementation detail: imported prompts were tuned against it, and a
// different name changes model behaviour on prompts customers already have.
const FormatFinalJSONResponse = "format_final_json_response"

// DefaultOutputMaxRetries bounds how many times a model response that fails
// schema validation is retried before the run fails diagnosably.
const DefaultOutputMaxRetries = 2

// FormatInstructions states a schema to the model in words.
//
// A chain with a parser has to ask for the shape up front. Validating a
// free-text answer and then describing the failure one error at a time spends
// the retry budget on a guess the model had no way to make, which on a small
// local model is a run that always fails.
func FormatInstructions(schema map[string]any) string {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "Reply with a single JSON object, and nothing else."
	}
	return "Reply with a single JSON object that matches this JSON Schema exactly. " +
		"Use every required property, use no property the schema does not declare, " +
		"and write no prose and no code fences around the object.\n" + string(encoded)
}

// Output parser parameter keys, shared by the node definition, its
// validator, and the agent and chain executors that consume the descriptor.
const (
	OutputParserSchemaTypeKey = "schemaType"
	OutputParserSchemaKey     = "jsonSchema"
	OutputParserExampleKey    = "exampleJson"
	OutputParserRetriesKey    = "maxRetries"
)

// Output parser schema sources.
const (
	OutputSchemaJSONSchema = "jsonSchema"
	OutputSchemaExample    = "exampleJson"
)

// ParseOutputSchema reads a parser descriptor's output shape. The shape is
// either a JSON Schema or an example JSON document it is inferred from, and
// an invalid schema fails validation naming the offending path. It also
// returns the bounded retry count, defaulting when unset.
func ParseOutputSchema(parameters map[string]any) (schema map[string]any, maxRetries int, err error) {
	source, _ := parameters[OutputParserSchemaTypeKey].(string)
	if source == "" {
		source = OutputSchemaJSONSchema
	}
	switch source {
	case OutputSchemaJSONSchema:
		text, err := schemaText(parameters[OutputParserSchemaKey])
		if strings.TrimSpace(text) == "" {
			return nil, 0, fmt.Errorf("jsonSchema is required when the schema type is JSON Schema")
		}
		schema, err = parseJSONSchema([]byte(text))
		if err != nil {
			return nil, 0, err
		}
	case OutputSchemaExample:
		text, err := schemaText(parameters[OutputParserExampleKey])
		if strings.TrimSpace(text) == "" {
			return nil, 0, fmt.Errorf("exampleJson is required when the schema type is example JSON")
		}
		var example any
		if err = json.Unmarshal([]byte(text), &example); err != nil {
			return nil, 0, fmt.Errorf("exampleJson is not valid JSON: %w", err)
		}
		schema = inferJSONSchema(example)
	default:
		return nil, 0, fmt.Errorf("schemaType must be %q or %q", OutputSchemaJSONSchema, OutputSchemaExample)
	}
	maxRetries = DefaultOutputMaxRetries
	// An explicit zero disables retries; an absent value takes the default.
	if raw, present := parameters[OutputParserRetriesKey]; present && raw != nil {
		number, ok := raw.(float64)
		if !ok || number != float64(int(number)) || number < 0 {
			return nil, 0, fmt.Errorf("maxRetries must be a whole number of zero or more")
		}
		maxRetries = int(number)
	}
	return schema, maxRetries, nil
}

// parseJSONSchema decodes a schema document and checks the shape this
// package validates against, naming the offending path.
func parseJSONSchema(raw []byte) (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("jsonSchema is not valid JSON: %w", err)
	}
	if err := checkSchemaShape(schema, "jsonSchema"); err != nil {
		return nil, err
	}
	return schema, nil
}

// schemaText reads a schema or example value as text. An imported node may
// carry the decoded object rather than the source string; re-marshalling
// accepts both without a translation table.
func schemaText(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	if value == nil {
		return "", fmt.Errorf("a value is required")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode value: %w", err)
	}
	return string(encoded), nil
}

// checkSchemaShape enforces the shape of every keyword the validator below
// reads, naming the offending path. A keyword it would misread is refused at
// save, because a constraint that silently stops applying is how an
// out-of-range answer passes as success. Keywords it does not read ($ref,
// format, descriptions) are ignored rather than refused: a schema authored
// for a fuller validator still constrains what this one checks.
func checkSchemaShape(schema map[string]any, path string) error {
	if schemaType, present := schema["type"]; present {
		if _, err := schemaTypes(schemaType); err != nil {
			return fmt.Errorf("%s.type %s", path, err)
		}
	}
	if err := checkSubschemaMap(schema, "properties", path); err != nil {
		return err
	}
	if err := checkSubschemaMap(schema, "patternProperties", path); err != nil {
		return err
	}
	if patterns, ok := schema["patternProperties"].(map[string]any); ok {
		for _, pattern := range sortedKeys(patterns) {
			if _, err := compilePattern(pattern); err != nil {
				return fmt.Errorf("%s.patternProperties key %q is not a valid regular expression: %w", path, pattern, err)
			}
		}
	}
	if additional, present := schema["additionalProperties"]; present {
		switch typed := additional.(type) {
		case bool:
		case map[string]any:
			if err := checkSchemaShape(typed, path+".additionalProperties"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s.additionalProperties must be a boolean or an object", path)
		}
	}
	if required, present := schema["required"]; present {
		names, ok := required.([]any)
		if !ok {
			return fmt.Errorf("%s.required must be an array of strings", path)
		}
		for index, name := range names {
			if _, ok := name.(string); !ok {
				return fmt.Errorf("%s.required[%d] must be a string", path, index)
			}
		}
	}
	for _, keyword := range []string{"items", "not"} {
		if member, present := schema[keyword]; present {
			memberSchema, ok := member.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s must be an object", path, keyword)
			}
			if err := checkSchemaShape(memberSchema, path+"."+keyword); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		members, present := schema[keyword]
		if !present {
			continue
		}
		alternatives, ok := members.([]any)
		if !ok || len(alternatives) == 0 {
			return fmt.Errorf("%s.%s must be a non-empty array of schemas", path, keyword)
		}
		for index, member := range alternatives {
			memberPath := fmt.Sprintf("%s.%s[%d]", path, keyword, index)
			memberSchema, ok := member.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be an object", memberPath)
			}
			if err := checkSchemaShape(memberSchema, memberPath); err != nil {
				return err
			}
		}
	}
	if enum, present := schema["enum"]; present {
		if _, ok := enum.([]any); !ok {
			return fmt.Errorf("%s.enum must be an array", path)
		}
	}
	for _, keyword := range []string{"minimum", "maximum"} {
		if bound, present := schema[keyword]; present {
			if _, ok := bound.(float64); !ok {
				return fmt.Errorf("%s.%s must be a number", path, keyword)
			}
		}
	}
	// Draft 4 wrote the exclusive bounds as booleans beside minimum and
	// maximum; later drafts write them as numbers. Both are in the wild.
	for _, keyword := range []string{"exclusiveMinimum", "exclusiveMaximum"} {
		if bound, present := schema[keyword]; present {
			switch bound.(type) {
			case float64, bool:
			default:
				return fmt.Errorf("%s.%s must be a number", path, keyword)
			}
		}
	}
	if step, present := schema["multipleOf"]; present {
		if number, ok := step.(float64); !ok || number <= 0 {
			return fmt.Errorf("%s.multipleOf must be a number greater than zero", path)
		}
	}
	for _, keyword := range []string{"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
		if bound, present := schema[keyword]; present {
			if number, ok := bound.(float64); !ok || number < 0 || number != float64(int64(number)) {
				return fmt.Errorf("%s.%s must be a whole number of zero or more", path, keyword)
			}
		}
	}
	if pattern, present := schema["pattern"]; present {
		text, ok := pattern.(string)
		if !ok {
			return fmt.Errorf("%s.pattern must be a string", path)
		}
		if _, err := compilePattern(text); err != nil {
			return fmt.Errorf("%s.pattern is not a valid regular expression: %w", path, err)
		}
	}
	for _, keyword := range []string{"uniqueItems", "nullable"} {
		if flag, present := schema[keyword]; present {
			if _, ok := flag.(bool); !ok {
				return fmt.Errorf("%s.%s must be a boolean", path, keyword)
			}
		}
	}
	return nil
}

// checkSubschemaMap checks a keyword whose value maps names to schemas.
func checkSubschemaMap(schema map[string]any, keyword, path string) error {
	value, present := schema[keyword]
	if !present {
		return nil
	}
	members, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s.%s must be an object", path, keyword)
	}
	for _, name := range sortedKeys(members) {
		member, ok := members[name].(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s.%s must be an object", path, keyword, name)
		}
		if err := checkSchemaShape(member, path+"."+keyword+"."+name); err != nil {
			return err
		}
	}
	return nil
}

// schemaTypes reads a type keyword: one type name, or a list of them, which
// is how JSON Schema spells a nullable field (["string", "null"]).
func schemaTypes(value any) ([]string, error) {
	var names []string
	switch typed := value.(type) {
	case string:
		names = []string{typed}
	case []any:
		if len(typed) == 0 {
			return nil, fmt.Errorf("must name at least one type")
		}
		for _, member := range typed {
			name, ok := member.(string)
			if !ok {
				return nil, fmt.Errorf("must be a type name or an array of type names")
			}
			names = append(names, name)
		}
	case []string:
		names = typed
	default:
		return nil, fmt.Errorf("must be a type name or an array of type names")
	}
	for _, name := range names {
		switch name {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return nil, fmt.Errorf("%q is not a known JSON type", name)
		}
	}
	return names, nil
}

// patterns caches compiled pattern keywords: a schema is validated once per
// model answer, and recompiling the same expression for every answer is
// waste.
var patterns sync.Map

// compilePattern compiles a pattern keyword. Go's RE2 syntax covers the
// patterns schemas actually carry (anchors, classes, repetition); one that
// needs lookaround is refused at save rather than silently unenforced.
func compilePattern(pattern string) (*regexp.Regexp, error) {
	if cached, found := patterns.Load(pattern); found {
		return cached.(*regexp.Regexp), nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patterns.Store(pattern, compiled)
	return compiled, nil
}

// inferJSONSchema derives a schema from an example document: objects
// constrain their observed properties, arrays their first element, and
// scalars their own type.
func inferJSONSchema(example any) map[string]any {
	switch typed := example.(type) {
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		properties := make(map[string]any, len(typed))
		required := make([]string, 0, len(typed))
		for _, name := range names {
			properties[name] = inferJSONSchema(typed[name])
			required = append(required, name)
		}
		return map[string]any{"type": "object", "properties": properties, "required": required}
	case []any:
		items := map[string]any{}
		if len(typed) > 0 {
			items = inferJSONSchema(typed[0])
		}
		return map[string]any{"type": "array", "items": items}
	case string:
		return map[string]any{"type": "string"}
	case bool:
		return map[string]any{"type": "boolean"}
	case float64:
		if typed == float64(int64(typed)) {
			return map[string]any{"type": "integer"}
		}
		return map[string]any{"type": "number"}
	case nil:
		return map[string]any{"type": "null"}
	default:
		return map[string]any{}
	}
}

// ParseAndValidateOutput parses a model response against a schema. Fenced
// code blocks unwrap first, because a model that forgot the tool wrapper
// usually still answered in JSON. The error carries both the validation
// failure and the raw text, so the failure is diagnosable.
func ParseAndValidateOutput(schema map[string]any, raw string) (any, error) {
	text := unwrapCodeFence(raw)
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("output is not valid JSON: %w; raw output: %s", err, raw)
	}
	if err := ValidateValue(schema, value, "output"); err != nil {
		return nil, fmt.Errorf("%w; raw output: %s", err, raw)
	}
	return value, nil
}

// unwrapCodeFence removes one surrounding ``` fence, with or without a
// language tag, so ```json … ``` still parses.
func unwrapCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}
	lines = lines[1:]
	if last := strings.TrimSpace(lines[len(lines)-1]); last == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ValidateValue checks a decoded value against a schema, naming the
// offending path. It enforces the keywords a structured output schema
// carries: type (one name or a nullable list), enum, const, the numeric
// ranges, string length and pattern, array and object bounds, required,
// properties, additionalProperties, and allOf/anyOf/oneOf/not. It ignores
// the rest ($ref, format).
//
// Every violation is reported, not just the first, so one repair turn can
// fix them all instead of spending a retry per mistake.
func ValidateValue(schema map[string]any, value any, path string) error {
	violations := schemaViolations(schema, value, path)
	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}

// schemaViolations lists every way value breaks schema, each prefixed with
// the path it occurs at.
func schemaViolations(schema map[string]any, value any, path string) []string {
	if len(schema) == 0 {
		return nil
	}
	// OpenAPI's nullable: true, which tool-schema authors carry over, admits
	// null whatever else the schema says.
	if nullable, _ := schema["nullable"].(bool); nullable && value == nil {
		return nil
	}
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	if schemaType, present := schema["type"]; present {
		names, _ := schemaTypes(schemaType)
		if len(names) > 0 && !matchesAnyType(names, value) {
			// Every other keyword constrains a value of the right type, so a
			// wrong type is the one thing worth saying about this value.
			report("want %s, got %s", strings.Join(names, " or "), jsonTypeName(value))
			return violations
		}
	}
	if allowed, present := schema["enum"]; present {
		options, _ := allowed.([]any)
		matched := false
		for _, option := range options {
			if jsonEqual(option, value) {
				matched = true
				break
			}
		}
		if !matched {
			report("value %s is not one of the allowed options %s", jsonText(value), jsonText(options))
		}
	}
	if constant, present := schema["const"]; present && !jsonEqual(constant, value) {
		report("value %s is not the required value %s", jsonText(value), jsonText(constant))
	}
	switch typed := value.(type) {
	case float64:
		violations = append(violations, numberViolations(schema, typed, path)...)
	case string:
		violations = append(violations, stringViolations(schema, typed, path)...)
	case []any:
		violations = append(violations, arrayViolations(schema, typed, path)...)
	case map[string]any:
		violations = append(violations, objectViolations(schema, typed, path)...)
	}
	return append(violations, combinatorViolations(schema, value, path)...)
}

func matchesAnyType(names []string, value any) bool {
	for _, name := range names {
		if matchesType(name, value) {
			return true
		}
	}
	return false
}

func matchesType(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == math.Trunc(number)
	case "number":
		_, ok := value.(float64)
		return ok
	case "null":
		return value == nil
	}
	return true
}

func numberViolations(schema map[string]any, number float64, path string) []string {
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	minimum, hasMinimum := schema["minimum"].(float64)
	maximum, hasMaximum := schema["maximum"].(float64)
	// Draft 4's boolean exclusive bounds turn minimum and maximum exclusive.
	exclusiveFloor, _ := schema["exclusiveMinimum"].(bool)
	exclusiveRoof, _ := schema["exclusiveMaximum"].(bool)
	switch {
	case hasMinimum && exclusiveFloor && number <= minimum:
		report("%v must be greater than %v", number, minimum)
	case hasMinimum && number < minimum:
		report("%v is less than the minimum %v", number, minimum)
	}
	switch {
	case hasMaximum && exclusiveRoof && number >= maximum:
		report("%v must be less than %v", number, maximum)
	case hasMaximum && number > maximum:
		report("%v is greater than the maximum %v", number, maximum)
	}
	if floor, ok := schema["exclusiveMinimum"].(float64); ok && number <= floor {
		report("%v must be greater than %v (exclusiveMinimum)", number, floor)
	}
	if roof, ok := schema["exclusiveMaximum"].(float64); ok && number >= roof {
		report("%v must be less than %v (exclusiveMaximum)", number, roof)
	}
	if step, ok := schema["multipleOf"].(float64); ok && step > 0 {
		// A tolerance, because 1.5 / 0.1 is not a whole number in binary
		// floating point and a model writing 1.5 did nothing wrong.
		quotient := number / step
		if math.Abs(quotient-math.Round(quotient)) > 1e-9 {
			report("%v is not a multiple of %v", number, step)
		}
	}
	return violations
}

func stringViolations(schema map[string]any, text string, path string) []string {
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	// JSON Schema counts string length in code points, not bytes.
	length := utf8.RuneCountInString(text)
	if bound, ok := schema["minLength"].(float64); ok && float64(length) < bound {
		report("%d characters is shorter than the minimum length %v", length, bound)
	}
	if bound, ok := schema["maxLength"].(float64); ok && float64(length) > bound {
		report("%d characters is longer than the maximum length %v", length, bound)
	}
	if pattern, ok := schema["pattern"].(string); ok {
		// An expression that does not compile was refused at save; one that
		// reached here unchecked cannot constrain anything.
		if compiled, err := compilePattern(pattern); err == nil && !compiled.MatchString(text) {
			report("%s does not match the pattern %s", jsonText(text), pattern)
		}
	}
	return violations
}

func arrayViolations(schema map[string]any, items []any, path string) []string {
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	if bound, ok := schema["minItems"].(float64); ok && float64(len(items)) < bound {
		report("%d items is fewer than the minimum %v", len(items), bound)
	}
	if bound, ok := schema["maxItems"].(float64); ok && float64(len(items)) > bound {
		report("%d items is more than the maximum %v", len(items), bound)
	}
	if unique, _ := schema["uniqueItems"].(bool); unique {
	duplicates:
		for later := range items {
			for earlier := range later {
				if jsonEqual(items[earlier], items[later]) {
					report("items %d and %d are equal, want unique items", earlier, later)
					break duplicates
				}
			}
		}
	}
	memberSchema, _ := schema["items"].(map[string]any)
	for index, member := range items {
		violations = append(violations, schemaViolations(memberSchema, member, fmt.Sprintf("%s[%d]", path, index))...)
	}
	return violations
}

func objectViolations(schema map[string]any, object map[string]any, path string) []string {
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	var required []string
	switch typed := schema["required"].(type) {
	case []any:
		for _, name := range typed {
			key, _ := name.(string)
			required = append(required, key)
		}
	case []string:
		required = typed
	}
	for _, key := range required {
		if _, found := object[key]; !found {
			report("missing required property %q", key)
		}
	}
	if bound, ok := schema["minProperties"].(float64); ok && float64(len(object)) < bound {
		report("%d properties is fewer than the minimum %v", len(object), bound)
	}
	if bound, ok := schema["maxProperties"].(float64); ok && float64(len(object)) > bound {
		report("%d properties is more than the maximum %v", len(object), bound)
	}
	properties, _ := schema["properties"].(map[string]any)
	patternSchemas, _ := schema["patternProperties"].(map[string]any)
	for _, name := range sortedKeys(object) {
		member := object[name]
		memberPath := path + "." + name
		declared := false
		if memberSchema, found := properties[name]; found {
			declared = true
			typed, _ := memberSchema.(map[string]any)
			violations = append(violations, schemaViolations(typed, member, memberPath)...)
		}
		for _, pattern := range sortedKeys(patternSchemas) {
			compiled, err := compilePattern(pattern)
			if err != nil || !compiled.MatchString(name) {
				continue
			}
			declared = true
			typed, _ := patternSchemas[pattern].(map[string]any)
			violations = append(violations, schemaViolations(typed, member, memberPath)...)
		}
		if declared {
			continue
		}
		switch additional := schema["additionalProperties"].(type) {
		case bool:
			if !additional {
				report("property %q is not allowed, the schema declares no such property", name)
			}
		case map[string]any:
			violations = append(violations, schemaViolations(additional, member, memberPath)...)
		}
	}
	return violations
}

// combinatorViolations applies allOf, anyOf, oneOf and not. A failed anyOf
// or oneOf states why each alternative failed, because "matches none" alone
// gives a model nothing to correct.
func combinatorViolations(schema map[string]any, value any, path string) []string {
	var violations []string
	report := func(format string, args ...any) {
		violations = append(violations, path+": "+fmt.Sprintf(format, args...))
	}
	for _, member := range subschemas(schema["allOf"]) {
		violations = append(violations, schemaViolations(member, value, path)...)
	}
	if alternatives := subschemas(schema["anyOf"]); len(alternatives) > 0 {
		var reasons []string
		for index, member := range alternatives {
			failures := schemaViolations(member, value, path)
			if len(failures) == 0 {
				reasons = nil
				break
			}
			reasons = append(reasons, fmt.Sprintf("alternative %d: %s", index+1, strings.Join(failures, ", ")))
		}
		if len(reasons) > 0 {
			report("value matches none of the anyOf alternatives (%s)", strings.Join(reasons, "; "))
		}
	}
	if alternatives := subschemas(schema["oneOf"]); len(alternatives) > 0 {
		var reasons []string
		matched := 0
		for index, member := range alternatives {
			failures := schemaViolations(member, value, path)
			if len(failures) == 0 {
				matched++
				continue
			}
			reasons = append(reasons, fmt.Sprintf("alternative %d: %s", index+1, strings.Join(failures, ", ")))
		}
		switch {
		case matched == 0:
			report("value matches none of the oneOf alternatives (%s)", strings.Join(reasons, "; "))
		case matched > 1:
			report("value matches %d of the oneOf alternatives, want exactly one", matched)
		}
	}
	if negated, ok := schema["not"].(map[string]any); ok && len(schemaViolations(negated, value, path)) == 0 {
		report("value matches the schema under not, which it must not")
	}
	return violations
}

// subschemas reads an allOf/anyOf/oneOf list, skipping members that are not
// schemas; the shape check refused those at save.
func subschemas(value any) []map[string]any {
	members, _ := value.([]any)
	schemas := make([]map[string]any, 0, len(members))
	for _, member := range members {
		if schema, ok := member.(map[string]any); ok {
			schemas = append(schemas, schema)
		}
	}
	return schemas
}

// jsonText renders a decoded value as JSON for a violation message.
func jsonText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func sortedKeys(members map[string]any) []string {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// jsonTypeName names a decoded JSON value's type for error messages.
func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}

// jsonEqual compares two decoded JSON values structurally.
func jsonEqual(first, second any) bool {
	firstEncoded, err := json.Marshal(first)
	if err != nil {
		return false
	}
	secondEncoded, err := json.Marshal(second)
	if err != nil {
		return false
	}
	return string(firstEncoded) == string(secondEncoded)
}
