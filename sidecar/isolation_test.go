package sidecar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tenancy proof is the ticket's hard rule: one process serves exactly one
// tenant, and a second tenant's run never reaches a process holding the first
// tenant's decrypted credentials. The recorder here is deliberately stronger
// than a self-reported tenant field: it records which SPAWN argument the pool
// passed and appends every raw byte that process received, so a frame
// delivered to the wrong process is visible even though the frame carries the
// right tenant string.

type recordedSpawn struct {
	index  int
	tenant string
	lines  []string
}

type spawnRecorder struct {
	mu     sync.Mutex
	spawns []*recordedSpawn
}

func newSpawnRecorder() *spawnRecorder { return &spawnRecorder{} }

func (rec *spawnRecorder) spawn() SpawnFunc {
	return func(ctx context.Context, tenant, _ string) (*Child, error) {
		host, child := net.Pipe()
		rec.mu.Lock()
		entry := &recordedSpawn{index: len(rec.spawns), tenant: tenant}
		rec.spawns = append(rec.spawns, entry)
		rec.mu.Unlock()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			for {
				line, err := reader.next()
				if err != nil {
					return
				}
				rec.mu.Lock()
				entry.lines = append(entry.lines, string(line))
				rec.mu.Unlock()
				var frame executeFrame
				if err := json.Unmarshal(line, &frame); err != nil {
					return
				}
				_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
			}
		}()
		return &Child{Conn: host, Kill: func() error {
			_ = host.Close()
			_ = child.Close()
			return nil
		}}, nil
	}
}

func (rec *spawnRecorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.spawns)
}

func randomSecret(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return "secret-" + hex.EncodeToString(raw[:])
}

// checkNoCrossTalk is the shared assertion: every process saw only its own
// tenant's frames, no other tenant's secret reached it, and a process whose
// tenant had a secret received it (so an implementation that simply sent
// nothing could not pass).
func checkNoCrossTalk(t *testing.T, rec *spawnRecorder, secrets map[string]string) {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.spawns) == 0 {
		t.Fatal("no sidecar process was spawned")
	}
	for _, spawn := range rec.spawns {
		sawOwn := false
		for _, line := range spawn.lines {
			var frame executeFrame
			if err := json.Unmarshal([]byte(line), &frame); err != nil {
				t.Fatalf("process for %q received bytes that are not a frame: %v", spawn.tenant, err)
			}
			if frame.Tenant != spawn.tenant {
				t.Errorf("process started for %q received a frame carrying tenant %q", spawn.tenant, frame.Tenant)
			}
			for tenant, secret := range secrets {
				if tenant != spawn.tenant && strings.Contains(line, secret) {
					t.Errorf("process for %q received %s's secret", spawn.tenant, tenant)
				}
			}
			if secret, ok := secrets[spawn.tenant]; ok && strings.Contains(line, secret) {
				sawOwn = true
			}
		}
		if _, ok := secrets[spawn.tenant]; ok && !sawOwn {
			t.Errorf("process for %q never received its own secret", spawn.tenant)
		}
	}
}

func runTenant(t *testing.T, pool *Pool, tenant, secret string) error {
	t.Helper()
	_, err := pool.Execute(context.Background(), Request{
		Tenant: tenant, Node: "fixture.echo",
		Secrets: map[string]string{"api_key": secret},
	})
	return err
}

// (a) The warm path: A,B,A,B must reuse two processes and never route one
// tenant's frame to the other's.
func TestEachProcessOnlyEverSeesItsOwnTenantsBytes(t *testing.T) {
	secrets := map[string]string{"tenant-a": randomSecret(t), "tenant-b": randomSecret(t)}
	rec := newSpawnRecorder()
	pool := NewPool(rec.spawn(), testLimits())
	defer pool.Close()

	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-a", "tenant-b"} {
		if err := runTenant(t, pool, tenant, secrets[tenant]); err != nil {
			t.Fatalf("Execute(%s) error = %v", tenant, err)
		}
	}
	if got := rec.count(); got != 2 {
		t.Fatalf("spawns = %d, want 2: each tenant gets exactly one warm process", got)
	}
	checkNoCrossTalk(t, rec, secrets)
}

