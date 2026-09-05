package routing_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

const packType = "pack.chat"

// localPolicy allows the loopback test server while keeping every other guard.
func localPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// packDefinition is a declarative node in the shape a generated pack emits: a
// resource, an operation, and parameters gated on the operation. There is no Go
// anywhere in its execution path.
func packDefinition() node.Definition {
	return node.Definition{
		Type: packType, Version: workflow.V(1),
		DisplayName: "Chat", Category: "Pack",
		Group:       []node.NodeGroup{node.GroupOutput},
		Inputs:      []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Credentials: []node.CredentialRequirement{{Type: "httpHeaderAuth"}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Default: "message",
				Options: []node.PropertyOption{{Label: "Message", Value: "message"}, {Label: "Chat", Value: "chat"}},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: "send",
				Options: []node.PropertyOption{{Label: "Send", Value: "send"}, {Label: "List", Value: "list"}},
			},
			{
				Key: "chatId", Label: "Chat ID", Kind: node.PropertyString,
				VisibleWhen: []node.VisibilityCondition{{Key: "resource", Equals: "message"}},
			},
			{
				Key: "text", Label: "Text", Kind: node.PropertyString,
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: "send"}},
			},
		},
		ExecutorID: routing.ExecutorID,
	}
}

func packRouting(baseURL string) *routing.Node {
	dotted := false
	return &routing.Node{
		Type: packType, Version: workflow.V(1),
		Defaults: routing.Request{
			BaseURL: baseURL,
			Headers: map[string]any{"X-Pack": "kilasflow"},
		},
		Options: map[string]map[string]routing.Routing{
			"operation": {
				"send": {
					Request: &routing.Request{Method: "POST", URL: "/api/sendText"},
				},
				"list": {
					Request: &routing.Request{Method: "GET", URL: "/api/chats"},
					Output: &routing.Output{PostReceive: []routing.PostReceive{
						{Type: routing.PostReceiveRootProperty, Properties: map[string]any{"property": "data.chats"}},
					}},
				},
			},
		},
		Properties: map[string]routing.Routing{
			"chatId": {Send: &routing.Send{Type: "body", Property: "chatId"}},
			"text": {Send: &routing.Send{
				Type: "body", Property: "message.text", Value: "{{ $value }}",
				PropertyInDotNotation: nil,
			}},
			"resource": {Send: &routing.Send{Type: "query", Property: "resource", PropertyInDotNotation: &dotted}},
		},
	}
}

type harness struct {
	executor *routing.Executor
	registry *node.Registry
}

func newHarness(t *testing.T, policy safehttp.Policy, description *routing.Node) harness {
	t.Helper()
	registry := node.NewRegistry()
	if err := registry.Register(packDefinition()); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	routes := routing.NewRegistry()
	if err := routes.Register(description); err != nil {
		t.Fatalf("routing Register() error = %v", err)
	}
	return harness{executor: routing.NewExecutor(policy, routes, registry), registry: registry}
}

func packNode(t *testing.T, registry *node.Registry, parameters map[string]any, credentials map[string]string) workflow.IRNode {
	t.Helper()
	definition, found := registry.Lookup(packType, workflow.V(1))
	if !found {
		t.Fatal("the pack node is not registered")
	}
	return workflow.IRNode{
		ID: "pack-1", Name: "Chat", Type: packType, TypeVersion: workflow.V(1),
		Parameters: parameters, Credentials: credentials, Definition: definition,
	}
}

// stubCredentials stands in for the tenant-scoped resolver the runtime injects.
type stubCredentials struct {
	credential engine.Credential
	err        error
}

func (stub *stubCredentials) ResolveCredential(_ context.Context, _ string) (engine.Credential, error) {
	return stub.credential, stub.err
}

func headerCredential(domains ...string) *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-1", Name: "Pack key", Type: "httpHeaderAuth",
		Fields:         map[string]string{"name": "X-Api-Key", "value": "s3cret"},
		AllowedDomains: domains,
	}}
}

