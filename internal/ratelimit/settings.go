package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

// SettingsSection is the tenant-settings section this package owns: a
// tenant's own per-minute allowances, replacing the deployment default for
// that tenant on each bucket it sets.
const SettingsSection = "rateLimit"

// MaxPerMinute is the largest per-minute allowance a tenant setting may hold.
// It equals config.rateLimitPerMinuteCeiling (internal/config pins the two
// together in a test), so a tenant override can never
// exceed what the deployment configuration itself would accept.
const MaxPerMinute = 1_000_000

// TenantSettings is the rateLimit section. A nil field means the tenant has
// no override for that bucket and the deployment default applies.
type TenantSettings struct {
	RunPerMinute     *int `json:"runPerMinute,omitempty"`
	WebhookPerMinute *int `json:"webhookPerMinute,omitempty"`
	EventsPerMinute  *int `json:"eventsPerMinute,omitempty"`
}

// Validate refuses an allowance outside 1..MaxPerMinute, the same bounds the
// deployment's rate_limit configuration enforces. Zero is refused rather than
// read as "unlimited": a tenant that should fall back to the default sets the
// field to null.
func (settings TenantSettings) Validate() error {
	var problems []error
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"runPerMinute", settings.RunPerMinute},
		{"webhookPerMinute", settings.WebhookPerMinute},
		{"eventsPerMinute", settings.EventsPerMinute},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < 1 || *field.value > MaxPerMinute {
			problems = append(problems, &tenantsettings.FieldError{
				Field:   field.name,
				Message: fmt.Sprintf("must be between 1 and %d, got %d", MaxPerMinute, *field.value),
			})
		}
	}
	return errors.Join(problems...)
}

// allowance is the override for bucket, 0 when the tenant set none.
func (settings TenantSettings) allowance(bucket string) int {
	var value *int
	switch bucket {
	case BucketRun:
		value = settings.RunPerMinute
	case BucketWebhook:
		value = settings.WebhookPerMinute
	case BucketEvents:
		value = settings.EventsPerMinute
	}
	if value == nil {
		return 0
	}
	return *value
}

// RegisterSettings adds the rateLimit section to a tenant-settings registry.
func RegisterSettings(registry *tenantsettings.Registry) error {
	return tenantsettings.Register(registry, SettingsSection, TenantSettings.Validate)
}

// SettingsOverrides reads per-tenant allowances from the tenant-settings
// cache, so a limiter wired with it honours each tenant's rateLimit section.
type SettingsOverrides struct {
	Cache *tenantsettings.Cache
}

// Allowance returns the tenant's override for bucket, or 0 for the deployment
// default. A settings read that fails is logged and answered with the
// default: a settings outage must not take every ingress down with it.
func (overrides SettingsOverrides) Allowance(ctx context.Context, tenantID, bucket string) int {
	settings, ok, err := tenantsettings.Read[TenantSettings](ctx, overrides.Cache, tenantID, SettingsSection)
	if err != nil {
		slog.WarnContext(ctx, "tenant rate-limit settings unavailable; the deployment default applies",
			"tenant", tenantID, "bucket", bucket, "error", err)
		return 0
	}
	if !ok {
		return 0
	}
	return settings.allowance(bucket)
}
