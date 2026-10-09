package nodes_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlbuild"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
)

// The text every injection case sends as a model argument. It reaches the
// service as these characters, or the tool evaluated the model's value.
const modelTemplate = "{{ $env.SECRET }}"

const secretValue = "s3cr3t-value"

// recordingModel serves one chat-completion body per turn and keeps every
// request, so a test can read what the agent told the model a tool returned.
type recordingModel struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []string
}

func newRecordingModel(t *testing.T, bodies ...string) *recordingModel {
	t.Helper()
	model := &recordingModel{}
	model.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		model.mu.Lock()
		turn := len(model.requests)
		model.requests = append(model.requests, string(raw))
		model.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if turn >= len(bodies) {
			_, _ = w.Write([]byte(assistantAnswer("out of script")))
			return
		}
		_, _ = w.Write([]byte(bodies[turn]))
	}))
	t.Cleanup(model.server.Close)
	return model
}

// toolTurns returns the content of every tool message the model was sent.
func (model *recordingModel) toolTurns(t *testing.T) []string {
	t.Helper()
	model.mu.Lock()
	defer model.mu.Unlock()
	var turns []string
	if len(model.requests) == 0 {
		return nil
	}
	var last struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(model.requests[len(model.requests)-1]), &last); err != nil {
		t.Fatalf("decode the model request: %v", err)
	}
	for _, message := range last.Messages {
		if message.Role == "tool" {
			text, _ := message.Content.(string)
			turns = append(turns, text)
		}
	}
	return turns
}

func toolCall(name string, arguments map[string]any) string {
	encodedArguments, _ := json.Marshal(arguments)
	call, _ := json.Marshal(map[string]any{"name": name, "arguments": string(encodedArguments)})
	return assistantToolCalls(string(call))
}

// formHarness is the catalogue, the executors and an agent that can run tool
// forms, composed as the server composes them.
type formHarness struct {
	definitions *node.Registry
	executors   *engine.Registry
	agent       engine.Executor
}

func newFormHarness(t *testing.T, policy safehttp.Policy) formHarness {
	t.Helper()
	definitions, _, executors := toolFormCatalogue(t, policy)
	if err := nodes.RegisterExecutors(executors, policy, sqlGuard(), ai.NewLoopRuntime(), nil, nil,
		nodes.WithNodeCatalog(definitions)); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	agent, _ := executors.Lookup(nodes.AgentExecutorID)
	return formHarness{definitions: definitions, executors: executors, agent: agent}
}

// descriptor runs the tool node itself, as the runner would, and returns the
// item it hands the agent's tools port.
func (harness formHarness) descriptor(t *testing.T, toolType string, version workflow.TypeVersion, name string, parameters map[string]any, credentials map[string]string) workflow.Item {
	t.Helper()
	definition, found := harness.definitions.Lookup(toolType, version)
	if !found {
		t.Fatalf("%s v%s is not in the catalogue", toolType, version)
	}
	if err := definition.Validate(workflow.Node{Parameters: parameters}); err != nil {
		t.Fatalf("the tool node does not validate: %v", err)
	}
	executor, _ := harness.executors.Lookup(definition.ExecutorID)
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "tool", Name: name, Type: toolType, TypeVersion: version,
		Parameters: parameters, Credentials: credentials, Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("tool node Execute() error = %v", err)
	}
	return output[0][0]
}

func runFormAgent(t *testing.T, agent engine.Executor, model *recordingModel, tools []workflow.Item, credentials engine.CredentialResolver) (workflow.NodeOutput, error) {
	t.Helper()
	return agent.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Do it."}), workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {modelItem(model.server.URL)},
		"tools": tools,
	}, engine.Request{
		Credentials: credentials,
		Execution:   agentExecution(),
		Env:         map[string]string{"SECRET": secretValue},
	})
}

func modelCredential() engine.Credential {
	return engine.Credential{
		ID: "cred-key", Name: "Provider", Type: "httpBearerAuth",
		Fields: map[string]string{"token": "sk-test"},
	}
}

