package datastore

import (
	"strings"
	"testing"

	"gorm.io/gorm/schema"

	"github.com/kilaslabs/kilas-flow/internal/config"
)

// The reserved words refuse every case permutation, and the error names the
// canonical reserved word rather than just the offending input.
func TestReservedWordsRefuseEveryCasePermutation(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, canonical string }{
		{"id", "id"},
		{"ID", "id"},
		{"Id", "id"},
		{"createdAt", "createdAt"},
		{"CreatedAt", "createdAt"},
		{"CREATEDAT", "createdAt"},
		{"updatedAt", "updatedAt"},
		{"UPDATEDAT", "updatedAt"},
		{"UpdatedAt", "updatedAt"},
		{"dryRunState", "dryRunState"},
		{"dryrunstate", "dryRunState"},
		{"DRYRUNSTATE", "dryRunState"},
		{"DryRunState", "dryRunState"},
	}
	for _, c := range cases {
		err := validateColumnName(c.in)
		if err == nil {
			t.Errorf("validateColumnName(%q) = nil, want refusal", c.in)
			continue
		}
		if !strings.Contains(err.Error(), c.canonical) {
			t.Errorf("validateColumnName(%q) = %q, want it to name reserved word %q", c.in, err, c.canonical)
		}
	}
}

// Names outside the pattern, past the byte budget, or colliding only in
// case are refused before any SQL is composed — including an embedded
// double quote, which the pattern rejects rather than the quoter escaping.
func TestInvalidColumnNamesAreRefusedBeforeAnySQL(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 64)
	cases := []struct {
		name string
		cols []ColumnInput
	}{
		{"embedded double quote", []ColumnInput{{Name: `a"b`, Type: "string"}}},
		{"leading digit", []ColumnInput{{Name: "1abc", Type: "string"}}},
		{"leading underscore", []ColumnInput{{Name: "_abc", Type: "string"}}},
		{"empty", []ColumnInput{{Name: "", Type: "string"}}},
		{"with space", []ColumnInput{{Name: "a b", Type: "string"}}},
		{"with dash", []ColumnInput{{Name: "a-b", Type: "string"}}},
		{"past 63 bytes", []ColumnInput{{Name: long, Type: "string"}}},
		{"differing only in case", []ColumnInput{{Name: "Title", Type: "string"}, {Name: "TITLE", Type: "number"}}},
		{"reserved id", []ColumnInput{{Name: "id", Type: "string"}}},
		{"unknown type", []ColumnInput{{Name: "ok", Type: "object"}}},
		{"empty type", []ColumnInput{{Name: "ok", Type: ""}}},
	}
	for _, c := range cases {
		if _, err := normalizeColumns(c.cols); err == nil {
			t.Errorf("%s: normalizeColumns = nil, want refusal", c.name)
		}
	}

	// Exactly 63 bytes is the boundary that passes.
	if _, err := normalizeColumns([]ColumnInput{{Name: strings.Repeat("z", 63), Type: "string"}}); err != nil {
		t.Errorf("63-byte name refused: %v", err)
	}
}

// The same definition yields the two dialect type sets from the ticket, and
// the UI's "datetime" label normalises to the "date" wire value rather than
// being stored.
func TestTheSameDefinitionYieldsBothDialectTypeSets(t *testing.T) {
	t.Parallel()

	surrogate := "0123456789abcdef"
	cols, err := normalizeColumns([]ColumnInput{
		{Name: "title", Type: "string"},
		{Name: "score", Type: "number"},
		{Name: "flag", Type: "boolean"},
		{Name: "happened", Type: "datetime"},
	})
	if err != nil {
		t.Fatalf("normalizeColumns: %v", err)
	}
	if cols[3].Type != ColumnDate {
		t.Fatalf("datetime alias stored as %q, want the date wire value", cols[3].Type)
	}
	for i, want := range []int{0, 1, 2, 3} {
		if cols[i].Position != want {
			t.Errorf("column %d position = %d, want %d", i, cols[i].Position, want)
		}
	}

	pg := createTableStatement("postgres", "", surrogate, cols)
	for _, want := range []string{"TEXT", "DOUBLE PRECISION", "BOOLEAN", "TIMESTAMPTZ(3)"} {
		if !strings.Contains(pg, want) {
			t.Errorf("postgres DDL lacks %q:\n%s", want, pg)
		}
	}

	lite := createTableStatement("sqlite", "", surrogate, cols)
	for _, want := range []string{"TEXT", "REAL", "BOOLEAN", "DATETIME(3)"} {
		if !strings.Contains(lite, want) {
			t.Errorf("sqlite DDL lacks %q:\n%s", lite, want)
		}
	}
	if strings.Contains(lite, "TIMESTAMPTZ") || strings.Contains(pg, "DATETIME") {
		t.Errorf("dialect types leaked across dialects:\npg:\n%s\nsqlite:\n%s", pg, lite)
	}
}

