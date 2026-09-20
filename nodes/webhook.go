package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
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

// Webhook authentication modes.
//
// jwtAuth is accepted from an import and refused at activation rather than
// rewritten to "none": an n8n endpoint behind a JWT arrived here unauthenticated
// and activated, which silently published a protected endpoint. Refusing to
// activate is a visible failure an operator can act on; silently dropping the
// check is not.
const (
	WebhookAuthNone   = "none"
	WebhookAuthBasic  = "basicAuth"
	WebhookAuthHeader = "headerAuth"
	WebhookAuthJWT    = "jwtAuth"
)

// credentialTypesForAuth is the credential each auth mode needs attached.
var credentialTypesForAuth = map[string]string{
	WebhookAuthBasic:  "httpBasicAuth",
	WebhookAuthHeader: "httpHeaderAuth",
	WebhookAuthJWT:    "jwtAuth",
}

// Response modes decide when and with what a webhook request is answered.
const (
	ResponseModeImmediate = "immediate"
	ResponseModeLastNode  = "lastNode"
	ResponseModeNode      = "responseNode"
)

// Response data shapes the body the lastNode mode returns, using n8n's names.
const (
	ResponseDataFirstEntryJSON = "firstEntryJson"
	ResponseDataAllEntries     = "allEntries"
	ResponseDataNone           = "noData"
)

