package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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
	ExecutionWaitingEvent   ExecutionEvent
	NodeStartedEvent        ExecutionEvent
	NodeOutputEvent         ExecutionEvent
	NodeCompletedEvent      ExecutionEvent
	NodeFailedEvent         ExecutionEvent
	WorkflowSavedEvent      ExecutionEvent
	WebhookResponseEvent    ExecutionEvent
	CodeConsoleEvent        ExecutionEvent
	AIModelStartedEvent     ExecutionEvent
	AIModelDeltaEvent       ExecutionEvent
	AIModelCompletedEvent   ExecutionEvent
	AIToolStartedEvent      ExecutionEvent
	AIToolCompletedEvent    ExecutionEvent
	AIToolFailedEvent       ExecutionEvent
	AIAgentCompletedEvent   ExecutionEvent
	AIAgentFailedEvent      ExecutionEvent
	OtherEvent              ExecutionEvent
)

// OtherEventName is the frame a name with no registration of its own goes out
// under. The event's own name stays in the payload's `type`, so a client that
// listens for it can still tell events apart, and nothing is sent unnamed.
const OtherEventName = "execution.event"

// typedEvent converts one event into the Go type bound to its name.
//
// Every events.Type needs a case here. huma looks the SSE `event:` name up by
// the data's Go type, so a type with no case is sent as an unnamed `message`
// frame — which every client ignores — and huma prints an "unknown event type"
// stack trace to stderr for each one. That is exactly how execution.failed went
// missing for a while: the frame reached the browser with no name, the editor
// never learned the run had failed, and the server log filled with traces.
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
	case engine.EventExecutionWaiting:
		return ExecutionWaitingEvent(event)
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
	case events.Type(engine.ResponseEventName):
		return WebhookResponseEvent(event)
	case events.Type(engine.ConsoleEventName):
		return CodeConsoleEvent(event)
	case events.Type(ai.EventModelStarted):
		return AIModelStartedEvent(event)
	case events.Type(ai.EventModelDelta):
		return AIModelDeltaEvent(event)
	case events.Type(ai.EventModelCompleted):
		return AIModelCompletedEvent(event)
	case events.Type(ai.EventToolStarted):
		return AIToolStartedEvent(event)
	case events.Type(ai.EventToolCompleted):
		return AIToolCompletedEvent(event)
	case events.Type(ai.EventToolFailed):
		return AIToolFailedEvent(event)
	case events.Type(ai.EventAgentCompleted):
		return AIAgentCompletedEvent(event)
	case events.Type(ai.EventAgentFailed):
		return AIAgentFailedEvent(event)
	default:
		return OtherEvent(event)
	}
}

