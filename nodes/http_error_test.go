package nodes_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// upstreamFailure is the body the 2026-09-23 audit's stub answered its 500
// with.
const upstreamFailure = `{"error":"simulated","code":500,"detail":"upstream exploded"}`

// failingUpstream answers every request with the given status and body, as
// JSON, and records the raw query it was sent.
func failingUpstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// The audit's reproduction: a 500 failed the run with `request failed with
// status 500` and nothing else. The body was read and dropped, and neither the
// method nor the URL was named, so the user could not tell which call failed or
// why. The URL keeps its scheme and host only, as a transport error's does,
// because the path and the query are where a secret sits.
func TestHTTPRequestStatusErrorNamesTheStatusMethodURLAndBody(t *testing.T) {
	t.Parallel()

	server := failingUpstream(t, http.StatusInternalServerError, upstreamFailure)
	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "POST", "url": server.URL + "/status/500?token=PATH-SECRET-91",
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("a 500 succeeded, want the node to fail")
	}

	text := err.Error()
	for _, want := range []string{`node "Call API"`, "500", "POST", server.URL, `"detail":"upstream exploded"`} {
		if !strings.Contains(text, want) {
			t.Errorf("error = %q, want it to name %q", text, want)
		}
	}
	for _, withheld := range []string{"PATH-SECRET-91", "/status/500"} {
		if strings.Contains(text, withheld) {
			t.Errorf("error = %q, want %q withheld", text, withheld)
		}
	}

	var status *nodes.HTTPStatusError
	if !errors.As(err, &status) {
		t.Fatalf("error = %#v, want an *nodes.HTTPStatusError", err)
	}
	if status.StatusCode != http.StatusInternalServerError || status.Method != "POST" || status.URL != server.URL {
		t.Errorf("status error = %+v, want 500 POST %s", status, server.URL)
	}
	if status.Body != upstreamFailure {
		t.Errorf("status error body = %q, want %q", status.Body, upstreamFailure)
	}
}

// A failed response's body travels into the run's error, its trace row and
// every error item, so it is cut to a bound rather than carried whole: an HTML
// error page or a dump is not a message.
func TestHTTPRequestStatusErrorTruncatesALongBody(t *testing.T) {
	t.Parallel()

	server := failingUpstream(t, http.StatusNotFound, strings.Repeat("x", 64*1024))
	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL,
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("a 404 succeeded, want the node to fail")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want the status", err)
	}
	if len(err.Error()) > 4096 {
		t.Errorf("error is %d bytes long, want the body truncated", len(err.Error()))
	}
	var status *nodes.HTTPStatusError
	if !errors.As(err, &status) || !status.Truncated {
		t.Errorf("status error = %+v, want it to say the body was truncated", status)
	}
}

// runFailingHTTP runs Manual → Call API (under onError, against url) and
// answers Call API's trace row. Under continueErrorOutput a No Op hangs off
// the error output, so the port is real.
func runFailingHTTP(t *testing.T, url, onError string, credentials engine.CredentialResolver, nodeCredentials map[string]string) engine.NodeRun {
	t.Helper()
	catalog := node.NewRegistry()
	if err := nodes.RegisterAll(catalog); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	link := func(id, source, port, target string) workflow.Connection {
		return workflow.Connection{ID: id, Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: source, Port: port}, Target: workflow.Endpoint{NodeID: target, Port: "main"}}
	}
	document := workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_http_error", Name: "HTTP error items", Settings: map[string]any{},
		Nodes: []workflow.Node{
			{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
			{ID: "call", Name: "Call API", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"method": "GET", "url": url}, Settings: map[string]any{"onError": onError},
				Credentials: nodeCredentials},
			{ID: "ok", Name: "OK", Type: nodes.NoOpNodeType, TypeVersion: workflow.V(1)},
		},
		Connections: []workflow.Connection{link("c1", "manual", "main", "call"), link("c2", "call", "main", "ok")},
	}
	if onError == "continueErrorOutput" {
		document.Nodes = append(document.Nodes, workflow.Node{ID: "failed", Name: "Failed", Type: nodes.NoOpNodeType, TypeVersion: workflow.V(1)})
		document.Connections = append(document.Connections, link("c3", "call", "error", "failed"))
	}
	ir, err := workflow.Compile(document, catalog)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	result, err := engine.NewRunner(executors).Run(context.Background(), ir, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"u": "status/500"}}, Credentials: credentials,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, run := range result.NodeRuns {
		if run.NodeID == "call" {
			return run
		}
	}
	t.Fatal("Call API has no trace row")
	return engine.NodeRun{}
}

