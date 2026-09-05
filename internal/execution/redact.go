package execution

import (
	"encoding/json"
	"strings"
)

// RedactedValue replaces every value the redaction boundary refuses to keep.
// It is a visible marker rather than an omission so an inspector can tell
// "this field was withheld" apart from "this field was never present".
const RedactedValue = "[redacted]"

// sensitiveKeys are compared against normalized object keys: lowercased with
// `-`, `_`, and spaces removed, so `X-Api-Key`, `x_api_key`, and `apiKey` all
// collapse onto the same entry.
var sensitiveKeys = map[string]struct{}{
	"authorization":      {},
	"proxyauthorization": {},
	"wwwauthenticate":    {},
	"cookie":             {},
	"cookies":            {},
	"setcookie":          {},
	"apikey":             {},
	"apisecret":          {},
	"apitoken":           {},
	"token":              {},
	"accesstoken":        {},
	"refreshtoken":       {},
	"idtoken":            {},
	"bearertoken":        {},
	"secret":             {},
	"clientsecret":       {},
	"password":           {},
	"passwd":             {},
	"pwd":                {},
	"passphrase":         {},
	"privatekey":         {},
	"secretkey":          {},
	"credential":         {},
	"credentials":        {},
	// `session` and `sessionid` are deliberately absent. They name a WhatsApp
	// session in every WAHA envelope and an AI conversation in every n8n-style
	// memory node; redacting them sent the literal "[redacted]" to the WAHA API
	// and collapsed every conversation into one shared bucket, which is a
	// cross-tenant leak rather than a bug. `sessiontoken` stays, because that
	// one really is a credential.
	"sessiontoken":     {},
	"signature":        {},
	"xhubsignature":    {},
	"xhubsignature256": {},
	// `otp` and `pin` are deliberately absent too. Neither is ever a KilasFlow
	// credential — they arrive as the user's own words, and a WhatsApp message
	// carrying a one-time code is exactly the traffic this product handles.
}

// headerPairKeys are the two halves of the `{"name": …, "value": …}` shape that
// HTTP node configuration uses for headers. When the name is a sensitive header,
// the value beside it is the credential, and matching the *pair* is how that is
// caught without inspecting content.
//
// This replaces a prefix match on the value itself, which could not be made
// safe: any auth scheme string is also ordinary prose. "basic " destroyed a
// WhatsApp message reading "basic plan pricing?", and "bearer " and "digest "
// did the same to "bearer of this letter" and "digest of the meeting". A
// function looking only at a string has no way to tell a header value from a
// chat message, so it stopped looking.
var headerPairKeys = struct{ name, value string }{name: "name", value: "value"}

// Redact removes credential material from a payload before it is persisted or
// serialized. It preserves JSON shape — keys, arrays, and safe scalars survive
// — so an execution inspector still shows the structure of what ran.
//
// Empty and syntactically invalid payloads pass through untouched. The
// repository rejects invalid JSON with a precise error, which is better than
// silently storing something this function rewrote.
func Redact(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || !json.Valid(payload) {
		return payload
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return payload
	}
	redacted, err := json.Marshal(redactValue(value))
	if err != nil {
		// Re-marshalling a value that just round-tripped through encoding/json
		// cannot realistically fail, but withholding the payload is the only
		// safe answer if it ever does.
		return json.RawMessage(`"` + RedactedValue + `"`)
	}
	return redacted
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		// A `{"name": "Authorization", "value": "Bearer …"}` pair hides the
		// credential under an innocent key, so the pair is matched rather than
		// the value's content.
		pairIsSensitive := headerPairIsSensitive(typed)
		for key, nested := range typed {
			if isSensitiveKey(key) {
				redacted[key] = RedactedValue
				continue
			}
			if pairIsSensitive && normalizeKey(key) == headerPairKeys.value {
				redacted[key] = RedactedValue
				continue
			}
			redacted[key] = redactValue(nested)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, nested := range typed {
			redacted[index] = redactValue(nested)
		}
		return redacted
	default:
		// Scalars are returned as they are. Redaction is decided by the key a
		// value sits under, never by what the value says.
		return value
	}
}

// headerPairIsSensitive reports an object whose `name` names a sensitive header,
// so that the `value` beside it is the credential.
func headerPairIsSensitive(object map[string]any) bool {
	for key, value := range object {
		if normalizeKey(key) != headerPairKeys.name {
			continue
		}
		if name, ok := value.(string); ok && isSensitiveKey(name) {
			return true
		}
	}
	return false
}

func isSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if _, found := sensitiveKeys[normalized]; found {
		return true
	}
	// Vendor headers prefix the same names: X-Api-Key, X-Auth-Token, X-Secret.
	// Matching the remainder covers them without listing every variant.
	if after, prefixed := strings.CutPrefix(normalized, "x"); prefixed {
		_, found := sensitiveKeys[after]
		return found
	}
	return false
}

func normalizeKey(key string) string {
	var builder strings.Builder
	builder.Grow(len(key))
	for _, letter := range key {
		switch letter {
		case '-', '_', ' ', '.':
		default:
			builder.WriteRune(letter)
		}
	}
	return strings.ToLower(builder.String())
}

// RedactMap redacts one object without going through JSON.
//
// It exists for the trigger boundary. An inbound webhook body has never
// contained a KilasFlow credential — it is the caller's own data, and the
// running workflow reads it straight back out of the stored record — so
// redacting the whole payload protected nothing and destroyed everything. The
// header map is the one part that genuinely carries caller credentials, so it
// is redacted on its own and the body, query, method and path are stored as
// they arrived.
func RedactMap(object map[string]any) map[string]any {
	redacted, _ := redactValue(object).(map[string]any)
	return redacted
}

// triggerHeadersKey is the part of a trigger payload that carries a caller's
// own credentials.
const triggerHeadersKey = "headers"

// RedactTriggerHeaders redacts the `headers` object of a trigger payload and
// leaves everything else exactly as it arrived.
//
// A trigger payload is tenant data: the workflow runs on it, reading it back
// out of the stored record, so redacting the body would put "[redacted]" on the
// wire in place of the WAHA session the caller sent. The headers are the one
// part that can hold a credential, and they are never read as workflow data.
func RedactTriggerHeaders(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 || !json.Valid(payload) {
		return payload
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		// Not an object, so it carries no header map to redact.
		return payload
	}
	headers, present := decoded[triggerHeadersKey].(map[string]any)
	if !present {
		return payload
	}
	decoded[triggerHeadersKey] = RedactMap(headers)
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return payload
	}
	return encoded
}
