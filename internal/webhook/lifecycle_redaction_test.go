package webhook_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// BUG-asdh5q: a lifecycle target URL can carry a secret — Telegram's setWebhook
// wants the bot token in the path — and a transport failure is Go's *url.Error,
// which prints the whole URL. Activation turned that text into its 502 detail
// and deactivation logged it at Warn. Both now carry the scheme and host alone.
func TestALifecycleTransportErrorCarriesOnlyTheSchemeAndHost(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	endpoint := listener.Addr().String()
	_ = listener.Close()
	closed := "http://" + endpoint

	const token = "k-lifecycle-7d3a"
	descriptor := &webhook.RequestDescriptor{
		Method:         "POST",
		URL:            "{{ .baseUrl }}/bot{{ .apiKey }}/setWebhook?token={{ .apiKey }}",
		CredentialType: "wahaApi",
	}
	lifecycle := webhook.RequestLifecycle{Set: descriptor, Remove: descriptor}

	registry := webhook.NewLifecycleRegistry()
	if err := registry.Register("test.lifecycle", lifecycle); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{endpoint}
	var logged bytes.Buffer
	bindings := []repository.WebhookBinding{{
		NodeID: "trigger", NodeType: "pack.stubTrigger", Route: "abc123",
		Parameters: map[string]any{"$credentials": map[string]any{"wahaApi": "cred-1"}},
	}}
	coordinator := webhook.NewCoordinator(registry, routeReader{bindings: bindings}, policy,
		func(string) engine.CredentialResolver { return listCredential{baseURL: closed, apiKey: token} },
		"https://flows.example.test", slog.New(slog.NewTextHandler(&logged, nil)))
	declared := map[string]string{"pack.stubTrigger": "test.lifecycle"}

	_, err = coordinator.Activated(context.Background(), "tenant-a", "wf_1", declared)
	if err == nil {
		t.Fatal("activation against a closed port succeeded")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "setWebhook") {
		t.Errorf("activation error = %q, want no path or query in it", err)
	}
	if !strings.Contains(err.Error(), closed) {
		t.Errorf("activation error = %q, want the scheme and host kept", err)
	}

	coordinator.Deactivated(context.Background(), "tenant-a", "wf_1", declared)
	if !strings.Contains(logged.String(), "could not unregister") {
		t.Fatalf("log = %q, want the failed teardown logged", logged.String())
	}
	if strings.Contains(logged.String(), token) || strings.Contains(logged.String(), "setWebhook") {
		t.Errorf("log = %q, want no path or query in it", logged.String())
	}
}
