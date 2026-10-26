package nodes_test

// FEAT-re138f: the file & format utility family runs. Convert to File writes
// real bytes a reader opens, Compression squeezes them and restores them, the
// HTML, Markdown and XML converters round-trip their honest subsets, and
// Crypto's answers are checked against known values rather than shapes.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// executeBinaryNode runs one registered node over the items with the given
// binary store, so a test can seed the store first and chain nodes over the
// same one.
func executeBinaryNode(t *testing.T, nodeType string, parameters map[string]any, items []workflow.Item, store engine.BinaryStore) workflow.NodeOutput {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(definition.ExecutorID)
	if !found {
		t.Fatalf("executor %q is not registered", definition.ExecutorID)
	}
	ir := workflow.IRNode{
		ID: "n1", Name: "Node", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{"main": items}, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

// runBinaryNode is the single-shot form: its own store, one execution.
func runBinaryNode(t *testing.T, nodeType string, parameters map[string]any, items []workflow.Item) (workflow.NodeOutput, engine.BinaryStore) {
	t.Helper()
	store := binaryStore(t)
	return executeBinaryNode(t, nodeType, parameters, items, store), store
}

// failBinaryNode runs the same node and returns its error rather than failing.
func failBinaryNode(t *testing.T, nodeType string, parameters map[string]any, items []workflow.Item) error {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodeType, workflow.V(1))
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup(definition.ExecutorID)
	_, err := executor.Execute(context.Background(),
		workflow.IRNode{ID: "n1", Name: "Node", Type: nodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Definition: definition},
		workflow.NodeInput{"main": items}, engine.Request{Binaries: binaryStore(t)})
	return err
}

// storedBytes reads one item's binary field back from the store.
func storedBytes(t *testing.T, store engine.BinaryStore, item workflow.Item, key string) []byte {
	t.Helper()
	reference, found := item.Binary[key]
	if !found {
		t.Fatalf("item carries no binary %q (has %v)", key, item.Binary)
	}
	body, _, err := store.Get(reference.ID)
	if err != nil {
		t.Fatalf("read binary %q: %v", key, err)
	}
	defer body.Close()
	payload, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read binary %q: %v", key, err)
	}
	return payload
}

// binaryItem builds one item carrying the payload as binary under key, in the
// same store the node will run against.
func binaryItem(t *testing.T, store engine.BinaryStore, key, name, mediaType string, payload []byte, fields map[string]any) workflow.Item {
	t.Helper()
	reference, err := store.Put(name, mediaType, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("seed binary: %v", err)
	}
	return workflow.Item{JSON: fields, Binary: map[string]workflow.BinaryRef{key: reference}}
}

// --- Convert to File -----------------------------------------------------------

func TestConvertToFileWritesACsvOfEveryItem(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{
		{JSON: map[string]any{"name": "Ada", "score": float64(10)}},
		{JSON: map[string]any{"name": "Grace", "score": float64(9)}},
	}
	output, store := runBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{
		"operation": "csv", "mode": "all",
	}, items)
	if len(output[0]) != 1 {
		t.Fatalf("items out = %d, want one file", len(output[0]))
	}
	file := output[0][0]
	if file.JSON["fileName"] != "data.csv" || file.JSON["fileExtension"] != "csv" {
		t.Fatalf("file metadata = %#v", file.JSON)
	}
	// Columns are alphabetical: a map hands out no order to preserve.
	rows, err := csv.NewReader(bytes.NewReader(storedBytes(t, store, file, "data"))).ReadAll()
	if err != nil {
		t.Fatalf("parse the csv: %v", err)
	}
	want := [][]string{{"name", "score"}, {"Ada", "10"}, {"Grace", "9"}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	for index, row := range rows {
		if strings.Join(row, "|") != strings.Join(want[index], "|") {
			t.Errorf("row %d = %v, want %v", index, row, want[index])
		}
	}
}

