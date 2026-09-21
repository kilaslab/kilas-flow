package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// untouchedStore is a store the hash tests must not reach: the hash is pure,
// and a test that opened a database to compute one would be testing something
// else.
type untouchedStore struct{}

func (untouchedStore) Claim(context.Context, repository.IdempotencyClaim) (repository.IdempotencyClaimResult, error) {
	panic("the request hash must not touch the store")
}

func (untouchedStore) Complete(context.Context, repository.TenantScope, string, string, int, []byte, time.Time) (bool, error) {
	panic("the request hash must not touch the store")
}

func (untouchedStore) Release(context.Context, repository.TenantScope, string, string) error {
	panic("the request hash must not touch the store")
}

func (untouchedStore) Sweep(context.Context, time.Time, int) (int64, error) {
	panic("the request hash must not touch the store")
}

func (untouchedStore) PurgeTenant(context.Context, repository.TenantScope) (int64, error) {
	panic("the request hash must not touch the store")
}

func hashService(t *testing.T) *idempotency.Service {
	t.Helper()
	service, err := idempotency.NewService(untouchedStore{}, idempotency.Options{Retention: time.Hour})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// A client that re-serialises its retry — different whitespace, different key
// order — sends the same request, and must not be told it conflicted with
// itself.
func TestTheRequestHashIgnoresKeyOrderAndWhitespace(t *testing.T) {
	service := hashService(t)

	canonical, err := service.Hash("tenant-a", "key-1", jsonBody(`{"a":1,"b":{"c":3,"d":2}}`))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if len(canonical) != 64 {
		t.Errorf("Hash() = %q (%d chars), want a 64-character sha256 hex digest", canonical, len(canonical))
	}

	for name, body := range map[string]string{
		"key order":  `{"b":{"d":2,"c":3},"a":1}`,
		"whitespace": "{\n  \"b\": {\"c\": 3, \"d\": 2},\n  \"a\": 1\n}",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := service.Hash("tenant-a", "key-1", jsonBody(body))
			if err != nil {
				t.Fatalf("Hash() error = %v", err)
			}
			if got != canonical {
				t.Errorf("Hash() = %q, want the canonical body's %q", got, canonical)
			}
		})
	}

	different, err := service.Hash("tenant-a", "key-1", jsonBody(`{"a":1,"b":{"c":3,"d":4}}`))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if different == canonical {
		t.Error("two different bodies hashed the same")
	}
}

// The canonical form must not round-trip numbers through float64: 1 and 1.0
// are different requests, and an integer above 2^53 is still an integer.
func TestTheRequestHashKeepsNumbersExact(t *testing.T) {
	service := hashService(t)

	hash := func(t *testing.T, body string) string {
		t.Helper()
		got, err := service.Hash("tenant-a", "key-1", jsonBody(body))
		if err != nil {
			t.Fatalf("Hash() error = %v", err)
		}
		return got
	}

	if hash(t, `{"n":1}`) == hash(t, `{"n":1.0}`) {
		t.Error("1 and 1.0 hashed the same")
	}
	// 9007199254740993 is not representable as a float64; 9007199254740992 is.
	if hash(t, `{"n":9007199254740993}`) == hash(t, `{"n":9007199254740992}`) {
		t.Error("a large integer was rounded through float64")
	}
	if hash(t, `{"n":1}`) != hash(t, `{"n":1}`) {
		t.Error("the same body hashed differently twice")
	}
}

// The hash is salted with what the key means, so the same body under two keys,
// two operations, two targets or two tenants is never the same hash — which is
// what makes a cross-resource reuse a visible conflict instead of a replay.
func TestTheRequestHashSeparatesOperationTargetTenantAndKey(t *testing.T) {
	service := hashService(t)

	variants := map[string]struct {
		tenant string
		key    string
		req    idempotency.Request
	}{
		"base":      {"tenant-a", "key-1", idempotency.Request{Operation: "run-workflow", Target: "wf_1", Body: map[string]any{"input": "same"}}},
		"tenant":    {"tenant-b", "key-1", idempotency.Request{Operation: "run-workflow", Target: "wf_1", Body: map[string]any{"input": "same"}}},
		"key":       {"tenant-a", "key-2", idempotency.Request{Operation: "run-workflow", Target: "wf_1", Body: map[string]any{"input": "same"}}},
		"operation": {"tenant-a", "key-1", idempotency.Request{Operation: "insert-row", Target: "wf_1", Body: map[string]any{"input": "same"}}},
		"target":    {"tenant-a", "key-1", idempotency.Request{Operation: "run-workflow", Target: "wf_2", Body: map[string]any{"input": "same"}}},
	}

	seen := make(map[string]string, len(variants))
	for name, variant := range variants {
		got, err := service.Hash(variant.tenant, variant.key, variant.req)
		if err != nil {
			t.Fatalf("Hash(%s) error = %v", name, err)
		}
		if other, duplicate := seen[got]; duplicate {
			t.Errorf("%s and %s hashed the same", name, other)
		}
		seen[got] = name
	}
	if len(seen) != len(variants) {
		t.Errorf("%d distinct hashes for %d variants", len(seen), len(variants))
	}
}
