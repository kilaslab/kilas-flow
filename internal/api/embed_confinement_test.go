package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// These tests reproduce the escape the full review found, at the API surface
// the attack used.
//
// A workflow-scoped embed token could PUT arbitrary nodes into its own workflow
// and run them, so the Data table node's Table:List/Delete and any credential id
// executed with the tenant's authority — defeating the documented guarantee that
// no embed scope grants datastore management or a credential the workflow does
// not reference. The routing middleware never saw the document, so the fix is a
// confinement minted into the session and enforced on every save, publish,
// restore, and run.
//
// The reproduction is deliberately end-to-end: a token minted through the real
// endpoint, a document sent through the real handler, and the refusal read off
// the real response. A unit test of the walker would not have caught the
// original bug, which lived entirely in the gap between the two.

// embedWorkflowNode builds one http request node that attaches a credential.
func embedWorkflowNode(name, credentialID string) workflow.Node {
	return workflow.Node{
		ID: name, Name: name, Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"url": "https://partner.test/collect"},
		Credentials: map[string]string{"httpHeaderAuth": credentialID},
	}
}

// embedDatastoreNode builds one data table node addressing a table.
func embedDatastoreNode(id, operation, mode, value string) workflow.Node {
	return workflow.Node{
		ID: id, Name: id, Type: "kilasflow.datastore", TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"resource":  "row",
			"operation": operation,
			"dataTableId": map[string]any{
				"__rl": true, "mode": mode, "value": value,
			},
		},
	}
}

// documentReferencing builds a workflow document made of one trigger plus the
// supplied nodes.
func documentReferencing(name string, nodes ...workflow.Node) workflow.Document {
	document := validManualWorkflow(name)
	document.Nodes = append(document.Nodes, nodes...)
	return document
}

// mintEmbedSession mints a session through the endpoint the host backend uses,
// so the confinement under test is the one production mints.
func mintEmbedSession(t *testing.T, handler http.Handler, workflowID string, scopes ...string) string {
	t.Helper()
	created := requestJSON[struct {
		Token string `json:"token"`
	}](t, handler, http.MethodPost, "/api/v1/embed-sessions", map[string]any{
		"workflowId": workflowID, "scopes": scopes, "origin": hostOrigin,
	}, http.StatusCreated)
	if created.Token == "" {
		t.Fatal("the mint returned no token")
	}
	return created.Token
}

func TestAnEmbedSessionCannotAttachACredentialItsWorkflowNeverReferenced(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)

	// A credential the workflow's own document never mentions, stored by the
	// tenant's owner. The attack attaches it to a node and runs the workflow,
	// which would make the secret reach a host the credential's own scope
	// never named.
	unreferenced := storeCredential(t, handler, "Tenant wide", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "SCOPED-SECRET-5521",
	})

	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write", "workflow:run")

	hostile := documentReferencing("Embeddable", embedWorkflowNode("exfil", unreferenced.ID))
	got := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID, workflowDraft(hostile))
	if got.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a credential the workflow never referenced (body: %s)", got.Code, got.Body)
	}
	if !strings.Contains(got.Body.String(), unreferenced.ID) {
		t.Errorf("the refusal does not name the credential the guest tried to attach: %s", got.Body)
	}

	// And the draft is untouched: a refused save must not have written
	// anything, or the next run would carry it.
	stored := requestJSON[workflowResource](t, handler, http.MethodGet, "/api/v1/workflows/"+workflowID, nil, http.StatusOK)
	if len(stored.LatestVersion.Document.Nodes) != 0 {
		t.Errorf("a refused save left nodes behind: %#v", stored.LatestVersion.Document.Nodes)
	}
	_ = issuer
}

func TestAnEmbedSessionCannotSaveDataTableSchemaOperations(t *testing.T) {
	handler, _, workflowID := embedServer(t)
	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write")

	// Table:List enumerates every data table in the tenant; Delete and Clear
	// reshape one. None of that is a guest editor's business, whatever the
	// session's confinement happens to name.
	for _, operation := range []string{"list", "create", "rename", "deleteTable", "clear"} {
		node := embedDatastoreNode("datastore", operation, "id", "datastore_sibling")
		node.Parameters["resource"] = "table"
		got := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
			workflowDraft(documentReferencing("Embeddable", node)))
		if got.Code != http.StatusForbidden {
			t.Errorf("operation %q status = %d, want 403 (body: %s)", operation, got.Code, got.Body)
		}
	}
}

