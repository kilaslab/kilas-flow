package nodes_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/nodes"
)

// TestEveryBuiltinDeclaresItsPresentation is what stops a node arriving on the
// canvas as an unlabelled grey box. It is asserted over the whole registry
// rather than a list, so a node added later is covered without anyone
// remembering to extend this.
func TestEveryBuiltinDeclaresItsPresentation(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, definition := range registry.List() {
		if len(definition.Group) == 0 {
			t.Errorf("%s declares no group", definition.Type)
		}
		if definition.Icon == nil || definition.Icon.Light == "" {
			t.Errorf("%s declares no icon", definition.Type)
		}
		if definition.IconColor == "" {
			t.Errorf("%s declares no accent colour", definition.Type)
		}
	}
}

// TestTriggerGroupIsBehaviouralNotACategoryLabel pins the split this ticket
// exists for. The picker decided whether a node could start a workflow by
// comparing a display string, which is behaviour inferred from a caption.
func TestTriggerGroupIsBehaviouralNotACategoryLabel(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	triggers := map[string]bool{}
	for _, definition := range registry.List() {
		for _, group := range definition.Group {
			if group == node.GroupTrigger {
				triggers[definition.Type] = true
			}
		}
	}
	for _, want := range []string{"kilasflow.manual", "kilasflow.webhook", "kilasflow.schedule", "kilasflow.chatTrigger"} {
		if !triggers[want] {
			t.Errorf("%s is not in the trigger group", want)
		}
	}
	// A node that merely sits under a "Triggers" caption is not one, and a
	// transform is never a trigger however it is filed.
	for _, unwanted := range []string{"kilasflow.set", "kilasflow.httpRequest", "kilasflow.stickyNote"} {
		if triggers[unwanted] {
			t.Errorf("%s is classified as a trigger", unwanted)
		}
	}
}

// TestEveryBuiltinCanAlwaysOutputData pins the toggle's presence in every
// node's settings.
//
// The engine has honoured `alwaysOutputData` for a while, but the setting was
// never declared, so the editor had nothing to render: the one option that makes
// a no-rows read still answer `{}` was unreachable from the UI. Asserted over
// the whole registry, so a node added later inherits it by construction.
func TestEveryBuiltinCanAlwaysOutputData(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, definition := range registry.List() {
		// The canvas annotation is the one definition with no settings at all,
		// and it is named rather than matched by a rule so any other node that
		// loses its settings still fails this test: a note never runs, so a
		// retry or output toggle on it would be a control that does nothing.
		if definition.Type == nodes.StickyNoteNodeType {
			if len(definition.SharedSettings) != 0 {
				t.Errorf("%s declared settings; a note never runs", definition.Type)
			}
			continue
		}
		declared := false
		for _, setting := range definition.SharedSettings {
			if setting.Key == "alwaysOutputData" {
				declared = setting.Kind == node.PropertyBoolean
			}
		}
		if !declared {
			t.Errorf("%s does not offer alwaysOutputData as a boolean setting", definition.Type)
		}
	}
}
