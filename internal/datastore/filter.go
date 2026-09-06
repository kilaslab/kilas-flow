package datastore

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// This file is the row-store filter layer (FEAT-nrfg6e). It owns the n8n
// filter surface, the keyset cursor, and the page-size bounds. It composes
// SQL text but never touches the driver: values leave here as bound args
// only, and every column name is resolved against the catalogue before it
// is quoted.

// Condition is one n8n data-table filter operator. The set comes from n8n's
// own type union (design-refs/n8n-v2 shot 32), not from the screenshot,
// which did not render as a plain listbox: eq, neq, like, ilike, gt, gte,
// lt, lte. isEmpty and isNotEmpty complete the service surface the ticket
// names.
type Condition string

const (
	CondEq         Condition = "eq"
	CondNeq        Condition = "neq"
	CondLike       Condition = "like"
	CondILike      Condition = "ilike"
	CondGt         Condition = "gt"
	CondGte        Condition = "gte"
	CondLt         Condition = "lt"
	CondLte        Condition = "lte"
	CondIsEmpty    Condition = "isEmpty"
	CondIsNotEmpty Condition = "isNotEmpty"
)

// supportedConditions is the refusal message's source of truth: an
// unrecognised operator is an error naming this set, never a dropped
// predicate. A map of operator to fragment would miss into the zero string
// and compose a WHERE clause that still parses with the predicate gone; the
// switch in conditionFragment cannot be used that way.
const supportedConditions = "eq, neq, like, ilike, gt, gte, lt, lte, isEmpty, isNotEmpty"

// FilterCondition is one predicate in the service-API shape:
// {columnName, condition, value}. The node speaks keyName/keyValue instead;
// NodeConditionsToFilter maps between the two.
type FilterCondition struct {
	Column    string    `json:"columnName"`
	Condition Condition `json:"condition"`
	Value     any       `json:"value,omitempty"`
}

// Filter is the service-API envelope: {type: and|or, filters: [...]}.
type Filter struct {
	Type       string            `json:"type"`
	Conditions []FilterCondition `json:"filters"`
}

// NodeFilterCondition is one condition row in the node's parameter shape,
// verbatim from the DOM (design-refs/n8n-v2 shot 31):
// filters.conditions[i].keyName (Column), .condition (default eq),
// .keyValue (Value).
type NodeFilterCondition struct {
	KeyName   string    `json:"keyName"`
	Condition Condition `json:"condition"`
	KeyValue  any       `json:"keyValue"`
}

// NodeMatchToFilterType maps the Get row(s) Must Match control to the
// service envelope: Any Condition matches any (or), All Conditions matches
// all (and). Matching is case-insensitive and accepts the bare and/or the
// service values already, so a caller holding either spelling converges.
func NodeMatchToFilterType(match string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(match)) {
	case "any", "any condition", "any conditions", "or":
		return "or", nil
	case "all", "all conditions", "all condition", "and":
		return "and", nil
	default:
		return "", fmt.Errorf("datastore: unknown match mode %q, want Any Condition or All Conditions", match)
	}
}

// NodeConditionsToFilter maps the node's condition rows to the service
// filter: keyName becomes columnName, keyValue becomes value, and the Must
// Match mode becomes the envelope type. The table update operation the node
// lists is surfaced as Rename; it never reaches this filter path.
func NodeConditionsToFilter(match string, conds []NodeFilterCondition) (*Filter, error) {
	typ, err := NodeMatchToFilterType(match)
	if err != nil {
		return nil, err
	}
	out := &Filter{Type: typ}
	for _, c := range conds {
		out.Conditions = append(out.Conditions, FilterCondition{
			Column:    c.KeyName,
			Condition: c.Condition,
			Value:     c.KeyValue,
		})
	}
	return out, nil
}

