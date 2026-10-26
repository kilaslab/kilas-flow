package nodes

// The XML utility node (FEAT-re138f): XML to JSON and back, with the options
// n8n's own XML node exposes and n8n's defaults for them — attributes ignored
// by default, arrays collapsed to scalars when only one element carries a
// name, tag values parsed to numbers and booleans.
//
// Both directions are hand-written over the standard library's XML tokens, so
// the node is pure Go with no credential and no network. The parameter keys
// are n8n's own (`mode`, `xml`, `json`, `options.*`), so an imported node
// maps by type name alone.
import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	XMLExecutorID = "core.xml"
	XMLNodeType   = "kilasflow.xml"
)

const (
	XMLOperationToJSON   = "xmlToJson"
	XMLOperationFromJSON = "jsonToXml"
)

// The defaults mirror n8n's own, which in turn mirror the xml2js options its
// node documents: attributes and namespaces dropped, single-child arrays
// collapsed, tag values parsed, values trimmed.
type xmlOptions struct {
	explicitArray       bool
	ignoreAttributes    bool
	mergeAttributes     bool
	attributeNamePrefix string
	textNodeName        string
	parseTagValue       bool
	trimValues          bool
}

func defaultXMLOptions() xmlOptions {
	return xmlOptions{
		explicitArray:       false,
		ignoreAttributes:    true,
		mergeAttributes:     true,
		attributeNamePrefix: "@",
		textNodeName:        "#text",
		parseTagValue:       true,
		trimValues:          true,
	}
}

func readXMLOptions(parameters map[string]any) xmlOptions {
	options := defaultXMLOptions()
	collection := mapValue(parameters["options"])
	if value, present := collection["explicitArray"].(bool); present {
		options.explicitArray = value
	}
	if value, present := collection["ignoreAttributes"].(bool); present {
		options.ignoreAttributes = value
	}
	if value, present := collection["mergeAttributes"].(bool); present {
		options.mergeAttributes = value
	}
	if value := strings.TrimSpace(textValue(collection["attributeNamePrefix"], "")); value != "" {
		options.attributeNamePrefix = value
	}
	if value := strings.TrimSpace(textValue(collection["textNodeName"], "")); value != "" {
		options.textNodeName = value
	}
	if value, present := collection["parseTagValue"].(bool); present {
		options.parseTagValue = value
	}
	if value, present := collection["trimValues"].(bool); present {
		options.trimValues = value
	}
	return options
}

// xmlConvertNode is the XML utility node's definition; it is named for the
// conversion rather than the format because the RSS reader already has a type
// of this package's own called xmlNode.
func xmlConvertNode() node.Definition {
	return node.Definition{
		Type:        XMLNodeType,
		Version:     workflow.V(1),
		DisplayName: "XML",
		Description: "Converts XML to JSON and JSON to XML, with n8n's xml2js options: attribute prefixes, text nodes, arrays and value parsing.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "xml"},
		IconLight:   xmlArtwork(),
		IconColor:   "#8b5cf6",
		Subtitle:    "{{ $parameter.mode }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: XMLOperationToJSON,
				Options: []node.PropertyOption{
					{Label: "XML to JSON", Value: XMLOperationToJSON},
					{Label: "JSON to XML", Value: XMLOperationFromJSON},
				},
			},
			{
				Key: "xml", Label: "XML", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 8},
				Description: "The XML to convert. Supports expressions. When empty, the item's binary file is read.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: XMLOperationToJSON}},
			},
			{
				Key: "binaryPropertyName", Label: "Input Binary Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the XML file is read from when the XML field is empty.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: XMLOperationToJSON}},
			},
			{
				Key: "json", Label: "JSON", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 8},
				Description: "The JSON to convert, as text. Supports expressions. When empty, the whole item is converted.",
				VisibleWhen: []node.VisibilityCondition{{Key: "mode", Equals: XMLOperationFromJSON}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "explicitArray", Label: "Explicit Array", Kind: node.PropertyBoolean, Default: false, Description: "Always put child elements in arrays, even when only one element carries the name."},
					{Key: "ignoreAttributes", Label: "Ignore Attributes & Namespace", Kind: node.PropertyBoolean, Default: true},
					{
						Key: "mergeAttributes", Label: "Merge Attributes", Kind: node.PropertyBoolean, Default: true,
						Description: "Put attributes beside the child elements under the same names rather than keeping the prefix apart.",
						VisibleWhen: []node.VisibilityCondition{{Key: "ignoreAttributes", Equals: false}},
					},
					{Key: "attributeNamePrefix", Label: "Attribute Name Prefix", Kind: node.PropertyString, Default: "@"},
					{Key: "textNodeName", Label: "Text Node Name", Kind: node.PropertyString, Default: "#text"},
					{Key: "parseTagValue", Label: "Parse Tag Value", Kind: node.PropertyBoolean, Default: true, Description: "Turn numbers, booleans and nulls in the XML into the JSON values they are."},
					{Key: "trimValues", Label: "Trim Values", Kind: node.PropertyBoolean, Default: true},
				},
			},
			{
				Key: "destinationKey", Label: "Destination Key", Kind: node.PropertyString, Default: "data",
				Description: "The JSON field the converted value is written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     XMLExecutorID,
		Validate:       validateXMLNode,
	}
}

