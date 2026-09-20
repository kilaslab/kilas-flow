package datastore

import (
	"context"
	"errors"
	"testing"
)

func TestCatalogueListsRenamesAndIsolatesTenants(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()
	trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-1", "Alpha"))
	_ = db

	beta, err := eng.Create(ctx, "tenant-1", "Beta", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create Beta: %v", err)
	}
	trackPhysical(t, db, beta.Table)
	if _, err := eng.Create(ctx, "tenant-2", "Other", []ColumnInput{{Name: "title", Type: "string"}}); err != nil {
		t.Fatalf("Create Other: %v", err)
	}

	listed, err := eng.ListDatastores(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListDatastores: %v", err)
	}
	if len(listed) != 2 || listed[0].Name != "Alpha" || listed[1].Name != "Beta" {
		names := make([]string, 0, len(listed))
		for _, candidate := range listed {
			names = append(names, candidate.Name)
		}
		t.Fatalf("tenant-1 lists %v, want [Alpha Beta] in name order", names)
	}

	got, err := eng.GetDatastore(ctx, "tenant-1", beta.ID)
	if err != nil {
		t.Fatalf("GetDatastore: %v", err)
	}
	if got.Name != "Beta" || len(got.Columns) != 1 || got.Columns[0].Name != "title" {
		t.Fatalf("GetDatastore = %+v, want Beta with its title column", got)
	}
	if _, err := eng.GetDatastore(ctx, "tenant-2", beta.ID); !IsUnknown(err) {
		t.Fatalf("tenant-2 GetDatastore = %v, want unknown", err)
	}

	if err := eng.RenameDatastore(ctx, "tenant-1", beta.ID, "Gamma"); err != nil {
		t.Fatalf("RenameDatastore: %v", err)
	}
	renamed, err := eng.GetDatastore(ctx, "tenant-1", beta.ID)
	if err != nil {
		t.Fatalf("GetDatastore after rename: %v", err)
	}
	if renamed.Name != "Gamma" || len(renamed.Columns) != 1 {
		t.Fatalf("renamed = %+v, want Gamma with columns untouched", renamed)
	}
	if err := eng.RenameDatastore(ctx, "tenant-2", beta.ID, "Hijack"); !IsUnknown(err) {
		t.Fatalf("tenant-2 RenameDatastore = %v, want unknown", err)
	}
	if err := eng.RenameDatastore(ctx, "tenant-1", beta.ID, "  "); err == nil {
		t.Fatal("RenameDatastore with a blank name = nil, want refusal")
	}
	if err := eng.Drop(ctx, "tenant-1", beta.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

func mustCatalogueTable(t *testing.T, eng *Engine, tenant, name string) string {
	t.Helper()
	definition, err := eng.Create(context.Background(), tenant, name, []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	return definition.Table
}

// namesInPageOrder stores n datastores and returns the names the page yields in
// the order it yielded them.
func namesInPageOrder(t *testing.T, eng *Engine, tenant string) []string {
	t.Helper()

	listed := make([]string, 0, 8)
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := eng.ListDatastoresPage(context.Background(), tenant, DatastoreQuery{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListDatastoresPage: %v", err)
		}
		if len(page.Datastores) > 2 {
			t.Fatalf("page carried %d datastores, want at most the limit of 2", len(page.Datastores))
		}
		for _, definition := range page.Datastores {
			if len(definition.Columns) != 1 || definition.Columns[0].Name != "title" {
				t.Fatalf("page entry %s carried columns %+v, want its title column", definition.Name, definition.Columns)
			}
			listed = append(listed, definition.Name)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
	}
	return listed
}

// The catalogue list is what the management page loads, and it used to return
// every datastore a tenant owned along with a column read per row.
func TestTheCataloguePagesInNameOrder(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	tenant := "tenant-1"
	for _, name := range []string{"Delta", "Alpha", "Echo", "Beta", "Gamma"} {
		trackPhysical(t, db, mustCatalogueTable(t, eng, tenant, name))
	}

	listed := namesInPageOrder(t, eng, tenant)
	want := []string{"Alpha", "Beta", "Delta", "Echo", "Gamma"}
	if len(listed) != len(want) {
		t.Fatalf("catalogue yielded %v, want %v", listed, want)
	}
	for index := range want {
		if listed[index] != want[index] {
			t.Fatalf("catalogue yielded %v, want name order %v", listed, want)
		}
	}
}

// The page carries a cursor exactly while rows remain, and a cursor the engine
// did not issue is an error rather than a position: the API answers it with a
// 400, never by paging from somewhere a client invented.
func TestTheCatalogueCursorIsIssuedAndValidated(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	tenant := "tenant-1"
	for _, name := range []string{"Alpha", "Beta"} {
		trackPhysical(t, db, mustCatalogueTable(t, eng, tenant, name))
	}

	page, err := eng.ListDatastoresPage(context.Background(), tenant, DatastoreQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ListDatastoresPage: %v", err)
	}
	if len(page.Datastores) != 2 || page.NextCursor != "" {
		t.Fatalf("page = (%d datastores, cursor %q), want both rows and no cursor", len(page.Datastores), page.NextCursor)
	}

	short, err := eng.ListDatastoresPage(context.Background(), tenant, DatastoreQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListDatastoresPage: %v", err)
	}
	if short.NextCursor == "" {
		t.Fatal("a full page must carry the cursor for the next one")
	}

	for _, cursor := range []string{"not-a-cursor", "ISE=", "cm93LXYxADE"} {
		if _, err := eng.ListDatastoresPage(context.Background(), tenant, DatastoreQuery{Cursor: cursor}); !errors.Is(err, ErrInvalidDatastoreCursor) {
			t.Errorf("ListDatastoresPage(cursor %q) error = %v, want ErrInvalidDatastoreCursor", cursor, err)
		}
	}
}

// The page and the unbounded locator read the same catalogue: a listing that
// disagreed with ListDatastores would resolve a From-list reference to a name
// the page never shows.
func TestTheCataloguePageAgreesWithTheUnboundedListing(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	tenant := "tenant-1"
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		trackPhysical(t, db, mustCatalogueTable(t, eng, tenant, name))
	}

	all, err := eng.ListDatastores(context.Background(), tenant)
	if err != nil {
		t.Fatalf("ListDatastores: %v", err)
	}
	page, err := eng.ListDatastoresPage(context.Background(), tenant, DatastoreQuery{Limit: 100})
	if err != nil {
		t.Fatalf("ListDatastoresPage: %v", err)
	}
	if len(all) != len(page.Datastores) {
		t.Fatalf("ListDatastores returned %d, ListDatastoresPage returned %d", len(all), len(page.Datastores))
	}
	for index := range all {
		if all[index].ID != page.Datastores[index].ID {
			t.Errorf("row %d: ListDatastores has %s, ListDatastoresPage has %s", index, all[index].ID, page.Datastores[index].ID)
		}
	}
}
