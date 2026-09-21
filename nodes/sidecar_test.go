package nodes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// The adapter tests below use a fake runner and a fake credential resolver:
// what the executor resolves, what it refuses and what it puts on the wire is
// this package's contract, and pinning it against a real Node process would
// make a marshalling mistake look like a Node problem. The engine-level proofs
// in sidecar_engine_test.go run the real child.

// communityDescription is the node these tests convert: the fixture's greet
// node, with the shapes the adapter has to get right — a default that is an
// expression, a hidden default, a visibility rule and one credential type.
func communityDescription() sidecarnode.PackageInfo {
	return sidecarnode.PackageInfo{
		Name: "kf-node-test", Version: "1.0.0",
		Credentials: []sidecarnode.PackageCredential{{
			File: "dist/credentials/Api.credentials.js", Name: "testApi", DisplayName: "Test API",
			Properties: []map[string]any{
				{"displayName": "API Key", "name": "apiKey", "type": "string", "typeOptions": map[string]any{"password": true}, "default": ""},
				{"displayName": "Base URL", "name": "baseUrl", "type": "string", "default": "https://example.invalid"},
			},
		}},
		Nodes: []sidecarnode.PackageNode{{
			File: "dist/nodes/Greet/Greet.node.js", Name: "fixtureGreet",
			Version: json.RawMessage("1"), Execute: true,
			Description: map[string]any{
				"displayName": "Fixture Greet", "name": "fixtureGreet",
				"group": []any{"transform"}, "version": float64(1),
				"description": "Greets every item", "defaults": map[string]any{"name": "Fixture Greet"},
				"inputs": []any{"main"}, "outputs": []any{"main", "main"},
				"credentials": []any{map[string]any{"name": "testApi", "required": true}},
				"properties": []any{
					map[string]any{"displayName": "Name", "name": "name", "type": "string", "default": "={{ $json.name }}", "required": true},
					map[string]any{"displayName": "Mode", "name": "mode", "type": "options",
						"options": []any{map[string]any{"name": "Plain", "value": "plain"}, map[string]any{"name": "Shout", "value": "shout"}},
						"default": "plain"},
					map[string]any{"displayName": "Filename", "name": "attachmentFilename", "type": "string", "default": "",
						"displayOptions": map[string]any{"show": map[string]any{"mode": []any{"plain"}}}},
					map[string]any{"displayName": "Hidden Value", "name": "hiddenValue", "type": "hidden", "default": "hidden-default"},
				},
			},
		}},
	}
}

const (
	communityType = "sidecar.kf-node-test.fixtureGreet"
	communityNode = "Fixture Greet"
)

// communityCatalogue builds the node catalogue and the sidecar index the way
// composition does, without a Node process.
func communityCatalogue(t *testing.T) (*node.Registry, *sidecarnode.Index) {
	t.Helper()
	converted, _, excluded, err := sidecarnode.Convert(communityDescription())
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(excluded) != 0 {
		t.Fatalf("Convert() excluded %+v, want none", excluded)
	}
	registry := node.NewRegistry()
	for _, one := range converted {
		definition := one.Definition
		definition.SharedSettings = nodes.SidecarSharedSettings()
		if err := registry.RegisterFrom(node.SourceSidecar, definition); err != nil {
			t.Fatalf("RegisterFrom() error = %v", err)
		}
	}
	index := sidecarnode.NewIndex()
	index.Add(converted)
	return registry, index
}

// fakeRunner records what the executor put on the wire and answers with a
// canned result.
type fakeRunner struct {
	mu       sync.Mutex
	requests []sidecar.Request
	outputs  [][]sidecar.Item
	err      error
	calls    int
}

func (runner *fakeRunner) Execute(ctx context.Context, request sidecar.Request) (sidecar.Result, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls++
	runner.requests = append(runner.requests, request)
	if runner.err != nil {
		return sidecar.Result{}, runner.err
	}
	if runner.outputs != nil {
		return sidecar.Result{Items: runner.outputs[0], Outputs: runner.outputs}, nil
	}
	items := make([]sidecar.Item, 0, len(request.Items))
	for range request.Items {
		items = append(items, sidecar.Item{JSON: map[string]any{"ok": true}})
	}
	return sidecar.Result{Items: items, Outputs: [][]sidecar.Item{items, {}}}, nil
}

func (runner *fakeRunner) last() sidecar.Request {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.requests) == 0 {
		return sidecar.Request{}
	}
	return runner.requests[len(runner.requests)-1]
}

// staticResolver answers with a credential per ID, the way a tenant-scoped
// resolver does: it is built for one execution and can only reach that
// execution's credentials.
type staticResolver struct {
	credentials map[string]engine.Credential
}

