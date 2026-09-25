---
id: BUG-xpr3jj
title: SQLite credentials open any path on the server, and a locked file hangs the test and the node run past every deadline
status: todo
priority: medium
labels:
    - security
    - credentials
    - sqlite
    - tenancy
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

`sqlitePath` (internal/sqlnode/sqlnode.go:699-754) refuses only KilasFlow's own database files. Any other absolute or relative path is opened, and created if missing. On a multi-tenant install, a tenant can therefore:
- read or write other tenants' SQLite files;
- create database files anywhere the process can write.

Live evidence from 2026-09-25:
- A credential with the path `<scratch>/created-by-tenant.db` tested ok and created the file.
- `../../../../etc/hosts` tested ok.
- `/etc/passwd` hung. `db.PingContext` ignores its deadline, because the driver blocks while opening a file another process has locked, and only statements are interruptible. The test's in-flight claim was never released, so every later test answered 409 "already running" permanently, and each attempt leaked a goroutine.
- A SQLite node run on such a path would hold a worker the same way.

# Acceptance Criteria
- [ ] SQLite credential paths are confined to a configured root, one directory per tenant. Absolute paths and escapes out of that directory (via `..` or a symlink) are refused, and only regular files are accepted.
- [ ] An operator setting controls the root, and the type can be disabled. The default is safe for a multi-tenant install, and there is a documented escape hatch for single-tenant installs. e2e specs that use a SQLite credential are updated.
- [ ] Opening and pinging a SQLite file is bounded: a test or node run returns at its deadline, and the claim is released.
- [ ] Tests cover confinement, symlink escape, a non-regular file, and the deadline.
