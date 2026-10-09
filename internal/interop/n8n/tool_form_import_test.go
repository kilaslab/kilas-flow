package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// agentWithTool is a manual trigger, an agent with an OpenAI model, and one
// tool node given as raw n8n JSON.
func agentWithTool(tool string) string {
	return `{
  "name": "Agent with a tool",
  "nodes": [
    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
    {"id":"b","name":"AI Agent","type":"@n8n/n8n-nodes-langchain.agent","typeVersion":2.2,"position":[220,0],"parameters":{
      "promptType":"define","text":"Do the job."
    }},
    {"id":"c","name":"OpenAI Chat Model","type":"@n8n/n8n-nodes-langchain.lmChatOpenAi","typeVersion":1.2,"position":[160,200],"parameters":{
      "model":{"__rl":true,"mode":"list","value":"gpt-4.1-mini"},"options":{}
    }},
    ` + tool + `
  ],
  "connections": {
    "Manual": {"main": [[{"node":"AI Agent","type":"main","index":0}]]},
    "OpenAI Chat Model": {"ai_languageModel": [[{"node":"AI Agent","type":"ai_languageModel","index":0}]]},
    "Tool": {"ai_tool": [[{"node":"AI Agent","type":"ai_tool","index":0}]]}
  }
}`
}

// compileImported compiles an import as the person importing it would run
// it: with their own credentials attached where the import left the slots
// empty.
func compileImported(t *testing.T, document workflow.Document, catalogue *node.Registry) {
	t.Helper()
	document.ID = "wf_imported"
	for index, imported := range document.Nodes {
		definition, found := catalogue.Lookup(imported.Type, imported.TypeVersion)
		if !found || len(definition.RequiredCredentials) == 0 {
			continue
		}
		attached := map[string]string{}
		for _, credentialType := range definition.RequiredCredentials {
			attached[credentialType] = "cred-local"
		}
		document.Nodes[index].Credentials = attached
	}
	if _, err := workflow.Compile(document, catalogue); err != nil {
		t.Fatalf("Compile() error = %v, want the imported tool to run on the agent", err)
	}
}

// blockingApartFromCredentials lists the blocking issues an author could not
// clear by attaching their own credentials.
func blockingApartFromCredentials(result n8n.ImportResult) []n8n.ImportIssue {
	var blocking []n8n.ImportIssue
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.Field != "credentials" {
			blocking = append(blocking, issue)
		}
	}
	return blocking
}

func expressionTemplate(t *testing.T, value any) string {
	t.Helper()
	if !expression.IsExpression(value) {
		t.Fatalf("%#v is not an expression", value)
	}
	return value.(map[string]any)["value"].(string)
}

const gmailToolNode = `{"id":"d","name":"Tool","type":"n8n-nodes-base.gmailTool","typeVersion":2.1,"position":[400,200],
  "credentials":{"gmailOAuth2":{"id":"7","name":"Gmail account"}},
  "parameters":{
    "sendTo":"={{ $fromAI('To', 'who receives the mail', 'string') }}",
    "subject":"={{ /*n8n-auto-generated-fromAI-override*/ $fromAI('Subject', '', 'string') }}",
    "message":"={{ $fromAI('Message', 'the mail body', 'string') }}",
    "options":{"appendAttribution":false},
    "descriptionType":"manual",
    "toolDescription":"Send an email to the user"
  }}`

func TestAGmailToolImportsAsTheGmailToolForm(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	result := importFixture(t, agentWithTool(gmailToolNode))
	tool := nodeByName(result.Document, "Tool")
	if tool.Type != n8n.GmailToolNodeType || tool.TypeVersion != workflow.MustTypeVersion("2.1") {
		t.Fatalf("gmailTool imported as %s v%s, want %s at n8n's version", tool.Type, tool.TypeVersion, n8n.GmailToolNodeType)
	}
	if tool.Parameters["toolName"] != "Tool" || tool.Parameters["toolDescription"] != "Send an email to the user" {
		t.Errorf("tool naming = %#v / %#v", tool.Parameters["toolName"], tool.Parameters["toolDescription"])
	}
	if _, kept := tool.Parameters["descriptionType"]; kept {
		t.Error("descriptionType crossed as a node parameter")
	}
	if got := expressionTemplate(t, tool.Parameters["to"]); !strings.Contains(got, "$fromAI('To'") {
		t.Errorf("to = %q, want n8n's sendTo", got)
	}
	if _, kept := tool.Parameters["sendTo"]; kept {
		t.Error("sendTo was carried beside to")
	}
	if got := expressionTemplate(t, tool.Parameters["subject"]); strings.Contains(got, "n8n-auto-generated") {
		t.Errorf("subject = %q, want n8n's override comment stripped", got)
	}
	if tool.Parameters["operation"] != "send" || tool.Parameters["resource"] != "message" || tool.Parameters["emailType"] != "html" {
		t.Errorf("resource/operation/emailType = %v/%v/%v, want n8n's defaults message/send/html",
			tool.Parameters["resource"], tool.Parameters["operation"], tool.Parameters["emailType"])
	}
	if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
		t.Fatalf("blocking issues apart from credentials: %#v", blocking)
	}
	compileImported(t, result.Document, catalogue)
}

