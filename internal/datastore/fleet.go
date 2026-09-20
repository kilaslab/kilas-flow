package datastore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/kilaslab/kilas-flow/internal/database"
)

// CurrentSchemaVersion is the physical-table shape Create issues today, and
// the version an Engine serves and migrates towards (NewEngine copies it into
// the engine, so the runner, Create and the row gate can never disagree).
// It is stamped onto every new catalogue row, so a datastore born while the
// fleet runner is working is born at the version the runner is migrating
// towards rather than behind it.
//
// Raising it is a two-part change made in ONE commit: register the step that
// moves a datastore from the old version in shippedFleetSteps, or every
// existing datastore is refused at boot. TestEveryVersionBehindTheCurrentOneHasAShippedFleetStep
// fails the build when that step is missing.
const CurrentSchemaVersion = 1

// FleetStep migrates one datastore's physical table from one version to the
// next inside the transaction that also stamps the new version, which is
// what makes a killed process leave every datastore fully at the old
// version or fully at the new one rather than between.
//
// The step is handed the transaction it must use for every statement: a
// second transaction would deadlock SQLite's single connection, and a
// statement outside this one would survive the rollback of a failed step.
//
// A step MUST NOT write or delete the datastore's own catalogue row
// (datastores, and datastore_columns): the runner owns the version stamp and
// applies it with a compare-and-swap on the version the step was handed, so
// a step that moves the version itself, or removes the row, leaves that swap
// with nothing to match. The runner refuses the whole run and names the
// datastore and both versions rather than letting the row be re-selected
// forever; the message says so.
type FleetStep func(ctx context.Context, tx *gorm.DB, ds Datastore) error

// errFleetSkip ends one datastore's migration without an error: a peer moved
// it first, or it was dropped after the runner read it. The work list is
// re-read afterwards, so the datastore's true state decides what happens next.
//
// It is only ever answered from the re-read that happens before the step
// runs, where what it reports belongs to a write that already committed — so
// the re-query really cannot select the datastore again. A version stamp that
// matches no row after the step is a contract violation instead, see
// unstampedCatalogueRow.
var errFleetSkip = errors.New("datastore: skipped, another process moved or dropped it")

// FleetRunner applies registered steps to every datastore behind its target
// version, one step per transaction, re-reading the work list each time
// rather than snapshotting it.
//
// The composition root runs one through Engine.MigrateFleet straight after
// database.Migrate — a failure refuses boot the way a failed SQL migration
// does — and repeats the pass on a slow tick, so a datastore an older peer
// process created behind is migrated without a restart. Readiness reports the
// fleet's version spread through Engine.FleetStatus. No build has needed a
// step yet (the schema version is 1): the first bump of CurrentSchemaVersion
// registers its step in shippedFleetSteps, and a tripwire test fails the
// build if it does not. A datastore behind with no step for it, and a
// datastore ahead of the binary, are refused with both versions named rather
// than silently served.
//
// SQLite bounds are the step author's contract, not the runner's: a step
// must chunk its backfill into bounded batches because a deadline cannot
// un-hold the single connection once a long statement owns it. The runner
// honours context cancellation between datastores so a stop request never
// waits out the fleet.
type FleetRunner struct {
	steps map[int]FleetStep
	// target is the version the runner migrates towards; prefix is the table
	// prefix the steps' physical table names carry; afterSelect is a test
	// seam called right after a behind datastore is read. NewFleetRunner
	// defaults the first to CurrentSchemaVersion and the others to zero; the
	// engine sets all three, and no caller outside the package can.
	target      int
	prefix      string
	afterSelect func(id string)
}

// NewFleetRunner returns a runner with no steps aimed at CurrentSchemaVersion:
// the no-op path.
func NewFleetRunner() *FleetRunner {
	return &FleetRunner{steps: map[int]FleetStep{}, target: CurrentSchemaVersion}
}

// Register adds the step that moves a physical table from fromVersion to
// fromVersion+1.
func (r *FleetRunner) Register(fromVersion int, step FleetStep) {
	r.steps[fromVersion] = step
}

