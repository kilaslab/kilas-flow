package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowBody is one workflow resource as the API returns it.
const workflowBody = `{"id":"wf_1","name":"Orders","active":false,` +
	`"latestVersion":{"id":"wfv_1","workflowId":"wf_1","revision":1,"schemaVersion":1},` +
	`"createdAt":"2026-09-20T10:00:00Z","updatedAt":"2026-09-20T10:00:00Z"}`

// recordingAPI is a stub that records what it was asked, so a test can assert
// the method, the path, the query and the body a verb chose.
type recordingAPI struct {
	calls  []recordedCall
	routes map[string]http.HandlerFunc
}

// recordedCall is one request as the stub saw it.
type recordedCall struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// newRecordingAPI returns a stub whose routes answer canned JSON.
func newRecordingAPI(routes map[string]http.HandlerFunc) *recordingAPI {
	return &recordingAPI{routes: routes}
}

// routesFor renders the stub as the route table stubAPI takes, wrapping each
// handler in the recorder.
func (api *recordingAPI) routesFor(t *testing.T) map[string]http.HandlerFunc {
	t.Helper()

	out := make(map[string]http.HandlerFunc, len(api.routes))
	for path, handler := range api.routes {
		out[path] = func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			api.calls = append(api.calls, recordedCall{
				Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body),
			})
			handler(w, r)
		}
	}

	return out
}

// last returns the only call the stub saw, failing when there was another.
func (api *recordingAPI) last(t *testing.T) recordedCall {
	t.Helper()

	if len(api.calls) != 1 {
		t.Fatalf("the stub saw %d requests, want exactly one: %+v", len(api.calls), api.calls)
	}

	return api.calls[0]
}

func TestWorkflowListReadsAPageAndReportsTheNextCursor(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Cursor", "cur_2")
			_, _ = io.WriteString(w, `[{"id":"wf_1","name":"Orders","active":false},{"id":"wf_2","name":"Refunds","active":true}]`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "list", "--url", srv.URL, "--limit", "2", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/workflows" || call.Query != "limit=2" {
		t.Fatalf("call = %+v, want GET %s/workflows?limit=2", call, apiPrefix)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(2) {
		t.Errorf("data.count = %v, want 2", data["count"])
	}
	if data["nextCursor"] != "cur_2" {
		t.Errorf("data.nextCursor = %v, want cur_2", data["nextCursor"])
	}
	items, _ := data["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["id"] != "wf_1" || first["name"] != "Orders" {
		t.Errorf("data.items[0] = %v, want the server's own fields", first)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-workflows" {
		t.Errorf("meta.operation = %v, want list-workflows", meta["operation"])
	}
}

func TestWorkflowListQuietPrintsOneIdPerLine(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows": jsonBody(http.StatusOK, `[{"id":"wf_1"},{"id":"wf_2"}]`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "list", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stdout != "wf_1\nwf_2\n" {
		t.Fatalf("stdout = %q, want one id per line", stdout)
	}
}

func TestWorkflowGetReadsOneWorkflow(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1": jsonBody(http.StatusOK, workflowBody),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "get", "wf_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/workflows/wf_1" {
		t.Fatalf("call = %+v, want GET /workflows/wf_1", call)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["id"] != "wf_1" || data["name"] != "Orders" {
		t.Fatalf("data = %v, want the workflow resource", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-workflow" {
		t.Fatalf("meta.operation = %v, want get-workflow", meta["operation"])
	}
}

func TestWorkflowGetWithoutAnIdIsAUsageError(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "get", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing id sent %d requests, want none", len(api.calls))
	}
}

func TestWorkflowCreatePostsTheDocumentUnchanged(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", apiPrefix+"/workflows/wf_new")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, workflowBody)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	document := `{"schemaVersion":1,"name":"Orders","nodes":[],"connections":[],"settings":{}}`
	path := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "create", "--file", path, "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/workflows" {
		t.Fatalf("call = %+v, want POST /workflows", call)
	}
	if call.Body != document {
		t.Fatalf("body = %q, want the document unchanged (%q)", call.Body, document)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["id"] != "wf_1" {
		t.Fatalf("data = %v, want the created resource", data)
	}
}

func TestWorkflowCreateQuietPrintsTheNewId(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", apiPrefix+"/workflows/wf_from_location")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, workflowBody)
		},
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "create", "--file", "-", "--url", srv.URL, "--quiet"},
		Stdin:  strings.NewReader(`{"schemaVersion":1,"name":"x","nodes":[],"connections":[],"settings":{}}`),
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stdout != "wf_from_location\n" {
		t.Fatalf("stdout = %q, want the id from the Location header", stdout)
	}
}

