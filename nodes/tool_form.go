package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Tool forms: an ordinary node offered to an AI Agent as a tool.
//
// One table entry gives a registered node an agent-tool form. The form keeps
// the base node's parameters and credentials, adds the tool's own name and
// description and an ai_tool output, and is filed under AI › Tools. When the
// agent calls it, the base node's own registered executor runs, so the tool
// inherits the node's egress policy, credential checks and SQL binding rather
// than reimplementing any of them. There is no per-node tool code.
//
// The model's values reach that executor only as data: each `$fromAI('key')`
// in an expression reads the model's argument through the request's
// FromAIArguments, and a plain string holding a `$fromAI` call is filled in
// place and may not gain `{{` from the model. What kind of call the tool makes
// (its resource, its operation, and the keys a form names as structural) is
// the author's choice and never the model's.

const (
	// ToolFormExecutorID binds every tool form. It only emits a descriptor;
	// the agent runs the base node's executor when the model calls the tool.
	ToolFormExecutorID = "core.toolForm"
	toolKindForm       = "toolForm"

	GmailToolNodeType    = "kilasflow.gmailTool"
	PostgresToolNodeType = "kilasflow.postgresTool"
	// TelegramToolNodeType is the tool form of the Telegram pack. It is
	// registered from the pack source, because the kilasflow. prefix is
	// reserved for built-ins.
	TelegramToolNodeType = "pack.telegramTool"

	// telegramPackNodeType mirrors packs/telegram.NodeType. The pack cannot
	// be imported here: its own tests import this package.
	telegramPackNodeType = "pack.telegram"
)

// toolForm is one entry of the table.
type toolForm struct {
	toolType    string
	baseType    string
	baseVersion workflow.TypeVersion
	displayName string
	description string
	// pack marks a base registered by a node pack, whose form is registered
	// by RegisterPackToolForms once the packs are loaded.
	pack bool
	// structuralKeys are parameters besides resource and operation that
	// decide what kind of call the node makes. Each must be fixed by the
	// author: no $fromAI and no expression.
	structuralKeys []string
	// validate is this form's own rule, run before the generic ones so its
	// message names the rule.
	validate workflow.ConfigValidator
}

// toolForms is the whole list. Adding a form is one entry here plus one
// importer line (internal/interop/n8n toolVariants).
var toolForms = []toolForm{
	{
		toolType: GmailToolNodeType, baseType: GmailNodeType, baseVersion: workflow.V(1),
		displayName: "Gmail Tool",
		description: "Lets an AI Agent get, list and send Gmail messages through the Gmail node.",
	},
	{
		toolType: PostgresToolNodeType, baseType: PostgresNodeType, baseVersion: PostgresV2Version,
		displayName: "PostgreSQL Tool",
		description: "Lets an AI Agent select, insert, update and delete rows through the PostgreSQL node. " +
			"The SQL is the author's; the model supplies values only.",
		structuralKeys: []string{"query", "deleteCommand", "options"},
		validate:       validatePostgresToolQuery,
	},
	{
		toolType: TelegramToolNodeType, baseType: telegramPackNodeType, baseVersion: workflow.V(1),
		displayName: "Telegram Tool",
		description: "Lets an AI Agent send Telegram messages and run the Telegram node's other operations.",
		pack:        true,
	},
}

func toolFormFor(toolType string) (toolForm, bool) {
	for _, form := range toolForms {
		if form.toolType == toolType {
			return form, true
		}
	}
	return toolForm{}, false
}

// toolFormDefinition derives the tool node from its base's registered
// definition.
func toolFormDefinition(base node.Definition, form toolForm) node.Definition {
	definition := toolVariantOf(base, form.toolType, form.displayName, form.description, ToolFormExecutorID)
	definition.Version = base.Version
	definition.ToolFormOf = base.Type
	definition.VisibleTo = append([]string(nil), base.VisibleTo...)
	// A tool has one fixed ai_tool output and no item input, whatever the
	// base computes from its parameters; it is never a loop entry, never a
	// trigger and never a webhook.
	definition.PortsFor = nil
	definition.RequiredInputsFor = nil
	definition.LoopEntry = false
	definition.WholeBatch = false
	definition.ErrorAsMessage = false
	definition.Webhook = nil
	definition.LifecycleID = ""
	definition.Validate = validateToolForm(form, base.Validate)
	return definition
}

