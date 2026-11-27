package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func runMarkdownErr(t *testing.T, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	output, err := flowExecutor(t, nodes.MarkdownExecutorID).Execute(context.Background(),
		flowNode(t, nodes.MarkdownNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func markdownToHTML(t *testing.T, source string, options map[string]any) string {
	t.Helper()
	parameters := map[string]any{"mode": "markdownToHtml", "markdown": source}
	if options != nil {
		parameters["options"] = options
	}
	items, err := runMarkdownErr(t, parameters)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	result, _ := items[0].JSON["data"].(string)
	return result
}

func htmlToMarkdown(t *testing.T, source string, options map[string]any) string {
	t.Helper()
	parameters := map[string]any{"mode": "htmlToMarkdown", "html": source}
	if options != nil {
		parameters["options"] = options
	}
	items, err := runMarkdownErr(t, parameters)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	result, _ := items[0].JSON["data"].(string)
	return result
}

func TestMarkdownToHTMLRendersTheCommonConstructs(t *testing.T) {
	t.Parallel()

	got := markdownToHTML(t, "# Title\n\nSome *emphasis*, **strong**, `code` and a [link](https://example.com).\n\n- one\n- two\n\n```go\nx := 1\n```", nil)
	for _, want := range []string{
		`<h1 id="title">Title</h1>`,
		`<em>emphasis</em>`, `<strong>strong</strong>`, `<code>code</code>`,
		`<a href="https://example.com">link</a>`,
		`<li>one</li>`, `<li>two</li>`,
		`<pre><code class="language-go">x := 1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("html = %s\nwant it to contain %s", got, want)
		}
	}
}

func TestMarkdownToHTMLOptions(t *testing.T) {
	t.Parallel()

	table := "| a | b |\n|---|---|\n| 1 | 2 |"
	if got := markdownToHTML(t, table, nil); !strings.Contains(got, "<table>") {
		t.Errorf("tables are on by default: html = %s", got)
	}
	if got := markdownToHTML(t, table, map[string]any{"tables": false}); strings.Contains(got, "<table>") {
		t.Errorf("tables off: html = %s", got)
	}
	if got := markdownToHTML(t, "~~gone~~", nil); !strings.Contains(got, "<del>gone</del>") {
		t.Errorf("strikethrough: html = %s", got)
	}
	if got := markdownToHTML(t, "- [x] done\n- [ ] todo", nil); !strings.Contains(got, `type="checkbox"`) {
		t.Errorf("task list: html = %s", got)
	}
	if got := markdownToHTML(t, "a\nb", nil); strings.Contains(got, "<br") {
		t.Errorf("a single line break stays a space by default: html = %s", got)
	}
	if got := markdownToHTML(t, "a\nb", map[string]any{"simpleLineBreaks": true}); !strings.Contains(got, "<br") {
		t.Errorf("simple line breaks: html = %s", got)
	}
	if got := markdownToHTML(t, "see https://example.com now", nil); strings.Contains(got, "<a ") {
		t.Errorf("a bare address is not linked by default: html = %s", got)
	}
	if got := markdownToHTML(t, "see https://example.com now", map[string]any{"simplifiedAutoLink": true}); !strings.Contains(got, `<a href="https://example.com">`) {
		t.Errorf("simplified auto link: html = %s", got)
	}
	if got := markdownToHTML(t, "# Title", map[string]any{"noHeaderId": true}); got != "<h1>Title</h1>" {
		t.Errorf("no header id: html = %s", got)
	}
	if got := markdownToHTML(t, "[x](https://example.com)", map[string]any{"openLinksInNewWindow": true}); !strings.Contains(got, `target="_blank"`) || !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Errorf("open links in a new window: html = %s", got)
	}
	got := markdownToHTML(t, "hi", map[string]any{"completeHTMLDocument": true})
	if !strings.HasPrefix(got, "<!DOCTYPE html>") || !strings.Contains(got, "<p>hi</p>") || !strings.HasSuffix(got, "</html>") {
		t.Errorf("complete document: html = %s", got)
	}
}

func TestMarkdownToHTMLKeepsRawHTMLUnlessTold(t *testing.T) {
	t.Parallel()

	source := "line<br>break <script>alert(1)</script>"
	if got := markdownToHTML(t, source, nil); !strings.Contains(got, "<br>") {
		t.Errorf("raw HTML is kept as Markdown specifies: html = %s", got)
	}
	got := markdownToHTML(t, source, map[string]any{"allowRawHtml": false})
	if strings.Contains(got, "<script>") || strings.Contains(got, "<br>") {
		t.Errorf("raw HTML off: html = %s, want it replaced", got)
	}
}

func TestMarkdownWritesBesideTheRestOfTheItemAndHonoursDotNotation(t *testing.T) {
	t.Parallel()

	items, err := runMarkdownErr(t, map[string]any{"mode": "markdownToHtml", "markdown": "*x*", "destinationKey": "page.body"},
		workflow.Item{JSON: map[string]any{"keep": "me"}, Binary: map[string]workflow.BinaryRef{"f": {ID: "b"}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	page, _ := items[0].JSON["page"].(map[string]any)
	if page["body"] != "<p><em>x</em></p>" || items[0].JSON["keep"] != "me" || items[0].Binary["f"].ID != "b" {
		t.Fatalf("item = %#v", items[0])
	}
}

func TestMarkdownConvertsASourceResolvedFromEachItem(t *testing.T) {
	t.Parallel()

	items, err := runMarkdownErr(t, map[string]any{
		"mode":     "markdownToHtml",
		"markdown": map[string]any{"mode": "expression", "value": "**{{ $json.name }}**"},
	},
		workflow.Item{JSON: map[string]any{"name": "Ada"}}, workflow.Item{JSON: map[string]any{"name": "Grace"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if items[0].JSON["data"] != "<p><strong>Ada</strong></p>" || items[1].JSON["data"] != "<p><strong>Grace</strong></p>" {
		t.Fatalf("items = %#v", items)
	}
}

func TestHTMLToMarkdownConvertsTheCommonConstructs(t *testing.T) {
	t.Parallel()

	got := htmlToMarkdown(t, `<h1>Title</h1><p>Some <em>emphasis</em>, <strong>strong</strong> and a <a href="https://example.com">link</a>.</p><ul><li>one</li><li>two</li></ul><pre><code>x := 1</code></pre><del>gone</del>`, nil)
	for _, want := range []string{"# Title", "*emphasis*", "**strong**", "[link](https://example.com)", "- one", "- two", "```", "x := 1", "~~gone~~"} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown = %s\nwant it to contain %s", got, want)
		}
	}
}

func TestHTMLToMarkdownConvertsATable(t *testing.T) {
	t.Parallel()

	got := htmlToMarkdown(t, `<table><thead><tr><th>a</th><th>b</th></tr></thead><tbody><tr><td>1</td><td>2</td></tr></tbody></table>`, nil)
	if !strings.Contains(got, "| a") || !strings.Contains(got, "| 1") || !strings.Contains(got, "---") {
		t.Errorf("markdown = %s, want a pipe table", got)
	}
}

func TestHTMLToMarkdownOptions(t *testing.T) {
	t.Parallel()

	if got := htmlToMarkdown(t, "<h1>Title</h1>", map[string]any{"headingStyle": "setext"}); !strings.Contains(got, "Title\n=") {
		t.Errorf("setext heading: markdown = %q", got)
	}
	if got := htmlToMarkdown(t, "<ul><li>a</li></ul>", map[string]any{"bulletMarker": "*"}); !strings.HasPrefix(got, "* a") {
		t.Errorf("bullet marker: markdown = %q", got)
	}
	if got := htmlToMarkdown(t, "<p><em>x</em></p>", map[string]any{"emDelimiter": "_"}); got != "_x_" {
		t.Errorf("em delimiter: markdown = %q", got)
	}
	if got := htmlToMarkdown(t, "<p><strong>x</strong></p>", map[string]any{"strongDelimiter": "__"}); got != "__x__" {
		t.Errorf("strong delimiter: markdown = %q", got)
	}
	if got := htmlToMarkdown(t, "<pre><code>x</code></pre>", map[string]any{"fence": "~~~"}); !strings.Contains(got, "~~~") {
		t.Errorf("fence: markdown = %q", got)
	}
	if got := htmlToMarkdown(t, "<p>a</p><hr><p>b</p>", map[string]any{"hr": "---"}); !strings.Contains(got, "\n---\n") {
		t.Errorf("horizontal rule: markdown = %q", got)
	}
}

func TestMarkdownRefusesAChoiceItCannotHonour(t *testing.T) {
	t.Parallel()

	cases := []map[string]any{
		{"mode": "htmlToMarkdown", "html": "<p>x</p>", "options": map[string]any{"codeBlockStyle": "indented"}},
		{"mode": "htmlToMarkdown", "html": "<p>x</p>", "options": map[string]any{"headingStyle": "fancy"}},
		{"mode": "yaml"},
	}
	for _, parameters := range cases {
		if _, err := runMarkdownErr(t, parameters); err == nil {
			t.Errorf("%v: want an error, got none", parameters)
		}
	}
}

func TestMarkdownValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.MarkdownNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "pdf"}}); err == nil || !strings.Contains(err.Error(), "pdf") {
		t.Errorf("Validate() of a bad mode = %v", err)
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "markdownToHtml", "options": map[string]any{"emoji": true}}}); err == nil || !strings.Contains(err.Error(), "emoji") {
		t.Errorf("Validate() of an unknown option = %v", err)
	}
	// An option of the other mode is not an option of this one.
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "markdownToHtml", "options": map[string]any{"headingStyle": "atx"}}}); err == nil {
		t.Errorf("Validate() of another mode's option = nil, want an error")
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"mode": "htmlToMarkdown", "options": map[string]any{"headingStyle": "atx"}}}); err != nil {
		t.Errorf("Validate() of a good node = %v", err)
	}
}
