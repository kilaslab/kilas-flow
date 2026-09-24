package jsrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// HTML-like comments (ECMAScript Annex B) read as V8 reads them: `<!--`
// anywhere in code, and `-->` at the start of a line, begin a comment that
// runs to the end of the line. testdata/parity/html-comments.json is what
// Node 24 makes of each body, compiled as a script in the same wrapper.
//
// A body is analysed (the importer and a node's validation) and run, and
// both must agree with Node: a body Node runs is accepted by Analyze and
// returns Node's items; a body Node cannot parse is a syntax error in both.
func TestHTMLLikeCommentsMatchTheRecordedNodeGoldens(t *testing.T) {
	var golden struct {
		Probes []struct {
			Name  string          `json:"name"`
			Code  string          `json:"code"`
			Want  json.RawMessage `json:"want"`
			Error string          `json:"error"`
		} `json:"probes"`
	}
	loadGolden(t, "html-comments.json", &golden)
	if len(golden.Probes) == 0 {
		t.Fatal("the golden has no probes")
	}
	for _, probe := range golden.Probes {
		_, analyzed := jsrun.Analyze(probe.Code, jsrun.ModeAllItems)
		result, ran := newRunner().Run(context.Background(), jsrun.Task{Source: probe.Code, Items: numbered(1)})
		if probe.Error != "" {
			var syntax *jsrun.SyntaxError
			if !errors.As(analyzed, &syntax) || !errors.As(ran, &syntax) {
				t.Errorf("%s: Analyze() = %v, Run() = %v, want a SyntaxError from both, as Node's %s", probe.Name, analyzed, ran, probe.Error)
			}
			continue
		}
		if analyzed != nil || ran != nil {
			t.Errorf("%s: Analyze() = %v, Run() = %v, want the body accepted and run, as Node runs it", probe.Name, analyzed, ran)
			continue
		}
		got := make([]map[string]any, len(result.Items))
		for index, item := range result.Items {
			got[index] = item.JSON
		}
		if !sameJSON(got, probe.Want) {
			encoded, _ := json.Marshal(got)
			t.Errorf("%s:\n got  %s\n want %s", probe.Name, encoded, probe.Want)
		}
	}
}

// Blanking a comment moves nothing: a syntax error and a thrown error after
// one are reported on the line, and at the column, they would have without
// it.
func TestAnHTMLLikeCommentKeepsLinesAndColumns(t *testing.T) {
	for _, pair := range [][2]string{
		{"const a = 1 <!-- é ü a comment\n  const b = ;", "const a = 1 // é ü a comment\n  const b = ;"},
		{"<!-- opening\n--> closing\n  const b = ;", "// opening\n// closing\n  const b = ;"},
	} {
		_, withComment := jsrun.Analyze(pair[0], jsrun.ModeAllItems)
		_, plain := jsrun.Analyze(pair[1], jsrun.ModeAllItems)
		var got, want *jsrun.SyntaxError
		if !errors.As(withComment, &got) || !errors.As(plain, &want) {
			t.Fatalf("Analyze(%q) = %v and Analyze(%q) = %v, want syntax errors", pair[0], withComment, pair[1], plain)
		}
		if got.Line != want.Line || got.Column != want.Column || got.Line == 1 {
			t.Errorf("%q: line %d column %d, want line %d column %d as with a line comment", pair[0], got.Line, got.Column, want.Line, want.Column)
		}
	}

	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: "const a = 1 <!-- é ü\n  --> ü\n  const b = null; b.c"})
	var thrown *jsrun.ScriptError
	if !errors.As(err, &thrown) || thrown.Line != 3 || thrown.Column != 21 {
		t.Errorf("Run() = %v (%+v), want a TypeError at line 3, column 21", err, thrown)
	}
}

// When the lexer that finds the comments reads a `/` differently from goja's
// parser, it cannot be trusted with the comments either: the body is refused,
// in the one refusal sentence, never run on a guess. A function expression's
// body is a block to the lexer, so it reads `/ 2 <!--x /` as a regular
// expression, where goja divides and reads `2 < !--x / 1`, and V8 divides
// and reads a comment.
func TestAnHTMLLikeCommentTheLexerCannotReadIsRefused(t *testing.T) {
	body := "let x = 5\nconst v = function () { return 4 } / 2 <!--x / 1\nreturn [{ json: { v } }]"
	_, analyzed := jsrun.Analyze(body, jsrun.ModeAllItems)
	_, ran := newRunner().Run(context.Background(), jsrun.Task{Source: body})
	for _, err := range []error{analyzed, ran} {
		if !errors.Is(err, jsrun.ErrUnsupported) || !strings.Contains(err.Error(), "has code around a <!-- or --> that this server cannot read unambiguously") || !strings.Contains(err.Error(), "(line 2)") {
			t.Errorf("got %v, want the code around the <!-- refused on line 2", err)
		}
	}
}

// A body with no `<!--` and no `-->` that could start a line never reaches
// the lexer, and `i-->0` stays what it always was.
func TestADecrementBeforeAComparisonIsNotAComment(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "let i = 2, n = 0\nwhile (i-->0) n++\nreturn [{ json: { i, n } }]"})
	if result.Items[0].JSON["i"] != float64(-1) || result.Items[0].JSON["n"] != float64(2) {
		t.Errorf("items = %#v", result.Items)
	}
}

// A `<!--` that is no comment at all, in a string, is refused the same way
// when the code around it is what the lexer cannot read, and the refusal
// says so rather than claiming a comment.
func TestCodeAroundAnHTMLMarkerThatCannotBeReadIsRefusedAsSuch(t *testing.T) {
	body := "const s = '<!--'\nconst f = function () { return 4 } / 2 / 1\nreturn [{ json: { s, f } }]"
	_, err := jsrun.Analyze(body, jsrun.ModeAllItems)
	if !errors.Is(err, jsrun.ErrUnsupported) || !strings.Contains(err.Error(), "has code around a <!-- or --> that this server cannot read unambiguously (line 1)") {
		t.Errorf("Analyze() = %v", err)
	}
}