// registerToolForms registers the forms whose base is built in (pack false)
// or comes from a pack (pack true). A built-in base that is missing is a
// composition error; a pack that is not installed simply has no tool form.
func registerToolForms(registry *node.Registry, pack bool) error {
	for _, form := range toolForms {
		if form.pack != pack {
			continue
		}
		base, found := registry.Get(form.baseType, form.baseVersion)
		if !found {
			if pack {
				continue
			}
			return fmt.Errorf("tool form %q: its base %q version %s is not registered", form.toolType, form.baseType, form.baseVersion)
		}
		definition := toolFormDefinition(base, form)
		var err error
		if base.Source == node.SourceBuiltin {
			err = registry.Register(definition)
		} else {
			err = registry.RegisterFrom(base.Source, definition)
		}
		if err != nil {
			return fmt.Errorf("tool form %q: %w", form.toolType, err)
		}
	}
	return nil
}

// RegisterPackToolForms registers the tool forms of nodes that packs supply.
// Call it after every pack is loaded and before packs.visible_to is applied,
// so a form is registered with its base's scope and follows any override.
func RegisterPackToolForms(registry *node.Registry) error {
	return registerToolForms(registry, true)
}

// validateToolForm is the derived node's configuration check: the tool's own
// naming and $fromAI rules, the author-only keys, the form's own rule, then
// everything the base node checks.
func validateToolForm(form toolForm, base workflow.ConfigValidator) workflow.ConfigValidator {
	return func(n workflow.Node) error {
		if err := validateToolNameAndDescription(n); err != nil {
			return err
		}
		if err := validateToolFromAI(n); err != nil {
			return err
		}
		if err := ai.CheckFromAIConsistent(n.Parameters); err != nil {
			return err
		}
		if err := checkToolFormParameters(form, n.Parameters); err != nil {
			return err
		}
		if base != nil {
			return base(n)
		}
		return nil
	}
}

// checkToolFormParameters refuses a form whose kind of call the model could
// choose. It runs when the node is validated and again when the agent builds
// the tool, because a descriptor is data and the agent does not take its word.
func checkToolFormParameters(form toolForm, parameters map[string]any) error {
	if form.validate != nil {
		if err := form.validate(workflow.Node{Parameters: parameters}); err != nil {
			return err
		}
	}
	for _, key := range append([]string{"resource", "operation"}, form.structuralKeys...) {
		if value, present := parameters[key]; present && authorChoiceIsOpen(value) {
			return fmt.Errorf("%s decides what kind of call this tool makes, so the workflow author sets it: "+
				"it cannot call $fromAI or be an expression", key)
		}
	}
	return nil
}

// authorChoiceIsOpen reports whether a value, at any depth, is or holds an
// expression or a $fromAI call.
func authorChoiceIsOpen(value any) bool {
	if holdsExpressionMarker(value) {
		return true
	}
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "$fromAI(")
	case map[string]any:
		for _, nested := range typed {
			if authorChoiceIsOpen(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if authorChoiceIsOpen(nested) {
				return true
			}
		}
	}
	return false
}

// validatePostgresToolQuery keeps the SQL the author's. An expression in
// `query` is refused by the base node already; a $fromAI call in it, even in
// a plain string, would have the model write the statement.
func validatePostgresToolQuery(n workflow.Node) error {
	query := n.Parameters["query"]
	if expression.IsExpression(query) || authorChoiceIsOpen(query) {
		return fmt.Errorf("query is SQL the workflow author writes: the model may not write it, through $fromAI or an " +
			"expression. Put $1, $2 placeholders in the query and the values in Query Parameters, or use the " +
			"select, insert, update or delete operations, whose values are bound")
	}
	return nil
}

