package hostevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// stubCatalog resolves the types a test document names, the shape the node
// registry's Resolve answers with.
type stubCatalog map[string]node.Definition

func (catalog stubCatalog) Resolve(nodeType string, version workflow.TypeVersion) (node.Definition, bool) {
	definition, found := catalog[nodeType]
	if !found || definition.Version.Compare(version) != 0 {
		return node.Definition{}, false
	}
	return definition, true
}

func mainPort() workflow.Port {
	return workflow.Port{Name: "main", Kind: workflow.ConnectionMain}
}

// Extraction reads both subscription shapes: the built-in trigger's parameter
// and a pack trigger's fixed event list, and nothing else.
func TestExtractReadsBothSubscriptionShapes(t *testing.T) {
	catalog := stubCatalog{
		"kilasflow.hostEvent": {
			Type: "kilasflow.hostEvent", Version: workflow.V(1), Outputs: []workflow.Port{mainPort()},
			HostEvents: &node.HostEventDeclaration{Parameter: "events"},
		},
		"pack.acme": {
			Type: "pack.acme", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "message.received", Kind: workflow.ConnectionMain}, {Name: "other", Kind: workflow.ConnectionMain}},
			HostEvents: &node.HostEventDeclaration{
				Events: []string{"message.received", "contact.tagged"}, EventPath: "event",
			},
		},
		"kilasflow.webhook": {
			Type: "kilasflow.webhook", Version: workflow.V(1), Outputs: []workflow.Port{mainPort()},
		},
	}
	extract := Extract(catalog)

	document := workflow.Document{Nodes: []workflow.Node{
		{ID: "picked", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": []any{"booking.created", "contact.tagged"}}},
		{ID: "single", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": "message.received"}},
		{ID: "none", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": []any{}}},
		{ID: "off", Type: "kilasflow.hostEvent", TypeVersion: workflow.V(1), Disabled: true,
			Parameters: map[string]any{"events": []any{"booking.created"}}},
		{ID: "pack", Type: "pack.acme", TypeVersion: workflow.V(1)},
		{ID: "webhook", Type: "kilasflow.webhook", TypeVersion: workflow.V(1)},
	}}

	triggers := extract(document)
	want := []repository.HostEventTrigger{
		{NodeID: "picked", NodeType: "kilasflow.hostEvent",
			Events: []string{"booking.created", "contact.tagged"}},
		{NodeID: "single", NodeType: "kilasflow.hostEvent", Events: []string{"message.received"}},
		{NodeID: "pack", NodeType: "pack.acme",
			Events: []string{"message.received", "contact.tagged"}, EventPath: "event"},
	}
	if !reflect.DeepEqual(triggers, want) {
		t.Errorf("Extract() = %+v, want %+v", triggers, want)
	}
}

// fakeSubscriptions hands the service a fixed set of targets per event type.
type fakeSubscriptions struct {
	byType map[string][]repository.HostEventTarget
}

func (store fakeSubscriptions) HostEventSubscriptions(_ context.Context, _ repository.TenantScope, eventType string) ([]repository.HostEventTarget, error) {
	return store.byType[eventType], nil
}

// fakeClaims records claims in memory, the insert-fails-on-conflict half.
type fakeClaims struct {
	claimed  map[string]string
	recorded map[string]string
	existing map[string]string
}

func (store *fakeClaims) ClaimEventDelivery(_ context.Context, _, eventID, workflowID, _ string, _ time.Duration) (string, bool, error) {
	if store.claimed == nil {
		store.claimed = map[string]string{}
	}
	key := eventID + "\x00" + workflowID
	if _, found := store.claimed[key]; found {
		return store.existing[key], false, nil
	}
	store.claimed[key] = ""
	return "", true, nil
}

func (store *fakeClaims) RecordEventExecution(_ context.Context, _, eventID, workflowID, executionID string) error {
	if store.recorded == nil {
		store.recorded = map[string]string{}
	}
	store.recorded[eventID+"\x00"+workflowID] = executionID
	store.existing = store.claimed
	store.existing[eventID+"\x00"+workflowID] = executionID
	return nil
}

// fakeRunner queues runs in order, optionally failing the named workflow with
// the store's not-found, which is what an inactive workflow answers with.
type fakeRunner struct {
	queued   []string
	failWith map[string]error
	inputs   map[string]json.RawMessage
	counter  int
}

func (runner *fakeRunner) QueueHostEvent(_ context.Context, _ repository.TenantScope, workflowID, _, triggerNodeID string, input json.RawMessage) (execution.Record, error) {
	if err, found := runner.failWith[workflowID]; found {
		return execution.Record{}, err
	}
	runner.counter++
	if runner.inputs == nil {
		runner.inputs = map[string]json.RawMessage{}
	}
	runner.inputs[workflowID] = input
	runner.queued = append(runner.queued, workflowID)
	return execution.Record{ID: "exec_" + string(rune('a'+runner.counter-1)), TriggerNodeID: triggerNodeID}, nil
}

var errNotFound = fmt.Errorf("%w: active workflow", repository.ErrNotFound)

// One event, one execution per workflow: matched carries the workflows that
// ran, duplicates and deactivations are filtered with the reason, and the
// item is the event document.
func TestPublishFansOutAndReports(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n1", EventPath: ""},
			{WorkflowID: "wf_b", WorkflowVersionID: "v1", NodeID: "n2", EventPath: "event"},
			{WorkflowID: "wf_c", WorkflowVersionID: "v1", NodeID: "n3", EventPath: ""},
		},
	}}
	runner := &fakeRunner{failWith: map[string]error{"wf_c": errNotFound}}
	service := NewService(subscriptions, &fakeClaims{}, runner, time.Minute)

	result, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{
		ID: "evt-9", Type: "message.received",
		Payload: json.RawMessage(`{"contactId":"c_1"}`),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	want := Result{
		Matched: []Matched{
			{WorkflowID: "wf_a", ExecutionID: "exec_a"},
			{WorkflowID: "wf_b", ExecutionID: "exec_b"},
		},
		Filtered: []Filtered{{WorkflowID: "wf_c", Reason: FilteredInactive}},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Publish() = %+v, want %+v", result, want)
	}

	// The item is the event document: type, payload, occurrence time and id.
	var document map[string]any
	if err := json.Unmarshal(runner.inputs["wf_a"], &document); err != nil {
		t.Fatalf("wf_a input is not JSON: %v", err)
	}
	if document["type"] != "message.received" || document["eventId"] != "evt-9" {
		t.Errorf("wf_a input = %v, want the event's type and id", document)
	}
	payload, _ := document["payload"].(map[string]any)
	if payload["contactId"] != "c_1" {
		t.Errorf("wf_a payload = %v, want it carried through", document["payload"])
	}
	if _, ok := document["occurredAt"].(string); !ok {
		t.Errorf("wf_a input carries no occurredAt: %v", document)
	}

	// A pack trigger's item also names the event at its event path, so the
	// same ports a webhook delivery routes to work for a published event.
	var routed map[string]any
	if err := json.Unmarshal(runner.inputs["wf_b"], &routed); err != nil {
		t.Fatalf("wf_b input is not JSON: %v", err)
	}
	if routed["event"] != "message.received" {
		t.Errorf("wf_b input = %v, want the type at the event path `event`", routed)
	}
}

