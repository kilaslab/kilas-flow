package datastore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Ten workers incrementing one counter land every write: the increment is a
// single UPDATE per call, atomic per row on both drivers, so no read in the
// middle can be lost no matter how the workers interleave.
func TestConcurrentIncrementsLoseNoWrites(t *testing.T) {
	for _, drv := range testDrivers() {
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
	_, eng := testDrivers()[0].open(t, "")
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
	for _, drv := range testDrivers() {
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
}
