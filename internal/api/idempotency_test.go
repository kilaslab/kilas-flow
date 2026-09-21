package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
)

// runResource is the part of a queued run these tests read, including the
// input echo the first response carries and the replay deliberately omits.
type runResource struct {
	ID                string          `json:"id"`
	WorkflowID        string          `json:"workflowId"`
	WorkflowVersionID string          `json:"workflowVersionId"`
	Status            string          `json:"status"`
	Trigger           string          `json:"trigger"`
	TriggerNodeID     string          `json:"triggerNodeId"`
	Input             json.RawMessage `json:"input"`
	CreatedAt         string          `json:"createdAt"`
}

// idempotencyProblemBody is the problem document shape these tests assert on.
// Value is left raw because a framework validation detail carries the offending
// string there while an idempotency conflict carries the typed issue object.
type idempotencyProblemBody struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Errors []struct {
		Message  string          `json:"message"`
		Location string          `json:"location"`
		Value    json.RawMessage `json:"value"`
	} `json:"errors"`
}

// code is the typed issue code on the first error detail, empty when the
// detail carries no issue (a framework validation error, for instance).
func (problem idempotencyProblemBody) code() string {
	if len(problem.Errors) == 0 {
		return ""
	}
	var issue struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(problem.Errors[0].Value, &issue); err != nil {
		return ""
	}
	return issue.Code
}

// failingIdempotencyStore is a store that cannot answer, which stands in for
// the database underneath the idempotency layer going away.
type failingIdempotencyStore struct{ err error }

func (store failingIdempotencyStore) Claim(context.Context, repository.IdempotencyClaim) (repository.IdempotencyClaimResult, error) {
	return repository.IdempotencyClaimResult{}, store.err
}

func (failingIdempotencyStore) Complete(context.Context, repository.TenantScope, string, string, int, []byte, time.Time) (bool, error) {
	return false, nil
}

func (failingIdempotencyStore) Release(context.Context, repository.TenantScope, string, string) error {
	return nil
}

func (failingIdempotencyStore) Sweep(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (failingIdempotencyStore) PurgeTenant(context.Context, repository.TenantScope) (int64, error) {
	return 0, nil
}

// sharedIdempotencyDB is one migrated SQLite database two servers can share,
// which is how the tenant-scoping tests get a second tenant without a second
// store.
func sharedIdempotencyDB(t *testing.T) *database.DB {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "idempotency.db"),
	}, log)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	return db
}

// newIdempotentServer builds the API over a database the caller controls, so
// two servers can share one durable key store.
func newIdempotentServer(
	t *testing.T,
	db *database.DB,
	tenantID string,
	executions repository.ExecutionRepository,
	controller handlers.ExecutionController,
	service *idempotency.Service,
	issuer *embed.Issuer,
) http.Handler {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("nodes.RegisterAll() error = %v", err)
	}
	engine, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("datastore.NewEngine() error = %v", err)
	}
	return newTestServer(t, api.Deps{
		DB:                  db,
		NodeRegistry:        registry,
		Workflows:           repository.NewWorkflowStore(db.DB).WithWebhooks(webhook.Extract(registry, nodes.WebhookPath)),
		Executions:          executions,
		Datastores:          engine,
		Tenants:             fixedTenant{id: tenantID},
		ExecutionController: controller,
		EmbedIssuer:         issuer,
		Idempotency:         service,
	})
}

func newIdempotentAPI(t *testing.T, db *database.DB, tenantID string, executions repository.ExecutionRepository, controller handlers.ExecutionController) http.Handler {
	t.Helper()
	service, err := idempotency.NewService(repository.NewIdempotencyStore(db.DB), idempotency.Options{Retention: time.Hour})
	if err != nil {
		t.Fatalf("idempotency.NewService() error = %v", err)
	}
	return newIdempotentServer(t, db, tenantID, executions, controller, service, nil)
}

