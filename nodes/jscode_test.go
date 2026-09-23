package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestAnImportedCodeNodeKeepsItsSourceAndRefusesPython(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.ForeignCodeNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.ForeignCodeNodeType)
	}
	// The source has to be a first-class parameter, not a note: a user porting
	// the node needs to read it in the editor.
	keys := map[string]bool{}
	for _, parameter := range definition.Parameters {
		keys[parameter.Key] = true
	}
	for _, required := range []string{"language", "mode", "jsCode", "pythonCode", "replacement"} {
		if !keys[required] {
			t.Errorf("the placeholder is missing parameter %q", required)
		}
	}

	// Python is blocking, like the generic placeholder: a Code node that
	// quietly passed its items through would let the workflow run, produce
	// plausible output, and be missing whatever the code was there to do.
	for name, testCase := range map[string]struct {
		parameters map[string]any
		want       string
	}{
		"python names the language and the replacement": {
			parameters: map[string]any{"language": "python", "pythonCode": "return [i for i in items if i.json.ok]"},
			want:       "this node's code is written in Python, which this server does not run. Replace it with the native nodes",
		},
		"python recognised by its body names the native node": {
			parameters: map[string]any{"language": "python", "pythonCode": "items.sort(key=lambda i: i.json.n)\nreturn items"},
			want:       "this node's code is written in Python, which this server does not run. A Sort node does this without code.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := definition.Validate(workflow.Node{
				ID: "n1", Name: "Code", Type: nodes.ForeignCodeNodeType, TypeVersion: workflow.V(1),
				Parameters: testCase.parameters,
			})
			if err == nil || !strings.HasPrefix(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to start %q", err, testCase.want)
			}
		})
	}

	// JavaScript kept as the placeholder, by an import from before the
	// runtime, validates like a Code (JavaScript) node: it runs.
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"language": "javaScript", "jsCode": "return items;"}}); err != nil {
		t.Fatalf("JavaScript in the placeholder: Validate() = %v, want it accepted", err)
	}
}

func TestTheReplacementSuggestionNamesANativeNodeRatherThanTranslating(t *testing.T) {
	t.Parallel()

	// A curated table, not a translator. It suggests; it never rewrites,
	// because a body that translates into Go which compiles and computes
	// something else is the one outcome worse than a refusal.
	for body, want := range map[string]string{
		"return items.filter(i => i.json.ok);":                 "Filter node",
		"return items.sort((a, b) => a.json.n - b.json.n);":    "Sort node",
		"return items.flatMap(i => i.json.rows);":              "Split Out node",
		"const t = items.reduce((a, i) => a + i.json.n, 0);":   "Aggregate or Summarize",
		"return [...new Set(items.map(i => i.json.id))];":      "Remove Duplicates node",
		"const r = await fetch('https://example.test');":       "HTTP Request node",
		"return items.map(i => ({json: {when: new Date()}}));": "Date & Time node",
		"return items.slice(0, 10);":                           "Limit node",
		"return items.map(i => ({json: {id: i.json.id}}));":    "Set node",
	} {
		if got := nodes.SuggestReplacement(body); !strings.Contains(got, want) {
			t.Errorf("SuggestReplacement(%q) = %q, want it to mention %q", body, got, want)
		}
	}

	// Nothing recognised still names the alternatives rather than saying only
	// that this is unsupported.
	fallback := nodes.SuggestReplacement("someHelper();")
	if !strings.Contains(fallback, "Go Code node") {
		t.Errorf("fallback = %q, want the Go Code node named", fallback)
	}
}
