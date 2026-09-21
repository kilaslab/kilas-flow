package sidecarnode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Category is the panel group every community node is filed under, so a node
// an operator installed is never mistaken for one this project ships.
const Category = "Community"

// errorPortName is the output port the engine appends when a node continues on
// a separate error branch. A package may not use the name itself: see
// outputPorts.
const errorPortName = "error"

// sharedSettingKeys are the parameter keys every KilasFlow node already
// carries as a shared setting (nodes/core.go sharedSettings). A community
// parameter with one of these keys is refused at registration by
// validateGroupsDoNotCollide, so the node is excluded here with a reason
// instead of taking the boot down with it.
var sharedSettingKeys = map[string]bool{
	"continueOnFail":   true,
	"retryOnFail":      true,
	"timeoutSeconds":   true,
	"maxTries":         true,
	"waitBetweenTries": true,
	"alwaysOutputData": true,
}

// propertyKinds is the closed set of n8n property shapes this sidecar can
// marshal. Everything else is an exclusion, never a partial mapping: a
// resource locator sent to the package as a bare object is a request the user
// did not write.
var propertyKinds = map[string]property.Kind{
	"string":          property.KindString,
	"number":          property.KindNumber,
	"boolean":         property.KindBoolean,
	"options":         property.KindOptions,
	"multiOptions":    property.KindMultiOptions,
	"json":            property.KindJSON,
	"dateTime":        property.KindDateTime,
	"notice":          property.KindNotice,
	"collection":      property.KindCollection,
	"fixedCollection": property.KindFixedCollection,
}

// Converted is one node definition Convert built together with the index entry
// the executor dispatches through.
type Converted struct {
	Definition node.Definition
	Ref        NodeRef
}

// Exclusion is one node, credential or file a load left out, with the reason.
// An exclusion never fails the load: one broken node in one package must not
// take the boot down.
type Exclusion struct {
	Package string
	File    string
	Node    string
	Reason  string
}

func (exclusion Exclusion) String() string {
	where := exclusion.Package
	if exclusion.File != "" {
		where += " " + exclusion.File
	}
	if exclusion.Node != "" {
		where += " (" + exclusion.Node + ")"
	}
	return where + ": " + exclusion.Reason
}

// refusal is a shape Convert will not map. It is an exclusion, not an error:
// the caller turns it into one Exclusion and keeps going.
type refusal struct{ reason string }

func (refusal *refusal) Error() string { return refusal.reason }

func refuse(format string, args ...any) error {
	return &refusal{reason: fmt.Sprintf(format, args...)}
}

// Convert turns one loaded package into node definitions and credential types.
//
// It is pure: no registry, no process, no I/O. The caller (Load) decides what
// to do with a definition the real registry refuses; here, every shape this
// build cannot serve becomes an Exclusion naming the node and the reason.
//
// What v1 deliberately does not map, all of it presentation rather than
// behaviour: a node's icon (the package's artwork is not copied into this
// deployment), its subtitle template, its picker codex, and a credential
// class's documentationUrl (the credential type has no field for it). The
// editor shows such a node with the default glyph and no subtitle, which is a
// smaller loss than a definition the executor cannot marshal.
func Convert(pkg PackageInfo) ([]Converted, []credentials.Type, []Exclusion, error) {
	converted := make([]Converted, 0, len(pkg.Nodes))
	registered := make([]credentials.Type, 0, len(pkg.Credentials))
	excluded := make([]Exclusion, 0)

	// Credentials come first: a node's credential references are checked
	// against what the package itself declares, and a node that names a
	// credential this package failed to declare is excluded below.
	declared := map[string]bool{}
	for _, declaredCredential := range pkg.Credentials {
		credentialType, err := convertCredential(declaredCredential)
		if err != nil {
			excluded = append(excluded, Exclusion{Package: pkg.Name, File: declaredCredential.File, Node: declaredCredential.Name, Reason: err.Error()})
			continue
		}
		if declared[credentialType.ID] {
			excluded = append(excluded, Exclusion{Package: pkg.Name, File: declaredCredential.File, Node: declaredCredential.Name,
				Reason: fmt.Sprintf("credential type %q is declared more than once in this package", credentialType.ID)})
			continue
		}
		declared[credentialType.ID] = true
		registered = append(registered, credentialType)
	}

	// A node file that declares several versions is listed once per version by
	// the runner, so the same (type, version) pair arrives more than once. Only
	// the first is converted.
	seen := map[string]bool{}
	for _, declaredNode := range pkg.Nodes {
		definitions, err := convertNode(pkg, declaredNode, declared)
		if err != nil {
			excluded = append(excluded, Exclusion{Package: pkg.Name, File: declaredNode.File, Node: declaredNode.Name, Reason: err.Error()})
			continue
		}
		for _, one := range definitions {
			key := one.Definition.Type + "@" + one.Definition.Version.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			converted = append(converted, one)
		}
	}
	return converted, registered, excluded, nil
}

