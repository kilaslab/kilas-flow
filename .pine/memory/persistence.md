---
topic: persistence
updated: 2026-09-05T16:28:03Z
---

# persistence

- 2026-09-05: Migration SQL files must be indented with SPACES, never tabs. glebarez/sqlite's DDL parser treats a tab as a quote character (sqliteSeparator includes \t), so a tab-indented CREATE TABLE parses as a table with zero columns; GORM's migrator then concludes every column is missing and rebuilds the table (copy, drop, rename) on every single boot. Nothing in the failure points at whitespace. internal/database's TestMigrationFilesIndentWithSpacesRatherThanTabs pins it.
- 2026-09-05: PostgreSQL's CREATE TABLE IF NOT EXISTS is NOT safe against a concurrent CREATE of the same name: the loser does not see the table appear, it fails with a duplicate key on pg_type_typname_nsp_index (SQLSTATE 23505). Any bootstrap DDL two processes might race on has to re-check HasTable after the error rather than trusting IF NOT EXISTS. Found by the live-PostgreSQL gate in internal/database (TestConcurrentPostgresStartsApplyTheBaselineExactlyOnce); SQLite does not reproduce it.
- 2026-09-05: goose (github.com/pressly/goose/v3) cannot be used as KilasFlow's migration runner: its module graph forces modernc.org/sqlite v1.23.1 -> v1.54.0 and modernc.org/libc v1.22.5 -> v1.74.3 underneath glebarez/go-sqlite v1.21.2, and 'go build ./...' then fails outright. The pure-Go SQLite stack this product ships on is pinned by glebarez, so any dependency that also depends on modernc.org/sqlite drags the database engine out from under it. The runner in internal/database/migrate.go is hand-rolled for this reason.
