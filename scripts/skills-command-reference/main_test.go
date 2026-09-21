package main

import (
	"strings"
	"testing"
)

// The generator writes into a skill an agent loads, so the two ways a splice can
// go wrong are refusing to write and writing the wrong place.
func TestSpliceRefusesADocumentWithoutOneUnambiguousSection(t *testing.T) {
	document := func(lines ...string) string { return strings.Join(lines, "\n") }

	cases := []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "no markers",
			document: document("## Decision tree", "", "prose"),
			want:     "carries no " + beginMarker,
		},
		{
			name:     "begin without end",
			document: document("prose", beginMarker, "stale"),
			want:     "carries no " + endMarker,
		},
		{
			name:     "end without begin",
			document: document("prose", endMarker),
			want:     "carries no " + beginMarker,
		},
		{
			name:     "two sections",
			document: document(beginMarker, endMarker, beginMarker, endMarker),
			want:     "carries " + beginMarker + " twice",
		},
		{
			name:     "end before begin",
			document: document(endMarker, "stale", beginMarker),
			want:     endMarker + " precedes " + beginMarker,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := splice(testCase.document, "section"); err == nil {
				t.Fatalf("splice(%q) returned no error, want %q", testCase.document, testCase.want)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("splice(%q) failed with %q, want %q", testCase.document, err, testCase.want)
			}
		})
	}
}

// The declarations the index advertises are the claims a harness acts on, so a
// prefix that is not itself a verb must not resolve: `kilasflow workflow` is not
// a command, and accepting it would let a declaration name a surface that does
// not exist.
func TestResolvesMatchesWholeVerbPathsNotPrefixes(t *testing.T) {
	tree := []verb{
		{Path: "workflow get"},
		{Path: "workflow create"},
		{Path: "context"},
	}

	for path, want := range map[string]bool{
		"workflow get":  true,
		"workflow":      false,
		"context":       true,
		"con":           false,
		"workflow list": false,
		"future verb":   false,
	} {
		if got := resolves(tree, path); got != want {
			t.Errorf("resolves(%q) = %v, want %v", path, got, want)
		}
	}
}

// generator over its own output has to be a no-op: a splice that grew the
// document by a line each time would make the freshness check flap.
func TestRenderedSectionIsAFixedPointAndNamesEveryVerb(t *testing.T) {
	tree := []verb{
		{Path: "workflow get", Summary: "read one workflow by id", Operation: "get-workflow"},
		{Path: "context", Summary: "read-only briefing", Operation: ""},
	}

	document := strings.Join([]string{
		"## Decision tree",
		"",
		beginMarker,
		endMarker,
		"",
		"### Protocol, in order",
		"",
	}, "\n")

	once, err := splice(document, render(tree))
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	twice, err := splice(once, render(tree))
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if once != twice {
		t.Fatalf("splicing twice changed the document:\n%s\n---\n%s", once, twice)
	}

	// The prose around the section survives, and both verbs are in it with the
	// operation each drives.
	for _, want := range []string{
		"### Protocol, in order",
		"kilasflow workflow get",
		"get-workflow",
		"kilasflow context",
		"local",
	} {
		if !strings.Contains(once, want) {
			t.Errorf("the generated document does not carry %q:\n%s", want, once)
		}
	}
}