// convertNode builds one node type's definitions, one per declared version.
func convertNode(pkg PackageInfo, declared PackageNode, packageCredentials map[string]bool) ([]Converted, error) {
	description := declared.Description
	if len(description) == 0 {
		return nil, refuse("the node class carries no description object")
	}
	name := text(description, "name")
	if name == "" {
		return nil, refuse("the node description carries no name")
	}
	if !declared.Execute {
		return nil, refuse("the node has no execute() method, and this sidecar runs nodes with one only")
	}
	if len(declared.Unsupported) > 0 {
		return nil, refuse("the node declares %s, which this sidecar does not run", strings.Join(declared.Unsupported, ", "))
	}

	group, err := nodeGroup(description)
	if err != nil {
		return nil, err
	}
	inputs, err := inputPorts(description)
	if err != nil {
		return nil, err
	}
	outputs, err := outputPorts(description)
	if err != nil {
		return nil, err
	}
	requirements, err := credentialRequirements(description, packageCredentials)
	if err != nil {
		return nil, err
	}
	converter := &converter{}
	parameters, err := converter.properties(list(description["properties"]))
	if err != nil {
		return nil, err
	}
	versions, err := parseVersions(declared.Version)
	if err != nil {
		return nil, err
	}

	// The package's own name is slugged because an npm name may be scoped and
	// the type is used as a URL path segment (/node-types/{type}/icon). The
	// node's name is slugged too: both halves of the identifier have to be
	// safe, or a package with a typographic name would produce a type the
	// icon route cannot address.
	nodeType := "sidecar." + slug(pkg.Name) + "." + slug(name)
	definition := node.Definition{
		Type:        nodeType,
		DisplayName: text(description, "displayName"),
		Description: text(description, "description"),
		Category:    Category,
		Group:       []node.NodeGroup{group},
		Inputs:      inputs,
		Outputs:     outputs,
		Parameters:  parameters,
		ExecutorID:  ExecutorID,
		Credentials: requirements,
	}

	converted := make([]Converted, 0, len(versions))
	for _, version := range versions {
		one := definition
		one.Version = version
		// Trial registration is what catches everything the checks above do
		// not: an invalid visibility key, a duplicate nested key, an unnamed
		// group. The registry's own message is the exclusion reason, so the
		// reason and the rule can never drift apart.
		if err := dryRun(one); err != nil {
			return nil, refuse("%s", err)
		}
		converted = append(converted, Converted{
			Definition: one,
			Ref: NodeRef{
				Package:         pkg.Name,
				PackageVersion:  pkg.Version,
				Type:            one.Type,
				Version:         version,
				Name:            name,
				NodeVersion:     version.Float(),
				HiddenDefaults:  converter.hidden,
				CredentialTypes: credentialTypesOf(requirements),
			},
		})
	}
	return converted, nil
}

// dryRun registers the definition into a scratch catalogue.
//
// A second registry rather than the real one: a refusal here excludes this
// node and leaves every other package loadable, while only a collision in the
// real catalogue is severe enough to refuse the boot.
func dryRun(definition node.Definition) error {
	scratch := node.NewRegistry()
	return scratch.RegisterFrom(node.SourceSidecar, definition)
}

// nodeGroup maps the package's own behavioural group onto this registry's
// closed set. A trigger has no execute surface in v1, so it is excluded.
func nodeGroup(description map[string]any) (node.NodeGroup, error) {
	groups := stringsOf(description["group"])
	for _, declared := range groups {
		if declared == string(node.GroupTrigger) {
			return "", refuse("the node is a trigger, and this sidecar runs nodes with an execute() method only")
		}
	}
	for _, declared := range groups {
		switch declared {
		case string(node.GroupInput):
			return node.GroupInput, nil
		case string(node.GroupOutput):
			return node.GroupOutput, nil
		case string(node.GroupTransform):
			return node.GroupTransform, nil
		case string(node.GroupOrganization):
			return node.GroupOrganization, nil
		}
	}
	return node.GroupTransform, nil
}

