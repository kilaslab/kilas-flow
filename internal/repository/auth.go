package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Tenant is one customer of this deployment.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// User is a dashboard account.
type User struct {
	ID       string
	TenantID string
	Email    string
	Name     string
	// PasswordHash is populated only by FindUserForLogin. Every other read
	// leaves it empty, so a handler cannot accidentally serialise it into a
	// response by reaching for a field that happened to be filled in.
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DisabledAt   *time.Time
}

// APIKey describes a stored key without any part of its secret.
//
// There is deliberately no field that could hold the token or its hash: a
// listing cannot leak what the struct cannot carry.
type APIKey struct {
	ID         string
	TenantID   string
	Prefix     string
	Label      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Revoked reports whether the key has been withdrawn.
func (key APIKey) Revoked() bool { return key.RevokedAt != nil }

// AuthRepository is the persistence seam for identity.
//
// It is separate from the tenant-scoped repositories because two of its reads
// cannot be tenant-scoped by definition: authentication is what decides the
// tenant, so the lookup that resolves a key or an email has to run before one
// is known. Every method that runs after that point takes a TenantScope like
// everything else.
type AuthRepository interface {
	EnsureTenant(ctx context.Context, id, name string) (Tenant, error)
	GetTenant(ctx context.Context, id string) (Tenant, error)
	CreateUser(ctx context.Context, tenant TenantScope, email, name, passwordHash string) (User, error)
	// FindUserForLogin resolves an email across every tenant, because a login
	// form has no tenant to scope by.
	FindUserForLogin(ctx context.Context, email string) (User, error)
	CountUsers(ctx context.Context) (int64, error)
	CreateAPIKey(ctx context.Context, tenant TenantScope, label string) (APIKey, string, error)
	ListAPIKeys(ctx context.Context, tenant TenantScope) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, tenant TenantScope, id string) (APIKey, error)
	// AuthenticateAPIKey resolves a presented token to the key that owns it.
	// The stored hash never leaves this boundary.
	AuthenticateAPIKey(ctx context.Context, token string) (APIKey, error)
}

// GORMAuthStore is the GORM implementation of AuthRepository.
type GORMAuthStore struct {
	db  *gorm.DB
	now func() time.Time
}

var _ AuthRepository = (*GORMAuthStore)(nil)

// NewAuthStore constructs the identity persistence boundary.
func NewAuthStore(db *gorm.DB) *GORMAuthStore {
	return &GORMAuthStore{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock replaces the store's clock, for tests that need last-used and
// revocation timestamps to be predictable.
func (store *GORMAuthStore) WithClock(now func() time.Time) *GORMAuthStore {
	if now != nil {
		store.now = now
	}
	return store
}

// EnsureTenant creates a tenant if it does not exist and returns it either way.
//
// It is idempotent because the bootstrap path runs on every boot: an operator
// restarting the process must not get a second tenant, and must not get an
// error either.
func (store *GORMAuthStore) EnsureTenant(ctx context.Context, id, name string) (Tenant, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Tenant{}, fmt.Errorf("a tenant needs an ID")
	}
	if name == "" {
		name = id
	}
	now := store.now()
	model := tenantModel{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	// DoNothing rather than an upsert of the name: a tenant renamed through
	// some future admin surface must not be silently reset by the next restart
	// of a process still carrying the bootstrap name in its environment.
	if err := store.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
		Create(&model).Error; err != nil {
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return store.GetTenant(ctx, id)
}

// GetTenant reads one tenant.
func (store *GORMAuthStore) GetTenant(ctx context.Context, id string) (Tenant, error) {
	var model tenantModel
	if err := store.db.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Tenant{}, ErrNotFound
		}
		return Tenant{}, fmt.Errorf("read tenant: %w", err)
	}
	return Tenant{ID: model.ID, Name: model.Name, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}, nil
}

// CreateUser adds a dashboard account to a tenant.
func (store *GORMAuthStore) CreateUser(ctx context.Context, tenant TenantScope, email, name, passwordHash string) (User, error) {
	if tenant.ID == "" {
		return User{}, fmt.Errorf("a user needs a tenant")
	}
	email = NormalizeEmail(email)
	if email == "" || passwordHash == "" {
		return User{}, fmt.Errorf("a user needs an email address and a password")
	}
	id, err := workflow.NewID("usr")
	if err != nil {
		return User{}, err
	}
	now := store.now()
	model := userModel{
		ID: id, TenantID: tenant.ID, Email: email, PasswordHash: passwordHash,
		Name: strings.TrimSpace(name), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.db.WithContext(ctx).Omit("Tenant").Create(&model).Error; err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return userFromModel(model, false), nil
}

// CountUsers reports how many accounts exist, across every tenant.
//
// The bootstrap path uses it to decide whether this is a fresh installation, so
// a deployment that already has accounts is never handed a second owner from an
// environment variable someone forgot to remove.
func (store *GORMAuthStore) CountUsers(ctx context.Context) (int64, error) {
	var total int64
	if err := store.db.WithContext(ctx).Model(&userModel{}).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return total, nil
}

// FindUserForLogin resolves an email to the account behind it, hash included.
func (store *GORMAuthStore) FindUserForLogin(ctx context.Context, email string) (User, error) {
	var model userModel
	if err := store.db.WithContext(ctx).Where("email = ?", NormalizeEmail(email)).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return userFromModel(model, true), nil
}

// CreateAPIKey mints a tenant-scoped key and returns its token exactly once.
//
// The token is the second result rather than a field on APIKey so that it
// cannot travel any further than the one call site that has to render it: no
// listing, no read, and no later handler can reach a value that only exists
// here.
func (store *GORMAuthStore) CreateAPIKey(ctx context.Context, tenant TenantScope, label string) (APIKey, string, error) {
	if tenant.ID == "" {
		return APIKey{}, "", fmt.Errorf("an API key needs a tenant")
	}
	minted, err := auth.MintKey()
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := workflow.NewID("key")
	if err != nil {
		return APIKey{}, "", err
	}
	model := apiKeyModel{
		ID: id, TenantID: tenant.ID, Prefix: minted.Prefix, SecretHash: minted.Hash,
		Label: strings.TrimSpace(label), CreatedAt: store.now(),
	}
	if err := store.db.WithContext(ctx).Omit("Tenant").Create(&model).Error; err != nil {
		return APIKey{}, "", fmt.Errorf("create API key: %w", err)
	}
	return apiKeyFromModel(model), minted.Token, nil
}

// ListAPIKeys returns one tenant's keys, revoked ones included.
func (store *GORMAuthStore) ListAPIKeys(ctx context.Context, tenant TenantScope) ([]APIKey, error) {
	if tenant.ID == "" {
		return nil, fmt.Errorf("listing API keys needs a tenant")
	}
	var models []apiKeyModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ?", tenant.ID).Order("created_at DESC, id DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	keys := make([]APIKey, 0, len(models))
	for _, model := range models {
		keys = append(keys, apiKeyFromModel(model))
	}
	return keys, nil
}

// RevokeAPIKey withdraws a key from its own tenant.
//
// A key belonging to another tenant reports ErrNotFound rather than a refusal,
// so the endpoint cannot be used to discover which key IDs exist elsewhere.
func (store *GORMAuthStore) RevokeAPIKey(ctx context.Context, tenant TenantScope, id string) (APIKey, error) {
	if tenant.ID == "" {
		return APIKey{}, fmt.Errorf("revoking an API key needs a tenant")
	}
	var model apiKeyModel
	query := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, id)
	if err := query.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, fmt.Errorf("read API key: %w", err)
	}
	if model.RevokedAt == nil {
		revoked := store.now()
		if err := store.db.WithContext(ctx).Model(&apiKeyModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, id).
			Update("revoked_at", revoked).Error; err != nil {
			return APIKey{}, fmt.Errorf("revoke API key: %w", err)
		}
		model.RevokedAt = &revoked
	}
	return apiKeyFromModel(model), nil
}

