package datastore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslab/kilas-flow/internal/database"
)

// widenSQLitePool lifts the SQLite handle off the production single-connection
// pin so the concurrency suite can prove the store's guarantees under a real
// multi-connection pool. Production pinning in database.Open is untouched.
func widenSQLitePool(t *testing.T, db *database.DB, n int) {
	t.Helper()
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("access underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(n)
	sqlDB.SetMaxIdleConns(n)
}

// concurrencyDrivers is testDrivers widened: SQLite at pool widths 1, 4 and
// 10, plus PostgreSQL when a live server is configured. The store's
// guarantees must not depend on the single-connection accident that hides
// races today.
func concurrencyDrivers() []testDriver {
	base := testDrivers()
	sqlite := base[0]
	drivers := make([]testDriver, 0, len(base)+2)
	for _, width := range []int{1, 4, 10} {
		n := width
		drivers = append(drivers, testDriver{
			name: fmt.Sprintf("sqlite-pool-%d", n),
			open: func(t *testing.T, prefix string) (*database.DB, *Engine) {
				db, eng := sqlite.open(t, prefix)
				widenSQLitePool(t, db, n)
				return db, eng
			},
		})
	}
	return append(drivers, base[1:]...)
}

// captureTableSQL records every SQL statement GORM sends that mentions the
// physical table, at the dialector callback level rather than through the
// engine's own composeHook, so a single-statement claim is proven on the
// wire and not only by the code that composed it. The getter is safe to
// call while writers run.
func captureTableSQL(t *testing.T, db *database.DB, table string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var captured []string
	record := func(tx *gorm.DB) {
		if tx.Statement == nil {
			return
		}
		statement := tx.Statement.SQL.String()
		if statement == "" || !strings.Contains(statement, table) {
			return
		}
		mu.Lock()
		captured = append(captured, statement)
		mu.Unlock()
	}
	if err := db.Callback().Query().After("gorm:query").Register("kf_capture_query", record); err != nil {
		t.Fatalf("register query capture: %v", err)
	}
	if err := db.Callback().Row().After("gorm:row").Register("kf_capture_row", record); err != nil {
		t.Fatalf("register row capture: %v", err)
	}
	if err := db.Callback().Raw().After("gorm:raw").Register("kf_capture_raw", record); err != nil {
		t.Fatalf("register raw capture: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove("kf_capture_query")
		_ = db.Callback().Row().Remove("kf_capture_row")
		_ = db.Callback().Raw().Remove("kf_capture_raw")
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), captured...)
	}
}

