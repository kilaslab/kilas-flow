package datastore

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Adding a column is one statement on both drivers and existing rows read
// the new column back as null; a row written afterwards carries a value.
func TestAddColumnReadsBackNullForExistingRows(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "contacts", []ColumnInput{{Name: "title", Type: "string"}})
			mustInsertRow(t, eng, ds, map[string]any{"title": "old"})
			if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: "nick", Type: "string"}); err != nil {
				t.Fatalf("AddColumn: %v", err)
			}
			got, err := eng.Get(ctx, "tenant-1", ds.ID, 1)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got["nick"] != nil {
				t.Errorf("new column on old row = %#v, want null", got["nick"])
			}
			fresh := mustInsertRow(t, eng, ds, map[string]any{"title": "new", "nick": "nick-value"})
			if fresh["nick"] != "nick-value" {
				t.Errorf("new column on new row = %#v, want nick-value", fresh["nick"])
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// Renaming a column preserves every stored value and the catalogue's
// integer index ordering: the full row set reads back either side.
func TestRenamePreservesValuesAndOrdering(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "contacts", []ColumnInput{
				{Name: "alpha", Type: "string"},
				{Name: "beta", Type: "number"},
			})
			mustInsertRow(t, eng, ds, map[string]any{"alpha": "a1", "beta": 1.0})
			mustInsertRow(t, eng, ds, map[string]any{"alpha": "a2", "beta": 2.0})
			if err := eng.RenameColumn(ctx, "tenant-1", ds.ID, "alpha", "renamed"); err != nil {
				t.Fatalf("RenameColumn: %v", err)
			}
			page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page.Rows) != 2 || page.Rows[0]["renamed"] != "a1" || page.Rows[1]["renamed"] != "a2" {
				t.Errorf("values after rename = %v, want a1/a2 under renamed", page.Rows)
			}
			if _, ok := page.Rows[0]["alpha"]; ok {
				t.Errorf("old name survives in output: %v", page.Rows[0])
			}
			_, cols, err := eng.lookup(ctx, "tenant-1", ds.ID)
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if len(cols) != 2 || cols[0].Name != "renamed" || cols[0].Position != 0 || cols[1].Name != "beta" || cols[1].Position != 1 {
				t.Errorf("catalogue order after rename = %+v, want [renamed@0 beta@1]", cols)
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}

// Live evolution: concurrent readers keep working while columns are added.
// Add column is a single ALTER TABLE ... ADD statement on both drivers — an
// online schema-text edit, never a table rebuild — so readers queue briefly
// at worst and every read succeeds with a complete row set.
func TestAddColumnDoesNotStallConcurrentReaders(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			ctx := context.Background()
			eng, ds := mustCreateRowStore(t, drv, "live", []ColumnInput{{Name: "title", Type: "string"}})
			const seedRows = 20
			for i := range seedRows {
				mustInsertRow(t, eng, ds, map[string]any{"title": fmt.Sprintf("row-%d", i)})
			}

			const readers = 8
			const readsEach = 25
			var failures atomic.Int64
			var wrongCounts atomic.Int64
			start := make(chan struct{})
			var wg sync.WaitGroup
			for r := 0; r < readers; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for range readsEach {
						page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
						if err != nil {
							failures.Add(1)
							return
						}
						if len(page.Rows) != seedRows {
							wrongCounts.Add(1)
							return
						}
					}
				}()
			}
			close(start)
			const added = 5
			for i := range added {
				if err := eng.AddColumn(ctx, "tenant-1", ds.ID, ColumnInput{Name: fmt.Sprintf("extra%d", i), Type: "string"}); err != nil {
					t.Fatalf("AddColumn %d: %v", i, err)
				}
			}
			wg.Wait()
			if failures.Load() != 0 {
				t.Errorf("%d reader failures during evolution", failures.Load())
			}
			if wrongCounts.Load() != 0 {
				t.Errorf("%d reads saw a partial row set during evolution", wrongCounts.Load())
			}

			// The evolved schema serves old and new rows alike.
			page, err := eng.List(ctx, "tenant-1", ds.ID, RowQuery{ReturnAll: true})
			if err != nil {
				t.Fatalf("final List: %v", err)
			}
			if len(page.Rows) != seedRows {
				t.Fatalf("final rows = %d, want %d", len(page.Rows), seedRows)
			}
			for _, row := range page.Rows {
				for i := range added {
					value, ok := row[fmt.Sprintf("extra%d", i)]
					if !ok {
						t.Fatalf("row %v misses evolved column extra%d", row["id"], i)
					}
					if value != nil {
						t.Fatalf("row %v extra%d = %#v, want null", row["id"], i, value)
					}
				}
			}
			if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
				t.Fatalf("Drop: %v", err)
			}
		})
	}
}
