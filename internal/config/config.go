// Package config loads kilasflow configuration from defaults, an optional YAML
// file, and KILASFLOW_* environment variables, in that order of increasing
// precedence.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/kilaslab/kilas-flow/internal/datetime"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
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
	Server      Server       `koanf:"server"`
	Database    Database     `koanf:"database"`
	Datastore   Datastore    `koanf:"datastore"`
	Security    Security     `koanf:"security"`
	Secrets     Secrets      `koanf:"secrets"`
	Auth        Auth         `koanf:"auth"`
	Outbound    OutboundHTTP `koanf:"outbound"`
	Webhook     Webhook      `koanf:"webhook"`
	Embed       Embed        `koanf:"embed"`
	Branding    Branding     `koanf:"branding"`
	Execution   Execution    `koanf:"execution"`
	History     History      `koanf:"history"`
	Idempotency Idempotency  `koanf:"idempotency"`
	SQL         SQLNodes     `koanf:"sql"`
	Credential  Credential   `koanf:"credential"`
	Binary      Binary       `koanf:"binary"`
	Packs       Packs        `koanf:"packs"`
	Google      Google       `koanf:"google"`
	Log         Log          `koanf:"log"`
	Sidecar     Sidecar      `koanf:"sidecar"`
	Code        Code         `koanf:"code"`
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

// Google holds the optional platform Google Cloud OAuth client. Tenants can
// still paste their own client id and secret on a credential; empty fields
// here mean Connect only works when the credential itself carries a client.
type Google struct {
	// ClientID is the platform Google Cloud OAuth client id. Empty means every
	// tenant must supply their own on the credential.
	// Env: KILASFLOW_GOOGLE_CLIENT_ID. Default: "".
	ClientID string `koanf:"client_id"`
	// ClientSecretEnv names the environment variable holding the platform
	// Google Cloud OAuth client secret. The secret itself is never read from
	// the config file.
	// Env: KILASFLOW_GOOGLE_CLIENT_SECRET_ENV.
	// Default: "KILASFLOW_GOOGLE_CLIENT_SECRET".
	ClientSecretEnv string `koanf:"client_secret_env"`
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
	// OperatorKeyEnv names the variable holding the operator's API key: the
	// one credential that may provision customers. It is registered at boot
	// rather than minted, because the value exists in the environment before
	// the process starts. The key itself never comes from the file.
	// Env: KILASFLOW_AUTH_OPERATOR_KEY_ENV. Default: "KILASFLOW_AUTH_OPERATOR_KEY".
	OperatorKeyEnv string `koanf:"operator_key_env"`
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
	// RequireAuth refuses a delivery to any webhook trigger whose
	// authentication mode is none. Off by default; on is a deployment-wide
	// posture change. A trigger that verifies its own senders counts as
	// authenticated — Telegram's secret header, a pack trigger holding an HMAC
	// secret — while an IP allow-list alone does not. The refusal is a 403 that
	// names the workflow and the fix, and hosted form pages face the same gate
	// (a CORS preflight does not, because it cannot carry a credential).
	// Env: KILASFLOW_WEBHOOK_REQUIRE_AUTH. Default: false.
	RequireAuth bool `koanf:"require_auth"`
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
	// SessionTTL is how long an embed session token lives when the host does
	// not ask for a lifetime (ttlSeconds when it mints the session). It is a
	// default, not a ceiling: a host may ask for longer, and no token ever
	// outlives 30 minutes whatever is asked or configured here. A value below
	// one second or above 30 minutes is refused at startup rather than clamped,
	// so the file never says one lifetime while the server enforces another.
	// Write a unit (15m): a bare number in YAML is read as nanoseconds. Tokens
	// travel through a host page and sit in a browser, so a leaked one should
	// stay useful only briefly.
	// Env: KILASFLOW_EMBED_SESSION_TTL. Default: 15m.
	SessionTTL time.Duration `koanf:"session_ttl"`
}

// Branding is the deployment-wide default for the white-label values an
// embedded editor session carries.
//
// A host that passes its own branding when it mints a session overrides these
// field by field; a host that passes none gets them, in the mint response and in
// the session token. They reach only the embedded editor, the surface a host's
// end users see: the operator dashboard keeps its own name, logo and icon.
type Branding struct {
	// Name is the product name drawn in the header of an embedded editor whose
	// session names none: letters, digits, spaces and simple punctuation, up to
	// 60 characters. Empty draws no name.
	// Env: KILASFLOW_BRANDING_NAME. Default: "".
	Name string `koanf:"name"`
	// Logo is the logo URL drawn in that header when a session gives none: an
	// absolute https URL. Empty draws no logo; with neither a name nor a logo
	// the editor shows no header at all.
	// Env: KILASFLOW_BRANDING_LOGO. Default: "".
	Logo string `koanf:"logo"`
}

