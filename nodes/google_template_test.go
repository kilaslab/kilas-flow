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

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// A value an expression produced is data. The Google nodes used to walk the
// already-resolved parameters and evaluate any string in them that held
// `{{ … }}`, so text an outsider controls (a webhook body today, a model's
// tool argument tomorrow) ran as an expression with `$env` in reach.
const injectedTemplate = "{{ $env.SECRET }}"

func secretRequest(server *httptest.Server, credentialType string) engine.Request {
	return engine.Request{
		Credentials: googleCreds{typ: credentialType, host: httptestHost(server.URL)},
		Env:         map[string]string{"SECRET": "s3cr3t-value"},
	}
}

func TestGmailSendDoesNotEvaluateBracesInsideAResolvedValue(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Raw string `json:"raw"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		decoded, _ := base64.RawURLEncoding.DecodeString(body.Raw)
		sent = string(decoded)
		_, _ = io.WriteString(w, `{"id":"sent-1"}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	executor := nodes.NewGmailExecutor(allowing(server))
	ir := workflow.IRNode{
		ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation": "send",
			"to":        "ops@example.com",
			"subject":   map[string]any{"mode": "expression", "value": "{{ $json.subject }}"},
			"message":   map[string]any{"mode": "expression", "value": "Body: {{ $json.body }}"},
		},
		Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
	}
	item := workflow.Item{JSON: map[string]any{"subject": injectedTemplate, "body": injectedTemplate}}
	if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {item}}, secretRequest(server, nodes.GmailCredentialType)); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(sent, "s3cr3t-value") {
		t.Fatalf("the sent mail carries the secret, so a resolved value was evaluated again:\n%s", sent)
	}
	if !strings.Contains(sent, "Subject: "+injectedTemplate) || !strings.Contains(sent, "Body: "+injectedTemplate) {
		t.Fatalf("the sent mail does not carry the item's text verbatim:\n%s", sent)
	}
}

func TestGoogleDriveSearchDoesNotEvaluateBracesInsideAResolvedValue(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		_, _ = io.WriteString(w, `{"files":[]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest(server.URL+"/drive/v3", ""))

	executor := nodes.NewGoogleDriveExecutor(allowing(server))
	ir := workflow.IRNode{
		ID: "drive", Name: "Drive", Type: nodes.GoogleDriveNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation":   "search",
			"queryString": map[string]any{"mode": "expression", "value": "{{ $json.q }}"},
		},
		Credentials: map[string]string{nodes.GoogleDriveCredentialType: "cred"},
	}
	item := workflow.Item{JSON: map[string]any{"q": injectedTemplate}}
	if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {item}}, secretRequest(server, nodes.GoogleDriveCredentialType)); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if query != injectedTemplate {
		t.Fatalf("q = %q, want the item's text %q verbatim", query, injectedTemplate)
	}
}

// What the extra walk was for still works: an imported n8n parameter nested
// below the top level (n8n's `=` prefix, which the importer converts only at
// the top) is the workflow author's own template and is evaluated once.
func TestGmailStillEvaluatesANestedTemplateTheAuthorWrote(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		_, _ = io.WriteString(w, `{"messages":[]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	executor := nodes.NewGmailExecutor(allowing(server))
	ir := workflow.IRNode{
		ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"operation": "getAll",
			"filters":   map[string]any{"sender": "={{ $json.from }}"},
		},
		Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
	}
	item := workflow.Item{JSON: map[string]any{"from": "alice@example.com"}}
	if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {item}}, secretRequest(server, nodes.GmailCredentialType)); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if query != "from:alice@example.com" {
		t.Fatalf("q = %q, want the author's nested template evaluated once", query)
	}
}
