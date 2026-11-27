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

func runXMLErr(t *testing.T, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	output, err := flowExecutor(t, nodes.XMLExecutorID).Execute(context.Background(),
		flowNode(t, nodes.XMLNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func runXML(t *testing.T, parameters map[string]any, items ...workflow.Item) []workflow.Item {
	t.Helper()
	output, err := runXMLErr(t, parameters, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

// xmlToJSON parses one document with the given options and returns the item.
func xmlToJSON(t *testing.T, document string, options map[string]any) map[string]any {
	t.Helper()
	parameters := map[string]any{"mode": "xmlToJson", "dataPropertyName": "data"}
	if options != nil {
		parameters["options"] = options
	}
	items := runXML(t, parameters, workflow.Item{JSON: map[string]any{"data": document}})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	return items[0].JSON
}

func TestXMLToJSONUsesTheCommonConverterShape(t *testing.T) {
	t.Parallel()

	got := xmlToJSON(t, `<?xml version="1.0"?>
<order id="7" status="open">
  <customer>Ada</customer>
  <item sku="a">Pen</item>
  <item sku="b">Ink</item>
  <note/>
</order>`, nil)

	// Defaults: the root name kept, attributes merged into the element, a
	// repeated name becomes an array, a single child stays a value, and an
	// element with only text is that text.
	want := map[string]any{"order": map[string]any{
		"id": "7", "status": "open", "customer": "Ada", "note": "",
		"item": []any{
			map[string]any{"sku": "a", "_": "Pen"},
			map[string]any{"sku": "b", "_": "Ink"},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed = %#v\nwant   %#v", got, want)
	}
}

func TestXMLToJSONKeepsAttributesUnderTheAttributeKeyWhenNotMerged(t *testing.T) {
	t.Parallel()

	got := xmlToJSON(t, `<a id="1"><b x="y">text</b></a>`, map[string]any{"mergeAttrs": false})
	want := map[string]any{"a": map[string]any{
		"$": map[string]any{"id": "1"},
		"b": map[string]any{"$": map[string]any{"x": "y"}, "_": "text"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed = %#v\nwant   %#v", got, want)
	}

	got = xmlToJSON(t, `<a id="1"><b x="y">text</b></a>`, map[string]any{"mergeAttrs": false, "attrkey": "@", "charkey": "#"})
	if b := got["a"].(map[string]any)["b"].(map[string]any); b["#"] != "text" || b["@"] == nil {
		t.Fatalf("parsed = %#v, want the attribute and character keys renamed", got)
	}
}

func TestXMLToJSONOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		options  map[string]any
		want     map[string]any
	}{
		{
			name: "explicit array wraps a lone child", document: `<a><b>1</b></a>`,
			options: map[string]any{"explicitArray": true},
			want:    map[string]any{"a": map[string]any{"b": []any{"1"}}},
		},
		{
			name: "explicit root off returns the root's content", document: `<a><b>1</b></a>`,
			options: map[string]any{"explicitRoot": false},
			want:    map[string]any{"b": "1"},
		},
		{
			name: "ignore attributes drops them", document: `<a id="1"><b x="y">t</b></a>`,
			options: map[string]any{"ignoreAttrs": true},
			want:    map[string]any{"a": map[string]any{"b": "t"}},
		},
		{
			name: "trim", document: "<a><b>  padded  </b></a>",
			options: map[string]any{"trim": true},
			want:    map[string]any{"a": map[string]any{"b": "padded"}},
		},
		{
			name: "normalize collapses whitespace", document: "<a><b>one \n  two\t three</b></a>",
			options: map[string]any{"normalize": true},
			want:    map[string]any{"a": map[string]any{"b": "one two three"}},
		},
		{
			name: "normalize tags lower-cases names", document: `<A><B>1</B></A>`,
			options: map[string]any{"normalizeTags": true},
			want:    map[string]any{"a": map[string]any{"b": "1"}},
		},
		{
			name: "whitespace-only text is dropped by default", document: "<a>\n  <b>1</b>\n</a>",
			want: map[string]any{"a": map[string]any{"b": "1"}},
		},
		{
			name: "mixed text and children keeps the text under the character key", document: `<a>hello<b>1</b>world</a>`,
			want: map[string]any{"a": map[string]any{"_": "helloworld", "b": "1"}},
		},
		{
			name: "a prefix stays in the name", document: `<ns:a xmlns:ns="urn:x"><ns:b>1</ns:b></ns:a>`,
			want: map[string]any{"ns:a": map[string]any{"xmlns:ns": "urn:x", "ns:b": "1"}},
		},
		{
			name: "entities and CDATA are decoded", document: `<a><b>x &amp; y &lt;z&gt;</b><c><![CDATA[<raw & text>]]></c></a>`,
			want: map[string]any{"a": map[string]any{"b": "x & y <z>", "c": "<raw & text>"}},
		},
		{
			name: "an attribute and a child of one name make an array", document: `<a b="1"><b>2</b></a>`,
			want: map[string]any{"a": map[string]any{"b": []any{"1", "2"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := xmlToJSON(t, tc.document, tc.options); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parsed = %#v\nwant   %#v", got, tc.want)
			}
		})
	}
}

func TestXMLToJSONRefusesDocumentsThatAreNotWellFormed(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"unclosed":   `<a><b></a>`,
		"mismatched": `<a></b>`,
		"empty":      ``,
		"two roots":  `<a/><b/>`,
		"not xml":    `just text`,
	} {
		_, err := runXMLErr(t, map[string]any{"mode": "xmlToJson"}, workflow.Item{JSON: map[string]any{"data": document}})
		if err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
	_, err := runXMLErr(t, map[string]any{"mode": "xmlToJson"}, workflow.Item{JSON: map[string]any{"other": "<a/>"}})
	if err == nil || !strings.Contains(err.Error(), `"data"`) {
		t.Errorf("a missing field error = %v, want it to name the field", err)
	}
	_, err = runXMLErr(t, map[string]any{"mode": "xmlToJson"}, workflow.Item{JSON: map[string]any{"data": float64(3)}})
	if err == nil {
		t.Errorf("a non-text field: want an error, got none")
	}
}

func TestXMLToJSONAcceptsADeclaredNonUTF8Encoding(t *testing.T) {
	t.Parallel()

	got := xmlToJSON(t, `<?xml version="1.0" encoding="ISO-8859-1"?><a>café</a>`, nil)
	if got["a"] != "café" {
		t.Fatalf("parsed = %#v, want the text read as it was handed over", got)
	}
}

func TestXMLToJSONReplacesTheItemAndKeepsItsBinary(t *testing.T) {
	t.Parallel()

	attachment := map[string]workflow.BinaryRef{"file": {ID: "b1", FileName: "x.bin"}}
	items := runXML(t, map[string]any{"mode": "xmlToJson"},
		workflow.Item{JSON: map[string]any{"data": "<a>1</a>", "extra": true}, Binary: attachment})
	if !reflect.DeepEqual(items[0].JSON, map[string]any{"a": "1"}) || items[0].Binary["file"].ID != "b1" {
		t.Fatalf("item = %#v, want the parsed document alone and the attachment kept", items[0])
	}
}

func jsonToXML(t *testing.T, document map[string]any, options map[string]any) string {
	t.Helper()
	parameters := map[string]any{"mode": "jsonToxml", "dataPropertyName": "xml"}
	if options != nil {
		parameters["options"] = options
	}
	items := runXML(t, parameters, workflow.Item{JSON: document})
	text, _ := items[0].JSON["xml"].(string)
	if len(items[0].JSON) != 1 {
		t.Fatalf("item = %#v, want the XML field alone", items[0].JSON)
	}
	return text
}

func TestJSONToXMLWritesAPrettyDocumentWithTheSingleFieldAsRoot(t *testing.T) {
	t.Parallel()

	got := jsonToXML(t, map[string]any{"order": map[string]any{
		"$":        map[string]any{"id": "7"},
		"customer": "Ada & Co",
		"qty":      float64(3),
		"paid":     true,
		"note":     nil,
		"item":     []any{"Pen", "Ink"},
		"address":  map[string]any{"city": "Jakarta", "_": "home"},
	}}, nil)
	want := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<order id="7">
  <address>home
    <city>Jakarta</city>
  </address>
  <customer>Ada &amp; Co</customer>
  <item>Pen</item>
  <item>Ink</item>
  <note/>
  <paid>true</paid>
  <qty>3</qty>
</order>`
	if got != want {
		t.Fatalf("xml =\n%s\nwant\n%s", got, want)
	}
}

func TestJSONToXMLUsesTheRootNameForSeveralFields(t *testing.T) {
	t.Parallel()

	got := jsonToXML(t, map[string]any{"a": "1", "b": "2"}, map[string]any{"rootName": "data", "headless": true})
	want := "<data>\n  <a>1</a>\n  <b>2</b>\n</data>"
	if got != want {
		t.Fatalf("xml =\n%s\nwant\n%s", got, want)
	}
	// The default root name applies when none is chosen.
	if got := jsonToXML(t, map[string]any{"a": "1", "b": "2"}, map[string]any{"headless": true}); !strings.HasPrefix(got, "<root>") {
		t.Fatalf("xml = %s, want the default root", got)
	}
}

func TestJSONToXMLRenderAndCDATAOptions(t *testing.T) {
	t.Parallel()

	got := jsonToXML(t, map[string]any{"a": map[string]any{"b": "x < y", "c": "plain"}},
		map[string]any{"headless": true, "cdata": true, "renderOpts": map[string]any{"pretty": false}})
	want := "<a><b><![CDATA[x < y]]></b><c>plain</c></a>"
	if got != want {
		t.Fatalf("xml = %s, want %s", got, want)
	}

	got = jsonToXML(t, map[string]any{"a": map[string]any{"b": "1"}},
		map[string]any{"headless": true, "renderOpts": map[string]any{"indent": "\t", "newline": "\r\n"}})
	if got != "<a>\r\n\t<b>1</b>\r\n</a>" {
		t.Fatalf("xml = %q, want tab indent and CRLF", got)
	}

	got = jsonToXML(t, map[string]any{"a": map[string]any{"@": map[string]any{"k": `v"<`}, "#": "t"}},
		map[string]any{"headless": true, "attrkey": "@", "charkey": "#"})
	if got != `<a k="v&quot;&lt;">t</a>` {
		t.Fatalf("xml = %s, want escaped attribute and the renamed keys", got)
	}
}

func TestJSONToXMLRefusesAnInvalidElementName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"has space", "1starts", "a<b"} {
		_, err := runXMLErr(t, map[string]any{"mode": "jsonToxml"}, workflow.Item{JSON: map[string]any{"root": map[string]any{name: "x"}}})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%q: error = %v, want one naming it", name, err)
		}
	}
}

func TestXMLRoundTripsThroughJSON(t *testing.T) {
	t.Parallel()

	original := map[string]any{"library": map[string]any{
		"name": "Central",
		"book": []any{
			map[string]any{"id": "1", "_": "Dune"},
			map[string]any{"id": "2", "_": "Emma"},
		},
	}}
	document := jsonToXML(t, original, nil)
	// mergeAttrs reads the attribute back as a field and the text under _, so
	// the round trip needs the attribute key to be where the builder wrote it.
	parsed := xmlToJSON(t, document, map[string]any{"mergeAttrs": true})
	if !reflect.DeepEqual(parsed["library"].(map[string]any)["name"], "Central") {
		t.Fatalf("parsed = %#v", parsed)
	}
	books := parsed["library"].(map[string]any)["book"].([]any)
	if len(books) != 2 {
		t.Fatalf("books = %#v, want two", books)
	}
}

func TestXMLValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.XMLNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "yaml"}}); err == nil || !strings.Contains(err.Error(), "yaml") {
		t.Errorf("Validate() of a bad mode = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"options": map[string]any{"strict": true}}}); err == nil || !strings.Contains(err.Error(), "strict") {
		t.Errorf("Validate() of an unknown option = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "xmlToJson", "options": map[string]any{"explicitArray": true, "async": false}}}); err != nil {
		t.Errorf("Validate() of a good node = %v", err)
	}
}
