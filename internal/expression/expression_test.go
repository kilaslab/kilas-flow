package expression_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/expression"
)

func testContext() expression.Context {
	return expression.Context{
		JSON: map[string]any{
			"name":  "Ada",
			"count": float64(3),
			"tags":  []any{"alpha", "beta"},
			"nested": map[string]any{
				"deep": map[string]any{"value": true},
			},
			"odd key": "spaced",
		},
		Input: map[string][]map[string]any{
			"main": {{"name": "Ada"}, {"name": "Grace"}},
		},
		Nodes: map[string]map[string]any{
			"Get User": {"id": "user-42"},
		},
		Env:       map[string]string{"REGION": "eu-west-1"},
		Execution: expression.ExecutionContext{ID: "exec_1", Mode: "manual"},
		ItemIndex: 1,
	}
}

func TestEvaluateResolvesTheApprovedRoots(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		"{{ $json.name }}":              "Ada",
		"{{ $json.count }}":             float64(3),
		"{{ $json.tags[1] }}":           "beta",
		"{{ $json.nested.deep.value }}": true,
		`{{ $json["odd key"] }}`:        "spaced",
		"{{ $input.main[1].name }}":     "Grace",
		`{{ $node["Get User"].id }}`:    "user-42",
		"{{ $env.REGION }}":             "eu-west-1",
		"{{ $execution.id }}":           "exec_1",
		"{{ $execution.mode }}":         "manual",
		"{{ $itemIndex }}":              float64(1),
	} {
		got, err := expression.Evaluate(template, testContext())
		if err != nil {
			t.Errorf("Evaluate(%q) error = %v", template, err)
			continue
		}
		if got != want {
			t.Errorf("Evaluate(%q) = %#v, want %#v", template, got, want)
		}
	}
}

