package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
)

// These tests pin the host-declared half of an embed session's confinement:
// the `allow` set a host passes at mint so that an end user building a new
// automation inside the embed can pick the credentials, data tables and
// workflows the host provisioned for their organisation — which the derived
// half, read off the owner's published revision, cannot know about.
//
// Two tenants share one database here, because the property that matters most
// is that a host cannot declare another tenant's id into its own token.

// allowAPI is two servers over one database: tenant-a and tenant-b, the same
// stores, the same issuer.
type allowAPI struct {
	a, b     http.Handler
	issuer   *embed.Issuer
	workflow string
}

func newAllowAPI(t *testing.T) allowAPI {
	t.Helper()
	db, datastores := sharedDatastoreDB(t)
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	cipherKey := make([]byte, credentials.KeySize)
	for index := range cipherKey {
		cipherKey[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(cipherKey)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	executions := repository.NewExecutionStore(db.DB)
	broker := events.NewBroker(events.BrokerOptions{})
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: registry, Runner: engine.NewRunner(engine.NewRegistry()),
		Events: broker, WorkerID: "allow-test", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	workflows := repository.NewWorkflowStore(db.DB).WithWebhooks(webhook.Extract(registry, nodes.WebhookPath))
	credentialStore := repository.NewCredentialStore(db.DB, cipher)
	issuer := embedIssuer(t)
	server := func(tenant string) http.Handler {
		return newTestServer(t, api.Deps{
			DB: db, NodeRegistry: registry, Workflows: workflows, Executions: executions,
			Credentials: credentialStore, Datastores: datastores, ExecutionController: runtime,
			Events: broker, EmbedIssuer: issuer, Tenants: fixedTenant{id: tenant},
		})
	}
	built := allowAPI{a: server("tenant-a"), b: server("tenant-b"), issuer: issuer}
	built.workflow = createWorkflow(t, built.a, validManualWorkflow("Embeddable")).ID
	return built
}

// mintWithAllow mints a workflow session for tenant-a's workflow carrying an
// allow set, and answers the raw response so a refusal can be asserted.
func (server allowAPI) mintWithAllow(t *testing.T, allow any, scopes ...string) *jsonResponse {
	t.Helper()
	body := map[string]any{"workflowId": server.workflow, "scopes": scopes, "origin": hostOrigin}
	if allow != nil {
		body["allow"] = allow
	}
	recorder := doJSON(t, server.a, http.MethodPost, "/api/v1/embed-sessions", body)
	response := &jsonResponse{Code: recorder.Code, Body: recorder.Body.String()}
	if recorder.Code == http.StatusCreated {
		var created struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode mint: %v", err)
		}
		response.Token = created.Token
	}
	return response
}

type jsonResponse struct {
	Code  int
	Body  string
	Token string
}

func (server allowAPI) session(t *testing.T, token string) embed.Session {
	t.Helper()
	session, err := server.issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	return session
}

// A host can only declare what its own tenant owns. Another tenant's id reads
// exactly like an id nobody stored — 404, minting nothing — so the answer does
// not tell the host whether the id exists elsewhere.
func TestADeclaredIdFromAnotherTenantIsNotFound(t *testing.T) {
	server := newAllowAPI(t)
	foreignCredential := storeScopedCredential(t, server.b, "Globex key", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "globex-secret",
	}, "partner.test")
	foreignDatastore := createDatastoreWithColumns(t, server.b, "Globex leads", [2]string{"email", "string"})
	foreignWorkflow := createWorkflow(t, server.b, validManualWorkflow("Globex flow")).ID

	for name, allow := range map[string]map[string]any{
		"foreign credential": {"credentials": []string{foreignCredential.ID}},
		"foreign datastore":  {"datastores": []string{foreignDatastore.ID}},
		"foreign workflow":   {"workflows": []string{foreignWorkflow}},
		"unknown credential": {"credentials": []string{"cred_nobody_stored"}},
		"unknown datastore":  {"datastores": []string{"datastore_nobody_made"}},
		"unknown workflow":   {"workflows": []string{"wf_nobody_made"}},
	} {
		got := server.mintWithAllow(t, allow, "workflow:read", "workflow:write")
		if got.Code != http.StatusNotFound {
			t.Errorf("%s: mint status = %d, want 404 (body: %s)", name, got.Code, got.Body)
		}
		if got.Token != "" {
			t.Errorf("%s: a refused mint returned a token", name)
		}
		if strings.Contains(got.Body, "Globex") {
			t.Errorf("%s: the refusal names the other tenant's resource: %s", name, got.Body)
		}
	}
}

