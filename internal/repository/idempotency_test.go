package repository_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// Every tenant id in this file begins drv-, which is what eachDriver's
// PostgreSQL cleanup deletes, and every key carries a per-run suffix so a row
// a crashed run left behind on a shared server cannot collide with this one.

func idemHash(fill byte) string { return strings.Repeat(string(fill), 64) }

func idemToken(n int) string { return fmt.Sprintf("%032x", n) }

func idemKey(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
}

func idemClaim(tenant, key, token string, now time.Time) repository.IdempotencyClaim {
	return repository.IdempotencyClaim{
		Tenant:        repository.TenantScope{ID: tenant},
		Key:           key,
		Operation:     "run-workflow",
		RequestHash:   idemHash('a'),
		Token:         token,
		Now:           now,
		InFlightUntil: now.Add(2 * time.Minute),
	}
}

func countIdemKeys(t *testing.T, db *database.DB, tenant string) int {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM idempotency_keys WHERE tenant_id = ?", tenant).Scan(&n).Error; err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	return int(n)
}

// Two requests carrying one key that arrive together must not both be told they
// own it. The unique index is the only thing that can decide, and this is the
// property the whole feature stands on.
func TestIdempotencyClaimIsExclusiveOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		key := idemKey(t, "exclusive")
		now := time.Now()

		const contenders = 8
		var (
			start   sync.WaitGroup
			done    sync.WaitGroup
			results [contenders]repository.IdempotencyClaimResult
			errs    [contenders]error
		)
		start.Add(1)
		for i := range contenders {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				results[i], errs[i] = store.Claim(ctx, idemClaim("drv-idem-a", key, idemToken(i+1), now))
			}()
		}
		start.Done()
		done.Wait()

		acquired := 0
		for i, result := range results {
			if errs[i] != nil {
				t.Fatalf("Claim() #%d error = %v", i, errs[i])
			}
			if result.Acquired {
				acquired++
				continue
			}
			if result.Existing.State != repository.IdempotencyInProgress {
				t.Errorf("contender %d saw state %q, want in_progress", i, result.Existing.State)
			}
			if result.Existing.RequestHash != idemHash('a') {
				t.Errorf("contender %d saw hash %q, want the winner's", i, result.Existing.RequestHash)
			}
		}
		if acquired != 1 {
			t.Errorf("%d of %d concurrent claims acquired the key, want exactly 1", acquired, contenders)
		}
		if rows := countIdemKeys(t, db, "drv-idem-a"); rows != 1 {
			t.Errorf("%d rows for one key, want 1", rows)
		}
	})
}

func TestIdempotencyCompletedKeyReplaysItsOutcomeOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenant := repository.TenantScope{ID: "drv-idem-a"}
		key := idemKey(t, "replay")
		now := time.Now()

		first, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), now))
		if err != nil || !first.Acquired {
			t.Fatalf("Claim() = (%+v, %v), want acquired", first, err)
		}
		body := []byte(`{"id":"exec_1","note":"café <b>"}`)
		ok, err := store.Complete(ctx, tenant, key, idemToken(1), 201, body, now.Add(time.Hour))
		if err != nil || !ok {
			t.Fatalf("Complete() = (%v, %v), want true", ok, err)
		}

		replay, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(2), now.Add(time.Minute)))
		if err != nil {
			t.Fatalf("Claim(replay) error = %v", err)
		}
		if replay.Acquired {
			t.Fatal("a completed key was acquired again")
		}
		got := replay.Existing
		if got.State != repository.IdempotencyCompleted || got.Status != 201 || string(got.Body) != string(body) {
			t.Errorf("Existing = %+v, want completed 201 with the recorded body", got)
		}
		if got.Operation != "run-workflow" || got.RequestHash != idemHash('a') {
			t.Errorf("Existing operation/hash = %q/%q, want the first request's", got.Operation, got.RequestHash)
		}

		again, err := store.Complete(ctx, tenant, key, idemToken(1), 200, []byte(`{}`), now.Add(time.Hour))
		if err != nil || again {
			t.Errorf("second Complete() = (%v, %v), want false: the outcome is recorded once", again, err)
		}
		// A recorded outcome is never released, even by the token that wrote it:
		// a deferred cleanup that ran after success must not reopen the key.
		if err := store.Release(ctx, tenant, key, idemToken(1)); err != nil {
			t.Errorf("Release(completed key) error = %v, want nil", err)
		}
		still, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(3), now.Add(time.Minute)))
		if err != nil || still.Existing.Status != 201 {
			t.Errorf("after a refused second Complete the outcome = (%+v, %v), want the first", still.Existing, err)
		}
	})
}