// (b) The concurrent path: the same assertion under -race, and exactly one
// process per tenant even when 25 goroutines cold-start the same tenant at
// once. Written before the single-flight fix; it fails on the old pool with
// more than 8 spawns.
func TestConcurrentTenantsNeverCrossProcesses(t *testing.T) {
	tenants := 8
	concurrency := 25
	secrets := map[string]string{}
	for i := 0; i < tenants; i++ {
		tenant := fmt.Sprintf("tenant-%d", i)
		secrets[tenant] = randomSecret(t)
	}
	rec := newSpawnRecorder()
	pool := NewPool(rec.spawn(), testLimits())
	defer pool.Close()

	var wait sync.WaitGroup
	failures := make(chan error, tenants*concurrency)
	for i := 0; i < tenants; i++ {
		tenant := fmt.Sprintf("tenant-%d", i)
		for j := 0; j < concurrency; j++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				if err := runTenant(t, pool, tenant, secrets[tenant]); err != nil {
					failures <- err
				}
			}()
		}
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent Execute() error = %v", err)
	}
	if got := rec.count(); got != tenants {
		t.Fatalf("spawns = %d, want %d: N concurrent cold starts for one tenant must spawn once", got, tenants)
	}
	checkNoCrossTalk(t, rec, secrets)
}

// (c) Eviction drops the process; the replacement must hold only the
// evicting tenant's secrets.
func TestEvictedTenantRespawnsWithOnlyItsOwnSecrets(t *testing.T) {
	secrets := map[string]string{"tenant-a": randomSecret(t), "tenant-b": randomSecret(t)}
	rec := newSpawnRecorder()
	pool := NewPool(rec.spawn(), testLimits())
	defer pool.Close()

	if err := runTenant(t, pool, "tenant-a", secrets["tenant-a"]); err != nil {
		t.Fatalf("Execute(tenant-a) error = %v", err)
	}
	if err := runTenant(t, pool, "tenant-b", secrets["tenant-b"]); err != nil {
		t.Fatalf("Execute(tenant-b) error = %v", err)
	}
	pool.Evict("tenant-a")
	if err := runTenant(t, pool, "tenant-a", secrets["tenant-a"]); err != nil {
		t.Fatalf("Execute(tenant-a) after eviction error = %v", err)
	}
	if got := rec.count(); got != 3 {
		t.Fatalf("spawns = %d, want 3: the evicted tenant cold-starts again", got)
	}
	checkNoCrossTalk(t, rec, secrets)
}

// (d) Tenant ids are exact and opaque: trailing space and case are different
// tenants, and an empty tenant fails closed before anything spawns.
func TestTenantIDsAreExactAndOpaque(t *testing.T) {
	secrets := map[string]string{}
	for _, tenant := range []string{"tenant-a", "tenant-a ", "TENANT-A"} {
		secrets[tenant] = randomSecret(t)
	}
	rec := newSpawnRecorder()
	pool := NewPool(rec.spawn(), testLimits())
	defer pool.Close()

	for _, tenant := range []string{"tenant-a", "tenant-a ", "TENANT-A"} {
		if err := runTenant(t, pool, tenant, secrets[tenant]); err != nil {
			t.Fatalf("Execute(%q) error = %v", tenant, err)
		}
	}
	if got := rec.count(); got != 3 {
		t.Fatalf("spawns = %d, want 3: tenant ids are exact, never normalised", got)
	}
	checkNoCrossTalk(t, rec, secrets)

	before := rec.count()
	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeTenantRequired {
		t.Errorf("code = %q, want %q", err.Code, CodeTenantRequired)
	}
	if got := rec.count(); got != before {
		t.Errorf("spawns = %d after an empty tenant, want %d: nothing may start without a tenant", got, before)
	}
}

// A burst of concurrent cold starts for one tenant must spawn exactly once;
// the stragglers wait on the same start instead of racing to spawn spares.
func TestConcurrentColdStartsForOneTenantSpawnOnce(t *testing.T) {
	rec := newSpawnRecorder()
	base := rec.spawn()
	// The cold start is slow enough that the goroutines released together all
	// pile up on the start: without single-flight each would spawn its own.
	spawn := func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		time.Sleep(50 * time.Millisecond)
		return base(ctx, tenant, socketPath)
	}
	pool := NewPool(spawn, testLimits())
	defer pool.Close()

	start := make(chan struct{})
	var wait sync.WaitGroup
	failures := make(chan error, 25)
	secret := randomSecret(t)
	for i := 0; i < 25; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if err := runTenant(t, pool, "tenant-a", secret); err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent Execute() error = %v", err)
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("spawns = %d, want 1: a single-flight cold start", got)
	}
}

