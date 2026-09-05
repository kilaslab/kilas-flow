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
		&credentialModel{},
		&webhookBindingModel{},
		&scheduleModel{},
	}
}

// webhookBindingModel routes one inbound path to an active workflow's trigger
// node. Rows exist only while the workflow is active, so routing never has to
// re-check activation.
type webhookBindingModel struct {
	ID                uint   `gorm:"primaryKey;autoIncrement"`
	TenantID          string `gorm:"not null;size:64;index:idx_webhook_bindings_workflow,priority:1"`
	WorkflowID        string `gorm:"not null;size:64;index:idx_webhook_bindings_workflow,priority:2"`
	WorkflowVersionID string `gorm:"not null;size:64"`
	NodeID            string `gorm:"not null;size:64"`
	// The unique index spans method and path only: two active workflows must
	// not be able to claim the same endpoint, whichever tenant owns them, or an
	// inbound request would have no deterministic destination.
	Method     string    `gorm:"not null;size:8;uniqueIndex:uidx_webhook_bindings_route,priority:1"`
	Path       string    `gorm:"not null;size:255;uniqueIndex:uidx_webhook_bindings_route,priority:2"`
	Parameters []byte    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null"`
}

func (webhookBindingModel) TableName() string { return "webhook_bindings" }

// scheduleModel is one cron schedule for a workflow.
type scheduleModel struct {
	ID         string `gorm:"primaryKey;size:64"`
	TenantID   string `gorm:"not null;size:64;index:idx_schedules_tenant_workflow,priority:1"`
	WorkflowID string `gorm:"not null;size:64;index:idx_schedules_tenant_workflow,priority:2"`
	NodeID     string `gorm:"not null;size:64"`
	Cron       string `gorm:"not null;size:255"`
	Active     bool   `gorm:"not null;default:false"`
	LastRunAt  *time.Time
	// NextRunAt is indexed because the scheduler's only hot query is "what is
	// due now".
	NextRunAt *time.Time    `gorm:"index:idx_schedules_next_run"`
	CreatedAt time.Time     `gorm:"not null"`
	UpdatedAt time.Time     `gorm:"not null"`
	Workflow  workflowModel `gorm:"foreignKey:WorkflowID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
}

func (scheduleModel) TableName() string { return "schedules" }

// credentialModel stores an encrypted credential payload. The plaintext never
// exists as a column, so a database dump, a replica, or a support export
// cannot disclose a secret without the master key.
type credentialModel struct {
	ID       string `gorm:"primaryKey;size:64"`
	TenantID string `gorm:"not null;size:64;index:idx_credentials_tenant_name,priority:1"`
	Name     string `gorm:"not null;size:255;index:idx_credentials_tenant_name,priority:2"`
	Type     string `gorm:"not null;size:64"`
	// Payload is the AES-256-GCM sealed map of the type's secret fields.
	Payload []byte `gorm:"not null"`
	// PublicFields holds the type's non-secret values as plain JSON. Keeping
	// them out of the sealed payload lets a listing show a username or header
	// name without the master key ever being used to satisfy a read.
	PublicFields []byte `gorm:"not null"`
	// AllowedDomains is a JSON array scoping where the credential may be sent.
	AllowedDomains []byte    `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (credentialModel) TableName() string { return "credentials" }

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
	ID                      string    `gorm:"primaryKey;size:64"`
	TenantID                string    `gorm:"not null;size:64;index:idx_executions_tenant_started,priority:1;index:idx_executions_tenant_workflow,priority:1"`
	WorkflowID              string    `gorm:"not null;size:64;index:idx_executions_tenant_workflow,priority:2"`
	WorkflowVersionID       string    `gorm:"not null;size:64;index"`
	Status                  string    `gorm:"not null;size:32"`
	Trigger                 string    `gorm:"not null;size:32"`
	Input                   []byte    `gorm:"not null"`
	Output                  []byte    `gorm:"not null"`
	Error                   []byte    `gorm:"not null"`
	StartedAt               time.Time `gorm:"not null;index:idx_executions_tenant_started,priority:2"`
	FinishedAt              *time.Time
	LeaseOwner              string               `gorm:"size:128;index"`
	LeaseExpiresAt          *time.Time           `gorm:"index"`
	CancellationRequestedAt *time.Time           `gorm:"index"`
	Workflow                workflowModel        `gorm:"foreignKey:WorkflowID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	WorkflowVersion         workflowVersionModel `gorm:"foreignKey:WorkflowVersionID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
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
