package loadoptions_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

func workflowInputsResolver(t *testing.T, describe func(ctx context.Context, tenant repository.TenantScope, workflowID string) ([]loadoptions.WorkflowInputField, bool, error)) (*loadoptions.Resolver, property.OptionsLoader) {
	t.Helper()
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterSchema("workflows.inputs", loadoptions.WorkflowInputs(describe)); err != nil {
		t.Fatalf("RegisterSchema() error = %v", err)
	}
	return resolver, property.OptionsLoader{
		Source: property.LoaderInternal, Name: "workflows.inputs",
		DependsOn: []string{loadoptions.WorkflowInputsDependency},
	}
}

func TestWorkflowInputsLoaderListsTheTargetsDeclaredFields(t *testing.T) {
	t.Parallel()

	var seenTenant, seenWorkflow string
	resolver, loader := workflowInputsResolver(t, func(_ context.Context, tenant repository.TenantScope, workflowID string) ([]loadoptions.WorkflowInputField, bool, error) {
		seenTenant, seenWorkflow = tenant.ID, workflowID
		return []loadoptions.WorkflowInputField{
			{Name: "tempC", Type: "number"},
			{Name: "unit", Type: "string"},
			{Name: "verbose", Type: "boolean"},
			{Name: "tags", Type: "array"},
			{Name: "payload", Type: "object"},
			{Name: "anything", Type: "any"},
		}, true, nil
	})
	schema, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", Dependencies: map[string]string{"workflowId": " wf_celsius "},
	}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	if seenTenant != "t1" || seenWorkflow != "wf_celsius" {
		t.Fatalf("describe(tenant=%q, workflow=%q), want the caller's tenant and the trimmed workflow", seenTenant, seenWorkflow)
	}
	want := []property.MapperField{
		{ID: "tempC", DisplayName: "tempC", Type: "number"},
		{ID: "unit", DisplayName: "unit", Type: "string"},
		{ID: "verbose", DisplayName: "verbose", Type: "boolean"},
		{ID: "tags", DisplayName: "tags", Type: "array"},
		{ID: "payload", DisplayName: "payload", Type: "object"},
		// An input declared as any has no type of its own, so the column carries none.
		{ID: "anything", DisplayName: "anything", Type: ""},
	}
	if fmt.Sprintf("%+v", schema.Fields) != fmt.Sprintf("%+v", want) {
		t.Fatalf("fields = %+v, want %+v", schema.Fields, want)
	}
	for _, field := range schema.Fields {
		if field.Required || field.CanBeUsedToMatch {
			t.Errorf("field %q = %+v: a workflow input is neither required nor a match column", field.ID, field)
		}
	}
}

func TestWorkflowInputsLoaderNeedsAWorkflow(t *testing.T) {
	t.Parallel()

	resolver, loader := workflowInputsResolver(t, func(context.Context, repository.TenantScope, string) ([]loadoptions.WorkflowInputField, bool, error) {
		t.Fatal("describe must not run without a workflow")
		return nil, false, nil
	})
	if _, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{TenantID: "t1"}, "", nil); err == nil {
		t.Error("LoadSchema() without a workflow = nil, want a refusal")
	}
	if _, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		Dependencies: map[string]string{"workflowId": "wf_1"},
	}, "", nil); err == nil {
		t.Error("LoadSchema() without a tenant = nil, want a refusal")
	}
}

func TestWorkflowInputsLoaderSaysWhyAnInactiveTargetHasNoFields(t *testing.T) {
	t.Parallel()

	resolver, loader := workflowInputsResolver(t, func(context.Context, repository.TenantScope, string) ([]loadoptions.WorkflowInputField, bool, error) {
		return nil, false, fmt.Errorf("%w: active workflow", repository.ErrNotFound)
	})
	schema, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", Dependencies: map[string]string{"workflowId": "wf_gone"},
	}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v, want a reason the user can act on rather than a failure", err)
	}
	if len(schema.Fields) != 0 || !strings.Contains(schema.Reason, "active") {
		t.Fatalf("schema = %+v, want no fields and a reason naming the active requirement", schema)
	}

	other, otherLoader := workflowInputsResolver(t, func(context.Context, repository.TenantScope, string) ([]loadoptions.WorkflowInputField, bool, error) {
		return nil, false, fmt.Errorf("database is on fire")
	})
	if _, err := other.LoadSchema(context.Background(), otherLoader, loadoptions.Scope{
		TenantID: "t1", Dependencies: map[string]string{"workflowId": "wf_1"},
	}, "", nil); err == nil || strings.Contains(err.Error(), "on fire") {
		t.Errorf("LoadSchema() error = %v, want a refusal that does not echo the storage error", err)
	}
}

func TestWorkflowInputsLoaderSaysWhyAPassthroughTargetHasNoFields(t *testing.T) {
	t.Parallel()

	resolver, loader := workflowInputsResolver(t, func(context.Context, repository.TenantScope, string) ([]loadoptions.WorkflowInputField, bool, error) {
		return nil, false, nil
	})
	schema, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", Dependencies: map[string]string{"workflowId": "wf_open"},
	}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	if len(schema.Fields) != 0 || schema.Reason == "" {
		t.Fatalf("schema = %+v, want no fields and a reason", schema)
	}
}

func TestWorkflowInputsLoaderEmbedSessionSeesOnlyItsOwnWorkflow(t *testing.T) {
	t.Parallel()

	resolver, loader := workflowInputsResolver(t, func(_ context.Context, _ repository.TenantScope, workflowID string) ([]loadoptions.WorkflowInputField, bool, error) {
		if workflowID != "wf_self" {
			t.Errorf("describe(%q): an embed session must not read another workflow", workflowID)
		}
		return []loadoptions.WorkflowInputField{{Name: "orderId", Type: "string"}}, true, nil
	})
	other, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", WorkflowID: "wf_self", Dependencies: map[string]string{"workflowId": "wf_other"},
	}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	if len(other.Fields) != 0 || other.Reason == "" {
		t.Fatalf("schema for another workflow = %+v, want no fields and a reason", other)
	}
	own, err := resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", WorkflowID: "wf_self", Dependencies: map[string]string{"workflowId": "wf_self"},
	}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	if len(own.Fields) != 1 || own.Fields[0].ID != "orderId" {
		t.Fatalf("schema for its own workflow = %+v, want orderId", own)
	}
}
