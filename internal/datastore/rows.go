package datastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file is the row store (FEAT-nrfg6e): insert, get, list, update,
// upsert, delete and clear over the physical tables the engine creates.
// Every read projects the catalogue's live columns by name — never SELECT
// * — so a physically present but deleted column can never leak into an
// output shape. Every write resolves its columns against the catalogue
// before composing SQL, and every value binds as a ? placeholder.
//
// Upsert atomicity and row locking belong to a later ticket and the code
// says so where it matters: upsert here is read-then-write, not an atomic
// merge.

// Row is one datastore row: the system id, createdAt and updatedAt plus one
// entry per live user column. Values are normalised on read — numbers as
// float64, booleans as bool, dates as time.Time — whatever the driver
// stored.
type Row map[string]any

// DryRunState tags the rows of a dry-run pair: the fourth reserved name
// beside id, createdAt and updatedAt. It is wire-only, never physical.
const DryRunState = "dryRunState"

var (
	// ErrRowNotFound reports a Get for an id the table does not hold.
	ErrRowNotFound = errors.New("datastore: row not found")
	// ErrInvalidRowCursor reports a List cursor this encoder did not issue.
	ErrInvalidRowCursor = errors.New("datastore: invalid row cursor")
)

// DryRunPair is the before and after image of one row a dry run would
// touch. Both images carry DryRunState ("before"/"after"). A delete's After
// is a tombstone holding only the id and the tag; an upsert-that-inserts
// has a nil Before.
type DryRunPair struct {
	Before Row
	After  Row
}

// UpdateResult is the outcome of an Update: Matched rows, their committed
// after-images in Rows, or — when dryRun is set — the computed Pairs with
// the table left byte-identical.
type UpdateResult struct {
	Matched int64
	Rows    []Row
	Pairs   []DryRunPair
}

// DeleteResult is the outcome of a Delete: the committed before-images in
// Rows, or the computed Pairs when dryRun is set.
type DeleteResult struct {
	Deleted int64
	Rows    []Row
	Pairs   []DryRunPair
}

// UpsertResult is the outcome of an Upsert. Inserted tells whether the
// filter matched nothing and a row was inserted (or would be, on dry run).
type UpsertResult struct {
	Inserted bool
	Matched  int64
	Rows     []Row
	Pairs    []DryRunPair
}

// checkSchemaVersion refuses row operations on a datastore the engine must
// not serve: ahead of the version it serves (this build is too old, naming
// both versions) or behind it (the fleet migration has not reached it yet).
// Each refusal names both versions; every other datastore keeps serving. The
// target is the engine's own schemaVersion, the same value Create stamps and
// the fleet runner migrates towards, so the three cannot disagree.
func checkSchemaVersion(dsID string, version, target int) error {
	if version > target {
		return fmt.Errorf("datastore: %s is at schema version %d but this build knows version %d", dsID, version, target)
	}
	if version < target {
		return fmt.Errorf("datastore: %s is at schema version %d, want %d: the datastore fleet migration has not reached it yet (boot runs it; GET /api/v1/ready reports what is outstanding)",
			dsID, version, target)
	}
	return nil
}

// gatedLookup resolves the catalogue row, its live columns and its physical
// table, refusing unknown datastores and wrong-version ones before any row
// SQL is composed.
func (e *Engine) gatedLookup(ctx context.Context, tenantID, dsID string) (*datastoreModel, []ColumnDef, string, error) {
	row, cols, err := e.lookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, nil, "", err
	}
	if row == nil {
		return nil, nil, "", fmt.Errorf("datastore: unknown datastore %q", dsID)
	}
	if err := checkSchemaVersion(dsID, row.SchemaVersion, e.schemaVersion); err != nil {
		return nil, nil, "", err
	}
	return row, cols, PhysicalTableName(e.prefix, row.Surrogate), nil
}

