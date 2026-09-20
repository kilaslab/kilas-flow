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
//
// Every section below is one word long, and that is a load-bearing rule, not a
// style: envKeyToPath treats the first underscore after KILASFLOW_ as the
// section separator, so a section named `outbound_http` or `workflow_history`
// could never be reached by an environment override. Name any new section a
// single word or KILASFLOW_* overrides for it will silently do nothing.
//
// Precedence is defaults, then the YAML file, then KILASFLOW_* environment
// variables. The reference page and config.example.yaml are generated from
// these structs by scripts/config-reference.go — edit the comments here and
// regenerate, never the outputs by hand.
type Config struct {
	Server     Server       `koanf:"server"`
	Database   Database     `koanf:"database"`
	Datastore  Datastore    `koanf:"datastore"`
	Security   Security     `koanf:"security"`
	Secrets    Secrets      `koanf:"secrets"`
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
	Packs      Packs        `koanf:"packs"`
	Log        Log          `koanf:"log"`
}

// Server holds HTTP listener settings.
type Server struct {
	// Host is the interface to bind, e.g. "0.0.0.0" or "127.0.0.1".
	// Env: KILASFLOW_SERVER_HOST. Default: "0.0.0.0".
	Host string `koanf:"host"`
	// Port is the TCP port to listen on. Must be 1-65535.
	// Env: KILASFLOW_SERVER_PORT. Default: 8080.
	Port int `koanf:"port"`
	// ReadHeaderTimeout bounds reading one request's headers.
	// Env: KILASFLOW_SERVER_READ_HEADER_TIMEOUT. Default: 10s.
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	// ShutdownTimeout bounds graceful drain on SIGINT/SIGTERM.
	// Env: KILASFLOW_SERVER_SHUTDOWN_TIMEOUT. Default: 15s.
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`

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
	// Driver selects the backend: "sqlite" (default, zero infrastructure) or
	// "postgres". Anything else is refused at boot.
	// Env: KILASFLOW_DATABASE_DRIVER. Default: "sqlite".
	Driver string `koanf:"driver"`
	// DSN is the connection string. A SQLite path (a file that is created with
	// its parent directories if missing) or a PostgreSQL URL.
	// Required: boot fails when it is empty.
	// Env: KILASFLOW_DATABASE_DSN. Default: "./data/kilasflow.db".
	DSN string `koanf:"dsn"`

	// TablePrefix namespaces every KilasFlow table, index and constraint for
	// a database shared with another application: with "kflow_" the install
	// owns kflow_workflows instead of workflows, and coexists with a host
	// schema that already has its own workflows table and an index literally
	// named idx_workflows_tenant_updated.
	//
	// Empty (the default) leaves every identifier exactly as it is today, so
	// an existing install upgrades untouched. A non-empty prefix must be
	// lowercase letters, digits and underscores ending in an underscore, and
	// no longer than MaxTablePrefixLength so every identifier stays within
	// PostgreSQL's 63-byte limit. It is fixed for the life of an install:
	// starting with a different prefix than the database already holds
	// refuses to boot rather than creating a second empty schema alongside
	// the populated one. A prefix is a naming convention, not access
	// control: anything holding this connection can still read every
	// prefixed table.
	// Env: KILASFLOW_DATABASE_TABLE_PREFIX. Default: "".
	TablePrefix string `koanf:"table_prefix"`

	// MaxOpenConns and MaxIdleConns size the connection pool. Zero on either
	// means "work one out", which PoolSize does from the driver and the
	// configured execution concurrency — see it for the sizes and for why they
	// cannot be constants in Default().
	MaxOpenConns int `koanf:"max_open_conns"`
	// MaxIdleConns caps idle connections kept warm. Zero follows MaxOpenConns:
	// an idle bound lower than open churns a TLS handshake and a backend fork
	// per query exactly when the server is busiest.
	MaxIdleConns int `koanf:"max_idle_conns"`
}

// Datastore bounds the data tables a tenant may build. A datastore is a real
// table created by runtime DDL inside the operator's own database, so an
// unbounded one hands a host application's end users the ability to grow
// unbounded tables inside a production database. Every bound refuses the
// write and evicts nothing.
//
// The section name is one word for the same reason Outbound, SQL and Binary
// are: envKeyToPath treats the first underscore as the section separator, so
// a two-word section could never be set from the environment.
//
// The defaults repeat datastore.DefaultLimits rather than importing them:
// the datastore package imports this one for MaxTablePrefixLength, so the
// import would be a cycle. A test in the datastore package pins the two
// together.
type Datastore struct {
	// MaxDatastoresPerTenant caps how many data tables one tenant may own.
	// Env: KILASFLOW_DATASTORE_MAX_DATASTORES_PER_TENANT. Default: 100.
	MaxDatastoresPerTenant int `koanf:"max_datastores_per_tenant"`
	// MaxColumnsPerDatastore caps the user columns of one data table.
	// Env: KILASFLOW_DATASTORE_MAX_COLUMNS_PER_DATASTORE. Default: 100.
	MaxColumnsPerDatastore int `koanf:"max_columns_per_datastore"`
	// MaxRowsPerDatastore caps the rows of one data table.
	// Env: KILASFLOW_DATASTORE_MAX_ROWS_PER_DATASTORE. Default: 100000.
	MaxRowsPerDatastore int `koanf:"max_rows_per_datastore"`
	// MaxValueBytes caps one unbounded value: a string or raw bytes. Numbers,
	// booleans and dates bind fixed-width and are exempt.
	// Env: KILASFLOW_DATASTORE_MAX_VALUE_BYTES. Default: 1048576.
	MaxValueBytes int `koanf:"max_value_bytes"`
}

// MaxTablePrefixLength caps Database.TablePrefix so that the longest table,
// index or constraint identifier the migrations create still fits
// PostgreSQL's 63-byte limit once prefixed. Identifiers are ASCII by
// validation, so characters and bytes coincide. The budget also leaves room
// for the per-datastore tables a later milestone creates at run time, whose
// names this ticket never sees: lengthen this only after re-measuring every
// identifier the migrations define.
const MaxTablePrefixLength = 16

// Pool sizing bounds, used when the operator has set neither key.
const (
	// poolHeadroom is reserved for everything that is not an execution worker:
	// the scheduler, the history sweeper, the execution pruner, the webhook
	// receiver and every API request. Without it a burst of runs starves the
	// endpoint the operator is watching those runs from.
	poolHeadroom = 5
	// minimumPoolSize keeps a single-worker install from serialising its whole
	// API behind that one execution.
	minimumPoolSize = 4
	// maximumDerivedPoolSize stays well under PostgreSQL's stock
	// max_connections of 100, which several KilasFlow processes and whatever
	// else uses that server all draw on. A derived default that cannot connect
	// is worse than one that is a little small, so a deployment that really
	// wants more sets max_open_conns itself.
	maximumDerivedPoolSize = 50
)

// PoolSize returns the open and idle connection counts for this database,
// deriving whichever the operator left unset from maxConcurrent.
//
// SQLite derives to one because database.Open pins it there regardless: the
// single-writer pin and the WAL pragma set are a pair, and a configuration
// reporting fifteen while the pool holds one sends an operator hunting the
// wrong thing. Everything else follows the worker count, because "PostgreSQL
// unlocks concurrency" is only true if the workers have connections to run on.
func (d Database) PoolSize(maxConcurrent int) (open, idle int) {
	open, idle = d.MaxOpenConns, d.MaxIdleConns
	if open <= 0 {
		if d.Driver == "sqlite" {
			open = 1
		} else {
			open = min(max(maxConcurrent+poolHeadroom, minimumPoolSize), maximumDerivedPoolSize)
		}
	}
	if idle <= 0 {
		// Idle matches open rather than sitting at some fraction of it. A pool
		// whose idle bound is lower spends a sustained burst opening a
		// connection, using it once and destroying it, which costs a TLS
		// handshake and a backend fork per query at exactly the moment the
		// server is busiest. ConnMaxLifetime still recycles them.
		idle = open
	}
	return open, idle
}

// validateTablePrefix rejects a table prefix that would build identifiers
// PostgreSQL refuses or silently truncates. Truncation is the hazard, not
// refusal: past byte 63 two names that differ only at the tail collapse into
// one, and CREATE INDEX IF NOT EXISTS then skips the second without complaint
// while the constraint it was meant to enforce never exists.
func validateTablePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if len(prefix) > MaxTablePrefixLength {
		return fmt.Errorf("database.table_prefix %q is %d characters, longer than the maximum %d", prefix, len(prefix), MaxTablePrefixLength)
	}
	if !strings.HasSuffix(prefix, "_") {
		return fmt.Errorf("database.table_prefix %q must end in an underscore", prefix)
	}
	for _, character := range prefix {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '_':
		default:
			return fmt.Errorf("database.table_prefix %q must hold only lowercase letters, digits and underscores", prefix)
		}
	}
	return nil
}

// Security holds secret-material settings.
type Security struct {
	// EncryptionKeyEnv names the environment variable holding the AES-256-GCM
	// master key used to encrypt stored credentials. The key itself is never
	// read from the config file.
	//
	// Optional at boot: when the named variable holds no key the server still
	// starts and still runs workflows, but credential storage is disabled —
	// reads and writes report unconfigured. Generate one before storing
	// anything (openssl rand -base64 32); there is no re-encryption pass, so
	// changing the key later makes every credential already stored
	// undecryptable.
	// Env: KILASFLOW_SECURITY_ENCRYPTION_KEY_ENV.
	// Default: "KILASFLOW_ENCRYPTION_KEY".
	EncryptionKeyEnv string `koanf:"encryption_key_env"`
}

// Secrets sources the credential master key from an external secrets manager
// instead of the process environment.
//
// The koanf section is a single word for the same reason Outbound is:
// envKeyToPath treats the first underscore as the section separator, so a
// section named `secret_manager` could never be reached by an environment
// override.
//
// All empty (the default) leaves the environment path exactly as before: the
// manager is only consulted when an address is set, and a set address with
// no token variable or no master-key reference refuses to boot rather than
// silently running with credential storage disabled.
type Secrets struct {
	// ManagerAddr is the secrets manager address, e.g. Vault's
	// "https://vault.internal:8200". Empty disables the manager path.
	// Env: KILASFLOW_SECRETS_MANAGER_ADDR. Default: "".
	ManagerAddr string `koanf:"manager_addr"`
	// ManagerTokenEnv names the environment variable holding the manager
	// token. The token itself is never read from the config file, for the
	// same reason the credential master key is not: a file is copied, an
	// environment is not.
	// Env: KILASFLOW_SECRETS_MANAGER_TOKEN_ENV. Default: "".
	ManagerTokenEnv string `koanf:"manager_token_env"`
	// MasterKey is the reference (path) of the credential master key inside
	// the manager, e.g. "prod/master-key". It decodes exactly like the
	// environment path — base64, hex, or raw 32 bytes — so a key can move
	// from one source to the other without being re-encoded.
	// Env: KILASFLOW_SECRETS_MASTER_KEY. Default: "".
	MasterKey string `koanf:"master_key"`
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
	// stream-ticket signing key: exactly 32 bytes, encoded as base64, hex, or
	// raw bytes (`openssl rand -base64 32`). Like the credential and embed keys,
	// it never comes from the config file.
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
	// BootstrapEmail is the address of the first account, created once and only
	// on an installation that has none: a deployment that already has users is
	// never handed another owner by an environment variable somebody forgot to
	// remove.
	BootstrapEmail string `koanf:"bootstrap_email"`
	// BootstrapPasswordEnv names the variable holding the first account's
	// password. The password never comes from the file. Used once, with
	// bootstrap_email and enabled: true on the same first start, or the lock
	// closes before the key is cut.
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
	// AllowPrivateNetworks lifts the private-address guard for every outbound
	// workflow request. Default false: with it off a workflow URL cannot reach
	// loopback, RFC 1918 ranges, or the cloud metadata service, which is what
	// stops a tenant-authored URL probing the network the server runs in.
	// Prefer allowed_private_endpoints for a single loopback dependency.
	// Env: KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORKS. Default: false.
	AllowPrivateNetworks bool `koanf:"allow_private_networks"`
	// AllowedHosts restricts outbound requests to these hosts when non-empty
	// ("example.com", "*.internal.example"). Empty means no host restriction
	// beyond the private-address guard.
	// Env: KILASFLOW_OUTBOUND_ALLOWED_HOSTS. Default: [].
	AllowedHosts []string `koanf:"allowed_hosts"`
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
	AllowedPrivateEndpoints []string `koanf:"allowed_private_endpoints"`
	// MaxRedirects bounds how many redirects one outbound request follows;
	// every hop is re-checked against the policy.
	// Env: KILASFLOW_OUTBOUND_MAX_REDIRECTS. Default: 5.
	MaxRedirects int `koanf:"max_redirects"`
	// MaxResponseBytes bounds how much of one response is read into memory, in
	// bytes. Larger bodies are refused, not truncated.
	// Env: KILASFLOW_OUTBOUND_MAX_RESPONSE_BYTES. Default: 8388608 (8 MiB).
	MaxResponseBytes int64 `koanf:"max_response_bytes"`
	// Timeout bounds one whole outbound request.
	// Env: KILASFLOW_OUTBOUND_TIMEOUT. Default: 30s.
	Timeout time.Duration `koanf:"timeout"`
}

// Webhook bounds one inbound trigger request.
type Webhook struct {
	// MaxBodyBytes is the largest inbound delivery accepted, in bytes.
	// Env: KILASFLOW_WEBHOOK_MAX_BODY_BYTES. Default: 1048576 (1 MiB).
	MaxBodyBytes int64 `koanf:"max_body_bytes"`
	// ResponseTimeout bounds one trigger delivery end to end.
	// Env: KILASFLOW_WEBHOOK_RESPONSE_TIMEOUT. Default: 30s.
	ResponseTimeout time.Duration `koanf:"response_timeout"`
}

// Embed configures the iframe editor surface.
//
// The allowlist is empty by default, which means embedding is off: an operator
// opts in per origin rather than discovering their editor is frameable
// anywhere.
type Embed struct {
	// SigningKeyEnv names the environment variable holding the token signing
	// key: exactly 32 bytes, encoded as base64, hex, or raw bytes. Like the
	// credential key, it never comes from the config file.
	//
	// Optional at boot: with no key the session endpoints report themselves
	// unconfigured and every embed token is refused, with a warning at boot.
	// Env: KILASFLOW_EMBED_SIGNING_KEY_ENV. Default: "KILASFLOW_EMBED_SIGNING_KEY".
	//
	// Generate one with `openssl rand -base64 32`. A longer key is refused at
	// boot rather than truncated: the error names the length, and reading it
	// after a truncated key silently shipped would be much worse.
	SigningKeyEnv string `koanf:"signing_key_env"`
	// AllowedOrigins is the per-origin allowlist for the iframe editor. Empty
	// fails closed: even with a key set, no page may host the editor until its
	// origin is listed here.
	// Env: KILASFLOW_EMBED_ALLOWED_ORIGINS. Default: [].
	AllowedOrigins []string `koanf:"allowed_origins"`
	// SessionTTL is how long one embed session token lives. Capped at 30
	// minutes: a token travels through a host page and sits in a browser, so a
	// leaked one stays useful only briefly.
	// Env: KILASFLOW_EMBED_SESSION_TTL. Default: 15m.
	SessionTTL time.Duration `koanf:"session_ttl"`
}

// Branding drives white-label display options.
type Branding struct {
	// Name is the product name shown in the dashboard.
	// Env: KILASFLOW_BRANDING_NAME. Default: "KilasFlow".
	Name string `koanf:"name"`
	// Logo is the logo URL shown in the dashboard. Empty hides it.
	// Env: KILASFLOW_BRANDING_LOGO. Default: "".
	Logo string `koanf:"logo"`
	// Favicon is the favicon URL. Empty uses the built-in one.
	// Env: KILASFLOW_BRANDING_FAVICON. Default: "".
	Favicon string `koanf:"favicon"`
	// PoweredBy toggles the "Powered by KilasFlow" mark.
	// Env: KILASFLOW_BRANDING_POWERED_BY. Default: true.
	PoweredBy bool `koanf:"powered_by"`
}

// Execution bounds workflow runs.
type Execution struct {
	// MaxConcurrent bounds how many workflow runs execute at once. The
	// PostgreSQL pool derives from it (workers plus headroom), so raising it
	// raises the pool with it; SQLite always runs on one connection.
	// Env: KILASFLOW_EXECUTION_MAX_CONCURRENT. Default: 10.
	MaxConcurrent int `koanf:"max_concurrent"`
	// DefaultTimeout bounds one workflow run end to end.
	// Env: KILASFLOW_EXECUTION_DEFAULT_TIMEOUT. Default: 60s.
	DefaultTimeout time.Duration `koanf:"default_timeout"`
	// Retention deletes an execution once it has been finished for longer than
	// this, along with its node runs and its stored binary payloads.
	//
	// Zero keeps every execution, for the same reason History.Retention does:
	// an operator who has never configured retention must not discover that
	// installing a new build deleted the run history they were about to debug.
	// Turning it on is how an installation stops growing without bound, which
	// until this key existed it had no way to do at all.
	Retention time.Duration `koanf:"retention"`
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

// Packs configures directory-loaded node packs: the install path that needs
// no rebuild. Each immediate subdirectory of Dir is one pack, carrying a
// pack.json manifest beside the pack.sha256 checksum the operator approved.
//
// The section name is one word for the same reason Binary is: envKeyToPath
// treats the first underscore as the section separator, so a two-word
// section could never be set from the environment.
type Packs struct {
	// Dir is the directory packs are loaded from at startup, before the
	// registry is shared. Empty disables directory loading, and an absent or
	// empty directory is a normal silent condition, so the default deployment
	// is unchanged. A pack that fails to load refuses the whole boot, naming
	// the pack and the reason.
	// Env: KILASFLOW_PACKS_DIR. Default: "".
	Dir string `koanf:"dir"`
}

// Log configures structured logging.
type Log struct {
	// Level is one of debug, info, warn or error. Unknown values fall back to
	// info rather than refusing to start.
	// Env: KILASFLOW_LOG_LEVEL. Default: "info".
	Level string `koanf:"level"`
	// Format is "text" for local reading or "json" for production log
	// pipelines.
	// Env: KILASFLOW_LOG_FORMAT. Default: "text".
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
			Driver: "sqlite",
			DSN:    "./data/kilasflow.db",
			// Left at zero so PoolSize works them out after every layer has
			// merged. A number here could not follow execution.max_concurrent,
			// because koanf cannot tell a default of 1 apart from a file that
			// says 1 — which is how a PostgreSQL install came to run ten
			// execution workers, a scheduler, the webhook handler and every API
			// request through a single connection while config.example.yaml
			// advertised ten.
			MaxOpenConns: 0,
			MaxIdleConns: 0,
		},
		Datastore: Datastore{
			// Kept equal to datastore.DefaultLimits, which a test pins.
			MaxDatastoresPerTenant: 100,
			MaxColumnsPerDatastore: 100,
			MaxRowsPerDatastore:    100_000,
			MaxValueBytes:          1 << 20,
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
			// Keep every execution. See the field.
			Retention: 0,
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
		Packs: Packs{
			// Empty disables directory loading: the default deployment runs
			// the embedded packs only, exactly as before.
			Dir: "",
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

	// After the merge rather than before it, so an operator who raises only
	// execution.max_concurrent sees the pool follow without also having to
	// know that it exists.
	cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns = cfg.Database.PoolSize(cfg.Execution.MaxConcurrent)

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

	if err := validateTablePrefix(c.Database.TablePrefix); err != nil {
		return err
	}

	// A negative pool size means "unlimited" to database/sql, which is not
	// something anyone asks for on purpose and is indistinguishable from a
	// typo until the database refuses the connection nobody budgeted for.
	if c.Database.MaxOpenConns < 0 || c.Database.MaxIdleConns < 0 {
		return fmt.Errorf("database.max_open_conns and database.max_idle_conns must not be negative")
	}

	// A negative retention puts the prune cutoff in the future, and every
	// execution ever recorded is older than the future.
	if c.Execution.Retention < 0 {
		return fmt.Errorf("execution.retention %s must not be negative", c.Execution.Retention)
	}

	// A zero or negative datastore bound refuses every write it gates, which
	// is an outage shaped like a configuration, so it is an error here
	// rather than a quiet refusal later.
	if c.Datastore.MaxDatastoresPerTenant <= 0 {
		return fmt.Errorf("datastore.max_datastores_per_tenant %d must be positive", c.Datastore.MaxDatastoresPerTenant)
	}
	if c.Datastore.MaxColumnsPerDatastore <= 0 {
		return fmt.Errorf("datastore.max_columns_per_datastore %d must be positive", c.Datastore.MaxColumnsPerDatastore)
	}
	if c.Datastore.MaxRowsPerDatastore <= 0 {
		return fmt.Errorf("datastore.max_rows_per_datastore %d must be positive", c.Datastore.MaxRowsPerDatastore)
	}
	if c.Datastore.MaxValueBytes <= 0 {
		return fmt.Errorf("datastore.max_value_bytes %d must be positive", c.Datastore.MaxValueBytes)
	}

	// Caught here rather than at the first login, because an instance that
	// starts with authentication "on" and no key to sign with would answer
	// every request with 401 and look like a broken deployment.
	if c.Auth.Enabled && c.Auth.SigningKeyEnv == "" {
		return fmt.Errorf("auth.signing_key_env is required when auth.enabled is true")
	}

	// A half-configured manager is worse than none: an address with no token
	// or no master-key reference would fail every fetch at run time, and an
	// addressless token would silently keep the environment path while the
	// operator believes the manager is in charge. All three or none.
	if c.Secrets.ManagerAddr == "" || c.Secrets.ManagerTokenEnv == "" || c.Secrets.MasterKey == "" {
		if c.Secrets.ManagerAddr != "" || c.Secrets.ManagerTokenEnv != "" || c.Secrets.MasterKey != "" {
			return fmt.Errorf("secrets.manager_addr, secrets.manager_token_env and secrets.master_key must be set together")
		}
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
