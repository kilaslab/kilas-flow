package hostevents

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/triggerfilter"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// channelFilter admits events whose payload names one channel, written the way
// the editor saves an IF condition.
func channelFilter(channel string) map[string]any {
	return map[string]any{
		"combinator": "and",
		"conditions": []any{
			map[string]any{
				"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.payload.channel }}"},
				"rightValue": channel,
				"operator":   map[string]any{"type": "string", "operation": "equals"},
			},
		},
	}
}

// The extractor carries a trigger's filter onto its subscriptions as
// authored, so the fan-out resolves it per event.
func TestExtractCarriesTheTriggerFilter(t *testing.T) {
	catalog := stubCatalog{
		"kilasflow.hostEvent": {
			Type: "kilasflow.hostEvent", Version: workflow.V(1), Outputs: []workflow.Port{mainPort()},
			HostEvents: &node.HostEventDeclaration{Parameter: "events"},
		},
	}
	filter := channelFilter("whatsapp")
	document := workflow.Document{Nodes: []workflow.Node{
		{ID: "filtered", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": "message.received", triggerfilter.ParameterKey: filter}},
		{ID: "open", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": "message.received"}},
	}}

	triggers := Extract(catalog)(document)
	if len(triggers) != 2 {
		t.Fatalf("Extract() = %+v, want two triggers", triggers)
	}
	if !reflect.DeepEqual(triggers[0].Filter, filter) {
		t.Errorf("the filtered trigger carries %v, want its filter as authored", triggers[0].Filter)
	}
	if triggers[1].Filter != nil {
		t.Errorf("the open trigger carries %v, want no filter", triggers[1].Filter)
	}
}

// A workflow whose trigger filter refuses the event is reported as filtered
// with the reason and queues nothing; a workflow without a filter, and one
// whose filter admits the event, run as before.
func TestPublishDropsEventsTheTriggerFilterRefuses(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_email", WorkflowVersionID: "v1", NodeID: "n1", Filter: channelFilter("email")},
			{WorkflowID: "wf_open", WorkflowVersionID: "v1", NodeID: "n1"},
			{WorkflowID: "wf_whatsapp", WorkflowVersionID: "v1", NodeID: "n1", Filter: channelFilter("whatsapp")},
		},
	}}
	runner := &fakeRunner{}
	claims := &fakeClaims{}
	service := NewService(subscriptions, claims, runner, time.Minute)

	result, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{
		ID: "evt_1", Type: "message.received", Payload: json.RawMessage(`{"channel":"whatsapp","text":"hi"}`),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if want := []string{"wf_open", "wf_whatsapp"}; !reflect.DeepEqual(runner.queued, want) {
		t.Fatalf("queued = %v, want %v", runner.queued, want)
	}
	if len(result.Filtered) != 1 {
		t.Fatalf("filtered = %+v, want exactly the email workflow", result.Filtered)
	}
	filtered := result.Filtered[0]
	if filtered.WorkflowID != "wf_email" || filtered.Reason != FilteredCondition {
		t.Fatalf("filtered = %+v, want wf_email with reason %q", filtered, FilteredCondition)
	}
	if !strings.Contains(filtered.Detail, "did not match") {
		t.Errorf("filtered detail = %q, want it to say the event did not match", filtered.Detail)
	}
	// A filtered workflow is not claimed: a later publish of the same event id
	// after the author widened the filter must still be able to reach it.
	if _, claimed := claims.claimed["evt_1\x00wf_email"]; claimed {
		t.Error("the filtered workflow's delivery was claimed, want it left unclaimed")
	}
}

// When a workflow has two listening triggers, a refusal by the first hands the
// event to the second; the workflow is filtered only when neither admits it.
func TestPublishTriesEveryTriggerOfAWorkflowBeforeFiltering(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n_email", Filter: channelFilter("email")},
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n_whatsapp", Filter: channelFilter("whatsapp")},
			{WorkflowID: "wf_b", WorkflowVersionID: "v1", NodeID: "n_email", Filter: channelFilter("email")},
			{WorkflowID: "wf_b", WorkflowVersionID: "v1", NodeID: "n_sms", Filter: channelFilter("sms")},
		},
	}}
	runner := &fakeRunner{}
	service := NewService(subscriptions, &fakeClaims{}, runner, time.Minute)

	result, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{
		Type: "message.received", Payload: json.RawMessage(`{"channel":"whatsapp"}`),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(result.Matched) != 1 || result.Matched[0].WorkflowID != "wf_a" {
		t.Fatalf("matched = %+v, want wf_a only", result.Matched)
	}
	if len(result.Filtered) != 1 || result.Filtered[0].WorkflowID != "wf_b" {
		t.Fatalf("filtered = %+v, want wf_b reported once", result.Filtered)
	}
	if got := len(runner.queued); got != 1 {
		t.Fatalf("queued %d executions, want one", got)
	}
}
