package datastore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/kilaslab/kilas-flow/internal/database"
)

// These tests cover BUG-xmr673: the engine owns the schema version it serves,
// and Engine.MigrateFleet / Engine.FleetStatus are the entry points the
// composition root and readiness call. The product ships exactly one schema
// version, so every "next build" below is an Engine whose unexported
// schemaVersion and fleetSteps a test raises by hand.
//
// Refusals are asserted as the whole sentence, never as a digit: "1" or "2"
// is satisfied by any UUID or temp path and can never fail.

// fleetStepProbe counts the invocations of a test-only step.
type fleetStepProbe struct{ calls atomic.Int64 }

// step is a FleetStep that does nothing but count.
func (p *fleetStepProbe) step(context.Context, *gorm.DB, Datastore) error {
	p.calls.Add(1)
	return nil
}

// fleetTestDatastore creates a one-column datastore and registers its
// physical cleanup.
func fleetTestDatastore(t *testing.T, db *database.DB, eng *Engine, name string) *Datastore {
	t.Helper()
	ds, err := eng.Create(context.Background(), "tenant-1", name, []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	trackPhysical(t, db, ds.Table)
	return ds
}

// fleetNextBuild is an Engine over the same database that stands in for a
// build whose schema version is target: the previous build's datastores are
// behind it until MigrateFleet runs.
func fleetNextBuild(t *testing.T, db *database.DB, prefix string, target int) *Engine {
	t.Helper()
	next, err := NewEngine(db, prefix)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	next.schemaVersion = target
	return next
}

func fleetBehindText(id string, have, want int) string {
	return fmt.Sprintf("datastore: %s is at schema version %d, want %d", id, have, want)
}

func fleetAheadText(id string, have, known int) string {
	return fmt.Sprintf("datastore: %s is at schema version %d but this build knows version %d", id, have, known)
}

// wantFleetRefusal fails unless err carries the exact sentence.
func wantFleetRefusal(t *testing.T, what string, err error, sentence string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s = nil, want a refusal containing %q", what, sentence)
		return
	}
	if !strings.Contains(err.Error(), sentence) {
		t.Errorf("%s refused with %q, want the sentence %q", what, err, sentence)
	}
}

// createTitleIndex is the physical work a real step would do: a new index on
// the datastore's table, composed through the step's own transaction handle.
func createTitleIndex(tx *gorm.DB, table, indexName string) error {
	dialect := tx.Dialector.Name()
	return tx.Exec("CREATE INDEX " + quoteIdent(dialect, indexName) +
		" ON " + quoteIdent(dialect, table) + " (" + quoteIdent(dialect, "title") + ")").Error
}

// fleetReadBarrier parks each runner right after it read a datastore as
// behind, until every party has done the same, so both have decided to
// migrate before either opens its transaction: the interleaving of two
// processes booting together, made deterministic.
type fleetReadBarrier struct {
	t  *testing.T
	wg sync.WaitGroup
}

func newFleetReadBarrier(t *testing.T, parties int) *fleetReadBarrier {
	b := &fleetReadBarrier{t: t}
	b.wg.Add(parties)
	return b
}

// hook returns the afterSelect hook for one runner. It joins the barrier the
// first time only, so a runner that reads a second behind datastore cannot
// drive the counter negative.
func (b *fleetReadBarrier) hook() func(string) {
	var once sync.Once
	return func(string) {
		once.Do(func() {
			b.wg.Done()
			released := make(chan struct{})
			go func() { b.wg.Wait(); close(released) }()
			select {
			case <-released:
			case <-time.After(10 * time.Second):
				b.t.Errorf("the other runner never read the datastore as behind: the barrier timed out")
			}
		})
	}
}

