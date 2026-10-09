package repository

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// workflowTagModel is one tag of one workflow's latest revision (migration
// 000025). The tags are authored in the workflow document; this table is the
// derived index the list reads, so it never has to load a document.
//
// Like workflow_static_data and poll_cursors it is not in Models(): the drift
// test compares Models() with the migration baseline, and this table is created
// by a later migration.
type workflowTagModel struct {
	TenantID   string `gorm:"primaryKey;size:64"`
	WorkflowID string `gorm:"primaryKey;size:64"`
	Ordinal    int    `gorm:"not null"`
	Tag        string `gorm:"not null"`
	// TagKey is the tag's identity, lowercased (workflow.TagKey); the primary
	// key's third column, so one workflow cannot carry a tag twice.
	TagKey string `gorm:"primaryKey"`
}

func (workflowTagModel) TableName(namer schema.Namer) string {
	return namer.TableName("workflow_tags")
}

// replaceWorkflowTags makes the index say exactly what tags the workflow's
// newest revision carries: its rows are deleted and written again, in order.
//
// Every path that writes workflows.latest_revision must call it in the same
// transaction (saveDraft, RestoreVersion), so the index and the revision the
// list shows can never disagree. Publishing does not append a revision and does
// not call it.
//
// The document was validated before it was stored, so the tags are unique by
// key and the primary key cannot be violated.
func replaceWorkflowTags(tx *gorm.DB, tenantID, workflowID string, tags []string) error {
	if err := deleteWorkflowTags(tx, tenantID, workflowID); err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}
	rows := make([]workflowTagModel, 0, len(tags))
	for ordinal, tag := range tags {
		rows = append(rows, workflowTagModel{
			TenantID: tenantID, WorkflowID: workflowID, Ordinal: ordinal, Tag: tag, TagKey: workflow.TagKey(tag),
		})
	}
	if err := tx.Create(&rows).Error; err != nil {
		return fmt.Errorf("write workflow tags: %w", err)
	}
	return nil
}

// deleteWorkflowTags removes one workflow's tag rows, inside the transaction
// that replaces them or deletes the workflow.
func deleteWorkflowTags(tx *gorm.DB, tenantID, workflowID string) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).Delete(&workflowTagModel{}).Error; err != nil {
		return fmt.Errorf("delete workflow tags: %w", err)
	}
	return nil
}

// loadWorkflowTags returns the tags of the given workflows, in authored order,
// keyed by workflow ID. A workflow with none has no entry.
func loadWorkflowTags(db *gorm.DB, tenantID string, workflowIDs []string) (map[string][]string, error) {
	tags := map[string][]string{}
	if len(workflowIDs) == 0 {
		return tags, nil
	}
	var rows []workflowTagModel
	if err := db.Where("tenant_id = ? AND workflow_id IN ?", tenantID, workflowIDs).
		Order("workflow_id ASC, ordinal ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list workflow tags: %w", err)
	}
	for _, row := range rows {
		tags[row.WorkflowID] = append(tags[row.WorkflowID], row.Tag)
	}
	return tags, nil
}
