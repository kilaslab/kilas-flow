package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
	// bot token is substituted rather than stored in the descriptor. A node
	// parameter is escaped for where it lands in the URL; see substituteURL.
	URL string `json:"url"`
	// Headers are templated too, and a value with a line break is refused.
	Headers map[string]string `json:"headers,omitempty"`
	// Body is templated like the URL. A body that starts with `{` or `[` is
	// JSON, and values are written into it as JSON; see substituteBody.
	Body string `json:"body,omitempty"`
	// CredentialType names the credential whose fields the templates may read.
	CredentialType string `json:"credentialType,omitempty"`
	// SuccessJSONPath, when set, is a dotted path into the JSON response that
	// must hold this route's URL for a CheckExists call to report the webhook
	// already registered.
	SuccessJSONPath string `json:"successJsonPath,omitempty"`
	// Capture keeps values from the JSON response, by key: each path is where
	// the response holds one, dotted, such as `data.id`. It is how a service
	// that names the subscription it made, or generates the secret it will sign
	// with, is heard: it says so once, in the answer to the registration. The
	// values are kept on the route, sealed, and `check` and `remove` templates
	// read them as {{ .Captured.<key> }}. Only `set` may declare it, and a
	// response without one of its paths fails the registration.
	Capture map[string]string `json:"capture,omitempty"`
}

// RequestLifecycle implements TriggerLifecycle from descriptors alone.
type RequestLifecycle struct {
	Check  *RequestDescriptor
	Set    *RequestDescriptor
	Remove *RequestDescriptor
}

var _ TriggerLifecycle = RequestLifecycle{}

// capturedFamily is the field family a kept value is templated under.
const capturedFamily = "Captured"

// captureKey is what a captured value may be called: a name a placeholder can
// spell, so every key a pack declares is one its templates can read.
var captureKey = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Validate refuses a lifecycle that declares what it can never do.
//
// Only `set` captures. `check` and `remove` are addressed by what `set` kept,
// and a value they captured would replace the id they were addressed by with
// whatever they happened to answer. A key has to be one a template can name,
// and a path has to name something, or the value is kept for nothing.
//
// And every captured value a template reads has to be one `set` keeps. A
// `check` or `remove` naming anything else is never sent, since it would be
// sent with the placeholder's own text in it; and `set` runs before anything
// is captured, so a value it names is never there.
func (lifecycle RequestLifecycle) Validate() error {
	var captured map[string]string
	if lifecycle.Set != nil {
		captured = lifecycle.Set.Capture
		if keys := capturedReferences(lifecycle.Set); len(keys) > 0 {
			return fmt.Errorf("lifecycle set reads %s.%s, and set runs before anything is captured: it keeps what its own answer holds with capture",
				capturedFamily, keys[0])
		}
	}
	for _, other := range []struct {
		name       string
		descriptor *RequestDescriptor
	}{{"check", lifecycle.Check}, {"remove", lifecycle.Remove}} {
		if other.descriptor == nil {
			continue
		}
		if len(other.descriptor.Capture) > 0 {
			return fmt.Errorf("lifecycle %s declares capture, which only set may: %s reads what set kept as {{ .Captured.<key> }}",
				other.name, other.name)
		}
		for _, key := range capturedReferences(other.descriptor) {
			if _, kept := captured[key]; !kept {
				return fmt.Errorf("lifecycle %s reads %s.%s, which set does not capture", other.name, capturedFamily, key)
			}
		}
	}
	if lifecycle.Set == nil {
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(lifecycle.Set.Capture)) {
		path := lifecycle.Set.Capture[key]
		if !captureKey.MatchString(key) {
			return fmt.Errorf("lifecycle set captures a value as %q: a key is letters, digits and underscores, so a template can name it", key)
		}
		if strings.TrimSpace(path) == "" || slices.Contains(strings.Split(path, "."), "") {
			return fmt.Errorf("lifecycle set captures %s from %q, which names no value: a path is dotted keys, such as data.id", key, path)
		}
	}
	return nil
}