func TestEvaluateKeepsTypeForASingleExpressionAndStringifiesMixedText(t *testing.T) {
	t.Parallel()

	value, err := expression.Evaluate("{{ $json.count }}", testContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if _, ok := value.(float64); !ok {
		t.Fatalf("single expression = %T, want the underlying number", value)
	}

	mixed, err := expression.Evaluate("https://api.test/users/{{ $json.name }}?n={{ $json.count }}", testContext())
	if err != nil {
		t.Fatalf("Evaluate() mixed error = %v", err)
	}
	if mixed != "https://api.test/users/Ada?n=3" {
		t.Fatalf("mixed template = %#v, want the interpolated string", mixed)
	}
}

func TestEvaluateRejectsAnythingThatIsNotDataAccess(t *testing.T) {
	t.Parallel()

	// The grammar exists to keep tenant-authored parameters from becoming code.
	for _, template := range []string{
		"{{ process.exit(1) }}",
		"{{ require('fs') }}",
		"{{ fetch('http://x') }}",
		"{{ $json.name; drop() }}",
		"{{ this }}",
		"{{ globalThis }}",
		"{{ $unknown.value }}",
		"{{ }}",
		"{{ $json..name }}",
		"{{ $json.name }",
	} {
		if _, err := expression.Evaluate(template, testContext()); err == nil {
			t.Errorf("Evaluate(%q) succeeded, want a rejection", template)
		}
	}
}

// TestEvaluateTreatsAMissingPathAsUndefined is the behaviour n8n has and
// KilasFlow did not.
//
// A missing path used to fail the whole execution. Real workflows lean on
// optional fields constantly, so a field absent on some items has to produce an
// empty value rather than stopping the run — while everything structurally
// wrong stays a hard failure, which the test below pins.
func TestEvaluateTreatsAMissingPathAsUndefined(t *testing.T) {
	t.Parallel()

	// A lone expression that resolved to nothing is null, which is what a JSON
	// parameter can carry.
	value, err := expression.Evaluate("{{ $json.missing.deeper }}", testContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want a missing path to be undefined", err)
	}
	if value != nil {
		t.Errorf("value = %#v, want nil", value)
	}

	// Mixed with text it substitutes as nothing, rather than the word
	// "undefined" or an error.
	mixed, err := expression.Evaluate("name: {{ $json.missing }}!", testContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if mixed != "name: !" {
		t.Errorf("value = %#v, want %q", mixed, "name: !")
	}
}

// TestEvaluateStillFailsLoudlyOnRealErrors keeps the other half. Undefined is
// for a field that is absent, never for an expression that is wrong.
func TestEvaluateStillFailsLoudlyOnRealErrors(t *testing.T) {
	t.Parallel()

	for name, template := range map[string]string{
		"unsupported root":     "{{ $secrets.token }}",
		"unknown function":     "{{ $json.name.hack() }}",
		"call on a scalar":     "{{ $json.count.hack() }}",
		"call on an undefined": "{{ $json.missing.hack() }}",
		"unknown global":       "{{ Object.hack($json) }}",
		"missing $env key":     "{{ $env.NOT_ALLOWLISTED }}",
		"unclosed call":        "{{ $json.name.trim( }}",
		"statement separator":  "{{ $json.name; $json.count }}",
	} {
		if _, err := expression.Evaluate(template, testContext()); err == nil {
			t.Errorf("%s: Evaluate(%q) succeeded, want a hard failure", name, template)
		}
	}
}

func TestEvaluateLeavesTextWithoutExpressionsAlone(t *testing.T) {
	t.Parallel()

	got, err := expression.Evaluate("plain text with { braces }", testContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got != "plain text with { braces }" {
		t.Errorf("Evaluate() = %#v, want the text unchanged", got)
	}
}

func TestResolveEvaluatesOnlyExplicitlyMarkedParameters(t *testing.T) {
	t.Parallel()

	resolved, err := expression.Resolve(map[string]any{
		"url":     map[string]any{"mode": "expression", "value": "https://api.test/{{ $json.name }}"},
		"method":  "GET",
		"literal": "{{ $json.name }}",
		"headers": map[string]any{
			"X-Region": map[string]any{"mode": "expression", "value": "{{ $env.REGION }}"},
			"X-Fixed":  "static",
		},
		"list": []any{map[string]any{"mode": "expression", "value": "{{ $json.count }}"}},
	}, testContext())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got := resolved["url"]; got != "https://api.test/Ada" {
		t.Errorf("url = %#v, want the evaluated expression", got)
	}
	if got := resolved["method"]; got != "GET" {
		t.Errorf("method = %#v, want the fixed value", got)
	}
	// A fixed string is data, even when it looks like an expression. Only the
	// explicit marker opts a parameter into evaluation.
	if got := resolved["literal"]; got != "{{ $json.name }}" {
		t.Errorf("literal = %#v, want the unevaluated fixed string", got)
	}
	headers, ok := resolved["headers"].(map[string]any)
	if !ok || headers["X-Region"] != "eu-west-1" || headers["X-Fixed"] != "static" {
		t.Errorf("headers = %#v, want the nested expression evaluated", resolved["headers"])
	}
	list, ok := resolved["list"].([]any)
	if !ok || len(list) != 1 || list[0] != float64(3) {
		t.Errorf("list = %#v, want the nested expression evaluated", resolved["list"])
	}
}

func TestResolveReportsWhichParameterFailed(t *testing.T) {
	t.Parallel()

	_, err := expression.Resolve(map[string]any{
		// An unsupported root, not a missing field: a missing field is now
		// undefined rather than an error, so the parameter that fails has to
		// fail for a structural reason.
		"url": map[string]any{"mode": "expression", "value": "{{ $secrets.token }}"},
	}, testContext())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("error = %q, want it to name the failing parameter", err)
	}
}

func TestIsExpressionRecognizesOnlyWellFormedMarkers(t *testing.T) {
	t.Parallel()

	if !expression.IsExpression(map[string]any{"mode": "expression", "value": "{{ $json.a }}"}) {
		t.Error("a well-formed marker was not recognized")
	}
	for _, value := range []any{
		"plain",
		map[string]any{"mode": "fixed", "value": "x"},
		map[string]any{"value": "x"},
		map[string]any{"mode": "expression"},
		map[string]any{"mode": "expression", "value": 3},
		nil,
	} {
		if expression.IsExpression(value) {
			t.Errorf("IsExpression(%#v) = true, want false", value)
		}
	}
}

func nodeContext() expression.Context {
	ctx := testContext()
	ctx.NodeItems = map[string]expression.NodeItem{
		"Get User": {
			JSON:   map[string]any{"id": float64(7), "email": "  Ada@Example.COM "},
			Items:  []map[string]any{{"id": float64(7), "email": "  Ada@Example.COM "}},
			Paired: map[string]any{"id": float64(7), "email": "  Ada@Example.COM "},
		},
		"Many": {
			JSON:  map[string]any{"n": float64(1)},
			Items: []map[string]any{{"n": float64(1)}, {"n": float64(2)}, {"n": float64(3)}},
			// Several items and no way to choose: `.item` must say so rather
			// than returning the first.
			LineageReason: `node "Many" produced 3 items; use .all(), .first() or .last() to choose one`,
		},
	}
	ctx.Workflow = expression.WorkflowContext{ID: "wf_1", Name: "Orders", Active: true}
	ctx.Now = time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	return ctx
}

// TestNodeAccessResolvesBothForms is the divergence that broke every imported
// expression. `$node["X"].json.y` is what n8n workflows are written as and it
// failed on the `.json` step, because a node's name mapped straight onto the
// item's fields with no wrapper.
func TestNodeAccessResolvesBothForms(t *testing.T) {
	t.Parallel()

	for name, template := range map[string]string{
		"n8n form":  `{{ $node["Get User"].json.id }}`,
		"bare form": `{{ $node["Get User"].id }}`,
		"call form": `{{ $('Get User').item.json.id }}`,
		"first()":   `{{ $('Get User').first().json.id }}`,
		"last()":    `{{ $('Get User').last().json.id }}`,
	} {
		value, err := expression.Evaluate(template, nodeContext())
		if err != nil {
			t.Errorf("%s: Evaluate(%q) error = %v", name, template, err)
			continue
		}
		if value != float64(7) {
			t.Errorf("%s: value = %#v, want 7", name, value)
		}
	}

	// `.all()` yields every item.
	value, err := expression.Evaluate(`{{ $('Many').all().length }}`, nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if value != float64(3) {
		t.Errorf("all().length = %#v, want 3", value)
	}
}

// TestItemRefusesToGuessWhenLineageIsUnknown is the property that stops a
// confident wrong answer. Returning the first item is correct only when every
// node processed exactly one item.
func TestItemRefusesToGuessWhenLineageIsUnknown(t *testing.T) {
	t.Parallel()

	_, err := expression.Evaluate(`{{ $('Many').item.json.n }}`, nodeContext())
	if err == nil {
		t.Fatal("`.item` returned a value for a node whose lineage is unknown")
	}
	if !strings.Contains(err.Error(), "3 items") {
		t.Errorf("error = %q, want it to explain why there is no single item", err)
	}
}

// TestUnknownNodeIsAnErrorNotUndefined separates a mistake from an absence. A
// node that never ran cannot be what the author meant.
func TestUnknownNodeIsAnErrorNotUndefined(t *testing.T) {
	t.Parallel()

	if _, err := expression.Evaluate(`{{ $('Nowhere').json.id }}`, nodeContext()); err == nil {
		t.Error("naming a node that never ran resolved to a value")
	}
}

func TestDatesAndWorkflowIdentityResolve(t *testing.T) {
	t.Parallel()

	for template, want := range map[string]any{
		`{{ $now.format("yyyy-MM-dd") }}`:                 "2026-09-05",
		`{{ $today.format("yyyy-MM-dd HH:mm") }}`:         "2026-09-05 00:00",
		`{{ $now.plusDays(7).format("yyyy-MM-dd") }}`:     "2026-09-12",
		`{{ $now.minusDays(5).format("yyyy-MM-dd") }}`:    "2026-08-31",
		`{{ $now.plus({days: 1}).format("yyyy-MM-dd") }}`: "2026-09-06",
		`{{ $workflow.name }}`:                            "Orders",
		`{{ $workflow.id }}`:                              "wf_1",
	} {
		value, err := expression.Evaluate(template, nodeContext())
		if err != nil {
			t.Errorf("Evaluate(%q) error = %v", template, err)
			continue
		}
		if value != want {
			t.Errorf("Evaluate(%q) = %#v, want %#v", template, value, want)
		}
	}

	// A date in text renders the way n8n renders one: ISO 8601, milliseconds
	// and an offset. Dropping the milliseconds made every round-tripped
	// timestamp compare unequal.
	mixed, err := expression.Evaluate("at {{ $now }}", nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if mixed != "at 2026-09-05T14:30:00.000+00:00" {
		t.Errorf("value = %#v, want an ISO timestamp with milliseconds and an offset", mixed)
	}

	// A lone date is a time.Time, which is what the DateTime and IF nodes
	// accept. The evaluator's own date struct used to escape here and marshal
	// to "{}" in a Set node.
	lone, err := expression.Evaluate("{{ $now }}", nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if _, isTime := lone.(time.Time); !isTime {
		t.Errorf("value = %#v (%T), want a time.Time a node can read", lone, lone)
	}
}

func TestFunctionsAreClosedAndResolveAtParseTime(t *testing.T) {
	t.Parallel()

	value, err := expression.Evaluate(`{{ $node["Get User"].json.email.trim().toLowerCase() }}`, nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if value != "ada@example.com" {
		t.Errorf("value = %#v, want the trimmed lowercase address", value)
	}

	// Anything not on the list is a parse error, so it fails at save rather
	// than on the first item that reaches it — and nothing can reach the host,
	// the filesystem or the network.
	for _, template := range []string{
		`{{ $json.name.exec("rm -rf /") }}`,
		`{{ $json.name.constructor() }}`,
		`{{ $json.name.eval("1") }}`,
		`{{ $json.name.require("fs") }}`,
	} {
		if _, err := expression.Evaluate(template, nodeContext()); err == nil {
			t.Errorf("Evaluate(%q) succeeded, want a parse error", template)
		}
	}

	// Arity is checked too.
	if _, err := expression.Evaluate(`{{ $json.name.replace("a") }}`, nodeContext()); err == nil {
		t.Error("replace() accepted one argument, want two")
	}
}

// TestFromAIIsOnlyAvailableWhereAnAgentFillsIt keeps the marker from being used
// somewhere meaningless, such as an HTTP URL.
func TestFromAIIsOnlyAvailableWhereAnAgentFillsIt(t *testing.T) {
	t.Parallel()

	if _, err := expression.Evaluate(`{{ $fromAI("city") }}`, nodeContext()); err == nil {
		t.Error("$fromAI resolved outside an AI tool parameter")
	}

	ctx := nodeContext()
	ctx.AllowFromAI = true
	value, err := expression.Evaluate(`{{ $fromAI("city", "The city to look up") }}`, ctx)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	request, ok := value.(expression.FromAIRequest)
	if !ok {
		t.Fatalf("value = %#v, want a FromAIRequest the agent consumes", value)
	}
	if request.Name != "city" || request.Description != "The city to look up" {
		t.Errorf("request = %#v, want the name and description carried", request)
	}
}

// TestFromAIArgumentsAreDataTheExpressionComputesWith pins the tool-call form:
// with the agent's arguments in the context, `$fromAI` evaluates to the
// argument itself. The value is data — an expression concatenates, indexes or
// calls a method on it — and the evaluator never reads it as source, so an
// argument that spells an expression is only ever text.
func TestFromAIArgumentsAreDataTheExpressionComputesWith(t *testing.T) {
	t.Parallel()

	ctx := nodeContext()
	ctx.Execution = expression.ExecutionContext{ID: "exec-1"}
	ctx.FromAIArguments = map[string]any{
		"name":   "{{ $execution.id }}",
		"code":   "$execution.id + '|' + 6*7",
		"tier":   "'gold'].pct + $execution.id + [0",
		"gold":   "gold",
		"a":      "x{",
		"b":      "{ $execution.id }",
		"c":      "}",
		"object": map[string]any{"b": "nested"},
		"score":  float64(3),
	}
	for _, row := range []struct{ template, want string }{
		{`Customer {{ $fromAI('name') }}`, "Customer {{ $execution.id }}"},
		{`{{ String($fromAI('code')).toUpperCase() }}`, "$EXECUTION.ID + '|' + 6*7"},
		{`{{ JSON.parse('{"a":{"b":"Customer "}}').a.b + $fromAI('code') }}`, "Customer $execution.id + '|' + 6*7"},
		{`{{ $fromAI('a') }}{{ $fromAI('b') }}{{ $fromAI('c') }}`, "x{{ $execution.id }}"},
		{`{{ ({ gold: { pct: 20 }, silver: { pct: 10 }})[$fromAI('gold')].pct + '%' }}`, "20%"},
		{`{{ $fromAI('object').b }}`, "nested"},
		{`{{ $fromAI('score', 'the score', 'number') * 100 + '' }}`, "300"},
		{`{{ $fromAI('plan', 'the plan', 'string', 'free') }}`, "free"},
		// The other spellings n8n accepts, which an imported workflow carries.
		{`Customer {{ $fromai('name') }}`, "Customer {{ $execution.id }}"},
		{`Customer {{ $fromAi('name') }}`, "Customer {{ $execution.id }}"},
	} {
		value, err := expression.Evaluate(row.template, ctx)
		if err != nil {
			t.Errorf("Evaluate(%s) error = %v", row.template, err)
			continue
		}
		if fmt.Sprint(value) != row.want {
			t.Errorf("Evaluate(%s) = %#v, want %q", row.template, value, row.want)
		}
	}
	// A lookup keyed by text that spells code finds no such key: the text is
	// never spliced into the object literal around it.
	if value, err := expression.Evaluate(`{{ ({ gold: { pct: 20 }})[$fromAI('tier')] }}`, ctx); err != nil ||
		strings.Contains(fmt.Sprint(value), "exec-1") {
		t.Errorf("lookup by hostile text = %#v, %v, want no evaluated result", value, err)
	}
	// An argument the agent left out with no default is an error naming it.
	if _, err := expression.Evaluate(`{{ $fromAI('missing') }}`, ctx); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("Evaluate(a missing argument) error = %v, want it named", err)
	}
	// An argument present as null is null, whatever the call's default says.
	ctx.FromAIArguments["none"] = nil
	if value, err := expression.Evaluate(`{{ $fromAI('none', 'n', 'json', 'null') }}`, ctx); err != nil || value != nil {
		t.Errorf("Evaluate(an argument present as null) = %#v, %v, want null", value, err)
	}
	// And a template that is exactly one call returns the argument unchanged,
	// with its type — the step node writes it as it came.
	if value, err := expression.Evaluate(`{{ $fromAI('name') }}`, ctx); err != nil || value != "{{ $execution.id }}" {
		t.Errorf("Evaluate(a sole call) = %#v, %v, want the argument's own text", value, err)
	}
}

// TestRootsAndFunctionsAreServedNotDuplicated pins the allowlist the editor
// reads, so a root added here needs no client change.
func TestRootsAndFunctionsAreServedNotDuplicated(t *testing.T) {
	t.Parallel()

	roots := expression.Roots()
	for _, want := range []string{"$json", "$node", "$now", "$today", "$workflow", "$fromAI", "$("} {
		if !slices.Contains(roots, want) {
			t.Errorf("Roots() is missing %q", want)
		}
	}
	functions := expression.FunctionNames()
	for _, want := range []string{"trim", "toLowerCase", "first", "last", "all", "format"} {
		if !slices.Contains(functions, want) {
			t.Errorf("FunctionNames() is missing %q", want)
		}
	}
	// Every advertised root must actually resolve, or the editor accepts what
	// the server refuses. The callable roots are covered separately.
	for _, root := range roots {
		if expression.IsCallableRoot(root) {
			continue
		}
		if _, err := expression.Evaluate("{{ "+root+" }}", nodeContext()); err != nil {
			t.Errorf("advertised root %q does not resolve: %v", root, err)
		}
	}
}

// The three routing roots exist for one caller and are refused everywhere else.
// A user-authored expression must not be able to read a credential, even the
// non-secret half of one, and must not be able to reach around the item
// contract into the node's own configuration.
func TestRoutingRootsAreRefusedOutsideARoutingTemplate(t *testing.T) {
	t.Parallel()

	ordinary := expression.Context{JSON: map[string]any{"a": float64(1)}}
	for _, template := range []string{"{{ $parameter.chatId }}", "{{ $credentials.name }}", "{{ $value }}"} {
		_, err := expression.Evaluate(template, ordinary)
		if err == nil {
			t.Fatalf("Evaluate(%q) succeeded, want it refused outside a routing template", template)
		}
		if !strings.Contains(err.Error(), "routing templates") {
			t.Fatalf("Evaluate(%q) error = %v, want it to name where the root is valid", template, err)
		}
	}
}

func TestRoutingRootsResolveWhenTheInterpreterSuppliesThem(t *testing.T) {
	t.Parallel()

	ctx := expression.Context{
		AllowRouting: true,
		Parameters:   map[string]any{"chatId": "42"},
		Credentials:  map[string]string{"name": "X-Api-Key"},
		Value:        " hello ",
	}
	for template, want := range map[string]any{
		"{{ $parameter.chatId }}":   "42",
		"{{ $credentials.name }}":   "X-Api-Key",
		"{{ $value.trim() }}":       "hello",
		"{{ $credentials.absent }}": nil,
	} {
		got, err := expression.Evaluate(template, ctx)
		if err != nil {
			t.Fatalf("Evaluate(%q) error = %v", template, err)
		}
		if got != want {
			t.Fatalf("Evaluate(%q) = %#v, want %#v", template, got, want)
		}
	}
}

// The editor is served the root allowlist so it does not keep a copy that
// drifts. The routing roots are deliberately absent from it: nothing a user can
// write should be offered a root that only a generated pack may use.
func TestRoutingRootsAreNotAdvertisedToTheEditor(t *testing.T) {
	t.Parallel()

	for _, root := range expression.Roots() {
		switch root {
		case "$parameter", "$credentials", "$value":
			t.Fatalf("Roots() advertises %q, which only a node pack may use", root)
		}
	}
}

func TestABracketKeyMayBeSingleQuoted(t *testing.T) {
	t.Parallel()

	// The keys that need bracket access at all are the ones with spaces and
	// dashes in them — a Schedule Trigger emits `Day of week`, an API sends
	// `content-type` — and an author writes those in single quotes, as
	// JavaScript does. strconv.Unquote reads '…' as a Go rune literal, so it
	// accepted 'a' and refused 'Day of week', which made every one of those
	// expressions fail with a message about quoting.
	for name, testCase := range map[string]struct {
		body string
		json map[string]any
		want any
	}{
		"a key with spaces": {
			body: "{{ $json['Day of week'] }}",
			json: map[string]any{"Day of week": "Saturday"},
			want: "Saturday",
		},
		"a key with a dash": {
			body: "{{ $json['content-type'] }}",
			json: map[string]any{"content-type": "application/json"},
			want: "application/json",
		},
		"double quotes still work": {
			body: `{{ $json["Day of week"] }}`,
			json: map[string]any{"Day of week": "Saturday"},
			want: "Saturday",
		},
		"a single character is a key, not a rune": {
			body: "{{ $json['a'] }}",
			json: map[string]any{"a": float64(1)},
			want: float64(1),
		},
		"an escaped quote inside": {
			body: `{{ $json['it\'s'] }}`,
			json: map[string]any{"it's": "yes"},
			want: "yes",
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolved, err := expression.Resolve(
				map[string]any{"x": map[string]any{"mode": "expression", "value": testCase.body}},
				expression.Context{JSON: testCase.json})
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", testCase.body, err)
			}
			if resolved["x"] != testCase.want {
				t.Errorf("Resolve(%q) = %#v, want %#v", testCase.body, resolved["x"], testCase.want)
			}
		})
	}

	// An index is still an index, and something that is neither is still an
	// error rather than a key spelled oddly.
	for _, body := range []string{"{{ $json[oops] }}", "{{ $json['a'b'] }}"} {
		_, err := expression.Resolve(
			map[string]any{"x": map[string]any{"mode": "expression", "value": body}},
			expression.Context{JSON: map[string]any{}})
		if err == nil {
			t.Errorf("Resolve(%q) accepted an index that is neither a number nor a quoted key", body)
		}
	}
}
