package engine

import (
	"context"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// VariableSource supplies a tenant's variables, the `$vars` an execution reads.
// *repository.GORMVariableStore satisfies it.
type VariableSource interface {
	VariablesFor(ctx context.Context, tenant repository.TenantScope) (map[string]any, error)
}

// variablesFor reads the tenant's variables for one execution.
//
// A failed read fails the run rather than running it with an empty `$vars`:
// a workflow that reads `$vars.API_BASE` and gets nothing would call the wrong
// place, or nowhere, and report success.
func (service *Service) variablesFor(ctx context.Context, tenantID string) (map[string]any, error) {
	if service.variables == nil {
		return map[string]any{}, nil
	}
	variables, err := service.variables.VariablesFor(ctx, repository.TenantScope{ID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("read the tenant's variables: %w", err)
	}
	return variables, nil
}
