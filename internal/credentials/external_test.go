package credentials_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// fakeStore is a BindingStore keyed by tenant and name.
type fakeStore struct {
	bindings map[string]credentials.Binding
	err      error
}

func (store *fakeStore) LookupBinding(_ context.Context, tenantID, name string) (credentials.Binding, error) {
	if store.err != nil {
		return credentials.Binding{}, store.err
	}
	binding, found := store.bindings[tenantID+"\x00"+name]
	if !found {
		return credentials.Binding{}, fmt.Errorf("%w: %q", credentials.ErrUnknownBinding, name)
	}
	return binding, nil
}

// fakeProvider counts fetches and answers from a map.
type fakeProvider struct {
	values  map[string]string
	fetches *atomic.Int64
	err     error
}

func (provider *fakeProvider) Name() string { return "fake" }

func (provider *fakeProvider) Health(context.Context) error { return provider.err }

func (provider *fakeProvider) Fetch(_ context.Context, key string) (string, error) {
	if provider.fetches != nil {
		provider.fetches.Add(1)
	}
	if provider.err != nil {
		return "", provider.err
	}
	value, found := provider.values[key]
	if !found {
		return "", fmt.Errorf("%w: %q", credentials.ErrSecretNotFound, key)
	}
	return value, nil
}

func fakeResolver(bindings map[string]credentials.Binding, provider *fakeProvider) *credentials.Resolver {
	resolver := credentials.NewResolver(&fakeStore{bindings: bindings}, credentials.ResolverConfig{})
	resolver.RegisterProvider("fake", func(credentials.Binding) (credentials.Provider, error) {
		return provider, nil
	})
	return resolver
}

func TestReferenceStringRoundTripsThroughParse(t *testing.T) {
	t.Parallel()

	stored := credentials.ReferenceString("prod-vault", "prod/api-token")
	if !credentials.IsReference(stored) {
		t.Fatalf("IsReference(%q) = false", stored)
	}
	binding, key, err := credentials.ParseReference(stored)
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if binding != "prod-vault" || key != "prod/api-token" {
		t.Fatalf("ParseReference() = %q, %q", binding, key)
	}
	if credentials.IsReference("sk-plain-secret") {
		t.Error("a plain secret reads as a reference")
	}
	for _, malformed := range []string{"ext://", "ext:///key", "ext://binding/", "ext://binding"} {
		if _, _, err := credentials.ParseReference(malformed); !errors.Is(err, credentials.ErrBadReference) {
			t.Errorf("ParseReference(%q) = %v, want ErrBadReference", malformed, err)
		}
	}
}

func TestResolverPassesPlainValuesThrough(t *testing.T) {
	t.Parallel()

	resolver := fakeResolver(nil, &fakeProvider{values: map[string]string{}, fetches: &atomic.Int64{}})
	got, err := resolver.ResolveField(context.Background(), "tenant-a", "sk-plain")
	if err != nil || got != "sk-plain" {
		t.Fatalf("ResolveField(plain) = %q, %v", got, err)
	}
}

func TestReferenceUnderOneTenantCannotReadAnotherTenantsBinding(t *testing.T) {
	t.Parallel()

	store := &fakeStore{bindings: map[string]credentials.Binding{
		"tenant-a\x00shared-name": {TenantID: "tenant-a", Name: "shared-name", Provider: "fake", TokenEnv: "X"},
		"tenant-b\x00shared-name": {TenantID: "tenant-b", Name: "shared-name", Provider: "fake", TokenEnv: "X"},
	}}
	resolver := credentials.NewResolver(store, credentials.ResolverConfig{})
	// One provider per binding, each serving its own tenant's value: which
	// value comes back proves which binding the lookup used.
	resolver.RegisterProvider("fake", func(binding credentials.Binding) (credentials.Provider, error) {
		return &fakeProvider{values: map[string]string{"prod/api-token": "token-for-" + binding.TenantID}}, nil
	})

	got, err := resolver.ResolveField(context.Background(), "tenant-a", "ext://shared-name/prod/api-token")
	if err != nil || got != "token-for-tenant-a" {
		t.Fatalf("tenant-a ResolveField() = %q, %v", got, err)
	}
	got, err = resolver.ResolveField(context.Background(), "tenant-b", "ext://shared-name/prod/api-token")
	if err != nil || got != "token-for-tenant-b" {
		t.Fatalf("tenant-b ResolveField() = %q, %v", got, err)
	}
	if _, err := resolver.ResolveField(context.Background(), "tenant-c", "ext://shared-name/prod/api-token"); !errors.Is(err, credentials.ErrUnknownBinding) {
		t.Fatalf("tenant-c ResolveField() = %v, want ErrUnknownBinding", err)
	}
}

