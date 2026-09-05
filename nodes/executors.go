package nodes

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// RegisterExecutors installs native implementations for the core definitions.
//
// The HTTP policy is passed in because it is a deployment decision: a hosted
// install must refuse private targets, while a self-hosted one may legitimately
// call services on its own network.
func RegisterExecutors(registry *engine.Registry, httpPolicy safehttp.Policy) error {
	for id, executor := range map[string]engine.Executor{
		"core.manual":  engine.ExecutorFunc(executeManual),
		"core.set":     engine.ExecutorFunc(executeSet),
		"core.if":      engine.ExecutorFunc(executeIF),
		"core.merge":   engine.ExecutorFunc(executeMerge),
		HTTPExecutorID: NewHTTPExecutor(httpPolicy),
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

func executeSet(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	assignments, ok := node.Parameters["assignments"].(map[string]any)
	if !ok || len(assignments) == 0 {
		return nil, fmt.Errorf("Set assignments must be a non-empty object")
	}
	items := make([]workflow.Item, 0, len(input["main"]))
	for _, item := range input["main"] {
		copy := cloneItem(item)
		for key, value := range assignments {
			copy.JSON[key] = cloneValue(value)
		}
		items = append(items, copy)
	}
	return workflow.NodeOutput{items}, nil
}

func executeIF(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	condition, err := ifCondition(node.Parameters["conditions"])
	if err != nil {
		return nil, err
	}
	trueItems, falseItems := []workflow.Item{}, []workflow.Item{}
	for _, item := range input["main"] {
		matched, err := condition.matches(item.JSON)
		if err != nil {
			return nil, err
		}
		if matched {
			trueItems = append(trueItems, cloneItem(item))
		} else {
			falseItems = append(falseItems, cloneItem(item))
		}
	}
	return workflow.NodeOutput{trueItems, falseItems}, nil
}

func executeMerge(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mode, _ := node.Parameters["mode"].(string); mode != "append" {
		return nil, fmt.Errorf("Merge mode must be append")
	}
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

func cloneItem(item workflow.Item) workflow.Item {
	cloned := workflow.Item{JSON: cloneMap(item.JSON)}
	if item.Binary != nil {
		cloned.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, value := range item.Binary {
			cloned.Binary[key] = value
		}
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