// Execution bounds workflow runs.
type Execution struct {
	// MaxConcurrent bounds how many workflow runs execute at once. The
	// PostgreSQL pool derives from it (workers plus headroom), so raising it
	// raises the pool with it; SQLite always runs on one connection.
	// Env: KILASFLOW_EXECUTION_MAX_CONCURRENT. Default: 10.
	MaxConcurrent int `koanf:"max_concurrent"`
	// DefaultTimeout bounds one workflow run end to end, for a workflow that
	// names no executionTimeout of its own. Two minutes rather than one: an
	// AI agent on a local or reasoning model routinely spends longer than a
	// minute before it answers, and a stock budget that ends such runs makes
	// the default install look broken.
	// Env: KILASFLOW_EXECUTION_DEFAULT_TIMEOUT. Default: 2m.
	DefaultTimeout time.Duration `koanf:"default_timeout"`
	// Retention deletes an execution once it has been finished for longer than
	// this, along with its node runs and its stored binary payloads.
	//
	// Zero keeps every execution, for the same reason History.Retention does:
	// an operator who has never configured retention must not discover that
	// installing a new build deleted the run history they were about to debug.
	// Turning it on is how an installation stops growing without bound, which
	// until this key existed it had no way to do at all.
	// Env: KILASFLOW_EXECUTION_RETENTION. Default: 0 (keep everything).
	Retention time.Duration `koanf:"retention"`
	// WaitSweepInterval is how often expired durable waits are settled when no
	// exact timer is armed for them. The wait suspender arms an exact timer
	// in-process for a wait it is holding, so this is the restart-recovery
	// floor: a wait whose process died is settled within one interval of the
	// next boot.
	// Env: KILASFLOW_EXECUTION_WAIT_SWEEP_INTERVAL. Default: 10s.
	WaitSweepInterval time.Duration `koanf:"wait_sweep_interval"`
	// MaxTimeout is the ceiling a workflow's own settings.executionTimeout is
	// clamped to, in the spirit of n8n's EXECUTIONS_TIMEOUT_MAX. Zero means no
	// ceiling beyond the workflow's own value; DefaultTimeout stays the budget
	// for a workflow that names none.
	// Env: KILASFLOW_EXECUTION_MAX_TIMEOUT. Default: 1h.
	MaxTimeout time.Duration `koanf:"max_timeout"`
	// DefaultTimezone is the instance's IANA zone, used by a workflow whose
	// settings.timezone is absent or is n8n's literal "DEFAULT". Empty means
	// UTC, which is what a server with no configured zone has always used.
	// Env: KILASFLOW_EXECUTION_DEFAULT_TIMEZONE. Default: "".
	DefaultTimezone string `koanf:"default_timezone"`
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

// Idempotency bounds how long a request's Idempotency-Key is remembered.
//
// The section name is one word for the same reason History, Outbound and
// Binary are: envKeyToPath treats the first underscore as the section
// separator, so a two-word section could never be set from the environment.
type Idempotency struct {
	// Retention is how long a completed request's key is remembered. Within
	// it, a retry carrying the same key from the same tenant is answered with
	// the first request's outcome and repeats no side effect; a key past its
	// retention behaves as never seen and the request runs again. Bounded to
	// between 1m and 720h.
	//
	// If you set execution.retention (default 0, keep everything, so there is
	// nothing to align with by default), keep this at or below it: a replay of
	// a run whose execution has been pruned returns an id that no longer
	// resolves.
	// Env: KILASFLOW_IDEMPOTENCY_RETENTION. Default: 24h.
	Retention time.Duration `koanf:"retention"`
}

// The bounds idempotency.retention is validated against. A retention under a
// minute would expire a key before a retrying client could plausibly use it,
// and one over thirty days is a ledger rather than a retry guard.
const (
	MinIdempotencyRetention = time.Minute
	MaxIdempotencyRetention = 30 * 24 * time.Hour
)

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
	// Root is the directory payloads are written under.
	//
	// It has a default rather than being off, because it used to be off: a
	// stock install and the container image ran with no binary storage at all,
	// and the HTTP Request node's default autodetect then decoded a downloaded
	// file as text and reported success — a silent corruption that only showed
	// up downstream. A node that needs storage now finds it, and the directory
	// sits beside the default SQLite database, so it is inside whatever volume
	// the operator already mounted.
	//
	// Setting it to the empty string still disables storage deliberately; the
	// server then warns at boot, and every node that needs a payload names
	// this key and its environment variable instead of failing with a message
	// nobody can act on.
	// Env: KILASFLOW_BINARY_ROOT. Default: "./data/binary".
	Root string `koanf:"root"`
	// MaxBytes bounds one payload. It is enforced while reading, so an
	// oversized response is refused rather than truncated.
	// Env: KILASFLOW_BINARY_MAX_BYTES. Default: 16777216 (16 MiB).
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
	// VisibleTo overrides which tenants each node type is visible to, as
	// `"<node type>=<tenant id>"` list entries. The list REPLACES what a pack's
	// manifest declared for that type, so an operator can widen or narrow a
	// scope without editing pinned manifest bytes; there is no wildcard.
	//
	// It applies to any type a pack registers, embedded or from Dir: one entry
	// per type, so a WAHA install needs one for pack.waha and one for
	// pack.wahaTrigger. Types with no entry stay visible to every tenant, and a
	// built-in can never be scoped. An entry naming a type that is not
	// registered, or a tenant ID that is not a tenant ID, refuses the boot.
	//
	// Give every process the same value. The API role hides an invisible node,
	// but the worker is the authority: a worker started without this key accepts
	// and runs what the API would have refused.
	// Env: KILASFLOW_PACKS_VISIBLE_TO. Default: [].
	VisibleTo []string `koanf:"visible_to"`
}

