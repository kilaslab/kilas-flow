package property_test

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/property"
)

func TestALocatorSurvivesExpressionResolutionIntact(t *testing.T) {
	t.Parallel()

	// The whole reason a mode may never be named "expression": the locator and
	// the expression marker share the `mode` and `value` keys, so a locator
	// whose mode collided would be swallowed by Resolve and the node would
	// receive a bare string where it expects an object — with nothing anywhere
	// reporting it.
	for _, mode := range []string{"list", "id", "name", "url"} {
		stored := property.WriteLocator(property.Locator{
			Mode: mode, Value: "customers", CachedResultName: "Customers",
		})
		resolved, err := expression.Resolve(map[string]any{"table": stored}, expression.Context{
			JSON: map[string]any{"chosen": "orders"},
		})
		if err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !reflect.DeepEqual(resolved["table"], stored) {
			t.Errorf("mode %q: resolved = %#v, want the locator unchanged", mode, resolved["table"])
		}
	}
}

func TestAnExpressionInsideALocatorResolvesInPlace(t *testing.T) {
	t.Parallel()

	// An expression goes in the value slot, where the existing recursion
	// evaluates it and the sentinel survives.
	stored := property.WriteLocator(property.Locator{
		Mode:  "name",
		Value: map[string]any{"mode": "expression", "value": "{{ $json.chosen }}"},
	})
	resolved, err := expression.Resolve(map[string]any{"table": stored}, expression.Context{
		JSON: map[string]any{"chosen": "orders"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	locator, ok := property.ReadLocator(resolved["table"])
	if !ok {
		t.Fatalf("resolved = %#v, want a locator", resolved["table"])
	}
	if locator.Mode != "name" || locator.Value != "orders" {
		t.Errorf("locator = %#v, want the value evaluated and the mode kept", locator)
	}
}

func TestReadingALocatorAcceptsEveryShapeADocumentCarries(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		stored any
		want   property.Locator
		ok     bool
	}{
		"a stored locator": {
			stored: map[string]any{"__rl": true, "mode": "id", "value": "wf_1", "cachedResultName": "Enrichment"},
			want:   property.Locator{Mode: "id", Value: "wf_1", CachedResultName: "Enrichment"},
			ok:     true,
		},
		// What a document written before this kind existed carries. Refusing it
		// would break every such node on load rather than where it matters.
		"a bare string":   {stored: "wf_1", want: property.Locator{Value: "wf_1"}, ok: true},
		"an empty string": {stored: "", want: property.Locator{Value: ""}},
		// An object without the sentinel is not a locator, and reading one as
		// though it were is how a Set node's assignment becomes a table name.
		"an object without the sentinel": {stored: map[string]any{"mode": "id", "value": "wf_1"}},
		"nothing at all":                 {stored: nil},
		"a number":                       {stored: float64(7)},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := property.ReadLocator(testCase.stored)
			if ok != testCase.ok {
				t.Fatalf("ReadLocator(%#v) ok = %v, want %v", testCase.stored, ok, testCase.ok)
			}
			if ok && !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("ReadLocator(%#v) = %#v, want %#v", testCase.stored, got, testCase.want)
			}
		})
	}
}

func TestLocatorIsSetReadsEveryShapeOfNotChosenYet(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		stored any
		want   bool
	}{
		"absent":                  {stored: nil},
		"an empty string":         {stored: ""},
		"whitespace":              {stored: "   "},
		"an empty locator value":  {stored: map[string]any{"__rl": true, "mode": "id", "value": ""}},
		"not a locator":           {stored: map[string]any{"mode": "id", "value": "wf_1"}},
		"a bare string":           {stored: "wf_1", want: true},
		"a chosen locator":        {stored: map[string]any{"__rl": true, "mode": "id", "value": "wf_1"}, want: true},
		"a numeric locator value": {stored: map[string]any{"__rl": true, "mode": "id", "value": float64(7)}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := property.LocatorIsSet(testCase.stored); got != testCase.want {
				t.Errorf("LocatorIsSet(%#v) = %v, want %v", testCase.stored, got, testCase.want)
			}
		})
	}
}