// inputPorts requires exactly one main input.
func inputPorts(description map[string]any) ([]workflow.Port, error) {
	declared, err := connectionNames(description["inputs"], "input")
	if err != nil {
		return nil, err
	}
	if len(declared) == 1 && declared[0] == "main" {
		return []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}, nil
	}
	if len(declared) == 0 {
		return nil, refuse("the node takes no main input, and this sidecar serves nodes that do")
	}
	return nil, refuse("the node takes %s, and this sidecar serves exactly one main input", joinQuoted(declared))
}

// connectionNames reads a node's declared connection types.
//
// A declaration this build cannot read — an expression, an object, anything
// that is not a string — is refused rather than skipped: dropping it would make
// the node look like it has no input at all, which is a different and more
// confusing failure than "this shape is not supported".
func connectionNames(declared any, direction string) ([]string, error) {
	entries, isList := declared.([]any)
	if declared == nil {
		return nil, nil
	}
	if !isList {
		return nil, refuse("the node declares its %ss in a shape this sidecar does not read", direction)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, isText := entry.(string)
		if !isText {
			return nil, refuse("the node declares an %s that is not a named connection type", direction)
		}
		names = append(names, strings.TrimSpace(name))
	}
	return names, nil
}

// outputPorts builds one port per declared output.
//
// Port names must be unique in the registry, and n8n allows a package to
// declare two outputs both called `main` with no outputNames at all, so names
// are synthesised when the package does not supply a usable one.
func outputPorts(description map[string]any) ([]workflow.Port, error) {
	kinds, err := connectionNames(description["outputs"], "output")
	if err != nil {
		return nil, err
	}
	if len(kinds) == 0 {
		return nil, refuse("the node declares no output")
	}
	names := stringsOf(description["outputNames"])
	ports := make([]workflow.Port, 0, len(kinds))
	used := map[string]bool{}
	for index, kind := range kinds {
		if kind != "main" {
			return nil, refuse("output %d is a %q connection, and this sidecar serves main only", index+1, kind)
		}
		if index < len(names) && strings.TrimSpace(names[index]) == errorPortName {
			// The engine appends an error port of this name for a node that
			// continues on an error branch, and sizes the executor's answer
			// from the last port's name. A package output of the same name
			// would make that sizing wrong.
			return nil, refuse("the node names an output %q, which is reserved for the error branch the engine adds", errorPortName)
		}
		name := ""
		if index < len(names) {
			name = slug(strings.TrimSpace(names[index]))
		}
		if name == "" || used[name] {
			name = defaultPortName(index)
			for used[name] {
				name += "_"
			}
		}
		used[name] = true
		ports = append(ports, workflow.Port{Name: name, Kind: workflow.ConnectionMain})
	}
	return ports, nil
}

// defaultPortName names an output the package left unnamed, or named twice.
func defaultPortName(index int) string {
	if index == 0 {
		return "main"
	}
	return "main" + strconv.Itoa(index+1)
}

// credentialRequirements maps the node's own credential references.
//
// A reference to a type neither the package nor this deployment declares is an
// exclusion: the node could not authenticate even if it ran.
func credentialRequirements(description map[string]any, packageCredentials map[string]bool) ([]node.CredentialRequirement, error) {
	declared := mapsOf(description["credentials"])
	requirements := make([]node.CredentialRequirement, 0, len(declared))
	for _, entry := range declared {
		name := text(entry, "name")
		if name == "" {
			return nil, refuse("the node declares a credential with no name")
		}
		if !packageCredentials[name] {
			if _, builtin := credentials.Default().Get(name); !builtin {
				return nil, refuse("the node needs credential type %q, which neither the package nor this deployment declares", name)
			}
		}
		// A credential shown only for some parameter values cannot be demanded
		// of every configuration: the compiler would refuse a workflow that
		// never reaches the branch that needs it.
		displayOptions, shown := entry["displayOptions"]
		requirements = append(requirements, node.CredentialRequirement{
			Type:     name,
			Required: boolOf(entry["required"]) && !(shown && displayOptions != nil),
		})
	}
	return requirements, nil
}