// Sidecar configures the opt-in JavaScript sidecar that runs programmatic
// community nodes: a real execute() a declarative pack cannot replicate.
//
// It is off by default, and off is byte-for-byte the deployment this section
// did not change: no Node process, no runtime directory, and Node is never
// looked for on PATH.
//
// The section name is one word for the same reason Binary is: envKeyToPath
// treats the first underscore as the section separator, so a two-word section
// could never be set from the environment.
type Sidecar struct {
	// Enabled turns the sidecar on. When it is on, every process that boots
	// with it on needs a Node binary and the packages below at startup,
	// because the node catalogue is read from them at boot.
	// Env: KILASFLOW_SIDECAR_ENABLED. Default: false.
	Enabled bool `koanf:"enabled"`
	// NodePath is the operator-installed Node 24 LTS binary. Empty searches
	// PATH, which is what a development machine wants and what a container
	// without one should set explicitly.
	// Env: KILASFLOW_SIDECAR_NODE_PATH. Default: "" meaning node from PATH.
	NodePath string `koanf:"node_path"`
	// PackagesDir holds the installed packages: either an npm --prefix
	// directory containing node_modules/, or one directory per flat package.
	// It is required when the sidecar is enabled.
	// Env: KILASFLOW_SIDECAR_PACKAGES_DIR. Default: "".
	PackagesDir string `koanf:"packages_dir"`
	// Packages is the allowlist of package names to load. Transitive
	// dependencies and packages not named here are never loaded by the runner.
	// It must name at least one package when the sidecar is enabled.
	// Env: KILASFLOW_SIDECAR_PACKAGES (comma-separated). Default: empty.
	Packages []string `koanf:"packages"`
	// RuntimeDir holds the extracted runner and one socket directory per
	// process. It must be writable and on a local filesystem.
	// Env: KILASFLOW_SIDECAR_RUNTIME_DIR. Default: "./data/sidecar".
	RuntimeDir string `koanf:"runtime_dir"`
	// Wrapper is an argv prefix the child runs under, for an isolation tool
	// such as a sandbox launcher or setpriv. KilasFlow does not inspect what it
	// isolates, so it is the deployment's own boundary rather than a guarantee
	// this build makes. A wrapper argument containing a comma cannot be set
	// from the environment; use the YAML file for one.
	// Env: KILASFLOW_SIDECAR_WRAPPER (comma-separated). Default: empty.
	Wrapper []string `koanf:"wrapper"`
	// Timeout bounds one node run on a warm process.
	// Env: KILASFLOW_SIDECAR_TIMEOUT. Default: 30s.
	Timeout time.Duration `koanf:"timeout"`
	// SpawnTimeout bounds a cold start: listening, forking, and the child's
	// dial-back.
	// Env: KILASFLOW_SIDECAR_SPAWN_TIMEOUT. Default: 15s.
	SpawnTimeout time.Duration `koanf:"spawn_timeout"`
	// IdleTimeout is how long a tenant's process stays warm between runs
	// before the pool reaps it, and so also how long decrypted credentials can
	// linger in its memory after the last run.
	// Env: KILASFLOW_SIDECAR_IDLE_TIMEOUT. Default: 5m.
	IdleTimeout time.Duration `koanf:"idle_timeout"`
	// MaxHeapMB is the child's JavaScript heap ceiling. It is a heap bound,
	// not a resident-memory one: Buffer and native allocations live outside
	// V8's old space, which is why MaxRSSMB exists as a second bound.
	// Env: KILASFLOW_SIDECAR_MAX_HEAP_MB. Default: 256.
	MaxHeapMB int `koanf:"max_heap_mb"`
	// MaxRSSMB is the resident-set ceiling the host watchdog enforces by
	// polling the child. Zero disables the watchdog; the container limit stays
	// the hard backstop either way.
	// Env: KILASFLOW_SIDECAR_MAX_RSS_MB. Default: 512.
	MaxRSSMB int `koanf:"max_rss_mb"`
	// MaxProcesses caps how many tenant processes the pool holds at once.
	// Env: KILASFLOW_SIDECAR_MAX_PROCESSES. Default: 16.
	MaxProcesses int `koanf:"max_processes"`
	// MaxOutputBytes caps the decoded result payload one run may return.
	// Env: KILASFLOW_SIDECAR_MAX_OUTPUT_BYTES. Default: 4194304 (4 MiB).
	MaxOutputBytes int64 `koanf:"max_output_bytes"`
}

