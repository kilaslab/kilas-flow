package nodes

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/ledongthuc/pdf"
)

const (
	ExtractFromFileExecutorID = "core.extractFromFile"
	ExtractFromFileNodeType   = "kilasflow.extractFromFile"

	ExtractOperationPDF      = "pdf"
	ExtractOperationText     = "text"
	ExtractOperationJSON     = "json"
	ExtractOperationCSV      = "csv"
	ExtractOperationXLSX     = "xlsx"
	ExtractOperationToBinary = "binaryToProperty"
)

func extractFromFileNode() node.Definition {
	return node.Definition{
		Type:        ExtractFromFileNodeType,
		Version:     workflow.V(1),
		DisplayName: "Extract From File",
		Description: "Extracts text from a PDF with a text layer, a UTF-8 text file, or JSON. Scanned PDFs need OCR, which this node does not run.",
		Category:    "Core",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:archive"},
		IconColor:   "#f97316",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true,
				Default: ExtractOperationPDF,
				Options: []node.PropertyOption{
					{Label: "Extract From PDF", Value: ExtractOperationPDF},
					{Label: "Extract From Text File", Value: ExtractOperationText},
					{Label: "Extract From JSON File", Value: ExtractOperationJSON},
					{Label: "Extract From CSV", Value: ExtractOperationCSV},
					{Label: "Extract From XLSX", Value: ExtractOperationXLSX},
					{Label: "Binary to JSON Property", Value: ExtractOperationToBinary},
				},
				Description: "PDF extraction reads the text layer only. Image-only scans yield empty text; this node does not OCR. CSV and XLSX produce one item per row with the columns as fields.",
			},
			{
				Key: "binaryPropertyName", Label: "Input binary field", Kind: node.PropertyString, Default: "data",
				Description: "The binary property on the incoming item that holds the file.",
			},
			{
				Key: "destinationKey", Label: "Destination key", Kind: node.PropertyString, Default: "text",
				Description: "JSON field the extracted text is written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     ExtractFromFileExecutorID,
		Validate:       validateExtractFromFile,
	}
}

func validateExtractFromFile(n workflow.Node) error {
	operation := strings.ToLower(strings.TrimSpace(textValue(n.Parameters["operation"], ExtractOperationPDF)))
	switch operation {
	case ExtractOperationPDF, ExtractOperationText, ExtractOperationJSON, ExtractOperationCSV, ExtractOperationXLSX, strings.ToLower(ExtractOperationToBinary),
		"extractfrompdf", "extractfromfile", "extractfromjson":
		return nil
	default:
		return fmt.Errorf("operation must be pdf, text, json, csv, xlsx or binaryToProperty")
	}
}

func executeExtractFromFile(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
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
	operation := extractOperation(ir.Parameters["operation"])
	propertyName := strings.TrimSpace(textValue(ir.Parameters["binaryPropertyName"], "data"))
	if propertyName == "" {
		propertyName = "data"
	}
	destination := strings.TrimSpace(textValue(ir.Parameters["destinationKey"], "text"))
	if destination == "" {
		destination = "text"
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		ref, found := firstBinary(item, propertyName)
		if !found {
			return nil, fmt.Errorf("node %q: item %d has no binary property %q", ir.Name, index+1, propertyName)
		}
		body, meta, err := request.Binaries.Get(ref.ID)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(body, 32<<20))
		_ = body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("node %q: item %d could not be read: %w", ir.Name, index+1, readErr)
		}
		extracted, err := extractBytes(operation, payload, meta.MediaType)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		// CSV and XLSX read tables, and n8n emits one item per row with the
		// columns as the item's fields — not a single item holding an array of
		// rows, which would make every downstream field reference wrong.
		if rows, ok := extracted.([]any); ok && (operation == ExtractOperationCSV || operation == ExtractOperationXLSX) {
			for _, row := range rows {
				fields, ok := row.(map[string]any)
				if !ok {
					continue
				}
				output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
			}
			continue
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		fields[destination] = extracted
		output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

func extractOperation(raw any) string {
	value := strings.ToLower(strings.TrimSpace(textValue(raw, ExtractOperationPDF)))
	switch value {
	case "extractfrompdf", ExtractOperationPDF, "application/pdf":
		return ExtractOperationPDF
	case "extractfromjson", ExtractOperationJSON:
		return ExtractOperationJSON
	case "extractfromfile", ExtractOperationText, "text/plain":
		return ExtractOperationText
	case ExtractOperationCSV:
		return ExtractOperationCSV
	case ExtractOperationXLSX:
		return ExtractOperationXLSX
	case strings.ToLower(ExtractOperationToBinary), "binarytopropery":
		// n8n spells it binaryToPropery (no r); both spellings land here.
		return ExtractOperationToBinary
	default:
		return value
	}
}

func firstBinary(item workflow.Item, name string) (workflow.BinaryRef, bool) {
	if item.Binary == nil {
		return workflow.BinaryRef{}, false
	}
	if ref, found := item.Binary[name]; found && ref.ID != "" {
		return ref, true
	}
	for _, ref := range item.Binary {
		if ref.ID != "" {
			return ref, true
		}
	}
	return workflow.BinaryRef{}, false
}

func extractBytes(operation string, payload []byte, mediaType string) (any, error) {
	switch operation {
	case ExtractOperationCSV:
		return extractCSVRows(payload)
	case ExtractOperationXLSX:
		return extractXLSXRows(payload)
	case ExtractOperationToBinary:
		return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString(payload), "mimeType": mediaType}, nil
	}
	if operation == ExtractOperationJSON || strings.Contains(strings.ToLower(mediaType), "json") && operation != ExtractOperationPDF && operation != ExtractOperationText {
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return nil, fmt.Errorf("the file is not valid JSON: %w", err)
		}
		return decoded, nil
	}
	if operation == ExtractOperationPDF || strings.Contains(strings.ToLower(mediaType), "pdf") {
		text, err := extractPDFText(payload)
		if err != nil {
			return nil, err
		}
		return text, nil
	}
	if !utf8.Valid(payload) {
		return nil, fmt.Errorf("the file is not valid UTF-8 text")
	}
	return string(payload), nil
}

