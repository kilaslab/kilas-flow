package jsrun_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Tests run under jsrun.DefaultLimits, and tighten a single limit through the
// task, the way a node's own setting does. A limit widened for a test would
// hide a default that is wrong for a real user on the same machine.

func newRunner() *jsrun.Runner {
	return jsrun.NewRunner(jsrun.Options{})
}

func itemsOf(values ...map[string]any) []workflow.Item {
	items := make([]workflow.Item, len(values))
	for index, value := range values {
		items[index] = workflow.Item{JSON: value}
	}
	return items
}

func numbered(count int) []workflow.Item {
	items := make([]workflow.Item, count)
	for index := range items {
		items[index] = workflow.Item{JSON: map[string]any{"n": float64(index)}}
	}
	return items
}

func runAll(t *testing.T, runner *jsrun.Runner, source string, items []workflow.Item) (jsrun.Result, error) {
	t.Helper()
	return runner.Run(context.Background(), jsrun.Task{Source: source, Items: items})
}

func mustRun(t *testing.T, runner *jsrun.Runner, task jsrun.Task) jsrun.Result {
	t.Helper()
	result, err := runner.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

// stoppedPromptly runs a task that must hit a limit, and reports the error
// and how long the run took to return once its interrupt was delivered.
func stoppedPromptly(t *testing.T, runner *jsrun.Runner, task jsrun.Task) (error, time.Duration) {
	t.Helper()
	var delivered atomic.Int64
	runner.OnInterruptForTest(func(at time.Time) { delivered.CompareAndSwap(0, at.UnixNano()) })
	_, err := runner.Run(context.Background(), task)
	returned := time.Now()
	if delivered.Load() == 0 {
		t.Fatalf("Run() error = %v, but no interrupt was delivered", err)
	}
	return err, returned.Sub(time.Unix(0, delivered.Load()))
}

func TestCodeTransformsItemsAndReturnsThem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return items.map(item => ({ json: { doubled: item.json.n * 2 } }))",
		Items:  numbered(3),
	})
	if len(result.Items) != 3 || result.Items[2].JSON["doubled"] != float64(4) {
		t.Fatalf("items = %#v, want three doubled items", result.Items)
	}
}

func TestTopLevelAwaitAndAReturnedPromiseAreAwaited(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "const value = await Promise.resolve(41)\nreturn Promise.resolve([{ json: { value: value + 1 } }])",
	})
	if len(result.Items) != 1 || result.Items[0].JSON["value"] != float64(42) {
		t.Fatalf("items = %#v, want one item with value 42", result.Items)
	}
}