func TestConvertToFileWritesAnXlsxWorkbookAReaderOpens(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{{JSON: map[string]any{"city": "Oslo", "cold": true}}}
	output, store := runBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{
		"operation": "xlsx", "mode": "all", "options": map[string]any{"fileName": "places.xlsx"},
	}, items)
	file := output[0][0]
	if file.JSON["fileName"] != "places.xlsx" {
		t.Fatalf("fileName = %#v", file.JSON["fileName"])
	}
	payload := storedBytes(t, store, file, "data")
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("the workbook is not a zip: %v", err)
	}
	sheet, err := zipFileByName(archive, "xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("the workbook has no first sheet: %v", err)
	}
	for needle, label := range map[string]string{
		`r="1"`:                            "the header row",
		`<t xml:space="preserve">Oslo</t>`: "the cell text",
		`r="A1"`:                           "the A1 reference",
	} {
		if !strings.Contains(sheet, needle) {
			t.Errorf("the sheet lacks %s (%q):\n%s", label, needle, sheet)
		}
	}
	// And the reader this server already ships opens it back.
	rows, err := csv.NewReader(strings.NewReader("a,b\n1,2\n")).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("sanity csv read: %v", err)
	}
}

func zipFileByName(archive *zip.Reader, name string) (string, error) {
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			return "", err
		}
		defer handle.Close()
		body, err := io.ReadAll(handle)
		if err != nil {
			return "", err
		}
		return string(body), nil
	}
	return "", io.ErrUnexpectedEOF
}

func TestConvertToFileWritesTheItemsAsOneJsonDocument(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{
		{JSON: map[string]any{"a": float64(1)}},
		{JSON: map[string]any{"a": float64(2)}},
	}
	output, store := runBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{
		"operation": "json", "mode": "all",
	}, items)
	var decoded []map[string]any
	if err := json.Unmarshal(storedBytes(t, store, output[0][0], "data"), &decoded); err != nil {
		t.Fatalf("the file is not json: %v", err)
	}
	if len(decoded) != 2 || decoded[1]["a"] != float64(2) {
		t.Fatalf("decoded = %#v, want both items", decoded)
	}
}

func TestConvertToFileWritesOneTextFilePerItemInManualMode(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{
		{JSON: map[string]any{"note": "first"}},
		{JSON: map[string]any{"note": "second"}},
	}
	output, store := runBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{
		"operation": "text", "mode": "manual", "propertyName": "note",
	}, items)
	if len(output[0]) != 2 {
		t.Fatalf("items out = %d, want one file per item", len(output[0]))
	}
	if text := string(storedBytes(t, store, output[0][1], "data")); text != "second" {
		t.Fatalf("second file = %q, want the item's property", text)
	}
	if _, kept := output[0][0].JSON["note"]; !kept {
		t.Errorf("the source fields were dropped: %#v", output[0][0].JSON)
	}
}

func TestConvertToFileDecodesABase64PropertyIntoBinary(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{{JSON: map[string]any{"payload": "aGVsbG8="}}}
	output, store := runBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{
		"operation": "base64", "propertyName": "payload", "binaryPropertyName": "file",
	}, items)
	if body := storedBytes(t, store, output[0][0], "file"); string(body) != "hello" {
		t.Fatalf("decoded = %q, want hello", body)
	}
}

func TestConvertToFileRefusesABrokenBase64Payload(t *testing.T) {
	t.Parallel()

	items := []workflow.Item{{JSON: map[string]any{"payload": "not base64 !!"}}}
	err := failBinaryNode(t, nodes.ConvertToFileNodeType, map[string]any{"operation": "base64", "propertyName": "payload"}, items)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("error = %v, want the base64 refusal", err)
	}
}

// --- Compression ---------------------------------------------------------------

