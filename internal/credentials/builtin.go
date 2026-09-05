package credentials

import (
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/property"
)

// RegisterAll registers the credential types this server ships with.
//
// Every ID and every field key is byte-identical to what the package-level map
// held, because a stored credential row is keyed by that ID and its sealed
// payload by those keys: change one and an existing credential stops resolving
// with no way to recover it.
func RegisterAll(registry *Registry) error {
	for _, credentialType := range []Type{
		{
			ID: "httpBasicAuth", DisplayName: "HTTP Basic Auth",
			Description: "Sends an RFC 7617 Authorization header.",
			Properties: []property.PropertyDefinition{
				{Key: "user", Label: "User", Kind: property.KindString, Required: true},
				{
					Key: "password", Label: "Password", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
			},
			Secrets:      []string{"password"},
			Authenticate: &Authentication{Placement: PlacementBasic, User: "user", Password: "password"},
		},
		{
			ID: "httpHeaderAuth", DisplayName: "HTTP Header Auth",
			Description: "Sends a fixed header, for example an API key.",
			Properties: []property.PropertyDefinition{
				{Key: "name", Label: "Header name", Kind: property.KindString, Required: true},
				{
					Key: "value", Label: "Header value", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
			},
			Secrets: []string{"value"},
			// The header name is itself a field, so this one descriptor covers
			// every fixed-header API — including WAHA's X-Api-Key, which needs
			// no Go change at all.
			Authenticate: &Authentication{
				Placement: PlacementHeader, Name: "{{ name }}", Value: "{{ value }}",
			},
		},
		{
			ID: "httpBearerAuth", DisplayName: "HTTP Bearer Auth",
			Description: "Sends an Authorization: Bearer header.",
			Properties: []property.PropertyDefinition{
				{
					Key: "token", Label: "Token", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
			},
			Secrets:      []string{"token"},
			Authenticate: &Authentication{Placement: PlacementBearer, Value: "{{ token }}"},
		},
		{
			ID: "postgres", DisplayName: "PostgreSQL",
			Description: "Connects to a PostgreSQL database you own.",
			Properties: []property.PropertyDefinition{
				{Key: "host", Label: "Host", Kind: property.KindString, Required: true},
				{Key: "port", Label: "Port", Kind: property.KindString, Description: "Defaults to 5432."},
				{Key: "database", Label: "Database", Kind: property.KindString, Required: true},
				{Key: "user", Label: "User", Kind: property.KindString, Required: true},
				{
					Key: "password", Label: "Password", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
				{Key: "sslMode", Label: "SSL mode", Kind: property.KindString, Description: "disable, require, verify-ca, or verify-full. Defaults to require."},
			},
			Secrets: []string{"password"},
			// No Authenticate descriptor: a database credential does not sign
			// an HTTP request, and refusing by absence is a better error than a
			// default branch.
		},
		{
			ID: "mysql", DisplayName: "MySQL",
			Description: "Connects to a MySQL or MariaDB database you own.",
			Properties: []property.PropertyDefinition{
				{Key: "host", Label: "Host", Kind: property.KindString, Required: true},
				{Key: "port", Label: "Port", Kind: property.KindString, Description: "Defaults to 3306."},
				{Key: "database", Label: "Database", Kind: property.KindString, Required: true},
				{Key: "user", Label: "User", Kind: property.KindString, Required: true},
				{
					Key: "password", Label: "Password", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
				{Key: "tls", Label: "TLS", Kind: property.KindString, Description: "true, skip-verify, preferred, or a registered config name."},
			},
			Secrets: []string{"password"},
		},
		{
			ID: "telegramApi", DisplayName: "Telegram",
			Description: "A Telegram bot token from BotFather.",
			Properties: []property.PropertyDefinition{
				{
					Key: "accessToken", Label: "Access token", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
					Description: "The token BotFather gave you, in the form 123456:ABC-DEF…",
				},
				{
					Key: "baseUrl", Label: "Base URL", Kind: property.KindString, Required: true,
					Default:     "https://api.telegram.org",
					Description: "Where the Bot API lives. Change it only when you run Telegram's own local Bot API server.",
				},
			},
			Secrets: []string{"accessToken"},
			// The Bot API puts the token in the *path* —
			// https://api.telegram.org/bot<token>/method — so there is no
			// header or query parameter to place. Saying that explicitly is
			// what distinguishes it from a credential that cannot sign an HTTP
			// request at all, which is still refused.
			Authenticate: &Authentication{Placement: PlacementPath},
		},
		{
			ID: "wahaApi", DisplayName: "WAHA",
			Description: "A self-hosted WAHA instance: its base URL and its API key.",
			Properties: []property.PropertyDefinition{
				{
					Key: "baseUrl", Label: "Base URL", Kind: property.KindString, Required: true,
					Description: "Where the WAHA instance is, for example https://waha.example.com.",
				},
				{
					Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
			},
			Secrets: []string{"apiKey"},
			// The base URL is deliberately *not* secret. A declarative pack
			// reads it as `{{ $credentials.baseUrl }}` to build every request,
			// and `$credentials` exposes non-secret fields only — so marking it
			// secret would leave the pack with no address to call.
			Authenticate: &Authentication{
				Placement: PlacementHeader, Name: "X-Api-Key", Value: "{{ apiKey }}",
			},
		},
		{
			ID: "sqlite", DisplayName: "SQLite file",
			Description: "Opens a SQLite file on the server. The path must be given explicitly and cannot be KilasFlow's own database.",
			Properties: []property.PropertyDefinition{
				{Key: "path", Label: "File path", Kind: property.KindString, Required: true, Description: "Absolute path to the database file."},
			},
		},
	} {
		if err := registry.Register(credentialType); err != nil {
			return fmt.Errorf("register credential types: %w", err)
		}
	}
	return nil
}