// executeToolForm emits the tool descriptor. The base node runs only when the
// agent calls the tool.
func executeToolForm(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(ir.Parameters))
	for key, value := range ir.Parameters {
		parameters[key] = value
	}
	credentials := make(map[string]any, len(ir.Credentials))
	for typeID, id := range ir.Credentials {
		credentials[typeID] = id
	}
	return workflow.NodeOutput{{{JSON: map[string]any{descriptorKey: map[string]any{
		"kind":        toolKindForm,
		"toolType":    ir.Type,
		"name":        toolNameFor(ir),
		"description": textValue(ir.Parameters["toolDescription"], ""),
		"nodeName":    ir.Name,
		"parameters":  parameters,
		"credentials": credentials,
	}}}}}, nil
}

// executorLookup is the slice of engine.Registry the agent needs: the base
// node's executor, found at call time.
type executorLookup interface {
	Lookup(id string) (engine.Executor, bool)
}

// toolFormRuntime is what the agent needs to run a tool form: the catalogue
// that names each base node's executor and credential order, and the
// executors themselves.
type toolFormRuntime struct {
	catalog   workflow.Catalog
	executors executorLookup
}

// WithAgentToolForms lets the agent run tool forms. Both are looked up when a
// tool is built, so an executor registered after the agent (a pack's) is
// found.
func WithAgentToolForms(catalog workflow.Catalog, executors executorLookup) AgentOption {
	return func(executor *AgentExecutor) {
		executor.forms = toolFormRuntime{catalog: catalog, executors: executors}
	}
}

// toolFormFrom wraps a tool form descriptor as a callable tool.
//
// The base node is resolved here, on the server, from the tool type: the
// descriptor names which form it is and never which executor runs it. The
// lookup goes through the running tenant's catalogue, so a base hidden from
// that tenant is not reachable through its tool.
func (executor *AgentExecutor) toolFormFrom(ir workflow.IRNode, descriptor map[string]any, request engine.Request) (ai.Tool, error) {
	name := textValue(descriptor["name"], "")
	if name == "" {
		return nil, fmt.Errorf("node %q: a connected tool has no name", ir.Name)
	}
	nodeName := textValue(descriptor["nodeName"], name)
	toolType := textValue(descriptor["toolType"], "")
	form, known := toolFormFor(toolType)
	if !known {
		return nil, fmt.Errorf("node %q: connected tool %q names %q, which is not a tool form this server has", ir.Name, nodeName, toolType)
	}
	if executor.forms.catalog == nil || executor.forms.executors == nil {
		return nil, fmt.Errorf("node %q: tool %q runs the %s node, and this runtime was given no node catalogue to find it in", ir.Name, nodeName, form.baseType)
	}
	definition, found := workflow.CatalogFor(executor.forms.catalog, request.Execution.TenantID).Lookup(form.baseType, form.baseVersion)
	if !found {
		return nil, fmt.Errorf("node %q: tool %q runs the %s node, which is not available here", ir.Name, nodeName, form.baseType)
	}
	if definition.ExecutorID == "" || definition.ExecutorID == ToolFormExecutorID {
		return nil, fmt.Errorf("node %q: tool %q: the %s node has no executor to run", ir.Name, nodeName, form.baseType)
	}
	base, found := executor.forms.executors.Lookup(definition.ExecutorID)
	if !found {
		return nil, fmt.Errorf("node %q: tool %q: the %s node's executor is not installed", ir.Name, nodeName, form.baseType)
	}

	raw, _ := descriptor["parameters"].(map[string]any)
	parameters := make(map[string]any, len(raw))
	for key, value := range raw {
		if key == "toolName" || key == "toolDescription" {
			continue
		}
		parameters[key] = value
	}
	if err := checkToolFormParameters(form, parameters); err != nil {
		return nil, fmt.Errorf("node %q: tool %q: %w", ir.Name, nodeName, err)
	}
	credentials := map[string]string{}
	if attached, ok := descriptor["credentials"].(map[string]any); ok {
		for typeID, id := range attached {
			if text, ok := id.(string); ok {
				credentials[typeID] = text
			}
		}
	}
	return &formTool{
		name:        name,
		description: textValue(descriptor["description"], ""),
		executor:    base,
		node: workflow.IRNode{
			ID: ir.ID + ":" + name, Name: nodeName,
			// The base's own type and version: the routing interpreter finds a
			// pack's routes and properties by them.
			Type: definition.Type, TypeVersion: definition.Version,
			Parameters: parameters, Credentials: credentials,
			Definition: definition,
		},
		request: request,
	}, nil
}

