package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// DefaultTenantID isolates standalone installations until an authenticated
// caller supplies a real tenant scope.
const DefaultTenantID = "default"

// TenantScope is mandatory for every repository operation. Future auth and
// embed sessions resolve it from request context before calling a repository.
type TenantScope struct {
	ID string
}

// ErrNotFound is returned without exposing an ORM or cross-tenant distinction.
var ErrNotFound = errors.New("repository record not found")

// WorkflowRepository is the persistence seam used by lifecycle services and
// later API handlers. The execution engine never imports GORM.
type WorkflowRepository interface {
	SaveDraft(context.Context, TenantScope, workflow.Document) (workflow.StoredWorkflow, error)
	List(context.Context, TenantScope) ([]workflow.StoredWorkflow, error)
	Get(context.Context, TenantScope, string) (workflow.StoredWorkflow, error)
	GetVersion(context.Context, TenantScope, string, int) (workflow.Version, error)
	Activate(context.Context, TenantScope, string, workflow.Catalog) (workflow.StoredWorkflow, error)
	Deactivate(context.Context, TenantScope, string) (workflow.StoredWorkflow, error)
	Delete(context.Context, TenantScope, string) error
}

// GORMWorkflowStore is the GORM implementation of WorkflowRepository.
type GORMWorkflowStore struct {
	db *gorm.DB
}

var _ WorkflowRepository = (*GORMWorkflowStore)(nil)

// NewWorkflowStore constructs the GORM-backed workflow persistence boundary.
func NewWorkflowStore(db *gorm.DB) *GORMWorkflowStore {
	return &GORMWorkflowStore{db: db}
}

// SaveDraft appends an immutable revision. A newly created document receives a
// server-generated workflow ID before document validation and persistence.
func (store *GORMWorkflowStore) SaveDraft(ctx context.Context, tenant TenantScope, document workflow.Document) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	if document.ID == "" {
		id, err := workflow.NewID("wf")
		if err != nil {
			return workflow.StoredWorkflow{}, err
		}
		document.ID = id
	}
	if err := workflow.ValidateDraft(document); err != nil {
		return workflow.StoredWorkflow{}, err
	}

	definition, err := json.Marshal(document)
	if err != nil {
		return workflow.StoredWorkflow{}, fmt.Errorf("marshal workflow document: %w", err)
	}

	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model workflowModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenant.ID, document.ID).
			First(&model).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			model = workflowModel{
				ID:             document.ID,
				TenantID:       tenant.ID,
				Name:           document.Name,
				LatestRevision: 0,
			}
			if err := tx.Create(&model).Error; err != nil {
				return fmt.Errorf("create workflow: %w", err)
			}
		case err != nil:
			return fmt.Errorf("find workflow: %w", err)
		}

		versionID, err := workflow.NewID("wfv")
		if err != nil {
			return err
		}
		version := workflowVersionModel{
			ID:            versionID,
			TenantID:      tenant.ID,
			WorkflowID:    model.ID,
			Revision:      model.LatestRevision + 1,
			SchemaVersion: document.SchemaVersion,
			Definition:    definition,
		}
		if err := tx.Create(&version).Error; err != nil {
			return fmt.Errorf("create workflow version: %w", err)
		}

		if err := tx.Model(&workflowModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, model.ID).
			Updates(map[string]any{"name": document.Name, "latest_revision": version.Revision}).Error; err != nil {
			return fmt.Errorf("update workflow: %w", err)
		}

		return nil
	})
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.Get(ctx, tenant, document.ID)
}

// List returns every visible workflow for a tenant in stable dashboard order.
func (store *GORMWorkflowStore) List(ctx context.Context, tenant TenantScope) ([]workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []workflowModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ?", tenant.ID).
		Order("updated_at DESC, id ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	workflows := make([]workflow.StoredWorkflow, 0, len(models))
	for _, model := range models {
		stored, err := store.storedWorkflow(ctx, tenant, model)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, stored)
	}
	return workflows, nil
}

// Get returns a workflow and its latest snapshot, scoped to one tenant.
func (store *GORMWorkflowStore) Get(ctx context.Context, tenant TenantScope, workflowID string) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	model, err := store.findWorkflow(ctx, tenant, workflowID)
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.storedWorkflow(ctx, tenant, model)
}

// GetVersion returns one immutable revision owned by the requested workflow.
func (store *GORMWorkflowStore) GetVersion(ctx context.Context, tenant TenantScope, workflowID string, revision int) (workflow.Version, error) {
	if err := tenant.validate(); err != nil {
		return workflow.Version{}, err
	}
	var model workflowVersionModel
	err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ? AND revision = ?", tenant.ID, workflowID, revision).
		First(&model).Error
	if err != nil {
		return workflow.Version{}, mapNotFound(err, "workflow version")
	}
	return versionFromModel(model)
}