// --- Gmail ------------------------------------------------------------------

func TestTheGmailToolSendsWhatTheModelAddressesAsText(t *testing.T) {
	var sent []string
	var mu sync.Mutex
	gmail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gmail/v1/users/me/messages/send" {
			t.Errorf("Gmail path = %s", r.URL.Path)
		}
		var body struct {
			Raw string `json:"raw"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		decoded, _ := base64.RawURLEncoding.DecodeString(body.Raw)
		mu.Lock()
		sent = append(sent, string(decoded))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"id":"sent-1","threadId":"t-1"}`)
	}))
	t.Cleanup(gmail.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", gmail.URL+"/gmail/v1"))

	model := newRecordingModel(t,
		toolCall("Send_Email", map[string]any{"to": "ops@example.com", "body": modelTemplate}),
		assistantAnswer("sent"),
	)
	harness := newFormHarness(t, allowing(model.server, gmail))
	tool := harness.descriptor(t, nodes.GmailToolNodeType, workflow.V(1), "Send Email", map[string]any{
		"toolDescription": "Sends an email.", "operation": "send",
		"to":      formExpression("{{ $fromAI('to', 'who receives it') }}"),
		"subject": "Report",
		"message": formExpression("{{ $fromAI('body', 'the text') }}"),
	}, map[string]string{nodes.GmailCredentialType: "cred-gmail"})

	output, err := runFormAgent(t, harness.agent, model, []workflow.Item{tool}, credentialMap{
		"cred-key": modelCredential(),
		"cred-gmail": {
			ID: "cred-gmail", Name: "Mailbox", Type: nodes.GmailCredentialType,
			Fields:         map[string]string{"access_token": "token"},
			AllowedDomains: []string{httptestHost(gmail.URL)},
		},
	})
	if err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if output[0][0].JSON["output"] != "sent" {
		t.Fatalf("agent output = %#v", output[0][0].JSON)
	}
	if len(sent) != 1 {
		t.Fatalf("Gmail received %d sends, want 1: tool turns %v", len(sent), model.toolTurns(t))
	}
	if !strings.Contains(sent[0], "To: ops@example.com\r\n") {
		t.Errorf("the mail is not addressed to the model's recipient:\n%s", sent[0])
	}
	if strings.Contains(sent[0], secretValue) || !strings.HasSuffix(sent[0], "\r\n\r\n"+modelTemplate) {
		t.Errorf("the model's body was evaluated rather than sent as text:\n%s", sent[0])
	}
	if turns := model.toolTurns(t); len(turns) != 1 || !strings.Contains(turns[0], "sent-1") {
		t.Errorf("tool turns = %v, want Gmail's answer", turns)
	}
}

func TestTheGmailToolListsWithTheModelsSearch(t *testing.T) {
	var query string
	gmail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			query = r.URL.Query().Get("q")
			_, _ = io.WriteString(w, `{"messages":[{"id":"m1","threadId":"t1"}]}`)
		case "/gmail/v1/users/me/messages/m1":
			_, _ = io.WriteString(w, `{"id":"m1","threadId":"t1","snippet":"invoice attached"}`)
		default:
			t.Errorf("Gmail path = %s", r.URL.Path)
		}
	}))
	t.Cleanup(gmail.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", gmail.URL+"/gmail/v1"))

	model := newRecordingModel(t,
		toolCall("Find_Mail", map[string]any{"sender": "billing@example.com"}),
		assistantAnswer("found it"),
	)
	harness := newFormHarness(t, allowing(model.server, gmail))
	tool := harness.descriptor(t, nodes.GmailToolNodeType, workflow.V(1), "Find Mail", map[string]any{
		"toolDescription": "Finds mail.", "operation": "getAll",
		"filters": map[string]any{"sender": formExpression("{{ $fromAI('sender') }}")},
	}, map[string]string{nodes.GmailCredentialType: "cred-gmail"})

	if _, err := runFormAgent(t, harness.agent, model, []workflow.Item{tool}, credentialMap{
		"cred-key": modelCredential(),
		"cred-gmail": {
			ID: "cred-gmail", Name: "Mailbox", Type: nodes.GmailCredentialType,
			Fields: map[string]string{"access_token": "token"}, AllowedDomains: []string{httptestHost(gmail.URL)},
		},
	}); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if query != "from:billing@example.com" {
		t.Errorf("q = %q, want the model's sender", query)
	}
	if turns := model.toolTurns(t); len(turns) != 1 || !strings.Contains(turns[0], "invoice attached") {
		t.Errorf("tool turns = %v, want the listed message", turns)
	}
}

