package handlers

// Resume HTTP surface proof: distinct refusals (unknown 404, answered 409,
// expired 410, embed-denied 403), a second call refused with its own message,
// and an info surface that never serves the checkpoint or the token.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

type stubResumeService struct {
	info      repository.Wait
	infoErr   error
	wait      repository.Wait
	record    execution.Record
	resumeErr error
	calls     int
	decision  engine.ApprovalDecision
}

func (stub *stubResumeService) WaitInfo(context.Context, string, string) (repository.Wait, error) {
	return stub.info, stub.infoErr
}

func (stub *stubResumeService) ResumeApproval(_ context.Context, _, _ string, decision engine.ApprovalDecision, _ bool) (repository.Wait, execution.Record, error) {
	stub.calls++
	stub.decision = decision
	return stub.wait, stub.record, stub.resumeErr
}

func (stub *stubResumeService) ResumeCall(context.Context, string, string, json.RawMessage, bool) (repository.Wait, execution.Record, error) {
	stub.calls++
	return stub.wait, stub.record, stub.resumeErr
}

func resumeTestWait() repository.Wait {
	return repository.Wait{
		TenantID: "default", ExecutionID: "exec_1", WorkflowID: "wf_1",
		NodeID: "hold", Mode: engine.WaitModeApproval, ResumeToken: "token-1",
		Checkpoint: json.RawMessage(`{"version":1}`), ExpiresAt: time.Now().Add(time.Hour),
	}
}

func resumeServe(handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeRefusal(t *testing.T, recorder *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode refusal: %v (%q)", err, recorder.Body.String())
	}
	return recorder.Code, problem
}

