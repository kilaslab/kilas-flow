package sdk_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/runcode"
)

// moduleRoot walks up from this file to the directory holding go.mod, so the
// test is independent of where `go test` was invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller refused to say where this test lives")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test file")
		}
		dir = parent
	}
}

// runcode sandbox with no toolchain seam in between.
//
// It skips where no Go toolchain is reachable, the same posture the Code node
// itself takes in the distroless image — a deployment that cannot compile
// reports that instead of failing.
func TestExamplePackRunsUnderWazero(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no Go toolchain reachable; compiling the example is impossible here")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	wasmPath := filepath.Join(t.TempDir(), "echo.wasm")
	build := exec.CommandContext(ctx, goBinary, "build",
		"-o", wasmPath, "./pkg/sdk/example/echo")
	build.Dir = moduleRoot(t)
	build.Env = append(os.Environ(),
		"GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example pack: %v\n%s", err, output)
	}
	module, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read built pack: %v", err)
	}
	if len(module) == 0 {
		t.Fatal("built pack is empty")
	}

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), nil, runcode.DefaultLimits())
	artifact := runcode.Artifact{
		Hash:           "sdk-example-echo",
		RuntimeVersion: runcode.RuntimeVersion,
		Module:         module,
	}

	result, err := runner.Execute(ctx, artifact, []runcode.Item{{JSON: map[string]any{"name": "ada"}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["greeting"] != "hello, ada" {
		t.Fatalf("result = %+v, want one greeted item", result.Items)
	}

	_, err = runner.Execute(ctx, artifact, []runcode.Item{{JSON: map[string]any{}}})
	if err == nil || !strings.Contains(err.Error(), "missing its name") {
		t.Fatalf("Execute() error = %v, want the pack's own failure message", err)
	}
}
