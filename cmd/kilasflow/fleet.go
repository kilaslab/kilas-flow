package main

// The boot half of the datastore fleet migration (FEAT-gxppx1): the runner in
// internal/datastore is started here, after database.Migrate and before the
// listener opens, so a datastore this build can no longer serve is either
// migrated or the process refuses to boot — the same contract a failed schema
// migration has.
//
// Every process role runs it (api, worker, both): a worker executing the
// datastore node reads the same physical tables and needs them migrated too.
// Concurrent boots are safe, because the runner re-reads each datastore under
// the step's own transaction, so two processes booting together apply each
// step once.
//
// The first pass is synchronous on purpose. It blocks the listener, so a
// large fleet lengthens boot rather than serving half-migrated tables, and it
// honours ctx, so a SIGTERM mid-pass ends the boot with a context error.
// After it, a slow tick repeats the same idempotent pass: a datastore an
// older peer created behind after this process's first pass would otherwise
// keep GET /api/v1/ready at 503 until a restart, and readiness failures do
// not restart pods.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// fleetMigrator is the part of *datastore.Engine the boot pass uses, so the
// error wrapping and the retry loop are testable without a live database.
type fleetMigrator interface {
	MigrateFleet(ctx context.Context) (int, error)
}

// fleetResumeInterval is how often the boot pass is repeated.
//
// Slow on purpose: the pass exists to clear a straggler an older peer created
// during a rolling upgrade, which is a minutes-scale event, and once the
// fleet is current a pass costs one GROUP BY over the catalogue. No config
// key: an operator has nothing to tune here, and a knob nobody sets is a
// support surface with no owner.
const fleetResumeInterval = 30 * time.Second

// migrateDatastoreFleet runs one pass and reports what it moved. The error
// keeps the runner's own sentence, which names the datastore and both
// versions, behind a prefix saying what this process was doing.
func migrateDatastoreFleet(ctx context.Context, migrator fleetMigrator, log *slog.Logger) error {
	migrated, err := migrator.MigrateFleet(ctx)
	if err != nil {
		return fmt.Errorf("migrate the datastore fleet: %w", err)
	}
	if migrated > 0 {
		log.Info("migrated datastores to the current schema version",
			"datastores", migrated, "schema_version", datastore.CurrentSchemaVersion)
	}
	return nil
}

// runFleetResumer repeats the pass until ctx ends.
//
// A failure is logged and retried rather than fatal: boot already proved the
// fleet migratable, and stopping the server over one datastore would take
// every other tenant's automation down with it. Readiness reports what is
// still outstanding, so an operator sees the retry failing.
func runFleetResumer(ctx context.Context, migrator fleetMigrator, every time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := migrateDatastoreFleet(ctx, migrator, log); err != nil {
				log.Error("resuming the datastore fleet migration", "error", err)
			}
		}
	}
}

// bootDatastoreFleet migrates the fleet before anything serves traffic and
// starts the resume tick behind it. The error is returned as it is, so run()
// prints the same refusal a failed database.Migrate produces.
func bootDatastoreFleet(ctx context.Context, engine *datastore.Engine, log *slog.Logger) error {
	if err := migrateDatastoreFleet(ctx, engine, log); err != nil {
		return err
	}
	go runFleetResumer(ctx, engine, fleetResumeInterval, log)
	return nil
}
