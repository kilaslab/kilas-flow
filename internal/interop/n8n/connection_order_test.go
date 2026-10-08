package n8n_test

import (
	"strings"
	"testing"
)

// BUG-5xkexq: n8n walks the connections into one input in the order its
// connections object lists their source nodes, and `$('X')` reads the output
// of X that walk meets first. The import keeps that order in the document's
// connections, which the compiled graph and the walk then follow, rather than
// sorting the sources by name.
//
// Zeta is listed before Alpha, though it sorts after it, and both feed Join's
// only input.
const connectionOrderFixture = `{
  "name": "Two paths into one input",
  "nodes": [
    {"id": "trigger", "name": "Start", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "parameters": {}},
    {"id": "if", "name": "If", "type": "n8n-nodes-base.if", "typeVersion": 2,
     "parameters": {"conditions": {"conditions": [
       {"id": "1", "leftValue": "={{ $json.ok }}", "rightValue": true,
        "operator": {"type": "boolean", "operation": "true", "singleValue": true}}
     ]}}},
    {"id": "alpha", "name": "Alpha", "type": "n8n-nodes-base.noOp", "typeVersion": 1},
    {"id": "zeta", "name": "Zeta", "type": "n8n-nodes-base.noOp", "typeVersion": 1},
    {"id": "join", "name": "Join", "type": "n8n-nodes-base.noOp", "typeVersion": 1}
  ],
  "connections": {
    "Zeta": {"main": [[{"node": "Join", "type": "main", "index": 0}]]},
    "Start": {"main": [[{"node": "If", "type": "main", "index": 0}]]},
    "If": {"main": [
      [{"node": "Alpha", "type": "main", "index": 0}],
      [{"node": "Zeta", "type": "main", "index": 0}]
    ]},
    "Alpha": {"main": [[{"node": "Join", "type": "main", "index": 0}]]}
  }
}`

func TestImportKeepsTheOrderN8nListsConnectionsIn(t *testing.T) {
	t.Parallel()

	result := importFixture(t, connectionOrderFixture)
	var order []string
	for _, connection := range result.Document.Connections {
		order = append(order, connection.Source.NodeID+"→"+connection.Target.NodeID)
	}
	const want = "zeta→join trigger→if if→alpha if→zeta alpha→join"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("connections = %s, want %s", got, want)
	}
}