func TestWorkflowCreateRefusesADocumentThatIsNotJSON(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": jsonBody(http.StatusCreated, workflowBody),
	})
	srv := stubAPI(t, api.routesFor(t))

	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "create", "--file", path, "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a malformed document sent %d requests, want none", len(api.calls))
	}
}

func TestWorkflowVersionsReadsARevisionPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/versions": jsonBody(http.StatusOK,
			`{"items":[{"id":"wfv_2","workflowId":"wf_1","revision":2,"draft":true,"published":false}],"nextCursor":"cur_2"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "versions", "wf_1", "--url", srv.URL, "--limit", "5", "--cursor", "cur_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Path != apiPrefix+"/workflows/wf_1/versions" || call.Query != "cursor=cur_1&limit=5" {
		t.Fatalf("call = %+v, want the revision page with both parameters", call)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["nextCursor"] != "cur_2" || data["count"] != float64(1) {
		t.Fatalf("data = %v, want the page and its cursor", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-workflow-versions" {
		t.Fatalf("meta.operation = %v, want list-workflow-versions", meta["operation"])
	}
}

func TestWorkflowGetVersionReadsOneRevision(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/versions/wfv_2": jsonBody(http.StatusOK,
			`{"id":"wfv_2","workflowId":"wf_1","revision":2,"document":{"name":"Orders"}}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "get-version", "wf_1", "wfv_2", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Path != apiPrefix+"/workflows/wf_1/versions/wfv_2" {
		t.Fatalf("call = %+v, want the revision", call)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["id"] != "wfv_2" {
		t.Fatalf("data = %v, want the revision resource", data)
	}
}

func TestWorkflowGetVersionNeedsBothIdentifiers(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "get-version", "wf_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing revision id sent %d requests, want none", len(api.calls))
	}
}

func TestWorkflowPublishEventsReadsTheAuditTrail(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/publish-events": jsonBody(http.StatusOK,
			`[{"workflowId":"wf_1","versionId":"wfv_1","action":"published","actor":"k_1","createdAt":"2026-09-20T10:00:00Z"}]`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "publish-events", "wf_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Path != apiPrefix+"/workflows/wf_1/publish-events" {
		t.Fatalf("call = %+v, want the audit trail", call)
	}

	doc := envelope(t, stdout)
	events, _ := doc["data"].([]any)
	if len(events) != 1 {
		t.Fatalf("data = %v, want the audit rows", doc["data"])
	}
}

func TestWorkflowExportAndDiagnosticsReadTheirOperations(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/export": jsonBody(http.StatusOK,
			`{"format":"n8n","workflow":{"name":"Orders"},"lossy":[]}`),
		apiPrefix + "/workflows/wf_1/diagnostics": jsonBody(http.StatusOK,
			`{"workflowId":"wf_1","versionId":"wfv_1","revision":1,"issues":[]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "export", "wf_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("export exit = %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	doc := envelope(t, stdout)
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "export-workflow" {
		t.Fatalf("export meta.operation = %v, want export-workflow", meta["operation"])
	}

	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"workflow", "diagnostics", "wf_1", "--url", srv.URL, "--version-id", "wfv_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("diagnostics exit = %d (stdout=%q stderr=%q)", code, stdout, stderr)
	}
	doc = envelope(t, stdout)
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "workflow-diagnostics" {
		t.Fatalf("diagnostics meta.operation = %v, want workflow-diagnostics", meta["operation"])
	}
	if data, _ := doc["data"].(map[string]any); data["workflowId"] != "wf_1" {
		t.Fatalf("diagnostics data = %v", doc["data"])
	}

	if len(api.calls) != 2 {
		t.Fatalf("the stub saw %d calls, want 2: %+v", len(api.calls), api.calls)
	}
	if api.calls[1].Query != "versionId=wfv_1" {
		t.Fatalf("diagnostics query = %q, want versionId=wfv_1", api.calls[1].Query)
	}
}

func TestWorkflowVerbsCarryAServerRefusalUnchanged(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_missing": problemBody(http.StatusNotFound,
			`{"title":"Not Found","status":404,"detail":"workflow not found"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "get", "wf_missing", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotFound, stdout, stderr)
	}

	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	if failure["code"] != "not_found" || failure["status"] != float64(404) {
		t.Fatalf("error = %v, want a not_found carrying the status", failure)
	}
	detail, _ := failure["detail"].(map[string]any)
	problem, _ := json.Marshal(detail["problem"])
	if !strings.Contains(string(problem), "workflow not found") {
		t.Fatalf("the problem document was not carried verbatim: %s", problem)
	}
}
