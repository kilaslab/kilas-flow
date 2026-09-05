---
id: FEAT-gvn62x
title: Replace AutoMigrate with versioned migrations
status: todo
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
updated: "2026-09-05T05:00:36Z"
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

- Roadmap plan, p6 section, entry V2-p6-1: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- PRD §54 Migration Strategy, `gflow-prd-v1.md`, which already names `migrations/000001_initial.up.sql` and requires both SQLite and PostgreSQL.
- `internal/database/database.go` — `Migrate`, the only `AutoMigrate` call.
- `cmd/kilasflow/main.go` — the `migrate` helper.
- `internal/repository/models.go` — `Models()` and the seven model structs.
- `gorm.io/gorm@v1.31.2/schema/naming.go` — `NamingStrategy.IndexName` and `RelationshipFKName`, the source of the generated identifiers.
