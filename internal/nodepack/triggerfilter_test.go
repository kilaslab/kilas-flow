package nodepack_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/triggerfilter"
)

func filterManifest(parameters string) []byte {
	return []byte(`{
		"type": "pack.filterableTrigger", "version": 1,
		"displayName": "Filterable Trigger", "category": "Triggers",
		"requestDefaults": {},
		"parameters": ` + parameters + `,
		"trigger": {
			"events": ["message"], "catchAll": "other", "eventPath": "event", "shape": "bodyAsItem",
			"webhook": {"name": "default", "staticPath": "filterable", "method": "POST"}
		}
	}`)
}

// Every pack trigger carries the trigger filter, so a host's pack is filterable
// at the boundary like the built-in Webhook and Host Event triggers.
func TestEveryPackTriggerCarriesTheTriggerFilter(t *testing.T) {
	pack, err := nodepack.Decode(filterManifest(`[]`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	definition, _, err := nodepack.Load(pack)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, parameter := range definition.Parameters {
		if parameter.Key == triggerfilter.ParameterKey {
			if parameter.Kind != triggerfilter.Property().Kind || parameter.Required {
				t.Fatalf("the trigger filter parameter = %+v, want an optional conditions parameter", parameter)
			}
			return
		}
	}
	t.Fatalf("the pack trigger's parameters %+v carry no %q", definition.Parameters, triggerfilter.ParameterKey)
}

// A pack cannot declare the reserved key for a setting of its own.
func TestAPackTriggerCannotDeclareTheReservedFilterKey(t *testing.T) {
	pack, err := nodepack.Decode(filterManifest(`[{"key": "triggerFilter", "label": "Filter", "kind": "string"}]`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_, _, err = nodepack.Load(pack)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Load() error = %v, want the reserved-key refusal", err)
	}
}
