package hostevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// Runner queues one triggered execution and wakes a worker. *engine.Service
// satisfies it.
type Runner interface {
	QueueHostEvent(ctx context.Context, tenant repository.TenantScope, workflowID, versionID, triggerNodeID string, input json.RawMessage) (execution.Record, error)
}

// SubscriptionStore lists the active subscriptions to one event type.
type SubscriptionStore interface {
	HostEventSubscriptions(ctx context.Context, tenant repository.TenantScope, eventType string) ([]repository.HostEventTarget, error)
}

// DeliveryClaims dedupe a retried publish, per (event id, workflow).
type DeliveryClaims interface {
	ClaimEventDelivery(ctx context.Context, tenantID, eventID, workflowID, executionID string, window time.Duration) (string, bool, error)
	RecordEventExecution(ctx context.Context, tenantID, eventID, workflowID, executionID string) error
}

// Service is the fan-out: one published event becomes one queued execution
// per active workflow listening for it.
type Service struct {
	subscriptions SubscriptionStore
	claims        DeliveryClaims
	runner        Runner
	window        time.Duration
}

// NewService builds the fan-out. A non-positive window falls back to the
// repository's delivery window, so a caller that configures nothing gets the
// same five minutes a retried webhook delivery is remembered for.
func NewService(subscriptions SubscriptionStore, claims DeliveryClaims, runner Runner, window time.Duration) *Service {
	if window <= 0 {
		window = repository.DefaultDeliveryWindow
	}
	return &Service{subscriptions: subscriptions, claims: claims, runner: runner, window: window}
}

// Event is one published host event.
type Event struct {
	// ID is the host's own identifier for this event. When set it is the
	// dedupe key: publishing the same id again reaches each workflow once,
	// within the delivery window, no matter how many times it is retried.
	ID string
	// Type is the event type, exactly as triggers subscribe to it.
	Type string
	// Payload is the event's body, carried through to the workflow untouched.
	Payload json.RawMessage
	// OccurredAt is when the event happened on the host's clock. Zero means
	// the server stamps it at publish time.
	OccurredAt time.Time
}

// Matched is one workflow the event reached, and the execution it queued.
type Matched struct {
	WorkflowID  string `json:"workflowId"`
	ExecutionID string `json:"executionId"`
}

// FilteredReason names why a considered workflow did not run.
const (
	// FilteredDuplicate: this event id already reached this workflow inside
	// the delivery window, so the publish was collapsed with the first one.
	FilteredDuplicate = "duplicate"
	// FilteredInactive: the workflow was subscribed when the event was
	// published but no longer active when its execution was queued — the
	// race window between listing subscriptions and queueing.
	FilteredInactive = "inactive"
)

// Filtered is one workflow the event considered but did not run, and why.
type Filtered struct {
	WorkflowID string `json:"workflowId"`
	// Reason is FilteredDuplicate or FilteredInactive.
	Reason string `json:"reason"`
	// ExecutionID is the execution a duplicate was collapsed with, when it is
	// known. Empty when the first publish had not recorded its execution yet.
	ExecutionID string `json:"executionId,omitempty"`
}

// Result is what publishing one event did.
type Result struct {
	// Matched is every workflow that queued an execution, one entry per
	// workflow.
	Matched []Matched `json:"matched"`
	// Filtered is every workflow that was considered and did not run, with
	// the reason.
	Filtered []Filtered `json:"filtered"`
}

