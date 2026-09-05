---
id: FEAT-vwzd6r
title: Add isolated PostgreSQL MySQL and SQLite workflow nodes
status: done
priority: high
labels:
    - database
    - sql
    - nodes
    - credentials
deps:
    - FEAT-pn3dtq
parent: EPIC-c7gbdp
phase: p3
created: "2026-08-29T15:41:32Z"
updated: "2026-09-05T02:10:00Z"
---

## Scope

Add user-configured database workflow nodes and SQL credential types on top of the shared engine/credential boundary. They are external data sources, never a backdoor to KilasFlow internal persistence.

## Acceptance criteria

- PostgreSQL, MySQL, and SQLite are registered node types with typed, metadata-driven configuration and matching credential types.
- The workflow node connection path is separate from the internal GORM database handle; no default, inferred, or selectable connection can expose the internal KilasFlow SQLite/PostgreSQL database.
- Query/execute semantics, parameter binding, transaction scope, result-to-item mapping, errors, timeouts, and connection cleanup are explicitly documented and covered for every supported driver.
- SQLite credentials require an explicit permitted file path and reject the internal database path; tests prove the protection.
- Integration coverage runs representative successful and failing queries against each advertised database driver, with secrets redacted from output and diagnostics.

## References

- PRD: §§24 Database, 34, 57–58; Milestone 3; Definition of Done item 12.
- Design reference: `22-credential-modal-basic-auth.png` for credential attachment/scoping interaction pattern, not visual copying.

## Relevant documentation

- Use `find-docs` for current PostgreSQL/MySQL/SQLite Go driver APIs and any test-container library. Record exact driver versions and official docs used.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.

## Implementation Plan

- `internal/sqlnode` builds every connection from credential fields and opens it through `database/sql`, never through the internal GORM handle. There is no default, inferred, or selectable connection, so a workflow can only reach a database whose credential someone deliberately created.
- Credential types `postgres`, `mysql`, and `sqlite` carry the field metadata that drives the generic credential form; node types `kilasflow.postgres`, `kilasflow.mysql`, and `kilasflow.sqlite` each accept exactly their own driver's credential.
- A SQLite credential names a file on the server's own disk, so it is guarded: the path must be explicit and plain, and is canonicalized and compared against KilasFlow's own database before a connection is opened.

## Work Evidence

- Drivers: `github.com/jackc/pgx/v5` (stdlib), `github.com/go-sql-driver/mysql` v1.9.3, `github.com/glebarez/go-sqlite` — all through `database/sql`.
- Internal-database protection is proven, not asserted: `TestSQLiteRefusesKilasFlowsOwnDatabase` covers the exact path, its `-wal`/`-shm`/`-journal` siblings, a relative spelling, and a `..`-dotted one; `TestSQLiteRefusesASymlinkPointingAtTheInternalDatabase` covers a link that a plain string comparison would have let through. `TestDatabaseNodeCannotOpenTheInternalDatabase` proves the same at the node level.
- Canonicalization resolves symlinks through the *directory* rather than the file, so a target that does not exist yet — a WAL sidecar — still normalizes the same way. Resolving only the existing file left `/var/...` and `/private/var/...` looking different on macOS, which is exactly the gap a guard must not have; that bug was caught by the WAL case and fixed.
- Explicit-path requirement: an absent, empty, `:memory:`, `file:`-URI, or query-carrying path is refused, because a URI form could attach another database.
- Parameter binding is proven rather than described: `TestQueryBindsParametersRatherThanInterpolatingThem` sends `Ada'; DROP TABLE customers; --` as a bound value, asserts it matches zero rows, and then asserts the table still exists.
- Semantics covered per operation: query returns rows as items with `[]byte` decoded to strings and NULL as nil; execute reports rows affected; transaction commits together and rolls back together, with the rollback verified by counting rows afterwards. Row limits surface a `$truncated` marker rather than letting a partial read look complete. Timeouts, a cancelled context, a failing statement, an empty statement, and an unsupported driver all report errors.
- Connection cleanup: one connection per node run, closed on every path including failure, and `Close` is idempotent so a deferred close after an early return is safe.
- Secret redaction in diagnostics: PostgreSQL and MySQL both echo their DSN on a connection failure, and that DSN carries the password the credential store just decrypted. `sanitize` strips it, proven by `TestPostgresAndMySQLFailToConnectWithoutLeakingTheirPassword` and `TestDatabaseNodeReportsAFailingStatementWithoutLeakingTheCredential`.
- Two catalogue tests that hardcoded the node list were rewritten to assert the ordering and uniqueness contract instead, so adding a node no longer forces an unrelated edit into the commit.
- `go test ./... -race`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
