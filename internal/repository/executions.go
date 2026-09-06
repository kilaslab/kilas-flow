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

	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ExecutionRepository is the persistence seam for the graph engine. Payloads
// entering this interface are redaction-ready; transport data is never stored
// implicitly by GORM.
type ExecutionRepository interface {
	Create(context.Context, TenantScope, execution.Record) (execution.Record, error)
	QueueManualLatest(context.Context, TenantScope, string, workflow.Catalog, json.RawMessage) (execution.Record, error)
	QueueTriggered(context.Context, TenantScope, string, string, execution.Trigger, string, json.RawMessage) (execution.Record, error)
	Get(context.Context, TenantScope, string) (execution.Record, error)
	List(context.Context, TenantScope, ExecutionFilter) (ExecutionPage, error)
	CreateNodeRun(context.Context, TenantScope, execution.NodeRun) (execution.NodeRun, error)
	// Ancestry returns an execution's chain of parent executions, nearest
	// first, for a sub-workflow run.
	Ancestry(context.Context, TenantScope, string) ([]execution.Record, error)
}

// DefaultExecutionPageSize and MaxExecutionPageSize bound a history listing so
// a client cannot ask for an unbounded scan of a long-lived workspace.
const (
	DefaultExecutionPageSize = 25
	MaxExecutionPageSize     = 100
)

// ExecutionFilter narrows an execution history listing. The zero value lists
// the newest executions across every workflow in the tenant.
type ExecutionFilter struct {
	WorkflowID string
	Statuses   []execution.Status
	Trigger    execution.Trigger
	Limit      int
	// Cursor continues a previous listing. It is opaque to callers; only List
	// may construct one.
	Cursor string
}

// ExecutionPage is one page of execution summaries. Records carry no node-run
// trace: a history list must not load every payload it will never show.
type ExecutionPage struct {
	Records    []execution.Record
	NextCursor string
}

// GORMExecutionStore is the GORM implementation of ExecutionRepository.
type GORMExecutionStore struct {
	db *gorm.DB
}

var _ ExecutionRepository = (*GORMExecutionStore)(nil)

// NewExecutionStore constructs the durable execution persistence boundary.
func NewExecutionStore(db *gorm.DB) *GORMExecutionStore {
	return &GORMExecutionStore{db: db}
}

// QueueManualLatest validates the latest workflow revision and persists a
// queued manual execution while holding the workflow row lock. Keeping those
// actions together prevents a concurrent draft save from making the queued
// execution point at a revision that was already superseded when it persisted.
func (store *GORMExecutionStore) QueueManualLatest(ctx context.Context, tenant TenantScope, workflowID string, catalog workflow.Catalog, input json.RawMessage) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	if workflowID == "" {
		return execution.Record{}, fmt.Errorf("workflow ID is required")
	}
	if catalog == nil {
		return execution.Record{}, fmt.Errorf("workflow catalog is required for manual run")
	}
	inputPayload, err := triggerPayload(input)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution input: %w", err)
	}
	executionID, err := workflow.NewID("exec")
	if err != nil {
		return execution.Record{}, err
	}
	startedAt := time.Now().UTC()

	var model executionModel
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent workflowModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", tenant.ID, workflowID).
			First(&parent).Error; err != nil {
			return mapNotFound(err, "workflow")
		}

		var version workflowVersionModel
		if err := tx.Where("tenant_id = ? AND workflow_id = ? AND revision = ?", tenant.ID, workflowID, parent.LatestRevision).
			First(&version).Error; err != nil {
			return mapNotFound(err, "workflow version")
		}
		storedVersion, err := versionFromModel(version)
		if err != nil {
			return err
		}
		if _, err := workflow.Compile(storedVersion.Document, catalog); err != nil {
			return err
		}

		model = executionModel{
			ID:                executionID,
			TenantID:          tenant.ID,
			WorkflowID:        parent.ID,
			WorkflowVersionID: version.ID,
			Status:            string(execution.StatusQueued),
			Trigger:           string(execution.TriggerManual),
			Input:             inputPayload,
			Output:            []byte("null"),
			Error:             []byte("null"),
			StartedAt:         startedAt,
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create execution: %w", err)
		}
		// pg_notify fires on commit, so a rolled-back queue never wakes a
		// worker for a row that is not there.
		return notifyExecutionQueued(tx, tenant.ID, executionID)
	})
	if err != nil {
		return execution.Record{}, err
	}
	return executionFromModel(model), nil
}