// formTool runs a base node's executor for one tool call.
type formTool struct {
	name        string
	description string
	executor    engine.Executor
	node        workflow.IRNode
	request     engine.Request
}

// Definition offers the model one typed property per $fromAI call. A form
// with none takes no arguments: the model's values have no other way in.
func (tool *formTool) Definition() ai.ToolDefinition {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if calls, err := ai.ExtractFromAI(tool.node.Parameters); err == nil && len(calls) > 0 {
		schema = ai.FromAISchema(calls)
	}
	return ai.ToolDefinition{Name: tool.name, Description: tool.description, Parameters: schema}
}

// Invoke runs the base node once, on an empty item, with the model's
// arguments in the request for its expressions to read. The result is what
// the model reads, capped at httpToolMaxBytes like every tool's.
func (tool *formTool) Invoke(ctx context.Context, arguments json.RawMessage) (string, error) {
	supplied := map[string]any{}
	if len(arguments) > 0 && json.Valid(arguments) {
		var decoded map[string]any
		if err := json.Unmarshal(arguments, &decoded); err == nil && decoded != nil {
			supplied = decoded
		}
	}
	parameters, fromAI, err := fillToolFromAI(tool.node.Parameters, supplied)
	if err != nil {
		return "", fmt.Errorf("tool %q: %w", tool.name, err)
	}
	if err := checkNoModelTemplates(tool.node.Parameters, parameters, "parameters"); err != nil {
		return "", fmt.Errorf("tool %q: %w", tool.name, err)
	}
	// A copy of the node and of the request: a tool outlives one call, and
	// the next call must not see this one's values.
	node := tool.node
	node.Parameters = parameters
	request := tool.request
	request.FromAIArguments = fromAI

	output, err := tool.executor.Execute(ctx, node, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, request)
	if err != nil {
		return "", err
	}
	if len(output) == 0 || len(output[0]) == 0 {
		return "[]", nil
	}
	return httpToolObservation(output[0])
}

// checkNoModelTemplates refuses a plain string that gained `{{` when the
// model's values were filled in. Some executors read a plain `{{ … }}` string
// as a template the author wrote (the Google nodes, for n8n's nested
// parameters), so a model value must never be able to write one.
func checkNoModelTemplates(template, filled any, path string) error {
	if expression.IsExpression(template) {
		return nil
	}
	switch typed := template.(type) {
	case string:
		// A whole-string json call is filled with an object or a list, so
		// the model's value is searched at any depth.
		text, isText := filled.(string)
		if (isText && text != typed && strings.Contains(text, "{{")) || (!isText && holdsTemplateText(filled)) {
			return fmt.Errorf("%s holds {{ once the model's values were filled in, and a tool never lets the model write a template", path)
		}
	case map[string]any:
		object, _ := filled.(map[string]any)
		for key, nested := range typed {
			if err := checkNoModelTemplates(nested, object[key], path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		list, _ := filled.([]any)
		for index, nested := range typed {
			if index >= len(list) {
				break
			}
			if err := checkNoModelTemplates(nested, list[index], fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// holdsTemplateText reports whether a value holds a string with `{{` at any
// depth, keys included.
func holdsTemplateText(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "{{")
	case map[string]any:
		for key, nested := range typed {
			if strings.Contains(key, "{{") || holdsTemplateText(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if holdsTemplateText(nested) {
				return true
			}
		}
	}
	return false
}
