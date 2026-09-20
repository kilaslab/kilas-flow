package middleware

import (
	"sync"
	"time"
)

// DefaultLoginAttemptsPerMinute is the sustained sign-in allowance for one key.
//
// Ten is chosen for a person rather than for an attacker: a human mistyping a
// password once or twice stays well inside it, while a guesser working through
// a list is cut to ten attempts a minute per account and per address — four
// orders of magnitude below the flood the login endpoint used to admit, and
// cheap enough that refusing costs the process nothing.
const DefaultLoginAttemptsPerMinute = 10

// maxTrackedLoginKeys bounds how many buckets the limiter will hold.
//
// Each bucket is a few words, so ten thousand of them is nothing, and the map
// only reaches the ceiling while a flood is inventing keys faster than they age
// out. Past it a key that has never been seen is refused rather than tracked:
// failing closed costs an unseen caller one wait, where failing open would hand
// the flood exactly the unlimited attempt budget it is trying to buy.
const maxTrackedLoginKeys = 10_000

// loginPruneInterval is how often a pass over the bucket map is worth the work.
//
// Pruning happens on the way through an Allow call rather than on a timer, so
// an idle process holds no goroutine and no ticker.
const loginPruneInterval = time.Minute

// LoginLimiter is a token bucket per key, used to throttle sign-in attempts.
//
// Keys are chosen by the caller — the handler spends one bucket for the client
// address and one for the account being tried — so the same small type covers
// both without either policy being baked in here. A bucket refills at a steady
// rate up to a full minute's allowance, which bounds both the sustained guessing
// rate and the size of any single burst.
//
// The counter is process local. A deployment running several instances gives an
// attacker one allowance per instance per address; the account bucket narrows
// that only because it is per account rather than per address. Nothing here is
// storage-backed on purpose: a sign-in attempt must not be able to make the
// process do a database write, which is the resource this exists to protect.
type LoginLimiter struct {
	// rate is tokens per second; capacity is a full minute's allowance.
	rate     float64
	capacity float64
	// now is the clock, replaceable so a test can advance time without sleeping.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*loginBucket
	pruned  time.Time
}

type loginBucket struct {
	tokens float64
	// updated is when this bucket was last touched, which is what the refill
	// and the pruner both measure from.
	updated time.Time
}

// NewLoginLimiter builds a limiter allowing perMinute attempts a minute per key.
//
// A non-positive rate falls back to DefaultLoginAttemptsPerMinute: a throttle
// that can be switched off by passing zero is one that will be switched off by
// accident.
func NewLoginLimiter(perMinute int) *LoginLimiter {
	if perMinute <= 0 {
		perMinute = DefaultLoginAttemptsPerMinute
	}
	capacity := float64(perMinute)
	return &LoginLimiter{
		rate:     capacity / time.Minute.Seconds(),
		capacity: capacity,
		now:      time.Now,
		buckets:  make(map[string]*loginBucket),
	}
}

// WithClock replaces the limiter's clock.
//
// Refill and pruning are both functions of elapsed time, so a test that proves
// either has to say what "now" is rather than wait for it to pass. A nil clock
// is ignored, and production never calls this: NewLoginLimiter starts a limiter
// on the system clock and nothing in the service changes it.
func (limiter *LoginLimiter) WithClock(now func() time.Time) *LoginLimiter {
	if now != nil {
		limiter.now = now
	}
	return limiter
}

// Allow spends one attempt from a key's bucket.
//
// It reports whether the attempt may proceed, and when it may not, how long the
// caller should wait before trying again — rounded up to whole seconds so a
// Retry-After header can carry it. A nil limiter admits everything, which keeps
// a handler that was built without one working rather than panicking.
func (limiter *LoginLimiter) Allow(key string) (bool, time.Duration) {
	if limiter == nil {
		return true, 0
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.now()
	limiter.pruneLocked(now)

	bucket, tracked := limiter.bucketLocked(key, now)
	if !tracked {
		return false, limiter.waitFor(1)
	}
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	return false, limiter.waitFor(1 - bucket.tokens)
}

// Fail records an attempt that was refused, charging a second token.
//
// A wrong password costs twice what a first attempt does, so a bucket empties
// in half the time when every attempt is a guess. This is the difference
// between "ten tries a minute" and "ten tries a minute, but five mistakes end
// the minute".
func (limiter *LoginLimiter) Fail(key string) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	bucket, tracked := limiter.bucketLocked(key, limiter.now())
	if !tracked {
		return
	}
	if bucket.tokens -= 1; bucket.tokens < 0 {
		bucket.tokens = 0
	}
}

// Succeed clears a key's bucket.
//
// A person who signs in correctly has proved they are not the guesser the
// bucket was counting, so the mistakes they made on the way are forgotten. It
// is called with the account key only: a success must not forgive the address
// bucket, or an attacker holding one working account could reset their own
// address allowance indefinitely.
func (limiter *LoginLimiter) Succeed(key string) {
	if limiter == nil {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	delete(limiter.buckets, key)
}

// bucketLocked refills a key's bucket and returns it.
//
// The second result is false when the key is new and the map is at its ceiling,
// which is a refusal rather than an entry.
func (limiter *LoginLimiter) bucketLocked(key string, now time.Time) (*loginBucket, bool) {
	bucket, found := limiter.buckets[key]
	if !found {
		if len(limiter.buckets) >= maxTrackedLoginKeys {
			return nil, false
		}
		bucket = &loginBucket{tokens: limiter.capacity}
		limiter.buckets[key] = bucket
	} else if elapsed := now.Sub(bucket.updated).Seconds(); elapsed > 0 {
		bucket.tokens += elapsed * limiter.rate
		if bucket.tokens > limiter.capacity {
			bucket.tokens = limiter.capacity
		}
	}
	bucket.updated = now
	return bucket, true
}

// pruneLocked drops buckets that have gone unused for long enough to refill.
//
// A bucket nobody has touched in two refill periods holds no history worth
// keeping, so this is what stops an attacker who sends one request from each of
// a million addresses from leaving a million entries behind for the life of the
// process.
func (limiter *LoginLimiter) pruneLocked(now time.Time) {
	if now.Sub(limiter.pruned) < loginPruneInterval {
		return
	}
	limiter.pruned = now
	idle := 2 * limiter.waitFor(limiter.capacity)
	for key, bucket := range limiter.buckets {
		if now.Sub(bucket.updated) > idle {
			delete(limiter.buckets, key)
		}
	}
}

// waitFor reports how long it takes to accumulate the given number of tokens,
// rounded up to a whole second so it can be handed to a client as Retry-After.
func (limiter *LoginLimiter) waitFor(tokens float64) time.Duration {
	if tokens <= 0 {
		return 0
	}
	wait := time.Duration(tokens / limiter.rate * float64(time.Second))
	if remainder := wait % time.Second; remainder != 0 {
		wait += time.Second - remainder
	}
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}