// A request that lost its claim, or never had it, cannot complete or release a
// key. Without the token, a slow first request finishing after a takeover would
// overwrite the new owner's outcome or free a key somebody else is using.
func TestIdempotencyCompleteAndReleaseNeedTheClaimTokenOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenant := repository.TenantScope{ID: "drv-idem-a"}
		key := idemKey(t, "token")
		now := time.Now()

		if res, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), now)); err != nil || !res.Acquired {
			t.Fatalf("Claim() = (%+v, %v), want acquired", res, err)
		}

		if ok, err := store.Complete(ctx, tenant, key, idemToken(2), 201, []byte(`{}`), now.Add(time.Hour)); err != nil || ok {
			t.Errorf("Complete(wrong token) = (%v, %v), want (false, nil)", ok, err)
		}
		if err := store.Release(ctx, tenant, key, idemToken(2)); err != nil {
			t.Errorf("Release(wrong token) error = %v, want nil", err)
		}
		held, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(3), now.Add(time.Second)))
		if err != nil || held.Acquired || held.Existing.State != repository.IdempotencyInProgress {
			t.Fatalf("after wrong-token calls Claim() = (%+v, %v), want the original claim still in progress", held, err)
		}

		if err := store.Release(ctx, tenant, key, idemToken(1)); err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if ok, err := store.Complete(ctx, tenant, key, idemToken(1), 201, []byte(`{}`), now.Add(time.Hour)); err != nil || ok {
			t.Errorf("Complete(after release) = (%v, %v), want (false, nil)", ok, err)
		}
		free, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(4), now.Add(2*time.Second)))
		if err != nil || !free.Acquired {
			t.Errorf("Claim(after release) = (%+v, %v), want acquired", free, err)
		}
		if err := store.Release(ctx, tenant, "never-claimed-"+key, idemToken(1)); err != nil {
			t.Errorf("Release(no such key) error = %v, want nil", err)
		}
	})
}

