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

// The n8n import/export verbs: n8n's CLI forms — a file, a directory, --all —
// on this server's operations, with the JSON envelope and the exit-code
// contract every other verb follows.

// importResponse is the import operation's answer, as the stub serves it.
const importResponse = `{"workflow":{"id":"wf_9","name":"Imported","active":false},` +
	`"unsupported":[{"severity":"blocking","nodeName":"Legacy","reason":"KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.emailSend\"."}],` +
	`"webhooks":[{"nodeId":"t","method":"POST","path":"hook","url":"/webhook/r_1"}]}`

// exportResponse is the export operation's answer, as the stub serves it: the
// server's own envelope around the bare n8n document.
const exportResponse = `{"format":"n8n","workflow":{"name":"Orders","nodes":[{"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"parameters":{}}],"connections":{}},` +
	`"lossy":[{"severity":"lossy","nodeName":"Set","reason":"a note n8n has no field for was dropped"}],` +
	`"supportedMappings":["n8n-nodes-base.manualTrigger ↔ kilasflow.manual"]}`

// bareExportDocument is the bare n8n document inside exportResponse: the
// bytes --out must write and nothing else.
const bareExportDocument = `{"name":"Orders","nodes":[{"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"parameters":{}}],"connections":{}}`

// workflowSummaryRow is one row of the workflow listing, as the stub serves it.
func workflowSummaryRow(id, name string, active bool) string {
	encoded, _ := json.Marshal(map[string]any{"id": id, "name": name, "active": active})
	return string(encoded)
}

// tenantWideKey routes the identity read every guarded verb's authority gate
// makes, answering a key with no scope list.
func tenantWideKey() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
	}
}

// usageMessage runs an invocation the verb must refuse as a usage error and
// returns the envelope's error message, because a non-terminal invocation
// reports failures in the JSON envelope rather than on stderr.
func usageMessage(t *testing.T, args []string) string {
	t.Helper()

	code, _, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	message, _ := failure["message"].(string)

	return message
}

// n8nDocument writes one n8n export to a temporary file and returns its path.
func n8nDocument(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "n8n.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("could not write the n8n document: %v", err)
	}

	return path
}

