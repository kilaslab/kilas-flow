package nodes_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
)

// toolFormCatalogue is the shipped catalogue with the Telegram pack and every
// tool form, composed the way the server composes it.
func toolFormCatalogue(t *testing.T, policy safehttp.Policy) (*node.Registry, *routing.Registry, *engine.Registry) {
	t.Helper()
	definitions := node.NewRegistry()
	if err := nodes.RegisterAll(definitions); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(routing.ExecutorID, routing.NewExecutor(policy, routes, definitions)); err != nil {
		t.Fatalf("Register(routing) error = %v", err)
	}
	if err := telegram.Register(definitions, routes, executors, loadoptions.NewResolver(policy, 0)); err != nil {
		t.Fatalf("telegram.Register() error = %v", err)
	}
	if err := nodes.RegisterPackToolForms(definitions); err != nil {
		t.Fatalf("RegisterPackToolForms() error = %v", err)
	}
	return definitions, routes, executors
}

func formExpression(template string) map[string]any {
	return map[string]any{"mode": "expression", "value": template}
}

// One row per tool form. A new form (googleSheetsTool) is one more row.
var toolFormRows = []struct {
	toolType, baseType string
	version            workflow.TypeVersion
	source             node.Source
	// valid is a configuration the form accepts, with a $fromAI value.
	valid map[string]any
}{
	{
		toolType: nodes.GmailToolNodeType, baseType: nodes.GmailNodeType, version: workflow.V(1), source: node.SourceBuiltin,
		valid: map[string]any{
			"toolDescription": "Sends an email.", "resource": "message", "operation": "send",
			"to": formExpression("{{ $fromAI('to', 'recipient') }}"), "subject": "Hello",
			"message": formExpression("{{ $fromAI('body', 'the text') }}"),
		},
	},
	{
		toolType: nodes.PostgresToolNodeType, baseType: nodes.PostgresNodeType, version: workflow.V(2), source: node.SourceBuiltin,
		valid: map[string]any{
			"toolDescription": "Looks a customer up.", "operation": "select",
			"schema": map[string]any{"__rl": true, "mode": "name", "value": "public"},
			"table":  map[string]any{"__rl": true, "mode": "name", "value": "customers"},
			"where": []any{map[string]any{
				"field": "email", "operator": "equals", "value": formExpression("{{ $fromAI('email') }}"),
			}},
		},
	},
	{
		toolType: nodes.TelegramToolNodeType, baseType: telegram.NodeType, version: workflow.V(1), source: node.SourcePack,
		valid: map[string]any{
			"toolDescription": "Messages the team.", "resource": "message", "operation": "sendMessage",
			"chatId": "-100123", "text": formExpression("{{ $fromAI('text') }}"),
		},
	},
}