// QueueTriggered persists a queued execution against one pinned revision.
//
// Webhooks and schedules run the *active* revision, not the latest draft: an
// endpoint that silently started running unsaved work the moment someone typed
// in the editor would be indefensible. The caller supplies the version its
// binding or schedule was activated against.
func (store *GORMExecutionStore) QueueTriggered(ctx context.Context, tenant TenantScope, workflowID, versionID string, trigger execution.Trigger, triggerNodeID string, input json.RawMessage) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	if workflowID == "" || versionID == "" {
		return execution.Record{}, fmt.Errorf("workflow ID and version ID are required")
	}
	if trigger == "" {
		return execution.Record{}, fmt.Errorf("execution trigger is required")
	}
	inputPayload, err := triggerPayload(input)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution input: %w", err)
	}
	executionID, err := workflow.NewID("exec")
	if err != nil {
		return execution.Record{}, err
	}

	model := executionModel{
		ID: executionID, TenantID: tenant.ID, WorkflowID: workflowID, WorkflowVersionID: versionID,
		Status: string(execution.StatusQueued), Trigger: string(trigger), TriggerNodeID: triggerNodeID,
		Input: inputPayload, Output: []byte("null"), Error: []byte("null"), StartedAt: time.Now().UTC(),
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Re-reading the workflow inside the transaction is what stops a
		// deactivation racing a queued trigger into an execution nobody wanted.
		var parent workflowModel
		if err := tx.Where("tenant_id = ? AND id = ? AND active = ?", tenant.ID, workflowID, true).First(&parent).Error; err != nil {
			return mapNotFound(err, "active workflow")
		}
		if parent.ActiveVersionID == nil || *parent.ActiveVersionID != versionID {
			return fmt.Errorf("%w: workflow version is no longer active", ErrNotFound)
		}
		if err := tx.Create(&model).Error; err != nil {
			return err
		}
		return notifyExecutionQueued(tx, tenant.ID, executionID)
	})
	if err != nil {
		return execution.Record{}, err
	}
	return executionFromModel(model), nil
}

// Create persists an execution only when the requested workflow revision exists
// under the caller's tenant and belongs to the supplied workflow identity.
func (store *GORMExecutionStore) Create(ctx context.Context, tenant TenantScope, record execution.Record) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	if err := validateExecution(record); err != nil {
		return execution.Record{}, err
	}
	if record.ID == "" {
		id, err := workflow.NewID("exec")
		if err != nil {
			return execution.Record{}, err
		}
		record.ID = id
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}

	// The execution's own input is the trigger payload; its output and error
	// are produced by nodes and can hold resolved credentials.
	input, err := triggerPayload(record.Input)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution input: %w", err)
	}
	output, err := payload(record.Output)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution output: %w", err)
	}
	errorPayload, err := payload(record.Error)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution error: %w", err)
	}

	model := executionModel{
		ID:                record.ID,
		TenantID:          tenant.ID,
		WorkflowID:        record.WorkflowID,
		WorkflowVersionID: record.WorkflowVersionID,
		Status:            string(record.Status),
		Trigger:           string(record.Trigger),
		TriggerNodeID:     record.TriggerNodeID,
		ParentExecutionID: record.ParentExecutionID,
		Input:             input,
		Output:            output,
		Error:             errorPayload,
		StartedAt:         record.StartedAt,
		FinishedAt:        record.FinishedAt,
	}
	if err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent workflowModel
		if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, record.WorkflowID).First(&parent).Error; err != nil {
			return mapNotFound(err, "workflow")
		}
		var version workflowVersionModel
		if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, record.WorkflowVersionID).First(&version).Error; err != nil {
			return mapNotFound(err, "workflow version")
		}
		if version.WorkflowID != record.WorkflowID {
			return fmt.Errorf("%w: workflow version", ErrNotFound)
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create execution: %w", err)
		}
		return notifyExecutionQueued(tx, tenant.ID, record.ID)
	}); err != nil {
		return execution.Record{}, err
	}
	return executionFromModel(model), nil
}

