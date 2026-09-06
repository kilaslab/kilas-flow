package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// CredentialRepository is the persistence seam for stored secrets.
//
// Every method takes a tenant scope, and the encrypted payload only leaves
// this boundary through Resolve, which the runtime calls when it is about to
// authenticate a request. List and Get deliberately cannot return plaintext.
type CredentialRepository interface {
	Create(context.Context, TenantScope, credentials.Record) (credentials.Record, error)
	Update(context.Context, TenantScope, string, credentials.Record) (credentials.Record, error)
	List(context.Context, TenantScope) ([]credentials.Record, error)
	Get(context.Context, TenantScope, string) (credentials.Record, error)
	Delete(context.Context, TenantScope, string) error
	// Resolve returns the decrypted payload for runtime use only.
	Resolve(context.Context, TenantScope, string) (credentials.Record, map[string]string, error)
}

// GORMCredentialStore persists credentials with their payload encrypted.
type GORMCredentialStore struct {
	db     *gorm.DB
	cipher *credentials.Cipher
	// external resolves ext:// references held by secret fields. Nil until
	// SetExternalResolver attaches one, and resolution without it fails
	// closed: a reference is never returned as plaintext.
	external *credentials.Resolver
}

var _ CredentialRepository = (*GORMCredentialStore)(nil)

// NewCredentialStore constructs the credential persistence boundary. Without a
// cipher the store refuses every operation rather than storing plaintext.
func NewCredentialStore(db *gorm.DB, cipher *credentials.Cipher) *GORMCredentialStore {
	return &GORMCredentialStore{db: db, cipher: cipher}
}

// Create seals and stores a new credential.
func (store *GORMCredentialStore) Create(ctx context.Context, tenant TenantScope, record credentials.Record) (credentials.Record, error) {
	if err := store.ready(tenant); err != nil {
		return credentials.Record{}, err
	}
	if err := validateCredential(record); err != nil {
		return credentials.Record{}, err
	}
	id, err := workflow.NewID("cred")
	if err != nil {
		return credentials.Record{}, err
	}
	secret, public := credentials.Split(record.Type, record.Fields)
	if err := rejectPublicReferences(public); err != nil {
		return credentials.Record{}, err
	}
	sealed, err := store.cipher.Encrypt(secret)
	if err != nil {
		return credentials.Record{}, err
	}
	publicFields, err := json.Marshal(public)
	if err != nil {
		return credentials.Record{}, fmt.Errorf("encode credential fields: %w", err)
	}
	domains, err := json.Marshal(normalizeDomains(record.AllowedDomains))
	if err != nil {
		return credentials.Record{}, fmt.Errorf("encode allowed domains: %w", err)
	}
	now := time.Now().UTC()
	model := credentialModel{
		ID: id, TenantID: tenant.ID, Name: strings.TrimSpace(record.Name), Type: record.Type,
		Payload: sealed, PublicFields: publicFields, AllowedDomains: domains, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&model).Error; err != nil {
		return credentials.Record{}, fmt.Errorf("create credential: %w", err)
	}
	store.external.InvalidateTenant(tenant.ID)
	return credentialFromModel(model)
}

