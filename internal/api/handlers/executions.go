package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// ExecutionController is the narrow runtime service surface exposed to HTTP.
// The concrete worker remains independent from routing and its storage layer.
type ExecutionController interface {
	ExecutionWaker
	Get(context.Context, repository.TenantScope, string) (execution.Record, error)
	Cancel(context.Context, repository.TenantScope, string) (execution.Record, error)
}

// ExecutionEvent is one standardized event as it appears on the live feed.
type ExecutionEvent struct {
	ID          uint64          `json:"id" doc:"Monotonic per execution; send back as Last-Event-ID to resume"`
	Type        string          `json:"type"`
	ExecutionID string          `json:"executionId"`
	WorkflowID  string          `json:"workflowId,omitempty"`
	NodeID      string          `json:"nodeId,omitempty"`
	Status      string          `json:"status,omitempty"`
	Sequence    int             `json:"sequence,omitempty"`
	At          time.Time       `json:"at"`
	Data        json.RawMessage `json:"data,omitempty" doc:"Redacted, type-specific detail"`
}

// One Go type per event name.
//
// huma's SSE registration keys the `event:` field by the data's Go type, so
// distinct names need distinct types even though every event shares a shape.
// The payoff is an OpenAPI document that names each event explicitly instead
// of collapsing them into one opaque message.
type (
	ExecutionStartedEvent   ExecutionEvent
	ExecutionCompletedEvent ExecutionEvent
	ExecutionFailedEvent    ExecutionEvent
	ExecutionCancelledEvent ExecutionEvent
	NodeStartedEvent        ExecutionEvent
	NodeOutputEvent         ExecutionEvent
	NodeCompletedEvent      ExecutionEvent
	NodeFailedEvent         ExecutionEvent
	WorkflowSavedEvent      ExecutionEvent
)

// typedEvent converts one event into the Go type bound to its name.
func typedEvent(event ExecutionEvent, eventType events.Type) any {
	switch eventType {
	case events.ExecutionStarted:
		return ExecutionStartedEvent(event)
	case events.ExecutionCompleted:
		return ExecutionCompletedEvent(event)
	case events.ExecutionFailed:
		return ExecutionFailedEvent(event)
	case events.ExecutionCancelled:
		return ExecutionCancelledEvent(event)
	case events.NodeStarted:
		return NodeStartedEvent(event)
	case events.NodeOutput:
		return NodeOutputEvent(event)
	case events.NodeCompleted:
		return NodeCompletedEvent(event)
	case events.NodeFailed:
		return NodeFailedEvent(event)
	case events.WorkflowSaved:
		return WorkflowSavedEvent(event)
	default:
		return event
	}
}

type executionEventsInput struct {
	ID string `path:"id" minLength:"1" doc:"Execution identifier"`
	// LastEventID follows the SSE standard: a browser resends it automatically
	// on reconnect, so a dropped connection resumes instead of restarting.
	LastEventID string `header:"Last-Event-ID" doc:"Resume after this event ID"`
	From        uint64 `query:"from" doc:"Resume after this event ID when a header cannot be set"`
}

// Executions provides user-requested lifecycle controls for durable runs and
// the read-only execution history.
//
// Reads of persisted history go through the repository; only get and cancel
// need the live worker, because they observe or interrupt in-flight work.
type Executions struct {
	controller ExecutionController
	history    repository.ExecutionRepository
	events     *events.Broker
	tenants    TenantResolver
}

type executionPathInput struct {
	ID string `path:"id" minLength:"1" doc:"Execution identifier"`
}

type listExecutionsInput struct {
	WorkflowID string   `query:"workflowId" doc:"Only list executions of this workflow"`
	Status     []string `query:"status" doc:"Only list executions in these statuses"`
	Trigger    string   `query:"trigger" doc:"Only list executions started by this trigger"`
	Limit      int      `query:"limit" minimum:"1" maximum:"100" doc:"Maximum executions to return (default 25)"`
	Cursor     string   `query:"cursor" doc:"Opaque cursor from a previous listing's nextCursor"`
}

type executionListOutput struct {
	Body ExecutionListResource
}

