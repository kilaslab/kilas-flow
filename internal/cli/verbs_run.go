package cli

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// runWaitDefault is how long `run --wait` watches before giving up when the
// caller names no deadline of its own. It is longer than the default --timeout
// on purpose: waiting for a run is the one thing this CLI does that is expected
// to take minutes, and an agent that wants a shorter leash passes --timeout.
const runWaitDefault = 5 * time.Minute

// runPollDefault is how often a waited-for execution is read back. It is a
// read of one row, so polling it twice a second is cheaper than the request
// that started the run.
const runPollDefault = 500 * time.Millisecond

// runTerminalStatuses are the statuses a waited-for run can end in.
//
// The names are the wire values, not the server's Go constants: the CLI talks
// to whatever server the caller points it at, and importing the server's types
// to compare strings would tie the two together for no gain. `cancelling` and
// `waiting` are deliberately absent — a cancellation that has been requested is
// not a cancellation that happened, and a run waiting on an external resume has
// not finished.
var runTerminalStatuses = map[string]bool{
	"succeeded": true,
	"failed":    true,
	"cancelled": true,
}

// runVerbs is the one verb that starts work rather than reading it.
func runVerbs() []Verb {
	return []Verb{
		{
			Path:      "run",
			Operation: "run-workflow",
			Summary:   "start a workflow run (`--wait` to watch it finish)",
			Args:      []Arg{arg("workflow id")},
			Flags:     registerRunFlags,
			Run:       runWorkflow,
			Human:     humanExecution,
		},
	}
}

// runFlags are the run verb's own parameters.
type runFlags struct {
	input     string
	inputFile string
	trigger   string
	revision  string
	wait      bool
	poll      time.Duration
}

// registerRunFlags attaches the run parameters.
//
// --timeout stays the global flag it already is: the deadline for this
// invocation. For `run` that deadline is the wait, and its default is raised to
// five minutes when the caller does not name one.
func registerRunFlags(fs *flag.FlagSet) any {
	flags := &runFlags{}
	fs.StringVar(&flags.input, "input", "", "manual-run input as JSON, or @<file>")
	fs.StringVar(&flags.inputFile, "input-file", "", "read the input from a file, or - for stdin")
	fs.StringVar(&flags.trigger, "trigger", "", "trigger node to start from; omit to run every trigger")
	fs.StringVar(&flags.revision, "revision", "", "run a pinned revision of the workflow instead of the active one")
	fs.BoolVar(&flags.wait, "wait", false, "watch the run until it reaches a terminal state")
	fs.DurationVar(&flags.poll, "poll", runPollDefault, "how often to read a waited-for execution back")

	return flags
}

// runWorkflow starts a run and, when asked, waits for it.
func runWorkflow(ctx *Context, args []string) error {
	id, err := requireOneID(args, "workflow id")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*runFlags)
	if !ok {
		return usageError("the run verb was registered without its flags")
	}

	body, err := flags.request(ctx)
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost,
		apiPath("/workflows/"+url.PathEscape(id)+"/run"), nil, nil, body)
	if err != nil {
		return err
	}

	executionID := fieldValue(resp.Body, "id")
	ctx.Primary = executionID

	if !flags.wait {
		ctx.Data = jsonOrText(resp.Body)

		return nil
	}

	return waitForRun(ctx, flags, executionID)
}

// waitForRun watches one execution until it ends, the deadline passes, or the
// server stops answering.
func waitForRun(ctx *Context, flags *runFlags, executionID string) error {
	// The id goes out before the first poll, so a pipeline that traps it can
	// still find the run even when this process is interrupted mid-wait. It is
	// the whole of a --quiet invocation's output, so no envelope follows it.
	if ctx.Flags.Quiet && !ctx.Flags.JSON {
		if _, err := stdout(ctx.Env).Write([]byte(executionID + "\n")); err != nil {
			return outputWriteError("could not write the execution id: %v", err)
		}
		ctx.Streamed = true
	}

	deadline := runWaitDefault
	if ctx.Flags.wasProvided("timeout") {
		deadline = ctx.Flags.Timeout
	}
	cancel := ctx.bound(deadline)
	defer cancel()

	poll := flags.poll
	if poll <= 0 {
		poll = runPollDefault
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		record, status, err := readExecution(ctx, executionID)
		switch {
		case err == nil && status == "succeeded":
			ctx.Data = jsonOrText(record)

			return nil
		case err == nil && runTerminalStatuses[status]:
			return executionFailed(executionID, status, record)
		case err != nil && ctx.deadlineExceeded():
			return runTimedOut(executionID, deadline)
		case err != nil:
			return err
		}

		select {
		case <-ctx.Ctx.Done():
			return runTimedOut(executionID, deadline)
		case <-ticker.C:
		}
	}
}

