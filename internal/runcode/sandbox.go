package runcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// exitHostCallLimit is the exit code a module is closed with when it runs out
// of host calls.
//
// It has to be a code the host can recognize in the error wazero hands back,
// rather than something the sandbox could read afterwards from a shared
// variable, because closing the module mid-call is the only way to stop a
// guest that is inside a host function. 0xC0DE0001 is beyond anything a
// normal program or the Go runtime exits with, so a guest that exits with
// that code on purpose can only forge this failure onto itself — it still
// fails the run, and it gains nothing.
const exitHostCallLimit uint32 = 0xC0DE0001

// subjectDefault is who a failure is about when the caller did not say.
const subjectDefault = "code"

// Call is one request to run a module.
type Call struct {
	// Stdin is the module's standard input.
	Stdin []byte
	// Limits bound this call. A field left zero takes the value from
	// DefaultLimits, so the zero Limits is "the product's limits" rather than
	// "no limits".
	Limits Limits
	// Host installs the host functions this module may call, and is where its
	// capabilities come from. Nil means none: a module that imports a host
	// function then fails at instantiation, which is the honest answer for the
	// Code node, whose whole safety argument is that it has no capability.
	Host HostBinding
	// Subject is who the failure messages are about, so that a pack's node and
	// the Code node both read as themselves. Empty means "code", which is what
	// the Code node's messages have always said.
	Subject string
}

// Outcome is what a module did with one call.
//
// It is returned even when the call failed, because the bytes a module
// produced before it was stopped are the most useful thing a caller has when
// reading the failure.
type Outcome struct {
	// Stdout is what the module wrote, truncated at the call's output limit.
	Stdout []byte
	// Stderr is what the module wrote to standard error, trimmed and
	// truncated. It is diagnostics, not a contract.
	Stderr string
}

// HostBinding installs the host functions one call's module may reach.
//
// It is the seam a node pack's capabilities arrive through, and the only one:
// a module can call nothing that was not installed here, so the set of things
// a pack can do is exactly the set of things a binding installs. Installing is
// per call, into that call's runtime, so one execution can never hand a
// capability to the next.
type HostBinding interface {
	Install(ctx context.Context, runtime wazero.Runtime, state *CallState) error
}

// CallState is one call's host-call accounting.
//
// A host binding is handed it at install time and every host function it
// installs asks it before doing the host's work, which is what makes the
// host-call budget enforceable: the alternative — counting calls somewhere the
// sandbox can only read afterwards — would allow the work first and object
// second, and the work is the expensive part.
type CallState struct {
	mu      sync.Mutex
	limits  Limits
	used    int
	failure error
}

// Enter counts one host call and reports whether the caller may proceed.
//
// A host function calls it as its first statement and returns immediately when
// it is false. That false means the module is out of host calls: the module is
// closed with exitHostCallLimit, so the guest does not run another
// instruction, and the sandbox reports ErrHostCallLimit rather than whatever
// the guest would have done next.
func (state *CallState) Enter(ctx context.Context, module api.Module) bool {
	state.mu.Lock()
	state.used++
	over := state.used > state.limits.MaxHostCalls
	if over {
		state.failure = ErrHostCallLimit
	}
	state.mu.Unlock()
	if !over {
		return true
	}
	// Closing the module is what stops the guest: it is inside a host call, and
	// this is the only handle the host has on the execution around it.
	_ = module.CloseWithExitCode(ctx, exitHostCallLimit)
	return false
}

// Used reports how many host calls the module has made, counting the one that
// was refused for being over the budget.
func (state *CallState) Used() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.used
}

// limitError reports the limit this state tripped, or nil.
func (state *CallState) limitError() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.failure
}

