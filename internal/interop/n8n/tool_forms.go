package n8n

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// --- Agent-tool forms ---------------------------------------------------------
//
// n8n offers every usableAsTool node to an agent as its own type with Tool
// appended (gmail → gmailTool) at the base node's own versions. The tool keeps
// the base node's parameters and adds two: descriptionType, which says
// whether the description is written by the author ("manual") or generated
// from the operation ("auto", the default for a node with a resource or an
// operation), and toolDescription. The model-facing name is the node's own
// canvas name. A tool form here is the same node with the same parameters, so
// its translator is the base node's plus those differences.

// The tool forms' canonical types, mirrored from the nodes package as the
// other node types are. TestMirroredNodeTypesMatchTheNodePack keeps them in
// step.
const (
	GmailToolNodeType    = "kilasflow.gmailTool"
	PostgresToolNodeType = "kilasflow.postgresTool"
	// GoogleSheetsToolNodeType is the Sheets tool form; the node itself is in googlesheets.go.
	GoogleSheetsToolNodeType = "kilasflow.googleSheetsTool"
	TelegramToolNodeType     = "pack.telegramTool"
)

// toolOnlyParameters are what n8n adds to a node to make its tool; the base
// translator never sees them.
var toolOnlyParameters = []string{"toolDescription", "descriptionType"}

// toolFormToKilas is the import translator of a tool form: the base node's
// translator, n8n's nested `=` strings made expressions, then the tool's own
// name and description.
func toolFormToKilas(base mapping) func(node Node) (map[string]any, []Unsupported) {
	return func(node Node) (map[string]any, []Unsupported) {
		stripped := node
		stripped.Parameters = make(map[string]any, len(node.Parameters))
		for key, value := range node.Parameters {
			stripped.Parameters[key] = value
		}
		for _, key := range toolOnlyParameters {
			delete(stripped.Parameters, key)
		}
		var parameters map[string]any
		var issues []Unsupported
		if base.toKilas != nil {
			parameters, issues = base.toKilas(stripped)
		}
		if parameters == nil {
			parameters = map[string]any{}
		}
		// n8n evaluates a `=` string at any depth, and the base translators
		// convert only the ones they know about. A tool's $fromAI usually sits
		// in a nested one (a filter, a column value, a query replacement), and
		// left as a plain string it would be filled with the model's text and
		// sent as `=` plus that text.
		parameters, _ = deepFromN8NValue(parameters).(map[string]any)
		issues = append(issues, withToolFields(node, parameters, true)...)
		return parameters, issues
	}
}

// withToolFields writes the tool's name and description.
//
// n8n has no tool-name parameter: the model calls the tool by its canvas
// name, so the name is derived the same way and an imported system prompt
// that names the tool still names it. The description is n8n's when it
// stored one. When it did not, n8n generates one from the operation at run
// time; this server has no generated descriptions, so one is written, and for
// a tool form that is reported, because the model now reads different words.
func withToolFields(node Node, parameters map[string]any, reportWritten bool) []Unsupported {
	parameters["toolName"] = nodes.NormalizeToolName(node.Name)
	description := node.Parameters["toolDescription"]
	if text, isText := description.(string); description != nil && (!isText || strings.TrimSpace(strings.TrimPrefix(text, "=")) != "") {
		parameters["toolDescription"] = fromN8NValue(description)
		return nil
	}
	written := "Calls " + node.Name + "."
	if operation := stringParameter(node.Parameters, "operation"); operation != "" {
		written = fmt.Sprintf("Calls %s, which runs the %q operation.", node.Name, operation)
	}
	parameters["toolDescription"] = written
	if !reportWritten {
		return nil
	}
	return []Unsupported{{
		Severity: SeverityLossy, Field: "toolDescription",
		Reason: fmt.Sprintf("n8n generates this tool's description from its operation when none is written; "+
			"this server needs one written, so it was set to %q. Describe when the model should call the tool.", written),
	}}
}

