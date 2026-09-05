package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// ErrInvalidCursor reports a pagination cursor the caller did not receive from
// a previous listing. Callers translate it into a 400, never a 500.
var ErrInvalidCursor = errors.New("repository cursor is invalid")

// WorkflowRepository is the persistence seam used by lifecycle services and
// later API handlers. The execution engine never imports GORM.
type WorkflowRepository interface {
	SaveDraft(context.Context, TenantScope, workflow.Document) (workflow.StoredWorkflow, error)
	List(context.Context, TenantScope) ([]workflow.StoredWorkflow, error)
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
	// retention bounds how much history survives. The zero value keeps
	// everything, which is what an installation that never configured this gets.
	retention RetentionPolicy
}

var _ WorkflowRepository = (*GORMWorkflowStore)(nil)

// NewWorkflowStore constructs the GORM-backed workflow persistence boundary.
func NewWorkflowStore(db *gorm.DB) *GORMWorkflowStore {
	return &GORMWorkflowStore{db: db}
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

		version, err := appendVersion(tx, tenant, model, definition, document, nil, ActorFrom(ctx))
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
