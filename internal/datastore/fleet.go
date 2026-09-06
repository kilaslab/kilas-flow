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
//
// FEAT-gxppx1 owns the runner's future: per-datastore steps, leases, and
// readiness. This file is only the hook point it builds on.
const CurrentSchemaVersion = 1

// FleetStep migrates one datastore's physical table from one version to the
// next inside the transaction that also stamps the new version, which is
// what makes a killed process leave every datastore fully at the old
// version or fully at the new one rather than between.
type FleetStep func(ctx context.Context, tx *gorm.DB, ds Datastore) error

// FleetRunner applies registered steps to every datastore behind
// CurrentSchemaVersion, one datastore per transaction, re-reading the work
// list each time rather than snapshotting it. With no steps registered it
// migrates nothing; a datastore behind the binary is refused with both
// versions named rather than silently served.
type FleetRunner struct {
	steps map[int]FleetStep
}

// NewFleetRunner returns a runner with no steps: the no-op path, which is
// the only path this ticket exercises.
func NewFleetRunner() *FleetRunner {
	return &FleetRunner{steps: map[int]FleetStep{}}
}

// Register adds the step that moves a physical table from fromVersion to
// fromVersion+1.
func (r *FleetRunner) Register(fromVersion int, step FleetStep) {
	r.steps[fromVersion] = step
}

// Run migrates every datastore behind CurrentSchemaVersion and reports how
// many it moved. Running it twice applies each step exactly once, because
// the version stamp shares the step's transaction.
func (r *FleetRunner) Run(ctx context.Context, db *database.DB) (int, error) {
	var rows []datastoreModel
	if err := db.WithContext(ctx).Where("schema_version < ?", CurrentSchemaVersion).Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("datastore: list datastores behind schema version %d: %w", CurrentSchemaVersion, err)
	}
	migrated := 0
	for _, row := range rows {
		for version := row.SchemaVersion; version < CurrentSchemaVersion; version++ {
			step, ok := r.steps[version]
			if !ok {
				return migrated, fmt.Errorf("datastore: %s is at schema version %d but this build knows no step from version %d to %d",
					row.ID, row.SchemaVersion, version, version+1)
			}
			version := version
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := step(ctx, tx, Datastore{
					ID:            row.ID,
					TenantID:      row.TenantID,
					Name:          row.Name,
					Surrogate:     row.Surrogate,
					SchemaVersion: row.SchemaVersion,
				}); err != nil {
					return err
				}
				return tx.Model(&datastoreModel{}).
					Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
					Update("schema_version", version+1).Error
			}); err != nil {
				return migrated, err
			}
		}
		migrated++
	}
	return migrated, nil
}