// respondWith values a Respond to Webhook node may take, using n8n's names.
const (
	RespondWithText              = "text"
	RespondWithJSON              = "json"
	RespondWithAllIncomingItems  = "allIncomingItems"
	RespondWithFirstIncomingItem = "firstIncomingItem"
	RespondWithNoData            = "noData"
	RespondWithRedirect          = "redirect"
	RespondWithBinary            = "binary"
	RespondWithJWT               = "jwt"
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
				// GET is n8n's own default, which is why an n8n export of a GET
				// webhook carries no method at all — and why every such import
				// used to bind POST and answer 404 to its own callers.
				Key: "httpMethod", Label: "HTTP method", Kind: node.PropertyOptions, Required: true, Default: http.MethodGet,
				Options: []node.PropertyOption{
					{Label: "GET", Value: http.MethodGet}, {Label: "POST", Value: http.MethodPost},
					{Label: "PUT", Value: http.MethodPut}, {Label: "PATCH", Value: http.MethodPatch},
					{Label: "DELETE", Value: http.MethodDelete}, {Label: "HEAD", Value: http.MethodHead},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "multipleMethods", Equals: false}},
			},
			{
				Key: "multipleMethods", Label: "Allow Multiple HTTP Methods", Kind: node.PropertyBoolean, Default: false,
				Description: "Answer several methods on one endpoint, which is how n8n's v2.1 webhook works.",
			},
			{
				Key: "httpMethods", Label: "HTTP methods", Kind: node.PropertyMultiOptions, Default: []any{http.MethodGet},
				Description: "Every method this endpoint answers. One binding is created per method.",
				Options: []node.PropertyOption{
					{Label: "GET", Value: http.MethodGet}, {Label: "POST", Value: http.MethodPost},
					{Label: "PUT", Value: http.MethodPut}, {Label: "PATCH", Value: http.MethodPatch},
					{Label: "DELETE", Value: http.MethodDelete}, {Label: "HEAD", Value: http.MethodHead},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "multipleMethods", Equals: true}},
			},
			{
				Key: "authentication", Label: "Authentication", Kind: node.PropertyOptions, Required: true, Default: WebhookAuthNone,
				Options: []node.PropertyOption{
					{Label: "None", Value: WebhookAuthNone},
					{Label: "Basic auth", Value: WebhookAuthBasic},
					{Label: "Header auth", Value: WebhookAuthHeader},
				},
				Description: "An authenticated endpoint needs a credential of the matching type attached to the node " +
					"before the workflow can be activated.",
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
			{
				Key: "responseData", Label: "Response data", Kind: node.PropertyOptions, Default: ResponseDataFirstEntryJSON,
				Options: []node.PropertyOption{
					{Label: "First entry (JSON object)", Value: ResponseDataFirstEntryJSON},
					{Label: "All entries (array)", Value: ResponseDataAllEntries},
					{Label: "No data", Value: ResponseDataNone},
				},
				Description: "What the last node's items become in the response body.",
				VisibleWhen: []node.VisibilityCondition{{Key: "responseMode", Equals: ResponseModeLastNode}},
			},
			{
				// n8n's own options collection, key for key, so an imported node's
				// settings land in the same controls and a saved native node
				// means the same thing on both platforms.
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "responseCode", Label: "Response Code", Kind: node.PropertyNumber,
						Description: "The status the caller is answered with.",
					},
					{
						Key: "responseData", Label: "Response Data", Kind: node.PropertyString,
						Description: "A custom acknowledgement body, sent instead of the default message.",
						VisibleWhen: []node.VisibilityCondition{{Key: "responseMode", Equals: ResponseModeImmediate}},
					},
					{
						Key: "responseHeaders", Label: "Response Headers", Kind: node.PropertyKeyValue,
						Description: "Headers added to the acknowledgement.",
					},
					{
						Key: "noResponseBody", Label: "No Response Body", Kind: node.PropertyBoolean, Default: false,
						Description: "Acknowledge with the status and no body at all.",
						VisibleWhen: []node.VisibilityCondition{{Key: "responseMode", Equals: ResponseModeImmediate}},
					},
					{
						Key: "allowedOrigins", Label: "Allowed Origins (CORS)", Kind: node.PropertyString, Default: "*",
						Description: "Comma-separated origins allowed to call this endpoint from a browser. `*` accepts any.",
					},
					{
						Key: "ipWhitelist", Label: "IP Allow-list", Kind: node.PropertyString,
						Description: "Comma-separated addresses or CIDR ranges allowed to call this endpoint. " +
							"Empty accepts every caller. The check uses the connection's own peer address and " +
							"deliberately does not trust X-Forwarded-For, so behind a reverse proxy the list " +
							"should also be enforced there.",
					},
					{
						Key: "ignoreBots", Label: "Ignore Bots", Kind: node.PropertyBoolean, Default: false,
						Description: "Deliveries whose User-Agent names a crawler are acknowledged and not run.",
					},
					{
						Key: "rawBody", Label: "Raw Body", Kind: node.PropertyBoolean, Default: false,
						Description: "Send the acknowledgement with the body exactly as configured rather than as JSON.",
						VisibleWhen: []node.VisibilityCondition{{Key: "responseMode", Equals: ResponseModeImmediate}},
					},
				},
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
			{
				// n8n's default is the first incoming item, and an import that
				// omits the parameter (because it holds the default) used to be
				// mapped to an empty text response.
				Key: "respondWith", Label: "Respond with", Kind: node.PropertyOptions, Required: true, Default: RespondWithFirstIncomingItem,
				Options: []node.PropertyOption{
					{Label: "Text", Value: RespondWithText},
					{Label: "JSON", Value: RespondWithJSON},
					{Label: "All incoming items", Value: RespondWithAllIncomingItems},
					{Label: "First incoming item", Value: RespondWithFirstIncomingItem},
					{Label: "No data", Value: RespondWithNoData},
					{Label: "Redirect", Value: RespondWithRedirect},
				},
			},
			{Key: "responseCode", Label: "Status code", Kind: node.PropertyNumber, Required: true, Default: 200},
			{
				Key: "responseBody", Label: "Body", Kind: node.PropertyString,
				Description: "JSON or text returned to the caller. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{
					{Key: "respondWith", Equals: RespondWithText},
				},
			},
			{
				Key: "responseBodyJSON", Label: "Body", Kind: node.PropertyJSON,
				Description: "The JSON returned to the caller. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "respondWith", Equals: RespondWithJSON}},
			},
			{
				Key: "redirectURL", Label: "Redirect to", Kind: node.PropertyString,
				Description: "Where the caller is sent. The status code defaults to 307 unless you set one, so a " +
					"POST stays a POST — a 302 turns it into a GET in every browser.",
				VisibleWhen: []node.VisibilityCondition{{Key: "respondWith", Equals: RespondWithRedirect}},
			},
			{Key: "responseHeaders", Label: "Headers", Kind: node.PropertyKeyValue},
			{
				Key: "responseKey", Label: "Response key", Kind: node.PropertyString,
				Description: "Wrap a JSON response under this key, so the caller receives `{\"key\": …}` rather than " +
					"a bare object or array.",
			},
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
	for _, method := range webhookMethods(node.Parameters) {
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		default:
			return fmt.Errorf("httpMethod %q is not supported", method)
		}
	}
	authentication := textParameter(node.Parameters, "authentication")
	switch authentication {
	case "", WebhookAuthNone:
	case WebhookAuthBasic, WebhookAuthHeader:
		// A webhook configured to authenticate but with nothing to check
		// against used to activate and then answer 500 to every caller. Failing
		// at activation names the problem while it can still be fixed.
		required := credentialTypesForAuth[authentication]
		if !hasCredential(node, required) {
			return fmt.Errorf("this webhook authenticates with %s, so it needs a %s credential attached before it can be activated", authentication, required)
		}
	case WebhookAuthJWT:
		return fmt.Errorf("n8n's jwtAuth mode is not supported yet; the imported webhook will not activate " +
			"unauthenticated — attach an httpHeaderAuth credential and switch the mode to headerAuth")
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

// webhookMethods is every method the node answers on, from either the single
// or the multi-method form.
//
// A node with nothing configured is GET, which is n8n's own default.
func webhookMethods(parameters map[string]any) []string {
	if multiple, _ := parameters["multipleMethods"].(bool); multiple {
		methods := make([]string, 0, 2)
		for _, entry := range listParameter(parameters["httpMethods"]) {
			methods = append(methods, strings.ToUpper(entry))
		}
		if len(methods) > 0 {
			return methods
		}
	}
	method := strings.ToUpper(textParameter(parameters, "httpMethod"))
	if method == "" {
		return []string{http.MethodGet}
	}
	return []string{method}
}

// listParameter reads a multi-value parameter as a list of strings.
func listParameter(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		return values
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}

// hasCredential reports whether the node references a credential of one type.
func hasCredential(node workflow.Node, credentialType string) bool {
	id, present := node.Credentials[credentialType]
	return present && strings.TrimSpace(id) != ""
}

func validateRespondConfiguration(node workflow.Node) error {
	switch with := textParameter(node.Parameters, "respondWith"); with {
	case "", RespondWithText, RespondWithJSON, RespondWithAllIncomingItems,
		RespondWithFirstIncomingItem, RespondWithNoData:
	case RespondWithRedirect:
		if statementText(node.Parameters, "redirectURL") == "" {
			return fmt.Errorf("a redirect response needs a URL to redirect to")
		}
	case RespondWithBinary:
		return fmt.Errorf("responding with a binary file needs the response streamed from the binary store, " +
			"which this server does not do yet; respond with JSON carrying a link to the file instead")
	case RespondWithJWT:
		return fmt.Errorf("responding with a signed JWT needs a signing credential type this server does not " +
			"have; build the token in a Code node and respond with text")
	default:
		return fmt.Errorf("respondWith %q is not supported", with)
	}
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
		with := textValue(parameters["respondWith"], RespondWithFirstIncomingItem)
		code := int(numberValue(parameters["responseCode"]))
		if code == 0 {
			code = http.StatusOK
			if with == RespondWithRedirect {
				// A redirect with a 200 is not a redirect. n8n defaults this one
				// too rather than leaving a browser on a blank page, and its
				// default code is 307 — a 302 rewrites a POST into a GET.
				code = http.StatusTemporaryRedirect
			}
		}
		if code < 100 || code > 599 {
			return nil, fmt.Errorf("node %q: responseCode %d is not a valid HTTP status", ir.Name, code)
		}
		headers := map[string]any{}
		for key, value := range mapValue(parameters["responseHeaders"]) {
			headers[key] = textValue(value, "")
		}

		body, err := respondBody(with, parameters, items, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if with == RespondWithRedirect {
			headers["Location"] = textValue(parameters["redirectURL"], "")
		}
		if body, err = wrapResponseKey(body, textValue(parameters["responseKey"], "")); err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}

		// The items pass through unchanged. The response is *not* written onto
		// them: n8n's Respond node hands its input on as it received it, and a
		// `$response` field leaked into every downstream node's `$json` and into
		// the stored execution output.
		out = append(out, cloneItem(item))

		// The first item's response is the one the caller receives, which is
		// what n8n does with several items, so only it is published.
		if index == 0 {
			detail, err := json.Marshal(map[string]any{
				"statusCode": float64(code), "headers": headers, "body": body,
			})
			if err != nil {
				return nil, fmt.Errorf("node %q: encode the response: %w", ir.Name, err)
			}
			// An event rather than item data, so the HTTP boundary can answer
			// the caller the moment this node runs while the rest of the graph
			// keeps going.
			request.Events.Emit(engine.NodeEvent{NodeID: ir.ID, Name: webhook.ResponseEventName, Detail: detail})
		}
	}
	return workflow.NodeOutput{out}, nil
}

