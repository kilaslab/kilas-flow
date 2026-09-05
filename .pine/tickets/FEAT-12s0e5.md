---
id: FEAT-12s0e5
title: Bring the MySQL node to n8n's operation set
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-n5fdz3
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`databaseNode()` at `nodes/database.go:38` is one factory producing three definitions that differ in exactly four strings — a node type, an executor id, a display name and a credential type. `postgresNode()`, `mysqlNode()` and `sqliteNode()` (lines 84 to 94) each register `Version: workflow.V(1)` with the same three operations, `query`, `execute` and `transaction`. There is no dialect anywhere in the family: not in the definition, not in `DatabaseExecutor`, and not in `internal/sqlnode`, whose only per-driver code is two DSN builders and the SQLite path guard.

MySQL needs one the moment V2-p4-9's operation set exists, because those operations emit SQL rather than accept it. Identifiers are backtick-quoted, and an embedded backtick is escaped by doubling it. Placeholders are positional `?`, not `$1`. And there is no schema qualifier: MySQL's schema is its database, which is why the `mysql` credential at `internal/credentials/credentials.go:133-144` carries `host`, `port`, `database`, `user`, `password` and `tls` and nothing more, while `postgres` at 121 to 132 carries `sslMode` and would need a schema on top.

The option collection differs too — `connectionLimit`, `decimalNumbers`, `priority`, `selectDistinct` and `detailedOutput` have no Postgres counterpart, and their exact names and defaults must be read out of the reference checkout rather than transcribed. `connectionLimit` contradicts `sqlnode.Open` outright: `db.SetMaxOpenConns(1)` at `internal/sqlnode/sqlnode.go:100` pins every node run to one connection, deliberately.

The version story is smaller than the roadmap implies. n8n serves MySQL at `defaultVersion` 2.5 and Postgres at 2.7 — the numbers V2-p4-12 raises the export pins to — but there is no `defaultVersion` field to add here. `Registry.Resolve` at `internal/node/registry.go:131` already returns the highest registered version at or below the one requested, and the highest of all when a document requests none.

That leaves `kilasflow.sqlite`, which has no parity target because n8n ships no SQLite node, and is nonetheless not idle: `nodes/database_test.go` drives the whole family through `nodes.SQLiteNodeType`, transaction rollback and row truncation included. Whatever is done to the shared factory is done to the substrate the family's tests run on.

This matters because the customers this platform is aimed at run MySQL and MariaDB at least as often as PostgreSQL. A parity programme that reaches six operations on one dialect and leaves the other on hand-written SQL imports half the SQL nodes it advertises, and the half it misses fails at run time rather than at import.

## Acceptance criteria

- [ ] `kilasflow.mysql` registers the n8n operation set as a second type version while a document asking for version 1 still resolves to the query/execute/transaction shape, proven by a registry test.
- [ ] Every identifier a MySQL builder emits is backtick-quoted with embedded backticks doubled, proven by a fuzz test over adversarial table and column names asserting the emitted text.
- [ ] No MySQL builder output contains a `$1`-style placeholder or a qualified `schema.table` name, proven by a test that asserts the SQL text rather than the query result.
- [ ] The MySQL option collection carries n8n's own key set and defaults, differing from Postgres wherever n8n differs, asserted against the reference checkout by a table test.
- [ ] `connectionLimit` either changes the pool `sqlnode.Open` builds or is refused with a named diagnostic, so the option never reports a limit the runtime does not apply.
- [ ] `kilasflow.sqlite` keeps one registered version and exposes no operation string it has no implementation for, proven by a registry test over the whole database family.
- [ ] The existing `nodes/database_test.go` suite passes unchanged against SQLite, showing the dialect extraction altered no behaviour the family already had.
- [ ] The MySQL integration run against a disposable MariaDB is executed by hand and its output recorded in the ticket's work evidence, because this repository has no CI of any kind.

## Implementation Plan

Extract the dialect before writing a single operation. Every later decision here — quoting, placeholders, qualifier policy, the option collection, the version to register — hangs off which database is being addressed, and `databaseNode()` currently encodes that as four unrelated strings passed by three call sites. Turning those four into one dialect value is what makes the rest expressible, and it is a pure refactor with an existing suite standing behind it.

Recommend a `dialect` value carrying a quoting function, a placeholder function, a qualifier policy and the option collection, passed once into both the definition factory and the SQL builders. Reject a `switch driver` inside each builder: the Postgres and MySQL difference would then live in a dozen places, and the next dialect — or the Datastore DDL in p9 — would have to find all of them. One value, constructed once, is also the only shape a fuzz test can be written against.

The trap is the schema qualifier. MySQL reads `` `a`.`b` `` as *database* dot table, not schema dot table, so a shared builder that emits a qualified name for MySQL does not fail — it silently addresses a different database on the same server, one the credential very often can reach. Nothing errors, the row count is plausible, and the wrong table is read or written. Hence the criterion asserting emitted SQL text: a result-based test against a single-database fixture cannot see this defect at all.

`?` binds need no such guard. `go-sql-driver/mysql` rejects `$1` outright, so a placeholder mistake is loud on the first run; the qualifier is the only dialect difference that fails quietly.

Leave the DSN alone. `mysqlDSN` at `internal/sqlnode/sqlnode.go:150` writes `?parseTime=true` plus an optional `tls`, and neither `multiStatements` nor `interpolateParams` belongs in this ticket — batching is V2-p4-11's, and both flags change the binding contract the package comment at `internal/sqlnode/sqlnode.go:1-7` exists to hold. Note too that MySQL's `LAST_INSERT_ID()` and `ROW_COUNT()` are connection-scoped and are correct today only because the pool is pinned at line 100; an operation that reads them must record that dependency where the pool is configured, not where the SQL is built.

**The SQLite fork.** Recommend keeping `kilasflow.sqlite` on the shared factory at version 1 and never registering a version 2 for it. There is no parity target, no corpus fixture and no import path that produces one, so a locally invented SQLite operation set would be six SQL builders nothing measures — while the node's real job, being the family's test substrate, wants it identical to what the other two were before the split. Reject a hand-written `nodes/database/sqlite.go` definition: it duplicates three properties and a validator to express a difference that does not exist. What would reopen it is the p9 Datastore work choosing SQLite as a first-class runtime-DDL target, at which point SQLite acquires a parity target of its own and the fork earns its cost.

## References

- Roadmap plan, p4 section, entry V2-p4-10: `.pine/roadmap.md`.
- `nodes/database.go` — `databaseNode` at line 38, the three constructors at 84 to 94, and `DatabaseCredentialType`.
- `internal/sqlnode/sqlnode.go` — the package comment's binding contract, `mysqlDSN` at line 150, `dataSource` at 112, and the pinned pool at line 100.
- `internal/credentials/credentials.go` — the `mysql` fields at 133 to 144, which carry no schema because MySQL has none, against `postgres` at 121 to 132.
- `internal/node/registry.go` — `Resolve` at line 131, which is what this codebase has instead of n8n's `defaultVersion`.
- `nodes/database_test.go` — the whole family's tests, every one of which opens a SQLite file through `nodes.SQLiteNodeType`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `packages/nodes-base/nodes/MySql/` for the option collection and the node's `defaultVersion`. The checkout is sparse and currently holds only `HttpRequest`, `If`, `Schedule` and `Set`, and V2-p0-1's widening list does not add the database nodes, so `packages/nodes-base/nodes/{Postgres,MySql}` must join the sparse-checkout before this ticket starts.
