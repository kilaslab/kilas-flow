// Package datastore turns a datastore definition into one physical table.
//
// A datastore is the n8n storage model: one physical table per datastore,
// created by runtime DDL under the configured table prefix, with the
// definition held in the datastores and datastore_columns catalogue tables
// that migration 000005 creates. The promise is that a host application can
// read a datastore with ordinary SQL, which only holds if the names are
// stable, bounded and predictable.
//
// Ordering inside every write is catalogue first, DDL second, in one
// transaction. Both drivers execute DDL transactionally (unlike MySQL, which
// the workflow SQL nodes can reach but this engine never touches), so a DDL
// failure leaves no catalogue row and a catalogue failure leaves no physical
// table. Saying so here matters because a reader arriving from
// internal/sqlnode works against databases where that does not hold.
//
// The physical table name comes from a short opaque surrogate, never from
// the public datastore id: internal/workflow.NewID returns a prefix plus a
// 36-character UUIDv7, and prefix plus table plus that id leaves nothing of
// PostgreSQL's 63-byte budget for an index suffix. PostgreSQL truncates an
// over-length identifier without erroring, so the budget is asserted by
// tests, not by inspection.
//
// Rows live here too (FEAT-nrfg6e): rows.go stores and queries rows over
// these tables at n8n's filter surface, with keyset pagination on the
// integer id and dry-run pairs computed, never rolled back. fleet.go runs
// the per-datastore version steps (FEAT-gxppx1), re-reading the work list
// so mid-run creates are picked up. Schema evolution stays online
// (FEAT-wkmv5e): add column is one statement per dialect, never a rebuild.
// The catalogue HTTP API belongs to its own ticket, not this package.
package datastore