// Code configures the two Code nodes. The Go Code node needs a toolchain and
// a place to keep what it builds. The JavaScript Code node runs on an engine
// linked into the server and needs only its limits.
//
// The section is one word because envKeyToPath treats the first underscore as
// the section separator, so code.cache_dir is reachable as
// KILASFLOW_CODE_CACHE_DIR while a two-word section could never be set from
// the environment at all. The JavaScript keys are flat for the same reason:
// code.javascript_timeout is KILASFLOW_CODE_JAVASCRIPT_TIMEOUT, where a
// nested code.javascript.timeout could not be reached from the environment.
type Code struct {
	// GoBinary is the go command used to compile Code nodes. It is looked up
	// on PATH when it is not an absolute path.
	// Env: KILASFLOW_CODE_GO_BINARY. Default: "go".
	GoBinary string `koanf:"go_binary"`

	// CacheDir holds compiled artifacts, wazero's translations of them and the
	// toolchain's own build cache. It defaults to a directory inside the data
	// volume, so a container deployment persists all three without extra
	// mounts.
	//
	// Empty keeps every cache in memory, which is what the deployment got
	// before this key existed: compilation is repeated after a restart and
	// every build needs the toolchain again.
	// Env: KILASFLOW_CODE_CACHE_DIR. Default: "./data/codecache".
	CacheDir string `koanf:"cache_dir"`

	// CacheMaxBytes bounds the artifact and translation directories together.
	// Zero means unbounded.
	//
	// Translations are evicted before artifacts, because a translation is
	// rebuilt from the artifact it belongs to while an artifact needs a Go
	// toolchain that this deployment may not have. The default is generous for
	// the same reason: a deployment with no toolchain cannot afford to lose
	// the artifacts, so eviction is a last resort rather than housekeeping.
	// Env: KILASFLOW_CODE_CACHE_MAX_BYTES. Default: 2147483648 (2 GiB).
	CacheMaxBytes int64 `koanf:"cache_max_bytes"`

	// JavaScriptEnabled runs JavaScript Code nodes, including the ones
	// imported from n8n, on the engine inside the server. Turned off, the
	// node is greyed out in the editor and every run is refused, naming this
	// key. No Node.js process is involved either way.
	// Env: KILASFLOW_CODE_JAVASCRIPT_ENABLED. Default: true.
	JavaScriptEnabled bool `koanf:"javascript_enabled"`

	// JavaScriptTimeout bounds the user's own program in one JavaScript Code
	// node run. Setting up the engine, loading a library and handling the
	// input and output are not counted. A node's own time limit may lower it,
	// never raise it. At most 5m, because a backtracking regular expression
	// can overrun its limit by up to this long.
	// Env: KILASFLOW_CODE_JAVASCRIPT_TIMEOUT. Default: 10s.
	JavaScriptTimeout time.Duration `koanf:"javascript_timeout"`

	// JavaScriptMaxConcurrent bounds how many JavaScript Code nodes run at
	// once across the server, and so how many worker processes run them;
	// more wait their turn. Zero means one per CPU.
	// Env: KILASFLOW_CODE_JAVASCRIPT_MAX_CONCURRENT. Default: 0.
	JavaScriptMaxConcurrent int `koanf:"javascript_max_concurrent"`

	// JavaScriptHeapCeilingMB is one worker process's live heap, in MiB, at
	// which its JavaScript Code node is stopped with a memory-limit error.
	// Scripts run in workers apart from the server, so a runaway script costs
	// a worker, never the server; on Linux a worker that outgrows this in one
	// step is stopped by an address-space limit of four times it plus 1 GiB,
	// or by the kernel, which is asked to choose workers first. Budget up to
	// javascript_max_concurrent workers of this size. Zero means 1024.
	// Env: KILASFLOW_CODE_JAVASCRIPT_HEAP_CEILING_MB. Default: 0.
	JavaScriptHeapCeilingMB int64 `koanf:"javascript_heap_ceiling_mb"`

	// JavaScriptMaxInputBytes bounds a JavaScript Code node's input, as JSON.
	// Larger input is refused before the engine is involved at all.
	// Env: KILASFLOW_CODE_JAVASCRIPT_MAX_INPUT_BYTES. Default: 33554432 (32 MiB).
	JavaScriptMaxInputBytes int64 `koanf:"javascript_max_input_bytes"`

	// JavaScriptMaxOutputBytes bounds the items one JavaScript Code node run
	// returns, as JSON.
	// Env: KILASFLOW_CODE_JAVASCRIPT_MAX_OUTPUT_BYTES. Default: 16777216 (16 MiB).
	JavaScriptMaxOutputBytes int64 `koanf:"javascript_max_output_bytes"`

	// JavaScriptMaxConsoleBytes bounds the console output one JavaScript Code
	// node run keeps. Output past it is dropped, with a marker saying so.
	// Env: KILASFLOW_CODE_JAVASCRIPT_MAX_CONSOLE_BYTES. Default: 65536 (64 KiB).
	JavaScriptMaxConsoleBytes int64 `koanf:"javascript_max_console_bytes"`
}

