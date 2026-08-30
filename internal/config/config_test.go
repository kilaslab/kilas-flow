package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("a missing config file must not be an error, got %v", err)
	}

	if got, want := cfg.Server.Addr(), "0.0.0.0:8080"; got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
	if got, want := cfg.Database.Driver, "sqlite"; got != want {
		t.Errorf("Database.Driver = %q, want %q", got, want)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "server:\n  port: 9090\ndatabase:\n  driver: postgres\n  dsn: postgres://localhost/kilasflow\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := cfg.Server.Port, 9090; got != want {
		t.Errorf("Server.Port = %d, want %d", got, want)
	}
	if got, want := cfg.Database.Driver, "postgres"; got != want {
		t.Errorf("Database.Driver = %q, want %q", got, want)
	}
	// Untouched keys must keep their defaults.
	if got, want := cfg.Server.Host, "0.0.0.0"; got != want {
		t.Errorf("Server.Host = %q, want %q", got, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 9090\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KILASFLOW_SERVER_PORT", "7070")
	t.Setenv("KILASFLOW_LOG_LEVEL", "debug")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := cfg.Server.Port, 7070; got != want {
		t.Errorf("env must win over file: Server.Port = %d, want %d", got, want)
	}
	if got, want := cfg.Log.Level, "debug"; got != want {
		t.Errorf("Log.Level = %q, want %q", got, want)
	}
}

// Leaf keys contain underscores, so the env mapping must split only on the
// section boundary. This is the regression test for server.read.header.timeout.
func TestEnvKeyToPath(t *testing.T) {
	cases := map[string]string{
		"KILASFLOW_SERVER_PORT":                "server.port",
		"KILASFLOW_SERVER_READ_HEADER_TIMEOUT": "server.read_header_timeout",
		"KILASFLOW_DATABASE_MAX_OPEN_CONNS":    "database.max_open_conns",
		"KILASFLOW_LOG_LEVEL":                  "log.level",
	}

	for in, want := range cases {
		if got := envKeyToPath(in); got != want {
			t.Errorf("envKeyToPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvOverridesUnderscoredLeafKey(t *testing.T) {
	t.Setenv("KILASFLOW_SERVER_READ_HEADER_TIMEOUT", "45s")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := cfg.Server.ReadHeaderTimeout, 45*time.Second; got != want {
		t.Errorf("ReadHeaderTimeout = %v, want %v", got, want)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	cases := map[string]func(*Config){
		"port out of range": func(c *Config) { c.Server.Port = 0 },
		"unknown driver":    func(c *Config) { c.Database.Driver = "mongodb" },
		"empty dsn":         func(c *Config) { c.Database.DSN = "" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() = nil, want error")
			}
		})
	}
}