func TestAnInfiniteLoopStopsAtTheTimeLimit(t *testing.T) {
	err, latency := stoppedPromptly(t, newRunner(), jsrun.Task{
		Source: "while (true) {}",
		Limits: jsrun.Limits{Timeout: 100 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) || !strings.Contains(err.Error(), "100ms time limit") {
		t.Fatalf("Run() error = %v, want the 100ms time limit", err)
	}
	if latency > interruptTolerance {
		t.Fatalf("the run returned %v after its interrupt, want within %v", latency, time.Duration(interruptTolerance))
	}
}

func TestALoopInsideAPromiseJobStopsAtTheTimeLimit(t *testing.T) {
	err, latency := stoppedPromptly(t, newRunner(), jsrun.Task{
		Source: "await Promise.resolve().then(() => { while (true) {} })\nreturn []",
		Limits: jsrun.Limits{Timeout: 100 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want the time limit", err)
	}
	if latency > interruptTolerance {
		t.Fatalf("the run returned %v after its interrupt, want within %v", latency, time.Duration(interruptTolerance))
	}
}

func TestALoopAfterAnAwaitedHostCallStopsAtTheTimeLimit(t *testing.T) {
	runner := newRunner()
	runner.BindAsyncForTest("slowly", func(ctx context.Context, _ []any) (any, error) {
		time.Sleep(20 * time.Millisecond)
		return "done", nil
	})
	err, latency := stoppedPromptly(t, runner, jsrun.Task{
		Source: "await slowly()\nwhile (true) {}",
		Limits: jsrun.Limits{Timeout: 150 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want the time limit", err)
	}
	if latency > interruptTolerance {
		t.Fatalf("the run returned %v after its interrupt, want within %v", latency, time.Duration(interruptTolerance))
	}
}

// A host function settles its promise from Go. The continuation waiting on it
// must run as part of that settlement, before the code's next line.
func TestResolvingFromGoRunsTheContinuationImmediately(t *testing.T) {
	runner := newRunner()
	var received []any
	runner.BindAsyncForTest("fetchValue", func(_ context.Context, arguments []any) (any, error) {
		received = arguments
		time.Sleep(10 * time.Millisecond)
		return map[string]any{"value": "x"}, nil
	})
	result := mustRun(t, runner, jsrun.Task{Source: strings.Join([]string{
		"const order = []",
		"const pending = fetchValue('a', 2).then(answer => order.push('then:' + answer.value))",
		"await pending",
		"order.push('after')",
		"return [{ json: { order } }]",
	}, "\n")})
	order := fmt.Sprint(result.Items[0].JSON["order"])
	if order != "[then:x after]" {
		t.Fatalf("order = %s, want the continuation to run before the next line", order)
	}
	if fmt.Sprint(received) != "[a 2]" {
		t.Fatalf("the host function received %v, want [a 2]", received)
	}
}

// goja reports a regular expression's match timeout as "no match". If that
// timeout fired before the script's own limit, a catastrophic pattern would
// quietly return false and the script would carry on with a wrong answer. The
// match timeout covers the limit, so the script is stopped instead.
func TestABacktrackingRegexEndsInATimeoutNeverANoMatch(t *testing.T) {
	// The runner comes first: building one raises the match timeout to cover
	// its ceiling, which would undo the shorter one this test waits for.
	runner := newRunner()
	restore := jsrun.SetMatchTimeoutForTest(400 * time.Millisecond)
	defer restore()
	start := time.Now()
	result, err := runner.Run(context.Background(), jsrun.Task{
		Source: "return [{ json: { matched: /(?=a)(a+)+$/.test('a'.repeat(34) + '!') } }]",
		Limits: jsrun.Limits{Timeout: 150 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() = %#v, %v; want the time limit, never a result", result.Items, err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the run took %v; the match should have timed out at 400ms", elapsed)
	}
}

// Loading a library is setup, which the user is not charged for. lodash takes
// longer than this limit to load on a slow machine or under the race
// detector, so this passes only if loading it is outside the limit.
func TestTheTimeLimitIsNotSpentOnSetup(t *testing.T) {
	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.Limits{Timeout: 50 * time.Millisecond}})
	runner.PreloadForTest("lodash")
	for run := range 3 {
		result, err := runAll(t, runner, "const _ = require('lodash')\nreturn [{ json: { chunks: _.chunk([1, 2, 3, 4, 5], 2).length } }]", numbered(200))
		if err != nil {
			t.Fatalf("run %d: Run() error = %v", run, err)
		}
		if result.Items[0].JSON["chunks"] != float64(3) {
			t.Fatalf("run %d: items = %#v, want lodash's answer", run, result.Items)
		}
	}
}

func TestPerItemModeSpendsOneBudgetAcrossItems(t *testing.T) {
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "const until = Date.now() + 40\nwhile (Date.now() < until) {}\nreturn $json",
		Mode:   jsrun.ModeEachItem,
		Items:  numbered(10),
		Limits: jsrun.Limits{Timeout: 150 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want the time limit shared by all items", err)
	}
	if len(result.Items) == 0 || len(result.Items) >= 10 {
		t.Fatalf("%d items finished, want some but not all of them within one budget", len(result.Items))
	}
}

func TestTheClockDoesNotRunBetweenItems(t *testing.T) {
	runner := newRunner()
	runner.PauseBetweenItemsForTest(func() { time.Sleep(30 * time.Millisecond) })
	start := time.Now()
	result := mustRun(t, runner, jsrun.Task{
		Source: "return $json",
		Mode:   jsrun.ModeEachItem,
		Items:  numbered(8),
		Limits: jsrun.Limits{Timeout: 100 * time.Millisecond},
	})
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("the run took %v; the pauses alone should take 210ms", elapsed)
	}
	if len(result.Items) != 8 || result.UserTime >= 100*time.Millisecond {
		t.Fatalf("items = %d, user time = %v; want all 8 items and less than the limit charged", len(result.Items), result.UserTime)
	}
}

func TestAPromiseThatCanNeverSettleIsANamedError(t *testing.T) {
	_, err := runAll(t, newRunner(), "await new Promise(() => {})\nreturn []", nil)
	if !errors.Is(err, jsrun.ErrNeverSettles) {
		t.Fatalf("Run() error = %v, want ErrNeverSettles", err)
	}
}

func TestCancellingTheExecutionStopsTheScript(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := newRunner().Run(ctx, jsrun.Task{Source: "while (true) {}"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want the cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond+interruptTolerance {
		t.Fatalf("the run took %v to notice its cancellation", elapsed)
	}
}

func TestTheHeapWatchdogStopsARunawayAllocation(t *testing.T) {
	runtime.GC()
	ceiling := jsrun.HeapObjectBytesForTest() + 96<<20
	runner := jsrun.NewRunner(jsrun.Options{HeapCeiling: ceiling})
	// The chunk is built once: goja's repeat writes a character at a time,
	// which the race detector slows to seconds for a loop of them, while a
	// concatenation is one copy. The hoard reaches the ceiling in a couple of
	// thousand steps, long before the time limit on a loaded machine.
	_, err := runAll(t, runner, "const chunk = 'x'.repeat(1 << 16)\nconst hoard = []\nwhile (true) hoard.push(chunk + hoard.length)", nil)
	if !errors.Is(err, jsrun.ErrMemoryLimit) {
		t.Fatalf("Run() error = %v, want the memory limit", err)
	}
	// The process, and the runner, carry on.
	runtime.GC()
	result, err := runAll(t, runner, "return [{ json: { ok: true } }]", nil)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("a run after the watchdog fired: %#v, %v", result.Items, err)
	}
}

func TestOtherExecutionsSurviveTheWatchdog(t *testing.T) {
	var pressure atomic.Bool
	restore := jsrun.SetHeapReaderForTest(func() uint64 {
		if pressure.Load() {
			return 1 << 40
		}
		return 0
	})
	defer restore()
	runner := newRunner()

	stopped := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), jsrun.Task{Source: "while (true) {}"})
		stopped <- err
	}()
	time.Sleep(30 * time.Millisecond)
	pressure.Store(true)
	select {
	case err := <-stopped:
		if !errors.Is(err, jsrun.ErrMemoryLimit) {
			t.Fatalf("the runaway run: error = %v, want the memory limit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog did not stop the runaway run")
	}
	pressure.Store(false)

	result, err := runAll(t, runner, "return [{ json: { ok: true } }]", nil)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("the next run: %#v, %v; want it to run normally", result.Items, err)
	}
}

func TestRecursionBeyondTheCallDepthIsANamedError(t *testing.T) {
	_, err := runAll(t, newRunner(), "function dive(depth) { return dive(depth + 1) + 1 }\ntry { dive(0) } catch (error) { return [{ json: { caught: true } }] }", nil)
	if !errors.Is(err, jsrun.ErrCallDepth) {
		t.Fatalf("Run() error = %v, want ErrCallDepth", err)
	}
}

func TestInputOverTheCapIsRefusedBeforeAnyVMExists(t *testing.T) {
	before := jsrun.VMsCreatedForTest()
	_, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "return items",
		Items:  itemsOf(map[string]any{"text": strings.Repeat("x", 2048)}),
		Limits: jsrun.Limits{MaxInputBytes: 1024},
	})
	if !errors.Is(err, jsrun.ErrInputLimit) {
		t.Fatalf("Run() error = %v, want ErrInputLimit", err)
	}
	if created := jsrun.VMsCreatedForTest() - before; created != 0 {
		t.Fatalf("%d VMs were created for input that was refused", created)
	}
}

func TestOutputOverTheCapIsRefusedWithoutBuildingTheWholeString(t *testing.T) {
	start := time.Now()
	_, err := newRunner().Run(context.Background(), jsrun.Task{
		// 200 items that share one 1 MiB string: cheap to hold, 200 MiB as JSON.
		Source: "const text = 'x'.repeat(1 << 20)\nreturn Array.from({ length: 200 }, () => ({ json: { text } }))",
		Limits: jsrun.Limits{MaxOutputBytes: 1 << 20},
	})
	if !errors.Is(err, jsrun.ErrOutputLimit) {
		t.Fatalf("Run() error = %v, want ErrOutputLimit", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("refusing the output took %v; it should stop as soon as the limit is passed", elapsed)
	}
}

func TestInvalidReturnsAreNamedErrors(t *testing.T) {
	cases := map[string]string{
		"return":                    "returned nothing",
		"return 42":                 "returned a number",
		"return [1]":                "item 0 is a number",
		"return [{ json: [1, 2] }]": "json that is a list",
		"return [{ json: 'text' }]": "json that is a string",
	}
	for source, want := range cases {
		_, err := runAll(t, newRunner(), source, nil)
		if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error = %v, want ErrInvalidReturn saying %q", source, err, want)
		}
	}
}

func TestErrorsReportTheUsersOwnLine(t *testing.T) {
	_, err := runAll(t, newRunner(), "const a = 1\nconst b = null\nreturn b.missing", nil)
	var script *jsrun.ScriptError
	if !errors.As(err, &script) {
		t.Fatalf("Run() error = %v, want a ScriptError", err)
	}
	if script.Name != "TypeError" || script.Line != 3 || !strings.HasSuffix(err.Error(), "[line 3]") {
		t.Fatalf("error = %q (name %q, line %d), want a TypeError on line 3", err, script.Name, script.Line)
	}
}

func TestPerItemErrorsNameTheItem(t *testing.T) {
	_, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "if ($itemIndex === 2) {\n  throw new Error('bad item')\n}\nreturn $json",
		Mode:   jsrun.ModeEachItem,
		Items:  numbered(3),
	})
	if err == nil || err.Error() != "Error: bad item [line 2, for item 2]" {
		t.Fatalf("Run() error = %v, want the line and the item", err)
	}
}

