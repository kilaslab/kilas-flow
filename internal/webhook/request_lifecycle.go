package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// RequestDescriptor is one HTTP call expressed as data.
//
// A setWebhook call is a single request with a templated body, so it is
// expressible without code. Making the declarative form *an implementation of*
// TriggerLifecycle rather than a special case beside it is what lets a
// generated pack register a webhook with no hand-written Go, while leaving the
// Go form available for the things a descriptor cannot express — Telegram's
// secret_token verification on every delivery, for one.
type RequestDescriptor struct {
	Method string `json:"method"`
	// URL may reference {{ .PublicURL }} and any credential field by name, so a
	// bot token is substituted rather than stored in the descriptor.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// Body is templated the same way as the URL.
	Body string `json:"body,omitempty"`
	// CredentialType names the credential whose fields the templates may read.
	CredentialType string `json:"credentialType,omitempty"`
	// SuccessJSONPath, when set, must be truthy in the response for a
	// CheckExists call to report the webhook already registered.
	SuccessJSONPath string `json:"successJsonPath,omitempty"`
}

// RequestLifecycle implements TriggerLifecycle from descriptors alone.
type RequestLifecycle struct {
	Check  *RequestDescriptor
	Set    *RequestDescriptor
	Remove *RequestDescriptor
}

var _ TriggerLifecycle = RequestLifecycle{}

// CheckExists reports whether the remote service already points at this route.
// With no descriptor it reports false, so Create runs — re-registering an
// identical webhook is harmless, while skipping registration is not.
func (lifecycle RequestLifecycle) CheckExists(ctx context.Context, lifecycleContext LifecycleContext) (bool, error) {
	if lifecycle.Check == nil {
		return false, nil
	}
	body, err := lifecycle.send(ctx, lifecycle.Check, lifecycleContext)
	if err != nil {
		return false, err
	}
	if lifecycle.Check.SuccessJSONPath == "" {
		return true, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false, nil
	}
	value, present := decoded[lifecycle.Check.SuccessJSONPath]
	if !present {
		return false, nil
	}
	// The registered URL must be *this* route. A bot already pointing
	// somewhere else is not registered for this workflow, and treating it as
	// though it were would leave the workflow silently unreachable.
	return strings.Contains(fmt.Sprint(value), lifecycleContext.Binding.Route), nil
}

// Create registers the webhook.
func (lifecycle RequestLifecycle) Create(ctx context.Context, lifecycleContext LifecycleContext) error {
	if lifecycle.Set == nil {
		return nil
	}
	_, err := lifecycle.send(ctx, lifecycle.Set, lifecycleContext)
	return err
}

// Delete unregisters it.
func (lifecycle RequestLifecycle) Delete(ctx context.Context, lifecycleContext LifecycleContext) error {
	if lifecycle.Remove == nil {
		return nil
	}
	_, err := lifecycle.send(ctx, lifecycle.Remove, lifecycleContext)
	return err
}

func (lifecycle RequestLifecycle) send(ctx context.Context, descriptor *RequestDescriptor, lifecycleContext LifecycleContext) ([]byte, error) {
	var credential engine.Credential
	fields := map[string]string{"PublicURL": lifecycleContext.PublicURL, "Route": lifecycleContext.Binding.Route}
	// The node's own parameters, namespaced so they cannot shadow a credential
	// field or the route. A trigger that registers itself needs them — WAHA's
	// registration call names the session the node is configured for.
	for key, value := range lifecycleContext.Binding.Parameters {
		switch typed := value.(type) {
		case string:
			fields["Parameter."+key] = typed
		case bool:
			fields["Parameter."+key] = strconv.FormatBool(typed)
		case float64:
			fields["Parameter."+key] = strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	if descriptor.CredentialType != "" && lifecycleContext.Credentials != nil {
		reference, _ := lifecycleContext.Binding.Parameters["$credentials"].(map[string]any)
		id, _ := reference[descriptor.CredentialType].(string)
		if id == "" {
			return nil, fmt.Errorf("this trigger needs a %s credential to register itself", descriptor.CredentialType)
		}
		resolved, err := lifecycleContext.Credentials.ResolveCredential(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolve %s credential: %w", descriptor.CredentialType, err)
		}
		credential = resolved
		for key, value := range resolved.Fields {
			fields[key] = value
		}
	}

	method := strings.ToUpper(descriptor.Method)
	if method == "" {
		method = http.MethodPost
	}
	target := substitute(descriptor.URL, fields)
	body := substitute(descriptor.Body, fields)

	// Through safehttp, exactly as an executor does: a lifecycle hook is an
	// outbound call from this server and gets the same egress policy.
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for key, value := range descriptor.Headers {
		request.Header.Set(key, substitute(value, fields))
	}
	if body != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}

	// The credential's own authentication is applied on top of the templates.
	//
	// Both forms are needed and neither replaces the other. Telegram's
	// setWebhook wants the token *in the URL*, which only a template can do;
	// WAHA's wants an X-Api-Key header, which the credential type already knows
	// how to place — and a descriptor that had to name the header itself would
	// be a second place to get it wrong, with the secret written into a
	// template. A type that declares no authentication is templated only, which
	// is not an error.
	if credential.Type != "" {
		if credentialType, known := credentials.Default().Get(credential.Type); known && credentialType.Authenticate != nil {
			if err := credentials.ApplyAuthentication(request, credentialType, credential.Fields); err != nil {
				return nil, fmt.Errorf("apply %s credential: %w", credential.Type, err)
			}
		}
	}

	response, err := safehttp.NewClient(lifecycleContext.HTTP).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, _, err := lifecycleContext.HTTP.ReadBody(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		// The remote body is deliberately not echoed: it can carry the token
		// that was just sent to it.
		return nil, fmt.Errorf("the service answered %d", response.StatusCode)
	}
	return payload, nil
}

// reference matches a `{{ .Field }}` placeholder, with or without the spaces.
var reference = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_.]+)\s*\}\}`)

// substitute replaces {{ .Field }} references.
//
// Deliberately not the expression evaluator: a descriptor is configuration
// written by a pack author, not a user expression, and giving it the full
// grammar would let a pack read run-time data at activation time.
//
// One pass, so a substituted value is never rescanned. Replacing key by key
// would expand a placeholder that happened to appear *inside* a credential —
// which is to say, a bot token containing the right seven characters could pull
// another field of the same credential into the request.
func substitute(template string, fields map[string]string) string {
	return reference.ReplaceAllStringFunc(template, func(match string) string {
		key := reference.FindStringSubmatch(match)[1]
		value, present := fields[key]
		if !present {
			// An unresolved placeholder stays as it is rather than becoming an
			// empty string: a URL with a visible `{{ .session }}` in it is a
			// mistake somebody can see.
			return match
		}
		return value
	})
}