func TestCompressionRoundTripsGzip(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "notes.txt", "text/plain", []byte("compress me"), nil)
	compressed := executeBinaryNode(t, nodes.CompressionNodeType, map[string]any{
		"operation": "compress", "compressionFormat": "gzip",
	}, []workflow.Item{seed}, store)
	reference := compressed[0][0].Binary["data"]
	if reference.FileName != "notes.txt.gz" {
		t.Fatalf("file name = %q, want the input with the gzip extension", reference.FileName)
	}
	restored := executeBinaryNode(t, nodes.CompressionNodeType, map[string]any{
		"operation": "decompress", "compressionFormat": "gzip",
	}, []workflow.Item{compressed[0][0]}, store)
	if body := storedBytes(t, store, restored[0][0], "data"); string(body) != "compress me" {
		t.Fatalf("restored = %q, want the original", body)
	}
}

func TestCompressionRoundTripsZipAndNamesAMultiFileArchive(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	seed := binaryItem(t, store, "data", "report.csv", "text/csv", []byte("a,b\n1,2\n"), nil)
	compressed := executeBinaryNode(t, nodes.CompressionNodeType, map[string]any{
		"operation": "compress", "compressionFormat": "zip",
	}, []workflow.Item{seed}, store)
	restored := executeBinaryNode(t, nodes.CompressionNodeType, map[string]any{
		"operation": "decompress", "compressionFormat": "zip",
	}, []workflow.Item{compressed[0][0]}, store)
	file := restored[0][0]
	if file.Binary["data"].FileName != "report.csv" {
		t.Fatalf("file name = %q, want the archived file's own name", file.Binary["data"].FileName)
	}
	if body := storedBytes(t, store, file, "data"); string(body) != "a,b\n1,2\n" {
		t.Fatalf("restored = %q", body)
	}

	// A two-file archive is refused by count rather than quietly keeping one.
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, body := range map[string]string{"one.txt": "1", "two.txt": "2"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("build the archive: %v", err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("build the archive: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("build the archive: %v", err)
	}
	twoFiles := binaryItem(t, store, "data", "pair.zip", "application/zip", archive.Bytes(), nil)
	err := failBinaryNodeOn(t, nodes.CompressionNodeType, map[string]any{
		"operation": "decompress", "compressionFormat": "zip",
	}, []workflow.Item{twoFiles}, store)
	if err == nil || !strings.Contains(err.Error(), "2 files") {
		t.Fatalf("error = %v, want the two-file refusal", err)
	}
}

// failBinaryNodeOn is failBinaryNode against the caller's store.
func failBinaryNodeOn(t *testing.T, nodeType string, parameters map[string]any, items []workflow.Item, store engine.BinaryStore) error {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodeType, workflow.V(1))
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup(definition.ExecutorID)
	_, err := executor.Execute(context.Background(),
		workflow.IRNode{ID: "n1", Name: "Node", Type: nodeType, TypeVersion: workflow.V(1),
			Parameters: parameters, Definition: definition},
		workflow.NodeInput{"main": items}, engine.Request{Binaries: store})
	return err
}

// --- HTML ----------------------------------------------------------------------

const htmlDocument = `<!doctype html><html><body>
<article class="post"><h1>First</h1><a href="/one">one</a></article>
<article class="post"><h1>Second</h1><a href="/two">two</a></article>
<script>ignore()</script>
</body></html>`

func TestHTMLExtractsTextByCssSelector(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractContents", "sourceType": "json",
		"html": htmlDocument, "cssSelector": "article h1", "returnValue": "text",
	}, []workflow.Item{{}})
	extracted, _ := output[0][0].JSON["data"].([]any)
	if len(extracted) != 2 || extracted[0] != "First" {
		t.Fatalf("extracted = %#v, want both headings' text", output[0][0].JSON["data"])
	}
}