// n8n generates the description of a tool left on automatic. This server
// has no generated descriptions, so it writes one and says so.
func TestAnAutomaticToolDescriptionIsWrittenAndReported(t *testing.T) {
	t.Parallel()

	result := importFixture(t, agentWithTool(`{"id":"d","name":"Tool","type":"n8n-nodes-base.gmailTool","typeVersion":2.1,"position":[400,200],
	  "parameters":{"operation":"getAll","filters":{"sender":"={{ $fromAI('sender') }}"}}}`))
	tool := nodeByName(result.Document, "Tool")
	description, _ := tool.Parameters["toolDescription"].(string)
	if description == "" {
		t.Fatal("no description was written")
	}
	if issue, found := issueOn(result, "toolDescription"); !found || issue.Severity != n8n.SeverityLossy {
		t.Fatalf("toolDescription issue = %+v (found %v), want a lossy note", issue, found)
	}
	// The nested filter is n8n's `=` string, so it arrives as an expression.
	filters, _ := tool.Parameters["filters"].(map[string]any)
	if got := expressionTemplate(t, filters["sender"]); got != "{{ $fromAI('sender') }}" {
		t.Errorf("filters.sender = %q, want the nested expression converted", got)
	}
}

func TestAPostgresToolImportsWithItsValuesBound(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	for name, tool := range map[string]string{
		"select with a model condition": `{"id":"d","name":"Tool","type":"n8n-nodes-base.postgresTool","typeVersion":2.6,"position":[400,200],
		  "credentials":{"postgres":{"id":"3","name":"Postgres account"}},
		  "parameters":{"descriptionType":"manual","toolDescription":"Find a customer by email",
		    "operation":"select","schema":{"__rl":true,"mode":"list","value":"public"},
		    "table":{"__rl":true,"mode":"list","value":"customers"},
		    "where":{"values":[{"column":"email","value":"={{ $fromAI('email', 'the address', 'string') }}"}]},
		    "options":{}}}`,
		"a query with model replacements": `{"id":"d","name":"Tool","type":"n8n-nodes-base.postgresTool","typeVersion":2.6,"position":[400,200],
		  "credentials":{"postgres":{"id":"3","name":"Postgres account"}},
		  "parameters":{"descriptionType":"manual","toolDescription":"Orders of a customer",
		    "operation":"executeQuery","query":"SELECT * FROM orders WHERE customer = $1 AND status = $2",
		    "options":{"queryReplacement":"={{ $fromAI('customer') }}, {{ $fromAI('status') }}"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := importFixture(t, agentWithTool(tool))
			imported := nodeByName(result.Document, "Tool")
			if imported.Type != n8n.PostgresToolNodeType {
				t.Fatalf("postgresTool imported as %q", imported.Type)
			}
			if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
				t.Fatalf("blocking issues apart from credentials: %#v", blocking)
			}
			switch imported.Parameters["operation"] {
			case "select":
				where, _ := imported.Parameters["where"].([]any)
				row, _ := where[0].(map[string]any)
				if got := expressionTemplate(t, row["value"]); !strings.Contains(got, "$fromAI('email'") {
					t.Errorf("condition value = %q", got)
				}
			case "executeQuery":
				bound, _ := imported.Parameters["queryParameters"].([]any)
				if len(bound) != 2 || expressionTemplate(t, bound[0]) != "{{ $fromAI('customer') }}" ||
					expressionTemplate(t, bound[1]) != "{{ $fromAI('status') }}" {
					t.Errorf("queryParameters = %#v, want one bound expression per segment", bound)
				}
			}
			compileImported(t, result.Document, catalogue)
		})
	}
}

// n8n splices an expression in Query into the SQL text, so a postgresTool
// whose query calls $fromAI has the model writing SQL, which this server's
// tool never runs.
func TestAPostgresToolWhoseModelWritesSQLIsBlocked(t *testing.T) {
	t.Parallel()

	result := importFixture(t, agentWithTool(`{"id":"d","name":"Tool","type":"n8n-nodes-base.postgresTool","typeVersion":2.6,"position":[400,200],
	  "parameters":{"descriptionType":"manual","toolDescription":"Runs SQL",
	    "operation":"executeQuery","query":"={{ $fromAI('sql', 'the SQL to run') }}"}}`))
	issue, found := issueOn(result, "query")
	if !found || issue.Severity != n8n.SeverityBlocking || !strings.Contains(issue.Reason, "Query Parameters") {
		t.Fatalf("query issue = %+v (found %v), want a blocking issue naming the rule", issue, found)
	}
}

const telegramToolNode = `{"id":"d","name":"Tool","type":"n8n-nodes-base.telegramTool","typeVersion":1.2,"position":[400,200],
  "credentials":{"telegramApi":{"id":"5","name":"Telegram account"}},
  "parameters":{"chatId":"={{ $('Manual').item.json.chatId }}",
    "text":"={{ $fromAI('Text', 'what to tell the user', 'string') }}",
    "additionalFields":{"parse_mode":"HTML"},
    "descriptionType":"manual","toolDescription":"Message the user on Telegram"}}`

func TestATelegramToolImportsAsThePacksToolForm(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	result := importFixture(t, agentWithTool(telegramToolNode))
	tool := nodeByName(result.Document, "Tool")
	if tool.Type != n8n.TelegramToolNodeType {
		t.Fatalf("telegramTool imported as %q, want %s", tool.Type, n8n.TelegramToolNodeType)
	}
	if tool.Parameters["parseMode"] != "HTML" || tool.Parameters["toolDescription"] != "Message the user on Telegram" {
		t.Errorf("parameters = %#v, want the Telegram translator's fields and the description", tool.Parameters)
	}
	if blocking := blockingApartFromCredentials(result); len(blocking) != 0 {
		t.Fatalf("blocking issues apart from credentials: %#v", blocking)
	}
	compileImported(t, result.Document, catalogue)
}

// Each form goes back out as n8n's <base>Tool at the version it came in at,
// with its description written down as n8n's manual mode.
func TestToolFormsExportAsN8NsToolOfTheBase(t *testing.T) {
	t.Parallel()

	catalogue := registry(t)
	for name, testCase := range map[string]struct {
		tool, wantType string
		wantVersion    float64
		check          func(t *testing.T, parameters map[string]any)
	}{
		"gmail": {
			tool: gmailToolNode, wantType: "n8n-nodes-base.gmailTool", wantVersion: 2.1,
			check: func(t *testing.T, parameters map[string]any) {
				if got, _ := parameters["sendTo"].(string); !strings.HasPrefix(got, "={{ $fromAI('To'") {
					t.Errorf("sendTo = %#v", parameters["sendTo"])
				}
			},
		},
		"telegram": {
			tool: telegramToolNode, wantType: "n8n-nodes-base.telegramTool", wantVersion: 1.2,
			check: func(t *testing.T, parameters map[string]any) {
				additional, _ := parameters["additionalFields"].(map[string]any)
				if additional["parse_mode"] != "HTML" {
					t.Errorf("additionalFields = %#v", parameters["additionalFields"])
				}
			},
		},
		"postgres": {
			tool: `{"id":"d","name":"Tool","type":"n8n-nodes-base.postgresTool","typeVersion":2.6,"position":[400,200],
			  "parameters":{"descriptionType":"manual","toolDescription":"Find a customer",
			    "operation":"select","table":{"__rl":true,"mode":"list","value":"customers"},
			    "where":{"values":[{"column":"email","value":"={{ $fromAI('email') }}"}]}}}`,
			wantType: "n8n-nodes-base.postgresTool", wantVersion: 2.6,
			check: func(t *testing.T, parameters map[string]any) {
				where, _ := parameters["where"].(map[string]any)
				values, _ := where["values"].([]any)
				row, _ := values[0].(map[string]any)
				if row["value"] != "={{ $fromAI('email') }}" {
					t.Errorf("where = %#v, want the condition back as n8n's expression", parameters["where"])
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			imported := importFixture(t, agentWithTool(testCase.tool))
			exported, err := n8n.Export(imported.Document, catalogue)
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			var tool n8n.Node
			for _, candidate := range exported.Document.Nodes {
				if candidate.Name == "Tool" {
					tool = candidate
				}
			}
			if tool.Type != testCase.wantType || tool.TypeVersion != testCase.wantVersion {
				t.Fatalf("exported as %s v%v, want %s v%v", tool.Type, tool.TypeVersion, testCase.wantType, testCase.wantVersion)
			}
			if tool.Parameters["descriptionType"] != "manual" || tool.Parameters["toolDescription"] == "" {
				t.Errorf("description = %#v / %#v, want manual with the text", tool.Parameters["descriptionType"], tool.Parameters["toolDescription"])
			}
			if _, leaked := tool.Parameters["toolName"]; leaked {
				t.Error("toolName was written as an n8n parameter")
			}
			testCase.check(t, tool.Parameters)
			for _, lossy := range exported.Lossy {
				if lossy.NodeName == "Tool" {
					t.Errorf("export reported %s: %s", lossy.Field, lossy.Reason)
				}
			}
		})
	}
}

func TestARenamedToolFormSaysItsNameDoesNotSurviveExport(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, agentWithTool(gmailToolNode))
	for index, candidate := range imported.Document.Nodes {
		if candidate.Name == "Tool" {
			imported.Document.Nodes[index].Parameters["toolName"] = "send_mail"
		}
	}
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if !hasLossyReason(exported.Lossy, "send_mail") {
		t.Fatalf("lossy = %#v, want the tool name reported", exported.Lossy)
	}
}

// --- The Gmail node itself -----------------------------------------------------

func gmailFixture(version, parameters string) string {
	return `{
	  "name": "Mail",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Gmail","type":"n8n-nodes-base.gmail","typeVersion":` + version + `,"position":[220,0],"parameters":` + parameters + `}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Gmail","type":"main","index":0}]]}}
	}`
}

func TestGmailImportsWithN8NsNamesAndDefaults(t *testing.T) {
	t.Parallel()

	// sendTo is the recipient, and a node that names no operation sends: an
	// export omits n8n's default.
	result := importFixture(t, gmailFixture("2.1", `{"sendTo":"ops@example.com","subject":"Hi","message":"<b>hello</b>"}`))
	gmail := nodeByName(result.Document, "Gmail")
	if gmail.Parameters["to"] != "ops@example.com" || gmail.Parameters["operation"] != "send" || gmail.Parameters["emailType"] != "html" {
		t.Fatalf("parameters = %#v, want to, send and n8n's html", gmail.Parameters)
	}
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Gmail" {
			if candidate.Parameters["sendTo"] != "ops@example.com" || candidate.TypeVersion != 2.1 {
				t.Errorf("exported = v%v %#v, want sendTo back at 2.1", candidate.TypeVersion, candidate.Parameters)
			}
			if _, leaked := candidate.Parameters["to"]; leaked {
				t.Error("to was written beside sendTo")
			}
		}
	}

	// Version 1 kept recipients in a list, and its default resource is drafts.
	result = importFixture(t, gmailFixture("1", `{"resource":"message","toList":["a@example.com","b@example.com"],"subject":"Hi","message":"hello"}`))
	if got := nodeByName(result.Document, "Gmail").Parameters["to"]; got != "a@example.com, b@example.com" {
		t.Errorf("version 1 to = %#v, want the list joined", got)
	}
	for name, parameters := range map[string]string{
		"an operation this node lacks": `{"operation":"reply","messageId":"m1","message":"thanks"}`,
		"a resource this node lacks":   `{"resource":"label","operation":"getAll"}`,
	} {
		result := importFixture(t, gmailFixture("2.1", parameters))
		blocked := false
		for _, issue := range result.Unsupported {
			if issue.Severity == n8n.SeverityBlocking && (issue.Field == "operation" || issue.Field == "resource") {
				blocked = true
			}
		}
		if !blocked {
			t.Errorf("%s imported with no blocking issue: %#v", name, result.Unsupported)
		}
	}
	if result := importFixture(t, gmailFixture("1", `{"toList":["a@example.com"],"subject":"Hi","message":"x"}`)); !hasReason(result.Unsupported, "draft") {
		t.Errorf("version 1 with no resource (n8n's default is draft) imported as: %#v", result.Unsupported)
	}
}

// --- PostgreSQL query replacements ----------------------------------------------

// n8n resolves each {{ }} segment of an expression replacement on its own and
// binds each result as one value; the literal text between them is ignored.
// Splitting the stored text on its commas first bound the expressions' source.
func TestAnExpressionQueryReplacementBindsOneValuePerSegment(t *testing.T) {
	t.Parallel()

	result := importFixture(t, sqlNodeFixture(`{"operation":"executeQuery",
	  "query":"SELECT * FROM customers WHERE tier = $1 AND region = $2",
	  "options":{"queryReplacement":"={{ $json.tier }}, {{ $json.region }}"}}`))
	postgres := nodeByName(result.Document, "Postgres")
	bound, _ := postgres.Parameters["queryParameters"].([]any)
	if len(bound) != 2 || expressionTemplate(t, bound[0]) != "{{ $json.tier }}" || expressionTemplate(t, bound[1]) != "{{ $json.region }}" {
		t.Fatalf("queryParameters = %#v, want one expression per segment", postgres.Parameters["queryParameters"])
	}
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Postgres" {
			options, _ := candidate.Parameters["options"].(map[string]any)
			if options["queryReplacement"] != "={{ $json.tier }}, {{ $json.region }}" {
				t.Errorf("queryReplacement = %#v, want the segments back", options["queryReplacement"])
			}
		}
	}
}
