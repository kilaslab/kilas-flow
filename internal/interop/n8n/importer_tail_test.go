package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
)

// TestWebhookMethodDefaultsToGet covers n8n's own default.
//
// n8n's Webhook defaults to GET and omits the parameter when it holds the
// default, so a method-less webhook is a GET webhook. Reading the absence as
// POST minted a POST-only endpoint and every caller the workflow was written
// for got a 404 — including two of the four templates that used to activate.
func TestWebhookMethodDefaultsToGet(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Menu",
	  "nodes": [
	    {"id":"a","name":"GET /menu","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
	     "parameters":{"path":"menu","responseMode":"lastNode"}}
	  ],
	  "connections": {}
	}`)
	webhook := nodeByName(result.Document, "GET /menu")
	if webhook.Parameters["httpMethod"] != "GET" {
		t.Errorf("httpMethod = %#v, want GET", webhook.Parameters["httpMethod"])
	}

	// An explicit method is carried, and HEAD is one of them.
	explicit := importFixture(t, `{
	  "name": "Head",
	  "nodes": [
	    {"id":"a","name":"Head","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
	     "parameters":{"path":"ping","httpMethod":"head","responseMode":"lastNode"}}
	  ],
	  "connections": {}
	}`)
	if got := nodeByName(explicit.Document, "Head").Parameters["httpMethod"]; got != "HEAD" {
		t.Errorf("httpMethod = %#v, want HEAD", got)
	}

	// Several methods on one path are n8n 2.1's shape.
	multiple := importFixture(t, `{
	  "name": "Many",
	  "nodes": [
	    {"id":"a","name":"Any","type":"n8n-nodes-base.webhook","typeVersion":2.1,"position":[0,0],
	     "parameters":{"path":"any","httpMethod":["get","post"],"responseMode":"lastNode"}}
	  ],
	  "connections": {}
	}`)
	methods, _ := nodeByName(multiple.Document, "Any").Parameters["httpMethods"].([]any)
	if len(methods) != 2 || methods[0] != "GET" || methods[1] != "POST" {
		t.Errorf("httpMethods = %#v, want GET and POST", methods)
	}
}

// TestRespondToWebhookDefaultsToTheFirstItem covers n8n's own default.
//
// A missing respondWith means "answer with the first incoming item", and
// reading it as `text` answered every webhook-backed API with an empty body.
func TestRespondToWebhookDefaultsToTheFirstItem(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Answer",
	  "nodes": [
	    {"id":"a","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
	     "parameters":{"path":"answer","responseMode":"responseNode"}},
	    {"id":"b","name":"Set","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
	     "parameters":{"assignments":{"assignments":[{"id":"x","name":"answer","type":"number","value":42}]}}},
	    {"id":"c","name":"Respond","type":"n8n-nodes-base.respondToWebhook","typeVersion":1.1,"position":[440,0],
	     "parameters":{"options":{}}}
	  ],
	  "connections": {
	    "Webhook": {"main": [[{"node":"Set","type":"main","index":0}]]},
	    "Set": {"main": [[{"node":"Respond","type":"main","index":0}]]}
	  }
	}`)
	respond := nodeByName(result.Document, "Respond")
	if respond.Parameters["respondWith"] != "firstIncomingItem" {
		t.Errorf("respondWith = %#v, want firstIncomingItem", respond.Parameters["respondWith"])
	}
}

