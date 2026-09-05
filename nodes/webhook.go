package nodes

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
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
		Type: WebhookNodeType,
		// A webhook uses a credential to authenticate callers, not to call out,
		// so only the modes the inbound boundary can verify are offered.
		Credentials: []node.CredentialRequirement{
			{Type: "httpBasicAuth"},
			{Type: "httpHeaderAuth"},
		},
		Version:     workflow.V(1),
		DisplayName: "Webhook",
		Description: "Starts a workflow from an inbound HTTP request.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		// The binding declaration lives with the node rather than at
		// composition, so the extractor never needs to know this type's name.
		Webhook: &node.WebhookDeclaration{
			Name:            "default",
			PathParameter:   "path",
			MethodParameter: "httpMethod",
			Method:          http.MethodPost,
		},
		Icon:      &node.NodeIcon{Light: "builtin:webhook"},
		IconColor: "#8b5cf6",
		Subtitle:  "{{ $parameter.httpMethod }} {{ $parameter.path }}",
		Outputs:   mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "path", Label: "Path", Kind: node.PropertyString, Required: true,
				Description: "A label for this endpoint. The public URL uses an opaque route minted on activation, so two workflows may share a label as long as they are in different tenants.",
			},
			{
				Key: "deliveryIdHeader", Label: "Delivery ID Header", Kind: node.PropertyString,
				Description: "Header carrying the sender's own identifier for a delivery, such as X-Webhook-Request-Id. When set, a repeated delivery with the same identifier is answered without running the workflow again. Leave empty to run every request.",
			},
			{
				Key: "httpMethod", Label: "HTTP method", Kind: node.PropertyOptions, Required: true, Default: "POST",
				Options: []node.PropertyOption{
					{Label: "GET", Value: http.MethodGet}, {Label: "POST", Value: http.MethodPost},
					{Label: "PUT", Value: http.MethodPut}, {Label: "PATCH", Value: http.MethodPatch},
					{Label: "DELETE", Value: http.MethodDelete},
				},
			},
			{
				Key: "authentication", Label: "Authentication", Kind: node.PropertyOptions, Required: true, Default: WebhookAuthNone,
				Options: []node.PropertyOption{
					{Label: "None", Value: WebhookAuthNone},
					{Label: "Basic auth", Value: WebhookAuthBasic},
					{Label: "Header auth", Value: WebhookAuthHeader},
				},
			},
			{
				Key: "responseMode", Label: "Respond", Kind: node.PropertyOptions, Required: true, Default: ResponseModeImmediate,
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
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "builtin:reply"},
		IconColor:   "#8b5cf6",
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
		Group:       []node.NodeGroup{node.GroupTrigger, node.GroupSchedule},
		Icon:        &node.NodeIcon{Light: "builtin:clock"},
		IconColor:   "#8b5cf6",
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "rule", Label: "Trigger Rules", Kind: node.PropertyFixedCollection,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add interval"},
				Description: "When this workflow runs. Several intervals may be added: \"every weekday at 09:00\" " +
					"and \"the 1st of the month at 06:00\" are two rules on one trigger, not two triggers. " +
					"Times are read in the workflow's timezone setting, which defaults to UTC.",
				Groups: []node.PropertyGroup{{
					Key: "interval", Label: "Trigger Interval",
					Fields: scheduleIntervalFields(),
				}},
			},
			{
				Key: "cron", Label: "Cron expression (legacy)", Kind: node.PropertyString,
				Description: "Kept for workflows saved before Trigger Rules existed. It is used only when no " +
					"rule is set; add a Custom (Cron) interval instead.",
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

// scheduleIntervalFields is n8n's interval form, field for field.
//
// Each unit's own "every N" field is shown only for that unit, and the
// trigger-at fields are shared across the units that use them, which is how
// n8n's own form behaves — so an imported node's stored parameters land in
// controls with the same names and the user sees what they saw there.
func scheduleIntervalFields() []node.PropertyDefinition {
	shownFor := func(fields ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(fields))
		for _, field := range fields {
			conditions = append(conditions, node.VisibilityCondition{Key: "field", Equals: field})
		}
		return conditions
	}
	return []node.PropertyDefinition{
		{
			Key: "field", Label: "Trigger Interval", Kind: node.PropertyOptions, Default: scheduler.FieldDays,
			Options: []node.PropertyOption{
				{Label: "Seconds", Value: scheduler.FieldSeconds},
				{Label: "Minutes", Value: scheduler.FieldMinutes},
				{Label: "Hours", Value: scheduler.FieldHours},
				{Label: "Days", Value: scheduler.FieldDays},
				{Label: "Weeks", Value: scheduler.FieldWeeks},
				{Label: "Months", Value: scheduler.FieldMonths},
				{Label: "Custom (Cron)", Value: scheduler.FieldCronExpression},
			},
		},
		{
			Key: "secondsInterval", Label: "Seconds Between Triggers", Kind: node.PropertyNumber, Default: 30,
			Description: "This server looks for due schedules every " + scheduler.TickResolution.String() +
				", so an interval shorter than that fires once per check rather than more often.",
			VisibleWhen: shownFor(scheduler.FieldSeconds),
		},
		{
			Key: "minutesInterval", Label: "Minutes Between Triggers", Kind: node.PropertyNumber, Default: 5,
			VisibleWhen: shownFor(scheduler.FieldMinutes),
		},
		{
			Key: "hoursInterval", Label: "Hours Between Triggers", Kind: node.PropertyNumber, Default: 1,
			VisibleWhen: shownFor(scheduler.FieldHours),
		},
		{
			Key: "daysInterval", Label: "Days Between Triggers", Kind: node.PropertyNumber, Default: 1,
			VisibleWhen: shownFor(scheduler.FieldDays),
		},
		{
			Key: "weeksInterval", Label: "Weeks Between Triggers", Kind: node.PropertyNumber, Default: 1,
			VisibleWhen: shownFor(scheduler.FieldWeeks),
		},
		{
			Key: "monthsInterval", Label: "Months Between Triggers", Kind: node.PropertyNumber, Default: 1,
			VisibleWhen: shownFor(scheduler.FieldMonths),
		},
		{
			Key: "triggerAtDayOfMonth", Label: "Trigger at Day of Month", Kind: node.PropertyNumber, Default: 1,
			Description: "1 to 31. A month without that day is skipped rather than moved.",
			VisibleWhen: shownFor(scheduler.FieldMonths),
		},
		{
			Key: "triggerAtDay", Label: "Trigger on Weekdays", Kind: node.PropertyMultiOptions, Default: []any{float64(0)},
			Options: []node.PropertyOption{
				{Label: "Sunday", Value: "0"}, {Label: "Monday", Value: "1"}, {Label: "Tuesday", Value: "2"},
				{Label: "Wednesday", Value: "3"}, {Label: "Thursday", Value: "4"}, {Label: "Friday", Value: "5"},
				{Label: "Saturday", Value: "6"},
			},
			VisibleWhen: shownFor(scheduler.FieldWeeks),
		},
		{
			Key: "triggerAtHour", Label: "Trigger at Hour", Kind: node.PropertyNumber, Default: 0,
			Description: "0 to 23, in the workflow's timezone.",
			VisibleWhen: shownFor(scheduler.FieldDays, scheduler.FieldWeeks, scheduler.FieldMonths),
		},
		{
			Key: "triggerAtMinute", Label: "Trigger at Minute", Kind: node.PropertyNumber, Default: 0,
			Description: "0 to 59.",
			VisibleWhen: shownFor(scheduler.FieldHours, scheduler.FieldDays, scheduler.FieldWeeks, scheduler.FieldMonths),
		},
		{
			Key: "expression", Label: "Expression", Kind: node.PropertyString, Default: "0 * * * *",
			Description: "Five-field cron, or six with a leading seconds field.",
			VisibleWhen: shownFor(scheduler.FieldCronExpression),
		},
	}
}

func validateScheduleConfiguration(n workflow.Node) error {
	intervals := scheduler.NodeIntervals(n.Parameters)
	if len(intervals) == 0 {
		return fmt.Errorf("a schedule needs at least one trigger rule")
	}
	for index, interval := range intervals {
		if err := interval.Validate(); err != nil {
			// Numbered from one and named by unit, because a rule with four
			// intervals gives "interval 3" nothing to match against otherwise.
			return fmt.Errorf("trigger rule %d (%s): %w", index+1, interval.Field, err)
		}
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
