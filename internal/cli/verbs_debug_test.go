package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// evalAnswer is one eval-expression answer as the API returns it.
const evalAnswer = `{"value":3,"type":"number"}`

func TestDebugEvalSendsTheExpressionAndTheNode(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/eval": jsonBody(http.StatusOK, evalAnswer),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"debug", "eval", "$json.n + 1", "--execution", "exec_1",
			"--node", "HTTP Request", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/executions/exec_1/eval" {
		t.Fatalf("call = %+v, want POST %s/executions/exec_1/eval", call, apiPrefix)
	}

	var sent struct {
		Expression string `json:"expression"`
		NodeID     string `json:"nodeId"`
	}
	if err := json.Unmarshal([]byte(call.Body), &sent); err != nil {
		t.Fatalf("the body is not the eval request: %v (%q)", err, call.Body)
	}
	if sent.Expression != "$json.n + 1" {
		t.Fatalf("expression = %q, want the expression unchanged", sent.Expression)
	}
	if sent.NodeID != "HTTP Request" {
		t.Fatalf("nodeId = %q, want the node the expression narrows to", sent.NodeID)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["value"] != float64(3) || data["type"] != "number" {
		t.Fatalf("data = %v, want the value and its type", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "eval-expression" {
		t.Fatalf("meta.operation = %v, want eval-expression", meta["operation"])
	}
}

func TestDebugEvalQuietPrintsTheValue(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/eval": jsonBody(http.StatusOK, evalAnswer),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--execution", "exec_1", "--url", srv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if stdout != "3\n" {
		t.Fatalf("stdout = %q, want the value alone", stdout)
	}
}

// TestDebugEvalWithoutANodeSendsOnlyTheExpression pins the optional half: the
// node id is a narrowing within the execution, and omitting it must not send an
// empty one.
func TestDebugEvalWithoutANodeSendsOnlyTheExpression(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/eval": jsonBody(http.StatusOK,
			`{"value":"orders","type":"string"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--execution", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(api.last(t).Body), &sent); err != nil {
		t.Fatalf("the body is not the eval request: %v", err)
	}
	if _, present := sent["nodeId"]; present {
		t.Fatalf("body = %v, want no nodeId when --node was not given", sent)
	}
	if sent["expression"] != "$json.n" {
		t.Fatalf("body = %v, want the expression", sent)
	}
}

// TestDebugEvalWithoutAnExecutionIsAUsageError pins the decision recorded on
// FEAT-ew46cb: the route makes the execution the context source, so "the newest
// execution" is never guessed — the caller names the run it means.
func TestDebugEvalWithoutAnExecutionIsAUsageError(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing execution sent %d requests, want none", len(api.calls))
	}

	failure, _ := envelope(t, stdout)["error"].(map[string]any)
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "--execution") || !strings.Contains(message, "exec list") {
		t.Fatalf("message = %q, want it to name the flag and how to find an id", message)
	}
}

// TestDebugEvalOnAMissingExecutionExitsNotFound pins the read half of the
// refusal vocabulary: another tenant's execution is a 404 for this caller, the
// same as any other execution read.
func TestDebugEvalOnAMissingExecutionExitsNotFound(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_missing/eval": problemBody(http.StatusNotFound,
			`{"title":"Not Found","status":404,"detail":"execution not found"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--execution", "exec_missing", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotFound, stdout, stderr)
	}

	failure, _ := envelope(t, stdout)["error"].(map[string]any)
	if failure["code"] != "not_found" || failure["status"] != float64(404) {
		t.Fatalf("error = %v, want not_found carrying the status", failure)
	}
}

// TestDebugEvalCarriesAScopeRefusal pins the authority half: the verb's own
// authority is the scope the server asks for, so a refusal is the server's to
// make and it arrives as exit 3 with the server's error code.
func TestDebugEvalCarriesAScopeRefusal(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/eval": problemBody(http.StatusForbidden,
			`{"title":"Forbidden","status":403,"detail":"the key is not scoped for this operation"}`),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--execution", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitRefused {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitRefused, stdout, stderr)
	}

	failure, _ := envelope(t, stdout)["error"].(map[string]any)
	if failure["code"] != scopeDeniedCode {
		t.Fatalf("error.code = %v, want %q", failure["code"], scopeDeniedCode)
	}
}

func TestDebugEvalNeedsAnExpression(t *testing.T) {
	api := newRecordingAPI(nil)
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "--execution", "exec_1", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}
	if len(api.calls) != 0 {
		t.Fatalf("a missing expression sent %d requests, want none", len(api.calls))
	}
}

func TestDebugEvalHumanModePrintsTheTypeAndValue(t *testing.T) {
	srv := stubAPI(t, map[string]http.HandlerFunc{
		apiPrefix + "/executions/exec_1/eval": jsonBody(http.StatusOK, evalAnswer),
	})

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"debug", "eval", "$json.n", "--execution", "exec_1", "--url", srv.URL},
		TTY:    true,
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	if !strings.Contains(stdout, "type") || !strings.Contains(stdout, "number") || !strings.Contains(stdout, "3") {
		t.Fatalf("stdout = %q, want the type and the value", stdout)
	}
}