// A run queued behind another on the same process must honour its own
// context instead of waiting out the run in front of it.
func TestQueuedRunHonoursItsOwnContext(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseNow()
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			<-release // hold the process until the test lets go
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	first := make(chan error, 1)
	go func() {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		first <- err
	}()
	// Wait until the first run owns the semaphore, then queue a second with a
	// short deadline behind it.
	waitFor(t, time.Second, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		process := pool.procs["tenant-a"]
		return process != nil && process.busy()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := callCode(t, func() error {
		_, err := pool.Execute(ctx, Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	elapsed := time.Since(start)
	if err.Code != CodeSidecarTimeout {
		t.Errorf("code = %q, want %q", err.Code, CodeSidecarTimeout)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not unwrap to context.DeadlineExceeded: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("queued run waited %v: it must honour its own deadline", elapsed)
	}
	releaseNow()
	if err := <-first; err != nil {
		t.Errorf("first run error = %v", err)
	}
}

// CloseIdle must never hold the pool lock while waiting on a process: a
// running node previously stalled every other tenant's cold start behind it.
func TestCloseIdleDoesNotBlockOtherTenantsWhileARunIsInFlight(t *testing.T) {
	release := make(chan struct{})
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			if tenant == "tenant-a" {
				<-release // hold tenant-a's run in flight
			}
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	inFlight := make(chan struct{})
	go func() {
		close(inFlight)
		_, _ = pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
	}()
	<-inFlight
	waitFor(t, time.Second, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		process := pool.procs["tenant-a"]
		return process != nil && process.busy()
	})

	idleDone := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		pool.CloseIdle(time.Now().Add(time.Hour))
		idleDone <- time.Since(start)
	}()

	// A second tenant's cold start must not queue behind the close sweep.
	second := make(chan error, 1)
	go func() {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-b", Node: "fixture.echo"})
		second <- err
	}()
	select {
	case elapsed := <-idleDone:
		if elapsed > 2*time.Second {
			t.Errorf("CloseIdle took %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CloseIdle blocked behind an in-flight run")
	}
	close(release)
	select {
	case err := <-second:
		if err != nil {
			t.Errorf("tenant-b cold start error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tenant-b cold start stalled behind the in-flight run")
	}
}

// CloseIdle must not reap a process whose run is in flight, even when the
// clock says it is idle.
func TestCloseIdleNeverKillsARunningProcess(t *testing.T) {
	release := make(chan struct{})
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			<-release
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{}})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	done := make(chan error, 1)
	go func() {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		done <- err
	}()
	waitFor(t, time.Second, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		process := pool.procs["tenant-a"]
		return process != nil && process.busy()
	})
	pool.CloseIdle(time.Now().Add(time.Hour))
	if pool.Live() != 1 {
		t.Errorf("Live() = %d, want 1: a running process must survive CloseIdle", pool.Live())
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the in-flight run never finished")
	}
}

// A cancelled context kills and evicts the process and surfaces as
// sidecar-cancelled, and a deadline as sidecar-timeout, with the context
// error still reachable through Unwrap.
func TestCancelledContextKillsTheRunAndEvicts(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func(t *testing.T) context.Context
		code string
		want error
	}{
		{name: "cancel", ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(50*time.Millisecond, cancel)
			return ctx
		}, code: CodeCancelled, want: context.Canceled},
		{name: "deadline", ctx: func(t *testing.T) context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			t.Cleanup(cancel)
			return ctx
		}, code: CodeSidecarTimeout, want: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
				host, child := net.Pipe()
				go func() {
					defer child.Close()
					reader := newFrameReader(child, 1<<20)
					_, _ = reader.next()
					// Never answer: only the host's deadline nudges us.
					_, _ = reader.next()
				}()
				return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
			}, testLimits())
			defer pool.Close()

			start := time.Now()
			err := callCode(t, func() error {
				_, err := pool.Execute(test.ctx(t), Request{Tenant: "tenant-a", Node: "fixture.echo"})
				return err
			}())
			if err.Code != test.code {
				t.Errorf("code = %q, want %q (%v)", err.Code, test.code, err.Detail)
			}
			if !errors.Is(err, test.want) {
				t.Errorf("error does not unwrap to %v: %v", test.want, err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("run took %v: the context did not bound it", elapsed)
			}
			if pool.Live() != 0 {
				t.Errorf("Live() = %d, want 0: a cancelled run must evict the process", pool.Live())
			}
		})
	}
}