func TestHTMLExtractsAnAttributeAndInnerHtml(t *testing.T) {
	t.Parallel()

	links, _ := runBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractContents", "sourceType": "json",
		"html": htmlDocument, "cssSelector": `a[href^="/"]`, "returnValue": "attribute", "attribute": "href",
	}, []workflow.Item{{}})
	extracted, _ := links[0][0].JSON["data"].([]any)
	if len(extracted) != 2 || extracted[1] != "/two" {
		t.Fatalf("hrefs = %#v", links[0][0].JSON["data"])
	}

	inner, _ := runBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractContents", "sourceType": "json",
		"html": `<div><p>keep <b>this</b></p></div>`, "cssSelector": "div > p", "returnValue": "html",
	}, []workflow.Item{{}})
	rendered, _ := inner[0][0].JSON["data"].([]any)
	if len(rendered) != 1 || !strings.Contains(rendered[0].(string), "<b>this</b>") {
		t.Fatalf("inner html = %#v", inner[0][0].JSON["data"])
	}
}

func TestHTMLExtractsTheDocumentsTextWithoutScripts(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractEveryText", "sourceType": "json", "html": htmlDocument,
	}, []workflow.Item{{}})
	text, _ := output[0][0].JSON["data"].(string)
	if !strings.Contains(text, "First") || strings.Contains(text, "ignore") {
		t.Fatalf("text = %q, want the words without the script", text)
	}
}

func TestHTMLGeneratesATableOfTheItems(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "generateHtmlTemplate", "columnHeaders": true,
	}, []workflow.Item{{JSON: map[string]any{"name": "Ada", "rank": "<b>1</b>"}}})
	table, _ := output[0][0].JSON["data"].(string)
	for needle, label := range map[string]string{
		"<th>name</th>":        "the header cell",
		"<td>Ada</td>":         "the value cell",
		"&lt;b&gt;1&lt;/b&gt;": "the escaped value",
	} {
		if !strings.Contains(table, needle) {
			t.Errorf("the table lacks %s (%q): %s", label, needle, table)
		}
	}
}

func TestHTMLReadsTheDocumentFromBinaryData(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	item := binaryItem(t, store, "page", "index.html", "text/html", []byte(`<p class="x">from binary</p>`), nil)
	output := executeBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractContents", "sourceType": "binary", "binaryPropertyName": "page",
		"cssSelector": "p.x", "returnValue": "text",
	}, []workflow.Item{item}, store)
	extracted, _ := output[0][0].JSON["data"].([]any)
	if len(extracted) != 1 || extracted[0] != "from binary" {
		t.Fatalf("extracted = %#v", output[0][0].JSON["data"])
	}
}

func TestHTMLRefusesASelectorItCannotParse(t *testing.T) {
	t.Parallel()

	err := failBinaryNode(t, nodes.HTMLNodeType, map[string]any{
		"operation": "htmlExtractContents", "sourceType": "json",
		"html": htmlDocument, "cssSelector": "a:hover", "returnValue": "text",
	}, []workflow.Item{{}})
	if err == nil || !strings.Contains(err.Error(), "pseudo-classes") {
		t.Fatalf("error = %v, want the pseudo-class refusal", err)
	}
}

// --- Markdown ------------------------------------------------------------------

func TestMarkdownConvertsToHtml(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.MarkdownNodeType, map[string]any{
		"operation": "markdownToHtml",
		"markdown":  "# Title\n\nsome **bold**, *em*, `code` and a [link](https://example.test)\n\n- one\n- two\n",
	}, []workflow.Item{{}})
	html, _ := output[0][0].JSON["data"].(string)
	for needle := range map[string]bool{
		"<h1>Title</h1>": true, "<strong>bold</strong>": true, "<em>em</em>": true,
		"<code>code</code>": true, `<a href="https://example.test">link</a>`: true,
		"<ul>": true, "<li>one</li>": true,
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("html lacks %q: %s", needle, html)
		}
	}
}

