package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Variable is one of a tenant's key/value pairs, read by every workflow of the
// tenant as `$vars.KEY` — n8n's Settings → Variables.
//
// The value is plain text, as it is in n8n. A variable is configuration a
// workflow reads (a base URL, an environment name, a channel id), never a
// secret: it is stored unencrypted and shown to anyone who can list it, so a
// secret belongs in a credential.
type Variable struct {
	ID        string
	Key       string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// VariableKeyMaxLength and VariableValueMaxLength bound one variable.
const (
	VariableKeyMaxLength   = 64
	VariableValueMaxLength = 4096
	// VariablesPerTenantMax bounds a tenant's variables, because every
	// execution reads them all.
	VariablesPerTenantMax = 500
)

// variableKey is the shape an expression can read as `$vars.KEY` without
// bracket notation: a letter or underscore, then letters, digits and
// underscores. It is n8n's own rule.
var variableKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrVariableInvalid reports a key or value the store refuses.
var ErrVariableInvalid = errors.New("variable is invalid")

// VariableRepository is the persistence boundary for a tenant's variables.
type VariableRepository interface {
	ListVariables(ctx context.Context, tenant TenantScope) ([]Variable, error)
	CreateVariable(ctx context.Context, tenant TenantScope, key, value string) (Variable, error)
	UpdateVariable(ctx context.Context, tenant TenantScope, id, key, value string) (Variable, error)
	DeleteVariable(ctx context.Context, tenant TenantScope, id string) error
	// VariablesFor returns the tenant's variables as the `$vars` map an
	// execution reads.
	VariablesFor(ctx context.Context, tenant TenantScope) (map[string]any, error)
}

// GORMVariableStore stores variables in the variables table (000033).
type GORMVariableStore struct {
	db *gorm.DB
}

var _ VariableRepository = (*GORMVariableStore)(nil)

// NewVariableStore constructs the variable persistence boundary.
func NewVariableStore(db *gorm.DB) *GORMVariableStore {
	return &GORMVariableStore{db: db}
}

type variableModel struct {
	ID        string    `gorm:"primaryKey;size:64"`
	TenantID  string    `gorm:"not null;size:64;uniqueIndex:uidx_variables_tenant_key,priority:1"`
	Key       string    `gorm:"column:key;not null;size:64;uniqueIndex:uidx_variables_tenant_key,priority:2"`
	Value     string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (variableModel) TableName(namer schema.Namer) string {
	return namer.TableName("variables")
}

func variableFromModel(model variableModel) Variable {
	return Variable{
		ID: model.ID, Key: model.Key, Value: model.Value,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}

// ValidateVariable checks a key and value the way the store will, so the API
// can answer a field-level 422 before writing anything.
func ValidateVariable(key, value string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: key is required", ErrVariableInvalid)
	case len(key) > VariableKeyMaxLength:
		return fmt.Errorf("%w: key is longer than %d characters", ErrVariableInvalid, VariableKeyMaxLength)
	case !variableKey.MatchString(key):
		return fmt.Errorf("%w: key must start with a letter or underscore and contain only letters, digits and underscores", ErrVariableInvalid)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: value is not valid UTF-8", ErrVariableInvalid)
	case len(value) > VariableValueMaxLength:
		return fmt.Errorf("%w: value is longer than %d bytes", ErrVariableInvalid, VariableValueMaxLength)
	}
	return nil
}

// ListVariables returns every variable in the tenant, ordered by key.
//
// Unpaged on purpose: VariablesPerTenantMax bounds the set, and every
// execution reads the whole of it anyway.
func (store *GORMVariableStore) ListVariables(ctx context.Context, tenant TenantScope) ([]Variable, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []variableModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ?", tenant.ID).Order("key ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list variables: %w", err)
	}
	variables := make([]Variable, 0, len(models))
	for _, model := range models {
		variables = append(variables, variableFromModel(model))
	}
	return variables, nil
}

// CreateVariable stores a new variable. A key the tenant already has is
// ErrAlreadyExists.
func (store *GORMVariableStore) CreateVariable(ctx context.Context, tenant TenantScope, key, value string) (Variable, error) {
	if err := tenant.validate(); err != nil {
		return Variable{}, err
	}
	key = strings.TrimSpace(key)
	if err := ValidateVariable(key, value); err != nil {
		return Variable{}, err
	}
	id, err := workflow.NewID("var")
	if err != nil {
		return Variable{}, err
	}
	now := time.Now().UTC()
	model := variableModel{ID: id, TenantID: tenant.ID, Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&variableModel{}).Where("tenant_id = ?", tenant.ID).Count(&count).Error; err != nil {
			return fmt.Errorf("count variables: %w", err)
		}
		if count >= VariablesPerTenantMax {
			return fmt.Errorf("%w: a tenant holds at most %d variables", ErrVariableInvalid, VariablesPerTenantMax)
		}
		if err := tx.Create(&model).Error; err != nil {
			if isDuplicateVariable(err) {
				return fmt.Errorf("%w: variable %q", ErrAlreadyExists, key)
			}
			return fmt.Errorf("create variable: %w", err)
		}
		return nil
	})
	if err != nil {
		return Variable{}, err
	}
	return variableFromModel(model), nil
}

// UpdateVariable replaces a variable's key and value. Renaming onto a key the
// tenant already has is ErrAlreadyExists; an id the tenant does not own is
// ErrNotFound.
func (store *GORMVariableStore) UpdateVariable(ctx context.Context, tenant TenantScope, id, key, value string) (Variable, error) {
	if err := tenant.validate(); err != nil {
		return Variable{}, err
	}
	key = strings.TrimSpace(key)
	if err := ValidateVariable(key, value); err != nil {
		return Variable{}, err
	}
	var model variableModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, id).First(&model).Error; err != nil {
		return Variable{}, mapNotFound(err, "variable")
	}
	model.Key = key
	model.Value = value
	model.UpdatedAt = time.Now().UTC()
	if err := store.db.WithContext(ctx).Save(&model).Error; err != nil {
		if isDuplicateVariable(err) {
			return Variable{}, fmt.Errorf("%w: variable %q", ErrAlreadyExists, key)
		}
		return Variable{}, fmt.Errorf("update variable: %w", err)
	}
	return variableFromModel(model), nil
}

// DeleteVariable removes a variable the tenant owns.
func (store *GORMVariableStore) DeleteVariable(ctx context.Context, tenant TenantScope, id string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	result := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, id).Delete(&variableModel{})
	if result.Error != nil {
		return fmt.Errorf("delete variable: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: variable", ErrNotFound)
	}
	return nil
}

// VariablesFor returns the tenant's variables keyed by name, values as
// strings: what `$vars` holds for one execution.
func (store *GORMVariableStore) VariablesFor(ctx context.Context, tenant TenantScope) (map[string]any, error) {
	variables, err := store.ListVariables(ctx, tenant)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, len(variables))
	for _, variable := range variables {
		values[variable.Key] = variable.Value
	}
	return values, nil
}

// isDuplicateVariable reports a unique-index violation on (tenant, key).
//
// gorm translates it only when the dialector was opened with TranslateError,
// so the drivers' own wording is checked too.
func isDuplicateVariable(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint") || strings.Contains(text, "duplicate key") ||
		strings.Contains(text, "uidx_variables_tenant_key")
}
