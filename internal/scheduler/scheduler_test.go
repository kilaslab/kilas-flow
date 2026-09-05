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