func extractPDFText(payload []byte) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return "", fmt.Errorf("the PDF could not be opened (text-layer PDFs only; scanned pages need OCR, which this node does not run): %w", err)
	}
	var builder strings.Builder
	for page := 1; page <= reader.NumPage(); page++ {
		p := reader.Page(page)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("page %d has no readable text layer: %w", page, err)
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(text)
	}
	return strings.TrimSpace(builder.String()), nil
}

// extractCSVRows reads a CSV file the way n8n's CSV operation does: the first
// row names the columns, every row after becomes one item with the column
// names as its fields. A ragged row pads with empty strings rather than
// failing the whole file, because a trailing separator on the header line is
// common in exported data.
func extractCSVRows(payload []byte) (any, error) {
	reader := csv.NewReader(bytes.NewReader(payload))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("the file is not valid CSV: %w", err)
	}
	if len(rows) == 0 {
		return []any{}, nil
	}
	headers := rows[0]
	items := make([]any, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := make(map[string]any, len(headers))
		for index, header := range headers {
			value := ""
			if index < len(row) {
				value = row[index]
			}
			record[strings.TrimSpace(header)] = value
		}
		items = append(items, record)
	}
	return items, nil
}

// extractXLSXRows reads the first worksheet of an .xlsx file into the same
// one-item-per-row shape as CSV. The format is a zip of XML parts; only the
// shared strings and the first sheet are read, which is all a data extract
// needs.
func extractXLSXRows(payload []byte) (any, error) {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("the file is not a valid XLSX workbook: %w", err)
	}
	shared, err := xlsxSharedStrings(archive)
	if err != nil {
		return nil, err
	}
	sheet, err := xlsxFirstSheet(archive)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(sheet))
	var rows [][]string
	var current []string
	var cellText strings.Builder
	inCell := false
	cellHasShared := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the workbook's first sheet is not readable: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "row":
				current = nil
			case "c":
				inCell = true
				cellText.Reset()
				cellHasShared = false
				for _, attribute := range element.Attr {
					if attribute.Name.Local == "t" && attribute.Value == "s" {
						cellHasShared = true
					}
				}
			case "v", "t":
				if inCell {
					cellText.Reset()
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "c":
				inCell = false
				value := strings.TrimSpace(cellText.String())
				if cellHasShared {
					if index, err := strconv.Atoi(value); err == nil && index < len(shared) {
						value = shared[index]
					}
				}
				current = append(current, value)
			case "row":
				rows = append(rows, current)
			}
		case xml.CharData:
			if inCell {
				cellText.Write(element)
			}
		}
	}
	if len(rows) == 0 {
		return []any{}, nil
	}
	headers := rows[0]
	items := make([]any, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := make(map[string]any, len(headers))
		for index, header := range headers {
			value := ""
			if index < len(row) {
				value = row[index]
			}
			record[strings.TrimSpace(header)] = value
		}
		items = append(items, record)
	}
	return items, nil
}

// xlsxSharedStrings reads the workbook's shared string table, which is where
// text cells are stored.
func xlsxSharedStrings(archive *zip.Reader) ([]string, error) {
	file, found := findZipFile(archive, "xl/sharedStrings.xml")
	if !found {
		return nil, nil
	}
	data, err := readZipFile(file)
	if err != nil {
		return nil, fmt.Errorf("the workbook's shared strings are not readable: %w", err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var table []string
	var builder strings.Builder
	inside := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the workbook's shared strings are not readable: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == "si" {
				inside = true
				builder.Reset()
			}
		case xml.EndElement:
			if element.Name.Local == "si" {
				inside = false
				table = append(table, builder.String())
			}
		case xml.CharData:
			if inside {
				builder.Write(element)
			}
		}
	}
	return table, nil
}

// xlsxFirstSheet finds the first worksheet part. The workbook's own manifest
// maps sheet names to parts, and sheet1.xml is the first entry in every
// producer this import has to read; falling back to any xl/worksheets part
// covers the rest.
func xlsxFirstSheet(archive *zip.Reader) ([]byte, error) {
	if file, found := findZipFile(archive, "xl/worksheets/sheet1.xml"); found {
		return readZipFile(file)
	}
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "xl/worksheets/") && strings.HasSuffix(file.Name, ".xml") {
			return readZipFile(file)
		}
	}
	return nil, fmt.Errorf("the workbook has no worksheet")
}

func findZipFile(archive *zip.Reader, name string) (*zip.File, bool) {
	for _, file := range archive.File {
		if file.Name == name {
			return file, true
		}
	}
	return nil, false
}

func readZipFile(file *zip.File) ([]byte, error) {
	handle, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return io.ReadAll(handle)
}