// One test enumerates every identifier the DDL path can emit at the maximum
// configured prefix and asserts each fits 63 bytes and the set stays
// pairwise unique after truncation to 63. PostgreSQL truncates silently, so
// two names differing only past byte 63 would collapse into one index and
// CREATE INDEX IF NOT EXISTS would skip the second without complaint.
func TestEveryEmittedIdentifierFitsPostgresAtTheLongestPrefix(t *testing.T) {
	t.Parallel()

	prefix := strings.Repeat("a", config.MaxTablePrefixLength-1) + "_"
	if len(prefix) != config.MaxTablePrefixLength {
		t.Fatalf("test prefix is %d bytes, want %d", len(prefix), config.MaxTablePrefixLength)
	}
	surrogate := strings.Repeat("f", 16)

	seen := map[string]string{}
	claim := func(describe, name string) {
		t.Helper()
		if len(name) > 63 {
			t.Errorf("%s %q is %d bytes, past the 63-byte limit", describe, name, len(name))
		}
		truncated := name
		if len(truncated) > 63 {
			truncated = truncated[:63]
		}
		if first, dup := seen[truncated]; dup {
			t.Errorf("%s %q collides after truncation with %s", describe, name, first)
		}
		seen[truncated] = describe + " " + name
	}

	claim("table", PhysicalTableName(prefix, surrogate))
	claim("pk", PhysicalPKName(prefix, surrogate))
	// Positions far past any sane column count: the property must hold for
	// every index the rule can produce, not for hand-picked examples.
	for position := 0; position < 512; position++ {
		claim("index", PhysicalIndexName(prefix, surrogate, position))
	}

	if got := PhysicalTableName("", surrogate); got != "ds_"+surrogate {
		t.Errorf("empty prefix table = %q, want identity %q", got, "ds_"+surrogate)
	}
	if _, err := NewEngine(nil, prefix); err != nil {
		t.Errorf("NewEngine at max prefix: %v", err)
	}
	if _, err := NewEngine(nil, prefix+"_"); err == nil {
		t.Errorf("NewEngine past max prefix = nil, want refusal")
	}
}

// Surrogates are sixteen lowercase hex characters: the shape the budget
// proof above assumes.
func TestSurrogateShape(t *testing.T) {
	t.Parallel()

	for range 8 {
		surrogate, err := mintSurrogate()
		if err != nil {
			t.Fatalf("mintSurrogate: %v", err)
		}
		if len(surrogate) != 16 {
			t.Errorf("surrogate %q is %d chars, want 16", surrogate, len(surrogate))
		}
		for _, c := range surrogate {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Errorf("surrogate %q is not lowercase hex", surrogate)
			}
		}
	}
}

// The catalogue models resolve through the namer like every other model,
// so the prefix the migration runner applies is the prefix GORM queries.
func TestCatalogueModelsResolveThroughTheNamer(t *testing.T) {
	t.Parallel()

	strategy := namerForTest("")
	if got := (datastoreModel{}).TableName(strategy); got != "datastores" {
		t.Errorf("datastores table = %q, want identity", got)
	}
	if got := (datastoreColumnModel{}).TableName(strategy); got != "datastore_columns" {
		t.Errorf("datastore_columns table = %q, want identity", got)
	}
	strategy = namerForTest("kflow_")
	if got := (datastoreModel{}).TableName(strategy); got != "kflow_datastores" {
		t.Errorf("prefixed datastores table = %q, want kflow_datastores", got)
	}
	if got := (datastoreColumnModel{}).TableName(strategy); got != "kflow_datastore_columns" {
		t.Errorf("prefixed datastore_columns table = %q, want kflow_datastore_columns", got)
	}
}

func namerForTest(prefix string) schema.Namer {
	return schema.NamingStrategy{TablePrefix: prefix}
}
