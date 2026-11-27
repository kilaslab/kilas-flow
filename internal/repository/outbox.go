package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The event types the outbox carries. A tenant subscribes to them by name, so
// the spellings are part of the delivery contract and never change.
const (
	OutboundExecutionCompleted  = "execution.completed"
	OutboundExecutionFailed     = "execution.failed"
	OutboundWorkflowActivated   = "workflow.activated"
	OutboundWorkflowDeactivated = "workflow.deactivated"
)

// The states of one outbound event.
const (
	// OutboundPending is waiting for its next attempt.
	OutboundPending = "pending"
	// OutboundDelivered was acknowledged with a 2xx response.
	OutboundDelivered = "delivered"
	// OutboundDead exhausted its attempts and will not be sent again.
	OutboundDead = "dead"
)

// OutboxRouter decides, inside the transaction that commits a state change,
// whether the tenant wants to hear about it and where the event goes.
//
// It returns the endpoint and true for a tenant subscribed to eventType, and
// false for every other tenant, which then has no row written. settings is
// the tenant's stored settings document (nil when it has none) as the same
// transaction reads it, so the decision matches the committed state and the
// router never needs a read of its own: on SQLite the transaction holds the
// only connection, and a router that read the database itself would wait on
// itself for ever. It must not block.
type OutboxRouter func(tenantID, eventType string, settings []byte) (endpoint string, ok bool)

