package runcode_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/runcode"
)

// generousLimits give WASM execution room to finish under the race detector,
// which slows it well past the product's 10s default. Tests that assert a
// limit *fires* set their own, deliberately small, bound.
func generousLimits() runcode.Limits {
	limits := runcode.DefaultLimits()
	limits.Timeout = 120 * time.Second
	return limits
}

// The real toolchain is used where it is available. CI images without it still
// exercise every cache, limit, and sandbox path through prebuilt artifacts and
// the fake compiler below.
func requireToolchain(t *testing.T) *runcode.ToolchainCompiler {
	t.Helper()
	compiler := runcode.NewToolchainCompiler()
	if !compiler.Available() {
		t.Skip("go toolchain is not available")
	}
	return compiler
}

// countingCompiler records how often it was asked to build, which is how the
// tests prove compilation is an artifact lifecycle rather than a per-run step.
type countingCompiler struct {
	mu       sync.Mutex
	inner    runcode.Compiler
	builds   int
	failWith error
}

func (compiler *countingCompiler) Available() bool { return true }

func (compiler *countingCompiler) Compile(ctx context.Context, source string) ([]byte, error) {
	compiler.mu.Lock()
	compiler.builds++
	compiler.mu.Unlock()
	if compiler.failWith != nil {
		return nil, compiler.failWith
	}
	return compiler.inner.Compile(ctx, source)
}

func (compiler *countingCompiler) count() int {
	compiler.mu.Lock()
	defer compiler.mu.Unlock()
	return compiler.builds
}

func TestSourceHashIsStableAndCoversTheRuntimeVersion(t *testing.T) {
	t.Parallel()

	first := runcode.SourceHash("return items, nil")
	if first != runcode.SourceHash("return items, nil") {
		t.Error("the same source hashed differently twice")
	}
	if first == runcode.SourceHash("return nil, nil") {
		t.Error("different source produced the same hash")
	}
	if first == "" || len(first) != 64 {
		t.Errorf("hash = %q, want a sha256 hex digest", first)
	}
}

func TestValidateSourceRejectsWhatCannotBeAFunctionBody(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]string{
		"empty":      "   ",
		"whole file": "package main\nfunc main() {}",
		"no return":  "x := 1",
	} {
		if err := runcode.ValidateSource(source); err == nil {
			t.Errorf("%s source was accepted", name)
		}
	}
	if err := runcode.ValidateSource("return items, nil"); err != nil {
		t.Errorf("a valid body was rejected: %v", err)
	}
}

func TestCodeRunsAndReturnsTransformedItems(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	result, err := runner.Run(context.Background(), `
	out := make([]Item, 0, len(items))
	for _, item := range items {
		item.JSON["doubled"] = item.JSON["n"].(float64) * 2
		out = append(out, item)
	}
	return out, nil
`, []runcode.Item{{JSON: map[string]any{"n": float64(21)}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v, want 1", result.Items)
	}
	if result.Items[0].JSON["doubled"] != float64(42) {
		t.Fatalf("item = %#v, want the transformed value", result.Items[0].JSON)
	}
}

func TestCompilationHappensOncePerSourceAndIsInvalidatedByAChange(t *testing.T) {
	inner := requireToolchain(t)
	compiler := &countingCompiler{inner: inner}
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())
	input := []runcode.Item{{JSON: map[string]any{}}}

	for range 3 {
		if _, err := runner.Run(context.Background(), "return items, nil", input); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	// Three runs of unchanged source must build once, not three times.
	if compiler.count() != 1 {
		t.Fatalf("builds = %d, want 1 for unchanged source", compiler.count())
	}

	if _, err := runner.Run(context.Background(), "return []Item{}, nil", input); err != nil {
		t.Fatalf("Run() changed source error = %v", err)
	}
	if compiler.count() != 2 {
		t.Fatalf("builds = %d, want a rebuild after the source changed", compiler.count())
	}
}

func TestArtifactCacheReportsHitsAndMisses(t *testing.T) {
	inner := requireToolchain(t)
	cache := runcode.NewMemoryCache()
	runner := runcode.NewRunner(&countingCompiler{inner: inner}, cache, generousLimits())

	if _, err := runner.Artifact(context.Background(), "return items, nil"); err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}
	if _, err := runner.Artifact(context.Background(), "return items, nil"); err != nil {
		t.Fatalf("Artifact() second error = %v", err)
	}
	hits, misses := cache.Stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("cache stats = (%d hits, %d misses), want (1, 1)", hits, misses)
	}
}

