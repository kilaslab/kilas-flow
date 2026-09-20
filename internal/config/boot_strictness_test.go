package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cover the configuration defects the full review reproduced on a
// live instance: a list-valued key that accepted only one entry from the
// environment, an explicitly named file that did not have to exist, an unknown
// key that was dropped in silence, a log format that fell back instead of
// refusing, and binary storage that was off by default with nothing said.

// captureWarnings collects what the config package logs while fn runs.
//
// The package warns through the global logger because the knowledge of what was
// read exists only where the reading happens; the test installs its own
// default and reads it back.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	previous := slog.Default()
	var buffer bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	fn()
	return buffer.String()
}

func TestEveryListKeyTakesSeveralEntriesFromTheEnvironment(t *testing.T) {
	// The generated reference documents an Env var for each of these, and the
	// default decoder turned one string into a one-element slice — so a
	// container with no mounted file could allow exactly one private endpoint,
	// one outbound host, and one embed origin, and a combined value was either
	// refused (the endpoint list, whose grammar caught it) or accepted as a
	// pattern that never matched (the other two). Silence in both directions.
	t.Setenv("KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS", "127.0.0.1:8091, 127.0.0.1:8092")
	t.Setenv("KILASFLOW_OUTBOUND_ALLOWED_HOSTS", "example.com,*.internal.example")
	t.Setenv("KILASFLOW_EMBED_ALLOWED_ORIGINS", "https://host.example, https://second.example")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Outbound.AllowedPrivateEndpoints; len(got) != 2 || got[1] != "127.0.0.1:8092" {
		t.Errorf("AllowedPrivateEndpoints = %v, want both entries with surrounding space trimmed", got)
	}
	if got := cfg.Outbound.AllowedHosts; len(got) != 2 || got[1] != "*.internal.example" {
		t.Errorf("AllowedHosts = %v, want both entries", got)
	}
	if got := cfg.Embed.AllowedOrigins; len(got) != 2 || got[0] != "https://host.example" {
		t.Errorf("AllowedOrigins = %v, want both entries", got)
	}
}

func TestASingleListEntryStillReadsAsOneElement(t *testing.T) {
	t.Setenv("KILASFLOW_EMBED_ALLOWED_ORIGINS", "https://only.example")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Embed.AllowedOrigins; len(got) != 1 || got[0] != "https://only.example" {
		t.Errorf("AllowedOrigins = %v, want the single entry", got)
	}
}

func TestAValueThatMerelyContainsACommaIsNotSplit(t *testing.T) {
	// Only fields that are lists are split. A DSN, a password, or a filesystem
	// path may legitimately contain a comma, and splitting those would corrupt
	// a value that was perfectly correct.
	const dsn = "postgres://user:pa,ss@localhost/kilasflow?options=a,b"
	t.Setenv("KILASFLOW_DATABASE_DSN", dsn)
	t.Setenv("KILASFLOW_DATABASE_DRIVER", "postgres")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.DSN != dsn {
		t.Errorf("Database.DSN = %q, want %q", cfg.Database.DSN, dsn)
	}
}

func TestAnUnknownKeyIsReportedWithTheNearestMatch(t *testing.T) {
	// The review's repro: a config file spelling `allowed_private_endpoint`
	// without its final "s", plus KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORK, also
	// missing an "s". Both booted with nothing applied and nothing said.
	path := filepath.Join(t.TempDir(), "typo.yaml")
	body := "outbound:\n  allowed_private_endpoint:\n    - 127.0.0.1:11434\nbinary:\n  rootdir: ./data/binary\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORK", "true")

	warnings := captureWarnings(t, func() {
		if _, err := Load(path); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})

	for _, want := range []string{
		"outbound.allowed_private_endpoint",
		"outbound.allow_private_network",
		"binary.rootdir",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("no warning named %q; warnings were:\n%s", want, warnings)
		}
	}
	if !strings.Contains(warnings, "outbound.allowed_private_endpoints") {
		t.Errorf("no warning suggested the key that exists; warnings were:\n%s", warnings)
	}
	if !strings.Contains(warnings, "binary.root") {
		t.Errorf("no warning suggested binary.root for binary.rootdir; warnings were:\n%s", warnings)
	}
}