// The output bound must be reachable: NewPool raises the frame bound above
// the output bound instead of leaving MaxOutputBytes unreachable.
func TestOutputLimitIsReachableBeforeTheFrameLimit(t *testing.T) {
	pool := NewPool(nil, DefaultLimits())
	if pool.limits.MaxFrameBytes <= int(pool.limits.MaxOutputBytes) {
		t.Fatalf("MaxFrameBytes = %d, MaxOutputBytes = %d: the output limit is unreachable",
			pool.limits.MaxFrameBytes, pool.limits.MaxOutputBytes)
	}
}

// A multi-output answer must be charged the same as a single-output one, or
// moving bytes from items into outputs would bypass the limit.
func TestOutputLimitAppliesToMultiOutputResults(t *testing.T) {
	limits := testLimits()
	limits.MaxOutputBytes = 512
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			_ = writeFrame(child, terminalFrame{
				Type: frameResult, ID: frame.ID,
				Items:   []Item{{JSON: map[string]any{"small": true}}},
				Outputs: [][]Item{{{JSON: map[string]any{"blob": strings.Repeat("b", 2<<10)}}}},
			})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, limits)
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeOutputTooLarge {
		t.Errorf("code = %q, want %q", err.Code, CodeOutputTooLarge)
	}
}

// MaxProcesses evicts a least-recently-used idle process rather than refusing
// the new tenant.
func TestMaxProcessesEvictsIdleProcess(t *testing.T) {
	limits := testLimits()
	limits.MaxProcesses = 2
	rec := newSpawnRecorder()
	pool := NewPool(rec.spawn(), limits)
	defer pool.Close()

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if err := runTenant(t, pool, tenant, randomSecret(t)); err != nil {
			t.Fatalf("Execute(%s) error = %v", tenant, err)
		}
	}
	if err := runTenant(t, pool, "tenant-c", randomSecret(t)); err != nil {
		t.Fatalf("Execute(tenant-c) error = %v", err)
	}
	if got := rec.count(); got != 3 {
		t.Errorf("spawns = %d, want 3: the cap evicts an idle process and starts a new one", got)
	}
	if pool.Live() != 2 {
		t.Errorf("Live() = %d, want the cap of 2", pool.Live())
	}
}

// At the cap with every process busy the pool waits for a slot and only then
// names sidecar-busy, instead of making a burst of tenants fail each other.
func TestMaxProcessesWaitsForBusySlotThenBusy(t *testing.T) {
	limits := testLimits()
	limits.MaxProcesses = 1
	limits.SpawnTimeout = 150 * time.Millisecond
	release := make(chan struct{})
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			if _, err := reader.next(); err != nil {
				return
			}
			<-release
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, limits)
	defer pool.Close()
	defer close(release)

	go func() {
		_, _ = pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
	}()
	waitFor(t, time.Second, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		process := pool.procs["tenant-a"]
		return process != nil && process.busy()
	})

	start := time.Now()
	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-b", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeBusy {
		t.Errorf("code = %q, want %q (%s)", err.Code, CodeBusy, err.Detail)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("busy refusal took %v: the pool must wait for a slot before refusing", elapsed)
	}
	if !strings.Contains(err.Detail, "max_processes") {
		t.Errorf("detail = %q, want it to name sidecar.max_processes", err.Detail)
	}
}

// A child that reports a huge message must not grow the host's error object.
func TestChildMessagesAreTruncated(t *testing.T) {
	huge := strings.Repeat("m", 10<<10)
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			_ = writeFrame(child, terminalFrame{Type: frameError, ID: frame.ID, Code: "bad", Message: huge})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if len(err.ChildMessage) > maxChildMessage+len("…(truncated)") {
		t.Errorf("ChildMessage length = %d, want it truncated to %d", len(err.ChildMessage), maxChildMessage)
	}
	if !strings.Contains(err.ChildMessage, "truncated") {
		t.Errorf("ChildMessage = %q, want a truncation marker", err.ChildMessage)
	}
}

