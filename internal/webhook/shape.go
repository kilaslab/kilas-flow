package webhook

import (
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// Delivery is everything the HTTP boundary knows about one inbound request.
//
// RawBody is the exact bytes the client sent. They are carried here rather than
// on the item: putting raw bytes on the item would double every payload in the
// executions table, make redaction's job harder, and expose the body twice in
// the API. Verification needs them and shaping usually does not, so they live
// where both can reach them and neither has to store them.
type Delivery struct {
	Request     *http.Request
	RawBody     []byte
	ContentType string
	Binding     repository.WebhookBinding
	// Headers is the redacted header map. Shapes use this rather than the
	// request's own headers so a credential cannot reach an item.
	Headers map[string]any
	Query   map[string]any
	// Body is the decoded body: a map or slice for JSON, a string otherwise.
	Body any
	// Credential resolves the credential the binding names, by type.
	//
	// A verifier needs it: Telegram's per-delivery secret is derived from the
	// bot token, so checking a delivery means reading the same credential the
	// registration used. It is a closure rather than the record itself so a
	// delivery that never verifies never decrypts anything.
	Credential func(credentialType string) (map[string]string, error)
}

// Fields resolves the named credential, or an empty map.
func (delivery Delivery) Fields(credentialType string) (map[string]string, error) {
	if delivery.Credential == nil {
		return nil, fmt.Errorf("credentials are not available at this endpoint")
	}
	return delivery.Credential(credentialType)
}

// Shape turns one delivery into the item a trigger's workflow sees.
//
// It is a named variant rather than a Go function per node, because a generated
// node pack cannot ship Go code — a pack-supplied trigger has to be able to
// *name* a shape it wants.
type Shape string

const (
	// ShapeEnvelope is KilasFlow's own five-key envelope. It is the default so
	// that no already-activated workflow changes behaviour.
	ShapeEnvelope Shape = "envelope"
	// ShapeN8NCore is what n8n's own Webhook node emits.
	ShapeN8NCore Shape = "n8nCore"
	// ShapeBodyAsItem puts the parsed body at the top level, which is what a
	// third-party trigger such as WAHA's produces — those workflows read
	// $json.event, not $json.body.event.
	ShapeBodyAsItem Shape = "bodyAsItem"
)

// Apply builds the item for one delivery.
func (shape Shape) Apply(delivery Delivery) map[string]any {
	switch shape {
	case ShapeN8NCore:
		return map[string]any{
			"body":    delivery.Body,
			"headers": delivery.Headers,
			// n8n's path parameters come from a route pattern, and a binding
			// has no pattern behind it — a route is one opaque segment. The key
			// is present and empty rather than absent, so an expression reading
			// it gets an empty object instead of failing.
			"params": map[string]any{},
			"query":  delivery.Query,
		}
	case ShapeBodyAsItem:
		if body, ok := delivery.Body.(map[string]any); ok {
			item := make(map[string]any, len(body))
			for key, value := range body {
				item[key] = value
			}
			return item
		}
		// A non-object body cannot *be* the item, so it is carried under a key
		// rather than dropped.
		return map[string]any{"body": delivery.Body}
	default:
		return map[string]any{
			"method":      delivery.Request.Method,
			"path":        delivery.Binding.Path,
			"headers":     delivery.Headers,
			"query":       delivery.Query,
			"body":        delivery.Body,
			"contentType": delivery.ContentType,
		}
	}
}

// Verifier refuses a delivery before it is queued.
//
// Verification belongs at the HTTP boundary, not inside the workflow: a request
// that fails its signature check should be answered with an error and never
// become an execution at all.
type Verifier func(Delivery) error

// TriggerKind is how a webhook trigger node type wants its deliveries handled.
type TriggerKind struct {
	Shape Shape
	// Verify is optional. When set it runs before the execution is queued and
	// its error is the client's answer.
	Verify Verifier
	// Accept is optional and is *not* verification. It answers whether this
	// delivery is one the node asked for — a Telegram update from a chat the
	// trigger is restricted away from, say.
	//
	// The distinction is the answer given. A failed signature is 401 and means
	// "you should not be sending this"; a filtered update is 200 and means "I
	// received it and chose not to act", which is what the sender should be
	// told so it stops retrying. Collapsing the two would either invite
	// Telegram to retry a message the user filtered out, or teach an attacker
	// which chat ids a workflow watches.
	Accept Filter
}

// Filter reports whether a delivery is one the trigger asked for, and why not.
type Filter func(Delivery) (bool, string)

// Registry maps a trigger node type to how its deliveries are shaped and
// checked.
//
// It is populated at composition, like the executor registry, so the HTTP
// boundary holds no node-type knowledge of its own.
type Registry struct {
	kinds map[string]TriggerKind
}

// NewRegistry creates an empty trigger registry.
func NewRegistry() *Registry { return &Registry{kinds: map[string]TriggerKind{}} }

// Register binds a trigger node type to its delivery handling.
func (registry *Registry) Register(nodeType string, kind TriggerKind) error {
	if registry == nil || nodeType == "" {
		return fmt.Errorf("webhook trigger type is required")
	}
	if _, exists := registry.kinds[nodeType]; exists {
		return fmt.Errorf("webhook trigger %q is already registered", nodeType)
	}
	if kind.Shape == "" {
		kind.Shape = ShapeEnvelope
	}
	registry.kinds[nodeType] = kind
	return nil
}

// Lookup returns how a trigger type's deliveries are handled.
//
// An unregistered type — including a binding written before node types were
// recorded — gets the envelope shape and no verification, which is exactly what
// every trigger did before this existed.
func (registry *Registry) Lookup(nodeType string) TriggerKind {
	if registry == nil {
		return TriggerKind{Shape: ShapeEnvelope}
	}
	if kind, found := registry.kinds[nodeType]; found {
		return kind
	}
	return TriggerKind{Shape: ShapeEnvelope}
}

// decodeBody turns the raw bytes into the value a shape puts on the item.
//
// A body that is not JSON — form-encoded, plain text, binary — is carried as a
// string rather than lost. Its content type travels beside it, so a workflow
// can tell what it received.
func decodeBody(raw []byte, contentType string) any {
	if len(raw) == 0 {
		return nil
	}
	if json.Valid(raw) {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			return decoded
		}
	}
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		if values, err := parseForm(string(raw)); err == nil {
			return values
		}
	}
	return string(raw)
}

