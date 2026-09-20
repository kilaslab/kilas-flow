package n8n_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
	"github.com/kilaslabs/kilas-flow/packs/telegram"
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
	       "options":{"timeout":15000,"response":{"response":{"neverError":true,"responseFormat":"text",
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
	// n8n's `timeout` option is milliseconds, so 15000 in the fixture is 15
	// seconds. The fixture used to say 15 and this assertion pinned the number
	// straight through, which is the unit the importer got wrong.
	if call.Parameters["requestTimeoutSeconds"] != float64(15) {
		t.Errorf("timeout = %#v, want it carried in seconds", call.Parameters["requestTimeoutSeconds"])
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

// TestHTTPRequestRedirectPolicyTravelsBothWays covers a flag the importer
// carried only when it was false.
//
// `followRedirects: true` reached the document as "do not follow" with a clean
// report, so a node whose author turned redirects on stopped at the first 3xx.
// Export had the same asymmetry in the other direction.
func TestHTTPRequestRedirectPolicyTravelsBothWays(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Redirects",
	  "nodes": [
	    {"id":"a","name":"Call","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[0,0],
	     "parameters":{"method":"GET","url":"https://api.test/old","options":{
	       "redirect":{"redirect":{"followRedirects":true,"maxRedirects":5}}}}}
	  ],
	  "connections": {}
	}`)
	call := nodeByName(result.Document, "Call")
	if call.Parameters["followRedirects"] != true {
		t.Fatalf("followRedirects = %#v, want true carried", call.Parameters["followRedirects"])
	}
	if call.Parameters["maxRedirects"] != float64(5) {
		t.Errorf("maxRedirects = %#v, want it carried", call.Parameters["maxRedirects"])
	}

	// And back out: the flag survives the round trip rather than being written
	// only when it is false.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	redirect := exportedOptions(t, exported.Document, "Call")["redirect"]
	wrapper, _ := redirect.(map[string]any)
	inner, _ := wrapper["redirect"].(map[string]any)
	if inner["followRedirects"] != true {
		t.Errorf("exported redirect = %#v, want followRedirects true", redirect)
	}
}

// TestHTTPRequestFollowsRedirectsByDefaultFromV4 covers the default the two
// sides disagree about.
//
// n8n omits a parameter holding its default and follows redirects from v4 on,
// while this server's node defaults to not following — so a v4 node with no
// redirect option (nearly every exported one) has to be told to follow.
func TestHTTPRequestFollowsRedirectsByDefaultFromV4(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Defaults",
	  "nodes": [
	    {"id":"a","name":"Modern","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[0,0],
	     "parameters":{"method":"GET","url":"https://api.test/modern"}},
	    {"id":"b","name":"Legacy","type":"n8n-nodes-base.httpRequest","typeVersion":3,"position":[220,0],
	     "parameters":{"method":"GET","url":"https://api.test/legacy"}},
	    {"id":"c","name":"Off","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[440,0],
	     "parameters":{"method":"GET","url":"https://api.test/off","options":{
	       "redirect":{"redirect":{"followRedirects":false}}}}}
	  ],
	  "connections": {}
	}`)
	if modern := nodeByName(result.Document, "Modern"); modern.Parameters["followRedirects"] != true {
		t.Errorf("v4 followRedirects = %#v, want n8n's own default", modern.Parameters["followRedirects"])
	}
	// The legacy spelling never had that default, and inventing it would change
	// what a v3 node does.
	if legacy := nodeByName(result.Document, "Legacy"); legacy.Parameters["followRedirects"] != nil {
		t.Errorf("v3 followRedirects = %#v, want nothing invented", legacy.Parameters["followRedirects"])
	}
	if off := nodeByName(result.Document, "Off"); off.Parameters["followRedirects"] != false {
		t.Errorf("explicit followRedirects = %#v, want the node's own choice", off.Parameters["followRedirects"])
	}
}

// TestHTTPRequestTimeoutUnitIsMillisecondsOnImport covers the unit n8n uses for
// the `timeout` spelling.
//
// Both spellings were copied into a seconds parameter unchanged, so a 5000 ms
// timeout became 5000 seconds — clamped to the deployment ceiling — and the
// node waited far longer than its author asked for.
func TestHTTPRequestTimeoutUnitIsMillisecondsOnImport(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Timeouts",
	  "nodes": [
	    {"id":"a","name":"Millis","type":"n8n-nodes-base.httpRequest","typeVersion":3,"position":[0,0],
	     "parameters":{"method":"GET","url":"https://api.test/ms","options":{"timeout":5000}}},
	    {"id":"b","name":"Seconds","type":"n8n-nodes-base.httpRequest","typeVersion":4.2,"position":[220,0],
	     "parameters":{"method":"GET","url":"https://api.test/s","options":{"requestTimeout":30}}}
	  ],
	  "connections": {}
	}`)
	if millis := nodeByName(result.Document, "Millis"); millis.Parameters["requestTimeoutSeconds"] != float64(5) {
		t.Errorf("milliseconds timeout = %#v, want 5 seconds", millis.Parameters["requestTimeoutSeconds"])
	}
	// `requestTimeout` is already seconds and must not be divided.
	if seconds := nodeByName(result.Document, "Seconds"); seconds.Parameters["requestTimeoutSeconds"] != float64(30) {
		t.Errorf("seconds timeout = %#v, want 30 seconds", seconds.Parameters["requestTimeoutSeconds"])
	}

	// And out again in the unit of the spelling the export writes.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := exportedOptions(t, exported.Document, "Millis")["timeout"]; got != float64(5000) {
		t.Errorf("exported timeout = %#v, want milliseconds", got)
	}
}

// exportedOptions reads the `options` collection off one exported node.
func exportedOptions(t *testing.T, document n8n.Document, name string) map[string]any {
	t.Helper()
	for _, node := range document.Nodes {
		if node.Name != name {
			continue
		}
		options, _ := node.Parameters["options"].(map[string]any)
		return options
	}
	t.Fatalf("node %q was not exported", name)
	return nil
}

// TestDateTimeV1FormatTokensAreTranslatedInOnePass covers the format a v1 Date
// & Time node carries.
//
// The tokens were applied as a sequence of ReplaceAll passes, and one pass's
// output stayed matchable by the next: `DD` became `dd` and then `cc` (the ISO
// weekday), and `ZZ` became `ZZZZ`. `YYYY-MM-DD HH:mm` therefore rendered the
// weekday where the day of the month belongs, which is a wrong date rather than
// an error. The offset rows were off by one token as well: moment's `Z` is
// `+07:00` and its `ZZ` is `+0700`, while Luxon's `ZZ` is `+07:00` and `ZZZ` is
// `+0700`.
func TestDateTimeV1FormatTokensAreTranslatedInOnePass(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Formats",
	  "nodes": [
	    {"id":"a","name":"Stamp","type":"n8n-nodes-base.dateTime","typeVersion":1,"position":[0,0],
	     "parameters":{"action":"format","value":"={{ $json.at }}","toFormat":"YYYY-MM-DD HH:mm"}},
	    {"id":"b","name":"Offset","type":"n8n-nodes-base.dateTime","typeVersion":1,"position":[220,0],
	     "parameters":{"action":"format","value":"={{ $json.at }}","toFormat":"ZZ"}},
	    {"id":"c","name":"Epoch","type":"n8n-nodes-base.dateTime","typeVersion":1,"position":[440,0],
	     "parameters":{"action":"format","value":"={{ $json.at }}","toFormat":"X"}}
	  ],
	  "connections": {}
	}`)
	for name, want := range map[string]string{
		"Stamp":  "yyyy-MM-dd HH:mm", // not "yyyy-cc-cc": the day of the month.
		"Offset": "ZZZ",              // +0700, which is Luxon's ZZZ and not ZZZZ.
		"Epoch":  "X",                // Unix seconds, which Luxon spells the same way.
	} {
		node := nodeByName(result.Document, name)
		if got := node.Parameters["format"]; got != want {
			t.Errorf("%s format = %#v, want %q", name, got, want)
		}
	}
}