func TestTheGmailToolGetsTheMessageTheModelNames(t *testing.T) {
	var path string
	gmail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"id":"m 1","threadId":"t1","snippet":"the quarterly numbers"}`)
	}))
	t.Cleanup(gmail.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", gmail.URL+"/gmail/v1"))

	model := newRecordingModel(t,
		toolCall("Read_Mail", map[string]any{"id": "m 1/../profile"}),
		assistantAnswer("read it"),
	)
	harness := newFormHarness(t, allowing(model.server, gmail))
	tool := harness.descriptor(t, nodes.GmailToolNodeType, workflow.V(1), "Read Mail", map[string]any{
		"toolDescription": "Reads one message.", "operation": "get",
		"messageId": formExpression("{{ $fromAI('id', 'the message id') }}"),
	}, map[string]string{nodes.GmailCredentialType: "cred-gmail"})

	if _, err := runFormAgent(t, harness.agent, model, []workflow.Item{tool}, credentialMap{
		"cred-key": modelCredential(),
		"cred-gmail": {
			ID: "cred-gmail", Type: nodes.GmailCredentialType,
			Fields: map[string]string{"access_token": "token"}, AllowedDomains: []string{httptestHost(gmail.URL)},
		},
	}); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	// The model's id is one path segment, escaped, never a path of its own.
	if path != "/gmail/v1/users/me/messages/m%201%2F..%2Fprofile" {
		t.Errorf("Gmail path = %q, want the model's id escaped into one segment", path)
	}
	if turns := model.toolTurns(t); len(turns) != 1 || !strings.Contains(turns[0], "quarterly numbers") {
		t.Errorf("tool turns = %v, want the message", turns)
	}
}

// A tool form reads the item the agent is answering, as an n8n sub-node reads
// its root node's input: `$json` and `$input` are that item, and the model's
// values still arrive only through $fromAI.
func TestAToolFormReadsTheAgentsInputItem(t *testing.T) {
	var sent []string
	gmail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Raw string `json:"raw"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		decoded, _ := base64.RawURLEncoding.DecodeString(body.Raw)
		sent = append(sent, string(decoded))
		_, _ = io.WriteString(w, `{"id":"sent-1"}`)
	}))
	t.Cleanup(gmail.Close)
	t.Cleanup(nodes.SetGoogleAPIRootsForTest("", gmail.URL+"/gmail/v1"))

	model := newRecordingModel(t,
		toolCall("Reply", map[string]any{"body": "Thanks!"}),
		assistantAnswer("replied"),
		toolCall("Reply", map[string]any{"body": "Noted."}),
		assistantAnswer("replied"),
	)
	harness := newFormHarness(t, allowing(model.server, gmail))
	tool := harness.descriptor(t, nodes.GmailToolNodeType, workflow.V(1), "Reply", map[string]any{
		"toolDescription": "Replies to the sender.", "operation": "send",
		"to":      formExpression("{{ $json.from }}"),
		"subject": formExpression("Re: {{ $input.first().json.topic }}"),
		"message": formExpression("{{ $fromAI('body', 'the reply') }}"),
	}, map[string]string{nodes.GmailCredentialType: "cred-gmail"})

	_, err := harness.agent.Execute(context.Background(), agentIR(t, map[string]any{"prompt": "Reply."}), workflow.NodeInput{
		"main": {
			{JSON: map[string]any{"from": "ana@example.com", "topic": "Q3"}},
			{JSON: map[string]any{"from": "budi@example.com", "topic": "Q4"}},
		},
		"model": {modelItem(model.server.URL)},
		"tools": {tool},
	}, engine.Request{
		Credentials: credentialMap{
			"cred-key": modelCredential(),
			"cred-gmail": {
				ID: "cred-gmail", Type: nodes.GmailCredentialType,
				Fields: map[string]string{"access_token": "token"}, AllowedDomains: []string{httptestHost(gmail.URL)},
			},
		},
		Execution: agentExecution(),
	})
	if err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("Gmail received %d sends, want one per agent item", len(sent))
	}
	for index, want := range []struct{ to, subject, body string }{
		{"ana@example.com", "Re: Q3", "Thanks!"},
		{"budi@example.com", "Re: Q4", "Noted."},
	} {
		if !strings.Contains(sent[index], "To: "+want.to+"\r\n") || !strings.Contains(sent[index], "Subject: "+want.subject+"\r\n") ||
			!strings.HasSuffix(sent[index], want.body) {
			t.Errorf("send %d =\n%s\nwant to %s, subject %q and body %q", index, sent[index], want.to, want.subject, want.body)
		}
	}
}

