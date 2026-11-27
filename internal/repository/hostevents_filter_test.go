package repository_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A trigger's filter is stored on its subscriptions at activation and read
// back with them, so the fan-out never recompiles a document per event. A
// trigger without one reads back as no filter.
func TestHostEventSubscriptionsCarryTheTriggerFilter(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-host-event-filter"}
		filter := map[string]any{
			"combinator": "and",
			"conditions": []any{map[string]any{
				"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.payload.channel }}"},
				"rightValue": "whatsapp",
				"operator":   map[string]any{"type": "string", "operation": "equals"},
			}},
		}
		extract := func(document workflow.Document) []repository.HostEventTrigger {
			triggers := parameterHostEvents(document)
			for index := range triggers {
				if triggers[index].NodeID == "listener" {
					triggers[index].Filter = filter
				}
			}
			return triggers
		}
		workflows := repository.NewWorkflowStore(db.DB).WithHostEvents(extract)

		filtered, err := workflows.SaveDraft(ctx, tenant, hostEventDocument("wf_filtered", "message.received"))
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, filtered.ID, hostEventCatalog()); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}
		open := hostEventDocument("wf_open", "message.received")
		open.Nodes[0].ID = "open_listener"
		saved, err := workflows.SaveDraft(ctx, tenant, open)
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, hostEventCatalog()); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		targets, err := workflows.HostEventSubscriptions(ctx, tenant, "message.received")
		if err != nil {
			t.Fatalf("HostEventSubscriptions() error = %v", err)
		}
		byNode := map[string]repository.HostEventTarget{}
		for _, target := range targets {
			byNode[target.NodeID] = target
		}
		if got := byNode["listener"].Filter; !reflect.DeepEqual(got, filter) {
			t.Errorf("the filtered subscription reads back %#v, want %#v", got, filter)
		}
		if got := byNode["open_listener"].Filter; got != nil {
			t.Errorf("the open subscription reads back %#v, want no filter", got)
		}
	})
}
