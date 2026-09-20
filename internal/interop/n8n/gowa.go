package n8n

import (
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// DefaultGOWABaseURL is the host used when an imported GOWA node has no
// credential carrying a host (credentials never import). Lab and Compose
// commonly expose GOWA on :3000; operators rewrite the URL after import.
const DefaultGOWABaseURL = "http://127.0.0.1:3000"

// gowaToHTTP maps @aldinokemal2104/n8n-nodes-gowa.gowa onto kilasflow.httpRequest
// for the operations this suite actually uses (and a few close cousins).
//
// Unsupported resource/operation combinations still become an HTTP node aimed
// at a documented placeholder path rather than kilasflow.unsupported, so the
// workflow can be activated once the URL is corrected — with a lossy issue
// naming the gap.
func gowaToHTTP(node Node) (map[string]any, []Unsupported) {
	resource := strings.TrimSpace(stringParameter(node.Parameters, "resource"))
	operation := strings.TrimSpace(stringParameter(node.Parameters, "operation"))
	if operation == "" {
		operation = strings.TrimSpace(stringParameter(node.Parameters, "operationApp"))
	}
	issues := []Unsupported{}

	method, path, bodyKeys, known := gowaRoute(resource, operation)
	url := strings.TrimRight(DefaultGOWABaseURL, "/") + path
	params := map[string]any{
		"method": method,
		"url":    url,
	}
	if method == "POST" || method == "PUT" || method == "PATCH" {
		params["sendBody"] = true
		params["specifyBody"] = "json"
		params["jsonBody"] = gowaJSONBody(bodyKeys, node)
	}
	if !known {
		// Unreachable through Import, which refuses the pair before this runs
		// (see refuseUnknownGOWAOperation). Kept as a belt-and-braces path for
		// any caller that reaches the translator directly, and blocking rather
		// than lossy: this is the case where the imported node would send a
		// *different* GOWA call.
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking,
			Field:    "resource",
			Reason: fmt.Sprintf(
				"GOWA resource %q operation %q has no dedicated KilasFlow mapping; "+
					"the fallback HTTP call %s %s would not be the call this node made — "+
					"replace the node with the GOWA REST call you need (see packs/gowa/GAPS.md)",
				resource, operation, method, url),
		})
	} else {
		issues = append(issues, Unsupported{
			Severity: SeverityLossy,
			Field:    "credentials",
			Reason: "GOWA host/auth credentials do not import; the URL defaults to " + DefaultGOWABaseURL +
				" and must be pointed at your GOWA instance (and authenticated) before run",
		})
	}
	params["_gowaResource"] = resource
	params["_gowaOperation"] = operation
	return params, issues
}

func gowaRoute(resource, operation string) (method, path string, bodyKeys []string, known bool) {
	res := strings.ToLower(resource)
	op := strings.ToLower(operation)
	switch res {
	case "app", "":
		switch op {
		case "", "devices", "device", "listdevices":
			return "GET", "/app/devices", nil, true
		case "status":
			return "GET", "/app/status", nil, true
		case "info":
			return "GET", "/app/info", nil, true
		case "login":
			return "GET", "/app/login", nil, true
		case "logout":
			return "GET", "/app/logout", nil, true
		case "reconnect":
			return "GET", "/app/reconnect", nil, true
		default:
			return "GET", "/app/devices", nil, false
		}
	case "send", "message":
		switch op {
		case "", "message", "text", "sendtext", "send-message":
			return "POST", "/send/message", []string{"phone", "message"}, true
		case "link":
			return "POST", "/send/link", []string{"phone", "link", "caption"}, true
		case "presence":
			return "POST", "/send/presence", []string{"type"}, true
		case "chatpresence", "chat-presence", "chat_presence":
			return "POST", "/send/chat-presence", []string{"phone", "action"}, true
		default:
			return "POST", "/send/message", []string{"phone", "message"}, false
		}
	case "device", "devices":
		switch op {
		case "", "list", "get", "info":
			return "GET", "/devices", nil, true
		default:
			return "GET", "/devices", nil, false
		}
	default:
		return "GET", "/app/devices", nil, false
	}
}

func gowaJSONBody(keys []string, node Node) any {
	if len(keys) == 0 {
		return map[string]any{}
	}
	obj := map[string]any{}
	for _, k := range keys {
		if v, ok := node.Parameters[k]; ok {
			obj[k] = fromN8NValue(v)
			continue
		}
		alt := map[string][]string{
			"phone":   {"phoneNumber", "to", "chatId", "chat_id"},
			"message": {"text", "body", "caption"},
		}
		found := false
		for _, a := range alt[k] {
			if v, ok := node.Parameters[a]; ok {
				obj[k] = fromN8NValue(v)
				found = true
				break
			}
		}
		if !found {
			obj[k] = map[string]any{"mode": "expression", "value": "{{ $json." + k + " }}"}
		}
	}
	return obj
}

func gowaToN8N(node workflow.Node) (map[string]any, []Lossy) {
	params := map[string]any{}
	if r, ok := node.Parameters["_gowaResource"].(string); ok && r != "" {
		params["resource"] = r
	} else {
		params["resource"] = "app"
	}
	if o, ok := node.Parameters["_gowaOperation"].(string); ok {
		params["operation"] = o
	}
	return params, []Lossy{{
		Field:  "httpRequest",
		Reason: "exported back as the GOWA community node with resource/operation only; HTTP URL/body details were not reconstituted",
	}}
}

// refuseUnknownGOWAOperation names why a GOWA node must not import as HTTP.
//
// The fallback route used to be "POST /send/message" or "GET /app/devices",
// reported as lossy — so a node written to send an image, or to revoke a
// message, activated and then sent a text message instead. An automation that
// messages real contacts with the wrong content is not a fidelity gap; it is
// the importer doing something nobody asked for, and the honest answer is the
// unsupported placeholder.
func refuseUnknownGOWAOperation(node Node) string {
	resource := strings.TrimSpace(stringParameter(node.Parameters, "resource"))
	operation := strings.TrimSpace(stringParameter(node.Parameters, "operation"))
	if operation == "" {
		operation = strings.TrimSpace(stringParameter(node.Parameters, "operationApp"))
	}
	if _, _, _, known := gowaRoute(resource, operation); known {
		return ""
	}
	return fmt.Sprintf("GOWA's %q / %q operation has no KilasFlow mapping, and the generic HTTP "+
		"fallback would call a different GOWA endpoint than this node called — sending different "+
		"content to real contacts. Replace this node with the GOWA REST call you need (see "+
		"packs/gowa/GAPS.md).", resource, operation)
}

func isGOWANodeType(t string) bool {
	switch t {
	case "@aldinokemal2104/n8n-nodes-gowa.gowa", "n8n-nodes-gowa.gowa":
		return true
	default:
		return false
	}
}
