package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// TestMain drops any KILASFLOW_* variable the caller's environment is carrying
// before a single case runs.
//
// The loader composes the process environment, so a variable the test never set
// is a key it will warn about, and an unexpected warning is a different result:
// the Makefile exports KILASFLOW_WEB_PORT for `make dev`, and with it in the
// environment every case here sees a `web.port` that matches no field — which is
// what fails TestAKnownKeyIsNeverReportedAsUnknown under `make test` while the
// same suite passes when run directly. A suite that only passes in a clean
// environment is testing the shell rather than the loader.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		if name, _, found := strings.Cut(entry, "="); found && strings.HasPrefix(name, EnvPrefix) {
			os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}

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

func TestDatastoreDefaultsBoundGrowth(t *testing.T) {
	cfg := Default().Datastore
	if cfg.MaxDatastoresPerTenant != 100 {
		t.Errorf("Datastore.MaxDatastoresPerTenant = %d, want 100", cfg.MaxDatastoresPerTenant)
	}
	if cfg.MaxColumnsPerDatastore != 100 {
		t.Errorf("Datastore.MaxColumnsPerDatastore = %d, want 100", cfg.MaxColumnsPerDatastore)
	}
	if cfg.MaxRowsPerDatastore != 100_000 {
		t.Errorf("Datastore.MaxRowsPerDatastore = %d, want 100000", cfg.MaxRowsPerDatastore)
	}
	if cfg.MaxValueBytes != 1<<20 {
		t.Errorf("Datastore.MaxValueBytes = %d, want %d", cfg.MaxValueBytes, 1<<20)
	}
}

