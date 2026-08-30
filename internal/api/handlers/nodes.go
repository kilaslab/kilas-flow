package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/node"
)

// NodeTypes exposes the server-owned node catalogue to API clients.
type NodeTypes struct {
	registry *node.Registry
}

// NodeTypesOutput is a stable, metadata-only node catalogue. Executor bindings
// are private to the registry and omitted by Definition's JSON representation.
type NodeTypesOutput struct {
	Body []node.Definition
}

// NewNodeTypes constructs the node catalogue handler.
func NewNodeTypes(registry *node.Registry) *NodeTypes {
	return &NodeTypes{registry: registry}
}

// Register wires node metadata operations onto the API group.
func (handler *NodeTypes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-node-types",
		Method:      http.MethodGet,
		Path:        "/node-types",
		Summary:     "List supported node types",
		Description: "Returns the server-defined, versioned node catalogue used by the workflow editor and compiler.",
		Tags:        []string{"Nodes"},
	}, handler.List)
}

// List returns definitions in the registry's stable type/version order.
func (handler *NodeTypes) List(context.Context, *struct{}) (*NodeTypesOutput, error) {
	if handler.registry == nil {
		return nil, huma.Error503ServiceUnavailable("node catalogue unavailable")
	}
	return &NodeTypesOutput{Body: handler.registry.List()}, nil
}
