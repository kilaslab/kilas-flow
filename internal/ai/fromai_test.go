package ai_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

func TestExtractFromAIDerivesThreeProperties(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"url":    map[string]any{"mode": "expression", "value": "https://api.test/weather/{{ $fromAI('city', 'the city to look up') }}"},
		"method": "GET",
		"timeout": map[string]any{
			"mode":  "expression",
			"value": "{{ $fromAI('retries', 'how many retries', 'number', 3) }}",
		},
		"sendBody": map[string]any{"mode": "expression", "value": "{{ $fromAI('verbose', 'include details', 'boolean') }}"},
	}

	calls, err := ai.ExtractFromAI(parameters)
	if err != nil {
		t.Fatalf("ExtractFromAI() error = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("ExtractFromAI() found %d calls, want 3", len(calls))
	}
	schema := ai.FromAISchema(calls)
	properties, _ := schema["properties"].(map[string]any)
	for key, description := range map[string]string{
		"city": "the city to look up", "retries": "how many retries", "verbose": "include details",
	} {
		property, _ := properties[key].(map[string]any)
		if property["description"] != description {
			t.Errorf("property %q description = %#v, want %q", key, property["description"], description)
		}
	}
	if properties["retries"].(map[string]any)["type"] != "number" {
		t.Errorf("retries type = %#v, want number", properties["retries"])
	}
	required, _ := schema["required"].([]string)
	if len(required) != 2 || !containsString(required, "city") || !containsString(required, "verbose") {
		t.Errorf("schema requires %#v, want city and verbose with retries optional", required)
	}
}

func TestExtractFromAICaseInsensitiveAndTyped(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"value": "{{ $fromai('key', 'desc', 'number') }}",
	}
	calls, err := ai.ExtractFromAI(parameters)
	if err != nil {
		t.Fatalf("ExtractFromAI() error = %v", err)
	}
	if len(calls) != 1 || calls[0].Key != "key" || calls[0].Type != "number" {
		t.Fatalf("ExtractFromAI() = %#v, want one number call", calls)
	}
}

func TestExtractFromAIRejectsUnknownTypeNamingTheCall(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"value": "{{ $fromAI('city', 'the city', 'paragraph') }}",
	}
	_, err := ai.ExtractFromAI(parameters)
	if err == nil || !strings.Contains(err.Error(), "city") || !strings.Contains(err.Error(), "paragraph") {
		t.Fatalf("ExtractFromAI() error = %v, want the call and type named", err)
	}
}

// TestSubstituteFromAIFillsPlainStringsAndKeepsTheirType pins the fill: a
// plain string that is one whole call takes the argument with its type, or
// the call's typed default, and a call inside other text takes it as text.
func TestSubstituteFromAIFillsPlainStringsAndKeepsTheirType(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"limit": "$fromAI('limit', 'max rows', 'number', 10)",
		"url":   "https://api.test/weather/$fromAI('city', 'the city')?verbose=$fromAI('verbose', 'details', 'boolean', false)",
	}

	substituted, err := ai.SubstituteFromAI(parameters, map[string]any{"city": "Utrecht"})
	if err != nil {
		t.Fatalf("SubstituteFromAI() error = %v", err)
	}
	if substituted["limit"] != float64(10) {
		t.Errorf("limit = %#v, want the numeric default 10", substituted["limit"])
	}
	if substituted["url"] != "https://api.test/weather/Utrecht?verbose=false" {
		t.Errorf("url = %#v, want arguments interpolated", substituted["url"])
	}
}

// TestSubstituteFromAILeavesExpressionsAsWritten keeps the model's value out
// of an expression's source. Spliced in, a value spelling `{{ … }}` or code
// ran when the expression was evaluated; an expression reads the arguments
// from its context instead.
func TestSubstituteFromAILeavesExpressionsAsWritten(t *testing.T) {
	t.Parallel()

	marker := map[string]any{"mode": "expression", "value": "Note: {{ $fromAI('note', 'a note') }}"}
	parameters := map[string]any{"note": marker, "nested": map[string]any{"list": []any{marker}}}
	substituted, err := ai.SubstituteFromAI(parameters, map[string]any{"note": "{{ $execution.id }}"})
	if err != nil {
		t.Fatalf("SubstituteFromAI() error = %v", err)
	}
	if !reflect.DeepEqual(substituted, parameters) {
		t.Errorf("SubstituteFromAI() = %#v, want every expression exactly as written", substituted)
	}
}