// The whole claim of this ticket: one executor, no node-specific Go, and a
// request assembled entirely from metadata.
func TestOneExecutorRunsANodeDescribedOnlyByMetadata(t *testing.T) {
	t.Parallel()

	type received struct {
		method string
		path   string
		query  string
		header string
		apiKey string
		body   map[string]any
	}
	var got received
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = received{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
			header: r.Header.Get("X-Pack"), apiKey: r.Header.Get("X-Api-Key")}
		_ = json.Unmarshal(body, &got.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m-1","sent":true}`))
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	output, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "123@c.us", "text": "hello",
	}, map[string]string{"httpHeaderAuth": "cred-1"}), workflow.NodeInput{}, engine.Request{Credentials: headerCredential()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got.method != http.MethodPost || got.path != "/api/sendText" {
		t.Fatalf("request = %s %s, want POST /api/sendText from the operation's routing", got.method, got.path)
	}
	if got.header != "kilasflow" {
		t.Fatalf("X-Pack = %q, want the header from requestDefaults", got.header)
	}
	// The credential is applied by the same path the hand-written node uses.
	if got.apiKey != "s3cret" {
		t.Fatalf("X-Api-Key = %q, want the credential applied", got.apiKey)
	}
	if got.query != "resource=message" {
		t.Fatalf("query = %q, want the query-placed property", got.query)
	}
	if got.body["chatId"] != "123@c.us" {
		t.Fatalf("body chatId = %#v, want the sent parameter", got.body["chatId"])
	}
	// Dot notation nests by default, so `message.text` is an object.
	message, ok := got.body["message"].(map[string]any)
	if !ok || message["text"] != "hello" {
		t.Fatalf("body message = %#v, want dot notation to nest", got.body["message"])
	}

	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one item", output)
	}
	if output[0][0].JSON["id"] != "m-1" {
		t.Fatalf("item = %#v, want the decoded response", output[0][0].JSON)
	}
}

// A node keeps the parameters of every resource it has ever been set to, so
// without the visibility check a node switched from Message to Chat would still
// send the message body it no longer shows.
func TestRoutingSkipsPropertiesThatAreNotVisible(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"chats":[{"id":"a"},{"id":"b"}]}}`))
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	output, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		// resource is chat, so chatId is hidden; operation is list, so text is.
		"resource": "chat", "operation": "list", "chatId": "left over", "text": "left over",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, present := body["chatId"]; present {
		t.Fatalf("body = %#v, want no hidden property sent", body)
	}
	if _, present := body["message"]; present {
		t.Fatalf("body = %#v, want no hidden property sent", body)
	}
	// rootProperty walked a dotted path and produced one item per element.
	if len(output[0]) != 2 || output[0][0].JSON["id"] != "a" {
		t.Fatalf("output = %#v, want one item per extracted element", output[0])
	}
}

func TestPostReceiveSetsKeyValuesAndLimits(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"chats":[{"id":"a","n":1},{"id":"b","n":2},{"id":"c","n":3}]}}`))
	}))
	defer server.Close()

	description := packRouting(server.URL)
	description.Options["operation"]["list"] = routing.Routing{
		Request: &routing.Request{Method: "GET", URL: "/api/chats"},
		Output: &routing.Output{PostReceive: []routing.PostReceive{
			{Type: routing.PostReceiveRootProperty, Properties: map[string]any{"property": "data.chats"}},
			{Type: routing.PostReceiveSetKeyValue, Properties: map[string]any{
				"chatId": "{{ $json.id }}", "label": "chat {{ $json.id }}",
			}},
			{Type: routing.PostReceiveLimit, Properties: map[string]any{"maxResults": 2}},
		}},
	}

	test := newHarness(t, localPolicy(), description)
	output, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "chat", "operation": "list",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 {
		t.Fatalf("output = %#v, want the limit applied", output[0])
	}
	first := output[0][0].JSON
	if first["chatId"] != "a" || first["label"] != "chat a" {
		t.Fatalf("item = %#v, want setKeyValue over the extracted element", first)
	}
	// setKeyValue replaces the item rather than adding to it, which is what
	// makes a pack's output shape predictable.
	if _, present := first["n"]; present {
		t.Fatalf("item = %#v, want only the declared keys", first)
	}
}

