package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// TestWebhookRequireAuthSwitchIsWiredAtBoot pins the composition-root wiring of
// the webhook.require_auth deployment switch.
//
// The handler-level tests in internal/webhook prove RequireAuthentication and
// LogPosture work when called; nothing proved run() calls them with the loaded
// configuration. cmd/kilasflow/main.go is a named cross-ticket contention point,
// so a merge that drops the appended RequireAuthentication line used to leave
// every package green: KILASFLOW_WEBHOOK_REQUIRE_AUTH=true still loaded into
// cfg.Webhook.RequireAuth and the process still served unauthenticated
// deliveries. This test drives startWebhookHandler — the one function run()
// builds the handler through — and fails if either the switch or the posture
// line disappears from it.
func TestWebhookRequireAuthSwitchIsWiredAtBoot(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		var log bytes.Buffer
		handler := startWebhookHandler(processRoleBoth, webhookConfig(true),
			wiringBindings{}, wiringRunner{}, nil, nil, webhook.NewRegistry(),
			slog.New(slog.NewTextHandler(&log, nil)))

		// An open trigger must be refused outright by the deployment switch.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook/wiring-route", strings.NewReader(`{}`)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status with require_auth on = %d, want %d; the switch is not wired to the handler chain",
				rec.Code, http.StatusForbidden)
		}
		if !strings.Contains(log.String(), "require authentication") {
			t.Errorf("boot log = %q, want the requiring posture stated", log.String())
		}
	})

	t.Run("default", func(t *testing.T) {
		var log bytes.Buffer
		handler := startWebhookHandler(processRoleBoth, webhookConfig(false),
			wiringBindings{}, wiringRunner{}, nil, nil, webhook.NewRegistry(),
			slog.New(slog.NewTextHandler(&log, nil)))

		// The zero value has to mean off: an imported workflow keeps its old
		// answer, so the same open trigger is admitted. Asserting the exact
		// acknowledgement proves admission, where "not 403" would also accept a
		// 404 from a broken resolve or a 500 from a narrowed admission path.
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook/wiring-route", strings.NewReader(`{}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status with require_auth off = %d, want %d; the default posture refused the delivery",
				rec.Code, http.StatusOK)
		}
		var acknowledged struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &acknowledged); err != nil {
			t.Fatalf("decode acknowledgement %q: %v", rec.Body.String(), err)
		}
		if acknowledged.Message != "Workflow was started" {
			t.Fatalf("acknowledgement = %q, want the workflow-started body: the delivery was not admitted",
				rec.Body.String())
		}
		if !strings.Contains(log.String(), "accept unauthenticated deliveries") {
			t.Errorf("boot log = %q, want the open posture stated", log.String())
		}
	})

	// The posture line is claimed only for a process that serves the API. The
	// guard used to be an early return in run() that a worker role never passed;
	// it now lives in startWebhookHandler, so pin it in both directions: a
	// worker-only process must stay silent and an api process must still speak.
	t.Run("worker", func(t *testing.T) {
		var log bytes.Buffer
		startWebhookHandler(processRoleWorker, webhookConfig(false),
			wiringBindings{}, wiringRunner{}, nil, nil, webhook.NewRegistry(),
			slog.New(slog.NewTextHandler(&log, nil)))

		if log.String() != "" {
			t.Fatalf("boot log for a worker-only process = %q, want nothing: a worker serves no webhook surface",
				log.String())
		}
	})

	t.Run("api", func(t *testing.T) {
		var log bytes.Buffer
		startWebhookHandler(processRoleAPI, webhookConfig(false),
			wiringBindings{}, wiringRunner{}, nil, nil, webhook.NewRegistry(),
			slog.New(slog.NewTextHandler(&log, nil)))

		if !strings.Contains(log.String(), "accept unauthenticated deliveries") {
			t.Fatalf("boot log for an api process = %q, want the posture stated: the role gate swallowed it",
				log.String())
		}
	})
}

// webhookConfig is the loaded configuration with only the switch of interest set.
func webhookConfig(required bool) config.Config {
	var cfg config.Config
	cfg.Webhook.RequireAuth = required
	return cfg
}

// wiringBindings resolves every path to one open trigger, so the switch is the
// only thing that can refuse the request.
type wiringBindings struct{ repository.WebhookRepository }

func (wiringBindings) Resolve(context.Context, string, string) (repository.WebhookBinding, error) {
	return repository.WebhookBinding{
		Route:      "wiring-route",
		WorkflowID: "wf_wiring",
		NodeID:     "node-wiring",
		TenantID:   "tenant-wiring",
		NodeType:   "kilasflow.manual",
		Parameters: map[string]any{},
	}, nil
}

// wiringRunner accepts anything that reaches it, so a 403 can only come from
// the deployment switch and never from a queueing failure.
type wiringRunner struct{ webhook.Runner }

func (wiringRunner) QueueWebhook(context.Context, repository.WebhookBinding, json.RawMessage) (execution.Record, error) {
	return execution.Record{ID: "exec-wiring"}, nil
}