// Get loads one execution and its node runs under the requested tenant scope.
func (store *GORMExecutionStore) Get(ctx context.Context, tenant TenantScope, executionID string) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	var model executionModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, executionID).First(&model).Error; err != nil {
		return execution.Record{}, mapNotFound(err, "execution")
	}
	var nodeModels []executionNodeRunModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND execution_id = ?", tenant.ID, executionID).
		Order("sequence ASC, attempt ASC").Find(&nodeModels).Error; err != nil {
		return execution.Record{}, fmt.Errorf("list execution node runs: %w", err)
	}
	record := executionFromModel(model)
	record.NodeRuns = make([]execution.NodeRun, 0, len(nodeModels))
	for _, nodeModel := range nodeModels {
		record.NodeRuns = append(record.NodeRuns, nodeRunFromModel(nodeModel))
	}
	return record, nil
}

// List returns one page of execution summaries, newest first.
//
// Pagination is keyset rather than offset based: the cursor pins the last
// (started_at, id) pair seen, so an execution created while a user pages
// through history cannot shift rows onto a page they already read.
func (store *GORMExecutionStore) List(ctx context.Context, tenant TenantScope, filter ExecutionFilter) (ExecutionPage, error) {
	if err := tenant.validate(); err != nil {
		return ExecutionPage{}, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultExecutionPageSize
	}
	if limit > MaxExecutionPageSize {
		limit = MaxExecutionPageSize
	}

	query := store.db.WithContext(ctx).Model(&executionModel{}).Where("tenant_id = ?", tenant.ID)
	if filter.WorkflowID != "" {
		query = query.Where("workflow_id = ?", filter.WorkflowID)
	}
	if len(filter.Statuses) > 0 {
		statuses := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			statuses = append(statuses, string(status))
		}
		query = query.Where("status IN ?", statuses)
	}
	if filter.Trigger != "" {
		query = query.Where("trigger = ?", string(filter.Trigger))
	}
	if filter.Cursor != "" {
		startedAt, id, err := decodeExecutionCursor(filter.Cursor)
		if err != nil {
			return ExecutionPage{}, err
		}
		query = query.Where("(started_at < ?) OR (started_at = ? AND id < ?)", startedAt, startedAt, id)
	}

	// Read one extra row to learn whether another page exists without a
	// second COUNT query over the same predicate.
	var models []executionModel
	if err := query.Order("started_at DESC, id DESC").Limit(limit + 1).Find(&models).Error; err != nil {
		return ExecutionPage{}, fmt.Errorf("list executions: %w", err)
	}

	page := ExecutionPage{Records: make([]execution.Record, 0, limit)}
	if len(models) > limit {
		last := models[limit-1]
		page.NextCursor = encodeExecutionCursor(last.StartedAt, last.ID)
		models = models[:limit]
	}
	for _, model := range models {
		page.Records = append(page.Records, executionFromModel(model))
	}
	return page, nil
}

func encodeExecutionCursor(startedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(startedAt.UTC().Format(time.RFC3339Nano) + "\x00" + id))
}

func decodeExecutionCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: execution cursor is malformed", ErrInvalidCursor)
	}
	timestamp, id, found := strings.Cut(string(decoded), "\x00")
	if !found || id == "" {
		return time.Time{}, "", fmt.Errorf("%w: execution cursor is malformed", ErrInvalidCursor)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: execution cursor is malformed", ErrInvalidCursor)
	}
	return startedAt.UTC(), id, nil
}