// shippedFleetSteps returns the steps this build ships, keyed by the version
// each moves a datastore FROM. It is empty because the schema version is 1
// and nothing sits behind it. The first bump of CurrentSchemaVersion adds its
// step here in the same commit, and the tripwire test enforces that. Every
// call returns a fresh map, so registering a step on one engine (as tests do)
// never reaches another.
func shippedFleetSteps() map[int]FleetStep {
	return map[int]FleetStep{}
}

// missingFleetSteps lists the versions below current that no step moves a
// datastore from: the gaps that would leave a datastore behind with no way
// to catch up. It is a function of its arguments so a test can prove the
// tripwire is able to trip.
func missingFleetSteps(current int, steps map[int]FleetStep) []int {
	var missing []int
	for version := 1; version < current; version++ {
		if _, ok := steps[version]; !ok {
			missing = append(missing, version)
		}
	}
	return missing
}

// fleetRunner builds the runner for this engine: it migrates towards the
// version the engine serves, through the steps the engine ships, with the
// engine's table prefix. The steps are copied, so a runner is unaffected by a
// later change to the engine.
func (e *Engine) fleetRunner() *FleetRunner {
	steps := make(map[int]FleetStep, len(e.fleetSteps))
	for version, step := range e.fleetSteps {
		steps[version] = step
	}
	return &FleetRunner{steps: steps, target: e.schemaVersion, prefix: e.prefix}
}

// MigrateFleet brings every datastore to the schema version this engine
// serves and reports how many datastores it moved. The composition root
// calls it at boot, after database.Migrate, and again on a slow tick; it is
// safe to call from several processes at once, because each step re-reads
// its datastore under the step's own transaction and applies only if the
// datastore is still at the version the step moves from.
//
// A datastore ahead of this build, or behind it with no step registered, is
// an error naming the datastore and both versions.
func (e *Engine) MigrateFleet(ctx context.Context) (int, error) {
	if e == nil || e.db == nil {
		return 0, errors.New("datastore: engine is not configured")
	}
	return e.fleetRunner().Run(ctx, e.db)
}

// Run migrates every datastore behind the runner's target version and
// reports how many it brought to current. The work list is re-read after
// every datastore, so a datastore created mid-run is picked up by a later
// iteration and a resumed run only ever opens datastores still behind.
// Running it twice applies each step exactly once, because the version
// stamp shares the step's transaction; two runners in two processes apply
// each step once too, see advance.
func (r *FleetRunner) Run(ctx context.Context, db *database.DB) (int, error) {
	if err := r.refuseAhead(ctx, db); err != nil {
		return 0, err
	}
	migrated := 0
	for {
		if err := ctx.Err(); err != nil {
			return migrated, err
		}
		// One datastore per iteration, re-queried every time: a snapshot
		// taken once would miss datastores created during the run.
		var row datastoreModel
		err := db.WithContext(ctx).
			Where("schema_version < ?", r.target).
			Order("id ASC").
			First(&row).Error
		if err == gorm.ErrRecordNotFound {
			return migrated, nil
		}
		if err != nil {
			return migrated, fmt.Errorf("datastore: list datastores behind schema version %d: %w", r.target, err)
		}
		if r.afterSelect != nil {
			r.afterSelect(row.ID)
		}
		done, err := r.migrateOne(ctx, db, row)
		if err != nil {
			return migrated, err
		}
		if done {
			migrated++
		}
	}
}

