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

// The send builds the message's header block by hand, so a line break in a
// header value would start a header of the caller's choosing (a Bcc to an
// outsider). The value can come from a webhook or a model, so it is refused.
func TestGmailSendRefusesALineBreakInAHeaderValue(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"id":"sent-1"}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", server.URL+"/gmail/v1"))

	executor := nodes.NewGmailExecutor(allowing(server))
	for name, parameters := range map[string]map[string]any{
		"to":      {"to": "ops@example.com\r\nBcc: outsider@example.com", "subject": "Hi"},
		"subject": {"to": "ops@example.com", "subject": "Hi\nBcc: outsider@example.com"},
	} {
		parameters["operation"] = "send"
		parameters["message"] = "body"
		ir := workflow.IRNode{
			ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		}
		_, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, secretRequest(server, nodes.GmailCredentialType))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("a line break in %s: error = %v, want a refusal naming it", name, err)
		}
	}
	if calls != 0 {
		t.Fatalf("Gmail was called %d times, want none", calls)
	}
}

// n8n's Gmail sends HTML unless told otherwise, so an imported send keeps its
// markup only if this node can send HTML too.
func TestGmailSendsHTMLWhenTheEmailTypeSaysSo(t *testing.T) {
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
	for emailType, want := range map[string]string{"html": "text/html", "text": "text/plain", "": "text/plain"} {
		ir := workflow.IRNode{
			ID: "gmail", Name: "Gmail", Type: nodes.GmailNodeType, TypeVersion: workflow.V(1),
			Parameters: map[string]any{
				"operation": "send", "to": "ops@example.com", "subject": "Hi",
				"message": "<b>bold</b>", "emailType": emailType,
			},
			Credentials: map[string]string{nodes.GmailCredentialType: "cred"},
		}
		if _, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, secretRequest(server, nodes.GmailCredentialType)); err != nil {
			t.Fatalf("Execute(%q) error = %v", emailType, err)
		}
		if !strings.Contains(sent, "Content-Type: "+want+"; charset=utf-8\r\n") {
			t.Errorf("emailType %q sent:\n%s\nwant Content-Type %s", emailType, sent, want)
		}
	}
}
