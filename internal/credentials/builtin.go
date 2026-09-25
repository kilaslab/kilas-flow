package credentials

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/property"
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
			ID: "jwtAuth", DisplayName: "JWT Auth",
			Description: "Holds the key that verifies incoming JSON Web Tokens.",
			Properties: []property.PropertyDefinition{
				{
					Key: "keyType", Label: "Key Type", Kind: property.KindOptions, Required: true, Default: "passphrase",
					Options: []property.PropertyOption{
						{Label: "Passphrase", Value: "passphrase"},
						{Label: "PEM Key", Value: "pemKey"},
					},
				},
				{
					Key: "secret", Label: "Secret", Kind: property.KindString,
					TypeOptions: &property.TypeOptions{Password: true},
					VisibleWhen: []property.VisibilityCondition{{Key: "keyType", Equals: "passphrase"}},
					Description: "The shared secret an HMAC-signed token was signed with.",
				},
				{
					Key: "publicKey", Label: "Public Key", Kind: property.KindString,
					VisibleWhen: []property.VisibilityCondition{{Key: "keyType", Equals: "pemKey"}},
					Description: "PEM-encoded public key (RSA or ECDSA) the token signature is checked against.",
				},
				{
					Key: "privateKey", Label: "Private Key", Kind: property.KindString,
					TypeOptions: &property.TypeOptions{Password: true},
					VisibleWhen: []property.VisibilityCondition{{Key: "keyType", Equals: "pemKey"}},
					Description: "Kept so a payload written by n8n's JWT credential is stored unchanged; inbound verification reads the public key.",
				},
				{
					Key: "algorithm", Label: "Algorithm", Kind: property.KindOptions, Required: true, Default: "HS256",
					Options: []property.PropertyOption{
						{Label: "HS256", Value: "HS256"}, {Label: "HS384", Value: "HS384"}, {Label: "HS512", Value: "HS512"},
						{Label: "RS256", Value: "RS256"}, {Label: "RS384", Value: "RS384"}, {Label: "RS512", Value: "RS512"},
						{Label: "ES256", Value: "ES256"}, {Label: "ES384", Value: "ES384"}, {Label: "ES512", Value: "ES512"},
						{Label: "PS256", Value: "PS256"}, {Label: "PS384", Value: "PS384"}, {Label: "PS512", Value: "PS512"},
					},
				},
			},
			Secrets: []string{"secret", "privateKey"},
			// No Authenticate descriptor: this credential verifies callers
			// arriving at us, it does not sign our outbound requests.
		},
		{
			ID: "httpQueryAuth", DisplayName: "HTTP Query Auth",
			Description: "Sends a fixed query parameter, for example ?api_key=…",
			Properties: []property.PropertyDefinition{
				{Key: "name", Label: "Query parameter name", Kind: property.KindString, Required: true},
				{
					Key: "value", Label: "Query parameter value", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
				},
			},
			Secrets:      []string{"value"},
			Authenticate: &Authentication{Placement: PlacementQuery, Name: "{{ name }}", Value: "{{ value }}"},
		},
		{
			ID: "httpCustomAuth", DisplayName: "HTTP Custom Auth",
			Description: "Sends several headers and query parameters described by a JSON template, for example {\"headers\":{\"X-Api-Key\":\"abc\"},\"qs\":{\"tenant\":\"acme\"}}.",
			Properties: []property.PropertyDefinition{
				{
					Key: "json", Label: "JSON", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
					Description: "An object with optional headers and qs objects. Every value is sent as written; the body is not modifiable from here.",
				},
			},
			Secrets:      []string{"json"},
			Authenticate: &Authentication{Placement: PlacementCustom, Value: "{{ json }}"},
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
			ID: "openAiApi", DisplayName: "OpenAI",
			Description: "An OpenAI API key.",
			Properties: []property.PropertyDefinition{
				{
					Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
					Description: "A key from platform.openai.com, in the form sk-…",
				},
			},
			Secrets: []string{"apiKey"},
			// The field key is n8n's `apiKey`, not the generic `token`, because
			// a node declares which credential *type* it accepts and an
			// imported workflow names `openAiApi` — so the two have to be the
			// same type, and a stored payload of one must read as the other.
			Authenticate: &Authentication{Placement: PlacementBearer, Value: "{{ apiKey }}"},
			Test:         &TestRequest{URL: "https://api.openai.com/v1/models"},
		},
		{
			ID: "openRouterApi", DisplayName: "OpenRouter",
			Description: "An OpenRouter API key.",
			Properties: []property.PropertyDefinition{
				{
					Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true,
					TypeOptions: &property.TypeOptions{Password: true},
					Description: "A key from openrouter.ai, in the form sk-or-…",
				},
			},
			Secrets:      []string{"apiKey"},
			Authenticate: &Authentication{Placement: PlacementBearer, Value: "{{ apiKey }}"},
			// n8n probes /key here rather than /models: OpenRouter serves its
			// model catalogue unauthenticated, so /models answers 200 for a key
			// that is expired or revoked and the test would pass on a
			// credential that cannot complete anything.
			Test: &TestRequest{URL: "https://openrouter.ai/api/v1/key"},
		},
		googleOAuthType("googleDriveOAuth2Api", "Google Drive OAuth2",
			"Connects to Google Drive. Client ID and secret can be from your Google Cloud project or the platform app.",
			"https://www.googleapis.com/auth/drive"),
		googleOAuthType("gmailOAuth2", "Gmail OAuth2",
			"Connects to Gmail. Client ID and secret can be from your Google Cloud project or the platform app.",
			"https://www.googleapis.com/auth/gmail.modify"),
		{
			ID: "sqlite", DisplayName: "SQLite file",
			Description: "Opens a SQLite file in this tenant's own directory on the server. The file is created on first use if it does not exist.",
			Properties: []property.PropertyDefinition{
				{Key: "path", Label: "File path", Kind: property.KindString, Required: true, Description: "Path relative to this tenant's SQLite directory, for example orders.db or reports/q1.db. Absolute paths and .. are refused."},
			},
		},
	} {
		if err := registry.Register(credentialType); err != nil {
			return fmt.Errorf("register credential types: %w", err)
		}
	}
	return nil
}
