// External secret references resolve a stored credential field through a
// tenant-scoped manager binding at the moment the runtime needs it.
//
// A reference lives in the credential, where it is resolved on the Resolve
// path that already governs who may read what — not in an expression root,
// where any workflow author could interpolate an arbitrary secret into an HTTP
// body with only the redaction boundary standing between it and an execution
// record. The stored row keeps the sealed reference; plaintext exists only in
// the map Resolve hands back and in the bounded in-memory cache below, never
// on disk.
package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// ReferenceScheme prefixes every external secret reference stored in a
// credential field. The shape is ext://<binding>/<key>: the binding names the
// tenant's manager binding, the key is opaque to this package and interpreted
// by the provider (a Vault KV path, a cloud secret name, …).
const ReferenceScheme = "ext://"

// ErrBadReference reports a stored value that starts like a reference but
// parses as none. It is a stored-data error, not an operator error: writes go
// through Validate, so one of these means the row predates validation or was
// written around it.
var ErrBadReference = errors.New("credential holds a malformed external secret reference")

// ErrUnknownBinding reports a reference naming no binding of the calling
// tenant. The binding is looked up under the caller's tenant scope, so a
// reference authored under one tenant can never read a secret bound to
// another: the row simply is not there.
var ErrUnknownBinding = errors.New("external secret binding is not configured for this tenant")

// ErrUnknownProvider reports a binding naming no registered provider.
var ErrUnknownProvider = errors.New("external secret provider is not registered")

// Binding names one tenant's external secret source. The provider credential
// itself (a Vault token, a cloud access key) is named by environment variable,
// never stored: a binding row that held a token would make the manager a copy
// of the secret it was meant to replace.
type Binding struct {
	TenantID string
	Name     string
	Provider string
	// Address is the manager's base URL (a Vault address, …). Meaning depends
	// on the provider; "vault" requires http(s).
	Address string
	// TokenEnv names the environment variable holding the manager credential.
	TokenEnv string
}

// Validate rejects a binding that could never resolve.
func (binding Binding) Validate() error {
	if strings.TrimSpace(binding.TenantID) == "" {
		return fmt.Errorf("external secret binding needs a tenant")
	}
	name := strings.TrimSpace(binding.Name)
	if name == "" {
		return fmt.Errorf("external secret binding needs a name")
	}
	if strings.Contains(name, "/") || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("external secret binding name %q must not contain slashes or whitespace", binding.Name)
	}
	if strings.TrimSpace(binding.Provider) == "" {
		return fmt.Errorf("external secret binding %q needs a provider", name)
	}
	if strings.TrimSpace(binding.TokenEnv) == "" {
		return fmt.Errorf("external secret binding %q needs the environment variable holding its manager credential", name)
	}
	return nil
}

// IsReference reports whether a stored field value is an external reference
// rather than a sealed secret.
func IsReference(value string) bool {
	return strings.HasPrefix(value, ReferenceScheme)
}

// ParseReference splits ext://<binding>/<key>. The key keeps its slashes: a
// Vault path is several segments deep and the binding name may not contain
// one, so the first slash is the only boundary.
func ParseReference(value string) (binding, key string, err error) {
	rest, found := strings.CutPrefix(value, ReferenceScheme)
	if !found {
		return "", "", fmt.Errorf("%w: %q does not start with %q", ErrBadReference, value, ReferenceScheme)
	}
	name, key, found := strings.Cut(rest, "/")
	if !found || strings.TrimSpace(name) == "" || strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("%w: %q needs ext://<binding>/<key>", ErrBadReference, value)
	}
	return name, key, nil
}

// ReferenceString builds the stored value for one binding and key.
func ReferenceString(binding, key string) string {
	return ReferenceScheme + binding + "/" + key
}

// Provider fetches one secret by key. Deliberately small — fetch, health, and
// nothing else. A provider that also lists, writes or rotates invites
// KilasFlow to become a secrets manager, which it must not. Adding a second
// provider is a new file implementing this interface, not a change to the
// credential store.
type Provider interface {
	Name() string
	Health(ctx context.Context) error
	Fetch(ctx context.Context, key string) (string, error)
}

// ErrSecretNotFound reports a key the manager answers for with no secret.
// Distinct from a transport failure so a missing rotation is not retried as
// though the network had blipped.
var ErrSecretNotFound = errors.New("external secret was not found")

// ProviderFactory builds a Provider for one tenant binding. The factory reads
// the manager credential out of the process environment at call time, so a
// rotated token is picked up without restarting and never lands in a row.
type ProviderFactory func(binding Binding) (Provider, error)

// BindingStore is the persistence seam behind resolution. Implemented by the
// credential store, which scopes every lookup to the calling tenant; this
// package never touches the database directly.
type BindingStore interface {
	LookupBinding(ctx context.Context, tenantID, name string) (Binding, error)
}

