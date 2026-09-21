package datastore

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// upsertTestColumns is the shape every id-upsert test defines: a string, a
// number and a left-NULL string, so an unsupplied column is observable.
func upsertTestColumns() []ColumnInput {
	return []ColumnInput{
		{Name: "title", Type: "string"},
		{Name: "score", Type: "number"},
		{Name: "note", Type: "string"},
	}
}

// The statement shape is assertable with no server: pure function, both
// dialects.
func TestUpsertByIDStatementShape(t *testing.T) {
	cols := []ColumnDef{{Name: "title", Type: ColumnString}, {Name: "score", Type: ColumnNumber}}
	bound := map[string]any{"title": "a", "score": 1.0}
	const table = "ds_0123456789abcdef"
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			q := func(name string) string { return quoteIdent(dialect, name) }
			statement, args := upsertByIDStatement(dialect, table, cols, bound, 50, 100)

			if !strings.Contains(statement, "ON CONFLICT ("+q("id")+") DO UPDATE") {
				t.Errorf("statement %q has no id conflict target", statement)
			}
			if !strings.Contains(statement, "WHERE EXISTS (SELECT 1 FROM") {
				t.Errorf("statement %q has no mandatory existence WHERE", statement)
			}
			if !strings.Contains(statement, "RETURNING") {
				t.Errorf("statement %q has no RETURNING", statement)
			}
			if !strings.Contains(statement, q(table)+"."+q("updatedAt")) {
				t.Errorf("statement %q does not qualify updatedAt with the table", statement)
			}
			if strings.Contains(strings.ToUpper(statement), "FOR UPDATE") {
				t.Errorf("statement %q takes a row lock", statement)
			}
			if trimmed := strings.ToUpper(strings.TrimSpace(statement)); strings.HasPrefix(trimmed, "SELECT") {
				t.Errorf("statement %q starts with SELECT", statement)
			}
			if len(args) != 5 {
				t.Fatalf("args = %v, want id, two values, the probe id and the offset", args)
			}
			if args[0] != int64(50) || args[3] != int64(50) || args[4] != 99 {
				t.Errorf("args = %v, want the id at 0 and 3 and maxRows-1 (99) at 4", args)
			}

			switch dialect {
			case "sqlite":
				if !strings.HasPrefix(statement, "INSERT INTO") {
					t.Errorf("sqlite statement %q does not start with INSERT INTO", statement)
				}
				if strings.Contains(statement, "CAST(") || strings.Contains(statement, "setval") {
					t.Errorf("sqlite statement %q carries a PostgreSQL-only construct", statement)
				}
			case "postgres":
				if !strings.HasPrefix(statement, "WITH up AS (") {
					t.Errorf("postgres statement %q is not wrapped in the sequence CTE", statement)
				}
				for _, cast := range []string{"CAST(? AS BIGINT)", "CAST(? AS TEXT)", "CAST(? AS DOUBLE PRECISION)"} {
					if !strings.Contains(statement, cast) {
						t.Errorf("postgres statement %q has no %s on its SELECT placeholder", statement, cast)
					}
				}
				if !strings.Contains(statement, "setval(") || !strings.Contains(statement, "pg_get_serial_sequence(") {
					t.Errorf("postgres statement %q does not re-sync the sequence", statement)
				}
			}
		})
	}
}

// An id-addressed upsert sends exactly one statement to the database, the
// engine agrees, and that statement both carries ON CONFLICT and never waits
// on a SELECT of its own.
func TestUpsertByIDIsOneStatement(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "upid", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			trackPhysical(t, db, ds.Table)

			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			captured := captureTableSQL(t, db, ds.Table)

			assertSingle := func(label string, from int) {
				t.Helper()
				if len(composed) != from+1 {
					t.Errorf("%s: engine composed %d statements, want exactly 1: %v", label, len(composed)-from, composed[from:])
				}
				wire := captured()
				if len(wire) != from+1 {
					t.Errorf("%s: the database saw %d statements against the table, want exactly 1: %v", label, len(wire)-from, wire[from:])
				}
				if len(composed) <= from {
					return
				}
				statement := composed[from]
				upper := strings.ToUpper(statement)
				if !strings.Contains(upper, "ON CONFLICT") {
					t.Errorf("%s: statement %q has no ON CONFLICT", label, statement)
				}
				if strings.HasPrefix(strings.TrimSpace(upper), "SELECT") {
					t.Errorf("%s: statement %q is a read", label, statement)
				}
				if strings.Contains(upper, "FOR UPDATE") {
					t.Errorf("%s: statement %q takes a row lock", label, statement)
				}
			}

			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 50, map[string]any{"title": "a", "score": 1.0}, false); err != nil {
				t.Fatalf("UpsertByID create: %v", err)
			}
			assertSingle("create", 0)
			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 50, map[string]any{"title": "b"}, false); err != nil {
				t.Fatalf("UpsertByID update: %v", err)
			}
			assertSingle("update", 1)
		})
	}
}

