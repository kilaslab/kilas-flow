package nodes_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// closedLoopback is a loopback address nothing listens on, so a request to it
// fails with a transport error — Go's *url.Error, which prints the whole URL.
func closedLoopback(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

// The live reproduction from 2026-09-25: an httpQueryAuth credential on an HTTP
// Request node aimed at a closed port failed with
// `Get "http://127.0.0.1:18999/closed?api_key=<secret>": …`, and that text was
// stored as the execution's error and handed to any error branch.
func TestHTTPRequestTransportErrorDoesNotCarryAQueryCredential(t *testing.T) {
	t.Parallel()

	const secret = "QUERY-SECRET-4f1c"
	resolver := &stubCredentials{credential: engine.Credential{
		ID: "cred-1", Name: "Query key", Type: "httpQueryAuth",
		Fields: map[string]string{"name": "api_key", "value": secret},
	}}
	endpoint := closedLoopback(t)
	ir := httpNode(map[string]any{"method": "GET", "url": "http://" + endpoint + "/closed"})
	ir.Credentials = map[string]string{"httpQueryAuth": "cred-1"}

	_, err := nodes.NewHTTPExecutor(localPolicy()).Execute(context.Background(), ir, workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err == nil {
		t.Fatal("a request to a closed port succeeded")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error = %q, want the query withheld", err)
	}
	if !strings.Contains(err.Error(), "http://"+endpoint) {
		t.Errorf("error = %q, want the scheme and host kept for diagnosis", err)
	}
}

// Every Bot API call a Telegram trigger makes — registration, removal, and the
// polling loop's getUpdates, whose failures are logged at Warn on every retry —
// goes through one helper whose URL carries the bot token in its path. A
// transport failure there reaches the activation answer and the log with only
// the scheme and host.
func TestATelegramLifecycleTransportErrorDoesNotCarryTheBotToken(t *testing.T) {
	t.Parallel()

	closed := "http://" + closedLoopback(t)
	lifecycleContext := telegramLifecycleContext(t, localPolicy(), closed, map[string]any{"delivery": "polling"})
	err := nodes.NewTelegramLifecycle(nodes.NewTelegramPollers(context.Background(), localPolicy(), nil)).
		Create(context.Background(), lifecycleContext)
	if err == nil {
		t.Fatal("clearing the webhook against a closed port succeeded")
	}
	if strings.Contains(err.Error(), "123:ABC") || strings.Contains(err.Error(), "deleteWebhook\"") {
		t.Fatalf("error = %q, want no bot token in it", err)
	}
	if !strings.Contains(err.Error(), closed) {
		t.Errorf("error = %q, want the scheme and host kept", err)
	}
}