func TestResolvedValuesAreCachedAndDroppedOnBindingChange(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{values: map[string]string{"k": "v1"}, fetches: &atomic.Int64{}}
	resolver := credentials.NewResolver(
		&fakeStore{bindings: map[string]credentials.Binding{
			"tenant-a\x00b": {TenantID: "tenant-a", Name: "b", Provider: "fake", TokenEnv: "X"},
		}},
		credentials.ResolverConfig{CacheTTL: time.Minute},
	)
	resolver.RegisterProvider("fake", func(credentials.Binding) (credentials.Provider, error) {
		return provider, nil
	})
	ctx := context.Background()
	for range 3 {
		if got, err := resolver.ResolveField(ctx, "tenant-a", "ext://b/k"); err != nil || got != "v1" {
			t.Fatalf("ResolveField() = %q, %v", got, err)
		}
	}
	if fetches := provider.fetches.Load(); fetches != 1 {
		t.Fatalf("three resolves caused %d fetches, want 1 cached", fetches)
	}
	// A binding change drops what it supplied: the next read fetches again and
	// sees the rotation.
	resolver.InvalidateBinding("tenant-a", "b")
	provider.values["k"] = "v2"
	if got, err := resolver.ResolveField(ctx, "tenant-a", "ext://b/k"); err != nil || got != "v2" {
		t.Fatalf("post-invalidation ResolveField() = %q, %v", got, err)
	}
	if fetches := provider.fetches.Load(); fetches != 2 {
		t.Fatalf("fetches = %d, want 2", fetches)
	}
}

func TestCacheEntriesExpireAfterTheirTTL(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{values: map[string]string{"k": "v"}, fetches: &atomic.Int64{}}
	resolver := credentials.NewResolver(
		&fakeStore{bindings: map[string]credentials.Binding{
			"tenant-a\x00b": {TenantID: "tenant-a", Name: "b", Provider: "fake", TokenEnv: "X"},
		}},
		credentials.ResolverConfig{CacheTTL: 20 * time.Millisecond},
	)
	resolver.RegisterProvider("fake", func(credentials.Binding) (credentials.Provider, error) {
		return provider, nil
	})
	ctx := context.Background()
	if _, err := resolver.ResolveField(ctx, "tenant-a", "ext://b/k"); err != nil {
		t.Fatalf("ResolveField() error = %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := resolver.ResolveField(ctx, "tenant-a", "ext://b/k"); err != nil {
		t.Fatalf("ResolveField() error = %v", err)
	}
	if fetches := provider.fetches.Load(); fetches != 2 {
		t.Fatalf("fetches = %d across a TTL boundary, want 2", fetches)
	}
}

func TestKeyFromManagerDistinguishesDownFromUnconfigured(t *testing.T) {
	t.Parallel()

	key := make([]byte, credentials.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	encoded := base64.StdEncoding.EncodeToString(key)

	ok := &fakeProvider{values: map[string]string{"prod/master": encoded}}
	got, err := credentials.KeyFromManager(context.Background(), ok, "prod/master")
	if err != nil {
		t.Fatalf("KeyFromManager() error = %v", err)
	}
	if string(got) != string(key) {
		t.Fatal("KeyFromManager() returned the wrong key")
	}

	down := &fakeProvider{err: errors.New("connection refused")}
	if _, err := credentials.KeyFromManager(context.Background(), down, "prod/master"); !errors.Is(err, credentials.ErrManagerUnreachable) {
		t.Fatalf("down manager = %v, want ErrManagerUnreachable", err)
	} else if errors.Is(err, credentials.ErrNoKey) {
		t.Fatal("a down manager degraded into the no-key path, which would silently disable credential storage")
	}

	if _, err := credentials.KeyFromManager(context.Background(), nil, "prod/master"); !errors.Is(err, credentials.ErrNoKey) {
		t.Fatalf("nil provider = %v, want ErrNoKey", err)
	}

	garbage := &fakeProvider{values: map[string]string{"prod/master": "not-a-key"}}
	if _, err := credentials.KeyFromManager(context.Background(), garbage, "prod/master"); errors.Is(err, credentials.ErrManagerUnreachable) || err == nil {
		t.Fatalf("malformed key = %v, want a decode error rather than unreachable or nil", err)
	}
}

// vaultStub answers the two calls the provider makes: sys/health and one KV
// v2 read. It checks the token so the test proves the binding's credential is
// actually sent.
func vaultStub(t *testing.T, token, value string, reached *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sys/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/secret/data/prod/api-token" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Vault-Token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		reached.Store(true)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": map[string]any{"value": value}},
		})
	}))
}