// ExecutionSummary is the compact history row. It deliberately omits input,
// output, error, and the node-run trace: a list must not ship payloads the
// user has not asked to inspect.
type ExecutionSummary struct {
	ID                string            `json:"id"`
	WorkflowID        string            `json:"workflowId"`
	WorkflowVersionID string            `json:"workflowVersionId"`
	Status            execution.Status  `json:"status"`
	Trigger           execution.Trigger `json:"trigger"`
	StartedAt         time.Time         `json:"startedAt"`
	FinishedAt        *time.Time        `json:"finishedAt,omitempty"`
	DurationMs        *int64            `json:"durationMs,omitempty" doc:"Wall-clock duration in milliseconds, once the execution has finished"`
}

// ExecutionListResource is one page of execution history, newest first.
type ExecutionListResource struct {
	Items      []ExecutionSummary `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty" doc:"Pass back as ?cursor= to read the next page"`
}

// NewExecutions constructs the execution control and history handler.
func NewExecutions(controller ExecutionController, history repository.ExecutionRepository, broker *events.Broker, tenants TenantResolver) *Executions {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Executions{controller: controller, history: history, events: broker, tenants: tenants}
}

// Register wires execution history reads and the controls that need the live
// runtime service.
func (handler *Executions) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-executions", Method: http.MethodGet, Path: "/executions",
		Summary: "List workflow executions", Description: "Returns one page of execution history, newest first.", Tags: []string{"Executions"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "get-execution", Method: http.MethodGet, Path: "/executions/{id}",
		Summary: "Get a workflow execution", Description: "Returns durable execution state and its ordered node-run trace.", Tags: []string{"Executions"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "cancel-execution", Method: http.MethodPost, Path: "/executions/{id}/cancel", DefaultStatus: http.StatusAccepted,
		Summary: "Cancel a workflow execution", Description: "Requests cancellation of queued or running work.", Tags: []string{"Executions"},
	}, handler.Cancel)

	sse.Register(api, huma.Operation{
		OperationID: "stream-execution-events", Method: http.MethodGet, Path: "/executions/{id}/events",
		Summary: "Stream execution events",
		Description: "Live standardized event feed for one execution. Replays retained events after Last-Event-ID, " +
			"then streams until the execution reaches a terminal state.",
		Tags: []string{"Executions"},
	}, map[string]any{
		string(events.ExecutionStarted):   ExecutionStartedEvent{},
		string(events.ExecutionCompleted): ExecutionCompletedEvent{},
		string(events.ExecutionFailed):    ExecutionFailedEvent{},
		string(events.ExecutionCancelled): ExecutionCancelledEvent{},
		string(events.NodeStarted):        NodeStartedEvent{},
		string(events.NodeOutput):         NodeOutputEvent{},
		string(events.NodeCompleted):      NodeCompletedEvent{},
		string(events.NodeFailed):         NodeFailedEvent{},
		string(events.WorkflowSaved):      WorkflowSavedEvent{},
	}, handler.StreamEvents)
}

// StreamEvents serves the live feed for one execution.
//
// The subscription is opened before the durable record is read, so an event
// published between the two is queued rather than missed.
func (handler *Executions) StreamEvents(ctx context.Context, input *executionEventsInput, send sse.Sender) {
	if handler.events == nil {
		_ = send.Comment("execution events are unavailable")
		return
	}
	tenant := handler.tenants.Resolve(ctx)
	// An embed session may only watch its own workflow's runs. The execution is
	// loaded first so its owner is known before any event is streamed.
	if session, embedded := middleware.EmbedSessionFrom(ctx); embedded {
		if handler.controller == nil {
			_ = send.Comment("execution events are unavailable")
			return
		}
		record, err := handler.controller.Get(ctx, tenant, input.ID)
		if err != nil || record.WorkflowID != session.WorkflowID {
			_ = send.Comment("execution not found")
			return
		}
	}
	subscription := handler.events.Subscribe(tenant.ID, input.ID, resumeFrom(input))
	defer subscription.Close()

	// A comment immediately after connect flushes headers, so a client knows
	// the stream is open even before the first event.
	_ = send(sse.Message{Retry: 2000, Comment: "connected"})

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// Proxies drop an idle connection; a comment keeps it open without
			// being visible to the client as an event.
			if err := send.Comment("heartbeat"); err != nil {
				return
			}
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			if err := send(sse.Message{ID: int(event.ID), Data: typedEvent(executionEventResource(event), event.Type)}); err != nil {
				return
			}
			if event.Type.Terminal() {
				// Closing on a terminal event is what stops a browser from
				// reconnecting forever to a run that already finished.
				return
			}
		}
	}
}

