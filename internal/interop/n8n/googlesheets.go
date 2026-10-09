package n8n

import (
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Google Sheets pair's canonical types, mirrored from
// nodes.GoogleSheetsNodeType and nodes.GoogleSheetsTriggerNodeType rather than
// imported, for the same reason every other type here is.
// TestSheetsTypesMatchTheNodePack keeps them in step.
const (
	GoogleSheetsNodeType        = "kilasflow.googleSheets"
	GoogleSheetsTriggerNodeType = "kilasflow.googleSheetsTrigger"
)

// googleSheetsPublishedVersions are the typeVersions of n8n's Google Sheets
// node that this server reads and writes. n8n also publishes 3, whose
// parameters are a different shape (a data mode, a column to match on, a list of
// fields); an import of it is blocked and an export never writes it.
var googleSheetsPublishedVersions = []float64{4, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7}

// googleSheetsExportVersion is the version a node goes back out at, by the
// same rule as the mapping table: its own when n8n publishes it, else the pin.
func googleSheetsExportVersion(node workflow.Node) float64 {
	version := node.TypeVersion.Float()
	for _, published := range googleSheetsPublishedVersions {
		if published == version {
			return version
		}
	}
	return 4.7
}

// googleSheetsOperations are the operations of the sheet resource this server
// runs. n8n also has create and remove, which act on whole sheets.
var googleSheetsOperations = map[string]bool{
	"read": true, "append": true, "appendOrUpdate": true, "update": true, "delete": true, "clear": true,
}

// copyTree is a deep copy of a JSON-shaped value.
func copyTree(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(typed))
		for key, nested := range typed {
			copied[key] = copyTree(nested)
		}
		return copied
	case []any:
		copied := make([]any, len(typed))
		for index, nested := range typed {
			copied[index] = copyTree(nested)
		}
		return copied
	default:
		return value
	}
}

// sheetsMapper converts a resourceMapper value in either direction. Only the
// values are expressions: the schema rows, the matching columns and the flags
// around them are data, and a column labelled "=total" must not turn into one.
func sheetsMapper(raw any, convertValue func(any) any) any {
	mapper, ok := raw.(map[string]any)
	if !ok {
		return copyTree(raw)
	}
	converted := make(map[string]any, len(mapper))
	for key, value := range mapper {
		if key == "value" {
			converted[key] = convertValue(value)
			continue
		}
		converted[key] = copyTree(value)
	}
	return converted
}

func googleSheetsIssue(field, reason string, severity IssueSeverity) Unsupported {
	return Unsupported{Severity: severity, Field: field, Reason: reason}
}

// googleSheetsToKilas maps n8n's Google Sheets node onto the native one.
//
// The parameters cross as they are, with expressions marked, because the native
// node reads n8n's own names and values. What is rewritten is what n8n's
// version decided and a native node has one meaning for: the operation a
// missing key defaults to, the way filters combine, and the first-match option.
func googleSheetsToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := make(map[string]any, len(node.Parameters)+2)
	for key, value := range node.Parameters {
		switch key {
		case "columns":
			parameters[key] = sheetsMapper(value, fromN8NTree)
		default:
			parameters[key] = fromN8NTree(value)
		}
	}
	version := node.TypeVersion

	if version != 0 && version < 4 {
		issues = append(issues, googleSheetsIssue("typeVersion", fmt.Sprintf(
			"this node is Google Sheets version %s, whose parameters (a data mode, a column to match on and a list of fields) "+
				"are a shape this server does not implement. It was imported with what it carried: rebuild its columns on "+
				"version 4 or later in n8n, or set them again here, before activating.", formatN8NVersion(version)), SeverityBlocking))
	}
	if authentication := stringParameter(node.Parameters, "authentication"); authentication == "serviceAccount" {
		issues = append(issues, googleSheetsIssue("authentication", "this node signed in with a Google service account, "+
			"which this server does not support: Google Sheets here signs in with an OAuth2 credential. "+
			"It was imported with that setting: switch it to OAuth2 and connect a credential before activating.", SeverityBlocking))
	}
	if _, usesServiceAccount := node.Credentials["googleApi"]; usesServiceAccount {
		issues = append(issues, googleSheetsIssue("credentials", "this node used a Google service account credential (googleApi), "+
			"which this server does not support. Connect a Google Sheets OAuth2 credential before activating.", SeverityBlocking))
	}

	resource := stringParameter(node.Parameters, "resource")
	if resource != "" && resource != "sheet" {
		issues = append(issues, googleSheetsIssue("resource", fmt.Sprintf(
			"this node worked on the %q resource, which this server does not implement: it reads and writes the rows of a sheet, "+
				"and does not create or delete spreadsheets. It was imported with that setting: replace the node before activating.", resource), SeverityBlocking))
	}

	operation := stringParameter(node.Parameters, "operation")
	if _, present := node.Parameters["operation"]; !present || operation == "" {
		// n8n leaves its default out of the file. Reading none as "do nothing"
		// would be a guess; read is what n8n runs.
		operation = "read"
		parameters["operation"] = operation
	}
	if resource == "" || resource == "sheet" {
		switch {
		case googleSheetsOperations[operation]:
		case operation == "create" || operation == "remove":
			issues = append(issues, googleSheetsIssue("operation", fmt.Sprintf(
				"this node ran the operation %q, which creates or deletes a whole sheet and which this server does not implement. "+
					"It was imported with that operation: replace the node before activating.", operation), SeverityBlocking))
		case !isExpressionText(operation):
			issues = append(issues, googleSheetsIssue("operation", fmt.Sprintf(
				"this node ran the operation %q, which this server does not implement. "+
					"It was imported with that operation: set the operation before activating.", operation), SeverityBlocking))
		}
	}

	if operation == "read" {
		if _, present := parameters["combineFilters"]; !present {
			// The default moved at 4.3, and a native node has the one meaning.
			if version != 0 && version < 4.3 {
				parameters["combineFilters"] = "OR"
			} else {
				parameters["combineFilters"] = "AND"
			}
		}
		if version != 0 && version < 4.5 {
			options, _ := parameters["options"].(map[string]any)
			if options == nil {
				options = map[string]any{}
			}
			// Before 4.5 the option was a choice between the first match and
			// all of them, defaulting to the first. From 4.5 it is a flag.
			options["returnFirstMatch"] = stringFrom(options["returnAllMatches"]) != "returnAllMatches"
			delete(options, "returnAllMatches")
			parameters["options"] = options
		}
	}
	return parameters, issues
}

