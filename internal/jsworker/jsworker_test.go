package jsworker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The test binary is its own worker: started with the marker set, it serves
// jobs instead of running tests. The modes stand in for a worker stuck in a
// built-in nothing can interrupt, and one the runtime killed for memory.
const testModeVariable = "JSWORKER_TEST_MODE"

func TestMain(m *testing.M) {
	if IsWorker() {
		switch os.Getenv(testModeVariable) {
		case "hang":
			os.Exit(fakeWorker(func() { time.Sleep(time.Hour) }))
		case "oom":
			os.Exit(fakeWorker(func() {
				fmt.Fprintln(os.Stderr, "fatal error: runtime: out of memory")
				os.Exit(2)
			}))
		case "exit3":
			os.Exit(fakeWorker(func() { os.Exit(3) }))
		case "stale", "unknownfile", "flood", "calls", "bigcall", "badhelper", "staticflood", "staticunused", "bigdone":
			os.Exit(lyingWorker(os.Getenv(testModeVariable)))
		default:
			os.Exit(Serve(os.Stdin, os.Stdout))
		}
	}
	os.Exit(m.Run())
}

// fakeWorker reads the hello and one job the way a worker does, then does
// what the mode says instead of running it.
func fakeWorker(then func()) int {
	reader := bufio.NewReader(os.Stdin)
	if _, _, err := readFrame(reader, maxServerHeader, maxServerBlob); err != nil {
		return 2
	}
	if _, _, err := readFrame(reader, maxServerHeader, maxServerBlob); err != nil {
		return 2
	}
	then()
	return 0
}

// lyingWorker answers its one job with frames a real worker would never
// write: a done frame for another job, naming a file its input did not
// have, printing far past the console cap, handing back static data it was
// never given or results past the output cap; or more helper calls than its
// code may make, one carrying too much, one for a helper there is none of,
// or more questions about static data than there are kinds.
func lyingWorker(mode string) int {
	reader, writer := bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)
	if _, _, err := readFrame(reader, maxServerHeader, maxServerBlob); err != nil {
		return 2
	}
	run, _, err := readFrame(reader, maxServerHeader, maxServerBlob)
	if err != nil {
		return 2
	}
	call := func(id int64, question message, blob string) {
		question.Type, question.Nonce, question.ID = typeCall, run.Nonce, id
		_ = writeFrame(writer, question, blob)
	}
	helper := func(method string) message {
		return message{Method: methodHelper, Request: &jsrun.HostRequest{Method: method, HTTP: &jsrun.HTTPRequest{Method: "GET", URL: "https://example.com"}}}
	}
	output := `[{"json":{"ok":true},"binary":{},"paired":null}]`
	done := message{Type: typeDone, Nonce: run.Nonce, Executed: &jsrun.Executed{}}
	switch mode {
	case "stale":
		done.Nonce = "not-this-job"
	case "unknownfile":
		output = `[{"json":{},"binary":{"data":{"id":"bin_someone_elses"}},"paired":null}]`
	case "flood":
		for index := range 2000 {
			done.Executed.Console = append(done.Executed.Console, jsrun.ConsoleLine{Level: "log", Text: fmt.Sprintf("%04d %s", index, strings.Repeat("x", 1000))})
		}
	case "calls":
		for id := range int64(3) {
			call(id+1, helper(jsrun.HelperHTTPRequest), "")
		}
	case "bigcall":
		call(1, helper(jsrun.HelperWriteFile), strings.Repeat("x", jsrun.MaxFileBytes+1))
	case "badhelper":
		call(1, helper("readAnyFile"), "")
	case "staticflood":
		for id := range int64(3) {
			call(id+1, message{Method: methodStatic, Name: "global"}, "")
		}
	case "staticunused":
		done.Executed.StaticData = map[string]string{"global": "{}"}
	case "bigdone":
		output = `[{"json":{"pad":"` + strings.Repeat("x", 17<<20) + `"},"binary":{},"paired":null}]`
	}
	done.OutputSizes = []int{len(output)}
	if err := writeFrame(writer, done, output); err != nil {
		return 2
	}
	time.Sleep(time.Hour)
	return 0
}

