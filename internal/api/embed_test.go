package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

const hostOrigin = "https://host.example"

func embedIssuer(t *testing.T) *embed.Issuer {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 11)
	}
	issuer, err := embed.NewIssuer(key, []string{hostOrigin}, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func embedRequest(t *testing.T, handler http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body = %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", hostOrigin)
	if token != "" {
		request.Header.Set("X-KilasFlow-Embed", token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestEmbedSessionCreationReturnsOnlyWhatAHostNeeds(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("Embeddable"))

	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: embedIssuer(t)})

	recorder := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		"workflowId": created.ID,
		"scopes":     []string{"workflow:read", "workflow:write"},
		"origin":     hostOrigin,
		"branding":   map[string]any{"name": "Acme Flows", "accent": "#0ea5e9"},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
	var session struct {
		Token     string   `json:"token"`
		EmbedURL  string   `json:"embedUrl"`
		Scopes    []string `json:"scopes"`
		Origin    string   `json:"origin"`
		ExpiresAt string   `json:"expiresAt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode = %v", err)
	}
	if session.Token == "" || !strings.HasPrefix(session.Token, "kfe1.") {
		t.Fatalf("token = %q, want a signed embed token", session.Token)
	}
	if session.EmbedURL != "/embed/"+created.ID || session.Origin != hostOrigin {
		t.Fatalf("session = %#v, want the embed URL and origin echoed", session)
	}
	// The host's browser must receive no workflow content and no tenant detail.
	for _, leak := range []string{"tenantId", "document", "nodes", "credentials"} {
		if strings.Contains(recorder.Body.String(), leak) {
			t.Errorf("embed session response carried %q: %s", leak, recorder.Body)
		}
	}
}

func TestEmbedSessionCreationRefusesAnUnlistedOrigin(t *testing.T) {
	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: embedIssuer(t)})

	body, _ := json.Marshal(map[string]any{
		"workflowId": "wf-1", "scopes": []string{"workflow:read"}, "origin": "https://evil.example",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestEmbedSessionCreationRefusesInjectedBranding(t *testing.T) {
	server := newTestServer(t, api.Deps{DB: stubPinger{}, EmbedIssuer: embedIssuer(t)})

	body, _ := json.Marshal(map[string]any{
		"workflowId": "wf-1", "scopes": []string{"workflow:read"}, "origin": hostOrigin,
		"branding": map[string]any{"logoUrl": "javascript:alert(1)"},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embed-sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
}

// embedServer builds one server that has both real repositories and an issuer.
func embedServer(t *testing.T) (http.Handler, *embed.Issuer, string) {
	t.Helper()
	handler, issuer, workflowID, _ := embedServerWithExecutions(t)
	return handler, issuer, workflowID
}

// embedServerWithExecutions also hands back the execution store, for the tests
// that need to write a record the API has no endpoint to create.
func embedServerWithExecutions(t *testing.T) (http.Handler, *embed.Issuer, string, *repository.GORMExecutionStore) {
	t.Helper()
	issuer := embedIssuer(t)
	handler, workflowID, executions := newWorkflowAPIWithEmbed(t, issuer)
	return handler, issuer, workflowID, executions
}

func TestEmbedTokenIsConfinedToItsWorkflow(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/workflows/"+workflowID, nil); got.Code != http.StatusOK {
		t.Fatalf("own workflow status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	// A token that leaked out of an iframe must be useless for anything but
	// the one workflow it was minted for.
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/workflows/wf_someone_else", nil); got.Code != http.StatusForbidden {
		t.Errorf("other workflow status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/workflows", nil); got.Code != http.StatusForbidden {
		t.Errorf("workflow list status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/embed-sessions", map[string]any{
		"workflowId": workflowID, "scopes": []string{"workflow:write"}, "origin": hostOrigin,
	}); got.Code != http.StatusForbidden {
		t.Errorf("minting another session status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodDelete, "/api/v1/workflows/"+workflowID, nil); got.Code != http.StatusForbidden {
		t.Errorf("delete status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil); got.Code != http.StatusForbidden {
		t.Errorf("activate status = %d, want 403", got.Code)
	}
}

func TestAReadOnlyEmbedSessionCannotWriteOrRun(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/workflows/"+workflowID, nil); got.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	if got := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(validManualWorkflow("Renamed"))); got.Code != http.StatusForbidden {
		t.Errorf("write status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", nil); got.Code != http.StatusForbidden {
		t.Errorf("run status = %d, want 403", got.Code)
	}
}

func TestAWriteScopedEmbedSessionCanSaveButNotRun(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeWrite}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if got := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(validManualWorkflow("Saved from embed"))); got.Code != http.StatusOK {
		t.Fatalf("write status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", nil); got.Code != http.StatusForbidden {
		t.Errorf("run status = %d, want 403", got.Code)
	}
}

func TestAnEmbedTokenIsRejectedFromAnotherOrigin(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	// The origin is re-checked on every request, not only at mint time.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+workflowID, nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("X-KilasFlow-Embed", token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 from a different origin (body: %s)", recorder.Code, recorder.Body)
	}
}

func TestAnExpiredOrForgedEmbedTokenIsRejected(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)

	past := time.Now().UTC().Add(-time.Hour)
	expiredIssuer, _ := embed.NewIssuer(make([]byte, 32), []string{hostOrigin}, func() time.Time { return past })
	_, expired, _ := expiredIssuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin, Lifetime: time.Minute,
	})

	for name, token := range map[string]string{
		"expired":   expired,
		"forged":    "kfe1.eyJ3aWQiOiJ3Zi0xIn0.AAAA",
		"malformed": "kfe1.garbage",
	} {
		got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/workflows/"+workflowID, nil)
		if got.Code != http.StatusUnauthorized {
			t.Errorf("%s token status = %d, want 401 (body: %s)", name, got.Code, got.Body)
		}
	}
	_ = issuer
}

func TestARequestWithoutAnEmbedTokenIsUnaffected(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	// The internal dashboard must keep working with the middleware mounted.
	got := embedRequest(t, handler, "", http.MethodGet, "/api/v1/workflows", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("dashboard list status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	if got := embedRequest(t, handler, "", http.MethodGet, "/api/v1/workflows/"+workflowID, nil); got.Code != http.StatusOK {
		t.Errorf("dashboard read status = %d, want 200", got.Code)
	}
}

func TestAnEmbedSessionCanReadTheNodeCatalogueAndItsExecutions(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "standalone", WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	// The editor cannot render without the node catalogue.
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/node-types", nil); got.Code != http.StatusOK {
		t.Errorf("node types status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions?workflowId="+workflowID, nil); got.Code != http.StatusOK {
		t.Errorf("executions status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	// Credential values are never returned by this endpoint, only names.
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "sneaky", "type": "httpBearerAuth", "fields": map[string]string{"token": "x"},
	}); got.Code != http.StatusForbidden {
		t.Errorf("credential create status = %d, want 403", got.Code)
	}
}

func TestAnEmbedSessionCannotReachAnotherWorkflowsExecutions(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)

	// A second workflow with its own execution, owned by the same tenant. The
	// embed session below is scoped to the first one only.
	other := createWorkflow(t, handler, validManualWorkflow("Not embedded"))
	otherExecution := requestJSON[executionRequestResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+other.ID+"/run", nil, http.StatusAccepted)

	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	// Reading another workflow's execution by ID must not be possible: the
	// record carries that run's full input, output, and node trace.
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions/"+otherExecution.ID, nil); got.Code != http.StatusNotFound {
		t.Errorf("other execution status = %d, want 404 (body: %s)", got.Code, got.Body)
	}
	// Cancelling one is worse still.
	if got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/executions/"+otherExecution.ID+"/cancel", nil); got.Code == http.StatusAccepted {
		t.Errorf("an embed session cancelled another workflow's execution: %s", got.Body)
	}
	// An unfiltered listing would page through the whole tenant's history.
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions", nil); got.Code != http.StatusForbidden {
		t.Errorf("unfiltered list status = %d, want 403", got.Code)
	}
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions?workflowId="+other.ID, nil); got.Code != http.StatusForbidden {
		t.Errorf("other workflow list status = %d, want 403", got.Code)
	}
	// Its own workflow's history stays readable.
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions?workflowId="+workflowID, nil); got.Code != http.StatusOK {
		t.Errorf("own list status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
}

func TestAnEmbedSessionCannotStreamAnotherWorkflowsEvents(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)

	other := createWorkflow(t, handler, validManualWorkflow("Not embedded"))
	otherExecution := requestJSON[executionRequestResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+other.ID+"/run", nil, http.StatusAccepted)

	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions/"+otherExecution.ID+"/events", nil)
	// The stream opens (SSE always does) but must carry no event for a run the
	// session does not own.
	if strings.Contains(got.Body.String(), "event:") {
		t.Fatalf("an embed session streamed another workflow's events: %s", got.Body)
	}
}

func TestAnEmbedSessionCanReadTheSubWorkflowRunsItStarted(t *testing.T) {
	handler, issuer, workflowID, executions := embedServerWithExecutions(t)
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}
	ctx := context.Background()

	// The parent is the session's own workflow; the child belongs to a
	// different one, which the plain ownership check would hide — leaving an
	// embedded editor showing a parent that succeeded with an invisible child
	// and no way to see why it failed.
	parent := requestJSON[executionRequestResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+workflowID+"/run", nil, http.StatusAccepted)
	callee := createWorkflow(t, handler, validManualWorkflow("Sub-workflow"))
	child, err := executions.Create(ctx, tenant, execution.Record{
		WorkflowID: callee.ID, WorkflowVersionID: callee.LatestVersion.ID,
		Status: execution.StatusSucceeded, Trigger: execution.TriggerSubworkflow,
		ParentExecutionID: parent.ID, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: workflowID,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions/"+child.ID, nil); got.Code != http.StatusOK {
		t.Fatalf("child execution status = %d, want 200 (body: %s)", got.Code, got.Body)
	}

	// The boundary still holds for a run of the same other workflow that this
	// session did not start. Ancestry, not workflow identity, is what opened
	// the door.
	unrelated, err := executions.Create(ctx, tenant, execution.Record{
		WorkflowID: callee.ID, WorkflowVersionID: callee.LatestVersion.ID,
		Status: execution.StatusSucceeded, Trigger: execution.TriggerManual, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/executions/"+unrelated.ID, nil); got.Code != http.StatusNotFound {
		t.Errorf("unrelated execution status = %d, want 404 (body: %s)", got.Code, got.Body)
	}
}
