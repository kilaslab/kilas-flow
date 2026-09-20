package telegram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
)

type installed struct {
	definitions *node.Registry
	executors   *engine.Registry
}

func install(t *testing.T, policy safehttp.Policy) installed {
	t.Helper()
	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(routing.ExecutorID, routing.NewExecutor(policy, routes, definitions)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := telegram.Register(definitions, routes, executors, loadoptions.NewResolver(policy, 0)); err != nil {
		t.Fatalf("telegram.Register() error = %v", err)
	}
	return installed{definitions: definitions, executors: executors}
}

// An imported node carries n8n's own resource and operation values, so it has
// to find them here — one character out and it selects nothing.
func TestEveryResourceAndOperationInTheTicketIsRegistered(t *testing.T) {
	t.Parallel()

	pack, err := telegram.Pack()
	if err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	available := map[string][]string{}
	for _, resource := range pack.Resources {
		for _, operation := range resource.Operations {
			available[resource.Name] = append(available[resource.Name], operation.Name)
		}
	}
	want := map[string][]string{
		"message": {
			"sendMessage", "sendPhoto", "sendDocument", "sendAnimation", "sendAudio", "sendVideo",
			"sendSticker", "sendMediaGroup", "sendLocation", "sendChatAction",
			"editMessageText", "deleteMessage", "pinChatMessage", "unpinChatMessage",
		},
		"chat":     {"get", "administrators", "member", "leave", "setTitle", "setDescription"},
		"callback": {"answerQuery", "answerInlineQuery"},
		"file":     {"get"},
	}
	for resource, operations := range want {
		got := strings.Join(available[resource], ",")
		if got != strings.Join(operations, ",") {
			t.Errorf("resource %q has %q, want %q", resource, got, strings.Join(operations, ","))
		}
	}
	if len(available) != len(want) {
		t.Errorf("the pack has %d resources, want %d", len(available), len(want))
	}
}

func telegramNode(t *testing.T, set installed, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, found := set.definitions.Lookup(telegram.NodeType, workflow.V(1))
	if !found {
		t.Fatal("the Telegram pack did not register")
	}
	return workflow.IRNode{
		ID: "telegram-1", Name: "Telegram", Type: telegram.NodeType, TypeVersion: workflow.V(1),
		Parameters:  parameters,
		Credentials: map[string]string{telegram.CredentialType: "cred-1"},
		Definition:  definition,
	}
}

type botCredential struct{ baseURL string }

func (stub botCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "Bot", Type: telegram.CredentialType,
		Fields: map[string]string{"accessToken": "123:ABC", "baseUrl": stub.baseURL},
	}, nil
}

func localPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// The two operations the epic's first acceptance scenario needs, running on
// the shared interpreter with no Telegram-specific Go anywhere in the path.
func TestSendChatActionAndSendMessageMakeTheCallsTheyDescribe(t *testing.T) {
	t.Parallel()

	type call struct {
		path string
		body map[string]any
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, call{path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11}}`))
	}))
	defer server.Close()

	set := install(t, localPolicy())
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	request := engine.Request{Credentials: botCredential{baseURL: server.URL}}

	if _, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendChatAction", "chatId": "@my_channel", "action": "typing",
	}), workflow.NodeInput{}, request); err != nil {
		t.Fatalf("sendChatAction error = %v", err)
	}
	output, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendMessage", "chatId": "@my_channel", "text": "hi back",
	}), workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("sendMessage error = %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("made %d calls, want two", len(calls))
	}
	if calls[0].path != "/bot123:ABC/sendChatAction" || calls[0].body["action"] != "typing" {
		t.Fatalf("first call = %#v, want the typing indicator", calls[0])
	}
	// A channel username is sent as written. Coercing chat_id to a number
	// would break every one of them, and Telegram accepts both forms.
	if calls[1].path != "/bot123:ABC/sendMessage" || calls[1].body["chat_id"] != "@my_channel" {
		t.Fatalf("second call = %#v, want the chat id as written", calls[1])
	}
	if calls[1].body["text"] != "hi back" {
		t.Fatalf("text = %#v", calls[1].body["text"])
	}
	// A parameter belonging to another operation must not ride along.
	if _, present := calls[1].body["action"]; present {
		t.Fatalf("body = %#v, want nothing from another operation", calls[1].body)
	}
	if result, _ := output[0][0].JSON["result"].(map[string]any); result["message_id"] != float64(11) {
		t.Fatalf("item = %#v, want the API's answer", output[0][0].JSON)
	}
}

// The same operation either uploads the item's attachment or names a file
// Telegram already holds. Which one happens is decided by which parameter the
// user filled, not by a second operation.
func TestSendPhotoUploadsTheItemsAttachmentOrSendsAFileID(t *testing.T) {
	t.Parallel()

	var contentType string
	var parts map[string]string
	var fileName, fileBody string
	var jsonBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		parts, fileName, fileBody, jsonBody = nil, "", "", nil
		if strings.HasPrefix(contentType, "multipart/") {
			_, params, _ := mime.ParseMediaType(contentType)
			reader := multipart.NewReader(r.Body, params["boundary"])
			parts = map[string]string{}
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				contents, _ := io.ReadAll(part)
				if part.FileName() != "" {
					fileName, fileBody = part.FileName(), string(contents)
					continue
				}
				parts[part.FormName()] = string(contents)
			}
		} else {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &jsonBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":12}}`))
	}))
	defer server.Close()

	set := install(t, localPolicy())
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	scoped := binary.For(store, "tenant-a", "exec-1")
	reference, err := scoped.Put("holiday.jpg", "image/jpeg", strings.NewReader("JPEGBYTES"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// An attachment on the item, named by the Binary Property parameter.
	if _, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendPhoto",
		"chatId": "42", "binaryPropertyName": "data", "caption": "look",
	}), workflow.NodeInput{"main": {{
		JSON:   map[string]any{},
		Binary: map[string]workflow.BinaryRef{"data": reference},
	}}}, engine.Request{Credentials: botCredential{baseURL: server.URL}, Binaries: scoped}); err != nil {
		t.Fatalf("upload error = %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Fatalf("Content-Type = %q, want multipart", contentType)
	}
	if fileName != "holiday.jpg" || fileBody != "JPEGBYTES" {
		t.Fatalf("file part = %q/%q, want the stored attachment", fileName, fileBody)
	}
	// The scalar fields travel beside the file rather than as a JSON body.
	if parts["chat_id"] != "42" || parts["caption"] != "look" {
		t.Fatalf("form fields = %#v, want the operation's own parameters", parts)
	}

	// A file_id instead: no upload, an ordinary JSON call.
	if _, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendPhoto", "chatId": "42", "file": "AgACAgQx",
	}), workflow.NodeInput{}, engine.Request{
		Credentials: botCredential{baseURL: server.URL}, Binaries: scoped,
	}); err != nil {
		t.Fatalf("file_id error = %v", err)
	}
	if strings.HasPrefix(contentType, "multipart/") {
		t.Fatalf("Content-Type = %q, want JSON when nothing is uploaded", contentType)
	}
	if jsonBody["photo"] != "AgACAgQx" {
		t.Fatalf("body = %#v, want the file id", jsonBody)
	}
}

