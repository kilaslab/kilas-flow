package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// parser accepts standard five-field cron, with an optional leading seconds
// field and an optional TZ= prefix.
//
// Seconds are optional rather than required so every schedule written before
// this still parses unchanged. They are allowed at all because the trigger's
// seconds interval is a real n8n option, and mapping "every 30 seconds" to a
// minute would be a silent lie about when the workflow runs. What bounds it in
// practice is the tick: see TickResolution.
//
// Descriptors (@hourly and friends) stay off. They are a second vocabulary for
// things the five fields already say, and one of them — @reboot — has no
// meaning at all for a stored schedule.
var parser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ErrNeverFires reports a cron expression with no future occurrence, such as
// "0 0 31 2 *" (February 31st never exists). It is repository.ErrNeverFires
// under this name because ClaimDue, which cannot import this package without
// a cycle, needs to recognise it too.
var ErrNeverFires = repository.ErrNeverFires

// TickResolution is how often due schedules are looked for by default.
//
// A schedule finer than this cannot fire more often than this. Rather than
// letting such a schedule build a backlog it can never work through, ClaimDue's
// next-run calculation skips past occurrences already in the past: an every-5
// -seconds trigger on a 15-second tick fires once per tick at the current time,
// not three stale times.
const TickResolution = 15 * time.Second

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
//
// triggerNodeID names the schedule trigger node the run starts from. A workflow
// may declare several triggers and only this one is firing, so a webhook beside
// it must not also run.
type QueueFunc func(ctx context.Context, tenantID, workflowID, versionID, triggerNodeID string, payload json.RawMessage) error

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
//
// The expression may carry a `TZ=Area/City ` prefix, which is how a schedule
// carries its zone without this function needing a second argument — and how
// `0 9 * * *` means nine in the morning where the user is rather than nine UTC.
//
// A schedule pinned to a particular hour fires once per occurrence of that
// wall-clock time, including the night the clocks go back. Without the guard
// below it would fire twice that night: 01:30 happens twice, both are after the
// previous run, and both match the expression. A schedule that is *not* pinned
// to an hour — every five minutes, every thirty seconds — is left alone,
// because there the repeated hour is simply an extra hour of running and
// skipping an occurrence would be the bug.
func Next(expression string, after time.Time) (time.Time, error) {
	schedule, err := parser.Parse(expression)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	candidate := schedule.Next(after)
	if pinnedToAnHour(expression) {
		// The comparison has to happen in the schedule's own zone. Both
		// instants are UTC by the time they reach here, and in UTC the
		// repeated hour is two perfectly ordinary times an hour apart — the
		// collision only exists on a clock in the zone the schedule was
		// written for.
		zone := zoneOf(expression)
		// At most a handful: the repeat is one DST shift wide, so one skip
		// always suffices. The bound is there so a pathological expression
		// cannot spin.
		for attempt := 0; attempt < 4 && sameWallClock(after, candidate, zone); attempt++ {
			candidate = schedule.Next(candidate)
		}
	}
	// robfig/cron searches five years ahead before giving up, and answers
	// with the zero time rather than an error. Treating that zero as a real
	// due time is what made an impossible cron — a calendar date that never
	// exists — fire on every scheduler tick instead of being refused
	// (BUG-g7ffj1).
	if candidate.IsZero() {
		return time.Time{}, ErrNeverFires
	}
	return candidate.UTC(), nil
}

// sameWallClock reports two instants that read identically on a clock in the
// schedule's own zone. That is exactly the repeated hour and nothing else.
func sameWallClock(left, right time.Time, zone *time.Location) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	const wallClock = "2006-01-02 15:04:05"
	return left.In(zone).Format(wallClock) == right.In(zone).Format(wallClock)
}

// zoneOf reads the zone a spec's TZ= prefix names, defaulting to UTC.
func zoneOf(expression string) *time.Location {
	trimmed := strings.TrimSpace(expression)
	for _, prefix := range []string{"CRON_TZ=", "TZ="} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := trimmed[len(prefix):]
		name := rest
		if space := strings.IndexAny(rest, " \t"); space >= 0 {
			name = rest[:space]
		}
		if location, err := time.LoadLocation(name); err == nil {
			return location
		}
		return time.UTC
	}
	return time.UTC
}

// pinnedToAnHour reports an expression naming particular hours rather than
// running through them.
func pinnedToAnHour(expression string) bool {
	fields := strings.Fields(stripZonePrefix(expression))
	hour := ""
	switch len(fields) {
	case 5:
		hour = fields[1]
	case 6:
		hour = fields[2]
	default:
		return false
	}
	return hour != "*" && !strings.HasPrefix(hour, "*/")
}

// stripZonePrefix removes the TZ= or CRON_TZ= prefix robfig understands, so the
// remaining fields can be counted.
func stripZonePrefix(expression string) string {
	trimmed := strings.TrimSpace(expression)
	for _, prefix := range []string{"TZ=", "CRON_TZ="} {
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		if space := strings.IndexAny(trimmed, " \t"); space >= 0 {
			return strings.TrimSpace(trimmed[space+1:])
		}
		return ""
	}
	return trimmed
}

// InZone renders a cron expression with an explicit zone, which is the form
// stored schedules are evaluated in.
func InZone(expression, zone string) string {
	zone = strings.TrimSpace(zone)
	if zone == "" || zone == "UTC" {
		return expression
	}
	return "TZ=" + zone + " " + expression
}

// Validate reports whether a cron expression is usable.
//
// It calls Next rather than only parsing, so an expression that parses but
// never actually fires — "0 0 31 2 *" is syntactically fine and semantically
// impossible — is refused here too. That is what makes the check reach an
// inactive schedule: handlers/schedules.go's nextRun skips Next for those,
// since an inactive schedule has no due time to compute, but it always calls
// Validate.
func Validate(expression string) error {
	_, err := Next(expression, time.Now().UTC())
	return err
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
		// Built here rather than in the executor because this is where the
		// due time and the schedule's zone are both known. The executor sees
		// only the item, and a zone it had to guess would be the server's.
		dueAt := service.clock.Now()
		if item.Schedule.LastRunAt != nil {
			dueAt = *item.Schedule.LastRunAt
		}
		payload, err := json.Marshal(TriggerItem(item.Schedule.ID, dueAt, item.Schedule.Timezone))
		if err != nil {
			return queued, fmt.Errorf("encode schedule payload: %w", err)
		}
		if err := service.queue(ctx, item.Schedule.TenantID, item.Schedule.WorkflowID, item.WorkflowVersionID, item.Schedule.NodeID, payload); err != nil {
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
