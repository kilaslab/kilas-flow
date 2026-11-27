package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// --- Convert to File -----------------------------------------------------------
//
// The two nodes share their parameter names (operation, mode, sourceProperty,
// binaryPropertyName and an options collection), so the translators copy. What
// the native node writes is narrower than n8n's: CSV, XLSX, JSON, a text file
// and a base64 string decoded to a file. A node that asks for another format
// (HTML, iCalendar, ODS, RTF, legacy XLS) or an option the native node lacks
// would produce a different file than its author intended, so it is refused to
// the placeholder with the reason named.

var convertToFileOperations = map[string]bool{
	"csv": true, "xlsx": true, "toJson": true, "toText": true, "toBinary": true,
}

var convertToFileSupportedOptions = map[string]bool{
	"fileName": true, "delimiter": true, "headerRow": true, "sheetName": true, "format": true,
	"encoding": true, "addBOM": true, "mimeType": true,
}

// convertToFileDroppedOptions are accepted and not carried: they change how a
// file is packed, never what it holds. (The native workbook is always
// compressed.)
var convertToFileDroppedOptions = map[string]bool{"compression": true}

// ConvertToFileOptionSupported reports whether the importer carries a Convert
// to File option. It exists so a test can hold the importer's list to the
// native node's.
func ConvertToFileOptionSupported(key string) bool { return convertToFileSupportedOptions[key] }

func refuseUnsupportedConvertToFile(node Node) string {
	const tail = "; it was imported as an unsupported placeholder so the workflow does not run with a different file than the one it was written for"
	operation := defaultString(stringParameter(node.Parameters, "operation"), "csv")
	if !convertToFileOperations[operation] {
		return "this Convert to File node writes " + operation + ", which KilasFlow's Convert to File node does not: it writes " +
			"CSV, XLSX, JSON, text files, and files decoded from base64" + tail
	}
	options, _ := node.Parameters["options"].(map[string]any)
	unsupported := make([]string, 0)
	for _, key := range sortedKeys(options) {
		if !convertToFileSupportedOptions[key] && !convertToFileDroppedOptions[key] && !ignorableMarkdownOption(options[key]) {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		return "this Convert to File node sets the option " + strings.Join(unsupported, ", ") +
			", which KilasFlow's Convert to File node does not implement" + tail
	}
	if encoding := strings.ToLower(strings.ReplaceAll(textOf(options["encoding"]), "-", "")); encoding != "" && encoding != "utf8" {
		return "this Convert to File node writes its file as " + textOf(options["encoding"]) +
			", and KilasFlow's Convert to File node writes UTF-8 only" + tail
	}
	return ""
}

func convertToFileToKilas(node Node) (map[string]any, []Unsupported) {
	converted := copyToKilas(node.Parameters, "operation", "mode", "sourceProperty", "binaryPropertyName")
	if source, ok := node.Parameters["options"].(map[string]any); ok {
		options := map[string]any{}
		for key := range convertToFileSupportedOptions {
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

func convertToFileToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := copyToN8N(node.Parameters, "operation", "mode", "sourceProperty", "binaryPropertyName")
	if source, ok := node.Parameters["options"].(map[string]any); ok && len(source) > 0 {
		options := map[string]any{}
		for key, value := range source {
			options[key] = toN8NValue(value)
		}
		written["options"] = options
	}
	return written, nil
}