// A declarative node runs once per item and pagination walks pages inside that
// run. Confusing the two loops is what makes a two-item input silently return
// one list counted twice.
func TestOffsetPaginationWalksPagesPerInputItem(t *testing.T) {
	t.Parallel()

	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offsets = append(offsets, r.URL.Query().Get("offset"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`{"data":{"chats":[{"id":"a"},{"id":"b"}]}}`))
		default:
			// A short page ends the walk.
			_, _ = w.Write([]byte(`{"data":{"chats":[{"id":"c"}]}}`))
		}
	}))
	defer server.Close()

	description := packRouting(server.URL)
	listing := description.Options["operation"]["list"]
	listing.Operations = &routing.Operations{Pagination: &routing.Pagination{
		Type: routing.PaginationOffset,
		Properties: routing.OffsetPagination{
			LimitParameter: "limit", OffsetParameter: "offset", PageSize: 2, Type: "query",
		},
	}}
	description.Options["operation"]["list"] = listing

	test := newHarness(t, localPolicy(), description)
	output, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "chat", "operation": "list",
	}, nil), workflow.NodeInput{"main": {
		{JSON: map[string]any{"n": float64(1)}},
		{JSON: map[string]any{"n": float64(2)}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Two items, two walks of two pages each: 4 requests, 6 items.
	if len(offsets) != 4 {
		t.Fatalf("requests = %v, want two independent two-page walks", offsets)
	}
	if strings.Join(offsets, ",") != "0,2,0,2" {
		t.Fatalf("offsets = %v, want the offset to reset for the second input item", offsets)
	}
	if len(output[0]) != 6 {
		t.Fatalf("output = %d items, want three per input item", len(output[0]))
	}
}

// The URL is assembled from the defaults' base and the operation's suffix, with
// placeholders read from the node's own parameters and the credential's
// non-secret fields.
func TestRoutingResolvesTemplatesFromParametersAndCredentials(t *testing.T) {
	t.Parallel()

	var path, header string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, header = r.URL.Path, r.Header.Get("X-Header-Name")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	description := packRouting(server.URL)
	description.Options["operation"]["send"] = routing.Routing{
		Request: &routing.Request{
			Method: "POST",
			URL:    "/api/chats/{{ $parameter.chatId }}/messages",
			// A non-secret credential field is readable; the secret one is not.
			Headers: map[string]any{"X-Header-Name": "{{ $credentials.name }}"},
		},
	}

	test := newHarness(t, localPolicy(), description)
	if _, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "42", "text": "hi",
	}, map[string]string{"httpHeaderAuth": "cred-1"}), workflow.NodeInput{},
		engine.Request{Credentials: headerCredential()}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if path != "/api/chats/42/messages" {
		t.Fatalf("path = %q, want the parameter substituted", path)
	}
	if header != "X-Api-Key" {
		t.Fatalf("X-Header-Name = %q, want the non-secret credential field", header)
	}
}

// `$credentials` carries the credential type's non-secret fields only. A base
// URL, never a token — and the filter is the credential type's own descriptor,
// so a type this build does not know exposes nothing rather than everything.
func TestCredentialsRootNeverExposesASecret(t *testing.T) {
	t.Parallel()

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	description := packRouting(server.URL)
	description.Options["operation"]["send"] = routing.Routing{
		Request: &routing.Request{Method: "POST", URL: "/api/sendText", Body: map[string]any{
			// `value` is the credential's secret half.
			"leaked": "{{ $credentials.value }}",
			"public": "{{ $credentials.name }}",
		}},
	}

	test := newHarness(t, localPolicy(), description)
	if _, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, map[string]string{"httpHeaderAuth": "cred-1"}), workflow.NodeInput{},
		engine.Request{Credentials: headerCredential()}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode body error = %v", err)
	}
	if sent["public"] != "X-Api-Key" {
		t.Fatalf("public = %#v, want the non-secret field readable", sent["public"])
	}
	if sent["leaked"] != nil {
		t.Fatalf("leaked = %#v, want the secret field to resolve to nothing", sent["leaked"])
	}
	if strings.Contains(string(body), "s3cret") {
		t.Fatalf("body = %s, want no secret anywhere in it", body)
	}
}

// Every request goes through the policy, checked before the dial as well as at
// it, so a routed node aimed at a private address fails with the policy's own
// error rather than a connection error or a success.
func TestRoutedNodeIsRefusedByTheNetworkPolicy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// The default policy refuses private networks; the test server is loopback.
	test := newHarness(t, safehttp.DefaultPolicy(), packRouting(server.URL))
	_, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "request target is not allowed") {
		t.Fatalf("Execute() error = %v, want the policy's refusal", err)
	}
}

func TestRoutedNodeRefusesAWrongCredentialTypeOrAnOutOfScopeHost(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	parameters := map[string]any{"resource": "message", "operation": "send", "chatId": "1", "text": "hi"}
	declared := map[string]string{"httpHeaderAuth": "cred-1"}

	wrongType := headerCredential()
	wrongType.credential.Type = "httpBearerAuth"
	_, err := test.executor.Execute(context.Background(), packNode(t, test.registry, parameters, declared),
		workflow.NodeInput{}, engine.Request{Credentials: wrongType})
	if err == nil || !strings.Contains(err.Error(), "not httpHeaderAuth") {
		t.Fatalf("Execute() error = %v, want a credential type rejection", err)
	}

	outOfScope := headerCredential("api.elsewhere.test")
	_, err = test.executor.Execute(context.Background(), packNode(t, test.registry, parameters, declared),
		workflow.NodeInput{}, engine.Request{Credentials: outOfScope})
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Fatalf("Execute() error = %v, want a host scope rejection", err)
	}
}

