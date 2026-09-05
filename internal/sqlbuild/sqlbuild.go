// Package sqlbuild turns a described operation into a bound SQL statement.
//
// It exists because an operation set changes what SQL is: every statement in
// this product used to be written by a user and bound, so nothing ever had to
// build one. Insert, update, upsert, select and delete build theirs from a
// schema, a table and a column list — and identifiers cannot be bound. They are
// quoted, by exactly one function, and that function is the only place in the
// tree where user input reaches statement text.
//
// Values never join them. Every value goes out as a placeholder, so the shape
// of a statement depends on how many values there are and never on what they
// contain.
//
// It builds and returns; it does not execute. That is what makes the golden
// files worth having: the SQL a workflow will run is a pure function of what
// the node was configured with, testable with no database at all.
package sqlbuild

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// MaxIdentifierBytes is PostgreSQL's own limit.
//
// The server truncates a longer name silently, so two columns whose first
// sixty-three bytes match would collide into one — a statement that runs and
// writes the wrong column. Refused here rather than discovered there.
const MaxIdentifierBytes = 63

// Identifier quotes one or more identifier parts into a qualified name.
//
// It wraps pgx's Sanitize rather than calling it directly. Sanitize *strips* a
// NUL byte instead of refusing one, which turns a name the caller wrote into a
// different name it never wrote, and it says nothing about the length limit.
// Both are refusals here.
func Identifier(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("an identifier needs at least one part")
	}
	quoted := make(pgx.Identifier, 0, len(parts))
	for _, part := range parts {
		if err := checkIdentifier(part); err != nil {
			return "", err
		}
		quoted = append(quoted, part)
	}
	return quoted.Sanitize(), nil
}

func checkIdentifier(part string) error {
	if strings.TrimSpace(part) == "" {
		return fmt.Errorf("an identifier part cannot be empty")
	}
	if strings.ContainsRune(part, 0) {
		// pgx strips it, which would silently rename the thing being addressed.
		return fmt.Errorf("an identifier cannot contain a NUL byte")
	}
	if len(part) > MaxIdentifierBytes {
		return fmt.Errorf("the identifier %q is %d bytes, and PostgreSQL truncates anything over %d — "+
			"two names that long can silently become one", part, len(part), MaxIdentifierBytes)
	}
	return nil
}

// Target is the table an operation acts on.
type Target struct {
	Schema string
	Table  string
}

// Qualified renders the target as a quoted, qualified name.
func (target Target) Qualified() (string, error) {
	if strings.TrimSpace(target.Table) == "" {
		return "", fmt.Errorf("an operation needs a table")
	}
	if strings.TrimSpace(target.Schema) == "" {
		return Identifier(target.Table)
	}
	return Identifier(target.Schema, target.Table)
}

// Comparison is one WHERE clause term.
type Comparison struct {
	Column string
	// Operator is one of the values KnownOperators lists.
	Operator string
	Value    any
}

// KnownOperators is the closed set a comparison may use.
//
// Closed, because the operator is the one part of a WHERE clause that cannot be
// bound: it goes into the statement as written. A set the caller could extend
// would be string interpolation with extra steps.
func KnownOperators() []string {
	return []string{"equals", "notEquals", "gt", "gte", "lt", "lte", "like", "ilike", "isNull", "isNotNull"}
}

var comparisonSQL = map[string]string{
	"equals": "=", "notEquals": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=",
	"like": "LIKE", "ilike": "ILIKE",
}

// Order is one ORDER BY term.
type Order struct {
	Column string
	// Descending is the only direction choice there is, for the same reason
	// the operator set is closed.
	Descending bool
}

// Select builds a read.
//
// Combine is "AND" or "OR", defaulting to AND: a WHERE with several terms and
// no stated combinator almost always means all of them.
func Select(target Target, columns []string, where []Comparison, combine string, order []Order, limit int) (sqlnode.Statement, error) {
	name, err := target.Qualified()
	if err != nil {
		return sqlnode.Statement{}, err
	}
	projection := "*"
	if len(columns) > 0 {
		quoted, err := quoteAll(columns)
		if err != nil {
			return sqlnode.Statement{}, err
		}
		projection = strings.Join(quoted, ", ")
	}

	statement := "SELECT " + projection + " FROM " + name
	clause, values, err := whereClause(where, combine, 1)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	statement += clause

	if len(order) > 0 {
		terms := make([]string, 0, len(order))
		for _, term := range order {
			column, err := Identifier(term.Column)
			if err != nil {
				return sqlnode.Statement{}, err
			}
			direction := " ASC"
			if term.Descending {
				direction = " DESC"
			}
			terms = append(terms, column+direction)
		}
		statement += " ORDER BY " + strings.Join(terms, ", ")
	}
	if limit > 0 {
		// The limit is a number this package produced, never a string the
		// caller wrote, so it is formatted rather than bound — which keeps the
		// placeholder numbering the same whether or not a limit is set.
		statement += " LIMIT " + strconv.Itoa(limit)
	}
	return sqlnode.Statement{SQL: statement, Parameters: values, Returning: true}, nil
}