func TestToolFormsDeriveFromTheirBase(t *testing.T) {
	t.Parallel()

	definitions, _, _ := toolFormCatalogue(t, safehttp.DefaultPolicy())
	for _, row := range toolFormRows {
		t.Run(row.toolType, func(t *testing.T) {
			tool, found := definitions.Get(row.toolType, row.version)
			if !found {
				t.Fatalf("%s v%s is not registered", row.toolType, row.version)
			}
			base, found := definitions.Get(row.baseType, row.version)
			if !found {
				t.Fatalf("base %s v%s is not registered", row.baseType, row.version)
			}
			if tool.Source != row.source || tool.ToolFormOf != row.baseType {
				t.Errorf("source = %s, ToolFormOf = %q; want %s and %q", tool.Source, tool.ToolFormOf, row.source, row.baseType)
			}
			if tool.ExecutorID != nodes.ToolFormExecutorID {
				t.Errorf("ExecutorID = %q, want %q", tool.ExecutorID, nodes.ToolFormExecutorID)
			}
			if len(tool.Inputs) != 0 || len(tool.Outputs) != 1 || tool.Outputs[0].Kind != workflow.ConnectionTool {
				t.Errorf("ports = %#v -> %#v, want no input and one ai_tool output", tool.Inputs, tool.Outputs)
			}
			if tool.Codex == nil || len(tool.Codex.Subcategories["AI"]) == 0 || tool.Codex.Subcategories["AI"][0] != "Tools" {
				t.Errorf("codex = %#v, want AI › Tools", tool.Codex)
			}
			if len(tool.Parameters) != len(base.Parameters)+2 ||
				tool.Parameters[0].Key != "toolName" || tool.Parameters[1].Key != "toolDescription" {
				t.Fatalf("parameters do not lead with toolName and toolDescription before the base's")
			}
			for index, property := range base.Parameters {
				if tool.Parameters[index+2].Key != property.Key {
					t.Errorf("parameter %d = %q, want the base's %q", index+2, tool.Parameters[index+2].Key, property.Key)
				}
			}
			if len(tool.Credentials) != len(base.Credentials) {
				t.Fatalf("credentials = %#v, want the base's %#v", tool.Credentials, base.Credentials)
			}
			for index := range base.Credentials {
				if tool.Credentials[index].Type != base.Credentials[index].Type {
					t.Errorf("credential %d = %q, want %q", index, tool.Credentials[index].Type, base.Credentials[index].Type)
				}
			}

			if err := tool.Validate(workflow.Node{Parameters: row.valid}); err != nil {
				t.Fatalf("Validate(valid) = %v", err)
			}
			refused := map[string]map[string]any{
				"no description":         formWithout(row.valid, "toolDescription"),
				"$fromAI in operation":   formWith(row.valid, "operation", formExpression("{{ $fromAI('operation') }}")),
				"expression operation":   formWith(row.valid, "operation", formExpression("{{ $json.operation }}")),
				"$fromAI in resource":    formWith(row.valid, "resource", "$fromAI('resource')"),
				"inconsistent $fromAI":   formWith(row.valid, "extra", formExpression("{{ $fromAI('to', 'another description') }} {{ $fromAI('to', 'recipient') }}")),
				"malformed $fromAI type": formWith(row.valid, "extra", formExpression("{{ $fromAI('x', 'y', 'paragraph') }}")),
				// Every spelling the filler and the evaluator accept is a call.
				"$fromai in operation":       formWith(row.valid, "operation", "$fromai('operation')"),
				"$fromAi in operation":       formWith(row.valid, "operation", "$fromAi('operation')"),
				"$fromAI ( in operation":     formWith(row.valid, "operation", "$fromAI ('operation')"),
				"$FROMAI in operation":       formWith(row.valid, "operation", "$FROMAI('operation')"),
				"template text in operation": formWith(row.valid, "operation", "{{ $json.operation }}"),
				"template text in resource":  formWith(row.valid, "resource", "{{ 'message' }}"),
				// A tool form takes the model's values through expressions only,
				// so $fromAI in plain text is refused wherever it is written.
				"plain $fromAI in a value": formWith(row.valid, "extra", "$fromAI('extra')"),
				"plain $fromai in a value": formWith(row.valid, "extra", "Re: $fromai ('topic')"),
				"plain $fromAI nested":     formWith(row.valid, "extra", map[string]any{"deep": []any{"$FromAI('x')"}}),
			}
			for name, parameters := range refused {
				if err := tool.Validate(workflow.Node{Parameters: parameters}); err == nil {
					t.Errorf("Validate(%s) accepted it", name)
				}
			}
		})
	}
}