// TestWorkflowImportSendsOneFileUnderTheImportEnvelope: the file n8n wrote
// needs no editing — the CLI wraps it in {format, workflow} itself — and the
// envelope carries the import's own response, report and all.
func TestWorkflowImportSendsOneFileUnderTheImportEnvelope(t *testing.T) {
	document := n8nDocument(t, `{"name":"Imported","nodes":[],"connections":{}}`)

	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/auth/me":          jsonBody(http.StatusOK, identityWithoutScopes),
		apiPrefix + "/workflows/import": jsonBody(http.StatusCreated, importResponse),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "import", "--file", document, "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	var call recordedCall
	for _, seen := range api.calls {
		if seen.Method == http.MethodPost {
			call = seen
		}
	}
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/workflows/import" {
		t.Fatalf("calls = %+v, want POST %s/workflows/import among them", api.calls, apiPrefix)
	}
	var sent struct {
		Format   string          `json:"format"`
		Workflow json.RawMessage `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(call.Body), &sent); err != nil {
		t.Fatalf("the request body is not the import envelope: %v\n%s", err, call.Body)
	}
	if sent.Format != "n8n" || strings.TrimSpace(string(sent.Workflow)) != `{"name":"Imported","nodes":[],"connections":{}}` {
		t.Fatalf("the request carried format=%q and workflow=%s, want n8n and the document as it was read", sent.Format, sent.Workflow)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["workflow"] == nil || data["unsupported"] == nil || data["webhooks"] == nil {
		t.Fatalf("data = %v, want the import's own response with workflow, unsupported and webhooks", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "import-workflow" {
		t.Errorf("meta.operation = %v, want import-workflow", meta["operation"])
	}
}

// TestWorkflowImportQuietPrintsTheNewId pins the line an agent pipes onward.
func TestWorkflowImportQuietPrintsTheNewId(t *testing.T) {
	document := n8nDocument(t, `{"name":"Imported","nodes":[],"connections":{}}`)

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me":          jsonBody(http.StatusOK, identityWithoutScopes),
		apiPrefix + "/workflows/import": jsonBody(http.StatusCreated, importResponse),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "import", "--file", document, "--yes", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stdout != "wf_9\n" {
		t.Fatalf("stdout = %q, want the imported id", stdout)
	}
}

// TestWorkflowImportFromADirectoryImportsEveryJSONFile: n8n's --separate
// form. Only .json files, in name order; a directory named *.json is not a
// file and a .txt is not a workflow.
func TestWorkflowImportFromADirectoryImportsEveryJSONFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.json": `{"name":"A","nodes":[],"connections":{}}`,
		"b.json": `{"name":"B","nodes":[],"connections":{}}`,
		"c.txt":  "not a workflow",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("could not write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o755); err != nil {
		t.Fatalf("could not create the nested directory: %v", err)
	}

	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/auth/me":          jsonBody(http.StatusOK, identityWithoutScopes),
		apiPrefix + "/workflows/import": jsonBody(http.StatusCreated, importResponse),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "import", "--dir", dir, "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	imports := 0
	var bodies []string
	for _, call := range api.calls {
		if call.Method == http.MethodPost && call.Path == apiPrefix+"/workflows/import" {
			imports++
			bodies = append(bodies, call.Body)
		}
	}
	if imports != 2 {
		t.Fatalf("the stub saw %d imports, want one per .json file: %+v", imports, api.calls)
	}
	if bodies[0] == bodies[1] {
		t.Errorf("both imports sent the same document; want a.json then b.json, distinct")
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(2) {
		t.Errorf("data.count = %v, want 2", data["count"])
	}
	imported, _ := data["imported"].([]any)
	if len(imported) != 2 {
		t.Fatalf("data.imported has %d rows, want 2", len(imported))
	}
	first, _ := imported[0].(map[string]any)
	if first["file"] != filepath.Join(dir, "a.json") || first["id"] != "wf_9" {
		t.Errorf("data.imported[0] = %v, want file a.json and the imported id", first)
	}
}

// TestWorkflowImportNamesTheFileTheServerRefused: a directory run stops at
// the first refusal with the file's name in the message, keeping the server's
// own exit code for the status.
func TestWorkflowImportNamesTheFileTheServerRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(`{"name":"Good","nodes":[],"connections":{}}`), 0o600); err != nil {
		t.Fatalf("could not write good.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z-bad.json"), []byte(`{"name":"Bad","nodes":[]}`), 0o600); err != nil {
		t.Fatalf("could not write z-bad.json: %v", err)
	}
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
		apiPrefix + "/workflows/import": problemBody(http.StatusUnprocessableEntity,
			`{"title":"Unprocessable Entity","status":422,"detail":"this is not valid n8n workflow JSON: the workflow contains no connections"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "import", "--dir", dir, "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d: the server's 422 is the invocation's to fix (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	doc := envelope(t, stdout)
	failure, _ := doc["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "good.json") {
		t.Fatalf("error.message = %q, want the refused file named", message)
	}
}

// TestWorkflowImportUsageErrors: the argument shapes the verb refuses before
// it reads anything.
func TestWorkflowImportUsageErrors(t *testing.T) {
	document := n8nDocument(t, `{}`)
	empty := t.TempDir()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no document at all",
			args: []string{"workflow", "import", "--yes"},
			want: "no document",
		},
		{
			name: "both a file and a directory",
			args: []string{"workflow", "import", "--file", document, "--dir", filepath.Dir(document), "--yes"},
			want: "mutually exclusive",
		},
		{
			name: "a directory with no workflow in it",
			args: []string{"workflow", "import", "--dir", empty, "--yes"},
			want: "no .json workflow",
		},
		{
			name: "a positional argument the verb does not take",
			args: []string{"workflow", "import", "wf_1", "--file", document, "--yes"},
			want: "workflow import takes no arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if message := usageMessage(t, test.args); !strings.Contains(message, test.want) {
				t.Fatalf("error.message = %q, want it to say %q", message, test.want)
			}
		})
	}
}