// The ticket's criterion: a datastore created by the previous build is
// migrated by the runner and serves rows afterwards, instead of refusing
// traffic. The "previous build" is an engine at version 1 and the "next
// build" an engine at version 2 with one test-only step registered; the
// product's own CurrentSchemaVersion stays 1.
func TestMigrateFleetMovesAPreviousVersionDatastoreAndItServesRows(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()

			ds := fleetTestDatastore(t, db, eng, "contacts")
			mustInsertRow(t, eng, ds, map[string]any{"title": "Alpha Team"})
			mustInsertRow(t, eng, ds, map[string]any{"title": "beta crew"})

			next := fleetNextBuild(t, db, "", 2)
			indexName := "ix_" + ds.Surrogate + "_title"
			var probe fleetStepProbe
			var seen Datastore
			// The step uses only the transaction it is handed: a second
			// transaction would deadlock SQLite's single connection. It does
			// real physical work and deliberately writes no catalogue rows.
			next.fleetSteps[1] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
				probe.calls.Add(1)
				seen = got
				if err := createTitleIndex(tx, ds.Table, indexName); err != nil {
					return err
				}
				dialect := tx.Dialector.Name()
				title := quoteIdent(dialect, "title")
				return tx.Exec("UPDATE " + quoteIdent(dialect, ds.Table) + " SET " + title + " = upper(" + title + ")").Error
			}

			// Before the run the next build refuses the datastore and says
			// what is outstanding.
			_, err := next.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			wantFleetRefusal(t, "List before the migration", err, fleetBehindText(ds.ID, 1, 2))
			_, err = next.Insert(ctx, "tenant-1", ds.ID, map[string]any{"title": "refused"})
			wantFleetRefusal(t, "Insert before the migration", err, fleetBehindText(ds.ID, 1, 2))
			status, err := next.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus before: %v", err)
			}
			if status.Behind != 1 || status.Ready() || !reflect.DeepEqual(status.Spread, map[int]int64{1: 1}) {
				t.Errorf("status before = %+v, want 1 behind, not ready, spread map[1:1]", status)
			}

			n, err := next.MigrateFleet(ctx)
			if err != nil {
				t.Fatalf("MigrateFleet: %v", err)
			}
			if n != 1 || probe.calls.Load() != 1 {
				t.Fatalf("MigrateFleet migrated %d datastores with %d step invocations, want 1 and 1", n, probe.calls.Load())
			}
			// The step is handed the whole datastore: the old runner left
			// Table and Columns empty, a trap for step authors.
			wantSeen := Datastore{
				ID: ds.ID, TenantID: "tenant-1", Name: "contacts", Surrogate: ds.Surrogate,
				Table:         PhysicalTableName("", ds.Surrogate),
				SchemaVersion: 1, // the version the step moves FROM
				Columns:       []ColumnDef{{Name: "title", Type: ColumnString, Position: 0}},
			}
			if !reflect.DeepEqual(seen, wantSeen) {
				t.Errorf("the step saw %+v, want %+v", seen, wantSeen)
			}

			// After: the datastore serves rows on the new version.
			page, err := next.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("List after the migration: %v", err)
			}
			var titles []string
			for _, row := range page.Rows {
				titles = append(titles, asString(t, row["title"]))
			}
			if !reflect.DeepEqual(titles, []string{"ALPHA TEAM", "BETA CREW"}) {
				t.Errorf("titles after the backfill = %v, want [ALPHA TEAM BETA CREW]", titles)
			}
			inserted := mustInsertRow(t, next, ds, map[string]any{"title": "gamma"})
			if _, err := next.Get(ctx, "tenant-1", ds.ID, inserted["id"].(int64)); err != nil {
				t.Errorf("Get after the migration: %v", err)
			}
			if !db.Migrator().HasIndex(ds.Table, indexName) {
				t.Errorf("the step's index %s is missing after the migration", indexName)
			}
			if got := versionOf(t, next, ds); got != 2 {
				t.Errorf("catalogue version after the migration = %d, want 2", got)
			}
			status, err = next.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus after: %v", err)
			}
			if !status.Ready() || status.Version != 2 || status.Spread[2] != 1 || len(status.Spread) != 1 {
				t.Errorf("status after = %+v, want ready at version 2 with spread {2:1}", status)
			}

			// A second pass is a no-op: the step ran once.
			n, err = next.MigrateFleet(ctx)
			if err != nil || n != 0 || probe.calls.Load() != 1 {
				t.Errorf("second MigrateFleet = %d, %v with %d invocations, want 0, nil and 1", n, err, probe.calls.Load())
			}

			// The previous build refuses the migrated datastore as ahead of
			// it, naming both versions, at the row level and at the runner.
			_, err = eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			wantFleetRefusal(t, "the old build's List", err, fleetAheadText(ds.ID, 2, 1))
			_, err = eng.MigrateFleet(ctx)
			wantFleetRefusal(t, "the old build's MigrateFleet", err, fleetAheadText(ds.ID, 2, 1))

			// A datastore created by the next build is born at its version
			// and serves rows immediately.
			born := fleetTestDatastore(t, db, next, "born-at-two")
			if born.SchemaVersion != 2 {
				t.Errorf("a datastore created by the next build is at version %d, want 2", born.SchemaVersion)
			}
			mustInsertRow(t, next, born, map[string]any{"title": "first"})
			if page, err := next.List(ctx, "tenant-1", born.ID, RowQuery{ReturnAll: true}); err != nil || len(page.Rows) != 1 {
				t.Errorf("List on the born-at-two datastore = %d rows, %v, want 1 row", len(page.Rows), err)
			}

			for _, id := range []string{born.ID, ds.ID} {
				if err := next.Drop(ctx, "tenant-1", id); err != nil {
					t.Fatalf("Drop %s: %v", id, err)
				}
			}
		})
	}
}

