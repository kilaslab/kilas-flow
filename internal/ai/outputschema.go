package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

// checkSchemaShape enforces the object/array fields a schema needs for the
// validator below to read it. Unknown keywords are ignored rather than
// refused: a schema authored for a fuller validator still constrains what
// this one checks.
func checkSchemaShape(schema map[string]any, path string) error {
	if schemaType, present := schema["type"]; present {
		name, ok := schemaType.(string)
		if !ok {
			return fmt.Errorf("%s.type must be a string", path)
		}
		switch name {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return fmt.Errorf("%s.type %q is not a known JSON type", path, name)
		}
	}
	if properties, present := schema["properties"]; present {
		members, ok := properties.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		names := make([]string, 0, len(members))
		for name := range members {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			member, ok := members[name].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := checkSchemaShape(member, path+".properties."+name); err != nil {
				return err
			}
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
	if items, present := schema["items"]; present {
		member, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be an object", path)
		}
		if err := checkSchemaShape(member, path+".items"); err != nil {
			return err
		}
	}
	return nil
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
// offending path. It covers the structural keywords parsers actually emit —
// type, properties, required, items, enum — and ignores the rest.
func ValidateValue(schema map[string]any, value any, path string) error {
	if schema == nil || len(schema) == 0 {
		return nil
	}
	if allowed, present := schema["enum"]; present {
		options, ok := allowed.([]any)
		if !ok {
			return fmt.Errorf("%s: enum must be an array", path)
		}
		for _, option := range options {
			if jsonEqual(option, value) {
				return nil
			}
		}
		return fmt.Errorf("%s: value is not one of the allowed options", path)
	}
	if schemaType, present := schema["type"]; present {
		name, _ := schemaType.(string)
		if err := checkJSONType(name, value, path); err != nil {
			return err
		}
		if name == "object" {
			return validateObject(schema, value, path)
		}
		if name == "array" {
			return validateArray(schema, value, path)
		}
		return nil
	}
	// No type keyword: the structural keywords still apply where the value
	// has the shape they constrain.
	if object, ok := value.(map[string]any); ok {
		if _, present := schema["properties"]; present {
			return validateObject(schema, object, path)
		}
	}
	if _, present := schema["items"]; present {
		if _, ok := value.([]any); ok {
			return validateArray(schema, value, path)
		}
	}
	return nil
}

func checkJSONType(name string, value any, path string) error {
	switch name {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("%s: want object, got %s", path, jsonTypeName(value))
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("%s: want array, got %s", path, jsonTypeName(value))
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: want string, got %s", path, jsonTypeName(value))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: want boolean, got %s", path, jsonTypeName(value))
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("%s: want integer, got %s", path, jsonTypeName(value))
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: want number, got %s", path, jsonTypeName(value))
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s: want null, got %s", path, jsonTypeName(value))
		}
	}
	return nil
}

func validateObject(schema map[string]any, value any, path string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if required, present := schema["required"]; present {
		var names []string
		switch typed := required.(type) {
		case []any:
			for _, name := range typed {
				key, _ := name.(string)
				names = append(names, key)
			}
		case []string:
			names = typed
		default:
			return fmt.Errorf("%s: required must be an array of strings", path)
		}
		for _, key := range names {
			if _, found := object[key]; !found {
				return fmt.Errorf("%s: missing required property %q", path, key)
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for _, name := range sortedKeys(properties) {
		if member, found := object[name]; found {
			memberSchema, _ := properties[name].(map[string]any)
			if err := ValidateValue(memberSchema, member, path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(schema map[string]any, value any, path string) error {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	memberSchema, _ := schema["items"].(map[string]any)
	for index, member := range items {
		if err := ValidateValue(memberSchema, member, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
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
