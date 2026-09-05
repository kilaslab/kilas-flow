package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"fmt"
	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// NodeTypes exposes the server-owned node catalogue to API clients.
type NodeTypes struct {
	registry *node.Registry
	tenants  TenantResolver
	options  *loadoptions.Resolver
	// credentials builds a tenant-scoped resolver, so a request naming another
	// tenant's credential resolves to nothing rather than to a secret.
	credentials func(repository.TenantScope) loadoptions.CredentialResolver
	// availability reports which nodes this deployment cannot run, keyed by
	// node type. Nil means everything registered can run.
	availability func() map[string]string
}

// WithOptionLoading enables the load-options endpoint.
func (handler *NodeTypes) WithOptionLoading(
	tenants TenantResolver,
	resolver *loadoptions.Resolver,
	credentials func(repository.TenantScope) loadoptions.CredentialResolver,
) *NodeTypes {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	handler.tenants = tenants
	handler.options = resolver
	handler.credentials = credentials
	return handler
}

// NodeTypesOutput is a stable, metadata-only node catalogue. Executor bindings
// are private to the registry and omitted by Definition's JSON representation.
type NodeTypesOutput struct {
	Body []node.Definition
}

// NewNodeTypes constructs the node catalogue handler.
func NewNodeTypes(registry *node.Registry) *NodeTypes {
	return &NodeTypes{registry: registry, tenants: defaultTenantResolver{}}
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
		OperationID: "get-node-icon",
		Method:      http.MethodGet,
		Path:        "/node-types/{type}/icon",
		Summary:     "Serve a node's icon",
		Description: "Returns the artwork a node ships. Only a registered node type that declares a served icon answers; everything else is 404.",
		Tags:        []string{"Nodes"},
	}, handler.Icon)
	huma.Register(api, huma.Operation{
		OperationID: "load-node-property-options",
		Method:      http.MethodPost,
		Path:        "/node-types/{type}/load-options",
		Summary:     "Load a property's selectable values",
		Description: "Resolves the options for a property whose valid values live on the customer's own service. The loader is taken from the registered definition, never from the request.",
		Tags:        []string{"Nodes"},
	}, handler.LoadOptions)
	huma.Register(api, huma.Operation{
		OperationID: "load-node-property-schema",
		Method:      http.MethodPost,
		Path:        "/node-types/{type}/load-schema",
		Summary:     "Load a resource mapper's columns",
		Description: "Resolves the column list a resource mapper maps onto, with each column's type, required flag and match eligibility. A sibling of load-options rather than a widening of it: an option is {label, value} and a column is not.",
		Tags:        []string{"Nodes"},
	}, handler.LoadSchema)
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
	definitions := handler.registry.List()
	if handler.availability == nil {
		return &NodeTypesOutput{Body: definitions}, nil
	}
	// Stamped here rather than stored on the definition: the catalogue is
	// static and assembled once at startup, while whether a node can run is a
	// property of this deployment right now.
	unavailable := handler.availability()
	for index := range definitions {
		if reason, blocked := unavailable[definitions[index].Type]; blocked {
			definitions[index].Unavailable = reason
		}
	}
	return &NodeTypesOutput{Body: definitions}, nil
}

// WithAvailability reports which nodes this deployment cannot run.
//
// A function rather than a map, because the answer can change while the process
// is up — a compiler sidecar that comes back, a credential store that is
// configured after boot — and a snapshot taken at composition would go stale
// in exactly the direction that misleads.
func (handler *NodeTypes) WithAvailability(report func() map[string]string) *NodeTypes {
	handler.availability = report
	return handler
}

// Grammar returns the expression surface the server actually accepts.
func (handler *NodeTypes) Grammar(context.Context, *struct{}) (*ExpressionGrammarOutput, error) {
	return &ExpressionGrammarOutput{Body: ExpressionGrammar{
		Roots:     expression.Roots(),
		Functions: expression.FunctionNames(),
	}}, nil
}