// Insert builds a write of one row.
//
// It returns rows, because the generated key is the one thing the caller cannot
// know and usually needs.
func Insert(target Target, values map[string]any) (sqlnode.Statement, error) {
	name, err := target.Qualified()
	if err != nil {
		return sqlnode.Statement{}, err
	}
	columns := sortedKeys(values)
	if len(columns) == 0 {
		return sqlnode.Statement{}, fmt.Errorf("an insert needs at least one column")
	}
	quoted, err := quoteAll(columns)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	placeholders := make([]string, 0, len(columns))
	bound := make([]any, 0, len(columns))
	for index, column := range columns {
		placeholders = append(placeholders, "$"+strconv.Itoa(index+1))
		bound = append(bound, values[column])
	}
	statement := "INSERT INTO " + name + " (" + strings.Join(quoted, ", ") +
		") VALUES (" + strings.Join(placeholders, ", ") + ") RETURNING *"
	return sqlnode.Statement{SQL: statement, Parameters: bound, Returning: true}, nil
}

// Update builds a write to existing rows.
func Update(target Target, values map[string]any, matching []string) (sqlnode.Statement, error) {
	name, err := target.Qualified()
	if err != nil {
		return sqlnode.Statement{}, err
	}
	if len(matching) == 0 {
		return sqlnode.Statement{}, fmt.Errorf("an update needs at least one column to match on")
	}
	assignments, bound, err := assignmentList(values, matching, 1)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	where, whereValues, err := matchClause(values, matching, len(bound)+1)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	statement := "UPDATE " + name + " SET " + strings.Join(assignments, ", ") + where + " RETURNING *"
	return sqlnode.Statement{SQL: statement, Parameters: append(bound, whereValues...), Returning: true}, nil
}

// Upsert builds an insert that updates on conflict.
func Upsert(target Target, values map[string]any, matching []string) (sqlnode.Statement, error) {
	name, err := target.Qualified()
	if err != nil {
		return sqlnode.Statement{}, err
	}
	if len(matching) == 0 {
		return sqlnode.Statement{}, fmt.Errorf("an upsert needs at least one column to match on")
	}
	columns := sortedKeys(values)
	if len(columns) == 0 {
		return sqlnode.Statement{}, fmt.Errorf("an upsert needs at least one column")
	}
	quoted, err := quoteAll(columns)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	conflict, err := quoteAll(matching)
	if err != nil {
		return sqlnode.Statement{}, err
	}
	placeholders := make([]string, 0, len(columns))
	bound := make([]any, 0, len(columns))
	for index, column := range columns {
		placeholders = append(placeholders, "$"+strconv.Itoa(index+1))
		bound = append(bound, values[column])
	}
	// EXCLUDED is PostgreSQL's own name for the row that would have been
	// inserted, so the update half needs no second copy of the values.
	updates := make([]string, 0, len(columns))
	for index, column := range columns {
		if contains(matching, column) {
			continue
		}
		updates = append(updates, quoted[index]+" = EXCLUDED."+quoted[index])
	}
	statement := "INSERT INTO " + name + " (" + strings.Join(quoted, ", ") +
		") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (" + strings.Join(conflict, ", ") + ")"
	if len(updates) == 0 {
		// Every column is a matching column, so there is nothing to update.
		// DO NOTHING is the honest statement; DO UPDATE SET with an empty list
		// is a syntax error.
		statement += " DO NOTHING RETURNING *"
	} else {
		statement += " DO UPDATE SET " + strings.Join(updates, ", ") + " RETURNING *"
	}
	return sqlnode.Statement{SQL: statement, Parameters: bound, Returning: true}, nil
}

