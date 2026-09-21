package nodes_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// The tests here run the real runner and the real package: the Relay fixture
// calls this.helpers.httpRequest exactly the way the owner's own nodes do, so
// what they prove is that a community node's outbound HTTP is the host's
// request under the host's policy and not the package's own socket.
//
// They are the criterion-6 proof. The loopback test server is granted through
// Policy.AllowedPrivateEndpoints — the narrow mechanism — never by turning on
// AllowPrivateNetworks.

// relayFixture is the Relay node running against a real sidecar process.
type relayFixture struct {
	executor *nodes.SidecarExecutor
	ir       workflow.IRNode
}

func relayFixtureFor(t *testing.T, policy safehttp.Policy) relayFixture {
	t.Helper()
	nodePath := sidecartest.Node(t)
	runnerPath, err := sidecar.ExtractRunner(t.TempDir())
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	limits := sidecar.DefaultLimits()
	limits.Timeout = 30 * time.Second
	limits.SpawnTimeout = 30 * time.Second
	spawn := sidecar.RunnerSpawn(sidecar.RunnerConfig{
		NodePath: nodePath, RunnerPath: runnerPath,
		PackagesDir: sidecartest.PackagesDir(t), Packages: []string{"kf-fixture-nodes"},
		RuntimeDir: t.TempDir(),
	})
	pool := sidecar.NewPool(spawn, limits)
	t.Cleanup(pool.Close)

	registry := node.NewRegistry()
	index, err := sidecarnode.Load(context.Background(), sidecarnode.LoadDeps{
		Spawn: spawn, Limits: limits, Definitions: registry, Credentials: credentials.NewRegistry(),
		SharedSettings: nodes.SidecarSharedSettings(),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	definition, found := registry.Lookup("sidecar.kf-fixture-nodes.fixtureRelay", workflow.V(1))
	if !found {
		t.Fatal("the relay fixture is not in the catalogue")
	}
	return relayFixture{
		executor: nodes.NewSidecarExecutor(pool, index, registry, policy, slog.New(slog.NewTextHandler(io.Discard, nil))),
		ir: workflow.IRNode{
			ID: "relay-1", Name: "Fixture Relay", Type: "sidecar.kf-fixture-nodes.fixtureRelay",
			TypeVersion: workflow.V(1), Parameters: map[string]any{"path": "/echo"},
			Credentials: map[string]string{"fixtureApi": "cred-1"}, Definition: definition,
		},
	}
}

// run sends one item through the Relay node with the given credential.
func (fixture relayFixture) run(t *testing.T, baseURL string, allowedDomains []string) (workflow.NodeOutput, error) {
	t.Helper()
	request := engine.Request{
		Execution: engine.ExecutionContext{TenantID: "tenant-a", ID: "exec-1", WorkflowID: "wf-1", Mode: "manual"},
		Workflow:  expression.WorkflowContext{ID: "wf-1", Name: "Relay", Active: true, Timezone: "UTC"},
		Credentials: staticResolver{credentials: map[string]engine.Credential{
			"cred-1": {
				ID: "cred-1", Name: "fixtureApi", Type: "fixtureApi", AllowedDomains: allowedDomains,
				Fields: map[string]string{"apiKey": "fixture-api-key", "baseUrl": baseURL},
			},
		}},
	}
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"hello": "world"}}}}
	return fixture.executor.Execute(context.Background(), fixture.ir, input, request)
}

// loopbackEgressPolicy grants exactly the servers a test uses.
func loopbackEgressPolicy(servers ...*httptest.Server) safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	for _, server := range servers {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, strings.TrimPrefix(server.URL, "http://"))
	}
	return policy
}

// echoServer answers with the request body and the headers it saw.
func echoServer(t *testing.T) (*httptest.Server, *recordedRequests) {
	t.Helper()
	recorded := &recordedRequests{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		recorded.record(request)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"echoed": decoded})
	}))
	t.Cleanup(server.Close)
	return server, recorded
}

type recordedRequests struct {
	mu       sync.Mutex
	requests []*http.Request
	headers  []map[string]string
}

func (recorded *recordedRequests) record(request *http.Request) {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	headers := map[string]string{}
	for name, values := range request.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	recorded.requests = append(recorded.requests, request)
	recorded.headers = append(recorded.headers, headers)
}

func (recorded *recordedRequests) count() int {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	return len(recorded.requests)
}

func (recorded *recordedRequests) lastHeaders() map[string]string {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	if len(recorded.headers) == 0 {
		return nil
	}
	return recorded.headers[len(recorded.headers)-1]
}

