package nodes_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestExtractFromFileReadsTextAndJSON(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	textRef, err := store.Put("note.txt", "text/plain", strings.NewReader("hello from a file"))
	if err != nil {
		t.Fatalf("Put text = %v", err)
	}
	jsonRef, err := store.Put("doc.json", "application/json", strings.NewReader(`{"ok":true}`))
	if err != nil {
		t.Fatalf("Put json = %v", err)
	}

	executor := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest)
	textOut, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "text", "binaryPropertyName": "data"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": textRef}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("text extract = %v", err)
	}
	if textOut[0][0].JSON["text"] != "hello from a file" {
		t.Fatalf("text = %#v", textOut[0][0].JSON["text"])
	}

	jsonOut, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "json"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": jsonRef}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("json extract = %v", err)
	}
	decoded, _ := jsonOut[0][0].JSON["text"].(map[string]any)
	if decoded["ok"] != true {
		t.Fatalf("json = %#v", jsonOut[0][0].JSON["text"])
	}
}

func TestExtractFromFileReadsAPDFTextLayer(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	ref, err := store.Put("hello.pdf", "application/pdf", bytes.NewReader(minimalPDF("HelloPDF")))
	if err != nil {
		t.Fatalf("Put pdf = %v", err)
	}
	output, err := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest).Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "pdf"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": ref}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("pdf extract = %v", err)
	}
	text, _ := output[0][0].JSON["text"].(string)
	if !strings.Contains(text, "HelloPDF") {
		t.Fatalf("pdf text = %q, want HelloPDF", text)
	}
}

func TestExtractFromFileRefusesWithoutBinaries(t *testing.T) {
	t.Parallel()

	_, err := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest).Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "text"},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "binary storage") {
		t.Fatalf("err = %v, want binary storage", err)
	}
}

func minimalPDF(text string) []byte {
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	objects := []string{
		"1 0 obj<< /Type /Catalog /Pages 2 0 R >>endobj\n",
		"2 0 obj<< /Type /Pages /Kids [3 0 R] /Count 1 >>endobj\n",
		"3 0 obj<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources<< /Font<< /F1 5 0 R >> >> >>endobj\n",
		"4 0 obj<< /Length " + itoa(len(stream)) + " >>stream\n" + stream + "\nendstream\nendobj\n",
		"5 0 obj<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>endobj\n",
	}
	header := "%PDF-1.4\n"
	offsets := make([]int, len(objects))
	cursor := len(header)
	body := header
	for index, object := range objects {
		offsets[index] = cursor
		body += object
		cursor += len(object)
	}
	xrefPos := cursor
	xref := "xref\n0 6\n0000000000 65535 f \n"
	for _, offset := range offsets {
		padded := fmt.Sprintf("%010d 00000 n \n", offset)
		xref += padded
	}
	xref += "trailer<< /Size 6 /Root 1 0 R >>\nstartxref\n" + itoa(xrefPos) + "\n%%EOF\n"
	return []byte(body + xref)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestExtractFromFileReadsCSVAndXLSXAndBinary(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	csvRef, err := store.Put("rows.csv", "text/csv", strings.NewReader("name,role\nAda,engineer\nGrace,admiral\n"))
	if err != nil {
		t.Fatalf("Put csv = %v", err)
	}
	var workbook bytes.Buffer
	if err := writeXLSXFixture(&workbook); err != nil {
		t.Fatalf("build xlsx fixture = %v", err)
	}
	xlsxRef, err := store.Put("rows.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", &workbook)
	if err != nil {
		t.Fatalf("Put xlsx = %v", err)
	}
	pdfishRef, err := store.Put("blob.bin", "application/octet-stream", strings.NewReader("raw bytes"))
	if err != nil {
		t.Fatalf("Put bin = %v", err)
	}

	executor := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest)

	csvOut, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "csv", "destinationKey": "text"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": csvRef}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("csv extract = %v", err)
	}
	if len(csvOut[0]) != 2 {
		t.Fatalf("csv rows = %d items, want 2 (one per row, as n8n emits)", len(csvOut[0]))
	}
	if csvOut[0][0].JSON["name"] != "Ada" || csvOut[0][1].JSON["role"] != "admiral" {
		t.Fatalf("csv rows = %#v", csvOut[0])
	}

	xlsxOut, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "xlsx"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": xlsxRef}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("xlsx extract = %v", err)
	}
	if len(xlsxOut[0]) != 1 || xlsxOut[0][0].JSON["name"] != "Ada" {
		t.Fatalf("xlsx rows = %#v", xlsxOut[0])
	}

	binaryOut, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "binaryToProperty", "destinationKey": "payload"},
	}, workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"data": pdfishRef}}}}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("binaryToProperty extract = %v", err)
	}
	payload, _ := binaryOut[0][0].JSON["payload"].(map[string]any)
	if payload == nil || payload["dataBase64"] != "cmF3IGJ5dGVz" {
		t.Fatalf("binaryToProperty = %#v", binaryOut[0][0].JSON["payload"])
	}
}

// writeXLSXFixture builds a minimal two-row workbook the way every producer
// this node has to read lays one out: a zip holding a shared string table and
// a first worksheet.
func writeXLSXFixture(buffer *bytes.Buffer) error {
	archive := zip.NewWriter(buffer)
	write := func(name, body string) error {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write([]byte(body))
		return err
	}
	if err := write("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`); err != nil {
		return err
	}
	if err := write("xl/sharedStrings.xml", `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>name</t></si><si><t>role</t></si><si><t>Ada</t></si><si><t>engineer</t></si></sst>`); err != nil {
		return err
	}
	if err := write("xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>`+
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row>`+
		`</sheetData></worksheet>`); err != nil {
		return err
	}
	return archive.Close()
}