// Update replaces a credential's name, scope, and payload.
//
// A secret field left at the redaction placeholder keeps its stored value, so
// editing a name never silently blanks a password the client was never given.
func (store *GORMCredentialStore) Update(ctx context.Context, tenant TenantScope, credentialID string, record credentials.Record) (credentials.Record, error) {
	if err := store.ready(tenant); err != nil {
		return credentials.Record{}, err
	}
	var model credentialModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, credentialID).First(&model).Error; err != nil {
		return credentials.Record{}, mapNotFound(err, "credential")
	}
	if record.Type != "" && record.Type != model.Type {
		return credentials.Record{}, fmt.Errorf("credential type cannot be changed")
	}
	record.Type = model.Type

	stored, err := store.cipher.Decrypt(model.Payload)
	if err != nil {
		return credentials.Record{}, err
	}
	merged := make(map[string]string, len(record.Fields))
	for key, value := range record.Fields {
		if value == credentials.RedactedValue {
			merged[key] = stored[key]
			continue
		}
		merged[key] = value
	}
	record.Fields = merged
	if err := validateCredential(record); err != nil {
		return credentials.Record{}, err
	}

	secret, public := credentials.Split(record.Type, merged)
	if err := rejectPublicReferences(public); err != nil {
		return credentials.Record{}, err
	}
	sealed, err := store.cipher.Encrypt(secret)
	if err != nil {
		return credentials.Record{}, err
	}
	publicFields, err := json.Marshal(public)
	if err != nil {
		return credentials.Record{}, fmt.Errorf("encode credential fields: %w", err)
	}
	domains, err := json.Marshal(normalizeDomains(record.AllowedDomains))
	if err != nil {
		return credentials.Record{}, fmt.Errorf("encode allowed domains: %w", err)
	}
	model.Name = strings.TrimSpace(record.Name)
	model.Payload = sealed
	model.PublicFields = publicFields
	model.AllowedDomains = domains
	model.UpdatedAt = time.Now().UTC()
	if err := store.db.WithContext(ctx).Save(&model).Error; err != nil {
		return credentials.Record{}, fmt.Errorf("update credential: %w", err)
	}
	// The reference a secret field holds may have changed, so cached values
	// resolved under this tenant are dropped rather than trusted.
	store.external.InvalidateTenant(tenant.ID)
	return credentialFromModel(model)
}

// List returns every credential in the tenant without any payload.
func (store *GORMCredentialStore) List(ctx context.Context, tenant TenantScope) ([]credentials.Record, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []credentialModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ?", tenant.ID).Order("name ASC, id ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	records := make([]credentials.Record, 0, len(models))
	for _, model := range models {
		record, err := credentialFromModel(model)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Get returns one credential's metadata without its payload.
func (store *GORMCredentialStore) Get(ctx context.Context, tenant TenantScope, credentialID string) (credentials.Record, error) {
	if err := tenant.validate(); err != nil {
		return credentials.Record{}, err
	}
	var model credentialModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, credentialID).First(&model).Error; err != nil {
		return credentials.Record{}, mapNotFound(err, "credential")
	}
	return credentialFromModel(model)
}

// Resolve returns the decrypted payload. It is the single path by which a
// plaintext secret leaves storage, and only the runtime calls it.
func (store *GORMCredentialStore) Resolve(ctx context.Context, tenant TenantScope, credentialID string) (credentials.Record, map[string]string, error) {
	if err := store.ready(tenant); err != nil {
		return credentials.Record{}, nil, err
	}
	var model credentialModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, credentialID).First(&model).Error; err != nil {
		return credentials.Record{}, nil, mapNotFound(err, "credential")
	}
	record, err := credentialFromModel(model)
	if err != nil {
		return credentials.Record{}, nil, err
	}
	fields, err := store.cipher.Decrypt(model.Payload)
	if err != nil {
		return credentials.Record{}, nil, err
	}
	// The executor needs the complete payload, so the plaintext half is merged
	// back in here rather than at every call site.
	for key, value := range record.Fields {
		if _, sealed := fields[key]; !sealed {
			fields[key] = value
		}
	}
	return store.resolveExternal(ctx, tenant, record, fields)
}

// Delete removes a credential from the tenant.
func (store *GORMCredentialStore) Delete(ctx context.Context, tenant TenantScope, credentialID string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	result := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, credentialID).Delete(&credentialModel{})
	if result.Error != nil {
		return fmt.Errorf("delete credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: credential", ErrNotFound)
	}
	store.external.InvalidateTenant(tenant.ID)
	return nil
}

func (store *GORMCredentialStore) ready(tenant TenantScope) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if store.cipher == nil {
		return fmt.Errorf("credential encryption is not configured")
	}
	return nil
}

func validateCredential(record credentials.Record) error {
	if strings.TrimSpace(record.Name) == "" {
		return fmt.Errorf("credential name is required")
	}
	return credentials.Validate(record.Type, record.Fields)
}

func normalizeDomains(domains []string) []string {
	normalized := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			normalized = append(normalized, domain)
		}
	}
	return normalized
}

