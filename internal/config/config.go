// Package config loads kilasflow configuration from defaults, an optional YAML
// file, and KILASFLOW_* environment variables, in that order of increasing
// precedence.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// EnvPrefix is the prefix for environment overrides, e.g. KILASFLOW_SERVER_PORT.
const EnvPrefix = "KILASFLOW_"

// Config is the root configuration document.
type Config struct {
	Server     Server       `koanf:"server"`
	Database   Database     `koanf:"database"`
	Security   Security     `koanf:"security"`
	Auth       Auth         `koanf:"auth"`
	Outbound   OutboundHTTP `koanf:"outbound"`
	Webhook    Webhook      `koanf:"webhook"`
	Embed      Embed        `koanf:"embed"`
	Branding   Branding     `koanf:"branding"`
	Execution  Execution    `koanf:"execution"`
	History    History      `koanf:"history"`
	SQL        SQLNodes     `koanf:"sql"`
	Credential Credential   `koanf:"credential"`
	Binary     Binary       `koanf:"binary"`
	Log        Log          `koanf:"log"`
}

// Server holds HTTP listener settings.
type Server struct {
	Host string `koanf:"host"`
	Port int    `koanf:"port"`

	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	ShutdownTimeout   time.Duration `koanf:"shutdown_timeout"`

	// PublicURL is how this instance is reachable from the internet, without a
	// trailing slash. A webhook lifecycle hook has to tell a remote service
	// where to deliver, and the listen address is not that: an instance behind
	// a proxy or a tunnel binds one address and is reached at another.
	//
	// Empty disables self-registration rather than guessing, because a bot
	// registered against a wrong address receives nothing and reports success.
	PublicURL string `koanf:"public_url"`
}

