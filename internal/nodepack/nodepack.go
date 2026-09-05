// Package nodepack is the on-disk format of a generated node pack.
//
// A pack is a JSON file, committed to the repository and embedded in the
// binary. Not generated Go: 124 operations of it would drown every future diff
// in this repository. Not an OpenAPI document parsed at server start: that
// would put a third-party file on the boot path, where a spec change becomes a
// startup failure rather than a build failure.
//
// The file is deliberately not a dump of `node.Definition` and `routing.Node`.
// It is shaped the way the thing it describes is shaped — a list of resources,
// each a list of operations, each with its request and its parameters — so one
// operation's whole story sits in one place and a review diff is local. Loading
// converts it into the two internal shapes.
//
// A pack cannot name its own executor. `Load` sets `ExecutorID` to the routing
// interpreter's, because a pack that could choose its binding could claim any
// executor the server has registered, including one with privileges no pack
// should reach.
package nodepack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// Pack is one generated node type.
type Pack struct {
	Type             string               `json:"type"`
	Version          workflow.TypeVersion `json:"version"`
	DisplayName      string               `json:"displayName"`
	Description      string               `json:"description,omitempty"`
	Category         string               `json:"category"`
	Icon             string               `json:"icon,omitempty"`
	IconColor        string               `json:"iconColor,omitempty"`
	Subtitle         string               `json:"subtitle,omitempty"`
	DocumentationURL string               `json:"documentationUrl,omitempty"`
	// CredentialType is the credential this pack authenticates with.
	CredentialType string `json:"credentialType,omitempty"`
	// RequestDefaults are shared by every operation.
	RequestDefaults routing.Request `json:"requestDefaults"`
	// Trigger, when set, makes this a webhook trigger node rather than an
	// action node: it declares one output per event instead of a resource and
	// operation cascade, and carries no resources at all.
	Trigger *Trigger `json:"trigger,omitempty"`
	// Parameters are the node's own properties, one per key.
	//
	// One per key because the node registry refuses duplicates, deliberately.
	// n8n keeps one property per operation and lets several share a name; here
	// a parameter used by several operations is a single property that says
	// which resources and operations show it.
	Parameters []Parameter `json:"parameters"`
	Resources  []Resource  `json:"resources,omitempty"`
	// Generator records where this file came from, so a reviewer can tell a
	// regeneration from a hand edit.
	Generator Provenance `json:"generator"`
}

// Provenance is how a pack file says where it came from.
type Provenance struct {
	Tool        string `json:"tool"`
	Source      string `json:"source"`
	SourceTitle string `json:"sourceTitle,omitempty"`
	// SourceVersion is the described API's version, not this tool's.
	SourceVersion string `json:"sourceVersion,omitempty"`
	// SourceDigest is the SHA-256 of the document this was generated from, so
	// "regenerate and diff" is a check anyone can run.
	SourceDigest string `json:"sourceDigest,omitempty"`
}

// Resource is one group of operations, and one value of the `resource` picker.
type Resource struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Operations  []Operation `json:"operations"`
}

// Operation is one request the node can make.
type Operation struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Method      string `json:"method"`
	// URL is appended to the request defaults' base URL. `{name}` placeholders
	// are filled from path parameters, one escaped segment each.
	URL string `json:"url"`
	// Sends place this operation's parameters into the request.
	Sends      []routing.Send      `json:"sends,omitempty"`
	Output     *routing.Output     `json:"output,omitempty"`
	Pagination *routing.Pagination `json:"pagination,omitempty"`
}

// Parameter is one node property plus where it is shown.
type Parameter struct {
	Key         string                    `json:"key"`
	Label       string                    `json:"label"`
	Description string                    `json:"description,omitempty"`
	Kind        property.Kind             `json:"kind"`
	Required    bool                      `json:"required,omitempty"`
	Default     any                       `json:"default,omitempty"`
	Options     []property.PropertyOption `json:"options,omitempty"`
	TypeOptions *property.TypeOptions     `json:"typeOptions,omitempty"`
	// Resources and Operations are the values that show this property. Both are
	// required: an operation name alone does not identify an operation, because
	// names repeat across resources.
	Resources  []string `json:"resources,omitempty"`
	Operations []string `json:"operations,omitempty"`
}