// credentialFromModel maps storage to a record without its payload. Callers
// that legitimately need plaintext go through Resolve.
func credentialFromModel(model credentialModel) (credentials.Record, error) {
	var domains []string
	if len(model.AllowedDomains) > 0 {
		if err := json.Unmarshal(model.AllowedDomains, &domains); err != nil {
			return credentials.Record{}, fmt.Errorf("decode allowed domains: %w", err)
		}
	}
	public := map[string]string{}
	if len(model.PublicFields) > 0 {
		if err := json.Unmarshal(model.PublicFields, &public); err != nil {
			return credentials.Record{}, fmt.Errorf("decode credential fields: %w", err)
		}
	}
	// Fields carries only the non-secret half. A caller that needs the secret
	// goes through Resolve, which is the one path that uses the cipher.
	return credentials.Record{
		ID: model.ID, TenantID: model.TenantID, Name: model.Name, Type: model.Type,
		Fields: public, AllowedDomains: domains, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}, nil
}

// SetExternalResolver attaches external secret reference resolution to the
// store. Until it is called, any credential holding an ext:// reference fails
// closed on Resolve: the reference is never returned as plaintext, and no new
// route exists by which a secret leaves storage.
func (store *GORMCredentialStore) SetExternalResolver(resolver *credentials.Resolver) {
	store.external = resolver
}

// LookupBinding implements credentials.BindingStore: one tenant's binding by
// name. The tenant scopes the query, so a reference authored under one tenant
// cannot read a secret bound to another — the row is simply not found.
func (store *GORMCredentialStore) LookupBinding(ctx context.Context, tenantID, name string) (credentials.Binding, error) {
	var model secretBindingModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return credentials.Binding{}, fmt.Errorf("%w: %q", credentials.ErrUnknownBinding, name)
		}
		return credentials.Binding{}, fmt.Errorf("lookup secret binding: %w", err)
	}
	return secretBindingFromModel(model), nil
}

// CreateSecretBinding stores one tenant's manager binding. The row names the
// environment variable holding the manager credential, never the credential.
func (store *GORMCredentialStore) CreateSecretBinding(ctx context.Context, tenant TenantScope, binding credentials.Binding) (credentials.Binding, error) {
	if err := tenant.validate(); err != nil {
		return credentials.Binding{}, err
	}
	binding.TenantID = tenant.ID
	binding.Name = strings.TrimSpace(binding.Name)
	binding.Provider = strings.TrimSpace(binding.Provider)
	binding.Address = strings.TrimSpace(binding.Address)
	binding.TokenEnv = strings.TrimSpace(binding.TokenEnv)
	if err := binding.Validate(); err != nil {
		return credentials.Binding{}, err
	}
	id, err := workflow.NewID("sbnd")
	if err != nil {
		return credentials.Binding{}, err
	}
	now := time.Now().UTC()
	model := secretBindingModel{
		ID: id, TenantID: tenant.ID, Name: binding.Name, Provider: binding.Provider,
		Address: binding.Address, TokenEnv: binding.TokenEnv, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&model).Error; err != nil {
		return credentials.Binding{}, fmt.Errorf("create secret binding: %w", err)
	}
	store.external.InvalidateBinding(tenant.ID, binding.Name)
	return secretBindingFromModel(model), nil
}

// GetSecretBinding returns one tenant's binding without any secret: the row
// holds none, only the variable naming it.
func (store *GORMCredentialStore) GetSecretBinding(ctx context.Context, tenant TenantScope, name string) (credentials.Binding, error) {
	if err := tenant.validate(); err != nil {
		return credentials.Binding{}, err
	}
	return store.LookupBinding(ctx, tenant.ID, name)
}

// ListSecretBindings returns every binding of one tenant.
func (store *GORMCredentialStore) ListSecretBindings(ctx context.Context, tenant TenantScope) ([]credentials.Binding, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []secretBindingModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ?", tenant.ID).Order("name ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list secret bindings: %w", err)
	}
	bindings := make([]credentials.Binding, 0, len(models))
	for _, model := range models {
		bindings = append(bindings, secretBindingFromModel(model))
	}
	return bindings, nil
}