// ClaimNext atomically assigns the oldest queued execution to one local
// worker and returns the immutable document pinned by that record. A lease is
// persisted with the claim so a later worker can recover abandoned work.
func (store *GORMExecutionStore) ClaimNext(ctx context.Context, workerID string, leaseUntil time.Time) (execution.Record, workflow.Document, bool, error) {
	if workerID == "" || leaseUntil.IsZero() {
		return execution.Record{}, workflow.Document{}, false, fmt.Errorf("worker ID and lease expiry are required")
	}
	leaseUntil = leaseUntil.UTC()
	leaseID, err := workflow.NewID("lease")
	if err != nil {
		return execution.Record{}, workflow.Document{}, false, err
	}
	leaseOwner := workerID + "/" + leaseID
	var claimed executionModel
	var document workflow.Document
	found := false
	now := time.Now().UTC()
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidate executionModel
		// SKIP LOCKED fans contending workers out over distinct rows instead
		// of racing them all for the oldest one. The glebarez SQLite driver
		// drops clause.Locking silently ("SQLite3 does not support row-level
		// locking"), so on SQLite this is a plain SELECT and correctness
		// rests on the conditional UPDATE below; on PostgreSQL the lock is
		// real. Either way the UPDATE re-applies the whole predicate and a
		// lost race reads as RowsAffected == 0, never as a double claim.
		//
		// First appends its own primary-key ordering, so the statement the
		// server receives ends ORDER BY started_at ASC, id ASC,
		// "executions"."id" LIMIT 1 — any EXPLAIN evidence must use that
		// spelling, not the two-column Order above.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}).
			Where("status = ? OR ((status = ? OR status = ?) AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)",
				string(execution.StatusQueued), string(execution.StatusRunning), string(execution.StatusCancelling), now).
			Order("started_at ASC, id ASC").
			First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find queued execution: %w", err)
		}
		reclaimingExpiredLease := candidate.Status == string(execution.StatusRunning)
		nextStatus := execution.StatusRunning
		if candidate.Status == string(execution.StatusCancelling) {
			nextStatus = execution.StatusCancelling
		}
		result := tx.Model(&executionModel{}).
			Where("id = ? AND tenant_id = ? AND (status = ? OR ((status = ? OR status = ?) AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
				candidate.ID, candidate.TenantID, string(execution.StatusQueued), string(execution.StatusRunning), string(execution.StatusCancelling), now).
			Updates(map[string]any{
				"status":           string(nextStatus),
				"lease_owner":      leaseOwner,
				"lease_expires_at": leaseUntil,
			})
		if result.Error != nil {
			return fmt.Errorf("claim execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if reclaimingExpiredLease {
			// Node runs are written after the in-memory graph has completed. A
			// process can die between individual trace writes, leaving a partial
			// attempt that would otherwise conflict with the recovered attempt's
			// (execution, sequence) and (execution, node, attempt) keys.
			if err := tx.Where("tenant_id = ? AND execution_id = ?", candidate.TenantID, candidate.ID).Delete(&executionNodeRunModel{}).Error; err != nil {
				return fmt.Errorf("clear abandoned execution trace: %w", err)
			}
		}
		candidate.Status = string(nextStatus)
		candidate.LeaseOwner = leaseOwner
		candidate.LeaseExpiresAt = &leaseUntil

		var version workflowVersionModel
		if err := tx.Where("tenant_id = ? AND id = ?", candidate.TenantID, candidate.WorkflowVersionID).First(&version).Error; err != nil {
			return mapNotFound(err, "workflow version")
		}
		storedVersion, err := versionFromModel(version)
		if err != nil {
			return err
		}
		claimed, document, found = candidate, storedVersion.Document, true
		return nil
	})
	if err != nil {
		return execution.Record{}, workflow.Document{}, false, err
	}
	if !found {
		return execution.Record{}, workflow.Document{}, false, nil
	}
	return executionFromModel(claimed), document, true, nil
}