// migrateOne moves one datastore through every pending step, one step per
// transaction with the stamp sharing the step's transaction. A kill inside
// a step rolls that step back and leaves the datastore at its previous
// version, never between; a kill between steps leaves the datastore at the
// version its last committed step reached, and a kill between datastores
// leaves finished ones current and the rest behind. The next run resumes all
// of them from wherever they are.
//
// It reports true when this runner brought the datastore to the target, and
// false without an error when a peer moved or dropped it first: the caller
// re-reads the datastore's real state, so a datastore a peer left half-way
// is resumed rather than abandoned.
func (r *FleetRunner) migrateOne(ctx context.Context, db *database.DB, row datastoreModel) (bool, error) {
	for version := row.SchemaVersion; version < r.target; version++ {
		step, ok := r.steps[version]
		if !ok {
			return false, fmt.Errorf("datastore: %s is at schema version %d but this build knows no step from version %d to %d",
				row.ID, version, version, version+1)
		}
		err := r.advance(ctx, db, row, version, step)
		if errors.Is(err, errFleetSkip) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// advance applies one step to one datastore in one transaction: it re-reads
// the catalogue row, runs the step and stamps the next version, and either
// all of it commits or none of it does.
//
// The re-read is what makes two processes booting together apply each step
// once. Both may have read the datastore as behind; the first to open its
// transaction moves it, and the second finds it already advanced (or gone)
// and skips. On PostgreSQL the re-read is SELECT ... FOR UPDATE, so the
// second blocks on the row until the first commits and then sees the new
// version; under READ COMMITTED a row a concurrent Drop deleted comes back
// empty. The SQLite driver silently discards clause.Locking (see
// concurrency.go and TestDatastoreWritesTakeNoRowLocks), so there is no row
// lock there and none is claimed: SQLite installs are single-process by
// construction (cmd/kilasflow refuses a split role on SQLite) and the pool
// is one connection, so no transaction can interleave with this one and the
// same re-read is safe.
//
// The step gets the datastore as the catalogue holds it: the physical table
// under the configured prefix, the columns in definition order, and the
// version it moves FROM. Everything is read through tx, never through the
// engine's own handle: a second connection would deadlock SQLite.
func (r *FleetRunner) advance(ctx context.Context, db *database.DB, row datastoreModel, from int, step FleetStep) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked datastoreModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
			First(&locked).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errFleetSkip
		}
		if err != nil {
			return fmt.Errorf("datastore: re-read %s before migrating it: %w", row.ID, err)
		}
		if locked.SchemaVersion != from {
			return errFleetSkip
		}
		cols, err := columnsIn(tx, row.ID)
		if err != nil {
			return err
		}
		if err := step(ctx, tx, Datastore{
			ID:            locked.ID,
			TenantID:      locked.TenantID,
			Name:          locked.Name,
			Surrogate:     locked.Surrogate,
			Table:         PhysicalTableName(r.prefix, locked.Surrogate),
			SchemaVersion: from,
			Columns:       cols,
		}); err != nil {
			return err
		}
		stamped := tx.Model(&datastoreModel{}).
			Where("tenant_id = ? AND id = ? AND schema_version = ?", row.TenantID, row.ID, from).
			Update("schema_version", from+1)
		if stamped.Error != nil {
			return stamped.Error
		}
		if stamped.RowsAffected != 1 {
			return unstampedCatalogueRow(tx, row, from)
		}
		return nil
	})
}

// unstampedCatalogueRow answers a version stamp that matched no row. The
// locked re-read above already proved the row was there at `from` inside
// this transaction, so nothing but the step can have moved it: the stamp's
// only other writer is the runner itself, and the step's own statements are
// the only ones that ran since. Returning errFleetSkip here would loop —
// this transaction has not committed, so rolling the step back restores the
// row at `from` and the caller's re-query selects it again, which is what
// the runner did before this check existed: 9775 step invocations in three
// seconds, at boot, before the listener opens.
//
// A step that writes or deletes the datastore's own catalogue row has broken
// the contract the FleetStep doc states, so the run refuses, naming the
// datastore and both versions.
func unstampedCatalogueRow(tx *gorm.DB, row datastoreModel, from int) error {
	var after datastoreModel
	err := tx.Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).First(&after).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("datastore: the step from version %d deleted %s from the catalogue: %s",
			from, row.ID, stepWroteCatalogue)
	}
	if err != nil {
		return fmt.Errorf("datastore: re-read %s after the step from version %d stamped no row: %w", row.ID, from, err)
	}

	return fmt.Errorf("datastore: %s is at schema version %d after the step from version %d wrote the catalogue row: %s",
		row.ID, after.SchemaVersion, from, stepWroteCatalogue)
}

// stepWroteCatalogue states the rule a FleetStep broke, so every refusal
// that names it also says what the step author has to change.
const stepWroteCatalogue = "the step MUST NOT write or delete the datastore's own catalogue row, the runner owns the version stamp"

