---
id: FEAT-cpdp8y
title: Prove the Datastore path end to end on both drivers
status: todo
priority: high
labels:
    - e2e
    - testing
    - datastore
deps:
    - FEAT-cx3hq1
    - FEAT-3xqky1
    - FEAT-agj52c
    - FEAT-nc6z9r
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:06:02Z"
updated: "2026-09-05T12:06:02Z"
---

## Scope

p9 builds the Datastore across sixteen tickets: a storage engine creating a real physical table per datastore by runtime DDL, a catalogue, a row store with n8n's filter shape and keyset pagination, schema evolution, a per-table migration runner, limits, concurrency semantics, a management API, an editor surface, a node, an agent tool, isolation and redaction rules, n8n Data Table import, and CSV.

It is the phase with the most ways to be subtly wrong, because the correctness properties are all about what happens *between* components rather than inside one:

- **Identifier safety spans DDL and the catalogue.** PostgreSQL truncates over-length identifiers without erroring, so the physical table name comes from a short opaque surrogate and never from the public datastore id, and every index is named deterministically rather than by PostgreSQL. A Go test can assert the naming function; only a full path proves the name that reaches the database is the one that function produced.
- **Isolation is expressed through the catalogue, not the table.** A physical datastore table carries no `tenant_id` column, so cross-tenant refusal depends entirely on the catalogue lookup being correct on every path — API, editor, node and agent tool. That is four call sites, and a test per site is the only way to know all four hold.
- **Column identifiers are attacker-reachable.** `internal/webhook/webhook.go` puts attacker-controlled JSON into `$json` on every inbound request, so an expression-capable column slot is identifier injection sourced from a webhook body needing no workflow-edit rights. V2-p9-10 makes column names literal-only and re-validates in the executor after `expression.Resolve`. Proving that requires driving a webhook payload through a workflow into a datastore write, which is an end-to-end path by definition.
- **Row contents must not reach the execution trace.** V2-p9-12 settles that a datastore write records row counts and identifiers, not row contents, because nothing prunes traces and a tenant's datastore delete would otherwise not delete the data.
- **Concurrency is a read-modify-write race by design.** `execution.max_concurrent` defaults to 10 and the most popular real use of this feature is a cross-run key-value store. V2-p9-6 settles the semantics; only concurrent execution proves them.

The management API is also the surface a host application drives, so this suite is where the SDK's datastore methods from V2-p10-7 get exercised against a real server rather than a stubbed fetch.

## Acceptance criteria

- [ ] A datastore is created through the editor, columns are added and edited, and rows are written and read back, all through the UI.
- [ ] A workflow node writes rows and reads them with a filter, and the results are observed through the execution record rather than by querying the database directly.
- [ ] A webhook payload carrying a hostile column name is refused by the executor's re-validation, and the datastore's schema is unchanged afterwards.
- [ ] A second tenant cannot read, write or enumerate the first tenant's datastores through the API, the editor, the node or the agent tool, with a case per surface.
- [ ] A datastore write records row counts and identifiers in the execution trace and not row contents, asserted against the stored trace.
- [ ] Concurrent executions writing the same row produce the semantics V2-p9-6 settles, proven by running them concurrently rather than sequentially.
- [ ] The SDK's datastore methods drive the same operations against a real server, including the required-filter rule on row delete and cursor pagination to exhaustion.
- [ ] A workflow imported from an n8n export that used a Data Table binds to a datastore and runs, and the case where the referenced table has no counterpart produces a diagnostic rather than a silent success.

## Implementation Plan

Run this suite against both drivers. The Datastore is the one feature whose behaviour genuinely differs between SQLite and PostgreSQL by design — identifier limits, `DROP COLUMN` restrictions, byte accounting availability — and a suite that runs only on the default SQLite path would miss the entire class of PostgreSQL identifier defects that V2-p9-1 exists to prevent. `scripts/smoke-postgres.sh` already establishes how to bring up a database and point the app at it.

Write the isolation cases first, before the happy path. They are the ones that must not regress, they are cheap once the harness can create two tenants, and writing them first prevents the common failure where isolation tests are added last against a design that made them awkward.

For the identifier-safety property, assert against the database's own catalogue rather than against the application's belief. Query `information_schema` on PostgreSQL for the actual table and index names and check them against the bound; the whole defect class is that the application believes an index exists that PostgreSQL silently declined to create under a truncated name.

The concurrency case needs care to be meaningful. Two executions started sequentially and merely overlapping in wall time do not test a race. Drive genuine concurrency — several executions triggered together against one row — and assert the invariant V2-p9-6 chose, whether that is atomic upsert or an optimistic-locking failure. A test that passes because the race did not happen is worse than no test.

One thing to keep out of scope: byte-level quota assertions. The roadmap settles that per-datastore byte accounting is a PostgreSQL-tier capability via `pg_total_relation_size` and unavailable on the pinned SQLite driver, which omits `ENABLE_DBSTAT_VTAB`. Assert count-based limits on both drivers and byte-based limits only on PostgreSQL, matching the tier language the epic uses elsewhere, rather than writing a test that can only ever pass on one driver and looks broken on the other.

## References

- Roadmap plan, p11 section, entry V2-p11-7: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the p9 section, including the identifier-budget reasoning and the tier asymmetry on byte accounting.
- `.pine/tickets/FEAT-ss44d9.md` — V2-p9-1, identifier safety and dialect-aware DDL.
- `.pine/tickets/FEAT-nrfg6e.md` — V2-p9-2, the filter shape and keyset pagination.
- `.pine/tickets/FEAT-1axhdn.md` — V2-p9-6, the concurrency semantics this suite asserts.
- `.pine/tickets/FEAT-3xqky1.md` — V2-p9-10, the node and its literal-only column names.
- `.pine/tickets/FEAT-cjpbe6.md` — V2-p9-12, isolation, the redaction carve-out and the trace policy.
- `.pine/tickets/FEAT-nch9dg.md` — V2-p9-13, the n8n Data Table import case.
- `.pine/tickets/FEAT-nc6z9r.md` — V2-p10-7, the SDK methods exercised here.
- `internal/webhook/webhook.go` — the attacker-controlled `$json` that makes the injection case real.
- `scripts/smoke-postgres.sh` — the PostgreSQL topology this suite reuses.
