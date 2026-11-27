package triggerfilter

import (
	"strings"
	"testing"
)

func expr(template string) map[string]any {
	return map[string]any{"mode": "expression", "value": template}
}

func channelFilter(channel string) map[string]any {
	return map[string]any{
		"combinator": "and",
		"options":    map[string]any{"caseSensitive": true, "typeValidation": "loose"},
		"conditions": []any{
			map[string]any{
				"leftValue":  expr("{{ $json.body.channel }}"),
				"rightValue": channel,
				"operator":   map[string]any{"type": "string", "operation": "equals"},
			},
		},
	}
}

func TestCheckWithoutAFilterMatchesEveryItem(t *testing.T) {
	for name, parameters := range map[string]map[string]any{
		"no parameters": nil,
		"absent key":    {"path": "orders"},
		"explicit null": {ParameterKey: nil},
		"no conditions": {ParameterKey: map[string]any{"conditions": []any{}}},
		"empty object":  {ParameterKey: map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			if result := Check(parameters, map[string]any{"body": map[string]any{}}); !result.Matched {
				t.Fatalf("Check() = %+v, want a match", result)
			}
		})
	}
}

func TestCheckResolvesExpressionsAgainstTheTriggerItem(t *testing.T) {
	parameters := map[string]any{ParameterKey: channelFilter("whatsapp")}

	matched := Check(parameters, map[string]any{"body": map[string]any{"channel": "whatsapp"}})
	if !matched.Matched || matched.Reason != "" {
		t.Fatalf("matching item: Check() = %+v, want a match with no reason", matched)
	}

	filtered := Check(parameters, map[string]any{"body": map[string]any{"channel": "email"}})
	if filtered.Matched {
		t.Fatal("non-matching item: Check() matched, want it filtered out")
	}
	if !strings.Contains(filtered.Reason, "did not match") {
		t.Fatalf("non-matching item: reason = %q, want it to say the event did not match", filtered.Reason)
	}
}

func TestCheckCombinesConditionsWithOr(t *testing.T) {
	filter := map[string]any{
		"combinator": "or",
		"conditions": []any{
			map[string]any{
				"leftValue": expr("{{ $json.payload.text }}"), "rightValue": "refund",
				"operator": map[string]any{"type": "string", "operation": "contains"},
			},
			map[string]any{
				"leftValue": expr("{{ $json.payload.isGroup }}"),
				"operator":  map[string]any{"type": "boolean", "operation": "false", "singleValue": true},
			},
		},
	}
	parameters := map[string]any{ParameterKey: filter}

	cases := []struct {
		name string
		item map[string]any
		want bool
	}{
		{"keyword in a group", map[string]any{"payload": map[string]any{"text": "I want a refund", "isGroup": true}}, true},
		{"direct message without keyword", map[string]any{"payload": map[string]any{"text": "hello", "isGroup": false}}, true},
		{"group without keyword", map[string]any{"payload": map[string]any{"text": "hello", "isGroup": true}}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Check(parameters, testCase.item).Matched; got != testCase.want {
				t.Fatalf("Check().Matched = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestCheckFiltersOutWhenTheFilterCannotBeEvaluated(t *testing.T) {
	strict := map[string]any{
		"options": map[string]any{"typeValidation": "strict"},
		"conditions": []any{
			map[string]any{
				"leftValue": expr("{{ $json.body.count }}"), "rightValue": 3,
				"operator": map[string]any{"type": "number", "operation": "gt"},
			},
		},
	}
	result := Check(map[string]any{ParameterKey: strict}, map[string]any{"body": map[string]any{"count": "many"}})
	if result.Matched {
		t.Fatal("a strict type failure matched, want the event filtered out")
	}
	if !strings.Contains(result.Reason, "could not be evaluated") {
		t.Fatalf("reason = %q, want it to say the filter could not be evaluated", result.Reason)
	}
}

func TestCheckFiltersOutAMalformedFilter(t *testing.T) {
	for name, declared := range map[string]any{
		"a string":           "channel == whatsapp",
		"a condition string": map[string]any{"conditions": []any{"nope"}},
		"no operator":        map[string]any{"conditions": []any{map[string]any{"leftValue": "a"}}},
	} {
		t.Run(name, func(t *testing.T) {
			result := Check(map[string]any{ParameterKey: declared}, map[string]any{})
			if result.Matched || result.Reason == "" {
				t.Fatalf("Check() = %+v, want it filtered out with a reason", result)
			}
		})
	}
}

func TestPropertyIsAnOptionalConditionsParameter(t *testing.T) {
	definition := Property()
	if definition.Key != ParameterKey {
		t.Fatalf("Key = %q, want %q", definition.Key, ParameterKey)
	}
	if definition.Required {
		t.Fatal("the trigger filter must be optional: a trigger without one runs for every event")
	}
	if definition.Kind != "conditions" {
		t.Fatalf("Kind = %q, want conditions", definition.Kind)
	}
}
