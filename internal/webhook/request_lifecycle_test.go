package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// listCall is one request the merge made.
type listCall struct {
	method string
	path   string
	body   string
}

// listStub is a service that holds a document of its own — a WAHA session, in
// the shape this exists for: a webhook list beside settings and fields this
// code has never heard of.
type listStub struct {
	mu       sync.Mutex
	document string
	calls    []listCall
}

func newListStub(t *testing.T, document string) (*listStub, *httptest.Server) {
	t.Helper()
	stub := &listStub{document: document}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		stub.mu.Lock()
		stub.calls = append(stub.calls, listCall{method: request.Method, path: request.URL.Path, body: string(raw)})
		if request.Method == http.MethodPut {
			stub.document = string(raw)
		}
		document := stub.document
		stub.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, document)
	}))
	t.Cleanup(server.Close)
	return stub, server
}

func (stub *listStub) recorded() []listCall {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]listCall(nil), stub.calls...)
}

// written is the body of the one write, and fails when there was not exactly
// one: a merge that writes twice has restarted a session twice.
func (stub *listStub) written(t *testing.T) map[string]any {
	t.Helper()
	writes := 0
	var body string
	for _, call := range stub.recorded() {
		if call.method == http.MethodPut {
			writes++
			body = call.body
		}
	}
	if writes != 1 {
		t.Fatalf("writes = %d, want exactly one: %#v", writes, stub.recorded())
	}
	return decodedObject(t, body)
}

func decodedObject(t *testing.T, body string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode %q error = %v", body, err)
	}
	return decoded
}

// entriesOf reads a joined list out of a decoded document.
func entriesOf(t *testing.T, document map[string]any, path ...string) []any {
	t.Helper()
	current := document
	for _, segment := range path[:len(path)-1] {
		nested, isObject := current[segment].(map[string]any)
		if !isObject {
			t.Fatalf("document has no %s to read: %#v", strings.Join(path, "."), document)
		}
		current = nested
	}
	entries, isList := current[path[len(path)-1]].([]any)
	if !isList {
		t.Fatalf("document has no %s list: %#v", strings.Join(path, "."), document)
	}
	return entries
}

// listCredential is a WAHA credential pointed at the stub.
type listCredential struct {
	baseURL string
	apiKey  string
}

func (credential listCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "WAHA", Type: "wahaApi",
		Fields: map[string]string{"baseUrl": credential.baseURL, "apiKey": credential.apiKey},
	}, nil
}

// listLifecycle is the merge the WAHA pack describes.
func listLifecycle() webhook.WebhookListLifecycle {
	return webhook.WebhookListLifecycle{
		Session:        "/api/sessions/{{ .Parameter.session }}",
		BaseURLField:   "baseUrl",
		CredentialType: "wahaApi",
		ListPath:       "config.webhooks",
		URLField:       "url",
		Entry: map[string]any{
			"url":    "{{ .PublicURL }}",
			"events": []any{"*"},
			"hmac":   map[string]any{"key": "{{ .Parameter.hmacSecret }}"},
		},
	}
}

func listContext(stubURL, route string, parameters map[string]any) webhook.LifecycleContext {
	declared := map[string]any{
		"session":      "sales",
		"$credentials": map[string]any{"wahaApi": "cred-1"},
	}
	for key, value := range parameters {
		declared[key] = value
	}
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return webhook.LifecycleContext{
		TenantID: "tenant-a", WorkflowID: "wf_1",
		Binding: repository.WebhookBinding{
			NodeID: "trigger", NodeType: "pack.wahaTrigger", Route: route, Parameters: declared,
		},
		PublicURL:   "https://flows.example.test/webhook/" + route,
		HTTP:        policy,
		Credentials: listCredential{baseURL: stubURL, apiKey: "k-waha"},
	}
}

// listSession is a session somebody else configured: another workflow's
// webhook, a proxy, an engine, and a field no shape in this repository has.
const listSession = `{
  "name": "sales",
  "status": "WORKING",
  "engine": {"type": "NOWEB"},
  "proxy": {"server": "http://proxy.example.test:3128", "enabled": true},
  "config": {"webhooks": [{"url": "https://someone-else.example.test/hook", "events": ["message"]}], "debug": true}
}`