func (resolver staticResolver) ResolveCredential(_ context.Context, credentialID string) (engine.Credential, error) {
	credential, found := resolver.credentials[credentialID]
	if !found {
		return engine.Credential{}, fmt.Errorf("credential %q does not exist", credentialID)
	}
	return credential, nil
}

func testCredential(credentialID, secret string) engine.Credential {
	return engine.Credential{
		ID: credentialID, Name: credentialID, Type: "testApi",
		Fields: map[string]string{"apiKey": secret, "baseUrl": "https://example.invalid"},
	}
}

// adapterRequest builds the runtime request an execution hands an executor.
func adapterRequest(tenant, credentialID string) engine.Request {
	request := engine.Request{
		Execution: engine.ExecutionContext{TenantID: tenant, ID: "exec-1", WorkflowID: "wf-1", Mode: "manual"},
		Workflow:  expression.WorkflowContext{ID: "wf-1", Name: "Community node", Active: true, Timezone: "UTC"},
	}
	if credentialID != "" {
		request.Credentials = staticResolver{credentials: map[string]engine.Credential{
			credentialID: testCredential(credentialID, "super-secret-key"),
		}}
	}
	return request
}

func fixtureItem(t *testing.T, registry *node.Registry, parameters map[string]any, version workflow.TypeVersion) workflow.IRNode {
	t.Helper()
	definition, found := registry.Lookup(communityType, version)
	if !found {
		t.Fatalf("%s is not in the catalogue", communityType)
	}
	return workflow.IRNode{
		ID: "greet-1", Name: communityNode, Type: communityType, TypeVersion: version,
		Parameters:  parameters,
		Credentials: map[string]string{"testApi": "cred-1"},
		Definition:  definition,
	}
}

func executorFor(t *testing.T, runner nodes.SidecarRunner) (*nodes.SidecarExecutor, *node.Registry) {
	t.Helper()
	registry, index := communityCatalogue(t)
	return nodes.NewSidecarExecutor(runner, index, registry, safehttp.DefaultPolicy(),
		slog.New(slog.NewTextHandler(io.Discard, nil))), registry
}

// TestExecutorMarshalsParamsPerItemAndInput is the marshalling contract: one
// item in, one item out, and the parameters the package reads are the item's
// own.
func TestExecutorMarshalsParamsPerItemAndInput(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "ada"}},
		{JSON: map[string]any{"name": "grace"}},
	}}

	output, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	request := runner.last()
	if got, want := request.Tenant, "tenant-a"; got != want {
		t.Errorf("tenant = %q, want %q", got, want)
	}
	if got, want := request.Node, "fixtureGreet"; got != want {
		t.Errorf("dispatch node = %q, want the package's own name %q", got, want)
	}
	if got, want := request.NodeVersion, float64(1); got != want {
		t.Errorf("dispatch version = %v, want %v", got, want)
	}
	if got, want := len(request.Items), 2; got != want {
		t.Fatalf("items = %d, want %d", got, want)
	}
	if got := request.Items[1].JSON["name"]; got != "grace" {
		t.Errorf("second item = %#v, want the second item's json", request.Items[1].JSON)
	}
	if got, want := len(request.ParamsByItem), 2; got != want {
		t.Fatalf("paramsByItem = %d, want one per item", got)
	}
	if got := request.ParamsByItem[1]["name"]; got != "grace" {
		t.Errorf("second item's name = %#v, want grace", got)
	}
	if request.Context == nil || request.Context.NodeType != communityType || request.Context.WorkflowID != "wf-1" {
		t.Errorf("run context = %+v, want the workflow identity", request.Context)
	}
	// Two declared outputs, two streams back, in order.
	if got, want := len(output), 2; got != want {
		t.Fatalf("output streams = %d, want %d", got, want)
	}
	if got, want := len(output[0]), 2; got != want {
		t.Errorf("first output items = %d, want %d", got, want)
	}
	if got, want := len(output[1]), 0; got != want {
		t.Errorf("second output items = %d, want %d", got, want)
	}
}

