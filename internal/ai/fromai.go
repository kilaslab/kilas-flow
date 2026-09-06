package ai

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// FromAIArgument is one $fromAI(key, description, type, defaultValue) call
// found in a tool's parameters.
//
// A tool parameter written as {{ $fromAI('city', 'the city to look up') }}
// contributes a typed property to the tool's JSON Schema, and the model's
// argument for that key replaces the call before the node executes. Without
// any such call the model's arguments arrive as $json, exactly as before.
type FromAIArgument struct {
	Key         string
	Description string
	// Type is one of string, number, boolean, json. It defaults to string.
	Type string
	// Default is the value used when the model omits the key. Nil means the
	// call declared no default and the argument is required.
	Default any
	// HasDefault reports whether the call declared a default at all, so a
	// default of false or zero stays distinguishable from no default.
	HasDefault bool
}

// fromAITypes are the types a $fromAI call may declare, spelled as n8n
// spells them. An unrecognised type fails validation with the call named.
var fromAITypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "json": true,
}

// ExtractFromAI walks a parameter tree and collects every $fromAI call in
// document order. A repeated key keeps its last occurrence, so redefining a
// key refines rather than duplicates its schema property.
func ExtractFromAI(parameters map[string]any) ([]FromAIArgument, error) {
	var collected []FromAIArgument
	if err := collectFromAI(parameters, &collected); err != nil {
		return nil, err
	}
	seen := make(map[string]int, len(collected))
	ordered := collected[:0]
	for _, argument := range collected {
		if index, duplicate := seen[argument.Key]; duplicate {
			ordered[index] = argument
			continue
		}
		seen[argument.Key] = len(ordered)
		ordered = append(ordered, argument)
	}
	return ordered, nil
}

func collectFromAI(value any, collected *[]FromAIArgument) error {
	switch typed := value.(type) {
	case string:
		calls, err := scanFromAICalls(typed)
		if err != nil {
			return err
		}
		for _, call := range calls {
			*collected = append(*collected, call.Argument)
		}
	case map[string]any:
		for _, nested := range typed {
			if err := collectFromAI(nested, collected); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := collectFromAI(nested, collected); err != nil {
				return err
			}
		}
	}
	return nil
}

// fromAICall is one located call: its parsed argument and its span in the
// source string.
type fromAICall struct {
	Argument FromAIArgument
	Start    int
	End      int
}

// scanFromAICalls finds every $fromAI(...) call in a string. Detection is
// case-insensitive, so $fromai written in an imported workflow matches, and
// the argument list is parsed character by character rather than with a
// regex, because arguments contain quotes, escapes, and nested parentheses
// a regex gets wrong on real workflows.
func scanFromAICalls(source string) ([]fromAICall, error) {
	lowered := strings.ToLower(source)
	var calls []fromAICall
	search := 0
	for search < len(source) {
		relative := strings.Index(lowered[search:], "$fromai")
		if relative < 0 {
			break
		}
		callStart := search + relative
		open := callStart + len("$fromai")
		for open < len(source) && (source[open] == ' ' || source[open] == '\t' || source[open] == '\n' || source[open] == '\r') {
			open++
		}
		if open >= len(source) || source[open] != '(' {
			search = open
			continue
		}
		end, ok := matchParen(source, open)
		if !ok {
			return nil, fmt.Errorf("unclosed $fromAI call starting at offset %d", callStart)
		}
		argument, err := parseFromAIArguments(source[open+1 : end])
		if err != nil {
			return nil, err
		}
		calls = append(calls, fromAICall{Argument: argument, Start: callStart, End: end + 1})
		search = end + 1
	}
	return calls, nil
}

// matchParen finds the closer balancing the opener at index open, skipping
// over quoted sections and backslash escapes.
func matchParen(source string, open int) (int, bool) {
	depth := 0
	var quote byte
	escaped := false
	for index := open; index < len(source); index++ {
		char := source[index]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '"', '\'', '`':
			quote = char
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index, true
			}
		}
	}
	return 0, false
}

// parseFromAIArguments parses the inside of one call as
// key, description, type, defaultValue. Only the key is required; the type
// defaults to string.
func parseFromAIArguments(argsString string) (FromAIArgument, error) {
	raw := splitFromAIArgs(argsString)
	cleaned := make([]string, 0, len(raw))
	for _, arg := range raw {
		cleaned = append(cleaned, unquoteFromAIArg(strings.TrimSpace(arg)))
	}
	call := strings.TrimSpace(argsString)
	name := func() string {
		if len(cleaned) > 0 && cleaned[0] != "" {
			return fmt.Sprintf("$fromAI(%q, …)", cleaned[0])
		}
		return fmt.Sprintf("$fromAI(%s)", call)
	}
	if len(cleaned) == 0 || cleaned[0] == "" {
		return FromAIArgument{}, fmt.Errorf("%s: a key is required", name())
	}
	argument := FromAIArgument{Key: cleaned[0]}
	if len(cleaned) > 1 {
		argument.Description = cleaned[1]
	}
	argument.Type = "string"
	if len(cleaned) > 2 && cleaned[2] != "" {
		argument.Type = strings.ToLower(cleaned[2])
	}
	if !fromAITypes[argument.Type] {
		return FromAIArgument{}, fmt.Errorf("%s names unknown type %q, want string, number, boolean, or json", name(), cleaned[2])
	}
	if len(cleaned) > 3 {
		value, err := parseFromAIDefault(cleaned[3], argument.Type)
		if err != nil {
			return FromAIArgument{}, fmt.Errorf("%s: %w", name(), err)
		}
		argument.Default = value
		argument.HasDefault = true
	}
	return argument, nil
}

