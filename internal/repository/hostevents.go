package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// HostEventTrigger is one host-event subscription found in a document: a
// trigger node, and the host event types it listens for.
//
// The repository stays free of node-type knowledge, so the caller that owns
// the node definitions supplies these, exactly as it does for webhook
// bindings and schedules.
type HostEventTrigger struct {
	NodeID   string
	NodeType string
	// Events are the host event types this node subscribes to.
	Events []string
	// EventPath is the dotted path in the trigger item naming the event, for
	// a trigger that routes an item to the port matching its event. Empty
	// means the trigger emits every event from its one output.
	EventPath string
}

// HostEventExtractor pulls the host-event subscriptions out of a document.
type HostEventExtractor func(workflow.Document) []HostEventTrigger

// HostEventTarget is one place a published event reaches: the workflow (and
// pinned version) whose active document subscribes, and the trigger node the
// run starts from.
type HostEventTarget struct {
	WorkflowID        string
	WorkflowVersionID string
	NodeID            string
	NodeType          string
	EventPath         string
}

// WithHostEvents returns a store that keeps host-event subscriptions in step
// with activation. Without an extractor the store still works; nothing
// becomes reachable through the events API, which is the correct behaviour
// for a build with no host-event triggers.
func (store *GORMWorkflowStore) WithHostEvents(extract HostEventExtractor) *GORMWorkflowStore {
	store.hostEvents = extract
	return store
}

// HostEventSubscriptions lists every active subscription to one event type,
// ordered by workflow and node so a fan-out is deterministic.
//
// Rows exist only while the workflow is active — the same contract webhook
// bindings hold — so this list is the set of workflows an event of this type
// reaches. Activation state is re-checked when each execution is queued, so a
// workflow deactivated between this listing and the queue is reported rather
// than run.
func (store *GORMWorkflowStore) HostEventSubscriptions(ctx context.Context, tenant TenantScope, eventType string) ([]HostEventTarget, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	if eventType == "" {
		return nil, fmt.Errorf("event type is required")
	}
	var models []hostEventSubscriptionModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND event_type = ?", tenant.ID, eventType).
		Order("workflow_id ASC, node_id ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list host event subscriptions: %w", err)
	}
	targets := make([]HostEventTarget, 0, len(models))
	for _, model := range models {
		targets = append(targets, HostEventTarget{
			WorkflowID:        model.WorkflowID,
			WorkflowVersionID: model.WorkflowVersionID,
			NodeID:            model.NodeID,
			NodeType:          model.NodeType,
			EventPath:         model.EventPath,
		})
	}
	return targets, nil
}

// syncHostEventSubscriptions replaces a workflow's subscriptions inside the
// caller's transaction.
//
// One row per (node, event type), so a trigger listening for three types
// holds three rows and the publish query stays a plain equality on
// event_type. Like binding sync it runs in the activation's own commit:
// there is never a window where a workflow is active but unreachable by
// event, or reachable while inactive.
func syncHostEventSubscriptions(tx *gorm.DB, tenantID, workflowID, versionID string, triggers []HostEventTrigger) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).
		Delete(&hostEventSubscriptionModel{}).Error; err != nil {
		return fmt.Errorf("clear host event subscriptions: %w", err)
	}
	now := time.Now().UTC()
	for _, trigger := range triggers {
		seen := make(map[string]bool, len(trigger.Events))
		for _, eventType := range trigger.Events {
			if seen[eventType] {
				continue
			}
			seen[eventType] = true
			subscription := hostEventSubscriptionModel{
				TenantID: tenantID, WorkflowID: workflowID, WorkflowVersionID: versionID,
				NodeID: trigger.NodeID, NodeType: trigger.NodeType,
				EventType: eventType, EventPath: trigger.EventPath, CreatedAt: now,
			}
			if err := tx.Create(&subscription).Error; err != nil {
				return fmt.Errorf("record host event subscription for node %q: %w", trigger.NodeID, err)
			}
		}
	}
	return nil
}

// removeHostEventSubscriptions drops every subscription a workflow owns.
func removeHostEventSubscriptions(tx *gorm.DB, tenantID, workflowID string) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).
		Delete(&hostEventSubscriptionModel{}).Error; err != nil {
		return fmt.Errorf("remove host event subscriptions: %w", err)
	}
	return nil
}

