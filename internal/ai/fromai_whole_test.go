package ai_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

// A Workflow Tool column is "the model's" when its value is nothing but one
// $fromAI call. The editor, the importer and the exporter all ask this, so the
// rule lives in one place.
func TestWholeFromAICallRecognisesAColumnLeftToTheModel(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		template string
		whole    bool
		key      string
		kind     string
	}{
		"the canonical form":        {"{{ $fromAI('tempC', '', 'number') }}", true, "tempC", "number"},
		"no spaces, double quotes":  {`{{$fromAI("a")}}`, true, "a", "string"},
		"any case":                  {"{{ $FROMAI('a', '', 'boolean') }}", true, "a", "boolean"},
		"n8n's backticks":           {"{{ $fromAI('a', ``, 'json') }}", true, "a", "json"},
		"surrounding whitespace":    {"  {{ $fromAI('a') }}\n", true, "a", "string"},
		"a call plus arithmetic":    {"{{ $fromAI('a', '', 'number') + 1 }}", false, "", ""},
		"literal text before":       {"x {{ $fromAI('a') }}", false, "", ""},
		"literal text after":        {"{{ $fromAI('a') }} x", false, "", ""},
		"two calls":                 {"{{ $fromAI('a') }}{{ $fromAI('b') }}", false, "", ""},
		"two calls in one segment":  {"{{ $fromAI('a') + $fromAI('b') }}", false, "", ""},
		"another expression":        {"{{ $json.note }}", false, "", ""},
		"plain text":                {"F", false, "", ""},
		"an unclosed template":      {"{{ $fromAI('a')", false, "", ""},
		"a call that names no key":  {"{{ $fromAI() }}", false, "", ""},
		"a call of an unknown type": {"{{ $fromAI('a', '', 'banana') }}", false, "", ""},
		"nothing":                   {"", false, "", ""},
	} {
		call, whole := ai.WholeFromAICall(test.template)
		if whole != test.whole {
			t.Errorf("%s: WholeFromAICall(%q) = %v, want %v", name, test.template, whole, test.whole)
			continue
		}
		if whole && (call.Key != test.key || call.Type != test.kind) {
			t.Errorf("%s: call = %+v, want key %q type %q", name, call, test.key, test.kind)
		}
	}
}
