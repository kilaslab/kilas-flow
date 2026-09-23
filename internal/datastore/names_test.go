package datastore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// wantNameTaken fails unless err is the taken-name refusal naming the table
// that holds the name, which is what the create and rename dialogs show.
func wantNameTaken(t *testing.T, what string, err error, holder string) {
	t.Helper()
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("%s = %v, want ErrNameTaken", what, err)
		return
	}
	var taken *NameTakenError
	if !errors.As(err, &taken) || taken.Name != holder {
		t.Errorf("%s = %v, want it to name %q, the table that holds the name", what, err, holder)
	}
}

// A name identifies one data table in its tenant, the way n8n's do. The By-Name
// locator compares names without regard to case, so "leads" beside "Leads" is
// the same name, and a second table under it is refused rather than left for a
// lookup to pick one of (BUG-e7dwpk).
func TestADataTableNameIsTakenWhateverItsCase(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-1", "Leads"))

			for _, name := range []string{"Leads", "leads", "LEADS", "  leads  "} {
				_, err := eng.Create(ctx, "tenant-1", name, []ColumnInput{{Name: "title", Type: "string"}})
				wantNameTaken(t, "Create "+name, err, "Leads")
			}
			listed, err := eng.ListDatastores(ctx, "tenant-1")
			if err != nil {
				t.Fatalf("ListDatastores: %v", err)
			}
			if len(listed) != 1 {
				t.Fatalf("tenant-1 holds %d data tables after the refusals, want the one Leads", len(listed))
			}

			// Another tenant's names are its own.
			trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-2", "leads"))
		})
	}
}

// Renaming onto another table's name is the same collision as creating one,
// and a table may still take its own name in another case: nothing else holds
// it.
func TestRenamingOntoAnotherTablesNameIsRefusedWhateverItsCase(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-1", "Alpha"))
			beta, err := eng.Create(ctx, "tenant-1", "Beta", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create Beta: %v", err)
			}
			trackPhysical(t, db, beta.Table)

			for _, name := range []string{"Alpha", "alpha", " ALPHA "} {
				wantNameTaken(t, "RenameDatastore "+name, eng.RenameDatastore(ctx, "tenant-1", beta.ID, name), "Alpha")
			}
			unchanged, err := eng.GetDatastore(ctx, "tenant-1", beta.ID)
			if err != nil {
				t.Fatalf("GetDatastore: %v", err)
			}
			if unchanged.Name != "Beta" {
				t.Fatalf("a refused rename left the name %q, want Beta", unchanged.Name)
			}

			if err := eng.RenameDatastore(ctx, "tenant-1", beta.ID, " BETA "); err != nil {
				t.Fatalf("renaming Beta to its own name in capitals = %v, want it allowed", err)
			}
			renamed, err := eng.GetDatastore(ctx, "tenant-1", beta.ID)
			if err != nil {
				t.Fatalf("GetDatastore: %v", err)
			}
			if renamed.Name != "BETA" {
				t.Fatalf("renamed = %q, want BETA with the padding trimmed", renamed.Name)
			}
		})
	}
}

// The engine's check is the authority; the unique index is what stops two
// writers that both passed it. A catalogue row written around the engine proves
// the index is there, on both drivers, and that the driver's refusal reaches
// the engine as a duplicate key it can classify.
func TestTheCatalogueRefusesADuplicateNameWrittenAroundTheEngine(t *testing.T) {
	for _, drv := range testDrivers() {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-1", "Leads"))

			err := db.Create(&datastoreModel{
				ID: "datastore_written_around", TenantID: "tenant-1", Name: "LEADS",
				Surrogate: "0123456789abcdef", SchemaVersion: 1,
			}).Error
			if !errors.Is(err, gorm.ErrDuplicatedKey) {
				t.Fatalf("inserting LEADS beside Leads = %v, want the unique index to refuse it", err)
			}
			if err := db.Create(&datastoreModel{
				ID: "datastore_other_tenant", TenantID: "tenant-2", Name: "LEADS",
				Surrogate: "fedcba9876543210", SchemaVersion: 1,
			}).Error; err != nil {
				t.Fatalf("inserting LEADS in another tenant = %v, want it allowed", err)
			}
		})
	}
}

// A duplicate key is not always the name: the surrogate and the public id are
// random and can collide, and that collision is retried with fresh ones rather
// than reported as a taken name.
func TestADuplicateKeyThatIsNotTheNameIsRetried(t *testing.T) {
	db, eng := testDrivers()[0].open(t, "")
	attempts := 0
	eng.inject = func(stage string) error {
		if stage != StageCatalogue {
			return nil
		}
		attempts++
		if attempts == 1 {
			return gorm.ErrDuplicatedKey
		}
		return nil
	}
	created, err := eng.Create(context.Background(), "tenant-1", "Leads", []ColumnInput{{Name: "title", Type: "string"}})
	if err != nil {
		t.Fatalf("Create after one surrogate collision = %v, want the retry to succeed", err)
	}
	trackPhysical(t, db, created.Table)
	if attempts != 2 {
		t.Fatalf("Create took %d attempts, want 2: one collision and one retry", attempts)
	}
}

