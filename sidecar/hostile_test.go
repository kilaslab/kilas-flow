package sidecar

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// hostileCase is one real-Node failure: the fixture, the limits that must
// catch it, and the diagnostic the operator must see.
type hostileCase struct {
	name    string
	fixture string
	heapMB  int
	rssMB   int
	code    string
	detail  string
}

// newHostilePool wraps the real spawn so the test can see how many processes
// were started and reach the last child's pid, and gives each case a wall
// clock long enough that the failure under test is the one that fires.
func newHostilePool(t *testing.T, fixture string, heapMB, rssMB int, timeout time.Duration) (*Pool, func() int, func() *Child) {
	t.Helper()
	base := NewProcessSpawn(ProcessSpec{
		NodePath: sidecartest.Node(t),
		Script:   fixturePath(t, fixture),
		HeapMB:   heapMB,
		MaxRSSMB: rssMB,
		Diag:     NewLogWriter(slog.New(slog.NewTextHandler(io.Discard, nil)), "fixture"),
	})
	var mu sync.Mutex
	spawns := 0
	var last *Child
	wrapped := func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		child, err := base(ctx, tenant, socketPath)
		if err == nil {
			mu.Lock()
			spawns++
			last = child
			mu.Unlock()
		}
		return child, err
	}
	limits := testLimits()
	limits.Timeout = timeout
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(wrapped, limits)
	return pool, func() int {
			mu.Lock()
			defer mu.Unlock()
			return spawns
		}, func() *Child {
			mu.Lock()
			defer mu.Unlock()
			return last
		}
}

// nextRunColdStarts proves the pool did not keep a dead process: the next run
// for the same tenant starts a fresh one and fails the same way.
func nextRunColdStarts(t *testing.T, pool *Pool, spawns func() int, wantCode string) {
	t.Helper()
	before := spawns()
	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.hostile"})
		return err
	}())
	if err.Code != wantCode {
		t.Errorf("second run code = %q, want %q (%s)", err.Code, wantCode, err.Detail)
	}
	if spawns() != before+1 {
		t.Errorf("spawns = %d, want %d: the dead process must not be reused", spawns(), before+1)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d after the second failure, want 0", pool.Live())
	}
}

// A banner and a forged result frame on stdout, plus megabytes of stderr
// noise, must not reach the protocol: the honest socket answer is the one the
// pool returns.
func TestBannerAndForgedFramesOnStdoutNeverEnterTheProtocol(t *testing.T) {
	pool, _, _ := newHostilePool(t, "forged.js", 64, 0, 15*time.Second)
	defer pool.Close()

	result, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.forged"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("result = %+v, want one item", result.Items)
	}
	if result.Items[0].JSON["honest"] != true {
		t.Errorf("result = %+v, want the honest socket answer, not the forged stdout frame", result.Items[0].JSON)
	}
	if _, forged := result.Items[0].JSON["forged"]; forged {
		t.Error("the forged stdout frame became the answer")
	}
}

// A busy child is killed with its process group and is really gone when the
// run returns, not lingering to spin a core.
func TestHungChildIsKilledAndGone(t *testing.T) {
	pool, spawns, last := newHostilePool(t, "hang.js", 64, 0, 1500*time.Millisecond)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.hang"})
		return err
	}())
	if err.Code != CodeSidecarTimeout {
		t.Errorf("code = %q, want %q (%s)", err.Code, CodeSidecarTimeout, err.Detail)
	}
	child := last()
	if child == nil || child.PID <= 0 {
		t.Fatal("no child pid was recorded")
	}
	if err := syscall.Kill(child.PID, 0); err != syscall.ESRCH {
		t.Errorf("kill(%d, 0) = %v, want ESRCH: the hung process must be gone, not lingering", child.PID, err)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0", pool.Live())
	}
	nextRunColdStarts(t, pool, spawns, CodeSidecarTimeout)
}

// A child that exits mid-run is reported with its exit status, taken from the
// wait status rather than anything it printed.
func TestCrashReportsExitStatus(t *testing.T) {
	pool, spawns, _ := newHostilePool(t, "crash.js", 64, 0, 15*time.Second)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.crash"})
		return err
	}())
	if err.Code != CodeSidecarCrash {
		t.Errorf("code = %q, want %q (%s)", err.Code, CodeSidecarCrash, err.Detail)
	}
	if !strings.Contains(err.Detail, "status 3") {
		t.Errorf("detail = %q, want it to carry the child's exit status", err.Detail)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0", pool.Live())
	}
	nextRunColdStarts(t, pool, spawns, CodeSidecarCrash)
}

// A heap overrun is an abort (SIGABRT), and the diagnostic names the heap
// ceiling that caused it.
func TestHeapOverrunReportsTheMemoryLimit(t *testing.T) {
	pool, spawns, _ := newHostilePool(t, "heap.js", 32, 0, 15*time.Second)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.heap"})
		return err
	}())
	if err.Code != CodeMemoryLimit {
		t.Errorf("code = %q, want %q (%s)", err.Code, CodeMemoryLimit, err.Detail)
	}
	if !strings.Contains(err.Detail, "heap ceiling") {
		t.Errorf("detail = %q, want it to name the heap ceiling", err.Detail)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0", pool.Live())
	}
	nextRunColdStarts(t, pool, spawns, CodeMemoryLimit)
}

// The RSS watchdog catches memory that the heap ceiling cannot see: Buffer
// allocations outside V8's old space.
func TestResidentMemoryOverrunIsKilledByTheWatchdog(t *testing.T) {
	pool, spawns, _ := newHostilePool(t, "rss.js", 0, 200, 15*time.Second)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.rss"})
		return err
	}())
	if err.Code != CodeMemoryLimit {
		t.Errorf("code = %q, want %q (%s)", err.Code, CodeMemoryLimit, err.Detail)
	}
	if !strings.Contains(err.Detail, "resident memory") {
		t.Errorf("detail = %q, want the watchdog's measurement", err.Detail)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0", pool.Live())
	}
	nextRunColdStarts(t, pool, spawns, CodeMemoryLimit)
}