// UpdateRuntime persists the terminal state and safe result data written by
// the engine after it has claimed an execution.
func (store *GORMExecutionStore) UpdateRuntime(ctx context.Context, tenant TenantScope, record execution.Record) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	if record.ID == "" || record.Status == "" || record.LeaseOwner == "" {
		return execution.Record{}, fmt.Errorf("execution ID, status, and lease owner are required")
	}
	output, err := payload(record.Output)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution output: %w", err)
	}
	errorPayload, err := payload(record.Error)
	if err != nil {
		return execution.Record{}, fmt.Errorf("execution error: %w", err)
	}
	cancellationError := []byte(`{"code":"execution.cancelled","message":"execution cancellation was accepted before completion"}`)

	// Two updates rather than one carrying a CASE, and the reason is a defect
	// this shape caused rather than a preference. The CASE branches were all
	// untyped placeholders, which PostgreSQL types as `text`; assigning that
	// into the `bytea` output and error columns is refused during parse
	// analysis, so the *whole* UPDATE was rejected and status, finished_at and
	// the lease columns were never written either. SQLite's type affinity
	// accepted the identical statement, so the tier people run in production
	// was the only one that broke: every execution stayed `running`, its lease
	// expired, and the workflow ran again, once a minute, for ever.
	//
	// Written as two statements because each placeholder then lands directly
	// in the column it belongs to and the driver types it from that column.
	// Exactly one of them matches — an execution's status is either cancelling
	// or running, never both — and they run inside one transaction so the row
	// cannot change status between them.
	var affected int64
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cancelled := tx.Model(&executionModel{}).
			Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status = ?",
				tenant.ID, record.ID, record.LeaseOwner, string(execution.StatusCancelling)).
			Updates(map[string]any{
				"status":           string(execution.StatusCancelled),
				"output":           []byte("null"),
				"error":            cancellationError,
				"finished_at":      record.FinishedAt,
				"lease_owner":      "",
				"lease_expires_at": nil,
			})
		if cancelled.Error != nil {
			return cancelled.Error
		}
		affected = cancelled.RowsAffected
		if affected > 0 {
			// The cancellation won the race; the reported outcome is discarded
			// deliberately, which is what the CASE was expressing.
			return nil
		}
		finished := tx.Model(&executionModel{}).
			Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status = ?",
				tenant.ID, record.ID, record.LeaseOwner, string(execution.StatusRunning)).
			Updates(map[string]any{
				"status":           string(record.Status),
				"output":           output,
				"error":            errorPayload,
				"finished_at":      record.FinishedAt,
				"lease_owner":      "",
				"lease_expires_at": nil,
			})
		if finished.Error != nil {
			return finished.Error
		}
		affected = finished.RowsAffected
		return nil
	})
	if err != nil {
		return execution.Record{}, fmt.Errorf("update execution runtime state: %w", err)
	}
	if affected == 0 {
		return execution.Record{}, ErrNotFound
	}
	return store.Get(ctx, tenant, record.ID)
}

// Cancel persists a cancellation request. Queued work becomes terminal before
// any worker can claim it; running work enters cancelling so its local worker
// can observe the request and write the final cancelled state.
func (store *GORMExecutionStore) Cancel(ctx context.Context, tenant TenantScope, executionID string) (execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, err
	}
	if executionID == "" {
		return execution.Record{}, fmt.Errorf("execution ID is required")
	}
	var model executionModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, executionID).First(&model).Error; err != nil {
		return execution.Record{}, mapNotFound(err, "execution")
	}
	now := time.Now().UTC()
	updates := map[string]any{}
	switch execution.Status(model.Status) {
	case execution.StatusQueued:
		updates["status"] = string(execution.StatusCancelled)
		updates["finished_at"] = &now
		updates["lease_owner"] = ""
		updates["lease_expires_at"] = nil
	case execution.StatusRunning:
		updates["status"] = string(execution.StatusCancelling)
		updates["cancellation_requested_at"] = &now
	case execution.StatusCancelling, execution.StatusCancelled, execution.StatusSucceeded, execution.StatusFailed:
		return executionFromModel(model), nil
	default:
		return execution.Record{}, fmt.Errorf("execution %q has unsupported status %q", executionID, model.Status)
	}
	if err := store.db.WithContext(ctx).Model(&executionModel{}).
		Where("tenant_id = ? AND id = ?", tenant.ID, executionID).Updates(updates).Error; err != nil {
		return execution.Record{}, fmt.Errorf("cancel execution: %w", err)
	}
	return store.Get(ctx, tenant, executionID)
}

