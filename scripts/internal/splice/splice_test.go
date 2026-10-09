package splice_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

const (
	begin = "<!-- generated:thing -->"
	end   = "<!-- /generated:thing -->"
)

func lines(parts ...string) string { return strings.Join(parts, "\n") }

// A generator writes into a file people also edit, so the two ways a splice can
// go wrong are refusing to write and writing the wrong place.
func TestReplaceRefusesADocumentWithoutOneUnambiguousSection(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     string
	}{
		{"no markers", lines("## Heading", "", "prose"), "carries no " + begin},
		{"begin without end", lines("prose", begin, "stale"), "carries no " + end},
		{"end without begin", lines("prose", end), "carries no " + begin},
		{"two sections", lines(begin, end, begin, end), "carries " + begin + " twice"},
		{"two ends", lines(begin, end, end), "carries " + end + " twice"},
		{"end before begin", lines(end, "stale", begin), end + " precedes " + begin},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := splice.Replace(testCase.document, begin, end, "section")
			if err == nil {
				t.Fatalf("Replace(%q) returned no error, want %q", testCase.document, testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Replace(%q) failed with %q, want %q", testCase.document, err, testCase.want)
			}
		})
	}
}

// Replace swaps the markers and everything between them for the section, and
// leaves every other line, blank runs included, exactly as it was: a generator
// that tidied the rest of a hand-written page would turn every run into a diff
// nobody asked for.
func TestReplaceTouchesOnlyTheSectionItWasGiven(t *testing.T) {
	document := lines("intro", "", "", "double blank above stays", begin, "stale", end, "outro")
	section := lines(begin, "fresh", end)

	got, err := splice.Replace(document, begin, end, section)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	want := lines("intro", "", "", "double blank above stays", begin, "fresh", end, "outro")
	if got != want {
		t.Fatalf("Replace() =\n%q\nwant\n%q", got, want)
	}
}

// Fill is Replace for a caller that has a body and not the markers: it keeps the
// page's own marker lines, so the section it writes can never lose them.
func TestFillKeepsTheMarkersAndReplacesTheBody(t *testing.T) {
	document := lines("intro", begin, "stale one", "stale two", end, "outro")

	got, err := splice.Fill(document, begin, end, lines("fresh one", "fresh two"))
	if err != nil {
		t.Fatalf("Fill() error = %v", err)
	}
	want := lines("intro", begin, "fresh one", "fresh two", end, "outro")
	if got != want {
		t.Fatalf("Fill() =\n%q\nwant\n%q", got, want)
	}
	again, err := splice.Fill(got, begin, end, lines("fresh one", "fresh two"))
	if err != nil || again != got {
		t.Fatalf("Fill() is not idempotent: %q, %v", again, err)
	}
}

func TestCollapseBlankLinesLeavesOneBlankLinePerRun(t *testing.T) {
	got := strings.Join(splice.CollapseBlankLines([]string{"a", "", "", " ", "b", "", "c"}), "|")
	if want := "a||b||c"; got != want {
		t.Fatalf("CollapseBlankLines() = %q, want %q", got, want)
	}
}

func TestStaleSummaryNamesTheFirstDifferingLine(t *testing.T) {
	summary := splice.StaleSummary("one\ntwo\nthree", "one\n2\nthree\nfour")
	for _, want := range []string{"2 line(s) differ", "first at line 2", `committed "two"`, `rendered "2"`} {
		if !strings.Contains(summary, want) {
			t.Errorf("StaleSummary() = %q, want it to hold %q", summary, want)
		}
	}
}

func TestRepoRootIsTheDirectoryHoldingGoMod(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("RepoRoot() = %q, which holds no go.mod: %v", root, err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "internal", "splice")); err != nil {
		t.Fatalf("RepoRoot() = %q is not this repository's root: %v", root, err)
	}
}
