package runcode_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/runcode"
)

// sharedModules gives a test the process-wide translation cache a server keeps.
//
// Tests run under the product's own DefaultLimits rather than an inflated
// number: the time limit bounds the user's program, which is measured in
// microseconds here, so a limit that has to be widened for a test would mean
// the shipped default is wrong for a real user on the same machine.
func sharedModules(t *testing.T) *runcode.ModuleCache {
	t.Helper()
	modules := runcode.NewModuleCache()
	t.Cleanup(func() { _ = modules.Close(context.Background()) })
	return modules
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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
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
	runner := runcode.NewRunner(&countingCompiler{inner: inner}, cache, sharedModules(t), runcode.DefaultLimits())

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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
	_, err := runner.Run(context.Background(), "return items, nil", nil)
	// A deployment without the toolchain must say so, not fail obscurely.
	if !errors.Is(err, runcode.ErrCompilerUnavailable) {
		t.Fatalf("error = %v, want ErrCompilerUnavailable", err)
	}
}

func TestAnArtifactFromAnOlderContractIsNotRun(t *testing.T) {
	t.Parallel()

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
	_, err := runner.Execute(context.Background(), runcode.Artifact{
		Hash: "x", RuntimeVersion: "wasip1-v0", Module: []byte{0x00, 0x61, 0x73, 0x6d},
	}, nil)
	if !errors.Is(err, runcode.ErrNotCompiled) {
		t.Fatalf("error = %v, want ErrNotCompiled for a stale contract", err)
	}
}

func TestUserCodeCannotReachTheFilesystem(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	limits := runcode.DefaultLimits()
	limits.Timeout = 500 * time.Millisecond
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), limits)

	// Built and translated before the measurement, so what is timed below is
	// the run alone. Neither the Go build nor wazero's translation of the
	// module is bounded by this limit — both are the host's work — and under
	// the race detector either is slow enough to swamp the assertion.
	artifact, err := runner.Artifact(context.Background(), source)
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}
	if _, err := runner.Execute(context.Background(), artifact, nil); err == nil {
		t.Fatal("an endless loop completed")
	}

	start := time.Now()
	_, err = runner.Execute(context.Background(), artifact, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("an endless loop completed")
	}
	if !strings.Contains(err.Error(), "time limit") {
		t.Errorf("error = %v, want the time limit reported", err)
	}
	// The limit has to be what governs the duration, not merely what the error
	// says: a limit that fires ten seconds late is not a limit anyone can plan
	// around. The margin over the 500ms bound is wide because wazero interrupts
	// between instructions and the module's own start-up runs first, but it is
	// no longer wide enough to hide the translation this used to be paying for.
	if elapsed > 5*time.Second {
		t.Errorf("execution took %s, want the %s limit to stop it", elapsed, limits.Timeout)
	}
}

// The time limit is the user's budget for their own program. Spending it on
// wazero's translation of the module charged the user for the host's work, and
// on any machine where translating a multi-megabyte wasip1 module is slow — a
// small CI runner, or this suite under the race detector, where one translation
// takes longer than the product's whole 10s default — a body that returns
// immediately was refused for exceeding a limit it never came close to using.
func TestTheTimeLimitIsNotSpentTranslatingTheModule(t *testing.T) {
	compiler := requireToolchain(t)
	limits := runcode.DefaultLimits()
	// Far less than one translation costs anywhere, so this can only pass if
	// the translation is outside the limit.
	limits.Timeout = time.Second
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), limits)

	for run := range 3 {
		result, err := runner.Run(context.Background(), "return items, nil",
			[]runcode.Item{{JSON: map[string]any{"n": float64(1)}}})
		if err != nil {
			t.Fatalf("run %d: Run() error = %v", run, err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("run %d: items = %#v, want the one item back", run, result.Items)
		}
	}
}

// Translating a module is the expensive half of running one, and the Code node
// promises in its own editor that per-item mode costs one build either way.
// That was only true of the Go build: every sandbox call re-translated the same
// module, so a node running over a hundred items paid for it a hundred times.
func TestTheSameModuleIsTranslatedOncePerProcess(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())
	artifact, err := runner.Artifact(context.Background(), "return items, nil")
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}

	start := time.Now()
	if _, err := runner.Execute(context.Background(), artifact, nil); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	cold := time.Since(start)

	start = time.Now()
	if _, err := runner.Execute(context.Background(), artifact, nil); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	warm := time.Since(start)

	// Measured against the first run rather than against a wall-clock number,
	// so the assertion means the same thing on a laptop and on a small runner.
	// Four is far below the ratio a reused translation actually gives and far
	// above anything a repeated one could reach, which leaves room for a noisy
	// machine without letting a regression through.
	t.Logf("first run %s, second run %s", cold.Round(time.Millisecond), warm.Round(time.Millisecond))
	if warm*4 > cold {
		t.Errorf("second run took %s against a first run of %s, want the translation reused", warm, cold)
	}
}

// Two Code nodes can hold the same source and different memory limits, and once
// translations are shared they run the same machine code. The limit has to come
// from the execution asking for it rather than from whichever node happened to
// be translated first, or a node would silently inherit a ceiling it never set
// — and the one that inherited the roomier ceiling would be a sandbox escape in
// the direction that matters.
func TestASharedTranslationDoesNotCarryAMemoryLimitWithIt(t *testing.T) {
	compiler := requireToolchain(t)
	modules := sharedModules(t)
	artifacts := runcode.NewMemoryCache()
	const source = `
	block := make([]byte, 8<<20)
	for index := range block {
		block[index] = byte(index)
	}
	return []Item{{JSON: map[string]any{"size": float64(len(block))}}}, nil
`

	roomy := runcode.DefaultLimits()
	roomy.MemoryPages = 1024 // 64 MiB
	result, err := runcode.NewRunner(compiler, artifacts, modules, roomy).
		Run(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("Run() under 64 MiB error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["size"] != float64(8<<20) {
		t.Fatalf("result = %#v, want the 8 MiB allocation to have succeeded", result.Items)
	}

	tight := runcode.DefaultLimits()
	tight.MemoryPages = 32 // 2 MiB
	// Same source, so the same artifact and the same translation, reached
	// through the same module cache the runner above filled.
	if _, err := runcode.NewRunner(compiler, artifacts, modules, tight).
		Run(context.Background(), source, nil); err == nil {
		t.Fatal("a module allocated 8 MiB inside a 2 MiB limit")
	}
}

func TestExecutionStopsWhenCancelled(t *testing.T) {
	compiler := requireToolchain(t)
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	limits := runcode.DefaultLimits()
	limits.MemoryPages = 32 // 2 MiB
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), limits)

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
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), runcode.DefaultLimits())

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
	limits := runcode.DefaultLimits()
	limits.MaxOutputBytes = 512
	runner := runcode.NewRunner(compiler, runcode.NewMemoryCache(), sharedModules(t), limits)

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
