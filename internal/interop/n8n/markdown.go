package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// --- Markdown ------------------------------------------------------------------
//
// The two nodes share their parameter names (mode, markdown, html,
// destinationKey and an options collection), so the translators copy. Two
// things need care:
//
//   - n8n's converters have defaults of their own that differ from the native
//     node's (Markdown to HTML leaves tables, strikethrough, task lists and
//     auto links off; HTML to Markdown writes setext headings, `*` bullets and
//     `_` emphasis). n8n omits an option that equals its default, so the import
//     writes those defaults out, and the converted text stays what the author's
//     workflow produced.
//   - The options are an allow-list per mode: one the native node lacks would
//     change the output, so the node is refused to the placeholder with the
//     option named.

var markdownToHTMLOptions = map[string]bool{
	"openLinksInNewWindow": true, "simpleLineBreaks": true, "strikethrough": true, "tables": true,
	"tasklists": true, "simplifiedAutoLink": true, "noHeaderId": true, "completeHTMLDocument": true,
}

var htmlToMarkdownOptions = map[string]bool{
	"headingStyle": true, "bulletMarker": true, "emDelimiter": true, "strongDelimiter": true,
	"fence": true, "hr": true, "codeBlockStyle": true,
}

// MarkdownOptionSupported reports whether the importer carries a Markdown
// option in a mode. It exists so a test can hold the importer's lists to the
// native node's.
func MarkdownOptionSupported(mode, key string) bool {
	if mode == "htmlToMarkdown" {
		return htmlToMarkdownOptions[key]
	}
	return markdownToHTMLOptions[key] || key == "allowRawHtml"
}

func markdownMode(node Node) string {
	return defaultString(stringParameter(node.Parameters, "mode"), "markdownToHtml")
}

// ignorableMarkdownOption reports whether an option the node lacks can be left
// out without changing the output: one set to off or to nothing.
func ignorableMarkdownOption(value any) bool {
	if enabled, isBool := value.(bool); isBool {
		return !enabled
	}
	text, isText := value.(string)
	return value == nil || (isText && text == "")
}

func refuseUnsupportedMarkdownOption(node Node) string {
	mode := markdownMode(node)
	options, _ := node.Parameters["options"].(map[string]any)
	unsupported := make([]string, 0)
	for _, key := range sortedKeys(options) {
		supported := markdownToHTMLOptions[key]
		if mode == "htmlToMarkdown" {
			supported = htmlToMarkdownOptions[key]
		}
		if !supported && !ignorableMarkdownOption(options[key]) {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return ""
	}
	return "this Markdown node sets the option " + strings.Join(unsupported, ", ") + ", which KilasFlow's Markdown node does not implement; " +
		"it was imported as an unsupported placeholder so the workflow does not run with a different conversion than the one it was written for"
}

// n8nMarkdownDefaults are the values n8n's converters use when an option is
// not set, written out on import. The booleans are Markdown to HTML's; the
// strings are HTML to Markdown's.
var (
	markdownToHTMLDefaults = map[string]any{
		"tables": false, "strikethrough": false, "tasklists": false, "simplifiedAutoLink": false,
	}
	htmlToMarkdownDefaults = map[string]any{
		"headingStyle": "setext", "bulletMarker": "*", "emDelimiter": "_",
	}
)

func markdownToKilas(node Node) (map[string]any, []Unsupported) {
	converted := copyToKilas(node.Parameters, "mode", "markdown", "html", "destinationKey")
	mode := markdownMode(node)
	source, _ := node.Parameters["options"].(map[string]any)
	options := map[string]any{}
	issues := make([]Unsupported, 0)
	if mode == "htmlToMarkdown" {
		for key, fallback := range htmlToMarkdownDefaults {
			options[key] = fallback
		}
		for key := range htmlToMarkdownOptions {
			if value, present := source[key]; present {
				options[key] = fromN8NValue(value)
			}
		}
		// The converter writes fenced code blocks only; n8n's default is
		// indented ones.
		if style := textOf(source["codeBlockStyle"]); style != "fenced" {
			delete(options, "codeBlockStyle")
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "options.codeBlockStyle",
				Reason: "this node writes code blocks indented (the converter's default), and KilasFlow's Markdown node writes them fenced",
			})
		}
	} else {
		for key, fallback := range markdownToHTMLDefaults {
			options[key] = fallback
		}
		for key := range markdownToHTMLOptions {
			if value, present := source[key]; present {
				options[key] = fromN8NValue(value)
			}
		}
	}
	converted["options"] = options
	return converted, issues
}

func markdownToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := copyToN8N(node.Parameters, "mode", "markdown", "html", "destinationKey")
	lossy := make([]Lossy, 0)
	source, _ := node.Parameters["options"].(map[string]any)
	options := map[string]any{}
	for key, value := range source {
		if key == "allowRawHtml" {
			if enabled, isBool := value.(bool); isBool && !enabled {
				lossy = append(lossy, Lossy{Field: "options.allowRawHtml", Reason: "n8n's Markdown node always keeps raw HTML written inside the Markdown"})
			}
			continue
		}
		options[key] = toN8NValue(value)
	}
	if len(options) > 0 {
		written["options"] = options
	}
	return written, lossy
}
