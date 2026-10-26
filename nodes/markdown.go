package nodes

// The Markdown utility node (FEAT-re138f): Markdown to HTML and back, the two
// operations n8n's own Markdown node carries, with n8n's parameter keys
// (`operation`, `markdown`, `html`, `options.breaks`).
//
// Both directions are hand-written converters over the honest subset of each
// format a workflow round-trips: headings, lists, code, block quotes, links,
// images and the inline marks. The Markdown a template emits is that subset;
// anything outside it crosses as the text it is rather than being refused,
// because a converter's job is to carry words, not to police dialects. The
// HTML direction goes through the same pure-Go parser the HTML node reads.
import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	MarkdownExecutorID = "core.markdown"
	MarkdownNodeType   = "kilasflow.markdown"
)

const (
	MarkdownOperationToHTML   = "markdownToHtml"
	MarkdownOperationFromHTML = "htmlToMarkdown"
)

func markdownNode() node.Definition {
	return node.Definition{
		Type:        MarkdownNodeType,
		Version:     workflow.V(1),
		DisplayName: "Markdown",
		Description: "Converts Markdown to HTML or HTML to Markdown, in place, with no network involved.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "markdown"},
		IconLight:   markdownArtwork(),
		IconColor:   "#087fa4",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: MarkdownOperationToHTML,
				Options: []node.PropertyOption{
					{Label: "Markdown to HTML", Value: MarkdownOperationToHTML},
					{Label: "HTML to Markdown", Value: MarkdownOperationFromHTML},
				},
			},
			{
				Key: "markdown", Label: "Markdown", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 8},
				Description: "The Markdown to convert. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: MarkdownOperationToHTML}},
			},
			{
				Key: "html", Label: "HTML", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 8},
				Description: "The HTML to convert. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: MarkdownOperationFromHTML}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "breaks", Label: "Line Breaks", Kind: node.PropertyBoolean, Default: false,
						Description: "In Markdown to HTML, turn every single line break into <br> rather than a space.",
					},
				},
			},
			{
				Key: "destinationKey", Label: "Destination Key", Kind: node.PropertyString, Default: "data",
				Description: "The JSON field the converted text is written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     MarkdownExecutorID,
		Validate:       validateMarkdownNode,
	}
}

func validateMarkdownNode(n workflow.Node) error {
	switch operation := statementText(n.Parameters, "operation"); operation {
	case "", MarkdownOperationToHTML, MarkdownOperationFromHTML:
		return nil
	default:
		return fmt.Errorf("operation %q is not one this node runs", operation)
	}
}