// Ten workers incrementing one counter land every write: the increment is a
// single UPDATE per call, atomic per row on both drivers, so no read in the
// middle can be lost no matter how the workers interleave.
func TestConcurrentIncrementsLoseNoWrites(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "counter", []ColumnInput{{Name: "n", Type: "number"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			inserted, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": 0.0})
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			id := inserted["id"].(int64)
			filter := &Filter{Type: "and", Conditions: []FilterCondition{
				{Column: "id", Condition: CondEq, Value: float64(id)},
			}}

			const workers, perWorker = 10, 10
			errs := make(chan error, workers*perWorker)
			var group sync.WaitGroup
			for w := 0; w < workers; w++ {
				group.Add(1)
				go func() {
					defer group.Done()
					for i := 0; i < perWorker; i++ {
						if _, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "n", 1); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			group.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("Increment: %v", err)
			}
			got, err := eng.Get(ctx, "tenant-1", ds.ID, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got["n"] != float64(workers*perWorker) {
				t.Errorf("counter = %v, want exactly %d: a write was lost", got["n"], workers*perWorker)
			}
		})
	}
}

// Incrementing a NULL cell starts from delta, the statement is one UPDATE
// with no preceding SELECT, and anything but a number column is refused.
func TestIncrementSemantics(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()
	ds, err := eng.Create(ctx, "tenant-1", "meters", []ColumnInput{
		{Name: "n", Type: "number"},
		{Name: "label", Type: "string"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"label": "empty counter"})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	id := row["id"].(int64)
	filter := &Filter{Type: "and", Conditions: []FilterCondition{
		{Column: "id", Condition: CondEq, Value: float64(id)},
	}}

	captured := captureTableSQL(t, db, ds.Table)
	var composed []string
	eng.composeHook = func(statement string) { composed = append(composed, statement) }
	res, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "n", 5)
	if err != nil {
		t.Fatalf("Increment: %v", err)
	}
	if res.Matched != 1 || len(res.Rows) != 1 || res.Rows[0]["n"] != 5.0 {
		t.Errorf("Increment = %+v, want one row at exactly 5", res)
	}
	updates := 0
	for _, statement := range composed {
		upper := strings.ToUpper(statement)
		if strings.HasPrefix(upper, "UPDATE") {
			updates++
			if !strings.Contains(upper, "COALESCE") {
				t.Errorf("increment statement %q has no null-tolerant add", statement)
			}
		}
		if strings.HasPrefix(upper, "SELECT") && strings.Contains(upper, "FOR UPDATE") {
			t.Errorf("statement %q takes a row lock", statement)
		}
	}
	if updates != 1 {
		t.Errorf("increment emitted %d UPDATEs, want the single atomic one", updates)
	}
	// The 2026-09-06 Evidence claimed "exactly one UPDATE with no preceding
	// SELECT"; that was false (the call read, wrote, then read again). Pin
	// the whole statement count, on the wire as well as in the engine.
	if len(composed) != 1 {
		t.Errorf("increment composed %d statements, want exactly one: %v", len(composed), composed)
	}
	if wire := captured(); len(wire) != 1 {
		t.Errorf("increment sent %d statements against the table, want exactly one: %v", len(wire), wire)
	} else if !strings.Contains(strings.ToUpper(wire[0]), "COALESCE") {
		t.Errorf("increment statement %q has no null-tolerant add", wire[0])
	}
	if _, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "label", 1); err == nil {
		t.Error("Increment(string column) = nil, want a refusal")
	}
	// Case-insensitive like every other column resolution.
	if _, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "N", 2); err != nil {
		t.Errorf("Increment(\"N\") = %v, want case-insensitive resolution", err)
	}
	got, err := eng.Get(ctx, "tenant-1", ds.ID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got["n"] != 7.0 {
		t.Errorf("counter = %v, want 7 after the two increments", got["n"])
	}
}

// A preconditioned update lands on a fresh stamp and refuses a stale one
// with the current stamp attached, so the caller retries without a second
// read. A row deleted underneath is the empty result, not a conflict.
func TestPreconditionedUpdate(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "flags", []ColumnInput{{Name: "on", Type: "boolean"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"on": false})
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			id := row["id"].(int64)
			filter := &Filter{Type: "and", Conditions: []FilterCondition{
				{Column: "id", Condition: CondEq, Value: float64(id)},
			}}
			stamp := row["updatedAt"].(time.Time)

			fresh, err := eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"on": true}, stamp)
			if err != nil {
				t.Fatalf("UpdateWithPrecondition(fresh) = %v, want success", err)
			}
			if fresh.Matched != 1 || len(fresh.Rows) != 1 || fresh.Rows[0]["on"] != true {
				t.Errorf("fresh update = %+v, want the single rewritten row", fresh)
			}
			moved, err := eng.Get(ctx, "tenant-1", ds.ID, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			_, err = eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"on": false}, stamp)
			if !errors.Is(err, ErrPreconditionConflict) {
				t.Fatalf("UpdateWithPrecondition(stale) = %v, want the conflict", err)
			}
			var conflict *PreconditionError
			if !errors.As(err, &conflict) {
				t.Fatalf("conflict %v carries no current stamp", err)
			}
			if !conflict.Current.Equal(moved["updatedAt"].(time.Time)) {
				t.Errorf("conflict current = %v, want the row's %v", conflict.Current, moved["updatedAt"])
			}
			// The refused write changed nothing.
			kept, err := eng.Get(ctx, "tenant-1", ds.ID, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if kept["on"] != true {
				t.Errorf("row = %v after refused write, want the winner's value", kept)
			}

			// A filter matching two rows is an error rather than a partial
			// conditional write.
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"on": false}); err != nil {
				t.Fatalf("Insert second: %v", err)
			}
			_, err = eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, nil, map[string]any{"on": false}, stamp)
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Errorf("multi-row preconditioned update = %v, want the single-row refusal", err)
			}

			// Deleted underneath: the empty result, because there is nothing
			// left to be stale about.
			if _, err := eng.Delete(ctx, "tenant-1", ds.ID, filter, false); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			empty, err := eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"on": false}, stamp)
			if err != nil || empty.Matched != 0 {
				t.Errorf("update after delete = (%+v, %v), want the empty result", empty, err)
			}
		})
	}
}

