package jsrun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// fakeHelpers stands in for the node's server half of this.helpers: it
// records what the code asked for and answers from the test's functions.
type fakeHelpers struct {
	mu       sync.Mutex
	requests []jsrun.HTTPRequest
	bodies   []string
	http     func(ctx context.Context, request jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error)
	files    map[string][]byte
	stored   []workflow.BinaryRef
	static   map[string]string
	panics   bool
}

func (fake *fakeHelpers) HTTPRequest(ctx context.Context, request jsrun.HTTPRequest, body []byte) (jsrun.HTTPResponse, []byte, error) {
	if fake.panics {
		panic("a bug in a helper")
	}
	fake.mu.Lock()
	fake.requests = append(fake.requests, request)
	fake.bodies = append(fake.bodies, string(body))
	fake.mu.Unlock()
	if fake.http != nil {
		return fake.http(ctx, request)
	}
	return jsrun.HTTPResponse{StatusCode: 200, StatusMessage: "OK", Headers: map[string]any{"content-type": "application/json"}}, []byte(`{"ok":true}`), nil
}

func (fake *fakeHelpers) ReadFile(_ context.Context, itemIndex int, property string) ([]byte, error) {
	data, ok := fake.files[fmt.Sprintf("%d/%s", itemIndex, property)]
	if !ok {
		return nil, fmt.Errorf("item %d has no file %q", itemIndex, property)
	}
	return data, nil
}

func (fake *fakeHelpers) WriteFile(_ context.Context, data []byte, fileName, mimeType string) (workflow.BinaryRef, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	stored := workflow.BinaryRef{ID: fmt.Sprintf("bin_stored_%d", len(fake.stored)), FileName: fileName, MediaType: mimeType, Size: int64(len(data))}
	fake.stored = append(fake.stored, stored)
	return stored, nil
}

func (fake *fakeHelpers) StaticData(kind string) (string, error) {
	if text, ok := fake.static[kind]; ok {
		return text, nil
	}
	return "{}", nil
}

func withHelpers(helpers jsrun.Helpers) jsrun.Roots { return jsrun.Roots{Helpers: helpers} }

func helperRun(t *testing.T, helpers jsrun.Helpers, source string) (jsrun.Result, error) {
	t.Helper()
	return newRunner().Run(context.Background(), jsrun.Task{Source: source, Roots: withHelpers(helpers)})
}

// helperJSON runs source with helpers and returns its first item's JSON.
func helperJSON(t *testing.T, helpers jsrun.Helpers, source string) map[string]any {
	t.Helper()
	result, err := helperRun(t, helpers, source)
	return firstJSON(t, result, err)
}

func firstJSON(t *testing.T, result jsrun.Result, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("Run() returned no items")
	}
	return result.Items[0].JSON
}

// httpRequest is a promise the server settles. The request the code
// described reaches the server whole: the query in the URL, the headers in
// order, an object body as JSON.
func TestHTTPRequestHandsTheServerTheRequestTheCodeDescribed(t *testing.T) {
	fake := &fakeHelpers{}
	got := helperJSON(t, fake, strings.Join([]string{
		"const pending = this.helpers.httpRequest({",
		"  method: 'post', baseURL: 'https://api.example.com/v1/', url: '/items',",
		"  qs: { page: 2, tags: ['a', 'b'], skip: null },",
		"  headers: { 'X-Trace': 'one' }, body: { name: 'x' }, json: true, timeout: 1500,",
		"  unknownOption: 'ignored as n8n ignores it',",
		"})",
		"const isPromise = pending instanceof Promise",
		"const answer = await pending",
		"return [{ json: { isPromise, answer } }]",
	}, "\n"))
	if got["isPromise"] != true || fmt.Sprint(got["answer"]) != "map[ok:true]" {
		t.Fatalf("got %#v, want a promise of the parsed JSON body", got)
	}
	request := fake.requests[0]
	if request.Method != "POST" || request.URL != "https://api.example.com/v1/items?page=2&tags%5B%5D=a&tags%5B%5D=b" {
		t.Errorf("request = %s %s", request.Method, request.URL)
	}
	if fmt.Sprint(request.Headers) != "[[X-Trace one] [Accept application/json] [Content-Type application/json]]" {
		t.Errorf("headers = %v", request.Headers)
	}
	if fake.bodies[0] != `{"name":"x"}` || request.TimeoutMS != 1500 || request.Redirects != nil {
		t.Errorf("body = %q, timeout = %d, redirects = %v", fake.bodies[0], request.TimeoutMS, request.Redirects)
	}
}