// UpdateSecretBinding replaces a binding's provider, address and manager
// credential variable. The name is the identity references use, so renaming
// is refused: every ext://<name>/… stored anywhere would otherwise dangle.
func (store *GORMCredentialStore) UpdateSecretBinding(ctx context.Context, tenant TenantScope, name string, binding credentials.Binding) (credentials.Binding, error) {
	if err := tenant.validate(); err != nil {
		return credentials.Binding{}, err
	}
	var model secretBindingModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenant.ID, name).First(&model).Error; err != nil {
		return credentials.Binding{}, mapNotFound(err, "secret binding")
	}
	if binding.Name != "" && binding.Name != name {
		return credentials.Binding{}, fmt.Errorf("secret binding %q cannot be renamed to %q", name, binding.Name)
	}
	binding.TenantID = tenant.ID
	binding.Name = name
	binding.Provider = strings.TrimSpace(binding.Provider)
	binding.Address = strings.TrimSpace(binding.Address)
	binding.TokenEnv = strings.TrimSpace(binding.TokenEnv)
	if err := binding.Validate(); err != nil {
		return credentials.Binding{}, err
	}
	model.Provider = binding.Provider
	model.Address = binding.Address
	model.TokenEnv = binding.TokenEnv
	model.UpdatedAt = time.Now().UTC()
	if err := store.db.WithContext(ctx).Save(&model).Error; err != nil {
		return credentials.Binding{}, fmt.Errorf("update secret binding: %w", err)
	}
	store.external.InvalidateBinding(tenant.ID, name)
	return secretBindingFromModel(model), nil
}

// DeleteSecretBinding removes one tenant's binding. Cached values it supplied
// are dropped, so the next Resolve fails closed rather than serving a secret
// whose binding no longer exists.
func (store *GORMCredentialStore) DeleteSecretBinding(ctx context.Context, tenant TenantScope, name string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	result := store.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenant.ID, name).Delete(&secretBindingModel{})
	if result.Error != nil {
		return fmt.Errorf("delete secret binding: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: secret binding", ErrNotFound)
	}
	store.external.InvalidateBinding(tenant.ID, name)
	return nil
}

// resolveExternal resolves ext:// references after decryption and before the
// plaintext leaves storage, so the one documented exit path stays the one
// exit path. Values are replaced in the returned map only: the stored row
// keeps the sealed reference, and a failure anywhere returns no fields at
// all rather than a half-resolved payload or the reference as plaintext.
func (store *GORMCredentialStore) resolveExternal(ctx context.Context, tenant TenantScope, record credentials.Record, fields map[string]string) (credentials.Record, map[string]string, error) {
	referenced := false
	for _, value := range fields {
		if credentials.IsReference(value) {
			referenced = true
			break
		}
	}
	if !referenced {
		return record, fields, nil
	}
	if store.external == nil {
		return credentials.Record{}, nil, fmt.Errorf("credential %q holds an external reference but no secret manager is configured", record.Name)
	}
	resolved := make(map[string]string, len(fields))
	for key, value := range fields {
		fetched, err := store.external.ResolveField(ctx, tenant.ID, value)
		if err != nil {
			return credentials.Record{}, nil, err
		}
		resolved[key] = fetched
	}
	return record, resolved, nil
}

// rejectPublicReferences refuses an ext:// value in a non-secret field. Public
// fields are stored in plaintext, so a reference there would disclose the
// binding path to any reader — and a secret reachable without the Resolve
// path is not a secret at all.
func rejectPublicReferences(public map[string]string) error {
	for key, value := range public {
		if credentials.IsReference(value) {
			return fmt.Errorf("credential field %q is not secret: an external reference needs a secret field", key)
		}
	}
	return nil
}

func secretBindingFromModel(model secretBindingModel) credentials.Binding {
	return credentials.Binding{
		TenantID: model.TenantID, Name: model.Name, Provider: model.Provider,
		Address: model.Address, TokenEnv: model.TokenEnv,
	}
}
