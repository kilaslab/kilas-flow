package nodes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/binary"
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
	// The parsed response body *is* the item, which is what n8n produces and
	// what an imported workflow's `$json.<field>` expects.
	if output[0][0].JSON["ok"] != true {
		t.Fatalf("first item = %#v, want the decoded JSON response", output[0][0].JSON)
	}
	if output[0][0].JSON["path"] != "/users/ada" {
		t.Errorf("path = %#v, want the response body's own field", output[0][0].JSON["path"])
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
		w.Header().Set("Content-Type", "application/json")
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
	// n8n parity: with neverError the response is ordinary data, so the parsed
	// body is the item and the status is visible only through fullResponse.
	if output[0][0].JSON["error"] != "boom" {
		t.Errorf("JSON = %#v, want the 500 response's parsed body as the item", output[0][0].JSON)
	}
}

// The default output is n8n's, not an envelope: the parsed body as the item, a
// top-level array split into one item per element, anything else under `data`,
// and `{}` for an empty body. Every downstream `$json.<field>` after an
// imported HTTP Request depends on this, and 199 nodes in the corpus read it.
func TestHTTPRequestDefaultOutputIsTheParsedBodyLikeN8N(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/array":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":1},{"id":2},{"id":3}]`))
		case "/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello plain text"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":7,"name":"ada"}`))
		}
	}))
	defer server.Close()

	executor := nodes.NewHTTPExecutor(localPolicy())

	run := func(path string) []workflow.Item {
		t.Helper()
		output, err := executor.Execute(context.Background(), httpNode(map[string]any{
			"method": "GET", "url": server.URL + path,
		}), workflow.NodeInput{}, engine.Request{})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", path, err)
		}
		return output[0]
	}

	object := run("/object")
	if len(object) != 1 || object[0].JSON["id"] != float64(7) {
		t.Fatalf("object response = %#v, want the parsed body as the item", object)
	}
	if _, wrapped := object[0].JSON["body"]; wrapped {
		t.Errorf("object response = %#v, want no envelope key", object[0].JSON)
	}
	if _, wrapped := object[0].JSON["statusCode"]; wrapped {
		t.Errorf("object response = %#v, want no status key without fullResponse", object[0].JSON)
	}

	array := run("/array")
	if len(array) != 3 {
		t.Fatalf("array response produced %d items, want one per element", len(array))
	}
	for index, item := range array {
		if item.JSON["id"] != float64(index+1) {
			t.Errorf("item %d = %#v, want the element at that index", index, item.JSON)
		}
	}

	text := run("/text")
	if len(text) != 1 || text[0].JSON["data"] != "hello plain text" {
		t.Fatalf("text response = %#v, want {data: ...}", text)
	}

	empty := run("/empty")
	if len(empty) != 1 || len(empty[0].JSON) != 0 {
		t.Fatalf("empty response = %#v, want {}", empty)
	}
}

// fullResponse is the only way to the envelope, and then it is n8n's shape:
// lower-case header names and a status message beside the status code.
func TestHTTPRequestFullResponseMatchesN8NEnvelope(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Trace", "abc")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL, "fullResponse": true,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0].JSON
	if item["statusCode"] != float64(http.StatusCreated) {
		t.Errorf("statusCode = %#v, want 201", item["statusCode"])
	}
	if item["statusMessage"] != "Created" {
		t.Errorf("statusMessage = %#v, want Created", item["statusMessage"])
	}
	headers, _ := item["headers"].(map[string]any)
	if headers["x-trace"] != "abc" || headers["content-type"] != "application/json" {
		t.Errorf("headers = %#v, want lower-case names", headers)
	}
	body, _ := item["body"].(string)
	if body != `{"ok":true}` {
		t.Errorf("body = %#v, want the response body", item["body"])
	}
}

