package jsrun

import (
	"context"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
)

// BareGlobalsForTest lists the globals of an untouched goja VM, so a test can
// name exactly what the runtime adds.
func BareGlobalsForTest() []string {
	names, _ := goja.New().RunString("Object.getOwnPropertyNames(globalThis)")
	var out []string
	for _, name := range names.Export().([]any) {
		out = append(out, name.(string))
	}
	return out
}

// BindAsyncForTest installs an asynchronous host function in every VM the
// runner creates, the way a node hands the runtime a capability.
func (runner *Runner) BindAsyncForTest(name string, fn func(context.Context, []any) (any, error)) {
	runner.testHost = func(v *vm) { _ = v.bindAsync(name, fn) }
}

// PauseBetweenItemsForTest runs pause between the items of per-item mode,
// where the clock is stopped.
func (runner *Runner) PauseBetweenItemsForTest(pause func()) { runner.betweenItems = pause }

// PreloadForTest loads libraries whatever the body's analysis says.
func (runner *Runner) PreloadForTest(names ...string) { runner.forcedLibrary = names }

// OnInterruptForTest records when an interrupt is delivered.
func (runner *Runner) OnInterruptForTest(record func(time.Time)) { runner.onInterrupt = record }

// VMsCreatedForTest counts VMs created so far in this process.
func VMsCreatedForTest() int64 { return vmsCreated.Load() }

// ProgramCacheStatsForTest reports the shared program cache's hits and misses.
func ProgramCacheStatsForTest() (hits, misses int) {
	sharedPrograms.mu.Lock()
	defer sharedPrograms.mu.Unlock()
	return sharedPrograms.hits, sharedPrograms.misses
}

// SetHeapReaderForTest replaces the watchdog's heap measurement.
func SetHeapReaderForTest(read func() uint64) (restore func()) {
	heapWatchdog.mu.Lock()
	previous := heapWatchdog.read
	heapWatchdog.read = read
	heapWatchdog.mu.Unlock()
	return func() {
		heapWatchdog.mu.Lock()
		heapWatchdog.read = previous
		heapWatchdog.mu.Unlock()
	}
}

// HeapObjectBytesForTest is the watchdog's real measurement.
func HeapObjectBytesForTest() uint64 { return heapObjectBytes() }

// SetMatchTimeoutForTest sets the regular-expression match timeout directly,
// so a test can watch it fire without waiting out the shipped one. Programs
// compiled under it are keyed by it, and are never reused afterwards.
func SetMatchTimeoutForTest(timeout time.Duration) (restore func()) {
	matchTimeout.Lock()
	previous := matchTimeout.value
	matchTimeout.value = timeout
	regexp2.DefaultMatchTimeout = timeout
	matchTimeout.Unlock()
	return func() {
		matchTimeout.Lock()
		matchTimeout.value = previous
		regexp2.DefaultMatchTimeout = previous
		matchTimeout.Unlock()
	}
}
