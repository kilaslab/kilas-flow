---
id: FEAT-gvn62x
title: Replace AutoMigrate with versioned migrations
status: done
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-hv4q8e
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:00:36Z"
updated: "2026-09-05T16:28:49Z"
---

## Scope

`internal/database/database.go`'s `Migrate` is the only schema authority in the product, and it is one call to `db.AutoMigrate(models...)` — the single `AutoMigrate` in the whole repository — driven from `cmd/kilasflow/main.go`'s `migrate` helper, which hands it the seven models `repository.Models()` returns. The `migrations/` directory the PRD asks for exists and holds nothing but `.gitkeep`. PRD §54 already requires versioned migrations, numbered up/down pairs, working on both SQLite and PostgreSQL. This ticket pays that debt.

AutoMigrate is additive only. It creates tables, adds columns and adds indexes; it never drops one, never renames one, never changes a column's type and never backfills data. Every remaining ticket in this phase is a schema change AutoMigrate cannot express: a table and index prefix for shared databases, an index on `executions.status`, execution pruning, and pgvector tables. Workflow history in p7 adds tables of its own. Nothing downstream can be built on reflection-driven migration.

The sharper reason is the PostgreSQL posture itself. The decision on record is that KilasFlow may share a customer's database. An ORM that reads the live schema at every boot and reshapes it to match a Go struct is not something you point at someone else's database — the operator has to be able to read, review and stage the exact DDL before it runs. Versioned migration files are that artifact.

This ticket changes no table, column or index. It moves the authority for the schema from struct reflection to files, reproducing today's shape exactly, so that an existing install and a fresh install end up byte-identical and every later ticket has a place to write DDL.

## Acceptance criteria

- [ ] `database.Migrate` no longer calls `AutoMigrate`; the schema is created only by numbered migration files under `migrations/`.
- [ ] A fresh SQLite database and a fresh PostgreSQL database both reach the same seven-table schema from the baseline migration, proven by `make smoke-sqlite` and `make smoke-postgres`.
- [ ] An existing install whose schema was created by AutoMigrate adopts the migration baseline without data loss and without re-creating or altering any table.
- [ ] Applied migrations are recorded in the internal database, and two processes starting at the same moment cannot apply the same migration twice.
- [ ] A binary started against a database at a schema version it does not know refuses to serve and says which version it found and which it expects.
- [ ] Migrations run inside the shipped distroless image with `CGO_ENABLED=0`.
- [ ] A test compares `repository.Models()` against the migration baseline and fails when a model gains a field with no corresponding migration, so drift is caught in CI rather than at run time.

## Implementation Plan

Decide first whether to take a dependency. The two candidates are a hand-rolled runner over an `embed.FS` of `.sql` files plus a `schema_migrations` table, or goose (`github.com/pressly/goose/v3`). Recommend goose: it accepts an existing `*sql.DB` — which `db.DB.DB()` already returns — and an `embed.FS`, so it never selects a driver of its own and the build keeps `CGO_ENABLED=0` and `glebarez/go-sqlite`. golang-migrate is the wrong choice here: its SQLite driver is built on `mattn/go-sqlite3`, which is cgo, and pulling it in would break the distroless image. A hand-rolled runner is defensible given how small the schema is, but versioning, down-migrations and the concurrent-start lock are already solved in goose and are exactly the parts that are tedious to get right.

Split the files by dialect: `migrations/sqlite/` and `migrations/postgres/`, sharing version numbers. A single dialect-neutral file is tempting and will not survive this phase — SQLite has no `ALTER TABLE ... ALTER COLUMN`, the byte and timestamp types differ (`BLOB`/`DATETIME` against `bytea`/`timestamptz`), and the tickets that follow are explicitly Postgres-only. Making the split now costs one directory; retrofitting it later costs a renumbering.

Generate the baseline rather than writing it. Boot the current binary against a fresh SQLite file and a fresh PostgreSQL, dump each schema, and use those dumps as migration `000001`. This is the step that protects existing installs, and it is where the trap sits: GORM generates identifiers the models never spell out. `schema.NamingStrategy.IndexName` names the bare `index` tags `idx_<table>_<column>` — `idx_workflows_deleted_at`, `idx_executions_lease_owner`, `idx_executions_lease_expires_at`, `idx_executions_cancellation_requested_at`, `idx_executions_workflow_version_id` — and `RelationshipFKName` names the foreign keys `fk_<table>_<relation>`. Hand-written SQL that invents different names will look correct, pass its own tests, and then make the prefix rename in the next ticket unable to find the objects it needs to rename.