// The delete variant mirrors the update one: fresh stamp deletes, stale
// stamp refuses with the current stamp and the row survives.
func TestPreconditionedDelete(t *testing.T) {
	_, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()
	ds, err := eng.Create(ctx, "tenant-1", "gone", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"title": "doomed"})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	id := row["id"].(int64)
	filter := &Filter{Type: "and", Conditions: []FilterCondition{
		{Column: "id", Condition: CondEq, Value: float64(id)},
	}}

	_, err = eng.DeleteWithPrecondition(ctx, "tenant-1", ds.ID, filter, time.Now().UTC().Add(-time.Hour))
	if !errors.Is(err, ErrPreconditionConflict) {
		t.Fatalf("DeleteWithPrecondition(stale) = %v, want the conflict", err)
	}
	if _, err := eng.Get(ctx, "tenant-1", ds.ID, id); err != nil {
		t.Errorf("Get after refused delete = %v, want the surviving row", err)
	}
	deleted, err := eng.DeleteWithPrecondition(ctx, "tenant-1", ds.ID, filter, row["updatedAt"].(time.Time))
	if err != nil {
		t.Fatalf("DeleteWithPrecondition(fresh) = %v, want success", err)
	}
	if deleted.Deleted != 1 {
		t.Errorf("deleted = %+v, want the one row", deleted)
	}
}

// No datastore statement may believe in a row lock it never receives: the
// SQLite dialector discards clause.Locking without an error, so any write
// path built on it would be locked on PostgreSQL and unlocked on SQLite
// with identical source. This pins the absence on both sides — our emitted
// SQL and the dialector itself.
func TestDatastoreWritesTakeNoRowLocks(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()
	ds, err := eng.Create(ctx, "tenant-1", "locked", []ColumnInput{{Name: "n", Type: "number"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": 1.0})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	filter := &Filter{Type: "and", Conditions: []FilterCondition{
		{Column: "id", Condition: CondEq, Value: row["id"].(int64)},
	}}

	var composed []string
	eng.composeHook = func(statement string) { composed = append(composed, statement) }
	if _, err := eng.Update(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": 2.0}, false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "n", 1); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	current, err := eng.Get(ctx, "tenant-1", ds.ID, row["id"].(int64))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": 5.0}, current["updatedAt"].(time.Time)); err != nil {
		t.Fatalf("UpdateWithPrecondition: %v", err)
	}
	if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, row["id"].(int64), map[string]any{"n": 6.0}, false); err != nil {
		t.Fatalf("UpsertByID: %v", err)
	}
	if _, err := eng.Delete(ctx, "tenant-1", ds.ID, filter, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, statement := range composed {
		if strings.Contains(strings.ToUpper(statement), "FOR UPDATE") {
			t.Errorf("datastore emitted a locking read: %q", statement)
		}
	}

	// And the dialector itself: a locking clause through the SQLite driver
	// compiles to SQL without the lock, which is why the paths above must
	// never ask for one.
	var found []map[string]any
	locked := db.Session(&gorm.Session{DryRun: true}).
		Table("probe").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&found)
	if locked.Error != nil {
		t.Fatalf("DryRun locking read: %v", locked.Error)
	}
	if sql := locked.Statement.SQL.String(); strings.Contains(strings.ToUpper(sql), "FOR UPDATE") {
		t.Errorf("SQLite dialector kept the lock: %q", sql)
	}

	// Positive control that never needs a server: the PostgreSQL dialector
	// DOES emit the lock, so the SQLite absence above is a dialector
	// behaviour and not an assertion that forgot what it measures.
	pg, err := gorm.Open(postgres.New(postgres.Config{
		DSN: "host=127.0.0.1 port=1 user=x dbname=x sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open postgres dialector: %v", err)
	}
	var pgFound []map[string]any
	pgLocked := pg.Session(&gorm.Session{DryRun: true}).
		Table("probe").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&pgFound)
	if pgLocked.Error != nil {
		t.Fatalf("DryRun postgres locking read: %v", pgLocked.Error)
	}
	if sql := pgLocked.Statement.SQL.String(); !strings.Contains(strings.ToUpper(sql), "FOR UPDATE") {
		t.Errorf("postgres dialector dropped the lock too: %q (the SQLite absence would be vacuous)", sql)
	}
}

