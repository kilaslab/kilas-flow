package api_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
)

// BUG-epy2se: the catalogue says which types have a probe, so the editor can
// leave Test off a type that has none instead of reporting a failure for it.
func TestTheCatalogueSaysWhichCredentialTypesCanBeTested(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	listed := requestJSON[[]struct {
		ID       string `json:"id"`
		Testable *bool  `json:"testable"`
	}](t, handler, http.MethodGet, "/api/v1/credential-types", nil, http.StatusOK)
	want := map[string]bool{
		"openAiApi": true, "openRouterApi": true, "telegramApi": true, "gmailOAuth2": true,
		// A database is tested by connecting to it.
		"postgres": true, "mysql": true, "sqlite": true,
		"httpHeaderAuth": false, "httpBasicAuth": false, "httpBearerAuth": false,
		"httpQueryAuth": false, "httpCustomAuth": false, "jwtAuth": false,
	}
	seen := map[string]bool{}
	for _, entry := range listed {
		expected, pinned := want[entry.ID]
		if !pinned {
			continue
		}
		seen[entry.ID] = true
		if entry.Testable == nil || *entry.Testable != expected {
			t.Errorf("%s testable = %v, want %v", entry.ID, entry.Testable, expected)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s is missing from the catalogue", id)
		}
	}

	// A type with no probe still answers untestable rather than failed.
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credential-types/httpHeaderAuth/test", map[string]any{
			"fields": map[string]string{"name": "X-Api-Key", "value": "abc"},
		}, http.StatusOK)
	if verdict.OK || !verdict.Untestable {
		t.Errorf("verdict = %#v, want untestable", verdict)
	}
}

// BUG-epy2se: the docs tell people to leave a Telegram credential's base URL
// empty, so a token alone has to save, and its scope is Telegram's own host.
func TestATelegramCredentialSavesWithATokenAlone(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	created := storeCredential(t, handler, "Bot", "telegramApi", map[string]string{"accessToken": "123:abc"})
	if !reflect.DeepEqual(created.AllowedDomains, []string{"api.telegram.org"}) {
		t.Errorf("allowedDomains = %v, want Telegram's own host when no base URL is set", created.AllowedDomains)
	}
}
