package credentials_test

// BUG-epy2se: a Telegram credential could not be saved without the base URL the
// docs say to leave empty and had no probe, and an OpenAI credential had no
// base URL, so one pointed at an OpenAI-compatible endpoint always failed its
// test against api.openai.com.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// probeRecorder answers 200 to a request carrying want in its path and
// Authorization header, 401 to anything else, and records the path it saw.
type probeRecorder struct {
	mu   sync.Mutex
	path string
	auth string
}

func (recorder *probeRecorder) server(t *testing.T, wantPath, wantAuth string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.mu.Lock()
		recorder.path, recorder.auth = r.URL.Path, r.Header.Get("Authorization")
		recorder.mu.Unlock()
		if r.URL.Path != wantPath || r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// endpointPolicy is the default egress policy with exactly one loopback
// endpoint granted, never allow_private_networks.
func endpointPolicy(t *testing.T, serverURL string) safehttp.Policy {
	t.Helper()
	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks, which would make this test prove nothing")
	}
	policy.AllowedPrivateEndpoints = []string{endpointOf(t, serverURL)}
	return policy
}

func TestATelegramCredentialSavesWithoutABaseURL(t *testing.T) {
	t.Parallel()
	if err := credentials.Validate("telegramApi", map[string]string{"accessToken": "123:abc"}); err != nil {
		t.Fatalf("Validate() = %v, want a token alone to be a complete Telegram credential", err)
	}
	definition, _ := credentials.Lookup("telegramApi")
	for _, field := range definition.Fields {
		if field.Key != "baseUrl" {
			continue
		}
		if field.Required {
			t.Error("baseUrl is required, but the docs tell people to leave it empty")
		}
		if field.Default != "https://api.telegram.org" {
			t.Errorf("baseUrl default = %q, want https://api.telegram.org", field.Default)
		}
	}
}

func TestWithDefaultsFillsOnlyWhatTheCredentialLeftEmpty(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"absent", map[string]string{"accessToken": "1:A"}, "https://api.telegram.org"},
		{"blank", map[string]string{"accessToken": "1:A", "baseUrl": "  "}, "https://api.telegram.org"},
		{"set", map[string]string{"accessToken": "1:A", "baseUrl": "http://bot-api.internal.test:8081"}, "http://bot-api.internal.test:8081"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			filled := credentials.WithDefaults("telegramApi", test.fields)
			if filled["baseUrl"] != test.want {
				t.Errorf("baseUrl = %q, want %q", filled["baseUrl"], test.want)
			}
			if filled["accessToken"] != "1:A" {
				t.Errorf("accessToken = %q, want the stored value untouched", filled["accessToken"])
			}
		})
	}
	// The caller's map is not written to: it may be the resolved credential.
	original := map[string]string{"accessToken": "1:A"}
	credentials.WithDefaults("telegramApi", original)
	if _, written := original["baseUrl"]; written {
		t.Error("WithDefaults wrote into the caller's map")
	}
}

func TestATelegramCredentialProbesGetMeOnItsOwnBotAPIServer(t *testing.T) {
	t.Parallel()
	recorder := &probeRecorder{}
	server := recorder.server(t, "/bot123:abc/getMe", "")
	telegram, _ := credentials.Default().Get("telegramApi")
	if telegram.Test == nil {
		t.Fatal("telegramApi declares no probe")
	}

	fields := map[string]string{"accessToken": "123:abc", "baseUrl": server.URL}
	record := credentials.Record{Type: "telegramApi", Fields: fields}
	detail, err := credentials.RunTest(context.Background(), telegram, record, fields, endpointPolicy(t, server.URL))
	if err != nil {
		t.Fatalf("RunTest() = %v (path %q), want getMe to answer", err, recorder.path)
	}
	if !strings.Contains(detail, "200") {
		t.Errorf("detail = %q, want the status named", detail)
	}

	wrong := map[string]string{"accessToken": "999:nope", "baseUrl": server.URL}
	if _, err := credentials.RunTest(context.Background(), telegram,
		credentials.Record{Type: "telegramApi", Fields: wrong}, wrong, endpointPolicy(t, server.URL)); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("RunTest() with a wrong token = %v, want the 401 reported", err)
	}
}

func TestAnOpenAICredentialCarriesAnOptionalBaseURL(t *testing.T) {
	t.Parallel()
	if err := credentials.Validate("openAiApi", map[string]string{"apiKey": "sk-1"}); err != nil {
		t.Fatalf("Validate() = %v, want the base URL optional", err)
	}
	definition, _ := credentials.Lookup("openAiApi")
	var found bool
	for _, field := range definition.Fields {
		if field.Key != "url" {
			continue
		}
		found = true
		if field.Required || field.Secret {
			t.Errorf("url field = %#v, want optional and readable", field)
		}
		if field.Default != "https://api.openai.com/v1" {
			t.Errorf("url default = %q, want https://api.openai.com/v1", field.Default)
		}
	}
	if !found {
		t.Fatalf("openAiApi fields = %#v, want n8n's url key", definition.Fields)
	}

	// The key's home follows the base URL its owner set, and is OpenAI itself
	// when none is set.
	plain := credentials.Record{Type: "openAiApi", Fields: map[string]string{"apiKey": "sk-1"}}
	if !plain.AllowsHost("api.openai.com") || plain.AllowsHost("attacker.test") {
		t.Errorf("an OpenAI credential with no base URL scopes to %v, want api.openai.com only", plain.EffectiveDomains())
	}
	gateway := credentials.Record{Type: "openAiApi", Fields: map[string]string{
		"apiKey": "sk-1", "url": "https://gateway.example.test/v1",
	}}
	if !gateway.AllowsHost("gateway.example.test") || gateway.AllowsHost("api.openai.com") {
		t.Errorf("an OpenAI credential pointed at a gateway scopes to %v, want the gateway only", gateway.EffectiveDomains())
	}
}

func TestAnOpenAICredentialProbesModelsUnderItsBaseURL(t *testing.T) {
	t.Parallel()
	recorder := &probeRecorder{}
	server := recorder.server(t, "/v1/models", "Bearer ollama")
	openAI, _ := credentials.Default().Get("openAiApi")

	fields := map[string]string{"apiKey": "ollama", "url": server.URL + "/v1/"}
	record := credentials.Record{Type: "openAiApi", Fields: fields}
	if _, err := credentials.RunTest(context.Background(), openAI, record, fields, endpointPolicy(t, server.URL)); err != nil {
		t.Fatalf("RunTest() = %v (path %q, auth %q), want the compatible endpoint's /models to answer",
			err, recorder.path, recorder.auth)
	}
}