// loadOptionsInput is a partially configured node straight from a browser.
//
// It is untrusted input in the strongest sense: the type, the property and
// every parameter value are attacker-controlled, and the server then makes an
// outbound request shaped by them. Three rules follow, and they are enforced
// below rather than assumed — validate against the registry and take the loader
// from the registered definition, never from the request; treat every parameter
// value as data to be escaped; and resolve the credential by ID under the
// caller's tenant.
type loadOptionsInput struct {
	Type string `path:"type"`
	Body struct {
		Version  string `json:"version,omitempty" doc:"Node type version. Omit for the registered default."`
		Property string `json:"property" doc:"The property whose options to load."`
		// Mode names which of a resource locator's modes is asking. A locator
		// carries a loader per mode rather than one for the property, because
		// "from list" searches and "by ID" does not.
		Mode string `json:"mode,omitempty" doc:"For a resource locator, the mode whose list to load."`
		// Parameters is the node as currently configured in the editor.
		Parameters map[string]any `json:"parameters,omitempty"`
		// CredentialID names a credential in the caller's own tenant. Inline
		// credential fields are never accepted.
		CredentialID string `json:"credentialId,omitempty"`
		// WorkflowID bounds an internal lookup for an embed session.
		WorkflowID string `json:"workflowId,omitempty"`
	}
}

// LoadOptionsResource is a resolved option list.
type LoadOptionsResource struct {
	Options []loadoptions.Option `json:"options"`
	Reason  string               `json:"reason,omitempty" doc:"Why the list is empty, when it is empty for a reason the user can act on."`
}

type loadOptionsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LoadOptionsResource
}

// LoadOptions resolves a property's selectable values.
func (handler *NodeTypes) LoadOptions(ctx context.Context, input *loadOptionsInput) (*loadOptionsOutput, error) {
	if handler.registry == nil || handler.options == nil {
		return nil, huma.Error503ServiceUnavailable("the node catalogue is unavailable")
	}

	declared, err := handler.declaredProperty(input)
	if err != nil {
		return nil, err
	}

	loader := declared.LoadOptions
	if declared.Kind == node.PropertyResourceLocator {
		// From the declared mode, never from the request: the request says
		// *which* mode is asking, and the server decides what that mode may
		// load.
		loader = nil
		for _, mode := range declared.Modes {
			if mode.Name == input.Body.Mode {
				loader = mode.LoadOptions
				break
			}
		}
		if loader == nil {
			return nil, huma.Error422UnprocessableEntity("that resource locator has no list for the mode you asked for")
		}
	}
	if loader == nil {
		return nil, huma.Error422UnprocessableEntity("that property's options are fixed and do not need loading")
	}

	for _, key := range loader.DependsOn {
		// A dependency holding an expression cannot be resolved: there is no
		// item to evaluate it against, because there is no execution. Refusing
		// beats guessing — a guessed value produces a wrong list, and
		// evaluating against an empty item produces a confidently wrong one.
		if property.ExpressionMarker(input.Body.Parameters[key]) {
			return &loadOptionsOutput{
				CacheControl: "no-store",
				Body: LoadOptionsResource{
					Options: []loadoptions.Option{},
					Reason:  fmt.Sprintf("%s depends on an expression, which cannot be resolved while editing", input.Body.Property),
				},
			}, nil
		}
	}
	scope, err := handler.scopeFor(ctx, input, loader.DependsOn)
	if err != nil {
		return nil, err
	}
	tenant := handler.tenants.Resolve(ctx)

	result, err := handler.options.Load(ctx, *loader, scope, input.Body.CredentialID, handler.credentials(tenant))
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	if result.Options == nil {
		result.Options = []loadoptions.Option{}
	}
	return &loadOptionsOutput{
		// So the browser does not refetch on every focus.
		CacheControl: "private, max-age=30",
		Body:         LoadOptionsResource{Options: result.Options, Reason: result.Reason},
	}, nil
}