// --- Telegram ---------------------------------------------------------------

func TestTheTelegramToolRunsThePacksOperationWithTheModelsText(t *testing.T) {
	type call struct {
		path string
		body map[string]any
	}
	var calls []call
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, call{path: r.URL.Path, body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11}}`))
	}))
	t.Cleanup(bot.Close)

	model := newRecordingModel(t,
		toolCall("Notify_Team", map[string]any{"text": modelTemplate}),
		assistantAnswer("notified"),
	)
	harness := newFormHarness(t, allowing(model.server, bot))
	tool := harness.descriptor(t, nodes.TelegramToolNodeType, workflow.V(1), "Notify Team", map[string]any{
		"toolDescription": "Messages the team.", "resource": "message", "operation": "sendMessage",
		"chatId": "-100123", "text": formExpression("{{ $fromAI('text', 'what to say') }}"),
	}, map[string]string{telegram.CredentialType: "cred-bot"})

	if _, err := runFormAgent(t, harness.agent, model, []workflow.Item{tool}, credentialMap{
		"cred-key": modelCredential(),
		"cred-bot": {
			ID: "cred-bot", Name: "Bot", Type: telegram.CredentialType,
			Fields: map[string]string{"accessToken": "123:ABC", "baseUrl": bot.URL},
		},
	}); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0].path, "/sendMessage") {
		t.Fatalf("bot calls = %#v, want one sendMessage: tool turns %v", calls, model.toolTurns(t))
	}
	if calls[0].body["chat_id"] != "-100123" {
		t.Errorf("chat_id = %#v, want the author's chat", calls[0].body["chat_id"])
	}
	if calls[0].body["text"] != modelTemplate {
		t.Errorf("text = %#v, want the model's text verbatim", calls[0].body["text"])
	}
	if turns := model.toolTurns(t); len(turns) != 1 || !strings.Contains(turns[0], "message_id") {
		t.Errorf("tool turns = %v, want the Bot API's answer", turns)
	}
}

// --- PostgreSQL ---------------------------------------------------------------

// capturedSQL stands in for the PostgreSQL executor: it resolves the
// parameters the way the real one does, builds the statement, and keeps both.
type capturedSQL struct {
	mu         sync.Mutex
	statements []sqlnode.Statement
	nodes      []workflow.IRNode
	result     map[string]any
}

func (captured *capturedSQL) Execute(_ context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	resolved, err := expression.Resolve(ir.Parameters, request.ExpressionContext(input["main"][0], input, 0))
	if err != nil {
		return nil, err
	}
	statement, err := nodes.BuildSQLStatementForTest(sqlbuild.Postgres, resolved)
	if err != nil {
		return nil, err
	}
	captured.mu.Lock()
	captured.statements = append(captured.statements, statement)
	captured.nodes = append(captured.nodes, ir)
	captured.mu.Unlock()
	result := captured.result
	if result == nil {
		result = map[string]any{"id": float64(7)}
	}
	return workflow.NodeOutput{{{JSON: result}}}, nil
}

// postgresFormAgent is an agent whose PostgreSQL executor is the capture.
func postgresFormAgent(t *testing.T, model *recordingModel, captured *capturedSQL) (*nodes.AgentExecutor, formHarness) {
	t.Helper()
	harness := newFormHarness(t, allowing(model.server))
	executors := engine.NewRegistry()
	if err := executors.Register(nodes.PostgresV2ExecutorID, captured); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return nodes.NewAgentExecutor(ai.NewLoopRuntime(), allowing(model.server), nil,
		nodes.WithAgentToolForms(harness.definitions, executors)), harness
}

func postgresCredentials() credentialMap {
	return credentialMap{
		"cred-key": modelCredential(),
		"cred-db":  {ID: "cred-db", Name: "Database", Type: "postgres", Fields: map[string]string{"host": "db.example.com"}},
	}
}

func TestThePostgresToolBindsTheModelsValues(t *testing.T) {
	t.Parallel()

	model := newRecordingModel(t,
		toolCall("Find_Customer", map[string]any{"email": modelTemplate}),
		assistantAnswer("found"),
	)
	captured := &capturedSQL{}
	agent, harness := postgresFormAgent(t, model, captured)
	tool := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Find Customer", map[string]any{
		"toolDescription": "Looks a customer up by email.", "operation": "select",
		"schema": map[string]any{"__rl": true, "mode": "name", "value": "public"},
		"table":  map[string]any{"__rl": true, "mode": "name", "value": "customers"},
		"where": []any{map[string]any{
			"field": "email", "operator": "equals", "value": formExpression("{{ $fromAI('email', 'the address') }}"),
		}},
	}, map[string]string{"postgres": "cred-db"})

	if _, err := runFormAgent(t, agent, model, []workflow.Item{tool}, postgresCredentials()); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if len(captured.statements) != 1 {
		t.Fatalf("statements = %#v, want one: tool turns %v", captured.statements, model.toolTurns(t))
	}
	statement := captured.statements[0]
	if !strings.Contains(statement.SQL, `"email" = $1`) || strings.Contains(statement.SQL, modelTemplate) {
		t.Errorf("SQL = %q, want the value bound to $1", statement.SQL)
	}
	if len(statement.Parameters) != 1 || statement.Parameters[0] != modelTemplate {
		t.Errorf("bound = %#v, want the model's text verbatim", statement.Parameters)
	}
	ran := captured.nodes[0]
	if ran.Type != nodes.PostgresNodeType || ran.TypeVersion != workflow.V(2) || ran.Credentials["postgres"] != "cred-db" {
		t.Errorf("the base ran as %s v%s with %v, want kilasflow.postgres v2 with the tool's credential", ran.Type, ran.TypeVersion, ran.Credentials)
	}
	if _, kept := ran.Parameters["toolDescription"]; kept {
		t.Error("the tool's own description reached the base node's parameters")
	}
}

// The table is the model's to choose when the author lets it (n8n does), and
// it is safe because the builder quotes every identifier: a hostile name is
// one quoted identifier, never a second statement.
func TestThePostgresToolQuotesATableTheModelNames(t *testing.T) {
	t.Parallel()

	hostile := `x"; DROP TABLE y; --`
	model := newRecordingModel(t,
		toolCall("Read_Table", map[string]any{"table": hostile}),
		assistantAnswer("read"),
	)
	captured := &capturedSQL{}
	agent, harness := postgresFormAgent(t, model, captured)
	tool := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Read Table", map[string]any{
		"toolDescription": "Reads a table.", "operation": "select",
		"schema": map[string]any{"__rl": true, "mode": "name", "value": "public"},
		"table":  map[string]any{"__rl": true, "mode": "name", "value": formExpression("{{ $fromAI('table') }}")},
	}, map[string]string{"postgres": "cred-db"})

	if _, err := runFormAgent(t, agent, model, []workflow.Item{tool}, postgresCredentials()); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if len(captured.statements) != 1 {
		t.Fatalf("statements = %#v, want one", captured.statements)
	}
	// The whole statement, so nothing can follow the quoted name: the
	// embedded quote is doubled and the separators stay inside the identifier.
	if sql, want := captured.statements[0].SQL, `SELECT * FROM "public"."x""; DROP TABLE y; --"`; sql != want {
		t.Fatalf("SQL = %q, want %q", sql, want)
	}
}

