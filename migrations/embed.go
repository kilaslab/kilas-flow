// Package migrations carries KilasFlow's schema as versioned SQL the binary
// embeds, so a release ships the exact DDL it will run and an operator can
// review it before pointing KilasFlow at a database they share with something
// else.
//
// Files are named <version>_<name>.up.sql and <version>_<name>.down.sql and are
// split by dialect. The dialects share version numbers but not files: SQLite
// has no ALTER TABLE ... ALTER COLUMN, the byte and timestamp types differ, and
// some later migrations are PostgreSQL-only, so a single dialect-neutral file
// would have to be abandoned partway through and renumbered.
//
// Indent with spaces, never tabs. SQLite stores a CREATE TABLE verbatim and
// glebarez/sqlite's DDL parser counts a tab as a quote character, so a
// tab-indented table reads back as having no columns and GORM's migrator
// concludes the whole table is missing.
package migrations

import "embed"

// FS holds every migration for every dialect. The runner in internal/database
// selects the subdirectory matching the dialect it is connected to.
//
// The pattern names the directories rather than the files so that adding a
// migration never means remembering to extend a list here.
//
//go:embed sqlite postgres
var FS embed.FS
