---
id: FEAT-vwzd6r
title: Add isolated PostgreSQL MySQL and SQLite workflow nodes
status: todo
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
updated: "2026-08-29T15:41:32Z"
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