// CheckExists reports whether the remote service already points at this route.
// With no descriptor it reports false, so Create runs — re-registering an
// identical webhook is harmless, while skipping registration is not.
func (lifecycle RequestLifecycle) CheckExists(ctx context.Context, lifecycleContext LifecycleContext) (bool, error) {
	if lifecycle.Check == nil {
		return false, nil
	}
	captured, err := loadCaptured(ctx, lifecycleContext)
	if err != nil {
		return false, err
	}
	if needsUncaptured(lifecycle.Check, captured) {
		// Addressed by a value no registration kept: there is no registration
		// of this route's to find. It is not registered, so Create runs.
		return false, nil
	}
	body, err := lifecycle.send(ctx, lifecycle.Check, lifecycleContext, captured)
	if err != nil {
		return false, err
	}
	if lifecycle.Check.SuccessJSONPath == "" {
		return true, nil
	}
	document, err := decodeAnswer(body)
	if err != nil {
		return false, nil
	}
	value, present := jsonPathValue(document, lifecycle.Check.SuccessJSONPath)
	if !present {
		return false, nil
	}
	// The registered URL must be *this* route. A bot already pointing
	// somewhere else is not registered for this workflow, and treating it as
	// though it were would leave the workflow silently unreachable.
	return strings.Contains(fmt.Sprint(value), lifecycleContext.Binding.Route), nil
}

// Create registers the webhook, and keeps what the answer was asked to yield.
//
// A trigger that captures is refused before anything is sent when there is
// nowhere to keep the values: the registration it would make is one whose id
// nobody has, which nothing can ever remove.
func (lifecycle RequestLifecycle) Create(ctx context.Context, lifecycleContext LifecycleContext) error {
	if lifecycle.Set == nil {
		return nil
	}
	capture := lifecycle.Set.Capture
	if len(capture) > 0 && lifecycleContext.State == nil {
		return fmt.Errorf("this trigger keeps values from its service's answer, and this server has nowhere to keep them: lifecycle state is sealed with the credential encryption key, which is not configured")
	}
	body, err := lifecycle.send(ctx, lifecycle.Set, lifecycleContext, nil)
	if err != nil {
		return err
	}
	if len(capture) == 0 {
		return nil
	}
	captured, err := captureFrom(body, capture)
	if err == nil {
		if saveErr := lifecycleContext.State.Save(ctx, captured); saveErr != nil {
			err = fmt.Errorf("its values could not be kept: %w", saveErr)
		}
	}
	if err != nil {
		return lifecycle.unkept(ctx, lifecycleContext, captured, err)
	}
	return nil
}

// unkept answers for a registration the service accepted and this server
// could not keep what it answered with.
//
// It is not "could not register": the service now holds a registration that
// delivers to this route. It is removed again when what was captured is enough
// to address the remove, and otherwise the error and the log say it may need
// removing by hand. The log names the tenant, workflow, node and route, never
// a value: what was captured may be the secret.
func (lifecycle RequestLifecycle) unkept(ctx context.Context, lifecycleContext LifecycleContext, captured map[string]string, problem error) error {
	removed := false
	if lifecycle.Remove != nil && !needsUncaptured(lifecycle.Remove, captured) {
		_, removeErr := lifecycle.send(ctx, lifecycle.Remove, lifecycleContext, captured)
		removed = removeErr == nil
	}
	if logger := lifecycleContext.Logger; logger != nil {
		logger.Warn("a trigger's service accepted its registration, and what it answered with could not be kept",
			"tenant", lifecycleContext.TenantID, "workflow", lifecycleContext.WorkflowID,
			"node", lifecycleContext.Binding.NodeID, "route", lifecycleContext.Binding.Route,
			"removed", removed, "error", problem)
	}
	if removed {
		return fmt.Errorf("the service accepted the registration, but %w, so it was removed again", problem)
	}
	return fmt.Errorf("the service accepted the registration, but %w; it may need removing by hand", problem)
}

// Delete unregisters it, and forgets what its registration kept once it is
// gone.
func (lifecycle RequestLifecycle) Delete(ctx context.Context, lifecycleContext LifecycleContext) error {
	if lifecycle.Remove == nil {
		return nil
	}
	captured, err := loadCaptured(ctx, lifecycleContext)
	if err != nil {
		return err
	}
	if needsUncaptured(lifecycle.Remove, captured) {
		// Addressed by a value no registration kept: nothing this server
		// registered is there to remove.
		return nil
	}
	if _, err := lifecycle.send(ctx, lifecycle.Remove, lifecycleContext, captured); err != nil {
		// Kept on a failure, so the next deactivation can still address it.
		return err
	}
	if len(captured) == 0 {
		return nil
	}
	// Kept past the removal, they would address the next check at a
	// registration that no longer exists.
	return lifecycleContext.State.Clear(ctx)
}

// loadCaptured reads what this route's registration kept. A context with no
// state has kept nothing.
func loadCaptured(ctx context.Context, lifecycleContext LifecycleContext) (map[string]string, error) {
	if lifecycleContext.State == nil {
		return nil, nil
	}
	captured, err := lifecycleContext.State.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("read what this trigger's registration kept: %w", err)
	}
	return captured, nil
}

