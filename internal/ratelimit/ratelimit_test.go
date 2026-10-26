package ratelimit

import (
	"context"
	"testing"
	"time"
)

// fakeClock is a clock a test advances by hand.
type fakeClock struct{ now time.Time }

func (clock *fakeClock) tick(d time.Duration) time.Time {
	clock.now = clock.now.Add(d)
	return clock.now
}

// A bucket holds a full minute's allowance, refills at a sixtieth of it per
// second, and refuses with a whole-second Retry-After once it is empty.
func TestTheMemoryStoreSpendsRefillsAndRefuses(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	service := NewService(NewMemoryStore(), map[string]int{BucketRun: 3}).WithClock(func() time.Time { return clock.now })

	for spend := 0; spend < 3; spend++ {
		allowed, _, err := service.Allow(context.Background(), "acme", BucketRun)
		if err != nil || !allowed {
			t.Fatalf("Allow() #%d = (%v, %v), want it spent from the allowance", spend+1, allowed, err)
		}
	}
	allowed, retryAfter, err := service.Allow(context.Background(), "acme", BucketRun)
	if err != nil {
		t.Fatalf("Allow() exhausted error = %v", err)
	}
	if allowed {
		t.Fatal("Allow() after exhaustion = true, want the refusal")
	}
	if retryAfter < time.Second || retryAfter%time.Second != 0 {
		t.Errorf("retryAfter = %s, want a whole number of seconds", retryAfter)
	}

	// Ten seconds refills half a token at three per minute: still refused.
	// Twenty refills one whole one.
	clock.tick(10 * time.Second)
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketRun); allowed {
		t.Error("Allow() after 10s = true, want still refused (half a token refilled)")
	}
	clock.tick(10 * time.Second)
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketRun); !allowed {
		t.Error("Allow() after 20s = false, want one token refilled")
	}

	// Another tenant's allowance is its own, and another bucket's too.
	if allowed, _, _ := service.Allow(context.Background(), "globex", BucketRun); !allowed {
		t.Error("another tenant's Allow() = false, want an independent bucket")
	}
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketWebhook); !allowed {
		t.Error("another bucket's Allow() = false, want an independent bucket")
	}
}

// The per-tenant override replaces the deployment default for that tenant,
// and zero means the default rather than a block.
func TestTheOverrideReplacesTheDefault(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	overrides := map[string]int{"acme\x00" + BucketWebhook: 1}
	service := NewService(NewMemoryStore(), map[string]int{BucketWebhook: 10}).
		WithOverrides(mapOverrides(overrides)).
		WithClock(func() time.Time { return clock.now })

	for spend := 0; spend < 10; spend++ {
		if allowed, _, _ := service.Allow(context.Background(), "globex", BucketWebhook); !allowed {
			t.Fatalf("the default tenant was refused at spend %d of 10", spend+1)
		}
	}
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketWebhook); !allowed {
		t.Fatal("the overridden tenant's first spend was refused, want the allowance of one")
	}
	if allowed, _, _ := service.Allow(context.Background(), "acme", BucketWebhook); allowed {
		t.Fatal("the overridden tenant's second spend was allowed, want the allowance of one to be exhausted")
	}

	// Zero is the honest no-override: the default applies.
	zero := map[string]int{"globex\x00" + BucketWebhook: 0}
	withZero := NewService(NewMemoryStore(), map[string]int{BucketWebhook: 2}).
		WithOverrides(mapOverrides(zero)).
		WithClock(func() time.Time { return clock.now })
	if allowed, _, _ := withZero.Allow(context.Background(), "globex", BucketWebhook); !allowed {
		t.Error("a zero override was read as a refusal, want the default to apply")
	}
}

// mapOverrides adapts a literal map to the Overrides interface, for tests.
type mapOverrides map[string]int

func (overrides mapOverrides) Allowance(tenantID, bucket string) int {
	return overrides[tenantID+"\x00"+bucket]
}

// A bucket this deployment does not limit admits everything, and a nil
// service is the documented off state.
func TestUnconfiguredBucketsAndNilServicesAdmitEverything(t *testing.T) {
	service := NewService(NewMemoryStore(), map[string]int{BucketRun: 1})
	if allowed, _, err := service.Allow(context.Background(), "acme", BucketWebhook); err != nil || !allowed {
		t.Errorf("Allow(unconfigured bucket) = (%v, %v), want unlimited", allowed, err)
	}
	var absent *Service
	if allowed, _, err := absent.Allow(context.Background(), "acme", BucketRun); err != nil || !allowed {
		t.Errorf("nil service Allow() = (%v, %v), want unlimited", allowed, err)
	}
}

// The in-memory store is bounded the way the login limiter is: past the
// ceiling a key that has never been seen is refused rather than tracked,
// because failing open would hand a flood the unlimited budget it is buying.
func TestTheMemoryStoreIsBounded(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	seen := 0
	for key := 0; key < maxTrackedKeys; key++ {
		allowed, _, err := store.Spend(context.Background(), "tenant-"+string(rune('a'+key%26))+string(rune('0'+key/26)), BucketRun, 1, 1.0/60.0, now)
		if err != nil {
			t.Fatalf("Spend() error = %v", err)
		}
		if !allowed {
			t.Fatalf("Spend() #%d was refused before the ceiling", key+1)
		}
		seen++
	}
	allowed, _, err := store.Spend(context.Background(), "one-tenant-too-many", BucketRun, 1, 1.0/60.0, now)
	if err != nil {
		t.Fatalf("Spend(past the ceiling) error = %v", err)
	}
	if allowed {
		t.Error("Spend(past the ceiling) = true, want the fail-closed refusal")
	}
	_ = seen
}