// OutboundEvent is one outbox row as the dispatcher and the delivery log see
// it.
type OutboundEvent struct {
	ID             string
	TenantID       string
	EventType      string
	SubjectID      string
	Endpoint       string
	Payload        json.RawMessage
	Status         string
	Attempts       int
	NextAttemptAt  *time.Time
	LeaseOwner     string
	LeaseUntil     *time.Time
	LastStatusCode int
	LastError      string
	DeliveredAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type outboundEventModel struct {
	ID             string     `gorm:"primaryKey;size:64"`
	TenantID       string     `gorm:"not null;size:64;index:idx_outbound_events_tenant,priority:1"`
	EventType      string     `gorm:"not null;size:64"`
	SubjectID      string     `gorm:"not null;size:64;default:''"`
	Endpoint       string     `gorm:"not null"`
	Payload        []byte     `gorm:"not null"`
	Status         string     `gorm:"not null;size:16;index:idx_outbound_events_due,priority:1"`
	Attempts       int        `gorm:"not null;default:0"`
	NextAttemptAt  *time.Time `gorm:"index:idx_outbound_events_due,priority:2"`
	LeaseOwner     string     `gorm:"not null;size:128;default:''"`
	LeaseUntil     *time.Time
	LastStatusCode int    `gorm:"not null;default:0"`
	LastError      string `gorm:"not null;default:''"`
	DeliveredAt    *time.Time
	CreatedAt      time.Time `gorm:"not null;index:idx_outbound_events_tenant,priority:2;index:idx_outbound_events_created"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (outboundEventModel) TableName(namer schema.Namer) string {
	return namer.TableName("outbound_events")
}

// outboundEnvelope is the body every delivery carries. The id repeats the
// webhook-id header so a receiver that logs bodies alone can still
// deduplicate.
type outboundEnvelope struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	TenantID  string    `json:"tenantId"`
	CreatedAt time.Time `json:"createdAt"`
	Data      any       `json:"data"`
}

// maxOutboundErrorLength bounds the error text kept on a row. A receiver's
// error page can be large; the log needs its first line, not its body.
const maxOutboundErrorLength = 512

// enqueueOutbound writes one outbox row inside tx when the router subscribes
// the tenant to eventType. A nil router, or one that declines, writes nothing.
func enqueueOutbound(tx *gorm.DB, router OutboxRouter, tenantID, eventType, subjectID string, data any, now time.Time) error {
	if router == nil {
		return nil
	}
	var tenant tenantModel
	if err := tx.Select("id", "settings").Where("id = ?", tenantID).Limit(1).Find(&tenant).Error; err != nil {
		return fmt.Errorf("read tenant settings for outbound routing: %w", err)
	}
	endpoint, ok := router(tenantID, eventType, tenant.Settings)
	if !ok || strings.TrimSpace(endpoint) == "" {
		return nil
	}
	id, err := workflow.NewID("evt")
	if err != nil {
		return err
	}
	now = now.UTC()
	body, err := json.Marshal(outboundEnvelope{ID: id, Type: eventType, TenantID: tenantID, CreatedAt: now, Data: data})
	if err != nil {
		return fmt.Errorf("encode outbound event: %w", err)
	}
	row := outboundEventModel{
		ID: id, TenantID: tenantID, EventType: eventType, SubjectID: subjectID,
		Endpoint: endpoint, Payload: body, Status: OutboundPending, NextAttemptAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&row).Error; err != nil {
		return fmt.Errorf("write outbound event: %w", err)
	}
	return nil
}

// executionEventData is the data of an execution.* event.
type executionEventData struct {
	ExecutionID       string          `json:"executionId"`
	WorkflowID        string          `json:"workflowId"`
	WorkflowVersionID string          `json:"workflowVersionId"`
	Status            string          `json:"status"`
	Trigger           string          `json:"trigger,omitempty"`
	Error             json.RawMessage `json:"error,omitempty"`
	StartedAt         time.Time       `json:"startedAt"`
	FinishedAt        *time.Time      `json:"finishedAt,omitempty"`
}

// enqueueExecutionOutcome writes the execution.completed or execution.failed
// event for an execution the same transaction just settled. Any other status
// (cancelled, still running) has no event.
func enqueueExecutionOutcome(tx *gorm.DB, router OutboxRouter, tenantID, executionID string, now time.Time) error {
	if router == nil {
		return nil
	}
	var model executionModel
	if err := tx.Select("id", "tenant_id", "workflow_id", "workflow_version_id", "status", "trigger", "error", "started_at", "finished_at").
		Where("tenant_id = ? AND id = ?", tenantID, executionID).Take(&model).Error; err != nil {
		return mapNotFound(err, "execution")
	}
	var eventType string
	switch execution.Status(model.Status) {
	case execution.StatusSucceeded:
		eventType = OutboundExecutionCompleted
	case execution.StatusFailed:
		eventType = OutboundExecutionFailed
	default:
		return nil
	}
	data := executionEventData{
		ExecutionID: model.ID, WorkflowID: model.WorkflowID, WorkflowVersionID: model.WorkflowVersionID,
		Status: model.Status, Trigger: model.Trigger, StartedAt: model.StartedAt.UTC(), FinishedAt: model.FinishedAt,
	}
	if eventType == OutboundExecutionFailed && len(model.Error) > 0 && json.Valid(model.Error) && string(model.Error) != "null" {
		data.Error = json.RawMessage(model.Error)
	}
	return enqueueOutbound(tx, router, tenantID, eventType, model.ID, data, now)
}

// workflowEventData is the data of a workflow.* event.
type workflowEventData struct {
	WorkflowID string `json:"workflowId"`
	VersionID  string `json:"versionId,omitempty"`
	Name       string `json:"name,omitempty"`
}

// OutboundFilter narrows one page of a tenant's delivery log.
type OutboundFilter struct {
	// Status keeps only rows in that state; empty keeps every row.
	Status string
	// Limit bounds the page; zero or out-of-range values use the default.
	Limit int
}

// DefaultOutboundPageSize and MaxOutboundPageSize bound a delivery log page.
const (
	DefaultOutboundPageSize = 50
	MaxOutboundPageSize     = 200
)

// OutboxStore is the persistence seam the dispatcher uses.
type OutboxStore interface {
	ClaimDue(ctx context.Context, now time.Time, workerID string, lease time.Duration) (OutboundEvent, bool, error)
	RecordAttempt(ctx context.Context, attempt OutboundAttempt) error
}

// OutboundAttempt is the outcome of one delivery attempt, as the dispatcher
// reports it.
type OutboundAttempt struct {
	ID string
	// LeaseOwner is the claimed event's LeaseOwner: the token of that one
	// claim, not the worker's name, so a stale attempt never passes the fence
	// of a later claim, even one made by a process with the same worker ID.
	LeaseOwner string
	StatusCode int
	Error      string
	// Delivered is set for a 2xx response.
	Delivered bool
	// Dead is set when the attempt was the last one allowed.
	Dead bool
	// NextAttemptAt is when a failed, not-dead row is due again.
	NextAttemptAt time.Time
	At            time.Time
}

// GORMOutboxStore persists the outbox and serves the delivery log.
type GORMOutboxStore struct {
	db *gorm.DB
}

// NewOutboxStore constructs the outbox persistence boundary.
func NewOutboxStore(db *gorm.DB) *GORMOutboxStore {
	return &GORMOutboxStore{db: db}
}

var _ OutboxStore = (*GORMOutboxStore)(nil)

// ClaimDue leases the oldest due pending row: one whose next attempt has come
// and whose lease has expired or was never taken.
//
// It is the poll cursors' claim. SKIP LOCKED fans dispatchers onto different
// rows on PostgreSQL; SQLite drops the locking clause, and on both the UPDATE
// re-applies the whole predicate, so a dispatcher that lost the race reads
// RowsAffected 0 and moves on rather than delivering a row another one holds.
// The attempt count is not advanced here: RecordAttempt does that, so a
// dispatcher that dies mid-delivery leaves the row due again once its lease
// expires, with the attempt it never finished not counted against it.
func (store *GORMOutboxStore) ClaimDue(ctx context.Context, now time.Time, workerID string, lease time.Duration) (OutboundEvent, bool, error) {
	if strings.TrimSpace(workerID) == "" {
		return OutboundEvent{}, false, fmt.Errorf("outbox claim requires a worker ID")
	}
	now = now.UTC()
	if lease <= 0 {
		lease = time.Minute
	}
	until := now.Add(lease)
	leaseID, err := workflow.NewID("lease")
	if err != nil {
		return OutboundEvent{}, false, err
	}
	leaseOwner := workerID + "/" + leaseID
	var claimed outboundEventModel
	found := false
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		due := "status = ? AND next_attempt_at IS NOT NULL AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until < ?)"
		for range claimScanLimit {
			var model outboundEventModel
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}).
				Where(due, OutboundPending, now, now).
				Order("next_attempt_at ASC, id ASC").
				First(&model).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("find due outbound event: %w", err)
			}
			result := tx.Model(&outboundEventModel{}).
				Where("id = ? AND "+due, model.ID, OutboundPending, now, now).
				Updates(map[string]any{"lease_until": until, "lease_owner": leaseOwner, "updated_at": now})
			if result.Error != nil {
				return fmt.Errorf("lease outbound event: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				continue
			}
			model.LeaseUntil = &until
			model.LeaseOwner = leaseOwner
			model.UpdatedAt = now
			claimed, found = model, true
			return nil
		}
		return nil
	})
	if err != nil {
		return OutboundEvent{}, false, err
	}
	if !found {
		return OutboundEvent{}, false, nil
	}
	return outboundFromModel(claimed), true, nil
}

// RecordAttempt writes one attempt's outcome and releases the lease.
//
// The update is fenced on the lease owner and the pending status, so a
// dispatcher whose lease expired while it waited on a slow endpoint cannot
// overwrite the outcome of the dispatcher that took the row over. That case is
// ErrNotFound, which the caller logs: the delivery may have been made twice,
// which at-least-once allows and the event id lets the receiver absorb.
func (store *GORMOutboxStore) RecordAttempt(ctx context.Context, attempt OutboundAttempt) error {
	if attempt.ID == "" || strings.TrimSpace(attempt.LeaseOwner) == "" {
		return fmt.Errorf("outbound attempt requires the event and the lease it was claimed under")
	}
	at := attempt.At.UTC()
	if attempt.At.IsZero() {
		at = time.Now().UTC()
	}
	errText := attempt.Error
	if len(errText) > maxOutboundErrorLength {
		// Cut on a character boundary: PostgreSQL refuses text that is not
		// valid UTF-8, and a refused write would leave the attempt uncounted
		// and the row retried for ever.
		cut := maxOutboundErrorLength
		for cut > 0 && !utf8.RuneStart(errText[cut]) {
			cut--
		}
		errText = errText[:cut]
	}
	errText = strings.ToValidUTF8(errText, "")
	updates := map[string]any{
		"attempts":         gorm.Expr("attempts + 1"),
		"last_status_code": attempt.StatusCode,
		"last_error":       errText,
		"lease_owner":      "",
		"lease_until":      nil,
		"updated_at":       at,
	}
	switch {
	case attempt.Delivered:
		updates["status"] = OutboundDelivered
		updates["delivered_at"] = at
		updates["next_attempt_at"] = nil
	case attempt.Dead:
		updates["status"] = OutboundDead
		updates["next_attempt_at"] = nil
	default:
		next := attempt.NextAttemptAt.UTC()
		updates["next_attempt_at"] = next
	}
	result := store.db.WithContext(ctx).Model(&outboundEventModel{}).
		Where("id = ? AND lease_owner = ? AND status = ?", attempt.ID, attempt.LeaseOwner, OutboundPending).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("record outbound attempt: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: outbound event lease", ErrNotFound)
	}
	return nil
}

// List reads one page of a tenant's delivery log, newest first.
func (store *GORMOutboxStore) List(ctx context.Context, tenant TenantScope, filter OutboundFilter) ([]OutboundEvent, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultOutboundPageSize
	}
	if limit > MaxOutboundPageSize {
		limit = MaxOutboundPageSize
	}
	query := store.db.WithContext(ctx).Where("tenant_id = ?", tenant.ID)
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var models []outboundEventModel
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list outbound events: %w", err)
	}
	events := make([]OutboundEvent, 0, len(models))
	for _, model := range models {
		events = append(events, outboundFromModel(model))
	}
	return events, nil
}

// PruneOutbound deletes every outbox row created before cutoff, in every
// tenant, whatever its state, and reports how many went.
//
// A pending row that old has been undeliverable for the whole retention
// window, so it goes with the delivered and dead ones: retention bounds the
// table, and a row nobody can deliver is the one that would otherwise grow it
// for ever. A row deleted while a dispatcher holds it only makes that
// dispatcher's RecordAttempt miss.
//
// It deletes in batches, each its own short statement, so a first prune of a
// large log (after retention was shortened, say) never holds SQLite's single
// connection long enough to stall the terminal transitions waiting behind it.
func (store *GORMOutboxStore) PruneOutbound(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC()
	var pruned int64
	for ctx.Err() == nil {
		var ids []string
		if err := store.db.WithContext(ctx).Model(&outboundEventModel{}).
			Where("created_at < ?", cutoff).Order("created_at ASC, id ASC").
			Limit(outboundPruneBatch).Pluck("id", &ids).Error; err != nil {
			return pruned, fmt.Errorf("find expired outbound events: %w", err)
		}
		if len(ids) == 0 {
			return pruned, nil
		}
		result := store.db.WithContext(ctx).Where("id IN ? AND created_at < ?", ids, cutoff).Delete(&outboundEventModel{})
		if result.Error != nil {
			return pruned, fmt.Errorf("prune outbound events: %w", result.Error)
		}
		pruned += result.RowsAffected
		if len(ids) < outboundPruneBatch {
			return pruned, nil
		}
	}
	return pruned, ctx.Err()
}

// outboundPruneBatch is how many outbox rows one prune statement deletes.
const outboundPruneBatch = 500

func outboundFromModel(model outboundEventModel) OutboundEvent {
	return OutboundEvent{
		ID: model.ID, TenantID: model.TenantID, EventType: model.EventType, SubjectID: model.SubjectID,
		Endpoint: model.Endpoint, Payload: json.RawMessage(model.Payload), Status: model.Status,
		Attempts: model.Attempts, NextAttemptAt: model.NextAttemptAt, LeaseOwner: model.LeaseOwner,
		LeaseUntil: model.LeaseUntil, LastStatusCode: model.LastStatusCode, LastError: model.LastError,
		DeliveredAt: model.DeliveredAt, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}