// Insert must return the row the call wrote. At HEAD on SQLite it read the
// id with a second checkout (SELECT last_insert_rowid()) while another
// worker's INSERT had already moved the connection's last rowid, so the
// readback belonged to a neighbour.
func TestConcurrentInsertsReturnTheirOwnRows(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "inserts", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()

			const workers, perWorker = 10, 20
			var group sync.WaitGroup
			var mu sync.Mutex
			ids := map[int64]bool{}
			errs := make(chan error, workers*perWorker)
			for w := 0; w < workers; w++ {
				group.Add(1)
				go func(w int) {
					defer group.Done()
					for i := 0; i < perWorker; i++ {
						want := float64(w*perWorker + i + 1)
						row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": want})
						if err != nil {
							errs <- err
							return
						}
						if got, ok := row["n"].(float64); !ok || got != want {
							errs <- fmt.Errorf("insert returned n=%v, want the value it wrote (%v)", row["n"], want)
							return
						}
						id, ok := row["id"].(int64)
						if !ok {
							errs <- fmt.Errorf("insert returned id %#v, want an integer", row["id"])
							return
						}
						mu.Lock()
						if ids[id] {
							mu.Unlock()
							errs <- fmt.Errorf("id %d returned by two inserts", id)
							return
						}
						ids[id] = true
						mu.Unlock()
					}
				}(w)
			}
			group.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("Insert: %v", err)
			}
			count, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			if count != workers*perWorker {
				t.Errorf("table holds %d rows, want %d", count, workers*perWorker)
			}
		})
	}
}

// A read-then-preconditioned-write loop must lose nothing. At HEAD the
// millisecond stamp repeated within one millisecond, so a stale stamp
// matched a later write and the loop finished short of its target.
func TestPreconditionedWritesLoseNoUpdateUnderContention(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "cas", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()
			row := mustInsertRow(t, eng, ds, map[string]any{"n": 0.0})
			id := row["id"].(int64)
			filter := idFilter(id)

			const workers, perWorker = 10, 10
			var group sync.WaitGroup
			errs := make(chan error, workers)
			for w := 0; w < workers; w++ {
				group.Add(1)
				go func() {
					defer group.Done()
					for i := 0; i < perWorker; i++ {
						for {
							current, err := eng.Get(ctx, "tenant-1", ds.ID, id)
							if err != nil {
								errs <- err
								return
							}
							n := current["n"].(float64)
							_, err = eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter,
								map[string]any{"n": n + 1}, current["updatedAt"].(time.Time))
							if err == nil {
								break
							}
							if errors.Is(err, ErrPreconditionConflict) {
								continue
							}
							errs <- err
							return
						}
					}
				}()
			}
			group.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("preconditioned write: %v", err)
			}
			got, err := eng.Get(ctx, "tenant-1", ds.ID, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got["n"] != float64(workers*perWorker) {
				t.Errorf("counter = %v, want exactly %d: an update was lost", got["n"], workers*perWorker)
			}
		})
	}
}

