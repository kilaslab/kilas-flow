package datastore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file is the identifier layer. It never imports GORM: everything else
// here composes SQL strings, and a naming rule added after the statements
// are written has to be retrofitted into every statement already passing
// its tests. Keep it that way.

// ColumnType is a datastore column's wire type. The four values match n8n's
// Add Column dialog verbatim, except that the dialog labels the fourth
// "datetime" while the filter list and the wire carry "date": date is the
// contract, datetime is accepted as an alias and normalised to date on the
// way in so the catalogue never holds the UI label.
type ColumnType string

const (
	ColumnString  ColumnType = "string"
	ColumnNumber  ColumnType = "number"
	ColumnBoolean ColumnType = "boolean"
	ColumnDate    ColumnType = "date"
)

// datetimeAlias is the UI label for ColumnDate. It is accepted wherever a
// column type is read and never stored.
const datetimeAlias = "datetime"

// ColumnDef describes one user column of a datastore. Position is the
// catalogue `index` that preserves column order; indexes are named from it,
// never from the column name, because a 63-byte column name appended to a
// table name is past the budget before any suffix.
type ColumnDef struct {
	Name     string
	Type     ColumnType
	Position int
}

// columnNamePattern is the whole rule for user column names: start with a
// letter, then letters, digits and underscores. Anything else — including a
// double quote — is refused before any SQL is composed.
var columnNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// maxColumnNameBytes is PostgreSQL's identifier limit, which SQLite accepts
// as well. Identifiers are ASCII by the pattern above, so length and bytes
// coincide.
const maxColumnNameBytes = 63

// reservedColumns may never be user columns, matched case-insensitively:
// SQLite folds ASCII identifiers while PostgreSQL treats a quoted identifier
// as case-sensitive, so "createdAt" and "CreatedAt" are two columns on one
// driver and one on the other. Reserving all four spellings is what makes a
// definition mean the same thing on both. dryRunState is not a physical
// column; it is reserved because the wire already uses it.
var reservedColumns = map[string]string{
	"id":          "id",
	"createdat":   "createdAt",
	"updatedat":   "updatedAt",
	"dryrunstate": "dryRunState",
}

// systemColumns are the columns every physical table carries beyond the
// user's own. They are exactly the set the reservation above protects.
var systemColumns = []string{"id", "createdAt", "updatedAt"}

// normalizeColumnType maps the accepted spellings to the wire value, so the
// catalogue stores "date" even when the caller wrote the UI label.
func normalizeColumnType(raw string) (ColumnType, error) {
	switch ColumnType(raw) {
	case ColumnString, ColumnNumber, ColumnBoolean, ColumnDate:
		return ColumnType(raw), nil
	}
	if raw == datetimeAlias {
		return ColumnDate, nil
	}
	return "", fmt.Errorf("datastore: unknown column type %q, want one of string, number, boolean, date", raw)
}

// validateColumnName refuses a name outside the pattern, past the byte
// budget, or colliding with a reserved word. It names the reserved word it
// matched so the caller knows which rule fired.
func validateColumnName(name string) error {
	if len(name) == 0 || len(name) > maxColumnNameBytes || !columnNamePattern.MatchString(name) {
		return fmt.Errorf("datastore: invalid column name %q, want 1-63 bytes matching ^[a-zA-Z][a-zA-Z0-9_]*$", name)
	}
	if canonical, reserved := reservedColumns[strings.ToLower(name)]; reserved {
		return fmt.Errorf("datastore: column name %q is reserved (reserved word %q)", name, canonical)
	}
	return nil
}

// ColumnInput is one user column as the caller wrote it: the type may still
// carry the "datetime" UI label, which normalisation maps to the "date"
// wire value before anything is stored or composed.
type ColumnInput struct {
	Name string
	Type string
}

// normalizeColumns validates a definition and stamps catalogue positions in
// the order given. It runs before any SQL is composed, so an invalid
// definition fails without touching the database.
func normalizeColumns(in []ColumnInput) ([]ColumnDef, error) {
	seen := map[string]string{}
	cols := make([]ColumnDef, 0, len(in))
	for i, col := range in {
		if err := validateColumnName(col.Name); err != nil {
			return nil, err
		}
		lower := strings.ToLower(col.Name)
		if first, dup := seen[lower]; dup {
			return nil, fmt.Errorf("datastore: column name %q collides with %q differing only in case", col.Name, first)
		}
		seen[lower] = col.Name
		typ, err := normalizeColumnType(col.Type)
		if err != nil {
			return nil, err
		}
		cols = append(cols, ColumnDef{Name: col.Name, Type: typ, Position: i})
	}
	return cols, nil
}

// mintSurrogate returns sixteen hex characters of fresh randomness. It is
// random rather than derived from the datastore id on purpose: a hash is
// deterministic, so a collision is permanent, while a random collision is
// retried against the catalogue's unique constraint.
func mintSurrogate() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("datastore: mint surrogate: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// PhysicalTableName is the table a datastore lives in: the configured
// prefix, ds_, and the surrogate. The public id appears in no identifier on
// purpose; see the package doc for the budget that forces it.
func PhysicalTableName(prefix, surrogate string) string {
	return prefix + "ds_" + surrogate
}

// PhysicalPKName is the explicitly-named primary-key constraint on a
// physical table. The name is chosen by this package so PostgreSQL never
// names anything on its own: an unnamed inline PRIMARY KEY would read back
// as <table>_pkey, which is still deterministic, but stating it keeps every
// name in the DDL under one authority.
func PhysicalPKName(prefix, surrogate string) string {
	return prefix + "ds_" + surrogate + "_pkey"
}

// PhysicalIndexName is the deterministic name for a per-column index at the
// given catalogue position. Indexes are named from the surrogate and the
// position, never from the column name, for the budget reason ColumnDef
// states. This ticket creates no per-column indexes; the function fixes the
// rule now so a later ticket cannot invent a colliding one.
func PhysicalIndexName(prefix, surrogate string, position int) string {
	return prefix + "dsidx_" + surrogate + "_" + strconv.Itoa(position)
}

// quoteIdent quotes a bare identifier the way the dialect expects, doubling
// any embedded quote. It mirrors database.quoteIdentifier, which this
// package cannot reuse unexported; the runtime DDL path is not GORM and
// never goes through the migration runner's prefixStatement.
func quoteIdent(dialect, name string) string {
	if dialect == "sqlite" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// physicalType maps a wire type to the dialect's storage type. The sets
// match n8n so a datastore stays portable: the read side normalises
// SQLite's 0/1 booleans and DATETIME strings back to Go values.
func physicalType(dialect string, typ ColumnType) (string, error) {
	if dialect == "sqlite" {
		switch typ {
		case ColumnString:
			return "TEXT", nil
		case ColumnNumber:
			return "REAL", nil
		case ColumnBoolean:
			return "BOOLEAN", nil
		case ColumnDate:
			return "DATETIME(3)", nil
		}
	} else {
		switch typ {
		case ColumnString:
			return "TEXT", nil
		case ColumnNumber:
			return "DOUBLE PRECISION", nil
		case ColumnBoolean:
			return "BOOLEAN", nil
		case ColumnDate:
			return "TIMESTAMPTZ(3)", nil
		}
	}
	return "", fmt.Errorf("datastore: unknown column type %q for dialect %q", string(typ), dialect)
}
