package jsworker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// serverHelpers is the server's half of the helpers a job's code calls: it
// answers from memory, and slow makes each request take that long.
type serverHelpers struct {
	mu      sync.Mutex
	slow    time.Duration
	barrier *sync.WaitGroup
	urls    []string
	stored  int
}

func (helpers *serverHelpers) HTTPRequest(ctx context.Context, request jsrun.HTTPRequest, body []byte) (jsrun.HTTPResponse, []byte, error) {
	helpers.mu.Lock()
	helpers.urls = append(helpers.urls, request.URL)
	helpers.mu.Unlock()
	if helpers.barrier != nil {
		helpers.barrier.Done()
		arrived := make(chan struct{})
		go func() { helpers.barrier.Wait(); close(arrived) }()
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			return jsrun.HTTPResponse{}, nil, errors.New("the other request never arrived")
		}
	}
	select {
	case <-time.After(helpers.slow):
	case <-ctx.Done():
		return jsrun.HTTPResponse{}, nil, ctx.Err()
	}
	return jsrun.HTTPResponse{StatusCode: 200, StatusMessage: "OK", Headers: map[string]any{"content-type": "text/plain"}}, []byte("echo " + request.URL + " " + string(body)), nil
}

func (helpers *serverHelpers) ReadFile(_ context.Context, itemIndex int, property string) ([]byte, error) {
	if itemIndex != 0 || property != "data" {
		return nil, fmt.Errorf("item %d has no file %q", itemIndex, property)
	}
	return []byte("file bytes"), nil
}

func (helpers *serverHelpers) WriteFile(_ context.Context, data []byte, fileName, mimeType string) (workflow.BinaryRef, error) {
	helpers.mu.Lock()
	defer helpers.mu.Unlock()
	helpers.stored++
	return workflow.BinaryRef{ID: fmt.Sprintf("bin_new_%d", helpers.stored), FileName: fileName, MediaType: mimeType, Size: int64(len(data))}, nil
}

func (helpers *serverHelpers) StaticData(kind string) (string, error) {
	if kind == "global" {
		return `{"runs":41}`, nil
	}
	return "{}", nil
}

