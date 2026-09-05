package expression

import (
	"fmt"
	"strconv"
	"strings"
)

// ExecutionContext is the `$execution` root: identity a parameter may read
// without reaching into runtime internals.
type ExecutionContext struct {
	ID   string
	Mode string
}

// Context supplies the approved V1 expression roots. Anything absent here is
// unreachable from a parameter by construction.
type Context struct {
	// JSON is the current item, exposed as `$json`.
	JSON map[string]any
	// Input is every item on each input port, exposed as `$input`.
	Input map[string][]map[string]any
	// Nodes maps a node's display name to its last output item, `$node`.
	Nodes map[string]map[string]any
	// Env is the allowlisted environment, `$env`. The runtime decides what
	// enters this map; the evaluator never reads os.Environ itself.
	Env       map[string]string
	Execution ExecutionContext
	// ItemIndex is the current item's position, `$itemIndex`.
	ItemIndex int
}

const (
	modeKey        = "mode"
	valueKey       = "value"
	expressionMode = "expression"
)

// IsExpression reports whether a parameter value is the explicit expression
// marker `{"mode":"expression","value":"…"}`.
//
// Requiring the marker means a fixed string that happens to contain `{{ }}` is
// still data. Nothing is evaluated by accident.
func IsExpression(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if mode, _ := object[modeKey].(string); mode != expressionMode {
		return false
	}
	_, isString := object[valueKey].(string)
	return isString
}

// Resolve evaluates every expression marker in a parameter tree and returns a
// plain value tree. Fixed values pass through untouched.
func Resolve(parameters map[string]any, ctx Context) (map[string]any, error) {
	resolved := make(map[string]any, len(parameters))
	for key, value := range parameters {
		evaluated, err := resolveValue(value, ctx)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", key, err)
		}
		resolved[key] = evaluated
	}
	return resolved, nil
}

func resolveValue(value any, ctx Context) (any, error) {
	if IsExpression(value) {
		template, _ := value.(map[string]any)[valueKey].(string)
		return Evaluate(template, ctx)
	}
	switch typed := value.(type) {
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, nested := range typed {
			evaluated, err := resolveValue(nested, ctx)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", key, err)
			}
			resolved[key] = evaluated
		}
		return resolved, nil
	case []any:
		resolved := make([]any, len(typed))
		for index, nested := range typed {
			evaluated, err := resolveValue(nested, ctx)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", index, err)
			}
			resolved[index] = evaluated
		}
		return resolved, nil
	default:
		return value, nil
	}
}

// Evaluate resolves `{{ … }}` segments in a template.
//
// A template that is exactly one expression returns that value with its type
// intact, so a number stays a number. A template mixing text and expressions
// returns a string.
func Evaluate(template string, ctx Context) (any, error) {
	segments, err := split(template)
	if err != nil {
		return nil, err
	}
	if len(segments) == 1 && segments[0].isExpression {
		return lookup(segments[0].text, ctx)
	}

	var builder strings.Builder
	for _, segment := range segments {
		if !segment.isExpression {
			builder.WriteString(segment.text)
			continue
		}
		value, err := lookup(segment.text, ctx)
		if err != nil {
			return nil, err
		}
		builder.WriteString(stringify(value))
	}
	return builder.String(), nil
}

type segment struct {
	text         string
	isExpression bool
}

// split separates literal text from `{{ … }}` expressions. An unterminated
// opener is an error rather than literal text, so a typo surfaces at save time
// instead of silently sending braces to an upstream API.
func split(template string) ([]segment, error) {
	segments := make([]segment, 0, 4)
	rest := template
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			if rest != "" {
				segments = append(segments, segment{text: rest})
			}
			break
		}
		if start > 0 {
			segments = append(segments, segment{text: rest[:start]})
		}
		remainder := rest[start+2:]
		end := strings.Index(remainder, "}}")
		if end < 0 {
			return nil, fmt.Errorf("expression is missing its closing }}")
		}
		segments = append(segments, segment{text: strings.TrimSpace(remainder[:end]), isExpression: true})
		rest = remainder[end+2:]
	}
	if len(segments) == 0 {
		segments = append(segments, segment{text: ""})
	}
	return segments, nil
}

