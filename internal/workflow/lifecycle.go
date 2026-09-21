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

// VersionSummary is one row of a workflow's history listing.
//
// It carries no Document. A history list exists to let somebody choose a
// revision, and shipping every snapshot to draw a list of dates would make the
// listing cost grow with the size of the graphs rather than the length of the
// history.
type VersionSummary struct {
	ID            string `json:"id"`
	WorkflowID    string `json:"workflowId"`
	Revision      int    `json:"revision"`
	SchemaVersion int    `json:"schemaVersion"`
	// Label and CreatedBy are absent rather than empty when nothing is known.
	Label     string    `json:"label,omitempty"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// ActorKind, ActorLabel and ActorKeyID are who wrote this revision: "user"
	// for a signed-in person, "key" for an API key. They are absent rather than
	// empty when the write predates attribution or arrived unauthenticated —
	// "unknown" is not a kind, and a listing that named one would be inventing
	// the very fact an audit trail exists to carry.
	ActorKind  string `json:"actorKind,omitempty"`
	ActorLabel string `json:"actorLabel,omitempty"`
	ActorKeyID string `json:"actorKeyId,omitempty"`
	// ActorMeta is what the caller reported using to make the write, today the
	// skills an agent listed in X-KilasFlow-Skills-Used.
	ActorMeta []string `json:"actorMeta,omitempty"`
	// Draft and Published are two flags rather than one role enum because a
	// version is routinely both — publishing the latest revision is the common
	// case — and a single field would have to drop one of the two answers. The
	// server states them so a client never has to compare identifiers against
	// the workflow to work out what it is looking at.
	Draft     bool `json:"draft"`
	Published bool `json:"published"`
}

// PublishAction names what happened to a version.
type PublishAction string

// The publish audit vocabulary. Restored is included because bringing an old
// snapshot back is a deliberate act on the history a reader must be able to
// see, even though it changes no pin.
const (
	PublishActionPublished   PublishAction = "published"
	PublishActionUnpublished PublishAction = "unpublished"
	PublishActionRestored    PublishAction = "restored"
)

// PublishEvent is one entry of a workflow's publish audit trail.
//
// For a restore, VersionID names the snapshot that was restored *from* rather
// than the revision the restore appended: "restored version X" is the fact
// worth recording, and the appended revision is the newest one at that instant
// anyway.
type PublishEvent struct {
	WorkflowID string        `json:"workflowId"`
	VersionID  string        `json:"versionId"`
	Action     PublishAction `json:"action"`
	Actor      string        `json:"actor,omitempty"`
	// ActorKind, ActorLabel and ActorKeyID are who acted, in the same
	// vocabulary the revision listing uses. Absent when the publish predates
	// attribution, which is a different answer from an actor nobody named.
	ActorKind  string    `json:"actorKind,omitempty"`
	ActorLabel string    `json:"actorLabel,omitempty"`
	ActorKeyID string    `json:"actorKeyId,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}