// updatedAt is the precondition's whole basis, so it must move on every
// write. At HEAD two back-to-back writes landed in the same millisecond and
// the stamp repeated, which is what let a stale precondition through.
func TestUpdatedAtStrictlyIncreasesOnEveryWrite(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "stamps", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()
			row := mustInsertRow(t, eng, ds, map[string]any{"n": 0.0})
			id := row["id"].(int64)
			filter := idFilter(id)

			stampOf := func(res *UpdateResult) time.Time {
				t.Helper()
				if res == nil || len(res.Rows) != 1 {
					t.Fatalf("write returned %+v, want one row", res)
				}
				stamp, ok := res.Rows[0]["updatedAt"].(time.Time)
				if !ok {
					t.Fatalf("after-image updatedAt = %#v, want a time", res.Rows[0]["updatedAt"])
				}
				return stamp
			}
			previous := row["updatedAt"].(time.Time)
			for i := 0; i < 200; i++ {
				var current time.Time
				switch i % 3 {
				case 0:
					res, err := eng.Update(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": float64(i)}, false)
					if err != nil {
						t.Fatalf("Update %d: %v", i, err)
					}
					current = stampOf(res)
				case 1:
					res, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "n", 1)
					if err != nil {
						t.Fatalf("Increment %d: %v", i, err)
					}
					current = stampOf(res)
				case 2:
					res, err := eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": float64(i)}, previous)
					if err != nil {
						t.Fatalf("UpdateWithPrecondition %d: %v", i, err)
					}
					current = stampOf(res)
				}
				if !current.After(previous) {
					t.Fatalf("write %d stamped %v, not after the previous %v", i, current, previous)
				}
				previous = current
			}
		})
	}
}

// A write that lands between the read and the conditional write must make
// the stale stamp lose, even when the two writes share a millisecond.
func TestStalePreconditionAfterAnInterleavedWriteIsRefused(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "stale", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()
			row := mustInsertRow(t, eng, ds, map[string]any{"n": 0.0})
			id := row["id"].(int64)
			filter := idFilter(id)

			for round := 0; round < 50; round++ {
				read, err := eng.Get(ctx, "tenant-1", ds.ID, id)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				stale := read["updatedAt"].(time.Time)
				if _, err := eng.Update(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": float64(round + 1)}, false); err != nil {
					t.Fatalf("interleaved Update: %v", err)
				}
				_, err = eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter, map[string]any{"n": float64(999)}, stale)
				if !errors.Is(err, ErrPreconditionConflict) {
					t.Fatalf("round %d: stale precondition = %v, want the conflict (stamp %v)", round, err, stale)
				}
				var conflict *PreconditionError
				if !errors.As(err, &conflict) {
					t.Fatalf("conflict %v carries no current stamp", err)
				}
				winner, err := eng.Get(ctx, "tenant-1", ds.ID, id)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if !conflict.Current.Equal(winner["updatedAt"].(time.Time)) {
					t.Errorf("round %d: conflict current = %v, want the row's %v", round, conflict.Current, winner["updatedAt"])
				}
				if winner["n"] != float64(round+1) {
					t.Errorf("round %d: row = %v, want the winner's value %d", round, winner["n"], round+1)
				}
			}
		})
	}
}

// Each increment call must return the post-image its own statement left.
// At HEAD the after-image came from a separate read, so a racing writer's
// later value could come back instead of the caller's own.
func TestConcurrentIncrementsReturnEachWritersOwnValue(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "owninc", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()
			row := mustInsertRow(t, eng, ds, map[string]any{"n": 0.0})
			id := row["id"].(int64)
			filter := idFilter(id)

			const workers = 10
			results := make([]float64, workers)
			var group sync.WaitGroup
			errs := make(chan error, workers)
			for w := 0; w < workers; w++ {
				group.Add(1)
				go func(w int) {
					defer group.Done()
					res, err := eng.Increment(ctx, "tenant-1", ds.ID, filter, "n", 1)
					if err != nil {
						errs <- err
						return
					}
					if res.Matched != 1 || len(res.Rows) != 1 {
						errs <- fmt.Errorf("increment returned %+v, want one row", res)
						return
					}
					results[w] = res.Rows[0]["n"].(float64)
				}(w)
			}
			group.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("Increment: %v", err)
			}
			sort.Float64s(results)
			for i, got := range results {
				if got != float64(i+1) {
					t.Fatalf("increment post-images sorted = %v, want 1..%d each once", results, workers)
				}
			}
		})
	}
}

