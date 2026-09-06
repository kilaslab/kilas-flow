package sidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// serveEcho is the in-process twin of fixture/echo.js: same frames, same
// greeting rule, same bad-input message. The Go fakes below and the real Node
// fixture prove the same contract from opposite sides of the boundary.
func serveEcho(conn net.Conn) {
	defer conn.Close()
	reader := newFrameReader(conn, 1<<20)
	for {
		line, err := reader.next()
		if err != nil {
			return
		}
		var frame executeFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			return
		}
		items := make([]Item, 0, len(frame.Items))
		failed := ""
		for _, item := range frame.Items {
			name, _ := item.JSON["name"].(string)
			if strings.TrimSpace(name) == "" {
				failed = "item is missing its name"
				break
			}
			items = append(items, Item{JSON: map[string]any{"name": name, "greeting": "hello, " + name}})
		}
		if failed != "" {
			_ = writeFrame(conn, terminalFrame{Type: frameError, ID: frame.ID, Code: "bad-input", Message: failed})
			continue
		}
		_ = writeFrame(conn, terminalFrame{Type: frameResult, ID: frame.ID, Items: items})
	}
}

type fakeWorld struct {
	spawns   atomic.Int32
	kills    atomic.Int32
	mu       sync.Mutex
	byTenant map[string][]string
}

func (world *fakeWorld) spawn(handler func(net.Conn)) SpawnFunc {
	if world.byTenant == nil {
		world.byTenant = map[string][]string{}
	}
	return func(ctx context.Context, tenant, _ string) (*Child, error) {
		world.spawns.Add(1)
		host, child := net.Pipe()
		go handler(child)
		return &Child{Conn: host, Kill: func() error {
			world.kills.Add(1)
			_ = host.Close()
			_ = child.Close()
			return nil
		}}, nil
	}
}

// captureSpawn records each tenant's raw request lines per connection, so the
// isolation test can prove which bytes reached which process.
func (world *fakeWorld) captureSpawn() SpawnFunc {
	return world.spawn(func(conn net.Conn) {
		defer conn.Close()
		reader := newFrameReader(conn, 1<<20)
		for {
			line, err := reader.next()
			if err != nil {
				return
			}
			var frame executeFrame
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			world.mu.Lock()
			world.byTenant[frame.Tenant] = append(world.byTenant[frame.Tenant], string(line))
			world.mu.Unlock()
			_ = writeFrame(conn, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}
	})
}

func testLimits() Limits {
	limits := DefaultLimits()
	limits.Timeout = 2 * time.Second
	limits.SpawnTimeout = 2 * time.Second
	limits.IdleTimeout = time.Minute
	return limits
}

func callCode(t *testing.T, err error) *CallError {
	t.Helper()
	if err == nil {
		t.Fatal("Execute() succeeded, want a named failure")
	}
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("Execute() error = %T (%v), want *CallError", err, err)
	}
	return callErr
}