func TestResumeInfoServesLinksWithoutRunData(t *testing.T) {
	stub := &stubResumeService{info: resumeTestWait()}
	handler := NewResume(stub, nil).Handler()
	recorder := resumeServe(handler, http.MethodGet, "/resume/token-1", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	var info map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode info: %v", err)
	}
	for _, key := range []string{"executionId", "workflowId", "nodeId", "mode", "expiresAt"} {
		if _, found := info[key]; !found {
			t.Errorf("info lacks %q: %v", key, info)
		}
	}
	// The token travels in the URL and the checkpoint never leaves storage:
	// neither may appear in the body under any spelling.
	lowered := strings.ToLower(recorder.Body.String())
	for _, forbidden := range []string{"token-1", "checkpoint", "version"} {
		if strings.Contains(lowered, forbidden) {
			t.Errorf("info body leaks %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestResumeRefusalsAreDistinctAndStable(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	now := time.Now()
	cases := []struct {
		name       string
		info       repository.Wait
		infoErr    error
		method     string
		wantStatus int
		wantCode   string
	}{
		{"unknown info", repository.Wait{}, repository.ErrWaitNotFound, http.MethodGet, http.StatusNotFound, "wait.not_found"},
		{"unknown resume", repository.Wait{}, repository.ErrWaitNotFound, http.MethodPost, http.StatusNotFound, "wait.not_found"},
		{"answered info", repository.Wait{ConsumedAt: &now}, nil, http.MethodGet, http.StatusConflict, "wait.answered"},
		{"expired info", repository.Wait{ExpiresAt: past}, nil, http.MethodGet, http.StatusGone, "wait.expired"},
		{"answered resume", repository.Wait{Mode: engine.WaitModeApproval}, nil, http.MethodPost, http.StatusConflict, "wait.answered"},
		{"expired resume", repository.Wait{Mode: engine.WaitModeApproval}, nil, http.MethodPost, http.StatusGone, "wait.expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubResumeService{info: tc.info, infoErr: tc.infoErr}
			if tc.method == http.MethodPost {
				switch tc.wantCode {
				case "wait.answered":
					stub.resumeErr = repository.ErrWaitConsumed
				case "wait.expired":
					stub.resumeErr = repository.ErrWaitExpired
				}
				stub.wait = tc.info
			}
			handler := NewResume(stub, nil).Handler()
			body := ""
			if tc.method == http.MethodPost {
				body = `{"approved":true,"decidedBy":"tester"}`
			}
			first := resumeServe(handler, tc.method, "/resume/token-1", body)
			status, problem := decodeRefusal(t, first)
			if status != tc.wantStatus || problem["code"] != tc.wantCode {
				t.Fatalf("status/code = (%d, %v), want (%d, %q)", status, problem["code"], tc.wantStatus, tc.wantCode)
			}
			// Stable: asking again gets the same answer, not the next error.
			second := resumeServe(handler, tc.method, "/resume/token-1", body)
			restatus, reproblem := decodeRefusal(t, second)
			if restatus != status || reproblem["code"] != problem["code"] {
				t.Fatalf("repeat = (%d, %v), want the same (%d, %v)", restatus, reproblem["code"], status, problem["code"])
			}
		})
	}
}

func TestResumeApprovalRecordsTheDecision(t *testing.T) {
	stub := &stubResumeService{
		info:   resumeTestWait(),
		wait:   resumeTestWait(),
		record: execution.Record{ID: "exec_1", Status: execution.StatusQueued},
	}
	handler := NewResume(stub, nil).Handler()
	recorder := resumeServe(handler, http.MethodPost, "/resume/token-1", `{"approved":true,"decidedBy":"tester","note":"ok"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("resume calls = %d, want 1", stub.calls)
	}
	if !stub.decision.Approved || stub.decision.DecidedBy != "tester" || stub.decision.Note != "ok" {
		t.Errorf("decision = %+v, want the posted approval", stub.decision)
	}
	if stub.decision.RespondedAt.IsZero() {
		t.Error("decision carries no timestamp")
	}
}

func TestResumeDeniedToEmbedSessionsBeforeConsuming(t *testing.T) {
	stub := &stubResumeService{
		info:   resumeTestWait(),
		wait:   resumeTestWait(),
		record: execution.Record{ID: "exec_1", Status: execution.StatusQueued},
	}
	handler := NewResume(stub, nil).Handler()
	issuer, err := embed.NewIssuer([]byte("0123456789abcdef0123456789abcdef"), []string{"https://host.example"}, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "default", WorkflowID: "wf_1",
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: "https://host.example",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	stack := middleware.EmbedAuth(issuer, "")(handler)
	request := httptest.NewRequest(http.MethodPost, "/resume/token-1", strings.NewReader(`{"approved":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-KilasFlow-Embed", token)
	recorder := httptest.NewRecorder()
	stack.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("embed POST status = %d, want 403 (%s)", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 0 {
		t.Fatalf("resume calls = %d, want none: a denied call must never consume", stub.calls)
	}
}

func TestWaitingStatusFlowsThroughListAndStream(t *testing.T) {
	if status, ok := parseExecutionStatus("waiting"); !ok || status != execution.StatusWaiting {
		t.Fatalf("parseExecutionStatus(waiting) = (%q, %v), want (waiting, true)", status, ok)
	}
	if _, ok := parseExecutionStatus("suspended"); ok {
		t.Error("parseExecutionStatus(suspended) = true, want false")
	}
	event, ok := typedEvent(ExecutionEvent{ExecutionID: "exec_1"}, engine.EventExecutionWaiting).(ExecutionWaitingEvent)
	if !ok {
		t.Fatalf("typedEvent(waiting) = %T, want ExecutionWaitingEvent", typedEvent(ExecutionEvent{}, engine.EventExecutionWaiting))
	}
	if event.ExecutionID != "exec_1" {
		t.Errorf("typed event lost the execution ID: %+v", event)
	}
	if engine.EventExecutionWaiting.Terminal() {
		t.Error("execution.waiting is terminal: the live feed would close across the wait")
	}
	if !events.ExecutionCancelled.Terminal() {
		t.Error("sanity: execution.cancelled must stay terminal")
	}
}
