package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// nestedLoaderNode keeps a loader below the top level, as a node does whose
// setting lives inside an Options collection (n8n stores Columns to Watch there)
// or inside a group of a fixed collection.
func nestedLoaderNode() node.Definition {
	return node.Definition{
		Type: "test.nested", Version: workflow.V(1),
		DisplayName: "Nested", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{
			{Key: "document", Label: "Document", Kind: node.PropertyString},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{{
					Key: "watched", Label: "Watched", Kind: node.PropertyMultiOptions,
					LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: "test.echo", DependsOn: []string{"document", "options"}},
				}},
			},
			{
				Key: "rules", Label: "Rules", Kind: node.PropertyFixedCollection,
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Rule",
					Fields: []node.PropertyDefinition{{
						Key: "column", Label: "Column", Kind: node.PropertyOptions,
						LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: "test.echo", DependsOn: []string{"document"}},
					}},
				}},
			},
		},
	}
}

func newNestedAPI(t *testing.T) http.Handler {
	t.Helper()
	registry := node.NewRegistry()
	if err := registry.Register(nestedLoaderNode()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	// The loader answers with what it was handed, so the test sees the
	// dependencies the endpoint built.
	if err := resolver.RegisterInternal("test.echo", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		options := []loadoptions.Option{}
		for _, key := range []string{"document", "options"} {
			if value, present := scope.Dependencies[key]; present {
				options = append(options, loadoptions.Option{Label: key, Value: value})
			}
		}
		return loadoptions.Result{Options: options}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}
	return newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry, OptionLoader: resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	})
}

type loadedOptions struct {
	Options []struct{ Label, Value string } `json:"options"`
}

// A property inside a collection or a fixed collection group is loadable by its
// own key, from the loader its definition declares: the request still never
// names a loader.
func TestALoaderBelowTheTopLevelIsFoundByItsOwnKey(t *testing.T) {
	handler := newNestedAPI(t)
	for _, property := range []string{"watched", "column"} {
		result := requestJSON[loadedOptions](t, handler, http.MethodPost, "/api/v1/node-types/test.nested/load-options",
			map[string]any{"property": property, "parameters": map[string]any{"document": "doc-1"}}, http.StatusOK)
		if len(result.Options) == 0 || result.Options[0].Label != "document" || result.Options[0].Value != "doc-1" {
			t.Errorf("%s: options = %#v, want the loader's answer built from the document", property, result.Options)
		}
	}
	requestProblem(t, handler, http.MethodPost, "/api/v1/node-types/test.nested/load-options",
		map[string]any{"property": "nope"}, http.StatusNotFound)
}

// A dependency that is a structure reaches the loader as its JSON, not as the
// text Go prints a map as, which no loader could read.
func TestAStructuredDependencyReachesTheLoaderAsJSON(t *testing.T) {
	handler := newNestedAPI(t)
	result := requestJSON[loadedOptions](t, handler, http.MethodPost, "/api/v1/node-types/test.nested/load-options",
		map[string]any{"property": "watched", "parameters": map[string]any{
			"document": "doc-1",
			"options":  map[string]any{"locationDefine": map[string]any{"values": map[string]any{"headerRow": 3}}},
		}}, http.StatusOK)
	var got string
	for _, option := range result.Options {
		if option.Label == "options" {
			got = option.Value
		}
	}
	if got != `{"locationDefine":{"values":{"headerRow":3}}}` {
		t.Errorf("options dependency = %q, want the compact JSON of the options collection", got)
	}
}
