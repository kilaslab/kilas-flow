package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-9wpy6m. The whole `columns` is the author's: a string, a list or a
// marker in its place would let the agent's item, or the model, choose the
// columns written.
func TestTheGoogleSheetsToolRefusesAColumnsThatIsNotAPlainMap(t *testing.T) {
	tool := sheetsToolNode(t)
	for name, columns := range map[string]any{
		"template text": "{{ $json.cols }}",
		"plain text":    "name",
		"a list":        []any{"name"},
		"a marker":      formExpression("{{ $json.cols }}"),
	} {
		parameters := sheetsToolParams("update", map[string]any{"columns": columns})
		if err := tool.Validate(workflow.Node{Parameters: parameters}); err == nil || !strings.Contains(err.Error(), "columns") {
			t.Errorf("Validate(columns as %s) = %v, want a refusal naming columns", name, err)
		}

		setup := newSheetsSetup(t)
		setup.peopleTab()
		_, err := setup.runAsTool(sheetsParams("update", map[string]any{"columns": columns}), nil, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "columns") {
			t.Errorf("Execute(columns as %s) error = %v, want it refused", name, err)
		}
		if log := setup.fake.callLog(); log != "" {
			t.Errorf("Google was called before the refusal (columns as %s):\n%s", name, log)
		}
	}
}

// The executor applies the generic author-only rule again before it calls
// Google, so a descriptor that reaches it without the agent's check cannot let
// the model, or an expression, choose the spreadsheet, the tab or how much a
// delete removes.
func TestTheExecutorAppliesTheAuthorOnlyKeysWhenRunAsATool(t *testing.T) {
	for name, test := range map[string]struct {
		operation string
		key       string
		value     any
	}{
		"documentId":     {"read", "documentId", sheetsExpr("{{ $fromAI('d', 'the spreadsheet') }}")},
		"sheetName":      {"read", "sheetName", sheetsExpr("{{ $fromAI('s', 'the tab') }}")},
		"options":        {"read", "options", map[string]any{"dataLocationOnSheet": sheetsExpr("{{ $fromAI('o', 'where') }}")}},
		"combineFilters": {"read", "combineFilters", sheetsExpr("{{ $fromAI('c', 'how') }}")},
		"numberToDelete": {"delete", "numberToDelete", sheetsExpr("{{ $fromAI('n', 'how many', 'number') }}")},
	} {
		t.Run(name, func(t *testing.T) {
			setup := newSheetsSetup(t)
			setup.peopleTab()
			parameters := sheetsParams(test.operation, map[string]any{test.key: test.value})
			_, err := setup.runAsTool(parameters, nil, map[string]any{"d": sheetsDoc, "s": "Sheet1", "n": float64(1)})
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("Execute() error = %v, want a refusal naming %s", err, test.key)
			}
			if log := setup.fake.callLog(); log != "" {
				t.Fatalf("Google was called before the refusal:\n%s", log)
			}
		})
	}
}
