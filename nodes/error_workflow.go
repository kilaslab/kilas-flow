package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The two nodes an error workflow is built from, and the half of n8n's error
// handling that lives in a graph rather than in a setting.
//
// n8n's model: a workflow may name another workflow in its settings as its
// error workflow, and that workflow starts from an Error Trigger when the first
// one fails — not on a manual cancellation. A workflow can also stop itself on
// purpose with Stop And Error, which is how a graph says "this is an error, not
// an empty result"; the engine then runs the error workflow for it exactly as
// for a node that failed on its own.
const (
	// ErrorTriggerNodeType starts an error workflow. Its run's input is the
	// error payload the engine builds from the failure.
	ErrorTriggerNodeType = "kilasflow.errorTrigger"
	// ErrorTriggerExecutorID is the server-owned binding for it.
	ErrorTriggerExecutorID = "core.errorTrigger"
	// StopAndErrorNodeType ends a workflow with an error the author chose.
	StopAndErrorNodeType = "kilasflow.stopAndError"
	// StopAndErrorExecutorID is the server-owned binding for it.
	StopAndErrorExecutorID = "core.stopAndError"
)

// errorTriggerNode is a trigger with no inputs, which is what makes it a root
// the compiler can start a run from.
//
// It has one output and no parameters: everything it needs is the input the
// engine hands the run, which is the error object. A workflow that carries an
// Error Trigger beside another root — a webhook, a schedule, a manual trigger —
// is started from the named node, and the same mechanism that confines a
// sub-workflow call to its own trigger is what confines an error workflow to
// this one.
func errorTriggerNode() node.Definition {
	return node.Definition{
		Type:        ErrorTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "Error Trigger",
		Description: "Starts an error workflow when another workflow fails.",
		Category:    "Trigger",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:alert-triangle"},
		IconColor:   "#ef4444",
		Inputs:      []workflow.Port{},
		Outputs:     mainOutput(),
		Parameters:  []node.PropertyDefinition{},
		ExecutorID:  ErrorTriggerExecutorID,
	}
}

// stopAndErrorNode ends a workflow with a message, and a non-empty output list
// is deliberately absent: nothing runs after it, because the run is over.
func stopAndErrorNode() node.Definition {
	return node.Definition{
		Type:        StopAndErrorNodeType,
		Version:     workflow.V(1),
		DisplayName: "Stop and Error",
		Description: "Stops the workflow and fails it with the message you give.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:octagon-alert"},
		IconColor:   "#ef4444",
		Inputs:      mainInput(),
		Outputs:     []workflow.Port{},
		Parameters: []node.PropertyDefinition{
			{
				Key: "errorMessage", Label: "Error Message", Kind: node.PropertyString,
				Description: "The message the execution fails with. Expressions are resolved per item.",
			},
			{
				// JSON text, not a json-kind property: n8n's own node carries an
				// object here, and the importer writes it as a string so the
				// value survives a round trip through a document that may hold
				// either shape. Read by stopAndErrorMessage below.
				Key: "errorObject", Label: "Error Object", Kind: node.PropertyString,
				Description: "Error object imported from n8n as JSON text, carried so an imported workflow's own message and description survive. Used when no message is set.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     StopAndErrorExecutorID,
	}
}

// executeErrorTrigger passes the error the run was started with to the graph.
//
// The engine puts the error payload on the execution's input, so an Error
// Trigger node running that execution has nothing to add: a trigger that
// reshaped it would be a second definition of the payload's shape, and an
// imported workflow reading `{{ $json.execution.error.message }}` would then be
// reading whatever this node decided instead of what n8n documents.
func executeErrorTrigger(ctx context.Context, _ workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The engine starts an error run with the error object, sent the same way a
	// sub-workflow call sends its items: the payload rides under `items` on the
	// execution's input. A run started any other way — an author testing the
	// error workflow on its own — has no such key and emits what it was given,
	// so the workflow stays runnable by hand.
	if list, ok := request.Input.JSON["items"].([]any); ok {
		items := make([]workflow.Item, 0, len(list))
		for _, entry := range list {
			fields, ok := entry.(map[string]any)
			if !ok {
				fields = map[string]any{"value": entry}
			}
			items = append(items, workflow.Item{JSON: fields})
		}
		if len(items) == 0 {
			items = []workflow.Item{{JSON: map[string]any{}}}
		}
		return workflow.NodeOutput{items}, nil
	}
	if items := input["main"]; len(items) > 0 {
		return workflow.NodeOutput{items}, nil
	}
	return workflow.NodeOutput{{request.Input}}, nil
}

// executeStopAndError fails the node, which is the whole node. A workflow that
// reaches it stops there and its execution fails with the author's message, so
// the run's error workflow is started for it the same way a failed node's is.
func executeStopAndError(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Resolved against the first item, because the message is the node's own
	// and a Stop and Error after a Split runs once per branch: reading item 0
	// on every branch is what n8n does, and a per-item message would need one
	// node per branch to be useful.
	item := workflow.Item{JSON: map[string]any{}}
	if items := input["main"]; len(items) > 0 {
		item = items[0]
	}
	message, err := stopAndErrorMessage(node.Parameters, expressionContext(item, input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", node.Name, err)
	}
	return nil, errors.New(message)
}

// stopAndErrorMessage reads the message an author set and resolves any
// expression in it.
//
// Three shapes are accepted, because three exist in practice. A parameter
// carrying the expression marker (`{mode: "expression", value: …}`) is resolved
// by Resolve. A plain string with a `{{ … }}` template in it is one an author
// typed inline — n8n's own shape for this field, and what the importer carries
// through — so it is evaluated as a template. Anything else is the literal
// message. A message that cannot be resolved fails the node rather than
// stopping the workflow with a half-rendered sentence.
func stopAndErrorMessage(parameters map[string]any, ctx expression.Context) (string, error) {
	resolved, err := expression.Resolve(parameters, ctx)
	if err != nil {
		return "", err
	}
	message := strings.TrimSpace(textOf(resolved["errorMessage"]))
	if message == "" {
		object, ok := errorObjectOf(resolved["errorObject"])
		if ok {
			message = strings.TrimSpace(textOf(object["errorMessage"]))
			if message == "" {
				message = strings.TrimSpace(textOf(object["message"]))
			}
			if description := strings.TrimSpace(textOf(object["errorDescription"])); description != "" {
				if message == "" {
					message = description
				} else {
					message += ": " + description
				}
			}
		}
	}
	if !strings.Contains(message, "{{") {
		if message == "" {
			return "the workflow was stopped by a Stop and Error node with no message", nil
		}
		return message, nil
	}
	evaluated, err := expression.Evaluate(message, ctx)
	if err != nil {
		return "", err
	}
	rendered := strings.TrimSpace(textOf(evaluated))
	if rendered == "" {
		return "the workflow was stopped by a Stop and Error node with no message", nil
	}
	return rendered, nil
}

// errorObjectOf reads the error object an author gave, in either shape a
// document can hold: the JSON text the importer writes, or an object a
// hand-authored workflow may carry directly.
func errorObjectOf(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, false
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(trimmed), &object); err != nil {
			return nil, false
		}
		return object, true
	default:
		return nil, false
	}
}
