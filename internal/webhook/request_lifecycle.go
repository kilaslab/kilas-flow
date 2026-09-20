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

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
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
	fields, credential, err := lifecycleFields(ctx, descriptor.CredentialType, lifecycleContext)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(descriptor.Method)
	if method == "" {
		method = http.MethodPost
	}
	headers := make(map[string]string, len(descriptor.Headers))
	for key, value := range descriptor.Headers {
		headers[key] = substitute(value, fields)
	}
	return call(ctx, lifecycleContext, credential, method, substitute(descriptor.URL, fields), headers, substitute(descriptor.Body, fields))
}

// lifecycleFields is what a lifecycle request templates with: the delivery URL,
// the route, the node's own parameters, and the fields of the credential the
// request authenticates with.
//
// The node's own parameters are namespaced so they cannot shadow a credential
// field or the route. A trigger that registers itself needs them — WAHA's
// registration call names the session the node is configured for.
func lifecycleFields(ctx context.Context, credentialType string, lifecycleContext LifecycleContext) (map[string]string, engine.Credential, error) {
	fields := map[string]string{"PublicURL": lifecycleContext.PublicURL, "Route": lifecycleContext.Binding.Route}
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
	var credential engine.Credential
	if credentialType == "" || lifecycleContext.Credentials == nil {
		return fields, credential, nil
	}
	bound, _ := lifecycleContext.Binding.Parameters["$credentials"].(map[string]any)
	id, _ := bound[credentialType].(string)
	if id == "" {
		return nil, credential, fmt.Errorf("this trigger needs a %s credential to register itself", credentialType)
	}
	resolved, err := lifecycleContext.Credentials.ResolveCredential(ctx, id)
	if err != nil {
		return nil, credential, fmt.Errorf("resolve %s credential: %w", credentialType, err)
	}
	for key, value := range resolved.Fields {
		fields[key] = value
	}
	return fields, resolved, nil
}

