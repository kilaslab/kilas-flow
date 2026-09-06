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
	// IntervalIndex names which of the trigger node's intervals this row is.
	IntervalIndex int
	Cron          string
	// Timezone is the IANA zone the cron is read in. Empty means UTC.
	Timezone  string
	Active    bool
	LastRunAt *time.Time
	NextRunAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ScheduleTrigger is one interval of one Schedule Trigger node, as an
// activation needs to store it.
//
// One of these per interval rather than one per node: a rule holding "every
// weekday at 09:00 and again at 17:00" is two rows, which keeps ClaimDue's
// transactional claim exactly as it was — one row, one NextRunAt, one advance.
type ScheduleTrigger struct {
	NodeID        string
	IntervalIndex int
	Cron          string
	// Timezone is the IANA zone the cron is read in. Empty means UTC.
	Timezone string
}

// ScheduleExtractor pulls the schedule triggers out of a document.
//
// Injected for the same reason WebhookExtractor is: the repository stays free
// of node-type knowledge, while the rows still land inside the activation
// transaction, so there is never a moment where a workflow is active and its
// schedule does not exist.
type ScheduleExtractor func(workflow.Document) []ScheduleTrigger

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
		IntervalIndex: schedule.IntervalIndex,
		Cron:          strings.TrimSpace(schedule.Cron), Timezone: strings.TrimSpace(schedule.Timezone),
		Active: schedule.Active, NextRunAt: schedule.NextRunAt, CreatedAt: now, UpdatedAt: now,
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
	model.Timezone = strings.TrimSpace(schedule.Timezone)
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

// maximumCatchUpSteps bounds how far ClaimDue will skip forward over
// occurrences that are already past.
//
// A bound rather than a loop to completion: a one-second schedule and a week of
// downtime is six hundred thousand steps, and a scheduler tick that takes a
// minute to compute is worse than one that catches up over a few ticks.
const maximumCatchUpSteps = 512

// ClaimDue advances every due schedule and returns what to run.
//
// The read, the due check, and the advance all happen inside one transaction
// holding a row lock, so a due time fires exactly once even if two ticks
// overlap. The next run is computed from the due time rather than from now, so
// a slow tick cannot make a schedule drift later and later.
//
// The row lock reaches PostgreSQL as FOR UPDATE and never reaches SQLite at
// all: the glebarez driver drops clause.Locking silently, so there this is a
// plain SELECT and overlapping ticks are serialized by the single-writer
// SQLite pool database.Open configures instead.
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
			// The zone is applied here rather than stored in the expression,
			// so `0 9 * * *` still reads back as the user wrote it while
			// meaning nine in the morning where they are.
			spec := model.Cron
			if model.Timezone != "" {
				spec = "TZ=" + model.Timezone + " " + spec
			}
			following, err := next(spec, dueAt)
			if err != nil {
				return fmt.Errorf("schedule %q cron: %w", model.ID, err)
			}
			// Skip past anything already in the past. A schedule finer than
			// the scheduler's tick, or one whose process was down for a day,
			// would otherwise accumulate a backlog and spend hours firing
			// stale occurrences — which is never what "every five minutes"
			// meant. One run now, then the ordinary cadence.
			for guard := 0; guard < maximumCatchUpSteps && !following.After(now); guard++ {
				skipped, err := next(spec, following)
				if err != nil {
					return fmt.Errorf("schedule %q cron: %w", model.ID, err)
				}
				following = skipped
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

// syncSchedules makes a workflow's schedule rows match its document.
//
// Rows are replaced rather than reconciled in place. A schedule's identity is
// its node and interval index, and both can move when a rule is edited — an
// interval deleted from the middle renumbers everything after it — so matching
// old rows to new ones would be guesswork. What is deliberately preserved is
// nothing: a re-activated schedule starts from its next occurrence, which is
// the same answer a user gets from any cron daemon after a restart.
//
// A schedule created by hand through the API for a node this document does not
// declare is left alone, because it was never this document's to own.
func syncSchedules(tx *gorm.DB, tenantID, workflowID string, triggers []ScheduleTrigger, next func(string, time.Time) (time.Time, error)) error {
	if err := removeSchedules(tx, tenantID, workflowID, triggers); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, trigger := range triggers {
		if strings.TrimSpace(trigger.Cron) == "" {
			continue
		}
		spec := trigger.Cron
		if trigger.Timezone != "" {
			spec = "TZ=" + trigger.Timezone + " " + spec
		}
		firstRun, err := next(spec, now)
		if err != nil {
			return fmt.Errorf("schedule for node %q: %w", trigger.NodeID, err)
		}
		id, err := workflow.NewID("sched")
		if err != nil {
			return err
		}
		row := scheduleModel{
			ID: id, TenantID: tenantID, WorkflowID: workflowID, NodeID: trigger.NodeID,
			IntervalIndex: trigger.IntervalIndex, Cron: trigger.Cron, Timezone: trigger.Timezone,
			Active: true, NextRunAt: &firstRun, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("create schedule for node %q: %w", trigger.NodeID, err)
		}
	}
	return nil
}

// removeSchedules drops the rows this document owns.
//
// "Owns" means a row whose node is one of the document's schedule triggers, or
// — when the document has none — every row for the workflow. A row for some
// other node came from the schedules API and is not activation's to delete.
func removeSchedules(tx *gorm.DB, tenantID, workflowID string, triggers []ScheduleTrigger) error {
	query := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID)
	if len(triggers) > 0 {
		nodeIDs := make([]string, 0, len(triggers))
		for _, trigger := range triggers {
			nodeIDs = append(nodeIDs, trigger.NodeID)
		}
		query = query.Where("node_id IN ?", nodeIDs)
	}
	if err := query.Delete(&scheduleModel{}).Error; err != nil {
		return fmt.Errorf("clear schedules: %w", err)
	}
	return nil
}

func scheduleFromModel(model scheduleModel) Schedule {
	return Schedule{
		ID: model.ID, TenantID: model.TenantID, WorkflowID: model.WorkflowID, NodeID: model.NodeID,
		IntervalIndex: model.IntervalIndex, Cron: model.Cron, Timezone: model.Timezone,
		Active: model.Active, LastRunAt: model.LastRunAt, NextRunAt: model.NextRunAt,
		CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt,
	}
}