// TestWebhookResponseOptionsAndHeadersAreCarried covers the options collection
// on both webhook nodes, which used to be dropped without a word.
func TestWebhookResponseOptionsAndHeadersAreCarried(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Configured",
	  "nodes": [
	    {"id":"a","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,"position":[0,0],
	     "parameters":{"path":"hook","responseMode":"responseNode","responseData":"firstEntryJson",
	       "options":{"rawBody":true,"allowedOrigins":"https://app.test"}}},
	    {"id":"c","name":"Respond","type":"n8n-nodes-base.respondToWebhook","typeVersion":1.1,"position":[440,0],
	     "parameters":{"respondWith":"json","options":{"responseCode":201,
	       "responseHeaders":{"entries":[{"name":"X-Trace","value":"={{ $json.trace }}"}]},
	       "responseKey":"result"}}}
	  ],
	  "connections": {"Webhook": {"main": [[{"node":"Respond","type":"main","index":0}]]}}
	}`)
	webhook := nodeByName(result.Document, "Webhook")
	if webhook.Parameters["responseData"] != "firstEntryJson" {
		t.Errorf("responseData = %#v, want it carried", webhook.Parameters["responseData"])
	}
	options, _ := webhook.Parameters["options"].(map[string]any)
	if options["rawBody"] != true || options["allowedOrigins"] != "https://app.test" {
		t.Errorf("webhook options = %#v, want them carried verbatim", options)
	}

	respond := nodeByName(result.Document, "Respond")
	if respond.Parameters["responseKey"] != "result" {
		t.Errorf("responseKey = %#v, want it carried", respond.Parameters["responseKey"])
	}
	headers, _ := respond.Parameters["responseHeaders"].(map[string]any)
	trace, _ := headers["X-Trace"].(map[string]any)
	if trace["value"] != "{{ $json.trace }}" {
		t.Errorf("responseHeaders = %#v, want the expression carried", respond.Parameters["responseHeaders"])
	}
}

// TestHTTPRequestOptionsAndBodyFieldsAreCarried covers the options n8n keeps
// under `options`, which the translator never read.
func TestHTTPRequestOptionsAndBodyFieldsAreCarried(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Options",
	  "nodes": [
	    {"id":"a","name":"Call","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[0,0],
	     "parameters":{"method":"POST","url":"https://api.test/orders","sendBody":true,"contentType":"json",
	       "specifyBody":"keypair","bodyParameters":{"parameters":[
	         {"name":"id","value":"={{ $json.id }}"},{"name":"note","value":"plain"}]},
	       "options":{"timeout":15,"response":{"response":{"neverError":true,"responseFormat":"text",
	         "fullResponse":true,"outputPropertyName":"payload"}},
	         "redirect":{"redirect":{"followRedirects":false,"maxRedirects":3}},
	         "pagination":{"pagination":{}}}}}
	  ],
	  "connections": {}
	}`)
	call := nodeByName(result.Document, "Call")
	fields, _ := call.Parameters["bodyFields"].(map[string]any)
	id, _ := fields["id"].(map[string]any)
	if id["value"] != "{{ $json.id }}" {
		t.Errorf("bodyFields = %#v, want the expression kept rather than marshalled into text",
			call.Parameters["bodyFields"])
	}
	if fields["note"] != "plain" {
		t.Errorf("bodyFields[note] = %#v, want the fixed value kept", fields["note"])
	}
	if call.Parameters["requestTimeoutSeconds"] != float64(15) {
		t.Errorf("timeout = %#v, want it carried", call.Parameters["requestTimeoutSeconds"])
	}
	if call.Parameters["neverError"] != true || call.Parameters["responseFormat"] != "text" ||
		call.Parameters["fullResponse"] != true || call.Parameters["outputPropertyName"] != "payload" {
		t.Errorf("response options = %#v, want them carried", call.Parameters)
	}
	if call.Parameters["followRedirects"] != false || call.Parameters["maxRedirects"] != float64(3) {
		t.Errorf("redirect options = %#v, want them carried", call.Parameters)
	}
	// Pagination has no equivalent, so it is named rather than ignored.
	named := false
	for _, issue := range result.Unsupported {
		if issue.Field == "options.pagination" {
			named = true
		}
	}
	if !named {
		t.Errorf("unsupported = %#v, want the pagination option named", result.Unsupported)
	}

	// The body fields go back out as n8n's key/value collection.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Call" {
			continue
		}
		collection, _ := node.Parameters["bodyParameters"].(map[string]any)
		entries, _ := collection["parameters"].([]any)
		if len(entries) != 2 {
			t.Fatalf("exported bodyParameters = %#v, want both fields", node.Parameters["bodyParameters"])
		}
	}
}

