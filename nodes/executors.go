package nodes

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// RegisterExecutors installs native implementations for the core definitions.
//
// The HTTP policy and the database guard are both deployment decisions: a
// hosted install must refuse private targets, while a self-hosted one may
// legitimately call services on its own network, and the guard carries the
// install's own database paths so a SQLite credential can never open them.
func RegisterExecutors(registry *engine.Registry, httpPolicy safehttp.Policy, databaseGuard sqlnode.Guard, agentRuntime ai.AgentRuntime, agentMemory ai.Memory, codeCompiler runcode.Compiler) error {
	for id, executor := range map[string]engine.Executor{
		"core.manual":             engine.ExecutorFunc(executeManual),
		"core.set":                engine.ExecutorFunc(executeSet),
		"core.if":                 engine.ExecutorFunc(executeIF),
		"core.merge":              engine.ExecutorFunc(executeMerge),
		HTTPExecutorID:            NewHTTPExecutor(httpPolicy),
		WebhookExecutorID:         engine.ExecutorFunc(executeWebhook),
		ScheduleExecutorID:        engine.ExecutorFunc(executeSchedule),
		RespondExecutorID:         engine.ExecutorFunc(executeRespond),
		PostgresExecutorID:        NewDatabaseExecutor(sqlnode.DriverPostgres, "postgres", databaseGuard),
		MySQLExecutorID:           NewDatabaseExecutor(sqlnode.DriverMySQL, "mysql", databaseGuard),
		SQLiteExecutorID:          NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", databaseGuard),
		ChatModelExecutorID:       engine.ExecutorFunc(executeChatModel),
		MemoryExecutorID:          engine.ExecutorFunc(executeMemory),
		HTTPToolExecutorID:        engine.ExecutorFunc(executeHTTPTool),
		AgentExecutorID:           NewAgentExecutor(agentRuntime, httpPolicy, agentMemory),
		CodeExecutorID:            NewCodeExecutor(codeCompiler, runcode.NewMemoryCache(), runcode.DefaultLimits()),
		LoopExecutorID:            engine.ExecutorFunc(executeLoop),
		StickyNoteExecutorID:      engine.ExecutorFunc(executeStickyNote),
		TelegramTriggerExecutorID: engine.ExecutorFunc(executeTelegramTrigger),
		UnsupportedExecutorID:     engine.ExecutorFunc(executeUnsupported),
	} {
		if err := registry.Register(id, executor); err != nil {
			return err
		}
	}
	return nil
}

func executeManual(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}

// executeSet writes assignments onto every incoming item.
//
// Parameters are resolved *per item*, not once for the node. That is the whole
// difference between a Set node and a constant: an assignment reading
// `$json.name` has to see the item it is being written onto, and resolving once
// outside the loop writes the first item's value onto all of them.
func executeSet(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if declared, ok := node.Parameters["assignments"].(map[string]any); !ok || len(declared) == 0 {
		return nil, fmt.Errorf("Set assignments must be a non-empty object")
	}
	items := make([]workflow.Item, 0, len(input["main"]))
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(node.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		assignments, _ := resolved["assignments"].(map[string]any)
		copy := cloneItem(item)
		for key, value := range assignments {
			copy.JSON[key] = cloneValue(value)
		}
		items = append(items, copy)
	}
	return workflow.NodeOutput{items}, nil
}

// executeIF routes each item by its condition.
//
// The condition's *value* is resolved per item too. A comparison against
// `{{ $json.tier }}` is the ordinary way to compare two fields of one item, and
// against an unresolved marker it compares against an object and takes the same
// branch every time.
func executeIF(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Validated once, before any item, so a malformed condition is one error
	// rather than one per row.
	if _, err := ifCondition(node.Parameters["conditions"]); err != nil {
		return nil, err
	}
	// IF filters, so an output item's position no longer matches its input's.
	// The runner only infers provenance when the counts match, and here they do
	// not — so each item keeps the origin it arrived with, which is the true
	// answer and is what makes a reach-back from either branch land on the
	// right item.
	trueItems, falseItems := []workflow.Item{}, []workflow.Item{}
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(node.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		condition, err := ifCondition(resolved["conditions"])
		if err != nil {
			return nil, err
		}
		matched, err := condition.matches(item.JSON)
		if err != nil {
			return nil, err
		}
		routed := cloneItem(item)
		if routed.Paired == nil {
			routed.Paired = &workflow.PairedItem{SourceNodeID: node.ID, SourcePort: "main", ItemIndex: index}
		}
		if matched {
			trueItems = append(trueItems, routed)
		} else {
			falseItems = append(falseItems, routed)
		}
	}
	return workflow.NodeOutput{trueItems, falseItems}, nil
}