// FleetStatus classifies every bucket against the version the engine serves.
// Ahead is reported but does not fail readiness; behind does. The problem
// text is served on an unauthenticated endpoint, so it carries counts and
// versions and never an id.
func TestFleetStatusClassifiesBehindAndAhead(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()

			status, err := eng.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus on an empty fleet: %v", err)
			}
			if status.Version != CurrentSchemaVersion || status.Spread == nil || len(status.Spread) != 0 ||
				status.Behind != 0 || status.Ahead != 0 || !status.Ready() || status.Problem() != "" {
				t.Errorf("empty fleet status = %+v (ready %v, problem %q), want ready with a non-nil empty spread",
					status, status.Ready(), status.Problem())
			}

			behind := fleetTestDatastore(t, db, eng, "behind")
			ahead := fleetTestDatastore(t, db, eng, "ahead")
			current := fleetTestDatastore(t, db, eng, "current")
			pushBehind(t, eng, behind, 0)
			pushBehind(t, eng, ahead, CurrentSchemaVersion+2)

			status, err = eng.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus: %v", err)
			}
			if status.Version != CurrentSchemaVersion || status.Behind != 1 || status.Ahead != 1 {
				t.Errorf("status = %+v, want version %d with 1 behind and 1 ahead", status, CurrentSchemaVersion)
			}
			if want := map[int]int64{0: 1, 1: 1, 3: 1}; !reflect.DeepEqual(status.Spread, want) {
				t.Errorf("spread = %v, want %v", status.Spread, want)
			}
			if status.Ready() {
				t.Error("a fleet with a datastore behind is ready, want not ready")
			}
			wantProblem := "datastore migration outstanding: 1 datastore(s) behind schema version 1 (spread v0=1, v1=1, v3=1)"
			problem := status.Problem()
			if problem != wantProblem {
				t.Errorf("Problem() = %q, want %q", problem, wantProblem)
			}
			for _, secret := range []string{behind.ID, ahead.ID, current.ID, "tenant-1", "datastore_"} {
				if strings.Contains(problem, secret) {
					t.Errorf("Problem() %q leaks %q on an unauthenticated endpoint", problem, secret)
				}
			}

			// Behind resolved, ahead remains: reported, not a readiness
			// failure (an old pod is not broken because a newer peer
			// migrated a datastore, and the row gate refuses just that one).
			pushBehind(t, eng, behind, CurrentSchemaVersion)
			status, err = eng.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus with only an ahead datastore: %v", err)
			}
			if status.Ahead != 1 || status.Behind != 0 || !status.Ready() || status.Problem() != "" {
				t.Errorf("status with only an ahead datastore = %+v (ready %v, problem %q), want 1 ahead and ready",
					status, status.Ready(), status.Problem())
			}
		})
	}
}