// TestMergeOutputTypeReachesJoinMode covers the options bag the Merge mapping
// parked its keys in.
//
// includeUnpaired/keepNonMatches/enrichInput2/mergeMode were copied into
// `converted["options"]`, a bag no Merge path reads, and marked consumed so
// nothing was reported either — while the executor implements exactly those
// semantics under `joinMode`. Unpaired items therefore vanished from the output
// of an import that reported nothing.
func TestMergeOutputTypeReachesJoinMode(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"Include unpaired":  "keepEverything",
		"Keep non-matches":  "keepNonMatches",
		"Enrich input two":  "enrichInput2",
		"Modern merge mode": "keepEverything",
	} {
		result := importFixture(t, `{
		  "name": "Merge",
		  "nodes": [
		    {"id":"a","name":"`+name+`","type":"n8n-nodes-base.merge","typeVersion":3,"position":[0,0],
		     "parameters":{"mode":"combineByFields","fieldsToMatchString":"id","options":`+mergeOptionsFor(name)+`}}
		  ],
		  "connections": {}
		}`)
		node := nodeByName(result.Document, name)
		if got := node.Parameters["joinMode"]; got != want {
			t.Errorf("%s joinMode = %#v, want %q", name, got, want)
		}
		if _, parked := node.Parameters["options"]; parked {
			t.Errorf("%s kept an options bag nothing reads: %#v", name, node.Parameters["options"])
		}
	}
}