func TestAToolResultIsCappedWithANote(t *testing.T) {
	t.Parallel()

	model := newRecordingModel(t,
		toolCall("Dump", map[string]any{}),
		assistantAnswer("too much"),
	)
	captured := &capturedSQL{result: map[string]any{"blob": strings.Repeat("a", 300*1024)}}
	agent, harness := postgresFormAgent(t, model, captured)
	tool := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Dump", map[string]any{
		"toolDescription": "Dumps a table.", "operation": "select",
		"table": map[string]any{"__rl": true, "mode": "name", "value": "big"},
	}, map[string]string{"postgres": "cred-db"})

	if _, err := runFormAgent(t, agent, model, []workflow.Item{tool}, postgresCredentials()); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	turns := model.toolTurns(t)
	if len(turns) != 1 || len(turns[0]) > 257*1024 || !strings.Contains(turns[0], "[truncated:") {
		t.Fatalf("tool turn is %d bytes, want at most 256 KiB plus the truncation note", len(turns[0]))
	}
}

// --- What the agent refuses ---------------------------------------------------

func TestTheAgentResolvesAToolFormsBaseItselfOrRefuses(t *testing.T) {
	t.Parallel()

	model := newRecordingModel(t, assistantAnswer("unused"))
	harness := newFormHarness(t, allowing(model.server))
	credentials := postgresCredentials()
	valid := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Find", map[string]any{
		"toolDescription": "Finds.", "operation": "select",
		"table": map[string]any{"__rl": true, "mode": "name", "value": "t"},
	}, map[string]string{"postgres": "cred-db"})

	forged := func(change func(descriptor map[string]any)) workflow.Item {
		descriptor := map[string]any{}
		for key, value := range valid.JSON["$ai"].(map[string]any) {
			descriptor[key] = value
		}
		change(descriptor)
		return workflow.Item{JSON: map[string]any{"$ai": descriptor}}
	}
	cases := map[string]struct {
		agent engine.Executor
		tool  workflow.Item
		want  string
	}{
		"a type that is not a tool form": {
			agent: harness.agent,
			tool:  forged(func(d map[string]any) { d["toolType"] = nodes.HTTPToolNodeType }),
			want:  "not a tool form",
		},
		"a model-chosen operation in a forged descriptor": {
			agent: harness.agent,
			tool: forged(func(d map[string]any) {
				d["parameters"] = map[string]any{"operation": "$fromai('op')", "table": d["parameters"].(map[string]any)["table"]}
			}),
			want: "operation",
		},
		"plain $fromAI in a forged descriptor": {
			// The backstop for the validation rule: a tool form never fills
			// plain text with the model's words.
			agent: harness.agent,
			tool: forged(func(d map[string]any) {
				parameters := map[string]any{}
				for key, value := range d["parameters"].(map[string]any) {
					parameters[key] = value
				}
				parameters["limit"] = "$FromAI ('limit')"
				d["parameters"] = parameters
			}),
			want: "plain text",
		},
		"a runtime with no catalogue": {
			agent: nodes.NewAgentExecutor(ai.NewLoopRuntime(), allowing(model.server), nil),
			tool:  valid,
			want:  "no node catalogue",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runFormAgent(t, testCase.agent, model, []workflow.Item{testCase.tool}, credentials)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Execute() error = %v, want one mentioning %q", err, testCase.want)
			}
		})
	}
}

