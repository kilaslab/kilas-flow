package wasmtest_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/kilaslab/kilas-flow/internal/wasmtest"
)

// runMinimal instantiates the hand-built module the way runcode does — WASI's
// snapshot preview 1, stdin and stdout, nothing else — and returns what the
// guest wrote.
func runMinimal(t *testing.T, stdout string) string {
	t.Helper()

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("instantiate wasi_snapshot_preview1: %v", err)
	}

	compiled, err := runtime.CompileModule(ctx, wasmtest.MinimalModule(stdout))
	if err != nil {
		t.Fatalf("the hand-built module did not compile: %v", err)
	}
	var out bytes.Buffer
	if _, err := runtime.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithStdin(strings.NewReader("ignored")).WithStdout(&out)); err != nil {
		t.Fatalf("the hand-built module did not run: %v", err)
	}
	return out.String()
}

// The point of this package is a module that needs no Go toolchain, so the
// existing runcode tests that skip on a toolchain-free machine have something
// to run. An empty string is the boundary worth pinning: a zero-length iovec
// is the one case where fd_write returning its own count could be confused
// with having written nothing.
func TestMinimalModuleWritesItsStdout(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		`{"items":[]}` + "\n",
		"",
		strings.Repeat("x", 5000),
	} {
		if got := runMinimal(t, want); got != want {
			t.Errorf("module wrote %d bytes, want %d (%q)", len(got), len(want), firstBytes(got))
		}
	}
}

// Nothing in this file creates runtimes concurrently, and none of it is
// t.Parallel: wazero v1.9.0 caches its own version in a package-level string
// without synchronization (internal/version.GetWazeroVersion, called from
// NewRuntimeWithConfig), so two runtimes built at the same moment are a data
// race inside a dependency this repository does not own. Building them one at
// a time is the honest way to keep `go test -race` meaningful here.
//
// run assembles a module with Build and runs it against one host function the
// way runcode does, returning the error rather than failing the test: the
// interesting cases here are the ones that are supposed to go wrong.
func run(t *testing.T, module []byte, install func(runtime wazero.Runtime) error) error {
	t.Helper()

	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("instantiate wasi_snapshot_preview1: %v", err)
	}
	if install != nil {
		if err := install(runtime); err != nil {
			t.Fatalf("install host function: %v", err)
		}
	}

	compiled, err := runtime.CompileModule(ctx, module)
	if err != nil {
		t.Fatalf("the assembled module did not compile: %v", err)
	}
	_, err = runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStdout(&bytes.Buffer{}))
	return err
}

// pingHost answers every call with answer, so a guest can assert on it.
func pingHost(answer int32) func(wazero.Runtime) error {
	return func(runtime wazero.Runtime) error {
		_, err := runtime.NewHostModuleBuilder("kilasflow_v1").
			NewFunctionBuilder().
			WithFunc(func(context.Context, api.Module) int32 { return answer }).
			Export("ping").
			Instantiate(context.Background())
		return err
	}
}

var ping = []wasmtest.Import{{
	Module: "kilasflow_v1", Name: "ping", Results: []wasmtest.ValueType{wasmtest.I32},
}}

// An empty start body is a whole module: the assembler has to produce
// something wazero accepts with nothing in it but the sections a command
// module needs.
func TestBuildAssemblesAModuleThatDoesNothing(t *testing.T) {
	module := wasmtest.Build(nil, nil, 1, nil)
	if err := run(t, module, nil); err != nil {
		t.Fatalf("an empty _start did not run: %v", err)
	}
}

// The contract ExpectResult exists for: the guest traps unless the host
// answered exactly the expected value, which is how a host-call test states
// what the host must have returned without a payload encoder.
func TestTheAssemblerTrapsUnlessTheHostAnswersExactly(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		answer  int32
		want    int
		trapped bool
	}{
		{name: "exact answer", answer: 7, want: 7},
		{name: "different answer", answer: 8, want: 7, trapped: true},
		{name: "different by a sign", answer: -1, want: 7, trapped: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			module := wasmtest.Build(ping, wasmtest.ExpectResult(0, testCase.want), 1, nil)
			err := run(t, module, pingHost(testCase.answer))
			if testCase.trapped {
				if err == nil {
					t.Fatal("the guest accepted an answer it did not expect")
				}
				return
			}
			if err != nil {
				t.Fatalf("the guest rejected the answer it expected: %v", err)
			}
		})
	}
}

// If and Unreachable compose into the trap above; on their own they are how a
// test writes "this must not be reached".
func TestIfRunsItsBodyOnlyOnATrueCondition(t *testing.T) {
	reached := append(wasmtest.I32Const(1), wasmtest.If(wasmtest.Unreachable())...)
	if err := run(t, wasmtest.Build(nil, reached, 1, nil), nil); err == nil {
		t.Fatal("a true condition did not run the trap in its body")
	}

	skipped := append(wasmtest.I32Const(0), wasmtest.If(wasmtest.Unreachable())...)
	if err := run(t, wasmtest.Build(nil, skipped, 1, nil), nil); err != nil {
		t.Fatalf("a false condition ran the trap in its body: %v", err)
	}
}

// Loop and Br are how a limit test gets a guest that never returns, and the
// context is what stops it: the module is not cooperatively cancellable and
// nothing in it reads the deadline.
func TestALoopRunsUntilTheContextIsDone(t *testing.T) {
	module := wasmtest.Build(nil, wasmtest.Loop(wasmtest.Br(0)), 1, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(context.Background(), module)
	if err != nil {
		t.Fatalf("the looping module did not compile: %v", err)
	}
	_, err = runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the deadline to stop the loop", err)
	}
}

func firstBytes(value string) string {
	if len(value) > 40 {
		return value[:40]
	}
	return value
}
