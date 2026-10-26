package nodes

// The file & format utility family (FEAT-re138f): Convert to File and
// Compression, the two halves that touch the binary store.
//
// Convert to File is the write half of Extract From File: items (or one
// property of them) serialised into a CSV, an XLSX workbook, JSON, plain text
// or a base64 payload, stored as binary and referenced from the output item.
// The parameter keys are n8n's own (`operation`, `mode`, `propertyName`,
// `binaryPropertyName`, `options.fileName`), so an imported node maps by type
// name alone, and the legacy `moveBinaryData` node's base64 direction imports
// onto the base64 operation.
//
// Compression reads one binary and writes it back squeezed — gzip and zip, the
// two formats workflows actually carry, plus the two raw deflate spellings
// because the Go standard library already speaks them. n8n's keys again
// (`operation`, `compressionFormat`, `binaryPropertyName`, `outputPrefix`).
//
// Both are pure Go with no outbound traffic and no credential: everything they
// read arrives on the item, and everything they write lands in this
// execution's binary store.
import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	ConvertToFileExecutorID = "core.convertToFile"
	CompressionExecutorID   = "core.compression"

	ConvertToFileNodeType = "kilasflow.convertToFile"
	CompressionNodeType   = "kilasflow.compression"
)

// The Convert to File operations, under n8n's values. n8n also writes iCal,
// ODS, RTF, XLS and HTML files; the importer refuses those by name so a
// workflow never runs a step that silently produced nothing.
const (
	ConvertToFileCSV    = "csv"
	ConvertToFileXLSX   = "xlsx"
	ConvertToFileJSON   = "json"
	ConvertToFileText   = "text"
	ConvertToFileBase64 = "base64"
)

// The Compression formats, under n8n's values.
const (
	CompressionGzip       = "gzip"
	CompressionZip        = "zip"
	CompressionDeflate    = "deflate"
	CompressionDeflateRaw = "deflateRaw"
)

// maxFileFormatBytes bounds one binary these nodes will read into memory — the
// same budget Extract From File reads to, so the two halves of the family
// refuse at the same size rather than at sizes that differ by an accident of
// implementation.
const maxFileFormatBytes = 32 << 20

func convertToFileNode() node.Definition {
	return node.Definition{
		Type: ConvertToFileNodeType,
		// WholeBatch: the all-items operations build one file from every
		// item, and being split into one-item calls under continue-on-fail
		// would write a plausible file per item where the user asked for one.
		WholeBatch:  true,
		Version:     workflow.V(1),
		DisplayName: "Convert to File",
		Description: "Turns items into a file: a CSV or XLSX table of every item, the items as a JSON document, a text or a base64 payload, stored as binary data.",
		Category:    "Files",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "convertToFile"},
		IconLight:   fileFormatArtwork("convert"),
		IconColor:   "#f97316",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: ConvertToFileCSV,
				Options: []node.PropertyOption{
					{Label: "Convert to CSV", Value: ConvertToFileCSV},
					{Label: "Convert to XLSX", Value: ConvertToFileXLSX},
					{Label: "Convert to JSON", Value: ConvertToFileJSON},
					{Label: "Convert to Text", Value: ConvertToFileText},
					{Label: "Convert from Base64", Value: ConvertToFileBase64},
				},
				Description: "The file to build. The base64 operation is the destination of n8n's legacy Move Binary Data node.",
			},
			{
				Key: "mode", Label: "Mode", Kind: node.PropertyOptions, Default: "all",
				Options: []node.PropertyOption{
					{Label: "All Items", Value: "all"},
					{Label: "Each Item's Property", Value: "manual"},
				},
				Description: "All items builds one file from every incoming item. Each item reads the property below, one file per item.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: ConvertToFileCSV},
					{Key: "operation", Equals: ConvertToFileXLSX}, {Key: "operation", Equals: ConvertToFileJSON},
					{Key: "operation", Equals: ConvertToFileText}},
			},
			{
				Key: "propertyName", Label: "Property Name", Kind: node.PropertyString, Default: "data",
				Description: "The field read in Each Item mode: the rows array for CSV/XLSX, the value serialised for JSON, the text for Text, the base64 string for Base64.",
			},
			{
				Key: "binaryPropertyName", Label: "Binary Property", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the file is written to on the output item.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "fileName", Label: "File Name", Kind: node.PropertyString, Description: "The file's name. Defaults to data with the operation's extension."},
					{Key: "headerRow", Label: "Header Row", Kind: node.PropertyBoolean, Default: true, Description: "Write the column names as the first row of a CSV or XLSX file."},
					{
						Key: "delimiter", Label: "CSV Delimiter", Kind: node.PropertyOptions, Default: ",",
						Options: []node.PropertyOption{
							{Label: "Comma", Value: ","}, {Label: "Semicolon", Value: ";"},
							{Label: "Tab", Value: "\t"}, {Label: "Pipe", Value: "|"},
						},
						Description: "The column separator written into a CSV file.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ConvertToFileExecutorID,
		Validate:       validateConvertToFile,
	}
}

