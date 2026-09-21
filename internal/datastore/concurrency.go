package datastore

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
//   - An id-addressed upsert is one statement too: INSERT ... SELECT ...
//     WHERE ... ON CONFLICT (id) DO UPDATE ... RETURNING. The id is the
//     auto-increment primary key, so ON CONFLICT needs no new constraint and
//     two concurrent upserts of the same id insert once. The SELECT's WHERE
//     clause is mandatory — SQLite's parser needs it to tell ON CONFLICT
//     from a join — and doubles as the row-limit gate.
//   - Insert is one statement — INSERT ... RETURNING with the full read
//     projection — and returns the row that call wrote, scanned from the
//     statement's own RETURNING output rather than re-selected. The SQLite
//     readback used to be a second checkout running SELECT
//     last_insert_rowid(), which is per-connection state.
//   - Filtered Update and Delete write in one statement, but the rows they
//     return are read before and after it, so under contention those rows
//     can show a neighbour's write. Insert, Increment and an id-addressed
//     upsert return the statement's own images.
//   - Upsert matched on any other column is read-then-write in no single
//     transaction: two concurrent upserts against the same filter may both
//     insert. A counter or a flag that must not lose writes uses Increment,
//     or a preconditioned write with a retry, never a read-modify-write
//     through Get and Update.
//   - Nothing here takes a row lock, on either driver. GORM's clause.Locking
//     compiles to SELECT ... FOR UPDATE on PostgreSQL and to nothing at
//     all on SQLite — the dialector discards it without an error — so
//     identical Go source would promise two different guarantees. No
//     datastore write path uses it, and a test pins the absence. That
//     describes the row-store write paths: the fleet runner's catalogue
//     re-read (advance in fleet.go) is the one deliberate FOR UPDATE, on
//     PostgreSQL only, and it claims no lock on SQLite.
//   - Limit checks are advisory under races: two inserts may both pass the
//     row probe. The probe keeps the common case bounded; exactness under
//     contention belongs to the statements, not the checks.
//
// updatedAt is strictly increasing per row: every write takes the greater of
// the wall clock and one millisecond past the row's current stamp, so two
// writes never share a stamp. That is what makes "untouched since I read it"
// a sound question, and it is why a row can run slightly ahead of the wall
// clock during a sustained burst of writes to it.
//
// A preconditioned write is compare-and-swap on updatedAt, the column every
// physical table already carries: the write lands only if the row is
// untouched since the caller read it, reusing the conditional-updates shape
// ClaimNext trusts. The precondition is single-row on purpose — bulk
// conditional writes have no sane partial-application story — so a filter
// matching anything but one row is an error, and matching nothing is the
// same empty result an unconditional write returns. The predicate is
// `id = ? AND updatedAt = ?`: the id the pre-read approved, not the caller's
// filter, so the statement can touch at most that one row however many rows
// the filter matches by the time it runs, and any affected-count but 1 is an
// error rather than a quiet "nothing matched".

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
// starts the counter at delta. A non-number column is refused by the same
// binding check every other value goes through — canonicalValues coerces the
// delta against the catalogue first, so a string, boolean or date column
// never reaches SQL — and that refusal is the single documented path. The
// statement is atomic per row on both drivers: ten concurrent increments land
// ten times, which the concurrent test pins. RETURNING makes the result each
// statement's own post-image, so a racing writer's later value can never come
// back instead of the caller's own; the rows are sorted by id because
// RETURNING order is not defined.
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
	dialect := e.dialect()
	clause, filterArgs, err := buildFilterClause(dialect, cols, filter)
	if err != nil {
		return nil, err
	}
	statement := "UPDATE " + quoteIdent(dialect, table) +
		" SET " + quoteIdent(dialect, name) + " = COALESCE(" + quoteIdent(dialect, name) + ",0)+?" +
		", " + quoteIdent(dialect, "updatedAt") + " = " + stampNow(dialect, table) +
		clause + " RETURNING " + quotedProjection(dialect, cols)
	e.emit(statement)
	sqlRows, err := e.db.WithContext(ctx).Raw(statement, append([]any{coerced}, filterArgs...)...).Rows()
	if err != nil {
		return nil, fmt.Errorf("datastore: increment rows: %w", err)
	}
	rows, err := scanRows(sqlRows, cols)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["id"].(int64) < rows[j]["id"].(int64) })
	if rows == nil {
		rows = []Row{}
	}
	return &UpdateResult{Matched: int64(len(rows)), Rows: rows}, nil
}

