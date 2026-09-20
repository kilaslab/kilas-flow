package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// DefaultTenantID isolates standalone installations until an authenticated
// caller supplies a real tenant scope.
const DefaultTenantID = "default"

// TenantScope scopes the repository operations that act for a tenant, not
// every operation. Claiming (ClaimNext, ClaimDue), routing (Resolve,
// ClaimDelivery, RecordDeliveryExecution), history-wide pruning
// (PruneAllVersions) and the lookups that run before any tenant is known
// (EnsureTenant, GetTenant, FindUserForLogin, AuthenticateAPIKey, CountUsers)
// take none. A query that forgets the tenant is a bug, but it still compiles.
type TenantScope struct {
	ID string
}

// ErrNotFound is returned without exposing an ORM or cross-tenant distinction.
var ErrNotFound = errors.New("repository record not found")

// ErrInvalidCursor reports a pagination cursor the caller did not receive from
// a previous listing. Callers translate it into a 400, never a 500.
var ErrInvalidCursor = errors.New("repository cursor is invalid")

// WorkflowRepository is the persistence seam used by lifecycle services and
// later API handlers. The execution engine never imports GORM.
type WorkflowRepository interface {
	SaveDraft(context.Context, TenantScope, workflow.Document) (workflow.StoredWorkflow, error)
	// ListSummaries is the dashboard's listing. It returns page summaries
	// rather than StoredWorkflow values because materialising each workflow's
	// latest document to render four fields is what made one list request load
	// every graph in the tenant (BUG-fv5fer).
	ListSummaries(context.Context, TenantScope, WorkflowFilter) (WorkflowSummaryPage, error)
	Get(context.Context, TenantScope, string) (workflow.StoredWorkflow, error)
	GetVersion(context.Context, TenantScope, string, int) (workflow.Version, error)
	GetVersionByID(context.Context, TenantScope, string, string) (workflow.Version, error)
	ListVersions(context.Context, TenantScope, string, VersionFilter) (VersionPage, error)
	Activate(context.Context, TenantScope, string, workflow.Catalog) (workflow.StoredWorkflow, error)
	// PublishVersion pins any snapshot, not only the newest. Activate is the
	// special case of it that means "publish the latest revision".
	PublishVersion(context.Context, TenantScope, string, string, workflow.Catalog, string) (workflow.StoredWorkflow, error)
	RestoreVersion(context.Context, TenantScope, string, string, string) (workflow.StoredWorkflow, error)
	ListPublishEvents(context.Context, TenantScope, string) ([]workflow.PublishEvent, error)
	Deactivate(context.Context, TenantScope, string) (workflow.StoredWorkflow, error)
	Delete(context.Context, TenantScope, string) error
}

// GORMWorkflowStore is the GORM implementation of WorkflowRepository.
type GORMWorkflowStore struct {
	db *gorm.DB
	// webhooks extracts routable triggers from a document. It is injected so
	// binding sync can happen inside the activation transaction without the
	// repository knowing any node type.
	webhooks WebhookExtractor
	// schedules extracts schedule triggers from a document, and next computes
	// a cron's first run. Together they are why activating a workflow with a
	// Schedule Trigger makes it fire: before them a schedule row existed only
	// if somebody called the schedules API by hand, so an imported
	// schedule-driven workflow activated cleanly and then never ran.
	schedules ScheduleExtractor
	next      func(string, time.Time) (time.Time, error)
	// subworkflows extracts the workflows a document calls, so activation can
	// refuse a caller whose target is not live. Nil leaves the check off.
	subworkflows SubworkflowExtractor
	// retention bounds how much history survives. The zero value keeps
	// everything, which is what an installation that never configured this gets.
	retention RetentionPolicy
}

var _ WorkflowRepository = (*GORMWorkflowStore)(nil)

// NewWorkflowStore constructs the GORM-backed workflow persistence boundary.
func NewWorkflowStore(db *gorm.DB) *GORMWorkflowStore {
	return &GORMWorkflowStore{db: db}
}

// SubworkflowCall names one workflow a document calls, for activation checks.
//
// It carries the node it came from as well as the target, because the refusal
// has to say which node to fix: a workflow with a dozen Execute Workflow nodes
// and one inactive target is a puzzle without the node's name on it.
type SubworkflowCall struct {
	NodeID     string
	NodeName   string
	WorkflowID string
}