// TestRequestLifecycleCreateMergesIntoTheSessionsDocument is the whole bug: the
// session is read, this route's entry is added, and everything else is written
// back exactly as it arrived.
//
// The body is compared as a whole document, so a merge that dropped the
// customer's proxy, their engine or a field this code has never heard of fails
// here rather than only in production.
func TestRequestLifecycleCreateMergesIntoTheSessionsDocument(t *testing.T) {
	t.Parallel()

	stub, server := newListStub(t, listSession)
	if err := listLifecycle().Create(context.Background(), listContext(server.URL, "abc123", nil)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	calls := stub.recorded()
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[1].method != http.MethodPut {
		t.Fatalf("calls = %#v, want one read of the session then one write of it", calls)
	}
	if calls[1].path != "/api/sessions/sales" {
		t.Fatalf("PUT path = %q, want the session the node names", calls[1].path)
	}

	want := decodedObject(t, listSession)
	config := want["config"].(map[string]any)
	config["webhooks"] = []any{
		map[string]any{"url": "https://someone-else.example.test/hook", "events": []any{"message"}},
		map[string]any{"url": "https://flows.example.test/webhook/abc123", "events": []any{"*"}},
	}
	if got := stub.written(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("PUT body = %s, want the session with one entry added:\n%s", mustEncode(t, got), mustEncode(t, want))
	}
}

func mustEncode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	return string(encoded)
}

// TestRequestLifecycleCarriesTheHmacKeyOnlyWhenTheNodeHasOne: WAHA signs with
// config.webhooks[].hmac.key, and an entry with a key the node does not have
// would configure signing this server then refuses. No secret has to mean no
// hmac at all, not an empty one.
func TestRequestLifecycleCarriesTheHmacKeyOnlyWhenTheNodeHasOne(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		parameters map[string]any
		want       any
	}{
		"with a secret": {parameters: map[string]any{"hmacSecret": "topsecret"},
			want: map[string]any{"key": "topsecret"}},
		"without a secret": {parameters: nil, want: nil},
		"with an empty secret": {parameters: map[string]any{"hmacSecret": ""},
			want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newListStub(t, `{"name":"sales"}`)
			if err := listLifecycle().Create(context.Background(), listContext(server.URL, "abc123", testCase.parameters)); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			entries := entriesOf(t, stub.written(t), "config", "webhooks")
			if len(entries) != 1 {
				t.Fatalf("webhooks = %#v, want one entry", entries)
			}
			entry := entries[0].(map[string]any)
			hmac, present := entry["hmac"]
			if testCase.want == nil {
				if present {
					t.Fatalf("entry = %#v, want no hmac key at all", entry)
				}
				return
			}
			if !reflect.DeepEqual(hmac, testCase.want) {
				t.Fatalf("hmac = %#v, want %#v", hmac, testCase.want)
			}
		})
	}
}

// TestRequestLifecycleCreateDoesNotDuplicateARoute: two entries for one route
// are two deliveries per event, and WAHA retries each of them.
func TestRequestLifecycleCreateDoesNotDuplicateARoute(t *testing.T) {
	t.Parallel()

	stub, server := newListStub(t, `{"config":{"webhooks":[
		{"url":"https://flows.example.test/webhook/abc123","events":["*"]},
		{"url":"https://flows.example.test/webhook/abc123?retry=1","events":["*"]},
		{"url":"https://someone-else.example.test/hook","events":["message"]}
	]}}`)
	if err := listLifecycle().Create(context.Background(), listContext(server.URL, "abc123", nil)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	entries := entriesOf(t, stub.written(t), "config", "webhooks")
	if len(entries) != 2 {
		t.Fatalf("webhooks = %#v, want this route once and the other workflow's entry", entries)
	}
	if entries[0].(map[string]any)["url"] != "https://someone-else.example.test/hook" {
		t.Fatalf("webhooks = %#v, want the other workflow's entry kept", entries)
	}
}

// TestRequestLifecycleCheckExistsReportsThisRouteOnly is what stops an
// activation from writing at all — and writing WAHA's document restarts a live
// session — so a route that is not there has to be recognised as not there.
func TestRequestLifecycleCheckExistsReportsThisRouteOnly(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		document string
		route    string
		want     bool
	}{
		"registered": {document: `{"config":{"webhooks":[{"url":"https://flows.example.test/webhook/abc123"}]}}`,
			route: "abc123", want: true},
		"another route": {document: `{"config":{"webhooks":[{"url":"https://flows.example.test/webhook/zzz999"}]}}`,
			route: "abc123", want: false},
		"another workflow on the session": {document: listSession, route: "abc123", want: false},
		"no list at all":                  {document: `{"name":"sales"}`, route: "abc123", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newListStub(t, testCase.document)
			exists, err := listLifecycle().CheckExists(context.Background(), listContext(server.URL, testCase.route, nil))
			if err != nil {
				t.Fatalf("CheckExists() error = %v", err)
			}
			if exists != testCase.want {
				t.Fatalf("CheckExists() = %v, want %v", exists, testCase.want)
			}
			if calls := stub.recorded(); len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("calls = %#v, want one read and no write", calls)
			}
		})
	}
}