// The declared ids are added to what the owner's revision uses, and nothing
// else is: a credential the host named saves, publishes and runs; one it did
// not name is refused with the same 403 a derived-only session gets.
func TestADeclaredCredentialIsUsableAndAnUnlistedOneIsNot(t *testing.T) {
	server := newAllowAPI(t)
	declared := storeScopedCredential(t, server.a, "Declared", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "declared-secret",
	}, "partner.test")
	unlisted := storeScopedCredential(t, server.a, "Unlisted", "httpHeaderAuth", map[string]string{
		"name": "X-Api-Key", "value": "unlisted-secret",
	}, "partner.test")

	minted := server.mintWithAllow(t, map[string]any{"credentials": []string{declared.ID}},
		"workflow:write", "workflow:run", "workflow:publish")
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201 (body: %s)", minted.Code, minted.Body)
	}
	if got := server.session(t, minted.Token).Confinement.Credentials; len(got) != 1 || got[0] != declared.ID {
		t.Fatalf("token credentials = %v, want exactly the declared one", got)
	}

	saved := embedRequest(t, server.a, minted.Token, http.MethodPut, "/api/v1/workflows/"+server.workflow,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", declared.ID))))
	if saved.Code != http.StatusOK {
		t.Fatalf("save with the declared credential = %d, want 200 (body: %s)", saved.Code, saved.Body)
	}
	var stored workflowResource
	if err := json.Unmarshal(saved.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode save: %v", err)
	}
	published := embedRequest(t, server.a, minted.Token, http.MethodPost,
		"/api/v1/workflows/"+server.workflow+"/versions/"+stored.LatestVersion.ID+"/publish", map[string]any{})
	if published.Code != http.StatusOK {
		t.Fatalf("publish with the declared credential = %d, want 200 (body: %s)", published.Code, published.Body)
	}
	restored := embedRequest(t, server.a, minted.Token, http.MethodPost,
		"/api/v1/workflows/"+server.workflow+"/versions/"+stored.LatestVersion.ID+"/restore", map[string]any{})
	if restored.Code != http.StatusOK {
		t.Fatalf("restore of a revision with the declared credential = %d, want 200 (body: %s)", restored.Code, restored.Body)
	}
	run := embedRequest(t, server.a, minted.Token, http.MethodPost, "/api/v1/workflows/"+server.workflow+"/run", map[string]any{})
	if run.Code != http.StatusAccepted {
		t.Fatalf("run with the declared credential = %d, want 202 (body: %s)", run.Code, run.Body)
	}

	refused := embedRequest(t, server.a, minted.Token, http.MethodPut, "/api/v1/workflows/"+server.workflow,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", unlisted.ID))))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("save with an unlisted credential = %d, want 403 (body: %s)", refused.Code, refused.Body)
	}
	if !strings.Contains(refused.Body.String(), "which this embed session was not granted") ||
		!strings.Contains(refused.Body.String(), unlisted.ID) {
		t.Errorf("the refusal does not name the ungranted credential: %s", refused.Body)
	}
}

