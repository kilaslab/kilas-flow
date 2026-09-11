package n8n

import (
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// tryTrivialCodeToSet recognises a Code node that only field-normalises the
// incoming item — the shape of AGL's "Normalize Message" — and rewrites it to
// kilasflow.set assignments so the workflow can activate without a Go port.
//
// Anything with statement-level control flow, network access, or a non-literal
// return shape is refused: a wrong Set is worse than an honest foreignCode.
func tryTrivialCodeToSet(node Node) (map[string]any, bool) {
	source := stringParameter(node.Parameters, "jsCode")
	if source == "" {
		return nil, false
	}
	lang := strings.ToLower(stringParameter(node.Parameters, "language"))
	if lang == "python" || lang == "pythonnative" || stringParameter(node.Parameters, "pythonCode") != "" {
		return nil, false
	}
	if !looksLikeTrivialFieldNormalize(source) {
		return nil, false
	}
	if assignments, ok := parseInboundNormalizeMessage(source); ok {
		return map[string]any{
			"assignments": map[string]any{"assignments": assignments},
		}, true
	}
	return nil, false
}

func looksLikeTrivialFieldNormalize(source string) bool {
	lower := strings.ToLower(source)
	for _, banned := range []string{
		"for (", "for(", "while (", "while(", "await ", "require(",
		"fetch(", "axios", "eval(", "function ", "switch ", "try {", "catch(",
	} {
		if strings.Contains(lower, banned) {
			return false
		}
	}
	if !strings.Contains(source, "return [{") && !strings.Contains(source, "return[{") {
		return false
	}
	if !strings.Contains(source, "json:") && !strings.Contains(source, "json :") {
		return false
	}
	return true
}

func parseInboundNormalizeMessage(source string) ([]any, bool) {
	needed := []string{"payload", "chat_id", "event", "device_id", "message_id", "sender", "is_group"}
	for _, n := range needed {
		if !strings.Contains(source, n) {
			return nil, false
		}
	}
	if !strings.Contains(source, ".body") {
		return nil, false
	}
	assignments := []any{
		setAssign("e1", "event", "string", "{{ $json.body.event }}"),
		setAssign("e2", "device_id", "string", "{{ $json.body.device_id }}"),
		setAssign("e3", "message_id", "string", "{{ $json.body.payload.id }}"),
		setAssign("e4", "chat_id", "string", "{{ $json.body.payload.chat_id }}"),
		setAssign("e5", "sender", "string", "{{ $json.body.payload.from }}"),
		setAssign("e6", "sender_name", "string", "{{ $json.body.payload.from_name }}"),
		setAssign("e7", "body", "string", "{{ $json.body.payload.body }}"),
		setAssign("e8", "is_group", "boolean", `{{ $json.body.payload.chat_id.endsWith("@g.us") }}`),
	}
	return assignments, true
}

func setAssign(id, name, typ, expr string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "type": typ,
		"value": map[string]any{"mode": "expression", "value": expr},
	}
}

// applyTrivialCodeRewrite swaps a foreignCode conversion for Set when the
// source is a recognised field-normalize. On success it replaces *issues with
// a single lossy note (caller drops prior blocking jsCode issues for this node).
func applyTrivialCodeRewrite(converted *workflow.Node, node Node, issues *[]Unsupported) bool {
	if converted.Type != ForeignCodeNodeType {
		return false
	}
	params, ok := tryTrivialCodeToSet(node)
	if !ok {
		return false
	}
	converted.Type = "kilasflow.set"
	converted.TypeVersion = workflow.V(1)
	converted.Parameters = params
	*issues = []Unsupported{{
		Severity: SeverityLossy,
		NodeName: converted.Name,
		NodeID:   converted.ID,
		Field:    "jsCode",
		Reason: "this Code node only field-normalised the incoming item and was rewritten to kilasflow.set on import; " +
			"review the assignments if the original JavaScript had behaviour Set cannot express",
	}}
	return true
}
