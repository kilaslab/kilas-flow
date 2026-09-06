package expression

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ExecutionContext is the `$execution` root: identity a parameter may read
// without reaching into runtime internals.
type ExecutionContext struct {
	ID   string
	Mode string
	// ResumeURL is the per-run machine resume link ($execution.resumeUrl),
	// minted before the graph runs so a workflow can send it before
	// suspending. Empty when the service composed no links.
	ResumeURL string
	// ApprovalURL is the human page for the same token
	// ($execution.approvalUrl). Empty alongside ResumeURL.
	ApprovalURL string
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
	// NodeItems is every completed node's output with its `json` wrapper,
	// backing `$('Name')` and `$node["Name"].json.…`.
	NodeItems map[string]NodeItem
	// Workflow is the `$workflow` root.
	Workflow WorkflowContext
	// ItemIndex is the current item's position, `$itemIndex`.
	ItemIndex int
	// Now fixes the clock for `$now` and `$today`, so one evaluation of a
	// parameter tree sees a single instant and a test can pin it.
	Now time.Time
	// AllowFromAI permits `$fromAI`, which is only meaningful in a parameter an
	// AI agent fills. Anywhere else it is a clear error rather than a value,
	// or an author will use it in an HTTP URL and get something meaningless.
	AllowFromAI bool

	// --- Declarative routing ------------------------------------------------
	//
	// Two roots that exist for one caller: the interpreter in
	// `internal/routing`, which resolves a node pack's request templates. They
	// are deliberately absent from Roots(), so the editor never offers them and
	// no user-authored expression is written against them.
	//
	// The alternative was a second, private evaluator for routing templates.
	// That would have been a second attack surface over tenant-authored data
	// and would have drifted from this one within a release; two extra roots on
	// the single evaluator keep the property that a parameter can never become
	// code.

	// Parameters is the node's own already-resolved parameters, `$parameter`.
	Parameters map[string]any
	// Value is the property being sent, `$value`. It is what a `routing.send`
	// template rewrites — `{{ $value.trim() }}` — and is meaningless outside
	// one, which is why it is gated with the other two.
	Value any
	// Credentials is the **non-secret** half of the node's credential,
	// `$credentials` — a base URL, never a token. The filtering happens at the
	// caller, from the credential type's own field descriptors, so a type this
	// evaluator has never heard of exposes nothing rather than everything.
	Credentials map[string]string
	// AllowRouting permits the two roots above. Without it they are an error
	// naming where they are valid, rather than resolving to an empty map and
	// letting a template silently produce a URL with a hole in it.
	AllowRouting bool
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
		value, err := lookup(segments[0].text, ctx)
		if err != nil {
			return nil, err
		}
		if IsUndefined(value) {
			// A lone expression that resolved to nothing is null, which is what
			// a JSON parameter can actually carry.
			return nil, nil
		}
		return value, nil
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
	// call marks an accessor that is a function call rather than a field read.
	// The name is resolved against the closed allowlist at parse time.
	call bool
	args []argument
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
		// Reading through an absent value yields absent rather than erroring,
		// which is what makes `$json.a.b.c` safe when `a` is optional.
		if IsUndefined(current) {
			return Undefined, nil
		}
		// `.item` on a node whose lineage is unknown fails here rather than
		// earlier, so a workflow that never reads it is unaffected.
		if failure, unavailable := current.(lineageError); unavailable {
			return nil, fmt.Errorf("%s: %s", path, failure.reason)
		}
		if step.call {
			entry := functions[step.name]
			value, err := entry.apply(current, step.args)
			if err != nil {
				return nil, fmt.Errorf("%s.%s(): %w", path, step.name, err)
			}
			path += "." + step.name + "()"
			current = value
			continue
		}
		if step.byKey {
			path += "." + step.name
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", path)
			}
			current, ok = object[step.name]
			if !ok {
				// Absent, not invalid. Every *structural* error below stays a
				// hard failure — the shape of the expression was wrong — but a
				// field that simply is not there on this item is the ordinary
				// case an optional field produces.
				return Undefined, nil
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
	if failure, unavailable := current.(lineageError); unavailable {
		return nil, fmt.Errorf("%s: %s", path, failure.reason)
	}
	return current, nil
}

// parse reads one expression body into a root and a chain of accessors.
//
// The grammar is deliberately not general. A root, then field reads, index
// reads and calls from a closed allowlist — no operators, no bare identifiers,
// no arbitrary calls. `require('fs')` is not rejected by a denylist: it cannot
// be written at all, because a body that does not begin with a supported root
// never parses.
func parse(body string) (string, []accessor, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil, fmt.Errorf("expression is empty")
	}
	if !strings.HasPrefix(body, "$") {
		return "", nil, fmt.Errorf("expression must start with a supported root such as $json")
	}

	var root string
	position := 1

	// `$('Node')` and `$fromAI('name')` take a quoted argument in the root
	// itself. It stays a special case in the root parser rather than opening
	// the grammar to general calls, which is the property that keeps an
	// expression unable to do anything but read data.
	if position < len(body) && body[position] == '(' {
		name, next, err := readCallArguments(body, position)
		if err != nil {
			return "", nil, err
		}
		if len(name) != 1 || name[0].isNumber {
			return "", nil, fmt.Errorf("$(…) takes one quoted node name")
		}
		accessors, err := parseAccessors(body, next)
		return nodeRootPrefix + name[0].text, accessors, err
	}
	for position < len(body) && isNameByte(body[position]) {
		position++
	}
	root = body[:position]

	if root == fromAIRoot {
		if position >= len(body) || body[position] != '(' {
			return "", nil, fmt.Errorf("$fromAI needs a quoted parameter name")
		}
		args, next, err := readCallArguments(body, position)
		if err != nil {
			return "", nil, err
		}
		if len(args) == 0 || args[0].isNumber {
			return "", nil, fmt.Errorf("$fromAI needs a quoted parameter name")
		}
		encoded := fromAIRoot + "(" + args[0].text
		if len(args) > 1 {
			encoded += "\x00" + args[1].text
		}
		accessors, err := parseAccessors(body, next)
		return encoded, accessors, err
	}

	accessors, err := parseAccessors(body, position)
	return root, accessors, err
}

