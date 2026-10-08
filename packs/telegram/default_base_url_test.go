package telegram_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-epy2se: a Telegram credential saved with no base URL (the docs say to
// leave it empty) reads Telegram's own in `$credentials.baseUrl`. The egress
// policy here admits one unrelated host, so the refusal names where the call
// was going without the test ever reaching Telegram.
func TestACredentialWithNoBaseURLCallsTelegramsOwnBotAPI(t *testing.T) {
	t.Parallel()

	policy := safehttp.DefaultPolicy()
	policy.AllowedHosts = []string{"only.example.test"}
	set := install(t, policy)
	executor, _ := set.executors.Lookup(routing.ExecutorID)

	_, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendMessage", "chatId": "@my_channel", "text": "hi",
	}), workflow.NodeInput{}, engine.Request{Credentials: botCredential{baseURL: ""}})
	if err == nil || !strings.Contains(err.Error(), "api.telegram.org") {
		t.Fatalf("sendMessage error = %v, want the call aimed at api.telegram.org", err)
	}
}