// stringFrom reads a parameter that may be text.
func stringFrom(value any) string {
	text, _ := value.(string)
	return text
}

// isExpressionText reports an operation n8n computed rather than chose.
func isExpressionText(text string) bool {
	return strings.Contains(text, "{{")
}

// googleSheetsToN8N writes the native node back in n8n's shape, at the version
// it came from, so the options that changed between versions go out in that
// version's spelling.
func googleSheetsToN8N(node workflow.Node) (map[string]any, []Lossy) {
	version := googleSheetsExportVersion(node)
	parameters := make(map[string]any, len(node.Parameters))
	for key, value := range node.Parameters {
		switch key {
		case "columns":
			parameters[key] = sheetsMapper(value, toN8NTree)
		default:
			parameters[key] = toN8NTree(value)
		}
	}
	if options, ok := parameters["options"].(map[string]any); ok && version < 4.5 {
		if first, present := options["returnFirstMatch"]; present {
			if firstOnly, isBool := first.(bool); isBool && !firstOnly {
				options["returnAllMatches"] = "returnAllMatches"
			} else {
				options["returnAllMatches"] = "returnFirstMatch"
			}
			delete(options, "returnFirstMatch")
		}
	}
	return parameters, nil
}

// googleSheetsTriggerToKilas maps n8n's Google Sheets Trigger.
func googleSheetsTriggerToKilas(node Node) (map[string]any, []Unsupported) {
	issues := make([]Unsupported, 0)
	parameters := make(map[string]any, len(node.Parameters))
	for key, value := range node.Parameters {
		parameters[key] = fromN8NTree(value)
	}
	if authentication := stringParameter(node.Parameters, "authentication"); authentication == "serviceAccount" {
		issues = append(issues, googleSheetsIssue("authentication", "this trigger signed in with a Google service account, "+
			"which this server does not support: it signs in with an OAuth2 credential. "+
			"It was imported with that setting: switch it to OAuth2 and connect a credential before activating.", SeverityBlocking))
	}
	if _, usesServiceAccount := node.Credentials["googleApi"]; usesServiceAccount {
		issues = append(issues, googleSheetsIssue("credentials", "this trigger used a Google service account credential (googleApi), "+
			"which this server does not support. Connect a Google Sheets Trigger OAuth2 credential before activating.", SeverityBlocking))
	}
	switch include := stringParameter(node.Parameters, "includeInOutput"); include {
	case "old", "both":
		parameters["includeInOutput"] = "new"
		issues = append(issues, googleSheetsIssue("includeInOutput", fmt.Sprintf(
			"this trigger put the %s version of an updated row in its output. This server compares fingerprints of the rows and does not keep "+
				"their previous values, so it emits the new version only.", map[string]string{"old": "old", "both": "old and new"}[include]), SeverityLossy))
	}
	return parameters, issues
}

func googleSheetsTriggerToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters := make(map[string]any, len(node.Parameters))
	for key, value := range node.Parameters {
		parameters[key] = toN8NTree(value)
	}
	return parameters, nil
}
