package datastore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Concurrency semantics.
//
// Ten workers run executions at once out of the box, and every one of them
// can touch the same datastore table in the same instant. The rules:
//
//   - A filtered Update, Delete, Clear or Increment is one statement. One
//     statement is atomic per row on both drivers, so concurrent writers
//     never interleave inside a row: the last writer wins the row, and no
//     write is silently half-applied. The semantics are identical on SQLite
//     and PostgreSQL by construction, not by configuration.
//   - Nothing here takes a row lock, on either driver. GORM's clause.Locking
//     compiles to SELECT ... FOR UPDATE on PostgreSQL and to nothing at
//     all on SQLite — the dialector discards it without an error — so
//     identical Go source would promise two different guarantees. No
//     datastore write path uses it, and a test pins the absence. That
//     describes the row-store write paths: the fleet runner's catalogue
//     re-read (advance in fleet.go) is the one deliberate FOR UPDATE, on
//     PostgreSQL only, and it claims no lock on SQLite.
//   - Upsert is read-then-write in no single transaction: two concurrent
//     upserts against the same filter may both insert. A counter or a flag
//     that must not lose writes uses Increment, or a preconditioned write
//     with a retry, never a read-modify-write through Get and Update.
//   - Limit checks are advisory under races: two inserts may both pass the
//     row probe. The probe keeps the common case bounded; exactness under
//     contention belongs to the statements, not the checks.
//
// A preconditioned write is compare-and-swap on updatedAt, the column every
// physical table already carries: the write lands only if the row is
// untouched since the caller read it, reusing the conditional-updates shape
// ClaimNext trusts. The precondition is single-row on purpose — bulk
// conditional writes have no sane partial-application story — so a filter
// matching anything but one row is an error, and matching nothing is the
// same empty result an unconditional write returns.

// ErrPreconditionConflict is matched with errors.Is when a preconditioned
// write loses to a concurrent one. The row is left exactly as the winner
// wrote it; nothing is merged and nothing is retried here.
var ErrPreconditionConflict = errors.New("datastore: write precondition failed")

// PreconditionError is the conflict: what the caller based its write on and
// what the row holds now, so the caller can retry without a second read.
type PreconditionError struct {
	DatastoreID string
	Expected    time.Time
	Current     time.Time
}

func (e *PreconditionError) Error() string {
	return fmt.Sprintf("datastore: row in %q changed concurrently (updatedAt %s, write assumed %s): refusing the stale write",
		e.DatastoreID, e.Current.UTC().Format(time.RFC3339Nano), e.Expected.UTC().Format(time.RFC3339Nano))
}

// Unwrap lets errors.Is see the sentinel through the detail.
func (e *PreconditionError) Unwrap() error { return ErrPreconditionConflict }

// Increment adds delta to a number column on every row matching the filter,
// in one statement. NULL counts as zero, so incrementing an empty cell
// starts the counter at delta. The statement is atomic per row on both
// drivers: ten concurrent increments land ten times, which the concurrent
// test pins. After-images come from the ids matched before the write, so
// the result names exactly the rows this call touched.
func (e *Engine) Increment(ctx context.Context, tenantID, dsID string, filter *Filter, column string, delta float64) (*UpdateResult, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	bound, err := canonicalValues(cols, map[string]any{column: delta})
	if err != nil {
		return nil, err
	}
	var name string
	var coerced any
	for name, coerced = range bound {
	}
	definition := columnDefinition(cols, name)
	if definition.Type != ColumnNumber {
		return nil, fmt.Errorf("datastore: increment needs a number column, %q is %s", name, definition.Type)
	}
	dialect := e.dialect()
	before, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(before) == 0 {
		return &UpdateResult{}, nil
	}
	ids := make([]int64, 0, len(before))
	holders := make([]string, 0, len(before))
	args := []any{coerced}
	for _, row := range before {
		id := row["id"].(int64)
		ids = append(ids, id)
		holders = append(holders, "?")
		args = append(args, id)
	}
	statement := "UPDATE " + quoteIdent(dialect, table) +
		" SET " + quoteIdent(dialect, name) + " = COALESCE(" + quoteIdent(dialect, name) + ",0)+?" +
		", " + quoteIdent(dialect, "updatedAt") + " = " + stampNow(dialect) +
		" WHERE " + quoteIdent(dialect, "id") + " IN (" + strings.Join(holders, ",") + ")"
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: increment rows: %w", res.Error)
	}
	after, err := e.rowsByIDs(ctx, dialect, table, cols, ids)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{Matched: res.RowsAffected, Rows: after}, nil
}