type testPool struct {
	*Pool
	mode   atomic.Value
	starts atomic.Int32
}

func newTestPool(t *testing.T, options Options) *testPool {
	t.Helper()
	pool := &testPool{}
	pool.mode.Store("")
	options.Command = func() *exec.Cmd {
		pool.starts.Add(1)
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(workerEnvironment(jsrun.DefaultHeapCeiling), testModeVariable+"="+pool.mode.Load().(string))
		return cmd
	}
	pool.Pool = New(options)
	t.Cleanup(pool.Close)
	return pool
}

func items(values ...string) []workflow.Item {
	out := make([]workflow.Item, len(values))
	for index, value := range values {
		out[index] = workflow.Item{JSON: map[string]any{"name": value}, Paired: &workflow.PairedItem{SourceNodeID: "webhook", ItemIndex: index}}
	}
	return out
}

// webhookRoots are roots with one earlier node, Webhook, whose items pair
// one to one with the input.
func webhookRoots() jsrun.Roots {
	return jsrun.Roots{
		Workflow: jsrun.WorkflowInfo{ID: "wf_1", Name: "Pipes"},
		Node: func(name string) (jsrun.NodeView, bool) {
			if name != "Webhook" {
				return jsrun.NodeView{}, false
			}
			return jsrun.NodeView{Items: []map[string]any{{"from": "hook-a"}, {"from": "hook-b"}}}, true
		},
		Pair: func(name string, index int) (int, string) { return index, "" },
	}
}

