package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/hostevents"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/ratelimit"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// HostEventPublisher is the fan-out behind POST /events: internal/hostevents'
// service. Taken as an interface so the handler can be built and tested
// without the repository and engine the real one holds, the same seam the
// webhook surface's Runner is.
type HostEventPublisher interface {
	Publish(ctx context.Context, tenant repository.TenantScope, event hostevents.Event) (hostevents.Result, error)
}

// Events serves the host → workflow path.
type Events struct {
	publisher   HostEventPublisher
	tenants     TenantResolver
	idempotency *idempotency.Service
	// rateLimiter bounds how many events one tenant may publish per minute.
	// Optional: nil leaves the events API unlimited.
	rateLimiter RateLimiter
}

// NewEvents builds the events handler. A nil publisher leaves the endpoint
// answering 503 rather than half-working, which is what an instance without
// the fan-out wired should say.
func NewEvents(publisher HostEventPublisher, tenants TenantResolver) *Events {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Events{publisher: publisher, tenants: tenants}
}

// WithIdempotency enables request-level replay on Idempotency-Key. Nil leaves
// a request carrying the key answered 503 rather than silently unprotected,
// the same contract the run and datastore handlers hold.
func (handler *Events) WithIdempotency(service *idempotency.Service) *Events {
	handler.idempotency = service
	return handler
}

// WithRateLimiter bounds the events API per tenant. A nil limiter is ignored.
func (handler *Events) WithRateLimiter(limiter RateLimiter) *Events {
	if limiter != nil {
		handler.rateLimiter = limiter
	}
	return handler
}

// Register mounts the events operations.
func (handler *Events) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "publish-event",
		Method:        http.MethodPost,
		Path:          "/events",
		DefaultStatus: http.StatusAccepted,
		Summary:       "Publish a host event",
		Description: "Publishes one event and queues an execution for every active workflow whose event trigger " +
			"lists the event's type, running each workflow's active revision. This is the recommended host → workflow path: " +
			"one request reaches every listening workflow, where a webhook route is per workflow and POST /workflows/{id}/run " +
			"runs the latest revision and ignores activation. The response names what ran. An event nobody listens for is " +
			"accepted and matches nothing. Send Idempotency-Key to make a retry safe; without one, an eventId in the body is " +
			"the idempotency key, and deduplicates per workflow either way. " + idempotencyKeyDoc,
		Tags: []string{"Events"},
	}, handler.Publish)
}