// TestExecutorAppliesDefaultsBeforeResolvingExpressions is the order of the
// parameter pipeline: the package's own default is an expression over the item,
// so it has to be filled in first and resolved second.
func TestExecutorAppliesDefaultsBeforeResolvingExpressions(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"name": "ada"}}}}

	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	params := runner.last().ParamsByItem[0]
	if got, want := params["name"], "ada"; got != want {
		t.Errorf("name = %#v, want the resolved default %q", got, want)
	}
	if marker, isMarker := params["name"].(map[string]any); isMarker && marker["mode"] == "expression" {
		t.Errorf("name = %#v, want it resolved before it reaches the package", params["name"])
	}
	// A hidden property is not in the document, but the package reads it.
	if got, want := params["hiddenValue"], "hidden-default"; got != want {
		t.Errorf("hidden value = %#v, want %#v", got, want)
	}
	// A stored parameter wins over the declared default.
	ir = fixtureItem(t, registry, map[string]any{"mode": "shout"}, workflow.V(1))
	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := runner.last().ParamsByItem[0]["mode"]; got != "shout" {
		t.Errorf("mode = %#v, want the stored value", got)
	}
}

// TestExecutorDropsNonVisibleParameters is what keeps a stale value from a
// resource the user switched away from out of the package's hands.
func TestExecutorDropsNonVisibleParameters(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, map[string]any{
		"mode":                 "shout",
		"attachmentFilename":   "stale.txt",
		"attachmentNoteHidden": true,
	}, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"name": "ada"}}}}

	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	params := runner.last().ParamsByItem[0]
	if _, present := params["attachmentFilename"]; present {
		t.Errorf("attachmentFilename = %#v, want a parameter the node does not show dropped", params["attachmentFilename"])
	}
	if got := params["mode"]; got != "shout" {
		t.Errorf("mode = %#v, want the visible value kept", got)
	}
}

// TestExecutorSendsOnlyTheCallingTenantsCredentials is the adapter-level
// tenancy proof: two tenants, two processes, and neither process ever sees the
// other tenant's decrypted secret.
//
// The recorder is keyed by the process the pool started, not by the frame's
// self-reported tenant, so a frame delivered to the wrong process is visible
// even though the frame carries the right tenant string.
func TestExecutorSendsOnlyTheCallingTenantsCredentials(t *testing.T) {
	recorder := newSpawnRecorder()
	pool := sidecar.NewPool(recorder.spawn(), sidecar.DefaultLimits())
	defer pool.Close()

	registry, index := communityCatalogue(t)
	executor := nodes.NewSidecarExecutor(pool, index, registry, safehttp.DefaultPolicy(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{"name": "ada"}}}}

	secrets := map[string]string{"tenant-a": "secret-for-tenant-a", "tenant-b": "secret-for-tenant-b"}
	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-a", "tenant-b"} {
		request := engine.Request{
			Execution: engine.ExecutionContext{TenantID: tenant, ID: "exec-" + tenant},
			Credentials: staticResolver{credentials: map[string]engine.Credential{
				"cred-1": {ID: "cred-1", Name: "testApi", Type: "testApi",
					Fields: map[string]string{"apiKey": secrets[tenant], "baseUrl": "https://example.invalid"}},
			}},
		}
		if _, err := executor.Execute(context.Background(), ir, input, request); err != nil {
			t.Fatalf("Execute(%s) error = %v", tenant, err)
		}
	}

	if got, want := recorder.count(), 2; got != want {
		t.Fatalf("spawns = %d, want %d: one process per tenant", got, want)
	}
	recorder.checkNoCrossTalk(t, secrets)
}

// spawnRecorder records which process the pool started and every byte that
// process received.
type spawnRecorder struct {
	mu     sync.Mutex
	spawns []*recordedSpawn
}

type recordedSpawn struct {
	tenant string
	lines  []string
}

func newSpawnRecorder() *spawnRecorder { return &spawnRecorder{} }

func (recorder *spawnRecorder) spawn() sidecar.SpawnFunc {
	return func(ctx context.Context, tenant, _ string) (*sidecar.Child, error) {
		host, child := net.Pipe()
		recorder.mu.Lock()
		entry := &recordedSpawn{tenant: tenant}
		recorder.spawns = append(recorder.spawns, entry)
		recorder.mu.Unlock()
		go func() {
			defer child.Close()
			reader := bufio.NewReader(child)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				var frame struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				}
				if err := json.Unmarshal([]byte(line), &frame); err != nil {
					return
				}
				recorder.mu.Lock()
				entry.lines = append(entry.lines, strings.TrimSpace(line))
				recorder.mu.Unlock()
				answer, err := json.Marshal(map[string]any{
					"type": "result", "id": frame.ID,
					"outputs": [][]any{
						{map[string]any{"ok": true}},
						[]any{},
					},
				})
				if err != nil {
					return
				}
				if _, err := child.Write(append(answer, '\n')); err != nil {
					return
				}
			}
		}()
		return &sidecar.Child{Conn: host, Kill: func() error {
			_ = host.Close()
			_ = child.Close()
			return nil
		}}, nil
	}
}