// requestBody encodes without HTML escaping, so a payload of angle brackets is
// large in the request and larger once the server escapes it for the response.
// That gap is where the recorded-outcome cap sits.
func requestBody(t *testing.T, body any) []byte {
	t.Helper()
	if body == nil {
		return nil
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		t.Fatalf("encode request body = %v", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

// postBytes drives one POST and takes no *testing.T, so a goroutine may use it.
func postBytes(handler http.Handler, path, key string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func postWithKey(t *testing.T, handler http.Handler, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return postBytes(handler, path, key, requestBody(t, body))
}

func decodeRun(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int) runResource {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", recorder.Code, wantStatus, recorder.Body)
	}
	var resource runResource
	if err := json.Unmarshal(recorder.Body.Bytes(), &resource); err != nil {
		t.Fatalf("decode run response = %v", err)
	}
	return resource
}

func decodeIdempotencyProblem(t *testing.T, recorder *httptest.ResponseRecorder) idempotencyProblemBody {
	t.Helper()
	var problem idempotencyProblemBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem = %v (body: %s)", err, recorder.Body)
	}
	return problem
}

func runPath(workflowID string) string { return "/api/v1/workflows/" + workflowID + "/run" }

func countExecutions(t *testing.T, executions repository.ExecutionRepository, tenantID string) int {
	t.Helper()
	page, err := executions.List(context.Background(), repository.TenantScope{ID: tenantID}, repository.ExecutionFilter{})
	if err != nil {
		t.Fatalf("executions.List() error = %v", err)
	}
	return len(page.Records)
}

func countIdempotencyRows(t *testing.T, db *database.DB) int64 {
	t.Helper()
	var count int64
	if err := db.DB.Raw("SELECT count(*) FROM idempotency_keys").Scan(&count).Error; err != nil {
		t.Fatalf("count idempotency_keys = %v", err)
	}
	return count
}

func listRows(t *testing.T, handler http.Handler, datastoreID string) []map[string]any {
	t.Helper()
	page := requestJSON[struct {
		Items []map[string]any `json:"items"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+datastoreID+"/rows", nil, http.StatusOK)
	return page.Items
}

func rowID(row map[string]any) float64 {
	id, _ := row["id"].(float64)
	return id
}

// upsertRequest is the body both the replay and the compaction tests send. The
// filter matches the row the values would insert, so a keyless repeat takes the
// update branch while a replay still reports the insert it recorded.
func upsertRequest(column, value string) map[string]any {
	return map[string]any{
		"filter": map[string]any{
			"type":    "and",
			"filters": []map[string]any{{"columnName": column, "condition": "eq", "value": value}},
		},
		"values": map[string]any{column: value},
	}
}

func TestRunWithTheSameIdempotencyKeyReplaysTheFirstExecution(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	controller := &recordingExecutionController{}
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, controller)
	created := createWorkflow(t, handler, validManualWorkflow("Idempotent"))
	body := map[string]any{"input": map[string]any{"n": 1}}

	first := decodeRun(t, postWithKey(t, handler, runPath(created.ID), "key-1", body), http.StatusAccepted)
	if first.ID == "" {
		t.Fatal("the first run queued no execution")
	}

	second := postWithKey(t, handler, runPath(created.ID), "key-1", body)
	replay := decodeRun(t, second, http.StatusAccepted)
	if replay.ID != first.ID {
		t.Errorf("replay execution id = %q, want the first %q", replay.ID, first.ID)
	}
	if got := second.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", got)
	}
	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}
	if controller.wakeCalls != 1 {
		t.Errorf("wakeCalls = %d, want 1 (a replay must not wake a worker)", controller.wakeCalls)
	}
}

func TestARunReplayDoesNotRepeatTheInputEcho(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Echo"))
	body := map[string]any{"input": map[string]any{"secret": "one"}}

	first := decodeRun(t, postWithKey(t, handler, runPath(created.ID), "key-1", body), http.StatusAccepted)
	if len(first.Input) == 0 {
		t.Fatal("the first response did not echo the run input")
	}

	replay := decodeRun(t, postWithKey(t, handler, runPath(created.ID), "key-1", body), http.StatusAccepted)
	if replay.ID != first.ID || replay.WorkflowID != first.WorkflowID || replay.CreatedAt != first.CreatedAt {
		t.Errorf("replay = %+v, want the first run's identity %+v", replay, first)
	}
	if len(replay.Input) != 0 {
		t.Errorf("the replay repeated the input echo: %s", replay.Input)
	}
}

func TestRunWithADifferentIdempotencyKeyQueuesASecondExecution(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Distinct"))
	body := map[string]any{"input": map[string]any{"n": 1}}

	first := decodeRun(t, postWithKey(t, handler, runPath(created.ID), "key-1", body), http.StatusAccepted)
	second := decodeRun(t, postWithKey(t, handler, runPath(created.ID), "key-2", body), http.StatusAccepted)
	if first.ID == second.ID {
		t.Errorf("two keys queued one execution %q", first.ID)
	}
	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 2 {
		t.Errorf("executions = %d, want 2", got)
	}
}

func TestRunWithoutAnIdempotencyKeyIsUnchanged(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Unkeyed"))
	body := map[string]any{"input": map[string]any{"n": 1}}

	first := postWithKey(t, handler, runPath(created.ID), "", body)
	second := postWithKey(t, handler, runPath(created.ID), "", body)
	if first.Header().Get("Idempotent-Replayed") != "" || second.Header().Get("Idempotent-Replayed") != "" {
		t.Error("an unkeyed run was marked as a replay")
	}
	if decodeRun(t, first, http.StatusAccepted).ID == decodeRun(t, second, http.StatusAccepted).ID {
		t.Error("two unkeyed runs queued one execution")
	}
	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 2 {
		t.Errorf("executions = %d, want 2", got)
	}
}

func TestRunReusingAKeyWithADifferentRequestIs409(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Conflict"))
	other := createWorkflow(t, handler, validManualWorkflow("Elsewhere"))

	if recorder := postWithKey(t, handler, runPath(created.ID), "key-1", map[string]any{"input": map[string]any{"n": 1}}); recorder.Code != http.StatusAccepted {
		t.Fatalf("first run status = %d, want 202 (body: %s)", recorder.Code, recorder.Body)
	}

	for _, test := range []struct {
		name string
		path string
		body any
	}{
		{"a different input", runPath(created.ID), map[string]any{"input": map[string]any{"n": 2}}},
		{"a different trigger node", runPath(created.ID), map[string]any{"triggerNodeId": "manual"}},
		{"a different workflow", runPath(other.ID), map[string]any{"input": map[string]any{"n": 1}}},
	} {
		recorder := postWithKey(t, handler, test.path, "key-1", test.body)
		if recorder.Code != http.StatusConflict {
			t.Errorf("%s: status = %d, want 409 (body: %s)", test.name, recorder.Code, recorder.Body)
			continue
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
			t.Errorf("%s: content type = %q, want application/problem+json", test.name, got)
		}
		problem := decodeIdempotencyProblem(t, recorder)
		if problem.Status != http.StatusConflict || problem.Title != "Conflict" {
			t.Errorf("%s: problem = %+v, want a 409 Conflict", test.name, problem)
		}
		if problem.code() != "idempotency_key_reused" {
			t.Errorf("%s: errors = %+v, want code idempotency_key_reused", test.name, problem.Errors)
		}
		if len(problem.Errors) == 0 || problem.Errors[0].Location != "header.Idempotency-Key" {
			t.Errorf("%s: location = %+v, want header.Idempotency-Key", test.name, problem.Errors)
		}
	}

	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
		t.Errorf("executions = %d, want 1 (a conflict must not queue)", got)
	}
}

func TestAnotherTenantCannotReplayOrObserveAnIdempotencyKey(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handlerA := newIdempotentAPI(t, db, "tenant-a", executions, nil)
	handlerB := newIdempotentAPI(t, db, "tenant-b", executions, nil)
	workflowA := createWorkflow(t, handlerA, validManualWorkflow("A"))
	workflowB := createWorkflow(t, handlerB, validManualWorkflow("B"))

	first := decodeRun(t, postWithKey(t, handlerA, runPath(workflowA.ID), "shared-key", map[string]any{"input": map[string]any{"n": 1}}), http.StatusAccepted)

	// Tenant B reuses the key with a different body. It must be a fresh run
	// rather than a 409, or B would learn that A holds the key.
	fresh := postWithKey(t, handlerB, runPath(workflowB.ID), "shared-key", map[string]any{"input": map[string]any{"n": 2}})
	if fresh.Code != http.StatusAccepted {
		t.Fatalf("tenant B status = %d, want 202 (body: %s)", fresh.Code, fresh.Body)
	}
	if got := fresh.Header().Get("Idempotent-Replayed"); got != "" {
		t.Errorf("tenant B saw Idempotent-Replayed = %q, want none", got)
	}
	freshResource := decodeRun(t, fresh, http.StatusAccepted)
	if freshResource.ID == first.ID {
		t.Error("tenant B replayed tenant A's execution")
	}

	// Each tenant's own replay returns its own execution.
	bReplay := decodeRun(t, postWithKey(t, handlerB, runPath(workflowB.ID), "shared-key", map[string]any{"input": map[string]any{"n": 2}}), http.StatusAccepted)
	if bReplay.ID != freshResource.ID {
		t.Errorf("tenant B replay id = %q, want %q", bReplay.ID, freshResource.ID)
	}
	aReplay := decodeRun(t, postWithKey(t, handlerA, runPath(workflowA.ID), "shared-key", map[string]any{"input": map[string]any{"n": 1}}), http.StatusAccepted)
	if aReplay.ID != first.ID {
		t.Errorf("tenant A replay id = %q, want %q", aReplay.ID, first.ID)
	}
	if got := countIdempotencyRows(t, db); got != 2 {
		t.Errorf("idempotency_keys rows = %d, want 2 (one per tenant)", got)
	}
}

func TestConcurrentRunsWithOneKeyQueueExactlyOnce(t *testing.T) {
	t.Run("a duplicate while the first is in flight is refused", func(t *testing.T) {
		db := sharedIdempotencyDB(t)
		executions := repository.NewExecutionStore(db.DB)
		entered := make(chan struct{})
		release := make(chan struct{})
		blocking := &saveBeforeExecutionStore{
			GORMExecutionStore: executions,
			before: func() error {
				close(entered)
				<-release
				return nil
			},
		}
		handler := newIdempotentAPI(t, db, repository.DefaultTenantID, blocking, nil)
		created := createWorkflow(t, handler, validManualWorkflow("In flight"))
		path := runPath(created.ID)
		body := requestBody(t, map[string]any{"input": map[string]any{"n": 1}})

		var wait sync.WaitGroup
		wait.Add(1)
		var first *httptest.ResponseRecorder
		go func() {
			defer wait.Done()
			first = postBytes(handler, path, "key-1", body)
		}()

		<-entered
		refused := postBytes(handler, path, "key-1", body)
		if refused.Code != http.StatusConflict {
			t.Fatalf("duplicate status = %d, want 409 (body: %s)", refused.Code, refused.Body)
		}
		problem := decodeIdempotencyProblem(t, refused)
		if problem.code() != "idempotency_key_in_flight" {
			t.Errorf("errors = %+v, want code idempotency_key_in_flight", problem.Errors)
		}
		if refused.Header().Get("Retry-After") == "" {
			t.Error("the in-flight refusal carried no Retry-After")
		}

		close(release)
		wait.Wait()
		if first == nil || first.Code != http.StatusAccepted {
			t.Fatalf("first request status = %v, want 202", first)
		}

		replayed := postBytes(handler, path, "key-1", body)
		if replayed.Header().Get("Idempotent-Replayed") != "true" {
			t.Errorf("Idempotent-Replayed = %q, want \"true\"", replayed.Header().Get("Idempotent-Replayed"))
		}
		if decodeRun(t, replayed, http.StatusAccepted).ID != decodeRun(t, first, http.StatusAccepted).ID {
			t.Error("the replay returned a different execution")
		}
		if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
			t.Errorf("executions = %d, want 1", got)
		}
	})

	t.Run("eight simultaneous requests queue one execution", func(t *testing.T) {
		db := sharedIdempotencyDB(t)
		executions := repository.NewExecutionStore(db.DB)
		handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
		created := createWorkflow(t, handler, validManualWorkflow("Barrier"))
		path := runPath(created.ID)
		body := requestBody(t, map[string]any{"input": map[string]any{"n": 1}})

		const racers = 8
		recorders := make([]*httptest.ResponseRecorder, racers)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index := 0; index < racers; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				recorders[index] = postBytes(handler, path, "key-1", body)
			}(index)
		}
		close(start)
		wait.Wait()

		queued := 0
		for _, recorder := range recorders {
			switch recorder.Code {
			case http.StatusAccepted:
				if recorder.Header().Get("Idempotent-Replayed") == "" {
					queued++
				}
			case http.StatusConflict:
				problem := decodeIdempotencyProblem(t, recorder)
				if problem.code() != "idempotency_key_in_flight" {
					t.Errorf("conflict errors = %+v, want code idempotency_key_in_flight", problem.Errors)
				}
			default:
				t.Errorf("unexpected status %d (body: %s)", recorder.Code, recorder.Body)
			}
		}
		if queued != 1 {
			t.Errorf("requests that queued without replaying = %d, want 1", queued)
		}
		if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
			t.Errorf("executions = %d, want 1", got)
		}
	})
}

func TestAFailedRunReleasesTheKey(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Retryable"))

	failed := postWithKey(t, handler, runPath(created.ID), "key-1", map[string]any{"triggerNodeId": "no-such-trigger"})
	if failed.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown trigger status = %d, want 422 (body: %s)", failed.Code, failed.Body)
	}

	corrected := postWithKey(t, handler, runPath(created.ID), "key-1", map[string]any{"input": map[string]any{"n": 1}})
	if corrected.Code != http.StatusAccepted {
		t.Fatalf("corrected retry status = %d, want 202 (body: %s)", corrected.Code, corrected.Body)
	}
	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}
}

func TestInsertRowWithTheSameKeyReplaysTheFirstRow(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	table := createDatastoreWithColumns(t, handler, "Metrics", [2]string{"title", "string"})
	path := "/api/v1/datastores/" + table.ID + "/rows"
	body := map[string]any{"values": map[string]any{"title": "first"}}

	first := postWithKey(t, handler, path, "key-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first insert status = %d, want 201 (body: %s)", first.Code, first.Body)
	}
	firstLocation := first.Header().Get("Location")
	var firstRow map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstRow); err != nil {
		t.Fatalf("decode first row = %v", err)
	}

	second := postWithKey(t, handler, path, "key-1", body)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201 (body: %s)", second.Code, second.Body)
	}
	if got := second.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", got)
	}
	if got := second.Header().Get("Location"); got != firstLocation {
		t.Errorf("replay Location = %q, want %q", got, firstLocation)
	}
	var replayRow map[string]any
	if err := json.Unmarshal(second.Body.Bytes(), &replayRow); err != nil {
		t.Fatalf("decode replay row = %v", err)
	}
	if rowID(firstRow) != rowID(replayRow) {
		t.Errorf("replay row id = %v, want %v", replayRow["id"], firstRow["id"])
	}
	if rows := listRows(t, handler, table.ID); len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}

	conflict := postWithKey(t, handler, path, "key-1", map[string]any{"values": map[string]any{"title": "second"}})
	if conflict.Code != http.StatusConflict {
		t.Errorf("reuse with a different body = %d, want 409 (body: %s)", conflict.Code, conflict.Body)
	}

	other := postWithKey(t, handler, path, "key-2", body)
	if other.Code != http.StatusCreated {
		t.Fatalf("second key status = %d, want 201 (body: %s)", other.Code, other.Body)
	}
	if rows := listRows(t, handler, table.ID); len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}

func TestInsertRowLargerThanTheOutcomeCapStillReplaysItsId(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	table := createDatastoreWithColumns(t, handler, "Big", [2]string{"payload", "string"})
	path := "/api/v1/datastores/" + table.ID + "/rows"
	// About 200 KB on the wire, about 1.2 MB once the server escapes each
	// angle bracket for the response: above the recorded-outcome cap, below
	// the request cap.
	body := map[string]any{"values": map[string]any{"payload": strings.Repeat("<", 200_000)}}

	first := postWithKey(t, handler, path, "key-1", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first insert status = %d, want 201 (body: %.200s)", first.Code, first.Body)
	}
	second := postWithKey(t, handler, path, "key-1", body)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201 (body: %.200s)", second.Code, second.Body)
	}
	if got := second.Header().Get("Idempotent-Replayed"); got != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", got)
	}
	if got := second.Header().Get("Location"); got != first.Header().Get("Location") {
		t.Errorf("replay Location = %q, want %q", got, first.Header().Get("Location"))
	}
	if rows := listRows(t, handler, table.ID); len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

func TestUpsertRowWithTheSameKeyReplaysTheFirstOutcome(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	table := createDatastoreWithColumns(t, handler, "Counters", [2]string{"title", "string"})
	path := "/api/v1/datastores/" + table.ID + "/rows/upsert"
	body := upsertRequest("title", "counter")

	first := postWithKey(t, handler, path, "key-1", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first upsert status = %d, want 200 (body: %s)", first.Code, first.Body)
	}
	var firstOutcome struct {
		Inserted bool             `json:"inserted"`
		Matched  int64            `json:"matched"`
		Rows     []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstOutcome); err != nil {
		t.Fatalf("decode first upsert = %v", err)
	}
	if !firstOutcome.Inserted || firstOutcome.Matched != 0 {
		t.Fatalf("first upsert = %+v, want inserted with no match", firstOutcome)
	}

	second := postWithKey(t, handler, path, "key-1", body)
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body: %s)", second.Code, second.Body)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("replay body = %s, want %s", second.Body.String(), first.Body.String())
	}

	// A keyless repeat takes the update branch, which is what makes the replay
	// observably different from a second call.
	keyless := postWithKey(t, handler, path, "", body)
	var keylessOutcome struct {
		Inserted bool  `json:"inserted"`
		Matched  int64 `json:"matched"`
	}
	if err := json.Unmarshal(keyless.Body.Bytes(), &keylessOutcome); err != nil {
		t.Fatalf("decode keyless upsert = %v", err)
	}
	if keylessOutcome.Inserted || keylessOutcome.Matched != 1 {
		t.Errorf("keyless upsert = %+v, want the update branch", keylessOutcome)
	}

	other := postWithKey(t, handler, path, "key-2", body)
	var otherOutcome struct {
		Inserted bool  `json:"inserted"`
		Matched  int64 `json:"matched"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &otherOutcome); err != nil {
		t.Fatalf("decode second-key upsert = %v", err)
	}
	if otherOutcome.Inserted {
		t.Errorf("a second key inserted again: %+v", otherOutcome)
	}
	if rows := listRows(t, handler, table.ID); len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

func TestUpsertOutcomeLargerThanTheCapReplaysCompactly(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	table := createDatastoreWithColumns(t, handler, "BigUpsert", [2]string{"payload", "string"})
	path := "/api/v1/datastores/" + table.ID + "/rows/upsert"
	body := upsertRequest("payload", strings.Repeat("<", 200_000))

	first := postWithKey(t, handler, path, "key-1", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first upsert status = %d, want 200 (body: %.200s)", first.Code, first.Body)
	}

	second := postWithKey(t, handler, path, "key-1", body)
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body: %.200s)", second.Code, second.Body)
	}
	var replay struct {
		Inserted bool             `json:"inserted"`
		Matched  int64            `json:"matched"`
		Rows     []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replay = %v", err)
	}
	if !replay.Inserted || replay.Matched != 0 || len(replay.Rows) != 0 {
		t.Errorf("compact replay = %+v, want inserted with no rows", replay)
	}
	if rows := listRows(t, handler, table.ID); len(rows) != 1 {
		t.Errorf("rows = %d, want 1", len(rows))
	}
}

func TestDatastoreKeysAreTenantScoped(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handlerA := newIdempotentAPI(t, db, "tenant-a", executions, nil)
	handlerB := newIdempotentAPI(t, db, "tenant-b", executions, nil)
	tableA := createDatastoreWithColumns(t, handlerA, "A", [2]string{"title", "string"})
	tableB := createDatastoreWithColumns(t, handlerB, "B", [2]string{"title", "string"})
	pathA := "/api/v1/datastores/" + tableA.ID + "/rows"
	pathB := "/api/v1/datastores/" + tableB.ID + "/rows"
	body := map[string]any{"values": map[string]any{"title": "shared"}}

	firstA := postWithKey(t, handlerA, pathA, "shared-key", body)
	if firstA.Code != http.StatusCreated {
		t.Fatalf("tenant A status = %d, want 201 (body: %s)", firstA.Code, firstA.Body)
	}
	firstB := postWithKey(t, handlerB, pathB, "shared-key", body)
	if firstB.Code != http.StatusCreated {
		t.Fatalf("tenant B status = %d, want 201 (body: %s)", firstB.Code, firstB.Body)
	}
	if firstB.Header().Get("Idempotent-Replayed") != "" {
		t.Error("tenant B replayed tenant A's insert")
	}
	if firstA.Header().Get("Location") == firstB.Header().Get("Location") {
		t.Error("both tenants inserted the same row")
	}

	replayA := postWithKey(t, handlerA, pathA, "shared-key", body)
	if replayA.Header().Get("Idempotent-Replayed") != "true" || replayA.Header().Get("Location") != firstA.Header().Get("Location") {
		t.Errorf("tenant A replay = %q / %q, want its own row", replayA.Header().Get("Idempotent-Replayed"), replayA.Header().Get("Location"))
	}
	if rows := listRows(t, handlerA, tableA.ID); len(rows) != 1 {
		t.Errorf("tenant A rows = %d, want 1", len(rows))
	}
	if rows := listRows(t, handlerB, tableB.ID); len(rows) != 1 {
		t.Errorf("tenant B rows = %d, want 1", len(rows))
	}
}

func TestIdempotencyKeyMustBePrintableASCIIOfAtMost255Characters(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Keys"))
	path := runPath(created.ID)
	body := map[string]any{"input": map[string]any{"n": 1}}

	for _, test := range []struct {
		name string
		key  string
	}{
		{"256 characters", strings.Repeat("k", 256)},
		{"a space", "two words"},
		{"a control character", "key\x01"},
	} {
		recorder := postWithKey(t, handler, path, test.key, body)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422 (body: %s)", test.name, recorder.Code, recorder.Body)
			continue
		}
		problem := decodeIdempotencyProblem(t, recorder)
		if len(problem.Errors) == 0 || problem.Errors[0].Location != "header.Idempotency-Key" {
			t.Errorf("%s: errors = %+v, want location header.Idempotency-Key", test.name, problem.Errors)
		}
	}

	// An empty header is indistinguishable from an absent one in the framework,
	// and means no idempotency rather than an invalid key.
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody(t, body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Errorf("empty header status = %d, want 202 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestAKeyWithoutAnIdempotencyServiceIsRefusedWith503(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentServer(t, db, repository.DefaultTenantID, executions, nil, nil, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Unprotected"))
	table := createDatastoreWithColumns(t, handler, "Rows", [2]string{"title", "string"})
	insertPath := "/api/v1/datastores/" + table.ID + "/rows"
	insertBody := map[string]any{"values": map[string]any{"title": "x"}}

	for _, test := range []struct {
		name string
		path string
		body any
	}{
		{"run", runPath(created.ID), map[string]any{"input": map[string]any{"n": 1}}},
		{"insert", insertPath, insertBody},
		{"upsert", insertPath + "/upsert", upsertRequest("title", "x")},
	} {
		recorder := postWithKey(t, handler, test.path, "key-1", test.body)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s with a key = %d, want 503 (body: %s)", test.name, recorder.Code, recorder.Body)
		}
	}

	// Without the header the routes behave exactly as before.
	if recorder := postWithKey(t, handler, runPath(created.ID), "", map[string]any{"input": map[string]any{"n": 1}}); recorder.Code != http.StatusAccepted {
		t.Errorf("unkeyed run = %d, want 202 (body: %s)", recorder.Code, recorder.Body)
	}
	if recorder := postWithKey(t, handler, insertPath, "", insertBody); recorder.Code != http.StatusCreated {
		t.Errorf("unkeyed insert = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestAStoreFailureIsA500OnBothRoutesNotA422(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	service, err := idempotency.NewService(failingIdempotencyStore{err: errors.New("the idempotency table is gone")}, idempotency.Options{Retention: time.Hour})
	if err != nil {
		t.Fatalf("idempotency.NewService() error = %v", err)
	}
	handler := newIdempotentServer(t, db, repository.DefaultTenantID, executions, nil, service, nil)
	created := createWorkflow(t, handler, validManualWorkflow("Broken store"))
	table := createDatastoreWithColumns(t, handler, "Rows", [2]string{"title", "string"})
	insertPath := "/api/v1/datastores/" + table.ID + "/rows"

	for _, test := range []struct {
		name string
		path string
		body any
	}{
		{"run", runPath(created.ID), map[string]any{"input": map[string]any{"n": 1}}},
		{"insert", insertPath, map[string]any{"values": map[string]any{"title": "x"}}},
		{"upsert", insertPath + "/upsert", upsertRequest("title", "x")},
	} {
		recorder := postWithKey(t, handler, test.path, "key-1", test.body)
		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("%s with a failing store = %d, want 500 (body: %s)", test.name, recorder.Code, recorder.Body)
		}
		if strings.Contains(recorder.Body.String(), "idempotency table is gone") {
			t.Errorf("%s leaked the cause: %s", test.name, recorder.Body)
		}
	}
}

func TestTheOpenAPIDocumentDescribesTheIdempotencyKeyHeader(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	handler := newIdempotentAPI(t, db, repository.DefaultTenantID, executions, nil)

	recorder := get(t, handler, "/api/openapi.json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json = %d, want 200", recorder.Code)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					MaxLength *int   `json:"maxLength"`
					Pattern   string `json:"pattern"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]struct {
				Headers map[string]json.RawMessage `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document = %v", err)
	}

	for _, test := range []struct {
		path    string
		method  string
		success string
	}{
		{"/api/v1/workflows/{id}/run", "post", "202"},
		{"/api/v1/datastores/{id}/rows", "post", "201"},
		{"/api/v1/datastores/{id}/rows/upsert", "post", "200"},
	} {
		operation, found := document.Paths[test.path][test.method]
		if !found {
			t.Errorf("%s %s is missing from the document", test.method, test.path)
			continue
		}
		var header *struct {
			Name   string `json:"name"`
			In     string `json:"in"`
			Schema struct {
				MaxLength *int   `json:"maxLength"`
				Pattern   string `json:"pattern"`
			} `json:"schema"`
		}
		for index := range operation.Parameters {
			if operation.Parameters[index].Name == "Idempotency-Key" {
				header = &operation.Parameters[index]
			}
		}
		if header == nil {
			t.Errorf("%s %s declares no Idempotency-Key parameter: %+v", test.method, test.path, operation.Parameters)
			continue
		}
		if header.In != "header" {
			t.Errorf("%s %s: Idempotency-Key is in %q, want header", test.method, test.path, header.In)
		}
		if header.Schema.MaxLength == nil || *header.Schema.MaxLength != 255 {
			t.Errorf("%s %s: maxLength = %v, want 255", test.method, test.path, header.Schema.MaxLength)
		}
		if header.Schema.Pattern == "" {
			t.Errorf("%s %s: Idempotency-Key has no pattern", test.method, test.path)
		}
		if _, found := operation.Responses[test.success].Headers["Idempotent-Replayed"]; !found {
			t.Errorf("%s %s: %s response declares no Idempotent-Replayed header: %+v", test.method, test.path, test.success, operation.Responses[test.success].Headers)
		}
	}

	// Cancelling an execution shares the execution-request body but is not an
	// idempotent write, so it must not gain the header.
	cancel := document.Paths["/api/v1/executions/{id}/cancel"]["post"]
	if _, found := cancel.Responses["202"].Headers["Idempotent-Replayed"]; found {
		t.Error("cancel-execution declared Idempotent-Replayed")
	}
}