func executeMarkdown(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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
		options := mapValue(parameters["options"])
		switch operation := textValue(parameters["operation"], MarkdownOperationToHTML); operation {
		case MarkdownOperationToHTML:
			fields[destination] = markdownToHTML(textValue(parameters["markdown"], ""), options["breaks"] == true)
		case MarkdownOperationFromHTML:
			converted, err := htmlToMarkdown(textValue(parameters["html"], ""))
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			fields[destination] = converted
		default:
			return nil, fmt.Errorf("node %q: operation %q has no runner here", ir.Name, operation)
		}
		output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

// --- Markdown to HTML ----------------------------------------------------------

var markdownCodeFence = regexp.MustCompile("^```")

// markdownToHTML renders the subset a workflow round-trips: ATX headings,
// unordered and ordered lists, fenced code, block quotes, rules, and inline
// emphasis, strong, code, links and images. Raw HTML crosses unchanged: the
// Markdown dialects this converter follows all let inline HTML through.
func markdownToHTML(source string, breaks bool) string {
	lines := strings.Split(source, "\n")
	var out strings.Builder
	var listStack []byte // 'u' or 'o', innermost last
	var paragraph []string
	closeLists := func() {
		for len(listStack) > 0 {
			inner := listStack[len(listStack)-1]
			listStack = listStack[:len(listStack)-1]
			if inner == 'u' {
				out.WriteString("</ul>\n")
			} else {
				out.WriteString("</ol>\n")
			}
		}
	}
	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		joined := paragraph
		separator := "\n"
		if breaks {
			separator = "<br>\n"
		}
		out.WriteString("<p>")
		out.WriteString(renderInlineMarkdown(strings.Join(joined, separator)))
		out.WriteString("</p>\n")
		paragraph = nil
	}
	closeList := func(kind byte) {
		if len(listStack) > 0 && listStack[len(listStack)-1] == kind {
			listStack = listStack[:len(listStack)-1]
			if kind == 'u' {
				out.WriteString("</ul>\n")
			} else {
				out.WriteString("</ol>\n")
			}
		}
	}
	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			flushParagraph()
			closeLists()
		case markdownCodeFence.MatchString(trimmed):
			flushParagraph()
			closeLists()
			language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var body []string
			index++
			for index < len(lines) && !markdownCodeFence.MatchString(strings.TrimSpace(lines[index])) {
				body = append(body, lines[index])
				index++
			}
			out.WriteString("<pre><code")
			if language != "" {
				out.WriteString(" class=\"language-")
				out.WriteString(renderInlineMarkdown(language))
				out.WriteString("\"")
			}
			out.WriteString(">")
			out.WriteString(escapeHTMLText(strings.Join(body, "\n")))
			out.WriteString("</code></pre>\n")
		case trimmed == "---" || trimmed == "***" || trimmed == "___":
			flushParagraph()
			closeLists()
			out.WriteString("<hr>\n")
		case headingLevel(trimmed) > 0:
			flushParagraph()
			closeLists()
			level := headingLevel(trimmed)
			out.WriteString(fmt.Sprintf("<h%d>", level))
			out.WriteString(renderInlineMarkdown(strings.TrimSpace(strings.TrimLeft(trimmed, "#"))))
			out.WriteString(fmt.Sprintf("</h%d>\n", level))
		case strings.HasPrefix(trimmed, "> "):
			flushParagraph()
			closeLists()
			var quoted []string
			for index < len(lines) {
				text := strings.TrimSpace(lines[index])
				if !strings.HasPrefix(text, ">") {
					break
				}
				quoted = append(quoted, strings.TrimSpace(strings.TrimPrefix(text, ">")))
				index++
			}
			index--
			out.WriteString("<blockquote>")
			out.WriteString(markdownToHTML(strings.Join(quoted, "\n"), breaks))
			out.WriteString("</blockquote>\n")
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ "):
			flushParagraph()
			closeList('o')
			if len(listStack) == 0 || listStack[len(listStack)-1] != 'u' {
				listStack = append(listStack, 'u')
				out.WriteString("<ul>\n")
			}
			out.WriteString("<li>")
			out.WriteString(renderInlineMarkdown(strings.TrimSpace(trimmed[2:])))
			out.WriteString("</li>\n")
		case orderedListItem(trimmed) != "":
			flushParagraph()
			closeList('u')
			if len(listStack) == 0 || listStack[len(listStack)-1] != 'o' {
				listStack = append(listStack, 'o')
				out.WriteString("<ol>\n")
			}
			out.WriteString("<li>")
			out.WriteString(renderInlineMarkdown(orderedListItem(trimmed)))
			out.WriteString("</li>\n")
		default:
			if len(listStack) > 0 {
				// A list item continued on the next line stays in the item.
				out.WriteString(renderInlineMarkdown(trimmed))
				continue
			}
			paragraph = append(paragraph, line)
		}
	}
	flushParagraph()
	closeLists()
	return strings.TrimSuffix(out.String(), "\n")
}

