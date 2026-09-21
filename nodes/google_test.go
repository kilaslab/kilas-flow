package nodes_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestPollIntervalReadsN8NPollTimes(t *testing.T) {
	t.Parallel()

	got := nodes.PollInterval(map[string]any{
		"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyX", "value": 5.0, "unit": "minutes"}}},
	})
	if got != 5*time.Minute {
		t.Fatalf("PollInterval() = %s, want 5m", got)
	}
}

func TestExtractPollsSkipsDisabledTriggers(t *testing.T) {
	t.Parallel()

	triggers := nodes.ExtractPolls(workflow.Document{Nodes: []workflow.Node{
		{ID: "on", Type: nodes.GmailTriggerNodeType, Parameters: map[string]any{}},
		{ID: "off", Type: nodes.GoogleDriveTriggerNodeType, Disabled: true},
		{ID: "other", Type: "kilasflow.manual"},
	}})
	if len(triggers) != 1 || triggers[0].NodeID != "on" {
		t.Fatalf("ExtractPolls() = %#v, want the enabled gmail trigger", triggers)
	}
}

func TestGoogleDriveSearchUsesTheStub(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v3/files" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"files":[{"id":"file-1","name":"manual.pdf"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGoogleDriveExecutor(policy)
	ir := workflow.IRNode{
		ID: "drive", Name: "Drive", Type: nodes.GoogleDriveNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "search", "queryString": "name = 'manual.pdf'"},
		Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
	}
	request := engine.Request{Credentials: googleCreds{typ: nodes.GoogleDriveCredentialType, host: httptestHost(server.URL)}}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["id"] != "file-1" {
		t.Fatalf("item = %#v", output[0][0].JSON)
	}
}

func TestGmailPollerSkipsSeenMessageIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[{"id":"m1","threadId":"t1"},{"id":"m2","threadId":"t2"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	handler := nodes.NewGmailPoller(policy)
	cursor, _ := json.Marshal(map[string]any{"seen": []string{"m1"}})
	items, next, err := handler.Poll(context.Background(), poller.Claim{
		Cursor: repository.PollCursor{Cursor: string(cursor)},
		Node: workflow.Node{
			ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		},
		Request: engine.Request{Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)}},
		Now:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(items) != 1 || items[0].JSON["id"] != "m2" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(next, "m2") {
		t.Fatalf("cursor %s did not record m2", next)
	}
}

func TestGmailPollerRecordsSeenOnTheFirstTick(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[{"id":"m1","threadId":"t1"},{"id":"m2","threadId":"t2"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	handler := nodes.NewGmailPoller(policy)
	items, cursor, err := handler.Poll(context.Background(), poller.Claim{
		Node: workflow.Node{
			ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		},
		Request: engine.Request{Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)}},
		Now:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("first tick emitted %#v, want no historical messages", items)
	}
	if !strings.Contains(cursor, "m1") || !strings.Contains(cursor, "m2") {
		t.Fatalf("cursor = %s, want the listed ids recorded as seen", cursor)
	}
}

func TestGoogleDriveDownloadStoresTheFileAsBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/drive/v3/files/file-1" && r.URL.Query().Get("alt") == "media":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(w, "%PDF-hello")
		case r.URL.Path == "/drive/v3/files/file-1":
			_, _ = io.WriteString(w, `{"id":"file-1","name":"manual.pdf","mimeType":"application/pdf"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGoogleDriveExecutor(policy)
	ir := workflow.IRNode{
		ID: "drive", Name: "Drive", Type: nodes.GoogleDriveNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "download", "fileId": "file-1"},
		Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GoogleDriveCredentialType, host: httptestHost(server.URL)},
		Binaries:    binaryStore(t),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["id"] != "file-1" || output[0][0].Binary["data"].ID == "" {
		t.Fatalf("download item = %#v binary=%#v", output[0][0].JSON, output[0][0].Binary)
	}
}

func TestGoogleDriveMovePatchesParents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/drive/v3/files/file-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("addParents") != "folder-2" {
			t.Errorf("addParents = %s", r.URL.Query().Get("addParents"))
		}
		if r.URL.Query().Get("removeParents") != "folder-1" {
			t.Errorf("removeParents = %s", r.URL.Query().Get("removeParents"))
		}
		_, _ = io.WriteString(w, `{"id":"file-1","name":"manual.pdf","parents":["folder-2"]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGoogleDriveExecutor(policy)
	ir := workflow.IRNode{
		ID: "drive", Name: "Drive", Type: nodes.GoogleDriveNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "move", "fileId": "file-1", "folderId": "folder-2"},
		Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{"parents": []any{"folder-1"}}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GoogleDriveCredentialType, host: httptestHost(server.URL)},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["id"] != "file-1" {
		t.Fatalf("move item = %#v", output[0][0].JSON)
	}
}

func TestGmailGetReadsTheMessageByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gmail/v1/users/me/messages/m1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"m1","threadId":"t1","snippet":"hello"}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGmailExecutor(policy)
	ir := workflow.IRNode{
		ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "get"},
		Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{"id": "m1"}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["id"] != "m1" {
		t.Fatalf("get item = %#v", output[0][0].JSON)
	}
}