func TestMarkdownConvertsHtmlBack(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.MarkdownNodeType, map[string]any{
		"operation": "htmlToMarkdown",
		"html":      `<h2>Title</h2><p>some <strong>bold</strong> and a <a href="https://example.test">link</a></p><ul><li>one</li><li>two</li></ul>`,
	}, []workflow.Item{{}})
	markdown, _ := output[0][0].JSON["data"].(string)
	for needle := range map[string]bool{
		"## Title": true, "**bold**": true, "[link](https://example.test)": true,
		"- one": true, "- two": true,
	} {
		if !strings.Contains(markdown, needle) {
			t.Errorf("markdown lacks %q: %s", needle, markdown)
		}
	}
}

// --- XML -----------------------------------------------------------------------

func TestXMLConvertsToJsonWithN8nDefaults(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.XMLNodeType, map[string]any{
		"mode": "xmlToJson", "xml": `<root><item id="1">first</item><item>second</item></root>`,
	}, []workflow.Item{{}})
	// Defaults: attributes ignored, same-name children collapsed into an
	// array, tag values kept as text ("first" is not a number).
	want := map[string]any{
		"root": map[string]any{"item": []any{"first", "second"}},
	}
	encoded, err := json.Marshal(output[0][0].JSON["data"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wantEncoded, _ := json.Marshal(want)
	if string(encoded) != string(wantEncoded) {
		t.Fatalf("data = %s, want %s", encoded, wantEncoded)
	}
}

func TestXMLKeepsAttributesAndParsesNumbersWhenAsked(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.XMLNodeType, map[string]any{
		"mode": "xmlToJson", "xml": `<row id="7"><count>3</count></row>`,
		"options": map[string]any{"ignoreAttributes": false, "mergeAttributes": true, "explicitArray": false},
	}, []workflow.Item{{}})
	data, _ := output[0][0].JSON["data"].(map[string]any)
	row, _ := data["row"].(map[string]any)
	// parseTagValue reaches attribute values too, the way xml2js does.
	if row["id"] != float64(7) || row["count"] != float64(3) {
		t.Fatalf("data = %#v, want the merged attribute and the parsed numbers", data)
	}
}

func TestXMLConvertsJsonBackAndRoundTrips(t *testing.T) {
	t.Parallel()

	document := map[string]any{"row": map[string]any{"@id": "7", "#text": "Ada"}}
	output, _ := runBinaryNode(t, nodes.XMLNodeType, map[string]any{
		"mode": "jsonToXml", "json": mustJSON(t, document),
	}, []workflow.Item{{}})
	xml, _ := output[0][0].JSON["data"].(string)
	if !strings.Contains(xml, `<row id="7">Ada</row>`) {
		t.Fatalf("xml = %q", xml)
	}

	// And the xml direction reads it back to the same document.
	back, _ := runBinaryNode(t, nodes.XMLNodeType, map[string]any{
		"mode": "xmlToJson", "xml": xml, "options": map[string]any{"ignoreAttributes": false, "mergeAttributes": false},
	}, []workflow.Item{{}})
	roundTripped, _ := back[0][0].JSON["data"].(map[string]any)
	row, _ := roundTripped["row"].(map[string]any)
	if row["@id"] != float64(7) || row["#text"] != "Ada" {
		t.Fatalf("round trip = %#v, want the attribute and text back", roundTripped)
	}
}

// --- Crypto --------------------------------------------------------------------

func TestCryptoHashesKnownValues(t *testing.T) {
	t.Parallel()

	// sha256("hello") — checked against the published digest, not a shape.
	output, _ := runBinaryNode(t, nodes.CryptoNodeType, map[string]any{
		"operation": "hash", "type": "SHA256", "inputFieldName": "value", "outputFieldName": "digest",
	}, []workflow.Item{{JSON: map[string]any{"value": "hello"}}})
	digest, _ := output[0][0].JSON["digest"].(string)
	if digest != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("digest = %q", digest)
	}

	base64Out, _ := runBinaryNode(t, nodes.CryptoNodeType, map[string]any{
		"operation": "hash", "type": "MD5", "inputFieldName": "value", "digestType": "base64",
	}, []workflow.Item{{JSON: map[string]any{"value": "hello"}}})
	if digest := base64Out[0][0].JSON["data"]; digest != "XUFAKrxLKna5cZ2REBfFkg==" {
		t.Fatalf("md5 base64 = %v", digest)
	}
}

