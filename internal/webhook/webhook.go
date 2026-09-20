package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Limits bound one inbound webhook request.
type Limits struct {
	// MaxBodyBytes caps the request body read into memory.
	MaxBodyBytes int64
	// ResponseTimeout bounds how long a request waits for a workflow that
	// answers from its own graph.
	ResponseTimeout time.Duration
	// DeliveryWindow is how long a delivery identifier is remembered, so a
	// sender's retry sequence resolves to one execution.
	DeliveryWindow time.Duration
}

// DefaultLimits are used when nothing is configured.
func DefaultLimits() Limits {
	return Limits{MaxBodyBytes: 1 << 20, ResponseTimeout: 30 * time.Second, DeliveryWindow: repository.DefaultDeliveryWindow}
}

// Runner queues an execution for a resolved binding.
type Runner interface {
	QueueWebhook(ctx context.Context, binding repository.WebhookBinding, payload json.RawMessage) (execution.Record, error)
	Get(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error)
}

// Handler serves the inbound webhook surface.
type Handler struct {
	bindings repository.WebhookRepository
	// triggers says how each trigger node type shapes and verifies a delivery.
	// Nil means every trigger gets the envelope shape, which is what they all
	// got before this existed.
	triggers    *Registry
	runner      Runner
	credentials repository.CredentialRepository
	events      *events.Broker
	limits      Limits
	logger      *slog.Logger
}

// WithLogger replaces the handler's logger. A filtered delivery is the only
// thing this writes: it is the difference between "the workflow ignored my
// message" and "the endpoint is broken", and a user has no other way to tell.
func (handler *Handler) WithLogger(logger *slog.Logger) *Handler {
	if logger != nil {
		handler.logger = logger
	}
	return handler
}

// NewHandler constructs the inbound webhook boundary.
// WithTriggers registers how each trigger node type shapes and verifies its
// deliveries. Without it every trigger keeps the envelope shape.
func (handler *Handler) WithTriggers(triggers *Registry) *Handler {
	handler.triggers = triggers
	return handler
}

func NewHandler(bindings repository.WebhookRepository, runner Runner, creds repository.CredentialRepository, broker *events.Broker, limits Limits) *Handler {
	if limits.MaxBodyBytes <= 0 {
		limits.MaxBodyBytes = DefaultLimits().MaxBodyBytes
	}
	if limits.ResponseTimeout <= 0 {
		limits.ResponseTimeout = DefaultLimits().ResponseTimeout
	}
	return &Handler{
		bindings: bindings, runner: runner, credentials: creds,
		events: broker, limits: limits, logger: slog.Default(),
	}
}

