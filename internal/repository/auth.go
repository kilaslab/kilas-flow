package repository

import (
	"context"
	"encoding/base64"
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

// OperatorTenantID names the tenant an operator's credential belongs to.
//
// Authority to provision customers is modelled as a tenant rather than as a
// role, because every piece of the puzzle already exists: the tenants table is
// real, a principal already carries a TenantID, and the gate is then one
// comparison. A role column would need a migration, a second authorization
// concept in a package whose whole point is that it has none (see
// internal/auth's package comment), and would be read by exactly one surface.
//
// The consequence to be honest about: any credential scoped to this tenant has
// the operator's authority. Widening the operator's reach is therefore adding a
// key or an account here, not editing a permission list.
const OperatorTenantID = "operator"

// ErrAlreadyExists reports a row whose identifying column is taken.
//
// It exists so a duplicate reaches a handler as a decision rather than as an
// unreadable driver error: the operator surface has to answer 409, and telling
// a duplicate apart from a foreign-key violation or a lost connection by
// matching on a message string would break the day either driver changes its
// wording.
var ErrAlreadyExists = errors.New("repository record already exists")

// TenantSummary is one tenant together with the number of accounts it holds.
//
// The count travels with the tenant rather than being fetched per row by the
// caller, so an operator listing a hundred customers issues one query and not
// one hundred and one.
type TenantSummary struct {
	Tenant
	UserCount int64
}

// DefaultAPIKeyPageSize and MaxAPIKeyPageSize bound the key listing.
//
// They match the schedules listing rather than being picked separately: a
// dashboard renders the same number of rows whichever resource it is paging,
// and one deployed maximum is easier to reason about than one per endpoint.
const (
	DefaultAPIKeyPageSize = 100
	MaxAPIKeyPageSize     = 500
)

// APIKeyFilter narrows a key listing.
type APIKeyFilter struct {
	Limit int
	// Cursor continues a previous listing. It is opaque to callers; only
	// ListAPIKeysPage may construct one.
	Cursor string
}

// APIKeyPage is one page of a tenant's keys, newest first, with the cursor for
// the next.
type APIKeyPage struct {
	Keys       []APIKey
	NextCursor string
}

// AuthRepository is the persistence seam for identity.
//
// It is separate from the tenant-scoped repositories because two of its reads
// cannot be tenant-scoped by definition: authentication is what decides the
// tenant, so the lookup that resolves a key or an email has to run before one
// is known. Every method that runs after that point takes a TenantScope like
// everything else.
type AuthRepository interface {
	EnsureTenant(ctx context.Context, id, name string) (Tenant, error)
	// CreateTenant adds a tenant that does not exist yet, refusing an ID that
	// is taken. EnsureTenant cannot answer that question: it is the bootstrap's
	// idempotent call and hands back the existing row instead.
	CreateTenant(ctx context.Context, id, name string) (Tenant, error)
	GetTenant(ctx context.Context, id string) (Tenant, error)
	// ListTenants is the operator's view: every tenant and its account count.
	ListTenants(ctx context.Context) ([]TenantSummary, error)
	CreateUser(ctx context.Context, tenant TenantScope, email, name, passwordHash string) (User, error)
	// FindUserForLogin resolves an email across every tenant, because a login
	// form has no tenant to scope by.
	FindUserForLogin(ctx context.Context, email string) (User, error)
	// ListUsers returns one tenant's accounts, never their password hashes.
	ListUsers(ctx context.Context, tenant TenantScope) ([]User, error)
	// SetUserDisabled stamps or clears an account's offboarding moment.
	SetUserDisabled(ctx context.Context, tenant TenantScope, userID string, disabled bool) (User, error)
	// SetUserPassword replaces an account's stored hash, which is what makes a
	// reset cut sessions that were minted under the old one.
	SetUserPassword(ctx context.Context, tenant TenantScope, userID, passwordHash string) (User, error)
	CountUsers(ctx context.Context) (int64, error)
	CreateAPIKey(ctx context.Context, tenant TenantScope, label string) (APIKey, string, error)
	// EnsureAPIKey adopts a token the caller supplies rather than minting one.
	// It exists for the operator credential, whose value lives in the
	// deployment's environment and therefore cannot be invented by the server.
	EnsureAPIKey(ctx context.Context, tenant TenantScope, label, token string) (APIKey, error)
	ListAPIKeys(ctx context.Context, tenant TenantScope) ([]APIKey, error)
	// ListAPIKeysPage is the bounded listing the API serves. ListAPIKeys stays
	// for the callers that want every key at once.
	ListAPIKeysPage(ctx context.Context, tenant TenantScope, filter APIKeyFilter) (APIKeyPage, error)
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

// CreateTenant adds a customer tenant, refusing an ID that is already taken.
//
// It is deliberately not EnsureTenant: an operator who posts a tenant that
// exists has to be told so, and a create that silently returns somebody else's
// tenant would look like it had provisioned a second customer when it had not.
func (store *GORMAuthStore) CreateTenant(ctx context.Context, id, name string) (Tenant, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Tenant{}, fmt.Errorf("a tenant needs an ID")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// Falling back to the ID rather than refusing, because a name is a
		// label the operator can add later and an unnamed tenant is still a
		// working one.
		name = id
	}
	now := store.now()
	model := tenantModel{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	if err := store.db.WithContext(ctx).Create(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return Tenant{}, fmt.Errorf("%w: tenant %q", ErrAlreadyExists, id)
		}
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	return Tenant{ID: model.ID, Name: model.Name, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}, nil
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

// ListTenants returns every tenant with its account count, oldest first.
//
// The operator tenant is included rather than filtered out: it is a real row
// that keys and accounts hang off, and hiding it would leave an operator
// looking at a listing that does not add up — a key they can see under one path
// with no tenant to explain it under another.
func (store *GORMAuthStore) ListTenants(ctx context.Context) ([]TenantSummary, error) {
	var models []tenantModel
	if err := store.db.WithContext(ctx).Order("created_at ASC, id ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	counts, err := store.userCountsByTenant(ctx)
	if err != nil {
		return nil, err
	}
	tenants := make([]TenantSummary, 0, len(models))
	for _, model := range models {
		tenants = append(tenants, TenantSummary{
			Tenant: Tenant{
				ID: model.ID, Name: model.Name,
				CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
			},
			UserCount: counts[model.ID],
		})
	}
	return tenants, nil
}

// userCountsByTenant counts accounts per tenant in one grouped read.
//
// A tenant with no accounts is absent from the result rather than present as
// zero; the caller reads a missing key as zero, which is what it is.
func (store *GORMAuthStore) userCountsByTenant(ctx context.Context) (map[string]int64, error) {
	type tenantCount struct {
		TenantID string
		Total    int64
	}
	var rows []tenantCount
	if err := store.db.WithContext(ctx).Model(&userModel{}).
		Select("tenant_id, COUNT(*) AS total").
		Group("tenant_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count users per tenant: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.TenantID] = row.Total
	}
	return counts, nil
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
		// The unique index on users.email is deployment-wide, so a duplicate
		// reports the same way whichever tenant asked for it: two accounts on
		// one address would leave login guessing which password to check.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return User{}, fmt.Errorf("%w: account %q", ErrAlreadyExists, email)
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return userFromModel(model, false), nil
}

// ListUsers returns one tenant's accounts, oldest first.
//
// The hash is left behind by userFromModel, so an operator listing an account
// cannot serialise something the read never brought into memory.
func (store *GORMAuthStore) ListUsers(ctx context.Context, tenant TenantScope) ([]User, error) {
	if tenant.ID == "" {
		return nil, fmt.Errorf("listing users needs a tenant")
	}
	var models []userModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ?", tenant.ID).Order("created_at ASC, id ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]User, 0, len(models))
	for _, model := range models {
		users = append(users, userFromModel(model, false))
	}
	return users, nil
}

// SetUserDisabled stamps or clears one account's offboarding moment.
//
// A timestamp rather than a delete, so the executions and workflows that
// account created keep an author an audit can name, and so re-enabling restores
// the same account instead of minting a second one that shares its address.
// Clearing the field is the absence of an offboarding, not another stamp: a
// later disable must record when it happened, and a cleared field is the only
// value that can say "enabled" without carrying a date that means something
// else.
func (store *GORMAuthStore) SetUserDisabled(ctx context.Context, tenant TenantScope, userID string, disabled bool) (User, error) {
	if tenant.ID == "" {
		return User{}, fmt.Errorf("disabling a user needs a tenant")
	}
	var model userModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenant.ID, userID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Another tenant's account reads as unknown rather than as a
			// refusal, so the endpoint cannot confirm which user IDs exist
			// outside the tenant the operator named.
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read user: %w", err)
	}
	var disabledAt *time.Time
	if disabled {
		now := store.now()
		disabledAt = &now
	}
	// Written unconditionally: disabling an account that already is, or
	// enabling one that is not, is the caller stating the state they want, and
	// making that a no-op would need a read-back decision for no gain.
	if err := store.db.WithContext(ctx).Model(&userModel{}).
		Where("tenant_id = ? AND id = ?", tenant.ID, userID).
		Update("disabled_at", disabledAt).Error; err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	model.DisabledAt = disabledAt
	return userFromModel(model, false), nil
}

// SetUserPassword replaces one account's stored hash.
//
// The hash, never the password: a boundary that accepted the plaintext would be
// the one place in the system a password exists outside the login form.
func (store *GORMAuthStore) SetUserPassword(ctx context.Context, tenant TenantScope, userID, passwordHash string) (User, error) {
	if tenant.ID == "" {
		return User{}, fmt.Errorf("changing a password needs a tenant")
	}
	if passwordHash == "" {
		return User{}, fmt.Errorf("changing a password needs a hash")
	}
	var model userModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenant.ID, userID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if err := store.db.WithContext(ctx).Model(&userModel{}).
		Where("tenant_id = ? AND id = ?", tenant.ID, userID).
		Update("password_hash", passwordHash).Error; err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	// The model's own hash is not carried out of here: the returned User comes
	// from userFromModel with the hash dropped, exactly like every other read.
	model.PasswordHash = passwordHash
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

// EnsureAPIKey adopts a token the caller already holds.
//
// CreateAPIKey draws a secret this process invents, which is right for a
// dashboard action but useless for the operator credential: that value is in
// the deployment's environment before the process starts, so the server has to
// register what it was given rather than mint something nobody can present.
//
// The row is keyed by the token's own prefix and left untouched if it exists,
// so a restart neither disables the operator's key nor issues a second one they
// would never see. Neither the token nor its hash is returned: the caller
// supplied the secret and the store keeps only the hash, exactly as if it had
// minted it.
func (store *GORMAuthStore) EnsureAPIKey(ctx context.Context, tenant TenantScope, label, token string) (APIKey, error) {
	if tenant.ID == "" {
		return APIKey{}, fmt.Errorf("an API key needs a tenant")
	}
	prefix, secret, ok := auth.SplitKey(token)
	if !ok {
		return APIKey{}, fmt.Errorf(
			"an API key token must be shaped like %s_<hex prefix>_<secret>", auth.KeyVersion)
	}
	id, err := workflow.NewID("key")
	if err != nil {
		return APIKey{}, err
	}
	model := apiKeyModel{
		ID: id, TenantID: tenant.ID, Prefix: prefix, SecretHash: auth.HashKeySecret(secret),
		Label: strings.TrimSpace(label), CreatedAt: store.now(),
	}
	if err := store.db.WithContext(ctx).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "prefix"}}, DoNothing: true}).
		Omit("Tenant").Create(&model).Error; err != nil {
		return APIKey{}, fmt.Errorf("register API key: %w", err)
	}
	// Read back rather than trusting model: on a restart the row that exists is
	// the earlier one, and its ID, label and creation time are what the operator
	// should see.
	var stored apiKeyModel
	if err := store.db.WithContext(ctx).Where("prefix = ?", prefix).First(&stored).Error; err != nil {
		return APIKey{}, fmt.Errorf("read API key: %w", err)
	}
	if stored.TenantID != tenant.ID {
		// The prefix was already taken by another tenant's key, so nothing was
		// written. Reporting that is the only safe answer: the alternative is
		// handing the operator a key that authenticates as somebody else, or a
		// success they would only discover was false when the key was refused.
		return APIKey{}, fmt.Errorf("%w: API key prefix %s already belongs to another tenant",
			ErrAlreadyExists, prefix)
	}
	return apiKeyFromModel(stored), nil
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