// SubworkflowExtractor reads the workflows one document calls.
//
// A function rather than node knowledge: persistence stays free of node types,
// exactly as it is for webhooks and schedules, and the caller that owns the
// node definitions supplies the reading.
type SubworkflowExtractor func(workflow.Document) []SubworkflowCall

// WithSubworkflows returns a store that refuses to activate a workflow whose
// saved document calls a workflow that is not active.
//
// The check exists because the failure it prevents is silent. A sub-workflow
// call resolves its target at run time, so activating the caller while the
// target is a draft — or deleted, or renamed away — publishes a workflow that
// looks healthy on the canvas and fails the moment it runs, halfway through a
// graph whose earlier nodes have already done their work. n8n refuses the
// activation instead, and so does this: activate the target, or point the node
// at one that is live.
//
// Nil leaves the check off, which is what an installation that never wires the
// extractor gets: activation behaves exactly as it did before.
func (store *GORMWorkflowStore) WithSubworkflows(extract SubworkflowExtractor) *GORMWorkflowStore {
	store.subworkflows = extract
	return store
}

// WithWebhooks returns a store that keeps webhook bindings in step with
// activation. Without an extractor the store still works; nothing becomes
// routable, which is the correct behaviour for a build with no webhook nodes.
func (store *GORMWorkflowStore) WithWebhooks(extract WebhookExtractor) *GORMWorkflowStore {
	store.webhooks = extract
	return store
}

// WithSchedules returns a store that keeps schedule rows in step with
// activation. Both arguments are required together: an extractor with no way to
// compute a first run could only write rows that never become due.
func (store *GORMWorkflowStore) WithSchedules(extract ScheduleExtractor, next func(string, time.Time) (time.Time, error)) *GORMWorkflowStore {
	if extract == nil || next == nil {
		return store
	}
	store.schedules = extract
	store.next = next
	return store
}

// WithRetention bounds how much version history the store keeps.
//
// The count bound is enforced inside SaveDraft's own transaction because that
// is the exact moment a new row appears; leaving it to a periodic sweep would
// let a burst of saves outrun the bound and sit over it until the sweep came
// round. The age bound cannot work that way — it has to fire for workflows
// nobody is saving — so it is PruneAllVersions' job.
func (store *GORMWorkflowStore) WithRetention(policy RetentionPolicy) *GORMWorkflowStore {
	store.retention = policy
	return store
}

// SaveDraft appends an immutable revision. A newly created document receives a
// server-generated workflow ID before document validation and persistence.
func (store *GORMWorkflowStore) SaveDraft(ctx context.Context, tenant TenantScope, document workflow.Document) (workflow.StoredWorkflow, error) {
	return store.saveDraft(ctx, tenant, document, nil)
}

// saveDraft is the one append path for an ordinary save and an imported one.
// SaveDraftWithDiagnostics passes the import report it must keep with the
// revision; everything else — the ID minting, the validation, the row lock, the
// retention bound — is shared, so the two cannot drift into disagreeing about
// what a saved revision is.
func (store *GORMWorkflowStore) saveDraft(ctx context.Context, tenant TenantScope, document workflow.Document, diagnostics json.RawMessage) (workflow.StoredWorkflow, error) {
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

		version, err := appendVersion(tx, tenant, model, definition, document, diagnostics, nil, ActorFrom(ctx))
		if err != nil {
			return err
		}

		if err := tx.Model(&workflowModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, model.ID).
			Updates(map[string]any{"name": document.Name, "latest_revision": version.Revision}).Error; err != nil {
			return fmt.Errorf("update workflow: %w", err)
		}

		// Enforcing the count bound here, in the transaction that created the
		// row that broke it, is what keeps history from exceeding the operator's
		// limit even briefly.
		_, err = prunePolicy(tx, tenant, model.ID, RetentionPolicy{MaxVersions: store.retention.MaxVersions})
		return err
	})
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.Get(ctx, tenant, document.ID)
}

// DefaultWorkflowPageSize and MaxWorkflowPageSize bound the dashboard's
// workflow listing so a tenant with a thousand workflows cannot make one
// request load all of them, and all of their documents, into memory.
const (
	DefaultWorkflowPageSize = 100
	MaxWorkflowPageSize     = 500
)

// WorkflowFilter narrows a workflow listing. The zero value returns the first
// page of the whole tenant.
type WorkflowFilter struct {
	Limit int
	// Cursor continues a previous listing. It is opaque to callers; only
	// ListSummaries may construct one.
	Cursor string
}