func TestGmailGetSimpleFlattensMimeText(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("hello inbox"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "full" {
			t.Errorf("format = %s, want full so the body can be flattened", r.URL.Query().Get("format"))
		}
		_, _ = io.WriteString(w, `{"id":"m1","threadId":"t1","snippet":"hello","payload":{"mimeType":"text/plain","headers":[{"name":"Subject","value":"Hi"}],"body":{"data":"`+encoded+`"}}}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGmailExecutor(policy)
	ir := workflow.IRNode{
		ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "get"},
		Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{"id": "m1"}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0].JSON
	if item["text"] != "hello inbox" {
		t.Fatalf("text = %#v, want the decoded body", item["text"])
	}
	if item["subject"] != "Hi" {
		t.Fatalf("subject = %#v", item["subject"])
	}
	if _, ok := item["payload"]; ok {
		t.Fatalf("simple item still carries payload: %#v", item)
	}
}

func TestGmailGetSimpleFlattensHtmlOnlyMailToText(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("<p>Hello <b>there</b></p>"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"m1","threadId":"t1","snippet":"Hello there","payload":{"mimeType":"text/html","body":{"data":"`+encoded+`"}}}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGmailExecutor(policy)
	ir := workflow.IRNode{
		ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
		Parameters:  map[string]any{"operation": "get"},
		Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{"id": "m1"}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := output[0][0].JSON["text"]; got != "Hello there" {
		t.Fatalf("text = %#v, want stripped html so the loader can read it", got)
	}
}

func TestGoogleDriveMoveResolvesN8NFileLocator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/drive/v3/files/file-1" {
			t.Errorf("path = %s, want the resolved file id", r.URL.Path)
		}
		if r.URL.Query().Get("addParents") != "folder-2" {
			t.Errorf("addParents = %s", r.URL.Query().Get("addParents"))
		}
		_, _ = io.WriteString(w, `{"id":"file-1","name":"manual.pdf","parents":["folder-2"]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	executor := nodes.NewGoogleDriveExecutor(policy)
	ir := workflow.IRNode{
		ID: "drive", Name: "Drive", Type: nodes.GoogleDriveNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation": "move",
			"fileId":    map[string]any{"__rl": true, "mode": "id", "value": "={{ $json.id }}"},
			"folderId":  "folder-2",
		},
		Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{
		"id": "file-1", "parents": []any{"folder-1"},
	}}}}, engine.Request{
		Credentials: googleCreds{typ: nodes.GoogleDriveCredentialType, host: httptestHost(server.URL)},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["id"] != "file-1" {
		t.Fatalf("move item = %#v", output[0][0].JSON)
	}
}

func TestGmailPollerUsesHistoryAfterTheFirstTick(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/history") {
			t.Errorf("path = %s, want history.list", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("startHistoryId") != "50" {
			t.Errorf("startHistoryId = %s", r.URL.Query().Get("startHistoryId"))
		}
		_, _ = io.WriteString(w, `{"historyId":"100","history":[{"messagesAdded":[{"message":{"id":"m3","threadId":"t3"}}]}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	handler := nodes.NewGmailPoller(policy)
	cursor, _ := json.Marshal(map[string]any{"historyId": "50", "seen": []string{"m1"}})
	items, next, err := handler.Poll(context.Background(), poller.Claim{
		Cursor: repository.PollCursor{Cursor: string(cursor)},
		Node: workflow.Node{
			ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		},
		Request: engine.Request{Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)}},
		Now:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(items) != 1 || items[0].JSON["id"] != "m3" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(next, `"historyId":"100"`) {
		t.Fatalf("cursor = %s, want the new history id", next)
	}
}

func TestGmailPollerFallsBackToMessagesWhenHistoryExpires(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/history") {
			http.Error(w, "expired", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"messages":[{"id":"m1","threadId":"t1"},{"id":"m4","threadId":"t4"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	handler := nodes.NewGmailPoller(policy)
	cursor, _ := json.Marshal(map[string]any{"historyId": "1", "seen": []string{"m1"}})
	items, next, err := handler.Poll(context.Background(), poller.Claim{
		Cursor: repository.PollCursor{Cursor: string(cursor)},
		Node: workflow.Node{
			ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		},
		Request: engine.Request{Credentials: googleCreds{typ: nodes.GmailCredentialType, host: httptestHost(server.URL)}},
		Now:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(items) != 1 || items[0].JSON["id"] != "m4" {
		t.Fatalf("items = %#v", items)
	}
	if !strings.Contains(next, "m4") {
		t.Fatalf("cursor %s did not record m4", next)
	}
}

func TestGoogleDrivePollerRecordsNowOnTheFirstTick(t *testing.T) {
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		_, _ = io.WriteString(w, `{"files":[{"id":"file-1","name":"old.pdf","modifiedTime":"2020-01-01T00:00:00Z"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	handler := nodes.NewGoogleDrivePoller(policy)
	items, cursor, err := handler.Poll(context.Background(), poller.Claim{
		Node: workflow.Node{
			ID: "drive", Name: "Drive Trigger", Type: nodes.GoogleDriveTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
			Parameters:  map[string]any{"folderId": "inbox"},
		},
		Request: engine.Request{Credentials: googleCreds{typ: nodes.GoogleDriveCredentialType, host: httptestHost(server.URL)}},
		Now:     time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if called == 0 {
		t.Fatal("first tick did not list files")
	}
	if len(items) != 0 {
		t.Fatalf("first tick emitted %#v, want no historical files", items)
	}
	if !strings.Contains(cursor, "2026-09-21") {
		t.Fatalf("cursor = %s, want the first-tick timestamp", cursor)
	}
}

func TestVectorStorePGVectorValidateRequiresACustomerCredential(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, ok := registry.Get(nodes.VectorStorePGVectorNodeType, workflow.V(1))
	if !ok {
		t.Fatal("vectorStorePGVector is not registered")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"tableName": "documents"}}); err == nil {
		t.Fatal("Validate() = nil, want a postgres credential")
	}
	if err := definition.Validate(workflow.Node{
		Parameters:  map[string]any{"tableName": "documents"},
		Credentials: map[string]string{"postgres": "cred-pg"},
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func httptestHost(raw string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host, _, found := strings.Cut(trimmed, ":")
	if found {
		return host
	}
	return trimmed
}

type googleCreds struct {
	typ, host string
}

func (creds googleCreds) ResolveCredential(_ context.Context, id string) (engine.Credential, error) {
	return engine.Credential{
		ID: id, Name: "google", Type: creds.typ,
		Fields:         map[string]string{"access_token": "token"},
		AllowedDomains: []string{creds.host, "googleapis.com", "*.googleapis.com", "gmail.googleapis.com"},
	}, nil
}
