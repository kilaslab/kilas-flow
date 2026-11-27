package nodes_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func runConvertErr(t *testing.T, store engine.BinaryStore, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	output, err := flowExecutor(t, nodes.ConvertToFileExecutorID).Execute(context.Background(),
		flowNode(t, nodes.ConvertToFileNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{Binaries: store})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func runConvert(t *testing.T, store engine.BinaryStore, parameters map[string]any, items ...workflow.Item) []workflow.Item {
	t.Helper()
	output, err := runConvertErr(t, store, parameters, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func people() []workflow.Item {
	return []workflow.Item{
		{JSON: map[string]any{"name": "Ada", "age": float64(36), "tags": []any{"math"}}},
		{JSON: map[string]any{"name": "Grace, \"Amazing\"", "active": true}},
	}
}

func TestConvertToCSVWritesOneFileFromEveryItem(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "csv"}, people()...)
	if len(out) != 1 || len(out[0].JSON) != 0 || len(out[0].Binary) != 1 {
		t.Fatalf("out = %#v, want one item holding only the file", out)
	}
	file := out[0].Binary["data"]
	if file.FileName != "file.csv" || file.MediaType != "text/csv" {
		t.Fatalf("file = %#v", file)
	}
	// The array is written as JSON, so its quotes are doubled inside a quoted cell.
	want := "active,age,name,tags\n" +
		",36,Ada,\"[\"\"math\"\"]\"\n" +
		"true,,\"Grace, \"\"Amazing\"\"\",\n"
	if got := string(readFile(t, store, file)); got != want {
		t.Fatalf("csv =\n%q\nwant\n%q", got, want)
	}
}

func TestConvertToCSVOptions(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{
		"operation": "csv", "binaryPropertyName": "report",
		"options": map[string]any{"delimiter": ";", "headerRow": false, "fileName": "people.csv", "addBOM": true},
	}, workflow.Item{JSON: map[string]any{"a": "1", "b": "2"}})
	file := out[0].Binary["report"]
	if file.FileName != "people.csv" {
		t.Fatalf("file = %#v", file)
	}
	if got := readFile(t, store, file); !bytes.Equal(got, append([]byte{0xEF, 0xBB, 0xBF}, "1;2\n"...)) {
		t.Fatalf("csv = %q, want a BOM, no header row and a semicolon", got)
	}
	for _, delimiter := range []string{";;", `"`, "\n"} {
		if _, err := runConvertErr(t, store, map[string]any{"operation": "csv", "options": map[string]any{"delimiter": delimiter}}, people()...); err == nil {
			t.Errorf("delimiter %q: want an error, got none", delimiter)
		}
	}
}

func TestConvertToCSVRoundTripsThroughExtractFromFile(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "csv"},
		workflow.Item{JSON: map[string]any{"name": "Ada", "city": "Jakarta"}},
		workflow.Item{JSON: map[string]any{"name": "Grace", "city": "New York"}})
	extracted, err := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest).Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "csv"},
	}, workflow.NodeInput{"main": out}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("extract error = %v", err)
	}
	want := []map[string]any{{"name": "Ada", "city": "Jakarta"}, {"name": "Grace", "city": "New York"}}
	if len(extracted[0]) != 2 || !reflect.DeepEqual(extracted[0][0].JSON, want[0]) || !reflect.DeepEqual(extracted[0][1].JSON, want[1]) {
		t.Fatalf("extracted = %#v, want %#v", extracted[0], want)
	}
}

