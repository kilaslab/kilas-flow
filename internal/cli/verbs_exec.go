package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// execTraceTimeoutDefault is how long `exec trace` watches a live feed before
// answering with what it has. It is the global --timeout's own default, stated
// here so the verb's contract does not depend on a flag elsewhere.
const execTraceTimeoutDefault = 30 * time.Second

// execVerbs are the observe verbs: the history of what ran, and the live feed
// of what is running.
func execVerbs() []Verb {
	return []Verb{
		{
			Path:      "exec list",
			Operation: "list-executions",
			Summary:   "list executions, newest first (`--workflow`, `--status`, `--trigger`, `--limit`, `--cursor`)",
			Flags:     registerExecListFlags,
			Run:       runExecList,
			Human:     humanResourceList("id", "status", "workflowId", "startedAt", "durationMs"),
		},
		{
			Path:      "exec get",
			Operation: "get-execution",
			Summary:   "read one execution, with its node-run trace",
			Run:       runExecGet,
			Human:     humanExecution,
		},
		{
			Path:      "exec cancel",
			Operation: "cancel-execution",
			Summary:   "request cancellation of a queued or running execution",
			Run:       runExecCancel,
			Human:     humanExecution,
		},
		{
			Path:      "exec retry",
			Operation: "retry-execution",
			Summary:   "start a fresh execution of the same workflow, input and trigger as one that finished",
			Run:       runExecRetry,
			Human:     humanExecution,
		},
		{
			Path:      "exec trace",
			Operation: "stream-execution-events",
			Summary:   "collect an execution's events into one envelope, stopping at its outcome",
			Flags:     registerStreamFlags,
			Run:       runExecTrace,
			Human:     humanTrace,
		},
		{
			Path:      "exec tail",
			Operation: "stream-execution-events",
			Summary:   "stream an execution's events as they arrive (one object per line; the documented exception)",
			Flags:     registerStreamFlags,
			Run:       runExecTail,
		},
	}
}

// execListFlags are the filters the execution listing takes.
type execListFlags struct {
	workflow string
	statuses stringListFlag
	trigger  string
	limit    int
	cursor   string
}

// registerExecListFlags attaches the listing's filters.
func registerExecListFlags(fs *flag.FlagSet) any {
	flags := &execListFlags{}
	fs.StringVar(&flags.workflow, "workflow", "", "only executions of this workflow")
	fs.Var(&flags.statuses, "status", "only executions in this status (repeatable)")
	fs.StringVar(&flags.trigger, "trigger", "", "only executions started by this trigger")
	fs.IntVar(&flags.limit, "limit", 0, "maximum executions to return (server default when 0)")
	fs.StringVar(&flags.cursor, "cursor", "", "opaque cursor from a previous page")

	return flags
}

// query renders the listing's filters, omitting what the caller did not set.
func (f *execListFlags) query() url.Values {
	query := url.Values{}
	if strings.TrimSpace(f.workflow) != "" {
		query.Set("workflowId", f.workflow)
	}
	for _, status := range f.statuses {
		query.Add("status", status)
	}
	if strings.TrimSpace(f.trigger) != "" {
		query.Set("trigger", f.trigger)
	}
	if f.limit > 0 {
		query.Set("limit", strconv.Itoa(f.limit))
	}
	if strings.TrimSpace(f.cursor) != "" {
		query.Set("cursor", f.cursor)
	}

	return query
}

// streamFlags are what both streaming verbs take.
type streamFlags struct {
	from uint64
}

// registerStreamFlags attaches --from.
func registerStreamFlags(fs *flag.FlagSet) any {
	flags := &streamFlags{}
	fs.Uint64Var(&flags.from, "from", 0, "resume after this event id, from a previous trace's lastEventId")

	return flags
}

// stringListFlag collects a repeatable string flag in the order it was given.
type stringListFlag []string

// String renders the flag as it would be written on a command line.
func (f *stringListFlag) String() string { return strings.Join(*f, ",") }

// Set records one value.
func (f *stringListFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("a value is required")
	}
	*f = append(*f, value)

	return nil
}

