package nodes

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Markdown node's server-owned binding and type.
const (
	MarkdownExecutorID = "core.markdown"
	MarkdownNodeType   = "kilasflow.markdown"
)

// The Markdown node's modes, as n8n names them.
const (
	MarkdownModeToHTML     = "markdownToHtml"
	MarkdownModeToMarkdown = "htmlToMarkdown"
)

// MarkdownToHTMLOptionKeys and MarkdownToMarkdownOptionKeys are the options the
// node implements in each mode. The importer refuses a node that sets any
// other, so a workflow never runs with a conversion its author did not write.
var (
	MarkdownToHTMLOptionKeys = []string{
		"openLinksInNewWindow", "simpleLineBreaks", "strikethrough", "tables", "tasklists",
		"simplifiedAutoLink", "noHeaderId", "completeHTMLDocument", "allowRawHtml",
	}
	MarkdownToMarkdownOptionKeys = []string{
		"headingStyle", "bulletMarker", "emDelimiter", "strongDelimiter", "fence", "hr", "codeBlockStyle",
	}
)

func markdownNode() node.Definition {
	toHTML := []node.VisibilityCondition{{Key: "mode", Equals: MarkdownModeToHTML}}
	toMarkdown := []node.VisibilityCondition{{Key: "mode", Equals: MarkdownModeToMarkdown}}
	return node.Definition{
		Type:        MarkdownNodeType,
		Version:     workflow.V(1),
		DisplayName: "Markdown",
		Description: "Converts Markdown to HTML, or HTML to Markdown, and writes the result to a field.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:file-text"},
		IconColor:   "#64748b",
		Subtitle:    "{{ $parameter.mode }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Required: true, Default: MarkdownModeToHTML,
				Options: []node.PropertyOption{
					{Label: "Markdown to HTML", Value: MarkdownModeToHTML},
					{Label: "HTML to Markdown", Value: MarkdownModeToMarkdown},
				},
			},
			{
				Key: "markdown", Label: "Markdown", Kind: node.PropertyString, VisibleWhen: toHTML,
				TypeOptions: &node.TypeOptions{Rows: 5},
				Description: "The Markdown to convert.",
			},
			{
				Key: "html", Label: "HTML", Kind: node.PropertyString, VisibleWhen: toMarkdown,
				TypeOptions: &node.TypeOptions{Rows: 5},
				Description: "The HTML to convert.",
			},
			{
				Key: "destinationKey", Label: "Destination Key", Kind: node.PropertyString, Default: "data",
				Description: "The field the result is written to. Dot notation reaches a nested field. The rest of the item is kept.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "openLinksInNewWindow", Label: "Open Links in New Window", Kind: node.PropertyBoolean, Default: false,
						Description: "Give every link target=\"_blank\".", VisibleWhen: toHTML},
					{Key: "simpleLineBreaks", Label: "Simple Line Breaks", Kind: node.PropertyBoolean, Default: false,
						Description: "Turn a single line break inside a paragraph into <br>.", VisibleWhen: toHTML},
					{Key: "strikethrough", Label: "Strikethrough", Kind: node.PropertyBoolean, Default: true,
						Description: "Read ~~text~~ as struck-through text.", VisibleWhen: toHTML},
					{Key: "tables", Label: "Tables", Kind: node.PropertyBoolean, Default: true,
						Description: "Read pipe tables.", VisibleWhen: toHTML},
					{Key: "tasklists", Label: "Task Lists", Kind: node.PropertyBoolean, Default: true,
						Description: "Read - [ ] and - [x] as check boxes.", VisibleWhen: toHTML},
					{Key: "simplifiedAutoLink", Label: "Simplified Auto Link", Kind: node.PropertyBoolean, Default: false,
						Description: "Turn a bare web address into a link.", VisibleWhen: toHTML},
					{Key: "noHeaderId", Label: "No Header ID", Kind: node.PropertyBoolean, Default: false,
						Description: "Leave the id attribute off headings.", VisibleWhen: toHTML},
					{Key: "completeHTMLDocument", Label: "Complete HTML Document", Kind: node.PropertyBoolean, Default: false,
						Description: "Wrap the result in a full HTML page.", VisibleWhen: toHTML},
					{Key: "allowRawHtml", Label: "Allow Raw HTML", Kind: node.PropertyBoolean, Default: true,
						Description: "Keep HTML written inside the Markdown, as Markdown specifies. Turn off to replace it with a comment when the Markdown comes from someone you do not trust.", VisibleWhen: toHTML},
					{Key: "headingStyle", Label: "Heading Style", Kind: node.PropertyOptions, Default: "atx", VisibleWhen: toMarkdown,
						Options: []node.PropertyOption{{Label: "ATX (# Title)", Value: "atx"}, {Label: "Setext (underlined)", Value: "setext"}}},
					{Key: "bulletMarker", Label: "Bullet List Marker", Kind: node.PropertyOptions, Default: "-", VisibleWhen: toMarkdown,
						Options: []node.PropertyOption{{Label: "-", Value: "-"}, {Label: "*", Value: "*"}, {Label: "+", Value: "+"}}},
					{Key: "emDelimiter", Label: "Emphasis Delimiter", Kind: node.PropertyOptions, Default: "*", VisibleWhen: toMarkdown,
						Options: []node.PropertyOption{{Label: "*", Value: "*"}, {Label: "_", Value: "_"}}},
					{Key: "strongDelimiter", Label: "Strong Delimiter", Kind: node.PropertyOptions, Default: "**", VisibleWhen: toMarkdown,
						Options: []node.PropertyOption{{Label: "**", Value: "**"}, {Label: "__", Value: "__"}}},
					{Key: "fence", Label: "Code Block Fence", Kind: node.PropertyOptions, Default: "```", VisibleWhen: toMarkdown,
						Options: []node.PropertyOption{{Label: "```", Value: "```"}, {Label: "~~~", Value: "~~~"}}},
					{Key: "hr", Label: "Horizontal Rule", Kind: node.PropertyString, Default: "* * *", VisibleWhen: toMarkdown,
						Description: "What a horizontal rule is written as."},
					{Key: "codeBlockStyle", Label: "Code Block Style", Kind: node.PropertyOptions, Default: "fenced", VisibleWhen: toMarkdown,
						Options:     []node.PropertyOption{{Label: "Fenced", Value: "fenced"}},
						Description: "Code blocks are always fenced."},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     MarkdownExecutorID,
		Validate:       validateMarkdownConfiguration,
	}
}

func validateMarkdownConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["mode"]) {
		return nil
	}
	mode := textValue(n.Parameters["mode"], MarkdownModeToHTML)
	var keys []string
	switch mode {
	case MarkdownModeToHTML:
		keys = MarkdownToHTMLOptionKeys
	case MarkdownModeToMarkdown:
		keys = MarkdownToMarkdownOptionKeys
	default:
		return fmt.Errorf("mode must be markdownToHtml or htmlToMarkdown, not %q", mode)
	}
	known := map[string]bool{}
	for _, key := range keys {
		known[key] = true
	}
	options, _ := n.Parameters["options"].(map[string]any)
	for key := range options {
		if !known[key] {
			return fmt.Errorf("option %q is not supported in %s mode", key, mode)
		}
	}
	return nil
}

func executeMarkdown(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]workflow.Item, 0, len(input["main"]))
	for index, item := range input["main"] {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		result, err := convertMarkdown(parameters)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		built := cloneItem(item)
		if built.JSON == nil {
			built.JSON = map[string]any{}
		}
		setPath(built.JSON, cryptoText(parameters["destinationKey"], "data"), result)
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

// markdownMaxInput bounds the text converted in one call.
const markdownMaxInput = 8 << 20

func convertMarkdown(parameters map[string]any) (string, error) {
	options, _ := parameters["options"].(map[string]any)
	switch mode := textValue(parameters["mode"], MarkdownModeToHTML); mode {
	case MarkdownModeToHTML:
		source := textValue(parameters["markdown"], "")
		if len(source) > markdownMaxInput {
			return "", fmt.Errorf("the Markdown is larger than %d bytes", markdownMaxInput)
		}
		return markdownToHTML(source, options)
	case MarkdownModeToMarkdown:
		source := textValue(parameters["html"], "")
		if len(source) > markdownMaxInput {
			return "", fmt.Errorf("the HTML is larger than %d bytes", markdownMaxInput)
		}
		return htmlToMarkdown(source, options)
	default:
		return "", fmt.Errorf("mode must be markdownToHtml or htmlToMarkdown, not %q", mode)
	}
}

// linkTargetTransformer opens every link in a new window.
type linkTargetTransformer struct{}

func (linkTargetTransformer) Transform(document *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(document, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if link, isLink := current.(*ast.Link); isLink && entering {
			link.SetAttributeString("target", []byte("_blank"))
			link.SetAttributeString("rel", []byte("noopener noreferrer"))
		}
		return ast.WalkContinue, nil
	})
}

