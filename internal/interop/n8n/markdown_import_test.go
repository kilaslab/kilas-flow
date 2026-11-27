package n8n_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestMarkdownOptionsMatchTheNodePack(t *testing.T) {
	t.Parallel()

	for _, key := range nodes.MarkdownToHTMLOptionKeys {
		if !n8n.MarkdownOptionSupported("markdownToHtml", key) {
			t.Errorf("the node implements Markdown to HTML option %q but the importer would refuse it", key)
		}
	}
	for _, key := range nodes.MarkdownToMarkdownOptionKeys {
		if !n8n.MarkdownOptionSupported("htmlToMarkdown", key) {
			t.Errorf("the node implements HTML to Markdown option %q but the importer would refuse it", key)
		}
	}
}

func TestMarkdownToHTMLImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.markdown", 1,
		`{"mode":"markdownToHtml","markdown":"={{ $json.text }}","destinationKey":"body","options":{"tables":true,"noHeaderId":true}}`)
	if imported.Type != n8n.MarkdownNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.MarkdownNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
	// n8n leaves strikethrough off unless asked; the import writes that out.
	options, _ := imported.Parameters["options"].(map[string]any)
	if options["strikethrough"] != false || options["tables"] != true || options["noHeaderId"] != true {
		t.Fatalf("options = %#v, want n8n's defaults written out beside the chosen ones", options)
	}

	output := executeImported(t, imported, nodes.MarkdownExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{
		"text": "# Hi\n\n~~old~~ | a\n\n| a | b |\n|---|---|\n| 1 | 2 |",
	}}}})
	body, _ := output[0][0].JSON["body"].(string)
	if !strings.Contains(body, "<h1>Hi</h1>") || !strings.Contains(body, "<table>") || strings.Contains(body, "<del>") {
		t.Fatalf("body = %s, want a table, no strikethrough and no heading id", body)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.markdown" || returned.TypeVersion != 1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.markdown v1", returned.Type, returned.TypeVersion)
	}
	if returned.Parameters["markdown"] != "={{ $json.text }}" || returned.Parameters["destinationKey"] != "body" || returned.Parameters["mode"] != "markdownToHtml" {
		t.Fatalf("exported parameters = %#v", returned.Parameters)
	}
}

func TestHTMLToMarkdownImportsWithN8NsOwnStyleDefaults(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.markdown", 1, `{"mode":"htmlToMarkdown","html":"<h1>Title</h1><ul><li>a</li></ul><p><em>x</em></p>"}`)
	if imported.Type != n8n.MarkdownNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.MarkdownNodeType)
	}
	// Code blocks are written fenced here and indented in n8n: said, not blocking.
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}
	foundNote := false
	for _, issue := range result.Unsupported {
		if issue.NodeName == "Legacy" && issue.Severity == n8n.SeverityLossy && strings.Contains(issue.Reason, "fenced") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("issues = %#v, want a lossy note about code block style", result.Unsupported)
	}

	output := executeImported(t, imported, nodes.MarkdownExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{}}}})
	got, _ := output[0][0].JSON["data"].(string)
	want := "Title\n=====\n\n* a\n\n_x_"
	if got != want {
		t.Fatalf("markdown = %q, want %q (n8n's setext, * and _ defaults)", got, want)
	}

	// An explicit fenced style is carried and raises no note.
	fenced, fencedResult := importNode(t, "n8n-nodes-base.markdown", 1, `{"mode":"htmlToMarkdown","html":"x","options":{"codeBlockStyle":"fenced","headingStyle":"atx"}}`)
	options, _ := fenced.Parameters["options"].(map[string]any)
	if options["codeBlockStyle"] != "fenced" || options["headingStyle"] != "atx" {
		t.Fatalf("options = %#v", options)
	}
	for _, issue := range fencedResult.Unsupported {
		if issue.NodeName == "Legacy" {
			t.Fatalf("issue %#v, want none for fenced code blocks", issue)
		}
	}
	if returned := exportedLegacyNode(t, result); !reflect.DeepEqual(returned.Parameters["mode"], "htmlToMarkdown") {
		t.Fatalf("exported parameters = %#v", returned.Parameters)
	}
}

func TestMarkdownRefusesAnOptionItDoesNotImplement(t *testing.T) {
	t.Parallel()

	for name, parameters := range map[string]string{
		"emoji":       `{"mode":"markdownToHtml","markdown":"x","options":{"emoji":true}}`,
		"underline":   `{"markdown":"x","options":{"underline":true}}`,
		"keep":        `{"mode":"htmlToMarkdown","html":"x","options":{"keep":"span"}}`,
		"textReplace": `{"mode":"htmlToMarkdown","html":"x","options":{"textReplace":{"values":[]}}}`,
	} {
		imported, result := importNode(t, "n8n-nodes-base.markdown", 1, parameters)
		if imported.Type != n8n.UnsupportedNodeType {
			t.Errorf("%s: imported as %q, want the unsupported placeholder", name, imported.Type)
			continue
		}
		blocking := blockingFor(result, "Legacy")
		if len(blocking) == 0 || !strings.Contains(blocking[0].Reason, name) {
			t.Errorf("%s: blocking = %#v, want a reason naming the option", name, blocking)
		}
	}
	// An unsupported option that is switched off changes nothing.
	imported, _ := importNode(t, "n8n-nodes-base.markdown", 1, `{"markdown":"x","options":{"emoji":false}}`)
	if imported.Type != n8n.MarkdownNodeType {
		t.Fatalf("imported as %q, want the node when the unsupported option is off", imported.Type)
	}
}

func TestMarkdownExportSaysWhenRawHTMLCannotBeTurnedOff(t *testing.T) {
	t.Parallel()

	_, result := importNode(t, "n8n-nodes-base.markdown", 1, `{"markdown":"x","options":{}}`)
	for index := range result.Document.Nodes {
		if result.Document.Nodes[index].Name == "Legacy" {
			result.Document.Nodes[index].Parameters["options"].(map[string]any)["allowRawHtml"] = false
		}
	}
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	said := false
	for _, issue := range exported.Lossy {
		if strings.Contains(issue.Reason, "raw HTML") {
			said = true
		}
	}
	if !said {
		t.Fatalf("lossy = %#v, want a note about raw HTML", exported.Lossy)
	}
}
