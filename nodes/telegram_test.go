package nodes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func telegramDefinition(t *testing.T) node.Definition {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Get(nodes.TelegramTriggerNodeType, workflow.V(1))
	if !found {
		t.Fatal("the Telegram trigger is not registered")
	}
	return definition
}

// An imported workflow lands on this form, so the option values are n8n's own
// and in n8n's order. Get one wrong and the imported node selects nothing.
func TestTheTriggerOnListMatchesTheOneImportedWorkflowsCarry(t *testing.T) {
	t.Parallel()

	definition := telegramDefinition(t)
	var updates node.PropertyDefinition
	fields := map[string]node.PropertyDefinition{}
	for _, parameter := range definition.Parameters {
		if parameter.Key == "updates" {
			updates = parameter
		}
		if parameter.Key == "additionalFields" {
			for _, nested := range parameter.Fields {
				fields[nested.Key] = nested
			}
		}
	}
	if updates.Kind != node.PropertyMultiOptions {
		t.Fatalf("Trigger On is %q, want a multi-select", updates.Kind)
	}
	want := []string{
		"*", "message", "edited_message", "channel_post", "edited_channel_post",
		"callback_query", "inline_query", "chosen_inline_result",
		"my_chat_member", "chat_member", "chat_join_request",
		"poll", "poll_answer", "pre_checkout_query", "shipping_query",
		"message_reaction", "message_reaction_count", "chat_boost", "removed_chat_boost",
		"business_connection", "business_message", "edited_business_message",
		"deleted_business_messages", "purchased_paid_media",
	}
	if len(updates.Options) != len(want) {
		t.Fatalf("Trigger On has %d options, want %d", len(updates.Options), len(want))
	}
	for index, value := range want {
		if updates.Options[index].Value != value {
			t.Errorf("option %d = %q, want %q", index, updates.Options[index].Value, value)
		}
	}

	// The three additional fields, under n8n's own collection key so an
	// imported node's `additionalFields.download` lands where it belongs.
	for _, key := range []string{"download", "imageSize", "chatIds", "userIds"} {
		if _, present := fields[key]; !present {
			t.Errorf("Additional Fields has no %q", key)
		}
	}
	if visible := fields["imageSize"].VisibleWhen; len(visible) != 1 || visible[0].Key != "download" {
		t.Errorf("Image Size is not gated on Download Images/Files: %#v", visible)
	}
}

// `*` means the Bot API's own default set, which excludes three update types.
// Expanding it into an explicit list would subscribe the workflow to traffic
// its author did not ask for.
func TestSelectingEverythingSendsNoAllowedUpdates(t *testing.T) {
	t.Parallel()

	if allowed := nodes.TelegramAllowedUpdates(map[string]any{"updates": []any{"*"}}); len(allowed) != 0 {
		t.Fatalf("allowed_updates = %v, want none so the API uses its default set", allowed)
	}
	// And `*` wins wherever it appears, because it is the wider request.
	if allowed := nodes.TelegramAllowedUpdates(map[string]any{"updates": []any{"message", "*"}}); len(allowed) != 0 {
		t.Fatalf("allowed_updates = %v, want none", allowed)
	}
	// A named selection is sent as itself — including the three the default
	// set withholds, which is what asking for them by name means.
	allowed := nodes.TelegramAllowedUpdates(map[string]any{"updates": []any{"message", "chat_member"}})
	if strings.Join(allowed, ",") != "message,chat_member" {
		t.Fatalf("allowed_updates = %v, want the selection as written", allowed)
	}
}

// The secret is derived from the bot token and the route, so registration and
// verification agree without storing anything — and it changes when either
// does.
func TestTheWebhookSecretIsDerivedAndTelegramShaped(t *testing.T) {
	t.Parallel()

	secret := nodes.TelegramSecret("123:ABC", "route-a")
	if secret == "" {
		t.Fatal("no secret was derived")
	}
	if secret != nodes.TelegramSecret("123:ABC", "route-a") {
		t.Fatal("the secret is not stable, so verification could never match registration")
	}
	if secret == nodes.TelegramSecret("123:ABC", "route-b") {
		t.Fatal("two routes share a secret")
	}
	if secret == nodes.TelegramSecret("456:DEF", "route-a") {
		t.Fatal("two bots share a secret")
	}
	// The Bot API accepts only A-Z a-z 0-9 _ - and 1 to 256 characters. A raw
	// base64 secret would be refused at registration, and the failure would
	// look like a bad token.
	if len(secret) < 1 || len(secret) > 256 {
		t.Fatalf("secret is %d characters, want 1 to 256", len(secret))
	}
	for _, char := range secret {
		switch {
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '-':
		default:
			t.Fatalf("secret contains %q, which the Bot API refuses", char)
		}
	}
}