// projectColumns is the explicit read list: the system id first, the live
// user columns in catalogue order, then the timestamps. Reads never use
// SELECT * — see the package note above.
func projectColumns(cols []ColumnDef) []string {
	names := make([]string, 0, len(cols)+3)
	names = append(names, "id")
	for _, col := range cols {
		names = append(names, col.Name)
	}
	names = append(names, "createdAt", "updatedAt")
	return names
}

// coerceColumnValue maps a caller-supplied value to the driver's bind type
// for the column. Nil stays nil (NULL). Anything else must fit the wire
// type; a mismatch is an error, never a silent format.
func coerceColumnValue(col ColumnDef, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch col.Type {
	case ColumnString:
		switch v := value.(type) {
		case string:
			return v, nil
		case []byte:
			return string(v), nil
		default:
			return nil, fmt.Errorf("datastore: column %q is a string, got %T", col.Name, value)
		}
	case ColumnNumber:
		switch v := value.(type) {
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int8:
			return float64(v), nil
		case int16:
			return float64(v), nil
		case int32:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case uint:
			return float64(v), nil
		case uint8:
			return float64(v), nil
		case uint16:
			return float64(v), nil
		case uint32:
			return float64(v), nil
		case uint64:
			return float64(v), nil
		default:
			return nil, fmt.Errorf("datastore: column %q is a number, got %T", col.Name, value)
		}
	case ColumnBoolean:
		switch v := value.(type) {
		case bool:
			return v, nil
		case int:
			return v != 0, nil
		case int64:
			return v != 0, nil
		default:
			return nil, fmt.Errorf("datastore: column %q is a boolean, got %T", col.Name, value)
		}
	case ColumnDate:
		switch v := value.(type) {
		case time.Time:
			return v.UTC(), nil
		case string:
			return parseDateValue(col.Name, v)
		case []byte:
			return parseDateValue(col.Name, string(v))
		default:
			return nil, fmt.Errorf("datastore: column %q is a date, got %T", col.Name, value)
		}
	default:
		return nil, fmt.Errorf("datastore: unknown column type %q for column %q", string(col.Type), col.Name)
	}
}