// Activate compiles the latest saved revision and pins it only if the graph is
// executable. Earlier snapshots can never be reactivated through this API.
func (store *GORMWorkflowStore) Activate(ctx context.Context, tenant TenantScope, workflowID string, catalog workflow.Catalog) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	if catalog == nil {
		return workflow.StoredWorkflow{}, fmt.Errorf("workflow catalog is required for activation")
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model workflowModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).
			First(&model).Error; err != nil {
			return mapNotFound(err, "workflow")
		}
		var version workflowVersionModel
		if err := tx.Where("tenant_id = ? AND workflow_id = ? AND revision = ?", tenant.ID, workflowID, model.LatestRevision).First(&version).Error; err != nil {
			return mapNotFound(err, "workflow version")
		}
		storedVersion, err := versionFromModel(version)
		if err != nil {
			return err
		}
		if _, err := workflow.Compile(storedVersion.Document, catalog); err != nil {
			return err
		}
		if err := tx.Model(&workflowModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, model.ID).
			Updates(map[string]any{"active": true, "active_version_id": version.ID}).Error; err != nil {
			return fmt.Errorf("activate workflow: %w", err)
		}
		return nil
	})
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.Get(ctx, tenant, workflowID)
}

// Deactivate is idempotent and retains the last active version for history.
func (store *GORMWorkflowStore) Deactivate(ctx context.Context, tenant TenantScope, workflowID string) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	model, err := store.findWorkflow(ctx, tenant, workflowID)
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	if model.Active {
		if err := store.db.WithContext(ctx).Model(&model).Update("active", false).Error; err != nil {
			return workflow.StoredWorkflow{}, fmt.Errorf("deactivate workflow: %w", err)
		}
	}
	return store.Get(ctx, tenant, workflowID)
}

// Delete soft-deletes a workflow identity while preserving version and
// execution evidence for audit and foreign-key integrity.
func (store *GORMWorkflowStore) Delete(ctx context.Context, tenant TenantScope, workflowID string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	model, err := store.findWorkflow(ctx, tenant, workflowID)
	if err != nil {
		return err
	}
	if err := store.db.WithContext(ctx).Delete(&model).Error; err != nil {
		return fmt.Errorf("delete workflow: %w", err)
	}
	return nil
}

func (store *GORMWorkflowStore) findWorkflow(ctx context.Context, tenant TenantScope, workflowID string) (workflowModel, error) {
	var model workflowModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).First(&model).Error; err != nil {
		return workflowModel{}, mapNotFound(err, "workflow")
	}
	return model, nil
}

func (store *GORMWorkflowStore) storedWorkflow(ctx context.Context, tenant TenantScope, model workflowModel) (workflow.StoredWorkflow, error) {
	latest, err := store.GetVersion(ctx, tenant, model.ID, model.LatestRevision)
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	stored := workflow.StoredWorkflow{
		ID:            model.ID,
		TenantID:      model.TenantID,
		Name:          model.Name,
		Active:        model.Active,
		LatestVersion: latest,
		CreatedAt:     model.CreatedAt,
		UpdatedAt:     model.UpdatedAt,
	}
	if model.ActiveVersionID != nil {
		var active workflowVersionModel
		if err := store.db.WithContext(ctx).
			Where("tenant_id = ? AND workflow_id = ? AND id = ?", tenant.ID, model.ID, *model.ActiveVersionID).
			First(&active).Error; err != nil {
			return workflow.StoredWorkflow{}, mapNotFound(err, "active workflow version")
		}
		version, err := versionFromModel(active)
		if err != nil {
			return workflow.StoredWorkflow{}, err
		}
		stored.ActiveVersion = &version
	}
	return stored, nil
}

func (tenant TenantScope) validate() error {
	if tenant.ID == "" {
		return fmt.Errorf("tenant scope is required")
	}
	return nil
}

func versionFromModel(model workflowVersionModel) (workflow.Version, error) {
	var document workflow.Document
	if err := json.Unmarshal(model.Definition, &document); err != nil {
		return workflow.Version{}, fmt.Errorf("decode persisted workflow version %q: %w", model.ID, err)
	}
	return workflow.Version{
		ID:            model.ID,
		WorkflowID:    model.WorkflowID,
		TenantID:      model.TenantID,
		Revision:      model.Revision,
		SchemaVersion: model.SchemaVersion,
		Document:      document,
		CreatedAt:     model.CreatedAt,
	}, nil
}

func mapNotFound(err error, resource string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, resource)
	}
	return fmt.Errorf("query %s: %w", resource, err)
}