// conditionFragment resolves every filter operator through one Go switch to
// a compile-time SQL fragment plus its placeholder count. An unrecognised
// operator is an error naming the supported set.
//
// Dialect matters only for the LIKE pair, and the mapping is explicit per
// driver because the defaults disagree silently: PostgreSQL LIKE is
// case-sensitive with ILIKE as the insensitive one, while SQLite has no
// ILIKE and its LIKE is case-insensitive for ASCII. So like is the
// case-sensitive match on both (GLOB on SQLite, LIKE on PostgreSQL) and
// ilike is the insensitive one (LIKE on SQLite, ILIKE on PostgreSQL). A
// fixture asserting like/ilike equivalence across drivers pins this.
func conditionFragment(dialect string, cond Condition) (string, int, error) {
	switch cond {
	case CondEq:
		return "= ?", 1, nil
	case CondNeq:
		return "<> ?", 1, nil
	case CondLike:
		if dialect == "sqlite" {
			return "GLOB ?", 1, nil
		}
		return "LIKE ?", 1, nil
	case CondILike:
		if dialect == "postgres" {
			return "ILIKE ?", 1, nil
		}
		return "LIKE ?", 1, nil
	case CondGt:
		return "> ?", 1, nil
	case CondGte:
		return ">= ?", 1, nil
	case CondLt:
		return "< ?", 1, nil
	case CondLte:
		return "<= ?", 1, nil
	case CondIsEmpty:
		return "IS EMPTY", 0, nil
	case CondIsNotEmpty:
		return "IS NOT EMPTY", 0, nil
	default:
		return "", 0, fmt.Errorf("datastore: unsupported filter condition %q, want one of %s", string(cond), supportedConditions)
	}
}