// toolFormToN8N is the export translator of a tool form: the base node's
// translator, nested expressions back to n8n's `=` strings, then the tool's
// description as a written one.
func toolFormToN8N(base mapping) func(node workflow.Node) (map[string]any, []Lossy) {
	return func(node workflow.Node) (map[string]any, []Lossy) {
		stripped := node
		stripped.Parameters = make(map[string]any, len(node.Parameters))
		for key, value := range node.Parameters {
			stripped.Parameters[key] = value
		}
		delete(stripped.Parameters, "toolName")
		delete(stripped.Parameters, "toolDescription")
		var parameters map[string]any
		var lossy []Lossy
		if base.toN8N != nil {
			parameters, lossy = base.toN8N(stripped)
		}
		if parameters == nil {
			parameters = map[string]any{}
		}
		parameters, _ = deepToN8NValue(parameters).(map[string]any)
		// Written by the author here, so it goes out as n8n's manual mode;
		// left at n8n's default (auto), n8n would replace it with one of its
		// own.
		parameters["descriptionType"] = "manual"
		parameters["toolDescription"] = toN8NValue(node.Parameters["toolDescription"])
		return parameters, append(lossy, toolNameLossy(node)...)
	}
}

// toolNameLossy reports a tool name n8n cannot express: there, a tool is
// called by its node's name.
func toolNameLossy(node workflow.Node) []Lossy {
	name := stringParameter(node.Parameters, "toolName")
	if name == "" || name == nodes.NormalizeToolName(node.Name) {
		return nil
	}
	return []Lossy{{
		Severity: SeverityLossy, Field: "toolName",
		Reason: fmt.Sprintf("the model called this tool %q. n8n derives a tool's name from the node's own name, "+
			"so after export it is called %q; rename the node to keep the old name.", name, nodes.NormalizeToolName(node.Name)),
	}}
}

// deepFromN8NValue converts every `=` string in a parameter tree, at any depth,
// into an expression marker. A marker already there is left as it is.
func deepFromN8NValue(value any) any {
	if expression.IsExpression(value) {
		return value
	}
	switch typed := value.(type) {
	case string:
		return fromN8NValue(typed)
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted[key] = deepFromN8NValue(nested)
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, nested := range typed {
			converted[index] = deepFromN8NValue(nested)
		}
		return converted
	default:
		return value
	}
}

// deepToN8NValue is the inverse: every expression marker, at any depth, back
// to n8n's `=` string.
func deepToN8NValue(value any) any {
	if expression.IsExpression(value) {
		return toN8NValue(value)
	}
	switch typed := value.(type) {
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, nested := range typed {
			converted[key] = deepToN8NValue(nested)
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, nested := range typed {
			converted[index] = deepToN8NValue(nested)
		}
		return converted
	default:
		return value
	}
}

// postgresToolToKilas maps n8n's PostgreSQL tool. On top of the generic tool
// translation, SQL the model writes is a blocking issue: n8n splices an
// expression in Query into the statement text, so a $fromAI there is the
// model writing SQL, which this server's tool never runs.
func postgresToolToKilas(node Node) (map[string]any, []Unsupported) {
	// The base translator named directly rather than looked up: the lookup
	// reads toolVariants, which names this function.
	parameters, issues := toolFormToKilas(mapping{toKilas: postgresToKilas})(node)
	if parameters["operation"] == "executeQuery" && expression.IsExpression(parameters["query"]) {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "query",
			Reason: "this tool builds its SQL from an expression, so the model (through $fromAI) or upstream data " +
				"writes part of the statement. This server's PostgreSQL tool only runs SQL the author wrote: put $1, $2 " +
				"placeholders in the query and the values in Query Parameters, or use the select, insert, update or " +
				"delete operation, before activating.",
		})
	}
	switch parameters["operation"] {
	case "insert", "update", "upsert":
		if columns, _ := parameters["columns"].(map[string]any); columns["mappingMode"] == "autoMapInputData" {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "columns",
				Reason: "this tool maps its columns automatically from the item it runs on, which for a tool is the " +
					"agent's input rather than a row to write. This server's PostgreSQL tool writes only columns the " +
					"author mapped: map each column, using $fromAI for the values the model supplies, before activating.",
			})
		}
	}
	return parameters, issues
}

