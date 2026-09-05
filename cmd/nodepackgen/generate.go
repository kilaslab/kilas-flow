package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// manifest is what the OpenAPI document cannot say.
//
// A document describes how to authenticate but not which KilasFlow credential
// type carries it, and it has no opinion about what the node is called or where
// it is filed. Those are decisions, so they are written down rather than
// guessed.
type manifest struct {
	Type             string               `json:"type"`
	Version          workflow.TypeVersion `json:"version"`
	DisplayName      string               `json:"displayName"`
	Description      string               `json:"description,omitempty"`
	Category         string               `json:"category"`
	Icon             string               `json:"icon,omitempty"`
	IconColor        string               `json:"iconColor,omitempty"`
	Subtitle         string               `json:"subtitle,omitempty"`
	DocumentationURL string               `json:"documentationUrl,omitempty"`
	// BaseURL overrides the document's first server. A self-hosted product has
	// no fixed host, so this is normally a template over the credential.
	BaseURL string `json:"baseUrl,omitempty"`
	// Security maps an OpenAPI security scheme name to a KilasFlow credential
	// type. A scheme the document uses and this map does not name is fatal: a
	// pack that generates cleanly and then cannot authenticate is the worst
	// outcome available here.
	Security map[string]string `json:"security"`
	// SkipDeprecated leaves deprecated operations out of the pack.
	SkipDeprecated bool `json:"skipDeprecated,omitempty"`
}

func (m manifest) validate() error {
	if strings.TrimSpace(m.Type) == "" {
		return fmt.Errorf("the manifest needs a node type")
	}
	if strings.TrimSpace(m.DisplayName) == "" {
		return fmt.Errorf("the manifest needs a display name")
	}
	if strings.TrimSpace(m.Category) == "" {
		return fmt.Errorf("the manifest needs a category")
	}
	return nil
}

// report collects everything the generator could not express.
//
// It is an output, not a log. A construct that is absent from the pack has to
// be findable, or the pack looks complete and is not.
type report struct {
	Source       string
	Manifest     string
	Out          string
	SourceDigest string
	Title        string
	APIVersion   string
	Operations   int
	Skipped      []string
	Notes        []string
}

func (r *report) skip(format string, arguments ...any) {
	r.Skipped = append(r.Skipped, fmt.Sprintf(format, arguments...))
}

func (r *report) note(format string, arguments ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, arguments...))
}

// generate turns a document and a manifest into a pack and a report.
func generate(doc *document, source string, raw []byte, m manifest) (*nodepack.Pack, *report, error) {
	if err := m.validate(); err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(raw)
	generated := &report{
		Source: source, SourceDigest: hex.EncodeToString(digest[:]),
		Title: doc.Info.Title, APIVersion: doc.Info.Version,
	}

	credentialType, err := credentialFor(doc, m, generated)
	if err != nil {
		return nil, nil, err
	}
	baseURL := strings.TrimSpace(m.BaseURL)
	if baseURL == "" {
		if len(doc.Servers) == 0 {
			return nil, nil, fmt.Errorf("the document declares no server and the manifest supplies no base URL")
		}
		baseURL = doc.Servers[0].URL
	}

	listed, err := doc.operations()
	if err != nil {
		return nil, nil, err
	}

	builder := newBuilder(generated)
	for _, entry := range listed {
		builder.add(doc, entry, m)
	}

	pack := &nodepack.Pack{
		Type: m.Type, Version: m.Version,
		DisplayName: m.DisplayName, Description: m.Description, Category: m.Category,
		Icon: m.Icon, IconColor: m.IconColor, Subtitle: m.Subtitle,
		DocumentationURL: m.DocumentationURL,
		CredentialType:   credentialType,
		RequestDefaults:  routing.Request{BaseURL: baseURL},
		Parameters:       builder.parameters(),
		Resources:        builder.resources(),
		Generator: nodepack.Provenance{
			Tool: "nodepackgen", Source: source,
			SourceTitle: doc.Info.Title, SourceVersion: doc.Info.Version,
			SourceDigest: generated.SourceDigest,
		},
	}
	generated.Operations = 0
	for _, resource := range pack.Resources {
		generated.Operations += len(resource.Operations)
	}
	if len(pack.Resources) == 0 {
		return nil, nil, fmt.Errorf("no operation in %s could be generated; see the report", source)
	}
	return pack, generated, nil
}

