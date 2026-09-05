package workflow

import "github.com/danielgtaylor/huma/v2"

// Schema describes TypeVersion to the OpenAPI generator as the JSON number it
// actually is.
//
// Without this, huma reflects on the Go struct and publishes `typeVersion` as
// an object, which both breaks every existing client and rejects the number
// every existing document contains. The wire contract is unchanged by the type
// existing, and this is what keeps that true at the API boundary as well as in
// encoding/json.
//
// It is the one place this package knows about the HTTP layer. That is a real
// cost, and the alternative — registering the schema wherever the API is
// assembled — was worse: the mapping would live away from the type it
// describes, and the next place the type is exposed would silently publish an
// object again.
func (TypeVersion) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:        "number",
		Format:      "double",
		Description: "Node type version. A decimal such as 1, 4.2, or a YYYYMM value such as 202502. Omit it to use the registered default.",
		Examples:    []any{1, 4.2, 202502},
	}
}

var _ huma.SchemaProvider = TypeVersion{}