func telegramDelivery(t *testing.T, header string, body map[string]any, parameters map[string]any) webhook.Delivery {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/webhook/route-a", nil)
	if header != "" {
		request.Header.Set(nodes.TelegramSecretHeader, header)
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	return webhook.Delivery{
		Request: request, Body: body,
		Binding: repository.WebhookBinding{Route: "route-a", Parameters: parameters},
		Credential: func(credentialType string) (map[string]string, error) {
			if credentialType != nodes.TelegramCredentialType {
				return nil, fmt.Errorf("no %s credential", credentialType)
			}
			return map[string]string{"accessToken": "123:ABC"}, nil
		},
	}
}

// Telegram returns the registration's secret on every update, so this is a
// comparison rather than a signature — and a check that accepts an absent
// secret verifies nothing.
func TestADeliveryWithoutTheMatchingSecretIsRefused(t *testing.T) {
	t.Parallel()

	kind := nodes.TelegramTriggerKind()
	update := map[string]any{"update_id": float64(1)}

	if err := kind.Verify(telegramDelivery(t, nodes.TelegramSecret("123:ABC", "route-a"), update, nil)); err != nil {
		t.Fatalf("a correctly signed delivery was refused: %v", err)
	}
	for name, header := range map[string]string{
		"no header":     "",
		"wrong secret":  "not-the-secret",
		"another route": nodes.TelegramSecret("123:ABC", "route-b"),
	} {
		if err := kind.Verify(telegramDelivery(t, header, update, nil)); err == nil {
			t.Errorf("a delivery with %s was accepted", name)
		}
	}
}

// A filtered update was received correctly and deliberately not acted on, so
// it is dropped without an execution and the reason is visible.
func TestRestrictedChatsAndUsersDropAnUpdateWithoutRunning(t *testing.T) {
	t.Parallel()

	kind := nodes.TelegramTriggerKind()
	message := func(chat, user float64) map[string]any {
		return map[string]any{"update_id": float64(1), "message": map[string]any{
			"chat": map[string]any{"id": chat},
			"from": map[string]any{"id": user},
			"text": "hello",
		}}
	}

	restricted := map[string]any{"additionalFields": map[string]any{"chatIds": "42, 43", "userIds": "7"}}
	if accepted, _ := kind.Accept(telegramDelivery(t, "", message(42, 7), restricted)); !accepted {
		t.Fatal("an allowed chat and user was filtered out")
	}
	accepted, reason := kind.Accept(telegramDelivery(t, "", message(99, 7), restricted))
	if accepted {
		t.Fatal("an update from a chat outside the restriction was accepted")
	}
	if !strings.Contains(reason, "99") {
		t.Errorf("reason = %q, want it to name the chat that was dropped", reason)
	}
	if accepted, _ := kind.Accept(telegramDelivery(t, "", message(42, 99), restricted)); accepted {
		t.Fatal("an update from a user outside the restriction was accepted")
	}

	// A filter that cannot find an id fails closed: an update type this build
	// has never seen carries it somewhere unknown, and letting it through
	// would make the restriction advisory.
	unknown := map[string]any{"update_id": float64(1), "invented_in_2027": map[string]any{"x": 1}}
	if accepted, _ := kind.Accept(telegramDelivery(t, "", unknown, restricted)); accepted {
		t.Fatal("an update with no findable chat id passed a chat restriction")
	}
	// With no restriction configured, the same update passes: an unknown
	// update type must not break an active workflow.
	if accepted, _ := kind.Accept(telegramDelivery(t, "", unknown, nil)); !accepted {
		t.Fatal("an unknown update type was dropped by a trigger with no restrictions")
	}
}

// Telegram ids are JSON numbers big enough that the shorthand rendering turns
// into an exponent, and `1.234567891e+09` matches nothing.
func TestALargeChatIDIsComparedAsAnInteger(t *testing.T) {
	t.Parallel()

	kind := nodes.TelegramTriggerKind()
	update := map[string]any{"message": map[string]any{"chat": map[string]any{"id": float64(1234567891)}}}
	restricted := map[string]any{"additionalFields": map[string]any{"chatIds": "1234567891"}}
	if accepted, reason := kind.Accept(telegramDelivery(t, "", update, restricted)); !accepted {
		t.Fatalf("a large chat id did not match itself: %s", reason)
	}
}

// The update object is the item, because an imported workflow reads
// `$json.message.text` and not `$json.body.message.text`.
func TestTheUpdateIsTheItem(t *testing.T) {
	t.Parallel()

	kind := nodes.TelegramTriggerKind()
	item := kind.Shape.Apply(webhook.Delivery{Body: map[string]any{
		"update_id": float64(1),
		"message":   map[string]any{"text": "hello"},
	}})
	message, ok := item["message"].(map[string]any)
	if !ok || message["text"] != "hello" {
		t.Fatalf("item = %#v, want the update at the top level", item)
	}
}

func telegramIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.TelegramTriggerNodeType, workflow.V(1))
	return workflow.IRNode{
		ID: "telegram-1", Name: "Telegram Trigger", Type: nodes.TelegramTriggerNodeType,
		TypeVersion: workflow.V(1), Parameters: parameters,
		Credentials: map[string]string{nodes.TelegramCredentialType: "cred-1"},
		Definition:  definition,
	}
}