// call performs one lifecycle request.
//
// Through safehttp, exactly as an executor does: a lifecycle hook is an
// outbound call from this server and gets the same egress policy. The
// credential's own authentication is applied on top of the templates.
//
// Both forms are needed and neither replaces the other. Telegram's setWebhook
// wants the token *in the URL*, which only a template can do; WAHA's wants an
// X-Api-Key header, which the credential type already knows how to place — and
// a descriptor that had to name the header itself would be a second place to
// get it wrong, with the secret written into a template. A type that declares
// no authentication is templated only, which is not an error.
func call(ctx context.Context, lifecycleContext LifecycleContext, credential engine.Credential, method, target string, headers map[string]string, body string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewBufferString(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if body != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
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

// WebhookListLifecycle registers one route into a list a service already holds,
// instead of replacing the whole document.
//
// The descriptor form above cannot express this. A `set` descriptor sends one
// fixed body, and WAHA documents `PUT /api/sessions/{session}` as replacing a
// session's entire configuration and restarting it: a body carrying a single
// webhook deletes the customer's other webhooks, their proxy and their engine
// settings, and restarts their live WhatsApp session. A second workflow on the
// same session then evicts the first, and the evicted route keeps receiving
// retries it can only answer with a 404.
//
// So this form reads the document, changes only the list, and writes the
// document back. Everything it does not understand survives verbatim: the
// answer is decoded as map[string]any and re-encoded, never rebuilt from a
// typed shape, because the fields this code has never heard of are exactly the
// ones a rewrite destroys.
type WebhookListLifecycle struct {
	// Session is the document's path relative to the credential's BaseURLField,
	// templated like a descriptor URL — e.g.
	// "/api/sessions/{{ .Parameter.session }}".
	Session string `json:"session"`
	// BaseURLField names the credential field holding the service's base URL.
	BaseURLField string `json:"baseUrlField"`
	// CredentialType is the credential whose fields template the requests.
	CredentialType string `json:"credentialType"`
	// ListPath is the dotted path in the GET response holding the existing
	// entries — e.g. "config.webhooks".
	ListPath string `json:"listPath"`
	// URLField names the entry field holding the delivery URL. It is what
	// identifies this route's entries in a document this code did not write.
	URLField string `json:"urlField"`
	// Entry is this route's entry, templated the same way a descriptor is, with
	// "{{ .PublicURL }}", "{{ .Parameter.x }}" and credential fields allowed.
	//
	// An entry value that is nothing but a placeholder with no field behind it
	// removes its key instead of being sent literally, and an object left empty
	// by that removal goes too. That is what makes a node with no HMAC secret
	// send no hmac at all rather than an hmac signing with the text of a
	// template — which is not the same request, and would be a delivery this
	// server refuses.
	Entry map[string]any `json:"entry"`
}

var _ TriggerLifecycle = WebhookListLifecycle{}

// mergeTarget is one read of the document, plus everything writing it back
// needs: the substituted fields, the credential, and the address.
type mergeTarget struct {
	fields     map[string]string
	credential engine.Credential
	target     string
}

// CheckExists reports whether the document already lists this route.
//
// A service that holds a list of webhooks is asked, not assumed: without this,
// every activation re-writes the document, and writing WAHA's document restarts
// the customer's session.
func (lifecycle WebhookListLifecycle) CheckExists(ctx context.Context, lifecycleContext LifecycleContext) (bool, error) {
	document, _, err := lifecycle.open(ctx, lifecycleContext)
	if err != nil {
		return false, err
	}
	entries, err := entriesAt(document, lifecycle.ListPath)
	if err != nil {
		return false, err
	}
	return lifecycle.matches(entries, lifecycleContext.Binding.Route), nil
}

// Create merges this route's entry into the list and writes the document back.
func (lifecycle WebhookListLifecycle) Create(ctx context.Context, lifecycleContext LifecycleContext) error {
	document, session, err := lifecycle.open(ctx, lifecycleContext)
	if err != nil {
		return err
	}
	entry := renderEntry(lifecycle.Entry, session.fields)
	if len(entry) == 0 {
		return fmt.Errorf("the webhook entry for this route is empty: %s is not configured", lifecycle.CredentialType)
	}
	entries, err := entriesAt(document, lifecycle.ListPath)
	if err != nil {
		return err
	}
	// Dropped before appended, so registering the same route twice leaves one
	// entry rather than two: two entries mean two deliveries per event, and
	// WAHA retries each of them.
	entries = append(dropRoute(entries, lifecycle.URLField, lifecycleContext.Binding.Route), entry)
	if err := setEntries(document, lifecycle.ListPath, entries); err != nil {
		return err
	}
	return lifecycle.write(ctx, lifecycleContext, session, document)
}

// Delete removes this route's entries and writes the document back.
//
// Only this route's entries: a deactivation that sent a whole new configuration
// would unregister every other workflow sharing the session, which is the same
// bug as Create's, on the way out.
func (lifecycle WebhookListLifecycle) Delete(ctx context.Context, lifecycleContext LifecycleContext) error {
	document, session, err := lifecycle.open(ctx, lifecycleContext)
	if err != nil {
		return err
	}
	entries, err := entriesAt(document, lifecycle.ListPath)
	if err != nil {
		return err
	}
	kept := dropRoute(entries, lifecycle.URLField, lifecycleContext.Binding.Route)
	if len(kept) == len(entries) {
		// This route is not in the document. Writing it back unchanged would
		// restart the customer's session to change nothing.
		return nil
	}
	if err := setEntries(document, lifecycle.ListPath, kept); err != nil {
		return err
	}
	return lifecycle.write(ctx, lifecycleContext, session, document)
}

// open reads the current document.
func (lifecycle WebhookListLifecycle) open(ctx context.Context, lifecycleContext LifecycleContext) (map[string]any, mergeTarget, error) {
	fields, credential, err := lifecycleFields(ctx, lifecycle.CredentialType, lifecycleContext)
	if err != nil {
		return nil, mergeTarget{}, err
	}
	base := strings.TrimRight(fields[lifecycle.BaseURLField], "/")
	if base == "" {
		return nil, mergeTarget{}, fmt.Errorf("this trigger needs a %s credential with a %s to register itself",
			lifecycle.CredentialType, lifecycle.BaseURLField)
	}
	path := substitute(lifecycle.Session, fields)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	session := mergeTarget{fields: fields, credential: credential, target: base + path}
	body, err := call(ctx, lifecycleContext, credential, http.MethodGet, session.target, nil, "")
	if err != nil {
		return nil, session, err
	}
	document, err := decodeDocument(body)
	if err != nil {
		return nil, session, err
	}
	return document, session, nil
}

// write replaces the document with the merged one.
func (lifecycle WebhookListLifecycle) write(ctx context.Context, lifecycleContext LifecycleContext, session mergeTarget, document map[string]any) error {
	body, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode the session document: %w", err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	_, err = call(ctx, lifecycleContext, session.credential, http.MethodPut, session.target, headers, string(body))
	return err
}

// matches reports whether any entry is this route's.
func (lifecycle WebhookListLifecycle) matches(entries []any, route string) bool {
	for _, entry := range entries {
		if isRouteEntry(entry, lifecycle.URLField, route) {
			return true
		}
	}
	return false
}

// isRouteEntry reports whether an entry delivers to this route.
//
// The URL field containing the route is the test, because the document was
// written by somebody else and may carry fields this code has never heard of.
// Anything that is not an object with a string URL is not this route's entry —
// an unknown entry is somebody's configuration and is never dropped.
func isRouteEntry(entry any, urlField, route string) bool {
	if route == "" {
		return false
	}
	object, isObject := entry.(map[string]any)
	if !isObject {
		return false
	}
	url, isString := object[urlField].(string)
	return isString && strings.Contains(url, route)
}

// dropRoute returns the entries that are not this route's.
func dropRoute(entries []any, urlField, route string) []any {
	kept := make([]any, 0, len(entries))
	for _, entry := range entries {
		if !isRouteEntry(entry, urlField, route) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// entriesAt walks the dotted path to the list.
//
// A path that is not there is an empty list, because a session nobody has
// configured has no webhooks to preserve. A value at that path that is not a
// list is refused rather than replaced: whatever it is, it is not this code's
// to throw away.
func entriesAt(document map[string]any, path string) ([]any, error) {
	segments := strings.Split(path, ".")
	current := document
	for _, segment := range segments[:len(segments)-1] {
		nested, present := current[segment]
		if !present || nested == nil {
			return nil, nil
		}
		object, isObject := nested.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("the service's %s is not an object, so its webhooks cannot be read", segment)
		}
		current = object
	}
	value, present := current[segments[len(segments)-1]]
	if !present || value == nil {
		return nil, nil
	}
	entries, isList := value.([]any)
	if !isList {
		return nil, fmt.Errorf("the service's %s is not a list, so it is not replaced", path)
	}
	return entries, nil
}

// setEntries replaces the list at the dotted path, creating the objects along
// it. The rest of the document is untouched.
func setEntries(document map[string]any, path string, entries []any) error {
	segments := strings.Split(path, ".")
	current := document
	for _, segment := range segments[:len(segments)-1] {
		nested, present := current[segment]
		if !present || nested == nil {
			created := map[string]any{}
			current[segment] = created
			current = created
			continue
		}
		object, isObject := nested.(map[string]any)
		if !isObject {
			return fmt.Errorf("refusing to overwrite the service's %s: it is not an object", segment)
		}
		current = object
	}
	last := segments[len(segments)-1]
	if existing, present := current[last]; present && existing != nil {
		if _, isList := existing.([]any); !isList {
			return fmt.Errorf("refusing to overwrite the service's %s: it is not a list", path)
		}
	}
	current[last] = entries
	return nil
}

// decodeDocument reads the document a GET answered with.
//
// An empty answer is an empty document — a service with nothing configured says
// so by saying nothing — and anything that is not an object is refused, because
// a PUT of this code's document over it would delete something that was not a
// session at all.
func decodeDocument(body []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("the service did not answer with a document to merge into: %w", err)
	}
	if document == nil {
		return map[string]any{}, nil
	}
	return document, nil
}

// renderEntry substitutes the fields through the entry template.
//
// Bodies as well as strings, because an entry is an object: `events` is a list
// and WAHA's `hmac` is a nested object, and either may be absent.
func renderEntry(entry map[string]any, fields map[string]string) map[string]any {
	rendered, _ := renderValue(entry, fields)
	object, _ := rendered.(map[string]any)
	return object
}

// renderValue substitutes one templated value. The second result is false when
// the value is to be left out entirely.
func renderValue(value any, fields map[string]string) (any, bool) {
	switch typed := value.(type) {
	case string:
		if placeholder := wholePlaceholder(typed); placeholder != "" {
			substituted, present := fields[placeholder]
			// Absent or empty: a field with nothing behind it is not sent as
			// the text of its own template, and is not sent as "" either.
			return substituted, present && substituted != ""
		}
		return substitute(typed, fields), true
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if rendered, present := renderValue(nested, fields); present {
				out[key] = rendered
			}
		}
		return out, len(out) > 0
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			if rendered, present := renderValue(nested, fields); present {
				out = append(out, rendered)
			}
		}
		return out, true
	default:
		return value, true
	}
}

// wholePlaceholder returns the field name when the string is a single
// placeholder and nothing else, and "" otherwise.
//
// Only the whole string, so a placeholder inside a longer value keeps
// substitute's behaviour of staying visible as the mistake it is.
func wholePlaceholder(value string) string {
	match := reference.FindStringSubmatch(value)
	if match == nil || match[0] != value {
		return ""
	}
	return match[1]
}