func TestAThrownNonErrorIsReportedAsItsString(t *testing.T) {
	_, err := runAll(t, newRunner(), "throw 'boom'", nil)
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || script.Message != "boom" || script.Name != "" {
		t.Fatalf("Run() error = %#v, want the thrown string", err)
	}
}

func TestASyntaxErrorReportsLineAndColumn(t *testing.T) {
	for _, check := range []func(string) error{
		func(source string) error { _, err := jsrun.Analyze(source, jsrun.ModeAllItems); return err },
		func(source string) error { _, err := runAll(t, newRunner(), source, nil); return err },
	} {
		err := check("const a = 1\nconst b = ;")
		var syntax *jsrun.SyntaxError
		if !errors.As(err, &syntax) || syntax.Line != 2 || syntax.Column <= 0 {
			t.Fatalf("error = %#v, want a syntax error on line 2 with its column", err)
		}
	}
}

// Code that balances its own brackets to close the function it runs in, and
// reopen another, would run its middle part while the wrapper is set up, off
// the clock. Its shape gives it away, whatever the text looks like.
func TestCodeThatClosesItsWrapperIsRefused(t *testing.T) {
	escape := "}).call(this); }); while (true) {} (function () { return (async function () {"
	start := time.Now()
	_, err := runAll(t, newRunner(), escape, nil)
	var syntax *jsrun.SyntaxError
	if !errors.As(err, &syntax) || !strings.Contains(err.Error(), "closes the function it runs in") {
		t.Fatalf("Run() error = %v, want the wrapper escape refused", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("refusing took %v; the escaped loop must never run", elapsed)
	}
}

// goja reads a file named by a trailing sourceMappingURL comment, in the
// body, in eval and in new Function. A FIFO makes such a read hang, so a run
// that returns proves nothing was read.
func TestASourceMapCommentNeverReadsTheFilesystem(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "sourcemap")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	for _, source := range []string{
		"return []\n//# sourceMappingURL=" + fifo,
		"eval('1\\n//# sourceMappingURL=" + fifo + "')\nreturn []",
		"new Function('return 1\\n//# sourceMappingURL=" + fifo + "')()\nreturn []",
	} {
		done := make(chan error, 1)
		go func() {
			_, err := runAll(t, newRunner(), source, nil)
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%q: Run() error = %v", source, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%q: the run hung reading its source map from the filesystem", source)
		}
	}
}

