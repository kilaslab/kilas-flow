package repository

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// DefaultVersionPageSize and MaxVersionPageSize bound a history listing so a
// client cannot ask for an unbounded scan of a workflow somebody has been
// saving for a year.
const (
	DefaultVersionPageSize = 25
	MaxVersionPageSize     = 100
)

// VersionFilter narrows a workflow history listing. The zero value returns the
// newest page.
type VersionFilter struct {
	Limit int
	// Cursor continues a previous listing. It is opaque to callers; only
	// ListVersions may construct one.
	Cursor string
}

// VersionPage is one page of history summaries, newest first.
type VersionPage struct {
	Versions   []workflow.VersionSummary
	NextCursor string
}

// actorContextKey carries the identity a write should be attributed to.
type actorContextKey struct{}

// WithActor tags a context with the identity responsible for a write.
//
// It is a context value rather than a parameter on every method because the
// main API has no authentication yet: today nothing populates it, and when
// V2-p8-1 lands, auth sets it once at the request boundary instead of
// threading an actor through signatures that would otherwise all have to
// change at once.
func WithActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFrom returns the identity a write is attributed to, or empty when the
// caller is genuinely unknown.
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorContextKey{}).(string)
	return actor
}

// RetentionPolicy bounds how much version history survives.
//
// Both zero values mean keep everything, which is deliberately what an
// installation that has never configured retention gets: an upgrade must never
// silently delete a customer's history.
type RetentionPolicy struct {
	// MaxAge drops versions older than this. Zero keeps every age.
	MaxAge time.Duration
	// MaxVersions keeps only the newest N revisions. Zero keeps every count.
	MaxVersions int
}

func (policy RetentionPolicy) prunes() bool {
	return policy.MaxAge > 0 || policy.MaxVersions > 0
}

// ListVersions returns one page of a workflow's history, newest first.
//
// Pagination is keyset rather than offset based, on revision alone: revision is
// unique within a workflow and strictly increasing, so it orders the history
// exactly and a save landing mid-page cannot shift a row onto a page the caller
// already read.
func (store *GORMWorkflowStore) ListVersions(ctx context.Context, tenant TenantScope, workflowID string, filter VersionFilter) (VersionPage, error) {
	if err := tenant.validate(); err != nil {
		return VersionPage{}, err
	}
	// The workflow is read first so an unknown ID is a 404 rather than an empty
	// page, which would look to a caller like a workflow with no history.
	model, err := store.findWorkflow(ctx, tenant, workflowID)
	if err != nil {
		return VersionPage{}, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultVersionPageSize
	}
	if limit > MaxVersionPageSize {
		limit = MaxVersionPageSize
	}

	query := store.db.WithContext(ctx).Model(&workflowVersionModel{}).
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID)
	if filter.Cursor != "" {
		revision, err := decodeVersionCursor(filter.Cursor)
		if err != nil {
			return VersionPage{}, err
		}
		query = query.Where("revision < ?", revision)
	}

	// Read one extra row to learn whether another page exists without a second
	// COUNT over the same predicate.
	var models []workflowVersionModel
	if err := query.Order("revision DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return VersionPage{}, fmt.Errorf("list workflow versions: %w", err)
	}

	page := VersionPage{Versions: make([]workflow.VersionSummary, 0, limit)}
	if len(models) > limit {
		page.NextCursor = encodeVersionCursor(models[limit-1].Revision)
		models = models[:limit]
	}
	published := ""
	if model.ActiveVersionID != nil {
		published = *model.ActiveVersionID
	}
	for _, version := range models {
		page.Versions = append(page.Versions, versionSummary(version, model.LatestRevision, published))
	}
	return page, nil
}

func versionSummary(model workflowVersionModel, latestRevision int, publishedID string) workflow.VersionSummary {
	summary := workflow.VersionSummary{
		ID: model.ID, WorkflowID: model.WorkflowID, Revision: model.Revision,
		SchemaVersion: model.SchemaVersion, CreatedAt: model.CreatedAt,
		Draft:     model.Revision == latestRevision,
		Published: publishedID != "" && model.ID == publishedID,
	}
	if model.Label != nil {
		summary.Label = *model.Label
	}
	if model.CreatedBy != nil {
		summary.CreatedBy = *model.CreatedBy
	}
	return summary
}

func encodeVersionCursor(revision int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(revision)))
}

func decodeVersionCursor(cursor string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: workflow version cursor is malformed", ErrInvalidCursor)
	}
	revision, err := strconv.Atoi(string(decoded))
	if err != nil {
		return 0, fmt.Errorf("%w: workflow version cursor is malformed", ErrInvalidCursor)
	}
	return revision, nil
}