// A failing call names what a user can actually change: the node, the resource
// and operation in effect, and what the server said.
func TestAFailingRoutedCallNamesTheOperationAndStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"bad chat id"}`))
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	_, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err == nil {
		t.Fatal("Execute() succeeded, want the status reported")
	}
	for _, want := range []string{`"Chat"`, "resource message", "operation send", "422"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
}

// An unimplemented routing feature fails when the pack is registered, never at
// run time: a request that quietly skips the step that was going to sign it is
// worse than a pack that will not load.
func TestRegistrationRefusesWhatTheInterpreterDoesNotImplement(t *testing.T) {
	t.Parallel()

	for name, routed := range map[string]routing.Routing{
		"a JavaScript preSend hook": {Send: &routing.Send{
			Type: "body", Property: "x", PreSend: []string{"presendSignRequest"},
		}},
		"an unimplemented postReceive action": {Output: &routing.Output{
			PostReceive: []routing.PostReceive{{Type: "binaryData"}},
		}},
		"an unimplemented pagination type": {Operations: &routing.Operations{
			Pagination: &routing.Pagination{Type: "generic"},
		}},
		"offset pagination with no page size": {Operations: &routing.Operations{
			Pagination: &routing.Pagination{Type: routing.PaginationOffset, Properties: routing.OffsetPagination{
				OffsetParameter: "offset", Type: "query",
			}},
		}},
		"a send placement that is neither body nor query": {Send: &routing.Send{
			Type: "headers", Property: "x",
		}},
	} {
		routes := routing.NewRegistry()
		err := routes.Register(&routing.Node{
			Type: packType, Version: workflow.V(1),
			Properties: map[string]routing.Routing{"x": routed},
		})
		if err == nil {
			t.Fatalf("Register() accepted %s, want it refused at registration", name)
		}
	}
}

// A pack written against a routing feature this build does not implement must
// fail loudly at load: silently dropping the field would produce a request that
// looks like it worked.
func TestDecodeRefusesAnUnknownRoutingField(t *testing.T) {
	t.Parallel()

	if _, err := routing.Decode([]byte(`{"type":"pack.chat","properties":{"x":{"send":{"type":"body","property":"x","encoding":"multipart"}}}}`)); err == nil {
		t.Fatal("Decode() accepted an unknown field, want it refused")
	}
	description, err := routing.Decode([]byte(`{"type":"pack.chat","version":1,"requestDefaults":{"baseURL":"https://api.test"}}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if description.Defaults.BaseURL != "https://api.test" {
		t.Fatalf("Defaults = %#v, want the decoded base URL", description.Defaults)
	}
}

func TestAnUnregisteredNodeTypeSaysSoRatherThanGuessing(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	executor := routing.NewExecutor(localPolicy(), routing.NewRegistry(), registry)
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "pack-1", Name: "Chat", Type: packType, TypeVersion: workflow.V(1),
	}, workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "no routing description is registered") {
		t.Fatalf("Execute() error = %v, want it to name the missing description", err)
	}
}

// The URL is checked before the dial, not only at it. Relying on the dialer
// alone means the check runs after DNS, so an unresolvable host fails with a
// lookup error instead of the policy's own, and a forbidden-but-resolvable host
// is contacted before it is refused.
func TestTheURLIsCheckedBeforeTheServerIsContacted(t *testing.T) {
	t.Parallel()

	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted = true
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	policy := localPolicy()
	policy.AllowedHosts = []string{"api.allowed.test"}
	test := newHarness(t, policy, packRouting(server.URL))
	_, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "not in the allowed list") {
		t.Fatalf("Execute() error = %v, want the pre-flight refusal", err)
	}
	if contacted {
		t.Fatal("the server was contacted before the URL was refused")
	}
}

// A declarative pack describes a JSON API. A response that is not JSON is not
// silently discarded and not guessed at — binary responses are the binary
// store's business, and `binaryData` is refused at registration so a pack that
// needs one fails to load rather than losing bytes at run time.
func TestANonJSONResponseIsReportedRatherThanDiscarded(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	_, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("Execute() error = %v, want the response shape reported", err)
	}
}

