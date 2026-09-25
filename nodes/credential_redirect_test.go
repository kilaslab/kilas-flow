package nodes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// BUG-0bzsa1, live evidence from 2026-09-25: an httpHeaderAuth request to a
// redirecting stub sent X-Api-Key on the first hop, and the cross-host hop was
// refused only because its target was loopback. Go drops Authorization and
// Cookie on a cross-host redirect and forwards every other header, so a
// credential that names no domains carried its header to whatever host the
// redirect chose. Each header-placed type is driven through the HTTP node with
// redirects followed, against a destination on another hostname.
func TestAHeaderPlacedSecretDoesNotFollowACrossHostRedirect(t *testing.T) {
	t.Parallel()

	for _, credential := range []engine.Credential{
		{ID: "cred-1", Name: "Header", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Api-Key", "value": "HEADER-SECRET"}},
		{ID: "cred-1", Name: "Custom", Type: "httpCustomAuth", Fields: map[string]string{"json": `{"headers":{"X-Custom-Token":"CUSTOM-SECRET"}}`}},
		{ID: "cred-1", Name: "WAHA", Type: "wahaApi", Fields: map[string]string{"baseUrl": "http://127.0.0.1", "apiKey": "WAHA-SECRET"}},
	} {
		t.Run(credential.Type, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var received http.Header
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				received = r.Header.Clone()
				mu.Unlock()
				_, _ = w.Write([]byte(`{"stolen":true}`))
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, strings.Replace(destination.URL, "127.0.0.1", "localhost", 1)+"/stolen", http.StatusFound)
			}))
			defer origin.Close()

			ir := httpNode(map[string]any{"method": "GET", "url": origin.URL + "/start", "followRedirects": true, "fullResponse": true})
			ir.Credentials = map[string]string{credential.Type: "cred-1"}
			output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), ir, workflow.NodeInput{},
				engine.Request{Credentials: &stubCredentials{credential: credential}})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if received != nil {
				t.Errorf("the redirect target on another host received the request: %v", received)
			}
			if output[0][0].JSON["statusCode"] != float64(http.StatusFound) {
				t.Errorf("statusCode = %#v, want the 302 handed back rather than followed", output[0][0].JSON["statusCode"])
			}
		})
	}
}

// The bound is on where a credential may go, not a ban on redirects: a
// service that moves its own endpoint on the same host keeps working with a
// credential that names no domains.
func TestACredentialStillFollowsASameHostRedirect(t *testing.T) {
	t.Parallel()

	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		seen = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	ir := httpNode(map[string]any{"method": "GET", "url": server.URL + "/old", "followRedirects": true})
	ir.Credentials = map[string]string{"httpHeaderAuth": "cred-1"}
	output, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), ir, workflow.NodeInput{},
		engine.Request{Credentials: &stubCredentials{credential: engine.Credential{
			ID: "cred-1", Name: "Header", Type: "httpHeaderAuth", Fields: map[string]string{"name": "X-Api-Key", "value": "k-1"},
		}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["ok"] != true || seen != "k-1" {
		t.Errorf("item = %#v, header %q; want the same-host redirect followed with the credential", output[0][0].JSON, seen)
	}
}

// A Telegram trigger's registration calls sent the bot token to the
// credential's base URL with no host check, no redirect scope and no type
// check. They now go through the engine's checks, so a credential scoped
// elsewhere, or one of another type, never reaches the Bot API call.
func TestATelegramLifecycleAppliesTheEnginesCredentialChecks(t *testing.T) {
	t.Parallel()

	calls := 0
	policy, server := telegramAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})
	bot := func(credentialType string, domains ...string) *stubCredentials {
		return &stubCredentials{credential: engine.Credential{
			ID: "cred-1", Name: "Bot", Type: credentialType, AllowedDomains: domains,
			Fields: map[string]string{"accessToken": "123:ABC", "baseUrl": server.URL},
		}}
	}

	for _, refused := range []struct {
		resolver *stubCredentials
		want     string
	}{
		{bot(nodes.TelegramCredentialType, "api.telegram.org"), "not allowed for host"},
		{bot("httpHeaderAuth"), "not " + nodes.TelegramCredentialType},
	} {
		lifecycleContext := telegramLifecycleContext(t, policy, server.URL, nil)
		lifecycleContext.Credentials = refused.resolver
		err := nodes.NewTelegramLifecycle(nil).Create(context.Background(), lifecycleContext)
		if err == nil || !strings.Contains(err.Error(), refused.want) {
			t.Errorf("Create() = %v, want a refusal naming %q", err, refused.want)
		}
	}
	if calls != 0 {
		t.Fatalf("the Bot API was called %d times despite the refusals", calls)
	}

	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, nil)
	lifecycleContext.Credentials = bot(nodes.TelegramCredentialType, "127.0.0.1")
	if err := nodes.NewTelegramLifecycle(nil).Create(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Create() with an in-scope credential error = %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want the one registration", calls)
	}
}