// telegramAPI stands in for the Bot API. The credential carries the base URL —
// Telegram publishes a local Bot API server and a deployment running one has no
// route to the public host — so the test points the credential at itself rather
// than reaching around the node.
func telegramAPI(t *testing.T, handler http.HandlerFunc) (safehttp.Policy, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	t.Cleanup(server.Close)
	return policy, server
}

// Download Images/Files resolves the file and attaches a reference. With the
// option off, nothing is fetched at all.
func TestDownloadingAFileAttachesAReferenceAndHonoursImageSize(t *testing.T) {
	t.Parallel()

	var requestedFileID, downloadedPath string
	calls := 0
	policy, server := telegramAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			requestedFileID = r.URL.Query().Get("file_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/file_7.jpg"}}`))
		default:
			downloadedPath = r.URL.Path
			_, _ = w.Write([]byte("JPEGBYTES"))
		}
	})
	// The node only ever calls api.telegram.org, so the test server is reached
	// by rewriting that host rather than by configuring a different one.
	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	update := map[string]any{"message": map[string]any{"photo": []any{
		map[string]any{"file_id": "small-id"},
		map[string]any{"file_id": "medium-id"},
		map[string]any{"file_id": "large-id"},
	}}}
	request := engine.Request{
		Input:       workflow.Item{JSON: update},
		Binaries:    binary.For(store, "tenant-a", "exec-1"),
		Credentials: telegramCredential{baseURL: server.URL},
	}

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, policy, sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup(nodes.TelegramTriggerExecutorID)

	// Off by default.
	output, err := executor.Execute(context.Background(), telegramIR(t, map[string]any{"path": "bot"}), workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].Binary != nil || calls != 0 {
		t.Fatalf("a file was fetched without being asked for: %d calls", calls)
	}

	// On, with the size the user chose.
	output, err = executor.Execute(context.Background(), telegramIR(t, map[string]any{
		"path": "bot", "additionalFields": map[string]any{"download": true, "imageSize": "medium"},
	}), workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() with download error = %v", err)
	}
	if requestedFileID != "medium-id" {
		t.Fatalf("getFile asked for %q, want the size the user chose", requestedFileID)
	}
	if !strings.Contains(downloadedPath, "photos/file_7.jpg") {
		t.Fatalf("downloaded %q, want the path getFile returned", downloadedPath)
	}
	reference, attached := output[0][0].Binary["data"]
	if !attached {
		t.Fatalf("Binary = %#v, want the file attached", output[0][0].Binary)
	}
	if reference.FileName != "file_7.jpg" || reference.Size != int64(len("JPEGBYTES")) {
		t.Fatalf("reference = %#v, want the downloaded file", reference)
	}
	// The bytes are in the store, never in the item.
	encoded, _ := json.Marshal(output[0][0].JSON)
	if strings.Contains(string(encoded), "JPEGBYTES") {
		t.Fatalf("the item carries the payload: %s", encoded)
	}
}

// A size the update does not have falls back to the largest it does, rather
// than indexing past the end of what Telegram sent.
func TestAnImageSizeTelegramDidNotSendFallsBack(t *testing.T) {
	t.Parallel()

	var requested string
	policy, server := telegramAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			requested = r.URL.Query().Get("file_id")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"p.jpg"}}`))
			return
		}
		_, _ = w.Write([]byte("x"))
	})
	store, _ := binary.NewFileStore(t.TempDir(), 1<<20)
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, policy, sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup(nodes.TelegramTriggerExecutorID)

	if _, err := executor.Execute(context.Background(), telegramIR(t, map[string]any{
		"path": "bot", "additionalFields": map[string]any{"download": true, "imageSize": "extraLarge"},
	}), workflow.NodeInput{}, engine.Request{
		Input: workflow.Item{JSON: map[string]any{"message": map[string]any{"photo": []any{
			map[string]any{"file_id": "only-one"},
		}}}},
		Binaries:    binary.For(store, "tenant-a", "exec-1"),
		Credentials: telegramCredential{baseURL: server.URL},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if requested != "only-one" {
		t.Fatalf("getFile asked for %q, want the only size there was", requested)
	}
}

type telegramCredential struct{ baseURL string }

func (stub telegramCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "Bot", Type: nodes.TelegramCredentialType,
		Fields: map[string]string{"accessToken": "123:ABC", "baseUrl": stub.baseURL},
	}, nil
}

