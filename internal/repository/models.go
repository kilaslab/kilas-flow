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
		// Tenants come first because users and API keys reference the table by
		// foreign key, and a migrator that creates children first has nothing
		// to point them at.
		&tenantModel{},
		&userModel{},
		&apiKeyModel{},
		&workflowModel{},
		&workflowVersionModel{},
		&workflowPublishEventModel{},
		&executionModel{},
		&executionNodeRunModel{},
		&credentialModel{},
		&webhookBindingModel{},
		&webhookRouteModel{},
		&webhookDeliveryModel{},
		&scheduleModel{},
	}
}

// webhookBindingModel routes one inbound path to an active workflow's trigger
// node. Rows exist only while the workflow is active, so routing never has to
// re-check activation.
type webhookBindingModel struct {
	ID                uint   `gorm:"primaryKey;autoIncrement"`
	TenantID          string `gorm:"not null;size:64;index:idx_webhook_bindings_workflow,priority:1;uniqueIndex:uidx_webhook_bindings_label,priority:1"`
	WorkflowID        string `gorm:"not null;size:64;index:idx_webhook_bindings_workflow,priority:2"`
	WorkflowVersionID string `gorm:"not null;size:64"`
	NodeID            string `gorm:"not null;size:64"`
	// NodeType is the trigger's registered type. The HTTP boundary needs it to
	// decide what shape a delivery takes: an n8n-compatible trigger expects a
	// different item to KilasFlow's own, and a third-party trigger expects the
	// parsed body at the top level rather than nested under a key.
	NodeType string `gorm:"not null;size:128;default:''"`
	// Route is the opaque segment the URL actually carries, and it is what an
	// inbound request resolves on. The unique index still spans the whole
	// route globally, because an inbound webhook has no session and the route
	// is the only thing identifying it — two rows matching one request would
	// be a cross-tenant routing bug far worse than a refused activation.
	Method string `gorm:"not null;size:8;uniqueIndex:uidx_webhook_bindings_route,priority:1"`
	Route  string `gorm:"not null;size:64;uniqueIndex:uidx_webhook_bindings_route,priority:2"`
	// Path is what the workflow's author called this endpoint. It is a display
	// label now rather than the route, so two tenants importing the same n8n
	// template both keep the template's path and neither collides. The
	// per-tenant unique index preserves the old rule where it still makes
	// sense: one tenant still cannot claim the same endpoint twice.
	Path       string    `gorm:"not null;size:255;uniqueIndex:uidx_webhook_bindings_label,priority:2"`
	Parameters []byte    `gorm:"not null"`
	CreatedAt  time.Time `gorm:"not null"`
}

// webhookRouteModel is the minted route for one workflow's trigger node.
//
// It outlives the binding on purpose. Bindings exist only while a workflow is
// active, so if the route were minted with the binding, deactivating and
// reactivating a workflow would change its public URL — which is the same
// failure as not having a URL at all for anyone who has already configured a
// sender.
type webhookRouteModel struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	TenantID   string `gorm:"not null;size:64;uniqueIndex:uidx_webhook_routes_node,priority:1"`
	WorkflowID string `gorm:"not null;size:64;uniqueIndex:uidx_webhook_routes_node,priority:2"`
	NodeID     string `gorm:"not null;size:64;uniqueIndex:uidx_webhook_routes_node,priority:3"`
	Route      string `gorm:"not null;size:64;uniqueIndex"`
	CreatedAt  time.Time
}

func (webhookRouteModel) TableName() string { return "webhook_routes" }

// webhookDeliveryModel records one logical delivery so a retry does not run the
// workflow twice.
//
// WAHA retries a failed delivery fifteen times at two-second intervals and
// identifies each logical delivery with a header, so a slow workflow that
// eventually succeeds could send fifteen WhatsApp replies.
type webhookDeliveryModel struct {
	ID    uint   `gorm:"primaryKey;autoIncrement"`
	Route string `gorm:"not null;size:64;uniqueIndex:uidx_webhook_deliveries,priority:1"`
	// DeliveryID is the sender's own identifier for this delivery, scoped by
	// route so two senders cannot collide.
	DeliveryID  string    `gorm:"not null;size:128;uniqueIndex:uidx_webhook_deliveries,priority:2"`
	ExecutionID string    `gorm:"not null;size:64"`
	ExpiresAt   time.Time `gorm:"not null;index"`
	CreatedAt   time.Time `gorm:"not null"`
}

func (webhookDeliveryModel) TableName() string { return "webhook_deliveries" }

func (webhookBindingModel) TableName() string { return "webhook_bindings" }

