package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// document is the slice of OpenAPI 3 this generator reads.
//
// Deliberately not a full model. A generator that parses everything and emits
// what it understands is a generator that quietly drops the rest; this one
// decodes only what it can turn into a node, and everything it meets outside
// this shape becomes a line in the generation report.
type document struct {
	OpenAPI    string              `json:"openapi"`
	Info       info                `json:"info"`
	Servers    []server            `json:"servers"`
	Paths      map[string]pathItem `json:"paths"`
	Components components          `json:"components"`
}

type info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type server struct {
	URL string `json:"url"`
}

type components struct {
	Schemas         map[string]*schema        `json:"schemas"`
	SecuritySchemes map[string]securityScheme `json:"securitySchemes"`
}

type securityScheme struct {
	Type   string `json:"type"`
	In     string `json:"in"`
	Name   string `json:"name"`
	Scheme string `json:"scheme"`
}

type pathItem map[string]json.RawMessage

// httpMethods is the closed set, in a stable order so a path with several
// operations always generates them the same way round.
var httpMethods = []string{"get", "post", "put", "patch", "delete"}

type operation struct {
	OperationID string      `json:"operationId"`
	Summary     string      `json:"summary"`
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	Deprecated  bool        `json:"deprecated"`
	Parameters  []parameter `json:"parameters"`
	RequestBody *body       `json:"requestBody"`
}

type parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Required    bool    `json:"required"`
	Description string  `json:"description"`
	Schema      *schema `json:"schema"`
}

type body struct {
	Required bool                `json:"required"`
	Content  map[string]mediaTyp `json:"content"`
}

type mediaTyp struct {
	Schema *schema `json:"schema"`
}

type schema struct {
	Ref         string             `json:"$ref"`
	Type        any                `json:"type"`
	Format      string             `json:"format"`
	Description string             `json:"description"`
	Default     any                `json:"default"`
	Enum        []any              `json:"enum"`
	Properties  map[string]*schema `json:"properties"`
	Required    []string           `json:"required"`
	Items       *schema            `json:"items"`
	OneOf       []*schema          `json:"oneOf"`
	AnyOf       []*schema          `json:"anyOf"`
	AllOf       []*schema          `json:"allOf"`
	Nullable    bool               `json:"nullable"`
}

// typeName reads the type, which OpenAPI 3.1 allows to be a list.
//
// `["string","null"]` is how 3.1 writes a nullable string, and reading it as a
// single value would silently make the property untyped.
func (s *schema) typeName() string {
	switch typed := s.Type.(type) {
	case string:
		return typed
	case []any:
		for _, entry := range typed {
			name, ok := entry.(string)
			if ok && name != "null" {
				return name
			}
		}
	}
	return ""
}

// resolve follows a `$ref` into the document's component schemas.
//
// Only local component references are followed. A remote or file reference
// would put a network fetch or a second file inside a build step, which is a
// larger decision than a generator gets to make on its own.
func (doc *document) resolve(s *schema) (*schema, error) {
	seen := map[string]bool{}
	for s != nil && s.Ref != "" {
		name, found := strings.CutPrefix(s.Ref, "#/components/schemas/")
		if !found {
			return nil, fmt.Errorf("reference %q is not a local component schema", s.Ref)
		}
		if seen[name] {
			return nil, fmt.Errorf("reference %q is circular", s.Ref)
		}
		seen[name] = true
		next, present := doc.Components.Schemas[name]
		if !present {
			return nil, fmt.Errorf("reference %q is not defined", s.Ref)
		}
		s = next
	}
	return s, nil
}

// operations lists every operation in the document in a stable order.
func (doc *document) operations() ([]resolvedOperation, error) {
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	listed := make([]resolvedOperation, 0, 128)
	for _, path := range paths {
		item := doc.Paths[path]
		for _, method := range httpMethods {
			raw, present := item[method]
			if !present {
				continue
			}
			var decoded operation
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, fmt.Errorf("%s %s: %w", strings.ToUpper(method), path, err)
			}
			listed = append(listed, resolvedOperation{Path: path, Method: strings.ToUpper(method), Operation: decoded})
		}
	}
	return listed, nil
}

type resolvedOperation struct {
	Path      string
	Method    string
	Operation operation
}