// Two processes booting together both read a datastore as behind. The step
// must still run once: the second re-reads the row inside its own transaction
// and finds it already advanced. The barrier makes the interleaving
// deterministic (both have read before either opens a transaction); on SQLite
// the one-connection pool serializes the transactions, on PostgreSQL the
// FOR UPDATE re-read does the work.
func TestFleetConcurrentRunsApplyEachStepOnce(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds := fleetTestDatastore(t, db, eng, "contacts")
			pushBehind(t, eng, ds, 0)

			var probe fleetStepProbe
			barrier := newFleetReadBarrier(t, 2)
			migrated := make([]int, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i := range 2 {
				runner := eng.fleetRunner()
				runner.Register(0, probe.step)
				runner.afterSelect = barrier.hook()
				wg.Add(1)
				go func() {
					defer wg.Done()
					migrated[i], errs[i] = runner.Run(ctx, db)
				}()
			}
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Errorf("runner %d: %v", i, err)
				}
			}
			if got := probe.calls.Load(); got != 1 {
				t.Errorf("the step ran %d times across two concurrent runs, want exactly 1", got)
			}
			counts := append([]int(nil), migrated...)
			sort.Ints(counts)
			if !reflect.DeepEqual(counts, []int{0, 1}) {
				t.Errorf("runs reported %v datastores migrated, want one run with 1 and the other with 0", migrated)
			}
			if got := versionOf(t, eng, ds); got != 1 {
				t.Errorf("version after the concurrent runs = %d, want 1", got)
			}
		})
	}
}

// A stress check next to the deterministic one above: several runners over
// several behind datastores with no hook apply every step exactly once.
func TestFleetConcurrentRunsStress(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			const datastores, runners = 5, 4

			var all []*Datastore
			for i := range datastores {
				ds := fleetTestDatastore(t, db, eng, fmt.Sprintf("stress-%d", i))
				pushBehind(t, eng, ds, 0)
				all = append(all, ds)
			}

			var probe fleetStepProbe
			migrated := make([]int, runners)
			errs := make([]error, runners)
			var wg sync.WaitGroup
			for i := range runners {
				runner := eng.fleetRunner()
				runner.Register(0, probe.step)
				wg.Add(1)
				go func() {
					defer wg.Done()
					migrated[i], errs[i] = runner.Run(ctx, db)
				}()
			}
			wg.Wait()

			total := 0
			for i, err := range errs {
				if err != nil {
					t.Errorf("runner %d: %v", i, err)
				}
				total += migrated[i]
			}
			if got := probe.calls.Load(); got != datastores {
				t.Errorf("the step ran %d times, want once per datastore (%d)", got, datastores)
			}
			if total != datastores {
				t.Errorf("the runners reported %d datastores migrated in total (%v), want %d", total, migrated, datastores)
			}
			for _, ds := range all {
				if got := versionOf(t, eng, ds); got != 1 {
					t.Errorf("%s at version %d after the runs, want 1", ds.ID, got)
				}
			}
		})
	}
}

