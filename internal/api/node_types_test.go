package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func TestNodeTypesServesTheRegisteredCatalogue(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	handler := newTestServer(t, api.Deps{DB: stubPinger{}, NodeRegistry: registry})

	recorder := get(t, handler, "/api/v1/node-types")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /node-types status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}

	var definitions []node.Definition
	if err := json.NewDecoder(recorder.Body).Decode(&definitions); err != nil {
		t.Fatalf("decode node catalogue = %v", err)
	}
	if got, want := len(definitions), 5; got != want {
		t.Fatalf("node catalogue length = %d, want %d", got, want)
	}
	if got, want := definitions[0].Type, "kilasflow.httpRequest"; got != want {
		t.Errorf("first catalogue type = %q, want %q", got, want)
	}
	if definitions[0].ExecutorID != "" {
		t.Errorf("executor binding leaked in API response = %q", definitions[0].ExecutorID)
	}
}