// executeMerge concatenates two streams.
//
// It deliberately does not resolve expressions: its only parameter is a mode
// chosen from a fixed list, and an expression there would name a mode that
// depends on the data, which is not a thing this node offers. Adding a resolve
// call it does not need would be a call nobody could explain.
func executeMerge(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mode, _ := node.Parameters["mode"].(string); mode != "append" {
		return nil, fmt.Errorf("Merge mode must be append")
	}
	// Merge concatenates two unrelated streams, so an output item's position
	// says nothing about where it came from. Each side keeps the provenance it
	// arrived with rather than being renumbered — an item that came through
	// input2 still descends from whatever produced it, and flattening that
	// would make a later lookup confidently wrong instead of honestly unable.
	items := append(cloneItems(input["input1"]), cloneItems(input["input2"])...)
	return workflow.NodeOutput{items}, nil
}

func validateSetConfiguration(node workflow.Node) error {
	assignments, ok := node.Parameters["assignments"].(map[string]any)
	if !ok || len(assignments) == 0 {
		return fmt.Errorf("assignments must be a non-empty object")
	}
	for key := range assignments {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("assignment keys must not be empty")
		}
	}
	return nil
}

func validateIFConfiguration(node workflow.Node) error {
	_, err := ifCondition(node.Parameters["conditions"])
	return err
}

func validateMergeConfiguration(node workflow.Node) error {
	if mode, _ := node.Parameters["mode"].(string); mode != "append" {
		return fmt.Errorf("mode must be append")
	}
	return nil
}

type condition struct {
	field    string
	operator string
	value    any
}

func ifCondition(value any) (condition, error) {
	conditions, ok := value.([]any)
	if !ok || len(conditions) != 1 {
		return condition{}, fmt.Errorf("IF conditions must contain exactly one condition")
	}
	raw, ok := conditions[0].(map[string]any)
	if !ok {
		return condition{}, fmt.Errorf("IF condition must be an object")
	}
	field, fieldOK := raw["field"].(string)
	operator, operatorOK := raw["operator"].(string)
	if !fieldOK || field == "" || !operatorOK {
		return condition{}, fmt.Errorf("IF condition requires field and operator")
	}
	switch operator {
	case "equals", "notEquals":
		if _, found := raw["value"]; !found {
			return condition{}, fmt.Errorf("IF condition %q requires value", operator)
		}
	case "exists", "notExists":
	default:
		return condition{}, fmt.Errorf("IF condition operator %q is unsupported", operator)
	}
	return condition{field: field, operator: operator, value: raw["value"]}, nil
}

func (condition condition) matches(value map[string]any) (bool, error) {
	current := any(value)
	found := true
	for _, segment := range strings.Split(condition.field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			found = false
			break
		}
		current, ok = object[segment]
		if !ok {
			found = false
			break
		}
	}
	switch condition.operator {
	case "exists":
		return found, nil
	case "notExists":
		return !found, nil
	case "equals":
		return found && reflect.DeepEqual(current, condition.value), nil
	case "notEquals":
		return !found || !reflect.DeepEqual(current, condition.value), nil
	default:
		return false, fmt.Errorf("IF condition operator %q is unsupported", condition.operator)
	}
}

// cloneItem is the twin of the runner's, and carries provenance for the same
// reason: an executor that maps items one to one loses the correspondence the
// moment it appends to a fresh slice unless the clone brings it along.
func cloneItem(item workflow.Item) workflow.Item {
	cloned := workflow.Item{JSON: cloneMap(item.JSON)}
	if item.Binary != nil {
		cloned.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, value := range item.Binary {
			cloned.Binary[key] = value
		}
	}
	if item.Paired != nil {
		paired := *item.Paired
		cloned.Paired = &paired
	}
	return cloned
}

func cloneItems(items []workflow.Item) []workflow.Item {
	cloned := make([]workflow.Item, len(items))
	for index, item := range items {
		cloned[index] = cloneItem(item)
	}
	return cloned
}

func cloneMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}
