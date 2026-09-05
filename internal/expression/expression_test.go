package expression_test

import (
	"strings"
	"testing"

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

func TestEvaluateReportsMissingDataWithoutLeakingContext(t *testing.T) {
	t.Parallel()

	_, err := expression.Evaluate("{{ $json.missing.deeper }}", testContext())
	if err == nil {
		t.Fatal("expected an error for a missing path")
	}
	if !strings.Contains(err.Error(), "$json.missing") {
		t.Errorf("error = %q, want it to name the failing path", err)
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
		"url": map[string]any{"mode": "expression", "value": "{{ $json.missing }}"},
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