// captured returns every raw line the recorder saw, so a test can look for a
// secret anywhere in the stream rather than in one field.
func (recorder *spawnRecorder) captured() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	lines := make([]string, 0)
	for _, spawn := range recorder.spawns {
		lines = append(lines, spawn.lines...)
	}
	return lines
}

func (recorder *spawnRecorder) count() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.spawns)
}

func (recorder *spawnRecorder) checkNoCrossTalk(t *testing.T, secrets map[string]string) {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	sawOwn := map[string]bool{}
	for _, spawn := range recorder.spawns {
		for _, line := range spawn.lines {
			// The spawn's own tenant is the process identity; a frame claiming
			// another tenant's identity arrived in the wrong process.
			if !strings.Contains(line, `"tenant":"`+spawn.tenant+`"`) {
				t.Errorf("process started for %q received a frame not addressed to it: %s", spawn.tenant, line)
			}
			for tenant, secret := range secrets {
				if tenant == spawn.tenant {
					continue
				}
				if strings.Contains(line, secret) {
					t.Errorf("process started for %q received %s's secret", spawn.tenant, tenant)
				}
			}
			if secret, ok := secrets[spawn.tenant]; ok && strings.Contains(line, secret) {
				sawOwn[spawn.tenant] = true
			}
		}
	}
	for tenant := range secrets {
		if !sawOwn[tenant] {
			t.Errorf("the process started for %q never received its own secret", tenant)
		}
	}
}

// TestExecutorRefusesUndeclaredCredentialTypes is why only the declared types
// are resolved: the compiler does not restrict the credential keys a document
// may carry.
func TestExecutorRefusesUndeclaredCredentialTypes(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	ir.Credentials = map[string]string{"testApi": "cred-1", "someOtherApi": "cred-2"}
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	_, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1"))
	if err == nil {
		t.Fatal("Execute() error = nil, want the undeclared credential refused")
	}
	if !strings.Contains(err.Error(), "someOtherApi") {
		t.Errorf("error = %v, want it to name the type the node does not declare", err)
	}
	if runner.calls != 0 {
		t.Errorf("the sidecar was called %d times, want 0: nothing may be sent for a refused run", runner.calls)
	}
}

// TestExecutorFailsClosedWithoutATenant: without a tenant there is no process
// the run's secrets are allowed to enter.
func TestExecutorFailsClosedWithoutATenant(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("", "cred-1")); err == nil {
		t.Fatal("Execute() error = nil, want the tenantless run refused")
	}
	if runner.calls != 0 {
		t.Errorf("the sidecar was called %d times, want 0", runner.calls)
	}
}

// TestExecutorRefusesBinaryInputAndOutput is the attachment rule: refused, not
// dropped, because a package that receives an item without the file it was told
// about sends something the user did not write.
func TestExecutorRefusesBinaryInputAndOutput(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	request := adapterRequest("tenant-a", "cred-1")

	input := workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": {ID: "b1", FileName: "data.bin"}}}}}
	if _, err := executor.Execute(context.Background(), ir, input, request); err == nil {
		t.Error("Execute() error = nil, want the binary input refused")
	}
	if runner.calls != 0 {
		t.Errorf("the sidecar was called %d times for a refused input, want 0", runner.calls)
	}

	runner.outputs = [][]sidecar.Item{
		{{JSON: map[string]any{}, Binary: map[string]any{"data": "b2"}}},
	}
	input = workflow.NodeInput{"main": {{JSON: map[string]any{}}}}
	if _, err := executor.Execute(context.Background(), ir, input, request); err == nil {
		t.Error("Execute() error = nil, want the binary output refused")
	}
}

// TestExecutorScrubsSecretsFromChildErrors: a diagnostic is not a reason to
// move a credential into a log, and a package that puts its key in a URL has it
// echoed by every transport error.
func TestExecutorScrubsSecretsFromChildErrors(t *testing.T) {
	const secret = "super-secret-key"
	runner := &fakeRunner{err: &sidecar.CallError{
		Code: sidecar.CodeSidecarCrash, Node: "fixtureGreet",
		Detail: "sidecar node reported failure: the request to https://example.invalid/?key=" + secret + " failed",
	}}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	_, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1"))
	if err == nil {
		t.Fatal("Execute() error = nil, want the child's failure reported")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error = %v, want the credential value withheld", err)
	}
	if !strings.Contains(err.Error(), communityNode) {
		t.Errorf("error = %v, want it to name the node", err)
	}
}