// Two writers of one name can both pass the engine's check. The index refuses
// the second, and that refusal has to reach the caller as the taken name — not
// as a surrogate that "kept colliding" after three retries of a write that
// could never succeed.
//
// SQLite runs every write through one connection, so no second writer can land
// between the check and the write; the window only opens on PostgreSQL.
func TestAWriteThatLosesTheRaceForANameIsToldTheNameIsTaken(t *testing.T) {
	drivers := testDrivers()[1:]
	if len(drivers) == 0 {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN to race two writers of one name: SQLite serialises them through its one connection")
	}
	for _, drv := range drivers {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()

			// raceFor lands a create of name from another connection inside the
			// window, once: the concurrent create passes the same stage, and a
			// second race from inside it would never end.
			raceFor := func(name string) {
				raced := false
				eng.inject = func(stage string) error {
					if stage != StageName || raced {
						return nil
					}
					raced = true
					winner, err := eng.Create(ctx, "tenant-1", name, []ColumnInput{{Name: "title", Type: "string"}})
					if err != nil {
						return err
					}
					trackPhysical(t, db, winner.Table)
					return nil
				}
			}

			raceFor("Leads")
			_, err := eng.Create(ctx, "tenant-1", "leads", []ColumnInput{{Name: "title", Type: "string"}})
			wantNameTaken(t, "the create that lost the race", err, "Leads")

			eng.inject = nil
			alpha, err := eng.Create(ctx, "tenant-1", "Alpha", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create Alpha: %v", err)
			}
			trackPhysical(t, db, alpha.Table)
			raceFor("Beta")
			wantNameTaken(t, "the rename that lost the race", eng.RenameDatastore(ctx, "tenant-1", alpha.ID, "beta"), "Beta")
		})
	}
}

// The index can be stricter than the rule. PostgreSQL's lower() folds by the
// database's locale, and under a libc locale folds "İ" to "i", which Go does
// not: the engine's check passes "istanbul" beside "İstanbul" and the index then
// refuses it. That refusal is still a taken name, reported as one, rather than
// three retries ending in a surrogate that "kept colliding" or a driver error
// answered as a server fault.
func TestANameOnlyTheDatabaseFoldsTogetherIsReportedAsTaken(t *testing.T) {
	drivers := testDrivers()[1:]
	if len(drivers) == 0 {
		t.Skip("set KILASFLOW_TEST_POSTGRES_DSN: only PostgreSQL's lower() folds by locale")
	}
	for _, drv := range drivers {
		t.Run(drv.name, func(t *testing.T) {
			db, eng := drv.open(t, "")
			ctx := context.Background()
			var folds bool
			if err := db.Raw(`SELECT lower('İstanbul') = lower('istanbul')`).Scan(&folds).Error; err != nil {
				t.Fatalf("ask the server how it folds: %v", err)
			}
			if !folds {
				t.Skip("this server's locale does not fold İ to i, so its index agrees with the engine's check")
			}
			trackPhysical(t, db, mustCatalogueTable(t, eng, "tenant-1", "İstanbul"))

			_, err := eng.Create(ctx, "tenant-1", "istanbul", []ColumnInput{{Name: "title", Type: "string"}})
			wantNameTaken(t, "Create istanbul", err, "İstanbul")

			other, err := eng.Create(ctx, "tenant-1", "Ankara", []ColumnInput{{Name: "title", Type: "string"}})
			if err != nil {
				t.Fatalf("Create Ankara: %v", err)
			}
			trackPhysical(t, db, other.Table)
			wantNameTaken(t, "RenameDatastore istanbul", eng.RenameDatastore(ctx, "tenant-1", other.ID, "istanbul"), "İstanbul")
		})
	}
}

// ResolveByName is the one By-Name lookup. It returns the single table a name
// identifies, and when several share the name it refuses and lists them, never
// acting on whichever the catalogue happened to list first.
func TestResolveByNameNeverPicksOneOfSeveralMatches(t *testing.T) {
	candidates := []Datastore{
		{ID: "datastore_1", Name: "Leads"},
		{ID: "datastore_2", Name: "leads"},
		{ID: "datastore_3", Name: "Other"},
	}

	id, err := ResolveByName(candidates, "LEADS")
	if !errors.Is(err, ErrAmbiguousName) {
		t.Fatalf("ResolveByName(LEADS) = (%q, %v), want ErrAmbiguousName", id, err)
	}
	if id != "" {
		t.Errorf("an ambiguous name resolved to %q, want no id at all", id)
	}
	for _, want := range []string{"datastore_1", "datastore_2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not list %s, one of the tables that share the name", err, want)
		}
	}
	if strings.Contains(err.Error(), "datastore_3") {
		t.Errorf("the refusal %q lists datastore_3, which does not share the name", err)
	}

	if id, err := ResolveByName(candidates, "  other "); err != nil || id != "datastore_3" {
		t.Errorf("ResolveByName(other) = (%q, %v), want datastore_3: case and padding do not change a name", id, err)
	}
	for _, name := range []string{"Missing", "", "   "} {
		if id, err := ResolveByName(candidates, name); !IsUnknown(err) || id != "" {
			t.Errorf("ResolveByName(%q) = (%q, %v), want the unknown-datastore refusal", name, id, err)
		}
	}
}