// The embedded credential picker is the existing listing, filtered to the
// session's grant: with a declared set it lists exactly the derived ids plus
// the declared ones, and never a sibling.
func TestTheCredentialPickerListsExactlyTheDeclaredAndDerivedCredentials(t *testing.T) {
	server := newAllowAPI(t)
	owner := storeScopedCredential(t, server.a, "Owner", "httpHeaderAuth", map[string]string{
		"name": "X-Owner", "value": "owner-secret",
	}, "partner.test")
	declared := storeCredential(t, server.a, "Declared", "httpHeaderAuth", map[string]string{
		"name": "X-Declared", "value": "declared-secret",
	})
	storeCredential(t, server.a, "Sibling", "httpHeaderAuth", map[string]string{
		"name": "X-Sibling", "value": "sibling-secret",
	})
	requestJSON[workflowResource](t, server.a, http.MethodPut, "/api/v1/workflows/"+server.workflow,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", owner.ID))), http.StatusOK)
	requestJSON[workflowResource](t, server.a, http.MethodPost, "/api/v1/workflows/"+server.workflow+"/activate", nil, http.StatusOK)

	minted := server.mintWithAllow(t, map[string]any{"credentials": []string{declared.ID}}, "workflow:read")
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint status = %d (body: %s)", minted.Code, minted.Body)
	}
	listed := listEmbedCredentials(t, server.a, minted.Token)
	if len(listed) != 2 || !listed[owner.ID] || !listed[declared.ID] {
		t.Fatalf("listed = %v, want exactly the owner's and the declared credential", listed)
	}
}

// "tenant" opens a kind to every resource the tenant owns, carried as a flag.
// It still stops at the tenant: the picker lists every credential of tenant-a
// and none of tenant-b's, and a document may attach any of tenant-a's.
func TestATenantWideCredentialGrantCoversTheTenantAndNothingElse(t *testing.T) {
	server := newAllowAPI(t)
	first := storeScopedCredential(t, server.a, "First", "httpHeaderAuth", map[string]string{
		"name": "X-First", "value": "first-secret",
	}, "partner.test")
	second := storeCredential(t, server.a, "Second", "httpHeaderAuth", map[string]string{
		"name": "X-Second", "value": "second-secret",
	})
	foreign := storeCredential(t, server.b, "Foreign", "httpHeaderAuth", map[string]string{
		"name": "X-Foreign", "value": "foreign-secret",
	})

	minted := server.mintWithAllow(t, map[string]any{"credentials": "tenant"}, "workflow:read", "workflow:write")
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201 (body: %s)", minted.Code, minted.Body)
	}
	confinement := server.session(t, minted.Token).Confinement
	if !confinement.TenantCredentials || len(confinement.Credentials) != 0 {
		t.Fatalf("confinement = %#v, want the tenant-wide credential flag and no expanded list", confinement)
	}
	if confinement.TenantDatastores || confinement.TenantWorkflows {
		t.Errorf("confinement = %#v, want only the credential kind opened", confinement)
	}

	listed := listEmbedCredentials(t, server.a, minted.Token)
	if len(listed) != 2 || !listed[first.ID] || !listed[second.ID] || listed[foreign.ID] {
		t.Fatalf("listed = %v, want both of tenant-a's credentials and not tenant-b's", listed)
	}

	saved := embedRequest(t, server.a, minted.Token, http.MethodPut, "/api/v1/workflows/"+server.workflow,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", first.ID))))
	if saved.Code != http.StatusOK {
		t.Fatalf("save under a tenant-wide grant = %d, want 200 (body: %s)", saved.Code, saved.Body)
	}
	// The grant does not lift the unscoped-credential rule: a credential with
	// no allowed domains could still be sent to any host the guest types.
	unscoped := embedRequest(t, server.a, minted.Token, http.MethodPut, "/api/v1/workflows/"+server.workflow,
		workflowDraft(documentReferencing("Embeddable", embedWorkflowNode("collect", second.ID))))
	if unscoped.Code != http.StatusForbidden || !strings.Contains(unscoped.Body.String(), "no allowed domains") {
		t.Fatalf("save of an unscoped credential = %d, want 403 naming the missing scope (body: %s)", unscoped.Code, unscoped.Body)
	}
}

