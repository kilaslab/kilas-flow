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
		// database/sql reads a negative open bound as "unlimited", which is
		// indistinguishable from a typo until the server refuses the
		// connections nobody budgeted for.
		"negative open connections": func(c *Config) { c.Database.MaxOpenConns = -1 },
		"negative idle connections": func(c *Config) { c.Database.MaxIdleConns = -1 },
		// A negative retention puts the prune cutoff in the future, and every
		// execution ever recorded is older than the future.
		"negative execution retention": func(c *Config) { c.Execution.Retention = -time.Hour },
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

// A PostgreSQL install with no configuration file at all must be able to run
// the concurrency it was configured for.
//
// The pool defaulted to a flat 1 for every driver while execution.max_concurrent
// defaulted to 10, so ten execution workers, a scheduler, the webhook handler
// and every API request queued behind one connection — and every claim that
// "PostgreSQL unlocks concurrency" was a no-op.
func TestThePostgresPoolCoversTheDefaultWorkerCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	body := "database:\n  driver: postgres\n  dsn: postgres://localhost/kilasflow\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxOpenConns <= cfg.Execution.MaxConcurrent {
		t.Errorf("Database.MaxOpenConns = %d with %d workers: the workers queue on the pool",
			cfg.Database.MaxOpenConns, cfg.Execution.MaxConcurrent)
	}
	if cfg.Database.MaxIdleConns != cfg.Database.MaxOpenConns {
		t.Errorf("Database.MaxIdleConns = %d, want it to match the open bound of %d",
			cfg.Database.MaxIdleConns, cfg.Database.MaxOpenConns)
	}
}

// Raising only the worker count raises the pool with it.
//
// This is why the pool is worked out after the layers merge rather than in
// Default(): koanf cannot tell a default apart from a file that repeats it, so
// a number baked into Default() could never follow max_concurrent.
func TestThePoolFollowsExecutionConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	body := "database:\n  driver: postgres\n  dsn: postgres://localhost/kilasflow\nexecution:\n  max_concurrent: 32\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxOpenConns <= 32 {
		t.Errorf("Database.MaxOpenConns = %d, want more than the 32 workers it has to serve",
			cfg.Database.MaxOpenConns)
	}
}

// An explicit pool size wins over the derived one, in both directions.
func TestAnExplicitPoolSizeIsNotOverridden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	body := "database:\n  driver: postgres\n  dsn: postgres://localhost/kilasflow\n" +
		"  max_open_conns: 3\n  max_idle_conns: 2\nexecution:\n  max_concurrent: 50\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxOpenConns != 3 {
		t.Errorf("Database.MaxOpenConns = %d, want the configured 3", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MaxIdleConns != 2 {
		t.Errorf("Database.MaxIdleConns = %d, want the configured 2", cfg.Database.MaxIdleConns)
	}
}

// SQLite stays on one connection whatever the worker count is.
//
// It tolerates a single writer, and the pin and the WAL pragma set are a pair.
// A configuration reporting fifteen while database.Open holds one would send an
// operator hunting the wrong thing.
func TestTheSQLitePoolStaysPinnedToOneConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	if err := os.WriteFile(path, []byte("execution:\n  max_concurrent: 64\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Fatalf("Database.Driver = %q, want the sqlite default", cfg.Database.Driver)
	}
	if cfg.Database.MaxOpenConns != 1 || cfg.Database.MaxIdleConns != 1 {
		t.Errorf("sqlite pool = (%d, %d), want (1, 1)", cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	}
}

// The example an operator copies and the defaults they get without it describe
// the same pool.
//
// The file advertised max_open_conns: 10 and max_idle_conns: 5 while Default()
// set 1 and 1. The documented value is the one people believe, so the two
// disagreeing is worse than either being wrong on its own.
func TestTheExampleConfigAndTheDefaultsAgreeOnThePool(t *testing.T) {
	example, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load(config.example.yaml) error = %v", err)
	}
	defaults, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if example.Database.Driver != defaults.Database.Driver {
		t.Fatalf("the example names driver %q and the defaults %q, so their pools are not comparable",
			example.Database.Driver, defaults.Database.Driver)
	}
	if example.Database.MaxOpenConns != defaults.Database.MaxOpenConns {
		t.Errorf("config.example.yaml advertises max_open_conns %d and the default is %d",
			example.Database.MaxOpenConns, defaults.Database.MaxOpenConns)
	}
	if example.Database.MaxIdleConns != defaults.Database.MaxIdleConns {
		t.Errorf("config.example.yaml advertises max_idle_conns %d and the default is %d",
			example.Database.MaxIdleConns, defaults.Database.MaxIdleConns)
	}
}

// An operator who has never configured execution retention keeps every run.
func TestExecutionRetentionDefaultsToKeepingEverything(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.Retention != 0 {
		t.Errorf("Execution.Retention = %s, want 0 meaning keep every execution", cfg.Execution.Retention)
	}
}

func TestExecutionRetentionIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	if err := os.WriteFile(path, []byte("execution:\n  retention: 24h\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.Retention != 24*time.Hour {
		t.Errorf("Execution.Retention from YAML = %s, want 24h", cfg.Execution.Retention)
	}

	t.Setenv("KILASFLOW_EXECUTION_RETENTION", "72h")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.Retention != 72*time.Hour {
		t.Errorf("Execution.Retention = %s, want the environment's 72h", cfg.Execution.Retention)
	}
}