// The two properties the format's cascade is built on.
const (
	ResourceKey  = "resource"
	OperationKey = "operation"
)

// Decode reads a pack file.
//
// Unknown fields are an error, for the same reason routing metadata refuses
// them: a pack written against a feature this build does not implement must
// fail at load rather than losing the field silently.
func Decode(data []byte) (*Pack, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var pack Pack
	if err := decoder.Decode(&pack); err != nil {
		return nil, fmt.Errorf("decode node pack: %w", err)
	}
	return &pack, nil
}

// Load converts a pack into the definition the editor sees and the routing
// description the interpreter executes.
func Load(pack *Pack) (node.Definition, *routing.Node, error) {
	if pack == nil {
		return node.Definition{}, nil, fmt.Errorf("a node pack is required")
	}
	if strings.TrimSpace(pack.Type) == "" || strings.TrimSpace(pack.DisplayName) == "" {
		return node.Definition{}, nil, fmt.Errorf("a node pack needs a type and a display name")
	}
	if pack.Trigger == nil && len(pack.Resources) == 0 {
		return node.Definition{}, nil, fmt.Errorf("node pack %q declares neither resources nor a trigger", pack.Type)
	}
	if pack.Trigger != nil && len(pack.Resources) > 0 {
		return node.Definition{}, nil, fmt.Errorf("node pack %q is both a trigger and an action node", pack.Type)
	}

	definition := node.Definition{
		Type: pack.Type, Version: pack.Version,
		DisplayName: pack.DisplayName, Description: pack.Description,
		Category:  pack.Category,
		Group:     []node.NodeGroup{node.GroupOutput},
		Inputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Input"}},
		Outputs:   []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Output"}},
		IconColor: pack.IconColor, Subtitle: pack.Subtitle,
		DocumentationURL: pack.DocumentationURL,
		// A pack does not get to choose its executor. One that could would be
		// able to claim any binding the server has registered.
		ExecutorID: routing.ExecutorID,
	}
	if pack.Icon != "" {
		definition.Icon = &node.NodeIcon{Light: pack.Icon}
	}
	if pack.CredentialType != "" {
		definition.Credentials = []node.CredentialRequirement{{Type: pack.CredentialType, Required: true}}
	}

	if pack.Trigger != nil {
		return loadTrigger(pack, definition)
	}

	description := &routing.Node{
		Type: pack.Type, Version: pack.Version,
		Defaults: pack.RequestDefaults,
		Cascade: &routing.Cascade{
			ResourceKey: ResourceKey, OperationKey: OperationKey,
			Routes: map[string]map[string]routing.Routing{},
		},
	}

	if len(pack.Resources[0].Operations) == 0 {
		return node.Definition{}, nil, fmt.Errorf("node pack %q resource %q declares no operations", pack.Type, pack.Resources[0].Name)
	}

	resourceOptions := make([]property.PropertyOption, 0, len(pack.Resources))
	operationOptions := make([]property.PropertyOption, 0, 32)
	seenOperation := map[string]bool{}
	for _, resource := range pack.Resources {
		if strings.TrimSpace(resource.Name) == "" {
			return node.Definition{}, nil, fmt.Errorf("node pack %q has a resource with no name", pack.Type)
		}
		resourceOptions = append(resourceOptions, property.PropertyOption{Label: resource.Name, Value: resource.Name})
		routes := make(map[string]routing.Routing, len(resource.Operations))
		for _, operation := range resource.Operations {
			if _, duplicate := routes[operation.Name]; duplicate {
				return node.Definition{}, nil, fmt.Errorf("node pack %q resource %q declares operation %q twice",
					pack.Type, resource.Name, operation.Name)
			}
			routes[operation.Name] = routing.Routing{
				Request: &routing.Request{Method: operation.Method, URL: operation.URL},
				Sends:   operation.Sends,
				Output:  operation.Output,
				Operations: func() *routing.Operations {
					if operation.Pagination == nil {
						return nil
					}
					return &routing.Operations{Pagination: operation.Pagination}
				}(),
			}
			// The picker lists each operation name once. The cascade is what
			// tells two same-named operations apart, so deduplicating here
			// loses nothing but a repeated row.
			if !seenOperation[operation.Name] {
				seenOperation[operation.Name] = true
				operationOptions = append(operationOptions, property.PropertyOption{Label: operation.Name, Value: operation.Name})
			}
		}
		description.Cascade.Routes[resource.Name] = routes
	}
	sort.Slice(operationOptions, func(i, j int) bool { return operationOptions[i].Value < operationOptions[j].Value })

	definition.Parameters = append(definition.Parameters,
		property.PropertyDefinition{
			Key: ResourceKey, Label: "Resource", Kind: property.KindOptions, Required: true,
			Default: pack.Resources[0].Name, Options: resourceOptions,
		},
		property.PropertyDefinition{
			Key: OperationKey, Label: "Operation", Kind: property.KindOptions, Required: true,
			Default: pack.Resources[0].Operations[0].Name, Options: operationOptions,
			// The static list is every operation in the pack, because a
			// definition holds one `operation` property where n8n holds one per
			// resource. The loader narrows it to the chosen resource, so the
			// picker shows the eight operations of Sessions rather than all
			// hundred-odd — and `dependsOn` is what discards the previous
			// resource's list when the user changes their mind.
			LoadOptions: &property.OptionsLoader{
				Source: property.LoaderInternal, Name: OperationsLoaderName(pack.Type, pack.Version),
				DependsOn: []string{ResourceKey},
			},
		},
	)
	seenParameter := map[string]bool{}
	for _, parameter := range pack.Parameters {
		declared, err := parameter.definition()
		if err != nil {
			return node.Definition{}, nil, fmt.Errorf("node pack %q parameter %q: %w", pack.Type, parameter.Key, err)
		}
		// The registry refuses duplicate keys, and would say so about a
		// definition rather than about the pack file that produced it.
		if seenParameter[parameter.Key] {
			return node.Definition{}, nil, fmt.Errorf("node pack %q declares parameter %q twice", pack.Type, parameter.Key)
		}
		seenParameter[parameter.Key] = true
		definition.Parameters = append(definition.Parameters, declared)
	}

	return definition, description, nil
}