func TestEveryExecutionGetsAFreshVM(t *testing.T) {
	runner := newRunner()
	mustRun(t, runner, jsrun.Task{Source: "Array.prototype.sneaky = 1\nglobalThis.leak = 42\nObject.prototype.polluted = true\nreturn []"})
	result := mustRun(t, runner, jsrun.Task{Source: "return [{ json: { leak: typeof leak, sneaky: typeof [].sneaky, polluted: typeof ({}).polluted } }]"})
	if got := fmt.Sprint(result.Items[0].JSON); got != "map[leak:undefined polluted:undefined sneaky:undefined]" {
		t.Fatalf("the second execution saw %s, want nothing the first one left behind", got)
	}
}

func TestACompiledProgramIsSharedAcrossRuns(t *testing.T) {
	source := fmt.Sprintf("return [{ json: { run: %d } }]", time.Now().UnixNano())
	runner := newRunner()
	hitsBefore, missesBefore := jsrun.ProgramCacheStatsForTest()
	mustRun(t, runner, jsrun.Task{Source: source})
	mustRun(t, jsrun.NewRunner(jsrun.Options{}), jsrun.Task{Source: source})
	hits, misses := jsrun.ProgramCacheStatsForTest()
	if misses-missesBefore != 1 || hits-hitsBefore != 1 {
		t.Fatalf("hits +%d, misses +%d; want the second run, on another runner, to reuse the first's program", hits-hitsBefore, misses-missesBefore)
	}
}