// CreateNodeRun appends a node attempt only to an execution visible to the
// current tenant.
func (store *GORMExecutionStore) CreateNodeRun(ctx context.Context, tenant TenantScope, nodeRun execution.NodeRun) (execution.NodeRun, error) {
	if err := tenant.validate(); err != nil {
		return execution.NodeRun{}, err
	}
	if err := validateNodeRun(nodeRun); err != nil {
		return execution.NodeRun{}, err
	}
	if nodeRun.ID == "" {
		id, err := workflow.NewID("run")
		if err != nil {
			return execution.NodeRun{}, err
		}
		nodeRun.ID = id
	}
	if nodeRun.StartedAt.IsZero() {
		nodeRun.StartedAt = time.Now().UTC()
	}
	input, err := payload(nodeRun.Input)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("node run input: %w", err)
	}
	output, err := payload(nodeRun.Output)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("node run output: %w", err)
	}
	errorPayload, err := payload(nodeRun.Error)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("node run error: %w", err)
	}
	model := executionNodeRunModel{
		ID:          nodeRun.ID,
		TenantID:    tenant.ID,
		ExecutionID: nodeRun.ExecutionID,
		NodeID:      nodeRun.NodeID,
		Attempt:     nodeRun.Attempt,
		RunIndex:    nodeRun.RunIndex,
		Sequence:    nodeRun.Sequence,
		Status:      string(nodeRun.Status),
		Input:       input,
		Output:      output,
		Error:       errorPayload,
		StartedAt:   nodeRun.StartedAt,
		FinishedAt:  nodeRun.FinishedAt,
	}
	if err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent executionModel
		if err := tx.Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status IN (?, ?)", tenant.ID, nodeRun.ExecutionID, nodeRun.LeaseOwner, string(execution.StatusRunning), string(execution.StatusCancelling)).First(&parent).Error; err != nil {
			return mapNotFound(err, "execution")
		}
		if err := tx.Create(&model).Error; err != nil {
			return fmt.Errorf("create execution node run: %w", err)
		}
		return nil
	}); err != nil {
		return execution.NodeRun{}, err
	}
	return nodeRunFromModel(model), nil
}

func validateExecution(record execution.Record) error {
	if record.WorkflowID == "" || record.WorkflowVersionID == "" {
		return fmt.Errorf("execution workflow and workflow version are required")
	}
	if record.Status == "" || record.Trigger == "" {
		return fmt.Errorf("execution status and trigger are required")
	}
	return nil
}

func validateNodeRun(nodeRun execution.NodeRun) error {
	if nodeRun.ExecutionID == "" || nodeRun.NodeID == "" || nodeRun.LeaseOwner == "" {
		return fmt.Errorf("node run execution, node, and lease owner are required")
	}
	if nodeRun.Attempt < 1 || nodeRun.Sequence < 1 {
		return fmt.Errorf("node run attempt and sequence must be positive")
	}
	if nodeRun.Status == "" {
		return fmt.Errorf("node run status is required")
	}
	return nil
}