// A create lands at exactly the id with unsupplied columns NULL; the next
// call updates in place, changes only supplied columns, keeps createdAt and
// moves updatedAt strictly later even back-to-back.
func TestUpsertByIDCreatesAtTheIdAndUpdatesInPlace(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidrow", upsertTestColumns())
			ctx := context.Background()

			created, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 50, map[string]any{"title": "a", "score": 1.0}, false)
			if err != nil {
				t.Fatalf("UpsertByID create: %v", err)
			}
			if !created.Inserted || created.Matched != 1 || len(created.Rows) != 1 {
				t.Fatalf("create = %+v, want one inserted row", created)
			}
			row := created.Rows[0]
			if row["id"] != int64(50) {
				t.Errorf("id = %#v, want int64(50)", row["id"])
			}
			if row["title"] != "a" || row["score"] != 1.0 {
				t.Errorf("row = %v, want the supplied values", row)
			}
			if row["note"] != nil {
				t.Errorf("unsupplied column = %v, want NULL", row["note"])
			}
			createdAt := row["createdAt"].(time.Time)
			updatedAt := row["updatedAt"].(time.Time)

			updated, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 50, map[string]any{"title": "b"}, false)
			if err != nil {
				t.Fatalf("UpsertByID update: %v", err)
			}
			if updated.Inserted {
				t.Errorf("second upsert = %+v, want Inserted false", updated)
			}
			row2 := updated.Rows[0]
			if row2["title"] != "b" {
				t.Errorf("title = %v, want the updated b", row2["title"])
			}
			if row2["score"] != 1.0 {
				t.Errorf("score = %v, want the earlier 1.0 untouched", row2["score"])
			}
			if !row2["createdAt"].(time.Time).Equal(createdAt) {
				t.Errorf("createdAt = %v, want the original %v", row2["createdAt"], createdAt)
			}
			if !row2["updatedAt"].(time.Time).After(updatedAt) {
				t.Errorf("updatedAt = %v, want strictly after %v", row2["updatedAt"], updatedAt)
			}
		})
	}
}

// An explicit id must leave the auto-increment counter ahead of it, and
// creating a lower id afterwards must not drag the counter down.
func TestUpsertByIDKeepsAutoIncrementAhead(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidseq", []ColumnInput{{Name: "title", Type: "string"}})
			ctx := context.Background()

			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 50, map[string]any{"title": "fifty"}, false); err != nil {
				t.Fatalf("UpsertByID(50): %v", err)
			}
			next := mustInsertRow(t, eng, ds, map[string]any{"title": "next"})
			if next["id"] != int64(51) {
				t.Errorf("plain insert id = %#v, want int64(51): the counter follows the explicit id", next["id"])
			}
			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 3, map[string]any{"title": "three"}, false); err != nil {
				t.Fatalf("UpsertByID(3): %v", err)
			}
			after := mustInsertRow(t, eng, ds, map[string]any{"title": "after"})
			if after["id"] != int64(52) {
				t.Errorf("insert after id 3 = %#v, want int64(52): a lower explicit id does not move the counter", after["id"])
			}
		})
	}
}

// Ten goroutines racing one id must insert it exactly once; the losers take
// the DO UPDATE branch. No error, and no id inserted twice.
func TestConcurrentUpsertByIDInsertsOnce(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidrace", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			ctx := context.Background()

			const ids, racers = 20, 10
			var mu sync.Mutex
			inserted := map[int64]int{}
			var group sync.WaitGroup
			errs := make(chan error, ids*racers)
			for n := 0; n < ids; n++ {
				id := int64(n + 1)
				for r := 0; r < racers; r++ {
					group.Add(1)
					go func(id int64) {
						defer group.Done()
						res, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, id, map[string]any{"title": "racing"}, false)
						if err != nil {
							errs <- err
							return
						}
						if res.Inserted {
							mu.Lock()
							inserted[id]++
							mu.Unlock()
						}
					}(id)
				}
			}
			group.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("racing upsert: %v", err)
			}
			count, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			if count != ids {
				t.Errorf("table holds %d rows, want %d: an id was inserted twice", count, ids)
			}
			if len(inserted) != ids {
				t.Errorf("%d ids reported an insert, want %d", len(inserted), ids)
			}
			for id, n := range inserted {
				if n != 1 {
					t.Errorf("id %d reported Inserted true %d times, want exactly once", id, n)
				}
			}
		})
	}
}