func telegramLifecycleContext(t *testing.T, policy safehttp.Policy, baseURL string, parameters map[string]any) webhook.LifecycleContext {
	t.Helper()
	if parameters == nil {
		parameters = map[string]any{}
	}
	parameters["$credentials"] = map[string]any{nodes.TelegramCredentialType: "cred-1"}
	return webhook.LifecycleContext{
		TenantID: "tenant-a", WorkflowID: "wf_1",
		Binding:     repository.WebhookBinding{NodeID: "telegram", Route: "route-a", Parameters: parameters},
		PublicURL:   "https://flows.example.test/webhook/route-a",
		HTTP:        policy,
		Credentials: telegramCredential{baseURL: baseURL},
	}
}

// Activation registers this workflow's own URL, the selected updates and the
// derived secret; deactivation removes it. Both go through the egress policy.
func TestActivationRegistersTheWebhookAndDeactivationRemovesIt(t *testing.T) {
	t.Parallel()

	type call struct {
		method string
		body   map[string]any
	}
	var calls []call
	policy, server := telegramAPI(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, call{method: strings.TrimPrefix(r.URL.Path, "/bot123:ABC/"), body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})

	lifecycle := nodes.NewTelegramLifecycle(nil)
	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, map[string]any{
		"updates": []any{"message", "callback_query"},
	})

	if err := lifecycle.Create(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(calls) != 1 || calls[0].method != "setWebhook" {
		t.Fatalf("calls = %#v, want one setWebhook", calls)
	}
	if calls[0].body["url"] != lifecycleContext.PublicURL {
		t.Fatalf("url = %#v, want this workflow's own address", calls[0].body["url"])
	}
	if calls[0].body["secret_token"] != nodes.TelegramSecret("123:ABC", "route-a") {
		t.Fatalf("secret_token = %#v, want the derived secret", calls[0].body["secret_token"])
	}
	allowed, _ := calls[0].body["allowed_updates"].([]any)
	if len(allowed) != 2 || allowed[0] != "message" || allowed[1] != "callback_query" {
		t.Fatalf("allowed_updates = %#v, want the selection", calls[0].body["allowed_updates"])
	}

	calls = nil
	if err := lifecycle.Delete(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(calls) != 1 || calls[0].method != "deleteWebhook" {
		t.Fatalf("calls = %#v, want one deleteWebhook", calls)
	}
}

// Telegram allows one webhook per bot, so a bot pointing somewhere else is not
// registered for *this* workflow — and treating it as though it were would
// leave the workflow silently unreachable.
func TestCheckExistsComparesTheRegisteredURLRatherThanItsPresence(t *testing.T) {
	t.Parallel()

	registered := "https://somewhere.else.test/webhook/other"
	policy, server := telegramAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"ok":true,"result":{"url":%q}}`, registered)))
	})

	lifecycle := nodes.NewTelegramLifecycle(nil)
	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, nil)

	exists, err := lifecycle.CheckExists(context.Background(), lifecycleContext)
	if err != nil {
		t.Fatalf("CheckExists() error = %v", err)
	}
	if exists {
		t.Fatal("a bot registered to another workflow was reported as already registered")
	}

	registered = lifecycleContext.PublicURL
	exists, err = lifecycle.CheckExists(context.Background(), lifecycleContext)
	if err != nil {
		t.Fatalf("CheckExists() error = %v", err)
	}
	if !exists {
		t.Fatal("a bot already registered to this workflow was reported as unregistered")
	}
}

// setWebhook accepts only HTTPS and its own error for a non-HTTPS URL says
// nothing useful. This is the one case where a laptop-shaped mistake has a
// laptop-shaped answer.
func TestActivationRefusesANonHTTPSPublicURLWithAUsefulMessage(t *testing.T) {
	t.Parallel()

	policy, server := telegramAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the API was called despite an unusable public URL")
		w.WriteHeader(http.StatusInternalServerError)
	})

	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, nil)
	lifecycleContext.PublicURL = "http://localhost:8080/webhook/route-a"
	err := nodes.NewTelegramLifecycle(nil).Create(context.Background(), lifecycleContext)
	if err == nil {
		t.Fatal("a non-HTTPS public URL was accepted")
	}
	for _, want := range []string{"HTTPS", "polling"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// Telegram refuses getUpdates while a webhook is set, and its error names
// neither the cause nor the cure — so polling clears the webhook first, every
// time.
func TestPollingClearsTheWebhookBeforeItStarts(t *testing.T) {
	t.Parallel()

	var methods []string
	policy, server := telegramAPI(t, func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot123:ABC/")
		methods = append(methods, method)
		w.Header().Set("Content-Type", "application/json")
		if method == "getUpdates" {
			// Answer nothing forever; the loop is cancelled by Stop.
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	})

	pollers := nodes.NewTelegramPollers(context.Background(), policy, nil)
	lifecycle := nodes.NewTelegramLifecycle(pollers)
	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, map[string]any{"delivery": "polling"})

	// With no runner there is nowhere to queue an execution, so starting is
	// refused rather than silently doing nothing — but the webhook is cleared
	// first either way, which is the ordering under test.
	if err := lifecycle.Create(context.Background(), lifecycleContext); err == nil {
		t.Fatal("polling started with no runner to queue executions")
	}
	if len(methods) == 0 || methods[0] != "deleteWebhook" {
		t.Fatalf("calls = %v, want deleteWebhook first", methods)
	}

	// A polling trigger never registers a webhook, so CheckExists must not
	// report one — otherwise Create would be skipped and nothing would poll.
	if exists, _ := lifecycle.CheckExists(context.Background(), lifecycleContext); exists {
		t.Fatal("a polling trigger reported an existing registration")
	}
}

// A poller is owned by activation and released by deactivation. Leaking one
// keeps calling a bot for a workflow nobody is running.
func TestAPollerStartsOnActivationAndStopsOnDeactivation(t *testing.T) {
	t.Parallel()

	policy, server := telegramAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	})

	pollers := nodes.NewTelegramPollers(context.Background(), policy, stubQueue{})
	lifecycle := nodes.NewTelegramLifecycle(pollers)
	lifecycleContext := telegramLifecycleContext(t, policy, server.URL, map[string]any{"delivery": "polling"})

	if err := lifecycle.Create(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if running := pollers.Running(); len(running) != 1 || running[0] != "route-a" {
		t.Fatalf("running = %v, want the activated route", running)
	}
	if err := lifecycle.Delete(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if running := pollers.Running(); len(running) != 0 {
		t.Fatalf("running = %v, want none after deactivation", running)
	}
}

type stubQueue struct{}

func (stubQueue) QueueWebhook(context.Context, repository.WebhookBinding, json.RawMessage) (execution.Record, error) {
	return execution.Record{ID: "exec-1"}, nil
}

func (stubQueue) Get(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return execution.Record{ID: "exec-1"}, nil
}