// TestWorkflowExportOutWritesTheBareN8NDocument: --out writes the file n8n's
// own import reads — no format, no lossy, no supportedMappings around it —
// and the lossy report travels in this CLI's envelope.
func TestWorkflowExportOutWritesTheBareN8NDocument(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/export": jsonBody(http.StatusOK, exportResponse),
	})
	srv := stubAPI(t, api.routesFor(t))

	out := filepath.Join(t.TempDir(), "orders.json")
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "export", "wf_1", "--out", out, "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/workflows/wf_1/export" {
		t.Fatalf("call = %+v, want GET the export operation", call)
	}

	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("could not read the export: %v", err)
	}
	if strings.TrimSpace(string(written)) != bareExportDocument {
		t.Fatalf("file = %s, want the bare n8n document and nothing else", written)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["path"] != out {
		t.Errorf("data.path = %v, want %q", data["path"], out)
	}
	lossy, _ := data["lossy"].([]any)
	if len(lossy) != 1 {
		t.Fatalf("data.lossy = %v, want the report the envelope promised", data["lossy"])
	}
}

// TestWorkflowExportOutDashStreamsTheDocumentAndPutsLossyOnStderr: the bytes
// being piped stay pure n8n, and the report a person needs goes where it
// cannot corrupt them.
func TestWorkflowExportOutDashStreamsTheDocumentAndPutsLossyOnStderr(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/workflows/wf_1/export": jsonBody(http.StatusOK, exportResponse),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "export", "wf_1", "--out", "-", "--url", srv.URL},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != bareExportDocument {
		t.Fatalf("stdout = %q, want the bare n8n document", stdout)
	}
	if !strings.Contains(stderr, "a note n8n has no field for") {
		t.Fatalf("stderr = %q, want the lossy report there", stderr)
	}
}

// TestWorkflowExportAllWritesOneBareDocumentPerWorkflow: n8n's --all form.
// Files are named by workflow id, the listing's pages are followed, and two
// workflows never share a file.
func TestWorkflowExportAllWritesOneBareDocumentPerWorkflow(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("cursor") == "" {
				w.Header().Set("X-Next-Cursor", "cur_2")
				_, _ = io.WriteString(w, "["+workflowSummaryRow("wf_1", "Orders", true)+"]")

				return
			}
			_, _ = io.WriteString(w, "["+workflowSummaryRow("wf_2", "Refunds", false)+"]")
		},
		apiPrefix + "/workflows/wf_1/export": jsonBody(http.StatusOK, exportResponse),
		apiPrefix + "/workflows/wf_2/export": jsonBody(http.StatusOK, exportResponse),
	})
	srv := stubAPI(t, api.routesFor(t))

	dir := t.TempDir()
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "export", "--all", "--out-dir", dir, "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	for _, id := range []string{"wf_1", "wf_2"} {
		written, err := os.ReadFile(filepath.Join(dir, id+".json"))
		if err != nil {
			t.Fatalf("could not read %s.json: %v", id, err)
		}
		if strings.TrimSpace(string(written)) != bareExportDocument {
			t.Errorf("%s.json = %s, want the bare n8n document", id, written)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(2) {
		t.Fatalf("data.count = %v, want 2", data["count"])
	}
}

// TestWorkflowExportUsageErrors: the --out/--all shapes the verb refuses
// before it reads anything.
func TestWorkflowExportUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "--all without a directory",
			args: []string{"workflow", "export", "--all"},
			want: "--out-dir",
		},
		{
			name: "--out-dir without --all",
			args: []string{"workflow", "export", "wf_1", "--out-dir", t.TempDir()},
			want: "--out-dir is for --all",
		},
		{
			name: "no workflow id at all",
			args: []string{"workflow", "export"},
			want: "no workflow id",
		},
		{
			name: "--all with a positional argument",
			args: []string{"workflow", "export", "wf_1", "--all", "--out-dir", t.TempDir()},
			want: "workflow export --all takes no arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if message := usageMessage(t, test.args); !strings.Contains(message, test.want) {
				t.Fatalf("error.message = %q, want it to say %q", message, test.want)
			}
		})
	}
}

