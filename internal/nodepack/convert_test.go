package nodepack_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// acmeTranscription is a hand-written transcription in the converter's own
// input schema, standing in for what an operator copies out of an installed
// community package. It is fixture data, not third-party bytes: no upstream
// description object appears here, only the format facts the converter reads.
const acmeTranscription = `{
  "sourceType": "n8n-nodes-acme.AcmeMail",
  "displayName": "Acme Mail",
  "description": "Send and manage mail through Acme.",
  "version": 1,
  "requestDefaults": {"baseURL": "https://api.acme.example"},
  "credentials": [{"name": "httpHeaderAuth", "required": true}],
  "hasExecute": false,
  "properties": [
    {"key": "resource", "label": "Resource", "kind": "options", "options": [
      {"label": "Message", "value": "message"},
      {"label": "Mailbox", "value": "mailbox"}
    ]},
    {"key": "operation", "label": "Operation", "kind": "options", "options": [
      {"label": "Send", "value": "send", "description": "Send a message",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "POST", "url": "/v1/messages"}}},
      {"label": "Get", "value": "get",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "GET", "url": "/v1/messages/{messageId}"},
                   "output": {"postReceive": [{"type": "rootProperty", "properties": {"property": "data"}}]}}},
      {"label": "List", "value": "list",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "GET", "url": "/v1/messages"},
                   "operations": {"pagination": {"type": "offset", "properties": {
                     "limitParameter": "limit", "offsetParameter": "offset",
                     "pageSize": 50, "type": "query"}}}}},
      {"label": "List", "value": "list",
       "show": {"resource": ["mailbox"]},
       "routing": {"request": {"method": "GET", "url": "/v1/mailboxes"}}},
      {"label": "Watch", "value": "watch",
       "show": {"resource": ["mailbox"]},
       "routing": {"request": {"method": "POST", "url": "/v1/mailboxes/watch"}}},
      {"label": "Stream", "value": "stream",
       "show": {"resource": ["mailbox"]},
       "routing": {"request": {"method": "GET", "url": "/v1/mailboxes/stream"},
                   "operations": {"pagination": {"type": "cursor", "properties": {
                     "limitParameter": "limit", "offsetParameter": "cursor",
                     "pageSize": 50, "type": "query"}}}}},
      {"label": "Purge", "value": "purge",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "POST", "url": "/v1/messages/purge"},
                   "output": {"postReceive": [{"type": "function"}]}}},
      {"label": "Raw", "value": "raw",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "GET", "url": "/v1/raw", "qs": {"fixed": "1"}}}},
      {"label": "Draft", "value": "draft",
       "show": {"resource": ["message"]},
       "routing": {"request": {"method": "POST"}}}
    ]},
    {"key": "to", "label": "To", "kind": "string", "required": true,
     "show": {"resource": ["message"], "operation": ["send"]},
     "routing": {"send": {"type": "body", "property": "to"}}},
    {"key": "subject", "label": "Subject", "kind": "string",
     "show": {"resource": ["message"], "operation": ["send", "noSuchOp"]},
     "routing": {"send": {"type": "body", "property": "subject"}}},
    {"key": "headers", "label": "Headers", "kind": "fixedCollection",
     "show": {"resource": ["message"], "operation": ["send"]},
     "routing": {"send": {"type": "body", "property": "headers"}}},
    {"key": "tags", "label": "Tags", "kind": "multiOptions",
     "options": [{"label": "Urgent", "value": "urgent"}, {"label": "Follow Up", "value": "followUp"}],
     "show": {"resource": ["message"], "operation": ["send"]},
     "routing": {"send": {"type": "body", "property": "tags"}}},
    {"key": "scheduledAt", "label": "Scheduled At", "kind": "dateTime",
     "show": {"resource": ["message"], "operation": ["send"]},
     "routing": {"send": {"type": "body", "property": "scheduledAt"}}},
    {"key": "hint", "label": "Hint", "kind": "notice",
     "show": {"resource": ["message"]}},
    {"key": "owner", "label": "Owner", "kind": "resourceLocator",
     "show": {"resource": ["mailbox"], "operation": ["list"]},
     "routing": {"send": {"type": "query", "property": "owner"}}},
    {"key": "messageId", "label": "Message ID", "kind": "string", "required": true,
     "show": {"resource": ["message"], "operation": ["get"]},
     "routing": {"send": {"type": "path", "property": "messageId"}}},
    {"key": "limit", "label": "Limit", "kind": "number", "default": 50,
     "show": {"resource": ["message"], "operation": ["list"]},
     "routing": {"send": {"type": "query", "property": "limit"}}},
    {"key": "timeout", "label": "Timeout", "kind": "number", "default": 30,
     "show": {"resource": ["message"], "operation": ["list"]}},
    {"key": "timeout", "label": "Timeout", "kind": "string", "default": "30s",
     "show": {"resource": ["mailbox"], "operation": ["list"]}},
    {"key": "folder", "label": "Folder", "kind": "options", "default": "inbox",
     "options": [{"label": "Inbox", "value": "inbox"}, {"label": "Sent", "value": "sent"}],
     "show": {"resource": ["mailbox"], "operation": ["list"]},
     "routing": {"send": {"type": "query", "property": "folder"}}},
    {"key": "webhookUrl", "label": "Webhook URL", "kind": "string",
     "show": {"resource": ["mailbox"], "operation": ["watch"]},
     "routing": {"send": {"type": "body", "property": "webhookUrl", "preSend": ["sign"]}}},
    {"key": "verbose", "label": "Verbose", "kind": "boolean", "default": false}
  ]
}`