// An expired row is not a duplicate. The sweeper may be late, so the claim
// itself has to treat a passed expiry as absence; otherwise retention is only as
// exact as the sweep interval.
func TestIdempotencyExpiredKeysAreNotDuplicatesOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenant := repository.TenantScope{ID: "drv-idem-a"}
		key := idemKey(t, "expiry")
		t0 := time.Now().UTC().Truncate(time.Second)

		first, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), t0))
		if err != nil || !first.Acquired {
			t.Fatalf("Claim() = (%+v, %v), want acquired", first, err)
		}

		// Inside the lease the claim holds.
		held, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(2), t0.Add(time.Minute)))
		if err != nil || held.Acquired || held.Existing.State != repository.IdempotencyInProgress {
			t.Fatalf("Claim(inside lease) = (%+v, %v), want in progress", held, err)
		}

		// At the lease boundary it is gone: a stale in-flight claim is taken over.
		takeover, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(3), t0.Add(2*time.Minute)))
		if err != nil || !takeover.Acquired {
			t.Fatalf("Claim(after lease) = (%+v, %v), want the stale claim taken over", takeover, err)
		}
		if ok, err := store.Complete(ctx, tenant, key, idemToken(1), 201, []byte(`{"old":true}`), t0.Add(time.Hour)); err != nil || ok {
			t.Errorf("Complete(old token after takeover) = (%v, %v), want (false, nil)", ok, err)
		}
		retained := t0.Add(2 * time.Minute).Add(time.Hour)
		if ok, err := store.Complete(ctx, tenant, key, idemToken(3), 201, []byte(`{"new":true}`), retained); err != nil || !ok {
			t.Fatalf("Complete(new owner) = (%v, %v), want true", ok, err)
		}

		// Inside retention the completed key replays.
		replay, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(4), retained.Add(-time.Second)))
		if err != nil || replay.Acquired || string(replay.Existing.Body) != `{"new":true}` {
			t.Fatalf("Claim(inside retention) = (%+v, %v), want a replay of the new owner's outcome", replay, err)
		}

		// At the retention boundary it behaves as never seen, and carries none of
		// the old outcome.
		fresh, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(5), retained))
		if err != nil || !fresh.Acquired {
			t.Fatalf("Claim(after retention) = (%+v, %v), want acquired fresh", fresh, err)
		}
		next, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(6), retained.Add(time.Second)))
		if err != nil || next.Acquired || next.Existing.State != repository.IdempotencyInProgress || len(next.Existing.Body) != 0 {
			t.Errorf("Claim(after fresh claim) = (%+v, %v), want an in-progress row with no recorded body", next, err)
		}
	})
}

// The store compares instants, not text. glebarez/sqlite writes a time.Time as
// text in the value's own zone and SQLite compares that text, so a caller whose
// clock is in UTC+7 would stamp '...19:02:00+07:00' and be compared against
// '...12:03:00+00:00' as though the lease were still hours away. The default
// service clock is time.Now, so the caller's zone is the normal case.
func TestIdempotencyKeysIgnoreTheCallersTimeZoneOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenant := repository.TenantScope{ID: "drv-idem-a"}
		wib := time.FixedZone("WIB", 7*3600)
		pst := time.FixedZone("PST", -8*3600)
		t0 := time.Now().UTC().Truncate(time.Second)

		// Written from UTC+7, read from UTC and from UTC-8 either side of the lease.
		key := idemKey(t, "tz-wib")
		if res, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), t0.In(wib))); err != nil || !res.Acquired {
			t.Fatalf("Claim(WIB) = (%+v, %v), want acquired", res, err)
		}
		held, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(2), t0.Add(time.Minute).UTC()))
		if err != nil || held.Acquired {
			t.Errorf("Claim(UTC, inside lease) = (%+v, %v), want held: the lease follows the instant", held, err)
		}
		heldPST, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(3), t0.Add(time.Minute).In(pst)))
		if err != nil || heldPST.Acquired {
			t.Errorf("Claim(PST, inside lease) = (%+v, %v), want held", heldPST, err)
		}
		expired, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(4), t0.Add(3*time.Minute).UTC()))
		if err != nil || !expired.Acquired {
			t.Errorf("Claim(UTC, past lease) = (%+v, %v), want the lease over: a +07:00 text sorts hours late", expired, err)
		}

		// The other direction: written from UTC, read from UTC+7.
		key = idemKey(t, "tz-utc")
		if res, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), t0.UTC())); err != nil || !res.Acquired {
			t.Fatalf("Claim(UTC) = (%+v, %v), want acquired", res, err)
		}
		held, err = store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(2), t0.Add(time.Minute).In(wib)))
		if err != nil || held.Acquired {
			t.Errorf("Claim(WIB, inside lease) = (%+v, %v), want held: a UTC text sorts hours early", held, err)
		}

		// Complete's retention deadline and Sweep's clock follow the instant too.
		key = idemKey(t, "tz-sweep")
		if res, err := store.Claim(ctx, idemClaim(tenant.ID, key, idemToken(1), t0.In(wib))); err != nil || !res.Acquired {
			t.Fatalf("Claim(WIB) = (%+v, %v), want acquired", res, err)
		}
		if ok, err := store.Complete(ctx, tenant, key, idemToken(1), 201, []byte(`{}`), t0.Add(time.Hour).In(wib)); err != nil || !ok {
			t.Fatalf("Complete(WIB) = (%v, %v), want true", ok, err)
		}
		if _, err := store.Sweep(ctx, t0.Add(30*time.Minute).In(pst), 0); err != nil {
			t.Fatalf("Sweep(PST, before retention) error = %v", err)
		}
		if rows := countIdemKeysFor(t, db, tenant.ID, key); rows != 1 {
			t.Errorf("a sweep 30 minutes before retention removed the key (%d rows left), want it kept", rows)
		}
		if _, err := store.Sweep(ctx, t0.Add(61*time.Minute).In(pst), 0); err != nil {
			t.Fatalf("Sweep(PST, after retention) error = %v", err)
		}
		if rows := countIdemKeysFor(t, db, tenant.ID, key); rows != 0 {
			t.Errorf("a sweep 61 minutes after the claim left %d rows, want the key gone", rows)
		}
	})
}

