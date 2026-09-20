package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
		Type: "kilasflow.httpRequest",
		Credentials: []node.CredentialRequirement{
			{Type: "httpBasicAuth"},
			{Type: "httpHeaderAuth"},
			{Type: "httpBearerAuth"},
		},
		Version:     workflow.V(1),
		DisplayName: "HTTP Request",
		Description: "Calls an external HTTP API and returns its response as items.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "builtin:globe"},
		IconColor:   "#10b981",
		Subtitle:    "{{ $parameter.method }} {{ $parameter.url }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "method", Label: "Method", Kind: node.PropertyOptions, Required: true, Default: "GET",
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
				Key: "bodyType", Label: "Body type", Kind: node.PropertyOptions, Default: "json",
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
				Key: "bodyFields", Label: "Body fields", Kind: node.PropertyKeyValue,
				Description: "One field per row. Each value is resolved for the item being sent and the " +
					"body is encoded after resolution, so `{{ $json.id }}` sends the id rather than the " +
					"template. Leave every row empty to use the Body field above instead.",
				VisibleWhen: []node.VisibilityCondition{{Key: "sendBody", Equals: true}},
			},
			{
				Key: "rawContentType", Label: "Raw content type", Kind: node.PropertyString,
				Default:     "text/plain; charset=utf-8",
				Description: "Content type sent with a raw body.",
				VisibleWhen: []node.VisibilityCondition{
					{Key: "sendBody", Equals: true}, {Key: "bodyType", Equals: "raw"},
				},
			},
			{
				Key: "responseFormat", Label: "Response format", Kind: node.PropertyOptions, Default: "autodetect",
				Options: []node.PropertyOption{
					{Label: "Autodetect", Value: "autodetect"},
					{Label: "JSON", Value: "json"},
					{Label: "Text", Value: "text"},
					{Label: "File", Value: "file"},
				},
				Description: "Autodetect attaches a response that is not text or JSON under the binary " +
					"property `data` when this server has binary storage configured, and decodes it as " +
					"text otherwise. File always attaches, and says so if storage is unavailable.",
			},
			{
				Key: "outputPropertyName", Label: "Binary property", Kind: node.PropertyString, Default: "data",
				Description: "Which binary property on the output item the response is attached to.",
				VisibleWhen: []node.VisibilityCondition{{Key: "responseFormat", Equals: "file"}},
			},
			{
				Key: "fullResponse", Label: "Include response headers and status", Kind: node.PropertyBoolean, Default: false,
				Description: "Off, the parsed response body is the item — an object as it is, a top-level array as one " +
					"item per element, anything else under `data`, so `$json.<field>` reads the response. On, the item " +
					"is {body, headers, statusCode, statusMessage} with lower-case header names.",
			},
			{
				Key: "followRedirects", Label: "Follow redirects", Kind: node.PropertyBoolean, Default: false,
				Description: "Off, a 3xx response is returned to the workflow as it is. On, the request follows up to " +
					"the deployment's redirect ceiling.",
			},
			{
				Key: "neverError", Label: "Never fail on HTTP error status", Kind: node.PropertyBoolean, Default: false,
				Description: "Return 4xx and 5xx responses as items instead of failing the node.",
			},
			{
				Key: "requestTimeoutSeconds", Label: "Request timeout (seconds)", Kind: node.PropertyNumber, Default: 30,
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
	// client follows redirects up to the policy's ceiling.
	client *http.Client
	// stopping hands the redirect response itself back to the node, which is
	// n8n's default.
	stopping *http.Client
}

// NewHTTPExecutor builds the HTTP Request executor for one policy.
//
// Two clients rather than one. n8n's HTTP Request node does not follow
// redirects unless it is asked to, and it hands the 302 itself to the workflow;
// KilasFlow followed them unconditionally, so `options.redirect.followRedirects
// = false` could not be expressed and a workflow that wanted to inspect a
// redirect never saw one. Both clients share the policy's transport, so the
// redirect hop check, the address allowlist and the credential domain scope all
// still apply on the following client.
func NewHTTPExecutor(policy safehttp.Policy) *HTTPExecutor {
	following := safehttp.NewClient(policy)
	stopping := safehttp.NewClient(policy)
	stopping.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPExecutor{policy: policy, client: following, stopping: stopping}
}

