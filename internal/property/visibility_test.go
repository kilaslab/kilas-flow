package property_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/property"
)

// visibilityCase is one row of the shared fixture.
type visibilityCase struct {
	Name        string              `json:"name"`
	Comment     string              `json:"comment,omitempty"`
	Visibility  property.Visibility `json:"visibility"`
	Parameters  map[string]any      `json:"parameters"`
	TypeVersion string              `json:"typeVersion,omitempty"`
	Visible     bool                `json:"visible"`
}

// TestVisibilityMatchesTheSharedFixture is half of the anti-drift mechanism.
//
// The editor's TypeScript reads the same file. Two implementations of one rule
// drift — that is not a risk, it is a certainty — and a shared fixture is the
// only thing that keeps the panel and the compiler agreeing about whether a
// workflow can be saved.
func TestVisibilityMatchesTheSharedFixture(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/visibility.json")
	if err != nil {
		t.Fatalf("reading the shared fixture: %v", err)
	}
	var fixture struct {
		Cases []visibilityCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parsing the shared fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("the shared fixture is empty")
	}

	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			got := property.Visible(testCase.Visibility, testCase.Parameters, testCase.TypeVersion)
			if got != testCase.Visible {
				t.Errorf("Visible() = %t, want %t", got, testCase.Visible)
			}
		})
	}
}

// TestVisibilityRefusesAnUnsupportedPseudoKey keeps @feature and @tool from
// being accepted and ignored, which would present the wrong fields silently.
func TestVisibilityRefusesAnUnsupportedPseudoKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"@feature", "@tool", "@anything"} {
		err := property.ValidateVisibility(property.Visibility{
			Show: []property.Condition{{Key: key, Values: []any{true}}},
		})
		if err == nil {
			t.Errorf("visibility key %q was accepted", key)
		}
	}
	if err := property.ValidateVisibility(property.Visibility{
		Show: []property.Condition{{Key: property.VersionKey, Values: []any{"2"}}},
	}); err != nil {
		t.Errorf("@version was refused: %v", err)
	}
	if err := property.ValidateVisibility(property.Visibility{
		Show: []property.Condition{{Key: "mode", Operator: "sortOf", Values: []any{"x"}}},
	}); err == nil {
		t.Error("an unknown operator was accepted")
	}
}

// TestVisiblePropertyMergesSameKeyShorthand pins the BUG-wp2y0y fix: several
// shorthand entries on one key mean "one of these values", not "all at once".
func TestVisiblePropertyMergesSameKeyShorthand(t *testing.T) {
	t.Parallel()

	shorthand := func(entries ...property.VisibilityCondition) property.PropertyDefinition {
		return property.PropertyDefinition{Key: "columns", VisibleWhen: entries}
	}
	multi := shorthand(
		property.VisibilityCondition{Key: "operation", Equals: "insert"},
		property.VisibilityCondition{Key: "operation", Equals: "update"},
		property.VisibilityCondition{Key: "operation", Equals: "upsert"},
	)
	for _, operation := range []string{"insert", "update", "upsert"} {
		if !property.VisibleProperty(multi, map[string]any{"operation": operation}, "") {
			t.Errorf("operation %q should show the property", operation)
		}
	}
	if property.VisibleProperty(multi, map[string]any{"operation": "delete"}, "") {
		t.Error("operation delete should hide the property")
	}
	// Different keys still AND: both must match.
	anded := shorthand(
		property.VisibilityCondition{Key: "operation", Equals: "update"},
		property.VisibilityCondition{Key: "resource", Equals: "message"},
	)
	if property.VisibleProperty(anded, map[string]any{"operation": "update", "resource": "other"}, "") {
		t.Error("a mismatched second key should hide the property")
	}
	if !property.VisibleProperty(anded, map[string]any{"operation": "update", "resource": "message"}, "") {
		t.Error("both keys matching should show the property")
	}
}
