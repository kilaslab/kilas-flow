---
id: FEAT-a94c8y
title: Guard the internal database from workflow SQL nodes
status: todo
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-r6xhnp
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:02:48Z"
updated: "2026-09-05T05:02:48Z"
---

## Scope

`databaseGuard` in `cmd/kilasflow/main.go` returns an empty `sqlnode.Guard{}` unless `cfg.Database.Driver` is `"sqlite"`. On a PostgreSQL install there is therefore no guard at all — and there could not be one, because `sqlnode.Guard` carries a single field, `InternalPaths []string`, which is a list of SQLite files. There is nothing in it for a PostgreSQL or MySQL target to be compared against.

The rest of `internal/sqlnode` matches. `dataSource` sends a PostgreSQL credential to `postgresDSN`, which assembles a DSN out of the credential's `host`, `port`, `user`, `password`, `database` and `sslMode` fields and returns it — no host check, no port check, no database-name check, nothing. `internal/safehttp`'s dial-time SSRF policy governs HTTP only; the SQL nodes never touch it. So the concrete defect is this: on a PostgreSQL install, a `kilasflow.postgres` node with a credential pointed at KilasFlow's own database reads `credentials` (sealed payloads plus `public_fields` in the clear), `workflows`, `workflow_versions` and every `executions` input and output, and can write or drop any of them. The same node can also reach anything else the process can reach on the network, including a metadata service or a neighbouring internal database — the thing `safehttp` exists to prevent for HTTP.

The SQLite guard itself is well built and is the model to widen, not to replace. `sqlitePath` demands an explicit path, rejects URI forms and `:memory:`, resolves to absolute, canonicalises through the parent directory so `/var` and `/private/var` compare equal on macOS, and blocks the `-wal`, `-shm` and `-journal` siblings that reach the same database by another name. The shape is right; it simply stops at one driver.

This is the ticket that makes the shared-database posture defensible, because a table prefix is a naming convention and not a boundary.

## Acceptance criteria

- [ ] For every driver, a SQL-node credential that resolves to KilasFlow's own internal database is refused with `ErrForbiddenTarget` and a message that names why, before any connection is opened.
- [ ] The PostgreSQL and MySQL guards compare a resolved target — address, port and database name, plus the table prefix where one is configured — rather than DSN text, so a different host spelling, a different port notation or a different `sslMode` for the same server is still caught.
- [ ] SQL nodes gain a host policy of their own: an operator-configured allow list, with loopback and private-network targets refused by default, matching the outbound HTTP posture instead of contradicting it.
- [ ] MySQL is covered by the same policy even though KilasFlow never uses MySQL internally, so an operator never has to reason about which drivers happen to be guarded.
- [ ] Existing SQLite guard behaviour is unchanged, including the `-wal`, `-shm` and `-journal` sibling checks and the symlink canonicalisation.
- [ ] The guard is proven live in `make smoke-postgres`: a workflow whose PostgreSQL credential points at KilasFlow's own database fails with the guard's message, and the same workflow against a different database succeeds.
- [ ] The configuration documentation states that a table prefix is a naming convention and not an isolation boundary, and documents a dedicated schema plus a role with no rights outside it as the only deployment that genuinely isolates KilasFlow from its host database.

## Implementation Plan

Widen `sqlnode.Guard` beyond file paths. Recommend it carry a set of normalised internal targets — driver, resolved address, port, database name — built once in `cmd/kilasflow/main.go` from `cfg.Database`, alongside the existing `InternalPaths`. That means parsing the internal PostgreSQL DSN there rather than passing it around as a string.

The trap is that comparing hostnames is not a guard. `localhost`, `127.0.0.1`, the Compose service name `postgres` and the host's own DNS name all reach the same server; `postgres://…/kilasflow` and `postgres://…/kilasflow?sslmode=disable` are the same target; an IPv6 literal and its IPv4-mapped form are the same address. Compare resolved addresses the way `internal/safehttp` does at dial time, not text. A text-only check is the version of this guard that gets bypassed by someone who is not even trying.

The second layer is the one that actually holds, and it should not be skipped just because the first exists. Add a host policy for SQL connections mirroring the `outbound` section: `allow_private_networks: false` by default and an `allowed_hosts` list, applied inside `sqlnode.Open` before `sql.Open`. Note the configuration trap already documented on `OutboundHTTP`: `envKeyToPath` treats the first underscore as the section separator, so the new section must be one word — `sqlnode`, not `sql_node` — or no `KILASFLOW_*` environment override can ever reach it.

Deployment documentation is part of this ticket, not a follow-up. A guard inside the process stops KilasFlow's own SQL nodes and nothing else; only database privileges stop everything else. Write down the configuration that actually isolates: KilasFlow's objects in a dedicated schema, owned by a role with no rights on the host application's schema, with `search_path` set on the KilasFlow connection.

One decision to settle rather than leave silent: whether an operator may deliberately opt a credential back in to the internal database, since reporting against one's own execution history is a real want. Recommend no. The guard stays unconditional, and an operator who wants that builds a read-only role — or points at a replica — and creates an ordinary credential for it. An opt-out flag would be the first thing anyone copies out of a forum post.

One consequence to record now that V2-p9-1 puts customer business data behind this guard. Datastore tables live in the internal database under the same prefix, so the guard's reach grows from credentials, workflows and executions to an unbounded set of runtime-created tables holding a tenant's own rows. The user-visible rule that follows is worth stating in the Datastore documentation rather than leaving it to surface as an error message: a workflow SQL node can never read a datastore, and the Datastore node is the only path to one. That also raises the stakes on the `file:`-prefixed DSN defect and the empty guard on non-SQLite drivers, both of which V2-p6-7 closes.

## References

- Roadmap plan, p6 section, entry V2-p6-5: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `cmd/kilasflow/main.go` — `databaseGuard`, which returns an empty guard for every non-SQLite driver, and `outboundPolicy` next to it as the shape to mirror.
- `internal/sqlnode/sqlnode.go` — `Guard`, `Open`, `dataSource`, `postgresDSN`, `mysqlDSN`, `sqlitePath`, `canonical`, `ErrForbiddenTarget`.
- `nodes/database.go` — `PostgresNodeType` (`kilasflow.postgres`), `MySQLNodeType`, `SQLiteNodeType` and `NewDatabaseExecutor`, which passes the guard through.
- `internal/safehttp/safehttp.go` — the dial-time address check this guard should reuse in spirit.
- `internal/config/config.go` — `OutboundHTTP` and the single-word section note on `envKeyToPath`.
- `scripts/smoke-postgres.sh`, `Makefile` target `smoke-postgres`.
