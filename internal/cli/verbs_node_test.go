package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// nodeCatalogue holds two versions of one type, which is the case `node
// describe` has to resolve rather than assume away, and a type whose name is
// one edit away from a typo.
const nodeCatalogue = `[` +
	`{"type":"httpRequest","version":3,"displayName":"HTTP Request","category":"Core"},` +
	`{"type":"httpRequest","version":4.2,"displayName":"HTTP Request","category":"Core"},` +
	`{"type":"set","version":1,"displayName":"Edit Fields","category":"Core"}]`

func TestNodeListReadsTheCatalogue(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types": jsonBody(http.StatusOK, nodeCatalogue),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, handled, stdout, stderr := runCLI(t, Env{
		Args:   []string{"node", "list", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if !handled {
		t.Fatal("`node list` fell through to the server path")
	}
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodGet || call.Path != apiPrefix+"/node-types" {
		t.Fatalf("call = %+v, want GET %s", call, apiPrefix+"/node-types")
	}
	if call.Query != "" {
		t.Fatalf("node list sent query %q; list-node-types takes no parameters", call.Query)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["count"] != float64(3) {
		t.Fatalf("data.count = %v, want 3", data["count"])
	}
	items, _ := data["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("data.items = %v, want the three definitions unchanged", data["items"])
	}
	first, _ := items[0].(map[string]any)
	if first["type"] != "httpRequest" {
		t.Fatalf("data.items[0] = %v, want the catalogue's first definition", first)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-node-types" {
		t.Fatalf("meta.operation = %v, want list-node-types", meta["operation"])
	}

	// --quiet prints the identifier an agent feeds back into `node describe`,
	// which is the type and not an id the catalogue does not have.
	quietAPI := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types": jsonBody(http.StatusOK, nodeCatalogue),
	})
	quietSrv := stubAPI(t, quietAPI.routesFor(t))

	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"node", "list", "--url", quietSrv.URL, "--quiet"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("--quiet exit = %d, want %d (stderr=%q)", code, ExitOK, stderr)
	}
	if stdout != "httpRequest\nhttpRequest\nset\n" {
		t.Fatalf("--quiet printed %q, want one type per line", stdout)
	}
}

func TestNodeDescribePrintsOneDefinitionFromTheCatalogue(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types": jsonBody(http.StatusOK, nodeCatalogue),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"node", "describe", "httpRequest", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	if call := api.last(t); call.Method != http.MethodGet || call.Path != apiPrefix+"/node-types" {
		t.Fatalf("call = %+v, want one read of the catalogue", call)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["displayName"] != "HTTP Request" {
		t.Fatalf("data = %v, want the node definition", data)
	}
	// Two versions of the type are registered and describe was given no
	// version, so it reports the one the server would resolve to: the highest.
	if data["version"] != 4.2 {
		t.Fatalf("data.version = %v, want the highest registered version", data["version"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "list-node-types" {
		t.Fatalf("meta.operation = %v, want list-node-types", meta["operation"])
	}
}

func TestNodeDescribeRefusesAnUnknownTypeWithCloseMatches(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types": jsonBody(http.StatusOK, nodeCatalogue),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"node", "describe", "httpRequst", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitNotFound, stdout, stderr)
	}

	doc := envelope(t, stdout)
	if envelopeFailure(doc) != "not_found" {
		t.Fatalf("error.code = %q, want not_found", envelopeFailure(doc))
	}
	message, _ := doc["error"].(map[string]any)["message"].(string)
	if !strings.Contains(message, "httpRequst") {
		t.Errorf("error.message %q does not name the type that was asked for", message)
	}
	if !strings.Contains(message, "httpRequest") {
		t.Errorf("error.message %q lists no close match", message)
	}
}

func TestNodeOptionsPostsThePropertyItWasGiven(t *testing.T) {
	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types/httpRequest/load-options": jsonBody(http.StatusOK,
			`{"options":[{"label":"General","value":"general"}],"reason":"one of many"}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	code, _, stdout, stderr := runCLI(t, Env{
		Args: []string{"node", "options", "httpRequest", "--property", "channel",
			"--version", "4.2", "--mode", "list", "--credential", "cred_1",
			"--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	call := api.last(t)
	if call.Method != http.MethodPost || call.Path != apiPrefix+"/node-types/httpRequest/load-options" {
		t.Fatalf("call = %+v, want POST %s", call, apiPrefix+"/node-types/httpRequest/load-options")
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
		t.Fatalf("the request body is not JSON: %v (%q)", err, call.Body)
	}
	want := map[string]any{"property": "channel", "version": "4.2", "mode": "list", "credentialId": "cred_1"}
	for field, value := range want {
		if body[field] != value {
			t.Errorf("body[%q] = %v, want %v (body=%s)", field, body[field], value, call.Body)
		}
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["reason"] != "one of many" {
		t.Fatalf("data = %v, want the resolver's answer", data)
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "load-node-property-options" {
		t.Fatalf("meta.operation = %v, want load-node-property-options", meta["operation"])
	}

	// The property is the one thing the operation cannot be called without, so
	// omitting it is a usage error before any request is made.
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"node", "options", "httpRequest", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit without --property = %d, want %d (stderr=%q)", code, ExitUsage, stderr)
	}
	if failure := envelopeFailure(envelope(t, stdout)); failure != "usage" {
		t.Fatalf("error.code = %q, want usage", failure)
	}
	if len(api.calls) != 1 {
		t.Fatalf("the stub saw %d requests, want 1: a missing --property must not reach the server", len(api.calls))
	}
}
