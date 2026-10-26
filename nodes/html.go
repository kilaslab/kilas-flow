package nodes

// The HTML utility node (FEAT-re138f): the reader and the writer n8n's own
// HTML node carries — extract by CSS selector, extract the document's text,
// and generate an HTML table of the items.
//
// Parsing goes through golang.org/x/net/html — the pure-Go parser this
// module already ships for its HTTP stack — with a small CSS selector
// matcher of our own on top: types, classes, ids, attributes and the four
// combinators (descendant, child, adjacent and general sibling), which is
// the surface workflows' selectors actually use. A selector outside that
// surface is refused by name at run time rather than matched against the
// wrong elements.
//
// The parameter keys are n8n's own (`operation`, `sourceType`, `html`,
// `binaryPropertyName`, `cssSelector`, `returnValue`, `attribute`,
// `columnHeaders`), so an imported node maps by type name alone.
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	HTMLExecutorID = "core.html"
	HTMLNodeType   = "kilasflow.html"
)

// The HTML operations, under n8n's values.
const (
	HTMLOperationGenerate  = "generateHtmlTemplate"
	HTMLOperationExtract   = "htmlExtractContents"
	HTMLOperationEveryText = "htmlExtractEveryText"
)

// The extract return values: the matched element's text, its inner HTML, or
// one of its attributes.
const (
	HTMLReturnText      = "text"
	HTMLReturnHTML      = "html"
	HTMLReturnAttribute = "attribute"
)

func htmlNode() node.Definition {
	return node.Definition{
		Type: HTMLNodeType,
		// WholeBatch: the generate operation writes one table of every item,
		// and the extractions read the same batch without harm.
		WholeBatch:  true,
		Version:     workflow.V(1),
		DisplayName: "HTML",
		Description: "Extracts elements from HTML with a CSS selector, strips the document to its text, or generates an HTML table of the items.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "html"},
		IconLight:   htmlArtwork(),
		IconColor:   "#e34f26",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: HTMLOperationGenerate,
				Options: []node.PropertyOption{
					{Label: "Generate HTML Template", Value: HTMLOperationGenerate},
					{Label: "Extract Content", Value: HTMLOperationExtract},
					{Label: "Extract Text Content", Value: HTMLOperationEveryText},
				},
			},
			{
				Key: "sourceType", Label: "Source Data", Kind: node.PropertyOptions, Default: "json",
				Options: []node.PropertyOption{
					{Label: "Text", Value: "json"},
					{Label: "Binary", Value: "binary"},
				},
				Description: "Read the HTML from the field below, or from the item's binary data.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationExtract},
					{Key: "operation", Equals: HTMLOperationEveryText}},
			},
			{
				Key: "html", Label: "HTML", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 6},
				Description: "The HTML to read. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "sourceType", Equals: "json"}},
			},
			{
				Key: "binaryPropertyName", Label: "Input Binary Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the HTML file is read from.",
				VisibleWhen: []node.VisibilityCondition{{Key: "sourceType", Equals: "binary"}},
			},
			{
				Key: "cssSelector", Label: "CSS Selector", Kind: node.PropertyString, Required: true,
				Description: "The elements to extract: .price, article > h1, a[href]. Types, classes, ids, attributes and the > + ~ combinators.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationExtract}},
			},
			{
				Key: "returnValue", Label: "Return Value", Kind: node.PropertyOptions, Default: HTMLReturnText,
				Options: []node.PropertyOption{
					{Label: "Text", Value: HTMLReturnText},
					{Label: "HTML", Value: HTMLReturnHTML},
					{Label: "Attribute", Value: HTMLReturnAttribute},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationExtract}},
			},
			{
				Key: "attribute", Label: "Attribute", Kind: node.PropertyString,
				Description: "The attribute to read when Return Value is Attribute, e.g. href.",
				VisibleWhen: []node.VisibilityCondition{{Key: "returnValue", Equals: HTMLReturnAttribute}},
			},
			{
				Key: "columnHeaders", Label: "Column Headers", Kind: node.PropertyBoolean, Default: true,
				Description: "Write the columns as a header row.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: HTMLOperationGenerate}},
			},
			{
				Key: "destinationKey", Label: "Destination Key", Kind: node.PropertyString, Default: "data",
				Description: "The JSON field the HTML or the extracted values are written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     HTMLExecutorID,
		Validate:       validateHTMLNode,
	}
}

