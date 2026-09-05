package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
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
		// A private-endpoint entry that names no port, or names a whole
		// network, or is a pasted URL, would be ignored by the guard. Ignored
		// is safe but silent, and silence here reads as the guard being broken.
		"private endpoint without a port": func(c *Config) {
			c.Outbound.AllowedPrivateEndpoints = []string{"localhost"}
		},
		"private endpoint with a wildcard": func(c *Config) {
			c.Outbound.AllowedPrivateEndpoints = []string{"*.internal:11434"}
		},
		"private endpoint pasted as a URL": func(c *Config) {
			c.Outbound.AllowedPrivateEndpoints = []string{"http://localhost:11434/v1"}
		},
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

func TestTheSQLCeilingDefaultsMatchTheNodePackage(t *testing.T) {
	// Two packages state the same numbers: sqlnode so a Ceiling built from a
	// zero value still bounds something, and here so an operator reading the
	// configuration sees what they are. This is what keeps them one number.
	ceiling := sqlnode.DefaultCeiling()
	cfg := Default().SQL
	if cfg.MaxRows != ceiling.MaxRows {
		t.Errorf("SQL.MaxRows = %d, want sqlnode's %d", cfg.MaxRows, ceiling.MaxRows)
	}
	if cfg.MaxStatementTimeout != ceiling.MaxTimeout {
		t.Errorf("SQL.MaxStatementTimeout = %s, want sqlnode's %s", cfg.MaxStatementTimeout, ceiling.MaxTimeout)
	}
}

func TestTheSQLCeilingIsReachableFromTheEnvironment(t *testing.T) {
	// The section is one word for exactly this reason: envKeyToPath treats the
	// first underscore as the section separator, so a section named sql_nodes
	// could never be overridden at all.
	t.Setenv("KILASFLOW_SQL_MAX_ROWS", "250")
	t.Setenv("KILASFLOW_SQL_MAX_STATEMENT_TIMEOUT", "90s")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SQL.MaxRows != 250 {
		t.Errorf("SQL.MaxRows = %d, want the environment's 250", cfg.SQL.MaxRows)
	}
	if cfg.SQL.MaxStatementTimeout != 90*time.Second {
		t.Errorf("SQL.MaxStatementTimeout = %s, want the environment's 90s", cfg.SQL.MaxStatementTimeout)
	}
}

// An operator who has never configured history must keep all of it. Anything
// else means installing a new build silently deletes a customer's versions.
func TestHistoryRetentionDefaultsToKeepingEverything(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.History.Retention != 0 {
		t.Errorf("History.Retention = %s, want 0 meaning keep every age", cfg.History.Retention)
	}
	if cfg.History.MaxVersions != 0 {
		t.Errorf("History.MaxVersions = %d, want 0 meaning keep every count", cfg.History.MaxVersions)
	}
}

func TestHistoryRetentionIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	if err := os.WriteFile(path, []byte("history:\n  retention: 24h\n  max_versions: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.History.Retention != 24*time.Hour {
		t.Errorf("History.Retention from YAML = %s, want 24h", cfg.History.Retention)
	}
	if cfg.History.MaxVersions != 10 {
		t.Errorf("History.MaxVersions from YAML = %d, want 10", cfg.History.MaxVersions)
	}

	// The section is one word for exactly this reason: envKeyToPath treats the
	// first underscore as the section separator, so a section named
	// workflow_history could never be reached from the environment at all.
	t.Setenv("KILASFLOW_HISTORY_RETENTION", "72h")
	t.Setenv("KILASFLOW_HISTORY_MAX_VERSIONS", "25")

	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.History.Retention != 72*time.Hour {
		t.Errorf("History.Retention = %s, want the environment's 72h", cfg.History.Retention)
	}
	if cfg.History.MaxVersions != 25 {
		t.Errorf("History.MaxVersions = %d, want the environment's 25", cfg.History.MaxVersions)
	}
}

// TestAPrivateEndpointAllowanceIsEmptyByDefaultAndReachableFromConfiguration
// keeps the exemption something an operator writes down. A deployment that was
// never configured for one has none, and the one that wants one says so in a
// file or an environment variable that an audit can read back.
func TestAPrivateEndpointAllowanceIsEmptyByDefaultAndReachableFromConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Outbound.AllowedPrivateEndpoints) != 0 {
		t.Errorf("AllowedPrivateEndpoints = %v, want none until one is configured", cfg.Outbound.AllowedPrivateEndpoints)
	}
	if cfg.Outbound.AllowPrivateNetworks {
		t.Error("AllowPrivateNetworks defaults to true, which would make the endpoint list pointless")
	}

	contents := "outbound:\n  allowed_private_endpoints:\n    - 127.0.0.1:11434\n    - '[::1]:11434'\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Outbound.AllowedPrivateEndpoints) != 2 {
		t.Fatalf("AllowedPrivateEndpoints from YAML = %v, want both entries", cfg.Outbound.AllowedPrivateEndpoints)
	}

	// The section is one word for the reason OutboundHTTP's own comment gives:
	// envKeyToPath treats the first underscore as the section separator, so a
	// leaf with underscores in it is still reachable but a section with one
	// would not be.
	t.Setenv("KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS", "127.0.0.1:11434")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Outbound.AllowedPrivateEndpoints; len(got) != 1 || got[0] != "127.0.0.1:11434" {
		t.Errorf("AllowedPrivateEndpoints = %v, want the environment's single entry", got)
	}
}