// What comes back is shaped as n8n's helper shapes it.
func TestHTTPRequestAnswersInN8nsShapes(t *testing.T) {
	fake := &fakeHelpers{http: func(_ context.Context, request jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		status := 200
		if strings.Contains(request.URL, "missing") {
			status = 404
		}
		return jsrun.HTTPResponse{StatusCode: status, StatusMessage: "Status", Headers: map[string]any{"x-id": "7"}}, []byte("plain text"), nil
	}}
	got := helperJSON(t, fake, strings.Join([]string{
		"const full = await this.helpers.httpRequest({ url: 'https://example.com', returnFullResponse: true })",
		"const bytes = await this.helpers.httpRequest({ url: 'https://example.com', encoding: 'arraybuffer', disableFollowRedirect: true })",
		"let failure",
		"try { await this.helpers.httpRequest({ url: 'https://example.com/missing' }) } catch (error) { failure = { message: error.message, status: error.status, data: error.response.data } }",
		"const ignored = await this.helpers.httpRequest({ url: 'https://example.com/missing', ignoreHttpStatusErrors: true })",
		"return [{ json: { full, isBuffer: Buffer.isBuffer(bytes), text: bytes.toString(), failure, ignored } }]",
	}, "\n"))
	if fmt.Sprint(got["full"]) != "map[body:plain text headers:map[x-id:7] statusCode:200 statusMessage:Status]" {
		t.Errorf("full response = %v", got["full"])
	}
	if got["isBuffer"] != true || got["text"] != "plain text" || got["ignored"] != "plain text" {
		t.Errorf("got %#v", got)
	}
	if fmt.Sprint(got["failure"]) != "map[data:plain text message:The request failed with status 404 Status status:404]" {
		t.Errorf("failure = %v", got["failure"])
	}
	if redirects := fake.requests[1].Redirects; redirects == nil || *redirects != 0 {
		t.Errorf("disableFollowRedirect sent redirects = %v, want 0", redirects)
	}
}

// An option that would change what the request does, and that this server
// does not honour, is refused in the one sentence rather than ignored.
func TestAnHTTPOptionThisServerDoesNotHonourIsRefused(t *testing.T) {
	for _, option := range []string{"proxy: { host: 'proxy.internal', port: 3128 }", "skipSslCertificateValidation: true", "encoding: 'stream'"} {
		_, err := helperRun(t, &fakeHelpers{}, "await this.helpers.httpRequest({ url: 'https://example.com', "+option+" })\nreturn []")
		if err == nil || !strings.Contains(err.Error(), "which this server does not run") {
			t.Errorf("%s: Run() error = %v, want the refusal", option, err)
		}
	}
}

// Two requests started together are in flight together: neither server
// call returns until both have arrived.
func TestRequestsStartedTogetherAreInFlightTogether(t *testing.T) {
	var arrived sync.WaitGroup
	arrived.Add(2)
	fake := &fakeHelpers{http: func(ctx context.Context, request jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		arrived.Done()
		done := make(chan struct{})
		go func() { arrived.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return jsrun.HTTPResponse{}, nil, errors.New("the other request never arrived")
		}
		return jsrun.HTTPResponse{StatusCode: 200}, []byte(request.URL), nil
	}}
	got := helperJSON(t, fake, strings.Join([]string{
		"const answers = await Promise.all([",
		"  this.helpers.httpRequest({ url: 'https://a.example.com' }),",
		"  this.helpers.httpRequest({ url: 'https://b.example.com' }),",
		"])",
		"return [{ json: { answers } }]",
	}, "\n"))
	if fmt.Sprint(got["answers"]) != "[https://a.example.com https://b.example.com]" {
		t.Fatalf("answers = %v", got["answers"])
	}
}

