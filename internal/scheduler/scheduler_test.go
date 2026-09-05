package scheduler_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// fixedClock makes every scheduler test deterministic: nothing sleeps, and a
// due time is reached by moving the clock rather than by waiting.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fixedClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fixedClock) Advance(by time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(by)
}

type queuedRun struct {
	tenantID   string
	workflowID string
	versionID  string
	// triggerNodeID says which schedule trigger fired. A workflow may declare
	// several triggers and only this one is running.
	triggerNodeID string
}

func TestNextComputesTheFollowingFireTimeInUTC(t *testing.T) {
	t.Parallel()

	after := time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC)
	next, err := scheduler.Next("0 * * * *", after)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if want := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("Next() = %s, want %s", next, want)
	}

	daily, err := scheduler.Next("30 2 * * *", after)
	if err != nil {
		t.Fatalf("Next() daily error = %v", err)
	}
	if want := time.Date(2026, 9, 6, 2, 30, 0, 0, time.UTC); !daily.Equal(want) {
		t.Errorf("Next() daily = %s, want %s", daily, want)
	}
}

func TestValidateRejectsUnusableExpressions(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{"", "not cron", "* * * *", "99 * * * *", "@every 1s"} {
		if err := scheduler.Validate(expression); err == nil {
			t.Errorf("Validate(%q) accepted an unusable expression", expression)
		}
	}
	for _, expression := range []string{"0 * * * *", "*/15 * * * *", "0 9 * * 1-5"} {
		if err := scheduler.Validate(expression); err != nil {
			t.Errorf("Validate(%q) = %v, want accepted", expression, err)
		}
	}
}

func TestTickFiresADueScheduleExactlyOncePerDueTime(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow

	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: start}
	next, _ := scheduler.Next("0 * * * *", start)
	if _, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, Cron: "0 * * * *", Active: true, NextRunAt: &next,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var runs []queuedRun
	service := newService(t, store, clock, &runs)

	// Nothing is due yet.
	if queued, err := service.Tick(context.Background()); err != nil || queued != 0 {
		t.Fatalf("Tick() before due = (%d, %v), want (0, nil)", queued, err)
	}

	clock.Advance(time.Hour)
	if queued, err := service.Tick(context.Background()); err != nil || queued != 1 {
		t.Fatalf("Tick() at due = (%d, %v), want (1, nil)", queued, err)
	}

	// Ticking again at the same time must not re-fire: the claim advanced the
	// due time inside the same transaction that read it.
	if queued, err := service.Tick(context.Background()); err != nil || queued != 0 {
		t.Fatalf("Tick() repeated = (%d, %v), want (0, nil)", queued, err)
	}

	if len(runs) != 1 || runs[0].workflowID != active.ID {
		t.Fatalf("queued runs = %#v, want exactly one for the scheduled workflow", runs)
	}
	if runs[0].versionID == "" {
		t.Error("a scheduled run was queued without pinning the active revision")
	}

	schedules, err := store.List(context.Background(), tenant)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if schedules[0].LastRunAt == nil || !schedules[0].LastRunAt.Equal(start.Add(time.Hour)) {
		t.Errorf("lastRunAt = %v, want the due time", schedules[0].LastRunAt)
	}
	if schedules[0].NextRunAt == nil || !schedules[0].NextRunAt.Equal(start.Add(2*time.Hour)) {
		t.Errorf("nextRunAt = %v, want the following hour", schedules[0].NextRunAt)
	}
}

func TestTickAdvancesFromTheDueTimeSoASlowTickDoesNotDrift(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow

	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	due := start.Add(time.Hour)
	clock := &fixedClock{now: start}
	if _, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, Cron: "0 * * * *", Active: true, NextRunAt: &due,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var runs []queuedRun
	service := newService(t, store, clock, &runs)

	// The tick arrives 40 minutes late. The next run must still be the top of
	// the following hour, not 40 minutes past it.
	clock.Advance(100 * time.Minute)
	if _, err := service.Tick(context.Background()); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}

	schedules, _ := store.List(context.Background(), tenant)
	if want := start.Add(2 * time.Hour); schedules[0].NextRunAt == nil || !schedules[0].NextRunAt.Equal(want) {
		t.Errorf("nextRunAt = %v, want %s with no drift", schedules[0].NextRunAt, want)
	}
}

func TestTickIgnoresInactiveSchedules(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow

	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	due := start.Add(-time.Hour)
	if _, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, Cron: "0 * * * *", Active: false, NextRunAt: &due,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var runs []queuedRun
	service := newService(t, store, &fixedClock{now: start}, &runs)

	if queued, err := service.Tick(context.Background()); err != nil || queued != 0 {
		t.Fatalf("Tick() = (%d, %v), want an inactive schedule to be skipped", queued, err)
	}
}