// likePatternToGlob translates a LIKE pattern to the GLOB pattern matching
// the same strings, so the case-sensitive like on SQLite agrees with LIKE
// on PostgreSQL: % becomes *, _ becomes ?, and every GLOB metacharacter in
// the literal text is bracketed. LIKE ESCAPE clauses are not supported; a
// backslash is literal text on both sides.
func likePatternToGlob(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))
	for _, r := range pattern {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteRune('[')
			b.WriteRune(r)
			b.WriteRune(']')
		case '%':
			b.WriteString("*")
		case '_':
			b.WriteString("?")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// resolveFilterColumn resolves a filter's column name against the
// catalogue's live columns plus the system columns the node's Column select
// offers (id, createdAt, updatedAt). Quoting is the second line of defence,
// never the first: a name that is a legal identifier but belongs to no
// column of this datastore is rejected before any SQL text is built.
// Matching is case-insensitive so the two drivers agree; the returned
// definition carries the catalogue's canonical spelling, which is what gets
// quoted. dryRunState is reserved, not physical, so filtering on it is a
// reserved-word error rather than "unknown".
func resolveFilterColumn(name string, cols []ColumnDef) (ColumnDef, error) {
	for _, col := range cols {
		if strings.EqualFold(col.Name, name) {
			return col, nil
		}
	}
	switch strings.ToLower(name) {
	case "id":
		return ColumnDef{Name: "id", Type: ColumnNumber}, nil
	case "createdat":
		return ColumnDef{Name: "createdAt", Type: ColumnDate}, nil
	case "updatedat":
		return ColumnDef{Name: "updatedAt", Type: ColumnDate}, nil
	}
	if canonical, reserved := reservedColumns[strings.ToLower(name)]; reserved {
		return ColumnDef{}, fmt.Errorf("datastore: column name %q is reserved (reserved word %q)", name, canonical)
	}
	return ColumnDef{}, fmt.Errorf("datastore: unknown column %q", name)
}

// buildFilterClause renders the WHERE clause (with leading space, without
// the WHERE when the filter is empty) and the bound args. Values reach the
// database only as ? placeholders: the composed text never contains a byte
// of a supplied value. An unknown column or operator aborts before any
// statement is emitted.
func buildFilterClause(dialect string, cols []ColumnDef, filter *Filter) (string, []any, error) {
	if filter == nil || len(filter.Conditions) == 0 {
		return "", nil, nil
	}
	joiner := ""
	switch strings.ToLower(strings.TrimSpace(filter.Type)) {
	case "and", "all", "all conditions", "all condition":
		joiner = " AND "
	case "or", "any", "any condition", "any conditions", "":
		joiner = " OR "
	default:
		return "", nil, fmt.Errorf("datastore: unknown filter type %q, want and or or", filter.Type)
	}
	parts := make([]string, 0, len(filter.Conditions))
	var args []any
	for _, c := range filter.Conditions {
		col, err := resolveFilterColumn(c.Column, cols)
		if err != nil {
			return "", nil, err
		}
		fragment, placeholders, err := conditionFragment(dialect, c.Condition)
		if err != nil {
			return "", nil, err
		}
		quoted := quoteIdent(dialect, col.Name)
		switch {
		case placeholders == 0:
			// Emptiness is null for every type, plus the empty string
			// for text: a string column holding "" reads empty in the
			// grid while a number holding 0 does not.
			empty := quoted + " IS NULL"
			if col.Type == ColumnString {
				empty = "(" + quoted + " IS NULL OR " + quoted + " = '')"
			}
			if c.Condition == CondIsEmpty {
				parts = append(parts, empty)
			} else {
				parts = append(parts, "NOT "+empty)
			}
		case c.Value == nil && c.Condition == CondEq:
			parts = append(parts, quoted+" IS NULL")
		case c.Value == nil && c.Condition == CondNeq:
			parts = append(parts, quoted+" IS NOT NULL")
		case c.Value == nil:
			return "", nil, fmt.Errorf("datastore: filter on column %q with condition %q needs a value", col.Name, string(c.Condition))
		default:
			arg, err := coerceFilterValue(col, c.Condition, c.Value)
			if err != nil {
				return "", nil, err
			}
			if dialect == "sqlite" && (c.Condition == CondLike || c.Condition == CondILike) {
				pattern, ok := arg.(string)
				if !ok {
					return "", nil, fmt.Errorf("datastore: filter on column %q with condition %q needs a string pattern", col.Name, string(c.Condition))
				}
				if c.Condition == CondLike {
					arg = likePatternToGlob(pattern)
				} else {
					arg = pattern
				}
			}
			parts = append(parts, quoted+" "+fragment)
			args = append(args, arg)
		}
	}
	return " WHERE (" + strings.Join(parts, joiner) + ")", args, nil
}

// coerceFilterValue coerces a filter value the way the write path coerces
// it, with two filter-specific rules: LIKE patterns must be strings, and
// the system id column binds as an integer rather than a float.
func coerceFilterValue(col ColumnDef, cond Condition, value any) (any, error) {
	if cond == CondLike || cond == CondILike {
		switch v := value.(type) {
		case string:
			return v, nil
		case []byte:
			return string(v), nil
		default:
			return nil, fmt.Errorf("datastore: filter on column %q with condition %q needs a string pattern, got %T", col.Name, string(cond), value)
		}
	}
	if strings.EqualFold(col.Name, "id") {
		id, err := coerceIntID(value)
		if err != nil {
			return nil, fmt.Errorf("datastore: filter on column %q needs an integer id: %w", col.Name, err)
		}
		return id, nil
	}
	return coerceColumnValue(col, value)
}

// DefaultRowPageSize and MaxRowPageSize bound a row listing the way the
// execution bounds bound history: a missing limit reads the default, a
// limit past the maximum is clamped. ReturnAll bypasses both by design —
// the node exposes it beside the limit — and the caller owns the size.
const (
	DefaultRowPageSize = 25
	MaxRowPageSize     = 100
)

// RowQuery is one row listing: the filter, the keyset cursor pinning the
// last id seen, and the limit. LimitPerInputRow is a node concern, not a
// store one: the node calls List once per input item with Limit set to the
// per-row value, so the store only ever sees one limit.
type RowQuery struct {
	Filter    *Filter
	Cursor    string
	Limit     int
	ReturnAll bool
}

// RowPage is one page of rows in id order with the cursor for the next.
type RowPage struct {
	Rows       []Row
	NextCursor string
}

// rowCursorPrefix versions the row cursor wire format, so carrying a sorted
// value beside the id later is a new version rather than a format change.
const rowCursorPrefix = "row-v1"

// encodeRowCursor pins the last id seen. Pagination is keyset on the
// integer id alone rather than on (createdAt, id): both drivers stamp at
// millisecond precision so ties are ordinary, and a single integer drops
// the timestamp round-trip. A row inserted while a client pages lands past
// every issued cursor, so it cannot shift rows onto a page already read.
func encodeRowCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(rowCursorPrefix + "\x00" + strconv.FormatInt(id, 10)))
}

// decodeRowCursor inverts encodeRowCursor. A cursor from another format is
// an error, never a row offset.
func decodeRowCursor(cursor string) (int64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: row cursor is malformed", ErrInvalidRowCursor)
	}
	prefix, raw, found := strings.Cut(string(decoded), "\x00")
	if !found || prefix != rowCursorPrefix {
		return 0, fmt.Errorf("%w: row cursor is malformed", ErrInvalidRowCursor)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%w: row cursor is malformed", ErrInvalidRowCursor)
	}
	return id, nil
}

// clampRowLimit resolves the query's effective limit: ReturnAll reports no
// limit (-1), otherwise the default fills a missing value and the maximum
// clamps an excessive one.
func clampRowLimit(q RowQuery) int {
	if q.ReturnAll {
		return -1
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultRowPageSize
	}
	if limit > MaxRowPageSize {
		limit = MaxRowPageSize
	}
	return limit
}