// declaredProperty resolves the property a request names, from the registered
// definition and never from the request.
func (handler *NodeTypes) declaredProperty(input *loadOptionsInput) (*node.PropertyDefinition, error) {
	version := workflow.TypeVersion{}
	if input.Body.Version != "" {
		parsed, err := workflow.ParseTypeVersion(input.Body.Version)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("the node type version is not a decimal number")
		}
		version = parsed
	}
	definition, found := handler.registry.Resolve(input.Type, version)
	if !found {
		return nil, huma.Error404NotFound("that node type is not registered")
	}
	for index, candidate := range definition.Parameters {
		if candidate.Key == input.Body.Property {
			return &definition.Parameters[index], nil
		}
	}
	return nil, huma.Error404NotFound("that node type has no such property")
}

// scopeFor builds the tenancy and dependency scope both loaders run under.
//
// One function for both endpoints, deliberately. An internal loader constructs
// no request, so nothing the egress policy defends applies to it, and the embed
// middleware allows the whole /node-types/ subtree on read scope without ever
// reading a body — this is the only check between an embedded editor and
// everything a loader can see across the tenant. Two copies of it is how two
// gates drift apart.
func (handler *NodeTypes) scopeFor(ctx context.Context, input *loadOptionsInput, dependsOn []string) (loadoptions.Scope, error) {
	dependencies := make(map[string]string, len(dependsOn))
	for _, key := range dependsOn {
		value, present := input.Body.Parameters[key]
		if !present || value == nil || property.ExpressionMarker(value) {
			continue
		}
		dependencies[key] = locatorDependency(value)
	}
	scope := loadoptions.Scope{TenantID: handler.tenants.Resolve(ctx).ID, Dependencies: dependencies}
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		scope.WorkflowID = input.Body.WorkflowID
		return scope, nil
	}
	// Refused rather than silently narrowed. A request naming another workflow
	// is either a mistake or a probe, and answering it with this session's own
	// list would tell the caller nothing about which one it got — which is the
	// reading that turns a bug into a slow leak.
	if input.Body.WorkflowID != "" && input.Body.WorkflowID != session.WorkflowID {
		return loadoptions.Scope{}, huma.Error403Forbidden(fmt.Sprintf(
			"this embed session is scoped to workflow %s and cannot load options for %s",
			session.WorkflowID, input.Body.WorkflowID))
	}
	scope.WorkflowID = session.WorkflowID
	return scope, nil
}

// locatorDependency renders a dependency value for a loader's path.
//
// A resource locator is a dependency as often as it is a target — a Table
// locator depends on the Schema one — and the stored form is an object. Sending
// it through fmt.Sprint would put `map[__rl:true mode:list value:public]` into
// the endpoint path, which is a lookup that quietly returns nothing.
func locatorDependency(value any) string {
	if locator, ok := property.ReadLocator(value); ok {
		return fmt.Sprint(locator.Value)
	}
	return fmt.Sprint(value)
}

// MapperColumn is one column a resource mapper may write.
type MapperColumn struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type,omitempty" doc:"string, number, boolean, dateTime, object or array. An unrecognised type renders as text with a warning rather than disappearing from the form."`
	Required    bool   `json:"required,omitempty"`
	// CanBeUsedToMatch marks a column that identifies a row.
	CanBeUsedToMatch bool `json:"canBeUsedToMatch,omitempty"`
	DefaultMatch     bool `json:"defaultMatch,omitempty"`
	// ReadOnly marks a column the database fills in. It may be matched on and
	// never written.
	ReadOnly bool                      `json:"readOnly,omitempty"`
	Options  []property.PropertyOption `json:"options,omitempty"`
}

// LoadSchemaResource is a resolved column list.
type LoadSchemaResource struct {
	Fields []MapperColumn `json:"fields"`
	Reason string         `json:"reason,omitempty" doc:"Why the list is empty, when it is empty for a reason the user can act on."`
}

type loadSchemaOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LoadSchemaResource
}