// coerceIntID binds the system id column as an integer.
func coerceIntID(value any) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > ^uint64(0)>>1 {
			return 0, fmt.Errorf("id %d overflows int64", v)
		}
		return int64(v), nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("id %v is not an integer", v)
		}
		return int64(v), nil
	case string:
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("id %q is not an integer", v)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("got %T", value)
	}
}

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func parseDateValue(column, raw string) (time.Time, error) {
	for _, layout := range dateLayouts {
		if got, err := time.Parse(layout, raw); err == nil {
			return got.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("datastore: column %q value %q is not a date", column, raw)
}

// canonicalValues resolves caller-supplied keys against the catalogue's live
// columns before any SQL is composed. Unknown names and reserved system
// names are errors; matching is case-insensitive and the catalogue's
// canonical spelling is what gets quoted and bound.
func canonicalValues(cols []ColumnDef, values map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(values))
	for key, value := range values {
		var match *ColumnDef
		for i := range cols {
			if strings.EqualFold(cols[i].Name, key) {
				match = &cols[i]
				break
			}
		}
		if match == nil {
			if canonical, reserved := reservedColumns[strings.ToLower(key)]; reserved {
				return nil, fmt.Errorf("datastore: column name %q is reserved (reserved word %q)", key, canonical)
			}
			return nil, fmt.Errorf("datastore: unknown column %q", key)
		}
		bound, err := coerceColumnValue(*match, value)
		if err != nil {
			return nil, err
		}
		out[match.Name] = bound
	}
	return out, nil
}

// scanRows drains sql rows into normalised Row values. Raw driver values
// differ per driver — SQLite booleans arrive as 0/1, datetimes as strings —
// so every column is normalised by its catalogue type here, once.
func scanRows(sqlRows *sql.Rows, cols []ColumnDef) ([]Row, error) {
	defer sqlRows.Close()
	names, err := sqlRows.Columns()
	if err != nil {
		return nil, fmt.Errorf("datastore: read row columns: %w", err)
	}
	byName := map[string]ColumnDef{}
	for _, col := range cols {
		byName[col.Name] = col
	}
	var out []Row
	for sqlRows.Next() {
		holders := make([]any, len(names))
		for i := range holders {
			var v any
			holders[i] = &v
		}
		if err := sqlRows.Scan(holders...); err != nil {
			return nil, fmt.Errorf("datastore: scan row: %w", err)
		}
		row := Row{}
		for i, name := range names {
			raw := *(holders[i].(*any))
			row[name] = normaliseValue(name, byName[name], raw)
		}
		out = append(out, row)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, fmt.Errorf("datastore: read rows: %w", err)
	}
	return out, nil
}

// normaliseValue maps one raw driver value to the Go value the row carries.
// Unknown names (the system id and timestamps) are handled by name; user
// columns by catalogue type; nil stays nil.
func normaliseValue(name string, col ColumnDef, raw any) any {
	if raw == nil {
		return nil
	}
	switch name {
	case "id":
		id, err := coerceIntID(deref(raw))
		if err != nil {
			return raw
		}
		return id
	case "createdAt", "updatedAt":
		if t, ok := deref(raw).(time.Time); ok {
			return t
		}
		if t, err := parseDateValue(name, stringOf(raw)); err == nil {
			return t
		}
		return raw
	}
	switch col.Type {
	case ColumnString:
		return stringOf(raw)
	case ColumnNumber:
		return floatOf(raw)
	case ColumnBoolean:
		return boolOf(raw)
	case ColumnDate:
		if t, ok := deref(raw).(time.Time); ok {
			return t
		}
		if t, err := parseDateValue(name, stringOf(raw)); err == nil {
			return t
		}
		return raw
	default:
		return raw
	}
}

func deref(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func stringOf(v any) string {
	switch v := deref(v).(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func floatOf(v any) any {
	switch v := deref(v).(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case uint:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case []byte:
		if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			return f
		}
		return v
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
		return v
	default:
		return v
	}
}

func boolOf(v any) any {
	switch v := deref(v).(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	case float64:
		return v != 0
	case string:
		if v == "1" || strings.EqualFold(v, "true") {
			return true
		}
		if v == "0" || strings.EqualFold(v, "false") {
			return false
		}
		return v
	default:
		return v
	}
}

// selectProjection runs a SELECT over the explicit column list and returns
// normalised rows.
func (e *Engine) selectProjection(ctx context.Context, dialect, table string, cols []ColumnDef, suffix string, args ...any) ([]Row, error) {
	names := projectColumns(cols)
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = quoteIdent(dialect, name)
	}
	statement := "SELECT " + strings.Join(quoted, ",") + " FROM " + quoteIdent(dialect, table) + suffix
	e.emit(statement)
	sqlRows, err := e.db.WithContext(ctx).Raw(statement, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("datastore: select rows: %w", err)
	}
	return scanRows(sqlRows, cols)
}

// Insert writes one row and reads it back: the id auto-increments from 1
// and both timestamps arrive set by the database. Keys must be live user
// columns; system names are refused with the reserved-word error.
func (e *Engine) Insert(ctx context.Context, tenantID, dsID string, values map[string]any) (Row, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	bound, err := canonicalValues(cols, values)
	if err != nil {
		return nil, err
	}
	// Both bounds refuse before any value is bound to SQL: the byte bound is
	// free to check, and the row probe stops at the limit-th row rather than
	// scanning the table behind SQLite's single connection.
	if err := checkValueSizes(bound, e.limits.MaxValueBytes); err != nil {
		return nil, err
	}
	if err := e.checkRowLimit(ctx, table); err != nil {
		return nil, err
	}
	dialect := e.dialect()
	names := make([]string, 0, len(bound))
	placeholders := make([]string, 0, len(bound))
	args := make([]any, 0, len(bound))
	for _, col := range cols {
		value, ok := bound[col.Name]
		if !ok {
			continue
		}
		names = append(names, quoteIdent(dialect, col.Name))
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	statement := "INSERT INTO " + quoteIdent(dialect, table)
	if len(names) > 0 {
		statement += " (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	} else {
		statement += " DEFAULT VALUES"
	}
	var id int64
	if dialect == "postgres" {
		e.emit(statement + " RETURNING " + quoteIdent(dialect, "id"))
		if err := e.db.WithContext(ctx).Raw(statement+" RETURNING "+quoteIdent(dialect, "id"), args...).Scan(&id).Error; err != nil {
			return nil, fmt.Errorf("datastore: insert row: %w", err)
		}
	} else {
		e.emit(statement)
		if err := e.db.WithContext(ctx).Exec(statement, args...).Error; err != nil {
			return nil, fmt.Errorf("datastore: insert row: %w", err)
		}
		if err := e.db.WithContext(ctx).Raw("SELECT last_insert_rowid()").Scan(&id).Error; err != nil {
			return nil, fmt.Errorf("datastore: read inserted id: %w", err)
		}
	}
	return e.Get(ctx, tenantID, dsID, id)
}

// Get returns one row by id.
func (e *Engine) Get(ctx context.Context, tenantID, dsID string, id int64) (Row, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	rows, err := e.selectProjection(ctx, e.dialect(), table, cols, " WHERE "+quoteIdent(e.dialect(), "id")+" = ?", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: %d in %s", ErrRowNotFound, id, dsID)
	}
	return rows[0], nil
}

// List returns one page of rows in id order. The cursor pins the last id
// seen, so a row inserted while a client pages lands past every issued
// cursor instead of shifting rows onto a page already read. ReturnAll
// ignores the cursor and the limit and returns every matching row with no
// next cursor; otherwise the limit is clamped to MaxRowPageSize and the
// page carries a next cursor exactly when another row exists.
func (e *Engine) List(ctx context.Context, tenantID, dsID string, q RowQuery) (RowPage, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return RowPage{}, err
	}
	dialect := e.dialect()
	clause, args, err := buildFilterClause(dialect, cols, q.Filter)
	if err != nil {
		return RowPage{}, err
	}
	if q.ReturnAll {
		rows, err := e.selectProjection(ctx, dialect, table, cols,
			clause+" ORDER BY "+quoteIdent(dialect, "id")+" ASC", args...)
		if err != nil {
			return RowPage{}, err
		}
		if rows == nil {
			rows = []Row{}
		}
		return RowPage{Rows: rows}, nil
	}
	if q.Cursor != "" {
		after, err := decodeRowCursor(q.Cursor)
		if err != nil {
			return RowPage{}, err
		}
		predicate := quoteIdent(dialect, "id") + " > ?"
		if clause == "" {
			clause = " WHERE " + predicate
		} else {
			clause += " AND " + predicate
		}
		args = append(args, after)
	}
	limit := clampRowLimit(q)
	// Read one extra row to learn whether another page exists without a
	// second COUNT query over the same predicate.
	rows, err := e.selectProjection(ctx, dialect, table, cols,
		clause+" ORDER BY "+quoteIdent(dialect, "id")+" ASC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return RowPage{}, err
	}
	page := RowPage{Rows: rows}
	if len(rows) > limit {
		last := rows[limit-1]
		page.NextCursor = encodeRowCursor(last["id"].(int64))
		page.Rows = rows[:limit]
	}
	if page.Rows == nil {
		page.Rows = []Row{}
	}
	return page, nil
}

// matchIDsAndRows returns the ids and full rows matching the filter, in id
// order. Update, upsert and delete share it so the dry-run before-images
// and the committed after-images come from one predicate.
func (e *Engine) matchIDsAndRows(ctx context.Context, dialect, table string, cols []ColumnDef, filter *Filter) ([]Row, error) {
	clause, args, err := buildFilterClause(dialect, cols, filter)
	if err != nil {
		return nil, err
	}
	return e.selectProjection(ctx, dialect, table, cols,
		clause+" ORDER BY "+quoteIdent(dialect, "id")+" ASC", args...)
}

// stampNow is the updatedAt bump writers apply on update: database-set, per
// dialect, so the engine never formats a timestamp into SQL text. SQLite
// resolves to milliseconds rather than CURRENT_TIMESTAMP's whole seconds:
// a stamp that moves once per second cannot tell two writes in the same
// second apart, which would make every optimistic precondition accept a
// stale write it should refuse.
func stampNow(dialect string) string {
	if dialect == "postgres" {
		return "now()"
	}
	return "STRFTIME('%Y-%m-%d %H:%M:%f','now')"
}

// Update sets the given columns on every row matching the filter. With
// dryRun it returns the paired before/after images tagged DryRunState and
// leaves the table byte-identical: the after-image is computed, never
// written — a rolled-back bulk write would hold SQLite's single connection
// for its full duration while producing nothing.
func (e *Engine) Update(ctx context.Context, tenantID, dsID string, filter *Filter, values map[string]any, dryRun bool) (*UpdateResult, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	bound, err := canonicalValues(cols, values)
	if err != nil {
		return nil, err
	}
	// An update binds values too, so the byte bound applies here as well —
	// still before any SQL is composed.
	if err := checkValueSizes(bound, e.limits.MaxValueBytes); err != nil {
		return nil, err
	}
	dialect := e.dialect()
	before, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if dryRun {
		pairs := make([]DryRunPair, 0, len(before))
		for _, row := range before {
			after := Row{DryRunState: "after"}
			for k, v := range row {
				after[k] = v
			}
			for k, v := range bound {
				after[k] = v
			}
			clone := Row{DryRunState: "before"}
			for k, v := range row {
				clone[k] = v
			}
			pairs = append(pairs, DryRunPair{Before: clone, After: after})
		}
		return &UpdateResult{Matched: int64(len(before)), Pairs: pairs}, nil
	}
	if len(before) == 0 {
		return &UpdateResult{}, nil
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
	statement := "UPDATE " + quoteIdent(dialect, table) + " SET " + strings.Join(set, ",") + clause
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, append(args, filterArgs...)...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: update rows: %w", res.Error)
	}
	ids := make([]int64, 0, len(before))
	for _, row := range before {
		ids = append(ids, row["id"].(int64))
	}
	after, err := e.rowsByIDs(ctx, dialect, table, cols, ids)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{Matched: res.RowsAffected, Rows: after}, nil
}

// Delete removes every row matching the filter. With dryRun it returns the
// paired images — the before-image plus a tombstone after holding only the
// id and the tag — and deletes nothing.
func (e *Engine) Delete(ctx context.Context, tenantID, dsID string, filter *Filter, dryRun bool) (*DeleteResult, error) {
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	dialect := e.dialect()
	before, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if dryRun {
		pairs := make([]DryRunPair, 0, len(before))
		for _, row := range before {
			clone := Row{DryRunState: "before"}
			for k, v := range row {
				clone[k] = v
			}
			pairs = append(pairs, DryRunPair{
				Before: clone,
				After:  Row{"id": row["id"], DryRunState: "after"},
			})
		}
		return &DeleteResult{Deleted: int64(len(before)), Pairs: pairs}, nil
	}
	clause, args, err := buildFilterClause(dialect, cols, filter)
	if err != nil {
		return nil, err
	}
	statement := "DELETE FROM " + quoteIdent(dialect, table) + clause
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return nil, fmt.Errorf("datastore: delete rows: %w", res.Error)
	}
	return &DeleteResult{Deleted: res.RowsAffected, Rows: before}, nil
}

// Upsert updates every row matching the filter, or inserts one row when the
// filter matches nothing. The insert merges the filter's eq conditions into
// the values — the match keys travel with the new row — without overwriting
// an explicitly supplied value. With dryRun nothing is written: an update
// returns before/after pairs, an insert returns one pair with a nil Before.
//
// Upsert atomicity and row locking belong to a later ticket: this is
// read-then-write inside no single transaction, so two concurrent upserts
// with the same filter may both insert.
func (e *Engine) Upsert(ctx context.Context, tenantID, dsID string, filter *Filter, values map[string]any, dryRun bool) (*UpsertResult, error) {
	if filter == nil || len(filter.Conditions) == 0 {
		return nil, fmt.Errorf("datastore: upsert needs a filter with at least one condition")
	}
	_, cols, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return nil, err
	}
	dialect := e.dialect()
	matching, err := e.matchIDsAndRows(ctx, dialect, table, cols, filter)
	if err != nil {
		return nil, err
	}
	if len(matching) > 0 {
		updated, err := e.Update(ctx, tenantID, dsID, filter, values, dryRun)
		if err != nil {
			return nil, err
		}
		return &UpsertResult{Matched: updated.Matched, Rows: updated.Rows, Pairs: updated.Pairs}, nil
	}
	merged := map[string]any{}
	for k, v := range values {
		merged[k] = v
	}
	for _, c := range filter.Conditions {
		if c.Condition != CondEq || c.Value == nil {
			continue
		}
		if _, supplied := merged[c.Column]; supplied {
			continue
		}
		// The key may use either casing; Insert validates it again.
		merged[c.Column] = c.Value
	}
	if dryRun {
		bound, err := canonicalValues(cols, merged)
		if err != nil {
			return nil, err
		}
		after := Row{DryRunState: "after"}
		for k, v := range bound {
			after[k] = v
		}
		return &UpsertResult{Inserted: true, Matched: 0, Pairs: []DryRunPair{{After: after}}}, nil
	}
	row, err := e.Insert(ctx, tenantID, dsID, merged)
	if err != nil {
		return nil, err
	}
	return &UpsertResult{Inserted: true, Matched: 1, Rows: []Row{row}}, nil
}

