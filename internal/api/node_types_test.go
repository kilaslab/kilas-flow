package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func TestNodeTypesServesTheRegisteredCatalogue(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, NodeRegistry: registry})

	recorder := get(t, handler, "/api/v1/node-types")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /node-types status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}

	var definitions []node.Definition
	if err := json.NewDecoder(recorder.Body).Decode(&definitions); err != nil {
		t.Fatalf("decode node catalogue = %v", err)
	}
	if len(definitions) == 0 {
		t.Fatal("node catalogue is empty")
	}
	// The API must serve the registry's stable order, whatever is registered.
	for index := 1; index < len(definitions); index++ {
		if definitions[index-1].Type > definitions[index].Type {
			t.Fatalf("catalogue is not in stable order at %d: %q then %q", index, definitions[index-1].Type, definitions[index].Type)
		}
	}
	if definitions[0].ExecutorID != "" {
		t.Errorf("executor binding leaked in API response = %q", definitions[0].ExecutorID)
	}
}

func TestTheNodeCatalogueSaysWhatThisDeploymentCannotRun(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	// A user must never discover at run time that their deployment cannot
	// compile: the editor learns it from the catalogue, before the workflow is
	// saved rather than after it runs.
	handler := newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry,
		NodeAvailability: func() map[string]string {
			return map[string]string{nodes.CodeNodeType: "This deployment has no Go compiler."}
		},
	})

	definitions := requestJSON[[]struct {
		Type        string `json:"type"`
		Unavailable string `json:"unavailable"`
	}](t, handler, http.MethodGet, "/api/v1/node-types", nil, http.StatusOK)

	seen := false
	for _, definition := range definitions {
		switch definition.Type {
		case nodes.CodeNodeType:
			seen = true
			if definition.Unavailable == "" {
				t.Errorf("the Code node reports no reason it cannot run")
			}
		default:
			if definition.Unavailable != "" {
				t.Errorf("%s was marked unavailable: %q", definition.Type, definition.Unavailable)
			}
		}
	}
	if !seen {
		t.Fatal("the Code node is not in the catalogue")
	}
}

// locatorNode is a node whose one parameter is a resource locator with an
// internal list mode — the shape this endpoint has to bound.
func locatorNode() node.Definition {
	return node.Definition{
		Type: "test.locator", Version: workflow.V(1),
		DisplayName: "Locator", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{{
			Key: "target", Label: "Target", Kind: node.PropertyResourceLocator,
			Modes: []node.PropertyMode{
				{
					Name: "list", Label: "From list", Kind: node.PropertyOptions,
					LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: "test.targets"},
				},
				{Name: "id", Label: "By ID", Kind: node.PropertyString},
			},
		}},
	}
}