// scheduleModel is one cron schedule for a workflow.
type scheduleModel struct {
	ID         string `gorm:"primaryKey;size:64"`
	TenantID   string `gorm:"not null;size:64;index:idx_schedules_tenant_workflow,priority:1"`
	WorkflowID string `gorm:"not null;size:64;index:idx_schedules_tenant_workflow,priority:2"`
	NodeID     string `gorm:"not null;size:64"`
	// IntervalIndex distinguishes the rows of one trigger node. A Schedule
	// Trigger's rule may hold several intervals — "every weekday at 09:00, and
	// again at 17:00" is two — and one row per interval keeps ClaimDue's
	// transactional claim untouched: it still advances exactly one NextRunAt
	// per row and never has to reason about which of a set is earliest.
	IntervalIndex int    `gorm:"not null;default:0"`
	Cron          string `gorm:"not null;size:255"`
	// Timezone is the IANA zone the cron expression is read in. Empty means
	// UTC. It is stored beside the expression rather than folded into it as a
	// TZ= prefix so the user's own cron reads back as they wrote it.
	Timezone  string `gorm:"not null;size:64;default:''"`
	Active    bool   `gorm:"not null;default:false"`
	LastRunAt *time.Time
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
	ID            string `gorm:"primaryKey;size:64"`
	TenantID      string `gorm:"not null;size:64;index:idx_workflow_versions_tenant_workflow,priority:1;uniqueIndex:uidx_workflow_versions_revision,priority:1"`
	WorkflowID    string `gorm:"not null;size:64;index:idx_workflow_versions_tenant_workflow,priority:2;uniqueIndex:uidx_workflow_versions_revision,priority:2"`
	Revision      int    `gorm:"not null;uniqueIndex:uidx_workflow_versions_revision,priority:3"`
	SchemaVersion int    `gorm:"not null"`
	Definition    []byte `gorm:"not null"`
	// Label is what a person called this revision, and CreatedBy is who wrote
	// it. Both are nullable, and nullable is the whole point: the main API has
	// no authentication yet, so every request resolves to tenant "default" and
	// the author of most writes is genuinely unknowable. A NOT NULL column
	// would force this code to invent an author, and an invented author in an
	// audit trail is worse than an absent one.
	Label     *string       `gorm:"size:255"`
	CreatedBy *string       `gorm:"size:64"`
	CreatedAt time.Time     `gorm:"not null"`
	Workflow  workflowModel `gorm:"foreignKey:WorkflowID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (workflowVersionModel) TableName() string { return "workflow_versions" }

// workflowPublishEventModel is one entry in a workflow's publish audit trail.
//
// It exists because workflows.active_version_id cannot answer the question by
// itself: deactivation deliberately leaves that pointer where it was, so the
// column says which version served last but never when it started or stopped.
// These rows are what a publish period is reconstructed from.
//
// VersionID carries no foreign key on purpose. Retention prunes old versions,
// and an audit row that vanished with the version it describes would destroy
// exactly the evidence somebody came looking for; the identifier outliving the
// row it names is the intended trade.
type workflowPublishEventModel struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	TenantID   string `gorm:"not null;size:64;index:idx_workflow_publish_events_workflow,priority:1"`
	WorkflowID string `gorm:"not null;size:64;index:idx_workflow_publish_events_workflow,priority:2"`
	VersionID  string `gorm:"not null;size:64"`
	// Action is published, unpublished or restored.
	Action string `gorm:"not null;size:32"`
	// Actor and Reason are empty rather than null when unknown: an audit row is
	// always written, and a caller that supplied neither should still leave the
	// trace that something happened.
	Actor     string    `gorm:"not null;size:64;default:''"`
	Reason    string    `gorm:"not null;size:255;default:''"`
	CreatedAt time.Time `gorm:"not null;index:idx_workflow_publish_events_workflow,priority:3"`
}

func (workflowPublishEventModel) TableName() string { return "workflow_publish_events" }

type executionModel struct {
	ID                string `gorm:"primaryKey;size:64"`
	TenantID          string `gorm:"not null;size:64;index:idx_executions_tenant_started,priority:1;index:idx_executions_tenant_workflow,priority:1"`
	WorkflowID        string `gorm:"not null;size:64;index:idx_executions_tenant_workflow,priority:2"`
	WorkflowVersionID string `gorm:"not null;size:64;index"`
	Status            string `gorm:"not null;size:32"`
	Trigger           string `gorm:"not null;size:32"`
	TriggerNodeID     string `gorm:"size:64"`
	// ParentExecutionID links a sub-workflow run to the run that called it.
	// Indexed because "show me this execution's children" is the only question
	// asked of it, and it is asked once per execution detail view.
	ParentExecutionID       string    `gorm:"size:64;index;default:''"`
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
	ID          string `gorm:"primaryKey;size:64"`
	TenantID    string `gorm:"not null;size:64;index:idx_node_runs_tenant_execution,priority:1"`
	ExecutionID string `gorm:"not null;size:64;index:idx_node_runs_tenant_execution,priority:2;uniqueIndex:uidx_node_runs_attempt,priority:1;uniqueIndex:uidx_node_runs_sequence,priority:1"`
	NodeID      string `gorm:"not null;size:64;uniqueIndex:uidx_node_runs_attempt,priority:2"`
	// RunIndex is the Nth time this node ran in this execution. It widens the
	// attempt index rather than overloading Attempt: attempt means "retry N of
	// the same run" and run index means "the Nth run", and conflating them
	// makes a retry inside a loop unrepresentable. Additive and defaulted, so
	// an existing database still opens.
	RunIndex   int       `gorm:"not null;default:0;uniqueIndex:uidx_node_runs_attempt,priority:4"`
	Attempt    int       `gorm:"not null;uniqueIndex:uidx_node_runs_attempt,priority:3"`
	Sequence   int       `gorm:"not null;uniqueIndex:uidx_node_runs_sequence,priority:2"`
	Status     string    `gorm:"not null;size:32"`
	Input      []byte    `gorm:"not null"`
	Output     []byte    `gorm:"not null"`
	Error      []byte    `gorm:"not null"`
	StartedAt  time.Time `gorm:"not null"`
	FinishedAt *time.Time
	Execution  executionModel `gorm:"foreignKey:ExecutionID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (executionNodeRunModel) TableName() string { return "execution_node_runs" }

// tenantModel is one customer of this deployment.
//
// It exists so that the tenant ID on every other table refers to something
// real. Before it, `tenant_id` was a string nobody had ever written a row for,
// which made "which tenants exist" unanswerable and made a typo in a tenant ID
// indistinguishable from a tenant that simply has no data yet.
type tenantModel struct {
	ID        string    `gorm:"primaryKey;size:64"`
	Name      string    `gorm:"not null;size:255"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (tenantModel) TableName() string { return "tenants" }

// userModel is a person who signs in to the dashboard.
//
// Email is unique across the deployment rather than per tenant, because login
// presents an email and a password and nothing else: a per-tenant unique index
// would let one address resolve to several accounts and leave the server
// guessing which password to check.
type userModel struct {
	ID       string `gorm:"primaryKey;size:64"`
	TenantID string `gorm:"not null;size:64;index:idx_users_tenant"`
	Email    string `gorm:"not null;size:255;uniqueIndex:uidx_users_email"`
	// PasswordHash is PBKDF2 over a per-user salt, never the password. The
	// column is wider than today's encoding needs so raising the work factor
	// later is not a schema change.
	PasswordHash string    `gorm:"not null;size:255"`
	Name         string    `gorm:"not null;size:255;default:''"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
	// DisabledAt locks an account out without deleting it, so the workflows and
	// executions it created keep an author.
	DisabledAt *time.Time
	Tenant     tenantModel `gorm:"foreignKey:TenantID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (userModel) TableName() string { return "users" }

// apiKeyModel is a machine caller's tenant-scoped credential.
//
// The secret itself is never stored. Prefix is a public handle the verifier
// looks the row up by, and SecretHash is SHA-256 of the rest, so a database
// dump — or a replica, or a support export — discloses nothing usable.
//
// A key belongs to a tenant rather than to a user on purpose: a host's
// integration must not stop working because the person who created its key
// left the company.
type apiKeyModel struct {
	ID       string `gorm:"primaryKey;size:64"`
	TenantID string `gorm:"not null;size:64;index:idx_api_keys_tenant"`
	Prefix   string `gorm:"not null;size:32;uniqueIndex:uidx_api_keys_prefix"`
	// SecretHash is hex-encoded SHA-256, which is always 64 characters.
	SecretHash string    `gorm:"not null;size:64"`
	Label      string    `gorm:"not null;size:255;default:''"`
	CreatedAt  time.Time `gorm:"not null"`
	// LastUsedAt answers "is anything still using this key" before someone
	// revokes it. It is written at most once a minute per key rather than on
	// every request, so authenticating does not turn every read into a write.
	LastUsedAt *time.Time
	// RevokedAt disables a key without deleting the row, so an audit of what
	// that key did still has something to name.
	RevokedAt *time.Time
	Tenant    tenantModel `gorm:"foreignKey:TenantID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (apiKeyModel) TableName() string { return "api_keys" }