// resumeFrom prefers the standard SSE header and falls back to a query
// parameter for clients that cannot set request headers.
func resumeFrom(input *executionEventsInput) uint64 {
	if input.LastEventID != "" {
		if parsed, err := strconv.ParseUint(strings.TrimSpace(input.LastEventID), 10, 64); err == nil {
			return parsed
		}
	}
	return input.From
}

func executionEventResource(event events.Event) ExecutionEvent {
	return ExecutionEvent{
		ID: event.ID, Type: string(event.Type), ExecutionID: event.ExecutionID,
		WorkflowID: event.WorkflowID, NodeID: event.NodeID, Status: string(event.Status),
		Sequence: event.Sequence, At: event.At, Data: event.Data,
	}
}

// List returns one page of tenant-scoped execution history.
func (handler *Executions) List(ctx context.Context, input *listExecutionsInput) (*executionListOutput, error) {
	if handler.history == nil {
		return nil, huma.Error503ServiceUnavailable("execution history unavailable")
	}
	filter := repository.ExecutionFilter{WorkflowID: input.WorkflowID, Limit: input.Limit, Cursor: input.Cursor}
	for _, status := range input.Status {
		parsed, ok := parseExecutionStatus(status)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("unsupported execution status " + status)
		}
		filter.Statuses = append(filter.Statuses, parsed)
	}
	if input.Trigger != "" {
		trigger, ok := parseExecutionTrigger(input.Trigger)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("unsupported execution trigger " + input.Trigger)
		}
		filter.Trigger = trigger
	}

	page, err := handler.history.List(ctx, handler.tenants.Resolve(ctx), filter)
	// A cursor the client did not receive from this API is a bad request, not a
	// server fault, so it must not be reported as a 500.
	if errors.Is(err, repository.ErrInvalidCursor) {
		return nil, huma.Error400BadRequest("execution cursor is invalid")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("execution listing failed")
	}

	resource := ExecutionListResource{Items: make([]ExecutionSummary, 0, len(page.Records)), NextCursor: page.NextCursor}
	for _, record := range page.Records {
		resource.Items = append(resource.Items, executionSummaryResource(record))
	}
	return &executionListOutput{Body: resource}, nil
}

func parseExecutionStatus(value string) (execution.Status, bool) {
	switch status := execution.Status(value); status {
	case execution.StatusQueued, execution.StatusRunning, execution.StatusCancelling,
		execution.StatusSucceeded, execution.StatusFailed, execution.StatusCancelled:
		return status, true
	default:
		return "", false
	}
}

func parseExecutionTrigger(value string) (execution.Trigger, bool) {
	switch trigger := execution.Trigger(value); trigger {
	case execution.TriggerManual, execution.TriggerWebhook, execution.TriggerSchedule:
		return trigger, true
	default:
		return "", false
	}
}

// ownsExecution confines an embed session to its own workflow's executions.
//
// Which workflow an execution belongs to is only knowable by loading it, so
// this check cannot live in the routing middleware — it has to happen here,
// against the record. Without it, a session scoped to one workflow could read
// every execution in the tenant by guessing or enumerating IDs.
//
// A request with no embed session is the internal dashboard and is unaffected.
func ownsExecution(ctx context.Context, workflowID string) error {
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		return nil
	}
	if session.WorkflowID != workflowID {
		// 404 rather than 403: an embed session has no business learning that
		// another workflow's execution exists.
		return huma.Error404NotFound("execution not found")
	}
	return nil
}

// Get returns a tenant-scoped execution record, including its persisted trace.
func (handler *Executions) Get(ctx context.Context, input *executionPathInput) (*executionOutput, error) {
	if handler.controller == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	record, err := handler.controller.Get(ctx, handler.tenants.Resolve(ctx), input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("execution lookup failed")
	}
	if err := ownsExecution(ctx, record.WorkflowID); err != nil {
		return nil, err
	}
	return &executionOutput{Body: executionResource(record)}, nil
}

// Cancel persists a cancellation request and interrupts a local worker when
// that worker owns the execution.
func (handler *Executions) Cancel(ctx context.Context, input *executionPathInput) (*executionRequestOutput, error) {
	if handler.controller == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	record, err := handler.controller.Cancel(ctx, handler.tenants.Resolve(ctx), input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("execution cancellation failed")
	}
	if err := ownsExecution(ctx, record.WorkflowID); err != nil {
		return nil, err
	}
	return &executionRequestOutput{Status: http.StatusAccepted, Body: executionRequestResource(record)}, nil
}