// A child that asks for a frame type the host does not serve must not be able
// to fill a log line with a megabyte of type name either.
func TestUnknownFrameTypeIsTruncatedInTheDiagnostic(t *testing.T) {
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			_ = writeFrame(child, map[string]any{"type": strings.Repeat("t", 5<<10), "id": frame.ID})
		}()
		return &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}, nil
	}, testLimits())
	defer pool.Close()

	err := callCode(t, func() error {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo"})
		return err
	}())
	if err.Code != CodeHostCallDenied {
		t.Errorf("code = %q, want %q", err.Code, CodeHostCallDenied)
	}
	if len(err.Detail) > 512 {
		t.Errorf("Detail length = %d, want the frame type truncated", len(err.Detail))
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// shutdownProc builds a proc whose semaphore is free, for tests that
// construct pool states by hand.
func shutdownProc(tenant string) (*proc, func() bool) {
	killed := false
	process := &proc{
		tenant: tenant,
		ready:  make(chan struct{}),
		sem:    make(chan struct{}, 1),
		child: &Child{Kill: func() error {
			killed = true
			return nil
		}},
	}
	close(process.ready)
	return process, func() bool { return killed }
}

// The eviction window in [C3], constructed deterministically: a queued run
// holds a dead P1 while a healthy replacement P2 is what the tenant's key
// points at. Evicting P1 must not touch P2 — the old tenant-keyed evict did.
func TestEvictIsIdentityAware(t *testing.T) {
	pool := NewPool(nil, testLimits())
	defer pool.Close()

	p1, p1Killed := shutdownProc("tenant-a")
	p2, p2Killed := shutdownProc("tenant-a")
	p2.dead.Store(false)

	pool.mu.Lock()
	pool.procs["tenant-a"] = p2
	pool.mu.Unlock()

	pool.evictProc(p1)

	if !p1.dead.Load() || !p1Killed() {
		t.Error("evicting the stale process did not kill it")
	}
	if p2.dead.Load() || p2Killed() {
		t.Error("evicting a stale process killed the healthy replacement")
	}
	pool.mu.Lock()
	current := pool.procs["tenant-a"]
	pool.mu.Unlock()
	if current != p2 {
		t.Error("evicting a stale process removed the healthy replacement from the pool")
	}
}

// Run-level counterpart: a run queued behind a process that dies must not
// fail; it re-resolves and completes on the replacement without killing it.
func TestQueuedRunResolvesAReplacementAfterItsProcessDies(t *testing.T) {
	die := make(chan struct{})
	var mu sync.Mutex
	children := []*Child{}
	pool := NewPool(func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		host, child := net.Pipe()
		mu.Lock()
		index := len(children)
		children = append(children, nil)
		mu.Unlock()
		go func() {
			defer child.Close()
			reader := newFrameReader(child, 1<<20)
			var frame executeFrame
			line, err := reader.next()
			if err != nil {
				return
			}
			if err := json.Unmarshal(line, &frame); err != nil {
				return
			}
			if index == 0 {
				<-die // the first process dies without answering
				return
			}
			_ = writeFrame(child, terminalFrame{Type: frameResult, ID: frame.ID, Items: []Item{{JSON: map[string]any{"replacement": true}}}})
		}()
		entry := &Child{Conn: host, Kill: func() error { _ = host.Close(); _ = child.Close(); return nil }}
		mu.Lock()
		children[index] = entry
		mu.Unlock()
		return entry, nil
	}, testLimits())
	defer pool.Close()

	first := make(chan error, 1)
	go func() {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.replacement"})
		first <- err
	}()
	waitFor(t, time.Second, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		process := pool.procs["tenant-a"]
		return process != nil && process.busy()
	})

	second := make(chan error, 1)
	go func() {
		_, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.replacement"})
		second <- err
	}()
	// Let the second run reach the semaphore, then kill the first process.
	time.Sleep(20 * time.Millisecond)
	close(die)

	if err := <-first; err == nil {
		t.Error("the first run succeeded, want it to fail with the dying process")
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the queued run failed instead of re-resolving: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the queued run never finished")
	}
	if pool.Live() != 1 {
		t.Errorf("Live() = %d, want 1: the replacement process must survive", pool.Live())
	}
}