// A condition whose column an expression computes, and which comes out empty
// (a model that sent no column name, an item without the field), is refused
// rather than dropped: dropping it makes a select or a delete match more rows
// than the author wrote it to. A row the author left blank is still skipped.
func TestAComputedConditionColumnThatResolvesEmptyIsRefused(t *testing.T) {
	t.Parallel()

	executor := nodes.NewPostgresV2Executor(sqlGuard(), sqlnode.DefaultCeiling())
	run := func(where []any) error {
		_, err := executor.Execute(context.Background(), workflow.IRNode{
			ID: "db", Name: "Database", Type: nodes.PostgresNodeType, TypeVersion: workflow.V(2),
			Parameters: map[string]any{
				"operation": "deleteTable", "deleteCommand": "delete",
				"table": map[string]any{"__rl": true, "mode": "name", "value": "orders"},
				"where": where,
			},
			Credentials: map[string]string{"postgres": "cred-db"},
		}, workflow.NodeInput{"main": {{JSON: map[string]any{"column": "", "id": float64(7)}}}}, engine.Request{
			Credentials: credentialMap{"cred-db": {ID: "cred-db", Type: "postgres", Fields: map[string]string{"host": "db.invalid"}}},
		})
		return err
	}
	err := run([]any{
		map[string]any{"field": "status", "operator": "equals", "value": "open"},
		map[string]any{"field": formExpression("{{ $json.column }}"), "operator": "equals", "value": formExpression("{{ $json.id }}")},
	})
	if err == nil || !strings.Contains(err.Error(), "condition 2") {
		t.Fatalf("Execute() error = %v, want condition 2 refused before anything connects", err)
	}
	// A blank row is the author's, and is skipped as before: the run gets as
	// far as connecting.
	err = run([]any{
		map[string]any{"field": "status", "operator": "equals", "value": "open"},
		map[string]any{"field": "", "operator": "equals", "value": ""},
	})
	if err == nil || strings.Contains(err.Error(), "condition") {
		t.Fatalf("Execute(blank row) error = %v, want it to reach the connection", err)
	}
}