// TestEgressGoesThroughSafehttp is the round trip: the package asked for a
// request, the host made it through the deployment's client, and the answer
// came back through the same socket.
func TestEgressGoesThroughSafehttp(t *testing.T) {
	server, recorded := echoServer(t)
	fixture := relayFixtureFor(t, loopbackEgressPolicy(server))

	output, err := fixture.run(t, server.URL, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := len(output[0]), 1; got != want {
		t.Fatalf("output items = %d, want %d", got, want)
	}
	relayed, _ := output[0][0].JSON["relayed"].(map[string]any)
	if relayed["hello"] != "world" {
		t.Errorf("relayed = %#v, want the echoed body", output[0][0].JSON["relayed"])
	}
	if got := output[0][0].JSON["transport"]; got != "host" {
		t.Errorf("transport = %#v, want the host's answer", got)
	}
	if got, want := recorded.count(), 1; got != want {
		t.Fatalf("the test server saw %d requests, want %d", got, want)
	}
	headers := recorded.lastHeaders()
	// The credential the package read reached the request: the host forwarded
	// the header the package set, it did not replay a secret of its own.
	if headers["x-api-key"] != "fixture-api-key" {
		t.Errorf("x-api-key = %q, want the header the package set", headers["x-api-key"])
	}
	if !strings.Contains(headers["content-type"], "application/json") {
		t.Errorf("content-type = %q, want the package's own", headers["content-type"])
	}
}

// TestEgressRefusesAnotherLoopbackPort is the SSRF guard: the deployment
// granted one endpoint, and a request to another port on the same host is
// refused before it is made.
func TestEgressRefusesAnotherLoopbackPort(t *testing.T) {
	granted, grantedRecorded := echoServer(t)
	other, otherRecorded := echoServer(t)
	fixture := relayFixtureFor(t, loopbackEgressPolicy(granted))

	_, err := fixture.run(t, other.URL, nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want a request to an ungranted endpoint refused")
	}
	if otherRecorded.count() != 0 {
		t.Errorf("the ungranted endpoint was reached %d times, want 0", otherRecorded.count())
	}
	if grantedRecorded.count() != 0 {
		t.Errorf("the granted endpoint was reached %d times, want 0", grantedRecorded.count())
	}
}

// TestEgressRefusesAHostOutsideTheCredentialsAllowedDomains is the credential
// scope: a credential scoped to one API never leaves it.
func TestEgressRefusesAHostOutsideTheCredentialsAllowedDomains(t *testing.T) {
	server, recorded := echoServer(t)
	fixture := relayFixtureFor(t, loopbackEgressPolicy(server))

	_, err := fixture.run(t, server.URL, []string{"api.example.com"})
	if err == nil {
		t.Fatal("Execute() error = nil, want a host outside the credential's domains refused")
	}
	if !strings.Contains(err.Error(), "api.example.com") && !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want it to say why the host was refused", err)
	}
	if recorded.count() != 0 {
		t.Errorf("the test server was reached %d times, want 0", recorded.count())
	}

	// The credential's own host is allowed, so the refusal above is the scope
	// and not a broken fixture.
	if _, err := fixture.run(t, server.URL, []string{"127.0.0.1"}); err != nil {
		t.Fatalf("Execute() error = %v, want the credential's own host allowed", err)
	}
	if got, want := recorded.count(), 1; got != want {
		t.Errorf("the test server saw %d requests, want %d", got, want)
	}
}

// TestEgressResponseBodyIsCapped is the output bound reaching the package: the
// host refuses to buffer an unbounded answer, and the child learns why.
func TestEgressResponseBodyIsCapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer server.Close()

	policy := loopbackEgressPolicy(server)
	policy.MaxResponseBytes = 64
	fixture := relayFixtureFor(t, policy)

	_, err := fixture.run(t, server.URL, nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want an oversized answer refused")
	}
	// The handler declares the code (response-too-large) on the http.error
	// frame; sidecar/hostcall_test.go pins that frame, and what the child's own
	// error carries into the trace is the message.
	if !strings.Contains(err.Error(), "larger than this deployment allows") {
		t.Errorf("error = %v, want the host's own diagnostic", err)
	}
}

// TestEgressSendsTheCredentialToThePackageOnlyThroughTheHost pins the direction
// of the secret: the package reads the credential from the run's own fields and
// puts it on the request itself, and the host forwards what it sent.
func TestEgressSendsTheCredentialToThePackageOnlyThroughTheHost(t *testing.T) {
	server, recorded := echoServer(t)
	fixture := relayFixtureFor(t, loopbackEgressPolicy(server))

	if _, err := fixture.run(t, server.URL, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	headers := recorded.lastHeaders()
	if _, leaked := headers["authorization"]; leaked {
		t.Errorf("the host added an authorization header of its own: %v", headers)
	}
}
