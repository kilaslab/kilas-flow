package repository

import (
	"context"
	"encoding/json"
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
	return record, fields, nil
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