// ClaimEventDelivery records that one event id has reached one workflow and
// reports whether this caller was first, so a retried publish does not run
// the workflow twice.
//
// The same insert-fails-on-conflict shape as ClaimDelivery, with the dedupe
// key the events API promises: (event id, workflow), scoped by the tenant
// that published. A claim that loses returns the execution the winner queued.
func (store *GORMWorkflowStore) ClaimEventDelivery(ctx context.Context, tenantID, eventID, workflowID, executionID string, window time.Duration) (string, bool, error) {
	if eventID == "" || workflowID == "" {
		// Nothing to dedupe on. An event with no id is never collapsed with
		// another, which is the documented meaning of publishing without one.
		return "", true, nil
	}
	if tenantID == "" {
		return "", false, ErrTenantRequired
	}
	if window <= 0 {
		window = DefaultDeliveryWindow
	}
	now := time.Now().UTC()

	// An expired claim is not a duplicate. Clearing it first keeps the unique
	// index from rejecting a legitimate later event that reuses the id.
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND event_id = ? AND workflow_id = ? AND expires_at < ?", tenantID, eventID, workflowID, now).
		Delete(&hostEventDeliveryModel{}).Error; err != nil {
		return "", false, fmt.Errorf("clear expired event delivery: %w", err)
	}

	claim := hostEventDeliveryModel{
		TenantID: tenantID,
		EventID:  eventID, WorkflowID: workflowID, ExecutionID: executionID,
		ExpiresAt: now.Add(window), CreatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&claim).Error; err == nil {
		return executionID, true, nil
	}

	var existing hostEventDeliveryModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND event_id = ? AND workflow_id = ?", tenantID, eventID, workflowID).
		First(&existing).Error; err != nil {
		return "", false, fmt.Errorf("read existing event delivery: %w", err)
	}
	return existing.ExecutionID, false, nil
}

// RecordEventExecution attaches the queued execution to a claim taken before
// that execution existed, so a concurrent duplicate can be answered with the
// run it dedupes against rather than an empty id.
func (store *GORMWorkflowStore) RecordEventExecution(ctx context.Context, tenantID, eventID, workflowID, executionID string) error {
	if eventID == "" || workflowID == "" || executionID == "" {
		return nil
	}
	if tenantID == "" {
		return ErrTenantRequired
	}
	return store.db.WithContext(ctx).Model(&hostEventDeliveryModel{}).
		Where("tenant_id = ? AND event_id = ? AND workflow_id = ?", tenantID, eventID, workflowID).
		Update("execution_id", executionID).Error
}

// hostEventSubscriptionModel is one active workflow's subscription to one
// host event type, for one trigger node.
//
// Rows exist only while the workflow is active, like webhook bindings, so the
// publish query never has to re-check activation — and the queue step that
// follows it re-checks anyway, because a deactivation can land between the
// two.
type hostEventSubscriptionModel struct {
	ID                uint   `gorm:"primaryKey;autoIncrement"`
	TenantID          string `gorm:"not null;size:64;uniqueIndex:uidx_host_event_subscriptions,priority:1;index:idx_host_event_subscriptions_event,priority:1"`
	WorkflowID        string `gorm:"not null;size:64;uniqueIndex:uidx_host_event_subscriptions,priority:2"`
	WorkflowVersionID string `gorm:"not null;size:64"`
	NodeID            string `gorm:"not null;size:64;uniqueIndex:uidx_host_event_subscriptions,priority:3"`
	// NodeType records which trigger family subscribed, so a listing can say
	// so without recompiling the document.
	NodeType string `gorm:"not null;size:128;default:''"`
	// EventType is the published event type, exactly as the host names it.
	EventType string `gorm:"not null;size:255;uniqueIndex:uidx_host_event_subscriptions,priority:4"`
	// EventPath is where the event's type sits in the trigger item, for a
	// trigger that routes per event. Empty for a single-output trigger.
	EventPath string    `gorm:"not null;size:128;default:''"`
	CreatedAt time.Time `gorm:"not null"`
}

func (hostEventSubscriptionModel) TableName(namer schema.Namer) string {
	return namer.TableName("host_event_subscriptions")
}

// hostEventDeliveryModel records one logical event delivery to one workflow,
// so a retried publish does not run it twice.
//
// The unique index is (tenant, event id, workflow): the dedupe the events API
// promises is per (eventId, workflow), and the tenant is part of the key so a
// claim can never be reached — or purged — across tenants.
type hostEventDeliveryModel struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	TenantID    string    `gorm:"not null;size:64;uniqueIndex:uidx_host_event_deliveries,priority:1;index:idx_host_event_deliveries_tenant"`
	EventID     string    `gorm:"not null;size:128;uniqueIndex:uidx_host_event_deliveries,priority:2"`
	WorkflowID  string    `gorm:"not null;size:64;uniqueIndex:uidx_host_event_deliveries,priority:3"`
	ExecutionID string    `gorm:"not null;size:64"`
	ExpiresAt   time.Time `gorm:"not null;index"`
	CreatedAt   time.Time `gorm:"not null"`
}

func (hostEventDeliveryModel) TableName(namer schema.Namer) string {
	return namer.TableName("host_event_deliveries")
}
