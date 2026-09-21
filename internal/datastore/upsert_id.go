package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Explicit-id upsert (FEAT-1axhdn).
//
// A physical datastore table carries no unique constraint on its user
// columns — a datastore is a plain n8n-shaped table — so a filter-addressed
// upsert has nothing to conflict on and cannot honestly be one statement.
// What it does carry is `id`, the auto-increment primary key, so a filter
// that addresses a single row by id can compile to one
// INSERT ... ON CONFLICT (id) DO UPDATE statement on both drivers, with no
// new index and no changed storage semantics. That is the primitive this
// file adds; every other filter keeps the legacy read-then-write path.
//
// The single statement is not a convenience. On PostgreSQL the sequence is
// re-synced past an explicit id (inside the same statement, so a plain
// insert racing it cannot move the sequence backwards), and on both drivers
// the SELECT's WHERE clause is mandatory — SQLite's parser needs it to tell
// ON CONFLICT from a join — and doubles as the MaxRowsPerDatastore gate.

// maxExplicitID bounds a caller-supplied id. 2^53-1 is the largest integer a
// JSON number (float64) holds exactly, and it is also short of the point
// where the identity counter can be exhausted: creating a row at
// math.MaxInt64 made every later plain Insert fail permanently on both
// drivers — SQLite "database or disk is full (13)" and PostgreSQL "nextval:
// reached maximum value of sequence" — which a workflow keyValue or an embed
// session with datastore:write could do to a datastore.
const maxExplicitID int64 = 1<<53 - 1

// sequenceResyncKey is the wire-only column the PostgreSQL wrapper adds for
// the sequence re-sync expression. It is deleted before a row is returned,
// so it never appears in an output shape.
const sequenceResyncKey = "__seq"

// idFilter is the single-condition `id eq` filter the id-addressed paths
// build from an already-validated id.
func idFilter(id int64) *Filter {
	return &Filter{Type: "and", Conditions: []FilterCondition{
		{Column: "id", Condition: CondEq, Value: id},
	}}
}

// idAddressed reports whether the filter addresses exactly one row by its
// id. Anything else — several conditions, another column, another operator,
// an unparseable or out-of-range value — is false, so the legacy path keeps
// its errors and its read-then-write semantics unchanged.
func idAddressed(filter *Filter) (int64, bool) {
	if filter == nil || len(filter.Conditions) != 1 {
		return 0, false
	}
	condition := filter.Conditions[0]
	if !strings.EqualFold(condition.Column, "id") || condition.Condition != CondEq || condition.Value == nil {
		return 0, false
	}
	id, err := coerceIntID(condition.Value)
	if err != nil || id < 1 || id > maxExplicitID {
		return 0, false
	}
	return id, true
}

// UpsertByID creates the row at exactly the given id, or updates that row in
// place when it already exists, in one statement on both drivers:
//
//	INSERT INTO t (id, <supplied columns>)
//	SELECT <placeholders>
//	WHERE EXISTS (SELECT 1 FROM t WHERE id = ?) OR NOT EXISTS (SELECT 1 FROM t LIMIT 1 OFFSET ?)
//	ON CONFLICT (id) DO UPDATE SET <col> = excluded.<col>, ..., updatedAt = <bump>
//	RETURNING <projection>
//
// Only supplied columns are written on conflict, so an upsert never nulls a
// column it did not name. createdAt is left alone; updatedAt strictly
// increases. Inserted reports whether the statement created the row, decided
// from the returned image alone: on insert both timestamps default to the
// same database instant, on update updatedAt has moved.
//
// The id is an address, and it is bounded: an id outside 1..2^53-1 is
// refused before any SQL, because an id at MaxInt64 exhausts the identity
// counter and permanently breaks later plain inserts. A plain insert and an
// id-addressed upsert can still meet at the same number — a duplicate-key
// refusal for the insert, or an update of the row the insert just made — but
// the counter itself can never be bricked.
func (e *Engine) UpsertByID(ctx context.Context, tenantID, dsID string, id int64, values map[string]any, dryRun bool) (*UpsertResult, error) {
	if id < 1 || id > maxExplicitID {
		return nil, fmt.Errorf("datastore: explicit id %d is outside 1..%d", id, maxExplicitID)
	}
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	bound, err := canonicalValues(cols, values)
	if err != nil {
		return nil, err
	}
	if len(bound) == 0 {
		return nil, fmt.Errorf("datastore: upsert needs at least one column")
	}
	if err := checkValueSizes(bound, e.limits.MaxValueBytes); err != nil {
		return nil, err
	}
	dialect := e.dialect()
	if dryRun {
		return e.upsertByIDDryRun(ctx, dialect, table, cols, id, bound)
	}
	statement, args := upsertByIDStatement(dialect, table, cols, bound, id, e.limits.MaxRowsPerDatastore)
	e.emit(statement)
	sqlRows, err := e.db.WithContext(ctx).Raw(statement, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("datastore: upsert row by id: %w", err)
	}
	rows, err := scanRows(sqlRows, cols)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// The single statement's own row-limit gate refused: the id is
		// new and the table sits at MaxRowsPerDatastore, so it produced
		// no row. Same text as checkRowLimit.
		maximum := e.limits.MaxRowsPerDatastore
		return nil, fmt.Errorf("datastore: table holds %d rows, at the maximum %d", maximum, maximum)
	}
	row := rows[0]
	delete(row, sequenceResyncKey)
	inserted := false
	if created, ok := row["createdAt"].(time.Time); ok {
		if updated, ok := row["updatedAt"].(time.Time); ok {
			inserted = created.Equal(updated)
		}
	}
	return &UpsertResult{Inserted: inserted, Matched: 1, Rows: []Row{row}}, nil
}