// The refusal covers a row that is itself an expression and a `where` that is
// one: a computed row that is not a condition with a column is refused, and so
// is a computed `where` that is not a list. Only a row the author left blank
// is skipped. And it applies only where conditions are read: a `where` left
// over from an earlier operation does not stop an insert, an update, an upsert
// or a query.
func TestComputedConditionsAreRefusedWhereTheyAreReadAndOnlyThere(t *testing.T) {
	t.Parallel()

	executor := nodes.NewPostgresV2Executor(sqlGuard(), sqlnode.DefaultCeiling())
	item := map[string]any{"row": map[string]any{"field": "", "operator": "equals", "value": float64(1)}, "column": "", "nothing": nil}
	run := func(parameters map[string]any) error {
		_, err := executor.Execute(context.Background(), workflow.IRNode{
			ID: "db", Name: "Database", Type: nodes.PostgresNodeType, TypeVersion: workflow.V(2),
			Parameters: parameters, Credentials: map[string]string{"postgres": "cred-db"},
		}, workflow.NodeInput{"main": {{JSON: item}}}, engine.Request{
			Credentials: credentialMap{"cred-db": {ID: "cred-db", Type: "postgres", Fields: map[string]string{"host": "db.invalid"}}},
		})
		return err
	}
	deleting := func(where any) map[string]any {
		return map[string]any{
			"operation": "deleteTable", "deleteCommand": "delete",
			"table": map[string]any{"__rl": true, "mode": "name", "value": "orders"}, "where": where,
		}
	}
	status := map[string]any{"field": "status", "operator": "equals", "value": "open"}
	for name, where := range map[string]any{
		"a computed row with no column":    []any{status, formExpression("{{ $json.row }}")},
		"a computed row that is not a row": []any{status, formExpression("{{ $json.nothing }}")},
		"a computed where with an empty row": formExpression(
			"{{ [{field: 'status', operator: 'equals', value: 'open'}, $json.row] }}"),
		"a computed where that is not a list": formExpression("{{ $json.nothing }}"),
	} {
		if err := run(deleting(where)); err == nil || !strings.Contains(err.Error(), "condition") {
			t.Errorf("%s: Execute() error = %v, want the condition refused before anything connects", name, err)
		}
	}

	stale := []any{map[string]any{"field": formExpression("{{ $json.column }}"), "operator": "equals", "value": "x"}}
	columns := map[string]any{"mappingMode": "defineBelow", "value": map[string]any{"id": float64(1)},
		"matchingColumns": []any{"id"},
		"schema":          []any{map[string]any{"id": "id", "displayName": "id", "type": "number"}}}
	for _, operation := range []string{"insert", "update", "upsert", "executeQuery"} {
		err := run(map[string]any{
			"operation": operation, "query": "SELECT 1",
			"table":   map[string]any{"__rl": true, "mode": "name", "value": "orders"},
			"columns": columns, "where": stale,
		})
		if err == nil || strings.Contains(err.Error(), "condition") {
			t.Errorf("%s with a stale where: Execute() error = %v, want it to reach the connection", operation, err)
		}
	}
}

