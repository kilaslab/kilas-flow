package credentials

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/execution"
)

// minimumScrubbedLength is the shortest secret value scrubbed out of text.
//
// Replacing a one- or two-character value everywhere it occurs would turn
// every message into a row of markers without protecting anything: a secret
// that short is guessed, not leaked. The community-node sidecar's scrubber
// draws the same line.
const minimumScrubbedLength = 4

// SecretValues lists the values of a resolved credential that must never
// appear in text the runtime keeps: its secret fields, in every form a request
// may have written them.
//
// The type's own Secrets decide which fields those are, so a base URL or a
// header name — which appear in ordinary diagnostics — is left alone. A type
// this server does not know has every field treated as secret, because there
// is no declaration to trust. Each value is listed as written, trimmed, and
// escaped the way a query value or a path segment escapes it, since that is
// the form a URL-borne secret takes inside an error. A secret that is a JSON
// object, as an httpCustomAuth template is, contributes every string inside
// it, which is where the header and query values it sends live.
//
// The list is longest first, so a secret that contains another is replaced
// whole rather than leaving its remainder behind.
func SecretValues(typeID string, fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	if credentialType, known := Default().Get(typeID); known {
		keys = append(keys, credentialType.Secrets...)
	} else {
		for key := range fields {
			keys = append(keys, key)
		}
	}
	seen := map[string]bool{}
	values := make([]string, 0, len(keys))
	add := func(value string) {
		if len(value) < minimumScrubbedLength || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	var collect func(value string)
	collect = func(value string) {
		for _, form := range []string{value, strings.TrimSpace(value), url.QueryEscape(value), url.PathEscape(value)} {
			add(form)
		}
		var decoded any
		if strings.HasPrefix(strings.TrimSpace(value), "{") && json.Unmarshal([]byte(value), &decoded) == nil {
			for _, nested := range jsonStrings(decoded) {
				collect(nested)
			}
		}
	}
	for _, key := range keys {
		collect(fields[key])
	}
	sort.SliceStable(values, func(left, right int) bool { return len(values[left]) > len(values[right]) })
	return values
}

// jsonStrings lists every string value inside a decoded JSON document.
func jsonStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case map[string]any:
		var found []string
		for _, nested := range typed {
			found = append(found, jsonStrings(nested)...)
		}
		return found
	case []any:
		var found []string
		for _, nested := range typed {
			found = append(found, jsonStrings(nested)...)
		}
		return found
	default:
		return nil
	}
}

// ScrubText replaces every occurrence of each known secret value in text with
// the execution redaction marker.
//
// The values are known exactly — they were just resolved — so matching them
// cannot misfire on ordinary text the way guessing at secrets from their shape
// did. An empty value is skipped rather than matched everywhere.
func ScrubText(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			text = strings.ReplaceAll(text, secret, execution.RedactedValue)
		}
	}
	return text
}