// While the server answers, the code runs on: a timer fires, and the wait
// is not charged to the code's time.
func TestTheCodeRunsOnWhileTheServerAnswersAndTheWaitIsFree(t *testing.T) {
	fake := &fakeHelpers{http: func(context.Context, jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		time.Sleep(700 * time.Millisecond)
		return jsrun.HTTPResponse{StatusCode: 200}, []byte("late"), nil
	}}
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: strings.Join([]string{
			"const order = []",
			"const pending = this.helpers.httpRequest({ url: 'https://example.com' }).then(body => order.push(body))",
			"await new Promise(resolve => setTimeout(resolve, 5))",
			"order.push('timer')",
			"await pending",
			"return [{ json: { order } }]",
		}, "\n"),
		Roots:  withHelpers(fake),
		Limits: jsrun.Limits{Timeout: 400 * time.Millisecond},
	})
	got := firstJSON(t, result, err)
	if fmt.Sprint(got["order"]) != "[timer late]" {
		t.Fatalf("order = %v, want the timer to fire while the request was out", got["order"])
	}
	if result.UserTime >= 400*time.Millisecond {
		t.Fatalf("UserTime = %v, want the wait for the server left out", result.UserTime)
	}
}

// Code that goes on computing once the server has answered is still held to
// its time limit.
func TestALoopAfterAnAwaitedHelperStopsAtTheTimeLimit(t *testing.T) {
	runner := newRunner()
	fake := &fakeHelpers{http: func(context.Context, jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		time.Sleep(20 * time.Millisecond)
		return jsrun.HTTPResponse{StatusCode: 200}, nil, nil
	}}
	err, latency := stoppedPromptly(t, runner, jsrun.Task{
		Source: "await this.helpers.httpRequest({ url: 'https://example.com' })\nwhile (true) {}",
		Roots:  withHelpers(fake),
		Limits: jsrun.Limits{Timeout: 150 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want the time limit", err)
	}
	if latency > interruptTolerance {
		t.Fatalf("the run returned %v after its interrupt, want within %v", latency, time.Duration(interruptTolerance))
	}
}

// A settled request runs the code waiting on it before the code's next line.
func TestASettledRequestRunsItsContinuationFirst(t *testing.T) {
	got := helperJSON(t, &fakeHelpers{}, strings.Join([]string{
		"const order = []",
		"const pending = this.helpers.httpRequest({ url: 'https://example.com' }).then(answer => order.push('then:' + answer.ok))",
		"await pending",
		"order.push('after')",
		"return [{ json: { order } }]",
	}, "\n"))
	if fmt.Sprint(got["order"]) != "[then:true after]" {
		t.Fatalf("order = %v, want the continuation to run before the next line", got["order"])
	}
}

func TestEveryHelperCallCountsAgainstTheHostCallLimit(t *testing.T) {
	fake := &fakeHelpers{files: map[string][]byte{"0/data": []byte("x")}}
	_, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: strings.Join([]string{
			"await this.helpers.httpRequest({ url: 'https://example.com' })",
			"await this.helpers.getBinaryDataBuffer(0, 'data')",
			"await this.helpers.prepareBinaryData(Buffer.from('y'))",
			"return []",
		}, "\n"),
		Roots:  withHelpers(fake),
		Limits: jsrun.Limits{MaxHostCalls: 2},
	})
	if !errors.Is(err, jsrun.ErrHostCallLimit) {
		t.Fatalf("Run() error = %v, want the host-call limit", err)
	}
	if len(fake.stored) != 0 {
		t.Fatalf("the call past the limit still reached the server: %v", fake.stored)
	}
}

// A request that fails rejects the promise, which the code may catch; a
// named limit stops the code, whatever it catches.
func TestAFailedRequestRejectsAndALimitStops(t *testing.T) {
	fake := &fakeHelpers{http: func(_ context.Context, request jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		if strings.Contains(request.URL, "huge") {
			return jsrun.HTTPResponse{}, nil, jsrun.ResponseLimitError(8 << 20)
		}
		return jsrun.HTTPResponse{}, nil, errors.New("request target is not allowed")
	}}
	got := helperJSON(t, fake, "try { await this.helpers.httpRequest({ url: 'http://10.0.0.1' }) } catch (error) { return [{ json: { message: error.message } }] }")
	if got["message"] != "request target is not allowed" {
		t.Fatalf("message = %v", got["message"])
	}
	_, err := helperRun(t, fake, "try { await this.helpers.httpRequest({ url: 'https://example.com/huge' }) } catch (error) {}\nreturn []")
	if !errors.Is(err, jsrun.ErrResponseLimit) {
		t.Fatalf("Run() error = %v, want the response limit", err)
	}
}