func TestAnEmbedSessionCannotAddressASiblingDataTable(t *testing.T) {
	handler, _, workflowID := embedServer(t)
	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write")

	// The workflow's own document names nothing, so every data table is
	// outside the session — by id and by name alike, because a By-Name locator
	// is resolved against the tenant's live list.
	for _, target := range []workflow.Node{
		embedDatastoreNode("byId", "get", "id", "datastore_sibling"),
		embedDatastoreNode("byName", "get", "name", "[api-security-tenancy] A-ds"),
	} {
		got := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
			workflowDraft(documentReferencing("Embeddable", target)))
		if got.Code != http.StatusForbidden {
			t.Errorf("node %q status = %d, want 403 (body: %s)", target.ID, got.Code, got.Body)
		}
	}
}

func TestAnEmbedSessionMaySaveWhatItsOwnPublishedRevisionReferences(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	// The owner authors a revision that uses one credential, and publishes it.
	// That revision is the session's whole authority — nothing else.
	referenced := storeCredential(t, handler, "Workspace", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "owner-secret",
	})
	ownerDocument := documentReferencing("Embeddable", embedWorkflowNode("collect", referenced.ID))
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(ownerDocument), http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:read", "workflow:write")

	// Saving the same reference back is inside the confinement: the workflow
	// demonstrably needs that credential.
	saved := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", referenced.ID))))
	if saved.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a reference the published revision already makes (body: %s)", saved.Code, saved.Body)
	}

	// A second credential the published revision does not use is still out.
	other := storeCredential(t, handler, "Other", "httpHeaderAuth", map[string]string{
		"name": "X-Other", "value": "other-secret",
	})
	refused := embedRequest(t, handler, token, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", other.ID))))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a credential outside the published revision (body: %s)", refused.Code, refused.Body)
	}
}

func TestAnEmbedSessionCannotRunAStoredRevisionOutsideItsConfinement(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)

	// The published revision references nothing...
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)

	// ...and a later draft — written by an owner session, or by an attacker
	// before this check existed — attaches a credential the workflow never
	// used. The run path compiles the latest revision, so this is exactly the
	// replay the engine-side check has to stop.
	smuggled := storeCredential(t, handler, "Smuggled", "httpHeaderAuth", map[string]string{
		"name": "X-Smuggled", "value": "smuggled-secret",
	})
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("exfil", smuggled.ID))), http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:run")
	session, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(session.Confinement.Credentials) != 0 {
		t.Fatalf("confinement = %#v, want none: it must come from the published revision, not the draft", session.Confinement)
	}

	got := embedRequest(t, handler, token, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", nil)
	if got.Code != http.StatusForbidden {
		t.Fatalf("run status = %d, want 403 — the latest revision is outside the session's confinement (body: %s)", got.Code, got.Body)
	}
}

func TestTheCredentialPickerShowsAnEmbedSessionOnlyItsOwnCredentials(t *testing.T) {
	handler, _, workflowID := embedServer(t)

	referenced := storeCredential(t, handler, "Workspace", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "owner-secret",
	})
	storeCredential(t, handler, "Sibling", "httpHeaderAuth", map[string]string{
		"name": "X-Sibling", "value": "sibling-secret",
	})
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", referenced.ID))), http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+workflowID+"/activate", nil, http.StatusOK)

	token := mintEmbedSession(t, handler, workflowID, "workflow:read")
	got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/credentials", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	var listed []credentialResource
	if err := json.Unmarshal(got.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != referenced.ID {
		t.Fatalf("listed = %#v, want exactly the credential this workflow references", listed)
	}
	// A name the guest may not use is not disclosed either.
	if strings.Contains(got.Body.String(), "Sibling") {
		t.Errorf("the credential listing leaked a sibling's name: %s", got.Body)
	}
}

// The confinement is minted, not derived per request, so the token itself is
// evidence: a session whose workflow has published nothing carries nothing.
func TestASessionForAnUnpublishedWorkflowCarriesNoReferences(t *testing.T) {
	handler, issuer, workflowID := embedServer(t)
	token := mintEmbedSession(t, handler, workflowID, "workflow:read")

	session, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !session.Confinement.Empty() {
		t.Fatalf("confinement = %#v, want empty for a workflow with no credential and no data table", session.Confinement)
	}
}

// The internal dashboard is not an embed session and must keep saving whatever
// its tenant owns: the check reads the session, not the endpoint.
func TestTheDashboardIsUnaffectedByTheEmbedConfinement(t *testing.T) {
	handler, _, workflowID := embedServer(t)
	credential := storeCredential(t, handler, "Any", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "secret",
	})
	requestJSON[workflowResource](t, handler, http.MethodPut, "/api/v1/workflows/"+workflowID,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", credential.ID))), http.StatusOK)
}
