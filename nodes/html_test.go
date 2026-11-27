package nodes_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func runHTMLErr(t *testing.T, store engine.BinaryStore, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	output, err := flowExecutor(t, nodes.HTMLExecutorID).Execute(context.Background(),
		flowNode(t, nodes.HTMLNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{Binaries: store})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func extractionRows(rows ...map[string]any) map[string]any {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	return map[string]any{"values": list}
}

// extractHTML runs one extraction over a document held in the data field and
// returns the output item's JSON.
func extractHTML(t *testing.T, document string, options map[string]any, rows ...map[string]any) map[string]any {
	t.Helper()
	parameters := map[string]any{"operation": "extractHtmlContent", "sourceData": "json", "dataPropertyName": "data", "extractionValues": extractionRows(rows...)}
	if options != nil {
		parameters["options"] = options
	}
	items, err := runHTMLErr(t, nil, parameters, workflow.Item{JSON: map[string]any{"data": document}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	return items[0].JSON
}

const sampleHTML = `<html><head><title>Shop</title><style>.x{color:red}</style></head><body>
<h1 class="title">  Welcome   to
   the shop </h1>
<ul id="items">
  <li class="item" data-sku="a1"><a href="/a">Apple</a> <span class="price">$1</span></li>
  <li class="item" data-sku="b2"><a href="/b">Banana</a> <span class="price">$2</span></li>
  <li class="item gone" data-sku="c3"><a href="/c">Cherry</a> <span class="price">$3</span></li>
</ul>
<form><input id="q" name="q" value="search me"><textarea id="t">typed text</textarea>
<select id="s"><option value="x">X</option><option value="y" selected>Y</option></select></form>
<script>var secret = 1;</script>
</body></html>`

func TestHTMLExtractsTextHTMLAttributeAndValue(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil,
		map[string]any{"key": "title", "cssSelector": "h1.title", "returnValue": "text"},
		map[string]any{"key": "firstLink", "cssSelector": "li.item a", "returnValue": "attribute", "attribute": "href"},
		map[string]any{"key": "markup", "cssSelector": "li.item", "returnValue": "html"},
		map[string]any{"key": "query", "cssSelector": "#q", "returnValue": "value"},
		map[string]any{"key": "typed", "cssSelector": "#t", "returnValue": "value"},
		map[string]any{"key": "chosen", "cssSelector": "#s", "returnValue": "value"},
	)
	want := map[string]any{
		// Clean-up is on by default: the whitespace and line breaks collapse.
		"title":     "Welcome to the shop",
		"firstLink": "/a",
		"markup":    `<a href="/a">Apple</a> <span class="price">$1</span>`,
		"query":     "search me",
		"typed":     "typed text",
		"chosen":    "y",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %#v\nwant       %#v", got, want)
	}
}

func TestHTMLReturnArrayReadsOneValuePerMatch(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil,
		map[string]any{"key": "names", "cssSelector": "li.item a", "returnArray": true},
		map[string]any{"key": "skus", "cssSelector": "li.item", "returnValue": "attribute", "attribute": "data-sku", "returnArray": true},
		map[string]any{"key": "none", "cssSelector": ".missing", "returnArray": true},
	)
	want := map[string]any{
		"names": []any{"Apple", "Banana", "Cherry"},
		"skus":  []any{"a1", "b2", "c3"},
		"none":  []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %#v\nwant       %#v", got, want)
	}
}

func TestHTMLWithoutReturnArrayJoinsTextAndTakesTheFirstOfTheRest(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil,
		map[string]any{"key": "allText", "cssSelector": "li.item a"},
		map[string]any{"key": "firstHTML", "cssSelector": "li.item a", "returnValue": "html"},
		map[string]any{"key": "noText", "cssSelector": ".missing"},
		map[string]any{"key": "noHTML", "cssSelector": ".missing", "returnValue": "html"},
		map[string]any{"key": "noAttr", "cssSelector": "li.item", "returnValue": "attribute", "attribute": "nope"},
	)
	want := map[string]any{"allText": "AppleBananaCherry", "firstHTML": "Apple", "noText": "", "noHTML": nil, "noAttr": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %#v\nwant       %#v", got, want)
	}
}

func TestHTMLSupportsSelectorGroupsAndPseudoClasses(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil,
		map[string]any{"key": "both", "cssSelector": "h1, title", "returnArray": true},
		map[string]any{"key": "second", "cssSelector": "li:nth-child(2) a"},
		map[string]any{"key": "notGone", "cssSelector": "li.item:not(.gone) a", "returnArray": true},
		map[string]any{"key": "sku", "cssSelector": "li[data-sku=c3] a"},
	)
	want := map[string]any{
		"both":    []any{"Shop", "Welcome to the shop"},
		"second":  "Banana",
		"notGone": []any{"Apple", "Banana"},
		"sku":     "Cherry",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %#v\nwant       %#v", got, want)
	}
}

