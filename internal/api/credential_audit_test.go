package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// credentialEventsOf reads a credential's audit trail, newest first.
func credentialEventsOf(t *testing.T, server *authenticatedAPI, tenantID, credentialID string) []repository.CredentialEvent {
	t.Helper()
	events, err := server.credentials.CredentialEvents(context.Background(), repository.TenantScope{ID: tenantID}, credentialID)
	if err != nil {
		t.Fatalf("CredentialEvents() error = %v", err)
	}
	return events
}

func eventActions(events []repository.CredentialEvent) []string {
	actions := make([]string, 0, len(events))
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	return actions
}

func createdCredentialID(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create credential = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode the created credential: %v (body: %s)", err, recorder.Body)
	}
	return created.ID
}

// Every credential operation an API key performs is on the trail with the
// key's name and id: create, update, both kinds of test that spend the stored
// secret, and delete — the last one still readable after the row is gone.
func TestCredentialWritesAreAttributedToTheKey(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	key := keys["acme"]
	listed, err := server.store.ListAPIKeys(context.Background(), repository.TenantScope{ID: "acme"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListAPIKeys() = %d keys, %v", len(listed), err)
	}
	keyID, keyLabel := listed[0].ID, listed[0].Label

	credentialID := createdCredentialID(t, auditCall(t, server, http.MethodPost, "/api/v1/credentials", key, map[string]any{
		"name": "Billing API", "type": "httpBearerAuth", "fields": map[string]string{"token": "secret-token"},
	}, nil))
	if recorder := auditCall(t, server, http.MethodPut, "/api/v1/credentials/"+credentialID, key, map[string]any{
		"name": "Billing API (prod)", "fields": map[string]string{},
	}, nil); recorder.Code != http.StatusOK {
		t.Fatalf("update = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if recorder := auditCall(t, server, http.MethodPost, "/api/v1/credentials/"+credentialID+"/test", key, nil, nil); recorder.Code != http.StatusOK {
		t.Fatalf("test = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if recorder := auditCall(t, server, http.MethodPost, "/api/v1/credential-types/httpBearerAuth/test", key, map[string]any{
		"credentialId": credentialID, "fields": map[string]string{"token": credentials.RedactedValue},
	}, nil); recorder.Code != http.StatusOK {
		t.Fatalf("payload test = %d (body: %s)", recorder.Code, recorder.Body)
	}
	// A payload test that names no stored credential spends no stored secret
	// and is not on any credential's trail.
	if recorder := auditCall(t, server, http.MethodPost, "/api/v1/credential-types/httpBearerAuth/test", key, map[string]any{
		"fields": map[string]string{"token": "typed-in"},
	}, nil); recorder.Code != http.StatusOK {
		t.Fatalf("unsaved payload test = %d (body: %s)", recorder.Code, recorder.Body)
	}
	if recorder := auditCall(t, server, http.MethodDelete, "/api/v1/credentials/"+credentialID, key, nil, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d (body: %s)", recorder.Code, recorder.Body)
	}

	events := credentialEventsOf(t, server, "acme", credentialID)
	want := []string{"deleted", "tested", "tested", "updated", "created"}
	if got := eventActions(events); len(got) != len(want) {
		t.Fatalf("trail = %v, want %v", got, want)
	} else {
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("trail = %v, want %v", got, want)
			}
		}
	}
	for _, event := range events {
		if event.Actor.Kind != repository.ActorKindKey || event.Actor.Label != keyLabel || event.Actor.KeyID != keyID {
			t.Errorf("%s actor = %+v, want key %q (%s)", event.Action, event.Actor, keyLabel, keyID)
		}
		if event.CredentialType != "httpBearerAuth" {
			t.Errorf("%s credential type = %q", event.Action, event.CredentialType)
		}
	}
	if events[0].CredentialName != "Billing API (prod)" || events[4].CredentialName != "Billing API" {
		t.Errorf("names on the trail = %q .. %q, want each event to keep the name it saw", events[0].CredentialName, events[4].CredentialName)
	}
	if events[1].Detail != "untestable" || events[2].Detail != "untestable" {
		t.Errorf("test details = %q, %q, want the verdict", events[1].Detail, events[2].Detail)
	}
}

