package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ErrSettingsContention reports that a tenant's settings kept changing under
// an update, so it gave up rather than overwrite a write it had not seen.
var ErrSettingsContention = errors.New("tenant settings changed concurrently")

// maxSettingsAttempts bounds how often UpdateTenantSettings re-reads and
// re-applies after losing the version check. Operators patch settings by hand;
// five losses in a row means something is writing in a loop, and the caller is
// better told than kept spinning.
const maxSettingsAttempts = 5

// ReadTenantSettings reads one tenant's settings document and version.
func (store *GORMAuthStore) ReadTenantSettings(ctx context.Context, id string) ([]byte, int64, error) {
	var model tenantModel
	err := store.db.WithContext(ctx).
		Select("id", "settings", "settings_version").
		Where("id = ?", id).
		Take(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read tenant settings: %w", err)
	}
	return model.Settings, model.SettingsVersion, nil
}

// UpdateTenantSettings applies a change to a tenant's settings document under
// a compare-and-swap on settings_version.
func (store *GORMAuthStore) UpdateTenantSettings(ctx context.Context, id string, apply func(current []byte) ([]byte, error)) (Tenant, error) {
	for attempt := 0; attempt < maxSettingsAttempts; attempt++ {
		var model tenantModel
		if err := store.db.WithContext(ctx).Where("id = ?", id).Take(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return Tenant{}, ErrNotFound
			}
			return Tenant{}, fmt.Errorf("read tenant settings: %w", err)
		}
		next, err := apply(model.Settings)
		if err != nil {
			return Tenant{}, fmt.Errorf("apply tenant settings: %w", err)
		}
		if len(next) == 0 {
			next = nil
		}
		if bytes.Equal(next, model.Settings) {
			// Nothing changed, so nothing is written and the version stays:
			// a client resending the same patch must not look like a change.
			return tenantFromModel(model), nil
		}
		now := store.now()
		// An untyped nil, so an emptied document is stored as NULL on every
		// driver rather than as an empty byte string.
		var stored any
		if next != nil {
			stored = next
		}
		// A plain map rather than gorm.Expr: a bytea value passed through an
		// expression is sent as text on PostgreSQL (BUG-br7ggc).
		result := store.db.WithContext(ctx).Model(&tenantModel{}).
			Where("id = ? AND settings_version = ?", id, model.SettingsVersion).
			Updates(map[string]any{
				"settings":         stored,
				"settings_version": model.SettingsVersion + 1,
				"updated_at":       now,
			})
		if result.Error != nil {
			return Tenant{}, fmt.Errorf("write tenant settings: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			model.Settings = next
			model.SettingsVersion++
			model.UpdatedAt = now
			return tenantFromModel(model), nil
		}
	}
	return Tenant{}, fmt.Errorf("%w: tenant %q", ErrSettingsContention, id)
}
