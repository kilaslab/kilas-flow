package tenantsettings

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTTL is how long a cached tenant's settings are served from memory
// before the next read checks the store again. It bounds how stale another
// process can be after an update: the process that served the update drops
// its entry at once, every other process sees the change within this long.
//
// There is no cross-process invalidation channel to lean on instead: the
// PostgreSQL LISTEN/NOTIFY relay carries execution events only, and SQLite is
// a single-process store. A short TTL with a version check keeps the store
// cost to one primary-key read per tenant per TTL.
const DefaultTTL = 5 * time.Second

// maxCacheEntries caps the cache. When it is full the whole map is dropped
// rather than tracking recency: the next read of each tenant is one
// primary-key read, and a deployment with more live tenants than this per TTL
// pays that read anyway.
const maxCacheEntries = 10_000

// Loader reads one tenant's stored settings document and its version. The
// repository's AuthRepository satisfies it.
type Loader interface {
	ReadTenantSettings(ctx context.Context, tenantID string) ([]byte, int64, error)
}

type cacheEntry struct {
	settings Settings
	// stored is the document the settings were decoded from. A version is
	// unique only within one tenant row's lifetime: a purged tenant id can be
	// created again and reach the same version with a different document,
	// and a process that never saw the purge must not keep the old one.
	stored   []byte
	loadedAt time.Time
}

// Cache serves tenants' decoded settings to hot paths.
type Cache struct {
	registry *Registry
	loader   Loader
	ttl      time.Duration
	now      func() time.Time
	logger   *slog.Logger

	mu      sync.Mutex
	entries map[string]cacheEntry
	// generation moves on every Invalidate. A load that started before an
	// invalidation does not store what it read, so a stale read cannot
	// overwrite the invalidation that followed a write.
	generation atomic.Uint64
}

// NewCache returns a cache that decodes with registry and reads through loader.
func NewCache(registry *Registry, loader Loader) *Cache {
	return &Cache{
		registry: registry,
		loader:   loader,
		ttl:      DefaultTTL,
		now:      time.Now,
		logger:   slog.Default(),
		entries:  map[string]cacheEntry{},
	}
}

// WithTTL replaces DefaultTTL; a non-positive value is ignored.
func (c *Cache) WithTTL(ttl time.Duration) *Cache {
	if ttl > 0 {
		c.ttl = ttl
	}
	return c
}

// WithClock replaces the clock, for tests; nil keeps time.Now.
func (c *Cache) WithClock(now func() time.Time) *Cache {
	if now != nil {
		c.now = now
	}
	return c
}

// Registry is the registry the cache decodes with; nil for a nil cache.
func (c *Cache) Registry() *Registry {
	if c == nil {
		return nil
	}
	return c.registry
}

// Get returns tenantID's settings. A nil cache reads as no settings, so a
// consumer wired without one falls back to the deployment configuration.
//
// A fresh entry is served from memory. Otherwise the store is read once; an
// unchanged version and document reuse the decoded settings. When the store fails, the
// last settings known for the tenant are served with a warning, and the error
// is returned only when there are none.
func (c *Cache) Get(ctx context.Context, tenantID string) (Settings, error) {
	if c == nil || c.registry == nil || c.loader == nil {
		return Settings{}, nil
	}
	now := c.now()
	c.mu.Lock()
	entry, cached := c.entries[tenantID]
	c.mu.Unlock()
	if cached && now.Sub(entry.loadedAt) < c.ttl {
		return entry.settings, nil
	}
	generation := c.generation.Load()
	stored, version, err := c.loader.ReadTenantSettings(ctx, tenantID)
	if err != nil {
		if cached {
			c.logger.WarnContext(ctx, "tenant settings read failed; serving the last known settings",
				"tenant", tenantID, "settings_version", entry.settings.version, "error", err)
			// Back off half a TTL before trying the store again, so a failing
			// store is not read, and warned about, on every request.
			c.mu.Lock()
			if current, ok := c.entries[tenantID]; ok && c.generation.Load() == generation {
				current.loadedAt = now.Add(-c.ttl / 2)
				c.entries[tenantID] = current
			}
			c.mu.Unlock()
			return entry.settings, nil
		}
		return Settings{}, err
	}
	var settings Settings
	if cached && entry.settings.version == version && bytes.Equal(entry.stored, stored) {
		settings = entry.settings
	} else {
		decoded, decodeErr := c.registry.Decode(stored, version)
		if decodeErr != nil {
			c.logger.WarnContext(ctx, "tenant settings section skipped; the deployment default applies",
				"tenant", tenantID, "settings_version", version, "error", decodeErr)
		}
		settings = decoded
	}
	c.mu.Lock()
	if c.generation.Load() == generation {
		if _, present := c.entries[tenantID]; !present && len(c.entries) >= maxCacheEntries {
			c.entries = map[string]cacheEntry{}
		}
		c.entries[tenantID] = cacheEntry{settings: settings, stored: stored, loadedAt: now}
	}
	c.mu.Unlock()
	return settings, nil
}

// Invalidate drops tenantID's entry, so this process's next read of it goes
// to the store. The update endpoint calls it after every write.
func (c *Cache) Invalidate(tenantID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.generation.Add(1)
	delete(c.entries, tenantID)
	c.mu.Unlock()
}

// Read returns tenantID's section name as a T through c, and whether the
// tenant set it. It is the one call a hot path makes.
func Read[T any](ctx context.Context, c *Cache, tenantID, name string) (T, bool, error) {
	settings, err := c.Get(ctx, tenantID)
	if err != nil {
		var zero T
		return zero, false, err
	}
	value, ok := Lookup[T](settings, name)
	return value, ok, nil
}
