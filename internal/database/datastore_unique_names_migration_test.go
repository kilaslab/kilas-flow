package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/kilaslab/kilas-flow/migrations"
)

// uniqueNamesMigration is looked up by its name and never by its number, for
// the reason backfillMigration gives.
const uniqueNamesMigration = "datastore_unique_names"

// rollBackBelow reverts migrations, newest first, until the one named is no
// longer applied. By name rather than by a count of calls: a count is right
// only until the next migration lands, and then it stops one short and the
// test goes on to read a schema it did not mean to.
func rollBackBelow(t *testing.T, db *DB, name string) {
	t.Helper()
	target := versionNamed(t, db.Dialector.Name(), name)
	all, err := loadMigrations(migrations.FS, db.Dialector.Name())
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	// Bounded by the number of migrations, so a Rollback that stops making
	// progress fails here instead of hanging the suite.
	for range len(all) + 1 {
		applied, err := appliedVersions(db)
		if err != nil {
			t.Fatalf("appliedVersions: %v", err)
		}
		if highestVersion(applied) < target {
			return
		}
		if err := Rollback(db, discardLogger()); err != nil {
			t.Fatalf("Rollback towards %s: %v", name, err)
		}
	}
	t.Fatalf("rolling back %d times left %s applied", len(all)+1, name)
}

// namedDatastore is one catalogue row the test writes and reads back.
type namedDatastore struct {
	id, tenant, name string
	created          time.Time
}

// The migration that makes a data table's name unique meets databases that
// already hold the duplicates it forbids, and it cannot refuse them: a
// migration that fails takes the install down. The rule is that the oldest
// table of each name, by created_at and then id, keeps it, and every other
// takes its own id as a suffix; only then can the index be built.
//
// Exercised the way a deployment meets it: the schema as it stood before the
// migration, duplicates written into it, and the migration applied over them.
func TestDuplicateDatastoreNamesAreRenamedBeforeTheyAreMadeUniqueOnSQLite(t *testing.T) {
	assertUniqueNamesMigration(t, freshSQLite(t))
}

func TestDuplicateDatastoreNamesAreRenamedBeforeTheyAreMadeUniqueOnPostgres(t *testing.T) {
	assertUniqueNamesMigration(t, openPostgres(t))
}