// UpdateWithPrecondition sets the given columns on the single row matching
// the filter, but only if its updatedAt still equals expectedUpdatedAt —
// the stamp a previous read returned. The write is addressed by the approved
// row's id, not by the caller's filter, so a second row that matches the
// filter when the statement runs cannot be caught by it: at most one row is
// ever touched, whatever the filter matches now. A concurrent write moves the
// stamp, the predicate matches nothing, and the caller gets a
// PreconditionError carrying the current stamp instead of an overwrite. A
// concurrently deleted row is the same empty result an unconditional update
// returns: there is nothing to be stale about.
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
	approvedID := before[0]["id"].(int64)
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
	set = append(set, quoteIdent(dialect, "updatedAt")+" = "+stampNow(dialect, table))
	condition, stamp := preconditionPredicate(dialect, approvedID, expectedUpdatedAt)
	statement := "UPDATE " + quoteIdent(dialect, table) + " SET " + strings.Join(set, ",") + condition
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, append(args, approvedID, stamp)...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: preconditioned update: %w", res.Error)
	}
	switch {
	case res.RowsAffected == 1:
		after, err := e.rowsByIDs(ctx, dialect, table, cols, []int64{approvedID})
		if err != nil {
			return nil, err
		}
		return &UpdateResult{Matched: 1, Rows: after}, nil
	case res.RowsAffected == 0:
		// The approved row is gone, or its stamp moved under us: reading it
		// back by id tells the two apart without a second round trip from
		// the caller.
		current, err := e.rowsByIDs(ctx, dialect, table, cols, []int64{approvedID})
		if err != nil {
			return nil, err
		}
		if len(current) == 0 {
			return &UpdateResult{}, nil
		}
		return nil, &PreconditionError{DatastoreID: dsID, Expected: expectedUpdatedAt, Current: latestUpdatedAt(current)}
	default:
		// The statement is addressed by the primary key, so this count is
		// impossible; refuse it loudly rather than report a write that
		// touched other rows as "nothing matched".
		return nil, fmt.Errorf("datastore: preconditioned update affected %d rows, want exactly the approved one", res.RowsAffected)
	}
}

// DeleteWithPrecondition removes the single row matching the filter, but
// only if its updatedAt still equals expectedUpdatedAt. The statement is
// addressed by the approved row's id, exactly as the update variant is, so
// no other row the filter matches can be deleted. Stale and missing behave
// exactly as the update variant documents.
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
	approvedID := before[0]["id"].(int64)
	condition, stamp := preconditionPredicate(dialect, approvedID, expectedUpdatedAt)
	statement := "DELETE FROM " + quoteIdent(dialect, table) + condition
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, approvedID, stamp)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: preconditioned delete: %w", res.Error)
	}
	switch {
	case res.RowsAffected == 1:
		return &DeleteResult{Deleted: 1, Rows: before}, nil
	case res.RowsAffected == 0:
		current, err := e.rowsByIDs(ctx, dialect, table, cols, []int64{approvedID})
		if err != nil {
			return nil, err
		}
		if len(current) == 0 {
			return &DeleteResult{}, nil
		}
		return nil, &PreconditionError{DatastoreID: dsID, Expected: expectedUpdatedAt, Current: latestUpdatedAt(current)}
	default:
		return nil, fmt.Errorf("datastore: preconditioned delete affected %d rows, want exactly the approved one", res.RowsAffected)
	}
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

// preconditionPredicate is the WHERE clause of a preconditioned write: the id
// of the row the pre-read approved, ANDed with the updatedAt
// compare-and-swap. The statement is addressed by that id rather than by the
// caller's filter, so however many rows the filter matches when the statement
// runs, at most the approved one can be touched; the filter's single-row rule
// stays a pre-flight check. It returns the clause and the stamp text to bind.
// The comparison runs at the storage resolution on both drivers: binding a Go
// time.Time directly compares differently-formatted text on SQLite — where
// updatedAt is second-precision CURRENT_TIMESTAMP text — and matches nothing
// even for the stamp a read just returned. Pass back a stamp a read returned
// rather than time.Now: anything finer than the storage resolution cannot
// have come from the store.
func preconditionPredicate(dialect string, id int64, expected time.Time) (string, string) {
	column := quoteIdent(dialect, "updatedAt")
	var predicate, stamp string
	if dialect == "postgres" {
		predicate = "date_trunc('milliseconds'," + column + ") = CAST(? AS timestamptz)"
		stamp = expected.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	} else {
		predicate = "strftime('%Y-%m-%d %H:%M:%f'," + column + ") = ?"
		stamp = expected.UTC().Format("2006-01-02 15:04:05.000")
	}
	return " WHERE " + quoteIdent(dialect, "id") + " = ? AND " + predicate, stamp
}