// LoadSchema resolves a resource mapper's columns.
//
// It reuses load-options' request body, registry validation and embed bound
// deliberately: two endpoints that answer for the same node with two gates is
// how two gates drift apart.
func (handler *NodeTypes) LoadSchema(ctx context.Context, input *loadOptionsInput) (*loadSchemaOutput, error) {
	if handler.registry == nil || handler.options == nil {
		return nil, huma.Error503ServiceUnavailable("the node catalogue is unavailable")
	}
	declared, err := handler.declaredProperty(input)
	if err != nil {
		return nil, err
	}
	if declared.Kind != node.PropertyResourceMapper || declared.Mapper == nil || declared.Mapper.Schema == nil {
		return nil, huma.Error422UnprocessableEntity("that property is not a resource mapper and has no column list")
	}

	scope, err := handler.scopeFor(ctx, input, declared.Mapper.Schema.DependsOn)
	if err != nil {
		return nil, err
	}
	schema, err := handler.options.LoadSchema(ctx, *declared.Mapper.Schema, scope)
	if err != nil {
		return nil, huma.Error502BadGateway(err.Error())
	}
	fields := make([]MapperColumn, 0, len(schema.Fields))
	for _, field := range schema.Fields {
		fields = append(fields, MapperColumn{
			ID: field.ID, DisplayName: field.DisplayName, Type: field.Type,
			Required: field.Required, CanBeUsedToMatch: field.CanBeUsedToMatch,
			DefaultMatch: field.DefaultMatch, ReadOnly: field.ReadOnly, Options: field.Options,
		})
	}
	return &loadSchemaOutput{
		// Uncached, unlike an option list: a mapping validated against a stale
		// column set would refuse a column that exists or accept one that no
		// longer does.
		CacheControl: "no-store",
		Body:         LoadSchemaResource{Fields: fields, Reason: schema.Reason},
	}, nil
}

// nodeIconInput identifies the artwork to serve.
type nodeIconInput struct {
	Type    string `path:"type"`
	Version string `query:"version" doc:"Node type version. Omit for the registered default."`
	Theme   string `query:"theme" enum:"light,dark" doc:"Which variant to serve. A node shipping one variant serves it for both."`
}

// nodeIconOutput carries the bytes and the headers that keep them inert.
type nodeIconOutput struct {
	ContentType  string `header:"Content-Type"`
	NoSniff      string `header:"X-Content-Type-Options"`
	CSP          string `header:"Content-Security-Policy"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

// Icon serves a node's own artwork.
//
// SVG is an active document format — it can carry script, event handlers and
// external references — and this editor is embedded in customer pages, so a
// stored XSS here is a cross-tenant problem rather than a cosmetic one. Three
// layers defend it and all three are wanted: the bytes are checked when a node
// registers, so hostile artwork fails to load rather than being rendered safely
// forever; the response is served inert by these headers; and the editor renders
// through `<img src>`, which gives the browser's own image sandbox.
func (handler *NodeTypes) Icon(_ context.Context, input *nodeIconInput) (*nodeIconOutput, error) {
	if handler.registry == nil {
		return nil, huma.Error503ServiceUnavailable("the node catalogue is unavailable")
	}
	version := workflow.TypeVersion{}
	if input.Version != "" {
		parsed, err := workflow.ParseTypeVersion(input.Version)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("the node type version is not a decimal number")
		}
		version = parsed
	}
	definition, found := handler.registry.Resolve(input.Type, version)
	if !found {
		return nil, huma.Error404NotFound("that node type is not registered")
	}
	icon := definition.IconFor(input.Theme)
	if icon == nil {
		// A node using a builtin glyph ships no bytes; that name resolves in
		// the editor to a component it already imports.
		return nil, huma.Error404NotFound("that node type ships no icon")
	}

	return &nodeIconOutput{
		ContentType: icon.MediaType,
		NoSniff:     "nosniff",
		// Nothing loads, nothing executes, and the document is sandboxed even
		// if a browser is persuaded to treat it as a page.
		CSP: "default-src 'none'; style-src 'unsafe-inline'; sandbox",
		// Immutable per type and version: a node's artwork does not change
		// without its version changing.
		CacheControl: "public, max-age=86400, immutable",
		Body:         icon.Bytes,
	}, nil
}