// parseAccessors reads the field, index and call chain after a root.
// singleQuoted reads a JavaScript-style '…' string, of any length.
//
// strconv.Unquote reads '…' as a Go rune literal, so it accepts 'a' and refuses
// 'Day of week'. JavaScript makes no such distinction, and the keys that need
// bracket access at all — a Schedule Trigger's `Day of week`, a header with a
// dash — are exactly the ones an author writes in single quotes.
func singleQuoted(text string) (string, bool) {
	if len(text) < 2 || text[0] != '\'' || text[len(text)-1] != '\'' {
		return "", false
	}
	inner := text[1 : len(text)-1]
	// An unescaped quote inside means this was never one string.
	if strings.Contains(strings.ReplaceAll(inner, "\\'", ""), "'") {
		return "", false
	}
	return strings.ReplaceAll(inner, "\\'", "'"), true
}

func parseAccessors(body string, position int) ([]accessor, error) {
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
				return nil, fmt.Errorf("expression has an empty field name")
			}
			name := body[start:position]
			if position < len(body) && body[position] == '(' {
				entry, known := functions[name]
				if !known {
					// Resolved here rather than at run time, so a workflow
					// naming a function that does not exist fails at save.
					return nil, fmt.Errorf("expression calls %s(), which is not an allowed function", name)
				}
				args, next, err := readCallArguments(body, position)
				if err != nil {
					return nil, err
				}
				if len(args) != entry.arity {
					return nil, fmt.Errorf("%s() takes %d argument(s), got %d", name, entry.arity, len(args))
				}
				accessors = append(accessors, accessor{name: name, call: true, args: args})
				position = next
				continue
			}
			accessors = append(accessors, accessor{name: name, byKey: true})
		case '[':
			position++
			end := strings.IndexByte(body[position:], ']')
			if end < 0 {
				return nil, fmt.Errorf("expression has an unclosed [")
			}
			inner := strings.TrimSpace(body[position : position+end])
			position += end + 1
			if quoted, ok := singleQuoted(inner); ok {
				accessors = append(accessors, accessor{name: quoted, byKey: true})
				continue
			}
			if quoted, err := strconv.Unquote(inner); err == nil {
				accessors = append(accessors, accessor{name: quoted, byKey: true})
				continue
			}
			index, err := strconv.Atoi(inner)
			if err != nil {
				return nil, fmt.Errorf("expression index must be a number or a quoted key")
			}
			accessors = append(accessors, accessor{index: index})
		default:
			return nil, fmt.Errorf("expression contains unsupported syntax at %q", body[position:])
		}
	}
	return accessors, nil
}