// lastUsedResolution is how stale last_used_at is allowed to get.
//
// Writing it on every request would make an UPDATE part of authenticating, so
// a read-only workload would generate one write per call and every key row
// would become a contention point.
const lastUsedResolution = time.Minute

// AuthenticateAPIKey resolves a presented token to the key that owns it.
//
// A token that is malformed, unknown, revoked, or whose secret does not match
// all answer with the same error. Telling them apart would let a caller confirm
// that a prefix exists before attacking its secret.
func (store *GORMAuthStore) AuthenticateAPIKey(ctx context.Context, token string) (APIKey, error) {
	prefix, secret, ok := auth.SplitKey(token)
	if !ok {
		return APIKey{}, auth.ErrUnauthenticated
	}
	var model apiKeyModel
	if err := store.db.WithContext(ctx).Where("prefix = ?", prefix).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return APIKey{}, auth.ErrUnauthenticated
		}
		return APIKey{}, fmt.Errorf("read API key: %w", err)
	}
	if !auth.MatchKeySecret(secret, model.SecretHash) {
		return APIKey{}, auth.ErrUnauthenticated
	}
	if model.RevokedAt != nil {
		return APIKey{}, auth.ErrUnauthenticated
	}

	now := store.now()
	if model.LastUsedAt == nil || now.Sub(*model.LastUsedAt) >= lastUsedResolution {
		// Best effort: a key that authenticated correctly must not be refused
		// because recording that fact failed.
		if err := store.db.WithContext(ctx).Model(&apiKeyModel{}).
			Where("id = ?", model.ID).Update("last_used_at", now).Error; err == nil {
			model.LastUsedAt = &now
		}
	}
	return apiKeyFromModel(model), nil
}

// NormalizeEmail lowercases and trims an address.
//
// Case folding is ASCII-only, so it does not fold the Unicode local parts some
// providers accept; two addresses differing only in the case of a non-ASCII
// letter would be two accounts here.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func userFromModel(model userModel, withHash bool) User {
	user := User{
		ID: model.ID, TenantID: model.TenantID, Email: model.Email, Name: model.Name,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt, DisabledAt: model.DisabledAt,
	}
	if withHash {
		user.PasswordHash = model.PasswordHash
	}
	return user
}

func apiKeyFromModel(model apiKeyModel) APIKey {
	return APIKey{
		ID: model.ID, TenantID: model.TenantID, Prefix: model.Prefix, Label: model.Label,
		CreatedAt: model.CreatedAt, LastUsedAt: model.LastUsedAt, RevokedAt: model.RevokedAt,
	}
}
