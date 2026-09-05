---
id: FEAT-r6xhnp
title: Prefix every table and index for shared databases
status: todo
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-gvn62x
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:01:08Z"
updated: "2026-09-05T05:01:08Z"
---

## Scope

The PostgreSQL decision on record is that KilasFlow may live inside a customer's existing database under a `kflow_` table prefix. The obvious mechanism for that is GORM's `NamingStrategy.TablePrefix`, and it will not work. `gorm.Open` in `internal/database/database.go` passes a `gorm.Config` with no `NamingStrategy` at all today, and adding one would change nothing: all seven models in `internal/repository/models.go` implement `TableName() string`, and `schema.ParseWithSpecialTableName` in gorm v1.31.2 tests the `Tabler` interface *before* `TablerWithNamer` and before falling back to the namer. A model that implements `Tabler` has its literal return value used verbatim, so `TablePrefix` is never consulted. Setting it would look like configuration and be a no-op.

Index names are the other half, and they are the half that actually breaks an install rather than quietly doing nothing. In PostgreSQL an index name is an object in the schema, not scoped to its table, so two applications in one database cannot both own `idx_workflows_tenant_updated`. Thirteen index names are written as literals in the struct tags: `idx_webhook_bindings_workflow`, `uidx_webhook_bindings_route`, `idx_schedules_tenant_workflow`, `idx_schedules_next_run`, `idx_credentials_tenant_name`, `idx_workflows_tenant_updated`, `idx_workflow_versions_tenant_workflow`, `uidx_workflow_versions_revision`, `idx_executions_tenant_started`, `idx_executions_tenant_workflow`, `idx_node_runs_tenant_execution`, `uidx_node_runs_attempt` and `uidx_node_runs_sequence`. (The roadmap said eleven; there are thirteen.) Five further indexes come from bare `index` tags — on `workflows.deleted_at`, `executions.workflow_version_id`, `lease_owner`, `lease_expires_at` and `cancellation_requested_at` — and those are named by `NamingStrategy.IndexName` as `idx_<table>_<column>`, so they inherit the prefix through the table name for free, as do the `fk_<table>_<relation>` constraint names from `RelationshipFKName`. Only the thirteen literals need deliberate work.

One thing makes this cheaper than it looks: nothing in `internal/repository` names a table in a string. There are no `.Table("…")`, `Raw` or `Exec` calls anywhere in the package — every query goes through a model. So the prefix has exactly two surfaces, the model type names and the migration SQL that p6-1 makes authoritative.

## Acceptance criteria

- [ ] `database.table_prefix` is a configuration key, empty by default, and the entire schema — tables, indexes and constraints — is created under it.
- [ ] With the prefix set to `kflow_`, a KilasFlow install coexists in one PostgreSQL database with a host application that owns its own `workflows`, `executions` and `credentials` tables and an index literally named `idx_workflows_tenant_updated`.
- [ ] Every query the repository layer issues resolves to the prefixed tables; no code path reaches an unprefixed name.
- [ ] The prefix is fixed for the life of an install: startup refuses to run when the configured prefix does not match what the database already holds, instead of silently creating a second empty schema alongside the populated one.
- [ ] A test enumerates every table, index and constraint identifier the migrations create and asserts each begins with the configured prefix, covering the generated names as well as the written ones.
- [ ] Every identifier stays within PostgreSQL's 63-byte limit once the prefix is applied.
- [ ] With the default empty prefix the resulting identifiers are byte-identical to today's, so an existing install is untouched.

## Implementation Plan

Take p6-1 first: once migrations are authoritative, the struct tags no longer create anything. That is the trap in this ticket. Editing the thirteen index names in the tags will feel like the work and will change nothing at run time — the migration SQL is what exists in the database. The tags remain useful only as documentation and as the input to the p6-1 drift test, so change both and let the drift test hold them together.

For the models there are two routes. The first converts each of the seven to `TablerWithNamer` — `func (workflowModel) TableName(namer schema.Namer) string` returning `namer.TableName("…")`. That keeps GORM as the single naming authority, which is why it is the recommendation: the generated index and constraint names then pick up the prefix automatically. The catch is that `NamingStrategy.TableName` runs `toDBName` and `inflection.Plural` over its argument, so each of the seven current names has to be checked for round-trip identity before trusting it — `workflow_versions`, `webhook_bindings` and `execution_node_runs` in particular — and any that does not survive must be spelled differently. Prove that with a table-driven test, not by reading it. The second route keeps `TableName() string` and has it read a prefix configured once in the `repository` package; it is simpler and it silently fails to prefix anything GORM generates, which is why it is not recommended.

For the thirteen literal index names, prefer deleting the explicit name wherever GORM's generated `idx_<table>_<columns>` is acceptable, and keeping a written name only where a composite index genuinely needs one. Those survivors get their prefix in the migration file.

Configuration: add `table_prefix` to `config.Database` and thread it to both `database.Open` (for the namer) and the migration runner (for the SQL). Validate it — letters, digits and underscore only, ending in `_` — before it is ever interpolated into DDL.

Two things this ticket must state plainly rather than leave implied. A prefix is a naming convention, not an isolation boundary: anything holding the KilasFlow connection can still read every prefixed table, which is why p6-5 exists. And the configured prefix has to be reachable at run time by code that is not GORM, because p6-4 needs it to key a `LISTEN/NOTIFY` channel and any advisory lock — both of which are per-database, not per-schema.

## References

- Roadmap plan, p6 section, entry V2-p6-2: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `internal/repository/models.go` — the seven `TableName() string` methods and the thirteen literal index names.
- `internal/database/database.go` — `gorm.Open` with no `NamingStrategy`.
- `gorm.io/gorm@v1.31.2/schema/schema.go`, `ParseWithSpecialTableName` — `Tabler` is tested before `TablerWithNamer`.
- `gorm.io/gorm@v1.31.2/schema/naming.go` — `NamingStrategy.TableName`, `IndexName`, `RelationshipFKName`.