func headingLevel(line string) int {
	if !strings.HasPrefix(line, "#") {
		return 0
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level > 6 || level == len(line) || line[level] != ' ' {
		return 0
	}
	return level
}

// orderedListItem returns the item's text when the line is an ordered-list
// item ("1. text", "2) text"), or "".
func orderedListItem(line string) string {
	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits+1 >= len(line) {
		return ""
	}
	if (line[digits] == '.' || line[digits] == ')') && line[digits+1] == ' ' {
		return strings.TrimSpace(line[digits+2:])
	}
	return ""
}

var (
	markdownLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	markdownImage  = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	markdownStrong = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	markdownEm     = regexp.MustCompile(`\*([^*\s][^*]*)\*|_([^_\s][^_]*)_`)
	markdownCode   = regexp.MustCompile("`([^`]+)`")
)

// renderInlineMarkdown renders the inline marks: images before links (an
// image is a link with a bang), code before the emphasis marks (its content
// is literal), and the emphasis marks outermost-strong first. HTML characters
// in the text itself are escaped so a value can never open a tag the author
// did not write.
func renderInlineMarkdown(text string) string {
	var builder strings.Builder
	// One pass, splitting on code spans so their contents stay literal.
	rest := text
	for {
		location := markdownCode.FindStringSubmatchIndex(rest)
		if location == nil {
			builder.WriteString(renderEmphasis(rest))
			break
		}
		builder.WriteString(renderEmphasis(rest[:location[0]]))
		builder.WriteString("<code>")
		builder.WriteString(escapeHTMLText(rest[location[2]:location[3]]))
		builder.WriteString("</code>")
		rest = rest[location[1]:]
	}
	return builder.String()
}

func renderEmphasis(text string) string {
	rendered := escapeHTMLText(text)
	rendered = markdownImage.ReplaceAllString(rendered, `<img src="$2" alt="$1">`)
	rendered = markdownLink.ReplaceAllString(rendered, `<a href="$2">$1</a>`)
	rendered = markdownStrong.ReplaceAllString(rendered, "<strong>${1}${2}</strong>")
	rendered = markdownEm.ReplaceAllString(rendered, "<em>${1}${2}</em>")
	return rendered
}

func escapeHTMLText(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(text)
}

// --- HTML to Markdown ----------------------------------------------------------

// htmlToMarkdown walks the document and writes the Markdown that carries the
// same content: headings, paragraphs, lists, quotes, code, links, images and
// line breaks. Elements outside the subset render as their text, which keeps
// the words a div was carrying without pretending the div was Markdown.
func htmlToMarkdown(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", nil
	}
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", fmt.Errorf("the HTML does not parse: %w", err)
	}
	var builder strings.Builder
	writeNodeMarkdown(&builder, document, "")
	return strings.Trim(builder.String(), "\n"), nil
}

