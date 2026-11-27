package outbound_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/outbound"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The section refuses what could never be delivered and accepts the four
// events this build sends.
func TestNotificationSettingsValidate(t *testing.T) {
	registry := tenantsettings.NewRegistry()
	if err := outbound.RegisterSettings(registry); err != nil {
		t.Fatalf("RegisterSettings() error = %v", err)
	}
	accepted := `{"url":"https://host.example/kilasflow","events":["execution.failed","execution.completed","workflow.activated","workflow.deactivated"]}`
	if _, err := registry.Apply(nil, map[string]json.RawMessage{outbound.SettingsSection: json.RawMessage(accepted)}); err != nil {
		t.Fatalf("Apply(valid) error = %v", err)
	}
	for name, section := range map[string]string{
		"relative URL":       `{"url":"/hooks"}`,
		"ftp URL":            `{"url":"ftp://host.example/hooks"}`,
		"credentials in URL": `{"url":"https://user:pass@host.example/hooks"}`,
		"unknown event":      `{"url":"https://host.example/hooks","events":["execution.cancelled"]}`,
		"unknown field":      `{"url":"https://host.example/hooks","secret":"x"}`,
	} {
		if _, err := registry.Apply(nil, map[string]json.RawMessage{outbound.SettingsSection: json.RawMessage(section)}); err == nil {
			t.Errorf("%s: Apply(%s) succeeded, want it refused", name, section)
		}
	}
}

// The router answers from the settings document alone: no section or no URL
// is no subscription, an endpoint with no events hears failures only, and a
// listed event is the whole subscription.
func TestNotificationRouterReadsTheSettingsDocument(t *testing.T) {
	route := outbound.Router()
	cases := []struct {
		name     string
		settings string
		event    string
		want     bool
	}{
		{"no document", ``, repository.OutboundExecutionFailed, false},
		{"another section only", `{"rateLimit":{"runPerMinute":5}}`, repository.OutboundExecutionFailed, false},
		{"no URL", `{"notifications":{"events":["execution.failed"]}}`, repository.OutboundExecutionFailed, false},
		{"default hears failures", `{"notifications":{"url":"https://host.example/h"}}`, repository.OutboundExecutionFailed, true},
		{"default is silent on success", `{"notifications":{"url":"https://host.example/h"}}`, repository.OutboundExecutionCompleted, false},
		{"listed event", `{"notifications":{"url":"https://host.example/h","events":["workflow.deactivated"]}}`, repository.OutboundWorkflowDeactivated, true},
		{"unlisted event", `{"notifications":{"url":"https://host.example/h","events":["workflow.deactivated"]}}`, repository.OutboundExecutionFailed, false},
		{"invalid section", `{"notifications":{"url":"ftp://host.example/h"}}`, repository.OutboundExecutionFailed, false},
		{"malformed document", `{"notifications":`, repository.OutboundExecutionFailed, false},
	}
	for _, tc := range cases {
		endpoint, ok := route("tenant-a", tc.event, []byte(tc.settings))
		if ok != tc.want {
			t.Errorf("%s: routed = %v, want %v", tc.name, ok, tc.want)
		}
		if ok && endpoint != "https://host.example/h" {
			t.Errorf("%s: endpoint = %q", tc.name, endpoint)
		}
	}
}

// Each tenant's secret is derived from the deployment key and differs from
// every other tenant's; no key is no secret.
func TestTenantSecretsAreDerivedPerTenant(t *testing.T) {
	key := []byte("deployment-key")
	first, second := outbound.TenantSecret(key, "tenant-a"), outbound.TenantSecret(key, "tenant-b")
	if first == "" || first == second || first != outbound.TenantSecret(key, "tenant-a") {
		t.Fatalf("secrets = %q, %q: want stable and distinct per tenant", first, second)
	}
	if secret, err := outbound.DerivedSecrets(nil)("tenant-a"); err != nil || secret != "" {
		t.Fatalf("DerivedSecrets(no key) = (%q, %v), want no secret", secret, err)
	}
	if secret, _ := outbound.DerivedSecrets(key)("tenant-a"); secret != first {
		t.Fatalf("DerivedSecrets(key) = %q, want %q", secret, first)
	}
}