// upsertByIDDryRun computes the before/after images the statement above
// would produce, without writing: a hit returns the pair, a miss returns one
// pair with a nil Before and an after-image carrying the id and the bound
// values. The id never travels through canonicalValues (it is the system
// column), so it is placed here.
func (e *Engine) upsertByIDDryRun(ctx context.Context, dialect, table string, cols []ColumnDef, id int64, bound map[string]any) (*UpsertResult, error) {
	matching, err := e.matchIDsAndRows(ctx, dialect, table, cols, idFilter(id))
	if err != nil {
		return nil, err
	}
	if len(matching) > 0 {
		pairs := make([]DryRunPair, 0, len(matching))
		for _, row := range matching {
			before := Row{DryRunState: "before"}
			after := Row{DryRunState: "after"}
			for k, v := range row {
				before[k] = v
				after[k] = v
			}
			for k, v := range bound {
				after[k] = v
			}
			pairs = append(pairs, DryRunPair{Before: before, After: after})
		}
		return &UpsertResult{Matched: int64(len(matching)), Pairs: pairs}, nil
	}
	after := Row{"id": id, DryRunState: "after"}
	for k, v := range bound {
		after[k] = v
	}
	return &UpsertResult{Inserted: true, Pairs: []DryRunPair{{After: after}}}, nil
}

// upsertByIDStatement is the pure statement builder: both dialects
// assertable without a server. It returns the statement and its bound args
// in placeholder order (id, supplied values in catalogue order, the
// existence-probe id, then maxRows-1 for the OFFSET gate).
//
// PostgreSQL wraps the insert in a CTE that re-syncs the bigserial: an
// explicit id does not advance the sequence, so a later plain insert would
// start at 1 and collide. The re-sync only fires when the statement actually
// inserted (createdAt equals updatedAt) and the id sits past the sequence's
// current value, and GREATEST is computed inside setval so a plain insert
// that advanced the sequence between the read and the setval cannot be moved
// backwards.
func upsertByIDStatement(dialect, table string, cols []ColumnDef, bound map[string]any, id int64, maxRows int) (string, []any) {
	q := func(name string) string { return quoteIdent(dialect, name) }
	supplied := make([]ColumnDef, 0, len(cols))
	args := []any{id}
	for _, col := range cols {
		if value, ok := bound[col.Name]; ok {
			supplied = append(supplied, col)
			args = append(args, value)
		}
	}
	columns := make([]string, 0, len(supplied)+1)
	columns = append(columns, q("id"))
	selects := make([]string, 0, len(supplied)+1)
	selects = append(selects, castPlaceholder(dialect, "?", "BIGINT"))
	for _, col := range supplied {
		typ, err := physicalType(dialect, col.Type)
		if err != nil {
			// Unreachable: canonicalValues accepted the column, so its
			// type came from the catalogue.
			typ = "TEXT"
		}
		columns = append(columns, q(col.Name))
		selects = append(selects, castPlaceholder(dialect, "?", typ))
	}
	updates := make([]string, 0, len(supplied)+1)
	for _, col := range supplied {
		updates = append(updates, q(col.Name)+" = excluded."+q(col.Name))
	}
	updates = append(updates, q("updatedAt")+" = "+stampNow(dialect, table))

	statement := "INSERT INTO " + q(table) +
		" (" + strings.Join(columns, ",") + ")" +
		" SELECT " + strings.Join(selects, ",") +
		" WHERE EXISTS (SELECT 1 FROM " + q(table) + " WHERE " + q("id") + " = ?)" +
		" OR NOT EXISTS (SELECT 1 FROM " + q(table) + " LIMIT 1 OFFSET ?)" +
		" ON CONFLICT (" + q("id") + ") DO UPDATE SET " + strings.Join(updates, ",") +
		" RETURNING " + quotedProjection(dialect, cols)
	args = append(args, id, maxRows-1)
	if dialect != "postgres" {
		return statement, args
	}

	sequence := "pg_get_serial_sequence('" + strings.ReplaceAll(table, "'", "''") + "','id')"
	outer := make([]string, 0, len(cols)+4)
	for _, name := range projectColumns(cols) {
		outer = append(outer, "up."+q(name))
	}
	outer = append(outer,
		"CASE WHEN up."+q("createdAt")+" = up."+q("updatedAt")+
			" AND up."+q("id")+" > COALESCE(pg_sequence_last_value("+sequence+"::regclass),0)"+
			" THEN setval("+sequence+", GREATEST(up."+q("id")+", COALESCE(pg_sequence_last_value("+sequence+"::regclass),0))) END AS "+q(sequenceResyncKey))
	return "WITH up AS (" + statement + ") SELECT " + strings.Join(outer, ",") + " FROM up", args
}

// castPlaceholder types a SELECT-list placeholder on PostgreSQL. A bare
// `SELECT ?` gives the server nothing to infer the parameter's type from
// (SQLSTATE 42P18), so every projection placeholder is cast to its physical
// type; SQLite needs no cast and takes the placeholder bare.
func castPlaceholder(dialect, placeholder, typ string) string {
	if dialect == "postgres" {
		return "CAST(" + placeholder + " AS " + typ + ")"
	}
	return placeholder
}