Adoption for an existing install is a stamp, not a run. Detect the case where the tables exist but the version table does not, record version 1 as applied, and do nothing else. Get this wrong and the first upgrade in the field tries to `CREATE TABLE` over live data.

Finally, `Migrate(db *DB, models ...any)` loses its reason to take models. Keep the argument only as the input to the drift test and update `cmd/kilasflow/main.go`'s `migrate` helper to match, or delete the parameter and give the drift test its own entry point — either is fine, but do not leave a signature that implies the models still drive anything.

## References

- Roadmap plan, p6 section, entry V2-p6-1: `.pine/roadmap.md`.
- PRD §54 Migration Strategy, `gflow-prd-v1.md`, which already names `migrations/000001_initial.up.sql` and requires both SQLite and PostgreSQL.
- `internal/database/database.go` — `Migrate`, the only `AutoMigrate` call.
- `cmd/kilasflow/main.go` — the `migrate` helper.
- `internal/repository/models.go` — `Models()` and the seven model structs.
- `gorm.io/gorm@v1.31.2/schema/naming.go` — `NamingStrategy.IndexName` and `RelationshipFKName`, the source of the generated identifiers.

## Work evidence

### What changed

`database.Migrate` no longer reflects over Go structs. The schema now comes from
`migrations/sqlite/` and `migrations/postgres/`, embedded by the new root
`migrations` package and applied by a runner in `internal/database/migrate.go`.

- `internal/database/migrate.go` — new. Loads `<version>_<name>.<direction>.sql`
  per dialect, records applied versions in `schema_migrations`, adopts an
  AutoMigrate-created install by stamping rather than running, refuses to start
  against a database newer than the binary, and exposes `Rollback` so the down
  files are executed rather than merely shipped.
- `migrations/embed.go`, `migrations/{sqlite,postgres}/000001_baseline.{up,down}.sql`
  — new. `migrations/.gitkeep` removed.
- `internal/database/database.go` — `Migrate(db, models ...any)` and the repo's
  only `AutoMigrate` call are gone.
- `cmd/kilasflow/main.go` — the `migrate` helper is gone; `run()` calls
  `database.Migrate(db, log)` directly, so migrations are logged.
- 24 call sites in `internal/{api,engine,repository,scheduler,webhook}` tests
  updated to the new signature; `scripts/smoke-postgres.sh` now runs
  `-run 'Postgres'` (it named `TestMigratePostgres`, which no longer exists).

### The baseline was generated, not written

A throwaway recorder (a `gormlogger.Interface` that captures every statement)
ran `AutoMigrate(repository.Models()...)` against a fresh SQLite file and a
fresh PostgreSQL, and its `CREATE`/`ALTER` output is the baseline verbatim. The
tool was deleted afterwards. This is what keeps the generated identifiers — the
`idx_*` index names and `fk_*` constraint names no model spells out — exactly as
they are on every existing install.

### Verification

`go build ./...`, `go vet ./...` and `gofmt -l .` (excluding `web/`) are clean.

Full suite, with live PostgreSQL 16, MySQL 8 and MariaDB 11 on 55433/55434/55435
(`.pine/memory/live-databases.md`): every package `ok`, including
`internal/database 9.315s` and `nodes 14.826s`.

`internal/database` alone, PostgreSQL gate on — 26 tests, all pass, 6 of them
against the live server:

    TestAFreshSQLiteDatabaseGetsTheWholeBaselineSchema
    TestAFreshPostgresDatabaseGetsTheWholeBaselineSchema
    TestBaselineLeavesAutoMigrateNothingToDoOnSQLite
    TestBaselineLeavesAutoMigrateNothingToDoOnPostgres
    TestAnAutoMigratedInstallIsStampedRatherThanRebuilt
    TestAnAutoMigratedPostgresInstallIsStampedRatherThanRebuilt
    TestMigratingTwiceChangesNothingTheSecondTime
    TestConcurrentStartsApplyTheBaselineExactlyOnce
    TestConcurrentPostgresStartsApplyTheBaselineExactlyOnce
    TestADatabaseAheadOfTheBinaryRefusesToStart
    TestRollingBackTheBaselineLeavesNoKilasFlowTables
    TestRollingBackThePostgresBaselineLeavesNoKilasFlowTables
    TestMigratingAfterARollbackRebuildsTheSchema
    TestAFailedMigrationRecordsNothing
    TestEveryMigrationShipsBothDirectionsForBothDialects
    TestTheBaselineCreatesTheSameTablesInBothDialects
    TestMigrationFilesIndentWithSpacesRatherThanTabs
    (+ parseMigrationName, splitStatements and dialect cases)

### The tests were proven to fail without the change

Each break was applied, observed and reverted.

