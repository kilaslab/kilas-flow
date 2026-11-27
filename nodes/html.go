package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The HTML node's server-owned binding and type.
const (
	HTMLExecutorID = "core.html"
	HTMLNodeType   = "kilasflow.html"
)

// The HTML node's operations, as n8n names them.
const (
	HTMLOperationTemplate = "generateHtmlTemplate"
	HTMLOperationExtract  = "extractHtmlContent"
	HTMLOperationTable    = "convertToHtmlTable"
)

// HTMLOptionKeys are the options the node implements. The importer refuses a
// node that sets any other.
var HTMLOptionKeys = []string{
	"trimValues", "cleanUpText",
	"capitalize", "customStyling", "caption", "tableAttributes", "headerAttributes", "rowAttributes", "cellAttributes",
}

// htmlMaxInput bounds the document parsed in one call.
const htmlMaxInput = 32 << 20

func htmlNode() node.Definition {
	whenExtract := []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationExtract}}
	whenTable := []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationTable}}
	return node.Definition{
		Type:        HTMLNodeType,
		Version:     workflow.V(1),
		DisplayName: "HTML",
		Description: "Extracts values from HTML with CSS selectors, writes an HTML table from items, or renders an HTML template.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:layout-template"},
		IconColor:   "#e44d26",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		// The table operation folds every item into one table, so the node is
		// batch-shaped: under continue-on-fail it must not be split per item.
		WholeBatch: true,
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: HTMLOperationTemplate,
				Options: []node.PropertyOption{
					{Label: "Generate HTML Template", Value: HTMLOperationTemplate},
					{Label: "Extract HTML Content", Value: HTMLOperationExtract},
					{Label: "Convert to HTML Table", Value: HTMLOperationTable},
				},
			},
			{
				Key: "html", Label: "HTML Template", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 8},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationTemplate}},
				Description: "The HTML to write to the html field. Expressions such as {{ $json.name }} are filled in per item.",
			},
			{
				Key: "sourceData", Label: "Source Data", Kind: node.PropertyOptions, Default: "json", VisibleWhen: whenExtract,
				Options:     []node.PropertyOption{{Label: "JSON", Value: "json"}, {Label: "Binary", Value: "binary"}},
				Description: "Where the HTML comes from: a field of the item, or a binary file.",
			},
			{
				Key: "dataPropertyName", Label: "Input Field", Kind: node.PropertyString, Default: "data", VisibleWhen: whenExtract,
				Description: "The JSON field, or the binary field, that holds the HTML.",
			},
			{
				Key: "extractionValues", Label: "Extraction Values", Kind: node.PropertyFixedCollection, VisibleWhen: whenExtract,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add value"},
				Description: "What to read out of the HTML. Each row becomes one field of the output item.",
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Values",
					Fields: []node.PropertyDefinition{
						{Key: "key", Label: "Key", Kind: node.PropertyString, Description: "The output field the value is written to."},
						{Key: "cssSelector", Label: "CSS Selector", Kind: node.PropertyString, Default: ".ready",
							Description: "Which elements to read. A comma lists several selectors."},
						{Key: "returnValue", Label: "Return Value", Kind: node.PropertyOptions, Default: "text",
							Options: []node.PropertyOption{
								{Label: "Attribute", Value: "attribute"}, {Label: "HTML", Value: "html"},
								{Label: "Text", Value: "text"}, {Label: "Value", Value: "value"},
							},
							Description: "Text is the elements' text, HTML their inner markup, Attribute one attribute, and Value a form control's value."},
						{Key: "attribute", Label: "Attribute", Kind: node.PropertyString,
							Description: "The attribute to read, when the return value is Attribute.",
							VisibleWhen: []node.VisibilityCondition{{Key: "returnValue", Equals: "attribute"}}},
						{Key: "skipSelectors", Label: "Skip Selectors", Kind: node.PropertyString,
							Description: "Selectors, separated by commas, of elements to leave out of the text or HTML read from the matches."},
						{Key: "returnArray", Label: "Return Array", Kind: node.PropertyBoolean, Default: false,
							Description: "Return one value per matching element instead of a single value."},
					},
				}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "trimValues", Label: "Trim Values", Kind: node.PropertyBoolean, Default: true, VisibleWhen: whenExtract,
						Description: "Remove whitespace from the start and end of each value."},
					{Key: "cleanUpText", Label: "Clean Up Text", Kind: node.PropertyBoolean, Default: true, VisibleWhen: whenExtract,
						Description: "Trim each value and turn every run of whitespace and line breaks into one space."},
					{Key: "capitalize", Label: "Capitalize Headers", Kind: node.PropertyBoolean, Default: false, VisibleWhen: whenTable,
						Description: "Capitalise the first letter of each column header."},
					{Key: "customStyling", Label: "Custom Styling", Kind: node.PropertyBoolean, Default: false, VisibleWhen: whenTable,
						Description: "Add the attribute options below to the table. Off, the table is written without attributes."},
					{Key: "caption", Label: "Caption", Kind: node.PropertyString, VisibleWhen: whenTable,
						Description: "A caption for the table."},
					{Key: "tableAttributes", Label: "Table Attributes", Kind: node.PropertyString, VisibleWhen: whenTable,
						Description: "Attributes for the table element, written as they would be in HTML: class=\"data\" id=\"report\"."},
					{Key: "headerAttributes", Label: "Header Attributes", Kind: node.PropertyString, VisibleWhen: whenTable,
						Description: "Attributes for each header cell."},
					{Key: "rowAttributes", Label: "Row Attributes", Kind: node.PropertyString, VisibleWhen: whenTable,
						Description: "Attributes for each body row."},
					{Key: "cellAttributes", Label: "Cell Attributes", Kind: node.PropertyString, VisibleWhen: whenTable,
						Description: "Attributes for each body cell."},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     HTMLExecutorID,
		Validate:       validateHTMLConfiguration,
	}
}

func validateHTMLConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["operation"]) {
		return nil
	}
	switch operation := textValue(n.Parameters["operation"], HTMLOperationTemplate); operation {
	case HTMLOperationTemplate, HTMLOperationTable:
	case HTMLOperationExtract:
		rows, err := readExtractionRows(n.Parameters["extractionValues"])
		if err != nil {
			return err
		}
		for index, row := range rows {
			if row.selector == "" || expression.IsExpression(row.selector) {
				continue
			}
			if _, err := cascadia.ParseGroup(row.selector); err != nil {
				return fmt.Errorf("extraction value %d: the CSS selector %q is not valid: %w", index+1, row.selector, err)
			}
		}
	default:
		return fmt.Errorf("operation must be generateHtmlTemplate, extractHtmlContent or convertToHtmlTable, not %q", operation)
	}
	known := map[string]bool{}
	for _, key := range HTMLOptionKeys {
		known[key] = true
	}
	options, _ := n.Parameters["options"].(map[string]any)
	for key := range options {
		if !known[key] {
			return fmt.Errorf("option %q is not supported", key)
		}
	}
	return nil
}

func executeHTML(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	// The table operation answers once, so it is resolved against the first
	// item only for its options; every other operation answers per item.
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	first, err := expression.Resolve(ir.Parameters, expressionContext(items[0], input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if textValue(first["operation"], HTMLOperationTemplate) == HTMLOperationTable {
		table, err := buildHTMLTable(items, mapValue(first["options"]))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		return workflow.NodeOutput{[]workflow.Item{{JSON: map[string]any{"table": table}}}}, nil
	}

	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters := first
		if index > 0 {
			parameters, err = expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
		}
		built := cloneItem(item)
		if built.JSON == nil {
			built.JSON = map[string]any{}
		}
		switch operation := textValue(parameters["operation"], HTMLOperationTemplate); operation {
		case HTMLOperationTemplate:
			built.JSON["html"] = textValue(parameters["html"], "")
		case HTMLOperationExtract:
			extracted, err := extractHTMLValues(parameters, item, request)
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			built.JSON = extracted
		default:
			return nil, fmt.Errorf("node %q: operation must be generateHtmlTemplate, extractHtmlContent or convertToHtmlTable, not %q", ir.Name, operation)
		}
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

// --- Extract -------------------------------------------------------------------

type extractionRow struct {
	key, selector, returnValue, attribute, skip string
	returnArray                                 bool
}

func readExtractionRows(value any) ([]extractionRow, error) {
	if value == nil || expression.IsExpression(value) {
		return nil, nil
	}
	collection, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("extractionValues must be a fixed collection of rows")
	}
	list, ok := collection["values"].([]any)
	if !ok {
		return nil, nil
	}
	rows := make([]extractionRow, 0, len(list))
	for _, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("extractionValues.values must be a list of rows")
		}
		rows = append(rows, extractionRow{
			key:         strings.TrimSpace(textValue(fields["key"], "")),
			selector:    strings.TrimSpace(textValue(fields["cssSelector"], "")),
			returnValue: textValue(fields["returnValue"], "text"),
			attribute:   strings.TrimSpace(textValue(fields["attribute"], "")),
			skip:        strings.TrimSpace(textValue(fields["skipSelectors"], "")),
			returnArray: boolValue(fields["returnArray"]),
		})
	}
	return rows, nil
}

