package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// tinyLimiter allows a fixed number of spends per (tenant, bucket) and
// refuses the rest, the behaviour a one-per-minute allowance produces on its
// second request.
type tinyLimiter struct {
	mu     sync.Mutex
	spends map[string]int
}

func (limiter *tinyLimiter) Allow(_ context.Context, tenantID, bucket string) (bool, time.Duration, error) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.spends == nil {
		limiter.spends = map[string]int{}
	}
	key := tenantID + "\x00" + bucket
	limiter.spends[key]++
	if limiter.spends[key] > 1 {
		return false, 7 * time.Second, nil
	}
	return true, 0, nil
}

// A tenant past its webhook allowance is answered 429 with Retry-After and
// the named problem code, before any execution or delivery claim exists.
func TestAWebhookPastItsAllowanceIsRefusedBeforeAnythingRuns(t *testing.T) {
	limiter := &tinyLimiter{}
	base := newHarness(t)
	base.handler = webhook.NewHandler(base.workflows, base.runner, base.credentials, nil, webhook.DefaultLimits()).
		WithTriggers(webhook.NewRegistry()).
		WithRateLimiter(limiter)
	bound := base.activate(t, webhookDocument("Throttled", map[string]any{
		"path": "throttled", "httpMethod": http.MethodPost, "responseMode": "immediate",
		"deliveryIdHeader": "X-Delivery-Id",
	}))

	deliver := func(deliveryID string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, base.url(t, bound), strings.NewReader(`{}`))
		request.Header.Set("X-Delivery-Id", deliveryID)
		response := httptest.NewRecorder()
		base.handler.ServeHTTP(response, request)
		return response
	}

	// The first delivery is admitted and runs.
	if first := deliver("d-1"); first.Code != http.StatusOK {
		t.Fatalf("the first delivery answered %d (body: %s)", first.Code, first.Body)
	}
	if base.queuedCount() != 1 {
		t.Fatalf("queued = %d after the first delivery, want 1", base.queuedCount())
	}

	// The second is refused: 429, Retry-After in whole seconds, the named
	// code, and no execution behind it.
	refused := deliver("d-2")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the second delivery answered %d, want 429 (body: %s)", refused.Code, refused.Body)
	}
	if retryAfter := refused.Header().Get("Retry-After"); retryAfter != strconv.Itoa(7) {
		t.Errorf("Retry-After = %q, want the limiter's whole seconds", retryAfter)
	}
	if body := refused.Body.String(); !strings.Contains(body, `"code":"rate_limited"`) {
		t.Errorf("the refusal body = %s, want the named problem code", body)
	}
	if base.queuedCount() != 1 {
		t.Fatalf("queued = %d after the refused delivery, want still 1: the refusal cost the queue nothing", base.queuedCount())
	}

	// No delivery claim was taken either: the same delivery id, retried once
	// the allowance refills, runs rather than collapsing with a claim the
	// refusal never made.
	if claims := countDeliveryClaims(t, base); claims != 1 {
		t.Fatalf("delivery claims = %d, want only the admitted one's", claims)
	}
}

// countDeliveryClaims counts the delivery rows the tenant has claimed: the
// refusal-before-any-claim assertion reads what is stored, not what the
// handler reports.
func countDeliveryClaims(t *testing.T, base harness) int {
	t.Helper()
	var claims int64
	if err := base.db.Raw("SELECT COUNT(*) FROM webhook_deliveries").Scan(&claims).Error; err != nil {
		t.Fatalf("count delivery claims: %v", err)
	}
	return int(claims)
}