// executionEventSchemas is the SSE registration's name-to-type map.
//
// It is one function rather than a literal inside Register so a test can read
// it: the defect this guards against is a name added to events.Type with no
// entry here, which no compiler and no other test would notice.
func executionEventSchemas() map[string]any {
	return map[string]any{
		string(events.ExecutionStarted):      ExecutionStartedEvent{},
		string(events.ExecutionCompleted):    ExecutionCompletedEvent{},
		string(events.ExecutionFailed):       ExecutionFailedEvent{},
		string(events.ExecutionCancelled):    ExecutionCancelledEvent{},
		string(engine.EventExecutionWaiting): ExecutionWaitingEvent{},
		string(events.NodeStarted):           NodeStartedEvent{},
		string(events.NodeOutput):            NodeOutputEvent{},
		string(events.NodeCompleted):         NodeCompletedEvent{},
		string(events.NodeFailed):            NodeFailedEvent{},
		string(events.WorkflowSaved):         WorkflowSavedEvent{},
		engine.ResponseEventName:             WebhookResponseEvent{},
		engine.ConsoleEventName:              CodeConsoleEvent{},
		string(ai.EventModelStarted):         AIModelStartedEvent{},
		string(ai.EventModelDelta):           AIModelDeltaEvent{},
		string(ai.EventModelCompleted):       AIModelCompletedEvent{},
		string(ai.EventToolStarted):          AIToolStartedEvent{},
		string(ai.EventToolCompleted):        AIToolCompletedEvent{},
		string(ai.EventToolFailed):           AIToolFailedEvent{},
		string(ai.EventAgentCompleted):       AIAgentCompletedEvent{},
		string(ai.EventAgentFailed):          AIAgentFailedEvent{},
		OtherEventName:                       OtherEvent{},
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
	// catalog is the node catalogue a retry compiles its revision against,
	// narrowed to the caller's tenant. Required for a retry, because a retry
	// runs a stored graph and only the catalogue answers whether this tenant
	// may still run it.
	catalog workflow.Catalog
	// workflows reads the revision an execution ran, so an evaluation can name
	// nodes the way the workflow's own expressions do. Optional: without it an
	// evaluation addresses nodes by the ids the trace shows.
	workflows WorkflowVersionReader
	// api is the surface this handler registered on, kept so an operation
	// middleware can write the same RFC 9457 problem body every other
	// endpoint does. huma hands the API to Register and to nothing else, and a
	// hand-built body would be the one refusal in the API that a generated
	// client cannot parse.
	api huma.API
	// streams bounds how many event streams one tenant may hold open at once.
	// A stream is a held connection with a goroutine behind it, so without a
	// bound a single caller can pin an unbounded number of them by opening
	// streams for ids it knows exist.
	streams sync.Map
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
	TriggerNodeID     string            `json:"triggerNodeId,omitempty" doc:"The trigger node this run started from, when one workflow declares several"`
	ParentExecutionID string            `json:"parentExecutionId,omitempty" doc:"The execution that called this one, for a sub-workflow run"`
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
//
// The catalogue is passed for the one operation that queues work — a retry —
// because queueing a stored graph means compiling it under the caller's
// tenant's node visibility, the same question `run` answers.
func NewExecutions(controller ExecutionController, history repository.ExecutionRepository, broker *events.Broker, tenants TenantResolver, catalog workflow.Catalog) *Executions {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Executions{controller: controller, history: history, events: broker, tenants: tenants, catalog: catalog}
}

// Register wires execution history reads and the controls that need the live
// runtime service.
func (handler *Executions) Register(api huma.API) {
	handler.api = api
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
	huma.Register(api, huma.Operation{
		OperationID: "retry-execution", Method: http.MethodPost, Path: "/executions/{id}/retry", DefaultStatus: http.StatusCreated,
		Summary: "Retry a finished execution",
		Description: "Starts a new execution from a finished one's workflow, revision and input — the revision that ran, " +
			"not the workflow's newest — under the trigger the original ran under, so a retried webhook run is a webhook " +
			"run again. An execution that is still queued or running is refused with 409, and so is one " +
			"waiting on an approval: retrying it would run the same input beside itself.",
		Tags: []string{"Executions"},
	}, handler.Retry)
	huma.Register(api, huma.Operation{
		OperationID: "eval-expression", Method: http.MethodPost, Path: "/executions/{id}/eval",
		Summary: "Evaluate an expression against an execution",
		Description: "Evaluates one expression against the node outputs an execution's trace already stores, under the " +
			"budget a node of that revision is given, and answers the value and its JSON shape. Nothing is written " +
			"and nothing runs: this reads what a field held while the workflow ran. The grammar is the one every " +
			"workflow document is already evaluated with, and $env is the runtime's allowlist rather than the " +
			"process environment.",
		Tags: []string{"Executions"},
	}, handler.EvalExpression)

	operation := huma.Operation{
		OperationID: "stream-execution-events", Method: http.MethodGet, Path: "/executions/{id}/events",
		Summary: "Stream execution events",
		Description: "Live standardized event feed for one execution. Replays retained events after Last-Event-ID, " +
			"then streams until the execution reaches a terminal state. A run that has already finished answers " +
			"404 when its record is gone, and otherwise replays its outcome and closes.",
		Tags: []string{"Executions"},
		// The record is read before the stream opens, so an unknown or
		// another workflow's execution answers a real 404 rather than a 200
		// stream that never says anything. It has to be an operation
		// middleware: huma commits 200 for an SSE response before the handler
		// runs, so the handler itself can no longer choose a status.
		Middlewares: huma.Middlewares{handler.gateStreamRecord},
	}
	sse.Register(api, operation, executionEventSchemas(), handler.StreamEvents)
}

// streamRecordKey carries the record the gate read into the stream handler.
type streamRecordKey struct{}

// gateStreamRecord refuses a stream for an execution the caller cannot see.
//
// It runs before the SSE machinery commits a response, so a caller learns the
// truth with a status code instead of holding an open connection that emits
// nothing. Two cases are refused the same way: an execution that does not exist
// in this tenant, and one that belongs to a workflow an embed session was not
// granted. Both are 404, because "does not exist" is all an embed session has
// any business learning.
//
// A deployment with no durable history store keeps the previous behaviour and
// opens the stream anyway: the alternative is refusing every stream on an
// installation whose broker is wired but whose repository is not.
func (handler *Executions) gateStreamRecord(ctx huma.Context, next func(huma.Context)) {
	record, found := handler.streamRecord(ctx.Context(), ctx.Param("id"))
	if !found {
		// A store that cannot answer is not the same as a missing execution,
		// and only the second one is a 404.
		if handler.history == nil && handler.controller == nil {
			next(ctx)
			return
		}
		huma.WriteErr(handler.api, ctx, http.StatusNotFound, "execution not found")
		return
	}
	if err := handler.ownsExecution(ctx.Context(), record); err != nil {
		huma.WriteErr(handler.api, ctx, http.StatusNotFound, "execution not found")
		return
	}
	next(huma.WithValue(ctx, streamRecordKey{}, record))
}

// streamRecord reads the durable execution a stream would describe.
func (handler *Executions) streamRecord(ctx context.Context, executionID string) (execution.Record, bool) {
	if executionID == "" {
		return execution.Record{}, false
	}
	tenant := handler.tenants.Resolve(ctx)
	if handler.history != nil {
		record, err := handler.history.Get(ctx, tenant, executionID)
		if err == nil {
			return record, true
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return execution.Record{}, false
		}
	}
	if handler.controller != nil {
		record, err := handler.controller.Get(ctx, tenant, executionID)
		if err == nil {
			return record, true
		}
	}
	return execution.Record{}, false
}

// streamRecordFrom returns the record the gate read, if it ran.
func streamRecordFrom(ctx context.Context) (execution.Record, bool) {
	record, found := ctx.Value(streamRecordKey{}).(execution.Record)
	return record, found
}

// terminalEventType maps a finished execution's status to the event its stream
// ends on. It reports false for a run that has not finished.
func terminalEventType(status execution.Status) (events.Type, bool) {
	switch status {
	case execution.StatusSucceeded:
		return events.ExecutionCompleted, true
	case execution.StatusFailed:
		return events.ExecutionFailed, true
	case execution.StatusCancelled:
		return events.ExecutionCancelled, true
	default:
		return "", false
	}
}

// syntheticTerminal builds the event a stream ends on when the live feed has
// nothing left to replay.
//
// A finished execution whose events are gone — the process restarted, or the
// broker's bounded history aged out — would otherwise leave a subscriber
// waiting for a terminal frame that can never arrive. The durable record is the
// authority, so the frame is reconstructed from it rather than invented: the
// same status the record already reports over the REST API.
func syntheticTerminal(record execution.Record) (ExecutionEvent, bool) {
	eventType, terminal := terminalEventType(record.Status)
	if !terminal {
		return ExecutionEvent{}, false
	}
	at := record.StartedAt
	if record.FinishedAt != nil {
		at = *record.FinishedAt
	}
	detail := json.RawMessage(nil)
	if eventType == events.ExecutionFailed && len(record.Error) > 0 {
		// Redacted by the broker on publish; the same treatment here, because
		// a failure detail is the one place a credential-shaped value could
		// reach a browser.
		detail = execution.Redact(record.Error)
	}
	return ExecutionEvent{
		Type: string(eventType), ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Status: string(record.Status), At: at, Data: detail,
	}, true
}

// StreamEvents serves the live feed for one execution.
//
// The subscription is opened before the terminal decision is made, so an event
// published in between is queued rather than missed — and everything the queue
// holds is already there when Subscribe returns, because the broker replays a
// stream's retained history synchronously.
//
// The record the gate read is what keeps a finished execution's stream from
// waiting forever. Without it a stream opened for a run whose events are gone —
// finished before a restart, or aged out of the broker's bounded history —
// emitted heartbeats until the client gave up, which is how a browser tab
// pinned a server connection per abandoned view.
func (handler *Executions) StreamEvents(ctx context.Context, input *executionEventsInput, send sse.Sender) {
	if handler.events == nil {
		_ = send.Comment("execution events are unavailable")
		return
	}
	record, gated := streamRecordFrom(ctx)
	if gated {
		if ok, done := handler.claimStream(ctx, record.TenantID); !ok {
			_ = send.Comment("too many executions are being watched at once")
			return
		} else {
			defer done()
		}
	}
	tenant := handler.tenants.Resolve(ctx)

	subscription := handler.events.Subscribe(tenant.ID, input.ID, resumeFrom(input))
	defer subscription.Close()

	// A comment immediately after connect flushes headers, so a client knows
	// the stream is open even before the first event.
	_ = send(sse.Message{Retry: 2000, Comment: "connected"})

	// Everything the broker retained is already in the queue, so the replay is
	// delivered before the live loop starts.
	queued, open := drainQueued(subscription)
	for _, event := range queued {
		if err := send(sse.Message{ID: int(event.ID), Data: typedEvent(executionEventResource(event), event.Type)}); err != nil {
			return
		}
		if event.Type.Terminal() {
			// Closing on a terminal event is what stops a browser from
			// reconnecting forever to a run that already finished.
			return
		}
	}
	if gated {
		// The replay held no terminal frame — the loop above returns the moment
		// it sends one, so reaching here means none was sent. For a finished run
		// that frame can never arrive from the feed: the durable record says the
		// execution is over, and a run that is over publishes nothing further.
		// The record therefore supplies it, reconstructed from the same status
		// the REST API reports.
		//
		// Gating this on an empty replay was the bug: a replay that kept earlier
		// frames but lost the terminal one — a dropped publication, a released
		// history — left the stream emitting heartbeats forever, which is the
		// held connection this endpoint exists not to hold.
		if terminal, ok := syntheticTerminal(record); ok {
			sendSyntheticTerminal(send, input, terminal)
			return
		}
	}
	if !open {
		return
	}

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
		case event, stillOpen := <-subscription.Events():
			if !stillOpen {
				// The broker dropped this stream — its history was released,
				// or the process is shutting down. A client watching a
				// finished run still needs its outcome, so it gets the same
				// frame a run with no events left would have produced.
				if gated {
					if terminal, ok := syntheticTerminal(record); ok {
						sendSyntheticTerminal(send, input, terminal)
					}
				}
				return
			}
			if err := send(sse.Message{ID: int(event.ID), Data: typedEvent(executionEventResource(event), event.Type)}); err != nil {
				return
			}
			if event.Type.Terminal() {
				return
			}
		}
	}
}

