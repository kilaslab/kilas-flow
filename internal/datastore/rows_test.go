package datastore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Helpers shared by the row-store tests. Engine-test helpers (open,
// testDrivers, trackPhysical, asBool/asTime) are reused, not redefined.

// mustCreateRowStore builds one datastore for row tests and registers its
// physical cleanup.
func mustCreateRowStore(t *testing.T, drv testDriver, name string, cols []ColumnInput) (*Engine, *Datastore) {
	t.Helper()
	db, eng := drv.open(t, "")
	_ = db
	ds, err := eng.Create(context.Background(), "tenant-1", name, cols)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, eng.db, ds.Table)
	return eng, ds
}

// mustInsertRow inserts one row or fails the test.
func mustInsertRow(t *testing.T, eng *Engine, ds *Datastore, values map[string]any) Row {
	t.Helper()
	row, err := eng.Insert(context.Background(), "tenant-1", ds.ID, values)
	if err != nil {
		t.Fatalf("Insert %v: %v", values, err)
	}
	return row
}

// dumpRowSet renders a row set canonically for byte-identical comparisons:
// one line per row in id order, keys sorted, values %v-formatted.
func dumpRowSet(rows []Row) string {
	ids := make([]int64, 0, len(rows))
	byID := map[int64]Row{}
	for _, row := range rows {
		id := row["id"].(int64)
		ids = append(ids, id)
		byID[id] = row
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for _, id := range ids {
		row := byID[id]
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "id=%d", id)
		for _, k := range keys {
			if k == "id" {
				continue
			}
			fmt.Fprintf(&b, " %s=%v", k, row[k])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// snapshotTable returns the count and canonical dump of the whole table.
func snapshotTable(t *testing.T, eng *Engine, ds *Datastore) (int64, string) {
	t.Helper()
	page, err := eng.List(context.Background(), "tenant-1", ds.ID, RowQuery{ReturnAll: true})
	if err != nil {
		t.Fatalf("List ReturnAll: %v", err)
	}
	return int64(len(page.Rows)), dumpRowSet(page.Rows)
}

// Every filter operator resolves through one Go switch to a compile-time
// fragment, and an unrecognised operator returns an error naming the
// supported set.
func TestFilterOperatorsResolveThroughOneSwitch(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		fragment, placeholders, err := conditionFragment(dialect, "contains")
		if err == nil {
			t.Fatalf("%s: unknown operator = %q, want refusal", dialect, fragment)
		}
		for _, want := range []string{"eq", "neq", "like", "ilike", "gt", "gte", "lt", "lte", "isEmpty", "isNotEmpty"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not name %q", dialect, err, want)
			}
		}
		if placeholders != 0 {
			t.Errorf("%s: refusal carries %d placeholders, want 0", dialect, placeholders)
		}
	}
	// The LIKE pair is dialect-explicit: case-sensitive like is GLOB on
	// SQLite and LIKE on PostgreSQL; insensitive ilike is LIKE on SQLite
	// and ILIKE on PostgreSQL.
	cases := []struct {
		dialect   string
		cond      Condition
		fragment  string
		placehold int
	}{
		{"sqlite", CondEq, "= ?", 1},
		{"postgres", CondEq, "= ?", 1},
		{"sqlite", CondNeq, "<> ?", 1},
		{"sqlite", CondLike, "GLOB ?", 1},
		{"postgres", CondLike, "LIKE ?", 1},
		{"sqlite", CondILike, "LIKE ?", 1},
		{"postgres", CondILike, "ILIKE ?", 1},
		{"sqlite", CondGt, "> ?", 1},
		{"postgres", CondGte, ">= ?", 1},
		{"sqlite", CondLt, "< ?", 1},
		{"postgres", CondLte, "<= ?", 1},
		{"sqlite", CondIsEmpty, "IS EMPTY", 0},
		{"postgres", CondIsNotEmpty, "IS NOT EMPTY", 0},
	}
	for _, c := range cases {
		got, n, err := conditionFragment(c.dialect, c.cond)
		if err != nil {
			t.Errorf("%s %s: %v", c.dialect, c.cond, err)
			continue
		}
		if got != c.fragment || n != c.placehold {
			t.Errorf("%s %s = (%q, %d), want (%q, %d)", c.dialect, c.cond, got, n, c.fragment, c.placehold)
		}
	}
}

// The node speaks keyName/condition/keyValue while the service speaks
// columnName/condition/value: the importer maps between the two, and Must
// Match Any/All becomes the or/and envelope.
func TestNodeFilterMapping(t *testing.T) {
	got, err := NodeConditionsToFilter("Any Condition", []NodeFilterCondition{
		{KeyName: "title", Condition: CondEq, KeyValue: "hello"},
	})
	if err != nil {
		t.Fatalf("Any Condition: %v", err)
	}
	if got.Type != "or" {
		t.Errorf("Any Condition type = %q, want or", got.Type)
	}
	if len(got.Conditions) != 1 || got.Conditions[0].Column != "title" || got.Conditions[0].Value != "hello" {
		t.Errorf("node condition mapped to %+v, want columnName=title value=hello", got.Conditions)
	}
	got, err = NodeConditionsToFilter("All Conditions", []NodeFilterCondition{
		{KeyName: "score", Condition: CondGt, KeyValue: 3.0},
	})
	if err != nil {
		t.Fatalf("All Conditions: %v", err)
	}
	if got.Type != "and" {
		t.Errorf("All Conditions type = %q, want and", got.Type)
	}
	if _, err := NodeConditionsToFilter("Sometimes", nil); err == nil {
		t.Error("unknown match mode = nil, want refusal")
	}
}

// The keyset cursor helpers share one versioned format: a cursor issued by
// today's encoder still decodes afterwards, pinned by recorded literals.
func TestRowCursorRoundTripAndLiteral(t *testing.T) {
	if got, err := decodeRowCursor("cm93LXYxADE"); err != nil || got != 1 {
		t.Errorf("recorded literal decodes to (%d, %v), want (1, nil)", got, err)
	}
	if got, err := decodeRowCursor("cm93LXYxADQy"); err != nil || got != 42 {
		t.Errorf("recorded literal decodes to (%d, %v), want (42, nil)", got, err)
	}
	for _, id := range []int64{1, 2, 42, 1000000} {
		got, err := decodeRowCursor(encodeRowCursor(id))
		if err != nil || got != id {
			t.Errorf("round trip %d = (%d, %v)", id, got, err)
		}
	}
	for _, bad := range []string{"", "!!!", "cm93LXYxADE=", "e30"} {
		if _, err := decodeRowCursor(bad); !errors.Is(err, ErrInvalidRowCursor) {
			t.Errorf("cursor %q error = %v, want ErrInvalidRowCursor", bad, err)
		}
	}
}

// Insert, Get and List round-trip Go types on both drivers.
func TestRowInsertGetListRoundTrip(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "contacts", fullDefinition)
			row := mustInsertRow(t, eng, ds, map[string]any{
				"title": "hello", "score": 1.5, "flag": true,
			})
			if row["id"] != int64(1) {
				t.Errorf("inserted id = %#v, want int64(1)", row["id"])
			}
			got, err := eng.Get(ctx, "tenant-1", ds.ID, 1)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got["title"] != "hello" {
				t.Errorf("title = %#v, want hello", got["title"])
			}
			if got["score"] != 1.5 {
				t.Errorf("score = %#v, want 1.5", got["score"])
			}
			if got["flag"] != true {
				t.Errorf("flag = %#v, want Go true", got["flag"])
			}
			if _, err := eng.Get(ctx, "tenant-1", ds.ID, 999); !errors.Is(err, ErrRowNotFound) {
				t.Errorf("Get missing = %v, want ErrRowNotFound", err)
			}
			page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page.Rows) != 1 || page.NextCursor != "" {
				t.Errorf("ReturnAll = %d rows cursor %q, want 1 row no cursor", len(page.Rows), page.NextCursor)
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// A row inserted while a client pages cannot shift rows onto a page that
// client has already read: keyset on the id alone.
func TestKeysetPaginationStableUnderInterleavedInsert(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "queue", []ColumnInput{{Name: "title", Type: "string"}})
			for i := 1; i <= 5; i++ {
				mustInsertRow(t, eng, ds, map[string]any{"title": fmt.Sprintf("row-%d", i)})
			}
			first, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{Limit: 2})
			if err != nil {
				t.Fatalf("page 1: %v", err)
			}
			if len(first.Rows) != 2 || first.NextCursor == "" {
				t.Fatalf("page 1 = %d rows cursor %q, want 2 rows and a cursor", len(first.Rows), first.NextCursor)
			}
			seen := map[int64]bool{first.Rows[0]["id"].(int64): true, first.Rows[1]["id"].(int64): true}
			mustInsertRow(t, eng, ds, map[string]any{"title": "row-6"})
			cursor := first.NextCursor
			for {
				page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{Cursor: cursor, Limit: 2})
				if err != nil {
					t.Fatalf("page after %q: %v", cursor, err)
				}
				for _, row := range page.Rows {
					id := row["id"].(int64)
					if seen[id] {
						t.Fatalf("row %d repeated after interleaved insert", id)
					}
					seen[id] = true
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if len(seen) != 6 {
				t.Errorf("visited %d rows, want 6 (5 + interleaved)", len(seen))
			}
			for id := int64(1); id <= 6; id++ {
				if !seen[id] {
					t.Errorf("row %d never visited", id)
				}
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// The same like/ilike filter over one fixture returns the same rows on
// both drivers. Case-sensitive like matches the lowercase row only;
// insensitive ilike matches all three capitalisations.
func TestLikeAndILikeAgreeAcrossDrivers(t *testing.T) {
	titles := []string{"Apple", "apple", "APPLE", "Banana", "apricot"}
	run := func(t *testing.T, drv testDriver) (likeIDs, ilikeIDs []int64) {
		ctx := context.Background()
		eng, ds := mustCreateRowStore(t, drv, "fruit", []ColumnInput{{Name: "title", Type: "string"}})
		for _, title := range titles {
			mustInsertRow(t, eng, ds, map[string]any{"title": title})
		}
		like, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true,
			Filter: &Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondLike, Value: "%app%"}}}})
		if err != nil {
			t.Fatalf("like: %v", err)
		}
		ilike, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true,
			Filter: &Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondILike, Value: "%app%"}}}})
		if err != nil {
			t.Fatalf("ilike: %v", err)
		}
		for _, row := range like.Rows {
			likeIDs = append(likeIDs, row["id"].(int64))
		}
		for _, row := range ilike.Rows {
			ilikeIDs = append(ilikeIDs, row["id"].(int64))
		}
		if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		return likeIDs, ilikeIDs
	}
	results := map[string][2][]int64{}
	drivers := testDrivers()
	for _, drv := range drivers {
		t.Run(drv.name, func(t *testing.T) {
			likeIDs, ilikeIDs := run(t, drv)
			results[drv.name] = [2][]int64{likeIDs, ilikeIDs}
			// "apple" is id 2; the three app* rows are ids 1-3.
			if len(likeIDs) != 1 || likeIDs[0] != 2 {
				t.Errorf("like ids = %v, want [2]", likeIDs)
			}
			if len(ilikeIDs) != 3 || ilikeIDs[0] != 1 || ilikeIDs[1] != 2 || ilikeIDs[2] != 3 {
				t.Errorf("ilike ids = %v, want [1 2 3]", ilikeIDs)
			}
		})
	}
	if len(drivers) == 2 && (fmt.Sprint(results["sqlite"]) != fmt.Sprint(results["postgres"])) {
		t.Errorf("drivers disagree: sqlite %v postgres %v", results["sqlite"], results["postgres"])
	}
	if len(drivers) == 1 {
		t.Logf("only sqlite ran; rerun with KILASFLOW_TEST_POSTGRES_DSN for the PostgreSQL half")
	}
}

