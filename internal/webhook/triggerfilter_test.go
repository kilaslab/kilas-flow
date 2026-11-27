package webhook_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/metrics"
	"github.com/kilaslab/kilas-flow/internal/triggerfilter"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// channelOnly is a trigger filter admitting deliveries whose body names one
// channel, written the way the editor saves an IF condition.
func channelOnly(channel string) map[string]any {
	return map[string]any{
		"combinator": "and",
		"options":    map[string]any{"caseSensitive": true, "typeValidation": "loose"},
		"conditions": []any{
			map[string]any{
				"leftValue":  map[string]any{"mode": "expression", "value": "{{ $json.body.channel }}"},
				"rightValue": channel,
				"operator":   map[string]any{"type": "string", "operation": "equals"},
			},
		},
	}
}

// A delivery the author's trigger filter rejects is acknowledged 200
// "filtered", creates no execution, and is counted as filtered; one it admits
// queues a run as before.
func TestTriggerFilterDropsNonMatchingDeliveriesBeforeQueueing(t *testing.T) {
	recorder := &outcomeRecorder{}
	base := newHarness(t)
	base.handler = webhook.NewHandler(base.workflows, base.runner, base.credentials, nil, webhook.DefaultLimits()).
		WithTriggers(webhook.NewRegistry()).
		WithMetrics(recorder)

	stored := base.activate(t, webhookDocument("Filtered by channel", map[string]any{
		"path": "inbound", "httpMethod": http.MethodPost, "responseMode": "immediate",
		triggerfilter.ParameterKey: channelOnly("whatsapp"),
	}))
	deliver := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, base.url(t, stored), strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		base.handler.ServeHTTP(response, request)
		return response
	}

	rejected := deliver(`{"channel":"email","text":"hi"}`)
	if rejected.Code != http.StatusOK {
		t.Fatalf("the filtered delivery answered %d (body: %s), want 200", rejected.Code, rejected.Body)
	}
	var answer map[string]any
	if err := json.Unmarshal(rejected.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the filtered answer: %v (body: %s)", err, rejected.Body)
	}
	if answer["filtered"] != true {
		t.Fatalf("the filtered answer = %v, want filtered: true", answer)
	}
	if reason, _ := answer["reason"].(string); !strings.Contains(reason, "trigger filter") {
		t.Fatalf("the filtered reason = %q, want it to name the trigger filter", reason)
	}
	if queued := base.queuedCount(); queued != 0 {
		t.Fatalf("a filtered delivery queued %d executions, want none", queued)
	}

	if admitted := deliver(`{"channel":"whatsapp","text":"hi"}`); admitted.Code != http.StatusOK {
		t.Fatalf("the matching delivery answered %d (body: %s), want 200", admitted.Code, admitted.Body)
	}
	if queued := base.queuedCount(); queued != 1 {
		t.Fatalf("the matching delivery queued %d executions, want exactly one", queued)
	}

	want := []string{
		base.tenant.ID + " " + metrics.WebhookFiltered,
		base.tenant.ID + " " + metrics.WebhookAccepted,
	}
	outcomes := recorder.outcomes()
	if len(outcomes) != len(want) {
		t.Fatalf("the reported outcomes = %v, want %v", outcomes, want)
	}
	for index, entry := range want {
		if outcomes[index] != entry {
			t.Errorf("outcomes[%d] = %q, want %q", index, outcomes[index], entry)
		}
	}
}