// A retried publish with the same event id reaches each workflow once, and
// the duplicate is reported with the execution it collapsed with.
func TestPublishDeduplicatesPerEventAndWorkflow(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n1"},
		},
	}}
	claims := &fakeClaims{existing: map[string]string{}}
	runner := &fakeRunner{}
	service := NewService(subscriptions, claims, runner, time.Minute)
	tenant := repository.TenantScope{ID: "tenant"}

	if _, err := service.Publish(context.Background(), tenant, Event{ID: "evt-1", Type: "message.received"}); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	result, err := service.Publish(context.Background(), tenant, Event{ID: "evt-1", Type: "message.received"})
	if err != nil {
		t.Fatalf("Publish(retry) error = %v", err)
	}
	if len(result.Matched) != 0 {
		t.Errorf("Publish(retry) matched = %+v, want none", result.Matched)
	}
	if len(result.Filtered) != 1 || result.Filtered[0].Reason != FilteredDuplicate {
		t.Errorf("Publish(retry) filtered = %+v, want one duplicate", result.Filtered)
	}
	if len(runner.queued) != 1 {
		t.Errorf("queued %d runs across both publishes, want 1", len(runner.queued))
	}

	// An event with no id is never collapsed: every publish runs.
	for range 2 {
		if _, err := service.Publish(context.Background(), tenant, Event{Type: "message.received"}); err != nil {
			t.Fatalf("Publish(no id) error = %v", err)
		}
	}
	if len(runner.queued) != 3 {
		t.Errorf("queued %d runs, want 3 (one deduped, two unidentifiable)", len(runner.queued))
	}
}

