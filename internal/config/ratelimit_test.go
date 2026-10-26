package config

import (
	"path/filepath"
	"testing"
)

// The rate limits always apply, so their defaults are real limits: one
// tenant cannot flood the shared queue by doing nothing at all.
func TestRateLimitDefaultsAreGenerousAndAlwaysOn(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RateLimit.Store != "memory" {
		t.Errorf("RateLimit.Store = %q, want memory", cfg.RateLimit.Store)
	}
	for name, want := range map[string]int{
		"RunPerMinute":     cfg.RateLimit.RunPerMinute,
		"WebhookPerMinute": cfg.RateLimit.WebhookPerMinute,
		"EventsPerMinute":  cfg.RateLimit.EventsPerMinute,
	} {
		if want <= 0 {
			t.Errorf("RateLimit.%s = %d, want a positive default", name, want)
		}
	}
	if cfg.RateLimit.RunPerMinute != 120 || cfg.RateLimit.WebhookPerMinute != 600 || cfg.RateLimit.EventsPerMinute != 600 {
		t.Errorf("defaults = run %d, webhook %d, events %d, want 120/600/600",
			cfg.RateLimit.RunPerMinute, cfg.RateLimit.WebhookPerMinute, cfg.RateLimit.EventsPerMinute)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() on the defaults = %v, want nil", err)
	}
}

func TestTheRateLimitSectionIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	// The section is one word for exactly this reason: envKeyToPath treats the
	// first underscore as the section separator, so a section named
	// rate_limit could never be set from the environment.
	t.Setenv("KILASFLOW_RATELIMIT_STORE", "database")
	t.Setenv("KILASFLOW_RATELIMIT_WEBHOOK_PER_MINUTE", "1200")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RateLimit.Store != "database" {
		t.Errorf("RateLimit.Store = %q, want the environment's database", cfg.RateLimit.Store)
	}
	if cfg.RateLimit.WebhookPerMinute != 1200 {
		t.Errorf("RateLimit.WebhookPerMinute = %d, want 1200", cfg.RateLimit.WebhookPerMinute)
	}
	if got := envKeyToPath("KILASFLOW_RATELIMIT_WEBHOOK_PER_MINUTE"); got != "ratelimit.webhook_per_minute" {
		t.Errorf("envKeyToPath = %q, want %q", got, "ratelimit.webhook_per_minute")
	}
}

// A rate that cannot mean what it says is refused: zero would be a limit
// switched off by a typo, and a number past the ceiling is no limit at all.
// Unlike the opt-in sections there is no switch, so this always validates.
func TestValidateRefusesARateLimitThatCannotMeanWhatItSays(t *testing.T) {
	cases := map[string]func(*RateLimit){
		"unknown store":         func(section *RateLimit) { section.Store = "redis" },
		"run rate of zero":      func(section *RateLimit) { section.RunPerMinute = 0 },
		"webhook rate of zero":  func(section *RateLimit) { section.WebhookPerMinute = 0 },
		"events rate of zero":   func(section *RateLimit) { section.EventsPerMinute = 0 },
		"events rate negative":  func(section *RateLimit) { section.EventsPerMinute = -5 },
		"rate past the ceiling": func(section *RateLimit) { section.RunPerMinute = rateLimitPerMinuteCeiling + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg.RateLimit)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() = nil, want the refusal")
			}
		})
	}
}
