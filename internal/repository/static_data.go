package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// workflowStaticDataModel is one workflow's static data: the JSON document
// n8n's $getWorkflowStaticData keeps between runs (migration 000024).
type workflowStaticDataModel struct {
	TenantID   string    `gorm:"primaryKey;size:64"`
	WorkflowID string    `gorm:"primaryKey;size:64"`
	Data       []byte    `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

func (workflowStaticDataModel) TableName(namer schema.Namer) string {
	return namer.TableName("workflow_static_data")
}

// GORMStaticDataStore keeps each workflow's static data, scoped to its
// tenant like every other read and write here.
type GORMStaticDataStore struct {
	db *gorm.DB
}

// NewStaticDataStore constructs the static data persistence boundary.
func NewStaticDataStore(db *gorm.DB) *GORMStaticDataStore {
	return &GORMStaticDataStore{db: db}
}

// LoadStaticData returns a workflow's static data, or nil when it has none.
func (store *GORMStaticDataStore) LoadStaticData(ctx context.Context, tenant TenantScope, workflowID string) (json.RawMessage, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var model workflowStaticDataModel
	err := store.db.WithContext(ctx).Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load workflow static data: %w", err)
	}
	return json.RawMessage(model.Data), nil
}

// SaveStaticData replaces a workflow's static data.
func (store *GORMStaticDataStore) SaveStaticData(ctx context.Context, tenant TenantScope, workflowID string, data json.RawMessage) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if workflowID == "" {
		return fmt.Errorf("a workflow ID is required")
	}
	model := workflowStaticDataModel{TenantID: tenant.ID, WorkflowID: workflowID, Data: data, UpdatedAt: time.Now().UTC()}
	err := store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "workflow_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "updated_at"}),
	}).Create(&model).Error
	if err != nil {
		return fmt.Errorf("save workflow static data: %w", err)
	}
	return nil
}

// deleteStaticData removes one workflow's static data, inside the
// transaction that deletes the workflow.
func deleteStaticData(tx *gorm.DB, tenantID, workflowID string) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).Delete(&workflowStaticDataModel{}).Error; err != nil {
		return fmt.Errorf("delete workflow static data: %w", err)
	}
	return nil
}