// DefaultRefCacheTTL bounds how long a resolved value is reused. Short enough
// that a rotation propagates without an operator hunting a cache, long enough
// that a burst of runs does not fan out into a burst of manager reads.
const DefaultRefCacheTTL = 5 * time.Minute

// DefaultRefCacheMaxEntries bounds how many resolved values one process
// holds. The cache is per-process memory: when it is full the oldest entries
// are dropped and later reads simply fetch again, so the bound costs a fetch,
// never correctness.
const DefaultRefCacheMaxEntries = 1024

// ResolverConfig tunes external resolution. Zero values select the defaults
// above and the instance egress policy.
type ResolverConfig struct {
	CacheTTL        time.Duration
	CacheMaxEntries int
	VaultPolicy     safehttp.Policy
}

// Resolver resolves external references inside a tenant scope, against that
// tenant's bindings, with a bounded in-memory cache in between.
type Resolver struct {
	store     BindingStore
	factories map[string]ProviderFactory
	cache     *RefCache
}

// NewResolver builds a Resolver over a tenant-scoped binding store. The Vault
// provider is registered from VaultPolicy; further providers arrive through
// RegisterProvider.
func NewResolver(store BindingStore, config ResolverConfig) *Resolver {
	ttl := config.CacheTTL
	if ttl <= 0 {
		ttl = DefaultRefCacheTTL
	}
	max := config.CacheMaxEntries
	if max <= 0 {
		max = DefaultRefCacheMaxEntries
	}
	policy := config.VaultPolicy
	if policy.Timeout <= 0 {
		policy.Timeout = safehttp.DefaultPolicy().Timeout
	}
	resolver := &Resolver{store: store, factories: map[string]ProviderFactory{}, cache: NewRefCache(ttl, max)}
	resolver.factories[VaultProviderName] = func(binding Binding) (Provider, error) {
		return vaultFactory(binding, policy)
	}
	return resolver
}

// RegisterProvider adds or replaces one provider factory. Exported so a second
// provider — and a fake in tests — is a call, not a change to this file.
func (resolver *Resolver) RegisterProvider(name string, factory ProviderFactory) {
	if resolver == nil {
		return
	}
	resolver.factories[name] = factory
}

// ResolveField resolves one stored field value. A plain value passes through
// untouched, so callers need no branch: the scan for references and the fetch
// live here, in the one place that knows the tenant.
func (resolver *Resolver) ResolveField(ctx context.Context, tenantID, value string) (string, error) {
	if !IsReference(value) {
		return value, nil
	}
	if resolver == nil || resolver.store == nil {
		return "", fmt.Errorf("%w: credential holds an external reference but no secret manager is configured", ErrUnknownBinding)
	}
	bindingName, key, err := ParseReference(value)
	if err != nil {
		return "", err
	}
	if resolved, found := resolver.cache.Get(tenantID, bindingName, key); found {
		return resolved, nil
	}
	binding, err := resolver.store.LookupBinding(ctx, tenantID, bindingName)
	if err != nil {
		return "", err
	}
	// Defense in depth: the store scopes the lookup, and the check here means
	// a store that forgot still cannot hand one tenant's binding to another.
	if binding.TenantID != tenantID {
		return "", fmt.Errorf("%w: %q", ErrUnknownBinding, bindingName)
	}
	factory, found := resolver.factories[binding.Provider]
	if !found {
		return "", fmt.Errorf("%w: %q", ErrUnknownProvider, binding.Provider)
	}
	provider, err := factory(binding)
	if err != nil {
		return "", err
	}
	resolved, err := provider.Fetch(ctx, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resolved) == "" {
		return "", fmt.Errorf("external secret %q holds an empty value", value)
	}
	resolver.cache.Set(tenantID, bindingName, key, resolved)
	return resolved, nil
}

// InvalidateTenant drops every cached value resolved under one tenant. Called
// when that tenant's credentials or bindings change.
func (resolver *Resolver) InvalidateTenant(tenantID string) {
	if resolver == nil || resolver.cache == nil {
		return
	}
	resolver.cache.InvalidateTenant(tenantID)
}

// InvalidateBinding drops the cached values one binding supplied. Called when
// that binding is updated or deleted, and when its manager credential rotates
// out of band.
func (resolver *Resolver) InvalidateBinding(tenantID, binding string) {
	if resolver == nil || resolver.cache == nil {
		return
	}
	resolver.cache.InvalidateBinding(tenantID, binding)
}

// refCacheKey joins the cache coordinates with a separator that cannot appear
// in a tenant ID, a binding name, or — after ParseReference — ambiguity about
// which slash was the boundary. The tuple is the key, never the value.
func refCacheKey(tenantID, binding, key string) string {
	return tenantID + "\x00" + binding + "\x00" + key
}

