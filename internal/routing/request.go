package routing

import (
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
)

// plan is everything one node's properties add up to: the request to make, what
// to do with the response, and whether to page.
type plan struct {
	request     Request
	postReceive []PostReceive
	maxResults  int
	pagination  *Pagination
}

// buildPlan walks the node's properties in declaration order and folds each
// one's routing into the request.
//
// Declaration order is the contract, not an implementation detail: a
// declarative node is written so that `resource` narrows the URL, `operation`
// sets it, and the operation's own parameters fill the body — reordering that
// walk changes which override wins.
//
// A property that is not visible contributes nothing. That matters more than it
// looks: a node keeps the parameters of every resource it has ever been set to,
// so without the visibility check a Telegram node switched from Message to Chat
// would still send the message body it no longer shows.
func buildPlan(
	definition node.Definition,
	description *Node,
	parameters map[string]any,
	base expression.Context,
) (plan, error) {
	built := plan{request: cloneRequest(description.Defaults)}
	// The defaults are templates too — a pack whose base URL comes from the
	// credential writes `{{ $credentials.baseUrl }}` there, which is how one
	// pack serves every self-hosted instance of the same product.
	if err := built.request.resolveTemplates(templateEvaluator(base)); err != nil {
		return plan{}, fmt.Errorf("requestDefaults: %w", err)
	}
	version := definition.Version.String()

	for _, declared := range definition.Parameters {
		if !property.VisibleProperty(declared, parameters, version) {
			continue
		}
		value, present := parameterValue(declared, parameters)
		if !present {
			continue
		}
		if routing, found := description.Properties[declared.Key]; found {
			if err := built.apply(routing, value, base); err != nil {
				return plan{}, fmt.Errorf("property %q: %w", declared.Key, err)
			}
		}
		options, found := description.Options[declared.Key]
		if !found {
			continue
		}
		for _, selected := range selectedValues(value) {
			routing, found := options[selected]
			if !found {
				continue
			}
			if err := built.apply(routing, value, base); err != nil {
				return plan{}, fmt.Errorf("property %q option %q: %w", declared.Key, selected, err)
			}
		}
	}
	return built, nil
}

// apply folds one routing object into the plan.
func (built *plan) apply(routing Routing, value any, base expression.Context) error {
	// `$value` is bound to the property this routing belongs to, so a request
	// template and a send template written on the same property see the same
	// value.
	evaluate := templateEvaluator(withValue(base, value))
	if routing.Request != nil {
		if err := built.request.merge(*routing.Request, evaluate); err != nil {
			return err
		}
	}
	if routing.Send != nil {
		if err := built.send(*routing.Send, value, evaluate); err != nil {
			return err
		}
	}
	if routing.Output != nil {
		built.postReceive = append(built.postReceive, routing.Output.PostReceive...)
		if routing.Output.MaxResults > 0 {
			built.maxResults = routing.Output.MaxResults
		}
	}
	if routing.Operations != nil && routing.Operations.Pagination != nil {
		built.pagination = routing.Operations.Pagination
	}
	return nil
}

// send places one property's value into the body or the query string.
func (built *plan) send(instruction Send, value any, evaluate func(any) (any, error)) error {
	if strings.TrimSpace(instruction.Property) == "" {
		return fmt.Errorf("send needs a destination property")
	}
	sent := value
	if instruction.Value != "" {
		// The evaluator has `$value` bound to this property, so `{{ $value }}`
		// and `{{ $value.trim() }}` both work.
		rewritten, err := evaluate(instruction.Value)
		if err != nil {
			return err
		}
		sent = rewritten
	}

	destination := &built.request.Body
	if instruction.Type == "query" {
		destination = &built.request.Query
	}
	if *destination == nil {
		*destination = map[string]any{}
	}
	dotted := instruction.PropertyInDotNotation == nil || *instruction.PropertyInDotNotation
	setPath(*destination, instruction.Property, sent, dotted)
	return nil
}