// ServeHTTP routes one inbound request to its workflow.
func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/webhook"), "/")
	if path == "" {
		problem(w, http.StatusNotFound, "No webhook path was given.")
		return
	}
	if handler.bindings == nil || handler.runner == nil {
		problem(w, http.StatusServiceUnavailable, "Webhook routing is not available.")
		return
	}

	// A browser's preflight names the route but not the method it will use, so
	// it is answered from the methods bound on that route rather than by
	// matching one. Routing by method alone answered 404, which is why no
	// browser page could post to a KilasFlow webhook at all.
	if r.Method == http.MethodOptions {
		handler.servePreflight(w, r, path)
		return
	}

	// A hosted page is answered from the route rather than from a
	// method-specific binding: the page is a GET, and a form trigger whose
	// submission method is the only one declared still has a page to serve.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if binding, page, found := handler.hostedPage(r, path); found {
			applyCORS(w, r, binding)
			writePage(w, http.StatusOK, page)
			return
		}
	}

	binding, params, err := handler.resolveBinding(r.Context(), strings.ToUpper(r.Method), path)
	if err != nil {
		// An inactive, deleted, or never-activated workflow has no binding, and
		// a wrong method has none either. Answering both the same way keeps the
		// endpoint from confirming which workflows exist.
		problem(w, http.StatusNotFound, "No active workflow is bound to this webhook.")
		return
	}
	applyCORS(w, r, binding)

	// An allow-list is checked before the credential, so a refused caller
	// cannot use the endpoint to probe for valid secrets.
	if !addressAllowed(r, binding) {
		problem(w, http.StatusForbidden, "This webhook does not accept requests from your address.")
		return
	}

	if status, err := handler.authenticate(r, binding); err != nil {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="webhook"`)
		}
		problem(w, status, err.Error())
		return
	}

	delivery, err := handler.readDelivery(r, binding, params)
	if err != nil {
		problem(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}

	// The trigger's own type decides both the shape and whether the delivery is
	// acceptable at all. A signature check belongs here rather than inside the
	// workflow: a request that fails it should never become an execution.
	kind := handler.triggers.Lookup(binding.NodeType)

	// A trigger that owns a page answers GET with it and reads POST as the
	// submission that page sent back. Neither is a workflow run of its own: the
	// page is rendered from the workflow's own declaration, and the submission
	// is the trigger item.
	if kind.Page != nil {
		page := kind.Page.Form(delivery)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			writePage(w, http.StatusOK, RenderForm(page, requestURL(r)))
			return
		default:
			submission, err := kind.Page.Submission(delivery)
			if err != nil {
				writePage(w, http.StatusBadRequest, RenderFormMessage("Submission rejected", err.Error(), page.Attribution))
				return
			}
			delivery.Body = submission
		}
	}
	if kind.Verify != nil {
		if err := kind.Verify(delivery); err != nil {
			problem(w, http.StatusUnauthorized, err.Error())
			return
		}
	}
	// Filtering is not verification, and the answer is different on purpose:
	// an update the trigger was restricted away from was received correctly and
	// deliberately not acted on, so the sender is told 200 and stops retrying.
	if kind.Accept != nil {
		if accepted, reason := kind.Accept(delivery); !accepted {
			handler.logger.Info("a delivery was filtered out by its trigger",
				"route", binding.Route, "workflow", binding.WorkflowID, "node", binding.NodeID, "reason", reason)
			writeJSON(w, http.StatusOK, map[string]any{"filtered": true, "reason": reason})
			return
		}
	}
	payload, err := json.Marshal(kind.Shape.Apply(delivery))
	if err != nil {
		problem(w, http.StatusBadRequest, "The request could not be encoded.")
		return
	}

	// A crawler that found the URL is acknowledged and not run, which is what
	// n8n's "Ignore Bots" option means: a preview fetch from a chat client must
	// not trigger the workflow's side effects.
	if ignoreBots(r, binding) {
		handler.logger.Info("a delivery was ignored as a bot",
			"route", binding.Route, "workflow", binding.WorkflowID, "node", binding.NodeID)
		writeJSON(w, http.StatusOK, map[string]any{"filtered": true, "reason": "the request looks like a bot"})
		return
	}

	// A retried delivery must not run the workflow twice. WAHA retries fifteen
	// times at two-second intervals, so a slow workflow that eventually
	// succeeds could otherwise send fifteen WhatsApp replies.
	deliveryID := deliveryIdentifier(r, binding)
	if deliveryID != "" {
		owner, claimed, err := handler.bindings.ClaimDelivery(r.Context(), binding.Route, deliveryID, "", handler.limits.DeliveryWindow)
		if err == nil && !claimed {
			handler.answerDuplicate(w, r, binding, owner)
			return
		}
	}

	record, err := handler.runner.QueueWebhook(r.Context(), binding, payload)
	if err == nil && deliveryID != "" {
		// The claim was taken before the execution existed, so that two
		// concurrent retries could not both pass it. Now that there is an
		// execution to point at, record it — a later duplicate can then be
		// answered with the original's outcome rather than a bare accept.
		_ = handler.bindings.RecordDeliveryExecution(r.Context(), binding.Route, deliveryID, record.ID)
	}
	if err != nil {
		// Logged rather than only answered: the caller gets a generic 500 on
		// purpose, and without this the cause — a workflow that fails to
		// compile, a version that was archived — is invisible to the operator.
		handler.logger.Error("a webhook delivery could not be queued",
			"route", binding.Route, "workflow", binding.WorkflowID, "node", binding.NodeID, "error", err)
		problem(w, http.StatusInternalServerError, "The workflow could not be queued.")
		return
	}

	if responseMode(binding) == modeImmediate {
		handler.respondImmediate(w, r, binding, kind)
		return
	}
	handler.awaitResponse(w, r, binding, record)
}

type mode int

const (
	modeImmediate mode = iota
	modeLastNode
	modeResponseNode
)

// hostedPage renders the page a trigger serves on this route, if it serves one.
//
// The lookup ignores the method on purpose. A form's page and its submission are
// two methods of one endpoint, and a document that named only the submission —
// an import, or an API client that did not write the default — would otherwise
// answer 404 to the browser asking for the page it was told about.
func (handler *Handler) hostedPage(r *http.Request, path string) (repository.WebhookBinding, []byte, bool) {
	if handler.bindings == nil {
		return repository.WebhookBinding{}, nil, false
	}
	candidates := []string{path}
	if route, _, found := strings.Cut(path, "/"); found {
		candidates = append(candidates, route)
	}
	for _, candidate := range candidates {
		bindings, err := handler.bindings.ResolveRoute(r.Context(), candidate)
		if err != nil {
			continue
		}
		for _, binding := range bindings {
			kind := handler.triggers.Lookup(binding.NodeType)
			if kind.Page == nil || kind.Page.Form == nil {
				continue
			}
			form := kind.Page.Form(Delivery{Request: r, Binding: binding})
			return binding, RenderForm(form, requestURL(r)), true
		}
	}
	return repository.WebhookBinding{}, nil, false
}

// resolveBinding finds the binding for one request, filling path parameters.
//
// A request may carry segments after the route — that is how n8n's
// `/webhook/<id>/user/:id` endpoints work — so an exact lookup is tried first
// and the route is then matched against the node's own path pattern.
func (handler *Handler) resolveBinding(ctx context.Context, method, path string) (repository.WebhookBinding, map[string]any, error) {
	if binding, err := handler.bindings.Resolve(ctx, method, path); err == nil {
		return binding, nil, nil
	}
	route, rest, found := strings.Cut(path, "/")
	if !found || strings.TrimSpace(rest) == "" {
		return repository.WebhookBinding{}, nil, errors.New("no binding for this path")
	}
	binding, err := handler.bindings.Resolve(ctx, method, route)
	if err != nil {
		return repository.WebhookBinding{}, nil, err
	}
	params, matched := matchPathParams(binding.Path, strings.Split(rest, "/"))
	if !matched {
		return repository.WebhookBinding{}, nil, fmt.Errorf("no binding for this path")
	}
	return binding, params, nil
}

// matchPathParams maps trailing request segments onto a route pattern.
//
// n8n serves `/webhook/<id>/user/:id`, so a node's path pattern is part of the
// URL after its identifier. KilasFlow's route is one opaque segment replacing
// both, so the pattern's *trailing* segments are what a request after the route
// supplies: `/user/:id` takes one segment, `:id/orders` takes two and matches
// `orders` literally.
func matchPathParams(pattern string, rest []string) (map[string]any, bool) {
	if len(rest) == 0 {
		return nil, false
	}
	segments := strings.Split(strings.Trim(pattern, "/"), "/")
	if len(rest) > len(segments) {
		return nil, false
	}
	tail := segments[len(segments)-len(rest):]
	params := make(map[string]any, len(rest))
	for index, segment := range tail {
		value := rest[index]
		if value == "" {
			return nil, false
		}
		if name, variable := strings.CutPrefix(segment, ":"); variable && name != "" {
			params[name] = value
			continue
		}
		if segment != value {
			return nil, false
		}
	}
	return params, true
}

// servePreflight answers a CORS preflight for a bound route.
//
// The methods advertised are the ones actually bound on the route plus OPTIONS
// itself, so a browser learns that a GET-only endpoint will refuse its POST
// instead of discovering it as a 404 with no CORS headers — which is what a
// browser reports as an opaque CORS failure.
func (handler *Handler) servePreflight(w http.ResponseWriter, r *http.Request, path string) {
	route, _, _ := strings.Cut(path, "/")
	bindings, err := handler.bindings.ResolveRoute(r.Context(), route)
	if err != nil || len(bindings) == 0 {
		problem(w, http.StatusNotFound, "No active workflow is bound to this webhook.")
		return
	}
	applyCORS(w, r, bindings[0])

	methods := make([]string, 0, len(bindings)+1)
	seen := make(map[string]bool, len(bindings)+1)
	for _, binding := range bindings {
		if seen[binding.Method] {
			continue
		}
		seen[binding.Method] = true
		methods = append(methods, binding.Method)
	}
	if !seen[http.MethodOptions] {
		methods = append(methods, http.MethodOptions)
	}
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
	if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
		w.Header().Set("Access-Control-Allow-Headers", requested)
	}
	w.Header().Set("Access-Control-Max-Age", "300")
	w.WriteHeader(http.StatusNoContent)
}

// applyCORS answers a cross-origin caller.
//
// n8n sends these headers on every webhook answer and defaults its allow-list
// to `*`, echoing the caller's own origin. KilasFlow sent none at all, so a
// chat widget, a landing-page form or an SPA backend — the ordinary
// "n8n webhook as an API" shape — failed in the browser before it started.
func applyCORS(w http.ResponseWriter, r *http.Request, binding repository.WebhookBinding) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return
	}
	allowed := allowedOrigins(binding)
	if len(allowed) == 0 || allowed["*"] || allowed[strings.ToLower(origin)] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
}

// allowedOrigins reads the trigger's own origin allow-list.
//
// An absent list is `*`, which is n8n's default. A list that does not name the
// caller's origin sets no header at all, which is what makes the browser refuse
// the response rather than the workflow silently accepting it.
func allowedOrigins(binding repository.WebhookBinding) map[string]bool {
	options, _ := binding.Parameters["options"].(map[string]any)
	raw, _ := options["allowedOrigins"].(string)
	allowed := map[string]bool{}
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
			allowed[entry] = true
		}
	}
	return allowed
}

// addressAllowed applies the trigger's IP allow-list.
//
// An n8n endpoint restricted by `ipWhitelist` used to arrive here open, which
// silently published a protected endpoint to the internet. Both single
// addresses and CIDR ranges are accepted, which is what n8n's own field takes.
//
// The check uses the connection's own peer address and deliberately does not
// trust X-Forwarded-For: a header any caller can set is not a source of truth
// for an allow-list, and honouring it would let a caller name itself an allowed
// address. A deployment behind a reverse proxy therefore sees the proxy's
// address and should enforce the list there as well; that fails closed, which
// is the right direction for an access rule.
func addressAllowed(r *http.Request, binding repository.WebhookBinding) bool {
	options, _ := binding.Parameters["options"].(map[string]any)
	raw, _ := options["ipWhitelist"].(string)
	entries := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	if len(entries) == 0 {
		return true
	}
	peer, err := netip.ParseAddr(peerAddress(r))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			if prefix.Contains(peer) {
				return true
			}
			continue
		}
		if address, err := netip.ParseAddr(entry); err == nil && address == peer {
			return true
		}
	}
	return false
}

// peerAddress is the address the connection came from, without its port.
func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// ignoreBots reports whether a delivery looks like a crawler rather than a
// caller, which is what n8n's own option of that name skips.
func ignoreBots(r *http.Request, binding repository.WebhookBinding) bool {
	options, _ := binding.Parameters["options"].(map[string]any)
	enabled, _ := options["ignoreBots"].(bool)
	if !enabled {
		return false
	}
	agent := strings.ToLower(r.Header.Get("User-Agent"))
	if agent == "" {
		return false
	}
	for _, marker := range []string{"bot", "crawler", "spider", "preview", "facebookexternalhit", "slackbot", "telegrambot", "curl/"} {
		if strings.Contains(agent, marker) {
			return true
		}
	}
	return false
}

func responseMode(binding repository.WebhookBinding) mode {
	switch value, _ := binding.Parameters["responseMode"].(string); value {
	case "lastNode":
		return modeLastNode
	case "responseNode":
		return modeResponseNode
	default:
		return modeImmediate
	}
}

// responseStatus is the status a webhook answers with.
//
// The trigger's `options.responseCode` is what n8n's own option writes, and the
// top-level `responseCode` is where KilasFlow kept it before the options
// collection existed — both are read so a workflow saved either way keeps
// answering with the code it was configured for.
func responseStatus(binding repository.WebhookBinding) int {
	options, _ := binding.Parameters["options"].(map[string]any)
	for _, value := range []any{options["responseCode"], binding.Parameters["responseCode"]} {
		code, ok := value.(float64)
		if ok && code >= 100 && code <= 599 {
			return int(code)
		}
	}
	return http.StatusOK
}

// responseHeaderEntries reads the response headers a trigger or a Respond node
// was configured with, in either shape they are stored in: n8n's
// `{entries: [{name, value}]}` collection, or KilasFlow's own key/value map.
func responseHeaderEntries(value any) map[string]string {
	headers := map[string]string{}
	if wrapper, ok := value.(map[string]any); ok {
		if entries, present := wrapper["entries"].([]any); present {
			for _, entry := range entries {
				fields, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := fields["name"].(string)
				text, _ := fields["value"].(string)
				if strings.TrimSpace(name) != "" {
					headers[name] = text
				}
			}
			return headers
		}
		for name, entry := range wrapper {
			if text, ok := entry.(string); ok {
				headers[name] = text
			}
		}
	}
	return headers
}

// authenticate applies the binding's configured V1 auth mode.
func (handler *Handler) authenticate(r *http.Request, binding repository.WebhookBinding) (int, error) {
	authentication, _ := binding.Parameters["authentication"].(string)
	switch authentication {
	case "", "none":
		return 0, nil
	case "basicAuth", "headerAuth":
	default:
		return http.StatusInternalServerError, errors.New("The webhook authentication mode is not supported.")
	}

	credentialID := credentialReference(binding)
	if credentialID == "" || handler.credentials == nil {
		// A webhook configured to authenticate but unable to is closed, not
		// open: failing open would silently publish an unprotected endpoint.
		return http.StatusInternalServerError, errors.New("This webhook requires a credential that is not configured.")
	}
	record, fields, err := handler.credentials.Resolve(r.Context(), repository.TenantScope{ID: binding.TenantID}, credentialID)
	if err != nil {
		return http.StatusInternalServerError, errors.New("This webhook's credential could not be resolved.")
	}

	switch authentication {
	case "basicAuth":
		if record.Type != "httpBasicAuth" {
			return http.StatusInternalServerError, errors.New("This webhook is bound to a credential of the wrong type.")
		}
		user, password, ok := r.BasicAuth()
		if !ok || !equal(user, fields["user"]) || !equal(password, fields["password"]) {
			return http.StatusUnauthorized, errors.New("Basic authentication failed.")
		}
	case "headerAuth":
		if record.Type != "httpHeaderAuth" {
			return http.StatusInternalServerError, errors.New("This webhook is bound to a credential of the wrong type.")
		}
		name := strings.TrimSpace(fields["name"])
		if name == "" || !equal(r.Header.Get(name), fields["value"]) {
			return http.StatusUnauthorized, errors.New("Header authentication failed.")
		}
	}
	return 0, nil
}

// credentialFields resolves the credential of one type the binding names.
//
// The type is checked rather than trusted: a binding whose reference points at
// a credential of another type is a misconfiguration, and using it anyway would
// hand a bot token to something expecting an API key.
func (handler *Handler) credentialFields(ctx context.Context, binding repository.WebhookBinding, credentialType string) (map[string]string, error) {
	if handler.credentials == nil {
		return nil, errors.New("credentials are not configured on this server")
	}
	references, _ := binding.Parameters["$credentials"].(map[string]any)
	id, _ := references[credentialType].(string)
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("this trigger has no %s credential attached", credentialType)
	}
	record, fields, err := handler.credentials.Resolve(ctx, repository.TenantScope{ID: binding.TenantID}, id)
	if err != nil {
		return nil, fmt.Errorf("this trigger's %s credential could not be resolved", credentialType)
	}
	if record.Type != credentialType {
		return nil, fmt.Errorf("this trigger is bound to a %s credential, not %s", record.Type, credentialType)
	}
	return fields, nil
}

func credentialReference(binding repository.WebhookBinding) string {
	credentialsValue, ok := binding.Parameters["$credentials"].(map[string]any)
	if !ok {
		return ""
	}
	for _, id := range credentialsValue {
		if text, ok := id.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// equal compares in constant time so a wrong secret cannot be discovered by
// timing how long the comparison took.
func equal(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requestPayload builds the trigger item.
//
// Headers are redacted here, before the payload is ever handed to the engine,
// so an inbound Authorization header never reaches an execution record.
// readDelivery captures everything the HTTP boundary knows about a request,
// including the exact bytes the client sent.
//
// Those bytes used to be discarded the moment the body was decoded, and the
// envelope was re-marshalled from the decoded form — so no signature could ever
// be verified. WAHA's X-Webhook-Hmac is a sha512 over the raw body, and a
// re-marshalled body does not hash to the same value, so HMAC verification was
// not merely unimplemented but impossible.
func (handler *Handler) readDelivery(r *http.Request, binding repository.WebhookBinding, params map[string]any) (Delivery, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, handler.limits.MaxBodyBytes+1))
	if err != nil {
		return Delivery{}, errors.New("The request body could not be read.")
	}
	if int64(len(raw)) > handler.limits.MaxBodyBytes {
		return Delivery{}, fmt.Errorf("The request body exceeds the %d byte limit.", handler.limits.MaxBodyBytes)
	}

	// Header names are lower-cased, which is what n8n's item carries and what
	// an imported workflow's own `$json.headers['x-api-key']` check is written
	// against — Go's canonical case made every such check miss.
	headers := make(map[string]any, len(r.Header)+1)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	if r.Host != "" {
		headers["host"] = r.Host
	}

	query := make(map[string]any, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		switch len(values) {
		case 0:
		case 1:
			query[key] = values[0]
		default:
			// A repeated query key is a list, which is what n8n's parser
			// produces and what `$json.query.tag` expects.
			list := make([]any, 0, len(values))
			for _, value := range values {
				list = append(list, value)
			}
			query[key] = list
		}
	}

	contentType := r.Header.Get("Content-Type")
	return Delivery{
		Request: r, RawBody: raw, ContentType: contentType, Binding: binding,
		Headers: headers, Query: query, Params: params, WebhookURL: requestURL(r),
		Body: decodeBody(raw, contentType),
		// A closure rather than the record: a delivery that never verifies
		// never decrypts anything.
		Credential: func(credentialType string) (map[string]string, error) {
			return handler.credentialFields(r.Context(), binding, credentialType)
		},
	}, nil
}

// requestURL is the address the request arrived at, which n8n puts on the item
// as `webhookUrl`.
func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host + r.URL.Path
}

// stateReader is the cheap half of a runner: one status read with no node runs
// and no payloads.
//
// Polling `Get` re-reads the whole execution — every node run, its input and its
// output — twenty times a second, which on a large execution is megabytes per
// poll for a single status field. A runner that implements this is polled
// through it and only read in full once the run is terminal.
type stateReader interface {
	ExecutionState(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Status, bool, error)
}

// awaitResponse answers the caller from the graph.
//
// A Respond to Webhook node answers the caller itself: the executor publishes
// the response as an engine event the moment it runs, and this writes it out
// and returns while the rest of the workflow keeps going. That is n8n's
// behaviour and the point of the node — a Slack or WhatsApp webhook has about
// three seconds to acknowledge, and a workflow that acknowledged and then did
// slow work used to time out, be retried, and duplicate its side effects.
//
// The durable record is still watched, because a run that fails before reaching
// any Respond node has to be answered too, and the broker is optional.
func (handler *Handler) awaitResponse(w http.ResponseWriter, r *http.Request, binding repository.WebhookBinding, record execution.Record) {
	var subscription *events.Subscription
	if handler.events != nil {
		// Subscribing replays the events already retained for this execution,
		// so a response published before this line is not missed.
		subscription = handler.events.Subscribe(binding.TenantID, record.ID, 0)
		defer subscription.Close()
	}

	deadline := time.Now().Add(handler.limits.ResponseTimeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	tenant := repository.TenantScope{ID: binding.TenantID}
	for {
		if subscription != nil {
			select {
			case event, open := <-subscription.Events():
				if !open {
					// The broker released the stream; the record is the floor.
					subscription = nil
					continue
				}
				if response, found := responseFromEvent(event); found {
					writeResponse(w, response)
					return
				}
				continue
			case <-ticker.C:
			case <-r.Context().Done():
				return
			}
		} else {
			select {
			case <-ticker.C:
			case <-r.Context().Done():
				return
			}
		}

		if status, err := handler.executionStatus(r.Context(), tenant, record.ID); err == nil && terminal(status) {
			finished, err := handler.runner.Get(r.Context(), tenant, record.ID)
			if err != nil {
				problem(w, http.StatusInternalServerError, "The execution could not be read.")
				return
			}
			handler.respondFromExecution(w, binding, finished)
			return
		}
		if time.Now().After(deadline) {
			problem(w, http.StatusGatewayTimeout, "The workflow did not finish before the response timeout.")
			return
		}
	}
}

// executionStatus reads just the status, through the cheap reader when the
// runner offers one.
func (handler *Handler) executionStatus(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Status, error) {
	if reader, ok := handler.runner.(stateReader); ok {
		status, _, err := reader.ExecutionState(ctx, tenant, executionID)
		return status, err
	}
	record, err := handler.runner.Get(ctx, tenant, executionID)
	if err != nil {
		return "", err
	}
	return record.Status, nil
}

func terminal(status execution.Status) bool {
	switch status {
	case execution.StatusSucceeded, execution.StatusFailed, execution.StatusCancelled:
		return true
	default:
		return false
	}
}

// respondImmediate acknowledges a delivery without waiting for the run.
//
// The body is n8n's own `{"message":"Workflow was started"}`, and the status and
// headers come from the trigger's options. KilasFlow answered with
// `{executionId, status}` and ignored the configured code, body and headers, so
// a workflow that acknowledged with 201 and a custom body — or with no body at
// all — did neither.
func (handler *Handler) respondImmediate(w http.ResponseWriter, r *http.Request, binding repository.WebhookBinding, kind TriggerKind) {
	options, _ := binding.Parameters["options"].(map[string]any)
	for name, value := range responseHeaderEntries(options["responseHeaders"]) {
		w.Header().Set(name, value)
	}
	status := responseStatus(binding)

	if noBody, _ := options["noResponseBody"].(bool); noBody {
		w.WriteHeader(status)
		return
	}
	// A person filled in a form in a browser, so the acknowledgement is a page:
	// n8n renders a thank-you page for the same reason, and a caller staring at
	// `{"message":"Workflow was started"}` cannot tell whether it was received.
	if kind.Page != nil {
		title, message := "Form submitted", ""
		if kind.Page.Message != nil {
			title, message = kind.Page.Message(deliveryOf(r, binding, kind))
		}
		writePage(w, status, RenderFormMessage(title, message, true))
		return
	}
	if body, ok := options["responseData"].(string); ok && strings.TrimSpace(body) != "" {
		// A custom acknowledgement is text, and n8n sends it as text/html —
		// which is what its own HTTP layer does with a string body.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	writeJSON(w, status, map[string]any{"message": "Workflow was started"})
}

// deliveryOf rebuilds the slice of a delivery the page readers need.
//
// The submission's own fields are not carried here: an acknowledgement page
// renders the title and message the trigger configured, which are binding
// parameters and nothing else.
func deliveryOf(r *http.Request, binding repository.WebhookBinding, _ TriggerKind) Delivery {
	return Delivery{Request: r, Binding: binding}
}

// writePage answers with a rendered page.
func writePage(w http.ResponseWriter, status int, page []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A form page is per-workflow and per-binding, so a shared cache holding
	// one tenant's form for another tenant's browser is not acceptable.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(page)
}

// respondFromExecution answers from a finished execution.
//
// The durable copy of a Respond node's answer is consulted first, and before
// the status is even looked at: a node that answered the caller did answer,
// whatever the rest of the graph did afterwards, and the boundary in another
// process only ever reaches this function — it never sees the node's event,
// because the cross-process relay carries identifiers rather than data. Reading
// the answer back off the node's own run is what makes a split api+worker
// deployment reply with the node's body instead of an empty 200 (BUG-cq4yk3).
func (handler *Handler) respondFromExecution(w http.ResponseWriter, binding repository.WebhookBinding, record execution.Record) {
	if response, found := findResponse(record.NodeRuns); found {
		writeResponse(w, response)
		return
	}
	if record.Status != execution.StatusSucceeded {
		// A generic body on purpose. The caller is whoever found the URL, and
		// the specific failure used to be returned here — node names and the
		// upstream's own error text — which told an unauthenticated caller what
		// the workflow is made of. n8n answers the same way.
		writeJSON(w, http.StatusInternalServerError, map[string]any{"message": "Error in workflow"})
		return
	}
	if responseMode(binding) == modeResponseNode {
		// The graph was configured to answer from a node and finished without
		// reaching one. n8n answers 200 with an empty body; answering 500 said
		// the workflow had broken when it had run cleanly.
		w.WriteHeader(http.StatusOK)
		return
	}
	handler.respondFromLastNode(w, binding, record)
}

// findResponse returns the answer a Respond to Webhook node left on its run.
//
// It walks the node runs in execution order and takes the first answer, the
// same choice the live event path makes: with a Respond node on each arm of an
// IF a graph can legitimately produce two, and a defined answer beats a
// different one on each request. A node that answered nobody has no response
// at all, so it is passed over without a special case.
func findResponse(runs []execution.NodeRun) (nodeResponse, bool) {
	ordered := append([]execution.NodeRun(nil), runs...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Sequence < ordered[right].Sequence
	})
	for _, run := range ordered {
		if len(run.Response) == 0 {
			continue
		}
		if response, ok := parseResponse(run.Response); ok {
			return response, true
		}
	}
	return nodeResponse{}, false
}

// respondFromLastNode answers with the items of the last node that ran.
//
// n8n's lastNode returns the last node's data as the body. This used to write
// KilasFlow's own envelope instead — {executionId, status, data} keyed by node
// ID — so a workflow imported with responseMode "lastNode", which is a common
// shape, activated, ran, returned 200, and handed the caller the wrong body
// with nothing reporting it.
//
// "Last" is the highest sequence among the node runs that produced items, which
// is exactly what the engine recorded in execution order. A skipped node and a
// node that produced nothing are both passed over, so the answer is the last
// node the caller would call the last node.
func (handler *Handler) respondFromLastNode(w http.ResponseWriter, binding repository.WebhookBinding, record execution.Record) {
	items := lastNodeItems(record.NodeRuns)
	status := responseStatus(binding)
	switch shape, _ := binding.Parameters["responseData"].(string); shape {
	case "noData":
		// An empty 200, which is what n8n sends for "No Data" — not a 204: the
		// option is about the body, not about the status.
		w.WriteHeader(status)
		return
	case "allEntries":
		// Always an array, including for one item, which is what n8n's own
		// description of this option promises.
		payload := make([]map[string]any, 0, len(items))
		for _, item := range items {
			payload = append(payload, itemJSON(item))
		}
		writeJSON(w, status, payload)
		return
	default:
		if len(items) == 0 {
			// n8n's own answer when the last node produced nothing: an error
			// naming the situation, never the trigger's own request data, which
			// is what used to be echoed back to the caller — headers added by a
			// proxy included.
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": "No item to return was found"})
			return
		}
		writeJSON(w, status, itemJSON(items[0]))
	}
}

// lastNodeItems returns the items of the last node run that produced any.
func lastNodeItems(runs []execution.NodeRun) []workflow.Item {
	ordered := append([]execution.NodeRun(nil), runs...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Sequence > ordered[right].Sequence
	})
	for _, run := range ordered {
		if run.Status != execution.StatusSucceeded || len(run.Output) == 0 {
			continue
		}
		var ports [][]workflow.Item
		if err := json.Unmarshal(run.Output, &ports); err != nil {
			continue
		}
		items := make([]workflow.Item, 0, 4)
		for _, port := range ports {
			items = append(items, port...)
		}
		if len(items) > 0 {
			return items
		}
	}
	return nil
}

func itemJSON(item workflow.Item) map[string]any {
	if item.JSON == nil {
		return map[string]any{}
	}
	return item.JSON
}

// nodeResponse is one answer produced by a Respond to Webhook node.
type nodeResponse struct {
	statusCode int
	headers    map[string]string
	body       string
}

// responseFromEvent reads a published response, if this event is one.
func responseFromEvent(event events.Event) (nodeResponse, bool) {
	if string(event.Type) != engine.ResponseEventName {
		return nodeResponse{}, false
	}
	return parseResponse(event.Data)
}

// parseResponse decodes the response a Respond node published.
func parseResponse(payload json.RawMessage) (nodeResponse, bool) {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nodeResponse{}, false
	}
	response := nodeResponse{statusCode: http.StatusOK, headers: map[string]string{}}
	if code, ok := decoded["statusCode"].(float64); ok && code >= 100 && code <= 599 {
		response.statusCode = int(code)
	}
	for name, value := range responseHeaderEntries(decoded["headers"]) {
		response.headers[name] = value
	}
	response.body, _ = decoded["body"].(string)
	return response, true
}

func writeResponse(w http.ResponseWriter, response nodeResponse) {
	for key, value := range response.headers {
		w.Header().Set(key, value)
	}
	if w.Header().Get("Content-Type") == "" {
		if json.Valid([]byte(response.body)) {
			w.Header().Set("Content-Type", "application/json")
		} else {
			// n8n answers a text response as text/html, which is what its own
			// HTTP layer does with a string — and what makes an HTML result
			// page render rather than appear as source.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
	}
	w.WriteHeader(response.statusCode)
	_, _ = io.WriteString(w, response.body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}

// Extract returns the routable webhook triggers in a document.
//
// It lives here rather than in the repository so node-type knowledge stays out
// of persistence, and is injected into the workflow store at composition.
// Extract reads a document's webhook triggers from the node catalogue.
//
// No node type name appears here or at composition. Any registered definition
// that declares a webhook produces a binding, which is what lets a generated
// pack ship a trigger that actually receives requests — before this, exactly
// one type could bind an inbound path and it was named in main().
func Extract(catalog Catalog, normalizePath func(string) string) repository.WebhookExtractor {
	return func(document workflow.Document) []repository.WebhookTrigger {
		triggers := make([]repository.WebhookTrigger, 0, 1)
		for _, node := range document.Nodes {
			if node.Disabled {
				// A trigger its author switched off must not open an endpoint:
				// the runner refuses to start a disabled trigger, so binding it
				// would publish a URL that answers and then never runs anything.
				continue
			}
			definition, found := catalog.Resolve(node.Type, node.TypeVersion)
			if !found || definition.Webhook == nil {
				continue
			}
			declaration := definition.Webhook

			// The parameter *key* is named by the definition, so a trigger
			// whose path lives under `chatPath` binds just as one using `path`
			// does.
			path := declaration.StaticPath
			if declaration.PathParameter != "" {
				if configured, ok := node.Parameters[declaration.PathParameter].(string); ok {
					path = configured
				}
			}
			path = normalizePath(path)
			if path == "" {
				continue
			}

			methods := declaredMethods(node.Parameters, declaration)
			if len(methods) == 0 {
				methods = []string{http.MethodGet}
			}

			parameters := make(map[string]any, len(node.Parameters)+1)
			for key, value := range node.Parameters {
				parameters[key] = value
			}
			if len(node.Credentials) > 0 {
				// Carried under a reserved key so the HTTP boundary can
				// authenticate without re-reading the workflow document.
				references := make(map[string]any, len(node.Credentials))
				for typeID, id := range node.Credentials {
					references[typeID] = id
				}
				parameters["$credentials"] = references
			}
			// One binding per method: n8n v2.1 lets a single webhook node answer
			// several, and a node that named two used to bind only one of them —
			// every caller of the other got a 404.
			for _, method := range methods {
				triggers = append(triggers, repository.WebhookTrigger{
					NodeID: node.ID, NodeType: node.Type,
					Method: strings.ToUpper(method), Path: path, Parameters: parameters,
				})
			}
		}
		return triggers
	}
}

// declaredMethods reads the HTTP methods a webhook node answers on.
//
// A missing method is GET, which is n8n's own default and therefore what its
// export omits — KilasFlow bound every such node as POST, so an imported GET
// webhook answered 404 to its own callers. `httpMethods` and an array-valued
// `httpMethod` are both read, because n8n v2.1 stores several methods in the
// same parameter the single-method form uses a string for.
func declaredMethods(parameters map[string]any, declaration *node.WebhookDeclaration) []string {
	collect := func(value any) []string {
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return []string{trimmed}
			}
		case []string:
			return typed
		case []any:
			methods := make([]string, 0, len(typed))
			for _, entry := range typed {
				if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
					methods = append(methods, text)
				}
			}
			return methods
		}
		return nil
	}
	if declaration.MethodParameter != "" {
		if methods := collect(parameters[declaration.MethodParameter]); len(methods) > 0 {
			return methods
		}
	}
	if methods := collect(parameters["httpMethods"]); len(methods) > 0 {
		return methods
	}
	if declaration.Method != "" {
		return []string{declaration.Method}
	}
	return nil
}

// Catalog is the slice of the node registry extraction needs. Declaring it here
// rather than taking *node.Registry keeps this package testable without one and
// says exactly what it reads.
type Catalog interface {
	// Resolve rather than Get, so a document carrying n8n's own typeVersion
	// finds the definition the compiler will run it against rather than
	// failing to bind because no exact match is registered.
	Resolve(nodeType string, version workflow.TypeVersion) (node.Definition, bool)
}

// deliveryIdentifier reads the sender's own identifier for this delivery.
//
// The header is named by the trigger, because every sender uses a different
// one — WAHA sends X-Webhook-Request-Id, Telegram and others their own. When
// the trigger names none, or the sender omits it, the request is never deduped:
// the only content-based alternative would be hashing the body, and two
// genuinely identical messages sent twice by a user are not a duplicate
// delivery.
func deliveryIdentifier(r *http.Request, binding repository.WebhookBinding) string {
	header, _ := binding.Parameters["deliveryIdHeader"].(string)
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(header))
}

// answerDuplicate replies to a repeated delivery without running the workflow
// again.
//
// When the original execution is known and finished, its outcome is mirrored,
// so a retrying sender sees the same answer it would have seen had its first
// attempt not timed out. When it is still running — or the winner had not yet
// recorded it — the honest answer is that the delivery was accepted, because it
// was.
func (handler *Handler) answerDuplicate(w http.ResponseWriter, r *http.Request, binding repository.WebhookBinding, executionID string) {
	if executionID != "" && handler.runner != nil {
		if record, err := handler.runner.Get(r.Context(), repository.TenantScope{ID: binding.TenantID}, executionID); err == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"executionId": record.ID,
				"status":      string(record.Status),
				"duplicate":   true,
			})
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"executionId": executionID,
		"duplicate":   true,
	})
}

// QueueRunner exposes the queueing half of the handler.
//
// Telegram's polling mode has no HTTP request to arrive on and still has to
// queue an execution against a binding, so it needs the same seam the inbound
// boundary uses rather than a second path into the runtime.
func (handler *Handler) QueueRunner() Runner { return handler.runner }