// A declared data table is granted by id and by name, so a By-Name locator the
// editor writes resolves; a sibling table stays out. A declared workflow may be
// called by a sub-workflow or tool node.
func TestDeclaredDataTablesAndWorkflowsAreGranted(t *testing.T) {
	server := newAllowAPI(t)
	leads := createDatastoreWithColumns(t, server.a, "Leads", [2]string{"email", "string"})
	sibling := createDatastoreWithColumns(t, server.a, "Payroll", [2]string{"email", "string"})
	child := createWorkflow(t, server.a, validManualWorkflow("Child")).ID

	minted := server.mintWithAllow(t, map[string]any{
		"datastores": []string{leads.ID}, "workflows": []string{child},
	}, "workflow:read", "workflow:write")
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201 (body: %s)", minted.Code, minted.Body)
	}
	for _, target := range []struct {
		name string
		ok   bool
		doc  any
	}{
		{"data table by id", true, workflowDraft(documentReferencing("Embeddable", embedDatastoreNode("byId", "get", "id", leads.ID)))},
		{"data table by name", true, workflowDraft(documentReferencing("Embeddable", embedDatastoreNode("byName", "get", "name", "Leads")))},
		{"declared workflow", true, workflowDraft(documentReferencing("Embeddable", embedWorkflowToolNode("delegate", child)))},
		{"sibling data table", false, workflowDraft(documentReferencing("Embeddable", embedDatastoreNode("sibling", "get", "id", sibling.ID)))},
	} {
		got := embedRequest(t, server.a, minted.Token, http.MethodPut, "/api/v1/workflows/"+server.workflow, target.doc)
		want := http.StatusOK
		if !target.ok {
			want = http.StatusForbidden
		}
		if got.Code != want {
			t.Errorf("%s: save = %d, want %d (body: %s)", target.name, got.Code, want, got.Body)
		}
	}
}

// Without `allow` a session is exactly what it was: the derived set, nothing
// more. An allow set on a datastore session, a malformed list, and a blank id
// are refused rather than read as something else.
func TestTheAllowSetIsOptionalAndRefusedWhereItMeansNothing(t *testing.T) {
	server := newAllowAPI(t)
	plain := server.mintWithAllow(t, nil, "workflow:read")
	if plain.Code != http.StatusCreated {
		t.Fatalf("mint without allow = %d (body: %s)", plain.Code, plain.Body)
	}
	confinement := server.session(t, plain.Token).Confinement
	if len(confinement.Credentials) != 0 || confinement.TenantCredentials || confinement.TenantDatastores || confinement.TenantWorkflows {
		t.Fatalf("a session minted without allow carries %#v", confinement)
	}

	for name, allow := range map[string]any{
		"an unknown keyword": map[string]any{"credentials": "everyone"},
		"a blank id":         map[string]any{"credentials": []string{" "}},
		"a number":           map[string]any{"workflows": 7},
	} {
		if got := server.mintWithAllow(t, allow, "workflow:read"); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: mint = %d, want 422 (body: %s)", name, got.Code, got.Body)
		}
	}

	store := createDatastoreWithColumns(t, server.a, "Rows", [2]string{"email", "string"})
	got := doJSON(t, server.a, http.MethodPost, "/api/v1/embed-sessions", map[string]any{
		"datastoreId": store.ID, "scopes": []string{"datastore:read"}, "origin": hostOrigin,
		"allow": map[string]any{"credentials": "tenant"},
	})
	if got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("allow on a datastore session = %d, want 422 (body: %s)", got.Code, got.Body)
	}
}

func listEmbedCredentials(t *testing.T, handler http.Handler, token string) map[string]bool {
	t.Helper()
	got := embedRequest(t, handler, token, http.MethodGet, "/api/v1/credentials", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("credential list = %d, want 200 (body: %s)", got.Code, got.Body)
	}
	var listed []credentialResource
	if err := json.Unmarshal(got.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode credential list: %v", err)
	}
	ids := map[string]bool{}
	for _, credential := range listed {
		ids[credential.ID] = true
	}
	return ids
}