// RestoreVersion appends a new revision carrying an older snapshot's document.
//
// History is append-only: the snapshot restored from stays in the list exactly
// as it was, and nothing rewrites a row. The read and the append share one
// transaction so a concurrent save cannot interleave and leave a revision whose
// document nobody asked for.
func (store *GORMWorkflowStore) RestoreVersion(ctx context.Context, tenant TenantScope, workflowID, versionID, reason string) (workflow.StoredWorkflow, error) {
	if err := tenant.validate(); err != nil {
		return workflow.StoredWorkflow{}, err
	}
	if workflowID == "" || versionID == "" {
		return workflow.StoredWorkflow{}, fmt.Errorf("workflow ID and version ID are required")
	}

	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model workflowModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).
			First(&model).Error; err != nil {
			return mapNotFound(err, "workflow")
		}
		source, err := lockedVersion(tx, tenant, model, versionID)
		if err != nil {
			return err
		}
		// The stored bytes are reused verbatim rather than re-marshalled from a
		// decoded document, so a restore is byte-identical to what was saved
		// even if the document struct has since gained a field.
		restored, err := versionFromModel(source)
		if err != nil {
			return err
		}
		label := fmt.Sprintf("Restored from revision %d", source.Revision)
		version, err := appendVersion(tx, tenant, model, source.Definition, restored.Document, nil, &label, ActorFrom(ctx))
		if err != nil {
			return err
		}
		if err := tx.Model(&workflowModel{}).
			Where("tenant_id = ? AND id = ?", tenant.ID, model.ID).
			Updates(map[string]any{"name": restored.Document.Name, "latest_revision": version.Revision}).Error; err != nil {
			return fmt.Errorf("update workflow: %w", err)
		}
		if _, err := prunePolicy(tx, tenant, model.ID, RetentionPolicy{MaxVersions: store.retention.MaxVersions}); err != nil {
			return err
		}
		return appendPublishEvent(tx, tenant, model.ID, source.ID, workflow.PublishActionRestored, ActorFrom(ctx), reason)
	})
	if err != nil {
		return workflow.StoredWorkflow{}, err
	}
	return store.Get(ctx, tenant, workflowID)
}

// appendVersion writes the next revision of a workflow inside the caller's
// transaction. It is the single append path, so a restore and a save cannot
// disagree about what a new revision looks like.
//
// diagnostics is the import report the revision was created with, and is nil
// for every writer that is not an import.
func appendVersion(tx *gorm.DB, tenant TenantScope, model workflowModel, definition []byte, document workflow.Document, diagnostics []byte, label *string, actor string) (workflowVersionModel, error) {
	versionID, err := workflow.NewID("wfv")
	if err != nil {
		return workflowVersionModel{}, err
	}
	version := workflowVersionModel{
		ID:            versionID,
		TenantID:      tenant.ID,
		WorkflowID:    model.ID,
		Revision:      model.LatestRevision + 1,
		SchemaVersion: document.SchemaVersion,
		Definition:    definition,
		// A restore copies an older revision's document, deliberately not its
		// report: the new revision is a restore, not an import, and carrying the
		// old report forward would claim this revision's nodes were translated
		// by an importer when they were restored by hand.
		Diagnostics: diagnostics,
		Label:       label,
	}
	if actor != "" {
		version.CreatedBy = &actor
	}
	if err := tx.Create(&version).Error; err != nil {
		return workflowVersionModel{}, fmt.Errorf("create workflow version: %w", err)
	}
	return version, nil
}

// ListPublishEvents returns a workflow's publish audit trail, newest first.
func (store *GORMWorkflowStore) ListPublishEvents(ctx context.Context, tenant TenantScope, workflowID string) ([]workflow.PublishEvent, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	if _, err := store.findWorkflow(ctx, tenant, workflowID); err != nil {
		return nil, err
	}
	var models []workflowPublishEventModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).
		Order("created_at DESC, id DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list workflow publish events: %w", err)
	}
	events := make([]workflow.PublishEvent, 0, len(models))
	for _, model := range models {
		events = append(events, workflow.PublishEvent{
			WorkflowID: model.WorkflowID, VersionID: model.VersionID,
			Action: workflow.PublishAction(model.Action), Actor: model.Actor,
			Reason: model.Reason, CreatedAt: model.CreatedAt,
		})
	}
	return events, nil
}

func appendPublishEvent(tx *gorm.DB, tenant TenantScope, workflowID, versionID string, action workflow.PublishAction, actor, reason string) error {
	event := workflowPublishEventModel{
		TenantID: tenant.ID, WorkflowID: workflowID, VersionID: versionID,
		Action: string(action), Actor: actor, Reason: reason,
		CreatedAt: time.Now().UTC(),
	}
	if err := tx.Create(&event).Error; err != nil {
		return fmt.Errorf("record workflow publish event: %w", err)
	}
	return nil
}

// PruneVersions drops the history one workflow's retention policy no longer
// covers, and reports how many rows went.
func (store *GORMWorkflowStore) PruneVersions(ctx context.Context, tenant TenantScope, workflowID string) (int, error) {
	if err := tenant.validate(); err != nil {
		return 0, err
	}
	var pruned int
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		pruned, err = prunePolicy(tx, tenant, workflowID, store.retention)
		return err
	})
	return pruned, err
}