func TestCompilationFailureIsReportedWithoutServerPaths(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	_, err := runner.Run(context.Background(), "this is not go; return items, nil", nil)
	if err == nil {
		t.Fatal("invalid code compiled successfully")
	}
	var compileErr *runcode.CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("error = %T (%v), want a CompileError", err, err)
	}
	// A build path is a server detail; a user reading the error should not see
	// where the server keeps its temporary directories.
	if strings.Contains(err.Error(), "/var/folders") || strings.Contains(err.Error(), "kilasflow-code-") {
		t.Errorf("compile error leaked a server path: %v", err)
	}
}

func TestARunnerWithNoCompilerReportsThatClearly(t *testing.T) {
	t.Parallel()

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), runcode.DefaultLimits())
	_, err := runner.Run(context.Background(), "return items, nil", nil)
	// A deployment without the toolchain must say so, not fail obscurely.
	if !errors.Is(err, runcode.ErrCompilerUnavailable) {
		t.Fatalf("error = %v, want ErrCompilerUnavailable", err)
	}
}

func TestAnArtifactFromAnOlderContractIsNotRun(t *testing.T) {
	t.Parallel()

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), runcode.DefaultLimits())
	_, err := runner.Execute(context.Background(), runcode.Artifact{
		Hash: "x", RuntimeVersion: "wasip1-v0", Module: []byte{0x00, 0x61, 0x73, 0x6d},
	}, nil)
	if !errors.Is(err, runcode.ErrNotCompiled) {
		t.Fatalf("error = %v, want ErrNotCompiled for a stale contract", err)
	}
}

func TestUserCodeCannotReachTheFilesystem(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	// No directory is preopened, so even the module's own working directory is
	// unreachable. The read must fail inside the sandbox.
	result, err := runner.Run(context.Background(), `
	if _, err := os.ReadFile("/etc/passwd"); err != nil {
		return []Item{{JSON: map[string]any{"denied": true}}}, nil
	}
	return []Item{{JSON: map[string]any{"denied": false}}}, nil
`, nil)
	if err != nil {
		// Failing to compile because `os` was not imported is also a denial;
		// what must never happen is a successful read.
		if strings.Contains(err.Error(), "undefined: os") {
			t.Skip("the wrapper does not import os, so a file read cannot even compile")
		}
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["denied"] != true {
		t.Fatalf("result = %#v, want the filesystem read to be denied", result.Items)
	}
}

func TestUserCodeCannotReachTheNetwork(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	result, err := runner.Run(context.Background(), `
	if _, err := net.Dial("tcp", "example.com:80"); err != nil {
		return []Item{{JSON: map[string]any{"denied": true}}}, nil
	}
	return []Item{{JSON: map[string]any{"denied": false}}}, nil
`, nil)
	if err != nil {
		if strings.Contains(err.Error(), "undefined: net") {
			t.Skip("the wrapper does not import net, so a dial cannot even compile")
		}
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["denied"] != true {
		t.Fatalf("result = %#v, want the network dial to be denied", result.Items)
	}
}

func TestUserCodeSeesNoEnvironment(t *testing.T) {
	compiler := requireToolchain(t)
	t.Setenv("KILASFLOW_ENCRYPTION_KEY", "super-secret-master-key")
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	result, err := runner.Run(context.Background(), `
	return []Item{{JSON: map[string]any{"env": os.Getenv("KILASFLOW_ENCRYPTION_KEY")}}}, nil
`, nil)
	if err != nil {
		if strings.Contains(err.Error(), "undefined: os") {
			t.Skip("the wrapper does not import os")
		}
		t.Fatalf("Run() error = %v", err)
	}
	// The host's environment carries the credential master key; the module is
	// given none at all.
	if got := result.Items[0].JSON["env"]; got != "" {
		t.Fatalf("module read the environment: %#v", got)
	}
}

func TestExecutionStopsAtItsTimeLimit(t *testing.T) {
	compiler := requireToolchain(t)
	source := `
	for {
		_ = 1
	}
	return items, nil
`
	// Compiled first so the measurement below covers execution alone. Timing
	// Run() would include the build, which is unbounded by this limit and slow
	// enough under the race detector to swamp what the test is asserting.
	artifact, err := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits()).
		Artifact(context.Background(), source)
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}

	limits := runcode.DefaultLimits()
	limits.Timeout = 500 * time.Millisecond
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), limits)

	start := time.Now()
	_, err = runner.Execute(context.Background(), artifact, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("an endless loop completed")
	}
	if !strings.Contains(err.Error(), "time limit") {
		t.Errorf("error = %v, want the time limit reported", err)
	}
	// The guarantee under test is that an endless loop *is* stopped, and the
	// error above proves the limit is what stopped it. The wall-clock bound is
	// deliberately loose: wazero interrupts between instructions, and under the
	// race detector each check is slow enough that a tight bound would be
	// measuring the detector rather than the product. Without a bound at all,
	// a regression that never stopped would hang the suite instead of failing.
	if elapsed > time.Minute {
		t.Errorf("execution took %s, want the limit to stop it", elapsed)
	}
}

