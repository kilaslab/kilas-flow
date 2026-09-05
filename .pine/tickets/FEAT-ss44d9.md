---
id: FEAT-ss44d9
title: Build the Datastore storage engine with dialect-aware DDL
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-gvn62x
    - FEAT-r6xhnp
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

There is no `internal/datastore` package. A search for `datastore` across every Go file and the whole of `web/src` returns zero hits, and nothing in the product emits DDL at run time: `internal/database/database.go`'s `Migrate` is one call to `db.AutoMigrate(models...)` over the seven models `repository.Models()` returns. The owner has chosen the n8n storage model — one physical table per datastore, created by runtime DDL under the `kflow_` prefix, with a two-table catalogue — so this ticket builds the layer that turns a definition into a table.

It owns two catalogue models — `datastores`, and `datastore_columns` with an explicit integer `index` preserving column order — and a DDL service: create table with columns, drop table, add, rename and drop column, table exists. Physical types match n8n so a datastore stays portable: PostgreSQL `TEXT`, `DOUBLE PRECISION`, `BOOLEAN` and `TIMESTAMPTZ(3)`; SQLite `TEXT`, `REAL`, `BOOLEAN` as 0 or 1, and `DATETIME(3)`, with SQLite's date serialisation and boolean normalisation applied on read.

Identifier handling is the correctness boundary and the security boundary at once. `internal/workflow/ids.go` returns `prefix + "_" + value.String()` — a prefix plus a 36-character UUIDv7 — so a public datastore id is 39 bytes, and `kflow_` (6) plus `datastore_user_` (15) plus 39 is 60 of PostgreSQL's 63-byte limit, leaving three for an index suffix. The rule that follows is load-bearing: the physical table name comes from a short opaque surrogate and never from the public id. `kflow_ds_` plus sixteen hex characters is 25 bytes and leaves the budget wide open.

The remaining rules sit around that one. Column names match `^[a-zA-Z][a-zA-Z0-9_]*$` within 63 bytes, quoting doubles any embedded quote, no index is left for PostgreSQL to name, and `id`, `createdAt`, `updatedAt` and `dryRunState` are reserved case-insensitively.

This matters because the reason physical tables were chosen is that a host application can read a datastore with ordinary SQL. That promise is only worth making if the names are stable, bounded and predictable — an install where two datastores silently share an index is one where the host's own queries read a table nobody can describe.

## Acceptance criteria

- [ ] Creating a datastore produces one physical table named from a short opaque surrogate, and a test asserts the public datastore id appears in no table, index or constraint identifier.
- [ ] One test enumerates every identifier the DDL path can emit at the maximum configured table prefix and asserts each is within 63 bytes and the set stays pairwise unique after truncation to 63.
- [ ] Every index the service creates carries a name the service chose, proven by a test asserting that no emitted `CREATE INDEX` statement omits an index name.
- [ ] A column named `id`, `CreatedAt`, `UPDATEDAT` or `dryrunstate` is refused with a message naming the reserved word, proven by a case-permuted table-driven test.
- [ ] A column name outside `^[a-zA-Z][a-zA-Z0-9_]*$`, longer than 63 bytes, or differing from an existing column only in case is refused before any SQL is composed, proven by a test whose cases include an embedded double quote.
- [ ] The same definition yields the PostgreSQL and SQLite type sets named in Scope and a boolean round-trips as a Go `bool` on both, proven by a driver-parameterised test that reaches PostgreSQL when `KILASFLOW_TEST_POSTGRES_DSN` is set.
- [ ] A DDL failure leaves no catalogue row and a catalogue failure leaves no physical table, proven by a test injecting a failure at each point inside the single transaction.
- [ ] Dropping a datastore removes the catalogue rows and the physical table together, and a second drop of the same datastore is a no-op rather than an error, proven by a test.

## Implementation Plan

Build the identifier layer first — surrogate minting, quoting, the reserved-word check, deterministic index naming and the byte budget — in one file that never imports GORM. Everything else here composes SQL strings, and a naming rule added afterwards has to be retrofitted into statements already written and already passing their tests.

