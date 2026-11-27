package n8n_test

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestCompressionImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.compression", 1.1,
		`{"operation":"compress","binaryPropertyName":"report","outputFormat":"zip","fileName":"reports.zip","binaryPropertyOutput":"archive"}`)
	if imported.Type != n8n.CompressionNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.CompressionNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	store := binaryStoreForImport(t)
	reference, err := store.Put("report.txt", "text/plain", bytes.NewReader([]byte("quarterly")))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	output := executeImportedWithBinaries(t, imported, nodes.CompressionExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"report": reference}}}}, store)
	archive := output[0][0].Binary["archive"]
	if archive.FileName != "reports.zip" {
		t.Fatalf("output binary = %#v, want the archive under the chosen field and name", output[0][0].Binary)
	}
	body, _, err := store.Get(archive.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	packed, _ := io.ReadAll(body)
	_ = body.Close()
	reader, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	if err != nil || len(reader.File) != 1 || reader.File[0].Name != "report.txt" {
		t.Fatalf("archive = %v (%v), want report.txt inside", reader, err)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.compression" || returned.TypeVersion != 1.1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.compression v1.1", returned.Type, returned.TypeVersion)
	}
	want := map[string]any{
		"operation": "compress", "binaryPropertyName": "report", "outputFormat": "zip",
		"fileName": "reports.zip", "binaryPropertyOutput": "archive",
	}
	if !reflect.DeepEqual(returned.Parameters, want) {
		t.Fatalf("exported parameters = %#v, want %#v", returned.Parameters, want)
	}
}

// A node saved with every default left out is a decompress of the data field.
func TestCompressionImportsAnAllDefaultsNodeAsDecompress(t *testing.T) {
	t.Parallel()

	imported, _ := importNode(t, "n8n-nodes-base.compression", 1, `{}`)
	store := binaryStoreForImport(t)
	var packed bytes.Buffer
	writer := zip.NewWriter(&packed)
	entry, _ := writer.Create("a.txt")
	_, _ = entry.Write([]byte("inside"))
	_ = writer.Close()
	reference, err := store.Put("in.zip", "application/zip", bytes.NewReader(packed.Bytes()))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	output := executeImportedWithBinaries(t, imported, nodes.CompressionExecutorID,
		workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": reference}}}}, store)
	if file := output[0][0].Binary["file_0"]; file.FileName != "a.txt" {
		t.Fatalf("output binary = %#v, want a.txt in file_0", output[0][0].Binary)
	}
}
