package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/repository"
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
	// Params holds the path parameters a route pattern captured, which is how
	// n8n's `/user/:id` endpoints carry the id into the item.
	Params map[string]any
	// WebhookURL is the address the request arrived at, which n8n puts on the
	// item so a workflow can quote its own endpoint back to a caller.
	WebhookURL string
	// Body is the decoded body: a map or slice for JSON, a string otherwise.
	Body any
	// Claims is the verified JWT payload this delivery presented, when its
	// trigger authenticated with jwtAuth. n8n puts the same value on the item as
	// `jwtPayload`.
	Claims map[string]any
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

// ExecutionModeProduction is n8n's `executionMode` for a live delivery.
//
// n8n also has a "test" mode for a request captured by "listen for test event",
// which KilasFlow has no equivalent of yet; a delivery that reaches a binding
// is by definition a production one.
const ExecutionModeProduction = "production"

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
	// ShapeFormSubmission is the submitted fields at the top level, which is
	// what n8n's form trigger emits: the page's own field labels, plus
	// `submittedAt` and `formMode` that the boundary stamps.
	ShapeFormSubmission Shape = "formSubmission"
)

// Apply builds the item for one delivery.
func (shape Shape) Apply(delivery Delivery) map[string]any {
	switch shape {
	case ShapeN8NCore:
		params := delivery.Params
		if params == nil {
			// The key is present and empty rather than absent, so an
			// expression reading `$json.params.id` gets nothing rather than
			// failing on an undefined root.
			params = map[string]any{}
		}
		item := map[string]any{
			"body":    delivery.Body,
			"headers": delivery.Headers,
			"params":  params,
			"query":   delivery.Query,
			// The three keys n8n's own Webhook item carries beside the four
			// above. An imported workflow that reads `$json.webhookUrl` or
			// branches on `$json.executionMode` resolved to nothing without
			// them, and a REST-style endpoint's `$json.params.id` never
			// existed at all.
			"webhookUrl":    delivery.WebhookURL,
			"executionMode": ExecutionModeProduction,
		}
		// n8n puts the verified token on the item as `jwtPayload`, and an
		// imported workflow reads it there — `$json.jwtPayload.sub`, usually.
		// The key is absent only when no token was verified: a workflow
		// branching on its presence must see the difference between "no JWT"
		// and "a JWT with no claims", and an empty payload is the second. A
		// length test cannot tell them apart, so this is a presence test —
		// jwtClaims returns nil exactly when nothing was verified.
		if delivery.Claims != nil {
			item["jwtPayload"] = delivery.Claims
		}
		return item
	case ShapeFormSubmission:
		if body, ok := delivery.Body.(map[string]any); ok {
			item := make(map[string]any, len(body))
			for key, value := range body {
				item[key] = value
			}
			return item
		}
		// A form with no readable field cannot be an item, so it is reported
		// rather than queued as an empty run.
		return map[string]any{"body": delivery.Body}
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

// HostedPage is how a trigger serves a page of its own and receives what that
// page sends back.
//
// It is a set of readers rather than a description, because the page belongs to
// one *binding* — a workflow's own fields and title — while a trigger kind is
// registered once per node type. Everything the page needs is therefore read
// from the delivery that arrived, which is also what keeps the HTTP boundary
// free of node-type knowledge: it renders whatever the registered reader
// returns.
type HostedPage struct {
	// Form describes the page for one delivery.
	Form func(Delivery) Form
	// Submission turns a submitted body into the item the workflow sees, or
	// refuses it. Refusing here is what stops a browser that disabled its own
	// validation from starting a run with an empty required field.
	Submission func(Delivery) (map[string]any, error)
	// Message is the page a submission is answered with when the trigger
	// answers immediately. A person filling in a form is owed a page, not a
	// JSON body.
	Message func(Delivery) (string, string)
}

// TriggerKind is how a webhook trigger node type wants its deliveries handled.
type TriggerKind struct {
	Shape Shape
	// Page, when set, makes this trigger serve a hosted page on GET and read a
	// submission on POST.
	Page *HostedPage
	// Verify is optional. When set it runs before the execution is queued and
	// its error is the client's answer.
	Verify Verifier
	// Verifies is optional and reports whether Verify actually checks the
	// sender of a delivery to this binding. A nil Verifies means Verify, when
	// set, always does.
	//
	// It exists because a verifier can be conditional on the binding: a pack
	// trigger declares an HMAC verifier but has nothing to check against when
	// the node holds no secret, so it skips the check and admits everyone. "Has
	// a Verify" and "authenticates its callers" are therefore different
	// questions, and webhook.require_auth asks the second one.
	Verifies func(Delivery) bool
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
// Every body type n8n's webhook accepts is decoded to the same value n8n would
// produce: JSON to its value, a urlencoded or multipart form to an object (a
// repeated key to a list), XML to an object, and text to a string. Anything
// that is not text at all keeps its bytes losslessly rather than being forced
// through a UTF-8 string, which is where an uploaded PNG used to come out as
// replacement characters with no way back.
func decodeBody(raw []byte, contentType string) any {
	if len(raw) == 0 {
		return nil
	}
	media := strings.ToLower(mediaType(contentType))
	switch {
	case media == "multipart/form-data":
		if decoded, err := parseMultipart(raw, contentType); err == nil {
			return decoded
		}
	case media == "application/x-www-form-urlencoded":
		if values, err := parseForm(string(raw)); err == nil {
			return values
		}
	case media == "application/xml" || media == "text/xml" || strings.HasSuffix(media, "+xml"):
		if decoded, err := parseXML(raw); err == nil {
			return decoded
		}
	}
	if json.Valid(raw) {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			return decoded
		}
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	// Not text: the bytes are kept verbatim, base64-encoded, in the same keys
	// n8n's binary data object uses. The execution-scoped binary store is not
	// reachable from the HTTP boundary yet, so this is where an upload lands
	// until it is; what it must not do is corrupt the payload, which is what
	// string(raw) did to every non-UTF-8 byte.
	return map[string]any{
		"data":     base64.StdEncoding.EncodeToString(raw),
		"mimeType": mediaType(contentType),
		"fileSize": float64(len(raw)),
	}
}

// mediaType strips the parameters from a Content-Type header.
func mediaType(contentType string) string {
	if index := strings.Index(contentType, ";"); index >= 0 {
		return strings.TrimSpace(contentType[:index])
	}
	return strings.TrimSpace(contentType)
}

// parseMultipart decodes a multipart/form-data body.
//
// Fields become body keys and a repeated field becomes a list, which is what
// n8n's own body parser produces. A file part carries its metadata and its
// bytes (base64) rather than the raw multipart text: an upload used to arrive
// as one string containing the whole envelope, boundary markers and all, and
// nothing downstream could tell one field from another.
func parseMultipart(raw []byte, contentType string) (map[string]any, error) {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	boundary := parameters["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("multipart body has no boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(raw), boundary)
	decoded := map[string]any{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := part.FormName()
		if name == "" {
			continue
		}
		contents, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		var value any
		if fileName := part.FileName(); fileName != "" {
			value = map[string]any{
				"fileName": fileName,
				"mimeType": part.Header.Get("Content-Type"),
				"fileSize": float64(len(contents)),
				"data":     base64.StdEncoding.EncodeToString(contents),
			}
		} else {
			value = string(contents)
		}
		if existing, present := decoded[name]; present {
			if list, ok := existing.([]any); ok {
				decoded[name] = append(list, value)
			} else {
				decoded[name] = []any{existing, value}
			}
			continue
		}
		decoded[name] = value
	}
	return decoded, nil
}

// parseXML decodes an XML body into an object.
//
// The conventions are the ones n8n's parser uses: an element's attributes land
// under `$`, repeated children become an array, and an element's text is the
// value itself unless the element also has attributes or children, in which
// case the text sits under `_`. SOAP and XML callbacks are a real share of the
// webhooks customers bring over, and a raw XML string is not something an
// expression can read a field out of.
func parseXML(raw []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	root := &xmlNode{children: map[string][]*xmlNode{}}
	stack := []*xmlNode{root}
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			node := &xmlNode{name: typed.Name.Local, children: map[string][]*xmlNode{}}
			for _, attribute := range typed.Attr {
				if node.attributes == nil {
					node.attributes = map[string]string{}
				}
				node.attributes[attribute.Name.Local] = attribute.Value
			}
			parent := stack[len(stack)-1]
			parent.children[node.name] = append(parent.children[node.name], node)
			stack = append(stack, node)
			text.Reset()
		case xml.CharData:
			text.Write(typed)
		case xml.EndElement:
			if len(stack) < 2 {
				continue
			}
			node := stack[len(stack)-1]
			node.text = strings.TrimSpace(text.String())
			text.Reset()
			stack = stack[:len(stack)-1]
		}
	}
	if len(root.children) == 0 {
		return nil, fmt.Errorf("XML body has no elements")
	}
	return root.object(), nil
}

// xmlNode is one element of a decoded document.
type xmlNode struct {
	name       string
	attributes map[string]string
	text       string
	children   map[string][]*xmlNode
}

// object renders an element as n8n's parser would.
func (node *xmlNode) object() map[string]any {
	rendered := make(map[string]any, len(node.children)+2)
	if len(node.attributes) > 0 {
		attributes := make(map[string]any, len(node.attributes))
		for name, value := range node.attributes {
			attributes[name] = value
		}
		rendered["$"] = attributes
	}
	for name, children := range node.children {
		if len(children) == 1 {
			rendered[name] = children[0].value()
			continue
		}
		list := make([]any, 0, len(children))
		for _, child := range children {
			list = append(list, child.value())
		}
		rendered[name] = list
	}
	if node.text != "" {
		if len(rendered) == 0 {
			return map[string]any{"_": node.text}
		}
		rendered["_"] = node.text
	}
	return rendered
}

// value renders an element as the value it holds: its own object when it has
// children or attributes, its text when that is all it has.
func (node *xmlNode) value() any {
	if len(node.children) == 0 && len(node.attributes) == 0 {
		return node.text
	}
	return node.object()
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
		switch len(entries) {
		case 0:
		case 1:
			decoded[key] = entries[0]
		default:
			// A repeated key is a list — `tags=a&tags=b` is a checkbox group or
			// a multi-value field, and keeping only the first value silently
			// dropped the rest.
			list := make([]any, 0, len(entries))
			for _, entry := range entries {
				list = append(list, entry)
			}
			decoded[key] = list
		}
	}
	return decoded, nil
}

