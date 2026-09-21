package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// PollCursor is one leased poll trigger row.
type PollCursor struct {
	ID         string
	TenantID   string
	WorkflowID string
	NodeID     string
	NodeType   string
	Interval   time.Duration
	Cursor     string
	NextPollAt *time.Time
	LeaseUntil *time.Time
	LeaseOwner string
	LastPollAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PollTrigger is one poll node as activation stores it.
type PollTrigger struct {
	NodeID   string
	NodeType string
	Interval time.Duration
}

// PollExtractor reads poll triggers from a document.
type PollExtractor func(workflow.Document) []PollTrigger

// DuePoll is one claimed poll tick.
type DuePoll struct {
	Cursor            PollCursor
	WorkflowVersionID string
}

// PollCursorStore is the persistence seam the poller uses.
type PollCursorStore interface {
	ClaimDue(ctx context.Context, now time.Time, workerID string, lease time.Duration) ([]DuePoll, error)
	Complete(ctx context.Context, id, cursor string, next time.Time, workerID string) error
}

type pollCursorModel struct {
	ID         string `gorm:"primaryKey;size:64"`
	TenantID   string `gorm:"not null;size:64;uniqueIndex:uidx_poll_cursors_node,priority:1"`
	WorkflowID string `gorm:"not null;size:64;uniqueIndex:uidx_poll_cursors_node,priority:2"`
	NodeID     string `gorm:"not null;size:64;uniqueIndex:uidx_poll_cursors_node,priority:3"`
	NodeType   string `gorm:"not null;size:128"`
	IntervalNs int64  `gorm:"not null"`
	Cursor     string `gorm:"not null"`
	NextPollAt *time.Time
	LeaseUntil *time.Time
	LeaseOwner string `gorm:"not null;size:128;default:''"`
	LastPollAt *time.Time
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

func (pollCursorModel) TableName(namer schema.Namer) string {
	return namer.TableName("poll_cursors")
}

// GORMPollCursorStore persists leased poll cursors.
type GORMPollCursorStore struct {
	db *gorm.DB
}

// NewPollCursorStore constructs the poll persistence boundary.
func NewPollCursorStore(db *gorm.DB) *GORMPollCursorStore {
	return &GORMPollCursorStore{db: db}
}

var _ PollCursorStore = (*GORMPollCursorStore)(nil)

// ClaimDue locks one due row whose lease has expired (or was never taken) and
// stamps a new lease. LIMIT 1 + SKIP LOCKED fans replicas onto different
// rows; the UPDATE re-applies the predicate so a lost race is RowsAffected 0
// rather than a double claim. next_poll_at is not advanced here: Complete
// does that after the tick so a crash mid-poll retries the same cursor.
func (store *GORMPollCursorStore) ClaimDue(ctx context.Context, now time.Time, workerID string, lease time.Duration) ([]DuePoll, error) {
	now = now.UTC()
	if lease <= 0 {
		lease = 3 * time.Minute
	}
	until := now.Add(lease)
	claimed := make([]DuePoll, 0, 1)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dueWhere := "next_poll_at IS NOT NULL AND next_poll_at <= ? AND (lease_until IS NULL OR lease_until < ?)"
		for range claimScanLimit {
			var model pollCursorModel
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}).
				Where(dueWhere, now, now).
				Order("next_poll_at ASC, id ASC").
				First(&model).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("find due polls: %w", err)
			}
			var parent workflowModel
			if err := tx.Where("tenant_id = ? AND id = ? AND active = ?", model.TenantID, model.WorkflowID, true).First(&parent).Error; err != nil {
				if err := tx.Where("id = ?", model.ID).Delete(&pollCursorModel{}).Error; err != nil {
					return fmt.Errorf("drop orphaned poll: %w", err)
				}
				continue
			}
			result := tx.Model(&pollCursorModel{}).
				Where("id = ? AND "+dueWhere, model.ID, now, now).
				Updates(map[string]any{"lease_until": until, "lease_owner": workerID, "updated_at": now})
			if result.Error != nil {
				return fmt.Errorf("lease poll: %w", result.Error)
			}
			if result.RowsAffected == 0 {
				continue
			}
			model.LeaseUntil = &until
			model.LeaseOwner = workerID
			versionID := ""
			if parent.ActiveVersionID != nil {
				versionID = *parent.ActiveVersionID
			}
			claimed = append(claimed, DuePoll{Cursor: pollFromModel(model), WorkflowVersionID: versionID})
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// Complete writes the new cursor, clears the lease, and schedules the next tick.
// The UPDATE is gated on lease_owner so an expired worker cannot rewind a
// watermark after another replica has claimed the row.
func (store *GORMPollCursorStore) Complete(ctx context.Context, id, cursor string, next time.Time, workerID string) error {
	if strings.TrimSpace(workerID) == "" {
		return fmt.Errorf("poll complete requires the claiming worker")
	}
	now := time.Now().UTC()
	next = next.UTC()
	result := store.db.WithContext(ctx).Model(&pollCursorModel{}).
		Where("id = ? AND lease_owner = ?", id, workerID).
		Updates(map[string]any{
			"cursor": cursor, "next_poll_at": next, "last_poll_at": now,
			"lease_until": nil, "lease_owner": "", "updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("complete poll: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: poll cursor", ErrNotFound)
	}
	return nil
}

func pollFromModel(model pollCursorModel) PollCursor {
	return PollCursor{
		ID: model.ID, TenantID: model.TenantID, WorkflowID: model.WorkflowID, NodeID: model.NodeID,
		NodeType: model.NodeType, Interval: time.Duration(model.IntervalNs), Cursor: model.Cursor,
		NextPollAt: model.NextPollAt, LeaseUntil: model.LeaseUntil, LeaseOwner: model.LeaseOwner,
		LastPollAt: model.LastPollAt, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}

func syncPolls(tx *gorm.DB, tenantID, workflowID string, triggers []PollTrigger) error {
	now := time.Now().UTC()
	wanted := make(map[string]PollTrigger, len(triggers))
	for _, trigger := range triggers {
		wanted[trigger.NodeID] = trigger
	}
	var existing []pollCursorModel
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).Find(&existing).Error; err != nil {
		return fmt.Errorf("list poll cursors: %w", err)
	}
	kept := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		trigger, keep := wanted[row.NodeID]
		if !keep {
			if err := tx.Where("id = ?", row.ID).Delete(&pollCursorModel{}).Error; err != nil {
				return fmt.Errorf("drop removed poll: %w", err)
			}
			continue
		}
		kept[row.NodeID] = struct{}{}
		interval := trigger.Interval
		if interval <= 0 {
			interval = time.Minute
		}
		if err := tx.Model(&pollCursorModel{}).Where("id = ?", row.ID).Updates(map[string]any{
			"node_type":   trigger.NodeType,
			"interval_ns": int64(interval),
			"updated_at":  now,
		}).Error; err != nil {
			return fmt.Errorf("update poll cursor for node %q: %w", row.NodeID, err)
		}
	}
	for _, trigger := range triggers {
		if _, ok := kept[trigger.NodeID]; ok {
			continue
		}
		interval := trigger.Interval
		if interval <= 0 {
			interval = time.Minute
		}
		id, err := workflow.NewID("poll")
		if err != nil {
			return err
		}
		next := now
		row := pollCursorModel{
			ID: id, TenantID: tenantID, WorkflowID: workflowID, NodeID: trigger.NodeID,
			NodeType: trigger.NodeType, IntervalNs: int64(interval), Cursor: "",
			NextPollAt: &next, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create poll cursor for node %q: %w", trigger.NodeID, err)
		}
	}
	return nil
}

func removePolls(tx *gorm.DB, tenantID, workflowID string) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).
		Delete(&pollCursorModel{}).Error; err != nil {
		return fmt.Errorf("clear poll cursors: %w", err)
	}
	return nil
}