// A descriptor is data: one that names an executor of its choosing still runs
// the base node the tool type names, found on the server.
func TestADescriptorCannotNameItsExecutor(t *testing.T) {
	t.Parallel()

	model := newRecordingModel(t,
		toolCall("Find", map[string]any{}),
		assistantAnswer("found"),
	)
	captured := &capturedSQL{}
	agent, harness := postgresFormAgent(t, model, captured)
	tool := harness.descriptor(t, nodes.PostgresToolNodeType, workflow.V(2), "Find", map[string]any{
		"toolDescription": "Finds.", "operation": "select",
		"table": map[string]any{"__rl": true, "mode": "name", "value": "t"},
	}, map[string]string{"postgres": "cred-db"})
	descriptor := tool.JSON["$ai"].(map[string]any)
	descriptor["executorId"] = nodes.HTTPExecutorID
	descriptor["executor"] = nodes.HTTPExecutorID

	if _, err := runFormAgent(t, agent, model, []workflow.Item{tool}, postgresCredentials()); err != nil {
		t.Fatalf("agent Execute() error = %v", err)
	}
	if len(captured.statements) != 1 || captured.nodes[0].Type != nodes.PostgresNodeType {
		t.Fatalf("the PostgreSQL executor ran %d statements, want the base node to run once whatever the descriptor named", len(captured.statements))
	}
}

// A tenant the base node is scoped away from cannot run it through its tool:
// the agent resolves the base through the running tenant's catalogue.
func TestAToolFormsBaseIsResolvedForTheRunningTenant(t *testing.T) {
	t.Parallel()

	model := newRecordingModel(t, assistantAnswer("unused"))
	harness := newFormHarness(t, allowing(model.server))
	tool := harness.descriptor(t, nodes.TelegramToolNodeType, workflow.V(1), "Notify", map[string]any{
		"toolDescription": "Notifies.", "resource": "message", "operation": "sendMessage",
		"chatId": "1", "text": "hello",
	}, map[string]string{telegram.CredentialType: "cred-bot"})
	if err := harness.definitions.ApplyVisibility(map[string][]string{telegram.NodeType: {"some-other-tenant"}}); err != nil {
		t.Fatalf("ApplyVisibility() error = %v", err)
	}
	_, err := runFormAgent(t, harness.agent, model, []workflow.Item{tool}, credentialMap{"cred-key": modelCredential()})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("Execute() error = %v, want the hidden base refused", err)
	}
	if errors.Is(err, safehttp.ErrBlocked) {
		t.Fatal("refused by the egress policy, not by the tenant scope")
	}
}
