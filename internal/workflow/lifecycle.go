package workflow

import "time"

// StoredWorkflow is the persistence-independent representation returned to the
// API and application layers. It deliberately carries no ORM metadata.
type StoredWorkflow struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenantId"`
	Name          string    `json:"name"`
	Active        bool      `json:"active"`
	LatestVersion Version   `json:"latestVersion"`
	ActiveVersion *Version  `json:"activeVersion,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Version is one immutable, persisted snapshot of a canonical document.
type Version struct {
	ID            string    `json:"id"`
	WorkflowID    string    `json:"workflowId"`
	TenantID      string    `json:"tenantId"`
	Revision      int       `json:"revision"`
	SchemaVersion int       `json:"schemaVersion"`
	Document      Document  `json:"document"`
	CreatedAt     time.Time `json:"createdAt"`
}
