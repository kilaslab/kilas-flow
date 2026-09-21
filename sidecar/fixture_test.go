package sidecar

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// lockedWriter serialises concurrent child transcripts into one buffer.
type lockedWriter struct {
	mu sync.Mutex
	sb strings.Builder
}

func (writer *lockedWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.sb.Write(payload)
}

func (writer *lockedWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.sb.String()
}

// TestFixtureEchoRunsHeadless runs the real JavaScript fixture in a real
// Node.js process with no operator setup beyond the binary: the pool spawns
// it, the test executes two tenants, and the host stays intact throughout.
// It skips where node is unreachable, the same honest posture as the Code
// node's missing toolchain — a deployment without Node gets the no-sidecar
// diagnostic instead of a failure.
func TestFixtureEchoRunsHeadless(t *testing.T) {
	node := sidecartest.Node(t)
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller refused to say where this test lives")
	}
	script := filepath.Join(filepath.Dir(file), "fixture", "echo.js")

	diagnostics := &lockedWriter{}
	limits := DefaultLimits()
	limits.Timeout = 15 * time.Second
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(NewProcessSpawn(ProcessSpec{NodePath: node, Script: script, HeapMB: 128, Diag: diagnostics}), limits)
	defer pool.Close()

	first, err := pool.Execute(context.Background(), Request{
		Tenant: "fixture-a", Node: "fixture.echo",
		Items: []Item{{JSON: map[string]any{"name": "ada"}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].JSON["greeting"] != "hello, ada" {
		t.Fatalf("result = %+v, want one greeted item", first.Items)
	}

	second, err := pool.Execute(context.Background(), Request{
		Tenant: "fixture-b", Node: "fixture.echo",
		Items: []Item{{JSON: map[string]any{"name": "grace"}}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].JSON["greeting"] != "hello, grace" {
		t.Fatalf("result = %+v, want one greeted item", second.Items)
	}
	if pool.Live() != 2 {
		t.Errorf("Live() = %d, want 2: two tenants hold two processes", pool.Live())
	}

	// The banner reached the diagnostics log, which is the proof it never
	// entered the protocol stream: every frame above decoded cleanly with the
	// banner sitting on the child's stdout the whole time.
	if banner := diagnostics.String(); !strings.Contains(banner, "diagnostics-only") {
		t.Errorf("diagnostics = %q, want the fixture's startup banner", banner)
	}

	_, err = pool.Execute(context.Background(), Request{
		Tenant: "fixture-a", Node: "fixture.echo",
		Items: []Item{{JSON: map[string]any{}}},
	})
	callErr, ok := err.(*CallError)
	if !ok {
		t.Fatalf("Execute() error = %T (%v), want *CallError", err, err)
	}
	if callErr.ChildMessage != "item is missing its name" {
		t.Errorf("child message = %q, want the fixture's own failure", callErr.ChildMessage)
	}
}