func validateConvertToFile(n workflow.Node) error {
	switch operation := statementText(n.Parameters, "operation"); operation {
	case "", ConvertToFileCSV, ConvertToFileXLSX, ConvertToFileJSON, ConvertToFileText, ConvertToFileBase64:
	default:
		return fmt.Errorf("operation %q is not one this node runs; it writes csv, xlsx, json, text or base64", operation)
	}
	switch encoding := statementText(mapValue(n.Parameters["options"]), "encoding"); encoding {
	case "", "UTF-8", "utf-8", "utf8":
	default:
		return fmt.Errorf("encoding %q is not supported; this node writes UTF-8", encoding)
	}
	return nil
}

func executeConvertToFile(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	// The whole-file operations read every item, so their parameters resolve
	// once, against the first item's context — a per-item resolution of an
	// "all items" setting would be four files where the user asked for one.
	parameters, err := expression.Resolve(ir.Parameters, expressionContext(items[0], input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	operation := textValue(parameters["operation"], ConvertToFileCSV)
	outputKey := strings.TrimSpace(textValue(parameters["binaryPropertyName"], "data"))
	if outputKey == "" {
		outputKey = "data"
	}
	options := mapValue(parameters["options"])
	switch operation {
	case ConvertToFileCSV, ConvertToFileXLSX:
		rows, headers, err := convertTableRows(operation, items, parameters)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		var body []byte
		var extension, media string
		if operation == ConvertToFileCSV {
			body, err = writeCSV(rows, headers, csvDelimiter(options), options["headerRow"] != false)
			extension, media = "csv", "text/csv"
		} else {
			body, err = writeXLSX(rows, headers, options["headerRow"] != false)
			extension, media = "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		item, err := fileOutputItem(request, outputKey, body, fileName(options, "data."+extension), media, extension)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		return workflow.NodeOutput{[]workflow.Item{item}}, nil

	case ConvertToFileJSON:
		if textValue(parameters["mode"], "all") == "manual" {
			return convertPerItem(request, ir, items, parameters, outputKey, func(value any) ([]byte, string, string, error) {
				body, err := json.MarshalIndent(value, "", "  ")
				if err != nil {
					return nil, "", "", fmt.Errorf("the property is not JSON-serialisable: %w", err)
				}
				return body, "json", "application/json", nil
			})
		}
		payload := make([]any, 0, len(items))
		for _, item := range items {
			payload = append(payload, item.JSON)
		}
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("node %q: the items are not JSON-serialisable: %w", ir.Name, err)
		}
		item, err := fileOutputItem(request, outputKey, body, fileName(options, "data.json"), "application/json", "json")
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		return workflow.NodeOutput{[]workflow.Item{item}}, nil

	case ConvertToFileText:
		if textValue(parameters["mode"], "all") == "manual" {
			return convertPerItem(request, ir, items, parameters, outputKey, func(value any) ([]byte, string, string, error) {
				return []byte(textValue(value, fmt.Sprint(value))), "txt", "text/plain", nil
			})
		}
		lines := make([]string, 0, len(items))
		for _, item := range items {
			encoded, err := json.Marshal(item.JSON)
			if err != nil {
				return nil, fmt.Errorf("node %q: an item is not JSON-serialisable: %w", ir.Name, err)
			}
			lines = append(lines, string(encoded))
		}
		body := []byte(strings.Join(lines, "\n"))
		item, err := fileOutputItem(request, outputKey, body, fileName(options, "data.txt"), "text/plain", "txt")
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		return workflow.NodeOutput{[]workflow.Item{item}}, nil

	case ConvertToFileBase64:
		return convertPerItem(request, ir, items, parameters, outputKey, func(value any) ([]byte, string, string, error) {
			text := strings.TrimSpace(textValue(value, ""))
			if text == "" {
				return nil, "", "", fmt.Errorf("the property holds no base64 text")
			}
			decoded := strings.TrimRight(text, "=")
			if len(decoded)%4 == 1 {
				return nil, "", "", fmt.Errorf("the property is not valid base64")
			}
			body, err := base64.StdEncoding.WithPadding(base64.NoPadding).DecodeString(decoded)
			if err != nil {
				return nil, "", "", fmt.Errorf("the property is not valid base64: %w", err)
			}
			return body, "bin", "application/octet-stream", nil
		})

	default:
		return nil, fmt.Errorf("node %q: operation %q has no writer here", ir.Name, operation)
	}
}

// convertPerItem runs the per-item operations: one resolved property, one
// produced file, one output item carrying the original JSON beside the
// reference.
func convertPerItem(request engine.Request, ir workflow.IRNode, items []workflow.Item, parameters map[string]any, outputKey string, build func(value any) (body []byte, extension, mediaType string, err error)) (workflow.NodeOutput, error) {
	options := mapValue(parameters["options"])
	propertyName := strings.TrimSpace(textValue(parameters["propertyName"], "data"))
	if propertyName == "" {
		propertyName = "data"
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		value, present := item.JSON[propertyName]
		if !present {
			return nil, fmt.Errorf("node %q: item %d has no property %q", ir.Name, index+1, propertyName)
		}
		body, extension, media, err := build(value)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		produced, err := fileOutputItem(request, outputKey, body, fileName(options, "data."+extension), media, extension)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		for key, carried := range item.JSON {
			if key == "fileName" || key == "fileExtension" || key == "mimeType" || key == "fileSize" {
				continue
			}
			produced.JSON[key] = carried
		}
		output = append(output, produced)
	}
	return workflow.NodeOutput{output}, nil
}

// fileOutputItem stores body and returns the item that references it, in the
// shape n8n's node writes: the file's name, extension, media type and size.
func fileOutputItem(request engine.Request, key string, body []byte, name, mediaType, extension string) (workflow.Item, error) {
	reference, err := request.Binaries.Put(name, mediaType, bytes.NewReader(body))
	if err != nil {
		return workflow.Item{}, err
	}
	return workflow.Item{
		JSON: map[string]any{
			"fileName":      name,
			"fileExtension": extension,
			"mimeType":      mediaType,
			"fileSize":      float64(len(body)),
		},
		Binary: map[string]workflow.BinaryRef{key: reference},
	}, nil
}

// convertTableRows gathers the rows a CSV or XLSX file carries. All-items mode
// writes one row per item with the union of the items' keys as columns;
// each-item mode reads a list of objects from the named property. Columns are
// alphabetical, which is the only order a Go map can hand out honestly: the
// JSON an item arrived as has no key order to preserve.
func convertTableRows(operation string, items []workflow.Item, parameters map[string]any) ([][]string, []string, error) {
	var records []map[string]any
	if textValue(parameters["mode"], "all") != "manual" {
		for _, item := range items {
			records = append(records, item.JSON)
		}
	} else {
		propertyName := strings.TrimSpace(textValue(parameters["propertyName"], "data"))
		if propertyName == "" {
			propertyName = "data"
		}
		value, _, err := firstTableProperty(items, propertyName)
		if err != nil {
			return nil, nil, err
		}
		list, ok := value.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("property %q is not a list of rows", propertyName)
		}
		for _, entry := range list {
			if record, ok := entry.(map[string]any); ok {
				records = append(records, record)
				continue
			}
			records = append(records, map[string]any{propertyName: entry})
		}
	}
	headers := tableHeaders(records)
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		row := make([]string, 0, len(headers))
		for _, header := range headers {
			row = append(row, cellText(record[header]))
		}
		rows = append(rows, row)
	}
	return rows, headers, nil
}