// hookStampTwin arms the engine's compose hook so that the first conditional
// write statement composed inserts a second row matching the caller's filter,
// the instant the statement is composed — that is, after the pre-read that
// approved one row and before the statement runs. The twin copies the
// approved row's createdAt and updatedAt verbatim, so it carries the very
// stamp the caller passed as ifUpdatedAt: on SQLite the physical default is
// second-precision CURRENT_TIMESTAMP and the PostgreSQL CAS compares
// date_trunc('milliseconds', ...), so two rows inserted in the same window
// share a stamp anyway — copying it makes the collision deterministic rather
// than a race on the clock. The returned pointer holds the twin's id once the
// write has run (0 if the hook never fired).
func hookStampTwin(t *testing.T, eng *Engine, ds *Datastore, approvedID int64, value float64) *int64 {
	t.Helper()
	dialect := eng.dialect()
	twin := new(int64)
	fired := false
	eng.composeHook = func(statement string) {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if fired || !(strings.HasPrefix(upper, "UPDATE") || strings.HasPrefix(upper, "DELETE")) {
			return
		}
		fired = true
		insert := "INSERT INTO " + quoteIdent(dialect, ds.Table) +
			" (" + quoteIdent(dialect, "n") + "," + quoteIdent(dialect, "createdAt") + "," + quoteIdent(dialect, "updatedAt") + ")" +
			" SELECT ?, " + quoteIdent(dialect, "createdAt") + "," + quoteIdent(dialect, "updatedAt") +
			" FROM " + quoteIdent(dialect, ds.Table) +
			" WHERE " + quoteIdent(dialect, "id") + " = ? RETURNING " + quoteIdent(dialect, "id")
		if err := eng.db.Raw(insert, value, approvedID).Scan(twin).Error; err != nil {
			t.Fatalf("insert the stamp twin: %v", err)
		}
	}
	return twin
}

// A preconditioned write is addressed by the row its pre-read approved, not
// by the caller's filter. A second row that matches the filter and carries
// the same stamp when the statement runs must be left alone, and the caller
// must be told exactly what was written — never "nothing matched" after the
// statement mutated both rows.
func TestPreconditionedWriteTouchesOnlyTheApprovedRow(t *testing.T) {
	filter := &Filter{Type: "and", Conditions: []FilterCondition{{Column: "n", Condition: CondEq, Value: 1.0}}}
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			t.Run("update", func(t *testing.T) {
				eng, ds := mustCreateRowStore(t, drv, "cas-scope-update", []ColumnInput{{Name: "n", Type: "number"}})
				ctx := context.Background()
				approved := mustInsertRow(t, eng, ds, map[string]any{"n": 1.0})
				approvedID := approved["id"].(int64)
				twin := hookStampTwin(t, eng, ds, approvedID, 1.0)

				res, err := eng.UpdateWithPrecondition(ctx, "tenant-1", ds.ID, filter,
					map[string]any{"n": 42.0}, approved["updatedAt"].(time.Time))
				eng.composeHook = nil
				if err != nil {
					t.Fatalf("UpdateWithPrecondition = %v, want the approved row written", err)
				}
				if *twin == 0 {
					t.Fatal("the conditional statement was never composed: the hook saw no UPDATE")
				}
				if res.Matched != 1 || len(res.Rows) != 1 {
					t.Errorf("result = %+v, want exactly the one row the statement touched", res)
				}
				got, err := eng.Get(ctx, "tenant-1", ds.ID, approvedID)
				if err != nil {
					t.Fatalf("Get approved: %v", err)
				}
				if got["n"] != 42.0 {
					t.Errorf("approved row n = %v, want 42", got["n"])
				}
				neighbour, err := eng.Get(ctx, "tenant-1", ds.ID, *twin)
				if err != nil {
					t.Fatalf("Get twin: %v", err)
				}
				if neighbour["n"] != 1.0 {
					t.Errorf("neighbour row n = %v, want its own 1: the filter widened the write", neighbour["n"])
				}
				if !neighbour["updatedAt"].(time.Time).Equal(approved["updatedAt"].(time.Time)) {
					t.Errorf("neighbour row stamped %v, want its original %v", neighbour["updatedAt"], approved["updatedAt"])
				}
				page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				rewritten := 0
				for _, row := range page.Rows {
					if row["n"] == 42.0 {
						rewritten++
					}
				}
				if rewritten != 1 {
					t.Errorf("%d rows carry n=42, want exactly the approved one", rewritten)
				}
			})

			t.Run("delete", func(t *testing.T) {
				eng, ds := mustCreateRowStore(t, drv, "cas-scope-delete", []ColumnInput{{Name: "n", Type: "number"}})
				ctx := context.Background()
				approved := mustInsertRow(t, eng, ds, map[string]any{"n": 1.0})
				approvedID := approved["id"].(int64)
				twin := hookStampTwin(t, eng, ds, approvedID, 1.0)

				res, err := eng.DeleteWithPrecondition(ctx, "tenant-1", ds.ID, filter, approved["updatedAt"].(time.Time))
				eng.composeHook = nil
				if err != nil {
					t.Fatalf("DeleteWithPrecondition = %v, want the approved row deleted", err)
				}
				if *twin == 0 {
					t.Fatal("the conditional statement was never composed: the hook saw no DELETE")
				}
				if res.Deleted != 1 {
					t.Errorf("deleted = %d, want 1", res.Deleted)
				}
				if _, err := eng.Get(ctx, "tenant-1", ds.ID, *twin); err != nil {
					t.Errorf("neighbour row = %v, want it to survive: the filter widened the delete", err)
				}
				page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(page.Rows) != 1 || page.Rows[0]["id"] != *twin {
					t.Errorf("table holds %+v, want only the neighbour row %d", page.Rows, *twin)
				}
			})
		})
	}
}