func TestValidateRejectsBadDatastoreBounds(t *testing.T) {
	cases := map[string]func(*Config){
		"datastore tenant ceiling zero":         func(c *Config) { c.Datastore.MaxDatastoresPerTenant = 0 },
		"datastore tenant ceiling negative":     func(c *Config) { c.Datastore.MaxDatastoresPerTenant = -1 },
		"datastore column ceiling zero":         func(c *Config) { c.Datastore.MaxColumnsPerDatastore = 0 },
		"datastore column ceiling negative":     func(c *Config) { c.Datastore.MaxColumnsPerDatastore = -1 },
		"datastore row ceiling zero":            func(c *Config) { c.Datastore.MaxRowsPerDatastore = 0 },
		"datastore row ceiling negative":        func(c *Config) { c.Datastore.MaxRowsPerDatastore = -1 },
		"datastore value byte ceiling zero":     func(c *Config) { c.Datastore.MaxValueBytes = 0 },
		"datastore value byte ceiling negative": func(c *Config) { c.Datastore.MaxValueBytes = -1 },
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

func TestDatastoreBoundsAreReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	if err := os.WriteFile(path, []byte("datastore:\n  max_rows_per_datastore: 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILASFLOW_DATASTORE_MAX_VALUE_BYTES", "4096")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Datastore.MaxRowsPerDatastore, 5000; got != want {
		t.Errorf("Datastore.MaxRowsPerDatastore = %d, want the file's %d", got, want)
	}
	if got, want := cfg.Datastore.MaxValueBytes, 4096; got != want {
		t.Errorf("Datastore.MaxValueBytes = %d, want the environment's %d", got, want)
	}
	// Untouched bounds keep their defaults.
	if got, want := cfg.Datastore.MaxColumnsPerDatastore, 100; got != want {
		t.Errorf("Datastore.MaxColumnsPerDatastore = %d, want the default %d", got, want)
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
		// database/sql reads a negative open bound as "unlimited", which is
		// indistinguishable from a typo until the server refuses the
		// connections nobody budgeted for.
		"negative open connections": func(c *Config) { c.Database.MaxOpenConns = -1 },
		"negative idle connections": func(c *Config) { c.Database.MaxIdleConns = -1 },
		// A prefix that PostgreSQL would truncate joins two identifiers into
		// one; one that does not end in an underscore reads as part of the
		// table name rather than a namespace.
		"prefix without a trailing underscore": func(c *Config) { c.Database.TablePrefix = "kflow" },
		"prefix with uppercase letters":        func(c *Config) { c.Database.TablePrefix = "Kflow_" },
		"prefix with a dash":                   func(c *Config) { c.Database.TablePrefix = "k-flow_" },
		"prefix past the length cap":           func(c *Config) { c.Database.TablePrefix = "0123456789abcdefg_" },
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

func TestTablePrefixDefaultsToEmptyAndAcceptsAKflowPrefix(t *testing.T) {
	for _, prefix := range []string{"", "kflow_", "tenant42_"} {
		cfg := Default()
		cfg.Database.TablePrefix = prefix
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with table_prefix %q = %v, want nil", prefix, err)
		}
	}
	// The section is one word, so the first-underscore cut reaches the leaf.
	if got := envKeyToPath("KILASFLOW_DATABASE_TABLE_PREFIX"); got != "database.table_prefix" {
		t.Errorf("envKeyToPath = %q, want %q", got, "database.table_prefix")
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

// An operator who has never configured idempotency still gets it, and for a
// day: long enough that a client retrying across a weekend outage is answered
// from the record, short enough that the table is not a permanent ledger.
func TestIdempotencyRetentionDefaultsToADay(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Idempotency.Retention != 24*time.Hour {
		t.Errorf("Idempotency.Retention = %s, want 24h", cfg.Idempotency.Retention)
	}
	if Default().Idempotency.Retention != cfg.Idempotency.Retention {
		t.Errorf("Default() and Load() disagree on the retention: %s vs %s", Default().Idempotency.Retention, cfg.Idempotency.Retention)
	}
}

func TestIdempotencyRetentionIsReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	if err := os.WriteFile(path, []byte("idempotency:\n  retention: 6h\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Idempotency.Retention != 6*time.Hour {
		t.Errorf("Idempotency.Retention from YAML = %s, want 6h", cfg.Idempotency.Retention)
	}

	// The section is one word for the same reason History and Outbound are:
	// envKeyToPath treats the first underscore as the section separator.
	t.Setenv("KILASFLOW_IDEMPOTENCY_RETENTION", "48h")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Idempotency.Retention != 48*time.Hour {
		t.Errorf("Idempotency.Retention = %s, want the environment's 48h", cfg.Idempotency.Retention)
	}
}

// The retention is bounded on both sides. Zero or negative would expire every
// key the moment it is recorded, which is idempotency that never replays; an
// unbounded top would let one typo turn the table into a ledger nobody prunes.
func TestIdempotencyRetentionIsBounded(t *testing.T) {
	refused := map[string]time.Duration{
		"zero":         0,
		"negative":     -time.Hour,
		"under a min":  30 * time.Second,
		"over 30 days": 721 * time.Hour,
	}
	for name, retention := range refused {
		t.Run("refuses "+name, func(t *testing.T) {
			cfg := Default()
			cfg.Idempotency.Retention = retention
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for %s, want an error", retention)
			}
			if !strings.Contains(err.Error(), "idempotency.retention") {
				t.Errorf("Validate() error = %q, want it to name idempotency.retention", err)
			}
		})
	}

	accepted := map[string]time.Duration{
		"one minute":  time.Minute,
		"one day":     24 * time.Hour,
		"thirty days": 720 * time.Hour,
	}
	for name, retention := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			cfg := Default()
			cfg.Idempotency.Retention = retention
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() error = %v for %s, want none", err, retention)
			}
		})
	}
}

// The sidecar is the most expensive thing on the roadmap and buys the least
// coverage, so it must cost a deployment nothing until it is asked for.
func TestSidecarIsDisabledByDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Sidecar.Enabled {
		t.Error("Sidecar.Enabled defaults to true: a stock install would need Node and would start a process")
	}
	if cfg.Sidecar.RuntimeDir != "./data/sidecar" {
		t.Errorf("Sidecar.RuntimeDir = %q, want ./data/sidecar", cfg.Sidecar.RuntimeDir)
	}
	if len(cfg.Sidecar.Packages) != 0 || len(cfg.Sidecar.Wrapper) != 0 {
		t.Errorf("Sidecar allowlists default to %v / %v, want empty", cfg.Sidecar.Packages, cfg.Sidecar.Wrapper)
	}
}