// googleSheetsToolToKilas maps n8n's Google Sheets tool: the generic tool
// translation over the Sheets node's own, with the mapper restored from the
// original so the generic `=` pass cannot read a schema label such as "=total"
// as an expression, and two blocking issues for what this server's tool never
// does: clear a sheet, and map its columns from the item it runs on, which for
// a tool is the agent's input.
func googleSheetsToolToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, issues := toolFormToKilas(mapping{toKilas: googleSheetsToKilas})(node)
	if original, ok := node.Parameters["columns"].(map[string]any); ok {
		parameters["columns"] = sheetsMapper(original, fromN8NTree)
	}
	operation, _ := parameters["operation"].(string)
	if operation == "clear" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "operation",
			Reason: "this tool clears a sheet, which empties a whole tab or range in one call. This server's Google Sheets tool " +
				"does not offer clear to a model: use delete for rows or columns of a size you set, or the Google Sheets node " +
				"as a step, before activating.",
		})
	}
	if operation == "delete" && parameters["toDelete"] == "columns" {
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "toDelete",
			Reason: "this tool deletes columns, and a start column the model chooses can reach the column that holds the " +
				"names. This server's Google Sheets tool deletes rows only: set Delete to Rows, or use the Google Sheets " +
				"node as a step, before activating.",
		})
	}
	switch operation {
	case "append", "appendOrUpdate", "update":
		issues = append(issues, Unsupported{
			Severity: SeverityLossy, Field: "cellFormat",
			Reason: "n8n writes a tool's values as typed, so a value that starts with = becomes a formula. This server's " +
				"Google Sheets tool always writes RAW, whatever Cell Format says, so a value a model supplies stays text.",
		})
		if columns, _ := parameters["columns"].(map[string]any); columns["mappingMode"] == "autoMapInputData" {
			issues = append(issues, Unsupported{
				Severity: SeverityBlocking, Field: "columns",
				Reason: "this tool maps its columns automatically from the item it runs on, which for a tool is the agent's " +
					"input rather than a row to write. This server's Google Sheets tool writes only columns the author mapped: " +
					"map each column, using $fromAI for the values the model supplies, before activating.",
			})
		}
	}
	return parameters, issues
}

// --- Gmail ---------------------------------------------------------------------

// gmailOperations are the message operations this server's Gmail node runs.
var gmailOperations = map[string]bool{"get": true, "getAll": true, "send": true}