// payload normalizes a durable JSON column and strips credential material on
// the way in. Redacting here rather than at each call site means every write
// path — manual queue, create, runtime update, node-run trace — is covered by
// construction, so a new caller cannot forget it.
// payload validates and redacts a value on its way into durable storage.
//
// This is where credentials the runtime resolved actually appear — a node's
// input holds the header the HTTP node was handed, a node's output holds what
// came back — so redaction belongs here and the guarantee that a credential is
// never readable in a stored record depends on it.
func payload(value json.RawMessage) ([]byte, error) {
	encoded, err := triggerPayload(value)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), execution.Redact(encoded)...), nil
}

// triggerPayload validates a trigger input and redacts only its headers.
//
// A trigger input is the caller's own data, not something the runtime resolved,
// and the runner rehydrates the trigger item straight back out of the stored
// record — so redacting the whole thing does not protect a credential, it puts
// "[redacted]" on the wire in place of the session the caller sent. The body,
// query, method and path are therefore stored exactly as they arrived.
//
// The header map is the exception, because it is the one part of an inbound
// request that genuinely carries a caller's credential. The webhook handler
// already redacts it at ingest; doing it here as well keeps the repository the
// last line of defence, so a trigger source added later cannot leak headers by
// forgetting.
func triggerPayload(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return execution.RedactTriggerHeaders(value), nil
}

// ChildExecution is a sub-workflow call, as durable storage needs it.
type ChildExecution struct {
	WorkflowID        string
	ParentExecutionID string
	// TriggerNodeID names the sub-workflow trigger the child starts from, so a
	// child carrying a webhook beside its sub-workflow trigger does not also
	// run the webhook.
	TriggerNodeID string
	Input         json.RawMessage
	// SelectTrigger picks the trigger node from the document, once storage has
	// resolved which version the child is pinned to.
	//
	// A function rather than a node type, so this package stays free of node
	// knowledge — the same seam WebhookExtractor uses, and for the same reason.
	SelectTrigger func(workflow.Document) string
	// LeaseOwner and LeaseUntil are the caller's own. A child is created
	// already claimed rather than queued: it runs inline in the parent's
	// goroutine, so a worker picking it up would run it a second time.
	LeaseOwner string
	LeaseUntil time.Time
}

// StartChild creates an execution that is already running and already claimed.
//
// Deliberately not QueueTriggered plus an update. A queued record is visible to
// ClaimNext the instant it commits, so a worker could claim the child between
// the two statements and run it in parallel with the parent's own inline run —
// the same workflow, twice, from one call.
//
// It returns the document as well, so the caller does not need a second read
// that could see a different version than the one the record is pinned to.
func (store *GORMExecutionStore) StartChild(ctx context.Context, tenant TenantScope, call ChildExecution) (execution.Record, workflow.Document, error) {
	if err := tenant.validate(); err != nil {
		return execution.Record{}, workflow.Document{}, err
	}
	if call.WorkflowID == "" || call.ParentExecutionID == "" || call.LeaseOwner == "" {
		return execution.Record{}, workflow.Document{}, fmt.Errorf("a sub-workflow call needs a workflow, a parent execution and a lease owner")
	}
	inputPayload, err := triggerPayload(call.Input)
	if err != nil {
		return execution.Record{}, workflow.Document{}, fmt.Errorf("sub-workflow input: %w", err)
	}
	executionID, err := workflow.NewID("exec")
	if err != nil {
		return execution.Record{}, workflow.Document{}, err
	}
	leaseUntil := call.LeaseUntil.UTC()

	model := executionModel{
		ID: executionID, TenantID: tenant.ID, WorkflowID: call.WorkflowID,
		Status: string(execution.StatusRunning), Trigger: string(execution.TriggerSubworkflow),
		TriggerNodeID: call.TriggerNodeID, ParentExecutionID: call.ParentExecutionID,
		Input: inputPayload, Output: []byte("null"), Error: []byte("null"),
		StartedAt: time.Now().UTC(), LeaseOwner: call.LeaseOwner, LeaseExpiresAt: &leaseUntil,
	}
	var document workflow.Document
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The tenant clause is the isolation boundary: a workflow ID from
		// another tenant simply does not exist here, so a call naming one is
		// refused as "not found" rather than reaching across.
		var parent workflowModel
		if err := tx.Where("tenant_id = ? AND id = ? AND active = ?", tenant.ID, call.WorkflowID, true).First(&parent).Error; err != nil {
			return mapNotFound(err, "active workflow")
		}
		if parent.ActiveVersionID == nil {
			return fmt.Errorf("%w: workflow has no active revision", ErrNotFound)
		}
		var versionModel workflowVersionModel
		if err := tx.Where("tenant_id = ? AND workflow_id = ? AND id = ?", tenant.ID, call.WorkflowID, *parent.ActiveVersionID).
			First(&versionModel).Error; err != nil {
			return mapNotFound(err, "workflow version")
		}
		version, err := versionFromModel(versionModel)
		if err != nil {
			return err
		}
		document = version.Document
		model.WorkflowVersionID = versionModel.ID
		if call.SelectTrigger != nil {
			model.TriggerNodeID = call.SelectTrigger(document)
		}
		return tx.Create(&model).Error
	})
	if err != nil {
		return execution.Record{}, workflow.Document{}, err
	}
	return executionFromModel(model), document, nil
}

