package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestVersionReportsTheBinaryVersionAndTheServerItReached(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, `{"status":"ok","version":"9.9.9"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:    []string{"version", "--json", "--url", srv.URL},
		TTY:     true,
		Version: "1.2.3",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["version"] != "1.2.3" {
		t.Fatalf("data.version = %v, want the binary version", data["version"])
	}
	if data["apiVersion"] != "v1" {
		t.Fatalf("data.apiVersion = %v, want v1", data["apiVersion"])
	}
	server, _ := data["server"].(map[string]any)
	if server["reachable"] != true || server["version"] != "9.9.9" {
		t.Fatalf("data.server = %v, want the reachable server's version", data["server"])
	}
}

func TestVersionStaysSuccessfulWhenTheServerIsUnreachable(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{})
	url := srv.URL
	srv.Close()

	code, _, stdout, _ := runCLI(t, Env{
		Args:    []string{"version", "--json", "--url", url, "--timeout", "2s"},
		TTY:     true,
		Version: "1.2.3",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d: an unreachable server is not a failure of `version`", code, ExitOK)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["version"] != "1.2.3" {
		t.Fatalf("data.version = %v, want the binary version", data["version"])
	}
	server, _ := data["server"].(map[string]any)
	if server["reachable"] != false {
		t.Fatalf("data.server = %v, want reachable false", data["server"])
	}
	if message, _ := server["error"].(string); message == "" {
		t.Fatalf("data.server has no error message: %v", data["server"])
	}
}

func TestVersionSkipsTheServerProbeWhenNoURLResolves(t *testing.T) {
	code, _, stdout, _ := runCLI(t, Env{
		Args:    []string{"version", "--json", "--url", ""},
		TTY:     true,
		Version: "1.2.3",
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if _, present := data["server"]; present {
		t.Fatalf("data = %v, want no server section when no URL resolves", data)
	}
}

func TestHealthPrintsTheBodyVerbatim(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, `{"status":"ok","version":"9.9.9"}`),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"health", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["status"] != "ok" || data["version"] != "9.9.9" {
		t.Fatalf("data = %v, want the body verbatim", doc["data"])
	}

	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "get-health" {
		t.Fatalf("meta.operation = %v, want get-health", meta["operation"])
	}
}

func TestReadyMaps503ToNotReady(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/ready": problemBody(http.StatusServiceUnavailable, `{"title":"Service Unavailable","status":503,"detail":"database unreachable"}`),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"ready", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitNotReady {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitNotReady, stdout)
	}

	doc := envelope(t, stdout)
	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "not_ready" {
		t.Fatalf("error.code = %v, want not_ready", errDoc["code"])
	}
}

func TestReadyReportsAReadyInstance(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/ready": jsonBody(http.StatusOK, `{"status":"ok","database":"ok"}`),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"ready", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}

	doc := envelope(t, stdout)
	meta, _ := doc["meta"].(map[string]any)
	if meta["operation"] != "get-ready" {
		t.Fatalf("meta.operation = %v, want get-ready", meta["operation"])
	}
}

func TestHelpIsGeneratedFromTheRegistry(t *testing.T) {
	code, handled, stdout, stderr := runCLI(t, Env{Args: []string{"help"}, TTY: true})
	if !handled || code != ExitOK {
		t.Fatalf("handled = %v, exit = %d (stderr=%q)", handled, code, stderr)
	}

	for _, verb := range registry() {
		if !strings.Contains(stdout, verb.Path) {
			t.Fatalf("help output is missing %q: %q", verb.Path, stdout)
		}
		if !strings.Contains(stdout, verb.Summary) {
			t.Fatalf("help output is missing the summary of %q: %q", verb.Path, stdout)
		}
	}
}

func TestHelpCarriesTheRegistryInJSONMode(t *testing.T) {
	_, _, stdout, _ := runCLI(t, Env{Args: []string{"help", "--json"}, TTY: false})

	doc := envelope(t, stdout)
	rows, ok := doc["data"].([]any)
	if !ok {
		t.Fatalf("data = %v, want the verb list", doc["data"])
	}
	if len(rows) != len(registry()) {
		t.Fatalf("data has %d verbs, want %d", len(rows), len(registry()))
	}

	// The listing must be usable by an agent: path, summary and operation id.
	first, _ := rows[0].(map[string]any)
	if first["path"] == nil || first["summary"] == nil || first["operation"] == nil {
		body, _ := json.Marshal(first)
		t.Fatalf("a verb row is missing fields: %s", body)
	}
}
