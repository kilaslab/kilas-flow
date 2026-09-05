package nodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// stubCredentials stands in for the tenant-scoped resolver the runtime injects.
type stubCredentials struct {
	credential engine.Credential
	err        error
	calls      []string
}

func (stub *stubCredentials) ResolveCredential(_ context.Context, credentialID string) (engine.Credential, error) {
	stub.calls = append(stub.calls, credentialID)
	return stub.credential, stub.err
}

// localPolicy allows the loopback test server while keeping every other guard.
func localPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

func httpNode(parameters map[string]any) workflow.IRNode {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		panic(err)
	}
	definition, _ := registry.Lookup("kilasflow.httpRequest", workflow.V(1))
	return workflow.IRNode{
		ID: "http-1", Name: "Call API", Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
}

func TestHTTPRequestResolvesExpressionsPerItem(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"path":"` + r.URL.Path + `"}`))
	}))
	defer server.Close()

	executor := nodes.NewHTTPExecutor(localPolicy())
	output, err := executor.Execute(context.Background(), httpNode(map[string]any{
		"method":    "GET",
		"url":       map[string]any{"mode": "expression", "value": server.URL + "/users/{{ $json.id }}"},
		"sendQuery": true,
		"queryParameters": map[string]any{
			"region": map[string]any{"mode": "expression", "value": "{{ $env.REGION }}"},
		},
	}), workflow.NodeInput{"main": {
		{JSON: map[string]any{"id": "ada"}},
		{JSON: map[string]any{"id": "grace"}},
	}}, engine.Request{Env: map[string]string{"REGION": "eu-west-1"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(output) != 1 || len(output[0]) != 2 {
		t.Fatalf("output = %#v, want one item per input item", output)
	}
	if len(paths) != 2 || paths[0] != "/users/ada?region=eu-west-1" || paths[1] != "/users/grace?region=eu-west-1" {
		t.Fatalf("requested paths = %v, want one per item with the resolved query", paths)
	}
	body, ok := output[0][0].JSON["body"].(map[string]any)
	if !ok || body["ok"] != true {
		t.Fatalf("first item body = %#v, want the decoded JSON response", output[0][0].JSON["body"])
	}
	if output[0][0].JSON["statusCode"] != float64(http.StatusOK) {
		t.Errorf("statusCode = %#v, want 200", output[0][0].JSON["statusCode"])
	}
}

func TestHTTPRequestAppliesACredentialAndHonoursItsDomainScope(t *testing.T) {
	t.Parallel()

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-1", Name: "Partner", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "secret-token"},
	}}
	ir := httpNode(map[string]any{"method": "GET", "url": server.URL})
	ir.Credentials = map[string]string{"httpBearerAuth": "cred-1"}

	executor := nodes.NewHTTPExecutor(localPolicy())
	if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if authorization != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, want the credential applied", authorization)
	}

	// The same credential scoped elsewhere must not be sent to this host.
	resolver.credential.AllowedDomains = []string{"api.partner.test"}
	_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Fatalf("Execute() with an out-of-scope credential error = %v, want a scope rejection", err)
	}
}

func TestHTTPRequestRejectsACredentialOfTheWrongType(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()

	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-1", Name: "Basic", Type: "httpBasicAuth",
		Fields: map[string]string{"user": "ada", "password": "x"},
	}}
	ir := httpNode(map[string]any{"method": "GET", "url": server.URL})
	ir.Credentials = map[string]string{"httpBearerAuth": "cred-1"}

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil || !strings.Contains(err.Error(), "not httpBearerAuth") {
		t.Fatalf("Execute() error = %v, want a credential type rejection", err)
	}
}

func TestHTTPRequestRefusesInternalTargetsUnderTheDefaultPolicy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()

	executor := nodes.NewHTTPExecutor(safehttp.DefaultPolicy())

	// A loopback server is only reachable when an operator has opted in.
	if _, err := executor.Execute(context.Background(), httpNode(map[string]any{"method": "GET", "url": server.URL}),
		workflow.NodeInput{}, engine.Request{}); err == nil {
		t.Error("the node reached a loopback server under the default policy")
	}

	// The cloud metadata service is the highest-value SSRF target.
	if _, err := executor.Execute(context.Background(), httpNode(map[string]any{"method": "GET", "url": "http://169.254.169.254/latest/meta-data/"}),
		workflow.NodeInput{}, engine.Request{}); err == nil {
		t.Error("the node reached the link-local metadata address")
	}

	// Non-HTTP schemes never leave the policy check.
	if _, err := executor.Execute(context.Background(), httpNode(map[string]any{"method": "GET", "url": "file:///etc/passwd"}),
		workflow.NodeInput{}, engine.Request{}); err == nil {
		t.Error("the node accepted a file:// URL")
	}
}

func TestHTTPRequestFailsOnErrorStatusUnlessTold(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	executor := nodes.NewHTTPExecutor(localPolicy())
	if _, err := executor.Execute(context.Background(), httpNode(map[string]any{"method": "GET", "url": server.URL}),
		workflow.NodeInput{}, engine.Request{}); err == nil {
		t.Error("a 500 response did not fail the node")
	}

	output, err := executor.Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL, "neverError": true,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() with neverError = %v", err)
	}
	if output[0][0].JSON["statusCode"] != float64(http.StatusInternalServerError) {
		t.Errorf("statusCode = %#v, want 500 reported as data", output[0][0].JSON["statusCode"])
	}
}

func TestHTTPRequestSendsAJSONBody(t *testing.T) {
	t.Parallel()

	var received string
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		received = string(body)
		contentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "POST", "url": server.URL, "sendBody": true, "bodyType": "json",
		"body": map[string]any{"mode": "expression", "value": `{"name":"{{ $json.name }}"}`},
	}), workflow.NodeInput{"main": {{JSON: map[string]any{"name": "Ada"}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if received != `{"name":"Ada"}` {
		t.Errorf("request body = %q, want the resolved JSON", received)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

func TestHTTPRequestRejectsAnInvalidJSONBodyBeforeSending(t *testing.T) {
	t.Parallel()

	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "POST", "url": server.URL, "sendBody": true, "bodyType": "json", "body": "not json",
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("Execute() error = %v, want a body validation failure", err)
	}
	if reached {
		t.Error("an invalid body was still sent upstream")
	}
}

func TestHTTPRequestCompilerValidationRejectsBadFixedConfiguration(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup("kilasflow.httpRequest", workflow.V(1))
	if !found {
		t.Fatal("HTTP Request is not registered")
	}

	for name, parameters := range map[string]map[string]any{
		"missing url":    {"method": "GET"},
		"bad scheme":     {"method": "GET", "url": "ftp://api.test/x"},
		"unknown method": {"method": "TRACE", "url": "https://api.test/x"},
		"host-less url":  {"method": "GET", "url": "https:///x"},
	} {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// A URL built from an expression cannot be checked until run time, so the
	// compiler must accept it and leave the SSRF check to the executor.
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{
		"method": "GET",
		"url":    map[string]any{"mode": "expression", "value": "{{ $json.url }}"},
	}}); err != nil {
		t.Errorf("an expression URL was rejected at compile time: %v", err)
	}
}

func TestHTTPRequestTruncatesAnOversizedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 5000)))
	}))
	defer server.Close()

	policy := localPolicy()
	policy.MaxResponseBytes = 128
	output, err := nodes.NewHTTPExecutor(policy).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL, "responseFormat": "text",
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0].JSON
	if item["truncated"] != true {
		t.Errorf("truncated = %#v, want true", item["truncated"])
	}
	if body, _ := item["body"].(string); len(body) != 128 {
		t.Errorf("body length = %d, want the policy limit of 128", len(body))
	}
}

func TestHTTPRequestExposesPriorNodeOutputsThroughDollarNode(t *testing.T) {
	t.Parallel()

	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET",
		"url":    map[string]any{"mode": "expression", "value": server.URL + `/{{ $node["Get User"].id }}`},
	}), workflow.NodeInput{}, engine.Request{
		NodeOutputs: map[string]map[string]any{"Get User": {"id": "user-42"}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if path != "/user-42" {
		t.Errorf("path = %q, want the $node lookup resolved", path)
	}
}

func TestHTTPRequestResponseCanBeSerializedForPersistence(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "sid=leaky")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal output = %v", err)
	}
	// The node itself returns the response cookie; the redaction boundary in
	// the repository is what keeps it out of storage.
	if !strings.Contains(string(encoded), "Set-Cookie") {
		t.Errorf("output = %s, want response headers preserved for redaction downstream", encoded)
	}
}