// Addr returns the host:port the server binds to.
func (s Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// Database selects and configures the internal persistence backend.
//
// This is kilasflow's own storage. It is deliberately unrelated to the SQL nodes a
// workflow may use, which are configured through credentials.
type Database struct {
	// Driver is "sqlite" or "postgres".
	Driver string `koanf:"driver"`
	DSN    string `koanf:"dsn"`

	MaxOpenConns int `koanf:"max_open_conns"`
	MaxIdleConns int `koanf:"max_idle_conns"`
}

// Security holds secret-material settings.
type Security struct {
	// EncryptionKeyEnv names the environment variable holding the AES-256-GCM
	// master key used to encrypt stored credentials. The key itself is never
	// read from the config file.
	EncryptionKeyEnv string `koanf:"encryption_key_env"`
}

// Auth turns identity on and describes how the first account is created.
//
// The koanf section is a single word for the same reason Outbound is:
// envKeyToPath treats the first underscore as the section separator, so a
// section named `auth_session` could never be reached by an environment
// override.
type Auth struct {
	// Enabled gates the whole surface. It defaults to false because turning
	// authentication on for an existing installation locks its operator out of
	// a server they were reaching a moment ago — the API keys and accounts that
	// would let them back in do not exist yet. An operator opts in once they
	// have bootstrapped a user, and the server says so loudly at boot until
	// they do.
	Enabled bool `koanf:"enabled"`
	// SigningKeyEnv names the environment variable holding the session and
	// stream-ticket signing key. Like the credential and embed keys, it never
	// comes from the config file.
	//
	// It is deliberately a different variable from the embed key. One key
	// signing both would mean a forged value of either kind could be presented
	// as the other the moment either payload grew a field the other's parser
	// also accepts.
	SigningKeyEnv string `koanf:"signing_key_env"`
	// SessionTTL is how long a dashboard login lasts. Sessions are stateless,
	// so this is also the longest a stolen session cookie keeps working: there
	// is no server-side revocation to cut it short.
	SessionTTL time.Duration `koanf:"session_ttl"`
	// CookieInsecure drops the Secure attribute and the __Host- cookie prefix.
	//
	// Only for an operator serving plain HTTP on a trusted network. It makes
	// the session cookie readable by a network attacker and plantable by a
	// sibling host, which is exactly what the prefix exists to prevent.
	CookieInsecure bool `koanf:"cookie_insecure"`
	// BootstrapTenant is the tenant a fresh installation creates.
	BootstrapTenant string `koanf:"bootstrap_tenant"`
	// BootstrapEmail and BootstrapPasswordEnv create the first account, and
	// only ever on an installation that has none: a deployment that already has
	// users is never handed another owner by an environment variable somebody
	// forgot to remove.
	BootstrapEmail       string `koanf:"bootstrap_email"`
	BootstrapPasswordEnv string `koanf:"bootstrap_password_env"`
}

// OutboundHTTP bounds requests workflow nodes make to the outside world.
//
// The koanf section is a single word because envKeyToPath treats the first
// underscore as the section separator: a section named `outbound_http` could
// never be reached by an environment override.
//
// The defaults assume a hosted install where a workflow URL is tenant-authored
// and must not be able to reach the cloud metadata service or a neighbouring
// internal service. A self-hosted operator opts out explicitly.
type OutboundHTTP struct {
	AllowPrivateNetworks bool     `koanf:"allow_private_networks"`
	AllowedHosts         []string `koanf:"allowed_hosts"`
	// AllowedPrivateEndpoints admits one `host:port` at a time through the
	// private-address guard while leaving it on for everything else, which is
	// what an install running a model server or a test stub on loopback needs
	// instead of `allow_private_networks: true`. The two settings are not
	// alternatives of the same size: this one names an endpoint, that one
	// hands every outbound request the whole internal network.
	//
	// An entry that is not a host and a port is refused at startup rather than
	// ignored, because an allowance that quietly grants nothing reads as the
	// guard being broken. See safehttp.Policy.AllowedPrivateEndpoints for what
	// the grant does and does not cover.
	AllowedPrivateEndpoints []string      `koanf:"allowed_private_endpoints"`
	MaxRedirects            int           `koanf:"max_redirects"`
	MaxResponseBytes        int64         `koanf:"max_response_bytes"`
	Timeout                 time.Duration `koanf:"timeout"`
}

// Webhook bounds one inbound trigger request.
type Webhook struct {
	MaxBodyBytes    int64         `koanf:"max_body_bytes"`
	ResponseTimeout time.Duration `koanf:"response_timeout"`
}

// Embed configures the iframe editor surface.
//
// The allowlist is empty by default, which means embedding is off: an operator
// opts in per origin rather than discovering their editor is frameable
// anywhere.
type Embed struct {
	// SigningKeyEnv names the environment variable holding the token signing
	// key. Like the credential key, it never comes from the config file.
	SigningKeyEnv  string        `koanf:"signing_key_env"`
	AllowedOrigins []string      `koanf:"allowed_origins"`
	SessionTTL     time.Duration `koanf:"session_ttl"`
}

// Branding drives white-label display options.
type Branding struct {
	Name      string `koanf:"name"`
	Logo      string `koanf:"logo"`
	Favicon   string `koanf:"favicon"`
	PoweredBy bool   `koanf:"powered_by"`
}

// Execution bounds workflow runs.
type Execution struct {
	MaxConcurrent  int           `koanf:"max_concurrent"`
	DefaultTimeout time.Duration `koanf:"default_timeout"`
}

// History bounds how much workflow version history an installation keeps.
//
// Both keys default to unbounded, which is what an existing deployment gets on
// upgrade: an operator who has never configured retention must never discover
// that installing a new build deleted a customer's history. Retention here is a
// configuration knob rather than a licence tier — for a white-label deployment
// it is the operator, not the vendor, who decides how much history a customer
// keeps.
//
// The section name is one word for the same reason Outbound, SQL, Credential
// and Binary are: envKeyToPath treats the first underscore as the section
// separator, so a section called workflow_history could never be reached by
// KILASFLOW_WORKFLOW_HISTORY_RETENTION.
type History struct {
	// Retention drops versions older than this. Zero keeps every age.
	Retention time.Duration `koanf:"retention"`
	// MaxVersions keeps only the newest N revisions of one workflow. Zero keeps
	// every count.
	MaxVersions int `koanf:"max_versions"`
}

// SQLNodes bounds what a workflow document may ask a database node for.
//
// This is not KilasFlow's own database — that is Database above. It bounds the
// SQL nodes, whose limits come from parameters a tenant authors and which may
// be expressions over an incoming item: without a ceiling, `{{ $json.maxRows }}`
// behind a webhook lets the caller choose how much of the customer's database
// this server buffers into memory.
//
// The section name is one word for the same reason Outbound and Binary are:
// envKeyToPath treats the first underscore as the section separator, so a
// two-word section could never be set from the environment.
type SQLNodes struct {
	// MaxRows is the largest row buffer a node may ask for. Zero falls back to
	// sqlnode's own ceiling rather than meaning unbounded.
	MaxRows int `koanf:"max_rows"`
	// MaxStatementTimeout is the longest a single statement may run.
	MaxStatementTimeout time.Duration `koanf:"max_statement_timeout"`
}

// Credential bounds the credential test endpoint.
//
// The endpoint opens an outbound connection to wherever a stored credential
// points, so it is a probe anyone who can reach the API can aim. Its own
// deadline, rather than the server's, is what stops a target that accepts a
// connection and then never answers from holding a request — and a worker —
// open for the whole of the server's much longer window.
//
// The section name is one word for the same reason Outbound, SQL and Binary
// are: envKeyToPath treats the first underscore as the section separator.
type Credential struct {
	// TestTimeout bounds one credential test end to end.
	TestTimeout time.Duration `koanf:"test_timeout"`
}

// Binary configures where item payloads are stored.
//
// The section name is one word deliberately: the environment override maps the
// first underscore in KILASFLOW_* to the section separator, so a two-word
// section could never be set from the environment.
type Binary struct {
	// Root is the directory payloads are written under. Empty disables binary
	// storage, and a node that needs it then fails with a clear message
	// instead of dropping an attachment on the floor.
	Root string `koanf:"root"`
	// MaxBytes bounds one payload. It is enforced while reading, so an
	// oversized response is refused rather than truncated.
	MaxBytes int64 `koanf:"max_bytes"`
}

// Log configures structured logging.
type Log struct {
	// Level is one of debug, info, warn, error.
	Level string `koanf:"level"`
	// Format is "text" or "json".
	Format string `koanf:"format"`
}

// Default returns the configuration used when nothing is overridden.
func Default() Config {
	return Config{
		Server: Server{
			Host:              "0.0.0.0",
			Port:              8080,
			ReadHeaderTimeout: 10 * time.Second,
			ShutdownTimeout:   15 * time.Second,
		},
		Database: Database{
			Driver:       "sqlite",
			DSN:          "./data/kilasflow.db",
			MaxOpenConns: 1,
			MaxIdleConns: 1,
		},
		Security: Security{
			EncryptionKeyEnv: "KILASFLOW_ENCRYPTION_KEY",
		},
		Auth: Auth{
			Enabled:       false,
			SigningKeyEnv: "KILASFLOW_AUTH_SIGNING_KEY",
			SessionTTL:    12 * time.Hour,
			// Spelled out rather than imported from the repository package,
			// which would point configuration at persistence. It matches
			// repository.DefaultTenantID so that a fresh install and an
			// upgraded one bootstrap into the same tenant, and the rows an
			// upgraded install already has stay reachable.
			BootstrapTenant:      "default",
			BootstrapPasswordEnv: "KILASFLOW_BOOTSTRAP_PASSWORD",
		},
		Outbound: OutboundHTTP{
			AllowPrivateNetworks: false,
			MaxRedirects:         5,
			MaxResponseBytes:     8 << 20,
			Timeout:              30 * time.Second,
		},
		Webhook: Webhook{
			MaxBodyBytes:    1 << 20,
			ResponseTimeout: 30 * time.Second,
		},
		Embed: Embed{
			SigningKeyEnv: "KILASFLOW_EMBED_SIGNING_KEY",
			SessionTTL:    15 * time.Minute,
		},
		Branding: Branding{
			Name:      "KilasFlow",
			PoweredBy: true,
		},
		Execution: Execution{
			MaxConcurrent:  10,
			DefaultTimeout: 60 * time.Second,
		},
		History: History{
			// Keep everything. Deleting a customer's history is not a default
			// anyone should get by not reading the configuration reference.
			Retention:   0,
			MaxVersions: 0,
		},
		SQL: SQLNodes{
			// Both sit well above the node defaults (10,000 rows, 30 seconds):
			// the ceiling exists to stop a document asking for something
			// absurd, not to second-guess an author who knows their own data.
			// Kept equal to sqlnode.DefaultCeiling, which a test pins.
			MaxRows:             50_000,
			MaxStatementTimeout: 5 * time.Minute,
		},
		Credential: Credential{
			// Short on purpose. A person is watching this one: a probe that
			// takes half a minute to say "unreachable" has already been given
			// up on.
			TestTimeout: 10 * time.Second,
		},
		Binary: Binary{
			// Empty root disables binary storage rather than defaulting to
			// somewhere surprising: a product that silently starts writing
			// multi-megabyte media into an unexpected directory is worse than
			// one that says it is not configured.
			Root:     "",
			MaxBytes: 16 << 20,
		},
		Log: Log{
			Level:  "info",
			Format: "text",
		},
	}
}

// Load builds a Config from defaults, then the YAML file at path if it exists,
// then KILASFLOW_* environment variables.
//
// A missing config file is not an error: kilasflow is meant to run with no
// configuration at all.
func Load(path string) (Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			// Only a genuine read/parse failure is fatal; absence is normal.
			if !isNotExist(err) {
				return Config{}, fmt.Errorf("load %s: %w", path, err)
			}
		}
	}

	if err := k.Load(env.Provider(EnvPrefix, ".", envKeyToPath), nil); err != nil {
		return Config{}, fmt.Errorf("load environment: %w", err)
	}

	var cfg Config
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate rejects configurations that cannot produce a working server.
func (c Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port %d out of range", c.Server.Port)
	}

	switch c.Database.Driver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("database.driver %q must be sqlite or postgres", c.Database.Driver)
	}

	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}

	// Caught here rather than at the first login, because an instance that
	// starts with authentication "on" and no key to sign with would answer
	// every request with 401 and look like a broken deployment.
	if c.Auth.Enabled && c.Auth.SigningKeyEnv == "" {
		return fmt.Errorf("auth.signing_key_env is required when auth.enabled is true")
	}

	// The grammar is safehttp's rather than a second copy of it here. Two
	// copies drift, and the way this one would drift is an operator being told
	// their exemption is well formed by a checker that is not the one deciding
	// whether to honour it.
	for index, entry := range c.Outbound.AllowedPrivateEndpoints {
		if err := safehttp.CheckPrivateEndpoint(entry); err != nil {
			return fmt.Errorf("outbound.allowed_private_endpoints[%d]: %w", index, err)
		}
	}

	return nil
}

// envKeyToPath maps an environment variable name to a koanf path.
//
// The config tree is two levels deep and its leaf keys themselves contain
// underscores, so only the first underscore is a section separator:
//
//	KILASFLOW_SERVER_PORT                 -> server.port
//	KILASFLOW_SERVER_READ_HEADER_TIMEOUT  -> server.read_header_timeout
//	KILASFLOW_DATABASE_MAX_OPEN_CONNS     -> database.max_open_conns
//
// Naively replacing every underscore would yield server.read.header.timeout,
// which matches no field and would be silently ignored.
func envKeyToPath(key string) string {
	key = strings.ToLower(strings.TrimPrefix(key, EnvPrefix))

	section, leaf, found := strings.Cut(key, "_")
	if !found {
		return key
	}

	return section + "." + leaf
}

func isNotExist(err error) bool {
	return strings.Contains(err.Error(), "no such file or directory") ||
		strings.Contains(err.Error(), "cannot find the file")
}
