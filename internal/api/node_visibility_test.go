package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The two node types these tests share: one scoped to acme, one visible to
// everyone. Both are pack nodes with a real served icon, because an icon-less
// node would answer 404 for a reason that has nothing to do with tenancy.
const (
	acmeCRMType    = "pack.acme.crm"
	sharedPingType = "pack.shared.ping"
)

// visibilityIcon is valid, inert SVG: the icon route is one of the surfaces a
// scoped type must disappear from.
const visibilityIcon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`

func visibilityPackNode(nodeType string, tenants []string) node.Definition {
	return node.Definition{
		Type: nodeType, Version: workflow.V(1),
		DisplayName: nodeType, Category: "Pack", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		IconLight: &node.IconAsset{
			MediaType: node.IconSVG,
			Bytes:     []byte(visibilityIcon),
		},
		VisibleTo: tenants,
		Parameters: []node.PropertyDefinition{{
			Key: "account", Label: "Account", Kind: node.PropertyOptions,
			LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: "test.accounts"},
		}},
	}
}

// newVisibilityAPI is one authenticated server holding a pack node scoped to
// acme and an unscoped one, with two real tenants. It is built here rather than
// shared with newAuthenticatedAPI because it needs an option loader: without
// one, load-options and load-schema answer 503, and every "hidden looks like
// unknown" assertion would compare 503 with 503 and prove nothing.
func newVisibilityAPI(t *testing.T) (*authenticatedAPI, map[string]string) {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "visibility-api.db"),
	}, discard)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, discard); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, definition := range []node.Definition{
		visibilityPackNode(acmeCRMType, []string{"acme"}),
		visibilityPackNode(sharedPingType, nil),
	} {
		if err := registry.RegisterFrom(node.SourcePack, definition); err != nil {
			t.Fatalf("RegisterFrom(%q) error = %v", definition.Type, err)
		}
	}

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterInternal("test.accounts", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		return loadoptions.Result{Options: []loadoptions.Option{{Label: scope.TenantID, Value: scope.TenantID}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}

	cipherKey := make([]byte, credentials.KeySize)
	for index := range cipherKey {
		cipherKey[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(cipherKey)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	issuer, err := auth.NewIssuer(signingKey(1), time.Hour, nil)
	if err != nil {
		t.Fatalf("auth.NewIssuer() error = %v", err)
	}
	embedIssuer, err := embed.NewIssuer(signingKey(90), []string{embedOrigin}, nil)
	if err != nil {
		t.Fatalf("embed.NewIssuer() error = %v", err)
	}

	authStore := repository.NewAuthStore(db.DB)
	keys := map[string]string{}
	for _, tenantID := range []string{"acme", "globex"} {
		if _, err := authStore.EnsureTenant(context.Background(), tenantID, tenantID); err != nil {
			t.Fatalf("EnsureTenant(%q) error = %v", tenantID, err)
		}
		_, token, err := authStore.CreateAPIKey(context.Background(), repository.TenantScope{ID: tenantID}, tenantID+" key")
		if err != nil {
			t.Fatalf("CreateAPIKey(%q) error = %v", tenantID, err)
		}
		keys[tenantID] = token
	}

	executions := repository.NewExecutionStore(db.DB)
	broker := events.NewBroker(events.BrokerOptions{})
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions: executions, Catalog: registry, Runner: engine.NewRunner(engine.NewRegistry()),
		Events: broker, WorkerID: "visibility-test", DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("engine.NewService() error = %v", err)
	}

	cfg := config.Default()
	cfg.Auth.Enabled = true

	handler := newTestServer(t, api.Deps{
		Config:              cfg,
		DB:                  db,
		NodeRegistry:        registry,
		Workflows:           repository.NewWorkflowStore(db.DB).WithWebhooks(webhook.Extract(registry, nodes.WebhookPath)),
		Executions:          executions,
		Credentials:         repository.NewCredentialStore(db.DB, cipher),
		Schedules:           repository.NewScheduleStore(db.DB),
		ExecutionController: runtime,
		Events:              broker,
		EmbedIssuer:         embedIssuer,
		Tenants:             handlers.NewPrincipalTenants(""),
		AuthStore:           authStore,
		AuthIssuer:          issuer,
		OptionLoader:        resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver {
			return nil
		},
	})

	return &authenticatedAPI{
		handler: handler, store: authStore, issuer: issuer,
		embedIssuer: embedIssuer, executions: executions, broker: broker,
	}, keys
}

// crmWorkflow is a manual workflow whose one step is nodeType.
func crmWorkflow(name, nodeType string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, Name: name,
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "crm", Name: "CRM", Type: nodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "crm", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// catalogueTypes reads /node-types as one caller, returning the types and the
// raw body so a test can assert on what is not disclosed.
func catalogueTypes(t *testing.T, server *authenticatedAPI, key string) ([]string, string) {
	t.Helper()
	recorder := server.call(t, http.MethodGet, "/api/v1/node-types", key, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /node-types = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	var definitions []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &definitions); err != nil {
		t.Fatalf("decode node catalogue: %v", err)
	}
	types := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		types = append(types, definition.Type)
	}
	return types, recorder.Body.String()
}

func containsType(types []string, wanted string) bool {
	for _, nodeType := range types {
		if nodeType == wanted {
			return true
		}
	}
	return false
}

// mintEmbedSessionAs issues an embed token for one workflow under one tenant's
// key. The unauthenticated helper of a similar name elsewhere in this package
// mints for the default tenant and cannot exercise a tenant's own catalogue.
func mintEmbedSessionAs(t *testing.T, server *authenticatedAPI, key, workflowID string, scopes ...string) string {
	t.Helper()
	minted := server.callJSON(t, http.MethodPost, "/api/v1/embed-sessions", key, map[string]any{
		"workflowId": workflowID, "scopes": scopes, "origin": embedOrigin,
	}, http.StatusCreated)
	token, _ := minted["token"].(string)
	if token == "" {
		t.Fatalf("no token in %#v", minted)
	}
	return token
}

func TestTheCatalogueHidesANodeScopedToAnotherTenant(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	acmeTypes, acmeBody := catalogueTypes(t, server, keys["acme"])
	globexTypes, globexBody := catalogueTypes(t, server, keys["globex"])

	if !containsType(acmeTypes, acmeCRMType) {
		t.Errorf("acme's catalogue does not list its own pack node: %#v", acmeTypes)
	}
	if containsType(globexTypes, acmeCRMType) {
		t.Errorf("globex's catalogue lists a node scoped to acme: %#v", globexTypes)
	}
	// Unscoped nodes and the built-ins are untouched for both.
	for _, wanted := range []string{sharedPingType, "kilasflow.manual"} {
		if !containsType(acmeTypes, wanted) || !containsType(globexTypes, wanted) {
			t.Errorf("%q is missing from a catalogue: acme=%v globex=%v", wanted, acmeTypes, globexTypes)
		}
	}
	// The catalogue never says which tenants a node is reserved for, and it
	// never names a tenant at all.
	if strings.Contains(acmeBody, "visibleTo") || strings.Contains(acmeBody, "globex") {
		t.Errorf("acme's catalogue body discloses the scope: %s", acmeBody)
	}
	if strings.Contains(globexBody, "visibleTo") || strings.Contains(globexBody, "acme") {
		t.Errorf("globex's catalogue body discloses the scope: %s", globexBody)
	}
}

// An embed session resolves to its own tenant, so its editor offers exactly
// what the tenant that minted it may use — not the deployment's whole
// catalogue.
func TestAnEmbedSessionSeesTheCatalogueOfItsOwnTenant(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Acme Onboarding")
	globexWorkflow := server.createWorkflowAs(t, keys["globex"], "Globex Billing")

	acmeToken := mintEmbedSessionAs(t, server, keys["acme"], acmeWorkflow, "workflow:read")
	globexToken := mintEmbedSessionAs(t, server, keys["globex"], globexWorkflow, "workflow:read")

	for _, test := range []struct {
		name       string
		token      string
		wantScoped bool
	}{
		{name: "acme's session", token: acmeToken, wantScoped: true},
		{name: "globex's session", token: globexToken, wantScoped: false},
	} {
		recorder := embedRequest(t, server.handler, test.token, http.MethodGet, "/api/v1/node-types", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: GET /node-types = %d, want 200 (body: %s)", test.name, recorder.Code, recorder.Body)
		}
		var definitions []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &definitions); err != nil {
			t.Fatalf("%s: decode catalogue: %v", test.name, err)
		}
		types := make([]string, 0, len(definitions))
		for _, definition := range definitions {
			types = append(types, definition.Type)
		}
		if containsType(types, acmeCRMType) != test.wantScoped {
			t.Errorf("%s lists %q = %v, want %v (%v)", test.name, acmeCRMType, !test.wantScoped, test.wantScoped, types)
		}
		if !containsType(types, sharedPingType) {
			t.Errorf("%s does not list the unscoped pack node", test.name)
		}
	}
}

// A type a tenant may not see has to answer exactly as one that does not
// exist: a different 404 would be an existence oracle, telling any caller which
// node types the deployment runs for somebody else.
func TestAScopedNodeAnswersLikeAnUnregisteredTypeToOtherTenants(t *testing.T) {
	server, keys := newVisibilityAPI(t)
	globex := keys["globex"]
	acme := keys["acme"]

	// Each surface answers 404 for the scoped type and for a misspelling of it,
	// with byte-identical bodies. The status is asserted explicitly: equality
	// alone would pass on two 503s.
	surfaces := []struct {
		name   string
		method string
		path   func(nodeType string) string
		body   any
	}{
		{
			name: "icon", method: http.MethodGet,
			path: func(nodeType string) string { return "/api/v1/node-types/" + nodeType + "/icon" },
		},
		{
			name: "load-options", method: http.MethodPost,
			path: func(nodeType string) string { return "/api/v1/node-types/" + nodeType + "/load-options" },
			body: map[string]any{"property": "account"},
		},
		{
			name: "load-schema", method: http.MethodPost,
			path: func(nodeType string) string { return "/api/v1/node-types/" + nodeType + "/load-schema" },
			body: map[string]any{"property": "account"},
		},
	}
	for _, surface := range surfaces {
		hidden := server.call(t, surface.method, surface.path(acmeCRMType), globex, surface.body)
		unknown := server.call(t, surface.method, surface.path(acmeCRMType+"m"), globex, surface.body)
		if hidden.Code != http.StatusNotFound {
			t.Errorf("%s as globex = %d, want 404 (body: %s)", surface.name, hidden.Code, hidden.Body)
		}
		if unknown.Code != http.StatusNotFound {
			t.Errorf("%s for an unregistered type = %d, want 404 (body: %s)", surface.name, unknown.Code, unknown.Body)
		}
		if hidden.Body.String() != unknown.Body.String() {
			t.Errorf("%s body for a scoped type = %s, want the unregistered body %s",
				surface.name, hidden.Body, unknown.Body)
		}
	}

	// Positive controls: every one of those surfaces does answer for acme.
	icon := server.call(t, http.MethodGet, "/api/v1/node-types/"+acmeCRMType+"/icon", acme, nil)
	if icon.Code != http.StatusOK {
		t.Fatalf("icon as acme = %d, want 200 (body: %s)", icon.Code, icon.Body)
	}
	if got := icon.Header().Get("Cache-Control"); !strings.HasPrefix(got, "private") {
		t.Errorf("scoped icon Cache-Control = %q, want a private directive: it is served for one tenant only", got)
	}
	options := server.call(t, http.MethodPost, "/api/v1/node-types/"+acmeCRMType+"/load-options", acme,
		map[string]any{"property": "account"})
	if options.Code != http.StatusOK {
		t.Fatalf("load-options as acme = %d, want 200 (body: %s)", options.Code, options.Body)
	}
	if !strings.Contains(options.Body.String(), "acme") {
		t.Errorf("load-options body = %s, want the loader's answer for acme", options.Body)
	}
	// Not a resource mapper, so the column list is refused — but refused at
	// 422, which proves the type itself resolved.
	schema := server.call(t, http.MethodPost, "/api/v1/node-types/"+acmeCRMType+"/load-schema", acme,
		map[string]any{"property": "account"})
	if schema.Code != http.StatusUnprocessableEntity {
		t.Fatalf("load-schema as acme = %d, want 422 (body: %s)", schema.Code, schema.Body)
	}

	// An unscoped node's icon stays shareable.
	shared := server.call(t, http.MethodGet, "/api/v1/node-types/"+sharedPingType+"/icon", globex, nil)
	if shared.Code != http.StatusOK {
		t.Fatalf("unscoped icon = %d, want 200 (body: %s)", shared.Code, shared.Body)
	}
	if got, want := shared.Header().Get("Cache-Control"), "public, max-age=86400, immutable"; got != want {
		t.Errorf("unscoped icon Cache-Control = %q, want %q", got, want)
	}
}

// The catalogue is assembled per caller, so a shared cache must not hold it.
func TestNodeTypesResponseIsNotSharedCacheable(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	recorder := server.call(t, http.MethodGet, "/api/v1/node-types", keys["acme"], nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /node-types = %d, want 200", recorder.Code)
	}
	if got, want := recorder.Header().Get("Cache-Control"), "private, no-cache"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

// activationProblem decodes the compile problem an activation or run answers
// with, in the shape the editor reads.
type activationProblem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Errors []struct {
		Message string `json:"message"`
		Value   struct {
			Code string `json:"code"`
		} `json:"value"`
	} `json:"errors"`
}

func activationProblemOf(t *testing.T, server *authenticatedAPI, key, method, path string, body any, wantStatus int) activationProblem {
	t.Helper()
	recorder := server.call(t, method, path, key, body)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s = %d, want %d (body: %s)", method, path, recorder.Code, wantStatus, recorder.Body)
	}
	var problem activationProblem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode %s %s problem: %v (body: %s)", method, path, err, recorder.Body)
	}
	return problem
}

// codesOf lists the compile codes a problem carries.
func codesOf(problem activationProblem) []string {
	codes := make([]string, 0, len(problem.Errors))
	for _, issue := range problem.Errors {
		codes = append(codes, issue.Value.Code)
	}
	return codes
}

func TestTheCompilerSaysNotAvailableToYouNotUnknown(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	// A draft is stored whatever it references — the editor renders unknown
	// types — so the refusal arrives at activation.
	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Copied", acmeCRMType)), http.StatusCreated)
	copiedID, _ := copied["id"].(string)

	problem := activationProblemOf(t, server, keys["globex"], http.MethodPost,
		"/api/v1/workflows/"+copiedID+"/activate", nil, http.StatusUnprocessableEntity)
	if got := codesOf(problem); !containsString(got, "node.not_available") {
		t.Errorf("activation codes = %v, want node.not_available", got)
	}
	if len(problem.Errors) == 0 {
		t.Fatalf("activation problem = %#v, want at least one issue", problem)
	}
	if !strings.Contains(problem.Errors[0].Message, "not available to this workspace") {
		t.Errorf("activation problem = %#v, want it to say the node is not available", problem)
	}

	misspelt := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Misspelt", acmeCRMType+"m")), http.StatusCreated)
	misspeltID, _ := misspelt["id"].(string)
	typoProblem := activationProblemOf(t, server, keys["globex"], http.MethodPost,
		"/api/v1/workflows/"+misspeltID+"/activate", nil, http.StatusUnprocessableEntity)
	if got := codesOf(typoProblem); !containsString(got, "node.unknown_type") {
		t.Errorf("misspelt activation codes = %v, want node.unknown_type", got)
	}

	// The same document is perfectly valid for the tenant the pack was given.
	own := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["acme"],
		workflowDraft(crmWorkflow("Own", acmeCRMType)), http.StatusCreated)
	ownID, _ := own["id"].(string)
	activated := server.call(t, http.MethodPost, "/api/v1/workflows/"+ownID+"/activate", keys["acme"], nil)
	if activated.Code != http.StatusOK {
		t.Fatalf("acme activation = %d, want 200 (body: %s)", activated.Code, activated.Body)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// A run refuses the document it would compile, not the draft it was asked to
// run: a workflow copied from another tenant must not execute the node it
// cannot use, and nothing may be queued for a worker to find.
func TestARunRefusesAWorkflowCopiedFromAnotherTenant(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	copied := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Copied", acmeCRMType)), http.StatusCreated)
	copiedID, _ := copied["id"].(string)

	problem := activationProblemOf(t, server, keys["globex"], http.MethodPost,
		"/api/v1/workflows/"+copiedID+"/run", nil, http.StatusUnprocessableEntity)
	if got := codesOf(problem); !containsString(got, "node.not_available") {
		t.Errorf("run codes = %v, want node.not_available", got)
	}
	listed := server.callJSON(t, http.MethodGet, "/api/v1/executions?workflowId="+copiedID, keys["globex"], nil, http.StatusOK)
	if items, _ := listed["items"].([]any); len(items) != 0 {
		t.Fatalf("globex executions = %#v, want nothing queued", listed["items"])
	}

	// acme's identical document is queued: 202 is the whole claim here, since
	// this harness has no worker and no test.exec executor.
	own := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["acme"],
		workflowDraft(crmWorkflow("Own", acmeCRMType)), http.StatusCreated)
	ownID, _ := own["id"].(string)
	queued := server.call(t, http.MethodPost, "/api/v1/workflows/"+ownID+"/run", keys["acme"], nil)
	if queued.Code != http.StatusAccepted {
		t.Fatalf("acme run = %d, want 202 (body: %s)", queued.Code, queued.Body)
	}

	// An embedded editor with run scope on its own workflow is refused the
	// same way: the session's tenant is globex's.
	token := mintEmbedSessionAs(t, server, keys["globex"], copiedID, "workflow:run")
	embedded := embedRequest(t, server.handler, token, http.MethodPost, "/api/v1/workflows/"+copiedID+"/run", nil)
	if embedded.Code != http.StatusUnprocessableEntity {
		t.Fatalf("embed run = %d, want 422 (body: %s)", embedded.Code, embedded.Body)
	}
	if !strings.Contains(embedded.Body.String(), "node.not_available") {
		t.Errorf("embed run body = %s, want node.not_available", embedded.Body)
	}
}

// Nothing existing changes: an unscoped pack is offered to every tenant, and a
// workflow using one activates and queues exactly as before.
func TestAnUnscopedPackBehavesAsToday(t *testing.T) {
	server, keys := newVisibilityAPI(t)

	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", keys["globex"],
		workflowDraft(crmWorkflow("Ping", sharedPingType)), http.StatusCreated)
	id, _ := created["id"].(string)

	activated := server.call(t, http.MethodPost, "/api/v1/workflows/"+id+"/activate", keys["globex"], nil)
	if activated.Code != http.StatusOK {
		t.Fatalf("activation = %d, want 200 (body: %s)", activated.Code, activated.Body)
	}
	queued := server.call(t, http.MethodPost, "/api/v1/workflows/"+id+"/run", keys["globex"], nil)
	if queued.Code != http.StatusAccepted {
		t.Fatalf("run = %d, want 202 (body: %s)", queued.Code, queued.Body)
	}
}
