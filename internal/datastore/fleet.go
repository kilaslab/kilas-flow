package datastore

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/kilaslabs/kilas-flow/internal/database"
)

// CurrentSchemaVersion is the physical-table shape Create issues today. It
// is stamped onto every new catalogue row, so a datastore born while the
// fleet runner is working is born at the version the runner is migrating
// towards rather than behind it.
const CurrentSchemaVersion = 1

// FleetStep migrates one datastore's physical table from one version to the
// next inside the transaction that also stamps the new version, which is
// what makes a killed process leave every datastore fully at the old
// version or fully at the new one rather than between.
type FleetStep func(ctx context.Context, tx *gorm.DB, ds Datastore) error

// FleetRunner applies registered steps to every datastore behind
// CurrentSchemaVersion, one datastore per transaction, re-reading the work
// list each time rather than snapshotting it. With no steps registered it
// migrates nothing; a datastore ahead of the binary is refused with both
// versions named rather than silently served.
//
// SQLite bounds are the step author's contract, not the runner's: a step
// must chunk its backfill into bounded batches because a deadline cannot
// un-hold the single connection once a long statement owns it. The runner
// honours context cancellation between datastores so a stop request never
// waits out the fleet.
type FleetRunner struct {
	steps map[int]FleetStep
}

// NewFleetRunner returns a runner with no steps: the no-op path.
func NewFleetRunner() *FleetRunner {
	return &FleetRunner{steps: map[int]FleetStep{}}
}

// Register adds the step that moves a physical table from fromVersion to
// fromVersion+1.
func (r *FleetRunner) Register(fromVersion int, step FleetStep) {
	r.steps[fromVersion] = step
}

// Run migrates every datastore behind CurrentSchemaVersion and reports how
// many it brought to current. The work list is re-read after every
// datastore, so a datastore created mid-run is picked up by a later
// iteration and a resumed run only ever opens datastores still behind.
// Running it twice applies each step exactly once, because the version
// stamp shares the step's transaction.
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
			Where("schema_version < ?", CurrentSchemaVersion).
			Order("id ASC").
			First(&row).Error
		if err == gorm.ErrRecordNotFound {
			return migrated, nil
		}
		if err != nil {
			return migrated, fmt.Errorf("datastore: list datastores behind schema version %d: %w", CurrentSchemaVersion, err)
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
// version; a kill between datastores leaves finished ones current and the
// rest behind, which the next run resumes.
func (r *FleetRunner) migrateOne(ctx context.Context, db *database.DB, row datastoreModel) (bool, error) {
	for version := row.SchemaVersion; version < CurrentSchemaVersion; version++ {
		step, ok := r.steps[version]
		if !ok {
			return false, fmt.Errorf("datastore: %s is at schema version %d but this build knows no step from version %d to %d",
				row.ID, row.SchemaVersion, version, version+1)
		}
		version := version
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := step(ctx, tx, Datastore{
				ID:            row.ID,
				TenantID:      row.TenantID,
				Name:          row.Name,
				Surrogate:     row.Surrogate,
				SchemaVersion: version,
			}); err != nil {
				return err
			}
			return tx.Model(&datastoreModel{}).
				Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
				Update("schema_version", version+1).Error
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// refuseAhead fails the run when any datastore sits past the version this
// build knows: migrating backwards is never attempted, and the error names
// the datastore and both versions.
func (r *FleetRunner) refuseAhead(ctx context.Context, db *database.DB) error {
	var row datastoreModel
	err := db.WithContext(ctx).
		Where("schema_version > ?", CurrentSchemaVersion).
		Order("id ASC").
		First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("datastore: list datastores ahead of schema version %d: %w", CurrentSchemaVersion, err)
	}
	return fmt.Errorf("datastore: %s is at schema version %d but this build knows version %d",
		row.ID, row.SchemaVersion, CurrentSchemaVersion)
}

// VersionSpread counts datastores per schema version without opening any
// physical table. It is the fleet half of readiness: an instance with
// datastores at mixed versions is visibly half-migrated instead of green.
// The HTTP handler that serves it belongs to a later ticket; this is the
// query it will call.
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
