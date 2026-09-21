package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// auditCall sends one request carrying an API key and any extra headers, which
// is what an agent's mutating call looks like.
func auditCall(t *testing.T, server *authenticatedAPI, method, path, key string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var contents *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		contents = bytes.NewReader(encoded)
	} else {
		contents = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, contents)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

// auditCallWithCookie is the same request authenticated by a browser session
// rather than a key, which is the other half of the actor vocabulary.
func auditCallWithCookie(t *testing.T, server *authenticatedAPI, method, path string, cookie *http.Cookie, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

// firstVersion reads the newest row of a workflow's revision listing.
func firstVersion(t *testing.T, server *authenticatedAPI, key, workflowID string) map[string]any {
	t.Helper()
	listed := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID+"/versions", key, nil, http.StatusOK)
	items, _ := listed["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("workflow %s has no revisions", workflowID)
	}
	first, _ := items[0].(map[string]any)
	return first
}

// allVersions reads every row of a workflow's revision listing, newest first.
func allVersions(t *testing.T, server *authenticatedAPI, key, workflowID string) []any {
	t.Helper()
	listed := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID+"/versions", key, nil, http.StatusOK)
	items, _ := listed["items"].([]any)
	return items
}

// The audit promise, end to end: a write made with an API key is attributed to
// that key, the skills the caller reported riding along with it.
func TestARevisionSavedByAnAPIKeyIsAttributedToTheKey(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Audited")

	keysListed, err := server.store.ListAPIKeys(context.Background(), repository.TenantScope{ID: "acme"})
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	if len(keysListed) != 1 {
		t.Fatalf("acme has %d keys, want 1", len(keysListed))
	}
	keyID, keyLabel := keysListed[0].ID, keysListed[0].Label

	recorder := auditCall(t, server, http.MethodPut, "/api/v1/workflows/"+workflowID, keys["acme"],
		workflowDraft(validManualWorkflow("Audited")),
		map[string]string{"X-KilasFlow-Skills-Used": "kilasflow-debugging, kilasflow-expressions"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("saving as a key = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}

	saved := firstVersion(t, server, keys["acme"], workflowID)
	if got := saved["actorKind"]; got != "key" {
		t.Errorf("actorKind = %v, want key", got)
	}
	if got := saved["actorLabel"]; got != keyLabel {
		t.Errorf("actorLabel = %v, want %q", got, keyLabel)
	}
	if got := saved["actorKeyId"]; got != keyID {
		t.Errorf("actorKeyId = %v, want %q", got, keyID)
	}
	meta, _ := saved["actorMeta"].([]any)
	if len(meta) != 2 || meta[0] != "kilasflow-debugging" || meta[1] != "kilasflow-expressions" {
		t.Errorf("actorMeta = %#v, want the two skills the caller reported", saved["actorMeta"])
	}

	// The key's own creation of the workflow carries the same attribution, and
	// no skills, because that request reported none.
	created := allVersions(t, server, keys["acme"], workflowID)
	oldest, _ := created[len(created)-1].(map[string]any)
	if got := oldest["actorKind"]; got != "key" {
		t.Errorf("actorKind of the created revision = %v, want key", got)
	}
	if _, present := oldest["actorMeta"]; present {
		t.Errorf("actorMeta of a write that reported nothing = %v, want absent", oldest["actorMeta"])
	}
}

// The other kind: a person's browser session is a user, not a key, and the
// session presents no key identifier to record.
func TestARevisionSavedByASessionIsAttributedToTheUser(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Audited")

	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if _, err := server.store.CreateUser(context.Background(),
		repository.TenantScope{ID: "acme"}, "owner@acme.example", "Owner", hash); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	login := server.call(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "owner@acme.example", "password": "hunter2",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200 (body: %s)", login.Code, login.Body)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login set %d cookies, want 1", len(cookies))
	}

	recorder := auditCallWithCookie(t, server, http.MethodPut, "/api/v1/workflows/"+workflowID,
		cookies[0], workflowDraft(validManualWorkflow("Audited")), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("saving as a session = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}

	saved := firstVersion(t, server, keys["acme"], workflowID)
	if got := saved["actorKind"]; got != "user" {
		t.Errorf("actorKind = %v, want user", got)
	}
	if got := saved["actorLabel"]; got != "owner@acme.example" {
		t.Errorf("actorLabel = %v, want the signed-in address", got)
	}
	if _, present := saved["actorKeyId"]; present {
		t.Errorf("actorKeyId of a session's save = %v, want absent", saved["actorKeyId"])
	}
}

// A publish is its own audit row, and it carries the actor too: who activated a
// workflow is the fact an incident review asks for first.
func TestAPublishIsAttributedToTheKeyThatActivated(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Audited")

	keysListed, err := server.store.ListAPIKeys(context.Background(), repository.TenantScope{ID: "acme"})
	if err != nil {
		t.Fatalf("ListAPIKeys() error = %v", err)
	}
	keyID, keyLabel := keysListed[0].ID, keysListed[0].Label

	if recorder := auditCall(t, server, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate",
		keys["acme"], nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("activating = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}

	events := server.callArray(t, "/api/v1/workflows/"+workflowID+"/publish-events", keys["acme"])
	if len(events) != 1 {
		t.Fatalf("publish events = %d, want 1: %#v", len(events), events)
	}
	event, _ := events[0].(map[string]any)
	if got := event["action"]; got != "published" {
		t.Fatalf("action = %v, want published", got)
	}
	if got := event["actorKind"]; got != "key" {
		t.Errorf("actorKind = %v, want key", got)
	}
	if got := event["actorLabel"]; got != keyLabel {
		t.Errorf("actorLabel = %v, want %q", got, keyLabel)
	}
	if got := event["actorKeyId"]; got != keyID {
		t.Errorf("actorKeyId = %v, want %q", got, keyID)
	}
}
