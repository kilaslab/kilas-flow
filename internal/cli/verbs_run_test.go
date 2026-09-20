package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// executionResource is one execution record as the API returns it.
func executionResource(id, status string) string {
	return `{"id":"` + id + `","workflowId":"wf_1","workflowVersionId":"wfv_1","status":"` + status + `",` +
		`"trigger":"manual","startedAt":"2026-09-20T10:00:00Z","durationMs":12}`
}

// executionStub answers the run and read operations for one execution whose
// status changes as the test dictates.
type executionStub struct {
	mu       sync.Mutex
	statuses []string
	reads    int
	runBody  string
	// onRead runs before each get-execution answer, which is how a test
	// observes the world at the moment the CLI polled.
	onRead func()
}

// nextStatus advances through the scripted statuses, repeating the last one.
func (s *executionStub) nextStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	index := s.reads
	s.reads++
	if index >= len(s.statuses) {
		index = len(s.statuses) - 1
	}

	return s.statuses[index]
}

// readCount is how many times the execution was read back.
func (s *executionStub) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reads
}

// routes renders the stub's handlers.
func (s *executionStub) routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/run": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, s.runBody)
		},
		apiPrefix + "/executions/exec_1": func(w http.ResponseWriter, _ *http.Request) {
			if s.onRead != nil {
				s.onRead()
			}
			jsonBody(http.StatusOK, executionResource("exec_1", s.nextStatus()))(w, nil)
		},
	}
}

func TestRunPostsTheInputAndReportsTheRequest(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/run": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, executionResource("exec_1", "queued"))
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"run", "wf_1", "--url", srv.URL,
			"--input", `{"n":1}`, "--trigger", "manual", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/workflows/wf_1/run" {
		t.Fatalf("call = %+v, want POST /workflows/wf_1/run", call)
	}

	var sent struct {
		Input         json.RawMessage `json:"input"`
		TriggerNodeID string          `json:"triggerNodeId"`
	}
	if err := json.Unmarshal([]byte(call.Body), &sent); err != nil {
		t.Fatalf("the body is not the run request: %v (%q)", err, call.Body)
	}
	if string(sent.Input) != `{"n":1}` || sent.TriggerNodeID != "manual" {
		t.Fatalf("body = %q, want the input and the trigger", call.Body)
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["id"] != "exec_1" {
		t.Fatalf("data = %v, want the execution request", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "run-workflow" {
		t.Fatalf("meta.operation = %v, want run-workflow", meta["operation"])
	}
}

func TestRunReadsTheInputFromAFileAndFromStdin(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/run": jsonBody(http.StatusAccepted, executionResource("exec_1", "queued")),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--input-file", "-", "--json"},
		Stdin:  strings.NewReader(`{"from":"stdin"}`),
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if !strings.Contains(call.Body, `"input":{"from":"stdin"}`) {
		t.Fatalf("body = %q, want the stdin input", call.Body)
	}
}

func TestRunRefusesAnInputThatIsNotJSON(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/run": jsonBody(http.StatusAccepted, executionResource("exec_1", "queued")),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--input", "{nope", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a malformed input sent %d requests, want none", len(api.calls))
	}
}

func TestRunWaitPollsUntilTheExecutionSucceeds(t *testing.T) {
	stub := &executionStub{
		statuses: []string{"running", "running", "succeeded"},
		runBody:  executionResource("exec_1", "queued"),
	}
	srv := stubAPI(t, stub.routes())

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--wait", "--poll", "1ms", "--timeout", "5s", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stub.readCount() != 3 {
		t.Fatalf("polled %d times, want 3 (until the status was terminal)", stub.readCount())
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["status"] != "succeeded" {
		t.Fatalf("data = %v, want the finished execution", data)
	}
}

func TestRunWaitFailsWhenTheExecutionFailed(t *testing.T) {
	stub := &executionStub{
		statuses: []string{"failed"},
		runBody:  executionResource("exec_1", "queued"),
	}
	srv := stubAPI(t, stub.routes())

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--wait", "--poll", "1ms", "--timeout", "5s", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitFailure, stdout, stderr)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != false {
		t.Fatalf("envelope = %v, want a failure: a run that failed is not a successful call", doc)
	}
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "execution_failed" {
		t.Fatalf("error.code = %v, want execution_failed", failure["code"])
	}
	detail, _ := failure["detail"].(map[string]any)
	record, _ := detail["execution"].(map[string]any)
	if record["status"] != "failed" || record["id"] != "exec_1" {
		t.Fatalf("error.detail.execution = %v, want the whole record", detail["execution"])
	}
}

func TestRunWaitTimesOutAndNamesTheExecution(t *testing.T) {
	stub := &executionStub{
		statuses: []string{"running"},
		runBody:  executionResource("exec_1", "queued"),
	}
	srv := stubAPI(t, stub.routes())

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--wait", "--poll", "5ms", "--timeout", "40ms", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitFailure, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "timeout" {
		t.Fatalf("error.code = %v, want timeout", failure["code"])
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "exec_1") {
		t.Fatalf("the timeout does not name the execution: %q", message)
	}
	if !strings.Contains(message, "exec get") {
		t.Fatalf("the timeout does not say how to keep watching: %q", message)
	}
}

func TestRunWaitQuietPrintsTheIdBeforeItWaits(t *testing.T) {
	var (
		mu       sync.Mutex
		observed string
	)
	var out bytes.Buffer

	stub := &executionStub{
		statuses: []string{"running", "succeeded"},
		runBody:  executionResource("exec_1", "queued"),
		onRead: func() {
			mu.Lock()
			defer mu.Unlock()
			// The first poll sees whatever stdout already holds: a pipeline
			// that traps the id must have it before the wait begins.
			if observed == "" {
				observed = out.String()
			}
		},
	}
	srv := stubAPI(t, stub.routes())

	var errOut bytes.Buffer
	env := Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--wait", "--poll", "1ms", "--timeout", "5s", "--quiet"},
		Stdout: &out,
		Stderr: &errOut,
		Stdin:  strings.NewReader(""),
		Getenv: homeEnv(t.TempDir(), nil),
		TTY:    true,
	}
	code, handled := Run(env)
	if !handled || code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, out.String(), errOut.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if observed != "exec_1\n" {
		t.Fatalf("stdout held %q when the first poll ran, want the id already printed", observed)
	}
	if out.String() != "exec_1\n" {
		t.Fatalf("stdout = %q, want the id alone", out.String())
	}
}

func TestRunQuietPrintsTheId(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/run": jsonBody(http.StatusAccepted, executionResource("exec_1", "queued")),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "wf_1", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stdout != "exec_1\n" {
		t.Fatalf("stdout = %q, want the execution id", stdout)
	}
}

func TestRunNeedsAWorkflowId(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"run", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing id sent %d requests, want none", len(api.calls))
	}
}
