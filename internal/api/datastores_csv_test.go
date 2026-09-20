package api_test

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
)

// csvImportReport mirrors the transfer report for response decoding.
type csvImportReport struct {
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
	Failed   []struct {
		Line     int    `json:"line"`
		Column   string `json:"column"`
		Severity string `json:"severity"`
		Reason   string `json:"reason"`
	} `json:"failed"`
}

func createDatastoreWithColumns(t *testing.T, handler http.Handler, name string, columns ...[2]string) datastoreResource {
	t.Helper()
	specs := make([]map[string]any, 0, len(columns))
	for _, column := range columns {
		specs = append(specs, map[string]any{"name": column[0], "type": column[1]})
	}
	return requestJSON[datastoreResource](t, handler, http.MethodPost, "/api/v1/datastores",
		map[string]any{"name": name, "columns": specs}, http.StatusCreated)
}

func insertRow(t *testing.T, handler http.Handler, id string, values map[string]any) {
	t.Helper()
	recorder := doJSON(t, handler, http.MethodPost, "/api/v1/datastores/"+id+"/rows", map[string]any{"values": values})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("insert row = %d, want 201 (body: %s)", recorder.Code, recorder.Body)
	}
}

func exportCSV(t *testing.T, handler http.Handler, id, query string) (string, []byte) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/datastores/"+id+"/rows/export"+query, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	contentType := recorder.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("export Content-Type = %q, want text/csv", contentType)
	}
	disposition := recorder.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") {
		t.Fatalf("export Content-Disposition = %q, want an attachment", disposition)
	}
	return recorder.Body.String(), recorder.Body.Bytes()
}

func importCSV(t *testing.T, handler http.Handler, id, file string, wantStatus int) csvImportReport {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datastores/"+id+"/rows/import", strings.NewReader(file))
	request.Header.Set("Content-Type", "text/csv")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("import = %d, want %d (body: %s)", recorder.Code, wantStatus, recorder.Body)
	}
	if wantStatus != http.StatusOK {
		return csvImportReport{}
	}
	var report csvImportReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode import report: %v (body: %s)", err, recorder.Body)
	}
	return report
}

