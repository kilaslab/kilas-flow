// Package ratelimit is the per-tenant allowance shared by every ingress a
// tenant can drive: webhook deliveries, the run API and the events API.
//
// One tenant flooding any of them can starve the others — the shared queue
// and the shared worker pool are the scarce things — so the allowance is per
// (tenant, surface), and a refusal happens before any execution or delivery
// claim exists.
//
// The bucket is the token bucket internal/api/middleware's login limiter
// uses, generalised in two ways: the key is a (tenant, bucket) pair rather
// than a free-form string, and the state lives behind a Store so a deployment
// running several processes shares one allowance instead of granting the
// full rate N times.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// The buckets, one per ingress surface. A fixed vocabulary rather than
// free-form names, because each name is also a configuration key and a
// database row: a bucket a caller invented would be a knob nothing reads.
const (
	// BucketWebhook is inbound webhook deliveries, per tenant.
	BucketWebhook = "webhook"
	// BucketRun is the run API, POST /workflows/{id}/run, per tenant.
	BucketRun = "run"
	// BucketEvents is the events API, POST /events, per tenant.
	BucketEvents = "events"
)

// maxTrackedKeys bounds the in-memory store, the same fail-closed ceiling
// maxTrackedLoginKeys holds: past it a key that has never been seen is
// refused rather than tracked, because failing open would hand a flood
// exactly the unlimited budget it is trying to buy. The keys here are
// (tenant, bucket) pairs — a bounded set by construction — so the ceiling is
// defence against a caller bug, not against the vocabulary.
const maxTrackedKeys = 10_000

// Store holds the buckets. Spend performs the refill and the spend atomically
// — two processes sharing the store must not both spend the same token — and
// reports how long a refused caller should wait for one to refill.
type Store interface {
	Spend(ctx context.Context, tenantID, bucket string, capacity float64, refillPerSecond float64, now time.Time) (spent bool, retryAfter time.Duration, err error)
}

// Overrides reads a per-tenant allowance: a positive value replaces the
// deployment default for that tenant and bucket, zero means the default.
//
// An interface rather than a table, because whose settings these are is the
// tenant-settings surface's question, not this package's: the composition
// root wires whatever the deployment stores them in, and a deployment
// without per-tenant settings wires nothing.
type Overrides interface {
	Allowance(tenantID, bucket string) int
}

// Service is the per-(tenant, bucket) rate limiter.
type Service struct {
	store     Store
	overrides Overrides
	defaults  map[string]int
	// now is the clock, replaceable so a test can advance time without
	// sleeping.
	now func() time.Time
}

// NewService builds a limiter over a store with the deployment's default
// allowances, per minute, keyed by bucket name.
func NewService(store Store, defaults map[string]int) *Service {
	copied := make(map[string]int, len(defaults))
	for bucket, perMinute := range defaults {
		copied[bucket] = perMinute
	}
	return &Service{store: store, defaults: copied, now: time.Now}
}

// WithOverrides installs the per-tenant allowance reader. Nil is ignored.
func (service *Service) WithOverrides(overrides Overrides) *Service {
	if overrides != nil {
		service.overrides = overrides
	}
	return service
}

// WithClock replaces the limiter's clock, for tests that move time rather
// than wait for it.
func (service *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		service.now = now
	}
	return service
}

// Allow spends one allowance from a tenant's bucket.
//
// It reports whether the request may proceed and, when it may not, how long
// the caller should wait — rounded up to whole seconds so a Retry-After
// header can carry it. A nil service admits everything, which keeps a
// handler that was built without one working rather than panicking.
//
// A bucket with no configured default admits everything too: a surface this
// deployment does not limit is a surface whose knob was never set, and
// refusing would be a limit nobody configured.
func (service *Service) Allow(ctx context.Context, tenantID, bucket string) (bool, time.Duration, error) {
	if service == nil || service.store == nil {
		return true, 0, nil
	}
	perMinute := service.defaults[bucket]
	if service.overrides != nil {
		// Zero is the honest "no override": an unset row and an absent table
		// mean the same thing, and a negative one would be a stored bug.
		if override := service.overrides.Allowance(tenantID, bucket); override > 0 {
			perMinute = override
		}
	}
	if perMinute <= 0 {
		return true, 0, nil
	}
	capacity := float64(perMinute)
	refill := capacity / time.Minute.Seconds()
	return service.store.Spend(ctx, tenantID, bucket, capacity, refill, service.now())
}

// MemoryStore is the single-process store: one bucket per key, refilled on
// access, pruned as it goes. A deployment running several processes gives
// each the full rate; the database store is what makes them share.
type MemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	pruned  time.Time
}

type bucket struct {
	tokens  float64
	updated time.Time
}

// NewMemoryStore builds the in-process store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{buckets: map[string]*bucket{}}
}

// pruneInterval is how often a pass over the bucket map is worth the work,
// the same on-use cadence the login limiter prunes on: an idle process holds
// no goroutine and no ticker.
const pruneInterval = time.Minute

// Spend refills and spends one token bucket in memory.
func (store *MemoryStore) Spend(_ context.Context, tenantID, bucketName string, capacity, refillPerSecond float64, now time.Time) (bool, time.Duration, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.pruneLocked(now)

	key := tenantID + "\x00" + bucketName
	held, tracked := store.buckets[key]
	if !tracked {
		if len(store.buckets) >= maxTrackedKeys {
			// Fail closed, for the same reason the login limiter does.
			return false, waitFor(1, refillPerSecond), nil
		}
		held = &bucket{tokens: capacity, updated: now}
		store.buckets[key] = held
	} else if elapsed := now.Sub(held.updated).Seconds(); elapsed > 0 {
		held.tokens += elapsed * refillPerSecond
		if held.tokens > capacity {
			held.tokens = capacity
		}
	}
	held.updated = now
	if held.tokens >= 1 {
		held.tokens--
		return true, 0, nil
	}
	return false, waitFor(1-held.tokens, refillPerSecond), nil
}

// pruneLocked drops buckets idle long enough to have refilled entirely. The
// keys are a bounded set, so this is hygiene rather than a leak defence —
// the ceiling above is the defence.
func (store *MemoryStore) pruneLocked(now time.Time) {
	if now.Sub(store.pruned) < pruneInterval {
		return
	}
	store.pruned = now
	idle := 2 * time.Minute
	for key, held := range store.buckets {
		if now.Sub(held.updated) > idle {
			delete(store.buckets, key)
		}
	}
}

// waitFor reports how long it takes to accumulate the given number of
// tokens, rounded up to a whole second so it can be handed to a client as
// Retry-After.
func waitFor(tokens, refillPerSecond float64) time.Duration {
	if tokens <= 0 || refillPerSecond <= 0 {
		return time.Second
	}
	wait := time.Duration(tokens / refillPerSecond * float64(time.Second))
	if remainder := wait % time.Second; remainder != 0 {
		wait += time.Second - remainder
	}
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}