// PruneAllVersions applies the age bound across every workflow in the store.
//
// The age bound cannot ride on a write the way the count bound does, because it
// has to fire for a workflow nobody is saving — which is exactly the workflow
// whose history has gone stale.
func (store *GORMWorkflowStore) PruneAllVersions(ctx context.Context) (int, error) {
	if !store.retention.prunes() {
		return 0, nil
	}
	type owner struct {
		TenantID string
		ID       string
	}
	var owners []owner
	if err := store.db.WithContext(ctx).Model(&workflowModel{}).
		Select("tenant_id", "id").Find(&owners).Error; err != nil {
		return 0, fmt.Errorf("list workflows to prune: %w", err)
	}
	total := 0
	for _, each := range owners {
		// One transaction per workflow rather than one for the sweep: a sweep
		// over a large installation would otherwise hold a write transaction
		// open across every workflow it touches, and on SQLite that is the
		// whole database.
		pruned, err := store.PruneVersions(ctx, TenantScope{ID: each.TenantID}, each.ID)
		if err != nil {
			return total, err
		}
		total += pruned
	}
	return total, nil
}

// prunePolicy deletes the versions a policy no longer covers, inside the
// caller's transaction.
//
// Candidates are chosen in Go rather than in one DELETE with correlated
// subqueries because the exclusions are the whole point of this function, and a
// reader has to be able to see each of them. The scan is bounded by one
// workflow's history and reads three columns.
func prunePolicy(tx *gorm.DB, tenant TenantScope, workflowID string, policy RetentionPolicy) (int, error) {
	if !policy.prunes() {
		return 0, nil
	}

	var model workflowModel
	if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).First(&model).Error; err != nil {
		return 0, mapNotFound(err, "workflow")
	}

	type candidate struct {
		ID        string
		Revision  int
		CreatedAt time.Time
	}
	var versions []candidate
	if err := tx.Model(&workflowVersionModel{}).
		Select("id", "revision", "created_at").
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).
		Order("revision DESC").
		Find(&versions).Error; err != nil {
		return 0, fmt.Errorf("read workflow versions to prune: %w", err)
	}

	protected, err := protectedVersionIDs(tx, tenant, workflowID, model)
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().UTC().Add(-policy.MaxAge)
	var doomed []string
	for index, version := range versions {
		if _, keep := protected[version.ID]; keep {
			continue
		}
		// versions is newest-first, so index is how many newer revisions exist.
		overCount := policy.MaxVersions > 0 && index >= policy.MaxVersions
		tooOld := policy.MaxAge > 0 && version.CreatedAt.Before(cutoff)
		if overCount || tooOld {
			doomed = append(doomed, version.ID)
		}
	}
	if len(doomed) == 0 {
		return 0, nil
	}

	result := tx.Where("tenant_id = ? AND workflow_id = ? AND id IN ?", tenant.ID, workflowID, doomed).
		Delete(&workflowVersionModel{})
	if result.Error != nil {
		return 0, fmt.Errorf("prune workflow versions: %w", result.Error)
	}
	return int(result.RowsAffected), nil
}

// protectedVersionIDs names every version a prune must not touch.
//
// The execution set is not merely a courtesy: executions.workflow_version_id is
// declared ON DELETE RESTRICT, so a delete that touched one would fail the whole
// statement — and working around the constraint would be worse, because the
// execution detail view fetches the pinned version by ID to draw the graph that
// actually ran, and a missing row turns a finished execution into an error page.
// webhook_bindings.workflow_version_id has no foreign key at all, so nothing but
// this function protects it.
func protectedVersionIDs(tx *gorm.DB, tenant TenantScope, workflowID string, model workflowModel) (map[string]struct{}, error) {
	protected := map[string]struct{}{}

	if model.ActiveVersionID != nil {
		protected[*model.ActiveVersionID] = struct{}{}
	}

	var latest workflowVersionModel
	err := tx.Select("id").
		Where("tenant_id = ? AND workflow_id = ? AND revision = ?", tenant.ID, workflowID, model.LatestRevision).
		First(&latest).Error
	if err == nil {
		protected[latest.ID] = struct{}{}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("read the latest workflow version: %w", err)
	}

	// Both lookups are scoped to this workflow rather than scanning the whole
	// table: an execution or a binding can only ever point at a version of the
	// workflow it belongs to, so the scope costs nothing in safety and keeps a
	// per-workflow prune from reading every execution in the installation.
	var pinnedByExecutions []string
	if err := tx.Model(&executionModel{}).
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).
		Distinct().Pluck("workflow_version_id", &pinnedByExecutions).Error; err != nil {
		return nil, fmt.Errorf("read versions pinned by executions: %w", err)
	}
	for _, id := range pinnedByExecutions {
		protected[id] = struct{}{}
	}

	var pinnedByBindings []string
	if err := tx.Model(&webhookBindingModel{}).
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).
		Distinct().Pluck("workflow_version_id", &pinnedByBindings).Error; err != nil {
		return nil, fmt.Errorf("read versions pinned by webhook bindings: %w", err)
	}
	for _, id := range pinnedByBindings {
		protected[id] = struct{}{}
	}

	return protected, nil
}