func assertUniqueNamesMigration(t *testing.T, db *DB) {
	t.Helper()
	dialect := db.Dialector.Name()
	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	rollBackBelow(t, db, uniqueNamesMigration)
	if db.Migrator().HasIndex("datastores", "uidx_datastores_tenant_lower_name") {
		t.Fatal("rolling the migration back left its unique index behind")
	}

	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 255)
	seeds := []namedDatastore{
		{"datastore_leads_old", "tenant-a", "Leads", base},
		{"datastore_leads_new", "tenant-a", "leads", base.Add(time.Hour)},
		{"datastore_leads_dup", "tenant-a", "Leads", base.Add(2 * time.Hour)},
		// A tie on created_at still has one answer: the lower id.
		{"datastore_tie_b", "tenant-a", "TIE", base},
		{"datastore_tie_a", "tenant-a", "Tie", base},
		// Another tenant's namesake is not a duplicate, and nor is a name
		// nothing else holds.
		{"datastore_other_tenant", "tenant-b", "Leads", base.Add(3 * time.Hour)},
		{"datastore_alone", "tenant-a", "Alone", base},
		// A name already at the column's width still takes the whole suffix.
		{"datastore_long_a", "tenant-a", long, base},
		{"datastore_long_b", "tenant-a", long, base.Add(time.Hour)},
		// The engine's rule ignores the spaces around a name as well as its
		// case, so a legacy "Tags " is the same name as "Tags", and so is one
		// behind a tab or before a line break. Grouped by case alone they
		// would all keep their names, and every By-Name reference to them
		// would then be refused as ambiguous.
		{"datastore_tags_old", "tenant-a", "Tags", base},
		{"datastore_tags_space", "tenant-a", "Tags ", base.Add(time.Hour)},
		{"datastore_tags_tab", "tenant-a", "\tTAGS", base.Add(2 * time.Hour)},
		{"datastore_tags_line", "tenant-a", " tags\r\n", base.Add(3 * time.Hour)},
	}
	seedDatastoreNames(t, db, seeds...)

	if err := Migrate(db, discardLogger()); err != nil {
		t.Fatalf("Migrate over duplicate names: %v", err)
	}

	// PostgreSQL's name column is varchar(255), so there the original is cut
	// to fit; SQLite's text has no width to fit.
	longRenamed := long + " (datastore_long_b)"
	if dialect == "postgres" {
		suffix := " (datastore_long_b)"
		longRenamed = long[:255-len(suffix)] + suffix
	}
	want := map[string]string{
		"datastore_leads_old":    "Leads",
		"datastore_leads_new":    "leads (datastore_leads_new)",
		"datastore_leads_dup":    "Leads (datastore_leads_dup)",
		"datastore_tie_a":        "Tie",
		"datastore_tie_b":        "TIE (datastore_tie_b)",
		"datastore_other_tenant": "Leads",
		"datastore_alone":        "Alone",
		"datastore_long_a":       long,
		"datastore_long_b":       longRenamed,
		"datastore_tags_old":     "Tags",
		"datastore_tags_space":   "Tags  (datastore_tags_space)",
		"datastore_tags_tab":     "\tTAGS (datastore_tags_tab)",
		"datastore_tags_line":    " tags\r\n (datastore_tags_line)",
	}
	got := datastoreNames(t, db)
	for id, name := range want {
		if got[id] != name {
			t.Errorf("%s is named %q after the migration, want %q", id, got[id], name)
		}
	}
	// What the migration leaves has to pass the engine's own rule, the one
	// ResolveByName refuses an ambiguous name by: no two of a tenant's names
	// the same once their surrounding spaces and case are set aside.
	for id, name := range got {
		for other, otherName := range got {
			if id < other && seedTenant(seeds, id) == seedTenant(seeds, other) &&
				strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(otherName)) {
				t.Errorf("%s (%q) and %s (%q) still share a name by the engine's rule", id, name, other, otherName)
			}
		}
	}

	if !db.Migrator().HasIndex("datastores", "uidx_datastores_tenant_lower_name") {
		t.Fatal("the migration did not create uidx_datastores_tenant_lower_name")
	}
	quote := func(name string) string { return quoteIdentifier(dialect, name) }
	err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
		quote("datastores"), quote("id"), quote("tenant_id"), quote("name"), quote("surrogate"),
		quote("schema_version"), quote("created_at"), quote("updated_at")),
		"datastore_after", "tenant-a", "LEADS", "00000000000000ff", 1, base, base).Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("inserting LEADS beside Leads after the migration = %v, want the unique index to refuse it", err)
	}
}

// seedDatastoreNames writes catalogue rows by SQL, around the engine, which is
// the only way to write the duplicates the engine now refuses.
func seedDatastoreNames(t *testing.T, db *DB, seeds ...namedDatastore) {
	t.Helper()
	quote := func(name string) string { return quoteIdentifier(db.Dialector.Name(), name) }
	statement := fmt.Sprintf("INSERT INTO %s (%s,%s,%s,%s,%s,%s,%s) VALUES (?,?,?,?,?,?,?)",
		quote("datastores"), quote("id"), quote("tenant_id"), quote("name"), quote("surrogate"),
		quote("schema_version"), quote("created_at"), quote("updated_at"))
	for index, seed := range seeds {
		surrogate := fmt.Sprintf("%016x", index+1)
		if err := db.Exec(statement, seed.id, seed.tenant, seed.name, surrogate, 1, seed.created, seed.created).Error; err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}
}

// seedTenant is the tenant the seed with the given id was written for.
func seedTenant(seeds []namedDatastore, id string) string {
	for _, seed := range seeds {
		if seed.id == id {
			return seed.tenant
		}
	}
	return ""
}

// datastoreNames reads every catalogue row's name back by id.
func datastoreNames(t *testing.T, db *DB) map[string]string {
	t.Helper()
	var rows []struct {
		ID   string
		Name string
	}
	if err := db.Raw(fmt.Sprintf("SELECT %s AS id, %s AS name FROM %s",
		quoteIdentifier(db.Dialector.Name(), "id"),
		quoteIdentifier(db.Dialector.Name(), "name"),
		quoteIdentifier(db.Dialector.Name(), "datastores"))).
		Scan(&rows).Error; err != nil {
		t.Fatalf("read the catalogue names: %v", err)
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names
}
