package nodes_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// jsExecutorUnder is the Code (JavaScript) executor under an egress policy.
func jsExecutorUnder(t *testing.T, policy safehttp.Policy) engine.Executor {
	t.Helper()
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, policy, sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, ok := registry.Lookup(nodes.JSCodeExecutorID)
	if !ok {
		t.Fatal("no Code (JavaScript) executor")
	}
	return executor
}

// allowing is the default policy with one exact loopback endpoint granted,
// the narrow way a deployment admits a service beside it: the private
// address guard stays on for everything else.
func allowing(servers ...*httptest.Server) safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	for _, server := range servers {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, strings.TrimPrefix(server.URL, "http://"))
	}
	return policy
}

func runCode(t *testing.T, executor engine.Executor, source string, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	t.Helper()
	if input == nil {
		input = workflow.NodeInput{"main": {{JSON: map[string]any{}}}}
	}
	return executor.Execute(context.Background(), jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": source}), input, request)
}

// The server sends the request, under the deployment's policy, and hands the
// code n8n's answer.
func TestACodeNodesRequestIsSentByTheServerUnderThePolicy(t *testing.T) {
	var seen struct {
		sync.Mutex
		method, query, contentType, body, trace string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.Lock()
		seen.method, seen.query, seen.contentType, seen.body, seen.trace = r.Method, r.URL.RawQuery, r.Header.Get("Content-Type"), string(body), r.Header.Get("X-Trace")
		seen.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		_, _ = w.Write([]byte(`{"echo":"ok"}`))
	}))
	defer server.Close()
	output, err := runCode(t, jsExecutorUnder(t, allowing(server)), strings.Join([]string{
		"const full = await this.helpers.httpRequest({",
		"  method: 'PUT', url: '" + server.URL + "/things', qs: { id: 7 },",
		"  headers: { 'X-Trace': 'abc' }, body: { name: 'kilas' }, returnFullResponse: true,",
		"})",
		"return [{ json: full }]",
	}, "\n"), nil, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := output[0][0].JSON
	if got["statusCode"] != float64(200) || got["statusMessage"] != "OK" || got["body"].(map[string]any)["echo"] != "ok" {
		t.Fatalf("full response = %#v", got)
	}
	headers := got["headers"].(map[string]any)
	if headers["content-type"] != "application/json" || len(headers["set-cookie"].([]any)) != 2 {
		t.Fatalf("headers = %#v, want lower-case names and every cookie", headers)
	}
	seen.Lock()
	defer seen.Unlock()
	if seen.method != "PUT" || seen.query != "id=7" || seen.contentType != "application/json" || seen.body != `{"name":"kilas"}` || seen.trace != "abc" {
		t.Fatalf("the server received %s ?%s %s %s trace=%s", seen.method, seen.query, seen.contentType, seen.body, seen.trace)
	}
}

// The default policy refuses an internal target from code exactly as it does
// from an HTTP Request node, and the code can see why.
func TestTheDefaultPolicyRefusesAnInternalTargetFromCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the internal target was reached")
	}))
	defer server.Close()
	_, err := runCode(t, jsExecutorUnder(t, safehttp.DefaultPolicy()), "await this.helpers.httpRequest({ url: '"+server.URL+"' })\nreturn []", nil, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
		t.Fatalf("Execute() error = %v, want the target refused", err)
	}
	// A different loopback endpoint is still refused under a policy that
	// grants one.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer other.Close()
	_, err = runCode(t, jsExecutorUnder(t, allowing(other)), "await this.helpers.httpRequest({ url: '"+server.URL+"' })\nreturn []", nil, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
		t.Fatalf("Execute() error = %v, want the ungranted endpoint refused", err)
	}
}

// Two requests the code starts together reach the server together.
func TestTwoRequestsFromCodeAreInFlightTogether(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Done()
		done := make(chan struct{})
		go func() { arrived.Wait(); close(done) }()
		select {
		case <-done:
			_, _ = w.Write([]byte(r.URL.Path))
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	defer server.Close()
	output, err := runCode(t, jsExecutorUnder(t, allowing(server)), strings.Join([]string{
		"const answers = await Promise.all(['/a', '/b'].map(path => this.helpers.httpRequest({ url: '" + server.URL + "' + path })))",
		"return [{ json: { answers } }]",
	}, "\n"), nil, engine.Request{})
	if err != nil || strings.Join(toStrings(output[0][0].JSON["answers"]), ",") != "/a,/b" {
		t.Fatalf("Execute() = %#v, %v", output, err)
	}
}

func toStrings(value any) []string {
	var out []string
	for _, element := range value.([]any) {
		out = append(out, element.(string))
	}
	return out
}

func TestAResponseLargerThanThePolicyAllowsIsNamed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
	}))
	defer server.Close()
	policy := allowing(server)
	policy.MaxResponseBytes = 1024
	_, err := runCode(t, jsExecutorUnder(t, policy), "await this.helpers.httpRequest({ url: '"+server.URL+"' })\nreturn []", nil, engine.Request{})
	if !errors.Is(err, jsrun.ErrResponseLimit) {
		t.Fatalf("Execute() error = %v, want the response limit", err)
	}
}