// credentialTypesOf lists the types a definition references, which is the set
// the executor will resolve at run time.
func credentialTypesOf(requirements []node.CredentialRequirement) []string {
	if len(requirements) == 0 {
		return nil
	}
	types := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		types = append(types, requirement.Type)
	}
	sort.Strings(types)
	return types
}

// converter carries the state one node's property walk accumulates: the
// defaults of the properties the editor is not allowed to show.
type converter struct {
	hidden map[string]any
}

// properties maps one level of property declarations, skipping hidden ones
// into the hidden-default set.
func (converter *converter) properties(declared []any) ([]node.PropertyDefinition, error) {
	properties := make([]node.PropertyDefinition, 0, len(declared))
	for _, entry := range declared {
		built, err := converter.property(mapOf(entry))
		if err != nil {
			return nil, err
		}
		if built == nil {
			continue
		}
		properties = append(properties, *built)
	}
	return properties, nil
}

// property maps one n8n property declaration.
func (converter *converter) property(raw map[string]any) (*node.PropertyDefinition, error) {
	if raw == nil {
		return nil, refuse("a property is not an object")
	}
	key, label := text(raw, "name"), text(raw, "displayName")
	if key == "" || label == "" {
		return nil, refuse("a property declares no name or no label")
	}
	declaredKind := text(raw, "type")
	if declaredKind == "hidden" {
		// A hidden property has no control in the editor, but the package can
		// still read it with getNodeParameter, so its default has to reach the
		// run. It travels beside the definition rather than inside it.
		if converter.hidden == nil {
			converter.hidden = map[string]any{}
		}
		converter.hidden[key] = expressionDefault(raw["default"])
		return nil, nil
	}
	kind, supported := propertyKinds[declaredKind]
	if !supported {
		return nil, refuse("the property %q is a %s, which this sidecar does not support", key, declaredKind)
	}
	if sharedSettingKeys[key] {
		return nil, refuse("the property %q has the same key as the shared setting every node carries, which registration refuses", key)
	}

	built := &node.PropertyDefinition{
		Key:         key,
		Label:       label,
		Description: text(raw, "description"),
		Kind:        kind,
		Required:    boolOf(raw["required"]),
		TypeOptions: typeOptions(raw["typeOptions"]),
	}
	if kind != property.KindNotice {
		// A notice holds no value at all: giving it a default would write one
		// into the node's stored parameters on the next save.
		built.Default = expressionDefault(raw["default"])
	}

	switch kind {
	case property.KindOptions, property.KindMultiOptions:
		if loader := loadOptionsMethod(raw["typeOptions"]); loader != "" {
			// A dynamic list needs a loadOptionsMethod hook this sidecar does
			// not serve. A free-text field is the honest downgrade: the value
			// still reaches the package, and the editor says why the list is
			// missing rather than showing an empty one.
			built.Kind = property.KindString
			built.TypeOptions = nil
			built.Description = joinDescription(built.Description,
				fmt.Sprintf("Values are loaded at run time by %q, which the JavaScript sidecar does not serve; enter the value instead.", loader))
			break
		}
		options, err := propertyOptions(raw["options"])
		if err != nil {
			return nil, err
		}
		built.Options = options
	case property.KindCollection:
		fields, err := converter.properties(list(raw["options"]))
		if err != nil {
			return nil, err
		}
		built.Fields = fields
	case property.KindFixedCollection:
		groups, err := converter.groups(raw["options"])
		if err != nil {
			return nil, err
		}
		built.Groups = groups
	}

	visibility, err := visibilityOf(raw["displayOptions"])
	if err != nil {
		return nil, err
	}
	built.DisplayOptions = visibility
	return built, nil
}

// groups maps a fixed collection's repeatable groups.
func (converter *converter) groups(declared any) ([]node.PropertyGroup, error) {
	entries := mapsOf(declared)
	groups := make([]node.PropertyGroup, 0, len(entries))
	for _, entry := range entries {
		key, label := text(entry, "name"), text(entry, "displayName")
		if key == "" || label == "" {
			return nil, refuse("a fixed collection declares a group with no name or no label")
		}
		fields, err := converter.properties(list(entry["values"]))
		if err != nil {
			return nil, err
		}
		groups = append(groups, node.PropertyGroup{Key: key, Label: label, Fields: fields})
	}
	return groups, nil
}

