package datastore

import (
	"context"
	"strings"
	"testing"
)

// Creating past the per-tenant ceiling fails naming the limit and the
// current count, while the datastores already held keep serving.
func TestDatastoreLimitEnforced(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			if err := eng.SetLimits(Limits{
				MaxDatastoresPerTenant: 2,
				MaxColumnsPerDatastore: 10,
				MaxRowsPerDatastore:    100,
				MaxValueBytes:          1 << 20,
			}); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			def := []ColumnInput{{Name: "title", Type: "string"}}
			if _, err := eng.Create(ctx, "tenant-1", "first", def); err != nil {
				t.Fatalf("Create first: %v", err)
			}
			if _, err := eng.Create(ctx, "tenant-1", "second", def); err != nil {
				t.Fatalf("Create second: %v", err)
			}
			_, err := eng.Create(ctx, "tenant-1", "third", def)
			if err == nil || !strings.Contains(err.Error(), "maximum 2") || !strings.Contains(err.Error(), "2 datastores") {
				t.Fatalf("Create third = %v, want the refusal naming the limit and the count", err)
			}
			// The ceiling is per tenant: a neighbour still creates.
			if _, err := eng.Create(ctx, "tenant-2", "theirs", def); err != nil {
				t.Errorf("Create(neighbour) = %v, want success under a per-tenant ceiling", err)
			}
			if listed, err := eng.ListDatastores(ctx, "tenant-1"); err != nil || len(listed) != 2 {
				t.Errorf("ListDatastores = (%d, %v), want the 2 held datastores", len(listed), err)
			}
		})
	}
}

// Adding a column past the ceiling leaves the catalogue and the physical
// table untouched: no DDL is emitted and the definition still holds the old
// columns only.
func TestColumnLimitLeavesTableUntouched(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			if err := eng.SetLimits(Limits{
				MaxDatastoresPerTenant: 10,
				MaxColumnsPerDatastore: 2,
				MaxRowsPerDatastore:    100,
				MaxValueBytes:          1 << 20,
			}); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			ds, err := eng.Create(ctx, "tenant-1", "narrow", []ColumnInput{
				{Name: "one", Type: "string"},
				{Name: "two", Type: "string"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			err = eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "three", Type: "string"})
			if err == nil || !strings.Contains(err.Error(), "maximum 2") {
				t.Fatalf("AddColumn = %v, want the refusal naming the limit", err)
			}
			for _, statement := range composed {
				if strings.Contains(strings.ToUpper(statement), "ALTER") {
					t.Errorf("emitted %q after a refused AddColumn, want no DDL", statement)
				}
			}
			stored, err := eng.GetDatastore(ctx, "tenant-1", ds.ID)
			if err != nil {
				t.Fatalf("GetDatastore: %v", err)
			}
			if len(stored.Columns) != 2 {
				t.Errorf("columns = %d, want the 2 held ones", len(stored.Columns))
			}
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"three": "x"}); err == nil {
				t.Error("Insert(new column) succeeded after a refused AddColumn, want unknown column")
			}
		})
	}
}

// A single value past the byte bound is refused before any SQL is bound:
// the statement hook sees nothing, not even the row probe, and the table
// stays empty.
func TestValueBytesRefusedBeforeSQL(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			if err := eng.SetLimits(Limits{
				MaxDatastoresPerTenant: 10,
				MaxColumnsPerDatastore: 10,
				MaxRowsPerDatastore:    100,
				MaxValueBytes:          16,
			}); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			ds, err := eng.Create(ctx, "tenant-1", "small", []ColumnInput{{Name: "note", Type: "string"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			_, err = eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"note": strings.Repeat("x", 17)})
			if err == nil || !strings.Contains(err.Error(), "maximum 16") {
				t.Fatalf("Insert = %v, want the refusal naming the byte bound", err)
			}
			if len(composed) != 0 {
				t.Errorf("emitted %d statements for a refused write, want none", len(composed))
			}
			if usage, err := eng.Usage(ctx, "tenant-1", ds.ID); err != nil || usage.Rows != 0 {
				t.Errorf("Usage = (%+v, %v), want zero rows", usage, err)
			}
			// Exactly at the bound still writes.
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"note": strings.Repeat("x", 16)}); err != nil {
				t.Errorf("Insert(at bound) = %v, want success", err)
			}
		})
	}
}

// Writing past the row maximum fails while every existing row stays
// readable and writable: the breach refuses and deletes nothing.
func TestRowLimitKeepsExistingRows(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			if err := eng.SetLimits(Limits{
				MaxDatastoresPerTenant: 10,
				MaxColumnsPerDatastore: 10,
				MaxRowsPerDatastore:    3,
				MaxValueBytes:          1 << 20,
			}); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			ds, err := eng.Create(ctx, "tenant-1", "capped", []ColumnInput{{Name: "n", Type: "number"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			for i := 1; i <= 3; i++ {
				if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": float64(i)}); err != nil {
					t.Fatalf("Insert %d: %v", i, err)
				}
			}
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": 4.0}); err == nil ||
				!strings.Contains(err.Error(), "maximum 3") {
				t.Fatalf("Insert past max = %v, want the refusal naming the limit", err)
			}
			page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page.Rows) != 3 {
				t.Fatalf("rows after refusal = %d, want the 3 held ones", len(page.Rows))
			}
			// Writable too: an update on a held row still lands.
			updated, err := eng.Update(ctx, "tenant-1", ds.ID,
				&Filter{Type: "and", Conditions: []FilterCondition{{Column: "n", Condition: CondEq, Value: 1.0}}},
				map[string]any{"n": 10.0}, false)
			if err != nil {
				t.Fatalf("Update(held row) = %v, want success at the ceiling", err)
			}
			if len(updated.Rows) != 1 || updated.Rows[0]["n"] != 10.0 {
				t.Errorf("Update rows = %v, want the rewritten row", updated.Rows)
			}
		})
	}
}