// TestRequestLifecycleDeleteRemovesOnlyThisRoute is the other half: leaving a
// route behind makes WAHA retry a delivery that can only answer 404, and
// removing somebody else's webhook unregisters their workflow.
func TestRequestLifecycleDeleteRemovesOnlyThisRoute(t *testing.T) {
	t.Parallel()

	stub, server := newListStub(t, `{"status":"WORKING","config":{"webhooks":[
		{"url":"https://someone-else.example.test/hook","events":["message"]},
		{"url":"https://flows.example.test/webhook/abc123","events":["*"]}
	]}}`)
	if err := listLifecycle().Delete(context.Background(), listContext(server.URL, "abc123", nil)); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	want := map[string]any{
		"status": "WORKING",
		"config": map[string]any{"webhooks": []any{
			map[string]any{"url": "https://someone-else.example.test/hook", "events": []any{"message"}},
		}},
	}
	if got := stub.written(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("PUT body = %s, want the session with this route removed:\n%s", mustEncode(t, got), mustEncode(t, want))
	}
}

// TestRequestLifecycleDeleteWithNothingToRemoveWritesNothing: writing WAHA's
// session document restarts the session, and a deactivation that changes
// nothing must not restart a live one.
func TestRequestLifecycleDeleteWithNothingToRemoveWritesNothing(t *testing.T) {
	t.Parallel()

	stub, server := newListStub(t, listSession)
	if err := listLifecycle().Delete(context.Background(), listContext(server.URL, "abc123", nil)); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	for _, call := range stub.recorded() {
		if call.method != http.MethodGet {
			t.Fatalf("calls = %#v, want reads only", stub.recorded())
		}
	}
}

// TestRequestLifecycleToleratesASessionWithNoList: a session that has never
// been configured has no webhooks to preserve, and a session whose document
// says nothing is an empty document rather than an error.
func TestRequestLifecycleToleratesASessionWithNoList(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		document string
		listPath string
	}{
		"no document":        {document: ``, listPath: "config.webhooks"},
		"no config key":      {document: `{"name":"sales"}`, listPath: "config.webhooks"},
		"the list elsewhere": {document: listSession, listPath: "settings.webhooks"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newListStub(t, testCase.document)
			lifecycle := listLifecycle()
			lifecycle.ListPath = testCase.listPath
			if err := lifecycle.Create(context.Background(), listContext(server.URL, "abc123", nil)); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			written := stub.written(t)
			entries := entriesOf(t, written, strings.Split(testCase.listPath, ".")...)
			if len(entries) != 1 || entries[0].(map[string]any)["url"] != "https://flows.example.test/webhook/abc123" {
				t.Fatalf("webhooks = %#v, want this route's entry", entries)
			}
			if testCase.listPath == "settings.webhooks" {
				// The list this lifecycle was not told about is not its to move.
				preexisting := entriesOf(t, written, "config", "webhooks")
				if len(preexisting) != 1 || preexisting[0].(map[string]any)["url"] != "https://someone-else.example.test/hook" {
					t.Fatalf("config.webhooks = %#v, want the session's own list untouched", preexisting)
				}
			}
		})
	}
}

// TestRequestLifecycleRefusesToOverwriteWhatItCannotRead: a document this code
// does not understand is a document it does not get to rewrite. Failing the
// activation is recoverable; deleting somebody's settings is not.
func TestRequestLifecycleRefusesToOverwriteWhatItCannotRead(t *testing.T) {
	t.Parallel()

	stub, server := newListStub(t, `{"config":{"webhooks":{"url":"https://someone-else.example.test/hook"}}}`)
	err := listLifecycle().Create(context.Background(), listContext(server.URL, "abc123", nil))
	if err == nil || !strings.Contains(err.Error(), "not a list") {
		t.Fatalf("Create() error = %v, want a refusal to overwrite the session's webhooks", err)
	}
	for _, call := range stub.recorded() {
		if call.method != http.MethodGet {
			t.Fatalf("calls = %#v, want reads only", stub.recorded())
		}
	}
}