func TestTickDeactivatesAScheduleWhoseWorkflowIsNoLongerActive(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow
	workflows := setup.workflows

	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	due := start.Add(-time.Minute)
	if _, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, Cron: "0 * * * *", Active: true, NextRunAt: &due,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := workflows.Deactivate(context.Background(), tenant, active.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}

	var runs []queuedRun
	service := newService(t, store, &fixedClock{now: start}, &runs)

	if queued, err := service.Tick(context.Background()); err != nil || queued != 0 {
		t.Fatalf("Tick() = (%d, %v), want no run for a deactivated workflow", queued, err)
	}
	// Leaving it due would make every tick retry work that can never succeed.
	schedules, _ := store.List(context.Background(), tenant)
	if schedules[0].Active || schedules[0].NextRunAt != nil {
		t.Errorf("schedule = %#v, want it deactivated with no due time", schedules[0])
	}
}

func TestNewRejectsAnIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := scheduler.New(scheduler.Options{}); err == nil {
		t.Error("New() accepted a scheduler with no store or queue")
	}
}

func newService(t *testing.T, store repository.ScheduleRepository, clock scheduler.Clock, runs *[]queuedRun) *scheduler.Service {
	t.Helper()
	service, err := scheduler.New(scheduler.Options{
		Schedules: store,
		Clock:     clock,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Queue: func(_ context.Context, tenantID, workflowID, versionID, triggerNodeID string, _ json.RawMessage) error {
			*runs = append(*runs, queuedRun{tenantID: tenantID, workflowID: workflowID, versionID: versionID, triggerNodeID: triggerNodeID})
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

// fixture is everything a scheduler test needs to reach the same database the
// schedule store writes to.
type fixture struct {
	schedules *repository.GORMScheduleStore
	workflows *repository.GORMWorkflowStore
	tenant    repository.TenantScope
	workflow  workflow.StoredWorkflow
}

// newScheduleFixture builds a migrated database holding one active workflow a
// schedule can legally target.
func newScheduleFixture(t *testing.T) fixture {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "scheduler.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflows := repository.NewWorkflowStore(db.DB)
	stored, err := workflows.SaveDraft(context.Background(), tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          "Scheduled",
		Nodes: []workflow.Node{{
			ID: "n1", Name: "Schedule", Type: nodes.ScheduleType, TypeVersion: workflow.V(1),
			Position: workflow.Position{}, Parameters: map[string]any{"cron": "0 * * * *"},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	active, err := workflows.Activate(context.Background(), tenant, stored.ID, registry)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	return fixture{
		schedules: repository.NewScheduleStore(db.DB),
		workflows: workflows,
		tenant:    tenant,
		workflow:  active,
	}
}

// TestScheduleQueuesItsOwnTriggerNode proves the scheduler names the node that
// fired. Without it a workflow carrying a webhook beside a nightly schedule
// would run the webhook too on every due tick.
func TestScheduleQueuesItsOwnTriggerNode(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow

	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	clock := &fixedClock{now: start}
	next, _ := scheduler.Next("0 * * * *", start)
	created, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, NodeID: "nightly-cron", Cron: "0 * * * *", Active: true, NextRunAt: &next,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var runs []queuedRun
	service := newService(t, store, clock, &runs)
	clock.Advance(time.Hour)
	if queued, err := service.Tick(context.Background()); err != nil || queued != 1 {
		t.Fatalf("Tick() = (%d, %v), want (1, nil)", queued, err)
	}

	if len(runs) != 1 {
		t.Fatalf("queued %d runs, want 1", len(runs))
	}
	if runs[0].triggerNodeID != created.NodeID {
		t.Errorf("triggerNodeID = %q, want %q — the run must start from the schedule that fired", runs[0].triggerNodeID, created.NodeID)
	}
}

// scheduleDocument is a workflow whose only node is a Schedule Trigger with the
// given rule.
func scheduleDocument(name string, timezone string, intervals ...map[string]any) workflow.Document {
	entries := make([]any, 0, len(intervals))
	for _, interval := range intervals {
		entries = append(entries, interval)
	}
	settings := map[string]any{}
	if timezone != "" {
		settings["timezone"] = timezone
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		Name:          name,
		Nodes: []workflow.Node{{
			ID: "n1", Name: "Schedule", Type: nodes.ScheduleType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{"rule": map[string]any{"interval": entries}},
		}},
		Connections: []workflow.Connection{},
		Settings:    settings,
	}
}

// TestActivatingAWorkflowCreatesItsScheduleRows is the change that makes an
// imported schedule-driven workflow work at all.
//
// Before it, activation synced webhook bindings and nothing else, so a Schedule
// Trigger activated cleanly and then never fired: a row existed only if somebody
// called POST /api/v1/schedules by hand.
func TestActivatingAWorkflowCreatesItsScheduleRows(t *testing.T) {
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "activation.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, repository.Models()...); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflows := repository.NewWorkflowStore(db.DB).
		WithSchedules(scheduler.Extract(nodes.ScheduleType), scheduler.Next)
	schedules := repository.NewScheduleStore(db.DB)
	ctx := context.Background()

	// Two intervals on one node: "every weekday at 09:00 and again at 17:00".
	stored, err := workflows.SaveDraft(ctx, tenant, scheduleDocument("Twice daily", "Asia/Jakarta",
		map[string]any{"field": "days", "triggerAtHour": float64(9)},
		map[string]any{"field": "days", "triggerAtHour": float64(17)},
	))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if rows, _ := schedules.List(ctx, tenant); len(rows) != 0 {
		t.Fatalf("a draft created %d schedule rows, want none until it is activated", len(rows))
	}

	if _, err := workflows.Activate(ctx, tenant, stored.ID, registry); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	rows, err := schedules.List(ctx, tenant)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("activation created %d rows, want one per interval", len(rows))
	}
	crons := map[string]bool{}
	for _, row := range rows {
		crons[row.Cron] = true
		if !row.Active || row.NextRunAt == nil {
			t.Errorf("row %#v is not due to run", row)
		}
		if row.Timezone != "Asia/Jakarta" {
			t.Errorf("row timezone = %q, want the workflow's", row.Timezone)
		}
	}
	if !crons["0 9 * * *"] || !crons["0 17 * * *"] {
		t.Errorf("crons = %#v, want both intervals", crons)
	}

	// Editing the rule and re-activating replaces the rows rather than adding
	// to them; a rule edited down to one interval must not leave the other
	// firing forever.
	edited := scheduleDocument("Twice daily", "Asia/Jakarta",
		map[string]any{"field": "hours", "hoursInterval": float64(6)})
	edited.ID = stored.ID
	if _, err := workflows.SaveDraft(ctx, tenant, edited); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := workflows.Activate(ctx, tenant, stored.ID, registry); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	rows, _ = schedules.List(ctx, tenant)
	if len(rows) != 1 || rows[0].Cron != "0 */6 * * *" {
		t.Fatalf("rows after the edit = %#v, want only the new interval", rows)
	}

	// Deactivating takes them away in the same commit, so nothing fires for a
	// workflow that is off.
	if _, err := workflows.Deactivate(ctx, tenant, stored.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if rows, _ = schedules.List(ctx, tenant); len(rows) != 0 {
		t.Fatalf("rows after deactivation = %#v, want none", rows)
	}
}

// TestASchedulePastDueSkipsForwardRatherThanBuildingABacklog covers the case a
// process restart creates.
func TestASchedulePastDueSkipsForwardRatherThanBuildingABacklog(t *testing.T) {
	setup := newScheduleFixture(t)
	store, tenant, active := setup.schedules, setup.tenant, setup.workflow

	// Due a day ago, on an hourly schedule: twenty-four occurrences went by
	// while nothing was running. Firing all of them would spend the next
	// twenty-four ticks running yesterday's work.
	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	due := start.Add(-24 * time.Hour)
	if _, err := store.Create(context.Background(), tenant, repository.Schedule{
		WorkflowID: active.ID, NodeID: "n1", Cron: "0 * * * *", Active: true, NextRunAt: &due,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	clock := &fixedClock{now: start}
	var runs []queuedRun
	service := newService(t, store, clock, &runs)
	if queued, err := service.Tick(context.Background()); err != nil || queued != 1 {
		t.Fatalf("Tick() = (%d, %v), want the one missed run, not a backlog", queued, err)
	}

	rows, err := store.List(context.Background(), tenant)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 || rows[0].NextRunAt == nil {
		t.Fatalf("rows = %#v, want one still-scheduled row", rows)
	}
	// The next run is ahead of now, not still a day behind.
	if !rows[0].NextRunAt.After(start) {
		t.Errorf("next run = %s, want a time after %s", rows[0].NextRunAt, start)
	}
	if queued, _ := service.Tick(context.Background()); queued != 0 {
		t.Errorf("a second tick queued %d more runs, so the backlog survived", queued)
	}
}