func countIdemKeysFor(t *testing.T, db *database.DB, tenant, key string) int {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM idempotency_keys WHERE tenant_id = ? AND idempotency_key = ?", tenant, key).Scan(&n).Error; err != nil {
		t.Fatalf("count idempotency keys: %v", err)
	}
	return int(n)
}

// One tenant's key says nothing to another. The same string from two tenants
// is two keys, and the read that answers a duplicate never crosses the line.
func TestIdempotencyKeysAreTenantScopedOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenantA := repository.TenantScope{ID: "drv-idem-a"}
		tenantB := repository.TenantScope{ID: "drv-idem-b"}
		key := idemKey(t, "tenant")
		now := time.Now()

		claimA := idemClaim(tenantA.ID, key, idemToken(1), now)
		claimB := idemClaim(tenantB.ID, key, idemToken(2), now)
		claimB.RequestHash = idemHash('b')

		if res, err := store.Claim(ctx, claimA); err != nil || !res.Acquired {
			t.Fatalf("Claim(a) = (%+v, %v), want acquired", res, err)
		}
		if res, err := store.Claim(ctx, claimB); err != nil || !res.Acquired {
			t.Fatalf("Claim(b, same key) = (%+v, %v), want acquired: another tenant's key is not a duplicate", res, err)
		}

		if ok, err := store.Complete(ctx, tenantB, key, idemToken(1), 201, []byte(`{"from":"b-with-a-token"}`), now.Add(time.Hour)); err != nil || ok {
			t.Errorf("Complete(b, a's token) = (%v, %v), want (false, nil)", ok, err)
		}
		if ok, err := store.Complete(ctx, tenantA, key, idemToken(1), 201, []byte(`{"from":"a"}`), now.Add(time.Hour)); err != nil || !ok {
			t.Fatalf("Complete(a) = (%v, %v), want true", ok, err)
		}

		// b's row is still its own, in progress, with its own hash: a's outcome
		// is nowhere in what b can read.
		seenByB, err := store.Claim(ctx, idemClaim(tenantB.ID, key, idemToken(3), now.Add(time.Second)))
		if err != nil || seenByB.Acquired {
			t.Fatalf("Claim(b again) = (%+v, %v), want held by b's own claim", seenByB, err)
		}
		if seenByB.Existing.State != repository.IdempotencyInProgress || len(seenByB.Existing.Body) != 0 {
			t.Errorf("b sees %+v, want its own in-progress row with no body", seenByB.Existing)
		}
		if seenByB.Existing.RequestHash != idemHash('b') {
			t.Errorf("b sees hash %q, want b's own", seenByB.Existing.RequestHash)
		}
		seenByA, err := store.Claim(ctx, idemClaim(tenantA.ID, key, idemToken(4), now.Add(time.Second)))
		if err != nil || seenByA.Existing.State != repository.IdempotencyCompleted || string(seenByA.Existing.Body) != `{"from":"a"}` {
			t.Errorf("a sees (%+v, %v), want its own completed outcome", seenByA.Existing, err)
		}
	})
}

