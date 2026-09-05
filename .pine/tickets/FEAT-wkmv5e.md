---
id: FEAT-wkmv5e
title: Evolve a Datastore schema without stalling the instance
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-ss44d9
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`internal/database/database.go`'s `Open` pins SQLite to one connection — `sqlDB.SetMaxOpenConns(1)` and `sqlDB.SetMaxIdleConns(1)` at lines 57-63, under the comment "SQLite tolerates only one writer." — and that fact decides this ticket. V2-p9-1 delivers add, rename and drop column as DDL primitives; what a user may ask for, and what each driver does when they ask, is settled here.

Delete carries the edge, and the edge is SQLite's. SQLite 3.41.2 — the version `modernc.org/sqlite@v1.23.1` compiles in, per its `doc.go:22-30` platform table — refuses `ALTER TABLE ... DROP COLUMN` when the column is indexed, unique, part of the primary key, named in a `CHECK` constraint, or referenced by a generated column, a trigger or a view, and it has no `ALTER COLUMN` at all. The only escape is the twelve-step rebuild: create a replacement table, copy every row, drop the original, rename, then rebuild indexes and triggers.

On a one-connection pool that rebuild is not slow, it is a stop. Every other caller — the API, the scheduler, the webhook server, each running execution — queues in `database/sql` rather than in SQLite, so the `busy_timeout(5000)` pragma at line 157 never applies and nothing ends the wait early. Deleting a column on a million-row datastore would halt the instance for a full table copy.

The recommended answer is that a column delete is a catalogue operation with physical reclamation deferred to an explicit maintenance action. That is not a workaround: PostgreSQL's own `DROP COLUMN` is a `pg_attribute` metadata edit that leaves the bytes in place until a later rewrite, so deferring makes the two drivers agree rather than diverge. Retype is the second decision, and it has no cheap form anywhere — n8n forbids it outright, PostgreSQL's `ALTER COLUMN ... TYPE ... USING` rewrites the table under an `ACCESS EXCLUSIVE` lock, and SQLite has no `ALTER COLUMN` to offer.

This matters because the editor promises that a datastore is editable, and an embedded host cannot be handed a delete button that may freeze the product for minutes with no progress and no cancel. What an operator sees, and how they choose the moment a rebuild happens, is as much the deliverable here as the DDL is.

## Acceptance criteria

- [ ] Adding a column is one statement on both drivers and existing rows read the new column back as null, proven by a test on SQLite and on PostgreSQL.
- [ ] Renaming a column preserves every stored value and the catalogue's integer `index` ordering, proven by reading the full row set back either side of the rename.
- [ ] Deleting a column removes it from the API, the editor and the node immediately while its data survives under a tombstoned physical name, proven by a test asserting both halves.
- [ ] A deleted column's name is immediately re-usable by a later add-column, and no value written before the delete is readable through the new column, proven by a write-delete-add-read test.
- [ ] No user-facing column operation performs a SQLite table rebuild; a request that would need one is refused with an error naming the maintenance action, proven by a test per restricted case.
- [ ] The maintenance action reclaims deferred columns and reports how many datastores it rebuilt and how long it held the SQLite connection, with that report captured as evidence on this ticket.
- [ ] A retype the driver cannot satisfy without a rewrite is queued for the maintenance action rather than run inline, proven by a table-driven test over every type pair.
- [ ] Deleting and re-adding a column survives `make smoke-sqlite` and `make smoke-postgres`, with the output recorded here.

## Implementation Plan

Settle how the catalogue represents a deleted column before writing any DDL, because every later question — name reuse, reclamation, CSV export, the n8n mapping — reads its answer from that representation.

**Physical name or opaque name.** Recommend that a column's physical name equals its logical name, and that delete therefore renames the physical column to a tombstone such as `zz_deleted_<n>_<name>`, freeing the logical name at once. Reject the tidier-looking alternative of opaque physical names — `c_1`, `c_2` — mapped through the catalogue, which would make delete and rename pure catalogue edits with no DDL at all: it destroys the reason physical tables were chosen, since the roadmap's case for them is that a host application can read a datastore with ordinary SQL, and nobody reads `c_7`. The tombstone rename is `ALTER TABLE ... RENAME COLUMN`, present on both drivers and a schema-text edit rather than a row rewrite, so the common path stays O(1).

The trap is `SELECT *`. A tombstoned column is still physically present, so any read that does not build an explicit column list from the catalogue silently returns deleted data — into API responses, into CSV exports, and into the execution trace V2-p9-12 shows nothing prunes. There is no error and no warning, just an extra column of values a tenant believed they had destroyed. Every read projects the catalogue's live columns by name, and one test asserts a tombstoned column never appears in any output shape.

Refuse rather than repair. A restricted case — an indexed, unique, primary-key or `CHECK`-referenced column on SQLite — is rejected on the user path with an error naming the maintenance action, never silently promoted into a rebuild. Derive the refusal from the catalogue and the driver, not from catching a driver error and reading its text.

Treat retype exactly like reclamation: both rewrite, so both belong to the maintenance action rather than to a click. Reject allowing retype inline on PostgreSQL only, where `ALTER COLUMN ... TYPE ... USING` is one statement — it takes `ACCESS EXCLUSIVE` for a full rewrite, and a feature whose blocking behaviour differs by driver is the asymmetry this phase keeps trying to avoid.

The maintenance action itself needs a bound and a report: one datastore at a time, a hard limit on how long a transaction may hold the SQLite connection, and a summary naming each datastore rebuilt and the time taken, so an operator can size the window before choosing it.

**Where the maintenance action lives.** Recommend an authenticated API operation over a CLI subcommand. `cmd/kilasflow/main.go` parses exactly two flags today — `-config` and `-version`, at lines 49-50 — so a subcommand means inventing a command shape for one caller, and a containerised install may have no shell to run it from. What reopens this is any requirement that reclamation run while the API is stopped; at that point the flag exists anyway and the endpoint becomes a second entry point onto the same code.

## References

- Roadmap plan, p9 section, entry V2-p9-3: `.pine/roadmap.md`.
- `internal/database/database.go:57-63` — the SQLite single-connection pin, and line 157 — the `busy_timeout(5000)` pragma that does not cover pool waits.
- `modernc.org/sqlite@v1.23.1/doc.go:22-30` — the platform table naming SQLite 3.41.2, the version this build actually runs.
- `go.mod:7-8` — `github.com/glebarez/go-sqlite v1.21.2` and `github.com/glebarez/sqlite v1.11.0`, the pure-Go driver chain that keeps `CGO_ENABLED=0`.
- `cmd/kilasflow/main.go:49-50` — the entire command-line surface, `-config` and `-version`.
- `.pine/tickets/EPIC-m42s3g.md:31` — the decisions-table entry sanctioning runtime DDL for datastores.
- `.pine/tickets/FEAT-r6xhnp.md` — V2-p6-2, the prefix and identifier-budget rules every generated name here inherits.
- `Makefile:126-128` and `Makefile:138-140` — `smoke-sqlite` and `smoke-postgres`, both run by hand and recorded per ticket.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 21 and 24 — the Add Column dialog, and the column header's `inline-editable-area`, which is how n8n exposes rename; nothing in the UI offers a type change, matching the documented n8n restriction. Captured from a live local n8n 2.x instance; gitignored, never vendored.