// A panic in a helper is outside every guard on the VM's goroutine; it must
// reject the call, never end the server.
func TestAPanickingHelperFailsTheCallNotTheServer(t *testing.T) {
	_, err := helperRun(t, &fakeHelpers{panics: true}, "await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn []")
	if err == nil || !strings.Contains(err.Error(), "the helper failed (a bug in a helper)") {
		t.Fatalf("Run() error = %v, want the panic turned into a failed call", err)
	}
	got := helperJSON(t, &fakeHelpers{panics: true}, "try { await this.helpers.httpRequest({ url: 'https://example.com' }) } catch (error) { return [{ json: { caught: error.message.includes('helper failed') } }] }")
	if got["caught"] != true {
		t.Fatalf("got %#v, want the code able to catch it", got)
	}
}

func TestWithoutHelpersEveryHelperSaysItIsUnavailable(t *testing.T) {
	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: "await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn []"})
	if err == nil || !strings.Contains(err.Error(), "this.helpers.httpRequest is not available here") {
		t.Fatalf("Run() error = %v", err)
	}
}

// A file of the node's input is read by the server; a file the code stores
// comes back as a reference the node may return, and nothing else may be.
func TestFilesRoundTripThroughTheServer(t *testing.T) {
	fake := &fakeHelpers{files: map[string][]byte{"0/data": []byte("hello")}}
	input := []workflow.Item{{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": {ID: "bin_input", FileName: "in.txt", MediaType: "text/plain", Size: 5}}}}
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: strings.Join([]string{
			"const bytes = await this.helpers.getBinaryDataBuffer(0, 'data')",
			"const file = await this.helpers.prepareBinaryData(Buffer.from(bytes.toString().toUpperCase()), 'out.txt', 'text/plain')",
			"return [{ json: { read: bytes.toString(), file }, binary: { data: file, original: $input.first().binary.data } }]",
		}, "\n"),
		Items: input, Roots: withHelpers(fake),
	})
	got := firstJSON(t, result, err)
	if got["read"] != "hello" || fmt.Sprint(got["file"]) != "map[fileExtension:txt fileName:out.txt fileSize:5 id:bin_stored_0 mimeType:text/plain]" {
		t.Fatalf("got %#v", got)
	}
	binary := result.Items[0].Binary
	if binary["data"].ID != "bin_stored_0" || binary["data"].Size != 5 || binary["original"].ID != "bin_input" {
		t.Fatalf("binary = %#v, want the stored file and the input's", binary)
	}
	if fake.stored[0].FileName != "out.txt" {
		t.Fatalf("stored = %#v", fake.stored)
	}
	_, err = newRunner().Run(context.Background(), jsrun.Task{
		Source: "return [{ json: {}, binary: { data: { id: 'bin_stored_0', fileName: 'x' } } }]",
		Roots:  withHelpers(fake),
	})
	if !errors.Is(err, jsrun.ErrInvalidReturn) {
		t.Fatalf("Run() error = %v, want a file stored by another run refused", err)
	}
}

func TestAFileLargerThanOneCallMayMoveIsNamed(t *testing.T) {
	fake := &fakeHelpers{}
	_, err := helperRun(t, fake, fmt.Sprintf("await this.helpers.prepareBinaryData(Buffer.alloc(%d))\nreturn []", jsrun.MaxFileBytes+1))
	if !errors.Is(err, jsrun.ErrFileLimit) {
		t.Fatalf("Run() error = %v, want the file limit", err)
	}
	if len(fake.stored) != 0 {
		t.Fatal("the oversized file reached the server")
	}
	fake.files = map[string][]byte{"0/data": bytes.Repeat([]byte("x"), jsrun.MaxFileBytes+1)}
	_, err = helperRun(t, fake, "await this.helpers.getBinaryDataBuffer(0, 'data')\nreturn []")
	if !errors.Is(err, jsrun.ErrFileLimit) {
		t.Fatalf("Run() error = %v, want the file limit on a read", err)
	}
}

