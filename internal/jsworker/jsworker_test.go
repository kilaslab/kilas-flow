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
	"strings"
	"sync"
	"sync/atomic"
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
		if !slices.Contains([]string{markerVariable, "GOMAXPROCS", "GOMEMLIMIT", "TZ", "ZONEINFO"}, name) {
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
	if _, _, err := readFrame(reader, 1<<20, 10); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("readFrame() error = %v, want the frame refused", err)
	}
}