**Surrogate shape.** Two candidates: a per-tenant sequence, or sixteen hex characters. Recommend sixteen hex characters of a fresh random value, held in the catalogue under a unique constraint with a retry on conflict. Reject the per-tenant sequence: it needs a monotonic counter with its own transactional write path, which on SQLite contends for the single connection the whole process shares. Never derive the surrogate by hashing the datastore id — a hash is deterministic, so a collision is permanent rather than retryable. Name indexes from that surrogate and the column's catalogue `index` integer, never from the column name, because a 63-byte column name appended to a 25-byte table name is 90 bytes before any suffix.

Issue the DDL with `tx.Exec` over the identifier layer's output. Reject GORM's `db.Migrator()`: it reflects over a Go struct to decide columns and types, and a datastore has no struct. Both internal drivers execute DDL transactionally, so the ordering — catalogue write first, DDL second, one transaction — holds on both; say so in the package doc, because a reader arriving from `internal/sqlnode` has been working against MySQL, which does not.

The trap is that PostgreSQL truncates an over-length identifier to 63 bytes without erroring. Two index names differing only past byte 63 collapse into one, `CREATE INDEX IF NOT EXISTS` finds the truncated name taken and skips the second without complaint, and the service records an index that does not exist. Nothing fails; the query that needed it does a sequential scan for the life of the install, on a table the operator believes is indexed. Test the property, not examples: no hand-picked set contains the pair that collides.

Handle case deliberately. SQLite folds ASCII identifiers case-insensitively while PostgreSQL treats a quoted identifier as case-sensitive, so `"createdAt"` and `"CreatedAt"` are two columns on one driver and one on the other; reserving the four system names case-insensitively and rejecting user columns differing only in case is what makes a definition mean the same thing on both. One reference gap to close before writing the type table: `packages/cli/src/modules/` in the widened n8n checkout holds only `community-packages`, so the `data-table` module the roadmap cites is not on disk.

**Where the prefix comes from.** Recommend consuming `database.table_prefix` from V2-p6-2 through a single accessor rather than threading it into every DDL call — `config.Database` today carries only `Driver`, `DSN`, `MaxOpenConns` and `MaxIdleConns`, so the key does not exist yet and this ticket must not invent a second one. What would reopen it: V2-p6-2 landing with the prefix reachable only through GORM's namer, since the DDL path is not GORM and would then need its own accessor.

## References

- Roadmap plan, p9 section, entry V2-p9-1: `.pine/roadmap.md`.
- `internal/workflow/ids.go` — `NewID`, a prefix plus a 36-character UUIDv7, the identifier that must stay out of every physical name.
- `internal/database/database.go` — lines 57-63 pin SQLite to one connection for the whole process, and `Migrate` is the repository's only `AutoMigrate`.
- `internal/repository/models.go` — `Models()` and the seven `TableName() string` methods, the naming convention the two catalogue models follow.
- `internal/repository/workflows.go` — `TenantScope` at lines 21-23, and the `store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error` pattern the catalogue-plus-DDL transaction reuses.
- `internal/database/database_test.go` — lines 145-150, the `KILASFLOW_TEST_POSTGRES_DSN` gate and the cleanup that drops only `models[0..3]`, leaking `credentials`, `webhook_bindings` and `schedules`; correct it here, since V2-p9-0 leaves it to the first p9 ticket that opens the database layer.
- `.pine/tickets/FEAT-r6xhnp.md` (V2-p6-2) — the `table_prefix` cap and the identifier-budget criterion this DDL path must satisfy.
- `.pine/tickets/FEAT-gvn62x.md` (V2-p6-1) — the goose runner the catalogue tables join, and the migration authority the runtime-DDL carve-out is measured against.
- `go.mod` — `github.com/glebarez/sqlite v1.11.0` over `modernc.org/sqlite v1.23.1`, the pure-Go driver whose boolean and datetime handling the read path normalises.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 20-23 — a fresh table already carrying `id`, `createdAt` and `updatedAt`; the Add Column dialog offering no nullability, uniqueness or default; the four column types verbatim (`string`, `number`, `boolean`, `datetime`, where the create dialog says `datetime` and the filter list says `date` — `date` is the wire value, so do not copy the UI label into the contract); and a new row arriving as `id=1` with both timestamps set by the database. Captured from a live local n8n 2.x instance; gitignored, never vendored.