func TestConvertToXLSXWritesAWorkbookExtractFromFileReadsBack(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "xlsx", "options": map[string]any{"sheetName": "People & Co"}},
		workflow.Item{JSON: map[string]any{"name": "Ada <1>", "age": float64(36), "ok": true}},
		workflow.Item{JSON: map[string]any{"name": "Grace", "age": float64(45.5)}})
	file := out[0].Binary["data"]
	if file.FileName != "file.xlsx" || !strings.Contains(file.MediaType, "spreadsheetml.sheet") {
		t.Fatalf("file = %#v", file)
	}

	// It is a zip with the parts a spreadsheet program expects.
	packed := readFile(t, store, file)
	archive, err := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	if err != nil {
		t.Fatalf("the output is not a zip: %v", err)
	}
	names := map[string]bool{}
	for _, entry := range archive.File {
		names[entry.Name] = true
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"} {
		if !names[want] {
			t.Errorf("the workbook has no %s", want)
		}
	}

	extracted, err := engine.ExecutorFunc(nodes.ExecuteExtractFromFileForTest).Execute(context.Background(), workflow.IRNode{
		ID: "x", Name: "Extract", Type: nodes.ExtractFromFileNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"operation": "xlsx"},
	}, workflow.NodeInput{"main": out}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("extract error = %v", err)
	}
	rows := extracted[0]
	if len(rows) != 2 {
		t.Fatalf("rows = %#v, want two", rows)
	}
	if rows[0].JSON["name"] != "Ada <1>" || rows[0].JSON["age"] != "36" || rows[0].JSON["ok"] != "1" ||
		rows[1].JSON["name"] != "Grace" || rows[1].JSON["age"] != "45.5" || rows[1].JSON["ok"] != "" {
		t.Fatalf("rows = %#v, want the cells read back with the empty cell kept in its column", rows)
	}
}

func TestConvertToXLSXNamesTheSheetAndRefusesABadName(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "xlsx", "options": map[string]any{"sheetName": "Q1 2026"}}, people()...)
	packed := readFile(t, store, out[0].Binary["data"])
	archive, _ := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	for _, entry := range archive.File {
		if entry.Name != "xl/workbook.xml" {
			continue
		}
		opened, _ := entry.Open()
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(opened)
		_ = opened.Close()
		if !strings.Contains(body.String(), `name="Q1 2026"`) {
			t.Errorf("workbook = %s, want the sheet named", body)
		}
	}
	for _, name := range []string{"a/b", "has:colon", strings.Repeat("x", 32), "'quoted'"} {
		if _, err := runConvertErr(t, store, map[string]any{"operation": "xlsx", "options": map[string]any{"sheetName": name}}, people()...); err == nil {
			t.Errorf("sheet name %q: want an error, got none", name)
		}
	}
}

func TestConvertToXLSXNamesColumnsPastZ(t *testing.T) {
	t.Parallel()

	row := map[string]any{}
	for index := 0; index < 28; index++ {
		row["c"+string(rune('a'+index/26))+string(rune('a'+index%26))] = "v"
	}
	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "xlsx"}, workflow.Item{JSON: row})
	packed := readFile(t, store, out[0].Binary["data"])
	archive, _ := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
	for _, entry := range archive.File {
		if entry.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		opened, _ := entry.Open()
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(opened)
		_ = opened.Close()
		for _, want := range []string{`r="Z1"`, `r="AA1"`, `r="AB2"`} {
			if !strings.Contains(body.String(), want) {
				t.Errorf("sheet lacks cell %s", want)
			}
		}
	}
}

func TestConvertToJSONAllItemsAndEachItem(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	all := runConvert(t, store, map[string]any{"operation": "toJson", "options": map[string]any{"fileName": "all.json"}},
		workflow.Item{JSON: map[string]any{"a": float64(1), "html": "<b>"}}, workflow.Item{JSON: map[string]any{"a": float64(2)}})
	if len(all) != 1 || all[0].Binary["data"].FileName != "all.json" || all[0].Binary["data"].MediaType != "application/json" {
		t.Fatalf("all = %#v", all)
	}
	var decoded []map[string]any
	body := readFile(t, store, all[0].Binary["data"])
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded) != 2 || decoded[0]["html"] != "<b>" || decoded[1]["a"] != float64(2) {
		t.Fatalf("json = %s (%v), want both items, HTML characters unescaped", body, err)
	}

	each := runConvert(t, store, map[string]any{"operation": "toJson", "mode": "each", "options": map[string]any{"format": true}},
		workflow.Item{JSON: map[string]any{"id": float64(1)}}, workflow.Item{JSON: map[string]any{"id": float64(2)}})
	if len(each) != 2 || each[0].JSON["id"] != float64(1) {
		t.Fatalf("each = %#v, want one file-carrying item per input, JSON kept", each)
	}
	if got := string(readFile(t, store, each[1].Binary["data"])); got != "{\n  \"id\": 2\n}" {
		t.Fatalf("second file = %q, want one indented object", got)
	}
}