// credentialFor maps the document's security scheme onto a credential type.
func credentialFor(doc *document, m manifest, generated *report) (string, error) {
	names := make([]string, 0, len(doc.Components.SecuritySchemes))
	for name := range doc.Components.SecuritySchemes {
		names = append(names, name)
	}
	sort.Strings(names)

	mapped := make([]string, 0, 1)
	for _, name := range names {
		credential, declared := m.Security[name]
		if !declared || strings.TrimSpace(credential) == "" {
			return "", fmt.Errorf("security scheme %q has no credential type in the manifest; "+
				"a pack that generates cleanly and then cannot authenticate is worse than one that fails here", name)
		}
		mapped = append(mapped, credential)
	}
	switch len(mapped) {
	case 0:
		generated.note("The document declares no security scheme, so the pack authenticates with nothing.")
		return "", nil
	case 1:
		return mapped[0], nil
	default:
		// One credential per node is what the definition can express. Naming
		// the rest is better than picking one silently.
		generated.note("The document declares %d security schemes (%s); the pack uses %q and the rest are unreachable.",
			len(mapped), strings.Join(mapped[1:], ", "), mapped[0])
		return mapped[0], nil
	}
}

// builder accumulates resources, operations and the merged parameter set.
type builder struct {
	report     *report
	byResource map[string][]nodepack.Operation
	merged     map[string]*mergedParameter
	order      []string
}

// mergedParameter is one node property being assembled from several operations.
//
// A KilasFlow definition holds one property per key — the registry refuses
// duplicates — so a parameter used by six operations is one property that says
// which resources and operations show it, not six properties sharing a name.
type mergedParameter struct {
	parameter  nodepack.Parameter
	resources  map[string]bool
	operations map[string]bool
	// occurrences counts how many operations declared it, so "required in all
	// of them" can be decided at the end rather than guessed at each one.
	occurrences int
	required    int
	conflict    bool
}

func newBuilder(generated *report) *builder {
	return &builder{report: generated, byResource: map[string][]nodepack.Operation{}, merged: map[string]*mergedParameter{}}
}

func (b *builder) add(doc *document, entry resolvedOperation, m manifest) {
	where := fmt.Sprintf("%s %s", entry.Method, entry.Path)
	if len(entry.Operation.Tags) == 0 {
		b.report.skip("%s: no tag, so it belongs to no resource", where)
		return
	}
	if entry.Operation.OperationID == "" {
		b.report.skip("%s: no operationId, so it has no stable name", where)
		return
	}
	if entry.Operation.Deprecated && m.SkipDeprecated {
		b.report.skip("%s: deprecated", where)
		return
	}

	resource := nodepack.ResourceName(entry.Operation.Tags[0])
	name := nodepack.OperationName(entry.Operation.OperationID)
	if resource == "" || name == "" {
		b.report.skip("%s: tag %q and operationId %q do not produce a usable name", where, entry.Operation.Tags[0], entry.Operation.OperationID)
		return
	}
	for _, existing := range b.byResource[resource] {
		if existing.Name == name {
			b.report.skip("%s: resource %q already has an operation named %q; the first one wins", where, resource, name)
			return
		}
	}

	sends := make([]routing.Send, 0, 8)

	// Path first, in the order the document lists them, then query, then body.
	// A stable order is what makes regeneration byte-identical.
	for _, declared := range entry.Operation.Parameters {
		if declared.In != "path" && declared.In != "query" {
			// The operation is still generated; only this parameter is not, so
			// it is a note rather than a skip. Filing it under "left out
			// operations" would say the request cannot be made at all.
			if declared.In != "" {
				b.report.note("%s parameter %q is in %q, which the interpreter cannot place; the operation is generated without it", where, declared.Name, declared.In)
			}
			continue
		}
		resolved, err := doc.resolve(declared.Schema)
		if err != nil {
			b.report.skip("%s: parameter %q: %v", where, declared.Name, err)
			continue
		}
		kind, options, note := kindOf(resolved)
		if note != "" {
			b.report.note("%s parameter %q: %s", where, declared.Name, note)
		}
		var fallback any
		if resolved != nil {
			fallback = resolved.Default
		}
		b.mergeParameter(nodepack.Parameter{
			Key: declared.Name, Label: nodepack.StartCase(declared.Name),
			Description: declared.Description, Kind: kind,
			Required: declared.Required || declared.In == "path",
			Default:  fallback, Options: options,
		}, resource, name)
		sends = append(sends, routing.Send{From: declared.Name, Type: declared.In, Property: declared.Name})
	}

	if entry.Operation.RequestBody != nil {
		bodySends, ok := b.addBody(doc, entry, where, resource, name)
		if !ok {
			return
		}
		sends = append(sends, bodySends...)
	}

	b.byResource[resource] = append(b.byResource[resource], nodepack.Operation{
		Name: name, Description: describe(entry.Operation), Method: entry.Method, URL: entry.Path, Sends: sends,
	})
}