func newLocatorAPI(t *testing.T, issuer *embed.Issuer) http.Handler {
	t.Helper()
	registry := node.NewRegistry()
	if err := registry.Register(locatorNode()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterInternal("test.targets", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		return loadoptions.Result{Options: []loadoptions.Option{{Label: scope.WorkflowID, Value: scope.WorkflowID}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}
	return newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry, OptionLoader: resolver, EmbedIssuer: issuer,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	})
}

func TestALocatorLoadsTheListOfTheModeThatAsked(t *testing.T) {
	handler := newLocatorAPI(t, nil)

	// A locator carries a loader per mode, so the request says which mode is
	// asking — and the server takes the loader from the declared mode, never
	// from the request.
	result := requestJSON[struct {
		Options []struct{ Value string } `json:"options"`
	}](t, handler, http.MethodPost, "/api/v1/node-types/test.locator/load-options",
		map[string]any{"property": "target", "mode": "list", "workflowId": "wf_1"}, http.StatusOK)
	if len(result.Options) != 1 || result.Options[0].Value != "wf_1" {
		t.Errorf("options = %#v, want the internal loader's answer", result.Options)
	}

	// A mode that offers no list is a 422, not an empty answer: "there is no
	// list here" and "the list is empty" are different things.
	requestProblem(t, handler, http.MethodPost, "/api/v1/node-types/test.locator/load-options",
		map[string]any{"property": "target", "mode": "id"}, http.StatusUnprocessableEntity)
	requestProblem(t, handler, http.MethodPost, "/api/v1/node-types/test.locator/load-options",
		map[string]any{"property": "target", "mode": "nonsense"}, http.StatusUnprocessableEntity)
}

func TestAnEmbedSessionCannotLoadOptionsForAnotherWorkflow(t *testing.T) {
	issuer := embedIssuer(t)
	handler := newLocatorAPI(t, issuer)

	const own = "wf_embedded"
	const other = "wf_someone_else"
	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: own,
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	// An internal loader constructs no request, so none of the defences that
	// guard an outbound one apply — and permits allows the whole /node-types/
	// subtree on read scope without reading a body. This is the only check
	// between an embedded editor and every workflow in the tenant.
	refused := embedRequest(t, handler, token, http.MethodPost, "/api/v1/node-types/test.locator/load-options",
		map[string]any{"property": "target", "mode": "list", "workflowId": other})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", refused.Code, refused.Body)
	}
	// The message names both, so the caller knows which session it is holding
	// rather than only that something was refused.
	for _, want := range []string{own, other} {
		if !strings.Contains(refused.Body.String(), want) {
			t.Errorf("body = %s, want it to name %q", refused.Body, want)
		}
	}

	// Its own workflow is fine, and so is a request naming nothing — which is
	// what the panel sends, since the session already says which workflow.
	for _, body := range []map[string]any{
		{"property": "target", "mode": "list", "workflowId": own},
		{"property": "target", "mode": "list"},
	} {
		allowed := embedRequest(t, handler, token, http.MethodPost, "/api/v1/node-types/test.locator/load-options", body)
		if allowed.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", allowed.Code, allowed.Body)
		}
		if !strings.Contains(allowed.Body.String(), own) {
			t.Errorf("body = %s, want the list scoped to %q", allowed.Body, own)
		}
	}
}

// mapperNode is a node whose one parameter is a resource mapper over an
// internal schema source.
func mapperNode() node.Definition {
	return node.Definition{
		Type: "test.mapper", Version: workflow.V(1),
		DisplayName: "Mapper", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{{
			Key: "columns", Label: "Columns", Kind: node.PropertyResourceMapper,
			Mapper: &node.ResourceMapperDeclaration{
				Schema:          &node.OptionsLoader{Source: property.LoaderInternal, Name: "test.columns"},
				SupportsAutoMap: true,
			},
		}},
	}
}

func TestTheSchemaResponseCarriesEachColumnsTypeRequiredAndMatchEligibility(t *testing.T) {
	registry := node.NewRegistry()
	if err := registry.Register(mapperNode()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterSchema("test.columns", func(context.Context, loadoptions.Scope) (property.MapperSchema, error) {
		return property.MapperSchema{Fields: []property.MapperField{
			{ID: "id", DisplayName: "id", Type: "number", CanBeUsedToMatch: true, DefaultMatch: true, ReadOnly: true},
			{ID: "email", DisplayName: "email", Type: "string", Required: true, CanBeUsedToMatch: true},
			// A type this build has no control for. It has to survive the
			// response so the editor can render it as text and say so; dropping
			// it would read as "this table has no such column".
			{ID: "geo", DisplayName: "geo", Type: "geography"},
		}}, nil
	}); err != nil {
		t.Fatalf("RegisterSchema() error = %v", err)
	}
	handler := newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry, OptionLoader: resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	})

	result := requestJSON[struct {
		Fields []struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Required         bool   `json:"required"`
			CanBeUsedToMatch bool   `json:"canBeUsedToMatch"`
			DefaultMatch     bool   `json:"defaultMatch"`
			ReadOnly         bool   `json:"readOnly"`
		} `json:"fields"`
	}](t, handler, http.MethodPost, "/api/v1/node-types/test.mapper/load-schema",
		map[string]any{"property": "columns"}, http.StatusOK)

	if len(result.Fields) != 3 {
		t.Fatalf("fields = %#v, want every column including the one with an unknown type", result.Fields)
	}
	if !result.Fields[0].ReadOnly || !result.Fields[0].DefaultMatch || result.Fields[0].Type != "number" {
		t.Errorf("id = %#v, want its type, read-only and default-match flags", result.Fields[0])
	}
	if !result.Fields[1].Required || !result.Fields[1].CanBeUsedToMatch {
		t.Errorf("email = %#v, want its required and match flags", result.Fields[1])
	}
	if result.Fields[2].Type != "geography" {
		t.Errorf("geo = %#v, want the unrecognised type carried rather than blanked", result.Fields[2])
	}

	// A property that is not a mapper has no column list, which is a different
	// answer from an empty one.
	requestProblem(t, handler, http.MethodPost, "/api/v1/node-types/test.mapper/load-schema",
		map[string]any{"property": "nope"}, http.StatusNotFound)
}