// TestExecutorMapsOutputsToDeclaredPorts: an answer with fewer streams than the
// node declares is padded, and extra empty ones are dropped.
func TestExecutorMapsOutputsToDeclaredPorts(t *testing.T) {
	runner := &fakeRunner{outputs: [][]sidecar.Item{
		{{JSON: map[string]any{"greeting": "hi"}}},
	}}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	output, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := len(output), 2; got != want {
		t.Fatalf("output streams = %d, want the declared %d", got, want)
	}
	if got := output[0][0].JSON["greeting"]; got != "hi" {
		t.Errorf("first item = %#v, want the child's answer", output[0][0].JSON)
	}
	if len(output[1]) != 0 {
		t.Errorf("second stream = %d items, want it padded empty", len(output[1]))
	}
	// Lineage is the runner's to infer when the counts line up.
	if output[0][0].Paired != nil {
		t.Errorf("paired = %+v, want it left for the runner's positional inference", output[0][0].Paired)
	}
}

// TestExecutorReturnsNoErrorPortStream covers the arithmetic the engine does:
// under continueErrorOutput the error port belongs to the runner, so the
// executor must return one stream fewer than the definition declares.
func TestExecutorReturnsNoErrorPortStream(t *testing.T) {
	runner := &fakeRunner{outputs: [][]sidecar.Item{
		{{JSON: map[string]any{"greeting": "hi"}}},
	}}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	ir.Settings = map[string]any{"onError": "continueErrorOutput"}
	// The compiler appends the error port to the compiled definition.
	ir.Definition.Outputs = append(append([]workflow.Port(nil), ir.Definition.Outputs...),
		workflow.Port{Name: "error", Kind: workflow.ConnectionMain})
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	output, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := len(output), 2; got != want {
		t.Fatalf("output streams = %d, want %d: the error port is the runner's", got, want)
	}

	// Items on a stream past the declared arity are refused rather than
	// silently discarded.
	runner.outputs = [][]sidecar.Item{
		{{JSON: map[string]any{"greeting": "hi"}}},
		{},
		{{JSON: map[string]any{"greeting": "extra"}}},
	}
	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1")); err == nil {
		t.Error("Execute() error = nil, want items on an undeclared stream refused")
	}
}

// TestExecutorReturnsTheContextErrorOnCancel: a run whose context ended reports
// the context, which is what the engine classifies as a cancellation or a
// timeout.
func TestExecutorReturnsTheContextErrorOnCancel(t *testing.T) {
	runner := &fakeRunner{err: &sidecar.CallError{Code: sidecar.CodeCancelled, Detail: "sidecar run was cancelled", Cause: context.Canceled}}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executor.Execute(ctx, ir, input, adapterRequest("tenant-a", "cred-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want the context error", err)
	}
}

// TestExecutorRefusesANodeTheIndexDoesNotKnow keeps a catalogue and an index
// that disagree from sending a run to an unknown node name.
func TestExecutorRefusesANodeTheIndexDoesNotKnow(t *testing.T) {
	runner := &fakeRunner{}
	executor, registry := executorFor(t, runner)
	ir := fixtureItem(t, registry, nil, workflow.V(1))
	ir.TypeVersion = workflow.V(9)
	ir.Definition.Version = workflow.V(9)
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}}}}

	if _, err := executor.Execute(context.Background(), ir, input, adapterRequest("tenant-a", "cred-1")); err == nil {
		t.Fatal("Execute() error = nil, want an unindexed version refused")
	}
	if runner.calls != 0 {
		t.Errorf("the sidecar was called %d times, want 0", runner.calls)
	}
}

// TestEverySidecarDefinitionsExecutorIsRegistered mirrors the built-in binding
// invariant for the sidecar catalogue: a definition naming an executor nobody
// registered fails on the first item of whatever workflow reaches it.
func TestEverySidecarDefinitionsExecutorIsRegistered(t *testing.T) {
	catalogue, executors := composition(t)
	registry, index := communityCatalogue(t)
	for _, definition := range registry.List() {
		if err := catalogue.RegisterFrom(node.SourceSidecar, definition); err != nil {
			t.Fatalf("RegisterFrom() error = %v", err)
		}
	}
	if err := nodes.RegisterSidecarExecutor(executors, &fakeRunner{}, index, registry, safehttp.DefaultPolicy(), nil); err != nil {
		t.Fatalf("RegisterSidecarExecutor() error = %v", err)
	}

	seen := 0
	for _, definition := range catalogue.List() {
		if definition.Source != node.SourceSidecar {
			continue
		}
		seen++
		if _, found := executors.Lookup(definition.ExecutorID); !found {
			t.Errorf("%s names executor %q, which is not registered", definition.Type, definition.ExecutorID)
		}
	}
	if seen == 0 {
		t.Fatal("no sidecar definition was registered")
	}
}