func TestIdempotencySweepDeletesOnlyExpiredKeysInBatchesOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenant := "drv-idem-sweep"
		now := time.Now().UTC().Truncate(time.Second)

		// Clear whatever an earlier run left expired on a shared server, so the
		// count below is this test's rows alone.
		if _, err := store.Sweep(ctx, now, 0); err != nil {
			t.Fatalf("Sweep(prime) error = %v", err)
		}

		seed := func(name string, n int, inFlight time.Duration) []string {
			keys := make([]string, 0, n)
			for i := range n {
				key := idemKey(t, fmt.Sprintf("%s-%d", name, i))
				claim := idemClaim(tenant, key, idemToken(i+1), now)
				claim.InFlightUntil = now.Add(inFlight)
				if res, err := store.Claim(ctx, claim); err != nil || !res.Acquired {
					t.Fatalf("Claim(%s) = (%+v, %v), want acquired", key, res, err)
				}
				keys = append(keys, key)
			}
			return keys
		}
		seed("expired", 5, -time.Hour)
		live := seed("live", 2, time.Hour)

		// A cancelled context stops the sweep before it deletes anything.
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if removed, err := store.Sweep(cancelled, now, 2); !errors.Is(err, context.Canceled) || removed != 0 {
			t.Errorf("Sweep(cancelled) = (%d, %v), want (0, context.Canceled)", removed, err)
		}
		if rows := countIdemKeys(t, db, tenant); rows != 7 {
			t.Fatalf("a cancelled sweep left %d rows, want all 7", rows)
		}

		removed, err := store.Sweep(ctx, now, 2)
		if err != nil {
			t.Fatalf("Sweep() error = %v", err)
		}
		if removed != 5 {
			t.Errorf("Sweep(batch 2) removed %d, want all 5 expired rows across three batches", removed)
		}
		if rows := countIdemKeys(t, db, tenant); rows != 2 {
			t.Errorf("%d rows left, want the 2 live ones", rows)
		}
		for _, key := range live {
			if rows := countIdemKeysFor(t, db, tenant, key); rows != 1 {
				t.Errorf("live key %q has %d rows, want 1", key, rows)
			}
		}

		if again, err := store.Sweep(ctx, now, 2); err != nil || again != 0 {
			t.Errorf("Sweep(again) = (%d, %v), want (0, nil)", again, err)
		}
		if _, err := store.Sweep(ctx, now, -1); err != nil {
			t.Errorf("Sweep(negative batch) error = %v, want the default batch", err)
		}
	})
}

func TestIdempotencyRefusesAnEmptyTenantOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		none := repository.TenantScope{}
		now := time.Now()

		if _, err := store.Claim(ctx, idemClaim("", "k", idemToken(1), now)); err == nil {
			t.Error("Claim(empty tenant) = nil, want an error")
		}
		if _, err := store.Complete(ctx, none, "k", idemToken(1), 201, nil, now); err == nil {
			t.Error("Complete(empty tenant) = nil, want an error")
		}
		if err := store.Release(ctx, none, "k", idemToken(1)); err == nil {
			t.Error("Release(empty tenant) = nil, want an error")
		}
		if _, err := store.PurgeTenant(ctx, none); err == nil {
			t.Error("PurgeTenant(empty tenant) = nil, want an error: a purge that matches nothing by accident is one typo from matching everything")
		}

		malformed := map[string]func(*repository.IdempotencyClaim){
			"empty key":       func(c *repository.IdempotencyClaim) { c.Key = "" },
			"256-byte key":    func(c *repository.IdempotencyClaim) { c.Key = strings.Repeat("k", 256) },
			"short hash":      func(c *repository.IdempotencyClaim) { c.RequestHash = "abc" },
			"empty token":     func(c *repository.IdempotencyClaim) { c.Token = "" },
			"33-byte token":   func(c *repository.IdempotencyClaim) { c.Token = strings.Repeat("t", 33) },
			"empty operation": func(c *repository.IdempotencyClaim) { c.Operation = "" },
			"65-byte op":      func(c *repository.IdempotencyClaim) { c.Operation = strings.Repeat("o", 65) },
		}
		for name, mutate := range malformed {
			claim := idemClaim("drv-idem-a", "k", idemToken(1), now)
			mutate(&claim)
			if _, err := store.Claim(ctx, claim); err == nil {
				t.Errorf("Claim(%s) = nil, want an error", name)
			}
		}
		if rows := countIdemKeys(t, db, "drv-idem-a"); rows != 0 {
			t.Errorf("a refused claim left %d rows behind", rows)
		}
	})
}