// HMACScheme is one service's signature scheme, every knob of it, as a pack
// names them on a trigger's `hmac` block.
//
// The zero values are the simplest scheme: the raw body, hex, no prefix, no
// timestamp — which is what WAHA's X-Webhook-Hmac is, and what a scheme with
// only Header and Algorithm left describes.
type HMACScheme struct {
	// Header carries the signature. A delivery without it is refused.
	Header string
	// Algorithm is the digest: sha256 or sha512.
	Algorithm string
	// Encoding is how the sender wrote the digest: hex (the default) or
	// base64. Hex compares case-insensitively, as it always has; base64 is
	// case-sensitive and is compared exactly.
	Encoding string
	// Prefix stands between the version marker and the digest, as GitHub's
	// `sha256=` or Standard Webhooks' `v1,` do. A prefix ending in a comma
	// marks a scheme whose entries are `v1,<digest>` pairs, so the comma
	// splitting one delivery's several signatures from each other also splits
	// each pair in two — the pair is re-joined before it is checked.
	Prefix string
	// SigningString is what is signed, as a `{{ .body }}`-style template over
	// the raw body (`{{ .body }}`), the timestamp header's value
	// (`{{ .timestamp }}`) and any request header (`{{ .header.X-Name }}`).
	// Empty is the raw body alone.
	SigningString string
	// TimestampHeader names the header `{{ .timestamp }}` reads, which is
	// also the header Tolerance judges. Signing a timestamp and refusing a
	// stale one is what closes the replay window a bare body signature leaves
	// open.
	TimestampHeader string
	// Tolerance is how old (or far ahead) the timestamp may be. Zero means no
	// staleness check.
	Tolerance time.Duration
}

