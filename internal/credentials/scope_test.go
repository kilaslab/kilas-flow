package credentials_test

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// These tests pin the default host scope of the credential types whose service
// lives at one known address. Before it existed, an OpenAI, OpenRouter or
// Telegram credential saved with no allowed domains could be sent to any host,
// so anyone able to edit a workflow could aim it at a server they control and
// read the key off the wire.

func TestAFixedHostCredentialIsConfinedToItsServiceByDefault(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		record credentials.Record
		home   string
	}{
		{"openai", credentials.Record{Type: "openAiApi", Fields: map[string]string{"apiKey": "sk-1"}}, "api.openai.com"},
		{"openrouter", credentials.Record{Type: "openRouterApi", Fields: map[string]string{"apiKey": "sk-or-1"}}, "openrouter.ai"},
		{"telegram", credentials.Record{Type: "telegramApi", Fields: map[string]string{
			"accessToken": "1:A", "baseUrl": "https://api.telegram.org",
		}}, "api.telegram.org"},
		// A row saved before baseUrl was stored reads the field's own default,
		// exactly as the Telegram node does.
		{"telegram without a stored base URL", credentials.Record{Type: "telegramApi", Fields: map[string]string{
			"accessToken": "1:A",
		}}, "api.telegram.org"},
		{"gmail", credentials.Record{Type: credentials.GmailOAuthType}, "gmail.googleapis.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !test.record.AllowsHost(test.home) {
				t.Errorf("AllowsHost(%q) = false, want the service's own host allowed", test.home)
			}
			if !test.record.AllowsHost(test.home + ":443") {
				t.Errorf("AllowsHost(%q) = false, want the port ignored", test.home+":443")
			}
			for _, foreign := range []string{"attacker.test", "attacker.test:443", "evil." + test.home + ".attacker.test"} {
				if test.record.AllowsHost(foreign) {
					t.Errorf("AllowsHost(%q) = true, want a host outside the default scope refused", foreign)
				}
			}
			if test.record.Unscoped() {
				t.Error("Unscoped() = true, want a type with a default scope to count as scoped")
			}
		})
	}
}

func TestATelegramScopeFollowsASelfHostedBotAPIServer(t *testing.T) {
	t.Parallel()
	// Telegram's own local Bot API server is the one reason the base URL is
	// editable. The default scope is the host that URL names, so a self-hosted
	// server keeps working with no allowed domains typed in — and the public
	// API, which this credential no longer talks to, is not in it.
	record := credentials.Record{Type: "telegramApi", Fields: map[string]string{
		"accessToken": "1:A", "baseUrl": "http://bot-api.internal.test:8081",
	}}
	if !record.AllowsHost("bot-api.internal.test:8081") {
		t.Error("the configured Bot API server is refused")
	}
	for _, foreign := range []string{"api.telegram.org", "attacker.test"} {
		if record.AllowsHost(foreign) {
			t.Errorf("AllowsHost(%q) = true, want only the configured server", foreign)
		}
	}
	if got := record.EffectiveDomains(); !reflect.DeepEqual(got, []string{"bot-api.internal.test"}) {
		t.Errorf("EffectiveDomains() = %v, want the base URL's host", got)
	}
}

func TestAWAHAScopeFollowsItsOwnBaseURL(t *testing.T) {
	t.Parallel()
	record := credentials.Record{Type: "wahaApi", Fields: map[string]string{
		"baseUrl": "https://waha.example.test:3000", "apiKey": "k",
	}}
	if !record.AllowsHost("waha.example.test:3000") {
		t.Error("the WAHA instance the credential names is refused")
	}
	if record.AllowsHost("attacker.test") {
		t.Error("a WAHA key may be sent to a host other than its own instance")
	}

	// With no usable address there is no host to derive, and the credential is
	// as unscoped as it ever was — which the confinement rule refuses.
	empty := credentials.Record{Type: "wahaApi", Fields: map[string]string{"baseUrl": "not a url", "apiKey": "k"}}
	if !empty.Unscoped() {
		t.Error("a WAHA credential with no parseable base URL must read as unscoped")
	}
}

func TestAnAuthoredScopeReplacesTheDefault(t *testing.T) {
	t.Parallel()
	// An owner who fronts OpenAI with a gateway lists the gateway; the default
	// is only what an empty list means.
	record := credentials.Record{Type: "openAiApi", AllowedDomains: []string{"gateway.example.test"}}
	if !record.AllowsHost("gateway.example.test") {
		t.Error("the authored host is refused")
	}
	if record.AllowsHost("api.openai.com") {
		t.Error("the default was added to an authored list; it must replace it, not widen it")
	}
}