// gmailToKilas maps n8n's Gmail node.
//
// Three things the verbatim copy got wrong. The recipient is `sendTo` in n8n's
// version 2 (a `toList` array in version 1) and `to` here, so a send arrived
// with no recipient. n8n's defaults are not this node's: an export omits a
// parameter still at its default, and n8n's Gmail sends by default (version 1
// even defaults to the draft resource) and sends HTML, where this node gets a
// message and sends text. And an operation this node does not run imported
// cleanly and failed on its first run.
func gmailToKilas(node Node) (map[string]any, []Unsupported) {
	parameters, _ := packToKilas(node)
	if parameters == nil {
		parameters = map[string]any{}
	}
	issues := make([]Unsupported, 0)
	versionOne := node.TypeVersion > 0 && node.TypeVersion < 2

	resource := stringParameter(node.Parameters, "resource")
	if resource == "" {
		resource = "message"
		if versionOne {
			resource = "draft"
		}
	}
	operation := stringParameter(node.Parameters, "operation")
	if operation == "" {
		operation = "send"
	}
	parameters["resource"] = resource
	parameters["operation"] = operation
	switch {
	case resource != "message":
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "resource",
			Reason: fmt.Sprintf("this server's Gmail node works on messages only, and this node works on %q; "+
				"replace it or rebuild the call as an HTTP Request before activating", resource),
		})
	case !gmailOperations[operation]:
		issues = append(issues, Unsupported{
			Severity: SeverityBlocking, Field: "operation",
			Reason: fmt.Sprintf("this server's Gmail node gets, lists and sends messages, and has no %q; "+
				"replace it or rebuild the call as an HTTP Request before activating", operation),
		})
	}

	// The recipient, under this node's name.
	delete(parameters, "sendTo")
	delete(parameters, "toList")
	if sendTo, present := node.Parameters["sendTo"]; present {
		parameters["to"] = fromN8NValue(sendTo)
	} else if list, present := node.Parameters["toList"]; present {
		parameters["to"] = gmailRecipients(list)
	}

	if operation == "send" {
		if versionOne {
			parameters["emailType"] = "text"
			if includeHTML, _ := node.Parameters["includeHtml"].(bool); includeHTML {
				issues = append(issues, Unsupported{
					Severity: SeverityDropped, Field: "htmlMessage",
					Reason: "this node also sent an HTML body; this server's Gmail node sends one body, so the plain-text message is sent",
				})
			}
		} else if stringParameter(node.Parameters, "emailType") == "" {
			// n8n's own default, materialised because this node's is text.
			parameters["emailType"] = "html"
		}
	}
	delete(parameters, "includeHtml")
	delete(parameters, "htmlMessage")

	for _, collection := range []string{"options", "additionalFields"} {
		options, _ := node.Parameters[collection].(map[string]any)
		delete(parameters, collection)
		for _, key := range sortedKeys(options) {
			if options[key] == nil {
				continue
			}
			if key == "appendAttribution" && options[key] == false {
				// This node never appends one, so nothing is lost.
				continue
			}
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: collection + "." + key,
				Reason: fmt.Sprintf("the n8n Gmail option %q has no equivalent in this server's Gmail node and was not carried", key),
			})
		}
	}
	if operation == "getAll" {
		returnAll, _ := node.Parameters["returnAll"].(bool)
		if limit, ok := numberParameter(node.Parameters, "limit"); returnAll || (ok && limit > 50) {
			issues = append(issues, Unsupported{
				Severity: SeverityLossy, Field: "limit",
				Reason: "this server's Gmail node lists at most 50 messages per run, fewer than this node asked for",
			})
		}
	}
	if filters, ok := node.Parameters["filters"].(map[string]any); ok {
		unread := make([]string, 0)
		for _, key := range []string{"includeSpamTrash", "receivedAfter", "receivedBefore"} {
			if _, present := filters[key]; present {
				unread = append(unread, key)
			}
		}
		if len(unread) > 0 {
			issues = append(issues, Unsupported{
				Severity: SeverityDropped, Field: "filters",
				Reason: "these Gmail filters are not applied by this server's Gmail node: " + strings.Join(unread, ", "),
			})
		}
	}
	return parameters, issues
}

// gmailRecipients joins version 1's recipient list into one address line.
func gmailRecipients(value any) any {
	list, ok := value.([]any)
	if !ok {
		return fromN8NValue(value)
	}
	addresses := make([]string, 0, len(list))
	for _, entry := range list {
		if text, isText := entry.(string); isText && strings.TrimSpace(text) != "" {
			addresses = append(addresses, strings.TrimSpace(text))
		}
	}
	return strings.Join(addresses, ", ")
}

// gmailToN8N writes the node back in n8n's version 2 shape.
func gmailToN8N(node workflow.Node) (map[string]any, []Lossy) {
	parameters, _ := packToN8N(node)
	if parameters == nil {
		parameters = map[string]any{}
	}
	if to, present := parameters["to"]; present {
		parameters["sendTo"] = to
		delete(parameters, "to")
	}
	if stringParameter(node.Parameters, "operation") == "" {
		// This node's default; n8n's is send.
		parameters["operation"] = "get"
	}
	if operation, _ := parameters["operation"].(string); operation == "send" {
		if _, present := parameters["emailType"]; !present {
			// This node's default; n8n's is html.
			parameters["emailType"] = "text"
		}
		// n8n appends an attribution line to a sent mail from 2.1 unless told
		// not to; this node sends none, so the export says not to.
		options, _ := parameters["options"].(map[string]any)
		copied := make(map[string]any, len(options)+1)
		for key, value := range options {
			copied[key] = value
		}
		copied["appendAttribution"] = false
		parameters["options"] = copied
	}
	return parameters, nil
}

// sortedToolVariantKeys lists the base types with a tool form, in order.
func sortedToolVariantKeys() []string {
	keys := make([]string, 0, len(toolVariants))
	for key := range toolVariants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