// TestPostgresOperationDefaultsToInsert covers n8n's own default, which differs
// from this server's.
//
// A node whose author never opened the operation dropdown carries no
// `operation` at all. Reading that as executeQuery produced a node with no
// query, discarded the column mapping, and blocked activation with "an execute
// query needs a query".
func TestPostgresOperationDefaultsToInsert(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Insert",
	  "nodes": [
	    {"id":"a","name":"insert data on db","type":"n8n-nodes-base.postgres","typeVersion":2.5,"position":[0,0],
	     "parameters":{"schema":{"value":"public","mode":"name"},"table":{"value":"orders","mode":"name"},
	       "columns":{"mappingMode":"defineBelow","value":{"id":"={{ $json.id }}"}}}}
	  ],
	  "connections": {}
	}`)
	node := nodeByName(result.Document, "insert data on db")
	if node.Parameters["operation"] != "insert" {
		t.Errorf("operation = %#v, want insert", node.Parameters["operation"])
	}
	if _, carried := node.Parameters["columns"]; !carried {
		t.Errorf("columns = %#v, want the configured mapping kept", node.Parameters["columns"])
	}
}

// TestLegacySwitchRulesAreTranslated covers n8n's v1/v2 rule shape.
//
// Those versions keep their rules under `rules.rules`, one row per output.
// Reading only the v3 shape imported a node with no rules and one output, so
// every branch from output 1 onwards was held back and activation failed with
// "switch rules must be a list".
func TestLegacySwitchRulesAreTranslated(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Legacy switch",
	  "nodes": [
	    {"id":"a","name":"CheckCommand","type":"n8n-nodes-base.switch","typeVersion":1,"position":[0,0],
	     "parameters":{"rules":{"rules":[
	       {"value1":"={{ $json.command }}","value2":"start","dataType":"string","operation":"equal","renameOutput":true},
	       {"value1":"={{ $json.command }}","value2":"stop","dataType":"string","operation":"equal"}
	     ]},"fallbackOutput":2}}
	  ],
	  "connections": {}
	}`)
	node := nodeByName(result.Document, "CheckCommand")
	rules, _ := node.Parameters["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("rules = %#v, want both legacy rows translated", node.Parameters["rules"])
	}
	first, _ := rules[0].(map[string]any)
	conditions, _ := first["conditions"].(map[string]any)
	rows, _ := conditions["conditions"].([]any)
	condition, _ := rows[0].(map[string]any)
	operator, _ := condition["operator"].(map[string]any)
	if operator["type"] != "string" || operator["operation"] != "equals" {
		t.Errorf("operator = %#v, want the legacy name translated to the evaluator's vocabulary", operator)
	}
	left, _ := condition["leftValue"].(map[string]any)
	if left["value"] != "{{ $json.command }}" {
		t.Errorf("leftValue = %#v, want the expression carried", condition["leftValue"])
	}
	if node.Parameters["fallbackOutput"] != "2" {
		t.Errorf("fallbackOutput = %#v, want the top-level legacy index carried", node.Parameters["fallbackOutput"])
	}
}

