package loadoptions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// Workflows answers the Execute Sub-workflow node's list mode.
//
// It constructs no outbound request at all — the list is this process's own
// storage — which is exactly why the egress policy and the credential scoping
// that defend an HTTP loader have nothing to say about it, and why the tenancy
// check has to be here instead. There is nothing else between a browser and
// every workflow in the installation.
//
// An embed session is bound to its own workflow, so it sees exactly that one:
// the panel needs the current value to render, and a session that could
// enumerate the rest would turn a picker into a directory of the tenant.
func Workflows(list func(context.Context, repository.TenantScope) ([]WorkflowOption, error)) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		if list == nil {
			return Result{}, fmt.Errorf("workflow storage is not available on this server")
		}
		if scope.TenantID == "" {
			return Result{}, fmt.Errorf("a workflow list needs a tenant")
		}
		found, err := list(ctx, repository.TenantScope{ID: scope.TenantID})
		if err != nil {
			return Result{}, fmt.Errorf("the workflow list could not be read")
		}
		options := make([]Option, 0, len(found))
		for _, candidate := range found {
			if scope.WorkflowID != "" && candidate.ID != scope.WorkflowID {
				continue
			}
			label := candidate.Name
			if !candidate.Active {
				// Said in the label rather than filtered out. A sub-workflow
				// call needs an active workflow, and a picker that hid the
				// inactive ones would leave the author hunting for a workflow
				// they know exists.
				label += " (inactive)"
			}
			options = append(options, Option{Label: label, Value: candidate.ID})
		}
		if len(options) == 0 && scope.WorkflowID != "" {
			return Result{Options: options, Reason: "this embedded editor can only see its own workflow"}, nil
		}
		return Result{Options: options}, nil
	}
}

// WorkflowOption is one listable workflow.
//
// A function returning these rather than the repository interface: an option
// loader has no business saving, activating or deleting anything, and a seam
// that could would eventually be used to.
type WorkflowOption struct {
	ID     string
	Name   string
	Active bool
}

// WorkflowInputsDependency is the parameter key the inputs loader reads: the
// resource locator holding the workflow a tool or call targets.
const WorkflowInputsDependency = "workflowId"

// WorkflowInputField is one field a called workflow's trigger declares it
// expects, in the trigger's own vocabulary (string, number, boolean, array,
// object or any).
type WorkflowInputField struct {
	Name string
	Type string
}

// WorkflowInputs answers a resource mapper's column list from the fields a
// target workflow's trigger declares.
//
// Read from the workflow's *active* revision, which is the one a call runs, so
// the editor never lists a field the model would not be offered at run time.
// declared is false for a target that takes whatever the caller sends: it has
// no fields to list, and the reason says so rather than leaving an empty form
// to be read as a fault.
//
// Like Workflows, it constructs no outbound request, so the tenancy check and
// the embed bound have to be here: an embed session is bound to its own
// workflow, and a mapper that described any other would be a way to read the
// shape of every workflow in the tenant.
func WorkflowInputs(describe func(ctx context.Context, tenant repository.TenantScope, workflowID string) (fields []WorkflowInputField, declared bool, err error)) SchemaLoader {
	return func(ctx context.Context, scope Scope) (property.MapperSchema, error) {
		if describe == nil {
			return property.MapperSchema{}, fmt.Errorf("workflow storage is not available on this server")
		}
		if scope.TenantID == "" {
			return property.MapperSchema{}, fmt.Errorf("a workflow's inputs need a tenant")
		}
		reference := strings.TrimSpace(scope.Dependencies[WorkflowInputsDependency])
		if reference == "" {
			return property.MapperSchema{}, fmt.Errorf("choose a workflow first")
		}
		if scope.WorkflowID != "" && reference != scope.WorkflowID {
			return property.MapperSchema{Reason: "this embedded editor can only see its own workflow"}, nil
		}
		found, declared, err := describe(ctx, repository.TenantScope{ID: scope.TenantID}, reference)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return property.MapperSchema{Reason: "this workflow is not active or no longer exists, so its inputs cannot be read"}, nil
			}
			return property.MapperSchema{}, fmt.Errorf("the workflow's inputs could not be read")
		}
		if !declared {
			return property.MapperSchema{Reason: "this workflow accepts whatever its caller sends and declares no input fields"}, nil
		}
		if len(found) == 0 {
			return property.MapperSchema{Reason: "this workflow's trigger declares no input fields"}, nil
		}
		fields := make([]property.MapperField, 0, len(found))
		for _, field := range found {
			fields = append(fields, property.MapperField{
				ID: field.Name, DisplayName: field.Name, Type: workflowInputMapperType(field.Type),
			})
		}
		return property.MapperSchema{Fields: fields}, nil
	}
}

// workflowInputMapperType renders a trigger's field type in the mapper's own
// vocabulary. An input declared as any (or anything unrecognised) has no type
// of its own, so its column is untyped rather than forced to text.
func workflowInputMapperType(declared string) string {
	normalised := strings.ToLower(strings.TrimSpace(declared))
	switch normalised {
	case "string", "number", "boolean", "array", "object":
		return normalised
	default:
		return ""
	}
}
