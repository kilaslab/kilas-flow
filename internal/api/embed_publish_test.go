package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// An embed session with workflow:activate reaches the same Activate handler the
// dashboard does, so a workflow that uses a node its tenant may not use is
// refused with the same compile problem — node.not_available, in the same
// words — rather than going live because the middleware let it through.
func TestAnEmbedActivationMeetsTheSameCompilerAsTheDashboard(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Copied", acmeCRMType)), http.StatusCreated)
	copiedID, _ := copied["id"].(string)

	dashboard := activationProblemOf(t, server, keys["globex"], http.MethodPost,
		"/api/v1/workflows/"+copiedID+"/activate", nil, http.StatusUnprocessableEntity)
	if !containsString(codesOf(dashboard), "node.not_available") {
		t.Fatalf("dashboard activation codes = %v, want node.not_available", codesOf(dashboard))
	}

	// Without the scope the request never reaches the handler.
	editor := mintEmbedSessionAs(t, server, keys["globex"], copiedID, "workflow:read", "workflow:write", "workflow:run")
	refused := embedRequest(t, server.handler, editor, http.MethodPost, "/api/v1/workflows/"+copiedID+"/activate", nil)
	if refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "This embed session cannot change activation.") {
		t.Fatalf("activation without workflow:activate = %d %s, want 403 naming activation", refused.Code, refused.Body)
	}

	// With it, the compiler answers exactly as it answered the dashboard.
	activator := mintEmbedSessionAs(t, server, keys["globex"], copiedID, "workflow:activate")
	embedded := embedRequest(t, server.handler, activator, http.MethodPost, "/api/v1/workflows/"+copiedID+"/activate", nil)
	if embedded.Code != http.StatusUnprocessableEntity {
		t.Fatalf("embed activation = %d, want 422 (body: %s)", embedded.Code, embedded.Body)
	}
	var problem activationProblem
	if err := json.Unmarshal(embedded.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode embed activation problem: %v (body: %s)", err, embedded.Body)
	}
	if got, want := codesOf(problem), codesOf(dashboard); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("embed activation codes = %v, want the dashboard's %v", got, want)
	}
	if len(problem.Errors) == 0 || len(dashboard.Errors) == 0 || problem.Errors[0].Message != dashboard.Errors[0].Message || problem.Detail != dashboard.Detail {
		t.Errorf("embed activation problem = %#v, want the dashboard's %#v", problem, dashboard)
	}
	stored := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+copiedID, keys["globex"], nil, http.StatusOK)
	if stored["active"] == true {
		t.Fatalf("workflow = %v, want it still inactive after a refused activation", stored)
	}

	// A workflow the tenant may run activates and deactivates from the embed.
	own := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Own", sharedPingType)), http.StatusCreated)
	ownID, _ := own["id"].(string)
	ownActivator := mintEmbedSessionAs(t, server, keys["globex"], ownID, "workflow:activate")
	if activated := embedRequest(t, server.handler, ownActivator, http.MethodPost, "/api/v1/workflows/"+ownID+"/activate", nil); activated.Code != http.StatusOK {
		t.Fatalf("embed activation of a valid workflow = %d, want 200 (body: %s)", activated.Code, activated.Body)
	}
	if deactivated := embedRequest(t, server.handler, ownActivator, http.MethodPost, "/api/v1/workflows/"+ownID+"/deactivate", nil); deactivated.Code != http.StatusOK {
		t.Fatalf("embed deactivation = %d, want 200 (body: %s)", deactivated.Code, deactivated.Body)
	}
}