func TestAJobRunsInAWorkerAndTheServerAnswersItsQuestions(t *testing.T) {
	pool := newTestPool(t, Options{})
	result, err := pool.Run(context.Background(), jsrun.Task{
		Mode:  jsrun.ModeEachItem,
		Items: items("a", "b"),
		Roots: webhookRoots(),
		Source: "console.log('item', $itemIndex, $workflow.name)\n" +
			"return { json: { name: $json.name, paired: $('Webhook').item.json.from, first: $('Webhook').first().json.from } }",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var got []string
	for _, item := range result.Items {
		got = append(got, fmt.Sprintf("%v/%v/%v from %s#%d", item.JSON["name"], item.JSON["paired"], item.JSON["first"], item.Paired.SourceNodeID, item.Paired.ItemIndex))
	}
	if want := []string{"a/hook-a/hook-a from webhook#0", "b/hook-b/hook-a from webhook#1"}; !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
	if len(result.Console) != 2 || result.Console[1].Text != "item 1 Pipes" || result.Console[1].Level != "log" {
		t.Fatalf("console = %#v", result.Console)
	}
}

// Whatever runs the code, a user reads the same failure, and the server can
// tell the same failures apart.
func TestAWorkerFailsExactlyAsTheRuntimeDoesInProcess(t *testing.T) {
	limits := jsrun.Limits{Timeout: 150 * time.Millisecond}
	pool := newTestPool(t, Options{Limits: limits})
	local := jsrun.NewRunner(jsrun.Options{Limits: limits})
	sentinels := []error{
		jsrun.ErrTimeLimit, jsrun.ErrMemoryLimit, jsrun.ErrOutputLimit, jsrun.ErrInputLimit, jsrun.ErrHostCallLimit,
		jsrun.ErrCallDepth, jsrun.ErrInvalidReturn, jsrun.ErrNeverSettles, jsrun.ErrUnsupported, jsrun.ErrEngineFault,
	}
	for _, task := range []jsrun.Task{
		{Source: "const x = 1\nthrow new TypeError('bad ' + x)"},
		{Source: "return {"},
		{Source: "const fs = require('fs')\nreturn items"},
		{Source: "while (true) {}"},
		{Source: "return 5"},
		{Source: "await new Promise(() => {})"},
		{Source: "function f() { return f() }\nreturn f()"},
		{Source: "throw 'plain'"},
		{Mode: jsrun.ModeEachItem, Source: "if ($itemIndex === 1) null.x\nreturn { json: {} }"},
		{Source: "return $('Nowhere').first()"},
		{Mode: jsrun.ModeComparator, Source: "return a.json.name > b.json.name"},
		{Mode: jsrun.ModeComparator, Source: "const x = 1\nthrow new TypeError('bad ' + x)"},
	} {
		task.Items = items("a", "b")
		task.Roots = webhookRoots()
		_, want := local.Run(context.Background(), task)
		_, got := pool.Run(context.Background(), task)
		if want == nil || got == nil || got.Error() != want.Error() {
			t.Errorf("%q: worker error %v, in process %v", task.Source, got, want)
			continue
		}
		for _, sentinel := range sentinels {
			if errors.Is(got, sentinel) != errors.Is(want, sentinel) {
				t.Errorf("%q: errors.Is(%v) differs", task.Source, sentinel)
			}
		}
		var gotScript, wantScript *jsrun.ScriptError
		if errors.As(got, &gotScript) != errors.As(want, &wantScript) || (gotScript != nil && !reflect.DeepEqual(*gotScript, *wantScript)) {
			t.Errorf("%q: script error %#v, in process %#v", task.Source, gotScript, wantScript)
		}
		var gotSyntax, wantSyntax *jsrun.SyntaxError
		if errors.As(got, &gotSyntax) != errors.As(want, &wantSyntax) {
			t.Errorf("%q: a syntax error on one side only", task.Source)
		}
	}
	if starts := pool.starts.Load(); starts > 3 {
		t.Errorf("%d workers started for failures the runtime stops itself; they should be reused", starts)
	}
}

// A Sort node's comparator runs in a worker like any other job, and the order
// it puts the items in comes back to the server, which reorders its own items.
func TestAComparatorRunsInAWorker(t *testing.T) {
	pool := newTestPool(t, Options{})
	result, err := pool.Run(context.Background(), jsrun.Task{
		Mode: jsrun.ModeComparator, Items: items("pear", "apple", "fig", "apple"),
		Source: "console.log('compared')\nreturn a.json.name < b.json.name ? -1 : a.json.name > b.json.name ? 1 : 0",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := []int{1, 3, 2, 0}; !slices.Equal(result.Order, want) || result.Items != nil {
		t.Fatalf("order = %v, items = %#v; want %v and no items", result.Order, result.Items, want)
	}
	if len(result.Console) == 0 || result.Console[0].Text != "compared" {
		t.Fatalf("console = %#v, want what the comparator printed", result.Console)
	}
}

// A per-item run that went on past a failed item reports every item's
// outcome, across the pipe as in process.
func TestItemOutcomesCrossTheProcessBoundaryIntact(t *testing.T) {
	pool := newTestPool(t, Options{})
	task := jsrun.Task{
		Mode: jsrun.ModeEachItem, Items: items("a", "b", "c"), ContinueOnItemError: true,
		Source: "if ($json.name === 'b') throw new RangeError('no b')\nreturn { json: { upper: $json.name.toUpperCase() } }",
	}
	want, err := jsrun.NewRunner(jsrun.Options{}).Run(context.Background(), task)
	if err != nil {
		t.Fatalf("in process: %v", err)
	}
	got, err := pool.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(got.Outcomes) != 3 || !reflect.DeepEqual(got.Outcomes, want.Outcomes) {
		t.Fatalf("outcomes = %#v, in process %#v", got.Outcomes, want.Outcomes)
	}
	var script *jsrun.ScriptError
	if !errors.As(got.Outcomes[1].Err(), &script) || script.ItemIndex != 1 || got.Outcomes[2].Items[0].JSON["upper"] != "C" {
		t.Fatalf("outcomes = %#v", got.Outcomes)
	}
}

// A worker is trusted only as far as its code could go: a frame for another
// job, a file its input did not have and a console past its cap are all
// caught in the server, and none is taken for the job's result.
func TestTheServerTrustsAWorkerOnlyAsFarAsItsCodeCouldGo(t *testing.T) {
	pool := newTestPool(t, Options{Limits: jsrun.Limits{MaxConsoleBytes: 4 << 10}})
	task := jsrun.Task{Source: "return items", Items: items("a")}

	pool.mode.Store("stale")
	if _, err := pool.Run(context.Background(), task); !errors.Is(err, jsrun.ErrEngineFault) || !strings.Contains(err.Error(), "broke the protocol: a done frame for another job") {
		t.Errorf("stale frame: Run() error = %v, want the protocol breach named", err)
	}
	pool.mode.Store("unknownfile")
	if _, err := pool.Run(context.Background(), task); !errors.Is(err, jsrun.ErrEngineFault) || !strings.Contains(err.Error(), "naming a file this node was not given") {
		t.Errorf("unknown file: Run() error = %v, want it refused", err)
	}
	pool.mode.Store("flood")
	result, err := pool.Run(context.Background(), task)
	if err != nil || !result.ConsoleTruncated || len(result.Console) > 5 {
		t.Errorf("flood: Run() = %d console lines (truncated %v), %v; want the server's own cap", len(result.Console), result.ConsoleTruncated, err)
	}
	if starts := pool.starts.Load(); starts != 3 {
		t.Errorf("%d workers started, want none trusted again after lying", starts)
	}
}

// A worker that dies says why in the runtime's words, and only the words
// that are true: its exit status, not a memory limit it never reached.
func TestAWorkerThatDiesIsReportedByItsExit(t *testing.T) {
	pool := newTestPool(t, Options{})
	pool.mode.Store("exit3")
	_, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"})
	if !errors.Is(err, jsrun.ErrEngineFault) || errors.Is(err, jsrun.ErrMemoryLimit) || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("Run() error = %v, want the exit status", err)
	}
}

func TestAWorkerIsReusedAcrossJobs(t *testing.T) {
	pool := newTestPool(t, Options{})
	for range 3 {
		if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if starts := pool.starts.Load(); starts != 1 {
		t.Fatalf("%d workers started for three jobs, want one", starts)
	}
}

// The worker's own clock cannot interrupt a built-in; the server's deadline
// can, by killing the worker.
func TestAWorkerStuckPastItsDeadlineIsKilledAndReplaced(t *testing.T) {
	pool := newTestPool(t, Options{Limits: jsrun.Limits{Timeout: 50 * time.Millisecond}, Grace: 200 * time.Millisecond})
	pool.mode.Store("hang")
	start := time.Now()
	_, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"})
	if !errors.Is(err, jsrun.ErrTimeLimit) || err.Error() != "code exceeded its 50ms time limit" {
		t.Fatalf("Run() error = %v, want the time-limit error", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the stuck worker was killed after %v", elapsed)
	}
	pool.mode.Store("")
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")}); err != nil {
		t.Fatalf("the next job failed: %v", err)
	}
	if starts := pool.starts.Load(); starts != 2 {
		t.Fatalf("%d workers started, want the killed one replaced", starts)
	}
}

// A stop signal reaches the workers as well as the server when it goes to the
// whole process group, as systemd and a terminal send it. The server drains;
// a worker must not die under the run it is finishing.
func TestAWorkerRidesOutAStopSignalMeantForTheServer(t *testing.T) {
	pool := newTestPool(t, Options{})
	done := make(chan error, 1)
	go func() {
		_, err := pool.Run(context.Background(), jsrun.Task{Source: "await new Promise((resolve) => setTimeout(resolve, 300))\nreturn items"})
		done <- err
	}()
	// The worker is this test's only child; pgrep finds it without reaching
	// into the pool.
	time.Sleep(100 * time.Millisecond)
	children, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil || len(strings.Fields(string(children))) != 1 {
		t.Fatalf("pgrep = %q, %v; want the one worker", children, err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(children)))
	for _, stop := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
		if err := syscall.Kill(pid, stop); err != nil {
			t.Fatalf("kill -%v error = %v", stop, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want the run to finish", err)
	}
}

func TestCancellingTheExecutionKillsItsWorker(t *testing.T) {
	pool := newTestPool(t, Options{})
	pool.mode.Store("hang")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := pool.Run(ctx, jsrun.Task{Source: "return items"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want the cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cancelling took %v", elapsed)
	}
}

func TestAWorkerThatRanOutOfMemoryIsTheMemoryLimit(t *testing.T) {
	pool := newTestPool(t, Options{HeapCeiling: 256 << 20})
	pool.mode.Store("oom")
	_, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"})
	if !errors.Is(err, jsrun.ErrMemoryLimit) || !strings.Contains(err.Error(), "256 MiB") {
		t.Fatalf("Run() error = %v, want the memory-limit error naming the ceiling", err)
	}
}

func TestTheConcurrencyCapBoundsTheWorkers(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 2})
	var wait sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wait.Go(func() {
			_, err := pool.Run(context.Background(), jsrun.Task{Source: "const until = Date.now() + 100\nwhile (Date.now() < until) {}\nreturn items"})
			errs <- err
		})
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if starts := pool.starts.Load(); starts > 2 {
		t.Fatalf("%d workers started under a cap of two", starts)
	}
}

func TestAnIdleWorkerIsRetiredAndClosingStopsTheRest(t *testing.T) {
	pool := newTestPool(t, Options{IdleTimeout: 100 * time.Millisecond})
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	idle := slices.Clone(pool.idle)
	pool.mu.Unlock()
	if len(idle) != 1 {
		t.Fatalf("%d idle workers, want one", len(idle))
	}
	select {
	case <-idle[0].exited:
	case <-time.After(3 * time.Second):
		t.Fatal("the idle worker was never retired")
	}

	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatal(err)
	}
	pool.mu.Lock()
	idle = slices.Clone(pool.idle)
	pool.mu.Unlock()
	pool.Close()
	select {
	case <-idle[0].exited:
	case <-time.After(3 * time.Second):
		t.Fatal("closing the pool left a worker running")
	}
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); !errors.Is(err, jsrun.ErrEngineFault) {
		t.Fatalf("Run() after Close = %v, want it refused", err)
	}
}

// A worker runs code written by workflow authors, so it is told nothing the
// server knows.
func TestAWorkersEnvironmentHoldsNoneOfTheServers(t *testing.T) {
	t.Setenv("KILASFLOW_AUTH_SECRET", "s3cret")
	t.Setenv("KILASFLOW_DATABASE_URL", "postgres://user:pass@db/app")
	t.Setenv("TZ", "Asia/Jakarta")
	env := defaultCommand(512 << 20).Env
	if env == nil {
		t.Fatal("the default command inherits the server's environment")
	}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains([]string{markerVariable, "GOMAXPROCS", "GOMEMLIMIT", "GOTRACEBACK", "TZ", "ZONEINFO"}, name) {
			t.Errorf("a worker is given %s", name)
		}
	}
	if !slices.Contains(env, "TZ=Asia/Jakarta") || !slices.Contains(env, markerVariable+"=1") {
		t.Errorf("env = %q, want the marker and the server's time zone", env)
	}
}

func TestAFrameLargerThanItsLimitIsRefusedBeforeItIsRead(t *testing.T) {
	var buffer strings.Builder
	writer := bufio.NewWriter(&buffer)
	if err := writeFrame(writer, message{Type: typeDone}, strings.Repeat("x", 100)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(strings.NewReader(buffer.String()))
	var violation *protocolError
	if _, _, err := readFrame(reader, 1<<20, 10); !errors.As(err, &violation) {
		t.Fatalf("readFrame() error = %v, want the frame refused", err)
	}
}