type accessor struct {
	name  string
	index int
	byKey bool
}

// lookup parses and evaluates one expression body. The grammar is a root
// followed by field and index accessors; there are no operators, calls, or
// bare identifiers, so an expression can only read data.
func lookup(body string, ctx Context) (any, error) {
	root, accessors, err := parse(body)
	if err != nil {
		return nil, err
	}

	current, err := rootValue(root, ctx)
	if err != nil {
		return nil, err
	}
	path := root
	for _, step := range accessors {
		if step.byKey {
			path += "." + step.name
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", path)
			}
			current, ok = object[step.name]
			if !ok {
				return nil, fmt.Errorf("%s is not set", path)
			}
			continue
		}
		path += "[" + strconv.Itoa(step.index) + "]"
		list, ok := current.([]any)
		if !ok {
			return nil, fmt.Errorf("%s is not a list", path)
		}
		if step.index < 0 || step.index >= len(list) {
			return nil, fmt.Errorf("%s is out of range", path)
		}
		current = list[step.index]
	}
	return current, nil
}

func parse(body string) (string, []accessor, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil, fmt.Errorf("expression is empty")
	}
	if !strings.HasPrefix(body, "$") {
		return "", nil, fmt.Errorf("expression must start with a supported root such as $json")
	}

	position := 1
	for position < len(body) && isNameByte(body[position]) {
		position++
	}
	root := body[:position]

	accessors := make([]accessor, 0, 4)
	for position < len(body) {
		switch body[position] {
		case '.':
			position++
			start := position
			for position < len(body) && isNameByte(body[position]) {
				position++
			}
			if start == position {
				return "", nil, fmt.Errorf("expression has an empty field name")
			}
			accessors = append(accessors, accessor{name: body[start:position], byKey: true})
		case '[':
			position++
			end := strings.IndexByte(body[position:], ']')
			if end < 0 {
				return "", nil, fmt.Errorf("expression has an unclosed [")
			}
			inner := strings.TrimSpace(body[position : position+end])
			position += end + 1
			if quoted, err := strconv.Unquote(inner); err == nil {
				accessors = append(accessors, accessor{name: quoted, byKey: true})
				continue
			}
			index, err := strconv.Atoi(inner)
			if err != nil {
				return "", nil, fmt.Errorf("expression index must be a number or a quoted key")
			}
			accessors = append(accessors, accessor{index: index})
		default:
			return "", nil, fmt.Errorf("expression contains unsupported syntax at %q", body[position:])
		}
	}
	return root, accessors, nil
}

func isNameByte(char byte) bool {
	return char == '_' ||
		(char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9')
}

func rootValue(root string, ctx Context) (any, error) {
	switch root {
	case "$json":
		return anyMap(ctx.JSON), nil
	case "$input":
		input := make(map[string]any, len(ctx.Input))
		for port, items := range ctx.Input {
			list := make([]any, len(items))
			for index, item := range items {
				list[index] = anyMap(item)
			}
			input[port] = list
		}
		return input, nil
	case "$node":
		nodes := make(map[string]any, len(ctx.Nodes))
		for name, item := range ctx.Nodes {
			nodes[name] = anyMap(item)
		}
		return nodes, nil
	case "$env":
		env := make(map[string]any, len(ctx.Env))
		for key, value := range ctx.Env {
			env[key] = value
		}
		return env, nil
	case "$execution":
		return map[string]any{"id": ctx.Execution.ID, "mode": ctx.Execution.Mode}, nil
	case "$itemIndex":
		return float64(ctx.ItemIndex), nil
	default:
		return nil, fmt.Errorf("expression root %q is not supported", root)
	}
}

func anyMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	return source
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
