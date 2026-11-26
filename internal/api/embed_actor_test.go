package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// mintEmbedSessionForActor issues an embed token under one tenant's key with
// the given actor, or none when actor is nil.
func mintEmbedSessionForActor(t *testing.T, server *authenticatedAPI, key, workflowID string, actor map[string]any, scopes ...string) string {
	t.Helper()
	body := map[string]any{"workflowId": workflowID, "scopes": scopes, "origin": embedOrigin}
	if actor != nil {
		body["actor"] = actor
	}
	minted := server.callJSON(t, http.MethodPost, "/api/v1/embed-sessions", key, body, http.StatusCreated)
	token, _ := minted["token"].(string)
	if token == "" {
		t.Fatalf("no token in %#v", minted)
	}
	return token
}

// embedActorFields reads the four actor fields of one decoded resource.
func embedActorFields(resource map[string]any) (kind, label, keyID, id string) {
	kind, _ = resource["actorKind"].(string)
	label, _ = resource["actorLabel"].(string)
	keyID, _ = resource["actorKeyId"].(string)
	id, _ = resource["actorId"].(string)
	return kind, label, keyID, id
}

func expectEmbedActor(t *testing.T, where string, resource map[string]any, kind, label, id string) {
	t.Helper()
	gotKind, gotLabel, gotKeyID, gotID := embedActorFields(resource)
	if gotKind != kind || gotLabel != label || gotID != id || gotKeyID != "" {
		t.Errorf("%s actor = (%q, %q, key %q, %q), want (%q, %q, no key, %q)", where,
			gotKind, gotLabel, gotKeyID, gotID, kind, label, id)
	}
}

// The whole path a host relies on: the actor it names at minting is signed
// into the token, and every write and run the session causes is attributed to
// it in what the tenant's key reads back.
func TestAnEmbedSessionsActorIsRecordedOnEverythingItDoes(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	key := keys["globex"]

	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", key,
		workflowDraft(crmWorkflow("Attributed", sharedPingType)), http.StatusCreated)
	workflowID, _ := created["id"].(string)

	token := mintEmbedSessionForActor(t, server, key, workflowID,
		map[string]any{"id": "auth0|ada", "displayName": "Ada Lovelace"},
		"workflow:read", "workflow:write", "workflow:run", "workflow:publish")

	saved := embedRequest(t, server.handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(crmWorkflow("Attributed, edited", sharedPingType)))
	if saved.Code != http.StatusOK {
		t.Fatalf("embed save = %d, want 200 (body: %s)", saved.Code, saved.Body)
	}
	var savedWorkflow map[string]any
	if err := json.Unmarshal(saved.Body.Bytes(), &savedWorkflow); err != nil {
		t.Fatalf("decode save: %v", err)
	}
	latest, _ := savedWorkflow["latestVersion"].(map[string]any)
	versionID, _ := latest["id"].(string)

	published := embedRequest(t, server.handler, token, http.MethodPost,
		"/api/v1/workflows/"+workflowID+"/versions/"+versionID+"/publish", map[string]any{})
	if published.Code != http.StatusOK {
		t.Fatalf("embed publish = %d, want 200 (body: %s)", published.Code, published.Body)
	}

	run := embedRequest(t, server.handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", map[string]any{})
	if run.Code != http.StatusAccepted {
		t.Fatalf("embed run = %d, want 202 (body: %s)", run.Code, run.Body)
	}
	var queued map[string]any
	if err := json.Unmarshal(run.Body.Bytes(), &queued); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	executionID, _ := queued["id"].(string)

	versions := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID+"/versions", key, nil, http.StatusOK)
	items, _ := versions["items"].([]any)
	if len(items) == 0 {
		t.Fatal("versions = none")
	}
	newest, _ := items[0].(map[string]any)
	expectEmbedActor(t, "newest revision", newest, "embed", "Ada Lovelace", "auth0|ada")
	if newest["createdBy"] != "Ada Lovelace" {
		t.Errorf("newest revision createdBy = %v, want the display name", newest["createdBy"])
	}

	events := server.callArray(t, "/api/v1/workflows/"+workflowID+"/publish-events", key)
	if len(events) == 0 {
		t.Fatal("publish events = none")
	}
	event, _ := events[0].(map[string]any)
	expectEmbedActor(t, "publish event", event, "embed", "Ada Lovelace", "auth0|ada")

	detail := server.callJSON(t, http.MethodGet, "/api/v1/executions/"+executionID, key, nil, http.StatusOK)
	expectEmbedActor(t, "execution detail", detail, "embed", "Ada Lovelace", "auth0|ada")

	listed := server.callJSON(t, http.MethodGet, "/api/v1/executions?workflowId="+workflowID, key, nil, http.StatusOK)
	rows, _ := listed["items"].([]any)
	if len(rows) == 0 {
		t.Fatal("executions = none")
	}
	row, _ := rows[0].(map[string]any)
	expectEmbedActor(t, "execution summary", row, "embed", "Ada Lovelace", "auth0|ada")

	// The tenant's own key, running the same workflow, is recorded as itself.
	keyRun := server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", key, map[string]any{}, http.StatusAccepted)
	keyRunID, _ := keyRun["id"].(string)
	keyDetail := server.callJSON(t, http.MethodGet, "/api/v1/executions/"+keyRunID, key, nil, http.StatusOK)
	kind, _, keyID, id := embedActorFields(keyDetail)
	if kind != "key" || keyID == "" || id != "" {
		t.Errorf("key run actor = (%q, key %q, id %q), want key with its key id and no host id", kind, keyID, id)
	}
}