// Two packages state the same numbers: sidecar so a pool built from a zero
// value still bounds something, and here so an operator reading the
// configuration sees what they are. This is what keeps them one number.
func TestTheSidecarLimitsMatchTheNodePackage(t *testing.T) {
	limits := sidecar.DefaultLimits()
	cfg := Default().Sidecar
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Timeout", cfg.Timeout, limits.Timeout},
		{"SpawnTimeout", cfg.SpawnTimeout, limits.SpawnTimeout},
		{"IdleTimeout", cfg.IdleTimeout, limits.IdleTimeout},
		{"MaxHeapMB", cfg.MaxHeapMB, limits.NodeMaxHeapMB},
		{"MaxRSSMB", cfg.MaxRSSMB, limits.MaxRSSMB},
		{"MaxProcesses", cfg.MaxProcesses, limits.MaxProcesses},
		{"MaxOutputBytes", cfg.MaxOutputBytes, limits.MaxOutputBytes},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("Sidecar.%s = %v, want sidecar's %v", check.name, check.got, check.want)
		}
	}
}

func TestSidecarKeysAreReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	body := strings.Join([]string{
		"sidecar:",
		"  enabled: true",
		"  node_path: /opt/node/bin/node",
		"  packages_dir: /opt/sidecar",
		"  packages:",
		"    - kf-fixture-nodes",
		"    - '@scope/pkg'",
		"  wrapper:",
		"    - /usr/bin/setpriv",
		"    - --no-new-privs",
		"  timeout: 45s",
		"  spawn_timeout: 20s",
		"  idle_timeout: 2m",
		"  max_heap_mb: 128",
		"  max_rss_mb: 256",
		"  max_processes: 4",
		"  max_output_bytes: 1048576",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Sidecar.Enabled {
		t.Fatal("Sidecar.Enabled from YAML = false, want true")
	}
	if got, want := cfg.Sidecar.NodePath, "/opt/node/bin/node"; got != want {
		t.Errorf("NodePath = %q, want %q", got, want)
	}
	if got, want := cfg.Sidecar.PackagesDir, "/opt/sidecar"; got != want {
		t.Errorf("PackagesDir = %q, want %q", got, want)
	}
	if got := cfg.Sidecar.Packages; len(got) != 2 || got[0] != "kf-fixture-nodes" || got[1] != "@scope/pkg" {
		t.Errorf("Packages = %v, want both entries", got)
	}
	if got := cfg.Sidecar.Wrapper; len(got) != 2 || got[0] != "/usr/bin/setpriv" {
		t.Errorf("Wrapper = %v, want both entries", got)
	}
	if got, want := cfg.Sidecar.Timeout, 45*time.Second; got != want {
		t.Errorf("Timeout = %s, want %s", got, want)
	}
	if got, want := cfg.Sidecar.MaxOutputBytes, int64(1<<20); got != want {
		t.Errorf("MaxOutputBytes = %d, want %d", got, want)
	}

	// The section is one word for exactly this reason: envKeyToPath treats the
	// first underscore as the section separator, so a section named side_car
	// could never be overridden at all. Lists are split on commas because that
	// is the only form a container with no mounted file can use.
	t.Setenv("KILASFLOW_SIDECAR_ENABLED", "true")
	t.Setenv("KILASFLOW_SIDECAR_PACKAGES_DIR", "/srv/pkgs")
	t.Setenv("KILASFLOW_SIDECAR_PACKAGES", "kf-fixture-nodes,kf-fixture-versions")
	t.Setenv("KILASFLOW_SIDECAR_WRAPPER", "/usr/bin/bwrap,/usr/bin/setpriv")
	t.Setenv("KILASFLOW_SIDECAR_TIMEOUT", "10s")
	t.Setenv("KILASFLOW_SIDECAR_MAX_PROCESSES", "3")

	cfg, err = Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Sidecar.Packages; len(got) != 2 || got[1] != "kf-fixture-versions" {
		t.Errorf("Packages from the environment = %v, want both entries (comma split)", got)
	}
	if got := cfg.Sidecar.Wrapper; len(got) != 2 {
		t.Errorf("Wrapper from the environment = %v, want both entries (comma split)", got)
	}
	if got, want := cfg.Sidecar.Timeout, 10*time.Second; got != want {
		t.Errorf("Timeout from the environment = %s, want %s", got, want)
	}
	if got, want := cfg.Sidecar.MaxProcesses, 3; got != want {
		t.Errorf("MaxProcesses from the environment = %d, want %d", got, want)
	}
}

func TestValidateRejectsBadSidecarConfig(t *testing.T) {
	valid := func() Sidecar {
		return Sidecar{
			Enabled:        true,
			NodePath:       "",
			PackagesDir:    "/opt/sidecar",
			Packages:       []string{"kf-fixture-nodes"},
			RuntimeDir:     "./data/sidecar",
			Timeout:        30 * time.Second,
			SpawnTimeout:   15 * time.Second,
			IdleTimeout:    5 * time.Minute,
			MaxHeapMB:      256,
			MaxRSSMB:       512,
			MaxProcesses:   16,
			MaxOutputBytes: 4 << 20,
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Sidecar)
		wantSub string
	}{
		{"no packages directory", func(c *Sidecar) { c.PackagesDir = "  " }, "packages_dir is required"},
		{"no runtime directory", func(c *Sidecar) { c.RuntimeDir = "" }, "runtime_dir is required"},
		{"no packages", func(c *Sidecar) { c.Packages = nil }, "at least one package"},
		{"parent traversal", func(c *Sidecar) { c.Packages = []string{"../escape"} }, "not an npm package name"},
		{"absolute path", func(c *Sidecar) { c.Packages = []string{"/etc/passwd"} }, "not an npm package name"},
		{"uppercase", func(c *Sidecar) { c.Packages = []string{"Bad"} }, "not an npm package name"},
		{"bad scope", func(c *Sidecar) { c.Packages = []string{"@scope/"} }, "not an npm package name"},
		{"zero timeout", func(c *Sidecar) { c.Timeout = 0 }, "timeout"},
		{"negative spawn timeout", func(c *Sidecar) { c.SpawnTimeout = -time.Second }, "spawn_timeout"},
		{"zero idle", func(c *Sidecar) { c.IdleTimeout = 0 }, "idle_timeout"},
		{"tiny heap", func(c *Sidecar) { c.MaxHeapMB = 8 }, "max_heap_mb"},
		{"no processes", func(c *Sidecar) { c.MaxProcesses = 0 }, "max_processes"},
		{"zero output", func(c *Sidecar) { c.MaxOutputBytes = 0 }, "max_output_bytes"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Sidecar = valid()
			test.mutate(&cfg.Sidecar)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want a refusal naming %q", test.wantSub)
			}
			if !strings.Contains(err.Error(), test.wantSub) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, test.wantSub)
			}
		})
	}

	// A disabled sidecar is never read, so a stray value in its section must
	// not refuse a boot.
	off := Default()
	off.Sidecar = valid()
	off.Sidecar.Packages = nil
	off.Sidecar.Enabled = false
	if err := off.Validate(); err != nil {
		t.Errorf("Validate() refused a disabled sidecar: %v", err)
	}

	// A non-unix host cannot run the sidecar at all, and saying so at boot is
	// better than a workflow discovering it. Injected because the test cannot
	// be run on Windows.
	restore := sidecarHostIsUnix
	sidecarHostIsUnix = false
	t.Cleanup(func() { sidecarHostIsUnix = restore })
	strict := Default()
	strict.Sidecar = valid()
	err := strict.Validate()
	if err == nil || !strings.Contains(err.Error(), "unix host") {
		t.Errorf("Validate() on a non-unix host = %v, want it refused at boot", err)
	}
}

