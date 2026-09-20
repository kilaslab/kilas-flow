package expression

import (
	"fmt"
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

// Context supplies the approved expression roots. Anything absent here is
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
	// Workflow is the `$workflow` root, including the timezone the clock reads.
	Workflow WorkflowContext
	// ItemIndex is the current item's position, `$itemIndex`.
	ItemIndex int
	// RunIndex is which run of the current node this is, `$runIndex`.
	RunIndex int
	// Vars is the workflow's static variables, `$vars`. Empty when the runtime
	// keeps none, which is the current state of the product; the root exists so
	// a workflow written against n8n fails on a missing *key* rather than on a
	// missing root.
	Vars map[string]any
	// Now fixes the clock for `$now` and `$today`, so one evaluation of a
	// parameter tree sees a single instant and a test can pin it.
	Now time.Time
	// Timezone is the IANA zone the clock reads when Now is not pinned:
	// the workflow's settings.timezone.
	Timezone string
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
// intact, so a number stays a number and a date stays a date a node can use. A
// template mixing text and expressions returns a string.
func Evaluate(template string, ctx Context) (any, error) {
	segments, err := split(template)
	if err != nil {
		return nil, err
	}
	if len(segments) == 1 && segments[0].isExpression {
		value, err := evaluate(segments[0].text, ctx)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case undefinedValue:
			// A lone expression that resolved to nothing is null, which is what
			// a JSON parameter can actually carry.
			return nil, nil
		case dateValue:
			// A date never escapes as the evaluator's own struct: the Set node
			// marshalled it to "{}" and the DateTime and IF nodes refused it.
			// time.Time is what those nodes already accept, and it marshals as
			// an ISO timestamp with an offset.
			return typed.at, nil
		default:
			return value, nil
		}
	}

	var builder strings.Builder
	for _, segment := range segments {
		if !segment.isExpression {
			builder.WriteString(segment.text)
			continue
		}
		value, err := evaluate(segment.text, ctx)
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

func anyMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	return source
}

// stringify renders one expression inside surrounding text.
//
// An absent value substitutes as nothing rather than the word "undefined" — a
// URL with a hole in it is easier to spot than one with "undefined" in it — and
// everything else follows JavaScript: a list joins with commas, an object is
// JSON rather than Go's `map[k:v]` syntax.
func stringify(value any) string {
	if IsUndefined(value) {
		return ""
	}
	return jsString(value)
}
