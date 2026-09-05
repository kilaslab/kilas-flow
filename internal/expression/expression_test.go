package expression_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/expression"
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
		"{{ $json.name + $json.count }}",
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
		"unsupported root":      "{{ $secrets.token }}",
		"index into non-list":   "{{ $json.name[0] }}",
		"field on a non-object": "{{ $json.name.deeper }}",
		"unknown function":      "{{ $json.name.hack() }}",
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
	value, err := expression.Evaluate(`{{ $('Many').all().length() }}`, nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if value != float64(3) {
		t.Errorf("all().length() = %#v, want 3", value)
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
		`{{ $now.format("2006-01-02") }}`:              "2026-09-05",
		`{{ $today.format("2006-01-02 15:04") }}`:      "2026-09-05 00:00",
		`{{ $now.plusDays(7).format("2006-01-02") }}`:  "2026-09-12",
		`{{ $now.minusDays(5).format("2006-01-02") }}`: "2026-08-31",
		`{{ $workflow.name }}`:                         "Orders",
		`{{ $workflow.id }}`:                           "wf_1",
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

	// A bare date stringifies as RFC 3339, which formats readably and compares
	// correctly against another timestamp in the same zone.
	mixed, err := expression.Evaluate("at {{ $now }}", nodeContext())
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if mixed != "at 2026-09-05T14:30:00Z" {
		t.Errorf("value = %#v, want an RFC 3339 timestamp", mixed)
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
	// the server refuses.
	for _, root := range roots {
		if root == "$(" || root == "$fromAI" {
			continue // These take an argument and are covered above.
		}
		if _, err := expression.Evaluate("{{ "+root+" }}", nodeContext()); err != nil {
			t.Errorf("advertised root %q does not resolve: %v", root, err)
		}
	}
}
