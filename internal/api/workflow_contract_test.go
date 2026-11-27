package api_test

import (
	"net/http"
	"testing"
)

// The contract route answers under the tenant's key and reads as unknown to
// another tenant.
func TestWorkflowContractRoute(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme", "globex")
	id := server.createWorkflowAs(t, keys["acme"], "Callable")

	contract := server.callJSON(t, http.MethodGet, "/api/v1/workflows/"+id+"/contract", keys["acme"], nil, http.StatusOK)
	if contract["workflowId"] != id || contract["source"] != "draft" {
		t.Fatalf("contract = %v, want the draft of %s", contract, id)
	}
	input, _ := contract["input"].(map[string]any)
	if input["declared"] != false {
		t.Fatalf("input = %v, want undeclared for a workflow without a sub-workflow trigger", input)
	}
	if recorder := server.call(t, http.MethodGet, "/api/v1/workflows/"+id+"/contract", keys["globex"], nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("another tenant got %d, want 404", recorder.Code)
	}
}
