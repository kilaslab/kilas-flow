package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
)

// openRateLimitDB opens one migrated database, SQLite in a temp directory —
// the dialect the repository suite drives the same store patterns on.
func openRateLimitDB(t *testing.T, name string) *database.DB {
	t.Helper()
	return openRateLimitFile(t, filepath.Join(t.TempDir(), name))
}

// openRateLimitFile opens and migrates one SQLite file, the shared helper of
// the store tests: the sharing test opens two handles to the same path.
func openRateLimitFile(t *testing.T, path string) *database.DB {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    path,
	}, quiet)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, quiet); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	return db
}

// The database store spends, refuses and refills like the memory one, so a
// deployment gets the same behaviour whichever store it configured.
func TestTheDatabaseStoreSpendsRefillsAndRefuses(t *testing.T) {
	db := openRateLimitDB(t, "rate-limit.db")
	store := NewGORMStore(db.DB)
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	service := NewService(store, map[string]int{BucketRun: 2}).WithClock(func() time.Time { return clock.now })

	for spend := 0; spend < 2; spend++ {
		allowed, _, err := service.Allow(context.Background(), "acme", BucketRun)
		if err != nil || !allowed {
			t.Fatalf("Allow() #%d = (%v, %v), want it spent", spend+1, allowed, err)
		}
	}
	allowed, retryAfter, err := service.Allow(context.Background(), "acme", BucketRun)
	if err != nil {
		t.Fatalf("Allow(exhausted) error = %v", err)
	}
	if allowed || retryAfter < time.Second {
		t.Fatalf("Allow(exhausted) = (%v, %s), want refused with a whole-second wait", allowed, retryAfter)
	}

	clock.tick(30 * time.Second)
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketRun); !allowed {
		t.Error("Allow() after 30s = false, want the one token two-per-minute refills")
	}

	// Another tenant's bucket is its own row.
	if allowed, _, _ := service.Allow(context.Background(), "globex", BucketRun); !allowed {
		t.Error("another tenant's Allow() = false, want an independent bucket")
	}
}

// Two limiter instances over one database share the allowance: the reason
// the store exists, because separate in-memory buckets behind a load
// balancer each grant the full rate.
func TestTwoLimitersShareTheDatabaseStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate-limit-shared.db")
	db := openRateLimitFile(t, path)
	// A second handle to the same file is the second process.
	second := openRateLimitFile(t, path)
	first := NewService(NewGORMStore(db.DB), map[string]int{BucketEvents: 3})
	other := NewService(NewGORMStore(second.DB), map[string]int{BucketEvents: 3})

	// The first process spends the whole allowance.
	for spend := 0; spend < 3; spend++ {
		allowed, _, err := first.Allow(context.Background(), "acme", BucketEvents)
		if err != nil || !allowed {
			t.Fatalf("the first limiter's spend #%d = (%v, %v)", spend+1, allowed, err)
		}
	}

	// The second process draws from the same bucket and is refused.
	allowed, retryAfter, err := other.Allow(context.Background(), "acme", BucketEvents)
	if err != nil {
		t.Fatalf("the second limiter's Allow() error = %v", err)
	}
	if allowed {
		t.Fatal("the second limiter was allowed, want the shared allowance to be exhausted")
	}
	if retryAfter < time.Second || retryAfter%time.Second != 0 {
		t.Errorf("retryAfter = %s, want a whole number of seconds", retryAfter)
	}

	// An unrelated tenant is unaffected through either handle.
	if allowed, _, _ := other.Allow(context.Background(), "initech", BucketEvents); !allowed {
		t.Error("another tenant's Allow() through the second limiter = false, want its own bucket")
	}
}