// writeNodeMarkdown renders one subtree. Blocks are separated by blank lines;
// inline content joins without them. The prefix carries a block quote's "> "
// onto every line inside it, the way Markdown itself does.
func writeNodeMarkdown(builder *strings.Builder, current *html.Node, prefix string) {
	if current.Type == html.TextNode {
		text := escapeMarkdownText(strings.TrimSpace(current.Data))
		if text != "" {
			builder.WriteString(prefix + text)
		}
		return
	}
	if current.Type != html.ElementNode {
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			writeNodeMarkdown(builder, child, prefix)
		}
		return
	}
	block := func(inner func()) {
		builder.WriteString("\n\n")
		inner()
		builder.WriteString("\n")
	}
	switch current.Data {
	// Document containers and grouping elements are transparent: their
	// children are rendered where they stand, block for block, because the
	// body of a parsed document is where the blocks live.
	case "html", "body", "template":
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			writeNodeMarkdown(builder, child, prefix)
		}
	case "head", "script", "style", "noscript", "title":
		// Code and metadata, not content: the markdown carries the words.
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(current.Data[1] - '0')
		block(func() {
			builder.WriteString(prefix + strings.Repeat("#", level) + " ")
			writeInlineMarkdown(builder, current)
		})
	case "p", "div", "section", "article", "header", "footer", "main", "nav", "aside":
		block(func() {
			builder.WriteString(prefix)
			writeInlineMarkdown(builder, current)
		})
	case "br":
		builder.WriteString("  \n" + prefix)
	case "hr":
		block(func() { builder.WriteString(prefix + "---") })
	case "blockquote":
		block(func() {
			var inner strings.Builder
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				writeNodeMarkdown(&inner, child, "")
			}
			for _, line := range strings.Split(strings.Trim(inner.String(), "\n"), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				builder.WriteString("> " + strings.TrimPrefix(line, "") + "\n")
			}
		})
	case "ul", "ol":
		block(func() {
			item := 1
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				if child.Type != html.ElementNode || child.Data != "li" {
					continue
				}
				var inner strings.Builder
				writeInlineMarkdown(&inner, child)
				if current.Data == "ul" {
					builder.WriteString(prefix + "- " + strings.Trim(inner.String(), "\n") + "\n")
				} else {
					builder.WriteString(prefix + fmt.Sprintf("%d. ", item) + strings.Trim(inner.String(), "\n") + "\n")
					item++
				}
			}
		})
	case "pre":
		block(func() {
			builder.WriteString(prefix + "```\n")
			builder.WriteString(strings.TrimPrefix(unprefixedText(current), "\n"))
			builder.WriteString("\n" + prefix + "```")
		})
	case "table":
		// A table is carried as its rows' text; pipe tables are a dialect
		// this converter does not write.
		block(func() { writeInlineMarkdown(builder, current) })
	default:
		// Inline elements and unknown blocks: render the children where they
		// stand, so their words survive even when their shape cannot.
		writeInlineMarkdown(builder, current)
	}
}

// writeInlineMarkdown renders a subtree as inline Markdown: the text with the
// marks applied, without block separation.
func writeInlineMarkdown(builder *strings.Builder, current *html.Node) {
	switch current.Type {
	case html.TextNode:
		builder.WriteString(escapeMarkdownText(strings.TrimSpace(current.Data)))
		return
	case html.CommentNode, html.DoctypeNode:
		return
	}
	if current.Type != html.ElementNode {
		return
	}
	switch current.Data {
	case "strong", "b":
		builder.WriteString("**")
		writeInlineChildren(builder, current)
		builder.WriteString("**")
	case "em", "i":
		builder.WriteString("*")
		writeInlineChildren(builder, current)
		builder.WriteString("*")
	case "code":
		builder.WriteString("`")
		builder.WriteString(escapeMarkdownText(nodeText(current)))
		builder.WriteString("`")
	case "a":
		builder.WriteString("[")
		writeInlineChildren(builder, current)
		builder.WriteString("](" + attributeValue(current, "href") + ")")
	case "img":
		alt := attributeValue(current, "alt")
		builder.WriteString("![" + alt + "](" + attributeValue(current, "src") + ")")
	default:
		writeInlineChildren(builder, current)
	}
}

func writeInlineChildren(builder *strings.Builder, current *html.Node) {
	for child := current.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(escapeMarkdownText(child.Data))
			continue
		}
		writeInlineMarkdown(builder, child)
	}
}

// unprefixedText collects a subtree's raw text — a pre block's code exactly as
// the document carries it, whitespace included.
func unprefixedText(current *html.Node) string {
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

// escapeMarkdownText escapes the characters Markdown gives meaning to when
// they appear in plain text, so a literal asterisk stays a literal asterisk.
func escapeMarkdownText(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "#", `\#`)
	return replacer.Replace(text)
}

// markdownArtwork is the node's own monochrome mark: a rounded square with an
// M and a down arrow, the markdown mark drawn in the editor's stroke idiom.
func markdownArtwork() *node.IconAsset {
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<rect x="2" y="4" width="20" height="16" rx="2"/><path d="M6 15v-4l2.5 2.5L11 11v4"/><path d="m15 11 2.5 2.5L20 11"/><path d="m14 6 2 2 2-2"/>` +
			`</svg>`),
	}
}
