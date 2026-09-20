package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// datastoreCSV is what the export route streams.
const datastoreCSV = "id,name\n1,Orders\n2,Refunds\n"

// csvBody answers with the media type the export route pre-seeds.
func csvBody(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestDatastoreListReadsAPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Cursor", "cur_2")
			_, _ = io.WriteString(w, `{"items":[{"id":"ds_1","name":"Orders","columns":[]}],"nextCursor":"cur_2"}`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"datastore", "list", "--url", srv.URL, "--limit", "5", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/datastores" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/datastores")
	}
	if !strings.Contains(call.Query, "limit=5") {
		t.Errorf("query %q is missing limit=5", call.Query)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) || data["nextCursor"] != "cur_2" {
		t.Fatalf("data = %v, want the page", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-datastores" {
		t.Fatalf("meta.operation = %v, want list-datastores", meta["operation"])
	}
}

func TestDatastoreGetReadsOne(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1": jsonBody(http.StatusOK,
			`{"id":"ds_1","name":"Orders","columns":[{"name":"total","type":"number"}]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"datastore", "get", "ds_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/datastores/ds_1" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/datastores/ds_1")
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["name"] != "Orders" {
		t.Fatalf("data = %v, want the datastore", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-datastore" {
		t.Fatalf("meta.operation = %v, want get-datastore", meta["operation"])
	}
}

func TestDatastoreRowsReadsAPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1/rows": jsonBody(http.StatusOK,
			`{"items":[{"id":1,"createdAt":"2026-09-20T10:00:00Z"}],"nextCursor":"cur_2"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"datastore", "rows", "ds_1", "--limit", "5", "--cursor", "cur_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/datastores/ds_1/rows" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/datastores/ds_1/rows")
	}
	for _, want := range []string{"limit=5", "cursor=cur_1"} {
		if !strings.Contains(call.Query, want) {
			t.Errorf("query %q is missing %s", call.Query, want)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) || data["nextCursor"] != "cur_2" {
		t.Fatalf("data = %v, want the page", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-datastore-rows" {
		t.Fatalf("meta.operation = %v, want list-datastore-rows", meta["operation"])
	}
}

func TestDatastoreExportStreamsTheCSVItWasSent(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1/rows/export": csvBody(http.StatusOK, datastoreCSV),
	})
	srv := stubAPI(t, api.routesFor(t))

	// --json is passed on purpose: this verb's documented exception is that its
	// bytes are the output, so the flag must not wrap them in an envelope.
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"datastore", "export", "ds_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/datastores/ds_1/rows/export" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/datastores/ds_1/rows/export")
	}
	if stdout != datastoreCSV {
		t.Fatalf("stdout = %q, want the CSV byte for byte", stdout)
	}
}

func TestDatastoreExportWritesTheFileItWasGiven(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1/rows/export": csvBody(http.StatusOK, datastoreCSV),
	})
	srv := stubAPI(t, api.routesFor(t))

	path := filepath.Join(t.TempDir(), "rows.csv")

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"datastore", "export", "ds_1", "--out", path, "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(written) != datastoreCSV {
		t.Fatalf("file = %q, want the CSV byte for byte", written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600: an export is customer data", perm)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["path"] != path || data["bytes"] != float64(len(datastoreCSV)) {
		t.Fatalf("data = %v, want the path and the byte count", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "export-datastore-rows" {
		t.Fatalf("meta.operation = %v, want export-datastore-rows", meta["operation"])
	}
}

// A directory the process cannot write to is an environment failure, not a
// malformed invocation: the documented contract reads exit 2 as "fix the
// invocation" and exit 1 as "report, do not retry blindly", and a caller that
// is told the second will not retry the same full disk forever.
func TestDatastoreExportReportsAnUnwritableFileAsAFailure(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/datastores/ds_1/rows/export": csvBody(http.StatusOK, datastoreCSV),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{
			"datastore", "export", "ds_1", "--url", srv.URL, "--json",
			"--out", filepath.Join(t.TempDir(), "no-such-dir", "rows.csv"),
		},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitFailure, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "output_error" {
		t.Fatalf("error.code = %v, want output_error", failure["code"])
	}
}
