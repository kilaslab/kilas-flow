package middleware

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/auth"
)

// sessionCache answers "is this session's account still the account that signed
// in?" without asking storage on every request.
//
// A signed session is a stateless grant, so revalidating it means reading the
// account row on a path that otherwise touches nothing. Caching the answer for
// a few seconds is what makes that affordable, and it is also the revocation
// window: a disabled account or a changed password is noticed at the first
// lookup after the cached answer expires, and the TTL is how long that can
// take. A hit is never trusted blindly — the caller compares the session's own
// user version against the cached one, so a stale entry cannot admit a token
// minted against a different credential state.
type sessionCache struct {
	lookup UserLookup
	ttl    time.Duration
	now    func() time.Time

	// warnOnce reports the unwired-lookup misconfiguration once per process
	// rather than once per dashboard request.
	warnOnce sync.Once

	mu      sync.Mutex
	entries map[string]sessionCacheEntry
}

// sessionCacheEntry is what revalidation compares against, and nothing else.
//
// The password hash is deliberately absent: the fingerprint built from it is
// enough to notice a change, and holding the hash in a request-path cache would
// put the whole credential state of every signed-in account in the memory of
// the busiest component in the process.
type sessionCacheEntry struct {
	userID    string
	tenantID  string
	version   string
	disabled  bool
	expiresAt time.Time
}

// newSessionCache builds the cache the gate reads through.
//
// A non-positive TTL falls back to DefaultSessionRevalidationTTL, so a
// deployment cannot accidentally choose "trust the last answer forever".
func newSessionCache(lookup UserLookup, ttl time.Duration) *sessionCache {
	if ttl <= 0 {
		ttl = DefaultSessionRevalidationTTL
	}
	return &sessionCache{
		lookup:  lookup,
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]sessionCacheEntry),
	}
}

// lookupMissing reports whether the account lookup is absent.
//
// The nil comparison alone is not enough: an installation that builds no store
// but passes its zero value on anyway hands over a *typed* nil, which satisfies
// the interface and then panics on the first call. Both shapes mean the same
// thing — nothing can be revalidated — so both are treated as missing, which
// refuses sessions rather than crashing the process that was asked for one.
func lookupMissing(lookup UserLookup) bool {
	if lookup == nil {
		return true
	}
	value := reflect.ValueOf(lookup)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// current returns the account state behind a session.
//
// The second result is false when the state could not be established, which
// every caller treats as a refusal. A failure is never cached: a lookup that
// failed because the database was briefly unreachable would otherwise keep
// refusing every session for the whole TTL after the database came back, and a
// refusal is already the safe side of a guess.
func (cache *sessionCache) current(ctx context.Context, session auth.Session) (sessionCacheEntry, bool) {
	if cache == nil {
		return sessionCacheEntry{}, false
	}
	if lookupMissing(cache.lookup) {
		// A deployment that has not wired the lookup refuses sessions rather
		// than admitting them unrevalidated, which is invisible from outside
		// (the caller just sees 401). Say so once, because the alternative is
		// an operator debugging a login that appears to succeed and then
		// authenticates nothing.
		cache.warnOnce.Do(func() {
			slog.WarnContext(ctx, "browser sessions are refused: no user lookup is wired into the authentication gate",
				slog.String("field", "middleware.AuthOptions.Users"))
		})
		return sessionCacheEntry{}, false
	}

	now := cache.now()
	cache.mu.Lock()
	entry, found := cache.entries[session.Email]
	cache.mu.Unlock()
	if found && now.Before(entry.expiresAt) {
		return entry, true
	}

	// The read runs outside the lock: it is a database round trip, and holding
	// the mutex across it would serialise every authenticated request behind
	// the slowest lookup. Two concurrent misses for one address do duplicate
	// work, which is cheaper than that serialisation and settles to one entry.
	user, err := cache.lookup.FindUserForLogin(ctx, session.Email)
	if err != nil {
		return sessionCacheEntry{}, false
	}
	entry = sessionCacheEntry{
		userID:   user.ID,
		tenantID: user.TenantID,
		version:  auth.UserVersion(user.PasswordHash, user.DisabledAt != nil),
		disabled: user.DisabledAt != nil,
		// Expiry is measured from when the read started, so a slow lookup
		// shortens the window rather than extending it.
		expiresAt: now.Add(cache.ttl),
	}

	cache.mu.Lock()
	cache.store(session.Email, entry, now)
	cache.mu.Unlock()
	return entry, true
}

// store writes one entry and drops the expired ones on the way past.
//
// Pruning happens here rather than on a timer so a process with no traffic
// holds no goroutine, and so the map cannot grow beyond the addresses that have
// resolved to a live session within one TTL. Entries only ever appear for
// sessions whose signature verified, which is why this needs no separate ceiling.
func (cache *sessionCache) store(email string, entry sessionCacheEntry, now time.Time) {
	for key, cached := range cache.entries {
		if now.After(cached.expiresAt) {
			delete(cache.entries, key)
		}
	}
	cache.entries[email] = entry
}
