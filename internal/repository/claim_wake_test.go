package repository_test

// NOTE (shared-server race): the PostgreSQL-gated tests in this file run
// against one shared KILASFLOW_TEST_POSTGRES_DSN, and internal/database's
// PostgreSQL tests drop every KilasFlow table on entry. A combined run must
// use `go test -p 1` (or give each package its own database), or the schema
// can vanish mid-test as `relation "executions" does not exist`. See
// .pine/memory/persistence.md.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// claimProbe stands in for the execution row for SQL-shape assertions: the
// locking clause and the ordering are what is under test, not the column
// list, so a three-column row with the same table and primary key renders
// the same FROM, ORDER BY and FOR UPDATE as ClaimNext's candidate select.
type claimProbe struct {
	ID        string `gorm:"primaryKey;size:64"`
	Status    string
	StartedAt time.Time
}

func (claimProbe) TableName() string { return "executions" }

// claimCandidate builds the statement ClaimNext selects its candidate with.
// It mirrors that select deliberately — including First, which appends its
// own primary-key ordering (see .pine/memory/persistence.md) — so the SQL
// asserted here is the SQL the server receives, not a paraphrase of it.
func claimCandidate(db *gorm.DB, now time.Time) *gorm.DB {
	return db.Session(&gorm.Session{DryRun: true}).
		Clauses(clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}).
		Where("status = ? OR ((status = ? OR status = ?) AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)",
			string(execution.StatusQueued), string(execution.StatusRunning), string(execution.StatusCancelling), now).
		Order("started_at ASC, id ASC").
		First(&claimProbe{})
}

// ClaimNext must issue FOR UPDATE SKIP LOCKED on PostgreSQL. The clause is
// built unconditionally and the SQLite driver drops it, so the rendering has
// to be proven per driver rather than read off the call site.
func TestClaimSelectLocksSkippedRowsOnPostgres(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=kilasflow dbname=kilasflow"}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	generated := claimCandidate(db, time.Now().UTC())
	sql := generated.Statement.SQL.String()
	if !strings.Contains(sql, "FOR UPDATE SKIP LOCKED") {
		t.Errorf("postgres claim SQL = %q, want it to hold FOR UPDATE SKIP LOCKED", sql)
	}
	// GORM's First appends ORDER BY on the primary key to the caller's Order:
	// the plan proved in the EXPLAIN test below is only evidence if it uses
	// this three-column spelling.
	if !strings.Contains(sql, `"executions"."id"`) {
		t.Errorf("postgres claim SQL = %q, want First's appended primary-key ordering", sql)
	}
}

// The same select must stay a plain SELECT on SQLite: the glebarez driver
// drops clause.Locking silently, and correctness there rests on the
// conditional UPDATE rather than on a lock this test would prove exists.
func TestClaimSelectDropsTheLockOnSQLite(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
	}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sql := claimCandidate(db.DB, time.Now().UTC()).Statement.SQL.String()
	if strings.Contains(sql, "FOR UPDATE") {
		t.Errorf("sqlite claim SQL = %q, want no locking clause: the driver drops it and the test must say so", sql)
	}
}

// Ten workers racing for ten queued executions each land on a different row:
// no lost update, no double claim, nothing left behind. On SQLite the locking
// clause above never reaches the database, so this is also the proof that
// the conditional UPDATE keeps the same code path correct there.
func TestConcurrentWorkersClaimEachExecutionExactlyOnce(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "kilasflow.db"),
	}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	claimConcurrency(t, db, "contention-tenant", "contention_wf", 10)
}