// ListAPIKeysPage returns one page of a tenant's keys, newest first.
//
// The order is the one ListAPIKeys already uses — created_at then id, both
// descending — so a dashboard that switches between the two see the same rows
// in the same sequence. The id is the tiebreaker because two keys minted in the
// same millisecond are otherwise indistinguishable, and a page boundary landing
// between them would either repeat a row or skip one.
func (store *GORMAuthStore) ListAPIKeysPage(ctx context.Context, tenant TenantScope, filter APIKeyFilter) (APIKeyPage, error) {
	if tenant.ID == "" {
		return APIKeyPage{}, fmt.Errorf("listing API keys needs a tenant")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultAPIKeyPageSize
	}
	if limit > MaxAPIKeyPageSize {
		limit = MaxAPIKeyPageSize
	}

	query := store.db.WithContext(ctx).Model(&apiKeyModel{}).Where("tenant_id = ?", tenant.ID)
	if filter.Cursor != "" {
		createdAt, id, err := decodeAPIKeyCursor(filter.Cursor)
		if err != nil {
			return APIKeyPage{}, err
		}
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", createdAt, createdAt, id)
	}

	// One extra row tells us whether another page exists without a second COUNT
	// over the same predicate.
	var models []apiKeyModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return APIKeyPage{}, fmt.Errorf("list API keys: %w", err)
	}

	page := APIKeyPage{Keys: make([]APIKey, 0, limit)}
	if len(models) > limit {
		last := models[limit-1]
		page.NextCursor = encodeAPIKeyCursor(last.CreatedAt, last.ID)
		models = models[:limit]
	}
	for _, model := range models {
		page.Keys = append(page.Keys, apiKeyFromModel(model))
	}
	return page, nil
}