func TestIdempotencyPurgeTenantRemovesOnlyThatTenantsKeysOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		store := repository.NewIdempotencyStore(db.DB)
		tenantA := repository.TenantScope{ID: "drv-idem-purge-a"}
		tenantB := repository.TenantScope{ID: "drv-idem-purge-b"}
		now := time.Now()

		for i := range 3 {
			if res, err := store.Claim(ctx, idemClaim(tenantA.ID, idemKey(t, fmt.Sprintf("a%d", i)), idemToken(i+1), now)); err != nil || !res.Acquired {
				t.Fatalf("Claim(a) = (%+v, %v)", res, err)
			}
		}
		if res, err := store.Claim(ctx, idemClaim(tenantB.ID, idemKey(t, "b"), idemToken(9), now)); err != nil || !res.Acquired {
			t.Fatalf("Claim(b) = (%+v, %v)", res, err)
		}

		removed, err := store.PurgeTenant(ctx, tenantA)
		if err != nil || removed != 3 {
			t.Errorf("PurgeTenant(a) = (%d, %v), want (3, nil)", removed, err)
		}
		if rows := countIdemKeys(t, db, tenantA.ID); rows != 0 {
			t.Errorf("tenant a still has %d keys", rows)
		}
		if rows := countIdemKeys(t, db, tenantB.ID); rows != 1 {
			t.Errorf("tenant b has %d keys, want its 1 untouched", rows)
		}
		if again, err := store.PurgeTenant(ctx, tenantA); err != nil || again != 0 {
			t.Errorf("PurgeTenant(retry) = (%d, %v), want (0, nil): a retried deletion converges", again, err)
		}
	})
}

// The execution store's tenant purge is the only repository-level purge, and
// the recorded outcome of a run names the execution it queued, so the keys go
// with the trace.
func TestPurgeTenantAlsoRemovesTheTenantsIdempotencyKeysOnEveryDriver(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		keys := repository.NewIdempotencyStore(db.DB)
		executions := repository.NewExecutionStore(db.DB)
		tenantA := repository.TenantScope{ID: "drv-idem-xpurge-a"}
		tenantB := repository.TenantScope{ID: "drv-idem-xpurge-b"}
		now := time.Now()

		for i := range 2 {
			if res, err := keys.Claim(ctx, idemClaim(tenantA.ID, idemKey(t, fmt.Sprintf("a%d", i)), idemToken(i+1), now)); err != nil || !res.Acquired {
				t.Fatalf("Claim(a) = (%+v, %v)", res, err)
			}
		}
		if res, err := keys.Claim(ctx, idemClaim(tenantB.ID, idemKey(t, "b"), idemToken(9), now)); err != nil || !res.Acquired {
			t.Fatalf("Claim(b) = (%+v, %v)", res, err)
		}

		purged, err := executions.PurgeTenant(ctx, tenantA)
		if err != nil {
			t.Fatalf("PurgeTenant() error = %v", err)
		}
		if purged.IdempotencyKeys != 2 {
			t.Errorf("PurgeTenant() reported %d idempotency keys, want 2", purged.IdempotencyKeys)
		}
		if rows := countIdemKeys(t, db, tenantA.ID); rows != 0 {
			t.Errorf("tenant a still has %d idempotency keys after its purge", rows)
		}
		if rows := countIdemKeys(t, db, tenantB.ID); rows != 1 {
			t.Errorf("tenant b has %d idempotency keys, want its 1 untouched", rows)
		}

		again, err := executions.PurgeTenant(ctx, tenantA)
		if err != nil || again.IdempotencyKeys != 0 {
			t.Errorf("PurgeTenant(retry) = (%+v, %v), want zero keys", again, err)
		}
	})
}