// parseForm decodes a form-encoded body into an object, so a form POST reads
// like JSON rather than like a query string a workflow has to parse itself.
func parseForm(body string) (map[string]any, error) {
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, err
	}
	decoded := make(map[string]any, len(values))
	for key, entries := range values {
		if len(entries) > 0 {
			decoded[key] = entries[0]
		}
	}
	return decoded, nil
}

// HMACVerifier refuses a delivery whose signature does not match the raw body.
//
// It is what the raw-body capture exists for. Both WAHA's X-Webhook-Hmac and
// the owner's own trigger sign the exact bytes, and a body that has been
// decoded and re-marshalled does not hash to the same value — so before the
// bytes were carried this check could not be written at all, only approximated.
//
// The comparison is constant-time, and a missing or malformed signature is a
// refusal rather than a pass: a verifier that accepts an absent signature
// verifies nothing.
func HMACVerifier(header string, newHash func() hash.Hash, secret func(Delivery) (string, error)) Verifier {
	return func(delivery Delivery) error {
		provided := strings.TrimSpace(delivery.Request.Header.Get(header))
		if provided == "" {
			return fmt.Errorf("this endpoint requires a %s signature", header)
		}
		key, err := secret(delivery)
		if err != nil {
			return fmt.Errorf("the signing secret for this endpoint could not be read")
		}
		if key == "" {
			return fmt.Errorf("this endpoint has no signing secret configured")
		}
		mac := hmac.New(newHash, []byte(key))
		mac.Write(delivery.RawBody)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(strings.ToLower(provided)), []byte(expected)) {
			return fmt.Errorf("the %s signature does not match the request body", header)
		}
		return nil
	}
}