// The row ceiling is answered by a bounded existence probe, never a COUNT(*)
// over the whole table: on SQLite every datastore operation shares one
// connection, and scanning a full table to answer "are there too many rows"
// would hold it for the scan's duration. A wall-clock assertion would be
// flaky by nature, so this pins the construction instead — the probe stops
// at the limit-th row by LIMIT 1 OFFSET, whatever the table holds past it.
func TestRowLimitUsesABoundedProbe(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			_, eng := drv.open(t, "")
			ctx := context.Background()
			if err := eng.SetLimits(Limits{
				MaxDatastoresPerTenant: 10,
				MaxColumnsPerDatastore: 10,
				MaxRowsPerDatastore:    3,
				MaxValueBytes:          1 << 20,
			}); err != nil {
				t.Fatalf("SetLimits: %v", err)
			}
			ds, err := eng.Create(ctx, "tenant-1", "probed", []ColumnInput{{Name: "n", Type: "number"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			for i := 1; i <= 3; i++ {
				if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": float64(i)}); err != nil {
					t.Fatalf("Insert %d: %v", i, err)
				}
			}
			var composed []string
			eng.composeHook = func(statement string) { composed = append(composed, statement) }
			if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"n": 4.0}); err == nil {
				t.Fatal("Insert past max succeeded, want the refusal")
			}
			var probed bool
			for _, statement := range composed {
				upper := strings.ToUpper(statement)
				if strings.Contains(upper, "COUNT(*)") {
					t.Errorf("row check scanned: %q, want the bounded probe", statement)
				}
				if strings.Contains(upper, "LIMIT 1 OFFSET") {
					probed = true
				}
			}
			if !probed {
				t.Errorf("no bounded probe in %q, want SELECT 1 .. LIMIT 1 OFFSET", composed)
			}
		})
	}
}

// Bounds must be positive: zero or negative would refuse every write, an
// outage shaped like a configuration, so SetLimits rejects it with the
// offending field named.
func TestSetLimitsRejectsNonPositive(t *testing.T) {
	good := DefaultLimits()
	cases := map[string]Limits{
		"datastores zero":     {MaxDatastoresPerTenant: 0, MaxColumnsPerDatastore: good.MaxColumnsPerDatastore, MaxRowsPerDatastore: good.MaxRowsPerDatastore, MaxValueBytes: good.MaxValueBytes},
		"datastores negative": {MaxDatastoresPerTenant: -1, MaxColumnsPerDatastore: good.MaxColumnsPerDatastore, MaxRowsPerDatastore: good.MaxRowsPerDatastore, MaxValueBytes: good.MaxValueBytes},
		"columns zero":        {MaxDatastoresPerTenant: good.MaxDatastoresPerTenant, MaxColumnsPerDatastore: 0, MaxRowsPerDatastore: good.MaxRowsPerDatastore, MaxValueBytes: good.MaxValueBytes},
		"rows zero":           {MaxDatastoresPerTenant: good.MaxDatastoresPerTenant, MaxColumnsPerDatastore: good.MaxColumnsPerDatastore, MaxRowsPerDatastore: 0, MaxValueBytes: good.MaxValueBytes},
		"bytes negative":      {MaxDatastoresPerTenant: good.MaxDatastoresPerTenant, MaxColumnsPerDatastore: good.MaxColumnsPerDatastore, MaxRowsPerDatastore: good.MaxRowsPerDatastore, MaxValueBytes: -100},
	}
	sqlite := testDrivers()[0]
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			_, eng := sqlite.open(t, "")
			if err := eng.SetLimits(limits); err == nil {
				t.Errorf("SetLimits(%+v) = nil, want a refusal", limits)
			}
		})
	}
	_, eng := sqlite.open(t, "")
	if err := eng.SetLimits(good); err != nil {
		t.Errorf("SetLimits(defaults) = %v, want success", err)
	}
	if eng.CurrentLimits() != good {
		t.Errorf("CurrentLimits = %+v, want %+v", eng.CurrentLimits(), good)
	}
}

// Usage reports exact rows on every driver and an explicit unknown for
// bytes where the driver cannot answer — never a zero that reads as empty.
func TestUsageObservable(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			ds, err := eng.Create(ctx, "tenant-1", "metered", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			for _, title := range []string{"a", "b"} {
				if _, err := eng.Insert(ctx, "tenant-1", ds.ID, map[string]any{"title": title}); err != nil {
					t.Fatalf("Insert: %v", err)
				}
			}
			usage, err := eng.Usage(ctx, "tenant-1", ds.ID)
			if err != nil {
				t.Fatalf("Usage: %v", err)
			}
			if usage.Rows != 2 || usage.DatastoreID != ds.ID {
				t.Errorf("Usage = %+v, want 2 rows for this datastore", usage)
			}
			if db.Dialector.Name() == "postgres" {
				if !usage.SizeKnown || usage.SizeBytes <= 0 {
					t.Errorf("Usage = %+v, want an exact positive byte figure on PostgreSQL", usage)
				}
			} else if usage.SizeKnown || usage.SizeBytes != 0 {
				t.Errorf("Usage = %+v, want explicit unavailability on SQLite, never a zero that reads as empty", usage)
			}
		})
	}
}
