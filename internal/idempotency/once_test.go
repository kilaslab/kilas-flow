package idempotency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
)

// A one-time key is used once across every replica: the second use, even from
// another handle on the same database, is refused.
func TestConsumeOnceAdmitsAKeyOnceOnEveryDriver(t *testing.T) {
	openDrivers(t, func(t *testing.T, open func() *database.DB) {
		clock := newTestClock(anchor())
		first := newService(t, open(), idempotency.Options{Now: clock.Now})
		second := newService(t, open(), idempotency.Options{Now: clock.Now})
		tenant := idemTenant("one-time")
		key := idempotency.OneTimeKey("oauth-state", idemKey("nonce-1"))
		until := clock.Now().Add(10 * time.Minute)

		used, err := first.ConsumeOnce(context.Background(), tenant, key, until)
		if err != nil || !used {
			t.Fatalf("first ConsumeOnce() = %v, %v, want the key admitted", used, err)
		}
		used, err = second.ConsumeOnce(context.Background(), tenant, key, until)
		if err != nil || used {
			t.Fatalf("replayed ConsumeOnce() on another replica = %v, %v, want the key refused", used, err)
		}

		// Another key is its own.
		other := idempotency.OneTimeKey("oauth-state", idemKey("nonce-2"))
		if used, err := second.ConsumeOnce(context.Background(), tenant, other, until); err != nil || !used {
			t.Fatalf("ConsumeOnce(other) = %v, %v, want it admitted", used, err)
		}

		// A key whose window has already closed is never admitted: the thing
		// it stands for has expired with it.
		stale := idempotency.OneTimeKey("oauth-state", idemKey("nonce-3"))
		if used, err := first.ConsumeOnce(context.Background(), tenant, stale, clock.Now().Add(-time.Second)); err != nil || used {
			t.Fatalf("ConsumeOnce(expired) = %v, %v, want it refused", used, err)
		}
	})
}

// One-time keys share the table with Idempotency-Key headers, so they are
// spelled in a way a header can never be: a client cannot pre-claim one.
func TestAOneTimeKeyCannotBeSentAsAnIdempotencyKey(t *testing.T) {
	t.Parallel()
	key := idempotency.OneTimeKey("oauth-state", "abc")
	if err := idempotency.ValidKey(key); !errors.Is(err, idempotency.ErrInvalidKey) {
		t.Fatalf("ValidKey(%q) = %v, want it refused as a header value", key, err)
	}
}
