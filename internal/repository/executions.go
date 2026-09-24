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

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ExecutionRepository is the persistence seam for the graph engine. Payloads
// entering this interface are redaction-ready; transport data is never stored
// implicitly by GORM.
type ExecutionRepository interface {
	Create(context.Context, TenantScope, execution.Record) (execution.Record, error)
	QueueManualLatest(context.Context, TenantScope, string, workflow.Catalog, string, json.RawMessage) (execution.Record, error)
	// QueueManualVersion is the same manual run pinned to one named revision
	// instead of the newest one, so a caller can run a revision history has
	// moved past — `run --revision`, and the retry of a finished execution.
	QueueManualVersion(context.Context, TenantScope, string, string, workflow.Catalog, string, json.RawMessage) (execution.Record, error)
	// QueueRetry queues a finished execution again: its workflow, revision,
	// trigger node and input, under the trigger it ran under.
	QueueRetry(context.Context, TenantScope, execution.Record, workflow.Catalog) (execution.Record, error)
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

// MaxExecutionReclaims bounds how many times an expired worker lease may hand
// the same execution to another worker.
//
// Reclaiming is recovery, not a retry policy: a worker that died mid-run leaves
// work nobody else can finish, and its successor clears the partial trace and
// runs the graph. It is bounded because the same mechanism silently becomes a
// loop when the reason the worker died is the execution itself — a trace that
// cannot be persisted (BUG-hfhzq6), a node that exhausts memory, a crash on one
// item. Each pass repeats whatever side effects the graph already performed,
// once per lease period, for ever, and the run never reaches a terminal state.
//
// Past the cap the execution is settled as crashed instead: a failed record an
// operator can see and deliberately re-queue, rather than a queue entry that is
// quietly run again every minute.
const MaxExecutionReclaims = 2

// claimScanLimit bounds how many poisoned rows one claim call settles before it
// returns empty-handed, so a backlog of crashed executions drains a few per
// claim instead of inside one unbounded transaction.
const claimScanLimit = 8

// nodeRunKey is the identity of one trace row: the uniqueness the
// uidx_node_runs_attempt index enforces, in Go.
type nodeRunKey struct {
	nodeID   string
	attempt  int
	runIndex int
}

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
//
// triggerNodeID names the trigger this run starts from, for a workflow that
// declares several. Empty is the documented default: every root runs, which is
// what a manual run of a single-trigger workflow has always meant. A named node
// is checked against the revision being pinned here, so a choice that cannot
// start a run is refused while the caller is still there to read why instead of
// being discovered by a worker as a run that did the wrong thing.
func (store *GORMExecutionStore) QueueManualLatest(ctx context.Context, tenant TenantScope, workflowID string, catalog workflow.Catalog, triggerNodeID string, input json.RawMessage) (execution.Record, error) {
	// The revision is deliberately unnamed: queueManual resolves the workflow's
	// latest one inside its own transaction.
	return store.queueManual(ctx, tenant, workflowID, "", execution.TriggerManual, catalog, triggerNodeID, input)
}

// QueueManualVersion validates one named revision and persists a queued manual
// execution pinned to it.
//
// It is the same run as QueueManualLatest with the revision chosen rather than
// resolved, sharing that call's transaction, catalogue check and start-node
// check: a second body here would be a second place for "may this tenant run
// this graph" to be answered, and the two would drift.
//
// A version that is not this workflow's, or not this tenant's, reads as
// missing rather than as forbidden — the caller learns nothing about a revision
// it did not name.
func (store *GORMExecutionStore) QueueManualVersion(ctx context.Context, tenant TenantScope, workflowID, versionID string, catalog workflow.Catalog, triggerNodeID string, input json.RawMessage) (execution.Record, error) {
	if strings.TrimSpace(versionID) == "" {
		return execution.Record{}, fmt.Errorf("workflow version ID is required")
	}
	return store.queueManual(ctx, tenant, workflowID, versionID, execution.TriggerManual, catalog, triggerNodeID, input)
}

// QueueRetry queues a finished execution again: the revision it ran, from the
// trigger node it started at, with its input, under the trigger it ran
// under. A retry of a webhook run is a webhook run again, not a test from
// the editor, which is what decides whether it keeps the workflow's static
// data; a retry of a manual run is still a manual one, and so is a retry of
// a sub-workflow run, which has no parent to have called it. It shares
// queueManual's transaction and checks, as QueueManualVersion does.
func (store *GORMExecutionStore) QueueRetry(ctx context.Context, tenant TenantScope, original execution.Record, catalog workflow.Catalog) (execution.Record, error) {
	if strings.TrimSpace(original.WorkflowVersionID) == "" {
		return execution.Record{}, fmt.Errorf("workflow version ID is required")
	}
	trigger := original.Trigger
	// A sub-workflow run, an error workflow's included, has a parent by
	// definition, and a retry is started by nobody's node: it runs as a
	// manual run rather than as an orphan sub-workflow run.
	if trigger == "" || trigger == execution.TriggerSubworkflow {
		trigger = execution.TriggerManual
	}
	return store.queueManual(ctx, tenant, original.WorkflowID, original.WorkflowVersionID, trigger, catalog, original.TriggerNodeID, original.Input)
}

// queueManual is the one manual-run body: it holds the workflow row lock, pins
// the revision the run will execute, compiles it under the caller's catalogue
// and persists the queued execution.
//
// Keeping those actions in one transaction prevents a concurrent draft save
// from making the queued execution point at a revision that was already
// superseded when it persisted. An empty versionID means "the workflow's latest
// revision", which is what a manual run means; a named one is only accepted
// when it belongs to this workflow under this tenant.
//
// triggerNodeID names the trigger this run starts from, for a workflow that
// declares several. Empty is the documented default: every root runs, which is
// what a manual run of a single-trigger workflow has always meant. A named node
// is checked against the revision being pinned here, so a choice that cannot
// start a run is refused while the caller is still there to read why instead of
// being discovered by a worker as a run that did the wrong thing.
func (store *GORMExecutionStore) queueManual(ctx context.Context, tenant TenantScope, workflowID, versionID string, trigger execution.Trigger, catalog workflow.Catalog, triggerNodeID string, input json.RawMessage) (execution.Record, error) {
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
		if versionID == "" {
			if err := tx.Where("tenant_id = ? AND workflow_id = ? AND revision = ?", tenant.ID, workflowID, parent.LatestRevision).
				First(&version).Error; err != nil {
				return mapNotFound(err, "workflow version")
			}
		} else {
			if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, versionID).First(&version).Error; err != nil {
				return mapNotFound(err, "workflow version")
			}
			if version.WorkflowID != parent.ID {
				return fmt.Errorf("%w: workflow version", ErrNotFound)
			}
		}
		storedVersion, err := versionFromModel(version)
		if err != nil {
			return err
		}
		ir, err := workflow.Compile(storedVersion.Document, catalog)
		if err != nil {
			return err
		}
		if triggerNodeID != "" {
			if err := manualStartProblem(ir, triggerNodeID); err != nil {
				return err
			}
		}

		model = executionModel{
			ID:                executionID,
			TenantID:          tenant.ID,
			WorkflowID:        parent.ID,
			WorkflowVersionID: version.ID,
			Status:            string(execution.StatusQueued),
			Trigger:           string(trigger),
			TriggerNodeID:     triggerNodeID,
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

// manualStartProblem refuses a manual run that names a node it cannot start
// from.
//
// The runner takes any node ID and runs the subgraph below it, so a node in the
// middle of the graph would be seeded with an empty input and the nodes above
// it would simply not run — a run that reports success and does something the
// caller did not ask for. A disabled trigger is refused for the same reason the
// runner refuses it: the author switched it off.
func manualStartProblem(ir workflow.IR, triggerNodeID string) error {
	invalid := func(message string) error {
		return &workflow.ValidationErrors{Issues: []workflow.ValidationError{{
			Code: workflow.ErrorInvalidTopology, Path: "/triggerNodeId",
			NodeID: triggerNodeID, Message: message,
		}}}
	}
	known := false
	for _, node := range ir.Nodes {
		if node.ID != triggerNodeID {
			continue
		}
		known = true
		if node.Disabled {
			return invalid(fmt.Sprintf("node %q is disabled, so it cannot start a run", triggerNodeID))
		}
		break
	}
	if !known {
		return invalid(fmt.Sprintf("this workflow has no node %q to start the run from", triggerNodeID))
	}
	for _, edge := range ir.Edges {
		if edge.Target.NodeID == triggerNodeID {
			return invalid(fmt.Sprintf("node %q is fed by %q, so a run cannot start from it; name a trigger node instead",
				triggerNodeID, edge.Source.NodeID))
		}
	}
	return nil
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
		// A loop rather than one candidate: a row past its reclaim cap is
		// settled here and then leaves the predicate, so the scan has to move
		// on to the next one. Returning empty-handed instead would leave the
		// crashed row first in the order and every later claim of every worker
		// would settle it again and claim nothing — a busy queue that looks
		// idle.
		for range claimScanLimit {
			var candidate executionModel
			// SKIP LOCKED fans contending workers out over distinct rows instead
			// of racing them all for the oldest one. The glebarez SQLite driver
			// drops clause.Locking silently ("SQLite3 does not support row-level
			// locking"), so on SQLite this is a plain SELECT and correctness
			// rests on the conditional UPDATE below; on PostgreSQL the lock is
			// real. Either way the UPDATE re-applies the whole predicate and a
			// lost race reads as RowsAffected == 0, never as a double claim.
			//
			// A waiting execution is excluded by the status allowlist in both the
			// SELECT and the UPDATE: suspending releases the lease, so without
			// this a suspended run would read as a crashed one and be reclaimed
			// mid-wait.
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
			if reclaimingExpiredLease && candidate.ReclaimCount+1 > MaxExecutionReclaims {
				if err := settleCrashedExecution(tx, candidate, now); err != nil {
					return err
				}
				continue
			}
			nextStatus := execution.StatusRunning
			if candidate.Status == string(execution.StatusCancelling) {
				nextStatus = execution.StatusCancelling
			}
			claim := map[string]any{
				"status":           string(nextStatus),
				"lease_owner":      leaseOwner,
				"lease_expires_at": leaseUntil,
			}
			if reclaimingExpiredLease {
				claim["reclaim_count"] = gorm.Expr("reclaim_count + 1")
			}
			result := tx.Model(&executionModel{}).
				Where("id = ? AND tenant_id = ? AND (status = ? OR ((status = ? OR status = ?) AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
					candidate.ID, candidate.TenantID, string(execution.StatusQueued), string(execution.StatusRunning), string(execution.StatusCancelling), now).
				Updates(claim)
			if result.Error != nil {
				return fmt.Errorf("claim execution: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				// Another worker claimed it between the SELECT and the UPDATE.
				// Its lease is now in the future, so the next candidate is a
				// different row, not this one again.
				continue
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
			if reclaimingExpiredLease {
				candidate.ReclaimCount++
			}

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
		}
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

// settleCrashedExecution ends an execution whose lease expired more times than
// MaxExecutionReclaims allows, without running its graph again.
//
// The partial trace is deliberately kept: it is the only record of what the
// abandoned attempts did, and it is what an operator reads before deciding to
// re-queue. The row is terminal and its lease is released, so it leaves the
// claim predicate for good — the difference between one crashed execution and
// a worker that re-runs it for ever.
func settleCrashedExecution(tx *gorm.DB, candidate executionModel, now time.Time) error {
	reclaims := candidate.ReclaimCount
	message := fmt.Sprintf("execution was abandoned by its worker %d times and is not being run again; re-queue it to try once more", reclaims)
	errorPayload, err := json.Marshal(map[string]string{"code": "execution.crashed", "message": message})
	if err != nil {
		return err
	}
	result := tx.Model(&executionModel{}).
		Where("id = ? AND tenant_id = ? AND status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?",
			candidate.ID, candidate.TenantID, string(execution.StatusRunning), now).
		Updates(map[string]any{
			"status":           string(execution.StatusFailed),
			"output":           []byte("null"),
			"error":            []byte(errorPayload),
			"finished_at":      &now,
			"lease_owner":      "",
			"lease_expires_at": nil,
		})
	if result.Error != nil {
		return fmt.Errorf("settle crashed execution: %w", result.Error)
	}
	return nil
}

// ExtendLease renews the fenced lease of one running execution.
//
// It is the heartbeat under the lease. A lease long enough to cover the longest
// run is a lease that keeps crashed work invisible for that long, and a lease
// as short as the run timeout expires while the worker is still persisting the
// trace it just produced — which is how the same execution came to be run twice
// (BUG-1tj5wy). Renewing the lease while the worker is alive makes the lease
// mean "this worker is still here" rather than "this worker was predicted to
// need this long", and leaves recovery of a genuinely dead worker to the same
// expiry it always used.
//
// The update carries the lease owner and the two live statuses, so it is a
// no-op — false, not an error — for a lease this worker no longer holds: a row
// whose lease was reclaimed, or one already settled. The caller decides what a
// lost fence means; the repository only reports it.
func (store *GORMExecutionStore) ExtendLease(ctx context.Context, tenant TenantScope, executionID, leaseOwner string, leaseUntil time.Time) (bool, error) {
	if err := tenant.validate(); err != nil {
		return false, err
	}
	if executionID == "" || leaseOwner == "" || leaseUntil.IsZero() {
		return false, fmt.Errorf("execution ID, lease owner, and lease expiry are required")
	}
	result := store.db.WithContext(ctx).Model(&executionModel{}).
		Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status IN ?",
			tenant.ID, executionID, leaseOwner, []string{string(execution.StatusRunning), string(execution.StatusCancelling)}).
		Update("lease_expires_at", leaseUntil.UTC())
	if result.Error != nil {
		return false, fmt.Errorf("extend execution lease: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ExecutionState reads the status of one execution, and nothing else.
//
// The poll a running worker keeps to notice cancellation asks one question
// several times a second. Get answers it by loading the record and its whole
// node-run trace, which is a payload read per tick per running execution and
// grows with the trace the run is producing. This is the same question asked as
// a single-column read.
//
// The second return reports whether cancellation has been requested or already
// taken effect, which is the only part of the answer the poll acts on.
func (store *GORMExecutionStore) ExecutionState(ctx context.Context, tenant TenantScope, executionID string) (execution.Status, bool, error) {
	if err := tenant.validate(); err != nil {
		return "", false, err
	}
	if executionID == "" {
		return "", false, fmt.Errorf("execution ID is required")
	}
	var state struct{ Status string }
	if err := store.db.WithContext(ctx).Model(&executionModel{}).
		Select("status").
		Where("tenant_id = ? AND id = ?", tenant.ID, executionID).
		First(&state).Error; err != nil {
		return "", false, mapNotFound(err, "execution")
	}
	status := execution.Status(state.Status)
	return status, status == execution.StatusCancelling || status == execution.StatusCancelled, nil
}

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
	// A waiting execution holds no worker and no lease, so cancellation
	// completes it at once like a queued one: entering cancelling would wait
	// for a worker that does not exist to observe the request.
	case execution.StatusQueued, execution.StatusWaiting:
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
//
// One row is the batch of one: the collision handling that keeps a loop trace
// writable (see CreateNodeRuns) applies to the suspend path and the incremental
// writers too, so a caller cannot append a row that the same run has already
// recorded.
func (store *GORMExecutionStore) CreateNodeRun(ctx context.Context, tenant TenantScope, nodeRun execution.NodeRun) (execution.NodeRun, error) {
	stored, err := store.CreateNodeRuns(ctx, tenant, []execution.NodeRun{nodeRun})
	if err != nil {
		return execution.NodeRun{}, err
	}
	if len(stored) == 0 {
		// Already stored under this exact key and sequence: the caller's own
		// row, as it is in the table, rather than an empty answer.
		return nodeRun, nil
	}
	return stored[0], nil
}

// CreateNodeRuns appends a batch of node attempts to one execution in a single
// transaction, and returns them as stored.
//
// One transaction rather than one per row, because the trace is written after
// the graph has finished: a hundred-iteration loop is a hundred round trips in
// the window where the worker must still hold its lease, and the wall time of
// that window is what used to push a run past its lease and hand it to a second
// worker (BUG-1tj5wy).
//
// The batch is also where the trace's keys are made unique. The runner stamps
// RunIndex on a row that ran, and leaves it 0 on the rows it records for
// another reason — skipped, failed, retried, tolerated by continueOnFail. A
// node inside a loop therefore produces several rows with the same
// (execution, node, attempt, run_index), which uidx_node_runs_attempt refuses,
// and the run never reaches a terminal state: the write fails, the execution
// stays running, and the next lease holder re-runs the graph and hits the same
// collision for ever (BUG-hfhzq6).
//
// So a row whose key is taken is given the next free index instead of being
// refused — the row is real, its node ran (or was pruned) a second time, and
// only its index was missing. A row whose key is taken *and* whose sequence
// matches the stored row is the same row written twice, which is what an
// incremental writer followed by this batch produces; it is skipped rather than
// duplicated, which is what makes this write idempotent.
func (store *GORMExecutionStore) CreateNodeRuns(ctx context.Context, tenant TenantScope, nodeRuns []execution.NodeRun) ([]execution.NodeRun, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	if len(nodeRuns) == 0 {
		return nil, nil
	}
	// One execution and one lease per batch: the fence below is checked once,
	// so a batch naming a second lease owner would smuggle rows past it.
	executionID, leaseOwner := nodeRuns[0].ExecutionID, nodeRuns[0].LeaseOwner
	for _, nodeRun := range nodeRuns {
		if nodeRun.ExecutionID != executionID || nodeRun.LeaseOwner != leaseOwner {
			return nil, fmt.Errorf("a node run batch must belong to one execution and one lease owner")
		}
	}
	models := make([]executionNodeRunModel, 0, len(nodeRuns))
	var created []executionNodeRunModel
	for _, nodeRun := range nodeRuns {
		model, err := nodeRunModel(tenant, nodeRun)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent executionModel
		if err := tx.Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status IN (?, ?)", tenant.ID, executionID, leaseOwner, string(execution.StatusRunning), string(execution.StatusCancelling)).First(&parent).Error; err != nil {
			return mapNotFound(err, "execution")
		}
		taken, err := existingNodeRuns(tx, tenant.ID, executionID)
		if err != nil {
			return err
		}
		// The stored keys are read once for the whole batch. Repairing from the
		// in-memory keys alone would be enough for a fresh trace and wrong for a
		// resumed one, whose first segment is already in the table.
		created = make([]executionNodeRunModel, 0, len(models))
		for index := range models {
			key := nodeRunKey{models[index].NodeID, models[index].Attempt, models[index].RunIndex}
			// Walk the key forward until it is either free — the missing index
			// this row needs — or turns out to be this very row, already
			// stored under a later index because an earlier call repaired it.
			alreadyStored := false
			for {
				storedSequence, collides := taken[key]
				if !collides {
					break
				}
				if storedSequence == models[index].Sequence {
					alreadyStored = true
					break
				}
				models[index].RunIndex++
				key = nodeRunKey{models[index].NodeID, models[index].Attempt, models[index].RunIndex}
			}
			if alreadyStored {
				continue
			}
			taken[key] = models[index].Sequence
			created = append(created, models[index])
		}
		if len(created) == 0 {
			return nil
		}
		if err := tx.Create(&created).Error; err != nil {
			return fmt.Errorf("create execution node runs: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Only the rows this call created, in the caller's sequence order, each
	// carrying the run index it was stored under. A row that was already
	// stored under the same key and sequence is not returned: it is not this
	// call's row to announce, which is what lets an incremental writer publish
	// the rows it persisted and the flush publish only the ones it added
	// itself.
	stored := make([]execution.NodeRun, 0, len(created))
	for index := range created {
		stored = append(stored, nodeRunFromModel(created[index]))
	}
	return stored, nil
}

// existingNodeRuns reads the identity of every trace row already stored for one
// execution: node, attempt, run index and the sequence that distinguishes a row
// from a duplicate of itself.
func existingNodeRuns(tx *gorm.DB, tenantID, executionID string) (map[nodeRunKey]int, error) {
	var rows []executionNodeRunModel
	if err := tx.Model(&executionNodeRunModel{}).
		Select("node_id", "attempt", "run_index", "sequence").
		Where("tenant_id = ? AND execution_id = ?", tenantID, executionID).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read execution trace keys: %w", err)
	}
	taken := make(map[nodeRunKey]int, len(rows))
	for _, row := range rows {
		taken[nodeRunKey{row.NodeID, row.Attempt, row.RunIndex}] = row.Sequence
	}
	return taken, nil
}

// nodeRunModel validates one node run on its way to durable storage, mints the
// identifier and start time a caller left unset, and encodes the three payload
// columns through the redacting payload path.
func nodeRunModel(tenant TenantScope, nodeRun execution.NodeRun) (executionNodeRunModel, error) {
	if err := validateNodeRun(nodeRun); err != nil {
		return executionNodeRunModel{}, err
	}
	if nodeRun.ID == "" {
		id, err := workflow.NewID("run")
		if err != nil {
			return executionNodeRunModel{}, err
		}
		nodeRun.ID = id
	}
	if nodeRun.StartedAt.IsZero() {
		nodeRun.StartedAt = time.Now().UTC()
	}
	input, err := payload(nodeRun.Input)
	if err != nil {
		return executionNodeRunModel{}, fmt.Errorf("node run input: %w", err)
	}
	output, err := payload(nodeRun.Output)
	if err != nil {
		return executionNodeRunModel{}, fmt.Errorf("node run output: %w", err)
	}
	errorPayload, err := payload(nodeRun.Error)
	if err != nil {
		return executionNodeRunModel{}, fmt.Errorf("node run error: %w", err)
	}
	// A node that answered nobody stores NULL rather than an empty body: the
	// two are different answers, and only one of them means "this node replied
	// to the caller".
	var response []byte
	if len(nodeRun.Response) > 0 {
		response, err = payload(nodeRun.Response)
		if err != nil {
			return executionNodeRunModel{}, fmt.Errorf("node run response: %w", err)
		}
	}
	// Console output is stored the same way: NULL for a node that printed
	// nothing, never an empty console it did not print.
	var console []byte
	if len(nodeRun.Console) > 0 {
		console, err = payload(nodeRun.Console)
		if err != nil {
			return executionNodeRunModel{}, fmt.Errorf("node run console: %w", err)
		}
	}
	return executionNodeRunModel{
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
		Response:    response,
		Console:     console,
		StartedAt:   nodeRun.StartedAt,
		FinishedAt:  nodeRun.FinishedAt,
	}, nil
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

// triggerPayload validates a trigger input on its way into durable storage.
//
// The value is stored exactly as it arrived, headers included. A trigger input
// is the caller's own data and the stored record IS the input the run executes
// on — the runner rehydrates it with inputItem(record.Input) — so redacting on
// write does not protect storage, it changes what the tenant's workflow sees:
// an imported workflow checking its own `headers['x-api-key']`, or reading a
// cookie, then takes the rejection branch on every call (BUG-cq4yk3, ruled).
//
// Protection lives at the read surfaces instead. Every path that serves a
// record back — the API boundary and the live feed — passes it through
// execution.Redact, which normalises header names and redacts credential keys,
// and the node-run trace goes through payload(), which redacts whole values.
// What storage holds is what the workflow runs on; what a reader, a dump or a
// support export is served is redacted by those surfaces. A database dump does
// carry the caller's headers now, which is why the operator note about
// protecting backups exists beside this.
func triggerPayload(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return value, nil
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
		Response:    append(json.RawMessage(nil), model.Response...),
		Console:     append(json.RawMessage(nil), model.Console...),
		StartedAt:   model.StartedAt,
		FinishedAt:  model.FinishedAt,
	}
}