// propertyOptions maps a static option list. A non-string value is refused
// rather than stringified: the package compares the value it gets by identity,
// and "1" is not 1.
func propertyOptions(declared any) ([]node.PropertyOption, error) {
	entries := mapsOf(declared)
	options := make([]node.PropertyOption, 0, len(entries))
	for _, entry := range entries {
		label := text(entry, "name")
		value, isText := entry["value"].(string)
		if label == "" || !isText {
			return nil, refuse("an option of this property has a value that is not a string, which this sidecar cannot represent")
		}
		options = append(options, node.PropertyOption{Label: label, Value: value})
	}
	return options, nil
}

// typeOptions maps the display hints this registry models, dropping the ones it
// does not (loadOptionsMethod is handled by the caller).
func typeOptions(declared any) *property.TypeOptions {
	raw := mapOf(declared)
	if len(raw) == 0 {
		return nil
	}
	built := &property.TypeOptions{}
	set := false
	if value, ok := raw["password"].(bool); ok {
		built.Password, set = value, true
	}
	if value, ok := raw["rows"].(float64); ok {
		built.Rows, set = int(value), true
	}
	if value, ok := raw["multipleValues"].(bool); ok {
		built.MultipleValues, set = value, true
	}
	if value, ok := raw["multipleValueButtonText"].(string); ok {
		built.MultipleValueButtonText, set = value, true
	}
	if value, ok := raw["minValue"].(float64); ok {
		built.MinValue, set = &value, true
	}
	if value, ok := raw["maxValue"].(float64); ok {
		built.MaxValue, set = &value, true
	}
	if value, ok := raw["numberPrecision"].(float64); ok {
		precision := int(value)
		built.NumberPrecision, set = &precision, true
	}
	if !set {
		return nil
	}
	return built
}

// loadOptionsMethod reports the run-time loader a property names, if any.
func loadOptionsMethod(declared any) string {
	return text(mapOf(declared), "loadOptionsMethod")
}

// visibilityOf maps displayOptions.show/hide onto this registry's visibility
// rule.
//
// Keys are visited in sorted order so a definition built from the same package
// twice is byte-identical. An `@version` key is carried through: it is the one
// pseudo-key this registry supports, and an unsupported one is refused by the
// trial registration rather than silently dropped here.
func visibilityOf(declared any) (property.Visibility, error) {
	raw := mapOf(declared)
	if len(raw) == 0 {
		return property.Visibility{}, nil
	}
	built := property.Visibility{}
	for _, direction := range []struct {
		key  string
		into *[]property.Condition
	}{{"show", &built.Show}, {"hide", &built.Hide}} {
		conditions, present := raw[direction.key]
		if !present || conditions == nil {
			continue
		}
		fields := mapOf(conditions)
		if fields == nil {
			return property.Visibility{}, refuse("displayOptions.%s is not an object", direction.key)
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "" {
				return property.Visibility{}, refuse("displayOptions.%s names no key", direction.key)
			}
			values := list(fields[key])
			if values == nil {
				// A single value is as valid a declaration as a list of one.
				values = []any{fields[key]}
			}
			*direction.into = append(*direction.into, property.Condition{Key: key, Values: values})
		}
	}
	return built, nil
}

// expressionDefault turns n8n's `={{ … }}` default into this registry's
// expression marker.
//
// The marker matters: a default that is an expression has to be resolved per
// item before it reaches the package, and a plain string that happens to
// contain braces is data. n8n marks the expression with a leading `=`; this
// registry spells the same rule as an object, which is what
// expression.Resolve and the editor both read.
func expressionDefault(value any) any {
	declared, isText := value.(string)
	if !isText {
		return value
	}
	if !strings.HasPrefix(declared, "=") || !strings.Contains(declared, "{{") {
		return value
	}
	return map[string]any{"mode": "expression", "value": strings.TrimPrefix(declared, "=")}
}