func TestExecuteEchoRoundTripAndWarmReuse(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(serveEcho), testLimits())
	defer pool.Close()

	for i := 0; i < 2; i++ {
		result, err := pool.Execute(context.Background(), Request{
			Tenant: "tenant-a", Node: "fixture.echo",
			Items: []Item{{JSON: map[string]any{"name": "ada"}}},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if len(result.Items) != 1 || result.Items[0].JSON["greeting"] != "hello, ada" {
			t.Fatalf("result = %+v, want one greeted item", result.Items)
		}
	}
	if got := world.spawns.Load(); got != 1 {
		t.Errorf("spawns = %d, want 1: the second run must reuse the warm process", got)
	}
}

func TestSecondTenantNeverReachesFirstTenantsProcess(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.captureSpawn(), testLimits())
	defer pool.Close()

	for _, req := range []Request{
		{Tenant: "tenant-a", Node: "fixture.echo", Secrets: map[string]string{"api_key": "SECRET-A"}},
		{Tenant: "tenant-b", Node: "fixture.echo", Secrets: map[string]string{"api_key": "SECRET-B"}},
	} {
		if _, err := pool.Execute(context.Background(), req); err != nil {
			t.Fatalf("Execute(%s) error = %v", req.Tenant, err)
		}
	}

	if got := world.spawns.Load(); got != 2 {
		t.Fatalf("spawns = %d, want 2: each tenant gets its own process", got)
	}
	world.mu.Lock()
	defer world.mu.Unlock()
	if len(world.byTenant) != 2 {
		t.Fatalf("requests reached %d processes, want 2", len(world.byTenant))
	}
	for tenant, lines := range world.byTenant {
		if len(lines) != 1 {
			t.Errorf("tenant %s saw %d requests, want exactly its own one", tenant, len(lines))
			continue
		}
		line := lines[0]
		if !strings.Contains(line, `"tenant":"`+tenant+`"`) {
			t.Errorf("process for %s received a foreign request: %s", tenant, line)
		}
		for other, secret := range map[string]string{"tenant-a": "SECRET-A", "tenant-b": "SECRET-B"} {
			if other != tenant && strings.Contains(line, secret) {
				t.Errorf("process for %s received %s's secret", tenant, other)
			}
		}
		if tenant == "tenant-a" && !strings.Contains(line, "SECRET-A") {
			t.Error("tenant-a's process never received tenant-a's secret")
		}
	}
}

func TestCrashFailsTheRunAndEvicts(t *testing.T) {
	world := &fakeWorld{}
	crashes := atomic.Int32{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		reader := newFrameReader(conn, 1<<20)
		if _, err := reader.next(); err != nil {
			return
		}
		// Die without answering: no frame, just gone.
		crashes.Add(1)
	}), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeSidecarCrash {
		t.Errorf("code = %q, want %q", err.Code, CodeSidecarCrash)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d after a crash, want 0: the dead process must be evicted", pool.Live())
	}
	if got := crashes.Load(); got != 1 {
		t.Errorf("child deaths = %d, want 1", got)
	}
}

func TestCrashRespawnsOnNextRun(t *testing.T) {
	world := &fakeWorld{}
	first := atomic.Bool{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		if first.CompareAndSwap(false, true) {
			return // first process dies on connect, before any request
		}
		serveEcho(conn)
	}), testLimits())
	defer pool.Close()

	// First execute may fail (raced the dying process) or may already have a
	// fresh one; either way the pool must converge on a working process.
	_, _ = pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
	result, err := pool.Execute(context.Background(), Request{
		Tenant: "tenant-a", Node: "fixture.echo",
		Items: []Item{{JSON: map[string]any{"name": "ada"}}},
	})
	if err != nil {
		t.Fatalf("Execute() after crash error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("result = %+v, want one item from the replacement process", result.Items)
	}
}

func TestHangFailsTheRunWithATimeout(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		// Read the request, then never answer. The second read unblocks only
		// when the host kills the connection, so the fake dies with its process.
		reader := newFrameReader(conn, 1<<20)
		if _, err := reader.next(); err != nil {
			return
		}
		_, _ = reader.next()
	}), testLimits())
	defer pool.Close()
	pool.limits.Timeout = 200 * time.Millisecond

	start := time.Now()
	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeSidecarTimeout {
		t.Errorf("code = %q, want %q", err.Code, CodeSidecarTimeout)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("hung run took %v: the timeout did not bound it", elapsed)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d after a hang, want 0: the hung process must be killed and evicted", pool.Live())
	}
	if got := world.kills.Load(); got != 1 {
		t.Errorf("kills = %d, want 1: a hung process must be killed, not left lingering", got)
	}
}

func TestOversizeFrameFailsTheRun(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		reader := newFrameReader(conn, 1<<20)
		if _, err := reader.next(); err != nil {
			return
		}
		_ = writeFrame(conn, terminalFrame{Type: frameResult, ID: "x", Items: []Item{{JSON: map[string]any{"blob": strings.Repeat("b", 2<<20)}}}})
	}), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeFrameTooLarge {
		t.Errorf("code = %q, want %q", err.Code, CodeFrameTooLarge)
	}
}

