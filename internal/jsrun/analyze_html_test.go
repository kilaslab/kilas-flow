package jsrun

import (
	"slices"
	"strings"
	"testing"
)

// The lexer blanks exactly the comments, byte for byte, and records the
// regular expressions it read, which is what the parser's are held to.
func TestHTMLCommentsBlankOnlyTheCommentsByteForByte(t *testing.T) {
	for _, test := range []struct {
		source, blanked string
		regexps         [][2]int
	}{
		{source: "a <!-- é\nb", blanked: "a        \nb"},
		{source: "--> x\n  --> y\r\nz --> 1", blanked: "     \n       \r\nz --> 1"},
		{source: "/* a\n */ --> x", blanked: "/* a\n */      "},
		{source: "'<!--' + \"-->\" + `<!--\n-->`", blanked: "'<!--' + \"-->\" + `<!--\n-->`"},
		{source: "`${ 1 <!-- x\n}`", blanked: "`${ 1       \n}`"},
		{source: "x = /<!--/g", blanked: "x = /<!--/g", regexps: [][2]int{{4, 11}}},
		{source: "if (a) /[/]<!--/.test(b)", blanked: "if (a) /[/]<!--/.test(b)", regexps: [][2]int{{7, 16}}},
		{source: "a / b <!-- / c", blanked: "a / b         "},
		{source: "a <<!--b", blanked: "a <<!--b"},
		{source: "a: {}\n/<!--/", blanked: "a: {}\n/<!--/", regexps: [][2]int{{6, 12}}},
		{source: "x = c ? {} / 2 <!-- y", blanked: "x = c ? {} / 2       "},
		{source: "a <!-- x\u2028b", blanked: "a       \u2028b"},
		{source: "// <!-- \n/* --> */", blanked: "// <!-- \n/* --> */"},
	} {
		scan := htmlComments(test.source)
		if scan.blanked != test.blanked {
			t.Errorf("htmlComments(%q) = %q, want %q", test.source, scan.blanked, test.blanked)
		}
		if len(scan.blanked) != len(test.source) || strings.Count(scan.blanked, "\n") != strings.Count(test.source, "\n") {
			t.Errorf("htmlComments(%q) moved text: %q", test.source, scan.blanked)
		}
		if !slices.Equal(scan.regexps, test.regexps) {
			t.Errorf("htmlComments(%q) read regular expressions at %v, want %v", test.source, scan.regexps, test.regexps)
		}
	}
}

// Only a body that could hold a comment is lexed at all.
func TestOnlyABodyThatCouldHoldAnHTMLCommentIsLexed(t *testing.T) {
	for source, want := range map[string]bool{
		"return items":            false,
		"while (i-->0) n++":       false,
		"x = a <!-- b":            true,
		"  --> x":                 true,
		"a\n\t--> x":              true,
		"a\r\n--> x":              true,
		"/* a */ --> x":           true,
		"a /* b */ c --> x":       false,
		"const s = '<!--'":        true,
		"a\u2028 --> x":           true,
		"x = y\n  z-->0":          false,
		"return [{ json: {} }]\n": false,
	} {
		if got := mayHoldHTMLComment(source); got != want {
			t.Errorf("mayHoldHTMLComment(%q) = %v, want %v", source, got, want)
		}
	}
}
