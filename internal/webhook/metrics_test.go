package webhook_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/metrics"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// outcomeRecorder is the metrics seam as a test double: one entry per
// reported webhook request, tenant and outcome together.
type outcomeRecorder struct {
	mu       sync.Mutex
	reported []string
}

func (recorder *outcomeRecorder) ExecutionClaimed(string, string, time.Duration) {}
func (recorder *outcomeRecorder) ExecutionStarted(string, string)                {}
func (recorder *outcomeRecorder) ExecutionSettled(string, string, string)        {}
func (recorder *outcomeRecorder) NodeRun(string, string, time.Duration)          {}

func (recorder *outcomeRecorder) WebhookRequest(tenant, outcome string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.reported = append(recorder.reported, tenant+" "+outcome)
}

func (recorder *outcomeRecorder) outcomes() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.reported...)
}

// Every answer the surface gives is one of the three outcomes, and each
// request is counted exactly once — an operator's alert reads these counts,
// so a path that counted twice or not at all would be a lie either way.
func TestEveryWebhookAnswerIsCountedOnce(t *testing.T) {
	recorder := &outcomeRecorder{}
	base := newHarness(t)
	base.handler = webhook.NewHandler(base.workflows, base.runner, base.credentials, nil, webhook.DefaultLimits()).
		WithTriggers(webhook.NewRegistry()).
		WithMetrics(recorder)

	// An accepted delivery: a webhook trigger in immediate mode.
	accepted := base.activate(t, webhookDocument("Accepted", map[string]any{
		"path": "counted", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		base.handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		return response
	}
	if response := request(http.MethodPost, base.url(t, accepted), `{}`); response.Code != http.StatusOK {
		t.Fatalf("the accepted delivery answered %d (body: %s)", response.Code, response.Body)
	}

	// A refused one: no route is bound to this path. A well-formed route that
	// was never minted, so it cannot be confused with a malformed one.
	request(http.MethodPost, "/webhook/0123456789abcdef0123456789abcdef", `{}`)

	// A filtered one: the bot option acknowledges without running. A crawler
	// user agent against a trigger with ignoreBots on is the configuration
	// the option exists for.
	filtered := base.activate(t, webhookDocument("Filtered", map[string]any{
		"path": "bots", "httpMethod": http.MethodPost,
		"options": map[string]any{"ignoreBots": true},
	}))
	crawler := httptest.NewRequest(http.MethodPost, base.url(t, filtered), strings.NewReader(`{}`))
	crawler.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0")
	crawlerResponse := httptest.NewRecorder()
	base.handler.ServeHTTP(crawlerResponse, crawler)
	if crawlerResponse.Code != http.StatusOK {
		t.Fatalf("the bot delivery answered %d, want the filtered acknowledgement", crawlerResponse.Code)
	}

	outcomes := recorder.outcomes()
	want := []string{
		base.tenant.ID + " " + metrics.WebhookAccepted,
		// An unroutable request belongs to no tenant: the tenant label is
		// empty rather than guessed.
		" " + metrics.WebhookRefused,
		base.tenant.ID + " " + metrics.WebhookFiltered,
	}
	if len(outcomes) != len(want) {
		t.Fatalf("the reported outcomes = %v, want %v", outcomes, want)
	}
	for index, entry := range want {
		if outcomes[index] != entry {
			t.Errorf("outcomes[%d] = %q, want %q", index, outcomes[index], entry)
		}
	}
}

// A retried delivery that the dedupe answers with the original outcome counts
// as accepted, not filtered: the dedupe doing its job is a success, where a
// filter is a choice the trigger made.
func TestADeduplicatedDeliveryCountsAsAccepted(t *testing.T) {
	recorder := &outcomeRecorder{}
	base := newHarness(t)
	base.handler = webhook.NewHandler(base.workflows, base.runner, base.credentials, nil, webhook.DefaultLimits()).
		WithTriggers(webhook.NewRegistry()).
		WithMetrics(recorder)

	deduped := base.activate(t, webhookDocument("Deduped", map[string]any{
		"path": "deduped", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"deliveryIdHeader": "X-Delivery-Id",
	}))
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, base.url(t, deduped), strings.NewReader(`{}`))
		request.Header.Set("X-Delivery-Id", "delivery-1")
		response := httptest.NewRecorder()
		base.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("the delivery answered %d (body: %s)", response.Code, response.Body)
		}
	}

	outcomes := recorder.outcomes()
	if len(outcomes) != 2 || outcomes[0] != outcomes[1] || !strings.HasSuffix(outcomes[0], " "+metrics.WebhookAccepted) {
		t.Fatalf("the reported outcomes = %v, want two accepted", outcomes)
	}
}

// A handler built with no observer behaves exactly as before: the counts are
// a seam, not a step in the path.
func TestAHandlerWithoutAnObserverChangesNothing(t *testing.T) {
	base := newHarness(t)
	uncounted := base.activate(t, webhookDocument("Uncounted", map[string]any{
		"path": "uncounted", "httpMethod": http.MethodPost, "responseMode": "immediate",
	}))

	response := httptest.NewRecorder()
	base.handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, base.url(t, uncounted), strings.NewReader(`{}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want the ordinary acknowledgement (body: %s)", response.Code, response.Body)
	}
	if base.queuedCount() != 1 {
		t.Errorf("queued = %d, want the delivery to have run", base.queuedCount())
	}
}
