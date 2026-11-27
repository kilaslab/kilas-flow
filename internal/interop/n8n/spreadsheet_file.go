package n8n

import (
	"fmt"
	"strings"
)

// --- Spreadsheet File (legacy) -------------------------------------------------
//
// The legacy node reads and writes spreadsheets in one node; KilasFlow splits
// the two halves. Reading maps onto Extract From File (CSV or XLSX) and writing
// onto Convert to File (CSV or XLSX), chosen from the node's operation. Both
// halves are narrower than the legacy node: a file format, or an option, that
// the native node does not have is refused to the placeholder with the reason
// named, because importing it would run a workflow with different data than
// the one its author wrote.
//
// n8n omits a parameter that equals its default, and the defaults here differ
// by operation: reading defaults to autodetecting the format, writing to the
// legacy XLS format. Neither is something the native nodes do, so a node that
// never named its format is refused too, and says what to choose.

func spreadsheetFileOperation(node Node) string {
	return defaultString(stringParameter(node.Parameters, "operation"), "fromFile")
}

func spreadsheetFileFormat(node Node) string {
	fallback := "autodetect"
	if spreadsheetFileOperation(node) == "toFile" {
		fallback = "xls"
	}
	return defaultString(stringParameter(node.Parameters, "fileFormat"), fallback)
}

// spreadsheetFileKilasType picks the native node for the node's operation.
func spreadsheetFileKilasType(node Node) string {
	if spreadsheetFileOperation(node) == "toFile" {
		return ConvertToFileNodeType
	}
	return ExtractFromFileNodeType
}

func refuseUnsupportedSpreadsheetFile(node Node) string {
	const tail = "; it was imported as an unsupported placeholder so the workflow does not run with different data than the one it was written for"
	operation := spreadsheetFileOperation(node)
	format := spreadsheetFileFormat(node)
	options, _ := node.Parameters["options"].(map[string]any)

	switch operation {
	case "fromFile":
		if format != "csv" && format != "xlsx" {
			what := "reads " + format + " files"
			if format == "autodetect" {
				what = "detects the file's format on its own"
			}
			return "this Spreadsheet File node " + what + ", which KilasFlow does not: Extract From File (kilasflow.extractFromFile) reads CSV and XLSX " +
				"when the format is chosen" + tail
		}
		unsupported := make([]string, 0)
		for _, key := range sortedKeys(options) {
			// The header row is how both native readers name their columns.
			if key == "headerRow" && textOf(options[key]) != "false" {
				continue
			}
			if !ignorableMarkdownOption(options[key]) {
				unsupported = append(unsupported, key)
			}
		}
		if len(unsupported) > 0 {
			return "this Spreadsheet File node sets the option " + strings.Join(unsupported, ", ") +
				", which Extract From File (kilasflow.extractFromFile) does not implement" + tail
		}
	case "toFile":
		if format != "csv" && format != "xlsx" {
			return "this Spreadsheet File node writes " + format + " files, which KilasFlow does not: Convert to File (kilasflow.convertToFile) writes CSV and XLSX" + tail
		}
		unsupported := make([]string, 0)
		for _, key := range sortedKeys(options) {
			if !convertToFileSupportedOptions[key] && !convertToFileDroppedOptions[key] && !ignorableMarkdownOption(options[key]) {
				unsupported = append(unsupported, key)
			}
		}
		if len(unsupported) > 0 {
			return "this Spreadsheet File node sets the option " + strings.Join(unsupported, ", ") +
				", which Convert to File (kilasflow.convertToFile) does not implement" + tail
		}
	default:
		return fmt.Sprintf("this Spreadsheet File node uses the %q operation, which KilasFlow does not map: it maps reading (fromFile) and writing (toFile)%s", operation, tail)
	}
	return ""
}

func spreadsheetFileToKilas(node Node) (map[string]any, []Unsupported) {
	format := spreadsheetFileFormat(node)
	converted := map[string]any{"operation": format}
	if name := stringParameter(node.Parameters, "binaryPropertyName"); name != "" {
		converted["binaryPropertyName"] = fromN8NValue(name)
	}
	issues := make([]Unsupported, 0)
	if spreadsheetFileOperation(node) == "toFile" {
		source, _ := node.Parameters["options"].(map[string]any)
		options := map[string]any{}
		for key := range convertToFileSupportedOptions {
			if value, present := source[key]; present {
				options[key] = fromN8NValue(value)
			}
		}
		if len(options) > 0 {
			converted["options"] = options
		}
		return converted, issues
	}
	if format == "xlsx" {
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "fileFormat",
			Reason: "this node read numbers and dates from the workbook as typed values; Extract From File reads every cell as text",
		})
	}
	return converted, issues
}
