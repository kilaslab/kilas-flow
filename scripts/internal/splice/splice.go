// Package splice is what the repository's document generators share: replacing
// a marked section of a hand-written file, saying how a stale file differs from
// what would be written, and finding the repository root from anywhere.
//
// A generated section is delimited by two marker lines (HTML comments, so a
// markdown renderer drops them and a reader of the source sees what to run).
// The generators refuse to run when a marker is absent or ambiguous rather than
// guessing where a section ends: a document whose section silently moved to the
// end is a document nobody re-reads.
package splice

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Replace swaps the lines from the begin marker to the end marker, both
// included, for section. The section carries its own markers when it should
// keep them; Fill is the form that does so on the caller's behalf.
//
// Every other line is left exactly as it was.
func Replace(document, begin, end, section string) (string, error) {
	lines := strings.Split(document, "\n")
	first, last, err := locate(lines, begin, end)
	if err != nil {
		return "", err
	}

	spliced := make([]string, 0, len(lines))
	spliced = append(spliced, lines[:first]...)
	spliced = append(spliced, strings.Split(section, "\n")...)
	spliced = append(spliced, lines[last+1:]...)

	return strings.Join(spliced, "\n"), nil
}

// Fill replaces what lies between the two markers with body and keeps the
// marker lines themselves, so a section can be rewritten any number of times
// and still be found.
func Fill(document, begin, end, body string) (string, error) {
	return Replace(document, begin, end, begin+"\n"+body+"\n"+end)
}

// locate finds the one begin line and the one end line, in that order.
func locate(lines []string, begin, end string) (first, last int, err error) {
	first, last = -1, -1
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case begin:
			if first >= 0 {
				return 0, 0, fmt.Errorf("the document carries %s twice", begin)
			}
			first = index
		case end:
			if last >= 0 {
				return 0, 0, fmt.Errorf("the document carries %s twice", end)
			}
			last = index
		}
	}
	switch {
	case first < 0:
		return 0, 0, fmt.Errorf("the document carries no %s marker", begin)
	case last < 0:
		return 0, 0, fmt.Errorf("the document carries no %s marker", end)
	case last < first:
		return 0, 0, fmt.Errorf("%s precedes %s", end, begin)
	}

	return first, last, nil
}

// CollapseBlankLines collapses each run of blank lines into one. A generator
// that owns a whole document uses it after Replace, to undo the doubled blank
// line a section that already ended in one introduces; one that shares a page
// with prose leaves the prose alone and does not call it.
func CollapseBlankLines(lines []string) []string {
	collapsed := make([]string, 0, len(lines))
	for index, line := range lines {
		if index > 0 && strings.TrimSpace(line) == "" && strings.TrimSpace(lines[index-1]) == "" {
			continue
		}
		collapsed = append(collapsed, line)
	}

	return collapsed
}

// StaleSummary is the short diff a stale file deserves: where it first diverges
// and how many lines do, rather than both documents twice.
func StaleSummary(committed, rendered string) string {
	committedLines := strings.Split(committed, "\n")
	renderedLines := strings.Split(rendered, "\n")

	first, differing := -1, 0
	for index := 0; index < len(committedLines) || index < len(renderedLines); index++ {
		var have, want string
		if index < len(committedLines) {
			have = committedLines[index]
		}
		if index < len(renderedLines) {
			want = renderedLines[index]
		}
		if have == want {
			continue
		}
		differing++
		if first < 0 {
			first = index + 1
		}
	}

	return fmt.Sprintf("%d line(s) differ, first at line %d: committed %q, rendered %q",
		differing, first, lineAt(committedLines, first), lineAt(renderedLines, first))
}

// lineAt reads a 1-based line, or "" past the end.
func lineAt(lines []string, number int) string {
	if number < 1 || number > len(lines) {
		return ""
	}

	return lines[number-1]
}

// RepoRoot is the repository root, found from this file's own location: a
// generator runs from anywhere, including a Makefile target invoked from a
// subdirectory.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate the repository root")
	}

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("locate the repository root above %s: no go.mod", file)
		}
		dir = parent
	}
}
