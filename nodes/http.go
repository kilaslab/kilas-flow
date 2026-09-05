package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// HTTPExecutorID is the server-owned binding for the HTTP Request node.
const HTTPExecutorID = "core.httpRequest"

func httpRequestNode() node.Definition {
	return node.Definition{
		Type:        "kilasflow.httpRequest",
		Version:     1,
		DisplayName: "HTTP Request",
		Description: "Calls an external HTTP API and returns its response as items.",
		Category:    "Core",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "method", Label: "Method", Kind: node.PropertySelect, Required: true, Default: "GET",
				Options: []node.PropertyOption{
					{Label: "GET", Value: "GET"}, {Label: "POST", Value: "POST"}, {Label: "PUT", Value: "PUT"},
					{Label: "PATCH", Value: "PATCH"}, {Label: "DELETE", Value: "DELETE"}, {Label: "HEAD", Value: "HEAD"},
				},
			},
			{
				Key: "url", Label: "URL", Kind: node.PropertyString, Required: true,
				Description: "Absolute http or https URL. Supports expressions.",
			},
			{Key: "sendQuery", Label: "Send query parameters", Kind: node.PropertyBoolean, Default: false},
			{
				Key: "queryParameters", Label: "Query parameters", Kind: node.PropertyKeyValue,
				VisibleWhen: []node.VisibilityCondition{{Key: "sendQuery", Equals: true}},
			},
			{Key: "sendHeaders", Label: "Send headers", Kind: node.PropertyBoolean, Default: false},
			{
				Key: "headers", Label: "Headers", Kind: node.PropertyKeyValue,
				VisibleWhen: []node.VisibilityCondition{{Key: "sendHeaders", Equals: true}},
			},
			{Key: "sendBody", Label: "Send body", Kind: node.PropertyBoolean, Default: false},
			{
				Key: "bodyType", Label: "Body type", Kind: node.PropertySelect, Default: "json",
				Options: []node.PropertyOption{
					{Label: "JSON", Value: "json"},
					{Label: "Form URL-encoded", Value: "form"},
					{Label: "Raw text", Value: "raw"},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "sendBody", Equals: true}},
			},
			{
				Key: "body", Label: "Body", Kind: node.PropertyString,
				Description: "JSON object, form fields as JSON, or raw text. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "sendBody", Equals: true}},
			},
			{
				Key: "responseFormat", Label: "Response format", Kind: node.PropertySelect, Default: "autodetect",
				Options: []node.PropertyOption{
					{Label: "Autodetect", Value: "autodetect"},
					{Label: "JSON", Value: "json"},
					{Label: "Text", Value: "text"},
				},
			},
			{
				Key: "neverError", Label: "Never fail on HTTP error status", Kind: node.PropertyBoolean, Default: false,
				Description: "Return 4xx and 5xx responses as items instead of failing the node.",
			},
			{
				Key: "timeoutSeconds", Label: "Request timeout (seconds)", Kind: node.PropertyNumber, Default: 30,
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     HTTPExecutorID,
		Validate:       validateHTTPConfiguration,
	}
}

// validateHTTPConfiguration checks what can be known before an execution.
//
// A URL built from an expression is only knowable at run time, so the compiler
// checks the shape and the executor re-checks the resolved value against the
// SSRF policy.
func validateHTTPConfiguration(node workflow.Node) error {
	method, err := stringParameter(node.Parameters, "method")
	if err != nil {
		return err
	}
	if method != "" && !knownHTTPMethod(method) {
		return fmt.Errorf("method %q is not supported", method)
	}
	raw, err := stringParameter(node.Parameters, "url")
	if err != nil {
		return err
	}
	if expression.IsExpression(node.Parameters["url"]) {
		return nil
	}
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("url is required")
	}
	target, parseErr := url.Parse(raw)
	if parseErr != nil {
		return fmt.Errorf("url is not a valid URL")
	}
	if scheme := strings.ToLower(target.Scheme); scheme != "http" && scheme != "https" {
		return fmt.Errorf("url must use http or https")
	}
	if target.Host == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

func knownHTTPMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return true
	default:
		return false
	}
}

// stringParameter reads a parameter that may be a fixed string or an
// expression marker, returning the raw template for the latter.
func stringParameter(parameters map[string]any, key string) (string, error) {
	value, found := parameters[key]
	if !found || value == nil {
		return "", nil
	}
	if expression.IsExpression(value) {
		template, _ := value.(map[string]any)["value"].(string)
		return template, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text", key)
	}
	return text, nil
}

// HTTPExecutor performs outbound requests under an SSRF policy.
type HTTPExecutor struct {
	policy safehttp.Policy
	client *http.Client
}

// NewHTTPExecutor builds the HTTP Request executor for one policy.
func NewHTTPExecutor(policy safehttp.Policy) *HTTPExecutor {
	return &HTTPExecutor{policy: policy, client: safehttp.NewClient(policy)}
}

// Execute sends one request per incoming item and maps each response to an
// output item. Running per item is what lets a URL or body reference `$json`.
func (executor *HTTPExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	items := input["main"]
	if len(items) == 0 {
		// A trigger-less run still has to make the configured call once, with an
		// empty `$json`, or an HTTP node behind a webhook with no body would
		// silently do nothing.
		items = []workflow.Item{{JSON: map[string]any{}}}
	}

	results := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		resolved, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		result, err := executor.sendOne(ctx, ir, resolved, request)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return workflow.NodeOutput{results}, nil
}

func expressionContext(item workflow.Item, input workflow.NodeInput, request engine.Request, index int) expression.Context {
	inputItems := make(map[string][]map[string]any, len(input))
	for port, portItems := range input {
		converted := make([]map[string]any, len(portItems))
		for position, portItem := range portItems {
			converted[position] = portItem.JSON
		}
		inputItems[port] = converted
	}
	return expression.Context{
		JSON:      item.JSON,
		Input:     inputItems,
		Nodes:     request.NodeOutputs,
		Env:       request.Env,
		Execution: expression.ExecutionContext{ID: request.Execution.ID, Mode: request.Execution.Mode},
		ItemIndex: index,
	}
}

func (executor *HTTPExecutor) sendOne(ctx context.Context, ir workflow.IRNode, parameters map[string]any, request engine.Request) (workflow.Item, error) {
	method := strings.ToUpper(textValue(parameters["method"], http.MethodGet))
	if !knownHTTPMethod(method) {
		return workflow.Item{}, fmt.Errorf("node %q: method %q is not supported", ir.Name, method)
	}

	target, err := url.Parse(strings.TrimSpace(textValue(parameters["url"], "")))
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: url is not a valid URL", ir.Name)
	}
	if err := executor.policy.CheckURL(target); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if boolValue(parameters["sendQuery"]) {
		query := target.Query()
		for key, value := range mapValue(parameters["queryParameters"]) {
			query.Set(key, textValue(value, ""))
		}
		target.RawQuery = query.Encode()
	}

	body, contentType, err := requestBody(parameters)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	timeout := executor.policy.Timeout
	if seconds := numberValue(parameters["timeoutSeconds"]); seconds > 0 {
		requested := time.Duration(seconds * float64(time.Second))
		// The node may only tighten the deployment's ceiling, never raise it.
		if executor.policy.Timeout <= 0 || requested < executor.policy.Timeout {
			timeout = requested
		}
	}
	requestCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	httpRequest, err := http.NewRequestWithContext(requestCtx, method, target.String(), body)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: build request: %w", ir.Name, err)
	}
	if contentType != "" {
		httpRequest.Header.Set("Content-Type", contentType)
	}
	if boolValue(parameters["sendHeaders"]) {
		for key, value := range mapValue(parameters["headers"]) {
			httpRequest.Header.Set(key, textValue(value, ""))
		}
	}
	if err := executor.authenticate(requestCtx, ir, httpRequest, request); err != nil {
		return workflow.Item{}, err
	}

	response, err := executor.client.Do(httpRequest)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	defer response.Body.Close()

	contents, truncated, err := executor.policy.ReadBody(response.Body)
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: read response: %w", ir.Name, err)
	}
	if !boolValue(parameters["neverError"]) && response.StatusCode >= 400 {
		return workflow.Item{}, fmt.Errorf("node %q: request failed with status %d", ir.Name, response.StatusCode)
	}

	return workflow.Item{JSON: map[string]any{
		"statusCode": float64(response.StatusCode),
		"headers":    responseHeaders(response),
		"body":       decodeBody(contents, textValue(parameters["responseFormat"], "autodetect"), response.Header.Get("Content-Type")),
		"truncated":  truncated,
	}}, nil
}

