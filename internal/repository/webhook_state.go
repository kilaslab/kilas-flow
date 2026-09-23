package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// WebhookRouteStateStore keeps what a trigger's lifecycle captured, on the
// route it answers on.
//
// Every method takes the tenant as well as the route. A route is globally
// unique, so the route alone would find the row; the tenant is there so that
// no caller can read or overwrite another tenant's captured secret by naming
// its route.
type WebhookRouteStateStore interface {
	LifecycleState(ctx context.Context, tenant TenantScope, route string) (map[string]string, error)
	SaveLifecycleState(ctx context.Context, tenant TenantScope, route string, values map[string]string) error
	ClearLifecycleState(ctx context.Context, tenant TenantScope, route string) error
}

var _ WebhookRouteStateStore = (*GORMWorkflowStore)(nil)

// WithLifecycleState returns a store that keeps lifecycle state, sealed with
// the credential cipher.
//
// Nil keeps none: a captured value may be the secret a service signs its
// deliveries with, and it is never written in the clear. Reading a route that
// has state then fails, rather than answering as though it had none.
func (store *GORMWorkflowStore) WithLifecycleState(cipher *credentials.Cipher) *GORMWorkflowStore {
	store.lifecycleCipher = cipher
	return store
}

// LifecycleState opens what one route's registration kept. A route with none,
// or no such route of this tenant's, has kept nothing.
func (store *GORMWorkflowStore) LifecycleState(ctx context.Context, tenant TenantScope, route string) (map[string]string, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	return store.lifecycleState(ctx, tenant.ID, route)
}

// SaveLifecycleState seals the values onto one route, replacing what it held.
func (store *GORMWorkflowStore) SaveLifecycleState(ctx context.Context, tenant TenantScope, route string, values map[string]string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if len(values) == 0 {
		return store.ClearLifecycleState(ctx, tenant, route)
	}
	if store.lifecycleCipher == nil {
		return fmt.Errorf("lifecycle state is not kept: it is sealed with the credential encryption key, which is not configured")
	}
	sealed, err := store.lifecycleCipher.Encrypt(values)
	if err != nil {
		return fmt.Errorf("seal lifecycle state: %w", err)
	}
	result := store.db.WithContext(ctx).Model(&webhookRouteModel{}).
		Where("tenant_id = ? AND route = ?", tenant.ID, route).
		Update("lifecycle_state", sealed)
	if result.Error != nil {
		return fmt.Errorf("save lifecycle state: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return mapNotFound(gorm.ErrRecordNotFound, "webhook route")
	}
	return nil
}

// ClearLifecycleState forgets what one route's registration kept. It needs no
// key, and clearing a route that kept nothing is not an error.
func (store *GORMWorkflowStore) ClearLifecycleState(ctx context.Context, tenant TenantScope, route string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if err := store.db.WithContext(ctx).Model(&webhookRouteModel{}).
		Where("tenant_id = ? AND route = ?", tenant.ID, route).
		Update("lifecycle_state", gorm.Expr("NULL")).Error; err != nil {
		return fmt.Errorf("clear lifecycle state: %w", err)
	}
	return nil
}

// lifecycleState reads and opens one route's state. Resolve calls it with the
// tenant its binding carries, so it takes the tenant's ID rather than a scope.
func (store *GORMWorkflowStore) lifecycleState(ctx context.Context, tenantID, route string) (map[string]string, error) {
	var rows []webhookRouteModel
	if err := store.db.WithContext(ctx).Select("lifecycle_state").
		Where("tenant_id = ? AND route = ?", tenantID, route).
		Limit(1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read lifecycle state: %w", err)
	}
	if len(rows) == 0 || len(rows[0].LifecycleState) == 0 {
		return nil, nil
	}
	if store.lifecycleCipher == nil {
		return nil, fmt.Errorf("this route's lifecycle state is sealed, and the credential encryption key is not configured")
	}
	values, err := store.lifecycleCipher.Decrypt(rows[0].LifecycleState)
	if err != nil {
		return nil, fmt.Errorf("open lifecycle state: %w", err)
	}
	return values, nil
}