type refCacheEntry struct {
	value     string
	expiresAt time.Time
}

// RefCache is a bounded TTL cache for resolved external values. Memory only:
// entries are never written to disk, and a restart starts empty, which is what
// makes rotation a matter of waiting out the TTL rather than purging storage.
type RefCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	items   map[string]refCacheEntry
	ordered []string
}

// NewRefCache builds a cache holding up to max entries for ttl each.
func NewRefCache(ttl time.Duration, max int) *RefCache {
	if ttl <= 0 {
		ttl = DefaultRefCacheTTL
	}
	if max <= 0 {
		max = DefaultRefCacheMaxEntries
	}
	return &RefCache{ttl: ttl, max: max, items: map[string]refCacheEntry{}}
}

// Get returns a live cached value. Expired entries are dropped on read.
func (cache *RefCache) Get(tenantID, binding, key string) (string, bool) {
	if cache == nil {
		return "", false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, found := cache.items[refCacheKey(tenantID, binding, key)]
	if !found {
		return "", false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(cache.items, refCacheKey(tenantID, binding, key))
		return "", false
	}
	return entry.value, true
}

// Set stores one resolved value. When full the oldest entries go first; a
// value that does not fit costs its next read a fetch, never an error.
func (cache *RefCache) Set(tenantID, binding, key, value string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cacheKey := refCacheKey(tenantID, binding, key)
	if _, found := cache.items[cacheKey]; !found {
		cache.ordered = append(cache.ordered, cacheKey)
	}
	cache.items[cacheKey] = refCacheEntry{value: value, expiresAt: time.Now().Add(cache.ttl)}
	for len(cache.items) > cache.max {
		oldest := cache.ordered[0]
		cache.ordered = cache.ordered[1:]
		delete(cache.items, oldest)
	}
}

// InvalidateTenant drops every entry resolved under one tenant.
func (cache *RefCache) InvalidateTenant(tenantID string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prefix := tenantID + "\x00"
	kept := cache.ordered[:0]
	for _, cacheKey := range cache.ordered {
		if strings.HasPrefix(cacheKey, prefix) {
			delete(cache.items, cacheKey)
			continue
		}
		kept = append(kept, cacheKey)
	}
	cache.ordered = kept
}

// InvalidateBinding drops every entry one binding supplied.
func (cache *RefCache) InvalidateBinding(tenantID, binding string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	prefix := refCacheKey(tenantID, binding, "")
	kept := cache.ordered[:0]
	for _, cacheKey := range cache.ordered {
		if strings.HasPrefix(cacheKey, prefix) {
			delete(cache.items, cacheKey)
			continue
		}
		kept = append(kept, cacheKey)
	}
	cache.ordered = kept
}

// ScrubResolved replaces known resolved-secret substrings in a JSON payload
// with the execution redaction marker.
//
// This is not the heuristic the execution boundary removed: that one guessed
// at secrets from shape and could not tell a header value from prose. Here the
// values are known — they were just fetched from a manager — so matching them
// exactly cannot misfire on ordinary text. It exists for the case key-based
// redaction cannot see: a token interpolated into a request body under an
// innocuous field name. Only string scalars are touched; keys, numbers and
// structure survive, so an inspector still shows the shape of what ran.
func ScrubResolved(payload json.RawMessage, secrets []string) json.RawMessage {
	needles := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			needles = append(needles, secret)
		}
	}
	if len(payload) == 0 || !json.Valid(payload) || len(needles) == 0 {
		return payload
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return payload
	}
	scrubbed := scrubValue(decoded, needles)
	encoded, err := json.Marshal(scrubbed)
	if err != nil {
		return payload
	}
	return encoded
}

func scrubValue(value any, needles []string) any {
	switch typed := value.(type) {
	case string:
		scrubbed := typed
		for _, needle := range needles {
			if strings.Contains(scrubbed, needle) {
				scrubbed = strings.ReplaceAll(scrubbed, needle, execution.RedactedValue)
			}
		}
		return scrubbed
	case map[string]any:
		for key, nested := range typed {
			typed[key] = scrubValue(nested, needles)
		}
		return typed
	case []any:
		for index, nested := range typed {
			typed[index] = scrubValue(nested, needles)
		}
		return typed
	default:
		return value
	}
}

// managerToken reads the manager credential for one binding out of the process
// environment. The variable — not the token — is what the binding row names,
// so the row stays committable and rotation never touches the database.
func managerToken(binding Binding) (string, error) {
	token := strings.TrimSpace(os.Getenv(binding.TokenEnv))
	if token == "" {
		return "", fmt.Errorf("external secret binding %q: %s holds no manager credential", binding.Name, binding.TokenEnv)
	}
	return token, nil
}