// redirectClient returns the client a node's redirect policy selects.
func (executor *HTTPExecutor) redirectClient(parameters map[string]any) *http.Client {
	if boolValue(parameters["followRedirects"]) {
		return executor.client
	}
	return executor.stopping
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
		produced, err := executor.sendOne(ctx, ir, resolved, request)
		if err != nil {
			return nil, err
		}
		results = append(results, produced...)
	}
	return workflow.NodeOutput{results}, nil
}

// expressionContext is the runtime's shared context builder, kept as a local
// name because every executor in this package calls it.
func expressionContext(item workflow.Item, input workflow.NodeInput, request engine.Request, index int) expression.Context {
	return request.ExpressionContext(item, input, index)
}

func (executor *HTTPExecutor) sendOne(ctx context.Context, ir workflow.IRNode, parameters map[string]any, request engine.Request) ([]workflow.Item, error) {
	method := strings.ToUpper(textValue(parameters["method"], http.MethodGet))
	if !knownHTTPMethod(method) {
		return nil, fmt.Errorf("node %q: method %q is not supported", ir.Name, method)
	}

	target, err := url.Parse(strings.TrimSpace(textValue(parameters["url"], "")))
	if err != nil {
		return nil, fmt.Errorf("node %q: url is not a valid URL", ir.Name)
	}
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
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
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	timeout := executor.policy.Timeout
	if seconds := timeoutParameter(parameters, "requestTimeoutSeconds"); seconds > 0 {
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
		return nil, fmt.Errorf("node %q: build request: %w", ir.Name, err)
	}
	if contentType != "" {
		httpRequest.Header.Set("Content-Type", contentType)
	}
	if boolValue(parameters["sendHeaders"]) {
		for key, value := range mapValue(parameters["headers"]) {
			httpRequest.Header.Set(key, textValue(value, ""))
		}
	}
	if err := request.Authenticate(requestCtx, ir, httpRequest); err != nil {
		return nil, err
	}

	response, err := executor.redirectClient(parameters).Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	defer response.Body.Close()

	contents, truncated, err := executor.policy.ReadBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("node %q: read response: %w", ir.Name, err)
	}
	if !boolValue(parameters["neverError"]) && response.StatusCode >= 400 {
		return nil, fmt.Errorf("node %q: request failed with status %d", ir.Name, response.StatusCode)
	}

	format := textValue(parameters["responseFormat"], "autodetect")
	responseType := response.Header.Get("Content-Type")

	if !storesAFile(format, responseType, truncated, request.Binaries != nil) {
		return decodeItems(response, contents, format, responseType, truncated, parameters), nil
	}

	// A truncated payload is refused rather than stored. Half a PDF that
	// reports success is worse than a failure naming the bound, and the item
	// carries no length a downstream node could check against.
	if truncated {
		return nil, fmt.Errorf("node %q: response exceeds the configured size limit and cannot be stored as a file", ir.Name)
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this server", ir.Name)
	}
	reference, err := request.Binaries.Put(responseFileName(response, target), mediaType(responseType), bytes.NewReader(contents))
	if err != nil {
		return nil, fmt.Errorf("node %q: store response: %w", ir.Name, err)
	}
	// The payload itself never enters the item, the execution record or a log
	// line — only the reference does.
	item := workflow.Item{
		JSON: map[string]any{},
		Binary: map[string]workflow.BinaryRef{
			textValue(parameters["outputPropertyName"], "data"): reference,
		},
	}
	if boolValue(parameters["fullResponse"]) {
		// n8n keeps the envelope beside the file when both were asked for, so
		// a workflow reading `$json.statusCode` still resolves.
		item.JSON = responseEnvelope(response, "", truncated)
	}
	return []workflow.Item{item}, nil
}

