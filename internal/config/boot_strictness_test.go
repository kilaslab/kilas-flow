package config

import (
	"bytes"
	"encoding/json"
	"io"
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

// warningLine returns the captured warning that is about key, or "".
func warningLine(warnings, key string) string {
	for line := range strings.SplitSeq(warnings, "\n") {
		if strings.Contains(line, " key="+key+" ") {
			return line
		}
	}
	return ""
}

func TestTheSecretVariablesTheProductDocumentsAreNotReportedAsUnknown(t *testing.T) {
	// Every one of these is read out of band — credentials.KeyFromEnvironment
	// and its siblings — and every one used to be reported at boot as a key
	// that "matches nothing and was ignored", with a did_you_mean pointing at
	// an unrelated section. An operator read that as their encryption key being
	// ignored, and renamed the embed key to the variable the suggestion named.
	documented := []string{
		"KILASFLOW_ENCRYPTION_KEY",
		"KILASFLOW_AUTH_SIGNING_KEY",
		"KILASFLOW_EMBED_SIGNING_KEY",
		"KILASFLOW_BOOTSTRAP_PASSWORD",
		"KILASFLOW_AUTH_OPERATOR_KEY",
		"KILASFLOW_GOOGLE_CLIENT_SECRET",
	}
	for _, variable := range documented {
		t.Setenv(variable, "a-secret-value")
	}

	warnings := captureWarnings(t, func() {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
	if strings.TrimSpace(warnings) != "" {
		t.Errorf("a documented secret variable was warned about:\n%s", warnings)
	}
}

func TestARenamedSecretVariableIsExemptAndTheOldDefaultIsNot(t *testing.T) {
	// The setting's current value is what the process reads, so that is the
	// name the mapper has to leave alone. The default is no longer read once
	// the setting points elsewhere, and a key left in its old variable is
	// exactly the mistake worth a warning.
	t.Setenv("KILASFLOW_SECURITY_ENCRYPTION_KEY_ENV", "KILASFLOW_VAULT_KEY")
	t.Setenv("KILASFLOW_VAULT_KEY", "a-secret-value")
	t.Setenv("KILASFLOW_EMBED_SIGNING_KEY_ENV", "KILASFLOW_HOST_EMBED_KEY")
	t.Setenv("KILASFLOW_HOST_EMBED_KEY", "a-secret-value")
	t.Setenv("KILASFLOW_ENCRYPTION_KEY", "left-behind")

	warnings := captureWarnings(t, func() {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
	for _, renamed := range []string{"vault.key", "host.embed_key"} {
		if warningLine(warnings, renamed) != "" {
			t.Errorf("the renamed variable behind %s was warned about:\n%s", renamed, warnings)
		}
	}
	if line := warningLine(warnings, "encryption.key"); !strings.Contains(line, "variable=KILASFLOW_ENCRYPTION_KEY") {
		t.Errorf("the variable no setting names any more was not reported; warnings were:\n%s", warnings)
	}
}

func TestEverySettingThatNamesAVariableExemptsIt(t *testing.T) {
	// Walked from the struct, so the *_env field someone adds next is covered
	// here without anyone remembering to list it: it is set to a variable of its
	// own, the variable is set, and nothing may be reported.
	var settings []string
	for path := range fieldPaths() {
		leaf := path[strings.LastIndex(path, ".")+1:]
		if strings.HasSuffix(leaf, "_env") {
			settings = append(settings, path)
		} else if strings.Contains(leaf, "env") {
			t.Errorf("%s looks like it names an environment variable but does not end in _env, so the variable it names would be reported as ignored", path)
		}
	}
	if len(settings) == 0 {
		t.Fatal("no *_env setting found: the walk is broken")
	}
	for _, path := range settings {
		name := strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
		sentinel := "KILASFLOW_SENTINEL_" + name
		t.Setenv(EnvPrefix+name, sentinel)
		t.Setenv(sentinel, "a-secret-value")
	}
	// A secrets manager is all or nothing, and the token setting is one of the
	// settings above.
	t.Setenv("KILASFLOW_SECRETS_MANAGER_ADDR", "https://vault.example:8200")
	t.Setenv("KILASFLOW_SECRETS_MASTER_KEY", "prod/master-key")

	warnings := captureWarnings(t, func() {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
	if strings.TrimSpace(warnings) != "" {
		t.Errorf("a variable named by a *_env setting was warned about:\n%s", warnings)
	}
}

func TestVariablesTheProcessReadsItselfAreNotReportedAsUnknown(t *testing.T) {
	// The $env allowlist and the CLI's two variables never reach the
	// configuration tree by design; the last one is a variable of the same
	// shape that nothing reads.
	t.Setenv("KILASFLOW_WORKFLOW_ENV_GREETING", "hello")
	t.Setenv("KILASFLOW_URL", "http://127.0.0.1:8080")
	t.Setenv("KILASFLOW_TOKEN", "a-secret-value")
	t.Setenv("KILASFLOW_URLS", "http://127.0.0.1:8080")

	warnings := captureWarnings(t, func() {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})
	for _, exempt := range []string{"workflow.env_greeting", "url", "token"} {
		if warningLine(warnings, exempt) != "" {
			t.Errorf("the variable behind %s was warned about:\n%s", exempt, warnings)
		}
	}
	if warningLine(warnings, "urls") == "" {
		t.Errorf("a variable nothing reads was not reported; warnings were:\n%s", warnings)
	}
}

func TestAGenuinelyUnknownVariableStillWarnsWhenTheSecretsAreSet(t *testing.T) {
	// Exempting the secret variables must not turn the check off: it is the only
	// thing that tells an operator a misspelled setting was dropped.
	t.Setenv("KILASFLOW_ENCRYPTION_KEY", "a-secret-value")
	t.Setenv("KILASFLOW_EMBED_SIGNING_KEY", "a-secret-value")
	t.Setenv("KILASFLOW_SERVR_PORT", "9090")
	t.Setenv("KILASFLOW_QUARTERLY_REVENUE_FORECAST_MODEL", "x")

	warnings := captureWarnings(t, func() {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})

	typo := warningLine(warnings, "servr.port")
	for _, want := range []string{"source=environment", "variable=KILASFLOW_SERVR_PORT", "did_you_mean=server.port"} {
		if !strings.Contains(typo, want) {
			t.Errorf("the misspelled variable's warning lacks %q; warnings were:\n%s", want, warnings)
		}
	}
	// Nothing is near enough to be a correction, so none is offered.
	far := warningLine(warnings, "quarterly.revenue_forecast_model")
	if far == "" {
		t.Fatalf("an unrelated unknown variable was not reported; warnings were:\n%s", warnings)
	}
	if strings.Contains(far, "did_you_mean") {
		t.Errorf("a suggestion was offered for a key nothing is close to:\n%s", far)
	}
	for _, secret := range []string{"encryption.key", "embed.signing_key"} {
		if warningLine(warnings, secret) != "" {
			t.Errorf("the secret variable behind %s was warned about:\n%s", secret, warnings)
		}
	}
}

func TestAnUnknownKeyNamesWhereItCameFrom(t *testing.T) {
	// The file's typo and the environment's used to be labelled with the file's
	// name, "(not found)" included when the file was absent, so a mistyped
	// variable read as a problem with a file that was never there.
	path := filepath.Join(t.TempDir(), "typo.yaml")
	if err := os.WriteFile(path, []byte("binary:\n  rootdir: ./data/binary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORK", "true")

	warnings := captureWarnings(t, func() {
		if _, err := Load(path); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})

	fromFile := warningLine(warnings, "binary.rootdir")
	if !strings.Contains(fromFile, "source="+path) || strings.Contains(fromFile, "variable=") {
		t.Errorf("the file's key should name the file and no variable:\n%s", fromFile)
	}
	fromEnvironment := warningLine(warnings, "outbound.allow_private_network")
	for _, want := range []string{"source=environment", "variable=KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORK"} {
		if !strings.Contains(fromEnvironment, want) {
			t.Errorf("the variable's warning lacks %q:\n%s", want, fromEnvironment)
		}
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

// captureStdout collects what the package writes to the process's own stdout,
// which is the stream the binary's logger is configured to use.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	previous := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = previous }()

	fn()
	if err := write.Close(); err != nil {
		t.Fatalf("close the captured stdout = %v", err)
	}
	captured, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("read the captured stdout = %v", err)
	}
	_ = read.Close()
	return string(captured)
}

func TestTheUnknownKeyWarningFollowsTheConfiguredLogFormat(t *testing.T) {
	// The warning fires while the configuration is still being read, so the
	// configured logger does not exist yet — and it used to go through slog's
	// default handler: text on stderr. A deployment logging JSON to stdout
	// therefore never saw the one line saying that a security setting had been
	// ignored, which is the line the warning exists for.
	t.Setenv("KILASFLOW_LOG_FORMAT", "json")
	path := filepath.Join(t.TempDir(), "typo.yaml")
	if err := os.WriteFile(path, []byte("outbound:\n  allowed_private_endpoint:\n    - 127.0.0.1:11434\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := captureStdout(t, func() {
		if _, err := Load(path); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	})

	// One JSON record per warning, naming the key and the nearest one that
	// exists: the same fields the text handler carries, in the format the
	// deployment asked for.
	lines := strings.Split(strings.TrimSpace(warnings), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout = %q, want one JSON warning", warnings)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("the warning is not JSON: %q (%v)", lines[0], err)
	}
	if record["msg"] != "configuration key matches nothing and was ignored" {
		t.Errorf("msg = %v, want the unknown-key warning", record["msg"])
	}
	if record["key"] != "outbound.allowed_private_endpoint" {
		t.Errorf("key = %v, want the misspelled key", record["key"])
	}
	if record["did_you_mean"] != "outbound.allowed_private_endpoints" {
		t.Errorf("did_you_mean = %v, want the key that exists", record["did_you_mean"])
	}
}
