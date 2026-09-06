// Conversion of operator-transcribed declarative node descriptions into packs.
//
// The input is a transcription the operator makes from a community package
// they installed themselves: the node's resource and operation values, its
// parameter shapes, and its routing metadata. Those are format facts in the
// sense `.pine/memory/licensing.md` means — resource and operation strings
// a workflow JSON references, the request each pair makes — and reading them
// is how any second implementation of the format works. What the converter
// never sees is the package itself: no JavaScript is read, executed, or
// shipped, and no third-party bytes are committed unless their licence
// independently permits it.
//
// The transcription schema only has words for what the routing interpreter
// implements. Anything else — a real `execute()`, a `preSend` hook, a
// function-form `postReceive`, an unsupported pagination strategy, a static
// request fragment the pack format cannot carry — is refused or excluded
// with a named diagnostic, never silently dropped. An excluded operation is
// left out of the pack rather than emitted broken, and the report names
// every exclusion so the conversion reads as complete only when it is.
//
// The converter is build-time tooling. It returns a pack the caller writes
// with WritePackDir; it is never on the server's boot path, so an
// unconvertible source is a build failure rather than a startup failure.
package nodepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// DeclarativeNode is one transcribed declarative action node.
//
// Show carries `displayOptions.show`: each key must match one of the listed
// values for the property to appear. Only the `resource` and `operation`
// keys decide where a property lands; any other key is reported and the
// property is shown wherever its resource and operation allow, because a
// pack parameter can only be gated on those two.
type DeclarativeNode struct {
	// SourceType is the node's own type string, provenance only. The
	// converted pack takes a KilasFlow-namespaced type instead, so imported
	// workflows match through the importer's mapping table.
	SourceType  string `json:"sourceType"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	// Version is the node's typeVersion. Zero means the first version.
	Version int `json:"version,omitempty"`
	// RequestDefaults is the node-level `requestDefaults`.
	RequestDefaults routing.Request         `json:"requestDefaults,omitempty"`
	Credentials     []DeclarativeCredential `json:"credentials,omitempty"`
	// HasExecute attests that the node file defines a programmatic
	// execute(). A node that does is refused, never partially converted.
	HasExecute bool                  `json:"hasExecute"`
	Properties []DeclarativeProperty `json:"properties"`
}

// DeclarativeCredential is one credential the node authenticates with. The
// converted pack requires the first by type; the operator binds a real
// credential afterwards, since an n8n credential reference is instance-local
// and cannot travel with the pack.
type DeclarativeCredential struct {
	Name     string `json:"name"`
	Required bool   `json:"required,omitempty"`
}

// DeclarativeProperty is one transcribed node property.
type DeclarativeProperty struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Kind is the declarative type string: string, number, boolean,
	// options, multiOptions, notice, json, dateTime, and anything else,
	// which is carried as raw JSON and reported.
	Kind        string                `json:"kind"`
	Default     any                   `json:"default,omitempty"`
	Required    bool                  `json:"required,omitempty"`
	Options     []DeclarativeOption   `json:"options,omitempty"`
	Show        map[string][]string   `json:"show,omitempty"`
	Routing     *DeclarativeRouting   `json:"routing,omitempty"`
	TypeOptions *property.TypeOptions `json:"typeOptions,omitempty"`
}

// DeclarativeOption is one value of an options property. On the `operation`
// properties each option is one request the node can make, which is why the
// option carries the routing rather than the property.
type DeclarativeOption struct {
	Label       string              `json:"label"`
	Value       string              `json:"value"`
	Description string              `json:"description,omitempty"`
	Show        map[string][]string `json:"show,omitempty"`
	Routing     *DeclarativeRouting `json:"routing,omitempty"`
}

// DeclarativeRouting is the routing attached to a property or an option,
// in the interpreter's own vocabulary. Reusing those types is what keeps
// the converter from drifting out of sync with what the interpreter
// implements: a field the interpreter does not know fails the strict
// decode below instead of being silently dropped here.
type DeclarativeRouting struct {
	Request    *routing.Request    `json:"request,omitempty"`
	Send       *routing.Send       `json:"send,omitempty"`
	Output     *routing.Output     `json:"output,omitempty"`
	Operations *routing.Operations `json:"operations,omitempty"`
}

// ConvertOptions are the decisions the transcription cannot make.
type ConvertOptions struct {
	// PackType is the converted pack's namespaced type, for example
	// "pack.acmemail". The reserved built-in namespace is refused.
	PackType string
	// Category files the node in the catalogue. Empty files it as
	// Community, which is what a converted third-party node is.
	Category string
}

// ConvertReport names everything the conversion left out, following the
// packs/waha/REPORT-*.md convention. Excluded lists the operations that
// were refused a place in the pack; Notes lists the properties that made
// it in a coarser form than the source.
type ConvertReport struct {
	Source    string
	Digest    string
	Converted int
	Excluded  []string
	Notes     []string
}

func (r *ConvertReport) exclude(format string, arguments ...any) {
	r.Excluded = append(r.Excluded, fmt.Sprintf(format, arguments...))
}

func (r *ConvertReport) note(format string, arguments ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, arguments...))
}

// Markdown renders the report the way the WAHA generation reports read:
// source and digest first, then what is absent from the pack.
func (r *ConvertReport) Markdown(packType string) string {
	var out strings.Builder
	out.WriteString("# Node pack conversion report\n\n")
	fmt.Fprintf(&out, "- Source: `%s`\n", r.Source)
	fmt.Fprintf(&out, "- SHA-256: `%s`\n", r.Digest)
	fmt.Fprintf(&out, "- Operations converted: %d\n", r.Converted)
	fmt.Fprintf(&out, "\nConverted with:\n\n```\nnodepack.ConvertDocument(data, nodepack.ConvertOptions{PackType: %q})\n```\n", packType)
	out.WriteString("\nEverything listed below is **absent from the pack**.\n")
	if len(r.Excluded) > 0 {
		fmt.Fprintf(&out, "\n## Operations left out (%d)\n\n", len(r.Excluded))
		for _, excluded := range r.Excluded {
			fmt.Fprintf(&out, "- %s\n", excluded)
		}
	}
	if len(r.Notes) > 0 {
		fmt.Fprintf(&out, "\n## Degraded or merged (%d)\n\n", len(r.Notes))
		for _, note := range r.Notes {
			fmt.Fprintf(&out, "- %s\n", note)
		}
	}
	return out.String()
}

// ConvertDocument strictly decodes one transcribed node and converts it.
// The digest of the input travels in the pack's provenance block, so
// "retranscribe and diff" is a check anyone can run.
func ConvertDocument(data []byte, opts ConvertOptions) (*Pack, *ConvertReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var src DeclarativeNode
	if err := decoder.Decode(&src); err != nil {
		return nil, nil, fmt.Errorf("decode declarative node: %w", err)
	}
	sum := sha256.Sum256(data)
	return Convert(&src, hex.EncodeToString(sum[:]), opts)
}

// Convert builds a pack from one transcribed declarative node.
//
// A refusal returns a nil pack: a node with a real execute() or with no
// declarative cascade yields no pack at all rather than a partial one that
// registers and silently does the wrong thing. When every operation is
// excluded the report comes back alongside the error, so the caller can
// still render the coverage.
func Convert(src *DeclarativeNode, digest string, opts ConvertOptions) (*Pack, *ConvertReport, error) {
	report := &ConvertReport{Digest: digest}
	if src == nil {
		return nil, report, fmt.Errorf("a declarative node is required")
	}
	report.Source = src.SourceType
	packType := strings.TrimSpace(opts.PackType)
	if packType == "" {
		return nil, report, fmt.Errorf("a pack type is required: name the KilasFlow-namespaced type the converted pack registers as")
	}
	if strings.HasPrefix(packType, node.BuiltinPrefix) {
		return nil, report, fmt.Errorf("pack type %q claims the reserved %q namespace, which only built-in nodes may use", packType, node.BuiltinPrefix)
	}
	if strings.TrimSpace(src.SourceType) == "" {
		return nil, report, fmt.Errorf("the transcription names no source type")
	}
	if strings.TrimSpace(src.DisplayName) == "" {
		return nil, report, fmt.Errorf("the transcription names no display name")
	}
	if src.HasExecute {
		return nil, report, fmt.Errorf("refusing %s: the node defines a programmatic execute(), which a pack cannot express; no pack was written", src.SourceType)
	}
	version := src.Version
	if version < 0 {
		return nil, report, fmt.Errorf("refusing %s: typeVersion %d is not a version", src.SourceType, version)
	}
	if version == 0 {
		version = 1
	}

	resources, err := convertResources(src, report)
	if err != nil {
		return nil, report, err
	}
	operations := collectOperations(src, resources, report)
	if len(operations) == 0 {
		return nil, report, fmt.Errorf("refusing %s: no convertible operations remain%s; no pack was written",
			src.SourceType, excludedSummary(report))
	}

	category := strings.TrimSpace(opts.Category)
	if category == "" {
		category = "Community"
	}
	pack := &Pack{
		Type:            packType,
		Version:         workflow.V(version),
		DisplayName:     src.DisplayName,
		Description:     src.Description,
		Category:        category,
		CredentialType:  convertCredential(src, report),
		RequestDefaults: src.RequestDefaults,
		Parameters:      convertParameters(src, resources, operations, report),
		Resources:       convertResourceList(resources, operations, report),
		Generator: Provenance{
			Tool:          "nodepack-convert",
			Source:        src.SourceType,
			SourceTitle:   src.DisplayName,
			SourceVersion: strconv.Itoa(version),
			SourceDigest:  digest,
		},
	}
	report.Converted = countOperations(pack.Resources)

	// The pack that leaves here loads: the real structural conversion and
	// the real routing validation run before it is returned, so a converter
	// bug is a conversion error rather than a pack that fails at install.
	if _, description, err := Load(pack); err != nil {
		return nil, report, fmt.Errorf("the converted pack does not load: %w", err)
	} else if description != nil {
		if err := routing.NewRegistry().Register(description); err != nil {
			return nil, report, fmt.Errorf("the converted pack's routing is not supported: %w", err)
		}
	}
	return pack, report, nil
}

func excludedSummary(report *ConvertReport) string {
	if len(report.Excluded) == 0 {
		return ""
	}
	return ": " + strings.Join(report.Excluded, "; ")
}

// convertResources reads the resource picker. The pack's cascade is keyed
// by the option values byte for byte, which is what makes a workflow JSON
// authored against the source node select the converted operations.
func convertResources(src *DeclarativeNode, report *ConvertReport) ([]DeclarativeOption, error) {
	for _, declared := range src.Properties {
		if declared.Key != ResourceKey {
			continue
		}
		if declared.Kind != "options" {
			return nil, fmt.Errorf("refusing %s: the %q picker is a %q control, not an options cascade; no pack was written",
				src.SourceType, ResourceKey, declared.Kind)
		}
		var resources []DeclarativeOption
		for _, option := range declared.Options {
			if strings.TrimSpace(option.Value) == "" {
				report.note("resource %q has no value and is left out", option.Label)
				continue
			}
			resources = append(resources, option)
			if option.Label != "" && option.Label != option.Value {
				report.note("resource %q shows as %q in the source; the picker shows the value", option.Value, option.Label)
			}
		}
		if len(resources) == 0 {
			return nil, fmt.Errorf("refusing %s: the %q picker offers no resources; no pack was written", src.SourceType, ResourceKey)
		}
		return resources, nil
	}
	return nil, fmt.Errorf("refusing %s: the node declares no %q picker; only nodes with a declarative resource/operation cascade convert",
		src.SourceType, ResourceKey)
}

// operationBuild is one candidate pack operation.
type operationBuild struct {
	resource   string
	operation  DeclarativeOption
	method     string
	url        string
	output     *routing.Output
	pagination *routing.Pagination
	sends      []routing.Send
	excluded   string
}

// collectOperations pairs every resource with the operations shown for it,
// excluding the pairs the pack format cannot carry.
func collectOperations(src *DeclarativeNode, resources []DeclarativeOption, report *ConvertReport) []*operationBuild {
	byResource := make(map[string][]DeclarativeOption, len(resources))
	for _, resource := range resources {
		byResource[resource.Value] = nil
	}
	for _, declared := range src.Properties {
		if declared.Key != OperationKey {
			continue
		}
		if declared.Kind != "options" {
			report.exclude("every operation: the %q picker is a %q control, not an options cascade", OperationKey, declared.Kind)
			return nil
		}
		for _, option := range declared.Options {
			if strings.TrimSpace(option.Value) == "" {
				report.note("operation %q has no value and is left out", option.Label)
				continue
			}
			for _, resource := range resourcesInScope(option.Show, resources) {
				byResource[resource] = append(byResource[resource], option)
			}
		}
	}
	var builds []*operationBuild
	seen := map[string]bool{}
	for _, resource := range resources {
		for _, option := range byResource[resource.Value] {
			key := resource.Value + "\x00" + option.Value
			if seen[key] {
				report.note("resource %q already has an operation named %q; the first one wins", resource.Value, option.Value)
				continue
			}
			seen[key] = true
			build := &operationBuild{resource: resource.Value, operation: option}
			if reason := checkOptionRouting(option); reason != "" {
				build.excluded = reason
				report.exclude("resource %q operation %q: %s", resource.Value, option.Value, reason)
				continue
			}
			request := option.Routing.Request
			build.method = strings.ToUpper(strings.TrimSpace(request.Method))
			if build.method == "" {
				// Both the source format and the interpreter default to
				// GET, so naming it is exact, not a guess.
				build.method = "GET"
			}
			build.url = strings.TrimSpace(request.URL)
			if option.Routing.Output != nil {
				build.output = &routing.Output{
					MaxResults:  option.Routing.Output.MaxResults,
					PostReceive: append([]routing.PostReceive(nil), option.Routing.Output.PostReceive...),
				}
			}
			if option.Routing.Operations != nil {
				build.pagination = option.Routing.Operations.Pagination
			}
			builds = append(builds, build)
		}
	}
	if len(builds) == 0 && len(report.Excluded) == 0 {
		report.exclude("every operation: the node declares no %q options", OperationKey)
	}
	applyPropertyRouting(src, builds, report)
	var kept []*operationBuild
	for _, build := range builds {
		if build.excluded != "" {
			continue
		}
		kept = append(kept, build)
	}
	return kept
}

// checkOptionRouting refuses the option-level routing the pack format has
// no words for, naming the construct rather than dropping it.
func checkOptionRouting(option DeclarativeOption) string {
	attached := option.Routing
	if attached == nil || attached.Request == nil {
		return "declares no request"
	}
	request := attached.Request
	if strings.TrimSpace(request.URL) == "" {
		return "declares no URL"
	}
	if strings.TrimSpace(request.BaseURL) != "" {
		return "a per-operation baseURL override is not expressible in a pack"
	}
	for _, field := range []struct {
		name    string
		present bool
	}{
		{"headers", len(request.Headers) > 0},
		{"query", len(request.Query) > 0},
		{"body", len(request.Body) > 0},
		{"path", len(request.Path) > 0},
	} {
		if field.present {
			return fmt.Sprintf("carries a static %s fragment the pack format cannot express", field.name)
		}
	}
	if attached.Send != nil {
		return "an operation option carries per-property placement, which belongs on the property"
	}
	if attached.Output != nil {
		for _, action := range attached.Output.PostReceive {
			switch action.Type {
			case routing.PostReceiveRootProperty, routing.PostReceiveSetKeyValue,
				routing.PostReceiveLimit, routing.PostReceiveBinaryData:
			case "function":
				return "a function postReceive is JavaScript and is not supported"
			default:
				return fmt.Sprintf("postReceive action %q is not supported", action.Type)
			}
			if action.Type == routing.PostReceiveBinaryData {
				if template, _ := action.Properties["url"].(string); strings.TrimSpace(template) == "" {
					return fmt.Sprintf("postReceive %s needs a url template", routing.PostReceiveBinaryData)
				}
			}
		}
	}
	if attached.Operations != nil && attached.Operations.Pagination != nil {
		if attached.Operations.Pagination.Type != routing.PaginationOffset {
			return fmt.Sprintf("pagination type %q is not supported; use %s",
				attached.Operations.Pagination.Type, routing.PaginationOffset)
		}
	}
	return ""
}

// applyPropertyRouting attributes each property's send to the operations
// that show it, excluding the pairs a property's routing disqualifies.
func applyPropertyRouting(src *DeclarativeNode, builds []*operationBuild, report *ConvertReport) {
	for _, declared := range src.Properties {
		if declared.Key == ResourceKey || declared.Key == OperationKey {
			continue
		}
		if declared.Routing == nil {
			continue
		}
		targets := buildsInScope(declared.Show, builds)
		if len(targets) == 0 {
			continue
		}
		propRouting := declared.Routing
		if propRouting.Request != nil || propRouting.Output != nil || propRouting.Operations != nil {
			for _, build := range targets {
				if build.excluded != "" {
					continue
				}
				build.excluded = fmt.Sprintf("property %q carries request-level routing the pack format cannot express", declared.Key)
				report.exclude("resource %q operation %q: %s", build.resource, build.operation.Value, build.excluded)
			}
			continue
		}
		if propRouting.Send == nil {
			continue
		}
		send := *propRouting.Send
		if reason := checkSend(send); reason != "" {
			for _, build := range targets {
				if build.excluded != "" {
					continue
				}
				build.excluded = fmt.Sprintf("property %q: %s", declared.Key, reason)
				report.exclude("resource %q operation %q: %s", build.resource, build.operation.Value, build.excluded)
			}
			continue
		}
		send.From = declared.Key
		for _, build := range targets {
			build.sends = append(build.sends, send)
		}
	}
}

// checkSend mirrors the interpreter's own send validation, so the
// converter excludes early with the property named rather than letting
// the pack fail at install.
func checkSend(send routing.Send) string {
	if strings.TrimSpace(send.Property) == "" {
		return "send needs a destination property"
	}
	switch send.Type {
	case "", "body", "query", "path", "binary":
	default:
		return fmt.Sprintf("send type %q is not supported; use body, query, path or binary", send.Type)
	}
	if len(send.PreSend) > 0 {
		return fmt.Sprintf("preSend hooks %s are JavaScript and are not supported", strings.Join(send.PreSend, ", "))
	}
	return ""
}

// resourcesInScope lists the resources an option applies to: its show
// list, or every resource when it names none.
func resourcesInScope(show map[string][]string, resources []DeclarativeOption) []string {
	listed, scoped := show[ResourceKey]
	if !scoped {
		all := make([]string, 0, len(resources))
		for _, resource := range resources {
			all = append(all, resource.Value)
		}
		return all
	}
	known := make(map[string]bool, len(resources))
	for _, resource := range resources {
		known[resource.Value] = true
	}
	var out []string
	for _, name := range listed {
		if known[name] {
			out = append(out, name)
		}
	}
	return out
}

// buildsInScope lists the candidate operations showing a property.
func buildsInScope(show map[string][]string, builds []*operationBuild) []*operationBuild {
	resources, resourceScoped := show[ResourceKey]
	operations, operationScoped := show[OperationKey]
	in := func(list []string, value string) bool {
		for _, allowed := range list {
			if allowed == value {
				return true
			}
		}
		return false
	}
	var out []*operationBuild
	for _, build := range builds {
		if resourceScoped && !in(resources, build.resource) {
			continue
		}
		if operationScoped && !in(operations, build.operation.Value) {
			continue
		}
		out = append(out, build)
	}
	return out
}

// convertCredential requires the node's first credential by type.
func convertCredential(src *DeclarativeNode, report *ConvertReport) string {
	if len(src.Credentials) == 0 {
		report.note("the node declares no credential; the pack authenticates with none")
		return ""
	}
	first := src.Credentials[0]
	if strings.TrimSpace(first.Name) == "" {
		report.note("the node's credential has no name and is left out")
		return ""
	}
	report.note("credential type %q is required; bind a real credential after installing the pack", first.Name)
	if len(src.Credentials) > 1 {
		names := make([]string, 0, len(src.Credentials)-1)
		for _, credential := range src.Credentials[1:] {
			names = append(names, strconv.Quote(credential.Name))
		}
		report.note("credentials %s are left out; the pack carries only %q", strings.Join(names, ", "), first.Name)
	}
	return first.Name
}

// declarativeKinds maps the declarative type strings onto the closed
// property-kind set. Anything else is carried as raw JSON with a note:
// the value still reaches the request through its send, only the editor
// control is coarser.
var declarativeKinds = map[string]property.Kind{
	"string":       property.KindString,
	"number":       property.KindNumber,
	"boolean":      property.KindBoolean,
	"options":      property.KindOptions,
	"multiOptions": property.KindMultiOptions,
	"notice":       property.KindNotice,
	"json":         property.KindJSON,
	"dateTime":     property.KindDateTime,
}

// convertParameters merges one property per key, the way the definition
// requires: a parameter used by several operations is one property that
// says which resources and operations show it. An unscoped property is
// shown unconditionally, so its lists stay empty rather than naming
// everything.
func convertParameters(src *DeclarativeNode, resources []DeclarativeOption, builds []*operationBuild, report *ConvertReport) []Parameter {
	allResources := make([]string, 0, len(resources))
	for _, resource := range resources {
		allResources = append(allResources, resource.Value)
	}
	seenOperation := map[string]bool{}
	var allOperations []string
	for _, build := range builds {
		if !seenOperation[build.operation.Value] {
			seenOperation[build.operation.Value] = true
			allOperations = append(allOperations, build.operation.Value)
		}
	}
	merged := map[string]*Parameter{}
	firstKind := map[string]string{}
	unknownNoted := map[string]bool{}
	var order []string
	for _, declared := range src.Properties {
		if declared.Key == ResourceKey || declared.Key == OperationKey {
			continue
		}
		parameter, found := merged[declared.Key]
		if !found {
			parameter = &Parameter{Key: declared.Key}
			merged[declared.Key] = parameter
			order = append(order, declared.Key)
			firstKind[declared.Key] = declared.Kind
			label := strings.TrimSpace(declared.Label)
			if label == "" {
				label = declared.Key
			}
			kind, options, _ := convertKind(declared, report)
			parameter.Label = label
			parameter.Description = declared.Description
			parameter.Kind = kind
			parameter.Required = declared.Required
			parameter.Default = declared.Default
			parameter.Options = options
			parameter.TypeOptions = declared.TypeOptions
		} else {
			if first, ok := firstKind[declared.Key]; ok && first != declared.Kind && !strings.HasSuffix(first, "!") {
				report.note("parameter %q is declared as %s and %s by different operations; generated as %s",
					declared.Key, formatKind(first), formatKind(declared.Kind), parameter.Kind)
				firstKind[declared.Key] += "!"
			}
			_, options, _ := convertKind(declared, report)
			if marked := firstKind[declared.Key]; len(options) > 0 && !sameParameterOptions(parameter.Options, options) && !strings.HasSuffix(marked, "!") {
				report.note("parameter %q offers different values to different operations; generated with the first set", declared.Key)
				firstKind[declared.Key] += "!"
			}
			if declared.Required {
				parameter.Required = true
			}
		}
		parameter.Resources = union(parameter.Resources, scopedNames(declared.Show, ResourceKey, allResources, declared.Key, report, unknownNoted))
		parameter.Operations = union(parameter.Operations, scopedNames(declared.Show, OperationKey, allOperations, declared.Key, report, unknownNoted))
	}
	out := make([]Parameter, 0, len(order))
	for _, key := range order {
		out = append(out, *merged[key])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// formatKind names a declared kind the way the degradation note does, so
// two notes about one parameter agree with each other.
func formatKind(kind string) string {
	if mapped, found := declarativeKinds[kind]; found {
		return string(mapped)
	}
	return strconv.Quote(kind)
}

// scopedNames resolves one show key to the names it allows, or nil when
// the property is unscoped on that key. Unknown names are kept verbatim
// and reported once: a visibility rule that never matches is a
// transcription worth rechecking, not a silent act.
func scopedNames(show map[string][]string, key string, known []string, param string, report *ConvertReport, noted map[string]bool) []string {
	listed, scoped := show[key]
	if !scoped {
		return nil
	}
	knownSet := make(map[string]bool, len(known))
	for _, name := range known {
		knownSet[name] = true
	}
	var out []string
	for _, name := range listed {
		mark := param + "\x00" + key + "\x00" + name
		if !knownSet[name] && !noted[mark] {
			noted[mark] = true
			report.note("parameter %q shows for unknown %s %q; kept verbatim", param, key, name)
		}
		out = append(out, name)
	}
	return out
}

// sameParameterOptions reports whether two option sets offer the same
// values in the same order.
func sameParameterOptions(left, right []property.PropertyOption) bool {
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

// convertKind maps one declared kind, degrading what has no pack control
// to raw JSON with a named note.
func convertKind(declared DeclarativeProperty, report *ConvertReport) (property.Kind, []property.PropertyOption, bool) {
	kind, found := declarativeKinds[declared.Kind]
	if !found {
		report.note("parameter %q: kind %q has no pack control; carries the raw value as JSON", declared.Key, declared.Kind)
		return property.KindJSON, nil, true
	}
	if kind == property.KindOptions || kind == property.KindMultiOptions {
		options := make([]property.PropertyOption, 0, len(declared.Options))
		for _, option := range declared.Options {
			label := option.Label
			if label == "" {
				label = option.Value
			}
			options = append(options, property.PropertyOption{Label: label, Value: option.Value})
		}
		return kind, options, false
	}
	return kind, nil, false
}

// union appends the names it has not already listed, keeping first-seen
// order so the output is stable.
func union(listed []string, names []string) []string {
	seen := make(map[string]bool, len(listed)+len(names))
	for _, name := range listed {
		seen[name] = true
	}
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			listed = append(listed, name)
		}
	}
	return listed
}

// convertResourceList assembles the pack's resources in picker order.
func convertResourceList(resources []DeclarativeOption, builds []*operationBuild, report *ConvertReport) []Resource {
	_ = report
	byResource := make(map[string][]Operation, len(resources))
	for _, build := range builds {
		operation := Operation{
			Name:        build.operation.Value,
			Description: build.operation.Description,
			Method:      build.method,
			URL:         build.url,
			Sends:       append([]routing.Send(nil), build.sends...),
			Output:      build.output,
		}
		if build.pagination != nil {
			operation.Pagination = &routing.Pagination{
				Type:       build.pagination.Type,
				Properties: build.pagination.Properties,
			}
		}
		byResource[build.resource] = append(byResource[build.resource], operation)
	}
	out := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		operations := byResource[resource.Value]
		if len(operations) == 0 {
			continue
		}
		out = append(out, Resource{
			Name:        resource.Value,
			Description: resource.Description,
			Operations:  operations,
		})
	}
	return out
}

func countOperations(resources []Resource) int {
	total := 0
	for _, resource := range resources {
		total += len(resource.Operations)
	}
	return total
}