// decodeItems turns one response body into the items n8n would produce.
//
// n8n's default output is the parsed body itself, not an envelope around it: an
// object becomes the item, a top-level array becomes one item per element, any
// other body lands under `data`, and an empty body yields `{}`. KilasFlow used
// to wrap every response in {body, headers, statusCode, truncated}, so every
// `$json.<field>` downstream of an imported HTTP Request resolved to nothing —
// 199 nodes across the template corpus.
//
// fullResponse asks for the envelope instead, and then the shape is exactly
// n8n's: {body, headers, statusCode, statusMessage} with lower-case header
// names.
func decodeItems(response *http.Response, contents []byte, format, contentType string, truncated bool, parameters map[string]any) []workflow.Item {
	body := decodeBody(contents, format, contentType)
	if boolValue(parameters["fullResponse"]) {
		return []workflow.Item{{JSON: responseEnvelope(response, bodyString(contents, body), truncated)}}
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		// A 204, a HEAD response, or an empty body: n8n answers with an empty
		// object rather than `{data: ""}`, so a downstream node reading a
		// field it expected to be absent behaves the same on both platforms.
		return []workflow.Item{{JSON: map[string]any{}}}
	}

	switch typed := body.(type) {
	case map[string]any:
		if truncated {
			// A truncated body is incomplete, and the parsed keys are the only
			// place the loss can be reported without hiding it in a wrapper
			// the workflow does not read.
			typed = copyObject(typed)
			typed["truncated"] = true
		}
		return []workflow.Item{{JSON: typed}}
	case []any:
		items := make([]workflow.Item, 0, len(typed))
		for _, entry := range typed {
			if object, ok := entry.(map[string]any); ok {
				items = append(items, workflow.Item{JSON: object})
				continue
			}
			// An array element that is not an object cannot be an item, so it
			// is carried under `data` — the same key n8n uses for a body that
			// is not an object — rather than dropped.
			items = append(items, workflow.Item{JSON: map[string]any{"data": entry}})
		}
		if len(items) == 0 {
			return []workflow.Item{{JSON: map[string]any{}}}
		}
		return items
	case nil:
		return []workflow.Item{{JSON: map[string]any{}}}
	default:
		item := map[string]any{"data": typed}
		if truncated {
			item["truncated"] = true
		}
		return []workflow.Item{{JSON: item}}
	}
}

// responseEnvelope builds n8n's full-response shape.
//
// Header names are lower-cased, which is what n8n's item carries and what an
// expression like `$json.headers['content-type']` is written against. The
// status message is added because n8n's envelope has one and an imported node
// reading it would otherwise resolve to nothing.
func responseEnvelope(response *http.Response, body string, truncated bool) map[string]any {
	envelope := map[string]any{
		"body":          body,
		"headers":       lowerCaseHeaders(response),
		"statusCode":    float64(response.StatusCode),
		"statusMessage": http.StatusText(response.StatusCode),
	}
	if truncated {
		envelope["truncated"] = true
	}
	return envelope
}

// lowerCaseHeaders is n8n's header map: every name in lower case, so an
// expression written against n8n (`$json.headers['content-type']`) resolves.
func lowerCaseHeaders(response *http.Response) map[string]any {
	headers := make(map[string]any, len(response.Header))
	for key, values := range response.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	return headers
}

func copyObject(object map[string]any) map[string]any {
	copied := make(map[string]any, len(object)+1)
	for key, value := range object {
		copied[key] = value
	}
	return copied
}

func bodyString(contents []byte, body any) string {
	if len(contents) == 0 {
		return ""
	}
	if text, ok := body.(string); ok {
		return text
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return string(contents)
	}
	return string(encoded)
}