// A workflow with two triggers listening for the same type queues one
// execution, started from the first of them: the fan-out unit is the
// workflow, and the dedupe key the API promises is per (event id, workflow).
func TestPublishQueuesOneExecutionPerWorkflow(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n_first", EventPath: ""},
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n_second", EventPath: ""},
		},
	}}
	runner := &fakeRunner{}
	service := NewService(subscriptions, &fakeClaims{}, runner, time.Minute)

	result, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{Type: "message.received"})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(result.Matched) != 1 || result.Matched[0].WorkflowID != "wf_a" {
		t.Fatalf("Publish() matched = %+v, want one execution for the workflow", result.Matched)
	}
	if runner.queued[0] != "wf_a" || len(runner.queued) != 1 {
		t.Fatalf("queued = %v, want the one workflow", runner.queued)
	}
	if result.Matched[0].ExecutionID == "" {
		t.Error("matched carries no execution id")
	}
}

// An event nobody listens for is accepted and matches nothing, which is the
// documented no-op rather than an error.
func TestPublishWithNoListenersMatchesNothing(t *testing.T) {
	service := NewService(fakeSubscriptions{byType: map[string][]repository.HostEventTarget{}}, &fakeClaims{}, &fakeRunner{}, time.Minute)
	result, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{Type: " unheard.of"})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(result.Matched) != 0 || len(result.Filtered) != 0 {
		t.Errorf("Publish() = %+v, want nothing matched and nothing filtered", result)
	}
}

// A queue failure is the caller's to retry: the executions already queued
// stay queued, and the per-workflow claim makes the retry finish the fan-out
// rather than repeat it.
func TestPublishReturnsTheQueueFailure(t *testing.T) {
	subscriptions := fakeSubscriptions{byType: map[string][]repository.HostEventTarget{
		"message.received": {
			{WorkflowID: "wf_a", WorkflowVersionID: "v1", NodeID: "n1"},
			{WorkflowID: "wf_b", WorkflowVersionID: "v1", NodeID: "n2"},
		},
	}}
	runner := &fakeRunner{failWith: map[string]error{"wf_b": errors.New("the queue is on fire")}}
	service := NewService(subscriptions, &fakeClaims{}, runner, time.Minute)

	if _, err := service.Publish(context.Background(), repository.TenantScope{ID: "tenant"}, Event{Type: "message.received"}); err == nil {
		t.Fatal("Publish() = nil error, want the queue failure")
	} else if len(runner.queued) != 1 {
		t.Errorf("queued = %v, want the workflow before the failing one", runner.queued)
	}
}

// The built-in trigger, read through the real registry the server assembles:
// one output, subscriptions chosen per node through its events parameter, and
// no event path — the item every event arrives as is the whole document.
func TestExtractReadsTheBuiltInTrigger(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	extract := Extract(registry)

	triggers := extract(workflow.Document{Nodes: []workflow.Node{
		{ID: "listen", Type: nodes.HostEventNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"events": []any{"message.received"}}},
	}})
	want := []repository.HostEventTrigger{
		{NodeID: "listen", NodeType: nodes.HostEventNodeType, Events: []string{"message.received"}},
	}
	if !reflect.DeepEqual(triggers, want) {
		t.Fatalf("Extract(built-in) = %+v, want %+v", triggers, want)
	}

	// A document without a host-event trigger extracts nothing: schedules,
	// webhooks and action nodes are not event subscriptions.
	if triggers = extract(workflow.Document{Nodes: []workflow.Node{
		{ID: "clock", Type: "kilasflow.schedule", TypeVersion: workflow.V(1)},
	}}); len(triggers) != 0 {
		t.Errorf("Extract(schedule) = %+v, want none", triggers)
	}
}
