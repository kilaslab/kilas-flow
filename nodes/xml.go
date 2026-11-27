package nodes

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The XML node's server-owned binding and type.
const (
	XMLExecutorID = "core.xml"
	XMLNodeType   = "kilasflow.xml"
)

// The XML node's modes, as n8n names them.
const (
	XMLModeToJSON = "xmlToJson"
	XMLModeToXML  = "jsonToxml"
)

// xmlMaxDepth bounds how deep a document may nest, so a hostile payload cannot
// build an arbitrarily deep value.
const xmlMaxDepth = 512

// XMLOptionKeys are the options the node implements, in the order the editor
// lists them. The importer refuses a node that sets any other, so a workflow
// never runs with a conversion its author did not write. "async" is accepted
// and ignored: it picks how a parser schedules its work, not what it returns.
var XMLOptionKeys = []string{
	"attrkey", "charkey", "explicitArray", "explicitRoot", "ignoreAttrs", "mergeAttrs",
	"normalize", "normalizeTags", "trim", "includeWhiteChars", "async",
	"cdata", "headless", "rootName", "renderOpts",
}

func xmlConvertNode() node.Definition {
	toJSON := []node.VisibilityCondition{{Key: "mode", Equals: XMLModeToJSON}}
	toXML := []node.VisibilityCondition{{Key: "mode", Equals: XMLModeToXML}}
	return node.Definition{
		Type:        XMLNodeType,
		Version:     workflow.V(1),
		DisplayName: "XML",
		Description: "Converts an XML document held in a field into JSON, or an item's JSON into an XML document.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:file-code"},
		IconColor:   "#f97316",
		Subtitle:    "{{ $parameter.mode }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: XMLModeToJSON,
				Options: []node.PropertyOption{
					{Label: "XML to JSON", Value: XMLModeToJSON},
					{Label: "JSON to XML", Value: XMLModeToXML},
				},
				Description: "XML to JSON replaces the item with the parsed document. JSON to XML replaces the item with one field holding the document.",
			},
			{
				Key: "dataPropertyName", Label: "Property Name", Kind: node.PropertyString, Default: "data",
				Description: "XML to JSON: the field that holds the XML text. JSON to XML: the field the XML text is written to.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "attrkey", Label: "Attribute Key", Kind: node.PropertyString, Default: "$",
						Description: "The key an element's attributes are collected under (when they are not merged into the element)."},
					{Key: "charkey", Label: "Character Key", Kind: node.PropertyString, Default: "_",
						Description: "The key an element's text is stored under when the element also has attributes or children."},
					{Key: "explicitArray", Label: "Explicit Array", Kind: node.PropertyBoolean, Default: false,
						Description: "Always wrap an element's children in an array, even when there is only one.", VisibleWhen: toJSON},
					{Key: "explicitRoot", Label: "Explicit Root", Kind: node.PropertyBoolean, Default: true,
						Description: "Keep the root element's name as the top-level key.", VisibleWhen: toJSON},
					{Key: "ignoreAttrs", Label: "Ignore Attributes", Kind: node.PropertyBoolean, Default: false,
						Description: "Drop element attributes.", VisibleWhen: toJSON},
					{Key: "mergeAttrs", Label: "Merge Attributes", Kind: node.PropertyBoolean, Default: true,
						Description: "Write attributes as fields of the element, beside its children, instead of under the attribute key.", VisibleWhen: toJSON},
					{Key: "normalize", Label: "Normalize", Kind: node.PropertyBoolean, Default: false,
						Description: "Collapse runs of whitespace in text to one space.", VisibleWhen: toJSON},
					{Key: "normalizeTags", Label: "Normalize Tags", Kind: node.PropertyBoolean, Default: false,
						Description: "Lower-case every element name.", VisibleWhen: toJSON},
					{Key: "trim", Label: "Trim", Kind: node.PropertyBoolean, Default: false,
						Description: "Trim whitespace from the start and end of text.", VisibleWhen: toJSON},
					{Key: "includeWhiteChars", Label: "Include White Space Text Nodes", Kind: node.PropertyBoolean, Default: false,
						Description: "Keep text that is only whitespace, which is dropped otherwise.", VisibleWhen: toJSON},
					{Key: "cdata", Label: "CDATA", Kind: node.PropertyBoolean, Default: false,
						Description: "Wrap text that contains &, < or > in a CDATA section instead of escaping it.", VisibleWhen: toXML},
					{Key: "headless", Label: "Headless", Kind: node.PropertyBoolean, Default: false,
						Description: "Leave out the XML declaration at the top of the document.", VisibleWhen: toXML},
					{Key: "rootName", Label: "Root Name", Kind: node.PropertyString, Default: "root",
						Description: "The root element's name. An item with a single top-level field uses that field as the root instead.", VisibleWhen: toXML},
					{Key: "renderOpts", Label: "Render Options", Kind: node.PropertyCollection,
						Description: "How the document is laid out.", VisibleWhen: toXML,
						Fields: []node.PropertyDefinition{
							{Key: "pretty", Label: "Pretty", Kind: node.PropertyBoolean, Default: true,
								Description: "Put each element on its own line, indented."},
							{Key: "indent", Label: "Indent", Kind: node.PropertyString, Default: "  ",
								Description: "What one level of indent is, when pretty."},
							{Key: "newline", Label: "Newline", Kind: node.PropertyString, Default: "\n",
								Description: "The line break, when pretty."},
						}},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     XMLExecutorID,
		Validate:       validateXMLConfiguration,
	}
}

func validateXMLConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["mode"]) {
		return nil
	}
	switch mode := textValue(n.Parameters["mode"], XMLModeToJSON); mode {
	case XMLModeToJSON, XMLModeToXML:
	default:
		return fmt.Errorf("mode must be xmlToJson or jsonToxml, not %q", mode)
	}
	options, _ := n.Parameters["options"].(map[string]any)
	known := map[string]bool{}
	for _, key := range XMLOptionKeys {
		known[key] = true
	}
	for key := range options {
		if !known[key] {
			return fmt.Errorf("option %q is not supported", key)
		}
	}
	return nil
}

func executeXML(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]workflow.Item, 0, len(input["main"]))
	for index, item := range input["main"] {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		converted, err := convertXML(parameters, item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		// The attachments and the provenance stay with the item; only its JSON
		// is replaced.
		built := cloneItem(item)
		built.JSON = converted
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

// convertXML is the node's behaviour over one item's resolved parameters.
func convertXML(parameters map[string]any, document map[string]any) (map[string]any, error) {
	options, _ := parameters["options"].(map[string]any)
	property := cryptoText(parameters["dataPropertyName"], "data")
	switch mode := textValue(parameters["mode"], XMLModeToJSON); mode {
	case XMLModeToJSON:
		source, found := lookupPath(document, property)
		if !found {
			return nil, fmt.Errorf("the item has no field %q holding XML", property)
		}
		text, isText := source.(string)
		if !isText {
			return nil, fmt.Errorf("field %q is not text, so it cannot be parsed as XML", property)
		}
		parsed, err := parseXML(text, readXMLParseOptions(options))
		if err != nil {
			return nil, err
		}
		if object, isObject := parsed.(map[string]any); isObject {
			return object, nil
		}
		// A root that is only text (explicit root off) has no field name of its
		// own, so it keeps the one the XML came from.
		return map[string]any{property: parsed}, nil
	case XMLModeToXML:
		text, err := buildXML(document, readXMLBuildOptions(options))
		if err != nil {
			return nil, err
		}
		return map[string]any{property: text}, nil
	default:
		return nil, fmt.Errorf("mode must be xmlToJson or jsonToxml, not %q", mode)
	}
}

// --- XML to JSON ---------------------------------------------------------------

type xmlParseOptions struct {
	attrKey, charKey                                     string
	explicitArray, explicitRoot, ignoreAttrs, mergeAttrs bool
	normalize, normalizeTags, trim, includeWhiteChars    bool
}

func optionBool(options map[string]any, key string, fallback bool) bool {
	switch typed := options[key].(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func optionText(options map[string]any, key, fallback string) string {
	if text, isText := options[key].(string); isText && text != "" {
		return text
	}
	return fallback
}

func readXMLParseOptions(options map[string]any) xmlParseOptions {
	return xmlParseOptions{
		attrKey:           optionText(options, "attrkey", "$"),
		charKey:           optionText(options, "charkey", "_"),
		explicitArray:     optionBool(options, "explicitArray", false),
		explicitRoot:      optionBool(options, "explicitRoot", true),
		ignoreAttrs:       optionBool(options, "ignoreAttrs", false),
		mergeAttrs:        optionBool(options, "mergeAttrs", true),
		normalize:         optionBool(options, "normalize", false),
		normalizeTags:     optionBool(options, "normalizeTags", false),
		trim:              optionBool(options, "trim", false),
		includeWhiteChars: optionBool(options, "includeWhiteChars", false),
	}
}

// xmlFrame is one open element while the document is read.
type xmlFrame struct {
	name   string
	fields map[string]any
	attrs  map[string]any
	text   strings.Builder
}

// parseXML reads a document into the shape the common XML-to-JS converters
// give: an element with only text is that text, an element with attributes or
// children is an object, and a name that repeats becomes an array.
func parseXML(source string, options xmlParseOptions) (any, error) {
	decoder := xml.NewDecoder(strings.NewReader(source))
	// The text is already decoded, so a declaration naming another encoding
	// describes bytes this decoder never sees.
	decoder.CharsetReader = func(_ string, reader io.Reader) (io.Reader, error) { return reader, nil }

	var stack []*xmlFrame
	var result any
	finished := false
	for {
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the XML could not be parsed: %w", err)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if finished {
				return nil, fmt.Errorf("the XML could not be parsed: more than one root element")
			}
			if len(stack) >= xmlMaxDepth {
				return nil, fmt.Errorf("the XML nests deeper than %d elements", xmlMaxDepth)
			}
			frame := &xmlFrame{name: xmlTagName(typed.Name, options), fields: map[string]any{}, attrs: map[string]any{}}
			if !options.ignoreAttrs {
				for _, attribute := range typed.Attr {
					name := xmlRawName(attribute.Name)
					if options.mergeAttrs {
						assignXMLField(frame.fields, name, attribute.Value, options.explicitArray)
					} else {
						frame.attrs[name] = attribute.Value
					}
				}
			}
			stack = append(stack, frame)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(typed)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("the XML could not be parsed: unexpected closing tag")
			}
			frame := stack[len(stack)-1]
			if closing := xmlTagName(typed.Name, options); closing != frame.name {
				return nil, fmt.Errorf("the XML could not be parsed: element %q is closed by %q", frame.name, closing)
			}
			stack = stack[:len(stack)-1]
			value := finishXMLFrame(frame, options)
			if len(stack) > 0 {
				assignXMLField(stack[len(stack)-1].fields, frame.name, value, options.explicitArray)
				continue
			}
			finished = true
			if options.explicitRoot {
				result = map[string]any{frame.name: value}
			} else {
				result = value
			}
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("the XML could not be parsed: element %q is never closed", stack[len(stack)-1].name)
	}
	if !finished {
		return nil, fmt.Errorf("the XML has no root element")
	}
	return result, nil
}

func xmlRawName(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}

func xmlTagName(name xml.Name, options xmlParseOptions) string {
	raw := xmlRawName(name)
	if options.normalizeTags {
		return strings.ToLower(raw)
	}
	return raw
}

// finishXMLFrame turns a closed element into its value.
func finishXMLFrame(frame *xmlFrame, options xmlParseOptions) any {
	text := frame.text.String()
	if strings.TrimSpace(text) == "" && !options.includeWhiteChars {
		text = ""
	} else {
		if options.trim {
			text = strings.TrimSpace(text)
		}
		if options.normalize {
			// strings.Fields drops the edges too, which is what normalising
			// whitespace means.
			text = strings.Join(strings.Fields(text), " ")
		}
	}
	if len(frame.attrs) > 0 {
		frame.fields[options.attrKey] = frame.attrs
	}
	if text != "" {
		frame.fields[options.charKey] = text
	}
	if len(frame.fields) == 0 {
		return ""
	}
	// An element that is only text is that text.
	if len(frame.fields) == 1 && text != "" {
		return text
	}
	return frame.fields
}

// assignXMLField adds a child or merged attribute: a name seen again turns the
// field into an array, and explicitArray makes it one from the first.
func assignXMLField(fields map[string]any, name string, value any, explicitArray bool) {
	existing, present := fields[name]
	switch {
	case !present && explicitArray:
		fields[name] = []any{value}
	case !present:
		fields[name] = value
	default:
		if list, isList := existing.([]any); isList {
			fields[name] = append(list, value)
		} else {
			fields[name] = []any{existing, value}
		}
	}
}

// --- JSON to XML ---------------------------------------------------------------

type xmlBuildOptions struct {
	attrKey, charKey, rootName string
	cdata, headless            bool
	pretty                     bool
	indent, newline            string
}

func readXMLBuildOptions(options map[string]any) xmlBuildOptions {
	built := xmlBuildOptions{
		attrKey:  optionText(options, "attrkey", "$"),
		charKey:  optionText(options, "charkey", "_"),
		rootName: optionText(options, "rootName", "root"),
		cdata:    optionBool(options, "cdata", false),
		headless: optionBool(options, "headless", false),
		pretty:   true, indent: "  ", newline: "\n",
	}
	if render, ok := options["renderOpts"].(map[string]any); ok {
		built.pretty = optionBool(render, "pretty", true)
		if text, isText := render["indent"].(string); isText {
			built.indent = text
		}
		if text, isText := render["newline"].(string); isText {
			built.newline = text
		}
	}
	if !built.pretty {
		built.indent, built.newline = "", ""
	}
	return built
}

// buildXML writes a document. The root is the item's single top-level field
// when it has exactly one and no other root name was chosen, and the root
// name option otherwise.
func buildXML(document map[string]any, options xmlBuildOptions) (string, error) {
	rootName := options.rootName
	var content any = document
	if len(document) == 1 && rootName == "root" {
		for key, value := range document {
			if _, isList := value.([]any); !isList {
				rootName, content = key, value
			}
		}
	}
	var builder strings.Builder
	if !options.headless {
		builder.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		builder.WriteString(options.newline)
	}
	if err := writeXMLElement(&builder, rootName, content, options, 0); err != nil {
		return "", err
	}
	built := builder.String()
	if options.newline != "" {
		built = strings.TrimSuffix(built, options.newline)
	}
	return built, nil
}

func validXMLName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		switch {
		case unicode.IsLetter(character) || character == '_' || character == ':':
		case index > 0 && (unicode.IsDigit(character) || character == '-' || character == '.'):
		default:
			return false
		}
	}
	return true
}