// TestWorkflowListFiltersByNameAndActiveAcrossPages: the filters are the
// CLI's, not the server's — the requests carry no name or active parameter —
// and the walk follows the cursor until the filters are satisfied.
func TestWorkflowListFiltersByNameAndActiveAcrossPages(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/workflows": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("cursor") == "" {
				w.Header().Set("X-Next-Cursor", "cur_2")
				_, _ = io.WriteString(w, "["+
					workflowSummaryRow("wf_1", "Orders", true)+","+
					workflowSummaryRow("wf_2", "Refunds", false)+"]")

				return
			}
			_, _ = io.WriteString(w, "["+
				workflowSummaryRow("wf_3", "orders archive", false)+","+
				workflowSummaryRow("wf_4", "Billing", true)+"]")
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "list", "--name", "orders", "--active", "false", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if len(api.calls) != 2 {
		t.Fatalf("the stub saw %d requests, want both pages walked: %+v", len(api.calls), api.calls)
	}
	for _, call := range api.calls {
		if strings.Contains(call.Query, "name") || strings.Contains(call.Query, "active") {
			t.Errorf("query %q carried a filter parameter the server does not read", call.Query)
		}
	}
	if api.calls[1].Query != "cursor=cur_2" {
		t.Errorf("second page query = %q, want the cursor and nothing else", api.calls[1].Query)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("data.items = %v, want only the inactive orders archive", data["items"])
	}
	only, _ := items[0].(map[string]any)
	if only["id"] != "wf_3" {
		t.Errorf("data.items[0] = %v, want wf_3", only)
	}
}

// TestWorkflowListRejectsABadActiveFlag: --active is tri-state and only
// true/false are its spellings.
func TestWorkflowListRejectsABadActiveFlag(t *testing.T) {
	message := usageMessage(t, []string{"workflow", "list", "--active", "yes"})
	if !strings.Contains(message, "--active") {
		t.Fatalf("error.message = %q, want the flag named", message)
	}
}

// TestWorkflowActivateAllChangesEveryInactiveWorkflow: the bulk form is one
// POST per workflow whose state would change, and nothing for the rest.
func TestWorkflowActivateAllChangesEveryInactiveWorkflow(t *testing.T) {
	api := newRecordingAPI(tenantWideKey())
	api.routes[apiPrefix+"/workflows"] = jsonBody(http.StatusOK,
		"["+workflowSummaryRow("wf_1", "Orders", true)+","+workflowSummaryRow("wf_2", "Refunds", false)+"]")
	api.routes[apiPrefix+"/workflows/wf_2/activate"] = jsonBody(http.StatusOK, workflowBody)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "activate", "--all", "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	posts := 0
	for _, call := range api.calls {
		if call.Method == http.MethodPost {
			posts++
			if call.Path != apiPrefix+"/workflows/wf_2/activate" {
				t.Errorf("POST %s, want only the inactive workflow activated", call.Path)
			}
		}
	}
	if posts != 1 {
		t.Fatalf("the verb made %d POSTs, want one: %+v", posts, api.calls)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("data.count = %v, want 1", data["count"])
	}
	if changed, _ := data["changed"].([]any); len(changed) != 1 || changed[0] != "wf_2" {
		t.Errorf("data.changed = %v, want [wf_2]", data["changed"])
	}
}

// TestWorkflowDeactivateAllChangesEveryActiveWorkflow is the same rule from
// the other side.
func TestWorkflowDeactivateAllChangesEveryActiveWorkflow(t *testing.T) {
	api := newRecordingAPI(tenantWideKey())
	api.routes[apiPrefix+"/workflows"] = jsonBody(http.StatusOK,
		"["+workflowSummaryRow("wf_1", "Orders", true)+","+workflowSummaryRow("wf_2", "Refunds", false)+"]")
	api.routes[apiPrefix+"/workflows/wf_1/deactivate"] = jsonBody(http.StatusOK, workflowBody)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "deactivate", "--all", "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	for _, call := range api.calls {
		if call.Method == http.MethodPost && call.Path != apiPrefix+"/workflows/wf_1/deactivate" {
			t.Errorf("POST %s, want only the active workflow deactivated", call.Path)
		}
	}
	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if changed, _ := data["changed"].([]any); len(changed) != 1 || changed[0] != "wf_1" {
		t.Errorf("data.changed = %v, want [wf_1]", data["changed"])
	}
}

// TestWorkflowActivateAllStillNeedsTheConfirmation: the bulk form is the same
// guarded verb, so a missing --yes sends nothing at all.
func TestWorkflowActivateAllStillNeedsTheConfirmation(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		"/": jsonBody(http.StatusOK, `{"id":"never"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "activate", "--all", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("the refusal sent %+v, want nothing", api.calls)
	}
}
