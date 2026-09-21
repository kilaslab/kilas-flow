package runcode_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
)

// A limit only means something if a caller can tell which one was hit and the
// process is still standing afterwards. Nothing here may need a Go toolchain:
// these tests clear PATH, so a build would fail here rather than pass on a
// machine that happens to have one.
func TestALimitFailureIsNamedAndNeverTheProcess(t *testing.T) {
	t.Setenv("PATH", "")

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

	for _, testCase := range []struct {
		name       string
		module     []byte
		limits     runcode.Limits
		want       error
		wantDetail string
		wantStdout int
	}{
		{
			name:       "wall clock",
			module:     wasmtest.Build(nil, wasmtest.Loop(wasmtest.Br(0)), 1, nil),
			limits:     runcode.Limits{Timeout: 100 * time.Millisecond},
			want:       runcode.ErrTimeLimit,
			wantDetail: "time limit",
		},
		{
			name:       "output bytes",
			module:     wasmtest.MinimalModule(strings.Repeat("x", 4096)),
			limits:     runcode.Limits{MaxOutputBytes: 32},
			want:       runcode.ErrOutputLimit,
			wantDetail: "output",
			// What the module managed to write before the limit stopped it
			// comes back with the failure.
			wantStdout: 32,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outcome, err := runner.Sandbox(context.Background(), testCase.module, runcode.Call{
				Stdin: []byte("{}"), Limits: testCase.limits,
			})
			if err == nil {
				t.Fatal("the limit was not reached")
			}
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want errors.Is(err, %v)", err, testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.wantDetail) {
				t.Errorf("error = %q, want it to name the %s", err, testCase.name)
			}
			// The named error is still the error a caller reads as an
			// ExecutionError, which is the message the Code node surfaces.
			var executionErr *runcode.ExecutionError
			if !errors.As(err, &executionErr) {
				t.Fatalf("error = %T (%v), want an ExecutionError", err, err)
			}
			if len(outcome.Stdout) != testCase.wantStdout {
				t.Errorf("Stdout = %d bytes, want %d", len(outcome.Stdout), testCase.wantStdout)
			}
		})
	}

	// Each case above was stopped by its own sandbox rather than by the test
	// binary exiting, and a module that spent its limit does not poison the
	// next call.
	result, err := runner.Execute(context.Background(), minimalArtifact(emptyItems), nil)
	if err != nil {
		t.Fatalf("a call after a limit failure failed: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("items = %#v, want none", result.Items)
	}
}

// allocationDenialPages is the memory limit the allocation-denial cases run
// under: 96 pages, 6 MiB.
//
// The guest's declared minimum memory is computed by the Go linker, and the
// tests must not hard-code it: with Go 1.27.1 a trivial wasip1 guest declares
// 50 pages (3 MiB), so a limit of 48 is refused while compiling — a case
// written against it passes on a start-up refusal and never exercises
// allocation denial at all, which is precisely the trap these cases exist to
// avoid. 6 MiB is clear of the minimum and a quarter of the 8 MiB the guest
// asks for, so the allocation is still what fails. requireAllocationDenial
// checks that it is, rather than trusting the number.
const allocationDenialPages = 96

// requireAllocationDenial fails unless a memory failure came from a running
// module that could not allocate.
//
// Both a module refused before it starts and a module that dies allocating
// report ErrMemoryLimit, and a case about denial inside a running program is
// vacuous if the program never ran. The guest's own out-of-memory report is
// what tells the two apart.
func requireAllocationDenial(t *testing.T, result runcode.Result, err error) {
	t.Helper()
	if !errors.Is(err, runcode.ErrMemoryLimit) {
		t.Fatalf("error = %v, want ErrMemoryLimit", err)
	}
	if !strings.Contains(result.Stderr, "out of memory") {
		t.Fatalf("stderr = %q, want the running guest's own out-of-memory report rather than a refusal at start-up", result.Stderr)
	}
}

// hostSpy installs one host function, kilasflow_v1.ping, which counts the
// calls it was allowed to serve and remembers the state it was handed.
type hostSpy struct {
	answer int32
	calls  int
	state  *runcode.CallState
}

func (host *hostSpy) Install(ctx context.Context, runtime wazero.Runtime, state *runcode.CallState) error {
	host.state = state
	_, err := runtime.NewHostModuleBuilder("kilasflow_v1").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, module api.Module) int32 {
			// The first statement of every host function: ask whether the
			// module has a host call left, and do no host work if it has not.
			if !state.Enter(ctx, module) {
				return 0
			}
			host.calls++
			return host.answer
		}).
		Export("ping").
		Instantiate(ctx)
	return err
}

var pingImport = []wasmtest.Import{{
	Module: "kilasflow_v1", Name: "ping", Results: []wasmtest.ValueType{wasmtest.I32},
}}

// pingLoop is a guest that calls the host forever, which is the only way a
// module can spend a host-call budget.
func pingLoop() []byte {
	body := wasmtest.Call(0)
	body = append(body, wasmtest.Drop()...)
	body = append(body, wasmtest.Br(0)...)
	return wasmtest.Build(pingImport, wasmtest.Loop(body), 1, nil)
}