// missingFleetSteps names the versions below current that no step moves a
// datastore from. The tripwire below is only worth having if the function
// can trip, so it is tested on its own first.
func TestEveryVersionBehindTheCurrentOneHasAShippedFleetStep(t *testing.T) {
	noop := func(context.Context, *gorm.DB, Datastore) error { return nil }

	if got := missingFleetSteps(3, map[int]FleetStep{}); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("missingFleetSteps(3, none) = %v, want [1 2]", got)
	}
	if got := missingFleetSteps(3, map[int]FleetStep{2: noop}); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("missingFleetSteps(3, {2}) = %v, want [1]", got)
	}
	if got := missingFleetSteps(3, map[int]FleetStep{1: noop, 2: noop}); len(got) != 0 {
		t.Errorf("missingFleetSteps(3, {1,2}) = %v, want none", got)
	}
	if got := missingFleetSteps(1, nil); len(got) != 0 {
		t.Errorf("missingFleetSteps(1, nil) = %v, want none: version 1 has nothing behind it", got)
	}

	if missing := missingFleetSteps(CurrentSchemaVersion, shippedFleetSteps()); len(missing) != 0 {
		t.Fatalf("CurrentSchemaVersion is %d but shippedFleetSteps has no step moving a datastore from version(s) %v: "+
			"bumping CurrentSchemaVersion without registering its step in the same commit takes every existing datastore offline at boot",
			CurrentSchemaVersion, missing)
	}

	// Every engine gets its own map, so registering a step on one (as the
	// tests do) can never leak into another.
	first := shippedFleetSteps()
	first[99] = noop
	if _, leaked := shippedFleetSteps()[99]; leaked {
		t.Error("shippedFleetSteps returned a shared map: a step registered on one engine leaks into every other")
	}
}

// A step that fails leaves the datastore fully at the old version, DDL
// included, on both dialects; the next pass with a working step resumes.
func TestFleetStepFailureRollsBackThroughMigrateFleet(t *testing.T) {
	errBoom := errors.New("boom: the step gave up after its DDL")
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds := fleetTestDatastore(t, db, eng, "contacts")
			indexName := "ix_" + ds.Surrogate + "_title"

			eng.schemaVersion = 2
			eng.fleetSteps[1] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
				if err := createTitleIndex(tx, got.Table, indexName); err != nil {
					return err
				}
				return errBoom
			}
			n, err := eng.MigrateFleet(ctx)
			if !errors.Is(err, errBoom) {
				t.Fatalf("MigrateFleet = %d, %v, want the step's error", n, err)
			}
			if n != 0 {
				t.Errorf("MigrateFleet reported %d datastores migrated after a failing step, want 0", n)
			}
			if got := versionOf(t, eng, ds); got != 1 {
				t.Errorf("version after the failed step = %d, want 1 (fully old, never between)", got)
			}
			if db.Migrator().HasIndex(ds.Table, indexName) {
				t.Error("the failed step's index survived: its DDL was not rolled back with the stamp")
			}

			var probe fleetStepProbe
			eng.fleetSteps[1] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
				probe.calls.Add(1)
				return createTitleIndex(tx, got.Table, indexName)
			}
			n, err = eng.MigrateFleet(ctx)
			if err != nil || n != 1 {
				t.Fatalf("MigrateFleet with a working step = %d, %v, want 1, nil", n, err)
			}
			if got := versionOf(t, eng, ds); got != 2 {
				t.Errorf("version after the resumed run = %d, want 2", got)
			}
			if !db.Migrator().HasIndex(ds.Table, indexName) {
				t.Error("the resumed step's index is missing")
			}
		})
	}
}

