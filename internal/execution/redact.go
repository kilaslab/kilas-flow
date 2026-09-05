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
	"session":            {},
	"sessionid":          {},
	"sessiontoken":       {},
	"signature":          {},
	"xhubsignature":      {},
	"xhubsignature256":   {},
	"otp":                {},
	"pin":                {},
}

// credentialSchemes catch header material that arrives under an innocent key,
// such as the `value` half of a `{"name":"authorization","value":"Bearer …"}`
// pair. Matching the scheme prefix keeps ordinary prose out of the net.
var credentialSchemes = []string{"basic ", "bearer ", "digest ", "negotiate ", "ntlm ", "hawk ", "aws4-hmac-sha256 "}

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
		for key, nested := range typed {
			if isSensitiveKey(key) {
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
	case string:
		if looksLikeCredential(typed) {
			return RedactedValue
		}
		return typed
	default:
		return value
	}
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

func looksLikeCredential(value string) bool {
	lowered := strings.ToLower(value)
	for _, scheme := range credentialSchemes {
		if strings.HasPrefix(lowered, scheme) {
			return true
		}
	}
	return false
}
