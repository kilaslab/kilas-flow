package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type convertedFragmentResponse struct {
	Nodes []struct {
		ID         string         `json:"id"`
		Name       string         `json:"name"`
		Type       string         `json:"type"`
		Parameters map[string]any `json:"parameters"`
	} `json:"nodes"`
	Connections []json.RawMessage     `json:"connections"`
	Unsupported []importIssueResponse `json:"unsupported"`
}

// What n8n's clipboard holds after copying a trigger and a Code node: the
// nodes, the name-keyed connections, and instance metadata.
const n8nClipboard = `{
  "nodes": [
    {"id": "a", "name": "On click", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0], "parameters": {}},
    {"id": "b", "name": "Code", "type": "n8n-nodes-base.code", "typeVersion": 2, "position": [220, 0],
     "parameters": {"jsCode": "return items;"}},
    {"id": "c", "name": "Mystery", "type": "n8n-nodes-base.noSuchNode", "typeVersion": 3, "position": [440, 0], "parameters": {}}
  ],
  "connections": {"On click": {"main": [[{"node": "Code", "type": "main", "index": 0}]]}},
  "meta": {"instanceId": "abc"}
}`

// BUG-txafja: a paste goes through the importer, and nothing is saved.
func TestConvertingPastedNodesAnswersImportNodesAndSavesNothing(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	converted := requestJSON[convertedFragmentResponse](t, handler, http.MethodPost, "/api/v1/workflows/convert",
		map[string]any{"format": "n8n", "workflow": json.RawMessage(n8nClipboard)}, http.StatusOK)

	types := map[string]string{}
	for _, node := range converted.Nodes {
		types[node.ID] = node.Type
	}
	if types["a"] != "kilasflow.manual" || types["b"] != "kilasflow.jsCode" {
		t.Errorf("types = %v, want the manual trigger and the JavaScript Code node an import makes", types)
	}
	if len(converted.Connections) != 1 {
		t.Errorf("connections = %d, want 1", len(converted.Connections))
	}
	var blocking, meta bool
	for _, issue := range converted.Unsupported {
		blocking = blocking || (issue.Severity == "blocking" && issue.NodeID == "c")
		meta = meta || issue.Field == "meta"
	}
	if !blocking {
		t.Errorf("unsupported = %#v, want the unknown node reported as blocking", converted.Unsupported)
	}
	if meta {
		t.Error("a paste reports n8n's clipboard metadata")
	}

	listing := requestJSON[[]json.RawMessage](t, handler, http.MethodGet, "/api/v1/workflows", nil, http.StatusOK)
	if len(listing) != 0 {
		t.Errorf("workflows after a conversion = %d, want none: converting saves nothing", len(listing))
	}
}

func TestConvertingSomethingThatIsNotN8nJSONNamesTheReason(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/convert",
		strings.NewReader(`{"workflow": {"nodes": [], "connections": {}}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "no nodes") {
		t.Errorf("status = %d body = %s, want 422 naming the empty node list", recorder.Code, recorder.Body.String())
	}
}