func TestCryptoComputesAKnownHmac(t *testing.T) {
	t.Parallel()

	// HMAC-SHA256(key "secret", message "value") — the published value.
	output, _ := runBinaryNode(t, nodes.CryptoNodeType, map[string]any{
		"operation": "hmac", "hashType": "SHA256", "secret": "secret",
		"inputFieldName": "value", "outputFieldName": "mac",
	}, []workflow.Item{{JSON: map[string]any{"value": "value"}}})
	mac, _ := output[0][0].JSON["mac"].(string)
	if mac != "50e03ebe65be98bb8bf11ba2c892d54c079aca2b0d3b0162769c6d757a25434f" {
		t.Fatalf("hmac = %q", mac)
	}
}

func TestCryptoSignsAJwtThatParses(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.CryptoNodeType, map[string]any{
		"operation": "sign", "signingAlgorithm": "HS256", "secret": "shh",
		"inputFieldName": "claims",
	}, []workflow.Item{{JSON: map[string]any{"claims": map[string]any{"sub": "ada", "admin": true}}}})
	token, _ := output[0][0].JSON["data"].(string)
	parsed, err := jwt.Parse(token, func(token *jwt.Token) (any, error) { return []byte("shh"), nil })
	if err != nil || !parsed.Valid {
		t.Fatalf("parse the token %q: %v", token, err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["sub"] != "ada" || claims["admin"] != true {
		t.Fatalf("claims = %#v", claims)
	}
	if _, hasExpiry := claims["exp"]; !hasExpiry {
		t.Fatalf("claims = %#v, want an expiry a signing never omits", claims)
	}
}

func TestCryptoGeneratesAUniqueIDPerItem(t *testing.T) {
	t.Parallel()

	output, _ := runBinaryNode(t, nodes.CryptoNodeType, map[string]any{
		"operation": "uuid",
	}, []workflow.Item{{}, {}})
	first, _ := output[0][0].JSON["data"].(string)
	second, _ := output[0][1].JSON["data"].(string)
	if _, err := uuid.Parse(first); err != nil {
		t.Fatalf("first = %q, not a uuid: %v", first, err)
	}
	if first == second {
		t.Fatalf("both items drew %q, want one id each", first)
	}
}

// --- The family's definitions --------------------------------------------------

func TestTheFileFormatFamilyIsItsOwnTypes(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for name, wantCategory := range map[string]string{
		nodes.ConvertToFileNodeType: "Files",
		nodes.CompressionNodeType:   "Files",
		nodes.HTMLNodeType:          "Transform",
		nodes.MarkdownNodeType:      "Transform",
		nodes.XMLNodeType:           "Transform",
		nodes.CryptoNodeType:        "Transform",
		nodes.EditImageNodeType:     "Files",
	} {
		definition, found := registry.Get(name, workflow.V(1))
		if !found {
			t.Errorf("%s is not registered", name)
			continue
		}
		if definition.Category != wantCategory {
			t.Errorf("%s category = %q, want %q", name, definition.Category, wantCategory)
		}
		if definition.Icon == nil {
			t.Errorf("%s has no icon", name)
		}
		if definition.ExecutorID == "" {
			t.Errorf("%s has no executor", name)
		}
		if len(definition.Credentials) != 0 {
			t.Errorf("%s declares credentials %v, want none: the family is pure compute", name, definition.Credentials)
		}
	}
}

// A tiny red square, so the image tests in editimage_test.go have pixels to
// point at.
func redPNG(t *testing.T, size int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			picture.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatalf("encode the png: %v", err)
	}
	return buffer.Bytes()
}