// authenticate resolves and applies the node's credential.
//
// Ownership, type, and domain scope are all checked before the secret touches
// the request, so a workflow cannot point a credential at an arbitrary host.
func (executor *HTTPExecutor) authenticate(ctx context.Context, ir workflow.IRNode, httpRequest *http.Request, request engine.Request) error {
	credentialID := ""
	credentialType := ""
	for typeID, id := range ir.Credentials {
		if strings.TrimSpace(id) == "" {
			continue
		}
		credentialType, credentialID = typeID, id
		break
	}
	if credentialID == "" {
		return nil
	}
	if request.Credentials == nil {
		return fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, err := request.Credentials.ResolveCredential(ctx, credentialID)
	if err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if credentialType != "" && resolved.Type != credentialType {
		return fmt.Errorf("node %q: credential %q is a %s credential, not %s", ir.Name, resolved.Name, resolved.Type, credentialType)
	}
	scope := credentials.Record{AllowedDomains: resolved.AllowedDomains}
	if !scope.AllowsHost(httpRequest.URL.Host) {
		return fmt.Errorf("node %q: credential %q is not allowed for host %q", ir.Name, resolved.Name, httpRequest.URL.Hostname())
	}
	if err := credentials.Apply(httpRequest, resolved.Type, resolved.Fields); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return nil
}

func requestBody(parameters map[string]any) (io.Reader, string, error) {
	if !boolValue(parameters["sendBody"]) {
		return nil, "", nil
	}
	raw := textValue(parameters["body"], "")
	switch textValue(parameters["bodyType"], "json") {
	case "json":
		if strings.TrimSpace(raw) == "" {
			return nil, "application/json", nil
		}
		if !json.Valid([]byte(raw)) {
			return nil, "", fmt.Errorf("body is not valid JSON")
		}
		return strings.NewReader(raw), "application/json", nil
	case "form":
		fields := map[string]any{}
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				return nil, "", fmt.Errorf("form body must be a JSON object of fields")
			}
		}
		form := url.Values{}
		for key, value := range fields {
			form.Set(key, textValue(value, ""))
		}
		return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
	default:
		return strings.NewReader(raw), "text/plain; charset=utf-8", nil
	}
}

// responseHeaders keeps one value per header. Credential material in a
// response is redacted again at the persistence boundary.
func responseHeaders(response *http.Response) map[string]any {
	headers := make(map[string]any, len(response.Header))
	for key, values := range response.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}

func decodeBody(contents []byte, format, contentType string) any {
	wantsJSON := format == "json" || (format == "autodetect" && strings.Contains(strings.ToLower(contentType), "json"))
	if wantsJSON {
		var decoded any
		if err := json.Unmarshal(bytes.TrimSpace(contents), &decoded); err == nil {
			return decoded
		}
		// An upstream that mislabels its content type should not fail the node;
		// the raw text is still the honest answer.
	}
	return string(contents)
}

func textValue(value any, fallback string) string {
	switch typed := value.(type) {
	case nil:
		return fallback
	case string:
		if typed == "" {
			return fallback
		}
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fallback
		}
		return string(encoded)
	}
}

func boolValue(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func mapValue(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}
