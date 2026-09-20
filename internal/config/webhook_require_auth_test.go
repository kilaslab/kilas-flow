package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The setting is off unless an operator asks for it: turning it on by default
// would refuse every imported workflow whose trigger never authenticated
// anything, which is most of them.
func TestWebhookRequireAuthDefaultsOffAndReadsTheEnvironment(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		if Default().Webhook.RequireAuth {
			t.Error("Default().Webhook.RequireAuth = true, want false")
		}
		cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if cfg.Webhook.RequireAuth {
			t.Error("Webhook.RequireAuth with no configuration = true, want false")
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("KILASFLOW_WEBHOOK_REQUIRE_AUTH", "true")
		cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !cfg.Webhook.RequireAuth {
			t.Error("Webhook.RequireAuth with KILASFLOW_WEBHOOK_REQUIRE_AUTH=true = false, want true")
		}
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("webhook:\n  require_auth: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !cfg.Webhook.RequireAuth {
			t.Error("Webhook.RequireAuth with webhook.require_auth: true = false, want true")
		}
	})
}