// Every host call is policed on the host side. What the budget protects is the
// host's work — an HTTP request, a credential, a datastore read — so the work
// must stop at the budget rather than after it.
func TestAHostCallBudgetStopsTheModule(t *testing.T) {
	t.Setenv("PATH", "")

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
	const allowed = 3

	host := &hostSpy{answer: 7}
	_, err := runner.Sandbox(context.Background(), pingLoop(), runcode.Call{
		Limits: runcode.Limits{MaxHostCalls: allowed},
		Host:   host,
	})
	if err == nil {
		t.Fatal("a module that called the host forever was allowed to")
	}
	if !errors.Is(err, runcode.ErrHostCallLimit) {
		t.Fatalf("error = %v, want ErrHostCallLimit", err)
	}
	if host.calls != allowed {
		t.Errorf("host calls = %d, want the %d it was allowed and not one more", host.calls, allowed)
	}
	if got := host.state.Used(); got != allowed+1 {
		t.Errorf("Used() = %d, want %d: the refused call is counted too", got, allowed+1)
	}

	// The accounting is not itself the failure: a guest that calls the host
	// once and checks the answer runs normally, and a budget of zero means the
	// shipped default rather than "no host calls at all" — the same rule the
	// other three limits follow.
	single := &hostSpy{answer: 7}
	if _, err := runner.Sandbox(context.Background(),
		wasmtest.Build(pingImport, wasmtest.ExpectResult(0, 7), 1, nil),
		runcode.Call{Host: single}); err != nil {
		t.Fatalf("a guest inside the default host-call budget failed: %v", err)
	}
	if single.calls != 1 {
		t.Errorf("host calls = %d, want 1", single.calls)
	}
	if got := single.state.Used(); got != 1 {
		t.Errorf("Used() = %d, want 1", got)
	}
}

// Importing a host function is a request for a capability, and a call with no
// binding grants none. The failure has to name the module that was missing:
// this is the seam a pack's declared capabilities arrive through, so a pack
// that asks for one and is granted nothing must fail loudly rather than have
// its call vanish.
func TestASandboxWithNoHostBindingCannotImportAHostFunction(t *testing.T) {
	t.Setenv("PATH", "")

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
	_, err := runner.Sandbox(context.Background(),
		wasmtest.Build(pingImport, wasmtest.ExpectResult(0, 7), 1, nil), runcode.Call{})
	if err == nil {
		t.Fatal("a module imported a host function with nothing to bind it to")
	}
	for _, limit := range []error{runcode.ErrTimeLimit, runcode.ErrMemoryLimit, runcode.ErrOutputLimit, runcode.ErrHostCallLimit} {
		if errors.Is(err, limit) {
			t.Fatalf("error = %v, want a capability failure rather than %v", err, limit)
		}
	}
	if !strings.Contains(err.Error(), "kilasflow_v1") {
		t.Errorf("error = %v, want the missing host module named", err)
	}
}

// A module whose declared minimum memory is over the call's limit never
// starts: wazero refuses it while compiling, before one instruction runs. The
// message has to state the limit the deployment configured, and it can only
// come from the call's own numbers — wazero's own sentence renders the
// module's minimum and the limit with the same rounded size, so an operator
// reading it cannot tell which number to change.
//
// The 40 declared pages are the assembler's choice rather than the Go
// linker's, which is what makes this deterministic and toolchain-free.
func TestAModuleThatCannotStartInsideTheMemoryLimitIsNamed(t *testing.T) {
	t.Setenv("PATH", "")

	limits := runcode.DefaultLimits()
	limits.MemoryPages = 32 // 2 MiB
	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), limits)

	_, err := runner.Sandbox(context.Background(), wasmtest.Build(nil, nil, 40, nil), runcode.Call{Limits: limits})
	if err == nil {
		t.Fatal("a module that cannot fit its memory limit started")
	}
	if !errors.Is(err, runcode.ErrMemoryLimit) {
		t.Fatalf("error = %v, want ErrMemoryLimit", err)
	}
	if !strings.Contains(err.Error(), "32-page (2 MiB)") {
		t.Errorf("error = %q, want the configured limit stated as 32-page (2 MiB)", err)
	}
}

// The memory failure wazero does not refuse at compile time: a module whose
// declared minimum fits starts, and then cannot allocate. For a Go guest that
// is the runtime's own out-of-memory exit, which is named as a limit failure
// rather than passed on as "exited with status 2".
func TestTheMemoryLimitIsNamed(t *testing.T) {
	compiler := requireToolchain(t)

	limits := runcode.DefaultLimits()
	limits.MemoryPages = allocationDenialPages
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), limits)

	artifact, err := runner.Artifact(context.Background(), `
	block := make([]byte, 8<<20)
	for index := range block {
		block[index] = byte(index)
	}
	return []Item{{JSON: map[string]any{"size": float64(len(block))}}}, nil
`)
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}

	result, err := runner.Execute(context.Background(), artifact, nil)
	requireAllocationDenial(t, result, err)
}
