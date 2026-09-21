package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	ExtractOperationPDF  = "pdf"
	ExtractOperationText = "text"
	ExtractOperationJSON = "json"
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
				},
				Description: "PDF extraction reads the text layer only. Image-only scans yield empty text; this node does not OCR.",
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
	case ExtractOperationPDF, ExtractOperationText, ExtractOperationJSON,
		"extractfrompdf", "extractfromfile", "extractfromjson":
		return nil
	default:
		return fmt.Errorf("operation must be pdf, text or json")
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