// runExecList reads one page of execution history.
func runExecList(ctx *Context, args []string) error {
	if err := refusePositional(args, "exec list"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*execListFlags)
	if !ok {
		return usageError("the exec list verb was registered without its flags")
	}

	list, err := ctx.listPage("/executions", flags.query())
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runExecGet reads one execution.
func runExecGet(ctx *Context, args []string) error {
	id, err := requireOneID(args, "execution id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodGet, "/executions/"+url.PathEscape(id), nil, id)
}

// runExecCancel asks the server to stop a run.
//
// It is not a guarded verb: cancelling a run stops work rather than publishing
// or destroying anything, and the answer is 202 because the server has accepted
// the request, not because the run has stopped.
func runExecCancel(ctx *Context, args []string) error {
	id, err := requireOneID(args, "execution id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodPost, "/executions/"+url.PathEscape(id)+"/cancel", nil, id)
}

// runExecRetry starts a fresh execution from a finished one.
//
// It is not a guarded verb: a retry spends a run rather than publishing or
// destroying anything, and the server refuses one that is still running with
// 409 — which the exit contract maps to exit 5, "re-read, then decide".
func runExecRetry(ctx *Context, args []string) error {
	id, err := requireOneID(args, "execution id")
	if err != nil {
		return err
	}

	// No body: the operation reads the workflow, the input and the trigger from
	// the execution it copies, and anything sent here would be ignored.
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost,
		apiPath("/executions/"+url.PathEscape(id)+"/retry"), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The new execution's own id, so a --quiet caller pipes the run it just
	// started rather than the one it retried.
	ctx.Primary = locationID(resp.Header.Get("Location"))
	if ctx.Primary == "" {
		ctx.Primary = fieldValue(resp.Body, "id")
	}

	return nil
}

// tracePayload is one collected trace.
type tracePayload struct {
	ExecutionID string        `json:"executionId"`
	Events      []streamEvent `json:"events"`
	Terminal    bool          `json:"terminal"`
	LastEventID uint64        `json:"lastEventId"`
}

// runExecTrace collects an execution's feed into one envelope.
//
// The deadline is not a failure here: a run that is still going when the trace
// ends answers with terminal:false and the id to resume from, which is a fact
// about the run rather than an error in the call. `exec tail` is the verb that
// reports "still running" in its exit code, because it has no envelope to say
// it in.
func runExecTrace(ctx *Context, args []string) error {
	id, err := requireOneID(args, "execution id")
	if err != nil {
		return err
	}
	flags, ok := ctx.VerbFlags.(*streamFlags)
	if !ok {
		return usageError("the exec trace verb was registered without its flags")
	}

	deadline := execTraceTimeoutDefault
	if ctx.Flags.wasProvided("timeout") {
		deadline = ctx.Flags.Timeout
	}
	cancel := ctx.bound(deadline)
	defer cancel()

	payload := tracePayload{ExecutionID: id, Events: []streamEvent{}}
	terminal, err := ctx.stream("/executions/"+url.PathEscape(id)+"/events", nil, resumeHeader(flags.from),
		func(event streamEvent) bool {
			payload.Events = append(payload.Events, event)
			payload.LastEventID = event.ID

			return true
		})
	payload.Terminal = terminal

	if err != nil && !ctx.deadlineExceeded() {
		return err
	}

	ctx.Data = payload
	ctx.Primary = id

	return nil
}

// runExecTail streams an execution's feed as it arrives.
//
// This is one of the documented exceptions to the one-envelope rule: an
// envelope cannot be written until the run ends, and the point of tailing is to
// see events before that. Each event is one JSON object per line in JSON mode
// and one line of text otherwise.
func runExecTail(ctx *Context, args []string) error {
	id, err := requireOneID(args, "execution id")
	if err != nil {
		return err
	}
	flags, ok := ctx.VerbFlags.(*streamFlags)
	if !ok {
		return usageError("the exec tail verb was registered without its flags")
	}

	// No --timeout means no deadline: tailing is what a caller does when a run
	// is long, and cutting it off after the thirty seconds that bound every
	// other verb would make the verb useless for its one purpose. --timeout 0
	// says the same thing explicitly.
	deadline := time.Duration(0)
	if ctx.Flags.wasProvided("timeout") {
		deadline = ctx.Flags.Timeout
	}
	cancel := ctx.bound(deadline)
	defer cancel()

	writer := stdout(ctx.Env)
	ctx.Streamed = true

	jsonMode := resolveMode(ctx.Flags, ctx.Env.TTY) == modeJSON
	terminal, err := ctx.stream("/executions/"+url.PathEscape(id)+"/events", nil, resumeHeader(flags.from),
		func(event streamEvent) bool {
			if writeErr := writeStreamEvent(writer, event, jsonMode); writeErr != nil {
				return false
			}

			return true
		})

	switch {
	case err != nil && !ctx.deadlineExceeded():
		return err
	case terminal:
		return nil
	case ctx.deadlineExceeded():
		return &ExitError{
			Code:    ExitNotReady,
			ErrCode: "timeout",
			Message: "execution " + id + " had not finished within " + deadline.String() + "; tail it again to keep watching",
		}
	default:
		// The server closed the feed without an outcome: the run is not over,
		// so the caller should come back rather than believe it finished.
		return &ExitError{
			Code:    ExitNotReady,
			ErrCode: "stream_closed",
			Message: "the event feed for execution " + id + " closed before the run finished; tail it again to keep watching",
		}
	}
}

// resumeHeader asks the server to resume after an event id.
//
// The Last-Event-ID header is the standard way to resume an event stream and
// the one the API reads first, so a caller that has a previous trace's
// lastEventId does not lose the events in between.
func resumeHeader(from uint64) http.Header {
	header := http.Header{}
	if from > 0 {
		header.Set("Last-Event-ID", strconv.FormatUint(from, 10))
	}

	return header
}

// writeStreamEvent writes one frame of a tailed stream.
func writeStreamEvent(w io.Writer, event streamEvent, jsonMode bool) error {
	var line string
	if jsonMode {
		encoded, err := json.Marshal(event)
		if err != nil {
			return outputWriteError("could not render an event: %v", err)
		}
		line = string(encoded)
	} else {
		line = humanEventLine(event)
	}

	if _, err := fmt.Fprintln(w, line); err != nil {
		return outputWriteError("could not write an event: %v", err)
	}

	return nil
}

// humanEventLine renders one event for a terminal: its name, the node it
// belongs to when it has one, and the status it reports.
func humanEventLine(event streamEvent) string {
	var payload struct {
		NodeID string `json:"nodeId"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(event.Data, &payload)

	line := event.Name
	if payload.NodeID != "" {
		line += " node=" + payload.NodeID
	}
	if payload.Status != "" {
		line += " status=" + payload.Status
	}

	return line
}

// humanExecution prints an execution as the lines a person needs to decide what
// to do next. It renders both shapes the run and exec verbs return: the request
// the server accepted, and the record of a run that finished.
func humanExecution(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var record struct {
		ID                string `json:"id"`
		WorkflowID        string `json:"workflowId"`
		Status            string `json:"status"`
		Trigger           string `json:"trigger"`
		StartedAt         string `json:"startedAt"`
		CreatedAt         string `json:"createdAt"`
		FinishedAt        string `json:"finishedAt"`
		DurationMs        *int64 `json:"durationMs"`
		ParentExecutionID string `json:"parentExecutionId"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		printJSONValue(w, raw)

		return
	}

	duration := "-"
	if record.DurationMs != nil {
		duration = strconv.FormatInt(*record.DurationMs, 10) + "ms"
	}

	started := record.StartedAt
	if started == "" {
		// A queued request has not started yet; when it was accepted is the
		// closest thing to a time that exists.
		started = record.CreatedAt
	}

	printKV(w, [][2]string{
		{"id", record.ID},
		{"workflow", record.WorkflowID},
		{"status", record.Status},
		{"trigger", record.Trigger},
		{"started", started},
		{"finished", record.FinishedAt},
		{"duration", duration},
	})
}

// humanTrace prints a collected trace as one line per event.
func humanTrace(w io.Writer, data any) {
	payload, ok := data.(tracePayload)
	if !ok {
		printJSONValue(w, data)

		return
	}

	for _, event := range payload.Events {
		fmt.Fprintln(w, humanEventLine(event))
	}
	if !payload.Terminal {
		fmt.Fprintf(w, "the run had not finished; resume with --from %d\n", payload.LastEventID)
	}
}
