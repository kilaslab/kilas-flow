package main

// BUG-b4cb1c: two pages stated the size of the node catalogue and called it
// measured, and the numbers were twelve types stale by the time anyone checked.
// The size is now generated: scripts/docs-facts writes one sentence, rendered by
// internal/shippedcatalogue from the registrations a default install makes,
// between two markers on each page, and this test fails when a page's block is
// not that sentence. A node added in one change moves the count in the same
// change, because the one command that fixes the pages is a make target.

import (
	"os"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
)

const (
	catalogueSizeBegin = "<!-- generated:catalogue-size -->"
	catalogueSizeEnd   = "<!-- /generated:catalogue-size -->"
)

// generatedBlock is the text between a page's two markers, trimmed. ok is false
// when the page does not carry exactly one well-ordered pair.
func generatedBlock(page, begin, end string) (block string, ok bool) {
	if strings.Count(page, begin) != 1 || strings.Count(page, end) != 1 {
		return "", false
	}
	from := strings.Index(page, begin) + len(begin)
	to := strings.Index(page, end)
	if to < from {
		return "", false
	}

	return strings.TrimSpace(page[from:to]), true
}

func TestTheDocumentedCatalogueSizeIsTheShippedOne(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("shippedcatalogue.Register() error = %v", err)
	}
	want := shippedcatalogue.RenderSentence(shippedcatalogue.Count(registry))

	for _, page := range []string{
		"../../docs/src/content/docs/concepts/node-registry.md",
		"../../docs/src/content/docs/start/what-kilasflow-is.md",
	} {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		block, ok := generatedBlock(string(raw), catalogueSizeBegin, catalogueSizeEnd)
		if !ok {
			t.Errorf("%s does not carry exactly one %s ... %s block; the catalogue's size is generated, "+
				"so keep the markers and run `make generate-docs-facts`", page, catalogueSizeBegin, catalogueSizeEnd)

			continue
		}
		if block != want {
			t.Errorf("%s states the catalogue's size as\n  %q\nthe shipped catalogue is\n  %q\nrun `make generate-docs-facts`",
				page, block, want)
		}
	}
}

func TestGeneratedBlockRefusesAnAmbiguousPage(t *testing.T) {
	for name, page := range map[string]string{
		"no markers":      "prose",
		"begin only":      "a\n" + catalogueSizeBegin + "\nx",
		"end before":      catalogueSizeEnd + "\nx\n" + catalogueSizeBegin,
		"two sections":    catalogueSizeBegin + "\nx\n" + catalogueSizeEnd + "\n" + catalogueSizeBegin + "\ny\n" + catalogueSizeEnd,
		"end without any": catalogueSizeEnd,
	} {
		if block, ok := generatedBlock(page, catalogueSizeBegin, catalogueSizeEnd); ok {
			t.Errorf("%s: generatedBlock() = %q, want it refused", name, block)
		}
	}
	if block, ok := generatedBlock("a\n"+catalogueSizeBegin+"\n  sentence.  \n"+catalogueSizeEnd+"\nb", catalogueSizeBegin, catalogueSizeEnd); !ok || block != "sentence." {
		t.Errorf("generatedBlock() = %q, %v, want the trimmed text between the markers", block, ok)
	}
}
