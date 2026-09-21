package idempotency_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func countKeys(t *testing.T, db *database.DB, tenant string) int {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM idempotency_keys WHERE tenant_id = ?", tenant).Scan(&n).Error; err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	return int(n)
}

// The sweeper is what keeps the table from growing for ever on an installation
// nothing retries against. Retention is enforced by the claim as well, so the
// sweeper is allowed to be late — but it must actually run.
func TestSweeperDeletesExpiredKeysOnItsInterval(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := newTestClock(anchor())
		db := open()
		service := newService(t, db, idempotency.Options{Now: clock.Now})
		tenant := idemTenant("sweep")
		key := idemKey("sweep")

		// A key that was completed an hour before the clock's now: expired, and
		// therefore the sweeper's business.
		store := repository.NewIdempotencyStore(db.DB)
		past := anchor().Add(-2 * time.Hour)
		claimed, err := store.Claim(ctx, repository.IdempotencyClaim{
			Tenant:        repository.TenantScope{ID: tenant},
			Key:           key,
			Operation:     "run-workflow",
			RequestHash:   strings.Repeat("a", 64),
			Token:         strings.Repeat("b", 32),
			Now:           past,
			InFlightUntil: past.Add(idempotency.DefaultInFlightWindow),
		})
		if err != nil || !claimed.Acquired {
			t.Fatalf("Claim() = (%+v, %v), want acquired", claimed, err)
		}
		ok, err := store.Complete(ctx, repository.TenantScope{ID: tenant}, key, strings.Repeat("b", 32), 201,
			[]byte(`{"id":"exec_1"}`), past.Add(time.Hour))
		if err != nil || !ok {
			t.Fatalf("Complete() = (%v, %v), want true", ok, err)
		}
		if rows := countKeys(t, db, tenant); rows != 1 {
			t.Fatalf("%d keys seeded, want 1", rows)
		}

		sweeper := idempotency.StartSweeper(ctx, service, 20*time.Millisecond, quietLogger())
		defer sweeper.Stop()

		deadline := time.Now().Add(10 * time.Second)
		for countKeys(t, db, tenant) != 0 {
			if time.Now().After(deadline) {
				t.Fatal("the sweeper left an expired key behind for 10s")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// A sweep in flight must finish before Stop returns, or a shutdown can leave a
// delete running against a database that is about to be closed.
func TestStopWaitsForTheSweepAndIsIdempotent(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := &blockingSweepStore{
			GORMIdempotencyStore: repository.NewIdempotencyStore(open().DB),
			started:              make(chan struct{}),
			release:              make(chan struct{}),
		}
		service := newServiceOver(t, store, idempotency.Options{Now: newTestClock(anchor()).Now})
		sweeper := idempotency.StartSweeper(ctx, service, 5*time.Millisecond, quietLogger())

		select {
		case <-store.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the sweeper never swept")
		}

		stopped := make(chan struct{})
		go func() {
			sweeper.Stop()
			close(stopped)
		}()
		select {
		case <-stopped:
			t.Fatal("Stop() returned while a sweep was still running")
		case <-time.After(50 * time.Millisecond):
		}
		close(store.release)
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Fatal("Stop() did not wait for the running sweep")
		}

		// The composition root defers Stop, and a deferred Stop may sit beside
		// another one on a path that failed half way through.
		again := make(chan struct{})
		go func() {
			sweeper.Stop()
			close(again)
		}()
		select {
		case <-again:
		case <-time.After(10 * time.Second):
			t.Fatal("the second Stop() hung")
		}
		if sweeps := store.sweeps.Load(); sweeps != 1 {
			t.Errorf("the sweeper swept %d times, want 1: it kept going after Stop", sweeps)
		}
	})
}

// blockingSweepStore is a store whose sweep blocks until the test releases it,
// which is the only way to observe what Stop waits for.
type blockingSweepStore struct {
	*repository.GORMIdempotencyStore
	started chan struct{}
	release chan struct{}
	once    sync.Once
	sweeps  atomic.Int32
}

func (s *blockingSweepStore) Sweep(ctx context.Context, now time.Time, batch int) (int64, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	s.sweeps.Add(1)
	return 0, nil
}