// Publish fans one event out to the tenant's active workflows.
//
// One execution is queued per workflow whose trigger lists the event's type,
// running the workflow's active revision: the queue step re-checks activation
// and the pinned version, so a workflow deactivated between the subscription
// listing and the queue is reported under FilteredInactive rather than run.
// A workflow with several triggers listening for the type queues one
// execution, started from the first of them.
//
// When the event carries an id, each (id, workflow) pair is claimed before
// its execution is queued, so a retried publish — a different idempotency
// key, another process, a request that timed out half way through the
// fan-out — collapses with the first one instead of running the workflow a
// second time. A queue failure is returned as an error rather than silently
// reported: the executions already queued stay queued, and the host's retry,
// deduped per workflow, finishes the fan-out.
func (service *Service) Publish(ctx context.Context, tenant repository.TenantScope, event Event) (Result, error) {
	if tenant.ID == "" {
		return Result{}, repository.ErrTenantRequired
	}
	if strings.TrimSpace(event.Type) == "" {
		return Result{}, fmt.Errorf("event type is required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}

	targets, err := service.subscriptions.HostEventSubscriptions(ctx, tenant, event.Type)
	if err != nil {
		return Result{}, err
	}

	document, err := event.document()
	if err != nil {
		return Result{}, err
	}

	result := Result{Matched: []Matched{}, Filtered: []Filtered{}}
	reached := make(map[string]bool, len(targets))
	for _, target := range targets {
		// One execution per workflow: the first trigger node listening for
		// the type starts the run, and a later node of the same workflow is
		// not a second execution.
		if reached[target.WorkflowID] {
			continue
		}
		reached[target.WorkflowID] = true

		if event.ID != "" {
			owner, claimed, err := service.claims.ClaimEventDelivery(ctx, tenant.ID, event.ID, target.WorkflowID, "", service.window)
			if err != nil {
				return Result{}, err
			}
			if !claimed {
				result.Filtered = append(result.Filtered, Filtered{
					WorkflowID: target.WorkflowID, Reason: FilteredDuplicate, ExecutionID: owner,
				})
				continue
			}
		}

		record, err := service.runner.QueueHostEvent(ctx, tenant, target.WorkflowID, target.WorkflowVersionID, target.NodeID, item(target.EventPath, document))
		if errors.Is(err, repository.ErrNotFound) {
			// The subscription said active and the queue said otherwise: the
			// workflow was deactivated, or its active version replaced, in
			// the window between the two reads.
			result.Filtered = append(result.Filtered, Filtered{
				WorkflowID: target.WorkflowID, Reason: FilteredInactive,
			})
			continue
		}
		if err != nil {
			return Result{}, err
		}
		if event.ID != "" {
			_ = service.claims.RecordEventExecution(ctx, tenant.ID, event.ID, target.WorkflowID, record.ID)
		}
		result.Matched = append(result.Matched, Matched{WorkflowID: target.WorkflowID, ExecutionID: record.ID})
	}
	return result, nil
}

// document builds the trigger item every matched workflow starts from: the
// event's own type, payload, occurrence time and id.
//
// One shape for every trigger, so a workflow reading `{{ $json.payload }}` or
// branching on `{{ $json.type }}` means the same thing whichever trigger
// family heard the event. The payload passes through as it arrived; an absent
// one becomes an empty object, which is an empty payload rather than a JSON
// null the next node would have to guard against.
func (event Event) document() (map[string]any, error) {
	var payload any = map[string]any{}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, fmt.Errorf("event payload is not valid JSON: %w", err)
		}
		if payload == nil {
			payload = map[string]any{}
		}
	}
	document := map[string]any{
		"type":       event.Type,
		"payload":    payload,
		"occurredAt": event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	if event.ID != "" {
		document["eventId"] = event.ID
	}
	return document, nil
}

// item is the trigger item for one target: the event document, with the
// event's type also placed at the target's event path.
//
// A pack trigger that opted into host events routes its item to the port
// matching the event, reading the type at the path its webhook deliveries
// use — `event`, for a WAHA-shaped pack. Placing the type there is what makes
// the same ports work for a published event without the pack shipping a
// second routing rule. An empty path, or the path already naming the type,
// needs no second copy.
func item(eventPath string, document map[string]any) json.RawMessage {
	if eventPath == "" || eventPath == "type" {
		encoded, err := json.Marshal(document)
		if err != nil {
			return json.RawMessage(`{}`)
		}
		return encoded
	}
	nested := make(map[string]any, len(document)+1)
	for key, value := range document {
		nested[key] = value
	}
	segments := strings.Split(eventPath, ".")
	cursor := nested
	for _, segment := range segments[:len(segments)-1] {
		next, ok := cursor[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			cursor[segment] = next
		}
		cursor = next
	}
	cursor[segments[len(segments)-1]] = document["type"]
	encoded, err := json.Marshal(nested)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