// n8n does not follow redirects unless it is told to; a workflow that wants to
// inspect a 302 never saw one.
func TestHTTPRequestDoesNotFollowRedirectsUnlessAsked(t *testing.T) {
	t.Parallel()

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"landed":true}`))
	}))
	defer final.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirecting.Close()

	executor := nodes.NewHTTPExecutor(localPolicy())
	output, err := executor.Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": redirecting.URL, "fullResponse": true,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["statusCode"] != float64(http.StatusFound) {
		t.Fatalf("statusCode = %#v, want the 302 handed to the workflow", output[0][0].JSON["statusCode"])
	}

	followed, err := executor.Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": redirecting.URL, "fullResponse": true, "followRedirects": true,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() with followRedirects error = %v", err)
	}
	if followed[0][0].JSON["statusCode"] != float64(http.StatusOK) {
		t.Fatalf("statusCode = %#v, want the redirect followed when asked", followed[0][0].JSON["statusCode"])
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

func TestHTTPRequestSendsResolvedBodyFieldsRatherThanExpressionWrappers(t *testing.T) {
	t.Parallel()

	var received string
	var contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		contentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// The shape an imported n8n bodyParameter set is stored as: each value is
	// the item's own expression, resolved per item by the runtime. Sending the
	// wrapper instead is the bug — the upstream API received
	// {"id":{"mode":"expression","value":"{{ $json.id }}"}} and the node still
	// reported success.
	parameters := func(bodyType string) map[string]any {
		return map[string]any{
			"method": "POST", "url": server.URL, "sendBody": true, "bodyType": bodyType,
			"bodyFields": map[string]any{
				"id":  map[string]any{"mode": "expression", "value": "{{ $json.id }}"},
				"q":   map[string]any{"mode": "expression", "value": "{{ $json.q }}"},
				"lit": "plain",
			},
		}
	}

	executor := nodes.NewHTTPExecutor(localPolicy())
	if _, err := executor.Execute(context.Background(), httpNode(parameters("json")), workflow.NodeInput{"main": {
		{JSON: map[string]any{"id": float64(1), "q": "hello"}},
	}}, engine.Request{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if received != `{"id":1,"lit":"plain","q":"hello"}` {
		t.Errorf("JSON body = %q, want the fields encoded from their resolved values", received)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	if _, err := executor.Execute(context.Background(), httpNode(parameters("form")), workflow.NodeInput{"main": {
		{JSON: map[string]any{"id": float64(1), "q": "hello"}},
	}}, engine.Request{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if received != "id=1&lit=plain&q=hello" {
		t.Errorf("form body = %q, want the fields encoded from their resolved values", received)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", contentType)
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
	// A body that is not an object lands under `data`, which is the key n8n
	// uses for it.
	if body, _ := item["data"].(string); len(body) != 128 {
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
		"method": "GET", "url": server.URL, "fullResponse": true,
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal output = %v", err)
	}
	// The node itself returns the response cookie — under n8n's lower-case
	// header name — and the redaction boundary in the repository is what keeps
	// it out of storage.
	if !strings.Contains(string(encoded), "set-cookie") {
		t.Errorf("output = %s, want response headers preserved for redaction downstream", encoded)
	}
}

func binaryStore(t *testing.T) engine.BinaryStore {
	t.Helper()
	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return binary.For(store, "tenant-a", "exec-1")
}

// An image forced through `string(contents)` produced an item full of
// replacement characters and no way to recover the bytes. Autodetect now stores
// anything that is not textual, and only the reference reaches the item.
func TestHTTPRequestStoresANonTextResponseAsABinaryReference(t *testing.T) {
	t.Parallel()

	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", `attachment; filename="avatar.png"`)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	store := binaryStore(t)
	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/files/ignored.bin",
	}), workflow.NodeInput{}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	item := output[0][0]
	if _, present := item.JSON["body"]; present {
		t.Fatalf("JSON = %#v, want no body: the payload must never enter the item", item.JSON)
	}
	reference, found := item.Binary["data"]
	if !found {
		t.Fatalf("Binary = %#v, want the response attached under the default property", item.Binary)
	}
	// The server named the file; that name wins over the URL's last segment.
	if reference.FileName != "avatar.png" {
		t.Fatalf("FileName = %q, want the name from Content-Disposition", reference.FileName)
	}
	if reference.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want the response's media type without parameters", reference.MediaType)
	}
	if reference.Size != int64(len(payload)) {
		t.Fatalf("Size = %d, want %d", reference.Size, len(payload))
	}

	body, _, err := store.Get(reference.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer body.Close()
	stored, _ := io.ReadAll(body)
	if !bytes.Equal(stored, payload) {
		t.Fatalf("stored payload = %#v, want the bytes the server sent", stored)
	}
}

func TestHTTPRequestKeepsTextualResponsesInJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", r.URL.Query().Get("type"))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// A structured suffix and an unlabelled response are both text: turning
	// every response with no Content-Type into a file would be a worse default
	// than the one this replaces.
	for _, contentType := range []string{"application/json", "text/csv", "application/vnd.api+json", ""} {
		output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
			"method": "GET", "url": server.URL + "/?type=" + url.QueryEscape(contentType),
		}), workflow.NodeInput{}, engine.Request{Binaries: binaryStore(t)})
		if err != nil {
			t.Fatalf("Execute() with %q error = %v", contentType, err)
		}
		if output[0][0].Binary != nil {
			t.Fatalf("Content-Type %q was stored as a file, want it decoded", contentType)
		}
		item := output[0][0].JSON
		if strings.Contains(contentType, "json") {
			if item["ok"] != true {
				t.Fatalf("Content-Type %q produced %#v, want the parsed body as the item", contentType, item)
			}
			continue
		}
		// Not labelled JSON: n8n keeps the raw text under `data` rather than
		// guessing, and every key of the item is still readable.
		if item["data"] != `{"ok":true}` {
			t.Fatalf("Content-Type %q produced %#v, want {data: ...}", contentType, item)
		}
	}
}

// `file` is explicit: a caller that wants the bytes of a JSON response gets
// them, under the property it names.
func TestHTTPRequestStoresAFileOnRequestUnderTheNamedProperty(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/report.json",
		"responseFormat": "file", "outputPropertyName": "attachment",
	}), workflow.NodeInput{}, engine.Request{Binaries: binaryStore(t)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	reference, found := output[0][0].Binary["attachment"]
	if !found {
		t.Fatalf("Binary = %#v, want the named property", output[0][0].Binary)
	}
	// No Content-Disposition, so the URL's last segment names the file.
	if reference.FileName != "report.json" {
		t.Fatalf("FileName = %q, want the URL's last segment", reference.FileName)
	}
}

// Half a PDF that reports success is worse than a failure naming the bound.
// Autodetect degrades to text instead, which the case below covers.
func TestHTTPRequestRefusesToStoreATruncatedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(make([]byte, 64))
	}))
	defer server.Close()

	policy := localPolicy()
	policy.MaxResponseBytes = 16
	_, err := nodes.NewHTTPExecutor(policy).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/big.bin", "responseFormat": "file",
	}), workflow.NodeInput{}, engine.Request{Binaries: binaryStore(t)})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("Execute() error = %v, want a refusal naming the bound", err)
	}

	// Autodetect never asked for a file, so it keeps the behaviour it had: a
	// truncated body and the flag that says so.
	output, err := nodes.NewHTTPExecutor(policy).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/big.bin",
	}), workflow.NodeInput{}, engine.Request{Binaries: binaryStore(t)})
	if err != nil {
		t.Fatalf("Execute() with autodetect error = %v", err)
	}
	if output[0][0].Binary != nil {
		t.Fatalf("Binary = %#v, want a truncated response left as text", output[0][0].Binary)
	}
	if output[0][0].JSON["truncated"] != true {
		t.Fatalf("truncated = %#v, want the flag set", output[0][0].JSON["truncated"])
	}
}

// `file` is a request, and one that cannot be honoured is an error the caller
// should see: a node that asked for a file and got a string is a silent wrong
// answer.
func TestHTTPRequestSaysWhenAFileWasAskedForAndStorageIsNotConfigured(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer server.Close()

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/doc.pdf", "responseFormat": "file",
	}), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Execute() error = %v, want it to name the missing configuration", err)
	}
}

// Autodetect is a preference, not a request. Binary storage is off by default,
// so a server that never configured it must keep decoding non-text responses
// exactly as it did before rather than failing every one of them at once.
func TestHTTPRequestAutodetectDecodesWhenThereIsNowhereToStore(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer server.Close()

	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/doc.pdf",
	}), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v, want the previous decoding behaviour", err)
	}
	if output[0][0].Binary != nil {
		t.Fatalf("Binary = %#v, want none with no store configured", output[0][0].Binary)
	}
	if output[0][0].JSON["data"] != "%PDF-1.4" {
		t.Fatalf("data = %#v, want the response decoded as text", output[0][0].JSON["data"])
	}
}

// A remote server does not get to put a path in a name a person will read.
func TestHTTPRequestReducesAResponseFileNameToOneSegment(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="..\..\windows\evil.exe"`)
		_, _ = w.Write([]byte("bytes"))
	}))
	defer server.Close()

	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), httpNode(map[string]any{
		"method": "GET", "url": server.URL + "/download",
	}), workflow.NodeInput{}, engine.Request{Binaries: binaryStore(t)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if name := output[0][0].Binary["data"].FileName; name != "evil.exe" {
		t.Fatalf("FileName = %q, want the trailing segment only", name)
	}
}
