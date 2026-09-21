package datastore

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

// pushBehind parks a datastore one version back for runner tests.
func pushBehind(t *testing.T, eng *Engine, ds *Datastore, version int) {
	t.Helper()
	if err := eng.db.Exec(`UPDATE datastores SET schema_version = ? WHERE id = ?`, version, ds.ID).Error; err != nil {
		t.Fatalf("push %s to %d: %v", ds.ID, version, err)
	}
}

func versionOf(t *testing.T, eng *Engine, ds *Datastore) int {
	t.Helper()
	var v int
	if err := eng.db.Raw(`SELECT schema_version FROM datastores WHERE id = ?`, ds.ID).Scan(&v).Error; err != nil {
		t.Fatalf("read version of %s: %v", ds.ID, err)
	}
	return v
}

// The work list is re-read after every datastore: a datastore created
// mid-run is picked up by the same run, and a datastore created while the
// runner is working is born at the current version.
func TestFleetRunPicksUpDatastoresCreatedMidRun(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	a, err := eng.Create(ctx, "tenant-1", "first", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	trackPhysical(t, db, a.Table)
	pushBehind(t, eng, a, 0)

	// Born at current: Create stamps CurrentSchemaVersion, so a mid-run
	// birth starts ahead of the work list rather than behind it.
	c, err := eng.Create(ctx, "tenant-1", "third", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create c: %v", err)
	}
	trackPhysical(t, db, c.Table)
	if c.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("new datastore born at version %d, want %d", c.SchemaVersion, CurrentSchemaVersion)
	}

	// A snapshot taken once would miss b: it is created inside the first
	// step's transaction — through the step's own handle, because opening
	// a second transaction on SQLite's single connection would deadlock —
	// and parked behind, so only a re-read work list migrates it.
	const bSurrogate = "6d696472756e3031"
	bTable := PhysicalTableName("", bSurrogate)
	trackPhysical(t, db, bTable)
	const bID = "datastore_midrun00000000000000000000"
	var calls atomic.Int64
	runner := NewFleetRunner()
	runner.Register(0, func(ctx context.Context, tx *gorm.DB, got Datastore) error {
		if calls.Add(1) == 1 {
			if err := tx.Create(&datastoreModel{
				ID: bID, TenantID: "tenant-1",
				Name: "second", Surrogate: bSurrogate, SchemaVersion: 0,
			}).Error; err != nil {
				return err
			}
			if err := tx.Create(&datastoreColumnModel{
				TenantID:    "tenant-1",
				DatastoreID: bID,
				Name:        "title", Type: "string", Position: 0,
			}).Error; err != nil {
				return err
			}
			// GORM's default:1 tag turns the zero SchemaVersion above into
			// the database default, so park b behind explicitly.
			if err := tx.Exec(`UPDATE datastores SET schema_version = 0 WHERE id = ?`, bID).Error; err != nil {
				return err
			}
			statement := createTableStatement("sqlite", "", bSurrogate,
				[]ColumnDef{{Name: "title", Type: ColumnString, Position: 0}})
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})

	migrated, err := runner.Run(ctx, db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if migrated != 2 {
		t.Errorf("migrated = %d, want 2 (the behind datastore plus the mid-run one)", migrated)
	}
	if calls.Load() != 2 {
		t.Errorf("step invocations = %d, want 2", calls.Load())
	}
	if versionOf(t, eng, a) != CurrentSchemaVersion {
		t.Errorf("a at version %d, want current", versionOf(t, eng, a))
	}
	var bVersion int
	if err := db.Raw(`SELECT schema_version FROM datastores WHERE id = ?`, bID).Scan(&bVersion).Error; err != nil {
		t.Fatalf("read b version: %v", err)
	}
	if bVersion != CurrentSchemaVersion {
		t.Errorf("mid-run datastore at version %d, want %d", bVersion, CurrentSchemaVersion)
	}

	// A resumed run visits only datastores still behind: everything is
	// current, so no step opens any datastore again.
	migrated, err = runner.Run(ctx, db)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if migrated != 0 || calls.Load() != 2 {
		t.Errorf("second run migrated %d with %d invocations, want 0 and 2", migrated, calls.Load())
	}

	for _, id := range []string{a.ID, bID, c.ID} {
		if err := eng.Drop(ctx, "tenant-1", id); err != nil {
			t.Fatalf("Drop %s: %v", id, err)
		}
	}
}

// A kill inside a step rolls that step back: the datastore stays fully at
// the old version, and the next run resumes it to current.
func TestFleetInterruptInsideStepLeavesOldVersion(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "contacts", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	pushBehind(t, eng, ds, 0)

	fail := true
	var calls atomic.Int64
	runner := NewFleetRunner()
	runner.Register(0, func(ctx context.Context, tx *gorm.DB, got Datastore) error {
		calls.Add(1)
		if err := tx.Exec("ALTER TABLE " + quoteIdent("sqlite", PhysicalTableName("", got.Surrogate)) +
			" ADD " + quoteIdent("sqlite", "migrated") + " TEXT").Error; err != nil {
			return err
		}
		if fail {
			return context.DeadlineExceeded // the kill, between DDL and stamp
		}
		return nil
	})

	if _, err := runner.Run(ctx, db); err == nil {
		t.Fatal("Run with failing step = nil, want the injected kill")
	}
	if got := versionOf(t, eng, ds); got != 0 {
		t.Errorf("version after kill = %d, want 0 (fully old, never between)", got)
	}

	fail = false
	migrated, err := runner.Run(ctx, db)
	if err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if migrated != 1 || calls.Load() != 2 {
		t.Errorf("resumed run migrated %d with %d invocations, want 1 and 2", migrated, calls.Load())
	}
	if got := versionOf(t, eng, ds); got != CurrentSchemaVersion {
		t.Errorf("version after resume = %d, want %d", got, CurrentSchemaVersion)
	}
	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// A run meeting a datastore past the version the build knows refuses it
// with both versions named.
func TestFleetRunRefusesAheadVersion(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	ds, err := eng.Create(ctx, "tenant-1", "future", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	trackPhysical(t, db, ds.Table)
	if err := eng.db.Exec(`UPDATE datastores SET schema_version = ? WHERE id = ?`, CurrentSchemaVersion+4, ds.ID).Error; err != nil {
		t.Fatalf("push ahead: %v", err)
	}
	_, err = NewFleetRunner().Run(ctx, db)
	if err == nil {
		t.Fatal("Run over ahead datastore = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "5") || !strings.Contains(err.Error(), "1") {
		t.Errorf("refusal %q does not name both versions", err)
	}
	if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
		t.Fatalf("Drop: %v", err)
	}
}

// VersionSpread counts datastores per version without opening any physical
// table, so a half-migrated fleet is visibly half-migrated.
func TestVersionSpreadShowsMixedFleet(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	ctx := context.Background()

	var created []*Datastore
	for _, name := range []string{"s1", "s2", "s3"} {
		ds, err := eng.Create(ctx, "tenant-1", name, []ColumnInput{{Name: "title", Type: "string"}})
		if err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		trackPhysical(t, db, ds.Table)
		created = append(created, ds)
	}
	pushBehind(t, eng, created[0], 0)

	spread, err := VersionSpread(ctx, db)
	if err != nil {
		t.Fatalf("VersionSpread: %v", err)
	}
	if spread[0] != 1 || spread[CurrentSchemaVersion] != 2 {
		t.Errorf("spread = %v, want map[0:1 1:2]", spread)
	}

	for _, ds := range created {
		if err := eng.Drop(ctx, "tenant-1", ds.ID); err != nil {
			t.Fatalf("Drop: %v", err)
		}
	}
	spread, err = VersionSpread(ctx, db)
	if err != nil {
		t.Fatalf("VersionSpread after drops: %v", err)
	}
	if len(spread) != 0 {
		t.Errorf("spread after drops = %v, want empty", spread)
	}
}
