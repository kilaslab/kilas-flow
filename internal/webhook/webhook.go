package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

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

	binding, err := handler.bindings.Resolve(r.Context(), strings.ToUpper(r.Method), path)
	if err != nil {
		// An inactive, deleted, or never-activated workflow has no binding, and
		// a wrong method has none either. Answering both the same way keeps the
		// endpoint from confirming which workflows exist.
		problem(w, http.StatusNotFound, "No active workflow is bound to this webhook.")
		return
	}

	if status, err := handler.authenticate(r, binding); err != nil {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="webhook"`)
		}
		problem(w, status, err.Error())
		return
	}

	delivery, err := handler.readDelivery(r, binding)
	if err != nil {
		problem(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}

	// The trigger's own type decides both the shape and whether the delivery is
	// acceptable at all. A signature check belongs here rather than inside the
	// workflow: a request that fails it should never become an execution.
	kind := handler.triggers.Lookup(binding.NodeType)
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
		problem(w, http.StatusInternalServerError, "The workflow could not be queued.")
		return
	}

	if mode := responseMode(binding); mode == modeImmediate {
		writeJSON(w, immediateStatus(binding), map[string]any{
			"executionId": record.ID,
			"status":      string(record.Status),
		})
		return
	}

	finished, err := handler.await(r.Context(), binding, record.ID)
	if err != nil {
		problem(w, http.StatusGatewayTimeout, "The workflow did not finish before the response timeout.")
		return
	}
	handler.respondFromExecution(w, binding, finished, responseMode(binding))
}

type mode int

const (
	modeImmediate mode = iota
	modeLastNode
	modeResponseNode
)

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

func immediateStatus(binding repository.WebhookBinding) int {
	code, ok := binding.Parameters["responseCode"].(float64)
	if !ok || code < 100 || code > 599 {
		return http.StatusOK
	}
	return int(code)
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
func (handler *Handler) readDelivery(r *http.Request, binding repository.WebhookBinding) (Delivery, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, handler.limits.MaxBodyBytes+1))
	if err != nil {
		return Delivery{}, errors.New("The request body could not be read.")
	}
	if int64(len(raw)) > handler.limits.MaxBodyBytes {
		return Delivery{}, fmt.Errorf("The request body exceeds the %d byte limit.", handler.limits.MaxBodyBytes)
	}

	headers := make(map[string]any, len(r.Header))
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	// Only the headers are redacted. The body is the caller's own data and the
	// running workflow reads it straight back out of the stored record, so
	// redacting it would put "[redacted]" on the wire to WAHA rather than the
	// session the envelope named.
	headers = execution.RedactMap(headers)

	query := make(map[string]any, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}

	contentType := r.Header.Get("Content-Type")
	return Delivery{
		Request: r, RawBody: raw, ContentType: contentType, Binding: binding,
		Headers: headers, Query: query, Body: decodeBody(raw, contentType),
		// A closure rather than the record: a delivery that never verifies
		// never decrypts anything.
		Credential: func(credentialType string) (map[string]string, error) {
			return handler.credentialFields(r.Context(), binding, credentialType)
		},
	}, nil
}

// await waits for the execution to reach a terminal state.
//
// It polls the durable record rather than trusting a live event, because the
// record is what the response must reflect and it is correct even if the
// broker dropped an event under backpressure.
func (handler *Handler) await(ctx context.Context, binding repository.WebhookBinding, executionID string) (execution.Record, error) {
	deadline := time.Now().Add(handler.limits.ResponseTimeout)
	tenant := repository.TenantScope{ID: binding.TenantID}
	for {
		record, err := handler.runner.Get(ctx, tenant, executionID)
		if err == nil && terminal(record.Status) {
			return record, nil
		}
		if time.Now().After(deadline) {
			return execution.Record{}, errors.New("timed out")
		}
		select {
		case <-ctx.Done():
			return execution.Record{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func terminal(status execution.Status) bool {
	switch status {
	case execution.StatusSucceeded, execution.StatusFailed, execution.StatusCancelled:
		return true
	default:
		return false
	}
}

// respondFromExecution turns a finished execution into the HTTP response.
func (handler *Handler) respondFromExecution(w http.ResponseWriter, binding repository.WebhookBinding, record execution.Record, responseMode mode) {
	if record.Status != execution.StatusSucceeded {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"executionId": record.ID,
			"status":      string(record.Status),
			"error":       json.RawMessage(record.Error),
		})
		return
	}

	if responseMode == modeLastNode {
		handler.respondFromLastNode(w, binding, record)
		return
	}

	outputs := map[string][][]workflow.Item{}
	if len(record.Output) > 0 {
		_ = json.Unmarshal(record.Output, &outputs)
	}

	if responseMode == modeResponseNode {
		if response, found := findResponse(record.NodeRuns); found {
			writeResponse(w, response)
			return
		}
		// The graph was configured to answer from a node but never reached one.
		// Saying so beats returning a misleading 200 with the last node's data.
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"executionId": record.ID,
			"error":       "The workflow finished without reaching a Respond to Webhook node.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"executionId": record.ID,
		"status":      string(record.Status),
		"data":        outputs,
	})
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
	switch shape, _ := binding.Parameters["responseData"].(string); shape {
	case "noData":
		w.WriteHeader(http.StatusNoContent)
		return
	case "allEntries":
		// Always an array, including for one item, which is what n8n's own
		// description of this option promises.
		payload := make([]map[string]any, 0, len(items))
		for _, item := range items {
			payload = append(payload, itemJSON(item))
		}
		writeJSON(w, http.StatusOK, payload)
		return
	default:
		// n8n's default: the first entry's JSON, always an object.
		if len(items) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, itemJSON(items[0]))
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

// ResponseKey marks the item field a Respond to Webhook node writes.
const ResponseKey = "$response"

type nodeResponse struct {
	statusCode int
	headers    map[string]string
	body       string
}

// findResponse returns the response a Respond to Webhook node produced.
//
// It walks the node runs in execution order rather than iterating the output
// map, because Go randomises map iteration: with a Respond node on each arm of
// an IF, which one answered the caller was decided by a coin flip. Pruning
// removes that for the common case — only the taken arm runs now — but a graph
// can still legitimately produce two responses, and the caller is better served
// by a defined answer than by a different one on each request.
//
// The first in execution order wins. A skipped node produced no response at
// all, so it is passed over without needing a special case.
func findResponse(runs []execution.NodeRun) (nodeResponse, bool) {
	ordered := append([]execution.NodeRun(nil), runs...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Sequence < ordered[right].Sequence
	})
	for _, run := range ordered {
		if len(run.Output) == 0 {
			continue
		}
		var ports [][]workflow.Item
		if err := json.Unmarshal(run.Output, &ports); err != nil {
			continue
		}
		for _, items := range ports {
			for _, item := range items {
				response, ok := responseFromItem(item)
				if ok {
					return response, true
				}
			}
		}
	}
	return nodeResponse{}, false
}

func responseFromItem(item workflow.Item) (nodeResponse, bool) {
	raw, ok := item.JSON[ResponseKey].(map[string]any)
	if !ok {
		return nodeResponse{}, false
	}
	response := nodeResponse{statusCode: http.StatusOK, headers: map[string]string{}}
	if code, ok := raw["statusCode"].(float64); ok && code >= 100 && code <= 599 {
		response.statusCode = int(code)
	}
	if headers, ok := raw["headers"].(map[string]any); ok {
		for key, value := range headers {
			if text, ok := value.(string); ok {
				response.headers[key] = text
			}
		}
	}
	response.body, _ = raw["body"].(string)
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
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
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

			method := declaration.Method
			if declaration.MethodParameter != "" {
				if configured, ok := node.Parameters[declaration.MethodParameter].(string); ok && configured != "" {
					method = configured
				}
			}
			if method == "" {
				method = http.MethodPost
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
			triggers = append(triggers, repository.WebhookTrigger{
				NodeID: node.ID, NodeType: node.Type,
				Method: strings.ToUpper(method), Path: path, Parameters: parameters,
			})
		}
		return triggers
	}
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