func endpointOf(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return parsed.Host
}

// TestVaultProviderEndToEnd runs the provider against a live HTTP stub through
// the egress policy: a real client, a real server, and the same dial-time
// guard production gets.
func TestVaultProviderEndToEnd(t *testing.T) {
	t.Parallel()

	var reached atomic.Bool
	server := vaultStub(t, "test-token", "live-secret", &reached)
	defer server.Close()

	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks, which would make this test prove nothing")
	}
	policy.AllowedPrivateEndpoints = []string{endpointOf(t, server.URL)}

	provider, err := credentials.NewVaultProvider(server.URL, "test-token", policy)
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	if err := provider.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	got, err := provider.Fetch(context.Background(), "prod/api-token")
	if err != nil || got != "live-secret" {
		t.Fatalf("Fetch() = %q, %v", got, err)
	}
	if !reached.Load() {
		t.Fatal("the stub was never reached")
	}
	if _, err := provider.Fetch(context.Background(), "prod/missing"); !errors.Is(err, credentials.ErrSecretNotFound) {
		t.Fatalf("missing key = %v, want ErrSecretNotFound", err)
	}

	// Without the endpoint grant the same server is unreachable: Vault inherits
	// the egress policy rather than bypassing it. Never allow_private_networks
	// to make this pass; name the endpoint, as above.
	denied, err := credentials.NewVaultProvider(server.URL, "test-token", safehttp.DefaultPolicy())
	if err != nil {
		t.Fatalf("NewVaultProvider() error = %v", err)
	}
	if _, err := denied.Fetch(context.Background(), "prod/api-token"); err == nil {
		t.Fatal("an ungranted loopback Vault was reachable")
	}
}

func TestRedactionStillMasksAReferenceValue(t *testing.T) {
	t.Parallel()

	fields := map[string]string{"apiKey": credentials.ReferenceString("prod-vault", "prod/openai")}
	safe := credentials.Redacted("openAiApi", fields)
	if safe["apiKey"] != credentials.RedactedValue {
		t.Fatalf("Redacted() = %q, want the set/unset marker rather than the reference", safe["apiKey"])
	}
	if strings.Contains(safe["apiKey"], "prod/openai") {
		t.Fatal("the binding path leaked into the API response")
	}
	// A reference is non-empty, so it satisfies a required secret field: the
	// stored row can hold a pointer where a secret used to be.
	if err := credentials.Validate("openAiApi", fields); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestScrubResolvedRemovesKnownValuesBodiesCannotHide(t *testing.T) {
	t.Parallel()

	payload := json.RawMessage(`{"headers":{"authorization":"Bearer abc"},"body":{"token":"tok-live-123","nested":[{"key":"tok-live-123"}],"note":"use tok-live-123 here"},"count":3}`)
	scrubbed := credentials.ScrubResolved(payload, []string{"tok-live-123"})
	var decoded map[string]any
	if err := json.Unmarshal(scrubbed, &decoded); err != nil {
		t.Fatalf("scrubbed payload is not JSON: %v", err)
	}
	encoded, _ := json.Marshal(decoded)
	if strings.Contains(string(encoded), "tok-live-123") {
		t.Fatalf("scrubbed payload still carries the secret: %s", encoded)
	}
	body := decoded["body"].(map[string]any)
	if body["token"] != execution.RedactedValue {
		t.Fatalf("body token = %v, want the redaction marker", body["token"])
	}
	if body["count"] != nil {
		t.Fatalf("unexpected key leaked in: %v", body)
	}
	// Shape survives: keys, arrays and safe scalars are intact.
	if decoded["count"] != float64(3) {
		t.Fatalf("safe scalar was rewritten: %s", encoded)
	}
	// Degenerate inputs pass through rather than failing the write.
	for _, input := range []json.RawMessage{nil, json.RawMessage("not json{"), json.RawMessage(`{"a":1}`)} {
		if got := credentials.ScrubResolved(input, []string{"tok-live-123"}); string(got) != string(input) && input != nil {
			t.Fatalf("ScrubResolved(%q) rewrote a payload it should not touch", input)
		}
	}
	if got := credentials.ScrubResolved(payload, nil); string(got) != string(payload) {
		t.Fatal("no secrets must mean byte-identical output")
	}
}