// Every statement the store issues resolves through the model, so
// database.table_prefix applies. A literal table name anywhere in it fails
// here with 'no such table'.
func TestIdempotencyStoreHonoursTheTablePrefix(t *testing.T) {
	t.Parallel()

	db := openPrefixedSQLite(t, "kflow_")
	ctx := context.Background()
	store := repository.NewIdempotencyStore(db.DB)
	tenant := repository.TenantScope{ID: "drv-idem-prefix"}
	now := time.Now()

	if db.Migrator().HasTable("idempotency_keys") {
		t.Error("an unprefixed idempotency_keys table exists after a prefixed migration")
	}
	if !db.Migrator().HasTable("kflow_idempotency_keys") {
		t.Fatal("kflow_idempotency_keys is missing after a prefixed migration")
	}
	countPrefixed := func() int {
		t.Helper()
		var n int64
		if err := db.Raw("SELECT COUNT(*) FROM kflow_idempotency_keys WHERE tenant_id = ?", tenant.ID).Scan(&n).Error; err != nil {
			t.Fatalf("count kflow_idempotency_keys: %v", err)
		}
		return int(n)
	}

	if res, err := store.Claim(ctx, idemClaim(tenant.ID, "k1", idemToken(1), now)); err != nil || !res.Acquired {
		t.Fatalf("Claim() = (%+v, %v), want acquired", res, err)
	}
	if rows := countPrefixed(); rows != 1 {
		t.Fatalf("%d rows in kflow_idempotency_keys after a claim, want 1", rows)
	}
	if ok, err := store.Complete(ctx, tenant, "k1", idemToken(1), 201, []byte(`{}`), now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("Complete() = (%v, %v), want true", ok, err)
	}
	if res, err := store.Claim(ctx, idemClaim(tenant.ID, "k1", idemToken(2), now.Add(time.Second))); err != nil || res.Acquired || res.Existing.Status != 201 {
		t.Fatalf("Claim(replay) = (%+v, %v), want the recorded outcome", res, err)
	}

	if res, err := store.Claim(ctx, idemClaim(tenant.ID, "k2", idemToken(3), now)); err != nil || !res.Acquired {
		t.Fatalf("Claim(k2) = (%+v, %v), want acquired", res, err)
	}
	if err := store.Release(ctx, tenant, "k2", idemToken(3)); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if rows := countPrefixed(); rows != 1 {
		t.Errorf("%d rows after a release, want only k1", rows)
	}

	if removed, err := store.Sweep(ctx, now.Add(2*time.Hour), 0); err != nil || removed != 1 {
		t.Errorf("Sweep() = (%d, %v), want (1, nil)", removed, err)
	}

	if res, err := store.Claim(ctx, idemClaim(tenant.ID, "k3", idemToken(4), now)); err != nil || !res.Acquired {
		t.Fatalf("Claim(k3) = (%+v, %v), want acquired", res, err)
	}
	if removed, err := store.PurgeTenant(ctx, tenant); err != nil || removed != 1 {
		t.Errorf("PurgeTenant() = (%d, %v), want (1, nil)", removed, err)
	}
	if rows := countPrefixed(); rows != 0 {
		t.Errorf("%d rows after a purge, want none", rows)
	}
}