// addBody turns a JSON request body's first-level properties into parameters.
//
// Only first level. A nested object becomes one JSON parameter that a user
// fills with an expression, which is what the packages these documents are
// usually turned into do — and reproducing it is what makes the generated
// parameter set match the one an imported workflow was authored against.
func (b *builder) addBody(doc *document, entry resolvedOperation, where, resource, operation string) ([]routing.Send, bool) {
	content := entry.Operation.RequestBody.Content
	media, present := content["application/json"]
	if !present {
		types := make([]string, 0, len(content))
		for mediaType := range content {
			types = append(types, mediaType)
		}
		sort.Strings(types)
		b.report.skip("%s: request body is %s, and only application/json can be generated", where, strings.Join(types, ", "))
		return nil, false
	}
	resolved, err := doc.resolve(media.Schema)
	if err != nil {
		b.report.skip("%s: request body: %v", where, err)
		return nil, false
	}
	if resolved == nil {
		return nil, true
	}
	if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
		b.report.skip("%s: request body is a oneOf/anyOf union, which has no single parameter set", where)
		return nil, false
	}
	if len(resolved.Properties) == 0 {
		b.report.skip("%s: request body has no named properties to turn into parameters", where)
		return nil, false
	}

	required := map[string]bool{}
	for _, key := range resolved.Required {
		required[key] = true
	}
	keys := make([]string, 0, len(resolved.Properties))
	for key := range resolved.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sends := make([]routing.Send, 0, len(keys))
	for _, key := range keys {
		field, err := doc.resolve(resolved.Properties[key])
		if err != nil {
			b.report.note("%s body property %q: %v; generated as free-form JSON", where, key, err)
			field = nil
		}
		kind, options, note := kindOf(field)
		if note != "" {
			b.report.note("%s body property %q: %s", where, key, note)
		}
		var description string
		var fallback any
		if field != nil {
			description, fallback = field.Description, field.Default
		}
		b.mergeParameter(nodepack.Parameter{
			Key: key, Label: nodepack.StartCase(key), Description: description,
			Kind: kind, Required: required[key], Default: fallback, Options: options,
		}, resource, operation)
		sends = append(sends, routing.Send{From: key, Type: "body", Property: key})
	}
	return sends, true
}