// needsUncaptured reports whether a descriptor's templates name a captured
// value that was not kept.
//
// Sent anyway, the placeholder would stay in the request as its own text — a
// DELETE of a subscription literally named `{{ .Captured.id }}`.
func needsUncaptured(descriptor *RequestDescriptor, captured map[string]string) bool {
	for _, key := range capturedReferences(descriptor) {
		if _, kept := captured[key]; !kept {
			return true
		}
	}
	return false
}

// capturedReferences lists the captured keys a descriptor's templates read, in
// the order they are written: the URL, the headers by name, then the body.
func capturedReferences(descriptor *RequestDescriptor) []string {
	templates := []string{descriptor.URL}
	for _, name := range slices.Sorted(maps.Keys(descriptor.Headers)) {
		templates = append(templates, descriptor.Headers[name])
	}
	templates = append(templates, descriptor.Body)
	var keys []string
	for _, template := range templates {
		for _, match := range reference.FindAllStringSubmatch(template, -1) {
			if key, isCaptured := strings.CutPrefix(match[1], capturedFamily+"."); isCaptured {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// captureFrom reads each captured value out of a response.
//
// All or nothing: an answer missing one is refused whole, because the pack
// declared it for a reason — a `remove` addressed by an id that was never kept
// can never run. What could be read is still returned beside the error, which
// is what lets a registration nobody will keep be removed again. A value is
// non-empty text, a number or a boolean. Empty text is missing: an empty id
// written into `/subscriptions/{{ .Captured.id }}` addresses the whole
// collection. A number keeps the digits it was sent with, since an id read as
// a float comes back rounded and names another subscription. The answer itself
// is never quoted in an error: it may carry the very secret being captured.
func captureFrom(body []byte, capture map[string]string) (map[string]string, error) {
	document, decodeErr := decodeAnswer(body)
	captured := make(map[string]string, len(capture))
	var failure error
	fail := func(err error) {
		if failure == nil {
			failure = err
		}
	}
	for _, key := range slices.Sorted(maps.Keys(capture)) {
		path := capture[key]
		if decodeErr != nil {
			return captured, fmt.Errorf("its answer is not JSON, so it has no %s to keep as %s", path, key)
		}
		value, present := jsonPathValue(document, path)
		text, scalar := scalarText(value)
		switch {
		case !present || value == nil || (scalar && strings.TrimSpace(text) == ""):
			fail(fmt.Errorf("its answer has no %s to keep as %s", path, key))
		case !scalar:
			fail(fmt.Errorf("its answer holds an object or a list at %s, not a value to keep as %s", path, key))
		default:
			captured[key] = text
		}
	}
	return captured, failure
}

// decodeAnswer decodes a JSON response, keeping every number as its text.
func decodeAnswer(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return document, nil
}

// jsonPathValue walks a dotted path through a response's objects.
//
// The one reader for both paths a descriptor declares, the success path a
// check reads and each value a set captures, so the two cannot disagree about
// what `data.id` names.
func jsonPathValue(document any, path string) (any, bool) {
	current := document
	for _, segment := range strings.Split(path, ".") {
		object, isObject := current.(map[string]any)
		if !isObject {
			return nil, false
		}
		value, present := object[segment]
		if !present {
			return nil, false
		}
		current = value
	}
	return current, true
}

// scalarText is a captured value as the text a template writes.
func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

// send renders one descriptor and performs it. The values a registration kept
// are there as Captured.<key>.
func (lifecycle RequestLifecycle) send(ctx context.Context, descriptor *RequestDescriptor, lifecycleContext LifecycleContext, captured map[string]string) ([]byte, error) {
	fields, credential, err := lifecycleFields(ctx, descriptor.CredentialType, lifecycleContext)
	if err != nil {
		return nil, err
	}
	for key, value := range captured {
		fields[capturedFamily+"."+key] = value
	}
	method := strings.ToUpper(descriptor.Method)
	if method == "" {
		method = http.MethodPost
	}
	// Each part is rendered for where its values land, and a part that cannot
	// be rendered safely stops the request before anything is sent: the request
	// carries the tenant's credential, and a value that rewrote it would carry
	// that credential somewhere the pack never named.
	headers := make(map[string]string, len(descriptor.Headers))
	for key, value := range descriptor.Headers {
		rendered, err := substituteHeader(key, value, fields)
		if err != nil {
			return nil, err
		}
		headers[key] = rendered
	}
	target, err := substituteURL(descriptor.URL, fields)
	if err != nil {
		return nil, err
	}
	body, err := substituteBody(descriptor.Body, fields)
	if err != nil {
		return nil, err
	}
	return call(ctx, lifecycleContext, credential, method, target, headers, body)
}

// lifecycleFields is what a lifecycle request templates with: the delivery URL,
// the route, the node's own parameters, and the fields of the credential the
// request authenticates with.
//
// The node's own parameters are namespaced so they cannot shadow a credential
// field or the route. A trigger that registers itself needs them — WAHA's
// registration call names the session the node is configured for.
//
// Each parameter is there twice. Parameter.<key> is a scalar as text, for a URL
// segment or the inside of a JSON string. ParameterJSON.<key> is every
// parameter encoded as JSON, lists and objects included, because a multi-select
// of events or a collection of conditions has no text form a service would
// read: `"events": {{ .ParameterJSON.events }}` is how a template sends one.
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
		if encoded, err := encodeJSON(value); err == nil {
			fields["ParameterJSON."+key] = encoded
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
		// A lifecycle URL may carry the credential — a bot token in the path —
		// and this error becomes activation's 502 detail and deactivation's
		// Warn line: only the scheme and host go on.
		return nil, safehttp.RedactError(err)
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

// dataFamilies are the field families whose values are data rather than
// addresses: the node's parameters, as text and as JSON, and the values a
// registration's answer was captured for. A tenant wrote the first and a remote
// service the second, so wherever one lands in a URL it is escaped to stay in
// its place.
//
// Families rather than one prefix, so that another kind of data is escaped by
// being listed here, not by every place that writes a URL learning a new
// prefix. PublicURL, Route and the credential's fields are in no family: they
// are what the request is built on, and a credential's base URL is an address
// by design, which escaping would break.
var dataFamilies = map[string]bool{"Parameter": true, "ParameterJSON": true, capturedFamily: true}

// isData reports whether a field's value is data, by its family: the name
// before the first dot.
func isData(key string) bool {
	family, _, namespaced := strings.Cut(key, ".")
	return namespaced && dataFamilies[family]
}

// expand is the one substitution pass every context shares.
//
// Each placeholder with a field behind it is replaced by what place writes for
// it, and place is given everything rendered before it, which is what tells it
// where the value lands. An unresolved placeholder stays as it is rather than
// becoming an empty string: a URL with a visible `{{ .session }}` in it is a
// mistake somebody can see.
//
// Deliberately not the expression evaluator: a descriptor is configuration
// written by a pack author, not a user expression, and giving it the full
// grammar would let a pack read run-time data at activation time.
//
// One pass, so a substituted value is never rescanned. Replacing key by key
// would expand a placeholder that happened to appear *inside* a credential —
// which is to say, a bot token containing the right seven characters could pull
// another field of the same credential into the request.
func expand(template string, fields map[string]string, place func(rendered, key, value string) (string, error)) (string, error) {
	var out strings.Builder
	last := 0
	for _, match := range reference.FindAllStringSubmatchIndex(template, -1) {
		out.WriteString(template[last:match[0]])
		last = match[1]
		key := template[match[2]:match[3]]
		value, present := fields[key]
		if !present {
			out.WriteString(template[match[0]:match[1]])
			continue
		}
		placed, err := place(out.String(), key, value)
		if err != nil {
			return "", err
		}
		out.WriteString(placed)
	}
	out.WriteString(template[last:])
	return out.String(), nil
}

// substitute replaces {{ .Field }} references with the values as they are.
//
// For a place that escapes what it is given itself — an entry the merge encodes
// as JSON — and for text whose grammar this code does not know: a header, once
// substituteHeader has refused its line breaks, and a body that is not JSON.
func substitute(template string, fields map[string]string) string {
	rendered, _ := expand(template, fields, func(_, _, value string) (string, error) {
		return value, nil
	})
	return rendered
}

// pathStarted matches a URL that has reached its path: a scheme, an authority
// and the slash that ends it, or a path that starts with a single slash.
var pathStarted = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9+.-]*://[^/]*/|/(?:[^/]|$))`)

// substituteURL renders a URL template.
//
// A data field is escaped for where it lands: as a path segment, or as a query
// value once the URL has a `?`. Everything else is written as it is, because it
// is the address being built on.
//
// Two places are refused rather than escaped. Before the path begins, a value
// would be part of the scheme or the authority, and no escaping keeps it out
// of the host: `@` and `:` are legal in a path segment, and `@evil.example`
// written after a host turns that host into a user name. And a segment of dots
// is refused, since `..` escaped is still `..`, and a proxy in front of the
// service resolves it to the parent path.
func substituteURL(template string, fields map[string]string) (string, error) {
	return expand(template, fields, func(rendered, key, value string) (string, error) {
		switch {
		case !isData(key):
			return value, nil
		case strings.Contains(rendered, "?"):
			return url.QueryEscape(value), nil
		case !pathStarted.MatchString(rendered):
			return "", fmt.Errorf("%s stands in the URL before its path begins, where it could change the host the request goes to", key)
		case value == "." || value == "..":
			return "", fmt.Errorf("%s is %q, which in a URL path would call another endpoint", key, value)
		default:
			return safehttp.PathSegment(value), nil
		}
	})
}

// substituteHeader renders one header value, and refuses a line break in it.
//
// A CR or LF would end the header and start another of the value's choosing.
// Go's transport refuses such a request too, but only once it has been built
// with the credential applied, and without saying which value was at fault.
func substituteHeader(name, template string, fields map[string]string) (string, error) {
	value := substitute(template, fields)
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("the %s header would contain a line break, so the request is not sent", name)
	}
	return value, nil
}

// substituteBody renders a request body.
//
// A body whose template starts with `{` or `[` is JSON, and is rendered as
// JSON. A placeholder inside a string has its value escaped to stay inside that
// string. A placeholder outside one — `"events": {{ .ParameterJSON.events }}` —
// is written as it is, and must be exactly one JSON value, so that it cannot
// add keys or elements beside itself. What results must be valid JSON, or
// nothing is sent. Any other body is substituted as it is, because this code
// knows no grammar for it to escape into.
func substituteBody(template string, fields map[string]string) (string, error) {
	trimmed := strings.TrimSpace(template)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return substitute(template, fields), nil
	}
	rendered, err := expand(template, fields, func(rendered, key, value string) (string, error) {
		inString, escaped := jsonPosition(rendered)
		switch {
		case escaped:
			return "", fmt.Errorf("the body template puts a backslash before %s, which would let its value end the string", key)
		case inString:
			return jsonText(value), nil
		case json.Valid([]byte(value)):
			return value, nil
		case strings.HasPrefix(key, "Parameter."):
			return "", fmt.Errorf("%s stands outside a string in the body and is not a JSON value: quote it, or use ParameterJSON", key)
		default:
			return "", fmt.Errorf("%s stands outside a string in the body and is not a JSON value: quote it", key)
		}
	})
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(rendered)) {
		return "", fmt.Errorf("the rendered body is not valid JSON, so the request is not sent")
	}
	return rendered, nil
}

// jsonPosition reports where the end of a JSON text stands: inside a string,
// and straight after a backslash in one.
//
// Scanning what has been rendered rather than the template is sound, because
// nothing the pass wrote can move it: a value inside a string was escaped, and
// a value outside one is a whole JSON value, whose strings close. Bytes rather
// than runes, because neither `"` nor `\` occurs inside a multi-byte character.
func jsonPosition(text string) (inString, escaped bool) {
	for index := 0; index < len(text); index++ {
		switch {
		case escaped:
			escaped = false
		case !inString:
			inString = text[index] == '"'
		case text[index] == '\\':
			escaped = true
		case text[index] == '"':
			inString = false
		}
	}
	return inString, escaped
}

// jsonText is a value escaped for the inside of a JSON string: its encoding,
// without the quotes the template already wrote.
func jsonText(value string) string {
	encoded, _ := encodeJSON(value) // a string always encodes
	return encoded[1 : len(encoded)-1]
}

// encodeJSON encodes a value as the JSON a service is sent.
//
// Without HTML escaping: the text goes to an API rather than a page, and an `&`
// in a delivery URL should arrive as an `&`.
func encodeJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
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
	// "/api/sessions/{{ .Parameter.session }}" — so the session a node names
	// stays inside its own segment.
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
	// Escaped, like any URL a lifecycle builds. Both requests to this path carry
	// the tenant's API key, and a session of `../../x?y=` written raw would take
	// the key, and a write of the session document, to another endpoint.
	//
	// The slash goes on before rendering rather than after: the template is a
	// path below the base URL, and rendered as one, a session at its start is
	// in the path rather than refused as though it stood in the host.
	template := lifecycle.Session
	if !strings.HasPrefix(template, "/") {
		template = "/" + template
	}
	path, err := substituteURL(template, fields)
	if err != nil {
		return nil, mergeTarget{}, err
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
// and WAHA's `hmac` is a nested object, and either may be absent. Values are
// substituted as they are, because the merged document is encoded as JSON
// afterwards and the encoder escapes every string it holds.
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
