package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

func settingsRegistry(t *testing.T) *tenantsettings.Registry {
	t.Helper()
	registry := tenantsettings.NewRegistry()
	if err := RegisterSettings(registry); err != nil {
		t.Fatalf("RegisterSettings() error = %v", err)
	}
	return registry
}

func TestARateLimitSettingMustBeWithinTheDeploymentBounds(t *testing.T) {
	registry := settingsRegistry(t)
	for _, value := range []string{"0", "-1", "1000001"} {
		_, err := registry.Apply(nil, map[string]json.RawMessage{
			SettingsSection: json.RawMessage(`{"webhookPerMinute":` + value + `}`),
		})
		var patchErr *tenantsettings.PatchError
		if !errors.As(err, &patchErr) || patchErr.Issues[0].Code != tenantsettings.CodeInvalid {
			t.Errorf("webhookPerMinute %s: error = %v, want an invalid issue", value, err)
		}
	}
	_, err := registry.Apply(nil, map[string]json.RawMessage{
		SettingsSection: json.RawMessage(`{"runPerMinute":0,"eventsPerMinute":0}`),
	})
	var patchErr *tenantsettings.PatchError
	if !errors.As(err, &patchErr) || len(patchErr.Issues) != 2 ||
		patchErr.Issues[0].Path != "rateLimit.runPerMinute" || patchErr.Issues[1].Path != "rateLimit.eventsPerMinute" {
		t.Errorf("two refused fields = %v, want one issue at each field path", err)
	}
	for _, value := range []string{"1", "1000000"} {
		if _, err := registry.Apply(nil, map[string]json.RawMessage{
			SettingsSection: json.RawMessage(`{"runPerMinute":` + value + `,"eventsPerMinute":` + value + `}`),
		}); err != nil {
			t.Errorf("runPerMinute %s: error = %v, want accepted", value, err)
		}
	}
}

// A capacity lowered at runtime applies at once: a bucket filled under the old
// capacity is capped to the new one even when no time has passed.
func TestALoweredCapacityAppliesWithoutWaitingForARefill(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	memory := NewMemoryStore()
	if ok, _, _ := memory.Spend(ctx, "acme", BucketRun, 100, 100.0/60, now); !ok {
		t.Fatal("first spend at capacity 100 refused")
	}
	if ok, _, _ := memory.Spend(ctx, "acme", BucketRun, 1, 1.0/60, now); !ok {
		t.Fatal("first spend at capacity 1 refused")
	}
	if ok, _, _ := memory.Spend(ctx, "acme", BucketRun, 1, 1.0/60, now); ok {
		t.Error("memory store: a second spend at the lowered capacity 1 was allowed")
	}

	held := &BucketModel{Tokens: 99, UpdatedAt: now}
	if ok, _ := settle(held, 1, 1.0/60, now); !ok {
		t.Fatal("settle: first spend at capacity 1 refused")
	}
	if ok, _ := settle(held, 1, 1.0/60, now); ok {
		t.Error("database store: a second spend at the lowered capacity 1 was allowed")
	}
}

func TestEachRateLimitFieldOverridesItsOwnBucket(t *testing.T) {
	cache := tenantsettings.NewCache(settingsRegistry(t), staticLoader{
		"acme": `{"rateLimit":{"runPerMinute":11,"webhookPerMinute":22,"eventsPerMinute":33}}`,
	})
	overrides := SettingsOverrides{Cache: cache}
	ctx := context.Background()
	for bucket, want := range map[string]int{BucketRun: 11, BucketWebhook: 22, BucketEvents: 33, "unknown": 0} {
		if got := overrides.Allowance(ctx, "acme", bucket); got != want {
			t.Errorf("Allowance(%s) = %d, want %d", bucket, got, want)
		}
	}
}

// staticLoader serves fixed documents, one per tenant.
type staticLoader map[string]string

func (loader staticLoader) ReadTenantSettings(_ context.Context, tenantID string) ([]byte, int64, error) {
	doc, ok := loader[tenantID]
	if !ok {
		return nil, 0, nil
	}
	return []byte(doc), 1, nil
}

func TestATenantRateLimitSettingReplacesTheDefaultForThatTenantOnly(t *testing.T) {
	cache := tenantsettings.NewCache(settingsRegistry(t), staticLoader{
		"acme": `{"rateLimit":{"webhookPerMinute":2}}`,
	})
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryStore(), map[string]int{BucketWebhook: 100}).
		WithOverrides(SettingsOverrides{Cache: cache}).
		WithClock(func() time.Time { return now })
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if ok, _, err := service.Allow(ctx, "acme", BucketWebhook); !ok || err != nil {
			t.Fatalf("acme spend %d = %v, %v; want allowed", i+1, ok, err)
		}
	}
	if ok, _, _ := service.Allow(ctx, "acme", BucketWebhook); ok {
		t.Error("acme's third webhook spend was allowed past its setting of 2")
	}
	for i := 0; i < 3; i++ {
		if ok, _, err := service.Allow(ctx, "globex", BucketWebhook); !ok || err != nil {
			t.Fatalf("globex spend %d = %v, %v; want the default allowance", i+1, ok, err)
		}
	}
	// acme set no run override, so the run bucket keeps the deployment default.
	if got := (SettingsOverrides{Cache: cache}).Allowance(ctx, "acme", BucketRun); got != 0 {
		t.Errorf("run allowance = %d, want 0 (the default)", got)
	}
}