// wrapResponseKey wraps a JSON response under the key the node names.
//
// n8n's `responseKey` option exists so a caller receives `{"data": […]}` rather
// than a bare array, which several frontends require. A body that is not JSON is
// left alone: wrapping text in a JSON envelope is not what the option means.
func wrapResponseKey(body, key string) (string, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(body) == "" || !json.Valid([]byte(body)) {
		return body, nil
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{key: json.RawMessage(body)})
	if err != nil {
		return "", fmt.Errorf("wrap the response under %q: %w", key, err)
	}
	return string(wrapped), nil
}

// respondBody renders the response body for one respondWith choice.
//
// The whole item stream is available, not only the item being written, because
// two of n8n's choices are about the stream rather than the item: "all incoming
// items" is the array and "first incoming item" is the first of it regardless
// of which item this iteration is on.
func respondBody(with string, parameters map[string]any, items []workflow.Item, item workflow.Item) (string, error) {
	switch with {
	case "":
		// n8n's default, and what an import of a node holding the default
		// arrives as.
		return respondBody(RespondWithFirstIncomingItem, parameters, items, item)
	case RespondWithText:
		return textValue(parameters["responseBody"], ""), nil
	case RespondWithJSON:
		// The JSON field first, falling back to the text one: a node saved
		// before respondWith existed put its JSON in `responseBody`, and
		// reading only the new key would empty every one of them.
		if body := textValue(parameters["responseBodyJSON"], ""); body != "" {
			return body, nil
		}
		return textValue(parameters["responseBody"], ""), nil
	case RespondWithAllIncomingItems:
		payload := make([]map[string]any, 0, len(items))
		for _, incoming := range items {
			payload = append(payload, jsonOf(incoming))
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("encode the incoming items: %w", err)
		}
		return string(encoded), nil
	case RespondWithFirstIncomingItem:
		first := item
		if len(items) > 0 {
			first = items[0]
		}
		encoded, err := json.Marshal(jsonOf(first))
		if err != nil {
			return "", fmt.Errorf("encode the first incoming item: %w", err)
		}
		return string(encoded), nil
	case RespondWithNoData, RespondWithRedirect:
		// A redirect's meaning is in its Location header; a body would be
		// dead weight the caller never sees.
		return "", nil
	default:
		return "", fmt.Errorf("respondWith %q is not supported", with)
	}
}

func jsonOf(item workflow.Item) map[string]any {
	if item.JSON == nil {
		return map[string]any{}
	}
	return item.JSON
}
