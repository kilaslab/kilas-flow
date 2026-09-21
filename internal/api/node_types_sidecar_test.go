package api_test

import (
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// TestSidecarNodesAreTaggedSidecarInTheNodeTypesResponse is the catalogue half
// of the ticket: a node that runs outside this process appears in
// GET /api/v1/node-types tagged `sidecar`, distinct from `builtin` and `pack`,
// so the editor can say where a node came from.
//
// The definition is registered by hand rather than loaded from a package: the
// tag is the API's to serve, and this test should not need a Node process to
// check it.
func TestSidecarNodesAreTaggedSidecarInTheNodeTypesResponse(t *testing.T) {
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	pack := node.Definition{
		Type: "pack.acme.post", Version: workflow.V(1), DisplayName: "Acme Post", Category: "Pack",
		ExecutorID: nodes.RoutingExecutorID, Group: []node.NodeGroup{node.GroupOutput},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	if err := registry.RegisterFrom(node.SourcePack, pack); err != nil {
		t.Fatalf("RegisterFrom(pack) error = %v", err)
	}
	community := node.Definition{
		Type: "sidecar.kf-fixture-nodes.fixtureGreet", Version: workflow.V(1),
		DisplayName: "Fixture Greet", Description: "Greets every item", Category: sidecarnode.Category,
		ExecutorID: sidecarnode.ExecutorID, Group: []node.NodeGroup{node.GroupTransform},
		Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{{
			Key: "name", Label: "Name", Kind: node.PropertyString, Default: "there",
		}},
	}
	if err := registry.RegisterFrom(node.SourceSidecar, community); err != nil {
		t.Fatalf("RegisterFrom(sidecar) error = %v", err)
	}

	handler := newTestServer(t, api.Deps{DB: stubPinger{}, NodeRegistry: registry})
	definitions := requestJSON[[]struct {
		Type       string `json:"type"`
		Source     string `json:"source"`
		ExecutorID string `json:"executorId"`
	}](t, handler, http.MethodGet, "/api/v1/node-types", nil, http.StatusOK)

	sources := map[string]string{}
	for _, definition := range definitions {
		sources[definition.Type] = definition.Source
		if definition.ExecutorID != "" {
			t.Errorf("%s leaked its executor binding: %q", definition.Type, definition.ExecutorID)
		}
	}
	if got, want := sources[community.Type], string(node.SourceSidecar); got != want {
		t.Errorf("%s is tagged %q, want %q", community.Type, got, want)
	}
	if got, want := sources[pack.Type], string(node.SourcePack); got != want {
		t.Errorf("%s is tagged %q, want %q", pack.Type, got, want)
	}
	if got, want := sources["kilasflow.manual"], string(node.SourceBuiltin); got != want {
		t.Errorf("a built-in node is tagged %q, want %q", got, want)
	}

	seen := map[string]bool{}
	for _, source := range sources {
		seen[source] = true
	}
	for _, want := range []string{string(node.SourceBuiltin), string(node.SourcePack), string(node.SourceSidecar)} {
		if !seen[want] {
			t.Errorf("the catalogue serves no %s node: %v", want, seen)
		}
	}
}