// splitFromAIArgs splits on commas outside quotes. A backslash escapes the
// next character, whatever it is.
func splitFromAIArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	var quote byte
	escaped := false
	for index := 0; index < len(argsString); index++ {
		char := argsString[index]
		if escaped {
			current.WriteByte(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			current.WriteByte(char)
			continue
		}
		switch char {
		case '"', '\'', '`':
			quote = char
			current.WriteByte(char)
		case ',':
			args = append(args, current.String())
			current.Reset()
		default:
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 || len(args) > 0 {
		args = append(args, current.String())
	}
	return args
}

// unquoteFromAIArg strips one pair of matching surrounding quotes.
func unquoteFromAIArg(arg string) string {
	if len(arg) >= 2 {
		first, last := arg[0], arg[len(arg)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') || (first == '`' && last == '`') {
			inner := arg[1 : len(arg)-1]
			inner = strings.ReplaceAll(inner, `\\`, "\x00")
			inner = strings.ReplaceAll(inner, `\'`, "'")
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, "\\`", "`")
			return strings.ReplaceAll(inner, "\x00", `\`)
		}
	}
	return arg
}

// parseFromAIDefault reads a default value in its declared type, preserving
// the type: a number default must arrive numeric, not as a string that
// happens to look like one.
func parseFromAIDefault(raw, argumentType string) (any, error) {
	switch argumentType {
	case "string":
		return raw, nil
	case "number":
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("default %q is not a number", raw)
		}
		return value, nil
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("default %q is not a boolean", raw)
		}
	case "json":
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("default %q is not valid JSON: %w", raw, err)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unknown type %q", argumentType)
	}
}

// FromAISchema builds the model-facing JSON Schema object for a tool's
// extracted calls. A key with a default is optional; one without is
// required. A json-typed key accepts anything, so it carries no type.
func FromAISchema(calls []FromAIArgument) map[string]any {
	properties := make(map[string]any, len(calls))
	var required []string
	for _, call := range calls {
		property := map[string]any{}
		switch call.Type {
		case "string":
			property["type"] = "string"
		case "number":
			property["type"] = "number"
		case "boolean":
			property["type"] = "boolean"
		}
		if call.Description != "" {
			property["description"] = call.Description
		}
		if call.HasDefault {
			property["default"] = call.Default
		} else {
			required = append(required, call.Key)
		}
		properties[call.Key] = property
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func SubstituteFromAI(parameters map[string]any, arguments map[string]any) (map[string]any, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	substituted, err := substituteFromAIValue(parameters, arguments)
	if err != nil {
		return nil, err
	}
	resolved, _ := substituted.(map[string]any)
	if resolved == nil {
		resolved = map[string]any{}
	}
	return resolved, nil
}

func substituteFromAIValue(value any, arguments map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		return substituteFromAIString(typed, arguments)
	case map[string]any:
		if mode, _ := typed["mode"].(string); mode == "expression" {
			if template, ok := typed["value"].(string); ok {
				return substituteFromAITemplate(template, arguments)
			}
		}
		resolved := make(map[string]any, len(typed))
		for key, nested := range typed {
			value, err := substituteFromAIValue(nested, arguments)
			if err != nil {
				return nil, err
			}
			resolved[key] = value
		}
		return resolved, nil
	case []any:
		resolved := make([]any, len(typed))
		for index, nested := range typed {
			value, err := substituteFromAIValue(nested, arguments)
			if err != nil {
				return nil, err
			}
			resolved[index] = value
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func substituteFromAIString(source string, arguments map[string]any) (any, error) {
	calls, err := scanFromAICalls(source)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return source, nil
	}
	if len(calls) == 1 && strings.TrimSpace(source) == strings.TrimSpace(source[calls[0].Start:calls[0].End]) {
		return fromAIValue(calls[0].Argument, arguments)
	}
	return renderFromAISegments(source, arguments)
}

func substituteFromAITemplate(template string, arguments map[string]any) (any, error) {
	calls, err := scanFromAICalls(template)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return map[string]any{"mode": "expression", "value": template}, nil
	}
	if only, ok := soleFromAICall(template, calls); ok {
		return fromAIValue(only, arguments)
	}
	rendered, err := renderFromAISegments(template, arguments)
	if err != nil {
		return nil, err
	}
	return map[string]any{"mode": "expression", "value": rendered}, nil
}

// soleFromAICall reports whether the template is exactly {{ one call }} and
// nothing else.
func soleFromAICall(template string, calls []fromAICall) (FromAIArgument, bool) {
	if len(calls) != 1 {
		return FromAIArgument{}, false
	}
	open := strings.Index(template, "{{")
	close := strings.LastIndex(template, "}}")
	if open < 0 || close < 0 || open+2 > close {
		return FromAIArgument{}, false
	}
	if strings.TrimSpace(template[:open]) != "" || strings.TrimSpace(template[close+2:]) != "" {
		return FromAIArgument{}, false
	}
	inner := template[open+2 : close]
	innerCalls, err := scanFromAICalls(inner)
	if err != nil || len(innerCalls) != 1 {
		return FromAIArgument{}, false
	}
	call := innerCalls[0]
	if strings.TrimSpace(inner[:call.Start]) != "" || strings.TrimSpace(inner[call.End:]) != "" {
		return FromAIArgument{}, false
	}
	return calls[0].Argument, true
}

// renderFromAISegments substitutes calls in a template that holds more than
// one bare call. A {{ }} segment holding exactly one call collapses to the
// rendered value with its braces gone, so the evaluator never sees it; a
// segment holding anything else substitutes inline and keeps its braces for
// the evaluator. Calls outside any segment substitute inline.
func renderFromAISegments(source string, arguments map[string]any) (string, error) {
	var rendered strings.Builder
	cursor := 0
	for cursor < len(source) {
		open := strings.Index(source[cursor:], "{{")
		if open < 0 {
			interpolated, err := interpolateFromAIRange(source, cursor, len(source), arguments)
			if err != nil {
				return "", err
			}
			rendered.WriteString(interpolated)
			break
		}
		open += cursor
		close := strings.Index(source[open+2:], "}}")
		if close < 0 {
			interpolated, err := interpolateFromAIRange(source, cursor, len(source), arguments)
			if err != nil {
				return "", err
			}
			rendered.WriteString(interpolated)
			break
		}
		close += open + 2
		inner := source[open+2 : close]
		innerCalls, err := scanFromAICalls(inner)
		if err != nil {
			return "", err
		}
		head, err := interpolateFromAIRange(source, cursor, open, arguments)
		if err != nil {
			return "", err
		}
		rendered.WriteString(head)
		if len(innerCalls) == 1 && isBareCall(inner, innerCalls[0]) {
			value, err := fromAIValue(innerCalls[0].Argument, arguments)
			if err != nil {
				return "", err
			}
			rendered.WriteString(renderFromAIText(value))
		} else if len(innerCalls) > 0 {
			interpolated, err := interpolateFromAIRange(source, open+2, close, arguments)
			if err != nil {
				return "", err
			}
			rendered.WriteString("{{")
			rendered.WriteString(interpolated)
			rendered.WriteString("}}")
		} else {
			rendered.WriteString(source[open : close+2])
		}
		cursor = close + 2
	}
	return rendered.String(), nil
}

// isBareCall reports whether a segment's whole content is one call.
func isBareCall(inner string, call fromAICall) bool {
	return strings.TrimSpace(inner[:call.Start]) == "" && strings.TrimSpace(inner[call.End:]) == ""
}

// interpolateFromAIRange substitutes the calls overlapping [start, end),
// shifting their spans by start. Calls outside the range are left for their
// own range to substitute.
func interpolateFromAIRange(source string, start, end int, arguments map[string]any) (string, error) {
	calls, err := scanFromAICalls(source[start:end])
	if err != nil {
		return "", err
	}
	var rendered strings.Builder
	cursor := start
	for _, call := range calls {
		value, err := fromAIValue(call.Argument, arguments)
		if err != nil {
			return "", err
		}
		rendered.WriteString(source[cursor : start+call.Start])
		rendered.WriteString(renderFromAIText(value))
		cursor = start + call.End
	}
	rendered.WriteString(source[cursor:end])
	return rendered.String(), nil
}

// renderFromAIText renders one argument as template text: strings verbatim,
// anything else as compact JSON.
func renderFromAIText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// fromAIValue resolves one call to the model's argument, or to its default
// when the model omitted the key.
func fromAIValue(argument FromAIArgument, arguments map[string]any) (any, error) {
	if value, present := arguments[argument.Key]; present && value != nil {
		return value, nil
	}
	if argument.HasDefault {
		return argument.Default, nil
	}
	return nil, fmt.Errorf("tool argument %q was not supplied and $fromAI(%q, …) declares no default", argument.Key, argument.Key)
}