// The single statement carries the row-limit gate: creating past the maximum
// is refused and writes nothing, while an update of an existing row at the
// maximum still lands.
func TestUpsertByIDHonoursTheRowLimit(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidlimit", []ColumnInput{{Name: "title", Type: "string"}})
			ctx := context.Background()
			limits := eng.CurrentLimits()
			limits.MaxRowsPerDatastore = 3
			if err := eng.SetLimits(limits); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			for id := int64(1); id <= 3; id++ {
				if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, id, map[string]any{"title": fmt.Sprintf("row-%d", id)}, false); err != nil {
					t.Fatalf("UpsertByID(%d): %v", id, err)
				}
			}
			_, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 4, map[string]any{"title": "fourth"}, false)
			if err == nil || !strings.Contains(err.Error(), "table holds 3 rows, at the maximum 3") {
				t.Fatalf("create past the limit = %v, want the row-limit refusal", err)
			}
			count, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			if count != 3 {
				t.Errorf("table holds %d rows after the refused create, want 3", count)
			}
			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 2, map[string]any{"title": "two"}, false); err != nil {
				t.Errorf("update of an existing row at the limit = %v, want success", err)
			}
		})
	}
}

// A caller-supplied id is an address into the auto-increment counter, so it
// is bounded: an id at MaxInt64 would exhaust the sequence and permanently
// break every later plain insert on both drivers. Out-of-range ids are
// refused before any SQL and create nothing.
func TestUpsertByIDRefusesUnsafeIDs(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidsafe", []ColumnInput{{Name: "title", Type: "string"}})
			ctx := context.Background()

			for _, id := range []int64{0, -1, 1 << 53, math.MaxInt64} {
				before, err := eng.countRows(ctx, ds.Table)
				if err != nil {
					t.Fatalf("countRows: %v", err)
				}
				_, err = eng.UpsertByID(ctx, "tenant-1", ds.ID, id, map[string]any{"title": "nope"}, false)
				if err == nil {
					t.Fatalf("UpsertByID(%d) = nil, want the bound refusal", id)
				}
				if !strings.Contains(err.Error(), "9007199254740991") {
					t.Errorf("refusal %q does not name the bound", err)
				}
				after, err := eng.countRows(ctx, ds.Table)
				if err != nil {
					t.Fatalf("countRows: %v", err)
				}
				if after != before {
					t.Fatalf("refused id %d created %d rows, want none", id, after-before)
				}
				if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"title": "ok"}); err != nil {
					t.Fatalf("plain insert after refusing id %d: %v", id, err)
				}
			}
			// The bound itself is accepted.
			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 9007199254740991, map[string]any{"title": "edge"}, false); err != nil {
				t.Fatalf("UpsertByID(2^53-1) = %v, want acceptance", err)
			}
			// A filter past the cap does not take the id path: the legacy
			// read-then-write refuses the reserved column name and creates
			// nothing.
			before, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			if _, err := eng.Upsert(ctx, "tenant-1", ds.ID, idFilter(math.MaxInt64), map[string]any{"title": "legacy"}, false); err == nil {
				t.Fatal("Engine.Upsert(id eq MaxInt64) = nil, want the reserved-name refusal")
			}
			after, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			if after != before {
				t.Errorf("legacy upsert created %d rows, want 0", after-before)
			}
		})
	}
}

