package nodepack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
)

// hostEventManifest wraps a host-events block in an otherwise well-formed
// trigger pack, so each case varies only the part under test. An empty
// hostEvents omits the block entirely.
func hostEventManifest(hostEvents string) []byte {
	block := ""
	if hostEvents != "" {
		block = `,"hostEvents": ` + hostEvents
	}
	return []byte(`{
		"type": "pack.hostEventTrigger", "version": 1,
		"displayName": "Host Event Trigger", "category": "Triggers",
		"requestDefaults": {},
		"trigger": {
			"events": ["message.received", "contact.tagged"], "catchAll": "other",
			"eventPath": "event", "shape": "bodyAsItem",
			"webhook": {"name": "default", "staticPath": "events", "method": "POST"}` + block + `
		},
		"parameters": []
	}`)
}

// A host-events block opts a trigger into event delivery, and the catalogue
// it carries has to describe the trigger's own events as valid JSON.
func TestATriggerHostEventsBlockValidates(t *testing.T) {
	t.Parallel()

	for name, manifest := range map[string][]byte{
		"an opt-in with no catalogue": hostEventManifest(`{}`),
		"a full catalogue":            hostEventManifest(`{"catalogue": [{"type": "message.received", "title": "Message received", "schema": {"type": "object"}, "sample": {"type": "message.received", "payload": {"id": "m_1"}}}]}`),
		"part of the events":          hostEventManifest(`{"catalogue": [{"type": "contact.tagged"}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if issues := nodepack.ValidateBytes(manifest, "pack.json"); len(issues) != 0 {
				t.Fatalf("a well-formed hostEvents block was refused: %v", issues)
			}
		})
	}

	for name, manifest := range map[string][]byte{
		"an event the trigger does not list": hostEventManifest(`{"catalogue": [{"type": "booking.created"}]}`),
		"an entry with no type":              hostEventManifest(`{"catalogue": [{"title": "No type"}]}`),
		"an entry declared twice":            hostEventManifest(`{"catalogue": [{"type": "message.received"}, {"type": "message.received"}]}`),
		"a schema that is not an object":     hostEventManifest(`{"catalogue": [{"type": "message.received", "schema": "https://example.com/schema"}]}`),
		"a sample that is not an object":     hostEventManifest(`{"catalogue": [{"type": "message.received", "sample": ["not", "an", "event"]}]}`),
		"an unknown field in the block":      hostEventManifest(`{"enabled": true}`),
		"an unknown field in an entry":       hostEventManifest(`{"catalogue": [{"type": "message.received", "description": "no such field"}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			issues := nodepack.ValidateBytes(manifest, "pack.json")
			if len(issues) == 0 {
				t.Fatal("a malformed hostEvents block was accepted")
			}
			// The refusal names the host-events block or its catalogue,
			// whichever arm produced it, so an author reads where to fix.
			joined := strings.Join(issueMessages(issues), " ")
			if !strings.Contains(joined, "hostEvents") && !strings.Contains(joined, "unknown field") {
				t.Errorf("refusals = %v, want one naming the hostEvents block or an unknown field", issues)
			}
		})
	}
}

// issueMessages renders each issue's message, for assertions that read better
// than Issue's %v.
func issueMessages(issues []nodepack.Issue) []string {
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Message)
	}
	return messages
}

// Loading an opted-in trigger carries both halves onto the definition the
// editor and the extractor read: the declaration that says how it subscribes,
// and the catalogue that says what to offer.
func TestLoadingCarriesTheHostEventHalvesOntoTheDefinition(t *testing.T) {
	t.Parallel()

	pack, err := nodepack.Decode(hostEventManifest(`{"catalogue": [{"type": "message.received", "title": "Message received", "schema": {"type": "object"}, "sample": {"type": "message.received", "payload": {}}}]}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	definition, description, err := nodepack.Load(pack)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if description != nil {
		t.Error("a trigger pack carries no routing description, and host events do not add one")
	}
	if definition.HostEvents == nil {
		t.Fatal("the definition declares no host events, so activation would subscribe nothing")
	}
	want := &node.HostEventDeclaration{
		Events: []string{"message.received", "contact.tagged"}, EventPath: "event",
	}
	if definition.HostEvents.Parameter != want.Parameter ||
		strings.Join(definition.HostEvents.Events, ",") != strings.Join(want.Events, ",") ||
		definition.HostEvents.EventPath != want.EventPath {
		t.Errorf("HostEvents = %+v, want %+v", definition.HostEvents, want)
	}
	if len(definition.EventCatalogue) != 1 || definition.EventCatalogue[0].Type != "message.received" {
		t.Fatalf("EventCatalogue = %+v, want the message.received entry", definition.EventCatalogue)
	}
	if string(definition.EventCatalogue[0].Schema) == "" || string(definition.EventCatalogue[0].Sample) == "" {
		t.Error("the catalogue entry carries no schema or sample, so the editor gets nothing to offer")
	}

	// A trigger that does not opt in declares neither half: the same
	// definition a pack without the block has always produced.
	pack, err = nodepack.Decode(hostEventManifest(""))
	if err != nil {
		t.Fatalf("Decode(plain) error = %v", err)
	}
	definition, _, err = nodepack.Load(pack)
	if err != nil {
		t.Fatalf("Load(plain) error = %v", err)
	}
	if definition.HostEvents != nil || definition.EventCatalogue != nil {
		t.Errorf("plain trigger declares host events: %+v / %+v", definition.HostEvents, definition.EventCatalogue)
	}
}

// The catalogue is served with the definition as JSON, the editor-facing half
// of the declaration, and an opt-in with no entries serialises as the empty
// list the picker renders.
func TestTheEventCatalogueIsServedOnTheDefinition(t *testing.T) {
	t.Parallel()

	pack, err := nodepack.Decode(hostEventManifest(`{}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	definition, _, err := nodepack.Load(pack)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"hostEvents":{"events":["message.received","contact.tagged"],"eventPath":"event"}`) {
		t.Errorf("the served definition does not carry the host-events declaration: %s", encoded)
	}
	if definition.EventCatalogue == nil {
		t.Error("an opt-in with no entries reads as not opted in, which loses the declaration's meaning")
	}
}