// End to end: a tenant names its endpoint in its settings, a run fails, and
// the endpoint receives execution.failed signed with that tenant's secret,
// verifiable by the inbound standardWebhooks scheme. The run that succeeds
// sends nothing, because the tenant did not ask for completions.
func TestTenantNotificationReachesTheConfiguredEndpoint(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	key := []byte("deployment-notifications-key")
	recv := newReceiver(t, func(int) int { return http.StatusOK })
	auth := repository.NewAuthStore(db.DB)
	const tenantID = "tenant-notified"
	if _, err := auth.CreateTenant(ctx, tenantID, "Notified"); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	if _, err := auth.UpdateTenantSettings(ctx, tenantID, func([]byte) ([]byte, error) {
		return []byte(`{"notifications":{"url":"` + recv.server.URL + `/kilasflow"}}`), nil
	}); err != nil {
		t.Fatalf("UpdateTenantSettings() error = %v", err)
	}
	tenant := repository.TenantScope{ID: tenantID}
	if _, err := repository.NewWorkflowStore(db.DB).SaveDraft(ctx, tenant, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_notified", Name: "Notified",
		Nodes:       []workflow.Node{{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	executions := repository.NewExecutionStore(db.DB).WithOutbox(outbound.Router())
	var failedID string
	for _, status := range []execution.Status{execution.StatusFailed, execution.StatusSucceeded} {
		if _, err := executions.QueueManualLatest(ctx, tenant, "wf_notified", manualCatalog, "", nil); err != nil {
			t.Fatalf("QueueManualLatest() error = %v", err)
		}
		claimed, _, found, err := executions.ClaimNext(ctx, "worker", time.Now().Add(time.Minute))
		if err != nil || !found {
			t.Fatalf("ClaimNext() = (%v, %v)", found, err)
		}
		finished := time.Now().UTC()
		claimed.Status, claimed.FinishedAt = status, &finished
		claimed.Output, claimed.Error = json.RawMessage(`null`), json.RawMessage(`{"code":"node.failed","message":"boom"}`)
		if status == execution.StatusSucceeded {
			claimed.Error = json.RawMessage(`null`)
		} else {
			failedID = claimed.ID
		}
		if _, err := executions.UpdateRuntime(ctx, tenant, claimed); err != nil {
			t.Fatalf("UpdateRuntime() error = %v", err)
		}
	}

	dispatcher, err := outbound.New(outbound.Options{
		Store: repository.NewOutboxStore(db.DB), Secret: outbound.DerivedSecrets(key),
		Policy: policyFor(t, recv.server.URL), WorkerID: "dispatcher", Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if attempted, err := dispatcher.Drain(ctx); err != nil || attempted != 1 {
		t.Fatalf("Drain() = (%d, %v), want the one failure delivered", attempted, err)
	}
	if recv.count() != 1 {
		t.Fatalf("receiver saw %d requests, want 1", recv.count())
	}
	var body struct {
		Type string `json:"type"`
		Data struct {
			ExecutionID string `json:"executionId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recv.bodies[0], &body); err != nil || body.Type != repository.OutboundExecutionFailed || body.Data.ExecutionID != failedID {
		t.Fatalf("delivered body = %s (%v), want execution.failed for %s", recv.bodies[0], err, failedID)
	}
	verify := webhook.HMACSchemeVerifier(webhook.HMACScheme{
		Header: "webhook-signature", Algorithm: "sha256", Encoding: "base64", Prefix: "v1,",
		SigningString: "{{ .timestamp }}.{{ .body }}", TimestampHeader: "webhook-timestamp", Tolerance: 5 * time.Minute,
	}, func(webhook.Delivery) (string, error) { return outbound.TenantSecret(key, tenantID), nil })
	request := httptest.NewRequest(http.MethodPost, "/kilasflow", strings.NewReader(string(recv.bodies[0])))
	request.Header = recv.got[0]
	if err := verify(webhook.Delivery{Request: request, RawBody: recv.bodies[0]}); err != nil {
		t.Fatalf("the delivery does not verify with the tenant's derived secret: %v", err)
	}
	if rows := logOf(t, db, tenantID); len(rows) != 1 || rows[0].Status != repository.OutboundDelivered {
		t.Fatalf("delivery log = %#v, want one delivered row", rows)
	}
}