// Clear removes every row and keeps the schema. Ids stay monotonic: the
// auto-increment sequence is not reset, so a row inserted after a clear
// never reuses an id the table held before.
func (e *Engine) Clear(ctx context.Context, tenantID, dsID string) (int64, error) {
	_, _, table, err := e.gatedLookup(ctx, tenantID, dsID)
	if err != nil {
		return 0, err
	}
	statement := "DELETE FROM " + quoteIdent(e.dialect(), table)
	e.emit(statement)
	res := e.db.WithContext(ctx).Exec(statement)
	if res.Error != nil {
		return 0, fmt.Errorf("datastore: clear table: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// rowsByIDs reads the given ids back in id order for committed
// after-images.
func (e *Engine) rowsByIDs(ctx context.Context, dialect, table string, cols []ColumnDef, ids []int64) ([]Row, error) {
	if len(ids) == 0 {
		return []Row{}, nil
	}
	holders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		holders[i] = "?"
		args[i] = id
	}
	return e.selectProjection(ctx, dialect, table, cols,
		" WHERE "+quoteIdent(dialect, "id")+" IN ("+strings.Join(holders, ",")+") ORDER BY "+quoteIdent(dialect, "id")+" ASC", args...)
}

// countRows returns the row count for tests and smoke evidence.
func (e *Engine) countRows(ctx context.Context, table string) (int64, error) {
	var count int64
	statement := "SELECT COUNT(*) FROM " + quoteIdent(e.dialect(), table)
	e.emit(statement)
	if err := e.db.WithContext(ctx).Raw(statement).Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("datastore: count rows: %w", err)
	}
	return count, nil
}