// Every refusal happens before any SQL is bound and leaves the table empty.
func TestUpsertByIDRefusals(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidrefuse", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			ctx := context.Background()

			cases := []struct {
				name   string
				values map[string]any
			}{
				{"empty values", map[string]any{}},
				{"reserved id", map[string]any{"id": 5}},
				{"unknown column", map[string]any{"nope": 1.0}},
				{"type mismatch", map[string]any{"score": "not a number"}},
			}
			for _, tc := range cases {
				if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 1, tc.values, false); err == nil {
					t.Errorf("%s = nil, want a refusal", tc.name)
				}
			}
			limits := eng.CurrentLimits()
			limits.MaxValueBytes = 4
			if err := eng.SetLimits(limits); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			if _, err := eng.UpsertByID(ctx, "tenant-1", ds.ID, 1, map[string]any{"title": "too large"}, false); err == nil {
				t.Error("oversized value = nil, want the byte refusal")
			}
			if count, err := eng.countRows(ctx, ds.Table); err != nil || count != 0 {
				t.Errorf("refused upserts left %d rows (err %v), want 0", count, err)
			}
		})
	}
}

// The public Upsert routes a single `id eq` filter to the one statement and
// creates at exactly that id; a dry run reports the images without writing.
func TestUpsertOnAnIDFilterTakesTheSingleStatementPath(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidonid", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			ctx := context.Background()

			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			res, err := eng.Upsert(ctx, "tenant-1", ds.ID, idFilter(50), map[string]any{"title": "a"}, false)
			if err != nil {
				t.Fatalf("Upsert(id eq 50): %v", err)
			}
			if !res.Inserted || len(res.Rows) != 1 || res.Rows[0]["id"] != int64(50) {
				t.Errorf("Upsert(id eq 50) = %+v, want a create at id 50", res)
			}
			if len(composed) != 1 || !strings.Contains(strings.ToUpper(composed[0]), "ON CONFLICT") {
				t.Errorf("id-addressed upsert composed %v, want one ON CONFLICT statement", composed)
			} else if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(composed[0])), "SELECT") {
				t.Errorf("id-addressed upsert composed a read: %q", composed[0])
			}

			count, err := eng.countRows(ctx, ds.Table)
			if err != nil {
				t.Fatalf("countRows: %v", err)
			}
			// A missing id on a dry run reports the create without writing.
			dry, err := eng.Upsert(ctx, "tenant-1", ds.ID, idFilter(99), map[string]any{"title": "dry"}, true)
			if err != nil {
				t.Fatalf("dry run missing id: %v", err)
			}
			if !dry.Inserted || len(dry.Pairs) != 1 || dry.Pairs[0].Before != nil {
				t.Errorf("dry-run insert = %+v, want one pair with a nil Before", dry)
			}
			if dry.Pairs[0].After["id"] != int64(99) || dry.Pairs[0].After["title"] != "dry" {
				t.Errorf("dry-run after-image = %v, want id 99 carrying the values", dry.Pairs[0].After)
			}
			if after, err := eng.countRows(ctx, ds.Table); err != nil || after != count {
				t.Errorf("dry run wrote: %d -> %d (err %v), want %d", count, after, err, count)
			}

			// An existing id returns before/after pairs.
			dryHit, err := eng.Upsert(ctx, "tenant-1", ds.ID, idFilter(50), map[string]any{"title": "b"}, true)
			if err != nil {
				t.Fatalf("dry run hit: %v", err)
			}
			if dryHit.Inserted || len(dryHit.Pairs) != 1 || dryHit.Pairs[0].Before == nil || dryHit.Pairs[0].After == nil {
				t.Errorf("dry-run hit = %+v, want one before/after pair", dryHit)
			}
		})
	}
}

// The amendment is pinned: a filter-addressed upsert is NOT one statement,
// it reads before it writes, and that is recorded rather than papered over.
func TestFilterAddressedUpsertStaysReadThenWrite(t *testing.T) {
	for _, drv := range concurrencyDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			eng, ds := mustCreateRowStore(t, drv, "upidfilter", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			ctx := context.Background()
			mustInsertRow(t, eng, ds, map[string]any{"title": "fresh", "score": 0.0})

			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			filter := &Filter{Type: "and", Conditions: []FilterCondition{
				{Column: "title", Condition: CondEq, Value: "fresh"},
			}}
			if _, err := eng.Upsert(ctx, "tenant-1", ds.ID, filter, map[string]any{"score": 1.0}, false); err != nil {
				t.Fatalf("Upsert(title eq fresh): %v", err)
			}
			if len(composed) == 0 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(composed[0])), "SELECT") {
				t.Errorf("filter-addressed upsert composed %v, want a SELECT before the write", composed)
			}
			if len(composed) > 0 && strings.Contains(strings.ToUpper(composed[0]), "ON CONFLICT") {
				t.Errorf("filter-addressed upsert used ON CONFLICT: %q", composed[0])
			}
		})
	}
}