func convertAcme(t *testing.T) (*nodepack.Pack, *nodepack.ConvertReport) {
	t.Helper()
	pack, report, err := nodepack.ConvertDocument([]byte(acmeTranscription), nodepack.ConvertOptions{PackType: "pack.acmemail"})
	if err != nil {
		t.Fatalf("ConvertDocument() error = %v", err)
	}
	return pack, report
}

func joinSentences(list []string) string {
	return strings.Join(list, "\n")
}

// The transcription converts to a pack whose resource and operation values
// match the source byte for byte, whose unconvertible operations are
// excluded with named diagnostics, and whose report names every exclusion
// and degradation.
func TestConvertDeclarativeNode(t *testing.T) {
	t.Parallel()

	pack, report := convertAcme(t)

	if pack.Type != "pack.acmemail" {
		t.Fatalf("Type = %q, want pack.acmemail", pack.Type)
	}
	if pack.CredentialType != "httpHeaderAuth" {
		t.Fatalf("CredentialType = %q, want httpHeaderAuth", pack.CredentialType)
	}
	if len(pack.Resources) != 2 || pack.Resources[0].Name != "message" || pack.Resources[1].Name != "mailbox" {
		t.Fatalf("resources = %#v, want message and mailbox in picker order", pack.Resources)
	}
	messageOps := pack.Resources[0].Operations
	if len(messageOps) != 3 || messageOps[0].Name != "send" || messageOps[1].Name != "get" || messageOps[2].Name != "list" {
		t.Fatalf("message operations = %#v, want send, get and list", messageOps)
	}
	mailboxOps := pack.Resources[1].Operations
	if len(mailboxOps) != 1 || mailboxOps[0].Name != "list" {
		t.Fatalf("mailbox operations = %#v, want only list", mailboxOps)
	}
	if messageOps[0].Method != "POST" || messageOps[0].URL != "/v1/messages" {
		t.Fatalf("send = %+v, want POST /v1/messages", messageOps[0])
	}
	if messageOps[0].Description != "Send a message" {
		t.Fatalf("send description = %q", messageOps[0].Description)
	}
	if messageOps[2].Pagination == nil || messageOps[2].Pagination.Type != "offset" {
		t.Fatalf("list pagination = %+v, want the offset strategy carried through", messageOps[2].Pagination)
	}
	if len(messageOps[0].Sends) != 5 {
		t.Fatalf("send sends = %+v, want to, subject, headers, tags and scheduledAt", messageOps[0].Sends)
	}
	if report.Converted != 4 {
		t.Fatalf("Converted = %d, want 4", report.Converted)
	}

	excluded := joinSentences(report.Excluded)
	for _, want := range []string{
		`resource "mailbox" operation "watch": property "webhookUrl": preSend hooks sign are JavaScript and are not supported`,
		`resource "message" operation "purge": a function postReceive is JavaScript and is not supported`,
		`resource "mailbox" operation "stream": pagination type "cursor" is not supported`,
		`resource "message" operation "raw": carries a static query fragment the pack format cannot express`,
		`resource "message" operation "draft": declares no URL`,
	} {
		if !strings.Contains(excluded, want) {
			t.Errorf("exclusions miss %q:\n%s", want, excluded)
		}
	}

	notes := joinSentences(report.Notes)
	for _, want := range []string{
		`parameter "headers": kind "fixedCollection" has no pack control`,
		`parameter "owner": kind "resourceLocator" has no pack control`,
		`parameter "timeout" is declared as number and string`,
		`parameter "subject" shows for unknown operation "noSuchOp"`,
		`credential type "httpHeaderAuth" is required; bind a real credential after installing the pack`,
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes miss %q:\n%s", want, notes)
		}
	}
	if issues := nodepack.Validate(pack, "pack.json"); len(issues) != 0 {
		t.Fatalf("Validate() = %v, want no issues", issues)
	}
	if markdown := report.Markdown("pack.acmemail"); !strings.Contains(markdown, "## Operations left out") ||
		!strings.Contains(markdown, "## Degraded or merged") ||
		!strings.Contains(markdown, "n8n-nodes-acme.AcmeMail") {
		t.Fatalf("Markdown() does not follow the report convention:\n%s", markdown)
	}
}

