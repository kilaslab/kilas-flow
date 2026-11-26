package tenantsettings

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingLoader is a store of one document per tenant that counts its reads.
type countingLoader struct {
	mu       sync.Mutex
	docs     map[string][]byte
	versions map[string]int64
	reads    int
	err      error
	// during, when set, runs inside a read, after the store was consulted.
	during func()
}

func newCountingLoader() *countingLoader {
	return &countingLoader{docs: map[string][]byte{}, versions: map[string]int64{}}
}

func (loader *countingLoader) set(tenantID, doc string) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	loader.docs[tenantID] = []byte(doc)
	loader.versions[tenantID]++
}

func (loader *countingLoader) ReadTenantSettings(_ context.Context, tenantID string) ([]byte, int64, error) {
	loader.mu.Lock()
	loader.reads++
	doc, version, err, during := loader.docs[tenantID], loader.versions[tenantID], loader.err, loader.during
	loader.mu.Unlock()
	if during != nil {
		during()
	}
	if err != nil {
		return nil, 0, err
	}
	return doc, version, nil
}

func (loader *countingLoader) readCount() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.reads
}

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time          { return clock.now }
func (clock *testClock) advance(d time.Duration) { clock.now = clock.now.Add(d) }

func newTestCache(t *testing.T, loader Loader) (*Cache, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)}
	return NewCache(testRegistry(t), loader).WithClock(clock.Now), clock
}

func runPerMinute(t *testing.T, cache *Cache, tenantID string) int {
	t.Helper()
	limits, ok, err := Read[limitsSection](context.Background(), cache, tenantID, "limits")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok || limits.RunPerMinute == nil {
		return 0
	}
	return *limits.RunPerMinute
}

func TestGetServesFromMemoryWithinTheTTL(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	cache, clock := newTestCache(t, loader)
	for range 3 {
		if got := runPerMinute(t, cache, "acme"); got != 3 {
			t.Fatalf("runPerMinute = %d, want 3", got)
		}
		clock.advance(time.Second)
	}
	if reads := loader.readCount(); reads != 1 {
		t.Errorf("store reads = %d, want 1 within the TTL", reads)
	}
}

func TestInvalidateMakesTheNextGetReadTheStore(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	cache, _ := newTestCache(t, loader)
	runPerMinute(t, cache, "acme")
	loader.set("acme", `{"limits":{"runPerMinute":9}}`)
	cache.Invalidate("acme")
	if got := runPerMinute(t, cache, "acme"); got != 9 {
		t.Errorf("after Invalidate runPerMinute = %d, want 9", got)
	}
}

func TestAnotherProcessSeesTheChangeWhenItsTTLRunsOut(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	serving, _ := newTestCache(t, loader)
	other, otherClock := newTestCache(t, loader)
	runPerMinute(t, serving, "acme")
	runPerMinute(t, other, "acme")

	loader.set("acme", `{"limits":{"runPerMinute":9}}`)
	serving.Invalidate("acme")
	if got := runPerMinute(t, serving, "acme"); got != 9 {
		t.Errorf("serving process = %d, want 9 at once", got)
	}
	if got := runPerMinute(t, other, "acme"); got != 3 {
		t.Errorf("other process before its TTL = %d, want the cached 3", got)
	}
	otherClock.advance(DefaultTTL)
	if got := runPerMinute(t, other, "acme"); got != 9 {
		t.Errorf("other process after its TTL = %d, want 9", got)
	}
}

func TestAnUnchangedVersionReusesTheDecodedSettings(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	cache, clock := newTestCache(t, loader)
	runPerMinute(t, cache, "acme")
	// The stored bytes change without a version bump; a reload that compares
	// versions keeps what it decoded, which proves no second decode ran.
	loader.mu.Lock()
	loader.docs["acme"] = []byte(`{"limits":{"runPerMinute":7}}`)
	loader.mu.Unlock()
	clock.advance(DefaultTTL)
	if got := runPerMinute(t, cache, "acme"); got != 3 {
		t.Errorf("runPerMinute = %d, want the reused 3", got)
	}
	if reads := loader.readCount(); reads != 2 {
		t.Errorf("store reads = %d, want 2 (one per TTL)", reads)
	}
}

func TestAStoreFailureServesTheLastKnownSettings(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	cache, clock := newTestCache(t, loader)
	runPerMinute(t, cache, "acme")
	loader.mu.Lock()
	loader.err = errors.New("database unavailable")
	loader.mu.Unlock()
	clock.advance(DefaultTTL)
	if got := runPerMinute(t, cache, "acme"); got != 3 {
		t.Errorf("runPerMinute during an outage = %d, want the last known 3", got)
	}
	if _, err := cache.Get(context.Background(), "globex"); err == nil {
		t.Error("a tenant never cached read without error during an outage")
	}
}

func TestAnInvalidationDuringALoadIsNotOverwritten(t *testing.T) {
	loader := newCountingLoader()
	loader.set("acme", `{"limits":{"runPerMinute":3}}`)
	cache, _ := newTestCache(t, loader)
	// The write and its invalidation land while this read is in flight: the
	// read returns what it saw, but must not cache it over the invalidation.
	loader.during = func() {
		loader.during = nil
		loader.set("acme", `{"limits":{"runPerMinute":9}}`)
		cache.Invalidate("acme")
	}
	runPerMinute(t, cache, "acme")
	if got := runPerMinute(t, cache, "acme"); got != 9 {
		t.Errorf("after an invalidation during a load = %d, want 9", got)
	}
}

func TestANilCacheReadsAsNoSettings(t *testing.T) {
	var cache *Cache
	settings, err := cache.Get(context.Background(), "acme")
	if err != nil || settings.Version() != 0 {
		t.Errorf("nil cache Get() = %+v, %v", settings, err)
	}
	if _, ok, err := Read[limitsSection](context.Background(), cache, "acme", "limits"); ok || err != nil {
		t.Errorf("nil cache Read() = %v, %v", ok, err)
	}
	cache.Invalidate("acme")
	if cache.Registry() != nil {
		t.Error("nil cache has a registry")
	}
}