func validateHTMLNode(n workflow.Node) error {
	switch operation := statementText(n.Parameters, "operation"); operation {
	case "", HTMLOperationGenerate, HTMLOperationExtract, HTMLOperationEveryText:
	default:
		return fmt.Errorf("operation %q is not one this node runs", operation)
	}
	if selector := statementText(n.Parameters, "cssSelector"); selector != "" {
		if _, err := compileSelector(selector); err != nil {
			return fmt.Errorf("cssSelector: %w", err)
		}
	}
	return nil
}

func executeHTML(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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
		operation := textValue(parameters["operation"], HTMLOperationGenerate)
		destination := strings.TrimSpace(textValue(parameters["destinationKey"], "data"))
		if destination == "" {
			destination = "data"
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		switch operation {
		case HTMLOperationGenerate:
			fields[destination] = htmlTable(items, parameters["columnHeaders"] != false)
		case HTMLOperationEveryText:
			document, err := htmlDocument(parameters, item, request, ir, index)
			if err != nil {
				return nil, err
			}
			fields[destination] = strings.TrimSpace(htmlDocumentText(document))
		case HTMLOperationExtract:
			document, err := htmlDocument(parameters, item, request, ir, index)
			if err != nil {
				return nil, err
			}
			extracted, err := htmlExtractContents(document, parameters)
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			fields[destination] = extracted
		default:
			return nil, fmt.Errorf("node %q: operation %q has no runner here", ir.Name, operation)
		}
		output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

// htmlDocument reads the document the extraction operations parse: the text
// parameter, or the item's binary file.
func htmlDocument(parameters map[string]any, item workflow.Item, request engine.Request, ir workflow.IRNode, index int) (*html.Node, error) {
	var payload []byte
	if textValue(parameters["sourceType"], "json") == "binary" {
		if request.Binaries == nil {
			return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
		}
		key := strings.TrimSpace(textValue(parameters["binaryPropertyName"], "data"))
		if key == "" {
			key = "data"
		}
		reference, found := firstBinary(item, key)
		if !found {
			return nil, fmt.Errorf("node %q: item %d has no binary property %q", ir.Name, index+1, key)
		}
		body, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		payload, err = io.ReadAll(io.LimitReader(body, maxFileFormatBytes))
		_ = body.Close()
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d could not be read: %w", ir.Name, index+1, err)
		}
	} else {
		payload = []byte(textValue(parameters["html"], ""))
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, fmt.Errorf("node %q: item %d carries no HTML to read", ir.Name, index+1)
	}
	document, err := html.Parse(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("node %q: item %d: the HTML does not parse: %w", ir.Name, index+1, err)
	}
	return document, nil
}

// htmlExtractContents runs the selector and renders every match by the
// configured return value: the element's text, its inner HTML, or one
// attribute. Every match is returned, in document order, as a list — n8n's
// node answers the same list even when the selector matched once.
func htmlExtractContents(document *html.Node, parameters map[string]any) ([]any, error) {
	selector, err := compileSelector(textValue(parameters["cssSelector"], ""))
	if err != nil {
		return nil, err
	}
	returnValue := textValue(parameters["returnValue"], HTMLReturnText)
	attribute := strings.TrimSpace(textValue(parameters["attribute"], ""))
	if returnValue == HTMLReturnAttribute && attribute == "" {
		return nil, fmt.Errorf("returnValue attribute needs the attribute's name, e.g. href")
	}
	matches := selectorAll(document, selector)
	extracted := make([]any, 0, len(matches))
	for _, matched := range matches {
		switch returnValue {
		case HTMLReturnHTML:
			var inner bytes.Buffer
			for child := matched.FirstChild; child != nil; child = child.NextSibling {
				if err := html.Render(&inner, child); err != nil {
					return nil, fmt.Errorf("the matched element could not be rendered: %w", err)
				}
			}
			extracted = append(extracted, inner.String())
		case HTMLReturnAttribute:
			for _, entry := range matched.Attr {
				if entry.Key == attribute {
					extracted = append(extracted, entry.Val)
					break
				}
			}
		default:
			extracted = append(extracted, strings.TrimSpace(nodeText(matched)))
		}
	}
	return extracted, nil
}

// htmlDocumentText collects the document's text, with script and style
// contents left out: they are code, not content, and a page's text minus its
// JavaScript is what "extract text" means to the workflow reading it.
func htmlDocumentText(document *html.Node) string {
	var builder strings.Builder
	var walk func(current *html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode {
			switch current.Data {
			case "script", "style", "noscript":
				return
			}
		}
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return builder.String()
}

// htmlTable renders the items as an HTML table: a header row of the columns
// and one row per item, both in the same alphabetical column order the
// Convert to File writer uses, escaped so a value can never open a cell the
// author did not write.
func htmlTable(items []workflow.Item, headerRow bool) string {
	records := make([]map[string]any, 0, len(items))
	for _, item := range items {
		records = append(records, item.JSON)
	}
	headers := tableHeaders(records)
	var builder strings.Builder
	builder.WriteString("<table>\n")
	if headerRow && len(headers) > 0 {
		builder.WriteString("\t<thead>\n\t\t<tr>")
		for _, header := range headers {
			builder.WriteString("<th>")
			builder.WriteString(htmlEscapeString(header))
			builder.WriteString("</th>")
		}
		builder.WriteString("</tr>\n\t</thead>\n")
	}
	builder.WriteString("\t<tbody>\n")
	for _, record := range records {
		builder.WriteString("\t\t<tr>")
		for _, header := range headers {
			builder.WriteString("<td>")
			builder.WriteString(htmlEscapeString(cellText(record[header])))
			builder.WriteString("</td>")
		}
		builder.WriteString("</tr>\n")
	}
	builder.WriteString("\t</tbody>\n</table>")
	return builder.String()
}

func htmlEscapeString(text string) string {
	var builder strings.Builder
	xmlEscapeText(&builder, text)
	return builder.String()
}

// --- The CSS selector engine ---------------------------------------------------

// cssSelector is one compiled selector: its comma-separated alternatives,
// each a sequence of compounds joined by combinators, first to last.
type cssSelector struct {
	alternatives [][]compoundStep
}

// compoundStep is one compound in a sequence and the combinator that joins it
// to the compound after it. The final step's combinator is never read.
type compoundStep struct {
	compound   compoundSelector
	combinator byte // ' ' descendant, '>' child, '+' adjacent, '~' general sibling
}

// compoundSelector is one tag.class#id[attr] compound.
type compoundSelector struct {
	tag        string
	id         string
	classes    []string
	attributes []attributeSelector
}

// attributeSelector is one [attr] or [attr=value] condition. The operation
// mirrors CSS: 0 means present, the rest equality, prefix, suffix, substring
// and word-in-list.
type attributeSelector struct {
	key       string
	value     string
	operation byte
}

// compileSelector parses the selector surface this node documents. Anything
// else — pseudo-classes, namespaces, an empty selector — is an error naming
// what is unsupported, because a selector quietly matched against the wrong
// elements is worse than one refused.
func compileSelector(raw string) (*cssSelector, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("the selector is empty")
	}
	// One alternative per comma. Whitespace inside brackets is part of the
	// value, so the split walks the string rather than strings.Split.
	var alternatives [][]compoundStep
	var current strings.Builder
	depth := 0
	for _, character := range raw {
		switch {
		case character == '[':
			depth++
		case character == ']':
			depth--
		case character == ',' && depth == 0:
			parsed, err := compileOneAlternative(current.String())
			if err != nil {
				return nil, err
			}
			alternatives = append(alternatives, parsed)
			current.Reset()
			continue
		}
		current.WriteRune(character)
	}
	parsed, err := compileOneAlternative(current.String())
	if err != nil {
		return nil, err
	}
	alternatives = append(alternatives, parsed)
	return &cssSelector{alternatives: alternatives}, nil
}

// compileOneAlternative splits one alternative into its compounds and the
// combinators between them, in order: `article > h1 .price` becomes
// [article >], [h1 ' '] and [price].
func compileOneAlternative(alternative string) ([]compoundStep, error) {
	var steps []compoundStep
	var token strings.Builder
	// pending is the combinator that joins the next compound to the previous
	// one; ' ' before the first compound means nothing.
	pending := byte(0)
	flush := func() error {
		text := strings.TrimSpace(token.String())
		token.Reset()
		if text == "" {
			if pending != 0 {
				return fmt.Errorf("the selector has a %q combinator with nothing after it", string(pending))
			}
			return nil
		}
		compound, err := compileCompound(text)
		if err != nil {
			return err
		}
		steps = append(steps, compoundStep{compound: compound, combinator: pending})
		pending = 0
		return nil
	}
	depth := 0
	for _, character := range alternative {
		switch {
		case character == '[':
			depth++
		case character == ']':
			depth--
		}
		if depth > 0 || character == ']' {
			token.WriteRune(character)
			continue
		}
		switch character {
		case '>', '+', '~':
			// Whitespace around an explicit combinator is noise the way CSS
			// itself treats it: `a > b` and `a>b` are the same selector.
			if strings.TrimSpace(token.String()) != "" {
				if err := flush(); err != nil {
					return nil, err
				}
			} else {
				token.Reset()
			}
			pending = byte(character)
		case ' ', '\t', '\n', '\r':
			if strings.TrimSpace(token.String()) != "" {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			if pending == 0 {
				pending = ' '
			}
		default:
			token.WriteRune(character)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("the selector alternative %q is empty", alternative)
	}
	return steps, nil
}

// compileCompound parses one compound: a tag, an id, any number of classes
// and attribute conditions, in any order — `a.button#go[href^=http]`.
func compileCompound(text string) (compoundSelector, error) {
	var compound compoundSelector
	var token strings.Builder
	var kind byte
	flush := func() error {
		defer token.Reset()
		value := token.String()
		if value == "" {
			return nil
		}
		switch kind {
		case '#':
			if compound.id != "" {
				return fmt.Errorf("the compound %q names two ids", text)
			}
			compound.id = value
		case '.':
			compound.classes = append(compound.classes, value)
		case '[':
			attribute, err := compileAttribute(value)
			if err != nil {
				return err
			}
			compound.attributes = append(compound.attributes, attribute)
		default:
			if compound.tag != "" {
				return fmt.Errorf("the compound %q names two tags", text)
			}
			compound.tag = strings.ToLower(value)
		}
		return nil
	}
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch {
		case character == '#', character == '.':
			if err := flush(); err != nil {
				return compound, err
			}
			kind = character
		case character == '[':
			if err := flush(); err != nil {
				return compound, err
			}
			kind = '['
			// The bracket's whole content is one token, including spaces.
			end := strings.IndexByte(text[index+1:], ']')
			if end < 0 {
				return compound, fmt.Errorf("the compound %q has an unclosed attribute bracket", text)
			}
			token.WriteString(text[index+1 : index+1+end])
			index += end + 1
			if err := flush(); err != nil {
				return compound, err
			}
			kind = 0
		default:
			if kind == 0 && !isNameByte(character) {
				return compound, fmt.Errorf("the compound %q has a character this selector engine does not read (%q); pseudo-classes are not supported", text, string(character))
			}
			if kind == 0 {
				character = lowerByte(character)
			}
			token.WriteByte(character)
		}
	}
	if err := flush(); err != nil {
		return compound, err
	}
	if compound.tag == "" && compound.id == "" && len(compound.classes) == 0 && len(compound.attributes) == 0 {
		return compound, fmt.Errorf("the compound %q is empty", text)
	}
	return compound, nil
}

// compileAttribute parses one bracket's content: attr, attr=value, or the
// attr^=value / $= / *= / ~= families, with the value optionally quoted.
func compileAttribute(text string) (attributeSelector, error) {
	text = strings.TrimSpace(text)
	operation := byte(0)
	separator := -1
	key, value := text, ""
	if index := strings.IndexByte(text, '='); index > 0 {
		switch text[index-1] {
		case '^', '$', '*', '~':
			operation, separator = text[index-1], index-1
		default:
			operation, separator = '=', index
		}
		key = strings.TrimSpace(text[:separator])
		value = strings.Trim(strings.TrimSpace(text[separator+len("x="):]), `"'`)
	}
	if strings.ContainsAny(key, "[]^$*~=") {
		return attributeSelector{}, fmt.Errorf("the attribute selector %q does not parse", text)
	}
	if key == "" {
		return attributeSelector{}, fmt.Errorf("the attribute selector %q names no attribute", text)
	}
	return attributeSelector{key: strings.ToLower(key), value: value, operation: operation}, nil
}

func isNameByte(character byte) bool {
	switch {
	case character >= 'a' && character <= 'z':
	case character >= 'A' && character <= 'Z':
	case character >= '0' && character <= '9':
	case character == '-' || character == '_' || character >= 0x80:
	default:
		return false
	}
	return true
}

func lowerByte(character byte) byte {
	if character >= 'A' && character <= 'Z' {
		return character + 'a' - 'A'
	}
	return character
}

// selectorAll returns every element the selector matches, in document order,
// with an element matched at most once even when several alternatives match
// it.
func selectorAll(document *html.Node, selector *cssSelector) []*html.Node {
	var matches []*html.Node
	seen := map[*html.Node]bool{}
	var walk func(current *html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode {
			for _, alternative := range selector.alternatives {
				if elementMatchesAlternative(current, alternative, len(alternative)-1) && !seen[current] {
					seen[current] = true
					matches = append(matches, current)
					break
				}
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return matches
}

// elementMatchesAlternative reports whether the element matches the
// alternative's compound at position, and everything the compounds before it
// require through their combinators.
func elementMatchesAlternative(element *html.Node, alternative []compoundStep, position int) bool {
	if !compoundMatches(element, alternative[position].compound) {
		return false
	}
	if position == 0 {
		return true
	}
	switch alternative[position-1].combinator {
	case '>':
		parent := element.Parent
		return parent != nil && elementMatchesAlternative(parent, alternative, position-1)
	case '+':
		if sibling := precedingElementSibling(element); sibling != nil {
			return elementMatchesAlternative(sibling, alternative, position-1)
		}
		return false
	case '~':
		for sibling := precedingElementSibling(element); sibling != nil; sibling = precedingElementSibling(sibling) {
			if elementMatchesAlternative(sibling, alternative, position-1) {
				return true
			}
		}
		return false
	default: // ' ' descendant
		for parent := element.Parent; parent != nil; parent = parent.Parent {
			if parent.Type == html.ElementNode && elementMatchesAlternative(parent, alternative, position-1) {
				return true
			}
		}
		return false
	}
}

func precedingElementSibling(element *html.Node) *html.Node {
	for sibling := element.PrevSibling; sibling != nil; sibling = sibling.PrevSibling {
		if sibling.Type == html.ElementNode {
			return sibling
		}
	}
	return nil
}

// compoundMatches checks the element against one compound: tag, id, every
// class and every attribute condition. The tag is matched case-insensitively
// and the universal tag matches anything, which is what HTML itself does.
func compoundMatches(element *html.Node, compound compoundSelector) bool {
	if compound.tag != "" && compound.tag != "*" && compound.tag != strings.ToLower(element.Data) {
		return false
	}
	if compound.id != "" && attributeValue(element, "id") != compound.id {
		return false
	}
	if len(compound.classes) > 0 {
		classes := strings.Fields(attributeValue(element, "class"))
		for _, wanted := range compound.classes {
			found := false
			for _, present := range classes {
				if present == wanted {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for _, wanted := range compound.attributes {
		value, present := "", false
		for _, entry := range element.Attr {
			if strings.ToLower(entry.Key) == wanted.key {
				value, present = entry.Val, true
				break
			}
		}
		if !present {
			return false
		}
		switch wanted.operation {
		case 0:
		case '=':
			if value != wanted.value {
				return false
			}
		case '^':
			if wanted.value == "" || !strings.HasPrefix(value, wanted.value) {
				return false
			}
		case '$':
			if wanted.value == "" || !strings.HasSuffix(value, wanted.value) {
				return false
			}
		case '*':
			if wanted.value == "" || !strings.Contains(value, wanted.value) {
				return false
			}
		case '~':
			matched := false
			for _, word := range strings.Fields(value) {
				if word == wanted.value {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

func attributeValue(element *html.Node, key string) string {
	for _, entry := range element.Attr {
		if strings.ToLower(entry.Key) == key {
			return entry.Val
		}
	}
	return ""
}

func nodeText(current *html.Node) string {
	var builder strings.Builder
	var walk func(current *html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(current)
	return builder.String()
}

// htmlArtwork is the node's own monochrome mark: the HTML shield with its
// angle brackets, in the stroke idiom the editor's builtin glyphs draw in.
func htmlArtwork() *node.IconAsset {
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M16 3h3a2 2 0 0 1 2 2v3"/><path d="M8 21H5a2 2 0 0 1-2-2v-3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/><path d="m9 9-2 3 2 3"/><path d="m15 9 2 3-2 3"/>` +
			`</svg>`),
	}
}
