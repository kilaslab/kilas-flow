package jsrun

import (
	"runtime"
	"time"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Mode is how often a body runs, in n8n's own words so an imported node's
// parameter is used as it is.
type Mode string

const (
	// ModeAllItems runs the body once, over every item. It is the default.
	ModeAllItems Mode = "runOnceForAllItems"
	// ModeEachItem runs the body once per item.
	ModeEachItem Mode = "runOnceForEachItem"
)

// orDefault treats an empty mode as n8n does, as the all-items mode.
func (mode Mode) orDefault() Mode {
	if mode == ModeEachItem {
		return ModeEachItem
	}
	return ModeAllItems
}

// Limits bound one run. A zero field means the default, never "unbounded",
// which is runcode's rule too: a limit that could be switched off by leaving
// it out would be switched off by accident.
type Limits struct {
	// Timeout bounds the user's program only; see the package documentation.
	Timeout time.Duration
	// MaxInputBytes bounds the node's input, as JSON, before any VM exists.
	MaxInputBytes int64
	// MaxOutputBytes bounds the returned items, as JSON.
	MaxOutputBytes int64
	// MaxConsoleBytes bounds the console output one run keeps.
	MaxConsoleBytes int64
	// MaxCallDepth bounds nested function calls.
	MaxCallDepth int
	// MaxHostCalls bounds calls into the host, such as HTTP requests.
	MaxHostCalls int
}

// DefaultLimits are the shipped defaults. The time limit matches the Go Code
// node's, so the two nodes behave alike out of the box.
func DefaultLimits() Limits {
	return Limits{
		Timeout:         10 * time.Second,
		MaxInputBytes:   32 << 20,
		MaxOutputBytes:  16 << 20,
		MaxConsoleBytes: 64 << 10,
		MaxCallDepth:    10_000,
		MaxHostCalls:    100,
	}
}

func (limits Limits) orDefault() Limits {
	defaults := DefaultLimits()
	if limits.Timeout <= 0 {
		limits.Timeout = defaults.Timeout
	}
	if limits.MaxInputBytes <= 0 {
		limits.MaxInputBytes = defaults.MaxInputBytes
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.MaxConsoleBytes <= 0 {
		limits.MaxConsoleBytes = defaults.MaxConsoleBytes
	}
	if limits.MaxCallDepth <= 0 {
		limits.MaxCallDepth = defaults.MaxCallDepth
	}
	if limits.MaxHostCalls <= 0 {
		limits.MaxHostCalls = defaults.MaxHostCalls
	}
	return limits
}

// tighten lowers each limit to the requested one where the request is
// smaller. A node may tighten the deployment's ceiling and never raise it.
func (limits Limits) tighten(requested Limits) Limits {
	if requested.Timeout > 0 && requested.Timeout < limits.Timeout {
		limits.Timeout = requested.Timeout
	}
	if requested.MaxInputBytes > 0 && requested.MaxInputBytes < limits.MaxInputBytes {
		limits.MaxInputBytes = requested.MaxInputBytes
	}
	if requested.MaxOutputBytes > 0 && requested.MaxOutputBytes < limits.MaxOutputBytes {
		limits.MaxOutputBytes = requested.MaxOutputBytes
	}
	if requested.MaxConsoleBytes > 0 && requested.MaxConsoleBytes < limits.MaxConsoleBytes {
		limits.MaxConsoleBytes = requested.MaxConsoleBytes
	}
	if requested.MaxCallDepth > 0 && requested.MaxCallDepth < limits.MaxCallDepth {
		limits.MaxCallDepth = requested.MaxCallDepth
	}
	if requested.MaxHostCalls > 0 && requested.MaxHostCalls < limits.MaxHostCalls {
		limits.MaxHostCalls = requested.MaxHostCalls
	}
	return limits
}

// Per-call bounds on the built-ins that allocate or loop as far as a number
// tells them to. goja cannot interrupt one built-in call, so without these a
// single line such as `new Uint8Array(2**30)` or `[...Array(2**26).keys()]`
// could take the server's memory, or a core, long past any limit. Each is far
// beyond what a Code node's items need: the node's own input is capped at 32
// MiB.
const (
	// MaxElementsPerCall bounds the length an array method, Array.from or an
	// argument list may walk.
	MaxElementsPerCall = 1 << 23
	// MaxCharactersPerCall bounds the string repeat, padStart and padEnd may
	// build.
	MaxCharactersPerCall = 1 << 25
	// MaxBytesPerCall bounds the size of a typed array or ArrayBuffer.
	MaxBytesPerCall = 1 << 26
)

// DefaultHeapCeiling is the live-heap size at which the watchdog stops every
// running script, when the deployment names none. The server computes a
// better one from GOMEMLIMIT when that is set.
const DefaultHeapCeiling = 1 << 30

// Options configure a Runner for the whole deployment.
type Options struct {
	// Limits is the deployment's ceiling. A task may tighten it, never raise
	// it. Zero fields take DefaultLimits.
	Limits Limits
	// MaxConcurrent bounds how many scripts run at once; more wait their
	// turn. Zero means runtime.GOMAXPROCS(0).
	MaxConcurrent int
	// HeapCeiling is the live-heap size, in bytes, at which every running
	// script is stopped with ErrMemoryLimit. Zero means DefaultHeapCeiling.
	HeapCeiling uint64
}

// Task is one node execution.
type Task struct {
	Source string
	Mode   Mode
	Items  []workflow.Item
	// Roots are the rest of what the code's globals read.
	Roots Roots
	// Limits tightens the runner's ceiling for this task; zero fields keep it.
	Limits Limits
	// ContinueOnItemError makes "Run once for each item" go on past an item
	// whose code threw or returned something that is not an item, as n8n
	// does when the node continues on failure, and report every item's
	// outcome in Result.Outcomes. A limit (time, memory, output) still ends
	// the whole run.
	ContinueOnItemError bool
}

// Result is what one run produced.
type Result struct {
	// Items are the returned items. An item that descends from an input item
	// carries that item's origin in Paired; one with no known source leaves
	// Paired nil for the runner to infer.
	Items []workflow.Item
	// Console is what the code printed, up to the console limit, and
	// ConsoleTruncated reports that more was dropped. Both are set when the
	// run fails too.
	Console          []ConsoleLine
	ConsoleTruncated bool
	// UserTime is the time charged to the user's program, which is what the
	// time limit is measured against.
	UserTime time.Duration
	// Outcomes are every input item's outcome, in order, when the task asked
	// to continue past failed items in "Run once for each item" mode. Items
	// then holds only what the items that succeeded returned.
	Outcomes []ItemOutcome `json:",omitempty"`
}

// ItemOutcome is one input item's result in "Run once for each item" mode.
type ItemOutcome struct {
	// Items are what the item's code returned.
	Items []workflow.Item `json:"items,omitempty"`
	// Error is why it failed, or nil; Err decodes it.
	Error *WireError `json:"error,omitempty"`
}

// Err is why the item failed, or nil.
func (outcome ItemOutcome) Err() error { return outcome.Error.Decode() }

// Runner runs Code-node JavaScript. It is safe for concurrent use; one per
// deployment is the intended shape.
type Runner struct {
	limits      Limits
	heapCeiling uint64
	slots       chan struct{}

	// Test seams, set only by export_test.go.
	testHost      func(*vm)
	betweenItems  func()
	forcedLibrary []string
	onInterrupt   func(time.Time)
}

// NewRunner builds a runner for one deployment.
func NewRunner(options Options) *Runner {
	limits := options.Limits.orDefault()
	coverTimeLimit(limits.Timeout)
	concurrent := options.MaxConcurrent
	if concurrent <= 0 {
		concurrent = runtime.GOMAXPROCS(0)
	}
	ceiling := options.HeapCeiling
	if ceiling == 0 {
		ceiling = DefaultHeapCeiling
	}
	return &Runner{
		limits:      limits,
		heapCeiling: ceiling,
		slots:       make(chan struct{}, concurrent),
	}
}

// Limits reports the runner's ceiling, after defaults.
func (runner *Runner) Limits() Limits { return runner.limits }