// A datastore several versions behind advances one step per transaction: a
// failure in step 2 leaves it committed at 2 (step 1 stays applied), and the
// next pass runs only step 2. The same path resumes a datastore a peer left
// half-way.
func TestFleetRunAdvancesAMultiStepGapOneTransactionPerStep(t *testing.T) {
	errStep2 := errors.New("step 2 is not ready yet")
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds := fleetTestDatastore(t, db, eng, "contacts")

			next := fleetNextBuild(t, db, "", 3)
			var step1, step2 fleetStepProbe
			failStep2 := true
			next.fleetSteps[1] = step1.step
			next.fleetSteps[2] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
				step2.calls.Add(1)
				if got.SchemaVersion != 2 {
					t.Errorf("step 2 was handed a datastore at version %d, want 2", got.SchemaVersion)
				}
				if failStep2 {
					return errStep2
				}
				return nil
			}

			n, err := next.MigrateFleet(ctx)
			if !errors.Is(err, errStep2) || n != 0 {
				t.Fatalf("first MigrateFleet = %d, %v, want 0 and the step 2 error", n, err)
			}
			if got := versionOf(t, next, ds); got != 2 {
				t.Errorf("version after step 2 failed = %d, want 2 (step 1 committed, nothing between)", got)
			}
			status, err := next.FleetStatus(ctx)
			if err != nil {
				t.Fatalf("FleetStatus: %v", err)
			}
			if status.Behind != 1 || status.Ready() || status.Spread[2] != 1 || len(status.Spread) != 1 {
				t.Errorf("status after the partial run = %+v, want 1 behind with spread {2:1}", status)
			}

			failStep2 = false
			n, err = next.MigrateFleet(ctx)
			if err != nil {
				t.Fatalf("resumed MigrateFleet: %v", err)
			}
			if n != 1 {
				t.Errorf("resumed MigrateFleet reported %d datastores, want 1 (it counts datastores, not steps)", n)
			}
			if step1.calls.Load() != 1 || step2.calls.Load() != 2 {
				t.Errorf("step invocations = step 1 x%d, step 2 x%d, want 1 and 2: the resume must not rerun step 1",
					step1.calls.Load(), step2.calls.Load())
			}
			if got := versionOf(t, next, ds); got != 3 {
				t.Errorf("version after the resume = %d, want 3", got)
			}
		})
	}
}

// A datastore dropped between the runner reading it and opening its
// transaction is skipped, not migrated: the locked re-read finds no row. This
// is the path a concurrent tenant purge relies on.
func TestFleetRunSkipsADatastoreDroppedAfterItWasRead(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds := fleetTestDatastore(t, db, eng, "contacts")
			pushBehind(t, eng, ds, 0)

			var probe fleetStepProbe
			runner := eng.fleetRunner()
			runner.Register(0, probe.step)
			// The runner holds no connection between its read and its
			// transaction, so the drop is free to run on SQLite too.
			runner.afterSelect = func(id string) {
				if err := eng.Drop(ctx, "tenant-1", id); err != nil {
					t.Errorf("Drop from the hook: %v", err)
				}
			}

			n, err := runner.Run(ctx, db)
			if err != nil || n != 0 {
				t.Errorf("Run = %d, %v, want 0, nil: the datastore vanished, nothing to migrate", n, err)
			}
			if got := probe.calls.Load(); got != 0 {
				t.Errorf("the step ran %d times for a datastore that no longer exists, want 0", got)
			}
		})
	}
}

