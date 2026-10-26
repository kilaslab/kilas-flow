package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// hostEventDocument is a one-trigger workflow listening for the named event
// types, wired the way the built-in Host Event trigger stores its
// subscriptions.
func hostEventDocument(id string, events ...string) workflow.Document {
	document := listDocument(id, "Evented")
	document.Nodes = []workflow.Node{{
		ID: "listener", Name: "Host Event", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"events": events},
	}}
	document.Connections = []workflow.Connection{}
	return document
}

// parameterHostEvents is the extractor half of a per-node subscription: the
// same reading the built-in trigger's declaration gets, expressed directly so
// the repository test does not depend on the node catalogue.
func parameterHostEvents(document workflow.Document) []repository.HostEventTrigger {
	triggers := make([]repository.HostEventTrigger, 0, 1)
	for _, node := range document.Nodes {
		if node.Disabled || node.Type != "kilasflow.hostEvent" {
			continue
		}
		events, _ := node.Parameters["events"].([]any)
		listened := make([]string, 0, len(events))
		for _, entry := range events {
			if text, ok := entry.(string); ok && text != "" {
				listened = append(listened, text)
			}
		}
		if len(listened) == 0 {
			continue
		}
		triggers = append(triggers, repository.HostEventTrigger{
			NodeID: node.ID, NodeType: node.Type, Events: listened,
		})
	}
	return triggers
}

func hostEventCatalog() activationCatalog {
	return activationCatalog{"kilasflow.hostEvent": {
		Type: "kilasflow.hostEvent", Version: workflow.V(1),
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}}
}

// Activation makes a workflow's host-event subscriptions reachable, and
// deactivation and deletion drop them with the same commit — the contract
// webhook bindings hold, read here for the events family.
func TestHostEventSubscriptionsFollowActivation(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-host-events"}
		catalog := hostEventCatalog()
		workflows := repository.NewWorkflowStore(db.DB).WithHostEvents(parameterHostEvents)

		saved, err := workflows.SaveDraft(ctx, tenant, hostEventDocument("wf_events", "message.received", "contact.tagged"))
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalog); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		targets, err := workflows.HostEventSubscriptions(ctx, tenant, "message.received")
		if err != nil {
			t.Fatalf("HostEventSubscriptions() error = %v", err)
		}
		if len(targets) != 1 || targets[0].WorkflowID != saved.ID || targets[0].NodeID != "listener" {
			t.Fatalf("HostEventSubscriptions() = %+v, want the listener of this workflow", targets)
		}
		if targets[0].WorkflowVersionID == "" {
			t.Error("subscription carries no version id, so a queued run could not pin the active revision")
		}

		// A re-activation with a different subscription list replaces the
		// rows: the events the new document listens for are reachable and the
		// old ones are not.
		revised := hostEventDocument("wf_events", "booking.created")
		revised.Nodes[0].Parameters["events"] = []any{"booking.created"}
		if _, err := workflows.SaveDraft(ctx, tenant, revised); err != nil {
			t.Fatalf("SaveDraft(revised) error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalog); err != nil {
			t.Fatalf("Activate(revised) error = %v", err)
		}
		if targets, err = workflows.HostEventSubscriptions(ctx, tenant, "message.received"); err != nil || len(targets) != 0 {
			t.Errorf("HostEventSubscriptions(old type) = (%v, %v), want none after re-activation", targets, err)
		}
		if targets, err = workflows.HostEventSubscriptions(ctx, tenant, "booking.created"); err != nil || len(targets) != 1 {
			t.Errorf("HostEventSubscriptions(new type) = (%v, %v), want the revised subscription", targets, err)
		}

		// Another tenant's event of the same type reaches nothing of ours.
		other, err := workflows.HostEventSubscriptions(ctx, repository.TenantScope{ID: "drv-host-events-other"}, "booking.created")
		if err != nil || len(other) != 0 {
			t.Errorf("HostEventSubscriptions(other tenant) = (%v, %v), want none", other, err)
		}

		if _, err := workflows.Deactivate(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("Deactivate() error = %v", err)
		}
		if targets, err = workflows.HostEventSubscriptions(ctx, tenant, "booking.created"); err != nil || len(targets) != 0 {
			t.Errorf("HostEventSubscriptions(after deactivate) = (%v, %v), want none", targets, err)
		}

		// Deletion of an active workflow drops them too: a soft-deleted
		// workflow must not keep hearing events.
		saved, err = workflows.SaveDraft(ctx, tenant, hostEventDocument("wf_events_2", "contact.tagged"))
		if err != nil {
			t.Fatalf("SaveDraft(second) error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalog); err != nil {
			t.Fatalf("Activate(second) error = %v", err)
		}
		if err := workflows.Delete(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if targets, err = workflows.HostEventSubscriptions(ctx, tenant, "contact.tagged"); err != nil || len(targets) != 0 {
			t.Errorf("HostEventSubscriptions(after delete) = (%v, %v), want none", targets, err)
		}
	})
}

