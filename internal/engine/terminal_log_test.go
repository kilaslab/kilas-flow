package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

const (
	terminalLogMessage = "execution finished"
	// terminalLogItemMarker rides in the trigger payload and must never reach a
	// log line: item data is what the operator's log pipeline is not cleared to
	// hold.
	terminalLogItemMarker = "ITEM-DATA-MARKER-7731"
)

// terminalLogLine is one parsed JSON log record.
type terminalLogLine map[string]any

func (line terminalLogLine) text(key string) string {
	value, _ := line[key].(string)
	return value
}

// terminalLogs collects the "execution finished" lines a buffer holds.
func terminalLogs(t *testing.T, buffer *bytes.Buffer) []terminalLogLine {
	t.Helper()
	var lines []terminalLogLine
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if raw == "" {
			continue
		}
		var line terminalLogLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		if line.text("msg") == terminalLogMessage {
			lines = append(lines, line)
		}
	}
	return lines
}

type terminalLogStubCredentials struct{ secret string }

func (store terminalLogStubCredentials) Resolve(context.Context, repository.TenantScope, string) (credentials.Record, map[string]string, error) {
	return credentials.Record{ID: "cred-1", Name: "Query key", Type: "httpQueryAuth"},
		map[string]string{"name": "api_key", "value": store.secret}, nil
}

// terminalLogFixture saves a manual workflow whose second node either fails or
// succeeds, and holds a service logging into the buffer.
type terminalLogFixture struct {
	sandbox *engineSandbox
	tenant  repository.TenantScope
	logs    *bytes.Buffer
	service *engine.Service
}

func newTerminalLogFixture(t *testing.T, level string, failure error) *terminalLogFixture {
	t.Helper()
	sandbox := newEngineSandbox(t, "kilasflow.db")
	tenant := repository.TenantScope{ID: "tenant-terminal-log"}
	if err := sandbox.catalog.Register(node.Definition{
		Group: []node.NodeGroup{node.GroupTransform}, Type: "kilasflow.test.terminal", Version: workflow.V(1),
		DisplayName: "Terminal", Category: "Test",
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, ExecutorID: "test.terminal",
	}); err != nil {
		t.Fatalf("Register(terminal node) error = %v", err)
	}
	if err := sandbox.executors.Register("test.terminal", engine.ExecutorFunc(func(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
		if failure != nil {
			resolved, err := request.Credentials.ResolveCredential(ctx, "cred-1")
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w (url ?api_key=%s)", failure, resolved.Fields["value"])
		}
		return workflow.NodeOutput{input["main"]}, nil
	})); err != nil {
		t.Fatalf("Register(terminal executor) error = %v", err)
	}
	if _, err := repository.NewWorkflowStore(sandbox.db.DB).SaveDraft(sandbox.ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_terminal", Name: "Nightly sync",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "step", Name: "Terminal", Type: "kilasflow.test.terminal", TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "step", Port: "main"},
		}},
		Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	logs := &bytes.Buffer{}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: sandbox.store, Catalog: sandbox.catalog, Runner: engine.NewRunner(sandbox.executors),
		Credentials: terminalLogStubCredentials{secret: leakySecret},
		WorkerID:    "terminal-log-worker", DefaultTimeout: 5 * time.Second,
		Logger:            slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ExecutionLogLevel: level,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return &terminalLogFixture{sandbox: sandbox, tenant: tenant, logs: logs, service: service}
}

func (fixture *terminalLogFixture) queue(t *testing.T) execution.Record {
	t.Helper()
	queued, err := fixture.sandbox.store.QueueManualLatest(fixture.sandbox.ctx, fixture.tenant, "wf_terminal",
		fixture.sandbox.catalog, "", json.RawMessage(fmt.Sprintf(`{"note":%q}`, terminalLogItemMarker)))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	return queued
}