// drainQueued collects everything the broker replayed into a fresh
// subscription.
//
// Subscribe delivers the retained history synchronously, so a non-blocking read
// yields the replay and never a live event that has not happened yet. It
// reports whether the subscription is still open: a closed channel means the
// stream is over, not that the queue is empty.
func drainQueued(subscription *events.Subscription) ([]events.Event, bool) {
	queued := []events.Event{}
	for {
		select {
		case event, open := <-subscription.Events():
			if !open {
				return queued, false
			}
			queued = append(queued, event)
		default:
			return queued, true
		}
	}
}

// sendSyntheticTerminal delivers the reconstructed terminal frame.
func sendSyntheticTerminal(send sse.Sender, input *executionEventsInput, terminal ExecutionEvent) {
	_ = send(sse.Message{ID: int(resumeFrom(input) + 1), Data: typedEvent(terminal, events.Type(terminal.Type))})
}

// maxStreamsPerTenant bounds how many event streams one tenant holds open.
//
// A stream is a held connection, a goroutine, and a broker subscription, and
// nothing else bounds them: the endpoint is reachable by any credential the
// tenant holds, so one client looping over execution ids could pin as many as
// it likes and starve everyone sharing the process.
const maxStreamsPerTenant = 32

// claimStream admits one stream per tenant up to the cap.
//
// The counter is never removed from the map. Deleting it on the way to zero
// races with a concurrent claim that already holds the pointer, and that claim
// would then increment a detached counter — resetting the cap for the rest of
// the process. One int32 per tenant the process has served is bounded by the
// number of tenants, which is the smaller problem.
func (handler *Executions) claimStream(ctx context.Context, tenantID string) (bool, func()) {
	key := tenantID
	if key == "" {
		key = handler.tenants.Resolve(ctx).ID
	}
	current, _ := handler.streams.LoadOrStore(key, new(int32))
	count, _ := current.(*int32)
	if count == nil {
		return true, func() {}
	}
	if atomic.AddInt32(count, 1) <= maxStreamsPerTenant {
		return true, func() { atomic.AddInt32(count, -1) }
	}
	atomic.AddInt32(count, -1)
	return false, func() {}
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
		return nil, serverProblem(ctx, "execution listing failed", err)
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
		execution.StatusWaiting,
		execution.StatusSucceeded, execution.StatusFailed, execution.StatusCancelled:
		return status, true
	default:
		return "", false
	}
}

