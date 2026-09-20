package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

type streamedEvent struct {
	name string
	id   string
	data map[string]any
}

// readStream consumes an SSE response until it has `want` events or the
// deadline passes, so a test never hangs on a stream that stays open.
func readStream(t *testing.T, body *bufio.Reader, want int) []streamedEvent {
	t.Helper()
	collected := make([]streamedEvent, 0, want)
	current := streamedEvent{}
	for len(collected) < want {
		line, err := body.ReadString('\n')
		if err != nil {
			return collected
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if current.data != nil {
				collected = append(collected, current)
			}
			current = streamedEvent{}
		case strings.HasPrefix(line, "id:"):
			current.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			current.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			payload := map[string]any{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload); err == nil {
				current.data = payload
			}
		}
	}
	return collected
}

func newEventServer(t *testing.T, broker *events.Broker) *httptest.Server {
	t.Helper()
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, Events: broker})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func TestExecutionEventStreamReplaysRetainedHistoryThenClosesOnTerminal(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)

	tenant := repository.DefaultTenantID
	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-1", WorkflowID: "wf-1", Type: events.ExecutionStarted, Status: execution.StatusRunning})
	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-1", NodeID: "n1", Type: events.NodeCompleted, Status: execution.StatusSucceeded})
	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-1", Type: events.ExecutionCompleted, Status: execution.StatusSucceeded})

	response, err := http.Get(server.URL + "/api/v1/executions/exec-1/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	streamed := readStream(t, bufio.NewReader(response.Body), 3)
	if len(streamed) != 3 {
		t.Fatalf("streamed %d events, want the full retained history", len(streamed))
	}
	if streamed[0].name != string(events.ExecutionStarted) || streamed[2].name != string(events.ExecutionCompleted) {
		t.Fatalf("streamed events = %v, want started…completed", []string{streamed[0].name, streamed[1].name, streamed[2].name})
	}
	if streamed[0].id != "1" || streamed[2].id != "3" {
		t.Errorf("event IDs = %q…%q, want 1…3", streamed[0].id, streamed[2].id)
	}

	// The stream must end after the terminal event rather than leaving the
	// client reconnecting to a run that already finished.
	rest := make([]byte, 1)
	done := make(chan struct{})
	go func() {
		_, _ = response.Body.Read(rest)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("the stream stayed open after a terminal event")
	}
}

func TestExecutionEventStreamResumesFromLastEventID(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)
	tenant := repository.DefaultTenantID

	for range 2 {
		broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-2", Type: events.NodeCompleted, Status: execution.StatusSucceeded})
	}
	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-2", Type: events.ExecutionCompleted, Status: execution.StatusSucceeded})

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/executions/exec-2/events", nil)
	request.Header.Set("Last-Event-ID", "2")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()

	streamed := readStream(t, bufio.NewReader(response.Body), 1)
	if len(streamed) != 1 {
		t.Fatalf("streamed %d events, want only the one after ID 2", len(streamed))
	}
	if streamed[0].id != "3" || streamed[0].name != string(events.ExecutionCompleted) {
		t.Errorf("resumed event = %q/%q, want 3/execution.completed", streamed[0].id, streamed[0].name)
	}
}

func TestExecutionEventStreamDeliversEventsPublishedWhileConnected(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)
	tenant := repository.DefaultTenantID

	response, err := http.Get(server.URL + "/api/v1/executions/exec-3/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)

	// Wait for the connect comment so the subscription is definitely attached.
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read connect frame: %v", err)
	}

	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-3", NodeID: "n1", Type: events.NodeStarted, Status: execution.StatusRunning})
	broker.Publish(events.Event{TenantID: tenant, ExecutionID: "exec-3", Type: events.ExecutionCompleted, Status: execution.StatusSucceeded})

	streamed := readStream(t, reader, 2)
	if len(streamed) != 2 {
		t.Fatalf("streamed %d live events, want 2", len(streamed))
	}
	if streamed[0].data["nodeId"] != "n1" {
		t.Errorf("first live event = %#v, want the node correlation ID", streamed[0].data)
	}
}

func TestExecutionEventStreamNeverLeaksAnotherTenantsRun(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)

	// Same execution ID, a different tenant: guessing the ID must yield nothing.
	broker.Publish(events.Event{TenantID: "someone-else", ExecutionID: "exec-4", Type: events.ExecutionStarted})
	broker.Publish(events.Event{TenantID: "someone-else", ExecutionID: "exec-4", Type: events.ExecutionCompleted})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/executions/exec-4/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()

	streamed := readStream(t, bufio.NewReader(response.Body), 1)
	if len(streamed) != 0 {
		t.Fatalf("another tenant's events were streamed: %#v", streamed)
	}
}