func (fixture *terminalLogFixture) runOne(t *testing.T) {
	t.Helper()
	if worked, err := fixture.service.RunOnce(fixture.sandbox.ctx); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want one execution run", worked, err)
	}
}

// A failed execution leaves one structured WARN line an operator can alert on:
// the status, the execution, the workflow by id and name, the node that
// failed, the tenant and the error. It is the ticket's whole point — before
// this a failed run left nothing in the server log (FEAT-53pa9a).
func TestAFailedExecutionWritesOneWarnLineNamingTheFailingNode(t *testing.T) {
	fixture := newTerminalLogFixture(t, "", errors.New("the API key was rejected"))
	queued := fixture.queue(t)
	fixture.runOne(t)

	lines := terminalLogs(t, fixture.logs)
	if len(lines) != 1 {
		t.Fatalf("terminal lines = %d, want exactly 1: %s", len(lines), fixture.logs.String())
	}
	line := lines[0]
	for key, want := range map[string]string{
		"level": "WARN", "status": "failed", "execution": queued.ID, "workflow": "wf_terminal",
		"workflowName": "Nightly sync", "tenant": fixture.tenant.ID, "node": "step", "trigger": "manual",
		"errorCode": "execution.failed",
	} {
		if got := line.text(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := line.text("error"); !strings.Contains(got, "the API key was rejected") {
		t.Errorf("error = %q, want the failure's own text", got)
	}
}

// The line's error text is the one already persisted on the execution, which
// the runner scrubbed of every secret the node resolved, and no item data is
// logged at all.
func TestTheTerminalLineCarriesNeitherASecretNorItemData(t *testing.T) {
	fixture := newTerminalLogFixture(t, "", errors.New("the API key was rejected"))
	queued := fixture.queue(t)
	fixture.runOne(t)

	raw := fixture.logs.String()
	if strings.Contains(raw, leakySecret) {
		t.Errorf("a log line carries the credential value %q: %s", leakySecret, raw)
	}
	if strings.Contains(raw, terminalLogItemMarker) {
		t.Errorf("a log line carries item data: %s", raw)
	}
	stored, err := fixture.sandbox.store.Get(fixture.sandbox.ctx, fixture.tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	var persisted struct{ Message string }
	if err := json.Unmarshal(stored.Error, &persisted); err != nil {
		t.Fatalf("decode persisted error: %v", err)
	}
	lines := terminalLogs(t, fixture.logs)
	if len(lines) != 1 || lines[0].text("error") != persisted.Message {
		t.Errorf("logged error = %v, want the persisted, scrubbed %q", lines, persisted.Message)
	}
}

// A succeeded run is INFO and names no node and no error.
func TestASucceededExecutionWritesOneInfoLine(t *testing.T) {
	fixture := newTerminalLogFixture(t, "", nil)
	queued := fixture.queue(t)
	fixture.runOne(t)

	lines := terminalLogs(t, fixture.logs)
	if len(lines) != 1 {
		t.Fatalf("terminal lines = %d, want 1: %s", len(lines), fixture.logs.String())
	}
	line := lines[0]
	if line.text("level") != "INFO" || line.text("status") != "succeeded" || line.text("execution") != queued.ID {
		t.Errorf("line = %v, want an INFO succeeded line for %s", line, queued.ID)
	}
	for _, key := range []string{"node", "error", "errorCode"} {
		if _, present := line[key]; present {
			t.Errorf("a succeeded line carries %q = %v", key, line[key])
		}
	}
}

// A run cancelled while still queued never reaches a worker, and still ends
// with a line: the cancel path is a terminal path too.
func TestACancelledQueuedExecutionWritesAnInfoLine(t *testing.T) {
	fixture := newTerminalLogFixture(t, "", nil)
	queued := fixture.queue(t)
	if _, err := fixture.service.Cancel(fixture.sandbox.ctx, fixture.tenant, queued.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	lines := terminalLogs(t, fixture.logs)
	if len(lines) != 1 || lines[0].text("status") != "cancelled" || lines[0].text("level") != "INFO" {
		t.Fatalf("terminal lines = %v, want one INFO cancelled line", lines)
	}
}

// The level is configurable: "warn" keeps failed runs and drops the rest, and
// "off" writes nothing.
func TestTheTerminalLineLevelIsConfigurable(t *testing.T) {
	for _, tc := range []struct {
		level       string
		failedLines int
		okLines     int
	}{
		{level: "info", failedLines: 1, okLines: 1},
		{level: "warn", failedLines: 1, okLines: 0},
		{level: "off", failedLines: 0, okLines: 0},
	} {
		t.Run(tc.level, func(t *testing.T) {
			failing := newTerminalLogFixture(t, tc.level, errors.New("boom"))
			failing.queue(t)
			failing.runOne(t)
			if got := len(terminalLogs(t, failing.logs)); got != tc.failedLines {
				t.Errorf("failed run lines at %q = %d, want %d", tc.level, got, tc.failedLines)
			}
			passing := newTerminalLogFixture(t, tc.level, nil)
			passing.queue(t)
			passing.runOne(t)
			if got := len(terminalLogs(t, passing.logs)); got != tc.okLines {
				t.Errorf("succeeded run lines at %q = %d, want %d", tc.level, got, tc.okLines)
			}
		})
	}
}

// Every terminal event and the line it deserves leave the engine through one
// function. A second publisher of a terminal event, or a second log site, would
// be a path whose runs end without a trace in the log.
func TestOnlyTheSettleHelperPublishesTerminalEventsAndLogsTheLine(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "terminal_log.go" {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		text := string(source)
		for _, forbidden := range []string{"events.ExecutionCompleted", "events.ExecutionFailed", "events.ExecutionCancelled", terminalLogMessage} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s mentions %q: terminal events and the terminal log line belong to terminal_log.go alone", name, forbidden)
			}
		}
	}
}

// A sub-workflow call is a terminal path of its own, finished inline by the
// calling worker rather than claimed from the queue: the child gets its line,
// naming the execution that called it, and the parent gets its own.
func TestASubWorkflowChildWritesItsOwnLineNamingItsParent(t *testing.T) {
	setup := newComposition(t)
	child := setup.activate(t, setup.tenant, subworkflow("Child", "stamped"))
	parent := setup.activate(t, setup.tenant, caller("Parent", child, nil))

	logs := &bytes.Buffer{}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions: setup.executions, Catalog: setup.catalog,
		Runner:   engine.NewRunner(withExecutors(t, nil)),
		WorkerID: "terminal-log-subworkflow", DefaultTimeout: 30 * time.Second,
		SubworkflowTriggerType: nodes.ExecuteWorkflowTriggerType,
		Logger:                 slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	queued, err := setup.executions.QueueManualLatest(context.Background(), setup.tenant, parent, setup.catalog, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	if worked, err := service.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("RunOnce() = (%v, %v), want the parent run", worked, err)
	}

	lines := terminalLogs(t, logs)
	if len(lines) != 2 {
		t.Fatalf("terminal lines = %d, want 2 (child, then parent): %s", len(lines), logs.String())
	}
	childLine, parentLine := lines[0], lines[1]
	if childLine.text("workflowName") != "Child" || childLine.text("trigger") != "subworkflow" ||
		childLine.text("parentExecution") != queued.ID || childLine.text("workflow") != child {
		t.Errorf("child line = %v, want the Child workflow called by %s", childLine, queued.ID)
	}
	if parentLine.text("execution") != queued.ID || parentLine.text("workflowName") != "Parent" {
		t.Errorf("parent line = %v, want the calling execution %s", parentLine, queued.ID)
	}
	if _, present := parentLine["parentExecution"]; present {
		t.Errorf("a top-level run carries parentExecution = %v", parentLine["parentExecution"])
	}
}