func TestAKnownKeyIsNeverReportedAsUnknown(t *testing.T) {
	// A warning that fires on correct configuration is worse than none: an
	// operator learns to ignore the line that matters.
	path := filepath.Join(t.TempDir(), "good.yaml")
	body := "server:\n  port: 9090\noutbound:\n  allowed_private_endpoints:\n    - 127.0.0.1:11434\nbinary:\n  root: ./data/binary\n  max_bytes: 1048576\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILASFLOW_OUTBOUND_ALLOWED_HOSTS", "example.com")
	t.Setenv("KILASFLOW_EXECUTION_DEFAULT_TIMEZONE", "Asia/Jakarta")

	warnings := captureWarnings(t, func() {
		if _, err := Load(path); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
	if strings.TrimSpace(warnings) != "" {
		t.Errorf("a correct configuration was warned about:\n%s", warnings)
	}
}

func TestAnExplicitlyNamedConfigFileMustExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

	// The default name is looked for and its absence is normal: that is how an
	// environment-only deployment starts.
	if _, err := Load(missing); err != nil {
		t.Fatalf("Load() on a missing default file must stay silent, got %v", err)
	}
	// A path the operator typed is a statement about the file existing.
	if _, err := LoadExplicit(missing); err == nil {
		t.Fatal("LoadExplicit() accepted a path that does not exist")
	} else if !strings.Contains(err.Error(), "does-not-exist.yaml") {
		t.Errorf("error = %v, want it to name the file", err)
	}
}

func TestAnUnsupportedLogFormatIsRefused(t *testing.T) {
	cfg := Default()
	cfg.Log.Format = "jsonl"
	if err := cfg.Validate(); err == nil {
		t.Fatal("log.format jsonl was accepted: a log pipeline would silently collect nothing")
	}
	for _, supported := range []string{"text", "json"} {
		cfg := Default()
		cfg.Log.Format = supported
		if err := cfg.Validate(); err != nil {
			t.Errorf("log.format %q was refused: %v", supported, err)
		}
	}
}

func TestBinaryStorageIsOnByDefaultBesideTheDatabase(t *testing.T) {
	// The stock install and the container image used to run with binary storage
	// disabled: the HTTP node's autodetect then decoded a downloaded file as
	// text and reported success.
	cfg := Default()
	if strings.TrimSpace(cfg.Binary.Root) == "" {
		t.Fatal("binary.root defaults to empty, so a fresh install silently corrupts file downloads")
	}
	if filepath.Dir(cfg.Binary.Root) != filepath.Dir(cfg.Database.DSN) {
		t.Errorf("binary.root %q is not beside the database %q, so the two do not share a volume",
			cfg.Binary.Root, cfg.Database.DSN)
	}

	// Turning it off deliberately still works, and is what the boot warning is
	// for.
	path := filepath.Join(t.TempDir(), "off.yaml")
	if err := os.WriteFile(path, []byte("binary:\n  root: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.TrimSpace(loaded.Binary.Root) != "" {
		t.Errorf("binary.root = %q, want the explicit empty string to disable storage", loaded.Binary.Root)
	}
}

func TestExecutionSchedulingKeysAreReachableAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.WaitSweepInterval != 10*time.Second {
		t.Errorf("WaitSweepInterval = %s, want 10s", cfg.Execution.WaitSweepInterval)
	}
	if cfg.Execution.MaxTimeout != time.Hour {
		t.Errorf("MaxTimeout = %s, want 1h", cfg.Execution.MaxTimeout)
	}
	if cfg.Execution.DefaultTimezone != "" {
		t.Errorf("DefaultTimezone = %q, want empty (UTC)", cfg.Execution.DefaultTimezone)
	}

	t.Setenv("KILASFLOW_EXECUTION_WAIT_SWEEP_INTERVAL", "3s")
	t.Setenv("KILASFLOW_EXECUTION_MAX_TIMEOUT", "45m")
	t.Setenv("KILASFLOW_EXECUTION_DEFAULT_TIMEZONE", "Asia/Jakarta")
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.WaitSweepInterval != 3*time.Second || cfg.Execution.MaxTimeout != 45*time.Minute {
		t.Errorf("Execution = %#v, want the environment's durations", cfg.Execution)
	}
	if cfg.Execution.DefaultTimezone != "Asia/Jakarta" {
		t.Errorf("DefaultTimezone = %q, want the environment's zone", cfg.Execution.DefaultTimezone)
	}

	// A zone nothing can load is refused at boot rather than silently
	// computing every schedule in UTC.
	broken := Default()
	broken.Execution.DefaultTimezone = "Mars/Olympus"
	if err := broken.Validate(); err == nil {
		t.Fatal("an unknown default timezone was accepted")
	}
}

func TestTheOperatorKeyEnvironmentVariableIsNamedByDefault(t *testing.T) {
	// The admin surface registers the key it finds under this name at boot.
	// Without a default the operator key would have to be named twice, and the
	// failure mode is an admin API nobody can reach.
	if got, want := Default().Auth.OperatorKeyEnv, "KILASFLOW_AUTH_OPERATOR_KEY"; got != want {
		t.Errorf("OperatorKeyEnv = %q, want %q", got, want)
	}
	t.Setenv("KILASFLOW_AUTH_OPERATOR_KEY_ENV", "MY_OPERATOR_KEY")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.OperatorKeyEnv != "MY_OPERATOR_KEY" {
		t.Errorf("OperatorKeyEnv = %q, want the environment's override", cfg.Auth.OperatorKeyEnv)
	}
}