func markdownToHTML(source string, options map[string]any) (string, error) {
	extensions := []goldmark.Extender{}
	if optionBool(options, "tables", true) {
		extensions = append(extensions, extension.Table)
	}
	if optionBool(options, "strikethrough", true) {
		extensions = append(extensions, extension.Strikethrough)
	}
	if optionBool(options, "tasklists", true) {
		extensions = append(extensions, extension.TaskList)
	}
	if optionBool(options, "simplifiedAutoLink", false) {
		extensions = append(extensions, extension.Linkify)
	}
	parserOptions := []parser.Option{}
	if !optionBool(options, "noHeaderId", false) {
		parserOptions = append(parserOptions, parser.WithAutoHeadingID())
	}
	if optionBool(options, "openLinksInNewWindow", false) {
		parserOptions = append(parserOptions, parser.WithASTTransformers(util.Prioritized(linkTargetTransformer{}, 100)))
	}
	rendererOptions := []renderer.Option{}
	if optionBool(options, "allowRawHtml", true) {
		rendererOptions = append(rendererOptions, html.WithUnsafe())
	}
	if optionBool(options, "simpleLineBreaks", false) {
		rendererOptions = append(rendererOptions, html.WithHardWraps())
	}
	converter := goldmark.New(
		goldmark.WithExtensions(extensions...),
		goldmark.WithParserOptions(parserOptions...),
		goldmark.WithRendererOptions(rendererOptions...),
	)
	var rendered bytes.Buffer
	if err := converter.Convert([]byte(source), &rendered); err != nil {
		return "", fmt.Errorf("the Markdown could not be converted: %w", err)
	}
	result := strings.TrimRight(rendered.String(), "\n")
	if optionBool(options, "completeHTMLDocument", false) {
		result = "<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n</head>\n<body>\n" + result + "\n</body>\n</html>"
	}
	return result, nil
}

func htmlToMarkdown(source string, options map[string]any) (string, error) {
	commonmarkOptions := []commonmark.OptionFunc{}
	switch style := optionText(options, "headingStyle", "atx"); style {
	case "atx":
		commonmarkOptions = append(commonmarkOptions, commonmark.WithHeadingStyle(commonmark.HeadingStyleATX))
	case "setext":
		commonmarkOptions = append(commonmarkOptions, commonmark.WithHeadingStyle(commonmark.HeadingStyleSetext))
	default:
		return "", fmt.Errorf("heading style must be atx or setext, not %q", style)
	}
	if marker := optionText(options, "bulletMarker", "-"); marker == "-" || marker == "*" || marker == "+" {
		commonmarkOptions = append(commonmarkOptions, commonmark.WithBulletListMarker(marker))
	} else {
		return "", fmt.Errorf("bullet marker must be -, * or +, not %q", marker)
	}
	if delimiter := optionText(options, "emDelimiter", "*"); delimiter == "*" || delimiter == "_" {
		commonmarkOptions = append(commonmarkOptions, commonmark.WithEmDelimiter(delimiter))
	} else {
		return "", fmt.Errorf("emphasis delimiter must be * or _, not %q", delimiter)
	}
	if delimiter := optionText(options, "strongDelimiter", "**"); delimiter == "**" || delimiter == "__" {
		commonmarkOptions = append(commonmarkOptions, commonmark.WithStrongDelimiter(delimiter))
	} else {
		return "", fmt.Errorf("strong delimiter must be ** or __, not %q", delimiter)
	}
	if fence := optionText(options, "fence", "```"); fence == "```" || fence == "~~~" {
		commonmarkOptions = append(commonmarkOptions, commonmark.WithCodeBlockFence(fence))
	} else {
		return "", fmt.Errorf("code block fence must be ``` or ~~~, not %q", fence)
	}
	if rule := optionText(options, "hr", ""); rule != "" {
		commonmarkOptions = append(commonmarkOptions, commonmark.WithHorizontalRule(rule))
	}
	if style := optionText(options, "codeBlockStyle", "fenced"); style != "fenced" {
		return "", fmt.Errorf("code blocks are always written fenced, not %q", style)
	}
	conv := htmltomarkdown.NewConverter(htmltomarkdown.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(commonmarkOptions...),
		strikethrough.NewStrikethroughPlugin(),
		table.NewTablePlugin(),
	))
	result, err := conv.ConvertString(source)
	if err != nil {
		return "", fmt.Errorf("the HTML could not be converted: %w", err)
	}
	return strings.TrimSpace(result), nil
}
