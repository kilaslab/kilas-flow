package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// Parser accepts standard five-field cron in UTC.
//
// Seconds and descriptors are deliberately not enabled: a schedule that can
// fire every second is a foot-gun in a single-instance deployment, and the
// five-field form is what users already know from crontab.
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Clock lets tests advance time deterministically instead of sleeping.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Service runs due schedules in a single process.
//
// V1 assumes one instance, which the SQLite default deployment guarantees. The
// due-claim is still transactional, so a second instance would be safe rather
// than double-firing — it simply is not required yet.
type Service struct {
	schedules repository.ScheduleRepository
	queue     QueueFunc
	clock     Clock
	interval  time.Duration
	logger    *slog.Logger
}

// QueueFunc starts one scheduled execution.
type QueueFunc func(ctx context.Context, tenantID, workflowID, versionID string, payload json.RawMessage) error

// Options configures the scheduler.
type Options struct {
	Schedules repository.ScheduleRepository
	Queue     QueueFunc
	// Clock defaults to the system clock; tests supply a controllable one.
	Clock Clock
	// Interval is how often due schedules are checked.
	Interval time.Duration
	Logger   *slog.Logger
}

// New constructs the scheduler.
func New(options Options) (*Service, error) {
	if options.Schedules == nil || options.Queue == nil {
		return nil, fmt.Errorf("scheduler requires a schedule store and a queue function")
	}
	if options.Clock == nil {
		options.Clock = systemClock{}
	}
	if options.Interval <= 0 {
		options.Interval = 15 * time.Second
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Service{
		schedules: options.Schedules, queue: options.Queue,
		clock: options.Clock, interval: options.Interval, logger: options.Logger,
	}, nil
}

// Next returns the next fire time strictly after `after` for a cron expression.
func Next(expression string, after time.Time) (time.Time, error) {
	schedule, err := parser.Parse(expression)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	return schedule.Next(after.UTC()).UTC(), nil
}

// Validate reports whether a cron expression is usable.
func Validate(expression string) error {
	_, err := parser.Parse(expression)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}

// Tick claims and queues everything due at the current clock time.
//
// It is exported so tests drive it directly with a controllable clock rather
// than waiting on wall-clock sleeps.
func (service *Service) Tick(ctx context.Context) (int, error) {
	due, err := service.schedules.ClaimDue(ctx, service.clock.Now(), Next)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, item := range due {
		if item.WorkflowVersionID == "" {
			service.logger.Warn("scheduled workflow has no active revision", "schedule", item.Schedule.ID)
			continue
		}
		payload, err := json.Marshal(map[string]any{
			"scheduledAt": item.Schedule.LastRunAt,
			"scheduleId":  item.Schedule.ID,
		})
		if err != nil {
			return queued, fmt.Errorf("encode schedule payload: %w", err)
		}
		if err := service.queue(ctx, item.Schedule.TenantID, item.Schedule.WorkflowID, item.WorkflowVersionID, payload); err != nil {
			// The due time was already advanced, so a queue failure loses this
			// occurrence rather than firing it repeatedly. Logging keeps that
			// visible instead of silent.
			service.logger.Error("queue scheduled execution", "schedule", item.Schedule.ID, "error", err)
			continue
		}
		queued++
	}
	return queued, nil
}

// Start runs Tick on an interval until the context is cancelled.
func (service *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(service.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := service.Tick(ctx); err != nil && ctx.Err() == nil {
					service.logger.Error("scheduler tick", "error", err)
				}
			}
		}
	}()
}
