package jsworker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// served records the tenant every worker was handed a job for, and fails
// the test the moment one is handed a second tenant's.
type served struct {
	t       *testing.T
	mu      sync.Mutex
	tenants map[*worker]string
	order   []*worker
	taken   chan *worker
}

func watchTenants(t *testing.T, pool *testPool) *served {
	record := &served{t: t, tenants: map[*worker]string{}, taken: make(chan *worker, 256)}
	pool.took = func(w *worker, tenant string) {
		record.mu.Lock()
		defer record.mu.Unlock()
		if earlier, ok := record.tenants[w]; ok && earlier != tenant {
			t.Errorf("a worker that ran a job of tenant %q was handed one of tenant %q", earlier, tenant)
		}
		if _, ok := record.tenants[w]; !ok {
			record.order = append(record.order, w)
		}
		record.tenants[w] = tenant
		select {
		case record.taken <- w:
		default:
		}
	}
	return record
}

// workers are the distinct workers handed a job, in the order each was
// first handed one.
func (record *served) workers() []*worker {
	record.mu.Lock()
	defer record.mu.Unlock()
	return append([]*worker(nil), record.order...)
}

func exitedWithin(w *worker, wait time.Duration) bool {
	select {
	case <-w.exited:
		return true
	case <-time.After(wait):
		return false
	}
}

func runFor(t *testing.T, pool *testPool, tenant string) {
	t.Helper()
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("x"), Tenant: tenant}); err != nil {
		t.Fatalf("Run() for tenant %q error = %v", tenant, err)
	}
}

// However the jobs of several tenants interleave, and whichever workers the
// cap makes them share out, no worker ever runs two tenants' jobs. The empty
// tenant, a deployment with none, is a tenant of its own.
func TestAWorkerNeverRunsTwoTenantsJobs(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 3})
	record := watchTenants(t, pool)
	tenants := []string{"tenant-a", "tenant-b", "tenant-c", "tenant-d", ""}
	var wait sync.WaitGroup
	errs := make(chan error, 40)
	for index := range 40 {
		wait.Go(func() {
			_, err := pool.Run(context.Background(), jsrun.Task{
				Source: "const until = Date.now() + 5\nwhile (Date.now() < until) {}\nreturn items",
				Items:  items("x"), Tenant: tenants[index%len(tenants)],
			})
			errs <- err
		})
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if len(record.workers()) < len(tenants) {
		t.Fatalf("%d workers ran the jobs of %d tenants", len(record.workers()), len(tenants))
	}
}

// Under the cap, each tenant's idle worker is kept for its next job. At the
// cap, a tenant with no worker of its own takes the place of the worker that
// has been idle longest, which is stopped, and the others stay warm.
func TestAtTheCapTheLongestIdleWorkerOfAnotherTenantMakesWay(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 2})
	record := watchTenants(t, pool)
	runFor(t, pool, "tenant-a")
	runFor(t, pool, "tenant-b")
	runFor(t, pool, "tenant-a")
	if starts := pool.started(); starts != 2 {
		t.Fatalf("%d workers started for two tenants under a cap of two, want one each", starts)
	}
	workers := record.workers()
	ofA, ofB := workers[0], workers[1]

	// tenant-b's worker has now been idle longest.
	runFor(t, pool, "tenant-c")
	if starts := pool.started(); starts != 3 {
		t.Fatalf("%d workers started, want a fresh one for tenant-c", starts)
	}
	if !exitedWithin(ofB, 3*time.Second) {
		t.Fatal("tenant-b's idle worker was not stopped to make way for tenant-c's")
	}
	if !ofA.alive() {
		t.Fatal("tenant-a's worker, idle for less long, was stopped")
	}
	runFor(t, pool, "tenant-a")
	if starts := pool.started(); starts != 3 {
		t.Fatalf("%d workers started, want tenant-a's own worker reused", starts)
	}
}

// A job that waits for a slot while another tenant's job holds the only one
// is served when that job ends, on a worker of its own.
func TestAJobWaitingOnAnotherTenantsWorkerIsServed(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 1})
	record := watchTenants(t, pool)
	busy := make(chan error, 1)
	go func() {
		_, err := pool.Run(context.Background(), jsrun.Task{
			Source: "const until = Date.now() + 300\nwhile (Date.now() < until) {}\nreturn items", Items: items("a"), Tenant: "tenant-a",
		})
		busy <- err
	}()
	var ofA *worker
	select {
	case ofA = <-record.taken:
	case <-time.After(10 * time.Second):
		t.Fatal("tenant-a's job never started")
	}
	result, err := pool.Run(context.Background(), jsrun.Task{Source: "return [{ json: { served: true } }]", Tenant: "tenant-b"})
	if err != nil || len(result.Items) != 1 || result.Items[0].JSON["served"] != true {
		t.Fatalf("tenant-b: Run() = %#v, %v", result.Items, err)
	}
	if err := <-busy; err != nil {
		t.Fatalf("tenant-a: Run() error = %v", err)
	}
	if starts := pool.started(); starts != 2 {
		t.Fatalf("%d workers started, want one per tenant", starts)
	}
	if !exitedWithin(ofA, 3*time.Second) {
		t.Fatal("tenant-a's worker was kept past the cap of one")
	}
}

// A tenant's worker is still replaced after its job cap, by a fresh worker
// for the same tenant.
func TestATenantsWorkerIsStillReplacedAfterItsJobCap(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 1, MaxRuns: 2})
	watchTenants(t, pool)
	for range 5 {
		runFor(t, pool, "tenant-a")
	}
	if starts := pool.started(); starts != 3 {
		t.Fatalf("%d workers started for five jobs at two a worker, want three", starts)
	}
}

// The worker Start starts has run no one's code, so the first job claims it
// whichever tenant it runs for, and it is that tenant's from then on.
func TestTheWorkerStartStartedServesTheFirstTenant(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 1})
	record := watchTenants(t, pool)
	if err := pool.Start(); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	runFor(t, pool, "tenant-a")
	if starts := pool.started(); starts != 1 {
		t.Fatalf("%d workers started, want the one Start started reused", starts)
	}
	runFor(t, pool, "")
	if starts := pool.started(); starts != 2 {
		t.Fatalf("%d workers started, want a fresh one for a job with no tenant", starts)
	}
	if got := fmt.Sprint(len(record.workers())); got != "2" {
		t.Fatalf("%s workers ran jobs, want two", got)
	}
}