// TestChainRolesAreTranslated covers n8n's prompt-template class names.
//
// n8n stores the LangChain class name and this server stores the role, so
// copying it through imported a chain that could not activate: "message 1 has
// type HumanMessagePromptTemplate, want system, human, or ai".
func TestChainRolesAreTranslated(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Chain",
	  "nodes": [
	    {"id":"a","name":"Basic LLM Chain","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.5,
	     "position":[0,0],"parameters":{"promptType":"define","text":"={{ $json.question }}",
	       "messages":{"messageValues":[
	         {"type":"SystemMessagePromptTemplate","message":"You are terse."},
	         {"type":"HumanMessagePromptTemplate","message":"={{ $json.question }}"}]}}},
	    {"id":"m","name":"Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,
	     "position":[220,0],"parameters":{"model":{"value":"gpt-4.1-mini","mode":"list"}}}
	  ],
	  "connections": {
	    "Model": {"ai_languageModel": [[{"node":"Basic LLM Chain","type":"ai_languageModel","index":0}]]}
	  }
	}`)
	chain := nodeByName(result.Document, "Basic LLM Chain")
	messages, _ := chain.Parameters["messages"].(map[string]any)
	values, _ := messages["messageValues"].([]any)
	if len(values) != 2 {
		t.Fatalf("messages = %#v, want both rows", chain.Parameters["messages"])
	}
	first, _ := values[0].(map[string]any)
	second, _ := values[1].(map[string]any)
	if first["type"] != "system" || second["type"] != "human" {
		t.Errorf("roles = %#v/%#v, want system and human", first["type"], second["type"])
	}

	// And back out as the class names n8n reads.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Basic LLM Chain" {
			continue
		}
		written, _ := node.Parameters["messages"].(map[string]any)
		rows, _ := written["messageValues"].([]any)
		row, _ := rows[1].(map[string]any)
		if row["type"] != "HumanMessagePromptTemplate" {
			t.Errorf("exported role = %#v, want n8n's class name", row["type"])
		}
	}
}

// TestAnOmittedChainPromptBecomesTheUserPrompt covers n8n's pre-1.4 shape: the
// user prompt lives in `prompt`, and reading only `text` lost the question.
func TestAnOmittedChainPromptBecomesTheUserPrompt(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Old chain",
	  "nodes": [
	    {"id":"a","name":"Assess","type":"@n8n/n8n-nodes-langchain.chainLlm","typeVersion":1.3,
	     "position":[0,0],"parameters":{"prompt":"={{ $json.message }}",
	       "messages":{"messageValues":[{"message":"Be brief."}]}}}
	  ],
	  "connections": {}
	}`)
	chain := nodeByName(result.Document, "Assess")
	text, _ := chain.Parameters["text"].(map[string]any)
	if text["value"] != "{{ $json.message }}" {
		t.Errorf("text = %#v, want the legacy prompt carried as the user prompt", chain.Parameters["text"])
	}
	if chain.Parameters["promptType"] != "define" {
		t.Errorf("promptType = %#v, want define", chain.Parameters["promptType"])
	}
}

// TestFromAIOverrideCommentIsStripped covers n8n's auto-generated comment.
//
// n8n writes `{{ /*n8n-auto-generated-fromAI-override*/ $fromAI('x', ``, 'string') }}`
// when a user clicks "let the model define this parameter". The comment is a
// JavaScript comment that n8n ignores, and this server's evaluator refused the
// body — so 27 of the corpus's 52 $fromAI parameters failed on every call.
func TestFromAIOverrideCommentIsStripped(t *testing.T) {
	t.Parallel()

	// n8n's exact auto-generated form, which contains backticks and so cannot
	// live in a raw string literal.
	const fixture = `{
	  "name": "Tool",
	  "nodes": [
	    {"id":"a","name":"Get Weather","type":"@n8n/n8n-nodes-langchain.toolHttpRequest","typeVersion":1.1,
	     "position":[0,0],"parameters":{"url":"https://api.test/weather",
	       "parametersQuery":{"values":[{"name":"city",
	         "value":"={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('city', TICKS, 'string') }}"}]}}}
	  ],
	  "connections": {}
	}`
	result := importFixture(t, strings.ReplaceAll(fixture, "TICKS", "``"))
	tool := nodeByName(result.Document, "Get Weather")
	query, _ := tool.Parameters["queryParameters"].(map[string]any)
	city, _ := query["city"].(map[string]any)
	value, _ := city["value"].(string)
	if strings.Contains(value, "n8n-auto-generated") {
		t.Fatalf("value = %q, want the auto-generated comment stripped", value)
	}
	if !strings.Contains(value, "$fromAI(") {
		t.Errorf("value = %q, want the $fromAI call kept", value)
	}
}