// convertCredential maps one credential class.
//
// Every field of a community credential is marked secret. KilasFlow encrypts a
// credential's fields as one blob and the package's own `authenticate` block is
// not run in v1, so this build cannot tell a non-secret field from a secret one
// by inspection; withholding all of them is the failure that leaks nothing. The
// cost is documented: a field such as a base URL also becomes write-only.
func convertCredential(declared PackageCredential) (credentials.Type, error) {
	if strings.TrimSpace(declared.Name) == "" {
		return credentials.Type{}, refuse("a credential class carries no name")
	}
	converter := &converter{}
	properties := make([]node.PropertyDefinition, 0, len(declared.Properties))
	for _, raw := range declared.Properties {
		if text(raw, "type") == "hidden" {
			return credentials.Type{}, refuse("the credential field %q is hidden, and a credential's fields must all be visible so the API can mask them", text(raw, "name"))
		}
		built, err := converter.property(raw)
		if err != nil {
			return credentials.Type{}, err
		}
		if built == nil {
			continue
		}
		properties = append(properties, *built)
	}
	secrets := make([]string, 0, len(properties))
	for _, field := range properties {
		secrets = append(secrets, field.Key)
	}
	displayName := strings.TrimSpace(declared.DisplayName)
	if displayName == "" {
		displayName = declared.Name
	}
	return credentials.Type{
		ID:          declared.Name,
		DisplayName: displayName,
		Properties:  properties,
		Secrets:     secrets,
	}, nil
}

// parseVersions reads a node's declared version: a number, a numeric string, or
// an array of either.
func parseVersions(declared json.RawMessage) ([]workflow.TypeVersion, error) {
	if len(declared) == 0 {
		return nil, refuse("the node declares no version")
	}
	var value any
	if err := json.Unmarshal(declared, &value); err != nil {
		return nil, refuse("the node's version is not readable: %v", err)
	}
	texts, err := versionTexts(value)
	if err != nil {
		return nil, err
	}
	versions := make([]workflow.TypeVersion, 0, len(texts))
	for _, text := range texts {
		version, err := workflow.ParseTypeVersion(text)
		if err != nil {
			return nil, refuse("the node's version %q is not one this catalogue can register: %v", text, err)
		}
		versions = append(versions, version)
	}
	return versions, nil
}

func versionTexts(value any) ([]string, error) {
	switch typed := value.(type) {
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, refuse("the node declares an empty version")
		}
		return []string{strings.TrimSpace(typed)}, nil
	case []any:
		texts := make([]string, 0, len(typed))
		for _, entry := range typed {
			inner, err := versionTexts(entry)
			if err != nil {
				return nil, err
			}
			texts = append(texts, inner...)
		}
		if len(texts) == 0 {
			return nil, refuse("the node declares an empty version list")
		}
		return texts, nil
	default:
		return nil, refuse("the node's version is not a number or a list of numbers")
	}
}

// slug makes one identifier segment safe to use in a node type, which is also a
// URL path segment (/node-types/{type}/icon).
//
// An npm name may be scoped, so `@scope/pkg` becomes `scope-pkg`. Everything
// outside the unreserved set becomes a dash: a type the icon route cannot
// address is a node whose artwork 404s forever.
func slug(name string) string {
	var built strings.Builder
	for _, character := range name {
		switch {
		case character == '@':
			continue
		case character == '/':
			built.WriteByte('-')
		case isUnreserved(character):
			built.WriteRune(character)
		default:
			built.WriteByte('-')
		}
	}
	return built.String()
}

func isUnreserved(character rune) bool {
	switch {
	case character >= 'a' && character <= 'z':
		return true
	case character >= 'A' && character <= 'Z':
		return true
	case character >= '0' && character <= '9':
		return true
	}
	return character == '.' || character == '_' || character == '-'
}

// --- raw description accessors --------------------------------------------
//
// A package's description is third-party JSON, so every read has to survive a
// shape it did not expect: the accessors below return the zero value rather
// than panicking, and the checks that matter (a missing name, an unsupported
// kind) happen where the value is used.

func text(source map[string]any, key string) string {
	value, _ := source[key].(string)
	return strings.TrimSpace(value)
}

func boolOf(value any) bool {
	flag, _ := value.(bool)
	return flag
}

func list(value any) []any {
	entries, _ := value.([]any)
	return entries
}

func mapOf(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func mapsOf(value any) []map[string]any {
	entries := list(value)
	if entries == nil {
		return nil
	}
	maps := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if object := mapOf(entry); object != nil {
			maps = append(maps, object)
		}
	}
	return maps
}

func stringsOf(value any) []string {
	entries := list(value)
	if entries == nil {
		return nil
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		if value, ok := entry.(string); ok {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values
}

func joinQuoted(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return strings.Join(quoted, ", ")
}

func joinDescription(existing, added string) string {
	if strings.TrimSpace(existing) == "" {
		return added
	}
	return existing + " " + added
}