func TestConvertToTextWritesTheNamedFieldPerItem(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	out := runConvert(t, store, map[string]any{"operation": "toText", "sourceProperty": "body.text", "binaryPropertyName": "doc", "options": map[string]any{"addBOM": true}},
		workflow.Item{JSON: map[string]any{"body": map[string]any{"text": "héllo"}, "keep": 1}},
		workflow.Item{JSON: map[string]any{"body": map[string]any{"text": "two"}}})
	if len(out) != 2 || out[0].JSON["keep"] != 1.0 && out[0].JSON["keep"] != 1 {
		t.Fatalf("out = %#v", out)
	}
	file := out[0].Binary["doc"]
	if file.FileName != "file.txt" || file.MediaType != "text/plain" {
		t.Fatalf("file = %#v", file)
	}
	if got := readFile(t, store, file); !bytes.Equal(got, append([]byte{0xEF, 0xBB, 0xBF}, "héllo"...)) {
		t.Fatalf("text = %q", got)
	}
}

func TestConvertBase64StringToFile(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	encoded := "iVBORw0KGgoAAAAA" // the same bytes in base64
	for name, source := range map[string]string{
		"plain":       encoded,
		"data URL":    "data:image/png;base64," + encoded,
		"line breaks": encoded[:8] + "\n" + encoded[8:],
	} {
		out := runConvert(t, store, map[string]any{"operation": "toBinary", "sourceProperty": "b64", "options": map[string]any{"fileName": "pic.png"}},
			workflow.Item{JSON: map[string]any{"b64": source}})
		file := out[0].Binary["data"]
		if !bytes.Equal(readFile(t, store, file), png) || file.FileName != "pic.png" || file.MediaType != "image/png" {
			t.Errorf("%s: file = %#v, want the PNG detected from its bytes", name, file)
		}
	}
	out := runConvert(t, store, map[string]any{"operation": "toBinary", "sourceProperty": "b64", "options": map[string]any{"mimeType": "application/x-custom"}},
		workflow.Item{JSON: map[string]any{"b64": encoded}})
	if out[0].Binary["data"].MediaType != "application/x-custom" {
		t.Errorf("media type = %q, want the chosen one", out[0].Binary["data"].MediaType)
	}
}

func TestConvertToFileRefusesWhatItCannotWrite(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := workflow.Item{JSON: map[string]any{"text": "x", "n": float64(1)}}
	cases := map[string]map[string]any{
		"no source property": {"operation": "toText"},
		"missing field":      {"operation": "toText", "sourceProperty": "nope"},
		"not text":           {"operation": "toText", "sourceProperty": "n"},
		"not base64":         {"operation": "toBinary", "sourceProperty": "text"},
		"unknown operation":  {"operation": "iCal"},
	}
	for name, parameters := range cases {
		if _, err := runConvertErr(t, store, parameters, item); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
	if _, err := runConvertErr(t, nil, map[string]any{"operation": "csv"}, item); err == nil {
		t.Errorf("no binary storage: want an error, got none")
	}
}

func TestConvertToFileValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.ConvertToFileNodeType, workflow.V(1))
	cases := map[string]map[string]any{
		"ods":      {"operation": "ods"},
		"compress": {"operation": "csv", "options": map[string]any{"compress": true}},
		"latin1":   {"operation": "csv", "options": map[string]any{"encoding": "latin1"}},
	}
	for name, parameters := range cases {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: Validate() = %v, want an error naming it", name, err)
		}
	}
	for _, encoding := range []string{"utf8", "utf-8", "UTF-8"} {
		if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "csv", "options": map[string]any{"encoding": encoding}}}); err != nil {
			t.Errorf("Validate() of encoding %q = %v", encoding, err)
		}
	}
}