// A converted pack installs through the directory loader and runs on the
// shared interpreter against a stub, driven by the exact resource and
// operation values a workflow JSON authored against the source node carries.
func TestConvertedPackLoadsFromDiskAndRuns(t *testing.T) {
	t.Parallel()

	pack, _ := convertAcme(t)

	var calls []struct {
		method, path, body, query, apiKey string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, struct {
			method, path, body, query, apiKey string
		}{r.Method, r.URL.Path, string(raw), r.URL.RawQuery, r.Header.Get("X-Api-Key")})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"id": "m1"}]}`))
	}))
	defer server.Close()

	// The transcription points at the real API; the test retargets the
	// converted pack's base URL at the stub before installing it.
	pack.RequestDefaults.BaseURL = server.URL
	manifest, err := nodepack.Encode(pack)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	dir := t.TempDir()
	writePack(t, dir, "acmemail", manifest)

	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("Parse(server.URL) error = %v", err)
	}
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{endpoint.Host}
	set := installDirDeps(policy)
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	executor, _ := set.executors.Lookup(routing.ExecutorID)
	irDefinition, found := set.definitions.Lookup("pack.acmemail", workflow.V(1))
	if !found {
		t.Fatal("the converted pack did not register")
	}
	run := func(parameters map[string]any) (workflow.NodeOutput, error) {
		return executor.Execute(context.Background(), workflow.IRNode{
			ID: "acme-1", Name: "Acme Mail", Type: "pack.acmemail", TypeVersion: workflow.V(1),
			Parameters:  parameters,
			Credentials: map[string]string{"httpHeaderAuth": "cred-1"},
			Definition:  irDefinition,
		}, workflow.NodeInput{}, engine.Request{Credentials: convertStubCredential{}})
	}

	output, err := run(map[string]any{
		"resource": "message", "operation": "send",
		"to": "a@example.com", "subject": "hi",
	})
	if err != nil {
		t.Fatalf("Execute(send) error = %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %#v, want one", calls)
	}
	sent := calls[0]
	if sent.method != "POST" || sent.path != "/v1/messages" {
		t.Fatalf("call = %+v, want POST /v1/messages", sent)
	}
	for _, want := range []string{`"to":"a@example.com"`, `"subject":"hi"`} {
		if !strings.Contains(sent.body, want) {
			t.Fatalf("body = %s, want %s", sent.body, want)
		}
	}
	if sent.apiKey != "s3cr3t" {
		t.Fatalf("X-Api-Key = %q, want the bound credential applied", sent.apiKey)
	}
	if output[0][0].JSON["data"] == nil {
		t.Fatalf("item = %#v, want the API's answer", output[0][0].JSON)
	}

	output, err = run(map[string]any{
		"resource": "message", "operation": "get", "messageId": "m42",
	})
	if err != nil {
		t.Fatalf("Execute(get) error = %v", err)
	}
	if got := calls[1].path; got != "/v1/messages/m42" {
		t.Fatalf("path = %q, want the path-placed message id", got)
	}
	if output[0][0].JSON["id"] != "m1" {
		t.Fatalf("item = %#v, want the rootProperty extraction", output[0][0].JSON)
	}

	// An excluded operation has no cascade route: the values from the
	// source workflow select nothing, loudly rather than wrongly.
	if _, err := run(map[string]any{"resource": "mailbox", "operation": "watch"}); err == nil ||
		!strings.Contains(err.Error(), `no request is declared for resource "mailbox" operation "watch"`) {
		t.Fatalf("Execute(watch) error = %v, want the undeclared-pair refusal", err)
	}
}

type convertStubCredential struct{}

func (convertStubCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "Acme", Type: "httpHeaderAuth",
		Fields: map[string]string{"name": "X-Api-Key", "value": "s3cr3t"},
	}, nil
}

// A node with a real execute() is refused with the reason named, and no
// partial pack comes back.
func TestConvertRefusesProgrammaticNode(t *testing.T) {
	t.Parallel()

	programmatic := strings.Replace(acmeTranscription, `"hasExecute": false`, `"hasExecute": true`, 1)
	pack, _, err := nodepack.ConvertDocument([]byte(programmatic), nodepack.ConvertOptions{PackType: "pack.acmemail"})
	if err == nil || !strings.Contains(err.Error(), "defines a programmatic execute()") {
		t.Fatalf("ConvertDocument() error = %v, want the execute() refusal", err)
	}
	if pack != nil {
		t.Fatalf("pack = %+v, want nil: refusals write no partial pack", pack)
	}
}

func TestConvertRefusalsNameTheReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(string) string
		options nodepack.ConvertOptions
		want    string
	}{
		{
			name: "no resource picker",
			mutate: func(s string) string {
				return strings.Replace(s, `{"key": "resource"`, `{"key": "res"`, 1)
			},
			options: nodepack.ConvertOptions{PackType: "pack.acmemail"},
			want:    `declares no "resource" picker`,
		},
		{
			name:    "reserved namespace",
			mutate:  func(s string) string { return s },
			options: nodepack.ConvertOptions{PackType: "kilasflow.acmemail"},
			want:    "reserved",
		},
		{
			name:    "missing pack type",
			mutate:  func(s string) string { return s },
			options: nodepack.ConvertOptions{},
			want:    "a pack type is required",
		},
		{
			name: "unknown field fails loudly",
			mutate: func(s string) string {
				return strings.Replace(s, `"hasExecute": false`, `"hasExecute": false, "dynamicBuilder": true`, 1)
			},
			options: nodepack.ConvertOptions{PackType: "pack.acmemail"},
			want:    "decode declarative node",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pack, _, err := nodepack.ConvertDocument([]byte(tc.mutate(acmeTranscription)), tc.options)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ConvertDocument() error = %v, want %q", err, tc.want)
			}
			if pack != nil {
				t.Fatalf("pack = %+v, want nil", pack)
			}
		})
	}
}

// When every operation is excluded the conversion fails, but the report
// still comes back so the caller can render the coverage.
func TestConvertWithNothingLeftFailsWithReport(t *testing.T) {
	t.Parallel()

	pack, report, err := nodepack.ConvertDocument([]byte(`{
	  "sourceType": "n8n-nodes-acme.Empty",
	  "displayName": "Empty",
	  "hasExecute": false,
	  "properties": [
	    {"key": "resource", "kind": "options", "options": [{"label": "R", "value": "r"}]},
	    {"key": "operation", "kind": "options", "options": [
	      {"label": "O", "value": "o", "show": {"resource": ["r"]},
	       "routing": {"request": {"method": "GET", "url": "/x"},
	                   "operations": {"pagination": {"type": "cursor", "properties": {
	                     "limitParameter": "limit", "offsetParameter": "cursor",
	                     "pageSize": 10, "type": "query"}}}}}
	    ]}
	  ]
	}`), nodepack.ConvertOptions{PackType: "pack.empty"})
	if err == nil || !strings.Contains(err.Error(), "no convertible operations remain") {
		t.Fatalf("ConvertDocument() error = %v, want the nothing-left refusal", err)
	}
	if pack != nil {
		t.Fatalf("pack = %+v, want nil", pack)
	}
	if report == nil || len(report.Excluded) != 1 {
		t.Fatalf("report = %+v, want the one exclusion", report)
	}
}
