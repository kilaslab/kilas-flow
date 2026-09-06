package datastore

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// The no-op path: a fleet already at the current version migrates nothing,
// and running the runner twice changes nothing the second time.
func TestFleetRunOverACurrentFleetMigratesNothing(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	if ds.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("new datastore schema version = %d, want %d: born at the current version",
			ds.SchemaVersion, CurrentSchemaVersion)
	}

	runner := NewFleetRunner()
	for run := 1; run <= 2; run++ {
		migrated, err := runner.Run(ctx, db)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if migrated != 0 {
			t.Errorf("run %d migrated %d datastores, want 0", run, migrated)
		}
	}

	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// Running the runner twice over a behind fleet applies each step exactly
// once: the count comes from step invocations, not from inspecting the
// resulting schema.
func TestFleetRunAppliesEachStepExactlyOnce(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	if err := db.Exec(`UPDATE datastores SET schema_version = 0 WHERE id = ?`, ds.ID).Error; err != nil {
		t.Fatalf("push the datastore behind: %v", err)
	}

	invocations := 0
	runner := NewFleetRunner()
	runner.Register(0, func(ctx context.Context, tx *gorm.DB, got Datastore) error {
		invocations++
		if got.ID != ds.ID {
			t.Errorf("step ran for %q, want %q", got.ID, ds.ID)
		}
		return nil
	})

	migrated, err := runner.Run(ctx, db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if migrated != 1 || invocations != 1 {
		t.Errorf("migrated = %d with %d invocations, want 1 and 1", migrated, invocations)
	}
	migrated, err = runner.Run(ctx, db)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if migrated != 0 || invocations != 1 {
		t.Errorf("second run migrated = %d with %d invocations, want 0 and 1", migrated, invocations)
	}

	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// A binary meeting a datastore at a version it does not know refuses that
// datastore's migration with an error naming both versions.
func TestFleetRunRefusesAnUnknownVersion(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	if err := db.Exec(`UPDATE datastores SET schema_version = 0 WHERE id = ?`, ds.ID).Error; err != nil {
		t.Fatalf("push the datastore behind: %v", err)
	}

	_, err = NewFleetRunner().Run(ctx, db)
	if err == nil {
		t.Fatal("Run with no step for version 0 = nil, want refusal")
	}
	for _, want := range []string{"0", "1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name version %s", err, want)
		}
	}

	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}