// WorkflowSummary is one row of the dashboard's workflow list: the identity and
// the lifecycle state the list renders, and nothing that costs a read of its
// own.
//
// It exists because the list used to load every workflow's latest document —
// one version row per workflow, each carrying the whole canonical graph — to
// read four fields out of it. A tenant with many workflows paid for every
// document on every page view (BUG-fv5fer).
type WorkflowSummary struct {
	ID             string
	Name           string
	Active         bool
	LatestRevision int
	UpdatedAt      time.Time
}

// WorkflowSummaryPage is one page of workflow summaries.
type WorkflowSummaryPage struct {
	Workflows  []WorkflowSummary
	NextCursor string
}

// ListSummaries returns one page of workflow summaries, newest first.
//
// Pagination is keyset rather than offset based, on the sort key the list has
// always used: the cursor pins the last (updated_at, id) pair seen, so a
// workflow saved while a user pages through the list cannot shift a row onto a
// page they already read. The tiebreaker is ascending where updated_at is
// descending, matching the ORDER BY the dashboard already relied on: two
// workflows saved in the same millisecond appear in a stable, total order, and
// the cursor predicate can express exactly that order in SQL.
func (store *GORMWorkflowStore) ListSummaries(ctx context.Context, tenant TenantScope, filter WorkflowFilter) (WorkflowSummaryPage, error) {
	if err := tenant.validate(); err != nil {
		return WorkflowSummaryPage{}, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultWorkflowPageSize
	}
	if limit > MaxWorkflowPageSize {
		limit = MaxWorkflowPageSize
	}

	query := store.db.WithContext(ctx).Model(&workflowModel{}).Where("tenant_id = ?", tenant.ID)
	if filter.Cursor != "" {
		updatedAt, id, err := decodeWorkflowCursor(filter.Cursor)
		if err != nil {
			return WorkflowSummaryPage{}, err
		}
		query = query.Where("(updated_at < ?) OR (updated_at = ? AND id > ?)", updatedAt, updatedAt, id)
	}

	// Read one extra row to learn whether another page exists without a second
	// COUNT over the same predicate.
	var models []workflowModel
	if err := query.Order("updated_at DESC, id ASC").Limit(limit + 1).Find(&models).Error; err != nil {
		return WorkflowSummaryPage{}, fmt.Errorf("list workflows: %w", err)
	}

	page := WorkflowSummaryPage{Workflows: make([]WorkflowSummary, 0, limit)}
	if len(models) > limit {
		last := models[limit-1]
		page.NextCursor = encodeWorkflowCursor(last.UpdatedAt, last.ID)
		models = models[:limit]
	}
	for _, model := range models {
		page.Workflows = append(page.Workflows, WorkflowSummary{
			ID: model.ID, Name: model.Name, Active: model.Active,
			LatestRevision: model.LatestRevision, UpdatedAt: model.UpdatedAt,
		})
	}
	return page, nil
}

// encodeWorkflowCursor pins the last (updated_at, id) pair seen.
//
// The timestamp keeps the zone it was read in — deliberately, and this is the
// one subtle part of the listing. GORM stamps updated_at with time.Now(), which
// is local, and the SQLite driver renders a bound time.Time with that value's
// own offset. Round-tripping through UTC would therefore bind
// "2026-09-20 00:38:17Z" against a stored "2026-09-20 07:38:17+07:00" and match
// nothing: the second page came back empty (BUG-fv5fer). Carrying the offset
// the row was read with makes the bound value the same text the column holds,
// which is also exactly what ORDER BY compares — so the cursor's total order is
// the query's own, whatever zone the rows were written in.
func encodeWorkflowCursor(updatedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(updatedAt.Format(time.RFC3339Nano) + "\x00" + id))
}

// decodeWorkflowCursor inverts encodeWorkflowCursor. A cursor this store did
// not issue is an error rather than a position, so a client cannot invent one
// to skip a page it is not entitled to see — the tenant predicate still applies
// either way, and ErrInvalidCursor is the 400 the API answers with.
func decodeWorkflowCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: workflow cursor is malformed", ErrInvalidCursor)
	}
	timestamp, id, found := strings.Cut(string(decoded), "\x00")
	if !found || id == "" {
		return time.Time{}, "", fmt.Errorf("%w: workflow cursor is malformed", ErrInvalidCursor)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: workflow cursor is malformed", ErrInvalidCursor)
	}
	return updatedAt, id, nil
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