// storesAFile decides whether a response is attached instead of decoded.
//
// `file` is a request, and one that cannot be honoured is an error the caller
// should see — a node asked for a file and got a string is a silent wrong
// answer. `autodetect` is a preference: it diverts a non-text response only
// when there is somewhere to divert it to, and otherwise decodes it exactly as
// it did before. Binary storage is off by default, so the alternative would be
// every non-text response on every unconfigured server failing at once.
func storesAFile(format, contentType string, truncated, configured bool) bool {
	if format == "file" {
		return true
	}
	if format != "autodetect" || isTextual(contentType) {
		return false
	}
	return configured && !truncated
}

// isTextual reports whether a content type is meant to be read as text.
//
// An absent content type is treated as text: that is what an ordinary API that
// forgot the header sends, and turning every unlabelled response into a file
// would be a worse default than the one it replaces.
func isTextual(contentType string) bool {
	media := strings.ToLower(mediaType(contentType))
	if media == "" {
		return true
	}
	if strings.HasPrefix(media, "text/") {
		return true
	}
	switch media {
	case "application/json", "application/xml", "application/xhtml+xml",
		"application/javascript", "application/x-www-form-urlencoded",
		"application/ld+json", "application/problem+json",
		"application/ndjson", "application/x-ndjson",
		"application/yaml", "application/x-yaml",
		"application/graphql", "application/sql":
		return true
	}
	// Structured suffixes: application/vnd.api+json and friends are text.
	return strings.HasSuffix(media, "+json") || strings.HasSuffix(media, "+xml")
}

// mediaType strips parameters from a Content-Type header.
func mediaType(contentType string) string {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	return media
}

// responseFileName names the stored payload, preferring what the server said.
//
// The name is metadata — the payload is keyed by a generated ID, never by this
// — but it is shown to a person and may be handed to another node, so a remote
// server does not get to put a path in it.
func responseFileName(response *http.Response, target *url.URL) string {
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		if _, parameters, err := mime.ParseMediaType(disposition); err == nil {
			if name := plainFileName(parameters["filename"]); name != "" {
				return name
			}
		}
	}
	if target != nil {
		if name := plainFileName(target.Path); name != "" {
			return name
		}
	}
	return "data"
}

// plainFileName reduces a candidate to a single path-free segment, or nothing.
//
// Both separators are cut, not just this platform's: the header comes off the
// wire, so a Windows-shaped path is exactly as likely as a POSIX one and
// `filepath.Base` would keep it whole on a Linux server.
func plainFileName(candidate string) string {
	name := strings.TrimSpace(candidate)
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	if name == "." || name == ".." {
		return ""
	}
	return name
}

func requestBody(parameters map[string]any) (io.Reader, string, error) {
	if !boolValue(parameters["sendBody"]) {
		return nil, "", nil
	}
	kind := textValue(parameters["bodyType"], "json")
	// Fields are encoded *after* the per-item expression pass, which is the
	// whole point of holding them as a map: `{{ $json.id }}` reaches this
	// function as the item's id, and the wire sees the value rather than the
	// template that produced it.
	if fields, present := objectValue(parameters["bodyFields"]); present && len(fields) > 0 {
		switch kind {
		case "form":
			form := url.Values{}
			for key, value := range fields {
				form.Set(key, textValue(value, ""))
			}
			return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
		default:
			encoded, err := json.Marshal(fields)
			if err != nil {
				return nil, "", fmt.Errorf("body fields are not encodable as JSON")
			}
			return strings.NewReader(string(encoded)), "application/json", nil
		}
	}
	raw := textValue(parameters["body"], "")
	switch kind {
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
		return strings.NewReader(raw), textValue(parameters["rawContentType"], "text/plain; charset=utf-8"), nil
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

// objectValue distinguishes an absent object from an empty one.
//
// `mapValue` cannot: a parameter that is not a map and a parameter that is an
// empty map both read as nil, and "no fields were configured" has to fall back
// to the Body field while "two fields were configured" must not.
func objectValue(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok
}
