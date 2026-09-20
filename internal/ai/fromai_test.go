package ai_test

import (
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

func TestSubstituteFromAIWholeMarkerKeepsType(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"limit": map[string]any{"mode": "expression", "value": "{{ $fromAI('limit', 'max rows', 'number', 10) }}"},
		"url":   map[string]any{"mode": "expression", "value": "https://api.test/weather/{{ $fromAI('city', 'the city') }}?verbose={{ $fromAI('verbose', 'details', 'boolean', false) }}"},
	}

	substituted, err := ai.SubstituteFromAI(parameters, map[string]any{"city": "Utrecht"})
	if err != nil {
		t.Fatalf("SubstituteFromAI() error = %v", err)
	}
	if substituted["limit"] != float64(10) {
		t.Errorf("limit = %#v, want the numeric default 10", substituted["limit"])
	}
	url, _ := substituted["url"].(map[string]any)["value"].(string)
	if url != "https://api.test/weather/Utrecht?verbose=false" {
		t.Errorf("url = %q, want arguments interpolated", url)
	}
}

func TestSubstituteFromAIMissingRequiredArgumentNamesKey(t *testing.T) {
	t.Parallel()

	parameters := map[string]any{
		"url": map[string]any{"mode": "expression", "value": "{{ $fromAI('city', 'the city') }}"},
	}
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
