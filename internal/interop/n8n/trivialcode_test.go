package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
)

const inboundNormalizeJS = `const root = $input.first().json;
const p = (root.body && typeof root.body === 'object') ? root.body : root;
const payload = p.payload || {};
const chatId = payload.chat_id || '';
return [{ json: {
event: p.event || 'message',
device_id: p.device_id || '',
message_id: payload.id || '',
chat_id: chatId,
sender: payload.from || '',
sender_name: payload.from_name || '',
body: payload.body || '',
is_group: chatId.endsWith('@g.us')
} }];`

func TestTrivialNormalizeMessageImportsAsSet(t *testing.T) {
	payload := []byte(`{
	  "name":"Inbound",
	  "nodes":[
	    {"id":"w","name":"Webhook","type":"n8n-nodes-base.webhook","typeVersion":2,
	     "parameters":{"path":"in","httpMethod":"POST"}},
	    {"id":"c","name":"Normalize Message","type":"n8n-nodes-base.code","typeVersion":2,
	     "parameters":{"mode":"runOnceForAllItems","jsCode":` + backtickJSON(inboundNormalizeJS) + `}}
	  ],
	  "connections":{"Webhook":{"main":[[{"node":"Normalize Message","type":"main","index":0}]]}}
	}`)
	result, err := n8n.Import(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range result.Document.Nodes {
		if node.Name != "Normalize Message" {
			continue
		}
		found = true
		if node.Type != "kilasflow.set" {
			t.Fatalf("Normalize Message type = %q, want kilasflow.set", node.Type)
		}
		assignments, _ := node.Parameters["assignments"].(map[string]any)
		rows, _ := assignments["assignments"].([]any)
		if len(rows) < 7 {
			t.Fatalf("assignments = %#v, want the inbound field map", rows)
		}
	}
	if !found {
		t.Fatal("Normalize Message missing")
	}
	blocking := 0
	for _, u := range result.Unsupported {
		if u.Severity == n8n.SeverityBlocking && u.NodeName == "Normalize Message" {
			blocking++
			t.Errorf("unexpected blocking issue: %+v", u)
		}
	}
	if blocking != 0 {
		t.Fatalf("blocking issues on Normalize Message: %d", blocking)
	}
}

func TestNonTrivialCodeStaysForeign(t *testing.T) {
	payload := []byte(`{
	  "name":"X",
	  "nodes":[
	    {"id":"c","name":"Gate","type":"n8n-nodes-base.code","typeVersion":2,
	     "parameters":{"jsCode":"const a=[]; for (const x of items) { a.push(x); } return a;"}}
	  ],
	  "connections":{}
	}`)
	result, err := n8n.Import(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Document.Nodes[0].Type != n8n.ForeignCodeNodeType {
		t.Fatalf("type = %q, want foreignCode", result.Document.Nodes[0].Type)
	}
}

func backtickJSON(s string) string {
	// JSON-encode as a string literal without importing encoding/json in the helper body twice.
	b := &strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