// mergeParameter folds one operation's parameter into the shared property.
func (b *builder) mergeParameter(candidate nodepack.Parameter, resource, operation string) {
	existing, found := b.merged[candidate.Key]
	if !found {
		existing = &mergedParameter{
			parameter: candidate,
			resources: map[string]bool{}, operations: map[string]bool{},
		}
		b.merged[candidate.Key] = existing
		b.order = append(b.order, candidate.Key)
	} else {
		if existing.parameter.Kind != candidate.Kind && !existing.conflict {
			existing.conflict = true
			b.report.note("Parameter %q is declared as %s and %s by different operations; generated as a string.",
				candidate.Key, existing.parameter.Kind, candidate.Kind)
			existing.parameter.Kind = property.KindString
			existing.parameter.Options = nil
			existing.parameter.Default = nil
		}
		if !existing.conflict && !sameOptions(existing.parameter.Options, candidate.Options) {
			existing.conflict = true
			b.report.note("Parameter %q has different option sets in different operations; generated as a string.", candidate.Key)
			existing.parameter.Kind = property.KindString
			existing.parameter.Options = nil
			existing.parameter.Default = nil
		}
		// DeepEqual rather than `!=`: a default can be an array or an object,
		// and comparing those with `==` panics rather than answering.
		if !existing.conflict && !reflect.DeepEqual(existing.parameter.Default, candidate.Default) {
			existing.parameter.Default = nil
		}
		if existing.parameter.Description == "" {
			existing.parameter.Description = candidate.Description
		}
	}
	existing.resources[resource] = true
	existing.operations[operation] = true
	existing.occurrences++
	if candidate.Required {
		existing.required++
	}
}

func (b *builder) parameters() []nodepack.Parameter {
	keys := append([]string(nil), b.order...)
	sort.Strings(keys)

	built := make([]nodepack.Parameter, 0, len(keys))
	for _, key := range keys {
		merged := b.merged[key]
		parameter := merged.parameter
		// Required only when every operation that shows it requires it. The
		// visibility union cannot say "required here but not there", and a
		// field demanded where it is not used makes the node unsavable.
		parameter.Required = merged.required == merged.occurrences
		parameter.Resources = sortedKeys(merged.resources)
		parameter.Operations = sortedKeys(merged.operations)
		built = append(built, parameter)
	}
	return built
}

func (b *builder) resources() []nodepack.Resource {
	names := make([]string, 0, len(b.byResource))
	for name := range b.byResource {
		names = append(names, name)
	}
	sort.Strings(names)

	built := make([]nodepack.Resource, 0, len(names))
	for _, name := range names {
		operations := b.byResource[name]
		sort.Slice(operations, func(i, j int) bool { return operations[i].Name < operations[j].Name })
		built = append(built, nodepack.Resource{Name: name, Operations: operations})
	}
	return built
}

// kindOf maps an OpenAPI schema onto a property kind.
func kindOf(s *schema) (property.Kind, []property.PropertyOption, string) {
	if s == nil {
		return property.KindString, nil, ""
	}
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return property.KindJSON, nil, "a oneOf/anyOf union has no single control; generated as free-form JSON"
	}
	if len(s.Enum) > 0 {
		options := make([]property.PropertyOption, 0, len(s.Enum))
		for _, entry := range s.Enum {
			text, ok := entry.(string)
			if !ok {
				return property.KindString, nil, "a non-string enum has no option list; generated as a string"
			}
			options = append(options, property.PropertyOption{Label: nodepack.StartCase(text), Value: text})
		}
		return property.KindOptions, options, ""
	}
	switch s.typeName() {
	case "string":
		return property.KindString, nil, ""
	case "integer", "number":
		return property.KindNumber, nil, ""
	case "boolean":
		return property.KindBoolean, nil, ""
	case "object", "array":
		// Only first-level properties become controls. A nested value is one
		// JSON parameter a user fills with an expression, which is what the
		// imported workflow was authored against.
		return property.KindJSON, nil, ""
	case "":
		return property.KindJSON, nil, "no type; generated as free-form JSON"
	default:
		return property.KindString, nil, fmt.Sprintf("type %q is not a control kind; generated as a string", s.typeName())
	}
}

func describe(op operation) string {
	if op.Summary != "" {
		return op.Summary
	}
	return op.Description
}

func sameOptions(left, right []property.PropertyOption) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