// UpdateWithPrecondition sets the given columns on the single row matching
// the filter, but only if its updatedAt still equals expectedUpdatedAt —
// the stamp a previous read returned. A concurrent write moves the stamp,
// the predicate matches nothing, and the caller gets a PreconditionError
// carrying the current stamp instead of an overwrite. A concurrently
// deleted row is the same empty result an unconditional update returns:
// there is nothing to be stale about.
func (e *Engine) UpdateWithPrecondition(ctx context.Context, tenantID, dsID string, filter *Filter, values map[string]any, expectedUpdatedAt time.Time) (*UpdateResult, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("datastore: update needs at least one column")
	}
	bound, err := canonicalValues(cols, values)
	if err != nil {
		return nil, err
	}
	if err := checkValueSizes(bound, e.limits.MaxValueBytes); err != nil {
		return nil, err
	}
	dialect := e.dialect()
	before, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(before) == 0 {
		return &UpdateResult{}, nil
	}
	if len(before) != 1 {
		return nil, fmt.Errorf("datastore: preconditioned update needs exactly one matching row, got %d", len(before))
	}
	set := make([]string, 0, len(bound)+1)
	var args []any
	for _, col := range cols {
		value, ok := bound[col.Name]
		if !ok {
			continue
		}
		set = append(set, quoteIdent(dialect, col.Name)+" = ?")
		args = append(args, value)
	}
	set = append(set, quoteIdent(dialect, "updatedAt")+" = "+stampNow(dialect))
	clause, filterArgs, err := buildFilterClause(dialect, cols, filter)
	if err != nil {
		return nil, err
	}
	condition, stamp := preconditionPredicate(clause, dialect, expectedUpdatedAt)
	statement := "UPDATE " + quoteIdent(dialect, table) + " SET " + strings.Join(set, ",") + condition
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, append(append(args, filterArgs...), stamp)...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: preconditioned update: %w", res.Error)
	}
	if res.RowsAffected == 1 {
		after, err := e.rowsByIDs(ctx, dialect, table, cols, []int64{before[0]["id"].(int64)})
		if err != nil {
			return nil, err
		}
		return &UpdateResult{Matched: 1, Rows: after}, nil
	}
	// The predicate matched nothing: either the row moved under us or it is
	// gone. A re-read tells the two apart without a second round trip from
	// the caller.
	current, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return &UpdateResult{}, nil
	}
	return nil, &PreconditionError{DatastoreID: dsID, Expected: expectedUpdatedAt, Current: latestUpdatedAt(current)}
}

// DeleteWithPrecondition removes the single row matching the filter, but
// only if its updatedAt still equals expectedUpdatedAt. Stale and missing
// behave exactly as the update variant documents.
func (e *Engine) DeleteWithPrecondition(ctx context.Context, tenantID, dsID string, filter *Filter, expectedUpdatedAt time.Time) (*DeleteResult, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	dialect := e.dialect()
	before, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(before) == 0 {
		return &DeleteResult{}, nil
	}
	if len(before) != 1 {
		return nil, fmt.Errorf("datastore: preconditioned delete needs exactly one matching row, got %d", len(before))
	}
	clause, filterArgs, err := buildFilterClause(dialect, cols, filter)
	if err != nil {
		return nil, err
	}
	condition, stamp := preconditionPredicate(clause, dialect, expectedUpdatedAt)
	statement := "DELETE FROM " + quoteIdent(dialect, table) + condition
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, append(filterArgs, stamp)...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: preconditioned delete: %w", res.Error)
	}
	if res.RowsAffected == 1 {
		return &DeleteResult{Deleted: 1, Rows: before}, nil
	}
	current, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return &DeleteResult{}, nil
	}
	return nil, &PreconditionError{DatastoreID: dsID, Expected: expectedUpdatedAt, Current: latestUpdatedAt(current)}
}

// latestUpdatedAt is the newest stamp across re-read rows: what a refused
// caller bases its retry on. Rows always carry updatedAt as a time.Time
// from the read path; anything else is skipped rather than trusted.
func latestUpdatedAt(rows []Row) time.Time {
	var latest time.Time
	for _, row := range rows {
		if stamp, ok := row["updatedAt"].(time.Time); ok && stamp.After(latest) {
			latest = stamp
		}
	}
	return latest.UTC()
}

// preconditionPredicate extends a filter clause with the updatedAt
// predicate, returning the fragment (starting with WHERE or AND) and the
// stamp text to bind. The comparison runs at the storage resolution on both
// drivers: binding a Go time.Time directly compares differently-formatted
// text on SQLite — where updatedAt is second-precision CURRENT_TIMESTAMP
// text — and matches nothing even for the stamp a read just returned.
// Pass back a stamp a read returned rather than time.Now: anything finer
// than the storage resolution cannot have come from the store.
func preconditionPredicate(clause, dialect string, expected time.Time) (string, string) {
	column := quoteIdent(dialect, "updatedAt")
	var predicate, stamp string
	if dialect == "postgres" {
		predicate = "date_trunc('milliseconds'," + column + ") = CAST(? AS timestamptz)"
		stamp = expected.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	} else {
		predicate = "strftime('%Y-%m-%d %H:%M:%f'," + column + ") = ?"
		stamp = expected.UTC().Format("2006-01-02 15:04:05.000")
	}
	if clause == "" {
		return " WHERE " + predicate, stamp
	}
	return clause + " AND " + predicate, stamp
}

// columnDefinition returns the catalogue entry for a canonical name the
// caller already resolved. It is only called with names canonicalValues
// returned, so the fallthrough is unreachable rather than an error.
func columnDefinition(cols []ColumnDef, name string) ColumnDef {
	for _, col := range cols {
		if col.Name == name {
			return col
		}
	}
	return ColumnDef{Name: name}
}