// GetVersionByID reads one immutable revision by its own identifier.
//
// Execution records pin a version ID rather than a revision number, and an
// inspector must replay the graph that actually ran even after later saves
// have superseded it. The workflow ID stays in the signature so a version can
// never be read through a workflow that does not own it.
func (store *GORMWorkflowStore) GetVersionByID(ctx context.Context, tenant TenantScope, workflowID, versionID string) (workflow.Version, error) {
	if err := tenant.validate(); err != nil {
		return workflow.Version{}, err
	}
	if workflowID == "" || versionID == "" {
		return workflow.Version{}, fmt.Errorf("workflow ID and version ID are required")
	}
	var model workflowVersionModel
	err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ? AND id = ?", tenant.ID, workflowID, versionID).
		First(&model).Error
	if err != nil {
		return workflow.Version{}, mapNotFound(err, "workflow version")
	}
	return versionFromModel(model)
}

// Activate compiles the latest saved revision and pins it only if the graph is
// executable.
//
// It is PublishVersion with the version left for the store to choose, so the
// two cannot drift into disagreeing about what publishing means.
func (store *GORMWorkflowStore) Activate(ctx context.Context, tenant TenantScope, workflowID string, catalog workflow.Catalog) (workflow.StoredWorkflow, error) {
	return store.publish(ctx, tenant, workflowID, "", catalog, "")
}

// PublishVersion pins one named snapshot, which is how a bad save is rolled
// back without rebuilding the canvas by hand.
func (store *GORMWorkflowStore) PublishVersion(ctx context.Context, tenant TenantScope, workflowID, versionID string, catalog workflow.Catalog, reason string) (workflow.StoredWorkflow, error) {
	if versionID == "" {
		return workflow.StoredWorkflow{}, fmt.Errorf("workflow version ID is required")
	}
	return store.publish(ctx, tenant, workflowID, versionID, catalog, reason)
}

// publish compiles one snapshot and pins it, resyncing everything that routes
// to it in the same commit.
//
// An empty versionID means the latest revision, resolved under the row lock
// rather than by the caller, so a save landing between the lookup and the pin
// cannot publish a revision nobody chose.
func (store *GORMWorkflowStore) publish(ctx context.Context, tenant TenantScope, workflowID, versionID string, catalog workflow.Catalog, reason string) (workflow.StoredWorkflow, error) {
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
		version, err := lockedVersion(tx, tenant, model, versionID)
		if err != nil {
			return err
		}
		storedVersion, err := versionFromModel(version)
		if err != nil {
			return err
		}
		if _, err := workflow.Compile(storedVersion.Document, catalog); err != nil {
			return err
		}
		if err := refuseInactiveSubworkflows(tx, tenant, model.ID, storedVersion.Document, store.subworkflows); err != nil {
			return err
		}
		if err := tx.Model(&workflowModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, model.ID).
			Updates(map[string]any{"active": true, "active_version_id": version.ID}).Error; err != nil {
			return fmt.Errorf("activate workflow: %w", err)
		}
		if store.webhooks != nil {
			// Same transaction as the activation itself: there is never a window
			// where a workflow is active but unroutable, or the reverse — and
			// publishing an earlier version never leaves the workflow serving a
			// path that belongs to the version it just stopped running.
			if err := syncWebhookBindings(tx, tenant.ID, model.ID, version.ID, store.webhooks(storedVersion.Document)); err != nil {
				return err
			}
		}
		if store.schedules != nil {
			if err := syncSchedules(tx, tenant.ID, model.ID, store.schedules(storedVersion.Document), store.next); err != nil {
				return err
			}
		}
		return appendPublishEvent(tx, tenant, model.ID, version.ID, workflow.PublishActionPublished, ActorFrom(ctx), reason)
	})
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.Get(ctx, tenant, workflowID)
}

// refuseInactiveSubworkflows fails an activation whose document calls a
// workflow that cannot run.
//
// Read inside the publishing transaction, so the answer is the state the
// activation is committing against rather than the one a moment before it. The
// caller's own row is exempt: a workflow that calls itself is refused by the
// compiler's cycle check, which is the right error for it, and a self-reference
// is not a target that has to be active.
//
// The two failures are one error each and both name the node: a target that
// does not exist in this tenant (deleted, or never imported) and one that
// exists but is not active (still a draft, or deactivated). "Not active" is
// deliberately not reported as "not found": they need different fixes.
func refuseInactiveSubworkflows(tx *gorm.DB, tenant TenantScope, workflowID string, document workflow.Document, extract SubworkflowExtractor) error {
	if extract == nil {
		return nil
	}
	for _, call := range extract(document) {
		target := strings.TrimSpace(call.WorkflowID)
		if target == "" || target == workflowID {
			continue
		}
		var model workflowModel
		if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, target).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("node %q (%s) calls workflow %q, which does not exist in this workspace: point it at a workflow that is active, or remove the node",
					call.NodeName, call.NodeID, target)
			}
			return fmt.Errorf("look up the workflow node %q calls: %w", call.NodeName, err)
		}
		if !model.Active {
			return fmt.Errorf("node %q (%s) calls workflow %q, which is not active: activate that workflow first, then activate this one",
				call.NodeName, call.NodeID, target)
		}
	}
	return nil
}

