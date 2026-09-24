package jsrun

import "strings"

// wrapperVersion names the wrapper's shape. It is part of every compiled
// program's cache key, so changing the wrapper cannot run a stale program.
const wrapperVersion = "jsrun-3"

// sourceName is the file name the user's code has in stack traces and
// positions. Frames under any other name belong to the runtime or a library.
const sourceName = "Code"

// modeRoots are the roots that change from call to call, per mode. They are
// the wrapper function's parameters, so they live in a scope that encloses
// the user's body: `const items = $input.all()`, the commonest first line of
// an n8n body, is then a legal redeclaration, where it would be a syntax
// error if the roots shared the body's own scope. Roots that do not apply to
// a mode are not parameters at all; they fall through to globals that fail
// with their name.
//
// They are plain positional parameters. A destructuring parameter list makes
// goja panic on a direct eval() inside the body, which is a goja bug the
// wrapper steps around.
//
// The compiled wrapper has one parameter more than its text: the runtime's
// reword, added to the parsed tree under a name no source can spell (see
// instrumentCatches), and passed last by runtime.js.
//
// A comparator sees the all-items roots: n8n hands its comparator the whole
// list as `items`.
var modeRoots = map[Mode][]string{
	ModeAllItems:   {"items", "$input"},
	ModeEachItem:   {"$json", "$itemIndex", "$input"},
	ModeComparator: {"items", "$input"},
}

// wrapped is a body inside the function that runs it.
//
// The body becomes an async function, so `await` at its top level works and
// a returned promise is awaited, as n8n documents. It is called with the
// wrapper's `this`, which is how `this.helpers` reaches it. The prelude is
// exactly one line and the trailer starts a new one, so the user's line N is
// line N+1 of the wrapped text and columns are unchanged.
//
// A comparator's wrapper returns the comparator itself, a plain function of
// a and b: n8n calls it from a synchronous sort, so `await` in it is a syntax
// error there too. The line rule is the same.
type wrapped struct {
	text string
	// comparator says the body is a Sort comparator's, in its own wrapper.
	comparator bool
	// open and close are the byte offsets of the braces that delimit the
	// body. The analyser proves the parsed body spans exactly these, which is
	// what stops code from closing its wrapper and running outside it.
	open, close int
	// outerClose is the byte offset of the wrapper function's own closing
	// brace.
	outerClose int
	// lines is the number of lines in the user's code.
	lines int
}

func wrap(source string, mode Mode) wrapped {
	mode = mode.orDefault()
	parameters := strings.Join(modeRoots[mode], ", ")
	prelude, trailer := "(function ("+parameters+") { return (async function () {\n", "\n}).call(this); })"
	if mode == ModeComparator {
		prelude, trailer = "(function ("+parameters+") { return function (a, b) {\n", "\n}; })"
	}
	text := prelude + source + trailer
	return wrapped{
		text:       text,
		comparator: mode == ModeComparator,
		open:       len(prelude) - 2,
		close:      len(prelude) + len(source) + 1,
		outerClose: len(text) - 2,
		lines:      strings.Count(source, "\n") + 1,
	}
}

// bodyStart is the offset of the user's code in the wrapped text.
func (w wrapped) bodyStart() int { return w.open + 2 }

// body is the user's code as the wrapper holds it.
func (w wrapped) body() string { return w.text[w.bodyStart() : w.close-1] }

// userLine turns a line of the wrapped text into the user's line. A position
// on the trailer is reported at the code's last line, and one on the prelude
// as unknown.
func (w wrapped) userLine(line int) int {
	switch {
	case line <= 1:
		return 0
	case line-1 > w.lines:
		return w.lines
	}
	return line - 1
}

// lineAt is the user's line for a byte offset into the wrapped text.
func (w wrapped) lineAt(offset int) int {
	if offset < 0 || offset > len(w.text) {
		return 0
	}
	return w.userLine(strings.Count(w.text[:offset], "\n") + 1)
}