// publishEventInput is one published event. eventId is optional: without it
// there is nothing to dedupe a retry against, which is a choice the host makes
// when every publish is a new event.
type publishEventInput struct {
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"255" pattern:"^[!-~]+$" doc:"1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means the body's eventId is used, when there is one."`
	Body           *struct {
		EventID    string          `json:"eventId,omitempty" maxLength:"128" doc:"The host's own identifier for this event. When Idempotency-Key is absent it is the idempotency key, and it deduplicates per workflow either way: publishing the same eventId again reaches each workflow once within a five-minute window."`
		Type       string          `json:"type" required:"true" minLength:"1" maxLength:"255" doc:"The event type, exactly as triggers subscribe to it."`
		Payload    json.RawMessage `json:"payload,omitempty" doc:"The event's body, carried through to each workflow as the trigger item's payload."`
		OccurredAt *time.Time      `json:"occurredAt,omitempty" doc:"When the event happened on the host's clock. Absent means the server stamps the publish time."`
	} `required:"true"`
}

// publishEventOutput carries what publishing did, and is the recorded form an
// idempotent replay answers with.
type publishEventOutput struct {
	Status   int    `status:"202"`
	Replayed string `header:"Idempotent-Replayed" doc:"true when this response replays an earlier request with the same Idempotency-Key; absent on a first response."`
	Body     PublishEventResult
}

// PublishEventResult is what publishing one event did: the workflows that
// queued an execution, and the ones that were considered and did not run.
type PublishEventResult struct {
	Matched  []PublishEventMatch  `json:"matched" doc:"One entry per workflow that queued an execution."`
	Filtered []PublishEventFilter `json:"filtered" doc:"Workflows that were considered and did not run, with the reason."`
}

// PublishEventMatch is one workflow the event reached.
type PublishEventMatch struct {
	WorkflowID  string `json:"workflowId" doc:"The workflow that queued an execution."`
	ExecutionID string `json:"executionId" doc:"The queued execution, followable on the event stream."`
}

// PublishEventFilter is one workflow the event considered but did not run.
type PublishEventFilter struct {
	WorkflowID string `json:"workflowId" doc:"The workflow that was considered."`
	// Reason is why it did not run: duplicate (this eventId already reached
	// this workflow inside the delivery window) or inactive (the workflow was
	// deactivated between the subscription listing and the queue).
	Reason      string `json:"reason" enum:"duplicate,inactive"`
	ExecutionID string `json:"executionId,omitempty" doc:"The execution a duplicate was collapsed with, when it is known."`
}

// publish is the side effect, and the recorded form of its outcome is the
// response body, so a replayed publish answers with what the first one did.
func (handler *Events) Publish(ctx context.Context, input *publishEventInput) (*publishEventOutput, error) {
	if handler.publisher == nil {
		return nil, unavailableProblem(ctx, "the host event surface is not available on this instance",
			errors.New("no host event publisher was wired at composition"))
	}
	// Spent before the fan-out reads a subscription or claims a delivery: a
	// refused publish queues nothing, and the host's retry — deduplicated per
	// workflow by the eventId — finishes what the first one started.
	if handler.rateLimiter != nil {
		allowed, retryAfter, err := handler.rateLimiter.Allow(ctx, handler.tenants.Resolve(ctx).ID, ratelimit.BucketEvents)
		if err != nil {
			return nil, serverProblem(ctx, "the events rate limiter could not be read", err)
		}
		if !allowed {
			return nil, rateLimitedProblem(retryAfter)
		}
	}
	body := input.Body
	event := hostevents.Event{
		ID:      body.EventID,
		Type:    body.Type,
		Payload: body.Payload,
	}
	if body.OccurredAt != nil {
		event.OccurredAt = *body.OccurredAt
	}

	// The header wins; an eventId stands in for it when no header was sent,
	// which is the idempotency the events API promises on the field alone.
	key := input.IdempotencyKey
	if key == "" {
		key = body.EventID
	}

	publish := func(ctx context.Context) (idempotency.Response, error) {
		result, err := handler.publisher.Publish(ctx, handler.tenants.Resolve(ctx), event)
		if err != nil {
			return idempotency.Response{}, err
		}
		return idempotency.Response{
			Status: http.StatusAccepted,
			Body:   publishEventResult(result),
		}, nil
	}

	if key == "" {
		response, err := publish(ctx)
		if err != nil {
			return nil, handler.problem(ctx, err)
		}
		return &publishEventOutput{Status: response.Status, Body: response.Body.(PublishEventResult)}, nil
	}
	if handler.idempotency == nil {
		return nil, idempotencyUnavailable()
	}
	result, err := handler.idempotency.Do(ctx, handler.tenants.Resolve(ctx).ID, key, idempotency.Request{
		Operation: "publish-event",
		Target:    body.Type,
		Body: struct {
			EventID    string          `json:"eventId"`
			Type       string          `json:"type"`
			Payload    json.RawMessage `json:"payload"`
			OccurredAt *time.Time      `json:"occurredAt"`
		}{EventID: body.EventID, Type: body.Type, Payload: body.Payload, OccurredAt: body.OccurredAt},
	}, publish)
	if err != nil {
		if problem, ok := idempotencyProblem(ctx, err); ok {
			return nil, problem
		}
		return nil, handler.problem(ctx, err)
	}
	var resource PublishEventResult
	if err := result.Outcome.Decode(&resource); err != nil {
		return nil, serverProblem(ctx, "the recorded publish outcome could not be read", err)
	}
	return &publishEventOutput{Status: result.Outcome.Status, Replayed: replayedHeader(result.Replayed), Body: resource}, nil
}

// publishEventResult converts the fan-out's answer into the API's own shapes,
// so the served document names these types rather than the service's.
func publishEventResult(result hostevents.Result) PublishEventResult {
	converted := PublishEventResult{
		Matched:  make([]PublishEventMatch, 0, len(result.Matched)),
		Filtered: make([]PublishEventFilter, 0, len(result.Filtered)),
	}
	for _, matched := range result.Matched {
		converted.Matched = append(converted.Matched, PublishEventMatch(matched))
	}
	for _, filtered := range result.Filtered {
		converted.Filtered = append(converted.Filtered, PublishEventFilter(filtered))
	}
	return converted
}

// problem maps the fan-out's failures onto what a caller sees. The store's
// own refusals are already the API's vocabulary; everything else is the
// server's, not the caller's.
func (handler *Events) problem(ctx context.Context, err error) error {
	return serverProblem(ctx, "publishing the event failed", err)
}
