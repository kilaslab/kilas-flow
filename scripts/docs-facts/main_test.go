package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

const (
	testBegin = "<!-- generated:test -->"
	testEnd   = "<!-- /generated:test -->"
)

// page writes a one-fact tree: a page with a stale block between the markers.
func page(t *testing.T, body string) (root string, list []fact) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{"# Title", "", testBegin, body, testEnd, "", "prose after"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "page.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	return root, []fact{{
		name: "test", path: "docs/page.md", begin: testBegin, end: testEnd,
		body: func() (string, error) { return "fresh sentence.", nil },
	}}
}

func read(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "docs", "page.md"))
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestApplyWritesTheBodyBetweenTheMarkersAndNothingElse(t *testing.T) {
	root, list := page(t, "stale sentence.")

	if err := apply(root, list, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	want := strings.Join([]string{"# Title", "", testBegin, "fresh sentence.", testEnd, "", "prose after"}, "\n")
	if got := read(t, root); got != want {
		t.Fatalf("page =\n%q\nwant\n%q", got, want)
	}
}

func TestCheckNamesTheFileAndTheFirstDifferingLineAndWritesNothing(t *testing.T) {
	root, list := page(t, "stale sentence.")
	before := read(t, root)

	err := apply(root, list, true, &bytes.Buffer{})
	if err == nil {
		t.Fatal("apply(check) returned no error for a stale page")
	}
	for _, want := range []string{"docs/page.md", "first at line 4", "stale sentence.", "fresh sentence.", "make generate-docs-facts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("apply(check) error = %q, want it to hold %q", err, want)
		}
	}
	if after := read(t, root); after != before {
		t.Fatalf("apply(check) rewrote the page:\n%q", after)
	}
}

func TestCheckPassesOnceTheFactsAreWritten(t *testing.T) {
	root, list := page(t, "stale sentence.")
	if err := apply(root, list, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if err := apply(root, list, true, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply(check) after a write = %v, want it clean", err)
	}
}

func TestApplyRefusesAPageThatLostItsMarkers(t *testing.T) {
	root, list := page(t, "stale sentence.")
	list[0].begin = "<!-- generated:gone -->"

	err := apply(root, list, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "docs/page.md") || !strings.Contains(err.Error(), "carries no") {
		t.Fatalf("apply() error = %v, want the page named and the missing marker said", err)
	}
}

// Every stale page is reported in one run, so a contributor who regenerates
// does not have to discover them one failure at a time.
func TestCheckReportsEveryStalePage(t *testing.T) {
	root, list := page(t, "stale sentence.")
	second := filepath.Join(root, "docs", "other.md")
	if err := os.WriteFile(second, []byte(testBegin+"\nold\n"+testEnd), 0o644); err != nil {
		t.Fatal(err)
	}
	list = append(list, fact{name: "test", path: "docs/other.md", begin: testBegin, end: testEnd,
		body: func() (string, error) { return "new", nil }})

	err := apply(root, list, true, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "docs/page.md") || !strings.Contains(err.Error(), "docs/other.md") {
		t.Fatalf("apply(check) error = %v, want both pages named", err)
	}
}

// The committed pages are the real subject: this is the same comparison
// `make generate-docs-facts-check` makes, run by `go test ./...` so a node added
// in one change cannot reach main with the docs' size untouched.
func TestTheCommittedPagesHoldTheGeneratedFacts(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	list, err := facts()
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}
	if len(list) == 0 {
		t.Fatal("facts() lists nothing")
	}
	if err := apply(root, list, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