func parseExecutionTrigger(value string) (execution.Trigger, bool) {
	switch trigger := execution.Trigger(value); trigger {
	case execution.TriggerManual, execution.TriggerWebhook, execution.TriggerSchedule, execution.TriggerPoll, execution.TriggerSubworkflow:
		return trigger, true
	default:
		return "", false
	}
}

// ownsExecution confines an embed session to its own workflow's executions,
// and to the sub-workflow runs that workflow started.
//
// Which workflow an execution belongs to is only knowable by loading it, so
// this check cannot live in the routing middleware — it has to happen here,
// against the record. Without it, a session scoped to one workflow could read
// every execution in the tenant by guessing or enumerating IDs.
//
// A request with no embed session is the internal dashboard and is unaffected.
//
// A sub-workflow execution belongs to a *different* workflow, so the plain
// comparison would hide it — leaving an embedded editor showing a parent that
// succeeded with an invisible child, and no way to see why it failed. A child
// whose ancestry reaches this session's own workflow is therefore readable:
// the session started that chain, and a chain a user can start but not inspect
// is worse than one they can read.
func (handler *Executions) ownsExecution(ctx context.Context, record execution.Record) error {
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		return nil
	}
	if session.WorkflowID == record.WorkflowID {
		return nil
	}
	// Only a sub-workflow run has anywhere else to look.
	if record.ParentExecutionID != "" && handler.history != nil {
		ancestors, err := handler.history.Ancestry(ctx, handler.tenants.Resolve(ctx), record.ID)
		if err == nil {
			for _, ancestor := range ancestors {
				if ancestor.WorkflowID == session.WorkflowID {
					return nil
				}
			}
		}
	}
	// 404 rather than 403: an embed session has no business learning that
	// another workflow's execution exists.
	return huma.Error404NotFound("execution not found")
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
		return nil, serverProblem(ctx, "execution lookup failed", err)
	}
	if err := handler.ownsExecution(ctx, record); err != nil {
		return nil, err
	}
	resource := executionResource(record)
	// A waiting execution answers with the links that resume it. The lookup
	// is optional so existing controller fakes keep compiling: without it
	// the record simply carries no links.
	if record.Status == execution.StatusWaiting {
		if linker, ok := handler.controller.(interface {
			WaitingLinks(context.Context, repository.TenantScope, string) (string, string, bool)
		}); ok {
			if resumeURL, approvalURL, found := linker.WaitingLinks(ctx, handler.tenants.Resolve(ctx), record.ID); found {
				resource.ResumeURL = resumeURL
				resource.ApprovalURL = approvalURL
			}
		}
	}
	return &executionOutput{Body: resource}, nil
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
		return nil, serverProblem(ctx, "execution cancellation failed", err)
	}
	if err := handler.ownsExecution(ctx, record); err != nil {
		return nil, err
	}
	return &executionRequestOutput{Status: http.StatusAccepted, Body: executionRequestResource(record)}, nil
}