- Delete `timezone` from the SQLite baseline (the "a model gained a field with no
  migration" case): `TestBaselineLeavesAutoMigrateNothingToDoOnSQLite` fails with
  ``AutoMigrate wanted to run: ALTER TABLE `schedules` ADD `timezone` text NOT NULL DEFAULT ""``.
- Rename `idx_webhook_routes_route` to `uidx_webhook_routes_route` (the generated
  identifier trap): the drift test fails, and
  `TestAFreshSQLiteDatabaseGetsTheWholeBaselineSchema` reports
  `the baseline did not create the "idx_webhook_routes_route" index`.
- Disable `adoptExistingSchema`: both stamping tests fail with
  ``table `workflows` already exists`` / `relation "workflows" already exists
  (SQLSTATE 42P07)` — exactly the CREATE-TABLE-over-live-data failure this ticket
  warned about.

### Two real bugs the live databases found

- PostgreSQL's `CREATE TABLE IF NOT EXISTS` is **not** safe against a concurrent
  `CREATE` of the same name. With four starters,
  `TestConcurrentPostgresStartsApplyTheBaselineExactlyOnce` failed three of four
  with `duplicate key value violates unique constraint
  "pg_type_typname_nsp_index" (SQLSTATE 23505)`. `ensureVersionTable` now
  re-checks `HasTable` after a failure. SQLite never reproduces this.
- Indenting the migration SQL with tabs makes every table rebuild itself on
  every boot: glebarez/sqlite's DDL parser counts a tab as a quote character, so
  a tab-indented `CREATE TABLE` reads back as having no columns. Files use
  spaces, and `TestMigrationFilesIndentWithSpacesRatherThanTabs` pins it.

### Distroless and CGO_ENABLED=0

`make smoke-sqlite` and `make smoke-postgres` could not run here: neither builds
without `web/node_modules`, which this worktree does not have, and `web/` was out
of scope. The equivalent was proven directly instead.

`make build` (which is `CGO_ENABLED=0`) then booting against a fresh SQLite file
logs `applied migration version=1 name=baseline`, answers `/api/v1/health` and
`/api/v1/ready`, and leaves 9 tables, 24 indexes and `schema_migrations` holding
`1|baseline`. A restart against the same file applies nothing.

A `CGO_ENABLED=0 GOOS=linux` binary in `gcr.io/distroless/static-debian12:nonroot`
— the image the Dockerfile ships — migrates and serves on both drivers:

    {"msg":"database connected","driver":"sqlite"}
    {"msg":"applied migration","version":1,"name":"baseline"}
    {"msg":"database connected","driver":"postgres"}
    {"msg":"applied migration","version":1,"name":"baseline"}

and PostgreSQL afterwards holds the nine tables plus `schema_migrations` with
`1 | baseline`.

### Stale in this ticket

- **"the seven models `repository.Models()` returns"** and **"the same
  seven-table schema"** — it returns **nine**: `workflows`, `workflow_versions`,
  `executions`, `execution_node_runs`, `credentials`, `webhook_bindings`,
  `webhook_routes`, `webhook_deliveries`, `schedules`. The References section
  repeats "the seven model structs". The baseline covers all nine.
- **The recommendation to use goose is not viable.** `go get
  github.com/pressly/goose/v3@v3.27.3` forces `modernc.org/sqlite` v1.23.1 →
  v1.54.0 and `modernc.org/libc` v1.22.5 → v1.74.3 underneath
  `glebarez/go-sqlite v1.21.2`, and `go build ./...` then fails outright
  (`missing go.sum entry for module providing package
  github.com/ncruces/go-strftime`). The plan's premise — that goose "never
  selects a driver of its own" so the build keeps `glebarez/go-sqlite` — is
  wrong: goose depends on `modernc.org/sqlite` directly, which is the engine
  glebarez wraps. The runner is hand-rolled, as the plan's own fallback allowed.
- **The list of generated identifiers is incomplete in a load-bearing way.** It
  names five `idx_*` indexes but not `idx_webhook_routes_route`, the bare
  `uniqueIndex` on `webhook_routes.Route` — which GORM names with the `idx_`
  prefix *despite being unique*, unlike every other unique index in the schema
  (`uidx_*`, which are named explicitly in the model tags). Following the
  ticket's own reasoning would have produced `uidx_webhook_routes_route` and
  broken the rename in the next ticket. It is called out in both baseline files.
- The `.gitkeep`-only `migrations/` directory, the single `AutoMigrate` call in
  `internal/database/database.go`, and the `migrate` helper in
  `cmd/kilasflow/main.go` (lines 319-321) were all accurate.
