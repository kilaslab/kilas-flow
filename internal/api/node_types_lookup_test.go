package api_test

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// loaderNode is a node whose properties are given, each loadable by an internal
// loader named after what it is for.
func loaderNode(nodeType string, parameters ...node.PropertyDefinition) node.Definition {
	return node.Definition{
		Type: nodeType, Version: workflow.V(1),
		DisplayName: nodeType, Category: "Test", ExecutorID: "test.exec",
		Group:      []node.NodeGroup{node.GroupTransform},
		Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: parameters,
	}
}

func loaded(key, loader string) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: key, Label: key, Kind: node.PropertyOptions,
		LoadOptions: &node.OptionsLoader{Source: property.LoaderInternal, Name: loader},
	}
}

func collectionOf(key string, fields ...node.PropertyDefinition) node.PropertyDefinition {
	return node.PropertyDefinition{Key: key, Label: key, Kind: node.PropertyCollection, Fields: fields}
}

func groupOf(key string, fields ...node.PropertyDefinition) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: key, Label: key, Kind: node.PropertyFixedCollection,
		Groups: []node.PropertyGroup{{Key: "values", Label: "Values", Fields: fields}},
	}
}

// lookupAPI registers the given nodes and one internal loader per name in
// loaders; each answers with its own name, so a test sees which one ran.
func lookupAPI(t *testing.T, definitions []node.Definition, loaders ...string) http.Handler {
	t.Helper()
	registry := node.NewRegistry()
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.Type, err)
		}
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	for _, name := range loaders {
		if err := resolver.RegisterInternal(name, func(context.Context, loadoptions.Scope) (loadoptions.Result, error) {
			return loadoptions.Result{Options: []loadoptions.Option{{Label: name, Value: name}}}, nil
		}); err != nil {
			t.Fatalf("RegisterInternal(%s) error = %v", name, err)
		}
	}
	return newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry, OptionLoader: resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	})
}

func answeredBy(t *testing.T, handler http.Handler, nodeType, key string) string {
	t.Helper()
	result := requestJSON[loadedOptions](t, handler, http.MethodPost, "/api/v1/node-types/"+nodeType+"/load-options",
		map[string]any{"property": key}, http.StatusOK)
	if len(result.Options) != 1 {
		t.Fatalf("%s.%s: options = %#v, want the one loader's answer", nodeType, key, result.Options)
	}
	return result.Options[0].Value
}

// Property keys inside a collection are looked up after the node's own, in the
// order the definition declares them, so a request can never reach for a nested
// loader when a top-level one owns the key.
func TestAKeyThatIsBothTopLevelAndNestedIsAnsweredByTheTopLevelProperty(t *testing.T) {
	handler := lookupAPI(t, []node.Definition{
		// The nested one is declared first, so declaration order alone would pick it.
		loaderNode("test.shadow",
			collectionOf("options", loaded("column", "test.nested")),
			loaded("column", "test.top")),
	}, "test.top", "test.nested")

	if got := answeredBy(t, handler, "test.shadow", "column"); got != "test.top" {
		t.Errorf("column was answered by %q, want the top-level property's loader", got)
	}
}

func TestTwoNestedPropertiesWithOneKeyAreAnsweredByTheFirstDeclared(t *testing.T) {
	handler := lookupAPI(t, []node.Definition{
		loaderNode("test.twins",
			collectionOf("options", loaded("column", "test.first")),
			groupOf("rules", loaded("column", "test.second"))),
		loaderNode("test.twins.reversed",
			groupOf("rules", loaded("column", "test.second")),
			collectionOf("options", loaded("column", "test.first"))),
	}, "test.first", "test.second")

	if got := answeredBy(t, handler, "test.twins", "column"); got != "test.first" {
		t.Errorf("column was answered by %q, want the first declared", got)
	}
	if got := answeredBy(t, handler, "test.twins.reversed", "column"); got != "test.second" {
		t.Errorf("column was answered by %q, want the first declared (the group is declared first here)", got)
	}
}

// The lookup is by key alone, so a key that two loader-bearing properties share
// would be answered by one of them and never the other. The shipped catalogue
// has none; this keeps it so.
func TestNoShippedNodeHasTwoLoaderBearingPropertiesWithOneKey(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("shippedcatalogue.Register() error = %v", err)
	}
	for _, definition := range registry.List() {
		seen := map[string]int{}
		var walk func(properties []node.PropertyDefinition)
		walk = func(properties []node.PropertyDefinition) {
			for _, declared := range properties {
				bearsLoader := declared.LoadOptions != nil || (declared.Mapper != nil && declared.Mapper.Schema != nil)
				for _, mode := range declared.Modes {
					bearsLoader = bearsLoader || mode.LoadOptions != nil
				}
				if bearsLoader {
					seen[declared.Key]++
				}
				walk(declared.Fields)
				for _, group := range declared.Groups {
					walk(group.Fields)
				}
			}
		}
		walk(definition.Parameters)

		shared := []string{}
		for key, count := range seen {
			if count > 1 {
				shared = append(shared, key)
			}
		}
		sort.Strings(shared)
		if len(shared) > 0 {
			t.Errorf("%s v%s declares more than one loader-bearing property with the key %v; the load-options "+
				"lookup is by key and would answer only one of them", definition.Type, definition.Version, shared)
		}
	}
}