func TestOversizeOutputFailsTheRun(t *testing.T) {
	world := &fakeWorld{}
	limits := testLimits()
	// The greeting payload is tens of bytes; ten fails it while leaving the
	// frame itself far under the frame limit, isolating the output bound.
	limits.MaxOutputBytes = 10
	pool := NewPool(world.spawn(serveEcho), limits)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{
			Tenant: "tenant-a", Node: "fixture.echo",
			Items: []Item{{JSON: map[string]any{"name": "ada"}}},
		})
		return err
	}())
	if err.Code != CodeOutputTooLarge {
		t.Errorf("code = %q, want %q", err.Code, CodeOutputTooLarge)
	}
}

func TestHostCallIsDeniedByDefault(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		reader := newFrameReader(conn, 1<<20)
		line, err := reader.next()
		if err != nil {
			return
		}
		var frame executeFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			return
		}
		// Reach the network by another route: ask the host for an HTTP call.
		_ = writeFrame(conn, map[string]any{"type": "http.request", "id": frame.ID, "url": "https://example.com"})
	}), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeHostCallDenied {
		t.Errorf("code = %q, want %q: the boundary refuses rather than silently widening", err.Code, CodeHostCallDenied)
	}
	if pool.Live() != 0 {
		t.Errorf("Live() = %d, want 0: a process that reaches past the boundary is not trusted again", pool.Live())
	}
}

func TestAnswerForAnotherRunIsAProtocolViolation(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(func(conn net.Conn) {
		defer conn.Close()
		reader := newFrameReader(conn, 1<<20)
		if _, err := reader.next(); err != nil {
			return
		}
		_ = writeFrame(conn, terminalFrame{Type: frameResult, ID: "call-someone-else", Items: []Item{}})
	}), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeProtocolViolation {
		t.Errorf("code = %q, want %q", err.Code, CodeProtocolViolation)
	}
}

func TestRunWithoutTenantFailsClosed(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(serveEcho), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeTenantRequired {
		t.Errorf("code = %q, want %q", err.Code, CodeTenantRequired)
	}
	if got := world.spawns.Load(); got != 0 {
		t.Errorf("spawns = %d, want 0: no process may start without a tenant", got)
	}
}

func TestDeclinedSidecarFailsWithADiagnostic(t *testing.T) {
	pool := NewPool(nil, testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeNoSidecar {
		t.Errorf("code = %q, want %q", err.Code, CodeNoSidecar)
	}
	if !strings.Contains(err.Detail, "Node 24") {
		t.Errorf("detail = %q, want the diagnostic to name what the operator must provide", err.Detail)
	}
}

func TestChildFailureSurfacesItsOwnMessage(t *testing.T) {
	world := &fakeWorld{}
	pool := NewPool(world.spawn(serveEcho), testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{
			Tenant: "tenant-a", Node: "fixture.echo",
			Items: []Item{{JSON: map[string]any{}}},
		})
		return err
	}())
	if !strings.Contains(err.Detail, "item is missing its name") {
		t.Errorf("detail = %q, want the node's own failure message", err.Detail)
	}
	// A node-level failure is not a process failure: the process stays warm.
	if pool.Live() != 1 {
		t.Errorf("Live() = %d, want 1: a failing node must not evict a healthy process", pool.Live())
	}
}

func TestCloseIdleReapsQuietProcesses(t *testing.T) {
	world := &fakeWorld{}
	limits := testLimits()
	limits.IdleTimeout = 50 * time.Millisecond
	pool := NewPool(world.spawn(serveEcho), limits)
	defer pool.Close()

	if _, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	pool.CloseIdle(time.Now().Add(time.Second))
	if pool.Live() != 0 {
		t.Errorf("Live() = %d after idle timeout, want 0", pool.Live())
	}
	if got := world.kills.Load(); got != 1 {
		t.Errorf("kills = %d, want 1", got)
	}
}
