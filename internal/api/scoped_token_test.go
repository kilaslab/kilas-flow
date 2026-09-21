package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// These tests are the acceptance criterion read off the real surface: a scoped
// key minted through the real endpoint, a request sent through the real
// middleware chain, and the refusal read off the real response. A unit test of
// the gate would prove the table; only this proves the table is mounted where
// the requests actually go.

// mintScopedKey mints an agent token the way an operator would: through the
// API, with the tenant's own key.
func mintScopedKey(t *testing.T, server *authenticatedAPI, tenantKey, label string, body map[string]any) string {
	t.Helper()
	body["label"] = label
	created := server.callJSON(t, http.MethodPost, "/api/v1/api-keys", tenantKey, body, http.StatusCreated)
	token, _ := created["token"].(string)
	if token == "" {
		t.Fatalf("minting %q returned no token: %v", label, created)
	}
	return token
}

func TestAScopedKeyIsRefusedActivationOnTheRealSurface(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	tenantKey := keys["tenant-a"]
	workflowID := server.createWorkflowAs(t, tenantKey, "Scoped target")

	scoped := mintScopedKey(t, server, tenantKey, "agent", map[string]any{
		"scopes": []string{"workflow:read", "workflow:write", "workflow:run"},
	})

	// The scope list it was minted with is what it can read back about itself,
	// which is how the CLI learns its own authority.
	me := server.callJSON(t, http.MethodGet, "/api/v1/auth/me", scoped, nil, http.StatusOK)
	if me["kind"] != "api_key" || me["keyId"] == "" {
		t.Errorf("auth/me = %v, want an api_key principal", me)
	}
	scopes, _ := me["scopes"].([]any)
	if len(scopes) != 3 {
		t.Errorf("auth/me scopes = %v, want the three the key was minted with", me["scopes"])
	}

	// Reading is what the key is for, so it must work.
	server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID, scoped, nil, http.StatusOK)

	// Activation publishes a public endpoint, and the refusal names it.
	recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", scoped, nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("activate status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "cannot change activation") {
		t.Errorf("activate refusal = %s, want it to name the refusal", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "problem+json") {
		t.Errorf("Content-Type = %q, want a problem document", contentType)
	}

	// Deleting and importing are refused with their own reasons, and the key
	// management surface is not an agent's at all.
	for _, refused := range []struct {
		method string
		path   string
		body   any
		reason string
	}{
		{http.MethodDelete, "/api/v1/workflows/" + workflowID, nil, "cannot delete a workflow"},
		{http.MethodPost, "/api/v1/workflows/import", map[string]any{}, "cannot import workflows"},
		{http.MethodPost, "/api/v1/api-keys", map[string]any{"label": "escalation"}, "cannot use this endpoint"},
		{http.MethodGet, "/api/v1/tenants", nil, "cannot use this endpoint"},
		{http.MethodPost, "/api/v1/embed-sessions", map[string]any{}, "cannot use this endpoint"},
	} {
		recorder := server.call(t, refused.method, refused.path, scoped, refused.body)
		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403 (body: %s)", refused.method, refused.path, recorder.Code, recorder.Body)
			continue
		}
		if body := recorder.Body.String(); !strings.Contains(body, refused.reason) {
			t.Errorf("%s %s refusal = %s, want it to name %q", refused.method, refused.path, body, refused.reason)
		}
	}

	// The tenant's own key still activates: the refusals apply to scoped keys,
	// which is what keeps an operator's existing automation working.
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", tenantKey, nil, http.StatusOK)
}

func TestAWorkflowBoundKeyReadsAnotherWorkflowAsMissing(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	tenantKey := keys["tenant-a"]
	mine := server.createWorkflowAs(t, tenantKey, "Mine")
	theirs := server.createWorkflowAs(t, tenantKey, "Theirs")

	bound := mintScopedKey(t, server, tenantKey, "bound agent", map[string]any{
		"scopes": []string{"workflow:read"}, "workflowId": mine,
	})

	server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+mine, bound, nil, http.StatusOK)

	recorder := server.call(t, http.MethodGet, "/api/v1/workflows/"+theirs, bound, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("reading another workflow status = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
	// 404 rather than 403 is the point: a refusal that said "you may not"
	// would confirm the id is real.
	var problem map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode refusal: %v (body: %s)", err, recorder.Body)
	}
	if status, _ := problem["status"].(float64); status != 404 {
		t.Errorf("problem status = %v, want 404", problem["status"])
	}
	if detail, _ := problem["detail"].(string); !strings.Contains(detail, "not found") {
		t.Errorf("problem detail = %q, want it to read as a miss", detail)
	}

	// A listing of another workflow's executions is refused with a reason
	// rather than a miss: the path names no workflow, the query does.
	recorder = server.call(t, http.MethodGet, "/api/v1/executions?workflowId="+theirs, bound, nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("listing another workflow's executions status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "own workflow") {
		t.Errorf("listing refusal = %s, want it to name the binding", body)
	}
}

// A read-only token cannot write, and the refusal is the scope's rather than
// the route's: the same path answers 200 for a key that holds workflow:write.
func TestAScopedKeysReadScopeIsNotAWriteScope(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	tenantKey := keys["tenant-a"]
	workflowID := server.createWorkflowAs(t, tenantKey, "Read only target")

	reader := mintScopedKey(t, server, tenantKey, "reader", map[string]any{"scopes": []string{"workflow:read"}})
	writer := mintScopedKey(t, server, tenantKey, "writer", map[string]any{"scopes": []string{"workflow:write"}})

	document := workflowDraft(validManualWorkflow("Renamed by the writer"))

	recorder := server.call(t, http.MethodPut, "/api/v1/workflows/"+workflowID, reader, document)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("reader update status = %d, want 403 (body: %s)", recorder.Code, recorder.Body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "read-only") {
		t.Errorf("reader refusal = %s, want it to name the read-only scope", body)
	}

	server.callJSON(t, http.MethodPut, "/api/v1/workflows/"+workflowID, writer, document, http.StatusOK)
}