func TestExecutionEventStreamRedactsPayloads(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)

	broker.Publish(events.Event{
		TenantID: repository.DefaultTenantID, ExecutionID: "exec-5", NodeID: "http", Type: events.NodeCompleted,
		Data: json.RawMessage(`{"headers":{"Authorization":"Bearer live-token"}}`),
	})
	broker.Publish(events.Event{TenantID: repository.DefaultTenantID, ExecutionID: "exec-5", Type: events.ExecutionCompleted})

	response, err := http.Get(server.URL + "/api/v1/executions/exec-5/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()

	streamed := readStream(t, bufio.NewReader(response.Body), 2)
	encoded, _ := json.Marshal(streamed[0].data)
	if strings.Contains(string(encoded), "live-token") {
		t.Fatalf("the live feed leaked a credential: %s", encoded)
	}
}

func TestExecutionEventStreamStopsWhenTheClientDisconnects(t *testing.T) {
	broker := events.NewBroker(events.BrokerOptions{})
	server := newEventServer(t, broker)

	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/executions/exec-6/events", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read connect frame: %v", err)
	}

	cancel()
	_ = response.Body.Close()

	// Publishing after the client is gone must not panic or block the caller.
	done := make(chan struct{})
	go func() {
		for range 100 {
			broker.Publish(events.Event{TenantID: repository.DefaultTenantID, ExecutionID: "exec-6", Type: events.NodeStarted})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked after the client disconnected")
	}
}

func TestExecutionEventStreamIsDocumentedInTheOpenAPISpec(t *testing.T) {
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, Events: events.NewBroker(events.BrokerOptions{})})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("openapi status = %d, want 200", recorder.Code)
	}

	var document struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Responses   map[string]struct {
				Content map[string]any `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&document); err != nil {
		t.Fatalf("decode openapi = %v", err)
	}
	operation, found := document.Paths["/api/v1/executions/{id}/events"]["get"]
	if !found {
		t.Fatal("the event stream is not documented in the OpenAPI spec")
	}
	if operation.OperationID != "stream-execution-events" {
		t.Errorf("operationId = %q, want stream-execution-events", operation.OperationID)
	}
	if _, ok := operation.Responses["200"].Content["text/event-stream"]; !ok {
		t.Errorf("documented content types = %#v, want text/event-stream", operation.Responses["200"].Content)
	}
}

// A finished run whose terminal frame never reached the broker still ends its
// stream, because the durable record is what says the run is over.
//
// The gate reads that record before the stream opens, and the handler used to
// synthesize the terminal frame only when the replay was empty. A replay that
// kept earlier frames but lost the terminal one — a dropped publication, a
// released history — therefore streamed heartbeats forever: exactly the held
// connection this endpoint exists not to hold, and one a browser tab reopens
// after every reconnect.
func TestAFinishedExecutionEndsItsStreamEvenWhenTheTerminalFrameWasLost(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "events.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}

	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	workflow, err := repository.NewWorkflowStore(db.DB).SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: "Streamed",
		Nodes:       []workflow.Node{{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	executions := repository.NewExecutionStore(db.DB)
	finished := time.Now().UTC()
	record, err := executions.Create(context.Background(), tenant, execution.Record{
		WorkflowID: workflow.ID, WorkflowVersionID: workflow.LatestVersion.ID, Status: execution.StatusSucceeded,
		Trigger: execution.TriggerManual, StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	broker := events.NewBroker(events.BrokerOptions{})
	// The frames before the outcome survived; the outcome itself did not.
	broker.Publish(events.Event{TenantID: repository.DefaultTenantID, ExecutionID: record.ID, Type: events.ExecutionStarted, Status: execution.StatusRunning})
	broker.Publish(events.Event{TenantID: repository.DefaultTenantID, ExecutionID: record.ID, NodeID: "n1", Type: events.NodeCompleted, Status: execution.StatusSucceeded})

	server := httptest.NewServer(newTestServer(t, api.Deps{
		DB: db, Events: broker, Executions: executions,
	}))
	t.Cleanup(server.Close)

	// The client deadline is what turns "the stream never ends" into a failed
	// read rather than a hung test: this endpoint's defect is a stream that
	// stays open, so the test has to bound its own patience.
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(server.URL + "/api/v1/executions/" + record.ID + "/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer response.Body.Close()

	streamed := readStream(t, bufio.NewReader(response.Body), 3)
	if len(streamed) != 3 {
		t.Fatalf("streamed %d events, want the two retained frames and the outcome", len(streamed))
	}
	if last := streamed[2]; last.name != string(events.ExecutionCompleted) || last.data["status"] != string(execution.StatusSucceeded) {
		t.Fatalf("the reconstructed outcome = %#v, want execution.completed with the record's status", last)
	}
	// And the stream is over rather than waiting for a publication that can
	// never come.
	rest := make([]byte, 1)
	done := make(chan struct{})
	go func() {
		_, _ = response.Body.Read(rest)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("the stream stayed open for a run that had already finished")
	}
}