// hmacReference matches one `{{ .… }}` placeholder in a signing string. The
// house style is the lifecycle template's — a leading dot, one pass, never the
// expression evaluator — with a charset that also admits header names, whose
// hyphens a field name never carries.
var hmacReference = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_.-]+)\s*\}\}`)

// anyPlaceholder matches every `{{ … }}` a signing string carries, so one
// that is not a placeholder this scheme reads — an expression, a mis-spelled
// field — is refused rather than signed over as literal text.
var anyPlaceholder = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// CheckSigningString refuses a template the verifier could not honour: a name
// that is not the body, the timestamp, or a header, and a timestamp the
// scheme has no header to read.
//
// It runs where the pack is validated, so an author hears about a typo in
// `{{ .boday }}` at authoring time rather than as a refused delivery.
func CheckSigningString(template, timestampHeader string) error {
	for _, span := range anyPlaceholder.FindAllStringIndex(template, -1) {
		placeholder := template[span[0]:span[1]]
		match := hmacReference.FindStringSubmatch(placeholder)
		if match == nil || match[0] != placeholder {
			return fmt.Errorf("the signing string may carry only {{ .body }}, {{ .timestamp }} and {{ .header.<name> }} placeholders, not %s", placeholder)
		}
		key := match[1]
		switch {
		case key == "body":
		case key == "timestamp":
			if timestampHeader == "" {
				return fmt.Errorf("the signing string reads {{ .timestamp }}, which needs the header it comes from: name a timestampHeader")
			}
		case strings.HasPrefix(key, "header."):
			if strings.TrimSpace(key[len("header."):]) == "" {
				return fmt.Errorf("the signing string reads a header with no name")
			}
		default:
			return fmt.Errorf("the signing string may name only the body, the timestamp and header values, not {{ .%s }}", key)
		}
	}
	return nil
}

// renderSigningString builds what this delivery is signed over, substituting
// once so a value is never rescanned. A header the request does not carry
// renders as the empty string — that is what the sender signed when they
// signed without it, and a signature computed any other way simply does not
// match.
func renderSigningString(template string, delivery Delivery, timestamp string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return string(delivery.RawBody), nil
	}
	var out strings.Builder
	last := 0
	for _, match := range hmacReference.FindAllStringSubmatchIndex(template, -1) {
		out.WriteString(template[last:match[0]])
		last = match[1]
		key := template[match[2]:match[3]]
		switch {
		case key == "body":
			out.Write(delivery.RawBody)
		case key == "timestamp":
			out.WriteString(timestamp)
		case strings.HasPrefix(key, "header."):
			name := key[len("header."):]
			if delivery.Request == nil {
				continue
			}
			out.WriteString(delivery.Request.Header.Get(name))
		default:
			return "", fmt.Errorf("the signing string names {{ .%s }}, which this delivery cannot provide", key)
		}
	}
	out.WriteString(template[last:])
	return out.String(), nil
}

// signatureCandidates splits one signature header into the digests it
// carries. Services that rotate their secret send several at once, and any one
// of them matching is a pass — the sender is proving knowledge of a secret
// this endpoint still accepts, whichever of the two it is.
//
// A declared prefix is load-bearing: a scheme behind `v1,` accepts only
// `v1,<digest>` pairs (the comma inside the prefix splits each pair in two, so
// the pair is re-joined), and a scheme behind `sha256=` accepts only tokens
// carrying it. A bare digest under either is a header this scheme did not
// send.
func signatureCandidates(value, prefix string) []string {
	joined := strings.HasSuffix(prefix, ",")
	version := strings.TrimSuffix(prefix, ",")

	var candidates []string
	tokens := strings.Split(value, ",")
	for index := 0; index < len(tokens); index++ {
		token := strings.TrimSpace(tokens[index])
		if joined {
			if token != version || index+1 >= len(tokens) {
				continue
			}
			if digest := strings.TrimSpace(tokens[index+1]); digest != "" {
				candidates = append(candidates, digest)
			}
			index++
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(token, prefix) {
				continue
			}
			token = token[len(prefix):]
		}
		if token != "" {
			candidates = append(candidates, token)
		}
	}
	return candidates
}

// parseSignedTimestamp reads a timestamp header: unix seconds, which is what
// Standard Webhooks sends, or RFC 3339 for the services that spell it out.
func parseSignedTimestamp(value string) (time.Time, bool) {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
		return time.Unix(seconds, 0), true
	}
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

// signatureMatches compares one candidate against the expected digest in
// constant time, case-insensitively for hex and exactly for base64, whose
// alphabet is its case.
func signatureMatches(candidate, expected, encoding string) bool {
	if encoding == "base64" {
		return hmac.Equal([]byte(candidate), []byte(expected))
	}
	return hmac.Equal([]byte(strings.ToLower(candidate)), []byte(strings.ToLower(expected)))
}

// HMACSchemeVerifier refuses a delivery whose signature does not match its
// scheme: missing, malformed, stale, or signed by somebody else.
//
// It is what the raw-body capture exists for. Both WAHA's X-Webhook-Hmac and
// Standard Webhooks' `v1,` signatures are over the exact bytes, and a body
// that has been decoded and re-marshalled does not hash to the same value —
// so before the bytes were carried this check could not be written at all,
// only approximated.
//
// A missing or malformed signature is a refusal rather than a pass: a
// verifier that accepts an absent signature verifies nothing. A timestamp
// outside the tolerance is refused with it, because it is a replay of a
// delivery that was signed earlier, by whoever kept the bytes.
func HMACSchemeVerifier(scheme HMACScheme, secret func(Delivery) (string, error)) Verifier {
	return func(delivery Delivery) error {
		provided := strings.TrimSpace(delivery.Request.Header.Get(scheme.Header))
		if provided == "" {
			return fmt.Errorf("this endpoint requires a %s signature", scheme.Header)
		}
		key, err := secret(delivery)
		if err != nil {
			return fmt.Errorf("the signing secret for this endpoint could not be read")
		}
		if key == "" {
			return fmt.Errorf("this endpoint has no signing secret configured")
		}

		timestamp := ""
		if scheme.TimestampHeader != "" {
			timestamp = strings.TrimSpace(delivery.Request.Header.Get(scheme.TimestampHeader))
			if timestamp == "" {
				return fmt.Errorf("this endpoint requires a %s timestamp", scheme.TimestampHeader)
			}
			if scheme.Tolerance > 0 {
				at, ok := parseSignedTimestamp(timestamp)
				if !ok {
					return fmt.Errorf("the %s timestamp cannot be read as unix seconds or RFC 3339", scheme.TimestampHeader)
				}
				if drift := time.Since(at); drift > scheme.Tolerance || drift < -scheme.Tolerance {
					return fmt.Errorf("the %s timestamp is outside this endpoint's tolerance, so the delivery is a replay", scheme.TimestampHeader)
				}
			}
		}

		signed, err := renderSigningString(scheme.SigningString, delivery, timestamp)
		if err != nil {
			return err
		}

		var newHash func() hash.Hash
		switch scheme.Algorithm {
		case "sha256":
			newHash = sha256.New
		case "sha512":
			newHash = sha512.New
		default:
			// Unreachable behind pack validation, which is the point of
			// refusing an unknown algorithm at registration: it names a
			// scheme this build cannot verify, and silently accepting every
			// delivery is what an unverified endpoint is.
			return fmt.Errorf("hmac algorithm %q is not implemented; use sha256 or sha512", scheme.Algorithm)
		}
		mac := hmac.New(newHash, []byte(key))
		mac.Write([]byte(signed))
		digest := mac.Sum(nil)

		expected := hex.EncodeToString(digest)
		if scheme.Encoding == "base64" {
			expected = base64.StdEncoding.EncodeToString(digest)
		}
		for _, candidate := range signatureCandidates(provided, scheme.Prefix) {
			if signatureMatches(candidate, expected, scheme.Encoding) {
				return nil
			}
		}
		return fmt.Errorf("the %s signature does not match the request body", scheme.Header)
	}
}