// A step that writes the datastore's own catalogue row is a contract
// violation, not a skip: the runner owns the version stamp, so the
// compare-and-swap matches no row and the step's write is rolled back with
// the rest of the transaction. Answering that with errFleetSkip re-selects
// the datastore the rollback just restored, forever — and because the boot
// pass runs before the listener, the process neither serves nor logs. The
// run must refuse the datastore and both versions instead.
//
// The run is bounded twice so a regression fails the suite rather than
// hanging it: the step stops the run after stepBound invocations, and the
// context deadline is the second net. The unfixed runner ran its step 9775
// times inside the three-second deadline of the throwaway repro.
func TestFleetRunRefusesAStepThatWritesTheCatalogueRow(t *testing.T) {
	const stepBound = 4
	errStepBound := errors.New("the step was invoked past the bound: the runner is looping")
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds := fleetTestDatastore(t, db, eng, "contacts")
			pushBehind(t, eng, ds, 0)

			next := fleetNextBuild(t, db, "", 1)
			var calls atomic.Int64
			next.fleetSteps[0] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
				if calls.Add(1) > stepBound {
					return errStepBound
				}

				return tx.Model(&datastoreModel{}).Where("id = ?", got.ID).Update("schema_version", 99).Error
			}

			runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			n, err := next.MigrateFleet(runCtx)

			if got := calls.Load(); got != 1 {
				t.Errorf("the step ran %d times, want 1: the runner re-selected the datastore it had just rolled back", got)
			}
			wantFleetRefusal(t, "MigrateFleet", err,
				fmt.Sprintf("%s is at schema version 99 after the step from version 0 wrote the catalogue row", ds.ID))
			if n != 0 {
				t.Errorf("MigrateFleet reported %d datastores migrated, want 0: the refused step is not progress", n)
			}
			if got := versionOf(t, eng, ds); got != 0 {
				t.Errorf("version after the refusal = %d, want 0: the step's write was rolled back with its stamp", got)
			}
		})
	}
}

// The table prefix reaches the step: the Table a step is handed is the
// physical name under database.table_prefix, so a step composing SQL from it
// touches the right table on a prefixed install. SQLite only: the PostgreSQL
// opener re-arms bare table names.
func TestMigrateFleetHandsTheStepThePrefixedTableName(t *testing.T) {
	db := openHandle(t, "sqlite", filepath.Join(t.TempDir(), "prefixed.db"), "kflow_")
	if err := database.Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	eng, err := NewEngine(db, "kflow_")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ctx := context.Background()
	ds := fleetTestDatastore(t, db, eng, "contacts")
	mustInsertRow(t, eng, ds, map[string]any{"title": "Alpha"})

	next := fleetNextBuild(t, db, "kflow_", 2)
	var seen Datastore
	next.fleetSteps[1] = func(ctx context.Context, tx *gorm.DB, got Datastore) error {
		seen = got
		dialect := tx.Dialector.Name()
		title := quoteIdent(dialect, "title")
		return tx.Exec("UPDATE " + quoteIdent(dialect, got.Table) + " SET " + title + " = upper(" + title + ")").Error
	}
	n, err := next.MigrateFleet(ctx)
	if err != nil || n != 1 {
		t.Fatalf("MigrateFleet = %d, %v, want 1, nil", n, err)
	}
	if want := PhysicalTableName("kflow_", ds.Surrogate); seen.Table != want || !strings.HasPrefix(seen.Table, "kflow_") {
		t.Errorf("the step saw table %q, want %q", seen.Table, want)
	}

	page, err := next.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
	if err != nil || len(page.Rows) != 1 || asString(t, page.Rows[0]["title"]) != "ALPHA" {
		t.Fatalf("List after the migration = %v, %v, want the one backfilled row", page.Rows, err)
	}
	got, err := next.GetDatastore(ctx, "tenant-1", ds.ID)
	if err != nil {
		t.Fatalf("GetDatastore: %v", err)
	}
	if got.SchemaVersion != 2 {
		t.Errorf("catalogue version = %d, want 2", got.SchemaVersion)
	}
}

// An engine that was never configured answers the two entry points with the
// same refusal ListDatastores gives, rather than a nil dereference.
func TestMigrateFleetAndFleetStatusRefuseAnUnconfiguredEngine(t *testing.T) {
	ctx := context.Background()
	for name, eng := range map[string]*Engine{"nil": nil, "no database": {}} {
		_, err := eng.MigrateFleet(ctx)
		wantFleetRefusal(t, name+" MigrateFleet", err, "datastore: engine is not configured")
		_, err = eng.FleetStatus(ctx)
		wantFleetRefusal(t, name+" FleetStatus", err, "datastore: engine is not configured")
	}
}