func TestAnEmbedSessionRunReplaysUnderItsOwnConfinement(t *testing.T) {
	db := sharedIdempotencyDB(t)
	executions := repository.NewExecutionStore(db.DB)
	issuer := embedIssuer(t)
	service, err := idempotency.NewService(repository.NewIdempotencyStore(db.DB), idempotency.Options{Retention: time.Hour})
	if err != nil {
		t.Fatalf("idempotency.NewService() error = %v", err)
	}
	handler := newIdempotentServer(t, db, repository.DefaultTenantID, executions, nil, service, issuer)
	created := createWorkflow(t, handler, validManualWorkflow("Embedded"))
	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: created.ID,
		Scopes: []embed.Scope{embed.ScopeRun}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("issuer.Issue() error = %v", err)
	}

	post := func(path, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody(t, map[string]any{"input": map[string]any{"n": 1}})))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", hostOrigin)
		request.Header.Set("X-KilasFlow-Embed", token)
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	first := decodeRun(t, post(runPath(created.ID), "key-1"), http.StatusAccepted)
	replay := post(runPath(created.ID), "key-1")
	if replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("Idempotent-Replayed = %q, want \"true\"", replay.Header().Get("Idempotent-Replayed"))
	}
	if decodeRun(t, replay, http.StatusAccepted).ID != first.ID {
		t.Error("the embed replay queued a second execution")
	}
	if got := countExecutions(t, executions, repository.DefaultTenantID); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}

	// The confinement check runs before any key is read, so another workflow
	// is refused rather than replayed.
	if recorder := post("/api/v1/workflows/wf_someone_else/run", "key-1"); recorder.Code != http.StatusForbidden {
		t.Errorf("another workflow status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
	}
}