// $getWorkflowStaticData hands out one object per kind for the whole run,
// and what it holds when the code finishes comes back for the node to keep.
func TestStaticDataIsOneObjectPerKindAndComesBackAfterTheRun(t *testing.T) {
	fake := &fakeHelpers{static: map[string]string{"global": `{"count":1}`}}
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: strings.Join([]string{
			"const state = $getWorkflowStaticData('global')",
			"state.count += 1",
			"const same = state === $getWorkflowStaticData('global')",
			"$getWorkflowStaticData('node').seen = $json.n",
			"return { json: { same, count: state.count } }",
		}, "\n"),
		Mode: jsrun.ModeEachItem, Items: numbered(3), Roots: withHelpers(fake),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Items[2].JSON["count"] != float64(4) || result.Items[2].JSON["same"] != true {
		t.Fatalf("items = %#v, want one object shared by every item", result.Items)
	}
	want := map[string]string{"global": `{"count":4}`, "node": `{"seen":2}`}
	if fmt.Sprint(result.StaticData) != fmt.Sprint(want) {
		t.Fatalf("StaticData = %v, want %v", result.StaticData, want)
	}
}

func TestStaticDataComesBackOnlyFromASuccessfulRun(t *testing.T) {
	result, err := helperRun(t, &fakeHelpers{}, "$getWorkflowStaticData('global').touched = true\nthrow new Error('no')")
	if err == nil || result.StaticData != nil {
		t.Fatalf("Run() = %v, %v; want the failed run's static data dropped", result.StaticData, err)
	}
}

func TestStaticDataTakesOnlyGlobalOrNode(t *testing.T) {
	_, err := helperRun(t, &fakeHelpers{}, "$getWorkflowStaticData('workflow')\nreturn []")
	if err == nil || !strings.Contains(err.Error(), "$getWorkflowStaticData takes 'global' or 'node', not 'workflow'") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestStaticDataPastItsCapIsNamed(t *testing.T) {
	_, err := helperRun(t, &fakeHelpers{}, fmt.Sprintf("$getWorkflowStaticData('global').blob = 'x'.repeat(%d)\nreturn []", jsrun.MaxStaticDataBytes))
	if !errors.Is(err, jsrun.ErrStaticDataLimit) {
		t.Fatalf("Run() error = %v, want the static data limit", err)
	}
}

// The data crosses as JSON whatever the code put in it.
func TestStaticDataCrossesAsJSON(t *testing.T) {
	result, err := helperRun(t, &fakeHelpers{}, "$getWorkflowStaticData('global').at = { list: [1, 'two'], none: undefined }\nreturn []")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.StaticData["global"]), &decoded); err != nil || fmt.Sprint(decoded) != "map[at:map[list:[1 two]]]" {
		t.Fatalf("StaticData = %v (%v)", result.StaticData, err)
	}
}

// In "Run once for each item" mode every item has the whole budget, as the
// code runs once per item; in "Run once for all items" mode the run has it.
func TestTheHostCallBudgetIsPerItemInPerItemMode(t *testing.T) {
	limits := jsrun.Limits{MaxHostCalls: 2}
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "const a = await this.helpers.httpRequest({ url: 'https://example.com' })\n" +
			"const b = await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn { json: { ok: a.ok && b.ok } }",
		Mode: jsrun.ModeEachItem, Items: numbered(5), Roots: withHelpers(&fakeHelpers{}), Limits: limits,
	})
	if err != nil || len(result.Items) != 5 {
		t.Fatalf("Run() = %d items, %v; want every item within its own budget", len(result.Items), err)
	}
	_, err = newRunner().Run(context.Background(), jsrun.Task{
		Source: "for (const item of items) await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn items",
		Items:  numbered(3), Roots: withHelpers(&fakeHelpers{}), Limits: limits,
	})
	if !errors.Is(err, jsrun.ErrHostCallLimit) {
		t.Fatalf("all items: Run() error = %v, want the host-call limit for the run", err)
	}
	_, err = newRunner().Run(context.Background(), jsrun.Task{
		Source: "for (let i = 0; i < 3; i++) await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn { json: {} }",
		Mode:   jsrun.ModeEachItem, Items: numbered(2), Roots: withHelpers(&fakeHelpers{}), Limits: limits,
	})
	if !errors.Is(err, jsrun.ErrHostCallLimit) {
		t.Fatalf("one item past its budget: Run() error = %v, want the host-call limit", err)
	}
}
