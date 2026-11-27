package nodes_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func runCompressionErr(t *testing.T, store engine.BinaryStore, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	output, err := flowExecutor(t, nodes.CompressionExecutorID).Execute(context.Background(),
		flowNode(t, nodes.CompressionNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{Binaries: store})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func runCompression(t *testing.T, store engine.BinaryStore, parameters map[string]any, items ...workflow.Item) []workflow.Item {
	t.Helper()
	output, err := runCompressionErr(t, store, parameters, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func putFile(t *testing.T, store engine.BinaryStore, name, mediaType string, contents []byte) workflow.BinaryRef {
	t.Helper()
	reference, err := store.Put(name, mediaType, bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("Put(%s) error = %v", name, err)
	}
	return reference
}

func readFile(t *testing.T, store engine.BinaryStore, reference workflow.BinaryRef) []byte {
	t.Helper()
	body, _, err := store.Get(reference.ID)
	if err != nil {
		t.Fatalf("Get(%s) error = %v", reference.ID, err)
	}
	defer body.Close()
	contents, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read %s: %v", reference.ID, err)
	}
	return contents
}

func zipOf(t *testing.T, files map[string]string, order ...string) []byte {
	t.Helper()
	var packed bytes.Buffer
	writer := zip.NewWriter(&packed)
	for _, name := range order {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return packed.Bytes()
}

func TestCompressionZipsEveryListedFieldIntoOneArchive(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := workflow.Item{
		JSON: map[string]any{"keep": "me"},
		Binary: map[string]workflow.BinaryRef{
			"a": putFile(t, store, "a.txt", "text/plain", []byte("alpha")),
			"b": putFile(t, store, "b.txt", "text/plain", []byte("beta")),
		},
	}
	out := runCompression(t, store, map[string]any{
		"operation": "compress", "binaryPropertyName": "a, b", "outputFormat": "zip", "fileName": "bundle.zip", "binaryPropertyOutput": "archive",
	}, item)

	if len(out) != 1 || out[0].JSON["keep"] != "me" || len(out[0].Binary) != 1 {
		t.Fatalf("out = %#v, want one item with its JSON and only the archive attached", out)
	}
	archive := out[0].Binary["archive"]
	if archive.FileName != "bundle.zip" || archive.MediaType != "application/zip" {
		t.Fatalf("archive = %#v", archive)
	}
	packed := readFile(t, store, archive)
	reader, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	if err != nil {
		t.Fatalf("the output is not a zip: %v", err)
	}
	got := map[string]string{}
	for _, entry := range reader.File {
		opened, _ := entry.Open()
		body, _ := io.ReadAll(opened)
		_ = opened.Close()
		got[entry.Name] = string(body)
	}
	if len(got) != 2 || got["a.txt"] != "alpha" || got["b.txt"] != "beta" {
		t.Fatalf("archive holds %#v, want both files", got)
	}
}

func TestCompressionZipNamesTheArchiveByDefaultAndKeepsDuplicateNamesApart(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{
		"a": putFile(t, store, "same.txt", "text/plain", []byte("one")),
		"b": putFile(t, store, "same.txt", "text/plain", []byte("two")),
	}}
	out := runCompression(t, store, map[string]any{"operation": "compress", "binaryPropertyName": "a,b"}, item)
	archive := out[0].Binary["data"]
	if archive.FileName != "data.zip" {
		t.Fatalf("archive name = %q, want data.zip", archive.FileName)
	}
	packed := readFile(t, store, archive)
	reader, _ := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	names := []string{}
	for _, entry := range reader.File {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "same.txt,same-2.txt" {
		t.Fatalf("entry names = %v, want the second to be renamed", names)
	}
}

func TestCompressionGzipsEachFileOnItsOwn(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{
		"data":  putFile(t, store, "report.csv", "text/csv", []byte("a,b\n1,2\n")),
		"other": putFile(t, store, "notes.txt", "text/plain", []byte("notes")),
	}}
	out := runCompression(t, store, map[string]any{
		"operation": "compress", "binaryPropertyName": "data,other", "outputFormat": "gzip", "outputPrefix": "gz_",
	}, item)
	if len(out[0].Binary) != 2 {
		t.Fatalf("binary = %#v, want one gzip file per input", out[0].Binary)
	}
	first := out[0].Binary["gz_0"]
	if first.FileName != "report.csv.gz" || first.MediaType != "application/gzip" {
		t.Fatalf("first = %#v", first)
	}
	reader, err := gzip.NewReader(bytes.NewReader(readFile(t, store, first)))
	if err != nil {
		t.Fatalf("the output is not gzip: %v", err)
	}
	body, _ := io.ReadAll(reader)
	if string(body) != "a,b\n1,2\n" {
		t.Fatalf("gunzipped = %q", body)
	}
}

func TestCompressionUnpacksAZipIntoNumberedFields(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	archive := zipOf(t, map[string]string{"dir/one.txt": "first", "two.json": `{"n":2}`}, "dir/one.txt", "two.json")
	item := workflow.Item{
		JSON:   map[string]any{"keep": "me"},
		Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "in.zip", "application/zip", archive)},
	}
	out := runCompression(t, store, map[string]any{"operation": "decompress", "binaryPropertyName": "data", "outputPrefix": "file_"}, item)

	if out[0].JSON["keep"] != "me" || len(out[0].Binary) != 2 {
		t.Fatalf("out = %#v, want the JSON kept and the two files attached, not the archive", out[0])
	}
	first, second := out[0].Binary["file_0"], out[0].Binary["file_1"]
	if first.FileName != "one.txt" || string(readFile(t, store, first)) != "first" {
		t.Fatalf("file_0 = %#v, want one.txt without its directory", first)
	}
	if second.FileName != "two.json" || second.MediaType == "" || string(readFile(t, store, second)) != `{"n":2}` {
		t.Fatalf("file_1 = %#v", second)
	}
}

func TestCompressionUnpacksGzipAndTellsTheFormatFromTheBytes(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	var packed bytes.Buffer
	writer := gzip.NewWriter(&packed)
	_, _ = writer.Write([]byte("hello gzip"))
	_ = writer.Close()
	// The file's own name and type claim nothing useful; the bytes decide.
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "mystery.bin", "application/octet-stream", packed.Bytes())}}
	out := runCompression(t, store, map[string]any{"operation": "decompress"}, item)
	file := out[0].Binary["file_0"]
	if string(readFile(t, store, file)) != "hello gzip" || file.FileName != "mystery.bin" {
		t.Fatalf("file_0 = %#v", file)
	}
}

