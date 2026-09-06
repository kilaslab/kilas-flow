package execution_test

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/execution"
)

// Datastore cells look exactly like credentials to key-based redaction,
// which is why the trace carries a summary instead of cells. Each case here
// names the rule that fires, so a future edit to the key lists can see what
// datastore shapes it would drag back into "[redacted]".
func TestRedactRewritesDatastoreShapedCells(t *testing.T) {
	cases := []struct {
		name    string
		rule    string
		payload string
		cell    string
	}{
		{
			name:    "column named api_key",
			rule:    "sensitiveKeys",
			payload: `{"api_key":"live-secret"}`,
			cell:    "live-secret",
		},
		{
			name:    "column named password",
			rule:    "sensitiveKeys",
			payload: `{"password":"hunter2"}`,
			cell:    "hunter2",
		},
		{
			// The cross-run key-value row: two innocent text columns whose
			// shape matches the HTTP header pair.
			name:    "name/value pair naming cookie",
			rule:    "headerPairIsSensitive",
			payload: `{"name":"cookie","value":"chocolate chip"}`,
			cell:    "chocolate chip",
		},
		{
			name:    "nested cell under a sensitive key",
			rule:    "sensitiveKeys at depth",
			payload: `{"rows":[{"secret":"buried"}]}`,
			cell:    "buried",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(execution.Redact([]byte(tc.payload)))
			if !strings.Contains(got, execution.RedactedValue) {
				t.Errorf("Redact(%s) = %s, want the %s rule to fire", tc.payload, got, tc.rule)
			}
			if strings.Contains(got, tc.cell) {
				t.Errorf("Redact(%s) = %s, want the cell destroyed", tc.payload, got)
			}
		})
	}
}

// The datastore trace summary is redaction-stable byte-for-byte: no key
// normalises onto the sensitive list and no object carries a name/value
// pair, so a projected trace round-trips through Redact unmarked.
func TestRedactLeavesDatastoreSummaryAlone(t *testing.T) {
	// Field order is alphabetical, matching the envelope the projector
	// emits: Redact round-trips through a map with sorted keys.
	summary := `{"datastore":{"ids":[1,2],"rows":2}}`
	if got := string(execution.Redact([]byte(summary))); got != summary {
		t.Errorf("Redact(summary) = %s, want byte-identical %s", got, summary)
	}
	truncated := `{"datastore":{"ids":[1],"rows":150,"truncated":true}}`
	if got := string(execution.Redact([]byte(truncated))); got != truncated {
		t.Errorf("Redact(truncated) = %s, want byte-identical %s", got, truncated)
	}
}