func xmlScalarText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return textValue(typed, "")
	}
}

var (
	xmlTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
)

func xmlText(text string, options xmlBuildOptions) string {
	if options.cdata && strings.ContainsAny(text, "&<>") {
		return "<![CDATA[" + strings.ReplaceAll(text, "]]>", "]]]]><![CDATA[>") + "]]>"
	}
	return xmlTextEscaper.Replace(text)
}

// writeXMLElement writes one named value; a list writes one element per entry.
func writeXMLElement(builder *strings.Builder, name string, value any, options xmlBuildOptions, depth int) error {
	if !validXMLName(name) {
		return fmt.Errorf("%q cannot be an XML element name", name)
	}
	if depth > xmlMaxDepth {
		return fmt.Errorf("the value nests deeper than %d elements", xmlMaxDepth)
	}
	indent := strings.Repeat(options.indent, depth)
	if list, isList := value.([]any); isList {
		for _, entry := range list {
			if err := writeXMLElement(builder, name, entry, options, depth); err != nil {
				return err
			}
		}
		return nil
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		text := xmlScalarText(value)
		if text == "" {
			fmt.Fprintf(builder, "%s<%s/>%s", indent, name, options.newline)
			return nil
		}
		fmt.Fprintf(builder, "%s<%s>%s</%s>%s", indent, name, xmlText(text, options), name, options.newline)
		return nil
	}

	var attributes strings.Builder
	if attrs, ok := object[options.attrKey].(map[string]any); ok {
		for _, key := range sortedMapKeys(attrs) {
			if !validXMLName(key) {
				return fmt.Errorf("%q cannot be an XML attribute name", key)
			}
			fmt.Fprintf(&attributes, ` %s="%s"`, key, xmlAttrEscaper.Replace(xmlScalarText(attrs[key])))
		}
	}
	text := ""
	if raw, present := object[options.charKey]; present {
		text = xmlScalarText(raw)
	}
	children := make([]string, 0, len(object))
	for _, key := range sortedMapKeys(object) {
		if key == options.attrKey || key == options.charKey {
			continue
		}
		children = append(children, key)
	}
	if len(children) == 0 {
		if text == "" {
			fmt.Fprintf(builder, "%s<%s%s/>%s", indent, name, attributes.String(), options.newline)
		} else {
			fmt.Fprintf(builder, "%s<%s%s>%s</%s>%s", indent, name, attributes.String(), xmlText(text, options), name, options.newline)
		}
		return nil
	}
	fmt.Fprintf(builder, "%s<%s%s>", indent, name, attributes.String())
	if text != "" {
		builder.WriteString(xmlText(text, options))
	}
	builder.WriteString(options.newline)
	for _, key := range children {
		if err := writeXMLElement(builder, key, object[key], options, depth+1); err != nil {
			return err
		}
	}
	fmt.Fprintf(builder, "%s</%s>%s", indent, name, options.newline)
	return nil
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