// Publishing a revision from an embed needs workflow:publish. workflow:write
// alone — the scope every editor session carries — is refused with a sentence
// that names what the host would have to grant, and a session with the scope
// meets the same compiler the dashboard's publish does.
func TestAnEmbedPublishesOnlyWithThePublishScope(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	own := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Own", sharedPingType)), http.StatusCreated)
	ownID, _ := own["id"].(string)
	latest, _ := own["latestVersion"].(map[string]any)
	versionID, _ := latest["id"].(string)
	publishPath := "/api/v1/workflows/" + ownID + "/versions/" + versionID + "/publish"

	editor := mintEmbedSessionAs(t, server, keys["globex"], ownID, "workflow:read", "workflow:write", "workflow:run")
	refused := embedRequest(t, server.handler, editor, http.MethodPost, publishPath, map[string]any{})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("publish with workflow:write = %d, want 403 (body: %s)", refused.Code, refused.Body)
	}
	if !strings.Contains(refused.Body.String(), "This embed session cannot publish a revision without the workflow:publish scope.") {
		t.Errorf("publish refusal = %s, want it to name workflow:publish", refused.Body)
	}

	publisher := mintEmbedSessionAs(t, server, keys["globex"], ownID, "workflow:publish")
	published := embedRequest(t, server.handler, publisher, http.MethodPost, publishPath, map[string]any{})
	if published.Code != http.StatusOK {
		t.Fatalf("publish with workflow:publish = %d, want 200 (body: %s)", published.Code, published.Body)
	}
	// Publishing is not activation's switch: the same session cannot take the
	// workflow offline again.
	if deactivated := embedRequest(t, server.handler, publisher, http.MethodPost, "/api/v1/workflows/"+ownID+"/deactivate", nil); deactivated.Code != http.StatusForbidden {
		t.Fatalf("deactivate with workflow:publish = %d, want 403 (body: %s)", deactivated.Code, deactivated.Body)
	}

	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Copied", acmeCRMType)), http.StatusCreated)
	copiedID, _ := copied["id"].(string)
	copiedLatest, _ := copied["latestVersion"].(map[string]any)
	copiedVersion, _ := copiedLatest["id"].(string)
	copiedPublisher := mintEmbedSessionAs(t, server, keys["globex"], copiedID, "workflow:publish")
	notAvailable := embedRequest(t, server.handler, copiedPublisher, http.MethodPost,
		"/api/v1/workflows/"+copiedID+"/versions/"+copiedVersion+"/publish", map[string]any{})
	if notAvailable.Code != http.StatusUnprocessableEntity || !strings.Contains(notAvailable.Body.String(), "node.not_available") {
		t.Fatalf("embed publish of a copied workflow = %d %s, want 422 node.not_available", notAvailable.Code, notAvailable.Body)
	}
}

// Activation pins the latest revision, so an embed activation is held to the
// confinement every other embed path that makes a revision live or runs it is
// held to: a later draft that attaches a credential the session was never
// granted does not go live because the session may flip the switch.
func TestAnEmbedActivationIsHeldToTheSessionsConfinement(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	published := requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/deactivate", nil, http.StatusOK)
	smuggled := storeCredential(t, handler, "Smuggled", "httpHeaderAuth", map[string]string{
		"name": "X-Smuggled", "value": "smuggled-secret",
	})
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("exfil", smuggled.ID))), http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:activate")
	got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil)
	if got.Code != http.StatusForbidden {
		t.Fatalf("activate status = %d, want 403 — the latest revision is outside the session's confinement (body: %s)", got.Code, got.Body)
	}
	if !strings.Contains(got.Body.String(), "this embed session cannot use that workflow document") {
		t.Errorf("activate refusal = %s, want the confinement sentence", got.Body)
	}
	stored := requestJSON[workflowResource](t, handler, http.MethodGet, "/api/v1/workflows/"+workflowID, nil, http.StatusOK)
	if stored.Active || stored.ActiveVersion == nil || stored.ActiveVersion.ID != published.ActiveVersion.ID {
		t.Fatalf("workflow = active %v pinned %#v, want it still off and pinned to %s", stored.Active, stored.ActiveVersion, published.ActiveVersion.ID)
	}
}

// The two scopes are embed-only: a scoped API key keeps the vocabulary it had,
// so minting one with either is refused by name rather than stored as a scope
// its arm would never honour.
func TestAnAPIKeyCannotBeMintedWithTheEmbedOnlyScopes(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	for _, scope := range []string{"workflow:publish", "workflow:activate"} {
		recorder := server.call(t, http.MethodPost, "/api/v1/api-keys", keys["tenant-a"], map[string]any{
			"label": "agent", "scopes": []string{"workflow:read", scope},
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("minting a key with %q = %d, want 422 (body: %s)", scope, recorder.Code, recorder.Body)
		}
		if body := recorder.Body.String(); !strings.Contains(body, scope) || !strings.Contains(body, "embed sessions only") {
			t.Errorf("minting a key with %q = %s, want it to name the scope as embed-only", scope, body)
		}
	}
}