// MaxJavaScriptTimeout is the highest code.javascript_timeout accepted. A
// backtracking regular expression can overrun a script's limit by about this
// long, so the ceiling is kept to minutes.
const MaxJavaScriptTimeout = 5 * time.Minute

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
		Google: Google{
			ClientSecretEnv: "KILASFLOW_GOOGLE_CLIENT_SECRET",
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
			OperatorKeyEnv:       "KILASFLOW_AUTH_OPERATOR_KEY",
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
			RequireAuth:     false,
		},
		Embed: Embed{
			SigningKeyEnv: "KILASFLOW_EMBED_SIGNING_KEY",
			SessionTTL:    15 * time.Minute,
		},
		// Empty on purpose. The editor draws a header bar as soon as a name or a
		// logo is present, so any non-empty default — branding.name used to be
		// "KilasFlow" — would add one to every white-label embed that already
		// exists. See Branding.
		Branding: Branding{},
		Execution: Execution{
			MaxConcurrent:  10,
			DefaultTimeout: 2 * time.Minute,
			// Keep every execution. See the field.
			Retention: 0,
			// Frequent enough that a wait whose process died is settled soon
			// after the next boot, coarse enough that the sweep is not a
			// per-second query on an idle install.
			WaitSweepInterval: 10 * time.Second,
			// An hour, matching n8n's EXECUTIONS_TIMEOUT_MAX default. A
			// workflow that asks for more than this is asking for a run that
			// holds a worker for the rest of the day.
			MaxTimeout: time.Hour,
		},
		History: History{
			// Keep everything. Deleting a customer's history is not a default
			// anyone should get by not reading the configuration reference.
			Retention:   0,
			MaxVersions: 0,
		},
		Idempotency: Idempotency{
			// On by default, unlike the retention bounds above: a retried
			// request that runs twice is a duplicate the customer did not ask
			// for, and the guarantee is worth more than the day of keys it
			// costs. A day covers the retry a client makes after an outage;
			// it is not meant to be a ledger.
			Retention: 24 * time.Hour,
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
			// Beside the default database, so the two share a volume and a
			// backup. See the field for why this is on by default.
			Root:     "./data/binary",
			MaxBytes: 16 << 20,
		},
		Packs: Packs{
			// Empty disables directory loading: the default deployment runs
			// the embedded packs only, exactly as before.
			Dir: "",
		},
		Sidecar: Sidecar{
			// Off by default, and every field below is the value used when an
			// operator turns it on without overriding the limits.
			Enabled:     false,
			RuntimeDir:  "./data/sidecar",
			PackagesDir: "",
			NodePath:    "",
			// Kept equal to sidecar.DefaultLimits, which a test pins. The
			// numbers are conservative because the code across the socket is
			// third party.
			Timeout:        30 * time.Second,
			SpawnTimeout:   15 * time.Second,
			IdleTimeout:    5 * time.Minute,
			MaxHeapMB:      256,
			MaxRSSMB:       512,
			MaxProcesses:   16,
			MaxOutputBytes: 4 << 20,
		},
		Code: Code{
			// The go command on PATH, which is what a self-hosted install and
			// a development machine have; the distroless image has neither,
			// and the diagnostic tells its operator to point this somewhere.
			GoBinary: "go",
			// Inside the data volume the Dockerfile already declares, so the
			// caches survive a container restart with nothing extra mounted.
			CacheDir: "./data/codecache",
			// 2 GiB. Generous on purpose: eviction can force a rebuild that a
			// deployment without a toolchain cannot perform at all.
			CacheMaxBytes: 2147483648,
			// The engine is linked into the binary, so there is nothing to
			// install; the limits match jsrun.DefaultLimits.
			JavaScriptEnabled:         true,
			JavaScriptTimeout:         10 * time.Second,
			JavaScriptMaxInputBytes:   32 << 20,
			JavaScriptMaxOutputBytes:  16 << 20,
			JavaScriptMaxConsoleBytes: 64 << 10,
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
// A missing config file is not an error here: kilasflow is meant to run with no
// configuration at all, and config.yaml is the name it looks for by default. A
// caller whose operator named the file themselves wants LoadExplicit, where
// absence is a mistake worth stopping for.
func Load(path string) (Config, error) {
	return load(path, false)
}

// LoadExplicit builds a Config the way Load does, but treats a missing file as
// an error.
//
// It exists because silence used to be indistinguishable from success in both
// directions: `kilasflow -config /etc/kilasflow/does-not-exist.yaml` booted on
// defaults with no warning, so an operator who mistyped a path — or who moved a
// file and forgot — believed their egress and storage settings were applied.
func LoadExplicit(path string) (Config, error) {
	return load(path, true)
}

// load merges defaults, an optional file, and the environment, then warns about
// anything in either source that matched no configuration key.
func load(path string, mustExist bool) (Config, error) {
	k := koanf.New(".")

	if err := k.Load(structs.Provider(Default(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("load defaults: %w", err)
	}

	fileRead := false
	if path != "" {
		err := k.Load(file.Provider(path), yaml.Parser())
		switch {
		case err == nil:
			fileRead = true
		case isNotExist(err):
			if mustExist {
				return Config{}, fmt.Errorf("config file %s does not exist", path)
			}
		default:
			return Config{}, fmt.Errorf("load %s: %w", path, err)
		}
	}

	// ProviderWithValue rather than Provider so a list-valued key can come from
	// the environment at all: the default decoder turns one string into a
	// one-element slice, which is why outbound.allowed_hosts,
	// outbound.allowed_private_endpoints and embed.allowed_origins used to
	// accept at most one entry from an environment variable — while the
	// generated reference documented an Env var for each of them.
	if err := k.Load(env.ProviderWithValue(EnvPrefix, ".", envValue), nil); err != nil {
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

	warnUnknownKeys(k, path, fileRead)
	return cfg, nil
}

// envValue maps one environment variable to a koanf path and value.
//
// A key whose target is a list is split on commas, because that is the only way
// a container deployment with no mounted file can allow two hosts, two private
// endpoints, or two embed origins. Everything else stays a string: a DSN, a
// password, or a path may legitimately contain a comma, and splitting those
// would corrupt a value that was perfectly correct.
func envValue(key, value string) (string, any) {
	path := envKeyToPath(key)
	if listPaths[path] {
		return path, splitList(value)
	}
	return path, value
}

// splitList turns a comma-separated environment value into its entries.
//
// Empty entries are dropped rather than kept as empty strings: an empty string
// in outbound.allowed_hosts is an entry that matches nothing and would read as
// a configured host, and the same in embed.allowed_origins is an origin that
// can never be normalised.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	entries := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

// listPaths holds every leaf path whose field is a slice.
//
// Derived from the struct by reflection rather than listed by hand: a
// hand-written list is one more thing to forget when a slice key is added, and
// the way it fails is an environment variable that silently keeps only its
// first entry — the exact defect this exists to remove.
var listPaths = sliceFieldPaths()

// warnUnknownKeys reports every merged key that matches no field, with the
// nearest key that does.
//
// Both a YAML typo and a misspelled KILASFLOW_* variable land here, because
// both reach the merged tree as a path nothing reads: koanf drops what no field
// claims, and the operator is left believing a security setting is in force.
func warnUnknownKeys(k *koanf.Koanf, path string, fileRead bool) {
	known := fieldPaths()
	logger := warningLogger()
	for _, key := range k.Keys() {
		if known[key] {
			continue
		}
		logger.Warn("configuration key matches nothing and was ignored",
			"key", key,
			"source", configSource(path, fileRead),
			"did_you_mean", nearestKey(key, known))
	}
}

// warningLogger is the logger a loader warning goes through.
//
// The configuration is still being read here, so the logger it configures does
// not exist yet and the warning used to go through slog's default handler:
// text on stderr, which is neither the stream nor the format a deployment
// collects. A JSON pipeline therefore lost the one line saying that a security
// setting had been ignored — the line it exists to hold.
//
// The format is read from the environment because that is the only signal
// available before the file has been parsed, exactly as the binary's fatal
// reporter reads it. Everything else keeps the default logger, so an in-process
// caller — and every test — still reads these warnings from the logger it
// installed.
func warningLogger() *slog.Logger {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("KILASFLOW_LOG_FORMAT")), "json") {
		return slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return slog.Default()
}

// configSource names where the merged value came from, for a warning's benefit.
func configSource(path string, fileRead bool) string {
	switch {
	case path == "":
		return "environment"
	case fileRead:
		return path
	default:
		return path + " (not found)"
	}
}

// nearestKey finds the configured key closest to an unknown one, so a warning
// names the key the operator probably meant.
func nearestKey(unknown string, known map[string]bool) string {
	best, bestDistance := "", -1
	for candidate := range known {
		distance := editDistance(unknown, candidate)
		if bestDistance < 0 || distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best
}

// editDistance is the Levenshtein distance between two keys. Keys are short, so
// the quadratic table costs nothing next to reading the file.
func editDistance(left, right string) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, min(current[j-1]+1, previous[j-1]+cost))
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

// fieldPaths returns every leaf and section path the Config struct declares.
//
// Sections are included as well as leaves: koanf reports a key for a section
// that holds a scalar only if a file or the environment set one, and a section
// name is never a typo worth warning about on its own.
func fieldPaths() map[string]bool {
	paths := map[string]bool{}
	collectFieldPaths(reflect.TypeOf(Config{}), "", paths, false)
	return paths
}

// sliceFieldPaths returns every leaf path whose field is a slice.
func sliceFieldPaths() map[string]bool {
	paths := map[string]bool{}
	collectFieldPaths(reflect.TypeOf(Config{}), "", paths, true)
	return paths
}

// collectFieldPaths walks the configuration struct through its koanf tags.
//
// onlySlices selects the two jobs this walk does: the full set of known paths,
// and the subset whose values are lists. One walk rather than two, so the two
// answers cannot come from two readings of the same struct that disagree.
func collectFieldPaths(t reflect.Type, prefix string, into map[string]bool, onlySlices bool) {
	for index := range t.NumField() {
		field := t.Field(index)
		name, tagged := field.Tag.Lookup("koanf")
		if !tagged || name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		switch fieldType.Kind() {
		case reflect.Struct:
			if !onlySlices {
				into[path] = true
			}
			collectFieldPaths(fieldType, path, into, onlySlices)
		case reflect.Slice:
			// A slice is a leaf for both jobs: it is a known path, and it is
			// one whose environment value has to be split.
			into[path] = true
		default:
			if !onlySlices {
				into[path] = true
			}
		}
	}
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

	// A retention under a minute would expire a key before a retrying client
	// could plausibly use it, and one over thirty days turns the retry guard
	// into a ledger. Zero is included in the refusal: idempotency that never
	// replays is indistinguishable from having none, which is not what an
	// operator setting the key to zero meant to ask for.
	if c.Idempotency.Retention < MinIdempotencyRetention || c.Idempotency.Retention > MaxIdempotencyRetention {
		return fmt.Errorf("idempotency.retention %s must be between %s and %s",
			c.Idempotency.Retention, MinIdempotencyRetention, MaxIdempotencyRetention)
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

	// A negative budget is not a smaller budget: it would evict every artifact
	// the moment it was written, including the one the operator's toolchain
	// just produced. Zero is the documented way to say unbounded.
	if c.Code.CacheMaxBytes < 0 {
		return fmt.Errorf("code.cache_max_bytes %d must not be negative", c.Code.CacheMaxBytes)
	}

	// A JavaScript limit of zero or less would read as "no limit" to anyone
	// but the runtime, which treats it as the default; saying so here is
	// clearer than either.
	if c.Code.JavaScriptTimeout <= 0 || c.Code.JavaScriptTimeout > MaxJavaScriptTimeout {
		return fmt.Errorf("code.javascript_timeout %s must be positive and at most %s", c.Code.JavaScriptTimeout, MaxJavaScriptTimeout)
	}
	for _, limit := range []struct {
		key   string
		value int64
	}{
		{"code.javascript_max_input_bytes", c.Code.JavaScriptMaxInputBytes},
		{"code.javascript_max_output_bytes", c.Code.JavaScriptMaxOutputBytes},
		{"code.javascript_max_console_bytes", c.Code.JavaScriptMaxConsoleBytes},
	} {
		if limit.value <= 0 {
			return fmt.Errorf("%s %d must be positive", limit.key, limit.value)
		}
	}
	if c.Code.JavaScriptMaxConcurrent < 0 {
		return fmt.Errorf("code.javascript_max_concurrent %d must not be negative; zero means one per CPU", c.Code.JavaScriptMaxConcurrent)
	}
	if c.Code.JavaScriptHeapCeilingMB < 0 {
		return fmt.Errorf("code.javascript_heap_ceiling_mb %d must not be negative; zero derives it from GOMEMLIMIT", c.Code.JavaScriptHeapCeilingMB)
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

	// Refused rather than silently falling back to text: an operator who
	// mistypes "json" gets a deployment whose log pipeline quietly discards
	// everything it was configured to collect, and the mistake is invisible
	// from the inside. log.level keeps its documented fallback, because a
	// level that is too high loses detail rather than structure.
	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format %q must be text or json", c.Log.Format)
	}

	// Checked here so a typo names the key at boot rather than surfacing as
	// every schedule computing its next run in UTC. Empty means UTC, which is
	// what a server with no configured zone has always used.
	if strings.TrimSpace(c.Execution.DefaultTimezone) != "" {
		if _, err := datetime.Zone(c.Execution.DefaultTimezone); err != nil {
			return fmt.Errorf("execution.default_timezone %q: %w", c.Execution.DefaultTimezone, err)
		}
	}

	if err := c.validateEmbed(); err != nil {
		return err
	}

	if err := c.Packs.validateVisibleTo(); err != nil {
		return err
	}

	if err := validateSidecar(c.Sidecar); err != nil {
		return err
	}
	return nil
}

// sidecarHostIsUnix reports whether this build can run the sidecar at all. It
// is a variable so a test can prove the refusal without being on Windows.
var sidecarHostIsUnix = runtime.GOOS != "windows"

// sidecarPackageName is the npm name grammar: a lower-case name with an
// optional @scope/ prefix. Every path segment has to begin with a letter or a
// digit, which is what refuses "..", an absolute path and any name carrying a
// separator — so a package list can never walk out of the packages directory.
var sidecarPackageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

// npmPackageNameMax is npm's own length limit for a package name, scope
// included.
const npmPackageNameMax = 214

// validateSidecar refuses a sidecar configuration that cannot work. It runs
// only when the sidecar is enabled: a declined sidecar is a deployment where
// none of these values is read, so a stray value there must not refuse a boot.
func validateSidecar(cfg Sidecar) error {
	if !cfg.Enabled {
		return nil
	}
	if !sidecarHostIsUnix {
		return fmt.Errorf("sidecar.enabled is not supported on %s: the JavaScript sidecar needs a unix host", runtime.GOOS)
	}
	if strings.TrimSpace(cfg.PackagesDir) == "" {
		return fmt.Errorf("sidecar.packages_dir is required when sidecar.enabled is true")
	}
	if strings.TrimSpace(cfg.RuntimeDir) == "" {
		return fmt.Errorf("sidecar.runtime_dir is required when sidecar.enabled is true")
	}
	if len(cfg.Packages) == 0 {
		return fmt.Errorf("sidecar.packages must name at least one package when sidecar.enabled is true")
	}
	for index, name := range cfg.Packages {
		if err := validateSidecarPackageName(name); err != nil {
			return fmt.Errorf("sidecar.packages[%d]: %w", index, err)
		}
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("sidecar.timeout %s must be positive", cfg.Timeout)
	}
	if cfg.SpawnTimeout <= 0 {
		return fmt.Errorf("sidecar.spawn_timeout %s must be positive", cfg.SpawnTimeout)
	}
	if cfg.IdleTimeout <= 0 {
		return fmt.Errorf("sidecar.idle_timeout %s must be positive", cfg.IdleTimeout)
	}
	if cfg.MaxHeapMB < 16 {
		return fmt.Errorf("sidecar.max_heap_mb %d must be at least 16", cfg.MaxHeapMB)
	}
	// Zero is allowed and documented: it turns the host-side RSS watchdog off
	// rather than making the bound zero, which would kill every run.
	if cfg.MaxRSSMB < 0 {
		return fmt.Errorf("sidecar.max_rss_mb %d must not be negative", cfg.MaxRSSMB)
	}
	if cfg.MaxProcesses < 1 {
		return fmt.Errorf("sidecar.max_processes %d must be at least 1", cfg.MaxProcesses)
	}
	if cfg.MaxOutputBytes <= 0 {
		return fmt.Errorf("sidecar.max_output_bytes %d must be positive", cfg.MaxOutputBytes)
	}
	return nil
}

// validateSidecarPackageName refuses anything outside the npm name grammar.
func validateSidecarPackageName(name string) error {
	if len(name) > npmPackageNameMax {
		return fmt.Errorf("%q is longer than npm's %d-character limit", name, npmPackageNameMax)
	}
	if !sidecarPackageName.MatchString(name) {
		return fmt.Errorf("%q is not an npm package name", name)
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
