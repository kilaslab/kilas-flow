package loadoptions

import (
	"context"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/repository"
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