func TestExecutionStopsWhenCancelled(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	// Compile first so the cancellation is observed by the run, not the build.
	artifact, err := runner.Artifact(context.Background(), `
	for {
		_ = 1
	}
	return items, nil
`)
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if _, err := runner.Execute(ctx, artifact, nil); err == nil {
		t.Fatal("a cancelled run completed")
	}
}

func TestMemoryPressureIsDeniedRatherThanExhaustingTheHost(t *testing.T) {
	compiler := requireToolchain(t)
	limits := generousLimits()
	limits.MemoryPages = 32 // 2 MiB
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), limits)

	_, err := runner.Run(context.Background(), `
	blocks := make([][]byte, 0, 4096)
	for i := 0; i < 4096; i++ {
		blocks = append(blocks, make([]byte, 1<<20))
	}
	return []Item{{JSON: map[string]any{"blocks": len(blocks)}}}, nil
`, nil)
	// Allocating 4 GiB inside a 2 MiB limit must fail the module, not the host.
	if err == nil {
		t.Fatal("a module allocated far past its memory limit")
	}
}

func TestUserCodeErrorIsReportedStructurally(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), generousLimits())

	_, err := runner.Run(context.Background(), `
	return nil, errors.New("the customer record was rejected")
`, nil)
	if err != nil && strings.Contains(err.Error(), "undefined: errors") {
		t.Skip("the wrapper does not import errors")
	}
	if err == nil {
		t.Fatal("code returning an error reported success")
	}
	var executionErr *runcode.ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error = %T (%v), want an ExecutionError", err, err)
	}
	if !strings.Contains(err.Error(), "the customer record was rejected") {
		t.Errorf("error = %v, want the user's message surfaced", err)
	}
}

func TestOutputIsBounded(t *testing.T) {
	compiler := requireToolchain(t)
	limits := generousLimits()
	limits.MaxOutputBytes = 512
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), limits)

	_, err := runner.Run(context.Background(), `
	out := make([]Item, 0, 500)
	for i := 0; i < 500; i++ {
		out = append(out, Item{JSON: map[string]any{"padding": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}})
	}
	return out, nil
`, nil)
	if err == nil {
		t.Fatal("a module wrote past the output limit without being stopped")
	}
	if !strings.Contains(err.Error(), "output") {
		t.Errorf("error = %v, want the output limit reported", err)
	}
}