// A session minted without an actor still records that an embedded editor
// acted, and invents no name for it.
func TestAnUnnamedEmbedSessionIsRecordedAsEmbedWithoutALabel(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	key := keys["globex"]

	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", key,
		workflowDraft(crmWorkflow("Unnamed", sharedPingType)), http.StatusCreated)
	workflowID, _ := created["id"].(string)
	token := mintEmbedSessionForActor(t, server, key, workflowID, nil, "workflow:read", "workflow:write")

	saved := embedRequest(t, server.handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(crmWorkflow("Unnamed, edited", sharedPingType)))
	if saved.Code != http.StatusOK {
		t.Fatalf("embed save = %d, want 200 (body: %s)", saved.Code, saved.Body)
	}
	versions := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+workflowID+"/versions", key, nil, http.StatusOK)
	items, _ := versions["items"].([]any)
	newest, _ := items[0].(map[string]any)
	expectEmbedActor(t, "unnamed revision", newest, "embed", "", "")
	if _, present := newest["createdBy"]; present {
		t.Errorf("unnamed revision createdBy = %v, want absent", newest["createdBy"])
	}
}

// A malformed actor is refused at minting with the reason, never signed.
func TestMintingRefusesAMalformedActor(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	key := keys["globex"]
	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", key,
		workflowDraft(crmWorkflow("Refused", sharedPingType)), http.StatusCreated)
	workflowID, _ := created["id"].(string)

	for name, actor := range map[string]map[string]any{
		"markup name":  {"id": "u1", "displayName": "<b>x</b>"},
		"long name":    {"id": "u1", "displayName": strings.Repeat("a", 65)},
		"spaced id":    {"id": "user 1"},
		"empty id":     {"id": ""},
		"oversized id": {"id": strings.Repeat("a", 129)},
	} {
		recorder := server.call(t, http.MethodPost, "/api/v1/embed-sessions", key, map[string]any{
			"workflowId": workflowID, "scopes": []string{"workflow:read"}, "origin": embedOrigin, "actor": actor,
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: mint = %d, want 422 (body: %s)", name, recorder.Code, recorder.Body)
			continue
		}
		if !strings.Contains(recorder.Body.String(), "actor") {
			t.Errorf("%s: refusal = %s, want it to name the actor", name, recorder.Body)
		}
	}
}