// encodeAPIKeyCursor pins the last (created_at, id) pair seen, keeping the zone
// the row was read in.
//
// Keys are stamped from NewAuthStore's clock, which is UTC, so the zone is UTC
// today and normalising it would be a no-op — but only by coincidence. The
// SQLite driver renders a bound time.Time with that value's own offset, so the
// moment either end of that pair changes, a cursor converted to UTC binds text
// the column does not hold, matches nothing, and returns an empty second page
// rather than an error. Carrying the zone makes the bound value the text the
// column holds, which is what ORDER BY itself compares; the schedule listing
// documents the same trap from the other side.
func encodeAPIKeyCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.Format(time.RFC3339Nano) + "\x00" + id))
}

// decodeAPIKeyCursor inverts encodeAPIKeyCursor. A cursor this store did not
// issue is ErrInvalidCursor, which the API answers as a 400 rather than paging
// from a position nobody handed out.
func decodeAPIKeyCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: API key cursor is malformed", ErrInvalidCursor)
	}
	timestamp, id, found := strings.Cut(string(decoded), "\x00")
	if !found || id == "" {
		return time.Time{}, "", fmt.Errorf("%w: API key cursor is malformed", ErrInvalidCursor)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: API key cursor is malformed", ErrInvalidCursor)
	}
	return createdAt, id, nil
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