// readCallArguments reads a parenthesised list of *literals*.
//
// Only string and number literals are accepted. Allowing a nested expression
// would make this a general call expression, and the whole safety property of
// this grammar is that it is not one.
func readCallArguments(body string, open int) ([]argument, int, error) {
	depth := 0
	end := -1
	for index := open; index < len(body); index++ {
		switch body[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = index
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, 0, fmt.Errorf("expression has an unclosed (")
	}
	inner := strings.TrimSpace(body[open+1 : end])
	if inner == "" {
		return nil, end + 1, nil
	}

	var args []argument
	for _, raw := range splitArguments(inner) {
		raw = strings.TrimSpace(raw)
		if unquoted, err := strconv.Unquote(raw); err == nil {
			args = append(args, argument{text: unquoted})
			continue
		}
		if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
			args = append(args, argument{text: raw[1 : len(raw)-1]})
			continue
		}
		if number, err := strconv.ParseFloat(raw, 64); err == nil {
			args = append(args, argument{number: number, isNumber: true})
			continue
		}
		return nil, 0, fmt.Errorf("expression argument %q must be a quoted string or a number", raw)
	}
	return args, end + 1, nil
}

// splitArguments splits on commas that are not inside a quoted string.
func splitArguments(inner string) []string {
	var parts []string
	var current strings.Builder
	var quote byte
	for index := 0; index < len(inner); index++ {
		char := inner[index]
		switch {
		case quote != 0:
			current.WriteByte(char)
			if char == quote && (index == 0 || inner[index-1] != '\\') {
				quote = 0
			}
		case char == '\'' || char == '"':
			quote = char
			current.WriteByte(char)
		case char == ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(char)
		}
	}
	parts = append(parts, current.String())
	return parts
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
		// Both forms resolve. `$node["X"].json.y` is what every imported n8n
		// workflow is written as and never worked; `$node["X"].y` is what
		// KilasFlow's own docs wrongly advertised and what existing workflows
		// may use. The wrapper carries `json` alongside the bare fields, so
		// neither breaks — a field genuinely named `json` on an item is the one
		// ambiguity, and the wrapper wins because that is the n8n meaning.
		nodes := make(map[string]any, len(ctx.NodeItems))
		for name, item := range ctx.NodeItems {
			merged := make(map[string]any, len(item.JSON)+3)
			for key, value := range item.JSON {
				merged[key] = value
			}
			for key, value := range nodeItemValue(item) {
				merged[key] = value
			}
			nodes[name] = merged
		}
		for name, item := range ctx.Nodes {
			if _, already := nodes[name]; already {
				continue
			}
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
		return map[string]any{"id": ctx.Execution.ID, "mode": ctx.Execution.Mode, "resumeUrl": ctx.Execution.ResumeURL, "approvalUrl": ctx.Execution.ApprovalURL}, nil
	case "$workflow":
		return map[string]any{"id": ctx.Workflow.ID, "name": ctx.Workflow.Name, "active": ctx.Workflow.Active}, nil
	case "$itemIndex":
		return float64(ctx.ItemIndex), nil
	case "$parameter":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", root)
		}
		return anyMap(ctx.Parameters), nil
	case "$value":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", root)
		}
		return ctx.Value, nil
	case "$credentials":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", root)
		}
		// Only what the caller put here, which is only what the credential type
		// declared non-secret.
		fields := make(map[string]any, len(ctx.Credentials))
		for key, value := range ctx.Credentials {
			fields[key] = value
		}
		return fields, nil
	case "$now":
		return dateValue{at: ctx.clock()}, nil
	case "$today":
		return dateValue{at: startOfDay(ctx.clock())}, nil
	default:
		if strings.HasPrefix(root, nodeRootPrefix) {
			return nodeRootValue(root, ctx)
		}
		if strings.HasPrefix(root, fromAIRoot+"(") {
			return fromAIValue(root, ctx)
		}
		return nil, fmt.Errorf("expression root %q is not supported", root)
	}
}

// clock is the instant `$now` and `$today` read. One instant per evaluation, so
// two expressions in the same parameter tree cannot disagree about the time.
func (ctx Context) clock() time.Time {
	if ctx.Now.IsZero() {
		return time.Now().UTC()
	}
	return ctx.Now
}

// fromAIValue resolves the marker an AI agent fills in.
func fromAIValue(root string, ctx Context) (any, error) {
	if !ctx.AllowFromAI {
		return nil, fmt.Errorf("$fromAI is only available in a parameter an AI agent fills; it has no value here")
	}
	encoded := strings.TrimPrefix(root, fromAIRoot+"(")
	parts := strings.SplitN(encoded, "\x00", 2)
	request := FromAIRequest{Name: parts[0]}
	if len(parts) > 1 {
		request.Description = parts[1]
	}
	return request, nil
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
	case undefinedValue:
		// An absent field substitutes as nothing, so mixing it into text yields
		// the text rather than the word "undefined".
		return ""
	case dateValue:
		return typed.String()
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