func extractHTMLValues(parameters map[string]any, item workflow.Item, request engine.Request) (map[string]any, error) {
	rows, err := readExtractionRows(parameters["extractionValues"])
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("an extraction needs at least one value to read")
	}
	options := mapValue(parameters["options"])
	trim := optionBool(options, "trimValues", true)
	cleanUp := optionBool(options, "cleanUpText", true)

	source, err := htmlSource(parameters, item, request)
	if err != nil {
		return nil, err
	}
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("the HTML could not be parsed: %w", err)
	}

	extracted := make(map[string]any, len(rows))
	for index, row := range rows {
		if row.key == "" {
			return nil, fmt.Errorf("extraction value %d needs a key", index+1)
		}
		if row.selector == "" {
			return nil, fmt.Errorf("extraction value %q needs a CSS selector", row.key)
		}
		value, err := extractRow(document, row, trim, cleanUp)
		if err != nil {
			return nil, fmt.Errorf("extraction value %q: %w", row.key, err)
		}
		extracted[row.key] = value
	}
	return extracted, nil
}

// htmlSource reads the document from the item's JSON field or its binary file.
func htmlSource(parameters map[string]any, item workflow.Item, request engine.Request) (string, error) {
	property := cryptoText(parameters["dataPropertyName"], "data")
	if textValue(parameters["sourceData"], "json") == "binary" {
		if request.Binaries == nil {
			return "", fmt.Errorf("binary storage is not configured on this install")
		}
		reference, found := firstBinary(item, property)
		if !found {
			return "", fmt.Errorf("the item has no binary field %q", property)
		}
		body, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return "", err
		}
		contents, readErr := io.ReadAll(io.LimitReader(body, htmlMaxInput+1))
		_ = body.Close()
		if readErr != nil {
			return "", fmt.Errorf("binary field %q could not be read: %w", property, readErr)
		}
		if len(contents) > htmlMaxInput {
			return "", fmt.Errorf("binary field %q is larger than %d bytes", property, htmlMaxInput)
		}
		return string(contents), nil
	}
	value, found := lookupPath(item.JSON, property)
	if !found {
		return "", fmt.Errorf("the item has no field %q holding HTML", property)
	}
	text, isText := value.(string)
	if !isText {
		return "", fmt.Errorf("field %q is not text, so it cannot be read as HTML", property)
	}
	if len(text) > htmlMaxInput {
		return "", fmt.Errorf("field %q is larger than %d bytes", property, htmlMaxInput)
	}
	return text, nil
}

