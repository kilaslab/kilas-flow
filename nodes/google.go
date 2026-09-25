package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	googleDriveAPIBase = "https://www.googleapis.com/drive/v3"
	gmailAPIBase       = "https://gmail.googleapis.com/gmail/v1"
)

var (
	driveAPIRoot = googleDriveAPIBase
	gmailAPIRoot = gmailAPIBase
)

// GoogleClient calls Google APIs through the instance egress policy and the
// node's OAuth credential.
type GoogleClient struct {
	policy safehttp.Policy
	client *http.Client
}

// NewGoogleClient builds the shared Drive/Gmail HTTP client.
func NewGoogleClient(policy safehttp.Policy) *GoogleClient {
	return &GoogleClient{policy: policy, client: safehttp.NewClient(policy)}
}

func irFromNode(node workflow.Node) workflow.IRNode {
	return workflow.IRNode{
		ID: node.ID, Name: node.Name, Type: node.Type, TypeVersion: node.TypeVersion,
		Parameters: node.Parameters, Credentials: node.Credentials, Disabled: node.Disabled,
	}
}

func locatorValue(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if value := strings.TrimSpace(textValue(typed["value"], "")); value != "" {
			return value
		}
		if value := strings.TrimSpace(textValue(typed["id"], "")); value != "" {
			return value
		}
	default:
		return strings.TrimSpace(textValue(raw, ""))
	}
	return ""
}

func resolveGoogleIR(ir workflow.IRNode, item workflow.Item, input workflow.NodeInput, request engine.Request, index int) (workflow.IRNode, error) {
	ctx := expressionContext(item, input, request, index)
	resolved, err := expression.Resolve(ir.Parameters, ctx)
	if err != nil {
		return ir, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	walked, err := evaluateTemplateStrings(resolved, ctx)
	if err != nil {
		return ir, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	parameters, _ := walked.(map[string]any)
	if parameters == nil {
		parameters = resolved
	}
	ir.Parameters = parameters
	return ir, nil
}

func evaluateTemplateStrings(value any, ctx expression.Context) (any, error) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if !strings.Contains(trimmed, "{{") {
			return typed, nil
		}
		return expression.Evaluate(strings.TrimPrefix(trimmed, "="), ctx)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			evaluated, err := evaluateTemplateStrings(nested, ctx)
			if err != nil {
				return nil, err
			}
			out[key] = evaluated
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			evaluated, err := evaluateTemplateStrings(nested, ctx)
			if err != nil {
				return nil, err
			}
			out[index] = evaluated
		}
		return out, nil
	default:
		return value, nil
	}
}

func escapeDriveQueryValue(value string) string {
	return strings.ReplaceAll(value, `'`, `\'`)
}

func (google *GoogleClient) do(ctx context.Context, request engine.Request, ir workflow.IRNode, credentialType, method, rawURL string, query url.Values, body any) ([]byte, *http.Response, error) {
	if google == nil || google.client == nil {
		return nil, nil, fmt.Errorf("node %q: google http client is not configured", ir.Name)
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if query != nil {
		existing := target.Query()
		for key, values := range query {
			for _, value := range values {
				existing.Add(key, value)
			}
		}
		target.RawQuery = existing.Encode()
	}
	if err := google.policy.CheckURL(target); err != nil {
		return nil, nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	var payload io.Reader
	var contentType string
	switch typed := body.(type) {
	case nil:
	case []byte:
		payload = bytes.NewReader(typed)
		contentType = "application/octet-stream"
	default:
		encoded, marshalErr := json.Marshal(typed)
		if marshalErr != nil {
			return nil, nil, fmt.Errorf("node %q: %w", ir.Name, marshalErr)
		}
		payload = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, target.String(), payload)
	if err != nil {
		return nil, nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if contentType != "" {
		httpRequest.Header.Set("Content-Type", contentType)
	}
	if err := request.AuthenticateAs(ctx, ir, credentialType, httpRequest); err != nil {
		return nil, nil, err
	}
	response, err := google.client.Do(httpRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("node %q: %w", ir.Name, safehttp.RedactError(err))
	}
	contents, truncated, err := google.policy.ReadBody(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("node %q: read google response: %w", ir.Name, err)
	}
	if truncated {
		return nil, response, fmt.Errorf("node %q: google response exceeds the configured size limit", ir.Name)
	}
	if response.StatusCode >= 400 {
		return contents, response, fmt.Errorf("node %q: google api status %d: %s", ir.Name, response.StatusCode, strings.TrimSpace(string(contents)))
	}
	return contents, response, nil
}

func (google *GoogleClient) json(ctx context.Context, request engine.Request, ir workflow.IRNode, credentialType, method, rawURL string, query url.Values, body any, dest any) error {
	contents, _, err := google.do(ctx, request, ir, credentialType, method, rawURL, query, body)
	if err != nil {
		return err
	}
	if dest == nil || len(bytes.TrimSpace(contents)) == 0 {
		return nil
	}
	if err := json.Unmarshal(contents, dest); err != nil {
		return fmt.Errorf("node %q: decode google json: %w", ir.Name, err)
	}
	return nil
}

func stringList(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value := strings.TrimSpace(textValue(item, "")); value != "" {
				out = append(out, value)
			}
		}
		return out
	case string:
		if value := strings.TrimSpace(typed); value != "" {
			return []string{value}
		}
	}
	return nil
}
