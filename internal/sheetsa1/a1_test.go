package sheetsa1_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/sheetsa1"
)

func TestQuoteDoublesAQuoteInsideATitle(t *testing.T) {
	t.Parallel()
	for title, want := range map[string]string{
		"Sheet1":     `'Sheet1'`,
		"Zed's data": `'Zed''s data'`,
		"a!b":        `'a!b'`,
		"":           `''`,
	} {
		if got := sheetsa1.Quote(title); got != want {
			t.Errorf("Quote(%q) = %s, want %s", title, got, want)
		}
	}
}

// The "!" that ends a sheet name is the last one outside single quotes.
func TestRegionDropsTheSheetPart(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"A1:C3":                  "A1:C3",
		"  A1:C3  ":              "A1:C3",
		"Sheet1!A1:C3":           "A1:C3",
		"'a!b'!A1:C3":            "A1:C3",
		"'it''s'!A:F":            "A:F",
		"'a!b'!'c!d'!A1":         "A1",
		"'a!b'":                  "'a!b'",
		"":                       "",
		"Sheet1!":                "",
		"Other!B1:B3":            "B1:B3",
		"'Sheet with space'!2:5": "2:5",
	} {
		if got := sheetsa1.Region(input); got != want {
			t.Errorf("Region(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStartRowIsTheRowARangeBeginsOn(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]int{
		"C4:E7": 4, "A:F": 1, "B2": 2, "$B$9:$D$12": 9, "3:8": 3, "": 1, "abc": 1, "ZZ100:ZZ200": 100,
	} {
		if got := sheetsa1.StartRow(input); got != want {
			t.Errorf("StartRow(%q) = %d, want %d", input, got, want)
		}
	}
}

// HeaderRange is the one row of a range that holds the column names, across the
// columns the range covers.
func TestHeaderRangeIsTheHeaderRowAcrossTheRangesColumns(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		region string
		row    int
		want   string
	}{
		{"", 1, "1:1"},
		{"", 3, "3:3"},
		{"A:F", 1, "A1:F1"},
		{"B2:D", 2, "B2:D2"},
		{"C4:E7", 4, "C4:E4"},
		{"$B$2:$D$9", 2, "B2:D2"},
		{"C4", 4, "C4:C4"},
		{"3:8", 3, "3:3"},
		{"AA2:AC9", 2, "AA2:AC2"},
	} {
		if got := sheetsa1.HeaderRange(test.region, test.row); got != test.want {
			t.Errorf("HeaderRange(%q, %d) = %q, want %q", test.region, test.row, got, test.want)
		}
	}
}