func TestCredentialWritesAreAttributedToTheUser(t *testing.T) {
	server, _ := newAuthenticatedAPI(t, "acme")
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
	cookie := login.Result().Cookies()[0]

	credentialID := createdCredentialID(t, auditCallWithCookie(t, server, http.MethodPost, "/api/v1/credentials", cookie, map[string]any{
		"name": "Billing API", "type": "httpBearerAuth", "fields": map[string]string{"token": "secret-token"},
	}, nil))

	events := credentialEventsOf(t, server, "acme", credentialID)
	if len(events) != 1 || events[0].Action != repository.CredentialEventCreated {
		t.Fatalf("trail = %v, want one created event", eventActions(events))
	}
	if actor := events[0].Actor; actor.Kind != repository.ActorKindUser || actor.Label != "owner@acme.example" || actor.KeyID != "" {
		t.Fatalf("actor = %+v, want the signed-in user and no key id", actor)
	}
}

// Google's callback carries no principal: it is a browser landing on a public
// URL. The connect is still attributed to whoever pressed Connect, by the
// nonce hash both events share, so the state URL never has to carry an email.
func TestOAuthConnectIsAttributedToTheBrowserThatStartedIt(t *testing.T) {
	fake := newFakeGoogle(t)
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		deps.Config.Server.PublicURL = "https://app.test"
		deps.OAuthSigningKey = signingKey(40)
		deps.OAuthTokenURL = fake.server.URL
		deps.OAuthHTTP = fake.server.Client()
	}, "acme")
	key := keys["acme"]
	listed, err := server.store.ListAPIKeys(context.Background(), repository.TenantScope{ID: "acme"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListAPIKeys() = %d keys, %v", len(listed), err)
	}

	credentialID := createdCredentialID(t, auditCall(t, server, http.MethodPost, "/api/v1/credentials", key, map[string]any{
		"name": "Drive", "type": credentials.GoogleDriveOAuthType,
		"fields": map[string]string{"clientId": "tenant-cid", "clientSecret": "tenant-csec"},
	}, nil))
	start := auditCall(t, server, http.MethodPost, "/api/v1/credentials/"+credentialID+"/oauth/start", key, nil,
		map[string]string{"Origin": "https://editor.example"})
	if start.Code != http.StatusOK {
		t.Fatalf("oauth start = %d (body: %s)", start.Code, start.Body)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode the start response: %v", err)
	}
	authorize, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL: %v", err)
	}
	fake.saw(authorize.Query().Get("code_challenge"))
	if callback := finishConnect(t, server.handler, authorize.Query().Get("state"), start.Result().Cookies()); !connected(callback) {
		t.Fatalf("callback = %s, want connected", callback.Body)
	}

	events := credentialEventsOf(t, server, "acme", credentialID)
	if got := eventActions(events); len(got) != 3 || got[0] != "connected" || got[1] != "connect-started" || got[2] != "created" {
		t.Fatalf("trail = %v, want connected, connect-started, created", got)
	}
	connectedEvent, startedEvent := events[0], events[1]
	for _, event := range []repository.CredentialEvent{connectedEvent, startedEvent} {
		if event.Actor.Kind != repository.ActorKindKey || event.Actor.KeyID != listed[0].ID {
			t.Errorf("%s actor = %+v, want the key that pressed Connect", event.Action, event.Actor)
		}
	}
	if startedEvent.Detail == "" || connectedEvent.Detail != startedEvent.Detail {
		t.Errorf("details = %q / %q, want the shared nonce hash", connectedEvent.Detail, startedEvent.Detail)
	}
}
