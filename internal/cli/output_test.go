package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const healthBody = `{"status":"ok","version":"9.9.9"}`

func TestEnvelope(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, healthBody),
	})

	code, handled, stdout, stderr := runCLI(t, Env{
		Args: []string{"health", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if !handled || code != ExitOK {
		t.Fatalf("handled = %v, exit = %d (stderr=%q)", handled, code, stderr)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("ok = %v, want true", doc["ok"])
	}
	if _, present := doc["error"]; present {
		t.Fatalf("a successful envelope carried an error: %q", stdout)
	}
	data, _ := doc["data"].(map[string]any)
	if data["status"] != "ok" || data["version"] != "9.9.9" {
		t.Fatalf("data = %v, want the response body verbatim", doc["data"])
	}

	meta, ok := doc["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is missing: %q", stdout)
	}
	if meta["operation"] != "get-health" {
		t.Fatalf("meta.operation = %v, want get-health", meta["operation"])
	}
	if duration, ok := meta["durationMs"].(float64); !ok || duration < 0 {
		t.Fatalf("meta.durationMs = %v, want a non-negative number", meta["durationMs"])
	}
}

func TestJSONIsForcedWhenStdoutIsNotATerminal(t *testing.T) {
	_, _, stdout, _ := runCLI(t, Env{Args: []string{"version"}, TTY: false})

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("ok = %v, want true", doc["ok"])
	}
	data, _ := doc["data"].(map[string]any)
	if data["version"] != "0.0.0-test" {
		t.Fatalf("data = %v, want the binary version", doc["data"])
	}
}

func TestQuietPrintsOnlyTheIdentifier(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, healthBody),
	})

	// --quiet is for shell pipelines, so it prints the identifier even when
	// stdout is not a terminal and JSON mode would otherwise apply.
	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"health", "--quiet", "--url", srv.URL},
		TTY:  false,
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if stdout != "ok\n" {
		t.Fatalf("stdout = %q, want just the identifier", stdout)
	}
}

func TestQuietDoesNotTurnOffExplicitJSON(t *testing.T) {
	_, _, stdout, _ := runCLI(t, Env{Args: []string{"version", "--quiet", "--json"}, TTY: true})

	doc := envelope(t, stdout)
	if doc["ok"] != true {
		t.Fatalf("ok = %v, want true", doc["ok"])
	}
}

func TestJSONModeKeepsStdoutToTheEnvelopeAlone(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": jsonBody(http.StatusOK, healthBody),
	})

	_, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"health", "--json", "--verbose", "--url", srv.URL},
		TTY:  true,
	})

	// Stdout parses as exactly one document; the request trace went to stderr.
	envelope(t, stdout)
	if !strings.Contains(stderr, "> GET ") {
		t.Fatalf("--verbose did not trace the request to stderr: %q", stderr)
	}
	if strings.Contains(stdout, "> GET ") {
		t.Fatalf("--verbose leaked into stdout: %q", stdout)
	}
}

func TestErrorEnvelopeCarriesTheProblemDocumentVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"detail":"this key may not activate workflows"}`

	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": problemBody(http.StatusForbidden, problem),
	})

	code, _, stdout, _ := runCLI(t, Env{
		Args: []string{"health", "--json", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout=%q)", code, ExitRefused, stdout)
	}

	doc := envelope(t, stdout)
	if doc["ok"] != false {
		t.Fatalf("ok = %v, want false", doc["ok"])
	}
	if _, present := doc["data"]; present {
		t.Fatalf("a failed envelope carried data: %q", stdout)
	}

	errDoc, _ := doc["error"].(map[string]any)
	if errDoc["code"] != "scope_denied" {
		t.Fatalf("error.code = %v, want scope_denied", errDoc["code"])
	}
	if errDoc["status"] != float64(http.StatusForbidden) {
		t.Fatalf("error.status = %v, want 403", errDoc["status"])
	}
	if message, _ := errDoc["message"].(string); message == "" {
		t.Fatalf("error.message is empty: %q", stdout)
	}

	detail, _ := errDoc["detail"].(map[string]any)
	got, err := json.Marshal(detail["problem"])
	if err != nil {
		t.Fatalf("error.detail.problem is not JSON: %v", err)
	}

	var want, found any
	if err := json.Unmarshal([]byte(problem), &want); err != nil {
		t.Fatalf("test fixture is not JSON: %v", err)
	}
	if err := json.Unmarshal(got, &found); err != nil {
		t.Fatalf("problem is not JSON: %v", err)
	}
	if !jsonEqual(t, want, found) {
		t.Fatalf("error.detail.problem = %s, want the problem document verbatim", got)
	}
}

func TestFailureGoesToStderrInHumanMode(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": problemBody(http.StatusForbidden, `{"title":"Forbidden","status":403,"detail":"nope"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"health", "--url", srv.URL},
		TTY:  true,
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d", code, ExitRefused)
	}
	if stdout != "" {
		t.Fatalf("human-mode failure wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "nope") {
		t.Fatalf("stderr does not carry the reason: %q", stderr)
	}
}

func TestQuietFailureKeepsStdoutEmpty(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/health": problemBody(http.StatusNotFound, `{"title":"Not Found","status":404}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"health", "--quiet", "--url", srv.URL},
		TTY:  false,
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d", code, ExitNotFound)
	}
	if stdout != "" {
		t.Fatalf("--quiet wrote to stdout on failure: %q", stdout)
	}
	if stderr == "" {
		t.Fatal("--quiet swallowed the failure message")
	}
}

func TestHumanModePrintsKeyValueText(t *testing.T) {
	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"version"}, TTY: true})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if !strings.Contains(stdout, "0.0.0-test") {
		t.Fatalf("human output does not carry the version: %q", stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("human mode printed JSON on a terminal: %q", stdout)
	}
}

func TestPrintKVAlignsLabelsAndPrintTableUsesTheHeader(t *testing.T) {
	var buf bytes.Buffer
	printKV(&buf, [][2]string{{"version", "1.2.3"}, {"apiVersion", "v1"}})
	if got := buf.String(); !strings.Contains(got, "version") || !strings.Contains(got, "1.2.3") {
		t.Fatalf("printKV output = %q", got)
	}

	buf.Reset()
	printTable(&buf, []string{"ID", "STATUS"}, [][]string{{"exec_1", "completed"}, {"exec_2", "failed"}})
	for _, want := range []string{"ID", "STATUS", "exec_1", "completed", "exec_2", "failed"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("printTable output = %q, missing %q", buf.String(), want)
		}
	}
}

// jsonEqual compares two decoded JSON values structurally.
func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()

	left, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return bytes.Equal(left, right)
}