// loadTrigger finishes a trigger pack's definition.
//
// A trigger has no input, one output per event, and its executor is the fan-out
// rather than the routing interpreter. It carries no routing description at
// all: a trigger makes no outbound request.
func loadTrigger(pack *Pack, definition node.Definition) (node.Definition, *routing.Node, error) {
	trigger := pack.Trigger
	if err := trigger.validate(); err != nil {
		return node.Definition{}, nil, fmt.Errorf("node pack %q: %w", pack.Type, err)
	}
	definition.Group = []node.NodeGroup{node.GroupTrigger}
	definition.Inputs = nil
	definition.Outputs = trigger.Ports()
	definition.Webhook = trigger.Webhook
	definition.ExecutorID = TriggerExecutorID
	if trigger.Lifecycle != nil {
		definition.LifecycleID = trigger.Lifecycle.ID
	}

	seen := map[string]bool{}
	for _, parameter := range pack.Parameters {
		declared, err := parameter.definition()
		if err != nil {
			return node.Definition{}, nil, fmt.Errorf("node pack %q parameter %q: %w", pack.Type, parameter.Key, err)
		}
		if seen[parameter.Key] {
			return node.Definition{}, nil, fmt.Errorf("node pack %q declares parameter %q twice", pack.Type, parameter.Key)
		}
		seen[parameter.Key] = true
		definition.Parameters = append(definition.Parameters, declared)
	}
	if trigger.Webhook.PathParameter != "" && !seen[trigger.Webhook.PathParameter] {
		return node.Definition{}, nil, fmt.Errorf(
			"node pack %q binds its route to parameter %q, which it does not declare",
			pack.Type, trigger.Webhook.PathParameter)
	}
	return definition, nil, nil
}

func (parameter Parameter) definition() (property.PropertyDefinition, error) {
	if strings.TrimSpace(parameter.Key) == "" {
		return property.PropertyDefinition{}, fmt.Errorf("a parameter needs a key")
	}
	if parameter.Key == ResourceKey || parameter.Key == OperationKey {
		return property.PropertyDefinition{}, fmt.Errorf("%q is reserved for the pack's own cascade", parameter.Key)
	}
	if parameter.Kind == "" {
		return property.PropertyDefinition{}, fmt.Errorf("a parameter needs a kind")
	}
	declared := property.PropertyDefinition{
		Key: parameter.Key, Label: parameter.Label, Description: parameter.Description,
		Kind: parameter.Kind, Required: parameter.Required, Default: parameter.Default,
		Options: parameter.Options, TypeOptions: parameter.TypeOptions,
	}
	visibility := property.Visibility{}
	if len(parameter.Resources) > 0 {
		visibility.Show = append(visibility.Show, property.Condition{
			Key: ResourceKey, Values: values(parameter.Resources),
		})
	}
	if len(parameter.Operations) > 0 {
		visibility.Show = append(visibility.Show, property.Condition{
			Key: OperationKey, Values: values(parameter.Operations),
		})
	}
	declared.DisplayOptions = visibility
	return declared, nil
}

