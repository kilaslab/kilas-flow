package middleware

// Token-bucket proof: an allowance is spent and then refused, it refills with
// time, a failure costs more than an attempt, a success clears the mistakes,
// idle keys are dropped, and the map has a ceiling so an address-spraying flood
// cannot grow it without limit.

import (
	"strconv"
	"testing"
	"time"
)

// clockedLimiter is a limiter whose time a test controls, so refill and pruning
// are proven without sleeping.
func clockedLimiter(perMinute int) (*LoginLimiter, *time.Time) {
	moment := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(perMinute)
	limiter.now = func() time.Time { return moment }
	return limiter, &moment
}

func TestALoginLimiterSpendsItsAllowanceThenRefuses(t *testing.T) {
	t.Parallel()

	limiter, _ := clockedLimiter(4)
	for attempt := 0; attempt < 4; attempt++ {
		if allowed, _ := limiter.Allow("address:203.0.113.9"); !allowed {
			t.Fatalf("attempt %d was refused inside the allowance", attempt)
		}
	}
	allowed, wait := limiter.Allow("address:203.0.113.9")
	if allowed {
		t.Fatal("a fifth attempt was admitted; the allowance is not being spent")
	}
	// The caller needs to know how long to wait, and it has to be a positive
	// whole number of seconds to be a Retry-After.
	if wait < time.Second {
		t.Errorf("wait = %s, want at least a second", wait)
	}
	if wait%time.Second != 0 {
		t.Errorf("wait = %s, want whole seconds", wait)
	}
	// A second key has its own allowance, which is what keeps one client from
	// spending another's.
	if allowed, _ := limiter.Allow("address:203.0.113.10"); !allowed {
		t.Error("a second key was refused by the first key's spending")
	}
}

func TestALoginLimiterRefillsWithTime(t *testing.T) {
	t.Parallel()

	limiter, moment := clockedLimiter(60) // one token a second
	for attempt := 0; attempt < 60; attempt++ {
		limiter.Allow("account:owner@example.test")
	}
	if allowed, _ := limiter.Allow("account:owner@example.test"); allowed {
		t.Fatal("an attempt was admitted with an empty bucket")
	}

	*moment = moment.Add(3 * time.Second)
	for attempt := 0; attempt < 3; attempt++ {
		if allowed, _ := limiter.Allow("account:owner@example.test"); !allowed {
			t.Fatalf("refilled attempt %d was refused", attempt)
		}
	}
	if allowed, _ := limiter.Allow("account:owner@example.test"); allowed {
		t.Error("a fourth attempt was admitted after three seconds of refill")
	}
}

func TestALoginLimiterChargesAFailureMoreThanAnAttempt(t *testing.T) {
	t.Parallel()

	spent := func(limiter *LoginLimiter, failures bool) int {
		for attempt := 0; ; attempt++ {
			allowed, _ := limiter.Allow("account:owner@example.test")
			if !allowed {
				return attempt
			}
			if failures {
				limiter.Fail("account:owner@example.test")
			}
		}
	}

	guesses, _ := clockedLimiter(10)
	attempts, _ := clockedLimiter(10)
	if failed := spent(guesses, true); failed != 5 {
		t.Errorf("wrong passwords admitted = %d, want half the allowance", failed)
	}
	if tried := spent(attempts, false); tried != 10 {
		t.Errorf("attempts admitted = %d, want the whole allowance", tried)
	}
}

func TestALoginLimiterForgetsMistakesOnSuccess(t *testing.T) {
	t.Parallel()

	limiter, _ := clockedLimiter(4)
	for attempt := 0; attempt < 3; attempt++ {
		limiter.Allow("account:owner@example.test")
		limiter.Fail("account:owner@example.test")
	}
	limiter.Succeed("account:owner@example.test")

	for attempt := 0; attempt < 4; attempt++ {
		if allowed, _ := limiter.Allow("account:owner@example.test"); !allowed {
			t.Fatalf("attempt %d after a success was refused; the counter was not cleared", attempt)
		}
	}
}

func TestALoginLimiterDropsKeysItHasNotSeenInAWhile(t *testing.T) {
	t.Parallel()

	limiter, moment := clockedLimiter(10)
	limiter.Allow("address:203.0.113.9")
	limiter.Allow("address:203.0.113.10")
	if len(limiter.buckets) != 2 {
		t.Fatalf("tracked keys = %d, want 2", len(limiter.buckets))
	}

	// Two refill periods with nothing from the first address: its history is
	// worth nothing, and holding it is what a flood of invented keys would use
	// to grow this map for the life of the process.
	*moment = moment.Add(3 * time.Minute)
	limiter.Allow("address:203.0.113.10")

	limiter.mu.Lock()
	tracked := len(limiter.buckets)
	limiter.mu.Unlock()
	if tracked != 1 {
		t.Errorf("tracked keys = %d, want the idle one dropped", tracked)
	}
}

func TestALoginLimiterRefusesNewKeysAtItsCeiling(t *testing.T) {
	t.Parallel()

	limiter, _ := clockedLimiter(10)
	for key := 0; key < maxTrackedLoginKeys; key++ {
		limiter.Allow("address:10.0." + strconv.Itoa(key/256) + "." + strconv.Itoa(key%256))
	}
	// A flood that keeps inventing keys must not be able to grow this map
	// without limit, and refusing an untracked key is the fail-closed answer:
	// admitting it would hand the flood the unlimited budget it wants.
	if allowed, wait := limiter.Allow("address:203.0.113.250"); allowed || wait <= 0 {
		t.Errorf("a new key at the ceiling = (%t, %s), want a refusal with a wait", allowed, wait)
	}
	// A key already tracked keeps working, so the ceiling does not lock out
	// everyone the moment a flood arrives.
	if allowed, _ := limiter.Allow("address:10.0.0.0"); !allowed {
		t.Error("a tracked key was refused at the ceiling")
	}
}

func TestANilLoginLimiterAdmitsEverything(t *testing.T) {
	t.Parallel()

	var limiter *LoginLimiter
	for attempt := 0; attempt < 100; attempt++ {
		if allowed, _ := limiter.Allow("address:203.0.113.9"); !allowed {
			t.Fatal("a limiter that was never built refused an attempt")
		}
	}
	// The two other calls have to survive the same treatment rather than
	// panicking, because a handler built without a limiter still calls them.
	limiter.Fail("address:203.0.113.9")
	limiter.Succeed("address:203.0.113.9")
}
