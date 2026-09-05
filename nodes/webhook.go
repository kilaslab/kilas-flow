package nodes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Executor IDs for the trigger family.
const (
	WebhookExecutorID  = "core.webhook"
	RespondExecutorID  = "core.respondToWebhook"
	ScheduleExecutorID = "core.schedule"
)

// WebhookNodeType and RespondNodeType are needed outside this package to bind
// routes and to find the responding node in an execution result.
const (
	WebhookNodeType = "kilasflow.webhook"
	RespondNodeType = "kilasflow.respondToWebhook"
	ScheduleType    = "kilasflow.schedule"
)

// Webhook authentication modes approved for V1.
const (
	WebhookAuthNone   = "none"
	WebhookAuthBasic  = "basicAuth"
	WebhookAuthHeader = "headerAuth"
)

// Response modes decide when and with what a webhook request is answered.
const (
	ResponseModeImmediate = "immediate"
	ResponseModeLastNode  = "lastNode"
	ResponseModeNode      = "responseNode"
)

func webhookTrigger() node.Definition {
	return node.Definition{
		Type:        WebhookNodeType,
		Version:     workflow.V(1),
		DisplayName: "Webhook",
		Description: "Starts a workflow from an inbound HTTP request.",
		Category:    "Triggers",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "path", Label: "Path", Kind: node.PropertyString, Required: true,
				Description: "Path segment appended to /webhook/. Must be unique across active workflows.",
			},
			{
				Key: "httpMethod", Label: "HTTP method", Kind: node.PropertySelect, Required: true, Default: "POST",
				Options: []node.PropertyOption{
					{Label: "GET", Value: http.MethodGet}, {Label: "POST", Value: http.MethodPost},
					{Label: "PUT", Value: http.MethodPut}, {Label: "PATCH", Value: http.MethodPatch},
					{Label: "DELETE", Value: http.MethodDelete},
				},
			},
			{
				Key: "authentication", Label: "Authentication", Kind: node.PropertySelect, Required: true, Default: WebhookAuthNone,
				Options: []node.PropertyOption{
					{Label: "None", Value: WebhookAuthNone},
					{Label: "Basic auth", Value: WebhookAuthBasic},
					{Label: "Header auth", Value: WebhookAuthHeader},
				},
			},
			{
				Key: "responseMode", Label: "Respond", Kind: node.PropertySelect, Required: true, Default: ResponseModeImmediate,
				Options: []node.PropertyOption{
					{Label: "Immediately", Value: ResponseModeImmediate},
					{Label: "When the last node finishes", Value: ResponseModeLastNode},
					{Label: "Using a Respond to Webhook node", Value: ResponseModeNode},
				},
			},
			{
				Key: "responseCode", Label: "Immediate response code", Kind: node.PropertyNumber, Default: 200,
				VisibleWhen: []node.VisibilityCondition{{Key: "responseMode", Equals: ResponseModeImmediate}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     WebhookExecutorID,
		Validate:       validateWebhookConfiguration,
	}
}

func respondToWebhookNode() node.Definition {
	return node.Definition{
		Type:        RespondNodeType,
		Version:     workflow.V(1),
		DisplayName: "Respond to Webhook",
		Description: "Produces the HTTP response returned to the webhook caller.",
		Category:    "Core",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{Key: "responseCode", Label: "Status code", Kind: node.PropertyNumber, Required: true, Default: 200},
			{
				Key: "responseBody", Label: "Body", Kind: node.PropertyString,
				Description: "JSON or text returned to the caller. Supports expressions.",
			},
			{Key: "responseHeaders", Label: "Headers", Kind: node.PropertyKeyValue},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     RespondExecutorID,
		Validate:       validateRespondConfiguration,
	}
}

func scheduleTrigger() node.Definition {
	return node.Definition{
		Type:        ScheduleType,
		Version:     workflow.V(1),
		DisplayName: "Schedule",
		Description: "Starts a workflow on a cron schedule.",
		Category:    "Triggers",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "cron", Label: "Cron expression", Kind: node.PropertyString, Required: true, Default: "0 * * * *",
				Description: "Standard five-field cron, evaluated in UTC.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ScheduleExecutorID,
		Validate:       validateScheduleConfiguration,
	}
}

// WebhookPath normalizes a configured path to the single form used for routing
// and for the uniqueness constraint.
func WebhookPath(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), "/")
}

func validateWebhookConfiguration(node workflow.Node) error {
	path := WebhookPath(textParameter(node.Parameters, "path"))
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if strings.ContainsAny(path, " ?#") {
		return fmt.Errorf("path must not contain spaces, ?, or #")
	}
	method := strings.ToUpper(textParameter(node.Parameters, "httpMethod"))
	switch method {
	case "", http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("httpMethod %q is not supported", method)
	}
	switch textParameter(node.Parameters, "authentication") {
	case "", WebhookAuthNone, WebhookAuthBasic, WebhookAuthHeader:
	default:
		return fmt.Errorf("authentication mode is not supported")
	}
	switch textParameter(node.Parameters, "responseMode") {
	case "", ResponseModeImmediate, ResponseModeLastNode, ResponseModeNode:
	default:
		return fmt.Errorf("responseMode is not supported")
	}
	return nil
}

func validateRespondConfiguration(node workflow.Node) error {
	// A status built from an expression is only knowable at run time; the
	// executor re-checks the resolved value.
	if expression.IsExpression(node.Parameters["responseCode"]) {
		return nil
	}
	code := int(numberValue(node.Parameters["responseCode"]))
	if code == 0 {
		return nil
	}
	if code < 100 || code > 599 {
		return fmt.Errorf("responseCode must be a valid HTTP status")
	}
	return nil
}

func validateScheduleConfiguration(node workflow.Node) error {
	if strings.TrimSpace(textParameter(node.Parameters, "cron")) == "" {
		return fmt.Errorf("cron is required")
	}
	return nil
}

// textParameter reads a fixed string parameter, treating an expression marker
// as absent because a trigger's routing configuration must be knowable before
// any execution exists to evaluate against.
func textParameter(parameters map[string]any, key string) string {
	value, _ := parameters[key].(string)
	return value
}

// executeWebhook emits the inbound request as the trigger item. The payload was
// already assembled and redacted at the HTTP boundary.
func executeWebhook(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}

// executeSchedule emits the scheduled fire time as the trigger item.
func executeSchedule(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}

// executeRespond shapes the HTTP response and passes items through unchanged.
//
// The response is data on the item, not a side effect: the node cannot write
// to a connection it does not own, and the HTTP boundary decides whether a
// response is still wanted once the run completes.
func executeRespond(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}

	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		// Resolved per item, like every other node: a response body that reads
		// `{{ $json.id }}` must see the item that reached this node, not the
		// unevaluated template.
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		code := int(numberValue(parameters["responseCode"]))
		if code == 0 {
			code = http.StatusOK
		}
		if code < 100 || code > 599 {
			return nil, fmt.Errorf("node %q: responseCode %d is not a valid HTTP status", ir.Name, code)
		}
		headers := map[string]any{}
		for key, value := range mapValue(parameters["responseHeaders"]) {
			headers[key] = textValue(value, "")
		}

		copied := cloneItem(item)
		copied.JSON[ResponseKey] = map[string]any{
			"statusCode": float64(code),
			"headers":    headers,
			"body":       textValue(parameters["responseBody"], ""),
		}
		out = append(out, copied)
	}
	return workflow.NodeOutput{out}, nil
}

// ResponseKey marks the item field carrying a webhook response. The HTTP
// boundary looks for exactly this key rather than guessing from shape.
const ResponseKey = "$response"