func validateXMLNode(n workflow.Node) error {
	switch mode := statementText(n.Parameters, "mode"); mode {
	case "", XMLOperationToJSON, XMLOperationFromJSON:
		return nil
	default:
		return fmt.Errorf("mode %q is not one this node runs", mode)
	}
}

func executeXML(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		destination := strings.TrimSpace(textValue(parameters["destinationKey"], "data"))
		if destination == "" {
			destination = "data"
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		switch mode := textValue(parameters["mode"], XMLOperationToJSON); mode {
		case XMLOperationToJSON:
			payload, err := xmlSource(parameters, item, request, ir, index)
			if err != nil {
				return nil, err
			}
			converted, err := xmlToJSON(payload, readXMLOptions(parameters))
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			fields[destination] = converted
		case XMLOperationFromJSON:
			source := strings.TrimSpace(textValue(parameters["json"], ""))
			var payload any = item.JSON
			if source != "" {
				var decoded any
				if err := json.Unmarshal([]byte(source), &decoded); err != nil {
					return nil, fmt.Errorf("node %q: item %d: the JSON field does not hold JSON: %w", ir.Name, index+1, err)
				}
				payload = decoded
			}
			fields[destination] = jsonToXML(payload, readXMLOptions(parameters))
		default:
			return nil, fmt.Errorf("node %q: mode %q has no runner here", ir.Name, mode)
		}
		output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

func xmlSource(parameters map[string]any, item workflow.Item, request engine.Request, ir workflow.IRNode, index int) ([]byte, error) {
	if source := textValue(parameters["xml"], ""); strings.TrimSpace(source) != "" {
		return []byte(source), nil
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: item %d carries neither XML text nor a binary store", ir.Name, index+1)
	}
	key := strings.TrimSpace(textValue(parameters["binaryPropertyName"], "data"))
	if key == "" {
		key = "data"
	}
	reference, found := firstBinary(item, key)
	if !found {
		return nil, fmt.Errorf("node %q: item %d has no XML text and no binary property %q", ir.Name, index+1, key)
	}
	body, _, err := request.Binaries.Get(reference.ID)
	if err != nil {
		return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
	}
	payload, err := io.ReadAll(io.LimitReader(body, maxFileFormatBytes))
	_ = body.Close()
	if err != nil {
		return nil, fmt.Errorf("node %q: item %d could not be read: %w", ir.Name, index+1, err)
	}
	return payload, nil
}

// --- XML to JSON ----------------------------------------------------------------

// xmlElement is the parser's intermediate tree: attributes by local name,
// children in document order, and the element's own text.
type xmlElement struct {
	name     string
	attrs    []xml.Attr
	children []*xmlElement
	text     strings.Builder
}

// xmlToJSON parses the document and converts its root into the json2js-shaped
// map: one key per root name, the value the converted element.
func xmlToJSON(payload []byte, options xmlOptions) (map[string]any, error) {
	root, err := parseXMLDocument(payload)
	if err != nil {
		return nil, err
	}
	return map[string]any{root.name: convertXMLElement(root, options)}, nil
}

func parseXMLDocument(payload []byte) (*xmlElement, error) {
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	var stack []*xmlElement
	var root *xmlElement
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the file is not valid XML: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			current := &xmlElement{name: element.Name.Local, attrs: element.Attr}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, current)
			} else if root == nil {
				root = current
			}
			stack = append(stack, current)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(element)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("the file has no XML element")
	}
	return root, nil
}

// convertXMLElement applies the xml2js rules to one element. A text-only
// element is its own value; an element with children or kept attributes is a
// map, with attributes under their prefix (or merged in, when asked) and text
// under the text node's name.
func convertXMLElement(element *xmlElement, options xmlOptions) any {
	if len(element.children) == 0 && options.ignoreAttributes {
		return convertXMLText(element.text.String(), options)
	}
	if len(element.children) == 0 && !options.ignoreAttributes && len(element.attrs) == 0 {
		return convertXMLText(element.text.String(), options)
	}
	result := map[string]any{}
	if !options.ignoreAttributes {
		for _, attr := range element.attrs {
			key := options.attributeNamePrefix + attr.Name.Local
			if options.mergeAttributes {
				key = attr.Name.Local
			}
			if existing, taken := result[key]; taken {
				result[key] = appendScalar(existing, convertXMLText(attr.Value, options))
				continue
			}
			result[key] = convertXMLText(attr.Value, options)
		}
	}
	grouped := map[string][]*xmlElement{}
	var order []string
	for _, child := range element.children {
		if _, seen := grouped[child.name]; !seen {
			order = append(order, child.name)
		}
		grouped[child.name] = append(grouped[child.name], child)
	}
	for _, name := range order {
		converted := make([]any, 0, len(grouped[name]))
		for _, child := range grouped[name] {
			converted = append(converted, convertXMLElement(child, options))
		}
		if options.explicitArray {
			result[name] = converted
			continue
		}
		if len(converted) == 1 {
			result[name] = converted[0]
			continue
		}
		result[name] = converted
	}
	if text := element.text.String(); strings.TrimSpace(text) != "" || len(element.children) == 0 {
		if _, taken := result[options.textNodeName]; !taken {
			result[options.textNodeName] = convertXMLText(text, options)
		}
	}
	return result
}