func TestThePostgresToolKeepsTheSQLTheAuthors(t *testing.T) {
	t.Parallel()

	definitions, _, _ := toolFormCatalogue(t, safehttp.DefaultPolicy())
	tool, _ := definitions.Get(nodes.PostgresToolNodeType, workflow.V(2))
	query := map[string]any{"toolDescription": "Runs a report.", "operation": "executeQuery"}
	for name, value := range map[string]any{
		"plain $fromAI":       "$fromAI('sql')",
		"$fromAI expression":  formExpression("{{ $fromAI('sql') }}"),
		"any expression":      formExpression("SELECT * FROM t WHERE id = {{ $json.id }}"),
		"$fromAI inside text": "SELECT * FROM t WHERE $fromAI('where')",
		"$fromai":             "SELECT * FROM users WHERE $fromai('w')",
		"$fromAi":             "SELECT * FROM users WHERE $fromAi('w')",
		"$fromAI (":           "SELECT * FROM users WHERE $fromAI ('w')",
		"$FROMAI":             "SELECT * FROM users WHERE $FROMAI('w')",
		// Plain template text: the PostgreSQL node would send it as SQL, but a
		// tool form's structural text is fixed, so a literal {{ belongs in a
		// bound value instead.
		"template text": "SELECT * FROM users WHERE name = '{{ $json.name }}'",
	} {
		err := tool.Validate(workflow.Node{Parameters: formWith(query, "query", value)})
		if err == nil || !strings.Contains(err.Error(), "query") {
			t.Errorf("Validate(query %s) = %v, want a refusal naming query", name, err)
		}
	}
	for name, parameters := range map[string]map[string]any{
		"model-chosen delete command": formWith(map[string]any{
			"toolDescription": "Deletes.", "operation": "deleteTable",
			"table": map[string]any{"__rl": true, "mode": "name", "value": "t"},
		}, "deleteCommand", formExpression("{{ $fromAI('how') }}")),
		"model-chosen option": formWith(query, "options", map[string]any{"queryBatching": formExpression("{{ $fromAI('mode') }}")}),
	} {
		if err := tool.Validate(workflow.Node{Parameters: formWith(parameters, "query", "SELECT 1")}); err == nil {
			t.Errorf("Validate(%s) accepted it", name)
		}
	}
	// Values are the model's to choose, and bound: Query Parameters and the
	// table name (quoted as an identifier) both take $fromAI.
	if err := tool.Validate(workflow.Node{Parameters: formWith(formWith(query, "query", "SELECT * FROM t WHERE id = $1"),
		"queryParameters", formExpression("{{ [$fromAI('id', 'row id', 'number')] }}"))}); err != nil {
		t.Errorf("Validate(bound $fromAI) = %v", err)
	}
	if err := tool.Validate(workflow.Node{Parameters: map[string]any{
		"toolDescription": "Reads a table.", "operation": "select",
		"table": map[string]any{"__rl": true, "mode": "name", "value": formExpression("{{ $fromAI('table') }}")},
	}}); err != nil {
		t.Errorf("Validate($fromAI table) = %v", err)
	}
}

// A grant naming the Telegram pack also scopes its tool form, so a tenant the
// operator kept away from Telegram cannot reach the bot through an agent.
func TestTheTelegramToolFollowsThePacksScope(t *testing.T) {
	t.Parallel()

	definitions, _, _ := toolFormCatalogue(t, safehttp.DefaultPolicy())
	if err := definitions.ApplyVisibility(map[string][]string{telegram.NodeType: {"acme"}}); err != nil {
		t.Fatalf("ApplyVisibility() error = %v", err)
	}
	if definitions.VisibleTo(nodes.TelegramToolNodeType, "globex") || !definitions.VisibleTo(nodes.TelegramToolNodeType, "acme") {
		t.Fatal("the Telegram tool does not follow the pack's scope")
	}
	err := definitions.ApplyVisibility(map[string][]string{nodes.TelegramToolNodeType: {"acme"}})
	if err == nil || !strings.Contains(err.Error(), telegram.NodeType) {
		t.Fatalf("ApplyVisibility(tool form) = %v, want a refusal naming %s", err, telegram.NodeType)
	}
}

func formWith(parameters map[string]any, key string, value any) map[string]any {
	copied := make(map[string]any, len(parameters)+1)
	for name, existing := range parameters {
		copied[name] = existing
	}
	copied[key] = value
	return copied
}

func formWithout(parameters map[string]any, key string) map[string]any {
	copied := formWith(parameters, key, nil)
	delete(copied, key)
	return copied
}