// columnsIn reads one datastore's live columns in position order through the
// given handle. It is the transaction-scoped twin of Engine.columnsOf, which
// reads through the engine's own handle and would deadlock a runner already
// holding SQLite's only connection inside a step transaction.
func columnsIn(tx *gorm.DB, id string) ([]ColumnDef, error) {
	var stored []datastoreColumnModel
	if err := tx.Where("datastore_id = ?", id).Order("position ASC").Find(&stored).Error; err != nil {
		return nil, fmt.Errorf("datastore: read columns of %s: %w", id, err)
	}
	cols := make([]ColumnDef, 0, len(stored))
	for _, col := range stored {
		cols = append(cols, ColumnDef{Name: col.Name, Type: ColumnType(col.Type), Position: col.Position})
	}
	return cols, nil
}

// refuseAhead fails the run when any datastore sits past the version this
// build knows: migrating backwards is never attempted, and the error names
// the datastore and both versions.
func (r *FleetRunner) refuseAhead(ctx context.Context, db *database.DB) error {
	var row datastoreModel
	err := db.WithContext(ctx).
		Where("schema_version > ?", r.target).
		Order("id ASC").
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("datastore: list datastores ahead of schema version %d: %w", r.target, err)
	}
	return fmt.Errorf("datastore: %s is at schema version %d but this build knows version %d",
		row.ID, row.SchemaVersion, r.target)
}

// VersionSpread counts datastores per schema version without opening any
// physical table. It is the fleet half of readiness: an instance with
// datastores at mixed versions is visibly half-migrated instead of green.
// Engine.FleetStatus classifies it against the version the engine serves and
// readiness reports the result. The map is never nil.
func VersionSpread(ctx context.Context, db *database.DB) (map[int]int64, error) {
	type bucket struct {
		SchemaVersion int
		Count         int64
	}
	var buckets []bucket
	if err := db.WithContext(ctx).
		Model(&datastoreModel{}).
		Select("schema_version, COUNT(*) AS count").
		Group("schema_version").
		Scan(&buckets).Error; err != nil {
		return nil, fmt.Errorf("datastore: count datastores by schema version: %w", err)
	}
	spread := make(map[int]int64, len(buckets))
	for _, b := range buckets {
		spread[b.SchemaVersion] = b.Count
	}
	return spread, nil
}

// FleetStatus is the fleet's schema-version spread judged against the version
// an engine serves. Behind counts datastores below it (a migration is
// outstanding), Ahead those above it (a newer build migrated them and this
// build refuses them). It holds counts and versions only, never ids or
// tenants, because readiness serves it on an unauthenticated endpoint.
type FleetStatus struct {
	// Version is the schema version the engine serves.
	Version int
	// Spread counts datastores per schema version. It is never nil.
	Spread        map[int]int64
	Behind, Ahead int64
}

// Ready reports whether no migration is outstanding. A datastore AHEAD does
// not fail it: an old replica is not broken because a newer peer migrated a
// datastore during a rolling upgrade, and the row gate already refuses
// exactly those datastores. Failing readiness for ahead would drain every
// old replica at once the moment the first upgraded pod finished migrating.
func (s FleetStatus) Ready() bool { return s.Behind == 0 }

// Problem describes what is outstanding, or is empty when the fleet is
// Ready. It names counts and versions only, never a datastore or a tenant.
func (s FleetStatus) Problem() string {
	if s.Ready() {
		return ""
	}
	versions := make([]int, 0, len(s.Spread))
	for version := range s.Spread {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	parts := make([]string, 0, len(versions))
	for _, version := range versions {
		parts = append(parts, fmt.Sprintf("v%d=%d", version, s.Spread[version]))
	}
	return fmt.Sprintf("datastore migration outstanding: %d datastore(s) behind schema version %d (spread %s)",
		s.Behind, s.Version, strings.Join(parts, ", "))
}

// FleetStatus reads the version spread of the whole fleet without opening
// any physical table and classifies it against the version this engine
// serves.
func (e *Engine) FleetStatus(ctx context.Context) (FleetStatus, error) {
	if e == nil || e.db == nil {
		return FleetStatus{}, errors.New("datastore: engine is not configured")
	}
	spread, err := VersionSpread(ctx, e.db)
	if err != nil {
		return FleetStatus{}, err
	}
	status := FleetStatus{Version: e.schemaVersion, Spread: spread}
	for version, count := range spread {
		switch {
		case version < e.schemaVersion:
			status.Behind += count
		case version > e.schemaVersion:
			status.Ahead += count
		}
	}
	return status, nil
}