// readExecution reads one execution record back.
func readExecution(ctx *Context, executionID string) (json.RawMessage, string, error) {
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet,
		apiPath("/executions/"+url.PathEscape(executionID)), nil, nil, nil)
	if err != nil {
		return nil, "", err
	}

	return json.RawMessage(resp.Body), fieldValue(resp.Body, "status"), nil
}

// executionFailed is the failure a run that ended badly produces.
//
// It exits non-zero rather than reporting ok:true with a failed status: a
// pipeline that ran `kilasflow run --wait` asked a question and the answer is
// "no", and an exit code of zero would make every `&&` chain continue past a
// failed run. The record travels in error.detail.execution, so a caller that
// wants to know why does not have to ask again.
func executionFailed(executionID, status string, record json.RawMessage) *ExitError {
	return &ExitError{
		Code:      ExitFailure,
		ErrCode:   "execution_failed",
		Message:   "execution " + executionID + " " + status,
		Execution: record,
	}
}

// runTimedOut is the failure of a run that had not finished in time.
func runTimedOut(executionID string, deadline time.Duration) *ExitError {
	return &ExitError{
		Code:    ExitFailure,
		ErrCode: "timeout",
		Message: "execution " + executionID + " did not finish within " + deadline.String() +
			"; keep watching it with `kilasflow exec get " + executionID + "`",
	}
}

// request builds the run body.
//
// The input is checked for being JSON before it is sent: the API carries it as
// a raw JSON value, so a malformed one is a 422 the caller would have to read
// a problem document to understand.
func (f *runFlags) request(ctx *Context) ([]byte, error) {
	input, err := f.inputJSON(ctx)
	if err != nil {
		return nil, err
	}

	trigger := strings.TrimSpace(f.trigger)
	revision := strings.TrimSpace(f.revision)
	if len(input) == 0 && trigger == "" && revision == "" {
		// Nothing to say: the API reads an absent body as "every trigger, no
		// input, the active revision", which is what a bare `run` means.
		return nil, nil
	}

	body := struct {
		Input             json.RawMessage `json:"input,omitempty"`
		TriggerNodeID     string          `json:"triggerNodeId,omitempty"`
		WorkflowVersionID string          `json:"workflowVersionId,omitempty"`
	}{Input: input, TriggerNodeID: trigger, WorkflowVersionID: revision}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, usageError("could not render the run request: %v", err)
	}

	return encoded, nil
}

// inputJSON resolves --input and --input-file into the raw JSON value to send.
func (f *runFlags) inputJSON(ctx *Context) (json.RawMessage, error) {
	switch {
	case f.input != "" && f.inputFile != "":
		return nil, usageError("--input and --input-file are mutually exclusive")
	case f.inputFile != "":
		raw, err := readDocument(f.inputFile, ctx.Env.Stdin)
		if err != nil {
			return nil, err
		}

		return raw, nil
	case f.input == "":
		return nil, nil
	case strings.HasPrefix(f.input, "@"):
		raw, err := readDocument(strings.TrimPrefix(f.input, "@"), ctx.Env.Stdin)
		if err != nil {
			return nil, err
		}

		return raw, nil
	default:
		if !json.Valid([]byte(f.input)) {
			return nil, usageError("--input is not valid JSON: %s", f.input)
		}

		return json.RawMessage(f.input), nil
	}
}