// A file of the node's input is read from the execution's storage, and a new
// one stored there; the node returns it as a file of its own.
func TestACodeNodeReadsAndStoresFiles(t *testing.T) {
	store := binaryStore(t)
	input, err := store.Put("notes.txt", "text/plain", strings.NewReader("hello files"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	output, err := runCode(t, jsExecutor(t, nodes.JSCodeExecutorID), strings.Join([]string{
		"const text = (await this.helpers.getBinaryDataBuffer(0, 'data')).toString()",
		"const shout = await this.helpers.prepareBinaryData(Buffer.from(text.toUpperCase()), 'dir/shout.txt')",
		"const json = await this.helpers.prepareBinaryData(Buffer.from('{}'), 'x.json')",
		"return [{ json: { text, mime: shout.mimeType, jsonMime: json.mimeType, name: shout.fileName }, binary: { shout } }]",
	}, "\n"), workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": input}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0]
	if item.JSON["text"] != "hello files" || item.JSON["name"] != "shout.txt" || item.JSON["mime"] != "text/plain; charset=utf-8" && item.JSON["mime"] != "text/plain" || item.JSON["jsonMime"] != "application/json" {
		t.Fatalf("json = %#v", item.JSON)
	}
	shout := item.Binary["shout"]
	reader, _, err := store.Get(shout.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer reader.Close()
	stored, _ := io.ReadAll(reader)
	if string(stored) != "HELLO FILES" || shout.FileName != "shout.txt" || shout.Size != 11 || !strings.HasPrefix(shout.MediaType, "text/plain") {
		t.Fatalf("stored %q as %#v", stored, shout)
	}
}

// Only the node's own input files are readable: an item it does not have, or
// a file its item does not hold, is refused where the code can see it.
func TestOnlyTheNodesOwnInputFilesAreReadable(t *testing.T) {
	store := binaryStore(t)
	for source, want := range map[string]string{
		"await this.helpers.getBinaryDataBuffer(3, 'data')":  "item 3 is not in this node's input",
		"await this.helpers.getBinaryDataBuffer(0, 'other')": `has no file "other"`,
	} {
		_, err := runCode(t, jsExecutor(t, nodes.JSCodeExecutorID), source+"\nreturn []", nil, engine.Request{Binaries: store})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Execute() error = %v, want %q", source, err, want)
		}
	}
}

// The static data a node run leaves goes into the execution's handle, and
// the next node run in the execution sees it.
func TestStaticDataIsSharedAcrossTheNodeRunsOfAnExecution(t *testing.T) {
	data := engine.NewStaticData(nil)
	request := engine.Request{StaticData: data}
	executor := jsExecutor(t, nodes.JSCodeExecutorID)
	for range 2 {
		if _, err := runCode(t, executor, "const s = $getWorkflowStaticData('global')\ns.n = (s.n || 0) + 1\n$getWorkflowStaticData('node').mine = true\nreturn []", nil, request); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}
	document, changed := data.Changed()
	if !changed || string(document) != `{"global":{"n":2},"node:Code":{"mine":true}}` {
		t.Fatalf("static data = %s (changed %v)", document, changed)
	}
}

// The cap is on the whole document: two kinds each within it may still,
// together, grow it past. The node run that did so fails, and keeps nothing.
func TestStaticDataGrownPastItsCapFailsTheNodeRun(t *testing.T) {
	data := engine.NewStaticData(nil)
	half := jsrun.MaxStaticDataBytes * 3 / 5
	_, err := runCode(t, jsExecutor(t, nodes.JSCodeExecutorID), strings.Join([]string{
		"$getWorkflowStaticData('global').a = 'x'.repeat(" + itoa(half) + ")",
		"$getWorkflowStaticData('node').b = 'y'.repeat(" + itoa(half) + ")",
		"return []",
	}, "\n"), nil, engine.Request{StaticData: data})
	if !errors.Is(err, jsrun.ErrStaticDataLimit) {
		t.Fatalf("Execute() error = %v, want the static data limit", err)
	}
	if _, changed := data.Changed(); changed {
		t.Fatal("the oversized static data was kept")
	}
}