// credentialAwareLoaderAPI is a node whose picker needs a database credential,
// with a workflow the embed session is scoped to.
func credentialAwareLoaderAPI(t *testing.T, issuer *embed.Issuer) (http.Handler, *repository.GORMWorkflowStore, *repository.GORMCredentialStore) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loaders.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	credentialStore := repository.NewCredentialStore(db.DB, cipher)
	workflowStore := repository.NewWorkflowStore(db.DB)

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := registry.Register(node.Definition{
		Type: "test.picker", Version: workflow.V(1),
		DisplayName: "Picker", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{{
			Key: "table", Label: "Table", Kind: node.PropertyString,
			LoadOptions: &node.OptionsLoader{
				Source: property.LoaderInternal, Name: "test.tables", CredentialType: "postgres",
			},
		}},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterInternal("test.tables", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		// Echoes the credential it was handed, so a test can see which one
		// reached the loader rather than only whether the call succeeded.
		return loadoptions.Result{Options: []loadoptions.Option{{Label: scope.Credential.Record.Name, Value: scope.Credential.Fields["host"]}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}

	handler := newTestServer(t, api.Deps{
		DB: db, NodeRegistry: registry, OptionLoader: resolver, EmbedIssuer: issuer,
		Workflows: workflowStore, Credentials: credentialStore,
		// Activation complains that the workflow service is unavailable without
		// an execution store, and the loader bound is derived from the revision
		// an activation published — so the test has to be able to activate.
		Executions: repository.NewExecutionStore(db.DB),
		CredentialResolverFor: func(tenant repository.TenantScope) loadoptions.CredentialResolver {
			return credentialLookup{store: credentialStore, tenant: tenant}
		},
	})
	return handler, workflowStore, credentialStore
}

// credentialLookup binds credential resolution to one tenant, the way the
// composition root does.
type credentialLookup struct {
	store  *repository.GORMCredentialStore
	tenant repository.TenantScope
}

func (lookup credentialLookup) Resolve(ctx context.Context, id string) (credentials.Record, map[string]string, error) {
	return lookup.store.Resolve(ctx, lookup.tenant, id)
}

func TestALoaderResolvesItsCredentialInTheCallersTenant(t *testing.T) {
	handler, _, store := credentialAwareLoaderAPI(t, nil)
	ctx := context.Background()

	mine, err := store.Create(ctx, repository.TenantScope{ID: repository.DefaultTenantID}, credentials.Record{
		Name: "Mine", Type: "postgres",
		Fields: map[string]string{"host": "mine.example", "database": "app", "user": "ada", "password": "x"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A credential owned by a second tenant, which exists and must not be
	// reachable from the first.
	theirs, err := store.Create(ctx, repository.TenantScope{ID: "tenant-b"}, credentials.Record{
		Name: "Theirs", Type: "postgres",
		Fields: map[string]string{"host": "theirs.example", "database": "app", "user": "ada", "password": "x"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result := requestJSON[struct {
		Options []struct{ Value string } `json:"options"`
	}](t, handler, http.MethodPost, "/api/v1/node-types/test.picker/load-options",
		map[string]any{"property": "table", "credentialId": mine.ID}, http.StatusOK)
	if len(result.Options) != 1 || result.Options[0].Value != "mine.example" {
		t.Fatalf("options = %#v, want the caller's own credential used", result.Options)
	}

	// Another tenant's credential does not exist here. The message says so and
	// nothing more: anything more specific would confirm it exists somewhere.
	recorder := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"property": "table", "credentialId": theirs.ID})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/node-types/test.picker/load-options", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatalf("another tenant's credential was used: %s", recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "theirs.example") {
		t.Fatalf("the response disclosed the other tenant's credential: %s", recorder.Body)
	}
}

// embedLoaderDocument is a workflow whose one node attaches a credential, so a
// session minted from it may read with exactly that credential.
//
// The workflow id is threaded so a second draft can be saved over the first,
// which is how the test writes a revision the session's authority does not
// come from.
func embedLoaderDocument(workflowID, credentialID string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: workflowID, Name: "Embedded",
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "sql", Name: "SQL", Type: nodes.PostgresNodeType, TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"operation": "query", "statement": "SELECT 1"},
				Credentials: map[string]string{"postgres": credentialID}},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
			Target: workflow.Endpoint{NodeID: "sql", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// A load-options request carries a credential id from a session that holds no
// authority of its own, and a schema read is performed on behalf of whoever
// holds the editor: "this session may read" and "this session may read *with
// that credential*" are two different permissions, and only the second one
// bounds the blast radius.
//
// The bound is the session's confinement, which was minted from the revision
// the workflow's owner published. It used to be a fresh read of the workflow's
// latest draft: a draft is inside a guest's write authority, so a guest could
// attach any credential to the draft and drive the internal loaders with a
// secret the published revision never named — while saving and running that
// same draft was refused.
func TestAnEmbedSessionMayOnlyLoadWithItsOwnWorkflowsCredentials(t *testing.T) {
	issuer := embedIssuer(t)
	handler, workflows, store := credentialAwareLoaderAPI(t, issuer)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: repository.DefaultTenantID}

	used, err := store.Create(ctx, tenant, credentials.Record{
		Name: "Used", Type: "postgres",
		Fields: map[string]string{"host": "used.example", "database": "app", "user": "ada", "password": "x"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	unused, err := store.Create(ctx, tenant, credentials.Record{
		Name: "Unused", Type: "postgres",
		Fields: map[string]string{"host": "unused.example", "database": "app", "user": "ada", "password": "x"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The owner publishes a revision that references exactly one of them. That
	// revision is the session's whole authority.
	stored, err := workflows.SaveDraft(ctx, tenant, embedLoaderDocument("", used.ID))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	requestJSON[workflowResource](t, handler, http.MethodPost, "/api/v1/workflows/"+stored.ID+"/activate", nil, http.StatusOK)

	// Then the workflow's latest draft references the other one — a draft that
	// nobody published.
	if _, err := workflows.SaveDraft(ctx, tenant, embedLoaderDocument(stored.ID, unused.ID)); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	token := mintEmbedSession(t, handler, stored.ID, "workflow:read")
	session, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(session.Confinement.Credentials) != 1 || session.Confinement.Credentials[0] != used.ID {
		t.Fatalf("confinement = %#v, want the published revision's one credential", session.Confinement)
	}

	allowed := embedRequest(t, handler, token, http.MethodPost, "/api/v1/node-types/test.picker/load-options",
		map[string]any{"property": "table", "credentialId": used.ID})
	if allowed.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", allowed.Code, allowed.Body)
	}
	if !strings.Contains(allowed.Body.String(), "used.example") {
		t.Errorf("body = %s, want the workflow's own credential used", allowed.Body)
	}

	// The credential the unpublished draft carries is refused: the session may
	// not read with a secret its published revision never named, whatever the
	// workflow's latest draft happens to say.
	refused := embedRequest(t, handler, token, http.MethodPost, "/api/v1/node-types/test.picker/load-options",
		map[string]any{"property": "table", "credentialId": unused.ID})
	if refused.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", refused.Code, refused.Body)
	}
	if strings.Contains(refused.Body.String(), "unused.example") {
		t.Fatalf("the refusal disclosed the credential it refused: %s", refused.Body)
	}
}
