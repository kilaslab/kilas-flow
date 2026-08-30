package repository

import (
	"time"

	"gorm.io/gorm"
)

// Models returns the complete initial KilasFlow persistence schema. Keeping
// these GORM models private prevents the ORM shape from becoming an API or
// engine contract.
func Models() []any {
	return []any{
		&workflowModel{},
		&workflowVersionModel{},
		&executionModel{},
		&executionNodeRunModel{},
	}
}

type workflowModel struct {
	ID              string         `gorm:"primaryKey;size:64"`
	TenantID        string         `gorm:"not null;size:64;index:idx_workflows_tenant_updated,priority:1"`
	Name            string         `gorm:"not null;size:255"`
	Active          bool           `gorm:"not null;default:false"`
	LatestRevision  int            `gorm:"not null"`
	ActiveVersionID *string        `gorm:"size:64"`
	CreatedAt       time.Time      `gorm:"not null"`
	UpdatedAt       time.Time      `gorm:"not null;index:idx_workflows_tenant_updated,priority:2"`
	DeletedAt       gorm.DeletedAt `gorm:"index"`
}

func (workflowModel) TableName() string { return "workflows" }

type workflowVersionModel struct {
	ID            string        `gorm:"primaryKey;size:64"`
	TenantID      string        `gorm:"not null;size:64;index:idx_workflow_versions_tenant_workflow,priority:1;uniqueIndex:uidx_workflow_versions_revision,priority:1"`
	WorkflowID    string        `gorm:"not null;size:64;index:idx_workflow_versions_tenant_workflow,priority:2;uniqueIndex:uidx_workflow_versions_revision,priority:2"`
	Revision      int           `gorm:"not null;uniqueIndex:uidx_workflow_versions_revision,priority:3"`
	SchemaVersion int           `gorm:"not null"`
	Definition    []byte        `gorm:"not null"`
	CreatedAt     time.Time     `gorm:"not null"`
	Workflow      workflowModel `gorm:"foreignKey:WorkflowID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (workflowVersionModel) TableName() string { return "workflow_versions" }

type executionModel struct {
	ID                string    `gorm:"primaryKey;size:64"`
	TenantID          string    `gorm:"not null;size:64;index:idx_executions_tenant_started,priority:1;index:idx_executions_tenant_workflow,priority:1"`
	WorkflowID        string    `gorm:"not null;size:64;index:idx_executions_tenant_workflow,priority:2"`
	WorkflowVersionID string    `gorm:"not null;size:64;index"`
	Status            string    `gorm:"not null;size:32"`
	Trigger           string    `gorm:"not null;size:32"`
	Input             []byte    `gorm:"not null"`
	Output            []byte    `gorm:"not null"`
	Error             []byte    `gorm:"not null"`
	StartedAt         time.Time `gorm:"not null;index:idx_executions_tenant_started,priority:2"`
	FinishedAt        *time.Time
	Workflow          workflowModel        `gorm:"foreignKey:WorkflowID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	WorkflowVersion   workflowVersionModel `gorm:"foreignKey:WorkflowVersionID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (executionModel) TableName() string { return "executions" }

type executionNodeRunModel struct {
	ID          string    `gorm:"primaryKey;size:64"`
	TenantID    string    `gorm:"not null;size:64;index:idx_node_runs_tenant_execution,priority:1"`
	ExecutionID string    `gorm:"not null;size:64;index:idx_node_runs_tenant_execution,priority:2;uniqueIndex:uidx_node_runs_attempt,priority:1;uniqueIndex:uidx_node_runs_sequence,priority:1"`
	NodeID      string    `gorm:"not null;size:64;uniqueIndex:uidx_node_runs_attempt,priority:2"`
	Attempt     int       `gorm:"not null;uniqueIndex:uidx_node_runs_attempt,priority:3"`
	Sequence    int       `gorm:"not null;uniqueIndex:uidx_node_runs_sequence,priority:2"`
	Status      string    `gorm:"not null;size:32"`
	Input       []byte    `gorm:"not null"`
	Output      []byte    `gorm:"not null"`
	Error       []byte    `gorm:"not null"`
	StartedAt   time.Time `gorm:"not null"`
	FinishedAt  *time.Time
	Execution   executionModel `gorm:"foreignKey:ExecutionID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (executionNodeRunModel) TableName() string { return "execution_node_runs" }