func values(names []string) []any {
	converted := make([]any, len(names))
	for index, name := range names {
		converted[index] = name
	}
	return converted
}

// ExecutorSet answers whether a binding is installed on this server.
//
// `*engine.Registry` satisfies it. It is an interface so this package stays out
// of the runtime's way, and a parameter rather than an assumption so the check
// cannot be skipped by a caller that forgot it.
type ExecutorSet interface {
	Lookup(executorID string) (engine.Executor, bool)
}

// OperationsLoaderName is the internal options loader one pack registers.
//
// Keyed by node type and version because the loader answers with that pack's
// operations and the loader scope carries no node identity: two packs sharing a
// name would answer each other's questions.
func OperationsLoaderName(nodeType string, version workflow.TypeVersion) string {
	return "nodepack." + nodeType + "@" + version.String() + ".operations"
}

// OperationsLoader answers the operations of the resource the editor is on.
//
// An unknown or absent resource yields an empty list with a reason rather than
// every operation in the pack: a picker that silently offers operations the
// chosen resource does not have is how a user builds a node that fails at run
// time with "no request is declared for this pair".
func OperationsLoader(pack *Pack) loadoptions.InternalLoader {
	byResource := make(map[string][]loadoptions.Option, len(pack.Resources))
	for _, resource := range pack.Resources {
		options := make([]loadoptions.Option, 0, len(resource.Operations))
		for _, operation := range resource.Operations {
			options = append(options, loadoptions.Option{Label: operation.Name, Value: operation.Name})
		}
		byResource[resource.Name] = options
	}
	return func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		resource := scope.Dependencies[ResourceKey]
		if resource == "" {
			return loadoptions.Result{Options: []loadoptions.Option{}, Reason: "Choose a resource first."}, nil
		}
		options, found := byResource[resource]
		if !found {
			return loadoptions.Result{
				Options: []loadoptions.Option{},
				Reason:  fmt.Sprintf("This node has no resource named %q.", resource),
			}, nil
		}
		return loadoptions.Result{Options: options}, nil
	}
}

// Register installs a pack's definition and its routing together, and refuses a
// pack whose executor binding is not installed.
//
// Together is the point, and the two halves are guaranteed differently.
//
// A definition bound to an executor nobody installed is refused here, by the
// lookup below — that one needs a check, because a pack can name any binding.
//
// A definition bound to the routing interpreter with no routing description
// cannot arise at all: Load builds the two from one manifest and returns them
// together, and the branch that returns no description is the trigger branch,
// which sets TriggerExecutorID instead. So the pairing is structural rather
// than asserted. Saying this precisely matters — an earlier version of this
// comment described it as a startup check, a reader trusted that, and the
// check they went looking for was not there to find.
func Register(definitions *node.Registry, routes *routing.Registry, executors ExecutorSet, options *loadoptions.Resolver, pack *Pack) error {
	definition, description, err := Load(pack)
	if err != nil {
		return err
	}
	if executors == nil {
		return fmt.Errorf("node pack %q: no executor registry was supplied to check its binding against", pack.Type)
	}
	if _, installed := executors.Lookup(definition.ExecutorID); !installed {
		return fmt.Errorf("node pack %q is bound to executor %q, which this server has not installed",
			pack.Type, definition.ExecutorID)
	}
	if description != nil {
		if err := routes.Register(description); err != nil {
			return err
		}
	}
	if options != nil && pack.Trigger == nil {
		if err := options.RegisterInternal(OperationsLoaderName(pack.Type, pack.Version), OperationsLoader(pack)); err != nil {
			return err
		}
	}
	return definitions.RegisterFrom(node.SourcePack, definition)
}
