package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/embed"
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
	issuer := embedIssuer(t)
	handler, workflowID := newWorkflowAPIWithEmbed(t, issuer)
	return handler, issuer, workflowID
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
