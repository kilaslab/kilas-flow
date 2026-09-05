package webhook_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// recordingHook counts each lifecycle call so a test can prove idempotence
// directly rather than inferring it.
type recordingHook struct {
	exists        bool
	checks        int
	creates       int
	deletes       int
	createErr     error
	deleteErr     error
	lastPublicURL string
}

func (hook *recordingHook) CheckExists(_ context.Context, ctx webhook.LifecycleContext) (bool, error) {
	hook.checks++
	hook.lastPublicURL = ctx.PublicURL
	return hook.exists, nil
}

func (hook *recordingHook) Create(_ context.Context, ctx webhook.LifecycleContext) error {
	hook.creates++
	hook.lastPublicURL = ctx.PublicURL
	return hook.createErr
}

func (hook *recordingHook) Delete(context.Context, webhook.LifecycleContext) error {
	hook.deletes++
	return hook.deleteErr
}

// routeReader stands in for the workflow store.
type routeReader struct {
	bindings []repository.WebhookBinding
	err      error
}

func (reader routeReader) WebhookRoutes(context.Context, repository.TenantScope, string) ([]repository.WebhookBinding, error) {
	return reader.bindings, reader.err
}

func coordinator(t *testing.T, hook webhook.TriggerLifecycle, bindings []repository.WebhookBinding) *webhook.Coordinator {
	t.Helper()
	registry := webhook.NewLifecycleRegistry()
	if hook != nil {
		if err := registry.Register("test.lifecycle", hook); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
	}
	return webhook.NewCoordinator(registry, routeReader{bindings: bindings},
		safehttp.DefaultPolicy(), nil, "https://flows.example.test", nil)
}

// TestActivationRegistersEachDeclaringTrigger is the half that makes p3's
// triggers possible: a bot receives nothing until it is told where to deliver.
func TestActivationRegistersEachDeclaringTrigger(t *testing.T) {
	hook := &recordingHook{}
	bindings := []repository.WebhookBinding{
		{NodeID: "telegram", NodeType: "kilasflow.telegramTrigger", Route: "abc123"},
		// A trigger that declares no lifecycle is left alone.
		{NodeID: "plain", NodeType: "kilasflow.webhook", Route: "def456"},
	}
	declared := map[string]string{"kilasflow.telegramTrigger": "test.lifecycle"}

	if _, err := coordinator(t, hook, bindings).Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
		t.Fatalf("Activated() error = %v", err)
	}
	if hook.creates != 1 {
		t.Errorf("Create ran %d times, want once — and only for the declaring trigger", hook.creates)
	}
	// The hook is told the public address, which is the whole point of it.
	if hook.lastPublicURL != "https://flows.example.test/webhook/abc123" {
		t.Errorf("public URL = %q, want the instance address plus the minted route", hook.lastPublicURL)
	}
}

// TestActivatingAnAlreadyActiveWorkflowRechecks is why all three of n8n's
// methods are kept rather than collapsing to two.
func TestActivatingAnAlreadyActiveWorkflowRechecks(t *testing.T) {
	hook := &recordingHook{exists: true}
	bindings := []repository.WebhookBinding{{NodeID: "t", NodeType: "trigger.type", Route: "abc"}}
	declared := map[string]string{"trigger.type": "test.lifecycle"}

	runner := coordinator(t, hook, bindings)
	for range 3 {
		if _, err := runner.Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
			t.Fatalf("Activated() error = %v", err)
		}
	}
	if hook.checks != 3 {
		t.Errorf("CheckExists ran %d times, want 3", hook.checks)
	}
	if hook.creates != 0 {
		t.Errorf("Create ran %d times on an already-registered trigger, want none", hook.creates)
	}
}

// TestARegistrationFailureIsNamedAndFatal keeps a half-registered workflow from
// looking active. The caller deactivates on this error.
func TestARegistrationFailureIsNamedAndFatal(t *testing.T) {
	hook := &recordingHook{createErr: errors.New("the bot token was rejected")}
	bindings := []repository.WebhookBinding{{NodeID: "telegram", NodeType: "trigger.type", Route: "abc"}}
	declared := map[string]string{"trigger.type": "test.lifecycle"}

	_, err := coordinator(t, hook, bindings).Activated(context.Background(), "tenant-a", "wf_1", declared)
	if err == nil {
		t.Fatal("a failed registration was not reported")
	}
	if !strings.Contains(err.Error(), "telegram") || !strings.Contains(err.Error(), "bot token") {
		t.Errorf("error = %v, want it to name the trigger and the remote failure", err)
	}
}

