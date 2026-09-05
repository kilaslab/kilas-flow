package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
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