// An item a tolerated HTTP failure emits carries the status as n8n's
// NodeApiError does — `httpCode`, as text, and a `description` of the status
// and the body — so a flow can branch on a 404 or a 429, plus the method, the
// redacted URL and the body on their own. The audit found only the message and
// the node's name.
func TestHTTPRequestErrorItemsCarryTheStatusAndTheBody(t *testing.T) {
	t.Parallel()

	server := failingUpstream(t, http.StatusTooManyRequests, upstreamFailure)
	for _, onError := range []string{"continueRegularOutput", "continueErrorOutput"} {
		run := runFailingHTTP(t, server.URL+"/status/429", onError, nil, nil)
		port := map[string]int{"continueRegularOutput": 0, "continueErrorOutput": 1}[onError]
		if len(run.Output) <= port || len(run.Output[port]) != 1 {
			t.Fatalf("%s: output %#v, want one error item on port %d", onError, run.Output, port)
		}
		failed := run.Output[port][0].JSON
		descriptor, ok := failed[engine.ErrorItemKey].(map[string]any)
		if !ok {
			t.Fatalf("%s: the error item = %#v, want an %s object", onError, failed, engine.ErrorItemKey)
		}
		want := map[string]any{
			"httpCode":    "429",
			"description": "429 - " + upstreamFailure,
			"method":      "GET",
			"url":         server.URL,
			"body":        upstreamFailure,
			"node":        "Call API",
		}
		for key, value := range want {
			if descriptor[key] != value {
				t.Errorf("%s: error.%s = %#v, want %#v", onError, key, descriptor[key], value)
			}
		}
		if message, _ := descriptor["message"].(string); !strings.Contains(message, "429") || !strings.Contains(message, "upstream exploded") {
			t.Errorf("%s: error.message = %q, want the status and the body", onError, message)
		}
		if onError == "continueErrorOutput" && failed["u"] != "status/500" {
			t.Errorf("%s: the error item = %#v, want the input's fields beside the error", onError, failed)
		}
		if run.Error == nil || !strings.Contains(run.Error.Error(), "upstream exploded") {
			t.Errorf("%s: the trace row's error = %v, want the body", onError, run.Error)
		}
	}
}

// An upstream that echoes the credential it was sent — an "invalid key
// <key>" answer — would put the secret into the error now that the body is
// carried. The runner's scrub withholds it from the error item's fields as it
// does from the message.
func TestHTTPRequestErrorItemFieldsAreScrubbedOfTheCredential(t *testing.T) {
	t.Parallel()

	const secret = "QUERY-SECRET-77ad"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key ` + r.URL.Query().Get("api_key") + `"}`))
	}))
	t.Cleanup(server.Close)

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-1", Name: "Query key", Type: "httpQueryAuth",
		Fields: map[string]string{"name": "api_key", "value": secret},
	}}
	run := runFailingHTTP(t, server.URL, "continueErrorOutput", resolver, map[string]string{"httpQueryAuth": "cred-1"})
	if len(run.Output) < 2 || len(run.Output[1]) != 1 {
		t.Fatalf("output %#v, want one error item on the error output", run.Output)
	}
	descriptor, _ := run.Output[1][0].JSON[engine.ErrorItemKey].(map[string]any)
	if descriptor["httpCode"] != "401" {
		t.Fatalf("error = %#v, want httpCode 401", descriptor)
	}
	// The upstream did echo the key, so the body is there with it withheld.
	if body, _ := descriptor["body"].(string); !strings.Contains(body, "invalid key") || strings.Contains(body, `invalid key "}`) {
		t.Fatalf("error.body = %q, want the echoed key in it, withheld", body)
	}
	for key, value := range descriptor {
		if text, ok := value.(string); ok && strings.Contains(text, secret) {
			t.Errorf("error.%s = %q, want the credential withheld", key, text)
		}
	}
	if strings.Contains(run.Error.Error(), secret) {
		t.Errorf("the trace row's error = %q, want the credential withheld", run.Error)
	}
}