// Ancestry returns an execution's chain of parents, nearest first.
//
// Bounded, because the data is a linked list and a corrupted row could make it
// a ring. The bound is generous relative to the call-depth limit the engine
// enforces, so a legitimate chain is never truncated.
func (store *GORMExecutionStore) Ancestry(ctx context.Context, tenant TenantScope, executionID string) ([]execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	chain := make([]execution.Record, 0, 4)
	current := executionID
	for depth := 0; depth < maximumAncestryDepth && current != ""; depth++ {
		var model executionModel
		if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, current).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return chain, nil
			}
			return nil, fmt.Errorf("read execution ancestry: %w", err)
		}
		if model.ParentExecutionID == "" {
			return chain, nil
		}
		var parent executionModel
		if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, model.ParentExecutionID).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return chain, nil
			}
			return nil, fmt.Errorf("read execution ancestry: %w", err)
		}
		chain = append(chain, executionFromModel(parent))
		current = parent.ID
	}
	return chain, nil
}

// maximumAncestryDepth bounds the walk above.
const maximumAncestryDepth = 64

func executionFromModel(model executionModel) execution.Record {
	return execution.Record{
		ID:                      model.ID,
		TenantID:                model.TenantID,
		WorkflowID:              model.WorkflowID,
		WorkflowVersionID:       model.WorkflowVersionID,
		Status:                  execution.Status(model.Status),
		Trigger:                 execution.Trigger(model.Trigger),
		TriggerNodeID:           model.TriggerNodeID,
		ParentExecutionID:       model.ParentExecutionID,
		Input:                   append(json.RawMessage(nil), model.Input...),
		Output:                  append(json.RawMessage(nil), model.Output...),
		Error:                   append(json.RawMessage(nil), model.Error...),
		StartedAt:               model.StartedAt,
		FinishedAt:              model.FinishedAt,
		CancellationRequestedAt: model.CancellationRequestedAt,
		LeaseOwner:              model.LeaseOwner,
	}
}

func nodeRunFromModel(model executionNodeRunModel) execution.NodeRun {
	return execution.NodeRun{
		ID:          model.ID,
		TenantID:    model.TenantID,
		ExecutionID: model.ExecutionID,
		NodeID:      model.NodeID,
		Attempt:     model.Attempt,
		RunIndex:    model.RunIndex,
		Sequence:    model.Sequence,
		Status:      execution.Status(model.Status),
		Input:       append(json.RawMessage(nil), model.Input...),
		Output:      append(json.RawMessage(nil), model.Output...),
		Error:       append(json.RawMessage(nil), model.Error...),
		StartedAt:   model.StartedAt,
		FinishedAt:  model.FinishedAt,
	}
}
