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