// convertXMLText renders the element's character data: trimmed when asked,
// parsed to a number, boolean or null when asked and the text is one, and
// always a string otherwise.
func convertXMLText(text string, options xmlOptions) any {
	if options.trimValues {
		text = strings.TrimSpace(text)
	}
	if !options.parseTagValue {
		return text
	}
	switch text {
	case "true":
		return true
	case "false":
		return false
	case "null", "":
		if text == "" {
			return text
		}
		return nil
	}
	if number, err := strconv.ParseFloat(text, 64); err == nil {
		return number
	}
	return text
}

func appendScalar(existing any, value any) []any {
	switch typed := existing.(type) {
	case []any:
		return append(typed, value)
	default:
		return []any{existing, value}
	}
}

// --- JSON to XML ----------------------------------------------------------------

// jsonToXML converts a JSON value into XML. A single-key object becomes one
// named root (the shape xmlToJson produces); anything else — several keys, an
// array, a scalar — is wrapped in <root>, because an XML document has exactly
// one and inventing the wrapper is the only way to honour the payload.
func jsonToXML(payload any, options xmlOptions) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	if root, ok := payload.(map[string]any); ok && len(root) == 1 {
		for name, value := range root {
			writeXMLElement(&builder, name, value, options)
		}
		return builder.String()
	}
	writeXMLElement(&builder, "root", payload, options)
	return builder.String()
}

// writeXMLElement renders one element from a JSON value: a map's prefixed
// keys become attributes, the text node becomes character data, every other
// key a child element, and an array the same element repeated.
func writeXMLElement(builder *strings.Builder, name string, value any, options xmlOptions) {
	if list, ok := value.([]any); ok {
		for _, entry := range list {
			writeXMLElement(builder, name, entry, options)
		}
		return
	}
	builder.WriteString("<")
	builder.WriteString(name)
	content, text := elementContent(value, options)
	for _, child := range content.attrs {
		builder.WriteString(" ")
		builder.WriteString(child.name)
		builder.WriteString(`="`)
		xml.EscapeText(builder, []byte(xmlScalarText(child.value)))
		builder.WriteString(`"`)
	}
	if len(content.elements) == 0 && text == nil {
		builder.WriteString("/>")
		return
	}
	builder.WriteString(">")
	if text != nil {
		xml.EscapeText(builder, []byte(*text))
	}
	for _, child := range content.elements {
		writeXMLElement(builder, child.name, child.value, options)
	}
	builder.WriteString("</")
	builder.WriteString(name)
	builder.WriteString(">")
}

type xmlChildElement struct {
	name  string
	value any
}

type xmlContent struct {
	attrs    []xmlChildElement
	elements []xmlChildElement
}

// elementContent splits a map into its attributes and child elements; a
// scalar becomes the element's text. Attributes are rendered ahead of the
// children in the map's own sorted order, which is where every reader (and
// every pair of eyes) looks for them.
func elementContent(value any, options xmlOptions) (xmlContent, *string) {
	var content xmlContent
	record, ok := value.(map[string]any)
	if !ok {
		text := xmlScalarText(value)
		return content, &text
	}
	text, hasText := record[options.textNodeName]
	prefix := options.attributeNamePrefix
	var names []string
	for key := range record {
		if hasText && key == options.textNodeName {
			continue
		}
		names = append(names, key)
	}
	sortStrings(names)
	for _, key := range names {
		entry := record[key]
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			content.attrs = append(content.attrs, xmlChildElement{name: strings.TrimPrefix(key, prefix), value: entry})
			continue
		}
		content.elements = append(content.elements, xmlChildElement{name: key, value: entry})
	}
	if hasText {
		rendered := xmlScalarText(text)
		return content, &rendered
	}
	return content, nil
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
	case []any:
		var parts []string
		for _, entry := range typed {
			parts = append(parts, xmlScalarText(entry))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for position := index; position > 0 && values[position] < values[position-1]; position-- {
			values[position], values[position-1] = values[position-1], values[position]
		}
	}
}

// xmlArtwork is the node's own monochrome mark: the angular-tag brackets of
// an XML document, in the editor's stroke idiom.
func xmlArtwork() *node.IconAsset {
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="M4 4h16v16H4z"/><path d="m9 9-2 3 2 3"/><path d="m15 9 2 3-2 3"/>` +
			`</svg>`),
	}
}