func TestHTMLSkipSelectorsLeaveElementsOutOfTheRead(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil,
		map[string]any{"key": "plain", "cssSelector": "li.item", "returnArray": true, "skipSelectors": ".price"},
		map[string]any{"key": "joined", "cssSelector": "li.item", "skipSelectors": ".price, a"},
		map[string]any{"key": "all", "cssSelector": "li.item", "returnArray": true},
	)
	want := map[string]any{
		"plain":  []any{"Apple", "Banana", "Cherry"},
		"joined": "",
		"all":    []any{"Apple $1", "Banana $2", "Cherry $3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extracted = %#v\nwant       %#v", got, want)
	}
}

func TestHTMLTextIgnoresScriptsAndStyles(t *testing.T) {
	t.Parallel()

	got := extractHTML(t, sampleHTML, nil, map[string]any{"key": "page", "cssSelector": "html"})
	text, _ := got["page"].(string)
	if strings.Contains(text, "secret") || strings.Contains(text, "color:red") || !strings.Contains(text, "Welcome to the shop") {
		t.Fatalf("text = %q, want the content without the script and style code", text)
	}
}

func TestHTMLTrimAndCleanUpOptions(t *testing.T) {
	t.Parallel()

	row := map[string]any{"key": "title", "cssSelector": "h1"}
	if got := extractHTML(t, sampleHTML, map[string]any{"cleanUpText": false, "trimValues": true}, row); got["title"] != "Welcome   to\n   the shop" {
		t.Errorf("trim only: title = %q", got["title"])
	}
	if got := extractHTML(t, sampleHTML, map[string]any{"cleanUpText": false, "trimValues": false}, row); got["title"] != "  Welcome   to\n   the shop " {
		t.Errorf("neither: title = %q", got["title"])
	}
}