// TestOnErrorContinueRegularOutputBecomesContinueOnFail covers the modern
// spelling of "keep going".
//
// Current n8n writes onError:"continueRegularOutput" instead of the legacy
// boolean, and the runner already honours exactly that — so reading only the
// boolean stopped a run that eight templates were written to tolerate.
func TestOnErrorContinueRegularOutputBecomesContinueOnFail(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Tolerant",
	  "nodes": [
	    {"id":"a","name":"Call","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[0,0],
	     "onError":"continueErrorOutput","parameters":{"method":"GET","url":"https://api.test/x"}},
	    {"id":"b","name":"Branchy","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[220,0],
	     "onError":"continueRegularOutput","parameters":{"method":"GET","url":"https://api.test/y"}}
	  ],
	  "connections": {
	    "Call": {"main": [[{"node":"Branchy","type":"main","index":0}],[{"node":"Branchy","type":"main","index":0}]]}
	  }
	}`)
	branchy := nodeByName(result.Document, "Branchy")
	if branchy.Settings["continueOnFail"] != true {
		t.Errorf("settings = %#v, want continueOnFail carried from onError", branchy.Settings)
	}
	// The error output has no equivalent, and the diagnostic says so instead of
	// claiming a port is missing.
	named := false
	for _, issue := range result.Unsupported {
		if issue.Field == "onError" && strings.Contains(issue.Reason, "error output") {
			named = true
		}
		if strings.Contains(issue.Reason, "declares no main port") {
			t.Fatalf("the error output was reported as a missing port: %+v", issue)
		}
	}
	if !named {
		t.Errorf("unsupported = %#v, want the error output named", result.Unsupported)
	}

	// And the settings go back out as the modern spelling n8n reads.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name == "Branchy" && node.OnError != "continueRegularOutput" {
			t.Errorf("exported onError = %q, want continueRegularOutput", node.OnError)
		}
	}
}

// TestDisabledNodesBlockActivation covers the side effects of a disabled node.
//
// KilasFlow has no disabled-node concept, so an imported disabled trigger
// becomes a live endpoint and a disabled HTTP node starts polling. Refusing to
// activate is the only honest answer until the flag exists.
func TestDisabledNodesBlockActivation(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Disabled",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Poll","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[220,0],
	     "disabled":true,"parameters":{"method":"GET","url":"https://api.test/poll"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Poll","type":"main","index":0}]]}}
	}`)
	blocking := 0
	for _, issue := range result.Unsupported {
		if issue.Field == "disabled" {
			blocking++
			if issue.Severity != n8n.SeverityBlocking {
				t.Errorf("disabled reported %q, want blocking", issue.Severity)
			}
		}
	}
	if blocking != 1 {
		t.Errorf("disabled issues = %d, want exactly one", blocking)
	}
}

// TestExportReportsAVersionChange covers the round trip's typeVersion.
//
// The exporter writes the version whose parameter shape it emits, which is not
// always the version the node arrived at. That is a behaviour change in n8n and
// it is named rather than left for somebody to discover in a diff.
func TestExportReportsAVersionChange(t *testing.T) {
	t.Parallel()

	// An agent at 1.5 has no published list here, so it exports at the pin.
	imported := importFixture(t, langchainFullFixture)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, issue := range exported.Lossy {
		if issue.Field == "typeVersion" && strings.Contains(issue.Reason, "authored at n8n typeVersion") {
			changed = true
		}
	}
	if !changed {
		t.Errorf("lossy = %#v, want the version change named", exported.Lossy)
	}
}

// TestSelfReferencingWorkflowToolIsCarried covers n8n's `$workflow.id`.
//
// A workflow tool or sub-workflow call that names its own workflow is how a
// recursive agent is written. The expression resolves here too, so it is
// carried rather than blocked.
func TestSelfReferencingWorkflowToolIsCarried(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Recursive",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Call itself","type":"n8n-nodes-base.executeWorkflow","typeVersion":1.2,"position":[220,0],
	     "parameters":{"workflowId":{"mode":"id","value":"={{ $workflow.id }}"}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Call itself","type":"main","index":0}]]}}
	}`)
	call := nodeByName(result.Document, "Call itself")
	locator, _ := call.Parameters["workflowId"].(map[string]any)
	value, _ := locator["value"].(map[string]any)
	if value["value"] != "{{ $workflow.id }}" {
		t.Errorf("workflowId = %#v, want the self-reference carried as an expression", call.Parameters["workflowId"])
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Fatalf("a self-reference was blocked: %+v", issue)
		}
	}

	// A foreign ID is still blocked: it names a row in somebody else's database.
	foreign := importFixture(t, `{
	  "name": "Foreign",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Call","type":"n8n-nodes-base.executeWorkflow","typeVersion":1.2,"position":[220,0],
	     "parameters":{"workflowId":{"mode":"id","value":"abc123"}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Call","type":"main","index":0}]]}}
	}`)
	blocked := false
	for _, issue := range foreign.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.Field == "workflowId" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("unsupported = %#v, want a foreign workflow ID blocked", foreign.Unsupported)
	}
}