// Sandbox runs one module against one call.
//
// This is the whole execution path: a runtime of its own, WASI's standard
// streams, the call's host binding and nothing else — no preopened directory,
// no environment, no arguments. Every execution gets its own runtime, closed
// when it returns, so one module instance can never observe or outlive
// another; only the translated machine code is shared, through the module
// cache, and that is immutable.
//
// Preparing the sandbox and translating the module run under the caller's
// context rather than the call's time limit, because they are the host's work
// and not the user's program. Charging them to the limit meant a body that
// returns immediately could still be told it "exceeded its 10s time limit" —
// every host where translating a multi-megabyte wasip1 module is slow spent
// the user's whole budget before reaching the user's first instruction, and
// under the race detector one translation alone takes longer than the
// product's default limit. The caller's context still bounds this, so a
// translation that never finished could not hang a workflow.
func (runner *Runner) Sandbox(ctx context.Context, module []byte, call Call) (Outcome, error) {
	limits := call.Limits.orDefault()
	subject := call.Subject
	if subject == "" {
		subject = subjectDefault
	}

	config := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(limits.MemoryPages)
	if runner.modules != nil {
		config = config.WithCompilationCache(runner.modules.compilation)
	}

	runtime := wazero.NewRuntimeWithConfig(ctx, config)
	defer runtime.Close(context.Background())

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return Outcome{}, fmt.Errorf("prepare sandbox: %w", err)
	}

	state := &CallState{limits: limits}
	if call.Host != nil {
		if err := call.Host.Install(ctx, runtime, state); err != nil {
			return Outcome{}, fmt.Errorf("install host capabilities: %w", err)
		}
	}

	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}
		// A module with a memory minimum over the call's limit is refused
		// here, before it starts. The message states the limit this deployment
		// configured rather than repeating wazero's, which renders the
		// module's minimum and the limit as the same rounded size.
		if strings.Contains(err.Error(), memoryLimitMarker) {
			return Outcome{}, named(ErrMemoryLimit, fmt.Sprintf(
				"this module cannot start inside the %d-page (%s) memory limit",
				limits.MemoryPages, memorySize(limits.MemoryPages)))
		}
		// Anything else at this point means a stored artifact is not a module
		// this runtime can load, which is a build or a cache that has gone
		// wrong rather than anything the user wrote.
		return Outcome{}, &ExecutionError{Detail: sanitizeRuntimeError(err)}
	}
	// compiled is deliberately not closed: closing it deletes the translation
	// from the shared cache, which is the one thing the cache exists to keep.
	// Closing the runtime releases this execution's instance either way, and
	// when there is no shared cache that also releases the translation with it.

	stdout := &limitedWriter{limit: limits.MaxOutputBytes}
	stderr := &limitedWriter{limit: 64 << 10}
	moduleConfig := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(call.Stdin)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("").
		// No WithFS, no WithEnv, no WithArgs: the module gets streams, the
		// call's host functions and nothing else.
		WithSysNanotime().
		WithSysWalltime()

	// The time limit starts here, where the user's program does.
	runCtx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()

	_, err = runtime.InstantiateModule(runCtx, compiled, moduleConfig)
	outcome := Outcome{Stdout: stdout.Bytes(), Stderr: strings.TrimSpace(stderr.String())}

	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			// wazero reports a deadline or a cancellation as a reserved exit
			// code rather than a context error, so those are separated from a
			// genuine non-zero exit before anything else is decided.
			switch exit.ExitCode() {
			case sys.ExitCodeDeadlineExceeded:
				return outcome, timedOut(subject, limits.Timeout)
			case sys.ExitCodeContextCanceled:
				if ctx.Err() != nil {
					return outcome, ctx.Err()
				}
				return outcome, &ExecutionError{Detail: subject + " was cancelled"}
			}
			if ctx.Err() != nil {
				return outcome, ctx.Err()
			}
			if state.limitError() != nil || exit.ExitCode() == exitHostCallLimit {
				return outcome, named(ErrHostCallLimit, fmt.Sprintf(
					"%s made more than %d host calls", subject, limits.MaxHostCalls))
			}
			if exit.ExitCode() != 0 {
				// A non-zero exit is user code reporting failure, which it does
				// by writing a structured error before exiting.
				if failure := decodeFailure(outcome.Stdout); failure != "" {
					return outcome, &ExecutionError{Detail: failure}
				}
				// Otherwise it is the Go runtime saying it could not get the
				// memory it asked for. wazero's limit is what denies the
				// allocation; naming it here is all this adds, and it is worth
				// adding because "runtime: out of memory" reaching a user
				// unlabelled reads as a server fault.
				if ranOutOfMemory(outcome.Stderr) {
					return outcome, named(ErrMemoryLimit, fmt.Sprintf(
						"%s ran out of memory inside its %d-page (%s) memory limit",
						subject, limits.MemoryPages, memorySize(limits.MemoryPages)))
				}
				return outcome, &ExecutionError{Detail: fmt.Sprintf("%s exited with status %d", subject, exit.ExitCode())}
			}
		}
		if ctx.Err() != nil {
			return outcome, ctx.Err()
		}
		if runCtx.Err() != nil {
			return outcome, timedOut(subject, limits.Timeout)
		}
		if stdout.Truncated() {
			return outcome, outputExceeded(subject)
		}
		return outcome, &ExecutionError{Detail: sanitizeRuntimeError(err)}
	}
	if stdout.Truncated() {
		return outcome, outputExceeded(subject)
	}
	return outcome, nil
}

// memoryLimitMarker is what wazero says when a module's declared memory
// minimum does not fit the runtime's limit. Only the marker is used: the
// numbers beside it are the module author's and the limit rendered with the
// same unit, so they are not what a user is told.
const memoryLimitMarker = "over limit of"

// memoryFailureMarkers are what the Go runtime prints when it dies from
// exhaustion inside the sandbox.
//
// This is deliberately Go-runtime-specific and is not an enforcement
// mechanism: wazero's memory limit is what denies the allocation, and these
// two messages are the shape that denial takes when the guest is a Go program.
// A guest that is not Go cannot print them, and one that prints them on
// purpose only mislabels its own failure.
var memoryFailureMarkers = []string{"fatal error: out of memory", "runtime: out of memory"}

// ranOutOfMemory reports whether a module's standard error says it died from
// exhaustion.
func ranOutOfMemory(stderr string) bool {
	for _, marker := range memoryFailureMarkers {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}

// memorySize renders a page count the way an operator reads it.
func memorySize(pages uint32) string {
	// One WebAssembly page is 64KiB.
	size := int64(pages) << 16
	switch {
	case size >= 1<<30 && size%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", size>>30)
	case size >= 1<<20 && size%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", size>>20)
	case size >= 1<<10 && size%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", size>>10)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// named builds a user-facing failure that also answers errors.Is for the limit
// that caused it.
func named(cause error, detail string) error {
	return &ExecutionError{Detail: detail, Cause: cause}
}

// timedOut is the one time-limit failure, so the wording cannot drift between
// the two places a module runs out of wall clock.
func timedOut(subject string, timeout time.Duration) error {
	return named(ErrTimeLimit, fmt.Sprintf("%s exceeded its %s time limit", subject, timeout))
}

// outputExceeded is the one output-limit failure.
func outputExceeded(subject string) error {
	return named(ErrOutputLimit, fmt.Sprintf("%s produced more output than the limit allows", subject))
}
