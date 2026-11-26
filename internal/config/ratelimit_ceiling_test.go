package config

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ratelimit"
)

// A tenant's rateLimit setting is bounded by ratelimit.MaxPerMinute and the
// deployment's ratelimit.* by rateLimitPerMinuteCeiling. They are one bound
// written in two packages, so this keeps a tenant override from accepting a
// rate the deployment configuration would refuse, or the other way round.
func TestTheTenantRateLimitBoundMatchesTheConfigurationCeiling(t *testing.T) {
	if ratelimit.MaxPerMinute != rateLimitPerMinuteCeiling {
		t.Fatalf("ratelimit.MaxPerMinute = %d, config ceiling = %d; keep them equal",
			ratelimit.MaxPerMinute, rateLimitPerMinuteCeiling)
	}
}
