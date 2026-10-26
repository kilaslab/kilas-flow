package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recording nothing here: the registry's own rendering is the test's subject,
// observed through the Observer methods the engine and webhook boundary call.
func TestTheRegistryRendersTheFixedSeries(t *testing.T) {
	registry := NewRegistry(TenantLabelOff, 0)

	registry.ExecutionClaimed("acme", "webhook", 25*time.Millisecond)
	registry.ExecutionStarted("acme", "webhook")
	registry.NodeRun("acme", "kilasflow.set", 5*time.Millisecond)
	registry.ExecutionSettled("acme", "webhook", "succeeded")
	registry.WebhookRequest("acme", WebhookAccepted)

	body := registry.Render(context.Background())
	for _, want := range []string{
		"# TYPE kilasflow_executions_started_total counter",
		`kilasflow_executions_started_total{trigger="webhook"} 1`,
		"# TYPE kilasflow_executions_finished_total counter",
		`kilasflow_executions_finished_total{trigger="webhook",status="succeeded"} 1`,
		"# TYPE kilasflow_execution_claim_seconds histogram",
		`kilasflow_execution_claim_seconds_bucket{trigger="webhook",le="0.025"} 1`,
		`kilasflow_execution_claim_seconds_bucket{trigger="webhook",le="+Inf"} 1`,
		`kilasflow_execution_claim_seconds_count{trigger="webhook"} 1`,
		"# TYPE kilasflow_node_run_seconds histogram",
		`kilasflow_node_run_seconds_bucket{node_type="kilasflow.set",le="0.005"} 1`,
		"# TYPE kilasflow_webhook_requests_total counter",
		`kilasflow_webhook_requests_total{outcome="accepted"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, body)
		}
	}

	// Every HELP line names its family, and no label-less series appears: a
	// tenant-label-off registry narrows the label set rather than rendering
	// empty values.
	if lines := strings.Count(body, "# HELP "); lines != 5 {
		t.Errorf("HELP lines = %d, want one per fixed family", lines)
	}
	if strings.Contains(body, `tenant=`) {
		t.Errorf("a tenant label appeared with the mode off:\n%s", body)
	}
}

// The tenant label is there when the operator asks for it, per series the
// fixed set declares it on.
func TestTheTenantLabelIsRecordedWhenOn(t *testing.T) {
	registry := NewRegistry(TenantLabelOn, 0)
	registry.ExecutionStarted("acme", "manual")
	registry.ExecutionStarted("globex", "manual")

	body := registry.Render(context.Background())
	for _, want := range []string{
		`kilasflow_executions_started_total{trigger="manual",tenant="acme"} 1`,
		`kilasflow_executions_started_total{trigger="manual",tenant="globex"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, body)
		}
	}
}

// The top mode keeps a label for the busiest tenants and folds the rest into
// tenant="other", so the number of series a scrape carries is bounded however
// many tenants the deployment serves.
func TestTheTopModeFoldsTheRestIntoOther(t *testing.T) {
	registry := NewRegistry(TenantLabelTop, 1)
	for range 3 {
		registry.ExecutionStarted("acme", "manual")
	}
	registry.ExecutionStarted("globex", "manual")
	registry.ExecutionStarted("initech", "manual")

	body := registry.Render(context.Background())
	for _, want := range []string{
		`kilasflow_executions_started_total{trigger="manual",tenant="acme"} 3`,
		`kilasflow_executions_started_total{trigger="manual",tenant="other"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exposition is missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `tenant="globex"`) || strings.Contains(body, `tenant="initech"`) {
		t.Errorf("a folded tenant kept its own label:\n%s", body)
	}
}

// Label values with characters the format gives meaning to are escaped, and
// so is a HELP line.
func TestLabelValuesAndHelpAreEscaped(t *testing.T) {
	registry := NewRegistry(TenantLabelOn, 0)
	registry.WebhookRequest("ac\"me\n", WebhookRefused)

	body := registry.Render(context.Background())
	if !strings.Contains(body, `outcome="refused",tenant="ac\"me\n"`) {
		t.Errorf("the tenant label value was not escaped:\n%s", body)
	}
}

// A gauge whose collector cannot answer is left out of that scrape: absent is
// honest, a zero would read as "no work queued" on a database that could not
// be reached.
func TestAFailingGaugeCollectorIsOmitted(t *testing.T) {
	registry := NewRegistry(TenantLabelOff, 0)
	if err := registry.RegisterGauge("kilasflow_test_depth", "Depth.", func(context.Context) (float64, error) {
		return 3, nil
	}); err != nil {
		t.Fatalf("RegisterGauge() error = %v", err)
	}
	if err := registry.RegisterGauge("kilasflow_test_broken", "Broken.", func(context.Context) (float64, error) {
		return 0, errors.New("the database is unreachable")
	}); err != nil {
		t.Fatalf("RegisterGauge(broken) error = %v", err)
	}
	if err := registry.RegisterGauge("kilasflow_test_depth", "Twice.", func(context.Context) (float64, error) {
		return 0, nil
	}); err == nil {
		t.Error("RegisterGauge(twice) = nil error, want the duplicate named")
	}

	body := registry.Render(context.Background())
	if !strings.Contains(body, "kilasflow_test_depth 3") {
		t.Errorf("the healthy gauge is missing:\n%s", body)
	}
	if strings.Contains(body, "kilasflow_test_broken") {
		t.Errorf("the failing gauge was rendered anyway:\n%s", body)
	}
}

// The handler answers the text exposition format, and a listener without a
// registry still answers — with the empty exposition, so a scraper sees a
// dead endpoint rather than a connection refused.
func TestTheHandlerServesTheExposition(t *testing.T) {
	recorder := writeRequest(Handler(nil))
	if recorder.Body.String() != "" {
		t.Errorf("a nil registry rendered %q, want the empty exposition", recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want the text exposition format", contentType)
	}

	registry := NewRegistry(TenantLabelOff, 0)
	registry.ExecutionStarted("acme", "manual")
	recorder = writeRequest(Handler(registry))
	if !strings.Contains(recorder.Body.String(), `kilasflow_executions_started_total{trigger="manual"} 1`) {
		t.Errorf("the handler served %q, want the observed series", recorder.Body.String())
	}
}

// writeRequest drives one GET against the handler, the request a scraper
// makes.
func writeRequest(handler http.Handler) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder
}
