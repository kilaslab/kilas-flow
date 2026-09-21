package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseStub answers the event feed with the frames given.
//
// hold keeps the connection open after the frames: a stream that has delivered
// its terminal event but has not closed is exactly what a client must not wait
// on, and it is the case a real server produces when the run ends while the
// connection is still up.
func sseStub(frames string, hold bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		_, _ = io.WriteString(w, frames)
		flusher.Flush()

		if !hold {
			return
		}

		// Capped so a client that never stops reading cannot hang the suite;
		// the request context ends the moment the CLI closes the body.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
}

// terminalFrames is a finished run's feed: the comment the server opens with,
// two node events and the terminal frame.
const terminalFrames = "retry: 2000\n" +
	": connected\n" +
	"\n" +
	"id: 1\nevent: execution.started\ndata: {\"id\":1,\"type\":\"execution.started\"}\n\n" +
	"id: 2\nevent: node.started\ndata: {\"id\":2,\"type\":\"node.started\",\"nodeId\":\"manual\"}\n\n" +
	"id: 3\nevent: execution.completed\ndata: {\"id\":3,\"type\":\"execution.completed\",\"status\":\"succeeded\"}\n\n"

func TestExecListSendsEveryFilter(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions": jsonBody(http.StatusOK,
			`{"items":[{"id":"exec_1","status":"failed"}],"nextCursor":"cur_2"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"exec", "list", "--url", srv.URL,
			"--workflow", "wf_1", "--status", "failed", "--status", "cancelled",
			"--trigger", "manual", "--limit", "5", "--cursor", "cur_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Path != apiPrefix+"/executions" {
		t.Fatalf("path = %q", call.Path)
	}
	for _, want := range []string{"workflowId=wf_1", "status=failed", "status=cancelled", "trigger=manual", "limit=5", "cursor=cur_1"} {
		if !strings.Contains(call.Query, want) {
			t.Errorf("query %q is missing %s", call.Query, want)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["nextCursor"] != "cur_2" || data["count"] != float64(1) {
		t.Fatalf("data = %v, want the page", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-executions" {
		t.Fatalf("meta.operation = %v, want list-executions", meta["operation"])
	}
}

func TestExecGetReadsOneExecution(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1": jsonBody(http.StatusOK, executionResource("exec_1", "succeeded")),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "get", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/executions/exec_1" {
		t.Fatalf("call = %+v", call)
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["status"] != "succeeded" {
		t.Fatalf("data = %v, want the execution", doc["data"])
	}
}

func TestExecCancelPostsAndReportsTheAcceptedRequest(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/cancel": jsonBody(http.StatusAccepted, executionResource("exec_1", "cancelling")),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "cancel", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodPost || call.Path != apiPrefix+"/executions/exec_1/cancel" {
		t.Fatalf("call = %+v, want POST /executions/exec_1/cancel", call)
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["status"] != "cancelling" {
		t.Fatalf("data = %v, want the accepted cancellation", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "cancel-execution" {
		t.Fatalf("meta.operation = %v, want cancel-execution", meta["operation"])
	}
}

func TestExecTraceReturnsOneEnvelopeWithTheEvents(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(terminalFrames, false),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "trace", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["executionId"] != "exec_1" {
		t.Errorf("data.executionId = %v", data["executionId"])
	}
	if data["terminal"] != true {
		t.Errorf("data.terminal = %v, want true", data["terminal"])
	}
	if data["lastEventId"] != float64(3) {
		t.Errorf("data.lastEventId = %v, want 3", data["lastEventId"])
	}
	events, _ := data["events"].([]any)
	if len(events) != 3 {
		t.Fatalf("data.events = %v, want three frames", data["events"])
	}
	last, _ := events[2].(map[string]any)
	if last["event"] != "execution.completed" {
		t.Errorf("last event = %v", last)
	}
	payload, _ := last["data"].(map[string]any)
	if payload["status"] != "succeeded" {
		t.Errorf("the event payload was not carried: %v", last["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "stream-execution-events" {
		t.Fatalf("meta.operation = %v, want stream-execution-events", meta["operation"])
	}

	if call := api.last(t); call.Query != "" {
		t.Fatalf("query = %q, want none without --from", call.Query)
	}
}

func TestExecTraceStopsAtTheTerminalEventWithoutWaitingForTheClose(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		// The connection stays open after the terminal frame. A client that
		// waits for the close instead of the terminal event hangs here.
		apiPrefix + "/executions/exec_1/events": sseStub(terminalFrames, true),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)

		code, _, _, _ := runCLI(t, Env{
			Args:   []string{"exec", "trace", "exec_1", "--url", srv.URL, "--json", "--timeout", "5s"},
			Getenv: homeEnv(t.TempDir(), nil),
		})
		if code != ExitOK {
			t.Errorf("exit = %d, want %d", code, ExitOK)
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("exec trace waited for the connection to close instead of for the terminal event")
	}
}

func TestExecTraceReportsAnUnfinishedRunWithoutFailing(t *testing.T) {
	frames := "id: 1\nevent: node.started\ndata: {\"id\":1,\"nodeId\":\"manual\"}\n\n"
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(frames, true),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "trace", "exec_1", "--url", srv.URL, "--json", "--timeout", "100ms"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["terminal"] != false {
		t.Fatalf("data.terminal = %v, want false", data["terminal"])
	}
	if data["lastEventId"] != float64(1) {
		t.Fatalf("data.lastEventId = %v, want 1", data["lastEventId"])
	}
	if events, _ := data["events"].([]any); len(events) != 1 {
		t.Fatalf("data.events = %v, want what arrived before the deadline", data["events"])
	}
}

func TestExecTraceResumesFromAnEventId(t *testing.T) {
	var gotHeader string
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": func(w http.ResponseWriter, r *http.Request) {
			gotHeader = r.Header.Get("Last-Event-ID")
			sseStub(terminalFrames, false)(w, r)
		},
	})

	code, _, _, stderr := runCLI(t, Env{
		Args:   []string{"exec", "trace", "exec_1", "--url", srv.URL, "--json", "--from", "2"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if gotHeader != "2" {
		t.Fatalf("Last-Event-ID = %q, want 2", gotHeader)
	}
}

func TestExecTailWritesOneObjectPerLine(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(terminalFrames, false),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "tail", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want one line per event", stdout)
	}

	// Each line is a JSON object on its own: that is the documented exception
	// to the one-envelope rule, and a parser reads it a line at a time.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 is not JSON: %v (%q)", err, lines[0])
	}
	if first["event"] != "execution.started" {
		t.Fatalf("line 1 = %v", first)
	}

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatalf("line 3 is not JSON: %v", err)
	}
	if last["event"] != "execution.completed" {
		t.Fatalf("line 3 = %v, want the terminal event", last)
	}
	if payload, _ := last["data"].(map[string]any); payload["status"] != "succeeded" {
		t.Fatalf("line 3 payload = %v", last["data"])
	}
}

func TestExecTailExitsSixWhenTheDeadlinePasses(t *testing.T) {
	frames := "id: 1\nevent: node.started\ndata: {\"id\":1}\n\n"
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(frames, true),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "tail", "exec_1", "--url", srv.URL, "--json", "--timeout", "100ms"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotReady {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotReady, stdout, stderr)
	}
	if stdout != "{\"id\":1,\"event\":\"node.started\",\"data\":{\"id\":1}}\n" {
		t.Fatalf("stdout = %q, want the events and no envelope", stdout)
	}
	// The reason goes to stderr: stdout is the stream.
	if !strings.Contains(stderr, "exec_1") {
		t.Fatalf("stderr = %q, want a message naming the execution", stderr)
	}
}

func TestExecTailWithoutATimeoutWaitsForTheRunToEnd(t *testing.T) {
	// No --timeout means no deadline: a long run must not be cut off by the
	// thirty seconds that bound every other verb.
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(terminalFrames, true),
	})

	done := make(chan int, 1)
	go func() {
		code, _, _, _ := runCLI(t, Env{
			Args:   []string{"exec", "tail", "exec_1", "--url", srv.URL, "--json"},
			Getenv: homeEnv(t.TempDir(), nil),
		})
		done <- code
	}()

	select {
	case code := <-done:
		if code != ExitOK {
			t.Fatalf("exit = %d, want %d", code, ExitOK)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec tail did not return when the run ended")
	}
}

func TestExecTailHumanModePrintsOneLinePerEvent(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/events": sseStub(terminalFrames, false),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "tail", "exec_1", "--url", srv.URL},
		Getenv: homeEnv(t.TempDir(), nil),
		TTY:    true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want one line per event", stdout)
	}
	if !strings.Contains(lines[0], "execution.started") {
		t.Fatalf("line 1 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "node=manual") {
		t.Fatalf("line 2 = %q, want the node it belongs to", lines[1])
	}
}

// TestExecTailReportsARefusalOnStderr pins the other half of the streaming
// exception: a tailed invocation's stdout is the stream, so a refusal that
// happened before the stream opened is reported on stderr and in the exit code.
func TestExecTailReportsARefusalOnStderr(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_missing/events": problemBody(http.StatusNotFound,
			`{"title":"Not Found","status":404,"detail":"execution not found"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "tail", "exec_missing", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotFound, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want the stream and nothing else", stdout)
	}
	if !strings.Contains(stderr, "execution not found") {
		t.Fatalf("stderr = %q, want the server's refusal", stderr)
	}
}

