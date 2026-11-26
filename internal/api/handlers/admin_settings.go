package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

// TenantSettingsConflictCode names a settings update that kept losing to
// concurrent writes and gave up rather than overwrite one it had not seen.
const TenantSettingsConflictCode = "tenant_settings_conflict"

type updateTenantInput struct {
	ID   string `path:"id" minLength:"1" maxLength:"64" doc:"Tenant ID"`
	Body struct {
		// Settings is untyped on purpose: the sections are registered by
		// their consumers at runtime, and a typed schema would have the
		// framework refuse an unknown key before this handler could name it.
		Settings map[string]json.RawMessage `json:"settings,omitempty" doc:"A merge patch over the tenant's settings: one object per section, null to remove a section or a field"`
	}
}

type updateTenantOutput struct {
	Body TenantResource
}

// tenantSettingsIssue is the typed value attached to each refused key.
type tenantSettingsIssue struct {
	Code string `json:"code"`
}

// UpdateTenant merges a patch into a tenant's settings.
func (handler *Admin) UpdateTenant(ctx context.Context, input *updateTenantInput) (*updateTenantOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	registry := handler.settings.Registry()
	if registry == nil {
		return nil, huma.Error503ServiceUnavailable("tenant settings are not configured on this instance")
	}
	tenant, err := handler.store.UpdateTenantSettings(ctx, input.ID, func(current []byte) ([]byte, error) {
		return registry.Apply(current, input.Body.Settings)
	})
	if err != nil {
		var patchErr *tenantsettings.PatchError
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return nil, huma.Error404NotFound("tenant not found")
		case errors.As(err, &patchErr):
			return nil, tenantSettingsProblem(patchErr)
		case errors.Is(err, repository.ErrSettingsContention):
			return nil, tenantSettingsConflictProblem()
		}
		return nil, serverProblem(ctx, "could not update the tenant's settings", err)
	}
	handler.settings.Invalidate(tenant.ID)
	principal, _ := auth.PrincipalFrom(ctx)
	slog.InfoContext(ctx, "tenant settings updated",
		"tenant", tenant.ID,
		"principal_key", principal.KeyID,
		"settings_version", tenant.SettingsVersion,
	)
	summary, err := handler.tenantSummary(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	return &updateTenantOutput{Body: tenantResource(summary)}, nil
}

// tenantSettingsProblem is the 422 a refused patch answers with: one error per
// problem, located at the key it names and carrying its code.
func tenantSettingsProblem(patchErr *tenantsettings.PatchError) error {
	const detail = "the settings patch was refused; nothing was written"
	model := &huma.ErrorModel{
		Status: http.StatusUnprocessableEntity,
		Title:  http.StatusText(http.StatusUnprocessableEntity),
		Detail: detail,
	}
	for _, issue := range patchErr.Issues {
		model.Errors = append(model.Errors, &huma.ErrorDetail{
			Location: "body.settings." + issue.Path,
			Message:  issue.Message,
			Value:    tenantSettingsIssue{Code: issue.Code},
		})
	}
	return model
}

func tenantSettingsConflictProblem() error {
	const detail = "the tenant's settings kept changing during the update; read them again and retry"
	return &huma.ErrorModel{
		Status: http.StatusConflict,
		Title:  http.StatusText(http.StatusConflict),
		Detail: detail,
		Errors: []*huma.ErrorDetail{{
			Message: detail,
			Value:   tenantSettingsIssue{Code: TenantSettingsConflictCode},
		}},
	}
}