// openClaimPostgres opens the shared PostgreSQL server for the contention and
// wake tests. Rows are removed by tenant afterwards rather than tables
// dropped — another package may be using them (see the note at the top of
// this file).
func openClaimPostgres(t *testing.T) (*database.DB, string) {
	t.Helper()
	dsn := os.Getenv("KILASFLOW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to run the PostgreSQL half")
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{Driver: "postgres", DSN: dsn}, quiet)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM execution_node_runs WHERE tenant_id LIKE 'drv-%'`)
		db.Exec(`DELETE FROM executions WHERE tenant_id LIKE 'drv-%'`)
		db.Exec(`DELETE FROM workflow_publish_events WHERE tenant_id LIKE 'drv-%'`)
		db.Exec(`DELETE FROM workflow_versions WHERE tenant_id LIKE 'drv-%'`)
		db.Exec(`DELETE FROM workflows WHERE tenant_id LIKE 'drv-%'`)
	})
	return db, dsn
}

func queueClaimFixture(t *testing.T, db *database.DB, tenantID, workflowID string, count int) []string {
	t.Helper()
	ctx := context.Background()
	tenant := repository.TenantScope{ID: tenantID}
	workflows := repository.NewWorkflowStore(db.DB)
	saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            workflowID, Name: "Claim contention",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	executions := repository.NewExecutionStore(db.DB)
	ids := make([]string, 0, count)
	for range count {
		queued, err := executions.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), nil)
		if err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		ids = append(ids, queued.ID)
	}
	return ids
}

// claimConcurrency runs workers against queued executions and requires every
// execution to be claimed exactly once. Shared by the SQLite proof above and
// the PostgreSQL proof below.
func claimConcurrency(t *testing.T, db *database.DB, tenantID, workflowID string, workers int) {
	t.Helper()
	queued := queueClaimFixture(t, db, tenantID, workflowID, workers)
	store := repository.NewExecutionStore(db.DB)
	type result struct {
		id    string
		found bool
		err   error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			claimed, _, found, err := store.ClaimNext(context.Background(), fmt.Sprintf("%s-worker-%d", tenantID, n), time.Now().Add(time.Minute))
			if err != nil || !found {
				results <- result{err: err, found: found}
				return
			}
			results <- result{id: claimed.ID, found: true}
		}(i)
	}
	wg.Wait()
	close(results)
	seen := map[string]int{}
	for r := range results {
		if r.err != nil {
			t.Fatalf("ClaimNext() error = %v", r.err)
		}
		if !r.found {
			t.Fatalf("a worker found nothing with %d executions queued", workers)
		}
		seen[r.id]++
	}
	if len(seen) != workers {
		t.Fatalf("workers claimed %d distinct executions, want %d (lost update or double claim)", len(seen), workers)
	}
	for _, id := range queued {
		if seen[id] != 1 {
			t.Errorf("execution %q claimed %d times, want exactly once", id, seen[id])
		}
	}
	if _, _, found, err := store.ClaimNext(context.Background(), tenantID+"-worker", time.Now().Add(time.Minute)); err != nil || found {
		t.Errorf("ClaimNext() after the race = (%v, %v), want nothing left to claim", found, err)
	}
}

// Ten concurrent workers claim ten distinct queued executions on PostgreSQL,
// where SKIP LOCKED is real and each worker lands on a different row instead
// of racing for one.
func TestTenWorkersClaimTenDistinctExecutionsOnPostgres(t *testing.T) {
	db, _ := openClaimPostgres(t)
	claimConcurrency(t, db, "drv-claim", "drv_wf_claim", 10)
}

// The EXPLAIN of the exact statement ClaimNext sends — same builder as the
// SQL-shape test, so the proved spelling is the executed spelling — shows
// the lock reaching the planner as a LockRows node over the executions scan.
func TestClaimSelectPlanLocksRowsOnPostgres(t *testing.T) {
	dry, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=kilasflow dbname=kilasflow"}), &gorm.Config{DisableAutomaticPing: true})
	db, _ := openClaimPostgres(t)
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	built := claimCandidate(dry, time.Now().UTC())
	var rows []struct {
		Plan string `gorm:"column:QUERY PLAN"`
	}
	if err := db.Raw("EXPLAIN "+built.Statement.SQL.String(), built.Statement.Vars...).Scan(&rows).Error; err != nil {
		t.Fatalf("EXPLAIN claim select: %v", err)
	}
	plan := make([]string, 0, len(rows))
	for _, row := range rows {
		plan = append(plan, row.Plan)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "LockRows") {
		t.Errorf("claim plan has no LockRows node, so SKIP LOCKED did not reach the planner:\n%s", joined)
	}
	if !strings.Contains(joined, "executions") {
		t.Errorf("claim plan never touches executions:\n%s", joined)
	}
}

// Queuing an execution in one process wakes a listener in another without
// waiting for the poll tick: the notification carries tenant and execution
// IDs only, and the woken worker re-reads the row through ClaimNext rather
// than trusting the payload.
func TestQueuedExecutionWakesAListenerOnPostgres(t *testing.T) {
	db, dsn := openClaimPostgres(t)
	ctx := context.Background()
	tenant := repository.TenantScope{ID: "drv-wake"}
	workflows := repository.NewWorkflowStore(db.DB)
	saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "drv_wf_wake", Name: "Wake",
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1),
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	})
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)

	watchCtx, stop := context.WithCancel(context.Background())
	defer stop()
	wakes := make(chan repository.ExecutionWake, 16)
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- repository.WatchExecutions(watchCtx, dsn, "", func(wake repository.ExecutionWake) {
			wakes <- wake
		}, nil)
	}()

	// The first queue can race the listener's LISTEN, so queue until a wake
	// lands rather than sleeping a fixed setup delay: every queue after the
	// LISTEN commits notifies, and pg_notify fires on commit, so a received
	// wake always names a row that is really there.
	queuedIDs := map[string]bool{}
	var got repository.ExecutionWake
	var latency time.Duration
	wonAt := -1
	woken := false
	for attempt := range 8 {
		if woken {
			break
		}
		queued, err := store.QueueManualLatest(ctx, tenant, saved.ID, driverCatalog(), nil)
		if err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		queuedIDs[queued.ID] = true
		queuedAt := time.Now()
		select {
		case got = <-wakes:
			latency = time.Since(queuedAt)
			wonAt = attempt
			woken = true
		case <-time.After(2 * time.Second):
		}
	}
	if !woken {
		t.Fatalf("no wake arrived for %d queued executions: the listener never fired", len(queuedIDs))
	}
	t.Logf("queue-to-wake latency %s against a 100ms poll tick (first wake on queue %d)", latency, wonAt)
	if got.TenantID != tenant.ID {
		t.Errorf("wake tenant = %q, want %q", got.TenantID, tenant.ID)
	}
	if !queuedIDs[got.ExecutionID] {
		t.Errorf("wake execution %q names no queued row: the worker must re-read, but the payload must still name reality", got.ExecutionID)
	}
	// The woken worker re-reads rather than trusting the payload: it must
	// come back with a row this test queued — any of them, since the setup
	// loop queues again whenever the first LISTEN misses its NOTIFY. Pinning
	// the woken identity would fail exactly when the listener was slow,
	// which is a property of the test's timing, not of the claim path.
	claimed, _, found, err := store.ClaimNext(ctx, "drv-wake-worker", time.Now().Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("ClaimNext() after the wake = (%v, %v), want a queued row", found, err)
	}
	if !queuedIDs[claimed.ID] {
		t.Errorf("claimed %q names no row this test queued: the re-read left the queued set", claimed.ID)
	}
	stop()
	if err := <-watchDone; err != nil {
		t.Errorf("WatchExecutions() = %v, want nil on context stop", err)
	}
}

// Two prefixed installs sharing one database must not wake each other's
// workers: the channel carries the prefix, stays a valid identifier, and
// stays inside PostgreSQL's 63-byte channel-name limit.
func TestExecutionWakeChannelKeysTheTablePrefix(t *testing.T) {
	bare := repository.ExecutionWakeChannel("")
	prefixed := repository.ExecutionWakeChannel("kflow_")
	other := repository.ExecutionWakeChannel("tenant42_")
	if bare == prefixed || bare == other || prefixed == other {
		t.Fatalf("channels collide: %q, %q, %q", bare, prefixed, other)
	}
	for prefix, channel := range map[string]string{"": bare, "kflow_": prefixed, "tenant42_": other} {
		if !strings.HasPrefix(channel, prefix) || channel == prefix {
			t.Errorf("channel %q does not carry prefix %q", channel, prefix)
		}
		if len(channel) > 63 {
			t.Errorf("channel %q is %d bytes, past the 63-byte limit", channel, len(channel))
		}
		for _, r := range channel {
			if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				t.Errorf("channel %q holds %q, which needs quoting on LISTEN", channel, r)
			}
		}
	}
}

// A listener that can never connect reports each drop and still stops with
// its context: losing the connection is neither silent nor fatal, and the
// engine's poll tick covers the work meanwhile.
func TestWatchExecutionsReportsDropsAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	drops := 0
	start := time.Now()
	err := repository.WatchExecutions(ctx, "host=127.0.0.1 port=1 user=nope dbname=nope connect_timeout=1", "",
		func(wake repository.ExecutionWake) {
			t.Error("no wake can arrive without a connection")
		}, func(report error) {
			mu.Lock()
			drops++
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("WatchExecutions() = %v, want nil on context stop", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if drops == 0 {
		t.Errorf("no drop reported in %s of retrying: the listener failed silently", time.Since(start))
	}
}