// Dry run on update, upsert and delete returns paired before/after rows
// tagged DryRunState and leaves the table byte-identical: counts and value
// checksums match either side.
func TestDryRunLeavesTableByteIdentical(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "ledger", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			mustInsertRow(t, eng, ds, map[string]any{"title": "a", "score": 1.0})
			mustInsertRow(t, eng, ds, map[string]any{"title": "b", "score": 2.0})
			mustInsertRow(t, eng, ds, map[string]any{"title": "c", "score": 3.0})
			countBefore, dumpBefore := snapshotTable(t, eng, ds)

			matching := &Filter{Type: "and", Conditions: []FilterCondition{{Column: "score", Condition: CondGte, Value: 2.0}}}

			updated, err := eng.Update(ctx, "tenant-1", ds.ID, matching, map[string]any{"score": 99.0}, true)
			if err != nil {
				t.Fatalf("dry-run update: %v", err)
			}
			if updated.Matched != 2 || len(updated.Pairs) != 2 {
				t.Fatalf("dry-run update matched %d with %d pairs, want 2 and 2", updated.Matched, len(updated.Pairs))
			}
			for _, pair := range updated.Pairs {
				if pair.Before[DryRunState] != "before" || pair.After[DryRunState] != "after" {
					t.Fatalf("dry-run pair tags = %v/%v, want before/after", pair.Before[DryRunState], pair.After[DryRunState])
				}
				if pair.After["score"] != 99.0 {
					t.Fatalf("dry-run after score = %v, want 99", pair.After["score"])
				}
			}

			upserted, err := eng.Upsert(ctx, "tenant-1", ds.ID, matching, map[string]any{"score": 7.0}, true)
			if err != nil {
				t.Fatalf("dry-run upsert (update path): %v", err)
			}
			if upserted.Inserted || len(upserted.Pairs) != 2 {
				t.Fatalf("dry-run upsert = inserted %v pairs %d, want false and 2", upserted.Inserted, len(upserted.Pairs))
			}
			insertUpsert, err := eng.Upsert(ctx, "tenant-1", ds.ID,
				&Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondEq, Value: "zzz"}}},
				map[string]any{"score": 4.0}, true)
			if err != nil {
				t.Fatalf("dry-run upsert (insert path): %v", err)
			}
			if !insertUpsert.Inserted || len(insertUpsert.Pairs) != 1 || insertUpsert.Pairs[0].Before != nil {
				t.Fatalf("dry-run insert upsert = %+v, want one pair with nil Before", insertUpsert)
			}

			deleted, err := eng.Delete(ctx, "tenant-1", ds.ID, matching, true)
			if err != nil {
				t.Fatalf("dry-run delete: %v", err)
			}
			if deleted.Deleted != 2 || len(deleted.Pairs) != 2 {
				t.Fatalf("dry-run delete removed %d with %d pairs, want 2 and 2", deleted.Deleted, len(deleted.Pairs))
			}

			if countAfter, dumpAfter := snapshotTable(t, eng, ds); countAfter != countBefore || dumpAfter != dumpBefore {
				t.Errorf("table changed across dry runs:\nbefore %d rows:\n%s\nafter %d rows:\n%s",
					countBefore, dumpBefore, countAfter, dumpAfter)
			}

			// The committed paths still work after the rehearsals.
			if _, err := eng.Update(ctx, "tenant-1", ds.ID, matching, map[string]any{"score": 99.0}, false); err != nil {
				t.Fatalf("committed update: %v", err)
			}
			del, err := eng.Delete(ctx, "tenant-1", ds.ID, matching, false)
			if err != nil {
				t.Fatalf("committed delete: %v", err)
			}
			if del.Deleted != 2 {
				t.Errorf("committed delete removed %d, want 2", del.Deleted)
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// A filter naming a column absent from the catalogue is rejected before any
// SQL text is built: no statement reaches the driver.
func TestUnknownFilterColumnRejectedBeforeSQL(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			var emitted []string
			eng.composeHook = func(statement string) { emitted = append(emitted, statement) }
			_, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{
				Filter: &Filter{Type: "and", Conditions: []FilterCondition{{Column: "nope", Condition: CondEq, Value: "x"}}},
			})
			if err == nil || !strings.Contains(err.Error(), `"nope"`) {
				t.Errorf("unknown column error = %v, want it naming nope", err)
			}
			if len(emitted) != 0 {
				t.Errorf("%d statements reached the driver after rejection: %v", len(emitted), emitted)
			}
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"nope": "x"}); err == nil {
				t.Error("insert with unknown column = nil, want refusal")
			}
			if len(emitted) != 0 {
				t.Errorf("%d statements reached the driver after insert rejection", len(emitted))
			}
			eng.composeHook = nil
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// Filter values reach the database only as bound placeholders: the
// generated statement text contains no byte of the supplied value, even
// when the value carries quotes, percents and underscores.
func TestFilterValuesBindAsPlaceholders(t *testing.T) {
	const sneaky = `O'Brien%x_"; DROP TABLE --`
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			mustInsertRow(t, eng, ds, map[string]any{"title": "plain"})
			var emitted []string
			eng.composeHook = func(statement string) { emitted = append(emitted, statement) }
			_, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{
				Filter: &Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondEq, Value: sneaky}}},
			})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(emitted) == 0 {
				t.Fatal("no statement composed")
			}
			for _, statement := range emitted {
				if strings.Contains(statement, sneaky) {
					t.Errorf("statement text carries the value bytes: %s", statement)
				}
				if !strings.Contains(statement, "?") {
					t.Errorf("statement has no placeholder: %s", statement)
				}
			}
			eng.composeHook = nil
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// ReturnAll returns every matching row with no next cursor; a bounded list
// clamps to the maximum and pages to the end.
func TestReturnAllAndBoundedPaging(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "bulk", []ColumnInput{{Name: "title", Type: "string"}})
			for i := range 7 {
				mustInsertRow(t, eng, ds, map[string]any{"title": fmt.Sprintf("r%d", i)})
			}
			all, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("ReturnAll: %v", err)
			}
			if len(all.Rows) != 7 || all.NextCursor != "" {
				t.Errorf("ReturnAll = %d rows cursor %q, want 7 and none", len(all.Rows), all.NextCursor)
			}
			var ids []int64
			cursor := ""
			for {
				page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{Cursor: cursor, Limit: 3})
				if err != nil {
					t.Fatalf("page: %v", err)
				}
				for _, row := range page.Rows {
					ids = append(ids, row["id"].(int64))
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if len(ids) != 7 {
				t.Errorf("paged visit = %d rows, want 7", len(ids))
			}
			huge, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{Limit: 1000000})
			if err != nil {
				t.Fatalf("clamped list: %v", err)
			}
			if len(huge.Rows) != 7 || huge.NextCursor != "" {
				t.Errorf("clamped list = %d rows cursor %q, want 7 and none", len(huge.Rows), huge.NextCursor)
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// Upsert inserts on a miss — carrying the filter's eq keys into the new
// row — and updates on a hit; Clear empties the table without resetting
// the id sequence.
func TestUpsertAndClear(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "up", []ColumnInput{
				{Name: "title", Type: "string"},
				{Name: "score", Type: "number"},
			})
			miss := &Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondEq, Value: "fresh"}}}
			res, err := eng.Upsert(ctx, "tenant-1", ds.ID, miss, map[string]any{"score": 1.0}, false)
			if err != nil {
				t.Fatalf("upsert miss: %v", err)
			}
			if !res.Inserted || len(res.Rows) != 1 || res.Rows[0]["title"] != "fresh" {
				t.Errorf("upsert miss = %+v, want one inserted row carrying title=fresh", res)
			}
			res, err = eng.Upsert(ctx, "tenant-1", ds.ID, miss, map[string]any{"score": 2.0}, false)
			if err != nil {
				t.Fatalf("upsert hit: %v", err)
			}
			if res.Inserted || res.Matched != 1 || res.Rows[0]["score"] != 2.0 {
				t.Errorf("upsert hit = %+v, want update of 1 row to score=2", res)
			}
			if _, err := eng.Upsert(ctx, "tenant-1", ds.ID, nil, map[string]any{"score": 3.0}, false); err == nil {
				t.Error("upsert without filter = nil, want refusal")
			}
			cleared, err := eng.Clear(ctx, "tenant-1", ds.ID)
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if cleared != 1 {
				t.Errorf("Clear removed %d, want 1", cleared)
			}
			after := mustInsertRow(t, eng, ds, map[string]any{"title": "again"})
			if after["id"] != int64(2) {
				t.Errorf("post-clear id = %#v, want int64(2): the sequence is not reset", after["id"])
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// Row operations on a datastore at a version the binary does not know
// refuse with an error naming both versions — ahead or behind — while
// every other datastore keeps serving.
func TestRowOpsRefuseUnknownVersions(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()
	a, err := eng.Create(ctx, "tenant-1", "a", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	trackPhysical(t, db, a.Table)
	b, err := eng.Create(ctx, "tenant-1", "b", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	trackPhysical(t, db, b.Table)

	pushVersion := func(ds *Datastore, v int) {
		t.Helper()
		if err := db.Exec(`UPDATE datastores SET schema_version = ? WHERE id = ?`, v, ds.ID).Error; err != nil {
			t.Fatalf("push %s to %d: %v", ds.ID, v, err)
		}
	}
	tryOps := func(ds *Datastore) []error {
		_, e1 := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"title": "x"})
		_, e2 := eng.Get(ctx, "tenant-1", ds.ID, 1)
		_, e3 := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
		_, e4 := eng.Update(ctx, "tenant-1", ds.ID, nil, map[string]any{"title": "x"}, false)
		_, e5 := eng.Delete(ctx, "tenant-1", ds.ID, nil, false)
		_, e6 := eng.Upsert(ctx, "tenant-1", ds.ID,
			&Filter{Type: "and", Conditions: []FilterCondition{{Column: "title", Condition: CondEq, Value: "x"}}},
			map[string]any{"title": "x"}, false)
		_, e7 := eng.Clear(ctx, "tenant-1", ds.ID)
		return []error{e1, e2, e3, e4, e5, e6, e7}
	}

	pushVersion(a, CurrentSchemaVersion+98)
	for i, err := range tryOps(a) {
		if err == nil {
			t.Fatalf("op %d on ahead datastore = nil, want refusal", i)
		}
		if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), "1") {
			t.Errorf("op %d refusal %q does not name both versions", i, err)
		}
	}
	// The sibling keeps serving while the ahead one refuses.
	if _, err := eng.Insert(ctx, "tenant-1", b.ID, map[string]any{"title": "ok"}); err != nil {
		t.Errorf("sibling insert while ahead present: %v", err)
	}

	pushVersion(a, 0)
	for i, err := range tryOps(a) {
		if err == nil {
			t.Fatalf("op %d on behind datastore = nil, want refusal", i)
		}
		if !strings.Contains(err.Error(), "0") || !strings.Contains(err.Error(), "1") {
			t.Errorf("op %d refusal %q does not name both versions", i, err)
		}
	}

	pushVersion(a, CurrentSchemaVersion)
	if _, err := eng.Insert(ctx, "tenant-1", a.ID, map[string]any{"title": "back"}); err != nil {
		t.Errorf("insert after restore: %v", err)
	}
	if err := eng.Drop(ctx, "tenant-1", a.ID); err != nil {
		t.Fatalf("Drop a: %v", err)
	}
	if err := eng.Drop(ctx, "tenant-1", b.ID); err != nil {
		t.Fatalf("Drop b: %v", err)
	}
}