// Delete modes.
const (
	// DeleteRows removes the rows a WHERE clause selects.
	DeleteRows = "delete"
	// DeleteTruncate empties the table and keeps it.
	DeleteTruncate = "truncate"
	// DeleteDrop removes the table itself.
	DeleteDrop = "drop"
)

// Delete builds a removal.
//
// The three modes are separate values rather than one with an optional WHERE,
// because "empty this table" and "remove some rows from it" are different
// intentions and a missing WHERE must never quietly become the first.
func Delete(target Target, mode string, where []Comparison, combine string) (sqlnode.Statement, error) {
	name, err := target.Qualified()
	if err != nil {
		return sqlnode.Statement{}, err
	}
	switch mode {
	case DeleteDrop:
		return sqlnode.Statement{SQL: "DROP TABLE IF EXISTS " + name}, nil
	case DeleteTruncate:
		return sqlnode.Statement{SQL: "TRUNCATE TABLE " + name}, nil
	case "", DeleteRows:
		if len(where) == 0 {
			// Refused rather than run. A delete with no condition is a
			// truncate, and a user who meant that has a mode for it — while a
			// user who forgot a condition has just emptied a table.
			return sqlnode.Statement{}, fmt.Errorf(
				"deleting rows needs at least one condition; use the truncate mode to empty the whole table")
		}
		clause, values, err := whereClause(where, combine, 1)
		if err != nil {
			return sqlnode.Statement{}, err
		}
		return sqlnode.Statement{SQL: "DELETE FROM " + name + clause, Parameters: values}, nil
	default:
		return sqlnode.Statement{}, fmt.Errorf("delete mode %q is not supported", mode)
	}
}

// whereClause renders a WHERE, starting placeholders at `from`.
func whereClause(where []Comparison, combine string, from int) (string, []any, error) {
	if len(where) == 0 {
		return "", nil, nil
	}
	joiner := " AND "
	if strings.EqualFold(strings.TrimSpace(combine), "or") {
		joiner = " OR "
	}
	terms := make([]string, 0, len(where))
	values := make([]any, 0, len(where))
	position := from
	for _, comparison := range where {
		column, err := Identifier(comparison.Column)
		if err != nil {
			return "", nil, err
		}
		switch comparison.Operator {
		case "isNull":
			terms = append(terms, column+" IS NULL")
			continue
		case "isNotNull":
			terms = append(terms, column+" IS NOT NULL")
			continue
		}
		symbol, known := comparisonSQL[comparison.Operator]
		if !known {
			return "", nil, fmt.Errorf("the comparison %q is not supported", comparison.Operator)
		}
		terms = append(terms, column+" "+symbol+" $"+strconv.Itoa(position))
		values = append(values, comparison.Value)
		position++
	}
	return " WHERE " + strings.Join(terms, joiner), values, nil
}

// assignmentList renders a SET list, skipping the matching columns.
func assignmentList(values map[string]any, matching []string, from int) ([]string, []any, error) {
	assignments := make([]string, 0, len(values))
	bound := make([]any, 0, len(values))
	position := from
	for _, column := range sortedKeys(values) {
		if contains(matching, column) {
			// A matching column identifies the row rather than supplying it,
			// so setting it to itself is noise at best and a moving target at
			// worst.
			continue
		}
		quoted, err := Identifier(column)
		if err != nil {
			return nil, nil, err
		}
		assignments = append(assignments, quoted+" = $"+strconv.Itoa(position))
		bound = append(bound, values[column])
		position++
	}
	if len(assignments) == 0 {
		return nil, nil, fmt.Errorf("an update needs at least one column that is not a matching column")
	}
	return assignments, bound, nil
}

// matchClause renders the WHERE that identifies the rows to change.
func matchClause(values map[string]any, matching []string, from int) (string, []any, error) {
	terms := make([]Comparison, 0, len(matching))
	for _, column := range matching {
		value, present := values[column]
		if !present {
			return "", nil, fmt.Errorf("the matching column %q has no value to match against", column)
		}
		terms = append(terms, Comparison{Column: column, Operator: "equals", Value: value})
	}
	return whereClause(terms, "and", from)
}

func quoteAll(names []string) ([]string, error) {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		part, err := Identifier(name)
		if err != nil {
			return nil, err
		}
		quoted = append(quoted, part)
	}
	return quoted, nil
}

// sortedKeys orders a value map, so the same configuration produces the same
// statement every time — which is what makes a golden file meaningful and a
// prepared-statement cache useful.
func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contains(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}