func TestHTMLReadsADocumentFromABinaryFile(t *testing.T) {
	t.Parallel()

	store := binaryStore(t)
	reference := putFile(t, store, "page.html", "text/html", []byte(`<p class="x">from a file</p>`))
	items, err := runHTMLErr(t, store, map[string]any{
		"operation": "extractHtmlContent", "sourceData": "binary", "dataPropertyName": "page",
		"extractionValues": extractionRows(map[string]any{"key": "text", "cssSelector": ".x"}),
	}, workflow.Item{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"page": reference}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if items[0].JSON["text"] != "from a file" || items[0].Binary["page"].ID != reference.ID {
		t.Fatalf("item = %#v, want the text read and the file kept", items[0])
	}
	if _, err := runHTMLErr(t, nil, map[string]any{"operation": "extractHtmlContent", "sourceData": "binary",
		"extractionValues": extractionRows(map[string]any{"key": "a", "cssSelector": "p"})},
		workflow.Item{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": reference}}); err == nil {
		t.Errorf("no binary storage: want an error, got none")
	}
}

func TestHTMLExtractRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	rows := extractionRows(map[string]any{"key": "a", "cssSelector": "p"})
	cases := map[string]struct {
		parameters map[string]any
		item       workflow.Item
	}{
		"missing field": {map[string]any{"operation": "extractHtmlContent", "extractionValues": rows}, workflow.Item{JSON: map[string]any{"other": "<p/>"}}},
		"not text":      {map[string]any{"operation": "extractHtmlContent", "extractionValues": rows}, workflow.Item{JSON: map[string]any{"data": float64(1)}}},
		"no rows":       {map[string]any{"operation": "extractHtmlContent", "extractionValues": map[string]any{"values": []any{}}}, workflow.Item{JSON: map[string]any{"data": "<p/>"}}},
		"no key":        {map[string]any{"operation": "extractHtmlContent", "extractionValues": extractionRows(map[string]any{"cssSelector": "p"})}, workflow.Item{JSON: map[string]any{"data": "<p/>"}}},
		"bad selector":  {map[string]any{"operation": "extractHtmlContent", "extractionValues": extractionRows(map[string]any{"key": "a", "cssSelector": "p["})}, workflow.Item{JSON: map[string]any{"data": "<p/>"}}},
		"no attribute":  {map[string]any{"operation": "extractHtmlContent", "extractionValues": extractionRows(map[string]any{"key": "a", "cssSelector": "p", "returnValue": "attribute"})}, workflow.Item{JSON: map[string]any{"data": "<p/>"}}},
	}
	for name, tc := range cases {
		if _, err := runHTMLErr(t, nil, tc.parameters, tc.item); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestHTMLTemplateFillsExpressionsPerItemAndKeepsTheItem(t *testing.T) {
	t.Parallel()

	items, err := runHTMLErr(t, nil, map[string]any{
		"operation": "generateHtmlTemplate",
		"html":      map[string]any{"mode": "expression", "value": "<h1>Hello {{ $json.name }}</h1>"},
	}, workflow.Item{JSON: map[string]any{"name": "Ada"}}, workflow.Item{JSON: map[string]any{"name": "Grace"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(items) != 2 || items[0].JSON["html"] != "<h1>Hello Ada</h1>" || items[1].JSON["html"] != "<h1>Hello Grace</h1>" || items[0].JSON["name"] != "Ada" {
		t.Fatalf("items = %#v", items)
	}
}

func TestHTMLTableWritesOneRowPerItem(t *testing.T) {
	t.Parallel()

	items, err := runHTMLErr(t, nil, map[string]any{"operation": "convertToHtmlTable"},
		workflow.Item{JSON: map[string]any{"name": "Ada <b>", "age": float64(36)}},
		workflow.Item{JSON: map[string]any{"name": "Grace", "tags": []any{"a", "b"}, "paid": true}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(items) != 1 || len(items[0].JSON) != 1 {
		t.Fatalf("items = %#v, want one item holding the table", items)
	}
	want := "<table><thead><tr><th>age</th><th>name</th><th>paid</th><th>tags</th></tr></thead><tbody>" +
		"<tr><td>36</td><td>Ada &lt;b&gt;</td><td></td><td></td></tr>" +
		`<tr><td></td><td>Grace</td><td>true</td><td>[&#34;a&#34;,&#34;b&#34;]</td></tr>` +
		"</tbody></table>"
	if items[0].JSON["table"] != want {
		t.Fatalf("table =\n%v\nwant\n%s", items[0].JSON["table"], want)
	}
}

func TestHTMLTableOptions(t *testing.T) {
	t.Parallel()

	options := map[string]any{
		"capitalize": true, "customStyling": true, "caption": "People & places",
		"tableAttributes": `class="data" id="report"`, "headerAttributes": `scope="col"`,
		"rowAttributes": `data-kind="person"`, "cellAttributes": `align="left"`,
	}
	items, err := runHTMLErr(t, nil, map[string]any{"operation": "convertToHtmlTable", "options": options},
		workflow.Item{JSON: map[string]any{"name": "Ada"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := `<table class="data" id="report"><caption>People &amp; places</caption><thead><tr><th scope="col">Name</th></tr></thead>` +
		`<tbody><tr data-kind="person"><td align="left">Ada</td></tr></tbody></table>`
	if items[0].JSON["table"] != want {
		t.Fatalf("table =\n%v\nwant\n%s", items[0].JSON["table"], want)
	}

	// The attribute options apply only with custom styling on.
	options["customStyling"] = false
	items, _ = runHTMLErr(t, nil, map[string]any{"operation": "convertToHtmlTable", "options": options}, workflow.Item{JSON: map[string]any{"name": "Ada"}})
	if table, _ := items[0].JSON["table"].(string); strings.Contains(table, "class=") || !strings.Contains(table, "<th>Name</th>") {
		t.Fatalf("table = %s, want no attributes without custom styling", table)
	}
}

func TestHTMLTableRefusesAttributesThatBreakOutOfTheTag(t *testing.T) {
	t.Parallel()

	for name, attributes := range map[string]string{
		"closes the tag": `class="a"><script>alert(1)</script><x y="`,
		"event handler":  `onclick="alert(1)"`,
		"stray text":     `"`,
	} {
		_, err := runHTMLErr(t, nil, map[string]any{"operation": "convertToHtmlTable", "options": map[string]any{"customStyling": true, "tableAttributes": attributes}},
			workflow.Item{JSON: map[string]any{"a": "1"}})
		if err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestHTMLValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.HTMLNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "scrape"}}); err == nil || !strings.Contains(err.Error(), "scrape") {
		t.Errorf("Validate() of a bad operation = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operation": "convertToHtmlTable", "options": map[string]any{"zebra": true}}}); err == nil || !strings.Contains(err.Error(), "zebra") {
		t.Errorf("Validate() of an unknown option = %v", err)
	}
	bad := map[string]any{"operation": "extractHtmlContent", "extractionValues": extractionRows(map[string]any{"key": "a", "cssSelector": "div["})}
	if err := definition.Validate(workflow.Node{Parameters: bad}); err == nil || !strings.Contains(err.Error(), "div[") {
		t.Errorf("Validate() of a bad selector = %v, want it caught before the run", err)
	}
	good := map[string]any{"operation": "extractHtmlContent", "extractionValues": extractionRows(map[string]any{"key": "a", "cssSelector": "div > p"})}
	if err := definition.Validate(workflow.Node{Parameters: good}); err != nil {
		t.Errorf("Validate() of a good node = %v", err)
	}
}
