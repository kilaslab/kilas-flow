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
)

// EnvPrefix is the prefix for environment overrides, e.g. KILASFLOW_SERVER_PORT.
const EnvPrefix = "KILASFLOW_"

// Config is the root configuration document.
type Config struct {
	Server    Server       `koanf:"server"`
	Database  Database     `koanf:"database"`
	Security  Security     `koanf:"security"`
	Outbound  OutboundHTTP `koanf:"outbound"`
	Webhook   Webhook      `koanf:"webhook"`
	Branding  Branding     `koanf:"branding"`
	Execution Execution    `koanf:"execution"`
	Log       Log          `koanf:"log"`
}

// Server holds HTTP listener settings.
type Server struct {
	Host string `koanf:"host"`
	Port int    `koanf:"port"`

	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	ShutdownTimeout   time.Duration `koanf:"shutdown_timeout"`
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
	AllowPrivateNetworks bool          `koanf:"allow_private_networks"`
	AllowedHosts         []string      `koanf:"allowed_hosts"`
	MaxRedirects         int           `koanf:"max_redirects"`
	MaxResponseBytes     int64         `koanf:"max_response_bytes"`
	Timeout              time.Duration `koanf:"timeout"`
}

// Webhook bounds one inbound trigger request.
type Webhook struct {
	MaxBodyBytes    int64         `koanf:"max_body_bytes"`
	ResponseTimeout time.Duration `koanf:"response_timeout"`
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
		Branding: Branding{
			Name:      "KilasFlow",
			PoweredBy: true,
		},
		Execution: Execution{
			MaxConcurrent:  10,
			DefaultTimeout: 60 * time.Second,
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