// TestATeardownFailureDoesNotBlockDeactivation is the mirror rule. A user
// deactivating must not be blocked by somebody else's service being down, and a
// stale registration delivers to a route that no longer resolves — a 404, not a
// leak.
func TestATeardownFailureDoesNotBlockDeactivation(t *testing.T) {
	hook := &recordingHook{deleteErr: errors.New("the service is unreachable")}
	bindings := []repository.WebhookBinding{{NodeID: "telegram", NodeType: "trigger.type", Route: "abc"}}
	declared := map[string]string{"trigger.type": "test.lifecycle"}

	// Returns nothing at all: there is no error path to block on.
	coordinator(t, hook, bindings).Deactivated(context.Background(), "tenant-a", "wf_1", declared)
	if hook.deletes != 1 {
		t.Errorf("Delete ran %d times, want once", hook.deletes)
	}
}

// TestAnUnboundLifecycleFailsAtStartup keeps a missing binding from becoming a
// workflow that activates and silently never registers.
func TestAnUnboundLifecycleFailsAtStartup(t *testing.T) {
	registry := webhook.NewLifecycleRegistry()
	if err := webhook.VerifyLifecycleBindings([]string{"telegram.setWebhook"}, registry); err == nil {
		t.Fatal("a node declaring an unregistered lifecycle was accepted at startup")
	}
	if err := registry.Register("telegram.setWebhook", &recordingHook{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := webhook.VerifyLifecycleBindings([]string{"telegram.setWebhook"}, registry); err != nil {
		t.Errorf("a registered lifecycle was rejected: %v", err)
	}
}

// TestExtractionReadsTheCatalogueNotANodeTypeName is the half that makes every
// p3 trigger possible.
//
// Exactly one node type could bind an inbound path and it was named at
// composition, so a Telegram or WAHA trigger could be registered, placed on a
// canvas, saved and activated — and would never receive a request. No error,
// just an active workflow that is unreachable.
func TestExtractionReadsTheCatalogueNotANodeTypeName(t *testing.T) {
	registry := node.NewRegistry()
	for _, definition := range []node.Definition{
		{
			Type: "pack.telegramTrigger", Version: workflow.V(1),
			DisplayName: "Telegram Trigger", Category: "Triggers", ExecutorID: "pack.exec",
			Group:   []node.NodeGroup{node.GroupTrigger},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			// A path parameter that is *not* called "path", and a fixed method:
			// a bot always receives POST and offers no choice.
			Webhook: &node.WebhookDeclaration{
				Name: "default", PathParameter: "chatPath", Method: "POST",
			},
		},
		{
			Type: "pack.notATrigger", Version: workflow.V(1),
			DisplayName: "Plain", Category: "Core", ExecutorID: "pack.exec2",
			Group:  []node.NodeGroup{node.GroupTransform},
			Inputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("Register(%s) error = %v", definition.Type, err)
		}
	}

	extract := webhook.Extract(registry, func(raw string) string { return strings.Trim(raw, "/") })
	triggers := extract(workflow.Document{
		Nodes: []workflow.Node{
			{ID: "tg", Name: "Telegram", Type: "pack.telegramTrigger", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"chatPath": "/bot-updates/"}},
			{ID: "plain", Name: "Plain", Type: "pack.notATrigger", TypeVersion: workflow.V(1)},
		},
	})

	if len(triggers) != 1 {
		t.Fatalf("extracted %d triggers, want 1: %#v", len(triggers), triggers)
	}
	if triggers[0].NodeID != "tg" || triggers[0].NodeType != "pack.telegramTrigger" {
		t.Errorf("trigger = %#v, want the pack's trigger", triggers[0])
	}
	// Read from the key the definition named, and normalised.
	if triggers[0].Path != "bot-updates" {
		t.Errorf("path = %q, want it read from chatPath and normalised", triggers[0].Path)
	}
	if triggers[0].Method != "POST" {
		t.Errorf("method = %q, want the declaration's fixed POST", triggers[0].Method)
	}
}

// TestExtractionIgnoresANodeTypeItCannotResolve keeps a document naming an
// unregistered type from producing a binding nothing can serve.
func TestExtractionIgnoresANodeTypeItCannotResolve(t *testing.T) {
	registry := node.NewRegistry()
	extract := webhook.Extract(registry, func(raw string) string { return strings.Trim(raw, "/") })
	triggers := extract(workflow.Document{
		Nodes: []workflow.Node{
			{ID: "x", Name: "Unknown", Type: "pack.neverRegistered", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"path": "orders"}},
		},
	})
	if len(triggers) != 0 {
		t.Errorf("extracted %#v, want nothing for an unregistered type", triggers)
	}
}
