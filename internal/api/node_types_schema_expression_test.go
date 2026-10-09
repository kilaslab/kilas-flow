package api_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/nodes"
)

// A mapper whose columns depend on a value that only exists at run time cannot
// list its columns while editing: there is no item to evaluate the expression
// against. A self-referencing Workflow Tool imported from n8n is exactly that
// (its By ID value is {{ $workflow.id }}), and sending the marker's own JSON to
// the loader as the workflow id answers a question nobody asked.
func TestALoadSchemaDependingOnAnExpressionSaysSo(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	var mu sync.Mutex
	var dependencies []map[string]string
	if err := resolver.RegisterSchema(nodes.WorkflowInputsLoader, func(_ context.Context, scope loadoptions.Scope) (property.MapperSchema, error) {
		mu.Lock()
		defer mu.Unlock()
		dependencies = append(dependencies, scope.Dependencies)
		return property.MapperSchema{Fields: []property.MapperField{{ID: "tempC", DisplayName: "tempC", Type: "number"}}}, nil
	}); err != nil {
		t.Fatalf("RegisterSchema() error = %v", err)
	}
	handler := newTestServer(t, api.Deps{
		DB: stubPinger{}, NodeRegistry: registry, OptionLoader: resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	})
	expression := map[string]any{"mode": "expression", "value": "{{ $workflow.id }}"}
	load := func(workflowID any) (fields []any, reason string) {
		t.Helper()
		result := requestJSON[struct {
			Fields []any  `json:"fields"`
			Reason string `json:"reason"`
		}](t, handler, http.MethodPost, "/api/v1/node-types/"+nodes.WorkflowToolNodeType+"/load-schema",
			map[string]any{"property": "workflowInputs", "parameters": map[string]any{"workflowId": workflowID}}, http.StatusOK)
		return result.Fields, result.Reason
	}

	for name, value := range map[string]any{
		"an expression inside a By ID locator": map[string]any{"__rl": true, "mode": "id", "value": expression},
		"an expression as the whole locator":   expression,
	} {
		fields, reason := load(value)
		if len(fields) != 0 || !strings.Contains(reason, "expression") {
			t.Errorf("%s: fields %v reason %q, want no columns and a reason naming the expression", name, fields, reason)
		}
	}
	mu.Lock()
	asked := len(dependencies)
	mu.Unlock()
	if asked != 0 {
		t.Errorf("the loader ran %d times for a workflow that is an expression, want it never asked: %v", asked, dependencies)
	}

	fields, reason := load(map[string]any{"__rl": true, "mode": "list", "value": "wf_celsius"})
	if len(fields) != 1 || reason != "" {
		t.Fatalf("a chosen workflow: fields %v reason %q, want its columns", fields, reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dependencies) != 1 || dependencies[0]["workflowId"] != "wf_celsius" {
		t.Errorf("loader dependencies = %v, want workflowId wf_celsius", dependencies)
	}
}
