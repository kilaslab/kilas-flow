package datastore

import (
	"context"
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