// An empty response body is an item, not a failure: an API that answers a
// delete with 204 and nothing else is ordinary.
func TestAnEmptyResponseStillProducesAnItem(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	test := newHarness(t, localPolicy(), packRouting(server.URL))
	output, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "message", "operation": "send", "chatId": "1", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || len(output[0][0].JSON) != 0 {
		t.Fatalf("output = %#v, want one empty item", output[0])
	}
}

// A generated pack expresses placement on the operation, because a KilasFlow
// definition holds one property per key and different operations put the same
// parameter in different places.
func TestOperationLevelSendsPlaceNamedParametersAndEscapePathSegments(t *testing.T) {
	t.Parallel()

	var path, query string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.EscapedPath(), r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	description := packRouting(server.URL)
	description.Properties = map[string]routing.Routing{}
	description.Options["operation"]["send"] = routing.Routing{
		Request: &routing.Request{Method: "POST", URL: "/api/{chatId}/messages"},
		Sends: []routing.Send{
			{From: "chatId", Type: "path", Property: "chatId"},
			{From: "text", Type: "body", Property: "message.text"},
			{From: "resource", Type: "query", Property: "resource"},
		},
	}

	test := newHarness(t, localPolicy(), description)
	if _, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		// A slash in a path parameter must not be able to change the endpoint.
		"resource": "message", "operation": "send", "chatId": "a/b", "text": "hi",
	}, nil), workflow.NodeInput{}, engine.Request{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if path != "/api/a%2Fb/messages" {
		t.Fatalf("path = %q, want the path parameter escaped to one segment", path)
	}
	if query != "resource=message" {
		t.Fatalf("query = %q, want the query-placed parameter", query)
	}
	message, _ := body["message"].(map[string]any)
	if message["text"] != "hi" {
		t.Fatalf("body = %#v, want the body-placed parameter", body)
	}
}

// A node keeps the parameters of every resource it has ever been set to. An
// operation that names one of those by accident must not post it to an endpoint
// that never asked for it.
func TestOperationLevelSendsSkipAParameterThatIsNotShown(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	description := packRouting(server.URL)
	description.Properties = map[string]routing.Routing{}
	description.Options["operation"]["list"] = routing.Routing{
		Request: &routing.Request{Method: "POST", URL: "/api/chats"},
		// chatId is only shown when resource is message.
		Sends: []routing.Send{{From: "chatId", Type: "body", Property: "chatId"}},
	}

	test := newHarness(t, localPolicy(), description)
	if _, err := test.executor.Execute(context.Background(), packNode(t, test.registry, map[string]any{
		"resource": "chat", "operation": "list", "chatId": "left over",
	}, nil), workflow.NodeInput{}, engine.Request{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, present := body["chatId"]; present {
		t.Fatalf("body = %#v, want the hidden parameter left out", body)
	}
}

// A declared default may itself be an expression, and it never passes through
// `expression.Resolve` — that only sees parameters the node actually carries.
// Generated packs rely on this: a template that omits the parameter entirely
// must still send the value the pack's default names.
func TestADeclaredDefaultThatIsAnExpressionIsResolved(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	registry := node.NewRegistry()
	definition := packDefinition()
	for index := range definition.Parameters {
		if definition.Parameters[index].Key == "chatId" {
			definition.Parameters[index].Default = map[string]any{"mode": "expression", "value": "{{ $json.from }}"}
		}
		if definition.Parameters[index].Key == "text" {
			definition.Parameters[index].Default = map[string]any{"mode": "expression", "value": "{{ $json.missing }}"}
		}
	}
	if err := registry.Register(definition); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	description := packRouting(server.URL)
	routes := routing.NewRegistry()
	if err := routes.Register(description); err != nil {
		t.Fatalf("routing Register() error = %v", err)
	}
	executor := routing.NewExecutor(localPolicy(), routes, registry)

	if _, err := executor.Execute(context.Background(), packNode(t, registry, map[string]any{
		"resource": "message", "operation": "send",
	}, nil), workflow.NodeInput{"main": {{JSON: map[string]any{"from": "9@c.us"}}}}, engine.Request{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if body["chatId"] != "9@c.us" {
		t.Fatalf("chatId = %#v, want the default resolved from the item", body["chatId"])
	}
	// A default that resolves to nothing is an absent parameter, not a null
	// one: sending `null` to a service that documents a default is worse than
	// sending nothing.
	message, _ := body["message"].(map[string]any)
	if _, present := message["text"]; present {
		t.Fatalf("body = %#v, want an unresolvable default left out entirely", body)
	}
}