// A claim on (event id, workflow) is won once: the loser learns the execution
// the winner recorded, and an expired claim frees the pair for a later event.
func TestClaimEventDeliveryDeduplicatesPerEventAndWorkflow(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-event-claims"}
		workflows := repository.NewWorkflowStore(db.DB)

		owner, claimed, err := workflows.ClaimEventDelivery(ctx, tenant.ID, "evt-1", "wf_a", "", time.Minute)
		if err != nil || !claimed {
			t.Fatalf("ClaimEventDelivery(first) = (%q, %v, %v), want the claim", owner, claimed, err)
		}
		if err := workflows.RecordEventExecution(ctx, tenant.ID, "evt-1", "wf_a", "exec_1"); err != nil {
			t.Fatalf("RecordEventExecution() error = %v", err)
		}

		// The same event to the same workflow loses and names the winner.
		owner, claimed, err = workflows.ClaimEventDelivery(ctx, tenant.ID, "evt-1", "wf_a", "", time.Minute)
		if err != nil {
			t.Fatalf("ClaimEventDelivery(duplicate) error = %v", err)
		}
		if claimed || owner != "exec_1" {
			t.Errorf("ClaimEventDelivery(duplicate) = (%q, %v), want the loser reading exec_1", owner, claimed)
		}

		// The same event to another workflow is a different pair and wins.
		if _, claimed, err = workflows.ClaimEventDelivery(ctx, tenant.ID, "evt-1", "wf_b", "", time.Minute); err != nil || !claimed {
			t.Errorf("ClaimEventDelivery(other workflow) = (%v, %v), want the claim", claimed, err)
		}

		// Another tenant's claim on the same pair is its own: the dedupe
		// never crosses tenants.
		if _, claimed, err = workflows.ClaimEventDelivery(ctx, "drv-event-claims-other", "evt-1", "wf_a", "", time.Minute); err != nil || !claimed {
			t.Errorf("ClaimEventDelivery(other tenant) = (%v, %v), want the claim", claimed, err)
		}

		// An event with no id is never collapsed with another.
		if _, claimed, err = workflows.ClaimEventDelivery(ctx, tenant.ID, "", "wf_a", "", time.Minute); err != nil || !claimed {
			t.Errorf("ClaimEventDelivery(no id) = (%v, %v), want the claim", claimed, err)
		}
		if _, _, err = workflows.ClaimEventDelivery(ctx, "", "evt-1", "wf_a", "", time.Minute); err == nil {
			t.Error("ClaimEventDelivery(no tenant) = nil error, want ErrTenantRequired")
		}

		// An expired claim is not a duplicate: the pair is free again.
		if err := db.Exec("UPDATE host_event_deliveries SET expires_at = ? WHERE tenant_id = ? AND event_id = ? AND workflow_id = ?",
			time.Now().UTC().Add(-time.Minute), tenant.ID, "evt-1", "wf_a").Error; err != nil {
			t.Fatalf("expire the claim: %v", err)
		}
		if _, claimed, err = workflows.ClaimEventDelivery(ctx, tenant.ID, "evt-1", "wf_a", "", time.Minute); err != nil || !claimed {
			t.Errorf("ClaimEventDelivery(expired) = (%v, %v), want the claim again", claimed, err)
		}
	})
}
