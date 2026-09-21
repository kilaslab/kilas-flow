package nodes_test

import (
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