// Insert must return the row its own statement wrote. A write that lands
// after the INSERT and before any readback must not come back as the
// inserted row: the compose hook performs exactly that neighbour write the
// instant a post-insert read is composed, which is the whole gap the old
// read-back shape left open.
func TestInsertReturnsTheStatementOwnImage(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "insert-image", []ColumnInput{{Name: "n", Type: "number"}})
			ctx := context.Background()
			dialect := eng.dialect()

			var composed []string
			inserted, neighbourWrote := false, false
			eng.composeHook = func(statement string) {
				composed = append(composed, statement)
				upper := strings.ToUpper(strings.TrimSpace(statement))
				switch {
				case strings.HasPrefix(upper, "INSERT"):
					inserted = true
				case inserted && !neighbourWrote && strings.HasPrefix(upper, "SELECT") && strings.Contains(statement, ds.Table):
					neighbourWrote = true
					if err := eng.db.Exec("UPDATE " + quoteIdent(dialect, ds.Table) +
						" SET " + quoteIdent(dialect, "n") + " = 99").Error; err != nil {
						t.Fatalf("neighbour write: %v", err)
					}
				}
			}
			row, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": 1.0})
			eng.composeHook = nil
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if got := row["n"]; got != 1.0 {
				t.Errorf("Insert returned n=%v, want the 1 it wrote: a neighbour's write came back in place of the inserted row", got)
			}
			if stamp, ok := row["updatedAt"].(time.Time); !ok || stamp.IsZero() {
				t.Errorf("Insert returned updatedAt %#v, want the timestamp the database set on the row it wrote", row["updatedAt"])
			}
			inserts := 0
			for _, statement := range composed {
				if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "INSERT") {
					continue
				}
				inserts++
				if !strings.Contains(strings.ToUpper(statement), "RETURNING") {
					t.Errorf("insert statement %q has no RETURNING", statement)
				}
			}
			if inserts != 1 {
				t.Errorf("Insert composed %d INSERT statements, want exactly one: %v", inserts, composed)
			}
			if neighbourWrote {
				t.Error("the engine read the row back after the INSERT: a neighbour's write can come back as the inserted row")
			}
		})
	}
}