func TestCompressionNumbersFilesAcrossSeveralInputFields(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{
		"a": putFile(t, store, "a.zip", "application/zip", zipOf(t, map[string]string{"x.txt": "x", "y.txt": "y"}, "x.txt", "y.txt")),
		"b": putFile(t, store, "b.zip", "application/zip", zipOf(t, map[string]string{"z.txt": "z"}, "z.txt")),
	}}
	out := runCompression(t, store, map[string]any{"operation": "decompress", "binaryPropertyName": "a,b"}, item)
	if len(out[0].Binary) != 3 || string(readFile(t, store, out[0].Binary["file_2"])) != "z" {
		t.Fatalf("binary = %#v, want three files numbered 0 to 2", out[0].Binary)
	}
}

func TestCompressionRoundTrips(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	payload := bytes.Repeat([]byte("round trip "), 50)
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "p.txt", "text/plain", payload)}}
	zipped := runCompression(t, store, map[string]any{"operation": "compress", "outputFormat": "zip"}, item)
	unzipped := runCompression(t, store, map[string]any{"operation": "decompress"}, zipped[0])
	if !bytes.Equal(readFile(t, store, unzipped[0].Binary["file_0"]), payload) {
		t.Fatalf("zip round trip changed the file")
	}
	gzipped := runCompression(t, store, map[string]any{"operation": "compress", "outputFormat": "gzip", "outputPrefix": "z"}, item)
	ungzipped := runCompression(t, store, map[string]any{"operation": "decompress", "binaryPropertyName": "z0"}, gzipped[0])
	if !bytes.Equal(readFile(t, store, ungzipped[0].Binary["file_0"]), payload) {
		t.Fatalf("gzip round trip changed the file")
	}
}

func TestCompressionRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	plain := workflow.Item{Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "x.txt", "text/plain", []byte("not an archive"))}}
	truncated := zipOf(t, map[string]string{"a": strings.Repeat("a", 1000)}, "a")
	broken := workflow.Item{Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "x.zip", "application/zip", truncated[:len(truncated)/2])}}

	for name, item := range map[string]workflow.Item{"plain text": plain, "truncated zip": broken, "no binary": {JSON: map[string]any{}}} {
		if _, err := runCompressionErr(t, store, map[string]any{"operation": "decompress"}, item); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
	if _, err := runCompressionErr(t, nil, map[string]any{"operation": "decompress"}, plain); err == nil {
		t.Errorf("no binary storage: want an error, got none")
	}
	_, err := runCompressionErr(t, store, map[string]any{"operation": "compress", "binaryPropertyName": "missing"}, plain)
	if err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Errorf("a missing field error = %v, want it to name the field", err)
	}
}

func TestCompressionStopsAnArchiveWithTooManyFiles(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	files := map[string]string{}
	order := []string{}
	for index := 0; index < 1001; index++ {
		name := fmt.Sprintf("f%04d.txt", index)
		files[name] = "1"
		order = append(order, name)
	}
	item := workflow.Item{Binary: map[string]workflow.BinaryRef{"data": putFile(t, store, "many.zip", "application/zip", zipOf(t, files, order...))}}
	_, err := runCompressionErr(t, store, map[string]any{"operation": "decompress"}, item)
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("error = %v, want the entry limit named", err)
	}
}

func TestCompressionValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.CompressionNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "encrypt"}}); err == nil || !strings.Contains(err.Error(), "encrypt") {
		t.Errorf("Validate() of a bad operation = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "compress", "outputFormat": "rar"}}); err == nil || !strings.Contains(err.Error(), "rar") {
		t.Errorf("Validate() of a bad format = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "compress", "outputFormat": "gzip"}}); err != nil {
		t.Errorf("Validate() of a good node = %v", err)
	}
}