// tableHeaders is the sorted union of every record's keys, so a column that
// only item three carries is a column the file carries.
func tableHeaders(records []map[string]any) []string {
	union := map[string]bool{}
	for _, record := range records {
		for key := range record {
			union[key] = true
		}
	}
	headers := make([]string, 0, len(union))
	for key := range union {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	return headers
}

// firstTableProperty resolves the named property against the first item that
// carries it, so a manual-mode table whose rows arrive on item three still
// reads them.
func firstTableProperty(items []workflow.Item, name string) (any, int, error) {
	for index, item := range items {
		if value, present := item.JSON[name]; present {
			return value, index, nil
		}
	}
	return nil, 0, fmt.Errorf("no item carries property %q", name)
}

// cellText renders one cell: numbers without float noise, everything else as
// its JSON form when it is structured, plain text when it is not.
func cellText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func csvDelimiter(options map[string]any) rune {
	switch textValue(options["delimiter"], ",") {
	case ";":
		return ';'
	case "\t", "\\t":
		return '\t'
	case "|":
		return '|'
	default:
		return ','
	}
}

func writeCSV(rows [][]string, headers []string, delimiter rune, headerRow bool) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.Comma = delimiter
	if headerRow && len(headers) > 0 {
		if err := writer.Write(headers); err != nil {
			return nil, err
		}
	}
	for _, row := range rows {
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

// writeXLSX builds the smallest workbook every spreadsheet reader opens: the
// container, the manifest, one sheet. It is the write twin of the XLSX reader
// in extract.go, which reads exactly these parts.
func writeXLSX(rows [][]string, headers []string, headerRow bool) ([]byte, error) {
	sheet := xlsxSheetXML(rows, headers, headerRow)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := []struct {
		name string
		body string
	}{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
		{"xl/worksheets/sheet1.xml", sheet},
	}
	for _, file := range files {
		entry, err := archive.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(file.body)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// xlsxSheetXML renders the sheet's rows. Cell references are computed A1-style
// because readers use them to place values; every cell is written as an inline
// string so no shared-string table is needed.
func xlsxSheetXML(rows [][]string, headers []string, headerRow bool) string {
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	write := func(row []string, number int) {
		sheet.WriteString(`<row r="`)
		sheet.WriteString(strconv.Itoa(number))
		sheet.WriteString(`">`)
		for column, value := range row {
			sheet.WriteString(`<c r="`)
			sheet.WriteString(xlsxCellReference(column, number))
			sheet.WriteString(`" t="inlineStr"><is><t xml:space="preserve">`)
			xmlEscapeText(&sheet, value)
			sheet.WriteString(`</t></is></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	rowNumber := 1
	if headerRow && len(headers) > 0 {
		write(headers, rowNumber)
		rowNumber++
	}
	for _, row := range rows {
		write(row, rowNumber)
		rowNumber++
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	return sheet.String()
}

func xlsxCellReference(column, row int) string {
	label := ""
	for column >= 0 {
		label = string(rune('A'+column%26)) + label
		column = column/26 - 1
	}
	return label + strconv.Itoa(row)
}

func xmlEscapeText(builder *strings.Builder, text string) {
	for _, character := range text {
		switch character {
		case '<':
			builder.WriteString("&lt;")
		case '>':
			builder.WriteString("&gt;")
		case '&':
			builder.WriteString("&amp;")
		case '"':
			builder.WriteString("&quot;")
		case '\'':
			builder.WriteString("&apos;")
		default:
			builder.WriteRune(character)
		}
	}
}

func fileName(options map[string]any, fallback string) string {
	if name := strings.TrimSpace(textValue(options["fileName"], "")); name != "" {
		return name
	}
	return fallback
}

// --- Compression ---------------------------------------------------------------

func compressionNode() node.Definition {
	return node.Definition{
		Type:        CompressionNodeType,
		Version:     workflow.V(1),
		DisplayName: "Compress",
		Description: "Compresses or decompresses a binary file: gzip, zip, or raw deflate, with no network involved.",
		Category:    "Files",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:archive"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: "compress",
				Options: []node.PropertyOption{
					{Label: "Compress", Value: "compress"},
					{Label: "Decompress", Value: "decompress"},
				},
			},
			{
				Key: "compressionFormat", Label: "Compression Format", Kind: node.PropertyOptions, Default: CompressionGzip,
				Options: []node.PropertyOption{
					{Label: "Gzip", Value: CompressionGzip},
					{Label: "Zip", Value: CompressionZip},
					{Label: "Deflate", Value: CompressionDeflate},
					{Label: "Deflate Raw", Value: CompressionDeflateRaw},
				},
			},
			{
				Key: "binaryPropertyName", Label: "Input Binary Field", Kind: node.PropertyString, Default: "data",
				Description: "The binary field the file is read from.",
			},
			{
				Key: "outputPrefix", Label: "Output Prefix", Kind: node.PropertyString,
				Description: "The compressed or restored file lands in a binary field named after this prefix (data when empty).",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CompressionExecutorID,
	}
}

func executeCompression(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	parameters := ir.Parameters
	operation := textValue(parameters["operation"], "compress")
	format := textValue(parameters["compressionFormat"], CompressionGzip)
	inputKey := strings.TrimSpace(textValue(parameters["binaryPropertyName"], "data"))
	if inputKey == "" {
		inputKey = "data"
	}
	outputKey := strings.TrimSpace(textValue(parameters["outputPrefix"], ""))
	if outputKey == "" {
		outputKey = "data"
	} else {
		outputKey = strings.TrimSuffix(outputKey, "Data") + "Data"
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		reference, found := firstBinary(item, inputKey)
		if !found {
			return nil, fmt.Errorf("node %q: item %d has no binary property %q", ir.Name, index+1, inputKey)
		}
		body, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(body, maxFileFormatBytes))
		_ = body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("node %q: item %d could not be read: %w", ir.Name, index+1, readErr)
		}
		// The store's Get answers with the payload only — the name and media
		// type travel on the item's own reference — so both come from there.
		produced, name, media, err := compressBytes(operation, format, payload, reference)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		stored, err := request.Binaries.Put(name, media, bytes.NewReader(produced))
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		carried := map[string]workflow.BinaryRef{}
		for key, value := range item.Binary {
			carried[key] = value
		}
		carried[outputKey] = stored
		output = append(output, workflow.Item{JSON: fields, Binary: carried, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

// compressBytes squeezes or restores one payload. The compressed file is named
// after the input with the format's extension; a decompressed file keeps the
// name the archive itself carries, because that is the file the author put in.
func compressBytes(operation, format string, payload []byte, source workflow.BinaryRef) ([]byte, string, string, error) {
	sourceName := source.FileName
	if sourceName == "" {
		sourceName = "data"
	}
	if operation == "decompress" {
		restored, name, err := decompressPayload(format, payload, sourceName)
		if err != nil {
			return nil, "", "", err
		}
		if name == "" {
			name = strings.TrimSuffix(sourceName, "."+format)
		}
		media := source.MediaType
		if media == "" || strings.Contains(media, format) {
			media = "application/octet-stream"
		}
		return restored, name, media, nil
	}
	var buffer bytes.Buffer
	switch format {
	case CompressionGzip:
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(payload); err != nil {
			return nil, "", "", err
		}
		if err := writer.Close(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), sourceName + ".gz", "application/gzip", nil
	case CompressionZip:
		archive := zip.NewWriter(&buffer)
		entry, err := archive.Create(sourceName)
		if err != nil {
			return nil, "", "", err
		}
		if _, err := entry.Write(payload); err != nil {
			return nil, "", "", err
		}
		if err := archive.Close(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), sourceName + ".zip", "application/zip", nil
	case CompressionDeflate, CompressionDeflateRaw:
		writer, err := flate.NewWriter(&buffer, flate.DefaultCompression)
		if err != nil {
			return nil, "", "", err
		}
		if _, err := writer.Write(payload); err != nil {
			return nil, "", "", err
		}
		if err := writer.Close(); err != nil {
			return nil, "", "", err
		}
		// zlib-wrapped on "deflate" (the HTTP Content-Encoding spelling),
		// bare on "deflateRaw" — the two differ by exactly that wrapper.
		if format == CompressionDeflate {
			return append([]byte{0x78, 0x9c}, buffer.Bytes()...), sourceName + ".zz", "application/zlib", nil
		}
		return buffer.Bytes(), sourceName + ".deflate", "application/octet-stream", nil
	default:
		return nil, "", "", fmt.Errorf("compression format %q is not one this node runs", format)
	}
}

// decompressPayload restores one payload. A zip archive holds any number of
// files; this node answers the workflows n8n's does, which zip exactly one,
// and refuses a multi-file archive by naming how many it found rather than
// quietly keeping the first and dropping the rest.
func decompressPayload(format string, payload []byte, sourceName string) ([]byte, string, error) {
	switch format {
	case CompressionGzip:
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, "", fmt.Errorf("the file is not valid gzip: %w", err)
		}
		restored, err := io.ReadAll(io.LimitReader(reader, maxFileFormatBytes))
		if err != nil {
			return nil, "", fmt.Errorf("the file is not valid gzip: %w", err)
		}
		name := strings.TrimSuffix(reader.Header.Name, "")
		if name == "" {
			name = strings.TrimSuffix(sourceName, ".gz")
		}
		return restored, name, nil
	case CompressionZip:
		archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			return nil, "", fmt.Errorf("the file is not a valid zip archive: %w", err)
		}
		files := 0
		var first *zip.File
		for _, file := range archive.File {
			if file.FileInfo().IsDir() {
				continue
			}
			files++
			if first == nil {
				first = file
			}
		}
		if files == 0 {
			return nil, "", fmt.Errorf("the zip archive holds no files")
		}
		if files > 1 {
			return nil, "", fmt.Errorf("the zip archive holds %d files; this node decompresses one", files)
		}
		handle, err := first.Open()
		if err != nil {
			return nil, "", err
		}
		defer handle.Close()
		restored, err := io.ReadAll(io.LimitReader(handle, maxFileFormatBytes))
		if err != nil {
			return nil, "", err
		}
		return restored, first.Name, nil
	case CompressionDeflate:
		if len(payload) < 2 {
			return nil, "", fmt.Errorf("the file is too short to be zlib data")
		}
		return flateRead(bytes.NewReader(payload[2:]))
	case CompressionDeflateRaw:
		return flateRead(bytes.NewReader(payload))
	default:
		return nil, "", fmt.Errorf("compression format %q is not one this node runs", format)
	}
}

func flateRead(reader io.Reader) ([]byte, string, error) {
	flater := flate.NewReader(reader)
	defer flater.Close()
	restored, err := io.ReadAll(io.LimitReader(flater, maxFileFormatBytes))
	if err != nil {
		return nil, "", fmt.Errorf("the file is not valid deflate data: %w", err)
	}
	return restored, "", nil
}

// fileFormatArtwork is the family's own monochrome marks, in the stroke idiom
// the editor's builtin glyphs draw in: a document with a down arrow for the
// conversion, and the archive glyph the editor already ships serves the
// compression node.
func fileFormatArtwork(key string) *node.IconAsset {
	marks := map[string]string{
		"convert": `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M12 18v-6"/><path d="m9 15 3 3 3-3"/>`,
	}
	body, drawn := marks[key]
	if !drawn {
		return nil
	}
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`),
	}
}
