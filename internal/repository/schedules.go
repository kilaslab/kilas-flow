package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Schedule is one cron schedule bound to a workflow.
type Schedule struct {
	ID         string
	TenantID   string
	WorkflowID string
	NodeID     string
	Cron       string
	Active     bool
	LastRunAt  *time.Time
	NextRunAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DueSchedule pairs a due schedule with the workflow revision it must run.
type DueSchedule struct {
	Schedule          Schedule
	WorkflowVersionID string
}

// ScheduleRepository persists cron schedules and hands out due work.
type ScheduleRepository interface {
	Create(context.Context, TenantScope, Schedule) (Schedule, error)
	Update(context.Context, TenantScope, string, Schedule) (Schedule, error)
	List(context.Context, TenantScope) ([]Schedule, error)
	Delete(context.Context, TenantScope, string) error
	// ClaimDue atomically claims schedules due at or before now and advances
	// their next run, so one due time produces exactly one execution.
	ClaimDue(ctx context.Context, now time.Time, next func(cron string, after time.Time) (time.Time, error)) ([]DueSchedule, error)
}

// GORMScheduleStore is the GORM implementation of ScheduleRepository.
type GORMScheduleStore struct {
	db *gorm.DB
}

var _ ScheduleRepository = (*GORMScheduleStore)(nil)

// NewScheduleStore constructs the schedule persistence boundary.
func NewScheduleStore(db *gorm.DB) *GORMScheduleStore {
	return &GORMScheduleStore{db: db}
}

// Create stores a schedule and computes its first due time.
func (store *GORMScheduleStore) Create(ctx context.Context, tenant TenantScope, schedule Schedule) (Schedule, error) {
	if err := tenant.validate(); err != nil {
		return Schedule{}, err
	}
	if schedule.WorkflowID == "" || strings.TrimSpace(schedule.Cron) == "" {
		return Schedule{}, fmt.Errorf("schedule workflow and cron expression are required")
	}
	id, err := workflow.NewID("sched")
	if err != nil {
		return Schedule{}, err
	}
	now := time.Now().UTC()
	model := scheduleModel{
		ID: id, TenantID: tenant.ID, WorkflowID: schedule.WorkflowID, NodeID: schedule.NodeID,
		Cron: strings.TrimSpace(schedule.Cron), Active: schedule.Active,
		NextRunAt: schedule.NextRunAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&model).Error; err != nil {
		return Schedule{}, fmt.Errorf("create schedule: %w", err)
	}
	return scheduleFromModel(model), nil
}

// Update replaces a schedule's cron, activation, and next due time.
func (store *GORMScheduleStore) Update(ctx context.Context, tenant TenantScope, scheduleID string, schedule Schedule) (Schedule, error) {
	if err := tenant.validate(); err != nil {
		return Schedule{}, err
	}
	var model scheduleModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, scheduleID).First(&model).Error; err != nil {
		return Schedule{}, mapNotFound(err, "schedule")
	}
	if strings.TrimSpace(schedule.Cron) != "" {
		model.Cron = strings.TrimSpace(schedule.Cron)
	}
	model.Active = schedule.Active
	model.NextRunAt = schedule.NextRunAt
	model.UpdatedAt = time.Now().UTC()
	if err := store.db.WithContext(ctx).Save(&model).Error; err != nil {
		return Schedule{}, fmt.Errorf("update schedule: %w", err)
	}
	return scheduleFromModel(model), nil
}

// List returns every schedule in the tenant.
func (store *GORMScheduleStore) List(ctx context.Context, tenant TenantScope) ([]Schedule, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []scheduleModel
	if err := store.db.WithContext(ctx).Where("tenant_id = ?", tenant.ID).Order("created_at ASC, id ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	schedules := make([]Schedule, 0, len(models))
	for _, model := range models {
		schedules = append(schedules, scheduleFromModel(model))
	}
	return schedules, nil
}

// Delete removes a schedule.
func (store *GORMScheduleStore) Delete(ctx context.Context, tenant TenantScope, scheduleID string) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	result := store.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenant.ID, scheduleID).Delete(&scheduleModel{})
	if result.Error != nil {
		return fmt.Errorf("delete schedule: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: schedule", ErrNotFound)
	}
	return nil
}

// ClaimDue advances every due schedule and returns what to run.
//
// The read, the due check, and the advance all happen inside one transaction
// holding a row lock, so a due time fires exactly once even if two ticks
// overlap. The next run is computed from the due time rather than from now, so
// a slow tick cannot make a schedule drift later and later.
func (store *GORMScheduleStore) ClaimDue(ctx context.Context, now time.Time, next func(string, time.Time) (time.Time, error)) ([]DueSchedule, error) {
	if next == nil {
		return nil, fmt.Errorf("schedule next-run function is required")
	}
	now = now.UTC()
	claimed := make([]DueSchedule, 0, 4)

	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var models []scheduleModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("active = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).
			Order("next_run_at ASC, id ASC").Find(&models).Error; err != nil {
			return fmt.Errorf("find due schedules: %w", err)
		}
		for _, model := range models {
			var parent workflowModel
			if err := tx.Where("tenant_id = ? AND id = ? AND active = ?", model.TenantID, model.WorkflowID, true).First(&parent).Error; err != nil {
				// The workflow was deactivated or deleted. Deactivate the
				// schedule rather than retrying a run that can never succeed.
				if err := tx.Model(&scheduleModel{}).Where("id = ?", model.ID).
					Updates(map[string]any{"active": false, "next_run_at": nil, "updated_at": now}).Error; err != nil {
					return fmt.Errorf("deactivate orphaned schedule: %w", err)
				}
				continue
			}
			dueAt := *model.NextRunAt
			following, err := next(model.Cron, dueAt)
			if err != nil {
				return fmt.Errorf("schedule %q cron: %w", model.ID, err)
			}
			if err := tx.Model(&scheduleModel{}).Where("id = ?", model.ID).
				Updates(map[string]any{"last_run_at": dueAt, "next_run_at": following, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("advance schedule: %w", err)
			}
			model.LastRunAt = &dueAt
			model.NextRunAt = &following
			versionID := ""
			if parent.ActiveVersionID != nil {
				versionID = *parent.ActiveVersionID
			}
			claimed = append(claimed, DueSchedule{Schedule: scheduleFromModel(model), WorkflowVersionID: versionID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func scheduleFromModel(model scheduleModel) Schedule {
	return Schedule{
		ID: model.ID, TenantID: model.TenantID, WorkflowID: model.WorkflowID, NodeID: model.NodeID,
		Cron: model.Cron, Active: model.Active, LastRunAt: model.LastRunAt, NextRunAt: model.NextRunAt,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}