// The Code node's three keys are the difference between "install a toolchain
// correctly" being an instruction an operator can follow in a shell-less image
// and being a riddle. They are pinned here as defaults so a later change to
// one of them is a decision rather than an accident.
func TestTheCodeDefaultsAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.GoBinary != "go" {
		t.Errorf("Code.GoBinary = %q, want the go command on PATH", cfg.Code.GoBinary)
	}
	if cfg.Code.CacheDir != "./data/codecache" {
		t.Errorf("Code.CacheDir = %q, want a directory inside the data volume", cfg.Code.CacheDir)
	}
	if cfg.Code.CacheMaxBytes != 2147483648 {
		t.Errorf("Code.CacheMaxBytes = %d, want 2 GiB", cfg.Code.CacheMaxBytes)
	}
}

func TestTheCodeKeysAreReachableFromYAMLAndTheEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kilasflow.yaml")
	body := "code:\n  go_binary: /opt/golang/bin/go\n  cache_dir: /srv/codecache\n  cache_max_bytes: 1048576\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.GoBinary != "/opt/golang/bin/go" {
		t.Errorf("Code.GoBinary from YAML = %q", cfg.Code.GoBinary)
	}
	if cfg.Code.CacheDir != "/srv/codecache" {
		t.Errorf("Code.CacheDir from YAML = %q", cfg.Code.CacheDir)
	}
	if cfg.Code.CacheMaxBytes != 1048576 {
		t.Errorf("Code.CacheMaxBytes from YAML = %d", cfg.Code.CacheMaxBytes)
	}

	// The section is one word so that envKeyToPath's first-underscore rule
	// reaches code.cache_dir, which is the whole reason it is not code_node.
	t.Setenv("KILASFLOW_CODE_GO_BINARY", "/usr/local/go/bin/go")
	t.Setenv("KILASFLOW_CODE_CACHE_DIR", "/var/lib/kilasflow/codecache")
	t.Setenv("KILASFLOW_CODE_CACHE_MAX_BYTES", "0")

	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Code.GoBinary != "/usr/local/go/bin/go" {
		t.Errorf("Code.GoBinary = %q, want the environment's path", cfg.Code.GoBinary)
	}
	if cfg.Code.CacheDir != "/var/lib/kilasflow/codecache" {
		t.Errorf("Code.CacheDir = %q, want the environment's path", cfg.Code.CacheDir)
	}
	if cfg.Code.CacheMaxBytes != 0 {
		t.Errorf("Code.CacheMaxBytes = %d, want an explicit zero meaning unbounded", cfg.Code.CacheMaxBytes)
	}
}

// A negative budget is not a smaller budget: it would mean the cache evicts
// everything it ever writes, including the artifact the operator's toolchain
// just produced.
func TestValidateRejectsANegativeCodeCacheBudget(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Code.CacheMaxBytes = -1
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() accepted a negative code.cache_max_bytes")
	}
	cfg.Code.CacheMaxBytes = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() rejected a zero code.cache_max_bytes: %v", err)
	}
}

// TestExecutionDefaultTimeoutIsTwoMinutesAndConfigurable: one minute ended AI
// agent runs on local and reasoning models long before they answered. The
// owner set the stock budget to two minutes; a deployment still sets its own.
func TestExecutionDefaultTimeoutIsTwoMinutesAndConfigurable(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.DefaultTimeout != 2*time.Minute {
		t.Errorf("Execution.DefaultTimeout = %s, want 2m0s", cfg.Execution.DefaultTimeout)
	}

	t.Setenv("KILASFLOW_EXECUTION_DEFAULT_TIMEOUT", "5m")
	cfg, err = Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.DefaultTimeout != 5*time.Minute {
		t.Errorf("Execution.DefaultTimeout from the environment = %s, want 5m0s", cfg.Execution.DefaultTimeout)
	}
}