// merge folds an override into the effective request.
//
// Scalars replace; maps merge key by key. That is the difference between an
// operation choosing its own URL — which must replace the default — and an
// operation adding a header, which must not erase the defaults' headers.
func (request *Request) merge(override Request, evaluate func(any) (any, error)) error {
	for _, field := range []struct {
		into *string
		from string
	}{
		{&request.Method, override.Method},
		{&request.BaseURL, override.BaseURL},
		{&request.URL, override.URL},
	} {
		if field.from == "" {
			continue
		}
		resolved, err := evaluate(field.from)
		if err != nil {
			return err
		}
		*field.into = stringOf(resolved)
	}
	for _, pair := range []struct {
		into *map[string]any
		from map[string]any
	}{
		{&request.Headers, override.Headers},
		{&request.Query, override.Query},
		{&request.Body, override.Body},
	} {
		if len(pair.from) == 0 {
			continue
		}
		if *pair.into == nil {
			*pair.into = map[string]any{}
		}
		for key, value := range pair.from {
			resolved, err := evaluate(value)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			(*pair.into)[key] = resolved
		}
	}
	return nil
}

// resolveTemplates evaluates every template in a request in place.
//
// Only the pack's own strings ever pass through here. A value a `routing.send`
// already placed is concrete data — often the user's own message text — and
// re-evaluating it would turn a message containing `{{` into a parse error, or
// worse, into an expression.
func (request *Request) resolveTemplates(evaluate func(any) (any, error)) error {
	for _, field := range []*string{&request.Method, &request.BaseURL, &request.URL} {
		if *field == "" {
			continue
		}
		resolved, err := evaluate(*field)
		if err != nil {
			return err
		}
		*field = stringOf(resolved)
	}
	for _, values := range []map[string]any{request.Headers, request.Query, request.Body} {
		for key, value := range values {
			resolved, err := evaluate(value)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			values[key] = resolved
		}
	}
	return nil
}

func cloneRequest(source Request) Request {
	cloned := Request{Method: source.Method, BaseURL: source.BaseURL, URL: source.URL}
	cloned.Headers = cloneAnyMap(source.Headers)
	cloned.Query = cloneAnyMap(source.Query)
	cloned.Body = cloneAnyMap(source.Body)
	return cloned
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// parameterValue reads a property's value, falling back to its declared
// default.
//
// The fallback is not a convenience: a declarative node's routing is written
// against the value the editor shows, and the editor shows the default for a
// property the user never touched. Skipping those would send a request missing
// exactly the fields nobody had to think about.
func parameterValue(declared node.PropertyDefinition, parameters map[string]any) (any, bool) {
	if value, present := parameters[declared.Key]; present && value != nil {
		return value, true
	}
	if declared.Default == nil {
		return nil, false
	}
	return declared.Default, true
}

// selectedValues reads which options are selected, for options and multiOptions
// alike.
func selectedValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return typed
	case []any:
		selected := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				selected = append(selected, text)
			}
		}
		return selected
	default:
		return nil
	}
}

// setPath writes into a nested map, creating the objects along the way.
//
// With dot notation off, the key is written whole — a destination genuinely
// named `user.name` is a real thing an API can want, and guessing wrong there
// silently sends a differently shaped body.
func setPath(target map[string]any, path string, value any, dotted bool) {
	if !dotted || !strings.Contains(path, ".") {
		target[path] = value
		return
	}
	segments := strings.Split(path, ".")
	current := target
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

// templateEvaluator resolves a routing template against one item.
//
// Only strings are templates. A number, a boolean or an already-structured
// object in routing metadata is data and is passed through: running it through
// the expression evaluator would turn a body value of `{{`, which some APIs
// genuinely take, into a parse error.
func templateEvaluator(base expression.Context) func(any) (any, error) {
	return func(value any) (any, error) {
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		return expression.Evaluate(text, base)
	}
}

// withValue rebinds `$value` for one `routing.send` template.
func withValue(base expression.Context, value any) expression.Context {
	base.Value = value
	return base
}
