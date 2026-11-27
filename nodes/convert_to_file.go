package nodes

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Convert to File node's server-owned binding and type.
const (
	ConvertToFileExecutorID = "core.convertToFile"
	ConvertToFileNodeType   = "kilasflow.convertToFile"
)

// The Convert to File node's operations, as n8n names them.
const (
	ConvertToFileCSV      = "csv"
	ConvertToFileXLSX     = "xlsx"
	ConvertToFileJSON     = "toJson"
	ConvertToFileText     = "toText"
	ConvertToFileBinary   = "toBinary"
	convertToFileJSONOnce = "once"
	convertToFileJSONEach = "each"
)

// ConvertToFileOptionKeys are the options the node implements. The importer
// refuses a node that sets any other.
var ConvertToFileOptionKeys = []string{
	"fileName", "delimiter", "headerRow", "sheetName", "format", "encoding", "addBOM", "mimeType",
}

// convertToFileMaxSheetName is the longest name a worksheet may have.
const convertToFileMaxSheetName = 31

func convertToFileNode() node.Definition {
	spreadsheet := []node.VisibilityCondition{
		{Key: "operation", Equals: ConvertToFileCSV}, {Key: "operation", Equals: ConvertToFileXLSX},
	}
	sourceOps := []node.VisibilityCondition{
		{Key: "operation", Equals: ConvertToFileText}, {Key: "operation", Equals: ConvertToFileBinary},
	}
	return node.Definition{
		Type:        ConvertToFileNodeType,
		Version:     workflow.V(1),
		DisplayName: "Convert to File",
		Description: "Turns items into a file in binary data: CSV, XLSX, JSON, a text file, or a file decoded from a base64 string.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:file-down"},
		IconColor:   "#16a34a",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		// CSV, XLSX and JSON (all items) fold the whole batch into one file, so
		// the node is batch-shaped: under continue-on-fail it must not be split
		// per item.
		WholeBatch: true,
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: ConvertToFileCSV,
				Options: []node.PropertyOption{
					{Label: "Convert to CSV", Value: ConvertToFileCSV},
					{Label: "Convert to XLSX", Value: ConvertToFileXLSX},
					{Label: "Convert to JSON", Value: ConvertToFileJSON},
					{Label: "Convert to Text File", Value: ConvertToFileText},
					{Label: "Move Base64 String to File", Value: ConvertToFileBinary},
				},
				Description: "CSV and XLSX write one file with a row per item. JSON writes the items' data. Text file and Base64 write one file per item from one field.",
			},
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: convertToFileJSONOnce,
				Options: []node.PropertyOption{
					{Label: "All Items to One File", Value: convertToFileJSONOnce},
					{Label: "Each Item to Separate File", Value: convertToFileJSONEach},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileJSON}},
			},
			{
				Key: "sourceProperty", Label: "Source Property", Kind: node.PropertyString, VisibleWhen: sourceOps,
				Description: "The field that holds the text, or the base64 string. Dot notation reaches a nested field.",
			},
			{
				Key: "binaryPropertyName", Label: "Put Output File in Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the file is written to.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "fileName", Label: "File Name", Kind: node.PropertyString,
						Description: "The file's name. Empty names it file.csv, file.xlsx, file.json, file.txt or file."},
					{Key: "delimiter", Label: "Delimiter", Kind: node.PropertyString, Default: ",", VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileCSV}},
						Description: "The character that separates the columns."},
					{Key: "headerRow", Label: "Header Row", Kind: node.PropertyBoolean, Default: true, VisibleWhen: spreadsheet,
						Description: "Write the field names as the first row."},
					{Key: "sheetName", Label: "Sheet Name", Kind: node.PropertyString, Default: "Sheet", VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileXLSX}},
						Description: "The worksheet's name, up to 31 characters and without []:*?/\\."},
					{Key: "format", Label: "Format", Kind: node.PropertyBoolean, Default: false, VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileJSON}},
						Description: "Indent the JSON so it reads easily."},
					{Key: "encoding", Label: "Encoding", Kind: node.PropertyOptions, Default: "utf8",
						Options:     []node.PropertyOption{{Label: "UTF-8", Value: "utf8"}},
						Description: "Text files are written as UTF-8."},
					{Key: "addBOM", Label: "Add BOM", Kind: node.PropertyBoolean, Default: false,
						Description: "Start a text file with a byte order mark, which some spreadsheet programs need to read UTF-8."},
					{Key: "mimeType", Label: "MIME Type", Kind: node.PropertyString, VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileBinary}},
						Description: "The file's media type. Empty detects it from the bytes."},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ConvertToFileExecutorID,
		Validate:       validateConvertToFileConfiguration,
	}
}

func validateConvertToFileConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["operation"]) {
		return nil
	}
	switch operation := textValue(n.Parameters["operation"], ConvertToFileCSV); operation {
	case ConvertToFileCSV, ConvertToFileXLSX, ConvertToFileJSON, ConvertToFileText, ConvertToFileBinary:
	default:
		return fmt.Errorf("operation must be csv, xlsx, toJson, toText or toBinary, not %q", operation)
	}
	known := map[string]bool{}
	for _, key := range ConvertToFileOptionKeys {
		known[key] = true
	}
	options, _ := n.Parameters["options"].(map[string]any)
	for key := range options {
		if !known[key] {
			return fmt.Errorf("option %q is not supported", key)
		}
	}
	if encoding := textValue(options["encoding"], "utf8"); !expression.IsExpression(options["encoding"]) && strings.ToLower(strings.ReplaceAll(encoding, "-", "")) != "utf8" {
		return fmt.Errorf("encoding %q is not supported: files are written as UTF-8", encoding)
	}
	return nil
}

func executeConvertToFile(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
	}
	first, err := expression.Resolve(ir.Parameters, expressionContext(items[0], input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	operation := textValue(first["operation"], ConvertToFileCSV)
	property := cryptoText(first["binaryPropertyName"], "data")

	// One file from the whole batch.
	if operation == ConvertToFileCSV || operation == ConvertToFileXLSX ||
		(operation == ConvertToFileJSON && textValue(first["mode"], convertToFileJSONOnce) != convertToFileJSONEach) {
		file, err := convertBatchToFile(operation, first, items)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		reference, err := request.Binaries.Put(file.name, file.mediaType, bytes.NewReader(file.contents))
		if err != nil {
			return nil, fmt.Errorf("node %q: store %s: %w", ir.Name, file.name, err)
		}
		return workflow.NodeOutput{[]workflow.Item{{
			JSON:   map[string]any{},
			Binary: map[string]workflow.BinaryRef{property: reference},
		}}}, nil
	}

	// One file per item.
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters := first
		if index > 0 {
			if parameters, err = expression.Resolve(ir.Parameters, expressionContext(item, input, request, index)); err != nil {
				return nil, fmt.Errorf("node %q: %w", ir.Name, err)
			}
		}
		file, err := convertItemToFile(operation, parameters, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		reference, err := request.Binaries.Put(file.name, file.mediaType, bytes.NewReader(file.contents))
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: store %s: %w", ir.Name, index+1, file.name, err)
		}
		built := cloneItem(item)
		if built.JSON == nil {
			built.JSON = map[string]any{}
		}
		if built.Binary == nil {
			built.Binary = map[string]workflow.BinaryRef{}
		}
		built.Binary[property] = reference
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

func fileNameOption(options map[string]any, fallback string) string {
	return optionText(options, "fileName", fallback)
}

// withBOM puts a byte order mark in front of UTF-8 text when asked.
func withBOM(contents []byte, options map[string]any) []byte {
	if !optionBool(options, "addBOM", false) {
		return contents
	}
	return append([]byte{0xEF, 0xBB, 0xBF}, contents...)
}

func convertBatchToFile(operation string, parameters map[string]any, items []workflow.Item) (compressionFile, error) {
	options := mapValue(parameters["options"])
	switch operation {
	case ConvertToFileCSV:
		contents, err := tableToCSV(items, options)
		if err != nil {
			return compressionFile{}, err
		}
		return compressionFile{name: fileNameOption(options, "file.csv"), mediaType: "text/csv", contents: withBOM(contents, options)}, nil
	case ConvertToFileXLSX:
		contents, err := tableToXLSX(items, options)
		if err != nil {
			return compressionFile{}, err
		}
		return compressionFile{
			name:      fileNameOption(options, "file.xlsx"),
			mediaType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", contents: contents,
		}, nil
	default:
		documents := make([]any, 0, len(items))
		for _, item := range items {
			documents = append(documents, item.JSON)
		}
		contents, err := encodeJSONFile(documents, options)
		if err != nil {
			return compressionFile{}, err
		}
		return compressionFile{name: fileNameOption(options, "file.json"), mediaType: "application/json", contents: withBOM(contents, options)}, nil
	}
}

func convertItemToFile(operation string, parameters map[string]any, item workflow.Item) (compressionFile, error) {
	options := mapValue(parameters["options"])
	switch operation {
	case ConvertToFileJSON:
		contents, err := encodeJSONFile(item.JSON, options)
		if err != nil {
			return compressionFile{}, err
		}
		return compressionFile{name: fileNameOption(options, "file.json"), mediaType: "application/json", contents: withBOM(contents, options)}, nil
	case ConvertToFileText:
		text, err := sourceString(parameters, item)
		if err != nil {
			return compressionFile{}, err
		}
		return compressionFile{name: fileNameOption(options, "file.txt"), mediaType: "text/plain", contents: withBOM([]byte(text), options)}, nil
	case ConvertToFileBinary:
		text, err := sourceString(parameters, item)
		if err != nil {
			return compressionFile{}, err
		}
		contents, err := decodeBase64Text(text)
		if err != nil {
			return compressionFile{}, err
		}
		media := optionText(options, "mimeType", "")
		if media == "" {
			media = http.DetectContentType(contents)
		}
		return compressionFile{name: fileNameOption(options, "file"), mediaType: media, contents: contents}, nil
	default:
		return compressionFile{}, fmt.Errorf("operation must be csv, xlsx, toJson, toText or toBinary, not %q", operation)
	}
}

func sourceString(parameters map[string]any, item workflow.Item) (string, error) {
	property := strings.TrimSpace(textValue(parameters["sourceProperty"], ""))
	if property == "" {
		return "", fmt.Errorf("a source property is required")
	}
	value, found := lookupPath(item.JSON, property)
	if !found {
		return "", fmt.Errorf("the item has no field %q", property)
	}
	text, isText := value.(string)
	if !isText {
		return "", fmt.Errorf("field %q is not text", property)
	}
	return text, nil
}

// decodeBase64Text reads a base64 string, with or without padding and line
// breaks, and with or without the data: URL prefix a browser would add.
func decodeBase64Text(text string) ([]byte, error) {
	if strings.HasPrefix(text, "data:") {
		if comma := strings.Index(text, ","); comma >= 0 {
			text = text[comma+1:]
		}
	}
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, text)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(cleaned); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("the source field is not valid base64")
}

func encodeJSONFile(value any, options map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if optionBool(options, "format", false) {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("the items could not be written as JSON: %w", err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// tableColumns is the union of the fields the items have, in alphabetical
// order because an item does not keep the order its fields were written in.
func tableColumns(items []workflow.Item) []string {
	seen := map[string]bool{}
	for _, item := range items {
		for key := range item.JSON {
			seen[key] = true
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

func tableToCSV(items []workflow.Item, options map[string]any) ([]byte, error) {
	delimiter := optionText(options, "delimiter", ",")
	separator, size := utf8.DecodeRuneInString(delimiter)
	if size != len(delimiter) || separator == utf8.RuneError || separator == '"' || separator == '\r' || separator == '\n' {
		return nil, fmt.Errorf("the delimiter must be one character, and not a quote or a line break")
	}
	columns := tableColumns(items)
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.Comma = separator
	if optionBool(options, "headerRow", true) {
		if err := writer.Write(columns); err != nil {
			return nil, err
		}
	}
	for _, item := range items {
		row := make([]string, len(columns))
		for index, column := range columns {
			row[index] = htmlCellText(item.JSON[column])
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// --- XLSX ----------------------------------------------------------------------

// xlsxColumnName turns a zero-based column number into its letters: A, B, ...
// Z, AA, AB.
func xlsxColumnName(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

var xlsxEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// xlsxText removes the characters XML 1.0 cannot carry before escaping.
func xlsxText(text string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF) {
			return r
		}
		return -1
	}, text)
	return xlsxEscaper.Replace(cleaned)
}

func validSheetName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > convertToFileMaxSheetName || strings.ContainsAny(name, `[]:*?/\`) ||
		strings.HasPrefix(name, "'") || strings.HasSuffix(name, "'") {
		return fmt.Errorf("the sheet name %q is not valid: use 1 to %d characters, without []:*?/\\ and without a quote at either end", name, convertToFileMaxSheetName)
	}
	return nil
}

// tableToXLSX writes a workbook of one worksheet. It is the smallest package a
// spreadsheet program opens: the content types, the relationships, the
// workbook and the sheet, with text kept inline and numbers and booleans typed.
func tableToXLSX(items []workflow.Item, options map[string]any) ([]byte, error) {
	sheetName := optionText(options, "sheetName", "Sheet")
	if err := validSheetName(sheetName); err != nil {
		return nil, err
	}
	columns := tableColumns(items)
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	rowNumber := 0
	writeRow := func(cells []string) {
		rowNumber++
		fmt.Fprintf(&sheet, `<row r="%d">`, rowNumber)
		for _, cell := range cells {
			sheet.WriteString(cell)
		}
		sheet.WriteString(`</row>`)
	}
	reference := func(column int) string { return xlsxColumnName(column) + strconv.Itoa(rowNumber+1) }
	if optionBool(options, "headerRow", true) && len(columns) > 0 {
		cells := make([]string, len(columns))
		for index, column := range columns {
			cells[index] = fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, reference(index), xlsxText(column))
		}
		writeRow(cells)
	}
	for _, item := range items {
		cells := make([]string, len(columns))
		for index, column := range columns {
			cells[index] = xlsxCell(reference(index), item.JSON[column])
		}
		writeRow(cells)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var packed bytes.Buffer
	archive := zip.NewWriter(&packed)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="` + xlsxText(sheetName) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{"xl/worksheets/sheet1.xml", sheet.String()},
	}
	for _, part := range parts {
		entry, err := archive.Create(part.name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(part.body)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return packed.Bytes(), nil
}

func xlsxCell(reference string, value any) string {
	switch typed := value.(type) {
	case nil:
		return fmt.Sprintf(`<c r="%s"/>`, reference)
	case bool:
		flag := "0"
		if typed {
			flag = "1"
		}
		return fmt.Sprintf(`<c r="%s" t="b"><v>%s</v></c>`, reference, flag)
	case float64:
		return fmt.Sprintf(`<c r="%s"><v>%s</v></c>`, reference, strconv.FormatFloat(typed, 'f', -1, 64))
	case int:
		return fmt.Sprintf(`<c r="%s"><v>%d</v></c>`, reference, typed)
	default:
		return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, reference, xlsxText(htmlCellText(typed)))
	}
}