// TestExtractFromAIReadsAnExpressionOnlyInsideItsBraces covers a $fromAI call
// written as an expression's literal text. Nothing evaluates the text around
// an expression's {{ }} segments, so the call stays the literal characters
// "$fromAI('name')" when the tool runs: offering name as a required argument
// asked the model for a value nothing would use. A call inside a segment, and
// a call in a plain string — which is filled wherever it sits — are offered as
// before.
func TestExtractFromAIReadsAnExpressionOnlyInsideItsBraces(t *testing.T) {
	t.Parallel()

	literal := map[string]any{"mode": "expression", "value": "Customer $fromAI('name')"}
	calls, err := ai.ExtractFromAI(map[string]any{"greeting": literal})
	if err != nil {
		t.Fatalf("ExtractFromAI() error = %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("ExtractFromAI(literal text) = %#v, want no argument the tool would never fill", calls)
	}
	if schema := ai.FromAISchema(calls); schema["required"] != nil {
		t.Errorf("FromAISchema() = %#v, want nothing required", schema)
	}

	calls, err = ai.ExtractFromAI(map[string]any{
		"greeting": literal,
		"segment":  map[string]any{"mode": "expression", "value": "Customer {{ $fromAI('name') }} of $fromAI('ignored') in {{ { city: $fromAI('city') }.city }}"},
		"plain":    "Order $fromAI('order', 'the order number', 'number')",
	})
	if err != nil {
		t.Fatalf("ExtractFromAI() error = %v", err)
	}
	keys := make([]string, 0, len(calls))
	for _, call := range calls {
		keys = append(keys, call.Key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"city", "name", "order"}) {
		t.Errorf("ExtractFromAI() keys = %v, want the calls inside {{ }} and in the plain string", keys)
	}

	// The plain string is filled where the call sits, so its argument is used.
	filled, err := ai.SubstituteFromAI(map[string]any{"plain": "Order $fromAI('order', 'the order number', 'number')"}, map[string]any{"order": float64(7)})
	if err != nil || filled["plain"] != "Order 7" {
		t.Errorf("SubstituteFromAI(plain) = (%#v, %v), want the argument filled in", filled, err)
	}
}

func TestSubstituteFromAIMissingRequiredArgumentNamesKey(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{"city": "$fromAI('city', 'the city')"}
	_, err := ai.SubstituteFromAI(parameters, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "city") {
		t.Fatalf("SubstituteFromAI() error = %v, want the key named", err)
	}
}

func TestSubstituteFromAIHandlesQuotesAndParens(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		// Nested parentheses and a comma inside quotes must not split the call.
		"value": "{{ $fromAI('filter', 'e.g. name (starts, with)', 'string', 'a,b') }}",
	}
	calls, err := ai.ExtractFromAI(parameters)
	if err != nil {
		t.Fatalf("ExtractFromAI() error = %v", err)
	}
	if len(calls) != 1 || calls[0].Key != "filter" {
		t.Fatalf("ExtractFromAI() = %#v, want the single quoted call", calls)
	}
	substituted, err := ai.SubstituteFromAI(parameters, map[string]any{})
	if err != nil {
		t.Fatalf("SubstituteFromAI() error = %v", err)
	}
	if substituted["value"] != "a,b" {
		t.Errorf("value = %#v, want the quoted default", substituted["value"])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCheckFromAIConsistentRefusesAKeyDeclaredTwoWays(t *testing.T) {
	t.Parallel()

	marker := func(template string) map[string]any {
		return map[string]any{"mode": "expression", "value": template}
	}
	// ExtractFromAI keeps one declaration per key, and which one depends on
	// map order: two that differ make the schema, and the type the argument is
	// checked against, change from run to run.
	for name, parameters := range map[string]map[string]any{
		"type": {
			"plan": marker("{{ $fromAI('x', 'x', 'number') + '' }}"),
			"name": marker("{{ $fromAI('x', 'x') }}"),
		},
		"description": {
			"a": marker("{{ $fromAI('x', 'the name') }}"),
			"b": marker("{{ $fromAI('x', 'the city') }}"),
		},
		"default": {
			"a": marker("{{ $fromAI('x', 'x', 'string', 'free') }}"),
			"b": []any{marker("{{ $fromAI('x', 'x') }}")},
		},
	} {
		if err := ai.CheckFromAIConsistent(parameters); err == nil || !strings.Contains(err.Error(), `"x"`) {
			t.Errorf("%s: CheckFromAIConsistent() = %v, want the key named", name, err)
		}
	}
	same := map[string]any{
		"a": marker("{{ $fromAI('x', 'the name') }}"),
		"b": map[string]any{"c": "$fromAI('x', 'the name')"},
		"d": marker("{{ $fromAI('y', 'y', 'number', 3) }}"),
	}
	if err := ai.CheckFromAIConsistent(same); err != nil {
		t.Errorf("CheckFromAIConsistent(one declaration repeated) = %v, want success", err)
	}
}
