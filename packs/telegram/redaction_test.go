package telegram_test

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The live reproduction from 2026-09-25: a sendMessage whose credential's
// baseUrl was a closed port failed with
// `Post "http://127.0.0.1:18999/bot<token>/sendMessage": …`. A transport error
// is Go's *url.Error, which prints the whole URL, and the bot token is in its
// path — so the error is cut to the scheme and host before it leaves the node.
func TestATransportFailureDoesNotCarryTheBotToken(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	closed := "http://" + listener.Addr().String()
	_ = listener.Close()

	set := install(t, localPolicy())
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	_, err = executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendMessage", "chatId": "0", "text": "x",
	}), workflow.NodeInput{}, engine.Request{Credentials: botCredential{baseURL: closed}})
	if err == nil {
		t.Fatal("a call to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "123:ABC") || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("error = %q, want no bot token and no path in it", err)
	}
	if !strings.Contains(err.Error(), closed) {
		t.Errorf("error = %q, want the scheme and host kept for diagnosis", err)
	}
}