// getFile answers with a path, and the bytes are behind a second call that
// needs the same credential. The reference reaches the item; the payload does
// not.
func TestGetFileDownloadsIntoTheBinaryStore(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"f1","file_path":"photos/file_9.jpg"}}`))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("PHOTOBYTES"))
	}))
	defer server.Close()

	set := install(t, localPolicy())
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	store, _ := binary.NewFileStore(t.TempDir(), 1<<20)
	scoped := binary.For(store, "tenant-a", "exec-1")

	output, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "file", "operation": "get", "fileId": "f1",
	}), workflow.NodeInput{}, engine.Request{
		Credentials: botCredential{baseURL: server.URL}, Binaries: scoped,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0]
	// rootProperty unwrapped the envelope, so the item is the file itself.
	if item.JSON["file_path"] != "photos/file_9.jpg" {
		t.Fatalf("item = %#v, want the file's own fields", item.JSON)
	}
	reference, attached := item.Binary["data"]
	if !attached {
		t.Fatalf("Binary = %#v, want the download attached", item.Binary)
	}
	if reference.FileName != "file_9.jpg" || reference.Size != int64(len("PHOTOBYTES")) {
		t.Fatalf("reference = %#v, want the downloaded file", reference)
	}
	encoded, _ := json.Marshal(item.JSON)
	if bytes.Contains(encoded, []byte("PHOTOBYTES")) {
		t.Fatalf("the item carries the payload: %s", encoded)
	}
	body, _, err := scoped.Get(reference.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer body.Close()
	stored, _ := io.ReadAll(body)
	if string(stored) != "PHOTOBYTES" {
		t.Fatalf("stored = %q", stored)
	}
}

// The Bot API's `description` is the only part of an error a user can act on,
// so it is the part that reaches them.
func TestAFailureReportsTelegramsOwnDescription(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer server.Close()

	set := install(t, localPolicy())
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	_, err := executor.Execute(context.Background(), telegramNode(t, set, map[string]any{
		"resource": "message", "operation": "sendMessage", "chatId": "0", "text": "x",
	}), workflow.NodeInput{}, engine.Request{Credentials: botCredential{baseURL: server.URL}})
	if err == nil {
		t.Fatal("a Bot API error was not reported")
	}
	for _, want := range []string{"chat not found", "operation sendMessage", "400"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	// The URL carries the bot token, so it must not reach the message.
	if strings.Contains(err.Error(), "123:ABC") {
		t.Errorf("error = %q, want no bot token in it", err)
	}
}

// The epic's first acceptance scenario, end to end through the runner: an
// update arrives, the bot shows it is typing, and the reply goes to the chat
// the update came from. Everything but the network is real.
func TestATriggerDeliveryProducesATypingIndicatorAndThenAReply(t *testing.T) {
	t.Parallel()

	type call struct {
		method string
		body   map[string]any
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, call{method: path.Base(r.URL.Path), body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99}}`))
	}))
	defer server.Close()

	policy := localPolicy()
	set := install(t, policy)
	if err := nodes.RegisterAll(set.definitions); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := nodes.RegisterExecutors(set.executors, policy, sqlnode.Guard{}, nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}

	action := func(operation string, parameters map[string]any) workflow.Node {
		merged := map[string]any{"resource": "message", "operation": operation,
			"chatId": map[string]any{"mode": "expression", "value": "{{ $json.message.chat.id }}"}}
		for key, value := range parameters {
			merged[key] = value
		}
		return workflow.Node{
			ID: operation, Name: operation, Type: telegram.NodeType, TypeVersion: workflow.V(1),
			Parameters:  merged,
			Credentials: map[string]string{telegram.CredentialType: "cred-1"},
		}
	}

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_bot", Name: "Reply to a message",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Telegram Trigger", Type: nodes.TelegramTriggerNodeType, TypeVersion: workflow.V(1),
				Parameters:  map[string]any{"path": "bot", "updates": []any{"message"}},
				Credentials: map[string]string{telegram.CredentialType: "cred-1"}},
			action("sendChatAction", map[string]any{"action": "typing"}),
			// The reply reads the *trigger*, not the previous node's answer:
			// by then `$json` is the Bot API's response to sendChatAction.
			// This is how a real workflow addresses a reply.
			{
				ID: "sendMessage", Name: "sendMessage", Type: telegram.NodeType, TypeVersion: workflow.V(1),
				Parameters: map[string]any{
					"resource": "message", "operation": "sendMessage",
					"chatId": map[string]any{"mode": "expression",
						"value": "{{ $('Telegram Trigger').item.json.message.chat.id }}"},
					"text": map[string]any{"mode": "expression",
						"value": "You said: {{ $('Telegram Trigger').item.json.message.text }}"},
				},
				Credentials: map[string]string{telegram.CredentialType: "cred-1"},
			},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
				Target: workflow.Endpoint{NodeID: "sendChatAction", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "sendChatAction", Port: "main"},
				Target: workflow.Endpoint{NodeID: "sendMessage", Port: "main"}},
		},
		Settings: map[string]any{},
	}, set.definitions)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	// The item a Telegram delivery produces: the update at the top level.
	update := map[string]any{
		"update_id": float64(1),
		"message": map[string]any{
			"chat": map[string]any{"id": float64(4242)},
			"from": map[string]any{"id": float64(7)},
			"text": "hello",
		},
	}
	if _, err := engine.NewRunner(set.executors).Run(context.Background(), ir, engine.Request{
		TriggerNodeID: "trigger",
		Input:         workflow.Item{JSON: update},
		Credentials:   botCredential{baseURL: server.URL},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("made %d Bot API calls, want the typing indicator and the reply", len(calls))
	}
	if calls[0].method != "sendChatAction" || calls[0].body["action"] != "typing" {
		t.Fatalf("first call = %#v, want the typing indicator first", calls[0])
	}
	if calls[1].method != "sendMessage" {
		t.Fatalf("second call = %#v, want the reply", calls[1])
	}
	// Both go to the chat the update came from, read through an expression —
	// which is how every real workflow addresses a reply. The id arrives as
	// the JSON number it was in the update; nothing coerces it, which is the
	// property that matters, because coercing to a number is what would break
	// an @channelusername.
	for index, made := range calls {
		if fmt.Sprintf("%v", made.body["chat_id"]) != "4242" {
			t.Fatalf("call %d chat_id = %#v, want the chat the update came from", index, made.body["chat_id"])
		}
	}
	if calls[1].body["text"] != "You said: hello" {
		t.Fatalf("text = %#v, want the message read out of the update", calls[1].body["text"])
	}
}
