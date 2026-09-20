package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCredentialListReadsAPage(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Cursor", "cur_2")
			_, _ = io.WriteString(w, `[{"id":"cred_1","name":"SMTP","type":"smtp","fields":{},"updatedAt":"2026-09-20T10:00:00Z"}]`)
		},
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "list", "--url", srv.URL, "--limit", "5", "--cursor", "cur_1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/credentials" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/credentials")
	}
	for _, want := range []string{"limit=5", "cursor=cur_1"} {
		if !strings.Contains(call.Query, want) {
			t.Errorf("query %q is missing %s", call.Query, want)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(1) || data["nextCursor"] != "cur_2" {
		t.Fatalf("data = %v, want the page and the cursor the API sent", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-credentials" {
		t.Fatalf("meta.operation = %v, want list-credentials", meta["operation"])
	}
}

func TestCredentialGetReadsOneWithoutItsSecret(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials/cred_1": jsonBody(http.StatusOK,
			`{"id":"cred_1","name":"SMTP","type":"smtp","fields":{"password":"[redacted]"},"updatedAt":"2026-09-20T10:00:00Z"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "get", "cred_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/credentials/cred_1" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/credentials/cred_1")
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["name"] != "SMTP" {
		t.Fatalf("data = %v, want the credential", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "get-credential" {
		t.Fatalf("meta.operation = %v, want get-credential", meta["operation"])
	}
}

func TestCredentialTestPostsTheProbeAndReportsTheVerdict(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/credentials/cred_1/test": jsonBody(http.StatusOK,
			`{"ok":true,"resolvedFromStorage":["password"]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"credential", "test", "cred_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/credentials/cred_1/test" {
		t.Fatalf("call = %+v, want POST %s", call, apiPrefix+"/credentials/cred_1/test")
	}
	if call.Body != "" {
		t.Fatalf("body = %q, want no body: the probe reads the stored credential", call.Body)
	}

	doc := envelope(t, stdout)
	if data, _ := doc["data"].(map[string]any); data["ok"] != true {
		t.Fatalf("data = %v, want the verdict", doc["data"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "test-credential" {
		t.Fatalf("meta.operation = %v, want test-credential", meta["operation"])
	}

	// The verdict is the identifier a pipeline branches on, so --quiet prints
	// the API's own boolean rather than the credential id.
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"credential", "test", "cred_1", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("--quiet exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "true\n" {
		t.Fatalf("--quiet printed %q, want the verdict", stdout)
	}
}
