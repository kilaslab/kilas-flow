package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// --- HTML ----------------------------------------------------------------------
//
// The two nodes share their parameter names (operation, html, sourceData,
// dataPropertyName, extractionValues and an options collection), so the
// translators copy. The options are an allow-list: one the native node lacks
// would change what the node writes, so the node is refused to the placeholder
// with the option named.

var htmlSupportedOptions = map[string]bool{
	"trimValues": true, "cleanUpText": true,
	"capitalize": true, "customStyling": true, "caption": true,
	"tableAttributes": true, "headerAttributes": true, "rowAttributes": true, "cellAttributes": true,
}

// HTMLOptionSupported reports whether the importer carries an HTML option. It
// exists so a test can hold the importer's list to the native node's.
func HTMLOptionSupported(key string) bool { return htmlSupportedOptions[key] }

func refuseUnsupportedHTMLOption(node Node) string {
	options, _ := node.Parameters["options"].(map[string]any)
	unsupported := make([]string, 0)
	for _, key := range sortedKeys(options) {
		if !htmlSupportedOptions[key] && !ignorableMarkdownOption(options[key]) {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return ""
	}
	return "this HTML node sets the option " + strings.Join(unsupported, ", ") + ", which KilasFlow's HTML node does not implement; " +
		"it was imported as an unsupported placeholder so the workflow does not run with a different result than the one it was written for"
}

var htmlExtractionKeys = []string{"key", "cssSelector", "returnValue", "attribute", "skipSelectors", "returnArray"}

func htmlToKilas(node Node) (map[string]any, []Unsupported) {
	converted := copyToKilas(node.Parameters, "operation", "html", "sourceData", "dataPropertyName")
	if wrapper, ok := node.Parameters["extractionValues"].(map[string]any); ok {
		rows := make([]any, 0)
		entries, _ := wrapper["values"].([]any)
		for _, entry := range entries {
			if row, isRow := entry.(map[string]any); isRow {
				rows = append(rows, copyToKilas(row, htmlExtractionKeys...))
			}
		}
		converted["extractionValues"] = map[string]any{"values": rows}
	}
	if source, ok := node.Parameters["options"].(map[string]any); ok {
		options := map[string]any{}
		for key := range htmlSupportedOptions {
			if value, present := source[key]; present {
				options[key] = fromN8NValue(value)
			}
		}
		if len(options) > 0 {
			converted["options"] = options
		}
	}
	return converted, nil
}

func htmlToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := copyToN8N(node.Parameters, "operation", "html", "sourceData", "dataPropertyName")
	if wrapper, ok := node.Parameters["extractionValues"].(map[string]any); ok {
		rows := make([]any, 0)
		entries, _ := wrapper["values"].([]any)
		for _, entry := range entries {
			if row, isRow := entry.(map[string]any); isRow {
				rows = append(rows, copyToN8N(row, htmlExtractionKeys...))
			}
		}
		written["extractionValues"] = map[string]any{"values": rows}
	}
	if source, ok := node.Parameters["options"].(map[string]any); ok && len(source) > 0 {
		options := map[string]any{}
		for key, value := range source {
			options[key] = toN8NValue(value)
		}
		written["options"] = options
	}
	return written, nil
}