func rowCount(t *testing.T, handler http.Handler, id string) int {
	t.Helper()
	listed := requestJSON[struct {
		Items []rowResource `json:"items"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+id+"/rows?limit=100", nil, http.StatusOK)
	return len(listed.Items)
}

func TestExportHeaderIsExactlyUserColumnsInIndexOrder(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"zeta", "string"}, [2]string{"alpha", "number"})
	insertRow(t, handler, created.ID, map[string]any{"zeta": "x", "alpha": 1})

	raw, _ := exportCSV(t, handler, created.ID, "")
	records, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("parse exported CSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("exported %d records, want header + one row", len(records))
	}
	if got, want := strings.Join(records[0], "|"), "zeta|alpha"; got != want {
		t.Fatalf("header = %q, want %q (catalogue index order, not alphabetical)", got, want)
	}
}

func TestExportWithSystemColumnsWrapsIdAndTimestamps(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})
	insertRow(t, handler, created.ID, map[string]any{"email": "a@example.com"})

	raw, _ := exportCSV(t, handler, created.ID, "?includeSystemColumns=true")
	records, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("parse exported CSV: %v", err)
	}
	if got, want := strings.Join(records[0], "|"), "id|email|createdAt|updatedAt"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
	row := records[1]
	if row[0] == "" || row[0] == "0" {
		t.Fatalf("id cell = %q, want the store's integer id", row[0])
	}
	// The timestamps must name the same instants the row store returned over
	// JSON: millisecond rendering of the same value, not a reformatting.
	listed := requestJSON[struct {
		Items []rowResource `json:"items"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=100", nil, http.StatusOK)
	jsonCreated, _ := listed.Items[0]["createdAt"].(string)
	jsonUpdated, _ := listed.Items[0]["updatedAt"].(string)
	for _, pair := range [][2]string{{row[2], jsonCreated}, {row[3], jsonUpdated}} {
		csvInstant, err := time.Parse(time.RFC3339Nano, pair[0])
		if err != nil {
			t.Fatalf("export timestamp %q does not parse: %v", pair[0], err)
		}
		jsonInstant, err := time.Parse(time.RFC3339Nano, pair[1])
		if err != nil {
			t.Fatalf("row timestamp %q does not parse: %v", pair[1], err)
		}
		if !csvInstant.Equal(jsonInstant) {
			t.Fatalf("export timestamp %q names a different instant than the row store's %q", pair[0], pair[1])
		}
	}
}

func TestCSVRoundTripIsByteExactAcrossTypes(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	columns := [][2]string{{"name", "string"}, {"score", "number"}, {"active", "boolean"}, {"seen", "date"}}
	source := createDatastoreWithColumns(t, handler, "Source", columns...)
	insertRow(t, handler, source.ID, map[string]any{
		"name": "plain", "score": 3.5, "active": true, "seen": "2026-09-05T15:07:56.694Z",
	})
	insertRow(t, handler, source.ID, map[string]any{
		// Hostile cells: commas, quotes, newlines and unicode must survive
		// the quoting round trip byte-exact.
		"name": "comma, quote\" newline\n unicode héllo→日本語", "score": -12.25, "active": false,
		"seen": "2026-01-02",
	})
	insertRow(t, handler, source.ID, map[string]any{
		// Empty string stays a string; missing keys stay NULL.
		"name": "", "score": 0,
	})

	raw, _ := exportCSV(t, handler, source.ID, "")
	target := createDatastoreWithColumns(t, handler, "Target", columns...)
	report := importCSV(t, handler, target.ID, raw, http.StatusOK)
	if report.Inserted != 3 || len(report.Failed) != 0 {
		t.Fatalf("import report = %+v, want 3 inserted and no failures", report)
	}

	// The target's export must be byte-identical: same values, same types,
	// same order, same quoting.
	again, _ := exportCSV(t, handler, target.ID, "")
	if again != raw {
		t.Fatalf("round-trip export differs:\nfirst:\n%s\nsecond:\n%s", raw, again)
	}

	// Spot-check the normalised types, not just the bytes: numbers read back
	// as numbers, booleans as booleans, dates as instants, NULLs as null.
	listed := requestJSON[struct {
		Items []rowResource `json:"items"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+target.ID+"/rows?limit=100", nil, http.StatusOK)
	first := listed.Items[0]
	if got, ok := first["score"].(float64); !ok || got != 3.5 {
		t.Fatalf("score = %#v, want float64 3.5", first["score"])
	}
	if got, ok := first["active"].(bool); !ok || !got {
		t.Fatalf("active = %#v, want bool true", first["active"])
	}
	second := listed.Items[1]
	if got := fmt.Sprintf("%v", second["name"]); !strings.Contains(got, "comma,") || !strings.Contains(got, "日本語") {
		t.Fatalf("hostile name = %q, want the commas, quotes, newline and unicode intact", got)
	}
	third := listed.Items[2]
	if got, ok := third["name"].(string); !ok || got != "" {
		t.Fatalf("empty string = %#v, want string \"\"", third["name"])
	}
	if _, present := third["active"]; third["active"] != nil && present {
		t.Fatalf("missing boolean = %#v, want null", third["active"])
	}
}

func TestImportReservedHeaderIsRefusedNamingIt(t *testing.T) {
	for _, reserved := range []string{"id", "createdAt", "updatedAt", "dryRunState"} {
		t.Run(reserved, func(t *testing.T) {
			handler, _ := newDatastoreAPI(t, "tenant-a")
			created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})
			file := "email," + reserved + "\na@example.com,1\n"
			request := httptest.NewRequest(http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/import", strings.NewReader(file))
			request.Header.Set("Content-Type", "text/csv")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("import = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
			}
			if !strings.Contains(recorder.Body.String(), reserved) {
				t.Fatalf("refusal %s does not name %q", recorder.Body.String(), reserved)
			}
			if got := rowCount(t, handler, created.ID); got != 0 {
				t.Fatalf("row count = %d, want 0: a refused import writes nothing", got)
			}
		})
	}
}

func TestImportAboveByteLimitIsRefused413(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Bulk", [2]string{"payload", "string"})
	// One cell past the five-mebibyte ceiling the import operation declares.
	file := "payload\n" + strings.Repeat("x", 5*1024*1024+64) + "\n"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datastores/"+created.ID+"/rows/import", strings.NewReader(file))
	request.Header.Set("Content-Type", "text/csv")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("import = %d, want 413 (body: %s)", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "5242880") {
		t.Fatalf("refusal %s does not name the configured limit", recorder.Body.String())
	}
}

func TestMalformedRowFailsWithLineAndColumnAndWritesNothing(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"}, [2]string{"score", "number"})
	file := "email,score\na@example.com,3\n   \nb@example.com,not-a-number\nc@example.com,5\n"
	report := importCSV(t, handler, created.ID, file, http.StatusOK)
	if report.Inserted != 0 {
		t.Fatalf("inserted = %d, want 0: a file with a failed row imports nothing", report.Inserted)
	}
	if report.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 for the blank line", report.Skipped)
	}
	if len(report.Failed) != 1 {
		t.Fatalf("failed = %+v, want exactly one issue", report.Failed)
	}
	issue := report.Failed[0]
	if issue.Line != 4 || issue.Column != "score" || issue.Severity != "blocking" {
		t.Fatalf("issue = %+v, want line 4, column score, blocking severity", issue)
	}
	if got := rowCount(t, handler, created.ID); got != 0 {
		t.Fatalf("row count = %d, want 0: no partial write", got)
	}
}

func TestCSVCrossTenantReadsAsUnknown(t *testing.T) {
	handler, engine := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Private", [2]string{"email", "string"})

	other := newTestServer(t, api.Deps{Datastores: engine, Tenants: fixedTenant{id: "tenant-b"}})
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/datastores/" + created.ID + "/rows/export"},
		{http.MethodPost, "/api/v1/datastores/" + created.ID + "/rows/import"},
	} {
		var request *http.Request
		if route[0] == http.MethodPost {
			request = httptest.NewRequest(route[0], route[1], strings.NewReader("email\na@example.com\n"))
			request.Header.Set("Content-Type", "text/csv")
		} else {
			request = httptest.NewRequest(route[0], route[1], nil)
		}
		recorder := httptest.NewRecorder()
		other.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s as tenant-b = %d, want 404 (body: %s)", route[0], route[1], recorder.Code, recorder.Body)
		}
	}
}

func TestImportStripsBOM(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Scores", [2]string{"email", "string"})
	// Without the strip the mark glues itself to "email", which matches no
	// column and shifts every value one place with no error anywhere.
	file := "\xef\xbb\xbfemail\na@example.com\nb@example.com\n"
	report := importCSV(t, handler, created.ID, file, http.StatusOK)
	if report.Inserted != 2 || len(report.Failed) != 0 {
		t.Fatalf("report = %+v, want 2 inserted and no failures", report)
	}
}

func TestImportEmptyFieldIsNullExceptForStrings(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	created := createDatastoreWithColumns(t, handler, "Mixed",
		[2]string{"name", "string"}, [2]string{"score", "number"},
		[2]string{"active", "boolean"}, [2]string{"seen", "date"})
	report := importCSV(t, handler, created.ID, "name,score,active,seen\n,,,\n", http.StatusOK)
	if report.Inserted != 1 || len(report.Failed) != 0 {
		t.Fatalf("report = %+v, want one inserted row", report)
	}
	listed := requestJSON[struct {
		Items []rowResource `json:"items"`
	}](t, handler, http.MethodGet, "/api/v1/datastores/"+created.ID+"/rows?limit=100", nil, http.StatusOK)
	row := listed.Items[0]
	if got, ok := row["name"].(string); !ok || got != "" {
		t.Fatalf("empty string = %#v, want string \"\"", row["name"])
	}
	for _, column := range []string{"score", "active", "seen"} {
		if row[column] != nil {
			t.Fatalf("empty %s = %#v, want null", column, row[column])
		}
	}
}

func TestTransferEndpointsAreUnavailableWithoutAStore(t *testing.T) {
	handler := newTestServer(t, api.Deps{Tenants: fixedTenant{id: "tenant-a"}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/datastores/datastore_x/rows/export", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("export without a store = %d, want 503 (body: %s)", recorder.Code, recorder.Body)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/datastores/datastore_x/rows/import", strings.NewReader("a\n1\n"))
	upload.Header.Set("Content-Type", "text/csv")
	uploadRecorder := httptest.NewRecorder()
	handler.ServeHTTP(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("import without a store = %d, want 503 (body: %s)", uploadRecorder.Code, uploadRecorder.Body)
	}
}

func TestExportUnknownDatastoreIs404(t *testing.T) {
	handler, _ := newDatastoreAPI(t, "tenant-a")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/datastores/datastore_missing/rows/export", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("export = %d, want 404 (body: %s)", recorder.Code, recorder.Body)
	}
}