func TestTheConcurrencyCapQueuesRatherThanFails(t *testing.T) {
	runner := jsrun.NewRunner(jsrun.Options{MaxConcurrent: 1})
	source := "const until = Date.now() + 60\nwhile (Date.now() < until) {}\nreturn [{ json: {} }]"
	start := time.Now()
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := runner.Run(context.Background(), jsrun.Task{Source: source})
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a queued run failed: %v", err)
		}
	}
	// Date.now() has millisecond granularity, so each run can end a little
	// early; two at once would take about 60ms in all.
	if elapsed := time.Since(start); elapsed < 110*time.Millisecond {
		t.Fatalf("two 60ms runs finished in %v with one slot; they ran at once", elapsed)
	}
}

func benchmarkRun(b *testing.B, mode jsrun.Mode, count int) {
	runner := newRunner()
	source := "return items.map(item => ({ json: { ...item.json, total: (item.json.n ?? 0) * 2 } }))"
	if mode == jsrun.ModeEachItem {
		source = "return { json: { ...$json, total: ($json.n ?? 0) * 2 } }"
	}
	task := jsrun.Task{Source: source, Mode: mode, Items: numbered(count)}
	b.ResetTimer()
	for range b.N {
		if _, err := runner.Run(context.Background(), task); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAllItems10(b *testing.B)   { benchmarkRun(b, jsrun.ModeAllItems, 10) }
func BenchmarkAllItems1000(b *testing.B) { benchmarkRun(b, jsrun.ModeAllItems, 1000) }
func BenchmarkEachItem10(b *testing.B)   { benchmarkRun(b, jsrun.ModeEachItem, 10) }
func BenchmarkEachItem1000(b *testing.B) { benchmarkRun(b, jsrun.ModeEachItem, 1000) }

// The early stop in stringify counts a lower bound, so output that fits is
// never refused because its size was overestimated: 3,000 small items come
// to about 90 KB of JSON, under a 100 KB limit.
func TestOutputUnderTheCapIsNeverRefusedByTheEstimate(t *testing.T) {
	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "return Array.from({ length: 3000 }, () => ({ json: { a: 1, b: 2, c: 3 } }))",
		Limits: jsrun.Limits{MaxOutputBytes: 100_000},
	})
	if err != nil || len(result.Items) != 3000 {
		t.Fatalf("Run() = %d items, %v; want all 3000 under the limit", len(result.Items), err)
	}
}