// Every helper crosses the pipe: the server makes the request, reads and
// stores the files and hands out the static data, and a stored file the
// code returns is decoded as the node's own.
func TestHelpersCrossTheProcessBoundary(t *testing.T) {
	pool := newTestPool(t, Options{})
	input := []workflow.Item{{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": {ID: "bin_in", FileName: "in.txt", Size: 10}}}}
	result, err := pool.Run(context.Background(), jsrun.Task{
		Items: input,
		Roots: jsrun.Roots{Helpers: &serverHelpers{}},
		Source: strings.Join([]string{
			"const body = await this.helpers.httpRequest({ method: 'POST', url: 'https://example.com/x', body: 'hi' })",
			"const read = (await this.helpers.getBinaryDataBuffer(0, 'data')).toString()",
			"const file = await this.helpers.prepareBinaryData(Buffer.from(read), 'copy.txt', 'text/plain')",
			"$getWorkflowStaticData('global').runs += 1",
			"return [{ json: { body, read }, binary: { copy: file } }]",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	item := result.Items[0]
	if item.JSON["body"] != "echo https://example.com/x hi" || item.JSON["read"] != "file bytes" {
		t.Fatalf("json = %#v", item.JSON)
	}
	if copied := item.Binary["copy"]; copied.ID != "bin_new_1" || copied.FileName != "copy.txt" || copied.Size != 10 {
		t.Fatalf("binary = %#v, want the stored file", item.Binary)
	}
	if result.StaticData["global"] != `{"runs":42}` {
		t.Fatalf("static data = %v", result.StaticData)
	}
}

// A worker does not wait for one request to finish before it sends the
// next: neither server call returns until both have arrived.
func TestAWorkersRequestsAreInFlightTogether(t *testing.T) {
	pool := newTestPool(t, Options{})
	var barrier sync.WaitGroup
	barrier.Add(2)
	result, err := pool.Run(context.Background(), jsrun.Task{
		Roots: jsrun.Roots{Helpers: &serverHelpers{barrier: &barrier}},
		Source: strings.Join([]string{
			"const [a, b] = await Promise.all([",
			"  this.helpers.httpRequest({ url: 'https://a.example.com' }),",
			"  this.helpers.httpRequest({ url: 'https://b.example.com' }),",
			"])",
			"return [{ json: { a, b } }]",
		}, "\n"),
	})
	if err != nil || result.Items[0].JSON["b"] != "echo https://b.example.com " {
		t.Fatalf("Run() = %#v, %v", result.Items, err)
	}
}

// The worker is killed past its deadline only for time it spent itself: a
// request the server takes its time over does not count.
func TestWaitingOnTheServerDoesNotCountAgainstTheDeadline(t *testing.T) {
	pool := newTestPool(t, Options{Limits: jsrun.Limits{Timeout: 100 * time.Millisecond}, Grace: 100 * time.Millisecond})
	result, err := pool.Run(context.Background(), jsrun.Task{
		Roots:  jsrun.Roots{Helpers: &serverHelpers{slow: 700 * time.Millisecond}},
		Source: "const body = await this.helpers.httpRequest({ url: 'https://slow.example.com' })\nreturn [{ json: { body } }]",
	})
	if err != nil || result.Items[0].JSON["body"] != "echo https://slow.example.com " {
		t.Fatalf("Run() = %#v, %v; want the slow request to finish", result.Items, err)
	}
}

// Cancelling the execution stops the server's half of a request too.
func TestCancellingTheExecutionStopsItsRequests(t *testing.T) {
	pool := newTestPool(t, Options{})
	helpers := &serverHelpers{slow: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := pool.Run(ctx, jsrun.Task{
		Roots:  jsrun.Roots{Helpers: helpers},
		Source: "await this.helpers.httpRequest({ url: 'https://never.example.com' })\nreturn []",
	})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("Run() error = %v after %v, want the cancellation", err, time.Since(start))
	}
}

// A worker that asks more of the server than its code could have is not
// running the runtime, and is never used again.
func TestAWorkerThatOverstepsTheHelpersIsRetired(t *testing.T) {
	pool := newTestPool(t, Options{Limits: jsrun.Limits{MaxHostCalls: 2}})
	task := jsrun.Task{Source: "return items", Items: items("a"), Roots: jsrun.Roots{Helpers: &serverHelpers{}}}
	for mode, want := range map[string]string{
		"calls":        "more than its 2 helper calls",
		"bigcall":      "past the",
		"badhelper":    `a helper "readAnyFile"`,
		"staticflood":  "more than 2 questions about static data",
		"staticunused": "static data of kind \"global\", which the code was never given",
		"bigdone":      "results larger than",
	} {
		pool.mode.Store(mode)
		if _, err := pool.Run(context.Background(), task); !errors.Is(err, jsrun.ErrEngineFault) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Run() error = %v, want it to say %q", mode, err, want)
		}
	}
}

// The worker's end refuses a reply to a call it never made, or one for
// another job, and ends: the stream is not what the server writes.
func TestAWorkerRefusesARepliesItDidNotAskFor(t *testing.T) {
	for name, forge := range map[string]func(call message) message{
		"no such call": func(call message) message { return message{Type: typeReply, Nonce: call.Nonce, ID: call.ID + 7} },
		"another job":  func(call message) message { return message{Type: typeReply, Nonce: "someone-else", ID: call.ID} },
		"a second run": func(call message) message { return message{Type: typeRun, Nonce: "again", Job: &jsrun.Job{}} },
	} {
		t.Run(name, func(t *testing.T) {
			// Serve says why it stopped on stderr, which the pool logs; here
			// it would only clutter the test's output.
			stderr := os.Stderr
			os.Stderr, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			t.Cleanup(func() { os.Stderr.Close(); os.Stderr = stderr })
			toWorker, fromServer := io.Pipe()
			fromWorker, toServer := io.Pipe()
			exit := make(chan int, 1)
			go func() { exit <- Serve(toWorker, toServer); toServer.Close() }()
			server, worker := bufio.NewWriter(fromServer), bufio.NewReader(fromWorker)
			limits := jsrun.DefaultLimits()
			if err := writeFrame(server, message{Type: typeHello, Limits: &limits}, ""); err != nil {
				t.Fatal(err)
			}
			job, _, err := jsrun.NewRunner(jsrun.Options{}).Prepare(jsrun.Task{Source: "return [{ json: { page: await this.helpers.httpRequest({ url: 'https://example.com' }) } }]"})
			if err != nil {
				t.Fatal(err)
			}
			if err := writeFrame(server, message{Type: typeRun, Nonce: "job-1", Job: &job}, job.Input); err != nil {
				t.Fatal(err)
			}
			call, _, err := readFrame(worker, 1<<20, 1<<20)
			if err != nil || call.Type != typeCall || call.Method != methodHelper {
				t.Fatalf("readFrame() = %#v, %v; want the helper call", call, err)
			}
			if err := writeFrame(server, forge(call), ""); err != nil {
				t.Fatal(err)
			}
			go func() { _, _ = io.Copy(io.Discard, fromWorker) }()
			select {
			case code := <-exit:
				if code != 2 {
					t.Fatalf("Serve() = %d, want the broken-stream exit", code)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the worker went on after a forged frame")
			}
			fromServer.Close()
		})
	}
}