// mergeOptionsFor is the n8n options object one case above arrives with.
func mergeOptionsFor(name string) string {
	switch name {
	case "Include unpaired":
		return `{"includeUnpaired":true}`
	case "Keep non-matches":
		return `{"keepNonMatches":true}`
	case "Enrich input two":
		return `{"enrichInput2":true}`
	default:
		return `{"mergeMode":"keepEverything"}`
	}
}

// TestMergeOptionWithNoEquivalentIsNamed: what the executor does not implement
// is reported rather than mapped onto the nearest guess.
func TestMergeOptionWithNoEquivalentIsNamed(t *testing.T) {
	t.Parallel()

	result := importFixture(t, `{
	  "name": "Merge",
	  "nodes": [
	    {"id":"a","name":"Join","type":"n8n-nodes-base.merge","typeVersion":3.1,"position":[0,0],
	     "parameters":{"mode":"combineByFields","fieldsToMatchString":"id","options":{
	       "clashHandling":{"values":{"resolveClash":"preferInput2","mergeFields":""}},
	       "multipleMatches":"first"}}}
	  ],
	  "connections": {}
	}`)
	node := nodeByName(result.Document, "Join")
	if _, set := node.Parameters["joinMode"]; set {
		t.Errorf("joinMode = %#v, want nothing invented", node.Parameters["joinMode"])
	}
	named := map[string]bool{}
	for _, issue := range result.Unsupported {
		named[issue.Field] = true
	}
	for _, field := range []string{"options.clashHandling", "options.multipleMatches"} {
		if !named[field] {
			t.Errorf("unsupported = %#v, want %s named", result.Unsupported, field)
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
	// A numeric fallback names one of the rules' own outputs, which this
	// server's Switch cannot route to. Writing the index produced a value the
	// executor never reads — unmatched items were dropped while the import
	// report stayed clean — so the index is named instead.
	if node.Parameters["fallbackOutput"] != nil {
		t.Errorf("fallbackOutput = %#v, want no inert value written", node.Parameters["fallbackOutput"])
	}
	named := false
	for _, issue := range result.Unsupported {
		if issue.Field == "fallbackOutput" && strings.Contains(issue.Reason, "output 2") {
			named = true
		}
	}
	if !named {
		t.Errorf("unsupported = %#v, want the numeric fallback named with its index", result.Unsupported)
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
// n8n writes `{{ /*n8n-auto-generated-fromAI-override*/ $fromAI('x', “, 'string') }}`
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
	// The error branch is a real edge now: the compiler declares the node's
	// own `error` output for continueErrorOutput, and the import wires the
	// branch to it instead of holding the edge back.
	ports := map[string]string{}
	for _, connection := range result.Document.Connections {
		if connection.Target.NodeID == "b" {
			ports[connection.Source.Port] = connection.Target.Port
		}
	}
	if _, ok := ports["error"]; !ok {
		t.Errorf("connections = %#v, want the error branch wired to the error port", ports)
	}
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "declares no main port") ||
			strings.Contains(issue.Reason, "error branch") {
			t.Fatalf("the error output was not resolved: %+v", issue)
		}
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

// TestDisabledNodesStayDisabled covers the side effects of a disabled node.
//
// The flag is carried onto the node, and the runtime never invokes a disabled
// node — a disabled trigger is never started — so an imported automation does
// not begin calling endpoints the author switched off.
func TestDisabledNodesStayDisabled(t *testing.T) {
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
	if !nodeByName(result.Document, "Poll").Disabled {
		t.Error("the disabled flag did not reach the node")
	}
	for _, issue := range result.Unsupported {
		if issue.Field == "disabled" {
			t.Errorf("a carried flag was reported as a loss: %+v", issue)
		}
	}

	// And it goes back out switched off.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name == "Poll" && !node.Disabled {
			t.Error("the exported node came back enabled")
		}
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

// TestTelegramOperationsMatchThePack keeps the mirrored operation list honest.
//
// The adapter must not import the node pack, so the list is duplicated — and a
// duplication nobody checks is a mapping that silently stops covering an
// operation the pack gained.
func TestTelegramOperationsMatchThePack(t *testing.T) {
	t.Parallel()

	pack, err := telegram.Pack()
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range pack.Resources {
		for _, operation := range resource.Operations {
			if !n8n.TelegramOperationKnown(resource.Name, operation.Name) {
				t.Errorf("pack declares %s/%s, which the importer does not know about",
					resource.Name, operation.Name)
			}
		}
	}
}

// TestTelegramAdditionalFieldsReachThePack covers the options n8n keeps in a
// collection and the pack reads at the top level.
//
// Copying the node verbatim dropped every one of them: HTML formatting
// disappeared and messages arrived with raw tags, and sendAndWait activated and
// then failed because the pack has no such request.
func TestTelegramAdditionalFieldsReachThePack(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Telegram",
	  "nodes": [
	    {"id":"a","name":"Send","type":"n8n-nodes-base.telegram","typeVersion":1.2,"position":[0,0],
	     "parameters":{"resource":"message","operation":"sendMessage","chatId":"123","text":"hi",
	       "additionalFields":{"parse_mode":"HTML","disable_notification":true,"reply_to_message_id":"7"},
	       "replyMarkup":"inlineKeyboard","inlineKeyboard":{"rows":[{"row":{"buttons":[
	         {"text":"Yes","additionalFields":{"callback_data":"yes"}}]}}]}}}
	  ],
	  "connections": {}
	}`

	result := importFixture(t, fixture)
	send := nodeByName(result.Document, "Send")
	if send.Parameters["parseMode"] != "HTML" || send.Parameters["disableNotification"] != true ||
		send.Parameters["replyToMessageId"] != "7" {
		t.Errorf("parameters = %#v, want the additional fields mapped onto the pack's names", send.Parameters)
	}
	encoded, err := json.Marshal(send.Parameters["replyMarkup"])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"inline_keyboard":[[{"callback_data":"yes","text":"Yes"}]]}` {
		t.Fatalf("replyMarkup = %s, want the inline keyboard built as Bot API JSON", encoded)
	}

	// An operation the pack does not implement blocks rather than activating
	// and failing on its first run.
	approval := importFixture(t, `{
	  "name": "Approval",
	  "nodes": [
	    {"id":"a","name":"Ask","type":"n8n-nodes-base.telegram","typeVersion":1.2,"position":[0,0],
	     "parameters":{"resource":"message","operation":"sendAndWait","chatId":"123","text":"ok?"}}
	  ],
	  "connections": {}
	}`)
	blocked := false
	for _, issue := range approval.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.Field == "operation" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("unsupported = %#v, want sendAndWait blocked", approval.Unsupported)
	}
}

// TestFormTriggerMapsOntoTheNativeNode covers the form trigger's fields.
//
// n8n keeps each choice of a dropdown in its own `{option}` row and this
// server reads a list of strings, and the two nodes otherwise ask for the same
// thing in the same shape.
func TestFormTriggerMapsOntoTheNativeNode(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Form",
	  "nodes": [
	    {"id":"a","name":"On form submission","type":"n8n-nodes-base.formTrigger","typeVersion":2.2,
	     "position":[0,0],"parameters":{"path":"signup","formTitle":"Sign up","formDescription":"Tell us",
	       "responseMode":"lastNode","formFields":{"values":[
	         {"fieldLabel":"Name","fieldType":"text","requiredField":true,"placeholder":"Ada"},
	         {"fieldLabel":"Plan","fieldType":"dropdown","fieldOptions":{"values":[
	           {"option":"free"},{"option":"paid"}]}}]}}}
	  ],
	  "connections": {}
	}`

	result := importFixture(t, fixture)
	trigger := nodeByName(result.Document, "On form submission")
	if trigger.Type != "kilasflow.formTrigger" {
		t.Fatalf("type = %q, want the native form trigger", trigger.Type)
	}
	if trigger.Parameters["path"] != "signup" || trigger.Parameters["formTitle"] != "Sign up" ||
		trigger.Parameters["responseMode"] != "lastNode" {
		t.Errorf("parameters = %#v, want the path, title and response mode carried", trigger.Parameters)
	}
	fields, _ := trigger.Parameters["formFields"].(map[string]any)
	rows, _ := fields["values"].([]any)
	if len(rows) != 2 {
		t.Fatalf("formFields = %#v, want both fields", trigger.Parameters["formFields"])
	}
	second, _ := rows[1].(map[string]any)
	options, _ := second["fieldOptions"].(map[string]any)
	choices, _ := options["values"].([]any)
	if len(choices) != 2 || choices[0] != "free" || choices[1] != "paid" {
		t.Errorf("fieldOptions = %#v, want the choices reduced to a list of strings", second["fieldOptions"])
	}

	// And back out in n8n's shape.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "On form submission" {
			continue
		}
		written, _ := node.Parameters["formFields"].(map[string]any)
		back, _ := written["values"].([]any)
		if len(back) != 2 {
			t.Fatalf("exported formFields = %#v, want both fields", node.Parameters["formFields"])
		}
		row, _ := back[1].(map[string]any)
		opts, _ := row["fieldOptions"].(map[string]any)
		entries, _ := opts["values"].([]any)
		first, _ := entries[0].(map[string]any)
		if first["option"] != "free" {
			t.Errorf("exported option = %#v, want n8n's {option} row shape", entries[0])
		}
	}

	// Test mode has no equivalent and is named rather than ignored.
	testMode := importFixture(t, `{
	  "name": "Test form",
	  "nodes": [
	    {"id":"a","name":"On form submission","type":"n8n-nodes-base.formTrigger","typeVersion":2.2,
	     "position":[0,0],"parameters":{"path":"t","formMode":"test","formFields":{"values":[]}}}
	  ],
	  "connections": {}
	}`)
	named := false
	for _, issue := range testMode.Unsupported {
		if issue.Field == "formMode" && issue.Severity == n8n.SeverityLossy {
			named = true
		}
	}
	if !named {
		t.Errorf("unsupported = %#v, want the test mode named", testMode.Unsupported)
	}
}

// TestErrorWorkflowNodesMapOntoTheNativePair covers n8n's error workflow.
//
// An error workflow is what n8n runs when another workflow fails, and both
// halves of it imported as unsupported placeholders — so a workflow that was
// *about* handling failures could not run at all.
func TestErrorWorkflowNodesMapOntoTheNativePair(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Error handler",
	  "nodes": [
	    {"id":"a","name":"Error Trigger","type":"n8n-nodes-base.errorTrigger","typeVersion":1,"position":[0,0],
	     "parameters":{"workflowId":"wf_self"}},
	    {"id":"b","name":"Tell me","type":"n8n-nodes-base.stopAndError","typeVersion":1,"position":[220,0],
	     "parameters":{"errorObject":"object","errorMessage":"={{ $json.execution.error.message }}",
	       "errorDescription":"the run failed"}},
	    {"id":"c","name":"Bad JSON","type":"n8n-nodes-base.stopAndError","typeVersion":1,"position":[440,0],
	     "parameters":{"errorObject":"json","errorObjectJson":"{not json"}}
	  ],
	  "connections": {"Error Trigger": {"main": [[{"node":"Tell me","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	if got := nodeByName(result.Document, "Error Trigger").Type; got != "kilasflow.errorTrigger" {
		t.Fatalf("error trigger type = %q, want the native node", got)
	}
	stop := nodeByName(result.Document, "Tell me")
	if stop.Type != "kilasflow.stopAndError" {
		t.Fatalf("stop type = %q, want the native node", stop.Type)
	}
	message, _ := stop.Parameters["errorMessage"].(map[string]any)
	if message["value"] != "{{ $json.execution.error.message }}" {
		t.Errorf("errorMessage = %#v, want the expression carried", stop.Parameters["errorMessage"])
	}
	// The object mode becomes JSON text, description included: dropping it
	// would throw away the half of the error the author wrote for a human.
	object, _ := stop.Parameters["errorObject"].(string)
	if !strings.Contains(object, "the run failed") || !strings.Contains(object, "errorMessage") {
		t.Errorf("errorObject = %q, want the message and description serialised", object)
	}

	// Unreadable JSON blocks rather than throwing an empty error.
	blocked := false
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.Field == "errorObject" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("unsupported = %#v, want unreadable JSON blocked", result.Unsupported)
	}

	// The pair goes back out as n8n's own nodes.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, node := range exported.Document.Nodes {
		names[node.Type] = true
	}
	if !names["n8n-nodes-base.errorTrigger"] || !names["n8n-nodes-base.stopAndError"] {
		t.Errorf("exported types = %#v, want n8n's error pair", names)
	}
}
