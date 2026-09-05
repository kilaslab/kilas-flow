package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/expression"
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

// ExpressionGrammarOutput is the expression surface the editor validates
// against.
//
// It is served rather than duplicated in the client. The editor kept its own
// hardcoded list of roots and its own error message, so every root added on the
// server was rejected in the editor until somebody remembered to edit one
// specific Svelte file.
type ExpressionGrammarOutput struct {
	Body ExpressionGrammar
}

// ExpressionGrammar names everything an expression may use.
type ExpressionGrammar struct {
	// Roots are the values an expression may start from. `$(` is the prefix of
	// the `$('Node Name')` form, which takes a quoted argument rather than
	// being a plain name.
	Roots []string `json:"roots"`
	// Functions is the closed callable allowlist. Anything not here is a parse
	// error, so an expression cannot reach the host, the filesystem, the
	// network, or another tenant's data.
	Functions []string `json:"functions"`
}

// Register wires node metadata operations onto the API group.
func (handler *NodeTypes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-expression-grammar",
		Method:      http.MethodGet,
		Path:        "/expression-grammar",
		Summary:     "Describe the expression grammar",
		Description: "Returns the roots and functions an expression may use, so the editor validates against the server rather than a copy that drifts from it.",
		Tags:        []string{"Nodes"},
	}, handler.Grammar)
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

// Grammar returns the expression surface the server actually accepts.
func (handler *NodeTypes) Grammar(context.Context, *struct{}) (*ExpressionGrammarOutput, error) {
	return &ExpressionGrammarOutput{Body: ExpressionGrammar{
		Roots:     expression.Roots(),
		Functions: expression.FunctionNames(),
	}}, nil
}