// lockedVersion resolves the snapshot a publish is about, inside the caller's
// transaction. An empty versionID means the workflow's latest revision.
func lockedVersion(tx *gorm.DB, tenant TenantScope, model workflowModel, versionID string) (workflowVersionModel, error) {
	var version workflowVersionModel
	query := tx.Where("tenant_id = ? AND workflow_id = ?", tenant.ID, model.ID)
	if versionID == "" {
		query = query.Where("revision = ?", model.LatestRevision)
	} else {
		// Scoped by workflow as well as ID so a version can never be published
		// through a workflow that does not own it.
		query = query.Where("id = ?", versionID)
	}
	if err := query.First(&version).Error; err != nil {
		return workflowVersionModel{}, mapNotFound(err, "workflow version")
	}
	return version, nil
}

// Deactivate is idempotent and retains the last active version for history.
//
// active_version_id stays populated, as it always has: the audit row is now
// what says whether the workflow is live, and the retained pointer is what
// keeps the last-published document findable.
func (store *GORMWorkflowStore) Deactivate(ctx context.Context, tenant TenantScope, workflowID string) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	model, err := store.findWorkflow(ctx, tenant, workflowID)
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	if model.Active {
		if err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model).Update("active", false).Error; err != nil {
				return fmt.Errorf("deactivate workflow: %w", err)
			}
			// Dropping the bindings with the same commit is what makes the
			// endpoint stop answering the instant the workflow is inactive.
			if err := removeWebhookBindings(tx, tenant.ID, workflowID); err != nil {
				return err
			}
			if store.schedules != nil {
				// The document, not an empty list: deactivation removes the
				// rows this workflow's own triggers own and leaves any created
				// through the schedules API, which activation never claimed
				// either.
				active, err := store.activeDocument(tx, tenant, workflowID)
				if err != nil {
					return err
				}
				if err := removeSchedules(tx, tenant.ID, workflowID, store.schedules(active)); err != nil {
					return err
				}
			}
			// The version that stopped serving, not the latest: the audit row
			// has to name what was actually live for a publish period to be
			// reconstructible from these rows alone.
			pinned := ""
			if model.ActiveVersionID != nil {
				pinned = *model.ActiveVersionID
			}
			return appendPublishEvent(tx, tenant, workflowID, pinned, workflow.PublishActionUnpublished, ActorFrom(ctx), "")
		}); err != nil {
			return workflow.StoredWorkflow{}, err
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
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model).Error; err != nil {
			return fmt.Errorf("delete workflow: %w", err)
		}
		// A soft-deleted workflow keeps its version and execution history, but
		// it must stop answering immediately.
		return removeWebhookBindings(tx, tenant.ID, workflowID)
	})
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

// activeDocument loads the document a workflow is currently active on.
//
// Deactivation needs it to know which schedule rows this workflow's own
// triggers own, as against rows somebody created through the schedules API for
// a node the document does not declare. A workflow with no active version
// returns an empty document, which owns nothing — and so removes nothing.
func (store *GORMWorkflowStore) activeDocument(tx *gorm.DB, tenant TenantScope, workflowID string) (workflow.Document, error) {
	var model workflowModel
	if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).First(&model).Error; err != nil {
		return workflow.Document{}, mapNotFound(err, "workflow")
	}
	if model.ActiveVersionID == nil {
		return workflow.Document{}, nil
	}
	var versionModel workflowVersionModel
	if err := tx.Where("tenant_id = ? AND workflow_id = ? AND id = ?", tenant.ID, workflowID, *model.ActiveVersionID).
		First(&versionModel).Error; err != nil {
		return workflow.Document{}, mapNotFound(err, "workflow version")
	}
	version, err := versionFromModel(versionModel)
	if err != nil {
		return workflow.Document{}, err
	}
	return version.Document, nil
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