func extractRow(document *html.Node, row extractionRow, trim, cleanUp bool) (any, error) {
	selector, err := cascadia.ParseGroup(row.selector)
	if err != nil {
		return nil, fmt.Errorf("the CSS selector %q is not valid: %w", row.selector, err)
	}
	var skip cascadia.SelectorGroup
	if row.skip != "" {
		if skip, err = cascadia.ParseGroup(row.skip); err != nil {
			return nil, fmt.Errorf("the skip selector %q is not valid: %w", row.skip, err)
		}
	}
	matches := cascadia.QueryAll(document, selector)

	tidy := func(text string) string {
		switch {
		case cleanUp:
			return strings.Join(strings.Fields(text), " ")
		case trim:
			return strings.TrimSpace(text)
		default:
			return text
		}
	}
	read := func(element *html.Node) (any, error) {
		prepared := element
		if len(skip) > 0 {
			prepared = cloneHTMLNode(element)
			for _, doomed := range cascadia.QueryAll(prepared, skip) {
				if doomed != prepared && doomed.Parent != nil {
					doomed.Parent.RemoveChild(doomed)
				}
			}
		}
		switch row.returnValue {
		case "text":
			return tidy(htmlText(prepared)), nil
		case "html":
			return tidy(htmlInner(prepared)), nil
		case "attribute":
			if row.attribute == "" {
				return nil, fmt.Errorf("the return value is Attribute, so it needs an attribute name")
			}
			for _, attribute := range element.Attr {
				if strings.EqualFold(attribute.Key, row.attribute) {
					return tidy(attribute.Val), nil
				}
			}
			return nil, nil
		case "value":
			if value, found := htmlControlValue(element); found {
				return tidy(value), nil
			}
			return nil, nil
		default:
			return nil, fmt.Errorf("return value must be text, html, attribute or value, not %q", row.returnValue)
		}
	}

	if row.returnArray {
		values := make([]any, 0, len(matches))
		for _, element := range matches {
			value, err := read(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	}
	if len(matches) == 0 {
		if row.returnValue == "text" {
			return "", nil
		}
		return nil, nil
	}
	if row.returnValue == "text" {
		// All the matches' text joins, as one selection's text does.
		var joined strings.Builder
		for _, element := range matches {
			if len(skip) > 0 {
				value, err := read(element)
				if err != nil {
					return nil, err
				}
				joined.WriteString(textValue(value, ""))
				continue
			}
			joined.WriteString(htmlText(element))
		}
		return tidy(joined.String()), nil
	}
	return read(matches[0])
}

func cloneHTMLNode(source *html.Node) *html.Node {
	cloned := &html.Node{
		Type: source.Type, DataAtom: source.DataAtom, Data: source.Data, Namespace: source.Namespace,
		Attr: append([]html.Attribute(nil), source.Attr...),
	}
	for child := source.FirstChild; child != nil; child = child.NextSibling {
		cloned.AppendChild(cloneHTMLNode(child))
	}
	return cloned
}

// htmlText is the text of an element and everything in it, leaving out the
// text of scripts and styles, which is code and not content.
func htmlText(element *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		switch current.Type {
		case html.TextNode:
			builder.WriteString(current.Data)
		case html.ElementNode:
			if current.Data == "script" || current.Data == "style" {
				return
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(element)
	return builder.String()
}

// htmlInner is the markup inside an element.
func htmlInner(element *html.Node) string {
	var rendered bytes.Buffer
	for child := element.FirstChild; child != nil; child = child.NextSibling {
		_ = html.Render(&rendered, child)
	}
	return rendered.String()
}

// htmlControlValue reads a form control's value: an input's value attribute, a
// textarea's text, a select's chosen option and an option's value.
func htmlControlValue(element *html.Node) (string, bool) {
	attribute := func(node *html.Node, name string) (string, bool) {
		for _, candidate := range node.Attr {
			if candidate.Key == name {
				return candidate.Val, true
			}
		}
		return "", false
	}
	switch element.Data {
	case "input":
		return attribute(element, "value")
	case "textarea":
		return htmlText(element), true
	case "option":
		if value, found := attribute(element, "value"); found {
			return value, true
		}
		return htmlText(element), true
	case "select":
		var first *html.Node
		var chosen *html.Node
		var walk func(*html.Node)
		walk = func(current *html.Node) {
			if current.Type == html.ElementNode && current.Data == "option" {
				if first == nil {
					first = current
				}
				if _, selected := attribute(current, "selected"); selected && chosen == nil {
					chosen = current
				}
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(element)
		if chosen == nil {
			chosen = first
		}
		if chosen == nil {
			return "", false
		}
		return htmlControlValue(chosen)
	default:
		return "", false
	}
}

// --- Table ---------------------------------------------------------------------

// buildHTMLTable writes every item as one row of a table. The columns are the
// fields any item has, in alphabetical order because an item does not keep the
// order its fields were written in.
func buildHTMLTable(items []workflow.Item, options map[string]any) (string, error) {
	columns := map[string]bool{}
	for _, item := range items {
		for key := range item.JSON {
			columns[key] = true
		}
	}
	headers := make([]string, 0, len(columns))
	for key := range columns {
		headers = append(headers, key)
	}
	sort.Strings(headers)

	capitalize := optionBool(options, "capitalize", false)
	styled := optionBool(options, "customStyling", false)
	attributes := func(key string) (string, error) {
		if !styled {
			return "", nil
		}
		return validHTMLAttributes(textValue(options[key], ""), key)
	}
	tableAttributes, err := attributes("tableAttributes")
	if err != nil {
		return "", err
	}
	headerAttributes, err := attributes("headerAttributes")
	if err != nil {
		return "", err
	}
	rowAttributes, err := attributes("rowAttributes")
	if err != nil {
		return "", err
	}
	cellAttributes, err := attributes("cellAttributes")
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	builder.WriteString("<table" + tableAttributes + ">")
	if caption := textValue(options["caption"], ""); caption != "" {
		builder.WriteString("<caption>" + html.EscapeString(caption) + "</caption>")
	}
	builder.WriteString("<thead><tr>")
	for _, header := range headers {
		label := header
		if capitalize && label != "" {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		builder.WriteString("<th" + headerAttributes + ">" + html.EscapeString(label) + "</th>")
	}
	builder.WriteString("</tr></thead><tbody>")
	for _, item := range items {
		builder.WriteString("<tr" + rowAttributes + ">")
		for _, header := range headers {
			builder.WriteString("<td" + cellAttributes + ">" + html.EscapeString(htmlCellText(item.JSON[header])) + "</td>")
		}
		builder.WriteString("</tr>")
	}
	builder.WriteString("</tbody></table>")
	return builder.String(), nil
}

func htmlCellText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	default:
		return xmlScalarText(typed)
	}
}

// validHTMLAttributes checks an attribute string by parsing it as the inside of
// a start tag, so a value that closes the tag and opens another is refused
// instead of being written into the table as markup.
func validHTMLAttributes(text, option string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	tokenizer := html.NewTokenizer(strings.NewReader("<x " + text + ">"))
	token := tokenizer.Next()
	if token != html.StartTagToken && token != html.SelfClosingTagToken {
		return "", fmt.Errorf("%s is not a valid attribute list", option)
	}
	parsed := tokenizer.Token()
	if parsed.Data != "x" || tokenizer.Next() != html.ErrorToken {
		return "", fmt.Errorf("%s is not a valid attribute list", option)
	}
	var builder strings.Builder
	for _, attribute := range parsed.Attr {
		if strings.Trim(attribute.Key, "abcdefghijklmnopqrstuvwxyz0123456789-_:.") != "" {
			return "", fmt.Errorf("%s has an attribute name %q that cannot be written", option, attribute.Key)
		}
		if strings.HasPrefix(attribute.Key, "on") {
			return "", fmt.Errorf("%s cannot set the event handler %q", option, attribute.Key)
		}
		builder.WriteString(" " + attribute.Key + `="` + html.EscapeString(attribute.Val) + `"`)
	}
	return builder.String(), nil
}