func TestAGenericHTTPCredentialKeepsNoDefaultScope(t *testing.T) {
	t.Parallel()
	record := credentials.Record{Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Key"}}
	if !record.AllowsHost("anything.example.test") {
		t.Error("a generic credential with no allowed domains must still reach any host for its owner")
	}
	if len(record.EffectiveDomains()) != 0 {
		t.Errorf("EffectiveDomains() = %v, want none: a generic type has no home host", record.EffectiveDomains())
	}
}

func TestUnscopedNamesTheCredentialsThatCouldBeSentAnywhere(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		record credentials.Record
		want   bool
	}{
		{"generic header, no domains", credentials.Record{Type: "httpHeaderAuth"}, true},
		{"generic bearer, no domains", credentials.Record{Type: "httpBearerAuth"}, true},
		{"generic custom, no domains", credentials.Record{Type: "httpCustomAuth"}, true},
		{"generic header, scoped", credentials.Record{Type: "httpHeaderAuth", AllowedDomains: []string{"partner.test"}}, false},
		{"openai default", credentials.Record{Type: "openAiApi"}, false},
		{"telegram default", credentials.Record{Type: "telegramApi"}, false},
		{"google default", credentials.Record{Type: credentials.GoogleDriveOAuthType}, false},
		// A database dials the host its own fields name, a file credential
		// names a path, and a JWT credential verifies requests arriving here:
		// none of them is placed on a request whose URL a node chooses.
		{"postgres", credentials.Record{Type: "postgres"}, false},
		{"mysql", credentials.Record{Type: "mysql"}, false},
		{"sqlite", credentials.Record{Type: "sqlite"}, false},
		{"jwt", credentials.Record{Type: "jwtAuth"}, false},
		// A type this server does not know — a pack since uninstalled — is
		// read the safe way: nothing says where it may go.
		{"unknown type", credentials.Record{Type: "pack.somethingApi"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.record.Unscoped(); got != test.want {
				t.Errorf("Unscoped() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTheCatalogueCarriesEachTypesDefaultScope(t *testing.T) {
	t.Parallel()
	openAI, _ := credentials.Lookup("openAiApi")
	if !reflect.DeepEqual(openAI.DefaultDomains, []string{"api.openai.com"}) {
		t.Errorf("openAiApi DefaultDomains = %v, want api.openai.com", openAI.DefaultDomains)
	}
	openRouter, _ := credentials.Lookup("openRouterApi")
	if !reflect.DeepEqual(openRouter.DefaultDomains, []string{"openrouter.ai"}) {
		t.Errorf("openRouterApi DefaultDomains = %v, want openrouter.ai", openRouter.DefaultDomains)
	}
	for _, typeID := range []string{"telegramApi", "wahaApi"} {
		definition, _ := credentials.Lookup(typeID)
		if definition.DefaultDomainsFrom != "baseUrl" {
			t.Errorf("%s DefaultDomainsFrom = %q, want baseUrl", typeID, definition.DefaultDomainsFrom)
		}
	}
	header, _ := credentials.Lookup("httpHeaderAuth")
	if len(header.DefaultDomains) != 0 || header.DefaultDomainsFrom != "" {
		t.Errorf("httpHeaderAuth carries a default scope %v/%q, want none", header.DefaultDomains, header.DefaultDomainsFrom)
	}
}

func TestApplyDefaultDomainsMaterialisesOnlyAFixedList(t *testing.T) {
	t.Parallel()
	openAI := credentials.Record{Type: "openAiApi"}
	credentials.ApplyDefaultDomains(&openAI)
	if !reflect.DeepEqual(openAI.AllowedDomains, []string{"api.openai.com"}) {
		t.Errorf("AllowedDomains = %v, want the fixed default stored", openAI.AllowedDomains)
	}
	// A scope derived from a field is computed when it is read, never stored:
	// a stored copy would go stale the moment the owner moved the base URL.
	telegram := credentials.Record{Type: "telegramApi", Fields: map[string]string{"baseUrl": "https://api.telegram.org"}}
	credentials.ApplyDefaultDomains(&telegram)
	if len(telegram.AllowedDomains) != 0 {
		t.Errorf("AllowedDomains = %v, want a derived scope left unstored", telegram.AllowedDomains)
	}
	authored := credentials.Record{Type: "openAiApi", AllowedDomains: []string{"gateway.example.test"}}
	credentials.ApplyDefaultDomains(&authored)
	if !reflect.DeepEqual(authored.AllowedDomains, []string{"gateway.example.test"}) {
		t.Errorf("AllowedDomains = %v, want an authored list untouched", authored.AllowedDomains)
	}
}