func TestExecTraceOnAMissingExecutionCarriesTheRefusal(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_missing/events": problemBody(http.StatusNotFound,
			`{"title":"Not Found","status":404,"detail":"execution not found"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "trace", "exec_missing", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotFound, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "not_found" {
		t.Fatalf("error = %v, want the server's refusal", failure)
	}
}

func TestExecVerbsNeedAnExecutionId(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	for _, verb := range []string{"get", "cancel", "trace", "tail"} {
		code, _, stdout, stderr := runCLI(t, Env{
			Args:   []string{"exec", verb, "--url", srv.URL, "--json"},
			Getenv: homeEnv(t.TempDir(), nil),
		})
		if code != ExitUsage {
			t.Fatalf("exec %s exit = %d, want %d (stdout=%q stderr=%q)", verb, code, ExitUsage, stdout, stderr)
		}
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing id sent %d requests, want none", len(api.calls))
	}
}

func TestExecRetryPostsAndReportsTheNewExecution(t *testing.T) {
	// The operation answers 201 with the new execution resource and sets no
	// Location header, so the id a --quiet caller reads comes from the body.
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/retry": jsonBody(http.StatusCreated, executionResource("exec_2", "queued")),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "retry", "exec_1", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/executions/exec_1/retry" {
		t.Fatalf("call = %+v, want POST %s/executions/exec_1/retry", call, apiPrefix)
	}
	if call.Body != "" {
		t.Fatalf("body = %q, want none: the operation copies the execution it reads", call.Body)
	}
	if stdout != "exec_2\n" {
		t.Fatalf("stdout = %q, want the new execution id", stdout)
	}
}

func TestExecRetryOnARunningExecutionExitsConflict(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/retry": problemBody(http.StatusConflict,
			`{"title":"Conflict","status":409,"detail":"execution is still running"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "retry", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitConflict {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitConflict, stdout, stderr)
	}

	failure, _ := envelope(t, stdout)["error"].(map[string]any)
	if failure["code"] != "conflict" || failure["status"] != float64(409) {
		t.Fatalf("error = %v, want conflict carrying the status", failure)
	}
}

func TestExecRetryNeedsAnExecutionId(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"exec", "retry", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing id sent %d requests, want none", len(api.calls))
	}
}
